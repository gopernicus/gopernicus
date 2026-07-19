// Package cms is the SSR HTTP driving adapter for the CMS: the registry-driven
// route table, the generic entry admin + public handlers, and the supporting
// taxonomy/menu/media/contact handlers. It depends on the domain + sdk, maps
// domain errors to HTML responses, and renders templ views.
package cms

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/features/cms/internal/logic/entrysvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// entryService is the narrow surface the entry handlers consume. *entrysvc.Service
// satisfies it; tests substitute a fake.
type entryService interface {
	Create(ctx context.Context, typeSlug string, in entrysvc.Input) (content.Entry, error)
	Edit(ctx context.Context, id string, in entrysvc.Input) (content.Entry, error)
	Get(ctx context.Context, id string) (content.Entry, error)
	GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error)
	ListByTerm(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error)
	Publish(ctx context.Context, id string) (content.Entry, error)
	Unpublish(ctx context.Context, id string) (content.Entry, error)
	SetTerms(ctx context.Context, entryID string, termIDs []string) error
}

// EntryHandlers are the generic admin CRUD handlers for any registered content
// type. The content type is bound per-route at mount time (Mount iterates the
// registry), so every method takes the resolved ContentType — one handler set
// serves Articles, Pages, and every host-registered type.
type EntryHandlers struct {
	svc   entryService
	taxo  taxonomyService
	media mediaService
	views Views
}

// NewEntryHandlers constructs the entry handlers over the content + taxonomy +
// media services (taxonomy powers term checkboxes; media powers image pickers)
// and the HTML rendering port.
func NewEntryHandlers(svc entryService, taxo taxonomyService, media mediaService, views Views) *EntryHandlers {
	return &EntryHandlers{svc: svc, taxo: taxo, media: media, views: views}
}

// List renders the admin index for ct. It parses order (against the content
// allow-list; a bad value falls back to the default order per Q3 — never a
// 4xx/5xx), limit (NormalizedLimit clamp semantics), and cursor, and threads the
// page's bidirectional pagination state through the Pager.
func (h *EntryHandlers) List(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	order, linkOrder := parseEntryOrder(r)
	status := parseEntryStatus(r)
	q := content.EntryQuery{Type: ct.Slug, Status: status, ListRequest: crud.ListRequest{
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  atoiOrZero(r.URL.Query().Get("limit")),
		Order:  order,
	}}
	page, err := h.svc.List(r.Context(), q)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	items := make([]EntryListItem, 0, len(page.Items))
	for _, e := range page.Items {
		items = append(items, EntryListItem{ID: e.ID, Title: e.Title, Slug: e.Slug, Status: string(e.Status)})
	}
	base := "/" + ct.AdminBase()
	pager := Pager{
		NextCursor:     page.NextCursor,
		HasPrev:        page.HasPrev,
		PreviousCursor: page.PreviousCursor,
		Order:          linkOrder,
		Status:         string(status),
		BaseHref:       base,
	}
	// HTMX enhancement: a sort/filter/page request carries HX-Request, so we return
	// ONLY the swappable content region; a non-HTMX request (or no-JS) gets the full
	// document. Both render the same server-owned content, so the HTMX path degrades
	// to a full-document reload. The HX-Request header is a presentation hint read
	// directly here — the feature core takes no dependency on any HTMX/UI package,
	// and no identity/CSRF/authorization is ever derived from it.
	if isHTMX(r) {
		web.Render(r.Context(), w, http.StatusOK, h.views.EntriesListContent(ct.Plural, base+"/new", base, items, pager))
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.EntriesList(ct.Plural, base+"/new", base, items, pager))
}

// parseEntryStatus resolves the admin list's ?status filter against the content
// status set. An unknown value falls back to "any" (no 4xx/5xx), matching the Q3
// order-fallback discipline, and is NOT carried into the links.
func parseEntryStatus(r *http.Request) content.Status {
	switch content.Status(r.URL.Query().Get("status")) {
	case content.StatusDraft:
		return content.StatusDraft
	case content.StatusPublished:
		return content.StatusPublished
	default:
		return "" // any
	}
}

// isHTMX reports whether r is an HTMX-issued request. It reads the HX-Request
// header as a presentation hint only (never an identity/CSRF/authorization
// signal), so the feature core needs no dependency on the UI/HTMX package.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// parseEntryOrder resolves the admin list's ?order param against the content
// allow-list. Per Q3, an unknown field/direction falls back to the default order
// (no 4xx/5xx) and is NOT propagated into the pagination links; a valid value is
// returned verbatim as linkOrder so the "Older →"/"← Newer" links carry it.
func parseEntryOrder(r *http.Request) (order crud.Order, linkOrder string) {
	raw := r.URL.Query().Get("order")
	resolved, err := crud.ParseOrder(content.OrderFields, raw, content.DefaultOrder)
	if err != nil {
		return content.DefaultOrder, ""
	}
	return resolved, raw
}

// New renders an empty create form for ct.
func (h *EntryHandlers) New(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	model := h.formModel(r.Context(), ct, "New "+ct.Singular, "/"+ct.AdminBase(), content.Entry{}, nil)
	web.Render(r.Context(), w, http.StatusOK, h.views.EntryForm(model))
}

// Create handles the create form submission for ct.
func (h *EntryHandlers) Create(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	in := h.formInput(r, ct)
	e, err := h.svc.Create(r.Context(), ct.Slug, in)
	if err != nil {
		h.renderFormError(w, r, ct, err, in, "New "+ct.Singular, "/"+ct.AdminBase())
		return
	}
	if err := h.svc.SetTerms(r.Context(), e.ID, r.PostForm["term_id"]); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/"+ct.AdminBase(), http.StatusSeeOther)
}

// Edit renders a prefilled edit form (which doubles as the admin entry view).
func (h *EntryHandlers) Edit(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	e, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	action := "/" + ct.AdminBase() + "/" + e.ID
	model := h.formModel(r.Context(), ct, "Edit "+ct.Singular, action, e, e.TermIDs)
	web.Render(r.Context(), w, http.StatusOK, h.views.EntryForm(model))
}

// Update handles the edit form submission for ct.
func (h *EntryHandlers) Update(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	in := h.formInput(r, ct)
	e, err := h.svc.Edit(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			h.renderError(w, r, err)
			return
		}
		h.renderFormError(w, r, ct, err, in, "Edit "+ct.Singular, "/"+ct.AdminBase()+"/"+id)
		return
	}
	if err := h.svc.SetTerms(r.Context(), e.ID, r.PostForm["term_id"]); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/"+ct.AdminBase()+"/"+e.ID+"/edit", http.StatusSeeOther)
}

// Publish marks an entry published and redirects back to its editor.
func (h *EntryHandlers) Publish(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	id := r.PathValue("id")
	if _, err := h.svc.Publish(r.Context(), id); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/"+ct.AdminBase()+"/"+id+"/edit", http.StatusSeeOther)
}

// Unpublish returns an entry to draft and redirects back to its editor.
func (h *EntryHandlers) Unpublish(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	id := r.PathValue("id")
	if _, err := h.svc.Unpublish(r.Context(), id); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/"+ct.AdminBase()+"/"+id+"/edit", http.StatusSeeOther)
}

// Delete removes an entry and redirects to the type's list.
func (h *EntryHandlers) Delete(w http.ResponseWriter, r *http.Request, ct content.ContentType) {
	if err := h.svc.Delete(r.Context(), r.PathValue("id")); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/"+ct.AdminBase(), http.StatusSeeOther)
}

// formInput parses the create/edit form into a service Input, including custom
// fields keyed "field_<key>".
func (h *EntryHandlers) formInput(r *http.Request, ct content.ContentType) entrysvc.Input {
	in := entrysvc.Input{
		Title:     r.PostForm.Get("title"),
		Excerpt:   r.PostForm.Get("excerpt"),
		Body:      r.PostForm.Get("body"),
		Author:    r.PostForm.Get("author"),
		Status:    content.Status(r.PostForm.Get("status")),
		Template:  r.PostForm.Get("template"),
		ParentID:  r.PostForm.Get("parent_id"),
		MenuOrder: atoiOrZero(r.PostForm.Get("menu_order")),
		Fields:    content.Fields{},
	}
	for _, def := range ct.Fields {
		in.Fields[def.Key] = content.Value{Raw: r.PostForm.Get("field_" + def.Key)}
	}
	return in
}

// formModel builds the editor model for ct, prefilled from e (zero Entry for a
// create form). checked are the term IDs to pre-check.
func (h *EntryHandlers) formModel(ctx context.Context, ct content.ContentType, heading, action string, e content.Entry, checked []string) EntryFormModel {
	m := EntryFormModel{
		Heading:      heading,
		Action:       action,
		Title:        e.Title,
		Excerpt:      e.Excerpt,
		Body:         e.Body,
		Author:       e.Author,
		Status:       statusOrDraft(e.Status),
		Hierarchical: ct.Hierarchical,
		ParentID:     e.ParentID,
		MenuOrder:    e.MenuOrder,
		Templates:    templateOptions(ct, e.Template),
		Fields:       h.fieldInputs(ctx, ct, e),
		Terms:        h.termChoices(ctx, checked),
	}
	if ct.Hierarchical {
		m.Parents = h.parentOptions(ctx, ct, e.ID, e.ParentID)
	}
	return m
}

// fieldInputs builds one FieldInput per the type's FieldDefs, prefilled from e
// and with options for image (media assets) and relation (entries of RelTo).
func (h *EntryHandlers) fieldInputs(ctx context.Context, ct content.ContentType, e content.Entry) []FieldInput {
	out := make([]FieldInput, 0, len(ct.Fields))
	for _, def := range ct.Fields {
		cur := e.Fields[def.Key].Raw
		fi := FieldInput{
			Key:      def.Key,
			Label:    def.DisplayLabel(),
			Kind:     string(def.Kind),
			Help:     def.Help,
			Required: def.Required,
			Value:    cur,
			Checked:  cur == "true",
		}
		switch def.Kind {
		case content.KindImage:
			fi.Options = h.assetOptions(ctx, cur)
		case content.KindRelation:
			fi.Options = h.relationOptions(ctx, def.RelTo, cur)
		}
		out = append(out, fi)
	}
	return out
}

// assetOptions lists media assets as select options, marking selected.
func (h *EntryHandlers) assetOptions(ctx context.Context, selected string) []SelectOption {
	assets, err := h.media.ListAssets(ctx)
	if err != nil {
		return nil
	}
	out := make([]SelectOption, 0, len(assets))
	for _, a := range assets {
		out = append(out, SelectOption{Value: a.ID, Label: a.Filename, Selected: a.ID == selected})
	}
	return out
}

// relationOptions lists published+draft entries of relTo as select options.
func (h *EntryHandlers) relationOptions(ctx context.Context, relTo, selected string) []SelectOption {
	page, err := h.svc.List(ctx, content.EntryQuery{Type: relTo, ListRequest: crud.ListRequest{Limit: crud.MaxLimit}})
	if err != nil {
		return nil
	}
	out := make([]SelectOption, 0, len(page.Items))
	for _, e := range page.Items {
		out = append(out, SelectOption{Value: e.ID, Label: e.Title, Selected: e.ID == selected})
	}
	return out
}

// parentOptions lists candidate parents (entries of ct except exclude).
func (h *EntryHandlers) parentOptions(ctx context.Context, ct content.ContentType, exclude, selected string) []SelectOption {
	page, err := h.svc.List(ctx, content.EntryQuery{Type: ct.Slug, ListRequest: crud.ListRequest{Limit: crud.MaxLimit}})
	if err != nil {
		return nil
	}
	out := make([]SelectOption, 0, len(page.Items))
	for _, e := range page.Items {
		if e.ID == exclude {
			continue
		}
		out = append(out, SelectOption{Value: e.ID, Label: e.Title, Selected: e.ID == selected})
	}
	return out
}

// termChoices builds the term checkbox list, marking checked those in checked.
func (h *EntryHandlers) termChoices(ctx context.Context, checked []string) []TermChoice {
	on := map[string]bool{}
	for _, id := range checked {
		on[id] = true
	}
	var out []TermChoice
	for _, kind := range []taxonomy.Kind{taxonomy.KindCategory, taxonomy.KindTag} {
		terms, err := h.taxo.ListTerms(ctx, kind)
		if err != nil {
			return out
		}
		for _, t := range terms {
			out = append(out, TermChoice{ID: t.ID, Label: string(t.Kind) + ": " + t.Name, Checked: on[t.ID]})
		}
	}
	return out
}

// renderError maps a domain error to an HTML error page.
func (h *EntryHandlers) renderError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := web.ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		web.RecordError(w, err)
	}
	web.Render(r.Context(), w, mapped.Status, h.views.AdminError(mapped.Status, mapped.Message))
}

// renderFormError re-renders the editor with the validation/collision message.
func (h *EntryHandlers) renderFormError(w http.ResponseWriter, r *http.Request, ct content.ContentType, err error, in entrysvc.Input, heading, action string) {
	status := http.StatusBadRequest
	model := h.formModel(r.Context(), ct, heading, action, entryFromInput(ct, in), r.PostForm["term_id"])
	switch {
	case errors.Is(err, sdk.ErrAlreadyExists):
		status = http.StatusConflict
		model.FormError = "A " + ct.Singular + " with that title (slug) already exists."
	case errors.Is(err, sdk.ErrInvalidInput):
		model.FormError = err.Error()
	default:
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, status, h.views.EntryForm(model))
}

// entryFromInput reconstructs a partial Entry from a rejected form submission so
// the re-rendered editor keeps the user's values.
func entryFromInput(ct content.ContentType, in entrysvc.Input) content.Entry {
	e := content.Entry{
		Title: in.Title, Excerpt: in.Excerpt, Body: in.Body, Author: in.Author,
		Status: in.Status, Template: in.Template, ParentID: in.ParentID, MenuOrder: in.MenuOrder,
		Fields: in.Fields,
	}
	return e
}

// templateOptions builds the template-picker options for ct.
func templateOptions(ct content.ContentType, selected string) []SelectOption {
	if selected == "" {
		selected = ct.DefaultTemplate()
	}
	out := make([]SelectOption, 0, len(ct.Templates))
	for _, t := range ct.Templates {
		out = append(out, SelectOption{Value: t, Label: t, Selected: t == selected})
	}
	return out
}

// statusOrDraft returns s, defaulting an empty status to draft for new forms.
func statusOrDraft(s content.Status) string {
	if s == "" {
		return string(content.StatusDraft)
	}
	return string(s)
}

// atoiOrZero parses a base-10 int, returning 0 on any error.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
