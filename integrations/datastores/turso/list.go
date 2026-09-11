package turso

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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
// list.MapPage(page, row.toDomain)); pass a callback only for a T that is not a
// plain row struct. OrderValueOf and PKOf supply cursor encoding. Limits is the
// resource's page-size vocabulary passed to req.NormalizedLimit; the zero value
// preserves the list-constant defaults.
//
// All list ordering and predicates run outside BaseSQL in a derived table.
// This keeps expressions over output aliases consistent on every page. Project
// every search/order/PK column using an unqualified output name; inner table aliases
// are not visible outside that SELECT. Include added columns in the row scanner.
// OrderValueOf must return the projected value and type used for ordering.
// SQL keyset ordering requires non-null order values and primary keys in every
// matching row; use a non-null projection or offset mode for nullable ordering.
type ListQuery[T any] struct {
	BaseSQL      string
	Args         []any
	OrderFields  map[string]list.OrderField
	DefaultOrder list.Order
	// SearchFields is the allow-list of searchable text columns — the twin of
	// OrderFields (crud-search-upstream T3), and the dialect sibling of the pgx
	// connector's field of the same name. Empty means the list is NOT searchable:
	// a blank Request.Search still works, and a NON-blank one is
	// sdk.ErrInvalidInput rather than a silently unfiltered page.
	SearchFields []list.SearchField
	PK           string
	Limits       list.Limits
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
func List[T any](ctx context.Context, db Querier, q ListQuery[T], req list.Request) (list.Page[T], error) {
	if err := req.Validate(); err != nil {
		return list.Page[T]{}, err
	}

	orderCol, castLower, direction, err := q.resolveOrder(req.Order)
	if err != nil {
		return list.Page[T]{}, err
	}

	// The search predicate is folded into BaseSQL BEFORE the strategy switch, so
	// all FOUR query paths inherit it: the cursor page, the offset page, the
	// cursor strategy's reverse probe, and the COUNT(*) wrap. Appending it to a
	// per-page buffer instead would make WithCount report the UNFILTERED total and
	// let the reverse probe derive HasPrev from rows the search excluded.
	searched, err := q.withSearch(req.Search)
	if err != nil {
		return list.Page[T]{}, err
	}

	if req.ResolvedStrategy() == list.StrategyOffset {
		return searched.listOffset(ctx, db, req, orderCol, castLower, direction)
	}
	return searched.listCursor(ctx, db, req, orderCol, castLower, direction)
}

// withSearch places BaseSQL in a projected outer scope and applies optional
// search. Even a blank term wraps the SELECT, so ORDER BY expressions cannot
// bind to an inner source column that an output alias transforms.
//
// The args slice is COPIED rather than appended to in place: ListQuery is passed
// by value, but a slice header shares its backing array, so an in-place append
// could scribble into the caller's array when it has spare capacity. The copy
// also preserves BaseSQL's own argument ORDER — search arguments land after the
// caller's and before any cursor/limit/offset arguments a later step appends.
func (q ListQuery[T]) withSearch(term string) (ListQuery[T], error) {
	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	if strings.TrimSpace(term) == "" {
		wrapListSource(&buf)
		q.BaseSQL = buf.String()
		return q, nil
	}
	args := append([]any(nil), q.Args...)
	if err := AddSearchClause(&buf, &args, q.SearchFields, term); err != nil {
		return ListQuery[T]{}, err
	}
	q.BaseSQL = buf.String()
	q.Args = args
	return q, nil
}

// listCursor is the keyset flow: decode the cursor (a nil cursor — first page or
// a stale token — skips the predicate and the reverse probe), over-fetch
// limit+1, TrimPage for HasMore/NextCursor, then the reverse probe for
// HasPrev/PreviousCursor when a cursor was present.
func (q ListQuery[T]) listCursor(ctx context.Context, db Querier, req list.Request, orderCol string, castLower bool, direction string) (list.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)

	var cursor *list.Cursor
	if req.Cursor != "" {
		var err error
		cursor, err = list.DecodeCursor(req.Cursor, orderCol)
		if err != nil {
			return list.Page[T]{}, fmt.Errorf("decode cursor: %w: %w", sdk.ErrInvalidInput, err)
		}
	}

	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := append([]any(nil), q.Args...)

	if cursor != nil {
		if err := appendCursorPredicate(&buf, &args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, false, castLower); err != nil {
			return list.Page[T]{}, err
		}
	}
	if err := appendOrderBy(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return list.Page[T]{}, err
	}
	buf.WriteString(" LIMIT ?")
	args = append(args, limit+1)

	items, err := q.query(ctx, db, buf.String(), args)
	if err != nil {
		return list.Page[T]{}, err
	}

	encode := func(row T) (string, error) {
		value := q.OrderValueOf(row, orderCol)
		if err := validateCursorValue(value, castLower); err != nil {
			return "", err
		}
		return list.EncodeCursor(orderCol, value, q.PKOf(row))
	}

	page, err := list.TrimPage(items, limit, encode)
	if err != nil {
		return list.Page[T]{}, err
	}

	if cursor != nil {
		if err := q.markPrev(ctx, db, &page, orderCol, direction, castLower, limit, cursor, encode); err != nil {
			return list.Page[T]{}, err
		}
	}

	if req.WithCount {
		total, err := q.count(ctx, db)
		if err != nil {
			return list.Page[T]{}, err
		}
		page.Total = &total
	}

	return page, nil
}

// listOffset is the LIMIT/OFFSET flow: same ORDER BY, LIMIT n+1 OFFSET off. It
// derives HasMore from its own over-fetch and sets HasPrev from Offset; it never
// encodes a cursor — NextCursor/PreviousCursor stay empty, and the caller does
// the offset arithmetic (see the crud strategy matrix).
func (q ListQuery[T]) listOffset(ctx context.Context, db Querier, req list.Request, orderCol string, castLower bool, direction string) (list.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)

	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := append([]any(nil), q.Args...)

	if err := appendOrderBy(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return list.Page[T]{}, err
	}
	buf.WriteString(" LIMIT ?")
	args = append(args, limit+1)
	buf.WriteString(" OFFSET ?")
	args = append(args, req.Offset)

	items, err := q.query(ctx, db, buf.String(), args)
	if err != nil {
		return list.Page[T]{}, err
	}

	page := list.Page[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.HasMore = true
	}
	page.HasPrev = req.Offset > 0

	if req.WithCount {
		total, err := q.count(ctx, db)
		if err != nil {
			return list.Page[T]{}, err
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
func (q ListQuery[T]) resolveOrder(order list.Order) (column string, castLower bool, direction string, err error) {
	if order.Field == "" {
		order = q.DefaultOrder
	}

	dir := list.ASC
	if order.Direction == list.DESC {
		dir = list.DESC
	}

	for _, of := range q.OrderFields {
		if of.Column == order.Field {
			return of.Column, of.CastLower, dir, nil
		}
	}
	return "", false, "", fmt.Errorf("unknown order field %q: %w", order.Field, sdk.ErrInvalidInput)
}

// markPrev runs the reverse probe for cursor mode and applies list.MarkPrevPage.
func (q ListQuery[T]) markPrev(ctx context.Context, db Querier, page *list.Page[T], orderCol, direction string, castLower bool, limit int, cursor *list.Cursor, encode func(T) (string, error)) error {
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
	args = append(args, limit+1)

	prev, err := q.query(ctx, db, buf.String(), args)
	if err != nil {
		return err
	}

	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}

	return list.MarkPrevPage(page, prev, limit, encode)
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

	items := make([]T, 0)
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

// appendCursorPredicate wraps the authored SELECT and applies a keyset
// predicate to its projected columns. Reverse probes include the boundary.
func appendCursorPredicate(buf *strings.Builder, args *[]any, orderCol, pkCol string, orderValue any, pk, direction string, forPrevious, castLower bool) error {
	if err := validateCursorValue(orderValue, castLower); err != nil {
		return err
	}
	quotedOrder, err := QuoteIdentifier(orderCol)
	if err != nil {
		return fmt.Errorf("order field: %w", err)
	}
	quotedPK, err := QuoteIdentifier(pkCol)
	if err != nil {
		return fmt.Errorf("pk field: %w", err)
	}

	beginListPredicate(buf)

	operator := keysetOperator(direction, forPrevious)
	if forPrevious {
		operator += "=" // Include the boundary among the previous page's records.
	}
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
	if orderCol != pkCol || castLower {
		fmt.Fprintf(buf, ", %s %s", quotedPK, dir)
	}
	return nil
}

// keysetOperator picks the comparison operator for a keyset predicate from the
// sort direction and traversal direction. Forward paging in ASC order and
// backward paging in DESC order both advance with ">"; the other two combos use
// "<". This is the operator half of the direction × forPrevious truth table.
func keysetOperator(direction string, forPrevious bool) string {
	ascending := direction != list.DESC
	if ascending != forPrevious {
		return ">"
	}
	return "<"
}

// resolvedDirection returns the ORDER BY direction for a traversal: the request
// direction as-is for forward paging, flipped for a backward probe. An unset
// direction defaults to ASC.
func resolvedDirection(direction string, forPrevious bool) string {
	dir := list.ASC
	if direction == list.DESC {
		dir = list.DESC
	}
	if forPrevious {
		if dir == list.ASC {
			return list.DESC
		}
		return list.ASC
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

// beginListPredicate preserves the authored SELECT's filters and projection.
// An outer WHERE composes with nested queries and OR without parsing SQL text.
func beginListPredicate(buf *strings.Builder) {
	wrapListSource(buf)
	buf.WriteString(" WHERE ")
}

// wrapListSource makes projected columns available to outer expressions.
func wrapListSource(buf *strings.Builder) {
	base := buf.String()
	buf.Reset()
	buf.WriteString("SELECT * FROM (\n")
	buf.WriteString(base)
	buf.WriteString("\n) AS list_source")
}

// validateCursorValue checks constraints known without inspecting a DB schema.
func validateCursorValue(value any, castLower bool) error {
	if value == nil {
		return fmt.Errorf("SQL keyset order values must not be null: %w", sdk.ErrInvalidInput)
	}
	if castLower {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("case-folded cursor order value must be a string: %w", sdk.ErrInvalidInput)
		}
	}
	return nil
}
