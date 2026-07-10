# gopernicus

A Go framework for building hexagonal, server-rendered apps: a stdlib-only
kernel (`sdk`), reusable third-party connectors (`integrations/`), pluggable
feature modules (`features/`), and worked example hosts (`examples/`) that
prove the design end to end — one on Turso, one with zero external
infrastructure. See [ARCHITECTURE.md](ARCHITECTURE.md) for the full layering
rules and [NOTES.md](NOTES.md) for the decision log.

## The thirty-four modules

```
sdk/                                stdlib-only framework kernel (empty go.mod = structural enforcement)
integrations/cryptids/bcrypt/       password-hashing connector (x/crypto), its own module
integrations/cryptids/golang-jwt/   JWT-signing connector (golang-jwt/jwt v5), its own module
integrations/cryptids/google-uuid/  uuid ID-generation connector (google/uuid v4/v7), its own module
integrations/datastores/pgxdb/        reusable Postgres connector (sdk + pgx/v5), its own module
integrations/datastores/turso/      reusable Turso/libSQL connector (sdk + libsql), its own module
integrations/email/sendgrid/        SendGrid email connector (sendgrid-go), its own module
integrations/filestorage/gcs/       Google Cloud Storage connector (cloud.google.com/go/storage), its own module
integrations/filestorage/s3/        S3-compatible object-storage connector (aws-sdk-go-v2; MinIO/DO Spaces via endpoint + path-style), its own module
integrations/kvstores/goredis/      Redis connector — events bus, cacher, ratelimiter over one go-redis client, its own module
integrations/oauth/github/          GitHub OAuth provider (vendor API contract; zero external libs), its own module
integrations/oauth/google/          Google OIDC provider connector (coreos/go-oidc v3), its own module
integrations/scheduling/robfig-cron/ cron-expression connector (robfig/cron v3), its own module
integrations/tracing/otel/          OpenTelemetry tracing connector (stdout/OTLP-gRPC exporters or caller-supplied provider), its own module
features/authentication/                      session-auth hexagon — datastore-free; domain/ public rim, internal/ interior
features/authentication/stores/pgx/           auth's pgx store adapter, its own module
features/authentication/stores/turso/         auth's Turso store adapter, its own module
features/authorization/             IAM hexagon — independently wireable kinds (relationships/ReBAC + roles); datastore-free; public memstore/
features/authorization/stores/pgx/  authorization's pgx store adapter, its own module
features/authorization/stores/turso/ authorization's Turso store adapter, its own module
features/cms/                       the CMS hexagon — datastore-free; domain/ public rim, internal/ interior
features/cms/stores/pgx/            the CMS feature's pgx store adapter, its own module
features/cms/stores/turso/          the CMS feature's Turso store adapter, its own module
features/cms/views/templ/           cms's bundled default views (templ) — the FS3 sibling, its own module
features/events/                    durable outbox + SSE gateway hexagon — datastore-free; domain/ public rim
features/events/stores/pgx/         events' pgx store adapter, its own module
features/events/stores/turso/       events' Turso store adapter, its own module
features/jobs/                      durable queue + schedules hexagon — datastore-free; public memstore/
features/jobs/stores/pgx/           jobs' pgx store adapter, its own module
features/jobs/stores/turso/         jobs' Turso store adapter, its own module
examples/cms/                       a host app: features/cms on Turso, with a custom theme
examples/minimal/                   a host app: features/cms on an in-memory store — zero libsql in its module graph
examples/auth-cms/                  a host app: auth + cms + events + the authorization flagship composed in-memory (rule 6, live)
examples/jobs-minimal/              a host app: features/jobs on its memstore — zero drivers, the §8 protocol host
```

`go.work` resolves these locally for development; real consumers would pin
tagged versions, not the workspace.

## The rules

- **`sdk` imports only the standard library.** Third-party types cross into
  `sdk` only via structural typing seams (e.g. `templ.Component` satisfying
  `sdk/web.Renderer`).
- **One external dependency ⇒ its own module.** A stdlib-only implementation
  of an `sdk` port ships *inside* `sdk` as a default (`cacher.Memory`,
  `filestorage.Disk`, `email.SMTP`/`Console`); anything needing a third-party
  library is an `integrations/<category>/<tech>` module.
- **A feature is an sdk-only core + per-concern sibling modules.**
  `features/<name>`'s go.mod requires exactly `sdk` and never imports
  `integrations/`, `examples/`, or its own `stores/`/`views/`; each
  `features/<name>/stores/<dialect>` is its own module owning that dialect's
  SQL and migrations, and presentation defaults ship the same way
  (`views/<pkg>`, where a feature has HTML).
- **No init() registration, no service locator.** A host wires a feature
  explicitly in its `main`: `svc, err := name.NewService(repos, cfg)` then
  `svc.Register(mount)` — the public `Service` is the feature's use-case
  surface, and the shipped HTTP layer is an optional adapter over it (cms
  still takes the earlier `Register(mount, repos, cfg)` form until its
  public Service lands).
- **Dependencies point inward.** `examples` → `features`/`integrations` →
  `sdk`, never the reverse. `make check`'s ten layering guards enforce this.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full detail, including the
feature contract (`sdk/feature`), the app-hexagon pattern (`internal/logic`),
and the Registry content model.

## Quickstart

Zero external infrastructure — an in-memory store, no libsql in the build:

```sh
cd examples/minimal && go run ./cmd/server   # localhost:8081 by default
```

The Turso-backed host (needs `.env` with `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`):

```sh
cp .env.example .env   # fill in Turso credentials
make migrate           # applies examples/cms/workshop/migrations pre-boot
make run                # or: cd examples/cms && go run ./cmd/server
```

From the repo root, `make check` builds, vets, and tests all thirty-four modules
and runs the ten layering guards; `make test-stores` runs the live dialect
conformance suites (expects `POSTGRES_TEST_DSN` / `TURSO_*`). See [examples/cms/README.md](examples/cms/README.md)
for that host's full env/make-target reference.
