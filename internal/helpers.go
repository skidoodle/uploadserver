package internal

import (
	"os"
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
