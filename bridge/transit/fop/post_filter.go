// Package fop provides bridge-layer pagination and authorization helpers.
package fop

import (
	"context"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// PostfilterLoop fetches pages from a list function, filters results by
// authorization via FilterAuthorized, and accumulates authorized records
// until at least limit+1 are held (proving a next page exists) or the data
// source is exhausted. The final page is built by fop.TrimPage, so the
// pagination policy matches Repository.List: NextCursor is encoded from the
// last record returned to the caller, and is empty when no further
// authorized record is known to exist — a returned cursor never leads to an
// empty page.
//
// Uses 2× overfetch per iteration to minimize round trips when most records
// are authorized. Stops when:
//   - accumulated authorized records exceed the requested limit
//   - the data source returns fewer rows than requested (exhausted)
//   - there is no next cursor to follow
//
// encodeCursor must encode with the same order field the list function uses.
//
// HasPrev and PreviousCursor are taken from the first fetched batch (the
// requested page position). Because the previous window is probed at the
// overfetch size and without authorization filtering, PreviousCursor is
// approximate under postfilter pagination.
//
// Example usage in a generated bridge handler:
//
//	records, pagination, err := bridfop.PostfilterLoop(
//	    r.Context(), b.authorizer, subject, "read", "user",
//	    func(rec usersrepo.User) string { return rec.UserID },
//	    func(rec usersrepo.User) (string, error) { return usersrepo.EncodeUserCursor(rec, orderBy.Field) },
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
	encodeCursor func(T) (string, error),
	list func(context.Context, fop.PageStringCursor) ([]T, fop.Pagination, error),
	page fop.PageStringCursor,
) ([]T, fop.Pagination, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = 25
	}
	overfetch := limit * 2

	var accumulated []T
	var firstPagination fop.Pagination
	firstBatch := true
	currentCursor := page.Cursor

	for {
		batch, pagination, err := list(ctx, fop.PageStringCursor{Limit: overfetch, Cursor: currentCursor})
		if err != nil {
			return nil, fop.Pagination{}, err
		}
		if firstBatch {
			firstPagination = pagination
			firstBatch = false
		}

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

			// The whole batch is already authorized in one call, so keep every
			// allowed record — TrimPage trims back to the limit at the end.
			for _, rec := range batch {
				if allowedSet[getID(rec)] {
					accumulated = append(accumulated, rec)
				}
			}
		}

		// Stop: limit+1 authorized records held — a next page provably exists.
		if len(accumulated) > limit {
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

	records, result, err := fop.TrimPage(accumulated, limit, encodeCursor)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	result.HasPrev = firstPagination.HasPrev
	result.PreviousCursor = firstPagination.PreviousCursor

	return records, result, nil
}
