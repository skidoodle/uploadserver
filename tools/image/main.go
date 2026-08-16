package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var commonExts = []string{"jpg", "png", "webp", "avif"}

// randomHex generates a random hex string of specified length matching uploadserver's naming convention.
func randomHex(length int) string {
	numBytes := (length + 1) / 2
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)[:length]
}

func main() {
	uploadDir := os.Getenv("UPLOAD_DIR")
	if uploadDir == "" {
		uploadDir = "./data"
	}

	defaultLen := 8
	if n, err := strconv.Atoi(os.Getenv("RANDOM_NAME_LENGTH")); err == nil && n > 0 {
		defaultLen = n
	}

	dirFlag := flag.String("dir", uploadDir, "Output directory for generated files (defaults to UPLOAD_DIR or ./data)")
	countFlag := flag.Int("count", 50, "Number of test images to generate")
	lenFlag := flag.Int("length", defaultLen, "Filename random hex length (defaults to RANDOM_NAME_LENGTH or 8)")
	extFlag := flag.String("ext", "random", "File extension ('jpg', 'png', or 'random')")
	widthFlag := flag.Int("width", 300, "Image width in pixels")
	heightFlag := flag.Int("height", 300, "Image height in pixels")
	flag.Parse()

	targetDir := filepath.Clean(*dirFlag)
	if err := os.MkdirAll(targetDir, 0750); err != nil { // #nosec G301 G703 -- Directory permission restricted to 0750 for test mock data
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generating %d mock test images in '%s'...\n", *countFlag, targetDir)
	start := time.Now()

	var wg sync.WaitGroup
	var completed atomic.Int64
	numWorkers := 8
	jobs := make(chan int, *countFlag)

	for range numWorkers {
		wg.Go(func() {
			for range jobs {
				name := randomHex(*lenFlag)

				var chosenExt string
				if *extFlag == "random" {
					b := make([]byte, 1)
					_, _ = rand.Read(b)
					chosenExt = commonExts[int(b[0])%len(commonExts)]
				} else {
					chosenExt = strings.ToLower(*extFlag)
				}

				filename := fmt.Sprintf("%s.%s", name, chosenExt)
				filePath := filepath.Clean(filepath.Join(targetDir, filename))

				img := image.NewRGBA(image.Rect(0, 0, *widthFlag, *heightFlag))
				b := make([]byte, 3)
				_, _ = rand.Read(b)
				c := color.RGBA{R: b[0], G: b[1], B: b[2], A: 255}

				for y := 0; y < *heightFlag; y++ {
					for x := 0; x < *widthFlag; x++ {
						img.Set(x, y, c)
					}
				}

				f, err := os.Create(filePath) // #nosec G304 G703 -- Safe test mock image creation
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", filePath, err)
					continue
				}

				switch chosenExt {
				case "png":
					_ = png.Encode(f, img)
				default:
					_ = jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
				}
				_ = f.Close()

				completed.Add(1)
			}
		})
	}

	for i := 0; i < *countFlag; i++ {
		jobs <- i
	}
	close(jobs)

	wg.Wait()
	fmt.Printf("Done! Generated %d files in %v.\n", completed.Load(), time.Since(start).Round(time.Millisecond))
}
