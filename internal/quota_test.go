package internal

import (
	"errors"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"":          0,
		"0":         0,
		"off":       0,
		"unlimited": 0,
		"1024":      1024,
		"1kb":       1 << 10,
		"1 KB":      1 << 10,
		"5gb":       5 << 30,
		"5 GB":      5 << 30,
		"1.5gb":     1<<30 + 1<<29,
		"500mb":     500 << 20,
		"2tib":      2 << 40,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}

	for _, bad := range []string{"abc", "-5gb", "5zz", "gb"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q): expected error", bad)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		512:     "512 B",
		1 << 10: "1 KB",
		1536:    "1.5 KB",
		5 << 30: "5 GB",
		1 << 40: "1 TB",
	}
	for in, want := range cases {
		if got := FormatSize(in); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestComma(t *testing.T) {
	cases := map[int64]string{0: "0", 12: "12", 1234: "1,234", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := Comma(in); got != want {
			t.Errorf("Comma(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBudgetCountQuota(t *testing.T) {
	now := time.Now().UTC()
	lim := Limits{MaxUploads: 2}

	if _, err := lim.budget(Usage{Uploads: 1}, now, 1<<20); err != nil {
		t.Fatalf("under count quota: unexpected error %v", err)
	}
	if _, err := lim.budget(Usage{Uploads: 2}, now, 1<<20); !errors.Is(err, ErrQuotaUploads) {
		t.Fatalf("at count quota: got %v, want ErrQuotaUploads", err)
	}
}

func TestBudgetClampsToRemainingBytes(t *testing.T) {
	now := time.Now().UTC()
	lim := Limits{MaxBytes: 1000}

	// 400 already used, server cap 1<<20: budget is clamped to the remaining 600.
	got, err := lim.budget(Usage{Bytes: 400}, now, 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 600 {
		t.Fatalf("budget = %d, want 600", got)
	}

	// When remaining > hardMax, the server cap still wins.
	if got, _ := lim.budget(Usage{Bytes: 0}, now, 100); got != 100 {
		t.Fatalf("budget = %d, want 100 (server cap)", got)
	}

	// Exhausted byte quota is refused.
	if _, err := lim.budget(Usage{Bytes: 1000}, now, 1<<20); !errors.Is(err, ErrQuotaBytes) {
		t.Fatalf("at byte quota: got %v, want ErrQuotaBytes", err)
	}
}

func TestBudgetMonthlyResetsAcrossPeriods(t *testing.T) {
	now := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)
	lastMonth := time.Date(2026, time.May, 31, 23, 0, 0, 0, time.UTC)
	lim := Limits{MonthlyUploads: 1, MonthlyBytes: 100}

	// Usage was recorded last month, so this month it should read as fresh.
	stale := Usage{MonthUploads: 5, MonthBytes: 999, Period: lastMonth}
	got, err := lim.budget(stale, now, 1<<20)
	if err != nil {
		t.Fatalf("stale monthly usage should not block: %v", err)
	}
	if got != 100 {
		t.Fatalf("monthly budget = %d, want 100", got)
	}

	// Within the same month, the monthly cap applies.
	fresh := Usage{MonthUploads: 1, MonthBytes: 0, Period: now}
	if _, err := lim.budget(fresh, now, 1<<20); !errors.Is(err, ErrQuotaUploads) {
		t.Fatalf("same-month monthly cap: got %v, want ErrQuotaUploads", err)
	}
}

func TestRecordUploadRollsMonth(t *testing.T) {
	store := newMemStore(t)
	id, _, err := store.Add("k", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RecordUpload(id, 500); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUpload(id, 300); err != nil {
		t.Fatal(err)
	}

	rec := findRec(t, store, id)
	if rec.Usage.Uploads != 2 || rec.Usage.Bytes != 800 {
		t.Fatalf("lifetime usage = %d uploads / %d bytes, want 2 / 800", rec.Usage.Uploads, rec.Usage.Bytes)
	}
	if rec.Usage.MonthUploads != 2 || rec.Usage.MonthBytes != 800 {
		t.Fatalf("monthly usage = %d / %d, want 2 / 800", rec.Usage.MonthUploads, rec.Usage.MonthBytes)
	}
	if rec.LastUsed.IsZero() {
		t.Fatal("RecordUpload should stamp LastUsed")
	}
}

func TestEffectiveLimits(t *testing.T) {
	global := Limits{MaxBytes: 5 << 30, MaxUploads: 1000}

	// No personal caps: the token inherits the global default wholesale.
	if got := EffectiveLimits(Limits{}, global, false); got != global {
		t.Fatalf("inherit: got %+v, want %+v", got, global)
	}

	// A personal cap overrides only its own dimension; the rest still inherit.
	personal := Limits{MaxBytes: 20 << 30}
	got := EffectiveLimits(personal, global, false)
	if got.MaxBytes != 20<<30 {
		t.Errorf("personal MaxBytes should win: got %d", got.MaxBytes)
	}
	if got.MaxUploads != 1000 {
		t.Errorf("MaxUploads should inherit global: got %d", got.MaxUploads)
	}

	// A bypassing token is fully exempt: no quota, personal caps included.
	if got := EffectiveLimits(personal, global, true); got != (Limits{}) {
		t.Fatalf("bypass: got %+v, want zero (exempt)", got)
	}
}

func TestGlobalQuotaPersistsAndApplies(t *testing.T) {
	store := newMemStore(t)
	id, _, err := store.Add("k", RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetGlobalLimits(Limits{MaxUploads: 1}); err != nil {
		t.Fatal(err)
	}

	// The global cap applies to a token with no personal limits.
	if _, err := store.AllowUpload(id, 1<<20); err != nil {
		t.Fatalf("first upload under global cap: %v", err)
	}
	if err := store.RecordUpload(id, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AllowUpload(id, 1<<20); !errors.Is(err, ErrQuotaUploads) {
		t.Fatalf("second upload past global cap: got %v, want ErrQuotaUploads", err)
	}

	// Bypassing the global lifts the cap for that token.
	if err := store.SetLimits(id, Limits{}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AllowUpload(id, 1<<20); err != nil {
		t.Fatalf("bypassing token should not be capped: %v", err)
	}
}

func newMemStore(t *testing.T) *TokenStore {
	t.Helper()
	store, err := OpenStore(t.TempDir() + "/tokens.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func findRec(t *testing.T, store *TokenStore, id string) TokenRecord {
	t.Helper()
	for _, r := range store.List() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("token %s not found", id)
	return TokenRecord{}
}
