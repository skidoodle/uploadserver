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
	"log"
	"net/http"
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
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	setCookie(w, r, name, "", -1)
}

func setAdminCookie(w http.ResponseWriter, r *http.Request, secret string) {
	setCookie(w, r, adminCookieName, secret, 0)
}

func setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	setCookie(w, r, csrfCookieName, token, 0)
}

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

func (s *server) renderAdmin(w http.ResponseWriter, data adminPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminTmpl.Execute(w, data); err != nil {
		log.Printf("admin template error: %v", err)
	}
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

// handleAdminPage renders the login screen or the dashboard, and shows any
// one-shot flash message left by the previous request.
func (s *server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	csrf := generateCSRF()
	setCSRFCookie(w, r, csrf)

	data := adminPageData{CSRF: csrf}

	if c, err := r.Cookie(adminCookieName); err == nil {
		if rec, ok := s.store.Authenticate(c.Value); ok && internal.IsAdmin(rec.Role) {
			data.LoggedIn = true
			data.Tokens = s.store.List()
			data.Count = len(data.Tokens)
			data.Global = s.store.GlobalLimits()
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

// handleAdminLogin checks the submitted token and, if it is a valid admin
// credential, drops the session cookie and sends the user to the dashboard.
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
	rec, ok := s.store.Authenticate(token)
	if !ok || !internal.IsAdmin(rec.Role) {
		redirectWithError(w, r, "invalid token")
		return
	}
	setAdminCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if !validateCSRF(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	clearCookie(w, r, adminCookieName)
	http.Redirect(w, r, "/", http.StatusSeeOther)
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

	if flashData, err := json.Marshal(newTokenSecret{ID: id, Role: role, Secret: secret}); err == nil {
		setCookie(w, r, flashSecretName, base64.URLEncoding.EncodeToString(flashData), 0)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) handleAdminDeleteTokenSSR(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminCookie(w, r) {
		return
	}
	if !validateCSRF(r) {
		redirectWithError(w, r, "invalid request")
		return
	}

	if err := s.store.Remove(r.PathValue("id")); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleAdminSetLimitsSSR updates a token's quotas from the dashboard's limits
// dialog, accepting human sizes (e.g. "5GB") and counts.
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleAdminSetGlobalLimitsSSR updates the server-wide default quota from the
// dashboard's global-quota form.
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

func (s *server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.store.List(), "global": s.store.GlobalLimits()})
}

// handleCreateToken is the JSON API for minting a token. The request body is
// optional (an empty body defaults to an upload token); root is never allowed.
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

// handleSetDisabled returns the enable/disable API handler for the given target
// state, sharing the lookup and error mapping between both routes.
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

// redirectWithError stashes a message in a flash cookie and bounces back to the
// dashboard, which renders and clears it.
func redirectWithError(w http.ResponseWriter, r *http.Request, msg string) {
	setCookie(w, r, flashErrorName, base64.URLEncoding.EncodeToString([]byte(msg)), 0)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
