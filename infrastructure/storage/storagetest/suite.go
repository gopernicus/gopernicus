// Package storagetest provides compliance tests for storage.Client implementations.
//
// Example:
//
//	func TestCompliance(t *testing.T) {
//	    client, _ := disk.New(t.TempDir())
//	    storagetest.RunSuite(t, client)
//	}
package storagetest

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/storage"
)

// RunSuite runs the standard compliance tests against any storage.Client implementation.
func RunSuite(t *testing.T, c storage.Client) {
	t.Helper()

	t.Run("UploadAndDownload", func(t *testing.T) {
		ctx := context.Background()
		data := []byte("hello world")
		if err := c.Upload(ctx, "test/file.txt", bytes.NewReader(data)); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		rc, err := c.Download(ctx, "test/file.txt")
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		defer rc.Close()

		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(got) != "hello world" {
			t.Fatalf("Download: got %q, want %q", got, "hello world")
		}
	})

	t.Run("Exists", func(t *testing.T) {
		ctx := context.Background()
		c.Upload(ctx, "test/exists.txt", bytes.NewReader([]byte("x")))

		ok, err := c.Exists(ctx, "test/exists.txt")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !ok {
			t.Fatal("Exists: expected true for uploaded file")
		}

		ok, err = c.Exists(ctx, "test/nonexistent.txt")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if ok {
			t.Fatal("Exists: expected false for nonexistent file")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		ctx := context.Background()
		c.Upload(ctx, "test/delete.txt", bytes.NewReader([]byte("x")))

		if err := c.Delete(ctx, "test/delete.txt"); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		ok, _ := c.Exists(ctx, "test/delete.txt")
		if ok {
			t.Fatal("file should not exist after Delete")
		}
	})

	t.Run("List", func(t *testing.T) {
		ctx := context.Background()
		c.Upload(ctx, "test/list/a.txt", bytes.NewReader([]byte("a")))
		c.Upload(ctx, "test/list/b.txt", bytes.NewReader([]byte("b")))

		files, err := c.List(ctx, "test/list/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(files) < 2 {
			t.Fatalf("List: expected at least 2 files, got %d", len(files))
		}
	})

	t.Run("GetObjectSize", func(t *testing.T) {
		ctx := context.Background()
		data := []byte("twelve chars")
		c.Upload(ctx, "test/size.txt", bytes.NewReader(data))

		size, err := c.GetObjectSize(ctx, "test/size.txt")
		if err != nil {
			t.Fatalf("GetObjectSize: %v", err)
		}
		if size != int64(len(data)) {
			t.Fatalf("GetObjectSize: got %d, want %d", size, len(data))
		}
	})
}
