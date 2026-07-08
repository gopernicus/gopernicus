# features/events/stores/pgx

The events feature's **PostgreSQL** transactional-outbox store adapter — the
dialect sibling of `features/events/stores/turso`. Its own module so a host that
brings a different datastore never pulls `pgx` into its module graph. It owns the
SQL and the canonical migration files; the host owns its database lifecycle.

It fills the events feature's one outbound port, `outbox.EntryRepository`, over
the `integrations/datastores/pgxdb` connector — `TIMESTAMPTZ` timestamps (postgres
orders them natively; no lexicographic-`TEXT` convention needed), `JSON` payload,
`event_id` as the primary key and the at-least-once de-dupe key (a duplicate
append surfaces as `errs.ErrAlreadyExists`). Representation changes vs turso;
structure and port semantics do not.

**`payload` is `JSON`, not `JSONB`.** The payload is opaque to this store (no jsonb
operators or indexes), and `JSON` preserves the caller's exact bytes while `JSONB`
re-canonicalizes whitespace/key order. The shared `storetest` suite asserts a
byte-exact payload round-trip, which only `JSON` satisfies — same decision and
rationale as `features/jobs/stores/pgx` (jobs-v1 precedent), and a deliberate
deviation from the design's illustrative `JSONB`.

## ⚠️ Prerequisite: apply the `events` migration source before wiring an appender

The outbox table belongs to migration source **`events`**, distinct from
`cms`/`auth`/`jobs`. The shared `(source, version)` migration ledger expresses
**no ordering between sources**, so a host that scaffolds another feature's
migrations but not this store's would fail at *runtime*, not boot.

**`New(db)` guards against exactly that:** it probes for the `event_outbox` table
at construction (`SELECT to_regclass('event_outbox')`) and returns
`errs.ErrNotFound` if the `events` source has not been applied — the failure
surfaces at wiring time, before the host serves traffic (design §5 mitigation b).
Scaffold this store's migrations with `ExportMigrations` and apply them with your
host's runner pre-boot, alongside every other feature source you wire.

## Surface

Mirrors the turso store's exported surface (a host switches dialect by one import
+ one `Open` call):

| member | shape |
|---|---|
| `New(db *pgxdb.DB) (*Store, error)` | the outbox store; errors if `event_outbox` is missing (boot-time probe) |
| `(*Store).Append(ctx, recs...) error` | non-transactional convenience append (its own tx) |
| `(*Store).AppendTx(ctx, tx *pgxdb.Tx, recs...) error` | dialect-typed transactional appender — shares the caller's commit |
| `(*Store).ListUnpublished` / `MarkPublished` / `PurgePublished` | the poller's drain, idempotent mark, and retention purge |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

### `AppendTx` — the transactional outbox seam

`AppendTx` takes the integration's `*pgxdb.Tx` so an emitting feature's store can
write its domain rows and the outbox rows in **one commit** (true outbox
atomicity). No feature core ever sees the driver type: a future emitting store
consumer-declares a matching one-method port that `*Store` satisfies
*structurally* — zero import edge between the two store modules, the only shared
vocabulary being `*pgxdb.Tx` from the integration both already require (design
§5). In events v1 nothing wires it; it ships tested but unconsumed. This seam is
**unguarded** — no `make guard` target covers the per-store appender glue (design
§5 cost 1); the abstraction revisit trigger is the third emitting feature.

## Migrations

`migrations/0001_event_outbox.sql` (source `events`) is the canonical schema. The
turso sibling carries the **identical filename set** — same filename == same
logical schema step; content is per-dialect. After export, the host owns the final
migration stream in its own dir.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test runs, and the live
conformance + appender suites **skip loudly** without a DSN (`POSTGRES_TEST_DSN
not set — postgres conformance NOT verified`). A silent green that tested nothing
is the false-green failure mode this gating exists to prevent. Unlike the turso
sibling (which is `-tags=integration`), this store follows the pgx convention of
plain env-gating — no build tag.

The live conformance run is this store's dialect-parity gate. Spin a local
database and run it:

```sh
docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test -count=1 ./...
```

Each `newRepo` opens a connection, applies the migrations via the connector's
`RunMigrations`, `TRUNCATE ... CASCADE`s the outbox table (up front and via
`t.Cleanup`), and constructs the `Store` via `New` — so every subtest also
exercises the boot-time probe. `TestAppendTx` proves the transactional appender: a
record written via `AppendTx` inside an `InTx` block is visible after commit and
leaves no row when the surrounding transaction rolls back.

`make check` stays hermetic (the suite skips); `make test-stores` runs this live
path expecting `POSTGRES_TEST_DSN`.
