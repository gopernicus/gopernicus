package feature

import (
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// PrefixRegistrar wraps a RouteRegistrar so every route registered through it
// is mounted under a fixed path prefix — the C1 answer to route namespacing
// (see features/README.md): a host can relocate a feature under e.g. "/blog"
// without the feature's own Register knowing or cooperating, because the
// feature only ever sees the RouteRegistrar it was handed.
//
// PrefixRegistrar only changes the path a handler is registered under. It does
// not rewrite anything the feature itself renders — links, redirects, form
// actions. A feature whose views build absolute paths (e.g. a hardcoded "/")
// will still point at the un-prefixed root; see features/README.md's
// known-limitation note for the cms case.
type PrefixRegistrar struct {
	Prefix string // e.g. "/blog"; "" or "/" mounts at the root (no-op)
	Next   RouteRegistrar
}

// Handle prefixes path and delegates to Next, so a host builds one of these
// per feature it wants to relocate and passes it as Mount.Router instead of
// its bare router.
func (p PrefixRegistrar) Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	p.Next.Handle(method, joinPrefix(p.Prefix, path), handler, middleware...)
}

// Group wraps a RouteRegistrar so every route registered through it is mounted
// under a shared path prefix AND runs a shared Middleware stack first — the
// two-in-one PrefixRegistrar (prefix only) does not cover. Like PrefixRegistrar
// it is itself a RouteRegistrar, so a host builds one and passes it as
// Mount.Router; the feature's Register never learns it was grouped. This is
// where group ergonomics come from — composition, never widening the one-method
// RouteRegistrar contract (FS7). The natural use is a subsystem whose routes
// share both a mount point and a middleware policy, e.g. an admin section behind
// an authorizer.
type Group struct {
	Prefix     string // e.g. "/admin"; "" or "/" mounts at the root (prefix a no-op)
	Middleware []web.Middleware
	Next       RouteRegistrar
}

// Handle prefixes path via joinPrefix and prepends the group's Middleware to any
// route-local middleware, then delegates to Next. The group's middleware sits at
// the front, so it runs before the route's own (web.WebHandler applies the slice
// outermost-first). The combined slice is freshly allocated rather than appended
// onto the shared Middleware field, so registering many routes through one Group
// never corrupts the group's stack.
func (g Group) Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware) {
	combined := make([]web.Middleware, 0, len(g.Middleware)+len(middleware))
	combined = append(combined, g.Middleware...)
	combined = append(combined, middleware...)
	g.Next.Handle(method, joinPrefix(g.Prefix, path), handler, combined...)
}

// joinPrefix concatenates a mount prefix and a route path, normalizing the
// slashes naive concatenation gets wrong: a trailing slash on prefix, a
// missing leading slash on prefix, and the "{$}"-suffixed exact-match
// patterns Go 1.22+ ServeMux uses for a feature's root route all collapse to
// the same result either way. "" or "/" as prefix is a deliberate no-op, so a
// host can unconditionally wrap its Router without special-casing the
// zero-prefix (root-mounted) case.
func joinPrefix(prefix, path string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return path
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return prefix + path
}
