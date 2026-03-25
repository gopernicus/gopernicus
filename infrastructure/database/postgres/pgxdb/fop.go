package pgxdb

import (
	"bytes"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/sdk/fop"
)

// CursorConfig holds configuration for cursor-based pagination with order field validation.
// The Cursor field is a base64-encoded token produced by fop.EncodeCursor.
type CursorConfig struct {
	Cursor     string
	OrderField string // Expected order field (from the current request)
	PKField    string
	Direction  string // fop.ASC or fop.DESC
	Limit      int
	CastLower  bool // wrap order field in LOWER() for case-insensitive text sorting
}

// ApplyCursorPaginationFromToken decodes a cursor token via fop.DecodeCursor and
// applies keyset pagination to the query buffer. If the cursor's order field doesn't
// match the expected field, the cursor is silently ignored (stale cursor = first page).
func ApplyCursorPaginationFromToken(
	buf *bytes.Buffer,
	data pgx.NamedArgs,
	config CursorConfig,
	forPrevious bool,
) error {
	if config.Cursor == "" {
		return nil
	}

	cursor, err := fop.DecodeCursor(config.Cursor, config.OrderField)
	if err != nil {
		return fmt.Errorf("decode cursor: %w", err)
	}

	// Stale cursor (order field changed) — treat as first page.
	if cursor == nil {
		return nil
	}

	return ApplyCursorPaginationWithOptions(
		buf, data,
		config.OrderField, config.PKField,
		&cursor.OrderValue, &cursor.PK,
		config.Direction, forPrevious,
		config.CastLower,
	)
}

// AddOrderByClause appends an ORDER BY clause to the buffer.
// The primary key is used as a secondary sort for stable pagination.
func AddOrderByClause(buf *bytes.Buffer, orderField, pkField, direction string, forPrevious bool) error {
	return AddOrderByClauseWithOptions(buf, orderField, pkField, direction, forPrevious, false)
}

// AddOrderByClauseWithOptions is like AddOrderByClause but supports
// LOWER() wrapping on the order field for case-insensitive text sorting.
func AddOrderByClauseWithOptions(buf *bytes.Buffer, orderField, pkField, direction string, forPrevious bool, castLower bool) error {
	quotedOrderField, err := QuoteIdentifier(orderField)
	if err != nil {
		return fmt.Errorf("invalid order field name: %w", err)
	}
	quotedPKField, err := QuoteIdentifier(pkField)
	if err != nil {
		return fmt.Errorf("invalid pk field name: %w", err)
	}

	actualDirection := direction
	if forPrevious {
		if direction == fop.ASC {
			actualDirection = fop.DESC
		} else {
			actualDirection = fop.ASC
		}
	}

	orderExpr := quotedOrderField
	if castLower {
		orderExpr = fmt.Sprintf("LOWER(%s)", quotedOrderField)
	}

	buf.WriteString(fmt.Sprintf(" ORDER BY %s %s", orderExpr, actualDirection))

	if orderField != pkField {
		buf.WriteString(fmt.Sprintf(", %s %s", quotedPKField, actualDirection))
	}

	return nil
}

// AddLimitClause appends a LIMIT clause to the buffer and sets the named arg.
// Defaults to 50 if limit is <= 0 to prevent unbounded queries.
func AddLimitClause(limit int, data pgx.NamedArgs, buf *bytes.Buffer) {
	if limit <= 0 {
		limit = 50
	}
	buf.WriteString(" LIMIT @limit")
	data["limit"] = limit
}

// AliasedOrderField returns a table-qualified field name for use in queries with aliases.
func AliasedOrderField(field string, alias string) string {
	return fmt.Sprintf("%s.%s", alias, field)
}
