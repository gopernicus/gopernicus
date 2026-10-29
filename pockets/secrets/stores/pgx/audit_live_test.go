package pgx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// fixtureRolePassword is every fixture login role's password.
const fixtureRolePassword = "secrets_pgx_fixture_pw"

// legacyAuditInsert is an insert shaped like a binary built before 0002: it
// names only 0001's columns.
const legacyAuditInsert = ` (id, occurred_at, action, owner_type, owner_id, name, version, key_id, actor_type, actor_id, system, reason)
	VALUES ($1, now(), 'secret_read', 'tenant', 'globex', 'a', 1, 'k1', '', '', 'old-binary', '')`

// runID makes cluster-wide fixture names unique to this run.
func runID() string { return fmt.Sprintf("%d", time.Now().UnixNano()%10_000_000_000) }

func mustExec(t *testing.T, db *pgxdb.DB, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// fixtureRole creates a LOGIN role unique to this run with USAGE on schema,
// dropped with every privilege granted to it at cleanup.
func fixtureRole(t *testing.T, admin *pgxdb.DB, schema pgxdb.Schema, suffix string) string {
	t.Helper()
	name := "secrets_pgx_" + runID() + "_" + suffix
	mustExec(t, admin, `CREATE ROLE "`+name+`" LOGIN PASSWORD '`+fixtureRolePassword+`'`)
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := admin.Exec(ctx, `DROP OWNED BY "`+name+`" CASCADE`); err != nil {
			t.Logf("drop owned by %s: %v", name, err)
		}
		if _, err := admin.Exec(ctx, `DROP ROLE IF EXISTS "`+name+`"`); err != nil {
			t.Logf("drop role %s: %v", name, err)
		}
	})
	namespace := schema.String()
	if namespace == "" {
		namespace = "public"
	}
	mustExec(t, admin, `GRANT USAGE ON SCHEMA "`+namespace+`" TO "`+name+`"`)
	return name
}

// openAs connects as role under mode; a non-zero mode acknowledges the
// plaintext test DSN.
func openAs(t *testing.T, dsn, role string, mode environment.Mode) *pgxdb.DB {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse POSTGRES_TEST_DSN: %v", err)
	}
	u.User = url.UserPassword(role, fixtureRolePassword)
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{
		DSN:                     sdk.Secret(u.String()),
		MaxConns:                2,
		RuntimeMode:             mode,
		Logger:                  slog.New(slog.DiscardHandler),
		TLSDisabledAcknowledged: mode != "",
	})
	if err != nil {
		t.Fatalf("open as %s: %v", role, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func appendRecord(t *testing.T, repo vault.Repository, name string) vault.AuditRecord {
	t.Helper()
	rec, err := vault.NewAuditRecord(time.Now(), vault.ActionSecretRead, vault.Owner{Type: "tenant", ID: "globex"}, name, 1, "k1", vault.Source{System: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendAudit(context.Background(), []vault.AuditRecord{rec}); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestLive_AuditRail runs migrations 0002 and 0003 against a real Postgres:
// the defaults an old binary's insert relies on, the evidence columns, the
// trigger pair as a non-owner and as the owner, and the posture probe Open
// runs in production for a role without USAGE on the sequence.
func TestLive_AuditRail(t *testing.T) {
	dsn := requireDSN(t)
	admin, opts := openAndMigrate(t, dsn)
	schema := testSchema(t)
	audit, sequence := schema.Table("secrets_audit"), schema.Table("secrets_audit_seq")
	repo := NewRepository(admin, opts...)
	ctx := context.Background()

	t.Run("old_binary_insert_takes_defaults", func(t *testing.T) {
		role := fixtureRole(t, admin, schema, "old")
		mustExec(t, admin, `GRANT SELECT, INSERT ON `+audit+` TO "`+role+`"`)
		old := openAs(t, dsn, role, "")
		if _, err := old.Exec(ctx, `INSERT INTO `+audit+legacyAuditInsert, "old-1"); err != nil {
			t.Fatalf("an insert naming only 0001's columns, as a role holding no grant on the sequence: %v", err)
		}
		got, _, err := repo.ListAudit(ctx, vault.AuditFilter{System: "old-binary"}, "", 10)
		if err != nil || len(got) != 1 || got[0].Outcome != vault.OutcomeOK || got[0].RequestID != "" || got[0].TraceID != "" {
			t.Fatalf("old-binary row: %+v %v", got, err)
		}
		var seq, recorded bool
		if err := admin.QueryRow(ctx, `SELECT seq IS NOT NULL, recorded_at IS NOT NULL FROM `+audit+` WHERE id = $1`, "old-1").Scan(&seq, &recorded); err != nil || !seq || !recorded {
			t.Fatalf("old-binary row evidence: seq %v recorded_at %v %v", seq, recorded, err)
		}
	})

	t.Run("seq_and_recorded_at_increase", func(t *testing.T) {
		var ids []string
		for _, name := range []string{"s1", "s2", "s3"} {
			ids = append(ids, appendRecord(t, repo, name).ID)
		}
		rows, err := admin.Query(ctx, `SELECT id, seq, recorded_at FROM `+audit+` WHERE id = ANY($1) ORDER BY seq`, ids)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var (
			n        int
			lastSeq  int64
			lastTime time.Time
		)
		for rows.Next() {
			var (
				id       string
				seq      *int64
				recorded *time.Time
			)
			if err := rows.Scan(&id, &seq, &recorded); err != nil {
				t.Fatal(err)
			}
			if seq == nil || recorded == nil || id != ids[n] || *seq <= lastSeq || recorded.Before(lastTime) {
				t.Fatalf("row %d: id %s seq %v recorded_at %v after seq %d at %v", n, id, seq, recorded, lastSeq, lastTime)
			}
			n, lastSeq, lastTime = n+1, *seq, *recorded
		}
		if err := rows.Err(); err != nil || n != 3 {
			t.Fatalf("read %d rows: %v", n, err)
		}
	})

	t.Run("triggers", func(t *testing.T) {
		role := fixtureRole(t, admin, schema, "writer")
		mustExec(t, admin, `GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON `+audit+` TO "`+role+`"`)
		rec := appendRecord(t, repo, "trig")
		writer := openAs(t, dsn, role, "")
		for _, stmt := range []string{
			`UPDATE ` + audit + ` SET reason = 'changed' WHERE id = '` + rec.ID + `'`,
			`DELETE FROM ` + audit + ` WHERE id = '` + rec.ID + `'`,
			`TRUNCATE ` + audit,
		} {
			_, err := writer.Exec(ctx, stmt)
			if !errors.Is(err, sdk.ErrForbidden) || !strings.Contains(err.Error(), "secrets_audit is append-only") {
				t.Fatalf("non-owner %s: want the trigger's refusal as sdk.ErrForbidden, got %v", strings.Fields(stmt)[0], err)
			}
		}
		var reason string
		if err := admin.QueryRow(ctx, `SELECT reason FROM `+audit+` WHERE id = $1`, rec.ID).Scan(&reason); err != nil || reason != "" {
			t.Fatalf("after refused writes: reason %q %v", reason, err)
		}

		// The owner (the migrator, here the admin connection) is exempt: its
		// UPDATE writes the new row, not the old one back, and DELETE passes.
		if n, err := pgxdb.ExecAffecting(ctx, admin, `UPDATE `+audit+` SET reason = 'owner' WHERE id = $1`, rec.ID); err != nil || n != 1 {
			t.Fatalf("owner update: %d %v", n, err)
		}
		if err := admin.QueryRow(ctx, `SELECT reason FROM `+audit+` WHERE id = $1`, rec.ID).Scan(&reason); err != nil || reason != "owner" {
			t.Fatalf("owner update did not persist: reason %q %v", reason, err)
		}
		if n, err := pgxdb.ExecAffecting(ctx, admin, `DELETE FROM `+audit+` WHERE id = $1`, rec.ID); err != nil || n != 1 {
			t.Fatalf("owner delete: %d %v", n, err)
		}
	})

	t.Run("open_production_sequence_trap", func(t *testing.T) {
		mustExec(t, admin, `REVOKE USAGE ON SEQUENCE `+sequence+` FROM PUBLIC`)
		t.Cleanup(func() { mustExec(t, admin, `GRANT USAGE ON SEQUENCE `+sequence+` TO PUBLIC`) })
		role := fixtureRole(t, admin, schema, "runtime")
		mustExec(t, admin,
			`GRANT SELECT, INSERT, UPDATE, DELETE ON `+schema.Table("secrets_owner_keys")+`, `+schema.Table("secrets_values")+` TO "`+role+`"`,
			`GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE ON `+audit+` TO "`+role+`"`,
		)
		runtime := openAs(t, dsn, role, environment.ModeProduction)

		// Table grants alone: the role could not append, and the UPDATE,
		// DELETE and TRUNCATE it holds are not a finding because the verified
		// trigger pair refuses them.
		_, err := Open(ctx, runtime, opts...)
		if !errors.Is(err, pgxdb.ErrAppendOnlyTableUnwritable) || errors.Is(err, pgxdb.ErrAppendOnlyTableWritable) {
			t.Fatalf("table grants only: want only ErrAppendOnlyTableUnwritable, got %v", err)
		}
		if !strings.Contains(err.Error(), "sequence_usage=false") {
			t.Fatalf("refusal does not name the sequence: %v", err)
		}

		mustExec(t, admin, `GRANT USAGE ON SEQUENCE `+sequence+` TO "`+role+`"`)
		store, err := Open(ctx, runtime, opts...)
		if err != nil {
			t.Fatalf("with USAGE on the sequence: %v", err)
		}
		owner := vault.Owner{Type: "tenant", ID: "runtime"}
		created, err := vault.NewAuditRecord(time.Now(), vault.ActionOwnerKeyCreated, owner, "", 0, "k1", vault.Source{System: "live"})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := store.CreateOwnerKey(ctx, vault.OwnerKey{Owner: owner, KeyID: "k1", WrappedKey: "w", WrapperKeyID: "m1", CreatedAt: now, RotatedAt: now}, []vault.AuditRecord{created}); err != nil {
			t.Fatalf("write as the runtime role: %v", err)
		}
	})
}

// TestLive_OpenRequiresIntegrityMigration builds a database that stopped at
// 0001 in its own schema: Open refuses it naming 0002, and once 0002 and
// 0003 are applied the row written before them keeps NULL evidence columns.
func TestLive_OpenRequiresIntegrityMigration(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: sdk.Secret(dsn)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema, err := pgxdb.NewSchema("secrets_pre0002_" + runID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema.String()+`" CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	first, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/0001_secrets.sql")
	if err != nil {
		t.Fatal(err)
	}
	only0001 := fstest.MapFS{MigrationsDir + "/0001_secrets.sql": {Data: first}}
	if err := pgxdb.RunMigrations(ctx, db, only0001, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate 0001: %v", err)
	}
	audit := schema.Table("secrets_audit")
	mustExec(t, db, `INSERT INTO `+audit+` (id, occurred_at, action, owner_type, owner_id) VALUES ('pre-0002', now(), 'secret_put', 'tenant', 'globex')`)

	_, err = Open(ctx, db, WithSchema(schema))
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "0002_secrets_audit_integrity.sql") {
		t.Fatalf("Open on a 0001-only database: want sdk.ErrNotFound naming 0002, got %v", err)
	}

	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate 0002, 0003: %v", err)
	}
	repo, err := Open(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Open after 0002: %v", err)
	}
	var (
		seqNull, recordedNull bool
		outcome               string
	)
	if err := db.QueryRow(ctx, `SELECT seq IS NULL, recorded_at IS NULL, outcome FROM `+audit+` WHERE id = 'pre-0002'`).Scan(&seqNull, &recordedNull, &outcome); err != nil {
		t.Fatal(err)
	}
	if !seqNull || !recordedNull || outcome != string(vault.OutcomeOK) {
		t.Fatalf("pre-0002 row: seq null %v, recorded_at null %v, outcome %q", seqNull, recordedNull, outcome)
	}
	rec := appendRecord(t, repo, "post")
	if err := db.QueryRow(ctx, `SELECT seq IS NULL, recorded_at IS NULL FROM `+audit+` WHERE id = $1`, rec.ID).Scan(&seqNull, &recordedNull); err != nil || seqNull || recordedNull {
		t.Fatalf("post-0002 row: seq null %v, recorded_at null %v %v", seqNull, recordedNull, err)
	}
}
