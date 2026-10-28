package invitations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

type grantFunc func(context.Context, GrantInput) error

func (f grantFunc) Grant(ctx context.Context, in GrantInput) error { return f(ctx, in) }

func TestAcceptanceRequiresVerifiedEmailIndependentlyOfInput(t *testing.T) {
	for _, lookup := range []IdentifierLister{nil, func(context.Context, string) ([]sdk.IdentityAddress, error) { return nil, sdk.ErrNotFound }, verifiedAs(sdk.AddressKindEmail, "other@example.com"), verifiedAs(sdk.AddressKindPhone, "invitee@x.com")} {
		t.Run("unproven", func(t *testing.T) {
			repo := newFakeInvRepo()
			g := &fakeGranter{}
			svc := newSvc(t, repo, g, constructorConfig{})
			svc.verifiedIdentifiers = lookup
			inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", true, time.Now().Add(time.Hour))
			_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret", SubjectID: "user-9", Identifier: "invitee@x.com"})
			if !errors.Is(err, ErrIdentifierMismatch) {
				t.Fatalf("accept: %v", err)
			}
			n, err := svc.ResolveInvitations(context.Background(), sdk.AddressKindEmail, "invitee@x.com", "user", "user-9")
			if err != nil || n != 0 || len(g.calls) != 0 {
				t.Fatalf("resolve=%d,%v grants=%d", n, err, len(g.calls))
			}
			got, _ := repo.Get(context.Background(), inv.ID)
			if got.Status != StatusPending {
				t.Fatalf("unproven email claimed: %+v", got)
			}
		})
	}
}

func TestClaimBlocksCancelResendAndAnotherSubjectWhileGrantRuns(t *testing.T) {
	repo := newFakeInvRepo()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	svc := newSvc(t, repo, grantFunc(func(context.Context, GrantInput) error { calls.Add(1); close(entered); <-release; return nil }), constructorConfig{})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	done := make(chan error, 1)
	go func() {
		_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret", SubjectID: "user-9"})
		done <- err
	}()
	<-entered
	if err := svc.Cancel(context.Background(), prepareManagement(t, svc, inv.ID)); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("cancel=%v", err)
	}
	if _, err := svc.Resend(context.Background(), prepareManagement(t, svc, inv.ID), ""); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("resend=%v", err)
	}
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret", SubjectID: "another"}); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("another subject=%v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("grant calls=%d", calls.Load())
	}
}

type staleTokenReader struct {
	InvitationRepository
	read   chan struct{}
	resume chan struct{}
}

func (r staleTokenReader) GetByTokenHash(ctx context.Context, hash string) (Invitation, error) {
	inv, err := r.InvitationRepository.GetByTokenHash(ctx, hash)
	close(r.read)
	<-r.resume
	return inv, err
}
func TestResendWinsAgainstAcceptanceOfPreviouslyReadToken(t *testing.T) {
	repo := newFakeInvRepo()
	g := &fakeGranter{}
	svc := newSvc(t, repo, g, constructorConfig{})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	reader := staleTokenReader{repo, make(chan struct{}), make(chan struct{})}
	svc.invitations = reader
	done := make(chan error, 1)
	go func() {
		_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret", SubjectID: "user-9"})
		done <- err
	}()
	<-reader.read
	if _, err := svc.Resend(context.Background(), prepareManagement(t, svc, inv.ID), ""); err != nil {
		t.Fatal(err)
	}
	close(reader.resume)
	if err := <-done; !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale acceptance=%v", err)
	}
	if len(g.calls) != 0 {
		t.Fatal("stale token reached grant")
	}
}

func TestInvitationCancelAcceptanceTransition(t *testing.T) {
	now := time.Now()
	pending, err := NewInvitation(sdk.IDGenerator{}, "project", "p", "member", "invitee@x.com", "", "owner", "hash", false, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	claim := Acceptance{TokenHash: "hash", SubjectType: "user", SubjectID: "user-9", Now: now.Add(time.Minute)}
	accepting, err := pending.ClaimAcceptance(claim)
	if err != nil {
		t.Fatal(err)
	}
	later := claim
	later.Now = pending.ExpiresAt.Add(time.Hour)
	cancelled, err := accepting.CancelAcceptance(later)
	if err != nil || cancelled.Status != StatusCancelled || cancelled.TokenHash != "hash" || cancelled.ResolvedSubjectID != "user-9" ||
		!cancelled.AcceptedAt.IsZero() || !cancelled.UpdatedAt.Equal(later.Now.UTC()) || cancelled.Active() {
		t.Fatalf("cancel past expiry=%+v %v", cancelled, err)
	}
	again := later
	again.Now = later.Now.Add(time.Hour)
	if repeat, err := cancelled.CancelAcceptance(again); err != nil || !repeat.UpdatedAt.Equal(cancelled.UpdatedAt) {
		t.Fatalf("repeat=%+v %v", repeat, err)
	}
	accepted, err := accepting.CompleteAcceptance(claim)
	if err != nil {
		t.Fatal(err)
	}
	unclaimed, err := pending.UpdateStatus(StatusUpdate{ExpectedTokenHash: "hash", Status: StatusCancelled, TokenHash: "hash", ExpiresAt: pending.ExpiresAt, ResolvedSubjectID: "user-9", UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	other := claim
	other.SubjectID = "other"
	for name, tc := range map[string]struct {
		inv   Invitation
		claim Acceptance
	}{
		"pending":                    {pending, claim},
		"accepted":                   {accepted, claim},
		"cancelled via UpdateStatus": {unclaimed, claim},
		"another subject":            {accepting, other},
	} {
		if _, err := tc.inv.CancelAcceptance(tc.claim); !errors.Is(err, sdk.ErrConflict) {
			t.Errorf("%s=%v", name, err)
		}
	}
	if _, err := accepting.CancelAcceptance(Acceptance{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("invalid claim=%v", err)
	}
}

type failFinalization struct {
	InvitationRepository
	remaining atomic.Int32
}

func (r *failFinalization) CompleteAcceptance(ctx context.Context, id string, a Acceptance) (Invitation, error) {
	if r.remaining.Add(-1) >= 0 {
		return Invitation{}, errors.New("finalization unavailable")
	}
	return r.InvitationRepository.CompleteAcceptance(ctx, id, a)
}

func TestClaimResumesAfterAmbiguousGrantOrFinalizationAndRestart(t *testing.T) {
	for _, failGrant := range []bool{true, false} {
		name := "finalization"
		if failGrant {
			name = "grant"
		}
		t.Run(name, func(t *testing.T) {
			repo := newFakeInvRepo()
			var mu sync.Mutex
			effects := map[string]bool{}
			var operations []string
			grant := grantFunc(func(_ context.Context, in GrantInput) error {
				mu.Lock()
				defer mu.Unlock()
				effects[in.OperationID] = true
				operations = append(operations, in.OperationID)
				if failGrant && len(operations) == 1 {
					return errors.New("response lost after host commit")
				}
				return nil
			})
			svc := newSvc(t, repo, grant, constructorConfig{})
			inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
			durable := &failFinalization{InvitationRepository: repo}
			if !failGrant {
				durable.remaining.Store(1)
			}
			svc.invitations = durable
			input := AcceptInput{Token: "secret", SubjectID: "user-9"}
			if _, err := svc.Accept(context.Background(), input); err == nil {
				t.Fatal("expected partial failure")
			}
			claimed, _ := repo.Get(context.Background(), inv.ID)
			if claimed.Status != StatusAccepting || claimed.ResolvedSubjectID != "user-9" {
				t.Fatalf("claim lost: %+v", claimed)
			}
			// A new service has no in-process state from the first attempt. Its clock is
			// beyond token expiry; the persisted claim supplies the original proof.
			resumed := newSvc(t, repo, grant, constructorConfig{Clock: func() time.Time { return inv.ExpiresAt.Add(time.Hour) }})
			resumed.invitations = durable
			resumed.verifiedIdentifiers = nil
			if _, err := resumed.Accept(context.Background(), input); err != nil {
				t.Fatalf("resume: %v", err)
			}
			accepted, _ := repo.Get(context.Background(), inv.ID)
			if accepted.Status != StatusAccepted {
				t.Fatalf("final state: %+v", accepted)
			}
			if _, err := resumed.Accept(context.Background(), input); err != nil {
				t.Fatalf("completed replay: %v", err)
			}
			if len(effects) != 1 || len(operations) != 2 || operations[0] != inv.ID || operations[1] != inv.ID {
				t.Fatalf("effects=%v operations=%v", effects, operations)
			}
			after, _ := repo.Get(context.Background(), inv.ID)
			if !after.AcceptedAt.Equal(accepted.AcceptedAt) {
				t.Fatal("replay changed acceptance timestamp")
			}
		})
	}
}

// TestDeclineByToken pins the token-only decline: a pending row declines with
// its invitation_declined row; unknown is ErrNotFound; expired unclaimed is
// ErrExpired; accepted, declined and accepting rows are ErrNotPending with no
// state change; and neither the token nor its hash reaches a log line or an
// event detail.
func TestDeclineByToken(t *testing.T) {
	var logs bytes.Buffer
	repo := newFakeInvRepo()
	events := &fakeSecurityEvents{}
	g := &fakeGranter{}
	svc := newSvc(t, repo, g, constructorConfig{SecurityEvents: events, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	ctx := t.Context()

	pending := seedInvite(t, repo, "project", "p", "member", "a@x.com", "owner", "tok-pending", false, time.Now().Add(time.Hour))
	if err := svc.DeclineByToken(ctx, "tok-pending"); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if got, _ := repo.Get(ctx, pending.ID); got.Status != StatusDeclined {
		t.Fatalf("pending not declined: %+v", got)
	}
	var declined []securityevent.SecurityEvent
	for _, evt := range events.created {
		if evt.EventType == securityevent.TypeInvitationDeclined {
			declined = append(declined, evt)
		}
	}
	if len(declined) != 1 || declined[0].Details["resource_id"] != "p" {
		t.Fatalf("declined rows=%+v", declined)
	}
	if err := svc.DeclineByToken(ctx, "tok-pending"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second decline=%v, want ErrNotPending", err)
	}

	for _, tok := range []string{"tok-unknown", ""} {
		if err := svc.DeclineByToken(ctx, tok); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("unknown %q=%v, want ErrNotFound", tok, err)
		}
	}

	expired := seedInvite(t, repo, "project", "p", "member", "b@x.com", "owner", "tok-expired", false, time.Now().Add(-time.Hour))
	if err := svc.DeclineByToken(ctx, "tok-expired"); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("expired=%v, want ErrExpired", err)
	}
	if got, _ := repo.Get(ctx, expired.ID); got.Status != StatusPending {
		t.Fatalf("expired row changed: %+v", got)
	}

	accepted := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "tok-accepted", false, time.Now().Add(time.Hour))
	if _, err := svc.Accept(ctx, AcceptInput{Token: "tok-accepted", SubjectID: "user-9"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeclineByToken(ctx, "tok-accepted"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("accepted=%v, want ErrNotPending", err)
	}
	if got, _ := repo.Get(ctx, accepted.ID); got.Status != StatusAccepted {
		t.Fatalf("accepted row changed: %+v", got)
	}

	claimed := seedInvite(t, repo, "project", "p2", "member", "sub@x.com", "owner", "tok-accepting", false, time.Now().Add(time.Hour))
	before, err := repo.ClaimAcceptance(ctx, claimed.ID, Acceptance{TokenHash: claimed.TokenHash, SubjectType: "user", SubjectID: "user-7", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeclineByToken(ctx, "tok-accepting"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("accepting=%v, want ErrNotPending", err)
	}
	if got, _ := repo.Get(ctx, claimed.ID); got.Status != StatusAccepting || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("accepting row changed: %+v", got)
	}
	n := 0
	for _, evt := range events.created {
		if evt.EventType == securityevent.TypeInvitationDeclined {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("declined rows=%d, want 1", n)
	}

	secrets := []string{"tok-pending", "tok-expired", "tok-accepted", "tok-accepting", "tok-unknown"}
	for _, sec := range secrets {
		for _, needle := range []string{sec, hashOf(t, sec)} {
			if strings.Contains(logs.String(), needle) {
				t.Fatalf("log carries %q", needle)
			}
			for _, evt := range events.created {
				if strings.Contains(fmt.Sprint(evt.Details), needle) {
					t.Fatalf("%s details carry %q", evt.EventType, needle)
				}
			}
		}
	}
}

// TestDeclineByIDKeepsDecliningExpired pins ruling R-8: the id route keeps its
// no-expiry behaviour while DeclineByToken surfaces ErrExpired.
func TestDeclineByIDKeepsDecliningExpired(t *testing.T) {
	repo := newFakeInvRepo()
	svc := newSvc(t, repo, &fakeGranter{}, constructorConfig{})
	inv := seedInvite(t, repo, "project", "p", "member", "a@x.com", "owner", "tok", false, time.Now().Add(-time.Hour))
	if err := svc.Decline(t.Context(), inv.ID, "tok"); err != nil {
		t.Fatalf("Decline(expired)=%v", err)
	}
	if got, _ := repo.Get(t.Context(), inv.ID); got.Status != StatusDeclined {
		t.Fatalf("status=%q", got.Status)
	}
}

// lostDeclineRace serves a pending row by token hash but loses the conditional
// write, as when an accept claims the row between the read and the decline.
type lostDeclineRace struct {
	*fakeInvRepo
}

func (r lostDeclineRace) UpdateStatus(context.Context, string, StatusUpdate) (Invitation, error) {
	return Invitation{}, sdk.ErrConflict
}

// TestDeclineByTokenLostRaceConflicts pins that a decline whose conditional
// write loses to a concurrent accept surfaces the conflict (409 class) and
// records no invitation_declined row.
func TestDeclineByTokenLostRaceConflicts(t *testing.T) {
	repo := newFakeInvRepo()
	events := &fakeSecurityEvents{}
	seedInvite(t, repo, "project", "p", "member", "a@x.com", "owner", "tok", false, time.Now().Add(time.Hour))
	d := constructorConfig{SecurityEvents: events, Invitations: lostDeclineRace{repo}, Granter: &fakeGranter{}, Mailer: &recordingMailer{}}
	svc := newService(d)
	err := svc.DeclineByToken(t.Context(), "tok")
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("lost race = %v, want sdk.ErrConflict", err)
	}
	for _, evt := range events.created {
		if evt.EventType == securityevent.TypeInvitationDeclined {
			t.Fatalf("declined row recorded on a lost race: %+v", evt)
		}
	}
}
