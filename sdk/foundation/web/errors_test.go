package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

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
// bare sentinels, an arbitrary wrapped *Error, and a pocket-internal-style
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

// A crud parse rejection reaches web as a plain error wrapping the root
// sdk.ErrInvalidInput sentinel: ErrFromDomain classifies it 400 with the
// generic body, and ErrValidation is the path that carries its sentence.
func TestParseRejection_StatusAndMessagePaths(t *testing.T) {
	err := fmt.Errorf("rows value too large, must be at most 100: %w", sdk.ErrInvalidInput)

	domain := ErrFromDomain(err)
	if domain.Status != http.StatusBadRequest {
		t.Errorf("ErrFromDomain status = %d, want 400", domain.Status)
	}
	if domain.Code != "bad_request" {
		t.Errorf("ErrFromDomain code = %q, want bad_request", domain.Code)
	}
	if domain.Message != "invalid input" {
		t.Errorf("ErrFromDomain message = %q, want the generic %q", domain.Message, "invalid input")
	}

	validation := ErrValidation(err)
	if validation.Status != http.StatusBadRequest {
		t.Errorf("ErrValidation status = %d, want 400", validation.Status)
	}
	if validation.Code != "bad_request" {
		t.Errorf("ErrValidation code = %q, want bad_request", validation.Code)
	}
	if want := "rows value too large, must be at most 100: invalid input"; validation.Message != want {
		t.Errorf("ErrValidation message = %q, want %q", validation.Message, want)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Error("the synthetic error must keep matching sdk.ErrInvalidInput")
	}
}

// ---------------------------------------------------------------------------
// The write vocabulary: sdk.ValidationError and sdk.StaleError
// ---------------------------------------------------------------------------

func TestErrValidation_SDKValidationError(t *testing.T) {
	ve := sdk.Refuse("name", sdk.CodeRequired, "name is required")
	ve.Add("starts_at", sdk.CodeInvalidFormat, "starts_at must be a date")

	got := ErrValidation(fmt.Errorf("create booking: %w", ve))

	if got.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", got.Status)
	}
	if got.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", got.Code)
	}
	if got.Message != "validation failed" {
		t.Errorf("message = %q, want %q", got.Message, "validation failed")
	}
	want := []FieldError{
		{Field: "name", Message: "name is required", Code: sdk.CodeRequired},
		{Field: "starts_at", Message: "starts_at must be a date", Code: sdk.CodeInvalidFormat},
	}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("fields = %+v, want %+v", got.Fields, want)
	}
}

// TestValidationErrorRenderParity pins that ErrValidation and ErrFromDomain
// render the same *sdk.ValidationError to byte-identical bodies — they share
// one helper precisely so the two cannot drift.
func TestValidationErrorRenderParity(t *testing.T) {
	ve := sdk.Refuse("owner_id", sdk.CodeUnknownReference, `unknown owner_id "usr_1"`)
	ve.Add("title", sdk.CodeRequired, "title is required")
	err := fmt.Errorf("update article: %w", ve)

	fromValidation := ErrValidation(err)
	fromDomain := ErrFromDomain(err)

	if !reflect.DeepEqual(fromValidation, fromDomain) {
		t.Fatalf("ErrValidation = %+v, ErrFromDomain = %+v, want identical", fromValidation, fromDomain)
	}

	a, err1 := json.Marshal(fromValidation)
	b, err2 := json.Marshal(fromDomain)
	if err1 != nil || err2 != nil {
		t.Fatalf("marshal errors: %v / %v", err1, err2)
	}
	if string(a) != string(b) {
		t.Errorf("bodies differ:\n%s\n%s", a, b)
	}
}

// TestErrValidation_MaxBytesWinsOverValidationError pins the branch order at
// the transport edge: the 413 contract is checked before the field rendering.
func TestErrValidation_MaxBytesWinsOverValidationError(t *testing.T) {
	err := fmt.Errorf("%w: %w", &http.MaxBytesError{Limit: 1024}, sdk.Refuse("name", sdk.CodeRequired, "name is required"))

	got := ErrValidation(err)
	if got.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", got.Status)
	}
	if got.Code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", got.Code)
	}
}

// TestEmptyValidationErrorFallsThrough pins the empty-collector rule: a typed
// but empty *sdk.ValidationError never renders "fields":[] — it falls through
// to the generic 400 on both paths.
func TestEmptyValidationErrorFallsThrough(t *testing.T) {
	empty := &sdk.ValidationError{}

	fromDomain := ErrFromDomain(empty)
	if fromDomain.Status != http.StatusBadRequest || fromDomain.Code != "bad_request" {
		t.Errorf("ErrFromDomain status/code = %d/%q, want 400/bad_request", fromDomain.Status, fromDomain.Code)
	}
	if fromDomain.Message != "invalid input" {
		t.Errorf("ErrFromDomain message = %q, want the generic body", fromDomain.Message)
	}
	if fromDomain.Fields != nil {
		t.Errorf("ErrFromDomain fields = %+v, want nil", fromDomain.Fields)
	}

	fromValidation := ErrValidation(empty)
	if fromValidation.Status != http.StatusBadRequest || fromValidation.Code != "bad_request" {
		t.Errorf("ErrValidation status/code = %d/%q, want 400/bad_request", fromValidation.Status, fromValidation.Code)
	}
	if fromValidation.Fields != nil {
		t.Errorf("ErrValidation fields = %+v, want nil", fromValidation.Fields)
	}

	body, _ := json.Marshal(fromValidation)
	if strings.Contains(string(body), "fields") {
		t.Errorf("body = %s, want no fields key", body)
	}
}

// TestErrFromDomain_SafeDomainErrorWinsOverValidationError documents the pinned
// branch order: the host seam's chosen body wins and the per-field detail is
// dropped.
func TestErrFromDomain_SafeDomainErrorWinsOverValidationError(t *testing.T) {
	safe := NewSafeDomainError(
		ErrBadRequest("that booking window is closed"),
		sdk.Refuse("starts_at", sdk.CodeInvalidFormat, "starts_at must be in the future"),
	)

	got := ErrFromDomain(fmt.Errorf("book: %w", safe))
	if got.Message != "that booking window is closed" {
		t.Errorf("message = %q, want the host's sentence", got.Message)
	}
	if got.Fields != nil {
		t.Errorf("fields = %+v, want nil (documented field drop)", got.Fields)
	}
}

func TestErrStale(t *testing.T) {
	current := time.Date(2026, 8, 31, 12, 30, 15, 123456000, time.UTC)

	got := ErrStale("the resource changed", current)
	if got.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", got.Status)
	}
	if got.Code != "stale" {
		t.Errorf("code = %q, want stale", got.Code)
	}
	if want := "2026-08-31T12:30:15.123456Z"; got.CurrentUpdatedAt != want {
		t.Errorf("current_updated_at = %q, want %q", got.CurrentUpdatedAt, want)
	}
}

func TestErrStale_NormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*60*60)
	got := ErrStale("the resource changed", time.Date(2026, 8, 31, 14, 0, 0, 0, zone))

	if want := "2026-08-31T12:00:00Z"; got.CurrentUpdatedAt != want {
		t.Errorf("current_updated_at = %q, want %q", got.CurrentUpdatedAt, want)
	}
}

// TestErrFromDomain_StaleError pins that the StaleError branch runs BEFORE the
// errors.Is switch, whose sdk.ErrConflict arm would otherwise swallow it.
func TestErrFromDomain_StaleError(t *testing.T) {
	current := time.Date(2026, 8, 31, 12, 30, 15, 123456000, time.UTC)
	stale := &sdk.StaleError{CurrentUpdatedAt: current}

	got := ErrFromDomain(fmt.Errorf("update article: %w", stale))
	if got.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", got.Status)
	}
	if got.Code != "stale" {
		t.Errorf("code = %q, want stale (not the generic conflict)", got.Code)
	}
	if want := "2026-08-31T12:30:15.123456Z"; got.CurrentUpdatedAt != want {
		t.Errorf("current_updated_at = %q, want %q", got.CurrentUpdatedAt, want)
	}
	if got.Message != stale.Error() {
		t.Errorf("message = %q, want %q", got.Message, stale.Error())
	}
	if !errors.Is(stale, sdk.ErrConflict) {
		t.Error("errors.Is(StaleError, sdk.ErrConflict) = false, want the sentinel preserved")
	}
}

// TestErrorBodies_UnchangedWhenNewFieldsAreZero is the compatibility assertion:
// every response that does not use the write vocabulary marshals exactly as it
// did before code/current_updated_at existed.
func TestErrorBodies_UnchangedWhenNewFieldsAreZero(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{"NotFound", ErrNotFound("not found"), `{"message":"not found","code":"not_found"}`},
		{"StateConflict", ErrStateConflict("conflict"), `{"message":"conflict","code":"conflict"}`},
		{
			"FieldErrors",
			ErrValidation(FieldErrors{{Field: "name", Message: "name is required"}}),
			`{"message":"validation failed","code":"validation_failed","fields":[{"field":"name","message":"name is required"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.err)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(body) != tt.want {
				t.Errorf("body = %s, want %s", body, tt.want)
			}
		})
	}
}
