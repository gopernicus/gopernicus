package goth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// errNilBundle is returned by New for a nil bundle.
var errNilBundle = errors.New("cms views/goth: New requires a non-nil *goth.Bundle")

// menuOrderStr renders an int menu-order as a string for a form value attribute.
func menuOrderStr(n int) string { return strconv.Itoa(n) }

// statusText renders an HTTP status as "<code> <reason>" for error/admin pages.
func statusText(status int) string {
	if t := http.StatusText(status); t != "" {
		return strconv.Itoa(status) + " " + t
	}
	return strconv.Itoa(status)
}

// componentFunc adapts a render closure to templ.Component (identical Render
// signature to web.Renderer).
type componentFunc func(context.Context, io.Writer) error

func (f componentFunc) Render(ctx context.Context, w io.Writer) error { return f(ctx, w) }

// rendererComponent adapts a feature-produced web.Renderer (a registered per-entry
// body) to a templ.Component so the public chrome can wrap it. web.Renderer and
// templ.Component share the same Render(ctx, io.Writer) error signature.
func rendererComponent(r web.Renderer) templ.Component {
	return componentFunc(func(ctx context.Context, w io.Writer) error { return r.Render(ctx, w) })
}

// parseURL validates s into a primitives.URL for an href/action prop. A handler
// supplies already-safe relative paths, so an error yields the zero URL (rendering
// nothing) rather than a panic.
func parseURL(s string) primitives.URL {
	u, _ := primitives.ParseURL(s)
	return u
}

// entryCountText is the entries-list aria-live status text.
func entryCountText(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return strconv.Itoa(n) + " entries"
}

// statusBadgeVariant maps an entry status to a Badge variant: published is the
// solid default, everything else (draft) is the muted secondary.
func statusBadgeVariant(status string) primitives.BadgeVariant {
	if status == "published" {
		return primitives.BadgeDefault
	}
	return primitives.BadgeSecondary
}
