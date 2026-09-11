package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestPrincipalRefInvalid proves empty and malformed decision callers are
// rejected and well-formed ones accepted. A PrincipalRef structurally cannot
// carry a userset relation — there is no field for one.
func TestPrincipalRefInvalid(t *testing.T) {
	cases := []struct {
		name    string
		ref     PrincipalRef
		wantErr bool
	}{
		{"ok", PrincipalRef{Type: "user", ID: "u1"}, false},
		{"empty type", PrincipalRef{ID: "u1"}, true},
		{"empty id", PrincipalRef{Type: "user"}, true},
		{"both empty", PrincipalRef{}, true},
		{"control char", PrincipalRef{Type: "user", ID: "u\x01"}, true},
		{"invalid utf8", PrincipalRef{Type: "\xff", ID: "u1"}, true},
		{"over long", PrincipalRef{Type: "user", ID: strings.Repeat("x", 300)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ref.Validate()
			if tc.wantErr {
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("Validate(%+v) = %v, want sdk.ErrInvalidInput", tc.ref, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", tc.ref, err)
			}
		})
	}
}
