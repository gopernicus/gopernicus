package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestExternalMaxAuthenticationAge pins ExternalProvider.MaxAuthenticationAge:
// a token whose authentication time is older than the bound plus the 60-second
// leeway, or that carries none, is denied before the subject resolves (no
// provisioning) with its bounded reason; a younger one passes; and the check
// runs on every request, never from the login-record cache.
func TestExternalMaxAuthenticationAge(t *testing.T) {
	bound := func(c *ExternalConfig) { c.Providers[0].MaxAuthenticationAge = 10 * time.Minute }
	cases := []struct {
		name   string
		age    time.Duration
		reason string
	}{
		{"older than the bound and the leeway", 11*time.Minute + time.Second, externalReasonAgeExceeded},
		{"inside the leeway", 10*time.Minute + 30*time.Second, ""},
		{"younger than the bound", 9 * time.Minute, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newExtHarness(t, bound)
			raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Email: "a@example.test", EmailVerified: true, AuthenticatedAt: h.clock.Now().Add(-tc.age)})
			_, _, err := h.engine.Verify(context.Background(), raw, TransportHeader)
			denied := h.eventsOf(securityevent.TypeExternalSubjectDenied)
			if tc.reason == "" {
				if err != nil || len(denied) != 0 {
					t.Fatalf("Verify = %v, denials %+v; want a pass", err, denied)
				}
				return
			}
			if !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("Verify = %v, want a 401 denial", err)
			}
			if len(denied) != 1 || denied[0].Details["reason"] != tc.reason || denied[0].Details["provider"] != "alpha" {
				t.Fatalf("denied events = %+v, want reason %q provider alpha", denied, tc.reason)
			}
			if h.store.provisionCalls.Load() != 0 {
				t.Fatal("a token past its authentication age provisioned a user")
			}
		})
	}

	t.Run("no authentication time", func(t *testing.T) {
		h := newExtHarness(t, bound)
		raw := extToken(h.providerA.issuer, "s1")
		h.providerA.identities[raw] = sdkauth.Identity{Issuer: h.providerA.issuer, Subject: "s1", Email: "a@example.test", EmailVerified: true}
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("Verify = %v, want a 401 denial", err)
		}
		denied := h.eventsOf(securityevent.TypeExternalSubjectDenied)
		if len(denied) != 1 || denied[0].Details["reason"] != externalReasonAuthTimeMissing {
			t.Fatalf("denied events = %+v, want reason %q", denied, externalReasonAuthTimeMissing)
		}
	})

	t.Run("judged per request", func(t *testing.T) {
		h := newExtHarness(t, bound)
		h.store.addUser("u1", "alpha", "s1", user.StatusActive)
		raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", AuthenticatedAt: h.clock.Now().Add(-9 * time.Minute)})
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); err != nil {
			t.Fatalf("first Verify = %v, want a pass", err)
		}
		h.clock.advance(3 * time.Minute)
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("Verify once the same token aged past the bound = %v, want a denial", err)
		}
	})

	t.Run("unbounded by default", func(t *testing.T) {
		h := newExtHarness(t, nil)
		raw := extToken(h.providerA.issuer, "s1")
		h.providerA.identities[raw] = sdkauth.Identity{Issuer: h.providerA.issuer, Subject: "s1", Email: "a@example.test", EmailVerified: true}
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); err != nil {
			t.Fatalf("Verify without a bound = %v, want a pass", err)
		}
	})
}

// TestExternalLifetimePosture pins the provider lifetime boot line, the
// negative-bound refusal, the production WARN for a provider bounded by token
// expiry alone, and the RevocationFailOpen posture_waiver line.
func TestExternalLifetimePosture(t *testing.T) {
	repos := func() ExternalRepositories {
		s := newExtStore()
		return ExternalRepositories{Users: extUsers{s}, Identifiers: extIdentifiers{s}, ExternalSubjects: extSubjects{s}}
	}
	build := func(mode environment.Mode, p ExternalProvider) (string, error) {
		var buf bytes.Buffer
		p.Name, p.Provider, p.RequireMFA = "idp", &extProvider{issuer: extIssuerA}, true
		_, err := NewExternalAuthenticator(repos(), ExternalConfig{Providers: []ExternalProvider{p}}, mode, WithExternalLogger(slog.New(slog.NewTextHandler(&buf, nil))))
		return buf.String(), err
	}

	if _, err := build(environment.ModeDevelopment, ExternalProvider{MaxAuthenticationAge: -time.Minute}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("negative MaxAuthenticationAge = %v, want sdk.ErrInvalidInput", err)
	}

	logs, err := build(environment.ModeDevelopment, ExternalProvider{MaxAuthenticationAge: 10 * time.Minute})
	if err != nil || !strings.Contains(logs, `msg="auth: external provider lifetime" provider=idp max_authentication_age=10m0s revocation_checker=none fail_open=false`) {
		t.Fatalf("boot line: err = %v, logs:\n%s", err, logs)
	}

	logs, err = build(environment.ModeProduction, ExternalProvider{})
	if err != nil || strings.Count(logs, "external_authentication_age=unbounded provider=idp revocation_checker=none") != 1 {
		t.Fatalf("production unbounded: err = %v, logs:\n%s", err, logs)
	}
	for _, p := range []ExternalProvider{{MaxAuthenticationAge: time.Hour}, {Revocation: &extRevocation{}}} {
		logs, err = build(environment.ModeProduction, p)
		if err != nil || strings.Contains(logs, "external_authentication_age=unbounded") {
			t.Fatalf("production bounded %+v: err = %v, logs:\n%s", p, err, logs)
		}
	}
	logs, _ = build(environment.ModeDevelopment, ExternalProvider{})
	if strings.Contains(logs, "external_authentication_age=unbounded") {
		t.Fatalf("development logged the production WARN:\n%s", logs)
	}

	logs, err = build(environment.ModeProduction, ExternalProvider{Revocation: &extRevocation{}, RevocationFailOpen: true})
	if err != nil || strings.Count(logs, `msg="auth: production posture requirement waived" posture_waiver=RevocationFailOpen runtime_mode=production provider=idp`) != 1 {
		t.Fatalf("production fail-open waiver: err = %v, logs:\n%s", err, logs)
	}
	logs, _ = build(environment.ModeDevelopment, ExternalProvider{Revocation: &extRevocation{}, RevocationFailOpen: true})
	if strings.Contains(logs, "posture_waiver=RevocationFailOpen") {
		t.Fatalf("development logged the fail-open waiver:\n%s", logs)
	}
}

// TestExternalAuthenticationAgeDenialRecordedOncePerToken pins the
// per-token dedupe of the authentication-age denial event: every request is
// still a 401, but external_subject_denied is recorded once per token (keyed
// by its SHA-256, so the cache never holds it); a cache fault records the row
// anyway; the other denial reasons are not deduped.
func TestExternalAuthenticationAgeDenialRecordedOncePerToken(t *testing.T) {
	const n = 4
	bound := func(c *ExternalConfig) { c.Providers[0].MaxAuthenticationAge = 10 * time.Minute }
	overAge := func(h *extHarness, subject string) string {
		return h.token(h.providerA, sdkauth.Identity{Subject: subject, Email: subject + "@example.test", EmailVerified: true,
			AuthenticatedAt: h.clock.Now().Add(-time.Hour), ExpiresAt: h.clock.Now().Add(30 * time.Minute)})
	}
	verifyN := func(t *testing.T, h *extHarness, raw string) {
		t.Helper()
		for i := range n {
			if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrExternalSubjectDenied) || !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("Verify #%d = %v, want the 401 denial every time", i+1, err)
			}
		}
	}
	reasons := func(h *extHarness) []any {
		var out []any
		for _, e := range h.eventsOf(securityevent.TypeExternalSubjectDenied) {
			out = append(out, e.Details["reason"])
		}
		return out
	}

	t.Run("same token, then a second token", func(t *testing.T) {
		logins := newClockedStore(func() time.Time { return time.Now() })
		h := newExtHarness(t, func(c *ExternalConfig) { bound(c); c.LoginRecordCache = logins })
		first := overAge(h, "s1")
		verifyN(t, h, first)
		if got := reasons(h); len(got) != 1 || got[0] != externalReasonAgeExceeded {
			t.Fatalf("denials after %d requests = %v, want one %s", n, got, externalReasonAgeExceeded)
		}
		if logins.lastTTL != 30*time.Minute {
			t.Errorf("dedupe TTL = %v, want the token's remaining 30m", logins.lastTTL)
		}
		verifyN(t, h, overAge(h, "s2"))
		if got := reasons(h); len(got) != 2 {
			t.Fatalf("denials after a second token = %v, want two", got)
		}
		if h.store.provisionCalls.Load() != 0 {
			t.Fatal("an over-age token provisioned a user")
		}
	})

	t.Run("missing authentication time, no known expiry", func(t *testing.T) {
		logins := newClockedStore(func() time.Time { return time.Now() })
		h := newExtHarness(t, func(c *ExternalConfig) { bound(c); c.LoginRecordCache = logins })
		raw := extToken(h.providerA.issuer, "s1")
		h.providerA.identities[raw] = sdkauth.Identity{Issuer: h.providerA.issuer, Subject: "s1"}
		verifyN(t, h, raw)
		if got := reasons(h); len(got) != 1 || got[0] != externalReasonAuthTimeMissing {
			t.Fatalf("denials = %v, want one %s", got, externalReasonAuthTimeMissing)
		}
		if logins.lastTTL != maxLoginRecordTTL {
			t.Errorf("dedupe TTL with no known expiry = %v, want %v", logins.lastTTL, maxLoginRecordTTL)
		}
	})

	t.Run("cache failure still records", func(t *testing.T) {
		h := newExtHarness(t, func(c *ExternalConfig) { bound(c); c.LoginRecordCache = failingCache{} })
		verifyN(t, h, overAge(h, "s1"))
		if got := reasons(h); len(got) != n {
			t.Fatalf("denials with the cache down = %v, want %d (fail toward evidence)", got, n)
		}
	})

	t.Run("other reasons are not deduped", func(t *testing.T) {
		h := newExtHarness(t, func(c *ExternalConfig) { bound(c); c.Providers[0].AllowedTenants = []string{"org-1"} })
		raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Tenant: "org-2", Email: "a@example.test", EmailVerified: true})
		verifyN(t, h, raw)
		if got := reasons(h); len(got) != n || got[0] != externalReasonTenant {
			t.Fatalf("tenant denials = %v, want %d", got, n)
		}
	})
}

// TestExternalRequireAuthenticationTime pins
// ExternalProvider.RequireAuthenticationTime: under a bound, an identity whose
// AuthenticatedAt is the iat fallback is denied authenticated_at_unasserted
// before the subject resolves, every request, recorded once per token; an
// asserted one passes; a missing time still reads missing, an over-age
// asserted one still reads exceeded; without the flag the same unasserted
// token passes (token-age bound only); the flag without a bound refuses at
// construction; and the lifetime boot line names the posture.
func TestExternalRequireAuthenticationTime(t *testing.T) {
	required := func(c *ExternalConfig) {
		c.Providers[0].MaxAuthenticationAge = 10 * time.Minute
		c.Providers[0].RequireAuthenticationTime = true
	}
	identity := func(h *extHarness, subject string, asserted bool, age time.Duration) string {
		return h.token(h.providerA, sdkauth.Identity{Subject: subject, Email: subject + "@example.test", EmailVerified: true,
			AuthenticatedAt: h.clock.Now().Add(-age), AuthTimeAsserted: asserted, ExpiresAt: h.clock.Now().Add(30 * time.Minute)})
	}
	reasons := func(h *extHarness) []any {
		var out []any
		for _, e := range h.eventsOf(securityevent.TypeExternalSubjectDenied) {
			out = append(out, e.Details["reason"])
			if e.Details["provider"] != "alpha" {
				t.Fatalf("denial details = %v, want provider alpha", e.Details)
			}
		}
		return out
	}

	t.Run("unasserted denied once per token", func(t *testing.T) {
		h := newExtHarness(t, required)
		raw := identity(h, "s1", false, time.Minute)
		for i := range 3 {
			if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrExternalSubjectDenied) || !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("Verify #%d = %v, want the 401 denial", i+1, err)
			}
		}
		if got := reasons(h); len(got) != 1 || got[0] != externalReasonAuthTimeUnasserted {
			t.Fatalf("denials = %v, want one %s", got, externalReasonAuthTimeUnasserted)
		}
		if h.store.provisionCalls.Load() != 0 {
			t.Fatal("an unasserted token provisioned a user")
		}
	})

	t.Run("asserted passes", func(t *testing.T) {
		h := newExtHarness(t, required)
		if _, _, err := h.engine.Verify(context.Background(), identity(h, "s1", true, time.Minute), TransportHeader); err != nil {
			t.Fatalf("asserted Verify = %v, want a pass", err)
		}
	})

	t.Run("order", func(t *testing.T) {
		h := newExtHarness(t, required)
		missing := extToken(h.providerA.issuer, "s-missing")
		h.providerA.identities[missing] = sdkauth.Identity{Issuer: h.providerA.issuer, Subject: "s-missing"}
		for _, raw := range []string{missing, identity(h, "s-old", true, time.Hour), identity(h, "s-old-iat", false, time.Hour)} {
			_, _, _ = h.engine.Verify(context.Background(), raw, TransportHeader)
		}
		want := []any{externalReasonAuthTimeMissing, externalReasonAgeExceeded, externalReasonAuthTimeUnasserted}
		if got := reasons(h); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("reasons = %v, want %v", got, want)
		}
	})

	t.Run("off admits the iat fallback", func(t *testing.T) {
		h := newExtHarness(t, func(c *ExternalConfig) { c.Providers[0].MaxAuthenticationAge = 10 * time.Minute })
		if _, _, err := h.engine.Verify(context.Background(), identity(h, "s1", false, time.Minute), TransportHeader); err != nil {
			t.Fatalf("unasserted Verify without the flag = %v, want a pass", err)
		}
	})

	t.Run("needs a bound, logs its posture", func(t *testing.T) {
		build := func(p ExternalProvider) (string, error) {
			var buf bytes.Buffer
			s := newExtStore()
			p.Name, p.Provider, p.RequireMFA = "idp", &extProvider{issuer: extIssuerA}, true
			_, err := NewExternalAuthenticator(ExternalRepositories{Users: extUsers{s}, Identifiers: extIdentifiers{s}, ExternalSubjects: extSubjects{s}},
				ExternalConfig{Providers: []ExternalProvider{p}}, environment.ModeDevelopment, WithExternalLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			return buf.String(), err
		}
		if _, err := build(ExternalProvider{RequireAuthenticationTime: true}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("RequireAuthenticationTime without a bound = %v, want sdk.ErrInvalidInput", err)
		}
		logs, err := build(ExternalProvider{RequireAuthenticationTime: true, MaxAuthenticationAge: time.Hour})
		if err != nil || !strings.Contains(logs, `max_authentication_age=1h0m0s revocation_checker=none fail_open=false authentication_time=asserted_required`) {
			t.Fatalf("boot line: err = %v, logs:\n%s", err, logs)
		}
		logs, _ = build(ExternalProvider{MaxAuthenticationAge: time.Hour})
		if !strings.Contains(logs, `fail_open=false authentication_time=any`) {
			t.Fatalf("boot line without the flag:\n%s", logs)
		}
	})
}
