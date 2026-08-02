package internal

import (
	"os"
	"strings"
)

// Env returns the environment variable value or the default value.
func Env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// CheckWritable verifies the directory accepts writes, failing fast at startup
// rather than on the first upload.
func CheckWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// SanitizeLog removes newlines and control characters from strings to prevent log injection.
func SanitizeLog(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		if r < 32 && r != '\t' {
			return -1
		}
		return r
	}, s)
}
