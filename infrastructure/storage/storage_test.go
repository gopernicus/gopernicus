package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// mockClient is a test implementation of the Client interface.
type mockClient struct {
	uploadFunc        func(ctx context.Context, path string, reader io.Reader) error
	downloadFunc      func(ctx context.Context, path string) (io.ReadCloser, error)
	deleteFunc        func(ctx context.Context, path string) error
	existsFunc        func(ctx context.Context, path string) (bool, error)
	listFunc          func(ctx context.Context, prefix string) ([]string, error)
	downloadRangeFunc func(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
	getObjectSizeFunc            func(ctx context.Context, path string) (int64, error)
	initiateResumableUploadFunc  func(ctx context.Context, path, contentType string) (string, error)
	signedURLFunc                func(ctx context.Context, path string, expiry time.Duration) (string, error)
}

func (m *mockClient) Upload(ctx context.Context, path string, reader io.Reader) error {
	if m.uploadFunc != nil {
		return m.uploadFunc(ctx, path, reader)
	}
	return nil
}

func (m *mockClient) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if m.downloadFunc != nil {
		return m.downloadFunc(ctx, path)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockClient) Delete(ctx context.Context, path string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, path)
	}
	return nil
}

func (m *mockClient) Exists(ctx context.Context, path string) (bool, error) {
	if m.existsFunc != nil {
		return m.existsFunc(ctx, path)
	}
	return false, nil
}

func (m *mockClient) List(ctx context.Context, prefix string) ([]string, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, prefix)
	}
	return nil, nil
}

func (m *mockClient) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	if m.downloadRangeFunc != nil {
		return m.downloadRangeFunc(ctx, path, offset, length)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockClient) GetObjectSize(ctx context.Context, path string) (int64, error) {
	if m.getObjectSizeFunc != nil {
		return m.getObjectSizeFunc(ctx, path)
	}
	return 0, nil
}

func (m *mockClient) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	if m.initiateResumableUploadFunc != nil {
		return m.initiateResumableUploadFunc(ctx, path, contentType)
	}
	return "", nil
}

func (m *mockClient) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if m.signedURLFunc != nil {
		return m.signedURLFunc(ctx, path, expiry)
	}
	return "", nil
}

func TestUpload_Success(t *testing.T) {
	called := false
	fs := New(&mockClient{
		uploadFunc: func(ctx context.Context, path string, reader io.Reader) error {
			called = true
			if path != "test/file.txt" {
				t.Errorf("path = %q, want %q", path, "test/file.txt")
			}
			return nil
		},
	})

	err := fs.Upload(context.Background(), "test/file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if !called {
		t.Error("client was not called")
	}
}

func TestUpload_ClientError(t *testing.T) {
	fs := New(&mockClient{
		uploadFunc: func(ctx context.Context, path string, reader io.Reader) error {
			return errors.New("disk full")
		},
	})

	err := fs.Upload(context.Background(), "test/file.txt", strings.NewReader("hello"))
	if err == nil {
		t.Fatal("Upload() should return error")
	}
	if !strings.Contains(err.Error(), "upload file") {
		t.Errorf("error = %q, want to contain 'upload file'", err.Error())
	}
}

func TestDownload_Success(t *testing.T) {
	fs := New(&mockClient{
		downloadFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("file content")), nil
		},
	})

	reader, err := fs.Download(context.Background(), "test/file.txt")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if string(data) != "file content" {
		t.Errorf("content = %q, want %q", string(data), "file content")
	}
}

func TestDownload_ClientError(t *testing.T) {
	fs := New(&mockClient{
		downloadFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
			return nil, ErrObjectNotFound
		},
	})

	_, err := fs.Download(context.Background(), "missing.txt")
	if err == nil {
		t.Fatal("Download() should return error")
	}
	if !strings.Contains(err.Error(), "download file") {
		t.Errorf("error = %q, want to contain 'download file'", err.Error())
	}
}

func TestDelete_Success(t *testing.T) {
	called := false
	fs := New(&mockClient{
		deleteFunc: func(ctx context.Context, path string) error {
			called = true
			return nil
		},
	})

	err := fs.Delete(context.Background(), "test/file.txt")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !called {
		t.Error("client was not called")
	}
}

func TestExists_Success(t *testing.T) {
	fs := New(&mockClient{
		existsFunc: func(ctx context.Context, path string) (bool, error) {
			return true, nil
		},
	})

	exists, err := fs.Exists(context.Background(), "test/file.txt")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Error("Exists() = false, want true")
	}
}

func TestList_Success(t *testing.T) {
	fs := New(&mockClient{
		listFunc: func(ctx context.Context, prefix string) ([]string, error) {
			return []string{"a.txt", "b.txt", "c.txt"}, nil
		},
	})

	paths, err := fs.List(context.Background(), "prefix/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(paths) != 3 {
		t.Errorf("List() returned %d paths, want 3", len(paths))
	}
}

func TestGetObjectSize_Success(t *testing.T) {
	fs := New(&mockClient{
		getObjectSizeFunc: func(ctx context.Context, path string) (int64, error) {
			return 1024, nil
		},
	})

	size, err := fs.GetObjectSize(context.Background(), "test/file.txt")
	if err != nil {
		t.Fatalf("GetObjectSize() error = %v", err)
	}
	if size != 1024 {
		t.Errorf("GetObjectSize() = %d, want 1024", size)
	}
}

func TestDownloadRange_Success(t *testing.T) {
	fs := New(&mockClient{
		downloadRangeFunc: func(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
			if offset != 10 || length != 5 {
				t.Errorf("offset = %d, length = %d, want 10, 5", offset, length)
			}
			return io.NopCloser(strings.NewReader("range")), nil
		},
	})

	reader, err := fs.DownloadRange(context.Background(), "test/file.txt", 10, 5)
	if err != nil {
		t.Fatalf("DownloadRange() error = %v", err)
	}
	defer reader.Close()

	data, _ := io.ReadAll(reader)
	if string(data) != "range" {
		t.Errorf("content = %q, want %q", string(data), "range")
	}
}

func TestInitiateResumableUpload_Success(t *testing.T) {
	fs := New(&mockClient{
		initiateResumableUploadFunc: func(ctx context.Context, path, contentType string) (string, error) {
			if path != "uploads/video.mp4" {
				t.Errorf("path = %q, want %q", path, "uploads/video.mp4")
			}
			if contentType != "video/mp4" {
				t.Errorf("contentType = %q, want %q", contentType, "video/mp4")
			}
			return "https://storage.example.com/session/abc123", nil
		},
	})

	uri, err := fs.InitiateResumableUpload(context.Background(), "uploads/video.mp4", "video/mp4")
	if err != nil {
		t.Fatalf("InitiateResumableUpload() error = %v", err)
	}
	if uri != "https://storage.example.com/session/abc123" {
		t.Errorf("URI = %q, want %q", uri, "https://storage.example.com/session/abc123")
	}
}

func TestInitiateResumableUpload_ClientError(t *testing.T) {
	fs := New(&mockClient{
		initiateResumableUploadFunc: func(ctx context.Context, path, contentType string) (string, error) {
			return "", errors.New("backend error")
		},
	})

	_, err := fs.InitiateResumableUpload(context.Background(), "uploads/video.mp4", "video/mp4")
	if err == nil {
		t.Fatal("InitiateResumableUpload() should return error")
	}
	if !strings.Contains(err.Error(), "initiate resumable upload") {
		t.Errorf("error = %q, want to contain 'initiate resumable upload'", err.Error())
	}
}

func TestSignedURL_Success(t *testing.T) {
	fs := New(&mockClient{
		signedURLFunc: func(ctx context.Context, path string, expiry time.Duration) (string, error) {
			if path != "docs/report.pdf" {
				t.Errorf("path = %q, want %q", path, "docs/report.pdf")
			}
			if expiry != 15*time.Minute {
				t.Errorf("expiry = %v, want %v", expiry, 15*time.Minute)
			}
			return "https://storage.example.com/signed/xyz", nil
		},
	})

	url, err := fs.SignedURL(context.Background(), "docs/report.pdf", 15*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}
	if url != "https://storage.example.com/signed/xyz" {
		t.Errorf("URL = %q, want %q", url, "https://storage.example.com/signed/xyz")
	}
}

func TestSignedURL_ClientError(t *testing.T) {
	fs := New(&mockClient{
		signedURLFunc: func(ctx context.Context, path string, expiry time.Duration) (string, error) {
			return "", errors.New("not supported")
		},
	})

	_, err := fs.SignedURL(context.Background(), "docs/report.pdf", 15*time.Minute)
	if err == nil {
		t.Fatal("SignedURL() should return error")
	}
	if !strings.Contains(err.Error(), "generate signed URL") {
		t.Errorf("error = %q, want to contain 'generate signed URL'", err.Error())
	}
}
