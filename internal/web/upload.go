package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	budget, err := s.store.AllowUpload(rec.ID, cfg.MaxBytes)
	switch {
	case errors.Is(err, internal.ErrQuotaUploads), errors.Is(err, internal.ErrQuotaBytes):
		httpError(w, http.StatusTooManyRequests, err.Error())
		return
	case errors.Is(err, internal.ErrNotFound):
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	case err != nil:
		log.Printf("quota check for %s: %v", rec.ID, err)
		httpError(w, http.StatusInternalServerError, "could not check quota")
		return
	}
	quotaLimited := budget < cfg.MaxBytes

	// Hard cap on the request body before touching the multipart parser.
	r.Body = http.MaxBytesReader(w, r.Body, budget)

	mr, err := r.MultipartReader()
	if err != nil {
		httpError(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}

	name, n, err := savePart(cfg, mr)
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
		log.Printf("save: %v", err)
		httpError(w, http.StatusInternalServerError, "could not store upload")
		return
	}

	if err := s.store.RecordUpload(rec.ID, n); err != nil {
		log.Printf("record usage for %s: %v", rec.ID, err)
	}

	url := publicURL(cfg, r, name)
	log.Printf("stored %s (%d bytes) by token %s (%s) from %s", name, n, rec.ID, rec.Label, clientIP(r))

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]string{"url": url})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, url)
}

// savePart finds the configured file field, writes it atomically to disk, and
// returns the generated object name and the number of bytes written. The data
// is streamed straight to a temp file (constant memory) then renamed into place.
func savePart(cfg internal.Config, mr *multipart.Reader) (name string, n int64, err error) {
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
		tmp, terr := os.CreateTemp(cfg.Dir, ".upload-*")
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
		if err = os.Chmod(tmpName, 0o644); err != nil {
			_ = os.Remove(tmpName)
			return "", 0, err
		}
		if err = os.Rename(tmpName, filepath.Join(cfg.Dir, name)); err != nil {
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
		if requestIsHTTPS(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
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
