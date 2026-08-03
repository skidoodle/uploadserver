package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
	"uploadserver/internal"
)

// secureHeaders applies conservative response headers to every request.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
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
func logging(next http.Handler) http.Handler {
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
				"ip", clientIP(r),
			)
		}
	})
}
