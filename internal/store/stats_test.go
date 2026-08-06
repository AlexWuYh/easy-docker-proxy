package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "s.db")
	st, err := Open(config.StorageConfig{Driver: "sqlite", DSN: dsn, EventRetentionDays: 30, AggregateRetentionDays: 365})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSummaryAndTop(t *testing.T) {
	st := testStore(t)
	today := time.Now().UTC()
	day := today.Format("2006-01-02")
	events := []record.Event{
		{TS: today, ClientIP: "10.0.0.1", Registry: "dockerhub", EventType: record.EventManifest, Repository: "library/nginx", Reference: "latest", Status: 200, Bytes: 100},
		{TS: today, ClientIP: "10.0.0.1", Registry: "dockerhub", EventType: record.EventBlob, Repository: "library/nginx", Reference: "sha256:x", Status: 200, Bytes: 900},
		{TS: today, ClientIP: "10.0.0.2", Registry: "ghcr", EventType: record.EventManifest, Repository: "o/img", Reference: "v1", Status: 200, Bytes: 50},
		{TS: today, ClientIP: "10.0.0.2", Registry: "ghcr", EventType: record.EventManifest, Repository: "o/img", Reference: "v1", Status: 500, Bytes: 0, Error: "boom"},
	}
	if err := st.WriteBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}

	sum, err := st.Summary(context.Background(), "7d")
	if err != nil {
		t.Fatal(err)
	}
	if sum.TodayPulls != 2 {
		t.Fatalf("today pulls=%d", sum.TodayPulls)
	}
	if sum.RangeBytes != 1050 {
		t.Fatalf("bytes=%d", sum.RangeBytes)
	}
	if sum.ActiveClients != 2 {
		t.Fatalf("clients=%d", sum.ActiveClients)
	}
	if len(sum.ByRegistry) < 2 {
		t.Fatalf("by_registry=%+v", sum.ByRegistry)
	}
	if sum.ToDay != day {
		t.Fatalf("toDay=%s want %s", sum.ToDay, day)
	}

	ts, err := st.Timeseries(context.Background(), "7d")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 7 {
		t.Fatalf("timeseries len=%d", len(ts))
	}

	repos, err := st.TopRepos(context.Background(), "7d", 10)
	if err != nil || len(repos) < 1 {
		t.Fatalf("repos=%v err=%v", repos, err)
	}
	clients, err := st.TopClients(context.Background(), "7d", 10)
	if err != nil || len(clients) != 2 {
		t.Fatalf("clients=%v err=%v", clients, err)
	}
	errs, err := st.RecentErrors(context.Background(), 10)
	if err != nil || len(errs) < 1 {
		t.Fatalf("errors=%v err=%v", errs, err)
	}
	evs, err := st.RecentEvents(context.Background(), 10)
	if err != nil || len(evs) != 4 {
		t.Fatalf("events=%d err=%v", len(evs), err)
	}
}

func TestParseRange(t *testing.T) {
	from, to, n, err := ParseRange("7d")
	if err != nil || n != 7 || from == "" || to == "" {
		t.Fatalf("%s %s %d %v", from, to, n, err)
	}
	if _, _, _, err := ParseRange("nope"); err == nil {
		t.Fatal("expected error")
	}
}
