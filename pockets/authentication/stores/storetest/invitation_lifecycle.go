package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
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

// skipUnsupportedCancelAcceptance skips a CancelAcceptance case for an adapter
// that reports the method unsupported (the firestore stub, ruling R6). Any other
// answer to the probe, including ErrNotFound, runs the case.
func skipUnsupportedCancelAcceptance(t *testing.T, repos auth.Repositories) {
	t.Helper()
	probe := invitations.Acceptance{TokenHash: "probe-token", SubjectType: "user", SubjectID: "probe", Now: suiteBase}
	if _, err := repos.Invitations.CancelAcceptance(context.Background(), "storetest-unsupported-probe", probe); errors.Is(err, errors.ErrUnsupported) {
		t.Skip("CancelAcceptance unsupported by this adapter — terminal-settlement conformance NOT verified")
	}
}

// skipUnsupportedListActive is skipUnsupportedCancelAcceptance for ListActiveByResource.
func skipUnsupportedListActive(t *testing.T, repos auth.Repositories) {
	t.Helper()
	in := invitations.ActiveListInput{ResourceType: "probe", ResourceID: "probe"}
	if _, err := repos.Invitations.ListActiveByResource(context.Background(), in, listing.Request{Limit: 1}); errors.Is(err, errors.ErrUnsupported) {
		t.Skip("ListActiveByResource unsupported by this adapter — active-listing conformance NOT verified")
	}
}

// claimInvitation creates a pending invitation and claims it for claimant one
// minute after creation, returning the accepting row and its claim.
func claimInvitation(t *testing.T, repo invitations.InvitationRepository, resourceID, relation, identifier, tokenHash string, createdAt time.Time) (invitations.Invitation, invitations.Acceptance) {
	t.Helper()
	ctx := context.Background()
	inv, err := repo.Create(ctx, mustNewInvitation(t, "project", resourceID, relation, identifier, "owner", tokenHash, createdAt))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claim := invitations.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "claimant", Now: createdAt.Add(time.Minute)}
	claimed, err := repo.ClaimAcceptance(ctx, inv.ID, claim)
	if err != nil || claimed.Status != invitations.StatusAccepting {
		t.Fatalf("claim=%+v %v", claimed, err)
	}
	return claimed, claim
}

// sameInvitationState reports whether two reads hold the same lifecycle state.
func sameInvitationState(a, b invitations.Invitation) bool {
	return a.Status == b.Status && a.TokenHash == b.TokenHash && a.ResolvedSubjectType == b.ResolvedSubjectType &&
		a.ResolvedSubjectID == b.ResolvedSubjectID && a.UpdatedAt.Equal(b.UpdatedAt) && a.AcceptedAt.Equal(b.AcceptedAt)
}

func testInvitationCancelAcceptance(t *testing.T, repos auth.Repositories) {
	skipUnsupportedCancelAcceptance(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	inv, claim := claimInvitation(t, repo, "p", "member", "cancel@example.com", "cancel-token", suiteBase)
	claim.Now = suiteBase.Add(2 * time.Minute)
	cancelled, err := repo.CancelAcceptance(ctx, inv.ID, claim)
	if err != nil {
		t.Fatalf("CancelAcceptance: %v", err)
	}
	for _, got := range []invitations.Invitation{cancelled, mustGetInvitation(t, repo, inv.ID)} {
		if got.Status != invitations.StatusCancelled || got.TokenHash != inv.TokenHash || got.ResolvedSubjectType != "user" ||
			got.ResolvedSubjectID != "claimant" || !got.AcceptedAt.IsZero() || !got.UpdatedAt.Equal(claim.Now) || got.Active() {
			t.Fatalf("cancelled claim=%+v", got)
		}
	}
	if _, err := repo.Create(ctx, mustNewInvitation(t, "project", "p", "member", "cancel@example.com", "owner", "cancel-next", suiteBase)); err != nil {
		t.Fatalf("cancelled claim did not release its tuple: %v", err)
	}
	if _, err := repo.CancelAcceptance(ctx, "no-such-invitation", claim); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unknown id=%v", err)
	}
	invalid := claim
	invalid.TokenHash = ""
	if _, err := repo.CancelAcceptance(ctx, inv.ID, invalid); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("invalid claim=%v", err)
	}
}

func testInvitationCancelAcceptanceIdempotent(t *testing.T, repos auth.Repositories) {
	skipUnsupportedCancelAcceptance(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	inv, claim := claimInvitation(t, repo, "p", "member", "twice@example.com", "twice-token", suiteBase)
	claim.Now = suiteBase.Add(2 * time.Minute)
	first, err := repo.CancelAcceptance(ctx, inv.ID, claim)
	if err != nil {
		t.Fatal(err)
	}
	claim.Now = suiteBase.Add(time.Hour)
	second, err := repo.CancelAcceptance(ctx, inv.ID, claim)
	if err != nil || !sameInvitationState(first, second) {
		t.Fatalf("repeat cancel=%+v %v, first=%+v", second, err, first)
	}
	if got := mustGetInvitation(t, repo, inv.ID); !sameInvitationState(first, got) {
		t.Fatalf("repeat cancel changed the row: %+v", got)
	}
}

func testInvitationCancelAcceptancePastExpiry(t *testing.T, repos auth.Repositories) {
	skipUnsupportedCancelAcceptance(t, repos)
	repo := repos.Invitations
	inv, claim := claimInvitation(t, repo, "p", "member", "late@example.com", "late-token", suiteBase)
	claim.Now = inv.ExpiresAt.Add(time.Hour)
	got, err := repo.CancelAcceptance(context.Background(), inv.ID, claim)
	if err != nil || got.Status != invitations.StatusCancelled {
		t.Fatalf("cancel past expiry=%+v %v", got, err)
	}
}

// testInvitationCancelAcceptanceMismatch proves only the exact accepting claim
// cancels: another token, subject or subject type, and every other status —
// including a row cancelled through UpdateStatus — conflict and change nothing.
func testInvitationCancelAcceptanceMismatch(t *testing.T, repos auth.Repositories) {
	skipUnsupportedCancelAcceptance(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	later := suiteBase.Add(30 * time.Minute)

	accepting, claim := claimInvitation(t, repo, "p-accepting", "member", "mismatch@example.com", "mismatch-token", suiteBase)
	for name, wrong := range map[string]func(*invitations.Acceptance){
		"token":        func(a *invitations.Acceptance) { a.TokenHash = "other-token" },
		"subject":      func(a *invitations.Acceptance) { a.SubjectID = "other" },
		"subject type": func(a *invitations.Acceptance) { a.SubjectType = "service_account" },
	} {
		c := claim
		c.Now = later
		wrong(&c)
		if _, err := repo.CancelAcceptance(ctx, accepting.ID, c); !errors.Is(err, sdk.ErrConflict) {
			t.Errorf("wrong %s=%v", name, err)
		}
	}
	if got := mustGetInvitation(t, repo, accepting.ID); !sameInvitationState(accepting, got) {
		t.Fatalf("mismatched cancel changed the claim: %+v", got)
	}

	accepted, acceptedClaim := claimInvitation(t, repo, "p-accepted", "member", "mismatch@example.com", "accepted-token", suiteBase)
	accepted, err := repo.CompleteAcceptance(ctx, accepted.ID, acceptedClaim)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := repo.Create(ctx, mustNewInvitation(t, "project", "p-pending", "member", "mismatch@example.com", "owner", "pending-token", suiteBase))
	if err != nil {
		t.Fatal(err)
	}
	declined := mustTransition(t, repo, "p-declined", "member", "mismatch@example.com", "declined-token", invitations.StatusDeclined)
	cancelled := mustTransition(t, repo, "p-cancelled", "member", "mismatch@example.com", "cancelled-token", invitations.StatusCancelled)

	for name, inv := range map[string]invitations.Invitation{"accepted": accepted, "pending": pending, "declined": declined, "cancelled via UpdateStatus": cancelled} {
		c := invitations.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: "claimant", Now: later}
		if _, err := repo.CancelAcceptance(ctx, inv.ID, c); !errors.Is(err, sdk.ErrConflict) {
			t.Errorf("%s=%v", name, err)
		}
		if got := mustGetInvitation(t, repo, inv.ID); !sameInvitationState(inv, got) {
			t.Errorf("%s changed: %+v", name, got)
		}
	}
}

func testInvitationCancelThenCompleteConflicts(t *testing.T, repos auth.Repositories) {
	skipUnsupportedCancelAcceptance(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	inv, claim := claimInvitation(t, repo, "p", "member", "settled@example.com", "settled-token", suiteBase)
	cancelled, err := repo.CancelAcceptance(ctx, inv.ID, claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CompleteAcceptance(ctx, inv.ID, claim); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("complete after cancel=%v", err)
	}
	if _, err := repo.ClaimAcceptance(ctx, inv.ID, claim); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("claim after cancel=%v", err)
	}
	if got := mustGetInvitation(t, repo, inv.ID); !sameInvitationState(cancelled, got) {
		t.Fatalf("cancelled row changed: %+v", got)
	}
}

// testInvitationConcurrentCompleteCancel races completion against cancellation
// of the same claim: exactly one wins and the row ends in the winner's state.
func testInvitationConcurrentCompleteCancel(t *testing.T, repos auth.Repositories) {
	skipUnsupportedCancelAcceptance(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	for i := range 4 {
		inv, claim := claimInvitation(t, repo, fmt.Sprintf("race-%d", i), "member", "race@example.com", fmt.Sprintf("race-token-%d", i), suiteBase)
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = repo.CompleteAcceptance(ctx, inv.ID, claim)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = repo.CancelAcceptance(ctx, inv.ID, claim)
		}()
		close(start)
		wg.Wait()
		var want string
		switch {
		case errs[0] == nil && errors.Is(errs[1], sdk.ErrConflict):
			want = invitations.StatusAccepted
		case errs[1] == nil && errors.Is(errs[0], sdk.ErrConflict):
			want = invitations.StatusCancelled
		default:
			t.Fatalf("complete/cancel results=%v", errs)
		}
		if got := mustGetInvitation(t, repo, inv.ID); got.Status != want {
			t.Fatalf("race state=%s, want %s", got.Status, want)
		}
	}
}

// testInvitationListActiveHidesTerminalHistory proves the active listing pages
// only stored pending/accepting rows — expired-but-pending rows included —
// however much terminal history the resource carries.
func testInvitationListActiveHidesTerminalHistory(t *testing.T, repos auth.Repositories) {
	skipUnsupportedListActive(t, repos)
	skipUnsupportedCancelAcceptance(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	const resource = "res-active"
	var active, fromAccepting []invitations.Invitation
	for i := 0; i < 12; i++ {
		createdAt := suiteBase.Add(time.Duration(i) * time.Minute)
		identifier := fmt.Sprintf("a%d@example.com", i)
		token := fmt.Sprintf("hash-act-%d", i)
		switch {
		case i < 4:
			status := invitations.StatusDeclined
			if i >= 2 {
				status = invitations.StatusCancelled
			}
			inv, err := repo.Create(ctx, mustNewInvitation(t, "project", resource, "member", identifier, "owner", token, createdAt))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.UpdateStatus(ctx, inv.ID, invitations.StatusUpdate{ExpectedTokenHash: inv.TokenHash, TokenHash: inv.TokenHash, Status: status, ExpiresAt: inv.ExpiresAt, UpdatedAt: createdAt.Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
		case i < 6:
			inv, claim := claimInvitation(t, repo, resource, "member", identifier, token, createdAt)
			if _, err := repo.CompleteAcceptance(ctx, inv.ID, claim); err != nil {
				t.Fatal(err)
			}
		case i < 8:
			inv, claim := claimInvitation(t, repo, resource, "member", identifier, token, createdAt)
			if _, err := repo.CancelAcceptance(ctx, inv.ID, claim); err != nil {
				t.Fatal(err)
			}
			fromAccepting = append(fromAccepting, inv)
		case i == 9:
			inv, _ := claimInvitation(t, repo, resource, "member", identifier, token, createdAt)
			active = append(active, inv)
		default:
			inv, err := repo.Create(ctx, mustNewInvitation(t, "project", resource, "member", identifier, "owner", token, createdAt))
			if err != nil {
				t.Fatal(err)
			}
			active = append(active, inv)
		}
	}
	// Every suiteBase row is past ExpiresAt by the wall clock; the stored status
	// still reserves the tuple, so the listing must keep the pending ones.
	if !active[len(active)-1].Expired(time.Now()) {
		t.Fatal("fixture: the pending rows must be expired by the wall clock")
	}
	for _, other := range []invitations.Invitation{
		mustNewInvitation(t, "project", "res-other-active", "member", "a8@example.com", "owner", "hash-act-other", suiteBase),
		mustNewInvitation(t, "folder", resource, "member", "a8@example.com", "owner", "hash-act-folder", suiteBase),
	} {
		if _, err := repo.Create(ctx, other); err != nil {
			t.Fatal(err)
		}
	}

	in := invitations.ActiveListInput{ResourceType: "project", ResourceID: resource}
	want := invitationIDsSorted(active)
	got := pageAllInvitations(t, func(req listing.Request) (listing.Page[invitations.Invitation], error) {
		return repo.ListActiveByResource(ctx, in, req)
	}, 2)
	if !equalStrings(got, want) {
		t.Errorf("ListActiveByResource paged order = %v, want %v", got, want)
	}
	page, err := repo.ListActiveByResource(ctx, in, listing.Request{Limit: 2, WithCount: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total == nil || *page.Total != int64(len(active)) || len(page.Items) != 2 || !page.HasMore {
		t.Errorf("counted first page: total=%v items=%d hasMore=%v", page.Total, len(page.Items), page.HasMore)
	}
	for _, inv := range fromAccepting {
		if got := mustGetInvitation(t, repo, inv.ID); got.Status != invitations.StatusCancelled || !got.AcceptedAt.IsZero() {
			t.Errorf("cancelled-from-accepting row=%+v", got)
		}
	}
}

func testInvitationListActiveRelationFilter(t *testing.T, repos auth.Repositories) {
	skipUnsupportedListActive(t, repos)
	ctx := context.Background()
	repo := repos.Invitations
	const resource = "res-rel"
	memberPending, err := repo.Create(ctx, mustNewInvitation(t, "project", resource, "member", "m1@example.com", "owner", "hash-rel-m1", suiteBase))
	if err != nil {
		t.Fatal(err)
	}
	memberAccepting, _ := claimInvitation(t, repo, resource, "member", "m2@example.com", "hash-rel-m2", suiteBase.Add(time.Minute))
	adminPending, err := repo.Create(ctx, mustNewInvitation(t, "project", resource, "admin", "a1@example.com", "owner", "hash-rel-a1", suiteBase.Add(2*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	mustTransition(t, repo, resource, "admin", "a2@example.com", "hash-rel-a2", invitations.StatusDeclined)

	for relation, want := range map[string][]invitations.Invitation{
		"":       {memberPending, memberAccepting, adminPending},
		"member": {memberPending, memberAccepting},
		"admin":  {adminPending},
		"viewer": nil,
	} {
		in := invitations.ActiveListInput{ResourceType: "project", ResourceID: resource, Relation: relation}
		got := pageAllInvitations(t, func(req listing.Request) (listing.Page[invitations.Invitation], error) {
			return repo.ListActiveByResource(ctx, in, req)
		}, 2)
		if !equalStrings(got, invitationIDsSorted(want)) {
			t.Errorf("relation %q = %v, want %v", relation, got, invitationIDsSorted(want))
		}
	}
}

// testInvitationListActiveRejectsInvalidInput pins ActiveListInput.Validate and
// the no-searchable-fields refusal at the store edge.
func testInvitationListActiveRejectsInvalidInput(t *testing.T, repos auth.Repositories) {
	skipUnsupportedListActive(t, repos)
	ctx := context.Background()
	for _, in := range []invitations.ActiveListInput{
		{ResourceID: "r"},
		{ResourceType: "project"},
		{ResourceType: " ", ResourceID: "r"},
	} {
		if _, err := repos.Invitations.ListActiveByResource(ctx, in, listing.Request{Limit: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("input %+v=%v", in, err)
		}
	}
	in := invitations.ActiveListInput{ResourceType: "project", ResourceID: "r"}
	if _, err := repos.Invitations.ListActiveByResource(ctx, in, listing.Request{Limit: 2, Search: "a"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("search=%v", err)
	}
}

func mustGetInvitation(t *testing.T, repo invitations.InvitationRepository, id string) invitations.Invitation {
	t.Helper()
	inv, err := repo.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return inv
}

// mustTransition creates a pending invitation and moves it to an unclaimed
// terminal status through UpdateStatus, binding the would-be claimant's subject
// ID so only the missing claim distinguishes it from a settled claim.
func mustTransition(t *testing.T, repo invitations.InvitationRepository, resourceID, relation, identifier, tokenHash, status string) invitations.Invitation {
	t.Helper()
	ctx := context.Background()
	inv, err := repo.Create(ctx, mustNewInvitation(t, "project", resourceID, relation, identifier, "owner", tokenHash, suiteBase))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.UpdateStatus(ctx, inv.ID, invitations.StatusUpdate{ExpectedTokenHash: inv.TokenHash, TokenHash: inv.TokenHash, Status: status, ExpiresAt: inv.ExpiresAt, ResolvedSubjectID: "claimant", UpdatedAt: suiteBase.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
