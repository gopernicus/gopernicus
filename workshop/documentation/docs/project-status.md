---
title: Project status & scope
description: What Gopernicus ships today, what is deferred, and how to read stability.
---

# Project status & scope

Gopernicus is an open-source, actively developed multi-module repository released under the [MIT License](https://github.com/gopernicus/gopernicus/blob/main/LICENSE). The current `go.work` composes 39 modules across the SDK, integrations, pocket cores and adapters, UI, examples, and Workshop CLI. The code has extensive boundary guards and conformance tests, but the public distribution and documentation should still be treated as pre-stable until module tags establish a release line.

## Available today

- stdlib-only layered SDK: kernel, foundation, capabilities, and pocket mount;
- web server/router helpers, middleware, JSON and HTML responses, SSE, static files, streaming, and OpenAPI construction;
- reusable capabilities for cache, email, events, file storage, notification, OAuth, rate limiting, tracing, and work submission;
- authentication, authorization, CMS, events, and jobs pocket cores;
- PostgreSQL and Turso store modules for every shipped durable pocket;
- reusable integrations for databases, cryptography/IDs, email, object storage, Redis, OAuth, scheduling, and OpenTelemetry;
- complete `ui/goth` component system and pocket view adapters;
- an API-only host shape and a React/TanStack client integration guide;
- zero-infrastructure and datastore-backed example hosts;
- Workshop host/pocket scaffolding and migration-ledger commands;
- repository-wide architecture guards and hermetic tests.

## Deliberately not available yet

- schema-, annotation-, or field-spec-driven application regeneration;
- generated store adapters from an entity specification;
- OpenAPI/TypeScript client generation;
- `gopernicus doctor` or a SQL guard command;
- generated integration-test harnesses;
- a shipped `ui/react` module; the current guide documents the client boundary and a future module shape;
- a jobs admin HTTP surface (`/jobs/*` is reserved only);
- an unlimited events SSE connection-age mode;
- automatic cross-module release versioning in the docs.

Workshop generation is planned work. The current roadmap includes store adapters, entity specifications, regeneration markers, and typed clients from OpenAPI; the [Workshop overview](workshop/overview.md) records the current command surface.

## Contributors

Gopernicus is currently maintained by jrazmi and contributors. It is open source, but the
contribution model is still being figured out. There is no formal contributor
guide, support commitment, or release process yet. If you want to propose a
change, an issue or pull request is the best place to start; please include the
problem, the affected package boundaries, and any compatibility concerns.

## AI assistance

Gopernicus is developed with AI tools used as an engineering assistant — for code authoring, review, and documentation, and to generate the site's gopher illustrations. Every change is reviewed and owned by the human maintainers, and the repository's architecture guards and conformance tests apply to all contributions regardless of how they were drafted.

## Known sharp edges

- Pocket route prefixes change registered paths, not links or redirects rendered by a pocket. The current CMS views use host-rooted links, so prefixed CMS mounting is not fully transparent.
- The events outbox poller is single-instance today; it does not lease batches.
- Jobs handlers are at-least-once and should be idempotent where possible.
- Query logging in the datastore connectors includes arguments and is development-only.
- Console email and notification implementations are development-only; production posture checks fail closed on unsafe or metadata-less senders.
- Real consumers should pin module tags when available rather than relying on this repository's `go.work` replacements.

## Sources of truth

When documentation and code disagree, use this order while the public surface is pre-stable:

1. public Go types and doc comments;
2. conformance tests and executable examples;
3. `ARCHITECTURE.md`, `pockets/README.md`, and `sdk/README.md`;
4. this site;
5. historical plans and decision notes.

Please fix documentation drift with the same change that adjusts a public contract. Each page has an edit link back to its source.
