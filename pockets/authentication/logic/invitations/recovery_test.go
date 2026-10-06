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
