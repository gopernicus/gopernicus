// Id-collation tests pin the plan D3 inventory: every jobs id column carries an
// explicit COLLATE "C" in the canonical migrations, so id comparison and
// ordering are byte-wise (the memory store's Go string order) whatever the
// cluster's default collation is. Two inventories stand side by side so adding
// or dropping a column is a visible test edit: generatedIDColumns (every
// Go-minted id, with its database backstop) and referencingIDColumns (the
// columns that hold one of those ids).
//
//   - TestIDCollation_SQL (hermetic): every inventoried column's canonical DDL
//     carries COLLATE "C", and every generated id column carries its backstop.
//   - TestIDCollation_Catalog (live, POSTGRES_TEST_DSN): the applied schema
//     reports collation_name = 'C' for every inventoried column.
package pgx

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

const (
	// collationSQL reads one column's catalog collation on the search_path.
	collationSQL = `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2 AND table_schema = current_schema()`

	// collationSchemaSQL scopes that read to the POSTGRES_TEST_SCHEMA leg's
	// schema, so a same-named table in the default schema cannot answer for it.
	collationSchemaSQL = `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2 AND table_schema = $3`

	// recordBackstop is the record-class column default (PostgreSQL 18). The
	// jobs services mint every id and the store always supplies it, so this
	// serves only a raw SQL insert.
	recordBackstop = "DEFAULT uuidv7()::text"
)

// collatedColumn names one table column the canonical schema must pin to
// COLLATE "C" and why.
type collatedColumn struct {
	Table  string
	Column string
	Why    string
}

// generatedIDColumns inventories every id the jobs services mint. Occurrence
// job ids are deterministic (schedule.OccurrenceID) and carry no backstop, so
// they are listed under referencingIDColumns.
var generatedIDColumns = []collatedColumn{
	{"job_queue", "job_id", "record id"},
	{"fenced_job_queue", "job_id", "record id"},
	{"job_schedules", "schedule_id", "record id"},
}

// referencingIDColumns inventories the columns that hold a jobs id (plan R5):
// the planner uses an index only under the join key's collation, and ORDER BY
// over them must match the memory store's byte order.
var referencingIDColumns = []collatedColumn{
	{"job_schedules", "last_job_id", "references job_queue.job_id"},
	{"job_schedule_occurrences", "job_id", "deterministic occurrence id; becomes job_queue.job_id"},
	{"job_schedule_occurrences", "schedule_id", "references job_schedules.schedule_id"},
}

// canonicalSQL concatenates every embedded migration in filename order.
func canonicalSQL(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var all strings.Builder
	for _, name := range names {
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		all.Write(b)
		all.WriteByte('\n')
	}
	return all.String()
}

// tableCreateBody returns the parenthesized column list of a table's CREATE
// TABLE statement within the concatenated canonical SQL.
func tableCreateBody(t *testing.T, sql, table string) string {
	t.Helper()
	head := "CREATE TABLE IF NOT EXISTS " + table + " ("
	i := strings.Index(sql, head)
	if i < 0 {
		t.Fatalf("no CREATE TABLE for %q", table)
	}
	rest := sql[i+len(head):]
	end := strings.Index(rest, "\n);")
	if end < 0 {
		t.Fatalf("unterminated CREATE TABLE for %q", table)
	}
	return rest[:end]
}

// columnLine returns the DDL line defining column within a CREATE TABLE body.
func columnLine(body, column string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == column {
			return line, true
		}
	}
	return "", false
}

// TestIDCollation_SQL asserts every inventoried column's DDL line carries
// COLLATE "C", and every generated id its backstop (hermetic — no database).
func TestIDCollation_SQL(t *testing.T) {
	sql := canonicalSQL(t)
	check := func(c collatedColumn, backstop string) {
		line, found := columnLine(tableCreateBody(t, sql, c.Table), c.Column)
		if !found {
			t.Errorf("no column definition found for %s.%s", c.Table, c.Column)
			return
		}
		if !strings.Contains(line, `COLLATE "C"`) {
			t.Errorf("%s.%s (%s) DDL lacks COLLATE \"C\": %q", c.Table, c.Column, c.Why, strings.TrimSpace(line))
		}
		if backstop != "" && !strings.Contains(line, backstop) {
			t.Errorf("%s.%s (%s) DDL lacks its backstop %q: %q", c.Table, c.Column, c.Why, backstop, strings.TrimSpace(line))
		}
		if backstop == "" && strings.Contains(line, "DEFAULT") {
			t.Errorf("%s.%s (%s) must not carry a default: %q", c.Table, c.Column, c.Why, strings.TrimSpace(line))
		}
	}
	for _, c := range generatedIDColumns {
		check(c, recordBackstop)
	}
	for _, c := range referencingIDColumns {
		check(c, "")
	}
}

// TestIDCollation_Catalog applies the canonical schema and asserts the catalog
// reports collation_name = 'C' for every inventoried column.
func TestIDCollation_Catalog(t *testing.T) {
	db, _ := openAndMigrate(t, requireDSN(t))
	ctx := context.Background()
	schema := testSchema(t)

	for _, c := range append(append([]collatedColumn(nil), generatedIDColumns...), referencingIDColumns...) {
		var collation *string
		q, args := collationSQL, []any{c.Table, c.Column}
		if !schema.IsZero() {
			q, args = collationSchemaSQL, []any{c.Table, c.Column, schema.String()}
		}
		if err := db.QueryRow(ctx, q, args...).Scan(&collation); err != nil {
			t.Fatalf("catalog lookup %s.%s: %v", c.Table, c.Column, err)
		}
		if collation == nil || *collation != "C" {
			got := "<default>"
			if collation != nil {
				got = *collation
			}
			t.Errorf("%s.%s collation = %s, want C (%s)", c.Table, c.Column, got, c.Why)
		}
	}
}
