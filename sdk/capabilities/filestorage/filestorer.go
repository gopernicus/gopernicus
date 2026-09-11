// Package filestorage defines replaceable object storage and a local Disk default.
// Hosts supply a Storer (or their own narrower interface) directly and own its
// concrete resources. Optional capabilities are discovered on the real adapter.
package filestorage

import (
	"context"
	"io"
	"time"
)

// Storer stores objects under relative slash-separated keys. ValidatePath defines
// accepted keys; implementations must not silently clean or rename them. Filesystem
// backends retain platform limits such as case collisions and file/directory
// conflicts. Hosts inventory incompatible legacy keys before migrating backends.
//
// Methods check context before work. Cancellation during I/O is cooperative: an
// arbitrary blocked source Reader cannot be interrupted by the store. An error
// racing remote completion does not prove that a write was rolled back.
type Storer interface {
	// Upload streams reader to path, replacing an existing object on success.
	// It does not require seeking or close reader. A source error must abort
	// uncommitted data and preserve its error chain and any previous object.
	Upload(ctx context.Context, path string, reader io.Reader) error

	// Download returns stored bytes. The caller must close the returned reader.
	// Missing objects match ErrObjectNotFound and sdk.ErrNotFound.
	Download(ctx context.Context, path string) (io.ReadCloser, error)

	// Delete removes an object. An already missing object is not an error.
	Delete(ctx context.Context, path string) error

	// Exists reports object presence. Configuration and permission failures
	// remain errors rather than being treated as normal absence.
	Exists(ctx context.Context, path string) (bool, error)

	// List returns caller-facing keys starting with the literal prefix. Empty
	// prefix selects all objects. Results are materialized in memory with no
	// ordering guarantee; directory markers are omitted.
	List(ctx context.Context, prefix string) ([]string, error)

	// DownloadRange returns stored bytes from offset, up to length bytes.
	// offset must be nonnegative; length -1 means to EOF. Zero length and
	// offsets at/beyond EOF return an empty reader after confirming existence.
	// Reads truncate at EOF. Invalid or overflowing ranges match sdk.ErrInvalidInput.
	// The caller must close the reader. Independent stat/read requests do not
	// promise a consistent snapshot during concurrent replacement.
	DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)

	// GetObjectSize returns the stored size in bytes.
	GetObjectSize(ctx context.Context, path string) (int64, error)
}

// ResumableUploadOptions configures a directly uploaded object's content type and
// browser origin. Hosts authorize Origin and configure bucket CORS; accepting an
// arbitrary incoming Origin here is not an authorization policy.
type ResumableUploadOptions struct {
	ContentType string
	Origin      string
}

// ResumableUploader starts sessions to which clients can PUT data directly (GCS,
// for example). An S3 multipart upload ID does not satisfy this protocol.
type ResumableUploader interface {
	// InitiateResumableUpload returns a bearer session URI. Keep it private;
	// callers own session completion/cancellation according to the provider.
	InitiateResumableUpload(ctx context.Context, path string, options ResumableUploadOptions) (sessionURI string, err error)
}

// SignedURLer mints signed read URLs. Signing may require credential or IAM I/O.
type SignedURLer interface {
	// SignedURL returns a bearer URL, not proof of object existence. expiry must
	// be whole seconds in [1 second, 7 days]. Credentials/provider policy may
	// expire the URL sooner. Do not log the returned URL.
	SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}
