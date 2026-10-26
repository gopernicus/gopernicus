package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authenticationlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// A host transport can establish caller identity and drive step-up without
// depending on the pocket's internal service or HTTP request structures.
func TestHostCanUsePublicSensitiveAuthentication(t *testing.T) {
	host := newLinkHost(t)
	host.signUp("public-api@example.com")
	pair, _, err := host.svc.Authentication.Login(context.Background(), "public-api@example.com", linkPassword)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := host.svc.HTTP.RequireAccessTokenLive()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		uid, ok := host.svc.Authentication.CurrentUser(r.Context())
		if !ok {
			t.Fatal("no authenticated user")
		}
		sid, ok := host.svc.Authentication.CurrentSessionID(r.Context())
		if !ok {
			t.Fatal("no live session")
		}
		methods, err := host.svc.Authentication.Methods(r.Context(), uid)
		if err != nil || !methods.HasPassword {
			t.Fatalf("public inventory: %+v %v", methods, err)
		}
		_, err = host.svc.Authentication.RequireRecentAuthentication(r.Context(), sid, uid, "host_operation", "resource", authenticationlogic.RecentAuthPolicy{MinAssurance: session.AssuranceAAL2})
		if !errors.Is(err, authenticationlogic.ErrStepUpRequired) {
			t.Fatalf("stronger host policy accepted weaker login: %v", err)
		}
		proof, err := host.svc.Authentication.CompleteStepUpWithPassword(r.Context(), authenticationlogic.StepUpCompletion{UserID: uid, SessionID: sid, Purpose: "host_operation", Context: "resource"}, linkPassword)
		if err != nil || proof.SessionID != sid || proof.Assurance != session.AssuranceAAL1 {
			t.Fatalf("public password proof: %+v %v", proof, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPost, "/host-operation", nil)
	r.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("host live gate: %d %s", w.Code, w.Body)
	}
}

// A host transport can read a user's verified identifiers and run the
// on-demand invitation sync through the public service; this fixture wires no
// Granter, so the sync resolves nothing.
func TestHostCanUseVerifiedIdentifiersAndSync(t *testing.T) {
	host := newLinkHost(t)
	c := host.signUp("public-sync@example.com")
	uid := c.userIDFor()
	addresses, err := host.svc.Authentication.VerifiedIdentifiers(context.Background(), uid)
	if err != nil || len(addresses) != 1 || addresses[0].Kind != "email" || addresses[0].Value != "public-sync@example.com" {
		t.Fatalf("VerifiedIdentifiers = %+v, %v", addresses, err)
	}
	if n, err := host.svc.Authentication.SyncInvitations(context.Background(), uid); err != nil || n != 0 {
		t.Fatalf("SyncInvitations = %d, %v; want 0 without invitations", n, err)
	}
}

// TestInvitationOnlyRegistrationThroughTheHost walks AUTH_REGISTRATION
// invitation_only over this host's route table: an uninvited register is the
// coded 403, an invited one registers, and its verification grants the
// invitation (the S7 every-verification rule).
func TestInvitationOnlyRegistrationThroughTheHost(t *testing.T) {
	granter := &recordingGranter{}
	sender := &recordingSender{}
	cfg, err := buildAuthConfig(quietLog(), environment.ModeDevelopment, granter)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	cfg.Registration = auth.RegistrationInvitationOnly
	cfg.PasswordlessProvisionOnRedeem = true
	cfg.InviteCheck = func(context.Context, authenticationhttp.InviteCheckRequest) error { return nil }
	svc, err := auth.New(authmem.New().Repositories(), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatal(err)
	}
	stop := runDelivery(t, svc)
	t.Cleanup(stop)
	srv := httptest.NewServer(mountInProcess(t, svc))
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}

	resp, body := host.newClient().do("POST", "/auth/register", `{"email":"stranger@example.com","password":"`+linkPassword+`","display_name":"S"}`, nil)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), `"code":"registration_invitation_required"`) {
		t.Fatalf("uninvited register = %d %s", resp.StatusCode, body)
	}
	if _, err := svc.Invitations.Create(context.Background(), invitations.CreateInput{
		ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "member",
		Identifier: "invitee@example.com", IdentifierKind: "email", InvitedBy: "admin", AutoAccept: true,
	}); err != nil {
		t.Fatal(err)
	}
	member := host.signUp("invitee@example.com")
	if got := granter.grants(); len(got) != 1 || !strings.HasPrefix(got[0], demoResourceType+":"+demoResourceID+"#member@") {
		t.Fatalf("grants after the invitee verified = %v, want the invitation granted", got)
	}
	// An existing account's magic link needs no invitation once it is accepted.
	before := sender.count()
	member.startMagicLink(t, "invitee@example.com")
	token := host.awaitMagicToken(t, before, "invitee@example.com")
	if resp, body := host.newClient().do("POST", "/auth/passwordless/redeem", `{"token":"`+token+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the member's magic link = %d %s, want 200", resp.StatusCode, body)
	}
}
