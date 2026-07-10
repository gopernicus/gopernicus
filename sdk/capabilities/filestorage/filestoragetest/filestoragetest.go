// Package filestoragetest is a conformance suite for filestorage.Storer
// implementations: every backend that satisfies the port should pass Run
// against a fresh instance. Modeled on the net/http/httptest /
// go/analysis/analysistest pattern. Imports stdlib + sdk/capabilities/filestorage only
// (sdk stays dependency-free per the constitution).
package filestoragetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

// Run exercises the filestorage.Storer contract against a fresh instance
// obtained from newStorer for each subtest.
func Run(t *testing.T, newStorer func(t *testing.T) filestorage.Storer) {
	t.Helper()

	t.Run("UploadDownloadRoundTrip", func(t *testing.T) { testUploadDownloadRoundTrip(t, newStorer(t)) })
	t.Run("Exists", func(t *testing.T) { testExists(t, newStorer(t)) })
	t.Run("Delete", func(t *testing.T) { testDelete(t, newStorer(t)) })
	t.Run("List", func(t *testing.T) { testList(t, newStorer(t)) })
	t.Run("DownloadRange", func(t *testing.T) { testDownloadRange(t, newStorer(t)) })
	t.Run("GetObjectSize", func(t *testing.T) { testGetObjectSize(t, newStorer(t)) })
	t.Run("NotFoundErrorMapping", func(t *testing.T) { testNotFoundErrorMapping(t, newStorer(t)) })
}

func testUploadDownloadRoundTrip(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	const path = "dir/file.txt"
	const content = "hello, filestorage"

	if err := s.Upload(ctx, path, bytes.NewReader([]byte(content))); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	rc, err := s.Download(ctx, path)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read downloaded content: %v", err)
	}
	if string(got) != content {
		t.Errorf("downloaded content = %q, want %q", got, content)
	}
}

func testExists(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	const path = "exists.txt"

	ok, err := s.Exists(ctx, path)
	if err != nil {
		t.Fatalf("Exists() before upload error = %v", err)
	}
	if ok {
		t.Error("Exists() before upload = true, want false")
	}

	if err := s.Upload(ctx, path, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	ok, err = s.Exists(ctx, path)
	if err != nil {
		t.Fatalf("Exists() after upload error = %v", err)
	}
	if !ok {
		t.Error("Exists() after upload = false, want true")
	}
}

func testDelete(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	const path = "to-delete.txt"

	if err := s.Upload(ctx, path, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if err := s.Delete(ctx, path); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	ok, err := s.Exists(ctx, path)
	if err != nil {
		t.Fatalf("Exists() after delete error = %v", err)
	}
	if ok {
		t.Error("Exists() after delete = true, want false")
	}
	// Deleting an object that was never uploaded must not error.
	if err := s.Delete(ctx, "never-uploaded.txt"); err != nil {
		t.Errorf("Delete(never-uploaded) error = %v, want nil", err)
	}
}

func testList(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	paths := []string{"list/a.txt", "list/b.txt", "other/c.txt"}
	for _, p := range paths {
		if err := s.Upload(ctx, p, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatalf("Upload(%s) error = %v", p, err)
		}
	}

	got, err := s.List(ctx, "list")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	sort.Strings(got)
	want := []string{"list/a.txt", "list/b.txt"}
	if len(got) != len(want) {
		t.Fatalf("List(list) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List(list)[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func testDownloadRange(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	const path = "range.txt"
	const content = "0123456789"

	if err := s.Upload(ctx, path, bytes.NewReader([]byte(content))); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	rc, err := s.DownloadRange(ctx, path, 2, 3)
	if err != nil {
		t.Fatalf("DownloadRange(2,3) error = %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if string(got) != "234" {
		t.Errorf("DownloadRange(2,3) = %q, want %q", got, "234")
	}

	rc, err = s.DownloadRange(ctx, path, 7, -1)
	if err != nil {
		t.Fatalf("DownloadRange(7,-1) error = %v", err)
	}
	got, err = io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read range to end: %v", err)
	}
	if string(got) != "789" {
		t.Errorf("DownloadRange(7,-1) = %q, want %q", got, "789")
	}
}

func testGetObjectSize(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	const path = "size.txt"
	const content = "0123456789"

	if err := s.Upload(ctx, path, bytes.NewReader([]byte(content))); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	size, err := s.GetObjectSize(ctx, path)
	if err != nil {
		t.Fatalf("GetObjectSize() error = %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("GetObjectSize() = %d, want %d", size, len(content))
	}
}

func testNotFoundErrorMapping(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()

	if _, err := s.Download(ctx, "does-not-exist.txt"); !errors.Is(err, filestorage.ErrObjectNotFound) {
		t.Errorf("Download(missing) error = %v, want errors.Is(_, filestorage.ErrObjectNotFound)", err)
	}
	if _, err := s.DownloadRange(ctx, "does-not-exist.txt", 0, -1); !errors.Is(err, filestorage.ErrObjectNotFound) {
		t.Errorf("DownloadRange(missing) error = %v, want errors.Is(_, filestorage.ErrObjectNotFound)", err)
	}
	if _, err := s.GetObjectSize(ctx, "does-not-exist.txt"); !errors.Is(err, filestorage.ErrObjectNotFound) {
		t.Errorf("GetObjectSize(missing) error = %v, want errors.Is(_, filestorage.ErrObjectNotFound)", err)
	}
}

// RunOptionalCapabilityAbsent asserts the optional-capability story for a
// Storer that does NOT implement filestorage.ResumableUploader or
// filestorage.SignedURLer: FileStore's type-assert helpers must yield the
// documented sentinel errors rather than panicking or silently no-opping.
// Call this only for backends known not to implement those optional
// interfaces (e.g. Disk); a backend that does implement one or both (e.g. a
// future GCS integration) is out of scope for this helper.
func RunOptionalCapabilityAbsent(t *testing.T, newStorer func(t *testing.T) filestorage.Storer) {
	t.Helper()
	ctx := context.Background()
	s := newStorer(t)

	if _, ok := s.(filestorage.ResumableUploader); ok {
		t.Fatalf("%T implements ResumableUploader; RunOptionalCapabilityAbsent is only for backends that don't", s)
	}
	if _, ok := s.(filestorage.SignedURLer); ok {
		t.Fatalf("%T implements SignedURLer; RunOptionalCapabilityAbsent is only for backends that don't", s)
	}

	fs := filestorage.New(s)
	if _, err := fs.InitiateResumableUpload(ctx, "path", "text/plain"); !errors.Is(err, filestorage.ErrResumableNotSupported) {
		t.Errorf("InitiateResumableUpload() error = %v, want errors.Is(_, filestorage.ErrResumableNotSupported)", err)
	}
	if _, err := fs.SignedURL(ctx, "path", time.Minute); !errors.Is(err, filestorage.ErrSignedURLNotSupported) {
		t.Errorf("SignedURL() error = %v, want errors.Is(_, filestorage.ErrSignedURLNotSupported)", err)
	}
}
