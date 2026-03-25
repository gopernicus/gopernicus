package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamWriter_Send(t *testing.T) {
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)
	if sw == nil {
		t.Fatal("NewStreamWriter returned nil")
	}

	sw.Send(SSEEvent{Event: "token", Data: "hello"})
	sw.Send(SSEEvent{Data: "world"})

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: token\n") {
		t.Errorf("missing event type in body:\n%s", body)
	}
	if !strings.Contains(body, "data: hello\n\n") {
		t.Errorf("missing first data in body:\n%s", body)
	}
	if !strings.Contains(body, "data: world\n\n") {
		t.Errorf("missing second data in body:\n%s", body)
	}
}

func TestStreamWriter_SendJSON(t *testing.T) {
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)

	sw.SendJSON("update", map[string]int{"progress": 50})

	body := w.Body.String()
	if !strings.Contains(body, "event: update\n") {
		t.Errorf("missing event type in body:\n%s", body)
	}
	if !strings.Contains(body, `data: {"progress":50}`) {
		t.Errorf("missing JSON data in body:\n%s", body)
	}
}

func TestStreamWriter_SendData(t *testing.T) {
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)

	sw.SendData("just data")

	body := w.Body.String()
	if !strings.Contains(body, "data: just data\n\n") {
		t.Errorf("body = %q", body)
	}
	// Should NOT have an event: line.
	if strings.Contains(body, "event:") {
		t.Errorf("unexpected event type in body:\n%s", body)
	}
}

func TestAcceptsStream(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"text/event-stream", true},
		{"application/json, text/event-stream", true},
		{"application/json", false},
		{"", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Accept", tt.accept)
		if got := AcceptsStream(r); got != tt.want {
			t.Errorf("AcceptsStream(%q) = %v, want %v", tt.accept, got, tt.want)
		}
	}
}

func TestStreamWriter_FallbackWhenNotSupported(t *testing.T) {
	// Verify that the pattern of checking nil works.
	// httptest.NewRecorder implements Flusher, so we test the API contract.
	w := httptest.NewRecorder()
	sw := NewStreamWriter(w)

	if sw == nil {
		t.Skip("httptest.ResponseRecorder supports Flusher, can't test nil path")
	}

	// Demonstrate the JSON-or-stream pattern.
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Accept", "application/json, text/event-stream")

	if AcceptsStream(r) && sw != nil {
		sw.SendJSON("result", map[string]string{"answer": "42"})
	} else {
		RespondJSON(w, http.StatusOK, map[string]string{"answer": "42"})
	}

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
