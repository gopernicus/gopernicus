package turso

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// LOWER(name) must use the selected COALESCE alias on every page, including
// the first page without search. Applying it inside the authored SELECT orders
// the nullable source column instead and can make the first cursor skip rows.
func TestListTransformedOrderProjection(t *testing.T) {
	ctx := context.Background()
	db := newMemDB(t)
	if _, err := db.Exec(ctx, `CREATE TABLE projected_names (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO projected_names VALUES ('e1', NULL), ('e2', 'alpha'), ('e3', 'bravo')`); err != nil {
		t.Fatal(err)
	}
	type projectedRow struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	query := ListQuery[projectedRow]{
		BaseSQL:      `SELECT id, COALESCE(name, 'zeta') AS name FROM projected_names`,
		OrderFields:  map[string]list.OrderField{"name": {Column: "name", CastLower: true}},
		SearchFields: []list.SearchField{{Column: "name"}},
		PK:           "id",
		OrderValueOf: func(row projectedRow, _ string) any { return row.Name },
		PKOf:         func(row projectedRow) string { return row.ID },
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		for _, search := range []string{"", "a"} { // Every projected name contains a.
			for _, strategy := range []list.Strategy{list.StrategyCursor, list.StrategyOffset} {
				t.Run(fmt.Sprintf("%s/search=%q/%s", direction, search, strategy), func(t *testing.T) {
					want := []string{"e2", "e3", "e1"}
					if direction == list.DESC {
						slices.Reverse(want)
					}
					req := list.Request{Limit: 1, Order: list.NewOrder("name", direction), Search: search, Strategy: strategy, WithCount: true}
					for i, id := range want {
						page, err := List(ctx, db, query, req)
						if err != nil {
							t.Fatal(err)
						}
						if len(page.Items) != 1 || page.Items[0].ID != id {
							t.Fatalf("page %d items=%v, want id %s", i, page.Items, id)
						}
						if page.Total == nil || *page.Total != 3 || page.HasMore != (i < 2) || page.HasPrev != (i > 0) {
							t.Fatalf("page %d metadata: %+v", i, page)
						}
						if strategy == list.StrategyOffset {
							req.Offset++
							continue
						}
						if page.HasPrev {
							backRequest := req
							backRequest.Cursor = page.PreviousCursor
							back, err := List(ctx, db, query, backRequest)
							if err != nil {
								t.Fatal(err)
							}
							if len(back.Items) != 1 || back.Items[0].ID != want[i-1] {
								t.Fatalf("previous page=%v, want id %s", back.Items, want[i-1])
							}
						}
						req.Cursor = page.NextCursor
					}
				})
			}
		}
	}
}
