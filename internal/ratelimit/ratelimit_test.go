package ratelimit

import (
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func TestAllowDisabled(t *testing.T) {
	l := New(config.RateLimitConfig{Enabled: false, PerIPRPS: 1, PerIPBurst: 1})
	for i := 0; i < 20; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("disabled limiter rejected on iter %d", i)
		}
	}
}

func TestAllowEmptyKey(t *testing.T) {
	l := New(config.RateLimitConfig{Enabled: true, PerIPRPS: 1, PerIPBurst: 1})
	if !l.Allow("") {
		t.Fatal("empty key must be allowed so unattributed clients are not locked out")
	}
}

func TestAllowBurstThenReject(t *testing.T) {
	l := New(config.RateLimitConfig{Enabled: true, PerIPRPS: 1, PerIPBurst: 2})
	if !l.Allow("10.0.0.1") || !l.Allow("10.0.0.1") {
		t.Fatal("burst of 2 should allow the first two")
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("third request within the burst window should be rejected")
	}
	if !l.Allow("10.0.0.2") {
		t.Fatal("a different IP should have its own bucket")
	}
}

func TestAllowRefill(t *testing.T) {
	l := New(config.RateLimitConfig{Enabled: true, PerIPRPS: 100, PerIPBurst: 1})
	if !l.Allow("10.0.0.1") {
		t.Fatal("first request")
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("burst 1 should reject immediately")
	}
	time.Sleep(20 * time.Millisecond)
	if !l.Allow("10.0.0.1") {
		t.Fatal("expected refill after 20ms at 100 rps")
	}
}

func TestUpdateResetsBuckets(t *testing.T) {
	l := New(config.RateLimitConfig{Enabled: true, PerIPRPS: 1, PerIPBurst: 1})
	if !l.Allow("10.0.0.1") {
		t.Fatal("first")
	}
	l.Update(config.RateLimitConfig{Enabled: true, PerIPRPS: 1, PerIPBurst: 2})
	if !l.Allow("10.0.0.1") || !l.Allow("10.0.0.1") {
		t.Fatal("update should reset buckets and apply new burst")
	}
}
