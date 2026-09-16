package pgx

import (
	"context"
	"fmt"
	"maps"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// These are PostgreSQL's catalog forms, including the expression grouping.
// Checking constraint names alone would accept a same-named CHECK (true).
const canonicalScopeConstraint = "CHECK ((((scope_kind = 1) AND (resource_type = ''::text) AND (resource_id = ''::text)) OR ((scope_kind = 2) AND (resource_type <> ''::text) AND (resource_id <> ''::text))))"
const canonicalRefsConstraint = "CHECK (((relation <> ''::text) AND (subject_type <> ''::text) AND (subject_id <> ''::text)))"

func probeCanonicalSchema(ctx context.Context, db *pgxdb.DB, schema pgxdb.Schema, audit bool) error {
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tables := []string{"iam_tuples"}
	if audit {
		tables = append(tables, "iam_audit")
	}
	resolved := ""
	for _, table := range tables {
		var namespace, kind, persistence string
		var rls, inherited bool
		err := tx.QueryRow(ctx, `SELECT n.nspname, c.relkind::text, c.relpersistence::text,
c.relrowsecurity OR c.relforcerowsecurity,
EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhparent=c.oid OR i.inhrelid=c.oid)
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE c.oid=pg_catalog.to_regclass($1)`, schema.Table(table)).Scan(&namespace, &kind, &persistence, &rls, &inherited)
		if err != nil {
			return err
		}
		if kind != "r" || persistence != "p" || rls || inherited || resolved != "" && resolved != namespace {
			return incompatibleSchema(table, "requires ordinary durable tables in one schema without row security or inheritance")
		}
		resolved = namespace
	}
	schema, err = pgxdb.NewSchema(resolved)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
		return err
	}
	columns := map[string]string{"scope_kind": "smallint", "resource_type": "text", "resource_id": "text", "relation": "text", "subject_type": "text", "subject_id": "text", "subject_relation": "text"}
	constraints := map[string]bool{
		"PRIMARY KEY (" + tupleColumns + ")": true,
		canonicalScopeConstraint:             true,
		canonicalRefsConstraint:              true,
	}
	if err := probeCanonicalTable(ctx, tx, schema.Table("iam_tuples"), columns, constraints); err != nil {
		return err
	}
	if audit {
		columns = maps.Clone(columns)
		for _, name := range []string{"id", "event_id", "actor_type", "actor_id", "system_source", "reason", "action", "encoding"} {
			columns[name] = "text"
		}
		columns["occurred_at"] = "timestamp with time zone"
		constraints = map[string]bool{
			"PRIMARY KEY (id)":       true,
			canonicalScopeConstraint: true,
			canonicalRefsConstraint:  true,
			"CHECK ((action = ANY (ARRAY['added'::text, 'removed'::text])))": true,
			"CHECK ((encoding = 'tuple/v2'::text))":                          true,
			"CHECK ((((actor_type <> ''::text) AND (actor_id <> ''::text) AND (system_source = ''::text)) OR ((actor_type = ''::text) AND (actor_id = ''::text) AND (system_source <> ''::text))))": true,
		}
		if err := probeCanonicalTable(ctx, tx, schema.Table("iam_audit"), columns, constraints); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func probeCanonicalTable(ctx context.Context, q pgxdb.Querier, table string, columns map[string]string, constraints map[string]bool) error {
	want := maps.Clone(columns)
	rows, err := q.Query(ctx, `SELECT a.attname, a.atttypid::regtype::text, a.attnotnull,
COALESCE(c.collname, ''), a.attgenerated::text
FROM pg_catalog.pg_attribute a LEFT JOIN pg_catalog.pg_collation c ON c.oid=a.attcollation
WHERE a.attrelid=$1::regclass AND a.attnum>0 AND NOT a.attisdropped`, table)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, typ, collation, generated string
		var nonnull bool
		if err := rows.Scan(&name, &typ, &nonnull, &collation, &generated); err != nil {
			rows.Close()
			return err
		}
		if want[name] != typ || !nonnull || generated != "" || typ == "text" && collation != "C" {
			rows.Close()
			return incompatibleSchema(table, "column "+name+" has incompatible type, nullability, generation or collation")
		}
		delete(want, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(want) != 0 {
		return incompatibleSchema(table, "required columns missing")
	}
	rows, err = q.Query(ctx, `SELECT pg_catalog.pg_get_constraintdef(oid), convalidated, condeferrable
FROM pg_catalog.pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u','c','x','f')`, table)
	if err != nil {
		return err
	}
	wantChecks := maps.Clone(constraints)
	for rows.Next() {
		var definition string
		var validated, deferred bool
		if err := rows.Scan(&definition, &validated, &deferred); err != nil {
			rows.Close()
			return err
		}
		if !wantChecks[definition] || !validated || deferred {
			rows.Close()
			return incompatibleSchema(table, "incompatible constraint "+definition)
		}
		delete(wantChecks, definition)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(wantChecks) != 0 {
		return incompatibleSchema(table, "required primary key or shape constraints missing")
	}
	// An extra unique index can silently reinstate the retired one-label rule.
	var restrictiveIndex bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_index
WHERE indrelid=$1::regclass AND (indisunique AND NOT indisprimary OR indisprimary AND (NOT indisvalid OR NOT indisready)))`, table).Scan(&restrictiveIndex); err != nil {
		return err
	}
	if restrictiveIndex {
		return incompatibleSchema(table, "additional uniqueness or unusable primary key")
	}
	return nil
}

func incompatibleSchema(table, detail string) error {
	return fmt.Errorf("authorization pgx store: %s incompatible canonical schema (%s) — apply the %q migration source before boot: %w", table, detail, migrationSource, sdk.ErrInvalidInput)
}
