package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	// Event is the event type (optional). Maps to the "event:" field.
	Event string

	// Data is the event payload. Maps to the "data:" field.
	Data any

	// ID is the event ID (optional). Maps to the "id:" field.
	ID string

	// Retry suggests a reconnection time in milliseconds (optional).
	Retry int
}

// SSEStream streams Server-Sent Events from a channel until the channel closes
// or the client disconnects.
type SSEStream struct {
	events    <-chan SSEEvent
	heartbeat time.Duration // 0 = no heartbeat frames
}

// SSEOption configures an SSEStream.
type SSEOption func(*SSEStream)

// WithHeartbeat emits an SSE comment frame (": ping") on the given interval so
// proxies and clients see a live connection between events. 0 disables.
func WithHeartbeat(d time.Duration) SSEOption {
	return func(s *SSEStream) { s.heartbeat = d }
}

// NewSSEStream creates an SSE stream that reads events from the channel.
func NewSSEStream(events <-chan SSEEvent, opts ...SSEOption) *SSEStream {
	s := &SSEStream{events: events}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// writeWindow is the per-write deadline extension: generous relative to the
// heartbeat so a healthy stream never trips it, finite so a dead client's
// connection is reclaimed.
func (s *SSEStream) writeWindow() time.Duration {
	if s.heartbeat > 0 {
		return s.heartbeat * 4
	}
	return 2 * time.Minute
}

// ServeHTTP streams events to the client until the channel closes or the
// request context is cancelled.
func (s *SSEStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// ResponseController reaches Flush through middleware wrappers that
	// implement Unwrap — a bare type assertion fails under any wrapping
	// middleware (the request logger's StatusRecorder, etc.).
	rc := http.NewResponseController(w)

	// Long-lived streams outlive the server's WriteTimeout: the deadline arms
	// at request start and every write after it fails, killing the stream at
	// the first post-deadline frame. Extend it per write instead; the call
	// falls back silently when the connection doesn't support deadlines (some
	// test recorders).
	extendDeadline := func() {
		_ = rc.SetWriteDeadline(time.Now().Add(s.writeWindow()))
	}
	extendDeadline()

	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	var heartbeat <-chan time.Time
	if s.heartbeat > 0 {
		ticker := time.NewTicker(s.heartbeat)
		defer ticker.Stop()
		heartbeat = ticker.C
	}

	for {
		select {
		case <-r.Context().Done():
			return

		case <-heartbeat:
			extendDeadline()
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}

		case event, ok := <-s.events:
			if !ok {
				return
			}
			extendDeadline()
			if err := writeSSEEvent(w, rc, event); err != nil {
				return
			}
		}
	}
}

// writeSSEEvent serializes one event to the SSE wire format and flushes it.
func writeSSEEvent(w http.ResponseWriter, rc *http.ResponseController, event SSEEvent) error {
	if event.Event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event.Event); err != nil {
			return err
		}
	}

	if event.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}

	if event.Retry > 0 {
		if _, err := fmt.Fprintf(w, "retry: %d\n", event.Retry); err != nil {
			return err
		}
	}

	var dataStr string
	switch v := event.Data.(type) {
	case string:
		dataStr = v
	case []byte:
		dataStr = string(v)
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return err
		}
		dataStr = string(jsonBytes)
	}

	if _, err := fmt.Fprintf(w, "data: %s\n\n", dataStr); err != nil {
		return err
	}

	return rc.Flush()
}
