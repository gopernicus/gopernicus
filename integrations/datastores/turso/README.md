# integrations/datastores/turso

The datastore connector for Turso/libSQL (SQLite dialect). It wraps the libsql
driver behind the same connector shape as
`integrations/datastores/pgxdb`: `Config` / `Open` / `DB` / `MapError` /
`StatusCheck` / `RunMigrations`, plus the shared store-facing surface
(`Querier`, `Scanner`, `ExecAffecting`, the timestamp helpers, and `List`).

It owns "how to talk to libSQL," never any feature's SQL. App/feature
repositories consume this package's `*DB`.

## The list helper — `List[T]`, the pgxdb semantic twin

`List[T]`/`ListQuery[T]` implement the `sdk/crud` list standards — ordering
against a per-aggregate allow-list, bidirectional keyset cursors (reverse-probe
prev pages), and opt-in `COUNT(*)` totals — with observable semantics identical
to `pgxdb.List`. Like the pgxdb twin it switches on the request's **resolved
strategy** (`crud.StrategyCursor` / `crud.StrategyOffset`, explicit — never
inferred from the offset value) into a `listCursor` or `listOffset` flow; the
offset flow appends `LIMIT/OFFSET`, derives HasMore from its own over-fetch, and
emits no cursors. Like the pgxdb twin, `ListQuery[T]` carries an optional
`Limits` (`crud.Limits` — the resource's page-size default/max, passed to
`req.NormalizedLimit`; the zero value keeps `crud`'s `DefaultLimit`/`MaxLimit`).
The per-feature `storetest` conformance suites are the parity proof.

The twin is *semantic*, deliberately not idiomatic: it binds `?` placeholders
from an `Args []any` slice (no named-args emulation), scans through a
hand-written `Scan func(Scanner) (T, error)` callback (no struct scanning), and
formats time order values through `FormatTime` to match the fixed-width TEXT
storage. Order-identifier safety is allow-list membership — column strings are
store-authored constants, and raw request input never reaches SQL.

**Follow-up milestone (declared, not scheduled): `turso-crud-parity`** — named
parameters, struct scanning, and builder ergonomics to match the pgx toolkit's
authoring experience. Until it lands, this connector carries exactly the
semantics the conformance suites demand, nothing more.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — the `List`
behavior tests run against in-memory SQLite through the real driver.

Live conformance runs are per-feature (the `stores/turso` modules), gated on
`-tags=integration` + `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`, and only ever
against the authorized playground database. Unset, they skip loudly.
