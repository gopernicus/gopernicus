//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The refresh-claim lifecycle cases the shared conformance suite cannot reach,
// because NO PORT RETURNS A CLAIM. The suite proves the session STATE after a
// rotation; these prove the claim document that carries the state's uniqueness
// moved with it — the failure mode SCHEMA.md §5.3 names, where a store passes
// every port case and still cannot arbitrate the next login.

// seedSession creates one session through the port and returns it.
func seedSession(t *testing.T, r auth.Repositories, userID, hash string, ttl time.Duration) session.Session {
	t.Helper()
	sess, _ := session.NewSession(userID, ttl, time.Now())
	sess.RefreshTokenHash = hash
	created, err := r.Sessions.Create(context.Background(), sess)
	if err != nil {
		t.Fatalf("Sessions.Create(%q): %v", hash, err)
	}
	return created
}

// refreshClaimOwner reports which session holds the claim on a refresh hash, if
// any. An absent claim is a VALUE — "this credential is unclaimed" is the state
// most of these assertions are about.
func refreshClaimOwner(t *testing.T, db *firestoredb.DB, hash string) (refreshHashClaimDoc, bool) {
	t.Helper()
	var claim refreshHashClaimDoc
	if !readClaim(t, db, refreshClaimRef(db, hash), &claim) {
		return refreshHashClaimDoc{}, false
	}
	return claim, true
}

// TestRotationMovesTheCurrentHashClaim is the §5.3 asymmetry, asserted on the
// CLAIM DOCUMENTS: a rotation releases the old current hash and takes the new
// one, and the rotated-away hash — now the grace slot — is left with NO claim at
// all. A store that claimed the grace hash too would collide with itself on the
// next rotation; one that forgot to release the old claim would refuse the
// address forever.
func TestRotationMovesTheCurrentHashClaim(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	sess := seedSession(t, r, "u1", "hashA", time.Hour)
	if claim, ok := refreshClaimOwner(t, db, "hashA"); !ok || claim.SessionID != sess.ID {
		t.Fatalf("fresh session: claim(hashA) = %+v ok=%v, want the session", claim, ok)
	}

	if err := r.Sessions.Rotate(ctx, sess.ID, "hashA", "hashB"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, ok := refreshClaimOwner(t, db, "hashA"); ok {
		t.Errorf("the rotated-away hash still holds a claim: the grace slot takes NONE (SCHEMA §5.3)")
	}
	if claim, ok := refreshClaimOwner(t, db, "hashB"); !ok || claim.SessionID != sess.ID {
		t.Errorf("claim(hashB) = %+v ok=%v, want the rotated session", claim, ok)
	}

	// The freed hash is claimable again: uniqueness followed the CURRENT slot,
	// which is exactly what the non-unique SQL index on the previous slot says.
	other := seedSession(t, r, "u2", "hashA", time.Hour)
	if claim, ok := refreshClaimOwner(t, db, "hashA"); !ok || claim.SessionID != other.ID {
		t.Errorf("claim(hashA) after re-use = %+v ok=%v, want the new session", claim, ok)
	}

	// And the CLAIM is the access path, so the current match wins over another
	// row's grace slot carrying the same value.
	got, match, err := r.Sessions.GetByRefreshHash(ctx, "hashA")
	if err != nil || got.ID != other.ID || match != session.RefreshMatchCurrent {
		t.Errorf("GetByRefreshHash(hashA) = (%q, %d, %v), want the CURRENT holder", got.ID, match, err)
	}
}

// TestConsumeGraceRetainsTheResolvableGraceHash is the reason ConsumeGrace flips
// a flag instead of clearing the slot. A reused refresh token arriving after the
// grace window is spent must still RESOLVE to its session — that resolution is
// what lets the service revoke the session it belongs to. Dropping the hash
// would make the reuse anonymous, and an anonymous reuse revokes nothing.
func TestConsumeGraceRetainsTheResolvableGraceHash(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	sess := seedSession(t, r, "u1", "hashA", time.Hour)
	if err := r.Sessions.Rotate(ctx, sess.ID, "hashA", "hashB"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := r.Sessions.ConsumeGrace(ctx, sess.ID, "hashA"); err != nil {
		t.Fatalf("ConsumeGrace: %v", err)
	}

	got, match, err := r.Sessions.GetByRefreshHash(ctx, "hashA")
	if err != nil || got.ID != sess.ID || match != session.RefreshMatchPrevious {
		t.Fatalf("a SPENT grace token must still resolve its session: (%q, %d, %v)", got.ID, match, err)
	}
	if !got.PreviousUsed {
		t.Errorf("previous_used = false after ConsumeGrace, want true (the reuse signal)")
	}
	if _, ok := refreshClaimOwner(t, db, "hashA"); ok {
		t.Errorf("a spent grace hash holds a claim; it must hold none")
	}
}

// TestDeleteReleasesTheRefreshClaim proves revocation is complete: the row and
// the credential's claim go together, so the hash is immediately re-claimable. A
// store that deleted only the row would refuse that credential for the lifetime
// of the database, with nothing at read time to explain why.
func TestDeleteReleasesTheRefreshClaim(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	sess := seedSession(t, r, "u1", "hash-live", time.Hour)
	if err := r.Sessions.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := refreshClaimOwner(t, db, "hash-live"); ok {
		t.Fatalf("Delete left the refresh claim behind")
	}
	if _, _, err := r.Sessions.GetByRefreshHash(ctx, "hash-live"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByRefreshHash after Delete: err=%v, want ErrNotFound", err)
	}
	revived := seedSession(t, r, "u2", "hash-live", time.Hour)
	if claim, ok := refreshClaimOwner(t, db, "hash-live"); !ok || claim.SessionID != revived.ID {
		t.Errorf("the released hash is not re-claimable: %+v ok=%v", claim, ok)
	}
}

// TestDeleteByUserReleasesEveryClaim is the same completeness rule for the bulk
// cascade — the logout-everywhere primitive a password change calls. Every row
// AND every claim, in one transaction, and another user's session untouched.
func TestDeleteByUserReleasesEveryClaim(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	seedSession(t, r, "userA", "ha1", time.Hour)
	seedSession(t, r, "userA", "ha2", time.Hour)
	kept := seedSession(t, r, "userB", "hb1", time.Hour)

	if err := r.Sessions.DeleteByUser(ctx, "userA"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	for _, hash := range []string{"ha1", "ha2"} {
		if _, ok := refreshClaimOwner(t, db, hash); ok {
			t.Errorf("DeleteByUser left the claim on %q behind", hash)
		}
	}
	if claim, ok := refreshClaimOwner(t, db, "hb1"); !ok || claim.SessionID != kept.ID {
		t.Errorf("DeleteByUser(userA) disturbed userB's claim: %+v ok=%v", claim, ok)
	}
}

// TestRotationIntoAClaimedHashRollsBackWhole is ruling R3 on this collection: a
// rotation whose NEW hash is already some other session's live credential loses
// at the server, and the rotating session is left exactly as it was — same
// current hash, same empty grace slot, same counter. A store that wrote the row
// and the claim separately would leave a session holding a credential no claim
// backs.
func TestRotationIntoAClaimedHashRollsBackWhole(t *testing.T) {
	r, _ := openRepos(t)
	ctx := context.Background()

	mine := seedSession(t, r, "u1", "mine", time.Hour)
	seedSession(t, r, "u2", "theirs", time.Hour)

	if err := r.Sessions.Rotate(ctx, mine.ID, "mine", "theirs"); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("Rotate into a claimed hash: err=%v, want ErrAlreadyExists", err)
	}
	got, err := r.Sessions.Get(ctx, mine.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshTokenHash != "mine" || got.PreviousRefreshTokenHash != "" || got.RotationCount != 0 {
		t.Errorf("a rolled-back rotation changed the row: %+v", got)
	}
}
