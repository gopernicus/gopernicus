package tracing

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type failingResponse struct {
	*httptest.ResponseRecorder
	failure error
}

func (w failingResponse) Write([]byte) (int, error) { return 0, w.failure }
func (w failingResponse) FlushError() error         { return w.failure }

type hijackResponse struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w hijackResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestHTTPCompletionFailures(t *testing.T) {
	secret := errors.New("SECRET-MARKER response cause")
	for _, test := range []struct {
		name, status string
		handler      http.HandlerFunc
		failing      bool
		panicValue   any
	}{
		{name: "empty", status: "200", handler: func(http.ResponseWriter, *http.Request) {}},
		{name: "created", status: "201", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) }},
		{name: "not found", status: "404", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }},
		{name: "server error", status: "503", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }, failing: true},
		{name: "write", status: "200", handler: func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("partial")) }, failing: true},
		{name: "flush", status: "200", handler: func(w http.ResponseWriter, _ *http.Request) { http.NewResponseController(w).Flush() }, failing: true},
		{name: "panic before headers", handler: func(http.ResponseWriter, *http.Request) { panic(secret) }, failing: true, panicValue: secret},
		{name: "panic after headers", status: "200", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200); panic(secret) }, failing: true, panicValue: secret},
		{name: "abort before headers", handler: func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }, failing: true, panicValue: http.ErrAbortHandler},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracer := &recTracer{}
			var writer http.ResponseWriter = httptest.NewRecorder()
			if test.name == "write" || test.name == "flush" {
				writer = failingResponse{httptest.NewRecorder(), secret}
			}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				Middleware(tracer)(test.handler).ServeHTTP(writer, httptest.NewRequest("GET", "/", nil))
			}()
			if recovered != test.panicValue {
				t.Fatalf("panic changed: %v", recovered)
			}
			span := tracer.spans[0]
			if span.finishCount != 1 || span.attrs["http.status_code"] != test.status || (span.err != nil) != test.failing {
				t.Fatalf("completion=%+v", span)
			}
			if span.err != nil && strings.Contains(span.err.Error(), "SECRET-MARKER") {
				t.Fatal("secret exported")
			}
		})
	}
}

func TestHijackHasNoInventedStatus(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tracer := &recTracer{}
	Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
			t.Fatal(err)
		}
	})).ServeHTTP(hijackResponse{httptest.NewRecorder(), a}, httptest.NewRequest("GET", "/", nil))
	span := tracer.spans[0]
	if _, ok := span.attrs["http.status_code"]; ok || span.finishCount != 1 {
		t.Fatalf("hijack completion: %+v", span)
	}
}

func TestTracingPreservesLoggerCause(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	tracer := &recTracer{}
	handler := Middleware(tracer)(web.Logger(logger)(web.Panics(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200); panic("SECRET-MARKER") }))))
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Error("partial response did not abort")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	span := tracer.spans[0]
	if span.attrs["http.status_code"] != "200" || span.err == nil || span.finishCount != 1 {
		t.Fatalf("partial failure not traced: %+v", span)
	}
	if !strings.Contains(log.String(), "SECRET-MARKER") || strings.Contains(span.err.Error(), "SECRET-MARKER") {
		t.Fatal("logger cause lost or exported")
	}
}

type setupPanicTracer struct{ span *setupPanicSpan }

func (t setupPanicTracer) StartSpan(ctx context.Context, _ string) (context.Context, SpanFinisher) {
	return ctx, t.span
}

type setupPanicSpan struct {
	recSpan
	first bool
}

func (s *setupPanicSpan) SetAttributes(attrs ...Attribute) {
	if !s.first {
		s.first = true
		panic("setup")
	}
	s.recSpan.SetAttributes(attrs...)
}
func TestSpanFinishesWhenMetadataSetupPanics(t *testing.T) {
	span := &setupPanicSpan{recSpan: recSpan{attrs: map[string]string{}}}
	func() {
		defer func() {
			if recover() != "setup" {
				t.Error("setup panic changed")
			}
		}()
		Middleware(setupPanicTracer{span})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler reached") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if span.finishCount != 1 {
		t.Fatalf("finish count=%d", span.finishCount)
	}
}
