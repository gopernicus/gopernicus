package pgxdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// aoRolePassword is every fixture login role's password.
const aoRolePassword = "pgxdb_append_only_pw"

// appendOnlyLive is one run of the append-only live matrix: the throwaway
// database as its superuser, a run-unique name prefix (roles are cluster-wide)
// and the NOLOGIN migrator role that owns every fixture table and function.
type appendOnlyLive struct {
	dsn      string
	admin    *DB
	id       string
	migrator string
}

// aoTable is one fixture table: its bare name, the schema it lives in, and the
// spec a store would pass for it.
type aoTable struct {
	name   string
	schema string
	spec   AppendOnlyTable
}

// qualified is the table's schema-qualified name.
func (tb aoTable) qualified() string { return `"` + tb.schema + `"."` + tb.name + `"` }

// function is the table's trigger function, schema-qualified.
func (tb aoTable) function() string { return `"` + tb.schema + `"."` + tb.name + `_refuse_write"` }

// aoTableOption shapes a fixture table.
type aoTableOption struct {
	schema      string // "" is public
	noSeq       bool   // no seq column (the iam_operations shape)
	noTriggers  bool   // no trigger pair
	allowDelete bool
	qualify     bool // pass the schema-qualified name in the spec
}

func newAppendOnlyLive(t *testing.T) *appendOnlyLive {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — append-only posture probe NOT verified")
	}
	l := &appendOnlyLive{dsn: dsn, admin: openLive(t, dsn), id: fmt.Sprintf("pgao%d", time.Now().UnixNano()%10_000_000_000)}
	l.migrator = l.id + "_migrator"
	l.exec(t, `CREATE ROLE "`+l.migrator+`" NOLOGIN`)
	t.Cleanup(func() { l.dropRole(t, l.migrator) })
	return l
}

func (l *appendOnlyLive) exec(t *testing.T, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := l.admin.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func (l *appendOnlyLive) dropRole(t *testing.T, role string) {
	ctx := context.Background()
	if _, err := l.admin.Exec(ctx, `DROP OWNED BY "`+role+`" CASCADE`); err != nil {
		t.Logf("drop owned by %s: %v", role, err)
	}
	if _, err := l.admin.Exec(ctx, `DROP ROLE IF EXISTS "`+role+`"`); err != nil {
		t.Logf("drop role %s: %v", role, err)
	}
}

// role creates a LOGIN fixture role named <id>_<suffix> with attrs and USAGE on
// public, dropped (with everything it owns or was granted) at cleanup.
func (l *appendOnlyLive) role(t *testing.T, suffix, attrs string) string {
	t.Helper()
	name := l.id + "_" + suffix
	l.exec(t, `CREATE ROLE "`+name+`" LOGIN PASSWORD '`+aoRolePassword+`' `+attrs)
	t.Cleanup(func() { l.dropRole(t, name) })
	l.exec(t, `GRANT USAGE ON SCHEMA public TO "`+name+`"`)
	return name
}

// canonicalBody is the plan's canonical trigger function body for table.
func canonicalBody(table string) string {
	return "\nBEGIN\n" +
		"  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN\n" +
		"    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;   -- RETURN OLD here would write the old row back: a silent no-op\n" +
		"    RETURN OLD;                                    -- DELETE; NULL for the statement-level TRUNCATE trigger\n" +
		"  END IF;\n" +
		"  RAISE EXCEPTION '" + table + " is append-only' USING ERRCODE = '42501';\n" +
		"END\n"
}

// table creates <id>_<suffix> the way the stores' migrations do — a nullable
// seq column defaulting to <table>_seq, OWNED BY it — plus the canonical
// function and the verified trigger pair, then hands table, sequence and
// function to the migrator role. It is dropped at cleanup.
func (l *appendOnlyLive) table(t *testing.T, suffix string, opt aoTableOption) aoTable {
	t.Helper()
	tb := aoTable{name: l.id + "_" + suffix, schema: opt.schema}
	if tb.schema == "" {
		tb.schema = "public"
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := l.admin.Exec(ctx, `DROP TABLE IF EXISTS `+tb.qualified()+` CASCADE`); err != nil {
			t.Logf("drop table %s: %v", tb.name, err)
		}
		if _, err := l.admin.Exec(ctx, `DROP FUNCTION IF EXISTS `+tb.function()+`() CASCADE`); err != nil {
			t.Logf("drop function %s: %v", tb.name, err)
		}
	})
	q := tb.qualified()
	seqName := `"` + tb.schema + `"."` + tb.name + `_seq"`
	if opt.noSeq {
		l.exec(t, `CREATE TABLE `+q+` (id TEXT PRIMARY KEY, outcome TEXT NOT NULL DEFAULT 'applied')`)
	} else {
		l.exec(t,
			`CREATE SEQUENCE `+seqName,
			`CREATE TABLE `+q+` (id TEXT PRIMARY KEY, seq BIGINT NULL)`,
			`ALTER TABLE `+q+` ALTER COLUMN seq SET DEFAULT pg_catalog.nextval('`+seqName+`')`,
			`ALTER SEQUENCE `+seqName+` OWNED BY `+q+`.seq`,
		)
	}
	l.exec(t,
		`CREATE OR REPLACE FUNCTION `+tb.function()+`() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $ao$`+canonicalBody(tb.name)+`$ao$`,
		`ALTER FUNCTION `+tb.function()+`() OWNER TO "`+l.migrator+`"`,
		`ALTER TABLE `+q+` OWNER TO "`+l.migrator+`"`,
	)
	if !opt.noTriggers {
		l.createTriggers(t, tb)
	}
	tb.spec = AppendOnlyTable{
		Name:        tb.name,
		AllowDelete: opt.allowDelete,
		Canonical:   []TriggerBody{{Function: tb.name + "_refuse_write", Body: canonicalBody(tb.name)}},
	}
	if opt.qualify {
		tb.spec.Name = tb.schema + "." + tb.name
	}
	return tb
}

// createTriggers installs the canonical pair, ENABLE ALWAYS.
func (l *appendOnlyLive) createTriggers(t *testing.T, tb aoTable) {
	t.Helper()
	q := tb.qualified()
	l.exec(t,
		`CREATE OR REPLACE TRIGGER "`+tb.name+`_immutable" BEFORE UPDATE OR DELETE ON `+q+` FOR EACH ROW EXECUTE FUNCTION `+tb.function()+`()`,
		`CREATE OR REPLACE TRIGGER "`+tb.name+`_no_truncate" BEFORE TRUNCATE ON `+q+` FOR EACH STATEMENT EXECUTE FUNCTION `+tb.function()+`()`,
		`ALTER TABLE `+q+` ENABLE ALWAYS TRIGGER "`+tb.name+`_immutable"`,
		`ALTER TABLE `+q+` ENABLE ALWAYS TRIGGER "`+tb.name+`_no_truncate"`,
	)
}

// grant gives role privileges on the table, and USAGE on its sequence when
// sequence is set.
func (l *appendOnlyLive) grant(t *testing.T, tb aoTable, role, privileges string, sequence bool) {
	t.Helper()
	l.exec(t, `GRANT `+privileges+` ON `+tb.qualified()+` TO "`+role+`"`)
	if sequence {
		l.exec(t, `GRANT USAGE ON SEQUENCE "`+tb.schema+`"."`+tb.name+`_seq" TO "`+role+`"`)
	}
}

// openAs opens the database as role under mode, with a captured logger reset
// after Open so the buffer holds only what the probe logs. A non-zero mode
// acknowledges the plaintext test DSN.
func (l *appendOnlyLive) openAs(t *testing.T, role string, mode environment.Mode, acknowledged bool) (*DB, *bytes.Buffer) {
	t.Helper()
	u, err := url.Parse(l.dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	u.User = url.UserPassword(role, aoRolePassword)
	logger, buf := captureLogger()
	db, err := Open(context.Background(), Config{
		DSN:                            u.String(),
		MaxConns:                       2,
		RuntimeMode:                    mode,
		Logger:                         logger,
		TLSDisabledAcknowledged:        mode != "",
		AppendOnlyWritableAcknowledged: acknowledged,
	})
	if err != nil {
		t.Fatalf("open as %s: %v", role, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.RuntimeMode() != mode {
		t.Fatalf("RuntimeMode() = %q, want %q", db.RuntimeMode(), mode)
	}
	buf.Reset()
	return db, buf
}

// inspect preflights role from the admin connection and returns its one table.
func (l *appendOnlyLive) inspect(t *testing.T, role string, tb aoTable) TablePrivilege {
	t.Helper()
	report, err := InspectTablePrivileges(context.Background(), l.admin, role, tb.spec)
	if err != nil {
		t.Fatalf("InspectTablePrivileges(%s): %v", role, err)
	}
	if report.Role != role || len(report.Tables) != 1 {
		t.Fatalf("report = %+v, want role %s and one table", report, role)
	}
	return report.Tables[0]
}

// refused probes tb as role in production and requires a refusal wrapping
// want whose text names the table and every reason.
func (l *appendOnlyLive) refused(t *testing.T, role string, tb aoTable, want error, reasons ...string) error {
	t.Helper()
	db, _ := l.openAs(t, role, environment.ModeProduction, false)
	err := db.ProbeAppendOnly(context.Background(), tb.spec)
	if !errors.Is(err, want) {
		t.Fatalf("ProbeAppendOnly = %v, want %v", err, want)
	}
	msg := err.Error()
	for _, r := range append([]string{tb.spec.Name, appendOnlyFixLead}, reasons...) {
		if !strings.Contains(msg, r) {
			t.Errorf("refusal lacks %q:\n%s", r, msg)
		}
	}
	return err
}

// admitted probes tb as role in production and requires nil with no log line.
func (l *appendOnlyLive) admitted(t *testing.T, role string, tb aoTable) {
	t.Helper()
	db, buf := l.openAs(t, role, environment.ModeProduction, false)
	if err := db.ProbeAppendOnly(context.Background(), tb.spec); err != nil {
		t.Fatalf("ProbeAppendOnly = %v, want nil", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("an admitted probe logged:\n%s", buf)
	}
}

// TestLive_AppendOnlyProbe is the append-only posture matrix against a real
// Postgres: fixture roles (unique per run) against fixture tables carrying the
// canonical trigger pair, each case decided by InspectTablePrivileges from the
// owner's connection and by ProbeAppendOnly as the role itself.
func TestLive_AppendOnlyProbe(t *testing.T) {
	l := newAppendOnlyLive(t)
	ctx := context.Background()

	t.Run("table_grants_only_cannot_append", func(t *testing.T) {
		tb := l.table(t, "tgrants", aoTableOption{})
		role := l.role(t, "tgrants", "")
		l.grant(t, tb, role, "SELECT, INSERT, UPDATE, DELETE", false)

		p := l.inspect(t, role, tb)
		if !p.Insert || p.SequenceUsage || !p.ImmutableTrigger || p.Writable() || !p.CannotAppend() {
			t.Fatalf("evidence = %+v, want insert without sequence usage behind verified triggers", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableUnwritable, "sequence_usage=false", "sequence=public."+tb.name+"_seq")

		// The acknowledgement never admits it, and the trap is real: the insert
		// fails on the sequence.
		db, _ := l.openAs(t, role, environment.ModeProduction, true)
		if err := db.ProbeAppendOnly(ctx, tb.spec); !errors.Is(err, ErrAppendOnlyTableUnwritable) || errors.Is(err, ErrAppendOnlyTableWritable) {
			t.Fatalf("acknowledged ProbeAppendOnly = %v, want only ErrAppendOnlyTableUnwritable", err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO `+tb.qualified()+` (id) VALUES ('a')`); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("insert without sequence USAGE = %v, want sdk.ErrForbidden", err)
		}
	})

	t.Run("sequence_usage_and_revokes_protected_by_privilege", func(t *testing.T) {
		tb := l.table(t, "privilege", aoTableOption{})
		role := l.role(t, "privilege", "")
		l.grant(t, tb, role, "SELECT, INSERT", true)

		p := l.inspect(t, role, tb)
		if p.Writable() || p.CannotAppend() || p.ProtectedBy(false) != protectedByPrivilege {
			t.Fatalf("evidence = %+v (protected_by=%s), want clean, protected_by=privilege", p, p.ProtectedBy(false))
		}
		l.admitted(t, role, tb)

		db, _ := l.openAs(t, role, "", false)
		if _, err := db.Exec(ctx, `INSERT INTO `+tb.qualified()+` (id) VALUES ('a')`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := db.Exec(ctx, `UPDATE `+tb.qualified()+` SET id = 'b'`); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("update without the grant = %v, want sdk.ErrForbidden", err)
		}
	})

	t.Run("grants_kept_verified_triggers_protected_by_trigger", func(t *testing.T) {
		tb := l.table(t, "trigger", aoTableOption{})
		role := l.role(t, "trigger", "")
		l.grant(t, tb, role, "SELECT, INSERT, UPDATE, DELETE", true)

		p := l.inspect(t, role, tb)
		if !p.Update || !p.Delete || !p.ImmutableTrigger || p.TriggerState != triggerVerified ||
			p.Writable() || p.CannotAppend() || p.ProtectedBy(false) != protectedByTrigger {
			t.Fatalf("evidence = %+v (protected_by=%s), want clean, protected_by=trigger", p, p.ProtectedBy(false))
		}
		l.admitted(t, role, tb)

		db, _ := l.openAs(t, role, "", false)
		if _, err := db.Exec(ctx, `INSERT INTO `+tb.qualified()+` (id) VALUES ('a')`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		for _, stmt := range []string{`UPDATE ` + tb.qualified() + ` SET id = 'b'`, `DELETE FROM ` + tb.qualified()} {
			_, err := db.Exec(ctx, stmt)
			if !errors.Is(err, sdk.ErrForbidden) || !strings.Contains(err.Error(), tb.name+" is append-only") {
				t.Fatalf("%s = %v, want the trigger's refusal as sdk.ErrForbidden", stmt, err)
			}
		}
	})

	t.Run("member_of_owning_role", func(t *testing.T) {
		tb := l.table(t, "member", aoTableOption{})
		role := l.role(t, "member", "")
		l.exec(t, `GRANT "`+l.migrator+`" TO "`+role+`"`)

		if p := l.inspect(t, role, tb); !p.Owner || p.OwnerKind != ownerTable || !p.Writable() {
			t.Fatalf("evidence = %+v, want owner=table", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableWritable, "owner=table")
	})

	t.Run("noinherit_member", func(t *testing.T) {
		tb := l.table(t, "noinherit", aoTableOption{})
		role := l.role(t, "noinherit", "NOINHERIT")
		l.exec(t, `GRANT "`+l.migrator+`" TO "`+role+`"`)
		l.grant(t, tb, role, "SELECT, INSERT", true)

		if p := l.inspect(t, role, tb); !p.Owner || p.OwnerKind != ownerTable || p.Update {
			t.Fatalf("evidence = %+v, want owner=table without inherited UPDATE", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableWritable, "owner=table")
	})

	t.Run("schema_owner", func(t *testing.T) {
		role := l.role(t, "nspowner", "")
		schema := l.id + "_nsp"
		l.exec(t, `CREATE SCHEMA "`+schema+`" AUTHORIZATION "`+role+`"`)
		t.Cleanup(func() { _, _ = l.admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
		tb := l.table(t, "nspowner", aoTableOption{schema: schema, qualify: true})
		l.grant(t, tb, role, "SELECT, INSERT", true)

		if p := l.inspect(t, role, tb); !p.Owner || p.OwnerKind != ownerSchema {
			t.Fatalf("evidence = %+v, want owner=schema", p)
		}
		err := l.refused(t, role, tb, ErrAppendOnlyTableWritable, "owner=schema")
		if !strings.Contains(err.Error(), "ALTER SCHEMA "+schema+" OWNER TO <migrator>") {
			t.Fatalf("refusal does not name the table's schema in the fix: %v", err)
		}
	})

	t.Run("trigger_function_owner", func(t *testing.T) {
		tb := l.table(t, "fnowner", aoTableOption{})
		role := l.role(t, "fnowner", "")
		l.grant(t, tb, role, "SELECT, INSERT", true)
		l.exec(t, `ALTER FUNCTION `+tb.function()+`() OWNER TO "`+role+`"`)

		if p := l.inspect(t, role, tb); !p.Owner || p.OwnerKind != ownerFunction || !p.ImmutableTrigger {
			t.Fatalf("evidence = %+v, want owner=function behind verified triggers", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableWritable, "owner=function")
	})

	t.Run("trigger_grant", func(t *testing.T) {
		tb := l.table(t, "tgrant", aoTableOption{noTriggers: true})
		role := l.role(t, "tgrant", "")
		l.grant(t, tb, role, "SELECT, INSERT, TRIGGER", true)

		if p := l.inspect(t, role, tb); !p.Trigger || p.Owner || !p.Writable() {
			t.Fatalf("evidence = %+v, want a writable TRIGGER grant", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableWritable, "privileges=trigger", "trigger=absent")
	})

	t.Run("column_level_update_grant", func(t *testing.T) {
		tb := l.table(t, "colupd", aoTableOption{noTriggers: true})
		role := l.role(t, "colupd", "")
		l.grant(t, tb, role, "SELECT, INSERT", true)
		l.exec(t, `GRANT UPDATE (id) ON `+tb.qualified()+` TO "`+role+`"`)

		if p := l.inspect(t, role, tb); !p.Update || !p.Writable() {
			t.Fatalf("evidence = %+v, want a column grant counted as UPDATE", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableWritable, "privileges=update", "trigger=absent")
	})

	t.Run("superuser", func(t *testing.T) {
		tb := l.table(t, "super", aoTableOption{})
		role := l.role(t, "super", "SUPERUSER")

		if p := l.inspect(t, role, tb); !p.Superuser || !p.Writable() {
			t.Fatalf("evidence = %+v, want superuser", p)
		}
		l.refused(t, role, tb, ErrAppendOnlyTableWritable, "superuser")
	})

	// The trigger-state cases keep the role's UPDATE and DELETE grants, so the
	// trigger pair is all that protects the table: each departure is refused
	// and named.
	triggerCases := []struct {
		suffix string
		breaks func(t *testing.T, tb aoTable)
		reason string
	}{
		{"onlyrow", func(t *testing.T, tb aoTable) {
			l.exec(t, `DROP TRIGGER "`+tb.name+`_no_truncate" ON `+tb.qualified())
		}, "trigger=absent"},
		{"origin", func(t *testing.T, tb aoTable) {
			l.exec(t, `ALTER TABLE `+tb.qualified()+` ENABLE TRIGGER "`+tb.name+`_immutable"`)
		}, "tgenabled=O"},
		{"when", func(t *testing.T, tb aoTable) {
			l.exec(t,
				`CREATE OR REPLACE TRIGGER "`+tb.name+`_immutable" BEFORE UPDATE OR DELETE ON `+tb.qualified()+` FOR EACH ROW WHEN (OLD.id <> '') EXECUTE FUNCTION `+tb.function()+`()`,
				`ALTER TABLE `+tb.qualified()+` ENABLE ALWAYS TRIGGER "`+tb.name+`_immutable"`)
		}, "trigger=when_clause"},
		{"updateof", func(t *testing.T, tb aoTable) {
			l.exec(t,
				`CREATE OR REPLACE TRIGGER "`+tb.name+`_immutable" BEFORE UPDATE OF id OR DELETE ON `+tb.qualified()+` FOR EACH ROW EXECUTE FUNCTION `+tb.function()+`()`,
				`ALTER TABLE `+tb.qualified()+` ENABLE ALWAYS TRIGGER "`+tb.name+`_immutable"`)
		}, "trigger=column_list"},
		{"body", func(t *testing.T, tb aoTable) {
			l.exec(t, `CREATE OR REPLACE FUNCTION `+tb.function()+`() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $ao$`+strings.Replace(canonicalBody(tb.name), "RAISE EXCEPTION", "RAISE WARNING", 1)+`$ao$`)
		}, "trigger=body_differs"},
		{"secdef", func(t *testing.T, tb aoTable) {
			l.exec(t, `ALTER FUNCTION `+tb.function()+`() SECURITY DEFINER`)
		}, "trigger=security_definer"},
		{"proconfig", func(t *testing.T, tb aoTable) {
			l.exec(t, `ALTER FUNCTION `+tb.function()+`() RESET search_path`)
		}, "trigger=proconfig"},
	}
	t.Run("trigger_pair_present", func(t *testing.T) {
		tb := l.table(t, "pair", aoTableOption{})
		role := l.role(t, "pair", "")
		l.grant(t, tb, role, "SELECT, INSERT, UPDATE, DELETE", true)
		if p := l.inspect(t, role, tb); !p.ImmutableTrigger || p.TriggerState != triggerVerified {
			t.Fatalf("evidence = %+v, want the verified pair", p)
		}
		l.admitted(t, role, tb)
	})
	for _, tc := range triggerCases {
		t.Run("trigger_"+tc.suffix, func(t *testing.T) {
			tb := l.table(t, tc.suffix, aoTableOption{})
			role := l.role(t, tc.suffix, "")
			l.grant(t, tb, role, "SELECT, INSERT, UPDATE, DELETE", true)
			tc.breaks(t, tb)

			p := l.inspect(t, role, tb)
			if p.ImmutableTrigger || !p.Writable() {
				t.Fatalf("evidence = %+v, want unverified triggers", p)
			}
			err := l.refused(t, role, tb, ErrAppendOnlyTableWritable, "privileges=update,delete", tc.reason)
			if tc.reason == "tgenabled=O" && !strings.Contains(err.Error(), "ENABLE ALWAYS TRIGGER "+tb.name+"_immutable") {
				t.Fatalf("tgenabled refusal lacks the ENABLE ALWAYS fix: %v", err)
			}
		})
	}

	t.Run("named_role_preflight_matches_the_role", func(t *testing.T) {
		tb := l.table(t, "preflight", aoTableOption{})
		role := l.role(t, "preflight", "")
		l.grant(t, tb, role, "SELECT, INSERT, UPDATE", true)

		self, err := InspectTablePrivileges(ctx, l.admin, "", tb.spec)
		if err != nil {
			t.Fatal(err)
		}
		if !self.Tables[0].Superuser {
			t.Fatalf("admin self-inspection = %+v, want the superuser it connects as", self)
		}
		preflight, err := InspectTablePrivileges(ctx, l.admin, role, tb.spec)
		if err != nil {
			t.Fatal(err)
		}
		if p := preflight.Tables[0]; p.Superuser || p.Owner || !p.Update || p.Delete || !p.ImmutableTrigger {
			t.Fatalf("preflight = %+v, want the named role's own evidence", p)
		}
		db, _ := l.openAs(t, role, "", false)
		own, err := InspectTablePrivileges(ctx, db, "", tb.spec)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(own, preflight) {
			t.Fatalf("the role's own report differs from the preflight:\n own=%+v\npre=%+v", own, preflight)
		}
	})

	t.Run("no_seq_column", func(t *testing.T) {
		tb := l.table(t, "noseq", aoTableOption{noSeq: true, allowDelete: true})
		role := l.role(t, "noseq", "")
		l.grant(t, tb, role, "SELECT, INSERT, DELETE", false)

		p := l.inspect(t, role, tb)
		if !p.Exists || !p.SequenceUsage || !p.Delete || p.Writable() || p.CannotAppend() || p.ProtectedBy(false) != protectedByPrivilege {
			t.Fatalf("evidence = %+v, want a clean table without seq whose DELETE is allowed", p)
		}
		l.admitted(t, role, tb)
	})

	t.Run("missing_table_and_unknown_role", func(t *testing.T) {
		report, err := InspectTablePrivileges(ctx, l.admin, "", AppendOnlyTable{Name: l.id + "_absent"})
		if err != nil {
			t.Fatalf("missing table: %v", err)
		}
		if p := report.Tables[0]; p.Exists || !p.CannotAppend() {
			t.Fatalf("missing table = %+v, want Exists false", p)
		}
		if _, err := InspectTablePrivileges(ctx, l.admin, l.id+"_nobody"); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("unknown role = %v, want sdk.ErrNotFound", err)
		}
	})

	t.Run("errors_join_names_every_table", func(t *testing.T) {
		first := l.table(t, "joina", aoTableOption{})
		second := l.table(t, "joinb", aoTableOption{})
		role := l.role(t, "join", "")
		l.exec(t, `GRANT "`+l.migrator+`" TO "`+role+`"`)

		db, _ := l.openAs(t, role, environment.ModeProduction, false)
		err := db.ProbeAppendOnly(ctx, first.spec, second.spec)
		var joined interface{ Unwrap() []error }
		if !errors.Is(err, ErrAppendOnlyTableWritable) || !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
			t.Fatalf("ProbeAppendOnly = %v, want two joined refusals", err)
		}
		for _, name := range []string{first.name, second.name} {
			if !strings.Contains(err.Error(), name+": owner=table") {
				t.Errorf("refusal does not name %s:\n%v", name, err)
			}
		}
	})

	t.Run("acknowledged_owner_warns_and_boots", func(t *testing.T) {
		first := l.table(t, "acka", aoTableOption{})
		second := l.table(t, "ackb", aoTableOption{})
		role := l.role(t, "ack", "")
		l.exec(t, `GRANT "`+l.migrator+`" TO "`+role+`"`)

		db, buf := l.openAs(t, role, environment.ModeProduction, true)
		if err := db.ProbeAppendOnly(ctx, first.spec, second.spec); err != nil {
			t.Fatalf("acknowledged ProbeAppendOnly = %v, want nil", err)
		}
		out := buf.String()
		if n := strings.Count(out, "posture_waiver=AppendOnlyWritableAcknowledged"); n != 2 {
			t.Fatalf("logged %d waivers, want one per table:\n%s", n, out)
		}
		if n := strings.Count(out, "trigger_exempt=true"); n != 2 {
			t.Fatalf("logged %d trigger_exempt=true, want one per table:\n%s", n, out)
		}
		for _, name := range []string{first.name, second.name} {
			if !strings.Contains(out, "runtime role owns "+name+": immutability triggers do not apply") {
				t.Errorf("no waiver line for %s:\n%s", name, out)
			}
		}
	})

	t.Run("development_line", func(t *testing.T) {
		first := l.table(t, "deva", aoTableOption{})
		second := l.table(t, "devb", aoTableOption{})
		role := l.role(t, "dev", "")
		l.exec(t, `GRANT "`+l.migrator+`" TO "`+role+`"`)

		db, buf := l.openAs(t, role, environment.ModeDevelopment, false)
		if err := db.ProbeAppendOnly(ctx, first.spec, second.spec); err != nil {
			t.Fatalf("development ProbeAppendOnly = %v, want nil", err)
		}
		out := buf.String()
		if strings.Count(out, "\n") != 1 || !strings.Contains(out, "append_only=unenforced") ||
			!strings.Contains(out, first.name) || !strings.Contains(out, second.name) || !strings.Contains(out, "owner=table") {
			t.Fatalf("development logged, want one append_only=unenforced line naming both tables:\n%s", out)
		}
	})

	t.Run("zero_mode_silent", func(t *testing.T) {
		tb := l.table(t, "zero", aoTableOption{})
		role := l.role(t, "zero", "")
		l.exec(t, `GRANT "`+l.migrator+`" TO "`+role+`"`)

		db, buf := l.openAs(t, role, "", false)
		if err := db.ProbeAppendOnly(ctx, tb.spec); err != nil {
			t.Fatalf("zero mode ProbeAppendOnly = %v, want nil", err)
		}
		if buf.Len() != 0 {
			t.Fatalf("zero mode logged:\n%s", buf)
		}
	})
}
