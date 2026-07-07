package crud

import (
	"fmt"
	"strconv"
)

// TrimPage trims an over-fetched result set to the requested limit and builds a
// Page. Callers fetch limit+1 records; when the set exceeds the limit, the
// extra record proves a next page exists, so the records are trimmed, HasMore
// is set, and NextCursor is encoded from the last record actually returned.
// encode must use the same order field the query ordered by.
func TrimPage[T any](records []T, limit int, encode func(T) (string, error)) (Page[T], error) {
	page := Page[T]{Items: records}

	if limit > 0 && len(records) > limit {
		page.Items = records[:limit]
		page.HasMore = true
		nextCursor, err := encode(page.Items[len(page.Items)-1])
		if err != nil {
			return Page[T]{}, fmt.Errorf("encode next cursor: %w", err)
		}
		page.NextCursor = nextCursor
	}

	return page, nil
}

// MarkPrevPage sets HasPrev and PreviousCursor on a Page from a reverse probe's
// results: any record proves a previous page exists, and a full window (len ==
// limit) means the previous page starts at the probe's first record. encode
// must use the same order field the query ordered by.
func MarkPrevPage[T any](p *Page[T], prevRecords []T, limit int, encode func(T) (string, error)) error {
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

// ParseListRequest is the strict transport-edge parser for user-supplied page
// params (JSON query strings). An empty limitStr yields DefaultLimit; a
// non-numeric, non-positive, or greater-than-maxLimit value is an error, never
// clamped — see the package doc's two-semantics rule. When maxLimit <= 0 it
// falls back to MaxLimit.
func ParseListRequest(limitStr, cursor string, maxLimit int) (ListRequest, error) {
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}

	limit := DefaultLimit

	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err != nil {
			return ListRequest{}, fmt.Errorf("page limit conversion: %w", err)
		}
	}

	if limit <= 0 {
		return ListRequest{}, fmt.Errorf("rows value too small, must be larger than 0")
	}

	if limit > maxLimit {
		return ListRequest{}, fmt.Errorf("rows value too large, must be at most %d", maxLimit)
	}

	return ListRequest{
		Limit:  limit,
		Cursor: cursor,
	}, nil
}
