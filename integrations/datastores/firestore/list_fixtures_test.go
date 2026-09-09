//go:build integration

// Shared fixtures for the List suite. This file carries the //go:build
// integration tag ALONE — not `integration && !live` — because both legs page
// the same row type through the same helpers: the emulator leg in
// list_integration_test.go and the live leg in list_live_test.go. Each leg owns
// its own database factory and seeding; everything that describes WHAT a list
// row is and HOW a page is asserted lives here.
package firestore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// listBase is the timestamp the fixtures are spaced from.
var listBase = time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

// listItem is the row type the List suite pages over.
type listItem struct {
	ID        string
	CreatedAt time.Time
	Name      string
	N         int64
}

// listQueryFor builds the ListQuery the suite pages with: parent-scoped by
// kind, ordered over created_at/name/n/id or the document id, with "id" as the
// PK (the shape the SQL stores have).
func listQueryFor(db *firestore.DB, collection, kind string) firestore.ListQuery[listItem] {
	return firestore.ListQuery[listItem]{
		Query: db.Collection(collection).Where("kind", "==", kind),
		OrderFields: map[string]crud.OrderField{
			"created_at": {Column: "created_at"},
			"name":       {Column: "name"},
			"n":          {Column: "n"},
			"id":         {Column: "id"},
			"doc_id":     {Column: gcfs.DocumentID},
		},
		DefaultOrder: crud.NewOrder("created_at", crud.DESC),
		PK:           "id",
		Decode:       decodeListItem,
		OrderValueOf: func(row listItem, field string) any {
			switch field {
			case "name":
				return row.Name
			case "n":
				return row.N
			case "id", gcfs.DocumentID:
				return row.ID
			default:
				return row.CreatedAt
			}
		},
		PKOf: func(row listItem) string { return row.ID },
	}
}

// decodeListItem reads one document into a listItem. The id comes from the
// document reference, which is the one field a struct decode cannot see.
func decodeListItem(snap *gcfs.DocumentSnapshot) (listItem, error) {
	data := snap.Data()
	created, err := firestore.ParseTime(data["created_at"])
	if err != nil {
		return listItem{}, fmt.Errorf("%s: created_at: %w", snap.Ref.ID, err)
	}
	name, _ := data["name"].(string)
	n, _ := data["n"].(int64)
	return listItem{ID: snap.Ref.ID, CreatedAt: created, Name: name, N: n}, nil
}

// mustList runs one List call and fails the test on error.
func mustList(t *testing.T, ctx context.Context, r firestore.Reader, q firestore.ListQuery[listItem], req crud.ListRequest) crud.Page[listItem] {
	t.Helper()
	page, err := firestore.List(ctx, r, q, req)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return page
}

// traverseListIDs pages the whole list with the given order and limit,
// following NextCursor, and fails if any row appears twice.
func traverseListIDs(t *testing.T, ctx context.Context, r firestore.Reader, q firestore.ListQuery[listItem], order crud.Order, limit int) []string {
	t.Helper()

	seen := map[string]bool{}
	var ids []string
	cursor := ""
	for range 1000 {
		page := mustList(t, ctx, r, q, crud.ListRequest{Limit: limit, Cursor: cursor, Order: order})
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatalf("id %q on more than one page", row.ID)
			}
			seen[row.ID] = true
			ids = append(ids, row.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	return ids
}

func listIDs(rows []listItem) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.ID
	}
	return out
}

func eqListIDs(t *testing.T, got, want []string, label string) {
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
