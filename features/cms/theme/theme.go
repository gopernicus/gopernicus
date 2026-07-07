// Package theme is the public-site rendering seam for the CMS feature. Under the
// Registry model it holds only the SITE CHROME (plan §5): the PublicViews
// interface — home, archive, the single-entry chrome wrapper, and error — plus
// the bundled default theme. Per-entry rendering is NOT here; it lives in the
// content types' registered TemplateFuncs. A host overrides chrome by passing a
// PublicViews to cms.Config.Views, and per-type rendering via Config.Templates.
package theme

import (
	"github.com/gopernicus/gopernicus/features/cms/internal/inbound/http/views"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// ListItem is one entry in a public listing (home / archive). It is the public
// counterpart to the internal view model, so host themes can build listings
// without importing the feature's internal views package.
type ListItem struct {
	Title   string
	Href    string
	Excerpt string
}

// Default returns the bundled templ chrome — the PublicViews used when a host
// wires no override.
func Default() PublicViews { return defaultPublicViews{} }

// PublicViews is the site-chrome seam. Each method returns a web.Renderer rather
// than a concrete templ component, so a host can swap the chrome without the
// handler binding to specific views. Single wraps a registered per-entry body
// (from content.Registry.Render) in the chrome.
type PublicViews interface {
	Home(nav []menus.MenuItem, items []ListItem) web.Renderer
	Archive(heading string, nav []menus.MenuItem, items []ListItem, nextCursor, baseHref string) web.Renderer
	Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer) web.Renderer
	Error(status int, message string) web.Renderer
}

// defaultPublicViews renders with the bundled templ chrome.
type defaultPublicViews struct{}

func (defaultPublicViews) Home(nav []menus.MenuItem, items []ListItem) web.Renderer {
	return views.PublicHome(nav, toViewItems(items))
}

func (defaultPublicViews) Archive(heading string, nav []menus.MenuItem, items []ListItem, nextCursor, baseHref string) web.Renderer {
	return views.PublicArchive(heading, nav, toViewItems(items), nextCursor, baseHref)
}

func (defaultPublicViews) Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer) web.Renderer {
	return views.PublicSingle(title, metaDesc, nav, body)
}

func (defaultPublicViews) Error(status int, message string) web.Renderer {
	return views.PublicError(status, message)
}

// toViewItems maps the public ListItem to the internal view model.
func toViewItems(items []ListItem) []views.ListItem {
	out := make([]views.ListItem, len(items))
	for i, it := range items {
		out[i] = views.ListItem{Title: it.Title, Href: it.Href, Excerpt: it.Excerpt}
	}
	return out
}
