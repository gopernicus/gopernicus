package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

	jackpgx "github.com/jackc/pgx/v5"
)

// Append-only posture errors. DB.ProbeAppendOnly returns them, one per finding
// and joined, only under a production RuntimeMode; match them with errors.Is.
var (
	// ErrAppendOnlyTableWritable refuses a runtime role that can change or
	// remove an append-only table's rows: a superuser, a member of the role
	// owning the table, its schema or its trigger function, or a role holding
	// UPDATE, DELETE, TRUNCATE or TRIGGER on a table without the verified trigger
	// pair. Config.AppendOnlyWritableAcknowledged admits it.
	ErrAppendOnlyTableWritable = errors.New("postgres: runtime role can modify an append-only table")

	// ErrAppendOnlyTableUnwritable refuses a runtime role that cannot append to
	// an append-only table: it lacks INSERT, or USAGE on the sequence behind the
	// table's seq column. No acknowledgement admits it.
	ErrAppendOnlyTableUnwritable = errors.New("postgres: runtime role cannot append to an append-only table")
)

// TablePrivilege.OwnerKind values, as refusals print them after "owner=".
const (
	ownerTable    = "table"
	ownerSchema   = "schema"
	ownerFunction = "function"
)

// TablePrivilege.ProtectedBy values.
const (
	protectedByPrivilege = "privilege"
	protectedByTrigger   = "trigger"
	protectedByWaiver    = "waiver"
	protectedByNone      = "none"
)

// TablePrivilege.TriggerState values: triggerVerified, or the first reason the
// trigger pair failed verification. A trigger whose tgenabled is not 'A' is
// reported as "tgenabled=<value>".
const (
	triggerVerified        = "verified"
	triggerAbsent          = "absent"
	triggerInternal        = "internal"
	triggerLevel           = "level"
	triggerWhenClause      = "when_clause"
	triggerColumnList      = "column_list"
	triggerNamespace       = "namespace"
	triggerDiffers         = "differs"
	triggerLanguage        = "language"
	triggerBodyDiffers     = "body_differs"
	triggerSecurityDefiner = "security_definer"
	triggerProconfig       = "proconfig"
	triggerEnabledPrefix   = "tgenabled="
)

const (
	// ackAppendOnlyWritable is the acknowledgement as posture_waiver log lines
	// and refusals spell it.
	ackAppendOnlyWritable = "AppendOnlyWritableAcknowledged"

	// appendOnlyFixLead opens the remedy every append-only refusal names.
	appendOnlyFixLead = "activate the trigger files; create the runtime role with SQL, not the console"

	// canonicalTriggerConfig is the only proconfig entry a verified trigger
	// function carries: it is created SET search_path = pg_catalog, pg_temp.
	canonicalTriggerConfig = "search_path=pg_catalog, pg_temp"

	// The trigger pair, named <table><suffix>.
	immutableTriggerSuffix  = "_immutable"
	noTruncateTriggerSuffix = "_no_truncate"
)

// The append-only inspection statements. Every catalog object and function is
// qualified with pg_catalog and every name is bound, so neither the caller's
// search_path nor its temporary schema can stand in for the catalog.
const (
	inspectRoleSQL = `SELECT r.oid, r.rolname::pg_catalog.text, r.rolsuper
FROM pg_catalog.pg_roles r
WHERE r.rolname = COALESCE(NULLIF($1::pg_catalog.text, ''), CURRENT_USER::pg_catalog.text)`

	resolveTableSQL = `SELECT pg_catalog.to_regclass($1::pg_catalog.text)::pg_catalog.oid`

	// inspectTableSQL finds the seq column's sequence through its default
	// expression's dependency, never pg_get_serial_sequence, which raises on a
	// table without the column.
	inspectTableSQL = `SELECT n.nspname::pg_catalog.text, c.relname::pg_catalog.text,
	pg_catalog.pg_has_role($2::pg_catalog.oid, c.relowner, 'MEMBER'),
	pg_catalog.pg_has_role($2::pg_catalog.oid, n.nspowner, 'MEMBER'),
	pg_catalog.has_table_privilege($2::pg_catalog.oid, c.oid, 'INSERT'),
	pg_catalog.has_any_column_privilege($2::pg_catalog.oid, c.oid, 'UPDATE'),
	pg_catalog.has_table_privilege($2::pg_catalog.oid, c.oid, 'DELETE'),
	pg_catalog.has_table_privilege($2::pg_catalog.oid, c.oid, 'TRUNCATE'),
	pg_catalog.has_table_privilege($2::pg_catalog.oid, c.oid, 'TRIGGER'),
	seq.name, seq.usage
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN LATERAL (
	SELECT pg_catalog.format('%I.%I', sn.nspname, s.relname) AS name,
		pg_catalog.has_sequence_privilege($2::pg_catalog.oid, s.oid, 'USAGE') AS usage
	FROM pg_catalog.pg_attribute a
	JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
	JOIN pg_catalog.pg_depend dep ON dep.classid = 'pg_catalog.pg_attrdef'::pg_catalog.regclass
		AND dep.objid = d.oid AND dep.refclassid = 'pg_catalog.pg_class'::pg_catalog.regclass
	JOIN pg_catalog.pg_class s ON s.oid = dep.refobjid AND s.relkind = 'S'
	JOIN pg_catalog.pg_namespace sn ON sn.oid = s.relnamespace
	WHERE a.attrelid = c.oid AND a.attname = 'seq' AND NOT a.attisdropped
	LIMIT 1
) seq ON true
WHERE c.oid = $1::pg_catalog.oid`

	// inspectTriggerSQL returns one trigger's evidence, including the pieces
	// of the definition pg_get_triggerdef prints: the quoted trigger name, the
	// qualified table and the function, qualified unless it is visible on the
	// search_path the statement runs under.
	inspectTriggerSQL = `SELECT t.tgisinternal, t.tgenabled::pg_catalog.text,
	(t.tgtype::pg_catalog.int4 & 1) = 1,
	t.tgqual IS NULL,
	pg_catalog.cardinality(t.tgattr::pg_catalog.int2[]) = 0,
	pg_catalog.pg_get_triggerdef(t.oid),
	pg_catalog.quote_ident(t.tgname),
	pg_catalog.format('%I.%I', n.nspname, c.relname),
	CASE WHEN pg_catalog.pg_function_is_visible(p.oid) THEN pg_catalog.quote_ident(p.proname)
		ELSE pg_catalog.format('%I.%I', fn.nspname, p.proname) END,
	p.proname::pg_catalog.text, p.prosrc, p.prosecdef, p.proconfig, l.lanname::pg_catalog.text,
	p.pronamespace = c.relnamespace,
	pg_catalog.pg_has_role($2::pg_catalog.oid, p.proowner, 'MEMBER')
FROM pg_catalog.pg_trigger t
JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_proc p ON p.oid = t.tgfoid
JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace
JOIN pg_catalog.pg_language l ON l.oid = p.prolang
WHERE t.tgrelid = $1::pg_catalog.oid AND t.tgname = $3::pg_catalog.text`
)

// AppendOnlyTable is one append-only table a store owns, as the posture probe
// judges it. Name is bound as a parameter: bare (resolved on the connection's
// search_path) or schema-qualified. AllowDelete admits a DELETE grant, for a
// table whose trigger function decides which rows may be deleted. Canonical
// lists the trigger functions the store ships; the trigger pair verifies only
// against them.
type AppendOnlyTable struct {
	Name        string
	AllowDelete bool
	Canonical   []TriggerBody
}

// TriggerBody is one canonical trigger function for an append-only table: its
// name — it lives in the table's schema — and its plpgsql body exactly as the
// catalog's prosrc holds it, the text between the dollar quotes of the
// migration's CREATE FUNCTION. Both triggers of the pair execute it:
// <table>_immutable, BEFORE UPDATE OR DELETE FOR EACH ROW, and
// <table>_no_truncate, BEFORE TRUNCATE FOR EACH STATEMENT.
type TriggerBody struct {
	Function string
	Body     string
}

// TablePrivilege is the evidence for one append-only table and one role.
//
// Owner is membership (NOINHERIT included) in the role owning the table, its
// schema or a trigger function of the pair, and OwnerKind names which:
// "table", "schema" or "function". Update counts a column-level grant.
// SequenceUsage is USAGE on the sequence behind the table's seq column, true
// when the table has none. ImmutableTrigger is the verified trigger pair, and
// TriggerState is "verified" or the first reason it is not ("absent",
// "tgenabled=O", "when_clause", "column_list", "differs", "body_differs",
// "security_definer", "proconfig", …). A table that does not exist has Exists
// false and nothing set beyond Table, AllowDelete and Superuser.
type TablePrivilege struct {
	Table       string
	AllowDelete bool

	Exists    bool
	Owner     bool
	OwnerKind string
	Superuser bool

	Insert   bool
	Update   bool
	Delete   bool
	Truncate bool
	Trigger  bool

	SequenceUsage    bool
	ImmutableTrigger bool
	TriggerState     string

	// schema, relation and sequence are the catalog's names for the table's
	// namespace, the bare table and its seq sequence ("" without one), for the
	// remedy a refusal prints.
	schema   string
	relation string
	sequence string
}

// Report is InspectTablePrivileges' evidence: the role inspected and one
// TablePrivilege per requested table, in request order.
type Report struct {
	Role   string
	Tables []TablePrivilege
}

// triggerEvidence is one trigger's catalog row.
type triggerEvidence struct {
	present       bool
	internal      bool
	enabled       string
	rowLevel      bool
	noWhen        bool
	noColumns     bool
	definition    string
	quotedName    string
	relation      string
	function      string
	proname       string
	prosrc        string
	secdef        bool
	config        []string
	language      string
	sameNamespace bool
	functionOwner bool
}

// ProbeTable reports whether the relation named table exists — the boot-time
// check a store runs in its constructor so a host pointed at a database
// without its tables fails before serving traffic, naming the table, instead
// of 500ing on the first query. The name may be schema-qualified
// ("gps.organizations") or bare (resolved on the connection's search_path);
// it is bound as a parameter, never concatenated.
//
// Existence is resolved by to_regclass, which yields NULL for an absent
// relation rather than raising. An absent table wraps sdk.ErrNotFound; a
// query or infrastructure failure comes back through MapError and is never
// misreported as a missing table. db may be a *DB pool or a *Tx.
func ProbeTable(ctx context.Context, db Querier, table string) error {
	var reg *string
	if err := db.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
		return MapError(err)
	}
	if reg == nil {
		return fmt.Errorf("pgxdb: relation %s does not exist: %w", table, sdk.ErrNotFound)
	}
	return nil
}

// ProbeTables is ProbeTable over the relations one store owns — the boot-time
// check a repository spanning several tables runs in its constructor, instead
// of looping ProbeTable and re-wrapping the error itself. Probing stops at the
// FIRST failure and returns it unchanged: an absent relation already wraps
// sdk.ErrNotFound naming it, and a query or infrastructure failure is not about
// any one table, so neither is renamed here. No tables is a nil error. db may
// be a *DB pool or a *Tx.
func ProbeTables(ctx context.Context, db Querier, tables ...string) error {
	for _, table := range tables {
		if err := ProbeTable(ctx, db, table); err != nil {
			return err
		}
	}
	return nil
}

// Writable reports whether the role can change or remove the table's rows
// despite the append-only contract: it is a superuser or an owner (the trigger
// function exempts both by design), or it holds UPDATE, DELETE (unless
// AllowDelete), TRUNCATE or TRIGGER without the verified trigger pair. TRIGGER
// counts because its holder can change what happens to inserted rows.
func (p TablePrivilege) Writable() bool {
	return p.Owner || p.Superuser || (p.writePrivileged() && !p.ImmutableTrigger)
}

// CannotAppend reports whether the role cannot insert a row: it lacks INSERT or
// USAGE on the seq sequence.
func (p TablePrivilege) CannotAppend() bool {
	return !p.Insert || !p.SequenceUsage
}

// ProtectedBy names what keeps the role from modifying the table's rows:
// "privilege" when it holds no modifying privilege, "trigger" when the verified
// trigger pair refuses the privileges it holds, and for a Writable table
// "waiver" when acknowledged (Config.AppendOnlyWritableAcknowledged) or "none".
// A table that does not exist is "none".
func (p TablePrivilege) ProtectedBy(acknowledged bool) string {
	switch {
	case !p.Exists:
		return protectedByNone
	case p.Writable() && acknowledged:
		return protectedByWaiver
	case p.Writable():
		return protectedByNone
	case !p.writePrivileged():
		return protectedByPrivilege
	default:
		return protectedByTrigger
	}
}

// writePrivileged reports whether the role holds a privilege that modifies
// rows or the table's triggers.
func (p TablePrivilege) writePrivileged() bool {
	return p.Update || (p.Delete && !p.AllowDelete) || p.Truncate || p.Trigger
}

// writableReason names why Writable holds: owner=<kind>, superuser, or the
// modifying privileges with the trigger pair's state.
func (p TablePrivilege) writableReason() string {
	var reasons []string
	if p.Owner {
		reasons = append(reasons, "owner="+p.OwnerKind)
	}
	if p.Superuser {
		reasons = append(reasons, "superuser")
	}
	if len(reasons) > 0 {
		return strings.Join(reasons, " ")
	}
	var privileges []string
	for _, priv := range []struct {
		held bool
		name string
	}{
		{p.Update, "update"},
		{p.Delete && !p.AllowDelete, "delete"},
		{p.Truncate, "truncate"},
		{p.Trigger, "trigger"},
	} {
		if priv.held {
			privileges = append(privileges, priv.name)
		}
	}
	state := "trigger=" + p.TriggerState
	if strings.HasPrefix(p.TriggerState, triggerEnabledPrefix) {
		state = p.TriggerState
	}
	return "privileges=" + strings.Join(privileges, ",") + " " + state
}

// unwritableReason names why CannotAppend holds.
func (p TablePrivilege) unwritableReason() string {
	if !p.Exists {
		return "table=absent"
	}
	var reasons []string
	if !p.Insert {
		reasons = append(reasons, "insert=false")
	}
	if !p.SequenceUsage {
		reasons = append(reasons, "sequence_usage=false sequence="+p.sequence)
	}
	return strings.Join(reasons, " ")
}

// reason names every finding, for the development line.
func (p TablePrivilege) reason() string {
	var reasons []string
	if p.Writable() {
		reasons = append(reasons, p.writableReason())
	}
	if p.CannotAppend() {
		reasons = append(reasons, p.unwritableReason())
	}
	return strings.Join(reasons, " ")
}

// fix is the remedy a refusal names, with the inspected role and a <migrator>
// placeholder for the role that runs migrations.
func (p TablePrivilege) fix(role string) string {
	if !p.Exists {
		return "apply the store's migrations before boot"
	}
	revoke := "UPDATE, DELETE, TRUNCATE, TRIGGER"
	if p.AllowDelete {
		revoke = "UPDATE, TRUNCATE, TRIGGER"
	}
	steps := []string{appendOnlyFixLead, fmt.Sprintf("REVOKE %s ON %s FROM %s", revoke, p.Table, role)}
	if !p.Insert {
		steps = append(steps, fmt.Sprintf("GRANT INSERT ON %s TO %s", p.Table, role))
	}
	if p.sequence != "" {
		steps = append(steps, fmt.Sprintf("GRANT USAGE ON SEQUENCE %s TO %s", p.sequence, role))
	}
	steps = append(steps,
		fmt.Sprintf("ALTER TABLE %s OWNER TO <migrator>", p.Table),
		fmt.Sprintf("ALTER SCHEMA %s OWNER TO <migrator>", p.schema),
		fmt.Sprintf("ALTER TABLE %s ENABLE ALWAYS TRIGGER %s", p.Table, p.relation+immutableTriggerSuffix),
		fmt.Sprintf("ALTER TABLE %s ENABLE ALWAYS TRIGGER %s", p.Table, p.relation+noTruncateTriggerSuffix),
	)
	return strings.Join(steps, "; ")
}

// InspectTablePrivileges reports, for role, the append-only evidence of every
// table: ownership, superuser, INSERT/UPDATE/DELETE/TRUNCATE/TRIGGER, USAGE on
// the seq sequence and the verified trigger pair. An empty role inspects
// current_user; a named role lets a migrator preflight the runtime role from
// its own connection, deciding exactly as that role's DB.ProbeAppendOnly will.
// An unknown role wraps sdk.ErrNotFound; a missing table is Exists false, not
// an error. Each table is resolved once with to_regclass and inspected by oid;
// every name is bound as a parameter. q may be a *DB pool or a *Tx.
func InspectTablePrivileges(ctx context.Context, q Querier, role string, tables ...AppendOnlyTable) (Report, error) {
	return inspectTablePrivileges(ctx, q, role, false, tables)
}

// ProbeAppendOnly enforces the append-only contract for the DB's own role, in
// its own read-only transaction under SET LOCAL search_path TO pg_catalog: in
// production a store refuses to boot when its runtime role is superuser, owns
// an audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified
// triggers, or cannot insert into it.
//
// The zero RuntimeMode returns nil without a query. Development never refuses
// and logs one combined append_only=unenforced line naming every table
// production would refuse. Production returns errors.Join of one error per
// finding — ErrAppendOnlyTableWritable or ErrAppendOnlyTableUnwritable, naming
// the table, the reason and the fix — except that
// Config.AppendOnlyWritableAcknowledged turns each ErrAppendOnlyTableWritable
// into a posture_waiver WARN.
func (d *DB) ProbeAppendOnly(ctx context.Context, tables ...AppendOnlyTable) error {
	if d.mode == "" || len(tables) == 0 {
		return nil
	}
	tx, err := d.BeginRead(ctx)
	if err != nil {
		return fmt.Errorf("postgres: append-only probe: %w", err)
	}
	defer tx.Rollback()
	report, err := inspectTablePrivileges(ctx, tx, "", true, tables)
	if err != nil {
		return fmt.Errorf("postgres: append-only probe: %w", err)
	}
	return appendOnlyPosture(report, d.mode, d.appendOnlyWritableAcknowledged, d.logger)
}

// inspectTablePrivileges is InspectTablePrivileges; pinCatalog sets the
// transaction's search_path to pg_catalog once the table names are resolved
// on the caller's search_path, and requires q to be a transaction.
func inspectTablePrivileges(ctx context.Context, q Querier, role string, pinCatalog bool, tables []AppendOnlyTable) (Report, error) {
	var (
		report    Report
		roleOID   uint32
		superuser bool
	)
	if err := q.QueryRow(ctx, inspectRoleSQL, role).Scan(&roleOID, &report.Role, &superuser); err != nil {
		if errors.Is(err, jackpgx.ErrNoRows) {
			return Report{}, fmt.Errorf("pgxdb: role %q does not exist: %w", role, sdk.ErrNotFound)
		}
		return Report{}, fmt.Errorf("pgxdb: inspect role %q: %w", role, MapError(err))
	}
	oids := make([]*uint32, len(tables))
	for i, table := range tables {
		if err := q.QueryRow(ctx, resolveTableSQL, table.Name).Scan(&oids[i]); err != nil {
			return Report{}, fmt.Errorf("pgxdb: resolve %s: %w", table.Name, MapError(err))
		}
	}
	if pinCatalog {
		if _, err := q.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
			return Report{}, fmt.Errorf("pgxdb: set search_path: %w", err)
		}
	}
	report.Tables = make([]TablePrivilege, len(tables))
	for i, table := range tables {
		p := TablePrivilege{Table: table.Name, AllowDelete: table.AllowDelete, Superuser: superuser}
		if oids[i] != nil {
			if err := inspectTable(ctx, q, *oids[i], roleOID, table.Canonical, &p); err != nil {
				return Report{}, fmt.Errorf("pgxdb: inspect %s: %w", table.Name, err)
			}
		}
		report.Tables[i] = p
	}
	return report, nil
}

// inspectTable fills p from the catalog for the table oid and the role.
func inspectTable(ctx context.Context, q Querier, oid, role uint32, canonical []TriggerBody, p *TablePrivilege) error {
	var (
		tableOwner, schemaOwner bool
		sequence                *string
		sequenceUsage           *bool
	)
	if err := q.QueryRow(ctx, inspectTableSQL, oid, role).Scan(
		&p.schema, &p.relation, &tableOwner, &schemaOwner,
		&p.Insert, &p.Update, &p.Delete, &p.Truncate, &p.Trigger,
		&sequence, &sequenceUsage,
	); err != nil {
		return MapError(err)
	}
	p.Exists = true
	p.SequenceUsage = true
	if sequence != nil {
		p.sequence = *sequence
		p.SequenceUsage = sequenceUsage != nil && *sequenceUsage
	}

	immutable, err := inspectTrigger(ctx, q, oid, role, p.relation+immutableTriggerSuffix)
	if err != nil {
		return err
	}
	noTruncate, err := inspectTrigger(ctx, q, oid, role, p.relation+noTruncateTriggerSuffix)
	if err != nil {
		return err
	}
	p.TriggerState = immutable.state(true, "DELETE OR UPDATE", canonical)
	if p.TriggerState == triggerVerified {
		p.TriggerState = noTruncate.state(false, "TRUNCATE", canonical)
	}
	p.ImmutableTrigger = p.TriggerState == triggerVerified

	switch {
	case tableOwner:
		p.Owner, p.OwnerKind = true, ownerTable
	case schemaOwner:
		p.Owner, p.OwnerKind = true, ownerSchema
	case immutable.functionOwner || noTruncate.functionOwner:
		p.Owner, p.OwnerKind = true, ownerFunction
	}
	return nil
}

// inspectTrigger reads the named trigger on the table oid; an absent trigger
// is a zero triggerEvidence, not an error.
func inspectTrigger(ctx context.Context, q Querier, oid, role uint32, name string) (triggerEvidence, error) {
	var ev triggerEvidence
	err := q.QueryRow(ctx, inspectTriggerSQL, oid, role, name).Scan(
		&ev.internal, &ev.enabled, &ev.rowLevel, &ev.noWhen, &ev.noColumns,
		&ev.definition, &ev.quotedName, &ev.relation, &ev.function,
		&ev.proname, &ev.prosrc, &ev.secdef, &ev.config, &ev.language,
		&ev.sameNamespace, &ev.functionOwner,
	)
	switch {
	case errors.Is(err, jackpgx.ErrNoRows):
		return triggerEvidence{}, nil
	case err != nil:
		return triggerEvidence{}, MapError(err)
	}
	ev.present = true
	return ev, nil
}

// state verifies one trigger of the pair against its expected level and
// events and the canonical functions, returning triggerVerified or the first
// failing reason.
func (ev triggerEvidence) state(rowLevel bool, events string, canonical []TriggerBody) string {
	switch {
	case !ev.present:
		return triggerAbsent
	case ev.internal:
		return triggerInternal
	case ev.enabled != "A":
		return triggerEnabledPrefix + ev.enabled
	case ev.rowLevel != rowLevel:
		return triggerLevel
	case !ev.noWhen:
		return triggerWhenClause
	case !ev.noColumns:
		return triggerColumnList
	case !ev.sameNamespace:
		return triggerNamespace
	}
	level := "STATEMENT"
	if rowLevel {
		level = "ROW"
	}
	want := "CREATE TRIGGER " + ev.quotedName + " BEFORE " + events + " ON " + ev.relation +
		" FOR EACH " + level + " EXECUTE FUNCTION " + ev.function + "()"
	named, body := false, false
	for _, c := range canonical {
		if c.Function == ev.proname {
			named = true
			body = body || c.Body == ev.prosrc
		}
	}
	switch {
	case !named || ev.definition != want:
		return triggerDiffers
	case ev.language != "plpgsql":
		return triggerLanguage
	case !body:
		return triggerBodyDiffers
	case ev.secdef:
		return triggerSecurityDefiner
	case !slices.Equal(ev.config, []string{canonicalTriggerConfig}):
		return triggerProconfig
	}
	return triggerVerified
}

// appendOnlyPosture applies mode to report. The zero mode returns nil and
// logs nothing; development logs one line naming every table production would
// refuse; production joins one refusal per finding, logging a posture_waiver
// WARN instead of each ErrAppendOnlyTableWritable when acknowledged.
func appendOnlyPosture(report Report, mode environment.Mode, acknowledged bool, logger *slog.Logger) error {
	if mode == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	production := mode.IsProduction()
	var (
		refusals   []error
		unenforced []string
		reasons    []string
	)
	for _, p := range report.Tables {
		writable, unwritable := p.Writable(), p.CannotAppend()
		if !writable && !unwritable {
			continue
		}
		if !production {
			unenforced = append(unenforced, p.Table)
			reasons = append(reasons, p.Table+": "+p.reason())
			continue
		}
		switch {
		case writable && acknowledged:
			exempt := p.Owner || p.Superuser
			msg := "postgres: runtime role owns " + p.Table + ": immutability triggers do not apply"
			if !exempt {
				msg = "postgres: runtime role can modify " + p.Table + ": immutability triggers are not verified"
			}
			logger.Warn(msg,
				slog.String("posture_waiver", ackAppendOnlyWritable), slog.String("table", p.Table),
				slog.String("reason", p.writableReason()), slog.Bool("trigger_exempt", exempt))
		case writable:
			refusals = append(refusals, fmt.Errorf("%w: %s: %s; %s; or set Config.%s",
				ErrAppendOnlyTableWritable, p.Table, p.writableReason(), p.fix(report.Role), ackAppendOnlyWritable))
		}
		if unwritable {
			refusals = append(refusals, fmt.Errorf("%w: %s: %s; %s",
				ErrAppendOnlyTableUnwritable, p.Table, p.unwritableReason(), p.fix(report.Role)))
		}
	}
	if len(unenforced) > 0 {
		logger.Warn("postgres: append-only tables are not enforced for this runtime role; production refuses it",
			slog.String("append_only", "unenforced"), slog.Any("tables", unenforced),
			slog.String("reasons", strings.Join(reasons, "; ")))
	}
	return errors.Join(refusals...)
}
