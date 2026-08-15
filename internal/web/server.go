package web

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"uploadserver/internal"
)

// server bundles the resolved config with the shared token store.
type server struct {
	cfg          internal.Config
	store        *internal.TokenStore
	fileIndex    *internal.FileIndex
	sessions     *sessionStore
	loginLimiter *ipRateLimiter
}

// routes defines the HTTP routes for the server.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /{$}", s.handleUpload)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	if s.cfg.ServeFiles {
		mux.HandleFunc("GET /", s.handleFileServer)
		mux.HandleFunc("DELETE /", s.handleDeleteFile)
	}

	if s.cfg.AdminEnabled {
		sub, err := fs.Sub(staticFS, "static")
		if err != nil {
			panic("static sub-fs: " + err.Error())
		}
		assets := http.FileServer(http.FS(sub))
		gatedAssets := s.gateAdminAsset(assets)
		mux.Handle("GET /_/login.css", http.StripPrefix("/_/", assets))
		mux.Handle("GET /_/login.js", http.StripPrefix("/_/", assets))
		mux.Handle("GET /_/admin.css", http.StripPrefix("/_/", gatedAssets))
		mux.Handle("GET /_/admin.js", http.StripPrefix("/_/", gatedAssets))
		mux.Handle("GET /_/uploads.css", http.StripPrefix("/_/", gatedAssets))
		mux.Handle("GET /_/uploads.js", http.StripPrefix("/_/", gatedAssets))

		mux.HandleFunc("GET /{$}", s.handleAdminPage)
		mux.HandleFunc("POST /_/login", s.handleAdminLogin)
		mux.HandleFunc("POST /_/logout", s.handleAdminLogout)
		mux.HandleFunc("POST /_/tokens/create", s.handleAdminCreateTokenSSR)
		mux.HandleFunc("POST /_/tokens/invite", s.handleInviteTokenSSR)
		mux.HandleFunc("POST /_/tokens/giveaway", s.handleGiveawaySSR)
		mux.HandleFunc("POST /_/tokens/{id}/toggle", s.handleAdminToggleTokenSSR)
		mux.HandleFunc("POST /_/tokens/{id}/delete", s.handleAdminDeleteTokenSSR)
		mux.HandleFunc("POST /_/tokens/{id}/purge-media", s.handlePurgeUserMediaSSR)
		mux.HandleFunc("POST /_/tokens/{id}/cancel-purge", s.handleCancelPurgeUserMediaSSR)
		mux.HandleFunc("POST /_/tokens/{id}/limits", s.handleAdminSetLimitsSSR)
		mux.HandleFunc("POST /_/tokens/{id}/label", s.handleSetLabelSSR)
		mux.HandleFunc("POST /_/files/delete", s.handleDeleteFileSSR)
		mux.HandleFunc("POST /_/tokens/{id}/role", s.handleAdminSetRoleSSR)
		mux.HandleFunc("POST /_/global/limits", s.handleAdminSetGlobalLimitsSSR)
		mux.HandleFunc("POST /_/invite-policy", s.handleSetInvitePolicySSR)
		mux.HandleFunc("GET /_/api/tokens", s.handleListTokens)
		mux.HandleFunc("POST /_/api/tokens", s.handleCreateToken)
		mux.HandleFunc("POST /_/api/tokens/giveaway", s.handleGiveawayAPI)
		mux.HandleFunc("DELETE /_/api/tokens/{id}", s.handleDeleteToken)
		mux.HandleFunc("DELETE /_/api/purge", s.handlePurgeMedia)
		mux.HandleFunc("POST /_/api/purge/cancel", s.handleCancelPurgeMedia)
		mux.HandleFunc("DELETE /_/api/account", s.handleDeleteAccount)
		mux.HandleFunc("DELETE /_/api/tokens/{id}/media", s.handleAdminPurgeUserMedia)
		mux.HandleFunc("POST /_/api/tokens/{id}/cancel-purge", s.handleAdminCancelPurgeUserMedia)
		mux.HandleFunc("POST /_/api/tokens/{id}/disable", s.handleSetDisabled(true))
		mux.HandleFunc("POST /_/api/tokens/{id}/enable", s.handleSetDisabled(false))
		mux.HandleFunc("POST /_/api/tokens/{id}/limits", s.handleSetLimits)
		mux.HandleFunc("POST /_/api/tokens/{id}/label", s.handleSetLabel)
		mux.HandleFunc("POST /_/api/tokens/{id}/role", s.handleSetRole)
		mux.HandleFunc("POST /_/api/global/limits", s.handleSetGlobalLimits)
		mux.HandleFunc("GET /_/api/tokens/{id}/uploads", s.handleAPITokenUploads)
		mux.HandleFunc("GET /_/api/invite-policy", s.handleGetInvitePolicyAPI)
		mux.HandleFunc("POST /_/api/invite-policy", s.handleSetInvitePolicyAPI)
		mux.HandleFunc("GET /_/uploads/{id}", s.handleUserUploads)
		mux.HandleFunc("GET /_/users", s.handleAdminUsersPage)
		mux.HandleFunc("GET /_/user/{id}", s.handleAdminUserProfilePage)
	}
	return s.logging(s.requestDeadline(s.secureHeaders(limitFormBodies(mux))))
}

// authenticate resolves the bearer token on the request to a record, if valid.
func (s *server) authenticate(r *http.Request) (internal.TokenRecord, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return internal.TokenRecord{}, false
	}
	return s.store.Authenticate(strings.TrimPrefix(h, prefix))
}

// requireAdmin enforces an admin-role token, writing a 401 if absent.
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (internal.TokenRecord, bool) {
	rec, ok := s.authenticate(r)
	if !ok || !internal.IsAdmin(rec.Role) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		httpError(w, http.StatusUnauthorized, "admin token required")
		return internal.TokenRecord{}, false
	}
	return rec, true
}

// requireRoot enforces a root-role token, writing a 401 if absent or unauthorized.
func (s *server) requireRoot(w http.ResponseWriter, r *http.Request) (internal.TokenRecord, bool) {
	rec, ok := s.authenticate(r)
	if !ok || rec.Role != internal.RoleRoot {
		w.Header().Set("WWW-Authenticate", `Bearer realm="root"`)
		httpError(w, http.StatusUnauthorized, "root token required")
		return internal.TokenRecord{}, false
	}
	return rec, true
}

// validateSessionCookie resolves an admin/user session cookie to a valid TokenRecord.
func (s *server) validateSessionCookie(c *http.Cookie) (internal.TokenRecord, bool) {
	if c == nil || c.Value == "" {
		return internal.TokenRecord{}, false
	}
	if s.sessions != nil {
		if sess, ok := s.sessions.Get(c.Value); ok {
			rec, found := s.store.GetRecord(sess.tokenID)
			if found && !rec.Disabled {
				return rec, true
			}
		}
	}
	return s.store.Authenticate(c.Value)
}

// gateAdminAsset serves a static asset only to a request carrying a valid admin
// session cookie. Everyone else gets a 404, so the dashboard's CSS and JS aren't
// discoverable without logging in.
func (s *server) gateAdminAsset(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminCookieName)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if _, ok := s.validateSessionCookie(c); !ok {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// announce logs startup info, surfacing a freshly minted bootstrap token once.
func (s *server) announce(secret string, created bool) {
	if created {
		slog.Info(fmt.Sprintf("(default root token: %s - saved to %s)", secret, s.cfg.StorePath))
	} else {
		slog.Info(fmt.Sprintf("loaded %d token(s) from %s", s.store.Count(), s.cfg.StorePath))
	}
	if s.cfg.BaseURL == "" {
		slog.Warn("warning: BASE_URL is not configured")
	}
	if s.cfg.ServeFiles {
		slog.Info("serving uploads at GET /")
	}
}

// handleHealthz checks the database store status and returns HTTP 200/500.
func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if err := s.store.Ping(); err != nil {
		slog.Error("health check failed", "error", err)
		http.Error(w, "database unhealthy", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
