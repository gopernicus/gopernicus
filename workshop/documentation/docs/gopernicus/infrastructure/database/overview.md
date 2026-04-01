---
sidebar_position: 1
title: Overview
---

# Database

The database packages handle one thing: creating a live, verified connection to a database. There is no generic interface that spans drivers. That abstraction lives one layer up, at the store/repository level.

## The Two-Layer Design

Gopernicus separates database concerns across two layers:

**Infrastructure** (this layer) — connection management. Each package creates a pool or client for a specific driver and returns its native type. Nothing more.

**Core / Repositories** — the abstraction boundary. Each repository has a generated store written specifically for its driver. The store interface is defined in the domain package; the implementation uses the driver's native types directly.

The reason for this split: a lowest-common-denominator interface across PostgreSQL, SQLite, and Redis would mean giving up what makes each driver useful — batch queries, pgx-specific cursor semantics, WAL locking, key expiry. Gopernicus chose to put the abstraction at the store level, where each implementation can be as specific and performant as its driver allows.

The practical consequence: swapping databases means regenerating stores for the new driver, not just swapping a connection object. See [Core / Repositories](../../core/repositories) for how stores are structured and generated.

## Packages

| Package | Database | Returns |
|---|---|---|
| [postgres/pgxdb](./postgres/pgxdb) | PostgreSQL | `*pgxpool.Pool` |
| [sqlite/moderncdb](./sqlite/moderncdb) | SQLite (pure Go) | `*moderncdb.DB` |
| [kvstore/goredisdb](./kvstore/goredisdb) | Redis | `*redis.Client` |

These are low-level packages. New adapters for additional databases are added manually.

## Status Checks

Each package provides a `StatusCheck` function for health endpoints:

```go
pgxdb.StatusCheck(ctx, pool)
moderncdb.StatusCheck(ctx, db)
goredisdb.StatusCheck(ctx, client)
```

Each sets a 1-second deadline if none is present on the context.
