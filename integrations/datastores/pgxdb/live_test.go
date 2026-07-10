package pgxdb

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
	"time"

	jackpgx "github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// TestLive_OpenAndMigrate is the one env-gated live test: it opens a real
// connection (Open pings), then runs a migrate-apply round-trip and confirms
// the ledger recorded the host-owned migration stream. It skips loudly when
// POSTGRES_TEST_DSN is unset — a silent green here would be a false green.
//
// Spin a local database with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...
func TestLive_OpenAndMigrate(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}

	ctx := context.Background()

	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Keep the run repeatable: drop the ledger and the fixture table first.
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS pg_connector_live_check"); err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM schema_migrations WHERE source = 'default' AND version = '0001_init.sql'"); err != nil {
		// The ledger may not exist yet on a clean database; that is fine.
		t.Logf("clean ledger (ignored on first run): %v", err)
	}

	fsys := fstest.MapFS{
		"migrations/0001_init.sql": {Data: []byte("CREATE TABLE pg_connector_live_check (id TEXT PRIMARY KEY);")},
	}

	if err := RunMigrations(ctx, db, fsys, "migrations"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Ledger recorded exactly one row for the host-owned stream.
	var n int
	if err := db.QueryRow(ctx,
		"SELECT count(*) FROM schema_migrations WHERE source = $1", defaultMigrationSource).Scan(&n); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("ledger rows for %q = %d, want 1", defaultMigrationSource, n)
	}

	// The migrated table exists (to_regclass returns non-NULL).
	var regclass *string
	if err := db.QueryRow(ctx,
		"SELECT to_regclass('pg_connector_live_check')").Scan(&regclass); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if regclass == nil {
		t.Fatal("migrated table pg_connector_live_check does not exist")
	}

	// Re-apply is a no-op (checksum guard passes, no error, no new row).
	if err := RunMigrations(ctx, db, fsys, "migrations"); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

// scratchRow is the db-tagged row struct pgx.RowToStructByName scans in the
// List behavior suite.
type scratchRow struct {
	ID        string    `db:"id"`
	CreatedAt time.Time `db:"created_at"`
	Name      string    `db:"name"`
	N         int64     `db:"n"`
}

var scratchOrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
	"name":       {Column: "name"},
	"n":          {Column: "n"},
	"id":         {Column: "id"},
}

func scratchQuery(kind string) ListQuery[scratchRow] {
	return ListQuery[scratchRow]{
		BaseSQL:      "SELECT id, created_at, name, n FROM list_scratch WHERE kind = @kind",
		Args:         jackpgx.NamedArgs{"kind": kind},
		OrderFields:  scratchOrderFields,
		DefaultOrder: crud.NewOrder("created_at", crud.DESC),
		PK:           "id",
		OrderValueOf: func(r scratchRow, field string) any {
			switch field {
			case "name":
				return r.Name
			case "n":
				return r.N
			case "id":
				return r.ID
			default:
				return r.CreatedAt
			}
		},
		PKOf: func(r scratchRow) string { return r.ID },
	}
}

// traverse pages the whole result set forward via cursors and returns the ids in
// visitation order, asserting no page exceeds the limit and no id repeats.
func traverse(t *testing.T, db Querier, q ListQuery[scratchRow], order crud.Order, limit int) []string {
	t.Helper()
	ctx := context.Background()
	seen := map[string]bool{}
	var ids []string
	cursor := ""
	for i := 0; i < 1000; i++ {
		p, err := List(ctx, db, q, crud.ListRequest{Limit: limit, Cursor: cursor, Order: order})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(p.Items) > limit {
			t.Fatalf("page %d over limit: %d items", i, len(p.Items))
		}
		for _, r := range p.Items {
			if seen[r.ID] {
				t.Fatalf("id %q returned on more than one page", r.ID)
			}
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
		if !p.HasMore {
			break
		}
		if p.NextCursor == "" {
			t.Fatal("HasMore true but NextCursor empty")
		}
		cursor = p.NextCursor
	}
	return ids
}

func eqIDs(t *testing.T, got, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// TestLive_ListBehavior exercises List against a live Postgres scratch table:
// forward traversal per order (created_at asc/desc, name string, n int),
// prev-probe full/partial/first-page windows, offset↔cursor traversal
// equivalence, count under a WHERE filter, and stale-cursor-as-first-page.
// Gated on POSTGRES_TEST_DSN; skips loudly when unset.
func TestLive_ListBehavior(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres List behavior NOT verified")
	}

	ctx := context.Background()
	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS list_scratch"); err != nil {
		t.Fatalf("drop scratch: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE list_scratch (
		id TEXT PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL,
		name TEXT NOT NULL,
		n BIGINT NOT NULL,
		kind TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS list_scratch") })

	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	seed := []scratchRow{
		{ID: "e1", CreatedAt: base.Add(0 * time.Minute), Name: "alpha", N: 30},
		{ID: "e2", CreatedAt: base.Add(1 * time.Minute), Name: "bravo", N: 10},
		{ID: "e3", CreatedAt: base.Add(2 * time.Minute), Name: "charlie", N: 20},
		{ID: "e4", CreatedAt: base.Add(3 * time.Minute), Name: "delta", N: 40},
		{ID: "e5", CreatedAt: base.Add(4 * time.Minute), Name: "echo", N: 50},
	}
	for _, r := range seed {
		if _, err := db.Exec(ctx,
			"INSERT INTO list_scratch (id, created_at, name, n, kind) VALUES (@id, @created_at, @name, @n, @kind)",
			jackpgx.NamedArgs{"id": r.ID, "created_at": r.CreatedAt, "name": r.Name, "n": r.N, "kind": "a"}); err != nil {
			t.Fatalf("seed %s: %v", r.ID, err)
		}
	}
	// Two rows of a second kind, to prove the count honors the filter WHERE.
	for _, id := range []string{"b1", "b2"} {
		if _, err := db.Exec(ctx,
			"INSERT INTO list_scratch (id, created_at, name, n, kind) VALUES (@id, @created_at, @name, @n, @kind)",
			jackpgx.NamedArgs{"id": id, "created_at": base, "name": id, "n": int64(1), "kind": "b"}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	q := scratchQuery("a")

	t.Run("forward_created_desc", func(t *testing.T) {
		got := traverse(t, db, q, crud.NewOrder("created_at", crud.DESC), 2)
		eqIDs(t, got, []string{"e5", "e4", "e3", "e2", "e1"}, "created desc")
	})
	t.Run("forward_created_asc", func(t *testing.T) {
		got := traverse(t, db, q, crud.NewOrder("created_at", crud.ASC), 2)
		eqIDs(t, got, []string{"e1", "e2", "e3", "e4", "e5"}, "created asc")
	})
	t.Run("forward_name_asc", func(t *testing.T) {
		got := traverse(t, db, q, crud.NewOrder("name", crud.ASC), 2)
		eqIDs(t, got, []string{"e1", "e2", "e3", "e4", "e5"}, "name asc")
	})
	t.Run("forward_n_asc", func(t *testing.T) {
		got := traverse(t, db, q, crud.NewOrder("n", crud.ASC), 2)
		eqIDs(t, got, []string{"e2", "e3", "e1", "e4", "e5"}, "n asc")
	})

	t.Run("prev_probe_full_window", func(t *testing.T) {
		// With 5 rows at limit 2 the pages are [e5 e4] [e3 e2] [e1]. Page 3's
		// previous page is page 2 (a full window, not the first page), so the
		// probe returns limit rows and PreviousCursor is set.
		desc := crud.NewOrder("created_at", crud.DESC)
		p1, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Order: desc})
		if err != nil {
			t.Fatalf("p1: %v", err)
		}
		if p1.HasPrev {
			t.Fatal("first page HasPrev = true, want false")
		}
		p2, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: p1.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p2: %v", err)
		}
		p3, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: p2.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p3: %v", err)
		}
		eqIDs(t, idsOf(p3.Items), []string{"e1"}, "page 3 rows")
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("p3 HasPrev=%v prevCursor=%q, want true/set (full window)", p3.HasPrev, p3.PreviousCursor)
		}
		// The previous cursor pages back to page 2.
		back, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: p3.PreviousCursor, Order: desc})
		if err != nil {
			t.Fatalf("back: %v", err)
		}
		eqIDs(t, idsOf(back.Items), []string{"e3", "e2"}, "previous cursor round-trip")
	})

	t.Run("prev_probe_partial_window", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		p1, err := List(ctx, db, q, crud.ListRequest{Limit: 3, Order: desc})
		if err != nil {
			t.Fatalf("p1: %v", err)
		}
		p2, err := List(ctx, db, q, crud.ListRequest{Limit: 3, Cursor: p1.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p2: %v", err)
		}
		if !p2.HasPrev {
			t.Fatal("p2 HasPrev = false, want true")
		}
		if p2.PreviousCursor != "" {
			t.Fatalf("p2 PreviousCursor = %q, want empty (partial window = previous page is first page)", p2.PreviousCursor)
		}
	})

	t.Run("offset_matches_cursor", func(t *testing.T) {
		desc := crud.NewOrder("created_at", crud.DESC)
		var got []string
		for off := 0; off < 6; off += 2 {
			p, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Offset: off, Order: desc, Strategy: crud.StrategyOffset})
			if err != nil {
				t.Fatalf("offset %d: %v", off, err)
			}
			if wantPrev := off > 0; p.HasPrev != wantPrev {
				t.Errorf("offset %d HasPrev = %v, want %v", off, p.HasPrev, wantPrev)
			}
			// Offset strategy emits no cursors at any offset — the caller does the
			// offset arithmetic.
			if p.NextCursor != "" || p.PreviousCursor != "" {
				t.Errorf("offset %d emitted cursors: next=%q prev=%q", off, p.NextCursor, p.PreviousCursor)
			}
			got = append(got, idsOf(p.Items)...)
		}
		eqIDs(t, got, []string{"e5", "e4", "e3", "e2", "e1"}, "offset traversal")
	})

	t.Run("count_under_filter", func(t *testing.T) {
		p, err := List(ctx, db, q, crud.ListRequest{Limit: 2, WithCount: true})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if p.Total == nil || *p.Total != 5 {
			t.Fatalf("Total = %v, want 5 (kind a rows, not capped by limit)", p.Total)
		}
		pb, err := List(ctx, db, scratchQuery("b"), crud.ListRequest{Limit: 10, WithCount: true})
		if err != nil {
			t.Fatalf("List b: %v", err)
		}
		if pb.Total == nil || *pb.Total != 2 {
			t.Fatalf("Total(b) = %v, want 2", pb.Total)
		}
	})

	t.Run("stale_cursor_is_first_page", func(t *testing.T) {
		// A cursor tagged with a different order field decodes stale → first page.
		token, err := crud.EncodeCursor("name", "delta", "e4")
		if err != nil {
			t.Fatalf("EncodeCursor: %v", err)
		}
		p, err := List(ctx, db, q, crud.ListRequest{Limit: 2, Cursor: token, Order: crud.NewOrder("created_at", crud.DESC)})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		eqIDs(t, idsOf(p.Items), []string{"e5", "e4"}, "stale cursor first page")
		if p.HasPrev {
			t.Error("stale-cursor first page HasPrev = true, want false")
		}
	})
}

func idsOf(rows []scratchRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}
