# integrations/filestorage/s3

This module wraps the [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2)
`service/s3` client (with its presign client and multipart API) to implement the
sdk `filestorage` ports. It is the S3 backend for the framework's file-storage
facility, and it works against any S3-compatible service — AWS S3, MinIO, and
DigitalOcean Spaces — through a custom-endpoint option and a path-style
addressing option.

It is its own module (`github.com/gopernicus/gopernicus/integrations/filestorage/s3`) and depends only
on `sdk` (for the `filestorage` sentinels its errors map to) and the
aws-sdk-go-v2 family.

## Ports implemented

`Store` implements the core port and both optional capability interfaces the sdk
advertises via type assertion, asserted at compile time in `s3.go`:

| sdk interface | how |
|---|---|
| `filestorage.Storer` | `PutObject` / `GetObject` / `DeleteObject` / `HeadObject` / `ListObjectsV2` (paginated), `GetObject` with a `Range` header for `DownloadRange` |
| `filestorage.SignedURLer` | the s3 **presign client** — `PresignGetObject` mints a short-lived signed GET URL (a local signing operation, no network) |
| `filestorage.ResumableUploader` | S3 **multipart upload** — `CreateMultipartUpload` returns the upload ID as the resumable session identifier |

S3 errors map to the sdk sentinels: `NoSuchKey` / `NotFound` (and untyped 404s
from compatible services) → `filestorage.ErrObjectNotFound`; other read failures
→ `ErrDownloadFailed`; upload/delete failures → `ErrUploadFailed` /
`ErrDeleteFailed`. `Delete` is idempotent (a missing object is not an error).

## Construction

- `Open(ctx, Config)` builds a client from a `Config`. It performs no
  construction-time network round trip — credentials resolve lazily on first
  use, so a bad endpoint surfaces on the first operation.
- `New(*s3.Client, bucket)` wraps a caller-supplied, fully configured client
  (bring-your-own).

### Config (S3-compatible seam)

`Config` carries `env:` tags for `sdk/environment.ParseEnvTags`; struct-literal
construction stays first-class.

| field | env | purpose |
|---|---|---|
| `Bucket` | `S3_BUCKET` | target bucket (required) |
| `Region` | `S3_REGION` | signing region (e.g. `us-east-1` for MinIO) |
| `AccessKeyID` | `S3_ACCESS_KEY_ID` | static credential; empty ⇒ default AWS chain |
| `SecretAccessKey` | `S3_SECRET_ACCESS_KEY` | static credential |
| **`Endpoint`** | `S3_ENDPOINT` | custom endpoint for a compatible service (e.g. `http://localhost:9000` for MinIO, `https://nyc3.digitaloceanspaces.com` for Spaces) |
| **`UsePathStyle`** | `S3_USE_PATH_STYLE` | force path-style addressing (`endpoint/bucket/key`); MinIO and on-host deployments require it |

## Tests

`go test ./...` is hermetic: it runs offline with no credentials and no docker,
exercising error mapping, the port contract, and presigned-URL generation (a
purely local signing operation).

The live conformance leg (`TestConformance_Live`) runs the sdk
`filestoragetest` suite plus a signed-URL HTTP round trip and a multipart
initiate against a real S3-compatible service. It **skips loudly** unless
`S3_TEST_ENDPOINT` is set (with `S3_TEST_ACCESS_KEY_ID`,
`S3_TEST_SECRET_ACCESS_KEY`, and optional `S3_TEST_REGION`), so `make check`
stays hermetic while a silent green never claims unverified S3 conformance. It
creates a unique bucket per run and empties + drops it afterward.

Run it against a dockered MinIO:

```sh
docker run --rm -d -p 9000:9000 \
  -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin \
  quay.io/minio/minio server /data

S3_TEST_ENDPOINT=http://localhost:9000 \
  S3_TEST_ACCESS_KEY_ID=minioadmin \
  S3_TEST_SECRET_ACCESS_KEY=minioadmin \
  S3_TEST_REGION=us-east-1 \
  go test ./...
```
