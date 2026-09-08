package authorization

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// Candidate is one host row plus the source-compatible cursor immediately after
// it. The cursor is PER ROW, not per page: it is what lets FilterPage stop in
// the middle of an over-fetched source page without skipping or repeating a row.
type Candidate[T any] struct {
	Item       T
	NextCursor string
}

// CandidatePage is one source pull: the candidates in the host's stable order
// plus whether the source has more candidates after the last one.
//
// An empty page with HasMore false is normal exhaustion. An empty page with
// HasMore true cannot advance the cursor and is rejected (sdk.ErrInvalidInput).
type CandidatePage[T any] struct {
	Items   []Candidate[T]
	HasMore bool
}

// CandidateSource yields candidates in the host's stable order after cursor, at
// most limit of them. An empty cursor means "from the start". The source owns
// the order and the cursor encoding; FilterPage treats cursors as opaque and
// returns them to the caller verbatim.
type CandidateSource[T any] func(ctx context.Context, cursor string, limit int) (CandidatePage[T], error)

// FilterPageRequest is the struct input to FilterPage: the decision query, the
// host's candidate source, and the page the caller wants.
type FilterPageRequest[T any] struct {
	Principal    PrincipalRef
	Permission   string
	ResourceType string
	// ID reports the resource id of a candidate row — what FilterAuthorized
	// decides on. Required.
	ID func(T) string
	// Source pulls candidates. Required.
	Source CandidateSource[T]
	// Limit is the page size wanted; 0 means crud.DefaultLimit. A negative Limit,
	// or one above the resolved MaxBatchSize, is sdk.ErrInvalidInput.
	Limit int
	// Cursor is the continuation a previous FilterPage returned (empty = first
	// page).
	Cursor string
}

// FilterPage fills ONE page of authorized rows by POSTFILTERING a host-ordered
// candidate stream: it pulls candidate pages from req.Source, decides them in
// one FilterAuthorized call per pull, and appends the allowed rows in source
// order until the page is full, the source is exhausted, or the MaxFilterScan
// budget is reached.
//
// It is the DENSE-and-bounded enumeration path. Use it when the database
// already bounds and orders the candidates (a container's contents, a search
// result, the caller's own rows) and most of them are expected to be allowed.
// For "everything of type X this principal may see" use LookupResources /
// LookupResourcesIn instead: postfiltering a whole table is unbounded work.
//
// # The candidate-cursor contract
//
// Every candidate carries the source cursor immediately AFTER it. Each cursor
// must be non-empty and must differ from the cursor immediately before it, and
// no cursor may repeat within one FilterPage call — a source that cannot
// advance is rejected with sdk.ErrInvalidInput rather than spun on. A source
// that returns more than the requested candidate count is rejected the same way.
// Cursors are returned to the caller verbatim; FilterPage adds no encoding.
//
// # Scan bound
//
// Every source request is clamped to min(2×Limit, MaxBatchSize, the remaining
// MaxFilterScan budget), so one call never scans more than MaxFilterScan
// candidates. Reaching the bound with the page unfilled returns the PARTIAL page
// plus the continuation after the last scanned candidate — not
// ErrEvaluationLimit, because the continuation makes the work resumable.
//
// crud.Page.HasMore therefore means UNSCANNED CANDIDATES REMAIN, not that
// another authorized row is guaranteed: following a HasMore page may yield a
// final empty page when every remaining candidate is denied.
//
// Items is always a non-nil slice, so an empty page marshals "items":[].
func FilterPage[T any](ctx context.Context, s *Service, req FilterPageRequest[T]) (crud.Page[T], error) {
	if s == nil || s.decider == nil {
		return crud.Page[T]{}, ErrNoDecisionKind
	}
	if err := req.Principal.Validate(); err != nil {
		return crud.Page[T]{}, err
	}
	if err := relationship.ValidateRefField("permission", req.Permission); err != nil {
		return crud.Page[T]{}, err
	}
	if err := relationship.ValidateRefField("resource type", req.ResourceType); err != nil {
		return crud.Page[T]{}, err
	}
	if req.ID == nil {
		return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage requires an ID function: %w", sdk.ErrInvalidInput)
	}
	if req.Source == nil {
		return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage requires a Source: %w", sdk.ErrInvalidInput)
	}
	if req.Limit < 0 {
		return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage limit must not be negative, got %d: %w", req.Limit, sdk.ErrInvalidInput)
	}
	limit := req.Limit
	if limit == 0 {
		limit = crud.DefaultLimit
	}
	if limit > s.limits.MaxBatchSize {
		return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage limit %d exceeds MaxBatchSize %d: %w", limit, s.limits.MaxBatchSize, sdk.ErrInvalidInput)
	}

	maxScan := s.limits.MaxFilterScan
	out := make([]T, 0, limit)
	cursor := req.Cursor
	// consumed is the cursor after the last candidate this call consumed; it is
	// the continuation handed back. It stays empty when the call consumed nothing.
	consumed := ""
	// seen holds every cursor observed in this call, seeded with the input
	// cursor, so a source that loops is refused rather than spun on.
	seen := map[string]bool{req.Cursor: true}
	scanned := 0

	for len(out) < limit && scanned < maxScan {
		if err := ctx.Err(); err != nil {
			return crud.Page[T]{}, err
		}

		want := 2 * limit
		if want > s.limits.MaxBatchSize {
			want = s.limits.MaxBatchSize
		}
		if remaining := maxScan - scanned; want > remaining {
			want = remaining
		}

		page, err := req.Source(ctx, cursor, want)
		if err != nil {
			return crud.Page[T]{}, err
		}
		if len(page.Items) > want {
			return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage candidate source returned %d candidates for a limit of %d: %w", len(page.Items), want, sdk.ErrInvalidInput)
		}
		if len(page.Items) == 0 {
			if page.HasMore {
				return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage candidate source reported more candidates but returned none: %w", sdk.ErrInvalidInput)
			}
			// Normal exhaustion, and no decision spent on an empty page.
			return crud.Page[T]{Items: out, HasMore: false, NextCursor: consumed}, nil
		}

		// Validate the cursors BEFORE the decision call so a malformed page never
		// costs an authorization evaluation.
		previous := cursor
		for _, candidate := range page.Items {
			switch {
			case candidate.NextCursor == "":
				return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage candidate source returned an empty candidate cursor: %w", sdk.ErrInvalidInput)
			case candidate.NextCursor == previous:
				return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage candidate source returned a non-advancing candidate cursor %q: %w", candidate.NextCursor, sdk.ErrInvalidInput)
			case seen[candidate.NextCursor]:
				return crud.Page[T]{}, fmt.Errorf("authorization: FilterPage candidate source repeated cursor %q: %w", candidate.NextCursor, sdk.ErrInvalidInput)
			}
			seen[candidate.NextCursor] = true
			previous = candidate.NextCursor
		}

		ids := make([]string, len(page.Items))
		for i, candidate := range page.Items {
			ids[i] = req.ID(candidate.Item)
		}
		allowedIDs, err := s.decider.FilterAuthorized(ctx, req.Principal, req.Permission, req.ResourceType, ids)
		if err != nil {
			return crud.Page[T]{}, err
		}
		allowed := make(map[string]bool, len(allowedIDs))
		for _, id := range allowedIDs {
			allowed[id] = true
		}

		for i, candidate := range page.Items {
			scanned++
			consumed = candidate.NextCursor
			if !allowed[ids[i]] {
				continue
			}
			out = append(out, candidate.Item)
			if len(out) == limit {
				// Mid-batch stop: the unconsumed suffix of this pull is itself more.
				return crud.Page[T]{
					Items:      out,
					HasMore:    i < len(page.Items)-1 || page.HasMore,
					NextCursor: consumed,
				}, nil
			}
		}

		cursor = consumed
		if !page.HasMore {
			return crud.Page[T]{Items: out, HasMore: false, NextCursor: consumed}, nil
		}
		if scanned >= maxScan {
			return crud.Page[T]{Items: out, HasMore: true, NextCursor: consumed}, nil
		}
	}
	return crud.Page[T]{Items: out, HasMore: true, NextCursor: consumed}, nil
}
