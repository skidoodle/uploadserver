package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"uploadserver/internal"
)

// secureHeaders applies conservative response headers to every request.
func (s *server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; media-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		if requestIsHTTPS(r, s.cfg.TrustProxyHeaders) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// limitFormBodies caps dashboard form submissions before any handler can call
// FormValue. The streamed upload endpoint and bearer-authenticated JSON API are
// deliberately excluded.
func limitFormBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/_/") && !strings.HasPrefix(r.URL.Path, "/_/api/") {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if err := r.ParseForm(); err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					httpError(w, http.StatusRequestEntityTooLarge, "form body too large")
				} else {
					httpError(w, http.StatusBadRequest, "invalid form body")
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestDeadline bounds ordinary responses without imposing a server-wide
// WriteTimeout on potentially long streamed uploads.
func (s *server) requestDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !(r.Method == http.MethodPost && r.URL.Path == "/") && s.cfg.RequestTimeout > 0 {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(s.cfg.RequestTimeout))
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// shouldSilenceLog reports whether access logging should be suppressed for a request.
// It returns true for healthchecks, favicon requests, and root path GET requests.
func shouldSilenceLog(r *http.Request) bool {
	p := r.URL.Path

	if p == "/healthz" {
		return true
	}

	if p == "/favicon.ico" {
		return true
	}

	if r.Method == http.MethodGet && (p == "/" || p == "") {
		return true
	}

	if strings.HasPrefix(p, "/_/") {
		if strings.HasSuffix(p, ".css") || strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".ico") {
			return true
		}
	}

	if r.Method == http.MethodGet && !strings.HasPrefix(p, "/_/") {
		return true
	}

	return false
}

// logging records the request method, URL, status, and duration of each request.
func (s *server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if !shouldSilenceLog(r) {
			slog.Info("http request",
				"method", internal.SanitizeLog(r.Method),
				"path", internal.SanitizeLog(r.URL.Path),
				"status", rec.status,
				"duration", time.Since(start).Round(time.Millisecond),
				"ip", clientIP(r, s.cfg.TrustProxyHeaders),
			)
		}
	})
}
