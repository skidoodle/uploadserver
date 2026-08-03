package internal

import (
	"os"
	"strings"
	"time"
	_ "time/tzdata"
)

// Env returns the environment variable value or the default value.
func Env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// GetLocation returns the configured time.Location based on the TZ or TIMEZONE environment variable.
// If neither is set or if loading fails, it defaults to time.Local.
func GetLocation() *time.Location {
	tz := Env("TZ", Env("TIMEZONE", ""))
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.Local
}

// ToLocalTime converts a time to the configured timezone (or system local time).
func ToLocalTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(GetLocation())
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
