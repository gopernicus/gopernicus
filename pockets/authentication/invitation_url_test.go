package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// S7 R-7 — the invitation landing URL and the one-release query hatch.

func TestValidateInvitationURL(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mode    environment.Mode
		url     string
		wantErr error
	}{
		{"https accepted in production", environment.ModeProduction, "https://app.example.com/invite", nil},
		{"query accepted", environment.ModeProduction, "https://app.example.com/invite?from=mail", nil},
		{"http accepted in development", environment.ModeDevelopment, "http://localhost:8099/", nil},
		{"http rejected in production", environment.ModeProduction, "http://app.example.com/invite", ErrInvitationURLInsecure},
		{"relative rejected", environment.ModeDevelopment, "/invite", ErrInvitationURLInvalid},
		{"no host rejected", environment.ModeDevelopment, "https:///invite", ErrInvitationURLInvalid},
		{"userinfo rejected", environment.ModeDevelopment, "https://user:pass@app.example.com/invite", ErrInvitationURLInvalid},
		{"non-http scheme rejected", environment.ModeDevelopment, "ftp://app.example.com/invite", ErrInvitationURLInvalid},
		{"fragment rejected", environment.ModeDevelopment, "https://app.example.com/#/invite", ErrInvitationURLInvalid},
		{"empty fragment rejected", environment.ModeDevelopment, "https://app.example.com/invite#", ErrInvitationURLInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInvitationURL(tt.mode, tt.url)
			if tt.wantErr == nil && err != nil || tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateInvitationURL(%s, %q) = %v, want %v", tt.mode, tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestInvitationURLPosture proves the landing URL is required in production
// beside a Granter, WARNed about in development, never asked of a host without
// invitations, and that the query hatch is one production posture_waiver.
func TestInvitationURLPosture(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mode         environment.Mode
		granter      bool
		url          string
		inQuery      bool
		want         error
		unsetWarns   int
		hatchWaivers int
		hatchWarns   int
	}{
		{name: "production without a URL", mode: environment.ModeProduction, granter: true, want: ErrInvitationURLRequired},
		{name: "production with a URL", mode: environment.ModeProduction, granter: true, url: "https://app.example.com/invite"},
		{name: "production http URL", mode: environment.ModeProduction, granter: true, url: "http://app.example.com/invite", want: ErrInvitationURLInsecure},
		{name: "production hatch", mode: environment.ModeProduction, granter: true, url: "https://app.example.com/invite", inQuery: true, hatchWaivers: 1},
		{name: "production without invitations", mode: environment.ModeProduction},
		{name: "development without a URL", mode: environment.ModeDevelopment, granter: true, unsetWarns: 1},
		{name: "development fragment URL", mode: environment.ModeDevelopment, granter: true, url: "http://localhost/#x", want: ErrInvitationURLInvalid},
		{name: "development hatch", mode: environment.ModeDevelopment, granter: true, url: "http://localhost/", inQuery: true, hatchWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := bufferLog()
			cfg := noPasswordConfig()
			if tc.mode == environment.ModeProduction {
				cfg = prodLimiterConfig()
				cfg.RateLimiter = durableLimiter{}
			}
			if tc.granter {
				cfg.Granter = stubGranter{}
				cfg.InviteCheck = allowInvite
			}
			cfg.InvitationURL = tc.url
			cfg.InvitationTokenInQuery = tc.inQuery
			cfg.Logger = log
			_, err := newFixture(testRepositories(Repositories{Invitations: stubInvitations{}}), cfg)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == ErrInvitationURLRequired && !strings.Contains(err.Error(), "AUTH_INVITATION_URL") {
				t.Errorf("the refusal does not name the fix: %v", err)
			}
			if n := strings.Count(buf.String(), "LinksConfig.InvitationURL is unset"); n != tc.unsetWarns {
				t.Errorf("unset WARN logged %d times, want %d:\n%s", n, tc.unsetWarns, buf.String())
			}
			if n := strings.Count(buf.String(), "posture_waiver=InvitationTokenInQuery"); n != tc.hatchWaivers {
				t.Errorf("hatch waiver logged %d times, want %d:\n%s", n, tc.hatchWaivers, buf.String())
			}
			if n := strings.Count(buf.String(), "one-release hatch"); n != tc.hatchWarns {
				t.Errorf("development hatch WARN logged %d times, want %d:\n%s", n, tc.hatchWarns, buf.String())
			}
		})
	}
}

// TestSecurityEventsRouteRequiresRepositoryAndCheck proves the opt-in names a
// route that can mount: it needs Repositories.SecurityEvents and a
// UserAdminCheck.
func TestSecurityEventsRouteRequiresRepositoryAndCheck(t *testing.T) {
	allow := func(context.Context, inbound.UserAdminCheckRequest) error { return nil }
	cfg := noPasswordConfig()
	cfg.SecurityEventsRoute = true
	if _, err := newFixture(testRepositories(Repositories{}), cfg); !errors.Is(err, ErrSecurityEventsRouteRequires) {
		t.Fatalf("no check: err = %v, want ErrSecurityEventsRouteRequires", err)
	}
	cfg.UserAdminCheck = allow
	repos := testRepositories(Repositories{UserAdmin: stubUserAdmin{}})
	if _, err := newFixture(repos, cfg); !errors.Is(err, ErrSecurityEventsRouteRequires) {
		t.Fatalf("no repository: err = %v, want ErrSecurityEventsRouteRequires", err)
	}
}
