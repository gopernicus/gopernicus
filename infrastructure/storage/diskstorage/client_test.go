package diskstorage_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/storage"
	"github.com/gopernicus/gopernicus/infrastructure/storage/diskstorage"
	"github.com/gopernicus/gopernicus/infrastructure/storage/storagetest"
)

func setupTestClient(t *testing.T) (*diskstorage.Client, string) {
	t.Helper()
	tempDir := t.TempDir()
	client := diskstorage.New(tempDir)
	return client, tempDir
}

func TestCompliance(t *testing.T) {
	client, _ := setupTestClient(t)
	storagetest.RunSuite(t, client)
}

// =============================================================================
// Interface Method Tests
// =============================================================================

func TestUpload_Success(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	err := client.Upload(ctx, "test/file.txt", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	// Verify file was created with correct contents.
	data, err := os.ReadFile(filepath.Join(tempDir, "test", "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestUpload_CreatesDirectories(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	err := client.Upload(ctx, "a/b/c/deep.txt", strings.NewReader("deep"))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(tempDir, "a", "b", "c"))
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

func TestDownload_Success(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	// Create test file.
	filePath := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("download me"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reader, err := client.Download(ctx, "test.txt")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "download me" {
		t.Errorf("content = %q, want %q", string(data), "download me")
	}
}

func TestDownload_NotFound(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	_, err := client.Download(ctx, "nonexistent.txt")
	if err == nil {
		t.Fatal("Download() should return error for missing file")
	}
	if !errors.Is(err, storage.ErrObjectNotFound) {
		t.Errorf("error should wrap ErrObjectNotFound, got: %v", err)
	}
}

func TestDelete_Success(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	// Create test file.
	filePath := filepath.Join(tempDir, "delete-me.txt")
	if err := os.WriteFile(filePath, []byte("bye"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := client.Delete(ctx, "delete-me.txt")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify file is gone.
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should not exist after deletion")
	}
}

func TestDelete_NonExistent(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	// Deleting non-existent file should succeed (idempotent).
	err := client.Delete(ctx, "nonexistent.txt")
	if err != nil {
		t.Fatalf("Delete() should succeed for non-existent file, got: %v", err)
	}
}

func TestExists_True(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	// Create test file.
	filePath := filepath.Join(tempDir, "exists.txt")
	if err := os.WriteFile(filePath, []byte("here"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	exists, err := client.Exists(ctx, "exists.txt")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Error("Exists() = false, want true")
	}
}

func TestExists_False(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	exists, err := client.Exists(ctx, "nonexistent.txt")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Error("Exists() = true, want false")
	}
}

func TestList_Success(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	// Create test files in a subdirectory.
	dir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	paths, err := client.List(ctx, "subdir")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(paths) != 3 {
		t.Errorf("List() returned %d paths, want 3", len(paths))
	}
}

func TestList_EmptyPrefix(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	// Non-existent prefix should return empty list (not error).
	paths, err := client.List(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("List() returned %d paths, want 0", len(paths))
	}
}

func TestDownloadRange_Success(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	// Create test file.
	filePath := filepath.Join(tempDir, "range.txt")
	if err := os.WriteFile(filePath, []byte("0123456789"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reader, err := client.DownloadRange(ctx, "range.txt", 3, 4)
	if err != nil {
		t.Fatalf("DownloadRange() error = %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "3456" {
		t.Errorf("content = %q, want %q", string(data), "3456")
	}
}

func TestDownloadRange_ToEnd(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	filePath := filepath.Join(tempDir, "range2.txt")
	if err := os.WriteFile(filePath, []byte("0123456789"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// length=-1 means read to end.
	reader, err := client.DownloadRange(ctx, "range2.txt", 7, -1)
	if err != nil {
		t.Fatalf("DownloadRange() error = %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "789" {
		t.Errorf("content = %q, want %q", string(data), "789")
	}
}

func TestDownloadRange_NotFound(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	_, err := client.DownloadRange(ctx, "nonexistent.txt", 0, 10)
	if err == nil {
		t.Fatal("DownloadRange() should return error for missing file")
	}
	if !errors.Is(err, storage.ErrObjectNotFound) {
		t.Errorf("error should wrap ErrObjectNotFound, got: %v", err)
	}
}

func TestGetObjectSize_Success(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	data := []byte("exactly 20 bytes!!!")
	filePath := filepath.Join(tempDir, "sized.txt")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	size, err := client.GetObjectSize(ctx, "sized.txt")
	if err != nil {
		t.Fatalf("GetObjectSize() error = %v", err)
	}
	if size != int64(len(data)) {
		t.Errorf("GetObjectSize() = %d, want %d", size, len(data))
	}
}

func TestGetObjectSize_NotFound(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	_, err := client.GetObjectSize(ctx, "nonexistent.txt")
	if err == nil {
		t.Fatal("GetObjectSize() should return error for missing file")
	}
	if !errors.Is(err, storage.ErrObjectNotFound) {
		t.Errorf("error should wrap ErrObjectNotFound, got: %v", err)
	}
}

// =============================================================================
// Path Traversal Prevention Tests
// =============================================================================

var traversalPaths = []string{
	"../etc/passwd",
	"../../etc/passwd",
	"subdir/../../../etc/passwd",
	"../sibling/file.txt",
}

func TestUpload_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			err := client.Upload(ctx, path, strings.NewReader("malicious"))
			if err == nil {
				t.Error("Upload() should block path traversal")
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
			}
		})
	}
}

func TestUpload_AllowsValidPaths(t *testing.T) {
	client, tempDir := setupTestClient(t)
	ctx := context.Background()

	validPaths := []string{
		"file.txt",
		"subdir/file.txt",
		"a/b/c/deep/file.txt",
	}

	for _, path := range validPaths {
		t.Run(path, func(t *testing.T) {
			err := client.Upload(ctx, path, strings.NewReader("valid"))
			if err != nil {
				t.Fatalf("Upload() error = %v", err)
			}

			if _, err := os.Stat(filepath.Join(tempDir, path)); err != nil {
				t.Errorf("file not created at expected location: %v", err)
			}
		})
	}
}

func TestDownload_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			_, err := client.Download(ctx, path)
			if err == nil {
				t.Error("Download() should block path traversal")
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
			}
		})
	}
}

func TestDelete_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			err := client.Delete(ctx, path)
			if err == nil {
				t.Error("Delete() should block path traversal")
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
			}
		})
	}
}

func TestExists_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			_, err := client.Exists(ctx, path)
			if err == nil {
				t.Error("Exists() should block path traversal")
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
			}
		})
	}
}

func TestList_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	_, err := client.List(ctx, "../")
	if err == nil {
		t.Error("List() should block path traversal")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
	}
}

func TestDownloadRange_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			_, err := client.DownloadRange(ctx, path, 0, 100)
			if err == nil {
				t.Error("DownloadRange() should block path traversal")
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
			}
		})
	}
}

func TestGetObjectSize_BlocksPathTraversal(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			_, err := client.GetObjectSize(ctx, path)
			if err == nil {
				t.Error("GetObjectSize() should block path traversal")
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error = %q, want to contain 'path traversal'", err.Error())
			}
		})
	}
}
