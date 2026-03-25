// Package fop provides bridge-layer pagination and authorization helpers.
package fop

import (
	"context"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// PostfilterLoop fetches pages from a list function, filters results by
// authorization via BatchCheck, and accumulates until the target limit is
// reached or the data source is exhausted.
//
// Uses 2× overfetch per iteration to minimize round trips when most records
// are authorized. Stops when:
//   - accumulated results reach the requested limit
//   - the data source returns fewer rows than requested (exhausted)
//   - there is no next cursor to follow
//
// The returned Pagination reflects the last position consumed from the store,
// not the last record returned to the caller.
//
// Example usage in a generated bridge handler:
//
//	records, pagination, err := bridfop.PostfilterLoop(
//	    r.Context(), b.authorizer, subject, "read", "user",
//	    func(rec usersrepo.User) string { return rec.UserID },
//	    func(ctx context.Context, p fop.PageStringCursor) ([]usersrepo.User, fop.Pagination, error) {
//	        return b.userRepository.List(ctx, filter, orderBy, p)
//	    },
//	    page,
//	)
func PostfilterLoop[T any](
	ctx context.Context,
	authorizer *authorization.Authorizer,
	subject authorization.Subject,
	permission, resourceType string,
	getID func(T) string,
	list func(context.Context, fop.PageStringCursor) ([]T, fop.Pagination, error),
	page fop.PageStringCursor,
) ([]T, fop.Pagination, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = 25
	}
	overfetch := limit * 2

	var accumulated []T
	var lastPagination fop.Pagination
	currentCursor := page.Cursor

	for {
		batch, pagination, err := list(ctx, fop.PageStringCursor{Limit: overfetch, Cursor: currentCursor})
		if err != nil {
			return nil, fop.Pagination{}, err
		}
		lastPagination = pagination

		if len(batch) > 0 {
			ids := make([]string, len(batch))
			for i, rec := range batch {
				ids[i] = getID(rec)
			}

			allowedIDs, err := authorizer.FilterAuthorized(ctx, subject, permission, resourceType, ids)
			if err != nil {
				return nil, fop.Pagination{}, err
			}

			allowedSet := make(map[string]bool, len(allowedIDs))
			for _, id := range allowedIDs {
				allowedSet[id] = true
			}

			for _, rec := range batch {
				if allowedSet[getID(rec)] {
					accumulated = append(accumulated, rec)
					if len(accumulated) >= limit {
						break
					}
				}
			}
		}

		// Stop: accumulated enough records.
		if len(accumulated) >= limit {
			break
		}
		// Stop: data source exhausted.
		if len(batch) < overfetch {
			break
		}
		// Stop: no next cursor to follow.
		if pagination.NextCursor == "" {
			break
		}
		currentCursor = pagination.NextCursor
	}

	return accumulated, lastPagination, nil
}
