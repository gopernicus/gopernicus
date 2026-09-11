package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
)

func testInvitationAcceptanceClaim(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations
	inv := mustNewInvitation(t, "project", "p", "member", "claim@example.com", "owner", "claim-token", suiteBase)
	inv, err := repo.Create(ctx, inv)
	if err != nil {
		t.Fatal(err)
	}
	claim := invitations.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "claimant", Now: suiteBase.Add(time.Minute)}
	claimed, err := repo.ClaimAcceptance(ctx, inv.ID, claim)
	if err != nil || claimed.Status != invitations.StatusAccepting {
		t.Fatalf("claim=%+v %v", claimed, err)
	}
	duplicate := mustNewInvitation(t, "project", "p", "member", "claim@example.com", "owner", "another-token", suiteBase)
	if _, err := repo.Create(ctx, duplicate); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("claim released active tuple: %v", err)
	}
	for _, status := range []string{invitations.StatusPending, invitations.StatusDeclined, invitations.StatusCancelled} {
		_, err := repo.UpdateStatus(ctx, inv.ID, invitations.StatusUpdate{ExpectedTokenHash: inv.TokenHash, Status: status, TokenHash: "rotated", ExpiresAt: inv.ExpiresAt, UpdatedAt: claim.Now})
		if !errors.Is(err, sdk.ErrConflict) {
			t.Errorf("claimed transition to %s=%v", status, err)
		}
	}
	other := claim
	other.SubjectID = "other"
	if _, err := repo.ClaimAcceptance(ctx, inv.ID, other); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("different claimant=%v", err)
	}
	if _, err := repo.CompleteAcceptance(ctx, inv.ID, other); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("different finalizer=%v", err)
	}
	claim.Now = inv.ExpiresAt.Add(time.Hour)
	if got, err := repo.ClaimAcceptance(ctx, inv.ID, claim); err != nil || got.ResolvedSubjectID != "claimant" {
		t.Fatalf("resume beyond expiry=%+v %v", got, err)
	}
	accepted, err := repo.CompleteAcceptance(ctx, inv.ID, claim)
	if err != nil || accepted.Status != invitations.StatusAccepted {
		t.Fatalf("complete=%+v %v", accepted, err)
	}
	claim.Now = claim.Now.Add(time.Hour)
	replay, err := repo.CompleteAcceptance(ctx, inv.ID, claim)
	if err != nil || !replay.AcceptedAt.Equal(accepted.AcceptedAt) {
		t.Fatalf("repeat complete=%+v %v", replay, err)
	}
	if _, err := repo.Create(ctx, duplicate); err != nil {
		t.Fatalf("completed tuple not released: %v", err)
	}
}

func testInvitationStaleTokenTransition(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations
	inv := mustNewInvitation(t, "project", "p", "member", "rotate@example.com", "owner", "old-token", suiteBase)
	inv, err := repo.Create(ctx, inv)
	if err != nil {
		t.Fatal(err)
	}
	other := mustNewInvitation(t, "project", "other", "member", "other@example.com", "owner", "taken-token", suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	collision := invitations.StatusUpdate{ExpectedTokenHash: inv.TokenHash, TokenHash: other.TokenHash, Status: invitations.StatusPending, ExpiresAt: inv.ExpiresAt, UpdatedAt: suiteBase.Add(time.Minute)}
	if _, err := repo.UpdateStatus(ctx, inv.ID, collision); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("colliding resend=%v", err)
	}
	if current, err := repo.Get(ctx, inv.ID); err != nil || current.TokenHash != inv.TokenHash {
		t.Fatalf("colliding resend changed token: %+v %v", current, err)
	}
	update := invitations.StatusUpdate{ExpectedTokenHash: inv.TokenHash, TokenHash: "new-token", Status: invitations.StatusPending, ExpiresAt: inv.ExpiresAt.Add(time.Hour), UpdatedAt: suiteBase.Add(time.Minute)}
	if _, err := repo.UpdateStatus(ctx, inv.ID, update); err != nil {
		t.Fatal(err)
	}
	claim := invitations.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "claimant", Now: update.UpdatedAt}
	if _, err := repo.ClaimAcceptance(ctx, inv.ID, claim); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("old token claimed after resend: %v", err)
	}
	update.Status = invitations.StatusCancelled
	if _, err := repo.UpdateStatus(ctx, inv.ID, update); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale cancel overwrote resend: %v", err)
	}
	current, err := repo.Get(ctx, inv.ID)
	if err != nil || current.TokenHash != "new-token" || current.Status != invitations.StatusPending {
		t.Fatalf("stale write changed current row: %+v %v", current, err)
	}
	claim.TokenHash = current.TokenHash
	claim.Now = current.ExpiresAt
	if _, err := repo.ClaimAcceptance(ctx, inv.ID, claim); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("expired pending claim=%v", err)
	}
}

func testInvitationConcurrentClaimCancel(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations
	inv := mustNewInvitation(t, "project", "p", "member", "race@example.com", "owner", "race-token", suiteBase)
	inv, err := repo.Create(ctx, inv)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = repo.ClaimAcceptance(ctx, inv.ID, invitations.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "claimant", Now: suiteBase.Add(time.Minute)})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errs[1] = repo.UpdateStatus(ctx, inv.ID, invitations.StatusUpdate{ExpectedTokenHash: inv.TokenHash, TokenHash: inv.TokenHash, Status: invitations.StatusCancelled, ExpiresAt: inv.ExpiresAt, UpdatedAt: suiteBase.Add(time.Minute)})
	}()
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("race error=%v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("claim/cancel winners=%d errors=%v", wins, errs)
	}
	current, err := repo.Get(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != invitations.StatusAccepting && current.Status != invitations.StatusCancelled {
		t.Fatalf("race state=%s", current.Status)
	}
}
