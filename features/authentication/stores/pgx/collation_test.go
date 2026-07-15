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
//     the keyset ordering, proven through the store's real List query path.
package pgx

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
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

// contractualCollatedColumns is the authentication inventory (AAH-5). Every entry
// is the keyset PK tiebreak of a created_at DESC, id DESC listing whose storetest
// collision suite pins the tiebreak to the reference store's byte-wise (Go
// string) order. Tables without keyset pagination (users, sessions, oauth_states,
// challenges, contact_changes, authentication_grants), list orders the port does
// not promise and the suite does not assert (oauth_accounts.provider_user_id,
// user_identifiers.id — the reference iterates an unordered map), and human
// address/display columns (invitations.identifier, service_accounts.name, …) are
// deliberately excluded: equality parity already holds under any deterministic
// collation, and collating display text would change user-facing sort (Risk 7).
var contractualCollatedColumns = []collatedColumn{
	{"service_accounts", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"api_keys", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"security_events", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"invitations", "id", "keyset PK tiebreak; collision suite pins byte order"},
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
// catalog reports collation_name = 'C' for every inventoried column. It reuses
// the schema-probe drop/recreate reset so a stale ledger cannot mask a change.
func TestContractualCollation_Catalog(t *testing.T) {
	db := probeDial(t)
	probeResetSchema(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })

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
// collation, not the cluster default, controls the keyset ordering. It exercises
// service_accounts.List — a representative inventoried keyset path — with two ids
// whose byte order ('B' < 'a') is the reverse of the locale order ('a' < 'B'),
// so the paged created_at DESC, id DESC result reveals which collation won. The
// representative generalizes: TestContractualCollation_Catalog proves every
// inventoried column shares the same 'C' catalog collation.
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

	// Fresh schema so the current COLLATE "C" DDL is applied (drop + re-migrate,
	// bypassing the checksum ledger).
	probeResetSchema(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })

	store := NewServiceAccountStore(db)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 'B' (0x42) sorts before 'a' (0x61) byte-wise; en_US.utf8 sorts 'a' before
	// 'B'. Same created_at, so the id tiebreak alone decides the order.
	lower := serviceaccount.ServiceAccount{ID: "aaa-collate-proof", Name: "lower", CreatedBy: "proof", CreatedAt: base, UpdatedAt: base}
	upper := serviceaccount.ServiceAccount{ID: "BBB-collate-proof", Name: "upper", CreatedBy: "proof", CreatedAt: base, UpdatedAt: base}
	if _, err := store.Create(ctx, upper); err != nil {
		t.Fatalf("create upper: %v", err)
	}
	if _, err := store.Create(ctx, lower); err != nil {
		t.Fatalf("create lower: %v", err)
	}

	page, err := store.List(ctx, crud.ListRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(page.Items))
	for _, sa := range page.Items {
		if sa.CreatedBy == "proof" {
			got = append(got, sa.ID)
		}
	}
	// created_at DESC, id DESC under COLLATE "C" is byte-descending: "aaa…"
	// (0x61) before "BBB…" (0x42). A non-C cluster default would invert this.
	want := []string{"aaa-collate-proof", "BBB-collate-proof"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("paged id order = %v, want %v (COLLATE \"C\" byte order must win over the cluster default)", got, want)
	}
}
