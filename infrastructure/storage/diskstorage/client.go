// Package diskstorage provides local filesystem storage implementation.
package diskstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/storage"
)

var _ storage.Client = (*Client)(nil)

// Config holds disk storage configuration.
type Config struct {
	BasePath string
}

// Client implements the storage.Client interface using local disk storage.
type Client struct {
	basePath string
}

// New creates a new disk storage client.
func New(basePath string) *Client {
	// Normalize base path to absolute path for security.
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		absBase = basePath
	}
	return &Client{
		basePath: absBase,
	}
}

// securePath validates and returns a safe full path, preventing directory traversal attacks.
// Returns an error if the path would escape the base directory.
func (c *Client) securePath(path string) (string, error) {
	// Clean the path to resolve any .. or . components.
	cleanPath := filepath.Clean(path)

	// Join with base path.
	fullPath := filepath.Join(c.basePath, cleanPath)

	// Get absolute path.
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	// Ensure the resolved path is still within the base directory.
	// Add trailing separator to base to prevent prefix attacks (e.g., /tmp/storage-evil).
	baseWithSep := c.basePath + string(filepath.Separator)
	if !strings.HasPrefix(absPath, baseWithSep) && absPath != c.basePath {
		return "", fmt.Errorf("path traversal detected: path escapes base directory")
	}

	return absPath, nil
}

// Upload implements storage.Client.
func (c *Client) Upload(ctx context.Context, path string, reader io.Reader) error {
	fullPath, err := c.securePath(path)
	if err != nil {
		return fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	// Create directory structure.
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("disk: %w", errors.Join(storage.ErrUploadFailed, err))
	}

	// Create file.
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("disk: %w", errors.Join(storage.ErrUploadFailed, err))
	}
	defer file.Close()

	// Copy data.
	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("disk: %w", errors.Join(storage.ErrUploadFailed, err))
	}

	return nil
}

// Download implements storage.Client.
func (c *Client) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	fullPath, err := c.securePath(path)
	if err != nil {
		return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	return file, nil
}

// Delete implements storage.Client.
func (c *Client) Delete(ctx context.Context, path string) error {
	fullPath, err := c.securePath(path)
	if err != nil {
		return fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil // Treat as success — file already gone.
		}
		return fmt.Errorf("disk: %w", errors.Join(storage.ErrDeleteFailed, err))
	}

	return nil
}

// Exists implements storage.Client.
func (c *Client) Exists(ctx context.Context, path string) (bool, error) {
	fullPath, err := c.securePath(path)
	if err != nil {
		return false, fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	_, err = os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("disk: check file existence: %w", err)
}

// List implements storage.Client.
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	fullPrefix, err := c.securePath(prefix)
	if err != nil {
		return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	var results []string

	err = filepath.Walk(fullPrefix, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // Treat missing prefix as empty list.
			}
			return err
		}

		// Only include files, not directories.
		if !info.IsDir() {
			relPath, err := filepath.Rel(c.basePath, path)
			if err != nil {
				return err
			}
			results = append(results, relPath)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("disk: list files: %w", err)
	}

	sort.Strings(results)
	return results, nil
}

// DownloadRange implements storage.Client.
// Downloads a byte range from a file. If length is -1, reads from offset to end of file.
func (c *Client) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	fullPath, err := c.securePath(path)
	if err != nil {
		return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	// Seek to the offset.
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("disk: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	// If length is -1, return the file as-is (reads from offset to end).
	if length == -1 {
		return file, nil
	}

	// Otherwise, wrap in a LimitedReader to only read 'length' bytes.
	limitedReader := io.LimitReader(file, length)
	return &limitedReadCloser{
		Reader: limitedReader,
		closer: file,
	}, nil
}

// GetObjectSize implements storage.Client.
func (c *Client) GetObjectSize(ctx context.Context, path string) (int64, error) {
	fullPath, err := c.securePath(path)
	if err != nil {
		return 0, fmt.Errorf("disk: %w", errors.Join(storage.ErrInvalidPath, err))
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("disk: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return 0, fmt.Errorf("disk: get object size: %w", err)
	}

	return info.Size(), nil
}

// InitiateResumableUpload implements storage.Client.
// TODO: Disk storage does not support resumable uploads. If needed for local dev,
// this could be implemented with a temp file + rename pattern to align with the
// resumable upload semantics of GCS/S3.
func (c *Client) InitiateResumableUpload(_ context.Context, _, _ string) (string, error) {
	return "", storage.ErrResumableNotSupported
}

// SignedURL implements storage.Client.
// TODO: Disk storage does not support signed URLs. If needed for local dev,
// this could serve files via an HTTP handler with token-based access.
func (c *Client) SignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", storage.ErrSignedURLNotSupported
}

// limitedReadCloser wraps an io.Reader with a closer.
type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (lrc *limitedReadCloser) Close() error {
	return lrc.closer.Close()
}
