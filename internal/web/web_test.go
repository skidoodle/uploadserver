package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	rec := adminReq(t, h, "POST", "/_/api/tokens", admin, `{"label":"laptop","role":"upload"}`)
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
	if denied := adminReq(t, h, "GET", "/_/api/tokens", created.Secret, ""); denied.Code != http.StatusUnauthorized {
		t.Fatalf("upload token reached admin API: status %d", denied.Code)
	}

	// Revoke it; uploads must then fail.
	if del := adminReq(t, h, "DELETE", "/_/api/tokens/"+created.ID, admin, ""); del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	if up := upload(t, h, created.Secret, "file", "b.txt", "hi"); up.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still uploads: status %d", up.Code)
	}
}

func TestAdminRequiresAdminRole(t *testing.T) {
	_, h, _ := newTestServer(t)
	if rec := adminReq(t, h, "GET", "/_/api/tokens", "", ""); rec.Code != http.StatusUnauthorized {
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
	// Another admin tries to delete root; blocked (409).
	rec := adminReq(t, h, "DELETE", "/_/api/tokens/"+rootID, adminSecret, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("admin deleting root: status %d, want 409", rec.Code)
	}
	// Admins also can't mint a root via the API (403).
	cr := adminReq(t, h, "POST", "/_/api/tokens", adminSecret, `{"label":"x","role":"root"}`)
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
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"valid123","role":"upload"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for valid label, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: empty label (0 characters)
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty label, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: too long (10 characters)
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"invalid1234","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for too long label, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: contains spaces
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"ab cd","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label with space, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Valid label: contains hyphens and underscores in the middle
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"ab-c_d","role":"upload"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for label with middle hyphen/underscore, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: starts with hyphen
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"-abcd","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label starting with hyphen, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: ends with underscore
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"abcd_","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label ending with underscore, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Invalid label: contains invalid special characters (like @)
	{
		rec := adminReq(t, h, "POST", "/_/api/tokens", secret, `{"label":"ab@cd","role":"upload"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for label with special character @, got %d: %s", rec.Code, rec.Body.String())
		}
	}
}

// createUploadToken mints an upload token via the admin API and returns its id
// and one-time secret.
func createUploadToken(t *testing.T, h http.Handler, admin, label string) (id, secret string) {
	t.Helper()
	rec := adminReq(t, h, "POST", "/_/api/tokens", admin, `{"label":"`+label+`","role":"upload"}`)
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

	if rec := adminReq(t, h, "POST", "/_/api/tokens/"+id+"/limits", admin, `{"max_uploads":2}`); rec.Code != http.StatusOK {
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
	if rec := adminReq(t, h, "POST", "/_/api/tokens/"+id+"/limits", admin, `{"max_bytes":1}`); rec.Code != http.StatusOK {
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
	if rec := adminReq(t, h, "POST", "/_/api/global/limits", admin, `{"max_uploads":1}`); rec.Code != http.StatusOK {
		t.Fatalf("set global: status %d, body %q", rec.Code, rec.Body.String())
	}
	if up := upload(t, h, secret, "file", "a.txt", "hi"); up.Code != http.StatusOK {
		t.Fatalf("first upload under global cap: %d", up.Code)
	}
	if up := upload(t, h, secret, "file", "b.txt", "hi"); up.Code != http.StatusTooManyRequests {
		t.Fatalf("second upload past global cap: status %d, want 429", up.Code)
	}

	// Granting the token a bypass lifts the global cap for it alone.
	if rec := adminReq(t, h, "POST", "/_/api/tokens/"+id+"/limits", admin, `{"bypass":true}`); rec.Code != http.StatusOK {
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

func TestUserUploadsRoute(t *testing.T) {
	_, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "mytoken")

	// Perform an upload
	up := upload(t, h, secret, "file", "test.png", "imgdata")
	if up.Code != http.StatusOK {
		t.Fatalf("upload failed: %d", up.Code)
	}

	// Unauthenticated request to /_/uploads/{id} -> redirect
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/_/uploads/"+id, nil))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated GET /_/uploads/%s status = %d, want 333/302/303", id, rec.Code)
	}

	// Authenticated request to /_/uploads/{id} -> 200 OK with template
	req := httptest.NewRequest("GET", "/_/uploads/"+id, nil)
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: admin})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated GET /_/uploads/%s status = %d, want 200", id, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), ".png") {
		t.Errorf("expected body to contain .png for href/type, got: %s", rec.Body.String())
	}
	// Verify that the displayed link text strips the extension
	upURL := strings.TrimSpace(up.Body.String())
	entryName := upURL[strings.LastIndex(upURL, "/")+1:]
	rawExt := filepath.Ext(entryName)
	baseName := strings.TrimSuffix(entryName, rawExt)
	expectedLink := ">" + baseName + "</a>"
	if !strings.Contains(rec.Body.String(), expectedLink) {
		t.Errorf("expected body to contain stripped link text %q, got: %s", expectedLink, rec.Body.String())
	}

	// API request to /_/api/tokens/{id}/uploads -> 200 OK JSON
	apiReq := httptest.NewRequest("GET", "/_/api/tokens/"+id+"/uploads", nil)
	apiReq.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, apiReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /_/api/tokens/%s/uploads status = %d, want 200", id, rec.Code)
	}
	var jsonResp struct {
		TokenID string `json:"token_id"`
		Uploads []struct {
			Name string `json:"name"`
		} `json:"uploads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if len(jsonResp.Uploads) != 1 || !strings.HasSuffix(jsonResp.Uploads[0].Name, ".png") {
		t.Errorf("unexpected uploads in JSON: %+v", jsonResp)
	}
}

func TestAdminUsersRoute(t *testing.T) {
	_, h, admin := newTestServer(t)
	_, _ = createUploadToken(t, h, admin, "mytoken")

	// 1. Unauthenticated request to /_/users -> redirect
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/_/users", nil))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated GET /_/users status = %d, want 303/302", rec.Code)
	}

	// 2. Authenticated non-admin request to /_/users -> 403 Forbidden
	_, nonAdminSecret := createUploadToken(t, h, admin, "usertok")
	req := httptest.NewRequest("GET", "/_/users", nil)
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: nonAdminSecret})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin GET /_/users status = %d, want 403", rec.Code)
	}

	// 3. Authenticated admin request to /_/users -> 200 OK with template
	req = httptest.NewRequest("GET", "/_/users", nil)
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: admin})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET /_/users status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "mytoken") {
		t.Errorf("expected users page to list 'mytoken', got: %s", body)
	}

	// Test search query
	searchReq := httptest.NewRequest("GET", "/_/users?q=mytoken", nil)
	searchReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: admin})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, searchReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("search GET /_/users status = %d, want 200", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, "mytoken") {
		t.Errorf("expected search result to contain 'mytoken', got: %s", body)
	}
}

func TestUploadTokenLoginAndProfile(t *testing.T) {
	_, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "userone")

	// 1. Upload token should be able to authenticate and view its own dashboard (user profile)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: secret})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload token GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "user profile") || !strings.Contains(body, "userone") {
		t.Errorf("user dashboard should contain user profile and userone, got: %s", body)
	}
	if strings.Contains(body, "create token") || strings.Contains(body, "global quota") {
		t.Errorf("upload token dashboard must not contain admin controls")
	}

	// 2. Upload token user should be able to view their own /_/uploads/{id}
	userReq := httptest.NewRequest("GET", "/_/uploads/"+id, nil)
	userReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: secret})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, userReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload token GET /_/uploads/%s status = %d, want 200", id, rec.Code)
	}

	// 3. Upload token user should NOT be able to view another token's /_/uploads/{other_id}
	otherReq := httptest.NewRequest("GET", "/_/uploads/otherid", nil)
	otherReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: secret})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, otherReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload token GET /_/uploads/otherid status = %d, want 408/403", rec.Code)
	}
}

func TestInviteSystem(t *testing.T) {
	s, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "userone")

	// 1. Initially userone has 0 invites, so /_/tokens/invite should fail
	form := url.Values{"label": []string{"friend1"}, "_csrf": []string{"test"}}
	req := httptest.NewRequest("POST", "/_/tokens/invite", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: secret})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("invite with 0 invites status = %d, want 303 redirect with error", rec.Code)
	}

	// 2. Admin grants 2 invites to userone
	if err := s.store.SetInvites(id, 2); err != nil {
		t.Fatalf("SetInvites failed: %v", err)
	}

	// 3. userone uses 1 invite to create friend1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("invite with 2 invites status = %d, want 303 redirect with secret", rec.Code)
	}

	// 4. Check userone now has 1 invite left
	recUser, ok := s.store.Authenticate(secret)
	if !ok || recUser.Invites != 1 {
		t.Fatalf("expected 1 invite left for userone, got %d", recUser.Invites)
	}

	// 5. Admin can also create tokens via /_/tokens/invite without decrements
	adminReq := httptest.NewRequest("POST", "/_/tokens/invite", strings.NewReader(form.Encode()))
	adminReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	adminReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: admin})
	adminReq.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, adminReq)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("admin invite status = %d, want 303 redirect", rec.Code)
	}
}

func TestTokenRename(t *testing.T) {
	s, h, admin := newTestServer(t)
	id, secret := createUploadToken(t, h, admin, "oldname")

	// 1. User can rename their own token via POST /_/tokens/{id}/label
	form := url.Values{"label": []string{"newname"}, "_csrf": []string{"test"}}
	req := httptest.NewRequest("POST", "/_/tokens/"+id+"/label", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: secret})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("user rename status = %d, want 303", rec.Code)
	}

	recUser, ok := s.store.Authenticate(secret)
	if !ok || recUser.Label != "newname" {
		t.Fatalf("expected label 'newname', got %q", recUser.Label)
	}

	// 2. User cannot rename another user's token
	otherReq := httptest.NewRequest("POST", "/_/tokens/otherid/label", strings.NewReader(form.Encode()))
	otherReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	otherReq.AddCookie(&http.Cookie{Name: adminCookieName, Value: secret})
	otherReq.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, otherReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user rename other token status = %d, want 403", rec.Code)
	}

	// 3. Admin can rename any token via JSON API
	jsonBody := strings.NewReader(`{"label":"adm-ren"}`)
	apiReq := httptest.NewRequest("POST", "/_/api/tokens/"+id+"/label", jsonBody)
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("Authorization", "Bearer "+admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, apiReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin API rename status = %d, want 200", rec.Code)
	}

	recUser, ok = s.store.Authenticate(secret)
	if !ok || recUser.Label != "adm-ren" {
		t.Fatalf("expected label 'adm-ren', got %q", recUser.Label)
	}
}

func TestGatedAssets(t *testing.T) {
	_, h, adminSecret := newTestServer(t)

	// Unauthenticated request to /_/uploads.css => 404
	req := httptest.NewRequest("GET", "/_/uploads.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unauth /_/uploads.css status = %d, want 404", rec.Code)
	}

	// Authenticated request to /_/uploads.css => 200 OK
	reqAuth := httptest.NewRequest("GET", "/_/uploads.css", nil)
	reqAuth.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	recAuth := httptest.NewRecorder()
	h.ServeHTTP(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("auth /_/uploads.css status = %d, want 200", recAuth.Code)
	}

	// Unauthenticated request to /_/uploads.js => 404
	reqJS := httptest.NewRequest("GET", "/_/uploads.js", nil)
	recJS := httptest.NewRecorder()
	h.ServeHTTP(recJS, reqJS)
	if recJS.Code != http.StatusNotFound {
		t.Fatalf("unauth /_/uploads.js status = %d, want 404", recJS.Code)
	}

	// Authenticated request to /_/uploads.js => 200 OK
	reqJSAuth := httptest.NewRequest("GET", "/_/uploads.js", nil)
	reqJSAuth.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	recJSAuth := httptest.NewRecorder()
	h.ServeHTTP(recJSAuth, reqJSAuth)
	if recJSAuth.Code != http.StatusOK {
		t.Fatalf("auth /_/uploads.js status = %d, want 200", recJSAuth.Code)
	}
}

func TestGiveawayInvites(t *testing.T) {
	s, h, adminSecret := newTestServer(t)

	// Create 2 upload tokens
	id1, secret1 := createUploadToken(t, h, adminSecret, "user1")
	id2, _ := createUploadToken(t, h, adminSecret, "user2")

	// Initially both have 0 invites
	r1, _ := s.store.Authenticate(secret1)
	if r1.Invites != 0 {
		t.Fatalf("expected 0 invites initially, got %d", r1.Invites)
	}

	// 1. SSR Giveaway: grant +3 invites to all uploaders
	form := url.Values{}
	form.Set("count", "3")
	form.Set("_csrf", "test")
	req := httptest.NewRequest("POST", "/_/tokens/giveaway", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("giveaway SSR status = %d, want 303", rec.Code)
	}

	r1, _ = s.store.Authenticate(secret1)
	if r1.Invites != 3 {
		t.Fatalf("expected 3 invites after giveaway, got %d", r1.Invites)
	}

	// 2. JSON API Giveaway: grant +2 more invites
	jsonBody := strings.NewReader(`{"count":2}`)
	apiReq := httptest.NewRequest("POST", "/_/api/tokens/giveaway", jsonBody)
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("Authorization", "Bearer "+adminSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, apiReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("giveaway API status = %d, want 200", rec.Code)
	}

	r1, _ = s.store.Authenticate(secret1)
	if r1.Invites != 5 {
		t.Fatalf("expected 5 total invites after API giveaway, got %d", r1.Invites)
	}

	_ = id1
	_ = id2
}

func TestTokenPromotionDemotion(t *testing.T) {
	dir := t.TempDir()
	store, err := internal.OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rootSecret, created, err := store.Bootstrap()
	if err != nil || !created || rootSecret == "" {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// Add an admin token and an upload token.
	_, adminSecret, err := store.Add("anadmin", internal.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	uploadID, uploadSecret, err := store.Add("uploader", internal.RoleUpload)
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
	h := srv.routes()

	// 1. Non-root admin tries to promote uploader to admin -> fails (redirect to dashboard with error or forbidden)
	form := url.Values{"role": []string{"admin"}, "_csrf": []string{"test"}}
	req := httptest.NewRequest("POST", "/_/tokens/"+uploadID+"/role", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect/error for unauthorized role change, got status %d", rec.Code)
	}

	recUser, _ := store.Authenticate(uploadSecret)
	if recUser.Role != internal.RoleUpload {
		t.Fatalf("expected role to remain upload, got %s", recUser.Role)
	}

	// 2. Root tries to promote uploader to admin -> succeeds
	reqRoot := httptest.NewRequest("POST", "/_/tokens/"+uploadID+"/role", strings.NewReader(form.Encode()))
	reqRoot.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqRoot.AddCookie(&http.Cookie{Name: adminCookieName, Value: rootSecret})
	reqRoot.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test"})
	recRoot := httptest.NewRecorder()
	h.ServeHTTP(recRoot, reqRoot)
	if recRoot.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect for successful role change, got status %d", recRoot.Code)
	}

	recUser, _ = store.Authenticate(uploadSecret)
	if recUser.Role != internal.RoleAdmin {
		t.Fatalf("expected role to become admin, got %s", recUser.Role)
	}

	// 3. API endpoint: Non-root tries to demote back to upload -> fails (401 root required)
	jsonBody := strings.NewReader(`{"role":"upload"}`)
	apiReq := httptest.NewRequest("POST", "/_/api/tokens/"+uploadID+"/role", jsonBody)
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("Authorization", "Bearer "+adminSecret)
	recAPI := httptest.NewRecorder()
	h.ServeHTTP(recAPI, apiReq)
	if recAPI.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 Unauthorized, got %d", recAPI.Code)
	}

	// 4. API endpoint: Root demotes back to upload -> succeeds
	jsonBodyRoot := strings.NewReader(`{"role":"upload"}`)
	apiReqRoot := httptest.NewRequest("POST", "/_/api/tokens/"+uploadID+"/role", jsonBodyRoot)
	apiReqRoot.Header.Set("Content-Type", "application/json")
	apiReqRoot.Header.Set("Authorization", "Bearer "+rootSecret)
	recAPIRoot := httptest.NewRecorder()
	h.ServeHTTP(recAPIRoot, apiReqRoot)
	if recAPIRoot.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d: %s", recAPIRoot.Code, recAPIRoot.Body.String())
	}

	recUser, _ = store.Authenticate(uploadSecret)
	if recUser.Role != internal.RoleUpload {
		t.Fatalf("expected role to become upload, got %s", recUser.Role)
	}
}

func TestInvitePolicyAndGiveaways(t *testing.T) {
	srv, h, rootSecret := newTestServer(t)

	// Create 3 upload tokens
	id1, secret1 := createUploadToken(t, h, rootSecret, "user1")
	_, secret2 := createUploadToken(t, h, rootSecret, "user2")
	_, _ = createUploadToken(t, h, rootSecret, "user3")

	// 1. Get initial invite policy
	req := httptest.NewRequest("GET", "/_/api/invite-policy", nil)
	req.Header.Set("Authorization", "Bearer "+rootSecret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /_/api/invite-policy status = %d, want 200", rec.Code)
	}

	// 2. Set invite policy via API
	polJSON := `{
		"sched_on": true,
		"sched_interval": 3600,
		"sched_count": 2,
		"sched_mode": "random",
		"sched_pool": 2,
		"sched_max": 5,
		"newuser_on": true,
		"newuser_count": 3,
		"newuser_delay": 0,
		"newuser_max": 10
	}`
	req = httptest.NewRequest("POST", "/_/api/invite-policy", strings.NewReader(polJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rootSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /_/api/invite-policy status = %d, want 200", rec.Code)
	}

	pol := srv.store.InvitePolicy()
	if !pol.SchedEnabled || pol.SchedInterval != 3600 || pol.SchedCount != 2 || pol.SchedMode != "random" {
		t.Errorf("InvitePolicy mismatch: %+v", pol)
	}

	// 3. Test Random Giveaway API with max cap
	giveawayJSON := `{"count": 3, "mode": "random", "pool": 2, "max_cap": 4}`
	req = httptest.NewRequest("POST", "/_/api/tokens/giveaway", strings.NewReader(giveawayJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rootSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /_/api/tokens/giveaway status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Verify at most 2 uploaders got invites clamped at max cap 4
	rec1, _ := srv.store.Authenticate(secret1)
	rec2, _ := srv.store.Authenticate(secret2)
	totalInvites := rec1.Invites + rec2.Invites
	if totalInvites > 8 {
		t.Errorf("expected max invites to be capped at 4 per user, got user1=%d, user2=%d", rec1.Invites, rec2.Invites)
	}

	// 4. Test AddWithInvite auto-scheduling pending grant when newuser_on is true
	_ = srv.store.SetInvites(id1, 1)
	newID, _, err := srv.store.AddWithInvite(id1, "invited1")
	if err != nil {
		t.Fatalf("AddWithInvite failed: %v", err)
	}

	// Process due pending grants
	applied, err := srv.store.ProcessPendingGrants()
	if err != nil {
		t.Fatalf("ProcessPendingGrants error: %v", err)
	}
	if applied != 1 {
		t.Errorf("expected 1 applied pending grant, got %d", applied)
	}

	// Verify the new user received 3 invites
	var found *internal.TokenRecord
	for _, r := range srv.store.List() {
		if r.ID == newID {
			found = &r
			break
		}
	}
	if found == nil {
		t.Fatalf("failed to fetch newly invited token record")
	}
	if found.Invites != 3 {
		t.Errorf("expected new user to receive 3 invites from auto-grant, got %d", found.Invites)
	}
}
