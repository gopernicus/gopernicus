package turso

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// auditTriggerSQL is the canonical shape of one immutability trigger, as a
// store's migration file writes it.
func auditTriggerSQL(table, suffix, event string) string {
	return "CREATE TRIGGER " + table + "_immutable_" + suffix + " BEFORE " + event + " ON " + table + "\n" +
		"BEGIN\n" +
		"  SELECT RAISE(ABORT, '" + table + " is append-only');\n" +
		"END;"
}

// auditTable is the spec a store would pass for table's trigger pair.
func auditTable(table string) AppendOnlyTable {
	return AppendOnlyTable{Name: table, Triggers: []TableTrigger{
		{Name: table + "_immutable_update", Canonical: []TriggerBody{{SQL: auditTriggerSQL(table, "update", "UPDATE")}}},
		{Name: table + "_immutable_delete", Canonical: []TriggerBody{{SQL: auditTriggerSQL(table, "delete", "DELETE")}}},
	}}
}

// openAuditFile opens a fresh local file database under mode, creates the
// audit table and applies setup, and returns the DB with a buffer holding only
// what is logged after Open.
func openAuditFile(t *testing.T, mode environment.Mode, setup ...string) (*DB, *bytes.Buffer) {
	t.Helper()
	logger, buf := postureLogger()
	db, err := Open(context.Background(), Config{
		URL:         sdk.Secret("file:" + filepath.Join(t.TempDir(), "audit.db")),
		RuntimeMode: mode,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.RuntimeMode() != mode {
		t.Fatalf("RuntimeMode() = %q, want %q", db.RuntimeMode(), mode)
	}
	for _, stmt := range append([]string{"CREATE TABLE security_events (id TEXT PRIMARY KEY)"}, setup...) {
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	buf.Reset()
	return db, buf
}

// TestProbeAuditImmutable is the trigger matrix — present, absent, different —
// against a local file database in every mode: never an error, silent in the
// zero mode and when the pair is intact, otherwise one WARN naming the table,
// the trigger and the reason.
func TestProbeAuditImmutable(t *testing.T) {
	const table = "security_events"
	pair := []string{
		"DROP TRIGGER IF EXISTS " + table + "_immutable_update",
		auditTriggerSQL(table, "update", "UPDATE"),
		"DROP TRIGGER IF EXISTS " + table + "_immutable_delete",
		auditTriggerSQL(table, "delete", "DELETE"),
	}
	states := []struct {
		name    string
		setup   []string
		reason  string
		trigger string
	}{
		{"present", pair, "", ""},
		{"absent", nil, "absent", table + "_immutable_update"},
		{"only_update", pair[:2], "absent", table + "_immutable_delete"},
		{"different", []string{
			pair[0], pair[1], pair[2],
			"CREATE TRIGGER " + table + "_immutable_delete BEFORE DELETE ON " + table + " WHEN OLD.id = 'x'\nBEGIN\n  SELECT RAISE(ABORT, '" + table + " is append-only');\nEND;",
		}, "differs", table + "_immutable_delete"},
	}
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment, environment.ModeProduction} {
		for _, st := range states {
			name := string(mode)
			if name == "" {
				name = "zero"
			}
			t.Run(name+"/"+st.name, func(t *testing.T) {
				db, buf := openAuditFile(t, mode, st.setup...)
				if err := db.ProbeAuditImmutable(context.Background(), auditTable(table)); err != nil {
					t.Fatalf("ProbeAuditImmutable = %v, want nil (never a refusal)", err)
				}
				out := buf.String()
				if mode == "" || st.reason == "" {
					if out != "" {
						t.Fatalf("logged, want silence:\n%s", out)
					}
					return
				}
				if strings.Count(out, "\n") != 1 {
					t.Fatalf("logged %q, want one line", out)
				}
				for _, want := range []string{
					"level=WARN",
					"turso: audit table has no immutability triggers; restore the migration file",
					"table=" + table,
					"trigger=" + st.trigger,
					"reason=" + st.reason,
				} {
					if !strings.Contains(out, want) {
						t.Errorf("WARN lacks %q:\n%s", want, out)
					}
				}
			})
		}
	}
}

// TestMapError_AppendOnly: the immutability triggers' abort maps to
// sdk.ErrForbidden in the bare RAISE(ABORT) text, the remote driver's shape and
// modernc's "constraint failed: … (1811)" shape — the last also produced by a
// real trigger on a local file — while the constraint mappings are unchanged.
func TestMapError_AppendOnly(t *testing.T) {
	for _, msg := range []string{
		"security_events is append-only",
		"failed to execute SQL:\nSQLite error: security_events is append-only",
		"constraint failed: security_events is append-only (1811)",
	} {
		if got := MapError(errors.New(msg)); !errors.Is(got, sdk.ErrForbidden) {
			t.Errorf("MapError(%q) = %v, want sdk.ErrForbidden", msg, got)
		}
	}
	if got := MapError(errors.New("constraint failed: CHECK constraint failed: x (275)")); !errors.Is(got, sdk.ErrInvalidInput) {
		t.Errorf("CHECK mapping changed: %v", got)
	}

	db, _ := openAuditFile(t, "",
		auditTriggerSQL("security_events", "update", "UPDATE"),
		auditTriggerSQL("security_events", "delete", "DELETE"),
		"INSERT INTO security_events (id) VALUES ('a')")
	ctx := context.Background()
	for _, stmt := range []string{"UPDATE security_events SET id = 'b'", "DELETE FROM security_events"} {
		_, err := db.Exec(ctx, stmt)
		if !errors.Is(err, sdk.ErrForbidden) {
			t.Errorf("%s = %v, want sdk.ErrForbidden", stmt, err)
		}
		_, raw := db.Underlying().ExecContext(ctx, stmt)
		if raw == nil || !strings.Contains(raw.Error(), "constraint failed: security_events is append-only (1811)") {
			t.Errorf("%s raw error = %v, want modernc's constraint failed shape", stmt, raw)
		}
	}
}
