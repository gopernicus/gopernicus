package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestRespondJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"status": "ok"}

	if err := RespondJSON(w, http.StatusOK, data); err != nil {
		t.Fatal(err)
	}

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var got map[string]string
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["status"] != "ok" {
		t.Errorf("body = %v", got)
	}
}

func TestRespondJSONOK(t *testing.T) {
	w := httptest.NewRecorder()
	RespondJSONOK(w, map[string]string{"ok": "yes"})

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRespondJSONCreated(t *testing.T) {
	w := httptest.NewRecorder()
	RespondJSONCreated(w, map[string]int{"id": 1})

	if w.Code != 201 {
		t.Errorf("status = %d, want 201", w.Code)
	}
}

func TestRespondJSONAccepted(t *testing.T) {
	w := httptest.NewRecorder()
	RespondJSONAccepted(w, map[string]string{"job": "queued"})

	if w.Code != 202 {
		t.Errorf("status = %d, want 202", w.Code)
	}
}

func TestRespondJSONError_RetryAfterOn503(t *testing.T) {
	w := httptest.NewRecorder()
	RespondJSONError(w, ErrUnavailable("service unavailable"))
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("status = %d Retry-After = %q, want 503 and 5", w.Code, w.Header().Get("Retry-After"))
	}

	w = httptest.NewRecorder()
	RespondJSONDomainError(w, fmt.Errorf("limiter down: %w", sdk.ErrUnavailable))
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("ErrFromDomain 503: status = %d Retry-After = %q, want 503 and 5", w.Code, w.Header().Get("Retry-After"))
	}

	w = httptest.NewRecorder()
	w.Header().Set("Retry-After", "30")
	RespondJSONError(w, ErrUnavailable("service unavailable"))
	if got := w.Header().Values("Retry-After"); len(got) != 1 || got[0] != "30" {
		t.Fatalf("Retry-After = %q, want the caller's 30 kept", got)
	}

	for _, err := range []*Error{ErrInternal("internal error"), ErrTooManyRequests("slow down"), ErrBadRequest("bad")} {
		w = httptest.NewRecorder()
		RespondJSONError(w, err)
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Fatalf("status %d Retry-After = %q, want none", err.Status, got)
		}
	}
}

func TestRespondJSONError(t *testing.T) {
	w := httptest.NewRecorder()
	RespondJSONError(w, ErrNotFound("user not found"))

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var got map[string]string
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["message"] != "user not found" {
		t.Errorf("message = %q", got["message"])
	}
	if got["code"] != "not_found" {
		t.Errorf("code = %q", got["code"])
	}
}

func TestRespondJSON_NoSniff(t *testing.T) {
	tests := []struct {
		name       string
		respond    func(w http.ResponseWriter)
		wantStatus int
	}{
		{"success", func(w http.ResponseWriter) { _ = RespondJSON(w, http.StatusOK, map[string]string{"ok": "yes"}) }, http.StatusOK},
		{"marshal failure", func(w http.ResponseWriter) { _ = RespondJSON(w, http.StatusOK, func() {}) }, http.StatusInternalServerError},
		{"error body", func(w http.ResponseWriter) { RespondJSONError(w, ErrNotFound("missing")) }, http.StatusNotFound},
		{"domain error", func(w http.ResponseWriter) { RespondJSONDomainError(w, fmt.Errorf("boom")) }, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.respond(w)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Result().Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

// recordingWriter captures a RecordError call so the RespondJSONDomainError
// 5xx seam can be asserted without a delivery-layer import.
type recordingWriter struct {
	*httptest.ResponseRecorder
	recorded error
}

func (rw *recordingWriter) RecordError(err error) { rw.recorded = err }

func TestRespondJSONDomainError_RecordsOn5xx(t *testing.T) {
	original := fmt.Errorf("boom")
	rw := &recordingWriter{ResponseRecorder: httptest.NewRecorder()}

	RespondJSONDomainError(rw, original)

	if rw.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rw.Code)
	}
	if rw.recorded != original {
		t.Errorf("recorded = %v, want original error routed through RecordError", rw.recorded)
	}
}

func TestRespondJSONDomainError_NoRecordBelow5xx(t *testing.T) {
	rw := &recordingWriter{ResponseRecorder: httptest.NewRecorder()}

	RespondJSONDomainError(rw, sdk.ErrNotFound)

	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
	if rw.recorded != nil {
		t.Errorf("recorded = %v, want nil (no record below 5xx)", rw.recorded)
	}
}
