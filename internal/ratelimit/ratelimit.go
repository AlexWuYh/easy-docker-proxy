// Package ratelimit provides per-IP token-bucket rate limiting for the data plane.
package ratelimit

import (
	"sync"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

// Limiter is a concurrent per-key token bucket.
type Limiter struct {
	enabled bool
	rps     float64
	burst   float64

	mu   sync.Mutex
	buckets map[string]*bucket
	// lastCleanup avoids scanning every request.
	lastCleanup time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a Limiter from config. When disabled, Allow always returns true.
func New(cfg config.RateLimitConfig) *Limiter {
	rps := cfg.PerIPRPS
	if rps <= 0 {
		rps = 20
	}
	burst := float64(cfg.PerIPBurst)
	if burst <= 0 {
		burst = 40
	}
	return &Limiter{
		enabled:     cfg.Enabled,
		rps:         rps,
		burst:       burst,
		buckets:     make(map[string]*bucket),
		lastCleanup: time.Now(),
	}
}

// Allow reports whether key may proceed. Empty key is allowed when enabled
// (cannot attribute rate) to avoid accidental lockout.
func (l *Limiter) Allow(key string) bool {
	if l == nil || !l.enabled {
		return true
	}
	if key == "" {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		l.maybeCleanupLocked(now)
		return true
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Reset clears all buckets (e.g. on config reload with new limits).
func (l *Limiter) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.buckets = make(map[string]*bucket)
	l.mu.Unlock()
}

// Update replaces rate parameters without dropping the limiter pointer.
func (l *Limiter) Update(cfg config.RateLimitConfig) {
	if l == nil {
		return
	}
	rps := cfg.PerIPRPS
	if rps <= 0 {
		rps = 20
	}
	burst := float64(cfg.PerIPBurst)
	if burst <= 0 {
		burst = 40
	}
	l.mu.Lock()
	l.enabled = cfg.Enabled
	l.rps = rps
	l.burst = burst
	l.buckets = make(map[string]*bucket)
	l.mu.Unlock()
}

func (l *Limiter) maybeCleanupLocked(now time.Time) {
	if now.Sub(l.lastCleanup) < 5*time.Minute {
		return
	}
	l.lastCleanup = now
	// Drop idle buckets older than 10 minutes.
	cutoff := now.Add(-10 * time.Minute)
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
