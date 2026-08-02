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
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy = %q; want no-referrer", rec.Header().Get("Referrer-Policy"))
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
