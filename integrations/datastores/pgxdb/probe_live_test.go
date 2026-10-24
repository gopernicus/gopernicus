package pgxdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"slices"
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
		DSN:                            sdk.Secret(u.String()),
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

// definerArgs is the fixture definer function's argument list, every type
// pg_catalog-qualified as a DefinerFunction signature writes it.
const definerArgs = "(pg_catalog.timestamptz, pg_catalog.int4)"

// aoDefiner is one fixture definer function beside its table: the bare
// function name, the bare and the schema-qualified signature, and the table
// whose spec lists the qualified signature.
type aoDefiner struct {
	table     aoTable
	name      string
	bare      string
	signature string
}

// migrate runs statements as the migrator the way the runner applies a file
// under WithSchema: one transaction, SET LOCAL ROLE to the migrator and SET
// LOCAL search_path TO the schema.
func (l *appendOnlyLive) migrate(t *testing.T, schema string, statements ...string) {
	t.Helper()
	ctx := context.Background()
	err := l.admin.InTx(ctx, func(tx *Tx) error {
		lead := []string{`SET LOCAL ROLE "` + l.migrator + `"`, `SET LOCAL search_path TO "` + schema + `"`}
		for _, stmt := range append(lead, statements...) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("migrate as %s: %v", l.migrator, err)
	}
}

// pinStatement is migration 0024's DO block for d: it pins search_path to
// pg_catalog, the table's own schema (from its namespace, never
// current_schema()) and pg_temp. It doubles as the re-pin snippet.
func (d aoDefiner) pinStatement() string {
	return `DO $do$ BEGIN EXECUTE pg_catalog.format('ALTER FUNCTION ` + d.bare + ` SET search_path = pg_catalog, %I, pg_temp', ` +
		`(SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace ` +
		`WHERE c.oid = '` + d.table.name + `'::pg_catalog.regclass)); END $do$`
}

// definer creates, in a run-unique schema whose name needs quotes and the
// migrator owns, a fixture table with its canonical trigger pair, an unlisted
// LANGUAGE sql floor function, and a SECURITY DEFINER purge function the
// migrator creates through migration 0024's recipe: a static body, the DO
// block pinning search_path, REVOKE EXECUTE FROM PUBLIC. role gets the two-role
// recipe's grants: INSERT, the sequence, USAGE on the schema and EXECUTE.
func (l *appendOnlyLive) definer(t *testing.T, suffix, role string) aoDefiner {
	t.Helper()
	schema := "S3-Mixed-" + l.id + "-" + suffix
	l.exec(t, `CREATE SCHEMA "`+schema+`" AUTHORIZATION "`+l.migrator+`"`)
	t.Cleanup(func() {
		if _, err := l.admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	tb := l.table(t, suffix, aoTableOption{schema: schema})
	tb.spec.Name = tb.qualified()
	d := aoDefiner{table: tb, name: tb.name + "_purge_before"}
	d.bare = d.name + definerArgs
	d.signature = `"` + schema + `".` + d.bare
	l.migrate(t, schema,
		`CREATE FUNCTION `+d.name+`(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4) RETURNS pg_catalog.int8
LANGUAGE plpgsql SECURITY DEFINER AS $fn$
DECLARE
  n pg_catalog.int8;
BEGIN
  DELETE FROM `+tb.name+` WHERE id IN (SELECT id FROM `+tb.name+` LIMIT p_max_rows);
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$fn$`,
		`CREATE FUNCTION `+tb.name+`_retention_floor() RETURNS pg_catalog.interval LANGUAGE sql STABLE
AS $fn$ SELECT '365 days'::pg_catalog.interval $fn$`,
		d.pinStatement(),
		`REVOKE EXECUTE ON FUNCTION `+d.bare+` FROM PUBLIC`,
	)
	l.grant(t, tb, role, "SELECT, INSERT", true)
	l.exec(t,
		`GRANT USAGE ON SCHEMA "`+schema+`" TO "`+role+`"`,
		`GRANT EXECUTE ON FUNCTION `+d.signature+` TO "`+role+`"`,
	)
	d.table.spec.Functions = []DefinerFunction{{Signature: d.signature}}
	return d
}

// probeAs opens the database as role under mode and returns what
// ProbeAppendOnly over specs logged, with each line's time attribute dropped,
// and its error.
func (l *appendOnlyLive) probeAs(t *testing.T, role string, mode environment.Mode, acknowledged bool, specs ...AppendOnlyTable) (string, error) {
	t.Helper()
	db, buf := l.openAs(t, role, mode, acknowledged)
	err := db.ProbeAppendOnly(context.Background(), specs...)
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if _, rest, ok := strings.Cut(line, " "); ok && strings.HasPrefix(line, "time=") {
			line = rest
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), err
}

// pinnedInspect is ProbeAppendOnly's own inspection as role: its own
// connection, a read-only transaction, names resolved and then the
// search_path pinned to pg_catalog. It returns the report and the pinned
// transaction, rolled back at cleanup.
func (l *appendOnlyLive) pinnedInspect(t *testing.T, role string, specs ...AppendOnlyTable) (Report, *Tx) {
	t.Helper()
	db, _ := l.openAs(t, role, "", false)
	tx, err := db.BeginRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	report, err := inspectTablePrivileges(context.Background(), tx, "", true, specs)
	if err != nil {
		t.Fatalf("pinned inspect as %s: %v", role, err)
	}
	return report, tx
}

// escaped is s as slog's text handler writes it inside a quoted value.
func escaped(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }

// bits is f's evidence without the remedy text, for comparison.
func bits(f FunctionPrivilege) FunctionPrivilege {
	f.function, f.namespace, f.schema, f.tableOwner = "", "", "", ""
	return f
}

// cleanDefiner is the evidence of the 0024 recipe for a role granted EXECUTE.
var cleanDefiner = FunctionPrivilege{
	Exists: true, SecurityDefiner: true, SearchPathPinned: true, SameNamespace: true,
	OwnerIsTableOwner: true, Execute: true,
}

// TestLive_DefinerFunctionProbe is the definer-function matrix against a real
// Postgres: the migrator creates each function through migration 0024's
// recipe in a schema whose name needs quotes, and each departure is judged by
// InspectTablePrivileges and by ProbeAppendOnly as the runtime role — refused
// when unsafe, one WARN when inoperative, silent otherwise.
func TestLive_DefinerFunctionProbe(t *testing.T) {
	l := newAppendOnlyLive(t)
	ctx := context.Background()

	// refusedUnsafe requires a production refusal naming the function and every
	// reason, with or without the acknowledgement.
	refusedUnsafe := func(t *testing.T, role string, spec AppendOnlyTable, signature string, reasons ...string) error {
		t.Helper()
		var first error
		for _, acknowledged := range []bool{false, true} {
			_, err := l.probeAs(t, role, environment.ModeProduction, acknowledged, spec)
			if !errors.Is(err, ErrDefinerFunctionUnsafe) {
				t.Fatalf("acknowledged=%t ProbeAppendOnly = %v, want ErrDefinerFunctionUnsafe", acknowledged, err)
			}
			for _, want := range append([]string{spec.Name + ": function=" + signature}, reasons...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal lacks %q:\n%v", want, err)
				}
			}
			if first == nil {
				first = err
			}
		}
		return first
	}

	// warnedInoperative requires a production boot with exactly one WARN naming the
	// function, the reasons and the fix.
	warnedInoperative := func(t *testing.T, role string, spec AppendOnlyTable, signature string, wants ...string) {
		t.Helper()
		out, err := l.probeAs(t, role, environment.ModeProduction, false, spec)
		if err != nil {
			t.Fatalf("ProbeAppendOnly = %v, want a boot with a WARN", err)
		}
		if strings.Count(out, "\n") != 0 || !strings.Contains(out, "level=WARN") {
			t.Fatalf("logged, want exactly one WARN:\n%s", out)
		}
		for _, want := range append([]string{"definer_function=inoperative", `function="` + escaped(signature) + `"`}, wants...) {
			if !strings.Contains(out, want) {
				t.Errorf("WARN lacks %q:\n%s", want, out)
			}
		}
	}

	t.Run("recipe_is_clean", func(t *testing.T) {
		role := l.role(t, "dclean", "")
		d := l.definer(t, "dclean", role)

		p := l.inspect(t, role, d.table)
		f, ok := p.Functions[d.signature]
		if !ok || len(p.Functions) != 1 || bits(f) != cleanDefiner {
			t.Fatalf("functions = %+v, want the one clean definer", p.Functions)
		}
		if f.schema != `"`+d.table.schema+`"` || f.function != `"`+d.table.schema+`".`+d.name+"(timestamp with time zone, integer)" {
			t.Fatalf("remedy names = %q, %q, want the quoted schema", f.schema, f.function)
		}
		if p.UnsafeFunctions() != nil || p.InoperativeFunctions() != nil || p.Writable() || p.CannotAppend() {
			t.Fatalf("evidence = %+v, want no finding", p)
		}
		l.admitted(t, role, d.table)

		// The unlisted floor function keeps PUBLIC EXECUTE and is no definer:
		// listing only the definer keeps it out of the probe.
		var floorPublic, floorDefiner bool
		if err := l.admin.QueryRow(ctx, `SELECT pg_catalog.has_function_privilege('public', p.oid, 'EXECUTE'), p.prosecdef
FROM pg_catalog.pg_proc p WHERE p.oid = pg_catalog.to_regprocedure($1)`, `"`+d.table.schema+`".`+d.table.name+"_retention_floor()").Scan(&floorPublic, &floorDefiner); err != nil {
			t.Fatal(err)
		}
		if !floorPublic || floorDefiner {
			t.Fatalf("floor function public=%t definer=%t, want a plain PUBLIC-executable function", floorPublic, floorDefiner)
		}

		// The definer acts as the table's owner: the role, holding no DELETE,
		// purges through it, while its own DELETE is refused.
		db, _ := l.openAs(t, role, "", false)
		if _, err := db.Exec(ctx, `INSERT INTO `+d.table.qualified()+` (id) VALUES ('a'), ('b')`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := db.Exec(ctx, `DELETE FROM `+d.table.qualified()); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("direct delete = %v, want sdk.ErrForbidden", err)
		}
		var purged int64
		if err := db.QueryRow(ctx, `SELECT `+d.signature[:len(d.signature)-len(definerArgs)]+`($1::pg_catalog.timestamptz, $2::pg_catalog.int4)`,
			time.Now(), 1).Scan(&purged); err != nil || purged != 1 {
			t.Fatalf("purge through the definer = %d, %v, want 1 row", purged, err)
		}
	})

	t.Run("security_invoker_warns", func(t *testing.T) {
		role := l.role(t, "dinvoker", "")
		d := l.definer(t, "dinvoker", role)
		l.exec(t, `ALTER FUNCTION `+d.signature+` SECURITY INVOKER`)

		p := l.inspect(t, role, d.table)
		if f := p.Functions[d.signature]; f.SecurityDefiner || p.UnsafeFunctions() != nil ||
			!slices.Equal(p.InoperativeFunctions(), []string{d.signature}) {
			t.Fatalf("evidence = %+v, want inoperative only", p.Functions)
		}
		warnedInoperative(t, role, d.table.spec, d.signature, `reason="security_definer=false"`, escaped("ALTER FUNCTION "+p.Functions[d.signature].function+" SECURITY DEFINER"))

		// Inoperative is loud at call time: the role's own privileges apply.
		db, _ := l.openAs(t, role, "", false)
		if _, err := db.Exec(ctx, `SELECT `+d.signature[:len(d.signature)-len(definerArgs)]+`($1::pg_catalog.timestamptz, $2::pg_catalog.int4)`,
			time.Now(), 1); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("invoker call = %v, want sdk.ErrForbidden", err)
		}
	})

	t.Run("reset_search_path_refused", func(t *testing.T) {
		role := l.role(t, "dreset", "")
		d := l.definer(t, "dreset", role)
		l.exec(t, `ALTER FUNCTION `+d.signature+` RESET search_path`)

		if f := l.inspect(t, role, d.table).Functions[d.signature]; f.SearchPathPinned || !f.Exists {
			t.Fatalf("evidence = %+v, want search_path unpinned", f)
		}
		refusedUnsafe(t, role, d.table.spec, d.signature, "search_path_pinned=false",
			`SET search_path = pg_catalog, "`+d.table.schema+`", pg_temp (proconfig must hold exactly this one setting)`)

		out, err := l.probeAs(t, role, environment.ModeDevelopment, false, d.table.spec)
		if err != nil || strings.Count(out, "\n") != 0 || !strings.Contains(out, "append_only=unenforced") ||
			!strings.Contains(out, "function="+escaped(d.signature)+" search_path_pinned=false") {
			t.Fatalf("development = %v, want one append_only=unenforced line naming the function:\n%s", err, out)
		}

		// The recipe's DO block is the re-pin snippet.
		l.migrate(t, d.table.schema, d.pinStatement())
		l.admitted(t, role, d.table)
	})

	t.Run("wrong_search_path_refused", func(t *testing.T) {
		role := l.role(t, "dwrong", "")
		d := l.definer(t, "dwrong", role)
		schema := `"` + d.table.schema + `"`
		for name, statements := range map[string][]string{
			"no_pg_temp":    {`ALTER FUNCTION ` + d.bare + ` SET search_path = pg_catalog, ` + schema},
			"other_schema":  {`ALTER FUNCTION ` + d.bare + ` SET search_path = pg_catalog, public, pg_temp`},
			"pg_temp_first": {`ALTER FUNCTION ` + d.bare + ` SET search_path = pg_temp, pg_catalog, ` + schema},
			"from_current":  {`ALTER FUNCTION ` + d.bare + ` SET search_path FROM CURRENT`},
			"two_settings":  {d.pinStatement(), `ALTER FUNCTION ` + d.bare + ` SET work_mem = '64kB'`},
		} {
			t.Run(name, func(t *testing.T) {
				l.migrate(t, d.table.schema, append([]string{`ALTER FUNCTION ` + d.bare + ` RESET ALL`}, statements...)...)
				if f := l.inspect(t, role, d.table).Functions[d.signature]; f.SearchPathPinned {
					t.Fatalf("evidence = %+v, want search_path unpinned", f)
				}
				refusedUnsafe(t, role, d.table.spec, d.signature, "search_path_pinned=false", "RESET ALL")
			})
		}
		l.migrate(t, d.table.schema, `ALTER FUNCTION `+d.bare+` RESET ALL`, d.pinStatement())
		l.admitted(t, role, d.table)
	})

	t.Run("public_execute_refused", func(t *testing.T) {
		role := l.role(t, "dpublic", "")
		d := l.definer(t, "dpublic", role)
		l.exec(t, `GRANT EXECUTE ON FUNCTION `+d.signature+` TO PUBLIC`)

		if f := l.inspect(t, role, d.table).Functions[d.signature]; !f.PublicExecute {
			t.Fatalf("evidence = %+v, want PUBLIC EXECUTE", f)
		}
		fn := l.inspect(t, role, d.table).Functions[d.signature].function
		refusedUnsafe(t, role, d.table.spec, d.signature, "public_execute=true",
			"REVOKE EXECUTE ON FUNCTION "+fn+" FROM PUBLIC", "GRANT EXECUTE ON FUNCTION "+fn+` TO `+role)

		l.exec(t, `REVOKE EXECUTE ON FUNCTION `+d.signature+` FROM PUBLIC`)
		l.admitted(t, role, d.table)
	})

	t.Run("errors_join_names_every_function", func(t *testing.T) {
		role := l.role(t, "djoin", "")
		first := l.definer(t, "djoina", role)
		second := l.definer(t, "djoinb", role)
		l.exec(t,
			`GRANT EXECUTE ON FUNCTION `+first.signature+` TO PUBLIC`,
			`ALTER FUNCTION `+second.signature+` RESET search_path`,
		)

		_, err := l.probeAs(t, role, environment.ModeProduction, true, first.table.spec, second.table.spec)
		var joined interface{ Unwrap() []error }
		if !errors.Is(err, ErrDefinerFunctionUnsafe) || !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
			t.Fatalf("ProbeAppendOnly = %v, want two joined refusals", err)
		}
		for _, d := range []aoDefiner{first, second} {
			if !strings.Contains(err.Error(), "function="+d.signature) {
				t.Errorf("refusal does not name %s:\n%v", d.signature, err)
			}
		}
	})

	t.Run("owned_by_another_role_warns", func(t *testing.T) {
		role := l.role(t, "downer", "")
		d := l.definer(t, "downer", role)
		other := l.id + "_dother"
		l.exec(t, `CREATE ROLE "`+other+`" NOLOGIN`)
		t.Cleanup(func() { l.dropRole(t, other) })
		l.exec(t, `ALTER FUNCTION `+d.signature+` OWNER TO "`+other+`"`)

		p := l.inspect(t, role, d.table)
		if f := p.Functions[d.signature]; f.OwnerIsTableOwner || f.OwnerSuperuser || p.UnsafeFunctions() != nil {
			t.Fatalf("evidence = %+v, want a foreign owner only", f)
		}
		warnedInoperative(t, role, d.table.spec, d.signature, `reason="owner_is_table_owner=false"`,
			escaped("ALTER FUNCTION "+p.Functions[d.signature].function+" OWNER TO "+l.migrator))
	})

	t.Run("superuser_owner_warns", func(t *testing.T) {
		role := l.role(t, "dsuper", "")
		d := l.definer(t, "dsuper", role)
		l.exec(t, `ALTER FUNCTION `+d.signature+` OWNER TO CURRENT_USER`)

		if f := l.inspect(t, role, d.table).Functions[d.signature]; !f.OwnerSuperuser || !f.OwnerIsTableOwner {
			t.Fatalf("evidence = %+v, want a superuser owner", f)
		}
		warnedInoperative(t, role, d.table.spec, d.signature, `reason="owner_superuser=true"`, "NOSUPERUSER")
	})

	t.Run("schema_create_warns", func(t *testing.T) {
		role := l.role(t, "dcreate", "")
		d := l.definer(t, "dcreate", role)
		l.exec(t, `GRANT CREATE ON SCHEMA "`+d.table.schema+`" TO PUBLIC`)

		if f := l.inspect(t, role, d.table).Functions[d.signature]; !f.PathSchemaCreate {
			t.Fatalf("evidence = %+v, want CREATE on the schema", f)
		}
		warnedInoperative(t, role, d.table.spec, d.signature, `reason="path_schema_create=true"`,
			escaped(`REVOKE CREATE ON SCHEMA "`+d.table.schema+`" FROM PUBLIC, `+role))
	})

	t.Run("decoy_earlier_on_the_path_refused", func(t *testing.T) {
		role := l.role(t, "ddecoy", "")
		d := l.definer(t, "ddecoy", role)
		decoy := l.id + "_decoy"
		l.exec(t, `CREATE SCHEMA "`+decoy+`"`)
		t.Cleanup(func() { _, _ = l.admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+decoy+`" CASCADE`) })
		l.exec(t,
			`CREATE FUNCTION "`+decoy+`".`+d.name+`(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4)
RETURNS pg_catalog.int8 LANGUAGE sql AS $fn$ SELECT 0::pg_catalog.int8 $fn$`,
			`GRANT USAGE ON SCHEMA "`+decoy+`" TO "`+role+`"`,
			`ALTER ROLE "`+role+`" SET search_path = "`+decoy+`", "`+d.table.schema+`"`,
		)

		// A bare host: the table resolves to the fixture's schema, the
		// signature to the decoy earlier on the role's search_path.
		bare := AppendOnlyTable{Name: d.table.name, Canonical: d.table.spec.Canonical, Functions: []DefinerFunction{{Signature: d.bare}}}
		report, _ := l.pinnedInspect(t, role, bare)
		if f := report.Tables[0].Functions[d.bare]; !report.Tables[0].Exists || !f.Exists || f.SameNamespace {
			t.Fatalf("evidence = %+v, want the decoy resolved outside the table's schema", report.Tables[0])
		}
		err := refusedUnsafe(t, role, bare, d.bare, "same_namespace=false namespace="+decoy, "outside schema \""+d.table.schema+"\"")
		if strings.Contains(err.Error(), "REVOKE") {
			t.Errorf("a foreign function's refusal prescribes a grant change:\n%v", err)
		}

		// Listed schema-qualified in another schema, it is refused the same way.
		qualified := d.table.spec
		qualified.Functions = []DefinerFunction{{Signature: `"` + decoy + `".` + d.bare}}
		if f := l.inspect(t, role, aoTable{spec: qualified}).Functions[qualified.Functions[0].Signature]; !f.Exists || f.SameNamespace {
			t.Fatalf("evidence = %+v, want a foreign namespace", f)
		}
		refusedUnsafe(t, role, qualified, qualified.Functions[0].Signature, "same_namespace=false namespace="+decoy)
	})

	t.Run("bare_schema_resolves_before_the_pin", func(t *testing.T) {
		role := l.role(t, "dbare", "")
		d := l.definer(t, "dbare", role)
		l.exec(t, `ALTER ROLE "`+role+`" SET search_path = "`+d.table.schema+`"`)
		bare := AppendOnlyTable{Name: d.table.name, Canonical: d.table.spec.Canonical, Functions: []DefinerFunction{{Signature: d.bare}}}

		report, tx := l.pinnedInspect(t, role, bare)
		if f := report.Tables[0].Functions[d.bare]; bits(f) != cleanDefiner {
			t.Fatalf("evidence = %+v, want the clean definer resolved on the role's search_path", f)
		}
		// Under the pin the bare signature no longer resolves: resolution had
		// to come first.
		var after *uint32
		if err := tx.QueryRow(ctx, resolveFunctionSQL, d.bare).Scan(&after); err != nil || after != nil {
			t.Fatalf("to_regprocedure after the pin = %v, %v, want NULL", after, err)
		}
		l.admitted(t, role, aoTable{spec: bare})

		l.exec(t, `GRANT EXECUTE ON FUNCTION `+d.signature+` TO PUBLIC`)
		refusedUnsafe(t, role, bare, d.bare, "public_execute=true")
	})

	t.Run("absent_function_is_not_an_error", func(t *testing.T) {
		role := l.role(t, "dabsent", "")
		d := l.definer(t, "dabsent", role)
		missing := `"` + d.table.schema + `".` + d.table.name + "_missing(pg_catalog.int4)"
		spec := d.table.spec
		spec.Functions = []DefinerFunction{{Signature: missing}}

		p := l.inspect(t, role, aoTable{spec: spec})
		if f, ok := p.Functions[missing]; !ok || f != (FunctionPrivilege{}) || p.UnsafeFunctions() != nil || p.InoperativeFunctions() != nil {
			t.Fatalf("functions = %+v, want Exists false and no finding", p.Functions)
		}
		l.admitted(t, role, aoTable{spec: spec})
	})

	t.Run("execute_granted_and_revoked", func(t *testing.T) {
		role := l.role(t, "dexec", "")
		d := l.definer(t, "dexec", role)
		l.exec(t, `REVOKE EXECUTE ON FUNCTION `+d.signature+` FROM "`+role+`"`)

		p := l.inspect(t, role, d.table)
		if f := p.Functions[d.signature]; f.Execute || p.UnsafeFunctions() != nil || p.InoperativeFunctions() != nil {
			t.Fatalf("evidence = %+v, want EXECUTE missing and no finding", f)
		}
		l.admitted(t, role, d.table)

		l.exec(t, `GRANT EXECUTE ON FUNCTION `+d.signature+` TO "`+role+`"`)
		if f := l.inspect(t, role, d.table).Functions[d.signature]; !f.Execute {
			t.Fatalf("evidence = %+v, want EXECUTE held", f)
		}
	})

	t.Run("posture_role_preflight_matches_the_role", func(t *testing.T) {
		role := l.role(t, "dpre", "")
		d := l.definer(t, "dpre", role)
		l.exec(t, `GRANT CREATE ON SCHEMA "`+d.table.schema+`" TO PUBLIC`)

		// -posture-role: the owner's connection, a read-only transaction, the
		// runtime role named.
		tx, err := l.admin.BeginRead(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		preflight, err := InspectTablePrivileges(ctx, tx, role, d.table.spec)
		if err != nil {
			t.Fatal(err)
		}
		p := preflight.Tables[0]
		f := p.Functions[d.signature]
		if !f.Execute || !f.PathSchemaCreate || p.UnsafeFunctions() != nil ||
			!slices.Equal(p.InoperativeFunctions(), []string{d.signature}) {
			t.Fatalf("preflight = %+v, want execute=true unsafe=[] inoperative=[%s]", p, d.signature)
		}
		own, _ := l.pinnedInspect(t, role, d.table.spec)
		if !reflect.DeepEqual(own, preflight) {
			t.Fatalf("the role's own pinned report differs from the preflight:\n own=%+v\npre=%+v", own, preflight)
		}
	})

	t.Run("no_functions_output_unchanged", func(t *testing.T) {
		role := l.role(t, "dsame", "")
		d := l.definer(t, "dsame", role)
		l.exec(t, `REVOKE INSERT ON `+d.table.qualified()+` FROM "`+role+`"`)
		without := d.table.spec
		without.Functions = nil

		for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
			outWith, errWith := l.probeAs(t, role, mode, false, d.table.spec)
			outWithout, errWithout := l.probeAs(t, role, mode, false, without)
			if outWith != outWithout || fmt.Sprint(errWith) != fmt.Sprint(errWithout) {
				t.Fatalf("%s: a clean definer changed the output:\nwith:    %s / %v\nwithout: %s / %v", mode, outWith, errWith, outWithout, errWithout)
			}
			if mode.IsProduction() && !errors.Is(errWithout, ErrAppendOnlyTableUnwritable) {
				t.Fatalf("production = %v, want the insert=false refusal", errWithout)
			}
		}
	})
}
