// Live audit-rail integrity tests (migrations 0021 and 0022, the posture probe).
// Each test builds its OWN disposable schema owned by a run-unique migrator
// LOGIN role that applies the migrations — so the trigger owner is a real
// non-superuser — plus run-unique runtime roles holding grants, dropped with
// everything they own at cleanup. Roles are cluster-wide, hence the unique
// names. With POSTGRES_TEST_SCHEMA unset the stores are constructed without an
// option and the roles' search_path resolves the bare names; with it set they
// are constructed WithSchema, so each leg runs its own SQL path. Absent a DSN
// they skip loudly, like the conformance suite.
package pgx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// auditRolePassword is every fixture login role's password.
const auditRolePassword = "auth_audit_fixture_pw"

// auditLive is one disposable fixture: the superuser pool, the run-unique id
// that names the schema and every role, and the migrator that owns the schema.
type auditLive struct {
	dsn      string
	admin    *pgxdb.DB
	id       string
	schema   pgxdb.Schema
	migrator string
	qualify  bool
}

func newAuditLive(t *testing.T) *auditLive {
	t.Helper()
	return newAuditLiveNamed(t, "authaudit")
}

// newAuditLiveNamed is newAuditLive with the schema and role names prefixed by
// prefix — mixed case for a schema name that needs quotes.
func newAuditLiveNamed(t *testing.T, prefix string) *auditLive {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — audit-rail integrity NOT verified")
	}
	l := &auditLive{dsn: dsn, admin: probeDial(t), id: fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()%10_000_000_000), qualify: os.Getenv("POSTGRES_TEST_SCHEMA") != ""}
	l.schema = disposableSchema(t, l.id)
	l.migrator = l.role(t, "migrator")
	l.exec(t, `CREATE SCHEMA "`+l.id+`" AUTHORIZATION "`+l.migrator+`"`)
	t.Cleanup(func() {
		if _, err := l.admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+l.id+`" CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", l.id, err)
		}
	})
	return l
}

func (l *auditLive) exec(t *testing.T, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := l.admin.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// role creates the LOGIN role <id>_<suffix>, resolving bare names in the
// fixture schema on the unqualified leg, and drops it (and everything it owns
// or was granted) at cleanup.
func (l *auditLive) role(t *testing.T, suffix string) string {
	t.Helper()
	name := l.id + "_" + suffix
	l.exec(t, `CREATE ROLE "`+name+`" LOGIN PASSWORD '`+auditRolePassword+`'`)
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := l.admin.Exec(ctx, `DROP OWNED BY "`+name+`" CASCADE`); err != nil {
			t.Logf("drop owned by %s: %v", name, err)
		}
		if _, err := l.admin.Exec(ctx, `DROP ROLE IF EXISTS "`+name+`"`); err != nil {
			t.Logf("drop role %s: %v", name, err)
		}
	})
	if !l.qualify {
		l.exec(t, `ALTER ROLE "`+name+`" SET search_path = "`+l.id+`"`)
	}
	return name
}

// runtimeRole creates a runtime role with USAGE on the schema and privileges on
// every table in it.
func (l *auditLive) runtimeRole(t *testing.T, suffix, privileges string) string {
	t.Helper()
	name := l.role(t, suffix)
	l.exec(t,
		`GRANT USAGE ON SCHEMA "`+l.id+`" TO "`+name+`"`,
		`GRANT `+privileges+` ON ALL TABLES IN SCHEMA "`+l.id+`" TO "`+name+`"`,
	)
	return name
}

// openAs opens the database as role under mode. A non-zero mode acknowledges
// the plaintext test DSN.
func (l *auditLive) openAs(t *testing.T, role string, mode environment.Mode) *pgxdb.DB {
	t.Helper()
	return l.openWith(t, role, mode, slog.New(slog.DiscardHandler))
}

// openWith is openAs with the connector's posture log sent to logger.
func (l *auditLive) openWith(t *testing.T, role string, mode environment.Mode, logger *slog.Logger) *pgxdb.DB {
	t.Helper()
	u, err := url.Parse(l.dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	u.User = url.UserPassword(role, auditRolePassword)
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{
		DSN: sdk.Secret(u.String()), MaxConns: 2, RuntimeMode: mode, TLSDisabledAcknowledged: mode != "",
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("open as %s: %v", role, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// migrate applies fsys as the migrator, into the fixture schema.
func (l *auditLive) migrate(t *testing.T, fsys fs.FS) {
	t.Helper()
	if err := pgxdb.RunMigrations(context.Background(), l.openAs(t, l.migrator, ""), fsys, MigrationsDir, pgxdb.WithSchema(l.schema)); err != nil {
		t.Fatalf("migrate as %s: %v", l.migrator, err)
	}
}

// opts are the store options for this leg.
func (l *auditLive) opts() []Option {
	if l.qualify {
		return []Option{WithSchema(l.schema)}
	}
	return nil
}

// events is security_events, schema-qualified, for raw SQL.
func (l *auditLive) events() string { return l.schema.Table(securityEventsTable) }

// migrationsThrough is the embedded stream up to and including last.
func migrationsThrough(t *testing.T, last string) fstest.MapFS {
	t.Helper()
	prior := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() > last {
			continue
		}
		data, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		prior[MigrationsDir+"/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	return prior
}

func auditEvent(t *testing.T, repos auth.Repositories, userID string) securityevent.SecurityEvent {
	t.Helper()
	evt := securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, time.Now())
	evt.UserID = userID
	evt.RequestID, evt.TraceID = "req-"+userID, "trace-"+userID
	created, err := repos.SecurityEvents.Create(context.Background(), evt)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return created
}

func forbidden(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("%s = %v, want sdk.ErrForbidden", what, err)
	}
}

// TestLive_SecurityEventsImmutable proves the 0022 trigger pair: a runtime role
// holding UPDATE, DELETE and TRUNCATE boots in production (the verified trigger
// protects the table) yet every rewrite is refused; the owning migrator's UPDATE
// persists and its DELETE passes; a migrator-owned SECURITY DEFINER function
// deletes on a non-owner's behalf; and the owner itself is refused in production.
func TestLive_SecurityEventsImmutable(t *testing.T) {
	ctx := context.Background()
	l := newAuditLive(t)
	l.migrate(t, MigrationsFS)
	writer := l.runtimeRole(t, "writer", "SELECT, INSERT, UPDATE, DELETE, TRUNCATE")

	writerDB := l.openAs(t, writer, environment.ModeProduction)
	repos, err := Repositories(ctx, writerDB, l.opts()...)
	if err != nil {
		t.Fatalf("Repositories as a trigger-protected runtime role in production: %v", err)
	}
	first := auditEvent(t, repos, "u-first")
	second := auditEvent(t, repos, "u-second")

	_, err = writerDB.Exec(ctx, `UPDATE `+l.events()+` SET user_agent = 'rewritten' WHERE id = $1`, first.ID)
	forbidden(t, "non-owner UPDATE", err)
	_, err = writerDB.Exec(ctx, `DELETE FROM `+l.events()+` WHERE id = $1`, first.ID)
	forbidden(t, "non-owner DELETE", err)
	_, err = writerDB.Exec(ctx, `TRUNCATE `+l.events())
	forbidden(t, "non-owner TRUNCATE", err)
	if got := countRows(t, l.admin, l.events()); got != 2 {
		t.Fatalf("rows after refused rewrites = %d, want 2", got)
	}

	owner := l.openAs(t, l.migrator, "")
	if _, err := owner.Exec(ctx, `UPDATE `+l.events()+` SET user_agent = 'owner-update' WHERE id = $1`, first.ID); err != nil {
		t.Fatalf("owner UPDATE: %v", err)
	}
	var ua string
	if err := l.admin.QueryRow(ctx, `SELECT user_agent FROM `+l.events()+` WHERE id = $1`, first.ID).Scan(&ua); err != nil || ua != "owner-update" {
		t.Fatalf("owner UPDATE did not persist: user_agent = %q, %v", ua, err)
	}
	if _, err := owner.Exec(ctx, `DELETE FROM `+l.events()+` WHERE id = $1`, first.ID); err != nil {
		t.Fatalf("owner DELETE: %v", err)
	}

	purge := l.schema.Table("purge_security_event")
	if _, err := owner.Exec(ctx, `CREATE FUNCTION `+purge+`(target TEXT) RETURNS BIGINT LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp AS $purge$
DECLARE n BIGINT;
BEGIN
  DELETE FROM `+l.events()+` WHERE id = target;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$purge$`); err != nil {
		t.Fatalf("create SECURITY DEFINER function: %v", err)
	}
	l.exec(t, `GRANT EXECUTE ON FUNCTION `+purge+`(TEXT) TO "`+writer+`"`)
	var purged int64
	if err := writerDB.QueryRow(ctx, `SELECT `+purge+`($1)`, second.ID).Scan(&purged); err != nil || purged != 1 {
		t.Fatalf("SECURITY DEFINER delete as a non-owner = %d, %v; want 1 row", purged, err)
	}
	if got := countRows(t, l.admin, l.events()); got != 0 {
		t.Fatalf("rows after owner delete and purge = %d, want 0", got)
	}

	ownerProduction := l.openAs(t, l.migrator, environment.ModeProduction)
	if _, err := Repositories(ctx, ownerProduction, l.opts()...); !errors.Is(err, pgxdb.ErrAppendOnlyTableWritable) {
		t.Fatalf("Repositories as the table owner in production = %v, want ErrAppendOnlyTableWritable", err)
	}
	if _, err := Repositories(ctx, l.openAs(t, l.migrator, environment.ModeDevelopment), l.opts()...); err != nil {
		t.Fatalf("Repositories as the table owner in development: %v", err)
	}
}

// TestLive_SecurityEventsEvidenceColumns proves 0021's defaults: rows the store
// inserts — naming neither column — carry a non-NULL, increasing seq and a
// non-NULL recorded_at, and the correlation ids round-trip.
func TestLive_SecurityEventsEvidenceColumns(t *testing.T) {
	ctx := context.Background()
	l := newAuditLive(t)
	l.migrate(t, MigrationsFS)
	writer := l.runtimeRole(t, "writer", "SELECT, INSERT")
	repos, err := Repositories(ctx, l.openAs(t, writer, environment.ModeProduction), l.opts()...)
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	for i := 0; i < 3; i++ {
		auditEvent(t, repos, fmt.Sprintf("u-%d", i))
	}

	rows, err := l.admin.Query(ctx, `SELECT user_id, request_id, trace_id, seq, recorded_at FROM `+l.events()+` ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var (
		n       int
		lastSeq int64
		lastAt  time.Time
	)
	for rows.Next() {
		var (
			userID, requestID, traceID string
			seq                        *int64
			recordedAt                 *time.Time
		)
		if err := rows.Scan(&userID, &requestID, &traceID, &seq, &recordedAt); err != nil {
			t.Fatal(err)
		}
		if seq == nil || recordedAt == nil {
			t.Fatalf("%s: seq = %v, recorded_at = %v; want both set by the defaults", userID, seq, recordedAt)
		}
		if n > 0 && (*seq <= lastSeq || recordedAt.Before(lastAt)) {
			t.Fatalf("%s: seq %d after %d, recorded_at %v after %v; want increasing", userID, *seq, lastSeq, *recordedAt, lastAt)
		}
		if requestID != "req-"+userID || traceID != "trace-"+userID {
			t.Fatalf("%s: request_id %q, trace_id %q", userID, requestID, traceID)
		}
		lastSeq, lastAt = *seq, *recordedAt
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("read %d rows, want 3", n)
	}
}

// TestLive_SecurityEventsPreIntegrityRow proves the upgrade: before 0021 the
// store refuses to boot naming the file; a row written before it keeps NULL
// seq and recorded_at — no row is stamped with the migration time — and reads
// back with empty correlation ids.
func TestLive_SecurityEventsPreIntegrityRow(t *testing.T) {
	ctx := context.Background()
	l := newAuditLive(t)
	l.migrate(t, migrationsThrough(t, "0020_external_subjects.sql"))
	owner := l.openAs(t, l.migrator, "")
	if _, err := Repositories(ctx, owner, l.opts()...); !errors.Is(err, sdk.ErrNotFound) || !containsAll(err.Error(), "request_id", "0021_security_events_integrity.sql") {
		t.Fatalf("boot before 0021 = %v, want ErrNotFound naming request_id and 0021", err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO `+l.events()+` (id, user_id, event_type, event_status, created_at) VALUES ('legacy-row', 'u-legacy', 'login', 'success', now())`); err != nil {
		t.Fatal(err)
	}

	l.migrate(t, MigrationsFS)
	var seq *int64
	var recordedAt *time.Time
	if err := l.admin.QueryRow(ctx, `SELECT seq, recorded_at FROM `+l.events()+` WHERE id = 'legacy-row'`).Scan(&seq, &recordedAt); err != nil {
		t.Fatal(err)
	}
	if seq != nil || recordedAt != nil {
		t.Fatalf("pre-0021 row: seq = %v, recorded_at = %v; want both NULL", seq, recordedAt)
	}
	repos, err := Repositories(ctx, owner, l.opts()...)
	if err != nil {
		t.Fatalf("boot after 0022: %v", err)
	}
	page, err := repos.SecurityEvents.List(ctx, securityevent.ListFilter{UserID: "u-legacy"}, list.Request{Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != "" || page.Items[0].TraceID != "" {
		t.Fatalf("legacy row read = %+v, %v", page.Items, err)
	}
}

// TestLive_SecurityEventsSequenceTrap proves a runtime role with TABLE grants
// only cannot append once the PUBLIC sequence grant is revoked — the production
// boot refuses it with ErrAppendOnlyTableUnwritable — and that granting USAGE on
// the sequence is the fix.
func TestLive_SecurityEventsSequenceTrap(t *testing.T) {
	ctx := context.Background()
	l := newAuditLive(t)
	l.migrate(t, MigrationsFS)
	seq := l.schema.Table("security_events_seq")
	if _, err := l.openAs(t, l.migrator, "").Exec(ctx, `REVOKE USAGE ON SEQUENCE `+seq+` FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	trap := l.runtimeRole(t, "tablegrants", "SELECT, INSERT")

	db := l.openAs(t, trap, environment.ModeProduction)
	_, err := Repositories(ctx, db, l.opts()...)
	if !errors.Is(err, pgxdb.ErrAppendOnlyTableUnwritable) || errors.Is(err, pgxdb.ErrAppendOnlyTableWritable) {
		t.Fatalf("table grants without sequence USAGE = %v, want only ErrAppendOnlyTableUnwritable", err)
	}
	if !containsAll(err.Error(), "sequence_usage=false", "GRANT USAGE ON SEQUENCE") {
		t.Fatalf("refusal does not name the sequence fix: %v", err)
	}
	unprobed := NewSecurityEventStore(db, l.opts()...)
	if _, err := unprobed.Create(ctx, securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, time.Now())); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("insert without sequence USAGE = %v, want sdk.ErrForbidden — the row the probe protects", err)
	}

	l.exec(t, `GRANT USAGE ON SEQUENCE `+seq+` TO "`+trap+`"`)
	repos, err := Repositories(ctx, db, l.opts()...)
	if err != nil {
		t.Fatalf("Repositories after GRANT USAGE ON SEQUENCE: %v", err)
	}
	auditEvent(t, repos, "u-granted")
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
