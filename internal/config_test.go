package internal

import (
	"os"
	"strconv"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Clear relevant env vars to test default fallback logic
	envVars := []string{
		"LISTEN_ADDR", "UPLOAD_DIR", "BASE_URL", "UPLOAD_FIELD",
		"TOKEN_STORE", "ENABLE_ADMIN", "STRIP_EXTENSION", "SERVE_FILES",
		"RANDOM_NAME_LENGTH", "MAX_UPLOAD_BYTES",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() default error: %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Errorf("cfg.Addr = %q; want %q", cfg.Addr, ":8080")
	}
	if cfg.Dir != "./data" {
		t.Errorf("cfg.Dir = %q; want %q", cfg.Dir, "./data")
	}
	if cfg.Field != "file" {
		t.Errorf("cfg.Field = %q; want %q", cfg.Field, "file")
	}
	if cfg.NameLength != 32 {
		t.Errorf("cfg.NameLength = %d; want 32", cfg.NameLength)
	}
	if cfg.MaxBytes != (1 << 30) {
		t.Errorf("cfg.MaxBytes = %d; want %d", cfg.MaxBytes, 1<<30)
	}
	if !cfg.AdminEnabled {
		t.Errorf("cfg.AdminEnabled = false; want true")
	}
	if cfg.StripExtension {
		t.Errorf("cfg.StripExtension = true; want false")
	}
	if cfg.ServeFiles {
		t.Errorf("cfg.ServeFiles = true; want false")
	}
}

func TestLoadConfigCustomEnv(t *testing.T) {
	t.Setenv("LISTEN_ADDR", ":9090")
	t.Setenv("UPLOAD_DIR", "/tmp/uploads")
	t.Setenv("BASE_URL", "https://cdn.example.com/")
	t.Setenv("RANDOM_NAME_LENGTH", "16")
	t.Setenv("MAX_UPLOAD_BYTES", strconv.FormatInt(100<<20, 10))
	t.Setenv("ENABLE_ADMIN", "false")
	t.Setenv("STRIP_EXTENSION", "true")
	t.Setenv("SERVE_FILES", "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.Addr != ":9090" {
		t.Errorf("cfg.Addr = %q; want %q", cfg.Addr, ":9090")
	}
	if cfg.Dir != "/tmp/uploads" {
		t.Errorf("cfg.Dir = %q; want %q", cfg.Dir, "/tmp/uploads")
	}
	if cfg.BaseURL != "https://cdn.example.com" {
		t.Errorf("cfg.BaseURL = %q; want %q (trailing slash trimmed)", cfg.BaseURL, "https://cdn.example.com")
	}
	if cfg.NameLength != 16 {
		t.Errorf("cfg.NameLength = %d; want 16", cfg.NameLength)
	}
	if cfg.MaxBytes != (100 << 20) {
		t.Errorf("cfg.MaxBytes = %d; want %d", cfg.MaxBytes, 100<<20)
	}
	if cfg.AdminEnabled {
		t.Errorf("cfg.AdminEnabled = true; want false")
	}
	if !cfg.StripExtension {
		t.Errorf("cfg.StripExtension = false; want true")
	}
	if !cfg.ServeFiles {
		t.Errorf("cfg.ServeFiles = false; want true")
	}
}

func TestLoadConfigInvalidMaxBytes(t *testing.T) {
	t.Setenv("MAX_UPLOAD_BYTES", "invalid_number")

	if _, err := LoadConfig(); err == nil {
		t.Errorf("LoadConfig() expected error for invalid MAX_UPLOAD_BYTES")
	}
}
