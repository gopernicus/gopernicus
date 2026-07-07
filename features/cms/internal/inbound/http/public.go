package http

import (
	"context"
	"net/http"
	"sort"

	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/features/cms/logic/taxonomy"
	"github.com/gopernicus/gopernicus/features/cms/theme"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// homeEntryLimit caps how many recent entries the home page shows.
const homeEntryLimit = 5

// archiveType is the content type whose entries taxonomy archives list. v1 is
// article-only (plan §"Open", resolved 2026-06-22); generalize later.
const archiveType = "article"

// PublicHandlers render the public-facing themed site. Per-entry bodies come
// from the registry's registered TemplateFuncs; this handler only sequences
// reads and wraps the result in site chrome.
type PublicHandlers struct {
	svc      entryService
	menus    menuService
	taxo     taxonomyService
	registry *content.Registry
	views    theme.PublicViews
}

// NewPublicHandlers constructs the public site handlers. A nil views uses the
// bundled default chrome.
func NewPublicHandlers(svc entryService, menusvc menuService, taxo taxonomyService, registry *content.Registry, views theme.PublicViews) *PublicHandlers {
	if views == nil {
		views = theme.Default()
	}
	return &PublicHandlers{svc: svc, menus: menusvc, taxo: taxo, registry: registry, views: views}
}

// nav loads the "main" menu's items for the public layout (empty if absent).
func (h *PublicHandlers) nav(ctx context.Context) []menus.MenuItem {
	m, err := h.menus.GetMenuBySlug(ctx, "main")
	if err != nil {
		return nil
	}
	items, err := h.menus.Items(ctx, m.ID)
	if err != nil {
		return nil
	}
	return items
}

// Home renders recent published entries across all routable, non-hierarchical
// types, newest first.
func (h *PublicHandlers) Home(w http.ResponseWriter, r *http.Request) {
	var entries []content.Entry
	for _, ct := range h.registry.Types() {
		if !ct.Routable || ct.Hierarchical {
			continue
		}
		page, err := h.svc.List(r.Context(), content.EntryQuery{
			Type:        ct.Slug,
			Status:      content.StatusPublished,
			ListRequest: crud.ListRequest{Limit: crud.MaxLimit},
		})
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		entries = append(entries, page.Items...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	if len(entries) > homeEntryLimit {
		entries = entries[:homeEntryLimit]
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.Home(h.nav(r.Context()), h.listItems(entries)))
}

// Single renders one published entry of ct by slug, resolving its registered
// per-(type,template) renderer and wrapping it in chrome. Drafts and missing
// entries are 404.
func (h *PublicHandlers) Single(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	e, err := h.svc.GetBySlug(r.Context(), ct.Slug, r.PathValue("slug"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if e.Status != content.StatusPublished {
		h.notFound(w, r)
		return
	}
	body, ok := h.registry.Render(e)
	if !ok {
		h.notFound(w, r)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.Single(e.Title, e.Excerpt, h.nav(r.Context()), body))
}

// Category renders the archive for a category slug.
func (h *PublicHandlers) Category(w http.ResponseWriter, r *http.Request) {
	h.archive(w, r, taxonomy.KindCategory)
}

// Tag renders the archive for a tag slug.
func (h *PublicHandlers) Tag(w http.ResponseWriter, r *http.Request) {
	h.archive(w, r, taxonomy.KindTag)
}

func (h *PublicHandlers) archive(w http.ResponseWriter, r *http.Request, kind taxonomy.Kind) {
	term, err := h.taxo.GetTermBySlug(r.Context(), kind, r.PathValue("slug"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	page, err := h.svc.ListByTerm(r.Context(), term.ID, content.EntryQuery{
		Type:        archiveType,
		Status:      content.StatusPublished,
		ListRequest: crud.ListRequest{Cursor: r.URL.Query().Get("cursor")},
	})
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	heading := string(kind) + ": " + term.Name
	base := "/" + string(kind) + "/" + term.Slug
	web.Render(r.Context(), w, http.StatusOK, h.views.Archive(heading, h.nav(r.Context()), h.listItems(page.Items), page.NextCursor, base))
}

// listItems maps entries to theme list items, computing each public href from
// its type's route scheme.
func (h *PublicHandlers) listItems(entries []content.Entry) []theme.ListItem {
	out := make([]theme.ListItem, 0, len(entries))
	for _, e := range entries {
		ct, ok := h.registry.Type(e.Type)
		if !ok {
			continue
		}
		out = append(out, theme.ListItem{Title: e.Title, Href: publicHref(ct, e.Slug), Excerpt: e.Excerpt})
	}
	return out
}

// publicHref builds the public URL for an entry of ct with the given slug.
func publicHref(ct content.ContentType, slug string) string {
	if base := ct.PublicBase(); base != "" {
		return "/" + base + "/" + slug
	}
	return "/" + slug
}

func (h *PublicHandlers) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(r.Context(), w, http.StatusNotFound, h.views.Error(http.StatusNotFound, "not found"))
}

func (h *PublicHandlers) renderError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := web.ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		web.RecordError(w, err)
	}
	web.Render(r.Context(), w, mapped.Status, h.views.Error(mapped.Status, mapped.Message))
}
