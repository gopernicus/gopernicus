package pgxdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// ListQuery describes one paginated SELECT for List. T is a store-local,
// db-tagged row struct that pgx.RowToStructByName scans into. BaseSQL is the
// SELECT with its optional filter WHERE and NO ORDER BY / LIMIT / OFFSET —
// List appends those. Args holds BaseSQL's named args (nil when the filter has
// none). OrderFields is the aggregate's allow-list: List resolves the request
// Order against it (by column) so only vetted columns reach SQL, and DefaultOrder
// applies when the request Order is zero. PK is the tiebreaker/cursor column.
// OrderValueOf returns a row's value for the resolved order column and PKOf its
// pk, both used to encode cursors. Limits is the resource's page-size vocabulary
// passed to req.NormalizedLimit; the zero value preserves the crud-constant
// defaults.
type ListQuery[T any] struct {
	BaseSQL      string
	Args         pgx.NamedArgs
	OrderFields  map[string]crud.OrderField
	DefaultOrder crud.Order
	PK           string
	Limits       crud.Limits
	OrderValueOf func(row T, field string) any
	PKOf         func(row T) string
}

// List runs a paginated SELECT implementing the sdk/crud list matrix over
// pgx.CollectRows + RowToStructByName. It validates the request, resolves the
// order against q.OrderFields, then switches on req.ResolvedStrategy() into one
// of two linear flows: listCursor appends the keyset tuple predicate (and, when
// a cursor is present, runs a reverse probe to fill HasPrev/PreviousCursor);
// listOffset appends LIMIT/OFFSET, derives HasMore from its own over-fetch, and
// emits no cursors. Both over-fetch limit+1 for HasMore and share collect/count.
// When req.WithCount is set, Total is the full filtered row count from a
// COUNT(*) wrap of BaseSQL. A stale cursor (order field changed) decodes to the
// first page. Errors pass through MapError.
func List[T any](ctx context.Context, db Querier, q ListQuery[T], req crud.ListRequest) (crud.Page[T], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[T]{}, err
	}

	orderCol, castLower, direction, err := q.resolveOrder(req.Order)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		return q.listOffset(ctx, db, req, orderCol, castLower, direction)
	}
	return q.listCursor(ctx, db, req, orderCol, castLower, direction)
}

// listCursor is the keyset flow: decode the cursor (a nil cursor — first page or
// a stale token — skips the predicate and the reverse probe), over-fetch
// limit+1, TrimPage for HasMore/NextCursor, then the reverse probe for
// HasPrev/PreviousCursor when a cursor was present.
func (q ListQuery[T]) listCursor(ctx context.Context, db Querier, req crud.ListRequest, orderCol string, castLower bool, direction string) (crud.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)

	var cursor *crud.Cursor
	if req.Cursor != "" {
		var err error
		cursor, err = crud.DecodeCursor(req.Cursor, orderCol)
		if err != nil {
			return crud.Page[T]{}, fmt.Errorf("decode cursor: %w: %w", sdk.ErrInvalidInput, err)
		}
	}

	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := cloneArgs(q.Args)

	if cursor != nil {
		if err := ApplyCursorPagination(&buf, args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, false, castLower); err != nil {
			return crud.Page[T]{}, err
		}
	}
	if err := AddOrderByClause(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return crud.Page[T]{}, err
	}
	AddLimitClause(&buf, args, limit+1)

	items, err := q.collect(ctx, db, buf.String(), args)
	if err != nil {
		return crud.Page[T]{}, err
	}

	encode := func(row T) (string, error) {
		return crud.EncodeCursor(orderCol, q.OrderValueOf(row, orderCol), q.PKOf(row))
	}

	page, err := crud.TrimPage(items, limit, encode)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if cursor != nil {
		if err := q.markPrev(ctx, db, &page, orderCol, direction, castLower, limit, cursor, encode); err != nil {
			return crud.Page[T]{}, err
		}
	}

	if req.WithCount {
		total, err := q.count(ctx, db)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.Total = &total
	}

	return page, nil
}

// listOffset is the LIMIT/OFFSET flow: same ORDER BY, LIMIT n+1 OFFSET off. It
// derives HasMore from its own over-fetch and sets HasPrev from Offset; it never
// encodes a cursor — NextCursor/PreviousCursor stay empty, and the caller does
// the offset arithmetic (see the crud strategy matrix).
func (q ListQuery[T]) listOffset(ctx context.Context, db Querier, req crud.ListRequest, orderCol string, castLower bool, direction string) (crud.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)

	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := cloneArgs(q.Args)

	if err := AddOrderByClause(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return crud.Page[T]{}, err
	}
	AddLimitClause(&buf, args, limit+1)
	buf.WriteString(" OFFSET @" + offsetArg)
	args[offsetArg] = req.Offset

	items, err := q.collect(ctx, db, buf.String(), args)
	if err != nil {
		return crud.Page[T]{}, err
	}

	page := crud.Page[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.HasMore = true
	}
	page.HasPrev = req.Offset > 0

	if req.WithCount {
		total, err := q.count(ctx, db)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.Total = &total
	}

	return page, nil
}

// collect runs sql with args and scans every row into T via RowToStructByName.
func (q ListQuery[T]) collect(ctx context.Context, db Querier, sql string, args pgx.NamedArgs) ([]T, error) {
	rows, err := db.Query(ctx, sql, args)
	if err != nil {
		return nil, MapError(err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, MapError(err)
	}
	return items, nil
}

// resolveOrder maps the request Order (or DefaultOrder when zero) to a vetted
// column, its CastLower flag, and a normalized direction by matching the order
// field against the columns in q.OrderFields. An order field absent from the
// allow-list returns an error wrapping sdk.ErrInvalidInput.
func (q ListQuery[T]) resolveOrder(order crud.Order) (column string, castLower bool, direction string, err error) {
	if order.Field == "" {
		order = q.DefaultOrder
	}

	dir := crud.ASC
	if order.Direction == crud.DESC {
		dir = crud.DESC
	}

	for _, of := range q.OrderFields {
		if of.Column == order.Field {
			return of.Column, of.CastLower, dir, nil
		}
	}
	return "", false, "", fmt.Errorf("unknown order field %q: %w", order.Field, sdk.ErrInvalidInput)
}

// markPrev runs the reverse probe for cursor mode and applies crud.MarkPrevPage.
// The probe flips the operator and ORDER BY, fetches up to limit rows, and
// reverses them back to forward order so the probe's first row is the previous
// page's first record.
func (q ListQuery[T]) markPrev(ctx context.Context, db Querier, page *crud.Page[T], orderCol, direction string, castLower bool, limit int, cursor *crud.Cursor, encode func(T) (string, error)) error {
	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := cloneArgs(q.Args)

	if err := ApplyCursorPagination(&buf, args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, true, castLower); err != nil {
		return err
	}
	if err := AddOrderByClause(&buf, orderCol, q.PK, direction, true, castLower); err != nil {
		return err
	}
	AddLimitClause(&buf, args, limit)

	prev, err := q.collect(ctx, db, buf.String(), args)
	if err != nil {
		return err
	}

	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}

	return crud.MarkPrevPage(page, prev, limit, encode)
}

// count returns the full filtered row count by wrapping BaseSQL in a
// COUNT(*) subquery with the same filter args — never the cursor/offset
// predicates, never capped by limit.
func (q ListQuery[T]) count(ctx context.Context, db Querier) (int64, error) {
	sql := "SELECT COUNT(*) FROM (" + q.BaseSQL + ") AS list_count"
	var total int64
	if err := db.QueryRow(ctx, sql, cloneArgs(q.Args)).Scan(&total); err != nil {
		return 0, MapError(err)
	}
	return total, nil
}

// cloneArgs returns a non-nil copy of a's named args so a query can add cursor,
// limit, and offset args without mutating the caller's BaseSQL args.
func cloneArgs(a pgx.NamedArgs) pgx.NamedArgs {
	out := make(pgx.NamedArgs, len(a)+3)
	for k, v := range a {
		out[k] = v
	}
	return out
}
