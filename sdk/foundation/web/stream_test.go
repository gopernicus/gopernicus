package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamWriter_Send(t *testing.T) {
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)
	if sw == nil {
		t.Fatal("NewStreamWriter returned nil")
	}

	if err := sw.Send(SSEEvent{Event: "token", Data: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := sw.Send(SSEEvent{Data: "world"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: token\n") {
		t.Errorf("missing event type in body:\n%s", body)
	}
	if !strings.Contains(body, "data: hello\n\n") {
		t.Errorf("missing first data in body:\n%s", body)
	}
	if !strings.Contains(body, "data: world\n\n") {
		t.Errorf("missing second data in body:\n%s", body)
	}
}

func TestStreamWriter_SendJSON(t *testing.T) {
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)

	if err := sw.SendJSON("update", map[string]int{"progress": 50}); err != nil {
		t.Fatalf("SendJSON: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: update\n") {
		t.Errorf("missing event type in body:\n%s", body)
	}
	if !strings.Contains(body, `data: {"progress":50}`) {
		t.Errorf("missing JSON data in body:\n%s", body)
	}
}

func TestStreamWriter_SendData(t *testing.T) {
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)

	if err := sw.SendData("just data"); err != nil {
		t.Fatalf("SendData: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: just data\n\n") {
		t.Errorf("body = %q", body)
	}
	// A data-only event must not emit an event: line.
	if strings.Contains(body, "event:") {
		t.Errorf("unexpected event type in body:\n%s", body)
	}
}

func TestAcceptsStream(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"text/event-stream", true},
		{"application/json, text/event-stream", true},
		{"application/json", false},
		{"", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Accept", tt.accept)
		if got := AcceptsStream(r); got != tt.want {
			t.Errorf("AcceptsStream(%q) = %v, want %v", tt.accept, got, tt.want)
		}
	}
}

// TestStreamWriter_RespondOrUpgrade exercises the respond-or-upgrade decision:
// a streaming client gets an SSE upgrade via StreamWriter, everyone else gets a
// plain response and never triggers the lazy header write.
func TestStreamWriter_RespondOrUpgrade(t *testing.T) {
	handle := func(w http.ResponseWriter, r *http.Request) {
		if AcceptsStream(r) {
			sw := NewStreamWriter(w)
			_ = sw.SendJSON("result", map[string]string{"answer": "42"})
			return
		}
		RespondText(w, http.StatusOK, "42")
	}

	// Streaming client → SSE upgrade.
	streamW := httptest.NewRecorder()
	streamR := httptest.NewRequest("POST", "/", nil)
	streamR.Header.Set("Accept", "application/json, text/event-stream")
	handle(streamW, streamR)
	if ct := streamW.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("stream Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(streamW.Body.String(), `data: {"answer":"42"}`) {
		t.Errorf("missing streamed JSON:\n%s", streamW.Body.String())
	}

	// Non-streaming client → plain response.
	plainW := httptest.NewRecorder()
	plainR := httptest.NewRequest("POST", "/", nil)
	plainR.Header.Set("Accept", "application/json")
	handle(plainW, plainR)
	if plainW.Code != http.StatusOK {
		t.Errorf("plain status = %d, want 200", plainW.Code)
	}
	if ct := plainW.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("plain Content-Type = %q, want text/plain", ct)
	}
}
