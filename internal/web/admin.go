package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"uploadserver/internal"
)

var tokenIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

const (
	adminCookieName = "admin_token"
	csrfCookieName  = "csrf"
	flashSecretName = "flash_secret"
	flashErrorName  = "flash_error"
	flashNoticeName = "flash_notice"
)

type sessionInfo struct {
	tokenID   string
	role      string
	expiresAt time.Time
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]sessionInfo
}

func newSessionStore() *sessionStore {
	return &sessionStore{
		sessions: make(map[string]sessionInfo),
	}
}

// Create creates a new session with the given tokenID, role, and TTL.
// It returns the session ID.
//
// The session ID is a random hex string of 32 bytes.
func (st *sessionStore) Create(tokenID, role string, ttl time.Duration) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	id := hex.EncodeToString(b[:])

	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessions[id] = sessionInfo{
		tokenID:   tokenID,
		role:      role,
		expiresAt: time.Now().Add(ttl),
	}
	return id
}

// Get returns the session info for the given session ID, if it exists and is not expired.
// If the session does not exist or is expired, it returns an empty sessionInfo and false.
//
// The session info contains the token ID, role, and expiration time.
func (st *sessionStore) Get(sessionID string) (sessionInfo, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	info, ok := st.sessions[sessionID]
	if !ok || time.Now().After(info.expiresAt) {
		return sessionInfo{}, false
	}
	return info, true
}

// Delete deletes the session with the given session ID, if it exists.
// If the session does not exist, it does nothing.
//
// This method is safe to call concurrently with other methods on the same sessionStore.
func (st *sessionStore) Delete(sessionID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, sessionID)
}

// DeleteByTokenID deletes all sessions with the given token ID, if they exist.
// If no sessions have the token ID, it does nothing.
//
// This method is safe to call concurrently with other methods on the same sessionStore.
func (st *sessionStore) DeleteByTokenID(tokenID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for id, s := range st.sessions {
		if s.tokenID == tokenID {
			delete(st.sessions, id)
		}
	}
}

// setCookie writes a state cookie hardened for an admin surface: HttpOnly and
// SameSite=Strict always, plus Secure whenever the request came in over HTTPS so
// the bearer secret never rides a plaintext connection. On plain HTTP (local
// runs without a TLS proxy) Secure stays off so the cookie still works. maxAge
// follows net/http semantics: 0 means a session cookie, negative deletes it.
func (s *server) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure flag dynamically toggles based on HTTPS request state
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r, s.cfg.TrustProxyHeaders),
		SameSite: http.SameSiteStrictMode,
	})
}

// clearCookie clears the cookie with the given name
func (s *server) clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	s.setCookie(w, r, name, "", -1)
}

// setAdminCookie sets the admin cookie with the given secret
func (s *server) setAdminCookie(w http.ResponseWriter, r *http.Request, secret string) {
	s.setCookie(w, r, adminCookieName, secret, 0)
}

// setCSRFCookie sets the CSRF cookie with the given token
func (s *server) setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	s.setCookie(w, r, csrfCookieName, token, 0)
}

// generateCSRF generates a random CSRF token
func generateCSRF(sessionSecret string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	nonce := hex.EncodeToString(b[:])
	if sessionSecret == "" {
		return nonce
	}
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	_, _ = mac.Write([]byte(nonce))
	return nonce + "." + hex.EncodeToString(mac.Sum(nil))
}

// csrfForRequest returns the CSRF token for the given request, either from the CSRF cookie or a new token if not present.
func csrfForRequest(r *http.Request) string {
	if cookie, err := r.Cookie(adminCookieName); err == nil {
		return generateCSRF(cookie.Value)
	}
	return generateCSRF("")
}

// validateCSRF enforces the double-submit pattern: the token in the form body
// must match the one in the CSRF cookie, compared in constant time.
func validateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	form := r.FormValue("_csrf")
	if form == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(form)) != 1 {
		return false
	}
	admin, err := r.Cookie(adminCookieName)
	if err != nil || admin.Value == "" {
		return !strings.Contains(cookie.Value, ".")
	}
	nonce, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(admin.Value))
	_, _ = mac.Write([]byte(nonce))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) == 1
}

// renderAdmin renders the admin page with the given data
func (s *server) renderAdmin(w http.ResponseWriter, data adminPageData) {
	renderTemplate(w, adminTmpl, data)
}

// requireSession resolves the current cookie session once and returns its token
// record. Expired sessions clear all session-bound state.
func (s *server) requireSession(w http.ResponseWriter, r *http.Request) (internal.TokenRecord, bool) {
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return internal.TokenRecord{}, false
	}
	rec, ok := s.validateSessionCookie(c)
	if !ok {
		s.clearSessionCookies(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return internal.TokenRecord{}, false
	}
	return rec, true
}

// requireAdminCookie gates admin-only SSR handlers and returns the actor record.
func (s *server) requireAdminCookie(w http.ResponseWriter, r *http.Request) (internal.TokenRecord, bool) {
	rec, ok := s.requireSession(w, r)
	if !ok {
		return internal.TokenRecord{}, false
	}
	if !internal.IsAdmin(rec.Role) {
		s.redirectWithError(w, r, "forbidden: admin token required")
		return internal.TokenRecord{}, false
	}
	return rec, true
}

// requireRootCookie gates root-only SSR handlers and returns the actor record.
func (s *server) requireRootCookie(w http.ResponseWriter, r *http.Request) (internal.TokenRecord, bool) {
	rec, ok := s.requireSession(w, r)
	if !ok {
		return internal.TokenRecord{}, false
	}
	if rec.Role != internal.RoleRoot {
		s.redirectWithError(w, r, "forbidden: root token required")
		return internal.TokenRecord{}, false
	}
	return rec, true
}

// clearSessionCookies clears all session cookies, including the admin token, CSRF token, and flash messages.
func (s *server) clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{adminCookieName, csrfCookieName, flashSecretName, flashErrorName, flashNoticeName} {
		s.clearCookie(w, r, name)
	}
}

// handleAdminPage renders the login screen or the dashboard, and shows any
// one-shot flash message left by the previous request.
func (s *server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	csrf := csrfForRequest(r)
	s.setCSRFCookie(w, r, csrf)

	data := adminPageData{
		CSRF:      csrf,
		InvPolicy: s.store.InvitePolicy(),
	}
	sessionExpired := false

	if c, err := r.Cookie(adminCookieName); err == nil {
		if rec, ok := s.validateSessionCookie(c); ok {
			data.LoggedIn = true
			data.IsAdmin = internal.IsAdmin(rec.Role)
			data.IsRoot = (rec.Role == internal.RoleRoot)
			data.CurrentToken = &rec
			if p, ok := s.store.GetPendingPurge(rec.ID); ok {
				data.PendingPurge = &p
			}
			if data.IsAdmin {
				if purges, err := s.store.ListPendingPurges(); err == nil {
					data.PendingPurgeCount = len(purges)
				}
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
			sessionExpired = true
			s.clearSessionCookies(w, r)
			csrf = generateCSRF("")
			s.setCSRFCookie(w, r, csrf)
			data.CSRF = csrf
			data.Error = "session expired, please log in again"
		}
	}

	// Flash cookies are read once and immediately expired.
	if c, err := r.Cookie(flashSecretName); !sessionExpired && err == nil {
		s.clearCookie(w, r, flashSecretName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			var secretData newTokenSecret
			if json.Unmarshal(decoded, &secretData) == nil {
				data.Secret = &secretData
			}
		}
	}
	if c, err := r.Cookie(flashErrorName); !sessionExpired && err == nil {
		s.clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}
	if c, err := r.Cookie(flashNoticeName); !sessionExpired && err == nil {
		s.clearCookie(w, r, flashNoticeName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Notice = string(decoded)
		}
	}

	s.renderAdmin(w, data)
}

// roleActionName returns the human-readable action name for a role change.
// It returns "promote token" if the role is being promoted, "demote token" if the role is being demoted,
// and "token role updated" if the role remains the same.
func roleActionName(oldRole, newRole string) string {
	rank := map[string]int{
		internal.RoleUpload: 1,
		internal.RoleAdmin:  2,
		internal.RoleRoot:   3,
	}
	if rank[newRole] > rank[oldRole] {
		return "promote token"
	}
	if rank[newRole] < rank[oldRole] {
		return "demote token"
	}
	return "token role updated"
}

// handleAdminLogin checks the submitted token and, if valid, drops the session cookie
// and sends the user to the dashboard.
func (s *server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if s.loginLimiter != nil && !s.loginLimiter.Allow(clientIP(r, s.cfg.TrustProxyHeaders)) {
		slog.Warn("login rate limited", "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		http.Error(w, "too many login attempts, please try again later", http.StatusTooManyRequests)
		return
	}
	if !validateCSRF(r) {
		slog.Warn("login failed", "reason", "invalid_csrf", "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		s.redirectWithError(w, r, "invalid request")
		return
	}
	token := r.FormValue("token")
	if token == "" {
		slog.Warn("login failed", "reason", "missing_token", "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		s.redirectWithError(w, r, "token required")
		return
	}
	rec, ok := s.store.Authenticate(token)
	if !ok {
		slog.Warn("login failed", "reason", "invalid_token", "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		s.redirectWithError(w, r, "invalid token")
		return
	}
	if s.sessions == nil {
		s.sessions = newSessionStore()
	}
	sessionID := s.sessions.Create(rec.ID, rec.Role, 7*24*time.Hour)
	s.setAdminCookie(w, r, sessionID)
	s.setCSRFCookie(w, r, generateCSRF(sessionID))
	s.clearCookie(w, r, flashErrorName)
	slog.Info("login succeeded", "id", rec.ID, "label", internal.SanitizeLog(rec.Label), "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	redirect(w, r)
}

// handleAdminLogout clears the admin cookie and redirects the user to the login page.
// It validates the CSRF token and then clears the admin cookie before redirecting.
func (s *server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		redirect(w, r)
		return
	}
	if c, err := r.Cookie(adminCookieName); err == nil && s.sessions != nil {
		s.sessions.Delete(c.Value)
	}
	s.clearSessionCookies(w, r)
	slog.Info("logout", "id", actor.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	redirect(w, r)
}

// handleAdminCreateTokenSSR mints a token and stashes its one-time secret in a
// flash cookie so the dashboard can show it exactly once after the redirect.
func (s *server) handleAdminCreateTokenSSR(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdminCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	label := r.FormValue("label")
	role := r.FormValue("role")
	if role == "" {
		role = internal.RoleUpload
	}
	if role == internal.RoleRoot {
		s.redirectWithError(w, r, "root tokens are generated only on first run")
		return
	}

	actorID := actor.ID
	id, secret, err := s.store.Add(label, role)
	if err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}

	actionMsg := "upload token creation"
	if internal.IsAdmin(role) {
		actionMsg = "admin token creation"
	}
	slog.Info(actionMsg, "id", id, "label", internal.SanitizeLog(label), "role", role, "actor_id", actorID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))

	if flashData, err := json.Marshal(newTokenSecret{ID: id, Role: role, Secret: secret}); err == nil { // #nosec G117 -- One-time flash cookie for displaying generated token secret
		s.setCookie(w, r, flashSecretName, base64.URLEncoding.EncodeToString(flashData), 0)
	}
	redirect(w, r)
}

// handleAdminToggleTokenSSR toggles the disabled state of a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminToggleTokenSSR(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdminCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	actorID := actor.ID
	id := r.PathValue("id")
	disable := r.FormValue("disable") == "true"
	if err := s.store.SetDisabled(id, disable); err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	slog.Info("token state updated", "id", id, "disabled", disable, "actor_id", actorID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	redirect(w, r)
}

// handleAdminSetRoleSSR sets the role of a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminSetRoleSSR(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireRootCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	actorID := actor.ID
	id := r.PathValue("id")
	role := r.FormValue("role")
	oldRec, _ := s.store.GetRecord(id)
	if err := s.store.SetRole(id, role); err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	actionMsg := roleActionName(oldRec.Role, role)
	slog.Info(actionMsg, "id", id, "old_role", oldRec.Role, "new_role", role, "actor_id", actorID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	redirect(w, r)
}

// handleAdminDeleteTokenSSR deletes a token and purges its uploaded media.
// It accepts requests from an admin or the token owner (account self-deletion).
func (s *server) handleAdminDeleteTokenSSR(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	target, found := s.store.GetRecord(r.PathValue("id"))
	if !found {
		s.redirectWithError(w, r, "token not found")
		return
	}
	id := target.ID
	if !internal.IsAdmin(userRec.Role) && userRec.ID != id {
		s.redirectWithError(w, r, "forbidden: admin token or self required")
		return
	}

	phrase := strings.TrimSpace(r.FormValue("confirm_phrase"))
	expectedPhrase := "DELETE " + id
	if phrase != "" && phrase != expectedPhrase && phrase != "DELETE" && phrase != id {
		s.redirectWithError(w, r, fmt.Sprintf("invalid confirmation phrase: must type %q", expectedPhrase))
		return
	}

	if err := s.store.Remove(id); err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	if err := s.purgeUserMedia(id, false); err != nil {
		slog.Error("token deleted but media purge failed", "id", id, "error", err)
		s.redirectWithError(w, r, "token deleted, but media purge failed")
		return
	}
	slog.Info("token deletion", "id", id, "actor_id", userRec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))

	if userRec.ID == id {
		s.clearSessionCookies(w, r)
		s.setCookie(w, r, flashErrorName, base64.URLEncoding.EncodeToString([]byte("account deleted")), 0)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) && !strings.HasSuffix(ref, "/"+id) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	http.Redirect(w, r, "/_/users", http.StatusSeeOther)
}

// handlePurgeUserMediaSSR purges all media for a token via form submit.
// It requires an admin token or self and validates the CSRF token and confirmation phrase before purging.
func (s *server) handlePurgeUserMediaSSR(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	target, found := s.store.GetRecord(r.PathValue("id"))
	if !found {
		s.redirectWithError(w, r, "token not found")
		return
	}
	id := target.ID
	if !internal.IsAdmin(userRec.Role) && userRec.ID != id {
		s.redirectWithError(w, r, "forbidden")
		return
	}

	if _, exists := s.store.GetPendingPurge(id); exists {
		s.redirectWithError(w, r, "media purge already scheduled")
		return
	}

	phrase := strings.TrimSpace(r.FormValue("confirm_phrase"))
	expectedPhrase := "PURGE " + id
	if phrase != expectedPhrase && phrase != "PURGE" && phrase != id {
		s.redirectWithError(w, r, fmt.Sprintf("invalid confirmation phrase: must type %q", expectedPhrase))
		return
	}

	if s.cfg.PurgeGracePeriod > 0 {
		p, err := s.store.SchedulePurge(id, userRec.ID, s.cfg.PurgeGracePeriod)
		if err != nil {
			slog.Error("schedule media purge error", "id", id, "error", err)
			s.redirectWithError(w, r, "could not schedule media purge")
			return
		}
		slog.Info("media purge scheduled", "id", id, "actor_id", userRec.ID, "purge_at", p.PurgeAt, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		ref := r.Header.Get("Referer")
		if isSafeRedirectTarget(ref, r.Host) {
			http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if err := s.purgeUserMedia(id, true); err != nil {
		s.redirectWithError(w, r, "could not purge media")
		return
	}
	slog.Info("media purged", "id", id, "actor_id", userRec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	s.redirectWithNotice(w, r, "media purged")
}

// handleCancelPurgeUserMediaSSR cancels a scheduled media purge for a token.
func (s *server) handleCancelPurgeUserMediaSSR(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	target, found := s.store.GetRecord(r.PathValue("id"))
	if !found {
		s.redirectWithError(w, r, "token not found")
		return
	}
	id := target.ID
	if !internal.IsAdmin(userRec.Role) && userRec.ID != id {
		s.redirectWithError(w, r, "forbidden")
		return
	}

	cancelled, err := s.store.CancelPendingPurge(id)
	if err != nil {
		slog.Error("cancel pending purge error", "id", id, "error", err)
		s.redirectWithError(w, r, "could not cancel media purge")
		return
	}
	if !cancelled {
		s.redirectWithError(w, r, "no scheduled purge found")
		return
	}

	slog.Info("media purge cancelled", "id", id, "actor_id", userRec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	s.redirectWithNotice(w, r, "media purge cancelled")
}

// handleDeleteFileSSR removes a single uploaded file via form submit.
// It requires an admin token and validates the CSRF token before deleting.
func (s *server) handleDeleteFileSSR(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	filename := r.FormValue("filename")
	if filename == "" {
		s.redirectWithError(w, r, "missing filename")
		return
	}

	var ownerID, fullName string
	if s.fileIndex != nil {
		ownerID, fullName = s.fileIndex.Lookup(filename)
	}
	if ownerID == "" {
		s.redirectWithError(w, r, "file not found")
		return
	}
	if fullName == "" {
		fullName = filename
	}

	if !internal.IsAdmin(userRec.Role) && userRec.ID != ownerID {
		s.redirectWithError(w, r, "forbidden")
		return
	}

	disk := filepath.Join(s.cfg.Dir, ownerID, fullName)
	// #nosec G703,G706 -- disk is constructed from validated ownerID and filename from index, log input sanitized
	if err := os.Remove(disk); err != nil && !os.IsNotExist(err) {
		slog.Error("delete file error", "name", internal.SanitizeLog(fullName), "error", err)
		s.redirectWithError(w, r, "could not delete file")
		return
	}

	_ = s.store.RemoveUploadEntry(ownerID, fullName)
	if s.fileIndex != nil {
		s.fileIndex.Remove(fullName)
	}

	slog.Info("deleted file",
		"name", internal.SanitizeLog(fullName),
		"owner_id", internal.SanitizeLog(ownerID),
		"actor_id", internal.SanitizeLog(userRec.ID),
		"actor_label", internal.SanitizeLog(userRec.Label),
		"ip", internal.SanitizeLog(clientIP(r, s.cfg.TrustProxyHeaders)),
	)

	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	http.Redirect(w, r, "/_/uploads/"+ownerID, http.StatusSeeOther) // #nosec G710 -- Safe internal uploads URL
}

// handleAdminSetLimitsSSR updates a token's quotas from the dashboard's limits
// dialog, accepting human sizes (e.g. "5GB") and counts.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminSetLimitsSSR(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdminCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	actorID := actor.ID
	lim, err := parseLimitsForm(r)
	if err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	bypass := r.FormValue("bypass") == "on" || r.FormValue("bypass") == "true"
	id := r.PathValue("id")
	if err := s.store.SetLimits(id, lim, bypass); err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	var invites int
	if invitesStr := r.FormValue("invites"); invitesStr != "" {
		invites, _ = strconv.Atoi(invitesStr)
		_ = s.store.SetInvites(id, invites)
	}
	slog.Info("limits/invite updated",
		"id", id,
		"max_bytes", lim.MaxBytes,
		"max_uploads", lim.MaxUploads,
		"monthly_bytes", lim.MonthlyBytes,
		"monthly_uploads", lim.MonthlyUploads,
		"invites", invites,
		"bypass", bypass,
		"actor_id", actorID,
		"ip", clientIP(r, s.cfg.TrustProxyHeaders),
	)
	redirect(w, r)
}

// handleInviteTokenSSR allows an authorized token user (or admin) to create a token.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleInviteTokenSSR(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	label := r.FormValue("label")
	id, secret, err := s.store.AddWithInvite(userRec.ID, label)
	if err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	slog.Info("invite token creation", "id", id, "inviter_id", userRec.ID, "label", internal.SanitizeLog(label), "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	if flashData, err := json.Marshal(newTokenSecret{ID: id, Role: internal.RoleUpload, Secret: secret}); err == nil { // #nosec G117 -- One-time flash cookie for displaying generated token secret
		s.setCookie(w, r, flashSecretName, base64.URLEncoding.EncodeToString(flashData), 0)
	}
	redirect(w, r)
}

// handleAdminSetGlobalLimitsSSR updates the server-wide default quota from the
// dashboard's global-quota form.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminSetGlobalLimitsSSR(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdminCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	actorID := actor.ID
	lim, err := parseLimitsForm(r)
	if err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	if err := s.store.SetGlobalLimits(lim); err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	slog.Info("global quota updated",
		"max_bytes", lim.MaxBytes,
		"max_uploads", lim.MaxUploads,
		"monthly_bytes", lim.MonthlyBytes,
		"monthly_uploads", lim.MonthlyUploads,
		"actor_id", actorID,
		"ip", clientIP(r, s.cfg.TrustProxyHeaders),
	)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleListTokens lists all tokens and the global quota.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.store.List(), "global": s.store.GlobalLimits()})
}

// handleCreateToken is the JSON API for minting a token. The request body is
// optional (an empty body defaults to an upload token); root is never allowed.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Label string `json:"label"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(w, r, &req, true); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
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
	actionMsg := "upload token creation"
	if internal.IsAdmin(req.Role) {
		actionMsg = "admin token creation"
	}
	slog.Info(actionMsg, "id", id, "label", internal.SanitizeLog(req.Label), "role", req.Role, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     id,
		"label":  req.Label,
		"role":   req.Role,
		"secret": secret, // shown once
	})
}

// handleDeleteToken deletes a token by its ID and purges its uploaded media.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	target, found := s.store.GetRecord(r.PathValue("id"))
	if !found {
		httpError(w, http.StatusNotFound, "token not found")
		return
	}
	id := target.ID
	if err := s.store.Remove(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.purgeUserMedia(id, false); err != nil {
		slog.Error("token deleted but media purge failed", "id", id, "error", err)
		httpError(w, http.StatusInternalServerError, "token deleted, but media purge failed")
		return
	}
	slog.Info("token deletion", "id", id, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// purgeUserMedia removes all uploaded files for a token from disk, the upload
// entries from the store, and the reverse index. It is the shared core behind
// token deletion, self-serve purge, and admin-initiated purge.
func (s *server) purgeUserMedia(tokenID string, clearStore bool) error {
	if !tokenIDRe.MatchString(tokenID) {
		return fmt.Errorf("invalid token ID")
	}
	userDir := filepath.Join(s.cfg.Dir, tokenID)
	// #nosec G703,G706 -- tokenID is strictly validated as an 8-character hex ID.
	if err := os.RemoveAll(userDir); err != nil {
		slog.Error("purge media dir error", "dir", internal.SanitizeLog(userDir), "error", err)
		return fmt.Errorf("remove media directory: %w", err)
	}
	var historyErr error
	if clearStore {
		historyErr = s.store.RemoveAllUploadEntries(tokenID)
	}
	if s.fileIndex != nil {
		s.fileIndex.RemoveAll(tokenID)
	}
	if historyErr != nil {
		slog.Error("purge upload history error", "id", tokenID, "error", historyErr)
		return fmt.Errorf("remove upload history: %w", historyErr)
	}
	_, _ = s.store.CancelPendingPurge(tokenID)
	slog.Info("purged all media", "id", tokenID)
	return nil
}

// handlePurgeMedia lets an authenticated user delete all their own uploads.
func (s *server) handlePurgeMedia(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		ConfirmPhrase string `json:"confirm_phrase"`
		Force         bool   `json:"force"`
	}
	if r.Header.Get("Content-Type") == "application/json" {
		_ = decodeJSON(w, r, &req, false)
	}
	expectedPhrase := "PURGE " + rec.ID
	if req.ConfirmPhrase != "" && req.ConfirmPhrase != expectedPhrase && req.ConfirmPhrase != "PURGE" && req.ConfirmPhrase != rec.ID {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid confirmation phrase (must be %q)", expectedPhrase))
		return
	}

	if _, exists := s.store.GetPendingPurge(rec.ID); exists && !req.Force {
		httpError(w, http.StatusConflict, "media purge already scheduled")
		return
	}

	if s.cfg.PurgeGracePeriod > 0 && !req.Force {
		p, err := s.store.SchedulePurge(rec.ID, rec.ID, s.cfg.PurgeGracePeriod)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "could not schedule media purge")
			return
		}
		slog.Info("media purge scheduled", "id", rec.ID, "actor_id", rec.ID, "purge_at", p.PurgeAt, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"scheduled":    true,
			"purged":       rec.ID,
			"purge_at":     p.PurgeAt.Format(time.RFC3339),
			"grace_period": s.cfg.PurgeGracePeriod.String(),
		})
		return
	}

	if err := s.purgeUserMedia(rec.ID, true); err != nil {
		httpError(w, http.StatusInternalServerError, "could not purge media")
		return
	}
	slog.Info("media purged", "id", rec.ID, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "purged": rec.ID})
}

// handleCancelPurgeMedia allows an authenticated user to cancel their own scheduled purge.
func (s *server) handleCancelPurgeMedia(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	cancelled, err := s.store.CancelPendingPurge(rec.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not cancel media purge")
		return
	}
	if !cancelled {
		httpError(w, http.StatusNotFound, "no scheduled purge found")
		return
	}
	slog.Info("media purge cancelled", "id", rec.ID, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": rec.ID})
}

// handleDeleteAccount lets an authenticated user delete all their uploads and
// remove their own token. Requires confirm_phrase or {"confirm":true} in the request body.
func (s *server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="upload"`)
		httpError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		ConfirmPhrase string `json:"confirm_phrase"`
		Confirm       bool   `json:"confirm"`
	}
	if err := decodeJSON(w, r, &req, false); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	expectedPhrase := "DELETE " + rec.ID
	if req.ConfirmPhrase != "" && req.ConfirmPhrase != expectedPhrase && req.ConfirmPhrase != "DELETE" && req.ConfirmPhrase != rec.ID {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid confirmation phrase (must be %q)", expectedPhrase))
		return
	}
	if !req.Confirm && req.ConfirmPhrase == "" {
		httpError(w, http.StatusBadRequest, fmt.Sprintf(`send {"confirm":true} or {"confirm_phrase":%q} to delete your account`, expectedPhrase))
		return
	}
	if err := s.store.Remove(rec.ID); err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.purgeUserMedia(rec.ID, false); err != nil {
		slog.Error("account deleted but media purge failed", "id", rec.ID, "error", err)
		httpError(w, http.StatusInternalServerError, "account deleted, but media purge failed")
		return
	}
	if s.sessions != nil {
		s.sessions.DeleteByTokenID(rec.ID)
	}
	slog.Info("account self-deleted", "id", internal.SanitizeLog(rec.ID), "label", internal.SanitizeLog(rec.Label), "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": rec.ID})
}

// handleAdminPurgeUserMedia lets an admin purge all media for a specific user
// without deleting the user's token.
func (s *server) handleAdminPurgeUserMedia(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	target, found := s.store.GetRecord(r.PathValue("id"))
	if !found {
		httpError(w, http.StatusNotFound, "token not found")
		return
	}
	id := target.ID
	var req struct {
		ConfirmPhrase string `json:"confirm_phrase"`
		Force         bool   `json:"force"`
	}
	if r.Header.Get("Content-Type") == "application/json" {
		_ = decodeJSON(w, r, &req, false)
	}
	expectedPhrase := "PURGE " + id
	if req.ConfirmPhrase != "" && req.ConfirmPhrase != expectedPhrase && req.ConfirmPhrase != "PURGE" && req.ConfirmPhrase != id {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid confirmation phrase (must be %q)", expectedPhrase))
		return
	}

	if _, exists := s.store.GetPendingPurge(id); exists && !req.Force {
		httpError(w, http.StatusConflict, "media purge already scheduled")
		return
	}

	if s.cfg.PurgeGracePeriod > 0 && !req.Force {
		p, err := s.store.SchedulePurge(id, rec.ID, s.cfg.PurgeGracePeriod)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "could not schedule media purge")
			return
		}
		slog.Info("media purge scheduled", "id", id, "actor_id", rec.ID, "purge_at", p.PurgeAt, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"scheduled":    true,
			"purged":       id,
			"purge_at":     p.PurgeAt.Format(time.RFC3339),
			"grace_period": s.cfg.PurgeGracePeriod.String(),
		})
		return
	}

	if err := s.purgeUserMedia(id, true); err != nil {
		httpError(w, http.StatusInternalServerError, "could not purge media")
		return
	}
	slog.Info("media purged", "id", id, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "purged": id})
}

// handleAdminCancelPurgeUserMedia lets an admin cancel a scheduled media purge for a specific user.
func (s *server) handleAdminCancelPurgeUserMedia(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	target, found := s.store.GetRecord(r.PathValue("id"))
	if !found {
		httpError(w, http.StatusNotFound, "token not found")
		return
	}
	id := target.ID
	cancelled, err := s.store.CancelPendingPurge(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not cancel media purge")
		return
	}
	if !cancelled {
		httpError(w, http.StatusNotFound, "no scheduled purge found")
		return
	}
	slog.Info("media purge cancelled", "id", id, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": id})
}

// handleSetLabelSSR handles renaming a token via form submit.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetLabelSSR(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	id := r.PathValue("id")
	if !internal.IsAdmin(userRec.Role) && userRec.ID != id {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	newLabel := r.FormValue("label")
	if err := s.store.SetLabel(id, newLabel); err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	slog.Info("token renamed", "id", id, "label", internal.SanitizeLog(newLabel), "actor_id", userRec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
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
	if err := decodeJSON(w, r, &req, false); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetLabel(id, req.Label); err != nil {
		writeStoreErr(w, err)
		return
	}
	slog.Info("token renamed", "id", id, "label", internal.SanitizeLog(req.Label), "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "label": req.Label})
}

// handleSetRole is the JSON API endpoint for changing a token's role.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireRoot(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &req, false); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	oldRec, _ := s.store.GetRecord(id)
	if err := s.store.SetRole(id, req.Role); err != nil {
		writeStoreErr(w, err)
		return
	}
	actionMsg := roleActionName(oldRec.Role, req.Role)
	slog.Info(actionMsg, "id", id, "old_role", oldRec.Role, "new_role", req.Role, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": req.Role})
}

// handleSetDisabled returns the enable/disable API handler for the given target
// state, sharing the lookup and error mapping between both routes.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetDisabled(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, ok := s.requireAdmin(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if err := s.store.SetDisabled(id, disabled); err != nil {
			writeStoreErr(w, err)
			return
		}
		slog.Info("token state updated", "id", id, "disabled", disabled, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "disabled": disabled})
	}
}

// handleSetLimits is the JSON API for setting a token's quotas. Caps are given
// as raw byte/count integers and "bypass" toggles exemption from the global
// rate limit.
// It requires the admin cookie and a valid CSRF token.
// default; an empty body clears every quota and the bypass flag.
func (s *server) handleSetLimits(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		internal.Limits
		Bypass bool `json:"bypass"`
	}
	err := decodeJSON(w, r, &req, true)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id := r.PathValue("id")
	if err := s.store.SetLimits(id, req.Limits, req.Bypass); err != nil {
		writeStoreErr(w, err)
		return
	}
	slog.Info("limits/invite updated",
		"id", id,
		"max_bytes", req.MaxBytes,
		"max_uploads", req.MaxUploads,
		"monthly_bytes", req.MonthlyBytes,
		"monthly_uploads", req.MonthlyUploads,
		"bypass", req.Bypass,
		"actor_id", rec.ID,
		"ip", clientIP(r, s.cfg.TrustProxyHeaders),
	)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSetGlobalLimits is the JSON API for the server-wide default quota.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleSetGlobalLimits(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var lim internal.Limits
	err := decodeJSON(w, r, &lim, true)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetGlobalLimits(lim); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	slog.Info("global quota updated",
		"max_bytes", lim.MaxBytes,
		"max_uploads", lim.MaxUploads,
		"monthly_bytes", lim.MonthlyBytes,
		"monthly_uploads", lim.MonthlyUploads,
		"actor_id", rec.ID,
		"ip", clientIP(r, s.cfg.TrustProxyHeaders),
	)
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) redirectWithError(w http.ResponseWriter, r *http.Request, msg string) {
	s.setCookie(w, r, flashErrorName, base64.URLEncoding.EncodeToString([]byte(msg)), 0)
	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) redirectWithNotice(w http.ResponseWriter, r *http.Request, msg string) {
	s.setCookie(w, r, flashNoticeName, base64.URLEncoding.EncodeToString([]byte(msg)), 0)
	ref := r.Header.Get("Referer")
	if isSafeRedirectTarget(ref, r.Host) {
		http.Redirect(w, r, ref, http.StatusSeeOther) // #nosec G710 -- Target URL is validated by isSafeRedirectTarget
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleUserUploads renders the per-token upload history page.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleUserUploads(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
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

	csrf := csrfForRequest(r)
	s.setCSRFCookie(w, r, csrf)

	totalUnfiltered := 0
	if query != "" {
		totalUnfiltered = totalAll
	}

	data := uploadsPageData{
		Token:           *rec,
		Uploads:         pageEntries,
		BaseURL:         s.cfg.BaseURL,
		StripExtension:  s.cfg.StripExtension,
		CSRF:            csrf,
		Page:            page,
		TotalPages:      totalPages,
		TotalFiles:      totalFiles,
		TotalUnfiltered: totalUnfiltered,
		PerPage:         perPage,
		PageStart:       start + 1,
		PageEnd:         end,
		Query:           query,
		IsAdmin:         internal.IsAdmin(userRec.Role),
		IsSelf:          userRec.ID == tokenID,
	}

	if p, ok := s.store.GetPendingPurge(tokenID); ok {
		data.PendingPurge = &p
	}

	if c, err := r.Cookie(flashErrorName); err == nil {
		s.clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}
	if c, err := r.Cookie(flashNoticeName); err == nil {
		s.clearCookie(w, r, flashNoticeName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Notice = string(decoded)
		}
	}

	renderTemplate(w, uploadsTmpl, data)
}

// handleAdminUsersPage renders the paginated and searchable users list.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminUsersPage(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
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

	csrf := csrfForRequest(r)
	s.setCSRFCookie(w, r, csrf)

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

	if purges, err := s.store.ListPendingPurges(); err == nil {
		pMap := make(map[string]*internal.PendingPurge, len(purges))
		for i := range purges {
			pMap[purges[i].TokenID] = &purges[i]
		}
		data.PendingPurges = pMap
	}

	// Read flash cookies
	if c, err := r.Cookie(flashSecretName); err == nil {
		s.clearCookie(w, r, flashSecretName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			var secretData newTokenSecret
			if json.Unmarshal(decoded, &secretData) == nil {
				data.Secret = &secretData
			}
		}
	}
	if c, err := r.Cookie(flashErrorName); err == nil {
		s.clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}
	if c, err := r.Cookie(flashNoticeName); err == nil {
		s.clearCookie(w, r, flashNoticeName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Notice = string(decoded)
		}
	}

	renderTemplate(w, usersTmpl, data)
}

// handleAdminUserProfilePage renders the user profile card view for a specific token ID.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAdminUserProfilePage(w http.ResponseWriter, r *http.Request) {
	userRec, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	if !internal.IsAdmin(userRec.Role) && userRec.ID != targetID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	targetRec, found := s.store.GetRecord(targetID)
	if !found {
		s.redirectWithError(w, r, "user not found")
		return
	}

	csrf := csrfForRequest(r)
	s.setCSRFCookie(w, r, csrf)

	data := userProfilePageData{
		LoggedIn:     true,
		IsAdmin:      internal.IsAdmin(userRec.Role),
		IsRoot:       userRec.Role == internal.RoleRoot,
		IsSelf:       userRec.ID == targetID,
		CurrentToken: &userRec,
		TargetToken:  targetRec,
		Global:       s.store.GlobalLimits(),
		CSRF:         csrf,
	}

	if p, ok := s.store.GetPendingPurge(targetID); ok {
		data.PendingPurge = &p
	}

	if c, err := r.Cookie(flashErrorName); err == nil {
		s.clearCookie(w, r, flashErrorName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Error = string(decoded)
		}
	}
	if c, err := r.Cookie(flashNoticeName); err == nil {
		s.clearCookie(w, r, flashNoticeName)
		if decoded, derr := base64.URLEncoding.DecodeString(c.Value); derr == nil {
			data.Notice = string(decoded)
		}
	}

	renderTemplate(w, userProfileTmpl, data)
}

// handleAPITokenUploads returns JSON upload history for token id.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleAPITokenUploads(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
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
	actor, ok := s.requireAdminCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	actorID := actor.ID
	countStr := r.FormValue("count")
	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		s.redirectWithError(w, r, "invite count must be a positive number")
		return
	}

	mode := r.FormValue("mode")
	pool, _ := strconv.Atoi(r.FormValue("pool"))
	maxCap, _ := strconv.Atoi(r.FormValue("max_cap"))

	var updated int
	if mode == "random" {
		if pool <= 0 {
			s.redirectWithError(w, r, "pool size must be positive for random mode")
			return
		}
		updated, err = s.store.AddInvitesToRandomUploaders(count, pool, maxCap)
	} else {
		updated, err = s.store.AddInvitesToAllUploadersCapped(count, maxCap)
	}

	if err != nil {
		s.redirectWithError(w, r, err.Error())
		return
	}
	slog.Info("giveaway invites executed", "count", count, "mode", mode, "updated_users", updated, "actor_id", actorID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	redirect(w, r)
}

// handleGiveawayAPI handles the JSON API request to add N invites to all upload tokens.
// It requires the admin cookie and a valid CSRF token.
func (s *server) handleGiveawayAPI(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Count  int    `json:"count"`
		Mode   string `json:"mode"`
		Pool   int    `json:"pool"`
		MaxCap int    `json:"max_cap"`
	}
	if err := decodeJSON(w, r, &req, false); err != nil || req.Count <= 0 {
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
	slog.Info("giveaway invites executed", "count", req.Count, "mode", req.Mode, "updated_users", updated, "actor_id", rec.ID, "ip", clientIP(r, s.cfg.TrustProxyHeaders))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":            true,
		"updated_users": updated,
		"added_invites": req.Count,
	})
}

// handleSetInvitePolicySSR handles setting the invite policy via form submission.
func (s *server) handleSetInvitePolicySSR(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdminCookie(w, r)
	if !ok {
		return
	}
	if !validateCSRF(r) {
		s.redirectWithError(w, r, "invalid request")
		return
	}

	actorID := actor.ID
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
		s.redirectWithError(w, r, err.Error())
		return
	}
	slog.Info("invite policy updated", "actor_id", actorID, "sched_enabled", pol.SchedEnabled, "sched_interval", pol.SchedInterval, "sched_count", pol.SchedCount, "newuser_enabled", pol.NewUserEnabled, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	redirect(w, r)
}

// handleSetInvitePolicyAPI handles setting the invite policy via JSON API.
func (s *server) handleSetInvitePolicyAPI(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var pol internal.InvitePolicy
	if err := decodeJSON(w, r, &pol, false); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.SetInvitePolicy(pol); err != nil {
		writeStoreErr(w, err)
		return
	}
	slog.Info("invite policy updated", "actor_id", rec.ID, "sched_enabled", pol.SchedEnabled, "sched_interval", pol.SchedInterval, "sched_count", pol.SchedCount, "newuser_enabled", pol.NewUserEnabled, "ip", clientIP(r, s.cfg.TrustProxyHeaders))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGetInvitePolicyAPI handles getting the invite policy via JSON API.
func (s *server) handleGetInvitePolicyAPI(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.store.InvitePolicy())
}
