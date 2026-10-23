package invitations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// auditKeyer returns the bundled HMAC identifier keyer under a fixed test key.
func auditKeyer(t *testing.T) *protection.HMACIdentifierKeyer {
	t.Helper()
	keyer, err := protection.NewHMACIdentifierKeyer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewHMACIdentifierKeyer: %v", err)
	}
	return keyer
}

// assertNoAddressInDetails fails when any recorded row carries an `identifier`
// Details key, or a Details value holding an `@` or one of addresses (AUDIT-054).
func assertNoAddressInDetails(t *testing.T, events *fakeSecurityEvents, addresses ...string) {
	t.Helper()
	for _, evt := range events.created {
		if _, ok := evt.Details["identifier"]; ok {
			t.Errorf("%s/%s carries Details[identifier]", evt.EventType, evt.EventStatus)
		}
		for key, value := range evt.Details {
			for _, address := range append(addresses, "@") {
				if strings.Contains(fmt.Sprint(value), address) {
					t.Errorf("%s/%s Details[%q] carries the invitee address", evt.EventType, evt.EventStatus, key)
				}
			}
		}
	}
}

// requireDigestRow asserts events holds a row of eventType and status whose
// Details carry identifier_kind kind and identifier_digest want.
func requireDigestRow(t *testing.T, events *fakeSecurityEvents, eventType, status, kind, want string) {
	t.Helper()
	for _, evt := range events.created {
		if evt.EventType == eventType && evt.EventStatus == status {
			if evt.Details["identifier_kind"] != kind || evt.Details["identifier_digest"] != want {
				t.Fatalf("%s/%s Details = %+v, want identifier_kind %s and identifier_digest %s", eventType, status, evt.Details, kind, want)
			}
			return
		}
	}
	t.Fatalf("no %s/%s row recorded", eventType, status)
}

// TestInvitationRowsCarryIdentifierDigest pins AUDIT-054 for invitation rows:
// invitation_created (create and resend) and invitation_granted (accept and
// direct-add) carry identifier_kind + identifier_digest = IdentifierKey(kind,
// normalized), and no row carries the invitee address.
func TestInvitationRowsCarryIdentifierDigest(t *testing.T) {
	ctx := context.Background()
	keyer := auditKeyer(t)

	t.Run("create and resend", func(t *testing.T) {
		repo := newFakeInvRepo()
		events := &fakeSecurityEvents{}
		svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{SecurityEvents: events, IdentifierKeyer: keyer})
		res, err := svc.Create(ctx, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "  New-0451@X.com ", InvitedBy: "inviter"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		want := keyer.IdentifierKey(sdk.AddressKindEmail, "new-0451@x.com")
		requireDigestRow(t, events, securityevent.TypeInvitationCreated, securityevent.StatusSuccess, sdk.AddressKindEmail, want)
		assertNoAddressInDetails(t, events, "new-0451")
		events.created = nil
		if _, err := svc.Resend(ctx, prepareManagement(t, svc, res.Invitation.ID), ""); err != nil {
			t.Fatalf("Resend: %v", err)
		}
		requireDigestRow(t, events, securityevent.TypeInvitationCreated, securityevent.StatusSuccess, sdk.AddressKindEmail, want)
		assertNoAddressInDetails(t, events, "new-0451")
	})

	t.Run("accept", func(t *testing.T) {
		repo := newFakeInvRepo()
		events := &fakeSecurityEvents{}
		svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{SecurityEvents: events, IdentifierKeyer: keyer})
		seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "digest-token-0451", false, time.Now().Add(time.Hour))
		if _, err := svc.Accept(ctx, AcceptInput{Token: "digest-token-0451", SubjectType: "user", SubjectID: "user-9"}); err != nil {
			t.Fatalf("Accept: %v", err)
		}
		requireDigestRow(t, events, securityevent.TypeInvitationGranted, securityevent.StatusSuccess, sdk.AddressKindEmail,
			keyer.IdentifierKey(sdk.AddressKindEmail, "invitee@x.com"))
		assertNoAddressInDetails(t, events, "invitee", "digest-token-0451")
	})

	t.Run("direct add", func(t *testing.T) {
		events := &fakeSecurityEvents{}
		svc := newSvc(t, newFakeInvRepo(), &fakeGranter{}, constructorConfig{SecurityEvents: events, IdentifierKeyer: keyer,
			UserLookup: func(context.Context, string) (string, bool, error) { return "user-known", true, nil }})
		if _, err := svc.Create(ctx, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "Known@X.com", InvitedBy: "inviter", AutoAccept: true}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		requireDigestRow(t, events, securityevent.TypeInvitationGranted, securityevent.StatusSuccess, sdk.AddressKindEmail,
			keyer.IdentifierKey(sdk.AddressKindEmail, "known@x.com"))
		assertNoAddressInDetails(t, events, "known", "Known")
	})

	t.Run("phone kind", func(t *testing.T) {
		events := &fakeSecurityEvents{}
		svc := newSvc(t, newFakeInvRepo(), &fakeGranter{}, constructorConfig{SecurityEvents: events, IdentifierKeyer: keyer,
			BodySenders: map[string]delivery.BodySender{sdk.AddressKindPhone: &fakeNotifier{}}})
		if _, err := svc.Create(ctx, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "+1 555 0100", IdentifierKind: sdk.AddressKindPhone, InvitedBy: "inviter"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		requireDigestRow(t, events, securityevent.TypeInvitationCreated, securityevent.StatusSuccess, sdk.AddressKindPhone,
			keyer.IdentifierKey(sdk.AddressKindPhone, "+15550100"))
		assertNoAddressInDetails(t, events, "+15550100", "5550100")
	})
}

// TestInvitationDigestFallback pins the development fallback with no keyer
// wired: SHA-256 of kind + ":" + normalized, never the address.
func TestInvitationDigestFallback(t *testing.T) {
	events := &fakeSecurityEvents{}
	svc := newSvc(t, newFakeInvRepo(), &fakeGranter{}, constructorConfig{SecurityEvents: events})
	if _, err := svc.Create(context.Background(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "Fallback@X.com", InvitedBy: "inviter"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	sum := sha256.Sum256([]byte("email:fallback@x.com"))
	requireDigestRow(t, events, securityevent.TypeInvitationCreated, securityevent.StatusSuccess, sdk.AddressKindEmail, hex.EncodeToString(sum[:]))
	assertNoAddressInDetails(t, events, "fallback")
}

func TestWithIdentifierKeyer(t *testing.T) {
	keyer := auditKeyer(t)
	svc, err := New(newFakeInvRepo(), &fakeGranter{}, WithIdentifierKeyer(keyer))
	if err != nil {
		t.Fatal(err)
	}
	if svc.identifierKeyer != keyer {
		t.Fatal("WithIdentifierKeyer did not become the service's keyer")
	}
	if _, err := New(newFakeInvRepo(), &fakeGranter{}, WithIdentifierKeyer((*protection.HMACIdentifierKeyer)(nil))); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed-nil keyer: %v, want ErrInvalidInput", err)
	}
}
