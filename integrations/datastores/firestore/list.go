package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// scanPageSize is the MINIMUM number of documents one underlying pull fetches
// while a PostFilter is in play. A postfiltered page cannot be answered by a
// single limit+1 query — the server does not know the predicate — so List pulls
// successive pages until it has limit+1 matches or the population runs out.
// The pull size is max(want, scanPageSize): big enough that a sparse predicate
// does not turn into one round trip per match, small enough that a dense one
// over-reads at most a few dozen documents. Documents are billed as they are
// read, so this constant is a cost knob, not a correctness one.
const scanPageSize = 50

// ListQuery describes one paginated Firestore query for List (C-D6). It is the
// Firestore twin of the pgxdb/turso ListQuery, with documents and query values
// where those have SQL text and placeholders.
//
// Query is the BASE query — collection plus any Where filters — and must carry
// NO OrderBy, Limit, Offset, or cursor of its own: List owns all four, and a
// pre-set order would either duplicate its clauses or silently take precedence
// over the requested one. OrderFields is the aggregate's allow-list, resolved
// against the request Order by document field path (the value "__name__", i.e.
// gcfs.DocumentID, orders by document id); DefaultOrder applies when the
// request Order is zero. PK is the sortable tiebreaker/cursor field; an empty
// PK means the document id. Limits is the resource's page-size vocabulary
// passed to req.NormalizedLimit; the zero value keeps the crud defaults.
//
// Decode, OrderValueOf, and PKOf are REQUIRED (a nil one is sdk.ErrInvalidInput
// on the first call rather than a nil-map panic on page two):
//
//   - Decode turns a document snapshot into T. Firestore has no struct-scan
//     that can also see the document id, so every store writes this.
//   - OrderValueOf returns the row's value for an order field. It must return a
//     type the vendor accepts as a query cursor value — time.Time, string,
//     int64, float64, bool, or (for the document-id field) the id string — and
//     the same type the stored field holds, or the keyset predicate silently
//     compares across types. Firestore returns timestamps as UTC time.Time at
//     microsecond precision, which is exactly what TruncateTime writes.
//   - PKOf returns the row's PK value (the document id when PK is empty).
//
// PostFilter is R4's client-side predicate: nil means none, non-nil makes every
// path page-fill in Go (see the PostFilter section on List). Search is composed
// BY THE STORE into PostFilter from crud.MatchesSearch and its SearchFields —
// this helper never interprets req.Search, and refuses a non-blank one when no
// PostFilter is set rather than answering a search with an unfiltered page.
type ListQuery[T any] struct {
	Query        gcfs.Query
	OrderFields  map[string]crud.OrderField
	DefaultOrder crud.Order
	PK           string
	Limits       crud.Limits
	Decode       func(*gcfs.DocumentSnapshot) (T, error)
	OrderValueOf func(row T, field string) any
	PKOf         func(row T) string
	PostFilter   func(T) bool
}

// List runs a paginated query with the same observable semantics as pgxdb.List
// and turso.List, over Firestore's document API. It validates the request,
// resolves the order against q.OrderFields, then switches on
// req.ResolvedStrategy() into one of two flows: listCursor orders by the
// resolved field plus the PK and pages with an exclusive StartAfter tuple (and,
// when a cursor is present, runs a reverse probe for HasPrev/PreviousCursor);
// listOffset uses Offset/Limit, derives HasMore from its own over-fetch, and
// emits no cursors. Both over-fetch limit+1. A stale cursor (order field
// changed) decodes to the first page, exactly as the SQL connectors do.
//
// # The keyset tuple
//
// Every query orders by (order field, PK) in the requested direction, emitted
// ONCE when the two are the same field, and the cursor is the matching
// StartAfter tuple. Firestore requires exactly one cursor value per OrderBy
// clause, so the two counts are derived from one decision and cannot drift.
// When the PK is the document id the cursor value is the document ID STRING
// relative to the query's collection — the vendor resolves it to a reference
// itself (query.go, fieldValuesToCursorValues), so no DocumentRef is needed.
//
// # Ordering and indexes
//
// Ordering by a field while filtering on another needs a COMPOSITE INDEX in
// production; the emulator has no index registry and enforces none, so an
// emulator-green list proves nothing about production's FAILED_PRECONDITION.
// A missing index surfaces as *MissingIndexError (errors.Is ErrMissingIndex,
// sdk.ErrUnavailable) carrying the console URL. Every order direction a store
// serves — and the reversed direction the HasPrev probe issues, which needs the
// SAME composite index with all directions flipped — belongs in that store's
// index manifest (C-D7). Firestore orders strings by UTF-8 bytes, which is the
// byte order pgx pins with COLLATE "C" and turso gets from SQLite's BINARY
// collation; crud.OrderField.CastLower has no Firestore analogue and is
// REFUSED (sdk.ErrInvalidInput) rather than silently ignored.
//
// Order by a field EVERY document in the population has. An OrderBy clause
// drops documents that lack the field entirely (they are not in that index),
// and a document whose field is NULL sorts before every other value but cannot
// be addressed by a crud.Cursor — a cursor carries the DECODED order value, and
// an absent model decodes to a Go zero (time.Time{}), which Firestore compares
// as a timestamp rather than as null. Page-fill resumes are unaffected (they
// carry the snapshot, not the value), but a page BOUNDARY landing inside a null
// run cannot be resumed exactly. Stores write optional timestamps through
// NullTime and do not order by them.
//
// # WithCount
//
// Without a PostFilter the total is the server-side count aggregation over the
// ORDERED query — the whole population the page can traverse, never the page
// itself. Ordered rather than base because Firestore's OrderBy drops documents
// that lack the ordered field, so a base-query count would promise rows no
// cursor can reach. Inside a
// transaction or a ReadSnapshot the aggregation is unavailable
// (ErrCountInTransaction), so List falls back to counting by iterating the same
// population; that keeps a snapshot-bound count consistent with the page it
// accompanies, at O(population) document reads. With a PostFilter the count is
// always the iterate-and-filter one, for the same reason a search count cannot
// be a server aggregation: the server does not know the predicate.
//
// # PostFilter
//
// A PostFilter makes every read path a page-fill loop: successive underlying
// pages of max(want, scanPageSize) documents are pulled, decoded, and filtered
// until want matches are collected or the population is exhausted. HasMore and
// NextCursor still come from crud.TrimPage over limit+1 MATCHES, so NextCursor
// encodes the last RETURNED match — never the extra match that proved HasMore,
// and never the last document scanned. The reverse probe collects up to limit
// matches the same way, and an offset skips exactly n MATCHES rather than n
// scanned documents. The cost is O(scanned), not O(returned): R4 restricts this
// to parent-scoped lists for that reason.
//
// Iterator errors are mapped through MapError at the iteration boundary and
// every iterator is stopped; iterator.Done is consumed as the loop terminator.
func List[T any](ctx context.Context, r Reader, q ListQuery[T], req crud.ListRequest) (crud.Page[T], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[T]{}, err
	}
	if err := q.validate(req); err != nil {
		return crud.Page[T]{}, err
	}

	field, direction, err := q.resolveOrder(req.Order)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		return q.listOffset(ctx, r, req, field, direction)
	}
	return q.listCursor(ctx, r, req, field, direction)
}

// validate rejects a ListQuery that cannot answer this request before any
// document is read: a missing callback (a wiring bug, so it fails on page one
// rather than on the first cursor), and a search term against a list with no
// PostFilter to apply it — the turso rule ("a list that declares nothing
// searchable must not answer a search with an unfiltered page") in Firestore's
// vocabulary, where the predicate is Go code rather than a LIKE clause.
func (q ListQuery[T]) validate(req crud.ListRequest) error {
	switch {
	case q.Decode == nil:
		return fmt.Errorf("firestore: ListQuery requires a Decode function: %w", sdk.ErrInvalidInput)
	case q.OrderValueOf == nil:
		return fmt.Errorf("firestore: ListQuery requires an OrderValueOf function: %w", sdk.ErrInvalidInput)
	case q.PKOf == nil:
		return fmt.Errorf("firestore: ListQuery requires a PKOf function: %w", sdk.ErrInvalidInput)
	case strings.TrimSpace(req.Search) != "" && q.PostFilter == nil:
		return fmt.Errorf("firestore: search is not supported by this list: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// resolveOrder maps the request Order (or DefaultOrder when zero) to a document
// field path and a vendor direction by matching against q.OrderFields.
// Membership in the allow-list is what keeps an arbitrary request field out of
// a query; an absent field is sdk.ErrInvalidInput. CastLower is refused rather
// than ignored: Firestore cannot fold case in an index, and quietly serving raw
// byte order under a case-insensitive request is the false green.
func (q ListQuery[T]) resolveOrder(order crud.Order) (string, gcfs.Direction, error) {
	if order.Field == "" {
		order = q.DefaultOrder
	}

	direction := gcfs.Asc
	if order.Direction == crud.DESC {
		direction = gcfs.Desc
	}

	for _, of := range q.OrderFields {
		if of.Column != order.Field {
			continue
		}
		if of.CastLower {
			return "", direction, fmt.Errorf("firestore: order field %q asks for case-folded ordering, which Firestore cannot do: %w", of.Column, sdk.ErrInvalidInput)
		}
		return of.Column, direction, nil
	}
	return "", direction, fmt.Errorf("unknown order field %q: %w", order.Field, sdk.ErrInvalidInput)
}

// pk returns the tiebreaker field path: the configured PK, or the document id.
func (q ListQuery[T]) pk() string {
	if q.PK == "" {
		return gcfs.DocumentID
	}
	return q.PK
}

// ordered returns the base query sorted by (order field, PK) in one direction,
// with the PK clause omitted when it IS the order field — Firestore rejects a
// repeated OrderBy on the same field, and a cursor must carry exactly one value
// per clause. reverse flips both directions for the HasPrev probe; the caller
// reverses the rows back afterwards.
func (q ListQuery[T]) ordered(field string, direction gcfs.Direction, reverse bool) gcfs.Query {
	if reverse {
		direction = flipDirection(direction)
	}
	out := q.Query.OrderBy(field, direction)
	if pk := q.pk(); pk != field {
		out = out.OrderBy(pk, direction)
	}
	return out
}

// cursorPosition converts a decoded crud.Cursor into the StartAfter tuple for
// the ordering ordered() built: (order value, pk), or just the pk when the
// order field IS the pk. The single-value form deliberately uses Cursor.PK
// rather than Cursor.OrderValue: both describe the same field, but PK is
// typed string by the crud grammar, which is what the vendor wants for a
// document-id cursor and what a string sort key holds anyway.
func (q ListQuery[T]) cursorPosition(cursor *crud.Cursor, field string) []any {
	if q.pk() == field {
		return []any{cursor.PK}
	}
	return []any{cursor.OrderValue, cursor.PK}
}

// listCursor is the keyset flow: decode the cursor (a nil cursor — first page or
// a stale token — skips both the StartAfter and the reverse probe), collect
// limit+1 rows, TrimPage for HasMore/NextCursor, then probe backwards for
// HasPrev/PreviousCursor when a cursor was present.
func (q ListQuery[T]) listCursor(ctx context.Context, r Reader, req crud.ListRequest, field string, direction gcfs.Direction) (crud.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)

	var cursor *crud.Cursor
	if req.Cursor != "" {
		var err error
		cursor, err = crud.DecodeCursor(req.Cursor, field)
		if err != nil {
			return crud.Page[T]{}, fmt.Errorf("decode cursor: %w: %w", sdk.ErrInvalidInput, err)
		}
	}

	forward := q.ordered(field, direction, false)
	if cursor != nil {
		forward = forward.StartAfter(q.cursorPosition(cursor, field)...)
	}

	items, err := q.window(ctx, r, forward, limit+1)
	if err != nil {
		return crud.Page[T]{}, err
	}

	encode := func(row T) (string, error) {
		return crud.EncodeCursor(field, q.OrderValueOf(row, field), q.PKOf(row))
	}

	page, err := crud.TrimPage(items, limit, encode)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if cursor != nil {
		if err := q.markPrev(ctx, r, &page, field, direction, limit, cursor, encode); err != nil {
			return crud.Page[T]{}, err
		}
	}

	if req.WithCount {
		total, err := q.count(ctx, r, field, direction)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.Total = &total
	}

	return page, nil
}

// listOffset is the Offset/Limit flow: the same ordering, skipping req.Offset
// rows. It derives HasMore from its own over-fetch and sets HasPrev from the
// offset; it never encodes a cursor — the caller does the offset arithmetic
// (the crud strategy matrix). Under a PostFilter the offset counts MATCHES,
// not scanned documents, so paging a searched list by offset lands where the
// caller expects; the skipped matches are still read and billed.
func (q ListQuery[T]) listOffset(ctx context.Context, r Reader, req crud.ListRequest, field string, direction gcfs.Direction) (crud.Page[T], error) {
	limit := req.NormalizedLimit(q.Limits)
	base := q.ordered(field, direction, false)

	var (
		items []T
		err   error
	)
	if q.PostFilter == nil {
		items, _, err = q.fetch(ctx, r, base.Offset(req.Offset).Limit(limit+1))
	} else {
		items, err = q.filteredOffset(ctx, r, base, req.Offset, limit+1)
	}
	if err != nil {
		return crud.Page[T]{}, err
	}

	page := crud.Page[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.HasMore = true
	}
	page.HasPrev = req.Offset > 0

	if req.WithCount {
		total, err := q.count(ctx, r, field, direction)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.Total = &total
	}

	return page, nil
}

// markPrev runs the reverse probe and applies crud.MarkPrevPage: the rows
// strictly BEFORE the incoming cursor in forward order are the rows the
// reversed sort returns after the SAME StartAfter tuple. Up to limit of them
// are fetched (matches, under a PostFilter), restored to forward order, and
// handed to crud.MarkPrevPage — a full window supplies PreviousCursor, a
// partial one only HasPrev. A one-row existence probe would answer HasPrev but
// could not address the previous page.
func (q ListQuery[T]) markPrev(ctx context.Context, r Reader, page *crud.Page[T], field string, direction gcfs.Direction, limit int, cursor *crud.Cursor, encode func(T) (string, error)) error {
	backward := q.ordered(field, direction, true).StartAfter(q.cursorPosition(cursor, field)...)

	prev, err := q.window(ctx, r, backward, limit)
	if err != nil {
		return err
	}

	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}

	return crud.MarkPrevPage(page, prev, limit, encode)
}

// window returns up to want rows from base: one query when there is no
// PostFilter, the page-fill loop when there is.
func (q ListQuery[T]) window(ctx context.Context, r Reader, base gcfs.Query, want int) ([]T, error) {
	if q.PostFilter == nil {
		rows, _, err := q.fetch(ctx, r, base.Limit(want))
		return rows, err
	}

	matches := make([]T, 0, want)
	err := q.scan(ctx, r, base, want, func(row T) bool {
		if !q.PostFilter(row) {
			return true
		}
		matches = append(matches, row)
		return len(matches) < want
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// filteredOffset is the offset flow's page-fill: discard exactly offset
// MATCHES, then collect want of them.
func (q ListQuery[T]) filteredOffset(ctx context.Context, r Reader, base gcfs.Query, offset, want int) ([]T, error) {
	skipped := 0
	matches := make([]T, 0, want)
	err := q.scan(ctx, r, base, want, func(row T) bool {
		if !q.PostFilter(row) {
			return true
		}
		if skipped < offset {
			skipped++
			return true
		}
		matches = append(matches, row)
		return len(matches) < want
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// count returns the size of the whole filtered population — never the page,
// never bounded by the request limit.
//
// It counts the ORDERED query, not the bare base query, because those are two
// different populations in Firestore: an OrderBy clause EXCLUDES every document
// that does not have the ordered field (there is no NULLS LAST — an absent
// field means an absent index entry). The page traverses the ordered query, so
// a count over the base query would report a total the caller can never page
// to. Total therefore describes the population this list can actually reach.
//
// The server-side aggregation answers it in one round trip, but only outside a
// transaction: inside one (including a ReadSnapshot, which is a read-only
// transaction) the vendor exposes no transaction-guarded aggregation and the
// Reader refuses with ErrCountInTransaction. Rather than failing a snapshot-
// bound List that asked for a count, this falls back to counting by iterating
// the same population the page came from, so the count agrees with the page's
// snapshot. A PostFilter takes the iterating path unconditionally: the server
// cannot evaluate a Go predicate.
func (q ListQuery[T]) count(ctx context.Context, r Reader, field string, direction gcfs.Direction) (int64, error) {
	ordered := q.ordered(field, direction, false)

	if q.PostFilter == nil {
		total, err := r.Count(ctx, ordered)
		if err == nil {
			return total, nil
		}
		if !errors.Is(err, ErrCountInTransaction) {
			return 0, err
		}
	}

	var total int64
	err := q.scan(ctx, r, ordered, scanPageSize, func(row T) bool {
		if q.PostFilter == nil || q.PostFilter(row) {
			total++
		}
		return true
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// scan walks the population of base in its own order, pulling pages of
// max(want, scanPageSize) documents and calling visit with each decoded row
// until visit returns false or a short page proves the population is exhausted.
//
// Each pull resumes with a StartAfter on the last SNAPSHOT scanned (not the
// last row visit accepted), which is what makes the loop advance under a
// predicate that rejects a whole page. The cursor is the snapshot itself, which
// the vendor accepts and resolves against the query's own order clauses: it is
// the document the server already positioned, so resuming cannot depend on the
// decoded row round-tripping its order value. That difference is load-bearing —
// a NULL order field decodes to some Go zero value (a Decode that maps a null
// timestamp to time.Time{}, say), and a StartAfter built from that zero would
// jump the cursor to the wrong place and silently drop documents. Cursors a
// caller receives still come from OrderValueOf/PKOf: those describe a row, and
// a row is what the caller sends back.
func (q ListQuery[T]) scan(ctx context.Context, r Reader, base gcfs.Query, want int, visit func(T) bool) error {
	pageSize := max(want, scanPageSize)

	page := base
	for {
		rows, snaps, err := q.fetch(ctx, r, page.Limit(pageSize))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !visit(row) {
				return nil
			}
		}
		if len(rows) < pageSize {
			return nil
		}
		page = base.StartAfter(snaps[len(snaps)-1])
	}
}

// fetch runs one query and decodes every document it returns, returning the
// decoded rows AND the snapshots they came from, positionally. The snapshots
// are what a page-fill scan resumes after (see scan); callers that only page
// one query discard them. The iterator is always stopped, iterator.Done is
// consumed as the loop terminator, and any other iteration error — including a
// transaction's read-after-write refusal and the missing-index
// FAILED_PRECONDITION, both of which the vendor defers to Next — goes through
// MapError here, at the iteration boundary.
//
// The row slice is allocated even when the query matches nothing, so an empty
// page marshals "items":[] and never "items":null on every path, not just the
// ones crud.TrimPage normalizes.
func (q ListQuery[T]) fetch(ctx context.Context, r Reader, query gcfs.Query) ([]T, []*gcfs.DocumentSnapshot, error) {
	it := r.Documents(ctx, query)
	defer it.Stop()

	rows := []T{}
	var snaps []*gcfs.DocumentSnapshot
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return rows, snaps, nil
		}
		if err != nil {
			return nil, nil, MapError(err)
		}
		row, err := q.Decode(snap)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
		snaps = append(snaps, snap)
	}
}

// flipDirection reverses a sort direction for the backward probe.
func flipDirection(d gcfs.Direction) gcfs.Direction {
	if d == gcfs.Desc {
		return gcfs.Asc
	}
	return gcfs.Desc
}
