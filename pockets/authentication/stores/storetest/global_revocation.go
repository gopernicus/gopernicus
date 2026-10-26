package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// The fleet-wide revocation conformance cases (SOC 2 S4d). The port is
// OPTIONAL, so the group skips LOUDLY when unwired.

// runGlobalRevocation registers the session.GlobalRevoker conformance group.
func runGlobalRevocation(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()
	t.Run("GlobalRevocation", func(t *testing.T) {
		if newRepos(t).GlobalSessions == nil {
			t.Skip("GlobalSessions not wired — global revocation conformance NOT verified for this Repositories")
		}
		t.Run("BatchBumpsActiveUsersOnly", func(t *testing.T) { testGlobalBatchActiveOnly(t, newRepos(t)) })
		t.Run("KeysetBatchesUntilDone", func(t *testing.T) { testGlobalBatchPaging(t, newRepos(t)) })
		t.Run("RerunBumpsAgain", func(t *testing.T) { testGlobalRerun(t, newRepos(t)) })
		t.Run("NonPositiveLimitRefused", func(t *testing.T) {
			_, err := newRepos(t).GlobalSessions.RevokeAllBatch(context.Background(), "", 0, suiteBase)
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("limit 0 = %v, want sdk.ErrInvalidInput", err)
			}
		})
		t.Run("SweepRemovesInactiveUsersRows", func(t *testing.T) {
			repos := newRepos(t)
			if repos.UserAdmin == nil {
				t.Skip("UserAdmin not wired — the deactivated-user sweep is NOT verified for this Repositories")
			}
			testGlobalSweep(t, repos)
		})
	})
}

// seedRevocationUsers creates n active users, each holding one live session.
func seedRevocationUsers(t *testing.T, repos auth.Repositories, n int) ([]user.User, []session.Session) {
	t.Helper()
	ctx := context.Background()
	users := make([]user.User, 0, n)
	sessions := make([]session.Session, 0, n)
	for i := range n {
		u := seedDirectoryUser(t, repos, fmt.Sprintf("revoke-%d@example.com", i), suiteBase, suiteBase.Add(time.Duration(i)*time.Second))
		s, err := repos.Sessions.Create(ctx, newSession(u.ID, fmt.Sprintf("revoke-refresh-%d", i), time.Hour, time.Now()))
		if err != nil {
			t.Fatalf("Sessions.Create: %v", err)
		}
		users = append(users, u)
		sessions = append(sessions, s)
	}
	return users, sessions
}

// revisions reads each user's auth_revision.
func revisions(t *testing.T, repos auth.Repositories, users []user.User) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, u := range users {
		got, err := repos.Users.Get(context.Background(), u.ID)
		if err != nil {
			t.Fatalf("Users.Get(%s): %v", u.ID, err)
		}
		out[u.ID] = got.AuthRevision
	}
	return out
}

// sessionGone reports whether a session no longer reads back.
func sessionGone(repos auth.Repositories, id string) bool {
	_, err := repos.Sessions.Get(context.Background(), id)
	return errors.Is(err, sdk.ErrNotFound) || errors.Is(err, sdk.ErrExpired)
}

// testGlobalBatchActiveOnly is the core case: one batch bumps every active
// user's revision exactly once and deletes their sessions and grants, and
// leaves a deactivated user's revision alone.
func testGlobalBatchActiveOnly(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	users, sessions := seedRevocationUsers(t, repos, 3)
	if repos.AuthenticationGrants != nil {
		if _, err := (grantFixture{repos}).Create(ctx, newGrant(sessions[0].ID, users[0].ID, "set_password", "ctx", time.Hour, time.Now())); err != nil {
			t.Fatalf("AuthenticationGrants.Create: %v", err)
		}
	}
	if repos.Challenges != nil {
		if _, err := repos.Challenges.Replace(ctx, newChallenge(users[0].ID, challenge.PurposePasswordReset, "", "revoke-reset-proof", nil, 0, time.Hour, time.Now())); err != nil {
			t.Fatalf("Challenges.Replace: %v", err)
		}
	}
	active := users
	if repos.UserAdmin != nil {
		if _, err := repos.UserAdmin.SetStatus(ctx, users[2].ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
		active = users[:2]
	}
	before := revisions(t, repos, users)

	batch, err := repos.GlobalSessions.RevokeAllBatch(ctx, "", 500, suiteBase.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("RevokeAllBatch: %v", err)
	}
	if batch.Users != len(active) || !batch.Done {
		t.Fatalf("batch = %+v, want Users=%d Done", batch, len(active))
	}
	if batch.Sessions < len(active) {
		t.Errorf("batch.Sessions = %d, want at least %d", batch.Sessions, len(active))
	}
	after := revisions(t, repos, users)
	for _, u := range active {
		if after[u.ID] != before[u.ID]+1 {
			t.Errorf("active user %s revision = %d, want %d", u.ID, after[u.ID], before[u.ID]+1)
		}
	}
	if repos.UserAdmin != nil && after[users[2].ID] != before[users[2].ID] {
		t.Errorf("deactivated user revision moved: %d → %d", before[users[2].ID], after[users[2].ID])
	}
	for i := range active {
		if !sessionGone(repos, sessions[i].ID) {
			t.Errorf("session of active user %d survived", i)
		}
	}
	if repos.AuthenticationGrants != nil {
		if batch.Grants != 1 {
			t.Errorf("batch.Grants = %d, want 1", batch.Grants)
		}
		if _, err := (grantFixture{repos}).Consume(ctx, sessions[0].ID, "set_password", "ctx", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("grant survived the batch: err=%v", err)
		}
	}
	if repos.Challenges != nil {
		if _, err := repos.Challenges.ConsumeToken(ctx, challenge.PurposePasswordReset, "revoke-reset-proof", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("a password_reset challenge survived the batch: err=%v", err)
		}
	}
}

// testGlobalBatchPaging pages users by id in the store's keyset order: each
// batch holds at most limit users, LastUserID advances, the grants of every
// page go, and the last page reports Done. The order is the store's own (byte
// order), so the case does not compare it with Go's.
func testGlobalBatchPaging(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	users, sessions := seedRevocationUsers(t, repos, 5)
	if repos.AuthenticationGrants != nil {
		for i, s := range sessions {
			if _, err := (grantFixture{repos}).Create(ctx, newGrant(s.ID, users[i].ID, "set_password", "ctx", time.Hour, time.Now())); err != nil {
				t.Fatalf("AuthenticationGrants.Create: %v", err)
			}
		}
	}
	before := revisions(t, repos, users)

	after, total, batches, grants := "", 0, 0, 0
	seen := map[string]bool{}
	for {
		batch, err := repos.GlobalSessions.RevokeAllBatch(ctx, after, 2, suiteBase.Add(time.Hour))
		if err != nil {
			t.Fatalf("RevokeAllBatch(%q): %v", after, err)
		}
		batches++
		if batch.Users > 2 {
			t.Fatalf("batch %d holds %d users, over the limit", batches, batch.Users)
		}
		total += batch.Users
		grants += batch.Grants
		if batch.Users > 0 && (batch.LastUserID == "" || batch.LastUserID == after || seen[batch.LastUserID]) {
			t.Fatalf("batch %d LastUserID = %q after %q, want a fresh cursor", batches, batch.LastUserID, after)
		}
		seen[batch.LastUserID] = true
		if batch.Done {
			break
		}
		if batches > 10 {
			t.Fatal("batches never reported Done")
		}
		after = batch.LastUserID
	}
	if total != len(users) || batches != 3 {
		t.Fatalf("paged %d users in %d batches, want 5 in 3", total, batches)
	}
	if repos.AuthenticationGrants != nil && grants != len(users) {
		t.Errorf("grants deleted across pages = %d, want %d", grants, len(users))
	}
	got := revisions(t, repos, users)
	for _, u := range users {
		if got[u.ID] != before[u.ID]+1 {
			t.Errorf("user %s bumped %d times, want once", u.ID, got[u.ID]-before[u.ID])
		}
	}
	for _, s := range sessions {
		if !sessionGone(repos, s.ID) {
			t.Errorf("session %s survived", s.ID)
		}
	}
}

// testGlobalRerun proves the operation is re-runnable: a second run bumps
// every active user again and finds nothing left to delete.
func testGlobalRerun(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	users, _ := seedRevocationUsers(t, repos, 2)
	before := revisions(t, repos, users)
	for run := range 2 {
		batch, err := repos.GlobalSessions.RevokeAllBatch(ctx, "", 500, suiteBase.Add(time.Duration(run+1)*time.Hour))
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if run == 1 && (batch.Sessions != 0 || batch.Grants != 0) {
			t.Errorf("re-run deleted %+v, want nothing left", batch)
		}
	}
	got := revisions(t, repos, users)
	for _, u := range users {
		if got[u.ID] != before[u.ID]+2 {
			t.Errorf("user %s revision = %d, want %d after two runs", u.ID, got[u.ID], before[u.ID]+2)
		}
	}
}

// testGlobalSweep proves SweepOrphans removes a deactivated user's leftover
// session, which the active-only batches never reach.
func testGlobalSweep(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	users, _ := seedRevocationUsers(t, repos, 2)
	if _, err := repos.UserAdmin.SetStatus(ctx, users[1].ID, user.StatusDeactivated, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	leftover, err := repos.Sessions.Create(ctx, newSession(users[1].ID, "revoke-leftover", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Sessions.Create(leftover): %v", err)
	}
	// A store that refuses a grant for an inactive user leaves none to sweep.
	wantGrants := 0
	if repos.AuthenticationGrants != nil {
		if _, err := (grantFixture{repos}).Create(ctx, newGrant(leftover.ID, users[1].ID, "set_password", "ctx", time.Hour, time.Now())); err == nil {
			wantGrants = 1
		}
	}
	if _, err := repos.GlobalSessions.RevokeAllBatch(ctx, "", 500, suiteBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("RevokeAllBatch: %v", err)
	}
	if sessionGone(repos, leftover.ID) {
		t.Fatal("the active-only batch deleted a deactivated user's session")
	}
	sessions, grants, err := repos.GlobalSessions.SweepOrphans(ctx, suiteBase.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if grants != wantGrants {
		t.Errorf("SweepOrphans grants = %d, want %d", grants, wantGrants)
	}
	if sessions < 1 || !sessionGone(repos, leftover.ID) {
		t.Fatalf("sweep deleted %d sessions; leftover gone=%v", sessions, sessionGone(repos, leftover.ID))
	}
}
