package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func hybridConfig() *config.Config {
	en := true
	cfg := &config.Config{
		Server:   config.ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		Default:  "dockerhub",
		LogLevel: "quiet",
		Registries: []config.RegistryConfig{
			{
				Name:         "dockerhub",
				Hosts:        []string{"reg.example.com"},
				PathPrefixes: []string{"docker.io"},
				Upstream:     "https://registry-1.docker.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
			{
				Name:         "ghcr",
				PathPrefixes: []string{"ghcr.io"},
				Upstream:     "https://ghcr.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
			{
				Name:         "quay",
				PathPrefixes: []string{"quay.io"},
				Upstream:     "https://quay.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
		},
	}
	off := false
	cfg.UpstreamAllowlist.Enabled = &off
	config.Normalize(cfg)
	cfg.UpstreamAllowlist.Enabled = &off
	return cfg
}

func TestStripRepoPathPrefix(t *testing.T) {
	cases := []struct {
		path, prefix, want string
	}{
		{"/v2/ghcr.io/owner/app/manifests/v1", "ghcr.io", "/v2/owner/app/manifests/v1"},
		{"/v2/docker.io/library/nginx/manifests/latest", "docker.io", "/v2/library/nginx/manifests/latest"},
		{"/v2/library/nginx/manifests/latest", "", "/v2/library/nginx/manifests/latest"},
		{"/v2/library/nginx/manifests/latest", "ghcr.io", "/v2/library/nginx/manifests/latest"},
		{"/v2/ghcr.io/owner/app/blobs/sha256:abc", "ghcr.io", "/v2/owner/app/blobs/sha256:abc"},
	}
	for _, c := range cases {
		got := stripRepoPathPrefix(c.path, c.prefix)
		if got != c.want {
			t.Fatalf("strip(%q,%q)=%q want %q", c.path, c.prefix, got, c.want)
		}
	}
}

func TestResolveRoutePathPrefix(t *testing.T) {
	p := New(hybridConfig())

	// No prefix → default dockerhub
	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/library/nginx/manifests/latest", nil)
	req.Host = "reg.example.com"
	hit := p.resolveRoute(req)
	if hit.reg == nil || hit.reg.Name != "dockerhub" || hit.stripPrefix != "" {
		t.Fatalf("no prefix: %+v reg=%v", hit, hit.reg)
	}

	// ghcr.io prefix
	req2 := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/ghcr.io/owner/app/manifests/tag", nil)
	req2.Host = "reg.example.com"
	hit2 := p.resolveRoute(req2)
	if hit2.reg == nil || hit2.reg.Name != "ghcr" || hit2.stripPrefix != "ghcr.io" {
		t.Fatalf("ghcr: %+v", hit2)
	}

	// docker.io prefix → dockerhub + strip
	req3 := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/docker.io/library/nginx/manifests/latest", nil)
	req3.Host = "reg.example.com"
	hit3 := p.resolveRoute(req3)
	if hit3.reg == nil || hit3.reg.Name != "dockerhub" || hit3.stripPrefix != "docker.io" {
		t.Fatalf("docker.io prefix: %+v", hit3)
	}
}

func TestPathPrefixUpstreamRewrite(t *testing.T) {
	var seenPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"schemaVersion":2}`)
	}))
	defer up.Close()

	en := true
	cfg := hybridConfig()
	for i := range cfg.Registries {
		if cfg.Registries[i].Name == "ghcr" {
			cfg.Registries[i].Upstream = up.URL
			cfg.Registries[i].Auth = config.AuthConfig{Type: config.AuthAnonymous}
			cfg.Registries[i].Enabled = &en
		}
	}
	cfg.AllowInsecureUpstream = true
	p := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/ghcr.io/owner/app/manifests/v1", nil)
	req.Host = "reg.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if seenPath != "/v2/owner/app/manifests/v1" {
		t.Fatalf("upstream path %q want /v2/owner/app/manifests/v1", seenPath)
	}
}

func TestDockerIOPrefixUpstreamRewrite(t *testing.T) {
	var seenPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer up.Close()

	cfg := hybridConfig()
	for i := range cfg.Registries {
		if cfg.Registries[i].Name == "dockerhub" {
			cfg.Registries[i].Upstream = up.URL
		}
	}
	cfg.AllowInsecureUpstream = true
	p := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/docker.io/library/nginx/manifests/latest", nil)
	req.Host = "reg.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if seenPath != "/v2/library/nginx/manifests/latest" {
		t.Fatalf("path %q", seenPath)
	}
	if !strings.Contains(rr.Body.String(), "{") {
		t.Fatalf("body %q", rr.Body.String())
	}
}
