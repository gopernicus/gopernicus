package pgxdb

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestListRejectsUnsupportedSQLCursorValuesBeforeQuery(t *testing.T) {
	for _, value := range []any{nil, int64(1)} {
		q := newListQuery()
		q.OrderFields = map[string]list.OrderField{"name": {Column: "name", CastLower: true}}
		q.DefaultOrder = list.NewOrder("name", list.ASC)
		token, err := list.EncodeCursor("name", value, "pk")
		if err != nil {
			t.Fatal(err)
		}
		captured := &listCapture{}
		if _, err := List(context.Background(), captured, q, list.Request{Cursor: token}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("cursor value %v: %v, want invalid input", value, err)
		}
		if captured.query != "" {
			t.Fatal("invalid cursor reached the database")
		}
	}
}
