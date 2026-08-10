package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"uploadserver/internal"
)

// Run starts the web server by loading config and initializing routes.
func Run() (err error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return fmt.Errorf("create upload dir %q: %w", cfg.Dir, err)
	}
	if err := internal.CheckWritable(cfg.Dir); err != nil {
		return fmt.Errorf("upload dir %q not writable: %w", cfg.Dir, err)
	}

	store, err := internal.OpenStore(cfg.StorePath)
	if err != nil {
		return fmt.Errorf("open token store: %w", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	secret, created, err := store.Bootstrap()
	if err != nil {
		return fmt.Errorf("bootstrap token: %w", err)
	}

	fileIndex, err := internal.BuildFileIndex(store)
	if err != nil {
		return fmt.Errorf("build file index: %w", err)
	}
	slog.Info("indexed files across all tokens", "count", fileIndex.Count())

	srv := &server{cfg: cfg, store: store, fileIndex: fileIndex}
	srv.announce(secret, created)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64 KiB
	}

	// Start background invite scheduler for periodic giveaways and pending grants.
	inviteSched := internal.NewInviteScheduler(store)
	inviteSched.Start()
	defer inviteSched.Stop()

	// Start background purge scheduler for executing scheduled media purges.
	purgeSched := internal.NewPurgeScheduler(store, func(tokenID string) error {
		return srv.purgeUserMedia(tokenID, true)
	})
	purgeSched.Start()
	defer purgeSched.Stop()

	// Start control socket IPC server for live CLI commands
	ipcServer, err := internal.StartIPCServer(cfg.StorePath, store, cfg)
	if err != nil {
		slog.Warn("failed to start control socket ipc server", "error", err)
	} else if ipcServer != nil {
		defer func() { _ = ipcServer.Close() }()
		slog.Info("control socket listening", "path", ipcServer.SocketPath())
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "dir", cfg.Dir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		stop() // restore default handling so a second signal force-quits
		slog.Info("shutting down")
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}
