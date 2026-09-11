package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSSEStream_Heartbeat proves WithHeartbeat emits ": ping" comment frames on
// a live connection with no application events flowing.
func TestSSEStream_Heartbeat(t *testing.T) {
	events := make(chan SSEEvent) // never fed; only heartbeats flow
	srv := httptest.NewServer(NewSSEStream(events, WithHeartbeat(50*time.Millisecond)))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 256)
	deadline := time.Now().Add(2 * time.Second)
	var got strings.Builder
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if strings.Contains(got.String(), ": ping") {
			return
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("no ping within deadline; got %q", got.String())
}
