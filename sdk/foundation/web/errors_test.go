package web

import (
	"errors"
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

// TestErrFromDomain_SafeDomainError pins the host-seam exception: the explicit
// wrapper's public body reaches the wire while errors.Is still matches the
// domain cause it was constructed with.
func TestErrFromDomain_SafeDomainError(t *testing.T) {
	safe := NewSafeDomainError(
		ErrStateConflict("already attached to another account"),
		fmt.Errorf("routing: %w", sdk.ErrConflict),
	)

	got := ErrFromDomain(fmt.Errorf("grant: %w", safe))
	if got.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", got.Status)
	}
	if got.Code != "conflict" {
		t.Errorf("code = %q, want conflict", got.Code)
	}
	if got.Message != "already attached to another account" {
		t.Errorf("message = %q, want the host's sentence", got.Message)
	}
	if !errors.Is(safe, sdk.ErrConflict) {
		t.Error("errors.Is(safe, sdk.ErrConflict) = false, want the cause preserved")
	}
	if !errors.Is(fmt.Errorf("grant: %w", safe), sdk.ErrConflict) {
		t.Error("errors.Is through an outer wrap = false, want the cause preserved")
	}
}

// TestErrFromDomain_SafeDomainErrorNoBody covers the defensive branch: a wrapper
// without a public body falls through to the generic kind switch.
func TestErrFromDomain_SafeDomainErrorNoBody(t *testing.T) {
	got := ErrFromDomain(NewSafeDomainError(nil, sdk.ErrConflict))
	if got.Status != http.StatusConflict || got.Code != "conflict" {
		t.Errorf("status/code = %d/%q, want 409/conflict", got.Status, got.Code)
	}
	if got.Message != "conflict" {
		t.Errorf("message = %q, want the generic body", got.Message)
	}
}

// TestErrFromDomain_GenericBodies proves the wrapper is the only exception:
// bare sentinels, an arbitrary wrapped *Error, and a feature-internal-style
// error all keep today's generic body.
func TestErrFromDomain_GenericBodies(t *testing.T) {
	errAlreadyMember := fmt.Errorf("authentication: already a member: %w", sdk.ErrConflict)

	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"BareSentinel", sdk.ErrConflict, http.StatusConflict, "conflict", "conflict"},
		{
			"ArbitraryWrappedError",
			fmt.Errorf("%w: %w", ErrStateConflict("already attached to another account"), sdk.ErrConflict),
			http.StatusConflict, "conflict", "conflict",
		},
		{"FeatureInternal", errAlreadyMember, http.StatusConflict, "conflict", "conflict"},
		{
			"FeatureInternalInvalidReference",
			fmt.Errorf("routing key is not valid for this resource: %w", sdk.ErrInvalidReference),
			http.StatusBadRequest, "bad_request", "invalid reference",
		},
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
			if got.Message != tt.message {
				t.Errorf("message = %q, want %q", got.Message, tt.message)
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
		{"Conflict", ErrConflict("duplicate slug"), http.StatusConflict, "already_exists"},
		{"StateConflict", ErrStateConflict("last admin"), http.StatusConflict, "conflict"},
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
