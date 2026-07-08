// Package templ is the bundled default implementation of the CMS feature's HTML
// rendering port (cms.Views), built on a-h/templ components. It is a sibling
// module of features/cms (FS3/FS4 — presentation defaults ship as views/<pkg>),
// so the feature core stays sdk-only. Hosts wire it as Views: templ.New(), and
// customize by embedding templ.Views and overriding individual methods.
package templ

import (
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// _ pins Views to the cms.Views port at compile time.
var _ cms.Views = New()

// Views is the concrete bundled default renderer. It carries no state, so its
// zero value is usable and it is safe to embed for partial overrides.
type Views struct{}

// New returns the bundled default Views.
func New() Views { return Views{} }

// Home renders recent published entries in the site chrome.
func (Views) Home(nav []menus.MenuItem, items []cms.ListItem) web.Renderer {
	return PublicHome(nav, items)
}

// Archive renders a taxonomy archive listing in the site chrome.
func (Views) Archive(heading string, nav []menus.MenuItem, items []cms.ListItem, nextCursor, baseHref string) web.Renderer {
	return PublicArchive(heading, nav, items, nextCursor, baseHref)
}

// Single wraps a registered per-entry body in the site chrome.
func (Views) Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer) web.Renderer {
	return PublicSingle(title, metaDesc, nav, body)
}

// Error renders a public error page in the site chrome.
func (Views) Error(status int, message string) web.Renderer {
	return PublicError(status, message)
}

// ContactForm renders the public contact form.
func (Views) ContactForm(m cms.ContactModel) web.Renderer { return ContactForm(m) }

// ContactThanks renders the post-submit thank-you page.
func (Views) ContactThanks() web.Renderer { return ContactThanks() }

// MenuNav renders a menu's items as a public nav tree.
func (Views) MenuNav(m menus.Menu, items []menus.MenuItem) web.Renderer { return MenuNav(m, items) }

// EntriesList renders the admin index for one content type.
func (Views) EntriesList(heading, newHref, editPrefix string, items []cms.EntryListItem, nextCursor string) web.Renderer {
	return EntriesList(heading, newHref, editPrefix, items, nextCursor)
}

// EntryForm renders the generic create/edit entry editor.
func (Views) EntryForm(m cms.EntryFormModel) web.Renderer { return EntryForm(m) }

// TermsList renders the taxonomy admin page.
func (Views) TermsList(categories, tags []taxonomy.Term) web.Renderer {
	return TermsList(categories, tags)
}

// TermForm renders the taxonomy term form.
func (Views) TermForm(m cms.TermFormModel) web.Renderer { return TermForm(m) }

// MenusList renders the menus admin index.
func (Views) MenusList(ms []menus.Menu) web.Renderer { return MenusList(ms) }

// MenuNew renders the create-menu form.
func (Views) MenuNew(formError string) web.Renderer { return MenuNew(formError) }

// MenuDetail renders a menu with its items and the add-item form.
func (Views) MenuDetail(m menus.Menu, items []menus.MenuItem) web.Renderer {
	return MenuDetail(m, items)
}

// MenuItemForm renders the edit-menu-item form.
func (Views) MenuItemForm(it menus.MenuItem) web.Renderer { return MenuItemForm(it) }

// MediaLibrary renders the media library + upload form.
func (Views) MediaLibrary(assets []media.Asset, formError string) web.Renderer {
	return MediaLibrary(assets, formError)
}

// InquiriesList renders the admin inquiry list.
func (Views) InquiriesList(items []messaging.Inquiry) web.Renderer { return InquiriesList(items) }

// AdminError renders an admin error page.
func (Views) AdminError(status int, message string) web.Renderer { return ErrorPage(status, message) }

// SeedTemplates returns the default per-entry bindings for the seed content
// types (article, page), wrapping the bundled ArticleContent/PageContent bodies.
// cms.Register registers these on the Registry; Config.Templates still overrides.
func (Views) SeedTemplates() []content.TemplateBinding {
	return []content.TemplateBinding{
		{Type: "article", Template: "default", Fn: func(e content.Entry) web.Renderer { return ArticleContent(e) }},
		{Type: "page", Template: "default", Fn: func(e content.Entry) web.Renderer { return PageContent(e) }},
	}
}
