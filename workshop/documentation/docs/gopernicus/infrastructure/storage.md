---
sidebar_position: 10
title: Storage
---

# Infrastructure — Storage

`github.com/gopernicus/gopernicus/infrastructure/storage`

The `storage` package defines the `Client` interface (port) and a `FileStorer` facade that wraps any client with optional structured logging. Backend implementations live in subdirectories.

## Client interface

```go
type Client interface {
    Upload(ctx context.Context, path string, reader io.Reader) error
    Download(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
    List(ctx context.Context, prefix string) ([]string, error)
    DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
    GetObjectSize(ctx context.Context, path string) (int64, error)

    // Optional capabilities — not all backends support these.
    InitiateResumableUpload(ctx context.Context, path, contentType string) (sessionURI string, err error)
    SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}
```

`Download` and `DownloadRange` return an `io.ReadCloser` — the caller is responsible for closing it. For `DownloadRange`, pass `length = -1` to read from `offset` to end of file.

### Optional capabilities

`InitiateResumableUpload` and `SignedURL` are not supported by all backends. Unsupported backends return `ErrResumableNotSupported` or `ErrSignedURLNotSupported`. Check before relying on them:

```go
sessionURI, err := client.InitiateResumableUpload(ctx, path, "video/mp4")
if errors.Is(err, storage.ErrResumableNotSupported) {
    // fall back to direct upload
}
```

## Errors

```go
storage.ErrObjectNotFound         // object does not exist
storage.ErrInvalidPath            // path is invalid or would escape the base directory
storage.ErrUploadFailed           // infrastructure failure during upload
storage.ErrDownloadFailed         // infrastructure failure during download
storage.ErrDeleteFailed           // infrastructure failure during delete
storage.ErrResumableNotSupported  // backend does not support resumable uploads
storage.ErrSignedURLNotSupported  // backend does not support signed URLs
```

## FileStorer

`FileStorer` wraps any `Client` and adds optional structured error logging with path context. Logging is opt-in via `WithLogger` — when configured, it uses three tiers so that logs stay actionable without being noisy:

| Error | Log level | Rationale |
|---|---|---|
| `ErrObjectNotFound` | **Warn** | A route 404 is visible in request logs, but a storage miss deeper in the call stack is not — Warn ensures it surfaces without triggering alerts. |
| `ErrInvalidPath`, `ErrResumableNotSupported`, `ErrSignedURLNotSupported` | *none* | Input validation or capability checks — the caller is responsible for handling these. |
| Everything else | **Error** | Unexpected infrastructure failure (network, permissions, etc.). |

```go
// With logging — logs not-found warnings and unexpected errors
storer := storage.New(client, storage.WithLogger(log))

// Without logging — errors returned silently to the caller
storer := storage.New(client)
```

All errors are returned to the caller regardless of whether logging is enabled — logging is purely observational.

## Implementations

| Package | Backend | Resumable upload | Signed URL |
|---|---|---|---|
| `diskstorage` | Local filesystem | no | no |
| `s3` | AWS S3, DO Spaces, MinIO | yes | yes |
| `gcs` | Google Cloud Storage | yes | yes |

### diskstorage

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/diskstorage"

client := diskstorage.New("/var/app/uploads")
```

All paths are validated against the base directory to prevent traversal attacks. Directory structure is created automatically on upload. `Delete` is idempotent — deleting a non-existent file returns nil.

Resumable uploads and signed URLs are not supported. For local development, use direct upload/download.

### s3

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/s3"

client, err := s3.New(cfg.BucketName, cfg.Region, cfg.AccessKeyID, cfg.SecretAccessKey, cfg.CustomEndpoint)
```

Works with AWS S3 and any S3-compatible service. Set `CustomEndpoint` for non-AWS targets:

```go
// Digital Ocean Spaces
s3.New(bucket, "nyc3", keyID, secret, "https://nyc3.digitaloceanspaces.com")

// MinIO
s3.New(bucket, "us-east-1", keyID, secret, "https://minio.example.com:9000")

// AWS S3 (no custom endpoint)
s3.New(bucket, "us-east-1", keyID, secret, "")
```

### gcs

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/gcs"

// Service account key (production)
client, err := gcs.New(cfg.BucketName, cfg.ProjectID, cfg.ServiceAccountKeyJSON)

// Application Default Credentials (local dev with gcloud auth)
client, err := gcs.New(cfg.BucketName, cfg.ProjectID, "")
```

When `serviceAccountKeyJSON` is empty, GCS falls back to Application Default Credentials. Call `client.Close()` when done.

## Compliance testing

`storagetest.RunSuite` validates any `Client` implementation:

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/storagetest"

func TestCompliance(t *testing.T) {
    client := diskstorage.New(t.TempDir())
    storagetest.RunSuite(t, client)
}
```

The suite covers upload/download round-trip, exists, delete, list, and object size.
