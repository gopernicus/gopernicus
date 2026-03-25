package web

import (
	"net/http"
	"strings"
)

// StreamWriter provides a way to incrementally write SSE events to an HTTP
// response. Unlike SSEStream (which reads from a channel), StreamWriter gives
// the handler direct control over when and what to send.
//
// This enables the "respond with JSON or upgrade to a stream" pattern used
// by AI/LLM APIs, progress endpoints, and protocols like MCP Streamable HTTP.
//
// Usage:
//
//	handler.POST("/generate", func(w http.ResponseWriter, r *http.Request) {
//	    if needsStreaming {
//	        sw := web.NewStreamWriter(w)
//	        sw.SendJSON("token", map[string]string{"text": "hello"})
//	        sw.SendJSON("token", map[string]string{"text": " world"})
//	        return
//	    }
//	    web.RespondJSON(w, http.StatusOK, fullResponse)
//	})
type StreamWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	started bool
}

// NewStreamWriter creates a StreamWriter. Returns nil if the ResponseWriter
// does not support flushing (required for streaming).
func NewStreamWriter(w http.ResponseWriter) *StreamWriter {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	return &StreamWriter{
		w:       w,
		flusher: flusher,
	}
}

// Send writes a single SSE event to the stream. On the first call it sets
// the SSE headers and flushes them to the client.
func (sw *StreamWriter) Send(event SSEEvent) error {
	if !sw.started {
		sw.w.Header().Set("Content-Type", "text/event-stream")
		sw.w.Header().Set("Cache-Control", "no-cache")
		sw.w.Header().Set("Connection", "keep-alive")
		sw.w.Header().Set("X-Accel-Buffering", "no")
		sw.w.WriteHeader(http.StatusOK)
		sw.flusher.Flush()
		sw.started = true
	}

	return writeSSEEvent(sw.w, sw.flusher, event)
}

// SendJSON sends an SSE event with JSON-encoded data and an optional event type.
func (sw *StreamWriter) SendJSON(eventType string, v any) error {
	return sw.Send(SSEEvent{Event: eventType, Data: v})
}

// SendData sends an SSE event with just a data field (no event type).
func (sw *StreamWriter) SendData(v any) error {
	return sw.Send(SSEEvent{Data: v})
}

// AcceptsStream checks whether the client can accept an SSE stream by
// looking for text/event-stream in the Accept header.
func AcceptsStream(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
