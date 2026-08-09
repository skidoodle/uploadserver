package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware(t *testing.T) {
	// Test secureHeaders
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := secureHeaders(nextHandler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q; want nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("Referrer-Policy") != "same-origin" {
		t.Errorf("Referrer-Policy = %q; want same-origin", rec.Header().Get("Referrer-Policy"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q; want no-store", rec.Header().Get("Cache-Control"))
	}

	// Test logging middleware
	logHandler := logging(nextHandler)
	recLog := httptest.NewRecorder()
	reqLog := httptest.NewRequest("GET", "/test-path", nil)
	logHandler.ServeHTTP(recLog, reqLog)

	if recLog.Code != http.StatusOK {
		t.Errorf("logging middleware code = %d; want 200", recLog.Code)
	}
}

func TestShouldSilenceLog(t *testing.T) {
	tests := []struct {
		method  string
		path    string
		silence bool
	}{
		{"GET", "/healthz", true},
		{"GET", "/favicon.ico", true},
		{"GET", "/", true},
		{"GET", "/_/admin.css", true},
		{"GET", "/_/admin.js", true},
		{"GET", "/_/uploads.css", true},
		{"GET", "/_/uploads.js", true},
		{"GET", "/_/upload.js", true},
		{"GET", "/_/login.css", true},
		{"GET", "/_/login.js", true},
		{"GET", "/storedfile.png", true},
		{"GET", "/image.jpg", true},
		{"POST", "/", false},
		{"POST", "/_/login", false},
		{"POST", "/_/logout", false},
		{"POST", "/_/tokens/create", false},
		{"DELETE", "/storedfile.png", false},
		{"GET", "/_/users", false},
		{"GET", "/_/api/tokens", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		got := shouldSilenceLog(req)
		if got != tt.silence {
			t.Errorf("shouldSilenceLog(%s %s) = %v; want %v", tt.method, tt.path, got, tt.silence)
		}
	}
}
