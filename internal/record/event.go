// Package record defines pull events and the async ingest queue.
// Writers must never block the proxy hot path. See .ai/01_DESIGN.md §5.
package record

import "time"

// EventType classifies a registry request for analytics.
type EventType string

const (
	EventManifest  EventType = "manifest"
	EventBlob      EventType = "blob"
	EventTags      EventType = "tags"
	EventReferrers EventType = "referrers"
)

// Event is a single registry request summary (no layer / manifest payload).
type Event struct {
	TS         time.Time
	ClientIP   string
	Registry   string
	Host       string
	EventType  EventType
	Repository string
	Reference  string // tag or digest
	Method     string
	Status     int
	Bytes      int64
	DurationMS int64
	UserAgent  string
	Error      string
}

// IsPull reports whether this event counts as a successful image pull (manifest 2xx).
func (e Event) IsPull() bool {
	return e.EventType == EventManifest && e.Status >= 200 && e.Status < 300
}

// IsError reports status>=400 or explicit error text with no success status.
func (e Event) IsError() bool {
	if e.Status >= 400 {
		return true
	}
	return e.Error != "" && (e.Status == 0 || e.Status >= 500)
}
