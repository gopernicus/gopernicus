# sdk

`sdk` is the stdlib-only kernel of the gopernicus framework — its `go.mod` has
no `require` block, so "imports only the standard library" is enforced
structurally, not just by convention. Each subpackage owns one concern as a
small **service + the port(s) it needs**: the interface that adapters target,
a service struct that owns cross-cutting policy (logging, tracing, error
mapping), the shared types/errors for that concern, and a `New(...)`
constructor. Adapters (concrete implementations) live in `integrations/`
(reusable), `features/<name>/stores/<dialect>` (feature store adapters), and
`internal/outbound/` (app-specific) and depend on sdk — never the reverse.

`sdk` is **not** an interfaces-only package. The value is the behavior and
vocabulary the service structs own; the interfaces are how adapters plug in.

## The import rule

`sdk` is the adapter between the **standard library** and the application. It
imports **only** the standard library and other `sdk` packages — **never** an
external module (`github.com/…`, `cloud.google.com/…`, `golang.org/x/…`). A
concern that needs a third-party driver/SDK keeps the *generic seam* in `sdk`
and the *concrete dependency* in `integrations/`:

- the libSQL driver + `database/sql` plumbing live in
  `integrations/datastores/turso`, never in `sdk`.
- `sdk/web.Render` takes a local `Renderer` interface; `templ.Component`
  satisfies it implicitly, so `templ` stays out of `sdk`.

Enforced by `make check`.

## Naming criteria

Architectural words ("port", "adapter") describe *roles*, not type names. Names
describe *behavior*.

| Layer | Rule | Examples |
|---|---|---|
| Port (interface) | role/capability, `-er` suffix where it reads naturally — **never** `Port` | `Storer`, `SignedURLer`, `ResumableUploader`, `Resolver` |
| Service (the type apps use) | domain noun | `Cache`, `FileStore`, `RateLimiter` |
| Adapter (implementation) | the technology; the package name carries the "adapter" meaning | `redis`, `gcs` |

"Port-ness" is conveyed by **position** — it's the interface the service struct
*accepts* — not by the name. A port that some backends can't fully implement
should be **segregated** into optional capability interfaces rather than forcing
`ErrNotSupported` stubs (see `filestorage`: `Storer` + `ResumableUploader` +
`SignedURLer`). Consumers may still declare their own narrow interface locally
for the subset they use; Go satisfies it implicitly.

## Admission policy — what belongs in sdk

Promote a concern into `sdk` only when **all** hold:

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
| `config` | `.env` + environment loading, plus `ParseEnvTags` struct population from `env:`/`default:`/`required:` tags (no deps) |
| `logging` | `slog` setup + request/trace/span-id-from-context handler |
| `errs` | transport-agnostic sentinel errors (`ErrNotFound`, …) |
| `web` | generic HTTP primitives: handler/route groups + verb sugar, middleware (request-id, tracing, logger, panic recovery, CORS, default headers — place `Tracing` outer of `Logger` so the traced context reaches the access log line and `RecordError` keeps landing on Logger's writer), error→status mapping, response helpers (SSR + JSON kit), request decoding (`DecodeJSON` + auto-validate), SSE streaming, static/SPA file server, app-driven OpenAPI 3.1 builder, page caching, and the `templ` render seam |
| `crud` | optional generic CRUD contract (`Reader[T,F]`/`Writer`/`CRUD`), `Page`/`ListRequest`, ordering, strict-or-clamping limit parsing, cursor codec |
| `id` | dependency-free random IDs (`crypto/rand`) |
| `slug` | pure URL-safe slug generation with accent folding (no domain knowledge) |
| `email` | `Sender`/`Message` port — wired defaults `SMTP` (`net/smtp`, multipart text+HTML) and `Console` (dev logger); optional template layer (`TemplateRegistry`/`Emailer` with layered layouts + branding); SendGrid backend in `integrations/email/sendgrid` |
| `validation` | composable field validators (`Required`, `Email`, `UUID`, `PasswordStrength`, …) + `Errors` accumulator; composes with `web.FieldErrors.AddErr` (doc-only, no import edge) |
| `async` | bounded fire-and-forget goroutine pool for request-scoped side work — no polling, no jobs (that's `workers`) |
| `conversion` | representation utilities: `Ptr`/`Deref` generics, acronym-aware case conversion (custom acronyms via the immutable `Caser`), strict/flexible datetime parsing, nil-safe JSON helpers, `Overlap` |
| `tracing` | minimal span port (`Tracer`/`SpanFinisher`) + `Noop` default — OpenTelemetry backend in `integrations/tracing/otel` (stdout/OTLP-gRPC exporters) |
| `cryptids` | `Encrypter` port + `AESGCM` default, `SHA256Hasher` (API keys — never passwords), `JWTSigner` port — golang-jwt backend in `integrations/cryptids/golang-jwt` |
| `oauth` | OAuth 2.0/OIDC `Provider` port + PKCE S256 helper — providers live in `integrations/oauth/*` (no vendor-neutral default exists) |
| `events` | in-process event bus port (`Bus`/`Broadcaster`/`Emitter`/`TypedHandler[T]`) + `Memory` default, `Noop`, `WakeChannel` — with `eventstest` conformance suite; the durable-outbox + SSE-gateway consumer is `features/events` |
| `identity` | request-identity **vocabulary** (A-I1, 2026-07-07): `Principal{Type, ID}`, `User`/`ServiceAccount`, `WithPrincipal`/`FromContext` — no middleware, no defaults, no authorization vocabulary (those live with the credential owners, e.g. `features/authentication`). Admission trace: plurality (authentication stashes, the events gateway reads), narrow + stable (one struct, two funcs, two constants), shared policy (absence means deny/401 — fails closed) |
| `cacher`, `filestorage` | facility ports — wired defaults `cacher.Memory` (used by every example) and `filestorage.Disk` (used by `examples/cms`; `examples/minimal` leaves blob storage unset); GCS/S3 backends in `integrations/filestorage/{gcs,s3}` |
| `ratelimiter` | facility port — wired default `ratelimiter.Memory` (D6/phase-2); first real consumer is `features/authentication`'s login-attempt limiting; `Acquire` is the blocking counterpart for workers (waits on `RetryAfter` instead of rejecting — no separate throttler port) |
| `workers` | facility: worker pool (adaptive polling, coalesced wake channel, middleware, panic recovery, graceful drain) + generic `Runner[T Job]` (claim → hooks → process → complete/fail); first consumer is `features/jobs`' runtime |
| `feature` | the host↔feature pluggability contract (`Mount`, `RouteRegistrar`) — see [ARCHITECTURE.md](../ARCHITECTURE.md)'s Features section and the full charter, [features/README.md](../features/README.md) |

## Not responsible for

- **CMS-specific** HTTP transport: the route table, concrete handlers, and the
  `templ` views live in `features/cms/internal/http`. `sdk/web` owns only the
  reusable transport primitives above (middleware, response/error helpers,
  server config types, the render seam) — it never knows an app's routes or
  pages.
- Concrete infrastructure clients (Redis, GCS, SQL drivers) — those are adapters.
- App-specific domain schemas, services, or business rules.
- A `Port` interface over a concrete handle like `*sql.DB` (share the type/config
  instead; abstract the repository above it only if the dialect actually varies).
- Single-sink integrations (e.g. a lone Slack notifier) until a second sink or a
  test no-op actually requires the seam.
