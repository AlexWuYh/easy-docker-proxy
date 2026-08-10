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

func TestQueryTokenRejected(t *testing.T) {
	t.Setenv("PROXY_ADMIN_TOKEN", "static-token-value")
	st := testStore(t)
	h := NewMux(&Handler{Proxy: testProxy(t), Store: st, Stats: &statsapi.API{Store: st}})
	// ?token= must not authenticate (log/Referer leak surface).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d&token=static-token-value", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("query token want 401 got %d", rr.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/-/config?token=static-token-value", nil)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("query token config want 401 got %d", rr2.Code)
	}
}

func loginToken(t *testing.T, h http.Handler, user, pass string) string {
	t.Helper()
	body := strings.NewReader(`{"username":"` + user + `","password":"` + pass + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login %d %s", rr.Code, rr.Body.String())
	}
	s := rr.Body.String()
	i := strings.Index(s, `"token":"`)
	if i < 0 {
		t.Fatalf("no token: %s", s)
	}
	rest := s[i+9:]
	j := strings.Index(rest, `"`)
	return rest[:j]
}

func TestConfigReloadAdminOnly(t *testing.T) {
	st := testStore(t)
	if _, err := st.BootstrapAdmin(context.Background(), "admin", "password1"); err != nil {
		t.Fatal(err)
	}
	// viewer account
	if _, err := st.CreateUser(context.Background(), "viewer1", "password1", store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	reloadCalls := 0
	h := NewMux(&Handler{
		Proxy: testProxy(t),
		Store: st,
		Stats: &statsapi.API{Store: st},
		ReloadFunc: func() error {
			reloadCalls++
			return nil
		},
	})

	viewerTok := loginToken(t, h, "viewer1", "password1")
	adminTok := loginToken(t, h, "admin", "password1")

	// viewer: stats OK, config/reload forbidden
	reqS := httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d", nil)
	reqS.Header.Set("Authorization", "Bearer "+viewerTok)
	rrS := httptest.NewRecorder()
	h.ServeHTTP(rrS, reqS)
	if rrS.Code != http.StatusOK {
		t.Fatalf("viewer summary %d", rrS.Code)
	}

	reqC := httptest.NewRequest(http.MethodGet, "/-/config", nil)
	reqC.Header.Set("Authorization", "Bearer "+viewerTok)
	rrC := httptest.NewRecorder()
	h.ServeHTTP(rrC, reqC)
	if rrC.Code != http.StatusForbidden {
		t.Fatalf("viewer config want 403 got %d %s", rrC.Code, rrC.Body.String())
	}

	reqR := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
	reqR.Header.Set("Authorization", "Bearer "+viewerTok)
	rrR := httptest.NewRecorder()
	h.ServeHTTP(rrR, reqR)
	if rrR.Code != http.StatusForbidden {
		t.Fatalf("viewer reload want 403 got %d", rrR.Code)
	}
	if reloadCalls != 0 {
		t.Fatalf("reload should not run for viewer")
	}

	// admin: config + reload OK
	reqCA := httptest.NewRequest(http.MethodGet, "/-/config", nil)
	reqCA.Header.Set("Authorization", "Bearer "+adminTok)
	rrCA := httptest.NewRecorder()
	h.ServeHTTP(rrCA, reqCA)
	if rrCA.Code != http.StatusOK {
		t.Fatalf("admin config %d %s", rrCA.Code, rrCA.Body.String())
	}

	reqRA := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
	reqRA.Header.Set("Authorization", "Bearer "+adminTok)
	rrRA := httptest.NewRecorder()
	h.ServeHTTP(rrRA, reqRA)
	if rrRA.Code != http.StatusOK {
		t.Fatalf("admin reload %d %s", rrRA.Code, rrRA.Body.String())
	}
	if reloadCalls != 1 {
		t.Fatalf("reloadCalls=%d", reloadCalls)
	}

	// ops token still works as admin
	t.Setenv("PROXY_ADMIN_TOKEN", "ops-static-token")
	// rebuild mux so AdminToken() sees env (config reads env at request time)
	h2 := NewMux(&Handler{
		Proxy: testProxy(t),
		Store: st,
		Stats: &statsapi.API{Store: st},
		ReloadFunc: func() error { return nil },
	})
	reqOps := httptest.NewRequest(http.MethodGet, "/-/config", nil)
	reqOps.Header.Set("Authorization", "Bearer ops-static-token")
	rrOps := httptest.NewRecorder()
	h2.ServeHTTP(rrOps, reqOps)
	if rrOps.Code != http.StatusOK {
		t.Fatalf("ops token config %d %s", rrOps.Code, rrOps.Body.String())
	}
}
