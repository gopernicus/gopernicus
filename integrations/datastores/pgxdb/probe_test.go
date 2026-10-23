package pgxdb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// probeQuerier is a hermetic Querier stub for the to_regclass probe: it answers
// from a set of relations it "has", records the probe order, and can fail the
// query outright, so ProbeTables' short-circuit can be asserted without a live
// Postgres.
type probeQuerier struct {
	have    map[string]bool
	scanErr error

	probed []string
}

func (p *probeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (p *probeQuerier) Query(context.Context, string, ...any) (jackpgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (p *probeQuerier) QueryRow(_ context.Context, _ string, args ...any) jackpgx.Row {
	table, _ := args[0].(string)
	p.probed = append(p.probed, table)
	return probeRow{table: table, present: p.have[table], err: p.scanErr}
}

// probeRow replays to_regclass's answer: the relation's name for a present
// relation, SQL NULL (a nil *string) for an absent one.
type probeRow struct {
	table   string
	present bool
	err     error
}

func (r probeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	reg, ok := dest[0].(**string)
	if !ok {
		return errors.New("scan: destination is not **string")
	}
	if r.present {
		name := r.table
		*reg = &name
	}
	return nil
}

// TestProbeTables: every named relation present is a nil error with each probed
// in order; the first absent relation stops the loop and comes back naming it,
// wrapping sdk.ErrNotFound; a query failure travels through MapError and also
// stops the loop; no tables at all is a nil error.
func TestProbeTables(t *testing.T) {
	ctx := context.Background()

	t.Run("all_present", func(t *testing.T) {
		pq := &probeQuerier{have: map[string]bool{"plans": true, "gps.tiles": true, "reports": true}}
		if err := ProbeTables(ctx, pq, "plans", "gps.tiles", "reports"); err != nil {
			t.Fatalf("ProbeTables: %v", err)
		}
		if got := strings.Join(pq.probed, ","); got != "plans,gps.tiles,reports" {
			t.Fatalf("probed = %q, want the tables in order", got)
		}
	})

	t.Run("first_absent_names_it_and_stops", func(t *testing.T) {
		pq := &probeQuerier{have: map[string]bool{"plans": true, "reports": true}}
		err := ProbeTables(ctx, pq, "plans", "gps.tiles", "reports")
		if !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "gps.tiles") {
			t.Fatalf("err = %v, want the first failing table named", err)
		}
		if got := strings.Join(pq.probed, ","); got != "plans,gps.tiles" {
			t.Fatalf("probed = %q, want probing to stop at the first failure", got)
		}
	})

	t.Run("query_failure_mapped_and_stops", func(t *testing.T) {
		pq := &probeQuerier{have: map[string]bool{"plans": true}, scanErr: &pgconn.PgError{Code: "42501"}}
		err := ProbeTables(ctx, pq, "plans", "reports")
		if errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("err = %v, a query failure must never be reported as a missing table", err)
		}
		if err == nil {
			t.Fatal("ProbeTables = nil, want the query failure")
		}
		if len(pq.probed) != 1 {
			t.Fatalf("probed = %v, want probing to stop at the first failure", pq.probed)
		}
	})

	t.Run("no_tables", func(t *testing.T) {
		pq := &probeQuerier{}
		if err := ProbeTables(ctx, pq); err != nil {
			t.Fatalf("ProbeTables() with no tables = %v, want nil", err)
		}
	})
}

// appendOnlyRow is a hand-built TablePrivilege for an existing table with a seq
// sequence: INSERT and sequence USAGE held, nothing else, no trigger pair.
func appendOnlyRow(table string) TablePrivilege {
	return TablePrivilege{
		Table: table, Exists: true, Insert: true, SequenceUsage: true, TriggerState: triggerAbsent,
		schema: "public", relation: table, sequence: "public." + table + "_seq",
	}
}

// verified returns p with the verified trigger pair.
func verified(p TablePrivilege) TablePrivilege {
	p.ImmutableTrigger, p.TriggerState = true, triggerVerified
	return p
}

// TestTablePrivilege_Policy is the Writable / CannotAppend / ProtectedBy matrix:
// owner and superuser are writable with or without the trigger pair; a modifying
// privilege is writable only without it; DELETE does not count under
// AllowDelete; a missing INSERT or sequence USAGE cannot append.
func TestTablePrivilege_Policy(t *testing.T) {
	with := func(mutate func(*TablePrivilege)) TablePrivilege {
		p := appendOnlyRow("security_events")
		mutate(&p)
		return p
	}
	cases := []struct {
		name         string
		p            TablePrivilege
		writable     bool
		cannotAppend bool
		protectedBy  string
	}{
		{"insert_only", appendOnlyRow("security_events"), false, false, protectedByPrivilege},
		{"insert_only_verified", verified(appendOnlyRow("security_events")), false, false, protectedByPrivilege},
		{"owner_table", with(func(p *TablePrivilege) { p.Owner, p.OwnerKind = true, ownerTable }), true, false, protectedByNone},
		{"owner_with_triggers", verified(with(func(p *TablePrivilege) { p.Owner, p.OwnerKind = true, ownerTable })), true, false, protectedByNone},
		{"owner_schema", with(func(p *TablePrivilege) { p.Owner, p.OwnerKind = true, ownerSchema }), true, false, protectedByNone},
		{"owner_function", verified(with(func(p *TablePrivilege) { p.Owner, p.OwnerKind = true, ownerFunction })), true, false, protectedByNone},
		{"superuser", with(func(p *TablePrivilege) { p.Superuser = true }), true, false, protectedByNone},
		{"superuser_with_triggers", verified(with(func(p *TablePrivilege) { p.Superuser = true })), true, false, protectedByNone},
		{"update", with(func(p *TablePrivilege) { p.Update = true }), true, false, protectedByNone},
		{"update_verified", verified(with(func(p *TablePrivilege) { p.Update = true })), false, false, protectedByTrigger},
		{"delete", with(func(p *TablePrivilege) { p.Delete = true }), true, false, protectedByNone},
		{"delete_verified", verified(with(func(p *TablePrivilege) { p.Delete = true })), false, false, protectedByTrigger},
		{"delete_allowed", with(func(p *TablePrivilege) { p.Delete, p.AllowDelete = true, true }), false, false, protectedByPrivilege},
		{"delete_allowed_update", with(func(p *TablePrivilege) { p.Delete, p.AllowDelete, p.Update = true, true, true }), true, false, protectedByNone},
		{"delete_allowed_update_verified", verified(with(func(p *TablePrivilege) { p.Delete, p.AllowDelete, p.Update = true, true, true })), false, false, protectedByTrigger},
		{"truncate", with(func(p *TablePrivilege) { p.Truncate = true }), true, false, protectedByNone},
		{"trigger", with(func(p *TablePrivilege) { p.Trigger = true }), true, false, protectedByNone},
		{"trigger_verified", verified(with(func(p *TablePrivilege) { p.Trigger = true })), false, false, protectedByTrigger},
		{"no_insert", with(func(p *TablePrivilege) { p.Insert = false }), false, true, protectedByPrivilege},
		{"no_sequence_usage", with(func(p *TablePrivilege) { p.SequenceUsage = false }), false, true, protectedByPrivilege},
		{"owner_without_sequence_usage", with(func(p *TablePrivilege) { p.Owner, p.OwnerKind, p.SequenceUsage = true, ownerTable, false }), true, true, protectedByNone},
		{"absent", TablePrivilege{Table: "security_events"}, false, true, protectedByNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.Writable(); got != tc.writable {
				t.Errorf("Writable() = %v, want %v", got, tc.writable)
			}
			if got := tc.p.CannotAppend(); got != tc.cannotAppend {
				t.Errorf("CannotAppend() = %v, want %v", got, tc.cannotAppend)
			}
			if got := tc.p.ProtectedBy(false); got != tc.protectedBy {
				t.Errorf("ProtectedBy(false) = %q, want %q", got, tc.protectedBy)
			}
			want := tc.protectedBy
			if tc.writable && tc.p.Exists {
				want = protectedByWaiver
			}
			if got := tc.p.ProtectedBy(true); got != want {
				t.Errorf("ProtectedBy(true) = %q, want %q", got, want)
			}
		})
	}
}

// TestTablePrivilege_Reasons pins what a refusal names: the owner kind,
// superuser, the held privileges (DELETE omitted under AllowDelete) with the
// trigger state, tgenabled bare, and the sequence.
func TestTablePrivilege_Reasons(t *testing.T) {
	owner := appendOnlyRow("security_events")
	owner.Owner, owner.OwnerKind = true, ownerSchema
	if got := owner.writableReason(); got != "owner=schema" {
		t.Errorf("owner reason = %q", got)
	}
	super := appendOnlyRow("security_events")
	super.Superuser = true
	if got := super.writableReason(); got != "superuser" {
		t.Errorf("superuser reason = %q", got)
	}
	grants := appendOnlyRow("iam_operations")
	grants.Update, grants.Delete, grants.Truncate, grants.Trigger, grants.AllowDelete = true, true, true, true, true
	if got := grants.writableReason(); got != "privileges=update,truncate,trigger trigger=absent" {
		t.Errorf("privilege reason = %q", got)
	}
	grants.AllowDelete, grants.TriggerState = false, "tgenabled=O"
	if got := grants.writableReason(); got != "privileges=update,delete,truncate,trigger tgenabled=O" {
		t.Errorf("tgenabled reason = %q", got)
	}
	noSeq := appendOnlyRow("security_events")
	noSeq.SequenceUsage, noSeq.Insert = false, false
	if got := noSeq.unwritableReason(); got != "insert=false sequence_usage=false sequence=public.security_events_seq" {
		t.Errorf("unwritable reason = %q", got)
	}
}

// TestTablePrivilege_Fix pins the remedy: the lead sentence first, DELETE kept
// under AllowDelete, the sequence grant only for a table with one.
func TestTablePrivilege_Fix(t *testing.T) {
	p := appendOnlyRow("security_events")
	fix := p.fix("app")
	for _, want := range []string{
		"REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON security_events FROM app",
		"GRANT USAGE ON SEQUENCE public.security_events_seq TO app",
		"ALTER TABLE security_events OWNER TO <migrator>",
		"ALTER SCHEMA public OWNER TO <migrator>",
		"ALTER TABLE security_events ENABLE ALWAYS TRIGGER security_events_immutable",
		"ALTER TABLE security_events ENABLE ALWAYS TRIGGER security_events_no_truncate",
	} {
		if !strings.Contains(fix, want) {
			t.Errorf("fix lacks %q:\n%s", want, fix)
		}
	}
	if !strings.HasPrefix(fix, appendOnlyFixLead) {
		t.Errorf("fix does not begin with the lead: %s", fix)
	}

	ops := appendOnlyRow("iam_operations")
	ops.AllowDelete, ops.sequence = true, ""
	fix = ops.fix("app")
	if !strings.Contains(fix, "REVOKE UPDATE, TRUNCATE, TRIGGER ON iam_operations FROM app") {
		t.Errorf("AllowDelete fix revokes DELETE: %s", fix)
	}
	if strings.Contains(fix, "SEQUENCE") {
		t.Errorf("fix for a table without seq grants a sequence: %s", fix)
	}
}

// TestTriggerEvidence_State walks the verification order: every departure
// from the canonical pair names its own reason.
func TestTriggerEvidence_State(t *testing.T) {
	const body = "\nBEGIN\n  RAISE EXCEPTION 'x is append-only' USING ERRCODE = '42501';\nEND\n"
	canonical := []TriggerBody{{Function: "x_refuse_write", Body: body}}
	good := triggerEvidence{
		present: true, enabled: "A", rowLevel: true, noWhen: true, noColumns: true,
		definition: "CREATE TRIGGER x_immutable BEFORE DELETE OR UPDATE ON public.x FOR EACH ROW EXECUTE FUNCTION public.x_refuse_write()",
		quotedName: "x_immutable", relation: "public.x", function: "public.x_refuse_write",
		proname: "x_refuse_write", prosrc: body, config: []string{canonicalTriggerConfig},
		language: "plpgsql", sameNamespace: true,
	}
	if got := good.state(true, "DELETE OR UPDATE", canonical); got != triggerVerified {
		t.Fatalf("canonical trigger state = %q, want verified", got)
	}
	cases := []struct {
		name   string
		mutate func(*triggerEvidence)
		want   string
	}{
		{"absent", func(e *triggerEvidence) { *e = triggerEvidence{} }, triggerAbsent},
		{"internal", func(e *triggerEvidence) { e.internal = true }, triggerInternal},
		{"origin", func(e *triggerEvidence) { e.enabled = "O" }, "tgenabled=O"},
		{"disabled", func(e *triggerEvidence) { e.enabled = "D" }, "tgenabled=D"},
		{"statement_level", func(e *triggerEvidence) { e.rowLevel = false }, triggerLevel},
		{"when", func(e *triggerEvidence) { e.noWhen = false }, triggerWhenClause},
		{"columns", func(e *triggerEvidence) { e.noColumns = false }, triggerColumnList},
		{"namespace", func(e *triggerEvidence) { e.sameNamespace = false }, triggerNamespace},
		{"other_function", func(e *triggerEvidence) { e.proname = "other" }, triggerDiffers},
		{"definition", func(e *triggerEvidence) { e.definition = strings.Replace(e.definition, "BEFORE", "AFTER", 1) }, triggerDiffers},
		{"language", func(e *triggerEvidence) { e.language = "c" }, triggerLanguage},
		{"body", func(e *triggerEvidence) { e.prosrc = body + " " }, triggerBodyDiffers},
		{"security_definer", func(e *triggerEvidence) { e.secdef = true }, triggerSecurityDefiner},
		{"proconfig_missing", func(e *triggerEvidence) { e.config = nil }, triggerProconfig},
		{"proconfig_other", func(e *triggerEvidence) { e.config = []string{"search_path=public"} }, triggerProconfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := good
			tc.mutate(&ev)
			if got := ev.state(true, "DELETE OR UPDATE", canonical); got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}

	// A second canonical body for the same function verifies too.
	alt := []TriggerBody{{Function: "x_refuse_write", Body: "old"}, {Function: "x_refuse_write", Body: body}}
	if got := good.state(true, "DELETE OR UPDATE", alt); got != triggerVerified {
		t.Fatalf("state against two canonical bodies = %q, want verified", got)
	}
	if got := good.state(true, "DELETE OR UPDATE", nil); got != triggerDiffers {
		t.Fatalf("state with no canonical body = %q, want differs", got)
	}
}

// TestAppendOnlyPosture is the mode matrix over a hand-built report: zero mode
// is silent; development never refuses and logs one combined line; production
// joins one refusal per finding naming every table; the acknowledgement turns
// each Writable refusal into a WARN and never admits an Unwritable one.
func TestAppendOnlyPosture(t *testing.T) {
	owner := appendOnlyRow("security_events")
	owner.Owner, owner.OwnerKind = true, ownerTable
	granted := appendOnlyRow("iam_audit")
	granted.Update, granted.Delete = true, true
	noSeq := verified(appendOnlyRow("secrets_audit"))
	noSeq.SequenceUsage = false
	clean := verified(appendOnlyRow("iam_operations"))
	clean.Update = true
	report := Report{Role: "app", Tables: []TablePrivilege{owner, granted, noSeq, clean}}

	t.Run("zero_mode_silent", func(t *testing.T) {
		logger, buf := captureLogger()
		if err := appendOnlyPosture(report, "", false, logger); err != nil {
			t.Fatalf("zero mode = %v, want nil", err)
		}
		if buf.Len() != 0 {
			t.Fatalf("zero mode logged:\n%s", buf)
		}
	})

	t.Run("development_one_line", func(t *testing.T) {
		logger, buf := captureLogger()
		if err := appendOnlyPosture(report, environment.ModeDevelopment, false, logger); err != nil {
			t.Fatalf("development = %v, want nil", err)
		}
		out := buf.String()
		if n := strings.Count(out, "\n"); n != 1 {
			t.Fatalf("development logged %d lines, want 1:\n%s", n, out)
		}
		for _, want := range []string{"level=WARN", "append_only=unenforced", "security_events", "iam_audit", "secrets_audit", "owner=table", "privileges=update,delete trigger=absent", "sequence_usage=false"} {
			if !strings.Contains(out, want) {
				t.Errorf("development line lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "iam_operations") {
			t.Errorf("development line names a protected table:\n%s", out)
		}
	})

	t.Run("development_clean_is_silent", func(t *testing.T) {
		logger, buf := captureLogger()
		if err := appendOnlyPosture(Report{Role: "app", Tables: []TablePrivilege{clean}}, environment.ModeDevelopment, false, logger); err != nil {
			t.Fatal(err)
		}
		if buf.Len() != 0 {
			t.Fatalf("clean development logged:\n%s", buf)
		}
	})

	t.Run("production_joins_every_table", func(t *testing.T) {
		logger, buf := captureLogger()
		err := appendOnlyPosture(report, environment.ModeProduction, false, logger)
		if !errors.Is(err, ErrAppendOnlyTableWritable) || !errors.Is(err, ErrAppendOnlyTableUnwritable) {
			t.Fatalf("production = %v, want both refusals", err)
		}
		var joined interface{ Unwrap() []error }
		if !errors.As(err, &joined) || len(joined.Unwrap()) != 3 {
			t.Fatalf("production = %v, want three joined refusals", err)
		}
		msg := err.Error()
		for _, want := range []string{
			"security_events: owner=table",
			"iam_audit: privileges=update,delete trigger=absent",
			"secrets_audit: sequence_usage=false sequence=public.secrets_audit_seq",
			appendOnlyFixLead,
			"or set Config.AppendOnlyWritableAcknowledged",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal lacks %q:\n%s", want, msg)
			}
		}
		if strings.Contains(msg, "iam_operations") {
			t.Errorf("refusal names a protected table: %s", msg)
		}
		if buf.Len() != 0 {
			t.Errorf("an unacknowledged refusal logged:\n%s", buf)
		}
	})

	t.Run("production_acknowledged", func(t *testing.T) {
		logger, buf := captureLogger()
		err := appendOnlyPosture(report, environment.ModeProduction, true, logger)
		if errors.Is(err, ErrAppendOnlyTableWritable) || !errors.Is(err, ErrAppendOnlyTableUnwritable) {
			t.Fatalf("acknowledged = %v, want only the Unwritable refusal", err)
		}
		out := buf.String()
		if n := strings.Count(out, "posture_waiver="+ackAppendOnlyWritable); n != 2 {
			t.Fatalf("acknowledged logged %d waivers, want one per writable table:\n%s", n, out)
		}
		for _, want := range []string{
			"runtime role owns security_events: immutability triggers do not apply",
			"trigger_exempt=true",
			"runtime role can modify iam_audit: immutability triggers are not verified",
			"trigger_exempt=false",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("waiver lines lack %q:\n%s", want, out)
			}
		}

		buf.Reset()
		if err := appendOnlyPosture(Report{Role: "app", Tables: []TablePrivilege{owner, clean}}, environment.ModeProduction, true, logger); err != nil {
			t.Fatalf("acknowledged owner = %v, want nil", err)
		}
		if !strings.Contains(buf.String(), "trigger_exempt=true") {
			t.Fatalf("acknowledged owner lacks trigger_exempt=true:\n%s", buf)
		}
	})

	t.Run("production_clean", func(t *testing.T) {
		logger, buf := captureLogger()
		if err := appendOnlyPosture(Report{Role: "app", Tables: []TablePrivilege{clean}}, environment.ModeProduction, true, logger); err != nil {
			t.Fatalf("clean production = %v, want nil", err)
		}
		if buf.Len() != 0 {
			t.Fatalf("clean production logged:\n%s", buf)
		}
	})
}

// TestProbeAppendOnly_ZeroModeIsBlind: a DB opened without a mode probes
// nothing — not even a query.
func TestProbeAppendOnly_ZeroModeIsBlind(t *testing.T) {
	logger, buf := captureLogger()
	db := &DB{logger: logger}
	if err := db.ProbeAppendOnly(context.Background(), AppendOnlyTable{Name: "security_events"}); err != nil {
		t.Fatalf("zero mode ProbeAppendOnly = %v, want nil", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("zero mode logged:\n%s", buf)
	}
	if db.RuntimeMode() != "" {
		t.Fatalf("RuntimeMode() = %q, want the zero mode", db.RuntimeMode())
	}
}
