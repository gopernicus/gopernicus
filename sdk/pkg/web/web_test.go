package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
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
		seen, _ = sdk.RequestIDFromContext(r.Context())
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

func TestRequestID_ReplacesInvalidInbound(t *testing.T) {
	for _, inbound := range []string{
		strings.Repeat("A", 129),
		strings.Repeat("A", 300),
		"has space",
		"semi;colon",
		"ünïcode",
		"line\tbreak",
		"<script>",
	} {
		t.Run(inbound[:min(len(inbound), 16)], func(t *testing.T) {
			var seen string
			h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen, _ = sdk.RequestIDFromContext(r.Context())
			}))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set(RequestIDHeader, inbound)
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(RequestIDHeader)
			if got == inbound || got != seen || len(got) != 32 {
				t.Fatalf("echoed %q, context %q; want a fresh 32-char id, never the inbound value", got, seen)
			}
		})
	}
}

func TestRequestID_ReusesValidInbound(t *testing.T) {
	for _, inbound := range []string{"a", "abc-123", "trace.span_01", strings.Repeat("Z", 128)} {
		h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(RequestIDHeader, inbound)
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get(RequestIDHeader); got != inbound {
			t.Errorf("id = %q, want inbound %q reused", got, inbound)
		}
	}
}

func TestPanics_NegotiatesJSON(t *testing.T) {
	for _, tc := range []struct {
		name        string
		accept      string
		contentType string
		wantJSON    bool
	}{
		{"no headers", "", "", false},
		{"json accept", "application/json", "", true},
		{"json ahead of html", "application/json, text/html;q=0.9", "", true},
		{"html ahead of json", "text/html, application/json", "", false},
		{"browser navigation", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "", false},
		{"wildcard", "*/*", "", false},
		{"json body", "", "application/json; charset=utf-8", true},
		{"json body html accept", "text/html", "application/json", true},
		{"form body", "", "application/x-www-form-urlencoded", false},
		{"case-insensitive accept", "Application/JSON", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Panics(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				panic("boom")
			}))
			req := httptest.NewRequest("POST", "/", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			ct := rec.Header().Get("Content-Type")
			if !tc.wantJSON {
				if ct != "text/html; charset=utf-8" || !strings.Contains(rec.Body.String(), "internal error") {
					t.Fatalf("content-type = %q body = %q, want the HTML page", ct, rec.Body.String())
				}
				return
			}
			var body map[string]string
			if ct != "application/json; charset=utf-8" || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
				t.Fatalf("content-type = %q body = %q, want JSON", ct, rec.Body.String())
			}
			if body["code"] != "internal" || body["message"] != "internal error" || strings.Contains(rec.Body.String(), "boom") {
				t.Fatalf("body = %v, want the generic internal error", body)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("headers = %v, want nosniff", rec.Header())
			}
		})
	}
}

func TestPanics_Returns500HTML(t *testing.T) {
	h := Panics(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
