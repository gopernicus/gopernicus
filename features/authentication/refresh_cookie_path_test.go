package authentication

import (
	"errors"
	"testing"
)

// Refresh-cookie path tests (upstream evidence §2). Config.RefreshCookiePath scopes
// ONLY the refresh cookie; empty defaults to "/auth" and a non-empty value must be a
// valid absolute cookie path so a prefixed host cannot silently scope the cookie to a
// path a browser will never match. The end-to-end issue/rotate/delete and cookie-jar
// proofs live in internal/inbound/authentication.

func refreshPathBaseConfig() Config {
	return Config{
		Hasher:       stubHasher{},
		Mailer:       stubMailer{},
		TokenSigner:  stubSigner{},
		RuntimeMode:  RuntimeModeDevelopment,
		DeliveryMode: DeliveryModeOff,
	}
}

// TestRefreshCookiePathConstructionMatrix proves an empty path constructs (the /auth
// default), a prefixed absolute path constructs, and every malformed value fails
// LOUDLY with ErrRefreshCookiePathInvalid rather than being silently sanitized by
// net/http at Set-Cookie time.
func TestRefreshCookiePathConstructionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"empty defaults", "", nil},
		{"root", "/", nil},
		{"default explicitly", "/auth", nil},
		{"prefixed host", "/api/v1/auth", nil},
		{"no leading slash", "auth", ErrRefreshCookiePathInvalid},
		{"relative", "api/v1/auth", ErrRefreshCookiePathInvalid},
		{"trailing slash", "/api/v1/auth/", ErrRefreshCookiePathInvalid},
		{"query marker", "/auth?x=1", ErrRefreshCookiePathInvalid},
		{"fragment marker", "/auth#frag", ErrRefreshCookiePathInvalid},
		{"semicolon delimiter", "/auth;Domain=evil.example", ErrRefreshCookiePathInvalid},
		{"comma delimiter", "/auth,/other", ErrRefreshCookiePathInvalid},
		{"space", "/auth /refresh", ErrRefreshCookiePathInvalid},
		{"quote", `/auth"`, ErrRefreshCookiePathInvalid},
		{"control character", "/auth\x00", ErrRefreshCookiePathInvalid},
		{"header injection", "/auth\r\nSet-Cookie: a=b", ErrRefreshCookiePathInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := refreshPathBaseConfig()
			cfg.RefreshCookiePath = tt.path
			_, err := NewService(Repositories{}, cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("NewService: err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewService: err=%v, want %v", err, tt.wantErr)
			}
		})
	}
}
