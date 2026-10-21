package authorization

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// DefaultCheckChunk is FilterAuthorized's CheckBatch size when the Checker
// declares no Limiter.
const DefaultCheckChunk = 100

// FilterAuthorized returns the ids on which p holds permission, in input order
// with duplicates kept. It chunks CheckBatch by Limits().MaxCheckBatch (or
// DefaultCheckChunk without a Limiter) and fails as a whole when any chunk fails.
func FilterAuthorized(ctx context.Context, c Checker, p Principal, permission, resourceType string, ids []string) ([]string, error) {
	chunk := DefaultCheckChunk
	if l, ok := c.(Limiter); ok {
		if n := l.Limits().MaxCheckBatch; n > 0 {
			chunk = n
		}
	}
	out := make([]string, 0, len(ids))
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		reqs := make([]CheckRequest, 0, end-start)
		for _, id := range ids[start:end] {
			reqs = append(reqs, CheckRequest{Principal: p, Permission: permission, Resource: Resource{Type: resourceType, ID: id}})
		}
		decisions, err := c.CheckBatch(ctx, reqs)
		if err != nil {
			return nil, err
		}
		if len(decisions) != len(reqs) {
			return nil, fmt.Errorf("authorization: CheckBatch returned %d decisions for %d requests", len(decisions), len(reqs))
		}
		for i, d := range decisions {
			if d.Allowed {
				out = append(out, ids[start+i])
			}
		}
	}
	return out, nil
}

// LookupAll drains every page of req from its After, dedupes, and returns at
// most max unique IDs in first-seen order. More than max unique IDs, or a
// cursor the lookup already returned, is sdk.ErrUnavailable: it never returns
// a short list. A max below 1 is sdk.ErrInvalidInput.
func LookupAll(ctx context.Context, l Lookup, req LookupRequest, max int) ([]string, error) {
	if max < 1 {
		return nil, fmt.Errorf("authorization: LookupAll max must be positive, got %d: %w", max, sdk.ErrInvalidInput)
	}
	seen := make(map[string]struct{})
	cursors := make(map[string]struct{})
	out := []string{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := l.LookupResources(ctx, req)
		if err != nil {
			return nil, err
		}
		for _, id := range page.IDs {
			if _, dup := seen[id]; dup {
				continue
			}
			if len(out) == max {
				return nil, fmt.Errorf("authorization: lookup exceeds %d ids: %w", max, sdk.ErrUnavailable)
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		if page.NextCursor == "" {
			return out, nil
		}
		if _, again := cursors[page.NextCursor]; again {
			return nil, fmt.Errorf("authorization: lookup repeated a cursor: %w", sdk.ErrUnavailable)
		}
		cursors[page.NextCursor] = struct{}{}
		req.After = page.NextCursor
	}
}
