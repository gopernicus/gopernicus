package cms

import (
	"context"
	"io"
	"net/http"

	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// maxUploadBytes caps a single multipart upload (32 MiB).
const maxUploadBytes = 32 << 20

// mediaService is the narrow surface the media handlers consume.
type mediaService interface {
	Upload(ctx context.Context, filename, contentType string, size int64, reader io.Reader) (media.Asset, error)
	GetAsset(ctx context.Context, id string) (media.Asset, error)
	ListAssets(ctx context.Context) ([]media.Asset, error)
	OpenAsset(ctx context.Context, id string) (media.Asset, io.ReadCloser, error)
	DeleteAsset(ctx context.Context, id string) error
}

// MediaHandlers holds the media library + upload + serve handlers. The admin
// handlers (Library/Upload/Delete) render through views; Serve is the sole
// non-HTML endpoint and never touches views (it mounts even when views is nil).
type MediaHandlers struct {
	svc   mediaService
	views Views
}

// NewMediaHandlers constructs the media handlers over svc and the HTML port. A
// nil views is valid: only Serve is used in that case (see Mount's nil branch).
func NewMediaHandlers(svc mediaService, views Views) *MediaHandlers {
	return &MediaHandlers{svc: svc, views: views}
}

// Library lists assets and renders the upload form.
func (h *MediaHandlers) Library(w http.ResponseWriter, r *http.Request) {
	assets, err := h.svc.ListAssets(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.views.MediaLibrary(assets, ""))
}

// Upload handles the multipart upload form.
func (h *MediaHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		h.renderLibraryError(w, r, "upload too large or malformed")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderLibraryError(w, r, "choose a file to upload")
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	if _, err := h.svc.Upload(r.Context(), header.Filename, contentType, header.Size, file); err != nil {
		h.renderLibraryError(w, r, err.Error())
		return
	}
	web.RespondRedirect(w, r, "/media", http.StatusSeeOther)
}

// Serve streams an asset's bytes with its content type. Its error path responds
// with JSON (web.RespondJSONDomainError): this is a byte endpoint, not part of
// the HTML surface, so it mounts even when views is nil and never renders an
// HTML error page.
func (h *MediaHandlers) Serve(w http.ResponseWriter, r *http.Request) {
	a, rc, err := h.svc.OpenAsset(r.Context(), r.PathValue("id"))
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	defer rc.Close()

	if a.ContentType != "" {
		w.Header().Set("Content-Type", a.ContentType)
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	io.Copy(w, rc)
}

// Delete removes an asset and returns to the library.
func (h *MediaHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteAsset(r.Context(), r.PathValue("id")); err != nil {
		h.renderError(w, r, err)
		return
	}
	web.RespondRedirect(w, r, "/media", http.StatusSeeOther)
}

func (h *MediaHandlers) renderError(w http.ResponseWriter, r *http.Request, err error) {
	mapped := web.ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		web.RecordError(w, err)
	}
	web.Render(r.Context(), w, mapped.Status, h.views.AdminError(mapped.Status, mapped.Message))
}

func (h *MediaHandlers) renderLibraryError(w http.ResponseWriter, r *http.Request, msg string) {
	assets, _ := h.svc.ListAssets(r.Context())
	web.Render(r.Context(), w, http.StatusBadRequest, h.views.MediaLibrary(assets, msg))
}
