package turso

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// scanRowStruct is a well-formed db-tagged row struct exercising every wrapper
// Scanner type plus a skipped field.
type scanRowStruct struct {
	ID        string   `db:"id"`
	Name      string   `db:"name"`
	Active    Bool     `db:"active"`
	CreatedAt Time     `db:"created_at"`
	DeletedAt NullTime `db:"deleted_at"`
	Note      *string  `db:"note"`
	unexp     string   // unexported: skipped, no tag required
	Skip      string   `db:"-"`
}

// scanTypesTable builds a table with one row exercising the Scanner types.
func scanTypesTable(t *testing.T, db *DB) time.Time {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE things (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, active INTEGER NOT NULL,
		created_at TEXT NOT NULL, deleted_at TEXT, note TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	created := time.Date(2026, 7, 9, 12, 0, 0, 123456789, time.UTC)
	if _, err := db.Exec(ctx,
		`INSERT INTO things (id, name, active, created_at, deleted_at, note) VALUES (?, ?, ?, ?, ?, ?)`,
		"t1", "alpha", BoolToInt(true), FormatTime(created), nil, nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return created
}

func TestScanStruct_WrapperTypesRoundTrip(t *testing.T) {
	db := newMemDB(t)
	created := scanTypesTable(t, db)
	ctx := context.Background()

	rows, err := db.Query(ctx, "SELECT id, name, active, created_at, deleted_at, note FROM things")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no row")
	}
	got, err := ScanStruct[scanRowStruct](rows)
	if err != nil {
		t.Fatalf("ScanStruct: %v", err)
	}
	if got.ID != "t1" || got.Name != "alpha" {
		t.Fatalf("scalars: %+v", got)
	}
	if !bool(got.Active) {
		t.Fatalf("Bool: got %v want true", got.Active)
	}
	if !got.CreatedAt.Time.Equal(created) {
		t.Fatalf("Time: got %v want %v", got.CreatedAt.Time, created)
	}
	if got.DeletedAt.Valid {
		t.Fatalf("NullTime: NULL should read back invalid, got %+v", got.DeletedAt)
	}
	if got.DeletedAt.TimePtr() != nil {
		t.Fatalf("NullTime.TimePtr: NULL should be nil")
	}
	if got.Note != nil {
		t.Fatalf("*string: NULL should be nil, got %q", *got.Note)
	}
}

func TestScanStruct_NullTimeValidWhenSet(t *testing.T) {
	db := newMemDB(t)
	scanTypesTable(t, db)
	ctx := context.Background()
	del := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(ctx, "UPDATE things SET deleted_at = ? WHERE id = ?", FormatTime(del), "t1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows, err := db.Query(ctx, "SELECT id, name, active, created_at, deleted_at, note FROM things")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	rows.Next()
	got, err := ScanStruct[scanRowStruct](rows)
	if err != nil {
		t.Fatalf("ScanStruct: %v", err)
	}
	if !got.DeletedAt.Valid || !got.DeletedAt.Time.Equal(del) {
		t.Fatalf("NullTime set: got %+v want %v", got.DeletedAt, del)
	}
	if p := got.DeletedAt.TimePtr(); p == nil || !p.Equal(del) {
		t.Fatalf("NullTime.TimePtr: got %v", p)
	}
}

// unmatchedColumnRow lacks a field for the "name" column.
type unmatchedColumnRow struct {
	ID string `db:"id"`
}

// unmatchedFieldRow declares an extra tagged field with no column.
type unmatchedFieldRow struct {
	ID    string `db:"id"`
	Name  string `db:"name"`
	Extra string `db:"extra"`
}

// untaggedExportedRow carries an exported field with no db tag.
type untaggedExportedRow struct {
	ID   string `db:"id"`
	Name string `db:"name"`
	Oops string
}

// nullPlainRow scans a nullable column into a plain string (no wrapper).
type nullPlainRow struct {
	ID   string `db:"id"`
	Name string `db:"name"`
	Note string `db:"note"`
}

func firstRow(t *testing.T, db *DB, query string) *sql.Rows {
	t.Helper()
	rows, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("no row")
	}
	t.Cleanup(func() { rows.Close() })
	return rows
}

func TestScanStruct_StrictRejectionMatrix(t *testing.T) {
	db := newMemDB(t)
	scanTypesTable(t, db)

	t.Run("unmatched result column", func(t *testing.T) {
		rows := firstRow(t, db, "SELECT id, name FROM things")
		if _, err := ScanStruct[unmatchedColumnRow](rows); err == nil || !strings.Contains(err.Error(), "no matching db-tagged field") {
			t.Fatalf("want unmatched-column error, got %v", err)
		}
	})

	t.Run("unmatched tagged field", func(t *testing.T) {
		rows := firstRow(t, db, "SELECT id, name FROM things")
		if _, err := ScanStruct[unmatchedFieldRow](rows); err == nil || !strings.Contains(err.Error(), "no matching result column") {
			t.Fatalf("want unmatched-field error, got %v", err)
		}
	})

	t.Run("untagged exported field", func(t *testing.T) {
		rows := firstRow(t, db, "SELECT id, name FROM things")
		if _, err := ScanStruct[untaggedExportedRow](rows); err == nil || !strings.Contains(err.Error(), "has no db tag") {
			t.Fatalf("want untagged-exported error, got %v", err)
		}
	})

	t.Run("NULL into plain field surfaces driver error", func(t *testing.T) {
		rows := firstRow(t, db, "SELECT id, name, note FROM things")
		_, err := ScanStruct[nullPlainRow](rows)
		if err == nil {
			t.Fatal("want driver NULL-conversion error, got nil")
		}
		if strings.Contains(err.Error(), "no matching") {
			t.Fatalf("NULL error should be a driver scan error, not a mapping error: %v", err)
		}
	})

	t.Run("non-struct type", func(t *testing.T) {
		rows := firstRow(t, db, "SELECT id FROM things")
		if _, err := ScanStruct[string](rows); err == nil || !strings.Contains(err.Error(), "must be a struct") {
			t.Fatalf("want non-struct error, got %v", err)
		}
	})
}

// nilScanRow is a db-tagged row struct for the nil-Scan List path.
type nilScanRow struct {
	ID        string `db:"id"`
	Name      string `db:"name"`
	Active    Bool   `db:"active"`
	CreatedAt Time   `db:"created_at"`
}

func TestList_NilScanStructScan(t *testing.T) {
	db := newMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE items (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, active INTEGER NOT NULL, created_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
	for i, name := range []string{"a", "b", "c"} {
		if _, err := db.Exec(ctx,
			"INSERT INTO items (id, name, active, created_at) VALUES (?, ?, ?, ?)",
			name, name, BoolToInt(i%2 == 0), FormatTime(base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	q := ListQuery[nilScanRow]{
		BaseSQL:      "SELECT id, name, active, created_at FROM items",
		OrderFields:  map[string]crud.OrderField{"created_at": {Column: "created_at"}},
		DefaultOrder: crud.Order{Field: "created_at", Direction: crud.DESC},
		PK:           "id",
		OrderValueOf: func(r nilScanRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r nilScanRow) string { return r.ID },
		// Scan is nil: ScanStruct[nilScanRow] runs.
	}
	page, err := List(ctx, db, q, crud.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items: got %d want 3", len(page.Items))
	}
	// created_at DESC → c, b, a.
	if page.Items[0].ID != "c" || page.Items[2].ID != "a" {
		t.Fatalf("order: %v", page.Items)
	}
	if !bool(page.Items[2].Active) { // a was i==0 → active
		t.Fatalf("Bool round-trip through nil-Scan failed: %+v", page.Items[2])
	}
}

// compositeRow reproduces the hardest store-row shape (schedule/job-like): every
// nullability model in one struct — sql.NullString, sql.NullInt64, a []byte
// payload, the 0/1 Bool, non-null Time, nullable NullTime, and a nullable *string.
// It stands in for the swept stores' unexported row structs (which cannot build a
// memDB-backed *DB from their own module — no sqlite dep, no *sql.DB constructor),
// spot-checking the exact scan machinery those structs rely on against in-memory
// SQLite via the P2 technique: build table, insert, scan, compare.
type compositeRow struct {
	ID        string         `db:"id"`
	Kind      string         `db:"kind"`
	CronExpr  sql.NullString `db:"cron_expr"`
	EverySecs sql.NullInt64  `db:"every_secs"`
	Payload   []byte         `db:"payload"`
	Enabled   Bool           `db:"enabled"`
	NextRunAt Time           `db:"next_run_at"`
	LastRunAt NullTime       `db:"last_run_at"`
	LastJobID *string        `db:"last_job_id"`
}

func TestScanStruct_CompositeStoreRowShape(t *testing.T) {
	db := newMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE composites (
		id TEXT PRIMARY KEY, kind TEXT NOT NULL, cron_expr TEXT, every_secs INTEGER,
		payload TEXT NOT NULL, enabled INTEGER NOT NULL, next_run_at TEXT NOT NULL,
		last_run_at TEXT, last_job_id TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	next := time.Date(2026, 7, 9, 6, 0, 0, 0, time.UTC)
	last := time.Date(2026, 7, 9, 5, 0, 0, 0, time.UTC)
	// Row 1: nullables present. Row 2: nullables NULL.
	if _, err := db.Exec(ctx,
		`INSERT INTO composites VALUES (?,?,?,?,?,?,?,?,?)`,
		"a", "cron", "*/5 * * * *", nil, `{"k":1}`, BoolToInt(true), FormatTime(next), FormatTime(last), "job_1"); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO composites VALUES (?,?,?,?,?,?,?,?,?)`,
		"b", "every", nil, int64(30), `{}`, BoolToInt(false), FormatTime(next), nil, nil); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	const q = `SELECT id, kind, cron_expr, every_secs, payload, enabled, next_run_at, last_run_at, last_job_id FROM composites WHERE id = ?`

	a, err := func() (compositeRow, error) {
		rows, err := db.Query(ctx, q, "a")
		if err != nil {
			return compositeRow{}, err
		}
		defer rows.Close()
		rows.Next()
		return ScanStruct[compositeRow](rows)
	}()
	if err != nil {
		t.Fatalf("scan a: %v", err)
	}
	if a.CronExpr.String != "*/5 * * * *" || !a.CronExpr.Valid {
		t.Fatalf("NullString set: %+v", a.CronExpr)
	}
	if a.EverySecs.Valid {
		t.Fatalf("NullInt64 NULL should be invalid: %+v", a.EverySecs)
	}
	if string(a.Payload) != `{"k":1}` {
		t.Fatalf("payload: %q", a.Payload)
	}
	if !bool(a.Enabled) || !a.NextRunAt.Time.Equal(next) {
		t.Fatalf("enabled/next: %+v", a)
	}
	if !a.LastRunAt.Valid || !a.LastRunAt.Time.Equal(last) {
		t.Fatalf("NullTime set: %+v", a.LastRunAt)
	}
	if a.LastJobID == nil || *a.LastJobID != "job_1" {
		t.Fatalf("*string set: %v", a.LastJobID)
	}

	b, err := func() (compositeRow, error) {
		rows, err := db.Query(ctx, q, "b")
		if err != nil {
			return compositeRow{}, err
		}
		defer rows.Close()
		rows.Next()
		return ScanStruct[compositeRow](rows)
	}()
	if err != nil {
		t.Fatalf("scan b: %v", err)
	}
	if b.CronExpr.Valid {
		t.Fatalf("NullString NULL should be invalid: %+v", b.CronExpr)
	}
	if !b.EverySecs.Valid || b.EverySecs.Int64 != 30 {
		t.Fatalf("NullInt64 set: %+v", b.EverySecs)
	}
	if bool(b.Enabled) {
		t.Fatalf("enabled false expected")
	}
	if b.LastRunAt.Valid || b.LastRunAt.TimePtr() != nil {
		t.Fatalf("NullTime NULL: %+v", b.LastRunAt)
	}
	if b.LastJobID != nil {
		t.Fatalf("*string NULL should be nil: %v", *b.LastJobID)
	}
}

func TestScanStruct_MapsNoRowsThroughDriver(t *testing.T) {
	// Guards that a scan error still routes through MapError (sql.ErrNoRows path is
	// exercised by the store queryOne helpers; here we assert MapError is wired).
	if got := MapError(sql.ErrNoRows); !errors.Is(got, crud.ErrNotFound) {
		t.Fatalf("MapError(ErrNoRows) = %v", got)
	}
}
