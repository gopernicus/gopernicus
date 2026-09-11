# pockets/events/stores/turso

The events pocket's **Turso / libSQL** transactional-outbox store adapter — its
own module so a host that brings a different datastore never pulls `libsql` into
its module graph. It owns the SQL and the canonical migration files; the host
owns its database lifecycle.

It fills the events pocket's one outbound port, `outbox.EntryRepository`, over
the `integrations/datastores/turso` connector — fixed-width ISO-8601 `TEXT`
timestamps (lexicographic == chronological, which the unpublished-drain
`ORDER BY created_at` relies on), opaque `BLOB` payload, `event_id` as the primary
key and the at-least-once de-dupe key (a duplicate append surfaces as
`sdk.ErrAlreadyExists`).

## ⚠️ Prerequisite: apply the `events` migration source before wiring an appender

The outbox table belongs to migration source **`events`**, distinct from
`cms`/`auth`/`jobs`. The shared `(source, version)` migration ledger expresses
**no ordering between sources**, so a host that scaffolds another pocket's
migrations but not this store's would fail at *runtime*, not boot.

**`New(db)` guards against exactly that:** it probes for the `event_outbox` table
at construction and returns `sdk.ErrNotFound` if the `events` source has not
been applied — the failure surfaces at wiring time, before the host serves
traffic (design §5 mitigation b). Scaffold this store's migrations with
`ExportMigrations` and apply them with your host's runner pre-boot, alongside
every other pocket source you wire.

## Surface

| member | shape |
|---|---|
| `New(db *turso.DB) (*Store, error)` | the outbox store; errors if `event_outbox` is missing (boot-time probe) |
| `(*Store).Append(ctx, recs...) error` | non-transactional convenience append (its own tx) |
| `(*Store).AppendTx(ctx, tx *turso.Tx, recs...) error` | dialect-typed transactional appender — shares the caller's commit |
| `(*Store).ListUnpublished` / `MarkPublished` / `PurgePublished` | the poller's drain, idempotent mark, and retention purge |
| `ExportMigrations(dst string) error` | copies the canonical `migrations/*.sql` into the host's dir |
| `MigrationsFS` / `MigrationsDir` | the embedded canonical migration files |

### `AppendTx` — the transactional outbox seam

`AppendTx` takes the integration's `*turso.Tx` so an emitting pocket's store can
write its domain rows and the outbox rows in **one commit** (true outbox
atomicity). No pocket core ever sees the driver type: a future emitting store
consumer-declares a matching one-method port that `*Store` satisfies
*structurally* — zero import edge between the two store modules, the only shared
vocabulary being `*turso.Tx` from the integration both already require (design
§5). In events v1 nothing wires it; it ships tested but unconsumed. This seam is
**unguarded** — no `make guard` target covers the per-store appender glue (design
§5 cost 1); the abstraction revisit trigger is the third emitting pocket.

## Migrations

`migrations/0001_event_outbox.sql` (source `events`) is the canonical schema.
The existing payload column has TEXT affinity; new writes bind BLOB values and
preserve empty or binary bytes. Existing TEXT rows remain readable without a table
rebuild. Historical empty payloads already changed to `{}` cannot be recovered.
`0002_event_outbox_payload_bytes.sql` is an explicit no-op matching the pgx
bytea migration version. Apply it to retain the shared dialect version set. After export, the host owns the
final migration stream in its own dir.

`Append` validates every record's identity/type before any insert and commits the
whole batch in its own transaction. It does not join a transaction from context;
use `AppendTx` to share the domain write's commit. Malformed historical records
can block the poller; operator repair or quarantine is explicit host policy.

## Testing

`go test ./...` is hermetic: the `ExportMigrations` unit test runs, and the live
conformance + appender suites are behind `-tags=integration`.

The live conformance run is this store's dialect-parity gate. It **skips loudly**
without the env (`TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance
NOT verified`) — a silent green that tested nothing is the false-green failure
mode the gating exists to prevent. Run it against a live database:

```sh
TURSO_DATABASE_URL='libsql://…' TURSO_AUTH_TOKEN='…' \
  go test -tags=integration -count=1 ./...
```

Each `newRepo` opens a connection, applies the migrations via the connector's
`RunMigrations`, `DELETE`s the outbox table (up front and via `t.Cleanup`), and
constructs the `Store` via `New` — so every subtest also exercises the boot-time
probe. `TestAppendTx` (live leg) proves the transactional appender: a record
written via `AppendTx` inside an `InTx` block is visible after commit and leaves
no row when the surrounding transaction rolls back.

`make check` stays hermetic (the live suites are tag-gated); `make test-stores`
runs this live path expecting `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`.
