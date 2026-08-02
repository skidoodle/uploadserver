package internal

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_Bootstrap(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Initial bootstrap creates root
	secret, created, err := store.Bootstrap()
	if err != nil {
		t.Fatalf("Bootstrap error: %v", err)
	}
	if !created || secret == "" {
		t.Fatalf("Bootstrap got created=%v, secret=%q; want created=true and non-empty secret", created, secret)
	}

	// Subsequent bootstrap is a no-op
	_, created2, err := store.Bootstrap()
	if err != nil || created2 {
		t.Fatalf("Subsequent Bootstrap got created=%v, err=%v; want created=false", created2, err)
	}

	// Verify root token properties
	list := store.List()
	if len(list) != 1 || list[0].Role != RoleRoot || list[0].Label != "root" {
		t.Fatalf("List after Bootstrap got %+v; want 1 root token", list)
	}
}

func TestStore_AddAndAuthenticate(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	t.Run("Valid Add and Authenticate", func(t *testing.T) {
		id, secret, err := store.Add("uploader", RoleUpload)
		if err != nil {
			t.Fatalf("Add upload token error: %v", err)
		}
		if id == "" || secret == "" {
			t.Fatalf("Add returned empty id or secret")
		}

		rec, ok := store.Authenticate(secret)
		if !ok || rec.ID != id || rec.Role != RoleUpload || rec.Label != "uploader" {
			t.Fatalf("Authenticate got ok=%v, rec=%+v; want ok=true, ID=%s", ok, rec, id)
		}
		if rec.LastUsed.IsZero() {
			t.Error("Authenticate expected non-zero LastUsed timestamp")
		}
	})

	t.Run("Invalid Labels and Roles", func(t *testing.T) {
		invalidLabels := []string{"", "invalid label", "toolonglabelname", "label!", "-start"}
		for _, lbl := range invalidLabels {
			if _, _, err := store.Add(lbl, RoleUpload); !errors.Is(err, ErrInvalidLabel) {
				t.Errorf("Add(%q) got err=%v; want ErrInvalidLabel", lbl, err)
			}
		}

		if _, _, err := store.Add("valid", "super-role"); err == nil {
			t.Errorf("Add with invalid role expected error")
		}
	})

	t.Run("Invalid Secrets", func(t *testing.T) {
		if _, ok := store.Authenticate("short"); ok {
			t.Error("Authenticate with short secret expected false")
		}
		if _, ok := store.Authenticate("invalid-secret-string-1234567890"); ok {
			t.Error("Authenticate with unknown secret expected false")
		}
	})
}

func TestStore_RootProtection(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, _, _ = store.Bootstrap()
	list := store.List()
	var rootID string
	for _, r := range list {
		if r.Role == RoleRoot {
			rootID = r.ID
			break
		}
	}
	if rootID == "" {
		t.Fatal("Root token missing after bootstrap")
	}

	t.Run("Remove Root Forbidden", func(t *testing.T) {
		if err := store.Remove(rootID); !errors.Is(err, ErrProtectedRoot) {
			t.Errorf("Remove(root) got %v; want ErrProtectedRoot", err)
		}
	})

	t.Run("Disable Root Forbidden", func(t *testing.T) {
		if err := store.SetDisabled(rootID, true); !errors.Is(err, ErrProtectedRoot) {
			t.Errorf("SetDisabled(root) got %v; want ErrProtectedRoot", err)
		}
	})

	t.Run("Demote Root Forbidden", func(t *testing.T) {
		if err := store.SetRole(rootID, RoleUpload); !errors.Is(err, ErrProtectedRoot) {
			t.Errorf("SetRole(root) got %v; want ErrProtectedRoot", err)
		}
	})
}

func TestStore_LastAdminProtection(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	adminID, _, err := store.Add("admin1", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Remove Last Admin Forbidden", func(t *testing.T) {
		if err := store.Remove(adminID); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("Remove(lastAdmin) got %v; want ErrLastAdmin", err)
		}
	})

	t.Run("Disable Last Admin Forbidden", func(t *testing.T) {
		if err := store.SetDisabled(adminID, true); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("SetDisabled(lastAdmin) got %v; want ErrLastAdmin", err)
		}
	})

	t.Run("Demote Last Admin Forbidden", func(t *testing.T) {
		if err := store.SetRole(adminID, RoleUpload); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("SetRole(lastAdmin) got %v; want ErrLastAdmin", err)
		}
	})

	t.Run("Action Allowed when Second Admin Exists", func(t *testing.T) {
		admin2ID, _, err := store.Add("admin2", RoleAdmin)
		if err != nil {
			t.Fatal(err)
		}

		if err := store.SetDisabled(adminID, true); err != nil {
			t.Errorf("SetDisabled allowed when admin2 exists, got %v", err)
		}

		if err := store.Remove(admin2ID); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("Remove(admin2ID) should be blocked as last enabled admin, got %v; want ErrLastAdmin", err)
		}
	})
}

func TestStore_UploadEntriesAndImport(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	id, _, _ := store.Add("user1", RoleUpload)

	e1 := UploadEntry{Name: "file1.png", Size: 1024, UploadedAt: time.Now().UTC()}
	if err := store.RecordUploadEntry(id, e1); err != nil {
		t.Fatalf("RecordUploadEntry error: %v", err)
	}

	uploads, err := store.UploadsFor(id)
	if err != nil || len(uploads) != 1 || uploads[0].Name != "file1.png" {
		t.Fatalf("UploadsFor got %+v, err=%v", uploads, err)
	}

	imports := []UploadEntry{
		{Name: "imp1.jpg", Size: 2048, UploadedAt: time.Now().UTC()},
		{Name: "imp2.pdf", Size: 4096, UploadedAt: time.Now().UTC()},
	}
	if err := store.ImportUploadEntries(id, imports); err != nil {
		t.Fatalf("ImportUploadEntries error: %v", err)
	}

	allUploads, err := store.UploadsFor(id)
	if err != nil || len(allUploads) != 3 {
		t.Fatalf("UploadsFor after import got len=%d; want 3", len(allUploads))
	}

	rec, _ := store.GetRecord(id)
	if rec.Usage.Uploads != 2 || rec.Usage.Bytes != 6144 {
		t.Errorf("Usage after import = %d uploads / %d bytes; want 2 uploads / 6144 bytes", rec.Usage.Uploads, rec.Usage.Bytes)
	}
}

func TestStore_HashStripping(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer func() { _ = store.Close() }()

	id, _, _ := store.Add("user1", RoleUpload)

	rec, found := store.GetRecord(id)
	if !found {
		t.Fatal("GetRecord failed")
	}
	if rec.Hash != "" {
		t.Errorf("GetRecord exposed hash %q; want empty string", rec.Hash)
	}

	list := store.List()
	if len(list) != 1 || list[0].Hash != "" {
		t.Errorf("List exposed hash; want empty string")
	}

	recordsInternal := store.records()
	if len(recordsInternal) != 1 || recordsInternal[0].Hash == "" {
		t.Errorf("records() internal should preserve secret hash")
	}
}
