package sdk

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestValidationError_Unwrap(t *testing.T) {
	err := Refuse("name", CodeRequired, "name is required")

	if !errors.Is(err, ErrInvalidInput) {
		t.Error("errors.Is(ValidationError, ErrInvalidInput) = false, want true")
	}
	if !IsExpected(err) {
		t.Error("IsExpected(ValidationError) = false, want true")
	}
	if errors.Is(err, ErrConflict) {
		t.Error("errors.Is(ValidationError, ErrConflict) = true, want false")
	}
}

func TestValidationError_ErrorFormat(t *testing.T) {
	tests := []struct {
		name string
		err  *ValidationError
		want string
	}{
		{"empty", &ValidationError{}, "validation failed"},
		{"one", Refuse("name", CodeRequired, "name is required"), "name: name is required"},
		{
			"three",
			&ValidationError{Violations: []Violation{
				{Field: "name", Code: CodeRequired, Message: "name is required"},
				{Field: "email", Code: CodeInvalidFormat, Message: "email is malformed"},
				{Field: "age", Code: CodeInvalidType, Message: "age must be a number"},
			}},
			"name: name is required (and 2 more)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidationError_AddAndErr(t *testing.T) {
	var ve ValidationError

	if err := ve.Err(); err != nil {
		t.Errorf("Err() on empty collector = %v, want nil", err)
	}

	ve.Add("name", CodeRequired, "name is required")
	ve.Add("email", CodeInvalidFormat, "email is malformed")

	err := ve.Err()
	if err == nil {
		t.Fatal("Err() after Add = nil, want the collector")
	}
	if len(ve.Violations) != 2 {
		t.Fatalf("len(Violations) = %d, want 2", len(ve.Violations))
	}
	if ve.Violations[1] != (Violation{Field: "email", Code: CodeInvalidFormat, Message: "email is malformed"}) {
		t.Errorf("Violations[1] = %+v, want the email violation", ve.Violations[1])
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("errors.Is(Err(), ErrInvalidInput) = false, want true")
	}
}

// TestValidationError_ErrEmptyIsUntypedNil pins the typed-nil trap: a caller
// doing `if err := ve.Err(); err != nil` must not fire on an empty collector.
func TestValidationError_ErrEmptyIsUntypedNil(t *testing.T) {
	collect := func() error {
		var ve ValidationError
		return ve.Err()
	}

	if err := collect(); err != nil {
		t.Errorf("Err() on empty collector = %v (%T), want an untyped nil error", err, err)
	}
}

func TestValidationError_ErrorsAs(t *testing.T) {
	wrapped := fmt.Errorf("create article: %w", Refuse("title", CodeRequired, "title is required"))

	var ve *ValidationError
	if !errors.As(wrapped, &ve) {
		t.Fatal("errors.As(wrapped, &*ValidationError) = false, want true")
	}
	if len(ve.Violations) != 1 || ve.Violations[0].Field != "title" {
		t.Errorf("recovered violations = %+v, want one title violation", ve.Violations)
	}
}

// TestValidationError_ValueStoredDoesNotMatch documents the pointer-only
// contract: Error has a pointer receiver, so a ValidationError stored by value
// is not an error at all and cannot be recovered with errors.As. Always return
// *ValidationError (Refuse and Err both do).
func TestValidationError_ValueStoredDoesNotMatch(t *testing.T) {
	var anyValue any = ValidationError{Violations: []Violation{{Field: "name", Code: CodeRequired}}}
	if _, ok := anyValue.(error); ok {
		t.Error("ValidationError (value) satisfies error; Error must stay pointer-only")
	}
}

func TestUnknownReference(t *testing.T) {
	err := UnknownReference("owner_id", "usr_123")

	if len(err.Violations) != 1 {
		t.Fatalf("len(Violations) = %d, want 1", len(err.Violations))
	}
	v := err.Violations[0]
	if v.Field != "owner_id" {
		t.Errorf("Field = %q, want %q", v.Field, "owner_id")
	}
	if v.Code != CodeUnknownReference {
		t.Errorf("Code = %q, want %q", v.Code, CodeUnknownReference)
	}
	if want := `unknown owner_id "usr_123"`; v.Message != want {
		t.Errorf("Message = %q, want %q", v.Message, want)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Error("errors.Is(UnknownReference, ErrInvalidInput) = false, want true")
	}
}

func TestViolationCodes(t *testing.T) {
	codes := map[string]string{
		CodeRequired:         "required",
		CodeInvalidType:      "invalid_type",
		CodeInvalidFormat:    "invalid_format",
		CodeUnknownField:     "unknown_field",
		CodeUnknownReference: "unknown_reference",
	}
	for got, want := range codes {
		if got != want {
			t.Errorf("code = %q, want %q", got, want)
		}
	}
}

func TestStaleError(t *testing.T) {
	current := time.Date(2026, 8, 31, 12, 30, 15, 123456000, time.UTC)
	err := &StaleError{CurrentUpdatedAt: current}

	if !errors.Is(err, ErrConflict) {
		t.Error("errors.Is(StaleError, ErrConflict) = false, want true")
	}
	if !IsExpected(err) {
		t.Error("IsExpected(StaleError) = false, want true")
	}
	if errors.Is(err, ErrInvalidInput) {
		t.Error("errors.Is(StaleError, ErrInvalidInput) = true, want false")
	}

	want := "stale write: the resource changed at 2026-08-31T12:30:15.123456Z"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestStaleError_ErrorNormalizesToUTC pins that the emitted token is UTC
// RFC3339Nano regardless of the store's zone.
func TestStaleError_ErrorNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*60*60)
	err := &StaleError{CurrentUpdatedAt: time.Date(2026, 8, 31, 14, 0, 0, 0, zone)}

	want := "stale write: the resource changed at 2026-08-31T12:00:00Z"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestStaleError_ErrorsAs(t *testing.T) {
	current := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	wrapped := fmt.Errorf("update article: %w", &StaleError{CurrentUpdatedAt: current})

	var se *StaleError
	if !errors.As(wrapped, &se) {
		t.Fatal("errors.As(wrapped, &*StaleError) = false, want true")
	}
	if !se.CurrentUpdatedAt.Equal(current) {
		t.Errorf("CurrentUpdatedAt = %v, want %v", se.CurrentUpdatedAt, current)
	}
}

// TestStaleError_ValueStoredDoesNotMatch documents the pointer-only contract.
func TestStaleError_ValueStoredDoesNotMatch(t *testing.T) {
	var anyValue any = StaleError{CurrentUpdatedAt: time.Now()}
	if _, ok := anyValue.(error); ok {
		t.Error("StaleError (value) satisfies error; Error must stay pointer-only")
	}
}
