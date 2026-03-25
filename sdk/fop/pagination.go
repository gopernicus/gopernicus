package fop

import (
	"fmt"
	"strconv"
)

// PageStringCursor represents a paginated request with a limit and cursor position.
type PageStringCursor struct {
	Limit  int
	Cursor string
}

// Pagination holds pagination metadata returned alongside list results.
type Pagination struct {
	HasPrev        bool   `json:"has_prev,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
	NextCursor     string `json:"next_cursor,omitempty"`
	PageTotal      int    `json:"page_total,omitempty"`
}

// DefaultMaxLimit is the maximum page size when no explicit max is provided.
const DefaultMaxLimit = 100

// ParsePageStringCursor parses pagination parameters from query string values.
// Default limit is 25. maxLimit caps the allowed page size.
func ParsePageStringCursor(pageLimit string, cursor string, maxLimit int) (PageStringCursor, error) {
	if maxLimit <= 0 {
		maxLimit = DefaultMaxLimit
	}

	limit := 25

	if pageLimit != "" {
		var err error
		limit, err = strconv.Atoi(pageLimit)
		if err != nil {
			return PageStringCursor{}, fmt.Errorf("page limit conversion: %w", err)
		}
	}

	if limit <= 0 {
		return PageStringCursor{}, fmt.Errorf("rows value too small, must be larger than 0")
	}

	if limit > maxLimit {
		return PageStringCursor{}, fmt.Errorf("rows value too large, must be at most %d", maxLimit)
	}

	return PageStringCursor{
		Limit:  limit,
		Cursor: cursor,
	}, nil
}
