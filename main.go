package main

import (
	"fmt"
	"log"
	"os"
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
