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

	outputDir := *dirFlag
	count := *countFlag
	hexLen := *lenFlag
	width, height := *widthFlag, *heightFlag

	cleanOutputDir := filepath.Clean(outputDir)
	if err := os.MkdirAll(cleanOutputDir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", cleanOutputDir, err)
		os.Exit(1)
	}

	start := time.Now()
	fmt.Printf("Generating %d test image(s) in %q (hex length: %d)...\n", count, cleanOutputDir, hexLen)

	numWorkers := min(count, 16)

	jobs := make(chan int, count)
	var wg sync.WaitGroup
	var createdCount atomic.Int64

	for range numWorkers {
		wg.Go(func() {

			for range jobs {
				img := image.NewRGBA(image.Rect(0, 0, width, height))

				var rBuf [4]byte
				_, _ = rand.Read(rBuf[:])
				fillColor := color.RGBA{
					R: rBuf[0],
					G: rBuf[1],
					B: rBuf[2],
					A: 255,
				}

				for x := range width {
					for y := range height {
						img.Set(x, y, fillColor)
					}
				}

				ext := strings.ToLower(strings.TrimPrefix(*extFlag, "."))
				if ext == "random" || ext == "" {
					ext = commonExts[int(rBuf[3])%len(commonExts)]
				}

				baseName := randomHex(hexLen)
				fileName := filepath.Join(cleanOutputDir, filepath.Base(baseName+"."+ext))

				file, err := os.Create(fileName) // #nosec G304 -- Test helper creating mock files in cleaned output dir
				if err != nil {
					continue
				}

				switch ext {
				case "png":
					_ = png.Encode(file, img)
				default:
					_ = jpeg.Encode(file, img, &jpeg.Options{Quality: 80})
				}
				_ = file.Close()
				createdCount.Add(1)
			}
		})
	}

	for i := 1; i <= count; i++ {
		jobs <- i
	}
	close(jobs)

	wg.Wait()

	fmt.Printf("Done! Generated %d files in %v.\n", createdCount.Load(), time.Since(start).Round(time.Millisecond))
	fmt.Printf("Run 'uploadserver scan' to find and import these untracked files.\n")
}
