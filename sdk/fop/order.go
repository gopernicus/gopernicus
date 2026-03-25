package fop

import (
	"fmt"
	"strings"
)

const (
	ASC  = "ASC"
	DESC = "DESC"
)

// OrderField describes a sortable column.
type OrderField struct {
	Column    string // DB column name, e.g. "created_at"
	CastLower bool   // wrap in LOWER() for case-insensitive text sorting
}

// Order represents a field and direction for ordering query results.
type Order struct {
	Field     string
	Direction string
}

// NewOrder constructs an Order value. If direction is not ASC or DESC, defaults to ASC.
func NewOrder(field string, direction string) Order {
	if direction != ASC && direction != DESC {
		return Order{Field: field, Direction: ASC}
	}
	return Order{Field: field, Direction: direction}
}

// ParseOrder parses a "field:direction" string into an Order value.
//
// fields maps API-facing field names to OrderField definitions.
// If orderBy is empty, defaultOrder is returned. Direction is case-insensitive.
func ParseOrder(fields map[string]OrderField, orderBy string, defaultOrder Order) (Order, error) {
	if orderBy == "" {
		return defaultOrder, nil
	}

	fieldName, dirRaw, hasDir := strings.Cut(orderBy, ":")
	fieldName = strings.TrimSpace(fieldName)
	direction := ASC

	if hasDir {
		dirStr := strings.ToUpper(strings.TrimSpace(dirRaw))
		if dirStr == ASC || dirStr == DESC {
			direction = dirStr
		} else {
			return Order{}, fmt.Errorf("unknown direction: %s", dirStr)
		}
	}

	of, ok := fields[fieldName]
	if !ok {
		return Order{}, fmt.Errorf("unknown order field: %s", fieldName)
	}

	return NewOrder(of.Column, direction), nil
}
