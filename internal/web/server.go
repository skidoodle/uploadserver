package web

import (
	"io/fs"
	"log"
	"net/http"
	"strings"
	"uploadserver/internal"
)

// server bundles the resolved config with the shared token store.
type server struct {
	cfg   internal.Config
	store *internal.TokenStore
}

// routes defines the HTTP routes for the server.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /{$}", s.handleUpload)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	if s.cfg.ServeFiles {
		mux.HandleFunc("GET /", s.handleFileServer)
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

		mux.HandleFunc("GET /{$}", s.handleAdminPage)
		mux.HandleFunc("POST /login", s.handleAdminLogin)
		mux.HandleFunc("POST /logout", s.handleAdminLogout)
		mux.HandleFunc("POST /tokens/create", s.handleAdminCreateTokenSSR)
		mux.HandleFunc("POST /tokens/{id}/toggle", s.handleAdminToggleTokenSSR)
		mux.HandleFunc("POST /tokens/{id}/delete", s.handleAdminDeleteTokenSSR)
		mux.HandleFunc("POST /tokens/{id}/limits", s.handleAdminSetLimitsSSR)
		mux.HandleFunc("POST /global/limits", s.handleAdminSetGlobalLimitsSSR)
		mux.HandleFunc("GET /api/tokens", s.handleListTokens)
		mux.HandleFunc("POST /api/tokens", s.handleCreateToken)
		mux.HandleFunc("DELETE /api/tokens/{id}", s.handleDeleteToken)
		mux.HandleFunc("POST /api/tokens/{id}/disable", s.handleSetDisabled(true))
		mux.HandleFunc("POST /api/tokens/{id}/enable", s.handleSetDisabled(false))
		mux.HandleFunc("POST /api/tokens/{id}/limits", s.handleSetLimits)
		mux.HandleFunc("POST /api/global/limits", s.handleSetGlobalLimits)
	}
	return logging(secureHeaders(mux))
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
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	rec, ok := s.authenticate(r)
	if !ok || !internal.IsAdmin(rec.Role) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		httpError(w, http.StatusUnauthorized, "admin token required")
		return false
	}
	return true
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
		if rec, ok := s.store.Authenticate(c.Value); !ok || !internal.IsAdmin(rec.Role) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// announce logs startup info, surfacing a freshly minted bootstrap token once.
func (s *server) announce(secret string, created bool) {
	if created {
		log.Printf("(default root token: %s - saved to %s)", secret, s.cfg.StorePath)
	} else {
		log.Printf("loaded %d token(s) from %s", s.store.Count(), s.cfg.StorePath)
	}
	if s.cfg.BaseURL == "" {
		log.Print("warning: BASE_URL is not configured")
	}
	if s.cfg.ServeFiles {
		log.Printf("serving uploads at GET /")
	}
}

// handleHealthz checks the database store status and returns HTTP 200/500.
func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if err := s.store.Ping(); err != nil {
		log.Printf("health check failed: %v", err)
		http.Error(w, "database unhealthy", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
