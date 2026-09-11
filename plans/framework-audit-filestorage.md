# SDK audit S9a: File storage

Status: REVIEW COMPLETE — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).
The owner subsequently approved implementation; final contracts, verification and
AUDIT-013 are recorded in [filestorage-implementation.md](filestorage-implementation.md).
The evidence below describes the pre-implementation review.
Review SDK file storage, its bundled adapters and actual consumers for correctness,
clarity and useful simplification. Record evidence/recommendations before source
changes. AUDIT.md records implemented changes only; it remains through AUDIT-012.

## Preconditions and scope

Branch/base firestore-authentication / 6807ed06; 632 prior dirty entries.
Snapshot /tmp/gopernicus-filestorage-review-baseline.json contains 1976 visible files.
Preserve earlier audits/concurrent changes. Go 1.26.1; 42 modules, no root go.mod.
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter
/Users/jrazmi/go/bin/goimports. Review-only repository changes belong in this plan
and the master audit handoff. Temporary probes belong under /tmp and use only
owned temporary files/loopback services. No cloud credentials, deployed buckets,
external consumer edits, dependency/pin changes or generated manual edits.

## Review sequence

1. Inventory SDK Storer, FileStore wrapper, optional ports, errors, Disk and tests.
   Specify actual key/prefix/range/cancellation/overwrite/resource semantics.
2. Trace framework and sampled Segovia v2/Coordination Hub/GPS360 consumers. Compare
   original framework intent, especially direct uploads and signed reads, before
   treating unused optional capabilities as deletion candidates.
3. Review GCS/S3 against the shared contract, using pinned-library source and
   official provider documentation where behavior needs verification. Separate
   confirmed defects from incomplete features and provider-specific limitations.
4. Run focused build/test/vet and race tests; add independent temporary probes for
   important uncovered paths. Use local fake HTTP only if it proves adapter behavior
   without touching cloud state. Passing green existing tests is not sufficient.
5. Recommend the smallest useful contract and implementation changes, explaining
   compatibility/consumer impact. Record findings and final verification/limits;
   update the master handoff. Do not add speculative AUDIT-013 migration entries.

Named project agents may provide bounded read-only contract/provider reviews as
requested by the applicable AGENTS instructions. Current architecture/audit owner
choices override stale examples in agent descriptions. Full CMS media/domain and
consumer application audits remain later work.

## Contract and usage inventory

The SDK has seven meaningful core operations (Upload, Download, Delete, Exists,
List, DownloadRange, GetObjectSize), two optional ports, a 147-line FileStore
facade (filestorer.go:66 onward), seven sentinels, and a 179-line Disk default.
Keep the named filestorage capability: this is coherent I/O with resource and
transport semantics, not a small root-SDK helper. There is no reason to rename
its core methods solely for style or split every method into a new exported port.
Consumers can already declare narrow interfaces, as CMS does.

| Consumer snapshot | What actually depends on storage |
|---|---|
| Framework CMS | media.BlobStore owns only Upload/Download/Delete; examples/cms constructs Disk + FileStore. Asset metadata, validation and HTTP serving are pocket-owned. |
| Segovia v2, main / 76b3d78, SDK v0.8.0 | Live dashboard bundle Upload/Download/Exists via GCS production / Disk development. Non-seekable limitedReader over tar/gzip/multipart; source ErrBundleTooLarge must survive errors.Is. |
| GPS360, clean main / e1ab3f0, SDK v0.7.1 | comps worker archives an os.File; same computed key deliberately overwrites on replay. Server storage boots but is currently idle. |
| Coordination Hub, clean main / 84ff08a, SDK v0.7.0 | Own S3-compatible bucket implementation, not upstream FileStore. Public/private buckets, MIME derivation, immutable cache headers and CDN URL construction are application policy. |
| Original, docs/fix-cli-and-framework-reference-drift / 0f763a9 | Historical facade and real GCS signed/read/session ideas. Its S3 multipart-ID mismatch is also historical; copying it does not make the generic promise correct. |

Segovia has two unrelated untracked parent plans; original has untracked NEXT.md.
No sample was changed, built, upgraded or run. These are current source samples,
not a claim about unknown consumers or every deployed key. No sampled production
code uses List/range/size or signed/resumable APIs; that does not justify deleting
intentional framework capabilities.

Important consumer anchors (absolute paths for another context):

- /Users/jrazmi/code/segovia/segovia/v2/internal/outbound/domains/dashboards/content.go:32
  wraps FileStore and preserves ErrBundleTooLarge; logic/domains/dashboards/service.go:328
  supplies the non-seekable reader (limitedReader at :453).
- Segovia logic/domains/dashboards/dashboard.go:174 builds
  dashboards/<id>/<revision>/<cleaned bundle path>; cmd/server/filestorage.go:25
  uses `.segovia/boot-probe`. Preserve dot-prefixed ordinary segments, spaces,
  Unicode and literal `..` within a filename; reject traversal *segments*.
- /Users/jrazmi/code/gps/three-sixty/gps-360-go/cmd/workers/comps/main.go:489, :513,
  :535 preserves comps/<race>/<timestamp>-<digest12>.xlsx. Its
  integrations/compsfeed/drivexlsx/drivexlsx.go:590 trims race names without slugging.
- /Users/jrazmi/code/gps/coordination-hub/integrations/objectstore/objectstore.go:138
  contains useful host policy that must survive any later adapter adoption.
- /Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original/infrastructure/storage/gcs/client.go:167
  carries the original direct-upload intent; s3/client.go:219 returns a multipart ID.

## Findings

### F1 — Disk is not confined to its root; malformed keys alias real objects

**High; reproduced.** disk.go:34–40 cleans a string against a synthetic root,
then ordinary os calls follow filesystem symlinks. An internal directory symlink
allowed Download, Upload and Delete to read/write/delete files outside Disk.base.
The probe's targets were all owned temporary files, not real user data. The
precondition is a symlink in the storage tree; this is not a claim that CMS users
can currently create such a link through an upload route.

The same cleaning silently maps `../name.txt` to `name.txt`, overwriting that
object. Empty Delete removes an empty storage root. Exists and Download accept
actual directories as objects; the latter fails only when bytes are read.
The existing traversal test pins lexical containment and misses these cases.

Use a documented canonical key contract and stdlib os.Root operations, not an
EvalSymlinks-before-open check that introduces another race. Go 1.26.1 already
provides the required root-relative filesystem operations. Reject empty/root,
absolute, dot/dot-dot segments, repeated separators and backslashes for portable
slash-separated keys; do not silently clean, slug, case-fold or normalize Unicode.
Dot-prefixed ordinary names remain valid. Prefix validation separately permits
empty prefix and trailing slash/partial final components. Reject non-regular files
as objects; explicitly document host-owned directory trust and filesystem limits
(case sensitivity, hard links/mounts/device files are not magically normalized).

[Go's traversal-resistant APIs](https://go.dev/blog/osroot) describe why lexical
validation alone cannot provide this confinement. Root's local Go source at
/usr/local/go/src/os/root.go:31 documents platform limits. Resource ownership
should remain concrete/host-owned if Disk keeps an open Root; do not add Close to
Storer just to force every cloud backend to implement a dummy method.

### F2 — Failed uploads publish partial objects and destroy good replacements

**High; Disk reproduced, GCS request finalization reproduced with fake transport.**
Disk Upload (disk.go:44–58) truncates the destination before reading, leaves the
partial result on source failure, and ignores file Close errors. A failed new
upload also remains visible. A canceled Disk upload can create a new object and
read its source; cancellation is ignored by every Disk operation.

GCS Upload (gcs.go:150–161) calls Writer.Close after io.Copy fails. That finalizes
the bytes already accepted. With an injected reader returning partial bytes and
an error, the adapter sent the final upload request containing those bytes, then
returned the source error. Do not confuse a known failed source with an ambiguous
remote commit error: local source failure must abort uncommitted upload state.

Disk should stage into a confined temporary file, check copy/close/context, then
atomically replace the destination and clean up on failure. Preserve deliberate
overwrite behavior. GCS should cancel a child upload context before writer cleanup
on source failure. Preserve errors.Is for source/cancellation errors through any
cleanup wrapping. Check context before admission and between reads/list work;
cooperative I/O cannot forcibly interrupt an arbitrary blocked io.Reader. Neither
an error racing remote completion nor a process power loss implies rollback.
Atomic publication is not a claim of filesystem fsync durability.

### F3 — S3 advertises a resumable protocol it does not implement

**High contract defect; reproduced without cloud.** SDK filestorer.go:50–53 promises
a session URI clients PUT to. S3 s3.go:261–281 instead returns a multipart upload
ID, requiring UploadPart and CompleteMultipartUpload (or Abort). Its comment
redefines the SDK promise as an opaque token. A generic caller cannot use the
returned string as promised; tests merely assert a nonempty ID.

Keep the intentional optional capability and GCS's real session-URI implementation.
Remove S3's claim to ResumableUploader and name its current operation explicitly,
e.g. InitiateMultipartUpload, with the returned ID and required host-owned
part/completion/abort lifecycle documented. Do not replace resumability with a
single signed PUT or invent a generic multipart framework just to make the types
look uniform. An SDK optional port must describe an actually shared protocol.
The [AWS multipart API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html)
documents the ID and separate part/completion/abort requests.

### F4 — S3 Upload does not honor arbitrary io.Reader for supported HTTP endpoints

**High portability defect; reproduced before transport.** s3.go:134–143 passes an
arbitrary reader directly to PutObject. With the pinned SDK and a non-seekable
reader on an HTTP S3-compatible endpoint, signing fails with a seek-body error;
no request reaches the transport. This breaks the advertised MinIO configuration
and the shape of Segovia's streaming reader if adapted to it. The same reader
reaches an HTTPS fake transport; that alone does not prove cloud acceptance.

Preserve the generic io.Reader port and source error chains. Select an AWS-supported
bounded upload path that handles unknown length and retries appropriately; do not
force callers to seek, io.ReadAll unbounded data or loosen request signing as an
incidental workaround. The concrete choice belongs in the implementation plan
and the AWS integration, not new SDK/vendor dependencies. Verify non-seekable
streaming against an owned compatible service as well as transport tests.
[AWS's streaming guidance](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/sdk-utilities-s3.html#unseekable-streaming-input)
recommends its upload manager for non-seekable inputs of unknown length; configure
bounded part buffering/concurrency and cleanup rather than rebuilding that machinery.

### F5 — Missing-object errors do not compose with SDK; S3 hides bucket failures

**Medium/high; reproduced.** errors.go defines independent not-found/invalid-path
sentinels. A missing Disk object is ErrObjectNotFound but not sdk.ErrNotFound;
web.ErrFromDomain maps it to 500. CMS's Serve uses that responder, so stored
metadata with missing bytes is reported as an internal error through this path.

S3 isNotFound (s3.go:295–315) accepts any HTTP404 before distinguishing provider
error codes. A synthetic explicit NoSuchBucket response became Exists=false,
Delete=nil, and Download=ErrObjectNotFound. That hides configuration failure as
normal absence. Bare HEAD404 may be ambiguous; do not claim an adapter can infer
information the provider did not return. Preserve explicit NoSuchBucket/permission
causes and do not blanket-map every structured 404 to object absence.

Make ErrObjectNotFound wrap sdk.ErrNotFound and ErrInvalidPath wrap sdk.ErrInvalidInput,
or migrate directly to root kinds with clear compatibility instructions. Remove
operation-only ErrUploadFailed/ErrDownloadFailed/ErrDeleteFailed: only S3 uses them,
no sampled consumer branches on them, and operation context + preserved cause is
clearer than three inconsistently applied classifications. Preserve genuine provider
and reader errors. A missing configured Disk root should likewise not masquerade
as a missing object; construction/operational failures are distinct.

### F6 — List and byte ranges have backend-dependent meanings

**Medium; reproduced/source-confirmed.** Disk List("im") returns nothing even with
img/a.txt and image.txt; cloud List uses literal object-key prefixes. The SDK's
"directory or prefix" comment (filestorer.go:30–34) explicitly permits this drift,
so this is a contract correction as well as an implementation change. Prefer literal
prefix semantics everywhere, returning caller-facing keys. Empty prefix means all
objects. List currently materializes everything; document that cost and postpone
pagination until a real use requires a new interface.

Ranges accept invalid values inconsistently. Disk treats every negative length as
"to end"; GCS accepts suffix offsets; S3 constructs bytes=0--1 for (0,0), bytes=3-2
for (3,0), bytes=-3- for (-3,-1), and overflowing negative range ends. Cloud request
probes show these headers, not real-service acceptance of malformed requests.

Require offset >= 0, length == -1 or >= 0, and checked end arithmetic. Give zero
length one explicit meaning: an empty reader after confirming object existence.
Prefer normal read semantics for valid ranges: truncate at EOF and return an empty
reader when starting at/beyond EOF, normalizing provider unsatisfiable-range
responses without hiding missing objects or configuration errors. Freeze that
choice and verify empty objects, exact EOF, beyond EOF, oversized lengths, source
read errors and cancellation across all adapters before implementation is complete.

Specify that reads return stored bytes. Pinned GCS storage@v1.61.3/reader.go:102–106
warns that decompressive transcoding can ignore a requested range. Use the concrete
ObjectHandle.ReadCompressed(true) setting for consistent Download/range/size byte
meaning. This is source-confirmed, not a cloud-reproduced result; add an encoded
object case to adapter verification.

### F7 — Signed reads and GCS session setup need honest timing/HTTP contracts

**Medium/high; local signing and intercepted HTTP/IAM requests reproduced.**
Both adapters mint URLs with an already canceled context. GCS ignores ctx in
SignedURL (gcs.go:287–299); the pinned library's keyless default signer uses
context.Background (storage@v1.61.3/bucket.go:323 onward), so IAM signing may also
outlive the caller. Resumable setup adds its timeout only after that signing work.
The final probe used explicit synthetic auth.Credentials and a fake TokenProvider,
with every request intercepted: canceled SignedURL and session initialization both
made IAM SignBlob requests with uncanceled, deadline-free contexts. No real IAM or
credential discovery was used. An earlier incomplete credential fixture did not
reach IAM; only the corrected final probe supports this conclusion.

Expiry is inconsistent: S3 zero means its 900-second default, subsecond values
produce zero-second signatures and >7 days can produce a URL; GCS accepts expired
negative/zero values but rejects >7 days. Set a shared explicit valid range and
whole-second policy (recommend 1 second through 7 days, rejecting non-whole seconds),
with pre-canceled and post-operation checks. Document that signing can require
credential or IAM I/O, and expiry cannot outlive signing credentials/provider policy.

GCS InitiateResumableUpload uses http.DefaultClient rather than its configured
client and provides no Origin seam (gcs.go:243–283). A fake transport confirmed both.
[Google's XML session documentation](https://docs.cloud.google.com/storage/docs/resumable-uploads)
requires initiation Origin for cross-origin browser resume responses; browser CORS
itself was not exercised.
Keep browser direct uploads as intended functionality: give the host a narrow
content-type/origin options input and an explicitly configured HTTP client; validate
origin policy at the host. A context-aware signing path is required for IAM rather
than merely checking ctx around an unbounded background signer. Freeze that concrete
GCS design before edits. Prefer authenticated API session initiation through the
retained configured client, eliminating the unnecessary presigning step. For signed
reads, retain local private-key signing and use explicit signing identity plus a
context-aware IAM SignBytes closure; evaluate a host signer seam only if necessary.
Do not simulate cancellation by abandoning a goroutine while its RPC keeps running.
Sessions/signatures are bearer capabilities; never expose
raw signed URLs/session URIs in automatic operation-error logs.

### F8 — FileStore's delegation/logging facade can be removed

**Simplification recommendation, with real migration cost.** filestorer.go:66–212
adds wrapping, optional logger policy and dynamic optional calls. It owns no upload
policy, stable key namespace or other operation that callers cannot express directly.
Wrapping Disk structurally advertises SignedURLer and ResumableUploader even though
both return unsupported. This is the facade's documented fallback design, not a
new crash, but it weakens the otherwise useful optional-interface story.

Use Storer (or a consumer's narrow port) directly and assert optional capabilities
on the real adapter. Remove FileStore/New/Option/WithLogger and unsupported-fallback
helpers/sentinels if no caller needs them. Host/consumer code logs returned errors;
avoid replacing the facade with another logging decorator or callbacks on every
method. The CMS BlobStore port already accepts Disk directly. Segovia/GPS360 field
and constructor types change mechanically; preserve GCS client closing and caller
error reporting. No extra namespace/cache/retry facade is justified by inspected
usage. A future service should earn its place through concrete shared behavior.

## Focused implementation proposal

1. Freeze shared keys, literal prefixes, ranges, cancellation, overwrite, reader
   ownership and root-error behavior. Strengthen the shared conformance suite.
2. Fix Disk confinement and atomic publication; GCS abort-on-copy-error; S3
   arbitrary-reader uploads and explicit missing-bucket errors. Test real I/O.
3. Remove the delegation facade and operation-only errors; migrate framework hosts
   and narrow consumer docs, without changing app-owned policies or external repos.
4. Correct optional capabilities: preserve GCS resumability and signed reads;
   explicitly name S3 multipart initiation; finish GCS context/origin/client design.
5. Add standalone AUDIT-013/RELEASING only for the implemented result, including
   path-acceptance changes and deployed-key inventory, wrapper removal, logging
   ownership, range changes, optional protocols, Disk resources and caller checks.

Do not silently migrate stored keys. Existing relative, clean consumer keys should
keep their spelling. A bucket can contain arbitrary legacy keys outside a portable
Disk-compatible contract; hosts inventory and deliberately address those before
upgrading. Physical filesystem constraints (case collisions and file/directory
prefix conflicts) remain limits of Disk, not reasons to silently rename objects.

## Later pocket/integration work, not SDK scope

- CMS mediasvc/service.go:44–47 uploads bytes before metadata creation; failed
  metadata writes can orphan objects. :80–83 deletes bytes before deleting metadata.
  Define cleanup/reconciliation at the pocket/host workflow boundary, not a fake
  cross-storage transaction in SDK.
- CMS inbound/cms/media.go:17, :57 uses ParseMultipartForm(maxUploadBytes), whose
  argument is a memory threshold, not a total request-size cap. The example CMS
  global middleware has no body limit. Verify/repair the intended 32 MiB ceiling
  and multipart cleanup during the full media/pocket audit.
- GCS/S3 credential configuration, content metadata, object retention/versioning,
  copy/stat/multipart breadth and cloud deployment posture deserve adapter follow-up
  when required. Preserve Coordination Hub's app-owned headers/bucket/CDN policy.
  In particular, S3 s3.go:112 currently falls back to ambient credentials when just
  one static credential field is supplied; reject incomplete pairs during adapter
  cleanup. This was source-reviewed, not a credential-discovery runtime probe.

## Verification and next step

Passed with GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache:

| Scope / working directory | Commands and evidence |
|---|---|
| sdk | `go build ./capabilities/filestorage/...`, `go test ./capabilities/filestorage/...`, `go vet ./capabilities/filestorage/...`, `go test -race ./capabilities/filestorage/...` |
| integrations/filestorage/gcs | `go build ./...`, `go test -v ./...`, `go vet ./...`; hermetic tests passed, live test skipped because GCS_TEST_BUCKET was unset |
| integrations/filestorage/s3 | `go build ./...`, `go test -v ./...`, `go vet ./...`; hermetic tests passed, live test skipped because S3_TEST_ENDPOINT was unset |
| repository root | `make guard`: all 23 guards passed; /tmp/gopernicus-filestorage-guard.log |
| sdk, owned temporary filesystem | `go run /tmp/gopernicus-filestorage-disk-probe.go` and `go run -race /tmp/gopernicus-filestorage-disk-probe.go`; logs with matching .log / -race.log names |
| integrations/filestorage/s3, fake HTTP only | `go run /tmp/gopernicus-filestorage-provider-probe.go` and `go run -race /tmp/gopernicus-filestorage-provider-probe.go`; final evidence /tmp/gopernicus-filestorage-provider-probe.log and /tmp/gopernicus-filestorage-provider-probe-race-final.log |

The temporary probes demonstrate the current defects; they are not future
conformance tests and may require revision after implementation. Disk checks used
real file operations in owned temporary trees. Provider probes used synthetic
credentials/signing keys and RoundTrippers intercepting all requests. They prove
request construction, source-failure finalization attempts and error handling,
not real cloud persistence, permissions, compatible-server acceptance or browser
CORS. No services were started. No unresolved verification failure remains.

Full 42-module `make check`, docs-build and live services were not repeated for this
review-only Markdown change. Earlier events/workspace results remain historical
evidence in their own plan, not filestorage implementation verification. No Go
formatting is needed. The final `git diff --check` and baseline inventory check cover
this review's two repository paths: plans/framework-audit-filestorage.md (new) and
plans/framework-audit.md (updated). Source, dependencies, generated files, consumers,
RELEASING.md and AUDIT.md are unchanged relative to this review's baseline.

Next: agree on the recommendations, then write the focused implementation plan
under plans/ before source edits. Keep the seven operations and optional capability
intent; remove the facade for lack of shared behavior, not lack of usage alone.
Freeze portable keys/ranges and the concrete signing/upload choices first. Once
implemented, record actual breaking changes as AUDIT-013 and complete the required
workspace and real-I/O verification. Continue with email/notify/oauth/tracing and
S10 pocket wiring afterward.
