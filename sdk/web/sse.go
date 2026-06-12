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

// SSEStream streams Server-Sent Events from a channel.
type SSEStream struct {
	events    <-chan SSEEvent
	heartbeat time.Duration // 0 = no heartbeat frames
}

// SSEOption configures an SSEStream.
type SSEOption func(*SSEStream)

// WithHeartbeat emits an SSE comment frame (": ping") on the given interval
// so proxies and clients see a live connection between events. 0 disables.
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

// ServeHTTP streams events to the client until the channel closes or the
// client disconnects.
func (s *SSEStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

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
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case event, ok := <-s.events:
			if !ok {
				return
			}

			if err := writeSSEEvent(w, flusher, event); err != nil {
				return
			}
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event SSEEvent) error {
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

	flusher.Flush()
	return nil
}
