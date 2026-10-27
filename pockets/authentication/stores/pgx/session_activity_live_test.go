package pgx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The session-activity (0029) live legs. Each runs in its OWN disposable
// schema against POSTGRES_TEST_DSN and skips loudly without it; the
// conformance group itself runs through TestConformance_Postgres.

// activitySchema migrates a fresh disposable schema and returns it with its
// repositories.
func activitySchema(t *testing.T, name string) (*pgxdb.DB, pgxdb.Schema, auth.Repositories) {
	t.Helper()
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, name)
	assertNotOnSearchPath(t, db, schema)
	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	return db, schema, repos
}

// TestSessionActivitySchema_Postgres pins the conditional rule on a schema
// without 0029: Repositories constructs, ProbeSchema refuses with
// sdk.ErrNotFound naming the file, the pocket boots unwired (and refreshes
// without naming the column), and refuses with ErrIdleTimeoutSchemaMissing
// once IdleTimeout is set. A closed pool is never schema-missing, and the
// pocket maps it to sdk.ErrUnavailable.
func TestSessionActivitySchema_Postgres(t *testing.T) {
	db, schema, _ := activitySchema(t, "session_activity_unmigrated")
	ctx := context.Background()
	if _, err := db.Exec(ctx, "ALTER TABLE "+schema.Table(sessionsTable)+" DROP COLUMN last_rotated_at"); err != nil {
		t.Fatalf("drop last_rotated_at: %v", err)
	}
	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories without 0029 = %v, want it to construct (idle expiry is opt-in)", err)
	}
	store := repos.Sessions.(*SessionStore)
	storetest.RunSessionActivitySchemaProbe(t, func(*testing.T) session.ActivityRepository { return store })
	if err := store.ProbeSchema(ctx); err == nil || !strings.Contains(err.Error(), sessionActivityMigration) {
		t.Fatalf("ProbeSchema = %v, want an error naming %s", err, sessionActivityMigration)
	}

	unwired, err := newIdlePocket(t, repos, 0)
	if err != nil {
		t.Fatalf("the pocket with idle expiry unwired over a schema without 0029 = %v, want it to boot", err)
	}
	seedIdleUser(t, db, schema, "unmigrated")
	seedIdleSession(t, repos, "unmigrated", "tok-unmigrated", time.Now())
	if _, err := unwired.Authentication.Refresh(ctx, "tok-unmigrated"); err != nil {
		t.Fatalf("an unwired refresh over a schema without 0029 = %v, want success", err)
	}

	_, err = newIdlePocket(t, repos, time.Hour)
	if !errors.Is(err, auth.ErrIdleTimeoutSchemaMissing) || !strings.Contains(err.Error(), sessionActivityMigration) {
		t.Fatalf("the pocket with idle expiry wired over a schema without 0029 = %v, want ErrIdleTimeoutSchemaMissing naming %s", err, sessionActivityMigration)
	}
	t.Logf("wired idle expiry refused: %v", err)

	closed := probeDial(t)
	closedRepos := auth.Repositories(repos)
	closedRepos.Sessions = NewSessionStore(closed, WithSchema(schema))
	_ = closed.Close()
	if err := closedRepos.Sessions.(*SessionStore).ProbeSchema(ctx); err == nil || errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeSchema on a closed pool = %v, want an infrastructure error, never sdk.ErrNotFound", err)
	}
	if _, err := newIdlePocket(t, closedRepos, time.Hour); !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, auth.ErrIdleTimeoutSchemaMissing) {
		t.Fatalf("the wired pocket over a closed pool = %v, want sdk.ErrUnavailable, never schema-missing", err)
	}
}

// TestSessionActivityDeleteIfIdleRace_Postgres races DeleteIfIdle callers
// holding the same observation, NULL and stamped: exactly one deletes.
func TestSessionActivityDeleteIfIdleRace_Postgres(t *testing.T) {
	_, _, repos := activitySchema(t, "session_activity_race")
	ctx := context.Background()
	store := repos.Sessions.(*SessionStore)
	for i, stamped := range []bool{false, true} {
		sess := seedIdleSession(t, repos, "u-race", fmt.Sprintf("tok-race-%d", i), time.Now().Add(-2*time.Hour))
		hash := sess.RefreshTokenHash
		if stamped {
			hash = "race-next-" + sess.ID
			if err := store.RotateAt(ctx, sess.ID, sess.RefreshTokenHash, hash, time.Now().Add(-90*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		got, _, act, err := store.GetByRefreshHashWithActivity(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		const racers = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		deleted := 0
		for range racers {
			wg.Go(func() {
				ok, err := store.DeleteIfIdle(ctx, got.ID, act, got.RotationCount)
				if err != nil {
					t.Errorf("DeleteIfIdle: %v", err)
					return
				}
				if ok {
					mu.Lock()
					deleted++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if deleted != 1 {
			t.Fatalf("stamped=%v: %d of %d racing DeleteIfIdle calls deleted, want exactly 1", stamped, deleted, racers)
		}
	}
}

// TestSessionActivityPocket_Postgres drives the wired pocket's Refresh over
// the store: two concurrent refreshes of an idle row give two generic 401s,
// one deletion, one session_idle_expired row and no refresh_reuse; an idle
// STAMPED row (a sub-microsecond app-clock stamp) expires through the
// read-back observation, where an encoding defect would surface as an
// internal error; a legacy-rotated row is grandfathered and stamped on its
// next refresh; an unwired pocket never stamps.
func TestSessionActivityPocket_Postgres(t *testing.T) {
	db, schema, repos := activitySchema(t, "session_activity_pocket")
	ctx := context.Background()
	store := repos.Sessions.(*SessionStore)
	comps, err := newIdlePocket(t, repos, time.Hour)
	if err != nil {
		t.Fatalf("the pocket with idle expiry wired over a migrated schema = %v", err)
	}
	seedIdleUser(t, db, schema, "u-idle")

	race := seedIdleSession(t, repos, "u-idle", "tok-idle-race", time.Now().Add(-2*time.Hour))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { _, errs[i] = comps.Authentication.Refresh(ctx, "tok-idle-race") })
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, authlogic.ErrInvalidRefreshToken) {
			t.Fatalf("racing idle refresh %d = %v, want ErrInvalidRefreshToken", i, err)
		}
	}
	if _, err := store.Get(ctx, race.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("idle row after the race = %v, want sdk.ErrNotFound", err)
	}
	if n := countEvents(t, db, schema, "session_idle_expired", race.ID); n != 1 {
		t.Fatalf("session_idle_expired rows for the raced session = %d, want 1", n)
	}
	if n := countEvents(t, db, schema, "refresh_reuse", race.ID); n != 0 {
		t.Fatalf("refresh_reuse rows for the raced session = %d, want 0", n)
	}

	stamped := seedIdleSession(t, repos, "u-idle", "tok-idle-stamped", time.Now().Add(-3*time.Hour))
	if err := store.RotateAt(ctx, stamped.ID, stamped.RefreshTokenHash, sha256Hex(t, "tok-idle-stamped-2"), time.Now().Add(-2*time.Hour+333)); err != nil {
		t.Fatal(err)
	}
	if _, err := comps.Authentication.Refresh(ctx, "tok-idle-stamped-2"); !errors.Is(err, authlogic.ErrInvalidRefreshToken) {
		t.Fatalf("refresh of an idle stamped row = %v, want ErrInvalidRefreshToken (an internal error means the CAS missed the stored encoding)", err)
	}
	if n := countEvents(t, db, schema, "session_idle_expired", stamped.ID); n != 1 {
		t.Fatalf("session_idle_expired rows for the stamped session = %d, want 1", n)
	}

	legacy := seedIdleSession(t, repos, "u-idle", "tok-legacy", time.Now().Add(-3*time.Hour))
	if err := store.Rotate(ctx, legacy.ID, legacy.RefreshTokenHash, sha256Hex(t, "tok-legacy-2")); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if _, err := comps.Authentication.Refresh(ctx, "tok-legacy-2"); err != nil {
		t.Fatalf("refresh of a grandfathered legacy-rotated row = %v, want success", err)
	}
	row, err := store.Get(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, act, err := store.GetByRefreshHashWithActivity(ctx, row.RefreshTokenHash)
	if err != nil || !act.Stamped || act.LastRotatedAt.Before(before.Add(-time.Second)) {
		t.Fatalf("grandfathered row after a wired refresh = %+v, %v; want stamped now", act, err)
	}

	unwired, err := newIdlePocket(t, repos, 0)
	if err != nil {
		t.Fatal(err)
	}
	plain := seedIdleSession(t, repos, "u-idle", "tok-unwired", time.Now())
	if _, err := unwired.Authentication.Refresh(ctx, "tok-unwired"); err != nil {
		t.Fatalf("unwired refresh = %v", err)
	}
	var stamp *time.Time
	if err := db.QueryRow(ctx, `SELECT last_rotated_at FROM `+schema.Table(sessionsTable)+` WHERE id = $1`, plain.ID).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	if stamp != nil {
		t.Fatalf("an unwired refresh stamped last_rotated_at = %v, want NULL", *stamp)
	}
}

// TestSessionActivityMigrationLockTimeout_Postgres runs 0029 while another
// transaction holds ACCESS SHARE on sessions: the file's SET LOCAL
// lock_timeout aborts it (SQLSTATE 55P03) instead of queueing, nothing is
// recorded, and once the lock is released the re-run applies it.
func TestSessionActivityMigrationLockTimeout_Postgres(t *testing.T) {
	db, schema, _ := activitySchema(t, "session_activity_lock")
	ctx := context.Background()
	if _, err := db.Exec(ctx, "ALTER TABLE "+schema.Table(sessionsTable)+" DROP COLUMN last_rotated_at"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM "+schema.Table("schema_migrations")+" WHERE version = $1", sessionActivityMigration); err != nil {
		t.Fatal(err)
	}

	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, "LOCK TABLE "+schema.Table(sessionsTable)+" IN ACCESS SHARE MODE"); err != nil {
		_ = holder.Rollback()
		t.Fatal(err)
	}
	start := time.Now()
	err = pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema))
	elapsed := time.Since(start)
	_ = holder.Rollback()
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("0029 under a held ACCESS SHARE lock = %v, want the lock_timeout error", err)
	}
	if elapsed < 4*time.Second || elapsed > 30*time.Second {
		t.Fatalf("0029 gave up after %v, want about the file's 5s lock_timeout", elapsed)
	}
	t.Logf("0029 under a held ACCESS SHARE lock failed after %v: %v", elapsed.Round(time.Millisecond), err)

	var recorded bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+schema.Table("schema_migrations")+" WHERE version = $1)", sessionActivityMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded {
		t.Fatal("the timed-out 0029 was recorded in the ledger")
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("re-run after the lock was released = %v, want success", err)
	}
	if err := NewSessionStore(db, WithSchema(schema)).ProbeSchema(ctx); err != nil {
		t.Fatalf("ProbeSchema after the re-run = %v, want nil", err)
	}
}

// newIdlePocket builds the pocket over repos with SessionsConfig.IdleTimeout
// idle (zero = unwired).
func newIdlePocket(t *testing.T, repos auth.Repositories, idle time.Duration) (*auth.Components, error) {
	t.Helper()
	protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{Active: "test", Keys: map[string][]byte{"test": []byte("session-activity-test-key-32-byt")}})
	if err != nil {
		t.Fatal(err)
	}
	return auth.New(repos, identifierHTTPTestSigner{}, environment.ModeDevelopment, delivery.ModeOff,
		auth.WithPassword(auth.PasswordConfig{PasswordFlowsDisabled: true, RequireVerifiedEmail: true}),
		auth.WithIdentity(auth.IdentityConfig{ChallengeProtector: protector}),
		auth.WithSessions(auth.SessionsConfig{IdleTimeout: idle}),
		auth.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

func seedIdleUser(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema, id string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO `+schema.Table(usersTable)+` (id, display_name, created_at, updated_at) VALUES ($1, '', now(), now())`, id); err != nil {
		t.Fatal(err)
	}
}

// seedIdleSession creates a first-party session whose refresh token is
// presented, created at created.
func seedIdleSession(t *testing.T, repos auth.Repositories, userID, presented string, created time.Time) session.Session {
	t.Helper()
	sess, _ := session.NewSession(userID, 24*time.Hour, created)
	sess.RefreshTokenHash = sha256Hex(t, presented)
	out, err := repos.Sessions.Create(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sha256Hex(t *testing.T, presented string) string {
	t.Helper()
	h, err := cryptids.SHA256(presented)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func countEvents(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema, eventType, sessionID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM `+schema.Table(securityEventsTable)+` WHERE event_type = $1 AND details->>'session_id' = $2`,
		eventType, sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
