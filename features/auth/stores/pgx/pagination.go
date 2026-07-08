package pgx

import (
	"context"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// listPage runs a keyset-paginated SELECT shared by the paginated auth ports
// (service accounts, API keys, security events, invitations). where already holds
// its leading "WHERE …" with its own $1.. placeholders and args their bound
// values; listPage appends the created_at DESC, pkCol DESC cursor predicate
// (numbering its placeholders from len(args)+1) and the over-fetch LIMIT, scans
// each row with scan, and trims to a page — the SQL twin of the reference
// pageDESC. keyOf returns each record's (created_at, id) for cursor encoding.
func listPage[T any](
	ctx context.Context,
	db *pgxdb.DB,
	columns, table, where string,
	args []any,
	pkCol string,
	req crud.ListRequest,
	scan func(scanner) (T, error),
	keyOf func(T) (time.Time, string),
) (crud.Page[T], error) {
	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[T]{}, err
	}
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		ts := cv.UTC()
		where += fmt.Sprintf(" AND ((created_at < $%d) OR (created_at = $%d AND %s < $%d))",
			len(args)+1, len(args)+2, pkCol, len(args)+3)
		args = append(args, ts, ts, cur.PK)
	}

	limit := req.NormalizedLimit()
	query := fmt.Sprintf("SELECT %s FROM %s %s ORDER BY created_at DESC, %s DESC LIMIT $%d",
		columns, table, where, pkCol, len(args)+1)
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
		return crud.Page[T]{}, pgxdb.MapError(err)
	}

	return crud.TrimPage(items, limit, func(it T) (string, error) {
		t, id := keyOf(it)
		return crud.EncodeCursor(orderField, t, id)
	})
}
