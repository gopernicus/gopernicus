package pgxdb

import (
	"context"
	"errors"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// errCapture short-circuits ListPage right after it hands the assembled query to
// the Querier, so a hermetic test (no Postgres) can assert the exact SQL string,
// $N placeholder numbering, and the time.Time cursor binding the pgx dialect
// produces — without a live database.
var errCapture = errors.New("captured")

type captureQuerier struct {
	query string
	args  []any
}

func (c *captureQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCapture
}

func (c *captureQuerier) Query(_ context.Context, query string, args ...any) (jackpgx.Rows, error) {
	c.query = query
	c.args = args
	return nil, errCapture
}

func (c *captureQuerier) QueryRow(context.Context, string, ...any) jackpgx.Row { return nil }

func stubScan(Scanner) (int, error)   { return 0, nil }
func stubKey(int) (time.Time, string) { return time.Time{}, "" }

// TestListPage_SQLShape_NoCursor: the first-page query carries no cursor
// predicate and numbers its LIMIT placeholder immediately after the base WHERE's
// args.
func TestListPage_SQLShape_NoCursor(t *testing.T) {
	cq := &captureQuerier{}
	_, err := ListPage(context.Background(), cq, "id, name", "widgets", "WHERE kind = $1", []any{"gadget"},
		"created_at", "id", crud.ListRequest{Limit: 2}, stubScan, stubKey)
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}

	wantQuery := "SELECT id, name FROM widgets WHERE kind = $1 ORDER BY created_at DESC, id DESC LIMIT $2"
	if cq.query != wantQuery {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, wantQuery)
	}
	if len(cq.args) != 2 || cq.args[0] != "gadget" || cq.args[1] != 3 {
		t.Fatalf("args = %#v, want [gadget 3] (limit+1)", cq.args)
	}
}

// TestListPage_SQLShape_WithCursor: a cursor appends the (created_at DESC, id
// DESC) keyset predicate, continuing $N numbering from the base WHERE's args,
// and binds the cursor's time value as a time.Time (UTC) — the pgx dialect's
// contract, distinct from turso's FormatTime string.
func TestListPage_SQLShape_WithCursor(t *testing.T) {
	curTime := time.Date(2026, 7, 8, 12, 34, 56, 0, time.FixedZone("x", 3*3600))
	token, err := crud.EncodeCursor("created_at", curTime, "pk-123")
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}

	cq := &captureQuerier{}
	_, err = ListPage(context.Background(), cq, "id, name", "widgets", "WHERE kind = $1", []any{"gadget"},
		"created_at", "id", crud.ListRequest{Limit: 2, Cursor: token}, stubScan, stubKey)
	if !errors.Is(err, errCapture) {
		t.Fatalf("err = %v, want errCapture", err)
	}

	wantQuery := "SELECT id, name FROM widgets " +
		"WHERE kind = $1 AND ((created_at < $2) OR (created_at = $3 AND id < $4)) " +
		"ORDER BY created_at DESC, id DESC LIMIT $5"
	if cq.query != wantQuery {
		t.Fatalf("query =\n  %q\nwant\n  %q", cq.query, wantQuery)
	}

	if len(cq.args) != 5 {
		t.Fatalf("args len = %d, want 5", len(cq.args))
	}
	if cq.args[0] != "gadget" {
		t.Errorf("args[0] = %v, want gadget", cq.args[0])
	}
	wantTS := curTime.UTC()
	for _, i := range []int{1, 2} {
		got, ok := cq.args[i].(time.Time)
		if !ok {
			t.Fatalf("args[%d] = %T, want time.Time (pgx binds the cursor as time.Time)", i, cq.args[i])
		}
		if !got.Equal(wantTS) {
			t.Errorf("args[%d] = %v, want %v", i, got, wantTS)
		}
	}
	if cq.args[3] != "pk-123" {
		t.Errorf("args[3] = %v, want pk-123", cq.args[3])
	}
	if cq.args[4] != 3 {
		t.Errorf("args[4] = %v, want 3 (limit+1)", cq.args[4])
	}
}
