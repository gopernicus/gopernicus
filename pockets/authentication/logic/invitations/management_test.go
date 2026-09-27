package invitations

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

func prepareManagement(t *testing.T, svc *Service, id string) PreparedManagement {
	t.Helper()
	p, err := svc.PrepareManagement(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type managementReader struct {
	InvitationRepository
	reads int
}

func (r *managementReader) Get(ctx context.Context, id string) (Invitation, error) {
	r.reads++
	return r.InvitationRepository.Get(ctx, id)
}

func TestPreparedManagementReadsOnceAndPinsTarget(t *testing.T) {
	for _, resend := range []bool{false, true} {
		repo := newFakeInvRepo()
		reader := &managementReader{InvitationRepository: repo}
		svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{})
		svc.invitations = reader
		inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "issuer", "secret", false, time.Now().Add(time.Hour))
		p := prepareManagement(t, svc, inv.ID)
		if p.ID() != inv.ID || p.InvitedBy() != "issuer" || reader.reads != 1 {
			t.Fatalf("prepared=%+v reads=%d", p, reader.reads)
		}
		// No principal is required by the domain; inbound already owns admission.
		var err error
		if resend {
			_, err = svc.Resend(t.Context(), p, "")
		} else {
			err = svc.Cancel(t.Context(), p)
		}
		if err != nil || reader.reads != 1 {
			t.Fatalf("resend=%v err=%v reads=%d", resend, err, reader.reads)
		}
	}
}

func TestPreparedManagementRejectsZeroForeignCanceledAndStale(t *testing.T) {
	repo := newFakeInvRepo()
	mailer := &recordingMailer{}
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{Mailer: mailer})
	other := newSvc(t, repo, &fakeGranter{}, constructorConfig{})
	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "issuer", "secret", false, time.Now().Add(time.Hour))
	for _, p := range []PreparedManagement{{}, prepareManagement(t, other, inv.ID)} {
		if err := svc.Cancel(t.Context(), p); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("cancel zero/foreign=%v", err)
		}
		if _, err := svc.Resend(t.Context(), p, ""); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("resend zero/foreign=%v", err)
		}
	}
	p := prepareManagement(t, svc, inv.ID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.PrepareManagement(ctx, inv.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare=%v", err)
	}
	if err := svc.Cancel(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cancel=%v", err)
	}
	if _, err := svc.Resend(ctx, p, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resend=%v", err)
	}
	if len(mailer.sent) != 0 {
		t.Fatal("invalid management delivered")
	}
	if _, err := svc.Resend(t.Context(), p, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(t.Context(), p); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale cancel=%v", err)
	}
	if _, err := svc.Resend(t.Context(), p, ""); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale resend=%v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("stale command delivered: %d", len(mailer.sent))
	}
}

// TestManagementAuditAttributesActor pins D5: cancel/resend populate the canonical
// SecurityEvent.Actor from the attached principal, put only a user actor in
// UserID, record the original invited_by, and fall back to InvitedBy headless.
func TestManagementAuditAttributesActor(t *testing.T) {
	cases := []struct {
		name      string
		actor     sdk.Principal
		wantActor securityevent.Principal
		wantUser  string
	}{
		{"co-manager user", sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "co-manager"}, securityevent.Principal{Type: sdk.PrincipalTypeUser, ID: "co-manager"}, "co-manager"},
		{"service account", sdk.Principal{Type: sdk.PrincipalTypeServiceAccount, ID: "sa-1"}, securityevent.Principal{Type: sdk.PrincipalTypeServiceAccount, ID: "sa-1"}, ""},
		{"no actor", sdk.Principal{}, securityevent.Principal{}, "issuer"},
	}
	for _, tc := range cases {
		for _, resend := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/resend=%v", tc.name, resend), func(t *testing.T) {
				repo := newFakeInvRepo()
				events := &fakeSecurityEvents{}
				svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{SecurityEvents: events})
				inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "issuer", "secret", false, time.Now().Add(time.Hour))
				p := prepareManagement(t, svc, inv.ID).WithActor(tc.actor)
				wantType := securityevent.TypeInvitationCancelled
				var err error
				if resend {
					wantType = securityevent.TypeInvitationCreated
					_, err = svc.Resend(t.Context(), p, "")
				} else {
					err = svc.Cancel(t.Context(), p)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(events.created) != 1 {
					t.Fatalf("events=%+v", events.created)
				}
				evt := events.created[0]
				if evt.EventType != wantType || evt.Actor != tc.wantActor || evt.UserID != tc.wantUser || evt.Details["invited_by"] != "issuer" {
					t.Fatalf("event=%+v", evt)
				}
				if _, dup := evt.Details["actor_id"]; dup {
					t.Fatal("actor duplicated into Details")
				}
			})
		}
	}
}

// TestManagementActorLeavesOtherAttributionUnchanged proves create, grant, and
// decline rows keep their historical attribution with no Actor.
func TestManagementActorLeavesOtherAttributionUnchanged(t *testing.T) {
	repo := newFakeInvRepo()
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{SecurityEvents: events})
	_, err := svc.Create(t.Context(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "new@x.com", InvitedBy: "issuer"})
	if err != nil {
		t.Fatal(err)
	}
	seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "issuer", "secret-a", false, time.Now().Add(time.Hour))
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "invitee@x.com"}); err != nil {
		t.Fatal(err)
	}
	declined := seedInvite(t, repo, "project", "p1", "member", "sub@x.com", "issuer", "secret-b", false, time.Now().Add(time.Hour))
	if err := svc.Decline(t.Context(), declined.ID, "secret-b"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{securityevent.TypeInvitationCreated: "issuer", securityevent.TypeInvitationGranted: "user-9", securityevent.TypeInvitationDeclined: ""}
	for _, evt := range events.created {
		if evt.Actor != (securityevent.Principal{}) {
			t.Fatalf("%s gained an Actor: %+v", evt.EventType, evt)
		}
		if u, ok := want[evt.EventType]; ok && evt.UserID != u {
			t.Fatalf("%s UserID=%q want %q", evt.EventType, evt.UserID, u)
		}
		if _, ok := evt.Details["invited_by"]; ok {
			t.Fatalf("%s gained invited_by", evt.EventType)
		}
	}
	if len(events.created) < 3 {
		t.Fatalf("events=%d", len(events.created))
	}
}
