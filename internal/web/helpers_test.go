package web

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebHelpers(t *testing.T) {
	// Test httpError
	rec := httptest.NewRecorder()
	httpError(rec, http.StatusBadRequest, "bad request")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("httpError code = %d; want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "bad request") {
		t.Errorf("httpError body = %q; want 'bad request'", rec.Body.String())
	}

	// Test writeJSON
	recJSON := httptest.NewRecorder()
	writeJSON(recJSON, http.StatusOK, map[string]string{"status": "ok"})
	if recJSON.Code != http.StatusOK {
		t.Errorf("writeJSON code = %d; want %d", recJSON.Code, http.StatusOK)
	}
	if recJSON.Header().Get("Content-Type") != "application/json" {
		t.Errorf("writeJSON content-type = %q; want application/json", recJSON.Header().Get("Content-Type"))
	}

	// Test requestIsHTTPS
	rHTTP := httptest.NewRequest("GET", "http://example.com", nil)
	if requestIsHTTPS(rHTTP, false) {
		t.Errorf("requestIsHTTPS for plain HTTP got true; want false")
	}

	rTLS := httptest.NewRequest("GET", "http://example.com", nil)
	rTLS.TLS = &tls.ConnectionState{}
	if !requestIsHTTPS(rTLS, false) {
		t.Errorf("requestIsHTTPS for TLS got false; want true")
	}

	rHeader := httptest.NewRequest("GET", "http://example.com", nil)
	rHeader.Header.Set("X-Forwarded-Proto", "https")
	if requestIsHTTPS(rHeader, false) {
		t.Error("requestIsHTTPS trusted a forwarded scheme by default")
	}
	if !requestIsHTTPS(rHeader, true) {
		t.Error("requestIsHTTPS ignored a trusted forwarded scheme")
	}

	// Test clientIP
	rProxy := httptest.NewRequest("GET", "http://example.com", nil)
	rProxy.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	rProxy.RemoteAddr = "192.0.2.2:1234"
	if ip := clientIP(rProxy, false); ip != "192.0.2.2" {
		t.Errorf("clientIP trusted X-Forwarded-For by default: %q", ip)
	}
	if ip := clientIP(rProxy, true); ip != "203.0.113.195" {
		t.Errorf("clientIP with trusted X-Forwarded-For = %q; want 203.0.113.195", ip)
	}

	rDirect := httptest.NewRequest("GET", "http://example.com", nil)
	rDirect.RemoteAddr = "192.0.2.1:12345"
	if ip := clientIP(rDirect, false); ip != "192.0.2.1" {
		t.Errorf("clientIP direct = %q; want 192.0.2.1", ip)
	}
}
