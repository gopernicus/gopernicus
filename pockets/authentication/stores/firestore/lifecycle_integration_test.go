//go:build integration && !live

package firestore

import (
	"context"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// The lifecycle transition's REVOCATION COMPLETENESS, asserted on documents.
//
// The shared conformance suite's UserLifecycle group checks the transition
// through the ports: the session is gone, the grant no longer consumes. That is
// necessary and not sufficient here. A Firestore session's uniqueness lives in a
// separate claim document, so a store can delete every session row, pass the
// whole suite, and still leave the refresh-hash claims behind — after which the
// user's next login can never take the same credential again, and nothing at
// read time notices. These tests read the claim documents themselves.
//
// They also pin the two properties a retryable transaction makes possible to get
// wrong: a rolled-back attempt must leave the world exactly as it found it, and
// the committed outcome must be the COMMITTING attempt's, not an earlier one's.

// livePrimaryUses is a verified primary email's full role set.
var livePrimaryUses = identifier.Uses{Login: true, Recovery: true, Notification: true}

// seedLifecycleSubject creates a user with a primary email, two sessions, and a
// grant bound to each session — a subject with something real to revoke.
func seedLifecycleSubject(t *testing.T, r auth.Repositories) (user.User, []string) {
	t.Helper()
	ctx := context.Background()

	u, _ := seedUser(t, r, identifier.KindEmail, "lifecycle@example.com", livePrimaryUses, true, testBase, testBase)

	hashes := []string{"lifecycle-refresh-0", "lifecycle-refresh-1"}
	for i, hash := range hashes {
		sess := seedSession(t, r, u.ID, hash, time.Hour)
		grant := newTestGrant(sess.ID, u.ID, "set_password", "ctx", time.Hour, time.Now())
		if i == 1 {
			// The second grant names the user but a purpose of its own, so the
			// two revocation disjuncts (owned by the user, bound to a session)
			// are both populated.
			grant = newTestGrant(sess.ID, u.ID, "change_email", "ctx", time.Hour, time.Now())
		}
		if _, err := r.AuthenticationGrants.Create(ctx, grant, u.AuthRevision, time.Now()); err != nil {
			t.Fatalf("AuthenticationGrants.Create: %v", err)
		}
	}
	return u, hashes
}

// storedRevocationState reports what the user still holds: session rows, grants,
// and how many of hashes still have a live refresh claim.
func storedRevocationState(t *testing.T, db *firestoredb.DB, userID string, hashes []string) (rows, grants, claims int) {
	t.Helper()
	ctx := context.Background()
	r := db.ReaderFrom(ctx)

	live, err := readSessionsForUser(ctx, db, r, userID)
	if err != nil {
		t.Fatalf("readSessionsForUser: %v", err)
	}
	ids := make([]string, 0, len(live))
	for _, sess := range live {
		ids = append(ids, sess.ID)
	}
	held, err := readGrantsForRevocation(ctx, db, r, userID, ids)
	if err != nil {
		t.Fatalf("readGrantsForRevocation: %v", err)
	}
	for _, hash := range hashes {
		if _, ok := refreshClaimOwner(t, db, hash); ok {
			claims++
		}
	}
	return len(live), len(held), claims
}

// TestSetStatusRevokesEveryDocumentItOwes is the completeness assertion: after a
// deactivation the user holds ZERO sessions, ZERO refresh claims, and ZERO
// grants — including the grant bound to a session rather than to the user — and
// the directory projection the transition must NOT touch is intact (SCHEMA.md
// §6.3 row 14).
func TestSetStatusRevokesEveryDocumentItOwes(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, hashes := seedLifecycleSubject(t, r)
	if rows, grants, claims := storedRevocationState(t, db, u.ID, hashes); rows != 2 || grants != 2 || claims != 2 {
		t.Fatalf("seeded state = %d sessions, %d grants, %d claims; want 2/2/2", rows, grants, claims)
	}

	change, err := r.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, testBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !change.Changed || change.RevokedSessions != 2 {
		t.Errorf("change = %+v, want changed with 2 revoked sessions", change)
	}

	rows, grants, claims := storedRevocationState(t, db, u.ID, hashes)
	if rows != 0 || grants != 0 || claims != 0 {
		t.Errorf("after deactivation: %d sessions, %d grants, %d refresh claims survive; want 0/0/0", rows, grants, claims)
	}

	// The released credential must be re-claimable: a claim left behind would
	// refuse this forever, and no port would report why.
	seedSession(t, r, u.ID, hashes[0], time.Hour)

	if email, verified := storedProjection(t, db, u.ID); email != "lifecycle@example.com" || !verified {
		t.Errorf("the transition disturbed the directory projection: (%q, %v)", email, verified)
	}
	after, err := r.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}
	if after.AuthRevision != 1 || after.StatusChangedAt.IsZero() {
		t.Errorf("persisted user = revision %d, changedAt %v; want revision 1 and a transition time", after.AuthRevision, after.StatusChangedAt)
	}
}

// TestSetStatusReplayRevokesNothing is the no-op half. Replaying the status the
// user already has must write nothing at all — not the revision, not the
// transition time, and above all not the revocation, which would log every
// active session out of an account whose posture never changed.
func TestSetStatusReplayRevokesNothing(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, hashes := seedLifecycleSubject(t, r)
	before, err := r.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}

	change, err := r.UserAdmin.SetStatus(ctx, u.ID, user.StatusActive, testBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("SetStatus(active on an active user): %v", err)
	}
	if change.Changed || change.RevokedSessions != 0 {
		t.Errorf("replay change = %+v, want unchanged with nothing revoked", change)
	}

	if rows, grants, claims := storedRevocationState(t, db, u.ID, hashes); rows != 2 || grants != 2 || claims != 2 {
		t.Errorf("a replay revoked something: %d sessions, %d grants, %d claims; want 2/2/2", rows, grants, claims)
	}
	after, err := r.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get after: %v", err)
	}
	if after.AuthRevision != before.AuthRevision {
		t.Errorf("auth_revision moved on a replay: %d → %d", before.AuthRevision, after.AuthRevision)
	}
	if !after.StatusChangedAt.Equal(before.StatusChangedAt) {
		t.Errorf("status_changed_at moved on a replay: %v → %v", before.StatusChangedAt, after.StatusChangedAt)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at moved on a replay: %v → %v", before.UpdatedAt, after.UpdatedAt)
	}
}

// TestSetStatusRollsBackWholeAndRetriesClean drives the REAL attempt function
// through this store's real contention loop with a genuine Aborted status ending
// attempt one. Two properties are under test and neither is reachable through
// the port:
//
//   - the aborted attempt left NOTHING behind — the user is still active and
//     every session, claim and grant it queued for deletion is still there; and
//   - the committing attempt revokes the world as IT read it, including a
//     session minted after the first attempt died. An implementation that
//     computed its revocation set once, outside the callback, would miss it.
func TestSetStatusRollsBackWholeAndRetriesClean(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, hashes := seedLifecycleSubject(t, r)
	admin := newUserAdminStore(db)

	var out user.StatusChange
	attempts, err := retryWithInjectedAbort(ctx, db,
		func() {
			// Attempt one has rolled back: nothing it queued may have landed.
			rows, grants, claims := storedRevocationState(t, db, u.ID, hashes)
			if rows != 2 || grants != 2 || claims != 2 {
				t.Errorf("the rolled-back attempt changed state: %d sessions, %d grants, %d claims; want 2/2/2", rows, grants, claims)
			}
			current, err := r.Users.Get(ctx, u.ID)
			if err != nil {
				t.Errorf("Users.Get between attempts: %v", err)
			} else if !user.NormalizeStatus(current.Status).Active() || current.AuthRevision != 0 {
				t.Errorf("the rolled-back attempt persisted %q at revision %d; want active at 0", current.Status, current.AuthRevision)
			}
			// The world moves between attempts: a third session the second
			// attempt must also revoke.
			seedSession(t, r, u.ID, "lifecycle-refresh-2", time.Hour)
		},
		func(ctx context.Context) error {
			return admin.transition(ctx, u.ID, user.StatusDeactivated, testBase.Add(time.Hour), &out)
		},
	)
	if err != nil {
		t.Fatalf("the retried transition must succeed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (the injected Aborted must re-run the callback)", attempts)
	}
	if !out.Changed || out.RevokedSessions != 3 {
		t.Errorf("outcome = %+v, want the COMMITTING attempt's three revoked sessions", out)
	}

	all := append(append([]string{}, hashes...), "lifecycle-refresh-2")
	if rows, grants, claims := storedRevocationState(t, db, u.ID, all); rows != 0 || grants != 0 || claims != 0 {
		t.Errorf("after the retry: %d sessions, %d grants, %d claims survive; want 0/0/0", rows, grants, claims)
	}
	after, err := r.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}
	if after.AuthRevision != 1 {
		t.Errorf("auth_revision = %d after a retried transition, want exactly one increment", after.AuthRevision)
	}
}
