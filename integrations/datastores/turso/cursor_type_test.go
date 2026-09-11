package turso

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestListRejectsUnsupportedSQLCursorValues(t *testing.T) {
	db := newMemDB(t)
	ctx := context.Background()
	seedListItems(t, db)
	for _, value := range []any{nil, int64(1)} {
		q := listQueryFor("a")
		q.OrderFields = map[string]list.OrderField{"name": {Column: "name", CastLower: true}}
		q.DefaultOrder = list.NewOrder("name", list.ASC)
		token, err := list.EncodeCursor("name", value, "e1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := List(ctx, db, q, list.Request{Cursor: token}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("cursor value %v: %v, want invalid input", value, err)
		}
		q.OrderValueOf = func(listRow, string) any { return value }
		if _, err := List(ctx, db, q, list.Request{Limit: 1}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("encoded value %v: %v, want invalid input", value, err)
		}
	}
}
