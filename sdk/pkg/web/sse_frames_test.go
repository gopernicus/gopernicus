package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type sseTestWriter struct {
	response   *httptest.ResponseRecorder
	writeError error
	shortWrite bool
	flushError error
	failFlush  int
	flushes    int
	recorded   error
}

func (w *sseTestWriter) Header() http.Header    { return w.response.Header() }
func (w *sseTestWriter) WriteHeader(status int) { w.response.WriteHeader(status) }
func (w *sseTestWriter) RecordError(err error)  { w.recorded = err }
func (w *sseTestWriter) Write(p []byte) (int, error) {
	if w.writeError != nil {
		return 0, w.writeError
	}
	if w.shortWrite {
		return w.response.Write(p[:len(p)-1])
	}
	return w.response.Write(p)
}
func (w *sseTestWriter) FlushError() error {
	w.flushes++
	if w.flushes == w.failFlush {
		return w.flushError
	}
	w.response.Flush()
	return nil
}

func TestSSEStream_MultilineData(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		want string
	}{
		{"LF", "first\nsecond", "data: first\ndata: second\n\n"},
		{"CRLF", "first\r\nsecond", "data: first\ndata: second\n\n"},
		{"CR", "first\rsecond", "data: first\ndata: second\n\n"},
		{"empty", "", "data: \n\n"},
		{"trailing newline", "first\n", "data: first\ndata: \n\n"},
		{"blank lines", "\nfirst\n\nlast", "data: \ndata: first\ndata: \ndata: last\n\n"},
		{"bytes", []byte("first\r\nsecond\rlast"), "data: first\ndata: second\ndata: last\n\n"},
		{"frame injection", "first\n\nevent: injected\ndata: second", "data: first\ndata: \ndata: event: injected\ndata: data: second\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan SSEEvent, 1)
			events <- SSEEvent{Data: tc.data}
			close(events)
			w := httptest.NewRecorder()
			NewSSEStream(events).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
			if got := w.Body.String(); got != tc.want {
				t.Fatalf("frame = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSSEStream_InvalidEventWritesNoPartialFrame(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event SSEEvent
	}{
		{"event CR", SSEEvent{Event: "update\rretry: 1", Data: "payload"}},
		{"event LF", SSEEvent{Event: "update\nevent: injected", Data: "payload"}},
		{"ID CR", SSEEvent{Event: "update", ID: "123\r456", Data: "payload"}},
		{"ID LF", SSEEvent{Event: "update", ID: "123\n456", Data: "payload"}},
		{"ID NUL", SSEEvent{Event: "update", ID: "123\x00456", Data: "payload"}},
		{"JSON encoding", SSEEvent{Event: "update", ID: "123", Retry: 100, Data: func() {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan SSEEvent, 2)
			events <- tc.event
			events <- SSEEvent{Data: "must not be sent"}
			close(events)
			w := &sseTestWriter{response: httptest.NewRecorder()}
			NewSSEStream(events).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
			if w.recorded == nil {
				t.Fatal("invalid event was not recorded")
			}
			if body := w.response.Body.String(); body != "" {
				t.Fatalf("invalid event wrote partial frame or HTTP error text: %q", body)
			}
			if w.response.Code != http.StatusOK || w.flushes != 1 {
				t.Fatalf("status = %d, flushes = %d; want initial 200 flush only", w.response.Code, w.flushes)
			}
		})
	}
}

func TestSSEStream_RecordsTransportErrors(t *testing.T) {
	writeErr := errors.New("connection write failed")
	flushErr := errors.New("connection flush failed")
	for _, tc := range []struct {
		name string
		w    sseTestWriter
		want error
	}{
		{"initial flush", sseTestWriter{failFlush: 1, flushError: flushErr}, flushErr},
		{"frame write", sseTestWriter{writeError: writeErr}, writeErr},
		{"short write", sseTestWriter{shortWrite: true}, io.ErrShortWrite},
		{"frame flush", sseTestWriter{failFlush: 2, flushError: flushErr}, flushErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan SSEEvent, 2)
			events <- SSEEvent{Data: "first"}
			events <- SSEEvent{Data: "must not be sent"}
			close(events)
			tc.w.response = httptest.NewRecorder()
			NewSSEStream(events).ServeHTTP(&tc.w, httptest.NewRequest(http.MethodGet, "/events", nil))
			if !errors.Is(tc.w.recorded, tc.want) {
				t.Fatalf("recorded = %v, want %v", tc.w.recorded, tc.want)
			}
			body := tc.w.response.Body.String()
			if strings.Contains(body, "must not be sent") || strings.Contains(body, "streaming not supported") {
				t.Fatalf("stream continued after transport failure: %q", body)
			}
			if tc.w.response.Code != http.StatusOK {
				t.Fatalf("status = %d, want committed 200", tc.w.response.Code)
			}
		})
	}
}

func TestSSEStream_RecordsHeartbeatErrors(t *testing.T) {
	for _, stage := range []string{"write", "flush"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				want := errors.New("heartbeat transport failed")
				w := &sseTestWriter{response: httptest.NewRecorder(), writeError: want}
				if stage == "flush" {
					w.writeError = nil
					w.flushError = want
					w.failFlush = 2
				}
				events := make(chan SSEEvent)
				NewSSEStream(events, WithHeartbeat(time.Second)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
				if !errors.Is(w.recorded, want) {
					t.Fatalf("recorded = %v, want %v", w.recorded, want)
				}
			})
		})
	}
}

func TestSSEStream_MultilineWire(t *testing.T) {
	events := make(chan SSEEvent, 1)
	events <- SSEEvent{Event: "update", ID: "42", Retry: 1000, Data: "first\r\n\r\nevent: injected\rdata: second"}
	close(events)
	server := httptest.NewServer(NewSSEStream(events))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := "event: update\nid: 42\nretry: 1000\ndata: first\ndata: \ndata: event: injected\ndata: data: second\n\n"
	if string(body) != want {
		t.Fatalf("wire frame = %q, want %q", body, want)
	}
}

func TestSSEOptionsDoNotRetainConstructionConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var settings *sseConfig
		events := make(chan SSEEvent)
		stream := NewSSEStream(events, WithHeartbeat(time.Hour), WithHeartbeat(time.Second), func(c *sseConfig) { settings = c })
		settings.heartbeat = 0
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		recorder := httptest.NewRecorder()
		stream.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx))
		if body := recorder.Body.String(); body != ": ping\n\n" {
			t.Fatalf("heartbeat body = %q", body)
		}
	})
}
