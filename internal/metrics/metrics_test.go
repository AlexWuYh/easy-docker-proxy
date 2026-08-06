package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerOutput(t *testing.T) {
	c := New()
	c.IncRequest("dockerhub", "ok")
	c.AddBytes(100)
	c.SetEventStats(3, 1)

	rr := httptest.NewRecorder()
	c.Handler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"easy_docker_proxy_requests_total",
		"dockerhub",
		"easy_docker_proxy_bytes_total 100",
		"easy_docker_proxy_events_written_total 3",
		"easy_docker_proxy_events_dropped_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}
