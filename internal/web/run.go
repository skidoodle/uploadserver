package web

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
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

	srv := &server{cfg: cfg, store: store}
	srv.announce(secret, created)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64 KiB
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s, storing uploads in %s", cfg.Addr, cfg.Dir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		stop() // restore default handling so a second signal force-quits
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}
