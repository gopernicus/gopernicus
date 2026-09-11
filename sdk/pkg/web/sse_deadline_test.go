package web

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

type sseDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *sseDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestSSEWriteDeadlineRespectsRequestLifetime(t *testing.T) {
	deadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	events := make(chan SSEEvent, 2)
	events <- SSEEvent{Data: "one"}
	events <- SSEEvent{Data: "two"}
	close(events)
	writer := &sseDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	stream := NewSSEStream(events, WithHeartbeat(time.Minute))
	stream.ServeHTTP(writer, httptest.NewRequest("GET", "/events", nil).WithContext(ctx))
	if len(writer.deadlines) != 3 {
		t.Fatalf("set %d deadlines, want initial flush and two frames", len(writer.deadlines))
	}
	for _, got := range writer.deadlines {
		if !got.Equal(deadline) {
			t.Fatalf("write deadline = %s, exceeds stream lifetime %s", got, deadline)
		}
	}
}
