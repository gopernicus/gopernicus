package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestErrValidation_MaxBytesError(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	body := strings.NewReader(`{"name":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	r := httptest.NewRequest("POST", "/", body)
	r.Body = http.MaxBytesReader(httptest.NewRecorder(), r.Body, 4)

	_, err := DecodeJSON[payload](r)
	if err == nil {
		t.Fatal("expected a decode error from the body-size limit")
	}

	got := ErrValidation(err)
	if got.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", got.Status)
	}
	if got.Code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", got.Code)
	}
}

func TestErrValidation_FieldErrors(t *testing.T) {
	var fe FieldErrors
	fe.Add("email", "is required")
	fe.Add("password", "too short")

	got := ErrValidation(fe.Err())
	if got.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", got.Status)
	}
	if got.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", got.Code)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(got.Fields))
	}
	if got.Fields[0].Field != "email" || got.Fields[1].Field != "password" {
		t.Errorf("fields = %+v, want per-field detail preserved", got.Fields)
	}
}

func TestErrValidation_PlainError(t *testing.T) {
	got := ErrValidation(fmt.Errorf("json decode: unexpected token"))
	if got.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", got.Status)
	}
	if got.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", got.Code)
	}
	if got.Message != "json decode: unexpected token" {
		t.Errorf("message = %q, want the raw error text", got.Message)
	}
}

// TestErrFromDomain_Kinds pins the domain-error-to-status mapping, including the
// backpressure kind (sdk.ErrUnavailable → 503) distinct from state contention
// (sdk.ErrConflict → 409). A domain error wrapping the kind must map by kind.
func TestErrFromDomain_Kinds(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"NotFound", fmt.Errorf("x: %w", sdk.ErrNotFound), http.StatusNotFound, "not_found"},
		{"Conflict", fmt.Errorf("x: %w", sdk.ErrConflict), http.StatusConflict, "conflict"},
		{"Expired", fmt.Errorf("x: %w", sdk.ErrExpired), http.StatusGone, "expired"},
		{"Unavailable", fmt.Errorf("queue full: %w", sdk.ErrUnavailable), http.StatusServiceUnavailable, "unavailable"},
		{"Unknown", fmt.Errorf("boom"), http.StatusInternalServerError, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrFromDomain(tt.err)
			if got.Status != tt.status {
				t.Errorf("status = %d, want %d", got.Status, tt.status)
			}
			if got.Code != tt.code {
				t.Errorf("code = %q, want %q", got.Code, tt.code)
			}
		})
	}
}

func TestErrSentinels(t *testing.T) {
	tests := []struct {
		name   string
		err    *Error
		status int
		code   string
	}{
		{"PayloadTooLarge", ErrPayloadTooLarge("too big"), http.StatusRequestEntityTooLarge, "payload_too_large"},
		{"TooManyRequests", ErrTooManyRequests("slow down"), http.StatusTooManyRequests, "rate_limit_exceeded"},
		{"Unavailable", ErrUnavailable("down"), http.StatusServiceUnavailable, "unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Status != tt.status {
				t.Errorf("status = %d, want %d", tt.err.Status, tt.status)
			}
			if tt.err.Code != tt.code {
				t.Errorf("code = %q, want %q", tt.err.Code, tt.code)
			}
		})
	}
}
