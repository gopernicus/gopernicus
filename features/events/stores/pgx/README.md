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
at construction (`pgxdb.ProbeTable`, qualified by the store's schema) and returns
`sdk.ErrNotFound` if the `events` source has not been applied — the failure
surfaces at wiring time, before the host serves traffic (design §5 mitigation b).
Scaffold this store's migrations with `ExportMigrations` and apply them with your
host's runner pre-boot, alongside every other feature source you wire.

## Surface

Mirrors the turso store's exported surface plus the Postgres-only `WithSchema`
option — SQLite has no schemas (a host switches dialect by one import + one `Open`
call):

| member | shape |
|---|---|
| `New(db *pgxdb.DB, opts ...Option) (*Store, error)` | the outbox store; errors if `event_outbox` is missing (boot-time probe) |
| `Option` | `func(*config)` — construction-time store configuration |
| `WithSchema(s pgxdb.Schema) Option` | places `event_outbox` in `s`; the zero `Schema` is the default (unqualified) |
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

## Schema

By default every statement names `event_outbox` unqualified — byte-for-byte the
SQL this store has always emitted. A host that keeps the feature tables in a
dedicated Postgres schema builds the schema value once and passes it to both the
runner and the store:

```go
schema, err := pgxdb.NewSchema(os.Getenv("EVENTS_SCHEMA")) // validated here, at the host
if err != nil { /* fail boot */ }

// Migrate the "events" stream into the schema — its own call, its own ledger.
err = pgxdb.RunMigrations(ctx, db, migrationsFS, "migrations/events", pgxdb.WithSchema(schema))

store, err := pgx.New(db, pgx.WithSchema(schema))
```

`WithSchema` never panics: the name is validated by `pgxdb.NewSchema` at the host,
before any store is constructed. The store and the migration stream must agree —
constructing for a schema the migrations never reached fails `New`'s boot-time
probe, naming the qualified table.

Quoting preserves case: `Auth` and `auth` are different schemas. Per-repository
*different* schemas within one feature are out of scope — this store has one table
and one schema, and the one-stream-per-schema migration model gives a split no
story.

**Advisory locks are database-scoped, not schema-scoped.** This store takes none
today, but the general rule matters when several schemas share one database: two
hosts using the same lock key text contend across schemas. Schema separation does
not partition the lock space.

## Migrations

`migrations/0001_event_outbox.sql` (source `events`) is the canonical schema. The
turso sibling carries the **identical filename set** — same filename == same
logical schema step; content is per-dialect. After export, the host owns the final
migration stream in its own dir.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test runs,
`TestNoBareTableReferences` parses this package's non-test sources and fails on any
table name that is not rendered through the store's `table` chokepoint (the table
list is derived from `MigrationsFS`, so a future migration is covered without a
hand list), `TestWithSchema` pins the option's two rendering states, and the live
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

Set `POSTGRES_TEST_SCHEMA` to run the same live suite inside a non-default schema:
setup drops the schema, migrates into it with `pgxdb.WithSchema`, constructs the
store with `WithSchema`, and truncates the qualified table. That leg is the
behavioral proof of the schema seam for every path conformance executes.
`TestLive_WithSchema_Decoy` proves the routing in both directions against a bare
`public.event_outbox` decoy: constructed with `WithSchema` before the schema is
migrated, `New` fails naming the qualified table; after migrating, a write lands in
the schema and leaves the decoy untouched; and with no option the same write lands
in the decoy and leaves the schema untouched.

`make check` stays hermetic (the suite skips); `make test-stores` runs this live
path expecting `POSTGRES_TEST_DSN`.
