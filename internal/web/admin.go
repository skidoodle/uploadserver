package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"uploadserver/internal"
)

const (
	adminCookieName = "admin_token"
	csrfCookieName  = "csrf"
	flashSecretName = "flash_secret"
	flashErrorName  = "flash_error"
)

// setCookie writes a state cookie hardened for an admin surface: HttpOnly and
// SameSite=Strict always, plus Secure whenever the request came in over HTTPS so
// the bearer secret never rides a plaintext connection. On plain HTTP (local
// runs without a TLS proxy) Secure stays off so the cookie still works. maxAge
// follows net/http semantics: 0 means a session cookie, negative deletes it.
func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure flag dynamically toggles based on HTTPS request state
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// clearCookie clears the cookie with the given name
func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	setCookie(w, r, name, "", -1)
}

// setAdminCookie sets the admin cookie with the given secret
func setAdminCookie(w http.ResponseWriter, r *http.Request, secret string) {
	setCookie(w, r, adminCookieName, secret, 0)
}

// setCSRFCookie sets the CSRF cookie with the given token
func setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	setCookie(w, r, csrfCookieName, token, 0)
}

// generateCSRF generates a random CSRF token
func generateCSRF() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// validateCSRF enforces the double-submit pattern: the token in the form body
// must match the one in the CSRF cookie, compared in constant time.
func validateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	form := r.FormValue("_csrf")
	if form == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(form)) == 1
}

// renderAdmin renders the admin page with the given data
func (s *server) renderAdmin(w http.ResponseWriter, data adminPageData) {
	renderTemplate(w, adminTmpl, data)
}

// requireAdminCookie gates the SSR mutation handlers. It returns true only when
// the cookie holds a valid admin token; otherwise it redirects and returns false.
func (s *server) requireAdminCookie(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return false
	}
	rec, ok := s.store.Authenticate(c.Value)
	if !ok || !internal.IsAdmin(rec.Role) {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return false
	}
	return true
}

// requireRootCookie gates the SSR mutation handlers that only the root user is allowed to execute.
func (s *server) requireRootCookie(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return false
	}
	rec, ok := s.store.Authenticate(c.Value)
	if !ok {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return false
	}
	if rec.Role != internal.RoleRoot {
		redirectWithError(w, r, "forbidden: root token required")
		return false
	}
	return true
}

// handleAdminPage renders the login screen or the dashboard, and shows any
// one-shot flash message left by the previous request.
func (s *server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	csrf := generateCSRF()
	setCSRFCookie(w, r, csrf)

	data := adminPageData{
		CSRF:      csrf,
		InvPolicy: s.store.InvitePolicy(),
	}

	if c, err := r.Cookie(adminCookieName); err == nil {
		if rec, ok := s.store.Authenticate(c.Value); ok {
			data.LoggedIn = true
			data.IsAdmin = internal.IsAdmin(rec.Role)
			data.IsRoot = (rec.Role == internal.RoleRoot)
			data.CurrentToken = &rec
			if data.IsAdmin {
				all := s.store.List()
				data.Tokens = all[:0:0]
				for _, t := range all {
					if t.Role == internal.RoleRoot || t.Role == internal.RoleAdmin {
						data.Tokens = append(data.Tokens, t)
					}
				}
				data.Count = len(all)
				data.Global = s.store.GlobalLimits()
			}
		} else {
			clearCookie(w, r, adminCookieName)
			data.Error = "session expired, please log in again"
		}
	}

	// Flash cookies are read once and immediately expired.
	if c, err := r.Cookie(flashSecretName); err == nil {
		clearCookie(w, r, flashSecretName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			var secretData newTokenSecret
			if json.Unmarshal(decoded, &secretData) == nil {
				data.Secret = &secretData
			}
		}
	}
	if c, err := r.Cookie(flashErrorName); err == nil {
		clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}

	s.renderAdmin(w, data)
}

// handleAdminLogin checks the submitted token and, if valid, drops the session cookie
// and sends the user to the dashboard.
func (s *server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}
	token := r.FormValue("token")
	if token == "" {
		redirectWithError(w, r, "token required")
		return
	}
	if _, ok := s.store.Authenticate(token); !ok {
		redirectWithError(w, r, "invalid token")
		return
	}
	setAdminCookie(w, r, token)
	redirect(w, r)
}

// handleAdminLogout clears the admin cookie and redirects the user to the login page.
// It validates the CSRF token and then clears the admin cookie before redirecting.
func (s *server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if !validateCSRF(r) {
		redirect(w, r)
		return
	}
	clearCookie(w, r, adminCookieName)
	redirect(w, r)
}

// handleAdminCreateTokenSSR mints a token and stashes its one-time secret in a
// flash cookie so the dashboard can show it exactly once after the redirect.
func (s *server) handleAdminCreateTokenSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	label := r.FormValue("label")
	role := r.FormValue("role")
	if role == "" {
		role = internal.RoleUpload
	}
	if role == internal.RoleRoot {
		redirectWithError(w, r, "root tokens are generated only on first run")
		return
	}

	id, secret, err := s.store.Add(label, role)
	if err != nil {
		redirectWithError(w, r, err.Error())
		return
	}

	if flashData, err := json.Marshal(newTokenSecret{ID: id, Role: role, Secret: secret}); err == nil { // #nosec G117 -- One-time flash cookie for displaying generated token secret
		setCookie(w, r, flashSecretName, base64.URLEncoding.EncodeToString(flashData), 0)
	}
	redirect(w, r)
}

// handleAdminToggleTokenSSR toggles the disabled state of a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminToggleTokenSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	id := r.PathValue("id")
	disable := r.FormValue("disable") == "true"
	if err := s.store.SetDisabled(id, disable); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	redirect(w, r)
}

// handleAdminSetRoleSSR sets the role of a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminSetRoleSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireRootCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	id := r.PathValue("id")
	role := r.FormValue("role")
	if err := s.store.SetRole(id, role); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	redirect(w, r)
}

// handleAdminDeleteTokenSSR deletes a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminDeleteTokenSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	id := r.PathValue("id")
	if err := s.store.Remove(id); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) && !strings.HasSuffix(ref, "/"+id) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	http.Redirect(w, r, "/_/users", http.StatusSeeOther)
}

// handleAdminSetLimitsSSR updates a token's quotas from the dashboard's limits
// dialog, accepting human sizes (e.g. "5GB") and counts.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminSetLimitsSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	lim, err := parseLimitsForm(r)
	if err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	bypass := r.FormValue("bypass") == "on" || r.FormValue("bypass") == "true"
	if err := s.store.SetLimits(r.PathValue("id"), lim, bypass); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	if invitesStr := r.FormValue("invites"); invitesStr != "" {
		invites, _ := strconv.Atoi(invitesStr)
		_ = s.store.SetInvites(r.PathValue("id"), invites)
	}
	redirect(w, r)
}

// handleInviteTokenSSR allows an authorized token user (or admin) to create a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleInviteTokenSSR(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	userRec, ok := s.store.Authenticate(c.Value)
	if !ok {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	label := r.FormValue("label")
	id, secret, err := s.store.AddWithInvite(userRec.ID, label)
	if err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	if flashData, err := json.Marshal(newTokenSecret{ID: id, Role: internal.RoleUpload, Secret: secret}); err == nil { // #nosec G117 -- One-time flash cookie for displaying generated token secret
		setCookie(w, r, flashSecretName, base64.URLEncoding.EncodeToString(flashData), 0)
	}
	redirect(w, r)
}

// handleAdminSetGlobalLimitsSSR updates the server-wide default quota from the
// dashboard's global-quota form.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminSetGlobalLimitsSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	lim, err := parseLimitsForm(r)
	if err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	if err := s.store.SetGlobalLimits(lim); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleListTokens lists all tokens and the global quota.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.store.List(), "global": s.store.GlobalLimits()})
}

// handleCreateToken is the JSON API for minting a token. The request body is
// optional (an empty body defaults to an upload token); root is never allowed.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Label string `json:"label"`
		Role  string `json:"role"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	if req.Role == "" {
		req.Role = internal.RoleUpload
	}
	if req.Role == internal.RoleRoot {
		httpError(w, http.StatusForbidden, "root tokens are generated only on first run; use `token reset` to replace it")
		return
	}

	id, secret, err := s.store.Add(req.Label, req.Role)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     id,
		"label":  req.Label,
		"role":   req.Role,
		"secret": secret, // shown once
	})
}

// handleDeleteToken deletes a token by its ID
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.Remove(r.PathValue("id")); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSetLabelSSR handles renaming a token via form submit.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetLabelSSR(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	userRec, ok := s.store.Authenticate(c.Value)
	if !ok {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	id := r.PathValue("id")
	if !internal.IsAdmin(userRec.Role) && userRec.ID != id {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	newLabel := r.FormValue("label")
	if err := s.store.SetLabel(id, newLabel); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	redirect(w, r)
}

// handleSetLabel is the JSON API endpoint for renaming a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetLabel(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authenticate(r)
	if !ok {
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	if !internal.IsAdmin(rec.Role) && rec.ID != id {
		httpError(w, http.StatusForbidden, "forbidden")
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetLabel(id, req.Label); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "label": req.Label})
}

// handleSetRole is the JSON API endpoint for changing a token's role.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireRoot(w, r) {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetRole(id, req.Role); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": req.Role})
}

// handleSetDisabled returns the enable/disable API handler for the given target
// state, sharing the lookup and error mapping between both routes.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetDisabled(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAdmin(w, r) {
			return
		}
		if err := s.store.SetDisabled(r.PathValue("id"), disabled); err != nil {
			writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "disabled": disabled})
	}
}

// handleSetLimits is the JSON API for setting a token's quotas. Caps are given
// as raw byte/count integers and "bypass" toggles exemption from the global
// rate limit.
// It requires the admin cookie and a valid CSRF token.
// default; an empty body clears every quota and the bypass flag.
func (s *server) handleSetLimits(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		internal.Limits
		Bypass bool `json:"bypass"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	if err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetLimits(r.PathValue("id"), req.Limits, req.Bypass); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSetGlobalLimits is the JSON API for the server-wide default quota.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetGlobalLimits(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var lim internal.Limits
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&lim)
	if err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetGlobalLimits(lim); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// parseLimitsForm reads the four quota fields off a form, accepting human sizes
// for the byte caps and plain integers for the count caps.
func parseLimitsForm(r *http.Request) (internal.Limits, error) {
	maxBytes, err := internal.ParseSize(r.FormValue("max_bytes"))
	if err != nil {
		return internal.Limits{}, fmt.Errorf("total size: %w", err)
	}
	monthlyBytes, err := internal.ParseSize(r.FormValue("monthly_bytes"))
	if err != nil {
		return internal.Limits{}, fmt.Errorf("monthly size: %w", err)
	}
	maxUploads, err := parseCount(r.FormValue("max_uploads"))
	if err != nil {
		return internal.Limits{}, fmt.Errorf("total uploads: %w", err)
	}
	monthlyUploads, err := parseCount(r.FormValue("monthly_uploads"))
	if err != nil {
		return internal.Limits{}, fmt.Errorf("monthly uploads: %w", err)
	}
	return internal.Limits{
		MaxBytes:       maxBytes,
		MaxUploads:     maxUploads,
		MonthlyBytes:   monthlyBytes,
		MonthlyUploads: monthlyUploads,
	}, nil
}

// parseCount reads an upload-count cap, treating blank/0/"off"/"none" as
// unlimited and tolerating thousands separators.
// It returns an error if the value is not a valid count.
func parseCount(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	switch strings.ToLower(s) {
	case "", "0", "off", "none", "unlimited":
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid count %q", s)
	}
	return n, nil
}

// writeStoreErr translates a token-store error into the closest HTTP status.
// It returns a generic error message for unknown errors.
func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, internal.ErrNotFound):
		httpError(w, http.StatusNotFound, "token not found")
	case errors.Is(err, internal.ErrLastAdmin), errors.Is(err, internal.ErrProtectedRoot):
		httpError(w, http.StatusConflict, err.Error())
	default:
		httpError(w, http.StatusBadRequest, err.Error())
	}
}

// isSafeRedirectTarget checks whether a redirect URL is safe (same origin or local relative path).
func isSafeRedirectTarget(target string, reqHost string) bool {
	if target == "" {
		return false
	}
	if strings.ContainsAny(target, "\r\n\t") {
		return false
	}
	if strings.HasPrefix(target, "/") {
		if strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
			return false
		}
		return true
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return u.Host != "" && strings.EqualFold(u.Host, reqHost)
}

// redirect stashes status and redirects back to Referer or default path.
// It returns the redirect status code.
func redirect(w http.ResponseWriter, r *http.Request) {
	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	if id := r.PathValue("id"); id != "" {
		http.Redirect(w, r, "/_/user/"+id, http.StatusSeeOther) // #nosec G710 -- Safe internal user profile URL
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// redirectWithError stashes a message in a flash cookie and bounces back to the
// Referer or fallback.
// It returns the redirect status code.
func redirectWithError(w http.ResponseWriter, r *http.Request, msg string) {
	setCookie(w, r, flashErrorName, base64.URLEncoding.EncodeToString([]byte(msg)), 0)
	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	if id := r.PathValue("id"); id != "" {
		http.Redirect(w, r, "/_/user/"+id, http.StatusSeeOther) // #nosec G710 -- Safe internal user profile URL
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleUserUploads renders the per-token upload history page.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleUserUploads(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	userRec, ok := s.store.Authenticate(c.Value)
	if !ok {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return
	}

	tokenID := r.PathValue("id")
	if !internal.IsAdmin(userRec.Role) && userRec.ID != tokenID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	records := s.store.List()
	var rec *internal.TokenRecord
	for i := range records {
		if records[i].ID == tokenID {
			rec = &records[i]
			break
		}
	}
	if rec == nil {
		http.NotFound(w, r)
		return
	}

	entries, _ := s.store.UploadsFor(tokenID)
	totalAll := len(entries)

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		lower := strings.ToLower(query)
		filtered := entries[:0:0]
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Name), lower) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	totalFiles := len(entries)

	const perPage = 50
	totalPages := max((totalFiles+perPage-1)/perPage, 1)

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p >= 1 {
		page = p
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	end := min(start+perPage, totalFiles)
	pageEntries := entries[start:end]

	csrf := generateCSRF()
	setCSRFCookie(w, r, csrf)

	totalUnfiltered := 0
	if query != "" {
		totalUnfiltered = totalAll
	}

	data := uploadsPageData{
		Token:           *rec,
		Uploads:         pageEntries,
		BaseURL:         s.cfg.BaseURL,
		CSRF:            csrf,
		Page:            page,
		TotalPages:      totalPages,
		TotalFiles:      totalFiles,
		TotalUnfiltered: totalUnfiltered,
		PerPage:         perPage,
		PageStart:       start + 1,
		PageEnd:         end,
		Query:           query,
	}

	renderTemplate(w, uploadsTmpl, data)
}

// handleAdminUsersPage renders the paginated and searchable users list.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminUsersPage(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	userRec, ok := s.store.Authenticate(c.Value)
	if !ok {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return
	}
	if !internal.IsAdmin(userRec.Role) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	tokens := s.store.List()
	totalAll := len(tokens)

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		lower := strings.ToLower(query)
		filtered := tokens[:0:0]
		for _, t := range tokens {
			if strings.Contains(strings.ToLower(t.Label), lower) || strings.Contains(strings.ToLower(t.ID), lower) || strings.Contains(strings.ToLower(t.Role), lower) {
				filtered = append(filtered, t)
			}
		}
		tokens = filtered
	}

	totalTokens := len(tokens)

	const perPage = 50
	totalPages := max((totalTokens+perPage-1)/perPage, 1)

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p >= 1 {
		page = p
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	end := min(start+perPage, totalTokens)
	pageTokens := tokens[start:end]

	csrf := generateCSRF()
	setCSRFCookie(w, r, csrf)

	totalUnfiltered := 0
	if query != "" {
		totalUnfiltered = totalAll
	}

	data := usersPageData{
		LoggedIn:        true,
		IsAdmin:         true,
		IsRoot:          userRec.Role == internal.RoleRoot,
		CurrentToken:    &userRec,
		Tokens:          pageTokens,
		Count:           totalTokens,
		TotalUnfiltered: totalUnfiltered,
		CSRF:            csrf,
		Page:            page,
		TotalPages:      totalPages,
		PageStart:       start + 1,
		PageEnd:         end,
		Query:           query,
		Global:          s.store.GlobalLimits(),
		InvPolicy:       s.store.InvitePolicy(),
	}

	// Read flash cookies
	if c, err := r.Cookie(flashSecretName); err == nil {
		clearCookie(w, r, flashSecretName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			var secretData newTokenSecret
			if json.Unmarshal(decoded, &secretData) == nil {
				data.Secret = &secretData
			}
		}
	}
	if c, err := r.Cookie(flashErrorName); err == nil {
		clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}

	renderTemplate(w, usersTmpl, data)
}

// handleAdminUserProfilePage renders the user profile card view for a specific token ID.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminUserProfilePage(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	userRec, ok := s.store.Authenticate(c.Value)
	if !ok {
		clearCookie(w, r, adminCookieName)
		redirectWithError(w, r, "session expired")
		return
	}
	if !internal.IsAdmin(userRec.Role) {
		httpError(w, http.StatusForbidden, "admin token required")
		return
	}

	targetID := r.PathValue("id")
	targetRec, found := s.store.GetRecord(targetID)
	if !found {
		redirectWithError(w, r, "user not found")
		return
	}

	csrf := generateCSRF()
	setCSRFCookie(w, r, csrf)

	data := userProfilePageData{
		LoggedIn:     true,
		IsAdmin:      true,
		IsRoot:       userRec.Role == internal.RoleRoot,
		CurrentToken: &userRec,
		TargetToken:  targetRec,
		Global:       s.store.GlobalLimits(),
		CSRF:         csrf,
	}

	if c, err := r.Cookie(flashErrorName); err == nil {
		clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}

	renderTemplate(w, userProfileTmpl, data)
}

// handleAPITokenUploads returns JSON upload history for token id.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAPITokenUploads(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	tokenID := r.PathValue("id")
	entries, err := s.store.UploadsFor(tokenID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token_id": tokenID,
		"uploads":  entries,
	})
}

// handleGiveawaySSR handles the SSR request to add N invites to all upload tokens.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleGiveawaySSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	countStr := r.FormValue("count")
	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		redirectWithError(w, r, "invite count must be a positive number")
		return
	}

	mode := r.FormValue("mode")
	pool, _ := strconv.Atoi(r.FormValue("pool"))
	maxCap, _ := strconv.Atoi(r.FormValue("max_cap"))

	if mode == "random" {
		if pool <= 0 {
			redirectWithError(w, r, "pool size must be positive for random mode")
			return
		}
		_, err = s.store.AddInvitesToRandomUploaders(count, pool, maxCap)
	} else {
		_, err = s.store.AddInvitesToAllUploadersCapped(count, maxCap)
	}

	if err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	redirect(w, r)
}

// handleGiveawayAPI handles the JSON API request to add N invites to all upload tokens.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleGiveawayAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Count  int    `json:"count"`
		Mode   string `json:"mode"`
		Pool   int    `json:"pool"`
		MaxCap int    `json:"max_cap"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || req.Count <= 0 {
		httpError(w, http.StatusBadRequest, "count must be a positive integer")
		return
	}

	var updated int
	var err error
	if req.Mode == "random" {
		if req.Pool <= 0 {
			httpError(w, http.StatusBadRequest, "pool size must be positive for random mode")
			return
		}
		updated, err = s.store.AddInvitesToRandomUploaders(req.Count, req.Pool, req.MaxCap)
	} else {
		updated, err = s.store.AddInvitesToAllUploadersCapped(req.Count, req.MaxCap)
	}

	if err != nil {
		writeStoreErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":            true,
		"updated_users": updated,
		"added_invites": req.Count,
	})
}

// handleSetInvitePolicySSR handles setting the invite policy via form submission.
func (s *server) handleSetInvitePolicySSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	schedInterval, _ := strconv.Atoi(r.FormValue("sched_interval"))
	schedCount, _ := strconv.Atoi(r.FormValue("sched_count"))
	schedPool, _ := strconv.Atoi(r.FormValue("sched_pool"))
	schedMax, _ := strconv.Atoi(r.FormValue("sched_max"))
	newuserCount, _ := strconv.Atoi(r.FormValue("newuser_count"))
	newuserDelay, _ := strconv.Atoi(r.FormValue("newuser_delay"))
	newuserMax, _ := strconv.Atoi(r.FormValue("newuser_max"))

	pol := internal.InvitePolicy{
		SchedEnabled:   r.FormValue("sched_on") != "",
		SchedInterval:  int64(schedInterval),
		SchedCount:     schedCount,
		SchedMode:      r.FormValue("sched_mode"),
		SchedPool:      schedPool,
		SchedMax:       schedMax,
		NewUserEnabled: r.FormValue("newuser_on") != "",
		NewUserCount:   newuserCount,
		NewUserDelay:   int64(newuserDelay),
		NewUserMax:     newuserMax,
	}

	if err := s.store.SetInvitePolicy(pol); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	redirect(w, r)
}

// handleSetInvitePolicyAPI handles setting the invite policy via JSON API.
func (s *server) handleSetInvitePolicyAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var pol internal.InvitePolicy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&pol); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.SetInvitePolicy(pol); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGetInvitePolicyAPI handles getting the invite policy via JSON API.
func (s *server) handleGetInvitePolicyAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.store.InvitePolicy())
}
