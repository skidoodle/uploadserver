package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uploadserver/internal"
)

// newTestServer returns a server backed by a temp store seeded with one admin
// token, and that admin token's secret.
func newTestServer(t *testing.T) (*server, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := internal.OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, secret, err := store.Add("testadmin", internal.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := internal.Config{
		Dir:          dir,
		BaseURL:      "https://cdn.example.com/u",
		Field:        "file",
		MaxBytes:     1 << 20,
		StorePath:    filepath.Join(dir, "tokens.json"),
		AdminEnabled: true,
	}
	srv := &server{cfg: cfg, store: store}
	return srv, srv.routes(), secret
}

func multipartBody(t *testing.T, field, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(fw, content); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

func upload(t *testing.T, h http.Handler, token, field, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, field, filename, content)
	req := httptest.NewRequest("POST", "/", body)
	req.Header.Set("Content-Type", ct)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUploadReturnsCDNURL(t *testing.T) {
	srv, h, secret := newTestServer(t)

	rec := upload(t, h, secret, "file", "cat.PNG", "hello world")
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%q", rec.Code, rec.Body.String())
	}
	url := rec.Body.String()
	if !strings.HasPrefix(url, "https://cdn.example.com/u/") {
		t.Fatalf("URL not built from BASE_URL: %q", url)
	}
	if !strings.HasSuffix(url, ".png") {
		t.Fatalf("extension not preserved/lowercased: %q", url)
	}
	name := url[strings.LastIndexByte(url, '/')+1:]
	if _, err := os.Stat(filepath.Join(srv.cfg.Dir, name)); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}
}

func TestUploadRequiresValidToken(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, tok := range []string{"", "wrong-token-aaaaaaaaaa"} {
		rec := upload(t, h, tok, "file", "x.txt", "data")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", tok, rec.Code)
		}
	}
}

func TestUploadTooLarge(t *testing.T) {
	srv, _, secret := newTestServer(t)
	srv.cfg.MaxBytes = 64
	h := srv.routes()

	rec := upload(t, h, secret, "file", "big.bin", strings.Repeat("A", 4096))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestExtOf(t *testing.T) {
	cases := map[string]string{
		"cat.png":          "png",
		"CAT.PNG":          "png",
		"archive.tar.gz":   "gz",
		"noext":            "",
		"weird.<script>":   "",
		"../../etc/passwd": "",
	}
	for in, want := range cases {
		if got := extOf(in); got != want {
			t.Errorf("extOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func adminReq(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminCreateTokenThenUpload(t *testing.T) {
	_, h, admin := newTestServer(t)

	// Create an upload-only token via the admin API.
	rec := adminReq(t, h, "POST", "/api/tokens", admin, `{"label":"laptop","role":"upload"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%q", rec.Code, rec.Body.String())
	}
	var created struct {
		ID, Secret, Role string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Secret == "" || created.Role != "upload" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	// The new token can upload.
	if up := upload(t, h, created.Secret, "file", "a.txt", "hi"); up.Code != http.StatusOK {
		t.Fatalf("upload with new token: %d", up.Code)
	}

	// But it cannot reach the admin API.
	if denied := adminReq(t, h, "GET", "/api/tokens", created.Secret, ""); denied.Code != http.StatusUnauthorized {
		t.Fatalf("upload token reached admin API: status %d", denied.Code)
	}

	// Revoke it; uploads must then fail.
	if del := adminReq(t, h, "DELETE", "/api/tokens/"+created.ID, admin, ""); del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	if up := upload(t, h, created.Secret, "file", "b.txt", "hi"); up.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still uploads: status %d", up.Code)
	}
}

func TestAdminRequiresAdminRole(t *testing.T) {
	_, h, _ := newTestServer(t)
	if rec := adminReq(t, h, "GET", "/api/tokens", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin list: status %d", rec.Code)
	}
}

func TestLastAdminProtected(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// The seeded admin is the only one; deleting it must be refused.
	ids := srv.store.List()
	if len(ids) != 1 {
		t.Fatalf("expected 1 token, got %d", len(ids))
	}
	if err := srv.store.Remove(ids[0].ID); err == nil {
		t.Fatal("expected removal of last admin to fail")
	}
	if err := srv.store.SetDisabled(ids[0].ID, true); err == nil {
		t.Fatal("expected disabling last admin to fail")
	}
}

func TestRootIsProtected(t *testing.T) {
	store, err := internal.OpenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secret, created, err := store.Bootstrap()
	if err != nil || !created || secret == "" {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	root := store.List()[0]
	if root.Role != internal.RoleRoot {
		t.Fatalf("bootstrap role = %q, want root", root.Role)
	}
	if err := store.Remove(root.ID); err != internal.ErrProtectedRoot {
		t.Fatalf("remove root: got %v, want ErrProtectedRoot", err)
	}
	if err := store.SetDisabled(root.ID, true); err != internal.ErrProtectedRoot {
		t.Fatalf("disable root: got %v, want ErrProtectedRoot", err)
	}
}

func TestAdminCannotDeleteRoot(t *testing.T) {
	dir := t.TempDir()
	store, err := internal.OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, _, err := store.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	_, adminSecret, err := store.Add("anadmin", internal.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := internal.Config{Dir: dir, Field: "file", MaxBytes: 1 << 20, StorePath: filepath.Join(dir, "tokens.json"), AdminEnabled: true}
	h := (&server{cfg: cfg, store: store}).routes()

	rootID := ""
	for _, r := range store.List() {
		if r.Role == internal.RoleRoot {
			rootID = r.ID
		}
	}
	// Another admin tries to delete root → blocked (409).
	rec := adminReq(t, h, "DELETE", "/api/tokens/"+rootID, adminSecret, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("admin deleting root: status %d, want 409", rec.Code)
	}
	// Admins also can't mint a root via the API (403).
	cr := adminReq(t, h, "POST", "/api/tokens", adminSecret, `{"label":"x","role":"root"}`)
	if cr.Code != http.StatusForbidden {
		t.Fatalf("admin creating root: status %d, want 403", cr.Code)
	}
}

func TestStorePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	s1, err := internal.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s1.Add("laptop", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	// bbolt is single-owner, so the file must be released before it can be
	// reopened; a fresh store then authenticates the same secret off disk.
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := internal.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if _, ok := s2.Authenticate(secret); !ok {
		t.Fatal("secret not recognized after reload")
	}
}

func TestTokenLabelValidation(t *testing.T) {
	_, h, secret := newTestServer(t)

	// Valid label (1-9 alphanumeric characters)
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"valid123","role":"upload"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for valid label, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: empty label (0 characters)
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty label, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: too long (10 characters)
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"invalid1234","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for too long label, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: contains spaces
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"ab cd","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label with space, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Valid label: contains hyphens and underscores in the middle
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"ab-c_d","role":"upload"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for label with middle hyphen/underscore, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: starts with hyphen
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"-abcd","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label starting with hyphen, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: ends with underscore
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"abcd_","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label ending with underscore, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: contains invalid special characters (like @)
	{
		rec := adminReq(t, h, "POST", "/api/tokens", secret, `{"label":"ab@cd","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label with special character @, got %d: %s", rec.Code, rec.Body.String())
		}
	}
}

// createUploadToken mints an upload token via the admin API and returns its id
// and one-time secret.
func createUploadToken(t *testing.T, h http.Handler, admin, label string) (id, secret string) {
	t.Helper()
	rec := adminReq(t, h, "POST", "/api/tokens", admin, `{"label":"`+label+`","role":"upload"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token: status %d, body %q", rec.Code, rec.Body.String())
	}
	var created struct{ ID, Secret string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.ID, created.Secret
}

func TestUploadCountQuota(t *testing.T) {
	srv, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "capped")

	if rec := adminReq(t, h, "POST", "/api/tokens/"+id+"/limits", admin, `{"max_uploads":2}`); rec.Code != http.StatusOK {
		t.Fatalf("set limits: status %d, body %q", rec.Code, rec.Body.String())
	}

	// The first two uploads are allowed; the third trips the count quota.
	for i := 1; i <= 2; i++ {
		if up := upload(t, h, secret, "file", "a.txt", "hi"); up.Code != http.StatusOK {
			t.Fatalf("upload %d: status %d", i, up.Code)
		}
	}
	if up := upload(t, h, secret, "file", "a.txt", "hi"); up.Code != http.StatusTooManyRequests {
		t.Fatalf("third upload past quota: status %d, want 429", up.Code)
	}

	// Usage was recorded for the two that went through.
	for _, r := range srv.store.List() {
		if r.ID == id && r.Usage.Uploads != 2 {
			t.Fatalf("recorded uploads = %d, want 2", r.Usage.Uploads)
		}
	}
}

func TestUploadByteQuota(t *testing.T) {
	_, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "tiny")

	// A 1-byte lifetime cap: any real upload exceeds the remaining budget.
	if rec := adminReq(t, h, "POST", "/api/tokens/"+id+"/limits", admin, `{"max_bytes":1}`); rec.Code != http.StatusOK {
		t.Fatalf("set limits: status %d", rec.Code)
	}
	if up := upload(t, h, secret, "file", "a.txt", "hello world"); up.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload past byte quota: status %d, want 413", up.Code)
	}
}

func TestGlobalQuotaAndBypass(t *testing.T) {
	_, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "globaled")

	// A global one-upload cap applies to a token with no personal limits.
	if rec := adminReq(t, h, "POST", "/api/global/limits", admin, `{"max_uploads":1}`); rec.Code != http.StatusOK {
		t.Fatalf("set global: status %d, body %q", rec.Code, rec.Body.String())
	}
	if up := upload(t, h, secret, "file", "a.txt", "hi"); up.Code != http.StatusOK {
		t.Fatalf("first upload under global cap: %d", up.Code)
	}
	if up := upload(t, h, secret, "file", "b.txt", "hi"); up.Code != http.StatusTooManyRequests {
		t.Fatalf("second upload past global cap: status %d, want 429", up.Code)
	}

	// Granting the token a bypass lifts the global cap for it alone.
	if rec := adminReq(t, h, "POST", "/api/tokens/"+id+"/limits", admin, `{"bypass":true}`); rec.Code != http.StatusOK {
		t.Fatalf("set bypass: status %d", rec.Code)
	}
	if up := upload(t, h, secret, "file", "c.txt", "hi"); up.Code != http.StatusOK {
		t.Fatalf("bypassing token should upload freely: status %d", up.Code)
	}
}

func TestStaticAssets(t *testing.T) {
	_, h, admin := newTestServer(t)

	// Login assets are public.
	for _, path := range []string{"/_/login.css", "/_/login.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s: empty body", path)
		}
	}

	// Dashboard assets are hidden from anyone without an admin session.
	for _, path := range []string{"/_/admin.css", "/_/admin.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s unauthenticated: status %d, want 404", path, rec.Code)
		}

		authed := httptest.NewRequest("GET", path, nil)
		authed.AddCookie(&http.Cookie{Name: adminCookieName, Value: admin})
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, authed)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s with admin cookie: status %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s with admin cookie: empty body", path)
		}
	}
}

func TestAdminPageReferencesScopedAssets(t *testing.T) {
	_, h, admin := newTestServer(t)

	// Logged out, the page pulls in only the login assets.
	out := httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest("GET", "/", nil))
	body := out.Body.String()
	if !strings.Contains(body, "/_/login.css") || !strings.Contains(body, "/_/login.js") {
		t.Errorf("login page is missing the login assets")
	}
	if strings.Contains(body, "/_/admin.css") || strings.Contains(body, "/_/admin.js") {
		t.Errorf("login page leaks dashboard asset references")
	}

	// Logged in, it pulls in only the dashboard assets.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: admin})
	in := httptest.NewRecorder()
	h.ServeHTTP(in, req)
	body = in.Body.String()
	if !strings.Contains(body, "/_/admin.css") || !strings.Contains(body, "/_/admin.js") {
		t.Errorf("dashboard is missing the dashboard assets")
	}
	if strings.Contains(body, "/_/login.css") || strings.Contains(body, "/_/login.js") {
		t.Errorf("dashboard leaks login asset references")
	}
}

func TestUploadJSONResponse(t *testing.T) {
	_, h, secret := newTestServer(t)

	body, ct := multipartBody(t, "file", "a.txt", "hi")
	req := httptest.NewRequest("POST", "/", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (%q)", err, rec.Body.String())
	}
	if !strings.HasPrefix(resp.URL, "https://cdn.example.com/u/") {
		t.Fatalf("unexpected url: %q", resp.URL)
	}
}

// The admin token lives in a cookie, so it must only carry the Secure flag when
// the request is actually on HTTPS, otherwise plain-HTTP/local runs would break.
func TestSessionCookieSecureFlag(t *testing.T) {
	_, h, _ := newTestServer(t)

	plain := httptest.NewRecorder()
	h.ServeHTTP(plain, httptest.NewRequest("GET", "/", nil))
	if c := findCookie(plain.Result().Cookies(), csrfCookieName); c == nil || c.Secure {
		t.Fatalf("over HTTP: csrf cookie = %+v, want present and not Secure", c)
	}

	fwd := httptest.NewRequest("GET", "/", nil)
	fwd.Header.Set("X-Forwarded-Proto", "https")
	secure := httptest.NewRecorder()
	h.ServeHTTP(secure, fwd)
	if c := findCookie(secure.Result().Cookies(), csrfCookieName); c == nil || !c.Secure {
		t.Fatalf("behind HTTPS proxy: csrf cookie = %+v, want Secure", c)
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}
