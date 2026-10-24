package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// requireMFADenial asserts the one mfa_required_denied row: blocked, attributed
// to userID, and carrying exactly want as Details.
func requireMFADenial(t *testing.T, rows []securityevent.SecurityEvent, userID string, want map[string]any) {
	t.Helper()
	var denials []securityevent.SecurityEvent
	for _, evt := range rows {
		if evt.EventType == securityevent.TypeMFARequiredDenied {
			denials = append(denials, evt)
		}
	}
	if len(denials) != 1 {
		t.Fatalf("mfa_required_denied rows = %d, want 1: %+v", len(denials), rows)
	}
	evt := denials[0]
	if evt.EventStatus != securityevent.StatusBlocked || evt.UserID != userID || len(evt.Details) != len(want) {
		t.Fatalf("mfa_required_denied row = %+v, want blocked, user %q, details %v", evt, userID, want)
	}
	for key, value := range want {
		if evt.Details[key] != value {
			t.Fatalf("mfa_required_denied Details[%q] = %v, want %v (%v)", key, evt.Details[key], value, evt.Details)
		}
	}
}

func TestExternalCredentialAssuranceStamped(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mfa       bool
		assurance session.AssuranceLevel
	}{
		{"provider asserted MFA", true, session.AssuranceAAL2},
		{"provider asserted none", false, session.AssuranceAAL1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newExtHarness(t, nil)
			h.store.addUser("u1", "alpha", "s1", user.StatusActive)
			raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", MFA: tc.mfa})
			_, cred, err := h.engine.Verify(context.Background(), raw, TransportHeader)
			if err != nil || cred.Assurance != tc.assurance {
				t.Fatalf("Verify = %v, assurance %q; want %q", err, cred.Assurance, tc.assurance)
			}
			logins := h.eventsOf(securityevent.TypeExternalLogin)
			if len(logins) != 1 || logins[0].Details["assurance"] != string(tc.assurance) {
				t.Fatalf("external_login rows = %+v, want assurance %s", logins, tc.assurance)
			}
			ctx := h.authenticate(t, raw)
			if stashed, _ := h.engine.CurrentCredential(ctx); stashed.Assurance != tc.assurance {
				t.Fatalf("CurrentCredential assurance = %q, want %q", stashed.Assurance, tc.assurance)
			}
		})
	}
}

// TestProviderMFAFloorPrecedesProvisioning proves RequireMFA denies an AAL1
// token before the subject resolves: nothing is provisioned, no user is read,
// and the denial row carries no UserID.
func TestProviderMFAFloorPrecedesProvisioning(t *testing.T) {
	h := newExtHarness(t, func(cfg *ExternalConfig) { cfg.Providers[0].RequireMFA = true })
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	for _, subject := range []string{"s-new", "s1"} {
		raw := h.token(h.providerA, sdkauth.Identity{Subject: subject, Email: subject + "@example.test", EmailVerified: true, Methods: []string{"pwd"}})
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrMFARequired) || !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("%s: Verify = %v, want ErrMFARequired", subject, err)
		}
	}
	if calls, gets := h.store.provisionCalls.Load(), h.store.userGets.Load(); calls != 0 || gets != 0 {
		t.Fatalf("a denied token provisioned %d users and read %d", calls, gets)
	}
	if rows := h.eventsOf(securityevent.TypeExternalSubjectProvisioned); len(rows) != 0 {
		t.Fatalf("a denied token recorded provisioning: %+v", rows)
	}
	if rows := h.eventsOf(securityevent.TypeExternalLogin); len(rows) != 0 {
		t.Fatalf("a denied token recorded a login: %+v", rows)
	}
	denials := h.eventsOf(securityevent.TypeMFARequiredDenied)
	if len(denials) != 2 {
		t.Fatalf("mfa_required_denied rows = %d, want 2", len(denials))
	}
	requireMFADenial(t, denials[1:], "", map[string]any{
		"gate": "provider", "credential": "external_token", "provider": "alpha", "assurance": "aal1", "required": "aal2",
	})
	assertNoSecretInDetails(t, h.events.recorded(), "pwd", "s1@example.test", "s-new")

	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s-mfa", Email: "mfa@example.test", EmailVerified: true, MFA: true})
	principal, cred, err := h.engine.Verify(context.Background(), raw, TransportHeader)
	if err != nil || principal.ID == "" || cred.Assurance != session.AssuranceAAL2 || h.store.provisionCalls.Load() != 1 {
		t.Fatalf("an MFA token on the floor = %+v %+v %v; want admitted and provisioned", principal, cred, err)
	}
	other := h.token(h.providerB, sdkauth.Identity{Subject: "s1"})
	if _, _, err := h.engine.Verify(context.Background(), other, TransportHeader); errors.Is(err, ErrMFARequired) {
		t.Fatal("the floor leaked onto a provider without RequireMFA")
	}
}

// TestMFADenialDetailsAreBounded proves the row carries enums and the host's
// provider name only: an unknown gate, kind or assurance records "unknown",
// a machine principal attributes no user, and no claim reaches Details.
func TestMFADenialDetailsAreBounded(t *testing.T) {
	h := newExtHarness(t, nil)
	h.engine.RecordMFADenied(context.Background(), MFADenial{
		Gate:      "elsewhere",
		Principal: Principal{Type: PrincipalServiceAccount, ID: "sa1"},
		Credential: Credential{Kind: "smart_card", Assurance: "aal9", Identity: sdkauth.Identity{
			Issuer: extIssuerA, Subject: "secret-sub", Email: "secret@example.test", Methods: []string{"otp"},
		}},
	})
	requireMFADenial(t, h.events.recorded(), "", map[string]any{
		"gate": "unknown", "credential": "unknown", "assurance": "unknown", "required": "aal2",
	})
	h.engine.RecordMFADenied(context.Background(), MFADenial{
		Gate:       MFAGateRoute,
		Principal:  Principal{Type: PrincipalUser, ID: "u1"},
		Credential: Credential{Kind: CredentialExternalToken, Assurance: session.AssuranceAAL1, Identity: sdkauth.Identity{Issuer: extIssuerB, Subject: "secret-sub", Methods: []string{"otp"}}},
	})
	route := h.eventsOf(securityevent.TypeMFARequiredDenied)[1]
	if route.UserID != "u1" || route.Details["gate"] != "route" || route.Details["provider"] != "beta" || route.Details["assurance"] != "aal1" {
		t.Fatalf("route denial = %+v", route)
	}
	assertNoSecretInDetails(t, h.events.recorded(), "secret-sub", "secret@example.test", "otp")
}

// TestMFADenialIsBudgeted proves the row draws on the failure-row budget: an
// AAL1 holder can drive it.
func TestMFADenialIsBudgeted(t *testing.T) {
	spy := newSpySecurityEvents()
	rec := securityevent.NewRecorder(spy, sdk.IDGenerator{}, nil, nil,
		securityevent.WithFailureRowBudget(denyLimiter{}, ratelimiter.PerMinute(1)))
	h := newExtHarness(t, func(cfg *ExternalConfig) { cfg.Providers[0].RequireMFA = true })
	engine, err := NewExternalAuthenticator(ExternalRepositories{
		Users: extUsers{h.store}, Identifiers: extIdentifiers{h.store}, ExternalSubjects: extSubjects{h.store},
	}, ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: h.providerA, RequireMFA: true}}},
		environment.ModeDevelopment, WithExternalSecurityEventRecorder(rec))
	if err != nil {
		t.Fatal(err)
	}
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	if _, _, err := engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("Verify = %v, want ErrMFARequired", err)
	}
	if rows := spy.recorded(); len(rows) != 0 || rec.SuppressedRows() != 1 {
		t.Fatalf("rows = %+v, suppressed = %d; want the denial suppressed", rows, rec.SuppressedRows())
	}
}

// TestServiceRecordsMFADenialWithProvider proves the hybrid Service records
// through its one recorder and names the wired provider.
func TestServiceRecordsMFADenialWithProvider(t *testing.T) {
	h := newHarness(t, nil)
	store := newExtStore()
	store.subjects["alpha|s1"] = externalsubject.ExternalSubject{Provider: "alpha", Subject: "s1", UserID: "u1"}
	components, err := New(Repositories{
		Users: h.users, Identifiers: h.idents, Sessions: h.sess, ActiveSessions: fakeActiveSessions{h.users, h.sess},
		ExternalSubjects: extSubjects{store}, SecurityEvents: h.events,
	}, h.signer, environment.ModeDevelopment, ratelimiter.NewMemory(),
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithExternal(ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: extIssuerA}}}}))
	if err != nil {
		t.Fatal(err)
	}
	components.Service.RecordMFADenied(context.Background(), MFADenial{
		Gate:       MFAGateAdmin,
		Principal:  Principal{Type: PrincipalUser, ID: "u1"},
		Credential: Credential{Kind: CredentialExternalToken, Assurance: session.AssuranceAAL1, Identity: sdkauth.Identity{Issuer: extIssuerA}},
	})
	components.Service.RecordMFADenied(context.Background(), MFADenial{
		Gate:       MFAGateAdmin,
		Principal:  Principal{Type: PrincipalUser, ID: "u2"},
		Credential: Credential{Kind: CredentialAccessToken},
	})
	rows := h.events.recorded()
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	requireMFADenial(t, rows[:1], "u1", map[string]any{
		"gate": "admin", "credential": "external_token", "provider": "alpha", "assurance": "aal1", "required": "aal2",
	})
	requireMFADenial(t, rows[1:], "u2", map[string]any{
		"gate": "admin", "credential": "access_token", "assurance": "unknown", "required": "aal2",
	})
}

// TestStandalonePosture proves NewExternalAuthenticator's production check: a
// provider without RequireMFA is cause external_no_mfa, refused unless
// acknowledged; RequireMFA everywhere boots silently; development WARNs once.
func TestStandalonePosture(t *testing.T) {
	repos := func() ExternalRepositories {
		s := newExtStore()
		return ExternalRepositories{Users: extUsers{s}, Identifiers: extIdentifiers{s}, ExternalSubjects: extSubjects{s}}
	}
	providers := func(requireMFA ...bool) []ExternalProvider {
		var out []ExternalProvider
		for i, mfa := range requireMFA {
			issuer := extIssuerA
			if i == 1 {
				issuer = extIssuerB
			}
			out = append(out, ExternalProvider{Name: "idp", Provider: &extProvider{issuer: issuer}, RequireMFA: mfa})
		}
		return out
	}
	build := func(mode environment.Mode, cfg ExternalConfig) (string, error) {
		var buf bytes.Buffer
		_, err := NewExternalAuthenticator(repos(), cfg, mode, WithExternalLogger(slog.New(slog.NewTextHandler(&buf, nil))))
		return buf.String(), err
	}

	for name, cfg := range map[string]ExternalConfig{
		"no RequireMFA":          {Providers: providers(false)},
		"one without of two":     {Providers: providers(true, false)},
		"none of two RequireMFA": {Providers: providers(false, false)},
	} {
		_, err := build(environment.ModeProduction, cfg)
		if !errors.Is(err, ErrSingleFactorUnacknowledged) || !strings.Contains(err.Error(), "ExternalConfig.SingleFactorAcknowledged") || !strings.Contains(err.Error(), "external_no_mfa") {
			t.Errorf("%s: production err = %v, want ErrSingleFactorUnacknowledged naming the acknowledgement and external_no_mfa", name, err)
		}
	}

	logs, err := build(environment.ModeProduction, ExternalConfig{Providers: providers(true, true)})
	if err != nil || strings.Contains(logs, "level=WARN") {
		t.Fatalf("RequireMFA on every provider: err = %v, logs:\n%s", err, logs)
	}

	logs, err = build(environment.ModeProduction, ExternalConfig{Providers: providers(false), SingleFactorAcknowledged: true})
	if err != nil || strings.Count(logs, "posture_waiver=SingleFactorAcknowledged") != 1 || !strings.Contains(logs, "causes=[external_no_mfa]") {
		t.Fatalf("acknowledged: err = %v, logs:\n%s", err, logs)
	}

	logs, err = build(environment.ModeDevelopment, ExternalConfig{Providers: providers(false)})
	if err != nil || strings.Count(logs, "ExternalConfig.SingleFactorAcknowledged") != 1 || strings.Contains(logs, "posture_waiver") {
		t.Fatalf("development: err = %v, logs:\n%s", err, logs)
	}
	logs, err = build(environment.ModeDevelopment, ExternalConfig{Providers: providers(true)})
	if err != nil || strings.Contains(logs, "SingleFactorAcknowledged") {
		t.Fatalf("development with RequireMFA: err = %v, logs:\n%s", err, logs)
	}
}
