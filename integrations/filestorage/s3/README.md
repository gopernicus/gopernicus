# integrations/filestorage/s3

`Store` implements `filestorage.Storer` and `filestorage.SignedURLer` using the
AWS SDK for Go v2. Use it directly or through the interface your application
needs. AWS S3, MinIO and other compatible services use the same adapter, with
`Endpoint` and `UsePathStyle` selecting the compatible endpoint.

## Construction

`Open(ctx, Config)` loads AWS configuration and builds the client.
`New(*s3.Client, bucket) (*Store, error)` accepts a host-configured client and
rejects a nil client or blank bucket with `sdk.ErrInvalidInput`, without I/O.
Open may use the network for configuration and credential discovery; neither
constructor verifies bucket existence. A host owns the injected client's signing
configuration, transports and other resources.

| Config field | Environment tag | Meaning |
|---|---|---|
| `Bucket` | `S3_BUCKET` | Required target bucket |
| `Region` | `S3_REGION` | Signing region, e.g. `us-east-1` for MinIO; empty uses the AWS configuration chain |
| `AccessKeyID` | `S3_ACCESS_KEY_ID` | Static credential; supply both credential fields or neither |
| `SecretAccessKey` | `S3_SECRET_ACCESS_KEY` | Static credential; neither selects the default AWS credential chain |
| `Endpoint` | `S3_ENDPOINT` | Optional endpoint, e.g. `http://localhost:9000` |
| `UsePathStyle` | `S3_USE_PATH_STYLE` | Address `endpoint/bucket/key`; generally enabled for MinIO |

`Open` requires a signing region after loading configuration. An explicit
`Config.Region` overrides the AWS environment/shared configuration; if none
resolves, construction fails with `sdk.ErrInvalidInput` before a store is returned.

The `env:` tags work with `environment.ParseEnvTags`; struct literals remain
first-class. Paths and list prefixes follow SDK validation. Keys are preserved
literally and are never cleaned, trimmed or renamed. Inventory incompatible
legacy keys before upgrading; `List` reports an invalid stored key rather than
returning a key that other operations reject. Folder markers are excluded.
Literal prefixes ending in a partial `.` or `..` segment query the containing
prefix and filter locally, because MinIO rejects those literal query values.
This can scan more keys while preserving the same matches.

## Reads, uploads and errors

`Upload` accepts an arbitrary `io.Reader`, including streams with unknown length.
AWS `feature/s3/transfermanager` v0.1.6 buffers bounded 5 MiB parts with two
concurrent part requests; it does not buffer an entire object or require seeking.
The 10,000-part S3 limit gives this adapter a maximum stream size of about
48.8 GiB. Larger uploads require a host-configured provider upload path with
larger parts. The pinned transfer manager fits the existing AWS client versions;
it adds no dependencies to SDK.

A successful upload replaces the old object. Source errors and cancellation
prevent completion of an uncommitted upload. Multipart cleanup uses a separate
30-second timeout so a canceled request can still abort its upload. Returned
errors preserve source, cancellation, provider and cleanup causes. A failed
cleanup may leave billable multipart parts; hosts should also configure their
bucket's incomplete-upload lifecycle policy. Remote completion races cannot
promise rollback. Upload never closes its input; an arbitrary blocked `Read`
cannot be forcibly interrupted.

`Download`, ranges and size describe stored bytes. Ranges require nonnegative
offsets, length `-1` for the remainder or a nonnegative length, and no end
overflow. Zero-length reads confirm object existence; reads at/beyond EOF return
an empty reader. Range errors are normalized only after confirming object size.
Independent range and size requests do not form a snapshot during replacement.
Callers close returned readers.

Missing objects match both `filestorage.ErrObjectNotFound` and `sdk.ErrNotFound`.
Explicit `NoSuchBucket` and other provider errors are preserved. A bare HEAD 404
cannot distinguish a missing key from a missing bucket. `Delete` is idempotent
for missing objects. Invalid keys, ranges and expiry match `sdk.ErrInvalidInput`;
operation-specific upload/download/delete sentinels are gone. The adapter adds
no logging; hosts decide how to report returned errors. The pinned vendor
transfer manager itself logs failures completing multipart uploads.

## Optional protocols

`SignedURL(ctx, path, expiry)` creates a presigned GET URL without checking object
existence. Expiry must be whole seconds between one second and seven days;
credential expiry can shorten the URL's life. Credential I/O receives the caller
context. Treat returned URLs as bearer credentials.

`InitiateMultipartUpload(ctx, path, contentType)` returns an **S3 upload ID**.
The host must use the S3 API to upload parts and complete or abort that upload
with the same bucket and key. It does not implement `ResumableUploader`, which
promises a URL clients upload directly to with PUT. Migrate the old
`InitiateResumableUpload` concrete call to this accurately named method and keep
its multipart lifecycle in the host.

## Verification

`go build ./...`, `go test ./...`, `go vet ./...` and `go test -race ./...` run
hermetic tests using synthetic credentials and intercepted requests. They cover
streaming and multipart requests, source and cleanup failures, cancellation,
range boundaries, provider error chains, validation and signing.

`TestConformance_Live` additionally exercises the SDK conformance suite, a
non-seekable multipart upload and failed replacement, multipart cleanup, a
signed GET round trip and explicit multipart initiation. It creates and removes
a unique bucket. It skips loudly unless `S3_TEST_ENDPOINT` is set and requires
explicit test credentials. Use an owned disposable service:

```sh
docker run --rm -d -p 127.0.0.1:9000:9000 \
  -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin \
  quay.io/minio/minio server /data

S3_TEST_ENDPOINT=http://127.0.0.1:9000 \
  S3_TEST_ACCESS_KEY_ID=minioadmin \
  S3_TEST_SECRET_ACCESS_KEY=minioadmin \
  S3_TEST_REGION=us-east-1 \
  go test ./...
```
