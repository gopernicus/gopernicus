package authentication

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

var uuidV7Text = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// errCaptured stops a flow once its repository input has been captured.
var errCaptured = errors.New("captured")

// idClassMode is one generator wiring and the shape each id class must take
// under it.
type idClassMode struct {
	name             string
	ids              func() sdk.IDGenerator
	identity, record func(string) bool
}

// captureRedeem keeps the passwordless redemption input and stops the flow.
type captureRedeem struct {
	passwordless.Repository
	got *passwordless.RedeemInput
}

func (c captureRedeem) Redeem(_ context.Context, in passwordless.RedeemInput) (passwordless.RedeemResult, error) {
	*c.got = in
	return passwordless.RedeemResult{}, errCaptured
}

// idClassModes are the two wirings every case runs under: no WithIDs (the class
// defaults) and a COUNTING host generator. A constant generator would collide on
// primary keys and defeat the contact-change stale-request guard.
//
// Review invariant (entropy): a record-class id carries a public timestamp and
// only 62 random bits, so no record-class id may ever be a bearer capability.
// Challenges redeem by secret_digest, grants are consumed by (session, purpose,
// context_digest) and no inbound surface exposes a grant id; identifiers,
// security events, passkeys, recovery codes and contact changes are opaque
// keys. Anything a caller presents as proof stays on the secret generators
// pinned in the *_test.go pins (sessions, refresh tokens, OAuth state, API-key
// secrets, resend generations, invitation secrets, archive keys).
func idClassModes() []idClassMode {
	fixed := func(s string) bool { return strings.HasPrefix(s, "fixed-") }
	return []idClassMode{
		{name: "class defaults", ids: func() sdk.IDGenerator { return sdk.IDGenerator{} }, identity: isNanoID, record: uuidV7Text.MatchString},
		{name: "host generator", ids: countingIDs, identity: fixed, record: fixed},
	}
}

func countingIDs() sdk.IDGenerator {
	var n atomic.Int64
	return sdk.NewIDGenerator(func() (string, error) { return fmt.Sprintf("fixed-%d", n.Add(1)), nil })
}

func isNanoID(s string) bool {
	return len(s) == sdk.DefaultIDLength && strings.Trim(s, sdk.DefaultIDAlphabet) == ""
}

// useIDs gives a harness-built Service the generators newService resolves from
// ids, and the recorder it builds over events, exactly as WithIDs would.
func useIDs(svc *Service, events securityevent.SecurityEventRepository, ids sdk.IDGenerator) {
	ref := newService(constructorConfig{IDs: ids, SecurityEvents: events, Clock: svc.now, Logger: svc.logger})
	svc.ids, svc.recordIDs, svc.securityEvents = ref.ids, ref.recordIDs, ref.securityEvents
}

func assertEventIDs(t *testing.T, events *spySecurityEvents, record func(string) bool) {
	t.Helper()
	rows := events.recorded()
	if len(rows) == 0 {
		t.Fatal("no security events recorded")
	}
	for _, evt := range rows {
		if !record(evt.ID) {
			t.Errorf("security event %s id %q is not record-class", evt.EventType, evt.ID)
		}
	}
}

func TestIDClassesRegistration(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newHarness(t, nil)
			useIDs(h.svc, h.events, m.ids())
			ctx := context.Background()
			u := h.mustRegister(t, "classes@example.com", "password123456789")
			if !m.identity(u.ID) {
				t.Errorf("user id %q is not identity-class", u.ID)
			}
			h.ch.mu.Lock()
			for id := range h.ch.byID {
				if !m.record(id) {
					t.Errorf("challenge id %q is not record-class", id)
				}
			}
			h.ch.mu.Unlock()
			// Verify retires the registration identifier and claims its verified
			// replacement through ApplyVerifiedChange.
			if err := h.svc.Verify(ctx, "classes@example.com", h.mailer.codeFor(t, "classes@example.com")); err != nil {
				t.Fatalf("Verify: %v", err)
			}
			h.idents.mu.Lock()
			rows := 0
			for id, it := range h.idents.byID {
				if it.UserID == u.ID {
					rows++
					if !m.record(id) {
						t.Errorf("identifier id %q is not record-class", id)
					}
				}
			}
			h.idents.mu.Unlock()
			if rows != 2 {
				t.Fatalf("identifier rows = %d, want the registration row and its verified replacement", rows)
			}
			assertEventIDs(t, h.events, m.record)
		})
	}
}

func TestIDClassesIdentifierChange(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newHarness(t, nil)
			useIDs(h.svc, h.events, m.ids())
			ctx := context.Background()
			userID, _, sessionID := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")
			if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
				SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
				Value: "second@example.com", Uses: identifier.Uses{Login: true, Recovery: true, Notification: true},
			}); err != nil {
				t.Fatalf("StartIdentifierChange: %v", err)
			}
			pending, err := h.contacts.Get(ctx, userID, identifier.KindEmail)
			if err != nil {
				t.Fatal(err)
			}
			if !m.record(pending.ID) {
				t.Errorf("contact change id %q is not record-class", pending.ID)
			}
			if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
				SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: h.mailer.proofCodeFor(t, "second@example.com"),
			}); err != nil {
				t.Fatalf("ConfirmIdentifierChange: %v", err)
			}
			it, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), "second@example.com")
			if err != nil {
				t.Fatalf("confirmed identifier: %v", err)
			}
			if !m.record(it.ID) || it.ID == pending.ID {
				t.Errorf("confirmed identifier id %q is not a fresh record-class id", it.ID)
			}
			assertEventIDs(t, h.events, m.record)
		})
	}
}

func TestIDClassesStepUpGrant(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newHarness(t, nil)
			useIDs(h.svc, h.events, m.ids())
			ctx := context.Background()
			userID, _, sessionID := h.mustVerifiedLogin(t, "grant@example.com", "password123456789")

			// The recent-login shortcut builds a synthetic grant from the session;
			// it is never persisted, so it must not mint an id.
			synthetic, err := h.svc.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeRemovePassword, "", RecentAuthPolicy{})
			if err != nil {
				t.Fatalf("RequireRecentAuthentication: %v", err)
			}
			if synthetic.ID != "" {
				t.Errorf("synthetic grant id = %q, want empty", synthetic.ID)
			}

			h.backdateLogin(sessionID, time.Hour)
			g, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeRemovePassword}, "password123456789")
			if err != nil {
				t.Fatalf("CompleteStepUpWithPassword: %v", err)
			}
			if !m.record(g.ID) {
				t.Errorf("grant id %q is not record-class", g.ID)
			}
		})
	}
}

func TestIDClassesOAuthRegistration(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			p := &fakeProvider{name: "google", trust: true, providerUserID: "g-new", email: "new@example.com", emailVerified: true}
			ids := m.ids()
			h := newOAuthHarness(t, p, nil, func(d *constructorConfig) { d.IDs = ids })
			res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(h.startState(t, "https://app.example.com/welcome")))
			if err != nil || res.Action != ActionRegister {
				t.Fatalf("OAuthCallback = %+v, %v; want a registration", res, err)
			}
			if !m.identity(res.User.ID) {
				t.Errorf("user id %q is not identity-class", res.User.ID)
			}
			it, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "new@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if !m.record(it.ID) {
				t.Errorf("identifier id %q is not record-class", it.ID)
			}
			assertEventIDs(t, h.events, m.record)
		})
	}
}

func TestIDClassesExternalProvisioning(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newExtHarness(t, nil, WithExternalIDs(m.ids()))
			raw := h.token(h.providerA, sdkauth.Identity{Subject: "s-classes", Email: "Ext@Example.test", EmailVerified: true})
			if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); err != nil {
				t.Fatalf("Verify: %v", err)
			}
			h.store.mu.Lock()
			defer h.store.mu.Unlock()
			if len(h.store.proposed) != 1 || len(h.store.idents) != 1 {
				t.Fatalf("provisioned %d users, %d identifiers; want one each", len(h.store.proposed), len(h.store.idents))
			}
			if !m.identity(h.store.proposed[0].ID) {
				t.Errorf("user id %q is not identity-class", h.store.proposed[0].ID)
			}
			if !m.record(h.store.idents[0].ID) {
				t.Errorf("identifier id %q is not record-class", h.store.idents[0].ID)
			}
			assertEventIDs(t, h.events, m.record)
		})
	}
}

func TestIDClassesPasswordlessProvisioning(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newHarness(t, nil)
			useIDs(h.svc, h.events, m.ids())
			var got passwordless.RedeemInput
			h.svc.passwordlessRedeem = captureRedeem{got: &got}
			if _, _, err := h.svc.redeemPasswordlessAtomic(context.Background(), "link-token"); !errors.Is(err, errCaptured) {
				t.Fatalf("redeemPasswordlessAtomic = %v, want the captured stop", err)
			}
			if !m.identity(got.NewUser.ID) {
				t.Errorf("provisioned user id %q is not identity-class", got.NewUser.ID)
			}
			if !m.record(got.NewIdentifier.ID) {
				t.Errorf("provisioned identifier id %q is not record-class", got.NewIdentifier.ID)
			}
		})
	}
}

func TestIDClassesMachine(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newMachineHarness(t)
			useIDs(h.svc, h.events, m.ids())
			ctx := context.Background()
			sa, err := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
			if err != nil {
				t.Fatalf("CreateServiceAccount: %v", err)
			}
			if !m.identity(sa.ID) {
				t.Errorf("service account id %q is not identity-class", sa.ID)
			}
			key, _, err := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
			if err != nil {
				t.Fatalf("MintAPIKey: %v", err)
			}
			if !m.record(key.ID) {
				t.Errorf("API key id %q is not record-class", key.ID)
			}
		})
	}
}

func TestIDClassesPasskey(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newPasskeyHarness(t)
			useIDs(h.svc, h.events, m.ids())
			u, _, sessionID := h.account(t, "passkey-classes@example.com")
			p := h.enroll(t, newPasskeyAuthenticator(true), sessionID, u.ID, "laptop")
			if !m.record(p.ID) {
				t.Errorf("passkey id %q is not record-class", p.ID)
			}
		})
	}
}

func TestIDClassesRecoveryCodes(t *testing.T) {
	for _, m := range idClassModes() {
		t.Run(m.name, func(t *testing.T) {
			h := newTOTPHarness(t)
			useIDs(h.svc, h.events, m.ids())
			u, _, sessionID := h.totpAccount(t, "totp-classes@example.com")
			h.enrollTOTP(t, sessionID, u.ID)
			h.totp.mu.Lock()
			codes := h.totp.codes[u.ID]
			h.totp.mu.Unlock()
			if len(codes) == 0 {
				t.Fatal("no recovery codes stored")
			}
			seen := map[string]bool{}
			for _, rc := range codes {
				if !m.record(rc.ID) || seen[rc.ID] {
					t.Errorf("recovery code id %q is not a distinct record-class id", rc.ID)
				}
				seen[rc.ID] = true
			}
		})
	}
}

// TestRecorderResolvesTheRecordDefault pins that NewRecorder itself resolves the
// zero generator to UUIDv7, which covers every recorder construction — the
// service, the external engine, invitations and the OAuth2 fallback.
func TestRecorderResolvesTheRecordDefault(t *testing.T) {
	events := newSpySecurityEvents()
	securityevent.NewRecorder(events, sdk.IDGenerator{}, nil, nil).Record(context.Background(), securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
	assertEventIDs(t, events, uuidV7Text.MatchString)
}
