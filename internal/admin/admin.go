// Package admin serves the management API on admin_listen.
// Auth: web session token (login) or PROXY_ADMIN_TOKEN for ops/metrics.
package admin

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/proxy"
	"github.com/alex_wuyh/easy-docker-proxy/internal/statsapi"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
	"github.com/alex_wuyh/easy-docker-proxy/internal/web"
)

// Handler serves admin endpoints.
type Handler struct {
	Proxy      *proxy.Proxy
	ConfigPath string
	ReloadFunc func() error
	Stats      *statsapi.API
	Store      *store.Store
	// MetricsHandler is optional Prometheus text handler; session or admin token gated.
	MetricsHandler http.Handler
}

// NewMux builds the admin HTTP mux.
func NewMux(h *Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/-/healthz", h.handleHealthz)
	mux.HandleFunc("/-/config", h.handleConfig)
	mux.HandleFunc("/-/reload", h.handleReload)
	if h.Stats != nil {
		h.Stats.Mount(mux)
	}
	if h.MetricsHandler != nil {
		mux.Handle("/metrics", h.MetricsHandler)
	}
	statsUI := web.Handler()
	mux.Handle("/stats", statsUI)
	mux.Handle("/stats/", statsUI)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Public probes
		if path == "/healthz" || path == "/-/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		// Public login API
		if path == "/api/v1/auth/login" {
			mux.ServeHTTP(w, r)
			return
		}
		// Static UI is public: browsers cannot send sessionStorage Bearer on navigation.
		// Auth is enforced client-side (requireAuth) + on all /api/v1/* below.
		if path == "/stats" || path == "/stats/" || strings.HasPrefix(path, "/stats/") {
			mux.ServeHTTP(w, r)
			return
		}

		// Authenticate API / admin / metrics: session user or static admin token
		user, ok := h.authenticate(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if user != nil {
			r = statsapi.WithUser(r, user)
		}
		mux.ServeHTTP(w, r)
	})
}

// authenticate returns (user, true) for session; (nil, true) for admin token; (nil, false) if unauthorized.
func (h *Handler) authenticate(r *http.Request) (*store.User, bool) {
	tok := bearerOrHeader(r)
	if tok == "" {
		return nil, false
	}
	// Prefer session lookup
	if h.Store != nil {
		if u, err := h.Store.SessionUser(r.Context(), tok); err == nil && u != nil {
			return u, true
		}
	}
	// Fallback: static PROXY_ADMIN_TOKEN (ops / metrics)
	cfg := h.Proxy.Config()
	adminTok := config.AdminToken(cfg)
	if adminTok != "" && secureEqual(tok, adminTok) {
		return &store.User{ID: 0, Username: "token-admin", Role: store.RoleAdmin}, true
	}
	return nil, false
}

func bearerOrHeader(r *http.Request) string {
	if ah := r.Header.Get("Authorization"); len(ah) >= 7 && strings.EqualFold(ah[:7], "Bearer ") {
		return strings.TrimSpace(ah[7:])
	}
	if ah := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(ah), "bearer ") {
		return strings.TrimSpace(ah[7:])
	}
	if t := r.Header.Get("X-Admin-Token"); t != "" {
		return t
	}
	return r.URL.Query().Get("token")
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, config.MaskedCopy(h.Proxy.Config()))
}

func (h *Handler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	var err error
	if h.ReloadFunc != nil {
		err = h.ReloadFunc()
	} else {
		cfg, e := config.Load(h.ConfigPath)
		if e != nil {
			err = e
		} else {
			h.Proxy.Reload(cfg)
		}
	}
	if err != nil {
		log.Printf("[admin] reload failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "reload failed: "+err.Error())
		return
	}
	n := 0
	if cfg := h.Proxy.Config(); cfg != nil {
		n = len(cfg.Registries)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "registries": n})
}

// requireAdmin allows only RoleAdmin (session admin or PROXY_ADMIN_TOKEN).
// Viewer sessions may read stats APIs but not config/reload.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u := statsapi.UserFromContext(r)
	if u == nil || u.Role != store.RoleAdmin {
		writeJSONError(w, http.StatusForbidden, "admin only")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
