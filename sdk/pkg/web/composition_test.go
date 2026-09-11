package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRouteGroupMiddlewareOwnership(t *testing.T) {
	pass := func(next http.Handler) http.Handler { return next }
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) })
	}
	t.Run("caller mutation", func(t *testing.T) {
		h := NewWebHandler()
		middleware := []Middleware{deny}
		group := h.Group("/private", middleware...)
		middleware[0] = pass
		group.GET("/data", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/private/data", nil))
		if w.Code != 403 {
			t.Fatalf("status = %d, protection was replaced", w.Code)
		}
	})
	for _, extension := range []string{"sibling", "parent route", "nested sibling"} {
		t.Run(extension, func(t *testing.T) {
			h := NewWebHandler()
			middleware := make([]Middleware, 1, 4)
			middleware[0] = pass
			parent := h.Group("/api", middleware...)
			path := "/api/private/data"
			if extension == "nested sibling" {
				parent = parent.Group("/middle", pass).Group("/inner", pass)
				path = "/api/middle/inner/private/data"
			}
			private := parent.Group("/private", deny)
			if extension != "parent route" {
				parent.Group("/public", pass)
			} else {
				parent.GET("/public", func(http.ResponseWriter, *http.Request) {}, pass)
			}
			private.GET("/data", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "secret") })
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 403 {
				t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestTrustProxiesRepeatedFields(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Add("X-Forwarded-For", "198.51.100.99")
	r.Header.Add("X-Forwarded-For", "203.0.113.10, 203.0.113.20")
	for count, want := range map[int]string{1: "203.0.113.20", 2: "203.0.113.10"} {
		TrustProxies(count)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if got, ok := ClientIP(r.Context()); !ok || got != want {
				t.Errorf("count %d: IP = %q, want %q", count, got, want)
			}
		})).ServeHTTP(httptest.NewRecorder(), r)
	}
}

func TestStatusRecorderWireStatus(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(http.ResponseWriter)
		want  int
	}{
		{"informational then final", func(w http.ResponseWriter) { w.WriteHeader(103); w.WriteHeader(201) }, 201},
		{"informational then implicit", func(w http.ResponseWriter) { w.WriteHeader(103) }, 200},
		{"flush then ignored final", func(w http.ResponseWriter) { http.NewResponseController(w).Flush(); w.WriteHeader(500) }, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			statuses := make(chan [2]int, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				outer := NewStatusRecorder(w)
				inner := NewStatusRecorder(outer)
				test.write(inner)
				io.WriteString(inner, "body")
				statuses <- [2]int{inner.Status(), outer.Status()}
			}))
			defer s.Close()
			resp, err := s.Client().Get(s.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := <-statuses; got != [2]int{test.want, test.want} || resp.StatusCode != test.want || string(body) != "body" {
				t.Fatalf("recorders = %v, wire = %d, body = %q; want %d", got, resp.StatusCode, body, test.want)
			}
		})
	}
}

func TestPanicsPreserveAbortAndPartialResponses(t *testing.T) {
	for _, sentinel := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary panic", true: "abort sentinel"}[sentinel], func(t *testing.T) {
			var logs bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&logs, nil))
			h := Logger(log)(Panics(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "partial")
				http.NewResponseController(w).Flush()
				if sentinel {
					panic(http.ErrAbortHandler)
				}
				panic("broken renderer")
			})))
			s := httptest.NewServer(h)
			resp, err := s.Client().Get(s.URL)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			s.Close()
			if resp.StatusCode != 200 || string(body) != "partial" || !errors.Is(readErr, io.ErrUnexpectedEOF) {
				t.Fatalf("status = %d, body = %q, error = %v", resp.StatusCode, body, readErr)
			}
			if !strings.Contains(logs.String(), `"msg":"request"`) {
				t.Fatal("abort missing access log")
			}
			if sentinel && strings.Contains(logs.String(), `"stack":`) {
				t.Fatal("intentional abort logged a stack")
			}
		})
	}
}

func TestPanicsBeforeResponse(t *testing.T) {
	for _, invalidStatus := range []bool{false, true} {
		h := Panics(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "999")
			w.Header().Set("Content-Encoding", "gzip")
			if invalidStatus {
				w.WriteHeader(42)
			}
			panic("boom")
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal error") || w.Header().Get("Content-Length") != "" || w.Header().Get("Content-Encoding") != "" {
			t.Fatalf("invalid status=%v: code=%d headers=%v body=%q", invalidStatus, w.Code, w.Header(), w.Body.String())
		}
	}
}

type transparentResponseWriter struct{ http.ResponseWriter }

func (w transparentResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestResponseErrorsSurviveWrappers(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Logger(log)(Panics(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = RespondJSONOK(transparentResponseWriter{w}, math.NaN())
	})))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	var entry struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if w.Code != 500 || entry.Status != 500 || !strings.Contains(entry.Error, "NaN") || strings.Contains(w.Body.String(), "NaN") {
		t.Fatalf("status=%d log=%s body=%q", w.Code, logs.String(), w.Body.String())
	}
}

func TestStatusRecorderUnsupportedFlush(t *testing.T) {
	w := NewStatusRecorder(struct{ http.ResponseWriter }{httptest.NewRecorder()})
	if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("flush error=%v", err)
	}
	w.WriteHeader(500)
	if w.Status() != 500 {
		t.Fatalf("unsupported flush committed %d", w.Status())
	}
}

func TestRecordErrorPreservesFirstCause(t *testing.T) {
	outer := NewStatusRecorder(httptest.NewRecorder())
	inner := NewStatusRecorder(transparentResponseWriter{outer})
	cause := errors.New("original render failure")
	RecordError(inner, cause)
	RecordError(inner, nil)
	RecordError(inner, http.ErrAbortHandler)
	if inner.Err() != cause || outer.Err() != cause {
		t.Fatalf("inner=%v outer=%v", inner.Err(), outer.Err())
	}
}

func TestLoggerAbortBeforeResponse(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Logger(log)(Panics(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })))
	func() {
		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Fatalf("panic=%v", got)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	var entry struct {
		Status  int  `json:"status"`
		Aborted bool `json:"aborted"`
	}
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Status != 0 || !entry.Aborted {
		t.Fatalf("log=%s", logs.String())
	}
}

func TestPanicsAfterHijack(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Logger(log)(Panics(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			panic(err)
		}
		defer conn.Close()
		io.WriteString(rw, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nbody")
		rw.Flush()
		panic("after hijack")
	})))
	finished := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		h.ServeHTTP(w, r)
	}))
	resp, err := s.Client().Get(s.URL)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	s.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("hijacked handler did not finish")
	}
	if err != nil || string(body) != "body" {
		t.Fatalf("body=%q error=%v", body, err)
	}
	entries := strings.Split(strings.TrimSpace(logs.String()), "\n")
	var entry struct {
		Status   int  `json:"status"`
		Hijacked bool `json:"hijacked"`
		Aborted  bool `json:"aborted"`
	}
	if err := json.Unmarshal([]byte(entries[len(entries)-1]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Status != 0 || !entry.Hijacked || !entry.Aborted {
		t.Fatalf("log=%s", logs.String())
	}
}
