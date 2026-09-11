package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"google.golang.org/api/iterator"
)

// effectivePageSize is how many documents ONE effective-listing stream pulls per
// physical page while it is being merged. Like lookupPageSize it is a cost knob,
// not a correctness one: the logical result is DISTINCT (subject, role) grants,
// so a page of documents can collapse to fewer grants and the merge simply pulls
// again.
const effectivePageSize = 50

// effectiveRow is one grouped logical grant plus the grant_key it was grouped
// on. The key is the STORED field, carried rather than recomputed, so the cursor
// this store issues is byte-identical to the one the SQL siblings issue from
// their derived column (SCHEMA.md §4.1) — a cursor is a wire format, and PKOf
// echoing a stored value is what keeps three families' formats the same.
type effectiveRow struct {
	grant roles.EffectiveGrant
	key   string
}

func (r effectiveRow) toDomain() roles.EffectiveGrant { return r.grant }

// listEffectiveByResource is ListEffectiveByResource's dedicated merge/group
// reader. It is NOT a boolean PostFilter over one query, and the reason is the
// contract rather than performance: the effective set is DE-DUPLICATED by
// (subject_type, subject_id, role) ACROSS two scopes, so the unit a page, a
// cursor, an offset, and a count all count is a GROUP — something no per-
// document predicate can produce. The SQL siblings say the same thing as a
// GROUP BY over a two-arm WHERE (turso's effectiveRolesBaseSQL); this is that
// query's shape in Firestore's vocabulary.
//
// Under ONE snapshot it reads two streams in grant_key order — the requested
// scope's assignments and the global ones — k-way merges them on grant_key, and
// groups the equal keys, setting provenance per turso's MAX(CASE …) rule:
// Direct for a row stored at the requested scope, Global for a global row a
// scoped HasRole falls back to, and BOTH when the same grant is held both ways.
// A GLOBAL request ("", "") reads ONCE and marks every grant Direct, because
// there is no fallback to itself — HasRole's no-fallback path for an unscoped
// query.
//
// Cost: the page reads O(scanned documents up to the page's last group); a
// WithCount reads and groups the WHOLE population of the two scopes, since a
// count of distinct groups is not a server aggregation. That is the documented
// price of the effective listing, and the reason the RAW listings (which count
// documents) stay on the connector's List.
func listEffectiveByResource(ctx context.Context, db *firestoredb.DB, resourceType, resourceID string, req list.Request) (list.Page[roles.EffectiveGrant], error) {
	if err := req.Validate(); err != nil {
		return list.Page[roles.EffectiveGrant]{}, err
	}
	if strings.TrimSpace(req.Search) != "" {
		return list.Page[roles.EffectiveGrant]{}, fmt.Errorf("authorization firestore store: search is not supported by the effective role listing: %w", sdk.ErrInvalidInput)
	}
	field, direction, err := effectiveOrderField(req.Order)
	if err != nil {
		return list.Page[roles.EffectiveGrant]{}, err
	}
	limit := req.NormalizedLimit(list.Limits{})

	var page list.Page[effectiveRow]
	err = db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		page = list.Page[effectiveRow]{}

		var err error
		if req.ResolvedStrategy() == list.StrategyOffset {
			page, err = effectiveOffsetPage(ctx, db, r, resourceType, resourceID, req.Offset, direction, limit)
		} else {
			page, err = effectiveCursorPage(ctx, db, r, resourceType, resourceID, req.Cursor, field, direction, limit)
		}
		if err != nil {
			return err
		}

		if req.WithCount {
			total, err := effectiveTotal(ctx, db, r, resourceType, resourceID)
			if err != nil {
				return err
			}
			page.Total = &total
		}
		return nil
	})
	if err != nil {
		return list.Page[roles.EffectiveGrant]{}, err
	}
	return list.MapPage(page, effectiveRow.toDomain), nil
}

// effectiveCursorPage is the keyset flow, and it is the connector List's cursor
// flow reproduced over grouped rows with the SAME crud helpers — list.TrimPage
// for HasMore/NextCursor and list.MarkPrevPage for the reverse window — so the
// three families answer a cursor identically.
//
// The reverse probe includes the incoming boundary and fetches limit+1 groups.
// The extra predecessor supplies a cursor that opens the previous page.
func effectiveCursorPage(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID, token, field string, direction gcfs.Direction, limit int) (list.Page[effectiveRow], error) {
	// A nil cursor is either the first page or a STALE token (one issued under a
	// different order field), which decodes to the first page exactly as the SQL
	// siblings' List does.
	cursor, err := list.DecodeCursor(token, field)
	if err != nil {
		return list.Page[effectiveRow]{}, fmt.Errorf("decode cursor: %w: %w", sdk.ErrInvalidInput, err)
	}

	after := ""
	if cursor != nil {
		after = cursor.PK
	}

	rows, err := mergeEffective(ctx, db, r, resourceType, resourceID, after, direction, limit+1, false)
	if err != nil {
		return list.Page[effectiveRow]{}, err
	}

	encode := func(row effectiveRow) (string, error) {
		return list.EncodeCursor(field, row.key, row.key)
	}
	page, err := list.TrimPage(rows, limit, encode)
	if err != nil {
		return list.Page[effectiveRow]{}, err
	}
	if cursor == nil {
		return page, nil
	}

	prev, err := mergeEffective(ctx, db, r, resourceType, resourceID, after, flipDirection(direction), limit+1, true)
	if err != nil {
		return list.Page[effectiveRow]{}, err
	}
	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}
	if err := list.MarkPrevPage(&page, prev, limit, encode); err != nil {
		return list.Page[effectiveRow]{}, err
	}
	return page, nil
}

// effectiveOffsetPage is the offset flow. The offset counts GROUPS, not
// documents: a subject holding a grant both directly and globally is one row of
// the logical result, so skipping n must skip n of those. Firestore's own
// Offset cannot express that, which is why the skip happens after the merge —
// the same posture the connector's List takes under a PostFilter, and the same
// billing note: the skipped groups are read.
func effectiveOffsetPage(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID string, offset int, direction gcfs.Direction, limit int) (list.Page[effectiveRow], error) {
	rows, err := mergeEffective(ctx, db, r, resourceType, resourceID, "", direction, offset+limit+1, false)
	if err != nil {
		return list.Page[effectiveRow]{}, err
	}
	if offset < len(rows) {
		rows = rows[offset:]
	} else {
		rows = nil
	}

	page := list.Page[effectiveRow]{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		page.HasMore = true
	}
	page.HasPrev = offset > 0
	return page, nil
}

// effectiveTotal counts the DISTINCT logical grants — the whole population the
// page can traverse, never the page itself. There is no server aggregation for
// it (the server cannot group), so it merges the full population and counts the
// groups: O(population) document reads, under the same snapshot as the page it
// accompanies, so the count and the page always agree.
func effectiveTotal(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID string) (int64, error) {
	rows, err := mergeEffective(ctx, db, r, resourceType, resourceID, "", gcfs.Asc, 0, false)
	if err != nil {
		return 0, err
	}
	return int64(len(rows)), nil
}

// mergeEffective is the merge/group itself: at most want grouped grants in
// grant_key order, after the `after` key in the requested direction. inclusive
// includes the boundary for reverse probes. Empty after starts at the beginning;
// a non-positive want is unbounded.
//
// Both streams are ordered by grant_key in the SAME direction, so the merged
// sequence is monotone and every document of one group arrives adjacent to the
// rest — which is what lets the grouping be streaming rather than a full
// materialization, and what makes a group that spans BOTH streams land on one
// side of a page boundary rather than being split across two pages.
func mergeEffective(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID, after string, direction gcfs.Direction, want int, inclusive bool) ([]effectiveRow, error) {
	streams := effectiveStreams(db, r, resourceType, resourceID, after, direction, inclusive)

	var out []effectiveRow
	for want <= 0 || len(out) < want {
		best := ""
		found := false
		for _, s := range streams {
			row, ok, err := s.peek(ctx)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if !found || precedes(row.GrantKey, best, direction) {
				best, found = row.GrantKey, true
			}
		}
		if !found {
			return out, nil
		}

		// Every document whose grant_key equals the winning key belongs to this
		// group, wherever it came from: the identity is taken from the first one
		// seen and each contributing stream sets its own provenance flag.
		grouped := effectiveRow{key: best}
		identified := false
		for _, s := range streams {
			for {
				row, ok, err := s.peek(ctx)
				if err != nil {
					return nil, err
				}
				if !ok || row.GrantKey != best {
					break
				}
				if !identified {
					grouped.grant.SubjectType = row.SubjectType
					grouped.grant.SubjectID = row.SubjectID
					grouped.grant.Role = row.Role
					identified = true
				}
				if s.global {
					grouped.grant.Global = true
				} else {
					grouped.grant.Direct = true
				}
				s.advance()
			}
		}
		out = append(out, grouped)
	}
	return out, nil
}

// effectiveStreams builds the streams one merge reads. A SCOPED request reads
// two — the requested scope and the global scope, whose provenance flags differ
// — while a GLOBAL request reads exactly ONE and marks it Direct: a global
// assignment IS the direct assignment at the global scope, and there is no
// second arm to fall back to (turso's scopedLiteral = 0 collapsing the union).
func effectiveStreams(db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID, after string, direction gcfs.Direction, inclusive bool) []*grantStream {
	scoped := resourceType != "" || resourceID != ""

	streams := []*grantStream{newGrantStream(db, r, resourceKey(resourceType, resourceID), after, direction, false, inclusive)}
	if scoped {
		streams = append(streams, newGrantStream(db, r, resourceKey("", ""), after, direction, true, inclusive))
	}
	return streams
}

// precedes reports whether a sorts before b in the merge's direction.
func precedes(a, b string, direction gcfs.Direction) bool {
	if direction == gcfs.Desc {
		return a > b
	}
	return a < b
}

// grantStream is ONE scope's assignments read as an ordered stream of role
// documents. It is idStream's shape for a different projection: the same
// physical-page cursor rule (resume on the document SNAPSHOT the server
// positioned, never on a decoded value) and the same "a short page proves only
// THIS stream is exhausted" rule.
type grantStream struct {
	reader   firestoredb.Reader
	base     gcfs.Query
	pageSize int
	// global marks the stream whose rows are the GLOBAL fallback, so provenance
	// is a property of the stream rather than a per-row scope comparison.
	global bool

	buf  []roleDoc
	pos  int
	last *gcfs.DocumentSnapshot
	done bool
}

// newGrantStream builds one scope's stream. Reverse probes include the boundary;
// forward pages exclude it. Physical continuation always resumes after a snapshot.
func newGrantStream(db *firestoredb.DB, r firestoredb.Reader, scopeKey, after string, direction gcfs.Direction, global, inclusive bool) *grantStream {
	q := rolesQuery(db).Where("resource_key", "==", scopeKey)
	if after != "" {
		op := ">"
		if direction == gcfs.Desc {
			op = "<"
		}
		if inclusive {
			op += "="
		}
		q = q.Where("grant_key", op, after)
	}
	return &grantStream{
		reader:   r,
		base:     q.OrderBy("grant_key", direction),
		pageSize: effectivePageSize,
		global:   global,
	}
}

// peek returns the stream's current row, reading the next physical page when the
// buffer is spent.
func (s *grantStream) peek(ctx context.Context) (roleDoc, bool, error) {
	if s.pos < len(s.buf) {
		return s.buf[s.pos], true, nil
	}
	if s.done {
		return roleDoc{}, false, nil
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
			return roleDoc{}, false, firestoredb.MapError(err)
		}
		row, err := decodeRole(snap)
		if err != nil {
			return roleDoc{}, false, err
		}
		s.buf = append(s.buf, row)
		s.last = snap
	}
	if len(s.buf) < s.pageSize {
		s.done = true
	}
	if len(s.buf) == 0 {
		return roleDoc{}, false, nil
	}
	return s.buf[0], true, nil
}

// advance consumes the current row.
func (s *grantStream) advance() { s.pos++ }

// flipDirection reverses a sort direction for the backward window.
func flipDirection(d gcfs.Direction) gcfs.Direction {
	if d == gcfs.Desc {
		return gcfs.Asc
	}
	return gcfs.Desc
}
