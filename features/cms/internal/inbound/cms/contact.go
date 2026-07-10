package cms

import (
	"context"
	"errors"
	"net/http"

	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// messagingService is the narrow surface the contact handlers consume.
type messagingService interface {
	Submit(ctx context.Context, name, email, message string) (messaging.Inquiry, error)
	ListInquiries(ctx context.Context) ([]messaging.Inquiry, error)
}

// ContactHandlers holds the public contact form + the admin inquiry list.
type ContactHandlers struct {
	svc   messagingService
	views Views
}

// NewContactHandlers constructs the contact handlers over svc and the HTML port.
func NewContactHandlers(svc messagingService, views Views) *ContactHandlers {
	return &ContactHandlers{svc: svc, views: views}
}

// Form renders the public contact form.
func (h *ContactHandlers) Form(w http.ResponseWriter, r *http.Request) {
	web.Render(r.Context(), w, http.StatusOK, h.views.ContactForm(ContactModel{}))
}

// Submit handles a contact-form submission.
func (h *ContactHandlers) Submit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, err)
		return
	}
	model := ContactModel{
		Name:    r.PostForm.Get("name"),
		Email:   r.PostForm.Get("email"),
		Message: r.PostForm.Get("message"),
	}

	if _, err := h.svc.Submit(r.Context(), model.Name, model.Email, model.Message); err != nil {
		if errors.Is(err, sdk.ErrInvalidInput) {
			model.FormError = err.Error()
			web.Render(r.Context(), w, http.StatusBadRequest, h.views.ContactForm(model))
			return
		}
		// Inquiry may still be persisted (notification failure); surface a 500.
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.ContactThanks())
}

// Inquiries renders the admin list of received inquiries.
func (h *ContactHandlers) Inquiries(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListInquiries(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.InquiriesList(items))
}

func (h *ContactHandlers) renderError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := web.ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		web.RecordError(w, err)
	}
	web.Render(r.Context(), w, mapped.Status, h.views.AdminError(mapped.Status, mapped.Message))
}
