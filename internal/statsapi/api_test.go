package statsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/ratelimit"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

func TestAPISummary(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "a.db")
	st, err := store.Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.WriteBatch(context.Background(), []record.Event{{
		TS: time.Now().UTC(), ClientIP: "1.1.1.1", Registry: "r",
		EventType: record.EventManifest, Repository: "a/b", Status: 200, Bytes: 10,
	}})

	api := &API{Store: st}
	mux := http.NewServeMux()
	api.Mount(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["range_pulls"].(float64) < 1 {
		t.Fatalf("%v", body)
	}
}

func TestLoginRateLimitAndBodyCap(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "a.db")
	st, err := store.Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.BootstrapAdmin(context.Background(), "admin", "password1"); err != nil {
		t.Fatal(err)
	}

	api := New(st)
	api.loginLim = ratelimit.New(config.RateLimitConfig{Enabled: true, PerIPRPS: 1, PerIPBurst: 1})
	mux := http.NewServeMux()
	api.Mount(mux)

	post := func(body string, addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	ok := post(`{"username":"admin","password":"password1"}`, "192.0.2.10:1")
	if ok.Code != 200 {
		t.Fatalf("first login %d %s", ok.Code, ok.Body.String())
	}
	blocked := post(`{"username":"admin","password":"password1"}`, "192.0.2.10:1")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", blocked.Code)
	}
	other := post(`{"username":"admin","password":"password1"}`, "192.0.2.11:1")
	if other.Code != 200 {
		t.Fatalf("other IP should not share the bucket: %d", other.Code)
	}

	huge := strings.Repeat("x", 4000)
	api2 := New(st)
	mux2 := http.NewServeMux()
	api2.Mount(mux2)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"`+huge+`","password":"password1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.20:1"
	rr := httptest.NewRecorder()
	mux2.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized body %d %s", rr.Code, rr.Body.String())
	}
}
