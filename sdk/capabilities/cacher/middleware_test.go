package cacher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type renderFunc func(context.Context, io.Writer) error

func (f renderFunc) Render(ctx context.Context, w io.Writer) error { return f(ctx, w) }

func TestPagesRenderFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, nil))
		calls := 0
		view := renderFunc(func(_ context.Context, w io.Writer) error {
			calls++
			if _, err := io.WriteString(w, "<h1>page"); err != nil {
				return err
			}
			if fail {
				return errors.New("render failed")
			}
			return nil
		})
		h := web.Logger(log)(Pages(NewMemory(), PageConfig{TTL: time.Minute})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { web.Render(r.Context(), w, 200, view) })))
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			want := "MISS"
			if i == 1 && !fail {
				want = "HIT"
			}
			if w.Code != 200 || w.Header().Get("X-Cache") != want {
				t.Fatalf("fail=%v request=%d status=%d cache=%q", fail, i, w.Code, w.Header().Get("X-Cache"))
			}
		}
		if fail && (calls != 2 || !strings.Contains(logs.String(), "render failed")) {
			t.Fatalf("calls=%d log=%s", calls, logs.String())
		}
		if !fail && calls != 1 {
			t.Fatalf("successful page rendered %d times", calls)
		}
	}
}

type failedWriter struct {
	*httptest.ResponseRecorder
	short bool
	flush bool
}

var errWrite = errors.New("connection write failed")

func (w failedWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) / 2, nil
	}
	return 0, errWrite
}
func (w failedWriter) FlushError() error {
	if w.flush {
		return errWrite
	}
	return nil
}

func TestPagesWriteAndFlushFailures(t *testing.T) {
	for _, kind := range []string{"write", "short write", "flush"} {
		t.Run(kind, func(t *testing.T) {
			store := NewMemory()
			h := Pages(store, PageConfig{TTL: time.Minute})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				if kind == "flush" {
					http.NewResponseController(w).Flush()
				} else {
					io.WriteString(w, "page")
				}
			}))
			r := httptest.NewRequest("GET", "/", nil)
			h.ServeHTTP(failedWriter{httptest.NewRecorder(), kind == "short write", kind == "flush"}, r)
			if _, hit, _ := store.Get(r.Context(), pageKey(r, nil)); hit {
				t.Fatal("failed response cached")
			}
		})
	}
}

func TestPagesFlushThroughLogger(t *testing.T) {
	h := web.Logger(slog.New(slog.DiscardHandler))(Pages(NewMemory(), PageConfig{TTL: time.Minute})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		io.WriteString(w, "page")
	})))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !w.Flushed || w.Header().Get("X-Cache") != "MISS" || w.Body.String() != "page" {
		t.Fatalf("flushed=%v headers=%v body=%q", w.Flushed, w.Header(), w.Body.String())
	}
}
