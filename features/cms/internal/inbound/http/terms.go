package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/cms/internal/inbound/http/views"
	"github.com/gopernicus/gopernicus/features/cms/logic/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// taxonomyService is the narrow surface the taxonomy handlers consume.
type taxonomyService interface {
	CreateTerm(ctx context.Context, kind taxonomy.Kind, name, parentID string) (taxonomy.Term, error)
	GetTerm(ctx context.Context, id string) (taxonomy.Term, error)
	GetTermBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error)
	ListTerms(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error)
	EditTerm(ctx context.Context, id, name, parentID string) (taxonomy.Term, error)
	DeleteTerm(ctx context.Context, id string) error
}

// TermHandlers holds the taxonomy admin handlers.
type TermHandlers struct {
	svc taxonomyService
}

// NewTermHandlers constructs the term handlers over svc.
func NewTermHandlers(svc taxonomyService) *TermHandlers {
	return &TermHandlers{svc: svc}
}

// List renders categories and tags.
func (h *TermHandlers) List(w http.ResponseWriter, r *http.Request) {
	cats, err := h.svc.ListTerms(r.Context(), taxonomy.KindCategory)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	tags, err := h.svc.ListTerms(r.Context(), taxonomy.KindTag)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, views.TermsList(cats, tags))
}

// New renders an empty term form. The kind is taken from ?kind= (default tag).
func (h *TermHandlers) New(w http.ResponseWriter, r *http.Request) {
	kind := taxonomy.Kind(r.URL.Query().Get("kind"))
	if !kind.Valid() {
		kind = taxonomy.KindTag
	}
	web.Render(r.Context(), w, http.StatusOK, views.TermForm(views.TermFormModel{
		Heading: "New " + string(kind),
		Action:  "/terms",
		Kind:    string(kind),
	}))
}

// Create handles the term create form.
func (h *TermHandlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	kind := taxonomy.Kind(r.PostForm.Get("kind"))
	name := r.PostForm.Get("name")
	parentID := r.PostForm.Get("parent_id")

	if _, err := h.svc.CreateTerm(r.Context(), kind, name, parentID); err != nil {
		h.renderTermFormError(w, r, err, views.TermFormModel{
			Heading: "New " + string(kind), Action: "/terms", Kind: string(kind), Name: name, ParentID: parentID,
		})
		return
	}
	web.RespondRedirect(w, r, "/terms", http.StatusSeeOther)
}

// Edit renders a prefilled term form.
func (h *TermHandlers) Edit(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.GetTerm(r.Context(), r.PathValue("id"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, views.TermForm(views.TermFormModel{
		Heading:  "Edit " + string(t.Kind),
		Action:   "/terms/" + t.ID,
		Kind:     string(t.Kind),
		Name:     t.Name,
		ParentID: t.ParentID,
	}))
}

// Update handles the term edit form.
func (h *TermHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	name := r.PostForm.Get("name")
	parentID := r.PostForm.Get("parent_id")

	if _, err := h.svc.EditTerm(r.Context(), id, name, parentID); err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			h.renderError(w, r, err)
			return
		}
		h.renderTermFormError(w, r, err, views.TermFormModel{
			Heading: "Edit term", Action: "/terms/" + id, Name: name, ParentID: parentID,
		})
		return
	}
	web.RespondRedirect(w, r, "/terms", http.StatusSeeOther)
}

// Delete removes a term.
func (h *TermHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteTerm(r.Context(), r.PathValue("id")); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/terms", http.StatusSeeOther)
}

func (h *TermHandlers) renderError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := web.ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		web.RecordError(w, err)
	}
	web.Render(r.Context(), w, mapped.Status, views.ErrorPage(mapped.Status, mapped.Message))
}

func (h *TermHandlers) renderTermFormError(w http.ResponseWriter, r *http.Request, err error, model views.TermFormModel) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errs.ErrAlreadyExists):
		status = http.StatusConflict
		model.FormError = "A term of that kind with the same slug already exists."
	case errors.Is(err, errs.ErrInvalidInput):
		model.FormError = err.Error()
	default:
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, status, views.TermForm(model))
}
