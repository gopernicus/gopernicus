package pgxdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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
// passed to req.NormalizedLimit; the zero value preserves the list-constant
// defaults.
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
	Args         pgx.NamedArgs
	OrderFields  map[string]list.OrderField
	DefaultOrder list.Order
	// SearchFields is the allow-list of searchable text columns — the twin of
	// OrderFields (crud-search-upstream T2). Empty means the list is NOT
	// searchable: a blank Request.Search still works, and a NON-blank one is
	// sdk.ErrInvalidInput rather than a silently unfiltered page.
	SearchFields []list.SearchField
	// FixedOrder is a store-authored ORDER BY expression (without the keyword)
	// for a list whose order is not the caller's to choose: composite columns,
	// NULLS LAST, computed sort keys — "closing_date DESC NULLS LAST, name ASC,
	// id ASC". It is trusted store text, like BaseSQL, never request data, and
	// is written verbatim over projected fields; the store includes its own PK
	// tiebreak in it — List
	// does not append one. When set, OrderFields/DefaultOrder are not consulted
	// (OrderFields also set is a programming error reported as
	// sdk.ErrInvalidInput), a request carrying an Order is sdk.ErrInvalidInput,
	// and the list is offset-only — a cursor-strategy request is
	// sdk.ErrInvalidInput, because a keyset predicate over an arbitrary
	// expression is not derivable. The zero value keeps the OrderFields path.
	FixedOrder   string
	PK           string
	Limits       list.Limits
	OrderValueOf func(row T, field string) any
	PKOf         func(row T) string
}

// List runs a paginated SELECT implementing the sdk/pkg/list list matrix over
// pgx.CollectRows + RowToStructByName. It validates the request, resolves the
// order against q.OrderFields, then switches on req.ResolvedStrategy() into one
// of two linear flows: listCursor appends the keyset tuple predicate (and, when
// a cursor is present, runs a reverse probe to fill HasPrev/PreviousCursor);
// listOffset appends LIMIT/OFFSET, derives HasMore from its own over-fetch, and
// emits no cursors. Both over-fetch limit+1 for HasMore and share collect/count.
// When req.WithCount is set, Total is the full filtered row count from a
// COUNT(*) wrap of BaseSQL. A stale cursor (order field changed) decodes to the
// first page. Errors pass through MapError.
//
// A ListQuery with FixedOrder takes the offset flow only, with its ORDER BY
// written verbatim; see the field doc for what it refuses.
func List[T any](ctx context.Context, db Querier, q ListQuery[T], req list.Request) (list.Page[T], error) {
	if err := req.Validate(); err != nil {
		return list.Page[T]{}, err
	}

	var (
		orderCol  string
		castLower bool
		direction string
	)
	if q.FixedOrder != "" {
		if err := q.checkFixedOrder(req); err != nil {
			return list.Page[T]{}, err
		}
	} else {
		var err error
		orderCol, castLower, direction, err = q.resolveOrder(req.Order)
		if err != nil {
			return list.Page[T]{}, err
		}
	}

	// The search predicate is folded into BaseSQL BEFORE the strategy switch, so
	// all FOUR query paths inherit it: the cursor page, the offset page, the
	// cursor strategy's reverse probe, and the COUNT(*) wrap.
	//
	// Appending it to a local per-page buffer instead — the way the cursor
	// predicate is appended — would be a real defect with two symptoms: WithCount
	// would report the UNFILTERED total (a page of 3 rows claiming 412 results),
	// and the reverse probe would derive HasPrev/PreviousCursor from rows the
	// search excluded.
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
// The copy is not cosmetic. ListQuery is passed BY VALUE, but its pgx.NamedArgs
// is a map — shared, not copied — so binding the reserved argument directly would
// leak it into the caller's query struct and into every later query built from
// it. cloneArgs gives this derivation its own map.
func (q ListQuery[T]) withSearch(term string) (ListQuery[T], error) {
	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	if strings.TrimSpace(term) == "" {
		wrapListSource(&buf)
		q.BaseSQL = buf.String()
		return q, nil
	}
	args := cloneArgs(q.Args)
	if err := AddSearchClause(&buf, args, q.SearchFields, term); err != nil {
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
	args := cloneArgs(q.Args)

	if cursor != nil {
		if err := ApplyCursorPagination(&buf, args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, false, castLower); err != nil {
			return list.Page[T]{}, err
		}
	}
	if err := AddOrderByClause(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return list.Page[T]{}, err
	}
	AddLimitClause(&buf, args, limit+1)

	items, err := q.collect(ctx, db, buf.String(), args)
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
// the offset arithmetic (see the crud strategy matrix). Under FixedOrder the
// ORDER BY is the store's expression verbatim (orderCol/direction are unused).
func (q ListQuery[T]) listOffset(ctx context.Context, db Querier, req list.Request, orderCol string, castLower bool, direction string) (list.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)

	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := cloneArgs(q.Args)

	if q.FixedOrder != "" {
		buf.WriteString(" ORDER BY " + q.FixedOrder)
	} else if err := AddOrderByClause(&buf, orderCol, q.PK, direction, false, castLower); err != nil {
		return list.Page[T]{}, err
	}
	AddLimitClause(&buf, args, limit+1)
	buf.WriteString(" OFFSET @" + offsetArg)
	args[offsetArg] = req.Offset

	items, err := q.collect(ctx, db, buf.String(), args)
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

// collect runs sql with args and scans every row into T via RowToStructByName.
func (q ListQuery[T]) collect(ctx context.Context, db Querier, sql string, args pgx.NamedArgs) ([]T, error) {
	return Collect[T](ctx, db, sql, args)
}

// checkFixedOrder enforces the FixedOrder rules before any SQL is built: the
// query must not also carry OrderFields (the two are mutually exclusive — a
// programming error, reported with the same posture as an order field absent
// from the allow-list), the request must not choose an Order, and the resolved
// strategy must be offset — a keyset predicate cannot be derived from an
// arbitrary ORDER BY expression, so the cursor flow is refused outright. Every
// refusal wraps sdk.ErrInvalidInput.
func (q ListQuery[T]) checkFixedOrder(req list.Request) error {
	if len(q.OrderFields) > 0 {
		return fmt.Errorf("FixedOrder and OrderFields are mutually exclusive: %w", sdk.ErrInvalidInput)
	}
	if req.Order.Field != "" {
		return fmt.Errorf("order field %q: the list order is fixed by the store: %w", req.Order.Field, sdk.ErrInvalidInput)
	}
	if req.ResolvedStrategy() != list.StrategyOffset {
		return fmt.Errorf("cursor strategy: a FixedOrder list is offset-only (no keyset predicate over a fixed ORDER BY expression): %w", sdk.ErrInvalidInput)
	}
	return nil
}

// resolveOrder maps the request Order (or DefaultOrder when zero) to a vetted
// column, its CastLower flag, and a normalized direction by matching the order
// field against the columns in q.OrderFields. An order field absent from the
// allow-list returns an error wrapping sdk.ErrInvalidInput.
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
// The probe includes the boundary, flips ORDER BY, fetches limit+1 rows, and
// restores forward order before applying the SDK helper.
func (q ListQuery[T]) markPrev(ctx context.Context, db Querier, page *list.Page[T], orderCol, direction string, castLower bool, limit int, cursor *list.Cursor, encode func(T) (string, error)) error {
	var buf strings.Builder
	buf.WriteString(q.BaseSQL)
	args := cloneArgs(q.Args)

	if err := ApplyCursorPagination(&buf, args, orderCol, q.PK, cursor.OrderValue, cursor.PK, direction, true, castLower); err != nil {
		return err
	}
	if err := AddOrderByClause(&buf, orderCol, q.PK, direction, true, castLower); err != nil {
		return err
	}
	AddLimitClause(&buf, args, limit+1)

	prev, err := q.collect(ctx, db, buf.String(), args)
	if err != nil {
		return err
	}

	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}

	return list.MarkPrevPage(page, prev, limit, encode)
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
