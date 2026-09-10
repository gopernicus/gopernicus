//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// The invitation claim-lifecycle cases the conformance suite cannot reach,
// because NO PORT RETURNS A CLAIM. The suite proves what a caller observes; these
// prove the two claim documents underneath it, which is where a partial index
// reproduced by hand actually goes wrong (SCHEMA.md §5.5, §5.6).

// invitationFixture builds a pending invitation with a store-assigned id.
func invitationFixture(t *testing.T, relation, identifierValue, kind, tokenHash string, ttl time.Duration, now time.Time) invitation.Invitation {
	t.Helper()
	inv, err := invitation.New(dbGenerated, "project", "p-claims", relation, identifierValue, kind, "inviter-1", tokenHash, false, ttl, now)
	if err != nil {
		t.Fatalf("invitation.New(%q, %q): %v", relation, tokenHash, err)
	}
	return inv
}

// pendingClaimHolder reports which invitation holds the pending-tuple claim for
// inv's five-column tuple, if any. The ref is derived through the production
// helper, so the test cannot disagree with the store about what the tuple is.
func pendingClaimHolder(t *testing.T, db *firestoredb.DB, inv invitation.Invitation) (invitationPendingClaimDoc, bool) {
	t.Helper()
	row, err := newInvitationDoc(inv)
	if err != nil {
		t.Fatalf("newInvitationDoc: %v", err)
	}
	var claim invitationPendingClaimDoc
	if !readClaim(t, db, invitationPendingClaimRef(db, row), &claim) {
		return invitationPendingClaimDoc{}, false
	}
	return claim, true
}

// tokenClaimHolder reports which invitation holds the claim on a token hash.
func tokenClaimHolder(t *testing.T, db *firestoredb.DB, tokenHash string) (invitationTokenClaimDoc, bool) {
	t.Helper()
	var claim invitationTokenClaimDoc
	if !readClaim(t, db, invitationTokenClaimRef(db, tokenHash), &claim) {
		return invitationTokenClaimDoc{}, false
	}
	return claim, true
}

// TestPendingClaimIncludesRelation asserts the tuple's fifth column at the CLAIM
// DOCUMENT. The conformance suite proves that two invitations differing only by
// relation both CREATE; this proves they hold two DIFFERENT claims, which is the
// reason they can — a store that keyed the claim on four columns would pass
// neither, and one that keyed it on the invitation id would pass both and
// enforce nothing.
func TestPendingClaimIncludesRelation(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	member := invitationFixture(t, "member", "rel@example.com", identity.KindEmail, "hash-rel-member", time.Hour, testBase)
	admin := invitationFixture(t, "admin", "rel@example.com", identity.KindEmail, "hash-rel-admin", time.Hour, testBase)

	createdMember, err := r.Invitations.Create(ctx, member)
	if err != nil {
		t.Fatalf("Create(member): %v", err)
	}
	createdAdmin, err := r.Invitations.Create(ctx, admin)
	if err != nil {
		t.Fatalf("Create(admin) differing only by relation: %v, want nil", err)
	}

	memberClaim, ok := pendingClaimHolder(t, db, createdMember)
	if !ok || memberClaim.InvitationID != createdMember.ID {
		t.Errorf("member pending claim = %+v ok=%v, want the member invitation", memberClaim, ok)
	}
	adminClaim, ok := pendingClaimHolder(t, db, createdAdmin)
	if !ok || adminClaim.InvitationID != createdAdmin.ID {
		t.Errorf("admin pending claim = %+v ok=%v, want the admin invitation", adminClaim, ok)
	}
}

// TestStoredStatusTransitionReleasesThePendingClaim is the partial index's
// release edge, asserted on the claim document rather than only on the next
// Create. The two halves matter separately: the claim must be GONE (a store that
// left it and special-cased the read would still refuse a legitimate re-invite
// under a different code path), and the tuple must then be claimable again.
func TestStoredStatusTransitionReleasesThePendingClaim(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	first := invitationFixture(t, "member", "release@example.com", identity.KindEmail, "hash-release-1", time.Hour, testBase)
	created, err := r.Invitations.Create(ctx, first)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := pendingClaimHolder(t, db, created); !ok {
		t.Fatal("a pending invitation holds no pending claim")
	}

	if _, err := r.Invitations.UpdateStatus(ctx, created.ID, invitation.StatusUpdate{
		Status:    invitation.StatusDeclined,
		TokenHash: created.TokenHash,
		ExpiresAt: created.ExpiresAt,
		UpdatedAt: testBase.Add(time.Minute),
	}); err != nil {
		t.Fatalf("UpdateStatus(declined): %v", err)
	}

	if claim, ok := pendingClaimHolder(t, db, created); ok {
		t.Errorf("pending claim survived a transition off pending: %+v", claim)
	}
	// The token claim is NOT released by a status transition: its predicate is
	// "the invitation exists with that hash", which a decline does not change.
	if claim, ok := tokenClaimHolder(t, db, created.TokenHash); !ok || claim.InvitationID != created.ID {
		t.Errorf("token claim = %+v ok=%v, want it retained by the declined invitation", claim, ok)
	}

	second := invitationFixture(t, "member", "release@example.com", identity.KindEmail, "hash-release-2", time.Hour, testBase.Add(2*time.Minute))
	reinvited, err := r.Invitations.Create(ctx, second)
	if err != nil {
		t.Fatalf("Create for the freed tuple: %v, want nil", err)
	}
	if claim, ok := pendingClaimHolder(t, db, reinvited); !ok || claim.InvitationID != reinvited.ID {
		t.Errorf("pending claim after re-invite = %+v ok=%v, want the new invitation", claim, ok)
	}
}

// TestReadTimeExpiryRetainsThePendingClaim is SCHEMA.md §5.6's sharpest edge and
// the one a Firestore store is most likely to get wrong, because unlike SQL it
// COULD cheaply consult the clock while deciding a claim's fate. It must not: a
// partial index in either SQL adapter is evaluated against the STORED status, so
// an expired-but-still-pending row keeps its tuple until a writer moves it.
//
// The invitation here is expired at read time — GetByTokenHash proves it with
// sdk.ErrExpired — and the tuple is still taken. Only the stored transition frees
// it.
func TestReadTimeExpiryRetainsThePendingClaim(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	// Created an hour ago with a one-minute lifetime: expired NOW, stored pending.
	stale := invitationFixture(t, "member", "stale@example.com", identity.KindEmail, "hash-stale", time.Minute, time.Now().Add(-time.Hour))
	created, err := r.Invitations.Create(ctx, stale)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.Invitations.GetByTokenHash(ctx, "hash-stale"); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("GetByTokenHash: err=%v, want ErrExpired (the fixture must be expired at read time)", err)
	}

	claim, ok := pendingClaimHolder(t, db, created)
	if !ok || claim.InvitationID != created.ID {
		t.Fatalf("pending claim after a read-time expiry = %+v ok=%v, want it RETAINED", claim, ok)
	}
	blocked := invitationFixture(t, "member", "stale@example.com", identity.KindEmail, "hash-stale-2", time.Hour, time.Now())
	if _, err := r.Invitations.Create(ctx, blocked); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("Create over an expired-but-pending tuple: err=%v, want ErrAlreadyExists", err)
	}

	// The STORED transition is the only thing that frees it.
	if _, err := r.Invitations.UpdateStatus(ctx, created.ID, invitation.StatusUpdate{
		Status:    invitation.StatusCancelled,
		TokenHash: created.TokenHash,
		ExpiresAt: created.ExpiresAt,
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpdateStatus(cancelled): %v", err)
	}
	if _, err := r.Invitations.Create(ctx, blocked); err != nil {
		t.Errorf("Create after the stored transition: err=%v, want nil", err)
	}
}

// TestResendMovesTheTokenClaim covers the other claim's only moving edge: a
// resend changes the token hash, so the old claim must be released and the new
// one taken in the SAME transaction. A store that only took the new one would
// leave the old secret resolvable forever — and the old hash is exactly the value
// a resend was issued to invalidate.
func TestResendMovesTheTokenClaim(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	// A LIVE invitation: GetByTokenHash below asserts the resolved record, and a
	// past expiry would answer sdk.ErrExpired before the claim ever mattered.
	inv := invitationFixture(t, "member", "resend@example.com", identity.KindEmail, "hash-resend-old", time.Hour, time.Now())
	created, err := r.Invitations.Create(ctx, inv)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := r.Invitations.UpdateStatus(ctx, created.ID, invitation.StatusUpdate{
		Status:    invitation.StatusPending,
		TokenHash: "hash-resend-new",
		ExpiresAt: time.Now().Add(2 * time.Hour),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpdateStatus(resend): %v", err)
	}

	if claim, ok := tokenClaimHolder(t, db, "hash-resend-old"); ok {
		t.Errorf("the superseded token hash is still claimed: %+v", claim)
	}
	if claim, ok := tokenClaimHolder(t, db, "hash-resend-new"); !ok || claim.InvitationID != created.ID {
		t.Errorf("new token claim = %+v ok=%v, want the resent invitation", claim, ok)
	}
	if _, err := r.Invitations.GetByTokenHash(ctx, "hash-resend-old"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByTokenHash(superseded): err=%v, want ErrNotFound", err)
	}
	got, err := r.Invitations.GetByTokenHash(ctx, "hash-resend-new")
	if err != nil || got.ID != created.ID {
		t.Errorf("GetByTokenHash(resent): id=%q err=%v, want %q", got.ID, err, created.ID)
	}
	// The invitation stayed pending, so it kept its tuple throughout.
	if claim, ok := pendingClaimHolder(t, db, created); !ok || claim.InvitationID != created.ID {
		t.Errorf("pending claim after a resend = %+v ok=%v, want it retained", claim, ok)
	}
}
