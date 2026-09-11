---
title: SDK packages
description: Pure mechanisms and vocabulary in the Gopernicus SDK.
---

# SDK packages

Packages under `pkg/` provide reusable mechanism and data vocabulary without service semantics. The tier is flat: a package under `pkg/` may import the root SDK kernel but never another package under `pkg/`.

## Catalog

| Package | Purpose |
|---|---|
| `async` | bounded fire-and-forget pool for in-process side work |
| `list` | listing requests/pages, ordering, search, cursor/offset pagination |
| `cryptids` | AES-GCM encryption, SHA-256 digests, and a JWT signing/verifying contract |
| `environment` | `.env` loading, tagged config parsing, and explicit deployment posture |
| `logging` | `slog` construction and request/trace-aware handler |
| `validation` | explicit field checks returning structured `sdk.Violation` data |
| `web` | routing, middleware, JSON/HTML responses, SSE, static serving, server lifecycle |
| `workers` | worker pools, runner mechanics, middleware, fencing, graceful drain |

## Common primitives

Pointer reads, slug generation, IDs and identity vocabulary live in root SDK.
See the [root API guide](overview.md#root-api). Packages under `pkg/` remain for
coherent APIs such as validation, environment configuration and cryptids.

## Environment and deployment posture

`environment.LoadEnv` reads `.env` in the working directory; `LoadPath` accepts another path. Missing files are allowed, and existing process variables, including empty ones, are preserved. Always check the returned error. Earlier assignments remain applied if a later line fails.

The format supports single-line `KEY=value` assignments, an optional `export ` prefix, and literal values. Single or double quotes preserve spaces and hashes; a space or tab followed by `#` outside quotes starts a comment. For example, `LABEL="value # inside" # note` loads `value # inside`. Malformed assignments, keys containing whitespace/NUL, unterminated quotes, and trailing text after a closing quote return errors with filename/line context. There is no escape processing, variable expansion, or multiline syntax.

`ParseEnvTags` fills tagged scalar fields and string slices, including named string types, and descends into untagged nested structs. Untagged collaborator pointers/interfaces are skipped. Tagged unsupported types are errors even when their environment variable is absent. Parsing errors identify the full key, field path, and type without retaining rejected values. Numeric syntax/range errors remain matchable with `errors.Is`; application and enum validation remain the constructor's responsibility.

Precedence is **nonempty env value > existing nonzero field > default tag**. Empty env values count as absent. Explicit `false`, zero, and empty slices cannot override nonzero defaults through a pre-seeded literal; apply such code overrides after parsing. A `required:"true"` field requires a nonempty env value even if the field was pre-seeded. `GetEnvOrDefault` deliberately uses raw lookup semantics instead: a present empty value wins over its fallback.

`Secret(key, minBytes)` reads a hex-encoded secret with a minimum decoded-byte floor and returns nil when the key is unset or empty. The host owns whether a missing secret is allowed; its consuming constructor enforces exact key requirements. `web.ServerConfig`, `logging.Options`, and pocket configs already carry tags:

```go
type Config struct {
    Host    string        `env:"HOST" default:"localhost"`
    Port    string        `env:"PORT" default:"8080"`
    Timeout time.Duration `env:"TIMEOUT" default:"10s"`
}

if err := environment.LoadEnv(); err != nil {
    return err
}

var cfg Config
if err := environment.ParseEnvTags("", &cfg); err != nil {
    return err
}

logOpts := logging.Options{Format: "text"}  // this host's own default; env wins, KEY= keeps it
if err := environment.ParseEnvTags("", &logOpts); err != nil {
    return err
}
srv := web.ServerConfig{Port: "8082"}
if err := environment.ParseEnvTags("", &srv); err != nil {
    return err
}
router.Use(web.TrustProxies(srv.TrustedProxyCount))
```

Deployment posture is a required two-value vocabulary: development or production. `ParseMode` reads no implicit environment variable; the host decides which key supplies it. Preview, staging, and CI normally map to production posture because permissive behavior should be an explicit development choice.

## Logging

`logging.New(logging.Options{})` returns a standard `*slog.Logger` with INFO level, JSON format, and STDERR output. Options carry `LOG_LEVEL`, `LOG_FORMAT`, and `LOG_OUTPUT` tags for explicit parsing by the host; logger construction reads no environment variables and does not change slog's global default. Unknown values retain those defaults.

Context-aware calls automatically include available `request_id`, `trace_id`, and `span_id` values from SDK context helpers. This adds correlation fields without starting a tracer or exporter. IDs follow the logger's current `WithGroup` group. Use `NewContextHandler` when constructing your own handler, or a plain standard handler if automatic IDs are unwanted:

```go
log := logging.New(logging.Options{Format: "text"})
ctx := sdk.WithRequestID(context.Background(), "request-123")
log.InfoContext(ctx, "request handled")

custom := slog.New(logging.NewContextHandler(slog.NewJSONHandler(output, nil)))
disabled := slog.New(slog.DiscardHandler)
```

The host should create its configured logger once, pass it into `run(ctx, log)`, and use that same logger for returned startup errors. Errors before configuration is available can use the bootstrap logger. `slog.DiscardHandler` disables every level; ordinary Go argument expressions still evaluate, but disabled calls do not resolve `LogValuer` values.

## Listing vocabulary

`pkg/list` supplies `Request`, `Page[T]`, `Limits`, ordering, search,
cursor encoding and row mapping. Domain packages declare their own repository
methods, filters and update inputs. Transactions live in the
[`transaction` capability](capabilities.md#transactions).

Use `ParseQuery` at an HTTP edge. It parses `limit`, `cursor`, `offset`, `count`
and `q`; order has its own per-resource allow-list:

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

Blank query values count as absent. Explicit invalid limits, offsets, booleans
and strategies return errors wrapping `sdk.ErrInvalidInput`. Programmatic store
calls use `Request.Validate` for strategy consistency and `NormalizedLimit` for
defaults/clamping. Zero-value limits resolve to 25 by default, at most 100.

Cursor mode is the default. Fetch `limit+1`, then `TrimPage` encodes the last
returned item as `NextCursor` when another page exists. For previous navigation,
fetch up to `limit+1` records **at or before the incoming boundary**, restore
normal order, and call `MarkPrevPage`. Any record sets `HasPrev`; an extra
predecessor supplies `PreviousCursor`. Otherwise an empty previous cursor opens
the first page. Offset mode must be explicit and emits no cursors. `WithCount`
requests the entire filtered count, excluding page boundaries.

Cursors retain padded base64url JSON and tagged scalar/time values. Malformed
tokens are invalid input, including malformed tokens for an old order field.
A well-formed token for a different field resets to the first page. Convert
named scalar types before encoding. Legacy untagged numbers are accepted only
within ±(2^53−1); tagged integers preserve the full int64/uint64 range. Cursors
are positions, not authorization credentials or signed tokens.

SQL adapters order the projected SELECT output on every page and add search
and cursor predicates outside that SELECT.
Project each search/order/PK field under an unqualified output name, and include
it in the store-local row scanner. `OrderValueOf` must return that projected
value and type. For example, select `t.created_at AS created_at` and use
`Column: "created_at"`; the inner `t` alias is unavailable outside the SELECT.
Fixed order expressions also operate on projected fields.

SQL keyset ordering requires non-null order and PK values throughout the matched
population. Use a non-null projected key or offset mode for nullable ordering.
A SQL cursor with a null order value, or a non-string case-folded value, is
invalid input. Other type compatibility belongs to `OrderValueOf` and the
projected column; the generic helper does not inspect the database schema.

`Items`, `MapItems`, `TrimPage`, `MapPage` and `MapPageErr` normalize empty items
to `[]`. A directly constructed zero `Page[T]{}` remains caller-owned and emits
`null`. Optional page fields are omitted; clients read missing flags as false.

## Field validation

Use the same collector in request DTOs and domain code. Helper functions return
nil for valid input or a field violation; `Err()` returns an ordinary Go error
only when problems were collected.

```go
func (in *createWidget) Validate() error {
    var problems sdk.ValidationError
    problems.AddViolation(validation.Required("name", in.Name))
    problems.AddViolation(validation.MaxLength("name", in.Name, 80))
    return problems.Err()
}
```

The returned error matches `sdk.ErrInvalidInput` through `errors.Is`, including
after wrapping with `%w`. Web response helpers preserve every field, message,
and optional code. Collect validation results before wrapping; `errors.Join`
does not merge independent field lists into one validation response.

String length checks count Unicode code points, without normalizing text.
Optional string checks accept empty strings; use `Required` for presence.
Pointer variants skip nil, then apply the scalar rule. For custom optional
checks, `IfSet` takes a callback returning `*sdk.Violation`.

Custom validators live in the consuming application. They can return a
caller-facing `sdk.Violation`, or add a domain rule with
`problems.Add(field, code, message)`. Keep unexpected dependency errors as
ordinary errors; their internal messages do not belong in public violations.
Password policy belongs in authentication or the host. The generic `Email`
helper accepts mail address syntax, including display names; it does not apply
authentication's bare-address normalization rules.

## Cryptographic primitives

ID generation and identity projection are part of the [root SDK API](overview.md#root-api).
`cryptids` supplies cryptographic contracts and helpers.

`cryptids.SHA256(value)` returns a lowercase hexadecimal digest for stable lookup, including high-entropy API keys. Password hashing belongs behind authentication's host-selected `PasswordHasher`; bcrypt's cost remains configurable.

`cryptids.NewAESGCM(key)` uses a 32-byte key and preserves the raw-base64url nonce/ciphertext/tag envelope for stored data.

`cryptids.JWTSigner` is implemented by `integrations/cryptids/golang-jwt`. Signing owns `exp` and `iat`; verification requires numeric expiration, validates optional `nbf`/`iat`, and allows 60 seconds of clock tolerance. Date values must be within calendar years 0001–9999. Hosts retain their exact effective key bytes when migrating from SDK HS256.

## Workers versus jobs

`pkg/workers` owns reusable execution mechanics. A `Pool` calls a
`WorkFunc` with bounded concurrency, polling, wake signals, panic recovery and
logging. `Runner[T]` adds claim/process/complete/fail through a host-supplied
`JobStore[T]`. Its `Job` constraint needs only `ID() string`. Jobs may optionally
implement `RetryLimitedJob` with `RetryLimit() int`: positive values override
the ordinary runner's default failure ceiling, while nonpositive values keep
that default. Permanent rejection still takes precedence. `FencedRunner[T]`
adds lease ownership and durable retry; its `FencedJob` also needs `RetryCount()`.
Neither runner requires the [jobs pocket](../pockets/jobs.md). Jobs supplies its
aggregate, repositories, schedules and policy as one implementation.

There are two middleware boundaries:

| API | Wraps | Useful for |
|---|---|---|
| `Middleware`, installed with `WithMiddleware` | One `WorkFunc` iteration, including claim and persistence | Pausing polling, worker instrumentation, shutdown policy |
| `JobMiddleware[T]`, composed with `ChainJobMiddleware` | `ProcessFunc[T](context.Context, T) error`, after claim and before persistence | A gate based on the claimed job, processing instrumentation, deadlines |

The first supplied wrapper is outermost. Compose before running; shared
middleware state must be safe for concurrent calls. A worker gate returns
`ErrNoWork` to back off without claiming. A job gate uses an explicit outcome:

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

`Report` and `queue` are host types. A complete SDK-only example lives in
`sdk/pkg/workers/example_test.go`.

| Processing result | Runner action |
|---|---|
| `nil` | Complete, even if middleware skipped `next` |
| `DeferUntil(futureTime, reason)` | Atomically release for later without spending a failure attempt |
| `Reject(reason)` | Dead-letter immediately |
| Other error or panic | Apply the runner's ordinary failure/retry policy |

Deferral requires the optional `JobDeferrer` or `FencedDeferrer` store port.
Fenced deferral verifies the live lease and refunds only this claim's increment;
previous attempts remain spent. Invalid or unsupported deferral returns an error
and leaves the claim for store recovery. Ordinary queues have no lease token to
protect a release from a stale worker; use fencing when ownership matters.
Permanent rejection takes precedence if a wrapped/joined error also contains a
deferral. The runner captures the execution ID before processing; middleware
must not change it. A wrapper's after-processing code runs before persistence;
`SetDeadLetterHook` is a separate hook that runs only after a successful fenced
failure transition.

Unexpected persistence failures return to Pool and are logged. Fenced ownership
conflicts are expected after expiry/replacement and are logged without retrying
a stale write. Pool logs ordinary iteration failures; a fatal `ErrPoolShutdown`
stops peers and returns from `Run` after drain, preserving its cause. A Pool is
single-use: a later or concurrent `Run` returns `ErrAlreadyRun`. Poll and idle
delays start after each iteration; a wake signal may run one sooner. Heartbeats
report `successful_iterations` (nil returns), which are not a count of delivered
jobs.

Cancellation is cooperative. Pool waits for admitted iterations to return.
A fenced runner leaves work reclaimable on parent cancellation. Its optional
processing timeout covers the middleware chain, starts after Claim, and must be
shorter than the lease; allow margin for store latency. Invalid configuration
panics in `NewFencedRunner`; jobs' constructor returns a configuration error.
Hosts must bound store calls separately.

## Async task lifetime

`pkg/async` runs bounded in-process tasks. `GoContext` uses its context
for admission; the callback owns the context it uses for work. `Close(ctx)` stops
admission, wakes blocked submitters and waits for accepted tasks. Nil means all
accepted tasks returned; a deadline returns `ctx.Err()` and tasks continue. A
later Close can wait for that same drain. `Wait` is a batch barrier: finish all
submissions before calling it. Hosts choose explicit concurrency/drop options
and Close deadlines. Tasks can outlive a request and are lost on process exit.

## SDK packages is not a miscellaneous drawer

To belong here, a package must be service-agnostic and stay flat. A helper that depends on another package under `pkg/` or encodes application policy belongs elsewhere. Common vocabulary and small, explicitly named primitives live in root SDK. A focused mechanism such as cryptids retains its own package and contracts; provider lifecycle and broader capability policy belong in capabilities.
