package internal

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestGenerateSecret(t *testing.T) {
	sec1, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret() error: %v", err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(sec1)
	if err != nil {
		t.Fatalf("GenerateSecret() output not raw base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("GenerateSecret() length = %d bytes; want 32 bytes", len(decoded))
	}

	sec2, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret() second call error: %v", err)
	}
	if sec1 == sec2 {
		t.Errorf("GenerateSecret() produced duplicate secrets: %q", sec1)
	}
}

func TestRandomID(t *testing.T) {
	id := randomID()
	if len(id) != 8 {
		t.Errorf("randomID() length = %d; want 8 hex characters", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Errorf("randomID() is not valid hex: %v", err)
	}
}
