package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	// Event is the event type (optional). It must not contain CR or LF.
	Event string

	// Data is the event payload. Strings and bytes are sent as text with line
	// endings normalized to LF; other values are JSON encoded.
	Data any

	// ID is the event ID (optional). It must not contain CR, LF, or NUL.
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

// SSEOption configures an SSEStream at construction.
type SSEOption func(*sseConfig)

type sseConfig struct {
	heartbeat time.Duration
}

// WithHeartbeat emits an SSE comment frame (": ping") on the given interval so
// proxies and clients see a live connection between events. 0 disables.
func WithHeartbeat(d time.Duration) SSEOption {
	return func(c *sseConfig) { c.heartbeat = d }
}

// NewSSEStream creates an SSE stream that reads events from the channel.
// A nil option panics.
func NewSSEStream(events <-chan SSEEvent, opts ...SSEOption) *SSEStream {
	var cfg sseConfig
	for _, opt := range opts {
		if opt == nil {
			panic("web.NewSSEStream: nil option")
		}
		opt(&cfg)
	}
	return &SSEStream{events: events, heartbeat: cfg.heartbeat}
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
	// the first post-deadline frame. Extend it per write, bounded by the request
	// deadline so blocked writes cannot outlive the stream lifetime. The call
	// falls back silently when the connection doesn't support deadlines (some
	// test recorders).
	extendDeadline := func() {
		deadline := time.Now().Add(s.writeWindow())
		if requestDeadline, ok := r.Context().Deadline(); ok && requestDeadline.Before(deadline) {
			deadline = requestDeadline
		}
		_ = rc.SetWriteDeadline(deadline)
	}
	extendDeadline()

	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		RecordError(w, err)
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
			if err := writeSSEFrame(w, rc, ": ping\n\n"); err != nil {
				RecordError(w, err)
				return
			}

		case event, ok := <-s.events:
			if !ok {
				return
			}
			extendDeadline()
			if err := writeSSEEvent(w, rc, event); err != nil {
				RecordError(w, err)
				return
			}
		}
	}
}

// writeSSEEvent serializes one event to the SSE wire format and flushes it.
func writeSSEEvent(w http.ResponseWriter, rc *http.ResponseController, event SSEEvent) error {
	if strings.ContainsAny(event.Event, "\r\n") {
		return fmt.Errorf("SSE event type must not contain CR or LF")
	}
	if strings.ContainsAny(event.ID, "\r\n\x00") {
		return fmt.Errorf("SSE event ID must not contain CR, LF, or NUL")
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

	var frame strings.Builder
	if event.Event != "" {
		fmt.Fprintf(&frame, "event: %s\n", event.Event)
	}
	if event.ID != "" {
		fmt.Fprintf(&frame, "id: %s\n", event.ID)
	}
	if event.Retry > 0 {
		fmt.Fprintf(&frame, "retry: %d\n", event.Retry)
	}
	dataStr = strings.ReplaceAll(dataStr, "\r\n", "\n")
	dataStr = strings.ReplaceAll(dataStr, "\r", "\n")
	for _, line := range strings.Split(dataStr, "\n") {
		fmt.Fprintf(&frame, "data: %s\n", line)
	}
	frame.WriteByte('\n')
	return writeSSEFrame(w, rc, frame.String())
}

func writeSSEFrame(w http.ResponseWriter, rc *http.ResponseController, frame string) error {
	n, err := io.WriteString(w, frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return io.ErrShortWrite
	}
	return rc.Flush()
}
