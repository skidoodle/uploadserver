package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uploadserver/internal"
	"uploadserver/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	args := os.Args[1:]
	base := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
	if base != "uploadserver" && base != "main" {
		args = append([]string{base}, args...)
	}

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Println(internal.CLIUsage)
		return
	}

	if args[0] == "-v" || args[0] == "--version" {
		fmt.Println(internal.VersionString())
		return
	}

	switch args[0] {
	case "run":
		if err := web.Run(); err != nil {
			slog.Error("server run error", "error", err)
			os.Exit(1)
		}
	case "healthcheck":
		addr := os.Getenv("LISTEN_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		// If listening on all interfaces (e.g. ":8080" or "0.0.0.0:8080"), request localhost.
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		} else if after, ok := strings.CutPrefix(addr, "0.0.0.0:"); ok {
			addr = "localhost:" + after
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil) // #nosec G704 -- Local healthcheck query to configured LISTEN_ADDR
		if err != nil {
			slog.Error("healthcheck request creation failed", "error", err)
			os.Exit(1)
		}
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req) // #nosec G704 -- Local healthcheck query
		if err != nil {
			slog.Error("healthcheck query failed", "error", err)
			os.Exit(1)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			slog.Error("healthcheck failed", "status", resp.StatusCode)
			os.Exit(1)
		}
		os.Exit(0)
	case "list", "add", "rm", "disable", "enable", "limit", "global", "scan", "dump", "reset", "info", "prune", "export", "import", "migrate", "version":
		if err := internal.RunTokenCLI(args); err != nil {
			slog.Error("cli command error", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("unknown command", "command", internal.SanitizeLog(args[0]))
		fmt.Println(internal.CLIUsage)
		os.Exit(1)
	}
}
