// Package metrics optionally exposes Prometheus text-format counters (no extra deps).
package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Collector holds process metrics for the proxy.
type Collector struct {
	// requests by registry and outcome class: ok|error|denied|ratelimit|other
	reqMu    sync.Mutex
	requests map[string]*atomic.Uint64 // key: registry|class

	bytesTotal atomic.Uint64
	// events
	eventsWritten atomic.Uint64
	eventsDropped atomic.Uint64
}

// New creates an empty collector.
func New() *Collector {
	return &Collector{
		requests: make(map[string]*atomic.Uint64),
	}
}

func (c *Collector) key(registry, class string) string {
	if registry == "" {
		registry = "unknown"
	}
	if class == "" {
		class = "other"
	}
	return registry + "|" + class
}

// IncRequest increments request counters.
func (c *Collector) IncRequest(registry, class string) {
	if c == nil {
		return
	}
	k := c.key(registry, class)
	c.reqMu.Lock()
	ctr, ok := c.requests[k]
	if !ok {
		ctr = &atomic.Uint64{}
		c.requests[k] = ctr
	}
	c.reqMu.Unlock()
	ctr.Add(1)
}

// AddBytes adds transferred response bytes.
func (c *Collector) AddBytes(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.bytesTotal.Add(uint64(n))
}

// SetEventStats updates absolute counters from the record queue (call on scrape or periodically).
func (c *Collector) SetEventStats(written, dropped uint64) {
	if c == nil {
		return
	}
	c.eventsWritten.Store(written)
	c.eventsDropped.Store(dropped)
}

// IncEventsWritten adds to written counter.
func (c *Collector) IncEventsWritten(n uint64) {
	if c == nil {
		return
	}
	c.eventsWritten.Add(n)
}

// IncEventsDropped adds to dropped counter.
func (c *Collector) IncEventsDropped(n uint64) {
	if c == nil {
		return
	}
	c.eventsDropped.Add(n)
}

// Handler returns an HTTP handler that scrapes Prometheus text format.
// Optional snapshotFn is called before render (e.g. to sync queue stats).
func (c *Collector) Handler(snapshotFn func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if snapshotFn != nil {
			snapshotFn()
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder
		b.WriteString("# HELP easy_docker_proxy_requests_total Registry proxy requests by registry and class.\n")
		b.WriteString("# TYPE easy_docker_proxy_requests_total counter\n")
		c.reqMu.Lock()
		for k, ctr := range c.requests {
			parts := strings.SplitN(k, "|", 2)
			reg, class := "unknown", "other"
			if len(parts) == 2 {
				reg, class = parts[0], parts[1]
			}
			fmt.Fprintf(&b, "easy_docker_proxy_requests_total{registry=%q,class=%q} %d\n",
				reg, class, ctr.Load())
		}
		c.reqMu.Unlock()

		b.WriteString("# HELP easy_docker_proxy_bytes_total Response body bytes streamed to clients.\n")
		b.WriteString("# TYPE easy_docker_proxy_bytes_total counter\n")
		fmt.Fprintf(&b, "easy_docker_proxy_bytes_total %d\n", c.bytesTotal.Load())

		b.WriteString("# HELP easy_docker_proxy_events_written_total Pull events successfully written to storage.\n")
		b.WriteString("# TYPE easy_docker_proxy_events_written_total counter\n")
		fmt.Fprintf(&b, "easy_docker_proxy_events_written_total %d\n", c.eventsWritten.Load())

		b.WriteString("# HELP easy_docker_proxy_events_dropped_total Pull events dropped because the async queue was full.\n")
		b.WriteString("# TYPE easy_docker_proxy_events_dropped_total counter\n")
		fmt.Fprintf(&b, "easy_docker_proxy_events_dropped_total %d\n", c.eventsDropped.Load())

		_, _ = w.Write([]byte(b.String()))
	})
}

// ClassFromStatus maps HTTP status to a metric class.
func ClassFromStatus(status int) string {
	switch {
	case status >= 200 && status < 400:
		return "ok"
	case status == http.StatusForbidden:
		return "denied"
	case status == http.StatusTooManyRequests:
		return "ratelimit"
	case status >= 400:
		return "error"
	default:
		return "other"
	}
}
