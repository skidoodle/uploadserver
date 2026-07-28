package internal

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	ErrNotFound      = errors.New("token not found")
	ErrLastAdmin     = errors.New("refusing to remove or disable the last enabled admin token")
	ErrProtectedRoot = errors.New("the root token cannot be deleted or disabled; use `token reset` to replace it")
	ErrInvalidLabel  = errors.New("label must be 1-9 characters, starting and ending with an alphanumeric character (can contain underscores or hyphens in the middle)")
	ErrNoInvites     = errors.New("no invites remaining")
	// ErrLocked is returned when another process (usually the running server)
	// already holds the database open.
	ErrLocked = errors.New("token store is locked by another process; stop the server or use the dashboard to manage tokens while it runs")
)

var labelRe = regexp.MustCompile("^[a-zA-Z0-9]([a-zA-Z0-9_-]{0,7}[a-zA-Z0-9])?$")

// bbolt key space: every token record lives in tokenBucket keyed by its id, and
// the single server-wide quota lives in metaBucket under globalKey.
var (
	tokenBucket  = []byte("tokens")
	metaBucket   = []byte("meta")
	globalKey    = []byte("global")
	uploadBucket = []byte("uploads")
)

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
		_, err := tx.CreateBucketIfNotExists(metaBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &TokenStore{db: db}, nil
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
	return tx.Bucket(tokenBucket).Put([]byte(r.ID), v)
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
// and enabled. Every record is compared in constant time to avoid leaking, via
// timing, which (if any) token matched. The returned LastUsed reflects now but
// is not persisted on its own — billing an upload is what writes it back.
func (s *TokenStore) Authenticate(secret string) (TokenRecord, bool) {
	if len(secret) < 16 {
		return TokenRecord{}, false
	}
	sum := sha256.Sum256([]byte(secret))

	var match *TokenRecord
	_ = s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(tokenBucket).ForEach(func(_, v []byte) error {
			var r TokenRecord
			if json.Unmarshal(v, &r) != nil {
				return nil
			}
			hb, err := hex.DecodeString(r.Hash)
			if err != nil || len(hb) != sha256.Size {
				return nil
			}
			if subtle.ConstantTimeCompare(hb, sum[:]) == 1 {
				rec := r
				match = &rec
			}
			return nil
		})
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
	if count <= 0 {
		return 0, nil
	}
	updated := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		var targets []TokenRecord
		_ = b.ForEach(func(_, v []byte) error {
			var r TokenRecord
			if json.Unmarshal(v, &r) == nil && r.Role == RoleUpload {
				r.Invites += count
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
	return rec.ID, secret, nil
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

// RecordUploadEntry appends a file record to the token's upload history.
func (s *TokenStore) RecordUploadEntry(tokenID string, entry UploadEntry) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(uploadBucket)
		var entries []UploadEntry
		if v := b.Get([]byte(tokenID)); v != nil {
			_ = json.Unmarshal(v, &entries)
		}
		entries = append(entries, entry)
		data, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		return b.Put([]byte(tokenID), data)
	})
}

// AllUploadEntries returns every upload entry across all tokens, keyed by token
// ID. The scanner uses this to identify which files on disk are already tracked.
func (s *TokenStore) AllUploadEntries() (map[string][]UploadEntry, error) {
	out := make(map[string][]UploadEntry)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(uploadBucket).ForEach(func(k, v []byte) error {
			var entries []UploadEntry
			if json.Unmarshal(v, &entries) == nil {
				out[string(k)] = entries
			}
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

		// Append to upload history.
		ub := tx.Bucket(uploadBucket)
		var existing []UploadEntry
		if v := ub.Get([]byte(tokenID)); v != nil {
			_ = json.Unmarshal(v, &existing)
		}
		existing = append(existing, entries...)
		data, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		if err := ub.Put([]byte(tokenID), data); err != nil {
			return err
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
		v := tx.Bucket(uploadBucket).Get([]byte(tokenID))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &entries)
	})
	if err != nil {
		return nil, err
	}
	// Reverse so newest first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}
