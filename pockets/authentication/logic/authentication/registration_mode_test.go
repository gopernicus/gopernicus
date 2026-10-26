package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// lookupResolver is an invitation resolver with the pending lookup: the listed
// addresses have an invitation.
type lookupResolver struct {
	invited map[string]bool
	err     error
	asked   []string
}

func (r *lookupResolver) ResolveInvitations(context.Context, string, string, string, string) (int, error) {
	return 0, nil
}

func (r *lookupResolver) HasPendingInvitation(_ context.Context, kind, value string) (bool, error) {
	r.asked = append(r.asked, kind+":"+value)
	return r.invited[value], r.err
}

func TestRegistrationModeValidation(t *testing.T) {
	for _, mode := range []RegistrationMode{"", "open", RegistrationInvitationOnly, RegistrationClosed} {
		d := constructorConfig{Registration: mode, Invitations: &lookupResolver{}}
		if err := validateRegistration(d); err != nil {
			t.Errorf("mode %q = %v, want valid", mode, err)
		}
	}
	if err := validateRegistration(constructorConfig{Registration: "bogus"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("an unknown mode = %v, want ErrInvalidInput", err)
	}
	if err := validateRegistration(constructorConfig{Registration: RegistrationInvitationOnly}); !errors.Is(err, ErrRegistrationModeRequiresInvitations) {
		t.Fatalf("invitation_only without invitations = %v", err)
	}
	ext := &ExternalConfig{Providers: []ExternalProvider{{Name: "idp", ProvisionOnFirstVerification: true}}}
	if err := validateRegistration(constructorConfig{Registration: RegistrationClosed, External: ext}); !errors.Is(err, ErrRegistrationClosedWithProvisioning) {
		t.Fatalf("closed beside provisioning = %v", err)
	}
	if got := RegistrationMode("").String(); got != "open" {
		t.Fatalf("String() of the zero mode = %q, want open", got)
	}
}

func TestRegisterUnderRegistrationModes(t *testing.T) {
	ctx := context.Background()
	closed := newHarness(t, nil)
	closed.svc.registration = RegistrationClosed
	if _, err := closed.svc.Register(ctx, "a@example.com", "password123456789", "A"); !errors.Is(err, ErrRegistrationClosed) || !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("closed register = %v, want ErrRegistrationClosed", err)
	}
	if row := requireEvent(t, closed.events, securityevent.TypeRegister, securityevent.StatusFailure); row.Details["reason"] != securityevent.ReasonRegistrationClosed {
		t.Fatalf("closed register row = %+v", row)
	}

	h := newHarness(t, nil)
	resolver := &lookupResolver{invited: map[string]bool{"invited@example.com": true}}
	h.svc.registration, h.svc.invitations = RegistrationInvitationOnly, resolver
	if _, err := h.svc.Register(ctx, "Stranger@Example.com", "password123456789", "S"); !errors.Is(err, ErrRegistrationInvitationRequired) {
		t.Fatalf("uninvited register = %v, want ErrRegistrationInvitationRequired", err)
	}
	if resolver.asked[0] != "email:stranger@example.com" {
		t.Fatalf("lookup asked %v, want the normalized address", resolver.asked)
	}
	if _, err := h.svc.Register(ctx, "invited@example.com", "password123456789", "I"); err != nil {
		t.Fatalf("invited register: %v", err)
	}
	resolver.invited["invited@example.com"] = false
	if _, err := h.svc.Register(ctx, "invited@example.com", "password123456789", "I"); !errors.Is(err, ErrRegistrationInvitationRequired) {
		t.Fatalf("an existing account without an invitation = %v, want the 403 before the 409", err)
	}
	resolver.err = sdk.ErrUnavailable
	if _, err := h.svc.Register(ctx, "x@example.com", "password123456789", "X"); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("a lookup outage = %v, want it returned", err)
	}
}

func TestOAuthFirstSignInUnderRegistrationModes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		mode   RegistrationMode
		code   string
		reason string
	}{
		{RegistrationClosed, RegistrationCodeClosed, securityevent.ReasonRegistrationClosed},
		{RegistrationInvitationOnly, RegistrationCodeInvitationRequired, securityevent.ReasonInvitationRequired},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			p := &fakeProvider{name: "google", trust: true, providerUserID: "g-new", email: "new@example.com", emailVerified: true}
			h := newOAuthHarness(t, p, nil, func(d *constructorConfig) {
				d.Registration = tc.mode
				d.Invitations = &lookupResolver{invited: map[string]bool{}}
			})
			state := h.startState(t, "https://app.example.com/welcome")
			_, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state))
			var refusal *RegistrationRefusal
			if !errors.As(err, &refusal) || refusal.Code != tc.code || refusal.RedirectTo != "https://app.example.com/welcome" || !errors.Is(err, sdk.ErrForbidden) {
				t.Fatalf("callback = %v (%+v), want a refusal with code %s and the destination", err, refusal, tc.code)
			}
			if _, err := h.idents.GetLogin(ctx, "email", "new@example.com"); !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("a refused registration created an identifier: %v", err)
			}
			row := requireEvent(t, h.events, securityevent.TypeOAuthRegister, securityevent.StatusFailure)
			if row.Details["reason"] != tc.reason || row.Details["provider"] != "google" {
				t.Fatalf("oauth_register row = %+v", row)
			}
		})
	}

	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-inv", email: "invitee@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil, func(d *constructorConfig) {
		d.Registration = RegistrationInvitationOnly
		d.Invitations = &lookupResolver{invited: map[string]bool{"invitee@example.com": true}}
	})
	state := h.startState(t, "")
	if res, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); err != nil || res.Action != ActionRegister {
		t.Fatalf("an invited first sign-in = %+v, %v; want register", res, err)
	}
}

// invitedExternal adds the pending lookup to the external harness's resolver.
type invitedExternal struct {
	*extInvitations
	invited map[string]bool
}

func (i invitedExternal) HasPendingInvitation(_ context.Context, _, value string) (bool, error) {
	return i.invited[value], nil
}

func TestExternalProvisionUnderInvitationOnly(t *testing.T) {
	ctx := context.Background()
	h := newExtHarness(t, nil)
	h.engine.registration = RegistrationInvitationOnly
	h.engine.invitations = invitedExternal{extInvitations: h.invitations, invited: map[string]bool{"invited@example.test": true}}

	for _, id := range []sdkauth.Identity{
		{Subject: "s-stranger", Email: "stranger@example.test", EmailVerified: true},
		{Subject: "s-unverified", Email: "invited@example.test"},
	} {
		raw := h.token(h.providerA, id)
		if _, _, err := h.engine.Verify(ctx, raw, TransportHeader); !errors.Is(err, ErrExternalSubjectUnknown) {
			t.Fatalf("%s = %v, want ErrExternalSubjectUnknown", id.Subject, err)
		}
	}
	unknown := h.eventsOf(securityevent.TypeExternalSubjectUnknown)
	if len(unknown) != 2 || unknown[0].Details["reason"] != securityevent.ReasonInvitationRequired {
		t.Fatalf("unknown rows = %+v, want two invitation_required rows", unknown)
	}
	if len(h.eventsOf(securityevent.TypeExternalSubjectProvisioned)) != 0 {
		t.Fatal("an uninvited subject was provisioned")
	}
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s-invited", Email: "Invited@Example.test", EmailVerified: true})
	if _, _, err := h.engine.Verify(ctx, raw, TransportHeader); err != nil {
		t.Fatalf("an invited, verified subject: %v", err)
	}
	if len(h.eventsOf(securityevent.TypeExternalSubjectProvisioned)) != 1 {
		t.Fatal("the invited subject was not provisioned")
	}
}

// stubRedeem satisfies passwordless.Repository so provisioning reads as wired;
// these cases only issue links.
type stubRedeem struct{ passwordless.Repository }

// TestExistingUsersMagicLinkSkipsTheInvitationLookup is M3: an address that
// already resolves to an account gets its link under invitation_only with no
// invitation I/O, so a lookup outage never breaks a known user's sign-in.
func TestExistingUsersMagicLinkSkipsTheInvitationLookup(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	h.svc.provisionOnRedeem, h.svc.passwordlessRedeem = true, stubRedeem{}
	resolver := &lookupResolver{err: sdk.ErrUnavailable}
	h.svc.registration, h.svc.invitations = RegistrationInvitationOnly, resolver
	h.mailer.sent = nil
	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "known@example.com", "link"); err != nil {
		t.Fatalf("StartPasswordless: %v", err)
	}
	if h.mailer.count() != 1 || len(resolver.asked) != 0 {
		t.Fatalf("mail = %d lookups = %v, want the link and no lookup", h.mailer.count(), resolver.asked)
	}
}
