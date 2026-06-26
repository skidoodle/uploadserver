package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// fileRule pairs a Cache-Control value with an optional Content-Disposition
// for a category of file types.
type fileRule struct {
	control     string
	disposition string // non-empty → Content-Disposition: attachment
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
	".svg":   {control: "public, max-age=31536000, immutable"},
	".mp4":   {control: "public, max-age=31536000, immutable"},
	".webm":  {control: "public, max-age=31536000, immutable"},
	".mov":   {control: "public, max-age=31536000, immutable"},
	".mp3":   {control: "public, max-age=31536000, immutable"},
	".flac":  {control: "public, max-age=31536000, immutable"},
	".txt":   {control: "public, max-age=31536000, immutable"},
	".html":  {control: "public, max-age=31536000, immutable"},
	".mhtml": {control: "public, max-age=31536000, immutable"},
	".css":   {control: "public, max-age=31536000, immutable"},
	".json":  {control: "public, max-age=31536000, immutable"},
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

	disk, ext, ok := s.resolveUpload(name)
	if !ok {
		fileNotFound(w, r)
		return
	}

	if rule, found := fileRules[ext]; found {
		w.Header().Set("Cache-Control", rule.control)
		if rule.disposition != "" {
			w.Header().Set("Content-Disposition", rule.disposition)
		}
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}

	f, err := os.Open(disk)
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
func (s *server) resolveUpload(name string) (disk, ext string, ok bool) {
	base := filepath.Join(s.cfg.Dir, filepath.FromSlash(name))
	if _, err := os.Stat(base); err == nil {
		return base, strings.ToLower(filepath.Ext(base)), true
	}
	if !s.cfg.StripExtension {
		return "", "", false
	}
	matches, _ := filepath.Glob(base + ".*")
	if len(matches) == 0 {
		return "", "", false
	}
	return matches[0], strings.ToLower(filepath.Ext(matches[0])), true
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
