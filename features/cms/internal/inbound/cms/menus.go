package cms

import (
	"context"
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// menuService is the narrow surface the menu handlers consume.
type menuService interface {
	CreateMenu(ctx context.Context, name string) (menus.Menu, error)
	GetMenu(ctx context.Context, id string) (menus.Menu, error)
	GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error)
	ListMenus(ctx context.Context) ([]menus.Menu, error)
	Items(ctx context.Context, menuID string) ([]menus.MenuItem, error)
	AddMenuItem(ctx context.Context, menuID, label, url, parentID string, position int) (menus.MenuItem, error)
	GetMenuItem(ctx context.Context, id string) (menus.MenuItem, error)
	EditMenuItem(ctx context.Context, id, label, url, parentID string, position int) (menus.MenuItem, error)
	RemoveMenuItem(ctx context.Context, id string) error
}

// MenuHandlers holds the menu admin handlers + a public nav render.
type MenuHandlers struct {
	svc   menuService
	views Views
}

// NewMenuHandlers constructs the menu handlers over svc and the HTML port.
func NewMenuHandlers(svc menuService, views Views) *MenuHandlers {
	return &MenuHandlers{svc: svc, views: views}
}

// List renders all menus.
func (h *MenuHandlers) List(w http.ResponseWriter, r *http.Request) {
	ms, err := h.svc.ListMenus(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.MenusList(ms))
}

// New renders the create-menu form.
func (h *MenuHandlers) New(w http.ResponseWriter, r *http.Request) {
	web.Render(r.Context(), w, http.StatusOK, h.views.MenuNew(""))
}

// Create handles the create-menu form.
func (h *MenuHandlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	m, err := h.svc.CreateMenu(r.Context(), r.PostForm.Get("name"))
	if err != nil {
		if errors.Is(err, sdk.ErrInvalidInput) || errors.Is(err, sdk.ErrAlreadyExists) {
			web.Render(r.Context(), w, http.StatusBadRequest, h.views.MenuNew(err.Error()))
			return
		}
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/menus/"+m.ID, http.StatusSeeOther)
}

// Detail renders a menu with its items and the add-item form.
func (h *MenuHandlers) Detail(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.GetMenu(r.Context(), r.PathValue("id"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	items, err := h.svc.Items(r.Context(), m.ID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.MenuDetail(m, items))
}

// AddItem appends an item to a menu.
func (h *MenuHandlers) AddItem(w http.ResponseWriter, r *http.Request) {
	menuID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	_, err := h.svc.AddMenuItem(r.Context(), menuID,
		r.PostForm.Get("label"), r.PostForm.Get("url"), r.PostForm.Get("parent_id"),
		atoiOrZero(r.PostForm.Get("position")))
	if err != nil && !errors.Is(err, sdk.ErrInvalidInput) {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/menus/"+menuID, http.StatusSeeOther)
}

// EditItem renders the edit-item form.
func (h *MenuHandlers) EditItem(w http.ResponseWriter, r *http.Request) {
	it, err := h.svc.GetMenuItem(r.Context(), r.PathValue("id"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.MenuItemForm(it))
}

// UpdateItem handles the edit-item form.
func (h *MenuHandlers) UpdateItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	it, err := h.svc.EditMenuItem(r.Context(), id,
		r.PostForm.Get("label"), r.PostForm.Get("url"), r.PostForm.Get("parent_id"),
		atoiOrZero(r.PostForm.Get("position")))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/menus/"+it.MenuID, http.StatusSeeOther)
}

// DeleteItem removes an item and returns to its menu.
func (h *MenuHandlers) DeleteItem(w http.ResponseWriter, r *http.Request) {
	it, err := h.svc.GetMenuItem(r.Context(), r.PathValue("id"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := h.svc.RemoveMenuItem(r.Context(), it.ID); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/menus/"+it.MenuID, http.StatusSeeOther)
}

// PublicNav renders a menu's items as a nav tree (public, by slug).
func (h *MenuHandlers) PublicNav(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.GetMenuBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	items, err := h.svc.Items(r.Context(), m.ID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.MenuNav(m, items))
}

func (h *MenuHandlers) renderError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := web.ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		web.RecordError(w, err)
	}
	web.Render(r.Context(), w, mapped.Status, h.views.AdminError(mapped.Status, mapped.Message))
}
