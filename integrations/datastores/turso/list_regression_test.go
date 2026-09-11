package turso

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type auditListRow struct {
	ID string `db:"id"`
	N  int64  `db:"n"`
}

func TestListNestedFiltersAndReverseNavigation(t *testing.T) {
	db := newMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "CREATE TABLE audit_rows (id TEXT PRIMARY KEY, n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"e1", "e2", "e3", "e4", "e5"} {
		if _, err := db.Exec(ctx, "INSERT INTO audit_rows VALUES (?,?)", id, i+1); err != nil {
			t.Fatal(err)
		}
	}
	for _, base := range []string{
		"SELECT id,n FROM audit_rows WHERE n=1 OR n=2 OR n=3",
		"SELECT id,n FROM (SELECT id,n FROM audit_rows WHERE n<=3) AS nested",
	} {
		for _, direction := range []string{list.ASC, list.DESC} {
			q := ListQuery[auditListRow]{
				BaseSQL:      base,
				OrderFields:  map[string]list.OrderField{"id": {Column: "id"}},
				DefaultOrder: list.NewOrder("id", direction),
				SearchFields: []list.SearchField{{Column: "id"}},
				PK:           "id",
				OrderValueOf: func(r auditListRow, _ string) any { return r.ID },
				PKOf:         func(r auditListRow) string { return r.ID },
			}
			for _, limit := range []int{1, 2, 3} {
				req := list.Request{Limit: limit, Search: "e", WithCount: true}
				var pages []list.Page[auditListRow]
				var seen []string
				for step := 0; step < 6; step++ {
					page, err := List(ctx, db, q, req)
					if err != nil {
						t.Fatal(err)
					}
					if page.Total == nil || *page.Total != 3 {
						t.Fatalf("filtered count: %+v", page)
					}
					if page.HasPrev != (step > 0) {
						t.Fatalf("limit %d step %d HasPrev=%v", limit, step, page.HasPrev)
					}
					for _, r := range page.Items {
						seen = append(seen, r.ID)
					}
					pages = append(pages, page)
					if !page.HasMore {
						break
					}
					req.Cursor = page.NextCursor
				}
				want := []string{"e1", "e2", "e3"}
				if direction == list.DESC {
					want = []string{"e3", "e2", "e1"}
				}
				if !reflect.DeepEqual(seen, want) {
					t.Fatalf("direction %s limit %d traversed %v, want %v", direction, limit, seen, want)
				}
				for i := len(pages) - 1; i > 0; i-- {
					req.Cursor = pages[i].PreviousCursor
					prev, err := List(ctx, db, q, req)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(prev.Items, pages[i-1].Items) {
						t.Fatalf("reverse step %d: %v, want %v", i, prev.Items, pages[i-1].Items)
					}
				}
			}
		}
	}
}

func TestListFoldedPrimaryKeyAndEmptyOffset(t *testing.T) {
	db := newMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "CREATE TABLE audit_rows (id TEXT PRIMARY KEY, n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "A", "b"} {
		if _, err := db.Exec(ctx, "INSERT INTO audit_rows VALUES (?,?)", id, i); err != nil {
			t.Fatal(err)
		}
	}
	q := ListQuery[auditListRow]{
		BaseSQL:      "SELECT id,n FROM audit_rows",
		OrderFields:  map[string]list.OrderField{"id": {Column: "id", CastLower: true}},
		DefaultOrder: list.NewOrder("id", list.ASC),
		PK:           "id",
		OrderValueOf: func(r auditListRow, _ string) any { return r.ID },
		PKOf:         func(r auditListRow) string { return r.ID },
	}
	var seen []string
	req := list.Request{Limit: 1}
	for step := 0; step < 4; step++ {
		page, err := List(ctx, db, q, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Items {
			seen = append(seen, r.ID)
		}
		if !page.HasMore {
			break
		}
		req.Cursor = page.NextCursor
	}
	if !reflect.DeepEqual(seen, []string{"A", "a", "b"}) {
		t.Fatalf("folded PK walk: %v", seen)
	}
	q.BaseSQL += " WHERE n=99"
	page, err := List(ctx, db, q, list.Request{Strategy: list.StrategyOffset})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"items":[]}` {
		t.Fatalf("empty offset: %s", body)
	}
}

func TestListFloat32CursorAdvances(t *testing.T) {
	type floatRow struct {
		ID string  `db:"id"`
		N  float32 `db:"n"`
	}
	db := newMemDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "CREATE TABLE floats (id TEXT PRIMARY KEY, n REAL)"); err != nil {
		t.Fatal(err)
	}
	for i, value := range []float32{1.1, 2.2, 3.3} {
		if _, err := db.Exec(ctx, "INSERT INTO floats VALUES (?,?)", fmt.Sprint(i), value); err != nil {
			t.Fatal(err)
		}
	}
	q := ListQuery[floatRow]{
		BaseSQL:      "SELECT id,n FROM floats",
		OrderFields:  map[string]list.OrderField{"n": {Column: "n"}},
		DefaultOrder: list.NewOrder("n", list.ASC),
		PK:           "id",
		OrderValueOf: func(r floatRow, _ string) any { return r.N },
		PKOf:         func(r floatRow) string { return r.ID },
	}
	req := list.Request{Limit: 1}
	for _, want := range []string{"0", "1", "2"} {
		page, err := List(ctx, db, q, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].ID != want {
			t.Fatalf("page = %+v, want id %s", page, want)
		}
		req.Cursor = page.NextCursor
	}
}
