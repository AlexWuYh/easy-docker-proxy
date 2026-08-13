package store

import (
	"context"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
)

func TestListImagesBytesIncludeBlobsAndSkipHEAD(t *testing.T) {
	st := testStore(t)
	now := time.Now().UTC()
	err := st.WriteBatch(context.Background(), []record.Event{
		{TS: now, ClientIP: "10.0.0.1", Registry: "dockerhub", EventType: record.EventManifest, Repository: "library/nginx", Reference: "latest", Status: 200, Bytes: 100, Method: "GET"},
		{TS: now, ClientIP: "10.0.0.1", Registry: "dockerhub", EventType: record.EventManifest, Repository: "library/nginx", Reference: "latest", Status: 200, Bytes: 10, Method: "HEAD"},
		{TS: now, ClientIP: "10.0.0.1", Registry: "dockerhub", EventType: record.EventBlob, Repository: "library/nginx", Reference: "sha256:x", Status: 200, Bytes: 900, Method: "GET"},
	})
	if err != nil {
		t.Fatal(err)
	}

	items, total, err := st.ListImages(context.Background(), "", "", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("total=%d items=%d", total, len(items))
	}
	im := items[0]
	if im.Pulls != 1 {
		t.Fatalf("pulls=%d want 1 (HEAD must not count)", im.Pulls)
	}
	if im.BytesTotal != 1010 {
		t.Fatalf("bytes=%d want 1010 (manifest+HEAD+blob)", im.BytesTotal)
	}

	tags, err := st.ListImageTags(context.Background(), "dockerhub", "library/nginx")
	if err != nil {
		t.Fatal(err)
	}
	var gotLatest bool
	for _, tg := range tags {
		if tg.Reference == "latest" {
			gotLatest = true
			if tg.Pulls != 1 {
				t.Fatalf("tag pulls=%d want 1", tg.Pulls)
			}
		}
	}
	if !gotLatest {
		t.Fatalf("tags %+v", tags)
	}
}

func TestWriteBatchHEADNotCountedAsPull(t *testing.T) {
	st := testStore(t)
	now := time.Now().UTC()
	if err := st.WriteBatch(context.Background(), []record.Event{
		{TS: now, ClientIP: "10.0.0.1", Registry: "r", EventType: record.EventManifest, Repository: "a/b", Status: 200, Method: "HEAD"},
		{TS: now, ClientIP: "10.0.0.1", Registry: "r", EventType: record.EventManifest, Repository: "a/b", Status: 200, Method: "GET"},
	}); err != nil {
		t.Fatal(err)
	}
	sum, err := st.Summary(context.Background(), "1d")
	if err != nil {
		t.Fatal(err)
	}
	if sum.TodayPulls != 1 {
		t.Fatalf("today pulls=%d want 1", sum.TodayPulls)
	}
}
