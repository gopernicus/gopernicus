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

	// ErrDefinerFunctionUnsafe refuses a definer function listed on an
	// append-only table that PUBLIC can execute, whose proconfig is not exactly
	// search_path=pg_catalog, <the table's schema>, pg_temp, or whose signature
	// resolves outside the table's schema. No acknowledgement admits it.
	ErrDefinerFunctionUnsafe = errors.New("postgres: definer function on an append-only table is unsafe")
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

	resolveFunctionSQL = `SELECT pg_catalog.to_regprocedure($1::pg_catalog.text)::pg_catalog.oid`

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

	// inspectFunctionSQL judges one resolved definer function against its
	// table. The pinned proconfig is computed here with quote_ident, the way
	// PostgreSQL flattens a search_path naming a schema that needs quotes, and
	// compared as a one-element array.
	inspectFunctionSQL = `SELECT p.prosecdef,
	p.proconfig IS NOT DISTINCT FROM
		ARRAY['search_path=pg_catalog, ' || pg_catalog.quote_ident(n.nspname) || ', pg_temp']::pg_catalog.text[],
	p.pronamespace = c.relnamespace,
	pg_catalog.pg_has_role(p.proowner, c.relowner, 'MEMBER'),
	o.rolsuper,
	pg_catalog.has_function_privilege('public'::pg_catalog.name, p.oid, 'EXECUTE'),
	pg_catalog.has_schema_privilege($3::pg_catalog.oid, c.relnamespace, 'CREATE'),
	pg_catalog.has_function_privilege($3::pg_catalog.oid, p.oid, 'EXECUTE'),
	pg_catalog.format('%I.%I(%s)', fn.nspname, p.proname, pg_catalog.oidvectortypes(p.proargtypes)),
	fn.nspname::pg_catalog.text,
	pg_catalog.quote_ident(n.nspname),
	pg_catalog.quote_ident(pg_catalog.pg_get_userbyid(c.relowner))
FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace
JOIN pg_catalog.pg_roles o ON o.oid = p.proowner
JOIN pg_catalog.pg_class c ON c.oid = $2::pg_catalog.oid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE p.oid = $1::pg_catalog.oid`
)

// AppendOnlyTable is one append-only table a store owns, as the posture probe
// judges it. Name is bound as a parameter: bare (resolved on the connection's
// search_path) or schema-qualified. AllowDelete admits a DELETE grant, for a
// table whose trigger function decides which rows may be deleted. Canonical
// lists the trigger functions the store ships; the trigger pair verifies only
// against them. Functions lists the SECURITY DEFINER functions the store ships
// for the table; a store that lists none is probed exactly as before.
type AppendOnlyTable struct {
	Name        string
	AllowDelete bool
	Canonical   []TriggerBody
	Functions   []DefinerFunction
}

// DefinerFunction is one SECURITY DEFINER function a store ships beside an
// append-only table: owned by the table's owner, it does for the runtime role
// what the role's own grants refuse. Signature is bound to to_regprocedure on
// the connection's search_path, bare or schema-qualified, with every argument
// type written pg_catalog-qualified:
// "security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4)".
type DefinerFunction struct {
	Signature string
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
// "security_definer", "proconfig", …). Functions is the evidence for each of
// the table's DefinerFunctions, keyed by Signature; UnsafeFunctions and
// InoperativeFunctions judge it, and nothing in it changes Writable,
// CannotAppend or ProtectedBy. A table that does not exist has Exists false
// and nothing set beyond Table, AllowDelete and Superuser.
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

	Functions map[string]FunctionPrivilege

	// schema, relation and sequence are the catalog's names for the table's
	// namespace, the bare table and its seq sequence ("" without one), for the
	// remedy a refusal prints.
	schema   string
	relation string
	sequence string
}

// FunctionPrivilege is the evidence for one DefinerFunction of an append-only
// table and one role.
//
// A signature that does not resolve has Exists false and nothing else set.
// SecurityDefiner is prosecdef. SearchPathPinned is a proconfig of exactly one
// element, search_path=pg_catalog, <the table's schema>, pg_temp, the schema
// written as quote_ident writes it. SameNamespace is the function living in
// the table's schema. OwnerIsTableOwner is the function's owner being a member
// of the table's owner; OwnerSuperuser is that owner being a superuser.
// PublicExecute is EXECUTE held by PUBLIC. PathSchemaCreate is CREATE on the
// table's schema held by the inspected role, PUBLIC's grant included; Execute
// is EXECUTE held by the inspected role.
type FunctionPrivilege struct {
	Exists            bool
	SecurityDefiner   bool
	SearchPathPinned  bool
	SameNamespace     bool
	OwnerIsTableOwner bool
	OwnerSuperuser    bool
	PublicExecute     bool
	PathSchemaCreate  bool
	Execute           bool

	// function is the resolved function, schema-qualified with its argument
	// types; namespace its schema's name; schema and tableOwner the table's
	// schema and owner as quote_ident writes them — for the remedy a finding
	// prints.
	function   string
	namespace  string
	schema     string
	tableOwner string
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

// UnsafeFunctions names, sorted by Signature, the table's existing definer
// functions any role could reach or redirect: PUBLIC holds EXECUTE, the
// search_path is not pinned to exactly pg_catalog, the table's schema and
// pg_temp, or the signature resolves outside the table's schema. Production
// refuses each with ErrDefinerFunctionUnsafe.
func (p TablePrivilege) UnsafeFunctions() []string {
	return p.functionsWhere(FunctionPrivilege.unsafe)
}

// InoperativeFunctions names, sorted by Signature, the table's existing
// definer functions that will not act as the table's owner as intended or
// act with more reach than intended: not SECURITY DEFINER, owned by a role
// that is not a member of the table's owner, owned by a superuser, or in a
// schema where the inspected role holds CREATE. Production logs a WARN for
// each.
func (p TablePrivilege) InoperativeFunctions() []string {
	return p.functionsWhere(FunctionPrivilege.inoperative)
}

// functionsWhere returns the sorted signatures whose evidence matches.
func (p TablePrivilege) functionsWhere(match func(FunctionPrivilege) bool) []string {
	var signatures []string
	for signature, f := range p.Functions {
		if match(f) {
			signatures = append(signatures, signature)
		}
	}
	slices.Sort(signatures)
	return signatures
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
	for _, signature := range p.UnsafeFunctions() {
		reasons = append(reasons, "function="+signature+" "+p.Functions[signature].unsafeReason())
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

// unsafe reports whether the function exists and PUBLIC can execute it, its
// search_path is not pinned, or it lives outside the table's schema.
func (f FunctionPrivilege) unsafe() bool {
	return f.Exists && (f.PublicExecute || !f.SearchPathPinned || !f.SameNamespace)
}

// inoperative reports whether the function exists and is not SECURITY
// DEFINER, is not owned by a member of the table's owner, is owned by a
// superuser, or sits in a schema where the inspected role holds CREATE.
func (f FunctionPrivilege) inoperative() bool {
	return f.Exists && (!f.SecurityDefiner || !f.OwnerIsTableOwner || f.OwnerSuperuser || f.PathSchemaCreate)
}

// unsafeReason names why unsafe holds.
func (f FunctionPrivilege) unsafeReason() string {
	var reasons []string
	if f.PublicExecute {
		reasons = append(reasons, "public_execute=true")
	}
	if !f.SearchPathPinned {
		reasons = append(reasons, "search_path_pinned=false")
	}
	if !f.SameNamespace {
		reasons = append(reasons, "same_namespace=false namespace="+f.namespace)
	}
	return strings.Join(reasons, " ")
}

// unsafeFix is the remedy an unsafe refusal names. A function outside the
// table's schema is not the store's function, so only the namespace remedy
// applies to it.
func (f FunctionPrivilege) unsafeFix(role string) string {
	if !f.SameNamespace {
		return "the signature resolves to " + f.function + ", outside schema " + f.schema +
			": drop that function or take its schema off the search_path"
	}
	var steps []string
	if f.PublicExecute {
		steps = append(steps,
			"REVOKE EXECUTE ON FUNCTION "+f.function+" FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION "+f.function+" TO "+role)
	}
	if !f.SearchPathPinned {
		steps = append(steps,
			"ALTER FUNCTION "+f.function+" RESET ALL",
			"ALTER FUNCTION "+f.function+" SET search_path = pg_catalog, "+f.schema+", pg_temp (proconfig must hold exactly this one setting)")
	}
	return strings.Join(steps, "; ")
}

// inoperativeReason names why inoperative holds.
func (f FunctionPrivilege) inoperativeReason() string {
	var reasons []string
	if !f.SecurityDefiner {
		reasons = append(reasons, "security_definer=false")
	}
	if !f.OwnerIsTableOwner {
		reasons = append(reasons, "owner_is_table_owner=false")
	}
	if f.OwnerSuperuser {
		reasons = append(reasons, "owner_superuser=true")
	}
	if f.PathSchemaCreate {
		reasons = append(reasons, "path_schema_create=true")
	}
	return strings.Join(reasons, " ")
}

// inoperativeFix is the remedy an inoperative WARN names, with a <migrator>
// placeholder for the role that runs migrations.
func (f FunctionPrivilege) inoperativeFix(table, role string) string {
	var steps []string
	if !f.SecurityDefiner {
		steps = append(steps, "ALTER FUNCTION "+f.function+" SECURITY DEFINER")
	}
	if !f.OwnerIsTableOwner {
		steps = append(steps, "ALTER FUNCTION "+f.function+" OWNER TO "+f.tableOwner)
	}
	if f.OwnerSuperuser {
		steps = append(steps,
			"run migrations as a NOSUPERUSER role",
			"ALTER TABLE "+table+" OWNER TO <migrator>",
			"ALTER FUNCTION "+f.function+" OWNER TO <migrator>")
	}
	if f.PathSchemaCreate {
		steps = append(steps, "REVOKE CREATE ON SCHEMA "+f.schema+" FROM PUBLIC, "+role)
	}
	return strings.Join(steps, "; ")
}

// InspectTablePrivileges reports, for role, the append-only evidence of every
// table: ownership, superuser, INSERT/UPDATE/DELETE/TRUNCATE/TRIGGER, USAGE on
// the seq sequence, the verified trigger pair and each listed definer
// function. An empty role inspects current_user; a named role lets a migrator
// preflight the runtime role from its own connection, deciding exactly as that
// role's DB.ProbeAppendOnly will. An unknown role wraps sdk.ErrNotFound; a
// missing table or function is Exists false, not an error. Each table is
// resolved once with to_regclass, and each function with to_regprocedure,
// then inspected by oid; every name is bound as a parameter. q may be a *DB
// pool or a *Tx.
func InspectTablePrivileges(ctx context.Context, q Querier, role string, tables ...AppendOnlyTable) (Report, error) {
	return inspectTablePrivileges(ctx, q, role, false, tables)
}

// ProbeAppendOnly enforces the append-only contract for the DB's own role, in
// its own read-only transaction under SET LOCAL search_path TO pg_catalog: in
// production a store refuses to boot when its runtime role is superuser, owns
// an audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified
// triggers, or cannot insert into it, or when a definer function the store
// lists is unsafe (TablePrivilege.UnsafeFunctions).
//
// The zero RuntimeMode returns nil without a query. Development never refuses
// and logs one combined append_only=unenforced line naming every table
// production would refuse. Production returns errors.Join of one error per
// finding — ErrAppendOnlyTableWritable, ErrAppendOnlyTableUnwritable or
// ErrDefinerFunctionUnsafe, naming the table, the reason and the fix — except
// that Config.AppendOnlyWritableAcknowledged turns each
// ErrAppendOnlyTableWritable into a posture_waiver WARN; it never admits
// ErrDefinerFunctionUnsafe. Each inoperative definer function
// (TablePrivilege.InoperativeFunctions) is a production WARN, never a refusal.
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
// transaction's search_path to pg_catalog once the table names and function
// signatures are resolved on the caller's search_path, and requires q to be a
// transaction.
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
	functionOIDs := make([][]*uint32, len(tables))
	for i, table := range tables {
		if err := q.QueryRow(ctx, resolveTableSQL, table.Name).Scan(&oids[i]); err != nil {
			return Report{}, fmt.Errorf("pgxdb: resolve %s: %w", table.Name, MapError(err))
		}
		functionOIDs[i] = make([]*uint32, len(table.Functions))
		for j, fn := range table.Functions {
			if err := q.QueryRow(ctx, resolveFunctionSQL, fn.Signature).Scan(&functionOIDs[i][j]); err != nil {
				return Report{}, fmt.Errorf("pgxdb: resolve %s: %w", fn.Signature, MapError(err))
			}
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
			if err := inspectFunctions(ctx, q, *oids[i], roleOID, table.Functions, functionOIDs[i], &p); err != nil {
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

// inspectFunctions fills p.Functions for the table oid and the role, one entry
// per listed function keyed by its Signature, from the oids resolved before
// the search_path was pinned; one that did not resolve is Exists false. A
// table that lists none keeps a nil map.
func inspectFunctions(ctx context.Context, q Querier, table, role uint32, functions []DefinerFunction, oids []*uint32, p *TablePrivilege) error {
	if len(functions) == 0 {
		return nil
	}
	p.Functions = make(map[string]FunctionPrivilege, len(functions))
	for i, fn := range functions {
		var f FunctionPrivilege
		if oids[i] != nil {
			err := q.QueryRow(ctx, inspectFunctionSQL, *oids[i], table, role).Scan(
				&f.SecurityDefiner, &f.SearchPathPinned, &f.SameNamespace,
				&f.OwnerIsTableOwner, &f.OwnerSuperuser, &f.PublicExecute,
				&f.PathSchemaCreate, &f.Execute,
				&f.function, &f.namespace, &f.schema, &f.tableOwner,
			)
			switch {
			case errors.Is(err, jackpgx.ErrNoRows):
				f = FunctionPrivilege{}
			case err != nil:
				return fmt.Errorf("%s: %w", fn.Signature, MapError(err))
			default:
				f.Exists = true
			}
		}
		p.Functions[fn.Signature] = f
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
// WARN instead of each ErrAppendOnlyTableWritable when acknowledged, and one
// definer_function=inoperative WARN per inoperative function.
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
		writable, unwritable, unsafe := p.Writable(), p.CannotAppend(), p.UnsafeFunctions()
		if !production {
			if writable || unwritable || len(unsafe) > 0 {
				unenforced = append(unenforced, p.Table)
				reasons = append(reasons, p.Table+": "+p.reason())
			}
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
		for _, signature := range unsafe {
			f := p.Functions[signature]
			refusals = append(refusals, fmt.Errorf("%w: %s: function=%s %s; %s",
				ErrDefinerFunctionUnsafe, p.Table, signature, f.unsafeReason(), f.unsafeFix(report.Role)))
		}
		for _, signature := range p.InoperativeFunctions() {
			f := p.Functions[signature]
			logger.Warn("postgres: definer function on "+p.Table+" is inoperative or over-privileged",
				slog.String("definer_function", "inoperative"), slog.String("table", p.Table),
				slog.String("function", signature), slog.String("reason", f.inoperativeReason()),
				slog.String("fix", f.inoperativeFix(p.Table, report.Role)))
		}
	}
	if len(unenforced) > 0 {
		logger.Warn("postgres: append-only tables are not enforced for this runtime role; production refuses it",
			slog.String("append_only", "unenforced"), slog.Any("tables", unenforced),
			slog.String("reasons", strings.Join(reasons, "; ")))
	}
	return errors.Join(refusals...)
}
