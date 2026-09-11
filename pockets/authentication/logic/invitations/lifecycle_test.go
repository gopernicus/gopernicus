package invitations

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

type grantFunc func(context.Context, GrantInput) error

func (f grantFunc) Grant(ctx context.Context, in GrantInput) error { return f(ctx, in) }

func TestAcceptanceRequiresVerifiedEmailIndependentlyOfInput(t *testing.T) {
	for _, lookup := range []IdentifierLookup{nil, func(context.Context, string, string) (string, error) { return "", sdk.ErrNotFound }, func(context.Context, string, string) (string, error) { return "other@example.com", nil }} {
		t.Run("unproven", func(t *testing.T) {
			repo := newFakeInvRepo()
			g := &fakeGranter{}
			svc := newSvc(t, repo, g, constructorConfig{})
			svc.callerIdentifiers = lookup
			inv := seedInvite(t, repo, "project", "p", "member", "invitee@x.com", "owner", "secret", true, time.Now().Add(time.Hour))
			_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret", SubjectID: "user-9", Identifier: "invitee@x.com"})
			if !errors.Is(err, ErrIdentifierMismatch) {
				t.Fatalf("accept: %v", err)
			}
			n, err := svc.ResolveInvitations(context.Background(), "invitee@x.com", "user", "user-9")
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
	if err := svc.Cancel(context.Background(), inv.ID, "owner"); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("cancel=%v", err)
	}
	if _, err := svc.Resend(context.Background(), inv.ID, "owner", ""); !errors.Is(err, sdk.ErrConflict) {
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
	if _, err := svc.Resend(context.Background(), inv.ID, "owner", ""); err != nil {
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
			resumed.callerIdentifiers = nil
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
