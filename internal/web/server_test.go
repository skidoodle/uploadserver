package web

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"uploadserver/internal"
)

// TestAllServerRoutes validates every single path registered in (s *server).routes().
func TestAllServerRoutes(t *testing.T) {
	srv, h, adminSecret := newTestServer(t)

	// Create a root token for testing root-only paths
	_, rootSecret, err := srv.store.Add("rootuser", internal.RoleRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Create target tokens for operations on {id} so we don't delete/modify the main admin token
	targetID, _, err := srv.store.Add("targettok", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	// Create tokens specifically for deletion routes
	ssrDeleteID, _, err := srv.store.Add("ssrdel", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	apiDeleteID, _, err := srv.store.Add("apidel", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	type routeTestCase struct {
		name                 string
		method               string
		path                 string
		contentType          string
		body                 string
		unauthExpectedStatus int
		authExpectedStatus   int
		useRootAuth          bool
	}

	tests := []routeTestCase{
		// 1. Upload route: POST /{$}
		{
			name:                 "POST /{$} (Upload)",
			method:               "POST",
			path:                 "/",
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusBadRequest, // missing multipart file form data
		},
		// 2. Health check route: GET /healthz
		{
			name:                 "GET /healthz",
			method:               "GET",
			path:                 "/healthz",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 3. File server route: GET /
		{
			name:                 "GET / (File Server)",
			method:               "GET",
			path:                 "/nonexistent-file.png",
			unauthExpectedStatus: http.StatusNotFound,
			authExpectedStatus:   http.StatusNotFound,
		},
		// 4. Public Static Asset: GET /_/login/css/login.bundle.css
		{
			name:                 "GET /_/login/css/login.bundle.css",
			method:               "GET",
			path:                 "/_/login/css/login.bundle.css",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 5. Public Static Asset: GET /_/login/js/login.bundle.js
		{
			name:                 "GET /_/login/js/login.bundle.js",
			method:               "GET",
			path:                 "/_/login/js/login.bundle.js",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 6. Static Asset: GET /_/admin/css/admin.bundle.css
		{
			name:                 "GET /_/admin/css/admin.bundle.css",
			method:               "GET",
			path:                 "/_/admin/css/admin.bundle.css",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 7. Static Asset: GET /_/admin/js/admin.bundle.js
		{
			name:                 "GET /_/admin/js/admin.bundle.js",
			method:               "GET",
			path:                 "/_/admin/js/admin.bundle.js",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 8. Static Asset: GET /_/uploads/css/uploads.bundle.css
		{
			name:                 "GET /_/uploads/css/uploads.bundle.css",
			method:               "GET",
			path:                 "/_/uploads/css/uploads.bundle.css",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 9. Static Asset: GET /_/uploads/js/uploads.bundle.js
		{
			name:                 "GET /_/uploads/js/uploads.bundle.js",
			method:               "GET",
			path:                 "/_/uploads/js/uploads.bundle.js",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 10. Admin Page: GET /{$}
		{
			name:                 "GET /{$} (Admin Page)",
			method:               "GET",
			path:                 "/",
			unauthExpectedStatus: http.StatusOK,
			authExpectedStatus:   http.StatusOK,
		},
		// 11. Admin Login: POST /_/login
		{
			name:                 "POST /_/login",
			method:               "POST",
			path:                 "/_/login",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 12. Admin Logout: POST /_/logout
		{
			name:                 "POST /_/logout",
			method:               "POST",
			path:                 "/_/logout",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 13. SSR Create Token: POST /_/tokens/create
		{
			name:                 "POST /_/tokens/create",
			method:               "POST",
			path:                 "/_/tokens/create",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 14. SSR Invite Token: POST /_/tokens/invite
		{
			name:                 "POST /_/tokens/invite",
			method:               "POST",
			path:                 "/_/tokens/invite",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 15. SSR Giveaway Tokens: POST /_/tokens/giveaway
		{
			name:                 "POST /_/tokens/giveaway",
			method:               "POST",
			path:                 "/_/tokens/giveaway",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 16. SSR Toggle Token: POST /_/tokens/{id}/toggle
		{
			name:                 "POST /_/tokens/{id}/toggle",
			method:               "POST",
			path:                 "/_/tokens/" + targetID + "/toggle",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 17. SSR Delete Token: POST /_/tokens/{id}/delete
		{
			name:                 "POST /_/tokens/{id}/delete",
			method:               "POST",
			path:                 "/_/tokens/" + ssrDeleteID + "/delete",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 18. SSR Set Limits: POST /_/tokens/{id}/limits
		{
			name:                 "POST /_/tokens/{id}/limits",
			method:               "POST",
			path:                 "/_/tokens/" + targetID + "/limits",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 19. SSR Set Label: POST /_/tokens/{id}/label
		{
			name:                 "POST /_/tokens/{id}/label",
			method:               "POST",
			path:                 "/_/tokens/" + targetID + "/label",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 20. SSR Set Role: POST /_/tokens/{id}/role
		{
			name:                 "POST /_/tokens/{id}/role",
			method:               "POST",
			path:                 "/_/tokens/" + targetID + "/role",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 21. SSR Set Global Limits: POST /_/global/limits
		{
			name:                 "POST /_/global/limits",
			method:               "POST",
			path:                 "/_/global/limits",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 22. SSR Set Invite Policy: POST /_/invite-policy
		{
			name:                 "POST /_/invite-policy",
			method:               "POST",
			path:                 "/_/invite-policy",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusSeeOther,
		},
		// 23. API List Tokens: GET /_/api/tokens
		{
			name:                 "GET /_/api/tokens",
			method:               "GET",
			path:                 "/_/api/tokens",
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 24. API Create Token: POST /_/api/tokens
		{
			name:                 "POST /_/api/tokens",
			method:               "POST",
			path:                 "/_/api/tokens",
			contentType:          "application/json",
			body:                 `{"label":"testtok","role":"upload"}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusCreated,
		},
		// 25. API Giveaway: POST /_/api/tokens/giveaway
		{
			name:                 "POST /_/api/tokens/giveaway",
			method:               "POST",
			path:                 "/_/api/tokens/giveaway",
			contentType:          "application/json",
			body:                 `{"count":1}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 26. API Delete Token: DELETE /_/api/tokens/{id}
		{
			name:                 "DELETE /_/api/tokens/{id}",
			method:               "DELETE",
			path:                 "/_/api/tokens/" + apiDeleteID,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 27. API Disable Token: POST /_/api/tokens/{id}/disable
		{
			name:                 "POST /_/api/tokens/{id}/disable",
			method:               "POST",
			path:                 "/_/api/tokens/" + targetID + "/disable",
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 28. API Enable Token: POST /_/api/tokens/{id}/enable
		{
			name:                 "POST /_/api/tokens/{id}/enable",
			method:               "POST",
			path:                 "/_/api/tokens/" + targetID + "/enable",
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 29. API Set Limits: POST /_/api/tokens/{id}/limits
		{
			name:                 "POST /_/api/tokens/{id}/limits",
			method:               "POST",
			path:                 "/_/api/tokens/" + targetID + "/limits",
			contentType:          "application/json",
			body:                 `{"max_uploads":10}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 30. API Set Label: POST /_/api/tokens/{id}/label
		{
			name:                 "POST /_/api/tokens/{id}/label",
			method:               "POST",
			path:                 "/_/api/tokens/" + targetID + "/label",
			contentType:          "application/json",
			body:                 `{"label":"newlabel"}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 31. API Set Role: POST /_/api/tokens/{id}/role
		{
			name:                 "POST /_/api/tokens/{id}/role",
			method:               "POST",
			path:                 "/_/api/tokens/" + targetID + "/role",
			contentType:          "application/json",
			body:                 `{"role":"admin"}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
			useRootAuth:          true,
		},
		// 32. API Set Global Limits: POST /_/api/global/limits
		{
			name:                 "POST /_/api/global/limits",
			method:               "POST",
			path:                 "/_/api/global/limits",
			contentType:          "application/json",
			body:                 `{"max_uploads":100}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 33. API Token Uploads: GET /_/api/tokens/{id}/uploads
		{
			name:                 "GET /_/api/tokens/{id}/uploads",
			method:               "GET",
			path:                 "/_/api/tokens/" + targetID + "/uploads",
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 34. API Get Invite Policy: GET /_/api/invite-policy
		{
			name:                 "GET /_/api/invite-policy",
			method:               "GET",
			path:                 "/_/api/invite-policy",
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 35. API Set Invite Policy: POST /_/api/invite-policy
		{
			name:                 "POST /_/api/invite-policy",
			method:               "POST",
			path:                 "/_/api/invite-policy",
			contentType:          "application/json",
			body:                 `{"sched_on":true}`,
			unauthExpectedStatus: http.StatusUnauthorized,
			authExpectedStatus:   http.StatusOK,
		},
		// 36. User Uploads UI Page: GET /_/uploads/{id}
		{
			name:                 "GET /_/uploads/{id}",
			method:               "GET",
			path:                 "/_/uploads/" + targetID,
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusOK,
		},
		// 37. Users List UI Page: GET /_/users
		{
			name:                 "GET /_/users",
			method:               "GET",
			path:                 "/_/users",
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusOK,
		},
		// 38. User Profile UI Page: GET /_/user/{id}
		{
			name:                 "GET /_/user/{id}",
			method:               "GET",
			path:                 "/_/user/" + targetID,
			unauthExpectedStatus: http.StatusSeeOther,
			authExpectedStatus:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. Test unauthenticated request
			var unauthBody io.Reader
			if tt.body != "" {
				unauthBody = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, unauthBody)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.unauthExpectedStatus {
				t.Errorf("[unauthenticated] %s %s got status %d, want %d (body: %q)", tt.method, tt.path, rec.Code, tt.unauthExpectedStatus, rec.Body.String())
			}

			// 2. Test authenticated request
			var authBody io.Reader
			if tt.body != "" {
				authBody = strings.NewReader(tt.body)
			}
			reqAuth := httptest.NewRequest(tt.method, tt.path, authBody)
			if tt.contentType != "" {
				reqAuth.Header.Set("Content-Type", tt.contentType)
			}

			secretToUse := adminSecret
			if tt.useRootAuth {
				secretToUse = rootSecret
			}

			reqAuth.Header.Set("Authorization", "Bearer "+secretToUse)
			reqAuth.AddCookie(&http.Cookie{
				Name:  adminCookieName,
				Value: secretToUse,
			})
			recAuth := httptest.NewRecorder()
			h.ServeHTTP(recAuth, reqAuth)

			if recAuth.Code != tt.authExpectedStatus {
				t.Errorf("[authenticated] %s %s got status %d, want %d (body: %q)", tt.method, tt.path, recAuth.Code, tt.authExpectedStatus, recAuth.Body.String())
			}
		})
	}
}

func TestServerRoutesDisabledAdmin(t *testing.T) {
	dir := t.TempDir()
	store, err := internal.OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := &server{
		cfg: internal.Config{
			Dir:          dir,
			AdminEnabled: false,
			ServeFiles:   true,
		},
		store: store,
	}
	h := srv.routes()

	adminPaths := []struct {
		method string
		path   string
	}{
		{"GET", "/_/login/css/login.css"},
		{"GET", "/_/admin/css/admin.css"},
		{"POST", "/_/login"},
		{"GET", "/_/api/tokens"},
		{"GET", "/_/users"},
		{"GET", "/_/user/123"},
	}

	for _, ap := range adminPaths {
		req := httptest.NewRequest(ap.method, ap.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("Admin disabled: %s %s status = %d, want 404 or 405", ap.method, ap.path, rec.Code)
		}
	}
}

func TestServerRoutesDisabledServeFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := internal.OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := &server{
		cfg: internal.Config{
			Dir:          dir,
			AdminEnabled: false,
			ServeFiles:   false,
		},
		store: store,
	}
	h := srv.routes()

	req := httptest.NewRequest("GET", "/testfile.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("ServeFiles disabled: GET /testfile.txt status = %d, want 404", rec.Code)
	}
}

func TestServerHealthzUnhealthy(t *testing.T) {
	dir := t.TempDir()
	store, err := internal.OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}

	srv := &server{
		cfg:   internal.Config{Dir: dir},
		store: store,
	}

	// Close store to force Ping() to error
	_ = store.Close()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.handleHealthz(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("handleHealthz status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "database unhealthy") {
		t.Fatalf("handleHealthz body = %q, want 'database unhealthy'", rec.Body.String())
	}
}

func TestServerAuthenticateAndRoleGuards(t *testing.T) {
	srv, _, adminSecret := newTestServer(t)

	_, userSecret, err := srv.store.Add("user1", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	_, rootSecret, err := srv.store.Add("root1", internal.RoleRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Test authenticate
	reqNoHeader := httptest.NewRequest("GET", "/", nil)
	if _, ok := srv.authenticate(reqNoHeader); ok {
		t.Errorf("authenticate with no header should return false")
	}

	reqBadHeader := httptest.NewRequest("GET", "/", nil)
	reqBadHeader.Header.Set("Authorization", "Bearer invalid-secret")
	if _, ok := srv.authenticate(reqBadHeader); ok {
		t.Errorf("authenticate with invalid secret should return false")
	}

	reqValidHeader := httptest.NewRequest("GET", "/", nil)
	reqValidHeader.Header.Set("Authorization", "Bearer "+adminSecret)
	if rec, ok := srv.authenticate(reqValidHeader); !ok || rec.Role != internal.RoleAdmin {
		t.Errorf("authenticate with valid admin secret failed: ok=%v, role=%s", ok, rec.Role)
	}

	// Test requireAdmin
	recNoAdmin := httptest.NewRecorder()
	reqUser := httptest.NewRequest("GET", "/", nil)
	reqUser.Header.Set("Authorization", "Bearer "+userSecret)
	if _, ok := srv.requireAdmin(recNoAdmin, reqUser); ok {
		t.Errorf("requireAdmin should return false for user role")
	}
	if recNoAdmin.Code != http.StatusUnauthorized {
		t.Errorf("requireAdmin status = %d, want 401", recNoAdmin.Code)
	}

	recAdmin := httptest.NewRecorder()
	reqAdmin := httptest.NewRequest("GET", "/", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminSecret)
	if _, ok := srv.requireAdmin(recAdmin, reqAdmin); !ok {
		t.Errorf("requireAdmin should return true for admin role")
	}

	// Test requireRoot
	recRootFail := httptest.NewRecorder()
	if _, ok := srv.requireRoot(recRootFail, reqAdmin); ok {
		t.Errorf("requireRoot should return false for admin role")
	}
	if recRootFail.Code != http.StatusUnauthorized {
		t.Errorf("requireRoot status = %d, want 401", recRootFail.Code)
	}

	recRootPass := httptest.NewRecorder()
	reqRoot := httptest.NewRequest("GET", "/", nil)
	reqRoot.Header.Set("Authorization", "Bearer "+rootSecret)
	if _, ok := srv.requireRoot(recRootPass, reqRoot); !ok {
		t.Errorf("requireRoot should return true for root role")
	}
}

func TestServerAnnounce(t *testing.T) {
	srv, _, _ := newTestServer(t)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.Writer())

	srv.announce("secret123", true)
	output := buf.String()
	if !strings.Contains(output, "default root token: secret123") {
		t.Errorf("announce(true) output missing default root token: %q", output)
	}

	buf.Reset()
	srv.announce("secret123", false)
	output = buf.String()
	if !strings.Contains(output, "loaded") {
		t.Errorf("announce(false) output missing loaded token count: %q", output)
	}
}

func TestUnregisteredUsersPath404(t *testing.T) {
	srv, h, adminSecret := newTestServer(t)

	tokID, _, err := srv.store.Add("user1", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that plural /_/users/<id> returns 404 Not Found
	req := httptest.NewRequest("GET", "/_/users/"+tokID, nil)
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /_/users/%s status = %d, want 404", tokID, rec.Code)
	}

	// Verify that singular /_/user/<id> returns 200 OK
	reqValid := httptest.NewRequest("GET", "/_/user/"+tokID, nil)
	reqValid.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
	recValid := httptest.NewRecorder()
	h.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusOK {
		t.Errorf("GET /_/user/%s status = %d, want 200", tokID, recValid.Code)
	}
}

func TestTemplateLinksMatchServerRoutes(t *testing.T) {
	srv, h, adminSecret := newTestServer(t)

	userTokID, _, err := srv.store.Add("user1", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	pagesToTest := []string{
		"/",
		"/_/users",
		"/_/user/" + userTokID,
	}

	for _, pagePath := range pagesToTest {
		req := httptest.NewRequest("GET", pagePath, nil)
		req.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Failed to fetch %s: status = %d", pagePath, rec.Code)
		}

		body := rec.Body.String()

		// Extract href and action links
		for _, tag := range []string{`href="`, `action="`} {
			idx := 0
			for {
				pos := strings.Index(body[idx:], tag)
				if pos == -1 {
					break
				}
				start := idx + pos + len(tag)
				end := strings.IndexByte(body[start:], '"')
				if end == -1 {
					break
				}
				link := body[start : start+end]
				idx = start + end

				// Check internal admin routes starting with /_/
				if strings.HasPrefix(link, "/_/") {
					if strings.HasPrefix(link, "/_/users/") {
						t.Errorf("Page %s contains invalid link %q (should use /_/user/{id})", pagePath, link)
					}
				}
			}
		}
	}
}

func TestRolePermissionsMatrix(t *testing.T) {
	srv, h, _ := newTestServer(t)

	// Create tokens for all 3 roles
	_, uploadSecret, err := srv.store.Add("uploader1", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}
	_, adminSecret, err := srv.store.Add("admin1", internal.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	rootID, rootSecret, err := srv.store.Add("root1", internal.RoleRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Create target tokens for mutations
	targetUploadID, _, err := srv.store.Add("target1", internal.RoleUpload)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("API Role Permission Matrix", func(t *testing.T) {
		apiTests := []struct {
			name       string
			method     string
			path       string
			body       string
			uploadWant int
			adminWant  int
			rootWant   int
		}{
			{
				name:       "List Tokens",
				method:     "GET",
				path:       "/_/api/tokens",
				uploadWant: http.StatusUnauthorized,
				adminWant:  http.StatusOK,
				rootWant:   http.StatusOK,
			},
			{
				name:       "Create Upload Token",
				method:     "POST",
				path:       "/_/api/tokens",
				body:       `{"label":"newtok","role":"upload"}`,
				uploadWant: http.StatusUnauthorized,
				adminWant:  http.StatusCreated,
				rootWant:   http.StatusCreated,
			},
			{
				name:       "Set Token Limits",
				method:     "POST",
				path:       "/_/api/tokens/" + targetUploadID + "/limits",
				body:       `{"max_uploads":10}`,
				uploadWant: http.StatusUnauthorized,
				adminWant:  http.StatusOK,
				rootWant:   http.StatusOK,
			},
			{
				name:       "Set Token Role (Root-Only)",
				method:     "POST",
				path:       "/_/api/tokens/" + targetUploadID + "/role",
				body:       `{"role":"admin"}`,
				uploadWant: http.StatusUnauthorized,
				adminWant:  http.StatusUnauthorized, // admin is denied
				rootWant:   http.StatusOK,           // root is allowed
			},
			{
				name:       "Get Invite Policy",
				method:     "GET",
				path:       "/_/api/invite-policy",
				uploadWant: http.StatusUnauthorized,
				adminWant:  http.StatusOK,
				rootWant:   http.StatusOK,
			},
		}

		for _, tt := range apiTests {
			t.Run(tt.name, func(t *testing.T) {
				for _, roleCase := range []struct {
					role   string
					secret string
					want   int
				}{
					{"upload", uploadSecret, tt.uploadWant},
					{"admin", adminSecret, tt.adminWant},
					{"root", rootSecret, tt.rootWant},
				} {
					var r io.Reader
					if tt.body != "" {
						r = strings.NewReader(tt.body)
					}
					req := httptest.NewRequest(tt.method, tt.path, r)
					req.Header.Set("Authorization", "Bearer "+roleCase.secret)
					if tt.body != "" {
						req.Header.Set("Content-Type", "application/json")
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)

					if rec.Code != roleCase.want {
						t.Errorf("[%s role] %s %s status = %d, want %d (body: %q)", roleCase.role, tt.method, tt.path, rec.Code, roleCase.want, rec.Body.String())
					}
				}
			})
		}
	})

	t.Run("Root Protection Matrix", func(t *testing.T) {
		// Admin attempting to delete root token -> forbidden/conflict
		reqAdminDel := httptest.NewRequest("DELETE", "/_/api/tokens/"+rootID, nil)
		reqAdminDel.Header.Set("Authorization", "Bearer "+adminSecret)
		recAdminDel := httptest.NewRecorder()
		h.ServeHTTP(recAdminDel, reqAdminDel)
		if recAdminDel.Code != http.StatusConflict && recAdminDel.Code != http.StatusForbidden {
			t.Errorf("Admin deleting root token status = %d, want 403/409", recAdminDel.Code)
		}

		// Create a disposable admin token to test root deletion
		dispAdminID, _, err := srv.store.Add("dispadmin", internal.RoleAdmin)
		if err != nil {
			t.Fatal(err)
		}

		// Root attempting to delete admin token -> allowed
		reqAdminDelAdmin := httptest.NewRequest("DELETE", "/_/api/tokens/"+dispAdminID, nil)
		reqAdminDelAdmin.Header.Set("Authorization", "Bearer "+rootSecret)
		recAdminDelAdmin := httptest.NewRecorder()
		h.ServeHTTP(recAdminDelAdmin, reqAdminDelAdmin)
		if recAdminDelAdmin.Code != http.StatusOK {
			t.Errorf("Root deleting admin token status = %d, want 200", recAdminDelAdmin.Code)
		}
	})

	t.Run("UI Pages Role Access Control Matrix", func(t *testing.T) {
		uiTests := []struct {
			name       string
			path       string
			uploadWant int
			adminWant  int
			rootWant   int
		}{
			{
				name:       "Users List /_/users",
				path:       "/_/users",
				uploadWant: http.StatusForbidden,
				adminWant:  http.StatusOK,
				rootWant:   http.StatusOK,
			},
			{
				name:       "User Profile /_/user/{targetID}",
				path:       "/_/user/" + targetUploadID,
				uploadWant: http.StatusForbidden,
				adminWant:  http.StatusOK,
				rootWant:   http.StatusOK,
			},
		}

		for _, tt := range uiTests {
			t.Run(tt.name, func(t *testing.T) {
				for _, roleCase := range []struct {
					role   string
					secret string
					want   int
				}{
					{"upload", uploadSecret, tt.uploadWant},
					{"admin", adminSecret, tt.adminWant},
					{"root", rootSecret, tt.rootWant},
				} {
					req := httptest.NewRequest("GET", tt.path, nil)
					req.AddCookie(&http.Cookie{Name: adminCookieName, Value: roleCase.secret})
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)

					if rec.Code != roleCase.want {
						t.Errorf("[%s role] GET %s status = %d, want %d", roleCase.role, tt.path, rec.Code, roleCase.want)
					}
				}
			})
		}
	})

	t.Run("SSR Role Permission Matrix", func(t *testing.T) {
		// 1. Upload role attempting SSR create token -> redirected with error
		reqUpload := httptest.NewRequest("POST", "/_/tokens/create", strings.NewReader("label=test&role=upload&_csrf=token"))
		reqUpload.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqUpload.AddCookie(&http.Cookie{Name: adminCookieName, Value: uploadSecret})
		recUpload := httptest.NewRecorder()
		h.ServeHTTP(recUpload, reqUpload)
		if recUpload.Code != http.StatusSeeOther {
			t.Errorf("Upload role SSR create token status = %d, want 303", recUpload.Code)
		}

		// 2. Admin role attempting SSR role modification (root required) -> redirected with error
		reqAdminRole := httptest.NewRequest("POST", "/_/tokens/"+targetUploadID+"/role", strings.NewReader("role=admin&_csrf=token"))
		reqAdminRole.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqAdminRole.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminSecret})
		recAdminRole := httptest.NewRecorder()
		h.ServeHTTP(recAdminRole, reqAdminRole)
		if recAdminRole.Code != http.StatusSeeOther {
			t.Errorf("Admin role SSR set role status = %d, want 303", recAdminRole.Code)
		}

		// 3. Root role attempting SSR role modification -> allowed (303 redirect back)
		reqRootRole := httptest.NewRequest("POST", "/_/tokens/"+targetUploadID+"/role", strings.NewReader("role=admin&_csrf=token"))
		reqRootRole.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqRootRole.AddCookie(&http.Cookie{Name: adminCookieName, Value: rootSecret})
		recRootRole := httptest.NewRecorder()
		h.ServeHTTP(recRootRole, reqRootRole)
		if recRootRole.Code != http.StatusSeeOther {
			t.Errorf("Root role SSR set role status = %d, want 303", recRootRole.Code)
		}
	})
}
