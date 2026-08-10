package internal

import (
	"runtime"
	"strings"
	"testing"
)

func TestVersionInfo(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	defer func() {
		Version, Commit, Date = origV, origC, origD
	}()

	Version = "v2.1.0"
	Commit = "fedcba987654"
	Date = "2026-08-10T12:00:00Z"

	if Version != "v2.1.0" {
		t.Errorf("expected Version to be v2.1.0, got %q", Version)
	}
	if Commit != "fedcba987654" {
		t.Errorf("expected Commit to be fedcba987654, got %q", Commit)
	}
	if Date != "2026-08-10T12:00:00Z" {
		t.Errorf("expected Date to be 2026-08-10T12:00:00Z, got %q", Date)
	}

	vs := VersionString()
	if !strings.Contains(vs, "uploadserver v2.1.0") {
		t.Errorf("expected VersionString to contain 'uploadserver v2.1.0', got %q", vs)
	}
	if !strings.Contains(vs, "commit: fedcba987654") {
		t.Errorf("expected VersionString to contain commit, got %q", vs)
	}
	if !strings.Contains(vs, "built at: 2026-08-10T12:00:00Z") {
		t.Errorf("expected VersionString to contain date, got %q", vs)
	}
	if !strings.Contains(vs, runtime.Version()) {
		t.Errorf("expected VersionString to contain Go runtime version, got %q", vs)
	}
	if !strings.Contains(vs, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("expected VersionString to contain GOOS/GOARCH, got %q", vs)
	}
}
