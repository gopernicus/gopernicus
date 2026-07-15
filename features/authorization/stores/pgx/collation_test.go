// Contractual-collation tests pin the AAH-5 / plan D5 inventory: the opaque TEXT
// columns whose byte-wise ordering is a store contract carry an explicit
// COLLATE "C" in the canonical migrations, so the guarantee travels with the
// schema instead of relying on the cluster's default collation.
//
// The inventory is a single table (contractualCollatedColumns) so adding or
// dropping a contractual column is a visible test edit. Three checks stand on it:
//
//   - TestContractualCollation_SQL (hermetic): every inventoried column's
//     canonical DDL carries COLLATE "C".
//   - TestContractualCollation_Catalog (live, POSTGRES_TEST_DSN): the applied
//     schema reports collation_name = 'C' for every inventoried column.
//   - TestCollationControlsOrdering_NonC (live, POSTGRES_NON_C_TEST_DSN): on a
//     NON-C database, the column collation — not the cluster default — controls
//     the ordering, proven through the store's real ListEffectiveByResource query
//     path (ordered by the derived grant_key over collated iam_roles columns).
package pgx

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// collatedColumn names one table column the canonical schema must pin to
// COLLATE "C" and why it is contractual (opaque ordering key, not display text).
type collatedColumn struct {
	Table  string
	Column string
	Why    string
}

// contractualCollatedColumns is the authorization inventory (AAH-5). relationship_id
// is the keyset PK tiebreak of the relationship listings (and, since a
// CreateRelationships batch shares one created_at, their effective discriminator).
// Every iam_roles structural column feeds the derived role_key / grant_key ordering
// expression, so ALL of them must be C for the concatenated key to sort byte-wise
// (a mixed collation would raise an indeterminate-collation error). Deliberately
// EXCLUDED: iam_relationships' resource_type/resource_id/relation/subject_type/
// subject_id/subject_relation are the recursion columns of the reachable
// userset-expansion CTE — collating them raises a recursive-term collation
// mismatch (SQLSTATE 42P21) unless the store SQL also collates the anchor casts
// (out of scope), and their ordering need only be a deterministic total order,
// which any collation supplies; iam_scopes / iam_mutations are equality/unique
// keys only (iam_scopes' lock order is computed in Go, not SQL). Byte parity
// already holds for all of those under any deterministic collation.
var contractualCollatedColumns = []collatedColumn{
	{"iam_relationships", "relationship_id", "keyset PK tiebreak"},
	{"iam_roles", "subject_type", "feeds derived role_key/grant_key ordering key"},
	{"iam_roles", "subject_id", "feeds derived role_key/grant_key ordering key"},
	{"iam_roles", "role", "feeds derived role_key/grant_key ordering key"},
	{"iam_roles", "resource_type", "feeds derived role_key ordering key"},
	{"iam_roles", "resource_id", "feeds derived role_key ordering key"},
}

// resetCanonicalSchema drops the canonical tables and clears their ledger rows,
// then re-applies the migrations, so the CURRENT COLLATE "C" DDL is materialized
// even if a stale schema_migrations checksum recorded an earlier version.
func resetCanonicalSchema(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range []string{"iam_mutations", "iam_scopes", "iam_roles", "iam_relationships"} {
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
			t.Fatalf("drop %s: %v", tbl, err)
		}
	}
	for _, v := range canonicalMigrations {
		_, _ = db.Exec(ctx, "DELETE FROM schema_migrations WHERE source = 'default' AND version = $1", v)
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// assertNonCDatabase fails loudly unless the connected database's default
// collation (datcollate) is a genuine non-C-family locale. Pointing the ordering
// proof at a C-family cluster would make it pass vacuously, so this is a hard
// failure, not a skip.
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

// TestContractualCollation_SQL asserts every inventoried column's DDL line
// carries COLLATE "C" in the canonical migrations (hermetic — no database).
func TestContractualCollation_SQL(t *testing.T) {
	var all strings.Builder
	for _, name := range migrationNames(t) {
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		all.Write(b)
		all.WriteByte('\n')
	}
	sql := all.String()

	for _, c := range contractualCollatedColumns {
		body := tableCreateBody(t, sql, c.Table)
		var found bool
		for _, line := range strings.Split(body, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == c.Column {
				found = true
				if !strings.Contains(line, `COLLATE "C"`) {
					t.Errorf("%s.%s is contractual (%s) but its DDL lacks COLLATE \"C\": %q", c.Table, c.Column, c.Why, strings.TrimSpace(line))
				}
				break
			}
		}
		if !found {
			t.Errorf("no column definition found for %s.%s", c.Table, c.Column)
		}
	}
}

// TestContractualCollation_Catalog applies the canonical schema and asserts the
// catalog reports collation_name = 'C' for every inventoried column.
func TestContractualCollation_Catalog(t *testing.T) {
	dsn := requireDSN(t)
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	resetCanonicalSchema(t, db)

	ctx := context.Background()
	for _, c := range contractualCollatedColumns {
		var collation *string
		const q = `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`
		if err := db.QueryRow(ctx, q, c.Table, c.Column).Scan(&collation); err != nil {
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
// collation, not the cluster default, controls ordering. It exercises
// ListEffectiveByResource — a representative inventoried ordering path, ordered by
// the derived grant_key (subject_type||chr(1)||subject_id||chr(1)||role over the
// collated iam_roles columns) — with two subject ids whose byte order ('B' < 'a')
// is the reverse of the locale order ('a' < 'B'). The representative generalizes:
// TestContractualCollation_Catalog proves every inventoried column shares the
// same 'C' catalog collation.
func TestCollationControlsOrdering_NonC(t *testing.T) {
	dsn := os.Getenv("POSTGRES_NON_C_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_NON_C_TEST_DSN not set — non-C ordering proof NOT verified")
	}
	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// The proof is only real against a non-C database: fail loudly if it is
	// pointed at a C-family cluster, where byte and locale order coincide.
	assertNonCDatabase(t, db)

	resetCanonicalSchema(t, db)

	repos, err := Repositories(db)
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	roles := repos.Roles

	// Two viewer grants scoped to one resource, differing only in subject_id. 'B'
	// (0x42) sorts before 'a' (0x61) byte-wise; en_US.utf8 sorts 'a' before 'B'.
	// The grant_key differs at that byte, so the derived-key ORDER BY reveals the
	// winning collation.
	for _, a := range []role.Assignment{
		{SubjectType: "user", SubjectID: "aaa-collate-proof", Role: "viewer", ResourceType: "doc", ResourceID: "d-collate-proof"},
		{SubjectType: "user", SubjectID: "BBB-collate-proof", Role: "viewer", ResourceType: "doc", ResourceID: "d-collate-proof"},
	} {
		if err := roles.Assign(ctx, a); err != nil {
			t.Fatalf("Assign %s: %v", a.SubjectID, err)
		}
	}

	page, err := roles.ListEffectiveByResource(ctx, "doc", "d-collate-proof", crud.ListRequest{})
	if err != nil {
		t.Fatalf("ListEffectiveByResource: %v", err)
	}
	got := make([]string, 0, len(page.Items))
	for _, g := range page.Items {
		got = append(got, g.SubjectID)
	}
	// ListEffectiveByResource orders by grant_key ASC; under COLLATE "C" that is
	// byte order: "BBB…" (0x42) before "aaa…" (0x61). A non-C cluster default
	// would invert this.
	want := []string{"BBB-collate-proof", "aaa-collate-proof"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("effective grant order = %v, want %v (COLLATE \"C\" byte order must win over the cluster default)", got, want)
	}
}
