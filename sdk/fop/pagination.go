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

// TrimPage trims an over-fetched result set to the requested limit and builds
// pagination metadata. Callers fetch limit+1 (or more) records; when the set
// exceeds the limit, the extra records prove a next page exists, so the
// records are trimmed and NextCursor is encoded from the last record actually
// returned. When the set fits within the limit, NextCursor is empty.
//
// This is the single owner of the trim-and-encode pagination policy — the
// generated Repository.List methods and bridge-layer PostfilterLoop both
// build on it. encode must encode with the same order field the query used.
func TrimPage[T any](records []T, limit int, encode func(T) (string, error)) ([]T, Pagination, error) {
	page := Pagination{Limit: limit}
	out := records

	if limit > 0 && len(records) > limit {
		out = records[:limit]
		nextCursor, err := encode(out[len(out)-1])
		if err != nil {
			return nil, Pagination{}, fmt.Errorf("encode next cursor: %w", err)
		}
		page.NextCursor = nextCursor
	}

	page.PageTotal = len(out)
	return out, page, nil
}

// MarkPrevPage sets HasPrev and PreviousCursor on pagination from a reverse
// probe's results: any record proves a previous page exists, and a full
// window means the previous page starts at the probe's first record.
func MarkPrevPage[T any](p *Pagination, prevRecords []T, limit int, encode func(T) (string, error)) error {
	if len(prevRecords) == 0 {
		return nil
	}
	p.HasPrev = true
	if len(prevRecords) == limit {
		previousCursor, err := encode(prevRecords[0])
		if err != nil {
			return fmt.Errorf("encode previous cursor: %w", err)
		}
		p.PreviousCursor = previousCursor
	}
	return nil
}

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
