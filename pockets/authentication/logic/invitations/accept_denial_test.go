package invitations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// requireAcceptDenied asserts the last row is an invitation_granted failure for
// subjectID with reason, carrying none of secrets in Details.
func requireAcceptDenied(t *testing.T, events *fakeSecurityEvents, subjectID, reason string, secrets ...string) securityevent.SecurityEvent {
	t.Helper()
	if len(events.created) == 0 {
		t.Fatal("no row recorded")
	}
	evt := events.created[len(events.created)-1]
	if evt.EventType != securityevent.TypeInvitationGranted || evt.EventStatus != securityevent.StatusFailure ||
		evt.Details["reason"] != reason || evt.UserID != subjectID {
		t.Fatalf("row = %+v, want invitation_granted failure reason=%s user=%s", evt, reason, subjectID)
	}
	for key, value := range evt.Details {
		for _, secret := range secrets {
			if strings.Contains(fmt.Sprint(value), secret) {
				t.Fatalf("Details[%q] carries an attempted secret", key)
			}
		}
	}
	return evt
}

func TestAcceptRecordsWrongToken(t *testing.T) {
	for name, token := range map[string]string{"unknown": "wrong-invite-token-0451", "expired": "expired-invite-token-0451", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeInvRepo()
			events := &fakeSecurityEvents{}
			svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{SecurityEvents: events})
			seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "expired-invite-token-0451", false, time.Now().Add(-time.Hour))
			if _, err := svc.Accept(context.Background(), AcceptInput{Token: token, SubjectType: "user", SubjectID: "user-9"}); err == nil {
				t.Fatal("Accept succeeded with a wrong token")
			}
			evt := requireAcceptDenied(t, events, "user-9", securityevent.ReasonInvalidToken, "invitee@x.com")
			if token != "" {
				requireAcceptDenied(t, events, "user-9", securityevent.ReasonInvalidToken, token)
			}
			if len(evt.Details) != 1 {
				t.Fatalf("a token that resolved no invitation carries %+v", evt.Details)
			}
		})
	}
}

func TestAcceptRecordsIdentifierMismatch(t *testing.T) {
	repo := newFakeInvRepo()
	events := &fakeSecurityEvents{}
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, constructorConfig{SecurityEvents: events,
		VerifiedIdentifiers: verifiedAs(sdk.AddressKindEmail, "someone-else@x.com")})
	seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "mismatch-token-0451", false, time.Now().Add(time.Hour))

	_, err := svc.Accept(context.Background(), AcceptInput{Token: "mismatch-token-0451", SubjectType: "user", SubjectID: "user-9"})
	if !errors.Is(err, ErrIdentifierMismatch) || !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("Accept = %v, want ErrIdentifierMismatch", err)
	}
	evt := requireAcceptDenied(t, events, "user-9", securityevent.ReasonPolicy, "mismatch-token-0451", "invitee@x.com", "someone-else@x.com")
	if evt.Details["resource_type"] != "project" || evt.Details["resource_id"] != "p1" || evt.Details["relation"] != "member" ||
		evt.Details["identifier_kind"] != sdk.AddressKindEmail {
		t.Fatalf("Details = %+v, want the resource coordinates and identifier kind", evt.Details)
	}
	if len(granter.calls) != 0 {
		t.Fatalf("Grant called on a mismatch: %+v", granter.calls)
	}
}
