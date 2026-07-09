package http

import (
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// Views is the CMS feature's HTML rendering port (FS3): the whole HTML surface —
// public site chrome AND the admin management pages — renders through it, so a
// host can swap presentation without the handlers binding to concrete templates.
// Every method returns a web.Renderer over domain types and the port's own view
// models; per-entry bodies ride the content.Registry via SeedTemplates.
//
// The bundled default lives in the sibling module features/cms/views/templ. The
// blessed way to customize is partial override: embed that concrete default and
// override individual methods (e.g. only the four chrome methods). Implementing
// all methods from scratch (e.g. over sdk/web.Template) is possible but not the
// sold path.
//
// A nil Config.Views means the HTML surface is not registered — only the media
// byte endpoint (GET /media/{id}/file) mounts.
type Views interface {
	// Public chrome.
	Home(nav []menus.MenuItem, items []ListItem) web.Renderer
	Archive(heading string, nav []menus.MenuItem, items []ListItem, pager Pager) web.Renderer
	Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer) web.Renderer
	Error(status int, message string) web.Renderer

	// Public forms / fragments.
	ContactForm(m ContactModel) web.Renderer
	ContactThanks() web.Renderer
	MenuNav(m menus.Menu, items []menus.MenuItem) web.Renderer

	// Admin.
	EntriesList(heading, newHref, editPrefix string, items []EntryListItem, pager Pager) web.Renderer
	EntryForm(m EntryFormModel) web.Renderer
	TermsList(categories, tags []taxonomy.Term) web.Renderer
	TermForm(m TermFormModel) web.Renderer
	MenusList(ms []menus.Menu) web.Renderer
	MenuNew(formError string) web.Renderer
	MenuDetail(m menus.Menu, items []menus.MenuItem) web.Renderer
	MenuItemForm(it menus.MenuItem) web.Renderer
	MediaLibrary(assets []media.Asset, formError string) web.Renderer
	InquiriesList(items []messaging.Inquiry) web.Renderer
	AdminError(status int, message string) web.Renderer

	// Seed templates supplies the default per-entry bindings for the seed content
	// types (article, page). The host registers them on the Registry via
	// cms.Register; Config.Templates still overrides (last-write-wins).
	SeedTemplates() []content.TemplateBinding
}
