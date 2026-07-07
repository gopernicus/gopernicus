# integrations/filestorage/gcs

A file-storage connector wrapping exactly one third-party library-family —
`cloud.google.com/go/storage` (plus `google.golang.org/api`'s client options and
iterator, the storage client's own required surface). Its `Store` implements the
`sdk/filestorage` core `Storer` port and, because Google Cloud Storage supports
them, the two optional capability interfaces `ResumableUploader` and
`SignedURLer`.

It owns "how to talk to GCS," never any app's storage policy. A different vendor
(`s3`, …) is a sibling connector, swapped at the composition root. A host that
mounts a feature needing file storage wires this in `cmd` and never sees the
vendor elsewhere.

## Surface

| member | shape |
|---|---|
| `Open(ctx, cfg Config, opts ...Option) (*Store, error)` | builds a bucket-scoped store; makes no network round trip (the client authenticates lazily on first use) |
| `WithClientOption(...option.ClientOption) Option` | bring-your-own escape hatch for settings `Config` does not surface |
| `Store.Close() error` | releases the underlying client |
| `Store` (core `Storer`) | `Upload` / `Download` / `Delete` / `Exists` / `List` / `DownloadRange` / `GetObjectSize` |
| `Store` (`ResumableUploader`) | `InitiateResumableUpload` — signs a V4 POST URL with `x-goog-resumable:start`, POSTs it, returns the session URI |
| `Store` (`SignedURLer`) | `SignedURL` — short-lived V4 signed GET URL, signed locally when credentials carry a private key |

## Config

| field | purpose |
|---|---|
| `Bucket` | target bucket (required) |
| `Prefix` | optional key root prepended to every path — the GCS analogue of the sdk `Disk` default's base directory; scopes one `Store` to a bucket subtree and is transparent to callers (`List` strips it back off) |
| `CredentialsJSON` | service-account key JSON; empty falls back to Application Default Credentials. A key with a private key also lets `SignedURL` sign locally |
| `Endpoint` | storage API host override for a `fake-gcs-server` / emulator; when set, `Open` also disables authentication |

## Error contract

Not-found conditions (`storage.ErrObjectNotExist`) map to
`filestorage.ErrObjectNotFound` so `errors.Is` at the call site stays
backend-agnostic. `Delete` of a missing object is a no-op (idempotent), matching
the sdk `Disk` default. Unrecognized driver errors pass through unchanged.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — error mapping,
prefix/key normalization, the compile-time port-satisfaction assertions, a
run-time optional-capability presence check, and a fully offline V4 signed-URL
test that signs with a self-generated RSA service-account key (no network, no
cloud spend).

The conformance leg wires the sdk `filestoragetest` suite against a live GCS API
and is env-gated on `GCS_TEST_BUCKET`; absent it, the leg **skips loudly** so
`make check` stays hermetic. Run it against a throwaway `fake-gcs-server`
(no credentials, no spend):

```
docker run --rm -d -p 4443:4443 fsouza/fake-gcs-server -scheme http -public-host localhost:4443
GCS_TEST_BUCKET=conformance \
  GCS_TEST_ENDPOINT=http://localhost:4443/storage/v1/ \
  go test ./...
```

or against real GCS with a bucket you own:

```
GCS_TEST_BUCKET=my-bucket \
  GCS_TEST_CREDENTIALS_JSON="$(cat sa-key.json)" \
  go test ./...
```

| env var | meaning |
|---|---|
| `GCS_TEST_BUCKET` | gate + target bucket for the conformance leg (required to run it) |
| `GCS_TEST_ENDPOINT` | emulator/`fake-gcs-server` URL; when set, the bucket is auto-created and auth is disabled |
| `GCS_TEST_CREDENTIALS_JSON` | service-account key JSON for a real-GCS run |
| `GCS_TEST_PROJECT` | project ID used only for emulator bucket creation (default `test-project`) |
