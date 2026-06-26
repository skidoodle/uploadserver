package internal

import (
	"fmt"
	"strconv"
	"strings"
)

// Config holds the fully-resolved runtime configuration.
type Config struct {
	Addr           string // listen address, e.g. ":8080"
	Dir            string // directory uploads are written to
	BaseURL        string // public base URL for returned links e.g. https://cdn.example.com
	Field          string // multipart form field name uploads arrive under
	MaxBytes       int64  // maximum upload size in bytes
	StorePath      string // path to the JSON token store
	AdminEnabled   bool   // whether the admin UI + API are mounted
	StripExtension bool   // whether to strip the file extension from the returned URL
	ServeFiles     bool   // whether to serve uploaded files over HTTP at GET /
	NameLength     int    // length of the random part of the filename (hex characters)
}

// LoadConfig resolves the configuration from environment variables.
func LoadConfig() (Config, error) {
	cfg := Config{
		Addr:           Env("LISTEN_ADDR", ":8080"),
		Dir:            Env("UPLOAD_DIR", "./data"),
		BaseURL:        strings.TrimRight(Env("BASE_URL", ""), "/"),
		Field:          Env("UPLOAD_FIELD", "file"),
		StorePath:      Env("TOKEN_STORE", "./state/tokens.db"),
		AdminEnabled:   Env("ENABLE_ADMIN", "true") != "false",
		StripExtension: Env("STRIP_EXTENSION", "false") == "true",
		ServeFiles:     Env("SERVE_FILES", "false") == "true",
	}

	lenStr := Env("RANDOM_NAME_LENGTH", "32")
	nameLen, err := strconv.Atoi(lenStr)
	if err != nil || nameLen <= 0 {
		nameLen = 32 // fallback to default 128-bit (32 hex characters)
	}
	cfg.NameLength = nameLen

	maxStr := Env("MAX_UPLOAD_BYTES", strconv.Itoa(1<<30)) // 1 GiB default
	maxBytes, err := strconv.ParseInt(maxStr, 10, 64)
	if err != nil || maxBytes <= 0 {
		return Config{}, fmt.Errorf("invalid MAX_UPLOAD_BYTES %q", maxStr)
	}
	cfg.MaxBytes = maxBytes

	return cfg, nil
}
