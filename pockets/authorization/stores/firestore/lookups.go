package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// lookupPageSize is the number of documents ONE keyset stream pulls per physical
// page while it is being merged. The logical result is DISTINCT resource ids, so
// a page of documents can yield anywhere between one id and lookupPageSize of
// them; the merge keeps pulling pages until it has enough distinct ids or every
// stream is exhausted. Like the connector's scan page size this is a cost knob,
// not a correctness one — a smaller limit shrinks it (see idStream.pageSize).
const lookupPageSize = 50

// The complexity of ONE streamed keyset lookup query, which decides its chunk
// budget. Each disjunct carries four filters — the resource type, the relation,
// the subject key, and the exclusive resource_id cursor — beside two sort orders
// (resource_id then the document name). The cursor filter is counted even when
// `after` is empty, so the chunking of a lookup does not change between the
// first page and the next.
const (
	lookupFiltersPerDisjunct = 4
	lookupSortOrders         = 2
)

// The complexity of ONE descendant hop: three filters per disjunct (resource
// type, relation, subject key) and no sort order, because the closure is sorted
// in Go.
const descendantFiltersPerDisjunct = 3

// lookupChunkBudget and descendantChunkBudget are those complexities resolved
// against both vendor caps (see maxChunk). They are variables only because the
// arithmetic is not a constant expression; chunking_test.go pins their values.
var (
	lookupChunkBudget     = maxChunk(lookupFiltersPerDisjunct, lookupSortOrders)
	descendantChunkBudget = maxChunk(descendantFiltersPerDisjunct, 0)
)

// idStream is ONE chunk query of a keyset lookup, read as an ordered stream of
// resource ids. The query is ordered by (resource_id, __name__) and starts
// strictly after the caller's cursor, so the stream is already in the port's
// contractual raw-byte order; the merge below k-way merges several of them.
//
// Two documents of one stream commonly carry the SAME resource_id (one resource
// matched by two relations, or by two of the subject's reached states), so the
// stream advances document by document and the MERGE deduplicates. The physical
// page cursor is the document snapshot the server positioned, never the decoded
// id — resuming after an id would skip the rest of a duplicate run.
type idStream struct {
	reader   firestoredb.Reader
	base     gcfs.Query
	pageSize int

	buf  []string
	pos  int
	last *gcfs.DocumentSnapshot
	done bool
}

// newIDStream builds one chunk stream. after is the port's EXCLUSIVE cursor: an
// empty after starts at the beginning, and the inequality is the query's first
// order clause as Firestore requires.
func newIDStream(reader firestoredb.Reader, q gcfs.Query, after string, limit int) *idStream {
	if after != "" {
		q = q.Where("resource_id", ">", after)
	}
	size := lookupPageSize
	if limit > 0 && limit < size {
		size = limit
	}
	return &idStream{
		reader:   reader,
		base:     q.OrderBy("resource_id", gcfs.Asc).OrderBy(gcfs.DocumentID, gcfs.Asc),
		pageSize: size,
	}
}

// peek returns the stream's current id, reading the next physical page when the
// buffer is spent. A short page proves THIS stream is exhausted; it proves
// nothing about the merged result, which is why every stream is drained
// independently.
func (s *idStream) peek(ctx context.Context) (string, bool, error) {
	if s.pos < len(s.buf) {
		return s.buf[s.pos], true, nil
	}
	if s.done {
		return "", false, nil
	}
	q := s.base.Limit(s.pageSize)
	if s.last != nil {
		q = s.base.StartAfter(s.last).Limit(s.pageSize)
	}
	it := s.reader.Documents(ctx, q)
	defer it.Stop()

	s.buf = s.buf[:0]
	s.pos = 0
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return "", false, firestoredb.MapError(err)
		}
		var row relationshipDoc
		if err := snap.DataTo(&row); err != nil {
			return "", false, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionRelationships, err, sdk.ErrInvalidInput)
		}
		s.buf = append(s.buf, row.ResourceID)
		s.last = snap
	}
	if len(s.buf) < s.pageSize {
		s.done = true
	}
	if len(s.buf) == 0 {
		return "", false, nil
	}
	return s.buf[0], true, nil
}

// advance consumes the current id.
func (s *idStream) advance() { s.pos++ }

// mergeDistinctIDs merges the chunk streams into the port's answer: DISTINCT
// resource ids in raw-byte order, at most limit of them (a non-positive limit is
// unbounded, the port's own meaning).
//
// Deduplication happens BEFORE the limit is applied, which is the whole reason
// the streams are merged rather than read to completion and folded: every stream
// is ascending, so the merged sequence is non-decreasing and duplicates arrive
// adjacent. A short physical page from one chunk never ends the merge — the loop
// keeps pulling from whichever stream currently holds the smallest id until
// enough DISTINCT ids are collected or every stream is drained.
func mergeDistinctIDs(ctx context.Context, streams []*idStream, limit int) ([]string, error) {
	var out []string
	var last string
	for limit <= 0 || len(out) < limit {
		best := -1
		var bestID string
		for i, s := range streams {
			id, ok, err := s.peek(ctx)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if best == -1 || id < bestID {
				best, bestID = i, id
			}
		}
		if best == -1 {
			return out, nil
		}
		if len(out) == 0 || bestID != last {
			out = append(out, bestID)
			last = bestID
		}
		streams[best].advance()
	}
	return out, nil
}

// chunkPair is one query's share of a two-dimensional `in` product.
type chunkPair struct {
	primary   []string
	secondary []string
}

// chunkProduct splits two `in` value sets into query-sized pairs. Firestore
// counts disjunctions AFTER expansion to disjunctive normal form, so two `in`
// filters in one query contribute the PRODUCT of their lengths — two relations
// against thirty frontier entries is sixty disjunctions and an InvalidArgument,
// not a valid query. The primary set is chunked to the budget first, then the
// secondary set is chunked to whatever the budget leaves per primary chunk, so
// every emitted pair satisfies len(primary) * len(secondary) <= budget.
func chunkProduct(primary, secondary []string, budget int) []chunkPair {
	var out []chunkPair
	for _, p := range chunkStrings(primary, budget) {
		size := max(budget/len(p), 1)
		for _, s := range chunkStrings(secondary, size) {
			out = append(out, chunkPair{primary: p, secondary: s})
		}
	}
	return out
}

// whereAnyOf adds an equality filter for a single value and an `in` filter for
// several. The single-value form is not an optimization for its own sake: it
// keeps a one-relation query on the same simple index shape the direct reads
// use, and one `in` value is one disjunction either way.
func whereAnyOf(q gcfs.Query, field string, values []string) gcfs.Query {
	if len(values) == 1 {
		return q.Where(field, "==", values[0])
	}
	return q.Where(field, "in", values)
}

// subjectKeysOf maps concrete subject ids to their expansion-free subject keys —
// the exact-subject filter the two target-shaped reads need (subject_relation is
// empty, so a stored userset with the same type and id does NOT match).
func subjectKeysOf(subjectType string, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, subjectKey(subjectType, id, ""))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// descendantClosure is LookupDescendantResourceIDs' walk: breadth-first over the
// UNION of relations, one batch of queries per hop, under the caller's snapshot.
//
// Every hop filters resource_type, the relation union, and the frontier as
// subject KEYS — and because a subject key hashes (type, id, subject_relation)
// together, one equality clause carries the port's subject_type AND its
// concrete-subject requirement (subject_relation == ""). The queries are chunked
// by the DNF PRODUCT of the two `in` sets (chunkProduct), so two relations mean
// fifteen frontier entries per query, not thirty.
//
// The closure is a SET: a resource already collected is never re-queued, which
// is what terminates a cycle and what makes a root appear in the result only
// when a cycle makes it a genuine descendant.
func descendantClosure(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType string, relations []string, subjectType string, rootIDs []string) ([]string, error) {
	rels := distinctSortedIDs(relations)
	frontier := subjectKeysOf(subjectType, rootIDs)
	found := map[string]struct{}{}

	for len(frontier) > 0 {
		var next []string
		for _, pair := range chunkProduct(rels, frontier, descendantChunkBudget) {
			q := whereAnyOf(
				whereAnyOf(db.Collection(collectionRelationships).Where("resource_type", "==", resourceType), "relation", pair.primary),
				"subject_key", pair.secondary)
			rows, err := queryRelationships(ctx, r, q)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if _, seen := found[row.ResourceID]; seen {
					continue
				}
				found[row.ResourceID] = struct{}{}
				next = append(next, subjectKey(subjectType, row.ResourceID, ""))
			}
		}
		slices.Sort(next)
		frontier = next
	}

	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	slices.Sort(out)
	return out, nil
}

// pageIDs applies the keyset window to an already sorted, distinct id slice:
// strictly after `after`, then at most limit (non-positive is unbounded). It is
// the memstore's afterIDs/capIDs pair, and it exists because the descendant walk
// computes its whole closure in Go — the two streamed lookups get the same
// window from the server instead.
func pageIDs(ids []string, after string, limit int) []string {
	if after != "" {
		i, _ := slices.BinarySearch(ids, after)
		for i < len(ids) && ids[i] <= after {
			i++
		}
		ids = ids[i:]
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}
