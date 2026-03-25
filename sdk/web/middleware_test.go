package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddleware_WildcardWithOrigin(t *testing.T) {
	handler := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	// Wildcard with an Origin header should reflect the origin back.
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "http://example.com")
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want %q", got, "true")
	}
}

func TestCORSMiddleware_WildcardNoOrigin(t *testing.T) {
	handler := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want %q", got, "*")
	}
	// Wildcard without specific origin should NOT set credentials.
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q, want empty", got)
	}
}

func TestCORSMiddleware_SpecificOrigin(t *testing.T) {
	handler := CORSMiddleware([]string{"http://allowed.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Matching origin.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "http://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://allowed.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "http://allowed.com")
	}

	// Non-matching origin.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Origin", "http://evil.com")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)

	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for non-matching origin", got)
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	nextCalled := false
	handler := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	r := httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != 204 {
		t.Errorf("status = %d, want 204", w.Code)
	}
	if nextCalled {
		t.Error("next handler should NOT be called for OPTIONS preflight")
	}
}

func TestDefaultHeadersMiddleware(t *testing.T) {
	handler := DefaultHeadersMiddleware(map[string]string{
		"X-Frame-Options": "DENY",
		"X-Custom":        "value",
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
	}
	if got := w.Header().Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom = %q, want %q", got, "value")
	}
}
