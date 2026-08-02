package internal

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestInviteSystem_CreditsAndNoInvites(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer store.Close()

	uploaderID, _, err := store.Add("user1", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("AddWithInvite with Zero Invites Fails", func(t *testing.T) {
		if _, _, err := store.AddWithInvite(uploaderID, "user2"); !errors.Is(err, ErrNoInvites) {
			t.Errorf("AddWithInvite with 0 invites got %v; want ErrNoInvites", err)
		}
	})

	t.Run("AddWithInvite Decrements Credits", func(t *testing.T) {
		if err := store.SetInvites(uploaderID, 2); err != nil {
			t.Fatal(err)
		}

		invitedID, secret, err := store.AddWithInvite(uploaderID, "user2")
		if err != nil || invitedID == "" || secret == "" {
			t.Fatalf("AddWithInvite got id=%q, sec=%q, err=%v", invitedID, secret, err)
		}

		rec, _ := store.GetRecord(uploaderID)
		if rec.Invites != 1 {
			t.Errorf("Invites after creation = %d; want 1", rec.Invites)
		}
	})

	t.Run("Admin Tokens Have Unlimited Invites", func(t *testing.T) {
		adminID, _, _ := store.Add("admin1", RoleAdmin)
		id, _, err := store.AddWithInvite(adminID, "user3")
		if err != nil || id == "" {
			t.Fatalf("AddWithInvite from admin error: %v", err)
		}
	})
}

func TestInviteSystem_GiveawaysAndCapping(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer store.Close()

	u1, _, _ := store.Add("user1", RoleUpload)
	u2, _, _ := store.Add("user2", RoleUpload)
	_ = store.SetInvites(u1, 5)

	t.Run("Giveaway Capped", func(t *testing.T) {
		// Add 2 invites, cap at 5
		updated, err := store.AddInvitesToAllUploadersCapped(2, 5)
		if err != nil {
			t.Fatalf("AddInvitesToAllUploadersCapped error: %v", err)
		}
		if updated != 1 {
			t.Errorf("Updated uploaders = %d; want 1 (u1 was at cap 5)", updated)
		}

		r1, _ := store.GetRecord(u1)
		r2, _ := store.GetRecord(u2)
		if r1.Invites != 5 {
			t.Errorf("r1 invites = %d; want 5 (capped)", r1.Invites)
		}
		if r2.Invites != 2 {
			t.Errorf("r2 invites = %d; want 2", r2.Invites)
		}
	})

	t.Run("Random Giveaway", func(t *testing.T) {
		updated, err := store.AddInvitesToRandomUploaders(1, 1, 10)
		if err != nil || updated != 1 {
			t.Fatalf("AddInvitesToRandomUploaders got updated=%d, err=%v", updated, err)
		}
	})
}

func TestInviteSystem_PendingGrantsAndScheduler(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.db"))
	if err != nil {
		t.Fatalf("OpenStore error: %v", err)
	}
	defer store.Close()

	// Enable new user policy
	pol := InvitePolicy{
		NewUserEnabled: true,
		NewUserCount:   3,
		NewUserDelay:   0, // immediate
	}
	_ = store.SetInvitePolicy(pol)

	adminID, _, _ := store.Add("admin1", RoleAdmin)
	newUserID, _, err := store.AddWithInvite(adminID, "newbie")
	if err != nil {
		t.Fatal(err)
	}

	applied, err := store.ProcessPendingGrants()
	if err != nil {
		t.Fatalf("ProcessPendingGrants error: %v", err)
	}
	if applied != 1 {
		t.Fatalf("ProcessPendingGrants applied = %d; want 1", applied)
	}

	rec, _ := store.GetRecord(newUserID)
	if rec.Invites != 3 {
		t.Errorf("New user invites after pending grant = %d; want 3", rec.Invites)
	}

	// Test scheduler start/stop
	sched := NewInviteScheduler(store)
	sched.Start()
	time.Sleep(50 * time.Millisecond)
	sched.Stop()
}
