package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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

func TestRespondText(t *testing.T) {
	w := httptest.NewRecorder()
	RespondText(w, http.StatusOK, "hello")

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Body.String() != "hello" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestRespondHTML(t *testing.T) {
	w := httptest.NewRecorder()
	RespondHTML(w, http.StatusOK, "<h1>Hi</h1>")

	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Body.String() != "<h1>Hi</h1>" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestRespondRaw(t *testing.T) {
	w := httptest.NewRecorder()
	RespondRaw(w, http.StatusOK, "image/png", []byte("fake-png"))

	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestRespondNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	RespondNoContent(w)

	if w.Code != 204 {
		t.Errorf("status = %d, want 204", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body should be empty, got %d bytes", w.Body.Len())
	}
}

func TestRespondRedirect(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/old", nil)
	RespondRedirect(w, r, "/new", http.StatusMovedPermanently)

	if w.Code != 301 {
		t.Errorf("status = %d, want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/new" {
		t.Errorf("Location = %q, want /new", loc)
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

func TestRespondFile(t *testing.T) {
	fs := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello world")},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hello.txt", nil)

	RespondFile(w, r, fs, "hello.txt")

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello world") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestRespondFile_NotFound(t *testing.T) {
	fs := fstest.MapFS{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/missing.txt", nil)

	RespondFile(w, r, fs, "missing.txt")

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
