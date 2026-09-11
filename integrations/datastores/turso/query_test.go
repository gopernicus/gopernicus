package turso

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryOneReportsWriteFinalizationFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target := url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "query.sqlite")}
	target.RawQuery = "_pragma=busy_timeout%281%29"
	db, err := Open(ctx, Config{URL: target.String(), MaxOpenConns: 2, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, "CREATE TABLE claims (id INTEGER PRIMARY KEY, claimed INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO claims VALUES (1, 0)"); err != nil {
		t.Fatal(err)
	}

	reader, err := db.Underlying().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var before int
	if err := reader.QueryRowContext(ctx, "SELECT claimed FROM claims WHERE id = 1").Scan(&before); err != nil {
		t.Fatal(err)
	}

	// The reader allows UPDATE ... RETURNING to produce its row but prevents
	// the implicit write transaction from committing when the rows close.
	type claim struct {
		ID int `db:"id"`
	}
	got, err := QueryOne[claim](ctx, db, "UPDATE claims SET claimed = 1 WHERE id = 1 RETURNING id")
	var sqliteError interface{ Code() int }
	if !errors.As(err, &sqliteError) || sqliteError.Code() != 5 {
		t.Fatalf("QueryOne = %+v, %v; want SQLite busy on write finalization", got, err)
	}
	if got.ID != 0 {
		t.Fatalf("uncommitted write returned a successful row: %+v", got)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := db.QueryRow(ctx, "SELECT claimed FROM claims WHERE id = 1").Scan(&stored); err != nil || stored != before {
		t.Fatalf("failed write changed persisted state: got %d, %v; want %d", stored, err, before)
	}

	got, err = QueryOne[claim](ctx, db, "UPDATE claims SET claimed = 1 WHERE id = 1 RETURNING id")
	if err != nil || got.ID != 1 {
		t.Fatalf("retry after releasing the reader = %+v, %v", got, err)
	}
	if err := db.QueryRow(ctx, "SELECT claimed FROM claims WHERE id = 1").Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("successful retry was not committed: got %d, %v", stored, err)
	}
}

func TestQueryOneHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"type":"ok","response":{"type":"execute","result":{"cols":[{"name":"id","decltype":"INTEGER"}],"rows":[[{"type":"integer","value":"1"}]],"affected_row_count":1}}}]}`))
	}))
	defer server.Close()
	db, err := Open(context.Background(), Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	type row struct {
		ID int `db:"id"`
	}
	for range 2 {
		got, err := QueryOne[row](context.Background(), db, "UPDATE claims SET claimed = 1 RETURNING id")
		if err != nil || got.ID != 1 {
			t.Fatalf("QueryOne through HTTP libsql = %+v, %v", got, err)
		}
	}
}
