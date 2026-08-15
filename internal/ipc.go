package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IPCRequest represents a command request sent over the control socket.
type IPCRequest struct {
	Args []string `json:"args"`
}

// IPCEvent represents a streaming event sent back to the client.
type IPCEvent struct {
	Type  string `json:"type"`            // "stdout", "stderr", "exit"
	Data  string `json:"data,omitempty"`  // chunk of output for stdout/stderr
	Code  int    `json:"code,omitempty"`  // exit code for "exit" type
	Error string `json:"error,omitempty"` // error message for "exit" type
}

// SocketPath returns the configured or default control socket path.
// If CONTROL_SOCKET is "off", "none", "false", or "0", it returns an empty string (IPC disabled).
func SocketPath(storePath string) string {
	if sock := os.Getenv("CONTROL_SOCKET"); sock != "" {
		s := strings.TrimSpace(strings.ToLower(sock))
		if s == "off" || s == "none" || s == "false" || s == "0" {
			return ""
		}
		return sock
	}
	if storePath == "" {
		return ""
	}
	dir := filepath.Dir(storePath)
	if dir == "" || dir == "." {
		return "control.sock"
	}
	return filepath.Join(dir, "control.sock")
}

// IPCServer manages the control socket listener and command dispatching.
type IPCServer struct {
	listener net.Listener
	sockPath string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	store    *TokenStore
	index    IndexUpdater
	cfg      Config
}

// StartIPCServer initializes and starts listening on the control socket for the given storePath.
// If IPC is disabled, it returns (nil, nil).
func StartIPCServer(storePath string, store *TokenStore, index IndexUpdater, cfg Config) (*IPCServer, error) {
	sockPath := SocketPath(storePath)
	if sockPath == "" {
		return nil, nil
	}

	if dir := filepath.Dir(sockPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create socket dir: %w", err)
		}
	}

	// Detect stale socket: if file exists, test if another active server is listening.
	if _, err := os.Stat(sockPath); err == nil {
		conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("control socket %s is already in use by a running instance", sockPath)
		}
		// Connection failed; socket is stale from a previous crash/run.
		_ = os.Remove(sockPath)
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen on control socket %s: %w", sockPath, err)
	}

	_ = os.Chmod(sockPath, 0o600) // Restrict socket to owner permissions

	ctx, cancel := context.WithCancel(context.Background())
	server := &IPCServer{
		listener: listener,
		sockPath: sockPath,
		ctx:      ctx,
		cancel:   cancel,
		store:    store,
		index:    index,
		cfg:      cfg,
	}

	server.wg.Add(1)
	go server.serve()

	return server, nil
}

// SocketPath returns the path of the control socket.
func (s *IPCServer) SocketPath() string {
	if s == nil {
		return ""
	}
	return s.sockPath
}

// Close gracefully terminates the IPC listener and removes the socket file.
func (s *IPCServer) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	s.cancel()
	err := s.listener.Close()
	s.wg.Wait()
	_ = os.Remove(s.sockPath)
	return err
}

func (s *IPCServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				slog.Debug("ipc accept error", "error", err)
				continue
			}
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() { _ = c.Close() }()
			s.handleConn(c)
		}(conn)
	}
}

func (s *IPCServer) handleConn(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	dec := json.NewDecoder(io.LimitReader(conn, 1<<20)) // 1 MiB limit for request
	var req IPCRequest
	if err := dec.Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(IPCEvent{
			Type:  "exit",
			Code:  1,
			Error: fmt.Sprintf("invalid request: %v", err),
		})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	enc := json.NewEncoder(conn)
	var mu sync.Mutex

	stdoutWriter := &ipcStreamWriter{enc: enc, mu: &mu, eventType: "stdout"}
	stderrWriter := &ipcStreamWriter{enc: enc, mu: &mu, eventType: "stderr"}

	uploadDir := s.cfg.Dir
	if uploadDir == "" {
		uploadDir = Env("UPLOAD_DIR", "./data")
	}

	execCtx := ExecutionContext{
		Store:     s.store,
		Index:     s.index,
		UploadDir: uploadDir,
		Stdout:    stdoutWriter,
		Stderr:    stderrWriter,
		IsIPC:     true,
	}

	err := RunCommand(execCtx, req.Args)
	exitCode := 0
	errMsg := ""
	if err != nil {
		exitCode = 1
		errMsg = err.Error()
	}

	mu.Lock()
	_ = enc.Encode(IPCEvent{
		Type:  "exit",
		Code:  exitCode,
		Error: errMsg,
	})
	mu.Unlock()
}

type ipcStreamWriter struct {
	enc       *json.Encoder
	mu        *sync.Mutex
	eventType string
}

func (w *ipcStreamWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	err = w.enc.Encode(IPCEvent{
		Type: w.eventType,
		Data: string(p),
	})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// SendIPCCommand attempts to connect to the control socket and execute the command remotely.
// Returns (handled bool, err error).
// If handled is false, the caller should fall back to offline direct execution.
func SendIPCCommand(sockPath string, args []string, stdout, stderr io.Writer) (bool, error) {
	if sockPath == "" {
		return false, nil
	}

	if _, err := os.Stat(sockPath); err != nil {
		return false, nil // Socket file does not exist
	}

	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return false, nil // Socket is stale or server not reachable
	}
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(IPCRequest{Args: args}); err != nil {
		return true, fmt.Errorf("send ipc request: %w", err)
	}

	dec := json.NewDecoder(conn)
	var exitErr error
	for {
		var ev IPCEvent
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return true, fmt.Errorf("read ipc response: %w", err)
		}
		switch ev.Type {
		case "stdout":
			if stdout != nil {
				_, _ = io.WriteString(stdout, ev.Data)
			}
		case "stderr":
			if stderr != nil {
				_, _ = io.WriteString(stderr, ev.Data)
			}
		case "exit":
			if ev.Error != "" {
				exitErr = errors.New(ev.Error)
			} else if ev.Code != 0 {
				exitErr = fmt.Errorf("command failed with exit code %d", ev.Code)
			}
			return true, exitErr
		}
	}

	return true, exitErr
}
