package cms

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	// defaultMaxUploadBytes caps an upload's request body when the host sets no
	// cms.Config.MaxUploadBytes (32 MiB).
	defaultMaxUploadBytes = 32 << 20
	// multipartMemoryBytes is ParseMultipartForm's in-memory limit; larger
	// files spill to temporary files. Fixed so a raised upload cap never
	// buffers the whole body in memory.
	multipartMemoryBytes = 32 << 20
	// sniffBytes is how much of a file http.DetectContentType considers.
	sniffBytes = 512
)

// inlineMediaTypes are the asset types Serve lets a browser render in place;
// every other type is served as an attachment (pdf included).
var inlineMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

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
	svc            mediaService
	views          Views
	maxUploadBytes int64
}

// NewMediaHandlers constructs the media handlers over svc and the HTML port. A
// nil views is valid: only Serve is used in that case (see Mount's nil branch).
// maxUploadBytes caps an upload's request body; zero selects 32 MiB.
func NewMediaHandlers(svc mediaService, views Views, maxUploadBytes int64) *MediaHandlers {
	if maxUploadBytes == 0 {
		maxUploadBytes = defaultMaxUploadBytes
	}
	return &MediaHandlers{svc: svc, views: views, maxUploadBytes: maxUploadBytes}
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

// Upload handles the multipart upload form. A body past the cap answers 413.
// The file's type is sniffed from its first bytes (http.DetectContentType); the
// part's client-sent Content-Type is never consulted.
func (h *MediaHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
			h.renderLibraryError(w, r, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		h.renderLibraryError(w, r, http.StatusBadRequest, "upload malformed")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderLibraryError(w, r, http.StatusBadRequest, "choose a file to upload")
		return
	}
	defer file.Close()

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		h.renderError(w, r, err)
		return
	}
	head = head[:n]
	contentType, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		contentType = "application/octet-stream"
	}
	body := io.MultiReader(bytes.NewReader(head), file)
	if _, err := h.svc.Upload(r.Context(), header.Filename, contentType, header.Size, body); err != nil {
		h.renderLibraryError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	web.RespondRedirect(w, r, "/media", http.StatusSeeOther)
}

// Serve streams an asset's bytes with its content type, nosniff, and a
// Content-Disposition: inline for png/jpeg/gif/webp, attachment for every other
// type. Its error path responds with JSON (web.RespondJSONDomainError): this is
// a byte endpoint, not part of the HTML surface, so it mounts even when views is
// nil and never renders an HTML error page.
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(a))
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

func (h *MediaHandlers) renderLibraryError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	assets, _ := h.svc.ListAssets(r.Context())
	web.Render(r.Context(), w, status, h.views.MediaLibrary(assets, msg))
}

// contentDisposition builds the asset's Content-Disposition with
// mime.FormatMediaType, which quotes or RFC 2231-encodes the filename.
func contentDisposition(a media.Asset) string {
	disposition := "attachment"
	if mt, _, err := mime.ParseMediaType(a.ContentType); err == nil && inlineMediaTypes[mt] {
		disposition = "inline"
	}
	if v := mime.FormatMediaType(disposition, map[string]string{"filename": dispositionFilename(a.Filename)}); v != "" {
		return v
	}
	return disposition
}

// dispositionFilename reduces a stored upload name to its last path element
// with control characters removed.
func dispositionFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '\\':
			return '/'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(path.Base(name))
	if name == "" || name == "." || name == "/" || name == ".." {
		return "file"
	}
	return name
}
