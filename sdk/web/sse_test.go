package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSEStream_BasicEvents(t *testing.T) {
	events := make(chan SSEEvent, 3)
	events <- SSEEvent{Data: "hello"}
	events <- SSEEvent{Event: "update", Data: "world"}
	events <- SSEEvent{Data: "bye", ID: "3", Retry: 5000}
	close(events)

	stream := NewSSEStream(events)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/events", nil)
	stream.ServeHTTP(w, r)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	body := w.Body.String()

	if !strings.Contains(body, "data: hello\n\n") {
		t.Errorf("missing 'data: hello' event in body:\n%s", body)
	}
	if !strings.Contains(body, "event: update\n") {
		t.Errorf("missing 'event: update' in body:\n%s", body)
	}
	if !strings.Contains(body, "id: 3\n") {
		t.Errorf("missing 'id: 3' in body:\n%s", body)
	}
	if !strings.Contains(body, "retry: 5000\n") {
		t.Errorf("missing 'retry: 5000' in body:\n%s", body)
	}
}

func TestSSEStream_JSONData(t *testing.T) {
	events := make(chan SSEEvent, 1)
	events <- SSEEvent{Data: map[string]string{"key": "value"}}
	close(events)

	stream := NewSSEStream(events)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/events", nil)
	stream.ServeHTTP(w, r)

	if !strings.Contains(w.Body.String(), `data: {"key":"value"}`) {
		t.Errorf("expected JSON data in body:\n%s", w.Body.String())
	}
}

func TestSSEStream_ContextCancellation(t *testing.T) {
	events := make(chan SSEEvent) // unbuffered, will block

	ctx, cancel := context.WithCancel(context.Background())

	stream := NewSSEStream(events)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		stream.ServeHTTP(w, r)
		close(done)
	}()

	// Cancel the context — stream should exit.
	cancel()
	<-done

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
