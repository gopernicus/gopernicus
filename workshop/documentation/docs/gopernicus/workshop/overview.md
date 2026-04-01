---
sidebar_position: 1
title: Overview
---

# Workshop

The Workshop is everything your project needs to run, but nothing that gets deployed. SQL migrations, local Docker services, and application documentation all live here — explicitly separated from application code so build tools, CI, and production images can ignore it entirely.

## Structure

```
workshop/
├── migrations/         # SQL migration files and reflected schema
├── dev/                # Local development environment
├── docker/             # Docker Compose services and related config
├── documentation/      # Your application's documentation
└── testing/            # Test infrastructure, fixtures, and E2E setup
```

You can extend this structure with any other infrastructure needs specific to your project — additional services, seed scripts, environment tooling, etc.

## Why a Separate Directory?

Gopernicus consolidates development concerns into `workshop/` for a few reasons:

- **Production images stay clean.** A single `.dockerignore` entry excludes everything at once.
- **CI can skip it.** Pipelines that don't need local services or docs don't have to cherry-pick exclusions.
- **Migrations are the source of truth.** The code generator reads the reflected schema from `workshop/migrations/` — keeping it adjacent to the tooling that uses it makes the relationship explicit. Migrations are forward-only; there are no down migrations.
- **Docs live near the code.** Application documentation in `workshop/documentation/` can be updated alongside the code changes that motivate them, rather than drifting in a separate repo or wiki.

## Pages

| Page | Purpose |
|---|---|
| [Migrations](./migrations.md) | SQL migration files, schema reflection |
| [Dev](./dev.md) | Local development environment setup |
| [Docker](./docker.md) | Docker Compose services (PostgreSQL, Redis, Jaeger) |
| [Documentation](./documentation.md) | Your application's documentation |
