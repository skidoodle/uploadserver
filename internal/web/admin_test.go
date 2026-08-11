package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"uploadserver/internal"
)

func TestParseCount(t *testing.T) {
	cases := map[string]int64{
		"100":       100,
		"1,000":     1000,
		"0":         0,
		"off":       0,
		"none":      0,
		"unlimited": 0,
	}
	for in, want := range cases {
		got, err := parseCount(in)
		if err != nil || got != want {
			t.Errorf("parseCount(%q) = %d, err=%v; want %d", in, got, err, want)
		}
	}

	for _, bad := range []string{"invalid", "-10"} {
		if _, err := parseCount(bad); err == nil {
			t.Errorf("parseCount(%q) expected error", bad)
		}
	}
}

func TestIsSafeRedirectTarget(t *testing.T) {
	reqHost := "example.com"

	validTargets := []string{
		"/",
		"/_/users",
		"/_/user/12345",
		"http://example.com/dashboard",
		"https://example.com/login",
	}

	for _, target := range validTargets {
		if !isSafeRedirectTarget(target, reqHost) {
			t.Errorf("isSafeRedirectTarget(%q, %q) = false; want true", target, reqHost)
		}
	}

	invalidTargets := []string{
		"",
		"//evil.com",
		"/\\evil.com",
		"http://evil.com/phish",
		"https://attacker.com",
		"/path\nwith\nnewlines",
		"/path\rwith\rCR",
	}

	for _, target := range invalidTargets {
		if isSafeRedirectTarget(target, reqHost) {
			t.Errorf("isSafeRedirectTarget(%q, %q) = true; want false", target, reqHost)
		}
	}
}

func TestCSRFGenerationAndValidation(t *testing.T) {
	csrf1 := generateCSRF("")
	csrf2 := generateCSRF("")

	if len(csrf1) != 32 {
		t.Errorf("generateCSRF length = %d; want 32 hex chars", len(csrf1))
	}
	if csrf1 == csrf2 {
		t.Errorf("generateCSRF generated duplicate tokens")
	}

	// Test validateCSRF matching
	r := httptest.NewRequest("POST", "/_/login", nil)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf1})
	r.Form = url.Values{"_csrf": []string{csrf1}}

	if !validateCSRF(r) {
		t.Error("validateCSRF failed on matching cookie and form token")
	}

	// Test mismatch
	rMismatch := httptest.NewRequest("POST", "/_/login", nil)
	rMismatch.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf1})
	rMismatch.Form = url.Values{"_csrf": []string{csrf2}}

	if validateCSRF(rMismatch) {
		t.Error("validateCSRF succeeded on mismatched tokens")
	}

	bound := generateCSRF("session-a")
	boundReq := httptest.NewRequest("POST", "/_/tokens/create", nil)
	boundReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: "session-a"})
	boundReq.AddCookie(&http.Cookie{Name: csrfCookieName, Value: bound})
	boundReq.Form = url.Values{"_csrf": []string{bound}}
	if !validateCSRF(boundReq) {
		t.Error("validateCSRF rejected token bound to the current session")
	}
	boundReq = httptest.NewRequest("POST", "/_/tokens/create", nil)
	boundReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: "session-b"})
	boundReq.AddCookie(&http.Cookie{Name: csrfCookieName, Value: bound})
	boundReq.Form = url.Values{"_csrf": []string{bound}}
	if validateCSRF(boundReq) {
		t.Error("validateCSRF accepted a token bound to another session")
	}
}

func TestUploadsTemplateMediaTypes(t *testing.T) {
	testFiles := []struct {
		filename  string
		isMedia   bool
		mediaType string
	}{
		// Archives & Binaries (not previewable)
		{"archive.7z", false, "other"},
		{"archive.gz", false, "other"},
		{"archive.rar", false, "other"},
		{"archive.zip", false, "other"},
		{"program.exe", false, "other"},
		{"library.jar", false, "other"},
		{"binary.so", false, "other"},
		{"image.pdn", false, "other"},

		// Images
		{"picture.avif", true, "image"},
		{"favicon.ico", true, "image"},
		{"graphic.png", true, "image"},
		{"vector.svg", true, "image"},

		// Videos
		{"clip.mov", true, "video"},
		{"movie.mp4", true, "video"},
		{"video.webm", true, "video"},

		// Audio
		{"song.flac", true, "audio"},
		{"music.mp3", true, "audio"},

		// PDF
		{"document.pdf", true, "pdf"},

		// Fonts
		{"font.ttf", true, "font"},
		{"webfont.woff", true, "font"},

		// Text & Code
		{"settings.conf", true, "text"},
		{"style.css", true, "text"},
		{"data.csv", true, "text"},
		{"page.html", true, "text"},
		{"payload.json", true, "text"},
		{"webpage.mhtml", true, "text"},
		{"script.sh", true, "text"},
		{"readme.txt", true, "text"},
		{"config.yaml", true, "text"},
		{"config.yml", true, "text"},
	}

	var entries []internal.UploadEntry
	for _, tf := range testFiles {
		entries = append(entries, internal.UploadEntry{
			Name: tf.filename,
			Size: 1024,
		})
	}

	data := uploadsPageData{
		Token: internal.TokenRecord{
			ID:    "testtoken",
			Label: "Test Token",
			Role:  "admin",
		},
		Uploads:         entries,
		BaseURL:         "https://example.com",
		Page:            1,
		TotalPages:      1,
		TotalFiles:      len(entries),
		TotalUnfiltered: len(entries),
		PerPage:         len(entries),
		PageStart:       1,
		PageEnd:         len(entries),
		IsAdmin:         true,
	}

	rec := httptest.NewRecorder()
	renderTemplate(rec, uploadsTmpl, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("renderTemplate returned status %d; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	for _, tf := range testFiles {
		expectedIsMedia := "false"
		if tf.isMedia {
			expectedIsMedia = "true"
		}
		expectedSnippet := "data-raw-name=\"" + tf.filename + "\" data-is-media=\"" + expectedIsMedia + "\" data-media-type=\"" + tf.mediaType + "\""
		if !strings.Contains(body, expectedSnippet) {
			t.Errorf("rendered template missing expected snippet %q for file %s", expectedSnippet, tf.filename)
		}
	}
}
