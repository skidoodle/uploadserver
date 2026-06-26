package internal

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Usage accumulates how much a token has uploaded. The lifetime counters never
// reset; the Month* counters cover the calendar month identified by Period and
// roll back to zero the first time the token is used in a later month.
type Usage struct {
	Uploads      int64     `json:"uploads,omitempty"`
	Bytes        int64     `json:"bytes,omitempty"`
	MonthUploads int64     `json:"month_uploads,omitempty"`
	MonthBytes   int64     `json:"month_bytes,omitempty"`
	Period       time.Time `json:"period,omitzero"`
}

// Limits caps what a token may upload. A zero in any field leaves that
// dimension unlimited, so the zero value is "no quota at all".
type Limits struct {
	MaxBytes       int64 `json:"max_bytes,omitempty"`       // lifetime total size
	MaxUploads     int64 `json:"max_uploads,omitempty"`     // lifetime upload count
	MonthlyBytes   int64 `json:"monthly_bytes,omitempty"`   // size per calendar month
	MonthlyUploads int64 `json:"monthly_uploads,omitempty"` // uploads per calendar month
}

var (
	// ErrQuotaUploads is returned when a token has hit an upload-count quota.
	ErrQuotaUploads = errors.New("upload count quota reached for this token")
	// ErrQuotaBytes is returned when a token has hit a storage-size quota.
	ErrQuotaBytes = errors.New("storage quota reached for this token")
)

// Any reports whether at least one quota dimension is set.
func (l Limits) Any() bool {
	return l.MaxBytes > 0 || l.MaxUploads > 0 || l.MonthlyBytes > 0 || l.MonthlyUploads > 0
}

// sanitized returns the limits with any negative (nonsensical) caps treated as
// unlimited, so callers can pass user input straight through.
func (l Limits) sanitized() Limits {
	clamp := func(n int64) int64 {
		if n < 0 {
			return 0
		}
		return n
	}
	return Limits{
		MaxBytes:       clamp(l.MaxBytes),
		MaxUploads:     clamp(l.MaxUploads),
		MonthlyBytes:   clamp(l.MonthlyBytes),
		MonthlyUploads: clamp(l.MonthlyUploads),
	}
}

// EffectiveLimits resolves the quota actually enforced for a token. A token
// flagged to bypass is fully exempt — no quota at all. Otherwise its personal
// caps layer over the server-wide global default: each dimension uses the
// personal value when set and falls back to the global value when not.
func EffectiveLimits(personal, global Limits, bypass bool) Limits {
	if bypass {
		return Limits{}
	}
	pick := func(p, g int64) int64 {
		if p != 0 {
			return p
		}
		return g
	}
	return Limits{
		MaxBytes:       pick(personal.MaxBytes, global.MaxBytes),
		MaxUploads:     pick(personal.MaxUploads, global.MaxUploads),
		MonthlyBytes:   pick(personal.MonthlyBytes, global.MonthlyBytes),
		MonthlyUploads: pick(personal.MonthlyUploads, global.MonthlyUploads),
	}
}

// SummarizeLimits renders a quota as a compact "5 GB · 1,000 uploads · …"
// string, or "" when nothing is capped. Used by the dashboard's collapsed
// global-quota summary and (via limitSummary) the CLI.
func SummarizeLimits(l Limits) string {
	var parts []string
	if l.MaxBytes > 0 {
		parts = append(parts, FormatSize(l.MaxBytes))
	}
	if l.MaxUploads > 0 {
		parts = append(parts, Comma(l.MaxUploads)+" uploads")
	}
	if l.MonthlyBytes > 0 {
		parts = append(parts, FormatSize(l.MonthlyBytes)+"/mo")
	}
	if l.MonthlyUploads > 0 {
		parts = append(parts, Comma(l.MonthlyUploads)+" uploads/mo")
	}
	return strings.Join(parts, " · ")
}

// samePeriod reports whether two instants fall in the same calendar month.
func samePeriod(a, b time.Time) bool {
	ay, am, _ := a.UTC().Date()
	by, bm, _ := b.UTC().Date()
	return ay == by && am == bm
}

// thisMonth returns the uploads and bytes already used in now's calendar month,
// reading a stale period as a fresh (zeroed) one without mutating anything.
func (u Usage) thisMonth(now time.Time) (uploads, bytes int64) {
	if samePeriod(u.Period, now) {
		return u.MonthUploads, u.MonthBytes
	}
	return 0, 0
}

// MonthUploadsNow and MonthBytesNow expose the current-month figures to callers
// (and HTML templates) that only have a record in hand, not the clock.
func (u Usage) MonthUploadsNow() int64 { n, _ := u.thisMonth(time.Now()); return n }
func (u Usage) MonthBytesNow() int64   { _, n := u.thisMonth(time.Now()); return n }

// budget reports the largest number of bytes an upload may write for a token
// with these limits and usage, clamped to the server-wide cap hardMax. It
// returns an error when a count- or size-based quota is already exhausted.
func (l Limits) budget(u Usage, now time.Time, hardMax int64) (int64, error) {
	mUploads, mBytes := u.thisMonth(now)

	if l.MaxUploads > 0 && u.Uploads >= l.MaxUploads {
		return 0, ErrQuotaUploads
	}
	if l.MonthlyUploads > 0 && mUploads >= l.MonthlyUploads {
		return 0, ErrQuotaUploads
	}

	budget := hardMax
	clamp := func(remaining int64) error {
		if remaining <= 0 {
			return ErrQuotaBytes
		}
		if remaining < budget {
			budget = remaining
		}
		return nil
	}
	if l.MaxBytes > 0 {
		if err := clamp(l.MaxBytes - u.Bytes); err != nil {
			return 0, err
		}
	}
	if l.MonthlyBytes > 0 {
		if err := clamp(l.MonthlyBytes - mBytes); err != nil {
			return 0, err
		}
	}
	return budget, nil
}

// sizeUnits maps human size suffixes to their byte scale, longest first so that
// e.g. "gib" is matched before "gb" and "gb" before the bare "b".
var sizeUnits = []struct {
	suffix string
	scale  int64
}{
	{"tib", 1 << 40}, {"tb", 1 << 40},
	{"gib", 1 << 30}, {"gb", 1 << 30},
	{"mib", 1 << 20}, {"mb", 1 << 20},
	{"kib", 1 << 10}, {"kb", 1 << 10},
	{"b", 1},
}

// ParseSize converts a human size such as "5GB", "500 mb" or "1073741824" into
// bytes. Blank, "0", "off", "none" and "unlimited" all parse to 0, the value
// used throughout to mean "no cap". Units are binary (1 GB = 1024 MB).
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "", "0", "off", "none", "unlimited":
		return 0, nil
	}

	scale := int64(1)
	for _, u := range sizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			scale = u.scale
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	bytes := f * float64(scale)
	if bytes > math.MaxInt64 {
		return 0, errors.New("size is too large")
	}
	return int64(bytes), nil
}

// FormatSize renders a byte count as a short human string like "3.2 GB". It is
// the display inverse of ParseSize (binary units, one trimmed decimal place).
func FormatSize(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	val := strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64)
	val = strings.TrimSuffix(val, ".0")
	return val + " " + [...]string{"KB", "MB", "GB", "TB", "PB"}[exp]
}

// Comma groups an integer with thousands separators, e.g. 12345 -> "12,345".
func Comma(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	head := len(s) % 3
	if head == 0 {
		head = 3
	}
	var b strings.Builder
	b.WriteString(s[:head])
	for i := head; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
