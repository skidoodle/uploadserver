package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"uploadserver/internal"
)

// extRe validates a lowercased file extension (without the dot).
var extRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

var errNoFile = errors.New("no file part")

// handleUpload authenticates the request, streams the file part to disk under a
// random name, and returns the resulting public URL.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	cfg := s.cfg

	// Reject keys that have already exhausted a quota, and learn how many bytes
	// this upload may still write (the server-wide cap, tightened by whatever
	// storage budget the token has left).
	reservation, budget, err := s.store.ReserveUpload(rec.ID, cfg.MaxBytes)
	switch {
	case errors.Is(err, internal.ErrQuotaUploads), errors.Is(err, internal.ErrQuotaBytes):
		slog.Warn("quota limits hit", "id", rec.ID, "error", err, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		httpError(w, http.StatusTooManyRequests, err.Error())
		return
	case errors.Is(err, internal.ErrNotFound):
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	case err != nil:
		slog.Error("quota check failed", "id", rec.ID, "error", err, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		httpError(w, http.StatusInternalServerError, "could not check quota")
		return
	}
	quotaLimited := budget < cfg.MaxBytes
	defer func() { _ = s.store.CancelUpload(rec.ID, reservation) }()

	// Hard cap on the request body before touching the multipart parser.
	r.Body = http.MaxBytesReader(w, r.Body, budget)

	mr, err := r.MultipartReader()
	if err != nil {
		httpError(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}

	name, n, err := savePart(cfg, mr, rec.ID)
	switch {
	case errors.Is(err, errNoFile):
		httpError(w, http.StatusBadRequest, "no "+cfg.Field+" field in upload")
		return
	case isTooLarge(err):
		if quotaLimited {
			httpError(w, http.StatusRequestEntityTooLarge, "upload exceeds the token's remaining quota")
		} else {
			httpError(w, http.StatusRequestEntityTooLarge, "upload exceeds size limit")
		}
		return
	case err != nil:
		slog.Error("save upload error", "error", err, "id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		httpError(w, http.StatusInternalServerError, "could not store upload")
		return
	}

	entry := internal.UploadEntry{Name: name, Size: n, UploadedAt: time.Now().UTC()}
	if err := s.store.CommitUpload(rec.ID, reservation, entry); err != nil {
		disk := filepath.Join(cfg.Dir, rec.ID, name)
		// #nosec G703 -- disk is constructed from authenticated token ID and generated filename
		if removeErr := os.Remove(disk); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Error("remove uncommitted upload error", "id", rec.ID, "name", name, "error", removeErr)
		}
		slog.Error("commit upload error", "id", rec.ID, "error", err)
		if errors.Is(err, internal.ErrNotFound) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
			httpError(w, http.StatusUnauthorized, "unauthorized")
		} else {
			httpError(w, http.StatusInternalServerError, "could not commit upload")
		}
		return
	}

	if s.fileIndex != nil {
		s.fileIndex.Add(name, rec.ID)
	}

	url := publicURL(cfg, r, name)
	slog.Info("stored file",
		"name", internal.SanitizeLog(name),
		"size", n,
		"id", internal.SanitizeLog(rec.ID),
		"label", internal.SanitizeLog(rec.Label),
		"ip", internal.SanitizeLog(clientIP(r, s.cfg.TrustProxyHeaders)),
	)

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]string{"url": url})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, url) // #nosec G705 -- Content-Type is text/plain; charset=utf-8
}

// savePart finds the configured file field, writes it atomically to disk, and
// returns the generated object name and the number of bytes written. The data
// is streamed straight to a temp file (constant memory) then renamed into place.
func savePart(cfg internal.Config, mr *multipart.Reader, tokenID string) (name string, n int64, err error) {
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			return "", 0, errNoFile
		}
		if perr != nil {
			return "", 0, perr
		}

		if part.FormName() != cfg.Field || part.FileName() == "" {
			_ = part.Close()
			continue
		}

		name = randomName(cfg, extOf(part.FileName()))
		userDir := filepath.Join(cfg.Dir, tokenID)
		if err := os.MkdirAll(userDir, 0o750); err != nil {
			_ = part.Close()
			return "", 0, fmt.Errorf("create user dir: %w", err)
		}
		tmp, terr := os.CreateTemp(userDir, ".upload-*")
		if terr != nil {
			_ = part.Close()
			return "", 0, terr
		}
		tmpName := tmp.Name()

		n, err = io.Copy(tmp, part)
		_ = part.Close()
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return "", 0, err
		}
		if err = tmp.Sync(); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return "", 0, err
		}
		if err = tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			return "", 0, err
		}
		if err = os.Chmod(tmpName, 0o600); err != nil {
			_ = os.Remove(tmpName)
			return "", 0, err
		}
		if err = os.Rename(tmpName, filepath.Join(userDir, name)); err != nil { // #nosec G703 -- name is a generated hex string with validated extension
			_ = os.Remove(tmpName)
			return "", 0, err
		}
		return name, n, nil
	}
}

// publicURL builds the link returned to the client. With BASE_URL set (your CDN)
// it is BASE_URL/name; otherwise it falls back to the upload host.
func publicURL(cfg internal.Config, r *http.Request, name string) string {
	base := cfg.BaseURL
	if base == "" {
		scheme := "http"
		if requestIsHTTPS(r, cfg.TrustProxyHeaders) {
			scheme = "https"
		}
		host := r.Host
		if len(cfg.AllowedHosts) > 0 {
			matched := false
			for _, h := range cfg.AllowedHosts {
				if strings.EqualFold(h, host) {
					matched = true
					break
				}
			}
			if !matched {
				host = cfg.AllowedHosts[0]
			}
		}
		base = scheme + "://" + host
	}

	if cfg.StripExtension {
		if ext := filepath.Ext(name); ext != "" {
			name = strings.TrimSuffix(name, ext)
		}
	}

	return base + "/" + name
}

// randomName returns a random hex name of configured length with an optional extension.
func randomName(cfg internal.Config, ext string) string {
	length := cfg.NameLength
	if length <= 0 {
		length = 32
	}
	numBytes := (length + 1) / 2
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is catastrophic and not something to paper over.
		panic("crypto/rand: " + err.Error())
	}
	name := hex.EncodeToString(b)[:length]
	if ext != "" {
		name += "." + ext
	}
	return name
}

// extOf extracts a safe, lowercased extension from a client filename, or "".
func extOf(filename string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filepath.Base(filename)), "."))
	if extRe.MatchString(ext) {
		return ext
	}
	return ""
}

// isTooLarge reports whether err is the request body hitting the size cap.
func isTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
