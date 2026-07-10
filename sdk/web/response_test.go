package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRespondStream(t *testing.T) {
	w := httptest.NewRecorder()
	reader := strings.NewReader("streamed data")

	err := RespondStream(w, http.StatusOK, "text/plain", reader)
	if err != nil {
		t.Fatal(err)
	}

	if w.Body.String() != "streamed data" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestRespondStream_DefaultContentType(t *testing.T) {
	w := httptest.NewRecorder()
	RespondStream(w, http.StatusOK, "", strings.NewReader("bytes"))

	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
}
