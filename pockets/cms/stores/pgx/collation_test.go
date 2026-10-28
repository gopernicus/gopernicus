// Collation tests pin the cms id inventory (plan D3 / R5): every
// pocket-generated id column and every column that holds one of those ids
// carries an explicit COLLATE "C" in the canonical migrations, so byte-wise id
// ordering travels with the schema instead of the cluster's default collation.
//
// Two inventories stand side by side so adding or dropping a column is a
// visible test edit: generatedIDColumns (every id the pocket generates, with
// its UUIDv7 backstop) and referencingIDColumns. Three checks stand on them:
//
//   - TestIDCollation_SQL (hermetic): every inventoried column's canonical DDL
//     carries COLLATE "C", and every generated id column carries the backstop.
//   - TestIDCollation_Catalog (live, POSTGRES_TEST_DSN): the applied schema
//     reports collation_name = 'C' for every inventoried column.
//   - TestCollationControlsOrdering_NonC (live, POSTGRES_NON_C_TEST_DSN): on a
//     NON-C database the column collation, not the cluster default, controls
//     the entry keyset tiebreak and the term-id order, through the store.
package pgx

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
)

// recordBackstop is the record class's database default: a UUIDv7
// (PostgreSQL 18). Go mints every cms id; this serves raw SQL inserts and a
// host wired with sdk.DatabaseID.
const recordBackstop = "DEFAULT uuidv7()::text"

// nonCSchemaName isolates the non-C ordering proof from the conformance tables.
const nonCSchemaName = "cms_collation_non_c"

// collatedColumn names one table column the canonical schema pins to
// COLLATE "C" and why.
type collatedColumn struct {
	Table  string
	Column string
	Why    string
}

// generatedIDColumns inventories every id the cms pocket generates. All are
// record class (UUIDv7 by default).
var generatedIDColumns = []collatedColumn{
	{"terms", "id", "record id"},
	{"menus", "id", "record id"},
	{"menu_items", "id", "record id"},
	{"assets", "id", "record id"},
	{"inquiries", "id", "record id"},
	{"entries", "id", "record id; keyset PK tiebreak"},
}

// referencingIDColumns inventories the columns that hold a cms-generated id.
// They share the "C" collation of the id they reference: an index serves a
// join key only under the key's collation, and ORDER BY term_id / parent_id
// must sort in the reference store's byte order.
var referencingIDColumns = []collatedColumn{
	{"terms", "parent_id", "references terms.id"},
	{"menu_items", "menu_id", "references menus.id"},
	{"menu_items", "parent_id", "references menu_items.id; ORDER BY parent_id"},
	{"entries", "parent_id", "references entries.id"},
	{"entry_fields", "entry_id", "references entries.id (FK)"},
	{"entry_terms", "entry_id", "references entries.id (FK)"},
	{"entry_terms", "term_id", "references terms.id; ORDER BY term_id"},
}

// collatedInventory is every column the two inventories pin to "C".
func collatedInventory() []collatedColumn {
	return append(append([]collatedColumn(nil), generatedIDColumns...), referencingIDColumns...)
}

// canonicalSQL concatenates every embedded migration in name order.
func canonicalSQL(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var all strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
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
	for _, c := range collatedInventory() {
		line, found := columnLine(tableCreateBody(t, sql, c.Table), c.Column)
		if !found {
			t.Errorf("no column definition found for %s.%s", c.Table, c.Column)
			continue
		}
		if !strings.Contains(line, `COLLATE "C"`) {
			t.Errorf("%s.%s is inventoried (%s) but its DDL lacks COLLATE \"C\": %q", c.Table, c.Column, c.Why, strings.TrimSpace(line))
		}
	}
	for _, g := range generatedIDColumns {
		line, _ := columnLine(tableCreateBody(t, sql, g.Table), g.Column)
		if !strings.Contains(line, recordBackstop) {
			t.Errorf("%s.%s DDL lacks its backstop %q: %q", g.Table, g.Column, recordBackstop, strings.TrimSpace(line))
		}
	}
}

// TestIDCollation_Catalog applies the canonical schema and asserts the catalog
// reports collation_name = 'C' for every inventoried column, scoped to the
// leg's schema (current_schema() on the default leg).
func TestIDCollation_Catalog(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	schema := testSchema(t)
	db := openDB(t, dsn)
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	q := `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2 AND table_schema = current_schema()`
	args := func(c collatedColumn) []any { return []any{c.Table, c.Column} }
	if !schema.IsZero() {
		q = `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2 AND table_schema = $3`
		args = func(c collatedColumn) []any { return []any{c.Table, c.Column, schema.String()} }
	}
	for _, c := range collatedInventory() {
		var collation *string
		if err := db.QueryRow(ctx, q, args(c)...).Scan(&collation); err != nil {
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

// TestCollationControlsOrdering_NonC proves on a NON-C database that the column
// collation controls ordering through the store's real query paths: the entry
// list's created_at DESC, id DESC tiebreak (entries.id) and an entry's term ids
// (ORDER BY entry_terms.term_id). The ids are mixed case so byte order ('B' <
// 'a') is the reverse of the locale order ('a' < 'B').
func TestCollationControlsOrdering_NonC(t *testing.T) {
	dsn := os.Getenv("POSTGRES_NON_C_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_NON_C_TEST_DSN not set — non-C ordering proof NOT verified")
	}
	ctx := context.Background()
	db := openDB(t, dsn)
	assertNonCDatabase(t, db)

	schema, err := pgxdb.NewSchema(nonCSchemaName)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	dropSchema(t, dsn, schema)
	t.Cleanup(func() { dropSchema(t, dsn, schema) })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewEntryStore(db, WithSchema(schema))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"BBB-collate-proof", "aaa-collate-proof"} {
		e := content.Entry{ID: id, Type: "article", Slug: id, Title: id, Status: content.StatusDraft, Template: "default", CreatedAt: base, UpdatedAt: base}
		if _, err := store.Create(ctx, e); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	page, err := store.List(ctx, content.EntryQuery{Type: "article"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(page.Items))
	for _, e := range page.Items {
		got = append(got, e.ID)
	}
	// id DESC under COLLATE "C" is byte-descending: "aaa…" (0x61) before
	// "BBB…" (0x42). A non-C cluster default would invert this.
	if want := []string{"aaa-collate-proof", "BBB-collate-proof"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entry list order = %v, want %v (COLLATE \"C\" byte order must win over the cluster default)", got, want)
	}

	if err := store.SetTerms(ctx, "aaa-collate-proof", []string{"aaa-term", "BBB-term"}); err != nil {
		t.Fatalf("SetTerms: %v", err)
	}
	e, err := store.Get(ctx, "aaa-collate-proof")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// term_id ASC under COLLATE "C": "BBB…" (0x42) before "aaa…" (0x61).
	if want := []string{"BBB-term", "aaa-term"}; strings.Join(e.TermIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("term id order = %v, want %v (COLLATE \"C\" byte order must win over the cluster default)", e.TermIDs, want)
	}
}

// assertNonCDatabase fails loudly unless the connected database's default
// collation is a genuine non-C-family locale: against a C-family cluster byte
// and locale order coincide and the ordering proof would pass vacuously.
func assertNonCDatabase(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	var datcollate string
	if err := db.QueryRow(context.Background(),
		`SELECT datcollate FROM pg_database WHERE datname = current_database()`).Scan(&datcollate); err != nil {
		t.Fatalf("read datcollate: %v", err)
	}
	upper := strings.ToUpper(datcollate)
	if strings.HasPrefix(upper, "C") || upper == "POSIX" {
		t.Fatalf("POSTGRES_NON_C_TEST_DSN points at a C-family database (datcollate=%q); the ordering proof requires a non-C locale (e.g. en_US.utf8)", datcollate)
	}
}
