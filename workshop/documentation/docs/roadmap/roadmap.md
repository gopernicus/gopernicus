---
sidebar_position: 1
title: Roadmap
---

# Roadmap

What's coming next for Gopernicus.

:::note
This page is a work in progress. Items are unordered and unprioritized.
:::

## Communications

- SMS channel (`communications/sms/`) — client interface + implementation (Twilio or similar)
- Instant messaging channel (`communications/slack/`) — client interface + Slack implementation

## Events & Jobs

- **Outbox poller wiring in app template** — generate the `workers.WorkerPool` + poller startup in `main.go` when the outbox feature is enabled
- **Job queue worker wiring in app template** — generate `workers.Runner[JobQueue]` + pool startup for the job queue
- **`@event: ... outbox` support for `create` and `update_returning` categories** — currently only `exec` and `update` categories support the outbox modifier
- **Job queue CLI** — `gopernicus jobs list`, `gopernicus jobs retry`, `gopernicus jobs dead-letter` for operational visibility
- **Outbox cleanup** — periodic deletion of published outbox rows (configurable retention)
- **`@event` example in scaffolded apps** — `gopernicus init` should produce a working `@event` (bus) and `@event: ... outbox` example so new projects demonstrate both patterns out of the box

## Infrastructure

- Consider whether satisfier patterns warrant `gopernicus generate` support
- `WithLogger` sub-options — allow per-package tuning of log levels for specific conditions (e.g. storage `LogNotFoundAs(slog.LevelDebug)`) so callers can distinguish expected vs unexpected outcomes without losing all logging

### HTTP Client (`httpc`)

- Retry / backoff support with configurable policies
- Per-request header overrides (currently headers are set at client construction only)
- Multipart / form-data request support

## CLI

- Audit and integration-test all `gopernicus new adapter <type>` commands before publishing docs — verified by code reading only, not by running
- `gopernicus new repo function <entity> <name>` — scaffold a custom Storer method signature, pgx store implementation, and test stub for methods that don't fit the `queries.sql` annotation model
- `gopernicus init --dry-run` — preview what would be created without writing files
- `gopernicus init` post-creation feature addition — ability to add framework features (authentication, authorization, etc.) to an existing project without re-initializing
- Fix `init` project name validation error message — says "only letters, numbers, and hyphens allowed" but actually accepts underscores too
- Fix `generate` CLI help text — references `model.go` as a bootstrap file but the actual bootstrap files are `repository.go` and `fop.go`

## Code Generation

- Evaluate a sqlc adapter — generate sqlc functions from annotated SQL but still use `bridge.yml` to bridge them. Would give users a familiar SQL-first workflow with gopernicus bridge wiring. This is exploratory and may not happen.

## Bridge

- Multi-protocol support — bridge packages currently only generate HTTP handlers (`generated.go`). Future protocols (gRPC, GraphQL) would live alongside as `grpc.go`, `graphql.go`, etc., sharing the same `bridge.yml` configuration and core wiring

## Packaging & Module Boundaries

:::note
This is a **consideration**, not a committed direction. It captures a question worth revisiting, along with the reasoning so far — not a decision to act on.
:::

**The question:** Gopernicus is currently a single Go module. Its `go.mod` requires every adapter's dependencies at once — AWS SDK, GCS, pgx, go-redis, SendGrid, OIDC, OTel, sqlite, testcontainers. A project that only needs sqlite still inherits the full module graph. Should heavy/optional adapters become their own modules?

**What's already true (so the cost isn't what it first looks like):**

- Go does package-level dead-code elimination. An app that never imports `infrastructure/cache/rediscache` does **not** compile the redis client into its binary. Binaries are already lean regardless of module layout.
- The real cost of one module is dependency-*graph* hygiene, not binary size: a consumer's `go.sum` inherits every adapter's deps, `go mod download` pulls all of them, and SCA tooling (`govulncheck`, dependency review) flags CVEs in adapters the app never calls.
- The port-in-parent / adapter-in-child layout already keeps `core` depending only on interface packages (`infrastructure/cache`), never concrete adapters. The architecture is clean; this is purely a *packaging* question.

**Three separable axes (easy to conflate):**

1. Should Gopernicus exist as a framework? — Yes. The value is the coherent wiring (`gopernicus.yml`, the CLI, bridge codegen, a consistent port shape), not any individual adapter. Atomizing into independent libraries would lose exactly that cohesion.
2. One repo or many? — One repo fits a framework + CLI that version together.
3. One `go.mod` or many? — The actual open question.

**Inspiration / prior art** for a small core plus many optional dependency-heavy adapters: `aws-sdk-go-v2` (per-service modules in one repo, done specifically so consumers don't pull all of AWS), `gorm` (core + per-driver modules like `gorm.io/driver/postgres`), and `testcontainers-go` (per-module modules — which Gopernicus already consumes that way). The common shape there is **monorepo, multi-module**, with the core module staying dependency-light and heavy adapters opted into explicitly.

**A possible staging, heaviest/most-optional first (if/when this is pursued):**

- Cloud storage (`storage/s3`, `storage/gcs`) — largest dependency trees, clearest win
- Email (`communications/emailer` / SendGrid)
- The redis trio together (`cache/rediscache`, `events/goredisbus`, `ratelimiter/goredislimiter`)
- Postgres (`database/postgres` / pgx) — heavy but very commonly used, so lower urgency to extract
- OAuth providers, OTLP tracing
- Core stays in the root module: `sdk`, `core`, `bridge`, all port interfaces, and the pure-Go / stdlib adapters (memory, noop, sqlite, disk)

**Trade-off to weigh before acting:** multi-module monorepos carry real ergonomic friction — per-module release tags (`infrastructure/database/postgres/v1.2.0`), careful ordering for cross-module changes, and reliance on `go.work` for local dev. For a pre-1.0, small-team project this overhead may outweigh the `go.sum`-noise benefit. Reasonable triggers to revisit: publishing for external consumers, dependency-scanning noise from unused adapters, or consumers reporting `go get` weight. If pursued, the cloud-storage seam is the natural first experiment to feel out the tagging/`go.work` ergonomics.

## Migrations

- Additional SQL dialect support beyond PostgreSQL
- Bring-your-own migrator integration

## Testing

- **Bridge auth response shape contract tests** *(elevated priority)* — `bridge/auth/authentication/` now has `*_test.go` coverage (`bridge_test.go`, `oauth_test.go`, `validation_test.go`), including handler behavior tests and a golden-file marshaling test for `OAuthAccountResponse`. The remaining gap is extending golden-file contract coverage to the other documented response types:
  - **Response shape contract tests.** Several response types carry stability contracts in their doc comments (`LoginResponse`, `MeResponse`, `OAuthCallbackResponse`, `UserResponse`, `SessionResponse`, `TokenResponse`, `SuccessResponse`) that do not yet have golden-file tests. The Go type system catches Go-side renames, but it does *not* catch JSON tag changes (e.g. `json:"provider"` → `json:"provider_name"` while leaving the Go field as `Provider`) or accidental array-vs-envelope changes. Each documented contract should be backed by a golden-file marshaling test that fixtures a populated struct and asserts the exact JSON output. Treat the test failure as the breaking-change alarm — frontends bind directly to these shapes.
- **Promote `penetration_test.go` harness to a shared `testing.go` helper** — the mocks and `testHarness` in `core/auth/authentication/penetration_test.go` are useful well beyond the penetration tests (they're already used by `sensitive_test.go`). Extracting them into a non-build-tagged helper would let other test files in the package use them without sharing the `penetration` build tag. This becomes more valuable once the bridge tests above exist, since the bridge tests will want to drive a real `Authenticator` and the cleanest way to construct one for tests is the existing harness.

## Enum filter values 500 on Postgres (robustness)

Filtering a List by an enum column with an out-of-domain value (e.g. a
client sends `?status=bogus`) returns 500 on the pgx path — Postgres rejects
the invalid enum cast before the comparison runs. On sqlite the column is
TEXT so it matches nothing. Desired: a clean 400 or empty result on both
drivers. The store knows the enum labels, so it could validate filter values
and short-circuit. Surfaced by the generated P2 SQL-injection probes, which
now skip enum filter params (type-constrained, not an injection surface).
