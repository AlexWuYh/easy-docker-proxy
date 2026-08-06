// Package statsapi exposes read-only analytics HTTP APIs for the Stats UI.
// All routes require admin token by default. See .ai/01_DESIGN.md §6.
package statsapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

// API serves /api/v1/* stats endpoints.
type API struct {
	Store *store.Store
}

// Mount registers routes on mux.
func (a *API) Mount(mux *http.ServeMux) {
	if a == nil || a.Store == nil {
		return
	}
	mux.HandleFunc("/api/v1/summary", a.handleSummary)
	mux.HandleFunc("/api/v1/timeseries", a.handleTimeseries)
	mux.HandleFunc("/api/v1/top/repos", a.handleTopRepos)
	mux.HandleFunc("/api/v1/top/clients", a.handleTopClients)
	mux.HandleFunc("/api/v1/errors", a.handleErrors)
	mux.HandleFunc("/api/v1/events", a.handleEvents)
}

func (a *API) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "7d"
	}
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
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "7d"
	}
	// metric query is accepted for API compatibility; response includes all metrics.
	pts, err := a.Store.Timeseries(r.Context(), rangeStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":  rangeStr,
		"metric": r.URL.Query().Get("metric"),
		"points": pts,
	})
}

func (a *API) handleTopRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "7d"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.TopRepos(r.Context(), rangeStr, limit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rangeStr, "items": rows})
}

func (a *API) handleTopClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "7d"
	}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
