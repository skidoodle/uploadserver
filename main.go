package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
		} else if strings.HasPrefix(addr, "0.0.0.0:") {
			addr = "localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
		}
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			log.Fatalf("healthcheck query failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Fatalf("healthcheck failed with status %d", resp.StatusCode)
		}
		os.Exit(0)
	case "list", "add", "rm", "disable", "enable", "limit", "global", "dump", "reset":
		if err := internal.RunTokenCLI(os.Args[1:]); err != nil {
			log.Fatal(err)
		}
	default:
		log.Printf("unknown command %q", os.Args[1])
		fmt.Println(internal.CLIUsage)
		os.Exit(1)
	}
}
