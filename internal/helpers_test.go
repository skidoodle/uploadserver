package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Clean string",
			input:    "normal log entry",
			expected: "normal log entry",
		},
		{
			name:     "String with newlines and carriage returns",
			input:    "line1\nline2\r\nline3",
			expected: "line1 line2  line3",
		},
		{
			name:     "String with control characters",
			input:    "hello\x00\x07world\t!",
			expected: "helloworld\t!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeLog(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeLog(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestEnv(t *testing.T) {
	key := "TEST_UPLOADSERVER_ENV_VAR"
	os.Unsetenv(key)

	if got := Env(key, "default_val"); got != "default_val" {
		t.Errorf("Env() for unset var = %q; want %q", got, "default_val")
	}

	os.Setenv(key, "custom_val")
	defer os.Unsetenv(key)

	if got := Env(key, "default_val"); got != "custom_val" {
		t.Errorf("Env() for set var = %q; want %q", got, "custom_val")
	}
}

func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	if err := CheckWritable(dir); err != nil {
		t.Errorf("CheckWritable(%q) unexpected error: %v", dir, err)
	}

	invalidDir := filepath.Join(dir, "non_existent_subdir_12345")
	if err := CheckWritable(invalidDir); err == nil {
		t.Errorf("CheckWritable(%q) expected error for non-existent directory", invalidDir)
	}
}
