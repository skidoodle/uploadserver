package web

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"uploadserver/internal"
)

// fileRule pairs a Cache-Control value with an optional Content-Disposition
// for a category of file types.
type fileRule struct {
	control     string
	disposition string // non-empty implies Content-Disposition: attachment
}

var fileRules = map[string]fileRule{
	".jpg":   {control: "public, max-age=31536000, immutable"},
	".jpeg":  {control: "public, max-age=31536000, immutable"},
	".png":   {control: "public, max-age=31536000, immutable"},
	".gif":   {control: "public, max-age=31536000, immutable"},
	".webp":  {control: "public, max-age=31536000, immutable"},
	".avif":  {control: "public, max-age=31536000, immutable"},
	".bmp":   {control: "public, max-age=31536000, immutable"},
	".ico":   {control: "public, max-age=31536000, immutable"},
	".heic":  {control: "public, max-age=31536000, immutable"},
	".svg":   {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".mp4":   {control: "public, max-age=31536000, immutable"},
	".webm":  {control: "public, max-age=31536000, immutable"},
	".mov":   {control: "public, max-age=31536000, immutable"},
	".mp3":   {control: "public, max-age=31536000, immutable"},
	".flac":  {control: "public, max-age=31536000, immutable"},
	".txt":   {control: "public, max-age=31536000, immutable"},
	".html":  {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".htm":   {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".xhtml": {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".mhtml": {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".xml":   {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".js":    {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".mjs":   {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".css":   {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".json":  {control: "public, max-age=31536000, immutable", disposition: "attachment"},
	".yaml":  {control: "public, max-age=31536000, immutable"},
	".yml":   {control: "public, max-age=31536000, immutable"},
	".csv":   {control: "public, max-age=31536000, immutable"},
	".conf":  {control: "public, max-age=31536000, immutable"},
	".sh":    {control: "public, max-age=31536000, immutable"},
	".pdf":   {control: "public, max-age=31536000, immutable"},
	// Downloads: force-download instead of inline.
	".zip":  {control: "public, max-age=31536000", disposition: "attachment"},
	".rar":  {control: "public, max-age=31536000", disposition: "attachment"},
	".7z":   {control: "public, max-age=31536000", disposition: "attachment"},
	".gz":   {control: "public, max-age=31536000", disposition: "attachment"},
	".exe":  {control: "public, max-age=31536000", disposition: "attachment"},
	".jar":  {control: "public, max-age=31536000", disposition: "attachment"},
	".so":   {control: "public, max-age=31536000", disposition: "attachment"},
	".pdn":  {control: "public, max-age=31536000", disposition: "attachment"},
	".woff": {control: "public, max-age=31536000", disposition: "attachment"},
	".ttf":  {control: "public, max-age=31536000", disposition: "attachment"},
}

// gzipExts is the set of extensions worth compressing in transit.
var gzipExts = map[string]bool{
	".txt": true, ".html": true, ".mhtml": true, ".css": true,
	".json": true, ".yaml": true, ".yml": true, ".csv": true,
	".conf": true, ".sh": true, ".svg": true,
}

// isSafeInlineMedia returns true for standard raster images, audio, and video
// formats that do not contain active executable scripts and are safe to render
// inline in browser native viewers without sandbox restrictions.
func isSafeInlineMedia(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".jfif", ".pjpeg", ".pjp",
		".png", ".gif", ".webp", ".avif", ".bmp", ".ico", ".heic",
		".mp4", ".webm", ".mov", ".m4v", ".ogv", ".mkv",
		".mp3", ".flac", ".wav", ".ogg", ".m4a", ".opus", ".weba", ".aac",
		".pdf":
		return true
	}
	return false
}

// handleFileServer serves uploaded files from cfg.Dir. When STRIP_EXTENSION is
// active it probes for extension variants so a URL like /abc resolves to abc.png
// on disk. Cache-Control and Content-Disposition reflect the file category.
// Compressible text types are gzip-encoded when the client advertises support.
func (s *server) handleFileServer(w http.ResponseWriter, r *http.Request) {
	// path.Clean removes traversal components; stripping the leading slash gives
	// the bare filename to look up in the upload directory.
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		fileNotFound(w, r)
		return
	}

	var ownerID string
	var disk, ext string
	var ok bool
	if s.fileIndex != nil {
		ownerID = s.fileIndex.Owner(name)
	}
	if ownerID != "" {
		disk, ext, ok = s.resolveUploadInDir(filepath.Join(s.cfg.Dir, ownerID), name)
	}
	if !ok {
		// Fallback: try flat directory for backward compatibility / migration.
		disk, ext, ok = s.resolveUploadInDir(s.cfg.Dir, name)
	}
	if !ok {
		fileNotFound(w, r)
		return
	}

	// Uploads are untrusted, potentially active content. Sandbox dangerous
	// formats to prevent script execution, but allow standard media files
	// (images/video/audio/pdf) to render properly in browser native viewers.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if isSafeInlineMedia(ext) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data: blob:; media-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; frame-src 'self' blob:; object-src 'self' blob:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	} else {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'")
	}

	if rule, found := fileRules[ext]; found {
		w.Header().Set("Cache-Control", rule.control)
		if rule.disposition != "" {
			w.Header().Set("Content-Disposition", rule.disposition)
		}
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}

	f, err := os.Open(disk) // #nosec G304 -- disk is validated for absolute directory containment by resolveUpload
	if err != nil {
		// File disappeared between the probe and open (race); treat as 404.
		fileNotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Gzip for compressible types, but only on full requests — gzip and Range
	// are mutually exclusive, so skip when the client sends a Range header.
	if gzipExts[ext] && r.Header.Get("Range") == "" &&
		strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		gw := &gzipResponseWriter{ResponseWriter: w, gz: gz, status: http.StatusOK}
		http.ServeContent(gw, r, info.Name(), info.ModTime(), f)
		// Skip gz.Close for 304 — no body is allowed on a not-modified response.
		if gw.status != http.StatusNotModified {
			_ = gz.Close()
		}
		return
	}

	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// resolveUpload maps a URL name to an absolute path on disk. When
// STRIP_EXTENSION is active and no exact match exists, it globs for
// name.* — safe because upload names are random hex, so at most one file
// matches any given base name.
func (s *server) resolveUploadInDir(dir, name string) (disk, ext string, ok bool) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false
	}
	base := filepath.Join(absDir, filepath.FromSlash(name))
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", "", false
	}
	rel, err := filepath.Rel(absDir, absBase)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", "", false
	}

	if _, err := os.Stat(absBase); err == nil {
		return absBase, strings.ToLower(filepath.Ext(absBase)), true
	}
	if !s.cfg.StripExtension {
		return "", "", false
	}
	matches, _ := filepath.Glob(absBase + ".*")
	if len(matches) == 0 {
		return "", "", false
	}
	matchAbs, err := filepath.Abs(matches[0])
	if err != nil {
		return "", "", false
	}
	matchRel, err := filepath.Rel(absDir, matchAbs)
	if err != nil || strings.HasPrefix(matchRel, "..") {
		return "", "", false
	}
	return matchAbs, strings.ToLower(filepath.Ext(matchAbs)), true
}

// gzipResponseWriter routes writes through a gzip encoder, strips
// Content-Length (compressed size is unknown), and tracks the status code so
// the caller can skip gz.Close on 304 Not Modified responses.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz     *gzip.Writer
	status int
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) { return g.gz.Write(b) }
func (g *gzipResponseWriter) WriteHeader(code int) {
	g.status = code
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// fileNotFound writes a plain-text 404 and resets Cache-Control to no-store so
// a not-found response is never cached regardless of headers set earlier.
func fileNotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, "404 not found\n")
}

// handleDeleteFile removes a single uploaded file. The caller must own the file
// or hold an admin/root role.
func (s *server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		httpError(w, http.StatusBadRequest, "missing filename")
		return
	}

	// Determine the owner and full filename of the file.
	var ownerID, fullName string
	if s.fileIndex != nil {
		ownerID, fullName = s.fileIndex.Lookup(name)
	}
	if ownerID == "" {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}
	if fullName == "" {
		fullName = name
	}

	// Only the owner or an admin may delete.
	if rec.ID != ownerID && !internal.IsAdmin(rec.Role) {
		httpError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Remove from disk.
	disk := filepath.Join(s.cfg.Dir, ownerID, fullName)
	// #nosec G703,G706 -- disk path is constructed from ownerID and validated fullName, log input sanitized
	if err := os.Remove(disk); err != nil && !os.IsNotExist(err) {
		slog.Error("delete file error", "name", internal.SanitizeLog(fullName), "error", err)
		httpError(w, http.StatusInternalServerError, "could not delete file")
		return
	}

	// Remove from store and index.
	_ = s.store.RemoveUploadEntry(ownerID, fullName)
	if s.fileIndex != nil {
		s.fileIndex.Remove(fullName)
	}

	slog.Info("deleted file",
		"name", internal.SanitizeLog(fullName),
		"owner_id", internal.SanitizeLog(ownerID),
		"actor_id", internal.SanitizeLog(rec.ID),
		"actor_label", internal.SanitizeLog(rec.Label),
		"ip", internal.SanitizeLog(clientIP(r, s.cfg.TrustProxyHeaders)),
	)
	w.WriteHeader(http.StatusNoContent)
}
