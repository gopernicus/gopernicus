package mediasvc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/sdk"
)

// fakeAssets is an in-memory AssetRepository.
type fakeAssets struct{ byID map[string]media.Asset }

func newFakeAssets() *fakeAssets { return &fakeAssets{byID: map[string]media.Asset{}} }

func (f *fakeAssets) Create(ctx context.Context, a media.Asset) (media.Asset, error) {
	f.byID[a.ID] = a
	return a, nil
}
func (f *fakeAssets) Get(ctx context.Context, id string) (media.Asset, error) {
	a, ok := f.byID[id]
	if !ok {
		return media.Asset{}, sdk.ErrNotFound
	}
	return a, nil
}
func (f *fakeAssets) List(ctx context.Context) ([]media.Asset, error) {
	var out []media.Asset
	for _, a := range f.byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (f *fakeAssets) Delete(ctx context.Context, id string) error {
	delete(f.byID, id)
	return nil
}

// memBlobs is an in-memory BlobStore.
type memBlobs struct{ data map[string][]byte }

func newMemBlobs() *memBlobs { return &memBlobs{data: map[string][]byte{}} }

func (m *memBlobs) Upload(ctx context.Context, path string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.data[path] = b
	return nil
}
func (m *memBlobs) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	b, ok := m.data[path]
	if !ok {
		return nil, sdk.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memBlobs) Delete(ctx context.Context, path string) error {
	delete(m.data, path)
	return nil
}

func TestMedia_UploadOpenDelete(t *testing.T) {
	ctx := context.Background()
	blobs := newMemBlobs()
	now := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	svc := NewService(newFakeAssets(), blobs, sdk.IDGenerator{}, func() time.Time { return now })
	if err := svc.SetUploadPolicy(UploadPolicy{AllowedMediaTypes: []string{"image/png"}}); err != nil {
		t.Fatalf("SetUploadPolicy: %v", err)
	}

	content := "PNG-BYTES"
	a, err := svc.Upload(ctx, "My Logo.PNG", "image/png", int64(len(content)), strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !strings.HasPrefix(a.StorageKey, "assets/") || !strings.HasSuffix(a.StorageKey, "/my-logo.png") {
		t.Errorf("unexpected storage key: %q", a.StorageKey)
	}
	if _, ok := blobs.data[a.StorageKey]; !ok {
		t.Errorf("bytes not stored at %q", a.StorageKey)
	}

	// Open returns the bytes back.
	got, rc, err := svc.OpenAsset(ctx, a.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if got.ID != a.ID || string(b) != content {
		t.Errorf("open mismatch: %+v %q", got, string(b))
	}

	// List has it.
	if list, _ := svc.ListAssets(ctx); len(list) != 1 {
		t.Errorf("expected 1 asset, got %d", len(list))
	}

	// Delete removes bytes + metadata.
	if err := svc.DeleteAsset(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := blobs.data[a.StorageKey]; ok {
		t.Errorf("bytes not deleted")
	}
	if _, err := svc.GetAsset(ctx, a.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("metadata not deleted")
	}
}

func TestMedia_UploadValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeAssets(), newMemBlobs(), sdk.IDGenerator{}, nil)

	if _, err := svc.Upload(ctx, "", "image/png", 1, strings.NewReader("x")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("blank filename: %v", err)
	}
	if _, err := svc.Upload(ctx, "f.png", "", 1, strings.NewReader("x")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("blank content type: %v", err)
	}
	if _, err := svc.Upload(ctx, "f.png", "image/png", 0, strings.NewReader("")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty file: %v", err)
	}
}

func TestMedia_UploadPolicy(t *testing.T) {
	ctx := context.Background()
	blobs := newMemBlobs()
	assets := newFakeAssets()
	svc := NewService(assets, blobs, sdk.IDGenerator{}, nil)

	// No policy installed: every upload is refused.
	if _, err := svc.Upload(ctx, "f.png", "image/png", 1, strings.NewReader("x")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("upload with no policy: %v, want sdk.ErrInvalidInput", err)
	}

	if err := svc.SetUploadPolicy(UploadPolicy{AllowedMediaTypes: []string{"IMAGE/PNG", "application/pdf; charset=binary"}}); err != nil {
		t.Fatalf("SetUploadPolicy: %v", err)
	}

	// Listed types are stored; parsing makes case and parameters irrelevant.
	for _, ct := range []string{"image/png", "application/pdf", "Image/Png; foo=bar"} {
		a, err := svc.Upload(ctx, "f.bin", ct, 1, strings.NewReader("x"))
		if err != nil {
			t.Fatalf("upload %q: %v", ct, err)
		}
		if _, ok := blobs.data[a.StorageKey]; !ok {
			t.Errorf("upload %q: bytes not stored", ct)
		}
	}

	// An unlisted type is refused before any byte is stored, naming the list.
	stored := len(blobs.data)
	_, err := svc.Upload(ctx, "evil.png", "text/html", 1, strings.NewReader("<script>"))
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unlisted type: %v, want sdk.ErrInvalidInput", err)
	}
	if want := "unsupported file type; allowed: image/png, application/pdf"; !strings.Contains(err.Error(), want) {
		t.Errorf("unlisted type message = %q, want it to contain %q", err.Error(), want)
	}
	if len(blobs.data) != stored || len(assets.byID) != stored {
		t.Errorf("refused upload stored bytes or metadata")
	}

	// Validation still answers first: an empty file is "file is empty".
	if _, err := svc.Upload(ctx, "f.png", "text/html", 0, strings.NewReader("")); err == nil || !strings.Contains(err.Error(), "file is empty") {
		t.Errorf("empty unlisted upload: %v, want the empty-file error", err)
	}
}

func TestMedia_SetUploadPolicyRefusesUnparsableEntry(t *testing.T) {
	svc := NewService(newFakeAssets(), newMemBlobs(), sdk.IDGenerator{}, nil)
	if err := svc.SetUploadPolicy(UploadPolicy{AllowedMediaTypes: []string{"image/png"}}); err != nil {
		t.Fatalf("SetUploadPolicy: %v", err)
	}
	if err := svc.SetUploadPolicy(UploadPolicy{AllowedMediaTypes: []string{"image/png", "not a type"}}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unparsable entry: %v, want sdk.ErrInvalidInput", err)
	}
	if _, err := svc.Upload(context.Background(), "f.png", "image/png", 1, strings.NewReader("x")); err != nil {
		t.Errorf("refused policy replaced the previous one: %v", err)
	}
}
