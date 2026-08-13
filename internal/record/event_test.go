package record

import "testing"

func TestIsPull(t *testing.T) {
	ok := Event{EventType: EventManifest, Status: 200, Method: "GET"}
	if !ok.IsPull() {
		t.Fatal("GET 2xx manifest")
	}
	legacy := Event{EventType: EventManifest, Status: 200}
	if !legacy.IsPull() {
		t.Fatal("empty method is treated as GET for older rows")
	}
	head := Event{EventType: EventManifest, Status: 200, Method: "HEAD"}
	if head.IsPull() {
		t.Fatal("HEAD must not count as a pull")
	}
	blob := Event{EventType: EventBlob, Status: 200, Method: "GET"}
	if blob.IsPull() {
		t.Fatal("blob is not a pull")
	}
	fail := Event{EventType: EventManifest, Status: 404, Method: "GET"}
	if fail.IsPull() {
		t.Fatal("4xx is not a pull")
	}
}
