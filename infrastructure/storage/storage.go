// Package storage provides file storage infrastructure with pluggable backends.
// It defines the Client interface for storage operations and a FileStorer facade
// that wraps any Client with error logging for unexpected failures.
package storage

import (
	"context"
	"errors"
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

// FileStorer wraps a Client and adds optional structured logging:
//   - Object not found → Warn with path (not visible from a stack trace the way a route 404 is)
//   - Invalid path, unsupported capability → no log (caller's concern)
//   - Everything else → Error with path and error detail
//
// Logging is opt-in via WithLogger. Without it, all errors are returned silently to the caller.
type FileStorer struct {
	log    *slog.Logger
	client Client
}

// Option is a functional option for configuring FileStorer.
type Option func(*FileStorer)

// WithLogger enables structured logging for storage operations.
// When set, the storer logs not-found warnings and unexpected errors with path context.
func WithLogger(log *slog.Logger) Option {
	return func(fs *FileStorer) {
		fs.log = log
	}
}

// New creates a new FileStorer with the specified client.
// Use WithLogger to enable structured logging of storage errors.
func New(client Client, opts ...Option) *FileStorer {
	fs := &FileStorer{client: client}
	for _, opt := range opts {
		opt(fs)
	}
	return fs
}

// logError logs a storage error with path context at the appropriate level.
// No-op when logger is not configured.
func (fs *FileStorer) logError(ctx context.Context, op, path string, err error) {
	if fs.log == nil {
		return
	}
	switch {
	case errors.Is(err, ErrObjectNotFound):
		fs.log.WarnContext(ctx, "storage: object not found", "op", op, "path", path)
	case errors.Is(err, ErrInvalidPath),
		errors.Is(err, ErrResumableNotSupported),
		errors.Is(err, ErrSignedURLNotSupported):
		// Input/config problems — propagate to caller, no log.
	default:
		fs.log.ErrorContext(ctx, "storage: "+op+" failed", "path", path, "error", err)
	}
}

// Upload uploads data from an io.Reader to the given path.
func (fs *FileStorer) Upload(ctx context.Context, path string, reader io.Reader) error {
	if err := fs.client.Upload(ctx, path, reader); err != nil {
		fs.logError(ctx, "upload", path, err)
		return fmt.Errorf("upload file: %w", err)
	}
	return nil
}

// Download retrieves an object from the given path and returns an io.ReadCloser.
// The caller is responsible for closing the reader.
func (fs *FileStorer) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	reader, err := fs.client.Download(ctx, path)
	if err != nil {
		fs.logError(ctx, "download", path, err)
		return nil, fmt.Errorf("download file: %w", err)
	}
	return reader, nil
}

// Delete removes an object at the given path.
func (fs *FileStorer) Delete(ctx context.Context, path string) error {
	if err := fs.client.Delete(ctx, path); err != nil {
		fs.logError(ctx, "delete", path, err)
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// Exists checks if an object exists at the given path.
func (fs *FileStorer) Exists(ctx context.Context, path string) (bool, error) {
	exists, err := fs.client.Exists(ctx, path)
	if err != nil {
		fs.logError(ctx, "exists", path, err)
		return false, fmt.Errorf("check file exists: %w", err)
	}
	return exists, nil
}

// List lists objects/keys under a given prefix.
func (fs *FileStorer) List(ctx context.Context, prefix string) ([]string, error) {
	paths, err := fs.client.List(ctx, prefix)
	if err != nil {
		fs.logError(ctx, "list", prefix, err)
		return nil, fmt.Errorf("list files: %w", err)
	}
	return paths, nil
}

// DownloadRange retrieves a specific byte range from an object at the given path.
// If length is -1, reads from offset to the end of the file.
// The caller is responsible for closing the reader.
func (fs *FileStorer) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	reader, err := fs.client.DownloadRange(ctx, path, offset, length)
	if err != nil {
		fs.logError(ctx, "download_range", path, err)
		return nil, fmt.Errorf("download file range: %w", err)
	}
	return reader, nil
}

// GetObjectSize returns the size in bytes of an object at the given path.
func (fs *FileStorer) GetObjectSize(ctx context.Context, path string) (int64, error) {
	size, err := fs.client.GetObjectSize(ctx, path)
	if err != nil {
		fs.logError(ctx, "get_object_size", path, err)
		return 0, fmt.Errorf("get file size: %w", err)
	}
	return size, nil
}

// InitiateResumableUpload starts a resumable upload session and returns a session URI.
func (fs *FileStorer) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	sessionURI, err := fs.client.InitiateResumableUpload(ctx, path, contentType)
	if err != nil {
		fs.logError(ctx, "initiate_resumable_upload", path, err)
		return "", fmt.Errorf("initiate resumable upload: %w", err)
	}
	return sessionURI, nil
}

// SignedURL returns a short-lived signed URL for reading an object.
func (fs *FileStorer) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	url, err := fs.client.SignedURL(ctx, path, expiry)
	if err != nil {
		fs.logError(ctx, "signed_url", path, err)
		return "", fmt.Errorf("generate signed URL: %w", err)
	}
	return url, nil
}
