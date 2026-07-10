package filestorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Storer is the core storage port: the operations every backend (disk, GCS,
// S3, …) can honor. Optional capabilities that not all backends support are
// segregated into their own interfaces below (ResumableUploader, SignedURLer)
// so this port never forces a backend to stub out methods it cannot implement.
type Storer interface {
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
}

// ResumableUploader is an optional capability for backends that support
// resumable upload sessions (e.g. GCS). A Storer that also implements this
// interface advertises the capability via type assertion; callers reach it
// through FileStore.InitiateResumableUpload, which returns
// ErrResumableNotSupported when the underlying Storer does not implement it.
type ResumableUploader interface {
	// InitiateResumableUpload starts a resumable upload session and returns a
	// session URI. The client uploads directly to this URI via PUT.
	InitiateResumableUpload(ctx context.Context, path, contentType string) (sessionURI string, err error)
}

// SignedURLer is an optional capability for backends that can mint short-lived
// signed read URLs (e.g. GCS, S3). A Storer that also implements this interface
// advertises the capability via type assertion; callers reach it through
// FileStore.SignedURL, which returns ErrSignedURLNotSupported when the
// underlying Storer does not implement it.
type SignedURLer interface {
	// SignedURL returns a short-lived signed URL for reading an object.
	SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}

type FileStore struct {
	log    *slog.Logger
	storer Storer
}

type Option func(*FileStore)

// WithLogger enables structured logging for storage operations.
// When set, the storer logs not-found warnings and unexpected errors with path context.
func WithLogger(log *slog.Logger) Option {
	return func(fs *FileStore) {
		fs.log = log
	}
}

// New creates a new FileStore with the specified store.
// Use WithLogger to enable structured logging of storage errors.
func New(store Storer, opts ...Option) *FileStore {
	fs := &FileStore{storer: store}
	for _, opt := range opts {
		opt(fs)
	}
	return fs
}

// logError logs a storage error with path context at the appropriate level.
// No-op when logger is not configured.
func (fs *FileStore) logError(ctx context.Context, op, path string, err error) {
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
func (fs *FileStore) Upload(ctx context.Context, path string, reader io.Reader) error {
	if err := fs.storer.Upload(ctx, path, reader); err != nil {
		fs.logError(ctx, "upload", path, err)
		return fmt.Errorf("upload file: %w", err)
	}
	return nil
}

// Download retrieves an object from the given path and returns an io.ReadCloser.
// The caller is responsible for closing the reader.
func (fs *FileStore) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	reader, err := fs.storer.Download(ctx, path)
	if err != nil {
		fs.logError(ctx, "download", path, err)
		return nil, fmt.Errorf("download file: %w", err)
	}
	return reader, nil
}

// Delete removes an object at the given path.
func (fs *FileStore) Delete(ctx context.Context, path string) error {
	if err := fs.storer.Delete(ctx, path); err != nil {
		fs.logError(ctx, "delete", path, err)
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// Exists checks if an object exists at the given path.
func (fs *FileStore) Exists(ctx context.Context, path string) (bool, error) {
	exists, err := fs.storer.Exists(ctx, path)
	if err != nil {
		fs.logError(ctx, "exists", path, err)
		return false, fmt.Errorf("check file exists: %w", err)
	}
	return exists, nil
}

// List lists objects/keys under a given prefix.
func (fs *FileStore) List(ctx context.Context, prefix string) ([]string, error) {
	paths, err := fs.storer.List(ctx, prefix)
	if err != nil {
		fs.logError(ctx, "list", prefix, err)
		return nil, fmt.Errorf("list files: %w", err)
	}
	return paths, nil
}

// DownloadRange retrieves a specific byte range from an object at the given path.
// If length is -1, reads from offset to the end of the file.
// The caller is responsible for closing the reader.
func (fs *FileStore) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	reader, err := fs.storer.DownloadRange(ctx, path, offset, length)
	if err != nil {
		fs.logError(ctx, "download_range", path, err)
		return nil, fmt.Errorf("download file range: %w", err)
	}
	return reader, nil
}

// GetObjectSize returns the size in bytes of an object at the given path.
func (fs *FileStore) GetObjectSize(ctx context.Context, path string) (int64, error) {
	size, err := fs.storer.GetObjectSize(ctx, path)
	if err != nil {
		fs.logError(ctx, "get_object_size", path, err)
		return 0, fmt.Errorf("get file size: %w", err)
	}
	return size, nil
}

// InitiateResumableUpload starts a resumable upload session and returns a session URI.
// Returns ErrResumableNotSupported if the underlying Storer does not implement
// ResumableUploader.
func (fs *FileStore) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	ru, ok := fs.storer.(ResumableUploader)
	if !ok {
		return "", ErrResumableNotSupported
	}
	sessionURI, err := ru.InitiateResumableUpload(ctx, path, contentType)
	if err != nil {
		fs.logError(ctx, "initiate_resumable_upload", path, err)
		return "", fmt.Errorf("initiate resumable upload: %w", err)
	}
	return sessionURI, nil
}

// SignedURL returns a short-lived signed URL for reading an object.
// Returns ErrSignedURLNotSupported if the underlying Storer does not implement
// SignedURLer.
func (fs *FileStore) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	su, ok := fs.storer.(SignedURLer)
	if !ok {
		return "", ErrSignedURLNotSupported
	}
	url, err := su.SignedURL(ctx, path, expiry)
	if err != nil {
		fs.logError(ctx, "signed_url", path, err)
		return "", fmt.Errorf("generate signed URL: %w", err)
	}
	return url, nil
}
