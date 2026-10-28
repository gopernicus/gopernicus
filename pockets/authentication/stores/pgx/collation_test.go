// Contractual-collation tests pin the AAH-5 / plan D5 inventory: the opaque TEXT
// columns whose byte-wise ordering is a store contract carry an explicit
// COLLATE "C" in the canonical migrations, so the guarantee travels with the
// schema instead of relying on the cluster's default collation.
//
// Three inventories stand side by side so adding or dropping a column is a
// visible test edit: contractualCollatedColumns (keyset tiebreaks),
// generatedIDColumns (every pocket-generated id, with its database backstop)
// and referencingIDColumns (the columns that hold one of those ids). Three
// checks stand on them:
//
//   - TestContractualCollation_SQL (hermetic): every inventoried column's
//     canonical DDL carries COLLATE "C", and every generated id column carries
//     its class's DEFAULT backstop.
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

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

const (
	// collationSQL reads one column's catalog collation. Like the store's own
	// default column probe it is unfiltered by table_schema — the default leg
	// resolves the column on the search_path, exactly as it always has.
	collationSQL = `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`

	// collationSchemaSQL scopes that read to the POSTGRES_TEST_SCHEMA leg's
	// schema, so a same-named table in the default schema cannot answer for it.
	collationSchemaSQL = `SELECT collation_name FROM information_schema.columns WHERE table_name = $1 AND column_name = $2 AND table_schema = $3`
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
// string) order. Tables without keyset pagination (sessions, oauth_states,
// challenges, contact_changes, authentication_grants), list orders the port does
// not promise and the suite does not assert (oauth_accounts.provider_user_id,
// user_identifiers.id — the reference iterates an unordered map), and human
// address/display columns (invitations.identifier, service_accounts.name, …) are
// deliberately excluded: equality parity already holds under any deterministic
// collation, and collating display text would change user-facing sort (Risk 7).
//
// users.id JOINED the inventory in CHAU-1.2: the operator directory
// (user.AdminRepository.List) pages users by (created_at DESC, id DESC), and the
// storetest UserDirectory/OrderingAndCursorParity collision case pins that
// tiebreak. Its pin is an ALTER in migration 0014 rather than a CREATE TABLE
// clause, because 0001_users.sql is tagged and immutable — see pinnedByAlter.
var contractualCollatedColumns = []collatedColumn{
	{"service_accounts", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"api_keys", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"security_events", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"invitations", "id", "keyset PK tiebreak; collision suite pins byte order"},
	{"users", "id", "operator-directory keyset PK tiebreak (CHAU-1.2); pinned by ALTER in 0014"},
}

// Database backstops by entity class (plan D3). Go mints every id; the column
// default only serves a raw SQL insert or a host wired with sdk.DatabaseID.
const (
	// recordBackstop is the record class's default: a UUIDv7 (PostgreSQL 18).
	recordBackstop = "DEFAULT uuidv7()::text"
	// identityBackstop is the identity class's default (users, service
	// accounts, invitations): a random UUIDv4, because a time-ordered id there
	// would reveal when the identity was created.
	identityBackstop = "DEFAULT gen_random_uuid()::text"
)

// generatedIDColumn is one pocket-generated id column and its backstop.
type generatedIDColumn struct {
	collatedColumn
	Backstop string
}

// generatedIDColumns inventories every id the pocket generates. Each carries
// COLLATE "C" — the collation is a property of the column, not the generator —
// and its class's backstop. users.id is pinned by the 0014 ALTER (0001 is
// left as it is).
var generatedIDColumns = []generatedIDColumn{
	{collatedColumn{"users", "id", "identity id; pinned by ALTER in 0014"}, identityBackstop},
	{collatedColumn{"service_accounts", "id", "identity id"}, identityBackstop},
	{collatedColumn{"invitations", "id", "identity id"}, identityBackstop},
	{collatedColumn{"api_keys", "id", "record id"}, recordBackstop},
	{collatedColumn{"security_events", "id", "record id"}, recordBackstop},
	{collatedColumn{"user_identifiers", "id", "record id"}, recordBackstop},
	{collatedColumn{"challenges", "id", "record id"}, recordBackstop},
	{collatedColumn{"contact_changes", "id", "record id"}, recordBackstop},
	{collatedColumn{"authentication_grants", "id", "record id"}, recordBackstop},
	{collatedColumn{"passkey_credentials", "id", "record id"}, recordBackstop},
	{collatedColumn{"recovery_codes", "id", "record id"}, recordBackstop},
}

// referencingIDColumns inventories the columns that hold a pocket-generated id
// (plan R5). They share the "C" collation of the id they reference because the
// planner uses an index only under the join key's collation: with
// user_identifiers.user_id on the default collation the directory join against
// users.id ("C") seq-scanned user_identifiers. Host-defined ids (resource_id,
// actor_id, provider_user_id) and recovery_codes.key_id (a pepper keyring id)
// are excluded.
var referencingIDColumns = []collatedColumn{
	{"user_passwords", "user_id", "references users.id"},
	{"sessions", "user_id", "references users.id"},
	{"oauth_accounts", "user_id", "references users.id"},
	{"service_accounts", "owner_user_id", "references users.id"},
	{"api_keys", "service_account_id", "references service_accounts.id"},
	{"security_events", "user_id", "references users.id"},
	{"user_identifiers", "user_id", "references users.id"},
	{"challenges", "user_id", "references users.id"},
	{"contact_changes", "user_id", "references users.id"},
	{"contact_changes", "replaces_identifier_id", "references user_identifiers.id"},
	{"authentication_grants", "session_id", "references sessions.id"},
	{"authentication_grants", "user_id", "references users.id"},
	{"oauth_authorization_codes", "user_id", "references users.id"},
	{"oauth_refresh_history", "session_id", "references sessions.id"},
	{"external_subjects", "user_id", "references users.id"},
	{"passkey_credentials", "user_id", "references users.id"},
	{"totp_credentials", "user_id", "references users.id"},
	{"totp_credentials", "session_id", "references sessions.id"},
	{"recovery_codes", "user_id", "references users.id"},
	{"service_account_status", "service_account_id", "references service_accounts.id"},
}

// collatedInventory is every column the three inventories pin to "C".
func collatedInventory() []collatedColumn {
	out := append([]collatedColumn(nil), contractualCollatedColumns...)
	for _, g := range generatedIDColumns {
		out = append(out, g.collatedColumn)
	}
	return append(out, referencingIDColumns...)
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

// pinnedByAlter reports whether the canonical SQL pins table.column's collation
// through an append-only ALTER instead of its CREATE TABLE. A column that became
// contractual AFTER its table was tagged cannot be fixed in place — the tagged
// migration is immutable — so the pin arrives as an ALTER and this is where the
// hermetic assertion learns to accept it. The LIVE catalog check
// (TestContractualCollation_Catalog) is unchanged and remains the real proof: it
// reads collation_name from the applied schema and does not care how it got there.
func pinnedByAlter(sql, table, column string) bool {
	want := `ALTER TABLE ` + table + ` ALTER COLUMN ` + column + ` TYPE TEXT COLLATE "C"`
	return strings.Contains(sql, want)
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

	for _, c := range collatedInventory() {
		if pinnedByAlter(sql, c.Table, c.Column) {
			continue
		}
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
		line, found := columnLine(tableCreateBody(t, sql, g.Table), g.Column)
		if !found {
			t.Errorf("no column definition found for %s.%s", g.Table, g.Column)
			continue
		}
		if !strings.Contains(line, g.Backstop) {
			t.Errorf("%s.%s (%s) DDL lacks its backstop %q: %q", g.Table, g.Column, g.Why, g.Backstop, strings.TrimSpace(line))
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
	schema := testSchema(t)
	for _, c := range collatedInventory() {
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
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: sdk.Secret(dsn)})
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

	store := NewServiceAccountStore(db, storeOpts(testSchema(t))...)
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

	page, err := store.List(ctx, list.Request{})
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
