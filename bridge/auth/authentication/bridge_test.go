package authentication

import (
	"net/http/httptest"
	"testing"
	"time"

	coreauth "github.com/gopernicus/gopernicus/core/auth/authentication"
)

func TestBuildAllowedRedirectURIs(t *testing.T) {
	tests := []struct {
		name           string
		callbackBase   string
		callbackPrefix string
		providers      []string
		want           []string
	}{
		{
			name:           "generates web and mobile URIs per provider",
			callbackBase:   "https://api.example.com",
			callbackPrefix: "/api/v1/auth",
			providers:      []string{"google", "github"},
			want: []string{
				"https://api.example.com/api/v1/auth/oauth/callback/google",
				"https://api.example.com/api/v1/auth/oauth/mobile-redirect/google",
				"https://api.example.com/api/v1/auth/oauth/callback/github",
				"https://api.example.com/api/v1/auth/oauth/mobile-redirect/github",
			},
		},
		{
			name:           "empty base URL returns nil",
			callbackBase:   "",
			callbackPrefix: "/api/v1/auth",
			providers:      []string{"google"},
			want:           nil,
		},
		{
			name:           "empty providers returns empty slice",
			callbackBase:   "https://api.example.com",
			callbackPrefix: "/api/v1/auth",
			providers:      []string{},
			want:           []string{},
		},
		{
			name:           "single provider",
			callbackBase:   "https://api.example.com",
			callbackPrefix: "/auth",
			providers:      []string{"google"},
			want: []string{
				"https://api.example.com/auth/oauth/callback/google",
				"https://api.example.com/auth/oauth/mobile-redirect/google",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildAllowedRedirectURIs(tt.callbackBase, tt.callbackPrefix, tt.providers)

			if tt.want == nil {
				if got != nil {
					t.Errorf("BuildAllowedRedirectURIs() = %v, want nil", got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("BuildAllowedRedirectURIs() returned %d URIs, want %d", len(got), len(tt.want))
				return
			}

			for i, uri := range got {
				if uri != tt.want[i] {
					t.Errorf("BuildAllowedRedirectURIs()[%d] = %q, want %q", i, uri, tt.want[i])
				}
			}
		})
	}
}

func TestSetSessionCookies_IncludesDomain(t *testing.T) {
	auth := coreauth.NewAuthenticator(
		"test",
		coreauth.Repositories{},
		nil, nil, nil,
		coreauth.Config{},
	)

	b := New(nil, Config{
		CookieDomain:       ".example.com",
		AccessTokenExpiry:  30 * time.Minute,
		RefreshTokenExpiry: 720 * time.Hour,
	}, auth, nil)

	w := httptest.NewRecorder()
	b.setSessionCookies(w, "access-token", "refresh-token")

	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}

	for _, c := range cookies {
		// Go's http.Cookie parsing strips the leading dot from domain.
		// Both ".example.com" and "example.com" are valid for cross-subdomain.
		if c.Domain != "example.com" && c.Domain != ".example.com" {
			t.Errorf("cookie %q has Domain=%q, want example.com", c.Name, c.Domain)
		}
	}
}

func TestClearSessionCookies_IncludesDomain(t *testing.T) {
	auth := coreauth.NewAuthenticator(
		"test",
		coreauth.Repositories{},
		nil, nil, nil,
		coreauth.Config{},
	)

	b := New(nil, Config{
		CookieDomain: ".example.com",
	}, auth, nil)

	w := httptest.NewRecorder()
	b.clearSessionCookies(w)

	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}

	for _, c := range cookies {
		if c.Domain != "example.com" && c.Domain != ".example.com" {
			t.Errorf("cookie %q has Domain=%q, want example.com", c.Name, c.Domain)
		}
		if c.MaxAge != -1 {
			t.Errorf("cookie %q has MaxAge=%d, want -1 (clear)", c.Name, c.MaxAge)
		}
	}
}

func TestSetSessionCookies_NoDomainWhenEmpty(t *testing.T) {
	auth := coreauth.NewAuthenticator(
		"test",
		coreauth.Repositories{},
		nil, nil, nil,
		coreauth.Config{},
	)

	b := New(nil, Config{
		CookieDomain:       "", // no domain set
		AccessTokenExpiry:  30 * time.Minute,
		RefreshTokenExpiry: 720 * time.Hour,
	}, auth, nil)

	w := httptest.NewRecorder()
	b.setSessionCookies(w, "access-token", "refresh-token")

	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Domain != "" {
			t.Errorf("cookie %q has Domain=%q, want empty", c.Name, c.Domain)
		}
	}
}
