package internal

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIPC_SocketPath(t *testing.T) {
	t.Run("Default Socket Path", func(t *testing.T) {
		t.Setenv("CONTROL_SOCKET", "")
		storePath := filepath.Join("var", "data", "tokens.db")
		got := SocketPath(storePath)
		want := filepath.Join("var", "data", "control.sock")
		if got != want {
			t.Errorf("SocketPath(%q) = %q; want %q", storePath, got, want)
		}
	})

	t.Run("Custom Socket Path", func(t *testing.T) {
		custom := filepath.Join(os.TempDir(), "custom.sock")
		t.Setenv("CONTROL_SOCKET", custom)
		got := SocketPath("tokens.db")
		if got != custom {
			t.Errorf("SocketPath() = %q; want %q", got, custom)
		}
	})

	t.Run("Disabled IPC Socket", func(t *testing.T) {
		for _, v := range []string{"off", "none", "false", "0", "OFF", "FALSE"} {
			t.Setenv("CONTROL_SOCKET", v)
			if got := SocketPath("tokens.db"); got != "" {
				t.Errorf("SocketPath() with CONTROL_SOCKET=%q = %q; want empty string", v, got)
			}
		}
	})

	t.Run("Empty Store Path", func(t *testing.T) {
		t.Setenv("CONTROL_SOCKET", "")
		if got := SocketPath(""); got != "" {
			t.Errorf("SocketPath(\"\") = %q; want empty string", got)
		}
	})
}

func TestIPC_ServerLifecycleAndStaleSocket(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.db")
	sockPath := filepath.Join(dir, "control.sock")
	t.Setenv("CONTROL_SOCKET", sockPath)

	store, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := Config{Dir: dir, StorePath: storePath}

	// 1. Start Server
	srv, err := StartIPCServer(storePath, store, cfg)
	if err != nil {
		t.Fatalf("StartIPCServer error: %v", err)
	}
	if srv.SocketPath() != sockPath {
		t.Errorf("srv.SocketPath() = %q; want %q", srv.SocketPath(), sockPath)
	}

	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("expected socket file %s to exist: %v", sockPath, err)
	}

	// 2. Starting another server on same socket should fail
	_, err = StartIPCServer(storePath, store, cfg)
	if err == nil {
		t.Fatal("expected error starting second server on same socket, got nil")
	}

	// 3. Graceful close should remove socket file
	if err := srv.Close(); err != nil {
		t.Fatalf("srv.Close error: %v", err)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("expected socket file to be removed after Close(), stat err: %v", err)
	}

	// 4. Stale socket recovery: create dummy socket file without active listener
	if err := os.WriteFile(sockPath, []byte("stale-socket"), 0o600); err != nil {
		t.Fatalf("failed to write stale socket file: %v", err)
	}
	srv2, err := StartIPCServer(storePath, store, cfg)
	if err != nil {
		t.Fatalf("StartIPCServer failed to clean up stale socket: %v", err)
	}
	_ = srv2.Close()
}

func TestIPC_AllSubcommandsOverIPC(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.db")
	sockPath := filepath.Join(dir, "control.sock")
	uploadDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o750); err != nil {
		t.Fatalf("mkdir uploadDir error: %v", err)
	}

	t.Setenv("CONTROL_SOCKET", sockPath)
	t.Setenv("TOKEN_STORE", storePath)
	t.Setenv("UPLOAD_DIR", uploadDir)

	store, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := Config{Dir: uploadDir, StorePath: storePath}
	srv, err := StartIPCServer(storePath, store, cfg)
	if err != nil {
		t.Fatalf("StartIPCServer error: %v", err)
	}
	defer func() { _ = srv.Close() }()

	runIPC := func(args ...string) (stdout, stderr string, err error) {
		var outBuf, errBuf bytes.Buffer
		handled, runErr := SendIPCCommand(sockPath, args, &outBuf, &errBuf)
		if !handled {
			return "", "", fmt.Errorf("command %v was not handled over IPC", args)
		}
		return outBuf.String(), errBuf.String(), runErr
	}

	// 1. Version
	out, _, err := runIPC("version")
	if err != nil || !strings.Contains(out, "uploadserver") {
		t.Fatalf("IPC version failed: err=%v out=%s", err, out)
	}

	// 2. Add Token
	out, _, err = runIPC("add", "--label", "ipc-user", "--role", RoleUpload)
	if err != nil {
		t.Fatalf("IPC add failed: %v", err)
	}
	if !strings.Contains(out, "created upload token") {
		t.Fatalf("unexpected add output: %s", out)
	}

	// Read token ID from store
	tokens := store.List()
	if len(tokens) == 0 {
		t.Fatal("expected at least 1 token in store")
	}
	tokenID := tokens[0].ID

	// 3. Info
	out, _, err = runIPC("info", tokenID)
	if err != nil || !strings.Contains(out, "Token ID:    "+tokenID) {
		t.Fatalf("IPC info failed: err=%v out=%s", err, out)
	}

	// 4. List
	out, _, err = runIPC("list")
	if err != nil || !strings.Contains(out, tokenID) {
		t.Fatalf("IPC list failed: err=%v out=%s", err, out)
	}

	// 5. Limit and Global
	out, _, err = runIPC("limit", tokenID, "--total-size", "1GB", "--bypass")
	if err != nil || !strings.Contains(out, "quotas for "+tokenID) {
		t.Fatalf("IPC limit failed: err=%v out=%s", err, out)
	}

	out, _, err = runIPC("global", "--monthly-size", "2GB")
	if err != nil || !strings.Contains(out, "global default: 2 GB/mo") {
		t.Fatalf("IPC global failed: err=%v out=%s", err, out)
	}

	// 6. Disable and Enable
	_, _, err = runIPC("disable", tokenID)
	if err != nil {
		t.Fatalf("IPC disable failed: %v", err)
	}
	rec, _ := store.GetRecord(tokenID)
	if !rec.Disabled {
		t.Errorf("expected token to be disabled")
	}

	_, _, err = runIPC("enable", tokenID)
	if err != nil {
		t.Fatalf("IPC enable failed: %v", err)
	}
	rec, _ = store.GetRecord(tokenID)
	if rec.Disabled {
		t.Errorf("expected token to be enabled")
	}

	// 7. Untracked file Scan & Import over IPC
	testFile := filepath.Join(uploadDir, "untracked.png")
	if err := os.WriteFile(testFile, []byte("image-data"), 0o600); err != nil {
		t.Fatalf("create untracked file error: %v", err)
	}

	out, _, err = runIPC("scan")
	if err != nil || !strings.Contains(out, "untracked.png") {
		t.Fatalf("IPC scan dry-run failed: err=%v out=%s", err, out)
	}

	out, _, err = runIPC("scan", "--token", tokenID)
	if err != nil || !strings.Contains(out, "imported 1 file(s)") {
		t.Fatalf("IPC scan import failed: err=%v out=%s", err, out)
	}

	// 8. Prune over IPC
	out, _, err = runIPC("prune", "--dry-run")
	if err != nil || !strings.Contains(out, "no temporary files") {
		t.Fatalf("IPC prune failed: err=%v out=%s", err, out)
	}

	// 9. Export and Import over IPC
	exportFile := filepath.Join(dir, "export.json")
	out, _, err = runIPC("export", "--out", exportFile)
	if err != nil || !strings.Contains(out, "exported") {
		t.Fatalf("IPC export failed: err=%v out=%s", err, out)
	}

	out, _, err = runIPC("import", "--in", exportFile)
	if err != nil || !strings.Contains(out, "imported global quota") {
		t.Fatalf("IPC import failed: err=%v out=%s", err, out)
	}

	// 10. Dump over IPC
	out, _, err = runIPC("dump")
	if err != nil || !strings.Contains(out, tokenID) {
		t.Fatalf("IPC dump failed: err=%v out=%s", err, out)
	}

	// 11. Migrate dry-run over IPC
	out, _, err = runIPC("migrate", "--token", tokenID, "--dry-run")
	if err != nil {
		t.Fatalf("IPC migrate dry-run failed: err=%v out=%s", err, out)
	}

	// 12. Reset rejected while server is running over IPC
	_, _, err = runIPC("reset")
	if err == nil {
		t.Fatal("expected error resetting database while server is running, got nil")
	}

	// 13. Remove token
	_, _, err = runIPC("rm", tokenID)
	if err != nil {
		t.Fatalf("IPC rm failed: %v", err)
	}
	if _, ok := store.GetRecord(tokenID); ok {
		t.Errorf("expected token %s to be deleted", tokenID)
	}
}

func TestIPC_ConcurrentRequests(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.db")
	sockPath := filepath.Join(dir, "control.sock")
	t.Setenv("CONTROL_SOCKET", sockPath)

	store, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	srv, err := StartIPCServer(storePath, store, Config{Dir: dir, StorePath: storePath})
	if err != nil {
		t.Fatalf("StartIPCServer error: %v", err)
	}
	defer func() { _ = srv.Close() }()

	var wg sync.WaitGroup
	workers := 10
	errCh := make(chan error, workers*3)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var stdout, stderr bytes.Buffer

			// 1. Add
			handled, err := SendIPCCommand(sockPath, []string{"add", "--label", fmt.Sprintf("worker-%d", idx)}, &stdout, &stderr)
			if !handled || err != nil {
				errCh <- fmt.Errorf("worker %d add error: %v", idx, err)
				return
			}

			// 2. List
			stdout.Reset()
			handled, err = SendIPCCommand(sockPath, []string{"list"}, &stdout, &stderr)
			if !handled || err != nil {
				errCh <- fmt.Errorf("worker %d list error: %v", idx, err)
				return
			}

			// 3. Version
			stdout.Reset()
			handled, err = SendIPCCommand(sockPath, []string{"version"}, &stdout, &stderr)
			if !handled || err != nil {
				errCh <- fmt.Errorf("worker %d version error: %v", idx, err)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent IPC failure: %v", err)
	}
}

func TestIPC_MalformedAndShortRequests(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.db")
	sockPath := filepath.Join(dir, "control.sock")
	t.Setenv("CONTROL_SOCKET", sockPath)

	store, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	srv, err := StartIPCServer(storePath, store, Config{Dir: dir, StorePath: storePath})
	if err != nil {
		t.Fatalf("StartIPCServer error: %v", err)
	}
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to dial socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send non-JSON junk
	_, _ = conn.Write([]byte("not-json-content\n"))

	var buf [512]byte
	n, _ := conn.Read(buf[:])
	resp := string(buf[:n])
	if !strings.Contains(resp, `"type":"exit"`) || !strings.Contains(resp, `"code":1`) {
		t.Errorf("expected exit with error response for malformed request, got: %s", resp)
	}
}
