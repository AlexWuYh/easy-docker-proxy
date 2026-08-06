// Package record defines pull events and the async ingest queue.
// Writers must never block the proxy hot path. See .ai/01_DESIGN.md §5.
package record

// Event is a single registry request summary (no layer payload).
type Event struct {
	// Fields filled in M2.
}
