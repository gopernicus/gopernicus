package pgxdb

import (
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk/crud"
)

// The cursor-arg names bound by ApplyCursorPagination. They are fixed because a
// single keyset predicate ever appears in one query — the tuple comparison
// carries both the order value and the pk under one pair of names.
const (
	cursorOrderValueArg = "cursor_order_value"
	cursorPKArg         = "cursor_pk"
	limitArg            = "limit"
	offsetArg           = "offset"
)

// normalizeOrderValue prepares a cursor's restored order value for binding into
// NamedArgs. The codec hands back a time.Time in whatever zone it parsed; pgx
// compares TIMESTAMPTZ in UTC, so the time value is normalized to UTC here.
// Every other type (int64, string, float64, bool) binds unchanged.
func normalizeOrderValue(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.UTC()
	}
	return v
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
// direction as-is for forward paging, flipped for a backward (previous-page)
// probe. An unset direction defaults to ASC.
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

// ApplyCursorPagination appends a keyset tuple-comparison predicate to buf and
// binds its two named args (@cursor_order_value, @cursor_pk). It writes WHERE
// when buf holds no WHERE clause yet and AND otherwise. The operator comes from
// direction × forPrevious (keysetOperator); castLower wraps both sides of the
// order comparison in LOWER() for case-insensitive text sorting. orderCol and
// pkCol are quoted via QuoteIdentifier; the order value is UTC-normalized.
func ApplyCursorPagination(buf *strings.Builder, args pgx.NamedArgs, orderCol, pkCol string, orderValue any, pk, direction string, forPrevious, castLower bool) error {
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
		fmt.Fprintf(buf, "(LOWER(%s), %s) %s (LOWER(@%s), @%s)", quotedOrder, quotedPK, operator, cursorOrderValueArg, cursorPKArg)
	} else {
		fmt.Fprintf(buf, "(%s, %s) %s (@%s, @%s)", quotedOrder, quotedPK, operator, cursorOrderValueArg, cursorPKArg)
	}

	args[cursorOrderValueArg] = normalizeOrderValue(orderValue)
	args[cursorPKArg] = pk
	return nil
}

// AddOrderByClause appends an ORDER BY on the order column plus the pk
// tiebreaker for a stable total order. forPrevious flips the direction (the
// backward probe reverses the sort, then the caller reverses the rows back).
// castLower wraps the order column in LOWER(). The pk term is omitted when the
// order column already is the pk.
func AddOrderByClause(buf *strings.Builder, orderCol, pkCol, direction string, forPrevious, castLower bool) error {
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

// AddLimitClause appends LIMIT @limit to buf and binds the limit arg.
func AddLimitClause(buf *strings.Builder, args pgx.NamedArgs, limit int) {
	buf.WriteString(" LIMIT @" + limitArg)
	args[limitArg] = limit
}
