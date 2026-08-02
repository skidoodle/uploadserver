package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"uploadserver/internal"
	"uploadserver/internal/web"
)

func main() {
	log.SetFlags(0) // logs go to stderr; let the container runtime add timestamps
	log.SetPrefix("uploadserver: ")

	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Println(internal.CLIUsage)
		return
	}

	switch os.Args[1] {
	case "run":
		if err := web.Run(); err != nil {
			log.Fatal(err)
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
			log.Fatalf("healthcheck request creation failed: %v", err)
		}
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req) // #nosec G704 -- Local healthcheck query
		if err != nil {
			log.Fatalf("healthcheck query failed: %v", err)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			log.Fatalf("healthcheck failed with status %d", resp.StatusCode)
		}
		os.Exit(0)
	case "list", "add", "rm", "disable", "enable", "limit", "global", "scan", "dump", "reset", "info", "prune", "export", "import", "version":
		if err := internal.RunTokenCLI(os.Args[1:]); err != nil {
			log.Fatal(err)
		}
	default:
		log.Printf("unknown command %q", internal.SanitizeLog(os.Args[1])) // #nosec G706 -- Sanitized via internal.SanitizeLog
		fmt.Println(internal.CLIUsage)
		os.Exit(1)
	}
}
