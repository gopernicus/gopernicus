package storage

import "errors"

var (
	// ErrObjectNotFound is returned when an object is not found in storage.
	ErrObjectNotFound = errors.New("object not found")

	// ErrInvalidPath is returned when a storage path is invalid.
	ErrInvalidPath = errors.New("invalid storage path")

	// ErrUploadFailed is returned when an upload operation fails.
	ErrUploadFailed = errors.New("upload failed")

	// ErrDownloadFailed is returned when a download operation fails.
	ErrDownloadFailed = errors.New("download failed")

	// ErrDeleteFailed is returned when a delete operation fails.
	ErrDeleteFailed = errors.New("delete failed")

	// ErrResumableNotSupported is returned when a backend does not support resumable uploads.
	ErrResumableNotSupported = errors.New("resumable uploads not supported by this storage backend")

	// ErrSignedURLNotSupported is returned when a backend does not support signed URLs.
	ErrSignedURLNotSupported = errors.New("signed URLs not supported by this storage backend")
)
