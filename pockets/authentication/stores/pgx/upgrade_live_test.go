// Live upgrade to the S3 retention migrations, in a disposable schema owned by
// a run-unique migrator (see audit_integrity_live_test.go). Skips loudly
// without a DSN.
package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestLive_RetentionUpgrade applies 0023 to a database at 0022 while another
// session holds a read of users. Before it the store refuses to boot naming
// erased_at and 0023. Every index build completes while that read is open —
// the stream waits only at the users ADD COLUMN, where a new read of users
// queues behind it and a read of an indexed table does not — and an index
// pre-built CONCURRENTLY under its name survives as the same index. After 0023
// and 0024 the rows written before them have erased_at NULL and read back
// active and deactivated, unchanged.
func TestLive_RetentionUpgrade(t *testing.T) {
	ctx := context.Background()
	l := newAuditLive(t)
	l.migrate(t, migrationsThrough(t, "0022_security_events_immutable.sql"))
	owner := l.openAs(t, l.migrator, "")
	users := l.schema.Table(usersTable)
	if _, err := owner.Exec(ctx, `INSERT INTO `+users+` (id, display_name, status, created_at, updated_at) VALUES
		('legacy-active', 'A', 'active', now(), now()), ('legacy-off', 'D', 'deactivated', now(), now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, owner, l.opts()...); !errors.Is(err, sdk.ErrNotFound) || !containsAll(err.Error(), "erased_at", retentionMigration) {
		t.Fatalf("boot before 0023 = %v, want ErrNotFound naming erased_at and %s", err, retentionMigration)
	}

	if _, err := owner.Exec(ctx, `CREATE INDEX CONCURRENTLY idx_sessions_expires_at ON `+l.schema.Table(sessionsTable)+` (expires_at)`); err != nil {
		t.Fatalf("pre-build CONCURRENTLY: %v", err)
	}
	indexOID := func() (oid uint32, valid bool) {
		t.Helper()
		if err := l.admin.QueryRow(ctx, `SELECT i.indexrelid, i.indisvalid FROM pg_catalog.pg_index i
			WHERE i.indexrelid = pg_catalog.to_regclass($1)`, l.schema.Table("idx_sessions_expires_at")).Scan(&oid, &valid); err != nil {
			t.Fatal(err)
		}
		return oid, valid
	}
	prebuilt, _ := indexOID()

	reader, err := l.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback() }()
	if _, err := reader.Exec(ctx, `SELECT count(*) FROM `+users); err != nil {
		t.Fatal(err)
	}
	migrated := make(chan error, 1)
	go func() {
		migrated <- pgxdb.RunMigrations(ctx, l.openAs(t, l.migrator, ""), migrationsThrough(t, retentionMigration), MigrationsDir, pgxdb.WithSchema(l.schema))
	}()

	var pid int
	deadline := time.Now().Add(4 * time.Second)
	for pid == 0 {
		if err := l.admin.QueryRow(ctx, `SELECT COALESCE(max(pid), 0) FROM pg_catalog.pg_stat_activity
			WHERE usename = $1 AND wait_event_type = 'Lock'`, l.migrator).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("0023 never waited on a lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	held := map[string]string{}
	var waiting []string
	rows, err := l.admin.Query(ctx, `SELECT c.relname::text, l.mode, l.granted FROM pg_catalog.pg_locks l
		JOIN pg_catalog.pg_class c ON c.oid = l.relation WHERE l.pid = $1 AND c.relkind = 'r'`, pid)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var rel, mode string
		var granted bool
		if err := rows.Scan(&rel, &mode, &granted); err != nil {
			t.Fatal(err)
		}
		if granted {
			held[rel] = mode
		} else {
			waiting = append(waiting, rel+":"+mode)
		}
	}
	rows.Close()
	if len(waiting) != 1 || waiting[0] != usersTable+":AccessExclusiveLock" {
		t.Fatalf("0023 waits on %v, want only users:AccessExclusiveLock (the ADD COLUMN)", waiting)
	}
	for _, idx := range retentionIndexes {
		if held[idx.table] != "ShareLock" {
			t.Errorf("%s holds %q on %s while waiting, want ShareLock — every index build precedes the ADD COLUMN", idx.name, held[idx.table], idx.table)
		}
	}

	probe := l.openAs(t, l.migrator, "")
	if err := probe.QueryRow(ctx, `SELECT count(*) FROM `+l.schema.Table(sessionsTable)).Scan(new(int)); err != nil {
		t.Fatalf("a read of an indexed table blocked behind 0023: %v", err)
	}
	err = probe.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '200ms'`); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM `+users).Scan(new(int))
	})
	if sqlState(err) != "57014" {
		t.Fatalf("a new read of users while the ADD COLUMN waits = %v, want it queued (statement timeout)", err)
	}

	if err := reader.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-migrated; err != nil {
		t.Fatalf("0023 after the reader committed: %v", err)
	}
	if oid, valid := indexOID(); oid != prebuilt || !valid {
		t.Fatalf("idx_sessions_expires_at = oid %d (valid %v), want the pre-built oid %d", oid, valid, prebuilt)
	}
	for _, idx := range retentionIndexes {
		var valid bool
		if err := l.admin.QueryRow(ctx, `SELECT indisvalid FROM pg_catalog.pg_index WHERE indexrelid = pg_catalog.to_regclass($1)`,
			l.schema.Table(idx.name)).Scan(&valid); err != nil || !valid {
			t.Errorf("%s after 0023: valid %v, %v", idx.name, valid, err)
		}
	}

	if _, err := Repositories(ctx, owner, l.opts()...); !errors.Is(err, sdk.ErrNotFound) || !containsAll(err.Error(), purgeFunction, railRetentionFile) {
		t.Fatalf("boot at 0023 = %v, want ErrNotFound naming %s", err, railRetentionFile)
	}
	l.migrate(t, MigrationsFS)
	repos, err := Repositories(ctx, owner, l.opts()...)
	if err != nil {
		t.Fatalf("boot after 0024: %v", err)
	}
	for id, want := range map[string]user.Status{"legacy-active": user.StatusActive, "legacy-off": user.StatusDeactivated} {
		var erasedAt *time.Time
		if err := l.admin.QueryRow(ctx, `SELECT erased_at FROM `+users+` WHERE id = $1`, id).Scan(&erasedAt); err != nil || erasedAt != nil {
			t.Errorf("%s erased_at = %v, %v; want NULL", id, erasedAt, err)
		}
		u, err := repos.Users.Get(ctx, id)
		if err != nil || u.Status != want {
			t.Errorf("%s reads %q, %v; want %q", id, u.Status, err, want)
		}
		s, err := repos.UserAdmin.GetSummary(ctx, id)
		if err != nil || s.Status != want {
			t.Errorf("%s directory row reads %q, %v; want %q", id, s.Status, err, want)
		}
	}
}
