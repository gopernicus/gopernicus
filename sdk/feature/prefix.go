package feature

import (
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/web"
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
