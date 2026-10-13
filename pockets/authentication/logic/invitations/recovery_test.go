package invitations

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// ledgerGranter models the host's atomic operation binding independently of any
// authorization pocket implementation. Repeats never change current membership.
type ledgerGranter struct {
	mu         sync.Mutex
	operations map[string]grantCall
	current    grantCall
	commits    int
	mismatch   bool
	entered    chan struct{}
	release    chan struct{}
}

func (g *ledgerGranter) Grant(_ context.Context, in GrantInput) error {
	if g.entered != nil {
		g.entered <- struct{}{}
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	requested := grantCall{in.ResourceType, in.ResourceID, in.Relation, in.SubjectType, in.SubjectID}
	if recorded, ok := g.operations[in.OperationID]; ok {
		if g.mismatch || recorded != requested || g.current != requested {
			return fmt.Errorf("recorded operation no longer matches: %w", ErrGrantSuperseded)
		}
		return nil
	}
	if g.operations == nil {
		g.operations = map[string]grantCall{}
	}
	g.operations[in.OperationID] = requested
	g.current = requested
	g.commits++
	return nil
}

func TestAcceptLedgerRecoveryAfterCompletionFailure(t *testing.T) {
	for _, change := range []string{"none", "revoke", "replace", "mismatch"} {
		t.Run(change, func(t *testing.T) {
			repo := newFakeInvRepo()
			granter := &ledgerGranter{}
			mailer := &recordingMailer{}
			events := &fakeSecurityEvents{}
			svc := newSvc(t, repo, granter, constructorConfig{Mailer: mailer, SecurityEvents: events})
			inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
			failing := &failFinalization{InvitationRepository: repo}
			failing.remaining.Store(1)
			svc.invitations = failing
			input := AcceptInput{Token: "secret", SubjectID: "user-9"}
			if _, err := svc.Accept(t.Context(), input); err == nil {
				t.Fatal("expected injected completion failure")
			}
			got, _ := repo.Get(t.Context(), inv.ID)
			if got.Status != StatusAccepting || granter.commits != 1 || granter.current.relation != "member" || len(mailer.sent) != 0 {
				t.Fatalf("after failure: invitation=%+v grant=%+v notices=%d", got, granter, len(mailer.sent))
			}
			switch change {
			case "revoke":
				granter.current = grantCall{}
			case "replace":
				granter.current.relation = "viewer"
			case "mismatch":
				granter.mismatch = true
			}
			current := granter.current
			if change == "revoke" {
				// Superseded grants still require durable invitation finalization.
				failing.remaining.Store(1)
				if _, err := svc.Accept(t.Context(), input); err == nil || errors.Is(err, ErrGrantSuperseded) {
					t.Fatalf("superseded completion failure: %v", err)
				}
				got, _ := repo.Get(t.Context(), inv.ID)
				if got.Status != StatusAccepting || len(events.created) != 0 || len(mailer.sent) != 0 || granter.current != current {
					t.Fatalf("failed completion finalized or notified: invitation=%+v events=%+v", got, events.created)
				}
			}
			// A fresh service resumes the durable claim using the same operation ID.
			svc = newSvc(t, repo, granter, constructorConfig{Mailer: mailer, SecurityEvents: events})
			_, err := svc.Accept(t.Context(), input)
			if change == "none" {
				if err != nil || len(mailer.sent) != 1 || !events.grantSuccessRecorded() {
					t.Fatalf("current replay: err=%v notices=%d events=%+v", err, len(mailer.sent), events.created)
				}
			} else {
				if !errors.Is(err, ErrGrantSuperseded) || !errors.Is(err, sdk.ErrConflict) || len(mailer.sent) != 0 || events.grantSuccessRecorded() {
					t.Fatalf("superseded replay: err=%v notices=%d events=%+v", err, len(mailer.sent), events.created)
				}
				if len(events.created) != 1 || events.created[0].EventType != securityevent.TypeInvitationGranted || events.created[0].EventStatus != securityevent.StatusBlocked || events.created[0].Details["reason"] != "superseded" {
					t.Fatalf("superseded event=%+v", events.created)
				}
			}
			got, _ = repo.Get(t.Context(), inv.ID)
			if got.Status != StatusAccepted || got.AcceptedAt.IsZero() || got.Active() || granter.commits != 1 || granter.current != current {
				t.Fatalf("replay changed access or failed to finalize: invitation=%+v grant=%+v", got, granter)
			}
			// An accepted token keeps its existing success short-circuit.
			if _, err := svc.Accept(t.Context(), input); err != nil || granter.commits != 1 || granter.current != current {
				t.Fatalf("accepted replay: %v", err)
			}
		})
	}
}

func TestConcurrentAcceptingRetriesCommitOneLedgerGrant(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &ledgerGranter{entered: make(chan struct{}, 2), release: make(chan struct{})}
	svc := newSvc(t, repo, granter, constructorConfig{})
	// Delivery is independently deduplicated; this test exercises only claims/grants.
	svc.queue = nil
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	if _, err := repo.ClaimAcceptance(t.Context(), inv.ID, Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "user-9", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"})
			done <- err
		}()
	}
	for range 2 {
		select {
		case <-granter.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent retries did not both reach the Granter")
		}
	}
	close(granter.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if got.Status != StatusAccepted || granter.commits != 1 || len(granter.operations) != 1 || granter.current.relation != "member" {
		t.Fatalf("invitation=%+v grant=%+v", got, granter)
	}
}

func TestResolveInvitationsLedgerSupersededRecovery(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &ledgerGranter{}
	mailer := &recordingMailer{}
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, granter, constructorConfig{Mailer: mailer, SecurityEvents: events})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", true, time.Now().Add(time.Hour))
	failing := &failFinalization{InvitationRepository: repo}
	failing.remaining.Store(1)
	svc.invitations = failing
	if n, err := svc.ResolveInvitations(t.Context(), "invitee@x.com", "user", "user-9"); err != nil || n != 0 || granter.commits != 1 {
		t.Fatalf("initial resolve: %d, %v", n, err)
	}
	granter.current = grantCall{}
	if n, err := svc.ResolveInvitations(t.Context(), "invitee@x.com", "user", "user-9"); err != nil || n != 0 {
		t.Fatalf("superseded resolve: %d, %v", n, err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if got.Status != StatusAccepted || granter.commits != 1 || granter.current != (grantCall{}) || len(mailer.sent) != 0 || len(events.created) != 1 || events.created[0].EventStatus != securityevent.StatusBlocked {
		t.Fatalf("resolved invitation=%+v grant=%+v notices=%d events=%+v", got, granter, len(mailer.sent), events.created)
	}
	if n, err := svc.ResolveInvitations(t.Context(), "invitee@x.com", "user", "user-9"); err != nil || n != 0 {
		t.Fatalf("terminal resolve: %d, %v", n, err)
	}
}

func TestConcurrentAcceptingRetriesRaceRevocationOrReplacement(t *testing.T) {
	for _, change := range []string{"revoke", "replace"} {
		t.Run(change, func(t *testing.T) {
			repo := newFakeInvRepo()
			granter := &ledgerGranter{}
			initial := newSvc(t, repo, granter, constructorConfig{})
			inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
			failing := &failFinalization{InvitationRepository: repo}
			failing.remaining.Store(1)
			initial.invitations = failing
			input := AcceptInput{Token: "secret", SubjectID: "user-9"}
			if _, err := initial.Accept(t.Context(), input); err == nil {
				t.Fatal("expected injected completion failure")
			}

			// Hold a successful replay after its serialized current-state snapshot.
			// A second accepting retry then observes a manager's intervening write.
			snapshotted := make(chan struct{})
			release := make(chan struct{})
			currentMailer := &recordingMailer{}
			currentEvents := &fakeSecurityEvents{}
			currentSvc := newSvc(t, repo, grantFunc(func(ctx context.Context, in GrantInput) error {
				err := granter.Grant(ctx, in)
				close(snapshotted)
				<-release
				return err
			}), constructorConfig{Mailer: currentMailer, SecurityEvents: currentEvents})
			currentDone := make(chan error, 1)
			go func() {
				_, err := currentSvc.Accept(t.Context(), input)
				currentDone <- err
			}()
			select {
			case <-snapshotted:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("first retry did not snapshot the current grant")
			}
			granter.mu.Lock()
			if change == "revoke" {
				granter.current = grantCall{}
			} else {
				granter.current.relation = "viewer"
			}
			changed := granter.current
			granter.mu.Unlock()

			supersededMailer := &recordingMailer{}
			supersededEvents := &fakeSecurityEvents{}
			supersededSvc := newSvc(t, repo, granter, constructorConfig{Mailer: supersededMailer, SecurityEvents: supersededEvents})
			_, supersededErr := supersededSvc.Accept(t.Context(), input)
			close(release)
			currentErr := <-currentDone
			if currentErr != nil || !errors.Is(supersededErr, ErrGrantSuperseded) {
				t.Fatalf("concurrent results: current=%v superseded=%v", currentErr, supersededErr)
			}
			if len(currentMailer.sent) != 1 || !currentEvents.grantSuccessRecorded() {
				t.Fatalf("successful snapshot notice/events: notices=%d events=%+v", len(currentMailer.sent), currentEvents.created)
			}
			if len(supersededMailer.sent) != 0 || len(supersededEvents.created) != 1 || supersededEvents.created[0].EventStatus != securityevent.StatusBlocked || supersededEvents.created[0].Details["reason"] != "superseded" {
				t.Fatalf("superseded branch notice/events: notices=%d events=%+v", len(supersededMailer.sent), supersededEvents.created)
			}
			got, _ := repo.Get(t.Context(), inv.ID)
			if got.Status != StatusAccepted || got.Active() || got.AcceptedAt.IsZero() || granter.commits != 1 || granter.current != changed {
				t.Fatalf("concurrent recovery changed access: invitation=%+v grant=%+v", got, granter)
			}
		})
	}
}

// fenceLedger models a host operation ledger shared by its Granter and Fencer:
// both serialize on one lock, a fenced ID replays as ErrGrantFenced and never
// commits, and an applied ID reports committed to a later fence.
type fenceLedger struct {
	mu       sync.Mutex
	outcomes map[string]string
	commits  int
	fences   int
	fenceErr error
	outcome  FenceOutcome // reported instead of the ledger's answer when set
	entered  chan struct{}
	release  chan struct{}
}

func (l *fenceLedger) Grant(_ context.Context, in GrantInput) error {
	if l.entered != nil {
		l.entered <- struct{}{}
		<-l.release
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.outcomes[in.OperationID] {
	case "fenced":
		return fmt.Errorf("replayed fenced operation: %w", ErrGrantFenced)
	case "applied":
		return nil
	}
	if l.outcomes == nil {
		l.outcomes = map[string]string{}
	}
	l.outcomes[in.OperationID] = "applied"
	l.commits++
	return nil
}

func (l *fenceLedger) Fence(_ context.Context, in FenceInput) (FenceOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fences++
	if l.fenceErr != nil {
		return "", l.fenceErr
	}
	if l.outcome != "" {
		return l.outcome, nil
	}
	if l.outcomes[in.OperationID] == "applied" {
		return FenceOutcomeCommitted, nil
	}
	if l.outcomes == nil {
		l.outcomes = map[string]string{}
	}
	l.outcomes[in.OperationID] = "fenced"
	return FenceOutcomeFenced, nil
}

func (l *fenceLedger) state(id string) (outcome string, commits, fences int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.outcomes[id], l.commits, l.fences
}

// pausedAccept starts Accept against a blocking ledger and waits until the
// grant is in flight, so the row is accepting with no committed operation.
func pausedAccept(t *testing.T, svc *Service, ledger *fenceLedger) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"})
		done <- err
	}()
	select {
	case <-ledger.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("acceptance did not reach the Granter")
	}
	return done
}

func TestCancelPendingThenAcceptRejects(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	svc := newSvc(t, repo, ledger, constructorConfig{Fencer: ledger})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"}); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("accept after cancel=%v", err)
	}
	if outcome, commits, fences := ledger.state(inv.ID); outcome != "" || commits != 0 || fences != 0 {
		t.Fatalf("pending cancel touched the ledger: outcome=%q commits=%d fences=%d", outcome, commits, fences)
	}
}

func TestCancelAcceptingFencesPausedGrant(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{entered: make(chan struct{}, 1), release: make(chan struct{})}
	mailer := &recordingMailer{}
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, ledger, constructorConfig{Mailer: mailer, SecurityEvents: events, Fencer: ledger})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	done := pausedAccept(t, svc, ledger)

	if err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID)); err != nil {
		close(ledger.release)
		t.Fatalf("cancel accepting=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if got.Status != StatusCancelled || got.ResolvedSubjectID != "user-9" || !got.AcceptedAt.IsZero() {
		t.Fatalf("cancelled claim=%+v", got)
	}
	close(ledger.release)
	if err := <-done; !errors.Is(err, ErrGrantFenced) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("resumed grant=%v", err)
	}
	if outcome, commits, _ := ledger.state(inv.ID); outcome != "fenced" || commits != 0 {
		t.Fatalf("fenced grant committed: outcome=%q commits=%d", outcome, commits)
	}
	got, _ = repo.Get(t.Context(), inv.ID)
	if got.Status != StatusCancelled || len(mailer.sent) != 0 {
		t.Fatalf("after fenced grant: invitation=%+v notices=%d", got, len(mailer.sent))
	}
	if len(events.created) != 2 {
		t.Fatalf("events=%+v", events.created)
	}
	cancelled, granted := events.created[0], events.created[1]
	if cancelled.EventType != securityevent.TypeInvitationCancelled || cancelled.EventStatus != securityevent.StatusSuccess ||
		cancelled.Details["claim_status"] != StatusAccepting || cancelled.Details["fence"] != "fenced" {
		t.Fatalf("cancel event=%+v", cancelled)
	}
	if granted.EventType != securityevent.TypeInvitationGranted || granted.EventStatus != securityevent.StatusBlocked || granted.Details["reason"] != "fenced" {
		t.Fatalf("grant event=%+v", granted)
	}
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"}); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("accept after fenced cancel=%v", err)
	}
	if _, commits, _ := ledger.state(inv.ID); commits != 0 {
		t.Fatal("a retry granted after the fence")
	}
}

// TestCancelPendingSnapshotRacedByClaimFences prepares while pending; the claim
// lands before execution, so the cancel re-reads and settles the claim instead
// of answering a conflict.
func TestCancelPendingSnapshotRacedByClaimFences(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{entered: make(chan struct{}, 1), release: make(chan struct{})}
	svc := newSvc(t, repo, ledger, constructorConfig{Fencer: ledger})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	prepared := prepareManagement(t, svc, inv.ID)
	done := pausedAccept(t, svc, ledger)

	if err := svc.Cancel(t.Context(), prepared); err != nil {
		close(ledger.release)
		t.Fatalf("cancel of a raced pending snapshot=%v", err)
	}
	close(ledger.release)
	if err := <-done; !errors.Is(err, ErrGrantFenced) {
		t.Fatalf("resumed grant=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if outcome, commits, fences := ledger.state(inv.ID); got.Status != StatusCancelled || outcome != "fenced" || commits != 0 || fences != 1 {
		t.Fatalf("invitation=%+v outcome=%q commits=%d fences=%d", got, outcome, commits, fences)
	}
}

func TestCancelAcceptingAfterCommittedGrantReturnsAlreadyGranted(t *testing.T) {
	repo := newFakeInvRepo()
	ledger := &fenceLedger{}
	mailer := &recordingMailer{}
	events := &fakeSecurityEvents{}
	svc := newSvc(t, repo, ledger, constructorConfig{Mailer: mailer, SecurityEvents: events, Fencer: ledger})
	inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", false, time.Now().Add(time.Hour))
	failing := &failFinalization{InvitationRepository: repo}
	failing.remaining.Store(1)
	svc.invitations = failing
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"}); err == nil {
		t.Fatal("expected injected completion failure")
	}

	err := svc.Cancel(t.Context(), prepareManagement(t, svc, inv.ID))
	if !errors.Is(err, ErrAlreadyGranted) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("cancel after committed grant=%v", err)
	}
	got, _ := repo.Get(t.Context(), inv.ID)
	if got.Status != StatusAccepted || got.AcceptedAt.IsZero() || got.Active() {
		t.Fatalf("committed claim not finalized: %+v", got)
	}
	if _, commits, _ := ledger.state(inv.ID); commits != 1 || len(mailer.sent) != 0 || events.grantSuccessRecorded() {
		t.Fatalf("commits=%d notices=%d events=%+v", commits, len(mailer.sent), events.created)
	}
	if len(events.created) != 1 || events.created[0].EventType != securityevent.TypeInvitationCancelled ||
		events.created[0].EventStatus != securityevent.StatusBlocked || events.created[0].Details["reason"] != "already_granted" {
		t.Fatalf("events=%+v", events.created)
	}
	if _, err := svc.Accept(t.Context(), AcceptInput{Token: "secret", SubjectID: "user-9"}); err != nil || len(mailer.sent) != 0 {
		t.Fatalf("accepted replay=%v notices=%d", err, len(mailer.sent))
	}
}
