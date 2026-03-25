# infrastructure/storage -- File Storage Reference

Package `storage` provides file storage infrastructure with pluggable backends (local disk, GCS, S3).

**Import:** `github.com/gopernicus/gopernicus/infrastructure/storage`

## Client Interface

The port that all storage backends implement.

```go
type Client interface {
    Upload(ctx context.Context, path string, reader io.Reader) error
    Download(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
    List(ctx context.Context, prefix string) ([]string, error)
    DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
    GetObjectSize(ctx context.Context, path string) (int64, error)
}
```

- `Download` / `DownloadRange` -- caller is responsible for closing the returned reader.
- `DownloadRange` with `length = -1` reads from offset to end of file.
- `List` returns full paths under the given prefix.

## FileStorer

Wraps a `Client` with structured logging.

```go
fs := storage.New(log, client)
```

`FileStorer` exposes the same methods as `Client` (`Upload`, `Download`, `Delete`, `Exists`, `List`, `DownloadRange`, `GetObjectSize`), adding debug/error logging to each operation.

## Error Sentinels

```go
var (
    ErrObjectNotFound = errors.New("object not found")
    ErrInvalidPath    = errors.New("invalid storage path")
    ErrUploadFailed   = errors.New("upload failed")
    ErrDownloadFailed = errors.New("download failed")
    ErrDeleteFailed   = errors.New("delete failed")
)
```

## Backends

### diskstorage (Local Filesystem)

Stores files on the local filesystem. Suitable for development and single-server deployments.

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/diskstorage"

client, err := diskstorage.New(diskstorage.Config{
    BasePath: "/var/data/uploads",
})
fs := storage.New(log, client)
```

### gcs (Google Cloud Storage)

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/gcs"

client, err := gcs.New(ctx, gcs.Config{
    BucketName:            "my-bucket",
    ProjectID:             "my-project",
    ServiceAccountKeyJSON: keyJSON,
})
fs := storage.New(log, client)
```

### s3 (S3-Compatible)

Works with AWS S3, DigitalOcean Spaces, MinIO, and other S3-compatible services.

```go
import "github.com/gopernicus/gopernicus/infrastructure/storage/s3"

client, err := s3.New(s3.Config{
    BucketName:      "my-bucket",
    Region:          "us-east-1",
    AccessKeyID:     accessKey,
    SecretAccessKey: secretKey,
    CustomEndpoint:  "", // empty for AWS, or "https://nyc3.digitaloceanspaces.com"
})
fs := storage.New(log, client)
```

## Usage Example

```go
// Upload
err := fs.Upload(ctx, "avatars/user-123.png", file)

// Download
reader, err := fs.Download(ctx, "avatars/user-123.png")
defer reader.Close()

// Check existence
exists, err := fs.Exists(ctx, "avatars/user-123.png")

// List
paths, err := fs.List(ctx, "avatars/")

// Partial download (e.g., for video streaming)
reader, err := fs.DownloadRange(ctx, "videos/intro.mp4", 0, 1024*1024)
defer reader.Close()

// Get size
size, err := fs.GetObjectSize(ctx, "videos/intro.mp4")

// Delete
err = fs.Delete(ctx, "avatars/user-123.png")
```

## Testing

The `storagetest` subpackage provides helpers for testing storage operations.

## Related

- [sdk/web](../sdk/web.md) -- `RespondStream` and `RespondFile` for serving stored files
