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

## Project Structure

- Move `core/testing/` (fixtures, setup helpers) into `workshop/testing/` — test infrastructure is a development concern, not part of the deployed binary, and belongs alongside migrations and dev tooling

## Migrations

- Additional SQL dialect support beyond PostgreSQL
- Bring-your-own migrator integration

## Testing

- **Bridge auth handler tests + response shape contracts** *(elevated priority)* — `bridge/auth/authentication/` has no `*_test.go` files. Two related gaps that should be closed together since they share the same harness:
  1. **Handler behavior tests.** The handlers (verify-email, change-password, remove-password send/verify, unlink-OAuth send/verify, OAuth link/unlink, etc.) all interact with the authenticator and the `httpErrFor` mapper, but there is no end-to-end test that exercises the full HTTP request → handler → core → response cycle. Bootstrapping requires a fake `Bridge` with stub repos + a real authenticator instance + an `httptest.Server`.
  2. **Response shape contract tests.** Several response types now carry stability contracts in their doc comments (`OAuthAccountResponse`, `LoginResponse`, `MeResponse`, `OAuthCallbackResponse`, `UserResponse`, `SessionResponse`, `TokenResponse`, `SuccessResponse`). The Go type system catches Go-side renames, but it does *not* catch JSON tag changes (e.g. `json:"provider"` → `json:"provider_name"` while leaving the Go field as `Provider`) or accidental array-vs-envelope changes. Each documented contract should be backed by a golden-file marshaling test that fixtures a populated struct and asserts the exact JSON output. Treat the test failure as the breaking-change alarm — frontends bind directly to these shapes.
  
  Both items share the same `bridge/auth/authentication/*_test.go` bootstrap, so do them in one pass.
- **Promote `penetration_test.go` harness to a shared `testing.go` helper** — the mocks and `testHarness` in `core/auth/authentication/penetration_test.go` are useful well beyond the penetration tests (they're already used by `sensitive_test.go`). Extracting them into a non-build-tagged helper would let other test files in the package use them without sharing the `penetration` build tag. This becomes more valuable once the bridge tests above exist, since the bridge tests will want to drive a real `Authenticator` and the cleanest way to construct one for tests is the existing harness.
