package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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

	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn)})
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

	// Ledger recorded exactly one row for this fixture's migration. Other
	// suites (the store suites) share the default source on a shared test
	// database, so only this version is counted.
	var n int
	if err := db.QueryRow(ctx,
		"SELECT count(*) FROM schema_migrations WHERE source = $1 AND version = '0001_init.sql'", defaultMigrationSource).Scan(&n); err != nil {
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

var scratchOrderFields = map[string]list.OrderField{
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
		DefaultOrder: list.NewOrder("created_at", list.DESC),
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
func traverse(t *testing.T, db Querier, q ListQuery[scratchRow], order list.Order, limit int) []string {
	t.Helper()
	ctx := context.Background()
	seen := map[string]bool{}
	var ids []string
	cursor := ""
	for i := 0; i < 1000; i++ {
		p, err := List(ctx, db, q, list.Request{Limit: limit, Cursor: cursor, Order: order})
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
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn)})
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
		got := traverse(t, db, q, list.NewOrder("created_at", list.DESC), 2)
		eqIDs(t, got, []string{"e5", "e4", "e3", "e2", "e1"}, "created desc")
	})
	t.Run("forward_created_asc", func(t *testing.T) {
		got := traverse(t, db, q, list.NewOrder("created_at", list.ASC), 2)
		eqIDs(t, got, []string{"e1", "e2", "e3", "e4", "e5"}, "created asc")
	})
	t.Run("forward_name_asc", func(t *testing.T) {
		got := traverse(t, db, q, list.NewOrder("name", list.ASC), 2)
		eqIDs(t, got, []string{"e1", "e2", "e3", "e4", "e5"}, "name asc")
	})
	t.Run("forward_n_asc", func(t *testing.T) {
		got := traverse(t, db, q, list.NewOrder("n", list.ASC), 2)
		eqIDs(t, got, []string{"e2", "e3", "e1", "e4", "e5"}, "n asc")
	})

	t.Run("prev_probe_full_window", func(t *testing.T) {
		// With 5 rows at limit 2 the pages are [e5 e4] [e3 e2] [e1]. Page 3's
		// previous page is page 2 (a full window, not the first page), so the
		// probe returns limit rows and PreviousCursor is set.
		desc := list.NewOrder("created_at", list.DESC)
		p1, err := List(ctx, db, q, list.Request{Limit: 2, Order: desc})
		if err != nil {
			t.Fatalf("p1: %v", err)
		}
		if p1.HasPrev {
			t.Fatal("first page HasPrev = true, want false")
		}
		p2, err := List(ctx, db, q, list.Request{Limit: 2, Cursor: p1.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p2: %v", err)
		}
		p3, err := List(ctx, db, q, list.Request{Limit: 2, Cursor: p2.NextCursor, Order: desc})
		if err != nil {
			t.Fatalf("p3: %v", err)
		}
		eqIDs(t, idsOf(p3.Items), []string{"e1"}, "page 3 rows")
		if !p3.HasPrev || p3.PreviousCursor == "" {
			t.Fatalf("p3 HasPrev=%v prevCursor=%q, want true/set (full window)", p3.HasPrev, p3.PreviousCursor)
		}
		// The previous cursor pages back to page 2.
		back, err := List(ctx, db, q, list.Request{Limit: 2, Cursor: p3.PreviousCursor, Order: desc})
		if err != nil {
			t.Fatalf("back: %v", err)
		}
		eqIDs(t, idsOf(back.Items), []string{"e3", "e2"}, "previous cursor round-trip")
	})

	t.Run("prev_probe_partial_window", func(t *testing.T) {
		desc := list.NewOrder("created_at", list.DESC)
		p1, err := List(ctx, db, q, list.Request{Limit: 3, Order: desc})
		if err != nil {
			t.Fatalf("p1: %v", err)
		}
		p2, err := List(ctx, db, q, list.Request{Limit: 3, Cursor: p1.NextCursor, Order: desc})
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
		desc := list.NewOrder("created_at", list.DESC)
		var got []string
		for off := 0; off < 6; off += 2 {
			p, err := List(ctx, db, q, list.Request{Limit: 2, Offset: off, Order: desc, Strategy: list.StrategyOffset})
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
		p, err := List(ctx, db, q, list.Request{Limit: 2, WithCount: true})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if p.Total == nil || *p.Total != 5 {
			t.Fatalf("Total = %v, want 5 (kind a rows, not capped by limit)", p.Total)
		}
		pb, err := List(ctx, db, scratchQuery("b"), list.Request{Limit: 10, WithCount: true})
		if err != nil {
			t.Fatalf("List b: %v", err)
		}
		if pb.Total == nil || *pb.Total != 2 {
			t.Fatalf("Total(b) = %v, want 2", pb.Total)
		}
	})

	t.Run("stale_cursor_is_first_page", func(t *testing.T) {
		// A cursor tagged with a different order field decodes stale → first page.
		token, err := list.EncodeCursor("name", "delta", "e4")
		if err != nil {
			t.Fatalf("EncodeCursor: %v", err)
		}
		p, err := List(ctx, db, q, list.Request{Limit: 2, Cursor: token, Order: list.NewOrder("created_at", list.DESC)})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		eqIDs(t, idsOf(p.Items), []string{"e5", "e4"}, "stale cursor first page")
		if p.HasPrev {
			t.Error("stale-cursor first page HasPrev = true, want false")
		}
	})

	t.Run("fixed_order_offset", func(t *testing.T) {
		// A store-fixed composite order with NULLS LAST over a nullable column
		// the allow-list path cannot express: closing_at DESC NULLS LAST, then
		// name ASC, then the pk. e2/e4 share the latest closing_at (name breaks
		// the tie), e5 closes earlier, e1/e3 never close and sort last by name.
		if _, err := db.Exec(ctx, "ALTER TABLE list_scratch ADD COLUMN closing_at TIMESTAMPTZ"); err != nil {
			t.Fatalf("add closing_at: %v", err)
		}
		for id, at := range map[string]time.Time{
			"e2": base.Add(72 * time.Hour),
			"e4": base.Add(72 * time.Hour),
			"e5": base.Add(24 * time.Hour),
		} {
			if _, err := db.Exec(ctx, "UPDATE list_scratch SET closing_at = @at WHERE id = @id", jackpgx.NamedArgs{"at": at, "id": id}); err != nil {
				t.Fatalf("close %s: %v", id, err)
			}
		}

		type fixedScratchRow struct {
			ID        string     `db:"id"`
			CreatedAt time.Time  `db:"created_at"`
			Name      string     `db:"name"`
			N         int64      `db:"n"`
			ClosingAt *time.Time `db:"closing_at"`
		}
		fq := ListQuery[fixedScratchRow]{
			BaseSQL:    "SELECT id, created_at, name, n, closing_at FROM list_scratch WHERE kind = @kind",
			Args:       jackpgx.NamedArgs{"kind": "a"},
			FixedOrder: "closing_at DESC NULLS LAST, name ASC, id ASC",
			PK:         "id",
			PKOf:       func(r fixedScratchRow) string { return r.ID },
		}

		var got []string
		for i, want := range []struct {
			ids     []string
			hasMore bool
			hasPrev bool
		}{
			{[]string{"e2", "e4"}, true, false},
			{[]string{"e5", "e1"}, true, true},
			{[]string{"e3"}, false, true},
		} {
			p, err := List(ctx, db, fq, list.Request{Limit: 2, Offset: i * 2, Strategy: list.StrategyOffset, WithCount: true})
			if err != nil {
				t.Fatalf("page %d: %v", i+1, err)
			}
			pageIDs := make([]string, len(p.Items))
			for j, row := range p.Items {
				pageIDs[j] = row.ID
			}
			eqIDs(t, pageIDs, want.ids, fmt.Sprintf("fixed order page %d", i+1))
			if p.HasMore != want.hasMore || p.HasPrev != want.hasPrev {
				t.Errorf("page %d HasMore/HasPrev = %v/%v, want %v/%v", i+1, p.HasMore, p.HasPrev, want.hasMore, want.hasPrev)
			}
			if p.Total == nil || *p.Total != 5 {
				t.Errorf("page %d Total = %v, want 5", i+1, p.Total)
			}
			if p.NextCursor != "" || p.PreviousCursor != "" {
				t.Errorf("page %d emitted cursors under the offset strategy", i+1)
			}
			got = append(got, pageIDs...)
		}
		eqIDs(t, got, []string{"e2", "e4", "e5", "e1", "e3"}, "fixed order traversal")

		if _, err := List(ctx, db, fq, list.Request{Limit: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("cursor strategy under FixedOrder err = %v, want ErrInvalidInput", err)
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

// TestLive_ProbeTable proves ProbeTable against a real database: a present
// relation (schema-qualified and bare) is nil, an absent one wraps
// sdk.ErrNotFound naming it, and the probe runs unchanged inside a *Tx.
func TestLive_ProbeTable(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres conformance NOT verified")
	}
	ctx := context.Background()
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(ctx, "CREATE TABLE IF NOT EXISTS pg_connector_probe_check (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, "DROP TABLE IF EXISTS pg_connector_probe_check") })

	if err := ProbeTable(ctx, db, "pg_connector_probe_check"); err != nil {
		t.Errorf("bare present table: %v", err)
	}
	if err := ProbeTable(ctx, db, "public.pg_connector_probe_check"); err != nil {
		t.Errorf("qualified present table: %v", err)
	}
	err = ProbeTable(ctx, db, "public.pg_connector_probe_absent")
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "public.pg_connector_probe_absent") {
		t.Errorf("absent table: err=%v, want ErrNotFound naming the relation", err)
	}
	if err := db.InTx(ctx, func(tx *Tx) error { return ProbeTable(ctx, tx, "pg_connector_probe_check") }); err != nil {
		t.Errorf("probe inside a transaction: %v", err)
	}
}

// collectScratchRow is the child-row struct TestLive_Collect scans into.
type collectScratchRow struct {
	ID       string `db:"id"`
	ParentID string `db:"parent_id"`
	Name     string `db:"name"`
}

// TestLive_Collect proves Collect against a real database: a parent-bounded
// query scans every child row in order, a parent with no children is an empty
// NON-NIL slice (so the caller's JSON says [] and never null), and a failing
// query comes back through MapError — a malformed uuid literal (SQLSTATE 22P02)
// as sdk.ErrInvalidInput keeping the server's message, an unknown relation
// unchanged. Gated on POSTGRES_TEST_DSN; skips loudly when unset.
func TestLive_Collect(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres Collect behavior NOT verified")
	}

	ctx := context.Background()
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS collect_scratch"); err != nil {
		t.Fatalf("drop scratch: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE collect_scratch (
		id TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS collect_scratch") })

	seed := []collectScratchRow{
		{ID: "c1", ParentID: "p1", Name: "alpha"},
		{ID: "c2", ParentID: "p1", Name: "bravo"},
		{ID: "c3", ParentID: "p2", Name: "charlie"},
	}
	for _, r := range seed {
		if _, err := db.Exec(ctx,
			"INSERT INTO collect_scratch (id, parent_id, name) VALUES (@id, @parent_id, @name)",
			jackpgx.NamedArgs{"id": r.ID, "parent_id": r.ParentID, "name": r.Name}); err != nil {
			t.Fatalf("seed %s: %v", r.ID, err)
		}
	}

	const childSQL = "SELECT id, parent_id, name FROM collect_scratch WHERE parent_id = @parent ORDER BY id"

	t.Run("parent_bounded_rows", func(t *testing.T) {
		got, err := Collect[collectScratchRow](ctx, db, childSQL, jackpgx.NamedArgs{"parent": "p1"})
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if len(got) != 2 || got[0].ID != "c1" || got[1].ID != "c2" || got[0].Name != "alpha" {
			t.Fatalf("rows = %#v, want c1/alpha then c2/bravo", got)
		}
	})

	t.Run("no_children_is_empty_not_nil", func(t *testing.T) {
		got, err := Collect[collectScratchRow](ctx, db, childSQL, jackpgx.NamedArgs{"parent": "absent"})
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if got == nil {
			t.Fatal("no rows returned a nil slice, want an empty non-nil []T")
		}
		if len(got) != 0 {
			t.Fatalf("rows = %#v, want empty", got)
		}
	})

	t.Run("inside_a_transaction", func(t *testing.T) {
		if err := db.InTx(ctx, func(tx *Tx) error {
			got, err := Collect[collectScratchRow](ctx, tx, childSQL, jackpgx.NamedArgs{"parent": "p2"})
			if err != nil {
				return err
			}
			if len(got) != 1 || got[0].ID != "c3" {
				t.Fatalf("rows = %#v, want just c3", got)
			}
			return nil
		}); err != nil {
			t.Fatalf("Collect inside a transaction: %v", err)
		}
	})

	t.Run("malformed_uuid_maps_to_invalid_input", func(t *testing.T) {
		got, err := Collect[collectScratchRow](ctx,
			db, "SELECT @id::uuid::text AS id, 'p' AS parent_id, 'n' AS name",
			jackpgx.NamedArgs{"id": "not-a-uuid"})
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput through MapError (SQLSTATE 22P02)", err)
		}
		if !strings.Contains(err.Error(), "not-a-uuid") {
			t.Fatalf("err = %v, want the server message naming the malformed value", err)
		}
		if got != nil {
			t.Fatalf("rows = %#v, want nil on error", got)
		}
	})

	t.Run("unknown_relation_passes_through", func(t *testing.T) {
		_, err := Collect[collectScratchRow](ctx, db, "SELECT id, parent_id, name FROM collect_scratch_absent", nil)
		if err == nil {
			t.Fatal("Collect over an absent relation = nil error")
		}
		if sdk.IsExpected(err) {
			t.Fatalf("err = %v, want an unmapped SQLSTATE to pass through", err)
		}
	})
}

// TestLive_Timeouts proves the statement/lock timeout knobs against a real
// server: a set knob is the session's setting, sent in the startup packet
// (pg_settings source "client"); a statement past StatementTimeout fails with
// SQLSTATE 57014 and a lock wait past LockTimeout with 55P03, each passing
// through MapError unchanged as every unmapped SQLSTATE does (a 500, never a
// domain sentinel); a zero knob sends nothing; and Open refuses a
// DSN-and-Config double setting and a sub-millisecond value while a DSN-only
// setting reaches the server as written. Gated on POSTGRES_TEST_DSN; skips
// loudly when unset.
func TestLive_Timeouts(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres statement/lock timeouts NOT verified")
	}
	ctx := context.Background()

	open := func(t *testing.T, cfg Config) *DB {
		t.Helper()
		db, err := Open(ctx, cfg)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	withParam := func(t *testing.T, key, value string) string {
		t.Helper()
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("parse POSTGRES_TEST_DSN: %v", err)
		}
		q := u.Query()
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	setting := func(t *testing.T, db *DB, name string) (value, source string) {
		t.Helper()
		if err := db.QueryRow(ctx, "SHOW "+name).Scan(&value); err != nil {
			t.Fatalf("SHOW %s: %v", name, err)
		}
		if err := db.QueryRow(ctx, "SELECT source FROM pg_settings WHERE name = $1", name).Scan(&source); err != nil {
			t.Fatalf("pg_settings source for %s: %v", name, err)
		}
		return value, source
	}
	wantSQLState := func(t *testing.T, err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("err = %v, want SQLSTATE %s", err, code)
		}
		if sdk.IsExpected(err) {
			t.Fatalf("err = %v mapped to a domain sentinel, want SQLSTATE %s passed through unchanged", err, code)
		}
	}

	t.Run("statement_timeout_cancels_pg_sleep", func(t *testing.T) {
		db := open(t, Config{DSN: sdk.Secret(dsn), StatementTimeout: time.Second})
		value, source := setting(t, db, "statement_timeout")
		if value != "1s" || source != "client" {
			t.Fatalf("statement_timeout = %q (source %q), want \"1s\" from the startup packet", value, source)
		}
		t.Logf("SHOW statement_timeout = %q (source %q)", value, source)

		started := time.Now()
		_, err := db.Exec(ctx, "SELECT pg_sleep(5)")
		elapsed := time.Since(started)
		wantSQLState(t, err, "57014")
		if elapsed >= 4*time.Second {
			t.Fatalf("pg_sleep(5) ran %v, want it canceled near the 1s statement timeout", elapsed)
		}
		t.Logf("pg_sleep(5) under StatementTimeout=1s: %v after %v", err, elapsed.Round(time.Millisecond))
	})

	t.Run("lock_timeout_abandons_a_held_lock", func(t *testing.T) {
		holder := open(t, Config{DSN: sdk.Secret(dsn)})
		if _, err := holder.Exec(ctx, "CREATE TABLE IF NOT EXISTS pg_connector_lock_check (id INT)"); err != nil {
			t.Fatalf("create fixture table: %v", err)
		}
		t.Cleanup(func() { _, _ = holder.Exec(context.Background(), "DROP TABLE IF EXISTS pg_connector_lock_check") })

		tx, err := holder.Begin(ctx)
		if err != nil {
			t.Fatalf("begin holder: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(ctx, "LOCK TABLE pg_connector_lock_check IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatalf("hold lock: %v", err)
		}

		db := open(t, Config{DSN: sdk.Secret(dsn), LockTimeout: 200 * time.Millisecond})
		value, source := setting(t, db, "lock_timeout")
		if value != "200ms" || source != "client" {
			t.Fatalf("lock_timeout = %q (source %q), want \"200ms\" from the startup packet", value, source)
		}
		t.Logf("SHOW lock_timeout = %q (source %q)", value, source)

		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		started := time.Now()
		_, err = db.Exec(waitCtx, "SELECT count(*) FROM pg_connector_lock_check")
		elapsed := time.Since(started)
		wantSQLState(t, err, "55P03")
		t.Logf("SELECT behind an ACCESS EXCLUSIVE lock under LockTimeout=200ms: %v after %v", err, elapsed.Round(time.Millisecond))
	})

	t.Run("zero_knobs_send_nothing", func(t *testing.T) {
		db := open(t, Config{DSN: sdk.Secret(dsn)})
		for _, name := range []string{"statement_timeout", "lock_timeout"} {
			if value, source := setting(t, db, name); source == "client" {
				t.Fatalf("%s = %q from the startup packet, want the server's own setting for a zero knob", name, value)
			}
		}
	})

	t.Run("dsn_only_setting_reaches_the_server", func(t *testing.T) {
		db := open(t, Config{DSN: sdk.Secret(withParam(t, "statement_timeout", "1500"))})
		value, source := setting(t, db, "statement_timeout")
		if value != "1500ms" || source != "client" {
			t.Fatalf("statement_timeout = %q (source %q), want the DSN's \"1500ms\"", value, source)
		}
	})

	t.Run("double_setting_refused_at_open", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			cfg  Config
		}{
			{"dsn_key", Config{DSN: sdk.Secret(withParam(t, "statement_timeout", "2000")), StatementTimeout: time.Second}},
			{"dsn_options", Config{DSN: sdk.Secret(withParam(t, "options", "-c lock_timeout=2000")), LockTimeout: time.Second}},
		} {
			db, err := Open(ctx, tc.cfg)
			if db != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				if db != nil {
					_ = db.Close()
				}
				t.Fatalf("%s: Open = %v, %v; want nil, ErrInvalidInput", tc.name, db, err)
			}
			t.Logf("%s: Open refused: %v", tc.name, err)
		}
	})

	t.Run("sub_millisecond_refused_at_open", func(t *testing.T) {
		for _, cfg := range []Config{
			{DSN: sdk.Secret(dsn), StatementTimeout: 500 * time.Microsecond},
			{DSN: sdk.Secret(dsn), LockTimeout: 999 * time.Microsecond},
		} {
			db, err := Open(ctx, cfg)
			if db != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				if db != nil {
					_ = db.Close()
				}
				t.Fatalf("Open(StatementTimeout=%v, LockTimeout=%v) = %v, %v; want nil, ErrInvalidInput", cfg.StatementTimeout, cfg.LockTimeout, db, err)
			}
			t.Logf("Open refused: %v", err)
		}
	})
}
