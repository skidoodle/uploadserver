package internal

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateSecret returns 256 bits of URL- and header-safe randomness, used as a
// bearer token secret.
func GenerateSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// randomID returns a short, human-friendly identifier for a token record.
func randomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
