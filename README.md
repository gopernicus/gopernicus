# Gopernicus

[![CI](https://github.com/gopernicus/gopernicus/actions/workflows/ci.yml/badge.svg)](https://github.com/gopernicus/gopernicus/actions/workflows/ci.yml)

A Go framework for building production-ready, multi-tenant APIs with code
generation, hexagonal architecture, and built-in auth.

Gopernicus generates the structural boilerplate of your API from two
source-of-truth files — annotated SQL (`queries.sql`) for the data layer and
`bridge.yml` for the HTTP surface — producing repositories, stores, bridges,
fixtures, and integration tests, while business logic stays hand-written in
bootstrap files the generator never overwrites.

## Quick start

```bash
go run github.com/gopernicus/gopernicus/workshop/gopernicus@latest init myapp
cd myapp
```

The scaffolded project pins the generator as an in-project tool, so everything
after init is:

```bash
go tool gopernicus db migrate     # apply migrations
go tool gopernicus generate      # regenerate from queries.sql + bridge.yml
go tool gopernicus doctor        # verify project health
```

## Architecture

Code is organized into layers where alphabetical order defines the import
rule — each layer may import only the layers after it:

| Layer          | Directory       | Responsibility                            |
| -------------- | --------------- | ----------------------------------------- |
| App            | app/            | Composition root — server wiring          |
| Bridge         | bridge/         | HTTP transport — generated handlers       |
| Core           | core/           | Domain logic — repositories, cases, auth  |
| Infrastructure | infrastructure/ | Adapters — database, cache, events, storage |
| SDK            | sdk/            | Dependency-free building blocks           |
| Telemetry      | telemetry/      | Logging, tracing, metrics                 |
| Workshop       | workshop/       | Tooling — not part of the deployed binary |

Feature engines — authentication, ReBAC authorization, tenancy, invitations,
events/outbox, jobs — ship as version-locked specs with the framework and
generate into your project.

## Documentation

Full reference lives at
[gopernicus.github.io/gopernicus](https://gopernicus.github.io/gopernicus/),
generated from [`workshop/documentation/`](workshop/documentation/).

## Developing the framework

```bash
go build ./... && go vet ./...   # build + vet
go test ./...                    # unit tests (no docker)
go test -tags=integration ./...  # integration tests (docker / testcontainers)
go run ./workshop/gopernicus generate   # regenerate; CI fails on drift
```

CI enforces build/unit, tagged-compile (`integration`/`e2e`/`penetration`
vet), a clean-regenerate check, and the integration suite on every PR.
Releases are tag-only (`git tag -a vX.Y.Z && git push origin vX.Y.Z`).
