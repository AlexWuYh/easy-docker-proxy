package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/proxy"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
	"github.com/alex_wuyh/easy-docker-proxy/internal/statsapi"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

func testProxy(t *testing.T) *proxy.Proxy {
	t.Helper()
	en := true
	cfg := &config.Config{
		Server:   config.ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel: "quiet",
		Default:  "x",
		Admin:    config.AdminConfig{TokenEnv: "PROXY_ADMIN_TOKEN"},
		Registries: []config.RegistryConfig{{
			Name: "x", Hosts: []string{"x.example.com"},
			Upstream: "https://example.com", Auth: config.AuthConfig{Type: config.AuthAnonymous}, Enabled: &en,
		}},
	}
	config.Normalize(cfg)
	return proxy.New(cfg)
}

func TestHealthzOpen(t *testing.T) {
	os.Unsetenv("PROXY_ADMIN_TOKEN")
	h := NewMux(&Handler{Proxy: testProxy(t)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestConfigFailClosedWithoutToken(t *testing.T) {
	os.Unsetenv("PROXY_ADMIN_TOKEN")
	h := NewMux(&Handler{Proxy: testProxy(t)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/-/config", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestConfigRequiresAuth(t *testing.T) {
	t.Setenv("PROXY_ADMIN_TOKEN", "secret-token-value")
	h := NewMux(&Handler{Proxy: testProxy(t)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/-/config", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/-/config", nil)
	req.Header.Set("Authorization", "Bearer secret-token-value")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr2.Code, rr2.Body.String())
	}
}

func TestStatsUIAndAPIAuth(t *testing.T) {
	t.Setenv("PROXY_ADMIN_TOKEN", "stats-token")
	dsn := "file:" + filepath.Join(t.TempDir(), "st.db")
	st, err := store.Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.WriteBatch(context.Background(), []record.Event{{
		TS: time.Now().UTC(), ClientIP: "9.9.9.9", Registry: "r",
		EventType: record.EventManifest, Repository: "n/m", Status: 200, Bytes: 1,
	}}); err != nil {
		t.Fatal(err)
	}

	h := NewMux(&Handler{
		Proxy: testProxy(t),
		Stats: &statsapi.API{Store: st},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stats/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("stats without auth: %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/stats/?token=stats-token", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("stats with token: %d body %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "easy-docker-proxy") {
		t.Fatalf("unexpected html: %s", rr2.Body.String()[:min(200, rr2.Body.Len())])
	}

	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d", nil)
	req3.Header.Set("Authorization", "Bearer stats-token")
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("api: %d %s", rr3.Code, rr3.Body.String())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
