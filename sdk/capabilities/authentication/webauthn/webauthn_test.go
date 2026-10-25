package webauthn

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestRequirementWireStrings(t *testing.T) {
	for got, want := range map[Requirement]string{
		Required:    "required",
		Preferred:   "preferred",
		Discouraged: "discouraged",
	} {
		if string(got) != want {
			t.Errorf("Requirement %q, want the wire string %q", got, want)
		}
	}
}

func TestErrorKinds(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		matches  []error
		excludes []error
	}{
		{"ceremony rejected", ErrCeremonyRejected, []error{sdk.ErrUnauthorized}, []error{ErrUserVerificationRequired, sdk.ErrInvalidInput}},
		{"user verification required", ErrUserVerificationRequired, []error{sdk.ErrUnauthorized}, []error{ErrCeremonyRejected, sdk.ErrInvalidInput}},
		{"origin outside the RP ID", ErrOriginOutsideRPID, []error{sdk.ErrInvalidInput}, []error{sdk.ErrUnauthorized}},
	}
	for _, tt := range tests {
		for _, target := range tt.matches {
			if !errors.Is(tt.err, target) {
				t.Errorf("%s: errors.Is(%v, %v) = false", tt.name, tt.err, target)
			}
		}
		for _, target := range tt.excludes {
			if errors.Is(tt.err, target) {
				t.Errorf("%s: errors.Is(%v, %v) = true", tt.name, tt.err, target)
			}
		}
	}
}
