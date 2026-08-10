package internal

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPurgeScheduler(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	id, _, err := store.Add("bob", RoleUpload)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Schedule purge due in past
	_, err = store.SchedulePurge(id, id, -10*time.Second)
	if err != nil {
		t.Fatalf("SchedulePurge: %v", err)
	}

	executed := make(chan string, 1)
	sched := NewPurgeScheduler(store, func(tokenID string) error {
		executed <- tokenID
		return nil
	})

	count, err := sched.ProcessNow()
	if err != nil {
		t.Fatalf("ProcessNow: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}

	select {
	case tokenID := <-executed:
		if tokenID != id {
			t.Errorf("got %q, want %q", tokenID, id)
		}
	default:
		t.Fatal("expected purgeHandler to be called")
	}

	// Should be removed from pending bucket
	if _, ok := store.GetPendingPurge(id); ok {
		t.Error("pending purge still present after processing")
	}
}
