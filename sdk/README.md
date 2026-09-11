# sdk

`sdk` is the stdlib-only core of the gopernicus framework. Root `package sdk`
contains common vocabulary and small, clearly named primitives. Packages under `pkg/` group coherent mechanisms; capabilities define replaceable contracts
and their observable behavior. A helper does not need a separate package solely
because it is a different operation.

The SDK includes implementations as well as interfaces. External-library adapters
live in integrations, pocket stores or host outbound packages and depend inward
on SDK. The SDK module has no third-party requirements; import guards enforce its
stdlib boundary.

## The layering law (sdk-layering, 2026-07-10)

The module is LAYERED, and the layers are physical:

- **The kernel** is root `package sdk`: errors/faults, request/trace/span and
  principal context values, identity projections, configurable ID generation,
  pointer reads and slug generation. It imports stdlib only and no SDK
  subpackage; guard G12(a) enforces that boundary.
  **Root admission:** common vocabulary or a small primitive whose explicit
  name is understandable without a subsystem qualifier; useful across
  applications, with no transport/application lifecycle. A candidate need not
  already be used by two packages under `pkg/`. This replaces the narrower
  two-consumer criterion by owner decision (2026-09-10).
  Keep subject areas in separate files, preserve host-owned configuration, and
  retain named packages when their namespace explains a coherent API.
- **`pkg/`** — reusable mechanisms and vocabulary (web, workers, list,
  cryptids, validation, logging, async, environment). Packages under `pkg/`
  import the ROOT only — the tier is FLAT. Validation and environment keep
  their useful namespaces; cryptids means "cryptography tidbits".
- **`capabilities/`** — behavioral ports + the observable policy that
  goes with them, pinned by conformance tests (cacher, tracing, email,
  notify, oauth, filestorage, ratelimiter, events, work, transaction). A capability
  MAY ship a first-party stdlib default (`cacher.Memory`, `email.Console`),
  or its implementation of record may live in an integration
  (`oauth` → `integrations/oauth/*`) or a pocket
  (`work` → `pockets/jobs`) — defaults are optional, not definitional
  (sdk-work-protocol, 2026-07-13).
  Capabilities import root + pkg and their own subsystem subpackages.
  For example, notify/email provides typed email within notify and adapts it into
  a selected Delivery. Different capabilities do not import each other. A
  capability's web middleware lives with the capability (cacher.Pages,
  tracing.Middleware), never in web.

The shared host/pocket contract lives in the separate `pockets` module, which
depends on SDK. SDK never imports it.

Guard G12 enforces these boundaries in both production and test code.

## The import rule

`sdk` is the adapter between the **standard library** and the application. It
imports **only** the standard library and other `sdk` packages — **never** an
external module (`github.com/…`, `cloud.google.com/…`, `golang.org/x/…`). A
concern that needs a third-party driver/SDK keeps the *generic seam* in `sdk`
and the *concrete dependency* in `integrations/`:

- the libSQL driver + `database/sql` plumbing live in
  `integrations/datastores/turso`, never in `sdk`.
- `sdk/pkg/web.Render` takes a local `Renderer` interface; `templ.Component`
  satisfies it implicitly, so `templ` stays out of `sdk`.

Enforced by `make check`.

## Naming criteria

Architectural words ("port", "adapter") describe *roles*, not type names. Names
describe *behavior*.

| Layer | Rule | Examples |
|---|---|---|
| Port (interface) | role/capability, `-er` suffix where it reads naturally — **never** `Port` | `Storer`, `SignedURLer`, `ResumableUploader`, `Resolver` |
| Service (when shared behavior needs a struct) | domain noun | `Cache` |
| Adapter (implementation) | the technology; the package name carries the "adapter" meaning | `redis`, `gcs` |

A port describes what its consumer accepts. A capability does not need a
service wrapper when callers can use the adapter through that interface. A port
that some backends can't fully implement should be **segregated** into optional capability interfaces rather than forcing
`ErrNotSupported` stubs (see `filestorage`: `Storer` + `ResumableUploader` +
`SignedURLer`). Consumers may still declare their own narrow interface locally
for the subset they use; Go satisfies it implicitly.

## Admission policy — what belongs in sdk

For a replaceable capability or service, require **all** of the following.
Small root primitives use the root-admission criteria above; they do not need an
artificial interface or multiple implementations.

1. **Plurality or test-seam** — two+ real implementations exist or are genuinely
   foreseen, or it must be faked across many packages in tests.
2. **The port is narrow and stable** — expressible without leaking backend
   specifics.
3. **There's shared policy or vocabulary worth owning** — logging/tracing/error
   mapping, or genuinely shared types.

Keep it app-local instead when: there's one implementation unlikely to grow
(define a 1-method interface at the consumer if you need to test it); the port
can't be expressed without backend capability flags; or it's a concrete
dependency with no policy variance (share the *type*, don't wrap it in an
interface).

## Packages

| package | concern |
|---|---|
| `environment` | Checked single-line dotenv loading; `ParseEnvTags` for typed config with env > nonzero field > default precedence (empty env counts as absent); `Secret`/`DecodeSecret` for hex secrets with a minimum-byte floor; explicit `Mode` deployment posture and validation. Hosts own missing-secret policy and choose the mode key. See [package docs](../workshop/documentation/docs/sdk/pkg.md#environment-and-deployment-posture) for parsing and precedence rules. |
| `logging` | `New(Options)` returns standard `*slog.Logger` with automatic request/trace/span IDs from context; `ContextHandler` wraps custom handlers. Empty/unknown options default to INFO, JSON, and STDERR. |
| **root `package sdk`** | Error/fault vocabulary; request/trace/span context; `Principal`, identity info/address/resolver and principal context; `IDGenerator`/`NanoID`/`DatabaseID`; `Deref`/`DerefOr`; `Slugify`. See the [root API guide](../workshop/documentation/docs/sdk/overview.md#root-api). |
| `web` | ServeMux routing/groups, JSON decoding/responses and domain-error mapping, HTML Renderer/Template, HTTP middleware/status/error recording, SSE, static/SPA serving and server lifecycle. Hosts own middleware configuration, body limits and OpenAPI documents; caching/tracing middleware live in their capabilities. |
| `list` | `Request`, `Page[T]`, per-resource `Limits`, cursor/offset pagination, ordering/search allow-lists, strict query parsing and row mapping. See the [listing contract](../workshop/documentation/docs/sdk/pkg.md#listing-vocabulary). Domain packages own repository methods and update inputs. |
| `transaction` | Capability exposing `Transactor`; SQL and Firestore connectors implement it. Repositories must participate through the callback context. The callback may retry; external effects follow a successful return. |
| `notify/email` | Typed `Message`/`Sender`, SMTP and development Console; `NewDelivery(sender, message)` preserves full HTML/text for an explicit `notify.Send` call. Optional `NewRenderer` and `Emailer` retain layered content/layouts and branding. Plain-text templates use text/template and are required. SMTP honors cancellation and encodes MIME; SendGrid lives in `integrations/email/sendgrid`. |
| `validation` | explicit field checks (`Required`, `Email`, `UUID`, `MinLength`, …) return optional `*sdk.Violation` data; collect with `sdk.ValidationError.AddViolation` and return `Err()` from DTO or domain validation. Length checks count Unicode code points; optional pointer checks skip nil and use the scalar rule. Password policy belongs to authentication or the host |
| `async` | bounded in-process task pool; explicit admission and `Close(ctx)` drain. Tasks may outlive a request; no persistence or automatic task cancellation |
| `tracing` | minimal span port (`Tracer`/`SpanFinisher`), optional HTTP metadata seams + `Noop` default — OpenTelemetry backend in `integrations/tracing/otel` (stdout/OTLP-gRPC exporters) |
| `cryptids` | `Encrypter` with `AESGCM`, the `SHA256` digest function, and `JWTSigner`; JWT implementation lives in `integrations/cryptids/golang-jwt` |
| `oauth` | OAuth 2.0/OIDC `Provider` port, explicit authorization request, optional OIDC/refresh + PKCE S256 helper — providers live in `integrations/oauth/*` (no vendor-neutral default exists) |
| `events` | bounded notifications (`Emitter`/`Subscriber`/`Bus`), explicit local `Memory.Dispatch`, `Record`/`RemoteEvent` envelope with stable identity, `Noop`, `TypedHandler`, and `WakeChannel`; Redis has checked `Publish` and separate `SubscribeWork`. `eventstest` verifies the common notification contract; `pockets/events` supplies outbox polling + SSE |
| `notify` | `Delivery`, `DeliveryFunc` and `Send(ctx, deliveries...)`: caller-selected sequential delivery, ordinary failures collected by index, cancellation skips remaining work. No registry, identity routing or implicit retries. `SendError` preserves causes and attempted/skipped failures. Shared `Capabilities`, `InspectTransport` and `CheckTransport` own production posture; body Console is development-only. |
| `cacher` | owned byte storage, optional literal prefix deletion, bounded Memory/Noop, namespaced Cache with JSON/load helpers, and public HTML Pages; Redis in `integrations/kvstores/goredis`. Hosts own TTL, scope, error reporting and invalidation. [Cache contract and examples](../workshop/documentation/docs/sdk/capabilities.md#caching-application-data-and-public-pages) |
| `filestorage` | seven-operation `Storer`, portable key/range/error rules, optional signed URLs and client PUT sessions; confined, staged `Disk` default with host-owned `Close`; GCS/S3 integrations. Use the adapter directly; no FileStore wrapper. [Storage contract and ownership](../workshop/documentation/docs/sdk/capabilities.md#file-storage-and-object-ownership) |
| `ratelimiter` | keyed `Allower` admission and `Limiter` reset, bounded `NewMemory(opts ...MemoryOption)` with `WithMaxEntries`, host-configured HTTP middleware, and blocking `Acquire` for workers; shared two-window approximation with Redis/Postgres |
| `workers` | Generic polling Pool, worker middleware, and `Runner`/`FencedRunner` over host-supplied store interfaces. Job middleware wraps processing with explicit defer/reject outcomes. Jobs supplies one queue implementation; custom queues need only SDK. [Execution contract](../workshop/documentation/docs/sdk/pkg.md#workers-versus-jobs) |
| `work` | the keyed-work submission **protocol** (sdk-work-protocol, 2026-07-13): typed lifecycle `Status` with the frozen seven-value vocabulary (`pending`/`running`/`completed`/`failed`/`dead_letter`/`canceled`/`superseded`; `failed` is NON-terminal — retryable; `Terminal()`/`Known()` predicates), segregated consumer ports `Enqueuer` (idempotent admission under a nonempty logical key), `Replacer` (optional atomic replace/supersede), `StatusReader` (deterministic latest-by-key), opaque `[]byte` payload — NO default implementation (the oauth posture); the implementation of record is `pockets/jobs`; `worktest` ships the conformance suite. Executor-side (claim/lease/checkpoint/fencing) stays in `pkg/workers` + the jobs domain |
| `pocket` | the host↔pocket pluggability contract (`Mount`, `RouteRegistrar`) — see [ARCHITECTURE.md](../ARCHITECTURE.md)'s Pockets section and the full charter, [pockets/README.md](../pockets/README.md) |

## Not responsible for

- **CMS-specific** HTTP transport: the route table, concrete handlers, and the
  `templ` views live in `pockets/cms/internal/http`. `sdk/pkg/web` owns only the
  reusable transport primitives above (middleware, response/error helpers,
  server config types, the render seam) — it never knows an app's routes or
  pages.
- Concrete infrastructure clients (Redis, GCS, SQL drivers) — those are adapters.
- App-specific domain schemas, services, or business rules.
- A `Port` interface over a concrete handle like `*sql.DB` (share the type/config
  instead; abstract the repository above it only if the dialect actually varies).
- Single-sink integrations (e.g. a lone Slack notifier) until a second sink or a
  test no-op actually requires the seam.
