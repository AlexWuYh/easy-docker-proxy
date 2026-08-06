package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
)

func TestWriteBatchAndAggregate(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "t.db") + "?_pragma=busy_timeout(5000)"
	st, err := Open(config.StorageConfig{
		Driver:                 "sqlite",
		DSN:                    dsn,
		EventRetentionDays:     30,
		AggregateRetentionDays: 365,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	day := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	events := []record.Event{
		{
			TS: day, ClientIP: "10.0.0.1", Registry: "dockerhub", Host: "hub.example.com",
			EventType: record.EventManifest, Repository: "library/nginx", Reference: "alpine",
			Method: "GET", Status: 200, Bytes: 1200, DurationMS: 10,
		},
		{
			TS: day, ClientIP: "10.0.0.1", Registry: "dockerhub", Host: "hub.example.com",
			EventType: record.EventBlob, Repository: "library/nginx", Reference: "sha256:abc",
			Method: "GET", Status: 200, Bytes: 5000, DurationMS: 40,
		},
		{
			TS: day, ClientIP: "10.0.0.2", Registry: "dockerhub", Host: "hub.example.com",
			EventType: record.EventManifest, Repository: "library/nginx", Reference: "alpine",
			Method: "GET", Status: 404, Bytes: 0, DurationMS: 5,
		},
	}
	if err := st.WriteBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}

	n, err := st.CountEvents(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("count=%d err=%v", n, err)
	}

	stats, err := st.QueryDaily(context.Background(), "2026-08-06", "dockerhub")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats rows=%d", len(stats))
	}
	s := stats[0]
	if s.Pulls != 1 {
		t.Fatalf("pulls=%d want 1 (only successful manifests)", s.Pulls)
	}
	if s.BytesTotal != 6200 {
		t.Fatalf("bytes=%d", s.BytesTotal)
	}
	if s.Errors != 1 {
		t.Fatalf("errors=%d", s.Errors)
	}
}

func TestPurgeExpired(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "t.db")
	st, err := Open(config.StorageConfig{
		Driver: "sqlite", DSN: dsn,
		EventRetentionDays: 1, AggregateRetentionDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	old := time.Now().UTC().AddDate(0, 0, -10)
	if err := st.WriteBatch(context.Background(), []record.Event{{
		TS: old, ClientIP: "1.1.1.1", Registry: "r", EventType: record.EventManifest,
		Repository: "a/b", Status: 200, Bytes: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	ed, _, _, err := st.PurgeExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ed < 1 {
		t.Fatalf("expected event purge, got %d", ed)
	}
	n, _ := st.CountEvents(context.Background())
	if n != 0 {
		t.Fatalf("remaining %d", n)
	}
}
