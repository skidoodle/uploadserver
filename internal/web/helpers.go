package web

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg+"\n") // #nosec G705 -- Content-Type is text/plain; charset=utf-8
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// requestIsHTTPS reports whether the request reached us over TLS, either
// directly or through a proxy that forwarded the original scheme.
func requestIsHTTPS(r *http.Request, trustProxyHeaders bool) bool {
	return r.TLS != nil || (trustProxyHeaders && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

// clientIP returns a best-effort client address for logging. When the server
// sits behind a reverse proxy, the first X-Forwarded-For entry wins.
func clientIP(r *http.Request, trustProxyHeaders bool) string {
	if xff := r.Header.Get("X-Forwarded-For"); trustProxyHeaders && xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// decodeJSON strictly decodes one JSON value and rejects unknown fields,
// malformed input, oversized bodies, and trailing values.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
