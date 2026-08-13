// Package statsapi exposes analytics, images, and auth HTTP APIs for the Stats UI.
package statsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/ratelimit"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

const loginBodyLimit = 2048

// API serves /api/v1/* endpoints.
type API struct {
	Store *store.Store
	// loginLim rate-limits POST /api/v1/auth/login by RemoteAddr (not XFF).
	loginLim *ratelimit.Limiter
}

// New builds an API with a default login limiter.
func New(st *store.Store) *API {
	return &API{
		Store: st,
		loginLim: ratelimit.New(config.RateLimitConfig{
			Enabled:    true,
			PerIPRPS:   1,
			PerIPBurst: 8,
		}),
	}
}

type ctxKey int

const userCtxKey ctxKey = 1

// WithUser attaches an authenticated user to the request context.
func WithUser(r *http.Request, u *store.User) *http.Request {
	ctx := context.WithValue(r.Context(), userCtxKey, u)
	return r.WithContext(ctx)
}

func userFromContext(r *http.Request) *store.User {
	u, _ := r.Context().Value(userCtxKey).(*store.User)
	return u
}

// UserFromContext returns the authenticated web user, if any.
func UserFromContext(r *http.Request) *store.User {
	return userFromContext(r)
}

// Mount registers routes on mux.
func (a *API) Mount(mux *http.ServeMux) {
	if a == nil || a.Store == nil {
		return
	}
	mux.HandleFunc("/api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("/api/v1/auth/logout", a.handleLogout)
	mux.HandleFunc("/api/v1/auth/me", a.handleMe)
	mux.HandleFunc("/api/v1/users", a.handleUsers)
	mux.HandleFunc("/api/v1/users/", a.handleUserByID)
	mux.HandleFunc("/api/v1/pull-users", a.handlePullUsers)
	mux.HandleFunc("/api/v1/pull-users/", a.handlePullUserByID)
	mux.HandleFunc("/api/v1/summary", a.handleSummary)
	mux.HandleFunc("/api/v1/timeseries", a.handleTimeseries)
	mux.HandleFunc("/api/v1/top/repos", a.handleTopRepos)
	mux.HandleFunc("/api/v1/top/clients", a.handleTopClients)
	mux.HandleFunc("/api/v1/errors", a.handleErrors)
	mux.HandleFunc("/api/v1/events", a.handleEvents)
	mux.HandleFunc("/api/v1/images", a.handleImages)
	mux.HandleFunc("/api/v1/images/tags", a.handleImageTags)
	mux.HandleFunc("/api/v1/images/timeseries", a.handleImageTimeseries)
}

func bearerToken(r *http.Request) string {
	if ah := r.Header.Get("Authorization"); len(ah) >= 7 && strings.EqualFold(ah[:7], "Bearer ") {
		return strings.TrimSpace(ah[7:])
	}
	return ""
}

func loginPeerIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a != nil && a.loginLim != nil && !a.loginLim.Allow(loginPeerIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyLimit)
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sess, err := a.Store.Authenticate(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCreds) {
			writeErr(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if tok := bearerToken(r); tok != "" {
		_ = a.Store.RevokeSession(r.Context(), tok)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	u := userFromContext(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *API) handleUsers(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	if u == nil || u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := a.Store.ListUsers(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
	case http.MethodPost:
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		created, err := a.Store.CreateUser(r.Context(), body.Username, body.Password, body.Role)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrUserExists) {
				status = http.StatusConflict
			}
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *API) handleUserByID(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if len(parts) == 2 && parts[1] == "password" && r.Method == http.MethodPut {
		if u.Role != store.RoleAdmin && u.ID != id {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := a.Store.SetPassword(r.Context(), id, body.Password); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if u.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		if err := a.Store.DeleteUser(r.Context(), id); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrLastAdmin) {
				status = http.StatusConflict
			}
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	writeErr(w, http.StatusNotFound, "not found")
}

func (a *API) handlePullUsers(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	if u == nil || u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := a.Store.ListPullUsers(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
	case http.MethodPost:
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Enabled  *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		en := true
		if body.Enabled != nil {
			en = *body.Enabled
		}
		created, err := a.Store.CreatePullUser(r.Context(), body.Username, body.Password, en)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrPullUserExists) {
				status = http.StatusConflict
			}
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *API) handlePullUserByID(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	if u == nil || u.Role != store.RoleAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/pull-users/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if len(parts) == 2 && parts[1] == "password" && r.Method == http.MethodPut {
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := a.Store.SetPullUserPassword(r.Context(), id, body.Password); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if len(parts) == 2 && parts[1] == "enabled" && r.Method == http.MethodPut {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := a.Store.SetPullUserEnabled(r.Context(), id, body.Enabled); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := a.Store.DeletePullUser(r.Context(), id); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	writeErr(w, http.StatusNotFound, "not found")
}

func (a *API) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := orDefault(r.URL.Query().Get("range"), "7d")
	sum, err := a.Store.Summary(r.Context(), rangeStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (a *API) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := orDefault(r.URL.Query().Get("range"), "7d")
	registry := strings.TrimSpace(r.URL.Query().Get("registry"))
	pts, err := a.Store.Timeseries(r.Context(), rangeStr, registry)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rangeStr, "registry": registry, "points": pts})
}

func (a *API) handleTopRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := orDefault(r.URL.Query().Get("range"), "7d")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	registry := strings.TrimSpace(r.URL.Query().Get("registry"))
	rows, err := a.Store.TopRepos(r.Context(), rangeStr, limit, registry)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rangeStr, "registry": registry, "items": rows})
}

func (a *API) handleTopClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := orDefault(r.URL.Query().Get("range"), "7d")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.TopClients(r.Context(), rangeStr, limit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rangeStr, "items": rows})
}

func (a *API) handleErrors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.RecentErrors(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.RecentEvents(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (a *API) handleImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	q := r.URL.Query().Get("q")
	registry := strings.TrimSpace(r.URL.Query().Get("registry"))
	items, total, err := a.Store.ListImages(r.Context(), q, registry, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	regs, _ := a.Store.ListImageRegistries(r.Context())
	if regs == nil {
		regs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total,
		"registry": registry, "registries": regs,
	})
}

func (a *API) handleImageTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	reg := r.URL.Query().Get("registry")
	repo := r.URL.Query().Get("repository")
	items, err := a.Store.ListImageTags(r.Context(), reg, repo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registry": reg, "repository": repo, "items": items})
}

func (a *API) handleImageTimeseries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	reg := r.URL.Query().Get("registry")
	repo := r.URL.Query().Get("repository")
	rangeStr := orDefault(r.URL.Query().Get("range"), "7d")
	pts, err := a.Store.ImageAnalytics(r.Context(), reg, repo, rangeStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rangeStr, "points": pts})
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
