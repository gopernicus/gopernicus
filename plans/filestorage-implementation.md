# Filestorage correctness and simplification

Status: COMPLETE — 2026-09-10. Owner authorized the recommendations with
“do it”, following [framework-audit-filestorage.md](framework-audit-filestorage.md).
Parent/handoff: [framework-audit.md](framework-audit.md).

## Preconditions and scope

- Branch/base: firestore-authentication / 6807ed06; 633 prior dirty entries.
- Task baseline: /tmp/gopernicus-filestorage-implementation-baseline.json, 1977
  visible files. Preserve all earlier/user work; compare against this baseline.
- Go 1.26.1, 42-module workspace, no root go.mod. GOCACHE is
  /tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter /Users/jrazmi/go/bin/goimports.
- Change SDK filestorage, required GCS/S3 adapter code/tests, framework wiring,
  canonical documentation, AUDIT-013 and release/handoff notes. No consumer edits,
  releases, production/cloud access, generated manual edits or unrelated pocket fixes.
- AWS upload support may require one vendor-family dependency in the S3 module.
  Keep SDK stdlib-only and existing unrelated dependency versions intact.
- Named implementer agents own disjoint provider modules; lead-backend-engineer
  and platform-sre reviews remain read-only. Current architecture and owner choices
  supersede stale examples in agent descriptions.

## Chosen contracts

1. Keep Storer's seven methods. Remove FileStore/New/Option/WithLogger and the
   unsupported optional-capability facade. Consumers use Storer or their narrow
   port, log returned errors and own concrete adapter resources.
2. Public ValidatePath, ValidatePrefix, ValidateRange and ValidateExpiry helpers
   are small shared adapter rules, not another storage service. ErrInvalidPath
   wraps sdk.ErrInvalidInput; ErrObjectNotFound wraps sdk.ErrNotFound. Remove
   ErrUploadFailed/ErrDownloadFailed/ErrDeleteFailed and unsupported sentinels.
   Preserve errors.Is/As for source, cancellation and provider errors.
3. Paths are nonempty relative UTF-8 slash-separated object keys. Reject leading
   or trailing slash, empty/dot/dot-dot segments, backslashes and NUL. Preserve
   spaces, Unicode, ordinary dot-prefixed segments and literal '..' in names.
   Prefixes additionally allow empty and trailing slash/partial final segments.
   Configured cloud Prefix is a validated directory prefix with at most one
   appended slash; no trimming or silent key rewriting.
4. List matches literal prefixes and returns caller-facing keys, excluding
   provider folder markers. It materializes its result and has no order guarantee.
   Existing incompatible provider keys require host inventory before migration.
5. ValidateRange requires offset >= 0, length >= -1, and no inclusive-end overflow.
   length -1 means to EOF; zero means an empty reader after confirming existence.
   Reads truncate at EOF and starting at/beyond EOF returns empty. Providers may
   normalize 416 only after confirming current object existence/size; don't hide
   permission/configuration failures. Concurrent replacement is not a snapshot
   across independent stat/read requests. Download/range/size refer to stored bytes.
6. Check context before work. Readers remain caller-owned; Upload does not close
   its input. Source failure/cancellation must abort uncommitted uploads and retain
   the previous object. Arbitrary blocked io.Reader calls cannot be forcibly
   interrupted; remote completion races cannot promise rollback. Successful writes
   deliberately replace existing objects; no fsync/power-loss durability promise.
7. SignedURL expiry must be whole seconds in [1 second, 7 days]. Check caller
   context before/after signing and propagate it to IAM/credential I/O. A signed
   URL is not an existence check and may expire earlier with its credentials.
8. ResumableUploader takes (ctx, path, ResumableUploadOptions{ContentType, Origin})
   and returns a client PUT session URI. Origin authorization/bucket CORS belong
   to the host. GCS keeps the capability; S3's concrete operation becomes
   InitiateMultipartUpload(ctx, path, contentType), returning its honest upload ID.

## Implementation sequence and ownership

- Root: SDK contracts/default/conformance; framework host and docs migration.
  Disk uses os.Root to confine operations; a concrete Close releases it. Stage
  writes inside the root, then rename after successful copy/close/context checks.
  Disk alone reserves the top-level .gopernicus-tmp directory case-insensitively, hides that subtree
  from every instance's List, and refuses object keys entering it. Ordinary
  .gopernicus-tmp-other names and partial-prefix matches remain valid. No global
  cloud-key restriction. Refuse staging symlinks/non-directories and create random
  O_EXCL files; never sweep another instance's files. Reject symlink components
  and other non-regular objects before opening; os.Root remains the race-resistant
  outside-root boundary. Hosts own/trust directory contents; root does not prevent
  hard links, mount/device changes or malicious within-root filesystem mutation.
  Rename EXDEV fails with the prior object intact, with no copy-overwrite fallback.
  Close only releases resources; hosts stop users first. A racing Close can leave
  hidden staging debris and does not drain arbitrary readers.
  New/replacement files use private mode0600; prior untouched files keep their
  permissions. Document this change for hosts with separate OS readers.
- GCS implementer: abort source failures using a child context; stored-byte reads;
  shared validation/errors/ranges; authenticated resumable API initiation with the
  retained configured HTTP client and Origin; context-aware signing with explicit
  IAM identity/private-key support. Keep its vendor options escape hatch. Avoid
  unbounded goroutine timeouts and raw bearer URLs in errors/logs. Freeze concrete
  signing config: Config.SigningServiceAccount explicitly selects context-aware IAM
  SignBlob through the retained authenticated HTTP client; otherwise local signing
  uses Config.CredentialsJSON's private_key/client_email. No implicit background
  identity lookup. Vendor-only credentials need explicit SigningServiceAccount or
  Config.CredentialsJSON for signed reads. Open can perform credential I/O.
- S3 implementer: bounded AWS-supported upload manager for unknown-length readers;
  shared validation/errors/ranges; correct explicit NoSuchBucket handling; reject
  incomplete static credential pairs; honest multipart initiation and expiry.
  Use feature/s3/transfermanager v0.1.6 compatible with current s3 pin; fixed 5 MiB
  parts/concurrency 2, bounded buffering and documented unknown-length size limit.
  Abort failed uploads with a separately bounded context and preserved causes;
  vendor abort otherwise inherits the canceled caller and can erase error identity.
- Read-only backend review: challenge confinement/publication, portable-key and
  provider-range edge cases; review final implementations independently.

## Verification

1. Format changed Go files with goimports. Expand shared conformance to actual
   streaming/failure/overwrite/cancellation, prefix, range, key and error behavior.
   Disk tests exercise real owned temporary files, symlinks and visibility.
2. Each changed module: go build ./..., go test ./..., go vet ./.... Race-check SDK
   filestorage and both adapters. Hermetic cloud request tests must intercept all
   HTTP and use synthetic credentials, not ambient credential discovery.
3. Exercise an owned S3-compatible service if available, including multi-part
   streaming, abort/replacement behavior and ranges. Initial Docker inspection was
   sandbox-denied; prove read-only daemon availability before creating a disposable
   loopback-only service. No real cloud resources or existing application stores.
4. Exercise actual framework HTTP media download/error mapping or a representative
   runnable temporary host with Disk and the SDK web responder. Verify caller-owned
   reader errors and optional capability type assertions through the migrated API.
5. Full make check (42-module build/test/vet, generation and guards), docs-build;
   report every unavailable live suite. Inspect generated outputs against baseline.
6. Update AUDIT-013/RELEASING with exact final APIs, path/byte/range changes,
   resources/logging, provider signing/session config and read-only consumer anchors.
   Final task-relative file inventory, git diff --check and reviewer findings.

## Progress / verification / unresolved work

SDK/host and both provider implementations are complete. Canonical documentation,
AUDIT-013, release notes and the master handoff describe the final APIs and gates.

- SDK build/test/vet passed. Expanded Disk race/conformance covers keys, literal
  prefixes, failure/cancellation/overwrite, ranges, source ownership, symlinks,
  regular files/FIFO refusal, private modes, root Close and multiple-instance
  staging visibility. Source review corrected panic-close cleanup, post-rename
  cleanup, and case-insensitive staging aliases; targeted tests pass.
- S3 build/test/vet/hermetic race and expanded live MinIO race passed. New shared
  cases exposed a non-fresh bucket factory and MinIO's refusal of literal '.'
  prefix queries; isolated factories and filtered containing-prefix queries fixed
  them. Real 6 MiB multipart and failed 7 MiB replacement/abort preserve bytes and
  leave no incomplete upload. S3 adds only transfermanager v0.1.6 plus its hashes.
  /tmp/gopernicus-filestorage-s3-live-final.log owns current service evidence.
- Real temporary Disk + HTTP/domain-responder probe passed under race:
  /tmp/gopernicus-filestorage-http-implementation.go and matching .log. Verified
  HTTP200 original bytes after failed replacement, HTTP404 missing object,
  HTTP400 traversal key, direct narrow port and source error/optional interfaces.
- GCS adds retained source-error capture because io.Copy can return a canceled
  write error instead of the simultaneous reader error. The shared cancellation
  test covers both error causes; final SDK, MinIO and GCS emulator runs passed it.
- Named platform source review found no remaining SDK or GCS blocker after
  corrections. Parent independently reviewed provider code; GCS IAM/HTTP ownership
  and upstream Close semantics were checked. No tests/services were run by the
  read-only reviewer.

Owned disposable services (both stopped and automatically removed; final Docker
inspection returned no matching containers):

- gopernicus-filestorage-audit-minio, image
  minio/minio:RELEASE.2025-09-07T16-13-09Z, http://127.0.0.1:56046;
  synthetic S3_TEST_ACCESS_KEY_ID=gopernicus-audit,
  S3_TEST_SECRET_ACCESS_KEY=filestorage-local-test-only, S3_TEST_REGION=us-east-1.
- gopernicus-filestorage-audit-gcs, cached fsouza/fake-gcs-server:latest,
  GCS_TEST_ENDPOINT=http://127.0.0.1:56443/storage/v1/,
  GCS_TEST_BUCKET=gopernicus-filestorage-audit; authentication disabled for emulator.

Initial Docker/HTTP probe sandbox access was denied; approved scoped reruns
succeeded. An initial formatter command used root-relative paths from sdk and
was corrected; Go build/test/vet itself passed. The first full make check reached
GCS during final test edits and caught an unused fmt import. Formatting removed
it; the final complete gate passed. No unresolved product or verification failure.
Cloud persistence/IAM permissions/browser CORS require external infrastructure and
remain unverified by local emulation. Full CMS media transactions and total
request-size limits stay in the later pocket audit.


## Final verification

All Go commands used GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.

| Working directory / scope | Passed command and evidence |
|---|---|
| sdk | `go build ./...`, `go test ./...`, `go vet ./...`; final `go test -race ./capabilities/filestorage/...` |
| integrations/filestorage/s3 | `go build ./...`, `go test ./...`, `go vet ./...`, `go test -race ./...`; `GOPROXY=off go mod tidy -diff` clean |
| integrations/filestorage/gcs | `go build ./...`, `go test ./...`, `go vet ./...`; `GOPROXY=off go mod tidy -diff` clean |
| S3 module, owned MinIO env above | `go test -race -count=1 -run '^TestConformance_Live$' ./...`; /tmp/gopernicus-filestorage-s3-live-final.log (1.587s) |
| GCS module, owned emulator env above | `go test -race -count=1 -v ./...`; /tmp/gopernicus-filestorage-gcs-implementation-live.log (1.554s), no skips, including synthetic credential/IAM tests and actual client session PUT |
| sdk, real owned files/loopback HTTP | `go run -race /tmp/gopernicus-filestorage-http-implementation.go`; matching .log confirms 200/404/400, source cause, retained bytes and direct optional interface behavior |
| repository root | `make check`; /tmp/gopernicus-filestorage-make-check-final.log: all 42 modules build/test/vet, tagged compilation, generation checks and 23 guards passed |
| repository root | `make docs-build`; /tmp/gopernicus-filestorage-docs-build.log: pnpm typecheck and Docusaurus static build passed; only the optional update-checker emitted a config-permission warning |
| task-relative paths | goimports on changed Go sources; final `git diff --check` clean; baseline inventory confirms no generated, unrelated pin or external consumer edits |

The normal workspace gate skips environment-gated Redis/SQL/Firestore/Turso/cloud
runtime suites when their targets are unset; tagged vet is compilation only.
This slice independently ran the storage tests against owned MinIO/fake-gcs-server.
No real AWS/GCS, external app, cloud IAM authorization/token exchange or browser
CORS claim is made. The full CMS UI/media workflow was not run: the example was
built/tested and the storage/web boundary exercised through a real temporary host.
Historical review probes use old APIs; use the committed regression tests and the
new implementation HTTP probe for this result. No release, commit or consumer
upgrade was performed. Both containers stopped; no task service remains running.

## Changed files and next step

Task-relative inventory below is compared with the 1977-file baseline, not the
large preexisting HEAD diff. AUDIT-001 through AUDIT-012 are byte-for-byte preserved.
Only the S3 upload manager was added as a new dependency; GCS's existing auth pin
became direct for tests. go.work and tracked generated assets are unchanged.
Go commands also maintained the ignored go.work.sum checksum entries for the new
AWS dependency; that local bookkeeping file is outside the visible-file baseline
and was not manually edited. An initial final-inventory assertion assumed it was
tracked; the corrected visible-file check passed for all paths below.

37 paths:

- `AUDIT.md`
- `RELEASING.md`
- `examples/cms/cmd/server/main.go`
- `integrations/filestorage/gcs/README.md`
- `integrations/filestorage/gcs/gcs.go`
- `integrations/filestorage/gcs/gcs_test.go`
- `integrations/filestorage/gcs/go.mod`
- `integrations/filestorage/gcs/internal_test.go`
- `integrations/filestorage/gcs/objects.go`
- `integrations/filestorage/gcs/objects_test.go`
- `integrations/filestorage/gcs/resumable.go`
- `integrations/filestorage/gcs/resumable_live_test.go`
- `integrations/filestorage/gcs/resumable_test.go`
- `integrations/filestorage/gcs/signing.go`
- `integrations/filestorage/gcs/signing_test.go`
- `integrations/filestorage/s3/README.md`
- `integrations/filestorage/s3/contract_test.go`
- `integrations/filestorage/s3/go.mod`
- `integrations/filestorage/s3/go.sum`
- `integrations/filestorage/s3/s3.go`
- `integrations/filestorage/s3/s3_test.go`
- `plans/filestorage-implementation.md`
- `plans/framework-audit-filestorage.md`
- `plans/framework-audit.md`
- `pockets/cms/domain/media/blobstore.go`
- `sdk/README.md`
- `sdk/capabilities/filestorage/disk.go`
- `sdk/capabilities/filestorage/disk_test.go`
- `sdk/capabilities/filestorage/disk_unix_test.go`
- `sdk/capabilities/filestorage/errors.go`
- `sdk/capabilities/filestorage/filestoragetest/contract.go`
- `sdk/capabilities/filestorage/filestoragetest/filestoragetest.go`
- `sdk/capabilities/filestorage/filestorer.go`
- `sdk/capabilities/filestorage/validation.go`
- `workshop/documentation/docs/integrations/catalog.md`
- `workshop/documentation/docs/sdk/capabilities.md`
- `workshop/documentation/docs/sdk/overview.md`

Next: review SDK email/notify, then oauth/tracing and S10 pocket wiring. Full
pocket reviews remain separate. Carry forward CMS blob/metadata reconciliation
and its intended total upload ceiling, and the prior pgxdb pool-default issue.
Consumer upgrades use AUDIT-013 plus earlier entries when their host is ready.
