# GCS file storage

`Store` implements `filestorage.Storer`, `filestorage.SignedURLer` and
`filestorage.ResumableUploader` using the Google Cloud Storage client family.
Construct it in the host's composition root, pass the required interface to its
consumers, and close the concrete store after active operations finish.

```go
store, err := gcs.Open(ctx, gcs.Config{
    Bucket:                "app-uploads",
    Prefix:                "tenant-a",
    SigningServiceAccount: "url-signer@example.iam.gserviceaccount.com",
})
if err != nil {
    return err
}
defer store.Close()

err = store.Upload(ctx, "documents/report.pdf", reader)
```

`Open` validates local settings before constructing the client. Credential
discovery can perform I/O; give startup a context with a deadline. Ordinary
operations do not need signing configuration.

## Configuration

| Field or option | Behavior |
| --- | --- |
| `Bucket` | Required target bucket. The host creates it. |
| `Prefix` | Optional canonical directory key, such as `tenant-a` or `tenant-a/`. A missing final slash is added; invalid keys are rejected. |
| `CredentialsJSON` | Explicit credentials JSON; empty follows the vendor's Application Default Credentials path unless vendor options override it. A service-account `private_key` and `client_email` permit local URL signing. |
| `SigningServiceAccount` | Explicit service-account email or unique ID for IAM SignBlob. When nonempty, selects IAM even if `CredentialsJSON` contains a private key. |
| `Endpoint` | Storage API override for an emulator, with default authentication disabled. |
| `WithClientOption(...option.ClientOption)` | Vendor credentials, scopes, HTTP client, quota project and endpoint settings, applied after Config. Storage operations, resumable initiation and IAM share the configured HTTP client. |

The vendor's `STORAGE_EMULATOR_HOST` default is honored; explicit endpoint options
win. A host supplying its own HTTP client also supplies that client's
authentication behavior. Do not disable authentication for production endpoints.
`Close` releases storage resources and closes idle connections on that client;
hosts should account for this when sharing a custom HTTP client.

## Keys, bytes and failures

Keys are relative UTF-8 slash-separated names. Leading/trailing or repeated
slashes, dot/dot-dot segments, backslashes and NUL are rejected. Spaces, Unicode,
`.segovia` and names containing literal `..` remain unchanged. Configured `Prefix`
is a complete directory key; `List` takes a literal prefix that can end in a
partial component, including `.`. It materializes caller-facing keys, removes the
configured directory prefix, excludes provider folder markers, and has no ordering
guarantee. An incompatible existing key causes a validation error.

`Upload` streams its input without closing it. A source error or cancellation
aborts the child upload before closing its writer, preserving an existing object
when the upload has not committed. Reader, provider and context errors remain
available through `errors.Is`/`errors.As`. Arbitrary blocked reader calls cannot
be interrupted; cancellation racing remote completion cannot guarantee rollback.
Successful uploads deliberately replace existing objects.

Downloads, sizes and byte ranges describe the **stored bytes**, including the
compressed bytes of gzip-encoded objects. Callers close returned readers. Ranges
require nonnegative offsets and length `-1` (to EOF) or a nonnegative length.
Length zero confirms existence and returns an empty reader; reads truncate at EOF
and starting at/beyond EOF returns empty. A provider 416 is treated as EOF only
after stat confirms the current size. Other stat errors remain visible. A
concurrent replacement is not a shared snapshot across stat and read requests.

`filestorage.ErrInvalidPath` wraps `sdk.ErrInvalidInput`.
`storage.ErrObjectNotExist` remains in the error chain alongside
`filestorage.ErrObjectNotFound` and `sdk.ErrNotFound`. Missing-object `Delete`
succeeds and `Exists` returns false without an error. Permission and configuration
failures remain errors. The adapter does not log; hosts own logging and HTTP
error mapping.

## Signed reads

`SignedURL(ctx, path, expiry)` returns a V4 GET URL without checking existence.
Expiry must be whole seconds from one second through seven days. The URL may
expire sooner with its credentials; treat it as a bearer credential.

With `SigningServiceAccount`, IAM requests and credential refresh use the caller's
context. The configured credentials need permission to sign for that identity.
Otherwise, local signing requires the private key and email in
`Config.CredentialsJSON`. There is no implicit background metadata or signing
identity lookup. Credentials supplied only through vendor options do not infer a
local signing identity: also select `SigningServiceAccount`, or put the local
service-account JSON in Config. See the [IAM SignBlob API](https://docs.cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/signBlob).

Context is checked before and after operations. Custom transports and legacy
`oauth2.TokenSource` implementations retain their own cancellation semantics;
the adapter cannot interrupt an implementation that ignores the request context.

## Resumable uploads

```go
session, err := store.InitiateResumableUpload(ctx, "video/clip.mp4",
    filestorage.ResumableUploadOptions{
        ContentType: "video/mp4",
        Origin:      "https://app.example.com",
    })
```

This sends an authenticated JSON API initiation request through the configured
HTTP client and returns the session's absolute URI. It needs upload credentials,
not a private key or IAM signing rights. The client sends its PUT payload to that
URI. The host authorizes the supplied browser Origin, configures storage access
and CORS, and sends Origin on the browser's subsequent upload requests. Initiation
forwards Origin and the requested content type; it does not enforce host origin
policy. See Google's [resumable upload instructions](https://docs.cloud.google.com/storage/docs/performing-resumable-uploads?hl=en)
and [browser origin requirements](https://docs.cloud.google.com/storage/docs/resumable-uploads?hl=en).

The session URI is a bearer credential. The adapter returns it to the caller but
omits response bodies and session-bearing URLs from formatted initiation errors.
HTTP status failures retain `*googleapi.Error` with the status; transport errors
retain their cause. Hosts should not log the returned URI or unwrap and print raw
transport errors. Without a caller deadline, initiation adds a 15-minute context
timeout. Actual interruption still depends on the configured transport honoring
that context.

## Migration

- Pass the concrete store or its interface directly; the SDK `FileStore` facade
  and unsupported/operation error wrappers are removed.
- Replace the third resumable argument with `ResumableUploadOptions` and provide
  a host-approved Origin for browser use. Initiation now uses authenticated JSON
  API requests and the configured HTTP client.
- Configure signing explicitly as described above. ADC/vendor-only credentials
  no longer trigger implicit signing identity discovery.
- Inventory legacy keys before upgrading. Leading slashes are no longer trimmed,
  invalid Config prefixes fail at Open, and List fails on incompatible object
  keys. Move or rename those objects under a host-owned migration.
- Reads of gzip-encoded objects now return stored compressed bytes. Range and
  expiry validation follow the shared SDK contract.

## Verification

Hermetic tests cover synthetic local and IAM signing, context propagation into
credential refresh, upload abort/error ownership, stored compressed bytes, range
normalization, literal prefixes and configured resumable HTTP requests. They do
not read real credentials or use ADC. The two environment-gated tests skip when
no target is supplied:

```sh
GCS_TEST_BUCKET=conformance \
GCS_TEST_ENDPOINT=http://127.0.0.1:4443/storage/v1/ \
GCS_TEST_CREDENTIALS_JSON= \
go test -race -run '^(TestConformance_GCS|TestResumableSession_GCS)$' -v .
```

Use a disposable `fake-gcs-server` with its external URL matching the mapped
loopback address. `TestConformance_GCS` creates a fresh prefix per factory call;
`TestResumableSession_GCS` performs initiation, client PUT and download/size
verification. Emulator setup creates the named bucket; no cloud bucket is created
by the adapter. The optional `GCS_TEST_PROJECT` defaults to `test-project` for
emulator bucket creation. With no endpoint, conformance uses a pre-existing bucket
and explicitly supplied `GCS_TEST_CREDENTIALS_JSON` or the vendor's credentials.
Only use that mode with a separately authorized target.

Local emulator tests do not prove real cloud IAM permissions, persistent storage
durability or a browser's CORS enforcement.

`WithClientOption` copies its input slice when created, and repeated calls append
vendor options in order after `Config` settings. Vendor option objects retain
their vendor-defined ownership and nil behavior. A nil Gopernicus `Option` makes
`Open` return an error wrapping `sdk.ErrInvalidInput` before credential discovery
or client allocation.
