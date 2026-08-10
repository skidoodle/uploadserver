package internal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

const (
	RoleRoot   = "root"   // bootstrap superadmin; immutable, cannot be deleted/disabled
	RoleAdmin  = "admin"  // may manage admin/upload tokens
	RoleUpload = "upload" // may only upload
)

// IsAdmin reports whether a role may use the admin API (root counts as admin).
func IsAdmin(role string) bool { return role == RoleRoot || role == RoleAdmin }

var (
	ErrNotFound           = errors.New("token not found")
	ErrLastAdmin          = errors.New("refusing to remove or disable the last enabled admin token")
	ErrProtectedRoot      = errors.New("the root token cannot be deleted or disabled; use `token reset` to replace it")
	ErrInvalidLabel       = errors.New("label must be 1-9 characters, starting and ending with an alphanumeric character (can contain underscores or hyphens in the middle)")
	ErrNoInvites          = errors.New("no invites remaining")
	ErrTokenDisabled      = errors.New("token is disabled")
	ErrReservationMissing = errors.New("upload reservation not found")
	// ErrLocked is returned when another process (usually the running server)
	// already holds the database open.
	ErrLocked = errors.New("token store is locked by another process; stop the server or use the dashboard to manage tokens while it runs")
)

var labelRe = regexp.MustCompile("^[a-zA-Z0-9]([a-zA-Z0-9_-]{0,7}[a-zA-Z0-9])?$")

// bbolt key space: every token record lives in tokenBucket keyed by its id, and
// the single server-wide quota lives in metaBucket under globalKey.
var (
	tokenBucket     = []byte("tokens")
	metaBucket      = []byte("meta")
	globalKey       = []byte("global")
	invitePolicyKey = []byte("invite_policy")
	uploadBucket    = []byte("uploads")
	authBucket      = []byte("auth_index")
	pendingBucket   = []byte("pending_grants")
	purgeBucket     = []byte("pending_purges")
)

// PendingPurge represents a scheduled purge of all media for a token.
type PendingPurge struct {
	TokenID     string    `json:"token_id"`
	RequestedAt time.Time `json:"requested_at"`
	PurgeAt     time.Time `json:"purge_at"`
	ActorID     string    `json:"actor_id"`
}

// InvitePolicy holds the server-wide invite distribution configuration.
type InvitePolicy struct {
	// Scheduled periodic giveaway
	SchedEnabled  bool   `json:"sched_on,omitempty"`
	SchedInterval int64  `json:"sched_interval,omitempty"` // seconds between cycles
	SchedCount    int    `json:"sched_count,omitempty"`    // invites per cycle
	SchedMode     string `json:"sched_mode,omitempty"`     // "all" or "random"
	SchedPool     int    `json:"sched_pool,omitempty"`     // users to pick when random
	SchedMax      int    `json:"sched_max,omitempty"`      // max invites a user can hold

	// New member auto-grant
	NewUserEnabled bool  `json:"newuser_on,omitempty"`
	NewUserCount   int   `json:"newuser_count,omitempty"` // invites to grant
	NewUserDelay   int64 `json:"newuser_delay,omitempty"` // seconds to wait
	NewUserMax     int   `json:"newuser_max,omitempty"`   // max invites cap
}

// PendingGrant is a delayed invite grant for a newly created user.
type PendingGrant struct {
	TokenID string    `json:"token_id"`
	Count   int       `json:"count"`
	MaxCap  int       `json:"max_cap"`
	GrantAt time.Time `json:"grant_at"`
}

// UploadEntry records a single file uploaded by a token.
type UploadEntry struct {
	Name       string    `json:"name"`        // filename on disk (e.g. "abc123.png")
	Size       int64     `json:"size"`        // bytes written
	UploadedAt time.Time `json:"uploaded_at"` // when the upload completed
}

// TokenRecord is a single credential. Only the hash of the secret is stored;
// the plaintext secret is shown once at creation and never persisted.
type TokenRecord struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Hash      string    `json:"hash,omitempty"` // hex(sha256(secret)); cleared before exposing via API
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
	Disabled  bool      `json:"disabled,omitempty"`
	Usage     Usage     `json:"usage,omitzero"`          // running upload counters
	Limits    Limits    `json:"limits,omitzero"`         // per-token upload quotas
	Bypass    bool      `json:"bypass_global,omitempty"` // exempt from all upload quotas
	Invites   int       `json:"invites,omitzero"`        // remaining invite token creation count
}

// BypassesGlobal reports whether the token is exempt from every upload quota.
// The root token is always exempt so a quota can never lock out the superadmin.
func (r TokenRecord) BypassesGlobal() bool {
	return r.Bypass || r.Role == RoleRoot
}

// TokenStore is the bbolt-backed set of token records. bbolt serializes writes
// and gives each method an atomic transaction, so the store is safe for the
// server's concurrent requests without any extra locking. The trade-off is that
// bbolt is single-owner: while the server holds the file open, the `uploadserver`
// CLI cannot open it (see ErrLocked).
type TokenStore struct {
	db *bolt.DB

	reservationMu sync.Mutex
	reservations  map[string]map[UploadReservation]uploadReservation
}

// UploadReservation identifies an in-flight upload admitted against a token's
// quotas. It is opaque to callers and valid until CommitUpload or CancelUpload.
type UploadReservation string

type uploadReservation struct {
	budget int64
}

// OpenStore opens (creating if needed) the bbolt store at path. The parent
// directory is created as well. A short open timeout turns "another process has
// it" into ErrLocked instead of blocking forever.
func OpenStore(path string) (*TokenStore, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, ErrLocked
		}
		return nil, err
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(tokenBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(uploadBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(pendingBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(purgeBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(metaBucket); err != nil {
			return err
		}
		if err := rebuildAuthIndex(tx); err != nil {
			return err
		}
		return migrateUploadEntries(tx)
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &TokenStore{
		db:           db,
		reservations: make(map[string]map[UploadReservation]uploadReservation),
	}, nil
}

func hashKey(hash string) ([]byte, bool) {
	key, err := hex.DecodeString(hash)
	return key, err == nil && len(key) == sha256.Size
}

func rebuildAuthIndex(tx *bolt.Tx) error {
	if tx.Bucket(authBucket) != nil {
		if err := tx.DeleteBucket(authBucket); err != nil {
			return err
		}
	}
	index, err := tx.CreateBucket(authBucket)
	if err != nil {
		return err
	}
	return tx.Bucket(tokenBucket).ForEach(func(_, value []byte) error {
		var record TokenRecord
		if json.Unmarshal(value, &record) != nil {
			return nil
		}
		if key, ok := hashKey(record.Hash); ok {
			return index.Put(key, []byte(record.ID))
		}
		return nil
	})
}

func migrateUploadEntries(tx *bolt.Tx) error {
	uploads := tx.Bucket(uploadBucket)
	var legacyKeys [][]byte
	if err := uploads.ForEach(func(key, value []byte) error {
		if value != nil {
			legacyKeys = append(legacyKeys, append([]byte(nil), key...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, key := range legacyKeys {
		value := append([]byte(nil), uploads.Get(key)...)
		var entries []UploadEntry
		if err := json.Unmarshal(value, &entries); err != nil {
			return fmt.Errorf("decode legacy uploads for %s: %w", key, err)
		}
		if err := uploads.Delete(key); err != nil {
			return err
		}
		bucket, err := uploads.CreateBucket(key)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := putUploadEntry(bucket, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close releases the database file. The server defers it; the CLI closes after
// each command so the file is free again the moment the process exits.
func (s *TokenStore) Close() error { return s.db.Close() }

// putRecord serializes a record to its bucket key within tx.
func putRecord(tx *bolt.Tx, r *TokenRecord) error {
	v, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tokens := tx.Bucket(tokenBucket)
	index := tx.Bucket(authBucket)
	if old := tokens.Get([]byte(r.ID)); old != nil {
		var previous TokenRecord
		if json.Unmarshal(old, &previous) == nil {
			if key, ok := hashKey(previous.Hash); ok && string(index.Get(key)) == r.ID {
				if err := index.Delete(key); err != nil {
					return err
				}
			}
		}
	}
	if key, ok := hashKey(r.Hash); ok {
		if err := index.Put(key, []byte(r.ID)); err != nil {
			return err
		}
	}
	return tokens.Put([]byte(r.ID), v)
}

// getRecord loads a record by id within tx, returning ErrNotFound if absent.
func getRecord(tx *bolt.Tx, id string) (*TokenRecord, error) {
	v := tx.Bucket(tokenBucket).Get([]byte(id))
	if v == nil {
		return nil, ErrNotFound
	}
	var r TokenRecord
	if err := json.Unmarshal(v, &r); err != nil {
		return nil, fmt.Errorf("decode token %s: %w", id, err)
	}
	return &r, nil
}

// GetRecord fetches a token record by ID with hashes cleared.
func (s *TokenStore) GetRecord(id string) (TokenRecord, bool) {
	var rec TokenRecord
	var found bool
	_ = s.db.View(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err == nil {
			rec = *r
			rec.Hash = ""
			found = true
		}
		return nil
	})
	return rec, found
}

// readGlobal returns the server-wide default quota, or the zero (unlimited) value.
func readGlobal(tx *bolt.Tx) Limits {
	v := tx.Bucket(metaBucket).Get(globalKey)
	if v == nil {
		return Limits{}
	}
	var l Limits
	_ = json.Unmarshal(v, &l)
	return l
}

// enabledAdmins counts enabled tokens that can use the admin API (root or admin),
// excluding excludeID, within tx.
func enabledAdmins(tx *bolt.Tx, excludeID string) int {
	n := 0
	_ = tx.Bucket(tokenBucket).ForEach(func(_, v []byte) error {
		var r TokenRecord
		if json.Unmarshal(v, &r) == nil && IsAdmin(r.Role) && !r.Disabled && r.ID != excludeID {
			n++
		}
		return nil
	})
	return n
}

// Authenticate returns the record matching the presented secret, if it is known
// and enabled. The digest index makes lookup O(1); the stored digest is still
// compared in constant time to defend against a stale or corrupted index entry.
func (s *TokenStore) Authenticate(secret string) (TokenRecord, bool) {
	if len(secret) < 16 {
		return TokenRecord{}, false
	}
	sum := sha256.Sum256([]byte(secret))

	var match *TokenRecord
	_ = s.db.View(func(tx *bolt.Tx) error {
		id := tx.Bucket(authBucket).Get(sum[:])
		if id == nil {
			return nil
		}
		r, err := getRecord(tx, string(id))
		if err != nil {
			return nil
		}
		stored, ok := hashKey(r.Hash)
		if !ok || subtle.ConstantTimeCompare(stored, sum[:]) != 1 {
			return nil
		}
		match = r
		return nil
	})
	if match == nil || match.Disabled {
		return TokenRecord{}, false
	}
	match.LastUsed = time.Now().UTC()
	return *match, true
}

// Add creates a new token, returning its id and the one-time plaintext secret.
// The root role is accepted here (used by bootstrap); the API/CLI layers forbid
// creating it directly so it only ever comes from first run or reset.
func (s *TokenStore) Add(label, role string) (id, secret string, err error) {
	if !labelRe.MatchString(label) {
		return "", "", ErrInvalidLabel
	}
	if role != RoleRoot && role != RoleAdmin && role != RoleUpload {
		return "", "", fmt.Errorf("invalid role %q (want %q or %q)", role, RoleAdmin, RoleUpload)
	}
	secret, err = GenerateSecret()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(secret))
	rec := &TokenRecord{
		Label:     label,
		Role:      role,
		Hash:      hex.EncodeToString(sum[:]),
		CreatedAt: time.Now().UTC(),
	}

	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		for {
			rec.ID = randomID()
			if b.Get([]byte(rec.ID)) == nil {
				break
			}
		}
		return putRecord(tx, rec)
	})
	if err != nil {
		return "", "", err
	}
	return rec.ID, secret, nil
}

// Remove deletes a token by ID. The root token can't be removed, and neither
// can the last enabled admin (that would lock everyone out of the admin API).
func (s *TokenStore) Remove(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		if r.Role == RoleRoot {
			return ErrProtectedRoot
		}
		if r.Role == RoleAdmin && !r.Disabled && enabledAdmins(tx, id) == 0 {
			return ErrLastAdmin
		}
		if key, ok := hashKey(r.Hash); ok && string(tx.Bucket(authBucket).Get(key)) == id {
			if err := tx.Bucket(authBucket).Delete(key); err != nil {
				return err
			}
		}
		if pb := tx.Bucket(purgeBucket); pb != nil {
			_ = pb.Delete([]byte(id))
		}
		if gb := tx.Bucket(pendingBucket); gb != nil {
			_ = gb.Delete([]byte(id))
		}
		return tx.Bucket(tokenBucket).Delete([]byte(id))
	})
}

// SetDisabled enables or disables a token, with the same root and last-admin
// guards as Remove.
func (s *TokenStore) SetDisabled(id string, disabled bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		if r.Role == RoleRoot {
			return ErrProtectedRoot
		}
		if disabled && r.Role == RoleAdmin && enabledAdmins(tx, id) == 0 {
			return ErrLastAdmin
		}
		r.Disabled = disabled
		return putRecord(tx, r)
	})
}

// SetLabel updates the label for token id.
func (s *TokenStore) SetLabel(id, newLabel string) error {
	if !labelRe.MatchString(newLabel) {
		return ErrInvalidLabel
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		r.Label = newLabel
		return putRecord(tx, r)
	})
}

// SetRole changes a token's role between admin and upload. The root token is
// immutable, promoting to root is forbidden, and demoting the last enabled
// admin is blocked (same guard as Remove and SetDisabled).
func (s *TokenStore) SetRole(id, newRole string) error {
	if newRole != RoleAdmin && newRole != RoleUpload {
		return fmt.Errorf("invalid role %q (want %q or %q)", newRole, RoleAdmin, RoleUpload)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		if r.Role == RoleRoot {
			return ErrProtectedRoot
		}
		if r.Role == newRole {
			return nil // no-op
		}
		// Demoting the last enabled admin would lock everyone out.
		if r.Role == RoleAdmin && newRole == RoleUpload && !r.Disabled && enabledAdmins(tx, id) == 0 {
			return ErrLastAdmin
		}
		r.Role = newRole
		return putRecord(tx, r)
	})
}

// ReserveUpload atomically admits one in-flight upload and returns its opaque
// reservation plus the maximum number of bytes it may write. Outstanding
// reservations count against both count and byte quotas.
func (s *TokenStore) ReserveUpload(tokenID string, hardMax int64) (UploadReservation, int64, error) {
	s.reservationMu.Lock()
	defer s.reservationMu.Unlock()

	var budget int64
	err := s.db.View(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, tokenID)
		if err != nil {
			return err
		}
		if r.Disabled {
			return ErrTokenDisabled
		}
		now := time.Now().UTC()
		usage := r.Usage
		monthUploads, monthBytes := usage.thisMonth(now)
		usage.MonthUploads, usage.MonthBytes, usage.Period = monthUploads, monthBytes, now
		for _, pending := range s.reservations[tokenID] {
			usage.Uploads++
			usage.Bytes += pending.budget
			usage.MonthUploads++
			usage.MonthBytes += pending.budget
		}
		limits := EffectiveLimits(r.Limits, readGlobal(tx), r.BypassesGlobal())
		budget, err = limits.budget(usage, now, hardMax)
		return err
	})
	if err != nil {
		return "", 0, err
	}
	if s.reservations[tokenID] == nil {
		s.reservations[tokenID] = make(map[UploadReservation]uploadReservation)
	}
	var reservation UploadReservation
	for {
		reservation = UploadReservation(randomID())
		if _, exists := s.reservations[tokenID][reservation]; !exists {
			break
		}
	}
	s.reservations[tokenID][reservation] = uploadReservation{budget: budget}
	return reservation, budget, nil
}

// CancelUpload releases an in-flight upload without changing persistent usage.
func (s *TokenStore) CancelUpload(tokenID string, reservation UploadReservation) error {
	s.reservationMu.Lock()
	defer s.reservationMu.Unlock()
	return s.releaseReservation(tokenID, reservation)
}

func (s *TokenStore) releaseReservation(tokenID string, reservation UploadReservation) error {
	pending := s.reservations[tokenID]
	if _, ok := pending[reservation]; !ok {
		return ErrReservationMissing
	}
	delete(pending, reservation)
	if len(pending) == 0 {
		delete(s.reservations, tokenID)
	}
	return nil
}

// CommitUpload atomically bills an admitted upload and records its history
// entry, then releases the reservation. Current token state and quotas are
// revalidated so deletion, disabling, or quota changes during transfer are safe.
func (s *TokenStore) CommitUpload(tokenID string, reservation UploadReservation, entry UploadEntry) error {
	s.reservationMu.Lock()
	defer s.reservationMu.Unlock()

	pending, ok := s.reservations[tokenID][reservation]
	if !ok {
		return ErrReservationMissing
	}
	defer func() { _ = s.releaseReservation(tokenID, reservation) }()
	if entry.Size < 0 || entry.Size > pending.budget {
		return ErrQuotaBytes
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, tokenID)
		if err != nil {
			return err
		}
		if r.Disabled {
			return ErrTokenDisabled
		}
		now := time.Now().UTC()
		usage := r.Usage
		monthUploads, monthBytes := usage.thisMonth(now)
		usage.MonthUploads, usage.MonthBytes, usage.Period = monthUploads, monthBytes, now
		for id, other := range s.reservations[tokenID] {
			if id == reservation {
				continue
			}
			usage.Uploads++
			usage.Bytes += other.budget
			usage.MonthUploads++
			usage.MonthBytes += other.budget
		}
		limits := EffectiveLimits(r.Limits, readGlobal(tx), r.BypassesGlobal())
		budget, err := limits.budget(usage, now, entry.Size)
		if err != nil {
			return err
		}
		if budget < entry.Size {
			return ErrQuotaBytes
		}

		if !samePeriod(r.Usage.Period, now) {
			r.Usage.MonthUploads = 0
			r.Usage.MonthBytes = 0
			r.Usage.Period = now
		}
		r.Usage.Uploads++
		r.Usage.Bytes += entry.Size
		r.Usage.MonthUploads++
		r.Usage.MonthBytes += entry.Size
		r.LastUsed = now
		if err := putRecord(tx, r); err != nil {
			return err
		}
		bucket, err := tx.Bucket(uploadBucket).CreateBucketIfNotExists([]byte(tokenID))
		if err != nil {
			return err
		}
		return putUploadEntry(bucket, entry)
	})
}

// AllowUpload checks token id against its quotas and returns the largest number
// of bytes the pending upload may write, clamped to hardMax. It returns
// ErrQuotaUploads or ErrQuotaBytes if a quota is already exhausted, or
// ErrNotFound if the token vanished since it authenticated.
func (s *TokenStore) AllowUpload(id string, hardMax int64) (int64, error) {
	var budget int64
	err := s.db.View(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		lim := EffectiveLimits(r.Limits, readGlobal(tx), r.BypassesGlobal())
		budget, err = lim.budget(r.Usage, time.Now().UTC(), hardMax)
		return err
	})
	return budget, err
}

// RecordUpload bills a successful upload of n bytes to token id, rolling the
// monthly window when the calendar month changes and persisting the result so
// quotas and stats survive restarts. A token that vanished mid-upload is a
// no-op (ErrNotFound), since there is nothing left to bill.
func (s *TokenStore) RecordUpload(id string, n int64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if !samePeriod(r.Usage.Period, now) {
			r.Usage.MonthUploads = 0
			r.Usage.MonthBytes = 0
			r.Usage.Period = now
		}
		r.Usage.Uploads++
		r.Usage.Bytes += n
		r.Usage.MonthUploads++
		r.Usage.MonthBytes += n
		r.LastUsed = now
		return putRecord(tx, r)
	})
}

// SetLimits replaces the personal quotas for token id and whether it bypasses
// the global default. Negative caps are treated as unlimited.
func (s *TokenStore) SetLimits(id string, lim Limits, bypass bool) error {
	lim = lim.sanitized()
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		r.Limits = lim
		r.Bypass = bypass
		return putRecord(tx, r)
	})
}

// SetInvites updates the remaining invite token creation count for token id.
func (s *TokenStore) SetInvites(id string, invites int) error {
	if invites < 0 {
		invites = 0
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return err
		}
		r.Invites = invites
		return putRecord(tx, r)
	})
}

// AddInvitesToAllUploaders adds count invite credits to every token with the upload role.
// Returns the number of tokens updated.
func (s *TokenStore) AddInvitesToAllUploaders(count int) (int, error) {
	return s.AddInvitesToAllUploadersCapped(count, 0)
}

// AddInvitesToAllUploadersCapped adds count invite credits to every upload token.
// If maxCap > 0, each user's invites are clamped to maxCap.
func (s *TokenStore) AddInvitesToAllUploadersCapped(count, maxCap int) (int, error) {
	if count <= 0 {
		return 0, nil
	}
	updated := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		var targets []TokenRecord
		_ = b.ForEach(func(_, v []byte) error {
			var r TokenRecord
			if json.Unmarshal(v, &r) == nil && r.Role == RoleUpload && !r.Disabled {
				if maxCap > 0 && r.Invites >= maxCap {
					return nil // already at cap
				}
				r.Invites += count
				if maxCap > 0 && r.Invites > maxCap {
					r.Invites = maxCap
				}
				targets = append(targets, r)
			}
			return nil
		})
		for i := range targets {
			if err := putRecord(tx, &targets[i]); err != nil {
				return err
			}
			updated++
		}
		return nil
	})
	return updated, err
}

// AddInvitesToRandomUploaders gives count invites to poolSize randomly selected
// upload tokens. If maxCap > 0, each user's invites are clamped.
func (s *TokenStore) AddInvitesToRandomUploaders(count, poolSize, maxCap int) (int, error) {
	if count <= 0 || poolSize <= 0 {
		return 0, nil
	}
	updated := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		var eligible []TokenRecord
		_ = b.ForEach(func(_, v []byte) error {
			var r TokenRecord
			if json.Unmarshal(v, &r) == nil && r.Role == RoleUpload && !r.Disabled {
				if maxCap <= 0 || r.Invites < maxCap {
					eligible = append(eligible, r)
				}
			}
			return nil
		})
		if len(eligible) == 0 {
			return nil
		}
		// Shuffle and pick
		for i := len(eligible) - 1; i > 0; i-- {
			nBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
			if err != nil {
				return err
			}
			j := int(nBig.Int64())
			eligible[i], eligible[j] = eligible[j], eligible[i]
		}
		n := min(poolSize, len(eligible))
		for i := range n {
			eligible[i].Invites += count
			if maxCap > 0 && eligible[i].Invites > maxCap {
				eligible[i].Invites = maxCap
			}
			if err := putRecord(tx, &eligible[i]); err != nil {
				return err
			}
			updated++
		}
		return nil
	})
	return updated, err
}

// AddWithInvite creates a new upload token using an invite from creatorID.
// If creatorID is an admin/root token, it has unlimited invites.
// Otherwise, creatorID must have Invites > 0, which is decremented by 1.
func (s *TokenStore) AddWithInvite(creatorID, label string) (id, secret string, err error) {
	if !labelRe.MatchString(label) {
		return "", "", ErrInvalidLabel
	}
	secret, err = GenerateSecret()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(secret))
	rec := &TokenRecord{
		Label:     label,
		Role:      RoleUpload,
		Hash:      hex.EncodeToString(sum[:]),
		CreatedAt: time.Now().UTC(),
	}

	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		creator, err := getRecord(tx, creatorID)
		if err != nil {
			return err
		}
		if !IsAdmin(creator.Role) {
			if creator.Invites <= 0 {
				return ErrNoInvites
			}
			creator.Invites--
			if err := putRecord(tx, creator); err != nil {
				return err
			}
		}

		for {
			rec.ID = randomID()
			if b.Get([]byte(rec.ID)) == nil {
				break
			}
		}
		return putRecord(tx, rec)
	})
	if err != nil {
		return "", "", err
	}
	// Schedule delayed invite grant for new members if policy is enabled.
	_ = s.ScheduleNewUserGrant(rec.ID)
	return rec.ID, secret, nil
}

// ScheduleNewUserGrant adds a pending grant that fires after delay seconds.
// Called automatically when a user is created via invite and newuser policy is enabled.
func (s *TokenStore) ScheduleNewUserGrant(tokenID string) error {
	pol := s.InvitePolicy()
	if !pol.NewUserEnabled || pol.NewUserCount <= 0 {
		return nil
	}
	g := PendingGrant{
		TokenID: tokenID,
		Count:   pol.NewUserCount,
		MaxCap:  pol.NewUserMax,
		GrantAt: time.Now().UTC().Add(time.Duration(pol.NewUserDelay) * time.Second),
	}
	v, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(pendingBucket).Put([]byte(tokenID), v)
	})
}

// ProcessPendingGrants applies all grants whose GrantAt has passed, then deletes them.
// Returns the number of grants applied.
func (s *TokenStore) ProcessPendingGrants() (int, error) {
	now := time.Now().UTC()
	applied := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		pb := tx.Bucket(pendingBucket)
		var done [][]byte
		_ = pb.ForEach(func(k, v []byte) error {
			var g PendingGrant
			if json.Unmarshal(v, &g) != nil {
				done = append(done, k) // corrupted, remove
				return nil
			}
			if now.Before(g.GrantAt) {
				return nil // not yet due
			}
			r, err := getRecord(tx, g.TokenID)
			if err != nil {
				done = append(done, k) // token deleted, clean up
				return nil
			}
			if g.MaxCap > 0 && r.Invites >= g.MaxCap {
				done = append(done, k)
				return nil
			}
			r.Invites += g.Count
			if g.MaxCap > 0 && r.Invites > g.MaxCap {
				r.Invites = g.MaxCap
			}
			if err := putRecord(tx, r); err != nil {
				return err
			}
			applied++
			done = append(done, k)
			return nil
		})
		for _, k := range done {
			_ = pb.Delete(k)
		}
		return nil
	})
	return applied, err
}

// InvitePolicy returns the current server-wide invite distribution policy.
func (s *TokenStore) InvitePolicy() InvitePolicy {
	var pol InvitePolicy
	_ = s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(metaBucket).Get(invitePolicyKey)
		if v != nil {
			_ = json.Unmarshal(v, &pol)
		}
		return nil
	})
	return pol
}

// SetInvitePolicy replaces the server-wide invite distribution policy.
func (s *TokenStore) SetInvitePolicy(pol InvitePolicy) error {
	v, err := json.Marshal(pol)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(metaBucket).Put(invitePolicyKey, v)
	})
}

// SetGlobalLimits replaces the server-wide default quota applied to every token
// that does not override a dimension personally or bypass the global entirely.
func (s *TokenStore) SetGlobalLimits(lim Limits) error {
	lim = lim.sanitized()
	v, err := json.Marshal(lim)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(metaBucket).Put(globalKey, v)
	})
}

// GlobalLimits returns the current server-wide default quota.
func (s *TokenStore) GlobalLimits() Limits {
	var lim Limits
	_ = s.db.View(func(tx *bolt.Tx) error {
		lim = readGlobal(tx)
		return nil
	})
	return lim
}

// LimitsOf returns the current personal quotas and bypass flag for token id,
// used by the CLI to support partial updates that preserve what the caller did
// not touch. ok is false when no such token exists.
func (s *TokenStore) LimitsOf(id string) (lim Limits, bypass, ok bool) {
	_ = s.db.View(func(tx *bolt.Tx) error {
		r, err := getRecord(tx, id)
		if err != nil {
			return nil
		}
		lim, bypass, ok = r.Limits, r.Bypass, true
		return nil
	})
	return lim, bypass, ok
}

// List returns a copy of all records with hashes stripped, oldest first.
func (s *TokenStore) List() []TokenRecord {
	out := s.records()
	for i := range out {
		out[i].Hash = ""
	}
	return out
}

// records reads every token verbatim (hashes intact), oldest first. List strips
// hashes for API safety; the CLI's dump keeps them.
func (s *TokenStore) records() []TokenRecord {
	var out []TokenRecord
	_ = s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(tokenBucket).ForEach(func(_, v []byte) error {
			var r TokenRecord
			if json.Unmarshal(v, &r) == nil {
				out = append(out, r)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Count returns the number of tokens in the store.
func (s *TokenStore) Count() int {
	n := 0
	_ = s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(tokenBucket).Stats().KeyN
		return nil
	})
	return n
}

// Bootstrap mints the immutable root token if the store is empty.
func (s *TokenStore) Bootstrap() (secret string, created bool, err error) {
	if s.Count() != 0 {
		return "", false, nil
	}
	_, secret, err = s.Add("root", RoleRoot)
	if err != nil {
		return "", false, err
	}
	return secret, true, nil
}

// Ping checks if the database is open and responsive.
func (s *TokenStore) Ping() error {
	return s.db.View(func(tx *bolt.Tx) error {
		_ = tx.Bucket(tokenBucket).Stats()
		return nil
	})
}

func putUploadEntry(bucket *bolt.Bucket, entry UploadEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(entry.Name), data)
}

func readUploadEntries(bucket *bolt.Bucket) ([]UploadEntry, error) {
	if bucket == nil {
		return nil, nil
	}
	entries := make([]UploadEntry, 0, bucket.Stats().KeyN)
	err := bucket.ForEach(func(_, value []byte) error {
		if value == nil {
			return nil
		}
		var entry UploadEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UploadedAt.Equal(entries[j].UploadedAt) {
			return entries[i].Name > entries[j].Name
		}
		return entries[i].UploadedAt.After(entries[j].UploadedAt)
	})
	return entries, nil
}

// RecordUploadEntry records a file in the token's upload history by filename.
func (s *TokenStore) RecordUploadEntry(tokenID string, entry UploadEntry) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.Bucket(uploadBucket).CreateBucketIfNotExists([]byte(tokenID))
		if err != nil {
			return err
		}
		return putUploadEntry(bucket, entry)
	})
}

// AllUploadEntries returns every upload entry across all tokens, keyed by token
// ID. The scanner uses this to identify which files on disk are already tracked.
func (s *TokenStore) AllUploadEntries() (map[string][]UploadEntry, error) {
	out := make(map[string][]UploadEntry)
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket(uploadBucket)
		return root.ForEach(func(tokenID, value []byte) error {
			if value != nil {
				return nil
			}
			entries, err := readUploadEntries(root.Bucket(tokenID))
			if err != nil {
				return err
			}
			out[string(tokenID)] = entries
			return nil
		})
	})
	return out, err
}

// ImportUploadEntries appends upload entries to the given token, updating its
// usage counters to reflect the imported files. The token must already exist.
// This is used by the directory scanner to adopt pre-existing files.
func (s *TokenStore) ImportUploadEntries(tokenID string, entries []UploadEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		// Verify the token exists.
		r, err := getRecord(tx, tokenID)
		if err != nil {
			return err
		}

		// Add each file directly to the token's nested history bucket.
		bucket, err := tx.Bucket(uploadBucket).CreateBucketIfNotExists([]byte(tokenID))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := putUploadEntry(bucket, entry); err != nil {
				return err
			}
		}

		// Update usage counters so quotas and stats reflect imported files.
		now := time.Now().UTC()
		for _, e := range entries {
			if !samePeriod(r.Usage.Period, now) {
				r.Usage.MonthUploads = 0
				r.Usage.MonthBytes = 0
				r.Usage.Period = now
			}
			r.Usage.Uploads++
			r.Usage.Bytes += e.Size
			r.Usage.MonthUploads++
			r.Usage.MonthBytes += e.Size
		}
		r.LastUsed = now
		return putRecord(tx, r)
	})
}

// UploadsFor returns all upload entries for a given token, newest first.
func (s *TokenStore) UploadsFor(tokenID string) ([]UploadEntry, error) {
	var entries []UploadEntry
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		entries, err = readUploadEntries(tx.Bucket(uploadBucket).Bucket([]byte(tokenID)))
		return err
	})
	return entries, err
}

// FileIndex is a concurrency-safe in-memory reverse index mapping upload
// filenames to the token ID that owns them. It is built once at startup from
// the uploads bucket and kept in sync by Add/Remove calls on every upload
// and delete.
type FileIndex struct {
	mu        sync.RWMutex
	files     map[string]string // filename → token ID
	fullNames map[string]string // baseName → full filename
}

// BuildFileIndex constructs a FileIndex from every upload entry in the store.
// Call once at server startup; the returned index is then passed to handlers.
func BuildFileIndex(s *TokenStore) (*FileIndex, error) {
	idx := &FileIndex{
		files:     make(map[string]string),
		fullNames: make(map[string]string),
	}
	all, err := s.AllUploadEntries()
	if err != nil {
		return nil, err
	}
	for tokenID, entries := range all {
		for _, e := range entries {
			idx.files[e.Name] = tokenID
			if ext := filepath.Ext(e.Name); ext != "" {
				base := strings.TrimSuffix(e.Name, ext)
				idx.fullNames[base] = e.Name
			}
		}
	}
	return idx, nil
}

// Lookup returns the token ID and full stored filename for a given name or base name.
func (idx *FileIndex) Lookup(name string) (ownerID, fullName string) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if id, ok := idx.files[name]; ok {
		return id, name
	}
	if full, ok := idx.fullNames[name]; ok {
		return idx.files[full], full
	}
	if ext := filepath.Ext(name); ext != "" {
		base := strings.TrimSuffix(name, ext)
		if full, ok := idx.fullNames[base]; ok {
			return idx.files[full], full
		}
	}
	return "", ""
}

// Owner returns the token ID that owns the given filename or base name, or an empty string if not found.
func (idx *FileIndex) Owner(filename string) string {
	ownerID, _ := idx.Lookup(filename)
	return ownerID
}

// Add associates a filename with a token ID in the index.
func (idx *FileIndex) Add(filename, tokenID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.files[filename] = tokenID
	if ext := filepath.Ext(filename); ext != "" {
		base := strings.TrimSuffix(filename, ext)
		idx.fullNames[base] = filename
	}
}

// Remove deletes a filename from the index.
func (idx *FileIndex) Remove(filename string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.files, filename)
	if ext := filepath.Ext(filename); ext != "" {
		base := strings.TrimSuffix(filename, ext)
		delete(idx.fullNames, base)
	} else {
		delete(idx.fullNames, filename)
	}
}

// RemoveAll deletes all filenames associated with a token ID from the index,
// returning the list of filenames removed.
func (idx *FileIndex) RemoveAll(tokenID string) []string {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	var removed []string
	for name, id := range idx.files {
		if id == tokenID {
			removed = append(removed, name)
			delete(idx.files, name)
			if ext := filepath.Ext(name); ext != "" {
				base := strings.TrimSuffix(name, ext)
				delete(idx.fullNames, base)
			}
		}
	}
	return removed
}

// Count returns the total number of indexed files.
func (idx *FileIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.files)
}

// RemoveUploadEntry removes a specific upload entry by filename in O(1).
func (s *TokenStore) RemoveUploadEntry(tokenID, filename string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(uploadBucket).Bucket([]byte(tokenID))
		if bucket == nil {
			return nil
		}
		return bucket.Delete([]byte(filename))
	})
}

// RemoveAllUploadEntries deletes the entire upload entry list for a token.
func (s *TokenStore) RemoveAllUploadEntries(tokenID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(uploadBucket)
		if root.Bucket([]byte(tokenID)) == nil {
			return nil
		}
		return root.DeleteBucket([]byte(tokenID))
	})
}

// SchedulePurge records a pending media purge for a token after the given delay.
func (s *TokenStore) SchedulePurge(tokenID, actorID string, delay time.Duration) (PendingPurge, error) {
	now := time.Now().UTC()
	p := PendingPurge{
		TokenID:     tokenID,
		RequestedAt: now,
		PurgeAt:     now.Add(delay),
		ActorID:     actorID,
	}
	v, err := json.Marshal(p)
	if err != nil {
		return PendingPurge{}, err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(purgeBucket).Put([]byte(tokenID), v)
	})
	if err != nil {
		return PendingPurge{}, err
	}
	return p, nil
}

// GetPendingPurge returns the pending purge for the given tokenID, if any.
func (s *TokenStore) GetPendingPurge(tokenID string) (PendingPurge, bool) {
	var p PendingPurge
	found := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(purgeBucket)
		if b == nil {
			return nil
		}
		v := b.Get([]byte(tokenID))
		if v == nil {
			return nil
		}
		if json.Unmarshal(v, &p) == nil {
			found = true
		}
		return nil
	})
	return p, found
}

// CancelPendingPurge removes a pending purge for the given tokenID.
// Returns true if a pending purge was cancelled, false if none was found.
func (s *TokenStore) CancelPendingPurge(tokenID string) (bool, error) {
	var cancelled bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(purgeBucket)
		if b == nil {
			return nil
		}
		if b.Get([]byte(tokenID)) != nil {
			cancelled = true
			return b.Delete([]byte(tokenID))
		}
		return nil
	})
	return cancelled, err
}

// ListPendingPurges returns all pending purges sorted by PurgeAt.
func (s *TokenStore) ListPendingPurges() ([]PendingPurge, error) {
	var list []PendingPurge
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(purgeBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var p PendingPurge
			if json.Unmarshal(v, &p) == nil {
				list = append(list, p)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].PurgeAt.Before(list[j].PurgeAt)
	})
	return list, nil
}

// ProcessDuePurges finds all pending purges whose PurgeAt has passed, invokes executePurge, and removes the pending record.
func (s *TokenStore) ProcessDuePurges(now time.Time, executePurge func(tokenID string) error) (int, error) {
	var due []PendingPurge
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(purgeBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var p PendingPurge
			if json.Unmarshal(v, &p) == nil {
				if !now.Before(p.PurgeAt) {
					due = append(due, p)
				}
			}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}

	executed := 0
	for _, p := range due {
		if executePurge != nil {
			_ = executePurge(p.TokenID)
		}
		_ = s.db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(purgeBucket)
			if b != nil {
				_ = b.Delete([]byte(p.TokenID))
			}
			return nil
		})
		executed++
	}
	return executed, nil
}
