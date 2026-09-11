package authentication

import (
	"errors"
	"testing"

	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// oauth-pending-link plan D2/D5 — the OAuth pending-link landing URL's
// configuration contract.
//
// Empty is deliberately allowed in EVERY mode (the caller degrades to the
// bare-token email line). Every non-empty rejection exists because the alternative
// failure is invisible until production: a fragment silently swallows the appended
// "#token=", and plain HTTP puts a single-use credential on the wire.

func TestValidateOAuthLinkBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		mode    environment.Mode
		url     string
		wantErr error
	}{
		{"https accepted in production", environment.ModeProduction, "https://app.example.com/auth/oauth/link", nil},
		{"https accepted in development", environment.ModeDevelopment, "https://app.example.com/auth/oauth/link", nil},
		{"http accepted in development", environment.ModeDevelopment, "http://localhost:3000/auth/oauth/link", nil},
		{"existing non-secret query accepted", environment.ModeProduction, "https://app.example.com/link?app=console", nil},

		{"http rejected in production", environment.ModeProduction, "http://app.example.com/link", ErrOAuthLinkURLInsecure},

		{"relative path rejected", environment.ModeDevelopment, "/auth/oauth/link", ErrOAuthLinkURLInvalid},
		{"scheme-relative rejected", environment.ModeDevelopment, "//app.example.com/link", ErrOAuthLinkURLInvalid},
		{"no host rejected", environment.ModeDevelopment, "https:///link", ErrOAuthLinkURLInvalid},
		{"non-http scheme rejected", environment.ModeDevelopment, "ftp://app.example.com/link", ErrOAuthLinkURLInvalid},
		{"fragment rejected", environment.ModeDevelopment, "https://app.example.com/link#step2", ErrOAuthLinkURLInvalid},
		{"empty fragment marker rejected", environment.ModeDevelopment, "https://app.example.com/link#", ErrOAuthLinkURLInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOAuthLinkBaseURL(tt.mode, tt.url)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validateOAuthLinkBaseURL(%q, %q) = %v, want nil", tt.mode, tt.url, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateOAuthLinkBaseURL(%q, %q) = %v, want errors.Is %v", tt.mode, tt.url, err, tt.wantErr)
			}
		})
	}
}
