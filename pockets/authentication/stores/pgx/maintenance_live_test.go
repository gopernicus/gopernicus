// Live retention tests (migrations 0023 and 0024, the purge function, the
// probe). Each test builds its own disposable schema owned by a run-unique
// NOSUPERUSER migrator that applies the migrations — so 0024's functions are
// owned by a real non-superuser — and connects as a run-unique runtime role in
// the S2 posture: full DML on the plain tables, and on security_events only
// SELECT and INSERT (UPDATE, DELETE and TRUNCATE revoked), so the rail purge
// can run only through the definer function. Under POSTGRES_TEST_SCHEMA the
// fixture schema's name is mixed case (it needs quotes) and the stores are
// constructed WithSchema; without it the roles resolve bare names on their
// search_path. Absent a DSN they skip loudly.
package pgx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// retentionDay is one calendar day.
const retentionDay = 24 * time.Hour

// syncBuffer is a log sink safe for concurrent writers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// Reset discards everything written so far.
func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// warnLogger returns a WARN-and-above text logger writing to buf.
func warnLogger(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// captureDefaultLog routes slog.Default — where the store logs its WARNs — to a
// buffer for the rest of the test. Tests using it never run in parallel.
func captureDefaultLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(warnLogger(buf))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// newRetentionLive is the fixture: a mixed-case schema on the schema leg.
func newRetentionLive(t *testing.T) *auditLive {
	t.Helper()
	prefix := "authretention"
	if os.Getenv("POSTGRES_TEST_SCHEMA") != "" {
		prefix = "AuthRetention"
	}
	return newAuditLiveNamed(t, prefix)
}

// purgeFn is the purge function's signature under the fixture schema.
func (l *auditLive) purgeFn() string { return l.schema.Table(purgeFunction) + purgeFunctionArgs }

// postureRole creates a runtime role in the S2 posture — full DML on every
// table but the rail, SELECT and INSERT on the rail — with or without EXECUTE
// on the purge function.
func (l *auditLive) postureRole(t *testing.T, suffix string, execute bool) string {
	t.Helper()
	name := l.runtimeRole(t, suffix, "SELECT, INSERT, UPDATE, DELETE")
	l.exec(t, `REVOKE UPDATE, DELETE, TRUNCATE ON `+l.events()+` FROM "`+name+`"`)
	if execute {
		l.exec(t, `GRANT EXECUTE ON FUNCTION `+l.purgeFn()+` TO "`+name+`"`)
	}
	return name
}

// asOwner runs statements as the migrator, the owner of the rail and the
// functions.
func (l *auditLive) asOwner(t *testing.T, statements ...string) {
	t.Helper()
	owner := l.openAs(t, l.migrator, "")
	for _, stmt := range statements {
		if _, err := owner.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("as owner: %s: %v", stmt, err)
		}
	}
}

// seedOldEvents appends n rail rows ages old through repos.
func seedOldEvents(t *testing.T, repos auth.Repositories, userID string, n int, age time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-age).Truncate(time.Microsecond)
	for i := range n {
		evt := securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, at.Add(time.Duration(i)*time.Second))
		evt.UserID = userID
		if _, err := repos.SecurityEvents.Create(context.Background(), evt); err != nil {
			t.Fatalf("seed rail row: %v", err)
		}
	}
}

// purgeRows reads the security_events_purged rows' details, oldest first.
func (l *auditLive) purgeRows(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := l.admin.Query(context.Background(), `SELECT actor_type, actor_id, details FROM `+l.events()+`
		WHERE event_type = 'security_events_purged' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var actorType, actorID string
		var details map[string]any
		if err := rows.Scan(&actorType, &actorID, &details); err != nil {
			t.Fatal(err)
		}
		if actorType != "system" || actorID != "maintenance" {
			t.Errorf("purge row actor = %s/%s, want system/maintenance", actorType, actorID)
		}
		out = append(out, details)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// sqlState returns err's SQLSTATE, or "".
func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// TestLive_SecurityEventsPurgeFunction proves 0024 under the runtime posture:
// a role with EXECUTE (and no UPDATE, DELETE or TRUNCATE on the rail) boots in
// production with no WARN, purges through the function and appends one row per
// call — a zero count included — naming itself as session_user; max_rows
// clamps to [1, 10000]; the floor refuses a day short of 365 days with 22023
// and accepts a day past it; NULL is refused; the runtime role cannot replace,
// alter, re-own or drop the functions; the function's proconfig pins the
// table's schema, quoted when it needs quotes; and on the bare leg the probe
// resolves the function on the role's search_path before it pins its own.
func TestLive_SecurityEventsPurgeFunction(t *testing.T) {
	ctx := context.Background()
	l := newRetentionLive(t)
	l.migrate(t, MigrationsFS)
	runtime := l.postureRole(t, "runtime", true)

	defaultLog := captureDefaultLog(t)
	connectorLog := &syncBuffer{}
	db := l.openWith(t, runtime, environment.ModeProduction, warnLogger(connectorLog))
	connectorLog.Reset() // the plaintext test DSN's TLS waiver
	repos, err := Repositories(ctx, db, l.opts()...)
	if err != nil {
		t.Fatalf("production Repositories as the posture role: %v", err)
	}
	if defaultLog.String() != "" || connectorLog.String() != "" {
		t.Fatalf("production boot with EXECUTE logged:\nstore: %s\nconnector: %s", defaultLog, connectorLog)
	}

	report, err := pgxdb.InspectTablePrivileges(ctx, db, "", AuditTables(l.opts()...)...)
	if err != nil {
		t.Fatal(err)
	}
	signature := AuditTables(l.opts()...)[0].Functions[0].Signature
	fn := report.Tables[0].Functions[signature]
	if !fn.Exists || !fn.Execute || !fn.SecurityDefiner || !fn.SearchPathPinned || !fn.SameNamespace || fn.PublicExecute ||
		len(report.Tables[0].UnsafeFunctions()) != 0 || len(report.Tables[0].InoperativeFunctions()) != 0 {
		t.Fatalf("function evidence for %s = %+v", signature, fn)
	}
	if report.Tables[0].Writable() || report.Tables[0].CannotAppend() {
		t.Fatalf("the posture role's rail evidence = %+v, want append-only", report.Tables[0])
	}

	var proconfig []string
	var secdef, public bool
	if err := l.admin.QueryRow(ctx, `SELECT p.proconfig, p.prosecdef, pg_catalog.has_function_privilege('public', p.oid, 'EXECUTE')
		FROM pg_catalog.pg_proc p WHERE p.oid = pg_catalog.to_regprocedure($1)`, l.purgeFn()).Scan(&proconfig, &secdef, &public); err != nil {
		t.Fatal(err)
	}
	wantSchema := l.id
	if strings.ToLower(l.id) != l.id {
		wantSchema = `"` + l.id + `"`
	}
	if len(proconfig) != 1 || proconfig[0] != "search_path=pg_catalog, "+wantSchema+", pg_temp" || !secdef || public {
		t.Fatalf("proconfig = %q, prosecdef = %v, public execute = %v; want [search_path=pg_catalog, %s, pg_temp], true, false", proconfig, secdef, public, wantSchema)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	seedOldEvents(t, repos, "u-old", 3, 500*retentionDay)
	auditEvent(t, repos, "u-fresh")
	cutoff := now.Add(-400 * retentionDay)
	res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, cutoff, 10)
	if err != nil || res.Count != 3 || !res.Cutoff.Equal(cutoff) {
		t.Fatalf("purge as the posture role = %+v, %v; want 3 rows at %v", res, err, cutoff)
	}
	if res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, cutoff, 10); err != nil || res.Count != 0 {
		t.Fatalf("empty purge = %+v, %v; want count 0", res, err)
	}
	purges := l.purgeRows(t)
	if len(purges) != 2 {
		t.Fatalf("purge rows = %d, want one per call", len(purges))
	}
	for i, want := range []float64{3, 0} {
		d := purges[i]
		if d["count"] != want || d["floor_days"] != float64(365) || d["session_user"] != runtime || d["max_rows"] != float64(10) {
			t.Errorf("purge row %d details = %v", i, d)
		}
		if raw, _ := d["cutoff"].(string); raw != cutoff.Format("2006-01-02T15:04:05.000000Z") {
			t.Errorf("purge row %d cutoff = %v, want %s", i, d["cutoff"], cutoff.Format("2006-01-02T15:04:05.000000Z"))
		}
	}

	call := `SELECT cutoff, purged FROM ` + l.schema.Table(purgeFunction) + `($1::pg_catalog.timestamptz, $2::pg_catalog.int4)`
	seedOldEvents(t, repos, "u-clamp", 3, 500*retentionDay)
	for _, tc := range []struct {
		maxRows          int32
		purged, recorded int64
	}{{0, 1, 1}, {-7, 1, 1}, {20000, 1, 10000}} {
		var at time.Time
		var purged int64
		if err := db.QueryRow(ctx, call, cutoff, tc.maxRows).Scan(&at, &purged); err != nil || purged != tc.purged {
			t.Fatalf("max_rows %d: purged %d, %v; want %d", tc.maxRows, purged, err, tc.purged)
		}
		last := l.purgeRows(t)
		if got := last[len(last)-1]["max_rows"]; got != float64(tc.recorded) {
			t.Errorf("max_rows %d recorded as %v, want %d", tc.maxRows, got, tc.recorded)
		}
	}

	if _, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, now.Add(-364*retentionDay), 10); !errors.Is(err, maintenance.ErrBelowRetentionFloor) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("floor - 1 day: %v, want ErrBelowRetentionFloor", err)
	}
	if _, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, now.Add(-366*retentionDay), 10); err != nil {
		t.Fatalf("floor + 1 day: %v", err)
	}
	var at time.Time
	var purged int64
	if err := db.QueryRow(ctx, call, nil, int32(10)).Scan(&at, &purged); sqlState(err) != belowFloorSQLState {
		t.Fatalf("NULL cutoff: %v, want SQLSTATE 22023", err)
	}

	for _, stmt := range []string{
		`CREATE OR REPLACE FUNCTION ` + l.schema.Table(floorFunction) + `() RETURNS pg_catalog.interval LANGUAGE sql STABLE AS $f$SELECT '1 day'::pg_catalog.interval$f$`,
		`ALTER FUNCTION ` + l.purgeFn() + ` SECURITY INVOKER`,
		`ALTER FUNCTION ` + l.purgeFn() + ` RESET search_path`,
		`DROP FUNCTION ` + l.purgeFn(),
		`ALTER FUNCTION ` + l.purgeFn() + ` OWNER TO "` + runtime + `"`,
	} {
		if _, err := db.Exec(ctx, stmt); sqlState(err) != forbiddenSQLState {
			t.Errorf("as the runtime role: %s = %v, want SQLSTATE 42501", stmt, err)
		}
	}
	if err := l.admin.QueryRow(ctx, `SELECT `+l.schema.Table(floorFunction)+`() = interval '365 days'`).Scan(new(bool)); err != nil {
		t.Fatalf("floor function after the refused replace: %v", err)
	}
}

// TestLive_SecurityEventsPurgeWithoutExecute proves the degraded boot: a
// posture role without EXECUTE boots in production with exactly one
// maintenance_execute=missing WARN, the rail purge fails with sdk.ErrForbidden
// naming the GRANT, and a maintenance pass reports the security_events step
// forbidden while every other step runs; granting EXECUTE clears it.
func TestLive_SecurityEventsPurgeWithoutExecute(t *testing.T) {
	ctx := context.Background()
	l := newRetentionLive(t)
	l.migrate(t, MigrationsFS)
	runtime := l.postureRole(t, "noexec", false)

	defaultLog := captureDefaultLog(t)
	db := l.openWith(t, runtime, environment.ModeProduction, warnLogger(&syncBuffer{}))
	repos, err := Repositories(ctx, db, l.opts()...)
	if err != nil {
		t.Fatalf("production Repositories without EXECUTE: %v", err)
	}
	if n := strings.Count(defaultLog.String(), "maintenance_execute=missing"); n != 1 || strings.Count(defaultLog.String(), "level=WARN") != 1 ||
		!strings.Contains(defaultLog.String(), "GRANT EXECUTE ON FUNCTION") {
		t.Fatalf("degraded boot logged %d maintenance_execute WARNs:\n%s", n, defaultLog)
	}
	_, err = repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*retentionDay), 10)
	if !errors.Is(err, sdk.ErrForbidden) || !containsAll(err.Error(), "GRANT EXECUTE ON FUNCTION", purgeFunction+purgeFunctionArgs) {
		t.Fatalf("purge without EXECUTE = %v, want sdk.ErrForbidden naming the GRANT", err)
	}

	passLog := &syncBuffer{}
	svc, err := maintenance.New(maintenance.Repositories{Maintenance: repos.Maintenance, Challenges: repos.Challenges, SecurityEvents: repos.SecurityEvents},
		maintenance.Config{}, maintenance.WithLogger(slog.New(slog.NewTextHandler(passLog, nil))))
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := svc.Pass(ctx)
	for _, step := range rep.Steps {
		switch {
		case step.Name == maintenance.StepSecurityEvents:
			if !errors.Is(step.Err, sdk.ErrForbidden) {
				t.Errorf("security_events step = %+v, want forbidden", step)
			}
		case step.Err != nil || step.Skipped:
			t.Errorf("step %s = %+v, want it to run", step.Name, step)
		}
	}
	if !strings.Contains(passLog.String(), "step=security_events count=0 skipped=false error_kind=forbidden") {
		t.Errorf("pass log lacks the forbidden rail step:\n%s", passLog)
	}

	l.exec(t, `GRANT EXECUTE ON FUNCTION `+l.purgeFn()+` TO "`+runtime+`"`)
	if res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*retentionDay), 10); err != nil || res.Count != 0 {
		t.Fatalf("purge after GRANT EXECUTE = %+v, %v", res, err)
	}
}

// TestLive_SecurityEventsPurgeFunctionTampered proves the probe's split on the
// owner's own changes: PUBLIC EXECUTE or a reset search_path refuses production
// with ErrDefinerFunctionUnsafe naming the fix; SECURITY INVOKER boots with the
// connector's inoperative WARN and fails at call time; a dropped function or
// floor is the store's refusal naming 0024 in every mode.
func TestLive_SecurityEventsPurgeFunctionTampered(t *testing.T) {
	ctx := context.Background()
	l := newRetentionLive(t)
	l.migrate(t, MigrationsFS)
	runtime := l.postureRole(t, "runtime", true)
	captureDefaultLog(t)
	connectorLog := &syncBuffer{}
	production := l.openWith(t, runtime, environment.ModeProduction, warnLogger(connectorLog))
	boot := func() (auth.Repositories, error) { return Repositories(ctx, production, l.opts()...) }

	l.asOwner(t, `GRANT EXECUTE ON FUNCTION `+l.purgeFn()+` TO PUBLIC`)
	if _, err := boot(); !errors.Is(err, pgxdb.ErrDefinerFunctionUnsafe) || !containsAll(err.Error(), purgeFunction, "public_execute=true", "REVOKE EXECUTE ON FUNCTION") {
		t.Fatalf("production boot with PUBLIC EXECUTE = %v, want ErrDefinerFunctionUnsafe naming the REVOKE", err)
	}
	l.asOwner(t, `REVOKE EXECUTE ON FUNCTION `+l.purgeFn()+` FROM PUBLIC`)
	if _, err := boot(); err != nil {
		t.Fatalf("production boot after REVOKE: %v", err)
	}

	l.asOwner(t, `ALTER FUNCTION `+l.purgeFn()+` RESET search_path`)
	if _, err := boot(); !errors.Is(err, pgxdb.ErrDefinerFunctionUnsafe) || !strings.Contains(err.Error(), "search_path_pinned=false") {
		t.Fatalf("production boot with an unpinned search_path = %v, want ErrDefinerFunctionUnsafe", err)
	}
	pin := l.id
	if strings.ToLower(pin) != pin {
		pin = `"` + pin + `"`
	}
	l.asOwner(t, `ALTER FUNCTION `+l.purgeFn()+` SET search_path = pg_catalog, `+pin+`, pg_temp`)

	l.asOwner(t, `ALTER FUNCTION `+l.purgeFn()+` SECURITY INVOKER`)
	repos, err := boot()
	if err != nil {
		t.Fatalf("production boot with SECURITY INVOKER must not refuse: %v", err)
	}
	if !containsAll(connectorLog.String(), "definer_function=inoperative", "security_definer=false", "SECURITY DEFINER") {
		t.Fatalf("SECURITY INVOKER boot lacks the inoperative WARN:\n%s", connectorLog)
	}
	seedOldEvents(t, repos, "u-invoker", 1, 500*retentionDay)
	_, err = repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*retentionDay), 10)
	if !errors.Is(err, sdk.ErrForbidden) || !strings.Contains(err.Error(), "refused the delete") {
		t.Fatalf("purge through a SECURITY INVOKER function = %v, want sdk.ErrForbidden naming the definer fix", err)
	}
	l.asOwner(t, `ALTER FUNCTION `+l.purgeFn()+` SECURITY DEFINER`)
	if res, err := repos.Maintenance.PurgeSecurityEventsBefore(ctx, time.Now().Add(-400*retentionDay), 10); err != nil || res.Count != 1 {
		t.Fatalf("purge after SECURITY DEFINER is restored = %+v, %v", res, err)
	}

	for _, drop := range []struct{ stmt, name string }{
		{`DROP FUNCTION ` + l.purgeFn(), purgeFunction},
		{`DROP FUNCTION ` + l.schema.Table(floorFunction) + `()`, floorFunction},
	} {
		l.asOwner(t, drop.stmt)
		for _, mode := range []environment.Mode{"", environment.ModeProduction} {
			if _, err := Repositories(ctx, l.openAs(t, runtime, mode), l.opts()...); !errors.Is(err, sdk.ErrNotFound) || !containsAll(err.Error(), drop.name, railRetentionFile) {
				t.Fatalf("%q boot after %s = %v, want ErrNotFound naming %s and %s", mode, drop.stmt, err, drop.name, railRetentionFile)
			}
		}
		if err := l.applyRailRetention(t, l.migrator, `"`+l.id+`"`); err != nil {
			t.Fatalf("re-apply %s: %v", railRetentionFile, err)
		}
	}
}

// applyRailRetention runs 0024's text as role under searchPath, outside the
// ledger, in one transaction.
func (l *auditLive) applyRailRetention(t *testing.T, role, searchPath string) error {
	t.Helper()
	ctx := context.Background()
	file := readMigration(t, railRetentionFile)
	return l.openAs(t, role, "").InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL search_path TO `+searchPath); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, file)
		return err
	})
}

// TestLive_SecurityEventsPurgeVersusErase runs rail purges and session purges
// against erasures of users whose expired sessions and old rail rows overlap
// them: every call completes and none deadlocks (SQLSTATE 40P01).
func TestLive_SecurityEventsPurgeVersusErase(t *testing.T) {
	ctx := context.Background()
	l := newRetentionLive(t)
	l.migrate(t, MigrationsFS)
	runtime := l.postureRole(t, "runtime", true)
	repos, err := Repositories(ctx, l.openAs(t, runtime, ""), l.opts()...)
	if err != nil {
		t.Fatal(err)
	}
	purger, err := Repositories(ctx, l.openAs(t, runtime, ""), l.opts()...)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	const users = 12
	var ids []string
	for i := range users {
		u, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx,
			user.User{DisplayName: fmt.Sprintf("u%d", i), Status: user.StatusActive, CreatedAt: now, UpdatedAt: now},
			identifier.Identifier{Kind: identifier.KindEmail, NormalizedValue: fmt.Sprintf("race-%d@example.com", i), VerifiedAt: now,
				LoginEnabled: true, IsPrimary: true, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
		for j := range 5 {
			s, _ := session.NewSession(u.ID, time.Hour, now.Add(-3*time.Hour))
			s.RefreshTokenHash = fmt.Sprintf("race-%d-%d", i, j)
			if _, err := repos.Sessions.Create(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		seedOldEvents(t, repos, u.ID, 10, 500*retentionDay)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 3*users)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			n, err := purger.Maintenance.PurgeExpiredSessions(ctx, now.Add(-time.Hour), 3)
			res, railErr := purger.Maintenance.PurgeSecurityEventsBefore(ctx, now.Add(-400*retentionDay), 4)
			if err != nil || railErr != nil {
				errs <- errors.Join(err, railErr)
				return
			}
			if n == 0 && res.Count == 0 {
				return
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, id := range ids {
			in := user.EraseInput{ID: id, Now: time.Now(), Event: securityevent.SecurityEvent{
				UserID: id, Actor: securityevent.Principal{Type: "user", ID: "race-admin"}, EventType: securityevent.TypeUserErased,
				EventStatus: securityevent.StatusSuccess, CreatedAt: time.Now()}}
			if _, err := repos.UserErasure.Erase(ctx, in); err != nil {
				errs <- err
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent purge/erase failed (SQLSTATE %q): %v", sqlState(err), err)
	}
	if got := countRows(t, l.admin, l.events()+` WHERE created_at < now() - interval '400 days'`); got != 0 {
		t.Errorf("%d old rail rows survived", got)
	}
	if got := countRows(t, l.admin, l.schema.Table(usersTable)+` WHERE erased_at IS NULL`); got != 0 {
		t.Errorf("%d users were not erased", got)
	}
}

// TestLive_RailRetentionMigrationAssertions proves 0024's assertion block: run
// where current_schema() is not the rail's schema it fails before creating
// anything, and run by a role that is not a member of the rail's owner it
// fails naming the GRANT.
func TestLive_RailRetentionMigrationAssertions(t *testing.T) {
	ctx := context.Background()
	l := newAuditLive(t)
	l.migrate(t, migrationsThrough(t, retentionMigration))
	other := l.id + "_other"
	l.exec(t, `CREATE SCHEMA "`+other+`" AUTHORIZATION "`+l.migrator+`"`)
	t.Cleanup(func() { _, _ = l.admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+other+`" CASCADE`) })
	intruder := l.role(t, "intruder")
	l.exec(t, `GRANT USAGE, CREATE ON SCHEMA "`+l.id+`" TO "`+intruder+`"`)

	apply := func(role, searchPath string) error { return l.applyRailRetention(t, role, searchPath) }
	if err := apply(l.migrator, `"`+other+`", "`+l.id+`"`); err == nil || !strings.Contains(err.Error(), "security_events lives in schema "+l.id+", migration runs in "+other) {
		t.Fatalf("0024 under another current_schema() = %v, want the schema assertion", err)
	}
	if err := apply(intruder, `"`+l.id+`"`); err == nil || !strings.Contains(err.Error(), "the migrator must be a member of the owner of security_events; fix: GRANT") {
		t.Fatalf("0024 by a non-member = %v, want the owner assertion", err)
	}
	var created bool
	if err := l.admin.QueryRow(ctx, `SELECT pg_catalog.to_regprocedure($1) IS NOT NULL`, l.purgeFn()).Scan(&created); err != nil || created {
		t.Fatalf("a refused 0024 created the function: %v, %v", created, err)
	}
	if err := apply(l.migrator, `"`+l.id+`"`); err != nil {
		t.Fatalf("0024 in the rail's schema by its owner: %v", err)
	}
}

// TestLive_RetentionIndexesServePurges seeds every purged table with mostly
// live rows, analyzes, and requires each bounded purge statement to scan the
// 0023 expires_at index.
func TestLive_RetentionIndexesServePurges(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — the retention index plans NOT verified")
	}
	ctx := context.Background()
	schema := testSchema(t)
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: sdk.Secret(dsn)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, migrateOpts(schema)...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db, schema)
	t.Cleanup(func() { truncate(t, db, schema) })

	const expiry = `CASE WHEN g % 100 = 0 THEN now() - interval '2 days' ELSE now() + g * interval '1 minute' END`
	seeds := map[string]string{
		sessionsTable:       `(id, user_id, refresh_token_hash, created_at, expires_at) SELECT 's' || g, 'u' || g, 'h' || g, now(), ` + expiry,
		authGrantsTable:     `(session_id, user_id, purpose, context_digest, authenticated_at, expires_at, created_at) SELECT 's' || g, 'u' || g, 'p', 'c' || g, now(), ` + expiry + `, now()`,
		contactChangesTable: `(user_id, kind, new_value, expires_at, created_at) SELECT 'u' || g, 'email', 'n' || g || '@example.com', ` + expiry + `, now()`,
		oauthStatesTable:    `(token, provider, purpose, expires_at) SELECT 't' || g, 'p', 'pu', ` + expiry,
		challengesTable:     `(user_id, subject_key, purpose, secret_digest, expires_at, created_at) SELECT 'u' || g, 'k' || g, 'p', 'd' || g, ` + expiry + `, now()`,
		oauthCodesTable:     `(code_hash, user_id, auth_revision, delegation, redirect_uri, code_challenge, created_at, expires_at) SELECT 'c' || g, 'u', 0, '{}', 'https://x', 'cc', now(), ` + expiry,
		oauthClientsTable:   `(id, name, redirect_uris, expires_at) SELECT 'https://c' || g, 'n', '[]', ` + expiry,
		oauthHistoryTable:   `(refresh_token_hash, session_id, expires_at) SELECT 'r' || g, 's' || g, ` + expiry,
	}
	keys := map[string]string{sessionsTable: "id", authGrantsTable: "id", contactChangesTable: "id", oauthStatesTable: "token",
		challengesTable: "id", oauthCodesTable: "code_hash", oauthClientsTable: "id", oauthHistoryTable: "refresh_token_hash"}
	store := NewMaintenanceStore(db, storeOpts(schema)...)
	for _, idx := range retentionIndexes {
		t.Run(idx.name, func(t *testing.T) {
			table := schema.Table(idx.table)
			if _, err := db.Exec(ctx, `INSERT INTO `+table+` `+seeds[idx.table]+` FROM generate_series(1, 20000) AS g`); err != nil {
				t.Fatalf("seed %s: %v", idx.table, err)
			}
			if _, err := db.Exec(ctx, `ANALYZE `+table); err != nil {
				t.Fatal(err)
			}
			args := jackpgx.NamedArgs{"before": time.Now().UTC().Add(-time.Hour), "limit": 1000}
			var raw []byte
			if err := db.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+store.purgeExpiredSQL(idx.table, keys[idx.table]), args).Scan(&raw); err != nil {
				t.Fatalf("explain: %v", err)
			}
			var plans []explainPlan
			if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
				t.Fatalf("decode plan: %v", err)
			}
			if !usesIndex(plans[0].Plan, idx.name) {
				t.Fatalf("the %s purge is not served by %s:\n%s", idx.table, idx.name, raw)
			}
		})
	}
}
