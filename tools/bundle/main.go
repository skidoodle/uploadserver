package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
)

func findStaticDir() string {
	candidates := []string{
		"static",
		filepath.Join("internal", "web", "static"),
		filepath.Join("..", "static"),
		filepath.Join("..", "web", "static"),
		filepath.Join("..", "..", "internal", "web", "static"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return filepath.Join("internal", "web", "static")
}

func main() {
	staticDir := findStaticDir()
	if len(os.Args) > 1 {
		staticDir = os.Args[1]
	}

	fmt.Printf("==> Bundling static assets using esbuild Go API in %s\n", staticDir)

	entries, err := os.ReadDir(staticDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading static dir: %v\n", err)
		os.Exit(1)
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "shared" || entry.Name() == "dist" {
			continue
		}

		domain := entry.Name()
		domainDir := filepath.Join(staticDir, domain)

		// Bundle CSS
		cssEntry := filepath.Join(domainDir, "css", domain+".css")
		if _, err := os.Stat(cssEntry); err == nil {
			outFile := filepath.Join(domainDir, "css", domain+".bundle.css")
			fmt.Printf("    Bundling CSS: %s -> %s\n", cssEntry, outFile)

			result := api.Build(api.BuildOptions{
				EntryPoints: []string{cssEntry},
				Bundle:      true,
				Write:       true,
				Outfile:     outFile,
				Loader: map[string]api.Loader{
					".svg": api.LoaderDataURL,
					".png": api.LoaderDataURL,
				},
				MinifyWhitespace:  false,
				MinifyIdentifiers: false,
				MinifySyntax:      false,
			})

			if len(result.Errors) > 0 {
				for _, msg := range result.Errors {
					fmt.Fprintf(os.Stderr, "Error bundling CSS %s: %s\n", cssEntry, msg.Text)
				}
				os.Exit(1)
			}
		}

		// Bundle JS
		jsEntry := filepath.Join(domainDir, "js", domain+".js")
		if _, err := os.Stat(jsEntry); err == nil {
			outFile := filepath.Join(domainDir, "js", domain+".bundle.js")
			fmt.Printf("    Bundling JS:  %s -> %s\n", jsEntry, outFile)

			result := api.Build(api.BuildOptions{
				EntryPoints:       []string{jsEntry},
				Bundle:            true,
				Format:            api.FormatESModule,
				Write:             true,
				Outfile:           outFile,
				MinifyWhitespace:  false,
				MinifyIdentifiers: false,
				MinifySyntax:      false,
			})

			if len(result.Errors) > 0 {
				for _, msg := range result.Errors {
					fmt.Fprintf(os.Stderr, "Error bundling JS %s: %s\n", jsEntry, msg.Text)
				}
				os.Exit(1)
			}
		}
	}

	fmt.Println("==> Asset bundling complete!")
}
