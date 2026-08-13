package upstream

import (
	"context"
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func TestParseBearerChallenge(t *testing.T) {
	h := `Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull"`
	realm, service, scope := ParseBearerChallenge(h)
	if realm != "https://auth.docker.io/token" {
		t.Fatalf("realm %q", realm)
	}
	if service != "registry.docker.io" {
		t.Fatalf("service %q", service)
	}
	if scope != "repository:library/nginx:pull" {
		t.Fatalf("scope %q", scope)
	}
}

func TestParseBearerChallengeNonBearer(t *testing.T) {
	realm, _, _ := ParseBearerChallenge(`Basic realm="x"`)
	if realm != "" {
		t.Fatal("expected empty")
	}
}

func TestGetTokenRejectsDisallowedRealm(t *testing.T) {
	on := true
	cfg := &config.Config{UpstreamAllowlist: config.UpstreamAllowlist{Enabled: &on}}
	reg := &config.RegistryConfig{Name: "ghcr", Upstream: "https://ghcr.io", Auth: config.AuthConfig{Type: config.AuthToken}}
	_, err := NewCache().GetToken(context.Background(), nil, "https://169.254.169.254/latest/meta-data/", "ghcr.io", "repository:x:pull", reg, cfg)
	if err == nil {
		t.Fatal("expected metadata realm rejected")
	}
}
