package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/logging"
)

// componentFunc is a local Renderer for tests — no templ import, proving the
// render seam works against any standard-library-shaped component.
type componentFunc func(ctx context.Context, w io.Writer) error

func (f componentFunc) Render(ctx context.Context, w io.Writer) error { return f(ctx, w) }

func comp(s string) Renderer {
	return componentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}

func TestRender_WritesHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(context.Background(), rec, http.StatusOK, comp("<h1>hi</h1>"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if rec.Body.String() != "<h1>hi</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestRequestID_GeneratesAndEchoes(t *testing.T) {
	var seen string
	h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = logging.RequestIDFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if seen == "" {
		t.Fatal("no request id on context")
	}
	if got := rec.Header().Get(RequestIDHeader); got != seen {
		t.Errorf("echoed id %q != context id %q", got, seen)
	}
}

func TestRequestID_ReusesInbound(t *testing.T) {
	h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(RequestIDHeader); got != "abc-123" {
		t.Errorf("id = %q, want abc-123 (reuse inbound)", got)
	}
}

func TestPanics_Returns500HTML(t *testing.T) {
	h := Panics(logging.NewNoop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q, want html", ct)
	}
}

func TestErrFromDomain_StatusMapping(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{sdk.ErrNotFound, http.StatusNotFound},
		{sdk.ErrAlreadyExists, http.StatusConflict},
		{sdk.ErrInvalidInput, http.StatusBadRequest},
		{sdk.ErrForbidden, http.StatusForbidden},
		{fmt.Errorf("wrap: %w", sdk.ErrNotFound), http.StatusNotFound},
		{fmt.Errorf("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		if got := ErrFromDomain(tt.err); got.Status != tt.want {
			t.Errorf("ErrFromDomain(%v).Status = %d, want %d", tt.err, got.Status, tt.want)
		}
	}
}

func TestFieldErrors(t *testing.T) {
	var fe FieldErrors
	if fe.Err() != nil {
		t.Error("empty FieldErrors should be nil error")
	}
	fe.Add("title", "is required")
	fe.Add("body", "too short")
	if fe.Err() == nil {
		t.Fatal("populated FieldErrors should be non-nil")
	}
	if len(fe) != 2 {
		t.Errorf("len = %d, want 2", len(fe))
	}
}
