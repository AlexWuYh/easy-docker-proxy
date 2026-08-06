// Package admin serves the management API on admin_listen (token required).
// Endpoints: /-/config, /-/reload, /healthz, stats, optional /metrics. See .ai/01_DESIGN.md.
package admin

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/proxy"
	"github.com/alex_wuyh/easy-docker-proxy/internal/statsapi"
	"github.com/alex_wuyh/easy-docker-proxy/internal/web"
)

// Handler serves admin endpoints. Fail-closed when admin token is unset.
type Handler struct {
	Proxy      *proxy.Proxy
	ConfigPath string
	// ReloadFunc reloads config from disk into the proxy. Optional; default uses config.Load.
	ReloadFunc func() error
	// Stats is optional read-only analytics API (M3).
	Stats *statsapi.API
	// MetricsHandler is optional Prometheus text handler (M4); still token-gated.
	MetricsHandler http.Handler
}

// NewMux builds the admin HTTP mux with authentication middleware.
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
	// Stats static UI (same handler for /stats and /stats/*)
	statsUI := web.Handler()
	mux.Handle("/stats", statsUI)
	mux.Handle("/stats/", statsUI)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Liveness probes are public.
		if r.URL.Path == "/healthz" || r.URL.Path == "/-/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		if !h.authorize(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	cfg := h.Proxy.Config()
	token := config.AdminToken(cfg)
	if token == "" {
		// Fail-closed: no token configured → refuse admin surface.
		http.Error(w, "admin token not configured", http.StatusServiceUnavailable)
		return false
	}
	got := bearerOrHeader(r)
	if got == "" || !secureEqual(got, token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
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

// constant-time-ish compare for tokens (length leak ok for admin token).
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
	cfg := h.Proxy.Config()
	writeJSON(w, http.StatusOK, config.MaskedCopy(cfg))
}

func (h *Handler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var err error
	if h.ReloadFunc != nil {
		err = h.ReloadFunc()
	} else {
		err = h.defaultReload()
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

func (h *Handler) defaultReload() error {
	cfg, err := config.Load(h.ConfigPath)
	if err != nil {
		return err
	}
	h.Proxy.Reload(cfg)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
