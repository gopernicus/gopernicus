package pgxdb

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ApplyCursorPagination adds a WHERE (or AND) clause for keyset cursor pagination
// using tuple comparison: (orderField, pkField) > (@cursor_order_value, @cursor_pk).
func ApplyCursorPagination[K any, O any](
	buf *bytes.Buffer,
	data pgx.NamedArgs,
	orderField string,
	pkField string,
	orderValue *O,
	keyValue *K,
	direction string,
	forPrevious bool,
) error {
	return ApplyCursorPaginationWithOptions(buf, data, orderField, pkField, orderValue, keyValue, direction, forPrevious, false)
}

// ApplyCursorPaginationWithOptions is like ApplyCursorPagination but supports
// LOWER() wrapping on the order field for case-insensitive text sorting.
func ApplyCursorPaginationWithOptions[K any, O any](
	buf *bytes.Buffer,
	data pgx.NamedArgs,
	orderField string,
	pkField string,
	orderValue *O,
	keyValue *K,
	direction string,
	forPrevious bool,
	castLower bool,
) error {
	if keyValue == nil || orderValue == nil {
		return nil
	}

	quotedOrder, err := QuoteIdentifier(orderField)
	if err != nil {
		return fmt.Errorf("invalid order field: %w", err)
	}
	quotedPK, err := QuoteIdentifier(pkField)
	if err != nil {
		return fmt.Errorf("invalid pk field: %w", err)
	}

	needsWhere := !strings.Contains(buf.String(), "WHERE")
	if needsWhere {
		buf.WriteString(" WHERE ")
	} else {
		buf.WriteString(" AND ")
	}

	operator := determineOperator(direction, forPrevious)

	if castLower {
		fmt.Fprintf(buf, "(LOWER(%s), %s) %s (LOWER(@cursor_order_value), @cursor_pk)", quotedOrder, quotedPK, operator)
	} else {
		fmt.Fprintf(buf, "(%s, %s) %s (@cursor_order_value, @cursor_pk)", quotedOrder, quotedPK, operator)
	}

	data["cursor_order_value"] = *orderValue
	data["cursor_pk"] = *keyValue

	return nil
}

func determineOperator(direction string, forPrevious bool) string {
	operator := ">"
	if forPrevious {
		operator = "<"
	}
	if direction == "DESC" && !forPrevious {
		operator = "<"
	} else if direction == "DESC" && forPrevious {
		operator = ">"
	}
	return operator
}
