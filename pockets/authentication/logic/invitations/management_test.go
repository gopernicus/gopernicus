package invitations

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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

// claimSeeded seeds a pending invitation and claims it for user-9 directly, so
// the row is accepting with no grant attempted.
func claimSeeded(t *testing.T, repo *fakeInvRepo) Invitation {
	t.Helper()
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	claimed, err := repo.ClaimAcceptance(t.Context(), inv.ID, Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "user-9", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}

func TestCancelAcceptingTwiceIsIdempotent(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	svc := newSvc(t, repo, ledger, constructorConfig{Fencer: ledger})
	inv := claimSeeded(t, repo)
	p := prepareManagement(t, svc, inv.ID)
	for i := range 2 {
		if err := svc.Cancel(t.Context(), p); err != nil {
			t.Fatalf("cancel %d=%v", i, err)
		}
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if outcome, commits, fences := ledger.state(inv.ID); got.Status != StatusCancelled || outcome != "fenced" || commits != 0 || fences != 2 {
		t.Fatalf("invitation=%+v outcome=%q commits=%d fences=%d", got, outcome, commits, fences)
	}
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); !errors.Is(err, ErrNotPending) {
		t.Fatalf("cancel of a cancelled row=%v", err)
	}
}

func TestResendAfterFencedCancelNotPending(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	mailer := &recordingMailer{}
	svc := newSvc(t, repo, ledger, constructorConfig{Mailer: mailer, Fencer: ledger})
	inv := claimSeeded(t, repo)
	stale := prepareManagement(t, svc, inv.ID)
	if err := svc.Cancel(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	for _, p := range []PreparedManagement{stale, prepareManagement(t, svc, inv.ID)} {
		if _, err := svc.Resend(t.Context(), p, ""); !errors.Is(err, ErrNotPending) {
			t.Fatalf("resend after fenced cancel=%v", err)
		}
	}
	if got, _ := repo.Get(t.Context(), inv.ID); got.Status != StatusCancelled || got.TokenHash != inv.TokenHash || len(mailer.sent) != 0 {
		t.Fatalf("resend changed a settled row: %+v notices=%d", got, len(mailer.sent))
	}
}

// TestCancelAcceptingFailsClosed covers every accepting outcome that must leave
// the claim untouched: no Fencer, a Fencer error, and an unknown outcome.
func TestCancelAcceptingFailsClosed(t *testing.T) {
	ledgerDown := errors.New("ledger down")
	for _, tc := range []struct {
		name   string
		fencer *fenceLedger
		want   error
	}{
		{"no fencer", nil, ErrNotPending},
		{"fence error", &fenceLedger{fenceErr: ledgerDown}, ledgerDown},
		{"unknown outcome", &fenceLedger{outcome: "maybe"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeInvRepo()
			events := &fakeSecurityEvents{}
			d := constructorConfig{SecurityEvents: events}
			if tc.fencer != nil {
				d.Fencer = tc.fencer
			}
			svc := newSvc(t, repo, &fakeGranter{}, d)
			inv := claimSeeded(t, repo)
			err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID))
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("cancel=%v, want %v", err, tc.want)
			}
			if got, _ := repo.Get(t.Context(), inv.ID); got.Status != StatusAccepting || len(events.created) != 0 {
				t.Fatalf("claim changed: %+v events=%+v", got, events.created)
			}
		})
	}
}

// completedBeforeCancel finalizes the claim before cancelling it, modelling a
// completion that won despite the fence (the degraded superseded mapping).
type completedBeforeCancel struct{ InvitationRepository }

func (r completedBeforeCancel) CancelAcceptance(ctx context.Context, id string, a Acceptance) (Invitation, error) {
	if _, err := r.InvitationRepository.CompleteAcceptance(ctx, id, a); err != nil {
		return Invitation{}, err
	}
	return r.InvitationRepository.CancelAcceptance(ctx, id, a)
}

func TestCancelFencedButAcceptedReturnsAlreadyGranted(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	svc := newSvc(t, repo, ledger, constructorConfig{Fencer: ledger})
	svc.invitations = completedBeforeCancel{repo}
	inv := claimSeeded(t, repo)
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); !errors.Is(err, ErrAlreadyGranted) {
		t.Fatalf("cancel=%v", err)
	}
	if got, _ := repo.Get(t.Context(), inv.ID); got.Status != StatusAccepted {
		t.Fatalf("invitation=%+v", got)
	}
}

// failCancelOnce fails CancelAcceptance transiently while remaining > 0.
type failCancelOnce struct {
	InvitationRepository
	remaining atomic.Int32
}

func (r *failCancelOnce) CancelAcceptance(ctx context.Context, id string, a Acceptance) (Invitation, error) {
	if r.remaining.Add(-1) >= 0 {
		return Invitation{}, errors.New("cancellation unavailable")
	}
	return r.InvitationRepository.CancelAcceptance(ctx, id, a)
}

// fencedButStillAccepting fences an accepting claim whose CancelAcceptance then
// fails transiently: the fence holds, the row is still accepting.
func fencedButStillAccepting(t *testing.T) (*Service, *fakeInvRepo, *fenceLedger, Invitation) {
	t.Helper()
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	svc := newSvc(t, repo, ledger, constructorConfig{Fencer: ledger})
	failing := &failCancelOnce{InvitationRepository: repo}
	failing.remaining.Store(1)
	svc.invitations = failing
	inv := claimSeeded(t, repo)
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); err == nil || errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("cancel with a failing store=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if outcome, _, _ := ledger.state(inv.ID); got.Status != StatusAccepting || outcome != "fenced" {
		t.Fatalf("after failed cancel: invitation=%+v outcome=%q", got, outcome)
	}
	return svc, repo, ledger, inv
}

func TestCancelAcceptingHealsByRetriedCancel(t *testing.T) {
	svc, repo, ledger, inv := fencedButStillAccepting(t)
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); err != nil {
		t.Fatalf("retried cancel=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if _, commits, _ := ledger.state(inv.ID); got.Status != StatusCancelled || commits != 0 {
		t.Fatalf("invitation=%+v commits=%d", got, commits)
	}
}

func TestCancelAcceptingHealsByFencedAcceptRetry(t *testing.T) {
	svc, repo, ledger, inv := fencedButStillAccepting(t)
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"}); !errors.Is(err, ErrGrantFenced) {
		t.Fatalf("accept retry=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if _, commits, _ := ledger.state(inv.ID); got.Status != StatusCancelled || commits != 0 {
		t.Fatalf("invitation=%+v commits=%d", got, commits)
	}
}

func TestCancelCommittedHealsAfterCompletionFailure(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	svc := newSvc(t, repo, ledger, constructorConfig{Fencer: ledger})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	failing := &failFinalization{InvitationRepository: repo}
	failing.remaining.Store(1)
	svc.invitations = failing
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"}); err == nil {
		t.Fatal("expected injected completion failure")
	}
	failing.remaining.Store(1)
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); err == nil || errors.Is(err, ErrAlreadyGranted) {
		t.Fatalf("cancel with a failing completion=%v", err)
	}
	if got, _ := repo.Get(t.Context(), inv.ID); got.Status != StatusAccepting {
		t.Fatalf("failed completion changed the claim: %+v", got)
	}
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); !errors.Is(err, ErrAlreadyGranted) {
		t.Fatalf("retried cancel=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if _, commits, _ := ledger.state(inv.ID); got.Status != StatusAccepted || commits != 1 {
		t.Fatalf("invitation=%+v commits=%d", got, commits)
	}
}

func TestAcceptFencedBranchCancelFailureStaysRetryable(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, ledger, constructorConfig{SecurityEvents: events, Fencer: ledger})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	if _, err := ledger.Fence(t.Context(), FenceInput{OperationID: inv.ID}); err != nil {
		t.Fatal(err)
	}
	failing := &failCancelOnce{InvitationRepository: repo}
	failing.remaining.Store(1)
	svc.invitations = failing
	input := AcceptInput{Token: "secret", SubjectID: "user-9"}
	if _, err := svc.Accept(t.Context(), input); err == nil || errors.Is(err, ErrGrantFenced) {
		t.Fatalf("accept with a failing cancel=%v", err)
	}
	if got, _ := repo.Get(t.Context(), inv.ID); got.Status != StatusAccepting || len(events.created) != 0 {
		t.Fatalf("failed cancel: invitation=%+v events=%+v", got, events.created)
	}
	if _, err := svc.Accept(t.Context(), input); !errors.Is(err, ErrGrantFenced) {
		t.Fatalf("accept retry=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if _, commits, _ := ledger.state(inv.ID); got.Status != StatusCancelled || commits != 0 {
		t.Fatalf("invitation=%+v commits=%d", got, commits)
	}
}

func TestNewRejectsTypedNilFencer(t *testing.T) {
	if _, err := New(newFakeInvRepo(), &fakeGranter{}, WithFencer((*fenceLedger)(nil))); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed-nil Fencer=%v", err)
	}
}

func TestListActiveByResource(t *testing.T) {
	repo := newFakeInvRepo()
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{})
	pending := seedInvite(t, repo, "project", "p", "member", "a@x.com", "owner", "secret-a", false, time.Now().Add(-time.Hour))
	declined := seedInvite(t, repo, "project", "p", "member", "b@x.com", "owner", "secret-b", false, time.Now().Add(time.Hour))
	if err := svc.Decline(t.Context(), declined.ID, "secret-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListActiveByResource(t.Context(), ActiveListInput{ResourceType: "project"}, list.Request{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("blank resource id=%v", err)
	}
	page, err := svc.ListActiveByResource(t.Context(), ActiveListInput{ResourceType: "project", ResourceID: "p"}, list.Request{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != pending.ID {
		t.Fatalf("active page=%+v %v", page, err)
	}
}
