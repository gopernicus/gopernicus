package turso

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// ListQuery describes one paginated SELECT for List. It is the turso twin of the
// pgxdb ListQuery, using positional `?` placeholders.
// BaseSQL is the SELECT with its optional filter WHERE and NO ORDER BY / LIMIT /
// OFFSET. Args holds BaseSQL's positional args in placeholder order. OrderFields
// is the aggregate's allow-list: List resolves the request Order against it (by
// column) so only vetted, store-authored columns reach SQL, and DefaultOrder
// applies when the request Order is zero. PK is the tiebreaker/cursor column.
// Scan reads one row into T and is OPTIONAL: a nil Scan struct-scans each row via
// ScanStruct[T], which requires T be a db-tagged row struct whose result columns
// exactly match its `db:"..."` fields (stores then map to domain entities with
// crud.MapPage(page, row.toDomain)); pass a callback only for a T that is not a
// plain row struct. OrderValueOf and PKOf supply cursor encoding. Limits is the
// resource's page-size vocabulary passed to req.NormalizedLimit; the zero value
// preserves the crud-constant defaults.
type ListQuery[T any] struct {
	BaseSQL      string
	Args         []any
	OrderFields  map[string]crud.OrderField
	DefaultOrder crud.Order
	PK           string
	Limits       crud.Limits
	Scan         func(Scanner) (T, error)
	OrderValueOf func(row T, field string) any
	PKOf         func(row T) string
}

// List runs a paginated SELECT with the same observable semantics as pgxdb.List,
// over SQLite/libSQL's dialect. It validates the request, resolves the order
// against q.OrderFields, then switches on req.ResolvedStrategy() into one of two
// linear flows: listCursor appends the keyset tuple predicate (and, when a
// cursor is present, runs a reverse probe to fill HasPrev/PreviousCursor);
// listOffset appends LIMIT/OFFSET, derives HasMore from its own over-fetch, and
// emits no cursors. Both over-fetch limit+1 for HasMore and share query/count.
// When req.WithCount is set, Total is the full filtered row count from a
// COUNT(*) wrap of BaseSQL. A stale cursor (order field changed) decodes to the
// first page. Time order values bind via FormatTime. Errors pass through
// MapError.
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
	args := append([]any(nil), q.Args...)

	if cursor != nil {
		if err := appendCursorPredicate(&buf, &args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, false, castLower); err != nil {
			return crud.Page[T]{}, err
		}
	}
	if err := appendOrderBy(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return crud.Page[T]{}, err
	}
	buf.WriteString(" LIMIT ?")
	args = append(args, limit+1)

	items, err := q.query(ctx, db, buf.String(), args)
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
	args := append([]any(nil), q.Args...)

	if err := appendOrderBy(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return crud.Page[T]{}, err
	}
	buf.WriteString(" LIMIT ?")
	args = append(args, limit+1)
	buf.WriteString(" OFFSET ?")
	args = append(args, req.Offset)

	items, err := q.query(ctx, db, buf.String(), args)
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

// resolveOrder maps the request Order (or DefaultOrder when zero) to a column,
// its CastLower flag, and a normalized direction by matching the order field
// against the columns in q.OrderFields. Membership in the allow-list is the
// injection guard (columns are store-authored constants, not quoted). An order
// field absent from the allow-list returns an error wrapping sdk.ErrInvalidInput.
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
func (q ListQuery[T]) markPrev(ctx context.Context, db Querier, page *crud.Page[T], orderCol, direction string, castLower bool, limit int, cursor *crud.Cursor, encode func(T) (string, error)) error {
	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := append([]any(nil), q.Args...)

	if err := appendCursorPredicate(&buf, &args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, true, castLower); err != nil {
		return err
	}
	if err := appendOrderBy(&buf, orderCol, q.PK, direction, true, castLower); err != nil {
		return err
	}
	buf.WriteString(" LIMIT ?")
	args = append(args, limit)

	prev, err := q.query(ctx, db, buf.String(), args)
	if err != nil {
		return err
	}

	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}

	return crud.MarkPrevPage(page, prev, limit, encode)
}

// count returns the full filtered row count by wrapping BaseSQL in a COUNT(*)
// subquery with the same filter args — never the cursor/offset predicates,
// never capped by limit.
func (q ListQuery[T]) count(ctx context.Context, db Querier) (int64, error) {
	sql := "SELECT COUNT(*) FROM (" + q.BaseSQL + ") AS list_count"
	var total int64
	if err := db.QueryRow(ctx, sql, q.Args...).Scan(&total); err != nil {
		return 0, MapError(err)
	}
	return total, nil
}

// query runs sql and scans every row through scanRow.
func (q ListQuery[T]) query(ctx context.Context, db Querier, sql string, args []any) ([]T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()

	var items []T
	for rows.Next() {
		it, err := q.scanRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, MapError(err)
	}
	return items, nil
}

// scanRow reads one row into T: through the Scan callback when set, otherwise via
// the strict ScanStruct[T] struct-scan (nil-Scan requires T be a db-tagged row
// struct).
func (q ListQuery[T]) scanRow(rows *sql.Rows) (T, error) {
	if q.Scan != nil {
		return q.Scan(rows)
	}
	return ScanStruct[T](rows)
}

// appendCursorPredicate appends a keyset tuple-comparison predicate with `?`
// placeholders to buf and its two positional args (order value, pk) to args. It
// writes WHERE when buf holds no WHERE yet and AND otherwise. The operator comes
// from direction × forPrevious (keysetOperator); castLower wraps both sides of
// the order comparison in LOWER(). Time order values bind via FormatTime. Both
// orderCol and pkCol pass through QuoteIdentifier, so a non-identifier column or
// raw-expression pk fails loud with sdk.ErrInvalidInput before any SQL runs.
func appendCursorPredicate(buf *strings.Builder, args *[]any, orderCol, pkCol string, orderValue any, pk, direction string, forPrevious, castLower bool) error {
	quotedOrder, err := QuoteIdentifier(orderCol)
	if err != nil {
		return fmt.Errorf("order field: %w", err)
	}
	quotedPK, err := QuoteIdentifier(pkCol)
	if err != nil {
		return fmt.Errorf("pk field: %w", err)
	}

	if strings.Contains(strings.ToUpper(buf.String()), "WHERE") {
		buf.WriteString(" AND ")
	} else {
		buf.WriteString(" WHERE ")
	}

	operator := keysetOperator(direction, forPrevious)
	if castLower {
		fmt.Fprintf(buf, "(LOWER(%s), %s) %s (LOWER(?), ?)", quotedOrder, quotedPK, operator)
	} else {
		fmt.Fprintf(buf, "(%s, %s) %s (?, ?)", quotedOrder, quotedPK, operator)
	}

	*args = append(*args, bindOrderValue(orderValue), pk)
	return nil
}

// appendOrderBy appends an ORDER BY on the order column plus the pk tiebreaker.
// forPrevious flips the direction (the backward probe reverses the sort, then
// the caller reverses the rows back). castLower wraps the order column in
// LOWER(). The pk term is omitted when the order column already is the pk — but
// pkCol is validated through QuoteIdentifier UNCONDITIONALLY, so a bad pk fails
// on page 1 (where no cursor predicate is appended) exactly as it would later.
func appendOrderBy(buf *strings.Builder, orderCol, pkCol, direction string, forPrevious, castLower bool) error {
	quotedOrder, err := QuoteIdentifier(orderCol)
	if err != nil {
		return fmt.Errorf("order field: %w", err)
	}
	quotedPK, err := QuoteIdentifier(pkCol)
	if err != nil {
		return fmt.Errorf("pk field: %w", err)
	}

	dir := resolvedDirection(direction, forPrevious)

	orderExpr := quotedOrder
	if castLower {
		orderExpr = "LOWER(" + quotedOrder + ")"
	}

	fmt.Fprintf(buf, " ORDER BY %s %s", orderExpr, dir)
	if orderCol != pkCol {
		fmt.Fprintf(buf, ", %s %s", quotedPK, dir)
	}
	return nil
}

// keysetOperator picks the comparison operator for a keyset predicate from the
// sort direction and traversal direction. Forward paging in ASC order and
// backward paging in DESC order both advance with ">"; the other two combos use
// "<". This is the operator half of the direction × forPrevious truth table.
func keysetOperator(direction string, forPrevious bool) string {
	ascending := direction != crud.DESC
	if ascending != forPrevious {
		return ">"
	}
	return "<"
}

// resolvedDirection returns the ORDER BY direction for a traversal: the request
// direction as-is for forward paging, flipped for a backward probe. An unset
// direction defaults to ASC.
func resolvedDirection(direction string, forPrevious bool) string {
	dir := crud.ASC
	if direction == crud.DESC {
		dir = crud.DESC
	}
	if forPrevious {
		if dir == crud.ASC {
			return crud.DESC
		}
		return crud.ASC
	}
	return dir
}

// bindOrderValue prepares a cursor's restored order value for a positional bind:
// a time.Time renders through FormatTime to match the fixed-width TEXT storage
// timestamps are compared as; every other type binds unchanged.
func bindOrderValue(v any) any {
	if t, ok := v.(time.Time); ok {
		return FormatTime(t)
	}
	return v
}
