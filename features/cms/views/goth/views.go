// Package goth is the ui/goth implementation of the CMS feature's HTML rendering
// port (cms.Views). It is a sibling module of features/cms (FS3/FS4 — presentation
// defaults ship as views/<pkg>), so the feature core stays sdk-only and never
// imports templ or ui/goth. This adapter owns the domain-to-GOTH translation: it
// renders every CMS page — the public site chrome AND the admin management pages —
// through the ui/goth Bundle (its self-hosted fingerprinted assets, primitives, and
// the forms/layouts/data compositions).
//
// Hosts wire it as:
//
//	bundle, _ := goth.New(goth.Config{AssetBasePath: "/assets/goth"})
//	cmsViews, _ := cmsgoth.New(bundle)
//	cms.Config{Views: cmsViews}
//
// and serve the bundle assets under the path the bundle names. Customize by
// embedding this Views and overriding individual methods (the blessed
// partial-override path — e.g. a host overriding only the public-chrome methods).
//
// A nil cms.Config.Views means the HTML surface is not registered (only the media
// byte endpoint mounts); this adapter is never constructed in that case.
package goth

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
)

// _ pins Views to the cms.Views port at compile time.
var _ cms.Views = Views{}

// Views is the ui/goth-backed renderer. It holds the immutable presentation Bundle
// so every page can emit the fingerprinted GOTH stylesheet through Bundle.Head().
// Its zero value is not usable — construct it with New so the Bundle is non-nil.
type Views struct {
	bundle *goth.Bundle
}

// New returns the ui/goth Views over bundle. It returns an error for a nil bundle
// so a misconfigured host fails loudly at construction rather than nil-panicking at
// the first render.
func New(bundle *goth.Bundle) (Views, error) {
	if bundle == nil {
		return Views{}, errNilBundle
	}
	return Views{bundle: bundle}, nil
}

// adminPage wraps an admin body in the shared GOTH document chrome (the bundle's
// head + the admin AppShell). title is the document/page title.
func (v Views) adminPage(title string, body templ.Component) web.Renderer {
	return document(v.bundle.Head(), title, adminShell(title, body))
}

// publicPage wraps a public body in the shared GOTH document chrome with the public
// nav. metaDesc drives the SEO/OpenGraph description when non-empty.
func (v Views) publicPage(title, metaDesc string, nav []menus.MenuItem, body templ.Component) web.Renderer {
	return publicDocument(v.bundle.Head(), title, metaDesc, publicShell(title, nav, body))
}

// Home renders recent published entries in the public site chrome.
func (v Views) Home(nav []menus.MenuItem, items []cms.ListItem) web.Renderer {
	return v.publicPage("Home", "Latest content", nav, homeBody(items))
}

// Archive renders a taxonomy archive listing in the public site chrome.
func (v Views) Archive(heading string, nav []menus.MenuItem, items []cms.ListItem, pager cms.Pager) web.Renderer {
	return v.publicPage(heading, "", nav, archiveBody(heading, items, pager))
}

// Single wraps a registered per-entry body in the public site chrome.
func (v Views) Single(title, metaDesc string, nav []menus.MenuItem, body web.Renderer) web.Renderer {
	return v.publicPage(title, metaDesc, nav, rendererComponent(body))
}

// Error renders a public error page in the public site chrome.
func (v Views) Error(status int, message string) web.Renderer {
	return v.publicPage("Error", "", nil, publicErrorBody(status, message))
}

// ContactForm renders the public contact form.
func (v Views) ContactForm(m cms.ContactModel) web.Renderer {
	return v.publicPage("Contact", "", nil, contactFormBody(m))
}

// ContactThanks renders the post-submit thank-you page.
func (v Views) ContactThanks() web.Renderer {
	return v.publicPage("Thanks", "", nil, contactThanksBody())
}

// MenuNav renders a menu's items as a public nav tree.
func (v Views) MenuNav(m menus.Menu, items []menus.MenuItem) web.Renderer {
	return v.publicPage(m.Name, "", nil, menuNavBody(m, items))
}

// EntriesList renders the admin index for one content type (full document).
func (v Views) EntriesList(heading, newHref, editPrefix string, items []cms.EntryListItem, pager cms.Pager) web.Renderer {
	return v.adminPage(heading, entriesListBody(heading, newHref, editPrefix, items, pager))
}

// EntriesListContent renders ONLY the swappable content region of the admin entry
// index (the HTMX target), so a sort/filter/page HTMX request swaps that region
// while the full EntriesList document backs the no-JS reload.
func (v Views) EntriesListContent(_, _, editPrefix string, items []cms.EntryListItem, pager cms.Pager) web.Renderer {
	return entriesContent(editPrefix, items, pager, false)
}

// EntryForm renders the generic create/edit entry editor.
func (v Views) EntryForm(m cms.EntryFormModel) web.Renderer {
	return v.adminPage(m.Heading, entryFormBody(m))
}

// TermsList renders the taxonomy admin page.
func (v Views) TermsList(categories, tags []taxonomy.Term) web.Renderer {
	return v.adminPage("Taxonomy", termsListBody(categories, tags))
}

// TermForm renders the taxonomy term form.
func (v Views) TermForm(m cms.TermFormModel) web.Renderer {
	return v.adminPage(m.Heading, termFormBody(m))
}

// MenusList renders the menus admin index.
func (v Views) MenusList(ms []menus.Menu) web.Renderer {
	return v.adminPage("Menus", menusListBody(ms))
}

// MenuNew renders the create-menu form.
func (v Views) MenuNew(formError string) web.Renderer {
	return v.adminPage("New Menu", menuNewBody(formError))
}

// MenuDetail renders a menu with its items and the add-item form.
func (v Views) MenuDetail(m menus.Menu, items []menus.MenuItem) web.Renderer {
	return v.adminPage(m.Name, menuDetailBody(m, items))
}

// MenuItemForm renders the edit-menu-item form.
func (v Views) MenuItemForm(it menus.MenuItem) web.Renderer {
	return v.adminPage("Edit menu item", menuItemFormBody(it))
}

// MediaLibrary renders the media library + upload form.
func (v Views) MediaLibrary(assets []media.Asset, formError string) web.Renderer {
	return v.adminPage("Media", mediaLibraryBody(assets, formError))
}

// InquiriesList renders the admin inquiry list.
func (v Views) InquiriesList(items []messaging.Inquiry) web.Renderer {
	return v.adminPage("Inquiries", inquiriesListBody(items))
}

// AdminError renders an admin error page.
func (v Views) AdminError(status int, message string) web.Renderer {
	return v.adminPage(statusText(status), adminErrorBody(status, message))
}

// SeedTemplates returns the default per-entry bindings for the seed content types
// (article, page), wrapping the bundled ArticleContent/PageContent bodies. The
// public chrome wraps these body renderers via Single.
func (v Views) SeedTemplates() []content.TemplateBinding {
	return []content.TemplateBinding{
		{Type: "article", Template: "default", Fn: func(e content.Entry) web.Renderer { return ArticleContent(e) }},
		{Type: "page", Template: "default", Fn: func(e content.Entry) web.Renderer { return PageContent(e) }},
	}
}
