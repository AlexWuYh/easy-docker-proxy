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
	off := false
	cfg.UpstreamAllowlist.Enabled = &off
	config.Normalize(cfg)
	cfg.UpstreamAllowlist.Enabled = &off
	return proxy.New(cfg)
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "a.db")
	st, err := store.Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestHealthzOpen(t *testing.T) {
	os.Unsetenv("PROXY_ADMIN_TOKEN")
	h := NewMux(&Handler{Proxy: testProxy(t), Store: testStore(t)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestSessionAndLogin(t *testing.T) {
	st := testStore(t)
	if _, err := st.BootstrapAdmin(context.Background(), "admin", "password1"); err != nil {
		t.Fatal(err)
	}
	h := NewMux(&Handler{
		Proxy: testProxy(t),
		Store: st,
		Stats: &statsapi.API{Store: st},
	})

	// static pages are public (client-side auth); index HTML without token still 200
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stats/login.html", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("login page %d", rr.Code)
	}
	rrIdx := httptest.NewRecorder()
	h.ServeHTTP(rrIdx, httptest.NewRequest(http.MethodGet, "/stats/index.html", nil))
	// 200 OK, or 301 from FileServer path cleaning — must not be auth 401/302 login loop
	if rrIdx.Code == http.StatusUnauthorized || rrIdx.Code == http.StatusFound {
		t.Fatalf("index page must not require server auth, got %d loc=%s", rrIdx.Code, rrIdx.Header().Get("Location"))
	}
	if rrIdx.Code != http.StatusOK && rrIdx.Code != http.StatusMovedPermanently {
		t.Fatalf("index page unexpected status %d", rrIdx.Code)
	}

	// login
	body := strings.NewReader(`{"username":"admin","password":"password1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Fatalf("login %d %s", rr2.Code, rr2.Body.String())
	}
	// parse token crudely
	s := rr2.Body.String()
	if !strings.Contains(s, `"token"`) {
		t.Fatalf("no token: %s", s)
	}
	// extract token between "token":" and "
	i := strings.Index(s, `"token":"`)
	rest := s[i+9:]
	j := strings.Index(rest, `"`)
	token := rest[:j]

	// summary with session
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("summary %d %s", rr3.Code, rr3.Body.String())
	}

	// seed image event
	_ = st.WriteBatch(context.Background(), []record.Event{{
		TS: time.Now().UTC(), ClientIP: "1.1.1.1", Registry: "dockerhub",
		EventType: record.EventManifest, Repository: "library/nginx", Reference: "alpine",
		Status: 200, Bytes: 10,
	}})
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/images", nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK || !strings.Contains(rr4.Body.String(), "library/nginx") {
		t.Fatalf("images %d %s", rr4.Code, rr4.Body.String())
	}
}

func TestAdminTokenFallback(t *testing.T) {
	t.Setenv("PROXY_ADMIN_TOKEN", "static-token-value")
	st := testStore(t)
	h := NewMux(&Handler{Proxy: testProxy(t), Store: st, Stats: &statsapi.API{Store: st}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d", nil)
	req.Header.Set("Authorization", "Bearer static-token-value")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
}
