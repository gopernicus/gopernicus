package httpmid

import (
	"context"
	"errors"
	"net/http"
)

// UniqueResolver resolves one or more URL path values to a resource ID.
// The params map contains the values of each readParam in the order they were
// declared. Returns the resolved resource ID, or an error if not found.
type UniqueResolver func(ctx context.Context, params map[string]string) (id string, err error)

// ErrUniqueNotFound is returned by a [UniqueResolver] when the unique value does not match
// any resource. UniqueToID maps this to a 404 response.
var ErrUniqueNotFound = errors.New("slug not found")

// UniqueToID resolves URL path params to a resource ID and injects the result
// via [http.Request.SetPathValue], making the resolved ID available to
// subsequent middleware (e.g. [AuthorizeParam]) and handlers via
// [http.Request.PathValue].
//
// readParams are the URL path param names to read and pass to the resolver.
// idParam is the path value name to write with the resolved ID.
//
// Simple (globally unique slug):
//
//	httpmid.UniqueToID(
//	    func(ctx context.Context, p map[string]string) (string, error) {
//	        return store.GetIDBySlug(ctx, p["slug"])
//	    },
//	    "tenant_id",
//	    "slug",
//	)
//
// Composite (slug unique within a scope):
//
//	httpmid.UniqueToID(
//	    func(ctx context.Context, p map[string]string) (string, error) {
//	        return store.GetIDBySlug(ctx, p["slug"], p["tenant_id"])
//	    },
//	    "group_id",
//	    "slug", "tenant_id",
//	)
func UniqueToID(
	resolver UniqueResolver,
	idParam string,
	readParams ...string,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			params := make(map[string]string, len(readParams))
			for _, p := range readParams {
				params[p] = r.PathValue(p)
			}

			id, err := resolver(r.Context(), params)
			if err != nil {
				if errors.Is(err, ErrUniqueNotFound) {
					http.NotFound(w, r)
					return
				}
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			r.SetPathValue(idParam, id)
			next.ServeHTTP(w, r)
		})
	}
}
