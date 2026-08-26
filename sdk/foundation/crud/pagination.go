package crud

import (
	"fmt"
	"strconv"
	"strings"
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

// MapPage converts a Page's item type by applying fn to each item, copying
// every pagination field (NextCursor, HasMore, HasPrev, PreviousCursor, Total)
// unchanged. This is the row-struct→domain bridge: a store gets a
// Page[rowStruct] from the connector helper and returns crud.MapPage(p,
// toDomain).
func MapPage[T, U any](p Page[T], fn func(T) U) Page[U] {
	out := Page[U]{
		NextCursor:     p.NextCursor,
		HasMore:        p.HasMore,
		HasPrev:        p.HasPrev,
		PreviousCursor: p.PreviousCursor,
		Total:          p.Total,
	}
	if p.Items != nil {
		out.Items = make([]U, len(p.Items))
		for i, item := range p.Items {
			out.Items[i] = fn(item)
		}
	}
	return out
}

// MapPageErr is MapPage for a fallible mapper — the row-struct→domain bridge
// when the conversion VALIDATES (a stored vocabulary outside the domain's
// contract must fail loud, not enter the domain). It stops at the first error
// and returns it unwrapped, with a zero Page; on success every pagination
// field is copied unchanged exactly as MapPage does.
func MapPageErr[T, U any](p Page[T], fn func(T) (U, error)) (Page[U], error) {
	out := Page[U]{
		NextCursor:     p.NextCursor,
		HasMore:        p.HasMore,
		HasPrev:        p.HasPrev,
		PreviousCursor: p.PreviousCursor,
		Total:          p.Total,
	}
	if p.Items != nil {
		out.Items = make([]U, len(p.Items))
		for i, item := range p.Items {
			u, err := fn(item)
			if err != nil {
				return Page[U]{}, err
			}
			out.Items[i] = u
		}
	}
	return out, nil
}

// ListParams carries the raw transport-edge page params ParseListRequest folds
// into a ListRequest. The string fields are the untrusted query values
// (limit/cursor/offset/count); Limits is the resource's page-size vocabulary,
// applying the same effective default/max resolution as NormalizedLimit (zero
// fields fall back to the crud constants). DefaultStrategy applies when neither
// cursor nor offset params are present; "" means StrategyCursor. Hosts populate
// DefaultStrategy from an env-tagged config field (default:"cursor" via
// sdk/config), never from os.Getenv inside crud.
type ListParams struct {
	Limit  string
	Cursor string
	Offset string
	Count  string
	// Search is the already-extracted search term (the canonical v3 query key is
	// `q`). It is parsed like the rest: trimmed, with blank meaning "no search".
	//
	// Unlike a bad limit, the CONTENTS of a search term are never invalid — `%`,
	// `_`, `\`, and non-ASCII text are all legal things a human types — so this
	// parser never rejects a term. A term reaching a list that declares no
	// SearchFields is rejected at the STORE edge with sdk.ErrInvalidInput, rather
	// than silently returning an unfiltered page that looks like a search result.
	Search          string
	Limits          Limits
	DefaultStrategy Strategy
}

// ParseListRequest is the strict transport-edge parser for user-supplied page
// params (JSON query strings). An empty Limit yields the resource's effective
// default; a non-numeric, non-positive, or above-the-effective-max value is an
// error, never clamped — see the package doc's two-semantics rule. The effective
// default and max come from p.Limits (zero fields fall back to DefaultLimit /
// MaxLimit), the same resolution NormalizedLimit uses.
//
// Strategy is resolved from which param is present, never inferred from the
// offset value: an Offset param (even "0") selects StrategyOffset; a Cursor
// param selects StrategyCursor; both present is an error; neither falls back to
// p.DefaultStrategy ("" → StrategyCursor). A non-numeric or negative offset, or
// a non-bool count (strconv.ParseBool; empty = false), is rejected. See the
// package doc's strategy/count matrix.
func ParseListRequest(p ListParams) (ListRequest, error) {
	defaultLimit := p.Limits.Default
	if defaultLimit <= 0 {
		defaultLimit = DefaultLimit
	}
	maxLimit := p.Limits.Max
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}
	if defaultLimit > maxLimit {
		defaultLimit = maxLimit
	}

	limit := defaultLimit
	if p.Limit != "" {
		var err error
		limit, err = strconv.Atoi(p.Limit)
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

	if p.Cursor != "" && p.Offset != "" {
		return ListRequest{}, fmt.Errorf("cursor and offset are mutually exclusive")
	}

	strategy := p.DefaultStrategy
	if strategy == "" {
		strategy = StrategyCursor
	}
	switch {
	case p.Offset != "":
		strategy = StrategyOffset
	case p.Cursor != "":
		strategy = StrategyCursor
	}

	offset := 0
	if p.Offset != "" {
		var err error
		offset, err = strconv.Atoi(p.Offset)
		if err != nil {
			return ListRequest{}, fmt.Errorf("page offset conversion: %w", err)
		}
		if offset < 0 {
			return ListRequest{}, fmt.Errorf("offset value too small, must not be negative")
		}
	}

	withCount := false
	if p.Count != "" {
		var err error
		withCount, err = strconv.ParseBool(p.Count)
		if err != nil {
			return ListRequest{}, fmt.Errorf("page count conversion: %w", err)
		}
	}

	return ListRequest{
		Limit:     limit,
		Cursor:    p.Cursor,
		Offset:    offset,
		WithCount: withCount,
		Strategy:  strategy,
		Search:    strings.TrimSpace(p.Search),
	}, nil
}
