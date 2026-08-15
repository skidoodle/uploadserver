package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_SubcommandsAndErrorCases(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.db")
	t.Setenv("TOKEN_STORE", storePath)
	t.Setenv("UPLOAD_DIR", dir)

	t.Run("No Command Error", func(t *testing.T) {
		if err := RunTokenCLI([]string{}); err == nil {
			t.Error("RunTokenCLI with no args expected error")
		}
	})

	t.Run("Unknown Command Error", func(t *testing.T) {
		if err := RunTokenCLI([]string{"unknown_cmd"}); err == nil {
			t.Error("RunTokenCLI with unknown command expected error")
		}
	})

	t.Run("Version Command", func(t *testing.T) {
		if err := RunTokenCLI([]string{"version"}); err != nil {
			t.Errorf("version command error: %v", err)
		}
	})

	t.Run("Add Token Validation", func(t *testing.T) {
		if err := RunTokenCLI([]string{"add", "--label", "testusr", "--role", RoleUpload}); err != nil {
			t.Fatalf("add command error: %v", err)
		}

		if err := RunTokenCLI([]string{"add", "--label", "testusr", "--role", RoleRoot}); err == nil {
			t.Error("add command creating root token expected error")
		}
	})

	// Get Token ID from Store
	store, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	tokens := store.List()
	_ = store.Close()

	if len(tokens) == 0 {
		t.Fatal("expected at least 1 token")
	}
	id := tokens[0].ID

	t.Run("Info Command", func(t *testing.T) {
		if err := RunTokenCLI([]string{"info"}); err == nil {
			t.Error("info command without ID expected error")
		}
		if err := RunTokenCLI([]string{"info", id}); err != nil {
			t.Fatalf("info command error: %v", err)
		}
	})

	t.Run("Limit and Global Commands", func(t *testing.T) {
		if err := RunTokenCLI([]string{"limit", id, "--total-size", "500MB", "--bypass"}); err != nil {
			t.Fatalf("limit command error: %v", err)
		}
		if err := RunTokenCLI([]string{"global", "--monthly-size", "2GB"}); err != nil {
			t.Fatalf("global command error: %v", err)
		}
	})

	t.Run("Disable and Enable Commands", func(t *testing.T) {
		if err := RunTokenCLI([]string{"disable"}); err == nil {
			t.Error("disable command without ID expected error")
		}
		if err := RunTokenCLI([]string{"disable", id}); err != nil {
			t.Fatalf("disable command error: %v", err)
		}
		if err := RunTokenCLI([]string{"enable", id}); err != nil {
			t.Fatalf("enable command error: %v", err)
		}
	})

	t.Run("Export and Import Commands", func(t *testing.T) {
		exportFile := filepath.Join(dir, "backup.json")
		if err := RunTokenCLI([]string{"export", "--out", exportFile}); err != nil {
			t.Fatalf("export command error: %v", err)
		}

		if err := RunTokenCLI([]string{"import"}); err == nil {
			t.Error("import command without --in expected error")
		}
		if err := RunTokenCLI([]string{"import", "--in", exportFile}); err != nil {
			t.Fatalf("import command error: %v", err)
		}
	})

	t.Run("Prune Command", func(t *testing.T) {
		if err := RunTokenCLI([]string{"prune", "--dry-run"}); err != nil {
			t.Fatalf("prune dry-run error: %v", err)
		}
	})

	t.Run("Scan and Dump Commands", func(t *testing.T) {
		if err := RunTokenCLI([]string{"scan"}); err != nil {
			t.Fatalf("scan command error: %v", err)
		}
		if err := RunTokenCLI([]string{"dump"}); err != nil {
			t.Fatalf("dump command error: %v", err)
		}
	})

	t.Run("Migrate Command", func(t *testing.T) {
		if err := RunTokenCLI([]string{"migrate"}); err == nil {
			t.Error("migrate command without --token expected error")
		}
		if err := RunTokenCLI([]string{"migrate", "--token", "nonexistent"}); err == nil {
			t.Error("migrate command with nonexistent token expected error")
		}
	})

	t.Run("Remove and Reset Commands", func(t *testing.T) {
		if err := RunTokenCLI([]string{"rm"}); err == nil {
			t.Error("rm command without ID expected error")
		}
		if err := RunTokenCLI([]string{"rm", id}); err != nil {
			t.Fatalf("rm command error: %v", err)
		}
		if err := RunTokenCLI([]string{"reset"}); err != nil {
			t.Fatalf("reset command error: %v", err)
		}
	})
}

func TestCLI_ShortHash(t *testing.T) {
	if got := shortHash(""); got != "-" {
		t.Errorf("shortHash(\"\") = %q; want \"-\"", got)
	}
	h := "abcdef1234567890"
	if got := shortHash(h); !strings.HasPrefix(got, "abcdef123456") || !strings.HasSuffix(got, "…") {
		t.Errorf("shortHash(%q) = %q; want truncated hex prefix", h, got)
	}
}

func TestCLI_ScanDoesNotDuplicateOrLoop(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tokens.db")
	uploadDir := filepath.Join(dir, "uploads")
	t.Setenv("TOKEN_STORE", storePath)
	t.Setenv("UPLOAD_DIR", uploadDir)

	store, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	id, _, err := store.Add("tester", RoleUpload)
	if err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	_ = store.Close()

	userDir := filepath.Join(uploadDir, id)
	if err := os.MkdirAll(userDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	testFile := filepath.Join(userDir, "image.png")
	if err := os.WriteFile(testFile, []byte("fake image bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// 1. Run scan with --token to import the file
	if err := RunTokenCLI([]string{"scan", "--token", id}); err != nil {
		t.Fatalf("first scan --token failed: %v", err)
	}

	// 2. Run scan again offline; assert nothing left to import
	store2, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = store2.Close() }()

	entries, err := store2.UploadsFor(id)
	if err != nil {
		t.Fatalf("UploadsFor: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 upload entry, got %d", len(entries))
	}
	if entries[0].Name != "image.png" {
		t.Errorf("entry.Name = %q; want %q", entries[0].Name, "image.png")
	}

	// 3. Scan a second time - should report 0 untracked files
	var stdout, stderr strings.Builder
	execCtx := ExecutionContext{
		Store:     store2,
		UploadDir: uploadDir,
		Stdout:    &stdout,
		Stderr:    &stderr,
	}
	if err := runScan(execCtx, []string{}); err != nil {
		t.Fatalf("second scan failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "all files on disk are already tracked") {
		t.Errorf("second scan should report all tracked, got output:\n%s", stdout.String())
	}
}
