package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// ListPage runs a keyset-paginated SELECT shared by the paginated store ports.
// where already holds its leading "WHERE …" and args its bound values; ListPage
// appends the (orderField DESC, pkCol DESC) cursor predicate and the over-fetch
// LIMIT, scans each row with scan, and trims to a page. orderField is both the
// cursor's order-field tag and the ordered/predicate column (created_at for
// every current caller). keyOf returns each record's (orderValue, pk) for cursor
// encoding. The libSQL dialect binds the cursor's time value as a FormatTime
// string, matching the fixed-width TEXT storage.
func ListPage[T any](
	ctx context.Context,
	db Querier,
	columns, table, where string,
	args []any,
	orderField, pkCol string,
	req crud.ListRequest,
	scan func(Scanner) (T, error),
	keyOf func(T) (time.Time, string),
) (crud.Page[T], error) {
	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[T]{}, err
	}
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		ts := FormatTime(cv)
		where += " AND ((" + orderField + " < ?) OR (" + orderField + " = ? AND " + pkCol + " < ?))"
		args = append(args, ts, ts, cur.PK)
	}

	limit := req.NormalizedLimit()
	query := "SELECT " + columns + " FROM " + table + " " + where + " ORDER BY " + orderField + " DESC, " + pkCol + " DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return crud.Page[T]{}, err
	}
	defer rows.Close()

	var items []T
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return crud.Page[T]{}, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return crud.Page[T]{}, MapError(err)
	}

	return crud.TrimPage(items, limit, func(it T) (string, error) {
		t, id := keyOf(it)
		return crud.EncodeCursor(orderField, t, id)
	})
}
