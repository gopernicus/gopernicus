---
title: SDK packages
description: Pure mechanisms under sdk/pkg, which one to reach for, and the rules each enforces.
---

# SDK packages

Packages under `sdk/pkg/` provide reusable mechanism and data vocabulary with no
service semantics. The tier is flat: a package here may import the root kernel
but never another package under `pkg/`.

## Which package

| Package | Reach for it when | Key entry points |
|---|---|---|
| `environment` | loading `.env`, parsing tagged config structs, reading secrets, choosing development vs production | `LoadEnv`, `ParseEnvTags`, `Secret`, `ParseMode` |
| `logging` | you want a `*slog.Logger` that carries request/trace IDs from context | `New`, `NewContextHandler`, `Options` |
| `web` | anything HTTP: routing, middleware, JSON and HTML responses, SSE, static files, server run | see the [Web package](web.md) page |
| `list` | paginated listings at an HTTP edge or in a store adapter | `ParseQuery`, `ParseOrder`, `Request`, `Page[T]`, `TrimPage` |
| `validation` | field checks that produce client-safe violations | `Required`, `MaxLength`, `Email`, `IfSet` |
| `cryptids` | digests, AES-GCM envelopes, or the JWT signing contract | `SHA256`, `NewAESGCM`, `JWTSigner` |
| `workers` | a worker pool or claim/process/complete runner over your own store | `NewPool`, `NewRunner`, `NewFencedRunner` |
| `async` | bounded fire-and-forget tasks inside one process | `GoContext`, `Close`, `Wait` |

Pointer reads, slugs, IDs and identity vocabulary are in the root package; see
the [root API](overview.md#root-api).

## environment

**Load a file, then parse tags.**

```go
type Config struct {
    Host    string        `env:"HOST" default:"localhost"`
    Port    string        `env:"PORT" default:"8080"`
    Timeout time.Duration `env:"TIMEOUT" default:"10s"`
}

if err := environment.LoadEnv(); err != nil {   // .env in the working directory; LoadPath takes another path
    return err
}
var cfg Config
if err := environment.ParseEnvTags("", &cfg); err != nil {
    return err
}
```

`web.ServerConfig`, `logging.Options` and pocket configs already carry tags, so
a host parses them the same way and can pre-seed its own defaults first:

```go
logOpts := logging.Options{Format: "text"}   // host default; env wins, KEY= keeps it
if err := environment.ParseEnvTags("", &logOpts); err != nil {
    return err
}
```

**File format.** Single-line `KEY=value`, optional `export ` prefix, literal
values. Single or double quotes preserve spaces and hashes; a space or tab
followed by `#` outside quotes starts a comment. Malformed lines, keys with
whitespace or NUL, unterminated quotes and trailing text after a closing quote
return errors with file and line context. There is no escaping, expansion or
multiline syntax. Missing files are allowed; existing process variables,
including empty ones, are preserved. Check the returned error: earlier lines
stay applied if a later one fails.

**Precedence** is nonempty env value, then existing nonzero field, then
`default` tag. Empty env values count as absent, so an explicit `false`, zero or
empty slice cannot override a nonzero default through a pre-seeded literal;
apply such overrides in code after parsing. `required:"true"` needs a nonempty
env value even if the field was pre-seeded. `ParseEnvTags` fills tagged scalars,
string slices and named string types, descends into untagged nested structs and
skips untagged pointers and interfaces; a tagged unsupported type is an error
even when its variable is absent. Errors name the full key, field path and type
without retaining rejected values. `GetEnvOrDefault` deliberately uses raw
lookup: a present empty value wins over its fallback.

**Secrets and posture.** `Secret(key, minBytes)` reads a hex-encoded secret with
a minimum decoded length and returns nil when unset or empty; the consuming
constructor decides whether missing is allowed. Deployment posture is a required
two-value vocabulary, development or production. `ParseMode` reads no implicit
variable; the host names the key. Preview, staging and CI normally map to
production, because permissive behavior should be an explicit development choice.

## logging

```go
log := logging.New(logging.Options{Format: "text"})
ctx := sdk.WithRequestID(context.Background(), "request-123")
log.InfoContext(ctx, "request handled")            // includes request_id

custom := slog.New(logging.NewContextHandler(slog.NewJSONHandler(output, nil)))
disabled := slog.New(slog.DiscardHandler)
```

`logging.New` returns a standard `*slog.Logger`: INFO, JSON, STDERR by default.
`Options` carries `LOG_LEVEL`, `LOG_FORMAT` and `LOG_OUTPUT` tags for the host
to parse; construction reads no environment and leaves slog's global default
alone. Unknown values keep the defaults. Context-aware calls add `request_id`,
`trace_id` and `span_id` when present, under the logger's current `WithGroup`,
without starting a tracer. Use `NewContextHandler` around your own handler, or a
plain handler if you do not want the IDs.

Create the configured logger once, pass it into `run(ctx, log)`, and use it for
returned startup errors. `slog.DiscardHandler` disables every level; argument
expressions still evaluate, but disabled calls do not resolve `LogValuer`s.

## list

**At the HTTP edge**, parse the query, then apply the resource's order allow-list:

```go
req, err := list.ParseQuery(r.URL.Query(), list.QueryOptions{})
if err == nil {
    req.Order, err = list.ParseOrder(orderFields,
        r.URL.Query().Get(list.QueryKeyOrder), defaultOrder)
}
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

`ParseQuery` reads `limit`, `cursor`, `offset`, `count` and `q`. Blank values
are absent; invalid limits, offsets, booleans and strategies return errors
wrapping `sdk.ErrInvalidInput`. Store code uses `Request.Validate` for strategy
consistency and `NormalizedLimit` for defaults and clamping: zero resolves to 25,
the ceiling is 100.

**Paging.** Cursor mode is the default and offset mode must be explicit (it
emits no cursors). Fetch `limit+1`, then `TrimPage` encodes the last returned
item as `NextCursor` when another page exists. For previous navigation, fetch up
to `limit+1` records at or before the incoming boundary, restore normal order
and call `MarkPrevPage`: any record sets `HasPrev`, an extra predecessor
supplies `PreviousCursor`, and an empty previous cursor opens the first page.
`WithCount` requests the whole filtered count.

Cursors are padded base64url JSON with tagged scalar and time values. Malformed
tokens are invalid input; a well-formed token for a different order field resets
to the first page. Convert named scalar types before encoding. Legacy untagged
numbers are accepted only within ±(2^53−1); tagged integers keep the full
int64/uint64 range. Cursors are positions, not credentials.

**In SQL adapters**, order the projected SELECT output on every page and add
search and cursor predicates outside that SELECT. Project each search, order
and primary-key field under an unqualified output name (`t.created_at AS
created_at`, `Column: "created_at"`), include it in the row scanner, and return
that projected value from `OrderValueOf`. Keyset ordering needs non-null order
and key values across the matched population; use a non-null key or offset mode
for nullable ordering. A cursor with a null order value, or a non-string
case-folded value, is invalid input. The helper does not inspect the schema.

`Items`, `MapItems`, `TrimPage`, `MapPage` and `MapPageErr` normalize empty
items to `[]`; a directly constructed `Page[T]{}` emits `null`. Optional page
fields are omitted and clients read missing flags as false. Domain packages
declare their own repository methods, filters and update inputs; transactions
live in the [`transaction` capability](capabilities.md#transactions).

## validation

```go
func (in *createWidget) Validate() error {
    var problems sdk.ValidationError
    problems.AddViolation(validation.Required("name", in.Name))
    problems.AddViolation(validation.MaxLength("name", in.Name, 80))
    return problems.Err()
}
```

Helpers return nil or one field violation; `Err()` returns an error only when
problems were collected, and that error matches `sdk.ErrInvalidInput` through
`errors.Is`, including after `%w` wrapping. Web responders preserve every field,
message and optional code. Collect before wrapping: `errors.Join` does not
merge independent field lists into one response.

Length checks count Unicode code points without normalizing. Optional string
checks accept empty strings; use `Required` for presence. Pointer variants skip
nil. `IfSet` takes a callback returning `*sdk.Violation` for custom optional
checks. Domain rules use `problems.Add(field, code, message)`; keep unexpected
dependency errors as ordinary errors, since their messages do not belong in
public violations. Password policy belongs to authentication or the host.
`Email` accepts mail address syntax including display names; it is not
authentication's bare-address normalization.

## cryptids

- `SHA256(value)` returns a lowercase hex digest for stable lookup, including high-entropy API keys. Password hashing stays behind authentication's host-selected `PasswordHasher`; bcrypt's cost is configurable.
- `NewAESGCM(key)` takes a 32-byte key and keeps the raw-base64url nonce/ciphertext/tag envelope for stored data.
- `JWTSigner` is the contract `integrations/cryptids/golang-jwt` implements. Signing owns `exp` and `iat`; verification requires numeric expiration, validates optional `nbf`/`iat`, and allows 60 seconds of clock tolerance. Dates must fall in years 0001 to 9999. Hosts keep their exact key bytes when migrating from SDK HS256.

## workers

`workers` owns execution mechanics; the [jobs pocket](../pockets/jobs.md) is
one implementation of a store for it, not a requirement.

| Type | Adds |
|---|---|
| `Pool` | calls a `WorkFunc` with bounded concurrency, polling, wake signals, panic recovery and logging |
| `Runner[T]` | claim, process, complete and fail through your `JobStore[T]`; `Job` needs only `ID() string` |
| `FencedRunner[T]` | lease ownership and durable retry; `FencedJob` also needs `RetryCount()` |

**Happy path.** Process a job, gate it with middleware, run it in a pool:

```go
func gate(next workers.ProcessFunc[Report]) workers.ProcessFunc[Report] {
    return func(ctx context.Context, report Report) error {
        if !inputsReady(report) {
            return workers.DeferUntil(time.Now().Add(time.Minute), "inputs pending")
        }
        return next(ctx, report)
    }
}

process := workers.ChainJobMiddleware(generateReport, gate)
runner := workers.NewRunner(queue, process, workers.WithRunnerLogger(log))
pool := workers.NewPool(runner.WorkFunc())
err := pool.Run(ctx)
```

`Report` and `queue` are host types; `sdk/pkg/workers/example_test.go` is a
complete SDK-only example.

**What a processing result means.**

| Result | Runner action |
|---|---|
| `nil` | complete, even if middleware skipped `next` |
| `DeferUntil(time, reason)` | release for later without spending a failure attempt |
| `Reject(reason)` | dead-letter immediately |
| other error or panic | the runner's ordinary failure/retry policy |

Jobs may implement `RetryLimitedJob` (`RetryLimit() int`): a positive value
overrides the runner's default failure ceiling. Permanent rejection always wins,
including over a wrapped deferral.

**Two middleware boundaries.** `Middleware` (installed with `WithMiddleware`)
wraps one `WorkFunc` iteration including claim and persistence; use it to pause
polling, instrument workers or apply shutdown policy, and return `ErrNoWork` to
back off without claiming. `JobMiddleware[T]` (composed with
`ChainJobMiddleware`) wraps `ProcessFunc[T]` after claim and before
persistence; use it for gates, processing instrumentation and deadlines. The
first supplied wrapper is outermost. Compose before running; shared state must
be safe for concurrent calls. The runner captures the execution ID before
processing and middleware must not change it. A wrapper's after-processing code
runs before persistence; `SetDeadLetterHook` runs only after a successful fenced
failure transition.

**Deferral and fencing.** Deferral needs the optional `JobDeferrer` or
`FencedDeferrer` store port. Fenced deferral verifies the live lease and refunds
only this claim's increment; invalid or unsupported deferral returns an error
and leaves the claim for store recovery. Ordinary queues have no lease token to
protect a release from a stale worker, so use fencing when ownership matters.
Fenced ownership conflicts after expiry or replacement are logged without
retrying a stale write.

**Lifecycle.** Unexpected persistence failures return to the pool and are
logged. A fatal `ErrPoolShutdown` stops peers and returns from `Run` after
drain, preserving its cause. A pool is single-use; a later or concurrent `Run`
returns `ErrAlreadyRun`. Poll and idle delays start after each iteration and a
wake signal may run one sooner. Heartbeats (`WithHeartbeat`) report
`successful_iterations`, which is not a count of delivered jobs. Cancellation is
cooperative: the pool waits for admitted iterations, and a fenced runner leaves
work reclaimable on parent cancellation. The optional fenced processing timeout
covers the middleware chain, starts after claim, and must be shorter than the
lease; invalid configuration panics in `NewFencedRunner`, while jobs' constructor
returns an error instead. Bound store calls separately.

## async

`GoContext` uses its context for admission; the callback owns the context it
works with. `Close(ctx)` stops admission, wakes blocked submitters and waits for
accepted tasks: nil means all returned, a deadline returns `ctx.Err()` while
tasks continue, and a later `Close` can wait for the same drain. `Wait` is a
batch barrier, so finish submitting before calling it. Hosts choose concurrency,
drop options and close deadlines. Tasks can outlive a request and are lost on
process exit.

## Not a miscellaneous drawer

To belong here a package must be service-agnostic and stay flat. A helper that
depends on another `pkg/` package or encodes application policy belongs
elsewhere. Common vocabulary and small primitives live in the root package;
provider lifecycle and broader policy live in capabilities.
