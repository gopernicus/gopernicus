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
