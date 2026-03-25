// Package storage provides file storage infrastructure with pluggable backends.
// It defines the Client interface for storage operations and a FileStorer facade
// that wraps any Client with structured logging.
package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Client defines the interface for storage operations.
// Different clients (disk, GCS, S3) implement this interface.
type Client interface {
	// Upload uploads data from an io.Reader to the given path.
	Upload(ctx context.Context, path string, reader io.Reader) error

	// Download retrieves an object from the given path and returns an io.ReadCloser.
	// The caller is responsible for closing the reader.
	Download(ctx context.Context, path string) (io.ReadCloser, error)

	// Delete removes an object at the given path.
	Delete(ctx context.Context, path string) error

	// Exists checks if an object exists at the given path.
	Exists(ctx context.Context, path string) (bool, error)

	// List lists objects/keys under a given prefix.
	// For hierarchical storage, this might list files in a "directory".
	// For flat storage, it lists keys starting with the prefix.
	// Returns a slice of full paths.
	List(ctx context.Context, prefix string) ([]string, error)

	// DownloadRange retrieves a specific byte range from an object at the given path.
	// If length is -1, reads from offset to the end of the file.
	// The caller is responsible for closing the reader.
	DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)

	// GetObjectSize returns the size in bytes of an object at the given path.
	GetObjectSize(ctx context.Context, path string) (int64, error)

	// InitiateResumableUpload starts a resumable upload session and returns a session URI.
	// The client uploads directly to this URI via PUT. Not all backends support this —
	// unsupported backends return ErrResumableNotSupported.
	InitiateResumableUpload(ctx context.Context, path, contentType string) (sessionURI string, err error)

	// SignedURL returns a short-lived signed URL for reading an object.
	// Not all backends support this — unsupported backends return ErrSignedURLNotSupported.
	SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}

// FileStorer provides file storage functionality with pluggable clients.
// It wraps a Client implementation and adds common functionality like logging.
type FileStorer struct {
	log    *slog.Logger
	client Client
}

// Option is a functional option for configuring FileStorer.
type Option func(*FileStorer)

// New creates a new FileStorer with the specified client.
//
// The client parameter determines how files are actually stored (disk, GCS, S3, etc.).
func New(log *slog.Logger, client Client, opts ...Option) *FileStorer {
	fs := &FileStorer{
		log:    log,
		client: client,
	}

	for _, opt := range opts {
		opt(fs)
	}

	return fs
}

// Upload uploads data from an io.Reader to the given path.
func (fs *FileStorer) Upload(ctx context.Context, path string, reader io.Reader) error {
	fs.log.DebugContext(ctx, "uploading file", "path", path)

	if err := fs.client.Upload(ctx, path, reader); err != nil {
		fs.log.ErrorContext(ctx, "failed to upload file",
			"error", err,
			"path", path,
		)
		return fmt.Errorf("upload file: %w", err)
	}

	fs.log.DebugContext(ctx, "file uploaded successfully", "path", path)
	return nil
}

// Download retrieves an object from the given path and returns an io.ReadCloser.
// The caller is responsible for closing the reader.
func (fs *FileStorer) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	fs.log.DebugContext(ctx, "downloading file", "path", path)

	reader, err := fs.client.Download(ctx, path)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to download file",
			"error", err,
			"path", path,
		)
		return nil, fmt.Errorf("download file: %w", err)
	}

	fs.log.DebugContext(ctx, "file downloaded successfully", "path", path)
	return reader, nil
}

// Delete removes an object at the given path.
func (fs *FileStorer) Delete(ctx context.Context, path string) error {
	fs.log.DebugContext(ctx, "deleting file", "path", path)

	if err := fs.client.Delete(ctx, path); err != nil {
		fs.log.ErrorContext(ctx, "failed to delete file",
			"error", err,
			"path", path,
		)
		return fmt.Errorf("delete file: %w", err)
	}

	fs.log.DebugContext(ctx, "file deleted successfully", "path", path)
	return nil
}

// Exists checks if an object exists at the given path.
func (fs *FileStorer) Exists(ctx context.Context, path string) (bool, error) {
	exists, err := fs.client.Exists(ctx, path)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to check if file exists",
			"error", err,
			"path", path,
		)
		return false, fmt.Errorf("check file exists: %w", err)
	}

	return exists, nil
}

// List lists objects/keys under a given prefix.
func (fs *FileStorer) List(ctx context.Context, prefix string) ([]string, error) {
	fs.log.DebugContext(ctx, "listing files", "prefix", prefix)

	paths, err := fs.client.List(ctx, prefix)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to list files",
			"error", err,
			"prefix", prefix,
		)
		return nil, fmt.Errorf("list files: %w", err)
	}

	fs.log.DebugContext(ctx, "files listed successfully", "prefix", prefix, "count", len(paths))
	return paths, nil
}

// DownloadRange retrieves a specific byte range from an object at the given path.
// If length is -1, reads from offset to the end of the file.
// The caller is responsible for closing the reader.
func (fs *FileStorer) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	fs.log.DebugContext(ctx, "downloading file range", "path", path, "offset", offset, "length", length)

	reader, err := fs.client.DownloadRange(ctx, path, offset, length)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to download file range",
			"error", err,
			"path", path,
			"offset", offset,
			"length", length,
		)
		return nil, fmt.Errorf("download file range: %w", err)
	}

	fs.log.DebugContext(ctx, "file range downloaded successfully", "path", path, "offset", offset, "length", length)
	return reader, nil
}

// GetObjectSize returns the size in bytes of an object at the given path.
func (fs *FileStorer) GetObjectSize(ctx context.Context, path string) (int64, error) {
	fs.log.DebugContext(ctx, "getting file size", "path", path)

	size, err := fs.client.GetObjectSize(ctx, path)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to get file size",
			"error", err,
			"path", path,
		)
		return 0, fmt.Errorf("get file size: %w", err)
	}

	fs.log.DebugContext(ctx, "file size retrieved successfully", "path", path, "size", size)
	return size, nil
}

// InitiateResumableUpload starts a resumable upload session and returns a session URI.
func (fs *FileStorer) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	fs.log.DebugContext(ctx, "initiating resumable upload", "path", path, "content_type", contentType)

	sessionURI, err := fs.client.InitiateResumableUpload(ctx, path, contentType)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to initiate resumable upload", "error", err, "path", path)
		return "", fmt.Errorf("initiate resumable upload: %w", err)
	}

	fs.log.DebugContext(ctx, "resumable upload initiated", "path", path)
	return sessionURI, nil
}

// SignedURL returns a short-lived signed URL for reading an object.
func (fs *FileStorer) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	fs.log.DebugContext(ctx, "generating signed URL", "path", path, "expiry", expiry)

	url, err := fs.client.SignedURL(ctx, path, expiry)
	if err != nil {
		fs.log.ErrorContext(ctx, "failed to generate signed URL", "error", err, "path", path)
		return "", fmt.Errorf("generate signed URL: %w", err)
	}

	fs.log.DebugContext(ctx, "signed URL generated", "path", path)
	return url, nil
}
