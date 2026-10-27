package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The session-activity conformance cases (SOC 2 S4d part 2). The capability
// is OPTIONAL on Repositories.Sessions, so the group skips LOUDLY when the
// store does not implement it. Every method runs on the store's pool path,
// never a transaction the context carries; a SQL store proves that with its
// own live test.

// runSessionActivity registers the session.ActivityRepository group.
func runSessionActivity(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()
	t.Run("SessionActivity", func(t *testing.T) {
		if _, ok := newRepos(t).Sessions.(session.ActivityRepository); !ok {
			t.Skip("session.ActivityRepository not implemented — session activity conformance NOT verified for this Repositories")
		}
		activity := func(t *testing.T) (session.SessionRepository, session.ActivityRepository) {
			repo := newRepos(t).Sessions
			return repo, repo.(session.ActivityRepository)
		}
		t.Run("ProbeSchemaPresent", func(t *testing.T) {
			_, a := activity(t)
			if err := a.ProbeSchema(context.Background()); err != nil {
				t.Fatalf("ProbeSchema on a migrated store = %v, want nil", err)
			}
		})
		t.Run("RotateAtStamps", func(t *testing.T) { testActivityRotateAtStamps(t, activity) })
		t.Run("ConsumeGraceNeverStamps", func(t *testing.T) { testActivityConsumeGraceNeverStamps(t, activity) })
		t.Run("LegacyRotatedRowGrandfathered", func(t *testing.T) { testActivityLegacyRotated(t, activity) })
		t.Run("DeleteIfIdleCAS", func(t *testing.T) { testActivityDeleteIfIdle(t, activity) })
		t.Run("StampEncodingRoundTrip", func(t *testing.T) { testActivityEncodingRoundTrip(t, activity) })
		t.Run("WithActivityMatchesGetByRefreshHash", func(t *testing.T) { testActivityMatchesGetByRefreshHash(t, newRepos) })
	})
}

type activityRepos func(t *testing.T) (session.SessionRepository, session.ActivityRepository)

// mustActivity reads the row and stamp for hash, failing on any error.
func mustActivity(t *testing.T, a session.ActivityRepository, hash string) (session.Session, session.RefreshMatch, session.Activity) {
	t.Helper()
	sess, match, act, err := a.GetByRefreshHashWithActivity(context.Background(), hash)
	if err != nil {
		t.Fatalf("GetByRefreshHashWithActivity(%q): %v", hash, err)
	}
	return sess, match, act
}

func testActivityRotateAtStamps(t *testing.T, repos activityRepos) {
	ctx := context.Background()
	repo, a := repos(t)
	created, err := repo.Create(ctx, newSession("u1", "act-a", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, match, act := mustActivity(t, a, "act-a"); match != session.RefreshMatchCurrent || act.Stamped {
		t.Fatalf("fresh row = match %v, %+v; want current and unstamped", match, act)
	}
	if _, _, _, err := a.GetByRefreshHashWithActivity(ctx, ""); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("GetByRefreshHashWithActivity(\"\") = %v, want sdk.ErrNotFound", err)
	}
	if _, _, _, err := a.GetByRefreshHashWithActivity(ctx, "act-none"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("GetByRefreshHashWithActivity(unknown) = %v, want sdk.ErrNotFound", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := a.RotateAt(ctx, created.ID, "act-a", "act-b", now); err != nil {
		t.Fatalf("RotateAt: %v", err)
	}
	got, match, act := mustActivity(t, a, "act-b")
	if match != session.RefreshMatchCurrent || got.RotationCount != 1 || got.PreviousRefreshTokenHash != "act-a" || got.PreviousUsed {
		t.Fatalf("after RotateAt = match %v, %+v; want Rotate's CAS result", match, got)
	}
	if !act.Stamped || !act.LastRotatedAt.Equal(now) {
		t.Fatalf("stamp = %+v, want %v", act, now)
	}
	if !got.ExpiresAt.Equal(created.ExpiresAt) {
		t.Fatalf("RotateAt changed ExpiresAt: %v → %v", created.ExpiresAt, got.ExpiresAt)
	}
	if _, match, prevAct := mustActivity(t, a, "act-a"); match != session.RefreshMatchPrevious || !prevAct.LastRotatedAt.Equal(now) {
		t.Fatalf("previous slot = match %v, %+v; want previous with the same stamp", match, prevAct)
	}

	if err := a.RotateAt(ctx, created.ID, "act-a", "act-c", now.Add(time.Minute)); !errors.Is(err, session.ErrRotationConflict) {
		t.Fatalf("RotateAt with a stale hash = %v, want ErrRotationConflict", err)
	}
	if _, _, act := mustActivity(t, a, "act-b"); !act.LastRotatedAt.Equal(now) {
		t.Fatalf("a lost RotateAt moved the stamp to %v", act.LastRotatedAt)
	}
}

func testActivityConsumeGraceNeverStamps(t *testing.T, repos activityRepos) {
	ctx := context.Background()
	repo, a := repos(t)
	created, err := repo.Create(ctx, newSession("u1", "grace-a", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	if err := a.RotateAt(ctx, created.ID, "grace-a", "grace-b", stamp); err != nil {
		t.Fatalf("RotateAt: %v", err)
	}
	if err := repo.ConsumeGrace(ctx, created.ID, "grace-a"); err != nil {
		t.Fatalf("ConsumeGrace: %v", err)
	}
	got, _, act := mustActivity(t, a, "grace-b")
	if !got.PreviousUsed || !act.LastRotatedAt.Equal(stamp) {
		t.Fatalf("after ConsumeGrace = %+v, %+v; want the slot consumed and the stamp unchanged", got, act)
	}
}

// testActivityLegacyRotated proves the grandfather rule's inputs: the plain
// Rotate never writes the stamp, so a row rotated while idle expiry was
// unwired reads rotation_count > 0 and Stamped false.
func testActivityLegacyRotated(t *testing.T, repos activityRepos) {
	ctx := context.Background()
	repo, a := repos(t)
	created, err := repo.Create(ctx, newSession("u1", "legacy-a", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Rotate(ctx, created.ID, "legacy-a", "legacy-b"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	got, _, act := mustActivity(t, a, "legacy-b")
	if got.RotationCount != 1 || act.Stamped {
		t.Fatalf("legacy rotation = count %d, %+v; want 1 and unstamped", got.RotationCount, act)
	}
}

func testActivityDeleteIfIdle(t *testing.T, repos activityRepos) {
	ctx := context.Background()
	repo, a := repos(t)

	// A never-rotated row: the NULL stamp matches NULL.
	fresh, err := repo.Create(ctx, newSession("u1", "idle-fresh", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if deleted, err := a.DeleteIfIdle(ctx, fresh.ID, session.Activity{}, 1); err != nil || deleted {
		t.Fatalf("DeleteIfIdle with a stale rotation count = %v, %v; want false", deleted, err)
	}
	if deleted, err := a.DeleteIfIdle(ctx, fresh.ID, session.Activity{LastRotatedAt: time.Now(), Stamped: true}, 0); err != nil || deleted {
		t.Fatalf("DeleteIfIdle observing a stamp on a NULL row = %v, %v; want false", deleted, err)
	}
	if deleted, err := a.DeleteIfIdle(ctx, fresh.ID, session.Activity{}, 0); err != nil || !deleted {
		t.Fatalf("DeleteIfIdle(observed NULL) = %v, %v; want true", deleted, err)
	}
	if _, _, err := repo.GetByRefreshHash(ctx, "idle-fresh"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("row after DeleteIfIdle = %v, want sdk.ErrNotFound", err)
	}
	if deleted, err := a.DeleteIfIdle(ctx, fresh.ID, session.Activity{}, 0); err != nil || deleted {
		t.Fatalf("DeleteIfIdle of a gone row = %v, %v; want false, nil", deleted, err)
	}

	// A stamped row that moved on: the observed stamp no longer matches.
	moved, err := repo.Create(ctx, newSession("u1", "idle-a", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first := time.Now().UTC().Truncate(time.Microsecond)
	if err := a.RotateAt(ctx, moved.ID, "idle-a", "idle-b", first); err != nil {
		t.Fatalf("RotateAt: %v", err)
	}
	_, _, observed := mustActivity(t, a, "idle-b")
	if err := a.RotateAt(ctx, moved.ID, "idle-b", "idle-c", first.Add(time.Second)); err != nil {
		t.Fatalf("RotateAt: %v", err)
	}
	if deleted, err := a.DeleteIfIdle(ctx, moved.ID, observed, 1); err != nil || deleted {
		t.Fatalf("DeleteIfIdle after a concurrent rotation = %v, %v; want false", deleted, err)
	}
	_, _, current := mustActivity(t, a, "idle-c")
	if deleted, err := a.DeleteIfIdle(ctx, moved.ID, current, 2); err != nil || !deleted {
		t.Fatalf("DeleteIfIdle(current observation) = %v, %v; want true", deleted, err)
	}
}

// testActivityEncodingRoundTrip proves a sub-microsecond stamp round-trips
// as the store persists it (an observation read back deletes by CAS), stays
// within a microsecond of the written instant, and orders after created_at.
func testActivityEncodingRoundTrip(t *testing.T, repos activityRepos) {
	ctx := context.Background()
	repo, a := repos(t)
	base := time.Now().Truncate(time.Microsecond)
	created, err := repo.Create(ctx, newSession("u1", "enc-a", time.Hour, base))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stamp := base.Add(time.Second + 750*time.Nanosecond).In(time.FixedZone("UTC+2", 2*60*60))
	if err := a.RotateAt(ctx, created.ID, "enc-a", "enc-b", stamp); err != nil {
		t.Fatalf("RotateAt: %v", err)
	}
	got, _, act := mustActivity(t, a, "enc-b")
	if d := act.LastRotatedAt.Sub(stamp); d < -time.Microsecond || d > time.Microsecond {
		t.Fatalf("stamp = %v, want within 1µs of %v", act.LastRotatedAt, stamp)
	}
	if !act.LastRotatedAt.After(got.CreatedAt) {
		t.Fatalf("stamp %v does not order after created_at %v", act.LastRotatedAt, got.CreatedAt)
	}
	if deleted, err := a.DeleteIfIdle(ctx, created.ID, act, got.RotationCount); err != nil || !deleted {
		t.Fatalf("DeleteIfIdle(the read-back observation) = %v, %v; want true", deleted, err)
	}
}

// RunSessionActivitySchemaProbe runs the probe contract against a store
// WITHOUT migration 0029: ProbeSchema must report sdk.ErrNotFound, the one
// answer New maps to ErrIdleTimeoutSchemaMissing.
func RunSessionActivitySchemaProbe(t *testing.T, newRepo func(t *testing.T) session.ActivityRepository) {
	t.Helper()
	if err := newRepo(t).ProbeSchema(context.Background()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeSchema on an unmigrated store = %v, want sdk.ErrNotFound", err)
	}
}

// testActivityMatchesGetByRefreshHash pins GetByRefreshHashWithActivity's
// (Session, RefreshMatch) to GetByRefreshHash's, field for field, so a
// wired-only column list cannot drift from the store's session columns
// (session_profile, delegation, authentication_methods, assurance_level,
// authenticated_at): on a first-party row with recorded authentication, at
// its current and previous hash after a rotation, and on a delegated row.
func testActivityMatchesGetByRefreshHash(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	ctx := context.Background()
	same := func(t *testing.T, repo session.SessionRepository, hash string, wantMatch session.RefreshMatch) {
		t.Helper()
		plain, plainMatch, err := repo.GetByRefreshHash(ctx, hash)
		if err != nil {
			t.Fatalf("GetByRefreshHash(%q): %v", hash, err)
		}
		wired, wiredMatch, _ := mustActivity(t, repo.(session.ActivityRepository), hash)
		if plainMatch != wantMatch || wiredMatch != plainMatch {
			t.Fatalf("match for %q: GetByRefreshHash %v, WithActivity %v; want %v", hash, plainMatch, wiredMatch, wantMatch)
		}
		if !reflect.DeepEqual(wired, plain) {
			t.Fatalf("GetByRefreshHashWithActivity(%q) drifted from GetByRefreshHash:\n with activity %+v\n plain         %+v", hash, wired, plain)
		}
	}

	t.Run("FirstParty", func(t *testing.T) {
		repo := newRepos(t).Sessions
		now := time.Now().UTC().Truncate(time.Microsecond)
		sess := newSession("u1", "match-a", time.Hour, now)
		sess.Authentication = session.AuthenticationMetadata{
			AuthenticatedAt: now.Add(-time.Minute),
			Assurance:       session.AssuranceAAL2,
			Methods: []session.AuthenticationMethod{
				{Kind: session.MethodPassword, Assurance: session.AssuranceAAL1},
				{Kind: session.MethodPasskey, Assurance: session.AssuranceAAL2, PhishingResistant: true, ReplayResistant: true},
			},
		}
		created, err := repo.Create(ctx, sess)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.(session.ActivityRepository).RotateAt(ctx, created.ID, "match-a", "match-b", now); err != nil {
			t.Fatalf("RotateAt: %v", err)
		}
		same(t, repo, "match-b", session.RefreshMatchCurrent)
		same(t, repo, "match-a", session.RefreshMatchPrevious)
		got, _, _ := mustActivity(t, repo.(session.ActivityRepository), "match-b")
		if !got.FirstParty() || len(got.Authentication.Methods) != 2 || got.Authentication.Assurance != session.AssuranceAAL2 || !got.Authentication.AuthenticatedAt.Equal(now.Add(-time.Minute)) {
			t.Fatalf("authentication metadata did not round-trip: %+v", got.Authentication)
		}
	})

	t.Run("Delegated", func(t *testing.T) {
		r := newRepos(t)
		if r.OAuth2 == nil {
			t.Skip("Repositories.OAuth2 not wired — the delegated-row comparison is NOT verified for this Repositories")
		}
		grant := newOAuthFixture(t, r).grant(t)
		if grant.Profile != session.ProfileDelegated || grant.Delegation == (session.Delegation{}) {
			t.Fatalf("fixture grant = %+v, want a delegated row", grant)
		}
		same(t, r.Sessions, grant.RefreshTokenHash, session.RefreshMatchCurrent)
	})
}
