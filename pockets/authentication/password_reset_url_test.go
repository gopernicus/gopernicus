package authentication

import (
	"errors"
	"testing"
)

// CHAU-5.1 — the reset landing URL's configuration contract.
//
// Every rejection here exists because the alternative failure is invisible until
// production: a fragment silently swallows the token query, a pre-existing
// `token` parameter gets overwritten, and plain HTTP puts a single-use credential
// on the wire.

func TestValidatePasswordResetURL(t *testing.T) {
	tests := []struct {
		name    string
		mode    RuntimeMode
		url     string
		wantErr error
	}{
		{"https accepted in production", RuntimeModeProduction, "https://app.example.com/reset-password", nil},
		{"https accepted in development", RuntimeModeDevelopment, "https://app.example.com/reset-password", nil},
		{"http accepted in development", RuntimeModeDevelopment, "http://localhost:3000/reset-password", nil},
		{"existing non-secret query accepted", RuntimeModeProduction, "https://app.example.com/reset?app=console", nil},

		{"http rejected in production", RuntimeModeProduction, "http://app.example.com/reset-password", ErrPasswordResetURLInsecure},

		{"relative path rejected", RuntimeModeDevelopment, "/reset-password", ErrPasswordResetURLInvalid},
		{"scheme-relative rejected", RuntimeModeDevelopment, "//app.example.com/reset", ErrPasswordResetURLInvalid},
		{"no host rejected", RuntimeModeDevelopment, "https:///reset-password", ErrPasswordResetURLInvalid},
		{"non-http scheme rejected", RuntimeModeDevelopment, "ftp://app.example.com/reset", ErrPasswordResetURLInvalid},
		{"fragment rejected", RuntimeModeDevelopment, "https://app.example.com/reset#step2", ErrPasswordResetURLInvalid},
		{"empty fragment marker rejected", RuntimeModeDevelopment, "https://app.example.com/reset#", ErrPasswordResetURLInvalid},
		{"pre-existing token parameter rejected", RuntimeModeDevelopment, "https://app.example.com/reset?token=abc", ErrPasswordResetURLInvalid},
		{"pre-existing empty token parameter rejected", RuntimeModeDevelopment, "https://app.example.com/reset?token=", ErrPasswordResetURLInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePasswordResetURL(tt.mode, tt.url)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validatePasswordResetURL(%q, %q) = %v, want nil", tt.mode, tt.url, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validatePasswordResetURL(%q, %q) = %v, want errors.Is %v", tt.mode, tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestPasswordResetTokenParamIsShared pins that the validator's rejection and the
// builder's append use ONE constant. Two string literals could drift, and the
// symptom would be a reset link the SPA cannot read.
func TestPasswordResetTokenParamIsShared(t *testing.T) {
	if PasswordResetTokenParam != "token" {
		t.Fatalf("PasswordResetTokenParam = %q, want \"token\"", PasswordResetTokenParam)
	}
	// The validator rejects exactly the parameter the builder appends.
	if err := validatePasswordResetURL(RuntimeModeDevelopment,
		"https://app.example.com/reset?"+PasswordResetTokenParam+"=x"); !errors.Is(err, ErrPasswordResetURLInvalid) {
		t.Errorf("the validator does not reject the parameter the builder appends: %v", err)
	}
}
