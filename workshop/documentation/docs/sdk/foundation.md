---
title: Foundation packages
description: Pure mechanisms and vocabulary in the Gopernicus SDK.
---

# Foundation packages

Foundation packages provide reusable mechanism and data vocabulary without service semantics. The tier is flat: a foundation package may import the root SDK kernel but never another foundation package.

## Catalog

| Package | Purpose |
|---|---|
| `async` | bounded fire-and-forget pool for request-scoped side work |
| `conversion` | pointers, slices, acronym-aware case conversion, datetime and JSON helpers |
| `crud` | generic CRUD vocabulary, ordering, search, cursor/offset pagination, transactions |
| `cryptids` | ID strategies, AES-GCM, SHA-256 hashing, and stdlib HS256 tokens |
| `environment` | `.env` loading, tagged config parsing, and explicit deployment posture |
| `identity` | principal/address/info vocabulary, resolver port, and context helpers |
| `logging` | `slog` construction and request/trace-aware handler |
| `slug` | domain-neutral URL slug creation with accent folding |
| `validation` | reusable validators and error accumulation |
| `web` | routing, middleware, JSON/HTML responses, SSE/streams, static serving, OpenAPI, server lifecycle |
| `workers` | worker pools, runner mechanics, middleware, fencing, graceful drain |

## Environment and deployment posture

`environment.LoadEnv` reads a dotenv file without overwriting existing process variables. `ParseEnvTags` fills a struct from `env`, `default`, and `required` tags while keeping programmatic construction first-class.

```go
type Config struct {
    Host    string        `env:"HOST" default:"localhost"`
    Port    string        `env:"PORT" default:"8080"`
    Timeout time.Duration `env:"TIMEOUT" default:"10s"`
}

_ = environment.LoadEnv()

var cfg Config
if err := environment.ParseEnvTags("", &cfg); err != nil {
    return err
}
```

Deployment posture is a required two-value vocabulary: development or production. `ParseMode` reads no implicit environment variable; the host decides which key supplies it. Preview, staging, and CI normally map to production posture because permissive behavior should be an explicit development choice.

## CRUD vocabulary

`crud` standardizes repository edges without prescribing SQL:

- `Reader`, `Writer`, and `CRUD` generic contracts;
- `Page[T]` and `ListRequest`;
- bidirectional keyset cursors by default or explicit limit/offset strategy;
- per-aggregate ordering and search allow-lists;
- opt-in total counts;
- strict request parsing and store-edge validation;
- `Transactor`/`Tx` coordination seams;
- transport-neutral sentinel aliases.

Each aggregate declares legal order and search fields beside its domain type. Store adapters map those safe names to concrete columns. Raw request input never becomes an SQL identifier.

```go
req, err := crud.ParseListRequest(crud.ListParams{
    Limit:  r.URL.Query().Get("limit"),
    Cursor: r.URL.Query().Get("cursor"),
    Order:  r.URL.Query().Get("order"),
    Search: r.URL.Query().Get("q"),
})
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}
```

## Identity is projection, not user ownership

`identity` contains:

- `Principal{Type, ID}` for authenticated identity;
- `Address{Kind, Value}` for delivery addresses;
- `Info` as a display/contact projection;
- `Resolver` for loading that projection;
- strict `ResolveAll` and context helpers.

No universal `User` aggregate enters SDK. Authentication owns user records; other identity providers may implement the same resolver without adopting authentication's schema.

## Cryptographic and ID primitives

The zero-value `cryptids.IDGenerator` emits a nanoid-shaped identifier. A host can choose custom nanoids, database-generated IDs, or a connector such as Google UUID once at composition time.

```go
ids := cryptids.IDGenerator{}          // default nanoid strategy
dbIDs := cryptids.Database             // empty ID delegates to the store
uuidIDs := cryptids.NewGenerator(googleuuid.V7())
```

`SHA256Hasher` is appropriate for high-entropy API keys, not human passwords. Password hashing belongs behind a feature-owned port and an integration such as bcrypt.

## Workers versus jobs

`foundation/workers` owns execution mechanics: polling, bounded concurrency, wake channels, panic recovery, middleware, claim/process/complete/fail sequencing, and graceful drain.

It does not own a durable job aggregate, queue schema, cron scheduling, or HTTP surface. Those belong to the [jobs feature](../features/jobs.md). Mechanism remains reusable; domain lifecycle remains feature-owned.

## Foundation is not a miscellaneous drawer

To belong here, a package must be service-agnostic and stay flat. A helper that depends on another foundation package, owns a behavioral provider port, or encodes application policy likely belongs in a capability, feature, integration, or host instead.
