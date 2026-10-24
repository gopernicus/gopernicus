//go:build integration

// The rail-purge and erasure cases against a libSQL server
// (TURSO_DATABASE_URL, TURSO_AUTH_TOKEN): the capture-drop-delete-restore-
// evidence transaction over the remote protocol, its fault rollback, and the
// timings of a 1000-row purge batch and an Erase over a realistic rail, which
// the README records. Absent a URL they skip loudly.
package turso

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// railServer dials the server, migrates, truncates and restores the trigger
// pair at cleanup.
func railServer(t *testing.T) *tursodb.DB {
	t.Helper()
	db := probeDial(t)
	if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DROP TRIGGER IF EXISTS host_mirror_insert`)
		_, _ = db.Exec(context.Background(), `DROP TABLE IF EXISTS host_mirror`)
		truncate(t, db)
	})
	return db
}

// seedRailBulk appends n rail rows age old in one statement.
func seedRailBulk(t *testing.T, db *tursodb.DB, n int, age time.Duration) {
	t.Helper()
	base := time.Now().UTC().Add(-age)
	if _, err := db.Exec(context.Background(), `WITH RECURSIVE g(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM g WHERE i < ?)
		INSERT INTO security_events (id, user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, created_at)
		SELECT 'bulk-' || i, 'u-' || (i % 500), 'user', 'u-' || (i % 500), 'login', 'success', '{"reason":"bulk"}', '203.0.113.9', 'agent/1.0',
			strftime('%Y-%m-%dT%H:%M:%S', ?, '+' || i || ' seconds') || '.000000000Z'
		FROM g`, n, base.Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("seed %d rail rows: %v", n, err)
	}
}

// TestRailPurge_Turso proves the purge transaction on the server: a host AFTER
// INSERT trigger and the 0022 pair are restored byte-identical, the host
// trigger fires for the evidence row, and a fault after the DROP TRIGGERs
// rolls back to the same set with every row in place.
func TestRailPurge_Turso(t *testing.T) {
	ctx := context.Background()
	db := railServer(t)
	for _, stmt := range []string{
		`CREATE TABLE host_mirror (id TEXT NOT NULL, event_type TEXT NOT NULL)`,
		`CREATE TRIGGER host_mirror_insert AFTER INSERT ON security_events
BEGIN
  INSERT INTO host_mirror (id, event_type) VALUES (NEW.id, NEW.event_type);
END`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	repos := mustRepos(t, db)
	seedRailBulk(t, db, 20, 500*railDay)
	before := railTriggerSQL(t, db)
	if len(before) != 3 {
		t.Fatalf("captured %d triggers, want 3", len(before))
	}

	store := NewMaintenanceStore(db)
	store.fault = func(_ context.Context, _ *tursodb.Tx, stage string) error {
		if stage == stageDropped {
			return errInjected
		}
		return nil
	}
	_, err := store.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*railDay), 10)
	if !errors.Is(err, errInjected) {
		t.Fatalf("faulted purge = %v, want the injected fault", err)
	}
	if after := railTriggerSQL(t, db); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("triggers after the rollback:\n%v\nbefore:\n%v", after, before)
	}
	if got := railCount(t, db, ""); got != 20 {
		t.Fatalf("rows after the rollback = %d, want 20", got)
	}

	res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*railDay), 15)
	if err != nil || res.Count != 15 {
		t.Fatalf("purge = %+v, %v; want 15", res, err)
	}
	if after := railTriggerSQL(t, db); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("triggers after the purge:\n%v\nbefore:\n%v", after, before)
	}
	if got := railCount(t, db, "login"); got != 5 {
		t.Fatalf("rows left = %d, want 5", got)
	}
	var mirrored int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM host_mirror WHERE event_type = ?`, securityevent.TypeSecurityEventsPurged).Scan(&mirrored); err != nil || mirrored != 1 {
		t.Fatalf("the host trigger saw %d purge rows (%v), want 1", mirrored, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM security_events`); err == nil {
		t.Fatal("a DELETE after the purge passed — the 0022 trigger was not restored")
	}
}

// TestRailPurgeAndEraseTimings_Turso times, over the server, one full purge
// batch (maintenance.Config's default 1000 rows) against a 20,000-row rail and
// one Erase of a user holding sessions, identifiers and rail rows — each its
// own BEGIN IMMEDIATE transaction, holding the write lock for its length.
func TestRailPurgeAndEraseTimings_Turso(t *testing.T) {
	ctx := context.Background()
	db := railServer(t)
	repos := mustRepos(t, db)
	seedRailBulk(t, db, 20000, 500*railDay)

	start := time.Now()
	res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*railDay), 1000)
	purgeTook := time.Since(start)
	if err != nil || res.Count != 1000 {
		t.Fatalf("purge = %+v, %v", res, err)
	}

	now := time.Now().UTC()
	u, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx,
		user.User{DisplayName: "Timed", Status: user.StatusActive, CreatedAt: now, UpdatedAt: now},
		identifier.Identifier{Kind: identifier.KindEmail, NormalizedValue: "timed@example.com", VerifiedAt: now, LoginEnabled: true, IsPrimary: true, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		s, _ := session.NewSession(u.ID, time.Hour, now)
		s.RefreshTokenHash = fmt.Sprintf("timed-%d", i)
		if _, err := repos.Sessions.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	start = time.Now()
	erasure, err := repos.UserErasure.Erase(ctx, user.EraseInput{ID: u.ID, Now: time.Now(), Event: securityevent.SecurityEvent{
		UserID: u.ID, Actor: securityevent.Principal{Type: "user", ID: "timer"}, EventType: securityevent.TypeUserErased,
		EventStatus: securityevent.StatusSuccess, CreatedAt: time.Now()}})
	eraseTook := time.Since(start)
	if err != nil || !erasure.Changed || erasure.Counts.Sessions != 10 {
		t.Fatalf("erase = %+v, %v", erasure, err)
	}
	t.Logf("libSQL server timings: purge of %d rows from a 20000-row rail = %v; erase (10 sessions, 1 identifier) = %v",
		res.Count, purgeTook, eraseTook)
}
