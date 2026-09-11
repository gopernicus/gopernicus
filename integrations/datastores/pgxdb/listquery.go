package pgxdb

import (
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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
	ascending := direction != list.DESC
	if ascending != forPrevious {
		return ">"
	}
	return "<"
}

// resolvedDirection returns the ORDER BY direction for a traversal: the request
// direction as-is for forward paging, flipped for a backward (previous-page)
// probe. An unset direction defaults to ASC.
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

// ApplyCursorPagination wraps the complete SELECT in buf and adds an outer
// keyset predicate, binding @cursor_order_value and @cursor_pk. Call it before
// final ORDER BY / LIMIT / OFFSET. orderCol and pkCol must be projected,
// unqualified output names. castLower applies LOWER to both order values.
// Forward comparisons exclude the boundary; reverse probes include it and must
// fetch limit+1 rows in reverse order before calling list.MarkPrevPage.
func ApplyCursorPagination(buf *strings.Builder, args pgx.NamedArgs, orderCol, pkCol string, orderValue any, pk, direction string, forPrevious, castLower bool) error {
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
// order column already is the pk and castLower is false.
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
	if orderCol != pkCol || castLower {
		fmt.Fprintf(buf, ", %s %s", quotedPK, dir)
	}
	return nil
}

// AddLimitClause appends LIMIT @limit to buf and binds the limit arg.
func AddLimitClause(buf *strings.Builder, args pgx.NamedArgs, limit int) {
	buf.WriteString(" LIMIT @" + limitArg)
	args[limitArg] = limit
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
