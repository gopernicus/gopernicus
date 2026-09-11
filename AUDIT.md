# Framework audit: consumer migration guide

This file records implemented breaking changes from the framework audit started
on 2026-09-09. It is intended to be copied into a consumer application's working
context when that application is ready to upgrade. Each entry contains the
information needed to migrate without the original audit conversation.

Keep this file current as SDK, pocket, and integration changes land. Record the
affected module, release status, old/new behavior, required migration, and
verification for every change to an API, accepted input, response, configuration,
or persisted data that consumers must account for. Keep entries after release
and add the actual module version when known. Proposed changes stay in the
[audit plan](plans/framework-audit.md) until implemented.

## Coordinated release versions (publication pending)

These versions collect the implemented audit changes below. Entries retain their
original implementation-time release status; this table records the coordinated
release targets. Publication and verification are tracked in
[plans/startup-release-segovia.md](plans/startup-release-segovia.md).

| Module directory | Version |
|---|---|
| `integrations/scheduling/robfig-cron` | `v0.2.0` |
| `sdk` | `v0.9.0` |
| `ui/goth` | `v0.2.0` |
| `integrations/cryptids/bcrypt` | `v0.2.0` |
| `integrations/cryptids/golang-jwt` | `v0.2.0` |
| `integrations/cryptids/google-uuid` | `v0.2.0` |
| `integrations/datastores/pgxdb` | `v0.7.0` |
| `integrations/datastores/turso` | `v0.4.0` |
| `integrations/email/sendgrid` | `v0.3.0` |
| `integrations/filestorage/gcs` | `v0.2.0` |
| `integrations/filestorage/s3` | `v0.2.0` |
| `integrations/kvstores/goredis` | `v0.2.0` |
| `integrations/oauth/github` | `v0.2.0` |
| `integrations/oauth/google` | `v0.2.0` |
| `integrations/tracing/otel` | `v0.2.0` |
| `pockets` | `v0.1.0` |
| `pockets/authentication` | `v0.11.0` |
| `pockets/authorization` | `v0.13.0` |
| `pockets/cms` | `v0.3.0` |
| `pockets/events` | `v0.3.0` |
| `pockets/jobs` | `v0.6.0` |
| `pockets/authentication/stores/pgx` | `v0.6.0` |
| `pockets/authentication/stores/turso` | `v0.5.0` |
| `pockets/authentication/views/goth` | `v0.4.0` |
| `pockets/authorization/stores/pgx` | `v0.8.0` |
| `pockets/authorization/stores/turso` | `v0.7.0` |
| `pockets/cms/stores/pgx` | `v0.4.0` |
| `pockets/cms/stores/turso` | `v0.3.0` |
| `pockets/cms/views/goth` | `v0.3.0` |
| `pockets/events/stores/pgx` | `v0.4.0` |
| `pockets/events/stores/turso` | `v0.3.0` |
| `pockets/jobs/stores/pgx` | `v0.6.0` |
| `pockets/jobs/stores/turso` | `v0.5.0` |
| `workshop/gopernicus` | `v0.3.0` |

The three Firestore modules are excluded pending reconciliation with newer
remote work and their required live verification. CMS versions provide framework
compatibility; its behavioral audit remains deferred. Examples are not tagged.

For SQL hosts, review the authentication `0018`, authorization `0006`/`0007`,
jobs `0004` and events `0002` migrations alongside the relevant entries. Export
new files into host ledgers; do not rewrite previously applied migrations.

## AUDIT-001: Validation consolidation

- **Implemented:** 2026-09-09.
- **Module:** `github.com/gopernicus/gopernicus/sdk`.
- **Release:** unreleased; target SDK version has not been selected.
- **Impact:** breaking Go APIs, validation acceptance, and field-error handling.
- **Data migration:** none.

### Why this changed

Request and domain validation used three collectors with different error
classification and HTTP handling. They now use `sdk.ValidationError`, preserving
field names, messages, optional codes, and `errors.Is`/`errors.As` through
ordinary wrapping. Helper functions produce typed field data for that collector.

### Find affected consumer code

Search application code and tests for imports of `sdk/foundation/validation`
and `sdk/foundation/web`, including import aliases. In particular, inspect:

- `validation.Errors`, `web.FieldErrors`, their methods and type assertions.
- Validation helpers used as ordinary errors, including direct returns,
  callback types, custom wrappers, and `IfSet`.
- `PasswordsMatch`, `PasswordStrength`, and string length checks.
- `web.DecodeJSON` and its `web.Decode` alias, especially pointer targets and
  endpoints accepting a top-level JSON `null`.
- Response tests or frontend code that inspect field lists and error codes.

Known affected call site: gps-360-go's
`internal/inbound/domains/echo/notes.go` uses `web.FieldErrors` in
`NoteRequestBody.Validate`. Its domain validation already uses the root types.
No production imports of the validation helper package were found in sampled
Segovia v2, coordination-hub, or gps-360-go code during the audit. Check current
consumer code; this sample is not a complete inventory of downstream callers.

### Migrate the Go API

| Previous usage | Replacement |
|---|---|
| `var problems validation.Errors` or `var problems web.FieldErrors` | `var problems sdk.ValidationError` |
| `problems.Add(validation.Required(...))` | `problems.AddViolation(validation.Required(...))` |
| `fields.AddErr("name", validation.Required("name", value))` | `problems.AddViolation(validation.Required("name", value))`; the helper owns the field name |
| `fields.Add(field, message)` | `problems.Add(field, "", message)` to retain sentence-only field JSON |
| A helper result stored or returned as `error` | Collect its `*sdk.Violation` result, then return `problems.Err()` |
| `IfSet(value, func(T) error { ... })` | Callback returns `*sdk.Violation` |
| `PasswordsMatch(password, confirm)` | `PasswordsMatch("confirm_password", password, confirm)`; use your application's actual field name |
| `PasswordStrength(field, password)` | Use an application-owned or existing authentication password policy; preserve the intended checks when removing this call |
| Inspecting/ranging over a collector slice | Inspect `problems.Violations`; recover `*sdk.ValidationError` with `errors.As` |

Example of the new pattern, with the relevant imports:

```go
import (
    "github.com/gopernicus/gopernicus/sdk"
    "github.com/gopernicus/gopernicus/sdk/foundation/validation"
)

type createUser struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func (in *createUser) Validate() error {
    var problems sdk.ValidationError
    problems.AddViolation(validation.Required("name", in.Name))
    problems.AddViolation(validation.Email("email", in.Email))
    return problems.Err()
}
```

`AddViolation` skips nil and copies a non-nil violation. `Err()` returns nil
when nothing was collected. `Validate() error`, `sdk.ValidationError.Add`, and
`sdk.Refuse` remain available. Custom field checks can return `*sdk.Violation`
or add a rule directly with `problems.Add(field, code, message)`.

`sdk.Violation` does not implement `error`. Keep unexpected dependency failures
as ordinary errors; do not turn `err.Error()` into public validation messages.
If the removed `validation.Errors` collector was used for unrelated failures,
keep that error handling separate from field validation. Aggregate field
problems in one collector before wrapping with `%w`; `errors.Join` does not
merge separate collectors' field lists for HTTP rendering.

### Review behavior changes even if the application compiles

| Area | Previous behavior | New behavior and migration |
|---|---|---|
| String length | `MinLength`/`MaxLength` and pointer variants counted UTF-8 bytes | They count Unicode code points. `"é"` has length 1. Combining marks count separately; text is not normalized. Review non-ASCII boundaries and use an explicit byte check for storage limits that require bytes. |
| Optional empty strings | `MinLengthPtr` rejected a present empty string while `MinLength` accepted it | Both accept it. Use `Required`/`RequiredPtr` when blank input must fail. Pointer helpers skip nil, then apply the scalar rule. |
| Pointer JSON targets | `DecodeJSON[*Request]` could skip `Validate` | Both value and pointer request targets validate once when the method exists. Previously accepted invalid pointer requests now fail validation. |
| JSON null | Top-level `null` could produce a zero value or nil pointer | `DecodeJSON` and `Decode` reject it for every target, including maps, slices, and unvalidated pointers. Represent optional null data inside request fields, or use an explicit decoder when the endpoint intentionally accepts top-level null. Nested nulls remain supported. |
| Error classification | The removed collectors did not match `sdk.ErrInvalidInput`; the generic domain mapper could return 500 | `sdk.ValidationError` matches `sdk.ErrInvalidInput`, and both `web.ErrValidation` and `web.ErrFromDomain` return structured 400 responses for populated collectors. Review code branching on these errors. |
| Field codes | `web.FieldErrors` carried only field names and messages | Helpers set `required` for presence checks and `invalid_format` for other checks. These codes appear in JSON. To preserve existing sentence-only responses, use `Add(field, "", message)` with the original message. |

`web.FieldError` remains the wire representation. Field order and the
`validation_failed` response envelope remain available. A collector with empty
field codes produces the same sentence-only field JSON as the old web collector.
The generic `Email` helper still accepts mail display-name syntax; it has not
become an authentication identifier validator. Removing `PasswordStrength`
does not change the authentication pocket's own password policy.

### Verify a consumer upgrade

1. Confirm the SDK version actually resolved by the application, accounting
   for `go.work`, local `replace` directives, and vendoring. Choose the released
   version containing this change once available; this entry does not name one.
2. Migrate affected call sites and error handling. Preserve application-specific
   field names, public messages, and password rules. Update expected codes only
   where the application adopts them.
3. Run the application's documented formatter, build, tests, and vet. For each
   applicable Go module, the baseline is `go build ./...`, `go test ./...`, and
   `go vet ./...`.
4. Exercise affected endpoints with valid input, multiple invalid fields,
   non-ASCII length boundaries, omitted/empty optional fields, and top-level
   null. Check status codes and the actual JSON or rendered form field errors.

Framework verification passed: SDK build/test/vet, the full 41-module
`make check`, all 23 architecture guards, documentation build, and an in-process
JSON request/response check covering both decoder target forms and multiple
field errors. Consumer applications were not migrated or run; live datastore
tests were not configured. Implementation record:
[plans/validation-consolidation.md](plans/validation-consolidation.md).

## AUDIT-002: Conversion removal and pointer package

- **Implemented:** 2026-09-09.
- **Module:** `github.com/gopernicus/gopernicus/sdk`.
- **Release:** unreleased; target SDK version has not been selected.
- **Impact:** the entire `sdk/foundation/conversion` import path is removed;
  its two pointer readers now live in root `sdk` (final destination after AUDIT-009).
- **Data migration:** none supplied or required by the framework change.
  Consumer replacements must account for existing generated identifiers,
  accepted date formats, and JSON representations.

### Why this changed

`conversion` mixed pointer access, identifier naming, date format guessing,
JSON defaults, and slice filtering. No runtime users of the package were found
in the framework or the three sampled consumer apps. The apps do repeat
nil-pointer reading, so that shared operation remains as sdk.Deref/DerefOr.
The other APIs are removed instead of maintaining speculative functionality
and unclear policies. Slug output is unchanged; its final name is sdk.Slugify
(AUDIT-009).

### Find affected consumer code

Search source, tests, templates, and generated-code inputs for the import path
`github.com/gopernicus/gopernicus/sdk/foundation/conversion`, including aliases.
Check function references and callback types as well as direct calls. No
compatibility package or aliases remain at the old path.

| Removed API | Consumer migration |
|---|---|
| `Deref`, `DerefOr` in `conversion` | Import root `sdk` and call `sdk.Deref` / `sdk.DerefOr` |
| `Ptr` | Use Go 1.26's `new(value)`; preserve explicit types and copy semantics as described below |
| `ToPascalCase`, `ToCamelCase`, `ToSnakeCase`, `ToKebabCase`, `ToLowerSpaced` | Keep only needed naming behavior in the application, with explicit input/output examples; there is no replacement SDK naming package |
| `Caser`, `CaserOption`, `NewCaser`, `WithAcronyms` | Remove the configuration machinery with its call sites, or implement the specific naming rules the application needs locally |
| `ParseDateTime`, `ParseFlexibleDate` | Choose explicit `time.Parse` layouts or an application-owned parser that preserves the intended format/timezone policy |
| `JSONOrEmptyObject`, `JSONOrEmpty` | Handle missing JSON and object requirements explicitly in the consuming model; see the old behavior below |
| `Overlap` | Use application-local filtering; preserve requested order, duplicate matches, and nil-empty behavior where significant |

### Move pointer readers

```go
import "github.com/gopernicus/gopernicus/sdk"

func displayName(name *string) string {
    return sdk.DerefOr(name, "Anonymous")
}
```

`sdk.Deref(p)` returns the pointed-to value, or the zero value of its type
if p is nil. `sdk.DerefOr(p, fallback)` returns fallback only for nil.
Present zero values, including `""`, `0`, and `false`, are kept. These are the
same implementations previously exported from `conversion`.

For pointer construction, the module already requires Go 1.26.1, which supports
expression operands to `new` ([Go release notes](https://go.dev/doc/go1.26#language)).

| Before | After |
|---|---|
| `conversion.Ptr(value)` | `new(value)` |
| `conversion.Ptr("name")` | `new("name")` |
| `conversion.Ptr[int64](1)` | `new(int64(1))` |
| `conversion.Ptr[*Thing](nil)` | `new((*Thing)(nil))` |

`new(value)` creates a pointer to a copy of the value, matching `Ptr`. Replacing
`Ptr(existingVariable)` with `&existingVariable` would instead alias the original
variable. Preserve explicit type conversions: `new(1)` has type `*int`, not
`*int64`. There is no new SDK constructor to import.

### Preserve consumer behavior while removing other helpers

**Naming:** characterize the outputs the application relies on, particularly
acronyms, digits, Unicode, and separators. The old case helpers had defects:
`ToCamelCase("api_id")` returned `"apiid"`, and accented input could produce
invalid UTF-8. Do not treat those defects as desired naming rules, but account
for existing generated keys/routes before changing results. URL slugging is a
different operation from identifier case conversion; `sdk.Slugify` is not a
drop-in replacement for these functions.

**Dates:** the old `ParseDateTime` tried RFC3339/RFC3339Nano, date-only
`2006-01-02`, and zone-free `2006-01-02 15:04:05` and
`2006-01-02T15:04:05`. The zone-free forms became UTC.
`ParseFlexibleDate` tried RFC3339/RFC3339Nano, date-only, the space-separated
zone-free form, `2006/01/02`, then month/day/year formats before day/month/year
formats (slashes or hyphens; four- or two-digit years). It did not accept the
zone-free `T` form. Thus `01/02/2024` meant January 2. An explicit layout such
as `time.Parse(time.DateOnly, input)` is appropriate only when that is the
application's intended contract; it does not preserve all old accepted inputs.

**JSON:** `JSONOrEmptyObject` defaulted nil pointers, zero-length data, and the
exact bytes `null` to `{}`. Other data passed through, including arrays, scalars,
whitespace-padded null, and malformed JSON. Despite its old comment, it did not
guarantee a valid object. `JSONOrEmpty` defaulted only nil pointers to `{}` and
otherwise returned the pointed-to bytes. Preserve the intended distinctions
between missing data, `null`, objects, and arrays, and return validation errors
when an object is required. Do not silently turn corrupt JSON into `{}`.

**Overlap:** the old helper returned matching items in requested order, including
repeated requested entries. For requested `["b", "a", "b"]` and allowed
`["a", "b"]`, the result was `["b", "a", "b"]`. No matches produced a nil
slice. Sorting, deduplicating, or replacing nil with an empty slice can alter
consumer behavior or JSON output.

### Slug compatibility

The accent table and output are unchanged. The function now lives at
`sdk.Slugify`; AUDIT-009 records its move from `sdk/foundation/slug.Make`.
It still produces ASCII slugs with limited accent folding, no
Unicode normalization, and potentially empty results. No stored slugs are
rewritten by this SDK update.

The documentation now correctly notes that regenerating a stored slug after
an algorithm change can change its URL. CMS currently regenerates entry/term
slugs on edits and menu slugs on renames, and derives content-type route bases
from plural names at runtime. Those existing behaviors are unchanged; their
URL-lifecycle decisions remain part of the later CMS audit.

### Verify a consumer upgrade

1. Confirm the resolved SDK version, local replacements/workspace, vendoring,
   and Go toolchain. Check both AUDIT-001 and AUDIT-002 when upgrading from a
   version preceding these unreleased changes.
2. Migrate old imports and every referenced symbol. Use the new pointer readers
   only where needed; existing application-local helpers do not require a move.
3. Test nil pointers, present zero values, explicit fallbacks, and pointer-copy
   behavior. For removed helper families actually used by the app, test their
   expected naming outputs, accepted dates, JSON defaults, and slice behavior.
4. Run the application's formatter and per-module `go build ./...`,
   `go test ./...`, and `go vet ./...`. Exercise any affected response, import,
   or code-generation flow and inspect generated identifiers/URLs before
   accepting a behavior change.

Framework verification passed: fresh pointer/slug tests, SDK build/test/vet,
the full 41-module `make check`, all 23 guards, documentation build, and a
consumer-style probe of the new import, JSON defaults, typed constructors, and
copy semantics. The slug implementation/table were compared against the baseline
and are unchanged. Exact results:
[plans/utility-package-cleanup.md](plans/utility-package-cleanup.md).
Consumer applications have not been migrated or run; live datastore tests were
not configured. No release tag or dependency pin has been changed.

## AUDIT-003: Environment parsing and context logging

- **Implemented:** 2026-09-09.
- **Module:** `github.com/gopernicus/gopernicus/sdk`; examples and Workshop templates also updated.
- **Release:** unreleased; target SDK/Workshop versions have not been selected.
- **Impact:** removed/renamed Go APIs, stricter configuration parsing, changed parse diagnostics, and automatic context fields in logs.
- **Data migration:** none. Review dotenv files, configuration structs, and log-processing expectations before upgrading.

### Why this changed

Dotenv quotes and trailing comments could corrupt values; environment write
errors were ignored. Tagged parsing could panic for named string slices or
silently overflow float32 fields. Its errors retained rejected values. Logging
modified shared records and required a separate option to include the request
ID already present in host middleware. The new surface fixes these cases and
removes redundant helpers.

### Find affected consumer code

Search Go code/tests for imports of `sdk/foundation/environment` and
`sdk/foundation/logging`, including aliases. Inspect calls to the symbols below,
config structs with `env` tags, ignored `LoadEnv`/`LoadPath` errors, startup logger
construction, and tests/parsers asserting exact log or error text. Check dotenv
syntax without copying real secrets into logs or diagnostic reports.

### Migrate the Go API

| Previous usage | Replacement |
|---|---|
| `logging.New(opts, logging.WithTracing())` | `logging.New(opts)`; context IDs are automatic |
| `logging.Option`, option slices, `logging.WithTracing` | Remove the optional configuration path; `New` accepts only `Options` |
| `logging.TracingHandler`, `logging.NewTracingHandler(inner)` | `logging.ContextHandler`, `logging.NewContextHandler(inner)` |
| `logging.NewDefault()` or `logging.NewDefault(logging.WithTracing())` | `logging.New(logging.Options{})` |
| `logging.NewNoop()` | `slog.New(slog.DiscardHandler)` using standard `log/slog` |
| `environment.GetNamespaceEnvValue(ns, key)` | `os.Getenv(environment.GetNamespaceEnvKey(ns, key))` |
| `environment.GetNamespaceEnvOrDefault(ns, key, fallback)` | `environment.GetEnvOrDefault(environment.GetNamespaceEnvKey(ns, key), fallback)` |

`logging.New` still returns `*slog.Logger`, without an error return. Its
`Options` fields and env tags, `ParseLevel`, default level/format/destination,
and case-insensitive parsing are unchanged. Unknown values still fall back to
INFO/JSON/STDERR. `GetEnvOrDefault`, `GetNamespaceEnvKey`, Mode, and secret APIs
remain available. No compatibility aliases preserve the removed symbols.

The standard discard handler disables every level. The old no-op helper
formatted enabled messages and resolved `LogValuer` values before discarding
bytes; the replacement does neither. Ordinary Go argument expressions still
evaluate. If tests depended on enabled logging or `LogValuer` side effects,
choose a test handler that explicitly models that behavior.

### Dotenv acceptance and error handling

`LoadEnv` reads `.env`; `LoadPath` reads the chosen path. Missing files still
succeed, and an already-set process variable still wins, even when empty.
Loading remains incremental: assignments before an error remain applied.

The supported format is single-line `KEY=value`, with optional `export ` prefix,
blank lines, comments, and literal values. Keys must be nonempty and contain no
whitespace or NUL. An unquoted space/tab followed by `#` starts a comment;
leading hashes such as `COLOR=#ff0000` remain values. Surrounding single/double
quotes preserve spaces and hashes. A quoted value can have a whitespace-separated
trailing comment. There is no escape processing, interpolation, or multiline
syntax; backslashes and dollar signs are literal.

```dotenv
# Both now load value # inside, without quotes or truncation.
LABEL="value # inside" # explanatory comment
OTHER_LABEL='value # inside' # explanatory comment
```

Malformed lines without `=`, invalid keys, unterminated quotes, and unexpected
text after a closing quote now return errors. Failed environment writes also
return errors. Diagnostics include filename/line and the key when available,
without embedding the raw value. A quoted value immediately followed by `#`
without separating whitespace is rejected; write `KEY="value" # comment`.
Correct malformed files instead of suppressing the error.

Replace ignored loader calls with checked startup handling. Create the
configured logger after loading dotenv and before constructing other components:

```go
func main() {
    if err := environment.LoadEnv(); err != nil {
        slog.Error("load environment", "error", err)
        os.Exit(1)
    }
    var opts logging.Options
    if err := environment.ParseEnvTags("", &opts); err != nil {
        slog.Error("configure logging", "error", err)
        os.Exit(1)
    }
    log := logging.New(opts)
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    if err := run(ctx, log); err != nil {
        log.Error("server exited with error", "error", err)
        os.Exit(1)
    }
}
```

This pattern uses standard `context`, `log/slog`, `os`, `os/signal`, and `syscall`
plus the two SDK packages. Keep the application's own logging defaults in the
Options literal when migrating. Pass `log` explicitly into existing host wiring;
SDK logger construction does not change slog's global default. Bootstrap errors
before logger configuration use the bootstrap logger. Examples and emitted
migration runners now also honor logging env options; migration runners retain
their text/STDOUT defaults when those options are absent. The goth-showcase
example now loads `.env` before reading configuration; previously it read only
process environment values.

### Tagged configuration changes

- Tagged unsupported kinds now fail even when their env variable is absent,
  empty, or the field was pre-seeded. Remove inappropriate env tags from
  collaborators or read unsupported configuration explicitly. Supported kinds
  remain string, int, int64 (including `time.Duration`), bool, float32/float64,
  and slices of strings; named string element/slice types now work safely.
- Integer and float parsing use the destination width. Out-of-range float32
  input now returns a range error rather than becoming infinity; int parsing
  also respects platform width. Failed conversion leaves that field unchanged.
- Errors identify the full env key, nested field path, and type, and omit the
  rejected input throughout the error chain. Numeric syntax/range errors retain
  `errors.Is(err, strconv.ErrSyntax/ErrRange)`, but no longer expose an
  input-bearing `*strconv.NumError` through `errors.As`. Invalid durations
  report `invalid duration` without retaining the original duration error.
  Update exact error-text assertions and NumError inspection.

Precedence is unchanged: nonempty env > existing nonzero field > default tag.
Empty env counts as absent. A false/zero/empty-slice literal cannot override a
nonzero default tag; apply such programmatic overrides after parsing. A required
tag requires a nonempty env value even when the field was pre-seeded.
Untagged nested structs recurse under the same namespace; untagged collaborator
pointers/interfaces are skipped. Mode/enum and domain validation remain separate.
Config fields assigned before a later error remain changed.

### Log output changes

`logging.New` now includes available SDK `request_id`, `trace_id`, and `span_id`
values on context-aware calls without an extra option. It does not start traces,
choose exporters, or manufacture IDs. Calls without those IDs add no fields.
Middleware order must still put request-ID/tracing middleware before access
logging so the context reaches the logger. Use slog's context-aware methods.

Update log snapshots or strict log schemas that reject the additional fields.
IDs remain inside the active `WithGroup` group. Avoid manually appending the same
ID fields a second time. For custom output, wrap a standard handler with
`logging.NewContextHandler`; to intentionally omit automatic IDs, construct a
plain slog handler directly. `ContextHandler` clones records before enrichment,
so shared-record reuse no longer emits slog's `!BUG` attribute.

### Known callers and upgrade checks

The framework migrated the CMS example's `WithTracing`, SDK tracing handler
references, and eight CMS no-op test calls. `NewDefault` and the two removed
namespace lookup wrappers had no observed callers beyond definitions/tests.

Segovia v2, Coordination Hub, and GPS 360 construct logging without the former
option and install request-ID/access middleware: their access logs gain IDs on
upgrade without a constructor edit. All sampled hosts ignore `LoadEnv` errors;
apply checked startup handling. Segovia also reports `run` errors using the
global logger rather than its configured local logger. Coordination Hub's
`cmd/seed` and `cmd/ingest` construct logging before loading dotenv; reorder them.
These consumer repositories were inspected only and have not been migrated.

For each consumer, run its formatter/build/tests/vet after resolving SDK pins,
local replacements, and vendor state. Exercise a disposable malformed dotenv
fixture, a startup error after logger configuration, and an HTTP request whose
response `X-Request-ID` matches the access-log ID. Do not use production secrets
or services for these checks.

Framework verification passed: SDK build/test/vet; fresh environment/logging
race tests; targeted tracing/CMS/example tests; the 41-module workspace gate,
23 guards, integration-tag compile checks, scaffold checks, and generated drift
checks; documentation typecheck/build. Generated SDK-only and Turso hosts reject
malformed dummy dotenv before serving/opening a datastore; the SDK-only host
reports invalid `READ_TIMEOUT` as one JSON error on configured STDOUT. A compiled
migration probe verifies the new APIs and matches an HTTP response request ID
to its default access log. Live datastores, consumer applications, browser flows,
and 32-bit runtime behavior were not exercised. Exact commands and logs are in
[the implementation record](plans/environment-logging-cleanup.md).

## AUDIT-004: Identity, IDs, cryptography, and host password policy

- **Implemented:** 2026-09-09.
- **Modules:** SDK; authentication, authorization, and CMS consumers; cryptids
  integrations (bcrypt, golang-jwt, google-uuid); Workshop pocket templates;
  corresponding examples and store adapters; jobs memstore ordering correction.
- **Release:** unreleased; versions have not been selected. Upgrade the SDK and
  affected dependents together. Old SDK versions do not contain the new packages.
  Published integration module paths remain unchanged.
- **Impact:** breaking package/type/function removals and stricter JWT, ID,
  context, and bcrypt input handling. Host password validation is additive.
- **Data migration:** none for default IDs, SHA-256 digests, or AES-GCM ciphertext.
  Ordinary SDK-issued JWTs remain valid under the same effective key. Previously
  accepted invalid/non-expiring tokens require replacement.

### Migrate imports and APIs

ID generation moves out of `foundation/cryptids` into root SDK. The temporary
`foundation/id` destination was consolidated by AUDIT-009; use the final names
below directly.
Cryptographic contracts and helpers remain in `foundation/cryptids` ("cryptography
tidbits"). The temporary `cryptography` rename was reversed on 2026-09-10 at the
owner's request; these mappings reflect the final unreleased API. See AUDIT-008
if your checkout already adopted the temporary path.

| Previous API | Replacement |
|---|---|
| `cryptids.IDGenerator` | `sdk.IDGenerator` from root SDK |
| `cryptids.GenerateFunc`, `NewGenerator`, `NanoID`, `Alphabet`, `DefaultLength` | Root `sdk.IDGenerateFunc`, `NewIDGenerator`, `NanoID`, `DefaultIDAlphabet`, `DefaultIDLength` respectively |
| `cryptids.Database` | `sdk.DatabaseID`, now a function rather than a mutable function variable |
| `cryptids.Encrypter`, `AESGCM`, `NewAESGCM` | Remain in `sdk/foundation/cryptids` |
| `cryptids.JWTSigner` | Remains `cryptids.JWTSigner`; use the JWT integration for implementation |
| `cryptids.NewSHA256Hasher().Hash(value)` | `cryptids.SHA256(value)` |
| A stored `*cryptids.SHA256Hasher` | Remove the field/constructor wiring; call `cryptids.SHA256` directly |
| `cryptids.NewHS256(keyBytes)` | `golangjwt.New(string(keyBytes))` from `integrations/cryptids/golang-jwt` |
| `identity.ResolveAll(ctx, resolver, principals)` | An application-owned loop with the ordering/error behavior that caller needs |

`sdk.IDGenerator{}` retains the exact default alphabet and 21-character length.
`sdk.NewIDGenerator(googleuuid.V7())` and `sdk.NewIDGenerator(sdk.DatabaseID)` preserve
configured strategies. The generator's `Func`, `Generate`, and `MustGenerate`
behaviors remain. Reassigning `Database` globally is no longer supported; pass
an explicit `sdk.IDGenerateFunc` instead. Search import aliases as well as `cryptids.`.

`SHA256` retains 64-character lowercase hex output and rejects empty input with
`sdk.ErrInvalidInput`. No stored refresh/invitation/API-key digest needs rewriting.

AES still requires exactly 32 key bytes and stores raw unpadded base64url of
`12-byte nonce || ciphertext || 16-byte tag`. The standard library now manages
nonce generation/prefix handling. Existing envelopes decrypt, and old readers
can decrypt new envelopes. Empty input remains invalid; a decoded envelope
shorter than 12 bytes still wraps `sdk.ErrInvalidInput`. Document/observe the
random-nonce limit of 2^32 encryptions per key across all instances.

The SDK implementation/type `HS256`, `NewHS256`, and its errors are removed:
`ErrSecretTooShort`, `ErrEmptyToken`, `ErrMalformedToken`,
`ErrUnexpectedSigningMethod`, `ErrSignatureInvalid`, `ErrTokenExpired`, and
`ErrTokenUsedBeforeIssued`. The integration retains its own `ErrSecretTooShort`
and `ErrEmptyToken`; other verification errors wrap the underlying JWT-library
errors. Authentication continues treating verification failures as credential
denials. Review any application branches on the removed SDK error symbols.

### Preserve JWT key bytes when changing constructors

The golang-jwt constructor accepts a string and uses its bytes directly. It does
not decode hex or base64. Preserve the effective bytes used by the old signer:

```go
import (
    golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
    "github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// keyBytes is exactly the byte slice previously passed to cryptids.NewHS256.
var signer cryptids.JWTSigner
signer, err := golangjwt.New(string(keyBytes))
```

If the application previously passed `[]byte(environmentString)`, use
`golangjwt.New(environmentString)`. If it decoded that string first, retain the
decoding and convert the resulting bytes to a string. Changing decoding changes
the key and invalidates existing access tokens.

Known callers: Segovia v2 decodes hex before construction; Coordination Hub,
GPS360's server/devcred, and auth-cms use environment-string bytes directly.
GPS360's testauth harness also names the removed concrete signer and hasher.
All sampled hosts need import/constructor migration; no consumer apps were edited.

### Review JWT acceptance changes

`cryptids.JWTSigner` now specifies one expiring-token contract, implemented
by the golang-jwt integration:

- Signing copies custom claims, then sets `exp` from `expiresAt` and `iat` from
  the current time. Supplying `exp`/`iat` in the map no longer overrides them.
  The input map is not mutated.
- Verification requires a JSON claims object and numeric `exp`. Missing, null,
  string, or otherwise invalid expiration is rejected. `exp: 0` is rejected.
- Present `nbf` and `iat` must be numeric, including rejection of explicit null.
  Future not-before/issued-at constraints are enforced.
- Time tolerance is 60 seconds for expiration, not-before, and issued-at checks.
  This preserves the SDK's ordinary expiry/issued-at tolerance; the old integration
  previously had zero leeway and did not validate issued-at.
- NumericDates must fall within calendar years 0001–9999. Extreme JSON numbers
  are rejected; upstream float/time conversion could otherwise accept huge
  future `nbf`/`iat` values as past dates.
- Signature/algorithm validation and strict base64url decoding are retained.
  The old SDK accepted noncanonical signature padding bits and case-insensitive
  `ALG` headers; these now fail. `alg=none`, changed MACs, and wrong keys fail.
- Only HS256, HS384, and HS512 are supported. The selected method requires at
  least 32, 48, and 64 key bytes respectively. Existing HS384/512 callers with
  shorter keys must rotate keys/tokens. HS256 callers with valid keys do not.
- Uninitialized/nil integration signers return errors. The SDK zero-value signer
  that could sign with an empty key no longer exists.

The existing integration does not support RS256/ES256; earlier SDK/authentication
comments claiming otherwise were incorrect. Audience/issuer/application-required
claims still belong to the consumer's token profile. Hosts sharing keys across
applications must account for that profile when choosing their verifier.

### Host-controlled password validation and bcrypt limits

`authentication.Config.ValidatePassword func(context.Context, string) error`
is optional. Nil preserves the existing default: 15–64 Unicode code points and
at most 256 bytes. A callback replaces all those length gates, including the
byte cap; it can implement the host's length/complexity/input-size rules.

```go
cfg.ValidatePassword = func(ctx context.Context, password string) error {
    // Apply the application's own rules here. For an ordinary refusal:
    // return fmt.Errorf("password does not meet application policy: %w", sdk.ErrInvalidInput)
    return validateNewPassword(ctx, password)
}
```

The callback receives the request context. Its errors are preserved: return
`sdk.ValidationError` or wrap `sdk.ErrInvalidInput` for a policy refusal;
ordinary dependency failures retain server-error classification. Do not include
submitted passwords in errors/logs. The independent
`Config.CompromisedPasswordChecker` still runs after the selected validator
succeeds, using the host's existing `CompromisedPasswordFailOpen` setting.

The same policy applies to register, initial password set, password change, and
password reset. It does not revalidate existing passwords during login, token
issuance, step-up, or the current-password check when changing a password.
Tightening creation policy therefore does not lock out existing credentials.

`Config.Hasher` remains host-selected. `bcrypt.New(bcrypt.WithCost(n))` keeps
its cost configuration and existing fallback. Bcrypt's algorithm supports at
most **72 bytes**, regardless of host policy; both hashing and verification now
reject longer candidates. The limit is bytes, not Unicode characters. A stored
72-byte password previously accepted matching candidates with extra trailing
bytes; those candidates now fail. Existing hashes need no migration.

`bcrypt.ErrPasswordTooLong` now also wraps `sdk.ErrInvalidInput`; the integration
therefore gains an SDK requirement. A too-long new password becomes HTTP 400
instead of 500. Password login/current-password mismatches retain generic
credential denial (401). Choosing an algorithm with a different input limit is
still a host decision.

### Other tightened inputs

- Custom `sdk.NanoID` alphabets must be ASCII with unique characters. Previously,
  non-ASCII input could generate invalid UTF-8 and collapse distinct bytes during
  JSON encoding. Default and valid ASCII generators are unchanged.
- `sdk.PrincipalFromContext` now requires both `Type` and `ID`; absent/incomplete
  principals return `sdk.Principal{}, false`. Hosts constructing malformed
  principals must fix their write sites. Protected endpoints decide whether a
  principal is required; public endpoints can permit anonymous access.
- `sdk.IdentityInfo.Addresses` is display/identity information, not notification
  eligibility. Segovia's first-email recipient selection needs a later consumer
  review. Do not globally filter the resolver: GPS360 also uses projection
  addresses for legacy ownership scoping.

### Upgrade and verify

The workspace check also exposed an existing jobs memstore defect: equal or
reversed clock timestamps could select a superseded job as the latest generation.
Same-key insertions now receive strictly increasing timestamps under the existing
lock. No API/schema migration is required. Durable adapter timestamp precision
and generation ordering remain part of the later jobs audit.

Search application source/tests for old package imports and symbols, update
module requirements to the coordinated new releases when available, and run
build/test/vet. Keep entity-ID configuration separate from token/secret generation;
`sdk.DatabaseID` must never become a bearer-secret generator.

Verify an existing valid access token with the replacement signer under the
same key bytes; test token issuance/refresh and protected routes. Confirm
host password policy on all setting flows, old-credential login, and multibyte
bcrypt boundary handling. Decrypt an existing dummy/safe fixture envelope and
check any stored digest expectations. No production data rewrite is required.

Framework verification passed: the full 42-module build/test/vet and architecture
gate, documentation typecheck/build, legacy JWT/AES compatibility, host password
flow regressions, and the jobs ordering race test. A real local HTTP test covers
host-approved registration, token issuance, authenticated access, tampered-token
rejection, and password-policy/bcrypt failures. Live stores and consumer apps
were not exercised. Exact commands and limitations are recorded in
[identity-cryptography-cleanup.md](plans/identity-cryptography-cleanup.md).

## AUDIT-005: Listing vocabulary and transaction correctness

- **Implemented:** 2026-09-09.
- **Modules:** SDK; pgxdb, Turso and Firestore connectors; pocket APIs/stores,
  examples and Workshop templates that consume listing or transactions.
- **Release:** unreleased; coordinate SDK, dependent module and Workshop versions.
  No consumer applications or published tags were changed.
- **Impact:** breaking imports/names, embedded CMS query field, stricter cursors,
  SQL query composition and previous-page helper contract; transaction cleanup.
- **Data migration:** no schema migration. Existing well-formed scalar/time cursor
  formats remain supported, subject to the input corrections below.

### Migrate imports and public names

`foundation/crud` is removed. Its used read helpers now live in
`github.com/gopernicus/gopernicus/sdk/foundation/list`:

| Previous | Replacement |
|---|---|
| `crud.ListRequest` | `list.Request` |
| `crud.ListParams` | `list.Params` |
| `crud.ListQueryOptions` | `list.QueryOptions` |
| `crud.ParseListRequest` | `list.ParseRequest` |
| `crud.ParseListQuery` | `list.ParseQuery` |
| `crud.Page`, `Limits`, `Order`, `OrderField`, `SearchField`, `Cursor` | Same names in `list` |
| `NewOrder`, `ParseOrder`, `EncodeCursor`, `DecodeCursor`, `TrimPage`, `MarkPrevPage` | Same names in `list` |
| `MapPage`, `MapPageErr`, `Items`, `MapItems`, `MatchesSearch` and constants | Same names in `list` |
| `crud.Transactor` | `transaction.Transactor` from `sdk/capabilities/transaction` |
| `crud.ErrNotFound` | `sdk.ErrNotFound` |
| `Reader`, `Writer`, `CRUD` | Domain-owned narrow repository interfaces |
| `Field`, `Some`, `Overlay` | Domain-owned update inputs and presence/null policy |

`Transactor` retains `Transact(context.Context, func(context.Context) error) error`.
No adapter wrapper is needed for existing implementations. No new module or
third-party SDK dependency was introduced. Use an import alias such as `listing`
where a local variable already uses the name `list`.

CMS `content.EntryQuery` now embeds `list.Request`, so keyed literals use
`Request: list.Request{...}` and selectors use `query.Request`. Apply the same
change to any host structs embedding the old type, or explicitly name a field
if preserving a host-owned field name is important.

The generic repository and sparse-write APIs had no active uses in the framework
or three sampled consumers. GPS360's retired directory-write helper was the sole
`Field` reference; remove that orphan when upgrading if it is still unused.
Do not introduce a new generic PATCH package just to replace these names.
Presence, omission, null clearing and validation belong to the domain contract.

### Cursor and request acceptance

Malformed cursors now wrap `sdk.ErrInvalidInput` and reach HTTP 400 through the
usual SDK responder. Decode requires one complete object, no trailing JSON or
unknown fields, nonempty `order_field`/`pk`, an explicit `order_value`, and a
supported value/type-tag pairing. Objects, arrays, missing values, typed nulls
and unknown tags are rejected, including in stale-field tokens. A well-formed
token for another order field still resets to the first page.

Supported encoded values are nil, exact built-in string/bool/numeric types and
`time.Time`. Explicitly convert named scalar types (for example `int64(sequence)`)
before encoding; unsupported values no longer fall through to lossy untagged
encoding. Untagged legacy numbers are accepted only within ±(2^53−1); tagged
int64/uint64 values retain full range. `float32` is widened to float64 before
encoding, matching its database value and preventing a repeated boundary row.
Old float32 tokens retain their encoded boundary; restart pagination when moving
between versions if those tokens were used. Tokens remain padded base64url JSON,
unsigned and unencrypted; stores still enforce authorization independently.

Memory stores reject incorrectly typed timestamp/string positions. SQL keyset
helpers reject null order values and non-string case-folded values, including
when constructing a new cursor. SQL keyset queries require non-null order and
PK values throughout the matching population; use a non-null projected key or
offset mode for nullable ordering. Firestore retains intentional null positions.
Other scalar/column type compatibility belongs to the authored SQL projection
and `OrderValueOf`; the generic helper does not infer a database schema.

Parsers validate the resolved default strategy. Invalid defaults now fail unless
an explicit cursor/offset input selects a valid strategy. Blank query values
remain absent (`?cursor=` does not force cursor mode). Limits remain default 25,
maximum 100 unless a resource supplies its own policy. `ParseOrder` remains a
separate allow-list check; `ParseQuery` does not read the order key.

### Custom SQL lists and previous-page probes

SQL listing operates on the authored SELECT's projected output consistently,
including the first page and offset lists. Search and cursor conditions wrap the
SELECT rather than searching its text for WHERE and appending AND. This preserves
nested queries and OR filters, and avoids first/subsequent-page differences for
computed projections.

Project every search/order/PK field using an **unqualified output name**. For
example, select `t.created_at AS created_at` and use `Column: "created_at"`.
Inner table aliases cannot be referenced outside the SELECT. Update strict
store-local row structs/scanners for additional projected columns.
`OrderValueOf` must return that projected value and type. Fixed order expressions
also operate on projected fields, so project every field they reference.

Public `AddSearchClause` and pgxdb `ApplyCursorPagination` accept a complete
SELECT and wrap it. Call them before final ORDER BY/LIMIT/OFFSET; they preserve
existing argument ownership/order. Blank search remains a no-op. Existing host
wrapper/WHERE TRUE workarounds still work and do not require removal.

`MarkPrevPage` now expects an **inclusive** reverse probe containing up to
`limit+1` records at or before the incoming cursor, restored to normal order.
Any match establishes `HasPrev`. An extra predecessor supplies `PreviousCursor`;
without one, the previous page is first and its cursor stays empty. Update
custom probes together with the SDK: SQL uses <=/>= with a flipped sort,
Firestore uses StartAt, memory uses the inclusive comparison. Existing exclusive
`limit` probes produce incorrect previous links with this helper.

Raw primary-key tiebreakers remain present under case-folded ordering, including
when the order column is itself the PK. This prevents case variants being
skipped. Empty Turso offset pages now emit `items: []`, matching cursor pages.
Page JSON fields, opt-in count semantics and literal-search rules are unchanged.

### Transactions and participation

SQL `InTx` and `Transact` share cleanup for errors and panics. Commit uses the
Begin context. Turso rolls back failed manual COMMIT before releasing the
connection and discards physical connections on failed rollback or uncertain
BEGIN. Error chains preserve original and cleanup causes; use `errors.Is` and
`errors.As`. A callback error is unchanged when cleanup succeeds. Panics retain
their original value after cleanup; cleanup errors do not replace that panic.

Rollback receives an independent five-second context deadline. Actual completion
depends on driver cancellation/disposal behavior; it is not a wall-clock promise.
Cancellation racing a commit cannot undo a commit already accepted by the server.

Pass the callback context to participating repositories backed by the **same DB
instance**. Operations that open their own transaction/connection, ignore that
context, or use other datastores do not participate. Bundled `Transact` methods
reject nesting. The shared capability does not create a default/no-op transaction,
untyped global context handle or multi-database transaction.

A connector may retry callbacks (Firestore does; the SQL helpers do not). Reset
captured results at the start of each attempt. Send external effects only after
`Transact` returns nil; use a durable outbox when effects must survive a crash.
Firestore requires reads before writes and cannot observe pending writes.
Isolation and locking remain connector-specific. Pocket-owned atomic methods
remain the appropriate boundary where they already own a workflow.

### Consumer upgrade checks

Update SDK requirements and affected pocket/connector versions together. Search
source, tests, generators and local docs for the old package, qualified aliases,
embedded `ListRequest` fields, and removed APIs. Check all custom SQL projections
and reverse probes; changing imports alone does not complete this migration.

Run formatter/build/test/vet and exercise forward/backward navigation at limits
1 and 2 in both directions, tied/case-folded keys, OR/nested filters, search plus
count, malformed cursors and empty offset JSON. For transaction consumers, check
rollback on callback error/panic, failed commit and cancellation, and verify each
repository participates on the same instance. Run datastore conformance against
a disposable backend before upgrading production.

Framework verification passed: full 42-module build/test/vet and architecture
checks, documentation build, transaction race tests, real SQLite regressions and
CMS router behavior. Live PostgreSQL, hosted Turso and Firestore runtime suites
were not exercised; tagged sources were compiled. Exact commands and coverage
limits are in [listing-transaction-cleanup.md](plans/listing-transaction-cleanup.md).

## AUDIT-006: Web correctness and API cleanup

- **Implemented:** 2026-09-09.
- **Modules:** `github.com/gopernicus/gopernicus/sdk`, affected authentication,
  authorization and CMS pockets, and Workshop scaffold output.
- **Release:** unreleased; coordinated module versions have not been selected.
- **Impact:** removed Go APIs; stricter malformed/oversized input handling;
  corrected proxy, streaming, shutdown, static-serving and logging behavior.
- **Data migration:** none. Consumer applications and their pins were not edited.

### Why this changed

Web middleware could overwrite another route group's protection, lose errors
through response wrappers, cache failed renders, misreport status, or turn an
interrupted response into a completed 200. Shutdown left connections alive after
its deadline. JSON/SSE/static handling had additional edge-case failures. The
cleanup retains the stdlib HTTP toolkit while removing unused duplicate APIs and
an unreliable reflection-based OpenAPI generator.

### Find affected consumer code

Search source, tests, templates and documentation for the APIs in the table
below, honoring import aliases. Inspect custom response writers, panic/recovery
middleware, SSE clients, static mounts, log processing and JSON request limits.

Known production `web.WithLogging` callers: Segovia v2 `cmd/server/main.go`,
Coordination Hub `cmd/server/main.go` and `cmd/devserver/main.go`, GPS360
`cmd/server/main.go`, framework examples, and CMS's router constructor. The
framework scaffold is updated. No production callers of the removed OpenAPI,
StreamWriter, Decode alias or generic responders were found in those four trees;
this does not establish that other consumers have none. The three applications
have 48 direct DecodeJSON references plus GPS360's shared directory-write helper.

### Migrate the Go API

| Removed API | Replacement |
|---|---|
| `web.WithLogging`, `web.HandlerOption`, constructor options | `web.NewWebHandler()`; supply the logger explicitly to `web.Logger(log)` and `web.Panics(log)` in `Use` |
| `web.Decode[T]` | `web.DecodeJSON[T]` |
| `web.StreamWriter`, `web.NewStreamWriter`, `web.AcceptsStream` | `web.NewSSEStream` over a channel of `web.SSEEvent`; choose content negotiation in the host |
| `web.RespondText`, `web.RespondHTML`, `web.RespondRaw` | Set the intended Content-Type/status and use `io.WriteString` or `w.Write`; offer write errors to `web.RecordError` |
| `web.RespondStream` | Set Content-Type/status and use `io.Copy`; retain ownership of closing the reader and record transfer errors |
| `web.RespondFile` | Standard `http.ServeFileFS`/`http.FileServerFS` for suitable filesystems, or the retained `StaticFileServer` when its mount/cache/SPA policies are wanted; non-seekable files require attention to range support |
| `web.BuildOpenAPISpec`, `(*WebHandler).ServeOpenAPI`, `web.OpenAPIInfo`, `web.RouteSpec`, `web.SpecQueryParam`, `web.ParamSchema` | Serve a host-owned, checked OpenAPI document using an ordinary route; select optional generation tooling separately |

Logging configuration previously had no effect unless logging middleware was
also installed. The explicit replacement is:

```go
router := web.NewWebHandler()
router.Use(web.RequestID(), web.Logger(log), web.Panics(log))
```

If tracing is installed, keep it outside Logger so request logs receive the
traced context. The JSON/error responders, Param/QueryParam, groups/verb methods,
Renderer/Render/Template, NoStore/CORS, ServerConfig/Run and SSEStream remain.
ReadBody and its presence-aware getters remain separate from DTO decoding.

For host-owned OpenAPI JSON bytes loaded or embedded at startup:

```go
router.GET("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _, err := w.Write(openAPIDocument)
    web.RecordError(w, err)
})
```

The SDK no longer generates schemas, pagination envelopes, auth metadata or
path-parameter descriptions. The host's document/generator must describe its
actual JSON and routes; simply moving the old reflection implementation would
retain its defects.

### JSON acceptance and host policy

- ReadBody now rejects malformed trailing closing delimiters such as
  `{"title":"ok"}]` and `{"title":"ok"}}`, returning the existing invalid-input
  400. Its 1-MiB bound, exact keys, presence/null getters and error vocabulary stay.
- Both strict pocket readers now return 413 if their body limit is exceeded
  while reading trailing whitespace, rather than misclassifying it as 400.
- Authentication's older JSON reader has been removed. Registration, login,
  token issuance, refresh JSON fallback, verification, password forgot/reset/change,
  invitation acceptance/decline, OAuth completion and verification resend now
  use the pocket's existing strict reader and **1-MiB limit**. A second JSON
  value or other trailing content is 400; an oversized body is 413. These cases
  previously could be accepted or read without a bound. Successful responses,
  unknown-field rejection, null behavior, content-type dispatch and forms retain
  their existing contracts. Rejected registration bodies do not create users.
- `DecodeJSON` still accepts unknown fields, rejects top-level null, and invokes
  value/pointer validation exactly once. It introduces no global size limit or
  stricter field policy. GPS360 deliberately relies on unknown-key acceptance;
  do not substitute the strict pocket reader indiscriminately.

Hosts choose their own DTO/upload limits using the standard library:

```go
// apiBodyLimit is an int64 selected by the host configuration.
limitJSON := func(next http.Handler) http.Handler {
    return http.MaxBytesHandler(next, apiBodyLimit)
}
api := router.Group("/api", limitJSON)
api.POST("/widgets", createWidget)
```

DecodeJSON preserves `*http.MaxBytesError`; use `web.ErrValidation(err)` for the
413 mapping. `http.MaxBytesReader(w, r.Body, limit)` is also available within a
handler. Content-type enforcement and stricter DTO decoding remain explicit
transport choices. ReadBody's Date implementation is unchanged: DateOnly accepts
zero-padded YYYY-MM-DD; an earlier comment wrongly advertised unpadded dates.

### HTTP, errors and lifecycle changes

- Route groups copy middleware slices. Caller mutation, sibling creation and
  later route registration cannot replace a group's middleware.
- TrustProxies combines repeated X-Forwarded-For fields in received order before
  applying the host's trusted-hop count. Attribution can change for these
  requests. Hosts must still configure the actual hop count and trusted ingress.
- Panic recovery writes HTML 500 only before response commitment. Later panics
  abort the response instead of appending an error page. Intentional
  `http.ErrAbortHandler` passes through without a panic stack log. Hijacking
  transfers ownership to the handler; recovery never writes HTML afterward.
- Run allows ordinary graceful draining, then closes remaining connections when
  ShutdownTimeout expires and returns the shutdown error. Closing connections
  cancels their request contexts, including SSE. Handlers must honor cancellation;
  Run cannot wait for uncooperative goroutines or manage hijacked connections.
- RecordError walks transparent `Unwrap() http.ResponseWriter` wrappers until
  the first recorder. A custom recorder owns forwarding beneath it. SDK
  StatusRecorders retain the first non-nil cause and forward it; `Err()` exposes
  that cause. Later abort/write errors do not replace the original render error.
- JSON marshal/write failures are recorded even when the handler ignores the
  returned error. Internal details remain out of the client body; the existing
  empty 500 on marshal failure is unchanged. Failed renders/transfers are not
  cached. Caching remains opt-in for public cacheable routes.
- StatusRecorder correctly handles informational responses, implicit statuses
  from flushing, and connection hijacking through ResponseController. Use
  `http.NewResponseController(w)` for wrapped flush/hijack/deadline operations.

Access logs now include recorded failures even if the response began as 200,
using ERROR level. Aborts include `aborted: true`; hijacks include
`hijacked: true`. **Status 0 in these logs means no final status was observed**
(before abort or ownership transfer), not an HTTP status sent to a client.
Raw bytes written on a hijacked connection are not observable by the recorder.
Update log assertions/dashboards that assume every entry has a status >= 100,
that 200 always logs at INFO, or that the last recorded cause wins.

### SSE and static serving

SSE strings/bytes remain raw data and other values remain JSON encoded. CRLF/CR
are normalized to LF and every data line is framed, preserving a multiline
payload as one event. Event names containing CR/LF, and IDs containing CR/LF/NUL,
are rejected. Serialization/metadata errors are recorded and stop the stream
before that event is written; I/O can still interrupt a frame. Invalid events no
longer emit partial metadata or injected frames. Use a producer channel with
cancellation when migrating StreamWriter; the host/pocket owns its lifetime.
To send a JSON string literal, explicitly JSON-marshal it into event.Data bytes.

StaticFileServer now resolves URL.Path for direct handler mounting; AddRoutes
strips its mount prefix. Code that manually set PathValue("path") must provide
an appropriate URL.Path instead. In SPA mode both fallback and direct index.html
responses have no-store, taking precedence over asset-prefix caching. Missing
paths/directories can fall back; permission and other filesystem faults return
500. Read/seek/write/close failures reach the error recorder. Non-seekable files
remain streamed without ranges, and HEAD avoids copying their body.

MIME lookup now uses the stdlib/platform registry. Notable test/header changes:
CSS includes a UTF-8 charset; JavaScript uses the registered JS media type;
`.map` no longer has a hard-coded JSON override; platform-known extensions can
receive their registered type. Unknown extensions use application/octet-stream.
Review exact-header assertions and configure required custom MIME registrations
in the host at startup.

### Release and verification

Coordinate the SDK, CMS and Workshop versions: old CMS source/scaffolds using
WithLogging will not compile with the new SDK. Release the authentication and
authorization body fixes deliberately and update their SDK requirements as needed.
No module tags, pins, database schemas or consumer apps were changed here.

Before adopting in a consumer, run its formatter, build, tests and vet; exercise
its middleware order, proxy setup, representative JSON writes, static files,
SSE and shutdown. Inspect log consumers and any maintained OpenAPI document.
Framework validation passed the full 42-module build/test/vet and guard gate,
web/cache/tracing race checks, documentation build, affected pocket suites and
the local HTTP composition/migration proof. Consumer upgrades, browser/HTTP2
sessions and live datastores were not exercised. Exact commands and coverage
limits are in [web-cleanup.md](plans/web-cleanup.md); framework tests do not
replace consumer-specific upgrade verification.

## AUDIT-007: Workers, job middleware and async lifecycle

- **Implemented:** 2026-09-10.
- **Modules:** SDK, jobs, jobs stores/pgx and stores/turso.
- **Release:** unreleased; coordinated target versions have not been selected.
- **Impact:** breaking SDK APIs, error/shutdown behavior, a heartbeat field,
  and empty-key acceptance in the consumer work protocol.
- **Data migration:** none; all seven stored status strings remain unchanged.

### Worker and job boundaries

Generic `workers.Pool`, `Runner` and `FencedRunner` stay in the stdlib-only SDK.
A host can bring its own job type and store without importing jobs. Jobs supplies
its richer aggregate, kind filters, scheduling, repositories and retry policy.
Worker middleware wraps a polling iteration, including claim and persistence.
Job middleware wraps processing after claim. These are supported extension points.

### Find and migrate affected code

Search imports of `sdk/foundation/{async,workers}`, processor definitions and
runner construction, hooks, pool error readers and heartbeat consumers.

| Previous API | Migration |
|---|---|
| `ProcessFunc[T] func(context.Context, T) (T, error)` | Return only `error`; change `return job, err` to `return err`. The runner captures the claimed ID before processing and uses it for every transition. Persist payload changes through an explicit store/checkpoint operation. |
| `workers.Job` requiring ID, Status and RetryCount | Now requires only `ID() string`. Fenced generic code uses `workers.FencedJob`, which also requires `RetryCount() int`. Code reading status declares its own needed interface or uses its concrete entity. Existing jobs entity fields/methods remain available. |
| `PreProcessHook`, `PostProcessHook`, `AddPreProcessHooks`, `AddPostProcessHooks` | Compose `JobMiddleware[T]` around an error-only processor. Before/after code surrounds processing, before persistence. Previously hook errors were logged and processing continued; choose deliberately whether a migrated wrapper returns an error or only logs it. |
| `WithRetryWithinClaim` | Removed. One processor invocation per claim; ordinary retries use store Fail, fenced retries use durable Reschedule. Any operation-specific short retry loop belongs in host processing, with its own bounded context. |
| `FencedRetryFunc`, `WithFencedRetry` | Use `WithFencedRetryDecider(func(err error, attempt int) (time.Duration, bool))`; an old attempt-only policy can ignore err. |
| `Pool.Errors()` | Removed. Configure `WithLogger` for ordinary iteration errors and handle `Run(ctx)`'s returned fatal error. No replacement error-channel reader is needed. |
| Reusing a Pool/runtime after Run | Construct a new one; subsequent or concurrent calls return `workers.ErrAlreadyRun`. |
| `async.WithShutdownTimeout` | Pass a deadline context to every `Close(ctx)` call that needs a bound. |
| `async.IOPreset` / `async.CPUPreset` | Set explicit `WithMaxConcurrency` and `WithDropOnFull`. Old IO values were 1000/true; old CPU values were GOMAXPROCS(0)/false. Preserve those only if they fit the host; old 30s/60s shutdown limits move to Close contexts. |
| Heartbeat field `claims` | Now `successful_iterations`: counts nil iteration results, including handled failure/deferral or a short-circuiting wrapper, not successful job deliveries. Update queries/dashboards that parse the old field. |

A generic job wrapper now looks like this (`Report`, `ready` and
`processReport` are application-owned):

```go
func gate(next workers.ProcessFunc[Report]) workers.ProcessFunc[Report] {
    return func(ctx context.Context, report Report) error {
        if !ready(report) {
            return workers.DeferUntil(time.Now().Add(time.Minute), "inputs pending")
        }
        return next(ctx, report)
    }
}

process := workers.ChainJobMiddleware(processReport, gate)
runner := workers.NewRunner(queue, process, log)
pool := workers.NewPool(runner.WorkFunc(), workers.WithLogger(log))
if err := pool.Run(ctx); err != nil {
    return err
}
```

The first wrapper is outermost. Configure before running and synchronize shared
state. For jobs wiring, ordinary `Config.JobMiddleware` is
`[]workers.JobMiddleware[job.Job]`; fenced `FencedRuntimeConfig.JobMiddleware` is
`[]workers.JobMiddleware[jobs.FencedClaim]`. Both expose `WorkerMiddleware` (`[]workers.Middleware`) for queue iterations. Existing host `HandlerFunc` wrappers
and handler signatures still work. Worker middleware does not gate the separate
scheduler and runs outside the ordinary runtime's detached execution context;
put execution-specific deadlines in job middleware or existing handler wrappers.

### Gate outcomes and optional store support

- Nil means successful processing, even if middleware did not call next. A
  finished check that found unchanged source content can correctly return nil.
- `workers.DeferUntil(until, reason)` means try later, without consuming a failure
  attempt. Until must still be in the future when processing returns.
- `workers.Reject(reason)` means immediate dead-lettering. `jobs.Permanent`
  forwards to this disposition and keeps its reason string. Permanent rejection
  wins if an error tree contains both rejection and deferral.
- Ordinary errors/panics use existing failure policy. Post-processing wrapper
  code precedes persistence. Fenced `SetDeadLetterHook` / jobs `DeadLetterFunc`
  still run only after a successful dead-letter write.

Base `JobStore`, `FencedStore`, `job.QueueRepository` and
`job.FencedQueueRepository` method sets remain unchanged. Custom stores opt into
deferral through these additional interfaces:

```go
// workers.JobDeferrer / job.QueueDeferrer
Defer(ctx context.Context, id string, availableAt time.Time, reason string, now time.Time) error

// workers.FencedDeferrer / job.FencedQueueDeferrer
Defer(ctx context.Context, id, leaseID string, availableAt time.Time, reason string, now time.Time) error
```

All built-in memory/pgx/Turso queues implement them. Ordinary deferral atomically
returns a running job to pending at the future time without changing retries.
Fenced deferral verifies the live lease, releases it and refunds only the current
claim's increment in one transaction; earlier attempts and checkpoint bytes
remain intact. Repeated, stale, expired and terminal-state releases conflict.
Do not implement fenced deferral as a separate Reschedule and retry decrement:
a race could refund an attempt belonging to someone else. Ordinary queues have
no per-claim token and cannot offer the same protection from stale workers.

Invalid deferral returns `workers.ErrInvalidDeferral`; missing optional support
returns `workers.ErrDeferralUnsupported`. Neither completes nor fails the job;
the claim remains subject to the custom store's recovery mechanism. Include
optional deferral conformance when implementing this feature in a custom store.

### Runtime behavior corrections

Async admission is synchronized with closing. Close stops new admission, releases
blocked submitters and waits for all admitted tasks; nil means drain completed.
A deadline returns `ctx.Err()` while running tasks continue. Repeated callers can
wait for the same drain with independent contexts. A nil logger is accepted.
GoContext controls admission, not the task's own context. Finish all submissions
before using Wait as a batch barrier; use Close to shut down concurrent producers.

Pool logs ordinary iteration failures and retains the first fatal cause even if
parent cancellation happens during drain. `ErrPoolShutdown` takes precedence over
`ErrWorkerShutdown`, stops peers, and returns from Run after they finish. The
consecutive-error middleware keeps counters per wrapped function and preserves
causes/control scope. Poll/idle intervals are delays after an iteration; long
work no longer consumes the next delay. Wake signals can bypass that delay.

Runners return unexpected persistence errors instead of reporting successful
completion. A fenced ownership conflict is an expected lost lease, logged without
attempting another write. Ordinary jobs Runtime drains already admitted work on
a live, detached context. A fatal queue or scheduler failure cancels the sibling
and waits for both. Hosts must bound store I/O as well as handlers and own the
outer shutdown deadline; handler timeouts alone do not bound Claim or persistence.

Fenced processing uses the parent cancellation signal and leaves work recoverable
on shutdown. Its timeout context now ends as soon as processing returns, before
persistence. Timeout must be shorter than the lease: invalid SDK configuration
panics in NewFencedRunner; jobs.NewFencedRuntime retains its configuration error.
Allow margin for Claim latency and persistence. Context cancellation requires
cooperative processing and does not forcibly terminate host code.

### Work protocol input tightening

`work.Enqueuer.EnqueueOnce`, `Replacer.Replace` and
`StatusReader.LatestStatusByKey` now require a nonempty logical key. Jobs Service
returns `sdk.ErrInvalidInput` on empty keys. Hosts choose a key namespace shared
across kinds; do not assume two kinds get separate deduplication scopes.

This does not change empty execution IDs in ordinary Enqueue/EnqueueJob, which
still request generated IDs. Full-input EnqueueOnceIn/ReplaceIn and direct
repositories retain their optional-key/unkeyed behavior. If you previously used
the positional protocol with an empty key for fresh independent work, use the
appropriate ordinary or full-input API explicitly.

### Consumer evidence and upgrade checks

The original sample found only jobs constructing generic runners; Segovia also
uses Pool for events. Refresh any consumer checkout before relying on an earlier
usage inventory. After the owner updated GPS360, a read-only review of clean main
`e1ab3f0` found echo, delivery and comps workers plus a jobs inspection/retry UI.
GPS360 now pins SDK v0.7.1 and jobs/stores v0.5.0, without local replacements;
this supersedes the earlier v0.4.1/local-replace sample. Segovia's sampled SDK/jobs
pins were v0.8.0/v0.4.2; Coordination Hub's were v0.7.0/v0.3.0.

GPS360's newer workers use jobs.Runtime, ordinary queues, kind filters,
host-owned timeout wrappers and production-only schedule wiring. They call none
of the removed SDK hooks/retry helpers. Their wrappers can stay unchanged or move
to JobMiddleware. Intentional unchanged-content/partial-success outcomes remain
host policy, not implicit deferral. Its retry UI creates a fresh execution from a
dead letter; the new empty-logical-key rule does not affect that ordinary path.

A separate, source-reviewed GPS360 follow-up: cancellation while processing the
last comps race can become a failure-summary string and then a nil overall job
result (`internal/logic/domains/comps/import.go`, `RunImport` adapter). Propagate
cancellation at the host workflow boundary while retaining intentional per-race
partial success. This is existing consumer behavior, not fixed or experimentally
verified in this framework change. No consumer application was edited or upgraded.

When upgrading, run the host build/tests and exercise its actual shutdown,
job retry/dead-letter, timeout and gate behavior. For custom stores, run the work
and applicable jobs conformance suites, including concurrent admission, byte
ownership and atomic fenced refunds. Validate pgx on the host's configured schema
and Turso on the intended backend. Release SDK/jobs/store module requirements
together; workspace builds alone do not establish compatible published pins.

Implementation and exact framework verification are recorded in
[workers-cleanup.md](plans/workers-cleanup.md). Full jobs/events pocket audits and
the existing SQL latest-generation ordering follow-up remain open.

## AUDIT-008: Cryptids name restoration

- **Implemented:** 2026-09-10.
- **Modules:** SDK and callers in authentication, golang-jwt and auth-cms.
- **Release:** unreleased; no versions, pins or tags changed.
- **Impact:** import/package rename for checkouts that adopted the temporary
  `sdk/foundation/cryptography` name during AUDIT-004.
- **Data migration:** none; encryption, digest and token behavior is unchanged.

The package name is `cryptids`, meaning "cryptography tidbits", by owner choice.
Replace imports of `sdk/foundation/cryptography` with
`sdk/foundation/cryptids`, and change the qualifier `cryptography.` to `cryptids.`.
There is no forwarding compatibility package at the temporary path. Consumers
still using the original `cryptids` path do not need to rename their crypto import.
The empty-digest diagnostic prefix is now `cryptids:`; it still wraps
`sdk.ErrInvalidInput`. Match the sentinel rather than the diagnostic string.

All other AUDIT-004 migrations still apply: ID generation is separate from crypto
(now root SDK after AUDIT-009); SHA256 is a function; the old hasher, SDK HS256 implementation
and identity batch helper remain removed. JWT signing still uses the golang-jwt
integration. AUDIT-004 above has been updated to show the final namespace, so
consumers can apply it directly without an intermediate rename.

AUDIT-009 subsequently implements the approved pointer/slug/ID/identity root
promotion. Validation and environment retain their packages. This naming entry
describes only the crypto rename. Run consumer build/tests when upgrading. Implementation and exact
verification: [cryptids-name-and-sdk-shape.md](plans/cryptids-name-and-sdk-shape.md).

## AUDIT-009: Common primitives promoted into root SDK

- **Implemented:** 2026-09-10.
- **Modules:** SDK and its consumers throughout pockets, stores, integrations,
  examples and Workshop scaffolding.
- **Release:** unreleased; no module versions, requirements or tags changed.
- **Impact:** four removed import paths; renamed root symbols and Go type identities.
- **Data migration:** none. ID/slug output, struct fields, constant strings and
  ordinary JSON/storage representations are unchanged.

### Final package shape

Common vocabulary and small, explicitly named primitives now live in root
`github.com/gopernicus/gopernicus/sdk`, in separate pointer.go, slug.go, id.go and
identity.go files. The old `sdk/foundation/{pointer,slug,id,identity}` directories
are removed. There are no alias/forwarding packages at those paths.

`foundation/validation`, `foundation/environment` and `foundation/cryptids` remain.
Validation checks, environment policy, cryptographic behavior and all other audit
changes are preserved. The SDK is still stdlib-only; root never imports a
subpackage. ID generation and caller identity remain independent concerns.

### Import and symbol migration

Replace the old package imports with the root SDK import. Merge duplicate imports
and reuse any existing root alias. Search Go source, tests, templates and generated
inputs; regenerate generated source rather than hand-editing it. Update only
selectors belonging to the imported package, not a local variable named id or slug.

| Previous qualified API | Root SDK API |
|---|---|
| `pointer.Deref`, `pointer.DerefOr` | `sdk.Deref`, `sdk.DerefOr` |
| `slug.Make` | `sdk.Slugify` |
| `id.Generator` | `sdk.IDGenerator` |
| `id.GenerateFunc` | `sdk.IDGenerateFunc` |
| `id.NewGenerator` | `sdk.NewIDGenerator` |
| `id.NanoID` | `sdk.NanoID` |
| `id.Database` | `sdk.DatabaseID` |
| `id.Alphabet`, `id.DefaultLength` | `sdk.DefaultIDAlphabet`, `sdk.DefaultIDLength` |
| `identity.Principal` | `sdk.Principal` |
| `identity.WithPrincipal`, `identity.FromContext` | `sdk.WithPrincipal`, `sdk.PrincipalFromContext` |
| `identity.Info`, `identity.Address`, `identity.Resolver` | `sdk.IdentityInfo`, `sdk.IdentityAddress`, `sdk.IdentityResolver` |
| `identity.User`, `identity.ServiceAccount` | `sdk.PrincipalTypeUser`, `sdk.PrincipalTypeServiceAccount` |
| `identity.KindEmail`, `identity.KindPhone` | `sdk.AddressKindEmail`, `sdk.AddressKindPhone` |

Example wiring:

```go
import "github.com/gopernicus/gopernicus/sdk"

ids := sdk.IDGenerator{}
dbIDs := sdk.NewIDGenerator(sdk.DatabaseID)
uuidIDs := sdk.NewIDGenerator(googleuuid.V7()) // existing UUID integration

name := sdk.DerefOr(optionalName, "Anonymous")
slug := sdk.Slugify(name)
ctx = sdk.WithPrincipal(ctx, sdk.Principal{
    Type: sdk.PrincipalTypeUser,
    ID:   userID,
})
principal, ok := sdk.PrincipalFromContext(ctx)
```

The expressions above belong in the application's wiring/function; `googleuuid`
keeps its existing integration import. No global configuration or service startup
is introduced by these root helpers.

### Preserve behavior while migrating

`IDGenerator.Func`, `Generate` and `MustGenerate` are unchanged. Zero value still
emits 21 characters from the same 52-character default alphabet. NanoID retains
its custom-alphabet/size validation. DatabaseID is a function returning an empty
ID for a supporting store to assign; it is not a database client. UUID V4/V7
constructors now return sdk.IDGenerateFunc and keep their module paths/output.
Pocket Config.IDs fields retain their name and accept the new root generator type.

Deref/DerefOr still default only nil pointers, preserving present zero values.
Slugify retains its ASCII/accent-folding behavior, empty-result handling and lack
of normalization/uniqueness enforcement. Existing stored IDs, slugs and routes
must not be regenerated merely because these functions moved.

Principal still has Type and ID. IdentityInfo retains Principal, DisplayName and
Addresses; IdentityAddress retains Kind and Value. Subject strings remain user /
service_account; address kinds remain email / phone, with host-defined strings
still supported. PrincipalFromContext requires both fields and uses a distinct
private key alongside request/trace/span keys. Migrate producers and readers to
the root helpers together; do not recreate their private key in an application.

Resolver and notification method signatures must use the identical root types:
`Resolve(context.Context, sdk.Principal) (sdk.IdentityInfo, error)` and
`Notify(context.Context, sdk.IdentityAddress, Message) error`. Existing host
interfaces and test doubles must migrate too. Preserve genuine type aliases
(e.g. `type Principal = sdk.Principal`) where the old code used aliases; defining
a new named lookalike struct will not satisfy the same interface automatically.
Identity projection still does not confer permission to notify an address.

Go reflection names/package paths change because these are newly located named
types. Framework code has no persisted reflection/gob registry using the old
names; review any application-owned registry keyed by Go type identity. Ordinary
JSON field names and persisted strings are unaffected.

### Cumulative audit migrations and release

AUDIT-002 and AUDIT-004 now show final root destinations so a consumer on an older
published SDK can migrate directly. The removal of ResolveAll, old conversion
helpers, SDK HS256 and hasher objects still applies. Cryptographic helpers retain
the owner-selected cryptids name (AUDIT-008).

Coordinate the SDK release and all affected module requirements, including
Workshop: emitted code now expects the root APIs and cannot compile against an
older SDK pin. Workspace replacements in framework/scaffold tests are development
verification, not proof of compatible published requirements. No pins or consumer
applications were upgraded during this task.

Run consumer build/test/vet and regenerate scaffolds or generated bindings when
adopting. Exercise authentication principal propagation, notification adapters,
custom ID strategies and database-generated IDs at their real application seams.
Framework verification covers root regressions, those existing pocket/example
paths and scaffold compilation; live datastores and consumer upgrades remain
separate. Exact commands, results and changed paths:
[sdk-root-promotion.md](plans/sdk-root-promotion.md).

## AUDIT-010: Cache storage, data helpers and public-page policy

- **Implemented:** 2026-09-10.
- **Modules:** `sdk`, `integrations/kvstores/goredis`, `pockets/cms`; example hosts
  and documentation migrated in this checkout.
- **Release:** unreleased; versions and downstream requirements have not been
  selected or bumped. Coordinate the SDK, Redis adapter and CMS requirements
  when publishing.
- **Impact:** breaking constructors/interfaces, literal-prefix invalidation,
  TTL/cancellation/byte-ownership behavior, bounded memory and public HTTP caching.
- **Data migration:** no database schema change. Old page entries are cold cache
  data; new Pages uses a new versioned key and response-record format. Allow old
  entries to expire or deliberately invalidate their owned `page:` prefix.
  Do not flush a shared Redis database to perform this migration.

### Migrate the API

Search imports and aliases of `sdk/capabilities/cacher`, custom Storer adapters,
cache mocks, Pages, CMS Config and the Redis cache adapter. Ratelimiter and events
have their own NewMemory/Close methods; this entry changes only caching.

| Previous usage | Replacement |
|---|---|
| `cacher.NewMemory()` | `cacher.NewMemory(cacher.MemoryConfig{})`, or set `MaxEntries` explicitly |
| `cacher.New(store, options...)` / SDK `cacher.CacheOption` | `cacher.New(store, cacher.Config{Namespace: "app:domain:v1", OnError: report})`; empty Config is valid. SDK CacheOption was unused and is removed; Redis's separate CacheOption remains. |
| `cacher.Pages(store, ttl)` | `cacher.Pages(store, cacher.PageConfig{TTL: ttl, MaxBodyBytes: 1 << 20})` |
| `cache.DeletePattern(ctx, "page:*")` | On a prefix-capable raw store: `cache.DeletePrefix(ctx, "page:")` |
| `Storer.DeletePattern` | Optional `cacher.PrefixDeleter.DeletePrefix`; core Storer now has only Get/GetMany/Set/Delete |
| `Storer.Close()`, `Memory.Close()` or Redis `Cacher.Close()` | Remove the cache close; the host closes its owned Redis client directly |
| Hardcoded CMS page freshness | Set `cms.Config.PageCache` (`cacher.PageConfig`) alongside `Config.Cache` |

DeletePrefix is **literal**, not a renamed glob operation. Remove the trailing
wildcard only when the old pattern actually meant a prefix. Arbitrary patterns
such as `user:*:permissions` need a deliberate key/invalidation design; do not
blindly translate them. Empty prefix clears the selected adapter namespace.
For shared Redis, choose nonoverlapping adapter prefixes and keep all invalidation
inside the namespace owned by the application.

Only adapters that support deletion should implement PrefixDeleter. A generic
caller checks that interface at composition time or handles absence explicitly.
The data facade has `Cache.InvalidatePrefix(ctx, logicalPrefix)`, which scopes
invalidation to its namespace and returns `errors.ErrUnsupported` when the raw
store lacks PrefixDeleter. Cache deliberately does not implement PrefixDeleter.
A wrapper that embeds only Storer no longer inherits prefix deletion implicitly.

### Storage behavior

- Memory now defaults to **10,000 entries**, with configurable MaxEntries and LRU
  eviction. Nonpositive MaxEntries selects the default. This bounds entry count,
  not payload bytes; choose capacity for the host's payload sizes. Memory must be
  constructed with NewMemory. It has no janitor or shutdown work.
- Memory copies writes and independently owns returned Get/GetMany bytes. Updating
  a retrieved slice no longer modifies stored data. Expired keys are reclaimed on
  access and before capacity evicts live entries. Any entry can be evicted before
  its TTL; cache storage is not authoritative persistence.
- Empty values are hits. GetMany returns only present logical keys; duplicate keys
  appear once. Missing keys and deletion of an absent key are successful operations.
- Raw Set and explicit SetJSON: **zero TTL means no expiration**, negative TTL
  matches `sdk.ErrInvalidInput`, positive TTL expires. Each write replaces the
  previous TTL. Redis rounds positive TTL up to milliseconds without overflow;
  `-1ns` no longer leaks the driver's keep-TTL sentinel.
- Canceled contexts return their error. Redis checks before and after commands;
  prompt interruption depends on the host's client timeout settings. Cancellation
  does not undo a write or deletion already accepted by Redis.

Custom adapters should run `cachertest.Run`; prefix-capable adapters should also
run `cachertest.RunPrefix`. The optional suite takes the same
`func(*testing.T) cacher.Storer` factory and asserts PrefixDeleter support.

### Complete application-data caching

`cacher.New(nil, cacher.Config{})` selects Noop; explicit `cacher.Noop{}` also
works. Use constructors rather than zero-value Cache/Memory, and do not pass a
typed-nil adapter. Noop preserves valid-input/cancellation rules but stores nothing.

Cache namespaces are length-framed even when empty, preventing ambiguous
namespace/key pairs. Adapter namespaces and service namespaces compose. Raw
Cache methods are strict and report errors through optional
`OnError func(context.Context, string, error)` without adding keys or payloads.
The hook runs synchronously and must be safe for concurrent calls.

Context-first generic helpers now provide:

```go
value, found, err := cacher.GetJSON[Value](ctx, cache, key)
err = cacher.SetJSON(ctx, cache, key, value, ttl)
value, err = cacher.GetOrLoadJSON(ctx, cache, key, ttl, load)
```

GetJSON preserves false, zero and JSON null as hits. GetJSON/SetJSON report and
return cache/codec errors. GetOrLoadJSON alone reports and bypasses those errors,
loads with the caller's context, and returns successful source data even when
cache storage fails. Loader errors and caller cancellation reach the caller;
failed loads are not cached. Negative TTL is rejected before loading. Choose a
namespace/key schema version when changing JSON shapes; decoded JSON is not
implicitly domain-validated. Concurrent misses can load independently.

### Review HTTP behavior even when the host compiles

Pages now accepts PageConfig. Zero TTL selects **60 seconds**, negative disables
it, and nonpositive MaxBodyBytes selects **1 MiB**. These defaults deliberately
differ from raw Set's zero=no-expiration. CMS uses the same zero defaults;
`Config.Cache == nil` still disables public-page caching. Hosts can now configure
CMS TTL, body limit and an additional trusted Scope without changing the pocket.

Page keys include request scheme, authority, exact URI and optional
`Scope func(*http.Request) string`; raw bytes are framed before hashing beneath
`page:v2:`. The host decides proxy trust and tenant identity. Records preserve
committed HTML metadata, bounded bodies and absolute expiration. Hits honor the
current TTL and body limit too. They include Age and X-Cache: HIT; eligible lookup
misses include X-Cache: MISS. Outer metadata conflicts cause a render.

Mount Pages only on public GET HTML whose variations are represented by the key.
It bypasses credentials, cookies, context principals, conditional/range requests
and request cache directives even for warm entries. A successful response needs
an explicit valid HTML Content-Type. It bypasses private/no-store/no-cache,
zero/invalid/unsupported cache directives, Vary, cookies, content encoding,
nonce CSP, explicit Date/Content-Length/Expires/Age, trailers and unsupported
response metadata. Put compression and per-request headers outside Pages. Keep
personalized, nonce-dependent or otherwise unkeyed variants outside cached routes.

Hits preserve Content-Type/charset, Content-Language, Cache-Control,
ETag/Last-Modified, Link and supported CSP/security headers. Request IDs, tracing,
timing and generated Date stay fresh. Oversized bodies and render/write/flush
failures continue through the response without storing it. Cache outages become
misses. The data Cache error hook does not automatically instrument Pages.

`DeletePrefix(ctx, "page:")` on a raw store invalidates its page entries. Prefix
invalidation is best effort: an in-flight loader/render or concurrent writer can
repopulate old data. CMS does not add synchronous write invalidation or promise
immediate publication freshness in this change. Host TTL and invalidation policy
remain necessary.

### Verify a consumer upgrade

1. Resolve the actual SDK/Redis/CMS module versions, including workspace/replace
   and vendor overrides; migrate custom adapters, mocks and constructors together.
2. Run the host's formatter, build, test and vet. Run both applicable cache
   conformance suites against its real adapter and verify namespace isolation.
3. Exercise miss/hit/invalidation, disabled storage, outage, corrupt JSON and
   source failure. Confirm errors are observable and source failures remain errors.
4. Exercise actual HTTP hits across hosts/tenants, private requests, cache policy,
   response headers and oversized pages. Review page TTL/body limits explicitly.

Runnable framework example: `examples/minimal` serves `GET /catalog.json` from a
public CMS product projection, with host namespace/TTL/error reporting and HTTP
no-store independent of data caching. It excludes drafts and demonstrates
cache invalidation and source fallback in its HTTP/service tests.

Implementation, exact verification and scope limits:
[plans/cacher-implementation.md](plans/cacher-implementation.md). External
consumer applications were not migrated or run; no release was published.

## AUDIT-011: Rate-limiter contract and adapter corrections

- **Implemented:** 2026-09-10.
- **Modules:** SDK, `integrations/kvstores/goredis`,
  `integrations/datastores/pgxdb`, and `pockets/authentication`.
- **Release:** unreleased; coordinate dependent requirements when publishing.
- **Impact:** breaking constructors/interfaces/middleware, input validation,
  quota arithmetic, Memory capacity, and distributed state format.
- **Data migration:** PostgreSQL requires the additional column below. Both
  distributed adapters use new physical keys and normally start fresh budgets.
  No consumer database, Redis instance, module pin, version or tag was changed.

### Migrate the Go API

| Previous usage | Replacement |
|---|---|
| `ratelimiter.NewMemory()` | `ratelimiter.NewMemory(ratelimiter.MemoryConfig{MaxEntries: 10000})`; nonpositive MaxEntries selects 10,000 |
| Limiter interface/concrete `Close()` | Remove limiter shutdown calls; close the host-owned Redis client or DB pool separately |
| `ratelimiter.RateLimiter`, `New`, `Option`, `WithLogger`, `AllowWithLimit`, `Resolver()` | Call the injected Allower/Limiter directly; resolve policy in the host and report errors in the host or HTTP OnError hook |
| `LimitResolver`, `ResolveRequest`, `APIKeyInfo`, `DefaultLimitResolver`, `NewDefaultResolver` | Resolve host/pocket policy to `(Limit, error)`, handle failure, then call `Allow(ctx, key, limit)`; own subject/key/override precedence locally |
| `DefaultLimit` | Choose an explicit application budget; PerSecond/PerMinute/PerHour/WithBurst remain |
| `Acquire(ctx, limiter, key, limit)` requiring Limiter | Same call, now accepts the smaller Allower interface (Allow only) |
| `Middleware(l, limit, key, reject)` | `Middleware(l, MiddlewareConfig{Limit: limit, Key: key, Reject: reject, FailOpen: ..., OnError: ...})` |
| `ErrRateLimitExceeded`, `ErrLimiterClosed` | These unproduced sentinels are removed; exhausted quota is `Result{Allowed: false}`, backend/validation/capacity failures are errors |

Keep dynamic policy out of the SDK: its previous user/service-account/anonymous
budgets were application choices. Preserve any intended old values and API-key
precedence explicitly in your host. Do not fall back silently when policy storage
fails unless that is your selected policy. A runnable example covering failure,
Allow and worker Acquire lives at
`examples/minimal/internal/logic/domains/catalog/ratelimit_example_test.go`.
No new public generic resolver abstraction replaces the removed wrapper.

### Review quotas and accepted input

Allow and Reset reject empty keys. Requests and Window must be positive and Burst
nonnegative. Normalize rounds windows up to whole milliseconds. Requests+Burst
must be at most MaxCeiling (2^31-1); the rounded window must fit MaxWindow; the
ceiling times window milliseconds must be at most 2^52-1. Unsupported values and
live Window changes match `sdk.ErrInvalidInput`. Validate policy at configuration
boundaries with `Limit.Normalize()` if useful; every bundled Allow validates too.

Memory now honors Burst and shares Redis/Postgres's anchored two-window counter
approximation. Effective usage is the current count plus the floored decaying
previous count; Burst adds to the ceiling. This is approximate sliding admission,
not an exact event log or continuously replenished burst bucket. ResetAt marks
the current bucket end. Denied RetryAfter is the backend-derived duration to that
checkpoint, not the earliest possible admission or a reservation. Workers may
wait across more than one checkpoint under contention; Acquire is not a fair queue.

A live key cannot change normalized Window. Reset it, let it expire, or deliberately
version the logical key. Those actions discard/separate quota. Changing Requests
or Burst preserves consumed quota. State expires at the bucket start plus two
windows; denial traffic does not extend expiry inside a bucket. Expired state is
absent for decisions even before physical pruning. Clock rollback clamps to the
last decision; Redis retries no longer depend on application clock skew.

Memory has no janitor or Close. It reclaims expired entries on access/at capacity
and never evicts live budgets to admit new keys. At capacity a new key returns
ErrCapacity, matching `sdk.ErrUnavailable`; existing keys remain enforced. Size
MaxEntries for active distinct keys and choose how to handle capacity failures.
This bounds entry count, not total bytes. Distributed adapters have no SDK Memory
capacity setting; hosts manage their storage and retention.

### Select HTTP failure policy explicitly

Middleware now defaults to closed: dependency/capacity failures produce 503.
Set FailOpen:true only on routes whose intended outage behavior is to proceed.
Invalid configuration, missing limiter/key function, empty keys and invalid/live-
changed windows produce 500 even when open. Omit middleware to disable it; nil is
not a disable switch. Typed-nil interface values remain caller misuse.

OnError receives the cause synchronously and must be concurrency-safe. Public
500/503 responses use generic messages; the hook receives diagnostics. Exhausted
quota uses JSON 429 with code `rate_limited` and overflow-safe Retry-After rounding,
unless Reject is supplied. Caller cancellation neither invokes downstream work
nor writes a new response. Acquire checks cancellation before attempts and before
returning success. Remote admission committed before cancellation is not refunded.

Authentication explicitly preserves its open public-IP and refresh-session outage
paths and now logs classified warnings without keys/backend details. Public-IP
quota denial gains Retry-After. A canceled refresh stops before token rotation.
Other service-level policies remain unchanged; review direct SDK middleware users
separately from authentication's selected policy.

### Migrate distributed state before rollout

Add this to the host's migration ledger and apply it before starting the new
PostgreSQL adapter (use the host schema/search_path that owns the existing table):

```sql
ALTER TABLE ratelimit_windows
    ADD COLUMN window_ms BIGINT NOT NULL DEFAULT 0;
```

For new installations use the complete reference DDL in
`integrations/datastores/pgxdb/README.md`. The default 0 marks legacy rows and
permits old writers to continue on their own keys. Runtime code does not create
or migrate schema. Call `limiter.StatusCheck(ctx)` before serving; it now probes
window_ms as well as the table and reports missing schema as `sdk.ErrNotFound`.
It does not validate every permission, constraint, or deployment policy.

Every Postgres admission decision is atomic and samples time after row locking.
First use of a missing key needs two statements: an expired zero-count placeholder,
then the decision. Established keys need one. This avoids stale admission if an
uncommitted competing first insert rolls back after a long wait. Backend errors
are never retried. If concurrent Reset/pruning interrupts initialization twice,
Allow returns `sdk.ErrConflict` without consuming quota.

Redis and PostgreSQL append internal `v2:` after **every** configured prefix,
including empty/custom prefixes. With the default prefix, logical `login:alice`
now maps to `ratelimit:v2:login:alice`. Redis stores whole milliseconds, window_ms,
last update and logical expiry; PostgreSQL adds window_ms and uses revised
anchored transitions. Existing old rows/keys are not converted. A preexisting
legacy record at a new physical key is rejected as `sdk.ErrConflict` and preserved
by Allow and Reset, even when logically expired. Resolve it through host-owned
namespace/cleanup policy, not by retrying Reset.

**Keep old and new physical namespaces disjoint during rollout.** An old logical
key beginning `v2:` can collide. If a new row/hash exists first, an old writer can
preserve its positive window_ms while overwriting other fields; the format guard
cannot detect that mixed state. Use a fresh host prefix if any overlap is possible,
or stop the old writers before moving traffic. Do not reuse a live key for a new
Window. Old and new fleets using distinct namespaces have independent budgets;
a rolling overlap can therefore admit against both quotas. Coordinate traffic
cutover and decide whether a fresh quota is acceptable for the application's
security/availability policy. There is no automatic quota transfer or rollback.

Leave ordinary old Redis keys to expire and prune old PostgreSQL rows using the
host's retention job. Do not drop window_ms while new code is active. A rollback
to old code normally resumes its old namespace; its retained quota may be stale
or expired, so treat rollback as another quota-policy decision.

### Verification for consumers

Run affected modules' build/test/vet and custom backends through
`ratelimitertest.Run`. Exercise denial, Burst, canceled calls, live Window changes,
capacity/outage policy and real HTTP Retry-After. Verify the PostgreSQL migration
and boot probe in a disposable database, and check physical key overlap before
rollout. Framework verification and task-relative file inventory are recorded in
[ratelimiter-implementation.md](plans/ratelimiter-implementation.md); historical
audit probe scripts describe the old API and must not be run unchanged.

## AUDIT-012: Event delivery contracts and Redis recovery

- **Implemented:** 2026-09-10.
- **Modules:** SDK, `pockets/events`, `integrations/kvstores/goredis`, and
  framework hosts/callers that emit or replay events.
- **Release:** unreleased; coordinate dependent requirements when publishing.
- **Impact:** breaking emission, Redis subscription, poller and remote-envelope
  APIs; bounded admission errors; pending recovery and Redis transport format.
- **Data migration:** existing outbox records need no schema change. Existing
  Redis event streams and pending work require a coordinated host-owned cutover.
  No external consumer, deployed state, module pin, version or tag was changed.

### Choose the delivery boundary explicitly

| Previous usage | Replacement |
|---|---|
| `bus.Emit(ctx, event)` | Same call, now bounded asynchronous admission; handle validation, cancellation, capacity and closure errors |
| `memory.Emit(ctx, event, events.WithSync())` | `memory.Dispatch(ctx, event)` to wait for selected local handlers |
| `redisBus.Emit(ctx, event, events.WithSync())` | `redisBus.Publish(ctx, event)` to wait for Redis acceptance; it does not force local callbacks |
| `EmitOption`, `EmitConfig`, `WithSync`, `ApplyOptions` | Removed; adapt custom emitters to `Emit(context.Context, Event) error` and expose any checked delivery separately |
| Redis `Subscribe(topic, handler)` used for competing work | `SubscribeWork(ctx, topic, handler)` with an exact topic; ordinary Subscribe now selects pub/sub notification fanout |
| Redis wildcard Subscribe used for work | Register explicit work topics, or keep `Subscribe("*", handler)` only for ephemeral fanout |
| `NewPoller(repo, bus, opts...)` | `NewPoller(repo, memory.Dispatch, opts...)` or `NewPoller(repo, redisBus.Publish, opts...)` according to the required handoff |
| Event-bus Redis `Options.MaxLen` / `Options.BatchSize` and associated env settings | Remove them; work claims one record per available worker, and the host owns archival/trimming |
| `RemoteEvent{EventType: ..., Occurred: ..., ...}` | `record.Event()` for a copied snapshot, or `RemoteEvent{Record: record}` when explicitly constructing an envelope |
| `DecodeRemoteMetadata(payload)` | Read explicit envelope metadata through `events.Metadata`; producers supply Metadata independently of their payload format |

An Emit success confirms admission only, not persistence or handler completion.
Accepted work retains context values but detaches caller cancellation; later
encoding, transport and handler failures are reported through the bus logger.
Memory.Dispatch returns the first handler error and recovers callback panics as
`events.ErrHandlerPanic`. Redis.Publish waits for XADD, then attempts best-effort
pub/sub notification. A lost notification does not undo an accepted stream entry.
Cancellation or a connection error cannot prove that a remote write did not commit;
retries must preserve identity and tolerate duplicates.

`Bus` still combines Emitter, Subscriber and Close. The new narrow `Subscriber`
port is sufficient for `WakeChannel`. `Broadcaster` remains supported for explicit
fanout; Memory and Redis Subscribe already have those notification semantics.
Redis subscribers on every connected process can receive each matching event;
disconnected and slow notification consumers can miss it. There is no total
ordering or exactly-once guarantee. Keep event graphs immutable after admission
and make handlers safe for concurrent calls.

### Migrate the outbox without weakening its handoff

The poller accepts `DeliverFunc(context.Context, events.Event) error` rather than
a Bus. It calls the function and marks an entry only after success, checking
cancellation before each delivery and before marking. Delivery failure, a closed
bus or cancellation leaves the entry unpublished. A nil delivery function returns
`sdk.ErrInvalidInput` when polling. A successful delivery followed by a failed mark
or crash can still produce a duplicate on the next poll.

For a local outbox subscriber that enqueues durable jobs, retain local handler
completion as the boundary:

```go
bus := sdkevents.NewMemory(sdkevents.WithLogger(log))
sub, err := bus.Subscribe("document.changed", enqueueDocumentJob)
if err != nil {
    return err
}
defer sub.Unsubscribe()
poller := eventspocket.NewPoller(outboxRepo, bus.Dispatch)
// Run poller.Poll on the host's workers.Pool after all required handlers register.
```

Here `sdkevents` is `sdk/capabilities/events` and `eventspocket` is `pockets/events`.
Memory.Dispatch succeeds with no selected handlers, so handler registration is a
host precondition. A host with several required operations may compose them into
one registration. In the sampled Segovia outbox-to-job pattern, replacing this
handoff with asynchronous Emit would allow marking before job admission succeeds.

For a remote handoff, pass `redisBus.Publish` and explicitly register work with
`SubscribeWork`. That poller can mark after Redis accepts the entry; the Redis
consumer group then owns delivery/retry. Registering only ordinary notification
subscribers does not create reliable remote work processing. Do not pass Emit,
`events.Noop{}.Emit`, or an unconditional-success wrapper as a required handoff:
the function type cannot inspect its delivery guarantees. Noop deliberately
disables notifications and provides no Dispatch/Publish method.

The poller's `WithBatchSize` option remains (default 100); it is unrelated to the
removed Redis option. Run one poller per outbox: ListUnpublished still does not
claim or lease entries. Stop the poller/producers before closing the bus.

### Preserve identity, metadata and payloads

`RemoteEvent` now embeds `Record`. Its former direct fields map as follows:
EventType → Record.Type, Occurred → Record.OccurredAt, Correlation →
Record.CorrelationID, Tenant → Record.TenantID, AggType → Record.AggregateType,
AggID → Record.AggregateID, Payload → Record.Payload. Methods continue to expose
the Event and Metadata contracts and now expose `Identified.EventID()`.

The canonical envelope has event_id, type, occurred_at, correlation_id, payload,
and optional aggregate_type/aggregate_id/tenant_id. `NewRecord(event)` copies the
payload and metadata, preserving a nonempty Identified ID or generating one.
`Record.Event()` returns an independent snapshot, and the poller uses it instead
of its old private wrapper. `Record.Validate()` requires a nonempty EventID and
valid event type; time and correlation remain host vocabulary. Existing persisted
outbox records already carry these fields and need no schema conversion.

Use EventID for deduplication, not CorrelationID: one correlation can identify
several distinct events. When retrying a publication outside an outbox, create
one Record and reuse its EventID rather than constructing a new unidentified event
for each attempt. `NewBaseEvent` creates correlation, not durable event identity.

Record JSON represents opaque payload bytes as base64. Metadata is transmitted
separately and no longer guessed from payload JSON. Custom producers must implement
Metadata or populate Record fields if remote routing needs that information.
`EventEncoder` remains available for binary payloads. RemoteEvent.Unmarshal and
TypedHandler's remote fallback decode JSON only; binary consumers must decode the
payload themselves. The Redis transport carries either without inspecting it.

### Configure reliable Redis work and lifecycle

Redis work requires **Redis 6.2 or newer** for XAUTOCLAIM. Work subscriptions accept
exact topics, validate configuration and create/reconcile the consumer group before
returning success. Instances sharing a ConsumerGroup must deploy identical handler
responsibilities for each topic; different responsibility sets need separate groups.
New/lost groups begin at the start of retained streams, so group recreation can
replay previously processed entries.

Workers defaults to four and bounds each publication/work pool; QueueSize defaults
to 1000 waiting async publications. RetryAfter defaults to one minute;
HandlerTimeout defaults to 30 seconds. Both must be positive whole milliseconds,
and RetryAfter must exceed HandlerTimeout. BlockTimeout defaults to five seconds
and also requires positive whole milliseconds. SubscribeWork reports invalid
settings as `sdk.ErrInvalidInput`. HandlerTimeout covers the entire selected-
handler attempt, including multiple callbacks. Network operation contexts are
checked before/after I/O; prompt network interruption also depends on the
caller-owned go-redis client's timeout/context configuration. One record per worker prevents
claimed batch tails from aging before processing starts.

Workers alternate new reads with idle-pending reclamation. Every selected handler
must succeed, with an uncanceled attempt, before ACK. Errors, panics, malformed or
mismatched envelopes, cancellation and no current handler leave the entry pending.
An ACK failure can also result in retry. Unsubscribe stops future topic selection;
an already-running read can still claim work, which stays pending if no handler
remains. Callbacks selected before unsubscribe can finish. A callback ignoring its
deadline cannot be forcibly stopped and can overlap a reclaimed attempt.

There is no automatic poison acknowledgment, maximum attempts or dead-letter
mechanism. Hosts monitor pending entries and own repair or explicit terminal
disposition through their Redis client. MaxLen trimming is removed because it
could destroy unacknowledged work. Hosts own retention and archival, checking all
relevant consumer groups before deleting stream records. Neither group ACK nor
bus Close deletes retained stream entries.

Memory and Redis now return `events.ErrClosed` after shutdown and
`events.ErrCapacity` on a full admission queue; both match `sdk.ErrUnavailable`.
Canceled calls fail before admission. Empty event types, reserved `"*"` event
types, empty subscription topics and nil handlers match `sdk.ErrInvalidInput`.
Memory and Redis isolate callback panics instead of terminating the process.

Close rejects new work, drains admitted publications and waits for active
callbacks. Repeated callers wait on the same completion using their own contexts;
a deadline returns the context error without claiming callbacks have stopped.
Redis stops new reads and cancels work attempts; unfinished work remains pending
for recovery. The host must close the bus before its Redis client. A callback must
not wait on its own bus's Close. Noop has no lifecycle state and its Close remains
a no-op; it is still not a checked delivery backend.

### Coordinate the Redis transport cutover

Every effective StreamPrefix gains internal `v2:`. With the default prefix,
`content.published` uses `events:v2:content.published` and pub/sub uses
`events:v2:broadcast`. New stream entries contain one serialized Record envelope.
Old streams, groups and pending entries are not automatically converted, deleted,
transferred or replayed. Upgrading Go binaries alone does not drain old work.

1. Inventory old physical streams, groups and pending entries. Stop old writers
   and drain old streams with compatible old consumers, or retain unresolved
   entries for a deliberate host repair/replay process. The old implementation
   could acknowledge failed work, so reconcile required side effects where needed.
2. Choose disjoint old/new physical prefixes and coordinate producer/consumer
   cutover. An old logical topic beginning `v2:` can overlap the new namespace;
   use a fresh host prefix if overlap is possible. Do not assume a mixed fleet
   exchanges broadcasts or shares compatible work state.
3. Replay only accounted-for work with stable EventIDs and duplicate-safe handlers.
   Old outbox Records retain their IDs; legacy Redis envelopes did not preserve
   them. Recover identity from the source outbox when available, or persist a host
   mapping from each legacy stream entry to its replay ID. Do not generate a new
   ID on every replay retry or use shared correlation as the deduplication key.
4. Keep old pending work and retained records until the host has verified delivery
   or explicitly disposed of them. Apply the selected retention policy afterward.
   Rollback resumes a different transport namespace; it does not move new work
   back to old code. Review pending state in both namespaces before rollback.

### Verification for consumers

Build, test and vet affected modules and update custom emitters/subscribers and
mocks to the new signatures. Exercise full/closed/canceled admission, repeated
Close, handler failure/panic and the required local outbox-to-job handoff. Run
custom notification buses through `eventstest.Run`. For Redis, verify cross-process
fanout separately from same-group competing work, pending recovery after failure,
unsubscribe and restart, and stable IDs/metadata on actual round trips. Exercise
the cutover in a disposable namespace before deployment; inspect old pending work
and retention explicitly. Framework results and task-relative inventory belong in
[events-implementation.md](plans/events-implementation.md); historical audit probes
use the old API and must not be run unchanged.

## AUDIT-013: File-storage correctness and direct adapter contracts

- **Implemented:** 2026-09-10.
- **Modules:** SDK, `integrations/filestorage/gcs`,
  `integrations/filestorage/s3`, and framework example wiring/documentation.
- **Release:** unreleased; no target module versions or tags selected. Coordinate
  SDK, adapter and dependent requirements when publishing. S3 adds only AWS
  `feature/s3/transfermanager v0.1.6`; GCS promotes its existing auth dependency
  to direct for synthetic credential-context tests. Existing version pins remain.
- **Impact:** removed wrapper APIs/error sentinels, stricter accepted keys,
  consistent ranges/errors, concrete resource ownership, safer upload publication,
  Disk file permissions, explicit GCS signing and distinct upload protocols.
- **Data migration:** no automatic renames or schema changes. Inventory existing
  keys, Disk symlinks/permissions/staging paths and cloud signing/session callers
  before upgrading. External consumer applications remain unchanged.

### Use the adapter directly

The SDK retains the seven-method `Storer`: Upload, Download, Delete, Exists, List,
DownloadRange and GetObjectSize. A domain can still declare a smaller interface.
The removed FileStore wrapper added delegation, optional-capability fallbacks,
error wrapping and logging; it provided no separate storage behavior.

| Previous usage | Replacement |
|---|---|
| `filestorage.FileStore`, `*filestorage.FileStore` | `filestorage.Storer`, a consumer-defined narrower interface or the concrete adapter at the composition root |
| `filestorage.New(adapter, ...)` | Pass `adapter` directly |
| `filestorage.Option`, `filestorage.WithLogger(log)` | Remove wrapper options; report returned errors at the host's chosen boundary |
| `ErrUploadFailed`, `ErrDownloadFailed`, `ErrDeleteFailed` | Preserve/check the actual error cause; the operation is already known at the call site |
| `ErrResumableNotSupported`, `ErrSignedURLNotSupported` | Type-assert the optional interface on the actual adapter before calling |
| `InitiateResumableUpload(ctx, path, contentType)` | For GCS, pass `ResumableUploadOptions{ContentType: contentType, Origin: allowedOrigin}`; see the distinct S3 operation below |
| S3 `InitiateResumableUpload(ctx, path, contentType)` | `InitiateMultipartUpload(ctx, path, contentType)`; continue the S3 part/completion/abort protocol |
| Disk construction without cleanup | Keep the concrete `*Disk` and call `Close()` after its users stop |

Composition-root wiring uses the real adapter:

```go
// import "github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
disk, err := filestorage.NewDisk(mediaDir)
if err != nil {
    return err
}
var blobs filestorage.Storer = disk
// Pass blobs to the application's storage consumers.
// After handlers/workers stop and their returned readers are closed:
return disk.Close()
```

Keep the concrete handle in the host for shutdown. Storer deliberately has no
Close method: Disk and GCS own resources; an injected S3 client's resource
lifecycle remains with its host. Do not add fake Close implementations to custom
stores. Disk.Close releases its root handle without draining operations or
closing previously returned readers. A racing Close can prevent upload cleanup.
Hosts must stop users first. GCS users likewise finish operations before Close;
host-supplied transports retain their host-owned lifecycle.

There is no automatic SDK storage logging after removing WithLogger. Preserve
operational reporting in the host, avoiding duplicate logs for the same error.
The pinned AWS transfer manager itself logs multipart completion failures;
the framework adapter adds no logging. Never log signed URLs or resumable
session URIs, which authorize access to their objects.

### Preserve keys and inspect existing storage

All object methods now require a nonempty relative UTF-8 key separated by `/`.
Leading/trailing slashes, empty segments, `.`/`..` segments, backslashes and NUL
are invalid. `../name` no longer aliases `name`, and `/name` no longer silently
becomes a relative key. Spaces, Unicode, `.segovia/boot-probe` and names such as
`two..dots` remain valid. There is no cleaning, slugging, trimming, case folding
or Unicode normalization. Hosts must not mechanically slug existing keys during
migration; that changes object identity.

List uses a **literal prefix**, including a partial final component. Empty prefix
selects all objects; `im` matches both `image.txt` and `img/a.txt`; `.` matches
`.segovia/boot-probe`. A trailing slash is allowed. Complete directory segments
still follow the key rules. Results contain caller-facing keys, omit directory
markers, have no order guarantee and are materialized in memory. GCS removes its
configured prefix from returned keys. The adapters report incompatible listed
keys instead of returning keys that their other methods reject. S3 queries the
containing prefix and filters locally for a partial final `.` or `..`, because
MinIO rejects those exact query values; those queries can scan more objects.

GCS Config.Prefix is now a validated directory key: an absent trailing slash is
added, but leading/repeated slashes and dot/dot-dot segments are rejected.
Do not rely on the old trimming behavior. S3 has no new generic Prefix setting.
Application bucket selection and key namespaces remain host policy.

Disk now confines filesystem operations with `os.Root`, rejects symlink
components and non-regular objects, and treats directories as invalid objects.
The host must still own and trust the tree: hard links, mounts/device changes
and malicious mutations inside the root are outside this guarantee. Platform
case collisions and file/directory prefix conflicts remain filesystem limits.
Uploads that cannot rename across a filesystem boundary fail rather than copy
partially into their final destination.

Disk reserves **only its top-level `.gopernicus-tmp` directory**, including case
variants such as `.GOPERNICUS-TMP`, for staging;
objects using that key/subtree are rejected and staging contents are omitted
from List. This is a Disk-specific restriction, not a globally reserved cloud
namespace. Inventory any existing path with that name or case variant before
upgrade. Startup
checks the staging directory but does not sweep files, because another live
instance may own them. After a crash, arrange cleanup only after establishing
that no active uploader owns the files.

New and replacement Disk uploads create their final files with mode **0600**
(subject to the platform), including overwrites of formerly group/world-readable
objects. Atomic replacement publishes a new private inode instead of preserving
the old object's mode. Hosts relying on a separate OS user or group reading
these files must deliberately adapt their serving/permission policy before
upgrading. Existing untouched object permissions are not rewritten.

### Upload publication, byte ranges and errors

Upload still accepts arbitrary streaming `io.Reader` values and never requires
seeking or closes the input. A successful call deliberately replaces an existing
object, preserving archival/replay workflows. A source error or cancellation
must abort uncommitted data, retain the previous object and preserve its error
chain. Disk now copies into a confined temporary file and checks copy, close and
context errors before rename. GCS cancels the child upload context before writer
cleanup after a source failure, instead of finalizing a partial prefix.

S3 uses the AWS transfer manager with bounded 5 MiB parts and two concurrent
requests. **All Upload calls are limited to 10,000 parts, about 48.8 GiB**, even
when the original reader is seekable. Use a deliberate host provider upload path
with larger parts for larger objects. Failed multipart uploads attempt abort
with a separate 30-second cleanup deadline, so caller cancellation does not
suppress cleanup. Returned errors preserve source, cancellation, provider and
cleanup causes. An abort failure can leave incomplete parts; host bucket
lifecycle policy and operational repair remain relevant.

Cancellation is cooperative: an arbitrary blocked source Read cannot be forcibly
interrupted. Neither an error racing a remote completion nor cancellation after
publication proves rollback. Disk's atomic publication does not promise fsync
or power-loss durability. It also needs temporary capacity for the replacement
while the previous object remains present.

Download, DownloadRange and GetObjectSize now refer to **stored bytes**, including
stored compression. GCS reads the compressed representation rather than silently
transcoding it; hosts that relied on decompression must do it explicitly and
choose appropriate HTTP content headers. Content metadata, cache headers and
CDN/bucket policy remain application concerns.

Range offset must be nonnegative. Length is `-1` for the remainder or a
nonnegative count; finite inclusive-end overflow is invalid. Other negative
lengths no longer mean the remainder. Zero-length reads confirm existence and
return an empty reader; offsets at or beyond EOF do the same. Reads truncate
at EOF. Missing objects still fail, and permission/configuration failures remain
errors while normalizing provider range responses. Independent size and read
requests are not a consistent snapshot during concurrent replacement.
Callers close every successfully returned reader, including empty readers.

`ErrObjectNotFound` now matches `sdk.ErrNotFound`, so the standard web domain-error
responder returns HTTP 404 instead of treating an absent storage object as an
unknown 500. `ErrInvalidPath` matches `sdk.ErrInvalidInput`. Invalid ranges and
expiry also match sdk.ErrInvalidInput. The public helpers ValidatePath,
ValidatePrefix, ValidateRange and ValidateExpiry expose shared adapter validation.
Source and provider causes remain available through errors.Is/errors.As.
Delete remains idempotent for missing objects. S3 preserves explicit NoSuchBucket
and other provider errors; a bare HEAD 404 cannot distinguish an absent key from
an absent bucket. Do not infer certainty from information the provider omits.

### Configure optional capabilities explicitly

Assert `SignedURLer` or `ResumableUploader` on the real adapter. Disk implements
neither; GCS implements both; S3 implements only SignedURLer. The old wrapper
structurally advertised optional methods even when its backend could not use them.

SignedURL now requires **whole seconds from one second through seven days**.
Zero, negative, fractional-second and longer durations are rejected instead of
receiving inconsistent provider defaults or invalid URLs. Signing checks caller
cancellation; credential/IAM I/O uses its context. Credential expiration can
shorten URL validity. A signed URL does not prove the object exists.

GCS signing has two explicit configurations:

| Host configuration | SignedURL behavior |
|---|---|
| `Config.SigningServiceAccount` set to the service-account email or unique ID | IAM SignBlob with the configured authenticated HTTP client and caller context; host supplies appropriate signing permission |
| SigningServiceAccount empty and `Config.CredentialsJSON` contains a service-account identity and private key | Local signing with that identity/key |
| ADC or vendor-only credentials, with neither explicit signing configuration | SignedURL returns a configuration error matching sdk.ErrInvalidInput; no implicit metadata/signing-identity discovery |

Explicit SigningServiceAccount selects IAM even if CredentialsJSON also contains
a private key. `WithClientOption` remains available for vendor credentials,
scopes, quota project, custom HTTP client and endpoint settings; credentials
provided only through those options do not implicitly configure local signing.
Open may perform credential discovery I/O, so give startup a deadline. The
configured authenticated HTTP client is retained for storage, IAM and resumable
requests. A host-provided legacy TokenSource or transport retains its own
cancellation behavior; arbitrary custom implementations cannot be made
interruptible by passing a context alone.

GCS resumable initiation now calls the **authenticated JSON upload API** through
that retained client. It no longer signs a POST URL and then uses http.DefaultClient.
Upload credentials are sufficient; URL-signing configuration is not required.
The SDK signature is:

```go
sessionURI, err := uploader.InitiateResumableUpload(ctx, key,
    filestorage.ResumableUploadOptions{
        ContentType: "application/octet-stream",
        Origin: allowedBrowserOrigin,
    })
// Return sessionURI only to the authorized client; it uploads with PUT.
```

The host authorizes Origin and configures the bucket's CORS policy; copying an
untrusted incoming Origin is not authorization. Session completion/cancellation
and browser error handling remain host/provider responsibilities. An initiation
without a caller deadline uses the adapter's 15-minute bound.

S3 `InitiateMultipartUpload` returns an upload ID, not a URL. Use that ID with
S3 UploadPart and CompleteMultipartUpload, or AbortMultipartUpload, using the
same bucket and key. It no longer satisfies ResumableUploader. Do not send its
ID to a generic client expecting a PUT session URI. The host owns any required
part signing and multipart lifecycle; no generic multipart framework was added.

### Find consumer callers and verify the migration

Search imports, aliases, mocks and wiring for sdk/capabilities/filestorage,
FileStore/New/WithLogger, removed sentinels and resumable method signatures.
Inspect custom adapters against the updated `filestoragetest.Run` suite; its
factory must supply isolated fresh storage for each test. Keep concrete resource
handles in the composition root and preserve source error chains in outbound
wrappers.

Read-only consumer anchors from the audit (recheck their current revisions):

- **Segovia v2**, `/Users/jrazmi/code/segovia/segovia/v2`, main/76b3d78,
  SDK v0.8.0: `cmd/server/filestorage.go`, `cmd/server/dashboards.go` and
  `internal/outbound/domains/dashboards/content.go` need facade migration.
  Preserve `.segovia/boot-probe`, generic non-seekable bundle readers, and
  errors.Is recognition of ErrBundleTooLarge. Storage is actively used by
  `internal/logic/domains/dashboards/service.go`; its startup comment suggesting
  otherwise was stale.
- **GPS360**, `/Users/jrazmi/code/gps/three-sixty/gps-360-go`, main/e1ab3f0,
  SDK v0.7.1: inspect `cmd/server/filestorage.go`, `cmd/workers/comps/main.go`
  and host `integrations/filestorage/{gcs,s3}`. Comps replays intentionally
  overwrite a stable archive key; do not turn the upgrade into create-only
  storage or silently rename race-derived keys.
- **Coordination Hub**, `/Users/jrazmi/code/gps/coordination-hub`, main/84ff08a,
  SDK v0.7.0: `integrations/objectstore/objectstore.go` uses its own S3 Bucket
  interface. Its public/private bucket selection, MIME/cache metadata and CDN
  URLs remain host policy; it is not a mechanical FileStore migration.

No external consumer was modified. The original framework was consulted for
intent, not restored as a dependency. CMS media metadata/byte reconciliation
and its total upload request-size limit remain the later pocket audit; this
change does not provide a cross-storage transaction or a request-body policy.

Before deployment, build/test/vet affected modules, run the shared conformance
suite on custom backends, exercise failed replacement and cancellation with real
streams, check not-found HTTP behavior, permissions and key inventory, and verify
browser sessions and signing with the host's actual credentials/CORS settings.
Inspect temporary/incomplete uploads and resource shutdown after failure.

Framework verification passed: full 42-module build/test/vet and 23 guards,
documentation typecheck/build, targeted SDK/adapter race tests, real Disk/HTTP
behavior, expanded MinIO conformance and full GCS emulator tests. MinIO exercised
non-seekable multipart publication, a failed replacement retaining original bytes,
no abandoned multipart uploads, signed GET and explicit multipart initiation.
GCS exercised the shared storage contract and session initiation → client PUT →
download/size, with synthetic IAM/credential-context tests. Simultaneous source
failure/cancellation preserves both causes on all three backends. Exact commands,
logs and limits are in [filestorage-implementation.md](plans/filestorage-implementation.md).
Unrelated live datastore suites remain skipped in the workspace gate. Local
emulation does not verify actual AWS/GCS persistence, cloud IAM permissions or
browser CORS; external consumers still need their own upgrade checks. Historical
audit probes used the old APIs and must not be rerun unchanged.

## AUDIT-014: Explicit notification deliveries and email correctness

- **Implemented:** 2026-09-10.
- **Modules:** SDK, authentication, SendGrid; dependent CMS and example imports.
  The integrations/notify/mailer module is removed.
- **Release:** unreleased; target versions have not been selected. Upgrade related
  module requirements together when releases are available.
- **Impact:** breaking imports, notification/rendering APIs, authentication wiring,
  accepted email input, template output, transport behavior and error diagnostics.
- **Data migration:** none. Authentication's queued Kind/Purpose/envelope schema,
  sealed payloads and worker checkpoint behavior are unchanged.

### Select deliveries on every call

The host decides which senders fire for each action. notify.Send accepts prepared
Delivery values, each with Send(context.Context) error. Preparing one performs
no I/O. Email keeps its typed Message and Sender; email.NewDelivery binds the
sender and message, snapshots recipients, and preserves HTML and text.

```go
import (
    "context"

    "github.com/gopernicus/gopernicus/sdk/capabilities/notify"
    "github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

// mailSender and slackSender are chosen/configured by the host.
// SlackMessage and slackSender.Send are host APIs, not new SDK types.
err := notify.Send(ctx,
    email.NewDelivery(mailSender, outageEmail),
    notify.DeliveryFunc(func(ctx context.Context) error {
        return slackSender.Send(ctx, outageSlackMessage)
    }),
)

// A reset selects only email, even when this host also has a Slack sender.
err = notify.Send(ctx, email.NewDelivery(mailSender, passwordResetEmail))
```

Calls run sequentially in argument order. An ordinary delivery error does not
prevent later selections from running. When the caller's context is canceled,
remaining deliveries are skipped; a provider's own cancellation error does not
cancel a healthy caller context. A final successful acknowledgement is preserved.
Empty selections fail with sdk.ErrInvalidInput. Nil deliveries fail at their
positions; do not pass typed-nil implementations.

Partial failure returns *notify.SendError. Use errors.As to inspect Failures:
Index is the original zero-based argument position, Attempted says whether Send
was called, and Err preserves that delivery's cause. Positions absent from
Failures returned nil. errors.Is/errors.As traverse all causes. The error summary
contains positions, without recipients, content or provider response text.

Keep the original selected deliveries if a host workflow needs to map failures
back to its actions. Retrying the whole call can resend successful deliveries.
Provider acceptance is not proof of receipt, and an error is not proof of
non-delivery. Queues, retries, idempotency, recipient eligibility and concurrency
policy remain host/pocket concerns. Prepared closures are in-memory values;
queue data and reconstruct a delivery inside a worker, as authentication does.

### Replace imports and the old notification surface

| Before | After |
| --- | --- |
| sdk/capabilities/email | sdk/capabilities/notify/email |
| notify.Notifier with Kind()/Notify(ctx, sdk.IdentityAddress, notify.Message) | notify.Delivery with Send(ctx), or DeliveryFunc wrapping a typed host send |
| notify.Message{Subject, Body} | channel-specific content, such as email.Message |
| integrations/notify/mailer.New(...) | email.NewDelivery(sender, message) per attempt |
| email/notify duplicated capability types | notify.Capabilities, CapabilityReporter, TransportSecurity, TransportPosture |
| email.InspectSender / notify.InspectNotifier | notify.InspectTransport |
| email.CheckSender / notify.CheckNotifier | notify.CheckTransport |
| email.ErrInsecureTransport | notify.ErrInsecureTransport |
| notify.NewConsole(kind, logger) | notify.NewConsole(logger), then Send(ctx, destination, body) |

Remove the bridge module from go.mod/go.work and migrate aliases, mocks and
production metadata methods. SendGrid stays at integrations/email/sendgrid.
Email remains a subpackage of the stdlib-only SDK, not a new Go module. No generic
SDK identity-kind registry or global sender list replaces the old API.

Production checks still reject undeclared or development-only transports. Custom
senders opt in structurally by returning notify.Capabilities; their declarations
must describe actual configuration. Typed nil reports undeclared. Prepared email
delivery forwards metadata only when the sender declares it. A DeliveryFunc has
no implicit posture; check the underlying transport at host construction.

### Authentication wiring

Config.Mailer is the only email transport. Replace Config.Notifiers with
Config.BodySenders, keyed explicitly by non-email identifier kind:

```go
cfg.Mailer = mailSender
cfg.BodySenders = map[string]authentication.BodySender{
    "phone": smsSender,
}
```

BodySender is a narrow authentication interface:
Send(ctx context.Context, destination, body string) error. Adapt an existing SMS
provider's Notify method to it and keep its capabilities method if used in
production. The map is copied at construction. Blank/whitespace kinds, "email",
nil and typed-nil values are invalid; ErrDuplicateNotifierKind is removed.

An email-kind entry is rejected because routing email through a body-only sender
would discard HTML. Authentication selects exactly one delivery for the command's
kind, even when several transports are configured. Email carries Subject, Body
and HTML through the Mailer. Other kinds receive their rendered body and resolved
destination. Config.MailFrom, password policy, delivery mode, queue/checkpoint
rules and recipient eligibility keep their existing owners and behavior.

Authentication now embeds deliberate .txt counterparts for all ten core email
content templates. Host HTML overrides can keep using core text when the purpose
exists there; provide a matching text override when changing meaning or links.
The HTML output goldens remain stable; plain text intentionally changes.

### Rendering migration

Renderer is now a concrete render-only type constructed without a sender:

```go
renderer, err := email.NewRenderer(
    email.WithContentTemplates("app", templatesFS, email.LayerApp),
    email.WithBranding(branding),
)
if err != nil {
    return err
}
htmlBody, textBody, err := renderer.Render(email.RenderRequest{
    Template: "app:reset",
    Subject:  subject,
    Data:     templateData,
    Layout:   email.LayoutTransactional,
})
if err != nil {
    return err
}
err = notify.Send(ctx, email.NewDelivery(mailSender, email.Message{
    From: from, To: []string{recipient}, Subject: subject,
    HTML: htmlBody, Text: textBody,
}))
```

email.New(sender, defaultFrom, options...) and Emailer.RenderAndSend remain available
for templated email-only workflows. Pass Layout on SendRequest. Its Render method
also takes RenderRequest. Migrate the old Renderer interface, positional Render
arguments, RenderOption/RenderConfig/DefaultRenderConfig/ApplyOptions and
WithLayout. Define a small consumer-owned rendering interface if one is needed.
WithLogger is removed; host code handles logging.

TemplateRegistry and runtime registry mutation are private. Constructor options
are opaque values: custom Option callbacks and applying options to an existing
renderer are no longer supported. Build a new renderer for new configuration.
WithContentTemplates(namespace, fs.FS, layer), WithLayouts(fs.FS, dir, layer) and
WithBranding remain useful construction inputs. Branding and SocialLinks are
copied; caller mutation after construction no longer changes rendered messages.
Caller Data must be safe for concurrent reads when sharing a renderer.

HTML content/layouts use html/template; .txt content/layouts use text/template.
Plain-text URLs retain '&' and other literal characters instead of HTML escaping.
Each rendered content name requires HTML and text; the old stripped-HTML fallback
is removed. Missing content, a broken text template and an explicitly unknown
layout return errors. An omitted Layout still selects transactional. Subject is
passed explicitly to layouts; Data stays available as .Data for structs and maps.

Content files live under templates/. Basenames produce namespace:name and
namespace:name.text. Write a bare template body, or use those exact named roots
in {{define ...}}. Layout roots are layout:transactional and
layout:transactional.text (or the chosen layout basename). Empty roots, including
files that define only a different name, fail during construction. Do not rename
.txt roots to .txt: their executable-name suffix is .text.

App > Core > Infra priority remains. Content alternatives resolve independently;
layout selection takes the highest-priority whole pair. An HTML-only layout
keeps text content unwrapped, so ship both formats when both should be branded.
Invalid layers/namespaces and duplicate same-layer basename/format registrations
now fail instead of being ignored or silently replacing another file. Filesystem
and parsing errors leave no partially published renderer.

### Email input and transport changes

Every From and To value must be one bare mailbox, without a display name, address
list, surrounding whitespace or line breaks, and at most 254 bytes. Invalid or
empty To members now fail. Subject rejects CR/LF and control characters except
tab; Subject/Text/HTML must be valid UTF-8. Nonblank subject and text remain
required, and all validation failures match sdk.ErrInvalidInput without echoing
unsafe input. Custom senders should honor Message.Validate too. Every To recipient
is visible to the others; use separate messages for private fan-out.

SMTP honors pre-cancellation, context-aware dialing and cancellation during SMTP
I/O. SMTPConfig.Timeout bounds each attempt (zero means 30 seconds; negative fails).
It MIME-encodes Unicode subjects, folds headers and uses quoted-printable bodies,
including both multipart alternatives. Non-ASCII mailboxes require SMTPUTF8 from
the server. A failed body write aborts the connection without submitting partial
DATA. Once DATA is acknowledged, a later QUIT cleanup error does not turn the send
into a retryable failure. Transport and context causes are preserved together.

SMTP still permits the host's chosen private/plain relay and uses opportunistic
STARTTLS. It now declares TransportSecuritySTARTTLS to describe that behavior;
CheckTransport does not enforce mandatory TLS. Hosts requiring mandatory encrypted
delivery must choose/enforce that policy in their transport configuration.
Console senders honor cancellation. email.NewConsole(nil) now uses slog.Default,
matching notify.Console, so use an explicit discard logger if silence is intended.
Both console senders remain development-only.

SendGrid now creates independent per-call HTTP requests. Config.HTTPClient is an
optional host input; the client is copied and redirects are disabled on the copy.
The transport remains shared and host-owned. Default timeout is 30 seconds.
Host overrides must be HTTP(S) origins without credentials/query/fragment/path
beyond '/'. Invalid config reports sdk.ErrInvalidInput before sending and declares
development-only posture. HTTPS declares TLS; local HTTP is development-only.

Non-2xx responses return *sendgrid.ResponseError{StatusCode}, reachable through
errors.As. Error strings no longer contain the provider's response body, and
unused response bodies close without reading. Existing mappings remain 400 →
sdk.ErrInvalidInput, 401 → sdk.ErrUnauthorized, 403 → sdk.ErrForbidden, 404 →
sdk.ErrNotFound. These credential errors describe the provider, not the end user.
Do not parse old error strings or present provider-auth failures as user-auth
failures. Raw underlying custom transport errors still need host handling.

CMS contact inquiries continue to persist before notifying the operator. Unsafe
names used in email subjects now fail delivery safely, leaving the inquiry saved.
The SendGrid response-body correction closes the confirmed provider-400 leakage
path without redesigning CMS's broader public error/workflow policy.

### Consumer checks and verification

Search source, templates, mocks and wiring for the old imports and APIs above.
Read-only consumer snapshots from the review (recheck current revisions): Segovia
v2 main/76b3d78 uses plain mail plus authentication; Coordination Hub main/84ff08a
uses Emailer, HTML/text content, app layouts and branding; GPS360 main/e1ab3f0
constructs a presently unused mailer and uses authentication DeliveryOff/Console.
They pinned different older SDK/authentication versions, so apply preceding AUDIT
entries too. Hub's custom layout root names need special attention. Provider,
From, digest/retry scheduling and domain authorization remain host choices.

Before upgrading a consumer, build/test/vet affected modules, render each actual
HTML/text template and follow its generated links, and exercise its chosen
email-only and multiple-delivery actions with owned sinks. Check partial failures,
cancellation and retry selection. Verify deployed SMTP TLS/auth or provider
credentials separately; no real email, Slack or SMS was sent during this audit.

Framework commands, results, source reviews and their limits are recorded in
[email-notify-implementation.md](plans/email-notify-implementation.md). Real SMTP
protocol exchanges used owned loopback peers; concurrent SendGrid, redirects and
response errors used owned HTTP/fake transports. External consumers were not
modified, and full CMS/pocket workflow audits remain later work.

## AUDIT-015: Bound OAuth flows and truthful tracing

- **Implemented:** 2026-09-10.
- **Modules:** SDK; OAuth Google/GitHub; authentication; tracing/otel; example hosts.
- **Release:** unreleased; coordinate dependent module requirements when publishing.
- **Impact:** breaking provider/service APIs, explicit host email trust, stricter configuration and callback proof, optional native routes, sampling and HTTP telemetry changes.
- **Data migration:** no repository interface/schema change. Old in-flight authorization-code logins must restart. Existing pending-link secrets, accounts, sessions and encrypted provider tokens retain their formats.

### Provider contracts and construction

The core `oauth.Provider` has `Name`, `GetAuthorizationURL`, `ExchangeCode` and
`GetUserInfo`. Replace positional authorization arguments with:

```go
authorizeURL, err := provider.GetAuthorizationURL(oauth.AuthorizationRequest{
    State: state,
    CodeVerifier: verifier,
    Nonce: nonce,
    RedirectURI: callbackURI,
})
```

Handle the returned error. State must be nonempty, PKCE verifiers contain 43–128
unreserved characters, and redirects must be absolute without credentials or
fragments; HTTP(S) requires a host.

Remove `SupportsOIDC`, `TrustEmailVerification` and SDK `ProviderConfig`.
Type-assert optional `oauth.IDTokenValidator` and `oauth.TokenRefresher` instead.
**Delete unsupported stub methods**, including from mocks: merely having a
`ValidateIDToken` method advertises OIDC. An OIDC start must receive a validated
ID token at completion; missing ID tokens never fall back to userinfo. An ID
token from a provider without a validator is also rejected.

Construct adapters with concrete config:

```go
googleProvider, err := google.New(ctx, google.Config{
    ClientID: clientID, ClientSecret: clientSecret, HTTPClient: httpClient,
})
// Check err; discovery uses ctx, so give it a deadline.

githubProvider, err := github.New(github.Config{
    ClientID: clientID, ClientSecret: clientSecret, HTTPClient: httpClient,
})
// Check err; GitHub validates local credentials without network I/O.
```

Scopes and client configuration are copied; transports remain shared. Defaults
are Google `openid email profile`, GitHub `user:email`, and a 30-second client
when nil. Google custom scopes require `openid`. For the former Google refresh
consent behavior, explicitly set `AccessType: "offline", Prompt: "consent"`;
the new defaults omit both parameters. Provider registration/grant policy still
determines token availability. Empty Google client secret is allowed when the
registered public client supports it; GitHub requires both credentials.

Redirects are refused, including Google discovery/JWKS. Responses over 1 MiB,
malformed token successes, blank stable IDs and nonpositive GitHub IDs fail.
Token access/type fields are required and negative expiry is invalid. Google
requires subject, rather than email, in validated ID tokens. GitHub missing email
permission (403) or absent email leaves a valid stable identity usable without
verified email evidence; other email endpoint errors still fail.

Use `errors.As` to inspect `*oauth.Error` for status, operation and remote code;
`errors.Is` retains wrapped causes/cancellation. Its string omits remote bodies
and descriptions. Do not log unwrapped causes or remote codes as trusted text.

### Host email trust

`EmailVerified` preserves provider evidence; new `EmailAuthoritative` describes
whether the provider controls the mailbox namespace. Google sets authority for
verified Gmail or Workspace (`hd`) identities; third-party `email_verified`
alone is insufficient. GitHub does not assert authority.

Authentication now requires explicit `Config.TrustOAuthEmail` **and** verified
email evidence for new email-based registration/adoption. A conservative policy:

```go
cfg.TrustOAuthEmail = func(provider string, identity oauth.UserInfo) bool {
    return provider == "google" && identity.EmailAuthoritative
}
```

Nil denies those paths. Hosts may choose a different policy deliberately.
Existing linked provider-ID login and explicit session-gated linking do not need
email or this callback. When another account already claims the email, the
existing mailed possession proof and adoption revocation still apply. Provider
success payloads are validated before any account lookup/write.

### Browser and native flow migration

`StartOAuth(ctx, provider, OAuthStartRequest)` and
`StartLink(ctx, userID, provider, OAuthStartRequest)` return `OAuthStart`, containing
`AuthorizationURL`, public `State`, independent `FlowSecret` and `ExpiresAt`.
`OAuthCallback(ctx, provider, OAuthCallbackRequest)` requires mode, code, state
and proof. `Mode` must explicitly be `OAuthBrowser` or `OAuthNative`.

```go
flow, err := svc.StartOAuth(ctx, "google", authentication.OAuthStartRequest{
    Mode: authentication.OAuthNative,
    RedirectURI: "com.example.app:/oauth",
})
// Check err; retain flow.FlowSecret locally, separately from the callback URL.
// Open flow.AuthorizationURL in the system browser, then receive code/state.
result, err := svc.OAuthCallback(ctx, "google", authentication.OAuthCallbackRequest{
    Mode: authentication.OAuthNative,
    Code: code, State: state, FlowSecret: retainedFlowSecret,
})
```

The repository lookup is a domain-separated, framed SHA-256 digest binding
provider, mode, public state and independent 256-bit proof. Wrong proof, provider
or mode does not consume a valid flow. Stored state supplies the exact redirect
URI during exchange; callback callers cannot replace it. State/proof inputs are
bounded canonical base64url values. Successful consumption remains single-use.

Bundled browser start/link handlers set a per-state host-only HttpOnly cookie,
SameSite=Lax and Path `/`, with Secure/`__Host-` on HTTPS. They do not inherit
session cookie domain/path. The browser callback must carry its initiation
cookie; copied callback URLs alone fail. Parallel flows are independent. Keep
one cookie jar through start/callback in scripts and browser tests. Only
login/register sets session cookies; normal application redirects remain.

Native routes are opt-in through exact `Config.OAuthNativeRedirectURIs`:

| Route | JSON request / response |
| --- | --- |
| `POST /auth/oauth/{provider}/native/start` | `{redirect_uri}` → `{authorization_url,state,flow_secret,expires_at}` |
| `POST /auth/oauth/{provider}/native/complete` | `{code,state,flow_secret}` → `{action,access_token?,refresh_token?,token_type?}` |
| `POST /auth/oauth/{provider}/native/link/start` | same start body; requires the person's access token |
| `POST /auth/oauth/native/verify-link` | `{token}` from the separately delivered pending-link proof → action and credentials |

Native endpoints use no cookies or credential redirects and set `no-store`.
When credentials are present, token type is `Bearer`; pending linking returns an
action without tokens. The host must register allowed callbacks and a compatible
client type with the provider. Exact matching supports configured loopback
IP/ports, private app schemes and claimed HTTPS links. Automatic arbitrary
loopback ports and OAuth device authorization are not implemented.

Provider wiring rejects nil/typed-nil, duplicate or invalid names.
`OAuthCallbackBase` is required with providers: an HTTP(S) origin with optional
clean mount prefix, no trailing slash/query/credentials/fragment, HTTPS in
production. Native HTTP callbacks require a loopback IP. Use the shared service
from custom transports without cookies; authorize `StartLink` user IDs in the
calling host.

### Tracing migration

SDK HTTP middleware finishes in a defer, reports response/write/flush failures
and escaping panics with safe categories, and preserves Logger's original cause.
A partial response keeps its committed status; panic/abort/hijack before headers
has no invented status. Escaping panic values are rethrown unchanged.

OTLP `SampleRate: 0` now means **zero root sampling**; set `SampleRate: 1`
explicitly in literals that previously relied on zero meaning full sampling.
Environment loading still defaults to one. Ratios must be finite and in [0,1].
Sampling is parent-based: a sampled/unsampled parent takes precedence over the
root ratio. Stdout samples roots and respects parents; supplied providers retain
their own sampler and lifecycle.

Use `otel.Middleware(tracer, otel.HTTPConfig{})` for HTTP server spans with typed
metadata; ordinary `StartSpan` remains an internal operation. Mount inside
routing, outside `web.Logger` and `web.Panics`. Optional SDK `HTTPTracer` and
`HTTPSpan` interfaces provide the narrow metadata seam. Other tracers can keep
the generic SDK middleware and its string attributes.

`TrustTraceContext: true` explicitly accepts W3C traceparent/tracestate, including
parent sampling decisions. Default false ignores those headers and retains any
existing context parent. No global propagator/provider or automatic baggage is
installed. Host policy chooses trusted inbound callers.

Update dashboard keys/types when switching middleware: `http.request.method`,
integer `http.response.status_code`, `server.address`/integer `server.port`,
`network.peer.address`/integer `network.peer.port`, and method-free `http.route`.
Raw URLs, queries, bodies and user-agent are omitted; unknown routes never use
raw paths. `error.type` is the fixed `http.request_failed` category. This is a
bounded HTTP metadata subset, not full semantic-convention coverage.

### Known consumers and verification

Segovia v2, coordination-hub and gps-360-go use the Google constructor through
authentication. Migrate constructors and deliberately choose host email trust,
callback configuration and provider consent options. Custom start/callback
callers must retain and submit proof; bundled browser routes handle it. Consumer
repositories were inspected, not edited. Original Gopernicus informed native
flow intent without copying its former callback security model.

All 41 workspace modules pass build/test/vet, tagged compile checks, generated
artifact checks and architecture guards. Focused race tests pass for SDK OAuth,
tracing/web, both providers, OTel and authentication. Owned HTTP tests cover a
stolen browser callback followed by legitimate completion, concurrent cookies,
native cookie-free pending-link completion, replay and proof/mode/URI checks.
Locally signed OIDC/fake HTTP and in-memory tracing tests cover malformed
identities, trust, redirects, safe errors, cancellation, partial responses,
propagation and sampling. Live Google/GitHub login, real delivery/collectors and
mobile OS callback dispatch were not exercised. See the
[implementation plan](plans/oauth-tracing-implementation.md) for exact commands,
file inventory, documentation checks and remaining deployment validation.


## AUDIT-016: Shared pockets module

- **Implemented:** 2026-09-10.
- **Modules:** SDK; new `github.com/gopernicus/gopernicus/pockets`; concrete
  pockets, their dependent adapters/examples, and Workshop templates.
- **Release:** unreleased. Shared-module requirements use the intended initial
  `pockets/v0.1.0` release. No tags or publication occurred; compatible SDK,
  concrete-pocket and adapter release versions must be coordinated.
- **Impact:** breaking import path and default package qualifier; new Go module
  dependency. Existing concrete pocket module paths are unchanged.
- **Data migration:** none. Route, middleware, logger and event behavior unchanged.

### Consumer migration

Replace the old `github.com/gopernicus/gopernicus/sdk/pocket` import with
`github.com/gopernicus/gopernicus/pockets`. Its package name is `pockets`:

| Old name | New name |
| --- | --- |
| `pocket.Mount` | `pockets.Mount` |
| `pocket.RouteRegistrar` | `pockets.RouteRegistrar` |
| `pocket.PrefixRegistrar` | `pockets.PrefixRegistrar` |
| `pocket.Group` | `pockets.Group` |

```go
import "github.com/gopernicus/gopernicus/pockets"

mount := pockets.Mount{
    Router: pockets.Group{Prefix: "/api", Next: router},
    Logger: log,
    Events: bus, // optional SDK events.Emitter; existing semantics
}
if err := service.Register(mount); err != nil {
    return err
}
```

Update custom registrar signatures, embedded types, tests, helpers and any
hand-maintained scaffolds. An explicit import alias can retain the old local
qualifier during consumer migration; the old SDK package itself is removed and
has no forwarding shim. SDK never imports the new shared module.

Add the shared module to every consumer module that imports it. Concrete cores
may require SDK and this exact shared module; importing a different concrete
pocket is still disallowed. Child modules such as `pockets/jobs` and
`pockets/authentication` remain independent and opt-in. Importing shared pockets
selects only SDK, not the concrete pockets or their drivers/views.

Until compatible releases are published, resolve the updated framework through
local workspace entries or explicit replacements. A standalone host needs its
own replacements; those in dependency go.mod files do not propagate. Include
both SDK and shared pockets, plus each concrete pocket/adapter used from the
updated checkout. For example, add these directives to the host go.mod:

```go
require github.com/gopernicus/gopernicus/pockets v0.1.0

replace github.com/gopernicus/gopernicus/pockets => /path/to/gopernicus/pockets
replace github.com/gopernicus/gopernicus/sdk => /path/to/gopernicus/sdk
```

A workspace should list both `./pockets` and each concrete pocket/adapter module
being developed; parent `go test ./...` does not cover nested modules. After
release, use compatible tagged requirements, remove local replacements, and run
`go mod tidy`, build/test/vet in each consumer module.

The repository aligned four existing SDK minimum-version mismatches exposed by
standalone checks: jobs-minimal now requires SDK v0.7.1, and authentication's
pgx/turso stores and goth views require SDK v0.6.0. No third-party dependency
versions changed. These pins describe the local module graph; confirm compatible
published versions when releasing this unreleased audit train.

### Behavior and audit scope

This relocation preserves all four types, Mount fields, method signatures,
prefix concatenation, middleware ordering, and optional registration semantics.
CMS received only import/module migration. The separate jobs handler-snapshot,
events cleanup/middleware ownership, logger and registration audit findings are
still pending; this entry does not claim those fixes shipped.

The workspace has 42 modules. Full build/test/vet and architecture checks,
standalone module resolution, generated pocket/store scaffold compilation,
shared-contract race tests and actual in-process HTTP mounting are recorded in
[pockets-module-move.md](plans/pockets-module-move.md). No external consumer
repositories or production services were changed. Module-proxy installation of
unpublished versions and live adapters were not verified.


## AUDIT-017: SDK foundation directory renamed to pkg

- **Implemented:** 2026-09-10.
- **Module:** `github.com/gopernicus/gopernicus/sdk`; all importing framework
  modules, host examples and Workshop templates are migrated locally.
- **Release:** unreleased; target SDK version has not been selected. No tags or
  publication. Coordinate compatible SDK and dependent module releases.
- **Impact:** breaking Go import paths. Package identifiers, exported APIs,
  configuration and runtime behavior are unchanged.
- **Data migration:** none.

### Consumer migration

Replace the import prefix `github.com/gopernicus/gopernicus/sdk/foundation/`
with `github.com/gopernicus/gopernicus/sdk/pkg/` in host code, tests, scaffolds
and generated-code sources. Regenerate outputs through their owning generator.

| Old SDK package | New SDK package |
| --- | --- |
| `sdk/foundation/async` | `sdk/pkg/async` |
| `sdk/foundation/cryptids` | `sdk/pkg/cryptids` |
| `sdk/foundation/environment` | `sdk/pkg/environment` |
| `sdk/foundation/list` | `sdk/pkg/list` |
| `sdk/foundation/logging` | `sdk/pkg/logging` |
| `sdk/foundation/validation` | `sdk/pkg/validation` |
| `sdk/foundation/web` | `sdk/pkg/web` |
| `sdk/foundation/workers` | `sdk/pkg/workers` |

```go
import (
    "github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
    "github.com/gopernicus/gopernicus/sdk/pkg/list"
    "github.com/gopernicus/gopernicus/sdk/pkg/web"
)
```

Selectors such as `web.NewWebHandler`, `list.Request` and `workers.Middleware`
retain their package qualifiers; only the import paths change. Keep any existing
explicit aliases. There is no `pkg` Go package to import and no new module:
`sdk/pkg` is the directory grouping these eight packages within the SDK module.
The old directories are removed without forwarding packages. Update local build,
formatter or guard scripts that name the old directory. The documentation catalog
moves from `docs/sdk/foundation.md` to `docs/sdk/pkg.md`.

Root SDK helpers (`sdk.Principal`, `sdk.IDGenerator`, etc.), SDK capabilities,
and the separate shared `pockets` module retain their paths. The layering stays
unchanged: packages under pkg import root SDK, not sibling pkg packages or
capabilities; capabilities may import root and pkg. SDK remains stdlib-only.

Prior audit entries retain historical paths. Apply this prefix migration to their
foundation-package examples after following any earlier package-specific moves.
For example, the restored cryptids name now lives at `sdk/pkg/cryptids`; identity,
ID, pointer and slug primitives previously promoted to root remain there.

The move changes no go.mod requirements, dependency versions or module count
(42). Before adopting released versions, upgrade all consuming framework modules
that reference the old paths as a coordinated set; an updated SDK alone cannot
compile an older consumer still importing a removed package. Local workspaces or
explicit root-SDK replacements can resolve the unreleased checkout meanwhile.

### Scope and verification

This is a path-only move, including mechanical updates to CMS and authorization
imports. The pending jobs/runtime fixes and authorization-listing design are
separate work. No SQL, schema, permission, pagination or worker behavior changed.

All 91 relocated files were compared with their baseline after reversing only
path and layer-name comment changes. Full workspace checks, documentation checks
and standalone behavior verification are recorded in
[sdk-pkg-rename.md](plans/sdk-pkg-rename.md). External consumer apps, published
module resolution, live providers/stores and production were not exercised.


## AUDIT-018: Jobs runtime handler snapshots

- **Implemented:** 2026-09-10.
- **Module:** `github.com/gopernicus/gopernicus/pockets/jobs`.
- **Release:** unreleased; no version selected, tags or publication. Adopt with
  the compatible SDK/shared-pockets audit releases described above.
- **Impact:** corrected scheduler ownership and runtime startup validation;
  optional registration no longer rejects services without unfenced handlers.
  No exported signatures or module requirements change.
- **Data migration:** none; repository ports and persisted formats are unchanged.

### Before and after

Previously, NewService retained the host's handler map but captured scheduler
kinds immediately. NewRuntime copied handlers later. If the map started empty,
the scheduler's empty filter selected every kind, including schedules the runtime
could not handle. If handlers changed after a nonempty initial map, the scheduler
could miss new kinds or retain removed ones. Late empty keys/nil handlers were
not rejected by NewRuntime.

NewRuntime now validates and copies the final handler map, derives its kinds
from that copy, and supplies those kinds to both queue and scheduler. Each runtime
owns its handler snapshot and scheduler filter; building another runtime does not
change existing ones. Foreign schedules remain due for their owning runtime,
without advancing their next-run time or creating a job here.

### Consumer migration

Staged startup remains supported. Allocate the map before NewService, populate
that same map once dependent services exist, and check NewRuntime's error:

```go
handlers := make(map[string]jobs.HandlerFunc)
svc, err := jobs.NewService(repos, jobs.Config{Handlers: handlers, Logger: log})
if err != nil {
    return err
}

// Build services that depend on svc, then wire their handlers.
handlers["thumbnail"] = createThumbnail
rt, err := jobs.NewRuntime(svc)
if err != nil {
    return err
}
// Run rt explicitly and handle its error/drain in the host's lifecycle.
```

Assigning a different map to cfg.Handlers does not update a previously built
service. Do not mutate the map concurrently with NewService, NewRuntime or
Register. Later map edits affect only runtimes constructed afterward; there is
no live registry update. Handler functions retain their captured state, whose
concurrency remains the host's responsibility. EnsureSchedule is still usable
before handlers are populated.

NewRuntime returns ErrHandlersRequired for an empty map and ErrInvalidHandler for
an empty key or nil handler, including invalid entries added after NewService.
Handle these startup errors even if the host never calls Register.

Service.Register now optionally logs the current unfenced service configuration,
mounts no routes and starts no goroutines. Enqueue-only and fenced-only services
may call it without a router or omit it. It no longer returns ErrHandlersRequired;
move any startup validation previously relying on that error to NewRuntime or
NewFencedRuntime. The jobs-minimal example now omits this unnecessary call.
Fenced runtime handler configuration and SDK worker/job middleware are unchanged.

### Known callers and verification

Segovia v2's staged handler map motivated this fix; no external consumer was
modified or executed. The local jobs-minimal host exercises ordinary runtimes;
auth-cms exercises the fenced delivery surface. Regressions cover empty/nonempty
staging, invalid late entries, independent runtimes, untouched foreign schedules,
handler selection, copied scheduler kinds and optional registration.

Commands, real host behavior, task-relative changes and verification limits are
recorded in [jobs-runtime-implementation.md](plans/jobs-runtime-implementation.md).
Full jobs scheduling durability, live stores and the remaining pocket ownership
findings are separate audit work; this entry does not declare those audited.


## AUDIT-019: Jobs SQL generation ordering

- **Implemented:** 2026-09-10.
- **Modules:** `github.com/gopernicus/gopernicus/pockets/jobs/stores/pgx` and
  `github.com/gopernicus/gopernicus/pockets/jobs/stores/turso`.
- **Release:** unreleased; no versions selected, tags or publication. Adopt with
  compatible SDK/shared-pockets/jobs versions from the ongoing audit release.
- **Impact:** corrected latest-generation lookup for new fenced/keyed admissions
  when clocks repeat or move backward. No public signatures or dependencies change.
- **Data migration:** none; no schema changes or automatic historical backfill.

### Before and after

The SQL adapters ordered logical-key history by created_at then job_id, while
new admissions always used the current wall clock. A previous writer with a
faster clock, or equal stored timestamps with descending random IDs, could leave
GetLatestByKey/LatestStatusByKey selecting a superseded or canceled generation
instead of the newly admitted pending job.

EnqueueOnce and Replace now inspect the greatest stored created_at for that key,
including terminal generations, within the existing admission transaction. New
creation timestamps follow it when necessary: PostgreSQL compares at microsecond
precision and advances one microsecond; Turso's fixed-width TEXT advances one
nanosecond. The returned row and a later Get share the stored timestamp. The
memory store already applies the same principle.

Both CreatedAt and initial UpdatedAt use the adjusted timestamp. ScheduledFor,
claim/lease deadlines and terminal timestamps keep their existing clocks. A
future creation timestamp does not delay a due job or extend a prior lease.
Independent keys and unkeyed admission are not shifted by another key's history.

### Consumer migration

No Go call-site or migration SQL changes are required. Upgrade every process
writing these fenced tables to the corrected store adapter; mixed old/new writers
cannot guarantee generation order because an old writer can still insert a
backdated generation. Preserve the existing SDK/import migrations when repinning.

Treat CreatedAt as ordering metadata that may be ahead of wall time after a
clock correction or rapid admission. Do not use it as proof of the exact enqueue
instant or replace ScheduledFor with it. Subsequent UpdatedAt values retain the
normal lifecycle clock and can be earlier than adjusted CreatedAt.

Existing rows are not rewritten, and historical insertion order cannot reliably
be reconstructed from tied/backdated timestamps and random IDs. The fix makes
new admissions follow all surviving history; it does not repair an already
misordered key until a new admission occurs. No automatic resend/replacement of
existing work is part of this update.

### Verification and scope

Tests reproduce the old failure with a future stored predecessor and descending
explicit IDs. They cover Replace, EnqueueOnce after terminal history, concurrent
replacements, stored/returned timestamps, independent keys, prompt claims and
retired lease rejection. Live race/conformance suites passed against disposable
PostgreSQL (bare and named schema) and local libSQL using the actual adapters.
Hosted Turso and external consumer applications were not exercised.

This fixes generation ordering only. The scheduler audit separately found a
claim-to-enqueue loss window, interval precision problems and stale latest-job
bookkeeping; no scheduler protocol changed here. See
[jobs-persistence-audit.md](plans/jobs-persistence-audit.md) for evidence, commands,
remaining work and verification limits.


## AUDIT-020: Jobs durable scheduling and queue correctness

- **Implemented:** 2026-09-10; unreleased. Upgrade jobs core, selected store
  adapter and SDK together. No consumer repositories or releases changed.
- **Breaking:** custom schedule repositories, scheduler SQL migration, fractional
  intervals, ordinary JSON input, and corrected retry/terminal behavior.

### Scheduler migration

Apply `0004_job_schedule_occurrences.sql` from the selected store adapter through
an appropriately ordered host migration. Stop old scheduler writers before
starting new ones; the old protocol cannot participate safely in pending dispatch.
The migration adds a table/index without rewriting schedules. Drain pending
occurrences before reverting to an older binary or removing the table. Past lost
occurrences cannot be recovered by this migration.

Custom `schedule.Repository` implementations must replace the old ClaimDue
signature with `ClaimDue(ctx, expected Schedule, next, now) (bool, error)`, remove
SetLastJob, and implement ListPending, RecordAttempt and AckOccurrence. ClaimDue
must atomically check ID/slot/kind/spec/enabled/due state, reject a second pending
occurrence for that schedule, snapshot current tenant/payload, advance NextRunAt
and persist an Occurrence. Consult the port comments and `storetest.RunSchedules`.
RecordAttempt increments a pending delivery count and reports false for missing
IDs. ListPending filters kinds before limit and orders attempts, slot and JobID.
AckOccurrence atomically deletes that exact occurrence and records matching
LastRunAt/LastJobID without rewinding UpdatedAt; stale acknowledgements are no-ops.

Failures or process restarts retry pending work using the same JobID, including
when schedules and queues use separate stores. IDs now include fractional slot
precision; do not parse or construct the previous second-only format. Treat the
schedule ID namespace as reserved for the scheduler. Delivery is at least once:
retain ordinary queue IDs through dispatch/retry and make handler effects
idempotent. Memory stores implement the same protocol without process durability.

Edits, disabling and deletion affect future claims; already-admitted occurrences
retain their snapshot and remain deliverable. Inspect ListPending to find those
occurrences. LastRunAt now identifies the admitted run whose delivery was
acknowledged, rather than a claim with no confirmed queue handoff. Attempt counts
include admission of a delivery that crashes before enqueue. Attempts rotate
failed deliveries; invalid legacy recurrences/missing cron configuration fail
before admission and require operator repair or disabling.

Every intervals must be positive whole seconds, at least one second. Fractional
intervals now return sdk.ErrInvalidInput instead of being silently truncated by
SQL. Cron must return a nonzero time strictly after now. EnsureSchedule validates
nonblank name/kind and JSON payload; empty ordinary/schedule JSON becomes `{}`.
PostgreSQL Ensure is an atomic upsert and preserves NextRunAt for unchanged specs.

### Queue and worker behavior

Ordinary jobs now honor a positive persisted MaxAttempts; the configured ceiling
is the default. SDK Runner recognizes optional `workers.RetryLimitedJob`
(`RetryLimit() int`), while Permanent/Reject still ends retries immediately.
Applications that accidentally relied on the runtime overriding job-specific
ceilings will observe corrected retry counts.

EnqueueJob rejects malformed JSON before storage and normalizes empty JSON to
`{}` consistently. Fenced payloads remain opaque bytes. Public fenced admission
rejects empty kinds; optional logical keys in the full-fidelity methods remain
supported.

Memory queues/schedules copy mutable payloads and timestamp pointers at their
boundaries. Mutating returned jobs or retained checkpoint buffers no longer edits
stored work. A duplicate-ID memory Replace fails without superseding existing
work. Ordinary terminal completion/failure cannot resurrect a terminal job:
repeating the same terminal outcome is idempotent, conflicting outcomes return
sdk.ErrConflict. Ordinary running jobs still have no ownership fencing; use the
fenced queue when stale-worker protection is required.

The jobs-minimal host now cancels the sibling HTTP/runtime component on failure,
including HTTP bind failure. Host-owned lifecycle and generic SDK workers remain.

### Verification

Memory regressions, SDK/jobs race tests, PostgreSQL bare/named-schema and libSQL
full race/conformance passed. Migration upgrade tests preserve existing schedules.
A separate executable admitted work, exited before enqueue, then a new process
recovered, executed, acknowledged and drained on each SQL adapter. Repository
`make check` passed. Details and exact paths are tracked in
[jobs-events-audit-implementation.md](plans/jobs-events-audit-implementation.md).


## AUDIT-021: Events visibility, lifecycle and opaque outbox payloads

- **Implemented:** 2026-09-10; unreleased. Upgrade events core/store and SDK together.
- **Breaking:** explicit stream visibility policy and PostgreSQL payload migration;
  outbox validation and per-connection projection behavior are corrected.

### Host migration

Set `events.Config.Visible` before Register. It receives the stream context,
principal and event and returns `(bool, error)`. Missing policy returns
sdk.ErrInvalidInput without mounting routes. Denial or error suppresses delivery.
Apply host tenant/resource/recipient rules there; the framework supplies no tenant
policy. A deliberately shared feed may explicitly return true. Resource streams
still require Config.Authorize and match the requested aggregate before delivery.
The auth-cms example explicitly chooses its shared authenticated demo feed.

Visible runs in each stream before projection; it must honor context cancellation.
Projector now runs per allowed connection, so make it concurrency-safe and do not
mutate events. Slow policy/projection fills only that connection's bounded buffer;
SSE remains a lossy wake-up signal, without replay or exactly-once delivery.

Set Config.Logger for operational diagnostics. Middleware is copied at construction;
changing the original slice does not reconfigure mounted streams. Register rejects
a nil router. Close releases the gateway subscription idempotently; stop/cancel
HTTP streams separately, then Close the gateway and finally the host-owned bus.
A closed gateway rejects new registration/connections. Poll with a nil repository
now returns sdk.ErrInvalidInput instead of panicking.

### Outbox migration

Apply `0002_event_outbox_payload_bytes.sql` from the chosen adapter through the
host migration stream. PostgreSQL converts JSON payload text to BYTEA, preserving
its stored textual representation; earlier empty-to-`{}` substitutions cannot be
reversed. Stop old writers/readers during rollout. Rollback to JSON is only valid
if every new payload is valid JSON; arbitrary binary data makes rollback lossy.
Turso's matching migration is a documented no-op: its existing column accepts
BLOB values and existing TEXT values remain readable.

Append and AppendTx validate the entire record batch before inserting any row.
Records require valid IDs/types under sdk events.Record.Validate. Payloads are
opaque bytes, including invalid UTF-8 and empty payloads; no automatic `{}`
substitution. Custom outbox adapters should run the expanded storetest suite and
return detached records. Append retains its documented own transaction; use
AppendTx to atomically couple domain changes and outbox writes.

Poller delivery must acknowledge the host's intended handoff before MarkPublished.
Failed acknowledgement/restart can replay an event; consumers deduplicate EventID.
Use one poller per outbox. Invalid historical head records or persistently failing
handlers need operator repair; they are not silently skipped or marked published.

### SDK SSE and verification

SSE write deadlines now respect the earlier request deadline, so a long heartbeat
window does not extend a shorter stream deadline. Custom ResponseWriters and host
callbacks still must support/honor cancellation; this does not forcibly interrupt
arbitrary application code.

Events core and SQL race/conformance passed, including real SSE HTTP behavior,
PostgreSQL bare/named schemas, libSQL, batch validation, opaque bytes, transaction
rollback and the PostgreSQL legacy migration. Scope and detailed commands are in
[jobs-events-audit-implementation.md](plans/jobs-events-audit-implementation.md).


## AUDIT-022: Authentication proof lifecycle and host API

- **Implemented:** 2026-09-10; unreleased. Authentication core, its store adapters,
  bundled views and custom stores must upgrade together. No tags or publication.
- **Breaking:** stronger repository contracts, verified invitation ownership,
  durable invitation acceptance, logout proof requirements, browser Origin checks,
  configuration validation and independent abuse budgets.
- Implementation and verification record:
  [authentication-audit-implementation.md](plans/authentication-audit-implementation.md).
  Authorization pocket implementation remains deferred.

### Rollout and schema

Apply the chosen SQL adapter's `0018_invitation_acceptance.sql` through the host
migration stream before starting the new binary. It adds
`invitations.resolved_subject_type` and reserves an active invitation tuple for
both `pending` and `accepting` rows. Existing pending rows need no secret reissue
solely for this migration. `users.auth_revision` is reused for credential fences;
no new credential/session column or blanket session invalidation is introduced.

Stop old authentication writers and delivery workers during the cutover. Old
writers cannot honor revision admission or invitation claims; a mixed deployment
reintroduces the races. Resolve outstanding `accepting` claims before any rollback
to an old writer. In-flight sensitive codes, identifier changes, password-reset
links, passwordless login codes, all magic links, and explicit/pending OAuth linking flows use
new proof bindings; start those flows again after upgrade. Old access sessions remain usable
until normal expiry/revocation; stateless JWT gates still have their existing
revocation delay. Use live-session middleware where immediate revocation is needed.

### Custom repository migration

Implement these contracts atomically in the store, then run the expanded
`authentication/storetest` families. A service-level read followed by an unchecked
write does not satisfy the concurrency contract.

- `Users.Provision(ctx, user, primary, user.InitialCredentials{...})` commits the
  user, identifier and optional initial password/provider together. A collision
  or failure leaves no orphan account or initial credential.
- `Passwords.Change(ctx, userID, user.PasswordChange{ExpectedAuthRevision,
  ExpectedHash, NewHash, Now})` checks the current active owner/revision/hash,
  writes the hash, increments the revision and revokes sessions, grants and reset
  proofs atomically. It returns the new revision. Trusted `Passwords.Set` now also
  increments/revokes; use provisioning for initial credentials without a mutation.
- `ActiveSessions.CreateForActiveUser(ctx, session, expectedAuthRevision)` is
  required for every service session mint. The active user and expected revision
  must match inside the insertion transaction. The plain session-insert fallback
  is removed. Password reset racing a login can no longer leave a new session
  authenticated with the old password.
- `PasswordResets.Redeem` keeps its signature but now requires the challenge's
  versioned `passwordreset.Binding` in `Context`. Inside the same reset transaction,
  check the issuing revision and original identifier's current active, verified
  recovery status and ownership. Reject old/unbound/stale proofs with
  `sdk.ErrNotFound` without consuming the token or changing credentials. The reset
  advances the revision and revokes sessions/grants/reset proofs atomically.
- `Passwordless.Redeem` must enforce `passwordless.Binding.AuthRevision` whenever
  the link was issued to an existing user. Atomic consumption alone does not make
  a proof issued before a credential change current. Preserve the stored
  provisioning intent for links issued while the address had no account.
  Magic-link `BindingVersion` is now `2`; reject version `1` rather than guessing
  whether its address had an owner at issuance. The previous initializer omitted
  owner binding for some unverified accounts, making those old payloads
  indistinguishable from intentionally accountless proof. All old magic links
  restart. Newly issued accountless version-2 links retain current-claim semantics.
- `OAuthAccounts.Link(ctx, account, expectedAuthRevision, adoptIdentifierID, now)`
  checks the revision and attaches the provider atomically. Adoption also verifies
  the same active login/recovery identifier and removes prior password/sessions/grants/reset
  proofs. An OAuth email proof for a retired or reassigned identifier is rejected;
  restart linking against the current account identity.
  Credential removal (including OAuth unlink) revokes sessions. `UnlinkOAuth`
  returns only an error, so the host prompts a fresh login with a remaining method.
- `AuthenticationGrants.Create` takes expected user revision and current time;
  admission requires an active owner and matching live session.
  `Consume(ctx, authgrant.Requirement, now)` atomically checks session liveness,
  requested age/assurance and spends a suitable grant once. An unsuitable grant
  cannot bypass policy and is not spent just because another operation rejected it.
- `ContactChanges.Get` reads the current pending generation;
  `Consume(ctx, userID, kind, expectedID)` must leave a newer generation intact.
  Each pending ID has its own proof binding, including the issuing session and
  credential revision. Confirmation requires that same live session; custom
  transports must supply `IdentifierChangeConfirm.SessionID`. PostgreSQL challenge and contact
  replacement use atomic upserts, including when no previous row exists.
- Invitations add `ClaimAcceptance` and `CompleteAcceptance`, both taking an
  `invitation.Acceptance` (token hash, subject type/ID and current time).
  `StatusUpdate.ExpectedTokenHash` is required for unclaimed cancel/decline/resend.
  Accepting rows cannot be cancelled or resent, and remain in active uniqueness.

Memory stores must copy mutable session/grant method slices at their boundaries.
The authentication SQL adapters own focused transactions; they do not generally
join the host's ambient connector transaction. Do not wrap several service calls
in host `Transact` and assume they commit or roll back together. Firestore refuses
ambient transactions explicitly and remains partially implemented; see the
[supported capability inventory](pockets/authentication/stores/firestore/README.md).

### Invitation ownership and retries

Unverified registration email is no longer sufficient to receive an automatic
resource grant. `RequireVerifiedEmail: false` may permit login, but does not relax
invitation ownership. Email and phone acceptance use the caller's active verified
identifier; a request's `Identifier` field is not authority. Registration does not
resolve invitations until address verification proves ownership.

Acceptance changes `pending` to `accepting` before calling the host `Granter`.
The invitation ID remains the stable grant operation ID. Hosts must deduplicate
concurrent/retried grant calls by that ID. A grant or finalization failure retains
the claim because the external side effect may already have committed. Retry with
the same token and bound subject to finish, even after the original expiry;
completed same-subject repeats succeed without granting again. A claim is not a
lease: cancellation cannot undo an in-progress external grant. No automatic retry
worker is added; hosts can retry acceptance/verified-address resolution or reconcile
an outstanding claim. Permanent failures need host resolution.

### Browser and native callers

Login and refresh now apply the existing browser Origin policy to JSON requests.
Configure exact `AllowedOrigins` for browser hosts; native requests without browser
Origin/fetch metadata remain supported. Host mutations of the original allowlist
slice after construction no longer change the running policy.

Logout accepts a valid refresh credential (cookie or JSON `{refresh_token}`), or
falls back to a verified, unexpired access token. Unsigned payloads and expired
access-only credentials cannot revoke sessions. Native clients should retain and
send refresh proof for logout. Missing/already-revoked refresh-only logout is
idempotent. Datastore lookup/deletion errors are reported; browser cookies still
clear, so a local logout does not falsely promise server revocation. Form logout
redirects only on success.

Step-up code completion carries the same `kind` as issuance (`email` default,
`phone` for SMS), plus the original operation/session/context binding. Phone proof
records SMS methods. Sensitive starts use unique delivery issuance keys, so a
replaced challenge cannot reuse an earlier queued message. Earlier messages may
already have reached the provider; superseded codes fail. Authentication writes
and queue admission are separate operations: an admission failure is reported and
requires a new start. There is no distributed transaction with an external sender.

### Host configuration and public use cases

`NewService` now requires `Users`, `Identifiers`, `Sessions`, `ActiveSessions` and
`TokenSigner`. Enabled password flows also need `Passwords` and `Hasher`; with
delivery enabled they need `Challenges`, `PasswordResets` and `ChallengeProtector`.
An OAuth-only host can set `PasswordFlowsDisabled: true` and `DeliveryModeOff` and
omit password/mail dependencies. Enabled delivery requires `Mailer`. Production
requires `SessionCookie.Secure: true`. Missing ports fail during construction;
`Register` rejects a nil router. Password algorithm and strength remain host policy.

`Config.AuthenticationLimits` gives each operation independent `PerSubject` and
`PerIP` limits. Defaults per minute: login/token password proof 5/30, password reset
starts 3/20, sensitive code starts 5/30, sensitive password proof 5/30. Full zero
limits select defaults; malformed partial limits fail boot. Subjects are digested;
use trusted client IP context and a shared production limiter. Changing IP no
longer resets a subject budget, and changing subjects does not reset an IP budget.
Limiter failures fail closed for these operations; the bundled HTTP routes report
exhaustion as 429. Callers must handle rate limits and temporary infrastructure errors.

Sensitive code and OAuth-link proofs are bound to their issuing credential
revision. A reset or recovery-method change invalidates earlier proof; start again
rather than retrying the mutation against a newer credential snapshot.
Passwordless codes and magic links issued to an existing account also carry its
issuing revision, including when provision-on-consumption is enabled. Address-only
links issued before an account exists retain their recorded provisioning intent
and resolve the current claim atomically. Registration
verification preserves the identifier's configured uses and primary status.

Each public verification resend now enqueues a distinct opaque delivery command;
it no longer replaces a shared queued command. Registration/admin delivery also
uses a distinct issuance key. This prevents overlapping initializers from replacing
a challenge and then losing the checkpoint containing its code. The valid code
follows challenge persistence order; concurrent HTTP requests and provider delivery
can finish in another order. Earlier messages may arrive with superseded codes.

The root `Service` now exposes the existing passwordless, credential inventory,
password/provider management, identifier and step-up use cases with public input
aliases, error sentinels and `CurrentSessionID` for live-gated host handlers. Custom web, terminal and mobile transports can call them without an
`internal` import. The host authenticates the caller, supplies its user/session IDs,
and authorizes application actions. `RequireRecentAuthentication` proves recency
and assurance; it does not decide application permissions.

External consumers (including gps-360-go, coordination-hub and segovia-v2) have not
been modified. Feed this entry into their upgrade work and exercise registration,
reset/login races, browser login/refresh/logout, verified invitations and the host's
idempotent grant adapter before rollout. Final verification is recorded in the
implementation plan; Firestore all-feature parity is not claimed.


## AUDIT-023: Authorization model authority, evaluation and listing APIs

- **Implemented:** 2026-09-11.
- **Modules:** `pockets/authorization` and its `stores/{pgx,turso,firestore}`;
  migrated framework consumers and the `examples/auth-cms` host.
- **Release:** unreleased; coordinated module versions have not been selected.
- **Impact:** breaking lookup/result and custom-store APIs, stricter model/boot
  validation, current-model permission reads, evaluator errors/work limits,
  guarded role checks and constructor logger ownership.
- **Data migration:** no new authorization DDL or automatic tuple deletion.
  Existing canonical migrations still apply. The new document migration belongs
  only to the worked example; consumer apps do not apply it to adopt the pocket.

### Complete permission sets and ID pages are distinct

| Previous API/type | Replacement | Required caller action |
| --- | --- | --- |
| `LookupResources(ctx, principal, permission, type)` | `LookupAllResourceIDs(...)` -> `ResourceSet{IDs, Unrestricted}` | Use only when the entire allowed set fits its configured bound. |
| `LookupResourcesIn(ctx, LookupRequest{...})` | `LookupResourceIDPage(ctx, ResourceIDPageRequest{...})` -> `ResourceIDPage` | Treat the result as one ID page; pass `NextCursor` as `After`. |
| Shared `LookupResult` | `ResourceSet` for complete sets; `ResourceIDPage` for pages | Change host ports, method values, mocks and wrapper return types deliberately. |
| `LookupResult.Truncated` | Removed; ID pages use `HasMore` and `NextCursor` | Do not use an ID page as a complete business filter. |
| `FilterPage` -> `list.Page[T]` | `authorization.FilteredPage[T]` | Map only Items/HasMore/NextCursor/ScanLimitReached into the host response. |

The old public lookup aliases are removed. `ResourceIDPageRequest.Limit: 0`
still means one `MaxLookupResults` page, **not all IDs**. A restricted empty
`ResourceSet` means no access; `Unrestricted` removes only the permission-ID
predicate, not tenant/search constraints. Complete-set overflow is an error,
never a partial successful set. Apply a complete ID filter before business
sorting and pagination. Sorting rows hydrated from the first ID page cannot
produce a correct globally name/date-sorted page.

`FilterPageRequest.BatchSize` now controls candidate pull size. Zero requests
the remaining page capacity; a positive value up to `MaxBatchSize` lets the host
reduce sparse-query round trips. Each pull remains clamped to `MaxFilterScan`.
`Limit: 0` is still `list.DefaultLimit`. The old implicit `2 * Limit` overfetch
is gone; a host may select that size explicitly when it fits the batch bound.
The new `FilteredPage.ScanLimitReached` distinguishes a budget-limited short or
empty page from ordinary source exhaustion. Resume after the last consumed
candidate; `HasMore` does not guarantee another permitted row. Totals, facets
and previous-page fields are absent because this operation does not compute
them. Update response schemas if previously serializing `list.Page` directly.

### Current host models govern stored authority

`NewService` compiles an immutable RelationshipModel/RoleModel snapshot. Every
permission surface uses it: Check, Explain, batches, filtering, complete/page
lookup and guarded CheckPermission. A previously valid tuple that no longer
matches the current relation's allowed subject shape stops granting access,
including intermediate userset and Through edges. Raw tuple/role lists remain
fact inspection and old data stays removable.

Narrowing a model can revoke access immediately on updated instances. Retained
facts may become authoritative again if a later model accepts their shapes.
Decide cleanup explicitly. Coordinate the rollout of core and all store adapters;
old-model instances can continue granting according to their old snapshot.
This change does not make mixed-model deployments globally atomic or introduce
persisted model versions.

Custom stores must implement
`relationship.Storer.ForModel(ReadModel) relationship.Reader`. Its immutable
allowlist must apply to **every expansion edge and final grant** for all scoped
methods, including batches and reverse/descendant lookup. A zero ReadModel
allows nothing. A factory must return a non-nil independent reader, preserve
its transaction and never overwrite shared store model state. Do not return a
raw reader or filter only final IDs. SQL adapters bind allowed shapes as data;
there is no required model column.

Custom `mutation.StoreDecisionView` implementations must also implement
`ForModel(ReadModel) relationship.PermissionReader`, retaining that view's
transaction and tracked dependencies. Update mocks/fakes as well as production
adapters. Run shared `storetest.Run`, especially the maintained `ReadModel`
matrix, against every adopting adapter. Bundled memory, PostgreSQL, libSQL and
Firestore readers implement the new contract.

### Evaluation and guarded behavior

- Checks, explanations and candidate filtering share one root-relative DFS.
  Direct-only homogeneous checks batch unresolved candidates and honor OR
  short-circuiting. Through targets sort by `(Type, ID, Relation)` so physical
  row order cannot select a different early grant or error. Filtering preserves
  input order/duplicates. The old inconsistent set traversal is removed.
- `EvaluationLimits.MaxEvaluationSteps` (default 100000, also exported as
  `DefaultMaxEvaluationSteps`) bounds repeated frames/rules/targets per root.
  Zero selects the default; negative values fail construction. Existing graph,
  Through depth, target, batch, lookup and scan limits remain. A formerly
  unbounded repeated traversal may now return `ErrEvaluationLimit`/HTTP 503.
  Never treat that error as an ordinary deny or a complete permission set.
- Through-heavy candidate batches can perform more reads than the old evaluator.
  Shared ancestors are memoized within a call, without caching a path-local
  cycle denial as a final answer. Role batches reuse full-coordinate exact/global
  facts within one call, preserving exact-before-global errors and provenance.
  There is no permission cache across FilterPage pulls or requests.
- Lookup verifies reverse-discovered candidates and lookahead with the ordinary
  root budgets. A depth/work failure returns its error; a discovered candidate
  no longer allowed during verification returns wrapped `sdk.ErrConflict` with
  no partial success. Retry the request on that concurrent-change conflict.
  A cursor does not promise a fixed snapshot across reads/pages.
- SQL userset expansion now uses deduplicated full states and a capped consumed
  prefix. It reduces the measured generated-state work; it does not bound every
  physical scan. Descendant result paging can still compute a full reverse
  closure. Size/deadline queries with these distinctions in mind.
- `DecisionView.CheckPermission` now supports RoleModel-owned permissions through
  tracked exact/global role reads. The obsolete `ErrPermissionOwnedByRoles`
  sentinel is removed. Use the same permission check for either owning model;
  an undeclared pair denies. No new guarded/baseline ownership feature is implied.
- Guardian rules are copied at option creation and application, so caller slice
  mutations cannot change running/reused stores. Existing MinAnchors defaults
  are preserved. Canceled memory/Firestore callbacks stop before writes and do
  not consume receipts; replay does not retain the transient
  `SameRoleGrantRemains` annotation.

### Construction and logging

Malformed model symbols now fail at boot using the same reference rules as
runtime input: length, UTF-8 and controls are checked. A Direct check with stray
Through/Permission fields is invalid. Duplicate role permission grantors are
invalid; remove duplicates in host configuration. Valid punctuation remains
supported, and cycle vertices distinguish dotted `(type, permission)` pairs.
Unchanged valid model digests remain stable.

Typed-nil Relationships/Roles/Mutations, Guard and Audit dependencies fail boot
with `sdk.ErrInvalidInput`; use actual nil for an absent optional capability.
Permission gates validate nil resolvers and fixed coordinates at construction.
A typed-nil router is refused when bundled routes require it.

Set `Config.Logger` when constructing authorization. It defaults once to
`slog.Default()` and serves both Service and SystemMutator, including headless
audit warnings. `Mount.Logger` no longer replaces it. Intentionally absent
role routes do not emit a missing-route warning.

### Guard dependency ownership still requires a host decision

The baseline writer does not advance guarded dependency revisions or enforce
its guardians. Separate destination relation ownership is insufficient if a
command's permission depends on a mutable baseline-owned parent/group edge.
Review the entire transitive dependency graph, including resource moves and
reparenting. Choose command ownership, host serialization, or explicitly accept
weaker concurrency. No revision-aware baseline feature, automatic host rewrite
or consumer migration was introduced in this pass.

The source-backed design and current Segovia/Coordination Hub/GPS360 patterns are
in [authorization-guard-dependency-design.md](plans/authorization-guard-dependency-design.md).
The [complete listing example](examples/auth-cms/internal/outbound/domains/documents/README.md)
shows the three store/join arrangements, one host operation, query-bound private
cursors and explicit supported-policy SQL. SQL must match the full selected
permission; this constrained example rejects usersets, inheritance, roles and
extra OR branches rather than partially compiling them.

### Verification and adoption

Core and all store adapters pass build/vet and focused/full race coverage.
PostgreSQL was exercised in default and named schemas, libSQL against a local
server, and Firestore against an isolated emulator. Maintained stale-model tests
cover all permission surfaces and scoped reader methods; evaluator, mutation,
constructor and listing regressions cover the changed contracts. The host
listing ran over real HTTP with memory and PostgreSQL, including 1,005 grants,
name ordering, sparse continuation, cursor binding and changing data.

The full 42-module build/test/vet gate, generation checks, root architecture
guards and documentation build passed. Exact paths, reports and verified
seven-fixture cleanup are recorded in
[authorization-audit-implementation.md](plans/authorization-audit-implementation.md).
Real Firestore indexes/IAM/production contention and the optional PostgreSQL
non-C database leg remain unverified. Firestore ambient transaction refusal is
intentional and tested; the unsupported conformance family skips. No production
provider, deployment, publication or external consumer was changed.

When adopting, update modules together, migrate APIs/custom readers, validate
host models at boot, exercise realistic permission shapes and list densities,
and resolve mutable guard dependency ownership. A separate in-depth review
follows this implementation pass.

## AUDIT-024: Authorization mutation safety and simpler host contracts

- **Implemented:** 2026-09-11.
- **Modules:** authorization core and its memory, PostgreSQL, Turso and Firestore stores; affected host examples.
- **Release:** unreleased; upgrade core and adapters together.
- **Impact:** breaking guard, repository, policy and error-handling APIs; stricter construction and evaluation limits.
- **Data migration:** none. Rejected mutations already persisted no receipt. Existing successful receipts retain their encoding and replay behavior; stored tuples are not rewritten.

### Declare guardian policy explicitly

Bundled stores now default to an empty `GuardianPolicy`. Previously they implicitly
required one concrete `owner` on every resource type, including models that could
never represent one. **Hosts relying on last-owner protection must add an explicit
policy before upgrading.** Configure it once at store construction:

```go
policy := authorization.GuardianPolicy{Rules: []authorization.GuardianRule{
    {ResourceType: "project", Relation: "owner", MinAnchors: 1},
}}
store := memstore.New(memstore.WithGuardianPolicy(policy))
// PostgreSQL, Turso and Firestore expose the same WithGuardianPolicy option.
```

`DefaultGuardianPolicy()` remains an explicit convenience for protecting one
concrete owner on every type. Use it only when every type supports that rule.
`MutationRepository` now requires `GuardianPolicy() GuardianPolicy`, returning a
defensive snapshot of the policy actually enforced. `NewService` checks that
policy against the configured relationship model and returns
`ErrInvalidGuardianPolicy` for unknown types/relations, malformed fields, negative
minima, or a relation that cannot hold concrete anchors. Wildcards must fit all
declared resource types. Zero minimum still means one. Guardian rules apply to
relationship commands; roles-only services need no relationship model.

### Handle refused writes through error

`GrantRelationship`, `ReplaceRelationship`, `RevokeRelationship`, purge, and the
underlying mutation repository return a **nil receipt and non-nil error** when a
write is refused:

- `ErrSemanticConflict`: an exact subject already has a different relationship;
  use an explicit replacement when appropriate.
- `ErrInvariantBlocked`: the proposed state violates a guardian minimum or the
  configured purge bound. `ErrInvariantConflict` aliases this same error identity.

Both wrap `sdk.ErrConflict`; use `errors.Is`. Bundled HTTP error mapping returns
409. Audit records classify these refusals as `AuditFailed`, with an empty
outcome and `ReasonSemanticConflict` or the existing `ReasonInvariantConflict`.
No mutation ID or revision is consumed. A later attempt with the same ID can
succeed after the conflicting state changes.

Successful receipts have `OutcomeApplied`, `OutcomeNoChange`, or
`OutcomeNotFound`. All are durable and replayable. Remove root-level
`OutcomeSemanticConflict`/`OutcomeInvariantBlocked` handling; those aliases were
removed. Adapter evaluation still has internal refusal result constants, which
must be translated to errors before returning. No helper is needed for the normal
Go pattern `_, err := ...; return err`.

### Migrate MutationGuard reads

Replace `view.CheckPermission(ctx, scope, permission, type, id)` with the same
request used outside a guard:

```go
result, err := view.Check(ctx, authorization.CheckRequest{
    Principal: attempt.Actor.PrincipalRef,
    Permission: "manage",
    Resource: authorization.Resource{Type: "project", ID: projectID},
})
if err != nil { return err }
if !result.Allowed { return sdk.ErrForbidden }
```

The public `DecisionView` no longer embeds the adapter port. `ForModel` and
`Dependencies` remain on `StoreDecisionView` for adapter implementations only.
For intentional raw-fact policies, migrate to:

```go
view.HasGlobalRole(ctx, principal, role)
view.HasRole(ctx, principal, role, resource) // scoped plus global fallback
view.CheckRelation(ctx, principal, relation, resource)
view.RelationTargets(ctx, resource, relation)
```

Fact methods stay independent of model permissions. Raw relationship expansion
uses `MaxGraphStates`; target results use `MaxRelationTargets`. These limits now
resolve whenever a guard is configured, including hosts with opaque roles and no
model. Guard proposal slices are copied for each callback invocation; mutating
an attempt cannot change the command or another retry. Keep callbacks synchronous,
cancellation-aware and free of side effects or detached service calls.

### Update adapters and route hooks

- Rename `AssignmentPolicy`, `Config.AssignmentPolicy`, and
  `ErrAssignmentPolicyWithoutRoutes` to `RoleRouteAssignmentPolicy`,
  `Config.RoleRouteAssignmentPolicy`, and `ErrRoleRouteAssignmentPolicyWithoutRoutes`.
  The hook remains bundled-assign-route-only. Core assignment legality belongs in
  the role model; state-dependent authorization belongs in the guard.
- `relationship.Reader` and `Storer` no longer require `FilterRelation` or
  `RelationTargetsFor`. Bundled concrete methods remain available through optional
  `relationship.RelationSetReader`. Custom adapters may omit them.
- `storetest.Run` now takes a factory accepting `(t, GuardianPolicy)`. Construct
  fresh repositories with that exact policy for each call.
- Global `StoreDecisionView.HasRole` checks validate scope shape and track the
  **queried principal's** dependency even when a different valid subject scope is
  supplied. Resource checks preserve global fallback. Custom stores must protect
  negative membership predicates as well as recorded revisions; tracking only
  existing reachable groups does not prevent write skew.

### Evaluation, concurrency, and listing behavior

Role checks now charge one root plus each visited grantor to
`MaxEvaluationSteps`, including memo hits. Role enumeration charges the root plus
all compiled grantors before reads. Review unusually small budgets; overflow
fails closed with `ErrEvaluationLimit`. Maximum-machine-integer lookup, graph,
and through-depth bounds are rejected rather than overflowing lookahead arithmetic.
Model cycle detection now uses a deterministic linear pass. Canceled lookups and
fact reads return cancellation, including empty-result boundaries.

PostgreSQL mutations use SERIALIZABLE transactions to protect negative predicates.
Trusted commands retry genuine serialization/deadlock contention with a bounded,
cancelable backoff; guarded contention returns `ErrStaleRevision` without rerunning
the policy internally. Explicit stale revisions and callback refusals are terminal.
Concurrent baseline `SetRelationTargets` uniqueness conflicts roll back instead of
reporting an incomplete set as success. PostgreSQL creation timestamps are
normalized to its microsecond precision for stable immediate listings.

Firestore distinguishes retryable transaction read failures from terminal guard
and semantic-validator refusals, preserving policy error identity. No changes to
indexes or document encoding are required.

`FilterPage` now defaults each candidate pull to the requested page size instead
of shrinking to remaining capacity. Explicit `BatchSize` and scan-budget clamping
remain. Hosts retain tenant/search/order/cursor ownership and the three listing
recipes from AUDIT-023. Baseline writes still do not advance mutation revisions;
receipt-free revision-aware writing remains a separate design task.

### Adoption checks

Search consumer code for old guard signatures, removed outcome aliases,
`AssignmentPolicy`, custom mutation repositories, and guardian-free constructors
that relied on implicit owner protection. Upgrade adapters and core together,
verify first-owner and last-owner flows, inspect HTTP/audit refusal handling, and
exercise listing continuation under the host's configured budgets. Run custom
stores through the updated conformance suite.

Implementation and verification are tracked in
[authorization-followup-implementation.md](plans/authorization-followup-implementation.md).


## AUDIT-025: Authorization tuple identity and metadata removal

- **Implemented:** 2026-09-11; verification tracked in the implementation plan.
- **Modules:** authorization core and its memory, PostgreSQL, Turso and Firestore stores.
- **Release:** unreleased; upgrade core and adapters together.
- **Impact:** breaking Go fields/configuration, raw listing order/cursors, role
  list JSON, and stored schema.
- **Data migration:** append-only SQL `0006_iam_tuple_identity.sql`; explicit
  Firestore maintenance upgrade and updated indexes.

This entry covers tuple metadata removal only. The proposed removal of
`iam_scopes` and the choice of audit-history versus receipt-ledger semantics
remain pending; this entry does not remove revisions, receipts, or mutation IDs.

### Migrate entity fields and configuration

Relationships use their full tuple as identity:
`(resource_type, resource_id, relation, subject_type, subject_id, subject_relation)`.
`group:engineering` and `group:engineering#member` remain distinct subjects.
The separate one-relation-per-exact-subject-per-resource rule remains enforced.

- Remove `RelationshipID` from `CreateRelationship` inputs and remove
  authorization `Config.IDs`. Authorization no longer mints relationship IDs.
  Other pockets' ID configuration is unrelated.
- Remove uses of `ID` and `CreatedAt` on `SubjectRelationship` and
  `ResourceRelationship`. Both projections now expose `SubjectRelation`, so a
  host can identify the exact userset when displaying or removing a grant.
- Remove `CreatedAt` from `role.Assignment` and expectations of `created_at` in
  bundled raw role-list responses. Role assignment identity is the existing
  five fields: subject type/ID, role, resource type/ID. The empty resource pair
  still means a global role.
- Custom adapters must store/project the natural fields and run the updated
  shared conformance tests. Raw tuple creation and role assignment reject
  malformed reference components, including control characters, invalid UTF-8,
  and fields longer than 256 bytes. Role scope must be both empty or both present.

To delete a relationship, pass its resource, relation and complete subject
reference to the existing exact-tuple deletion API. Do not replace the removed
ID with an application-generated surrogate identifier.

### Restart raw listing pagination

Raw relationship order is now `tuple_key ASC`, and raw role order is
`role_key ASC`; both accept descending order explicitly. These are order names,
not new fields on returned domain entities. Their keys join the natural tuple
components with U+0001 and sort by byte order. Effective-role listing still uses
`grant_key` and retains direct/global provenance.

Remove timestamp/relationship-ID ordering from clients and discard old raw-list
cursors when deploying. The bundled role routes accept `order=role_key:asc` or
`order=role_key:desc`; `order=created_at` is now a 400 response. Update API response
fixtures and any UI that displayed assignment creation time. Historical creation
time is removed by the migration; export it beforehand if the host needs it.

### Upgrade SQL storage

Stop old authorization writers, back up the database, export the new migration
beside the host's existing `0001`–`0005` files, then apply the host-owned migration
stream before deploying the new adapter. Do not rewrite or renumber migrations
already recorded in the host ledger. Fresh installations apply all six files.

`0006_iam_tuple_identity.sql` replaces the relationship surrogate primary key
with all six tuple fields and removes relationship/role creation timestamps.
PostgreSQL alters the table; Turso rebuilds the relationship table within the
migration transaction, preserving tuples, checks and indexes. Role assignments
are preserved. An old binary is incompatible with the resulting schema.

Natural-key components must not contain U+0001. The migration rejects legacy
rows containing that separator without completing the metadata removal; inspect
and repair those rows using host-owned semantics, then retry. Do not silently
merge or discard grants. Hosts with foreign keys, views, triggers or custom
indexes referring to removed columns must migrate those dependencies too.

### Upgrade Firestore storage

Stop authorization readers and writers. Export/deploy the current authorization
index manifest, wait for the indexes to be ready, and explicitly run
`firestore.UpgradeTupleStorage(ctx, db)` before starting the new adapter.
See the store's [upgrade guide](pockets/authorization/stores/firestore/UPGRADE.md).
No constructor performs this operation.

The maintenance function backfills two private tuple-order fields, removes
relationship/role metadata, and deletes obsolete `iam_relationship_ids` claims.
Tuple-derived document IDs and the subject claims enforcing exclusivity remain.
Work is paged and rerunnable; each write has an update-time precondition. Keep
traffic stopped until the full operation succeeds. Resolve validation or
concurrent-write errors before retrying. Existing documents without the new
order fields would otherwise be omitted from ordered Firestore queries.

### Adoption and verification

Search consumers for authorization `Config.IDs`, `RelationshipID`, projected
relationship `.ID`/`.CreatedAt`, assignment timestamps, raw list order parameters,
old cursors, copied migration inventories, and Firestore index manifests.
Repository callers and conformance fixtures are updated; external consumer apps
have not been edited. Exercise grant/revoke, exact userset identity, global and
scoped roles, and raw pagination after the host upgrade.

Concrete verification and remaining decisions are recorded in
[authorization-tuples-implementation.md](plans/authorization-tuples-implementation.md).

## AUDIT-026: Authorization change history and mutation simplification

Status: implemented and verified; results and explicit live-test limits are in
[the implementation plan](plans/authorization-audit-log-implementation.md).
Upgrade authorization core and all its store adapters together. This entry
supersedes the receipt/revision behavior described by earlier audit entries.
External consumer applications have not been edited.

### API changes

- Remove `MutationID`, `NewMutationID`, `DeriveMutationID`, `Revision`,
  `ExpectedRevision`, payload/schema digests on commands, `Receipt`, `Replayed`
  and dependency revision bookkeeping. Calls evaluate the current model and
  guard every time. No durable request deduplication remains; a host needing it
  implements that around the pocket.
- `ScopeKey`/`ScopeKind` become `Target`/`TargetKind`, with `TargetResource` and
  `TargetSubject`. Use `Command.Target` and `MutationAttempt.Target`. This is the
  address being changed, not a policy/scopes subsystem. Global versus
  resource-scoped role assignments remain supported.
- Write methods return `*MutationResult` (`mutation.Result` at the domain rim),
  containing `Outcome` and the role-removal annotation where applicable.
  `Service.UnassignRole` returns flat `UnassignRoleResult{Outcome,
  SameRoleGrantRemains}`; remove its nested receipt access. Outcomes are
  `applied`, `no_change`, `not_found`. Refusals return errors. Global-role
  fallback is recomputed on each unassign, including absent-assignment no-ops.
- Replace `TeardownAuthorizationScope` and its command with
  `TeardownResourceAuthorization` and `TeardownResourceAuthorizationCommand`.
  The required reason and guardian exception remain.
- Remove `ErrStaleRevision`, `ErrMutationMismatch`, their reason codes, and the
  domain payload-mismatch error. `ErrConcurrentMutation` / `concurrent_mutation`
  classify actual transaction contention. Semantic/guardian conflicts retain
  their existing error identities. There is no client compare-and-set revision.
- Remove `Config.Audit`, `AuditSink`, `AuditEvent`, `AuditDecision` and
  `ErrAuditWithoutGuard`. They were a best-effort attempt hook, not durable
  change history. Hosts may observe denials/errors around their own calls.

A repeated grant now returns `no_change`, not a replay of `applied`. Grant,
revoke, then grant applies again. If the current model no longer permits the
role/relation, even a previously successful grant is rejected; removal remains
available. Adapt retries and tests to these state-based semantics.

### Optional atomic history

Use the adapter's `WithAudit()` option (default off) to record every actual
relationship/role addition or removal made through its raw, trusted and guarded
writers. The reader is `Repositories.Audit` (memory: `Store.Audit()`), available
even when recording is disabled. Hosts control reader access, retention,
presentation and export. No HTTP audit endpoint is mounted.

Trusted/raw writes need explicit attribution when recording is enabled:

```go
ctx = authorization.WithAuditSource(ctx, authorization.AuditSource{
    System: "bootstrap", Reason: "initial tenant owner",
})
result, err := components.SystemMutator.GrantRelationship(ctx,
    authorization.GrantRelationshipCommand{
        ResourceType: "project", ResourceID: projectID,
        Relation: "owner", Subject: authorization.SubjectRef{Type: "user", ID: ownerID},
    },
)
```

Supply either one complete actor pair or an explicit system name. Optional
reason is valid UTF-8, at most 1024 bytes, without NUL. Enabled stores validate
source even for attempted no-ops. Guarded methods use their validated Actor,
overwriting caller attribution while preserving a valid optional reason.
Audit metadata never grants authority.

Each record contains a fresh record ID, an operation-grouping EventID, UTC
timestamp, source and one exact added/removed tuple or role assignment. IDs
identify history; callers never provide them as replay keys. Replacements record
removal and addition. No-ops, refusals and rollbacks record nothing. The fact
changes and history commit in the same transaction; audit failure fails the
write. Firestore transaction limits fail the whole operation, without splitting.

History listing supports paired resource/subject/actor filters, timestamp order
(default DESC), ID tiebreaking, cursor/offset pages and counts. Empty filter pairs
mean no filter. Complete history requires the host to enable recording on all
writers; direct database edits and recording-disabled writers are not captured.

### Concurrency and host transactions

Guard reads and supported raw writers now share datastore concurrency protection:
PostgreSQL locks relationship and role tables in fixed order before reading,
Turso uses `BEGIN IMMEDIATE`, Firestore uses native transactional document/query
reads, and memory uses one shared mutex. PostgreSQL serializes authorization
writes per schema while allowing ordinary reads. Assess that write contention
against the host workload before increasing its concurrency.

SQL raw operations still join host transactions. They use savepoints so failed
audit writes cannot leave their fact changes committable if a caller handles the
error. Return errors from the host callback to roll back the entire application
workflow. Guarded calls still refuse ambient transactions. Hosts keep authority
over cross-store workflows. Raw writers still bypass guard and guardian policy;
transaction isolation does not turn them into actor-authorized capabilities.

Ambiguous transport/commit failures are not automatically replayed. Retrying
an application operation after such an error is a host decision; the framework
cannot promise that the previous call did not commit.

### SQL upgrade

1. Stop old authorization writers. Archive `iam_scopes` / `iam_mutations` first
   if their old operational data is wanted.
2. Apply the complete authorization migration source through
   `0007_iam_audit.sql` (after AUDIT-025's `0006`). Existing `0001`–`0006` files
   remain unchanged. Migration 0007 drops the old counters/receipt tables and
   creates `iam_audit` and indexes. Tuple and role facts remain intact.
3. Deploy updated core/adapters/callers together. Enable `WithAudit()` and
   supply trusted/raw source metadata if recording is desired.

Old receipts cannot reconstruct exact historical additions and removals. The
migration does not manufacture an audit trail. Custom dependencies on removed
tables require host migration. Repositories that expose the audit reader expect
the audit table even when new recording is disabled.

### Firestore upgrade

Complete AUDIT-025 tuple upgrades first. With old writers stopped, explicitly run
`RemoveLegacyMutationStorage(ctx, db)` to delete legacy scope/receipt collections.
It is paged and rerunnable; constructors never perform cleanup. See the
[Firestore upgrade guide](pockets/authorization/stores/firestore/UPGRADE.md).
For recording, deploy the baseline and `firestore.audit.indexes.json` manifests
(or use `ExportAuditIndexes`) and wait for indexes to be ready before enabling
`WithAudit()`. Existing tuple/role facts are preserved; no old history is inferred.

### HTTP migration and consumer checks

Remove `mutation_id` and `expected_revision` from bundled role request JSON;
they now return 400 as unknown fields. Assign success is
`{"outcome":"applied"}`. Unassign success is
`{"outcome":"applied","same_role_grant_remains":false}`. No nested receipt,
revision, timestamp or replay flag remains. Model/guard/invariant errors remain
errors, not success outcomes.

Search consumers for the removed symbols, copied request/response DTOs, old
ledger queries and retry logic. Exercise grant/revoke/regrant, current-model
changes, guardians, global-role fallback, audit attribution, rollback and history
pagination after updating. Real Firestore index execution requires the separately
configured live gate; emulator success does not substitute for it.

## AUDIT-027: Pocket organization and store-support imports

- **Implemented:** 2026-09-11.
- **Modules:** authentication, authorization, jobs and events pocket cores;
  their adapters/tests and the Workshop scaffold are updated to match.
- **Release:** unreleased; no tags or external consumer changes in this phase.
- **Impact:** memory and conformance import paths/package names; defining-package
  identity for a few public aliases. Public service calls and configuration remain.
- **Data migration:** none. SQL migrations, stored formats and driver module paths are unchanged.
- **Scope:** CMS remains deferred and retains its existing paths.

### Move memory and conformance imports

Paths below are relative to `github.com/gopernicus/gopernicus/`:

| Previous import | Replacement | Package name |
|---|---|---|
| `pockets/authorization/memstore` | `pockets/authorization/stores/memory` | `memory` |
| `pockets/jobs/memstore` | `pockets/jobs/stores/memory` | `memory` |
| `pockets/authentication/storetest` | `pockets/authentication/stores/storetest` | `storetest` |
| `pockets/authorization/storetest` | `pockets/authorization/stores/storetest` | `storetest` |
| `pockets/jobs/storetest` | `pockets/jobs/stores/storetest` | `storetest` |
| `pockets/events/storetest` | `pockets/events/stores/storetest` | `storetest` |

Update imports in application code, custom adapter tests, examples and scripts.
Change unaliased `memstore.New...` references to `memory.New...`, or keep the old
local spelling with an explicit alias:

```go
import memstore "github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
```

Constructor signatures, options, memory behavior and conformance entry points
are unchanged. The old packages are removed; there are no compatibility shims.
`memory` and `storetest` remain packages in their respective core modules: do not
add requirements or version pins for new modules. Existing `stores/pgx`,
`stores/turso` and `stores/firestore` driver module paths remain unchanged.
Importing a public pocket service does not import memory or test support.

### Public API and implementation placement

Public Service construction, methods, configuration, optional Register and
runtime methods remain available. Authorization retains its separately held
Service, RelationshipWriter and SystemMutator capabilities. Hosts still customize
through declared dependencies, policy and their own handlers/workflows.

Root files now group related configuration, construction, vocabulary and methods.
HTTP cookie/header handling, middleware and response mapping live under inbound;
domain and internal logic are transport independent. Authorization's shipped
role handlers use the same command types as public Service callers. Jobs' public
Runtime directly owns its worker pools, replacing the duplicate internal runtime.

The following existing public names remain aliases, but the defining package
visible through reflection changes:

- `authentication.PrincipalOption`: authentication's internal inbound package.
- `authorization.PrincipalRef`: `authorization/domain/relationship`.
- `authorization.Actor`, `AssignRoleCommand`, `UnassignRoleCommand` and
  `UnassignRoleResult`: `authorization/domain/mutation`.
- `authorization.ResourceResolver`, `GateSpec` and `RoleRouteAssignmentPolicy`:
  authorization's internal inbound package.

Normal use through the public pocket names remains source-compatible. Review
custom reflection registries, generated code or serialization keyed by Go type
package paths. HTTP payloads and persistent datastore formats are unchanged.
Use keyed composite literals for these public structs, including Actor.

### Pocket authors and generated scaffolds

New scaffolds put memory and shared conformance under `stores` without creating
additional modules. The small generated Service implements its use cases
directly; an internal service plus forwarding facade is no longer mandatory.
Extract cohesive internal components when they simplify the implementation.

Dependency guards discover pocket cores and distinguish their packages from
legitimate driver/view modules. They cover memory and conformance even under
`stores`, reject unexpected nested modules, and keep domain/internal logic free
of HTTP dependencies. Existing generated host/pocket source remains host-owned;
apply the same changes deliberately rather than regenerating over custom code.

### Verification

All 42 modules passed build/test/vet and applicable integration/live-tag compile
checks through `make check` in an isolated copy of the working tree. Real-tree
guards passed separately; code matched the checked snapshot. All four pocket
cores passed race tests. Existing authentication browser/credential tests,
authorization HTTP/guardian/audit tests and memory conformance passed. The
generated pocket and both driver adapters compiled with the new layout, and the
documentation typecheck/build passed.

A rebuilt jobs example accepted HTTP requests, processed queued work and drained
an active job on SIGTERM before exiting successfully. Dependency inspection
confirmed root pocket imports exclude stores/test support and memory adapters
add no driver modules. Historical SQL migration hashes are unchanged. Live
databases and Firestore emulator/cloud were not rerun for these structural
changes; prior live-store evidence remains in the preceding audit entries.


## AUDIT-028: Public pocket services and HTTP adapters

Status: implemented and verified locally; unreleased.

Scope: authentication, authorization, jobs and events. CMS retains its existing
layout and API. This supersedes AUDIT-027's root-heavy organization. No SQL
migration or persisted format changes are introduced by this entry.

### New package structure

Real services, owned types and consumed ports live in public `logic/<concern>`
packages. Public `inbound/http` packages own middleware, HTTP handlers and route
registration. Roots assemble named components; they no longer mirror every use
case. Private helpers and engine machinery remain private. Driver stores keep
separate modules; memory and shared conformance remain in their core modules.

| Previous package | New public owner |
|---|---|
| authentication `domain/<aggregate>` | `logic/authentication/<aggregate>`, except invitation and delivery vocabulary, which live with `logic/invitations` and `logic/delivery` |
| authentication root use-case vocabulary | `logic/authentication`, `logic/invitations`, `logic/delivery`, or `inbound/http`, by responsibility |
| authorization `domain/relationship`, `domain/role` | `logic/relationships`, `logic/roles` |
| authorization `domain/mutation`, audit vocabulary | `logic/mutations`, `logic/audit` |
| authorization shared check/principal/limits and role-model vocabulary | `logic/model` |
| authorization list filtering/composite decision operations | `logic/decisions` |
| jobs `domain/job`, `domain/schedule` | `logic/queue`, `logic/schedules` |
| events `domain/outbox` and root poller | `logic/outbox` |
| events stream service | `logic/streams` |
| authentication/authorization/events HTTP vocabulary | each pocket's `inbound/http` |

There are no compatibility packages at the removed paths. Import only the
packages a host needs. Most root type aliases and helper re-exports moved to
their owning packages. The complete machine-readable symbol/import map is
[plans/pocket-api-migrations.json](plans/pocket-api-migrations.json).

### Authentication

`authentication.NewService(repos, cfg)` becomes `authentication.New(repos, cfg)`
and returns `*authentication.Components` with `Authentication`, `Invitations`,
`HTTP` and `Delivery` components.

| Previous call | Replacement |
|---|---|
| `svc.Login`, `svc.Refresh`, `svc.ChangePassword`, OAuth/account/machine use cases | `components.Authentication.<method>` |
| `svc.RegisterUser(...)` | `components.Authentication.Register(...)` |
| invitation create/list/accept/decline/cancel/resend | `components.Invitations.<method>`; this component is nil when disabled |
| `svc.RequireAccessToken()`, other HTTP gates and cookie methods | `components.HTTP.<method>` |
| `svc.Register(mount)` | `components.HTTP.Register(mount)` |
| `svc.RunDelivery(ctx)` | `components.Delivery.Run(ctx)` |
| `svc.InProcessQueueDepth()` | `components.Delivery.QueueDepth()` |
| `svc.DeliveryJobRuntime()` | `components.Delivery.JobRuntime()` |
| `svc.DeliveryStatus(...)` | `components.Authentication.DeliveryStatus(...)` |

Hosts may construct `authentication.Service`, `invitations.Service` and delivery
components from their public logic packages. The HTTP package exposes full-adapter
construction and middleware-only construction over a narrow credential contract.
Unverified callers cannot stamp the private verified-credential proof. Direct
construction validates its own dependencies, delivery/credential rails and policy.
Password policy and all application-specific gates remain host-supplied.
The former `domain/invitation.New` entity constructor is now
`invitations.NewInvitation`; `invitations.New` constructs the service.

Direct `logic/authentication.New` returns named `Service` and
`DeliveryInitializer` capabilities. Keep the initializer with the host's delivery
runtime; an ordinary authentication service cannot issue raw delivery challenges.
Verified contexts belong to the service and credential that verified them;
re-authenticating clears earlier session-liveness proof. Both HTTP constructors
require `AuthenticatorConfig.RuntimeMode`; production retains the secure-cookie
and shared-limiter requirements.

### Authorization

`authorization.NewService(repos, cfg)` becomes `authorization.New(repos, cfg)`.
The returned `Components` has `Decisions`, `Relationships`, `Roles`, `Mutations`
and `HTTP`, plus separately held `RelationshipWriter` and `SystemMutator`.
The old universal root `Service` is removed.

- Check, batch, explanation, permission lookup and filtered-list calls use
  `components.Decisions`. Generic `FilterPage` now lives in `logic/decisions`
  and receives that decision service.
- Relationship reads and schema inspection use `components.Relationships`.
  Its `GetSchema`, `SchemaDigest` and `GetPermissionsForRelation` return their
  values directly without an error; a constructed service always has its model.
- Role assignment reads use `components.Roles`; actor-facing assignments,
  revocations and relationship changes use `components.Mutations`.
- HTTP permission gates use `components.HTTP`. Root `components.Register(mount)`
  remains optional composition convenience. Independent adapters are constructed
  with `authorizationhttp.New` and registered with a route registrar.
- Shared `PrincipalRef`, `CheckRequest`, limits and role-model types live in
  `logic/model`; ReBAC schema/DSL types live in `logic/relationships`.
- Audit vocabulary/context helpers live in `logic/audit`, e.g.
  `audit.WithSource(ctx, audit.Source{System: "bootstrap"})`.

Only configured capabilities are present; check optional components before use.
Roles-only without a role permission model remains lookup-only. Relationships
and roles still share one guarded atomic mutation boundary. Ordinary services do
not expose trusted writers. Pass narrow services to request handlers; retain the
full component bundle and trusted writers in appropriate host composition code.
Direct decision and mutation constructors inherit the supplied relationship
service's evaluation limits when omitted and reject contradictory explicit limits.
They preserve exclusive role/relationship permission ownership, including when a
host supplies an independently compiled role model to mutation construction.

### Jobs

`jobs.NewService` becomes `jobs.New`, returning `*jobs.Components{Queue, Schedules}`.
Queue/work protocol methods move to `components.Queue`; schedules use
`components.Schedules`. `Schedules` is nil when disabled. The old forwarding
`ErrSchedulesNotConfigured` and logging-only `Register` method are removed.

```go
parts, err := jobs.New(repos, jobs.Config{
    Queue: queue.Config{MaxAttempts: 3},
    Cron: cronParser,
})
// Handle err before using parts.
rt, err := queue.NewRuntime(parts.Queue, parts.Schedules, queue.RuntimeConfig{
    Handlers: handlers,
    Logger: log,
})
```

Import `queue` and `schedules` from `pockets/jobs/logic`. `Handlers`, pool sizing,
worker/job middleware, cadence and logging move from `jobs.Config` into
`queue.RuntimeConfig`; queue admission defaults use `queue.Config`. Parse the
host's environment into those configurations separately (`jobs.Config` retains
`ScheduleBatch`). Handler registration finishes before `queue.NewRuntime`, which
validates and snapshots the map. Queue construction no longer retains it.

`queue.NewFencedRuntime(parts.Queue, queue.FencedRuntimeConfig{...})` replaces
`jobs.NewFencedRuntime(svc, cfg)`. `queue.Service` directly implements SDK work
ports. Hosts still own runtime start, cancellation, drain and side effects.

### Events

`events.NewService` becomes `events.New`, returning `*events.Components`.
`Register` and `Close` remain assembly/lifecycle conveniences. `Streams` exposes
the public filtered stream service; `components.HTTP()` constructs the validated
public HTTP adapter. Its `SubjectStream` and `ResourceStream` handlers can be
mounted on host routes while retaining configured middleware and visibility.
Resource handlers require the named resource path values. The raw fan-out hub
remains private; direct stream consumption applies visibility before projection.

`events.NewPoller`, `Poller`, `DeliverFunc`, `PollerOption` and `WithBatchSize`
move to `logic/outbox`. `EventVisibility` becomes `streams.Visibility` and
`Projector` becomes `streams.Projector`. Pollers still complete the selected
handoff before marking an entry published.

### Scaffold and boundaries

New pocket scaffolds put the actual service beside its types/port in
`logic/<aggregate>`. Root construction returns that service directly; no universal
service wrapper, root vocabulary aliases or placeholder `Register` are emitted.
Existing generated code remains host-owned. Public logic and old private logic
are covered by dependency guards, including the prohibition on importing root
composition or HTTP. SDK and module dependency boundaries remain unchanged.

### Verification

All 42 modules passed `go build ./...`, `go test ./...` and `go vet ./...`
through `make check` on a local snapshot matching the working sources. Template
generation was unchanged, integration/live-tag compilation passed, and layering
guards passed both there and in the actual repository. All four pocket cores
passed race tests; focused adapter and scaffold checks passed. Documentation
typecheck and production build passed.

Auth-CMS regression tests exercised local HTTP/TLS authentication, role gates,
guarded mutations, delivery and filtered listing. Events tests exercised real SSE
handlers. A separately run jobs host accepted queued work, completed it and drained
an active job during SIGTERM shutdown. The independent public-boundary review
found no remaining blockers after the recorded fixes.

CMS implementation, SDK, module definitions, SQL and persisted formats are
unchanged from this phase's baseline. Live PostgreSQL/Turso and Firestore
emulator/cloud checks were not rerun; tagged suites were compiled only. External
consumer applications were not edited. Detailed evidence and the final handoff
are in [plans/pocket-structure.md](plans/pocket-structure.md).

## AUDIT-029: Integration contracts and startup behavior

Status: implemented and verified. Unreleased.

Scope: PostgreSQL, Turso, Firestore and Redis connectors; cron and S3 adapters;
authentication/authorization/events SQL store construction; jobs and authorization
Turso connection ownership; scaffold and integration dependency guards. Upgrade
affected connector/store/host source together. No module version, dependency,
SQL migration, persisted-format or CMS implementation changes are introduced.

### SQL store constructors take the host context

These constructors now take `ctx context.Context` as their first argument:

| Store module | Changed constructors |
|---|---|
| authentication/stores/pgx and authentication/stores/turso | `Repositories(ctx, db, ...)` |
| authorization/stores/pgx and authorization/stores/turso | `Repositories(ctx, db, ...)`, `RelationshipRepository(ctx, db, ...)` |
| events/stores/pgx and events/stores/turso | `New(ctx, db, ...)` |

Remaining arguments, options and returns are unchanged. Schema checks previously
used an unbounded background context; the connector's `Open` timeout did not bound
these later queries. Pass the host's startup context/deadline through construction:

```go
startup, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()
repos, err := authorizationpgx.Repositories(startup, db, authorizationpgx.WithAudit())
// Handle err before constructing the pocket services.
```

There are no context-free wrappers. Firestore and jobs constructors do not acquire
this argument. New scaffolded SQL stores follow the context-first form. Existing
generated host code remains host-owned; migrate its probing constructors too.

### PostgreSQL pool defaults and current schema probes

Zero `Config.MaxLifetime` and `MaxIdleTime` now retain DSN values or pgx defaults
(one hour / 30 minutes with the pinned driver). Previously they forced zero and
could retire a connection after every use. Positive fields override DSN values.
Invalid negative settings, overflowing counts, inconsistent minimum/maximum
counts and nonpositive health-check periods are rejected before pool creation.
Hosts should review explicitly configured values; pgx's standard `PG*` environment
fallback still applies during DSN parsing.

Events' PostgreSQL constructor now reads the current payload column type from
the catalog. It no longer mistakes cached pre-migration row metadata for the
current type when migration 0002 is applied on a reused connection.

### Credentials and terminal errors

PostgreSQL redaction now masks URL `password` and `sslpassword` query values
as well as userinfo. Keyword DSNs and malformed inputs are fully redacted rather
than echoed into diagnostics. Connection parsing itself is unchanged.

Turso `Config.AuthToken` is encoded as a query value and overrides URL credentials
under `authToken`, `auth_token` or `jwt`. Reserved bytes now reach the HTTP
Authorization header intact. Redaction masks all three credential aliases;
malformed query strings are rejected at construction and fully redacted for
diagnostics. Hosts that relied on a URL token overriding an explicit AuthToken
must choose the intended credential explicitly.

Firestore deadline mapping preserves `errors.Is(err, context.DeadlineExceeded)`
alongside its SDK classification. Transaction retries now report the actual
terminal begin/cancellation error instead of a stale earlier contention error.
Callback errors retain their identity when the callback itself is the terminal
failure; contention retries and rollback behavior remain intact.

### Connection ownership and Redis deadlines

Redis `Open` enables go-redis's `ContextTimeoutEnabled`, so caller deadlines bound
reads/writes on established connections. `Open` and `StatusCheck` preserve context
failures. Borrowed clients remain unmodified and caller-owned; enable that vendor
option yourself when using `redis.NewClient`. Cancellation without a deadline
still depends on the transport's I/O timeout once a command is in flight.

Jobs and authorization Turso constructors no longer issue ignored
`PRAGMA busy_timeout=5000` calls on an arbitrary borrowed pooled connection.
Hosts own that connection policy; explicit store retry behavior remains. Configure
busy-timeout behavior at the host's driver/connection setup if required.
Apply it to every pooled connection, not by running one PRAGMA on an arbitrary
connection. Local authentication concurrency tests passed with a five-second
timeout; a 50ms timeout produced `SQLITE_BUSY` at transaction start, which that
adapter does not retry. Choose the timeout alongside the host's operation deadlines
and contention requirements; these local results do not establish remote Turso
behavior.

### Migration streams, cron and S3

- PostgreSQL and Turso migration runners read direct SQL files in the selected
  directory, in lexical order, preserving underscore skips. Nested directories
  are separate host-controlled streams; flatten a merged stream explicitly.
  Export copies direct SQL files, excluding unrelated files. Ledger identity and
  existing checksums are unchanged.
- Cron rejects `TZ=`/`CRON_TZ=` prefixes, including malformed inputs that previously
  panicked. The existing contract is UTC-only. `@every` requires a positive whole
  number of seconds; zero, negative or fractional durations are rejected instead
  of silently coerced. The parser directly satisfies the jobs port; no forwarding
  adapter is needed. The vendor's five-year calendar-search horizon remains a
  documented limitation.
- S3 `Open` requires a resolved signing region. Explicit `Config.Region` still
  overrides AWS environment/shared-config defaults; missing region fails during
  construction instead of later signing. No default region is invented.

### Boundaries and verification

The integration guard checks both Go imports and module requirements, allowing
only SDK/self among framework modules. Vendor companion modules remain permitted.
Current adapters retain explicit differences: Firestore callbacks can retry,
authentication Firestore remains partial, SQL transactions require participating
repositories, and provider consistency/optional capabilities are not universal.

All 42 modules passed build/test/vet through `make check` on a snapshot matching
the final code. Actual-repository guards, scaffold checks, documentation
typecheck/build and changed-adapter race tests passed. Disposable PostgreSQL and
Redis suites passed, as did the non-CMS SQL store suites against local SQLite
through pinned libsql. Firestore retry/error regressions exercised the real vendor
loop through an in-process transport; they do not establish cloud conformance.

Exact commands, initial failures, corrections and provider limits are recorded in
[plans/integration-sweep.md](plans/integration-sweep.md). Remote Turso, Firestore
emulator/cloud, real provider delivery/login/storage and collector checks were not
run. External consumer applications, cloud services, publication and deployment
were not modified.

## AUDIT-030: Constructor options

Status: implemented and verified. Unreleased.

Scope: selected SDK, integration, public pocket-service and UI constructors;
existing constructor option families; generated pocket service construction.
Upgrade affected modules and host source together. Required dependencies remain
ordinary arguments. Coherent connection, authentication/authorization policy,
runtime and root composition records remain configuration values. This is a
selective API change, not a conversion of every Config field into a setter.

No module version, dependency, SQL migration or persisted-format change is
introduced. CMS feature work remains deferred; its tests and a view GoDoc example
only receive mechanical SDK/UI caller updates. External applications were not
edited.

### SDK cache and limiter construction

| Old construction | New construction |
|---|---|
| `cacher.New(store, cacher.Config{})` | `cacher.New(store)` |
| `cacher.Config{Namespace: ns, OnError: report}` | Pass `cacher.WithNamespace(ns), cacher.WithOnError(report)` to New |
| `cacher.NewMemory(cacher.MemoryConfig{})` | `cacher.NewMemory()` |
| `cacher.MemoryConfig{MaxEntries: n}` | Pass `cacher.WithMaxEntries(n)` to NewMemory |
| `ratelimiter.NewMemory(ratelimiter.MemoryConfig{})` | `ratelimiter.NewMemory()` |
| `ratelimiter.MemoryConfig{MaxEntries: n}` | Pass `ratelimiter.WithMaxEntries(n)` to NewMemory |

The old cacher Config/MemoryConfig and ratelimiter MemoryConfig types are removed.
Host settings can remain ordinary host structs; translate their fields at the
composition boundary. `cacher.Option` and its separate `MemoryOption` apply only
to their corresponding constructor; rate limiter construction has its own
`MemoryOption`.

```go
store := cacher.NewMemory(cacher.WithMaxEntries(1000))
cache := cacher.New(store,
    cacher.WithNamespace("catalog:v1"),
    cacher.WithOnError(reportCacheFailure),
)
limiter := ratelimiter.NewMemory(ratelimiter.WithMaxEntries(1000))
```

Defaults remain 10,000 entries for omitted/nonpositive bounds. Cache LRU eviction
and limiter refusal to evict active budgets are unchanged. Empty cache namespaces
remain framed; nil storage still selects Noop. A nil error callback disables
reporting. No extra goroutines or lifecycle ownership are introduced.

### SDK worker loggers

The optional logger moves into the existing target-specific option family:

```go
// Before
runner := workers.NewRunner(store, process, logger, workers.WithMaxAttempts(5))
fenced := workers.NewFencedRunner(fencedStore, fencedProcess, logger)

// After
runner := workers.NewRunner(store, process,
    workers.WithRunnerLogger(logger), workers.WithMaxAttempts(5))
fenced := workers.NewFencedRunner(fencedStore, fencedProcess,
    workers.WithFencedLogger(logger))
```

Omitted/nil logger selects slog.Default. Store and processor stay required
positional inputs; processing, retries, middleware and shutdown are unchanged.
When passing an option slice, include the logger option in that slice before
expanding it. `workers.WithLogger` still configures a Pool, not either Runner.

### Redis bus and UI bundle

Redis bus construction becomes `goredis.New(rdb, opts ...BusOption)`. The previous
positional logger and `goredis.Options` record are removed. Map old fields to:
WithStreamPrefix, WithConsumerGroup, WithWorkers, WithQueueSize, WithBlockTimeout,
WithRetryAfter and WithHandlerTimeout; pass the logger with WithLogger.

```go
bus := goredis.New(rdb,
    goredis.WithLogger(logger),
    goredis.WithConsumerGroup("fulfillment"),
    goredis.WithWorkers(8),
)
```

The Redis client remains required and host-owned. Settings resolve before bus
workers start; delivery/timing/defaults and Close ownership are unchanged.
`WithLogging` remains a Redis connection ClientOption for instrumentation, distinct
from the bus's WithLogger. Existing cache/limiter constructor names stay unchanged.

UI construction becomes `goth.New(opts ...Option)`; `goth.Config` is removed:

```go
bundle, err := goth.New(
    goth.WithAssetBasePath("/assets/goth"),
    goth.WithProfile(goth.Full),
    goth.WithThemeStylesheetPath("/assets/app/theme.css"),
)
```

`goth.New()` retains the default asset path, StylesOnly profile and built-in theme.
Final path/profile validation is unchanged. DocumentOptions and other render
values remain structs; authentication/CMS view constructors still take the bundle.

### Focused pocket services

Imports below are relative to `github.com/gopernicus/gopernicus/pockets`:

| Package / constructor | New signature and migration |
|---|---|
| jobs/logic/queue.NewService | `NewService(repos Repositories, opts ...Option)`; replace its Config argument with WithMaxAttempts and WithClock |
| jobs/logic/schedules.NewService | `NewService(repo Repository, opts ...Option)`; pass the old Config.Schedules explicitly, then WithEnqueuer, WithCronParser, WithBatchSize and WithClock as needed |
| authorization/logic/relationships.NewService | `NewService(store Storer, schema Schema, opts ...Option)`; replace Config{Limits: limits} with WithLimits(limits) |
| authentication/logic/delivery.NewService | `NewService(dispatcher Dispatcher, encrypter cryptids.Encrypter)`; remove ServiceDeps and its unused Now field |
| authentication/logic/delivery/command.NewProcessor | `NewProcessor(encrypter cryptids.Encrypter, deliverer Deliverer, opts ...Option)`; replace ProcessorDeps's optional fields with WithInitializer, WithClock and WithPolicy |

Removed construction bags: schedules.Config, relationships.Config,
delivery.ServiceDeps and command.ProcessorDeps. `queue.Config` remains available
as the root pocket's tagged admission-policy value; jobs.New maps it into the
direct queue service's options. Root pocket constructor signatures, their Config
records, large dependency assemblies and runtime configurations remain unchanged.

```go
queueService, err := queue.NewService(repos,
    queue.WithMaxAttempts(cfg.MaxAttempts),
    queue.WithClock(cfg.Clock),
)
scheduleService, err := schedules.NewService(scheduleRepo,
    schedules.WithEnqueuer(queueService),
    schedules.WithCronParser(cronParser),
)
```

Schedule management still works without an enqueuer; occurrence processing needs
one, and only cron expressions need a parser. Limits retain final validation and
normalization. Required and typed-nil dependency checks remain intact.

The delivery admission service's old Now function was stored but never read.
It is removed without a replacement option. The command processor's clock remains
configurable because it controls retry timing; its coherent retry/timeout Config
is passed through WithPolicy. Opaque commands still fail closed when they require
an absent initializer. No provider send, transaction or worker behavior changes.

### Existing options configure construction only

These option types now target private construction settings rather than a usable
runtime object: SDK web StaticOption/SSEOption; bcrypt/JWT Option; Redis
CacheOption/LimiterOption; PostgreSQL LimiterOption; jobs memory Option and Turso
QueueOption; authentication Goth Option. Ordinary `WithFoo(...)` constructor calls
remain valid. Custom functions targeting the old exported concrete object, or
calling an option on a constructed object, no longer compile.

Use supported With functions and constructor-specific option slices for host
presets. Do not apply options to running services. This prevents, for example,
changing a JWT signing method after key-strength validation or removing a limiter's
internal versioned namespace. No generic option interface or compatibility
type-switch overload is provided.

Options apply in order. Scalar replacement and nil/zero argument behavior follow
each option's documentation; collection/vendor/instrumentation options retain
their documented append semantics. Constructors retain independently owned
configuration. Bcrypt option reuse no longer mutates captured cost, and captured
method/option slices are copied where required. Borrowed services, loggers and
clients keep their existing ownership.

A nil Gopernicus constructor option is invalid programming input, not an omission
marker. Constructors returning errors reject it through their existing error
vocabulary; other constructors panic with a specific diagnostic. This replaces
incidental nil-function panics with deliberate diagnostics. Valid options with
documented nil arguments remain valid, such as a default logger or an absent
optional initializer. Vendor options keep the vendor's contract. UI gains no SDK
dependency merely for error classification.

### Scaffold and verification

New generated pocket services use `NewService(store, opts ...Option)` and
`WithIDs(sdk.IDGenerator)` instead of a one-field Config. Root construction
forwards the same typed options after explicit repositories. Entity builders and
store construction remain explicit. Existing generated host code remains
host-owned; this updates future scaffold output.

The convention is documented in ARCHITECTURE.md and replaces the pocket charter's
former Config-only rule. Detailed inventory, retained constructor families,
verification results and separately recorded startup-context follow-ups are in
[plans/constructor-options.md](plans/constructor-options.md).

All 42 modules passed build/test/vet through make check; actual-repository guards,
scaffold generation/build/tests, documentation typecheck/build, targeted SDK/pocket
and changed-adapter race suites passed. The live Redis suite passed against
disposable local instances, which were stopped afterward. Independent source
reviews found no blockers. Live SQL/cloud providers and browser engine suites
were not rerun; the plan records exact coverage and remaining lifecycle items.
No release or external consumer migration was performed.


## AUDIT-031: Consistent pocket constructor options

This entry supersedes AUDIT-030's decision to retain non-CMS pocket root Config
arguments. Upgrade authentication, authorization, jobs and events with their
owned callers. Root assemblies and configurable logic/HTTP/runtime constructors
now use explicit required inputs followed by typed `WithFoo` options. Coherent
feature/policy records remain public data, passed through named options.
CMS and datastore schemas are unchanged. No module version was published.

### Rules shared by the new pocket APIs

- Options configure private construction state, never a live service. A nil
  option is invalid; the changed constructors return their established invalid
  input errors. A documented nil option argument can still mean disabled/default.
- Each feature/policy/middleware/hook option replaces its entire group, including
  zero and nil fields. For example, a second fenced policy containing only a
  timeout resets an earlier lease to the normal default; the final timeout must
  still fit inside that lease. Build one policy value when staging field edits.
- Retained maps/slices are captured when the With function is called; later host
  edits do not change that option. Borrowed services,
  callbacks and their own state remain host-owned. Finish wiring before concurrent
  construction and keep shared callback implementations concurrency-safe.
- Required modes, signers, buses and handler registries have no With option.
  Required related ports can use a named repositories/readers/services record.
  Validation, security defaults, route enablement and lifecycle ownership remain.
- The source filename is `constructor.go`, including authorization root and
  decisions (formerly `construct.go`). Clear existing `New` / `NewService` /
  `NewRuntime` function names are retained. Short constructors can stay in service
  files. Filename changes do not change imports.

### Authentication

Root `Config` is removed. Construction is now:

```go
components, err := authentication.New(repos, signer, runtimeMode, deliveryMode,
    authentication.WithPassword(passwordConfig),
    authentication.WithDelivery(deliveryConfig),
    authentication.WithBrowser(browserConfig),
    authentication.WithLogger(log))
```

`signer` is `cryptids.JWTSigner`; runtime and delivery modes are the existing
`environment.Mode` and `delivery.Mode` types. Move old `TokenSigner`, `RuntimeMode`
and `DeliveryMode` fields to those required arguments. Feature groups retain their
existing field names, defaults, environment tags and conditional requirements:

| Option / record | Former root Config fields |
|---|---|
| `WithPassword(PasswordConfig)` | `Hasher`, `ValidatePassword`, `CompromisedPasswordChecker`, `CompromisedPasswordFailOpen`, `PasswordFlowsDisabled`, `RequireVerifiedEmail` |
| `WithSessions(SessionsConfig)` | `AccessTokenTTL`, `RefreshTTL` |
| `WithIdentity(IdentityConfig)` | `ChallengeProtector`, `IdentifierNormalizer`, `IdentifierKeyer`, `CredentialPolicy` |
| `WithAbuseProtection(AbuseProtectionConfig)` | `AuthenticationLimits`, `RateLimiter` |
| `WithDelivery(DeliveryConfig)` | `Mailer`, `MailFrom`, `BodySenders`, `DeliveryEncrypter`, `DeliveryDispatcher`, `DeliveryEventsEmitter`, `DeliveryJobsAcknowledged`, `DeliveryEphemeralAcknowledged`, `InProcessDelivery` |
| `WithMessages(MessagesConfig)` | `EmailContentTemplates`, `EmailLayouts`, `EmailBranding`, `DeliveryData`, `EmailSubjects`, `SMSBodies` |
| `WithOAuth(OAuthConfig)` | `Providers`, `TokenEncrypter`, `OAuthCallbackBase`, `OAuthNativeRedirectURIs`, `TrustOAuthEmail` |
| `WithPasswordless(PasswordlessConfig)` | `Passwordless`, `PasswordlessProvisionOnRedeem` |
| `WithLinks(LinksConfig)` | `PublicAuthBaseURL`, `PasswordResetURL`, `OAuthLinkBaseURL`, `RedirectAllowlist` |
| `WithBrowser(BrowserConfig)` | `SessionCookie`, `RefreshCookiePath`, `AllowedOrigins`, `BrowserLoginPath`, `BundledRouteAuth`, `Views`, `HTMLPolicy` |
| `WithInvitations(InvitationsConfig)` | `Granter`, `InviteCheck`, `MemberCheck` |
| `WithAdministration(AdministrationConfig)` | `MachineRoutesGate`, `UserAdminCheck`, `ListStrategy` |

`IDs` and `Logger` become `WithIDs` and `WithLogger`. Hosts that want one
loadable settings record can embed the public feature records alongside their
own required mode fields, then supply each group at construction. The demo does
this in `examples/auth-cms/cmd/server/authentication.go`; there is no framework
WithConfig catch-all or compatibility overload. Enabling password, invitation,
OAuth, machine, or delivery features still requires the same paired dependencies.

Direct component construction also changes:

| Old constructor input | New signature / options |
|---|---|
| authentication logic `New(Deps)` | `New(repos Repositories, signer, runtimeMode, limiter, opts ...Option)`; repositories are separate from password/session/identity/delivery/OAuth/passwordless/link policy groups and optional collaborators |
| invitations `New(Deps)` | `New(repo, granter, opts ...Option)`; WithAccess(AccessConfig), WithDelivery(DeliveryConfig), normalizer/redirects/audit/TTL/clock/logger/IDs options |
| HTTP `New(Config)` | `New(service, runtimeMode, opts ...Option)`; WithAuthenticatorPolicy(AuthenticatorPolicy), WithBrowser(BrowserConfig), invitations, list strategy, machine gate and route authentication options |
| HTTP `NewAuthenticator(service, AuthenticatorConfig)` | `NewAuthenticator(service, runtimeMode, opts ...AuthenticatorOption)`; WithCookies, WithBrowserLoginPath, WithLimiter, WithAuthenticatorLogger |
| delivery `NewRouter(Deps)` | `NewRouter(mailer, opts ...RouterOption)`; WithTemplates(TemplatesConfig), WithBodySenders, WithMailFrom, WithDataHook, WithRouterLogger |
| delivery `NewJobsProcessor(JobsProcessorDeps)` | `NewJobsProcessor(encrypter, router, opts ...ProcessorOption)`; WithProcessorInitializer, WithProcessorClock, WithProcessorPolicy(command.Config), WithProcessorObserver |
| delivery `NewInProcessQueue(InProcessQueueConfig)` | `NewInProcessQueue(opts ...QueueOption)`; WithQueueAdmission(QueueAdmissionConfig), WithQueueRetention(QueueRetentionConfig), WithQueueClock |
| delivery `NewInProcessRuntime(queue, processor, InProcessRuntimeConfig)` | `NewInProcessRuntime(queue, processor, opts ...InProcessRuntimeOption)`; WithRetryPolicy(RetryPolicyConfig), WithWorkerCount, WithShutdownDeadline, WithRuntimeLogger |

The old construction-only Deps/Config records in that table are removed.
`delivery.InProcessConfig` remains the coherent root delivery policy, as do cookie,
credential-policy and command-policy values. The credential policy value factory,
simple emitter/logger observer, explicit runtime-component delivery assembly and entity
factories retain their existing forms. Existing store/view options remain.

Browser credential/transport strategies `Accept` and `Transports` now capture
caller slices, so a reused browser option cannot inherit subsequent host edits
that widen accepted credentials or transports. Construct a new strategy to
request a different policy.

### Authorization

Root `New(repos, Config)` becomes `New(repos, opts ...Option)`. The mappings are:

| Former field | Option |
|---|---|
| RelationshipModel | WithRelationshipModel(schema) |
| RoleModel | WithRoleModel(roleModel) |
| Limits | WithLimits(model.EvaluationLimits) |
| Guard | WithGuard(guard) |
| Logger | WithLogger(log) |
| RoleRoutesGate, RoleRouteAssignmentPolicy, ListStrategy | WithRoleRoutes(authorizationhttp.RoleRoutes{Gate, AssignmentPolicy, ListStrategy}) |

The route record has the shorter field names shown above. Models remain
host-authoritative and are snapshotted; relationship/role/guard/route dependencies
retain their existing validation, including malformed or orphan list strategies.
Empty final route/model options clear earlier settings and are validated normally.

Direct services use:

```go
decisions.NewService(decisions.Readers{Relationships: relationshipsService, Roles: rolesService},
    decisions.WithRoleModel(roleModel), decisions.WithLimits(limits))

mutations.NewService(repository,
    mutations.Services{Relationships: relationshipsService, Roles: rolesService},
    mutations.WithRoleModel(compiledRoleModel), mutations.WithGuard(guard),
    mutations.WithLimits(limits), mutations.WithLogger(log))

authorizationhttp.New(authorizationhttp.Services{
    Decisions: decisionsService, Roles: rolesService, Mutations: mutationsService,
}, authorizationhttp.WithRoleRoutes(roleRoutes))
```

Their old Config records are removed. Existing required-only roles and gate
constructors, relationships options, store options and trusted writers remain.
No authorization data or audit-history migration is needed.

### Jobs

Root `jobs.Config` and the obsolete root admission `queue.Config` are removed:

```go
parts, err := jobs.New(repos, jobs.WithMaxAttempts(3),
    jobs.WithCronParser(parser), jobs.WithScheduleBatchSize(20))
// WithClock changes queue admission time; it does not change scheduler time.

runtime, err := queue.NewRuntime(parts.Queue, handlers,
    queue.WithScheduler(parts.Schedules),
    queue.WithRuntimePolicy(queue.RuntimePolicy{Workers: 4}),
    queue.WithRuntimeLogger(log), queue.WithRuntimeJobMiddleware(gate))
```

Load tiny admission/scheduling scalars in the host, then pass the named options.
`RuntimeConfig` is replaced by `RuntimePolicy` for Workers, PollInterval,
IdleInterval and Heartbeat. Handlers is the required second argument; scheduler
is optional via WithScheduler. Logger, WorkerMiddleware and JobMiddleware become
WithRuntimeLogger, WithRuntimeWorkerMiddleware and WithRuntimeJobMiddleware.

`NewFencedRuntime(parts.Queue, handlers, opts ...FencedRuntimeOption)` similarly
requires the fenced handler map. `FencedRuntimeConfig` is replaced by
`FencedRuntimePolicy` for Workers, PollInterval, IdleInterval, LeaseFor,
ProcessTimeout, MaxAttempts and Backoff. Use WithFencedRuntimePolicy,
WithFencedRuntimeLogger, WithFencedRuntimeClock, WithDeadLetters,
WithFencedWorkerMiddleware and WithFencedJobMiddleware for the former fields.
Both policy records retain their JOBS_* tags. Handler maps remain immutable
snapshots per runtime; middleware/hook options replace their entire collection.

The auth-cms host adapter now exposes `authjobs.NewRuntime(queueService,
deliveryRuntime, opts ...queue.FencedRuntimeOption)` instead of producing an
arbitrarily mutable framework config. It keeps Handle/Discard under the same
kind; the host still starts and stops the resulting runtime. The demo retains
its explicit 20-second provider timeout within the default 30-second lease.

### Events

`events.Config` and its optional-only `Repositories` wrapper are removed.
Required bus construction is `events.New(bus, opts ...Option)`; use WithOutbox
for the former Repositories.Outbox. The host still runs the poller separately.

| Former Config fields | Options / grouped values |
|---|---|
| Visible, Projector, Logger | WithVisibility, WithProjector, WithLogger |
| BufferSize, MaxConnsPerSubject | WithStreamLimits(streams.Limits{...}) |
| Heartbeat, MaxConnAge | WithHTTPPolicy(eventshttp.Policy{...}) |
| Authorize, StreamMiddleware | WithAuthorization, WithStreamMiddleware(middleware...) |

Direct stream construction is `streams.New(bus, opts ...Option)` with
WithVisibility, WithProjector, WithLogger and WithLimits(streams.Limits).
Direct HTTP construction is `eventshttp.New(service, opts ...Option)` with
WithPolicy(eventshttp.Policy), WithAuthorization, WithMiddleware and WithLogger.
Their old Config bags are removed; Limits and Policy retain the EVENTS_* tags.

New still subscribes once immediately. HTTP remains optional; missing visibility
or invalid HTTP middleware is rejected by HTTP/registration/direct-stream checks
as before. Missing resource authorization omits resource routes. HTTP adapters
and repeated HTTP() calls do not subscribe again. Stop stream consumers before
closing components; the bus remains host-owned.

Implementation plan and exact verification status:
[plans/pocket-constructor-options.md](plans/pocket-constructor-options.md).

The exact final source passed the 42-module build/test/vet, generation/scaffold
and guard gate. Actual-workspace guards, all four pocket core race suites,
migrated host HTTP/jobs tests and documentation typecheck/build passed.
Live SQL/cloud providers and browser-engine suites were not rerun in this phase.


## AUDIT-032: Host startup cancellation and constructor errors

- **Implemented:** 2026-09-11.
- **Modules:** PostgreSQL/Turso connectors, SendGrid, S3, events core and affected
  authentication/authorization/jobs store adapters.
- **Release:** coordinated versions are being prepared in
  [plans/audit-release-manifest.json](plans/audit-release-manifest.json).
  Firestore's three first releases remain held for remote-work reconciliation
  and required live verification.
- **Impact:** context-first startup APIs, constructor error returns, explicit nil
  database preconditions. No schema or stored-format change in this entry.

### Carry the host's startup context

| Previous usage | Replacement |
|---|---|
| `pgxdb.Open(cfg)` | `pgxdb.Open(ctx, cfg)` |
| `turso.Open(cfg)` | `turso.Open(ctx, cfg)` |
| authentication Firestore `Repositories(db, opts...)` | `Repositories(ctx, db, opts...)` |
| authorization Firestore `Repositories(db, opts...)` | `Repositories(ctx, db, opts...)` |
| authorization Firestore `RelationshipRepository(db, opts...)` | `RelationshipRepository(ctx, db, opts...)` |

Pass the host startup/signal context. SQL Open bounds startup with the earlier of
that context and ConnectTimeout (unchanged 10-second default). Firestore keeps a
30-second cap per index probe and honors shorter host deadlines. Cancellation
is checked even when an index probe is disabled. Returned databases and stores
do not retain the startup context as their lifetime; closing a database remains
the owner's responsibility. A failed SQL Open closes any connection resource it
created. Firestore construction continues to borrow the supplied connector.

Turso HTTP/libSQL startup cancellation is verified. The pinned libsql driver's
WebSocket connection handshake does not honor context; forwarding ctx does not
repair that upstream limitation. Prefer its HTTP/libSQL transport when startup
must be cancellable.

### Handle construction errors before wiring runtime work

These constructors now return two values:

```go
sender, err := sendgrid.New(sendgrid.Config{APIKey: apiKey, FromName: name})
if err != nil {
    return err
}
store, err := s3.New(client, bucket)
if err != nil {
    return err
}
poller, err := outbox.NewPoller(repository, bus.Dispatch)
if err != nil {
    return err
}
```

- SendGrid rejects missing/blank API keys, line breaks in APIKey/FromName, and
  invalid HTTP(S) origins with `sdk.ErrInvalidInput`. It no longer retains a
  configuration error until Send. New performs no network request and does not
  verify the key with the provider. HTTPS/development-HTTP posture, client copy,
  redirect refusal and send error mapping are unchanged.
- S3.New rejects a nil client or blank bucket with `sdk.ErrInvalidInput`, before
  creating a presigner. It performs no I/O and leaves custom signing, transport
  and client ownership with the host. Open retains its configuration-loading and
  signing-region checks; its incomplete static credentials/empty bucket errors
  now also wrap `sdk.ErrInvalidInput`.
- NewPoller rejects missing/typed-nil repositories, nil delivery functions and
  nil options with `sdk.ErrInvalidInput`. It never calls the repository or sender
  during construction. Nonpositive batch sizes still select the documented
  default. Poll still delivers before marking published; cancellation and failed
  marking retain work for replay. The host registers handlers and owns lifecycle.

Raw authentication/jobs SQL NewXStore wrappers keep their existing return types.
Passing a nil borrowed database now produces a clear construction-time panic
instead of a later nil dereference. Existing error-returning authentication and
authorization SQL probing constructors reject nil databases with
`sdk.ErrInvalidInput`. Supply an initialized database; a zero wrapper is not a
substitute for a live connector when performing I/O. No generic constructor
framework or new connection probes were added.

### Verification

Focused build/test/vet and race checks passed for changed connectors/stores,
SendGrid, S3 and events. Disposable PostgreSQL and local SQLite checks verify a
returned database remains usable after its startup context is canceled. HTTP
fixtures verify cancellation, delivery and credential/redirect behavior. The
release's exact-source workspace/module/browser and consumer checks are tracked
in [plans/startup-release-segovia.md](plans/startup-release-segovia.md); no cloud
Firestore verification is implied by the constructor tests.


## AUDIT-033: Turso statement completion before success

- **Implemented:** 2026-09-11, during the pre-release live checks.
- **Module:** `integrations/datastores/turso`, target `v0.4.0`, and its dependent
  adapters in the coordinated release table.
- **Impact:** failed statement finalization is returned as an error; no exported
  signature, schema or stored-format change.

`QueryOne` previously returned a scanned `UPDATE ... RETURNING` row while ignoring
an error from closing its result. SQLite can produce that row before the implicit
transaction commits. Under contention, a busy failure at close could therefore
report a successful claim which was not committed, allowing another worker to
claim the same job.

The helper now requires successful result closure before returning the row. A
finalization failure returns the mapped driver error and a zero result. Existing
store-owned busy retry handles the failed statement. Explicit transactions still
commit through their owner; this change does not make an in-transaction row a
promise that the transaction has committed.

Upgrade the Turso connector and adapters together. Host code already checking
returned errors needs no API migration. Custom QueryOne callers must continue to
check the error before using the returned value.

A deterministic real-SQLite reader-lock regression demonstrated the old false
success, and now verifies the error, unchanged persisted state and successful
retry after the lock is released. Connector build/race/vet and HTTP libsql fixture
checks pass. Twenty repeated concurrent-claim runs and all six SQLite
integration/race suites also pass, with unchanged contention settings. Full
release evidence is in [plans/startup-release-segovia.md](plans/startup-release-segovia.md).
