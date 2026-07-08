package pgxdb

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// ListPage runs a keyset-paginated SELECT shared by the paginated store ports.
// where already holds its leading "WHERE …" with its own $1.. placeholders and
// args their bound values; ListPage appends the (orderField DESC, pkCol DESC)
// cursor predicate — numbering its placeholders from len(args)+1, so the base
// WHERE owns $1.. and the appended predicates continue the sequence — and the
// over-fetch LIMIT, scans each row with scan, and trims to a page. orderField is
// both the cursor's order-field tag and the ordered/predicate column (created_at
// for every current caller). keyOf returns each record's (orderValue, pk) for
// cursor encoding. The pgx dialect binds the cursor's time value as time.Time.
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
		ts := cv.UTC()
		where += fmt.Sprintf(" AND ((%s < $%d) OR (%s = $%d AND %s < $%d))",
			orderField, len(args)+1, orderField, len(args)+2, pkCol, len(args)+3)
		args = append(args, ts, ts, cur.PK)
	}

	limit := req.NormalizedLimit()
	query := fmt.Sprintf("SELECT %s FROM %s %s ORDER BY %s DESC, %s DESC LIMIT $%d",
		columns, table, where, orderField, pkCol, len(args)+1)
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
