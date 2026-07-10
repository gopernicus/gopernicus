package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/logging"
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

// TestSSEStream_SurvivesWriteTimeout is the named acceptance criterion (plan
// risk 4): the per-write http.ResponseController SetWriteDeadline extension
// must keep frames flowing on a stream that outlives a short server
// WriteTimeout. The stream runs ~400ms against a 150ms WriteTimeout; without
// the extension, writes after the deadline fail and the final event never
// arrives.
func TestSSEStream_SurvivesWriteTimeout(t *testing.T) {
	const (
		writeTimeout = 150 * time.Millisecond
		interval     = 80 * time.Millisecond
		total        = 5
	)

	events := make(chan SSEEvent)
	go func() {
		defer close(events)
		for i := 0; i < total; i++ {
			time.Sleep(interval)
			events <- SSEEvent{Data: fmt.Sprintf("event-%d", i)}
		}
	}()

	srv := httptest.NewUnstartedServer(NewSSEStream(events))
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// The final event is written at ~400ms, well past the 150ms WriteTimeout.
	last := fmt.Sprintf("data: event-%d\n\n", total-1)
	if !strings.Contains(string(body), last) {
		t.Fatalf("stream died before %q — the per-write SetWriteDeadline extension was lost; got:\n%s", last, body)
	}
}

// TestSSEStream_ThroughLoggerMiddleware proves StatusRecorder.Unwrap lets
// http.ResponseController reach the underlying Flusher through the Logger
// middleware: without Unwrap, the first Flush fails and ServeHTTP returns a 500
// instead of the event stream.
func TestSSEStream_ThroughLoggerMiddleware(t *testing.T) {
	events := make(chan SSEEvent, 2)
	events <- SSEEvent{Data: "hello"}
	events <- SSEEvent{Event: "update", Data: "world"}
	close(events)

	handler := Logger(logging.NewNoop())(NewSSEStream(events))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream (Flush blocked through middleware?)", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "data: hello\n\n") {
		t.Errorf("missing first event through middleware:\n%s", body)
	}
	if !strings.Contains(string(body), "event: update\n") {
		t.Errorf("missing typed event through middleware:\n%s", body)
	}
}
