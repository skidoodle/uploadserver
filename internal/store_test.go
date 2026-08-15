package internal

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
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

func TestStore_AuthIndexMigrationReopenAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id, secret, err := store.Add("indexed", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(secret))
	if err := store.db.View(func(tx *bolt.Tx) error {
		got := tx.Bucket(authBucket).Get(sum[:])
		if string(got) != id {
			t.Fatalf("auth index = %q, want %q", got, id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate an existing database from before the auth index existed.
	if err := store.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(authBucket) }); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if rec, ok := store.Authenticate(secret); !ok || rec.ID != id {
		t.Fatalf("authentication after index rebuild = (%+v, %v)", rec, ok)
	}

	if err := store.Remove(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Authenticate(secret); ok {
		t.Fatal("removed token still authenticated")
	}
	if err := store.db.View(func(tx *bolt.Tx) error {
		if got := tx.Bucket(authBucket).Get(sum[:]); got != nil {
			t.Fatalf("revoked digest remains indexed as %q", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStore_LegacyUploadMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	old := time.Now().UTC().Add(-time.Hour)
	newer := old.Add(time.Minute)
	legacy := []UploadEntry{
		{Name: "old.txt", Size: 1, UploadedAt: old},
		{Name: "new.txt", Size: 2, UploadedAt: newer},
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucket(uploadBucket)
		if err != nil {
			return err
		}
		data, err := json.Marshal(legacy)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("legacy-token"), data)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.UploadsFor("legacy-token")
	if err != nil || len(entries) != 2 || entries[0].Name != "new.txt" || entries[1].Name != "old.txt" {
		t.Fatalf("migrated entries = %+v, err=%v", entries, err)
	}
	if err := store.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket(uploadBucket)
		if root.Get([]byte("legacy-token")) != nil {
			t.Fatal("legacy JSON array remains")
		}
		nested := root.Bucket([]byte("legacy-token"))
		if nested == nil || nested.Get([]byte("old.txt")) == nil || nested.Get([]byte("new.txt")) == nil {
			t.Fatal("per-token filename keys were not created")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening is idempotent, and direct filename replacement/removal remains
	// one-key-per-file rather than growing or rewriting an array.
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	replacement := UploadEntry{Name: "old.txt", Size: 9, UploadedAt: newer.Add(time.Minute)}
	if err := store.RecordUploadEntry("legacy-token", replacement); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveUploadEntry("legacy-token", "new.txt"); err != nil {
		t.Fatal(err)
	}
	entries, err = store.UploadsFor("legacy-token")
	if err != nil || len(entries) != 1 || entries[0].Size != 9 {
		t.Fatalf("entries after O(1) replace/remove = %+v, err=%v", entries, err)
	}
}

func TestStore_ReservationConcurrencyAndCancellation(t *testing.T) {
	store := newMemStore(t)
	id, _, err := store.Add("reserve", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLimits(id, Limits{MaxUploads: 2}, false); err != nil {
		t.Fatal(err)
	}

	const workers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var reservations []UploadReservation
	var quotaErrors int
	for range workers {
		wg.Go(func() {
			<-start
			reservation, _, err := store.ReserveUpload(id, 100)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				reservations = append(reservations, reservation)
			} else if errors.Is(err, ErrQuotaUploads) {
				quotaErrors++
			} else {
				t.Errorf("ReserveUpload: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(reservations) != 2 || quotaErrors != workers-2 {
		t.Fatalf("admitted=%d quotaErrors=%d, want 2 and %d", len(reservations), quotaErrors, workers-2)
	}
	if err := store.CancelUpload(id, reservations[0]); err != nil {
		t.Fatal(err)
	}
	third, _, err := store.ReserveUpload(id, 100)
	if err != nil {
		t.Fatalf("reservation after cancellation: %v", err)
	}
	if err := store.CancelUpload(id, third); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelUpload(id, reservations[1]); err != nil {
		t.Fatal(err)
	}

	if err := store.SetLimits(id, Limits{MaxBytes: 100}, false); err != nil {
		t.Fatal(err)
	}
	first, firstBudget, err := store.ReserveUpload(id, 60)
	if err != nil || firstBudget != 60 {
		t.Fatalf("first byte reservation = (%q, %d, %v)", first, firstBudget, err)
	}
	second, secondBudget, err := store.ReserveUpload(id, 60)
	if err != nil || secondBudget != 40 {
		t.Fatalf("second byte reservation = (%q, %d, %v), want budget 40", second, secondBudget, err)
	}
	if _, _, err := store.ReserveUpload(id, 1); !errors.Is(err, ErrQuotaBytes) {
		t.Fatalf("reservation past byte cap = %v, want ErrQuotaBytes", err)
	}
	_ = store.CancelUpload(id, first)
	_ = store.CancelUpload(id, second)

	if err := store.SetLimits(id, Limits{MonthlyUploads: 1, MonthlyBytes: 10}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Update(func(tx *bolt.Tx) error {
		record, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		record.Usage.MonthUploads = 1
		record.Usage.MonthBytes = 10
		record.Usage.Period = time.Now().UTC().AddDate(0, -1, 0)
		return putRecord(tx, record)
	}); err != nil {
		t.Fatal(err)
	}
	monthly, monthlyBudget, err := store.ReserveUpload(id, 10)
	if err != nil || monthlyBudget != 10 {
		t.Fatalf("new monthly period reservation = (%q, %d, %v)", monthly, monthlyBudget, err)
	}
	_ = store.CancelUpload(id, monthly)

	if err := store.SetLimits(id, Limits{MaxBytes: 1, MaxUploads: 1}, true); err != nil {
		t.Fatal(err)
	}
	bypass, bypassBudget, err := store.ReserveUpload(id, 50)
	if err != nil || bypassBudget != 50 {
		t.Fatalf("bypass reservation = (%q, %d, %v)", bypass, bypassBudget, err)
	}
	_ = store.CancelUpload(id, bypass)
}

func TestStore_CommitUploadAtomicAndRevalidates(t *testing.T) {
	store := newMemStore(t)
	id, _, err := store.Add("commit", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLimits(id, Limits{MaxBytes: 20, MaxUploads: 1}, false); err != nil {
		t.Fatal(err)
	}
	reservation, budget, err := store.ReserveUpload(id, 20)
	if err != nil || budget != 20 {
		t.Fatalf("ReserveUpload = (%q, %d, %v)", reservation, budget, err)
	}
	entry := UploadEntry{Name: "atomic.bin", Size: 12, UploadedAt: time.Now().UTC()}
	if err := store.CommitUpload(id, reservation, entry); err != nil {
		t.Fatal(err)
	}
	record, _ := store.GetRecord(id)
	entries, err := store.UploadsFor(id)
	if err != nil || record.Usage.Uploads != 1 || record.Usage.Bytes != 12 || len(entries) != 1 || entries[0].Name != entry.Name {
		t.Fatalf("atomic commit record=%+v entries=%+v err=%v", record, entries, err)
	}
	if err := store.CommitUpload(id, reservation, entry); !errors.Is(err, ErrReservationMissing) {
		t.Fatalf("reused reservation = %v, want ErrReservationMissing", err)
	}

	id2, _, err := store.Add("changed", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := store.ReserveUpload(id2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDisabled(id2, true); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitUpload(id2, pending, UploadEntry{Name: "blocked", Size: 1, UploadedAt: time.Now().UTC()}); !errors.Is(err, ErrTokenDisabled) {
		t.Fatalf("commit after disable = %v, want ErrTokenDisabled", err)
	}
	record, _ = store.GetRecord(id2)
	entries, err = store.UploadsFor(id2)
	if err != nil || record.Usage.Uploads != 0 || len(entries) != 0 {
		t.Fatalf("failed commit was partially persisted: record=%+v entries=%+v err=%v", record, entries, err)
	}
	if err := store.CancelUpload(id2, pending); !errors.Is(err, ErrReservationMissing) {
		t.Fatalf("failed commit did not release reservation: %v", err)
	}

	id3, _, err := store.Add("limited", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLimits(id3, Limits{MaxBytes: 100}, false); err != nil {
		t.Fatal(err)
	}
	changed, _, err := store.ReserveUpload(id3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLimits(id3, Limits{MaxBytes: 5}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitUpload(id3, changed, UploadEntry{Name: "too-large", Size: 10, UploadedAt: time.Now().UTC()}); !errors.Is(err, ErrQuotaBytes) {
		t.Fatalf("commit after quota tightening = %v, want ErrQuotaBytes", err)
	}
	if entries, err := store.UploadsFor(id3); err != nil || len(entries) != 0 {
		t.Fatalf("quota-rejected commit persisted entries=%+v err=%v", entries, err)
	}

	id4, _, err := store.Add("deleted", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	deleted, _, err := store.ReserveUpload(id4, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(id4); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitUpload(id4, deleted, UploadEntry{Name: "gone", Size: 1, UploadedAt: time.Now().UTC()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("commit after deletion = %v, want ErrNotFound", err)
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

func TestPendingPurge(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	id, _, err := store.Add("alice", RoleUpload)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// 1. None pending
	if _, ok := store.GetPendingPurge(id); ok {
		t.Fatal("expected no pending purge initially")
	}

	// 2. Schedule purge
	p, err := store.SchedulePurge(id, id, 1*time.Hour)
	if err != nil {
		t.Fatalf("SchedulePurge: %v", err)
	}
	if p.TokenID != id {
		t.Errorf("got TokenID %q, want %q", p.TokenID, id)
	}

	gotP, ok := store.GetPendingPurge(id)
	if !ok {
		t.Fatal("expected pending purge to be found")
	}
	if gotP.TokenID != id {
		t.Errorf("got %q, want %q", gotP.TokenID, id)
	}

	list, err := store.ListPendingPurges()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListPendingPurges: len=%d, err=%v", len(list), err)
	}

	// 3. Cancel purge
	cancelled, err := store.CancelPendingPurge(id)
	if err != nil || !cancelled {
		t.Fatalf("CancelPendingPurge: cancelled=%v, err=%v", cancelled, err)
	}
	if _, ok := store.GetPendingPurge(id); ok {
		t.Fatal("expected pending purge to be deleted")
	}

	// 4. Process due purges
	_, err = store.SchedulePurge(id, id, -1*time.Minute)
	if err != nil {
		t.Fatalf("SchedulePurge: %v", err)
	}
	var executed []string
	count, err := store.ProcessDuePurges(time.Now().UTC(), func(tokenID string) error {
		executed = append(executed, tokenID)
		return nil
	})
	if err != nil {
		t.Fatalf("ProcessDuePurges: %v", err)
	}
	if count != 1 || len(executed) != 1 || executed[0] != id {
		t.Errorf("executed count=%d, list=%v", count, executed)
	}
	if _, ok := store.GetPendingPurge(id); ok {
		t.Fatal("expected pending purge to be removed after execution")
	}
}

func TestStore_LastUsedPersistedOnFlush(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tokens.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	id, secret, err := store.Add("user1", RoleUpload)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// 1. Authenticate with secret
	rec, ok := store.Authenticate(secret)
	if !ok {
		t.Fatalf("Authenticate failed")
	}
	if rec.LastUsed.IsZero() {
		t.Errorf("expected LastUsed to be non-zero in memory")
	}

	// 2. Explicitly flush LastUsed
	if err := store.FlushLastUsed(); err != nil {
		t.Fatalf("FlushLastUsed failed: %v", err)
	}
	_ = store.Close()

	// 3. Re-open store from disk and verify LastUsed was persisted to bbolt
	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore re-open failed: %v", err)
	}
	defer func() { _ = store2.Close() }()

	persisted, found := store2.GetRecord(id)
	if !found {
		t.Fatalf("GetRecord failed for %s", id)
	}
	if persisted.LastUsed.IsZero() {
		t.Errorf("expected persisted LastUsed to be non-zero in BoltDB on disk")
	}
}

func TestStore_QuotaReclaimedOnDelete(t *testing.T) {
	store := newMemStore(t)
	id, _, err := store.Add("qtest", RoleUpload)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Set 10 MB total size limit
	if err := store.SetLimits(id, Limits{MaxBytes: 10 * 1024 * 1024}, false); err != nil {
		t.Fatalf("SetLimits: %v", err)
	}

	// 1. Reserve and commit 8 MB upload
	res, budget, err := store.ReserveUpload(id, 10*1024*1024)
	if err != nil || budget != 10*1024*1024 {
		t.Fatalf("ReserveUpload: budget=%d, err=%v", budget, err)
	}
	entry := UploadEntry{Name: "large.bin", Size: 8 * 1024 * 1024, UploadedAt: time.Now().UTC()}
	if err := store.CommitUpload(id, res, entry); err != nil {
		t.Fatalf("CommitUpload: %v", err)
	}

	// 2. Next upload should only have 2 MB budget left
	res2, budget2, err := store.ReserveUpload(id, 10*1024*1024)
	if err != nil || budget2 != 2*1024*1024 {
		t.Fatalf("ReserveUpload remaining: budget=%d, err=%v", budget2, err)
	}
	_ = store.CancelUpload(id, res2)

	// 3. Delete the file using RemoveUploadEntry
	if err := store.RemoveUploadEntry(id, "large.bin"); err != nil {
		t.Fatalf("RemoveUploadEntry: %v", err)
	}

	// 4. Verify storage quota is reclaimed (10 MB budget available again)
	_, budget3, err := store.ReserveUpload(id, 10*1024*1024)
	if err != nil || budget3 != 10*1024*1024 {
		t.Fatalf("ReserveUpload after deletion: budget=%d, err=%v; want %d", budget3, err, 10*1024*1024)
	}
}
