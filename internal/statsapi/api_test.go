package statsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

func TestAPISummary(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "a.db")
	st, err := store.Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.WriteBatch(context.Background(), []record.Event{{
		TS: time.Now().UTC(), ClientIP: "1.1.1.1", Registry: "r",
		EventType: record.EventManifest, Repository: "a/b", Status: 200, Bytes: 10,
	}})

	api := &API{Store: st}
	mux := http.NewServeMux()
	api.Mount(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/summary?range=7d", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["range_pulls"].(float64) < 1 {
		t.Fatalf("%v", body)
	}
}
