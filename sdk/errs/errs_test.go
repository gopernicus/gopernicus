package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrors_Wrapping(t *testing.T) {
	sentinels := []struct {
		name     string
		sentinel error
	}{
		{"ErrNotFound", ErrNotFound},
		{"ErrAlreadyExists", ErrAlreadyExists},
		{"ErrInvalidReference", ErrInvalidReference},
		{"ErrInvalidInput", ErrInvalidInput},
		{"ErrUnauthorized", ErrUnauthorized},
		{"ErrForbidden", ErrForbidden},
		{"ErrConflict", ErrConflict},
		{"ErrExpired", ErrExpired},
	}

	for _, tt := range sentinels {
		wrapped := fmt.Errorf("user: %w", tt.sentinel)

		if !errors.Is(wrapped, tt.sentinel) {
			t.Errorf("errors.Is(wrapped, %s) = false, want true", tt.name)
		}

		if wrapped.Error() != "user: "+tt.sentinel.Error() {
			t.Errorf("wrapped.Error() = %q, want %q", wrapped.Error(), "user: "+tt.sentinel.Error())
		}
	}
}

func TestIsExpected(t *testing.T) {
	// Direct sentinels are expected.
	for _, sentinel := range expectedErrors {
		if !IsExpected(sentinel) {
			t.Errorf("IsExpected(%v) = false, want true", sentinel)
		}
	}

	// Wrapped sentinels are expected.
	wrapped := fmt.Errorf("user: %w", ErrNotFound)
	if !IsExpected(wrapped) {
		t.Error("IsExpected(wrapped ErrNotFound) = false, want true")
	}

	// Unknown errors are not expected.
	unknown := errors.New("something broke")
	if IsExpected(unknown) {
		t.Error("IsExpected(unknown error) = true, want false")
	}

	// Wrapped unknown errors are not expected.
	wrappedUnknown := fmt.Errorf("repo: %w", errors.New("connection refused"))
	if IsExpected(wrappedUnknown) {
		t.Error("IsExpected(wrapped unknown) = true, want false")
	}
}

func TestSentinelErrors_NotConfused(t *testing.T) {
	if errors.Is(ErrNotFound, ErrAlreadyExists) {
		t.Error("ErrNotFound should not match ErrAlreadyExists")
	}
	if errors.Is(ErrConflict, ErrAlreadyExists) {
		t.Error("ErrConflict should not match ErrAlreadyExists")
	}
	if errors.Is(ErrUnauthorized, ErrForbidden) {
		t.Error("ErrUnauthorized should not match ErrForbidden")
	}
}
