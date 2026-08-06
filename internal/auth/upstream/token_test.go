package upstream

import "testing"

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
