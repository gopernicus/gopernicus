# Instance-scoped ambient transactions: `QuerierFrom` / `TxFrom` on pgxdb and turso

**Status:** EXECUTED 2026-10-27 (tags: `integrations/datastores/pgxdb` v0.15.0 + `integrations/datastores/turso` v0.10.0 @ d5c9f213, #233; `pockets/authorization/stores/pgx` v0.22.0 + `pockets/authorization/stores/turso` v0.21.0 @ 18bb3f57, #234; record + examples + scaffold PR-3, with `workshop/gopernicus` v0.9.2 for the scaffold pins). Ratified 2026-10-27 (owner: "all recs" — R-1 (a), R-2 remove, R-3 per-store now / family later, R-4 tag workshop v0.9.2, R-5 three PRs, R-6 waive the libSQL-server leg). Build-time owner rulings 2026-10-27: the sdk clause reworded to "may reject nesting on its own instance, never because another instance holds one"; AUDIT-056 added. PR-1 passed a lead-backend review (ship-with-edits, 1 MED + 7 LOW) and a data-integration review (ship-with-edits, 1 MED + 5 LOW), all folded; both upheld the six implementer deviations. `live-stores` green on `main` (runs 40583774102 @ d5c9f213, 40586686568 @ 18bb3f57). The libSQL-server turso leg was waived (local file accepted). The store-tag freeze lasted 21:05:13Z → 21:25:20Z; no store tag was cut inside it. As-built notes are under "As built" at the end. Release record: `RELEASING.md` "Ambient transactions per datastore instance". Was DRAFT rev 1 — two-reviewer gate RUN (lead-backend-engineer BE1–BE11,
data-integration-reviewer DI1–DI9) plus platform-sre SRE1–SRE14 on the release section; every
finding folded under "Decided by gate". Drafted from `origin/main` @ `25eadbd0` (S4d part 2
complete). Supersedes the R-1 (b) spec in `soc2-readiness/s4d2-session-lifetime.md` Notes and the
R-1 deferral block in `soc2-readiness/NEXT.md`.

## Context

The ambient transaction both SQL connectors stash for `transaction.Transactor` is instance-blind.
`integrations/datastores/pgxdb/transact.go` keys the `*Tx` under one private `txCtxKey{}`; the
package-level `TxFromContext(ctx)` returns whatever transaction any `*DB` put there;
`(*DB).Transact` / `TransactSnapshot` (`:28`, `:41`) refuse nesting when ANY transaction is in the
context; `(*DB).QuerierFrom` (`:66`) hands back a foreign `*Tx` without checking it belongs to `d`.
`integrations/datastores/turso/transact.go` (`:26`, `:37`, `:46`) has the same shape. In a process
with two `*DB` instances (gps-three-sixty: `db` + `iamDB`, `cmd/server/main.go:227,232` and
`cmd/worker/main.go:200,205` — two DATABASES: boot refuses an `IAM_DB_*` whose host, port and
database equal `DB_*`'s, `.env.example:56`) this produces (a) a store over `iamDB` called inside
`db.Transact` running its SQL on the business transaction — an error, or a silent wrong write when
table names collide — and (b) `iamDB.Transact` inside `db.Transact` returning a false
`ErrNestedTransact`. S5 R-2 deferred the fix; S4d part 2 R-1 (b) made it its own PR. This plan is
that PR train.

## Owner rulings (2026-10-27)

- Breaking changes are allowed; prefer the correct API over a deprecation cycle. "Let's get this
  correct."
- Two databases never share a transaction. Each store routes to ITS OWN instance's transaction or
  pool; independent transactions on different instances nest. Cross-database atomicity is not
  provided; ordering or an outbox is the host's job.
- Reference host: gps-three-sixty (`db` + `iamDB`, no `Transact` yet). battleboardhq is
  bootstrapping (one pool; `Scope.Run` = `db.Transact` + `set_config`; `Scope.Querier` calls
  `pgxdb.TxFromContext`) and gets a migration note. gps-360-go: no compatibility needed. segovia: out.
- Standing rules: the owner merges PRs and pushes tags; one worktree per PR at
  `../gopernicus-<slug>-<n>`; annotated unsigned tags, then cold-verify (`GOWORK=off`, empty
  `GOMODCACHE`, proxy, go 1.26.10 consumer); never `go work sync`; never ports 8082/55432;
  `make guard && make check` on every PR; nothing exploit-shaped in the public repo; migration sets
  additive-only and untouched here; never authn/authz; vars and consts first, then interfaces.

## Goal

Every store routes to ITS OWN `*DB`'s ambient transaction or pool, independent transactions on
different instances nest, same-instance nesting still fails loud, and no instance-blind accessor
survives in either connector's public API.

## Definition of Done

- `pgxdb` and `turso` carry the ambient `*Tx` under a per-instance context key; `(*DB).TxFrom(ctx)`
  and `(*DB).QuerierFrom(ctx)` answer only for their own instance; the package-level `TxFromContext`
  is gone from both.
- Same-instance nesting (`Transact`/`TransactSnapshot` in any combination) returns `ErrNestedTransact`;
  different-instance nesting succeeds and each callback context carries both transactions.
- Two-instance tests prove (a) and (b) on pgx (hermetic routing + live nesting/visibility, including
  an A-write inside B's callback rolling back with A) and on turso (hermetic, two in-memory
  databases); the same-database two-instance lock/busy behaviour is pinned on both; every existing
  single-instance test passes unchanged after the mechanical rename.
- The **sixteen** authorization store sites (eight pgx, eight turso) call `<db>.TxFrom(ctx)`; a
  foreign-instance store test exists in each store module with one assertion per site that is red
  on `origin/main` before the fix.
- Tags cut and cold-verified in order: pgxdb v0.15.0 + turso v0.10.0, then authorization stores pgx
  v0.22.0 + turso v0.21.0, with a store-tag freeze between; examples and the scaffold repinned;
  RELEASING carries upgrade notes by host shape, including the same-database two-instance row and
  Battleboard's `Scope.Querier` one-liner.
- The docs site states "two databases never share a transaction" (persistence guide, database-posture
  "Two roles", runbooks — replacing the `operations-runbooks.md:343-346` hazard), with the lock-timeout
  and nesting-order guidance.

## Out of scope

- Cross-database atomicity of any kind. Ordering, compensation (`OperationID` + `Fence`) or
  `pockets/events`' outbox is the host's job; the docs say so.
- Changing which authentication/cms/events/jobs/secrets store methods join an ambient transaction
  (all `InTx`/pool-path today; §8 confirms they stay).
- An `ErrForeignTransaction`-style erroring Querier (the S4d2 R-1 (b) sketch) — rejected under the
  owner's "route to its own instance" ruling; see "Rejected shapes".
- A storetest conformance family for foreign instances (would force an authorization core tag);
  R-3 records the follow-up shape.
- A runtime signal when a store routes to its own pool beside a foreign transaction (SRE12: that is
  the correct outcome, not a fault; the hazards are host design and live in docs).
- A `noCopy` guard on `DB` (BE10: docs only).
- segovia; gps-360-go compatibility; any migration (none is needed).

## Schema / datastore impact

None. No SQL, no migration, no ledger copy. Both dialects change only how the Go side finds the
ambient handle. Store adapter parity: the pgx and turso authorization stores move the same sixteen
sites to the same accessor; `stores/memory` never joins any transaction, which is exactly the dialect
stores' new foreign-instance behaviour (DI parity note) — untouched.

## Module / API impact

| Module | Change | Tag |
|---|---|---|
| `integrations/datastores/pgxdb` | BREAKING: `TxFromContext` removed; NEW `(*DB).TxFrom(ctx) (*Tx, bool)`; `QuerierFrom`/`Transact`/`TransactSnapshot` instance-scoped; `ErrNestedTransact` text reworded | **v0.15.0** (minor under v0; the tag message names the removal, the lockstep floor and the store-tag freeze) |
| `integrations/datastores/turso` | the same | **v0.10.0** |
| `pockets/authorization/stores/pgx` | eight-site move to `TxFrom`; foreign-instance test; README; repin pgxdb v0.15.0 (PR-2) | **v0.22.0** |
| `pockets/authorization/stores/turso` | the same (eight sites); repin turso v0.10.0 (PR-2) | **v0.21.0** |
| `sdk/capabilities/transaction` | COMMENT ONLY: a contract clause binding every implementation (instance-scoped nesting); the interface is unchanged | not tagged (the v0.2.0 precedent: comment-only, ships with the next sdk tag) |
| `pockets/authorization` (core) | COMMENT ONLY: `logic/relationships/relationship.go:157`, `logic/mutations/repository.go:18,32,50`, `logic/mutations/mutation_service.go:134`, `logic/tuplecache/tuplecache.go:63`, `stores/storetest/transactional.go:737,808,843`, `README.md:204,532,567,807` say "of the store's own `*DB`" | not tagged (the S6 #185 precedent) |
| `ARCHITECTURE.md:799`, `AUDIT.md:3759,4761` | the same qualifier | — |
| `Makefile` | `INTEGRATION_TAG_MODULES` gains `integrations/datastores/turso` (compile-only vet of its tagged tests under `make check`) | — |
| `examples/{auth-cms,production-baseline,cms}` | repin (untagged) | — |
| `workshop/gopernicus` | `init.go:100,105` connector pins → v0.10.0 / v0.15.0; pocket `stores/*/go.mod.tmpl` ×2; `templates/init/readme.md.tmpl:153` guard `@v0.9.2`; `scaffold_test.go:51-52` | **v0.9.2** patch, "scaffold pins only; no rule-set change" — YOUR CALL R-4 |
| authentication/cms/events/jobs/secrets stores | NOT touched, NOT tagged: none references `TxFromContext`/`QuerierFrom`; MVS lifts their connector pin inside a host (verified in set A) | — |
| `pockets/authorization/stores/goredis` | NOT touched; its `go.mod` requires authorization stores pgx v0.18.0 / turso v0.17.0 for TESTS only — recorded under "Not tagged" (SRE10) | — |

**Lockstep (the one upgrade hazard, and it is transitive).** The released authorization stores (pgx
v0.21.0, turso v0.20.0) call `pgxdb.TxFromContext`/`tursodb.TxFromContext`. pgxdb ≥ v0.15.0
requires authorization stores pgx ≥ v0.22.0 and turso ≥ v0.10.0 requires authorization stores turso
≥ v0.21.0 — whether the host bumps the connector directly or ANOTHER store's tag lifts it through
MVS (SRE3: the next repin train of any sibling store will drag pgxdb to v0.15.0). The failure is a
compile error naming the module path, e.g.
`…/pockets/authorization/stores/pgx@v0.21.0/mutations.go:81: undefined: pgxdb.TxFromContext`. That is
the loud, intended outcome — a deprecated instance-blind helper would let v0.21.0 compile and
silently stay wrong on eight sites. The hatch is to hold pgxdb v0.14.0 / turso v0.9.0 (every other
released store accepts them) until both pins can move together. Set A records the negative build
verbatim.

## Generated-artifact impact

None (`*_templ.go` untouched).

## The API (section 1 of the brief)

### Identity rule

**The identity of a datastore instance is the `*DB` pointer one `Open` call returned.** One `Open` =
one pool = one instance; neither connector has any other constructor (`pgxdb/postgres.go:517`,
`turso/turso.go:234`). Wrappers embed or hold the pointer (the `snapshotTransactor{*pgxdb.DB}` test
shape, `stores/pgx/lookup_snapshot_test.go:73`); never copy a `DB` by value — `cp := *db; &cp` is a
second identity over the same pool (BE10). Consequences, stated for hosts:

- **A store built with `WithSchema` over the same `*DB` shares its identity.** Schema is a SQL-text
  concern on the store; the pointer is the instance. `db.Transact` joins every store built over
  `db` whatever schema each addresses — cross-schema atomicity in one database works through ONE
  `*DB` (gps-three-sixty's `jobs` and app schemas on `db`; battleboard's `iam`/`jobs`/`battleboard`
  on its one pool). The `POSTGRES_TEST_SCHEMA` leg (`pgx-leg`, Makefile) exercises exactly this.
- **Two databases never share a transaction.** An inner `Transact` on instance B commits or rolls
  back on its own when its callback returns, while the outer on A may still roll back afterwards.
  The framework provides no cross-database atomicity; ordering (do the non-retractable write last),
  idempotent replays, or a durable outbox (`pockets/events`) are the host's job.
- **Two `Open` calls against the SAME database are two instances** — two pools, two connections, no
  shared visibility, exactly as two processes would be: two roles over one database (a runtime-role
  `*DB` and a migrator-role `*DB`), a duplicate `Open` with a different tracer, or two identical DSNs.
  Before this fix such a pair joined one transaction by accident; after it they are independent, and
  **a nested call can block on itself** (BE3/DI1/SRE2): on PostgreSQL the inner connection waits on
  a row or table lock the outer holds (`runWrite`'s `LOCK TABLE iam_tuples IN SHARE ROW EXCLUSIVE
  MODE`, `pgx/writes.go:142-145`, conflicts with the outer's ROW EXCLUSIVE) while the outer waits in
  the same goroutine for the inner to return — the server's deadlock detector cannot see a
  client-side wait, so with the default `lock_timeout = 0` it hangs and pins two pooled connections;
  on a SQLite file the outer `BEGIN IMMEDIATE` holds RESERVED, so every inner write waits
  `busy_timeout` and then fails `SQLITE_BUSY`, every time. Rule for hosts: **if you want one
  transaction, hold one `*DB`; if you must hold two over one database, set `DB_LOCK_TIMEOUT` and
  `DB_STATEMENT_TIMEOUT` on the runtime role** (`pgxdb/postgres.go:116-127`) and never nest them.
- **A nested request holds two connections** (DI8/SRE7): one from A's pool and one from B's. Nest
  instances in one fixed order process-wide, give every request a context deadline, and size
  `DB_MAX_CONNS` on the inner pool for the outer's concurrency; two workflows nesting A→B and B→A
  can exhaust both pools against each other.

### The context key

Replace the single private key with a per-instance key. Go idiom, no map, no mutex, one
`WithValue` node per instance:

```go
// txKey scopes an ambient transaction to the *DB that began it. Two instances
// layer two values on one context; each TxFrom reads only its own.
type txKey struct{ db *DB }
```

`Transact`/`TransactSnapshot` stash with `context.WithValue(ctx, txKey{d}, tx)`. `context.Value`
compares keys with `==`, so the pointer field gives instance identity for free; the key type stays
private, so no other package can forge or read a slot (the `transaction.Transactor` contract's
"no shared untyped stash" clause holds). `Tx` gains no owner field.

### Accessors

```go
// TxFrom returns d's ambient transaction, stashed by d.Transact or
// d.TransactSnapshot, or (nil, false) when ctx carries none for d — including
// when it carries another instance's.
func (d *DB) TxFrom(ctx context.Context) (*Tx, bool)

// QuerierFrom returns d's ambient transaction when ctx carries one and d's pool
// otherwise. Another instance's transaction is invisible: the SQL runs on d.
func (d *DB) QuerierFrom(ctx context.Context) Querier
```

The package-level `TxFromContext(ctx)` is **removed** on both connectors. Why remove rather than
deprecate: it has no instance and therefore no correct answer in a multi-instance process; every
legitimate caller holds a `*DB` (every store has `s.db`/`m.db`; battleboard's `Scope` has `s.db`,
`scope.go:29`); keeping it compiles the exact bug this plan fixes; and a deprecated shim cannot even
be built over `txKey{db}` — `context.Value` cannot enumerate keys, so it would need a second,
instance-blind key written on every stash, putting the bug back inside the connector (BE7).
Breaking under v0 ships MINOR. The guard rule at `workshop/gopernicus/internal/commands/guard_rules.go:335`
matches `Tx`/`Transactor` identifiers, so `TxFrom` does not trip it.

### The nesting rule

- `d.Transact(ctx, …)` or `d.TransactSnapshot(ctx, …)` when `d.TxFrom(ctx)` is already true →
  `ErrNestedTransact` before `Begin` (either method inside either, unchanged).
- When ctx carries only OTHER instances' transactions → allowed; the callback context carries both;
  `a.TxFrom`, `b.TxFrom` each find their own; a third `a.Transact` inside still fails.
- `TransactSnapshot` follows the same key and rule; its REPEATABLE READ applies to its own
  connection only. An inner `b.Transact` inside `a.TransactSnapshot` runs at B's default isolation;
  an authorization `ReadTupleSnapshot` over B inside it checks B's isolation only (`tuples.go:384` →
  `b.TxFrom`), so `tuples.ErrSnapshotIsolation` is now correctly instance-local — and a foreign
  transaction falls through to `s.db.BeginRead` instead of a wrong isolation read (DI).
- `ErrNestedTransact`'s text becomes "pgxdb: nested Transact — this database instance already holds
  a transaction; pass the ambient ctx to this instance's repositories instead" (BE question: the old
  advice is misleading for a cross-instance caller). The sentinel identity is unchanged; alert on
  `errors.Is`, not the message (SRE12, upgrade row 9).

### Rejected shapes

- **A single key plus an owner field on `Tx`** (the S4d2 R-1 (b) owner-compare sketch,
  `s4d2-session-lifetime.md:434`): one slot. `b.Transact` nested in `a.Transact` overwrites it; A's
  stores called inside B's callback find B's transaction, see a foreign owner, and fall back to A's
  POOL — A's writes silently commit outside A's transaction (BE6). Only per-instance keys let one
  context carry both. task-2 pins this: an A write inside B's callback must roll back with A.
- **Erroring Querier on a foreign transaction** (`ErrForeignTransaction`): needs the instance-blind
  "is any tx present" lookup to detect foreignness and refuses the legitimate two-DB workflow
  (`db.Transact { authorization over iamDB }`) that gps-three-sixty will write next.
- **Keying by pool (`*pgxpool.Pool`/`*sql.DB`) instead of `*DB`:** equivalent today and worse
  tomorrow if a wrapper ever shares a pool.

## The sdk seam (section 2)

`sdk/capabilities/transaction.Transactor` needs **no interface change** and has no conformance suite
to extend (`transaction.go` is the package). Its doc comment (`transaction.go:7-15`) gains a clause
that binds every implementation, not a note about the bundled ones (BE8): "fn's context may carry
transactions of other datastore instances; an implementation neither joins nor refuses another
instance's transaction, and rejects nesting only on its own instance — two datastores never share a
transaction." The existing sentence keeps the error type: "Bundled connectors reject a nested
Transact on the same instance with a connector error." No sdk tag (precedent: the v0.2.0 ruling was
recorded as a comment; S6 shipped pocket doc comments untagged). The sdk-vs-logic test is unaffected.

## Call sites moved, per module (section 3)

Authoritative inventory, re-grepped at `origin/main` @ `25eadbd0` (`git grep -n -E
"TxFromContext|QuerierFrom\(" origin/main -- '*.go'`). **Sixteen production sites — eight per
dialect** (the S4d2 block counted seven pgx FILES; `operations.go` has two sites). `QuerierFrom`
callers keep their text; only `TxFromContext` callers move. The receivers are right at every site:
`runWrite`'s `db` is always `s.db` (pgx `relationships.go:610`, `tuples.go:434,453,480`; turso
`relationships.go:606`, `tuples.go:420,439,466`); `prepareTupleSource`'s `db` is the constructor's
(pgx `postgres.go:134`, turso `turso.go:100`); `mutations_eval.go:28` sets `readQuerier = tx.Tx`
explicitly and is untouched (DI).

**`integrations/datastores/pgxdb`** — `transact.go` (`:19` key, `:28` and `:41` nested checks, `:57`
`TxFromContext`, `:66` `QuerierFrom`); tests `transact_test.go:16-18,65-67` (rename
`TestTxFromContext_Absent` → `TestTxFrom_Absent`), `transact_snapshot_test.go:18,64`,
`snapshot_isolation_test.go:71` (`txCtxKey{}` → `txKey{db}` with the SAME `db`); README
`:1058-1066` gains new text (it never named `TxFromContext`) and `:1085` the per-instance sentence.

**`integrations/datastores/turso`** — `transact.go` (`:17`, `:26`, `:37`, `:46`); tests
`tx_lifecycle_test.go:18`, `transact_integration_test.go:58-60`; README `:258-265`.

**`pockets/authorization/stores/pgx`** — eight sites, each `pgxdb.TxFromContext(ctx)` → the
receiver's instance: `mutations.go:81` (`m.db.TxFrom`), `operations.go:68` and `:126` (`m.db.TxFrom`),
`read_snapshot.go:19` (`s.db.TxFrom`), `relationships.go:618` (`s.db.TxFrom`), `tuple_cache.go:33`
(`db.TxFrom` — the parameter), `tuples.go:384` (`s.db.TxFrom`), `writes.go:97` (`db.TxFrom` — the
parameter). Test: `audit_live_test.go:161`. Unchanged `QuerierFrom` callers: `audit.go:114`,
`operations.go:153`, `read_model.go:20`, `tuples.go:46`; comment `relationships.go:595`.

**`pockets/authorization/stores/turso`** — eight sites: `mutations.go:41`, `operations.go:67`,
`operations.go:117`, `read_snapshot.go:19`, `relationships.go:614`, `tuple_cache.go:40` (`db.TxFrom`),
`tuples.go:376`, `writes.go:94` (`db.TxFrom`). Tests: `audit_live_test.go:163`, `integrity_test.go:73`.
Unchanged `QuerierFrom` callers: `audit.go:109`, `operations.go:149`, `read_model.go:19`, `tuples.go:47`;
comments `turso.go:222,245`.

**Contract texts that say "rejects an ambient transaction" without an instance qualifier** (BE5) —
all become "an ambient transaction of the store's own `*DB`": `ARCHITECTURE.md:799`,
`AUDIT.md:3759,4761`, `pockets/authorization/README.md:204,532,567,807`,
`pockets/authorization/logic/mutations/repository.go:18,32,50`,
`pockets/authorization/logic/mutations/mutation_service.go:134`,
`pockets/authorization/logic/tuplecache/tuplecache.go:63` (delivery `Snapshot`/`Acknowledge` also
flip via `CacheableContext`), `pockets/authorization/logic/relationships/relationship.go:157`
(`QuerierFrom/TxFromContext` → `QuerierFrom/TxFrom`), `pockets/authorization/stores/storetest/transactional.go:737,808,843`,
`pockets/authorization/stores/pgx/README.md:63,195`, `pockets/authorization/stores/turso/README.md:62,86,181`.

**`examples/auth-cms`** — `internal/outbound/domains/documents/postgres.go:48,96` use
`p.db.QuerierFrom(ctx)`: API unchanged, no edit; repin only.

**Follow-up only (DI9):** `pockets/authentication/stores/pgx/session_activity.go:39-40` and turso
`:66-67` cite "`(pgxdb.QuerierFrom)`" as a package-level name in a comment; those modules are not
touched here.

**Hosts (outside this repo, upgrade notes):** battleboardhq
`internal/outbound/domains/tenancy/scope.go:62` `pgxdb.TxFromContext(ctx)` → `s.db.TxFrom(ctx)`;
`organizations.go:55,66` (`s.db.QuerierFrom`) unchanged. gps-three-sixty: no site; pins only.

**Semantics note on the authorization "refuse ambient" sites.** `Apply`/`Fence`/`Expire`
(`mutations.go:81`, `operations.go:68,126` and the turso twins) return
`mutations.ErrMutationInsideTransaction` when THEIR OWN instance has an ambient transaction. A
foreign instance's transaction no longer triggers the refusal: the atomic command cannot join it
anyway, so it runs in its own transaction on its own database — the documented "two databases never
share a transaction" behaviour, and the only reading under which gps-three-sixty can call
authorization inside a business transaction at all. Likewise `CacheableContext` (`read_snapshot.go:19`;
returning true under a foreign transaction is correct — the store's own pool holds the committed
state the cache mirrors) and `prepareTupleSource` (`tuple_cache.go:33/40`) judge only their own
instance. Two consequences the host must know (BE4/SRE8, upgrade row 7): a grant applied inside a
business transaction that later rolls back STAYS applied; and checks inside a business transaction
may now be served from the tuple cache. And one trade in the same-database shape (BE3/DI1): the old
refusal was an accidental fail-fast for a pair of `*DB` over one database — under (a) the inner write
can wait on the outer's lock (pgx, unbounded without `DB_LOCK_TIMEOUT`) or fail busy after
`busy_timeout` (turso file). See R-1.

## Tests (section 4)

Every new test runs its callbacks under a bounded context (≥ 10 s — generous, because turso
`Tx.Commit` uses the Begin context, `tx.go:62-66`, and an expired deadline turns a commit into a
rollback with a misleading failure) and every live pgx DSN carries `lock_timeout` (DI3/DI5) so a
mistake fails fast instead of hanging CI.

**pgxdb, hermetic** (`transact_instances_test.go`, no DSN): `a, b := &DB{}, &DB{}`; `txA, txB :=
&Tx{}, &Tx{}`; `ctx` carrying `txKey{a}` and `txKey{b}` layered; assert `a.TxFrom(ctx)` = `(txA, true)`,
`b.TxFrom(ctx)` = `(txB, true)`; on a context carrying only A's key `b.TxFrom` = `(nil, false)`,
`a.QuerierFrom == Querier(txA)`, `b.QuerierFrom == Querier(b)`; `a.Transact(ctx, …)` and
`a.TransactSnapshot(ctx, …)` return `ErrNestedTransact` without invoking the callback (extends
`TestTransactSnapshotNestingRejectedBeforeBegin`). Never call `Transact` on a zero `&DB{}` whose own
key is absent: `pool` is nil and it panics (DI). This is the (a) routing proof and the same-instance
half of (b).

**pgxdb, live** (`transact_instances_live_test.go`, `POSTGRES_TEST_DSN`): a new helper
`liveInstance(t) (*DB, table string)` returning the probe table name explicitly and a
`count(t, db, table)` helper — `transactLiveDB` stores its one table in `t.Setenv("TRANSACT_PROBE_TABLE")`
(`transact_test.go:43-47`) so two calls overwrite each other (DI3). Two `Open` calls on the same DSN
("two handles to the same database" is the shape and needs one Postgres), probe tables T1 (A) and T2
(B) so no row lock is shared. Assertions: inside `a.Transact`, an uncommitted row through
`a.QuerierFrom(ctx)` into T1 is invisible through `b.QuerierFrom(ctx)` (B's pool, another connection)
and visible through `a.QuerierFrom(ctx)` — no SQL of B ran on A's connection; `b.Transact(ctx, …)`
inside returns nil (not `ErrNestedTransact`), its callback sees `a.TxFrom` AND `b.TxFrom` true, writes
T2 through `b.QuerierFrom`, **and writes a second T1 row through `a.QuerierFrom` — that row must roll
back with A's outer error** (BE6: proves A's stores still join A inside B's callback); `b.Transact`
nested again inside → `ErrNestedTransact`; `a.Transact` inside → `ErrNestedTransact`. After the outer
returns nil every row exists; a second run where the outer returns an error after the inner committed
leaves T2's row and none of T1's. Repeat the different-instance case with `a.TransactSnapshot` outer.
**Same-database lock pin (DI1):** inside `a.Transact` holding a T1 row, `b.Exec("LOCK TABLE T1 IN
ACCESS EXCLUSIVE MODE")` over a DSN with `lock_timeout=200ms` returns a lock-timeout error — the
documented hazard, bounded.

**turso, hermetic** (`transact_instances_test.go`, no build tag): two `newMemDB(t)` instances are two
separate `:memory:` databases (so the inner `BEGIN IMMEDIATE` cannot be blocked by the outer's write
lock). Create the IDENTICAL table in both (BE11/DI6: this models the collision case — a misroute
shows as "B sees A's row", a correct route as "B counts 0"; without it B errors "no such table" and
proves nothing). Assert: A's uncommitted row visible through `a.QuerierFrom`, B counts 0; the inner
`b` write lands in B only (A counts 0 for it); the outer error leaves B's row and drops A's; the
`TxFrom`/nesting assertions as on pgx minus `TransactSnapshot`. `MaxOpenConns(1)` means any stray
`a.Exec` (pool) while A's transaction is pinned blocks until the deadline — never do it. **Same-file
busy pin (DI1):** two `Open` on one temp-dir `file:` database with `Config.BusyTimeout: 50 *
time.Millisecond`; `b.Transact` inside `a.Transact` returns the busy error.

**Same-instance behaviour unchanged:** every existing `Transact*`/`tx_lifecycle`/`snapshot_isolation`
test keeps its assertions after the mechanical rename; `TestTransact_NestedFailsLoud`, the
`ErrNestedTransact` snapshot tests, `RunTransactional`, `RunSnapshotCheckAmbient`, `RunLookupAmbient`,
and the audit_live/integrity ambient tests are the regression floor (every one passes the SAME `*DB`
as backend and transactor, so the own-instance branch stays exercised).

**Authorization stores, foreign-instance test** (one file per module; **each assertion confirmed red
on `origin/main` before the fix**, DI4 — a shared-schema read of committed rows passes on the old code
too and proves nothing):
- pgx `foreign_instance_live_test.go`: `db := openAndMigrate(t, dsn)` backs the repositories; the
  foreign instance is a **bare `pgxdb.Open` on the same DSN** with `t.Cleanup(foreign.Close)` — NOT
  `openAndMigrate`, whose re-migrate and `TRUNCATE … CASCADE` cleanup on the same schema adds ordering
  hazards (BE11/DI5); the foreign transaction touches no `iam_*` table except one ACCESS SHARE
  SELECT. Per site: **reads** (`Recorded`, `read_model`, `tuples.reader`, `rawView`
  `relationships.go:618`) — inside `foreign.TransactSnapshot`, pin a snapshot with
  `foreign.QuerierFrom(ctx).QueryRow("SELECT EXISTS(SELECT 1 FROM iam_tuples)")`, commit a fixture
  through the store with a bounded non-ambient context, then read through the store with the ambient
  ctx: it must see the fixture; **`tuples.go:384`** — `ReadTupleSnapshot` inside `foreign.Transact`
  (READ COMMITTED) succeeds (old code: `tuples.ErrSnapshotIsolation`); **`read_snapshot.go:19`** —
  in-package `tupleSource.CacheableContext(foreignCtx) == true`; **`tuple_cache.go:33`** — constructing
  repositories `WithTupleCache()` inside `foreign.Transact` succeeds (old code: `ErrInvalidInput`
  "ambient construction"; use the schema-isolated pgx `cacheFixture(t, true)`); **`Apply`, `Fence`,
  `Expire`** inside `foreign.Transact` return results, not `ErrMutationInsideTransaction`; **writes**
  (`writes.go:97`) — a facade write inside a foreign callback that returns an injected error is still
  there afterwards. Plus one nesting assertion: `foreign.Transact` nested inside `db.Transact` succeeds
  and the store still joins `db`'s transaction.
- turso `foreign_instance_test.go`: hermetic, two **migrated** `canonicalFixture(t, false)` files
  (each `t.TempDir()`, distinct files — no `BEGIN IMMEDIATE` contention; `emptyCanonicalFixture` would
  fail "no such table" on the old code instead of reproducing the silent wrong-database write, DI7);
  the same assertion set minus the isolation check, plus: after the foreign callback's injected error,
  the store's file has the tuple AND the foreign file has zero `iam_tuples` rows.

**storetest angle:** none in this train (R-3). With `TxFromContext` removed from both connectors there
is no instance-blind path left for a store to misuse — the compiler is the conformance check; the
per-store tests above pin the behaviour. For R-3 (b) later: `RunForeignInstance(t, newRepos, foreign
transaction.Transactor)`, which `stores/memory` runs with a stub transactor that calls `fn(ctx)` (DI).

**S4d part 2 pool-path methods:** `session_activity.go` (pgx `GetByRefreshHashWithActivity:46`,
`RotateAt:74`, `DeleteIfIdle:96`, `ProbeSchema`; turso `:73,97,116`) pass `s.db` straight to
`QueryOne`/`ExecAffecting`/`QueryRow` — the pool path never consults the context, so they are
unaffected by the key change; `storetest/session_activity.go:17-18` already pins "every method runs
on the store's pool path". No test change.

## Release (section 5)

Standing rules apply (Owner rulings). Worktrees: `../gopernicus-querierfrom-1`
(PR-1), `-2` (PR-2), `-3` (PR-3), each created from `origin/main` @ `25eadbd0` or later with a clean
`git status` — NOT the primary checkout, which sits on `docs/workshop-v0.9.1-release-record` (BE1).
Local Postgres: a throwaway container or a scratch database; if 5432 is taken by a native PG17,
create a database and point `POSTGRES_TEST_DSN` at it — never 55432/8082 (SRE13); the full pgxdb
live suite is destructive (limiter legs create and `DELETE` on `ratelimit_windows`).

**Why PR-1 carries the store call sites.** `make check` and the `live-stores` dispatch build every
module under `go.work` (neither `Makefile` nor `.github/workflows/*.yml` sets `GOWORK=off`, BE9).
Removing `TxFromContext` in the connectors alone turns the authorization stores red on `main` the
moment PR-1 merges. So PR-1 moves the sixteen sites in the same PR (they compile under the
workspace). This mirrors the pgxdb v0.5.0 WithSchema "ONE train" precedent, not S6's additive
#183/#185 split.

**The store-tag freeze (SRE4).** From PR-1's merge until PR-2's merge, `main`'s authorization store
`go.mod`s pin pgxdb v0.14.0 while their code calls `TxFrom`: `GOWORK=off` builds of those two
modules are red and nothing in `check.yml` runs `GOWORK=off`. A store tag cut from `main` in that
window would publish a permanently broken version, and pgxdb v0.15.0 sits on the proxy with no
compatible authorization store. Therefore: PR-1's description and both connector tag messages
declare "no `pockets/authorization/stores/*` tag until PR-2 merges"; **PR-2 is staged in its
worktree before PR-1 merges** so the gap is one owner session; the RELEASING table records both
merge timestamps so the window is visible.

**Turso live legs do not run in CI (SRE1).** `live-stores.yml:24-25,78`: no Turso secrets are
configured, the turso `-tags=integration` legs SKIP LOUDLY in CI; the authorization turso suite
additionally refuses unless `AUTHORIZATION_TURSO_DISPOSABLE_URL` equals `TURSO_DATABASE_URL`
(`fixture_safety_test.go:13-22`); and the connector's own `transact_integration_test.go` is in neither
`make test-stores` nor `INTEGRATION_TAG_MODULES` (`Makefile:12,19`). So the turso legs are LOCAL and
explicit, before each turso tag, in the S4d part 2 local-file shape:
`TURSO_DATABASE_URL=file:<scratch>/leg.sqlite TURSO_AUTH_TOKEN=local AUTHORIZATION_TURSO_DISPOSABLE_URL=file:<scratch>/leg.sqlite`
then `cd integrations/datastores/turso && go test -tags=integration ./... -count=1` and
`cd pockets/authorization/stores/turso && go test -tags=integration ./... -count=1`. The
libSQL-server leg (`ghcr.io/tursodatabase/libsql-server`, `http://127.0.0.1:8081`, `TURSO_AUTH_TOKEN=local`)
is run or WAIVED by the owner as in S4d parts 1 and 2 — R-6. PR-1 also adds
`integrations/datastores/turso` to `INTEGRATION_TAG_MODULES` so `make check` at least vets the
tagged test file.

| PR | Worktree / branch | Content | Gate | Tags after merge |
|---|---|---|---|---|
| **PR-1** connectors + authorization sites + contract texts | `gopernicus-querierfrom-1`, `datastores/querierfrom-instance-scope` | tasks 1–7 | `make guard && make check`; local live: `make test-stores` (needs the DSN; turso legs skip) + `cd integrations/datastores/pgxdb && go test -race ./...` + the two local turso `-tags=integration` legs above; `live-stores` dispatch green on merged `main` | `integrations/datastores/pgxdb/v0.15.0`, `integrations/datastores/turso/v0.10.0` at the squash commit (messages: the removal, the lockstep floor, the freeze); cold-verify **set A** incl. the negative |
| **PR-2** authorization stores repin (staged before PR-1 merges) | `gopernicus-querierfrom-2`, `authorization-stores/repin-pgxdb-0.15` | task 8 | `make guard && make check`; per store `GOWORK=off go build ./... && GOWORK=off go test ./... && GOWORK=off go vet -tags=integration ./...` (SRE14); `live-stores` dispatch green; the two local turso legs again | `pockets/authorization/stores/pgx/v0.22.0`, `pockets/authorization/stores/turso/v0.21.0`; cold-verify **set B** + the gps-three-sixty shape |
| **PR-3** examples + scaffold + docs + record | `gopernicus-querierfrom-3`, `docs/querierfrom-release-record` | tasks 9–12 | `make guard && make check && make docs-build` (note: `warm-scaffold-cache` inside `make check` resolves the new connector tags through the proxy — both must have polled first, SRE5); **set C** | `workshop/gopernicus/v0.9.2` (R-4); cold `init --db pgx` / `--db turso` from `@v0.9.2` |

**Tag order:** pgxdb + turso (PR-1) → authorization stores (PR-2) → workshop (PR-3). Never a
dependent before its dependency; never a store tag inside the freeze.

**Cold-verify / MVS sets** (scratch consumer, `go 1.26.10`, `GOWORK=off`, empty `GOMODCACHE`,
`proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build ./...`):
- **Set A (after PR-1 tags):** pgxdb v0.15.0 beside every released NON-authorization pgx store —
  authentication pgx v0.16.0, cms pgx v0.5.0, events pgx v0.4.0, jobs pgx v0.7.0, secrets pgx v0.2.1 —
  selects pgxdb v0.15.0 and builds (none references the removed symbol; MVS lifts their v0.14.0 pin).
  The turso twin: turso v0.10.0 beside authentication turso v0.15.0, cms turso v0.4.0, events turso
  v0.3.0, jobs turso v0.6.0 builds. **Negative, recorded on purpose and verbatim (SRE9):** a consumer
  requiring pgxdb v0.15.0 + authorization pgx v0.21.0 — `go mod tidy` succeeds, `go build ./...`
  fails with the compiler line including the module path (`…/stores/pgx@v0.21.0/mutations.go:81:
  undefined: pgxdb.TxFromContext`), paired with the fix (`go get
  github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx@v0.22.0`); turso v0.10.0 +
  authorization turso v0.20.0 likewise.
- **Set B (after PR-2 tags):** authorization v0.31.0 + stores pgx v0.22.0 / turso v0.21.0 + pgxdb
  v0.15.0 / turso v0.10.0 builds. **gps-three-sixty shape:** authentication v0.32.0 + authentication
  pgx v0.16.0 + authorization v0.31.0 + authorization pgx v0.22.0 + jobs pgx v0.7.0 + pgxdb v0.15.0 +
  authorization goredis v0.6.0 builds; `go mod verify` passes.
- **Set C (PR-3):** auth-cms, production-baseline and cms from an empty cache over the proxy:
  `all modules verified`, `go build ./...` per example (covers `production-baseline/cmd/bootstrap`,
  both `workshop/migrations` runners and auth-cms's jobs-store graph, SRE11). Excluded with reason:
  external-identity, goth-showcase, jobs-minimal, minimal have no SQL connector in their module graph.

**Pins moved.** PR-2: authorization stores pgx/turso `pgxdb v0.14.0 → v0.15.0`, `turso v0.9.0 →
v0.10.0` by explicit `GOWORK=off go get`, then `go mod tidy`; the authorization core pin (v0.30.0)
and sdk pin (v0.18.0) stay — pgxdb v0.15.0 still requires sdk v0.18.0, and pins move only when
needed. PR-3: auth-cms (pgxdb, turso, authorization pgx v0.22.0), production-baseline (pgxdb,
authorization pgx v0.22.0), cms (turso); scaffold: `init.go:100,105`, the two pocket
`go.mod.tmpl`s, `readme.md.tmpl:153`, `scaffold_test.go:51-52`.

**Standing note for RELEASING (SRE3/SRE10):** future store repin trains name the authorization floor
(pgxdb ≥ v0.15.0 ⇒ authorization pgx ≥ v0.22.0; turso ≥ v0.10.0 ⇒ authorization turso ≥ v0.21.0); a
future goredis-store repin of the connectors must move its test-only authorization store requires
(pgx v0.18.0 / turso v0.17.0 today) to ≥ v0.22.0 / v0.21.0 in the same commit or its `GOWORK=off
-tags=integration` build breaks.

## RELEASING upgrade notes by host shape (section 6)

Drafted for the PR-3 record; the "Read first" box and the rows are the plan's, verbatim.

> **Read first:** `grep -rnE 'TxFromContext|QuerierFrom\(|\.Transact\(|TransactSnapshot\(|ErrNestedTransact|(pgxdb|tursodb|turso)\.Open\(' --include='*.go'` — count your `Open` calls.
> **By host shape:** every host → rows 1, 2, 9; ONE `Open` per process → row 3 (no behaviour
> change); two `Open`s over two DATABASES → rows 4, 5; two `Open`s over ONE database → row 6 (read
> it first); hosts with their own `TxFromContext` caller (battleboard) → row 7; gps-three-sixty →
> row 8.
> - **Pins (lockstep, transitive):** pgxdb **v0.15.0** WITH authorization stores pgx **v0.22.0**;
>   turso **v0.10.0** WITH authorization stores turso **v0.21.0** — whether you bump the connector
>   or another store's tag lifts it. authentication/cms/events/jobs/secrets stores, every pocket
>   core, sdk, views, ui/goth are unchanged (MVS lifts their connector pin).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | **BREAKING:** `pgxdb.TxFromContext` / `tursodb.TxFromContext` removed; `(*DB).TxFrom(ctx)` added | a caller of the package-level helper stops compiling; replace with `<the *DB>.TxFrom(ctx)` | none (the instance is required) | pgxdb@v0.15.0, turso@v0.10.0 |
| 2 | **Lockstep:** authorization stores pgx v0.21.0 / turso v0.20.0 do not build against the new connectors — `…/stores/pgx@v0.21.0/mutations.go:81: undefined: pgxdb.TxFromContext` — and MVS brings the new connector in through ANY later store tag that requires it | the error names a module you never touched | hold pgxdb v0.14.0 / turso v0.9.0 (every other store accepts them) until both pins move together | authorization stores pgx@v0.22.0, turso@v0.21.0 |
| 3 | **One `Open` per process: no behaviour change.** `QuerierFrom`, `Transact`, `TransactSnapshot`, `ErrNestedTransact` behave exactly as before | nothing | — | pgxdb@v0.15.0, turso@v0.10.0 |
| 4 | **Two `*DB` over two databases:** a store over B called inside `A.Transact` now runs on B's pool (was: on A's transaction — the wrong database) | a workflow that "worked" because table names collided now writes to the right database; a "relation does not exist" error goes away | — | pgxdb@v0.15.0, turso@v0.10.0 |
| 5 | **Two `*DB` over two databases:** `B.Transact` inside `A.Transact` is allowed (was: false `ErrNestedTransact`); `A.Transact` inside `A.Transact` still refuses; a nested request holds one connection from each pool | the inner commits on its own when its callback returns; the outer may still roll back — no cross-database atomicity; order non-retractable writes last or use `pockets/events`' outbox; nest instances in one fixed order, give requests a deadline, size `DB_MAX_CONNS` on the inner pool for the outer's concurrency | — | pgxdb@v0.15.0, turso@v0.10.0 |
| 6 | **Two `*DB` over ONE database (two roles, a duplicate `Open`):** a store over B inside `A.Transact` WAS on A's transaction (atomic by accident) and is NOW on B's pool | no longer atomic with A; it lock-waits on rows or tables A holds — on PostgreSQL unbounded unless `DB_LOCK_TIMEOUT`/`DB_STATEMENT_TIMEOUT` are set (the server's deadlock detector cannot see a client-side wait; the request pins two connections); on a SQLite file every inner write fails `SQLITE_BUSY` after `busy_timeout`; symptom: requests stall in pool acquire / `idle in transaction` on A while B saturates | build those stores over A (one `*DB`, one transaction); otherwise set `DB_LOCK_TIMEOUT` and never nest the pair | pgxdb@v0.15.0, turso@v0.10.0 |
| 7 | **Battleboard** `internal/outbound/domains/tenancy/scope.go:62`: `tx, inTx := pgxdb.TxFromContext(ctx)` → `tx, inTx := s.db.TxFrom(ctx)`; the `Run` doc comment's "or inside any other pgxdb transaction, is refused" becomes "or inside another transaction of the same `*DB`" | one line; `Scope.Querier` keeps returning the scope's own transaction so `set_config('app.tenant_id', …, true)` stays transaction-local and FORCE RLS keeps answering tenant-owned stores; `organizations.go` (`QuerierFrom`) unchanged; one pool, so rows 3 and 7 are its whole upgrade; pins per row 2 (it pins authorization pgx v0.21.0 today) | — | host |
| 8 | **gps-three-sixty:** no code change (no `Transact`, no `TxFromContext`); two databases, so row 6 does not apply. After row 2, a future business `db.Transact` around authorization/authentication calls over `iamDB` runs them on `iamDB`, and `iamDB.Transact` may nest inside it. Two flips WITHOUT a signal under R-1 (a): `Apply`/`Fence`/`Expire` over `iamDB` inside `db.Transact` used to refuse (`ErrMutationInsideTransaction`, for the wrong reason) and now commit on their own — a grant applied inside a business transaction that later rolls back STAYS applied (a dangling permission on a resource never created); and `CacheableContext` is now true there, so checks inside a business transaction may be served from the goredis tuple cache | write grants after the business commit, or compensate on rollback with `OperationID` + `Fence`; pins only | — | host |
| 9 | `ErrNestedTransact` message text: "this database instance already holds a transaction; pass the ambient ctx to this instance's repositories instead" | a log alert keyed on the old string stops matching | alert on `errors.Is(err, pgxdb.ErrNestedTransact)` / `tursodb.ErrNestedTransact`, not the message | pgxdb@v0.15.0, turso@v0.10.0 |
| 10 | sdk `transaction.Transactor` contract clause: an implementation neither joins nor refuses another instance's transaction; nesting is per instance | none | — | sdk (untagged) |
| 11 | scaffold pins pgxdb v0.15.0 / turso v0.10.0; emitted README runs the guard `@v0.9.2` | a cold `init` lands on the fixed connector; hosts pinning the guard at v0.9.1 need not bump (no rule-set change) | — | workshop@v0.9.2 (R-4) |

**S4d part 2 interaction (recorded):** `RotateAt`/`DeleteIfIdle` stay on the pool path; the fix does
not change them (section 8).

## Docs (section 7)

- `workshop/documentation/docs/guides/persistence.md` "Transactions stay behind ports": add the
  paragraph **"Two databases never share a transaction"** — the identity rule (one `Open`, one
  instance; pointer identity, never copy by value; `WithSchema` stores share it; two `Open`s are two
  instances), cross-instance nesting allowed, same-instance refused, no cross-database atomicity,
  ordering/outbox is the host's, the same-database pair hazard with `DB_LOCK_TIMEOUT` as the bound,
  and the fixed nesting order / pool sizing rule.
- `workshop/documentation/docs/guides/database-posture.md` under "Two roles": a migrator-role `*DB`
  and a runtime-role `*DB` over one database are two instances; their transactions do not join and
  can block each other (inner waits on outer, unbounded without `DB_LOCK_TIMEOUT`); never run the
  migrator inside a runtime transaction (already the pre-boot rule).
- `workshop/documentation/docs/guides/operations-runbooks.md:343-346` (origin/main line numbers):
  replace the "one framework hazard … until the fix lands" paragraph with the fixed statement, a
  pointer to the persistence guide, and the symptom line from upgrade row 6.
- `workshop/documentation/docs/sdk/capabilities.md:314` "Bundled connectors reject nesting" → "reject
  nesting on the same instance; a different instance's `Transact` nests independently and is neither
  joined nor refused".
- `workshop/documentation/docs/pockets/authorization.md:130-133`: "an ambient transaction" → "an
  ambient transaction of the store's own `*DB`", plus the same-database warning.
- `workshop/documentation/docs/integrations/catalog.md:24-28`: one sentence after the snapshot
  sentence: ambient transactions are per `*DB`.
- Module READMEs (PR-1): `integrations/datastores/pgxdb/README.md:1058-1066` (new text) and `:1085`,
  `integrations/datastores/turso/README.md:258-265` gain the identity rule, the per-instance nesting
  sentence and the same-database hazard; `pockets/authorization/stores/pgx/README.md:63,195` and
  `.../turso/README.md:62,86,181` say "ambient transactions of the store's own `*DB`" and warn about
  the same-database pair.

## The S4d part 2 pool-path check and switch review (section 8)

- `pockets/authentication/stores/{pgx,turso}/session_activity.go` — `GetByRefreshHashWithActivity`,
  `RotateAt`, `DeleteIfIdle`, `ProbeSchema` pass `s.db` (pool) straight to the helpers. They never
  read the context key, so the change cannot affect them. Still correct, and still the right choice:
  a refresh rotation handed to a client must commit regardless of a host workflow's outcome (a
  rolled-back business transaction must not un-rotate a token). The SRE9 RLS rationale weakens for
  battleboard (its `iam` schema is not under FORCE RLS), but the independence argument stands.
  **Keep.** Pre-existing and unchanged by this plan (DI): a pool-path `RotateAt`/`DeleteIfIdle` called
  inside a SAME-instance ambient transaction busy-waits on a turso file (RESERVED held) and can block
  on a row the ambient transaction locked on pgx — hosts should not call refresh inside their own
  `Transact` over the auth database.
- **Should any store switch `s.db` → `QuerierFrom`?** No. authentication, cms, events, jobs and
  secrets stores are `InTx`/pool-path by design (atomic port operations, not ambient participants).
  One observation, not a change: a host wanting `pockets/events`' outbox write atomic with its own
  business write would need the events store to join the ambient transaction — a separate design
  question for that pocket.
- **Should any store switch `QuerierFrom` → `s.db`?** No. The authorization reads through
  `QuerierFrom` (`Recorded`, `read_model`, `tuples.reader`, `audit.List`) must see the host's pending
  writes inside its transaction; that is the RunTransactional contract and it is now correctly
  instance-local.

## Risks

1. **Same-database two-instance nesting can self-deadlock** (BE3/DI1/SRE2): a pair of `*DB` over one
   database goes from an accidental atomic join (or a loud `ErrMutationInsideTransaction`) to a
   client-side lock wait that PostgreSQL's deadlock detector cannot see — unbounded without
   `DB_LOCK_TIMEOUT` — or a guaranteed `SQLITE_BUSY` after `busy_timeout` on a file. Mitigated by
   upgrade row 6, the identity-rule docs, the `lock_timeout`/busy pin tests, and the fact that neither
   adopting host has this shape (gps-three-sixty: two databases; battleboard: one pool).
2. **Lockstep compile break for hosts, transitive through MVS** (pgxdb v0.15.0 with authorization pgx
   v0.21.0). Mitigated by the set-A verbatim negative, row 2's hatch, the store-tag freeze with PR-2
   pre-staged, and the standing RELEASING note for future store trains.
3. **Semantics shift on the authorization "refuse ambient" sites** (foreign transaction no longer
   refuses; orphaned grant on business rollback; cache serves checks inside a business transaction).
   Intended under the ruling, but a contract change for `ErrMutationInsideTransaction`; R-1 asks the
   owner to confirm with row 8's wording, and the contract texts (BE5 list) record it.

## Tasks

Paths below are under the PR's worktree; `$W1` = `../gopernicus-querierfrom-1`,
`$W2` = `…-2`, `$W3` = `…-3`.

### task-0: create the PR-1 worktree from `origin/main`

- **depends_on:** []
- **model:** sonnet
- **files:** []
- **verify:** `cd <primary checkout> && git fetch origin && git worktree add -b datastores/querierfrom-instance-scope ../gopernicus-querierfrom-1 origin/main && cd ../gopernicus-querierfrom-1 && git log --oneline -1 && git status --short` (the log line is `25eadbd0` or later; the status is empty)
- **description:** The primary checkout is on an older branch (BE1); every PR-1 task edits and verifies inside `$W1`. Repeat the shape for `$W2`/`$W3` at task-8/task-9.

### task-1: pgxdb — per-instance key, `TxFrom`, instance-scoped `QuerierFrom`/nesting; remove `TxFromContext`

- **depends_on:** [task-0]
- **model:** opus
- **files:** [`$W1/integrations/datastores/pgxdb/transact.go`, `$W1/integrations/datastores/pgxdb/transact_test.go`, `$W1/integrations/datastores/pgxdb/transact_snapshot_test.go`, `$W1/integrations/datastores/pgxdb/snapshot_isolation_test.go`, `$W1/integrations/datastores/pgxdb/transact_instances_test.go` (new, hermetic), `$W1/integrations/datastores/pgxdb/README.md`]
- **verify:** `cd $W1/integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...` (hermetic; live tests skip loudly without the DSN)
- **description:** Replace `txCtxKey{}` with `txKey{db *DB}`; stash `txKey{d}` in `Transact` and `TransactSnapshot`; add `(d *DB) TxFrom(ctx)`; route `QuerierFrom` and both nested checks through `d.TxFrom`; delete `TxFromContext`; reword `ErrNestedTransact` as in "The nesting rule"; update the three existing tests mechanically (`snapshot_isolation_test.go:71` uses `txKey{db}` with the same `db`); add the hermetic two-instance routing test (layered keys; never `Transact` on a zero `&DB{}` without its own key present); add the README identity-rule, per-instance nesting and same-database-hazard text.

### task-2: pgxdb — live two-instance test (visibility, nesting, A-join inside B, no cross-database atomicity, lock pin)

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`$W1/integrations/datastores/pgxdb/transact_instances_live_test.go` (new)]
- **verify:** `cd $W1/integrations/datastores/pgxdb && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./... -run 'Transact|TxFrom|Snapshot' -count=1 -race` (a throwaway `postgres:17` container, or a scratch database if 5432 is taken; never 55432/8082)
- **description:** Add `liveInstance(t) (*DB, table string)` and `count(t, db, table)` helpers (do not reuse `transactLiveDB`'s single `t.Setenv` table, DI3); open two `*DB` on the same DSN with `lock_timeout=5s` in the DSN; bounded contexts ≥ 10 s; assert everything listed under "pgxdb, live" in Tests — including the A write inside B's callback rolling back with A (BE6), the `TransactSnapshot` outer variant, and the same-database `LOCK TABLE` lock-timeout pin with `lock_timeout=200ms` (DI1).

### task-3: turso — the same connector change plus hermetic two-instance and same-file busy tests

- **depends_on:** [task-0]
- **model:** opus
- **files:** [`$W1/integrations/datastores/turso/transact.go`, `$W1/integrations/datastores/turso/tx_lifecycle_test.go`, `$W1/integrations/datastores/turso/transact_integration_test.go`, `$W1/integrations/datastores/turso/transact_instances_test.go` (new, no build tag), `$W1/integrations/datastores/turso/README.md`, `$W1/Makefile` (`INTEGRATION_TAG_MODULES` += `integrations/datastores/turso`)]
- **verify:** `cd $W1/integrations/datastores/turso && go build ./... && go test ./... -count=1 && go vet ./... && go vet -tags=integration ./...` then `mkdir -p $SCRATCH/turso-leg && TURSO_DATABASE_URL=file:$SCRATCH/turso-leg/leg.sqlite TURSO_AUTH_TOKEN=local go test -tags=integration ./... -count=1`
- **description:** Mirror task-1 on turso (`txKey{db *DB}`, `TxFrom`, instance-scoped `QuerierFrom` and nested check, `TxFromContext` removed, `ErrNestedTransact` reworded); update the two tests; add the hermetic test over two `newMemDB` with the IDENTICAL probe table in both and contexts ≥ 10 s (BE11/DI6), plus the same-file busy pin (two `Open` on one temp-dir `file:` with `Config.BusyTimeout: 50 * time.Millisecond`; `b.Transact` inside `a.Transact` returns busy); README paragraph; the one-line Makefile addition.

### task-4: sdk — `Transactor` contract clause: instance-scoped nesting binds every implementation

- **depends_on:** [task-0]
- **model:** sonnet
- **files:** [`$W1/sdk/capabilities/transaction/transaction.go`]
- **verify:** `cd $W1/sdk && go build ./... && go vet ./... && cd $W1 && make guard`
- **description:** Amend `transaction.go:7-15` with the clause quoted in "The sdk seam" (BE8); no API change, no tag.

### task-5: authorization pgx store — eight sites to `TxFrom`, tests, foreign-instance live test, README

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`$W1/pockets/authorization/stores/pgx/mutations.go`, `$W1/pockets/authorization/stores/pgx/operations.go`, `$W1/pockets/authorization/stores/pgx/read_snapshot.go`, `$W1/pockets/authorization/stores/pgx/relationships.go`, `$W1/pockets/authorization/stores/pgx/tuple_cache.go`, `$W1/pockets/authorization/stores/pgx/tuples.go`, `$W1/pockets/authorization/stores/pgx/writes.go`, `$W1/pockets/authorization/stores/pgx/audit_live_test.go`, `$W1/pockets/authorization/stores/pgx/foreign_instance_live_test.go` (new), `$W1/pockets/authorization/stores/pgx/README.md`]
- **verify:** `cd $W1/pockets/authorization/stores/pgx && go build ./... && go vet ./... && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./... -count=1 && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test ./... -count=1` (the schema leg is the `WithSchema` identity claim, SRE6) then `cd $W1 && make guard`
- **description:** Move each of the eight `pgxdb.TxFromContext(ctx)` sites to the receiver's `*DB` (`m.db`, `s.db`, or the `db` parameter) with no other edits; move the test site; add the foreign-instance live test exactly as specified under "Authorization stores, foreign-instance test" — foreign = bare `pgxdb.Open` (DI5), one assertion per site, each confirmed red by running the new file against `origin/main`'s store code first; README `:63,195` say "of the store's own `*DB`" and warn about the same-database pair. Builds under `go.work` only until PR-2 repins.

### task-6: authorization turso store — eight sites to `TxFrom`, tests, hermetic foreign-instance test, README

- **depends_on:** [task-3]
- **model:** opus
- **files:** [`$W1/pockets/authorization/stores/turso/mutations.go`, `$W1/pockets/authorization/stores/turso/operations.go`, `$W1/pockets/authorization/stores/turso/read_snapshot.go`, `$W1/pockets/authorization/stores/turso/relationships.go`, `$W1/pockets/authorization/stores/turso/tuple_cache.go`, `$W1/pockets/authorization/stores/turso/tuples.go`, `$W1/pockets/authorization/stores/turso/writes.go`, `$W1/pockets/authorization/stores/turso/audit_live_test.go`, `$W1/pockets/authorization/stores/turso/integrity_test.go`, `$W1/pockets/authorization/stores/turso/foreign_instance_test.go` (new, hermetic `file:` fixtures), `$W1/pockets/authorization/stores/turso/README.md`]
- **verify:** `cd $W1/pockets/authorization/stores/turso && go build ./... && go vet ./... && go test ./... -count=1 && go vet -tags=integration ./...` then the local turso store leg: `TURSO_DATABASE_URL=file:$SCRATCH/turso-leg/store.sqlite TURSO_AUTH_TOKEN=local AUTHORIZATION_TURSO_DISPOSABLE_URL=file:$SCRATCH/turso-leg/store.sqlite go test -tags=integration ./... -count=1` (this leg does NOT run in CI, SRE1) then `cd $W1 && make guard`
- **description:** Mirror task-5 on turso; the foreign-instance test uses two migrated `canonicalFixture(t, false)` temp-dir files (DI7) so the foreign `BEGIN IMMEDIATE` cannot block the store's own pool, the same assertion set minus the isolation check, and asserts the foreign file has zero `iam_tuples` rows after the injected error; README `:62,86,181`.

### task-7: PR-1 close — contract texts, full gate, live legs, handoff for tags with the freeze

- **depends_on:** [task-2, task-4, task-5, task-6]
- **model:** opus
- **files:** [`$W1/ARCHITECTURE.md` (`:799`), `$W1/AUDIT.md` (`:3759,4761`), `$W1/pockets/authorization/README.md` (`:204,532,567,807`), `$W1/pockets/authorization/logic/mutations/repository.go` (`:18,32,50`), `$W1/pockets/authorization/logic/mutations/mutation_service.go` (`:134`), `$W1/pockets/authorization/logic/tuplecache/tuplecache.go` (`:63`), `$W1/pockets/authorization/logic/relationships/relationship.go` (`:157`), `$W1/pockets/authorization/stores/storetest/transactional.go` (`:737,808,843`)]
- **verify:** `cd $W1 && make guard && make check` then, with Postgres: `make test-stores` and `cd integrations/datastores/pgxdb && POSTGRES_TEST_DSN=… go test -race ./...`, plus the two local turso `-tags=integration` legs from task-3 and task-6 (SRE1/SRE6)
- **description:** Qualify every "rejects an ambient transaction" text with "of the store's own `*DB`" (comment/doc edits only; the core and `pockets` stay untagged); rebase on `origin/main`; run the full gate and the local live legs; open PR-1 whose description names the removal, the lockstep floor (row 2), the store-tag freeze and the R-6 libSQL-server decision; the owner merges, dispatches `live-stores` on `main`, cuts `integrations/datastores/pgxdb/v0.15.0` and `integrations/datastores/turso/v0.10.0` at the squash commit (annotated, unsigned; messages name the removal, the floor and "no `pockets/authorization/stores/*` tag until PR-2"), and cold-verifies set A including the verbatim negative. **No authorization store tag at this commit (BE9/SRE4).**

### task-8: PR-2 — repin the authorization stores to pgxdb v0.15.0 / turso v0.10.0 (staged before PR-1 merges)

- **depends_on:** [task-7]
- **model:** sonnet
- **files:** [`$W2/pockets/authorization/stores/pgx/go.mod`, `$W2/pockets/authorization/stores/pgx/go.sum`, `$W2/pockets/authorization/stores/turso/go.mod`, `$W2/pockets/authorization/stores/turso/go.sum`]
- **verify:** worktree `$W2` from `origin/main` after PR-1 merges (branch `authorization-stores/repin-pgxdb-0.15`; the edits can be prepared earlier and rebased); in each store module `GOWORK=off go get github.com/gopernicus/gopernicus/integrations/datastores/pgxdb@v0.15.0` (turso: `…/turso@v0.10.0`) `&& GOWORK=off go mod tidy && GOWORK=off go build ./... && GOWORK=off go test ./... && GOWORK=off go vet -tags=integration ./...` (SRE14); then `cd $W2 && make guard && make check` and the two local turso legs
- **description:** Pins only (authorization core v0.30.0 and sdk v0.18.0 stay); open PR-2 the same owner session PR-1 merges; after merge and a green `live-stores` dispatch the owner cuts `pockets/authorization/stores/pgx/v0.22.0` and `pockets/authorization/stores/turso/v0.21.0`, cold-verifies set B and the gps-three-sixty shape, and the RELEASING table records the PR-1 and PR-2 merge timestamps (the freeze window).

### task-9: PR-3 — examples and scaffold repins

- **depends_on:** [task-8]
- **model:** sonnet
- **files:** [`$W3/examples/auth-cms/go.mod`, `$W3/examples/auth-cms/go.sum`, `$W3/examples/production-baseline/go.mod`, `$W3/examples/production-baseline/go.sum`, `$W3/examples/cms/go.mod`, `$W3/examples/cms/go.sum`, `$W3/workshop/gopernicus/internal/commands/init.go` (`:100,105`), `$W3/workshop/gopernicus/internal/commands/templates/init/readme.md.tmpl` (`:153` → `@v0.9.2`), `$W3/workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/go.mod.tmpl`, `$W3/workshop/gopernicus/internal/commands/templates/pocket/stores/turso/go.mod.tmpl`, `$W3/workshop/gopernicus/internal/commands/scaffold_test.go` (`:51-52`)]
- **verify:** worktree `$W3` from `origin/main` after both store tags have polled on the proxy; per example `GOWORK=off go get` the new pins `&& GOWORK=off go mod tidy && GOWORK=off go build ./... && GOWORK=off go mod verify`; `cd $W3/workshop/gopernicus && go test ./...`; `cd $W3 && make guard && make check`
- **description:** auth-cms → pgxdb v0.15.0, turso v0.10.0, authorization stores pgx v0.22.0; production-baseline → pgxdb v0.15.0, authorization stores pgx v0.22.0; cms → turso v0.10.0; scaffold: `init.go` connector pins (these set the cold `init` output, SRE5), the two pocket templates, the emitted README's guard version, and the pin test.

### task-10: PR-3 — docs site: "two databases never share a transaction"

- **depends_on:** [task-8]
- **model:** opus
- **files:** [`$W3/workshop/documentation/docs/guides/persistence.md`, `$W3/workshop/documentation/docs/guides/database-posture.md`, `$W3/workshop/documentation/docs/guides/operations-runbooks.md` (`:343-346`), `$W3/workshop/documentation/docs/sdk/capabilities.md` (`:314`), `$W3/workshop/documentation/docs/pockets/authorization.md` (`:130-133`), `$W3/workshop/documentation/docs/integrations/catalog.md` (`:24-28`)]
- **verify:** `cd $W3 && make docs-build`
- **description:** Write the six edits listed in "Docs"; the persistence guide carries the canonical statement (identity rule, same-database hazard with `DB_LOCK_TIMEOUT`, nesting order and pool sizing) and the others point to it; the runbook gains the row-6 symptom line.

### task-11: PR-3 — RELEASING record, upgrade notes by host shape, standing notes, NEXT.md closure

- **depends_on:** [task-9, task-10]
- **model:** opus
- **files:** [`$W3/RELEASING.md`, `$W3/.claude/plans/soc2-readiness/NEXT.md`]
- **verify:** `cd $W3 && make guard && make check`
- **description:** Add the top-of-file section in the S4d part 2 record's shape: tag table with commits/PRs/cold-verification and the two merge timestamps, the three MVS sets including the set-A verbatim negative and fix command, "Not tagged" (with the goredis test-require note and the authorization-floor standing note for future store trains), the R-6 run/waiver line, and the eleven-row upgrade table from section 6 verbatim; close the R-1 deferral block in NEXT.md with the tag list and move the ACTIVE pointer.

### task-12: PR-3 close — workshop tag and cold `init` (R-4)

- **depends_on:** [task-11]
- **model:** sonnet
- **files:** []
- **verify:** after the owner cuts `workshop/gopernicus/v0.9.2` ("scaffold pins only; no rule-set change"): a cold `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.9.2 init --db pgx` (and `--db turso`) from an empty module cache pins pgxdb v0.15.0 / turso v0.10.0, its README cites `@v0.9.2`, it tidies, builds, `go mod verify` passes, `guard --posture` is clean; set C recorded
- **description:** Owner-executed; record the result in the RELEASING table.

## Sequencing

task-0 → task-1 ∥ task-3 ∥ task-4 → task-2 (after 1) ∥ task-5 (after 1) ∥ task-6 (after 3) →
task-7 (PR-1 gate; connector tags; freeze begins) → task-8 (PR-2, pre-staged; store tags; freeze
ends) → task-9 ∥ task-10 → task-11 → task-12. Each PR is its own worktree from `origin/main`; PR-2
opens in the same owner session PR-1 merges; PR-3 only after both store tags have polled (its
`make check` warms the scaffold cache through the proxy).

## Decided by gate

Three reviewers, rev 0 → rev 1. Every finding below is folded into the body; this is the index.

- **BE1 (HIGH) worktree paths** → every task now uses `$W1/$W2/$W3` under `../gopernicus-querierfrom-<n>`; task-0 creates PR-1's worktree from `origin/main` and checks the base.
- **BE2 / DI2 (MED) site count** → sixteen sites (eight pgx, eight turso) everywhere; the S4d2 block counted seven pgx FILES.
- **BE3 / DI1 / SRE2 (HIGH) two `*DB` over ONE database** → the identity rule names the self-deadlock (pgx: client-side wait the server cannot see, unbounded without `DB_LOCK_TIMEOUT`; turso file: `SQLITE_BUSY` after `busy_timeout`); Risk 1; upgrade row 6 with the `DB_LOCK_TIMEOUT` hatch and the symptom; the Read-first grep counts `Open` calls; two pin tests (pgx `LOCK TABLE` under `lock_timeout=200ms`; turso same-file `BusyTimeout: 50ms`); folded into R-1 as the trade (refusal → possible wait). gps-three-sixty is two databases (`.env.example:56` refuses an equal host/port/database), so the reference host is not this shape; battleboard is one pool.
- **BE4 / SRE8 (MED) R-1 (a) flips gps-three-sixty's reliance** → row 8 names the orphaned grant on business rollback (write grants after commit, or `OperationID` + `Fence`) and the cache now serving checks inside a business transaction.
- **BE5 (MED) contract texts** → the full list (ARCHITECTURE/AUDIT, core port comments, tuplecache, storetest family comments, core and store READMEs) is in §3 and task-7; citations fixed (runbooks `:343-346`, capabilities `:314`, pgxdb README adds new text).
- **BE6 (MED) rejection reason for owner-on-Tx** → the single-slot shadowing argument replaces "no consumer"; task-2 asserts an A write inside B's callback rolls back with A.
- **BE7 (LOW) R-2 (b) is not buildable** → recorded under "Accessors" and R-2.
- **BE8 (LOW-MED) sdk clause** → a clause binding every implementation, not a note about bundled connectors; still comment-only, untagged.
- **BE9 / SRE4 (MED) the freeze** → "no `pockets/authorization/stores/*` tag until PR-2" in PR-1's description and both tag messages; PR-2 pre-staged; timestamps recorded.
- **BE10 (LOW) pointer identity** → wrappers embed the pointer, never copy by value; docs only.
- **BE11 / DI5 / DI6 / DI7 (MED) test fixtures** → identical table in both turso memory DBs; pgx foreign instance = bare `Open`; turso store foreign = migrated `canonicalFixture` file; zero-rows assertion on the foreign file.
- **DI3 (MED) pgx live helper** → `liveInstance(t) (*DB, table)` + `count`; `transactLiveDB`'s single `t.Setenv` table cannot hold two.
- **DI4 (MED) assertions red on old code** → one per site (snapshot-pinned read, `ReadTupleSnapshot` under READ COMMITTED, `CacheableContext`, `WithTupleCache` construction, `Apply`/`Fence`/`Expire`, write survives injected error), each confirmed red against `origin/main` first.
- **DI8 / SRE7 (MED) two connections per nested request** → identity rule bullet, row 5, persistence guide: fixed nesting order, request deadlines, inner-pool sizing; runbook symptom.
- **DI9 (LOW)** → runbooks citation fixed; the `session_activity.go` comment is a recorded follow-up.
- **SRE1 (HIGH) turso legs do not run in CI** → explicit local legs (connector + store, local-file shape with `AUTHORIZATION_TURSO_DISPOSABLE_URL`) in tasks 3, 6, 7, 8; `integrations/datastores/turso` added to `INTEGRATION_TAG_MODULES`; libSQL-server leg run or waived = R-6.
- **SRE3 / SRE10 (MED/LOW) transitive lockstep; goredis test requires** → row 2 wording and the "hold v0.14.0 / v0.9.0" hatch; standing RELEASING note for future store trains and the goredis repin rule.
- **SRE5 (MED) scaffold pins live in `init.go`** → task-9 files include `init.go:100,105`, `readme.md.tmpl:153`; PR-3's `make check` needs both connector tags polled first.
- **SRE6 (MED)** → task-5 runs the `POSTGRES_TEST_SCHEMA` leg; task-7 runs `make test-stores` + pgxdb `-race`.
- **SRE9 / SRE11 (LOW) sets** → set A negative via `go build ./...`, verbatim line + fix command; set C builds `./...` per example with `go mod verify` and names the excluded examples.
- **SRE12 (LOW) observability** → no runtime signal (routing to the own pool is correct); row 9 for the `ErrNestedTransact` text; alert on `errors.Is`.
- **SRE13 / SRE14 (LOW)** → scratch-database guidance in Release; `GOWORK=off go vet -tags=integration` in PR-2's gate.
- **DI parity / S4d2** → memory store needs nothing; §8 gains the pre-existing same-instance pool-path sentence; "keep" stands.

## Open rulings — YOUR CALL

- **R-1 — `ErrMutationInsideTransaction` on a foreign instance's transaction.** (a) Refuse only the
  store's own instance's ambient transaction (drafted: `m.db.TxFrom`). (b) Keep refusing when ANY
  instance's transaction is ambient — requires reintroducing an instance-blind "any transaction"
  lookup in the connectors and makes authorization unusable inside any business transaction of a
  two-DB host. **Recommend (a)** with row 8's wording as the price: it is the only reading consistent
  with "each store routes to its own instance"; the reference host needs it; and the trade (BE3/DI1:
  a pair over ONE database loses an accidental fail-fast and can wait on itself) is bounded by
  `DB_LOCK_TIMEOUT`, documented in row 6, and not the shape of either adopting host.
- **R-2 — Remove `TxFromContext` outright vs. keep it deprecated for one tag.** (a) Remove
  (drafted). (b) Keep as `Deprecated:`. **Recommend (a):** (b) cannot be built over `txKey{db}`
  without a second instance-blind key written on every stash (BE7) — it would put the bug back inside
  the connector; the owner asked for the correct API; the lockstep is one pin move.
- **R-3 — A storetest foreign-instance family now vs. later.** (a) Per-store tests (drafted; no
  authorization core tag). (b) `storetest.RunForeignInstance(t, newRepos, foreign transaction.Transactor)`
  in `pockets/authorization/stores/storetest` (memory runs it with a stub transactor) — pins every
  store but adds an authorization core minor and a core repin to the train. **Recommend (a) now, (b)
  when the core next tags.**
- **R-4 — Tag `workshop/gopernicus/v0.9.2` for the scaffold pins.** (a) Tag (drafted; the v0.9.1
  precedent; message "scaffold pins only; no rule-set change"). (b) Move the pins untagged. **Recommend
  (a):** cheap, and a cold `init` should land on the fixed connector.
- **R-5 — PR shape.** (a) Three PRs as drafted. (b) Four, splitting the store call-site moves from
  the connectors — impossible without a red `make check` on `main` unless R-2 (b). **Recommend (a).**
- **R-6 — The libSQL-server turso leg.** (a) Run it locally before each turso tag
  (`ghcr.io/tursodatabase/libsql-server` on `127.0.0.1:8081`, `TURSO_AUTH_TOKEN=local`). (b) WAIVE as in
  S4d parts 1 and 2, with the local-file legs as the proof. **Recommend (b)** for consistency with the
  last two trains — the change is Go-side routing, and the local-file driver exercises the same
  `BEGIN IMMEDIATE`/pinned-connection paths; record the waiver line in RELEASING.

## Open questions

- Battleboard: does any `iam`-schema table sit under a tenant RLS policy? (Carried from S4d part 2;
  the fix does not change the answer — `Scope.Querier` still hands tenant-owned stores the scope's
  own transaction.)
- gps-three-sixty: is a business `db.Transact` around authorization calls planned for M0/M1? If so,
  row 8 is its contract — grant after commit, or compensate.
- Does any adopting host run Dependabot/Renovate or `go get -u` that could land pgxdb v0.15.0 inside
  the PR-1 → PR-2 freeze window? (SRE4; the pre-staged PR-2 keeps the window to one session.)

## Recommended reviews

**lead-backend-engineer** (gate, done; re-read rev 1's R-1 text and the sdk clause).
**data-integration-reviewer** (gate, done; re-read the per-site assertion list and the two pin
tests). **platform-sre** (gate, done; re-read the freeze, the local turso legs and R-6).
**product-manager** (post-hoc): the eleven-row host-shape table as the adopter contract; R-3/R-4
scope. **architecture-steward** (post-hoc): the comment-only sdk clause, the identity rule's
placement in the persistence guide, and the Makefile `INTEGRATION_TAG_MODULES` addition.

## Notes

- Inventory delta vs. S4d part 2's R-1 block: the count (sixteen sites, not fifteen — `operations.go`
  holds two per dialect); otherwise identical, plus the `QuerierFrom` callers in auth-cms (API
  unchanged) and the hosts' sites named above.
- `examples/README.md` H3 ("no transaction handle" in compositions) is unaffected: the accessor is a
  store-side concern; compositions still never see a `*Tx`.
- The `operations-runbooks.md:343` hazard text is the only place the docs site currently admits the
  defect; it is replaced, not appended to.
- Follow-ups recorded, not planned: the `session_activity.go` comment citation (DI9); the
  `RunForeignInstance` storetest family (R-3 (b)); the events-outbox ambient-join question (§8).

## As built (2026-10-27)

- **The sixteen sites, as built.** Every one calls the receiver's own `*DB`:
  - pgx: `mutations.go:81` (`m.db`), `operations.go:68,126` (`m.db`), `read_snapshot.go:19`
    (`s.db`), `relationships.go:618` (`s.db`), `tuple_cache.go:33` (`db`, the constructor's),
    `tuples.go:384` (`s.db`), `writes.go:97` (`db`, always `s.db`);
  - turso: `mutations.go:41`, `operations.go:67,117`, `read_snapshot.go:19`,
    `relationships.go:614`, `tuple_cache.go:40`, `tuples.go:376`, `writes.go:94`, with the same
    receivers.
  The released v0.21.0 / v0.20.0 stores fail at exactly these eight lines per dialect against
  the new connectors (set A negative, `RELEASING.md`).
- **The sdk clause** (`sdk/capabilities/transaction/transaction.go`), as shipped after the owner's
  rewording: "Bundled connectors reject a nested Transact on the same instance with a connector
  error. fn's context may carry transactions of other datastore instances; an implementation
  neither joins nor refuses another instance's transaction; it may reject nesting on its own
  instance, never because another instance holds one — two datastores never share a
  transaction." The plan's "rejects nesting only on its own instance" became "may reject": an
  implementation is not required to refuse same-instance nesting, only forbidden to refuse because
  of another instance. Comment-only; no sdk tag.
- **AUDIT-056** ("Ambient transactions are per datastore instance; `TxFromContext` removed") was
  added in #233 to record the migration; the plan had only qualified the existing AUDIT entries.
  PR-3 set its Status to released with the four versions.
- **The reviewers' test additions** (folded into #233): a pgx `ReadTupleSnapshot` routing subtest
  beside the isolation one; a turso foreign-commit write subtest (`writes, foreign commits`); an
  own-instance-nests-around-a-foreign-one subtest in both stores; isolation checks in the pgxdb
  live two-instance test over three nesting shapes (plain in plain, plain in snapshot, snapshot in
  plain). The result is 13 foreign-instance subtests per dialect, every one red on `25eadbd0` and
  green after the change (reproduced independently by the lead-backend reviewer).
- **The review folds.** The MEDs and LOWs were: a qualifier on an AUDIT historical entry,
  reverted (history stays as written); docs wording; the turso `Expire` comment; the test
  additions above; reflows.
- **The freeze** held: 20 minutes between the two merges, no store tag inside it.
- **PR-3** repinned auth-cms, production-baseline and cms exactly as task-9 lists (no other example
  pins a moved module); the scaffold pins no authorization store, so `init.go`, the two pocket
  `go.mod.tmpl`s, `readme.md.tmpl` and `scaffold_test.go` were the whole scaffold change.
- **Follow-ups** (carried in `NEXT.md`): pgxdb `live_test.go:68` counts every `default`-source
  ledger row (pre-existing; narrow to `version = '0001_init.sql'`); the DI9 comments in
  `pockets/authentication/stores/{pgx,turso}/session_activity.go` still name
  `pgxdb.QuerierFrom`/`tursodb.QuerierFrom` as package functions; R-3 (b), the storetest
  `RunForeignInstance` family, when the authorization core next tags; pgx
  `Rotate`/`RotateAt`/`ConsumeGrace` `session_profile` filter parity (S4d2 LOW-2, owner rec yes).
- **Implementer deviations.** The six deviations both reviewers upheld (extracted-tree red-before-green, `Config.LockTimeout`, the nested `LOCK TABLE` pin, turso `ReadTupleSnapshot` as routing, the extra `Expire` comment, fixture choices) are listed in `RELEASING.md` under "The review record".
