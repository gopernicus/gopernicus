package cms

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/cms/domain/media"
)

func TestMedia_LibraryAndServe(t *testing.T) {
	asset := media.Asset{ID: "a1", Filename: "logo.png", ContentType: "image/png", Size: 9, StorageKey: "assets/a1/logo.png"}
	svc := &fakeMediaSvc{
		listFn: func(ctx context.Context) ([]media.Asset, error) { return []media.Asset{asset}, nil },
		openFn: func(ctx context.Context, id string) (media.Asset, io.ReadCloser, error) {
			return asset, io.NopCloser(strings.NewReader("PNG-BYTES")), nil
		},
	}

	// Library lists the asset + upload form.
	rec := httptest.NewRecorder()
	mediaRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/media", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "logo.png") || !strings.Contains(body, `enctype="multipart/form-data"`) {
		t.Fatalf("library: status=%d", rec.Code)
	}

	// Serve streams bytes with content type.
	rec = httptest.NewRecorder()
	mediaRouter(svc).ServeHTTP(rec, httptest.NewRequest("GET", "/media/a1/file", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != "PNG-BYTES" {
		t.Fatalf("serve: status=%d ct=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}
