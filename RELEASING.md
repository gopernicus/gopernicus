# Releasing gopernicus modules

## Transaction-local settings for row-level security (pgxdb v0.16.0)

One annotated tag (not signed) on one module: `integrations/datastores/pgxdb/v0.16.0`,
**owner-pending**. The owner merges this PR, dispatches `live-stores` on `main`, then cuts and
pushes the tag at the squash-merge commit, polls the proxy and cold-verifies from a scratch
consumer. No other module is tagged, no repins are required, and there is no migration. Design
and rulings: `plans/pgxdb-local-settings.md` (ratified 2026-10-27). Under the owner's 2026-10-23
process ruling this section is the whole record: there is no handoff file and no manifest. The
operator surface is the docs site's new *Row-level security with pgxdb* guide, linked from
*Persistence* and *Database posture*; the reference is the pgxdb README's "Transaction-local
settings for row-level security" section.

**What was added.** Generic primitives, and no tenant concept: no tenancy pocket, no policy
generation, nothing tenant-named in sdk or pgxdb.
- `pgxdb.Setting{Name, Value}`: one transaction-local configuration parameter.
- `(*DB).TransactWithSettings(ctx, settings, fn)`: `Transact` whose first statement is one
  `SELECT set_config($1, $2, true), …` with every name and value bound. It is implemented by
  wrapping `d.Transact`, so nesting (`ErrNestedTransact` on the same instance), the
  per-instance ambient context, cleanup, panic handling and `MapError` are inherited. A failure
  of the first statement is wrapped as `pgxdb: applying transaction settings: %w`, keeping the
  mapped error. Validation runs before `Begin` (no pool acquisition) and refuses with
  `sdk.ErrInvalidInput`: no settings; a name outside `^[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*$`; a
  duplicate name; an empty value, a NUL byte or invalid UTF-8.
- `(*Tx).Setting(name) (string, bool)`: what `TransactWithSettings` applied to this transaction,
  from memory. It is `("", false)` on a `Transact` or `TransactSnapshot` transaction.

The sdk `Transactor`, turso, and every store and pocket are unchanged.

**Upgrade note.** Additive; nothing to do unless you adopt it. A host adopting it (battleboard,
then Segovia) replaces its own `set_config` first statement with `TransactWithSettings` and its
own context marker with `d.TxFrom(ctx)` + `tx.Setting(name)`; the guide shows battleboard's
`Scope` against v0.16.0. That port happens in the host's repo.

**The review record.** The plan passed a two-reviewer gate before ratification; both returned
ratify-with-edits, and every finding was folded ("Decided by gate").
- **lead-backend-engineer** (HIGH-1, MED-1..4, LOW-1..4) and **data-integration-reviewer**
  (HIGH-1..2, MED-1..5, LOW-1..4).
- **HIGH-1 / DI HIGH-2:** a drafted `RequireTx` proved "in a transaction", not "scoped". It and
  `ErrNoTransaction` were dropped, with R-3 (turso parity); `(*Tx).Setting` replaced them (the
  data-integration reviewer's proposal; the lead-backend reviewer preferred no primitive, owner
  call R-5).
- **DI HIGH-1:** the RLS test enters its role with `SET LOCAL ROLE` only, with a post-commit
  `current_user` check.
- **MED-1 (both):** the name rule is a guardrail; extension parameters are not blocked and
  `plpgsql.x` is pinned as accepted; no denylist. **DI MED-2:** the case-insensitivity note.
  **DI MED-3:** NUL and invalid UTF-8 refused before `Begin`. **LB MED-2:** implemented by
  wrapping `d.Transact`. **LB MED-3:** the version split (v0.16.0 here, v0.17.0 for
  default-ids). **LB MED-4 / DI MED-5:** the RLS test's unique role, grants, superuser gate,
  cleanup order and FORCE owner leg; one test. **DI MED-4:** the guide's composite keys and
  uniques, `NOSUPERUSER`, `security_invoker`, `SECURITY DEFINER`, 0-row vs 42501, the
  not-an-injection-boundary line, the `''` reason for `NULLIF`, "do not ENABLE RLS on pocket
  tables" and PgBouncer.
- **DI LOW-1:** the "bound" wording, and simple protocol tested. **DI LOW-2:** the
  `pg_backend_pid` check and the deadlock comment. **LB LOW-1:** the `Example` uses
  `app.actor_id`. **LB LOW-2 / DI LOW-4:** battleboard's shape against v0.16.0. **LB LOW-3:**
  the statement shape is one multi-column `SELECT set_config(…)`. **LB LOW-4:** the guide's note
  on translating a missing tenant before the call.

**Owner rulings.** Ratified 2026-10-27 ("all recs"): **R-1 (a)** no snapshot variant;
**R-2 (a)** refuse empty values, NUL and invalid UTF-8; **R-4 (a)** lowercase dotted custom
names, with the guardrail wording; **R-5 (a)** `(*Tx).Setting`. Plus: the guide gains the
cross-tenant shapes and the `ALTER ROLE … SET` warning. During the build the coordinator added
two guide facts: superusers and `BYPASSRLS` roles bypass RLS even under FORCE (FORCE binds only
the owner), and `SET LOCAL` outside a transaction block only warns and has no lasting effect,
with why the helper binds through `set_config` rather than `SET LOCAL`.

**Verification record.**
- pgxdb: `go build ./...`, `go vet ./...`, `go test -race ./...` pass; gofmt and goimports clean.
- **Live, Homebrew PostgreSQL 17.4**, a fresh `initdb` (trust auth, superuser `postgres`), each
  run on a FRESH database, `go test -race -count=1`: bare **544/0/0** (176 top-level tests),
  and with `POSTGRES_TEST_SCHEMA=gopernicus_schema_test` **544/0/0**. The RLS test RAN (did not
  skip) in both, all five cases passing; its schemas and roles were gone afterwards.
- New tests: `TestTransactWithSettings_Refusals` (16 refusals, zero queries: a pool-less `*DB`
  would panic on `Begin`), `TestValidateSettings_AcceptedNames`, `TestTxSetting_Absent`,
  `TestSetConfigStatement`, `TestLive_TransactWithSettings_DoesNotLeak` (MaxConns 1,
  `pg_backend_pid` equal inside and after; commit, error rollback, recovered panic; nesting;
  `Transact` records nothing), `TestLive_TransactWithSettings_BoundValues` (default and simple
  protocol), `TestLive_TransactWithSettings_RowLevelSecurity`, and
  `ExampleDB_TransactWithSettings` (compiled, not run).
- `make guard` and `make check` pass. `make docs-build` succeeds.
- **Pre-tag consumer build:** a `GOWORK=off` scratch consumer calling `TransactWithSettings` and
  `Tx.Setting`, beside authorization pgx v0.22.0, authentication pgx v0.16.0 and jobs pgx v0.7.0,
  with pgxdb replaced onto this tree, tidies, builds and vets (sdk v0.20.0 selected).
  **Owner-pending:** the same set from the v0.16.0 tag, from an empty module cache over the
  proxy.

**Sequencing.** This record owns pgxdb **v0.16.0**. The database-default-ids plan takes
**v0.17.0** for its PG18 floor, and its PR-1 branches only after the v0.16.0 tag exists.

**Known and deferred.**
- **pgxdb `live_test.go:68`** (`TestLive_OpenAndMigrate`) still counts every `default`-source
  ledger row; run the pgxdb live suite on a fresh database.
- Battleboard's and Segovia's ports are in their own repos.

## Ambient transactions per datastore instance (published 2026-10-27)

Four annotated tags (not signed) on four modules, plus `workshop/gopernicus/v0.9.2` for the
scaffold pins (R-4; cut by the owner after this PR merges). Each tag was cut at its PR's
squash-merge commit on `main` after a green `live-stores` dispatch there, pushed, THEN polled on
the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an empty module cache,
`proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build ./...`, a consumer pinning
`go 1.26.10`). No sdk tag (the `transaction.Transactor` change is comment-only), no migration.
Design and rulings: `plans/querierfrom-instance-scope.md` (ratified 2026-10-27; executed copy in
`plans/`). The audit entry is **AUDIT-056**. Under the owner's 2026-10-23 process ruling this
section is the whole record: there is no handoff file and no manifest. The operator surface is
the docs site: *Persistence* ("Two databases never share a transaction"), *Database posture*
("Two roles"), *Operations runbooks* ("Tenants and offboarding"), the sdk *Capabilities* page,
the authorization pocket page and the integrations catalog. The reference is the pgxdb and turso
READMEs' "Ambient transactions are per instance" paragraphs and the two authorization store
READMEs.

**What changed.** Before, both SQL connectors stored the ambient transaction under one context
key that every `*DB` shared. In a process with two databases, a store over one database called
inside the other's `Transact` ran its SQL on the wrong database's transaction, and a `Transact`
on one inside the other failed with a false `ErrNestedTransact`. Now the context key carries the
owning `*DB`. `(*DB).TxFrom(ctx)` and `(*DB).QuerierFrom(ctx)` answer only for their own
instance, and `Transact`/`TransactSnapshot` refuse nesting only on the same instance. The
package-level `TxFromContext` is removed from both connectors. The sixteen authorization store
sites (eight pgx, eight turso) call `<own db>.TxFrom`. **Two databases never share a
transaction**: the framework provides no cross-database atomicity, and ordering or an outbox is
the host's job.

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `integrations/datastores/pgxdb/v0.15.0` | `d5c9f213` | #233 (PR-1) | OK; set A, including the negative |
| 1 | `integrations/datastores/turso/v0.10.0` | `d5c9f213` | #233 (PR-1) | OK; set A, including the negative |
| 2 | `pockets/authorization/stores/pgx/v0.22.0` | `18bb3f57` | #234 (PR-2) | OK; set B, gps-three-sixty shape |
| 2 | `pockets/authorization/stores/turso/v0.21.0` | `18bb3f57` | #234 (PR-2) | OK; set B |
| 3 | `workshop/gopernicus/v0.9.2` | this PR's squash commit | this PR (PR-3) | owner: a cold `init --db pgx` / `--db turso` from `@v0.9.2` (task-12) |

Order: #233 merged at 2026-10-27T21:05:13Z (`d5c9f213`); `live-stores` went green on `main` (run
40583774102), and both connector tags were cut there. Their messages name the removal, the
lockstep floor and the store-tag freeze. PR-2 repinned both authorization stores (pgxdb v0.14.0 →
v0.15.0, turso v0.9.0 → v0.10.0, by explicit `GOWORK=off go get`, then tidy; the authorization
core and sdk pins stay). #234 merged at 2026-10-27T21:25:20Z (`18bb3f57`); `live-stores` went
green on `main` (run 40586686568), and both store tags were cut there. Every module ships MINOR
under the v0 convention; the workshop tag is a PATCH ("scaffold pins only; no rule-set change").

**The store-tag freeze — lifted.** From #233's merge until #234's merge, `main`'s authorization
store `go.mod`s pinned pgxdb v0.14.0 / turso v0.9.0 while their code called `TxFrom`, so a
`GOWORK=off` build of those modules was red and a store tag cut in that window would have been
permanently broken. No store tag was cut inside it. The window was 20 minutes (21:05:13Z →
21:25:20Z), and it closed when #234 merged; the store tags v0.22.0 / v0.21.0 are the first after
it.

**The MVS sets.** Each tag was cold-verified when it was cut; every set selected sdk v0.20.0.
- **Set A** (step 1, positive): pgxdb v0.15.0 + authentication pgx v0.16.0 + cms pgx v0.5.0 +
  events pgx v0.4.0 + jobs pgx v0.7.0 + secrets pgx v0.2.1 built. The turso twin, turso v0.10.0 +
  authentication turso v0.15.0 + cms turso v0.4.0 + events turso v0.3.0 + jobs turso v0.6.0,
  built. None of these stores references the removed symbol; MVS lifts their connector pin.
- **Set A, negative (recorded on purpose, verbatim).** A consumer requiring pgxdb v0.15.0 +
  authorization pgx v0.21.0 tidies and then fails `go build ./...`:
  `…/stores/pgx@v0.21.0/mutations.go:81:20: undefined: pgxdb.TxFromContext`, at all eight sites
  (`mutations.go:81`, `operations.go:68,126`, `read_snapshot.go:19`, `relationships.go:618`,
  `tuple_cache.go:33`, `tuples.go:384`, `writes.go:97`). turso v0.10.0 + authorization turso
  v0.20.0 likewise: `…/stores/turso@v0.20.0/mutations.go:41:22: undefined: tursodb.TxFromContext`,
  at all eight sites. The fix: `go get
  github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx@v0.22.0` (turso: `…/stores/turso@v0.21.0`).
- **Set B** (step 2): authorization v0.31.0 + stores pgx v0.22.0 / turso v0.21.0 + pgxdb v0.15.0 /
  turso v0.10.0 built.
- **gps-three-sixty shape** (step 2): authentication v0.32.0 + authentication pgx v0.16.0 +
  authorization v0.31.0 + authorization pgx v0.22.0 + jobs pgx v0.7.0 + pgxdb v0.15.0 +
  authorization goredis v0.6.0 built; `go mod verify` passed. goredis v0.6.0 transitively requires
  authorization stores/turso v0.17.0 and datastores/turso v0.9.0 (its test-only requires). That is
  harmless while the host imports no turso package. A host that imports the turso store must
  require v0.21.0.
- **Set C** (this PR, from the tags): auth-cms, production-baseline and cms at `GOWORK=off` from
  an EMPTY module cache over `proxy.golang.org` + `sum.golang.org`: `all modules verified` and
  `go build ./...` in each (this covers `production-baseline/cmd/bootstrap`, both
  `workshop/migrations` runners and auth-cms's jobs-store graph). Selected: auth-cms pgxdb v0.15.0,
  datastores/turso v0.10.0, authorization pgx v0.22.0; the baseline pgxdb v0.15.0, authorization
  pgx v0.22.0; cms datastores/turso v0.10.0; sdk v0.20.0 in all three. external-identity,
  goth-showcase, jobs-minimal and minimal are excluded: no SQL connector is in their module graph.

**Not tagged.** sdk (the `Transactor` clause is comment-only; it ships with the next sdk tag, the
v0.2.0 precedent); the authorization core and `pockets` (comment-only qualifiers, the S6 #185
precedent); authentication, cms, events, jobs and secrets stores (none references
`TxFromContext`; MVS lifts their connector pin, set A); `pockets/authorization/stores/goredis`
(untouched: its `go.mod` requires authorization stores pgx v0.18.0 / turso v0.17.0 for TESTS
only); views, ui/goth and the examples. Nothing retracts.

**Standing notes for future store trains.**
- **The authorization floor.** pgxdb ≥ v0.15.0 ⇒ authorization stores pgx ≥ v0.22.0;
  datastores/turso ≥ v0.10.0 ⇒ authorization stores turso ≥ v0.21.0. A future repin train of ANY
  sibling store that lifts a connector drags this floor in through MVS; name it in that train's
  notes.
- **goredis.** A future goredis-store repin of the connectors must move its test-only
  authorization store requires (pgx v0.18.0 / turso v0.17.0 today) to ≥ v0.22.0 / v0.21.0 in the
  same commit, or its `GOWORK=off -tags=integration` build breaks.

**Upgrade notes.** Eleven rows. One break (row 1, `TxFromContext` removed) and one lockstep (row
2); everything else is a routing change that only a host with two `Open` calls can see. The "Read
first" box and the rows are the plan's, verbatim.

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

**S4d part 2 interaction.** `RotateAt`/`DeleteIfIdle` stay on the pool path; the fix does not
change them.

**Handoff notes — what a host does on upgrade, by host shape.** Repin with explicit `GOWORK=off
go get <module>@<tag>`, then `GOWORK=off go mod tidy`; move the connector and the authorization
store in the same commit (row 2).
- **Every host** (rows 1, 2, 9). Run the Read-first grep. Replace any `TxFromContext` call with
  `<the *DB>.TxFrom(ctx)`. Re-key log alerts on `errors.Is(err, …ErrNestedTransact)`.
- **Single-DB hosts** (one `Open` per process, row 3): no change beyond the pins.
- **Two `Open`s over two databases** (rows 4, 5): stores now run on their own database. Nothing
  is atomic across the two: order the write you cannot take back last, or use an outbox. Nest
  instances in one fixed order and size the inner pool.
- **Two `Open`s over ONE database** (row 6, read it first): hold one `*DB` for stores you want in
  one transaction. Otherwise set `DB_LOCK_TIMEOUT` (and `DB_STATEMENT_TIMEOUT`) and never nest
  the pair.
- **battleboard** (rows 3, 7): one line in `Scope.Querier`, `pgxdb.TxFromContext(ctx)` →
  `s.db.TxFrom(ctx)`, plus the `Run` comment and the row-2 pins (authorization pgx v0.21.0 →
  v0.22.0 with pgxdb v0.15.0).
- **gps-three-sixty** (row 8): pins only (pgxdb v0.15.0 + authorization pgx v0.22.0; the shape
  built in step 2). Under R-1 (a), a grant applied inside a business `db.Transact` that rolls
  back STAYS applied: write grants after the business commit, or compensate with `OperationID` +
  `Fence`. Checks inside a business transaction may be served from the goredis tuple cache. Its
  goredis v0.6.0 drags in authorization stores/turso v0.17.0 + datastores/turso v0.9.0
  transitively; harmless while no turso package is imported, but a host importing the turso store
  must require v0.21.0.
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on these tags.
  Pins moved (explicit `GOWORK=off go get`, then `GOWORK=off go mod tidy`): `auth-cms` pgxdb
  v0.14.0→v0.15.0, datastores/turso v0.9.0→v0.10.0, authorization stores/pgx v0.21.0→v0.22.0;
  `production-baseline` pgxdb v0.14.0→v0.15.0, authorization stores/pgx v0.21.0→v0.22.0; `cms`
  datastores/turso v0.9.0→v0.10.0. `external-identity`, `goth-showcase`, `jobs-minimal` and
  `minimal` pin no moved module.
- **Scaffold (this PR, row 11).** `init` pins pgxdb v0.15.0 / datastores/turso v0.10.0 (sdk
  stays v0.20.0); `new pocket`'s store templates likewise; the emitted README runs the guard
  `@v0.9.2`. The scaffold pins no authorization store.

**The review record.** Every PR passed a code review before merge; the fixes were folded as
follow-up commits on the PR.
- **#233 (PR-1), lead-backend, ship-with-edits** (1 MED, 7 LOW) and **data-integration,
  ship-with-edits** (1 MED, 5 LOW). The lead-backend MED was an AUDIT historical-entry
  (AUDIT-034, Firestore) qualifier, which was reverted. The data-integration MED was the
  pre-existing fresh-database requirement of the pgxdb live suite, noted in the PR and under
  "Known and deferred". The LOWs were all folded in:
  - docs wording;
  - the turso `Expire` comment;
  - a turso commit-variant write test;
  - a pgx `ReadTupleSnapshot` routing test;
  - isolation checks over three nesting shapes;
  - reflows.

  Both reviewers upheld all six implementer deviations:
  1. Red-before-green ran the new store tests unchanged against an extracted `origin/main` tree.
     A per-site revert was impossible, because the old site code no longer compiles once
     `TxFromContext` is removed.
  2. The live tests set `Config.LockTimeout` instead of a DSN parameter.
  3. The pgx same-database `LOCK TABLE` pin runs inside `b.Transact` nested in `a.Transact`. A
     bare `LOCK TABLE` outside a transaction block fails with 25P01.
  4. turso's `ReadTupleSnapshot` site is asserted as routing, because turso has no isolation
     check.
  5. An extra `Expire` contract comment, "of m.db", in pgx `operations.go` (mirrored on turso
     during review).
  6. Fixture choices: the pgx foreign `Open` sets a 5s lock timeout, and the turso store test
     defines its own grant helper (`grantCmd` sits behind the integration build tag).
- **#234 (PR-2):** pins only; gated by `make guard && make check` and the `GOWORK=off` legs below.

**Verification record.**
- `make guard` and `make check` pass on each PR. `live-stores` green on `main` before each tag
  (runs 40583774102 at `d5c9f213` and 40586686568 at `18bb3f57`).
- **PR-1 (#233).** pgxdb live **510/0/0** on a fresh PostgreSQL 17 database; authorization pgx
  live **797/0/1**, bare and with `POSTGRES_TEST_SCHEMA` (the skip is the known non-C collation
  test); turso connector `-tags=integration` on the local-file driver **230/0/0**; turso
  authorization store `-tags=integration` **1020/0/0**. **Red before green:** the 13
  foreign-instance subtests per dialect fail on `main` `25eadbd0` and pass with the change; the
  lead-backend reviewer reproduced that independently. The lead's own reruns: pgxdb two-instance
  9/0, the foreign-instance tests 13/0 on pgx and 13/0 on turso.
- **PR-2 (#234), `GOWORK=off` against the connector tags.** authorization pgx live **797/0/1**,
  bare and schema-scoped; turso store `-tags=integration` **1020/0/0**.
- **Cold verification:** every tag above; sets A (with the negative), B, the gps-three-sixty
  shape and C as listed.
- **This PR (PR-3).** Every example builds, vets and tests at `GOWORK=off`; set C from an empty
  module cache. A cold `init --db pgx` and `init --db turso` from THIS tree (built binary, empty
  module cache, `GOWORK=off`, the proxy) pins sdk v0.20.0 + pgxdb v0.15.0 / datastores/turso
  v0.10.0, cites the guard `@v0.9.2` in its README, tidies, builds, vets, passes `go mod verify`,
  and `guard --posture` is clean (25 rules, 2 packages). The cold `init` from `@v0.9.2` is the
  owner's, after the tag (task-12). `make docs-build` in `workshop/documentation` succeeds.

**Owner rulings.** Ratified 2026-10-27 ("all recs"): **R-1 (a)** `ErrMutationInsideTransaction`,
`CacheableContext` and snapshot reads judge only the store's own instance's transaction (row 8 is
the price); **R-2 (a)** `TxFromContext` removed outright, no deprecated shim (a shim would need a
second instance-blind key); **R-3 (a)** per-store foreign-instance tests now, a storetest family
when the authorization core next tags; **R-4 (a)** tag `workshop/gopernicus/v0.9.2` for the
scaffold pins; **R-5 (a)** three PRs; **R-6 (b)** the libSQL-server turso leg WAIVED, with the
local-file legs as the proof. During the build: the sdk clause was reworded to "may reject
nesting on its own instance, never because another instance holds one"; **AUDIT-056** was added.

**As-built deviations a host can see.**
- **The sdk clause** reads: an implementation "neither joins nor refuses another instance's
  transaction; it may reject nesting on its own instance, never because another instance holds
  one — two datastores never share a transaction" (owner rewording of the plan's text).
- **AUDIT-056** records the migration; the plan had only qualified the existing AUDIT entries.
- **13 foreign-instance subtests per dialect**, not one per site: the reviews added a pgx
  `ReadTupleSnapshot` routing case beside its isolation case, a turso foreign-commit write case,
  and an own-instance-nests-around-a-foreign-one case.
- **Isolation checks over three nesting shapes** in the pgxdb live two-instance test (plain in
  plain, plain in snapshot, snapshot in plain) where the plan named the `TransactSnapshot` outer
  variant.
- **The pgx foreign instance sets `LockTimeout: 5s`** on its bare `Open`, so a regression waits a
  bounded time instead of hanging the suite.

**Known and deferred.**
- **The Turso libSQL-server leg was WAIVED** (R-6), as in S4d parts 1 and 2; the local-file
  driver runs (connector 230/0/0, store 1020/0/0) are accepted as Turso coverage; CI's
  `live-stores` skips Turso.
- **pgxdb `live_test.go:68`** (`TestLive_OpenAndMigrate`) counts every `default`-source ledger
  row, so it fails on a database a prior suite has migrated. Pre-existing; narrow it to
  `version = '0001_init.sql'`. Until then run the pgxdb live suite on a fresh database.
- **The DI9 comments** in `pockets/authentication/stores/{pgx,turso}/session_activity.go` still
  name `pgxdb.QuerierFrom` / `tursodb.QuerierFrom` as package functions.
- **R-3 (b):** a storetest `RunForeignInstance` family when the authorization core next tags.
- **S4d part 2 LOW-2:** pgx `Rotate`/`RotateAt`/`ConsumeGrace` lack turso's `session_profile`
  filter (owner rec: yes).

## Guard P9 reads every host ledger (workshop/gopernicus v0.9.1 — published 2026-10-26)

**PATCH.** Plan: [plans/guard-p9-ledgers.md](plans/guard-p9-ledgers.md) (ratified 2026-10-26).
The consumer is gps-three-sixty, which keeps authentication and authorization in their own
`iam` database.

- **P9 reads every ledger:** each directory under `workshop/migrations/` (`_`/`.`-prefixed
  skipped). With none, it reads `primary` as before.
- **One store, one ledger.** A store's home is the ledger holding byte-identical copies of its
  files. Identical copies in a second ledger are one finding per store, and a suffix match
  alone never makes a home. Missing and edited copies are judged against the home ledger. With
  more than one ledger, the status line names each store's ledger, e.g.
  `P9 ok (3 stores; ledgers: authentication→iam, authorization→iam, jobs→primary)`.
- **No change for single-ledger hosts.** A host with only `primary` prints exactly what v0.9.0
  printed.
- **Scaffold pins.** `init` and `new pocket` move sdk v0.18.0 / v0.15.0 → **v0.20.0**. pgxdb
  v0.14.0 and turso v0.9.0 are unchanged (already the latest). The emitted README runs the
  guard `@v0.9.1`.
- **Contract doc.** `examples/README.md` §3b, "P9 in full", is updated.

**Upgrade.** Nothing is required. A host that wants a second ledger moves each store's copies,
whole, into that ledger and applies it to that database.

| Tag | Commit | PR | Cold verification |
|---|---|---|---|
| `workshop/gopernicus/v0.9.1` | `8efa21f8` | #219 | OK, poll 1. A cold `init --db pgx` from `@v0.9.1` (`GOWORK=off`, empty module cache, `proxy.golang.org`) pins sdk v0.20.0 + pgxdb v0.14.0. Its README cites `@v0.9.1`. It tidies, builds, passes `go mod verify`, and `guard --posture` is clean (25 rules, 2 packages). On a host mounting authentication pgx v0.15.0, authorization pgx v0.21.0 and jobs pgx v0.7.0 with an `iam` + `primary` split, P9 is `ok (3 stores; …)`. |

**Known.** `integrations/kvstores/goredis` `TestOpenHonorsDeadlineAfterConnection` failed once
under a full local `make check` ("i/o timeout, want deadline exceeded") and passed on rerun.
It looks flaky under load; nothing was tagged for it.

## Session lifetime (SOC 2 slice S4d part 2 — published 2026-10-27)

Three annotated tags (not signed) on three modules. Each was cut at its PR's squash-merge commit
on `main` after a green `live-stores` dispatch there, pushed, THEN polled on the proxy, and
cold-verified from a scratch consumer (`GOWORK=off`, an empty module cache, `proxy.golang.org`,
`sum.golang.org`, `go mod verify`, `go build <module>/...`, a consumer pinning `go 1.26.10`). No
sdk tag and no views tag; one migration (0029, conditional). Design and rulings:
`plans/s4d2-session-lifetime.md` (ratified 2026-10-26; executed copy in `plans/`). Under the
owner's 2026-10-23 process ruling this section is the whole record: there is no handoff file and
no manifest. The operator surface is the docs site: the authentication page's *Added by S4d part
2*, *Production posture* ("Added by S4d part 2" and the Revocation SLA rows), *Keys and rotation*
("Revoking every session at once", idle beside it) and *Operations runbooks* ("Enable an idle
timeout on a live fleet", "Re-enable an idle timeout after a gap", "Migration 0029 lock timeout:
re-run"). The reference is the authentication README's "Session lifetime (SOC 2 S4d part 2)" and
the two store READMEs' 0029 sections.

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `pockets/authentication/v0.32.0` | `345ef68d` | #221 + #222 (PR-1) | OK; set A |
| 2 | `pockets/authentication/stores/pgx/v0.16.0` | `1b56c5c4` | #223 (PR-2) | OK; set B, gps-three-sixty shape, set C |
| 2 | `pockets/authentication/stores/turso/v0.15.0` | `1b56c5c4` | #223 (PR-2) | OK; set B, set C |

Order: #221 merged at `fb659449` without the M2 dedupe, which followed as #222 (`345ef68d`) BEFORE
the tag; `live-stores` went green on `main` (run 40540225155) and tag 1 was cut at `345ef68d`.
PR-2 then repinned both stores to core v0.32.0 (explicit `GOWORK=off go get`, then tidy; sdk stays
v0.20.0); #223 merged at `1b56c5c4`, `live-stores` went green on `main` (run 40554986831), and
both store tags were cut there. Every module ships MINOR under the v0 convention. Both stores
require authentication v0.32.0 and sdk v0.20.0; core requires sdk v0.20.0. The tag messages name
rows 2, 4, 8 and 9 (below).

**The MVS sets.** Each tag was cold-verified when it was cut; every set selected sdk v0.20.0.
- **Set A** (step 1): authentication v0.32.0 + the RELEASED stores pgx v0.15.0 / turso v0.14.0 + views/goth v0.9.0
  built. No lockstep: no existing port gained or changed a method. The released pgx v0.15.0 `SessionStore` does NOT
  implement `session.ActivityRepository`, so `IdleTimeout` on it refuses with `ErrIdleTimeoutRequires`.
- **Set B** (step 2): authentication v0.32.0 + pgx v0.16.0 + turso v0.15.0 + pgxdb v0.14.0 + datastores/turso v0.9.0
  built.
- **gps-three-sixty shape** (step 2): authentication v0.32.0 + pgx v0.16.0 + the authorization goredis store v0.6.0
  built.
- **Set C** (step 2): views/goth v0.9.0 + ui/goth v0.2.1 + rsc-qr v0.1.0 + go-webauthn v0.1.0 + set B (an
  auth-cms-shaped consumer) built.
- **This PR (from the tags):** auth-cms, production-baseline and external-identity at `GOWORK=off` from an EMPTY module
  cache over `proxy.golang.org` + `sum.golang.org`: `all modules verified` in each, and each server binary built (see the
  verification record).

**Not tagged.** sdk, views/goth (the page model is untouched; MVS lifts its core), `pockets`,
`workshop/gopernicus` (no new P-rule: the constructor refusals and WARNs are the check), ui/goth,
pgxdb (R-1 (b) is its own train), the examples, and every other module. Nothing retracts.

**Upgrade notes.** Eleven rows. Two breaks (row 2, an `AccessTokenTTL` above `RefreshTTL` refuses
boot; row 4, the `Live()` floor under an overridden sensitive group); one conditional boot failure
(rows 1/8: `AUTH_IDLE_TIMEOUT` set without migration 0029); everything else opt-in, additive or a
WARN. The "Read first" box, rows 2–5, 7, 9 and 11 and the paragraph under the table are the
plan's, verbatim; rows 1, 6, 8 and 10 are amended for the as-built #221–#223 and this PR (the
plan's text is quoted under the table). The pin bullet in the box is appended.

> **Read first:** `grep -rnE 'AccessTokenTTL|AUTH_ACCESS_TOKEN_TTL|RefreshTTL|AUTH_REFRESH_TTL|BundledRouteAuth|
> WithRouteAuthentication|PrincipalStrategy\(|RevocationFailOpen|ExternalProvider\{|session\.SessionRepository' --include='*.go'`.
> **By host shape:** every host → rows 2, 3, 7; hosts overriding `BundledRouteAuth` → row 4 (the break); external-engine (IdP)
> hosts → rows 5, 6; store hosts wanting idle → rows 1, 8, 9, 11; hosts with their own `SessionRepository` → row 1 (implement
> the interface or leave idle off).
> - **Pins (appended):** pin **authentication v0.32.0**; a store host pins **stores pgx v0.16.0** or **turso v0.15.0**
>   (both require core v0.32.0 and sdk v0.20.0). views/goth stays **v0.9.0**; sdk, ui/goth and every other module are
>   unchanged.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | **(amended)** `SessionsConfig.IdleTimeout` (`AUTH_IDLE_TIMEOUT`); `session.ActivityRepository`; `session_idle_expired` | opt-in; wired + 0029 absent → BOOT FAILURE naming `0029_session_activity.sql` (`ErrIdleTimeoutSchemaMissing`; 5 s probe, `sdk.ErrNotFound` only, anything else `sdk.ErrUnavailable`; setting the env alone flips it); wired on a store without the interface → `ErrIdleTimeoutRequires`; `<= AccessTokenTTL` refuses (`ErrIdleTimeoutBelowAccessTokenTTL`), `< 2 × AccessTokenTTL` WARNs `idle_granularity`, `>= RefreshTTL` WARNs `idle_never_fires`; grandfathered rows are never judged until stamped; first-party only; the idle 401 is the generic refresh 401 | leave zero | authentication@v0.32.0 |
| 2 | **BREAKING:** `AccessTokenTTL > RefreshTTL` refuses boot in every mode | an incoherent pair that booted on v0.31.0 stops; the error names both fields | fix the pair | authentication@v0.32.0 |
| 3 | production `AccessTokenTTL > 1 h` → `posture_warning=access_token_ttl_long`; the `auth: session lifetime` boot line | one WARN line; no behaviour change | — | authentication@v0.32.0 |
| 4 | **BREAKING (R-2):** the `Live()` floor under `SessionHydration`, `SessionSecurityReads`, `UserAdministration`, `MachineLifecycle`, `CredentialManagement` | an override lowered to `Stateless()`/`LiveOnMutation()` now does one session read per call (DB QPS rises) and, for external tokens under `RevocationFailOpen`, fails closed on those groups during an IdP outage; default postures unchanged | `BundledRouteAuthLivenessFloorRelaxed` (production WARN) | authentication@v0.32.0 |
| 5 | `ExternalProvider.MaxAuthenticationAge`; `external_subject_denied reason=authentication_age_exceeded\|authenticated_at_missing`; production WARN when no checker and no bound | opt-in; a bound denies tokens whose `auth_time`/`iat` is older than it — `iat`-only providers get a token-age bound | leave zero | authentication@v0.32.0 |
| 6 | **(amended)** the production `RevocationFailOpen` WARN becomes a `posture_waiver` line; per-provider `auth: external provider lifetime` boot line; the age-denial event is recorded **once per token** (M2), the denial itself on every request | log alerts keyed on "external provider revocation fails open on the default liveness tier" stop matching; an alert counting `external_subject_denied` rows counts tokens, not requests | re-key on `posture_waiver=RevocationFailOpen` | authentication@v0.32.0 |
| 7 | `IdleTimeout`/`MaxAuthenticationAge` mirrors in `.env.example`; `config_env_test.go` rows | none | — | authentication@v0.32.0 |
| 8 | **(amended)** **migration 0029** (`sessions.last_rotated_at`, nullable, `lock_timeout 5s` on pgx, excluded from the blanket probe), ledger copy **0035** (byte-identical, G34); `ActivityRepository` filled on both `SessionStore`s (pool path; pgx `ProbeSchema` reads `pg_attribute` through `to_regclass`, decoy-safe) | apply 0029 before setting row 1's env; otherwise optional; on lock timeout re-run `make migrate`; turso holds the write lock briefly | — | stores pgx@v0.16.0, turso@v0.15.0 |
| 9 | stores repin core v0.31.0 → v0.32.0 | MVS lifts a store host's core (rows 2–6 arrive with it) | — | stores pgx@next minor, turso@next minor |
| 10 | **(amended)** auth-cms `authmem` implements `ActivityRepository` and passes `AUTH_IDLE_TIMEOUT` through (commented in its `.env.example`, #221); the baseline ledger copy 0035 (#223); **the baseline sets `MaxAuthenticationAge` on its provider** (`IDP_MAX_AUTHENTICATION_AGE`, default 12 h; E4 = (a), this PR) and names `AUTH_IDLE_TIMEOUT` / `AUTH_ACCESS_TOKEN_TTL` as not read (it is external-only: no native sessions); external-identity carries `MaxAuthenticationAge` commented in `cmd/server/main.go` and `.env.example` | example behaviour | — | examples (untagged) |
| 11 | re-enabling idle after a disabled window re-judges stale stamps | at most one forced re-login per session; the runbook's NULL reset avoids it | run the reset first | docs |

Row 2 and row 4 are the breaks; row 1/8 is the conditional boot failure. Every module ships MINOR under v0; tag messages
name rows 2, 4, 8, 9. **Not touched:** sdk, views/goth, `pockets`, workshop (no P-rule; the constructor checks are the
posture), ui/goth, any existing port's method set, `sessionColumns`.

The plan's text for the amended rows was:
- **Row 1:** "`SessionsConfig.IdleTimeout` (`AUTH_IDLE_TIMEOUT`); `session.ActivityRepository`; `session_idle_expired` |
  opt-in; wired + 0029 absent → BOOT FAILURE naming 0029 (setting the env alone flips it); wired on a store without the
  interface → `ErrIdleTimeoutRequires`; `<= AccessTokenTTL` refuses; grandfathered rows are never judged until stamped;
  first-party only | leave zero | authentication@v0.32.0".
- **Row 6:** "the production `RevocationFailOpen` WARN becomes a `posture_waiver` line; per-provider `auth: external
  provider lifetime` boot line | log alerts keyed on "external provider revocation fails open on the default liveness
  tier" stop matching | re-key on `posture_waiver=RevocationFailOpen` | authentication@v0.32.0". M2 (owner ruling (a),
  #222) added the per-token event.
- **Row 8:** "**migration 0029** (`sessions.last_rotated_at`, nullable, `lock_timeout 5s`, excluded from the blanket
  probe), ledger copy at the next free number; `ActivityRepository` filled on both `SessionStore`s (pool path) | apply 0029
  before setting row 1's env; otherwise optional; on lock timeout re-run `make migrate`; turso holds the write lock
  briefly | — | stores pgx@next minor, turso@next minor".
- **Row 10:** "auth-cms `authmem` implements `ActivityRepository`; both examples' `.env.example` carry `AUTH_IDLE_TIMEOUT`
  commented; the baseline ledger copy; external-identity gets `MaxAuthenticationAge` commented | example behaviour | — |
  examples (untagged)". E4 = (a) made the baseline SET the bound rather than comment it; the baseline wires no
  `SessionsConfig`, so its `.env.example` lists `AUTH_IDLE_TIMEOUT` as not read.

**The 0029 rule.** `sessions.last_rotated_at` is required ONLY when `AUTH_IDLE_TIMEOUT`
(`SessionsConfig.IdleTimeout`) is wired. Unwired and unmigrated, the host boots and the column is
never read (`Rotate` never names it); wired and unmigrated, it refuses boot naming
`0029_session_activity.sql` (`ErrIdleTimeoutSchemaMissing`; the probe runs under a 5 s deadline
and maps only `sdk.ErrNotFound`, anything else to `sdk.ErrUnavailable`). A host may apply 0029
ahead of time; an old binary (core v0.31.0 on stores pgx v0.15.0 / turso v0.14.0) keeps working on
a migrated table, because every column list is explicit. On pgx 0029 opens with `SET LOCAL
lock_timeout = '5s'` (the `ALTER` takes `ACCESS EXCLUSIVE` on `sessions`): **on lock timeout,
re-run `make migrate`** — the failed file records nothing and the re-run applies it. On turso the
`ALTER` briefly holds the database write lock. The baseline ledger carries it as
`0035_session_activity.sql` (byte-identical, G34); the baseline ledger now ends at 0035. S4c-2
still holds core migration 0028 (unclaimed in the ledger).

**The grandfather rule.** A session is judged on `last_rotated_at` when it is stamped; on
`created_at` when it was never rotated (`rotation_count = 0`: a login nobody refreshed is
genuinely idle); and NOT judged while it carries rotations but no stamp — a row rotated while idle
was off — until its first wired refresh stamps it (`RefreshTTL` still bounds it). Enabling idle on
a live fleet therefore logs nobody out at once. Re-enabling after a disabled window re-judges the
stamps left from before it (at most one forced re-login per session); to avoid that, reset them
BEFORE setting the env again:

```sql
UPDATE sessions SET last_rotated_at = NULL WHERE session_profile = 'first_party';
```

The NULL reset is the only supported manual write to `last_rotated_at`.

**Handoff notes — what a host does on upgrade, by host shape** (applied at the roadmap's end).
Pins: sdk v0.20.0 (unchanged); authentication **v0.32.0**; stores pgx **v0.16.0** / turso
**v0.15.0** for a store host; views/goth stays **v0.9.0**. Repin with explicit `GOWORK=off go get
<module>@<tag>`, then `GOWORK=off go mod tidy`. With the defaults (idle off, access 15 m, refresh
7 d, floor enforced on the default postures, no age bound) nothing visible changes.
- **Every host** (rows 2, 3, 7). Run the Read-first grep. Check `AccessTokenTTL` ≤ `RefreshTTL`
  (else boot refuses); read the `auth: session lifetime` boot line; production above 1 h WARNs.
- **Hosts overriding `BundledRouteAuth`** (row 4, the break). An override lowered below `Live()`
  on `/auth/me`, the session security reads, credential management, the machine lifecycle or user
  administration now pays one session read per call, and fails closed (503) for an external token
  under `RevocationFailOpen` during an IdP outage. Accept it, or set
  `BundledRouteAuthLivenessFloorRelaxed` (production logs the waiver).
- **External-engine (IdP) hosts** (rows 5, 6). Set `MaxAuthenticationAge` on each provider
  (Firebase asserts `auth_time`: an absolute cap on the provider session; go-oidc/auth0: an
  `auth_time` cap only when the IdP asserts it, request it with `max_age`, else a token-age bound).
  Re-key alerts on `posture_waiver=RevocationFailOpen`. Idle does not apply (no native sessions).
- **Store hosts wanting idle** (rows 1, 8, 9, 11). Repin the store (MVS lifts core), apply 0029
  pre-boot, then set `AUTH_IDLE_TIMEOUT` above `AccessTokenTTL`; the expected logout wave is zero.
  Refresh on demand (on a 401), not on a timer: a timer-driven SPA is never idle.
- **gps-three-sixty** (native Google OAuth, bearer SPA, pgx + goredis): rows 2, 3, 8, 9 at repin;
  recommended `AUTH_IDLE_TIMEOUT` between 1 h and 12 h with `AUTH_REFRESH_TTL` 24 h (apply 0029
  first); the shape built in step 2. Rows 4–6 do not apply.
- **battleboard**: as gps-three-sixty; `RotateAt`/`DeleteIfIdle` run on the pool path exactly as
  `Rotate` does today (its RLS scope is an open question, `NEXT.md`).
- **Hosts with their own `SessionRepository`** (row 1): implement `session.ActivityRepository` and
  run `storetest` (the `SessionActivity` sub-suite skips when the interface is absent), or leave
  idle off.
- **React/SPA hosts** (the JSON contracts). An idle refresh is the generic 401
  `{"message":"unauthorized","code":"unauthenticated"}`, the same body as a garbage token: a client
  cannot show "signed out for inactivity". An over-age external token is the provider's 401 on
  every request.
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on the S4d part 2
  tags. Pins moved (explicit `GOWORK=off go get`, then `GOWORK=off go mod tidy`): `auth-cms`
  authentication v0.31.0→v0.32.0 (views/goth stays v0.9.0); `production-baseline` authentication
  v0.31.0→v0.32.0 and authentication stores/pgx v0.15.0→v0.16.0; `external-identity`
  authentication v0.31.0→v0.32.0. `cms`, `goth-showcase`, `jobs-minimal` and `minimal` pin no
  moved module. The baseline sets `MaxAuthenticationAge` (E4 = (a)), so its production test is
  back to the strict zero-WARN assertion PR-1 had loosened.

**The review record.** Every PR passed a code review before merge; the fixes were folded as
follow-up commits on the PR.
- **#221 (PR-1), lead-backend, ship-with-edits.** Folded: **E1** storetest activity-read
  equivalence (`GetByRefreshHashWithActivity` answers exactly what `GetByRefreshHash` does, plus
  the stamp); **E2** a single-read `reResolveFrom`, and the CAS-mismatch WARN + 500 for a
  `DeleteIfIdle` that lost on a row that did not move; **L3**, **L4**, **L6**. **L5** skipped.
  **M2**: owner ruled (a), one age-denial event per token — shipped as #222 before the tag.
  Deviations upheld by the review: (a) `reResolveIdle`; (b) `idle_source=config|default`.
- **#223 (PR-2), data-integration, ship.** **LOW-1** (the mapper field-copy test) and **LOW-3**
  (the never-backfill README note) folded. **LOW-2** (pgx `Rotate`/`RotateAt`/`ConsumeGrace` lack
  turso's `session_profile IN ('', 'first_party')` filter) and **LOW-4** (the migration-set rule
  wording, ruled below) are carried in `NEXT.md` / ruled.

**Verification record.**
- `make guard` and `make check` pass on this PR's repinned tree. S4d part 2 adds no guard and no
  P-rule. `live-stores` green on `main` before each tag (runs 40540225155 and 40554986831).
- **Cold verification:** every tag above; sets A, B, the gps-three-sixty shape and C as listed.
- **§1, the real auth-cms binary over HTTP** (#221, development, access 1 m, idle 90 s):
  `AUTH_IDLE_TIMEOUT=30s` and `AUTH_ACCESS_TOKEN_TTL=200h` both refuse boot; the boot line appears with the `idle_granularity` WARN; a
  session refreshed every 50 s stays **200**; an idle session gets **401** at 100 s with the same
  body as a garbage token; a never-refreshed session is judged on `created_at` → **401**. No
  reuse, theft or CAS-mismatch WARN; no token in the logs.
- **§2, the store legs** (#223). Live pgx on a fresh native PostgreSQL 17: **520/0/1**, bare and
  with `POSTGRES_TEST_SCHEMA=s4d2`, against the v0.32.0 tag; the 17 `SessionActivity` storetest
  cases run and pass. turso `-tags=integration` on the local-file driver **850/0/0**; the
  libSQL-server leg is WAIVED, as in part 1. 0029 under a held `ACCESS SHARE` lock fails after
  ~5.0 s (`55P03`) and records nothing; the re-run applies it. An old binary (core v0.31.0 + pgx
  v0.15.0 / turso v0.14.0) on a migrated schema: login + 2 refreshes OK, the stamp stays NULL.
  production-baseline on a fresh PG17: the ledger applies through 0035 and the server boots with
  the session-lifetime line.
- **§3, from the tags (this PR).** Both binaries built at `GOWORK=off` from the repinned tree (and again from an empty
  module cache, above).
  `go version -m` on auth-cms reads `pockets/authentication v0.32.0`,
  `pockets/authentication/views/goth v0.9.0`, `pockets/authorization v0.31.0`, `sdk v0.20.0`; on
  the baseline `pockets/authentication v0.32.0`, `pockets/authentication/stores/pgx v0.16.0`,
  `sdk v0.20.0`.
  - **auth-cms** (development, the in-memory store, the console mailer, port 8096,
    `AUTH_ACCESS_TOKEN_TTL=1m AUTH_IDLE_TIMEOUT=90s`): boot line `idle_timeout=1m30s
    idle_source=config … live_floor=enforced` plus the `idle_granularity` WARN. Register **201**,
    verify with the console-mailer code **200**, cookie login **200**, `/auth/me` **200**; a
    cookie refresh 53 s after login **200**; then idle 95 s, the next refresh **401**
    `{"message":"unauthorized","code":"unauthenticated"}` — byte-identical to a garbage refresh
    token's 401. No reuse/theft/CAS-mismatch WARN, no ERROR, no JWT in the log.
  - **production-baseline** on a fresh native PostgreSQL 17.4 cluster (port 55444), the ledger
    applied through `0035_session_activity.sql` by the baseline's migrator, the development
    issuer. Default boot: `auth: external provider lifetime provider=idp
    max_authentication_age=12h0m0s revocation_checker=none fail_open=false` and `auth: session
    lifetime … idle_timeout=off idle_source=default …`. Then with `IDP_MAX_AUTHENTICATION_AGE=1s`:
    a fresh single-factor token on `/auth/me` **200** (provisioned); the same token 83 s later
    (past the bound + the 60 s leeway) **401**, twice; the admin route
    `GET /auth/admin/security-events?event_type=external_subject_denied`, called by an external
    admin (bootstrapped with `cmd/bootstrap`) on a fresh AAL2 token, **200** with exactly ONE row,
    `external_subject_denied` `blocked` `{"provider":"idp","reason":"authentication_age_exceeded"}`
    — two denied requests, one event (M2). This is the bound the part-1 record promised for "an
    external admin's IdP session survives revoke-all".
  - The plan's baseline `$P` idle legs were not run: the baseline is external-only and wires no
    `SessionsConfig` (as-built, below). The refusal on a wired + unmigrated store is covered by
    the pgx live pocket test (§2).
  - Every example builds, vets and tests at `GOWORK=off`; `pnpm build` in
    `workshop/documentation` succeeds.

**Owner rulings.** Ratified 2026-10-26 ("ratified"): **R-1 (b)** the `QuerierFrom` fix is a
separate small PR outside this slice (inventory in `NEXT.md`); **R-2 (a)** the `Live()` floor plus
`BundledRouteAuthLivenessFloorRelaxed`; **R-3 (a)** production WARN above 1 h, `AccessTokenTTL >
RefreshTTL` refuses; **R-4 (a)** delegated `aal`/`methods` carried to part 3; **R-5 (a)** the
per-provider `MaxAuthenticationAge` semantics documented. Idle expiry on refresh keeps the pinned
generic 401 (lead default; the owner did not rule). During the build (2026-10-27): **M2 = (a)**,
one age-denial event per token (#222); **the CAS-mismatch sentinel stays unexported**; **E4 = (a)**,
the baseline sets `MaxAuthenticationAge`; **the migration-set rule:** shipped sets are additive-only
defaults — `ADD COLUMN` is fine for opt-in features, never destructive — and hosts own their schema.

**As-built deviations a host can see.**
- **`reResolveIdle`.** A lost `DeleteIfIdle` re-reads once: a row already gone answers the generic
  401 with no further event; an unchanged row (the CAS mismatched on a row that did not move) logs
  the CAS-mismatch WARN and answers 500; a row a concurrent refresh moved goes on to the grace lane.
- **`idle_source` has no `env` value** (G33): the pocket never reads the process environment, so
  an env value and a literal are both `config`; the plan's §2 `config|env|default` is
  `config|default`.
- **The age-denial event is deduped per token** (M2): every request with an over-age token is
  denied, but `external_subject_denied` for `authentication_age_exceeded` /
  `authenticated_at_missing` is recorded once per token (keyed by its SHA-256 in the
  `LoginRecordCache` store, until the token expires, clamped to [1 m, 24 h]; a cache fault records
  it again).
- **pgx `ProbeSchema` reads `pg_attribute` through `to_regclass`**, so a `last_rotated_at` column
  in a decoy schema on the search path never satisfies it.
- **turso `sessionActivityRow` duplicates `sessionRow`'s fields**: `ScanStruct` does not flatten
  embedded structs.
- **Ledger 0035**, not 0029's own number: the baseline ledger was at 0034, and S4c-2 still holds
  core 0028 unclaimed.
- **The baseline idle legs were dropped**: production-baseline is external-only (no native
  sessions, no `SessionsConfig`), so Verification §2/§3's `$P` idle legs became the
  `MaxAuthenticationAge` denial above.

**Known and deferred.**
- **The Turso libSQL-server leg was WAIVED**, as in part 1; the local-file driver run
  (`-tags=integration`, 850/0/0) is accepted as Turso coverage; CI's `live-stores` skips Turso.
- **R-1, the `QuerierFrom` cross-instance defect,** is its own PR (`NEXT.md` carries the
  fifteen-site inventory and the instance-blind nested check).
- **LOW-2:** pgx `Rotate`/`RotateAt`/`ConsumeGrace` lack turso's `session_profile` filter
  (recommendation: pgx gains it plus a storetest case).
- **Part 3:** the key-ring JWT signer with `kid`, `KeyedEncrypter`, `iss`/`aud` (M7); the breach
  checker and escalating lockout (M8); `aal`/`methods` on delegated tokens (R-4);
  `Identity.AuthTimeAsserted` (R-5, an sdk minor). **S4c-2** takes migration 0028.
- **Follow-ups** (the plan's Notes): bounded deadlines and `ErrNotFound`-only mapping for the
  EXISTING 0025/0026 probes; an `/auth/sessions` idle marker; a distinct idle refresh code; a
  pre-expiry idle signal. The battleboard RLS question stays open.
- **Residuals.** After idle expiry an issued access JWT passes `Stateless()` routes and
  `LiveOnMutation` safe reads for at most `AccessTokenTTL`; a client that refreshes on a timer is
  never idle; `GET /auth/sessions` may list idle-dead rows; logout by refresh token of an idle
  session still answers 204; delegated OAuth2 connections keep `OAuth2Config.SessionTTL`;
  `MaxAuthenticationAge` on an `iat`-only provider bounds token age only, and on a provider
  sending neither claim denies every token (boot cannot detect it).

## Session and machine governance (SOC 2 slice S4d part 1 — published 2026-10-26)

Four annotated tags (not signed) on four modules. Each was cut at its PR's squash-merge commit
on `main`, pushed, THEN polled on the proxy, and cold-verified from a scratch consumer
(`GOWORK=off`, an empty module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`,
`go build <module>/...`). No sdk tag; one migration (0027, conditional). Design and rulings:
`plans/s4d-session-machine-governance.md` (ratified 2026-10-26; executed copy in `plans/`). Under the owner's 2026-10-23 process ruling this section is the whole
record: there is no handoff file and no manifest. The handoff notes below say what a host does
on upgrade, and hosts adopt them once, at the roadmap's end. The operator surface is the docs
site: the authentication page's *Added by S4d*, *Production posture* ("Added by S4d" and the
Revocation SLA's closing line), *Keys and rotation* ("Revoking every session at once",
"API-key lifetime") and *Operations runbooks* ("Revoke all sessions", "Disable a service
account"). The reference is the authentication README's "Session and machine governance (SOC 2
S4d)" and the two store READMEs' 0027 and global-revocation sections.

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `pockets/authentication/v0.31.0` | `dfe104b6` | #214 (PR-1) | OK; set A |
| 2 | `pockets/authentication/stores/pgx/v0.15.0` | `b90a4eee` | #216 (PR-2) | OK; set B, gps-360-go shape |
| 2 | `pockets/authentication/stores/turso/v0.14.0` | `b90a4eee` | #216 (PR-2) | OK; set B |
| 3 | `pockets/authentication/views/goth/v0.9.0` | `b10c1df8` | #215 (PR-3) | OK; set C (full, after step 2) |

Order: 1 after #214 merged and a `live-stores` dispatch went green on `main` at `dfe104b6`;
PR-2 and PR-3 then ran in parallel worktrees off tag 1. 3 after #215 merged and its
run-and-look; 2 after #216 merged and a `live-stores` dispatch went green on `main` at
`b90a4eee` (run 40416938960). #215 and #216 each carried their module's repin to core v0.31.0
(views v0.28.0 → v0.31.0, stores v0.29.0 → v0.31.0; explicit `GOWORK=off go get`, then tidy;
sdk stays v0.20.0). Every module ships MINOR under the v0 convention. The tag messages name
rows 3, 4, 6 and 8 (below), the conditional 0027 and each module's requirements.

**The MVS sets.** Each tag was cold-verified when it was cut.
- **Set A** (step 1): authentication v0.31.0 beside the RELEASED stores pgx v0.14.0 / turso v0.13.0 and views/goth
  v0.8.1 selected sdk v0.20.0 and built. No lockstep: no existing port gained or changed a method, so the new slots
  stay nil under the released stores.
- **Set B** (step 2): both stores + authentication v0.31.0 + pgxdb v0.14.0 + datastores/turso v0.9.0 selected sdk
  v0.20.0 and built. A gps-360-go-shaped consumer (authentication v0.31.0 + the pgx store v0.15.0 + the authorization
  goredis store v0.6.0) built.
- **Set C** (step 3, full after step 2): views/goth v0.9.0 + ui/goth v0.2.1 + rsc-qr v0.1.0 + go-webauthn v0.1.0 +
  set B (an auth-cms-shaped consumer) built.
- **This PR (from the tags):** auth-cms and production-baseline at `GOWORK=off` from an EMPTY module cache over
  `proxy.golang.org` + `sum.golang.org`: `all modules verified` in both, and both binaries built (see the verification
  record).

**Not tagged.** sdk, `pockets`, `workshop/gopernicus` (no new P-rule: the constructor refusals and WARNs are the
check), ui/goth, the examples, and every other module. Nothing retracts.

**Upgrade notes.** Eleven rows. One behaviour break (row 3, API keys denied at the MFA gates, with the
`MFAConfig.APIKeysExempt` hatch); one production-only behaviour change (row 4, the API-key lifetime bound); one
conditional boot failure (rows 6/7: `AUTH_SERVICE_ACCOUNT_STATUS` set without migration 0027); everything else opt-in,
additive or a WARN. The "Read first" box, rows 1, 3, 8 and 10 and the paragraph under the table are the plan's,
verbatim; rows 2, 4, 5, 6, 7, 9 and 11 are amended for the as-built #214–#216 and this PR (the plan's text is quoted
under the table). The pin bullet in the box is appended.

> **Read first:** `grep -rnE 'RequireMFA\(|Accept\(.*CredentialAPIKey|UserAdminCheck|AdministrationConfig\{|MFAConfig\{|
> IdentityConfig\{|apikey\.APIKeyRepository|session\.ManagementRepository' --include='*.go'`. **By host shape:** every host →
> rows 1, 2; hosts admitting API keys on `RequireMFA()` routes or through `UserAdminCheck` → row 3 (the break); machine hosts in
> production → rows 4–6; store hosts → rows 7–8; goth hosts → row 9; OAuth-only hosts (gps-360-go) → rows 1, 2 only.
> - **Pins (appended):** pin **authentication v0.31.0**; a store host pins **stores pgx v0.15.0** or **turso v0.14.0**
>   (both require core v0.31.0 and sdk v0.20.0); a goth host pins **views/goth v0.9.0** (requires core v0.31.0). sdk,
>   ui/goth and every other module are unchanged. turso v0.14.0 and go-webauthn v0.1.0 declare `go 1.26.10`: a consumer
>   whose `go` directive is older needs `GOTOOLCHAIN=auto` (the default) or a `go` bump.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `IdentityConfig.Registration` (`AUTH_REGISTRATION`); unknown value refuses boot; `closed` + `ProvisionOnFirstVerification` refuses | zero = `open`, today's behaviour; boot line names the mode | — | authentication@v0.31.0 |
| 2 | **(amended)** `RevokeAllSessions`, `Repositories.GlobalSessions`, the opt-in route `POST /auth/admin/sessions/revoke-all` (`AdministrationConfig.GlobalSessionRevocationRoute`; `ErrGlobalSessionRevocationRouteRequires` without the slot or a `UserAdminCheck`), `UserAdminRevokeAllSessions` (`revoke-all-sessions`), three event types (`all_sessions_revoked_global` started/success/failure, `service_account_disabled`, `service_account_enabled`); the route admits **first-party access tokens and external tokens** (`Accept(AccessToken, ExternalToken), Live()`; owner ruling H1), refuses API keys and delegated tokens (401), runs detached from the request (10-minute limit), one call a minute per actor (429) | additive; a host `UserAdminCheck` ignoring `Action` admits the new action — but the route mounts only under the flag; **a check with an allow-list (the baseline's) denies it until it names the action**; an external admin's own IdP session survives the revoke (its next token is admitted) | leave false | authentication@v0.31.0 |
| 3 | **BREAKING:** API keys denied at `RequireMFA()`, the admin and the machine MFA gates (403 `mfa_credential_not_eligible`, no challenge); admin passkey/TOTP reset and every admin route included | a service account administering users or calling an `Accept(…APIKey)` + `RequireMFA()` route is refused | `MFAConfig.APIKeysExempt` (production WARN) | authentication@v0.31.0 |
| 4 | **(amended)** `APIKeyMaxTTL` (`AUTH_API_KEY_MAX_TTL`): production zero → `DefaultAPIKeyMaxTTL` **365 d** (R-3 (a); 90 d recommended), development zero = unlimited; `expires_at` beyond it → 400 `api_key_ttl_exceeded` (`max_ttl_seconds`, `max_expires_at`); at or before now → 400 `api_key_expiry_past` in every case; omitted under a bound → **filled with now + max** and echoed in the 201, `api_key_minted` gains `expires_at` (or `never`) and `expires_at_defaulted` (R-4 (a)); boot line `auth: api key lifetime api_key_max_ttl=<d>\|unlimited source=config\|default\|acknowledged` | **behaviour change in production:** keys minted without `expires_at` now expire a year after mint, with no pre-expiry signal; existing keys untouched | `APIKeyUnlimitedTTLAcknowledged` (production `posture_waiver` WARN); set `APIKeyMaxTTL` explicitly | authentication@v0.31.0 |
| 5 | **(amended)** `apikey.FilteredLister`; `?status=active\|expired\|revoked` / `?never_expires=true\|false`; `apiKeyResponse.status` on every row; strict parsing of the two new params (a repeated key or an unknown value → 400 `filter_invalid`; `status=expired&never_expires=true` → 400 `filter_contradiction`; `status=revoked&never_expires=true` is valid) | additive; a store without the lister → 400 `filter_unsupported` on a filtered call only; cursors do not bind the clock | — | authentication@v0.31.0 |
| 6 | **(amended)** `ServiceAccountStatus` (`AUTH_SERVICE_ACCOUNT_STATUS`) + `Repositories.ServiceAccountStatus`; disable/enable routes (strict `{}` → `{service_account, changed, keys_revoked}`); disable revokes every unrevoked key in the same transaction, enable restores minting only; a mint racing a disable re-reads the status and revokes its own key (M1); `serviceAccountResponse.status` (always present); `apikey_auth reason=service_account_disabled`; mint on a disabled account → 409 `service_account_disabled`; `changed_by` is `<type>:<id>` | opt-in; wired + 0027 absent → BOOT FAILURE naming 0027 (`ErrServiceAccountStatusSchemaMissing`; 5 s probe); the flag without the slot, `ServiceAccounts` or `APIKeys` → `ErrServiceAccountStatusRequires`; unwired never reads the table; wired, a status-store read failure answers 503 on API-key routes, never 401 | leave false | authentication@v0.31.0 |
| 7 | **(amended)** **migration 0027** (`service_account_status`, no FK, excluded from the blanket schema probe), ledger 0034 (byte-identical, G34); `GlobalRevoker` (keyset batches; pgx `SET LOCAL lock_timeout 5s / statement_timeout 60s` per batch, turso one `BEGIN IMMEDIATE` per batch, which holds the database write lock), `StatusRepository` (Disable/Enable lock the account row; an unknown account → `sdk.ErrNotFound`) and `FilteredLister` filled; **no store-side mint guard** (it would break `APIKeyStore.Create` on a database without 0027; core's M1 re-check is the guarantee) | apply 0027 before setting row 6's flag; otherwise optional | — | stores pgx@v0.15.0, turso@v0.14.0 |
| 8 | stores repin core v0.29.0 → v0.31.0 | MVS lifts a store host's core | — | stores pgx@v0.15.0, turso@v0.14.0 |
| 9 | **(amended)** `LoginPage.RegistrationClosed`; the goth login hides "Create an account" under `closed` (only; `invitation_only` keeps it); the register-form 403 renders core's invitation-required copy in goth's existing message slot (no templ change for it), keeping the email | a `closed` host on views v0.8.1 still renders the link (it answers 404); **a host's own login template must guard the link itself** (auth-cms's now does) | — | views/goth@v0.9.0 |
| 10 | the BE9 WARN (admins + no native factor + machine routes); `machineMFA` unchanged | one WARN line | wire a factor / override `MachineLifecycle` | authentication@v0.31.0 |
| 11 | **(amended)** auth-cms (#214, #215) wires row 2 (mounted by default; `AUTH_GLOBAL_SESSION_REVOCATION_ROUTE=false` unmounts it), row 6 (`AUTH_SERVICE_ACCOUNT_STATUS`), `AUTH_REGISTRATION`, `AUTH_API_KEY_MAX_TTL` and a development-only `AUTH_DEMO_API_KEYS_EXEMPT`; `authmem` implements the three ports; its login override honours `RegistrationClosed`. The baseline (this PR) wires row 2 behind the same `AUTH_GLOBAL_SESSION_REVOCATION_ROUTE`, opt-in (unmounted unless `=true`) and names `UserAdminRevokeAllSessions` in its allow-list; it stays machine-unwired; both `.env.example` | example behaviour | — | examples (untagged) |

Row 3 is the behaviour break; row 4 is a production-only behaviour change; row 6/7 is the conditional boot failure. Every
module ships MINOR under v0; tag messages name rows 3, 4, 6, 8. **Not touched:** sdk, `pockets`, workshop (no P-rule), ui/goth,
any existing port's method set (the set-A invariant).

The plan's text for the amended rows was:
- **Row 2:** "`RevokeAllSessions`, `Repositories.GlobalSessions`, the opt-in route, `UserAdminRevokeAllSessions`, three
  event types | additive; a host `UserAdminCheck` ignoring `Action` admits the new action — but the route mounts only
  under the flag | leave false | authentication@v0.31.0". The plan's route authenticator was
  `Accept(CredentialAccessToken), FirstParty(), Live()`; H1 (owner ruling) admits external tokens.
- **Row 4:** "`APIKeyMaxTTL`: production zero → the R-3 default; `expires_at` beyond it → 400; past → 400; omitted → R-4
  | **behaviour change in production:** keys minted without `expires_at` now expire (or 400); `api_key_minted` names it |
  `APIKeyUnlimitedTTLAcknowledged` (production WARN); set `APIKeyMaxTTL` explicitly | authentication@v0.31.0". R-3 and
  R-4 were both ruled (a), so there is no 400 for an omitted expiry.
- **Row 5:** "`apikey.FilteredLister`; `?status=`/`?never_expires=`; `apiKeyResponse.status`; strict parsing of the two
  new params | additive; a store without the lister → 400 `filter_unsupported` on a filtered call only | — |
  authentication@v0.31.0".
- **Row 6:** "`ServiceAccountStatus` + `Repositories.ServiceAccountStatus`; disable/enable routes;
  `serviceAccountResponse.status`; `apikey_auth reason` | opt-in; wired + 0027 absent → BOOT FAILURE naming 0027;
  unwired never reads the table | leave false | authentication@v0.31.0".
- **Row 7:** "**migration 0027** (`service_account_status`), ledger 0034; `GlobalRevoker` + `StatusRepository` +
  `FilteredLister` filled | apply 0027 before setting row 6's flag; otherwise optional | — | stores pgx@v0.15.0,
  turso@v0.14.0".
- **Row 9:** "`LoginPage.RegistrationClosed`; the goth login hides "Create an account" under `closed`; the register-form
  403 copy | a `closed` host on views v0.8.1 still renders the link (it answers 404) | — | views/goth@v0.9.0".
- **Row 11:** "auth-cms wires rows 2, 6 (and `invitation_only` behind an env), `authmem` implements the three ports;
  baseline stays machine-unwired; both `.env.example` | example behaviour | — | examples (untagged)". The baseline did
  not wire row 2 in the plan; Verification §3 needed it on, so this PR wires it.

Build-time deviations a host can see, folded here:
- **Row 2, the external admin.** The plan's §3 expected the external admin's next request to be 401 at the pocket. As
  built (and as the README and the Revocation SLA say), an external token is not a session: the revoke bumps the
  admin's local revision, but the provider's token is re-verified per request and is admitted again; the local kill
  switch for an external user is deactivation, and the per-actor budget throttles a repeat. §3 below records the 200.
- **Row 5, L8.** `status=revoked&never_expires=true` is a valid filter (a revoked never-expiring key exists); only
  `expired` contradicts `never_expires=true`. The plan's §1(4) line was corrected after the PR-1 review.
- **Row 1, invitation lookups.** A known user's magic link does no invitation lookup under `invitation_only` (M3): the
  mode governs account creation only.

**The 0027 rule.** `service_account_status` is required ONLY when `AUTH_SERVICE_ACCOUNT_STATUS`
(`AdministrationConfig.ServiceAccountStatus`) is wired. Unwired and unmigrated, the host boots and the table is never
read; wired and unmigrated, it refuses boot naming `0027_service_account_status.sql`
(`ErrServiceAccountStatusSchemaMissing`; the probe runs under a 5 s deadline and maps only `sdk.ErrNotFound`, anything
else to `sdk.ErrUnavailable`). A host may apply 0027 ahead of time. The baseline ledger carries it as
`0034_service_account_status.sql` (byte-identical, G34); the baseline ledger now ends at 0034. The S4c-2 plan takes
0028.

**Measured revoke durations** (#216, 10k active users, 30k sessions, 10k grants, batches of 500): native PostgreSQL 17.4
**73–100 ms** for the whole run, slowest batch 6.8 ms; turso on the local-file driver **387 ms**. No libSQL-server
measurement was taken (the leg was waived, below). On turso each batch holds the database write lock for its duration.

**Handoff notes — what a host does on upgrade, by host shape and module** (applied at the roadmap's end). Pins: sdk
v0.20.0 (unchanged); authentication **v0.31.0**; stores pgx **v0.15.0** / turso **v0.14.0** for a store host; views/goth
**v0.9.0** for a goth host; every other module unchanged. Repin with explicit `GOWORK=off go get <module>@<tag>`, then
`GOWORK=off go mod tidy`.
- **Every host** (rows 1, 2, 10).
  1. Read the boot log: `auth: registration registration=open source=default` (today's behaviour), `auth: api key
     lifetime …` and `api_keys=denied` on the MFA posture line. `AUTH_REGISTRATION` with any value other than `open`,
     `invitation_only` or `closed` refuses boot.
  2. Choose a registration mode if `open` is wrong: `invitation_only` needs a Granter, `closed` cannot sit beside
     `ProvisionOnFirstVerification`. A goth host repins views/goth v0.9.0 so the login hides "Create an account" under
     `closed`; a host with its own login template guards the link on `RegistrationClosed`.
  3. To offer revoke-all over HTTP, make the `UserAdminCheck` switch on `Action` with an allow-list that names
     `UserAdminRevokeAllSessions` (only the platform admins), then set `GlobalSessionRevocationRoute: true`. Give the
     route a proxy read timeout longer than a fleet run. Keep a CLI over `RevokeAllSessions` for when the admin path is
     what is compromised (README recipe).
- **Hosts admitting API keys on `RequireMFA()` routes or through `UserAdminCheck`** (row 3, the break). Run the Read-first
  grep. A service account that administers users, or calls a route with `Accept(…APIKey)` + `RequireMFA()`, now gets 403
  `mfa_credential_not_eligible` with no challenge. Move that automation to a route without `RequireMFA()`, or set
  `MFAConfig.APIKeysExempt` (production logs `posture_waiver=APIKeysExempt`) while it moves. A machine client switching
  on the 401 challenge sees a 403 instead and must not retry.
- **Machine hosts in production** (rows 4–6).
  1. Decide the key lifetime: zero is now 365 days; set `AUTH_API_KEY_MAX_TTL=2160h` (90 days) if clients rotate.
     Automation that mints without `expires_at` keeps working and gets an expiry a year out; nothing warns before it,
     so schedule rotation and run the runbook's SQL for never-expiring and soon-expiring keys. A client sending
     `expires_at` beyond the bound or in the past gets 400.
  2. To pause machine identities, apply 0027 pre-boot, then set `AUTH_SERVICE_ACCOUNT_STATUS=1`. Disable revokes the
     keys; enable restores minting only. A compromised account: revoke its keys or delete it.
  3. The BE9 WARN (row 10) means native sessions mint keys at AAL1: wire passkeys or TOTP, or override
     `BundledRouteAuth.MachineLifecycle`.
- **Store hosts** (rows 7, 8). Repin the store (MVS lifts core to v0.31.0). The stores fill `GlobalSessions`,
  `ServiceAccountStatus` and the filtered lister; 0027 is optional until row 6's flag is set. A host implementing
  `session.ManagementRepository` or `apikey.APIKeyRepository` itself changes nothing (no port method changed) and gains
  the features only by implementing the new ports; run `storetest` (the three sub-suites skip when a port is nil).
- **Goth hosts** (row 9). Repin views/goth v0.9.0.
- **OAuth-only hosts (gps-360-go): rows 1, 2 only.** Repin authentication v0.31.0 and the pgx store v0.15.0 (the gps-360-go
  shape built in set B). The registration mode governs its first Google sign-in; whether it is `open`,
  `invitation_only` (onboarding by invitation to the Google-verified address) or `closed` (a frozen roster) is decided at
  its adoption. Nothing else in this slice touches it unless it wires machine repositories.
- **React/SPA hosts** (the JSON contracts).
  - `POST /auth/admin/sessions/revoke-all` with `{}` (cookie callers send the CSRF header): 200
    `{"users":n,"sessions":n,"grants":n,"batches":n,"revoked_at":"<UTC RFC 3339 nanos>"}` and the session cookies cleared,
    so the next call is 401 (sign in again); 401 with the RFC 9470 challenge at AAL1 (step up, retry); 403
    `permission_denied` for a non-admin; 429 `rate_limited` for a second call within a minute; 404 when unmounted.
  - `GET /auth/service-accounts/{id}/keys?status=active|expired|revoked&never_expires=true|false`: every row carries
    `status`; 400 `filter_invalid`, `filter_contradiction` or `filter_unsupported`. A mint answers 201 with the
    (possibly defaulted) `expires_at`, 400 `api_key_ttl_exceeded` (`max_ttl_seconds`, `max_expires_at`) or
    `api_key_expiry_past`, 409 `service_account_disabled`.
  - `POST /auth/service-accounts/{id}/disable` and `…/enable` with `{}`: 200
    `{"service_account":{…,"status":"disabled"|"active"},"changed":bool,"keys_revoked":n}`; 404 when unwired.
    `status` is on every `serviceAccountResponse`.
  - Registration: 403 `registration_invitation_required` / `registration_closed` at `POST /auth/register`; the browser
    OAuth callback lands on its destination with `?auth=registration_closed|registration_invitation_required`.
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on the S4d tags. Pins moved (explicit
  `GOWORK=off go get`, then `GOWORK=off go mod tidy`): `auth-cms` views/goth v0.8.1→v0.9.0 (its authentication pin moved
  to v0.31.0 in #215); `production-baseline` authentication v0.30.0→v0.31.0 and authentication stores/pgx
  v0.14.0→v0.15.0; `external-identity` authentication v0.30.0→v0.31.0. `cms`, `goth-showcase`, `jobs-minimal` and
  `minimal` pin no moved module. No non-S4d pin moved, direct or indirect. external-identity runs the standalone external
  engine with no API keys, admin routes or machine routes: row 3 and the BE9 WARN do not reach it (its boot log carries
  no new WARN; an MFA token is 200, a single-factor one 401, as before). The baseline gains row 2 (above) and an S4d
  block in `.env.example`; it reads `AUTH_GLOBAL_SESSION_REVOCATION_ROUTE` only, and names the pocket's other S4d keys
  as not read (it provisions on first IdP sign-in with no Granter, so only `open` boots, and it wires no machine
  identity).

**The review record.** Every PR passed a lead-backend code review before merge; the fixes were folded as follow-up
commits on the PR.
- **#214 (PR-1), ship-with-edits, no blocker** (1 HIGH, 4 MEDIUM, 8 LOW). **H1** (owner ruling 2026-10-26): the revoke-all
  route admits external tokens — `Accept(AccessToken, ExternalToken), Live()` — and still refuses delegated tokens and
  API keys (`TestRevokeAllAdmitsExternalAdmins`). **M1** a key minted while a disable races it is re-checked after the
  insert and revoked (the live race: 0 of 43 keys worked after enable). **M2** the fleet revoke runs detached from the
  request context, bounded at 10 minutes. **M3** a known user's magic link does no invitation I/O
  (`TestExistingUsersMagicLinkSkipsTheInvitationLookup`). **M4** the evidence rows go through the Recorder's failure
  accounting (`RecordReporting`). **L1–L7** fixed. **L8** was a plan line, corrected (row 5's
  `revoked&never_expires`).
- **#216 (PR-2), ship-with-edits, 0 HIGH.** **M1** the global-revocation race test is committed
  (`TestGlobalRevocationRacesMints_Postgres`, `…_LocalSQLite`). **M2** the baseline README row for 0034. LOWs: turso
  `SweepOrphans` uses `NOT EXISTS`, turso `Delete` goes through `MapError`, and the re-run notes in both READMEs. Not
  done: the optional storetest additions (exact-multiple paging, mixed-case id order), because they live in core, which
  was already released as v0.31.0 (below).
- **#215 (PR-3).** Its record is the run-and-look and the tests (`TestLoginRegistrationClosed`,
  `TestRegister_InvitationRequiredRerender`, auth-cms's `TestLoginOverrideHonorsRegistrationClosed`); #215 records no
  review finding. The one scope addition: auth-cms's own login template printed the link unconditionally and is now
  guarded.

**Verification record.**
- CI `check`, `vuln` and the CodeQL analyses passed on the final run of #214–#216. Three CodeQL
  `go/clear-text-logging` alerts on #214 (the MFA posture line's `api_keys=` value and the API-key lifetime boot
  line's duration and source, none of them a credential) were dismissed as false positives. `make guard` and `make check` pass on this PR's repinned tree. S4d adds no guard and no
  P-rule.
- **Cold verification:** every tag above; sets A, B (and the gps-360-go shape) and C as listed; this PR's empty-cache
  from-the-tags builds.
- **§1, the real auth-cms binary over HTTP** (#214, development): **48/48** — revoke-all gated (non-admin 403, AAL1
  admin 401 + challenge, after a TOTP step-up 200 with counts and both cookies cleared), every session, the bearer
  user's refresh, a pending magic link and a delegated OAuth2 connection dead afterwards while an API key survives, the
  rail `started` then `success`, an API key on the route 401, the second call 429, the unflagged binary 404; the boot
  lines and `AUTH_REGISTRATION=bogus` refusing boot; the three registration modes; key lifetime and filters; disable and
  enable, including concurrent mints racing a disable (0 of 43 keys worked after enable); API keys at the MFA gates
  with and without the hatch; no response, row or non-mailer log line carrying a key, token or address. Not shown on
  that binary, covered by tests instead: an external admin at AAL2 (`TestRevokeAllAdmitsExternalAdmins`; shown on the
  baseline in §3 below) and an existing user's magic link under `invitation_only`
  (`TestExistingUsersMagicLinkSkipsTheInvitationLookup`). `pockets/authentication` build, vet and `test -race` (24
  packages); auth-cms tests three runs with `-count=1`.
- **§2, the store legs** (#216). Live pgx on a fresh native PostgreSQL 17.4: **503/0/1**, bare and with
  `POSTGRES_TEST_SCHEMA=s4d` (previously 485/0/1; the skip is the known `TestCollationControlsOrdering_NonC`); the
  storetest `GlobalRevocation`, `ServiceAccountStatus` and `APIKeyFilter` suites now run. turso: untagged **400/0/0**;
  `-tags=integration` on the local-file driver **824/0/0**. The race leg (200 users, 8 minters racing
  `RevokeAllBatch(50)`): every user bumped exactly once and no session minted at the old revision survived; pgx 5/5 runs
  (622–859 old-revision mints per run, 0 survivors). The 10k-user durations above. Boot shapes on PG17 with the
  baseline ledger through 0034: flag on boots; flag off boots; table dropped + flag on refuses naming
  `0027_service_account_status.sql`; table dropped + flag off boots, the real baseline binary included. `live-stores`
  green on the branch and on `main` at `b90a4eee` before tag 2.
- **§3, from the tags (this PR).** auth-cms and production-baseline built at `GOWORK=off` from the repinned tree with an
  empty module cache (`all modules verified` in both). `go version -m` on auth-cms reads `pockets/authentication
  v0.31.0`, `pockets/authentication/views/goth v0.9.0`, `pockets/authorization v0.31.0` and `sdk v0.20.0`; on the
  baseline `pockets/authentication v0.31.0`, `pockets/authentication/stores/pgx v0.15.0`, `pockets/authorization
  v0.31.0`, the authorization pgx store v0.21.0 and `sdk v0.20.0`.
  - **auth-cms, 13/13** (development, `AUTH_DEBUG=1`, the in-memory store, the console mailer, port 8094): revoke-all
    as a non-admin **403**; as an AAL1 admin **401** with the `insufficient_user_authentication` challenge; after a TOTP
    step-up **200** `{"users":2,"sessions":2,"grants":0,"batches":1,"revoked_at":"…Z"}`; the victim's next `/auth/me`
    **401**; a second call within the minute (after a recovery-code step-up) **429** `rate_limited`; the rail holds
    `started` + `success`; a binary with `AUTH_GLOBAL_SESSION_REVOCATION_ROUTE=false` **404**. With
    `AUTH_SERVICE_ACCOUNT_STATUS=1`: disable **200** `{changed:true, keys_revoked:2}`; the account's key **401** (200
    before); a mint **409** `service_account_disabled`; enable **200** `changed:true`, then a mint **201**; both old keys
    stay **401**.
  - **production-baseline** on a fresh native PostgreSQL 17.4 cluster, the ledger applied through 0034 by the
    baseline's migrator, the route on (`AUTH_GLOBAL_SESSION_REVOCATION_ROUTE=true`; the baseline default is off), the development issuer: an external user bootstrapped as the global
    admin (`cmd/bootstrap`). Revoke-all as a non-admin external user **403**; as the admin with a single-factor token
    **401** + the challenge; as the admin at **AAL2** (`amr: [pwd, mfa]`) **200**
    `{"users":2,"sessions":0,"grants":0,"batches":1,…}`, the rail `started` (`batch_size: 500`) then `success`; a second
    call **429**. **The admin's next request with the same provider token: 200, not the plan's 401** — the as-built
    contract (row 2 deviation above): an external IdP session survives the revoke. The local kill switch held: the admin
    deactivated the second external user and that user's next request was **401**. A baseline with
    `AUTH_GLOBAL_SESSION_REVOCATION_ROUTE=false` (now also the default) answers **404**.
  - Every example builds, vets and tests at `GOWORK=off` (the auth-cms server package three runs, all green);
    `pnpm build` in `workshop/documentation` succeeds.

**Owner drill: WAIVED 2026-10-26** by the owner. The owner booted the auth-cms build from `main` (development, port 8094, `AUTH_SERVICE_ACCOUNT_STATUS=1`) and the boot posture lines held (`mfa_required=admins … api_keys=denied`, `registration=open source=default`, `api_key_max_ttl=unlimited source=default`); no drill requests were run. Verification §3 from the tags (auth-cms 13/13; production-baseline on PostgreSQL 17 with ledger 0034) is the acceptance evidence. Executed plan: `plans/s4d-session-machine-governance.md`.

**Owner rulings.** Ratified 2026-10-26 ("ratified"): **R-1 (a)** chunked keyset batches of 500 users per transaction,
active users only, re-runnable, `started`/`success`/`failure` rows with partial counts; **R-2 (a)** API keys denied at
every MFA gate (`RequireMFA()`, admin, machine) with 403 `mfa_credential_not_eligible` and the `MFAConfig.APIKeysExempt`
hatch; **R-3 (a)** `APIKeyMaxTTL` production default 365 days, docs recommend 90; **R-4 (a)** an omitted `expires_at`
in production is filled with now + max, echoed in the 201, `expires_at_defaulted: true` on the event; **R-5 (a)** the
service method plus the opt-in route. gps-360-go's registration mode is decided at its adoption. **H1, owner ruling
2026-10-26 after the #214 review:** the revoke-all route admits external tokens (`Accept(AccessToken, ExternalToken),
Live()`); delegated tokens are still refused. **The Turso libSQL-server leg, owner ruling 2026-10-26:** waived for this
release (below).

**Known and deferred.**
- **The Turso libSQL-server leg was WAIVED by the owner** for this release. The local-file driver run
  (`-tags=integration`, 824/0/0) is accepted as Turso coverage; CI's `live-stores` skips Turso (its token is empty). The
  plan's 10k-user measurement on the Turso playground was therefore not taken.
- **No store-side mint guard.** The stores do not refuse an insert for a disabled account (it would break
  `APIKeyStore.Create` on a database without 0027); core's M1 re-check after the insert is the guarantee, and the store
  locking makes it reliable.
- **An erased user's service-account status row is left behind** as a harmless orphan (account ids are never reused);
  deleting a service account deletes its row best-effort.
- **The optional storetest additions** from the #216 review (exact-multiple paging, mixed-case id order) wait for the
  next core minor.
- **Follow-ups** (the plan's Notes): a pre-expiry notification for API keys (a maintenance job reading `expires_at`);
  bounded deadlines and `ErrNotFound`-only mapping for the EXISTING 0025/0026 probes; `requireJSON` on `revokeAPIKey`
  (pre-existing); a cross-account key listing (a SQL recipe ships instead); the pre-existing JSON-on-navigation for an
  invalid OAuth state.
- **Part 2** (M4, M7): `SessionsConfig.IdleTimeout` (`last_rotated_at`, a store train), an `AccessTokenTTL` ceiling,
  Live-tier defaults on the bundled reads, `ExternalProvider.MaxTokenLifetime` and `RevocationFailOpen`; the key-ring JWT
  signer with `kid`, `KeyedEncrypter` for the delivery and provider-token keys, `iss`/`aud` on first-party tokens;
  `aal`/`methods` on delegated tokens. **Part 3** (M8): the breach checker with a production WARN, escalating lockout.
  **S4c-2** (native `all` + enrollment grace) takes migration 0028.
- **Owed legs.** Docker Desktop hung on the host, so every PostgreSQL leg ran on a native PostgreSQL 17 instead of a
  container; no Linux-container `make test-stores` ran (`live-stores` covered `main`).
- **Residuals.** A stateless access JWT outlives the revoke by at most `AccessTokenTTL`; API keys and IdP sessions are
  untouched by it (the SLA table; the controls are key revocation, `DisableServiceAccount`, JWT-secret rotation and
  deactivation). The chunked revoke is not globally atomic: a failure leaves a partial fleet revoked, reported and
  re-runnable. `invitation_only` answers "an invitation is pending for this address" at `POST /auth/register`, budgeted
  per IP. Un-wiring `ServiceAccountStatus` resumes mints on disabled accounts (their keys stay revoked). Under
  `MFAConfig.Required: none` revoke-all is reachable at AAL1 behind the host check.

## Authorization governance and invitations (SOC 2 slice S7 — published 2026-10-26)

Three annotated tags (not signed) on three modules. Each was cut at its PR's squash-merge commit
on `main`, pushed, THEN polled on the proxy, and cold-verified from a scratch consumer
(`GOWORK=off`, an empty module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`,
`go build <module>/...`). No sdk, no pgx/turso store, no views tag and no migration. Design and
rulings: [plans/s7-authorization-governance.md](plans/s7-authorization-governance.md) (rev 1, ratified 2026-10-26, EXECUTED 2026-10-26). One further PR,
#212, is example-only and untagged: auth-cms's development-only `AUTH_DEMO_ADMIN_EMAIL`, which
lets the owner run the drill as a real signed-in platform admin. Under the owner's 2026-10-23
process ruling this section is the whole record: there is no handoff file and no manifest. The
handoff notes below say what a host does on upgrade, and hosts adopt them once, at the roadmap's
end. The operator surface is the docs site: the authorization page's *Added by S7*, the
authentication page's *Invitations and the audit route (S7)*, *Production posture* ("Added by
S7" and the Redis trust-boundary line) and *Operations runbooks* (the two audit rails over
HTTP). The reference is the authorization README's "Audit listing", "Access review" and
"Scheduled full rebuilds", the goredis store README's "Trust boundary and hardening", and the
authentication README's "The invitation link and the landing page" and "Reading the rail over
HTTP".

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `pockets/authorization/v0.31.0` | `34192968` | #209 (PR-1) | OK; set A |
| 2 | `pockets/authorization/stores/goredis/v0.6.0` (requires authorization v0.31.0, sdk v0.20.0) | `5982914e` | #210 (PR-2) | OK; set B |
| 3 | `pockets/authentication/v0.30.0` | `09f6e5d9` | #211 (PR-3) | OK; set C |
| — | none (example only: auth-cms `AUTH_DEMO_ADMIN_EMAIL`) | `32f22c0d` | #212 | — |

Order: 1 after #209 merged; 2 after #210 merged and the goredis race tests ran again on `main`
against a real `redis-server`; 3 after #211 merged and a `live-stores` dispatch went green on
`main`. PR-2 and PR-3 ran in parallel worktrees off tag 1. #210 carried the slice's one store
repin (goredis: authorization v0.27.0 → v0.31.0, sdk v0.18.0 → v0.20.0, explicit `GOWORK=off go
get`, then tidy). Every module ships MINOR under the v0 convention. The tag messages name the
`State.FullAt` change (authorization), the core requirement and the one-key-per-environment rule
(goredis), and the fragment break with its hatch, `InvitationURL` and the identifier-resolution
rule (authentication).

**The MVS sets.** Each tag was cold-verified when it was cut; this PR re-ran the three sets from an empty module
cache (`coldverify`: proxy poll, `GOWORK=off`, `go mod verify`, `go build`), all OK on the first poll.
- **Set A** (step 1): authorization v0.31.0 beside the RELEASED stores pgx v0.21.0 / turso v0.20.0 and the released goredis
  store v0.5.0 (authorization v0.27.0 pin): MVS selected core v0.31.0 and the set built (`all modules verified`). No
  lockstep: v0.31.0 is additive for every released store.
- **Set B** (step 2): goredis v0.6.0 + authorization v0.31.0 + the authorization pgx store v0.21.0 + pgxdb v0.14.0 (a
  gps-360-go-shaped consumer) selected sdk v0.20.0 and go-redis v9.18.0 and built.
- **Set C** (step 3): authentication v0.30.0 beside the RELEASED stores pgx v0.14.0 / turso v0.13.0 and views/goth v0.8.1
  selected sdk v0.20.0 and built (no store or views lockstep).
- **This PR (from the tags):** auth-cms and production-baseline at `GOWORK=off` from an EMPTY module cache over
  `proxy.golang.org` + `sum.golang.org`: `all modules verified` in both, and both binaries built (see the verification
  record).

**Not tagged.** sdk, stores pgx/turso (both pockets), views/goth, ui/goth, `pockets`,
`workshop/gopernicus` (no new P-rule: the constructor refusals are the check), the examples, and
every other module. Nothing retracts.

**Upgrade notes.** Sixteen rows. One behavior break (row 8, the invitation link's fragment, with
a one-release hatch) and one new production refusal beside it (`InvitationURL`, folded into row
8); two compile-adjacent changes (row 9's `ResolveInvitations` `kind` parameter and
`VerifiedIdentifiers`; row 13's `State.FullAt` field for an unkeyed `tuplecache.State` literal);
one error-code change (row 10); everything else opt-in or a WARN. The "Read first" box, rows 1–6
and 11 and the paragraph under the table are the plan's, verbatim, with `authentication@next`
resolved to `authentication@v0.30.0`; rows 7–10 are amended for the as-built #211 (the plan's
text is quoted under the table) and rows 12–16 are appended (the PR-1 and PR-2 review fixes and
#212). The pin bullet in the box is appended.

> **Read first:** `grep -rnE 'tuplecache\.Policy\{|goredis\.NewTupleCache|RoleRoutes\{|InvitationsConfig\{|LinksConfig\{'
> --include='*.go'`. **By host shape:** goredis TupleCache hosts (gps-360-go) → rows 1–3; every authorization host → rows
> 4–6; every invitation host → rows 8–10 (row 8 is the break); IdP-only hosts without invitations → row 7 only.
> - **Pins (appended):** pin **authorization v0.31.0** and **authentication v0.30.0**; a TupleCache host pins the goredis
>   store **v0.6.0** (it requires authorization v0.31.0 and sdk v0.20.0). sdk stays v0.20.0; stores pgx/turso, views/goth
>   and ui/goth are unchanged. Add to the grep `tuplecache\.State\{` (row 13), `ResolveInvitations|CallerIdentifiers`
>   (row 9) and `UserAdminCheck` (row 7).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `WithIntegrityKeys`; the `tuplecache:v3:ik:{…}` prefix; header MAC + fact tags; `Stats()` | opt-in; turning it on rebuilds the mirror once; ~50 B per fact | leave unset | goredis store@v0.6.0 |
| 2 | goredis store repinned to core v0.31.0 / sdk v0.20.0 (`State.FullAt`) | MVS lifts a goredis host from core v0.27.0 to v0.31.0 | none | goredis store@v0.6.0 |
| 3 | `Policy.FullRebuildEvery`; `State.FullAt`; `Stats.LastFullRebuild` | additive; zero = never; a custom `Backend` may leave `FullAt` zero | leave zero | authorization@v0.31.0 |
| 4 | `RoleRoutes.AuditGate`, `Services.Audit`, `WithAuditReadObserver`, `GET /authorization/audit` | opt-in; a gate without a reader fails `New`; a gate without an observer WARNs | leave nil | authorization@v0.31.0 |
| 5 | `RefuseSelfTarget`, `AllOf` | additive | none | authorization@v0.31.0 |
| 6 | integrity WARN (`owner` unprotected, or `Mutations` nil beside `owner`) | one WARN line in production and development | `WithIntegrityPolicy(DefaultIntegrityPolicy())` | authorization@v0.31.0 |
| 7 | **(amended)** `AdministrationConfig.SecurityEventsRoute` → `GET /auth/admin/security-events`, `UserAdminReadAudit` (`read-audit`; the `user_id` filter is `TargetUserID`), `audit_read` (`securityevent.TypeAuditRead`); `ErrSecurityEventsRouteRequires` without `Repositories.SecurityEvents` or a `UserAdminCheck` | opt-in; **a `UserAdminCheck` that does not switch on `Action` admits the new action** (auth-cms's and the baseline's now switch on it with an allow-list) — scope it; cursor only, `since`/`until`, repeated keys, `offset`, `q` and `order` → 400; `event_type` validated by shape (snake_case), `event_status` ∈ success/failure/blocked | leave false | authentication@v0.30.0 |
| 8 | **(amended) BREAKING:** invitation token in the URL fragment (`#token=`); a `#` in a requested `redirect` → 400 `invitation_redirect_fragment` before anything persists; **`LinksConfig.InvitationURL`** (`AUTH_INVITATION_URL`, R-7 (a)): the default destination, **required in production beside a Granter** (`ErrInvitationURLRequired`), validated in every mode (absolute http(s), no fragment, no userinfo: `ErrInvitationURLInvalid`; HTTPS in production: `ErrInvitationURLInsecure`) | a landing page reading `?token=` stops accepting; invitation, magic and OAuth pending links all carry `#token=` — tell them apart by path; a production invitation host without `AUTH_INVITATION_URL` refuses boot (development WARNs and keeps `/`); templates building their own link from `Secret` are unaffected | `LinksConfig.InvitationTokenInQuery` (`AUTH_INVITATION_TOKEN_IN_QUERY`; ONE release — the next minor removes the field; production `posture_waiver=InvitationTokenInQuery` WARN, development WARN) | authentication@v0.30.0 |
| 9 | **(amended)** identifier resolution **by verified sign-in/recovery identifiers only** (login- or recovery-enabled, unique across users; owner ruling on HIGH-1) on every verification path: registration verify, passwordless provisioning **and adoption** (MEDIUM-3), OAuth, external, `ConfirmIdentifierChange` (last, after the notices, only on a nil apply) and `SetIdentifierUses` (an identifier gaining the claim); `SyncInvitations` + `POST /auth/invitations/sync` `{resolved}` (pending rows only, ≤ 8 identifiers, `AuthenticationLimits.InvitationSync` 10/min per user, 60/min per IP); `mine?identifier_kind=email\|phone` (else 400); **`ResolveInvitations` gains a `kind` parameter** (beside the new `ResolvePendingInvitations`); `VerifiedIdentifiers` replaces `CallerIdentifiers` in `invitations.AccessConfig`; an open-kind accept needs a claim-holding verified identifier; `AcceptInput.Identifier` deprecated and ignored | adding a sign-in identifier, or enabling login/recovery on a verified one, grants its AutoAccept invitations; a second sign-in email matches at accept; a notification-only identifier never resolves, syncs, lists or matches | none (behavior); fix the call | authentication@v0.30.0 |
| 10 | **(amended)** error codes `invitation_identity_mismatch` 403 (replaces `permission_denied` for the identity mismatch, and the 404 a caller with no verified email got before the match) and `invitation_redirect_fragment` 400; `invitation_granted` Details gain `operation_id` + `resolved_by` (`accept`\|`verification`\|`sync`\|`direct_add`) on every row, `invitation_id` on the accept paths, `direct_add` + `invited_by` on a direct add | a client switching on `permission_denied` or the 404 sees the new code; racing paths may record duplicate `invitation_granted` rows for one grant — dedupe on `operation_id` | none | authentication@v0.30.0 |
| 11 | baseline + auth-cms wire both routes, `AllOf(RefuseSelfTarget, …)`, the bridge method | example behaviour | — | examples (untagged) |
| 12 | a failed SCHEDULED full rebuild backs off for `min(FullRebuildEvery, tuplecache.ScheduledRebuildBackoff × PollInterval)` (8 intervals); `Stats.ScheduledRebuilds`, `Stats.ScheduledRebuildFailures`; a backend without `FullAt` starts the per-process clock at construction (no rebuild at boot) | deltas, revocations included, keep publishing while rebuilds fail; alert on `ScheduledRebuildFailures` | none | authorization@v0.31.0 |
| 13 | `tuplecache.State` gained `FullAt`: an unkeyed `State{binding, receipt}` literal no longer compiles; backends compare mirrors by `Binding` + `Receipt` only; `storetest` feeds the unstripped `State()` back into `Read`/`Publish` (`UnstrippedStateAddressesTheMirror`, `FullAtMarksFullPublications`) | compile break for an unkeyed literal; a custom `Backend` comparing whole `State` values with `==` fails the suite | use keyed fields; compare `Binding`/`Receipt` | authorization@v0.31.0, goredis store@v0.6.0 |
| 14 | `GET /authorization/audit` as built: `no-store` set ahead of `AuditGate` (gate denials carry it), every listing key at most once (400), a `HEAD` records no read; **`AuditGate` must admit global-history readers only** (every filter pair is optional) | a per-resource gate is not a tenant boundary on this route | none | authorization@v0.31.0 |
| 15 | `WithIntegrityKeys` as built: the header MAC also binds the **full mirror key** (namespace + binding), so a signed mirror copied across namespaces, bindings or cloned environments fails and is rebuilt; a header naming another binding is counted and replaced; `ErrIntegrity` matches `tuplecache.ErrUnavailable`; deltas sign with the key that opened the epoch; two-phase rotation `(old,new)` → `(new,old)` → `(new)`; the README ACL user grants `+select` | **one integrity key per environment/deployment**; a set holding a bad tag fails closed until the next full publication (bounded by `FullRebuildEvery`) | leave unset | goredis store@v0.6.0 |
| 16 | auth-cms `AUTH_DEMO_ADMIN_EMAIL` (development only; production refuses boot with it set): the verified login-enabled email gets `platform:main#admin` once, through `Mutations`, audit source `system=demo-admin` | example behaviour | — | examples (untagged, #212) |

Row 8 is the one behavior break; row 9 is a compile-adjacent signature change; row 10 changes one error code; row 7 is a
named opt-in. Every module ships MINOR under v0; tag messages name rows 2, 7, 8, 9 and 10. **If R-7 = (a):** row 8 also gains `LinksConfig.InvitationURL`
(production-required beside a Granter; `ErrInvitationURLRequired`), PR-3 adds it to `config.go`/`security.go`/both examples'
env and `.env.example`, and `guard --posture` is NOT extended (no new P-rule; the constructor refusal is the check).

R-7 was ruled (a), so `InvitationURL` is folded into amended row 8 (auth-cms sets it from its origin, overridable by
`AUTH_INVITATION_URL` in `.env.example`; the baseline wires no Granter). The plan's text for the amended rows was:
- **Row 7:** "`AdministrationConfig.SecurityEventsRoute` → `GET /auth/admin/security-events`, `UserAdminReadAudit`,
  `audit_read` | opt-in; **a `UserAdminCheck` that does not switch on `Action` admits the new action** (auth-cms's does) —
  scope it | leave false | authentication@next".
- **Row 8:** "**BREAKING:** invitation token in the URL fragment; a `#` in a requested `redirect` → 400 | a landing page
  reading `?token=` stops accepting; templates building their own link from `Secret` are unaffected |
  `LinksConfig.InvitationTokenInQuery` (one release; production WARN) | authentication@next".
- **Row 9:** "identifier resolution for every kind on every verification path incl. `ConfirmIdentifierChange`;
  `SyncInvitations` + `POST /auth/invitations/sync`; `mine?identifier_kind=`; **`ResolveInvitations` gains a `kind`
  parameter** (compile-adjacent: a host calling it or implementing `invitationResolver`); `VerifiedIdentifiers` replaces
  `CallerIdentifiers` in `invitations.AccessConfig` | adding a verified identifier now grants its AutoAccept invitations; a
  second verified email matches at accept | none (behavior); fix the call | authentication@next".
- **Row 10:** "error codes `invitation_identity_mismatch` (replaces `forbidden` for the email/phone mismatch) and
  `invitation_redirect_fragment`; `invitation_granted` Details gain `invited_by`/`operation_id`/`direct_add`/`invitation_id`/
  `resolved_by` | a client switching on `forbidden` for the mismatch sees a new code | none | authentication@next".
  As built, the replaced code is `permission_denied`, not `forbidden`.

Build-time deviations a host can see, folded here (rows 1–6 and 11 stay verbatim):
- **Row 9, the resolver port.** `authlogic`'s `invitationResolver` port stays one method; `SyncInvitations` type-asserts an
  unexported pending-only resolver and resolves nothing without it. No outside caller exists in this repo; gps-360-go
  carries only a vendored copy and never calls `ResolveInvitations` directly.
- **Row 9, `mine`.** It lists the invitations addressed to ONE claim-holding verified identifier of the kind (the primary
  first); none of that kind → 404. A merged listing across identifiers is deferred (below).
- **Row 9, a narrow race.** The accept-time match reads the verified identifiers, then claims: an identifier removed
  between the two still lets that one accept complete (both actions are the subject's own; README).
- **Row 7.** Only `user_id` is validated before the host check; the other query keys after it. `event_type` is checked by shape, not against `Types()`, because the rail carries host-bridged types.
- **Row 5.** `RefuseSelfTarget` compares the principal's and the subject's `Type`/`ID` byte for byte: a host mapping
  principals into another subject namespace (`account:1` granted as `user:1`) writes its own check; a grant to a group
  the actor belongs to passes.
- **Row 2.** The goredis repin moved authorization v0.27.0 → v0.31.0 and sdk v0.18.0 → v0.20.0 in one step; set B proves it.

**Handoff notes — what a host does on upgrade, by host shape and module** (applied at the roadmap's end). Pins: sdk
v0.20.0 (unchanged); authorization **v0.31.0**; authentication **v0.30.0**; the goredis store **v0.6.0** for a TupleCache
host; every other module unchanged. Repin with explicit `GOWORK=off go get <module>@<tag>`, then `GOWORK=off go mod tidy`.
- **Every host repinning** (rows 3–6, 12, 13).
  1. Nothing new is required at boot unless the host wires invitations in production (row 8: `AUTH_INVITATION_URL`). No
     migration ships.
  2. Row 13: grep for `tuplecache.State{`; give any unkeyed literal field names. A custom TupleCache `Backend` compares
     `Binding` and `Receipt`, never the whole struct, and runs `storetest` again.
  3. Row 6: read the boot log. An `owner` relation the integrity policy does not protect, or `Repositories.Mutations` nil
     beside one, now logs one WARN in production and development. Fix it with
     `WithIntegrityPolicy(mutations.DefaultIntegrityPolicy())` or a rule per type; it never refuses.
  4. Rows 4, 5, 7 are opt-in. A host turning on `GET /auth/admin/security-events` first makes its `UserAdminCheck`
     switch on `Action` with an allow-list, then sets `SecurityEventsRoute: true`. A host turning on
     `GET /authorization/audit` passes a global-history gate (platform admin or auditor) as `AuditGate` and bridges
     `WithAuditReadObserver` into its security-event rail as `audit_read` with `rail: authorization`. Role writes can
     refuse self-grants with `WritePolicy: AllOf(RefuseSelfTarget, hostPolicy)`.
- **IdP/OAuth-only hosts (gps-360-go): authorization + the goredis store** (rows 1–3, 12, 13, 15).
  1. Repin authorization v0.31.0 and the goredis store v0.6.0 together (MVS lifts the core from v0.27.0; set B built this
     exact shape). Authentication v0.30.0 matters only if the host wires invitations or the security-events route.
  2. The hardening recipe matches its `AUTH_CACHE_REDIS_URL` (`rediss://<acl-user>:<password>@<host>:<port>/<db>`, an ACL
     user, a dedicated instance): apply the README's ACL user, adding `+select` if the URL names a database other than 0
     (go-redis selects it on every connection), and keep `rediss://` with a verified CA.
  3. Turn on `WithIntegrityKeys` with **one key per environment** (`openssl rand -hex 32`, in the secret store beside the
     database credential, never shared with a staging database cloned from production), and **pair it with
     `FullRebuildEvery`** (one hour is a reasonable start; it must stay at or above the defaulted `PollInterval`). The
     first poll after turning keys on rebuilds the mirror in full under the new `tuplecache:v3:ik:{…}` key; the old mirror
     expires after `WithExpiry`. Roll it out quickly: keyed and unkeyed replicas on one database rebuild each other's
     mirror while they overlap.
  4. Rotate in two phases: deploy `WithIntegrityKeys(old, new)` everywhere, then `(new, old)`, then `(new)` after one full
     publication. Alert on a rising `Stats().IntegrityFailures` (goredis) and `ScheduledRebuildFailures` (core).
- **Hosts with invitations** (rows 8–10).
  1. **Landing page.** Read the token from `location.hash`, not `?token=`; `history.replaceState` it away before any
     network call or third-party script; hold it in `sessionStorage` across a sign-in detour (never `return_to` or a
     query); serve the page with `Referrer-Policy: no-referrer` and `Cache-Control: no-store`. Invitation, magic and
     OAuth pending links all carry `#token=`: **tell them apart BY PATH** and give the invitation landing its own route.
  2. Set **`AUTH_INVITATION_URL`** to that route's absolute `https` URL (production refuses boot without it beside a
     Granter). A requested `redirect` must not contain `#`.
  3. Until the page reads the fragment, set `AUTH_INVITATION_TOKEN_IN_QUERY=1` for this release only (production logs the
     waiver); the next authentication minor removes the field.
  4. A client switching on `permission_denied` (or a 404) for an identity mismatch switches on
     `invitation_identity_mismatch`. Audit queries over `invitation_granted` dedupe on `operation_id`.
  5. A Go caller of `ResolveInvitations` adds the `kind`; a fake of `invitations.AccessConfig` implements
     `VerifiedIdentifiers`.
- **React/SPA hosts** (rows 8, 9). After an identifier add or a login/recovery change on an identifier, call
  `POST /auth/invitations/sync` (`{}` → `{"resolved": n}`; cookie callers double-submit the CSRF header; 429 past the
  budget) and read `GET /auth/invitations/mine?identifier_kind=email` and `?identifier_kind=phone` to list what is still
  pending. The two audit routes are JSON only (R-4 (a)); page them by cursor.
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on the S7 tags. Pins moved (explicit
  `GOWORK=off go get`, then `GOWORK=off go mod tidy`): `auth-cms` authentication v0.29.0→v0.30.0 and authorization
  v0.30.0→v0.31.0; `production-baseline` authentication v0.29.0→v0.30.0 and authorization v0.30.0→v0.31.0;
  `external-identity` authentication v0.29.0→v0.30.0. No example pins the goredis store. `cms`, `goth-showcase`,
  `jobs-minimal` and `minimal` pin no moved module. No non-S7 pin moved, direct or indirect. The code-side example
  changes landed with #211 (both routes, the bridges' `AuditReads()`, `AllOf(RefuseSelfTarget, …)`, `InvitationURL`, the
  `Action` allow-lists) and #212 (`AUTH_DEMO_ADMIN_EMAIL`).

**The review record.** Every PR passed a lead-backend code review before merge; the fixes were folded as follow-up
commits on the PR.
- **#209 (PR-1), no blocker.** **HIGH-1** (lead ruling: back off) a failed scheduled full rebuild re-promoted every poll,
  so the mirror stayed unavailable; promotion now pauses for `min(FullRebuildEvery, 8 × PollInterval)` and
  `ScheduledRebuildFailures` counts it (the reviewer's repro went from 5/5 polls unavailable to one failure, then four
  publishes; row 12). **HIGH-2** `storetest` fed a stripped `State` back to the backend; it now feeds the unstripped
  `State()` (`UnstrippedStateAddressesTheMirror`), a same-receipt publish with a different `FullAt` is refused, and
  backends compare `Binding` + `Receipt` only (carried into #210; row 13). **MEDIUM-1** `no-store` runs ahead of the
  gate. **MEDIUM-2** the README says `AuditGate` must be a global-history gate; repeated listing keys are 400 (row 14).
  NITs: `HEAD` records no read; README notes on the `State.FullAt` upgrade, the restart period, replica races and the
  `RefuseSelfTarget` namespace limit.
- **#210 (PR-2), no blocker.** **HIGH** the header MAC did not cover the mirror key, so a signed mirror copied to another
  namespace or binding (for example between environments with cloned databases) could serve foreign grants; the MAC now
  binds the full mirror key, and the README requires one key per environment (row 15). **MEDIUM** the ACL recipe gained
  `+select` (proven on DB 1 with an empty `ACL LOG`). **MEDIUM** a header naming another binding counts as an integrity
  failure and is replaced instead of locking out silently. NITs: two-phase rotation, the `FullRebuildEvery` pairing, the
  wrong-type-value and unkeyed `!epoch_at` residuals, a recovery-fence conflict test. The reviewer's adversarial tests
  were ported as regressions and mutation-checked. Lead-accepted deviation: deltas sign with the key that opened the
  epoch, so one epoch never mixes keys.
- **#211 (PR-3), ship-with-edits, no blocker.** **HIGH-1** (owner-ruled 2026-10-26) resolution admitted any verified
  identifier, so a notification-only identifier two accounts may share (a household phone) could resolve, sync, list
  and match; only verified identifiers holding the sign-in/recovery claim do now, and enabling login or recovery on one
  resolves its invitations (row 9). **MEDIUM-2** documented (the accept-time race). **MEDIUM-3** (lead) passwordless
  adoption of an unverified identifier resolves like a provision. **LOW-4–6** and the NITs: an open-kind accept needs a
  claim-holding verified identifier, the resolver port is one method again, the sync budget is
  `AuthenticationLimits.InvitationSync`, the security-events query validates only `user_id` before the host check,
  `validateInvitationURL` refuses userinfo, the baseline's `UserAdmin` check switches on `Action` with an allow-list, and
  the landing-page "tell links apart by path" note. **A root-caused test flake:** `TestProvisionedAccountResolvesPending
  Invitations` failed 5 of 15 runs because the test's magic-link mail scraper matched the invitation mail's new
  `#token=`; the scrapers are anchored to the landing path (`/auth/magic#token=`). The product was unaffected.
- **#212** (example only): verified by its tests and its real-binary leg below.

**Verification record.**
- CI `check`, `vuln` and the CodeQL analyses passed on the final run of #209–#212. `make guard` and `make check` pass on
  this PR's repinned tree. S7 adds no guard and no P-rule.
- **Cold verification:** every tag above; sets A, B and C as listed; this PR's empty-cache from-the-tags builds.
- **Live legs.** #210: the goredis suite with `-race` and `-tags integration` against a scratch `redis-server` 8.4 on a
  private socket, `GOWORK=off` build and test, govulncheck clean; the store's Postgres end-to-end leg on a native
  PostgreSQL 17 (all four dual-dialect `TestSQLRedis` tests passing on sqlite and postgres); and the race tests again on
  `main` before tag 2. `live-stores` green on `main` before tag 3. No pgx/turso store changed, so no store pass/fail
  counts are owed.
- **The Redis tamper drill** (#210, Verification §2, by hand as the README's ACL user on DB 1): **12/12** — the ACL user
  reaches the cache keys only; forged and malformed facts are refused and answered from SQL; a tampered header is
  refused; an older mirror restored over the current one is ineligible within `MaxStaleness` and rebuilt; a replayed
  revoked fact is bounded by `Rebuild`; a revoke whose stored tag differs still removes; key rotation keeps reading;
  keyed and unkeyed mirrors stay separate; a mirror copied across namespaces and one naming a foreign binding fail and
  are rebuilt; two replicas with `FullRebuildEvery` made 2 scheduled rebuilds between them, not 4. Disabling tag
  verification, header verification or the Lua identity rule each fails the tamper tests (mutation-checked).
- **§1 + §3, the real binaries over HTTP** (#211, development): **auth-cms 44/44**, re-run after the review fixes, and
  **production-baseline on a native PostgreSQL 17, 17/17**: both audit routes gated,
  paged by cursor without duplicates, `no-store`, one `audit_read` per read and none deduplicated; a self-assign refused
  with a `role_write_refused` row; the invitation mail link carrying `#token=` and the hatch binary carrying `?token=`;
  the owner's cross-identifier case (add and verify an email on an existing account: the AutoAccept invitation grants
  with `resolved_by: verification`, the other stays pending for its token, sync answers `{resolved: 0}`, the 11th sync in a
  minute is 429). New tests pin the HIGH-1 ruling (`TestNotificationOnlyPhoneNeverResolvesSyncsOrMatches`,
  `TestNotificationOnlyEmailResolvesOnceLoginIsEnabled`), MEDIUM-3 (`TestAdoptedAccountResolvesPendingInvitations`) and
  the duplicate-row behaviour (`TestConcurrentResolutionDuplicatesShareOperationID`). auth-cms's full suite passed 3/3
  (lead) and 5/5 plus 10/10 on the scraper fix (implementer).
- **#212, the real auth-cms binary:** the grant fired on `/auth/verify` before sign-in; `/authorization/audit` 200 with
  the `demo-admin` source; `/auth/admin/security-events` 401 at AAL1, then 200 after a TOTP step-up, with
  `?event_type=audit_read` showing both rails; invitation links carrying `#token=`; a second user 403 on both routes.
  `TestDemoAdminEmailGrantsPlatformAdmin` and `TestDemoAdminEmailRefusedInProduction` pin it.
- **From the tags (this PR).** auth-cms and production-baseline built at `GOWORK=off` from the repinned tree with an
  empty module cache (`all modules verified` in both); `go version -m` on auth-cms reads
  `pockets/authentication v0.30.0`, `pockets/authorization v0.31.0`, `pockets/authentication/views/goth v0.8.1` and
  `sdk v0.20.0`, and on the baseline `pockets/authentication v0.30.0`, `pockets/authorization v0.31.0`, stores/pgx
  v0.14.0 (authentication) and v0.21.0 (authorization) and `sdk v0.20.0`. Every example builds, vets and tests at
  `GOWORK=off` (the auth-cms server package three runs, all green); `pnpm build` in `workshop/documentation` succeeds.

**Owner drill (Verification §4, R-6 (a)): PASSED 2026-10-26** on the auth-cms build at `main` 32f22c0d (development,
console mailer, curl): `AUTH_DEMO_ADMIN_EMAIL` granted platform admin on verify; two invitations to `x@example.com` on
`project/demo` (member AutoAccept, owner not), both mail links carrying `#token=`; a second account added and confirmed
`x@example.com` with the sign-in claim and the member invitation was `accepted` at the confirm, the owner one stayed
`pending` and accepted by its token; an authenticator-app enrollment and TOTP step-up to AAL2, then
`/authorization/audit` and `/auth/admin/security-events` both 200 with `?event_type=audit_read` listing the reads.
**One finding, docs only:** `mine?identifier_kind=email` was empty for the second account, because it lists the primary
sign-in email (the account's own), not the added one — as row 9 says, but the auth-cms drill text promised the listing;
the README is corrected here and the merged listing stays deferred (below). The drill also surfaced up-to-30-second mail
latency in auth-cms (deferred below); auth-cms now sets the delivery runtime's idle interval to one second.

**Owner rulings.** Ratified 2026-10-26 ("ratified"): **R-1 (a)** per-fact HMAC tags + the Go-computed header MAC + epoch;
**R-4 (a)** the audit routes are JSON only; **R-5 (a)** the invitation landing page is a README checklist, no bundled page;
**R-6 (a)** the drill on the auth-cms build (console mailer + browser JSON), the Redis drill optional, a gps-360-go goredis
run at adoption; **R-7 (a)** `LinksConfig.InvitationURL`, required in production beside a Granter; **R-8 (a)** the current
verified holder resolves. **R-2** (a typed `WithAuditReadObserver`, never deduplicated) and **R-3** (`RefuseSelfTarget` +
`AllOf`; two-person approval is a README recipe) were decided by the two-reviewer gate (BE6, BE11). **Rev 1** (owner
direction 2026-10-26): an invitation is addressed to `(kind, identifier)`, and every verification of that identifier
resolves it, replacing the open-kind binding. **HIGH-1, owner ruling 2026-10-26 after the #211 review**, narrowing R-8: only
verified identifiers holding the sign-in/recovery claim (unique across users) resolve, sync, list under `mine` and match at
accept; notification-only identifiers never do, and enabling login or recovery on a verified identifier resolves its
invitations. **MEDIUM-3, lead ruling:** passwordless adoption resolves too. Lead rulings on #209 HIGH-1 (back off) and the
#210 rotation deviation are above.

**Known and deferred.**
- **`mine` lists the primary claim-holding identifier of a kind only.** A merged listing across identifiers needs a
  `ListBySubjects` store change (pgx + turso); it is a follow-up and an owner question. The owner drill hit it: an
  invitation addressed to an added (non-primary) sign-in email resolves and accepts but does not list; a React host
  calls `POST /auth/invitations/sync` and accepts by token until then.
- **The fenced delivery queue has no enqueue wake.** The ordinary queue signals its workers on enqueue; the fenced queue
  that carries authentication delivery does not, so an idle worker waits out `IdleInterval` (30 s by default) before
  sending. auth-cms sets one second; production hosts tune `JOBS_IDLE_INTERVAL` or wait for a jobs-pocket wake.
- **Duplicate `invitation_granted` rows under concurrency.** Two paths racing on one invitation for the same subject (a
  token accept, a verification, a sync) may each record and count a success row; the Granter's dedupe and the
  `member_added:<id>` key keep the grant and the notice single. Auditors dedupe on `operation_id`. The fix is a store
  change: `CompleteAcceptance` reports whether it moved the row, so only that caller records.
- **goredis bad-tag self-heal** (lead-deferred at #210): a set holding a bad tag fails closed (every read of it falls back)
  until the next full publication, bounded by `FullRebuildEvery`; documented, not repaired in place.
- **Account merge** (owner: "merge accounts at some point with double verification") is a future slice: an invitee with two
  existing accounts can only add the second identifier to one of them today. The plan's Notes list what a merge plan must
  settle (double verification, the survivor, re-pointing relations and `iam_audit` actor ids through the host, every
  credential and pending-invitation table, one transaction across pgx + turso + memory + `storetest`).
- **Fixed in passing:** the auth-cms README's invitation leg targeted `project/other`, which is not a registered resource;
  #212 points both invitations at `project/demo`, matching the drill that ran on the real binary.
- **Follow-ups** (the plan's Notes): the reverse listing ("who can `manage` R") and an effective-access listing; removing
  `InvitationTokenInQuery` in the next authentication minor; a goth admin page for both rails (R-4 (b)); retiring
  auth-cms `/debug/security-events` now that the bundled route exists; an actor filter on `securityevent.ListFilter` and
  an open-kind `Mine` (store changes); gps-360-go's adoption of `WithIntegrityKeys` + `FullRebuildEvery` at the roadmap's
  end.
- **Owed legs.** Docker Desktop hung on the host, so the baseline legs ran on a native PostgreSQL 17 instead of a
  container; no Linux-container `make test-stores` ran (no pgx/turso store changed; `live-stores` covered `main`).
- **Residuals.** A Redis writer can still deny (delete facts or keys, or plant a value of another type at the mirror key)
  and can replay a revoked fact from the current epoch until the next full publication; the integrity key lives in the
  host process, so a host compromise is out of scope; on an unkeyed mirror `!epoch_at` is trusted as stored. The audit
  routes expose actor and subject ids to anyone the host gate admits. Decline stays token-only, so a leaked link lets
  anyone decline (FE12, accepted). An open-kind invitation still accepts by its token alone (for a caller with a
  claim-holding verified identifier); the fragment keeps that token out of logs and `Referer`s.

## TOTP fallback and recovery codes (SOC 2 slice S4c — published 2026-10-26)

Ten annotated tags (not signed) on six modules, one of them NEW (`integrations/encoding/rsc-qr`).
Each was cut at its PR's squash-merge commit on `main`, pushed, THEN polled on the proxy, and
cold-verified from a scratch consumer (`GOWORK=off`, an empty module cache, `proxy.golang.org`,
`sum.golang.org`, `go mod verify`, `go build <module>/...`). No sdk tag (BE8: TOTP is an algorithm
with one consumer and lives in the pocket). Design and rulings:
[plans/s4c-totp.md](plans/s4c-totp.md) (rev 1, ratified 2026-10-25, EXECUTED 2026-10-26). Two
owner rulings during the build made both factor schemas conditional (0026, then S4b's 0025),
which added a second core train (PR-2d, v0.29.0) and a second store repin (PR-2e) before this
record, so the examples repin once. Under the owner's 2026-10-23 process ruling this section is
the whole record: there is no handoff file and no manifest. The handoff notes below say what a
host does on upgrade, and hosts adopt them once, at the roadmap's end. The operator surface is
the docs site: the authentication page's *TOTP and recovery codes*, *Assurance and MFA* and
*Persistence and views*, *Keys and rotation* (the TOTP ring, the recovery-code pepper), and
*Production posture* ("Added by S4c"). The pocket README's "TOTP and recovery codes (SOC 2 S4c)"
section is the reference.

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `integrations/encoding/rsc-qr/v0.1.0` (NEW module, first tag) | `3805c1a3` | #200 (PR-1) | OK; adapter + rsc.io/qr v0.2.0 + sdk v0.20.0 |
| 2 | `pockets/authentication/v0.28.0` | `4e14b835` | #201 (PR-2a) + #202 (PR-2b) | OK; set A |
| 3 | `pockets/authentication/stores/pgx/v0.13.0`, `pockets/authentication/stores/turso/v0.12.0` | `fda49a03` | #203 (PR-2c) | OK; set B |
| 4 | `pockets/authentication/views/goth/v0.8.0` (requires core v0.28.0) | `8c76ebee` | #204 (PR-3) | OK; set C |
| 2d | `pockets/authentication/v0.29.0` (0025 conditional) | `e2f541a4` | #205 (PR-2d) | OK |
| 4f | `ui/goth/v0.2.1`, `pockets/authentication/views/goth/v0.8.1` (follow-ups) | `92c37a59` | #206 | OK |
| 3e | `pockets/authentication/stores/pgx/v0.14.0`, `pockets/authentication/stores/turso/v0.13.0` | `5d3a2776` | #207 (PR-2e) | OK |

Order: 1 and the core PRs in parallel; 2 after #201 and #202 merged and the live legs ran again
on `main`; 3 after #203 and a `live-stores` dispatch; 4 after #204 and its browser leg; 2d after
the owner's second conditional-schema ruling (#205); 4f after the S4 presentation follow-ups
(#206: dark-theme link contrast, the dark destructive foreground, checkbox spacing, human dates on
the passkey and TOTP pages); 3e last (#207). #203 and #207 moved only the stores' core pin
(v0.26.0 → v0.28.0 → v0.29.0, explicit `GOWORK=off go get`, then tidy); #204 moved views/goth from
authentication v0.27.1 to v0.28.0 (forced: the page models changed). Every module ships MINOR
under the v0 convention; ui/goth v0.2.1 and views/goth v0.8.1 are PATCH. The tag messages name
the `methods` claim (row 3), the one AAL2-factor predicate (row 5), the host erasure obligation
(row 9) and migration 0026 (row 10, required only when TOTP is wired).

**The MVS sets.**
- **Set A** (step 2): authentication v0.28.0 beside the RELEASED stores pgx v0.12.0 / turso
  v0.11.0 and views/goth v0.7.0, TOTP unwired; no lockstep.
- **Set B** (step 3): core v0.28.0 + stores pgx v0.13.0 / turso v0.12.0 + pgxdb v0.14.0 + turso
  v0.9.0 selected sdk v0.20.0 and built.
- **Set C** (step 4): views/goth v0.8.0 + core v0.28.0 + ui/goth v0.2.0 + rsc-qr v0.1.0 +
  go-webauthn v0.1.0 + set B built an auth-cms-shaped `main`.
- **This PR (from the tags):** auth-cms and production-baseline at `GOWORK=off` from an EMPTY
  module cache over `proxy.golang.org` + `sum.golang.org`: `all modules verified` in both, and
  both binaries built (see the verification record).

**Not tagged.** sdk, go-webauthn, `pockets`, `workshop/gopernicus` (no new refusal carries a
waiver, so guard P5 has nothing to print), the examples, and every other module. Nothing
retracts. v0.28.0, the stores' v0.13.0 / v0.12.0 and views v0.8.0 stay published; hosts pin the
versions below.

**Upgrade notes.** Sixteen rows. No compile-time break; one boot refusal, only for a host that
wires TOTP without its schema (row 10, amended); one boot refusal RELAXED (row 15: S4b's every-mode
0025 refusal becomes passkeys-wired only); row 3 changes a decision only under a non-empty
`Methods`; row 5 tightens a gate for TOTP holders (none exist before the upgrade); rows 13 and 14
change what an unwired host's stores read and erase. The "Read first" box and rows 1–12 with the
paragraph under them are the plan's, verbatim, except row 10, amended for the owner's
2026-10-25 ruling (the plan's text was "**REQUIRED migration 0026** | BOOT FAILURE without
`totp_credentials` + `recovery_codes`, naming 0026, every mode"); the pin bullet in the box and
rows 13–16 (PR-2d, the second ruling) are appended.

> **Read first:** `grep -rnE 'MFAConfig\{|RequireMFA\(|EraseRepository|KeysInput\{' --include='*.go'`; apply 0026 before repinning
> a store, TOTP wired or not. **By host shape** (PR-4 expands each): every store host → rows 10, 3, 9; IdP-only → row 10
> (and row 2 if it sets `Methods`); hosts turning TOTP on → rows 1, 4–8, 11; passkey hosts → rows 3, 5, 11 (the chooser page).
> - **Pins (appended):** pin **authentication v0.29.0**, stores **pgx v0.14.0 / turso v0.13.0**, **views/goth v0.8.1**
>   (requires authentication v0.28.0+) and **ui/goth v0.2.1**; **rsc-qr v0.1.0** only for a host that renders the QR through
>   views/goth. Since v0.29.0, "apply 0026 before repinning, TOTP wired or not" is advice, not a requirement: 0026 and 0025
>   are each required only when their factor is wired (rows 10 and 15).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | new adapter `rscqr.Modules` | additive | none | rsc-qr@v0.1.0 |
| 2 | `MFAConfig.Methods`; `mfa_required_denied` `reason`/`method` | additive; empty = any source; an unwired source → `ErrMFAUnsatisfiable` | leave empty | authentication@v0.28.0 |
| 3 | access tokens carry `methods`; `Credential.Methods`; delegated tokens refuse it | pre-upgrade AAL2 tokens fail closed under a non-empty `Methods` until refresh | leave `Methods` empty through the rollout | authentication@v0.28.0 |
| 4 | `WithTOTP`, `Repositories.TOTP`, the routes, `/auth/methods` `assurance`/`totp`/`recovery_codes`; `admins` satisfiable by TOTP; zero `Required` → `admins` with TOTP + `UserAdminCheck` | opt-in; a host wiring TOTP beside a `UserAdminCheck` gets the `admins` default (`source=default`) | `Required: MFARequiredNone` | authentication@v0.28.0 |
| 5 | one AAL2-factor predicate on both gates | a TOTP holder adds/removes a passkey only at AAL2, and the reverse | none | authentication@v0.28.0 |
| 6 | a TOTP/recovery step-up re-mints the session at AAL2 (R-3) and is a recent login for 5 min | new AAL2 source on every `RequireMFA()` gate | `Methods: [passkey, idp]` | authentication@v0.28.0 |
| 7 | `NewKeys`: `KeysInput.TOTPKeys`/`TOTPActiveKey`, `Keys.TOTPRing`, `KeyTOTPRing` | additive; production refuses an unset ring only when `KeyTOTPRing` is needed | do not pass `KeyTOTPRing` | authentication@v0.28.0 |
| 8 | seven event types; `mfa_change_notice`; the recovery-code key WARN | new audit rows and notices | override the template | authentication@v0.28.0 |
| 9 | erasure deletes TOTP + recovery codes (`EraseCounts.TOTP`, `.RecoveryCodes`); a host `EraseRepository` must, or TOTP secrets survive erasure | host-store obligation; `RunTOTP` pins it | implement per `RunTOTP` | authentication@v0.28.0 |
| 10 | **migration 0026 ships; REQUIRED ONLY WHEN TOTP IS WIRED** (owner ruling 2026-10-25, amending BE12) | with `WithTOTP`, BOOT FAILURE without `totp_credentials` + `recovery_codes` (`ErrTOTPSchemaMissing`, naming 0026, probed through the optional `totp.SchemaProber`); TOTP unwired, the tables are never probed, read or erased and boot proceeds without them | apply 0026 (hosts may apply the empty tables ahead of time); ledger copy (P9) | stores pgx@v0.13.0, turso@v0.12.0 |
| 11 | `TOTPViews`; `WithQR`; the chooser page is the `Browser()`+`RequireMFA()` redirect target when TOTP views are wired; `TOTP *TOTPLinks` on three page models | additive; passkey-only hosts keep `/auth/step-up/passkey` | leave `TOTPConfig.Views` nil | views/goth@v0.8.0 |
| 12 | auth-cms wires TOTP (ring from env, `rscqr.Modules`, views); baseline ledger 0033 | example behaviour | — | examples (untagged) |
| 13 | `user.EraseInput.Passkeys`: a host `EraseRepository` deletes and counts passkeys only when it is true (false when passkeys are unwired, when 0025 may be absent) | host-store obligation; `storetest.RunPasskeys` `EraseLeavesPasskeysWhenUnwired` pins it; a host that unwires passkeys stops erasing `passkey_credentials` | purge the table before unwiring | authentication@v0.29.0 |
| 14 | `credential.ScopedSnapshotter` / `SnapshotScope` (optional, no compile break) | passkeys unwired, the pocket reads the credential rail without passkeys (`SnapshotScoped`, `Passkeys: false`); a store without it gets `Snapshot` with its `Passkeys` dropped. Unwired passkeys no longer count as login methods | none | authentication@v0.29.0 |
| 15 | `passkey.SchemaProber` / `ErrPasskeySchemaMissing`: 0025 ships, required only when `WithPasskeys` is wired; `Repositories` (pgx/turso) no longer refuses a database without it (**relaxes S4b row 12**) | an unwired host boots without `passkey_credentials`; a passkey host without it refuses, naming `0025_passkey_credentials.sql` | apply 0025 when turning passkeys on | authentication@v0.29.0, stores pgx@v0.14.0, turso@v0.13.0 |
| 16 | `totp.Confirmation.Bootstrap` is false whenever passkeys are unwired | a TOTP confirm on a passkey-less host never reads the passkey table for its bootstrap check | none | authentication@v0.29.0 |

No compile-time breaks. Row 10 is a boot failure; row 3 changes a decision only under a non-empty `Methods`; row 5 tightens a
gate for TOTP holders (none exist before upgrade); rows 4 and 6 are opt-in. Every module ships MINOR under v0; tag messages name
rows 3, 5, 9 and 10. **Not touched:** sdk (BE8), `Mutation`/`MethodSet` (BE1 resolved in the aggregate), `passkey.js`.

Build-time deviations a host can see, folded here (rows 1–9, 11 and 12 stay verbatim):
- **Row 1.** `rscqr.Modules` returns the smallest version that fits at level M, `true` = dark,
  with **no quiet zone** (the renderer draws the four-module border); empty text or text over
  2331 bytes is `sdk.ErrInvalidInput`. rsc.io/qr always uses mask 0: valid QR, not
  penalty-optimal; the owner's device check is the scanning proof.
- **Row 4.** `ErrTOTPReposRequired` also requires a `ChallengeProtector` (recovery codes are
  digested under its pepper); `TOTPConfig.Issuer` is required with `Encrypter` and may not
  contain a colon. The budget field is `AuthenticationLimits.SecondFactor` (zero → 10/min per
  user, 30/min per IP). A host that never calls `WithTOTP` gets 404 on every TOTP route, and its
  `/auth/methods` gains only the additive `assurance`.
- **Row 9.** Erasure of the TOTP tables is gated on the new `user.EraseInput.TOTP`, set only when
  TOTP is wired (the owner's first ruling): a host that unwires TOTP stops erasing
  `totp_credentials` and `recovery_codes`, so purge them first. With TOTP wired, `EraseUser`
  logs an ERROR when a credential survives the erase (a store ignoring the flag).
  `storetest.RunTOTPSchemaProbe` pins a store's probe beside `RunTOTP`.
- **Row 10.** The stores' every-mode `Repositories` probe never checks 0026; the pocket calls
  `totp.SchemaProber` only under `WithTOTP`. Turso's twin is the same file name. The baseline
  ledger copy is `0033_totp.sql`, byte-identical to pgx 0026 (G34). `recovery_codes` has
  `UNIQUE (user_id, code_digest)` (it also serves the per-user lookups) and a `key_id` index;
  the planned separate `user_id` index was dropped.
- **Row 11.** The manage page offers "Continue setup" and "Cancel setup" while an enrollment is
  pending; cancelling signs nobody out (N3). The README's "Custom TOTP views" carries the markup
  contract and the `html/template` `#ZgotmplZ` note for `otpauth:` links.
- **The PR-2a code review** (lead-backend, folded into #201 before merge): **B1 (blocker)**
  `Confirm` is fenced on the pending secret and session it checked, so a concurrent
  re-enrollment is never confirmed by the first code; **H1** `MFAConfig.Methods` also binds the
  second-factor add/remove gate (under a restriction only allowed sources count as held, so a
  TOTP-only user under `[passkey]` bootstraps a passkey); **M1** `RecordUse` refuses while
  locked; **M2** a weaker step-up lowers assurance (the lead's amendment of BE6); **M3**
  `RunTOTP` gained the concurrency and contract cases; **M4** the erasure README note and the
  surviving-credential ERROR; NITs N1 (`KeyURI` uses `%20` and escapes `:` in the account), N2 (a
  recovery code resets the failure count), N3 and N8 (the step-up redirect honours the route
  prefix). N4–N7 and N9 are deferred (below).

**Handoff notes — what a host does on upgrade, by host shape and module** (applied at the
roadmap's end). Pins: sdk v0.20.0 (unchanged); authentication **v0.29.0**; stores pgx
**v0.14.0** / turso **v0.13.0**; views/goth **v0.8.1**; ui/goth **v0.2.1**; rsc-qr v0.1.0 only
for a host that renders the QR through views/goth. Repin with explicit `GOWORK=off go get
<module>@<tag>`, then `GOWORK=off go mod tidy`.
- **Every host that repins stores** (rows 10, 15, 3, 9, 13, 14).
  1. Nothing new is required at boot unless the host wires a factor: with stores pgx v0.14.0 /
     turso v0.13.0 the store constructors probe neither `passkey_credentials` (0025) nor the TOTP
     tables (0026). Both migrations still ship; a host may copy them into its ledger as the next
     numbers ahead of time (the production-baseline copies are `0032_passkey_credentials.sql` and
     `0033_totp.sql`, byte-identical; guard P9 and G34 check the copies). 0026 is two new, empty
     tables (`SET LOCAL lock_timeout = '5s'`, `CREATE TABLE IF NOT EXISTS`, `IF NOT EXISTS`
     indexes, no foreign keys): no `CONCURRENTLY`, no backfill; apply it in its own runner
     invocation like any additive migration.
  2. Row 3: access tokens now carry `methods` beside `aal`; a delegated token carrying it is
     refused. Nothing changes unless the host sets `MFAConfig.Methods` (leave it empty through
     the rollout: a pre-upgrade AAL2 token has no source and is denied under a restriction until
     it refreshes).
  3. Rows 9 and 13: a host-implemented `EraseRepository` honours `EraseInput.TOTP` and
     `EraseInput.Passkeys`, deleting and counting those rows only when the flag is true; run
     `storetest.RunTOTP` and `storetest.RunPasskeys` against it. Unwiring a factor stops its
     erasure: purge the tables first.
  4. Row 14: a host-implemented `MutationRepository` may add `SnapshotScoped`; without it the
     pocket drops `Passkeys` from `Snapshot` when passkeys are unwired. No compile break.
- **IdP-only and OAuth-only hosts** (the production-baseline shape, and the owner's
  gps-360-go). **Nothing to wire.** Neither 0025 nor 0026 is required: with neither factor wired
  the pocket never probes, reads or erases those tables. A host may apply them ahead of time so a
  factor can be turned on later (production-baseline does). Proven from the tags below: the
  baseline binary boots on a ledger through 0031 with neither table present. Row 2 only if the
  host sets `Methods` (an IdP-only host may set `[idp]`; `passkey` or `totp` there is
  `ErrMFAUnsatisfiable`).
- **Hosts turning TOTP on** (rows 1, 4–8, 10, 11; the pocket README's "Turn on TOTP").
  1. Apply 0026 (above); `WithTOTP` refuses boot without it (`ErrTOTPSchemaMissing`).
  2. Keys: add `KeyTOTPRing` to `NewKeys`. `AUTH_TOTP_KEYS` is a ring `id:hex,id:hex` (each key
     exactly 32 bytes, `openssl rand -hex 32`) and `AUTH_TOTP_ACTIVE_KEY` names the sealing id;
     development mints a one-key `ephemeral` ring with a WARN (enrolled apps stop working on
     restart), production refuses it unset. Build the encrypter with
     `cryptids.NewAESGCMRing(keys.TOTPRing)`. Rotation: add the new key, make it active, keep the
     old id in the ring (secrets are not re-sealed). Recovery codes live under the challenge
     pepper: **regenerate them before dropping a pepper key** (the boot WARN names orphaned key
     ids).
  3. `authentication.WithTOTP(TOTPConfig{Encrypter: ring, Issuer: "Your product", Views:
     authViews})` with `Repositories.TOTP`, `CredentialMutations`, `AuthenticationGrants` and a
     `ChallengeProtector`. With a `UserAdminCheck`, the zero `MFAConfig.Required` resolves to
     `admins` (boot line `mfa_required=admins source=default … sources=totp methods=any`); the
     sole-admin break-glass is `MFARequiredNone` and a redeploy; the operator reset is `POST
     /auth/admin/users/{id}/totp/reset` from an AAL2 session or an admin API key.
  4. `MFAConfig.Methods` (`passkey`, `totp`, `idp`; recovery codes count under `totp`) restricts
     the AAL2 sources every MFA gate admits; empty admits any. A host that refuses a possession
     code composed onto a passwordless or OAuth primary sets `Methods: [passkey, idp]`.
  5. **Server-rendered (views/goth):** `authgoth.New(bundle, authgoth.WithQR(rscqr.Modules))`
     (the views module imports no encoder; without `WithQR` the enroll page shows the key and the
     `otpauth://` link only) and `TOTPConfig.Views: authViews`. With TOTP views wired, the
     "Confirm it's you" chooser at `GET /auth/step-up/mfa` replaces the passkey page as the
     `Browser()` + `RequireMFA()` redirect target; it offers, filtered by `Methods`, the passkey
     control, the authenticator code, a recovery code, or the setup links. No new script.
  6. **SPAs: the JSON contract.** `POST /auth/totp/enrollment` → `{secret, otpauth_uri, issuer,
     account, algorithm, digits, period, expires_at}` (render the QR from `otpauth_uri`; the only
     response carrying the secret); `POST /auth/totp/enrollment/confirm {code}` → 201
     `{enrolled_at, recovery_codes, generated_at}`, shown once; `POST /auth/step-up/totp` and
     `/auth/step-up/recovery-code {code}` answer as `POST /auth/login` plus `assurance` (bearer
     callers get the new pair); `GET`/`DELETE /auth/totp` (removal revokes every session, bearer
     tokens included); `GET`/`POST /auth/recovery-codes` with `expected_generated_at` (409
     `recovery_codes_stale`); 429 `too_many_attempts` with `Retry-After`. On an MFA 401 read `GET
     /auth/methods` (`assurance`, `totp`, `recovery_codes`) to pick the step-up. Cookie callers
     send the double-submit CSRF header; bearer SPAs skip it.
  7. Row 6 / BE9: one code is a recent AAL2 login for every gate for five minutes, removal and
     regeneration included; the `mfa_change_notice` is the detective control.
- **Passkey hosts** (rows 3, 5, 11, 15). Row 5: a TOTP holder adds or removes a passkey only at
  AAL2, and the reverse. Row 15: 0025 stays required (now via `passkey.SchemaProber`, refusing
  with `ErrPasskeySchemaMissing`). Without TOTP views nothing about the step-up page changes.
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on the S4c tags.
  Pins moved (explicit `GOWORK=off go get`, then `GOWORK=off go mod tidy`): `auth-cms`
  authentication v0.27.1→v0.29.0, views/goth v0.7.0→v0.8.1 and ui/goth v0.2.0→v0.2.1 (rsc-qr
  v0.1.0 was already pinned by #204); `cms`, `goth-showcase` and `minimal` ui/goth
  v0.2.0→v0.2.1; `external-identity` authentication v0.27.1→v0.29.0; `production-baseline`
  authentication v0.27.1→v0.29.0 and stores/pgx v0.12.0→v0.14.0. `jobs-minimal` pins no moved
  module. No non-S4c pin moved. The code-side example changes landed with #201, #202 and #204
  (auth-cms wires TOTP; the baseline ledger gained 0033).

**The owner device check (Verification §4)**, on the owner's own auth-cms build.
- **Google Authenticator: PASS** — scanned the QR, enrolled, and stepped `/demo/mfa/page` up so
  `/demo/mfa` answered `aal2`.
- **Apple Passwords: inconclusive** — it attaches the codes to a saved website login instead of
  creating an issuer-named entry; deferred by the owner.
- **Other apps** (1Password and the rest), the `otpauth://` link handed to Apple Passwords on the
  Mac, and a per-theme device pass: **unverified.** The QR itself decoded to the page's exact
  `otpauth://` URI in Chromium and WebKit, light and dark (#204's browser leg).

**Verification record.**
- CI `check`, `vuln` and the CodeQL analyses passed on the final run of every PR (#200–#207). `make guard` runs
  thirty-six guards; S4c adds none (G13, G34 and G35 cover the new module and the ledger copy).
- **Cold verification:** every tag above; sets A, B and C as listed; this PR's empty-cache
  from-the-tags builds.
- **Live legs** (counted with `go test -json`, pass / fail / skip). Before step 2, on `main`:
  authentication `stores/pgx` **480 / 0 / 1** bare and schema-qualified (S4b baseline 453 / 0 /
  1; the known non-C collation skip), `stores/turso` **785 / 0 / 0** (758 / 0 / 0), and
  `live-stores` green; #202 also ran a Linux-container `make test-stores` (exit 0) and #203 a
  fresh PG17 + libSQL `make test-stores` (18 legs ok). PR-2d (#205): pgx **485 / 0 / 1** in both
  legs on a native Homebrew PostgreSQL 17 (+6 new, −1 retired), `TestLive_RetentionUpgrade`
  passing; turso **790 / 0 / 0 observed**, its log lost to a host disk-full event; a fresh PG17
  migrated through 0024 booted the store and pocket with both factors unwired (a password change,
  `/auth/methods` and an erasure succeeding) and refused with `WithPasskeys`, naming 0025;
  `live-stores` green on the branch, and on `main` before v0.29.0 and the store tags (#207).
- **§1, the real auth-cms binary over HTTP** (#201, development): **28/28** — enrollment and a
  replaced pending enrollment; a wrong code leaving the pending row; confirm and its replay
  refused; a step-up to `aal2` with `methods=[password totp]`; the lockout and its recovery after
  302 seconds; the AAL2 gates on removal and passkey registration; a recovery code, its reuse
  refused, and regeneration; 28.8 KB of server output scanned for secrets, codes and ciphertext
  with no hit. The `Methods` restriction and the admin reset are covered by host tests (the
  restriction has no env tag; auth-cms's only admin is boot-seeded).
- **§2, the browser leg** (#204, scratch Playwright, never committed): Chromium and WebKit ×
  light and dark, **36/36** — the AAL1 redirect to the chooser, enrollment, **the QR screenshot
  decoding (jsQR) to the page's exact `otpauth://` URI in all four runs** with a white quiet zone
  in dark, a reload keeping the secret, a wrong code 401 with the same secret, ten recovery
  codes, a chooser code → `aal2`, an upper-cased recovery code → `aal2`, regeneration without
  overlap and the old codes refused, removal signing out. CSP: zero violations in Chromium
  (WebKit's per-run hits are Playwright's screenshot styles; zero on a no-screenshot rerun); axe
  zero serious/critical in light. The dark-theme contrast hits it found were fixed by #206
  (serious/critical dark 1–7 per page → 0).
- **§3, from the tags (this PR).** auth-cms built at `GOWORK=off` from the repinned tree with an
  empty module cache (`all modules verified`); `go version -m` reads
  `pockets/authentication v0.29.0`, `pockets/authentication/views/goth v0.8.1`, `ui/goth v0.2.1`,
  `integrations/encoding/rsc-qr v0.1.0` and `sdk v0.20.0`. A scratch Playwright check (headless
  Chromium) against that binary in development (in-process delivery so the verification code
  reaches the console log at once): **14/14 PASS** — register and verify through the forms, sign
  in, `/demo/mfa` 401 with `insufficient_user_authentication` and `/demo/mfa/page` → the chooser,
  enrollment PRG to the enroll page, **the QR decoded with jsQR equal to the page's `otpauth://`
  URI** (issuer `Gopernicus CMS`, the page's secret), confirm with a code computed in Node from
  the secret → ten recovery codes, a chooser TOTP step-up back to `/demo/mfa/page` and
  `/demo/mfa` `aal2`; a fresh AAL1 sign-in in a second context, a recovery-code step-up →
  `aal2`; zero CSP violations; console errors only the two expected AAL1 401 fetches.
  **production-baseline without Docker** (Docker Desktop hung on the host): built at `GOWORK=off`
  (authentication v0.29.0, stores/pgx v0.14.0, sdk v0.20.0); a fresh Homebrew PostgreSQL 17.4
  cluster; its own runner applied the ledger **through `0031_security_events_retention.sql`
  only** (30 rows in `schema_migrations`; `0032_passkey_credentials.sql` and `0033_totp.sql`
  skipped, `passkey_credentials`, `totp_credentials` and `recovery_codes` absent); the server
  booted in development (`auth: mfa posture mfa_required=admins source=config
  gates=/auth/admin/users* sources=idp methods=any`) and `GET /healthz` answered **200**: an
  IdP-only host needs neither table. The plan's "boots on ledger 0033, refuses with
  `recovery_codes` dropped" leg no longer applies (row 10 amended: the baseline wires no TOTP);
  the wired refusal is pinned by `TestTOTPSchema_Postgres` and `RunTOTPSchemaProbe`. Every
  example builds, vets and tests at `GOWORK=off`; `make guard` and `make check` pass on the
  repinned tree; `pnpm build` in `workshop/documentation` succeeds.

**Owner rulings.** Ratified 2026-10-25 ("ratified"), R-1–R-5 at the recommendations: **R-1 (a)**
split — native `all` and the enrollment grace go to `s4c2-native-all`; **R-2 (a)** the rsc.io/qr
adapter behind a views/goth `WithQR` hook; **R-3 (a)** a TOTP or recovery-code step-up is AAL2
on any live first-party session, one predicate for every gate, the residual excludable with
`Methods`; **R-4 (a)** stdlib RFC 6238 in the pocket; **R-5 (a)** disabling TOTP revokes every
session. Owner note: every factor is host opt-in (an OAuth-only host like gps-360-go wires
none). **2026-10-25, first conditional-schema ruling** (amends BE12 / row 10): 0026 still ships,
but is required only when TOTP is wired; the pocket probes through an optional interface only
under `WithTOTP`; erasure and the admin reset skip TOTP when it is not wired. **2026-10-25,
second ruling:** the same rule for S4b's 0025 (`passkey.SchemaProber`; erasure skips passkeys
when unwired), shipped as PR-2d before this record so the examples repin once. **Lead amendment
of BE6** after the PR-2a review (M2): a step-up that proves less than the session holds lowers
assurance, and `mergeMethods` drops carried AAL2 methods below AAL2. Lead-accepted in PR-2d:
unwired passkeys no longer count as login methods (they cannot be used, so counting them could
let the policy remove the last usable method). Apple Passwords' behaviour: deferred by the owner.

**Known and deferred.**
- **To `s4c2-native-all`** (the plan's Notes handoff): native `Required: all` (deny below AAL2
  on every first-party route but an allowlist of the step-up, TOTP, passkey-registration,
  recovery-code, methods, me, logout and refresh routes), the enrollment grace
  (`max(EnrollmentStart, user.CreatedAt) + EnrollmentGrace`, 7-day default, 30-day maximum; 403
  `mfa_enrollment_required`; refresh re-checks) and the admin re-open through a 0027 per-user
  marker, with `holdsAAL2Factor` as the enrollment test.
- **To S4d.** API keys under MFA gates; `aal`/`methods` on delegated tokens; re-sealing TOTP
  secrets on ring rotation.
- **From the PR-2a review:** N4, N5, N6, N7 and N9 (nits, deferred at #201).
- **Follow-ups.** SHA-256 TOTP once Google Authenticator honours it; an escalating lockout
  replacing the fixed 5 × 5 minutes; a retention job for abandoned pending enrollments
  (`expires_at`); a committed Playwright spec; Apple Passwords and the other authenticator apps
  on real devices.
- **Cosmetic.** With `data-theme=dark` the ui/goth page background outside the card stays white.
  Pre-existing `gofmt` drift in three untouched ui/goth primitives.
- **Housekeeping.** README / `ARCHITECTURE.md` module rows still missing: auth0, firebase,
  external-identity, ui/goth, goth-showcase, and views/goth in the layout tree (#206 added
  go-oidc).
- **Owed legs.** Docker Desktop hung on the host from PR-2d on, so the local Linux-container
  `make test-stores` for #205 and #207 was replaced by `live-stores` dispatches (Linux CI,
  Postgres + turso), and this PR's from-the-tags baseline boot ran on a native Homebrew
  PostgreSQL 17 instead of a container. PR-2d's turso count is observed, not logged.
- **Residuals.** R-3(a) composes AAL2 from a passwordless or OAuth primary plus a possession
  code (strictly two possession factors), excludable with `Methods`. One code is a recent AAL2
  login for five minutes, so a stolen unlocked session that reads one code can disable TOTP; the
  notices are the detective control. Retiring a pepper key kills recovery codes digested under
  it (the WARN and the regenerate-first step). Clock skew beyond ±30 s fails every code.

## Passkeys (SOC 2 slice S4b — published 2026-10-25)

Eight annotated tags (not signed) on seven modules, one of them NEW. Each was cut at its PR's
squash-merge commit on `main`, pushed, THEN polled on the proxy, and cold-verified from a
scratch consumer (`GOWORK=off`, an empty module cache, `proxy.golang.org`, `sum.golang.org`, `go
mod verify`, `go build <module>/...`). Every tag resolved on the first poll it was checked on. Design and rulings:
[plans/s4b-passkeys.md](plans/s4b-passkeys.md) (rev 2, ratified 2026-10-24) and its addendum
[plans/s4b-device-check-fixes.md](plans/s4b-device-check-fixes.md) (ratified 2026-10-25), which
inserted two core patches (v0.27.0, v0.27.1) before the views tag after the owner's device check
failed. Under the owner's 2026-10-23 process ruling this section is the whole record: there is
no handoff file and no manifest. The handoff notes below say what a host does on upgrade, and
hosts adopt them once, at the roadmap's end. The operator surface is the docs site: the
authentication page's *Passkeys* and *Assurance and MFA*, the sdk authentication page's
*WebAuthn (passkeys)*, and *Production posture* ("Added by S4b"). The pocket README's "Passkeys
(SOC 2 S4b)" section is the reference.

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `sdk/v0.20.0` | `bf671b27` | #191 (PR-1) | OK, poll 1; the `webauthn` and `webauthntest` tests pass from the cache |
| 2 | `integrations/cryptids/go-webauthn/v0.1.0` (NEW module, first tag) | `d24a6f6c` | #192 (PR-2) | OK, poll 1; adapter + sdk v0.20.0 + go-webauthn v0.18.2 verified, built and tested from the cache |
| 3 | `pockets/authentication/v0.26.0` | `2f029830` | #193 (PR-3a) + #194 (PR-3b) | OK, poll 1; set A (below) |
| 4 | `pockets/authentication/stores/pgx/v0.12.0`, `pockets/authentication/stores/turso/v0.11.0` | `778bc45b` | #194 + #195 (PR-3c) | OK, poll 1; set B |
| 3d | `pockets/authentication/v0.27.0` | `ca2433ae` | #197 (PR-3d) | OK, poll 1; beside stores pgx v0.12.0, turso v0.11.0 and views v0.6.0 |
| 3e | `pockets/authentication/v0.27.1` | `c08d569a` | #198 (PR-3e) | OK; also inside set C |
| 5 | `pockets/authentication/views/goth/v0.7.0` (requires core v0.27.1) | `ec69b0b0` | #196 (PR-4) | OK, poll 1; set C |

Order: 1; then 2 and the core PRs; 3 after #193 and #194 merged and the live legs ran on `main`;
4 after #195 and a `live-stores` dispatch; 3d and 3e after the device check (addendum); 5 last,
rebased onto `main` after each core tag. #193 moved the core's sdk pin to v0.20.0 by explicit
`GOWORK=off go get`; #195 moved both stores to core v0.26.0 and sdk v0.20.0 (pins only); #196
moved views/goth from authentication v0.14.0 to v0.27.1 (forced: the page models changed), so
views/goth v0.7.0 lifts a host to v0.27.1 by MVS. Every
module ships MINOR under the v0 convention, v0.27.1 a PATCH. The tag messages name the access
token's `aal` claim (row 3), the `RequireMFA()` lift (row 4), the `MFARequiredNone` value change
(row 6), the host `MutationRepository` obligation (row 8) and the required migration 0025 (row 12).

**The MVS sets.**
- **Set A** (step 3): authentication v0.26.0 beside the RELEASED stores pgx v0.11.0 / turso
  v0.10.0 and views/goth v0.6.0 built with passkeys unwired. MVS selected sdk v0.20.0; no
  lockstep.
- **Set B** (step 4): core v0.26.0 + stores pgx v0.12.0 / turso v0.11.0 + pgxdb v0.14.0 + turso
  v0.9.0 selected sdk v0.20.0, verified and built.
- **Set C** (step 5): views/goth v0.7.0 + core v0.27.1 + ui/goth v0.2.0 + go-webauthn v0.1.0 +
  stores pgx v0.12.0 / turso v0.11.0 + pgxdb v0.14.0 + turso v0.9.0 built an auth-cms-shaped
  `main`; `all modules verified`.

**Not tagged.** `ui/goth`, `pockets`, `workshop/gopernicus` (neither new refusal carries a
waiver, so guard P5 has nothing to print), the examples, and every other module. Nothing
retracts. v0.26.0 and v0.27.0 stay published; hosts pin v0.27.1.

**Upgrade notes.** Seventeen rows. Two are compile-adjacent breaks (row 6, a constant's value;
row 8, host stores must cover new variants), one is a boot failure in every mode (row 12), row 17
changes a decision without a compile error, and the rest are opt-in or additive. The "Read first"
box and rows 1–14 with the paragraph under them are the plan's, verbatim; the pin bullet in the
box and rows 15–17 (the addendum's two patches) are appended.

> **Read first:** `grep -rnE 'MFARequiredNone|MutationRepository|RequireMFA\(' --include='*.go'`; apply 0025 before
> repinning a store. **By host shape** (PR-5 expands each):
> - **Every host that repins stores:** row 12 (apply 0025 first), rows 3 and 4; row 8 with a custom `MutationRepository`.
> - **IdP-only hosts** (the production-baseline shape): row 12 only; 0025 is required with passkeys unwired.
> - **Hosts turning passkeys on:** rows 2, 5–7, 9–11, 13 and the README "Turn on passkeys" recipe.
> - **Pins:** rows 3–11 name where each change first shipped; pin **authentication v0.27.1** (not v0.26.0), which
>   carries rows 15–17, and **views/goth v0.7.0**, which requires it.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `webauthn` port, `webauthntest`, `OriginPolicy.Allows`/`Domains` | additive | none | sdk@v0.20.0 |
| 2 | new adapter `gowebauthn.New(webauthn.Config)` | `Validate` failures refuse boot in every mode | fix RPID/Origins | go-webauthn@v0.1.0 |
| 3 | access tokens carry `aal`; native `Credential.Assurance` is aal1/aal2 | pre-upgrade tokens read `unknown` until refresh; a delegated token carrying `aal` is refused | none | authentication@v0.26.0 |
| 4 | `RequireMFA()` beside access tokens no longer panics | AAL1 tokens get 401 + challenge; `FreshAuth`/`VerifiedEmail`/`Tenant`/`Check` still panic | `Accept(...)` | authentication@v0.26.0 |
| 5 | `WithPasskeys(PasskeyConfig{…, Views})`, `Repositories.Passkeys`, the JSON routes, `PublicRouteLimits.Passkey`, `/auth/methods` `passkey`; `admins` satisfiable by passkeys | opt-in; production refuses a `localhost`/IP RP or an HTTP namespace; S4a's "needs WithExternal" refusal now boots | leave unwired | authentication@v0.26.0 |
| 6 | **BREAKING value:** `MFARequiredNone` = `"none"`; zero resolves once (B-2) | passkeys + `UserAdminCheck` → `admins`; boot line `source=default` | `Required: MFARequiredNone` | authentication@v0.26.0 |
| 7 | `admins` gates the machine lifecycle with passkeys (B-3) | AAL1 key minting → 401 after `MachineRoutesGate` | `MFARequiredNone` (drops the admin gate too) | authentication@v0.26.0 |
| 8 | `MethodSet.Passkeys`; `RemovePasskey`/`RemovePasskeys`; erasure deletes passkeys (`EraseRepository`, `EraseCounts`) | a host `MutationRepository` must snapshot and apply them, or it undercounts login methods (conservative) and cannot remove passkeys | implement per `RunPasskeys` | authentication@v0.26.0 |
| 9 | removing a passkey revokes its user's sessions; a UV-passkey holder adds/removes only at AAL2 | sign-out everywhere, as a password removal does | none | authentication@v0.26.0 |
| 10 | six event types; `mfa_required_denied` gains `access_token`/`machine`; `passkey_change_notice` | new audit rows and notices | override the template (`AppTemplates`) | authentication@v0.26.0 |
| 11 | `PasskeyViews`; page-model `Passkeys`; `Browser()`+`RequireMFA()` redirect | additive; the `Views` interface is unchanged | leave `PasskeyConfig.Views` nil | authentication@v0.26.0 |
| 12 | **REQUIRED migration 0025** | BOOT FAILURE without `passkey_credentials`, naming 0025, every mode | apply 0025; ledger copy (P9) | stores pgx@v0.12.0, turso@v0.11.0 |
| 13 | passkey pages; `PasskeyScriptHandler` | mount the script, or the passkey controls stay hidden | do not mount | views/goth@v0.7.0 |
| 14 | auth-cms wires passkeys (admins by default; RP from its origin; login markup); baseline ledger 0032 | example behaviour | — | examples (untagged) |
| 15 | the CSRF double-submit cookie follows the session cookie's `Secure`: a development host with a non-Secure session cookie gets `auth_csrf` (no `Secure`, no `__Host-`), which WebKit stores on `http://localhost`; production refuses it twice, independently | Safari over local HTTP stops 403-ing every JSON CSRF mutation; Secure hosts are byte-for-byte unchanged; SPAs keep reading `csrf_token` from the `GET /auth/csrf` body | none | authentication@v0.27.0 |
| 16 | `POST /auth/passkeys/registration/options` accepts `{"user_verification":"required"}`; a required ceremony leaves presence-only passkeys out of `excludeCredentials` (upgrade in place) | additive; `PasskeyConfig.UserVerification` stays the host toggle (`preferred` default) | send `{}` | authentication@v0.27.0 |
| 17 | a passkey step-up always requires user verification | a presence-only response to a step-up is refused (401 `passkey_rejected`, `uv_required`) instead of answering `aal1`; sign-in keeps the host setting | none | authentication@v0.27.1 |

Two compile-adjacent breaks: row 6 (a constant's value changes) and row 8 (host stores must cover the new variants). Row 12
is a boot failure. Every module ships MINOR under v0; tag messages name rows 3, 4, 6, 8 and 12.

Build-time deviations a host can see, folded here (the table stays verbatim):
- **Row 2.** `Config.Validate` also refuses an IP RP ID (browsers never accept one), so the
  pocket's production "or an IP" refusal is unreachable through a real adapter. Begin refuses an
  empty or over-64-byte user handle and an unknown `Requirement` (`sdk.ErrInvalidInput`); a
  positive `Timeout` under one millisecond is refused. Cross-origin (iframe) ceremonies are
  refused.
- **Row 5.** `ErrPasskeyReposRequired` also requires `Repositories.AuthenticationGrants`;
  `PasskeyConfig.Views` requires `BrowserConfig.Views`; `UserVerification: discouraged` is
  refused (`sdk.ErrInvalidInput`: it would leave no native AAL2 path); production also refuses an
  RP ID under `.localhost`. The passkey step-up page path derives from `BrowserLoginPath`
  (`…/auth/login` → `…/auth/step-up/passkey`).
- **Row 6.** The boot line is `auth: mfa posture mfa_required=<…> source=<config|default>
  gates=<…>[,machine_lifecycle]`.
- **Row 9 and the cap.** "AAL2-capable" is one predicate: the stored registration was
  user-verified. A session is AAL2 only when that flag AND this assertion's user verification are
  both set. The registration guard is evaluated under the user lock at `Create`: the bootstrap
  tier is refused once an AAL2-capable passkey exists, and a moved auth revision since Begin is a
  conflict (`ErrStepUpRequired`, `passkey_failed reason=conflict`).
- **Row 10.** `passkey_failed` also records `ceremony=registration` for refused registrations;
  `passkey_removed` has success rows only. The admin reset skips the credential policy
  (break-glass).
- **Row 11.** `PasskeyLinks` carries no script URL: a custom-view host renders `<script src>`
  from the path it mounts `PasskeyScriptHandler` at. `FinishPasskeySignIn` returns `(TokenPair,
  user.User, error)` and `StepUpResult` carries `User`. Unspecified shapes: `GET /auth/passkeys`
  → `{items}`; `DELETE` → `{status:"removed"}` and clears the cookies; `PATCH` → the item; reset
  → `{removed}`; `/auth/methods` lists `passkeys` only when enabled. An HTML removal without a
  grant redirects UV holders to the passkey step-up page and others to `/auth/step-up` with
  purpose `remove_passkey`.
- **Row 12.** The passkey probe runs after the 0024 check (file order). `id` has a DB default;
  `aaguid` is NOT NULL DEFAULT empty; `transports` DEFAULT `'[]'`.
- **Row 15.** The collision set blocks both CSRF cookie names in every mode. A stale development
  `auth_csrf` cookie from views v0.1.0 is now read on development plain-http hosts (the shape
  check and the Origin allowlist still apply).
- **Row 16.** `BeginPasskeyRegistration` takes variadic `PasskeyRegistrationOptions` (no break);
  the refusal reuses `reason=uv_required`. views/goth's step-up "Add a passkey" link carries no
  query parameter: it shows only while the user holds no AAL2-capable passkey, the same predicate
  that checks the box on the manage page.

**Handoff notes — what a host does on upgrade, by host shape and module** (applied at the
roadmap's end). Pins: sdk v0.20.0; authentication **v0.27.1**; stores pgx v0.12.0 / turso
v0.11.0; views/goth v0.7.0; go-webauthn v0.1.0 for hosts turning passkeys on. Repin with explicit
`GOWORK=off go get <module>@<tag>`, then `GOWORK=off go mod tidy`.
- **Every host that repins stores** (rows 12, 3, 4; row 8 with a custom `MutationRepository`).
  1. Apply `0025_passkey_credentials.sql` from the store's migration source BEFORE the repinned
     binary boots: copy it into the host ledger as the next number (the production-baseline copy
     is `0032_passkey_credentials.sql`, byte-identical; guard P9 and G34 check the copy). The
     probe refuses boot in every mode, passkeys wired or not, with
     `authentication pgx store: passkey_credentials table missing — apply migration
     0025_passkey_credentials.sql from the "authentication" migration source before boot`
     (wraps `sdk.ErrNotFound`); turso names the same file. It is a new, empty table (`SET LOCAL
     lock_timeout = '5s'`, `CREATE TABLE IF NOT EXISTS`, two `IF NOT EXISTS` indexes): no
     `CONCURRENTLY`, no `index_missing` WARN, no backfill. Apply it in its own runner invocation
     like any additive migration.
  2. Row 3: access tokens minted before the upgrade carry no `aal` and read
     `AssuranceUnknown` until they refresh; a legacy session row with an empty assurance level
     omits the claim; an unknown `aal` value is rejected; a delegated token carrying `aal` is
     refused. Nothing to change unless host code reads `Credential.Assurance`.
  3. Row 4: `RequireMFA()` beside a set that admits access tokens no longer panics at wiring;
     it is an `AtLeast(AAL2)` check, so an AAL1 access token gets 401 + the RFC 9470 challenge.
     API keys still pass (S4d). `FreshAuth`, `VerifiedEmail`, `Tenant` and `Check` still panic
     beside access tokens. A host that wants the old external-only scope narrows with
     `Accept(...)`.
  4. Row 8: a host-implemented `MutationRepository` must fill `MethodSet.Passkeys` in `Snapshot`
     and apply `RemovePasskey` and `RemovePasskeys` (the admin reset) in `Apply`, under the user
     lock; an `EraseRepository` deletes the user's passkeys and reports
     `EraseCounts.Passkeys`. Run `storetest.RunPasskeys` against it (separate from `Run`, like
     `RunOAuth2`). A store that ignores passkeys undercounts login methods (conservative: it
     refuses a removal it should allow) and cannot remove passkeys.
  5. Row 6, even without passkeys: `MFARequiredNone` is now `"none"`, and the zero value is the
     new `MFARequiredDefault`. A host that wrote the constant is unaffected; a host that wrote
     the string `""` or compared against it now reads the default. The boot line gains
     `source=`.
- **IdP-only hosts** (the production-baseline shape; row 12 only). Apply 0025 (or its ledger
  copy) even though passkeys stay unwired. Nothing else moves: the zero `Required` resolves to
  none without passkeys, so an IdP host at zero still trips S4a's `external_no_mfa` in
  production. production-baseline sets `Required: MFARequiredAdmins` explicitly and its boot line
  reads `mfa_required=admins source=config gates=/auth/admin/users*`.
- **Hosts turning passkeys on** (rows 2, 5–7, 9–11, 13, 16, 17; the pocket README's "Turn on
  passkeys").
  1. Apply 0025 (above).
  2. Build the relying party: `rp, err := gowebauthn.New(webauthn.Config{RPID, RPName,
     Origins})` (`integrations/cryptids/go-webauthn` v0.1.0). `RPID` is the registrable domain
     the passkeys bind to (`example.com`); every exact host and namespace suffix in `Origins` (a
     `web.OriginPolicy`) must equal it or sit under it, or `Validate` refuses boot in every mode
     (`webauthn.ErrOriginOutsideRPID` names the host). **The RP ID is permanent:** changing it
     orphans every registered passkey. Production refuses `localhost`, `*.localhost`, an IP RP
     ID and an http origin namespace (`ErrPasskeyDevelopmentRelyingParty`). auth-cms derives the
     RP ID from its origin's hostname and `Origins` from `AllowedOrigins`.
  3. `authentication.WithPasskeys(PasskeyConfig{RelyingParty: rp, Views: authViews})` with
     `Repositories.Passkeys`, `CredentialMutations`, `AuthenticationGrants` and `Challenges`
     wired (`ErrPasskeyReposRequired` otherwise). `UserVerification` stays `preferred` unless
     the host wants every ceremony to require a biometric or PIN; `MaxPerUser` defaults to 10.
     `PublicRouteLimits.Passkey` budgets sign-in (zero → 30/min per IP).
  4. Row 6 and 7: with a `UserAdminCheck`, the zero `Required` now resolves to `admins`: every
     `/auth/admin/users*` caller the check admits AND the machine lifecycle routes (after
     `MachineRoutesGate`) need AAL2. Admins enroll two passkeys before deploying. The sole-admin
     break-glass is `MFAConfig{Required: MFARequiredNone}` and a redeploy (it drops the machine
     gate too; there is no machine-only opt-out). The reset operator path is `POST
     /auth/admin/users/{id}/passkeys/reset` from an AAL2 session (a cookie + CSRF `fetch`) or
     with an admin API key the host's `UserAdminCheck` admits.
  5. Views (rows 11, 13): `PasskeyConfig.Views` takes the same `authgoth.Views` value as
     `BrowserConfig.Views` (nil keeps the passkey routes JSON-only and an AAL1 `Browser()` +
     `RequireMFA()` route answers the JSON 401). Mount `authgoth.PasskeyScriptHandler()` at
     `authgoth.DefaultPasskeyScriptPath` (`/assets/auth/passkey.js`) or
     `WithPasskeyScriptPath`, as `FragmentScriptHandler` is mounted. The tag carries no nonce:
     `HTMLPolicy`'s `script-src 'self'` and `connect-src 'self'` load it; a host without an
     `HTMLPolicy` keeps a nonce-only CSP that blocks it, and the controls stay hidden. A
     `Permissions-Policy` must not deny `publickey-credentials-get` or
     `publickey-credentials-create`.
  6. **Custom views and SPAs: the markup contract.** A host that overrides a page (auth-cms
     overrides `Login`) renders, for `passkey.js` to drive: a container
     `data-auth-passkey="signin|register|stepup"` with `data-options-url`, `data-finish-url`,
     `data-csrf` and `data-return-to` from `LoginPage.Passkeys` / the page model, `hidden` until
     the script finds `window.PublicKeyCredential`; a `data-auth-passkey-start` button; EMPTY
     `role="status"` `data-auth-passkey-status` and `role="alert"` `data-auth-passkey-error`
     live regions; inside a `register` container an optional `data-auth-passkey-name` input and
     the **checkbox** `data-auth-passkey-require-uv` ("Require Face ID, Touch ID or a PIN";
     checked, the options body is `{"user_verification":"required"}`, else `{}`; views/goth
     checks it while the user holds no AAL2-capable passkey). On the manage page, once the user
     holds a verified passkey, offer **"Confirm with a passkey"** beside the add control: a link
     to the passkey step-up page with `return_to` set to the manage page, because adding or
     removing then needs a passkey check in the last five minutes (the options answer 403
     without it). An SPA drives the same JSON calls: `…/options` with `{}` (registration
     optionally the `user_verification` field) → `{ceremony, public_key}` →
     `parse*OptionsFromJSON` → the finish with `{ceremony, credential: cred.toJSON()}` and the
     `X-CSRF-Token` header; it reads `GET /auth/methods` on an admin 401 to pick its step-up,
     and navigates after a step-up only when the body's `assurance` is `aal2`.
  7. Row 17: a passkey step-up always asks for user verification; a presence-only passkey can
     still sign in (AAL1) but never steps up. Row 16: a user holding only a presence-only passkey
     adds a verified one on the same device with the checkbox on (upgrade in place), then removes
     the old row. U2F-only security keys cannot enroll (B-5).
- **Hosts with a plain-HTTP development setup and a browser front end** (row 15). Nothing to
  change. Safari over `http://localhost` now stores the CSRF cookie (`auth_csrf`), so JSON
  CSRF mutations stop answering 403. A development boot logs `csrf_cookie=auth_csrf
  secure=false`. Production hosts keep `__Host-auth_csrf`; a production host that somehow
  resolves the relaxed cookie fails construction twice over (the S1 non-`Secure` session cookie
  refusal and a second, independent `sdk.ErrInvalidInput` naming the cookie).
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on the S4b tags.
  Pins moved (explicit `GOWORK=off go get`, then `GOWORK=off go mod tidy`): `auth-cms`
  authentication v0.25.0→v0.27.1 and views/goth v0.5.0→v0.7.0 (sdk v0.20.0 and go-webauthn
  v0.1.0 were already pinned by #193; tidy also pruned its stale `go.sum` lines);
  `cms` sdk v0.19.0→v0.20.0; `external-identity` sdk v0.19.0→v0.20.0 and authentication
  v0.25.0→v0.27.1; `goth-showcase` sdk v0.19.0→v0.20.0; `jobs-minimal` sdk v0.19.0→v0.20.0;
  `minimal` sdk v0.19.0→v0.20.0; `production-baseline` sdk v0.19.0→v0.20.0, authentication
  v0.25.0→v0.27.1 and stores/pgx v0.11.0→v0.12.0. No non-S4b pin moved. The code-side example
  changes landed with #193, #194 and #196 (auth-cms wires passkeys; the baseline ledger gained
  0032).

**The owner device check (Verification §5).**
- **Safari 26.3 + Touch ID on macOS — PASS, 2026-10-25,** on auth-cms over
  `http://localhost:8082` built from authentication v0.27.1 and the final #196: a verified
  iCloud Keychain passkey (synced) enrolled on `/auth/account/passkeys` with the Touch ID
  prompt, sign-out, usernameless sign-in on `/auth/login`, a Touch ID step-up from
  `/demo/mfa/page`, and `/demo/mfa` answering `{"assurance":"aal2","credential":"access_token"}`.
- **The three defects it found, each fixed and tagged before step 5:**
  - **F1 — Safari 403 on every JSON CSRF mutation** (pre-existing since S5). WebKit drops the
    `Secure` `__Host-auth_csrf` cookie on `http://localhost`; Chromium keeps it. Fixed by row 15
    (#197, v0.27.0).
  - **F2 — no biometric prompt at enrollment.** Under `preferred`, macOS saved the passkey
    without Touch ID, so it was presence-only and could never reach AAL2. Fixed by row 16 and the
    views checkbox (#197 → v0.27.0; #196 amended).
  - **Step-up without user verification.** On v0.27.0 Safari with iCloud Keychain skipped Touch
    ID on the step-up assertion under `preferred`, answering `aal1`. Fixed by row 17 (#198,
    v0.27.1, owner-approved).
- **Chrome on the Mac: pending owner check.**
- **iOS, Android and Windows Hello: UNVERIFIED** (no HTTPS RP ID exists yet to test them on).
- **The Chrome headless limit.** Chrome refuses a discoverable assertion from an authenticator
  without user verification and refuses to create a resident `usb` credential without it, so a
  presence-only passkey can enroll on an internal authenticator but never be used in this
  usernameless-only design under Chrome. The browser legs therefore exercise presence-only
  assurance by enrolling presence-only on an internal authenticator and asserting from a
  UV-capable one (AAL1 from the stored registration flag), and the "non-UV authenticator with
  the box checked" case by rewriting the options to `discouraged` in the page.

**Verification record.**
- CI `check` and `vuln` passed on the final run of every PR (#191–#198), and the CodeQL
  analyses on each PR that ran them (#194, stacked on #193, ran `check` and `vuln` only). `make
  guard` runs thirty-six guards; S4b adds none.
- **Cold verification:** every tag above on poll 1; sets A, B and C as listed.
- **Live legs on `main`.** Before step 3, counted with `go test -json` (pass / fail / skip):
  authentication `stores/pgx` 453 / 0 / 1 bare and 453 / 0 / 1 schema-qualified (the known
  non-C collation skip), `stores/turso` 758 / 0 / 0 (S4a baselines: 432 / 0 / 1 and 737 / 0 /
  0), and a `live-stores` dispatch green (run 40163407415). Before step 4: `live-stores` green on
  `778bc45b` (run 40213061410), and `make test-stores` exit 0 on fresh containers in PR-3c.
- **§2, the real auth-cms binary over HTTP** (a scratch client on `webauthntest`, development
  mode): the boot line `mfa_required=admins source=default`; a password session's `/demo/mfa`
  401 `insufficient_user_authentication`; enrollment 201 with a `passkey_change_notice` on the
  console mailer; usernameless sign-in → `/demo/mfa` 200 `aal2`; a fresh password login of a
  holder 403 on registration options and removal until a passkey step-up; step-up re-minting
  with the old refresh token refused and `ExpiresAt` unchanged; a second UV passkey stepping up
  and another user's passkey refused (`user_mismatch`); a UV-registered credential asserting
  without UV reading `aal1`; a regressed counter refused; a removal revoking every session — steps
  1–9 PASS, re-run on the fixed PR-3a tree. PR-3e's real-adapter leg: step-up options request
  `required`, a response without UV 401, with UV 200 `aal2`. Steps 10–11 (an AAL1 admin's 401
  and the admin reset) are not reachable over HTTP on auth-cms, whose only platform admin is the
  boot-seeded synthetic `user:demo-owner`; `TestPasskeysThroughTheHost` covers both on the real
  adapter. Step 12: 21,905 bytes of server output scanned for the run's credential id, public
  key, user handle and name (base64url, base64, hex) with no hit; event rows covered by a host
  test against real ids.
- **§3, the browser legs** (scratch Playwright scripts, never committed; headless Chromium 148
  with a CDP virtual authenticator). Chromium **12/12** on the final #196: steps 1–9 of the plan
  plus the addendum's (a) box checked + non-UV authenticator → the new copy, no row; (b) box
  checked + UV authenticator → `user_verified: true`, step-up to `aal2`; (c) a presence-only
  holder upgraded in place on one keychain. Playwright **WebKit 26.4 5/5**, proving F1 short of a
  ceremony: `auth_csrf` stored over `http://localhost`, the options calls 200 instead of 403,
  zero application CSP violations (Playwright's screenshots inject one inline style each), and
  axe clean. WebKit has no virtual authenticator, so the real ceremony is the owner's Safari
  check.
- **§4, from the tags (this PR).** auth-cms built at `GOWORK=off` from the repinned tree; `go
  version -m` reads `sdk v0.20.0`, `pockets/authentication v0.27.1`,
  `pockets/authentication/views/goth v0.7.0` and `integrations/cryptids/go-webauthn v0.1.0`.
  The Chromium leg against that binary: **12/12 PASS** — CSP `script-src 'self'` with a nonce
  and no `unsafe-inline`, zero violations, console errors only on the allowlisted 401s of steps
  4–6 and (a), `passkey.js` served as `application/javascript` with `nosniff`, a
  Permissions-Policy that does not deny `publickey-credentials-*`, and axe zero serious or
  critical on login, manage and both step-up pages. production-baseline built at `GOWORK=off`
  (sdk v0.20.0, authentication v0.27.1, stores/pgx v0.12.0) applied its ledger through
  `0032_passkey_credentials.sql` (31 rows in `schema_migrations`) on a fresh PostgreSQL 17 with
  its own runner, booted in development (`auth: mfa posture mfa_required=admins source=config
  gates=/auth/admin/users*`; `GET /healthz` 200), and, with `passkey_credentials` dropped,
  refused to boot (exit 1): `level=ERROR msg="server exited with error" error="authentication
  pgx store: passkey_credentials table missing — apply migration 0025_passkey_credentials.sql
  from the \"authentication\" migration source before boot: pgxdb: relation
  passkey_credentials does not exist: not found"`. Every example builds, vets and tests at `GOWORK=off`; `make guard` and
  `make check` pass on the repinned tree; `pnpm build` in `workshop/documentation` succeeds.

**Owner rulings.** Tags are annotated, not signed (ruling of 2026-10-22). 2026-10-24: passkeys
first, TOTP the S4c fallback; libraries go behind sdk ports, in adapters named for the package
whose API they use; example tiers may break. **B-1:** recovery codes, native `all`,
`MFAConfig.Methods` and the enrollment grace go to S4c. **B-2:** the zero `Required` resolves to
`admins` when passkeys and a `UserAdminCheck` are both wired; `MFARequiredNone` (`"none"`) opts
out. **B-3:** with passkeys, `admins` gates the machine lifecycle after `MachineRoutesGate`.
**B-4:** attestation `none`; the AAGUID is recorded when the client reports one, untrusted.
**B-5:** `residentKey: required`, usernameless sign-in only. **B-6:** the script lives in
views/goth. **The open question — YES:** a password/code step-up grant inside the 5-minute window
bootstraps the first passkey beside a fresh login. 2026-10-25 (addendum): **F1** relax the CSRF
cookie in development only, and production must hard-fail if the relaxed configuration is ever
present; **F2** do not force `required` as the default — keep it a host toggle — and let a user
add one or several passkeys with a biometric prompt. **The step-up UV fix** (#198) was approved
("do it") after the owner's Safari re-check on v0.27.0. Lead rulings during the build: the cap
on AAL2 (registration UV AND assertion UV), `discouraged` refused by the pocket, the
ceremony-kind mark in adapter state, assertions ignoring unsolicited client extension results
while registration rejects them, and the script path as a views/goth mount decision; each one a
host can see is folded above.

**Known and deferred.**
- **To S4c.** TOTP, recovery codes, native `Required: all`, `MFAConfig.Methods` (with the
  methods claim in the same PR as TOTP) and the enrollment grace.
- **To S4d.** `aal` on delegated tokens; API keys under MFA gates (an API key passes every MFA
  gate today).
- **Follow-ups.** Username-first passkey sign-in; attestation and AAGUID allow-lists (B-4);
  decoupled cross-origin SPA cookie sessions (cookie CSRF does not cross origins; such clients
  use bearer tokens); a committed Playwright spec; autofill and Related Origin Requests; iOS,
  Android and Windows Hello on an HTTPS RP ID.
- **Cosmetic.** The views checkbox sits flush to its label (ui/goth form styling); the manage
  page shows raw RFC3339 times.
- **Housekeeping.** The pgx store's package doc still says "the 20-port bundle" (24 now). The
  goredis `TestOpenHonorsDeadlineAfterConnection` flaked once under `make check` load (5/5 alone).
  `passkey.js` sits at its 200-line budget, so the next control needs a trim first. A Makefile
  guard for "a subsystem subpackage never imports its parent" is not built; the code holds it.
- **Residuals.** The first passkey is an AAL1 bootstrap: a stolen password can enroll it and
  reach AAL2; the `passkey_change_notice` is the detective control. AAL2 lasts the refresh
  lifetime (a step-up keeps `ExpiresAt`). Synced passkeys trust the platform account (B-4).
- **Browser legs and events.** `/debug/security-events` needs a platform admin, and auth-cms's
  only one is the boot-seeded `user:demo-owner`, so the browser legs cannot read event rows;
  `TestPasskeysThroughTheHost` pins them.

## MFA posture (SOC 2 slice S4a — published 2026-10-24)

Two annotated tags (not signed), each cut at its PR's squash-merge commit on `main`, pushed,
THEN polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an empty
module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build <module>/...`).
Both resolved on the first poll. Design and rulings:
[plans/s4a-mfa-posture.md](plans/s4a-mfa-posture.md) (rev 2, ratified 2026-10-24; its
"Build-time deviations" section records the lead rulings taken during the build). Under the
owner's 2026-10-23 process ruling this section is the whole record: there is no handoff file and
no manifest. The handoff notes below say what a host does on upgrade, and hosts adopt them once,
at the roadmap's end. The operator surface is the docs site: *Production posture* ("Added by
S4a"), the authentication page's *Assurance and MFA*, and the sdk authentication page's *MFA*.
The pocket README's "Assurance model" is the reference.

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `sdk/v0.19.0` | `544bc781` | #188 (PR-1) | OK, poll 1 |
| 2 | `pockets/authentication/v0.25.0` | `467b834b` | #189 (PR-2; cut after the authentication store legs and the three IdP-related examples on merged `main`) | OK, poll 1; also built the MVS set (below) |

Order: 1, then 2. #189 moved the pocket's sdk pin from v0.17.0 to v0.19.0 by explicit `GOWORK=off
go get`, so a host that upgrades the pocket gets the strict amr rule by MVS. The repin is
semantic: the pocket references no new sdk symbol. Both modules ship MINOR under the v0
convention. The sdk tag message names the amr rule change (row 1). The authentication tag
message names the assurance read (row 3) and the single-factor posture on both constructors
(rows 10–12).

**Step 2's MVS set.** One scratch consumer resolved authentication v0.25.0 beside `stores/pgx`
v0.11.0 and `stores/turso` v0.10.0. MVS selected sdk v0.19.0, which the pocket requires, `go mod
verify` passed and `go build` succeeded. The released stores need no retag, because no store code
changed.

**Not tagged.** Every store module (no store code; MVS lifts their pins inside a host),
`pockets/authentication/views/goth`, `ui/goth`, `pockets`, `integrations/cryptids/go-oidc`,
`integrations/authentication/firebase` and `integrations/authentication/auth0` (no code: Firebase
keeps its flag, and Auth0 emits `["mfa"]` under the defaults), and `workshop/gopernicus` (R-2a:
no new acknowledgement, and P5 matches `SingleFactorAcknowledged` by name). No migration:
`mfa_required_denied` is a string with no CHECK. Nothing retracts.

**Upgrade notes.** Thirteen rows. None is a compile-time break. Three are production boot failures
(10–12), two change a decision without a compile error (1 and 3), two are opt-in (7 and 8), and
the rest are additive or informational. The "Read first" box, the table and the paragraph under
it are the plan's, verbatim.

> **Read first — by host shape.** Development logs one WARN naming `SingleFactorAcknowledged` and its causes for every
> shape production would refuse: that WARN is the pre-deploy signal.
> - **IdP-only via `New`** (`WithExternal(`, native login off) → rows 1 and 10: declare `WithMFA(` or acknowledge.
> - **Native password with the acknowledgement** → none. **Native OAuth** (`WithOAuth(`) without it → row 11.
> - **Direct `NewExternalAuthenticator`** → rows 1 and 12. **A host-implemented `CredentialService`** → row 3.
>
> Greps: `grep -rnE 'RequireMFA\(|MFAMethods|NewOIDC\(' --include='*.go'` (row 1); `grep -rn 'WithOAuth('
> --include='*.go'` (row 11); `grep -rnE 'WithExternal\(|NewExternalAuthenticator\(|WithMFA\(|SingleFactorAcknowledged'
> --include='*.go'` (rows 10, 12).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | default amr rule strict (R-1) | one-factor amr (`["otp"]`, `["sms"]`, `["hwk"]`, `["swk"]`, `["pop"]`) leaves `MFA` false: `RequireMFA` 401 + challenge; no compile error. Auth0 (`["mfa"]`) and Firebase (flag) hosts are unaffected | `MFAMethods: []string{"mfa","otp","hwk","sms","swk","pop"}` (any-of: the v0.18.0 default exactly) | sdk@v0.19.0 |
| 2 | `Credential.Assurance`; `session.AssuranceLevel.AtLeast` | additive: external AAL1/AAL2; access token and API key `unknown` | none | authentication@v0.25.0 |
| 3 | `RequireMFA()` reads `Assurance` (the access-token panic stays) | a host's own `CredentialService` that never stamps `Assurance` gets its external MFA tokens denied; no compile error | stamp `AssuranceAAL2` when `Identity.MFA`, else `AssuranceAAL1` | authentication@v0.25.0 |
| 4 | `mfa_required_denied` rows (budgeted); `external_login` Details `assurance` | new audit rows; additive row content | none | authentication@v0.25.0 |
| 5 | `ExternalProvider.RequireMFA` | additive; a below-AAL2 token gets 401 + challenge on every route and a row (gate `provider`) | leave false | authentication@v0.25.0 |
| 6 | `WithMFA(MFAConfig{Required})`, zero = none | additive; an unknown value → `sdk.ErrInvalidInput` | none | authentication@v0.25.0 |
| 7 | opt-in `Required: MFARequiredAdmins` | `/auth/admin/users*` answers 401 + challenge to an ADMITTED principal below AAL2 (every native session in S4a); API keys pass; non-admins keep 403; no `UserAdminCheck` → `ErrMFAUnsatisfiable` | stay none until S4b on a native-admin host | authentication@v0.25.0 |
| 8 | opt-in `Required: MFARequiredAll` | every provider gets `RequireMFA` (cloned); native login or no `External` → `ErrMFAUnsatisfiable` in every mode | native login off, or `admins` | authentication@v0.25.0 |
| 9 | `ErrMFARequired`, `ErrMFAUnsatisfiable` new; `ErrSingleFactorUnacknowledged` re-exported from logic (same `errors.Is` target); `posture_waiver` gains `causes` | new `errors.Is` targets; log content | none | authentication@v0.25.0 |
| 10 | production `New`: `External` + `Required` none is cause `external_no_mfa` (R-2a) | BOOT FAILURE `ErrSingleFactorUnacknowledged` naming the cause | `WithMFA(…)` or `SingleFactorAcknowledged: true` | authentication@v0.25.0 |
| 11 | production `New`: OAuth `Providers` are cause `native_oauth` (R-2b) | BOOT FAILURE without the acknowledgement | `SingleFactorAcknowledged: true` | authentication@v0.25.0 |
| 12 | production `NewExternalAuthenticator` without `RequireMFA` on every provider | BOOT FAILURE `ErrSingleFactorUnacknowledged` | `RequireMFA` on each provider, or `ExternalConfig.SingleFactorAcknowledged: true` | authentication@v0.25.0 |
| 13 | examples wired (production-baseline admins + role routes; external-identity provider floor) | example behaviour; README walks updated | — | examples (untagged) |

There are no compile-time breaks. Rows 10–12 fail boot; rows 1 and 3 change decisions silently; rows 7–8 are opt-in.
Both modules ship MINOR under the v0 convention; the tag messages name rows 1, 3 and 10–12.

Build-time deviations a host can see, folded here (the table stays verbatim):
- **Row 1.** Matching stays case-sensitive, so `MFA` and `PWD` are unknown values. An empty
  non-nil `MFAMethods` is the strict rule, the same as nil. No sdk symbol was added or removed.
- **Row 4.** `mfa_required_denied` Details carry `gate` (`route`, `admin` or `provider`),
  `credential`, `provider` (external tokens), the presented `assurance` and `required`
  (`aal2`). They never carry an amr value, a claim or the token. `UserID` is empty for gate
  `provider`, because that denial precedes subject resolution.
- **Row 10.** `external_no_mfa` counts per provider: any `WithExternal` provider without
  `RequireMFA` while `Required` is none. A root host whose every provider sets `RequireMFA` boots
  without the acknowledgement, which matches the standalone path.
- **Posture order.** The MFA validation runs before the logic engine is built. A production host
  with a development-only external provider and no MFA posture therefore reports the
  single-factor refusal first.
- **The HTTP constructor.** `authenticationhttp.New` with `WithAdministrationMFA()` but without
  `WithUserAdminCheck` is `sdk.ErrInvalidInput`. The root `New` returns `ErrMFAUnsatisfiable`
  before reaching it.
- **Log rendering.** slog quotes a multi-cause value
  (`causes="[native_password external_no_mfa]"`), and an empty user id renders as `user_id=""`.
- **Exported symbols the table does not name.** In logic (`authlogic`): `ErrMFARequired` (wraps
  `sdk.ErrUnauthorized`, re-exported at the root), `MFAGate` with `MFAGateRoute`,
  `MFAGateAdmin` and `MFAGateProvider`, `MFADenial`, `(*Service).RecordMFADenied` and
  `(*ExternalAuthenticator).RecordMFADenied`. At the root: `MFARequirement` with its three
  constants. In `authenticationhttp`: `MFADenialRecorder` and `WithAdministrationMFA()`.
- **The tag messages** name the rows' changes in words, not by row number.

**Handoff notes — what a host does on upgrade, by host shape and module** (applied at the
roadmap's end).
- **Every host, first.** Run the three "Read first" greps. Then boot once in development and look
  for the WARN naming `SingleFactorAcknowledged` and its `causes`. Every shape that production
  would refuse logs it, and none of rows 10–12 gives a compile signal.
- **`sdk` v0.19.0 (row 1).** `Identity.MFA` now has one documented meaning on the port: the
  provider asserted at least two factor categories, or an explicit MFA claim or flag. A
  one-factor sign-in leaves it false. `sdkauth.NewOIDC`'s default amr rule is strict over the IANA
  amr registry, sorted into factor categories. `mfa` alone counts, or values from two of
  knowledge (`pwd` `pin` `kba`), possession (`otp` `sms` `tel` `hwk` `swk` `sc` `pop`) and
  inherence (`fpt` `face` `iris` `retina` `vbm`). Any other value is no factor. So `["otp"]`,
  `["sms"]`, `["hwk"]`, `["swk"]` and `["pop"]` no longer set `MFA`, and `RequireMFA()` answers
  them 401 with the challenge. The hatch is per provider: `ClaimMapping.MFAMethods:
  []string{"mfa", "otp", "hwk", "sms", "swk", "pop"}` restores the v0.18.0 any-of default
  exactly. Set it only for an issuer that emits those values after a second factor. An IdP
  passkey sign-in that emits only `hwk` or `swk` reads as one factor; list it only if the IdP
  enforces user verification. A host's own `sdkauth.Provider` must set `MFA` with the same
  meaning. Auth0 (`["mfa"]`) and Firebase (`firebase.sign_in_second_factor`) hosts are
  unaffected.
- **`pockets/authentication` v0.25.0** (requires sdk v0.19.0; the released stores resolve with
  it unchanged). By host shape:
  - **IdP-only through `New`** (`WithExternal`, native login off; rows 1 and 10). Production now
    refuses a provider without `RequireMFA` while `MFAConfig.Required` is none, with
    `ErrSingleFactorUnacknowledged` and cause `external_no_mfa`. Choose one fix.
    `WithMFA(MFAConfig{Required: MFARequiredAdmins})` requires AAL2 on `/auth/admin/users*` for
    callers the `UserAdminCheck` admits; it needs `AdministrationConfig.UserAdminCheck`
    (`ErrMFAUnsatisfiable` otherwise) and gates nothing else. `MFARequiredAll` sets `RequireMFA`
    on a copy of every provider. `RequireMFA: true` can go on each `ExternalProvider` directly.
    Or `IdentityConfig.SingleFactorAcknowledged: true` accepts AAL1 users, with one
    `posture_waiver=SingleFactorAcknowledged causes="[external_no_mfa]"` WARN. Every `New` logs
    `auth: mfa posture mfa_required=<none|admins|all>
    gates=<none|/auth/admin/users*|external_tokens>`.
  - **Native password with the acknowledgement.** Nothing changes, but the waiver line now
    carries `causes` (`native_password`, plus `passwordless`, `native_oauth` or
    `external_no_mfa` when those are wired).
  - **Native OAuth (`WithOAuth`) without the acknowledgement (row 11).** Production now fails
    boot with cause `native_oauth` (R-2b: an OAuth link is AAL1). Set
    `IdentityConfig.SingleFactorAcknowledged: true`. There is no other hatch until native second
    factors ship.
  - **Direct `NewExternalAuthenticator` (rows 1 and 12).** This constructor ran no posture check
    before. Production now refuses unless every provider sets `RequireMFA` or the new
    `ExternalConfig.SingleFactorAcknowledged: true` is set (one waiver WARN); development WARNs
    once. The error is `ErrSingleFactorUnacknowledged`, now defined in logic and re-exported at
    the root under the same name, so one `errors.Is` target serves both constructors. The root
    `New` refuses `ExternalConfig.SingleFactorAcknowledged` with `sdk.ErrInvalidInput` and reads
    `IdentityConfig.SingleFactorAcknowledged`, so neither field is ever inert.
  - **A host-implemented `CredentialService` (row 3).** `RequireMFA()` now reads
    `Credential.Assurance`. Stamp `session.AssuranceAAL2` when `Identity.MFA` is set, else
    `session.AssuranceAAL1`, on external tokens. Without the stamp, every MFA gate denies them.
    Implement `authenticationhttp.MFADenialRecorder` (`RecordMFADenied(ctx,
    authlogic.MFADenial)`) to record its denials; `NewAuthenticator` asserts it once.
    `CredentialService` itself is unchanged.
  - **Every host.** `RequireMFA()` still panics at wiring beside a set that admits access tokens,
    because no access token can reach AAL2 yet (S4b lifts the panic). API keys pass every MFA
    gate. Native access tokens and API keys carry `AssuranceUnknown`; external tokens are AAL1 or
    AAL2, never AAL3. `session.AssuranceLevel.AtLeast(min)` is the one comparator. A
    native-admin host stays at `Required` none until S4b (R-4), because `admins` would answer
    every native session 401. `admins` does NOT gate role routes, cms admin routes, host admin
    pages or the machine lifecycle routes (R-5); add `RequireMFA()` to those. Denials answer 401
    `WWW-Authenticate: Bearer error="insufficient_user_authentication",
    error_description="multi-factor authentication is required"` (`ErrMFARequired`). An
    unknown `Required` is `sdk.ErrInvalidInput`. An audit reader keyed on event types sees the new
    budgeted `mfa_required_denied` type and an `assurance` key in `external_login` Details.
  - **The re-auth recipe.** On `insufficient_user_authentication`, re-authorize at the IdP with a
    forced second factor, using the IdP's own step-up parameter or `prompt=login`. A route with
    `RequireMFA()` beside `Browser()` needs a login path that does this. Otherwise a live IdP
    session answers AAL1 again and the browser loops. **Self-check:** sign in with a second
    factor, and the newest `external_login` row's Details must read `"assurance":"aal2"`.
- **Examples (this PR).** Every example builds, vets and tests at `GOWORK=off` on the S4a tags.
  Pins moved (explicit `GOWORK=off go get`, then `go mod tidy`, which only rewrote `go.sum`):
  `auth-cms` sdk v0.18.0→v0.19.0 and authentication v0.24.0→v0.25.0; `cms` sdk
  v0.18.0→v0.19.0; `external-identity` sdk v0.18.0→v0.19.0 and authentication
  v0.24.0→v0.25.0; `goth-showcase` sdk v0.18.0→v0.19.0; `jobs-minimal` sdk v0.18.0→v0.19.0;
  `minimal` sdk v0.18.0→v0.19.0; `production-baseline` sdk v0.18.0→v0.19.0 and authentication
  v0.24.0→v0.25.0. No non-S4a pin moved. The code-side example changes landed with #189. In
  production-baseline, `WithMFA(MFAConfig{Required: MFARequiredAdmins})` and `RequireMFA()` on
  the role routes, with the README walk minting `["pwd","mfa"]` and adding a single-factor 401
  step. In external-identity, `RequireMFA: true` on both `dev` providers and a no-amr 401 step.
  auth-cms is unchanged, because its demo tokens already send `["pwd","mfa"]`.

**Verification record.**
- CI `check` and `vuln` (push and pull_request events) and the CodeQL analyses passed on the
  final run of #188 and #189. `make guard` runs thirty-six guards; S4a adds none.
- #188 (sdk): `TestOIDCMFARule` (36 cases: the plan's rows, each old default value alone,
  same-category pairs, unknown and mixed-case values, the flag path and the exact hatch) and
  `TestOIDCMFARuleCoversRegistry` (every registry value alone and every pair). Weakening the rule
  to one factor fails 247 assertions. A scratch program at `GOWORK=off` ran a real `gooidc`
  verifier through `NewOIDC`. `["pwd"]`, `["otp"]`, `["hwk"]` and `["user","hwk"]` gave false.
  `["pwd","otp"]`, `["mfa"]`, `["face","hwk"]`, `["hwk","pin"]` and `["pop","fpt"]` gave true.
  With the six-value hatch, `["otp"]` gave true and `["pwd"]` false.
- Live legs on merged `main` @ `467b834b` before step 2, counted with `go test -json` (pass /
  fail / skip). Authentication `stores/pgx` gave 432 / 0 / 1 bare and 432 / 0 / 1
  schema-qualified (the known collation skip), and `stores/turso` gave 737 / 0 / 0. The pocket
  core gave 2838 / 0 / 3. production-baseline, external-identity and auth-cms built, vetted and
  tested green.
- From the #189 record (real binaries, development mode). §2 production-baseline: the boot line
  read `mfa_required=admins gates=/auth/admin/users*`. `GET /auth/admin/users` answered 200 to
  `["pwd","mfa"]`, 401 with the challenge to `["pwd"]` or `["otp"]`, and 403 with no MFA row to a
  non-admin MFA token. `security_events` held `mfa_required_denied` (gate `admin`) and
  `external_login` with `aal2`, and the README walk's S2 `authorization_denied` step still landed.
  §3 external-identity: `["mfa"]` tokens got 200 on the header and cookie routes. A no-amr token
  got 401 with `memstore: security event type=mfa_required_denied status=blocked user_id=""` and
  no provisioning line. §4 posture program: `all` beside password flows and `admins` without a
  check both gave `ErrMFAUnsatisfiable`. Production external-only with `Required` none gave
  `ErrSingleFactorUnacknowledged … causes [external_no_mfa]`, and OAuth providers gave `causes
  [native_oauth]`. The acknowledged case logged one `posture_waiver` line with the causes. The
  standalone path refused, booted with `RequireMFA` on every provider, and warned once in
  development.
- This PR: every example builds, vets and tests at `GOWORK=off` against the tags. `go version -m`
  on the production-baseline binary shows sdk v0.19.0 and authentication v0.25.0. `make guard`
  and `make check` pass on the repinned tree, and `pnpm build` in `workshop/documentation`
  succeeds.
- This PR, boot from the tags: `GOWORK=off` binaries of `examples/production-baseline` in
  development, on a fresh scratch PostgreSQL 17 database, migrated by its own runner. The boot
  log carries the posture line:
  `time=2026-10-24T14:20:32.119-07:00 level=INFO msg="auth: mfa posture" mfa_required=admins
  gates=/auth/admin/users*`. Dev-issuer tokens for `u1` with `["pwd"]` and `["pwd","mfa"]` hit
  `POST /authorization/roles` (role routes, `RequireMFA()`). `["pwd"]` got `HTTP/1.1 401
  Unauthorized` with `Www-Authenticate: Bearer error="insufficient_user_authentication",
  error_description="multi-factor authentication is required"` and
  `{"message":"authentication required","code":"unauthenticated"}`. `["pwd","mfa"]` got 403
  `permission_denied` (authenticated; `u1` was not yet an admin). After `cmd/bootstrap` granted
  `u1` the global admin role, `GET /auth/admin/users` answered `HTTP/1.1 200 OK` to
  `["pwd","mfa"]`. It answered 401 with the same challenge to `["pwd"]` and to `["otp"]` (the
  strict rule), and 403 with no MFA row to `u2`, a non-admin, with `["pwd"]` or `["pwd","mfa"]`.
  `POST /authorization/roles` then answered `["pwd","mfa"]` with `HTTP/1.1 200 OK
  {"outcome":"applied"}` and `["pwd"]` with 401. `security_events` held four
  `mfa_required_denied|blocked|{"gate": "admin"|"route", "provider": "idp", "required":
  "aal2", "assurance": "aal1", "credential": "external_token"}` rows (two per gate) and
  `external_login` rows reading `"assurance": "aal2"` and `"aal1"`. No row or log line carries a
  token or an amr value, and the DSN appears zero times in the log.

**Owner rulings.** Tags are annotated, not signed (ruling of 2026-10-22). **R-1:** the default amr
rule is strict over the IANA registry, sorted into factor categories, and an explicit
`MFAMethods` is the hatch that restores any-of exactly. **R-2a:** IdP hosts with `Required` none
in production fall under the widened `SingleFactorAcknowledged`, with cause `external_no_mfa` on
both constructors and a `causes` attribute; there is no new acknowledgement and no workshop tag.
**R-2b:** native OAuth counts as single-factor (cause `native_oauth`). **R-3:** `Required: all`
beside native single-factor login is an `ErrMFAUnsatisfiable` boot refusal in every mode.
**R-4:** the bundled admin MFA default stays none until S4b. **R-5:** the machine lifecycle
routes stay outside `admins` until S4b. The three planner placements stand: root `New` refuses
`External.SingleFactorAcknowledged`, the boot line's `gates=` vocabulary, and
`ErrSingleFactorUnacknowledged` moving into logic. Every lead ruling taken during the build is in
the executed plan's "Build-time deviations", and each one a host can see is folded into the
notes above.

**Known and deferred.**
- **acr, a follow-up.** `ClaimMapping.ACR`/`MFAACRValues`, `Identity.ACR` and the devissuer
  field wait until one vendor's MFA acr value is verified against its documentation and an
  example uses it. `acr_values` in the challenge waits with it.
- **The IdP table.** The pocket README's Auth0 and Firebase cells cite this repository's code
  ("checked 2026-10-24"), because the build had no web access. Google, Okta and Entra ID are
  **unknown**, carry no mapping advice, and point at the self-check. Linking each vendor fact
  with a checked date is a follow-up.
- **To S4b.** Stamp `Credential.Assurance` on native access tokens, from a signed `aal` claim at
  mint or from the live session row. Lift the `RequireMFA()` panic beside access tokens in the
  same PR. Add `MFAConfig.Methods` and define enrollment grace. Take R-4/R-5 to `admins`,
  machine lifecycle included. `MinLoginAssurance` and `RecentAuthPolicy.MinAssurance` become
  meaningful once a native AAL2 method exists. **To S4c:** TOTP, recovery codes and the key
  ring.
- **To S4d.** API keys pass every MFA gate, and an AAL1 native session can mint API keys, so a
  `UserAdminCheck` that admits service accounts allows administration without MFA.
- **Standalone hosts.** A host calling `NewExternalAuthenticator` directly now gets the posture
  check (row 12). It is a production boot failure with no compile signal, and the development
  WARN is the only advance notice.
- Out of S4a by the plan: passkeys/WebAuthn (S4b), TOTP and recovery codes (S4c), idle timeout,
  registration mode, revoke-all, API key TTL/disable and the breach checker (S4d), AAL3 from
  external claims, and per-claim attribution in events. Native OAuth does not reach AAL2,
  because the OAuth ID token claims carry no amr.

## Secrets and logging (SOC 2 slice S6 — published 2026-10-24)

Twelve annotated tags (not signed), each cut at its PR's squash-merge commit on `main`, pushed,
THEN polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an empty
module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build <module>/...`).
Every one resolved on the first poll. Design and rulings:
[plans/s6-secrets-logging.md](plans/s6-secrets-logging.md) (rev 2, ratified 2026-10-24; its
"Build-time deviations" section records the lead rulings taken during the build). Under the
owner's 2026-10-23 process ruling this section is the whole record: there is no handoff file and
no manifest. The handoff notes below say what a host does on upgrade; hosts adopt them once, at
the roadmap's end. The operator surface is the docs site's *Production posture* guide ("Added by
S6" and "Secrets and logging (S6)") and the log-content policy paragraph in `sdk/README.md`
(Packages → `logging`).

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `sdk/v0.18.0` | `f347b66a` | #182 (PR-1) | OK, poll 1 |
| 2 | `integrations/datastores/pgxdb/v0.14.0` | `59c318d8` | #183 (PR-2; cut after the six pgx store legs on merged `main`) | OK, poll 1; also built the MVS set (below) |
| 3 | `integrations/datastores/turso/v0.9.0` | `59c318d8` | #183 (PR-2; cut after the five turso store legs on merged `main`) | OK, poll 1; also built the MVS set (below) |
| 4 | `integrations/kvstores/goredis/v0.4.0` | `d54de133` | #184 (PR-3; cut after the goredis live leg on merged `main`) | OK, poll 1 |
| 4 | `integrations/email/sendgrid/v0.5.0` | `d54de133` | #184 (PR-3) | OK, poll 1 |
| 4 | `integrations/filestorage/s3/v0.3.0` | `d54de133` | #184 (PR-3) | OK, poll 1 |
| 4 | `integrations/filestorage/gcs/v0.3.0` | `d54de133` | #184 (PR-3) | OK, poll 1 |
| 4 | `integrations/oauth/google/v0.3.0` | `d54de133` | #184 (PR-3) | OK, poll 1 |
| 4 | `integrations/oauth/github/v0.3.0` | `d54de133` | #184 (PR-3) | OK, poll 1 |
| 4 | `integrations/authentication/firebase/v0.2.0` | `d54de133` | #184 (PR-3; the tag message says it breaks every builder) | OK, poll 1 |
| 4 | `integrations/cryptids/golang-jwt/v0.3.0` | `d54de133` | #184 (PR-3) | OK, poll 1 |
| 5 | `workshop/gopernicus/v0.9.0` | `5143422f` | #186 (PR-4) | OK, poll 1; a cold `init --db pgx` from `@v0.9.0` requires sdk v0.18.0 and pgxdb v0.14.0, `--db turso` requires sdk v0.18.0 and turso v0.9.0; both build and `guard --posture` is clean (25 rules, 2 packages) |

Order: 1, then 2 and 3, then 4, then 5 — never a dependent before its dependency. After step 4,
#185 (PR-2b, `1b0643bb`) repinned the twelve store modules to sdk v0.18.0 and pgxdb v0.14.0 /
turso v0.9.0 by explicit `GOWORK=off go get` — pins only, no tags (R-3); step 5 followed. Every
compile-time break below ships under MINOR, per the v0 convention; each tag message names the
fields that changed type.

**Steps 2 and 3, MVS sets.** One scratch consumer resolved pgxdb v0.14.0 and sdk v0.18.0 beside
every released pgx store — authentication `stores/pgx` v0.11.0, authorization `stores/pgx`
v0.21.0, cms `stores/pgx` v0.5.0, events `stores/pgx` v0.4.0, jobs `stores/pgx` v0.7.0 and
secrets `stores/pgx` v0.2.1 — and another resolved turso v0.9.0 and sdk v0.18.0 beside every
released turso store (authentication v0.10.0, authorization v0.20.0, cms v0.4.0, jobs v0.6.0,
events v0.3.0); each `go.mod` selected the new connector, `go mod verify` passed and `go build`
succeeded. The released stores need no retag: their production code is unchanged and MVS lifts
their old sdk and connector pins inside a host.

**Not tagged.** Every store module (#185 moved pins only), every pocket (one doc comment in
`pockets/authentication/logic/authentication/context.go`), `pockets`, `integrations/tracing/otel`,
`integrations/cryptids/go-oidc`, `integrations/authentication/auth0` (no secret fields), `bcrypt`,
`google-uuid`, `robfig-cron`, every `views/goth` and `ui/goth`. No migration. Nothing retracts.

**Upgrade notes.** Nineteen rows: nine compile-time breaks (2, 7, 9, 12–17), one boot failure
(10), three log-content changes without a compile error (3, 8 and 11), the rest additive or
informational. The "Read first" box, the paragraph under it and the table are the
plan's, verbatim.

> **Read first.** (a) Every migrated field (rows 2, 7, 9, 12–17 — `DSN` and `URL` included) renders `[redacted]`
> through `fmt`, `+`, templates and `%s`; a DSN/URL COMPOSED from one would carry the marker, so pgxdb/turso `Open` and
> SMTP `Probe`/`Send` now refuse it with `sdk.ErrRedactedSecret` (loud at boot, not at the wire). Grep every read site
> and add `.Reveal()`: `grep -rnE '\.(DSN|URL|Password|AuthToken|APIKey|SecretAccessKey|CredentialsJSON|ClientSecret)\b'
> --include='*.go' . | grep -v 'Reveal()'`. (b) A production turso host with `DB_LOG_QUERIES=true` (prefixed forms
> included) fails boot, as pgx has since S1: `grep -rnE '^[^#]*[A-Z_]*DB_LOG_QUERY(IES|_ARGS)=true' .env* deploy/`.

Compile-time breaks are rows 2, 7, 9, 12–17: `sdk.Secret(v)` at the literal (an untyped constant needs nothing) and
`.Reveal()` where the host READS the field. Row 10 fails boot. Rows 3 and 8 change log content without a compile error.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `sdk.Secret`, `sdk.Redacted`, `sdk.ErrRedactedSecret`, `PrincipalCapture` | additive | none | sdk@v0.18.0 |
| 2 | `email.SMTPConfig.Password` is `sdk.Secret`; `Probe`/`Send` refuse a composed one | compile error for a `string` variable; `%s` prints `[redacted]` | `sdk.Secret(v)`; `.Reveal()` to read | sdk@v0.18.0 |
| 3 | `logging.New` redacts by default | attrs keyed password/token/api_key/… (Design 3 list) print `[redacted]` | rename the attribute (auth-cms's `seeded_api_key` is the worked example); `LOG_REDACT_KEYS` adds; compose `NewContextHandler` over your own handler | sdk@v0.18.0 |
| 4 | `web.Logger` adds `client_ip`, `request_id`, `user_agent`, `principal_type`, `principal_id` | additive; `remote_addr` kept; must sit inside `TrustProxies`/`RequestID` | `LOG_REDACT_KEYS=client_ip,user_agent` | sdk@v0.18.0 |
| 5 | `sdk.WithPrincipal` writes through to a capture chain | none unless the host reads `PrincipalCapture`; doc only | none | sdk@v0.18.0 |
| 6 | `ContextHandler` skips an id the record carries; nested `web.RequestID` reuses an existing id | one `request_id` per line; nested routers keep the host's id | none | sdk@v0.18.0 |
| 7 | pgxdb `Config.DSN`/`Password` are `sdk.Secret`; `Open` refuses a composed one | compile error for `string` variables; `ParseEnvTags` hosts unaffected unless they READ the field | `sdk.Secret(v)`; `.Reveal()` | pgxdb@v0.14.0 |
| 8 | query log = `arg_count`/`arg_types` + sqlstate/constraint (or `error_type`) on errors | no bind values or error text in dev logs | `DB_LOG_QUERY_ARGS=true` / `Config.LogQueryArgs` (development only; inert without `LogQueries`) | pgxdb@v0.14.0 |
| 9 | turso `Config.URL`/`AuthToken` are `sdk.Secret`; `Open` refuses a composed one | compile error for `string` variables | `sdk.Secret(v)`; `.Reveal()` | turso@v0.9.0 |
| 10 | turso production refuses `LogQueries`/`LogQueryArgs` | `Open` fails `ErrQueryLoggingRefused` naming `Config.LogQueries (DB_LOG_QUERIES)`, joined with any TLS refusal | none — unset it (matches pgxdb since S1; P7 warns pre-deploy) | turso@v0.9.0 |
| 11 | turso query log = arity/types | as row 8 | as row 8 | turso@v0.9.0 |
| 12 | goredis `Config.URL`/`Password` are `sdk.Secret` | compile error | `sdk.Secret(v)` | goredis@v0.4.0 |
| 13 | sendgrid `Config.APIKey` | compile error | `sdk.Secret(v)` | sendgrid@v0.5.0 |
| 14 | s3 `Config.SecretAccessKey` | compile error | `sdk.Secret(v)` | s3@v0.3.0 |
| 15 | gcs `Config.CredentialsJSON` | compile error | `sdk.Secret(v)` | gcs@v0.3.0 |
| 16 | google/github `Config.ClientSecret` | compile error | `sdk.Secret(v)` | oauth/google@v0.3.0, oauth/github@v0.3.0 |
| 17 | firebase `Config.CredentialsJSON` (`[]byte` → `sdk.Secret`) | compile error for EVERY builder, literal or not | `sdk.Secret(string(b))` | firebase@v0.2.0 |
| 18 | provider/sender/store/signer structs implement `Format` + `LogValue` | `%+v`/`%#v`/`slog.Any` of one prints a redacted summary | none | the PR-3 modules incl. golang-jwt@v0.3.0 |
| 19 | P7 flags uncommented `DB_LOG_QUERIES=true`/`DB_LOG_QUERY_ARGS=true`; scaffold pins + `sdk.Secret` wraps | `guard --posture` finding on a stale `.env.example` | comment the line | workshop@v0.9.0 |

Build-time deviations a host can see, folded here (the table stays verbatim): row 3's list is
`logging.DefaultRedactKeys`, seventeen keys — `password`, `passwd`, `secret`, `token`,
`access_token`, `refresh_token`, `api_key`, `apikey`, `x_api_key` (added in the build),
`authorization`, `cookie`, `set-cookie`, `private_key`, `client_secret`, `credentials_json`,
`auth_token`, `dsn` — matched exactly per key, case-insensitive, `-` and `_` equivalent (so
`X-API-Key` matches; `x_api_key_id` does not). Row 8's failed-query end line also carries
`command_tag`, and `constraint` is present (empty) on every `PgError`. Row 18's `Format` and
`LogValue` have pointer receivers, as every constructor returns a pointer; printing a
dereferenced value bypasses them. Row 19's P7 also matches prefixed keys (`AUTH_DB_LOG_QUERIES`),
any `strconv.ParseBool` true (`1`, `TRUE`) and quoted values.

**Handoff notes — what a host does on upgrade, per module** (applied at the roadmap's end).
- **Every host, first.** Run the two "Read first" greps. The type change is the whole migration:
  a literal or untyped constant still assigns; a `string` variable becomes `sdk.Secret(v)`
  (`sdk.Secret(os.Getenv("…"))` at the literal), and every place the host READS a migrated field
  becomes `cfg.Field.Reveal()` — `Reveal()` is the one read. A `Secret` rendered through `fmt`,
  `+`, a template or `%s` yields `[redacted]`, which is why a composed DSN, URL or SMTP password
  now fails loudly with `sdk.ErrRedactedSecret` (match it with `errors.Is`). `ParseEnvTags` hosts
  are unaffected unless they read the field. Also grep `slog.Any(` and `%+v` over a connector,
  provider, sender, store or signer value (row 18 and the pointer-receiver limit above), and
  grep for any attribute key in the row-3 list whose value the host relies on reading in its own
  logs. Never key a map by `Secret` (`encoding/json` writes string-kind keys as they are).
- **`sdk` v0.18.0.** New: `sdk.Secret`, `sdk.Redacted`, `sdk.ErrRedactedSecret`,
  `sdk.WithPrincipalCapture`/`PrincipalCapture`. `email.SMTPConfig.Password` is a `Secret`;
  `NewSMTP` keeps its signature, and a password containing `[redacted]` makes `Probe` and `Send`
  return `sdk.ErrRedactedSecret` before dialing (a production host probes at boot, so the boot
  fails there). `logging.New` now composes `ContextHandler(RedactHandler(text|json))` in every
  mode — there is no off switch (R-5): `LOG_REDACT_KEYS` (comma-separated, additive to the
  defaults, `logging.Options.RedactKeys`) adds keys; a host that must log a listed key renames
  the attribute (auth-cms's demo now logs `seeded_api_key` and `token_path`), or composes
  `logging.NewContextHandler` over its own handler. The handler cannot see inside `slog.Any`
  maps or structs, nor free-text `error` values (the access line's `error` attribute is a named
  residual); a `request_id` pre-bound with `logger.With(...)` is outside the dedupe. **The access
  line:** `web.Logger` keeps `method`, `path`, `status`, `elapsed`, `remote_addr` (the raw peer),
  `error`, `aborted`, `hijacked` and adds `client_ip` (from `web.ClientIP`, when `TrustProxies`
  ran), `user_agent` (cut to 256 bytes on a UTF-8 boundary), `request_id`, `principal_type` and
  `principal_id` — the last principal authenticated during the request, including one the surface
  then refused; observability only, never an authorization input. Query strings stay out. Mount
  `web.Logger` INSIDE `web.TrustProxies` and `web.RequestID` (the `sdk/README.md` middleware
  order). `client_ip` and `user_agent` are personal data: the hatch is
  `LOG_REDACT_KEYS=client_ip,user_agent`; retention is the host's log pipeline. `web.RequestID`
  reuses an id already on the context, so a nested mount keeps the host's id and header.
- **`integrations/datastores/pgxdb` v0.14.0** (requires sdk v0.18.0; the released pgx stores
  resolve with it unchanged). `Config.DSN` (`DB_URL`) and `Config.Password` (`DB_PASSWORD`) are
  `Secret`s; `Config.Redacted()` stays the operator's display. Query logging stays refused in
  production — now for `LogQueries`, the new `LogQueryArgs` (`DB_LOG_QUERY_ARGS`, even alone) and
  any `Tracer` that is or wraps a `*LoggingQueryTracer`; `pgxdb.ErrQueryLoggingRefused` keeps its
  name, its text is now "production refuses query logging" and the message names
  `Config.LogQueries (DB_LOG_QUERIES)` (match the sentinel, never the text). In development the
  query line is `sql`, `arg_count`, `arg_types` — never values — and a failed query logs
  `sqlstate`/`constraint`/`command_tag` (or `error_type`), never the error text;
  `DB_LOG_QUERY_ARGS=true` beside `DB_LOG_QUERIES=true` brings back `args` and the error text with
  a WARN, and alone it is inert (one WARN, no tracer). `request_id` reaches query lines through
  the context when the logger is `logging.New`'s (the tracer does not add it).
  `NewLoggingQueryTracer(logger)` keeps its signature with the safe default;
  `NewLoggingQueryTracerWithConfig(LoggingQueryTracerConfig{Logger, LogArgs})` is the widening.
- **`integrations/datastores/turso` v0.9.0** (requires sdk v0.18.0; the released turso stores
  resolve with it unchanged). `Config.URL` and `Config.AuthToken` are `Secret`s (a URL can embed
  the token). **New boot failure (row 10):** a production `Open` with `LogQueries` or
  `LogQueryArgs` set fails `turso.ErrQueryLoggingRefused` naming `Config.LogQueries
  (DB_LOG_QUERIES)`, joined with any TLS refusal — the pgxdb rule since S1, no waiver; unset it
  (run the second "Read first" grep). Development query lines are arity/types as for pgxdb.
  **Every turso host scaffolded by `gopernicus init` at workshop v0.8.0 or earlier** has
  `URL: os.Getenv("TURSO_DATABASE_URL")` and `AuthToken: os.Getenv("TURSO_AUTH_TOKEN")` in
  `cmd/server/main.go` and `workshop/migrations/main.go` (the scaffold's migrator) and wraps all
  four in `sdk.Secret(...)`; a store scaffolded by `new pocket` at v0.8.0 or earlier wraps the
  `dsn`, `url` and `token` variables in its conformance test the same way.
- **The eight integration modules** (each requires sdk v0.18.0): `integrations/kvstores/goredis`
  v0.4.0 (`Config.URL`, `Config.Password`), `integrations/email/sendgrid` v0.5.0
  (`Config.APIKey`), `integrations/filestorage/s3` v0.3.0 (`Config.SecretAccessKey` only;
  `AccessKeyID` is an identifier), `integrations/filestorage/gcs` v0.3.0
  (`Config.CredentialsJSON`), `integrations/oauth/google` and `integrations/oauth/github` v0.3.0
  (`Config.ClientSecret`) — wrap a `string` variable in `sdk.Secret(v)`.
  `integrations/authentication/firebase` v0.2.0: `ClientConfig.CredentialsJSON` moves from
  `[]byte` to `Secret`, so EVERY builder changes — `sdk.Secret(string(b))` (R-4).
  `integrations/cryptids/golang-jwt` v0.3.0: `New(secret string, …)` is unchanged; `*Signer`,
  like `*google.Provider`, `*github.Provider`, `*sendgrid.Sender` and `*gcs.Store`, now prints a
  redacted summary through every `fmt` verb and logs a group without the secret.
- **`workshop/gopernicus` v0.9.0.** Bump the tool pin (`go get -tool
  github.com/gopernicus/gopernicus/workshop/gopernicus@v0.9.0`) or run `@v0.9.0 guard --posture
  .`; P7 now flags an uncommented true `DB_LOG_QUERIES`/`DB_LOG_QUERY_ARGS` in `.env.example`
  (comment the line). A new scaffold pins sdk v0.18.0 and pgxdb v0.14.0 or turso v0.9.0, wraps
  its connector env reads in `sdk.Secret`, ships commented `# LOG_REDACT_KEYS=` (and, pgx only,
  `# DB_LOG_QUERIES=true` / `# DB_LOG_QUERY_ARGS=true`), and its README gains "Secrets and logs".
  The CLI's `version` still prints the `0.0.0-dev` placeholder.
- **Examples (this PR).** Every example builds and vets at `GOWORK=off` on the S6 tags. Pins moved
  (explicit `GOWORK=off go get`, then `go mod tidy`, which only pruned `go.sum`): `auth-cms` sdk
  v0.17.0→v0.18.0, pgxdb v0.13.0→v0.14.0, turso v0.8.0→v0.9.0, golang-jwt v0.2.0→v0.3.0; `cms`
  sdk v0.17.0→v0.18.0, turso v0.8.0→v0.9.0; `external-identity` sdk v0.17.0→v0.18.0;
  `goth-showcase` sdk v0.17.0→v0.18.0; `jobs-minimal` sdk v0.17.0→v0.18.0; `minimal` sdk
  v0.17.0→v0.18.0; `production-baseline` sdk v0.17.0→v0.18.0, pgxdb v0.13.0→v0.14.0, golang-jwt
  v0.2.0→v0.3.0. No non-S6 pin moved; no example pins the other seven PR-3 modules (§1 is their
  real-binary proof). `examples/cms` did not build at `GOWORK=off` before this repin (its SMTP
  and connector wraps, carried in #182 and #183, need sdk v0.18.0 and turso v0.9.0); it does
  now. The code-side example changes landed with their PRs: the cms SMTP and connector wraps,
  the auth-cms demo attribute rename and test literal wraps, and production-baseline's commented
  `# LOG_REDACT_KEYS=`.

**Verification record.**
- CI `check` and `vuln` (push and pull_request events) passed on the final run of every PR,
  #182–#186; the CodeQL analyses passed on #182, #184, #185 and #186 and flagged one alert on
  #183 (Known and deferred). `make guard` runs thirty-six guards: S6 adds G37
  `guard-credential-fields-secret` (credential-named `string`/`[]byte` config fields in
  `integrations/` and `sdk/` must be `sdk.Secret`), whose self-check compares the exact matched
  set; planted fixtures proved a `string` and a `[]byte` field caught, `json:"` lines, `_test.go`
  files and the excluded `oauth.go` ignored, and a dead or over-broad pattern failing.
- Live legs on merged `main`, counted with `go test -json` (pass / fail / skip). Before steps 2
  and 3, the eleven store legs @ `59c318d8`, pgx bare and schema-qualified: authentication 432 /
  0 / 1 each and turso 737 passed; authorization 783 / 0 / 1 each and turso 1006 passed; cms 59
  (bare) and 60 (schema-qualified) and turso 49 passed; events 18 each and turso 13 passed; jobs
  120 each and turso 103 passed; secrets 30 each; no failures. The connectors on the #183 branch:
  pgxdb hermetic 352 / 0 / 47 and live 501 / 0 / 0 (fresh scratch PostgreSQL 17), turso hermetic
  219 / 0 / 0 and live `-tags=integration` 225 / 0 / 0 (fresh libSQL). Before step 4, goredis @
  `d54de133` with `-race`: 158 / 0 / 0, including `TestLive_OpenAuthenticatesAsTheNamedUser`
  (the revealed password and URL reach a real Redis); the other seven PR-3 suites are hermetic
  and green, with three wire assertions (google and github exchange and refresh send the real
  `client_secret`).
- Every example builds and vets at `GOWORK=off` against the tags (`go version -m` on the built
  binaries shows sdk v0.18.0, pgxdb v0.14.0, turso v0.9.0 and golang-jwt v0.3.0 where used), then
  builds in the workspace; `make guard` and `make check` pass on the repinned tree;
  `cd workshop/gopernicus && go test ./...` passes with the scaffold README's guard line at
  `@v0.9.0`.
- From the PR records (real binaries, development mode unless named). §1, scratch programs at
  `GOWORK=off`: every migrated `Config` field and the five adapters print `[redacted]` under
  `%v %s %+v %#v %q` (and `%x` on a bare `Secret`), in slog text and JSON and in `json.Marshal`
  — `hunter2` appears exactly once, on the deliberate `Reveal()` line (#182, #184);
  `LOG_REDACT_KEYS=probe_only` parsed by `ParseEnvTags` redacts `probe_only` beside the
  defaults; a composed `[redacted]` password, DSN or URL fails `ErrRedactedSecret` from SMTP
  `Probe` and from both connectors' `Open` before any dial; turso through `ParseEnvTags` on a
  `file:` URL in production → `turso: production refuses query logging: Config.LogQueries
  (DB_LOG_QUERIES); unset it`, in development the posture WARN and `arg_count=3
  arg_types="[string []uint8 int]"` with no values (#183). §2(b)
  production-baseline with `DB_LOG_QUERIES=true`: first-login lines carry `sql`, `arg_count`,
  `arg_types` and `request_id`, while the email, the hash, `duplicate key`, `Key (`, `args=` and
  the database password appear zero times; a raced first login logs `sqlstate=23505
  constraint=external_subjects_pkey`; `DB_LOG_QUERY_ARGS=true` brings `args` and the WARN; §2(d)
  the real production boot with `DB_LOG_QUERIES=true` exits 1 with the refusal joined to the TLS
  refusal (#183), and a copy whose `.env.example` uncomments `DB_LOG_QUERIES=true` gets one P7
  finding, exit 1 (#186). §3 auth-cms (`TRUSTED_PROXY_COUNT=1`): request lines carry
  `client_ip=203.0.113.9`, `user_agent`, `request_id` and `principal_*` for a user and a service
  account; `/demo/external/mfa` (authenticated, then refused) still names the user; an expired
  session carries no principal; `AUTH_EXTERNAL_DEMO=1` prints `seeded_api_key` and
  `/demo/external/hybrid` answers 200 with it; a scratch host over the cms `BuildRouter` stack
  shows inner and outer lines sharing one `request_id` (#182).
- This PR, boot from the tags (`GOWORK=off` binaries; development; a fresh scratch PostgreSQL 17
  database; `TRUSTED_PROXY_COUNT=1`). `examples/production-baseline`, §2(a) — `curl
  /healthz?probe=1` with `X-Forwarded-For: 203.0.113.9`, `X-Request-ID: s6probe` and a
  300-byte user agent gave exactly one line, `request_id` once, `user_agent` cut to 256 bytes,
  no `principal_*`, no query string (the agent shortened here):
  `time=2026-10-24T10:50:27.312-07:00 level=INFO msg=request method=GET path=/healthz status=200
  elapsed=2.215084ms remote_addr=127.0.0.1:56989 client_ip=203.0.113.9 user_agent=UUUU…(256
  bytes) request_id=s6probe`. §2(c) — a dev-issuer token for `u1`, then `POST
  /authorization/roles` with `X-Forwarded-For: 203.0.113.9` and `X-Request-ID: s6who`
  (authenticated, then refused by authorization) gave the one "who, what, from where" line, the
  CC7 evidence: `time=2026-10-24T10:50:35.015-07:00 level=WARN msg=request method=POST
  path=/authorization/roles status=403 elapsed=33.143667ms remote_addr=127.0.0.1:57027
  client_ip=203.0.113.9 user_agent=curl/8.14.1 request_id=s6who principal_type=user
  principal_id=1FkwYv2mKdy2MW4jHx2fF`. The bearer token and the DSN (with
  its password) appear zero times in the run's log. Its `pgxdb.Config`, parsed by
  `ParseEnvTags` from the same environment and printed with `%+v` from a pgxdb v0.14.0 consumer:
  `{DSN:[redacted] Host: Port: User: Password:[redacted] Database: SSLMode: … StatementTimeout:30s
  LockTimeout:5s LogQueries:false LogQueryArgs:false …}`; `Redacted()` printed the target with
  `REDACTED` as the password. `examples/cms`, §4 (a fresh local libSQL server; SMTP pointed at a
  scratch relay that checks the PLAIN credential): the binary boots with its two wrapped connector
  literals; a contact-form POST answered 200 and the relay recorded the `AUTH PLAIN` password as
  matching (`Reveal()` on the PLAIN path), never the `[redacted]` marker; the admin
  `/inquiries` page load logged one line with `client_ip=203.0.113.9` and one `request_id`; the
  SMTP password appears zero times in the log. That host has no identity pocket (its admin is
  ungated by acknowledgement, development only), so its lines carry no `principal_*` — §4's
  principal expectation does not apply to it; §3 is the principal proof.

**Owner rulings.** Tags are annotated, not signed (ruling of 2026-10-22). **R-1:** `DSN` and `URL`
migrate too (a DSN carrying a password is the finding; `Redacted()` stays the display path).
**R-2:** production keeps refusing any query logging, on both connectors (turso gains the rule,
checked before its TLS early return and joined with the TLS refusal); arity/types is the
development default. **R-3:** #185 is
the slice's one store-repin PR — twelve store modules, pins only, no tags. **R-4:** firebase
`CredentialsJSON` moves to `Secret` (an eighth PR-3 module and tag; every builder breaks).
**R-5:** no off switch for the redacting handler. **The `NewSMTP` adaptation:** the constructor
keeps its `*SMTP` signature, and the composed-password refusal surfaces from `Probe` and `Send`.
Every lead ruling taken during the build is in the executed plan's "Build-time deviations"; each
one a host can see is folded into the rows and notes above.

**Known and deferred.**
- **CodeQL alert 14 — OWNER ACTION.** `go/clear-text-logging` at
  `integrations/datastores/turso/tracers.go:47`, the `args` attribute that only the
  development-only `LogQueryArgs` opt-in writes (with a WARN); production refuses query logging at
  `Open` (R-2), which is the control. It is the same flow as the earlier alert 13
  (`tracers.go:32`, ungated before S6), which now reads fixed. The build session could not
  dismiss it; the owner dismisses alert 14 as "won't fix" with that reason. CodeQL is not a
  required check; #183 merged with `check` and `vuln` green. Two other open clear-text-logging
  alerts on `main` predate S6 and are unrelated (`sdk/capabilities/notify/console.go`, the
  development console sender; `examples/auth-cms/cmd/server/main.go`).
- The redacted-secret check matches a literal `[redacted]` only, not a URL-escaped
  `%5Bredacted%5D`; such a value fails at the wire on authentication instead (follow-up).
- `examples/auth-cms/cmd/server/jobs_delivery_live_test.go` has not compiled under `-tags
  livedelivery` since #90 (pre-existing, thirteen stale calls; owner follow-up).
- `examples/auth-cms/README.md` Leg 3's "1s TTL" is stale (golang-jwt applies a one-minute
  leeway; follow-up).
- `AUDIT.md`'s historical snippets still show `string` credential fields, on purpose (records).
- Workshop, pre-existing: the pocket scaffold's `core.go.mod.tmpl` pins sdk v0.15.0 (the core
  uses no `Secret`) and `pgxVersion = "v5.8.0"` is stale against v5.9.2 (tidy lifts it);
  `workshop/documentation/docs/workshop/commands.md` still shows `@v0.6.0 guard --posture .` and
  `workshop/gopernicus/README.md` `@v0.6.1` (the scaffold README moves to `@v0.9.0` here).
- Out of S6 by the plan: `.env` in production (FSK-4, closed by S1), console transports (FSK-8),
  OTel sampling (FSK-5), redacting inside `slog.Any` values or error text, `oauth.Tokens`, an off
  switch, log retention (the host's pipeline).

## Transport and abuse hardening (SOC 2 slice S5 — published 2026-10-23)

Six annotated tags (not signed), each cut at its PR's squash-merge commit on `main`, pushed,
THEN polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an empty
module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build <module>/...`).
Every one resolved on the first poll. Design and rulings:
[plans/s5-transport-abuse-hardening.md](plans/s5-transport-abuse-hardening.md) (rev 2, ratified
2026-10-23; its "Build-time deviations" section records the lead rulings taken during the
build). Under the owner's 2026-10-23 process ruling this section is the whole record: there is
no handoff file and no manifest. The handoff notes below say what a host does on upgrade; hosts
adopt them once, at the roadmap's end. The operator surface is the docs site's *Production
posture* guide: "Added by S5" (the refusals and the two waivers) and "Transport and abuse (S5)".

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `sdk/v0.17.0` | `ba561be1` | #177 (PR-1) | OK, poll 1 |
| 2 | `integrations/datastores/pgxdb/v0.13.0` | `c9882f13` | #176 (PR-4; cut after the six pgx store legs on merged `main`) | OK, poll 1; also built the MVS set (below) |
| 3 | `integrations/cryptids/go-oidc/v0.2.0` | `815e8435` | #175 (PR-5) | OK, poll 1 |
| 4 | `pockets/authentication/v0.24.0` | `99b9e30d` | #180 (PR-2; cut after the auth store legs on merged `main`) | OK, poll 1; PR-7 rebuilt it cold beside `stores/pgx` v0.11.0 and `stores/turso` v0.10.0 |
| 5 | `pockets/cms/v0.6.0` | `e36add4a` | #178 (PR-3; its `AllowImplicitPublic` line landed in #177; cut after the cms store legs) | OK, poll 1; PR-7 rebuilt it cold beside `stores/pgx` v0.5.0 and `stores/turso` v0.4.0 |
| 6 | `workshop/gopernicus/v0.8.0` | `764d5d6e` | #179 (PR-6) | OK, poll 1; a cold `init --db pgx` from `@v0.8.0` requires sdk v0.17.0 and pgxdb v0.13.0, builds, and its `guard` is clean (25 rules, 2 packages) |

The trains are independent, so the tags were cut as they became ready — 3, 2, 1, 5, 6, then 4 —
never a dependent before its dependency. PR-7 is this record and the example repins.

**Step 2's MVS set.** One scratch consumer resolved pgxdb v0.13.0 beside every released pgx
store — authentication `stores/pgx` v0.11.0, authorization `stores/pgx` v0.21.0, cms
`stores/pgx` v0.5.0, events `stores/pgx` v0.4.0, jobs `stores/pgx` v0.7.0 and secrets
`stores/pgx` v0.2.1 — `go.mod` selected v0.13.0, `go mod verify` passed and `go build` succeeded.
PR-7 re-ran the set from an empty module cache with the same result.

**Not tagged.** Every store module (R-1: no store code changed; MVS lifts a store's core and
pgxdb pins inside a host), `integrations/datastores/turso` (one README sentence),
`pockets/authorization`, `pockets/jobs`, `pockets/events`, `pockets/secrets`,
`pockets/cms/views/goth`, `pockets/authentication/views/goth`, `ui/goth` and `pockets`. No
migration. Nothing retracts. **C1 is not in S5:** the `pgxdb.QuerierFrom` foreign-`*DB` refusal
(the roadmap carry-over) is deferred to the per-tenant authorities plan (R-2); the carry-over
stays open and the confidentiality row C1 is not claimed.

**Upgrade notes.** Ten behaviour flips with a hatch, five observable changes without a knob
(rows 2, 3, 12, 15, 16) and three additive rows (4, 7, 18). Rows 11 (production), 12 (any mode),
16 (only with a double setting) and 17 (a discovering http issuer) fail construction. Row 17 is
reworded from the plan per build-time deviation 1.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `DecodeJSON` bounded at 1 MiB | bodies > 1 MiB 413; an outer larger `MaxBytesReader` no longer governs | `DecodeJSONLimit[T](r, n)` | sdk@v0.17.0 |
| 2 | fixed decode-error text | 400 carries `ReadBody`'s sentences, never Go type names | none (match on `code`, not text) | sdk@v0.17.0 |
| 3 | `Panics` answers JSON to JSON clients | 500 body is JSON when `Accept`/`Content-Type` is JSON | none | sdk@v0.17.0 |
| 4 | static `nosniff`; `X-Request-ID` validated; unparsable forwarded hop → peer; 503 `Retry-After: 5` | additive | none | sdk@v0.17.0 |
| 5 | `X-Real-IP` ignored by default | a proxy setting only `X-Real-IP` attributes the peer; auth WARN names the knob | `TrustProxiesWithConfig(TrustProxiesConfig{…, TrustRealIPHeader: true})` | sdk@v0.17.0 |
| 6 | page cache stores only `public`/`s-maxage` | header-less or `max-age`-only HTML no longer cached | `PageConfig.AllowImplicitPublic: true` (cms sets it itself) | sdk@v0.17.0 |
| 7 | `no-store` on every `/auth` response; `X-Real-IP` posture WARN | additive | none | authentication@v0.24.0 |
| 8 | per-route IP budgets | new 429s on register/verify/reset/verify-link/native OAuth | `AbuseProtectionConfig.PublicRouteLimits.<Route>` | authentication@v0.24.0 |
| 9 | origin gate on the JSON arm | a same-site/cross-origin SPA calling register/verify/reset/token/verify-link without an allowlisted origin gets 403 | `BrowserConfig.AllowedOrigins` / `OriginPolicy` | authentication@v0.24.0 |
| 10 | limiters fail closed in every mode | a limiter outage 503s the budgeted routes incl. silent refresh and host `RateLimitByIP` routes | `PublicRouteLimiterFailOpen: true` (production: posture waiver WARN) | authentication@v0.24.0 |
| 11 | cookie `Domain` refused in production | `New` fails `ErrCookieDomainRefused` | `BrowserConfig.CookieDomainAcknowledged` | authentication@v0.24.0 |
| 12 | cookie `Domain` + namespace rule refused in every mode | `New` fails `ErrCookieDomainWithNamespaceRule` | none — remove one (R-3) | authentication@v0.24.0 |
| 13 | upload allow-list by sniffed type | non-listed uploads 400 naming the allowed types | `Config.AllowedMediaTypes` | cms@v0.6.0 |
| 14 | upload cap | > 32 MiB 413 | `Config.MaxUploadBytes` | cms@v0.6.0 |
| 15 | media `nosniff` + `Content-Disposition`; PDFs as `attachment` | visitors download PDFs instead of viewing inline | none (R-6 may pick inline + `CSP: sandbox`) | cms@v0.6.0 |
| 16 | timeout knobs; DSN-and-Config double setting refused | `Open` fails `ErrInvalidInput` only when both name the parameter | set one | pgxdb@v0.13.0 |
| 17 | https-only issuer when discovering | a non-loopback http issuer that discovers fails `New` before any fetch, naming the fix; v0.1.0 already failed a container-hostname issuer whose `jwks_uri` is on the same host (at the key-set check), so only an http issuer whose discovered key set is https or loopback newly fails; a pinned `WithJWKSURL` is exempt | `WithInsecureIssuer()` (dev only; also admits a plain-http discovered `jwks_uri`) | go-oidc@v0.2.0 |
| 18 | P2 second name; P5 three names; scaffold migrator zeroes timeouts | additive / informational | none | workshop@v0.8.0 |

**Handoff notes — what a host does on upgrade, per module** (applied at the roadmap's end).
- **`sdk` v0.17.0.** Grep for `web.DecodeJSON`: a route that must accept more than 1 MiB moves to
  `DecodeJSONLimit[T](r, n)` (`n <= 0` is refused — there is no unbounded mode). Clients that
  match decode-error text match on `code`. A `cacher.Pages` mount whose handlers send no
  `Cache-Control: public` or `s-maxage` stops storing: add the directive, or set
  `PageConfig.AllowImplicitPublic: true` when the routes are public by construction (cms does,
  on its own mount). An upstream that mints `X-Request-ID` values outside 1–128 characters of
  `[A-Za-z0-9._-]` sees them replaced by a fresh id. **The `X-Real-IP` flip and its WARN, together
  (rows 5 and 7):** `web.TrustProxies(n)` now reads `X-Forwarded-For` only, so behind a proxy
  that sets only `X-Real-IP` every client resolves to the proxy — and the new authentication
  budgets then key the whole site to one IP. In production the authentication pocket logs one
  WARN naming `web.TrustProxiesConfig.TrustRealIPHeader` the first time a request carries
  `X-Real-IP`, no `X-Forwarded-For`, and resolves to the peer (development logs nothing; the
  observer is production-only). The fix is `web.TrustProxiesWithConfig(web.TrustProxiesConfig{
  TrustedProxyCount: n, TrustRealIPHeader: true})`, which reads `X-Real-IP` only when the count
  is at least 1 and `X-Forwarded-For` is absent; run workshop v0.8.0's guard, since v0.7.0's P2
  does not know the second name.
- **`pockets/authentication` v0.24.0** (requires sdk v0.17.0; `stores/pgx` v0.11.0 and
  `stores/turso` v0.10.0 resolve with it unchanged; no migration). Read the README's "Per-IP
  budgets by route" table; test fixtures and load scripts that post register or verify from one
  IP raise `AbuseProtectionConfig.PublicRouteLimits` (fields are `ratelimiter.Limit`, zero takes
  the default, no off switch and no env tags — set them in code). An SPA on another origin that
  calls register, verify, forgot, reset, token or verify-link adds that origin to
  `BrowserConfig.AllowedOrigins` (or `OriginPolicy`) BEFORE the upgrade; the native verify-link
  shares the `verify_link` budget but, like every native route, is not origin-gated. Decide the
  limiter posture: every budget now fails closed in every mode, so run a highly available
  limiter, or set `PublicRouteLimiterFailOpen: true` as a literal (production logs one
  `posture_waiver=PublicRouteLimiterFailOpen` WARN at `New`; `guard --posture` v0.8.0 prints it
  as `posture_waiver`). A production host that sets `SessionCookie.Domain` sets
  `BrowserConfig.CookieDomainAcknowledged: true` (a literal; production logs one
  `posture_waiver=CookieDomainAcknowledged` WARN at `New`, P5 prints its `ack`) or drops the
  `Domain`; a `Domain` beside an `OriginPolicy` namespace rule must lose one of the two. No host
  action for `no-store`.
- **`pockets/cms` v0.6.0** (requires sdk v0.17.0; `stores/pgx` v0.5.0 and `stores/turso` v0.4.0
  unchanged). The sniffed bytes decide an upload's type — the client's `Content-Type` is never
  consulted. A host that accepts types beyond png, jpeg, gif, webp and pdf (SVG is absent by
  design) sets `Config.AllowedMediaTypes`; an unparsable entry fails `Register`. A host that
  uploads more than 32 MiB sets `Config.MaxUploadBytes` (negative fails `Register`) and keeps its
  edge body limit at or above it. Assets already stored with an unlisted type are still served,
  as `attachment`; PDFs now download instead of opening inline (R-6). Nothing changes on the
  page-cache wire. The pocket's docs are `docs/pockets/cms.md` on the docs site.
- **`integrations/datastores/pgxdb` v0.13.0.** Optional knobs `DB_STATEMENT_TIMEOUT` and
  `DB_LOCK_TIMEOUT` (`Config.StatementTimeout`, `Config.LockTimeout`; zero leaves the server
  default; negative or under 1 ms fails `Open`). They are for the server process only: a
  migrator that parses `pgxdb.Config` from the server's environment zeroes both after
  `ParseEnvTags` (the scaffold and `examples/production-baseline` do) or an index build inherits
  the statement timeout. Set each in one place — a field set while the DSN, its `options`,
  `PGOPTIONS` or a service-file entry also names the parameter fails `Open` with
  `ErrInvalidInput` (a startup parameter would otherwise silently win). Behind PgBouncer
  transaction mode, which rejects unknown startup parameters, use `ALTER ROLE … SET` instead. A
  timed-out statement surfaces as an unmapped SQLSTATE (57014, or 55P03 for a lock), so a 500.
  Turso has no statement timeout (`BusyTimeout` is its knob).
- **`integrations/cryptids/go-oidc` v0.2.0.** A discovering issuer is https, or http on
  loopback. A development issuer on a container hostname either pins its key set with
  `WithJWKSURL` or opts in with `WithInsecureIssuer()` (one WARN when used; the pocket's
  production engine refuses http issuers anyway). The refusal wraps `sdk.ErrInvalidInput`; there
  is no new error variable.
- **`workshop/gopernicus` v0.8.0.** Bump the tool pin (`go get -tool
  github.com/gopernicus/gopernicus/workshop/gopernicus@v0.8.0`) or run `@v0.8.0 guard --posture
  .`. P2 accepts `web.TrustProxiesWithConfig(` beside `web.TrustProxies(`; P5 recognizes
  `CookieDomainAcknowledged` (`ack`) and the two FailOpen flags, whose `true` literal prints
  `posture_waiver` (`posture_waiver (conditional)` under `if`/`switch`) — the P5 sentence in
  `examples/README.md` changed with it. A new scaffold pins sdk v0.17.0 and pgxdb v0.13.0, its
  migrator zeroes the two timeouts, and its `.env.example` ships them commented out. The CLI's
  `version` still prints the `0.0.0-dev` placeholder.
- **Examples (this PR).** Every example builds at `GOWORK=off` on the S5 tags (pins below); wiring
  is unchanged — the defaults are the safe ones. `examples/production-baseline`'s migrator zeroes
  `StatementTimeout` and `LockTimeout` after `ParseEnvTags`, and its `.env.example` ships
  `DB_STATEMENT_TIMEOUT=30s` and `DB_LOCK_TIMEOUT=5s` active for the server. Pins moved (explicit
  `GOWORK=off go get`, then `go mod tidy`): `auth-cms` sdk v0.16.0→v0.17.0, authentication
  v0.23.0→v0.24.0, cms v0.5.0→v0.6.0, pgxdb v0.12.0→v0.13.0, go-oidc v0.1.0→v0.2.0; `cms` sdk
  v0.16.0→v0.17.0, cms v0.5.0→v0.6.0; `external-identity` sdk v0.14.0→v0.17.0, authentication
  v0.19.1→v0.24.0, go-oidc v0.1.0→v0.2.0; `goth-showcase` sdk v0.9.0→v0.17.0; `jobs-minimal` sdk
  v0.16.0→v0.17.0; `minimal` sdk v0.16.0→v0.17.0, cms v0.5.0→v0.6.0; `production-baseline` sdk
  v0.16.0→v0.17.0, authentication v0.23.0→v0.24.0, pgxdb v0.12.0→v0.13.0, go-oidc
  v0.1.0→v0.2.0. No non-S5 pin moved. `external-identity` and `goth-showcase` did not build at
  `GOWORK=off` before this repin (their code had moved past sdk v0.14.0 / v0.9.0); they do now.

**Verification record.**
- CI `check` and `vuln` (push and pull_request events) and the CodeQL analyses passed on the
  final run of every code PR, #175–#180. `make guard` runs thirty-five guards (S5 adds none).
- Live legs, every leg on a fresh database of a scratch PostgreSQL 17 and a fresh local libSQL
  server, counted with `go test -json`; the store and pocket legs ran on merged `main` before
  each tag. Pass / fail / skip: pgxdb's own live leg (on the #176 branch) 490 / 0 / 0 (`SHOW
  statement_timeout` equals the knob, `pg_sleep` past it returns 57014, a lock wait past
  `lock_timeout` returns 55P03, both double-setting forms refused). The six pgx store legs @
  `c9882f13`, before step 2, bare and schema-qualified: authentication 432 / 0 / 1 each,
  authorization 783 / 0 / 1 each (the pre-existing `TestCollationControlsOrdering_NonC`, which
  needs a non-C DSN), cms 59 / 0 / 1 bare (`TestLive_TruncateCascadeStaysInSchema` runs only
  schema-qualified) and 60 / 0 / 0, events 18 / 0 / 0 each, jobs 120 / 0 / 0 each, secrets 30
  / 0 / 0 each. cms @ `e36add4a`, before step 5: pgx 59 / 0 / 1 bare and 60 / 0 / 0 schema,
  turso 49 / 0 / 0, core 147 / 0 / 0. Authentication @ `99b9e30d`, before step 4: pgx 432 / 0
  / 1 bare and 432 / 0 / 1 schema (the collation skip), turso `-tags=integration` 737 / 0 / 0,
  core 2813 / 0 / 3 (three gated `TestInProcessCharacterization` subtests).
- Every example builds at `GOWORK=off` against the tags (`go version -m` on the built binaries
  shows sdk v0.17.0, authentication v0.24.0, cms v0.6.0, pgxdb v0.13.0, go-oidc v0.2.0 where
  used); `examples/external-identity` `go test ./...` and `go vet ./...` pass at `GOWORK=off`.
  `make check` passes on the repinned tree, and `make guard-host-posture` is clean over the seven
  examples (the baseline prints exactly one `ack`, `MaintenanceRunnerAcknowledged`).
- Run-and-look on those binaries (this PR). `examples/auth-cms` (development, memory limiter,
  `TRUSTED_PROXY_COUNT=1`): `POST /auth/login` 401 and `GET /auth/login` 200 both carry
  `Cache-Control: no-store`; twelve registers from one forwarded IP answered ten 400s, then
  two 429s with `Retry-After: 60`; a cross-site register 403 `origin_rejected`, the same POST
  without `Origin`/`Sec-Fetch-Site` 400; a 300-character `X-Request-ID` came back as a fresh
  32-character id; 10,100 refreshes from distinct forwarded IPs answered 10,000 401s, then 503
  with `Retry-After: 5` and one `public route rate limiter failed … fail_open=false` WARN per
  refusal. `examples/cms` (development, fresh libSQL): an HTML file named `.png` 400
  "unsupported file type; allowed: image/png, image/jpeg, image/gif, image/webp,
  application/pdf"; a 40 MiB upload 413 "upload too large"; a PNG sent as `text/html` stored
  and served `image/png`, `inline; filename=real.png`, `nosniff`; a PDF served `attachment;
  filename=doc.pdf`, `nosniff`; `GET /` twice MISS then HIT with no `Cache-Control` on the
  wire. `examples/minimal`: a goth asset carries `nosniff` (its `SecurityHeaders` sets it too;
  the sdk consumer below has none); `GET /` HIT with no `Cache-Control`; a valid request id is
  reused, a 300-character one replaced. `examples/production-baseline`: the migrator run with
  `DB_STATEMENT_TIMEOUT=-1s DB_LOCK_TIMEOUT=-1s` reached the dial (zeroed) while the server
  with the same environment refused `StatementTimeout cannot be negative` (honoured). A
  scratch consumer of sdk v0.17.0 (`DecodeJSON` route, `Panics`, a failing limiter): a
  malformed body 400 "request body must be a JSON object"; a 2,000,000-byte body 413
  `payload_too_large`; a panic JSON 500 with `Accept: application/json`, HTML otherwise;
  static `nosniff`; a failing limiter 503 `Retry-After: 5`; `X-Forwarded-For: not-an-ip` and
  `X-Real-IP` alone both resolve to the peer, a bracketed ported IPv6 hop to `2001:db8::5`. A
  scratch consumer of go-oidc v0.2.0: a discovering `http://` container-hostname issuer
  refused before any fetch, accepted with a pinned https `WithJWKSURL`, and admitted to
  discovery with one WARN under `WithInsecureIssuer()`.
- From the PR records: production-baseline cannot boot locally in production (TLS PostgreSQL,
  an https issuer, a hardcoded `Secure: true` and no cookie-domain variable), so the cookie
  `Domain` refusal and the production `X-Real-IP` WARN (exactly one for two requests) were proven
  in-process over its real production composition (#180); a cold scaffold's migrator with
  `DB_STATEMENT_TIMEOUT=30s DB_LOCK_TIMEOUT=-1s` reached the dial while its server refused the
  negative value, and a planted host printed `posture_waiver … PublicRouteLimiterFailOpen` with
  `P2 ok` on `TrustProxiesWithConfig` (#179).

**Owner rulings.** Tags are annotated, not signed (ruling of 2026-10-22). **R-1:** no store tag
and no repin PR, but the live legs were mandatory before pgxdb, authentication and cms (above).
**R-2:** `QuerierFrom` deferred to the per-tenant authorities plan; C1 not claimed. **R-3:** a
cookie `Domain` beside a namespace rule is refused in every mode, no waiver. **R-4:** the default
budgets as recommended (register 10, verify 20, reset 20, verify-link 20, refresh 30 on the IP
arm, decline 10, native OAuth 40 on one shared key; forgot and `/auth/token` keep their logic
per-IP budgets). **R-5:** the page cache stores only on `public` or `s-maxage`, checked at commit
and `validPage`; cms sets `AllowImplicitPublic` on its own mount and emits no directive. **R-6:**
PDFs as `attachment`. Every lead ruling taken during the build is in the executed plan's
"Build-time deviations"; each one a host can see is folded into the rows and notes above.

**Known and deferred.**
- `pgxdb.QuerierFrom` foreign-`*DB` refusal: deferred to the per-tenant authorities plan (R-2;
  the correct shape is in the executed plan's Notes); the carry-over stays open, C1 not claimed.
- FHS-6's fail-open counter is not built (the flag now fails closed; a waiver logs per failure).
- Per-IP keys use the full IPv6 address; a `/64` bucketing option on `TrustProxiesConfig` is a
  follow-up (S6 or later).
- `pgxdb`'s existing `hasSessionTimeZone` substring check also matches `-c log_timezone=…`
  (follow-up; the new timeout check uses an exact-name parser).
- cms renders a refused upload's error text raw at 400 (pre-existing), so the allow-list message
  ends `: invalid input`.
- A pathological limiter that wraps `sdk.ErrInvalidInput` would make the refresh arm answer 400,
  not 503 (left).
- `examples/production-baseline`'s `cmd/bootstrap` parses `pgxdb.Config` and keeps the server
  timeouts; it runs one grant, so they do not bite.
- `workshop/documentation/docs/workshop/commands.md` still shows `@v0.6.0 guard --posture .`
  for hosts without a tool pin (the scaffold README moves to `@v0.8.0` here).
- Out of S5: escalating lockout (S4), `web.Logger` on `web.ClientIP` (S6), an SSE stream cap and
  explicit `ReadHeaderTimeout`/`MaxHeaderBytes` (an sdk follow-up issue, owner files).

## Retention and erasure (SOC 2 slice S3 — published 2026-10-23)

Fourteen annotated tags (not signed), each cut at its PR's squash-merge commit on `main`,
pushed, THEN polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an
empty module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build
<module>/...`). Every one resolved on the first poll. Design and rulings:
[plans/s3-retention-erasure.md](plans/s3-retention-erasure.md) (rev 2, ratified 2026-10-23;
its "Build-time deviations" section records the lead rulings taken during the build). Host
handoff: [plans/soc2-s3-retention-erasure-handoff.md](plans/soc2-s3-retention-erasure-handoff.md),
written now and applied at the roadmap's end. Manifest:
`plans/s3-retention-erasure-release-manifest.json` (from `go mod download -json` of the fourteen
tags). The operator surface is in the docs site's guides: *Production posture* "Retention (S3)"
(the retention table and the S3 refusals and WARNs), *Production database posture* "Two roles",
"Definer functions" and "Migrations", and *Operations runbooks* "Retention" and "Erase a person".

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `integrations/datastores/pgxdb/v0.12.0` | `31ac42b7` | #162 (PR-1; cut only after PR-2b's branch passed its live legs against the real 0024) | OK, poll 1; also built the MVS set (below) |
| 2a | `pockets/authentication/v0.23.0` | `f00205c5` | #169 (PR-2a) and #170 (PR-2b, no core-side change) — one core tag, cut at the later merge | OK, poll 1 |
| 2b | `pockets/authentication/stores/pgx/v0.11.0` | `371d47b4` | #171 (PR-2c; repin to core v0.23.0 + pgxdb v0.12.0) | OK, poll 1 |
| 2b | `pockets/authentication/stores/turso/v0.10.0` | `371d47b4` | #171 (repin to core v0.23.0; the turso connector pin does not move) | OK, poll 1 |
| 3a | `pockets/authorization/v0.30.0` | `66ecb03d` | #166 (PR-3) | OK, poll 1 |
| 3b | `pockets/authorization/stores/pgx/v0.21.0` | `78f68807` | #168 (PR-3b; repin to core v0.30.0) | OK, poll 1 |
| 3b | `pockets/authorization/stores/turso/v0.20.0` | `78f68807` | #168 | OK, poll 1 |
| 4a | `pockets/jobs/v0.7.0` | `627f95b1` | #165 (PR-4) | OK, poll 1 |
| 4b | `pockets/jobs/stores/pgx/v0.7.0` | `3388d16a` | #167 (PR-4b; repin to core v0.7.0) | OK, poll 1 |
| 4b | `pockets/jobs/stores/turso/v0.6.0` | `3388d16a` | #167 | OK, poll 1 |
| 5a | `pockets/cms/v0.5.0` | `acf7263d` | #163 (PR-5) | OK, poll 1 |
| 5b | `pockets/cms/stores/pgx/v0.5.0` | `00cc48c2` | #164 (PR-5b; repin to core v0.5.0) | OK, poll 1 |
| 5b | `pockets/cms/stores/turso/v0.4.0` | `00cc48c2` | #164 | OK, poll 1 |
| 6 | `workshop/gopernicus/v0.7.0` | `4beba21f` | #172 (PR-6) | OK, poll 1; a cold `init` from `@v0.7.0` requires pgxdb v0.12.0, builds, and its `guard` is clean (25 rules, 2 packages) |

The trains are independent, so the tags were cut in the order they became ready — 5a, 4a, 5b,
3a, 4b, 3b, then 1, 2a, 2b and 6 — never a dependent before its dependency. PR-7 (#173)
repinned every example to these tags; PR-8 is this record.

**Step 1's MVS set.** A host that adopts any S3 store pulls pgxdb v0.12.0 beside the pgx stores
it already runs, so step 1's cold verification resolved, in one scratch consumer, pgxdb v0.12.0
with every released pgx store — authentication `stores/pgx` v0.10.1, authorization `stores/pgx`
v0.21.0, jobs `stores/pgx` v0.7.0, cms `stores/pgx` v0.5.0, secrets `stores/pgx` v0.2.1 and
events `stores/pgx` v0.4.0 — and `go.mod` selected v0.12.0, `go mod verify` passed and `go
build` succeeded. The plan named authorization v0.20.0, jobs v0.6.0 and cms v0.4.0; those trains
had released first, so the set took their S3 tags. The scratch consumer builds the set and does
not run its tests; the stores' tests over pgxdb at its v0.12.0 content (the tree has not changed
since #162) ran in the workspace — the live legs and `live-stores` dispatches below.

**Not tagged.** `sdk` (v0.16.0), `integrations/datastores/turso` (v0.8.0; the turso stores use
`InTx`), `pockets/cms/views/goth` (v0.3.0; R-8), `pockets/events` and its stores,
`pockets/secrets` and `stores/pgx`, `pockets/authorization/stores/goredis` and `pockets`. The
store repins moved only the core pin: the authorization stores stay on pgxdb v0.11.0 / turso
v0.8.0, the jobs and cms stores on pgxdb v0.7.0 / turso v0.4.0, so pgxdb v0.12.0 reaches a host
through authentication `stores/pgx` v0.11.0 or the host's own require. Nothing retracts.

**Verification record.**
- CI `check` and `vuln` (push and pull_request events) and the CodeQL analyses passed on the
  final run of every PR, #162–#173. `make guard` runs thirty-five guards (G35 and G36 below).
- Live store legs, every full run against a fresh scratch PostgreSQL 17 and a fresh local libSQL
  server, counted with `go test -json` or the counted turso gate (`set -o pipefail; … go test -v
  -count=1 -tags=integration ./... 2>&1 | tee leg.log && ! grep -q -- '--- SKIP' leg.log` — the
  plan's rev-1 one-liner passed on skips and on failures). Pass / fail / skip:
  pgxdb (#162) 453 / 0 / 0 over fixture roles and a schema name that needs quoting; cms (#163)
  pgx bare 59 / 0 (one schema-gated skip by design), schema-qualified 60 / 0 / 0, turso 49 / 0 / 0;
  jobs (#165, re-run on merged `main` @ `627f95b1`) pgx bare and schema 120 / 0 / 0 each, turso
  103 / 0 / 0, `EXPLAIN` using `idx_job_queue_terminal` in both dialects; authorization (#166,
  re-run on `main` @ `66ecb03d`) pgx bare and schema 783 / 0 / 1 each (the pre-existing
  `TestCollationControlsOrdering_NonC`, which needs a non-C DSN), turso 1006 / 0 / 0 with
  `AUTHORIZATION_TURSO_DISPOSABLE_URL` set — **S2's authorization libSQL leg is re-proven here
  under the counted gate**; authentication core (#169) pgx 377 / 0 / 5 each (the four storetest
  groups gated on the not-yet-merged stores, plus the collation skip), turso 658 / 0 with exactly
  the eight gated skips; authentication stores (#170, re-run on `main` @ `f00205c5`) pgx bare and
  schema 432 / 0 / 1 each, turso `-tags=integration` 737 / 0 / 0, hermetic turso 382 / 0 / 0 —
  the four gated groups now run with zero skips. 0024 is applied by the non-superuser fixture
  migrator in every live case, and every purge-path case runs as a runtime role with UPDATE,
  DELETE and TRUNCATE revoked on the rail.
- Linux, before PR-2b merged: `make test-stores` end to end in a `golang:1.26.10` container
  against a fresh database, every leg green (authentication pgx 432 × 2; the authorization turso
  leg needs `AUTHORIZATION_TURSO_DISPOSABLE_URL` — a precondition — and passed on re-run).
- `live-stores` dispatches, both jobs (`redis-pgxdb`, `postgres-turso`) green each time, one
  before each store step: run 40034837753 (`main` @ `00cc48c2`, before 5b), 40036209894 (`main` @
  `3388d16a`, before 4b), 40037265148 (`main` @ `78f68807`, before 3b), 40045860760 (the PR-2b
  branch @ `6befc63f`, the pre-merge proof that gated step 1) and 40047381425 (`main` @
  `371d47b4`, before 2b).
- PR-2b driven by hand with a `NOSUPERUSER` owner: `-posture-role` printed `execute=false`, then
  `execute=true` after `GRANT EXECUTE`, then `unsafe=public_execute` and failed after a PUBLIC
  grant; as the runtime role a 367-day cutoff purged, a 364-day one was refused and a direct
  `DELETE` stayed denied.
- The examples (PR-7, untagged), against running binaries on a scratch TLS PostgreSQL 17 with a
  `NOSUPERUSER` owner and a fresh libSQL. `examples/production-baseline`: its three new ledger
  files applied alone; `-posture-role` printed `functions=[security_events_purge_before:execute=false
  …] floor=365 days`, then `execute=true` after the grant; as the runtime role a 367-day cutoff
  purged and wrote its `security_events_purged` row, a 364-day and a NULL cutoff returned 22023,
  and a direct DELETE and every function-tamper form returned 42501; a PUBLIC EXECUTE grant
  refused the production boot, `SECURITY INVOKER` WARNed `definer_function=inoperative` and the
  rail step reported `forbidden`, a missing grant logged exactly one `maintenance_execute=missing`
  WARN. The clean production boot logged its registration line, ran the first pass inside the
  jitter with no WARN and answered `/healthz` 200 with the three maintenance fields; ten
  backdated rail rows became one verified NDJSON object whose ids equal the captured set, one
  matching `security_events_archived` row and a purge row with `count=10`; a read-only archive
  mid-run failed the step `archive_failed` with no purge row and no partial object; `PassBudget`
  20 ms drained 50,000 rows over ten passes with `maintenance_budget_exhausted` WARNs and every id
  in a verified object; the 48 h alert query flipped after 49 h. The erase answered 200 with
  counts, left the person's rail IP/UA unchanged and wrote one `user_erased` row with the admin
  as actor; a repeat answered `changed:false`, reactivate 409, deactivate `changed:false`, self
  400, a non-admin 403; an accepted invitation was blanked with `invited_by` and
  `resolved_subject_id` kept; the sweep reported only the rail's IP carve-out; operation expiry
  kept the fenced row and an expired id read as never recorded. `examples/auth-cms`: a cancelled
  invitation purged at the next pass, 401 on the erased user's session, the inquiry step's
  counts, the delivery purge, an ordered shutdown. `examples/jobs-minimal`: the `retention` kind
  fired. Every example builds at `GOWORK=off`; `guard --posture` on the baseline prints exactly
  one `ack` (`MaintenanceRunnerAcknowledged`) and P9 ok.

**Owner rulings.** Tags are annotated, not signed (ruling of 2026-10-22). **R-7:** no in-place
scrub of `security_events` — the rows stay intact, keyed to the pseudonymous `user_id`, and their
IP, User-Agent and pre-AUDIT-054 raw addresses age out under the 400-day retention (FAS-3's own
recommendation); `user_erased` is written inside the erase transaction. **R-8:** the cms admin
paged inquiry list and delete UI are deferred to a separate cms plan, not yet ratified; S3 ships
the disposal capability only. The roadmap deviations blessed with the plan: the rail purge and
archive live on `maintenance.Repository`/`Service`, the rail port untouched; `Operations.Expire`
became the optional `OperationExpirer` slot with `Mutations.ExpireOperations`; `invited_by` and
`resolved_subject_id` are KEPT, not tombstoned (only `identifier` and `metadata` are blanked);
`erased_at` plus a three-valued projection instead of `status='erased'`; pending invitations are
purged past expiry with no stored `expired`; `ServiceAccounts.Delete` cascades; the in-process
`Run` by default with the jobs `HandlerFunc` recipe; one `Repositories` per service (a per-tenant
registry is its own plan); the floor in a sibling plain function. The lead's build-time rulings
are the executed plan's "Build-time deviations"; each one a host or adapter author can see is in
the surprise list below.

**What shipped, per module.**
- `integrations/datastores/pgxdb` v0.12.0 (additive): `DefinerFunction{Signature}`,
  `AppendOnlyTable.Functions`, `TablePrivilege.Functions map[string]FunctionPrivilege`
  (`Exists`, `SecurityDefiner`, `SearchPathPinned`, `SameNamespace`, `OwnerIsTableOwner`,
  `OwnerSuperuser`, `PublicExecute`, `PathSchemaCreate`, `Execute`), `UnsafeFunctions()`,
  `InoperativeFunctions()`, `ErrDefinerFunctionUnsafe`. The probe resolves each signature with
  `to_regprocedure` in the same phase as the tables, before it pins its own `search_path`.
  `Writable`, `CannotAppend` and `ProtectedBy` are unchanged — the runtime-role posture of S2 does
  not move — and a store that lists no function gets byte-identical output and no extra query.
- `pockets/authentication` v0.23.0 (pre-1.0 MINOR; no exported removal or signature change):
  `logic/maintenance` (`Repository`, `Config`, `Service` with `Pass`/`Run`/`LastReport`,
  `WithInterval`/`WithLogger`/`WithClock`, `Report` and the `Step*` constants, `ArchiveSink`,
  `MinSecurityEventRetention` 366 days, `MaxBatchSize` 10000, `ErrBelowRetentionFloor`,
  `ErrSecurityEventRetentionTooShort`, `ErrArchiveFailed`, `Service.Config()`); the root
  `WithMaintenance(MaintenanceConfig{maintenance.Config; MaintenanceRunnerAcknowledged})`,
  `Components.Maintenance`, the OPTIONAL `Repositories.Maintenance` and `Repositories.UserErasure`
  slots, `ErrMaintenanceAcknowledgedWithoutRepository`, `ErrUserErasureRouteRequires`,
  `ErrUserErasureRequiresActiveSessions`; `user.StatusErased`, `user.ErrErased`,
  `user.EraseRepository`, `EraseInput`, `Erasure`, `EraseCounts` (+ `Merge`),
  `ErrEraseBlockedByClaim`; `serviceaccount.CascadeDeleter` and `Deletion`; the service's
  `EraseUser` (`ErrEraseSelf`, `ErrUserErasureUnavailable`, `EraseTimeout` 30 s, run under
  `context.WithoutCancel`), `UserErasureEnabled`, `DeleteServiceAccount`; `POST
  /auth/admin/users/{id}/erase` behind `AdministrationConfig.UserErasureRoute`
  (`authenticationhttp.WithUserErasureRoute`) with the host check receiving `UserAdminErase`; four
  event types (`user_erased`, `service_account_deleted`, `security_events_purged`,
  `security_events_archived`) and `(*securityevent.Recorder).Prepare`; storetest groups
  `Maintenance`, `UserErasure`, `UserAdmin/SummaryReportsErased` and the cascade-gated
  `ServiceAccounts/DeleteCascadesKeys`. The registration log gains `auth: maintenance
  maintenance_runner= archive_sink= security_event_retention=`.
- `pockets/authentication/stores/pgx` v0.11.0 / `stores/turso` v0.10.0: migrations `0023` and
  `0024` (both REQUIRED), `NewMaintenanceStore` (the bounded purges with `FOR UPDATE SKIP LOCKED`
  subselects on pgx, the bounded `PruneOAuth2`, the whole-instant export, the purge-function call
  with explicit casts), `NewErasureStore` (the erase transaction in the port's order, the
  `user_erased` row inside it), `DeleteWithKeys` and the cascading `Delete`, the owner lock and
  erased refusal on a service-account insert, the `erased` projection on every read; `Repositories`
  fills `Maintenance` and `UserErasure`; the pgx `AuditTables` lists the purge function.
- `pockets/authorization` v0.30.0 (additive): `mutations.OperationExpirer`, the OPTIONAL
  `Repositories.OperationExpiry` slot (refused without `Mutations`), `WithOperationRetention`
  (default 180 days; `ErrOperationRetentionTooShort` below 7 days;
  `ErrOperationRetentionWithoutExpirer` without the slot), `(*mutations.Service).ExpireOperations`
  (`mutations.ErrOperationExpiryNotConfigured`), `operation_expiry=` on the registration line, the
  production WARN; storetest `OperationLedger/{ExpireKeepsFenced, ExpireBoundedAndBoundary,
  ExpireLimitZeroRefused, ExpireThenFenceRecordsFenced}`, `ExpireInsideTransactionRefused` and the
  `storetest.Repositories.SetOperationCommittedAt` fixture hook. `stores/pgx` v0.21.0 /
  `stores/turso` v0.20.0: `0007` and `Expire`; the pgx `Fence` re-runs its INSERT when an expiry
  commits between its conflict and its read-back, so it returns `fenced`, never an error.
- `pockets/jobs` v0.7.0 (additive): `queue.TerminalPurger`, `(*queue.Service).PurgeTerminalJobs`
  for the ordinary queue (`ErrTerminalPurgeRequired`), `terminal_purge=` on the registration line;
  `PurgeTerminal` (fenced) is byte-identical; storetest `Queue/BoundedTerminalPurge`. `stores/pgx`
  v0.7.0 / `stores/turso` v0.6.0: `0005` and the ordinary-queue purge.
- `pockets/cms` v0.5.0 (additive): `messaging.InquiryEraser` (`Delete`, `DeleteByEmail` — ASCII
  case-fold equality — and `PurgeBefore`), the OPTIONAL `Repositories.InquiryMaintenance` slot,
  `inquiry_maintenance=wired|absent` on the registration line; storetest `Inquiries/*` cases; no
  route, view or `Views` change (R-8). `stores/pgx` v0.5.0 / `stores/turso` v0.4.0: `0022` and the
  three methods.
- `workshop/gopernicus` v0.7.0 (MINOR): `MaintenanceRunnerAcknowledged` joins P5's recognized
  surfaces (`guard --posture` prints its `ack`); the scaffold pins pgxdb v0.12.0; the runner
  template's `-posture-role` prints `functions=[…] floor=…` and fails on a missing or unsafe
  definer function; the scaffold README gains "Retention".
- Docs: *Production posture* "Retention (S3)", *Production database posture* "Two roles" /
  "Definer functions" (with the host-owned-rail template) / "Migrations", *Operations runbooks*
  "Retention" (the evidence, the 48 h alert, verifying the archive, restoring the grants) and
  "Erase a person" (the composition and the sweep); `docs/pockets/{authentication, authorization,
  jobs, cms}.md`; `docs/workshop/commands.md`.

**Migration notes — what a repinning host meets.**
- (a) **Migrate, then deploy, the S3 files alone.** Authentication `0023_retention.sql` and
  `0024_security_events_retention.sql` are REQUIRED: authentication `stores/pgx` v0.11.0 /
  `stores/turso` v0.10.0 refuse, in every mode, a database without `users.erased_at` (naming
  `0023`) or without the 0024 functions (pgx) or floor row (turso) (naming `0024`). Authorization
  `0007_iam_operations_expiry.sql`, jobs `0005_job_queue_terminal_purge.sql` and cms
  `0022_inquiries_created_at.sql` are indexes, ADMITTED, not required: without one the pgx store
  WARNs `index_missing=<index>` (authorization when `Repositories` is built; jobs and cms in their
  pre-boot `StatusCheck`, so a host that never calls it sees nothing) and the purge still runs,
  sorting per batch.
  Every S3 pgx file begins `SET LOCAL lock_timeout = '5s'`, every index is `CREATE INDEX IF NOT
  EXISTS`, and `0023` builds its eight indexes FIRST and adds `users.erased_at` LAST, so the
  stream holds `users`' ACCESS EXCLUSIVE for one metadata-only statement. `lock_timeout` bounds
  only the wait to ACQUIRE a lock, never how long a build holds one, and the runner applies every
  pending file in one transaction — so **apply the S3 files in their own runner invocation**, and
  on a large host **pre-build each index** outside the runner with `CREATE INDEX CONCURRENTLY IF
  NOT EXISTS <the same name> …`, then check `SELECT c.relname FROM pg_index i JOIN pg_class c ON
  c.oid = i.indexrelid WHERE NOT i.indisvalid;` — a failed concurrent build leaves an INVALID
  index that `IF NOT EXISTS` keeps: `DROP INDEX` it and build again. The in-stream builds are then
  no-ops. The turso `0023` builds six indexes (`0019` already indexes the OAuth2 codes and refresh
  history); turso has no concurrent build — it runs under the write lock. The baseline's ledger
  copies are `0029_iam_operations_expiry.sql`, `0030_retention.sql` and
  `0031_security_events_retention.sql` (G34 holds them byte-identical).
- (b) **Rollback.** Rolling a binary back is safe: no previous binary calls the 0024 functions,
  and it reads an erased user as `deactivated`, which every credential path refuses. The index
  files are admitted by the previous stores too. Rolling the schema back is forward-fix only —
  there are no down files, and dropping `erased_at` after an erase loses the marker.
- (c) **The two-role recipe gains three lines** (*Production database posture*, "Two roles"):
  `GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz,
  pg_catalog.int4) TO <runtime role>;` (0024 cannot know the role, and it ends with `REVOKE
  EXECUTE … FROM PUBLIC`); `REVOKE CREATE ON SCHEMA <schema> FROM PUBLIC;`; and a `NOSUPERUSER`
  migrator (`CREATE ROLE migrator LOGIN NOSUPERUSER`) — a superuser owner makes the definer
  inoperative, and a docker image's default `postgres` role is a superuser. 0024 refuses to apply
  unless it runs in the schema that holds `security_events` as a member of the table's owner.
- (d) **The definer-function refuse/WARN split.** The probe judges the purge function by catalog
  evidence, never by its body. Production REFUSES with `pgxdb.ErrDefinerFunctionUnsafe` — not
  waivable, `AppendOnlyWritableAcknowledged` included — when PUBLIC can execute it, its
  `search_path` is not exactly `pg_catalog, <the table's schema>, pg_temp`, or it resolves
  outside the rail's schema (a function dropped and re-created by hand gets PUBLIC EXECUTE again).
  It WARNs once (`definer_function=inoperative`, naming the fix) when it is not SECURITY DEFINER,
  its owner is not a member of the table's owner, its owner is a superuser, or the runtime role
  holds CREATE on the schema; the rail step then fails at call time. A runtime role without
  EXECUTE boots with one `maintenance_execute=missing … fix=GRANT EXECUTE …` WARN, logged when the
  store is constructed in production, and the pass reports `step=security_events
  error_kind=forbidden` while every other step runs. A missing function is the store's refusal
  naming 0024 in every mode. `-posture-role` prints `functions=[security_events_purge_before:execute=…
  unsafe=… inoperative=…] floor=365 days` and fails only on what refuses the boot (a missing or
  unsafe function); a missing EXECUTE or an inoperative function is reported, as the boot WARNs.
  After `pg_restore -x`, revoke EXECUTE from PUBLIC and grant it to the runtime role again; after
  a restore into a renamed schema, re-run 0024's pin `DO` block.
- (e) **The floor is an invariant.** `security_events_retention_floor()` returns 365 days (pgx);
  row 1 of `security_events_retention.floor_days` is 365 (turso). The purge refuses a NULL cutoff
  or one younger than the floor with SQLSTATE 22023 (`maintenance.ErrBelowRetentionFloor`) —
  refused, never clamped — on the database's clock; `maintenance.MinSecurityEventRetention` (366
  days) keeps the service a day above it and the default is 400 days. No procedure lowers it. On
  turso it is a footgun guard, not a control: SQLite has no roles.
- (f) **Turso: capture, drop, restore.** One short `BEGIN IMMEDIATE` transaction per purge call
  captures the triggers present on `security_events`, drops exactly those, deletes the bounded
  batch, re-executes the CAPTURED text verbatim, then appends the purge row (after the restore,
  so a host `AFTER INSERT` trigger sees it). A host that declined `0022` gets no trigger back; a
  host-altered trigger is not "repaired"; a fault or a cancelled context rolls everything back,
  triggers included; after COMMIT a changed trigger set logs ERROR
  `error_kind=rail_triggers_changed`. Each batch holds the database's one write lock: logins wait
  behind it (measured locally: ~14 ms per 1000-row batch, ~10 ms per erase).
- (g) **`pgxdb.TablePrivilege` is no longer comparable**: it gained a map field, so host code that
  compares two values with `==` or keys a map by one no longer compiles; compare fields.
- (h) **The archive sink.** Any `filestorage.Storer` is a `maintenance.ArchiveSink`. In production
  use an object store (`integrations/filestorage/gcs`, `integrations/filestorage/s3`): a disk sink
  is per instance and does not fsync (`filestorage.Disk.Upload` — the fsync is a named sdk
  follow-up), so the read-back proves the object was written, not that it survives the node; on
  ephemeral storage it is not an archive until the host backs it. The archive holds IP and
  User-Agent (PII), its retention, backup and access control are host policy, and erasure does
  not reach it. With no sink the rail is purged unarchived and the registration line says
  `archive_sink=none` — a policy, not a WARN.
- (i) **Guards G35 and G36** (thirty-five in `make guard`). G35 `guard-rail-no-dml`: no non-test
  Go file under `pockets/*/stores/**` writes UPDATE, DELETE, `INSERT OR REPLACE`, TRUNCATE or DROP
  TRIGGER against `security_events`, `iam_audit` or `secrets_audit` — literal and concatenated
  spellings, plus rail triggers dropped by name — with a per-line allow-list (the turso purge's
  DELETE and the two canonical trigger constants). G36 `guard-auth-maintenance-leaf`:
  `pockets/authentication/logic/maintenance` never imports the root authentication service
  package (leaf domain packages only).

**Surprises — what a repinning host or adapter author meets.**
- The database says `deactivated` while the API says `erased`: `status:"erased"` breaks a client
  that validates a closed two-value enum, and the SQL for "erased" is `erased_at IS NOT NULL`.
- `user.Status.Valid()` has a third value; an exhaustive `switch` over `Status` needs the `erased`
  case. `SetUserStatus(…, StatusErased)` is refused as a transition (`ErrErased`); reactivating an
  erased user is 409, deactivating one a `changed:false` replay.
- The directory has no status filter: `AdminRepository.List` takes none, so the plan's "`erased`
  as a filter, `deactivated` excluding erased" was not built (follow-up).
- System rows on the rail: one `security_events_purged` row per purge call (a zero count
  included — at least one a day while the runner runs) and one `security_events_archived` row
  per archive object, each `actor_type='system'`, `actor_id='maintenance'`, `user_id` blank.
  Dashboards filter `actor_type='system'`.
- Accepted (and declined, cancelled) invitations are deleted 90 days after their last update,
  pending ones 90 days past expiry; `accepting` is never purged. There is no `INVITATION_TTL`
  option.
- A two-role host that skips `GRANT EXECUTE` sees the rail step `forbidden` every pass (and one
  WARN at boot) while every other step runs.
- The migrator must own `security_events` (be a member of its owning role): 0024 refuses to apply
  otherwise, and without 0024 the store refuses to boot. Run `-posture-role` before the deploy.
- A host store that implements `serviceaccount.CascadeDeleter` runs `ServiceAccounts/DeleteCascadesKeys`;
  the bundled `Delete` now removes the account's API keys too. An authorization adapter that wires
  `OperationExpiry` must fill `storetest.Repositories.SetOperationCommittedAt`, or the suite fails.
- Erase is not a ban. Deleting the provider mirror means the next external login provisions a
  fresh, empty user — with `ProvisionOnFirstVerification`, even the person's still-valid provider
  token answers 200 with a new user id (native sessions answer 401). Deactivate, or remove the
  person at the provider, to keep them out. A held `accepting` invitation claim blocks the erase
  (409) until it is settled. The rail keeps their IP and User-Agent until retention removes them
  (R-7); archives already written stay.
- `ExpireOperations` makes an expired operation id read as never recorded: `Recorded` is false,
  `Fence` fences it, a retried command applies again. `OperationRetention` (180 days) must exceed
  every producer's retry window and the age of the oldest open `accepting` claim; keep
  `InvitationRetention` (90 days) at or below half of it.
- pgx `Expire` takes no row lock: every PostgreSQL row-lock clause needs UPDATE, which the S2
  posture revokes on `iam_operations`. The rule: a runtime-role statement on a rail never carries
  a lock clause; a SECURITY DEFINER function runs as the owner and may.
- A host that enforces retention with its own SQL cron leaves `MaintenanceRunnerAcknowledged`
  false and accepts the production WARN; setting it true beside a nil `Repositories.Maintenance`
  is `ErrMaintenanceAcknowledgedWithoutRepository` in every mode. `UserErasureRoute` is an opt-in,
  not an acknowledgement — `guard --posture` prints nothing for it — and needs
  `Repositories.UserErasure` and a `UserAdminCheck` (`ErrUserErasureRouteRequires`);
  `Repositories.UserErasure` needs `Repositories.ActiveSessions`.
- `Components.Maintenance.Run` runs hourly with no interval option on the root; a host that wants
  another cadence calls `Pass` from its own ticker or a jobs schedule (auth-cms does, on
  `MAINTENANCE_INTERVAL`).
- The jobs capability WARN (`terminal_purge=absent`) is logged in every mode through
  `slog.Default`: the queue service takes no runtime mode or logger. Every bundled store
  implements the capability. The S3 store WARN and ERROR lines also go to `slog.Default`, not the
  pocket's logger; the turso stores have no index WARN.
- Archive lines are `json.Marshal` output: HTML characters in a value come out escaped
  (`<`), still valid JSON.
- `examples/auth-cms`: its inquiry-purge and operation-expiry loops run on the retention runner
  now, so their first pass runs at boot instead of one interval later and their failure lines
  carry `error_kind`, never error text; its platform-admin `UserAdminCheck` (added for the erase
  route) also mounts the admin directory routes. The baseline's `/healthz` gains
  `maintenance_last_pass`, `maintenance_steps_failed` and `maintenance_last_error_kind` (`none`,
  `forbidden`, `below_floor`, `archive_failed`, `budget_exhausted`, `other`) and its status code
  never changes for a maintenance failure; `AUDIT_ARCHIVE_DIR` is required in production.

**Known.**
- `outbox.PurgePublished` is not a runner step in any example (the plan's DoD had auth-cms run it);
  it stays unbounded, and a bounded variant is a named follow-up. Run it as its own step after a
  one-off manual drain.
- The scaffold README still runs `workshop/gopernicus@v0.6.0 guard --posture .` (v0.7.0 was not
  tagged when PR-6 merged), and v0.6.0 does not print the `MaintenanceRunnerAcknowledged` `ack`; a
  template pin is a PATCH (the v0.6.1 precedent).
- Residual: an API key minted while its account's cascade commits first is still inserted. It is
  inert — API-key authentication resolves the account and finds none.
- G35 residual: a dynamic `DROP TRIGGER ` + name concatenation is outside both patterns; the turso
  purge is the only such line in store code.
- The host-owned-rail definer template (`iam_audit`, `secrets_audit`) appends no evidence row —
  their CHECK constraints forbid one — and returns the count for the host's cron to record.
- `make test-stores` needs `AUTHORIZATION_TURSO_DISPOSABLE_URL` for the authorization turso leg (a
  precondition; a Makefile/README note is a follow-up). The S2 "Known" items on shared turso
  ledgers and database reuse still hold: every full run on a fresh server and a fresh database.
- `examples/jobs-minimal`'s 7-day boundary is not observable live on its memory store (storetest
  covers it); a local `PassBudget` of 1 s does not exhaust (one pass drains 50,000 rows).
- Named follow-ups (S3 Notes): the `Disk.Upload` fsync (sdk PATCH); a bounded
  `outbox.PurgePublished`; an `sdk/capabilities/retention` protocol; a pgxdb limiter `Prune`; the
  cms admin form-CSRF seam; the cms admin paging/delete plan; a status filter on the user
  directory.
- Known CI flake unrelated to S3: `integrations/kvstores/goredis`
  `TestOpenHonorsDeadlineAfterConnection`.

## S2 follow-ups (secrets stores/pgx v0.2.1; authentication v0.22.0, stores/pgx v0.10.1; workshop/gopernicus v0.6.1 — published 2026-10-23)

Four annotated tags (not signed), each cut at its PR's squash-merge commit on `main`, pushed,
THEN polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an empty
module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build <module>/...`).
Every one resolved on the first poll. They close the **Known** list of
[Audit completeness and integrity](#audit-completeness-and-integrity-soc-2-slice-s2--published-2026-10-23)
below, except `external_login`'s per-cache dedupe, a design property that stays Known. Design
and rulings: [plans/s2-follow-ups.md](plans/s2-follow-ups.md) (rev 2, ratified 2026-10-23).
Host handoff: [plans/soc2-s2-audit-completeness-handoff.md](plans/soc2-s2-audit-completeness-handoff.md),
updated under the maintenance rule (below). Manifest: `plans/s2-follow-ups-release-manifest.json`
(from `go mod download -json` of the four tags).

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 1 | `pockets/secrets/stores/pgx/v0.2.1` | `f7b3ec9f` | #156 (PR-1; PATCH; core `pockets/secrets` v0.2.0 + pgxdb v0.11.0 unchanged) | OK, poll 1 |
| 2b | `pockets/authentication/stores/pgx/v0.10.1` | `7e26db9e` | #158 (PR-2b; PATCH; its tree pins core v0.21.0 + pgxdb v0.11.0 — cut BEFORE PR-2a landed the live case that needs v0.22.0) | OK, poll 1 |
| 2 | `pockets/authentication/v0.22.0` | `c9cc0a9b` | #155 (PR-2d), #158 (PR-2b), #159 (PR-2a), #160 (PR-2c) — one core tag, MINOR, cut at the later merge | OK, poll 1 |
| 3 | `workshop/gopernicus/v0.6.1` | `9b51e10f` | #157 (PR-3; PATCH; tagged as the next step after the merge) | OK, poll 1; a cold `init --db pgx` from `@v0.6.1` emits a README running `@v0.6.0 guard --posture .`, builds, and is guard-clean (P9 `no scope`) |

**Not tagged.** `pockets/authentication/stores/turso` stays v0.9.0: its code does not change (it
stores nine fractional digits). Both authentication stores keep core v0.21.0 in their `go.mod`
(pgx v0.10.1, turso v0.9.0), so a host that pins core v0.22.0 beside them builds v0.22.0 by
MVS. `pockets/secrets` v0.2.0, `sdk` v0.16.0, pgxdb v0.11.0 and turso v0.8.0 do not move. No
migration, no store schema change, no port signature change; nothing retracts.

**Verification record.**
- CI `check` and `vuln` (push and pull_request events) and the CodeQL analyses passed on the
  final run of every PR, #155–#160.
- Live store legs on merged `main` before each pocket and store tag, against a scratch
  PostgreSQL 17 and a local libSQL server: the secrets pgx store bare and schema-qualified, the
  two race tests at `-count=20` (tag 1); the authentication pgx store bare and schema-qualified
  (tags 2b and 2); for tag 2 also the authentication turso store under `-tags=integration` and
  `TestConformance_LocalSQLite`, the five terminal-settlement subtests printing `--- PASS` in
  every run (`TestReference`, `LocalSQLite`, local libSQL, authmem, pgx bare and schema), and
  the pgx Recorder live case `TestLive_RecorderBoundsClientAttribution` (bare and schema). None
  skipped except the pre-existing `TestCollationControlsOrdering_NonC` (it needs a non-C DSN).
- Linux, before PR-2b merged: `make test-stores` end to end in a `golang:1.26.10` container
  against a fresh database exited 0 on every leg, the first full Linux run of the legs after
  authentication pgx since 2026-07-07; the `live-stores` dispatch on the PR-2b branch (run
  40002504439) passed both jobs, the authentication pgx bare and schema legs green on
  `ubuntu-latest` with no `GOCACHE` override in the workflow. That branch was cut from
  `019e8a36`, before #156, so the secrets race tests have run on macOS only; tag 1's Linux
  evidence is the next `live-stores` run on `main`.
- Tag 3: before the merge, a scaffold from the published v0.6.0 was checked posture-clean under
  `@v0.6.0 guard --posture .` (exit 0); after the tag, the cold `init --db pgx` above.
- `examples/production-baseline` was driven live in PR-2c (scratch PostgreSQL 17): a dev-token
  403 on the role routes wrote `external_subject_provisioned`, `external_login` and
  `authorization_denied` rows, each with `ip_address`, `user_agent` and the response's request
  id; a request whose User-Agent is not valid UTF-8 got the same three rows with `user_agent`
  blank; `/healthz` reported `audit_write_failures=0`, `audit_rows_suppressed=0`,
  `bridge_dropped=0`. `examples/auth-cms` drove the OAuth2 mismatch in PR-2a (below). Both
  examples (untagged) are repinned by this record's PR: auth-cms to authentication v0.22.0 (and
  `pockets/cms` v0.3.0 → v0.4.0, a pin stale since S1 that only the workspace had masked — the
  example now builds at `GOWORK=off` again);
  production-baseline to authentication v0.22.0 AND `stores/pgx` v0.10.1, an explicit `go get`
  (the baseline pins the store directly and `go mod tidy` does not raise a direct pin).

**What shipped, per module.**
- `pockets/secrets/stores/pgx` v0.2.1: `DeleteOwner` locks the owner key first — `SELECT key_id
  … FOR UPDATE`, scanned through `pgxdb.MapError` (no key ⇒ `sdk.ErrNotFound` before any
  delete) — then deletes the values and the key. A `PutSecret` that commits before the lock
  makes `DeleteOwner` return `sdk.ErrConflict` with nothing deleted, which the service's retries
  absorb (it returned `sdk.ErrInvalidReference`, which the service does not retry); one that
  arrives after the lock waits, then fails with `sdk.ErrInvalidReference` because the owner is
  gone. `DeleteOwner` and `ReplaceOwnerKey` now lock in the same order (owner key, then values),
  so the deadlock (SQLSTATE 40P01, unmapped) between owner deletion and a resealing rotation is
  gone. Proof: `TestLive_DeleteOwnerVersusPutSecret` (with a service-level assertion that
  `vault.Service.DeleteOwner` succeeds through its retries) and
  `TestLive_DeleteOwnerVersusRotation`, driven by a `pg_blocking_pids` barrier, no sleeps; both
  fail on the pre-fix store and pass 20 of 20 in the bare and schema-qualified legs. The outcome
  assumes the server default READ COMMITTED (`pgxdb.InTx` uses `pool.Begin`). The lock is held
  for the transaction and there is no `lock_timeout`: a request-path caller carries a deadline.
  `FOR UPDATE` needs UPDATE on `secrets_owner_keys`, which the runtime role already holds for
  `ReplaceOwnerKey`. The memory store holds one mutex for the whole call and is unchanged.
- `pockets/authentication/stores/pgx` v0.10.1: every session insert returns the session as
  persisted. One helper, `persisted`, truncates `CreatedAt`, `ExpiresAt` and
  `Authentication.AuthenticatedAt` to the microsecond in UTC; the insert encodes from it and all
  four insert paths return it — `Create`, `CreateForActiveUser` (the production mint path),
  `RedeemCode` and the passwordless redemption. Truncating in Go is load-bearing: pgx's binary
  encoding truncates but the text protocol rounds half-to-even, so only Go-side truncation
  stores the same value under every `default_query_exec_mode`. No SQL or migration change.
- `pockets/authentication` v0.22.0 (pre-1.0 MINOR; additive exports only): six reasons
  (`ReasonPrecondition`, `ReasonClaimConflict`, `ReasonUnknownProvider`, `ReasonInvalidRequest`,
  `ReasonInvalidScope`, `ReasonInvalidTarget`); `securityevent.BoundValue` with `Field` and its
  five constants, `MaxUserAgentLen`, `MaxDetailLen`; `securityevent.WithFailureRowBudget`,
  `WithMissingAttributionWarning`, `Input.Budgeted`, `(*Recorder).SuppressedRows()`;
  `Components.AuditRowsSuppressed()`; `(*oauth2.Service).AuthenticateClientContext`;
  `(*authenticationhttp.Adapter).ClientInfoMiddleware()`; the session port's "returns the
  session as persisted" sentence; and the storetest changes below. ARCHITECTURE.md gains the
  "pocket-owned context stamp" middleware row.
- `workshop/gopernicus` v0.6.1 (PATCH): the scaffold README and `docs/workshop/commands.md` run
  `@v0.6.0 guard --posture .` (P9 included); the install line in `workshop/gopernicus/README.md`
  names `@v0.6.1`. No code; the emitted `go.mod` pins do not change. This supersedes S2's
  Known "the init pin moves in the next workshop minor (the S1 precedent)": a template pin is a
  PATCH by the v0.4.1 "Scaffold pins" precedent.
- CI: `live-stores.yml` loses the workflow-level `env: GOCACHE: /tmp/gopernicus-audit-s1…`
  override, a local temp path that #124 leaked; setup-go's cache owns the build cache and
  `live-stores` shares `check.yml`'s cache key. Its header now says there are no Turso secrets in
  CI: the turso legs skip there and are proven locally against libSQL. The `RotationKeepsExpiresAt`
  failure the dispatch reported on every run since 2026-07-07 (called a "precision flake" on
  2026-08-15) was deterministic on Linux: the store returned the caller's nanosecond values
  while `timestamptz` keeps microseconds, and macOS's microsecond wall clock hid it.

**Behavior notes — what a repinning host or adapter author meets.**
- (a) **storetest at v0.22.0 tightens two adapter contracts (BREAKING for third-party
  adapters, learned at `storetest.Run` time, not at compile time).** Terminal settlement is
  mandatory: an adapter answering `errors.ErrUnsupported` to `CancelAcceptance` or
  `ListActiveByResource` now fails the suite (the R6 probes that exempted the retired Firestore
  store are gone). And `Create` / `CreateForActiveUser` return the session as persisted: the
  fixture clock in `Sessions/RotationKeepsExpiresAt` and `ActiveSessionMint/ActiveUserRoundTrip`
  carries a fixed 750 ns remainder (500 would be the rounding tie), so an adapter that stores
  coarser than nanoseconds but returns its input fails on every platform. Every in-repo
  adapter passes. A `stores/pgx` consumer at `GOWORK=off` on core v0.21.0 runs v0.21.0's suite,
  which the fixed store also passes.
- (b) **"Returns the session as persisted"** covers timestamps only: an insert's return now
  equals what `Get` reads back. A host-written caller that compared an insert's return with its
  own nanosecond input sees the stored microsecond value (nothing in this repository does; the
  service uses only the returned ID). `session.NewSession` keeps nanoseconds; precision is the
  store's fact, not the entity's.
- (c) **Nine more denial paths record**, through the one recorder, as failure rows unless noted:
  `SetPassword` with a password already set (`password_set`, `precondition`); a revision race in
  `SetPassword` / `ChangePassword` (`revision_conflict`); `RemovePassword` with no password
  (`password_removed`, `precondition`) or a deactivated account (`password_removed`, blocked,
  `inactive`); a refused recent-authentication grant on an identifier change, removal or uses
  change (the mutation's own type, `invalid_token`); the address-claim race on confirm
  (`email_changed` / `phone_changed`, `claim_conflict`); an unknown provider at the OAuth
  callback (`oauth_login`, `unknown_provider`, no `provider` key, budgeted) and at a session-bound
  link start (`oauth_linked`, `unknown_provider`, with the user) — never at the unauthenticated
  login start, which resolves no provider and records nothing (that route has no rate limit);
  the token endpoint's `invalid_request` / `invalid_scope` / `invalid_target` refusals
  (`oauth2_grant_failed`, budgeted); and the confidential-client secret mismatch
  (`oauth2_grant_failed`, `invalid_client`, budgeted). A dashboard that assumed malformed token
  requests leave no row sees rows now. `client_id` rides an `oauth2_grant_failed` row only when it
  names the configured confidential client; otherwise the key is absent. **The boundary is a
  rule:** *"A row is written when the server evaluated a credential, grant, token or parameter
  value and refused it. A request refused for its shape (auth-header form, credentials in the
  body, encoding, a parameter the grant does not accept) writes none; it appears only in access
  logs and rate-limit counters."*
- (d) **`AuthenticateClient` is deprecated, not removed.** It delegates to
  `AuthenticateClientContext(ctx, clientID, secret)` (one constant-time comparison site) with
  `context.Background()`, so a mismatch through it records an `oauth2_grant_failed` row with no
  request id, IP or User-Agent, counted in one shared failure-row bucket. The bundled token
  handler calls the context sibling. **Removal point:** the core MINOR after
  `pockets/authentication/stores/turso` repins past v0.21.0 (its tests call the old method).
- (e) **One attribution rule at the one recording site — and the pgx row loss it closes.**
  `Record` passes every attributed value through `securityevent.BoundValue`: a value carrying
  NUL, invalid UTF-8 or a control character is recorded blank, never truncated and never
  refused; the IP is blank unless `netip.ParseAddr` accepts it; the User-Agent is cut at 512
  bytes on a rune boundary; a top-level Details string over 2048 bytes is blank (before: an
  OAuth2 `client_id` was truncated at 2048). The request and trace ids keep S2's rule byte for
  byte — printable ASCII, at most 128 bytes, else blank — so no correlation id behaves
  differently. **This fixes a pre-existing pgx row loss:** before v0.22.0 a client id carrying an
  encoded NUL (JSONB refuses `\u0000`, SQLSTATE 22P05) or a User-Agent that is not valid UTF-8
  (`TEXT` refuses it, SQLSTATE 22021) lost the whole row on the pocket's own routes, counted only
  as an `audit_write_failed`; now the row is written with that field blank
  (`TestLive_RecorderBoundsClientAttribution`: the old recorder stored 0 of 3 rows, the new one
  3 of 3 with `WriteFailures() == 0`). Turso and the memory store kept such values; no producer
  can send one now. auth-cms run-and-look (`AUTH_OAUTH2_DEMO=1`): a wrong secret for the demo
  client, for a foreign id and for the demo id with an encoded NUL each answered 401
  `invalid_client` and wrote a row — `client_id` present, absent, absent — with IP and UA set
  and no secret anywhere.
- (f) **A per-IP failure-row budget: 60 rows a minute per client IP, per instance.** The root
  wires `securityevent.WithFailureRowBudget(limiter, ratelimiter.PerMinute(60))` with the same
  limiter it hands `OAuth2Config.Limiter`. Every `oauth2_grant_failed` row and the callback's
  `unknown_provider` row are budgeted (`Input.Budgeted`); S2's `invalid_state` callback row is
  not. Over budget the row is not written, `Components.AuditRowsSuppressed()` counts it, and one
  `audit_rows_suppressed` WARN logs, then at most one `count=N` line a minute; a limiter error
  writes the row (a limiter fault costs a row, never loses one). A blank IP shares one key.
  auth-cms run-and-look: seventy mismatches from one address in a minute logged
  `audit_rows_suppressed count=1`, then `count=13` a minute later, and no `audit_write_failed`.
- (g) **`ClientInfoMiddleware` — client attribution on host routes.** Before v0.22.0 only the
  authentication pocket's own routes carried the client-info context, so a host route's rows —
  bridged `authorization_denied` rows, `external_login` rows first recorded on a host route —
  had blank `ip_address` / `user_agent`. The recipe, in `cmd/`: `router.Use(authenticationComponents.HTTP.ClientInfoMiddleware())`
  right after `authentication.New`, registered after (so running inside) the `TrustProxies`
  `Use`. It is a stamp, not a gate; it reads `web.ClientIP` (set by `TrustProxies`; the peer
  address otherwise), never `X-Forwarded-For`; the pocket exports it and only the host installs
  it (`pockets.Mount` never grows a `Use`). The pocket's own routes keep their per-route stamp.
  The proxy posture now lives on the `Adapter`: `NewAuthenticator` builds it from its own
  runtime mode, so a `NewAuthenticator`-built adapter in production also warns once that
  `TrustProxies` is not wired. In production the recorder logs one WARN per process, *"security
  event recorded without client attribution; mount ClientInfoMiddleware"*, the first time it
  writes a row whose context carries a request id but no client IP (background rows never
  warn). Both examples mount it, and their bridges' 60 s dedupe key gains the client address: a
  second address within the window records its own row. The baseline's audit-write observer
  logs the first failure at ERROR, then one counted line a minute, and `/healthz` reports
  `audit_rows_suppressed`.
- (h) **Every surface whose per-IP bucketing changes.** Today's behavior on a host route is the
  defect: `RateLimitByIP` there put every client in one `prefix:` bucket. Now:
  `RateLimitByIP` and the OAuth2 per-IP budget key on the stamped IP, and on an unstamped route
  fall back to the `TrustProxies`-resolved or peer address — so `RateLimitByIP` on a host route
  rebuckets per client with or without the middleware. The logic-side per-IP budgets
  (`allowAuthenticationRequest`, passwordless start and redeem, verification resend) have no
  request to fall back on: on the pocket's own routes they are always stamped, and a host that
  calls those services from a host route rebuckets them only once the middleware is mounted —
  the production WARN in (g) is their signal. (Identifier-management budgets are per user and per
  target, not per IP.) The rebucketing therefore happens only when the middleware is mounted or
  the fallback applies. Shared → per-client can only admit more: no host newly answers 429,
  but the implicit global cap is gone. **Caveat:** attribution is only as good as
  `TRUSTED_PROXY_COUNT` plus an origin reachable solely through those proxies; the
  `X-Real-IP` fallback and the leftmost clamp are now a spoof surface on every host route that
  carries the stamp.
- (i) **Host routes now add IP and User-Agent — personal data — to `security_events`, an
  immutable table; erasure is S3's.**

**Owner rulings.** Tags are annotated, not signed (ruling of 2026-10-22). **R-A = (a):**
`.github/scripts/authorization_cache_verify.py` and its test stay — nothing in CI or the
`Makefile` runs them, but they reproduce the 109-case matrix `pockets/authorization/BENCHMARKS.md`
still cites — and BENCHMARKS' recipe now calls the script a historical, owned runner. **R-B =
(a): no adoption exception.** The maintenance rule (owner, 2026-10-23): host handoffs are
written and updated per slice but are NOT applied to the hosts until the SOC 2 roadmap is
complete; the hosts adopt once, at the end. The risk, named: the pgx row-loss fix (v0.22.0) and
the secrets race fix (v0.2.1) do not reach gps-360-go, which mounts OAuth2, until the roadmap
ends, and the eventual adoption stacks the S1, S2 and follow-up runtime refusals and migrations
into one deploy. The flood posture in (f) is acceptable in part BECAUSE hosts adopt after S3
retention.

**Known.**
- **The flood bound, with the budget:** ≤ 60 rows a minute per IP per instance (~86 k rows a
  day per IP per instance), each ≤ ~3 KB (UA ≤ 512 B, ids ≤ 2048 B), multiplied by the instance
  count because the memory limiter is per instance; each row is a synchronous insert bounded at
  2 s on the host's shared pool, append-only, with no purge until S3. Operators alert on
  `oauth2_grant_failed reason=invalid_client`.
- The budget bounds rows, not requests: the OAuth callback route has no rate limit (follow-up).
- Placement debt: grant validation is split across the handler (`oauth2PublicResource`,
  `actor_token_type`, an empty `token`) and the service (`Resource`) — a later re-split, not an
  audit gap alone. Under the rule in (c) the handler's shape rejects stay unrecorded.
- The logic-side per-IP budgets stay carrier-dependent (`abuse.go`, `passwordless.go`,
  `resend.go`): they rebucket on a host route only behind the middleware (h).
- Secrets `DeleteOwner` under a `default_transaction_isolation = 'repeatable read'` server
  default: the race returns SQLSTATE 40001, which `MapError` passes through and the service does
  not retry. READ COMMITTED (the server default) is assumed.
- The `owner_key_destroyed` row can name a stale key id: the secrets service reads the key id
  outside the deleting transaction, so a rotation committing in between leaves the record naming
  the rotated-away key. The fix is a secrets core change for a later minor.
- The `pockets/authentication/stores/pgx` tree on `main` carries `TestLive_RecorderBoundsClientAttribution`,
  which needs core ≥ v0.22.0; it is not in the v0.10.1 tag (that tree pins v0.21.0), and the next
  `stores/pgx` tag repins past v0.22.0.
- `external_login` dedupe is per cache (carried from S2): under the memory default each instance
  records once per token, and once more per eviction.
- Turso legs share one ledger: `pockets/authentication/stores/turso/schema_probe_test.go:48`
  drops the shared `schema_migrations` table, so a later authorization turso leg against the
  SAME libSQL server fails with `table iam_tuples already exists`. Run each full pass on a fresh
  server (CI skips turso; `make test-stores` passes on a fresh server). Follow-up: scope the probe
  test's reset to its own tables.
- Database reuse: `TestInvitationAcceptanceUpgrade` and `TestOAuth2SessionUpgradePreservesBrowserSession`
  (authentication pgx) fail against a database another run already migrated — the table probe
  reports the missing `oauth_clients` before the column probe names 0018/0019 — and pass on a
  fresh database. Use a fresh database per run. Follow-up: have the two tests create their own
  schema.

## Retired: Firestore stores (no tags — owner ruling 2026-10-23)

`integrations/datastores/firestore` and `pockets/authentication/stores/firestore` left the
workspace in #153; the authorization Firestore store was retired earlier (AUDIT-040). Guard G24
(`guard-firestore-mediation`) is retired with them and its number is not reused. Retired modules
are frozen at their last published version; a deletion gets no tag and no retraction. These tags
stay on the proxy with their original contracts:

```
integrations/datastores/firestore/v0.1.0-beta.1
integrations/datastores/firestore/v0.1.0            @ 10e5f97b  2026-09-13   (last)
pockets/authentication/stores/firestore/v0.1.0-beta.1
pockets/authentication/stores/firestore/v0.1.0
pockets/authentication/stores/firestore/v0.1.1
pockets/authentication/stores/firestore/v0.2.0     @ 6a399d1e  2026-09-18   (last)
pockets/authorization/stores/firestore/v0.1.0-beta.1 … v0.3.0              (already retired, AUDIT-040)
```

S2's side of the ruling is item (l) of
[Audit completeness and integrity](#audit-completeness-and-integrity-soc-2-slice-s2--published-2026-10-23)
below. The pre-retirement tree is reachable at the repository ref
`archive/firestore-stores-2026-10-23` (#153's parent, `f73e70a1`; not a module tag). Consumer
guidance:
[AUDIT-055](AUDIT.md#audit-055-retire-the-firestore-connector-and-the-authentication-firestore-adapter).

## Audit completeness and integrity (SOC 2 slice S2 — published 2026-10-23)

Fourteen annotated tags (not signed), each cut at its PR's squash-merge commit on `main`,
pushed, THEN polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an
empty module cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build
<module>/...`). Every one resolved on the first poll. Design and rulings:
[plans/s2-audit-completeness.md](plans/s2-audit-completeness.md) (rev 3, ratified
2026-10-22). Host handoff:
[plans/soc2-s2-audit-completeness-handoff.md](plans/soc2-s2-audit-completeness-handoff.md).
Manifest: `plans/s2-audit-completeness-release-manifest.json` (from `go mod download -json`
of the published tags; `pockets/authorization` appears twice, v0.29.0 and v0.29.1). The
Details flip is recorded as
[AUDIT-054](AUDIT.md#audit-054-audit-details-carry-identifier-digests-not-addresses).

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 0 | `pockets/authorization/stores/pgx/v0.19.1` | `84b0154d` | #139 (expand: ships 0005, tolerates it, does not require it) | OK, poll 1 |
| 0 | `pockets/authorization/stores/turso/v0.18.1` | `84b0154d` | #139 | OK, poll 1 |
| 1 | `integrations/datastores/pgxdb/v0.11.0` | `f0e1043f` | #140 | OK, poll 1; also built the MVS pair authorization `stores/pgx` v0.19.1 on pgxdb v0.11.0 |
| 1 | `integrations/datastores/turso/v0.8.0` | `f0e1043f` | #140 | OK, poll 1 |
| 2a | `pockets/authentication/v0.21.0` | `9590e366` | #144, #147, #149 (one core tag for PR-2a/2b/2c) | OK, poll 1 |
| 2b | `pockets/authentication/stores/pgx/v0.10.0` | `ada0aeda` | #150 (repin to core v0.21.0 + pgxdb v0.11.0) | OK, poll 1 |
| 2b | `pockets/authentication/stores/turso/v0.9.0` | `ada0aeda` | #150 (repin to core v0.21.0 + turso v0.8.0) | OK, poll 1 |
| 3a | `pockets/authorization/v0.29.0` | `7344393c` | #142 | OK, poll 1 |
| 3a' | `pockets/authorization/v0.29.1` | `159be98a` | #151 (gate-denial target patch; no store retag) | OK, poll 1 |
| 3b | `pockets/authorization/stores/pgx/v0.20.0` | `1f22ad38` | #146 (repin to core v0.29.0 + pgxdb v0.11.0) | OK, poll 1 |
| 3b | `pockets/authorization/stores/turso/v0.19.0` | `1f22ad38` | #146 (repin to core v0.29.0 + turso v0.8.0) | OK, poll 1 |
| 4a | `pockets/secrets/v0.2.0` | `cd80f491` | #141 | OK, poll 1 |
| 4b | `pockets/secrets/stores/pgx/v0.2.0` | `a8a12eb4` | #145 (repin to core v0.2.0 + pgxdb v0.11.0) | OK, poll 1 |
| 5 | `workshop/gopernicus/v0.6.0` | `7619e090` | #143 | OK, poll 1; cold-installed, `guard --list --posture` row 25 = P9 |

**Step 0 is the expand step.** `stores/pgx` v0.19.1 and `stores/turso` v0.18.1 are patches by
owner ruling: they ship `0005_iam_audit_integrity.sql` in both dialects with probes that admit a
database with or without it, require nothing new (core v0.28.0 and the connector pins of
v0.19.0 / v0.18.0), and were tagged on `main` only after 0005's text was pinned by a test (the
"Expand-step patches" rule under [Tagging scheme](#tagging-scheme)). v0.20.0 / v0.19.0 make 0005
required. Not tagged: `pockets/authentication/stores/firestore` (retired, below),
`pockets/authorization/stores/goredis` and `sdk` (stays v0.16.0).

**Verification record.**
- CI `check` and `vuln` (push and pull_request events) and the CodeQL analyses passed on the
  final run of every PR, #139–#151.
- Live store legs on merged `main` before every pocket and store tag: per-module runs
  equivalent to `make test-stores` (every pgx store bare and schema-qualified, now including the
  secrets leg and the fixture-role cases) against a scratch Postgres 17 on `127.0.0.1:55433`;
  the turso connector and turso stores under `-tags=integration` against local libSQL servers on
  ports 8081–8084; the Firestore emulator on 8080. None skipped except the pre-existing
  `TestCollationControlsOrdering_NonC` (it needs a non-C DSN). One load flake,
  `TestFenceContentionTwoConnections` (authorization turso store), re-passed 3/3 run alone
  before Step 0. The emulator leg's pre-existing failure `TestConstructorSkipsTheProbeWhenAskedTo`
  (it fails on `main` before S2) is moot with the firestore retirement.
- Step 1's cold verification also built the pair a host sits on between its authentication and
  authorization repins: `pockets/authorization/stores/pgx` v0.19.1 over pgxdb v0.11.0, including
  the 42501 mapping change.
- `examples/production-baseline` and `examples/auth-cms` (PR-6, #148, untagged) were driven live
  (run-and-look notes under Known) and are repinned to the published tags, with
  `pockets/authorization` at v0.29.1, by the release-record PR.

**Owner rulings during execution.** Tags are annotated, not signed (ruling of 2026-10-22). The
four roadmap deviations were ratified with the plan: (a) the immutability triggers ship
DEFAULT-ON as numbered migrations, not opt-in; (b) `secret_read_denied` is the OUTCOME value
`denied` of a `secret_read` row, not a new action; (c) `audit_read` moves to S7 with the
audit-read routes, so S2 records `user_directory_read` only; (d) the `iam_operations` trigger
refuses DELETE of fenced rows only. Found during the build: P9 treats a store's
`*_immutable.sql` as declinable — absent from the host ledger it is a `declined:` note on the
status line, not a finding; an edited copy is a finding (G34 on the baseline stays strict). The
baseline ledger copies moved from PR-6 to PR-5 (#143) and are numbered
`0025_iam_audit_integrity.sql`, `0026_iam_audit_immutable.sql`,
`0027_security_events_integrity.sql`, `0028_security_events_immutable.sql`. AUDIT-054's lookup
recipe was corrected in PR-2c, before tag 2a (below). The gate-denial finding from PR-6 was patched as
authorization v0.29.1 ("patch it"). The firestore store adapters are retired (below). The
secrets `DeleteOwner` race is a follow-up (Known).

**What shipped, per module.**
- `integrations/datastores/pgxdb` v0.11.0: `AppendOnlyTable`, `TriggerBody`, `TablePrivilege`,
  `Report`, `InspectTablePrivileges`, `(*DB).ProbeAppendOnly`, `(*DB).RuntimeMode()`,
  `Config.AppendOnlyWritableAcknowledged` (programmatic, no env tag), `ErrAppendOnlyTableWritable`,
  `ErrAppendOnlyTableUnwritable`.
- `integrations/datastores/turso` v0.8.0: `(*DB).RuntimeMode()`, `AppendOnlyTable`,
  `TableTrigger`, `TriggerBody`, `(*DB).ProbeAuditImmutable` (WARN-only).
- `pockets/authentication` v0.21.0 (no exported signature change): one security-event recorder
  (`securityevent.Recorder`, exposed as `Components.SecurityEvents` and handed to every
  consumer), `SecurityEvent.RequestID`/`TraceID`, `Components.AuditWriteFailures()`,
  `WithAuditWriteObserver`, the `audit_write_failed` WARN (once, then one summary a minute),
  `session_revoked`/`all_sessions_revoked` promoted to constants with a registration test,
  failure and blocked rows on the denial paths of `IssueToken`, `ChangePassword`,
  `SetPassword`, `ResetPassword`, `Verify`, `RemovePassword`, identifier changes, OAuth
  callbacks and link verification, the OAuth2 server (`oauth2_consent_denied`,
  `oauth2_grant_failed`, all through the one recorder) and invitation acceptance;
  `oauth_pending_link_sent`, `external_login` (once per token per cache) and
  `user_directory_read`; AUDIT-054. Recording fails OPEN.
- `pockets/authorization` v0.29.0: the decision observer (`authorization.WithDecisionObserver`,
  `decisions.WithDecisionObserver`, `authorizationhttp.WithDecisionObserver`),
  `decisions.EventAuthorizationDenied` / `EventRoleWriteRefused`, `audit.Source.RequestID` /
  `TraceID` (the bundled role routes blank an id longer than 128 bytes or non-printable rather
  than refuse the write; `Validate` bounds an id a host stamps by hand), a WARN and an
  `OutcomeRefused` event on every refused role write, a production WARN when no observer is
  wired. v0.29.1: `Evaluate` and `EvaluateResolved` — what every `Require` gate calls — name
  their target in the decision event and log line (a single leaf: its permission, relation or
  role and its resource; a composite: the first leaf that denied, else the first reached). No
  exported signature, store or `go.mod` change.
- `pockets/secrets` v0.2.0 (BREAKING port, pre-1.0 minor): `vault.Repository` gains
  `AppendAudit`; `DeleteOwner(ctx, owner, expected, audit)` deletes in its transaction and
  records the rows it removed; one `secret_read` row per read after the open, with
  `Outcome` `ok`/`denied` and the version read; `AuditRecord.RequestID`/`TraceID` through
  `vault.Correlate`; `AuditFilter.Outcome`. `stores/pgx` v0.2.0 adds `Open(ctx, db, opts...)`,
  the probing constructor (`NewRepository` stays; `StatusCheck` is boot-only).
- The stores: authentication `stores/pgx` v0.10.0 / `stores/turso` v0.9.0 (migrations 0021 +
  0022), authorization `stores/pgx` v0.20.0 / `stores/turso` v0.19.0 (0005 required + 0006),
  secrets `stores/pgx` v0.2.0 (0002 + 0003); each pgx store runs the posture probe at
  construction and exports `AuditTables(...)`; each turso store runs the trigger probe.
- `workshop/gopernicus` v0.6.0: posture rule P9 (every migration a mounted framework store ships
  has a byte-identical copy in the host's ledger), `AppendOnlyWritableAcknowledged` on the
  recognized posture surfaces (P5), the runner template's `-posture-role <role>` preflight,
  scaffold pins pgxdb v0.11.0 / turso v0.8.0, the two-role posture docs. In this repo, guard
  G34 (`guard-baseline-ledger`) holds the baseline ledger byte-identical to its stores'
  migrations.

**Migration notes — what a repinning host meets.**
- (a) **Migrate, then deploy: authentication 0021 and secrets 0002.** Authentication
  `stores/pgx` v0.10.0 / `stores/turso` v0.9.0 refuse a database without
  `0021_security_events_integrity.sql`, and secrets `pgx.Open` one without
  `0002_secrets_audit_integrity.sql`, each naming the file. Both probes check the presence of
  known columns only and every new column has a default, so the previous binaries keep running
  on a migrated database. Export each rail's integrity file, then its immutable file
  (`0021` → `0022`, `0002` → `0003`). Secrets 0002 adds its `outcome` CHECK `NOT VALID`;
  `VALIDATE CONSTRAINT` is a later host maintenance step.
- (b) **Authorization 0005 is REQUIRED and LOCKSTEP, after Step 0.** Both schema probes are
  exact: v0.19.0 / v0.18.0 refuse a database with 0005 applied and v0.20.0 / v0.19.0 refuse one
  without it (`Repositories`, or any store built `WithAudit()`). Order: every instance on
  `stores/pgx` v0.19.1 / `stores/turso` v0.18.1 first → adopt the authentication (and secrets)
  stores until production boots clean → migrate `0005_iam_audit_integrity.sql`, then
  `0006_iam_audit_immutable.sql` → repin core v0.29.1 with `stores/pgx` v0.20.0 /
  `stores/turso` v0.19.0. After 0005 roll back only to v0.19.1 / v0.18.1, never v0.19.0 /
  v0.18.0. On PostgreSQL 0005 is two metadata-only `ADD COLUMN`s per table. On SQLite/Turso it
  rebuilds `iam_audit` and `iam_operations` as 0004 did, holding the write lock inside the
  runner's two-minute budget: restartable, not online — plan a maintenance window, snapshot or
  branch the database first, and run the 0004 preflight for host views, triggers and indexes.
  Authentication's turso `0021` rebuilds `security_events` the same way. The full procedure is
  in `pockets/authorization/stores/UPGRADE.md` ("Adopting 0005"). Its reverse, reference SQL
  that is not shipped (author it as a new forward ledger entry, never by deleting or editing
  0005 or 0006; it discards the evidence 0005 recorded; roll the binaries back to v0.19.1 /
  v0.18.1 first):

  PostgreSQL:

  ```sql
  DROP TRIGGER IF EXISTS iam_audit_immutable ON iam_audit;
  DROP TRIGGER IF EXISTS iam_audit_no_truncate ON iam_audit;
  DROP TRIGGER IF EXISTS iam_operations_immutable ON iam_operations;
  DROP TRIGGER IF EXISTS iam_operations_no_truncate ON iam_operations;
  DROP FUNCTION IF EXISTS iam_audit_refuse_write();
  DROP FUNCTION IF EXISTS iam_operations_refuse_write();
  -- 0005: drops iam_audit_seq with the column that owns it
  ALTER TABLE iam_audit DROP COLUMN request_id, DROP COLUMN trace_id, DROP COLUMN seq, DROP COLUMN recorded_at;
  ALTER TABLE iam_operations DROP COLUMN recorded_at;
  ```

  SQLite/Turso (a rebuild to the 0001 and 0004 bodies, with the same preflight and maintenance
  window; from the `sqlite3` CLI run it with `-bail`):

  ```sql
  DROP TRIGGER IF EXISTS main.iam_audit_immutable_update;
  DROP TRIGGER IF EXISTS main.iam_audit_immutable_delete;
  DROP TRIGGER IF EXISTS main.iam_operations_immutable_update;
  DROP TRIGGER IF EXISTS main.iam_operations_immutable_delete;
  CREATE TABLE main.iam_audit_prev (
      id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
      event_id TEXT COLLATE BINARY NOT NULL,
      occurred_at TEXT NOT NULL,
      actor_type TEXT COLLATE BINARY NOT NULL,
      actor_id TEXT COLLATE BINARY NOT NULL,
      system_source TEXT COLLATE BINARY NOT NULL,
      reason TEXT NOT NULL,
      action TEXT NOT NULL CONSTRAINT ck_iam_audit_action CHECK (action IN ('added','removed')),
      encoding TEXT NOT NULL CONSTRAINT ck_iam_audit_encoding CHECK (encoding = 'tuple/v2'),
      scope_kind INTEGER NOT NULL,
      resource_type TEXT COLLATE BINARY NOT NULL,
      resource_id TEXT COLLATE BINARY NOT NULL,
      relation TEXT COLLATE BINARY NOT NULL,
      subject_type TEXT COLLATE BINARY NOT NULL,
      subject_id TEXT COLLATE BINARY NOT NULL,
      subject_relation TEXT COLLATE BINARY NOT NULL,
      CONSTRAINT ck_iam_audit_scope CHECK (
          (scope_kind=1 AND resource_type='' AND resource_id='')
          OR (scope_kind=2 AND resource_type<>'' AND resource_id<>'')
      ),
      CONSTRAINT ck_iam_audit_tuple CHECK (relation<>'' AND subject_type<>'' AND subject_id<>''),
      CONSTRAINT ck_iam_audit_source CHECK
          ((actor_type<>'' AND actor_id<>'' AND system_source='') OR (actor_type='' AND actor_id='' AND system_source<>''))
  );
  INSERT INTO main.iam_audit_prev (id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
  SELECT id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation FROM main.iam_audit ORDER BY occurred_at, id;
  DROP TABLE main.iam_audit;
  ALTER TABLE main.iam_audit_prev RENAME TO iam_audit;
  CREATE INDEX main.idx_iam_audit_time ON iam_audit (occurred_at DESC, id DESC);
  CREATE INDEX main.idx_iam_audit_resource ON iam_audit (resource_type, resource_id, occurred_at DESC, id DESC);
  CREATE INDEX main.idx_iam_audit_subject ON iam_audit (subject_type, subject_id, occurred_at DESC, id DESC);
  CREATE INDEX main.idx_iam_audit_actor ON iam_audit (actor_type, actor_id, occurred_at DESC, id DESC);
  CREATE TABLE main.iam_operations_prev (
      operation_id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
      encoding TEXT COLLATE BINARY NOT NULL,
      fingerprint TEXT COLLATE BINARY NOT NULL,
      outcome TEXT COLLATE BINARY NOT NULL,
      committed_at TEXT NOT NULL,
      CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
      CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
      CONSTRAINT ck_iam_operations_fingerprint CHECK (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
      CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found', 'fenced'))
  );
  INSERT INTO main.iam_operations_prev (operation_id, encoding, fingerprint, outcome, committed_at)
  SELECT operation_id, encoding, fingerprint, outcome, committed_at FROM main.iam_operations ORDER BY committed_at, operation_id;
  DROP TABLE main.iam_operations;
  ALTER TABLE main.iam_operations_prev RENAME TO iam_operations;
  ```

- (c) **The sequence grant.** Each pgx integrity file (`0021`, `0005`, `0002`) adds a nullable
  `seq` drawn from `<rail>_seq` (not on `iam_operations`) and a nullable `recorded_at` defaulting
  to the database clock, and ends with `GRANT USAGE ON SEQUENCE <rail>_seq TO PUBLIC`. The grant
  is load-bearing: without USAGE every insert's `nextval` fails, so the fail-open
  authentication rail would lose every row and the fail-closed rails would fail every write.
  A host that split its roles before S2 keeps inserting through the PUBLIC grant; granting
  USAGE to the runtime role and revoking it from PUBLIC is optional hardening, and the probe
  refuses a role left without it. `seq` proves insertion order, not completeness (a rolled-back
  write leaves a gap); `recorded_at` is NULL on rows written before the migration. Never
  backfill either with UPDATE.
- (d) **The immutability triggers are default-on.** `0022_security_events_immutable.sql`
  (pgx and turso), `0006_iam_audit_immutable.sql` (pgx and turso) and
  `0003_secrets_audit_immutable.sql` (pgx) are ordinary numbered migrations: exported, applied
  and checksummed like every other file. **A host that declines one deletes it from its ledger
  before applying.** On pgx the probe then judges the runtime role by its privileges alone
  (`protected_by=privilege`) or refuses; on turso it WARNs once per table at boot. The pgx
  triggers refuse UPDATE, DELETE and TRUNCATE with SQLSTATE 42501 for every role that is not a
  member of the table's owner and are `ENABLE ALWAYS`; the owner (the migrator, or a SECURITY
  DEFINER function it owns) is exempt by design, and its UPDATE persists. `iam_operations`
  refuses UPDATE always and DELETE of a fenced row only. After `pg_restore --disable-triggers`
  the triggers are left `tgenabled=O`: re-run `ALTER TABLE <t> ENABLE ALWAYS TRIGGER …` for
  each. A future turso migration that rebuilds an audit table ships a paired file re-creating
  its triggers, and no audit-table path uses SQLite `INSERT OR REPLACE` (it deletes rows without
  firing DELETE triggers). Host code that issues `DELETE FROM` (or UPDATE) against
  `security_events`, `iam_audit` or `secrets_audit` as the runtime role now fails: run it as
  the migrator.
- (e) **The posture refusal production hosts meet (BREAKING at runtime).** In production a
  store refuses to boot when its runtime role is superuser, owns an audit table, can
  UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified triggers, or cannot insert into it.
  Ownership includes membership of the owning role and ownership of the table's schema or the
  trigger function (`owner=table|schema|function`). The errors are
  `pgxdb.ErrAppendOnlyTableWritable` (waivable with
  `pgxdb.Config.AppendOnlyWritableAcknowledged: true`, a literal in the composition root that
  P5 prints as `ack`; the waiver logs one `posture_waiver=AppendOnlyWritableAcknowledged` WARN
  per waived table, for an owned table "runtime role owns <t>: immutability triggers do not
  apply" with `trigger_exempt=true`) and `pgxdb.ErrAppendOnlyTableUnwritable` (never
  waivable), joined, one per table, each naming its reason and the fix. Development logs one `append_only=unenforced
  tables=[…]` line; the zero mode is silent; turso stores only WARN. **Every production host
  whose server connects as the role that runs its migrations refuses to boot after repinning a
  pgx store with an audit table.** The fix is the two-role posture in
  `workshop/documentation/docs/guides/database-posture.md` ("Two roles"): a migrator that owns
  the schema, tables, sequences and trigger functions, and a runtime role — create the runtime
  role with SQL, not the console — with `SELECT, INSERT` on the audit tables, `USAGE` on their
  sequences, `UPDATE`, `DELETE`, `TRUNCATE` and `TRIGGER` revoked, and `DELETE` kept on
  `iam_operations` (S0 hosts that revoked it grant it back). Preflight the runtime role from the
  migrator's connection before the deploy — `go run ./workshop/migrations -posture-role <role>`
  in a scaffolded host, `pgxdb.InspectTablePrivileges(ctx, q, "<role>", <store>.AuditTables(opts...)...)`
  elsewhere — and archive its `protected_by=privilege|trigger|waiver|none` lines as audit
  evidence.
- (f) **AUDIT-054 — audit Details carry identifier digests, not addresses (BREAKING for
  dashboards, pre-1.0 minor).** New `login` rows carry `identifier_kind` + `identifier_digest`
  instead of `email`; new `invitation_created` / `invitation_granted` rows carry them instead of
  `identifier`. New rows only: existing rows keep their raw values until S3 lands retention.
  Known users are found by `user_id`. The digest is `IdentifierKeyer.IdentifierKey(kind,
  normalized)` in hex — an HMAC over three length-prefixed fields (the label
  `gopernicus.auth.identifier-key.v1`, the kind, the normalized value; an 8-byte big-endian
  length before each), **not** over `email:<normalized>`. AUDIT-054 ships the runnable shell
  recipe for an unknown-address lookup, with its check value
  (`35916db2967bd336a7f3e7b94ebee7e4cd70017d104cb28c7427e8d77a51f478` for the key
  `000102…1e1f` and `alice@example.com`), pinned by `TestIdentifierKeyAuditRecipe`.
- (g) **`MapError` behavior changes for every consumer.** pgxdb maps SQLSTATE `42501`
  (`insufficient_privilege`) to `sdk.ErrForbidden` — a 403 through the web mappers — keeping
  the `*pgconn.PgError` in the chain; turso maps a message containing `is append-only` to
  `sdk.ErrForbidden`. A host that matched the raw driver error for a missing grant sees the
  sentinel now. Nothing refuses at boot from the connector tags alone: the refusal reaches a
  host through the pgx store releases that call the probe, and both migration runners are
  unchanged.
- (h) **PostgreSQL ≥ 14** for every S2 pgx migration (`CREATE OR REPLACE TRIGGER`). Each S2 pgx
  file begins `SET LOCAL lock_timeout = '5s'`: the pending stream runs in one transaction, so a
  file blocked behind a long-running reader (a `pg_dump`) aborts the stream with nothing applied
  and the host retries.
- (i) **Pin authorization v0.29.1, not v0.29.0**, so bridged gate denials name their target:
  under v0.29.0 an `authorization_denied` row written by a `Require` gate carried no
  permission or resource, and the bridge's dedupe window folded a principal's gate denials into
  one row. The decision observer is opt-in; the host-pocket bridge to the security-event rail is
  `examples/production-baseline/pockets/audit/outbound/securityevents` (bounded in-flight writes,
  a per-instance dedupe window, `Dropped()`), built over `Components.SecurityEvents` so its lost
  rows count in `AuditWriteFailures()`.
- (j) **Secrets v0.2.0 breaks host-implemented repositories** (`AppendAudit`, the new
  `DeleteOwner` signature). No example mounts secrets.
- (k) **Constraints S3 inherits (O-3).** S3's purge is a migrator-owned SECURITY DEFINER
  `<table>_purge_before(ts)` that keeps its retention floor inside the function, under owner
  control, never from the caller's argument, and appends a purge row (cutoff, count,
  `session_user`) to the rail it purged; S2 ships the live test that such a function passes the
  owner-exempt branch. A turso purge is `DROP TRIGGER`, `DELETE`, re-`CREATE` in one
  transaction. No later migration may UPDATE-backfill a triggered table; every future turso
  rebuild of an audit table ships its paired trigger file; `iam_operations` keeps non-fenced
  DELETE so `Expire` runs in the server; in-place IP/UA erasure is S3's to design against the
  owner-exempt UPDATE branch.
- (l) **The firestore store adapters are retired (owner ruling 2026-10-23).**
  `pockets/authentication/stores/firestore` is not tagged at this release (it stays at v0.2.0)
  and is not repinned onto core v0.21.0; its retirement, with `integrations/datastores/firestore`,
  is its own plan. Firebase Auth as an identity provider (`integrations/authentication/firebase`)
  stays.

**Known.**
- Secrets `DeleteOwner` race: a `PutSecret` committing between the `DELETE … RETURNING` and the
  owner-key delete surfaces as `sdk.ErrInvalidReference`, not `sdk.ErrConflict` (nothing is
  deleted either way). A later patch locks the owner key.
- Authentication denial paths still unrecorded (follow-up): `SetPassword` with
  `ErrPasswordAlreadySet`; revision races in `passwords.Change`; refused recent-auth grants on
  identifier mutations; the address-claim race on confirm; `RemovePassword` with no password or
  a deactivated account; an unknown OAuth provider name; malformed OAuth2 requests; and the
  OAuth2 confidential-client secret mismatch (refused in the inbound handler before the service;
  `AuthenticateClient` takes no context).
- Run-and-look notes from the examples (PR-6): the development `append_only=unenforced` line
  prints once per store; the owner refusal names the first store constructed; a disabled
  trigger with no modifying grants is admitted as `protected_by=privilege`, as designed; bridged
  denial rows have empty `ip_address`/`user_agent`, because the authorization routes do not
  carry the authentication client-info carrier (follow-up); proving the 200-byte `X-Request-ID`
  blanking needs the 60 s dedupe window to expire first; auth-cms has no signed-in platform
  admin, so its `/debug/security-events` dump is proven by in-process tests.
- `external_login` dedupe is per cache: under the memory default each instance records once per
  token (and once more per eviction); a fleet-wide dedupe needs a shared `LoginRecordCache`.
- The scaffold README still runs `guard@v0.5.0`, which has no P9; the init pin moves in the
  next workshop minor (the S1 precedent).

## Production posture floor (SOC 2 slice S1 — published 2026-10-22)

Ten annotated tags in seven steps, each cut at its PR's squash-merge commit, pushed, THEN
polled on the proxy, and cold-verified from a scratch consumer (`GOWORK=off`, an empty module
cache, `proxy.golang.org`, `sum.golang.org`, `go mod verify`, `go build <module>/...`). Every
one resolved on the first poll. Design and rulings:
[plans/s1-production-posture.md](plans/s1-production-posture.md) (rev 2, ratified
2026-10-22). Host handoff:
[plans/soc2-s1-production-posture-handoff.md](plans/soc2-s1-production-posture-handoff.md).
Manifest: `plans/s1-production-posture-release-manifest.json` (from `go mod download -json`
of the published tags). The cms flip is recorded as
[AUDIT-053](AUDIT.md#audit-053-cms-admin-routes-are-deny-by-absence).

| Step | Tag | Commit | PR | Cold verification |
|---|---|---|---|---|
| 0 | `pockets/cms/v0.4.0` | `2f789281` | #129 | OK, poll 1; builds against sdk v0.9.0 (pin unchanged) |
| 1 | `sdk/v0.16.0` | `bfaf233a` | #130 | OK, poll 1; the consumer auto-switched to go1.26.10 |
| 2 | `integrations/datastores/pgxdb/v0.10.0` | `31d57904` | #131 | OK, poll 1 |
| 2 | `integrations/datastores/turso/v0.7.0` | `31d57904` | #131 | OK, poll 1 |
| 2 | `integrations/tracing/otel/v0.3.0` | `31d57904` | #131 | OK, poll 1 |
| 3a | `pockets/authorization/v0.28.0` | `ddcb1468` | #128 | OK, poll 1 |
| 3b | `pockets/authorization/stores/pgx/v0.19.0` | `10135055` | #133 (repin to core v0.28.0) | OK, poll 1 |
| 3b | `pockets/authorization/stores/turso/v0.18.0` | `10135055` | #133 | OK, poll 1 |
| 4 | `pockets/authentication/v0.20.0` | `b8036dd0` | #134 | OK, poll 1 |
| 5 | `workshop/gopernicus/v0.5.0` | `2de17b50` | #135 | OK, poll 1 |

**Verification record.**
- CI `check` and `vuln` passed on every PR (push and pull_request events); CodeQL analyses
  passed. One `check` run (#129, push event) failed on the known
  `integrations/kvstores/goredis` `TestOpenHonorsDeadlineAfterConnection` timing flake and
  passed on rerun; the module is untouched by S1.
- Live store legs on merged `main`: `make test-stores` pgx legs before tags 2/3a — 18 legs
  (every pgx store bare and schema-qualified, plus the pgxdb connector's own live suite) against
  a scratch Postgres 17, 0 failures. The turso connector and the authorization turso store
  `-tags=integration` suites PASSED against a local libSQL server
  (`ghcr.io/tursodatabase/libsql-server`, zero mode, `http://127.0.0.1:8081`). The hosted Turso
  playground leg could NOT run: every statement returned Turso `error code 404: group not found`
  (the playground database's group no longer exists on the account), so the hosted secret must be
  repointed before the next `live-stores.yml` dispatch. Before tag 4: the authentication pgx
  store live suite (bare and schema-qualified), the authentication turso store `-tags=integration`
  suite (local libSQL) and the core's own tests PASSED on `main` @ `b8036dd0`.
- Tag 5 legs: a cold `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.5.0 init
  --db pgx --module example.com/x` (`GOWORK=off`, empty cache) tidied and built against
  sdk v0.16.0, pgxdb v0.10.0 and pgx v5.9.2; its `.env.example` mentions `APP_RUNTIME_MODE` only
  in a comment and carries `DB_SSLMODE=verify-full` with the commented local-docker line; `guard
  --posture .` on it is clean. The old pin, `go run …/workshop/gopernicus@v0.4.1 guard --posture .`,
  prints `flag provided but not defined: -posture`, the usage text, and `exit status 2` — the
  binary exits 2; a `go run` invocation reports `exit status 2` and returns 1 — non-zero either
  way, so CI must treat any non-zero as a broken invocation, never a pass.
- `examples/production-baseline` (PR #136, untagged, open at this record) was driven live
  against a throwaway Postgres: development boot with HSTS on `/healthz`; production refusals in
  order (`ErrKeyRequired` naming both needed keys with `.env` ignored, `ErrPublicBaseURLRequired`,
  `pgxdb.ErrTLSRequired` class `plaintext` with zero connection attempts); the audited
  `cmd/bootstrap` first-admin grant (403 before, one `iam_audit` row with `system=bootstrap`,
  200 after).

**Owner rulings during execution.** Tags are annotated, not signed (PR #132; a dedicated
release-signing key is a follow-up, FHC-9). Dependabot was removed pending research; CodeQL
default setup stays on. O-1…O-6 of the plan were taken at their recommendations.
`SingleFactorAcknowledged` lives on `IdentityConfig` (the pocket has no exported `Config`).
`ErrCredentialPolicyRequired` was deleted — the only exported removal in the train. The runtime
mode is never assigned in `.env` (strict `LoadEnvForMode`). The baseline gained an audited
first-admin bootstrap.

**Migration notes — what a repinning host meets.**
- (a) **cms admin is deny-by-absence (BREAKING, pre-1.0 minor, AUDIT-053).** A nil or empty
  `cms.Config.AdminMiddleware` no longer mounts the admin routes: `GET /articles` and friends
  fall through to the public page handler (404, or 200 if a published page carries that slug),
  admin `POST`s answer 405, and `Register` logs one WARN. Pass a gate that authorizes, or
  `AdminUngatedAcknowledged: true` for a development demo (production refuses it with
  `ErrAdminUngatedRefused`; beside a gate it is `ErrAdminAcknowledgedWithMiddleware` in every mode).
- (b) **Production refusals and their acknowledgements**, all construction-time, every
  acknowledgement a boolean literal in the composition root with no env tag, and every honored
  one logged once as `posture_waiver=<Flag>`:
  - `pockets/authentication` (positional `RuntimeMode`): nil `Repositories.SecurityEvents` →
    `ErrAuditRequired` unless `WithAuditDisabledAcknowledged()` (beside a wired rail:
    `ErrAuditAcknowledgedWithSecurityEvents`, every mode); native single-factor login — password
    flows on or any `Passwordless` kind — → `ErrSingleFactorUnacknowledged` unless
    `IdentityConfig.SingleFactorAcknowledged`; a delivery transport declaring opportunistic
    STARTTLS → `ErrInsecureDeliveryTransport` (wrapping `notify.ErrOpportunisticTLS`) unless
    `DeliveryConfig.OpportunisticTLSAcknowledged`, honored at BOTH check sites; a provider
    declaring `DevelopmentOnly` → `ErrDevelopmentOnlyProvider`. Production joins every violation
    into one error naming each acknowledgement.
  - `pockets/authorization` (`WithRuntimeMode(mode)`, zero = mode-blind): `Repositories.Tuples`
    (and `Mutations` when wired) must report `audit.RecordingReporter` true and
    `Repositories.Audit` must be wired → `ErrAuditRequired` unless `WithAuditDisabledAcknowledged()`
    (beside a recording authority: `ErrAuditAcknowledgedWithRecording`, every mode). `Components`
    now exposes `Audit`. Wrappers forward `AuditRecording()`.
  - `pockets/cms` (`Config.RuntimeMode`): the acknowledgement itself, and a Mailer failing
    `notify.CheckTransport` → `ErrInsecureMailTransport`.
  - `integrations/datastores/pgxdb` (`Config.RuntimeMode`): every connection target — primary and
    fallbacks — must be *verified* (`verify-full`, `verify-ca`, `require`+`sslrootcert`,
    `sslrootcert=system`) or *local* (a unix socket) → `ErrTLSRequired` otherwise;
    `TLSUnverifiedAcknowledged` admits a bare `require`, `TLSDisabledAcknowledged` admits all;
    `LogQueries`, or a `Tracer` that is or wraps `*LoggingQueryTracer`, → `ErrQueryLoggingRefused`,
    no acknowledgement.
  - `integrations/datastores/turso` (`Config.RuntimeMode`): a non-`file:` URL that is not
    `libsql`/`https`/`wss`, or `libsql` with `tls=0`, → `ErrTLSRequired` unless
    `TLSDisabledAcknowledged`.
  - `integrations/tracing/otel` (`Config.RuntimeMode`): an empty `Exporter` →
    `ErrExporterRequired` (the `default:"stdout"` env tag is gone; `Open` still fills stdout
    outside production); OTLP `Insecure` to a non-loopback endpoint → `ErrInsecureExporter`.
  - `sdk`: `notify.CheckTransport`'s default verdict is unchanged; the STARTTLS refusal is the
    opt-in `notify.RequireMandatoryTLS()`. `web.SecurityHeaders()` (HSTS, nosniff, referrer,
    `X-Frame-Options: DENY`, permissions policy; zero `FrameOptions` means DENY,
    `DisableFrameOptions` drops it, `HSTSMaxAge < 0` emits `max-age=0`);
    `ServerConfig.ValidateProduction()` (a bare https origin, never echoed); `RespondJSON*` set
    `nosniff`; `environment.LoadEnvForMode`; `oauth.CheckProvider` / `sdkauth.CheckProvider`;
    `email.SMTP.Probe` and `SMTPConfig.RequireTLS`.
- (c) **Key continuity.** `authentication.NewKeys` decodes all five keys as hex. The reference
  host used the hex TEXT of `AUTH_JWT_SECRET` as key material and the raw 32-character strings of
  `AUTH_DELIVERY_ENCRYPTER_KEY` and `AUTH_TOKEN_ENCRYPTER_KEY`. To keep every outstanding access
  token, in-flight sealed outbox job and stored provider token valid, set each new variable to
  the hex of the OLD bytes — `printf %s "$OLD" | xxd -p | tr -d '\n'` — and never mint a new
  value at the upgrade. `AUTH_TOKEN_ENCRYPTER_KEY` stays optional in every mode.
- (d) **The mode comes from the process environment only.** `environment.LoadEnvForMode(key)`
  refuses a `.env` that assigns the key (`ErrModeFromFile`), never opens `.env` in production
  (one WARN if present), and loads it in development. A host that ships `.env` beside a
  production binary moves EVERY variable to the process environment, not only the mode.
- (e) **Consumer toolchain floor.** These are the first published tags carrying the S8
  `go 1.26.10` directive and pgx v5.9.2: a consumer building with `GOTOOLCHAIN=local` on an older
  toolchain fails; `GOTOOLCHAIN=auto` switches on its own.
- (f) **Pair the authorization core with its stores.** Bump `pockets/authorization` v0.28.0
  together with `stores/pgx` ≥ v0.19.0 / `stores/turso` ≥ v0.18.0 before passing
  `WithRuntimeMode(production)`: a v0.28.0 core over an older store refuses with
  `ErrAuditRequired` ("does not report AuditRecording()").
- (g) `authentication.ErrCredentialPolicyRequired` is removed (it was never returned).
- (h) `gopernicus guard --posture` needs workshop/gopernicus ≥ v0.5.0; an older pin fails the
  invocation (see the tag-5 leg above) and CI must treat that as broken, never as a pass.

**Known.** The hosted Turso playground is unavailable (above). The S1 scaffold's `guard` target
still carries no `tool` pin (v0.4.0 Known). `examples/README.md` §3b says the old pin "exits 2";
the one-clause clarification about `go run` returning 1 belongs in a follow-up after #136 lands.

## Scaffold pins (workshop/gopernicus v0.4.1 — published 2026-10-21)

**PATCH.** The templates move to the current tags, so a freshly scaffolded host or
pocket starts on today's releases:
- sdk v0.9.0 → **v0.15.0**: `init`, plus the pocket core and its pgx and turso stores;
- `integrations/datastores/pgxdb` v0.7.0 → **v0.9.0**: `init --db pgx` and the pocket pgx store;
- `integrations/datastores/turso` v0.4.0 → **v0.6.0**: `init --db turso` and the pocket turso store.

No template code changed. Existing hosts are unaffected, because the CLI only
writes these pins at scaffold time.

**Verified before merge.**
- With `GOWORK=off`, an empty module cache and the public proxy,
  `init --db none|pgx|turso` and `new pocket` (core plus pgx and turso stores)
  each pass `go mod tidy`, `go build`, `go vet` and `go test`, resolving to
  exactly the pins above.
- `gopernicus guard` on each emitted host exits 0.

**Tag and verification.** `workshop/gopernicus/v0.4.1` is annotated at
`2de8edc6` (#121 merge). The proxy `.info` resolved on the first poll. A cold
`go run …/workshop/gopernicus@v0.4.1 init --db pgx` (`GOWORK=off`, an empty
module cache) emitted a host that tidied and built against sdk v0.15.0 and
pgxdb v0.9.0.

**Known.** Emitting the `tool` pin and a `guard:` target in `init` (v0.4.0 Known,
YOUR CALL 7) is still deferred.

## Authorization protocol, slice A (sdk v0.15.0; authorization v0.27.0 — published 2026-10-21)

Two tags, in order, both cold-verified through the proxy on the first poll:
- `sdk/v0.15.0` @ `1ed60c4f`: #116, then #117. #117 applied owner ruling D6′,
  which says grammar belongs to the implementation, before the tag.
- `pockets/authorization/v0.27.0` @ `f5eda0a4` (#118).

The store modules were repinned in #119, with no tags. Design and rulings:
[plans/authorization-provider-port.md](plans/authorization-provider-port.md)
(rev 4.2, slice A; slices B–E still open). Host handoffs:
`plans/segovia-v2-authorization-v0.27-repin-handoff.md` and
`plans/gps-360-go-authorization-v0.27-repin-handoff.md`. Host guidance:
"The authorization protocol" in `pockets/authorization/README.md` and the v0.27.0
section of `pockets/authorization/stores/UPGRADE.md`.

**BREAKING, pre-1.0 — `pockets/authorization` v0.27.0.**
- **`Check` / `CheckBatch` return `sdkauthorization.Decision`.**
  `decisions.Service.Check` is `(sdkauthorization.Decision, error)` and
  `CheckBatch` is `([]sdkauthorization.Decision, error)`; `Decision` carries
  `Allowed` only. Reasons stay in `CheckExplain` and the decision log.
  **Host retypes** (mechanical): every narrow interface typed
  `Check(...) (model.CheckResult, error)` or `CheckBatch(...) ([]model.CheckResult, error)`
  — gps-360-go `ask/allowed.go:18` and `access.go:38`; Segovia's `CheckBatch`
  ports. Code that read `ReasonCode`/`Reason` from a check calls `CheckExplain`.
- **`model.PrincipalRef` is `sdk.Principal`** and has no `Validate` method; call
  `model.ValidatePrincipal`. `model.Resource`, `model.CheckRequest` and
  `model.LookupRequest` are deprecated aliases of the sdk types; everything else
  compiles through them. Their `Validate` methods check structure only
  (non-empty fields, `Limit` not negative); the pocket's opaque-reference
  grammar is `model.ValidateCheckRequest` / `model.ValidateLookupRequest`, which
  the decision service uses unchanged.
- **`Require` answers 503 for any `sdk.ErrUnavailable`**, not only
  `ErrEvaluationLimit`: TupleCache unavailability, a closed snapshot and a tuple
  read limit move from 500 to 503.

**What changed.**
- **sdk v0.15.0 (MINOR, additive).** `sdk/capabilities/authorization`
  (`sdkauthorization`): `Checker`, `Lookup`, `Authorizer`, optional
  `SchemaReader`/`Limiter`, `Decision`, structure-only request `Validate` (grammar
  belongs to each implementation, owner ruling D6′), the opt-in strict
  `ValidatePortable` (with `ValidatePortableRef`/`ValidatePortableObject`), the
  cursor envelope `EncodeCursor`/`DecodeCursor`, `FilterAuthorized`, `LookupAll`,
  `ErrInvalidCursor`, `ErrConditional`; `authorizationtest.Run` with four fixtures
  and `WithGrammar` for the implementation's declared grammar.
- **authorization v0.27.0.** `Components.Authorizer` (`logic/authorizer.Engine`),
  the implementation of record, as lenient as the pocket's own grammar;
  `decisions.Service.EvaluateBatch`;
  `decisions.NewLoweredEvaluator` + `WithGlobalResource`;
  `authorizationhttp.WithLoweredPolicy()` (Require refuses a lowered evaluator
  without it); `decisions.ResourceSlot.Pure` / `BindPureResource` (set by `Path`);
  `authorizer.ErrUnrestricted` (wraps `sdk.ErrUnavailable`; do not retry);
  `storetest.RunAuthorizer`. `model.ErrInvalidCursor` is the sdk sentinel.
- **examples/auth-cms.** Document listing decides `view` through
  `Components.Authorizer` as an `sdkauthorization.Checker`
  (`documents.ProtocolListing`); pages match the native Candidates listing.

**sdk catch-up.** The pocket and its stores pin sdk v0.9.0; v0.27.0 pins v0.15.0.
The v0.9.0 → v0.14.0 audit (plan, A0) found no removed export; the behaviour
changes (static-file caching, email unsubscribe headers, opt-in origin admission)
touch nothing the pocket or its stores use.

**Stores.** pgx, turso and goredis runtime code is unchanged. Their tests gain
`TestAuthorizer` (live, env-gated), so the repin PR moves them to sdk v0.15.0 and
authorization v0.27.0 (goredis also its pgx/turso pins). No store tags.

**Gates before the pocket tag.** `make check`, `make guard`; the live PostgreSQL and
Turso store legs (including `TestAuthorizer`) on merged main;
`GOWORK=off go build ./... && go vet ./...` on pgx, turso and goredis against the
new pins; cold-verify each tag.

## External identity providers (sdk v0.14.0; go-oidc v0.1.0; firebase v0.1.0; auth0 v0.1.0; authentication v0.19.0 → v0.19.1; stores pgx v0.9.0, turso v0.8.0 — published 2026-10-20)

One train, eight tags, in dependency order. A host can now admit tokens from an
external identity provider (Firebase Auth, Auth0, or any OIDC issuer) as
ordinary `sdk.Principal`s, with the authentication pocket as the identity home.
Design, rulings and build record:
[plans/external-identity-providers.md](plans/external-identity-providers.md).
Host guidance: the "External identity providers" section of
`pockets/authentication/README.md`.

**BREAKING — `pockets/authentication` v0.19 (pre-1.0 MINOR).** Hosts: **skip
v0.19.0 and take v0.19.1** (v0.19.0's regression is under Known).
- **`CredentialService` signatures.** `Authenticate` and `RequireLive` return
  `(context.Context, error)` instead of `(context.Context, bool)`, so the guard
  can tell a denial (401) from an outage (503). `RequireLive` takes a
  `LivenessTier` and an `unsafe` flag. The interface gains `ClassifyBearer` and
  `ExternalEnabled`. This breaks compilation only for a host that implements
  `CredentialService` itself; none is known.
- **The default set.** `RequirePrincipal()` with no `Accept` admits every
  wired kind, so it admits external tokens once providers are wired. The named
  helpers stay literal: `RequireAccessTokenOrAPIKey*` never admit an external
  token. `/auth/csrf` moves to its own `CSRFBootstrap` group; `/auth/me`, user
  administration and invitations admit every wired kind.
- **R-LIVE: native POSTs now read the session row.** The default liveness tier
  is `LiveOnMutation()` for every kind, native access tokens included. A host
  `POST`/`PUT`/`PATCH`/`DELETE` route that used to check only the access JWT's
  signature now pays one session read and denies a revoked session. Safe reads
  stay stateless. Escape per route with `RequirePrincipal(Stateless())`.
- **503 on store errors.** A session-store read error used to look like a
  missing session (401). It is now `503` with `Retry-After: 5`, on the default
  tier and `Live()`. `Browser()` answers the JSON 503 instead of the login
  redirect, and `Optional()` answers 503 instead of anonymity. A missing or
  expired row is still 401.
- **Strict CSRF for cookie clients (v0.19.1).** Every bundled mutation reached
  with a cookie needs the `X-CSRF-Token` header (bootstrap `GET /auth/csrf`),
  whatever the body's content type. The four authenticated invitation POSTs
  (create, accept, cancel, resend) are now gated too. Only the form-lane routes,
  whose handlers check a body `csrf_token` (the bundled HTML forms), still accept
  a form body without the header. Bearer and API-key callers are unaffected.
- **Migration 0020 is required.** `0020_external_subjects.sql` must be in the
  ledger of **every** pgx and Turso host before it deploys stores pgx v0.9.0 /
  Turso v0.8.0, even with no external provider wired: both store constructors
  probe the table at boot and refuse to construct without it.

**What changed.**
- **sdk v0.14.0 (MINOR, PR #99).** `cryptids.JWTVerifier`, a plain port for
  JWTs from another issuer. `sdk/capabilities/authentication` (`sdkauth`), the
  external identity port: `Provider` → `Identity`, which carries no principal
  and no user id; optional `RevocationChecker` (facts, not verdicts);
  `Profile`/`ClaimMapping`; the stdlib `NewOIDC`; stable errors including
  `ErrProviderUnavailable` with an `UnavailableError` kind;
  `authenticationtest.RunProvider` and `RunRevocation`. Additive.
- **integrations/cryptids/go-oidc v0.1.0 (new, PR #100).** A
  `cryptids.JWTVerifier` over `coreos/go-oidc`. It pins `iss`, requires an
  audience, allows only asymmetric algorithms, and applies 60 s of tolerance.
  Cheap rejections run before any fetch. The JWKS refetch floor is per issuer
  and single-flight, the last good key set survives a failed fetch, and the
  unknown-kid cache is bounded. `oidctest` is a local dev issuer.
- **integrations/authentication/firebase v0.1.0 + auth0 v0.1.0 (new, PR #101).**
  Firebase, over the Admin SDK: ID-token and session-cookie providers,
  `CheckRevocation`, `MintSessionCookie`, anonymous users denied, and production
  construction refused while `FIREBASE_AUTH_EMULATOR_HOST` is set. Also
  `make dev-firebase` (an Auth emulator under `workshop/dev/firebase`). Auth0: a
  zero-library `Profile` + `Issuer`. **Client-credentials tokens are denied by
  default** (owner ruling): machines stay native API keys.
- **pockets/authentication v0.19.0 (PRs #102 engine, #104 HTTP guard).** The
  external engine (`authlogic.NewExternalAuthenticator`, or `WithExternal` on
  `New`). It routes a token by its `iss` to exactly one provider, maps the
  subject through the `external_subjects` mirror keyed by a host-chosen provider
  name, and provisions just in time. A collision with a native login email is
  denied and audited. Revocation facts are cached (30 s TTL, 5 m outage grace),
  and an outage fails closed unless the provider sets `RevocationFailOpen`.
  Five new security events. In HTTP: `CredentialExternalToken`, provider cookies
  (`WithExternalCookies`), the `Live`/`LiveOnMutation`/`Stateless` tiers, the
  assurance options `RequireMFA`/`FreshAuth`/`VerifiedEmail`/`Tenant`/`Check`
  (RFC 9470 `WWW-Authenticate` on MFA and freshness denials), and
  `RequireExternalToken*`. A provider user is denied on native step-up,
  sessions and credential management, by design.
- **pockets/authentication v0.19.1 (PATCH).** #106: `ClassifyBearer` sends
  every JWT-shaped bearer whose `iss` is not a wired provider to the host
  signer, as before v0.19. #107 (fixes #105): the invitation create, accept,
  cancel and resend POSTs are CSRF-gated, and decline is origin-gated. #108: the
  CSRF gate is strict; the form lane is opt-in per route.
- **stores pgx v0.9.0 + turso v0.8.0 (PR #103 code, #110 pins).** Migration
  `0020_external_subjects.sql` (identical filename in both trees), the
  `ExternalSubjects` repository, `Users.Provision` writing the mirror row in the
  user's transaction, the boot probe, and `storetest.RunExternalSubjects`.
  Firestore has no mirror this release (documented nil).
- **Examples (PR #111, untagged).** `examples/external-identity`: an
  external-only host with no datastore driver, signer, sessions or mail, added
  to G30. `examples/auth-cms` gains `AUTH_EXTERNAL_DEMO=1`: a hybrid route
  (provider token or API key) and an MFA route (README Leg 12).
- **Guards.** `guard-auth-no-vendor-names` keeps vendor names out of the pocket
  core. `make check` now also vets the provider `-tags=live` legs (auth0,
  firebase, go-oidc) and the firebase `-tags=emulator` leg, compile-only.

**Tags and verification.**
- `sdk/v0.14.0` at `d4c3bfa43499b74b1701e27cf35c3746cb0ca7dd` (PR #99).
  `integrations/cryptids/go-oidc/v0.1.0` at
  `845d83133d220379595f898db44cea1ad808a86c` (PR #100).
  `integrations/authentication/firebase/v0.1.0` and
  `integrations/authentication/auth0/v0.1.0` at
  `a7b97e4bea578f69d72acbcc0dc881b2b11fea2e` (PR #101).
  `pockets/authentication/v0.19.0` at `6a71627ce79cfa2afe2cdb32c47935c7edae6cda`
  (PR #104; with #102 and #103). `pockets/authentication/v0.19.1` at
  `5ae1ada8ce4b4a4799306fff640fec435eba247c` (PR #108; with #106 and #107).
  `pockets/authentication/stores/pgx/v0.9.0` and
  `pockets/authentication/stores/turso/v0.8.0` at
  `2c7476070844c253d4720db8a414ef78146d988f` (PR #110). All annotated.
- The proxy resolved each tag before its dependents were pinned (sdk and
  go-oidc in about 10 s); the store pins were tidied with `GOWORK=off`.
- Cold verification (`GOWORK=off`, empty module cache, `proxy.golang.org`,
  `sum.golang.org`), as recorded per tag. sdk: the capability and cryptids
  tests, an ES256 scratch consumer, `go mod verify`. go-oidc: a real-HTTP
  scratch. firebase: all 7 emulator tests against the PUBLISHED module.
  authentication v0.19.0 and v0.19.1: 19 packages ok. stores: live PostgreSQL
  bare and under a schema (11 `ExternalSubjects` cases), local Turso,
  `go mod verify`.
- `make check` passed on each PR before merge. The pocket's live pgx suite passed for
  #106 and on `main` after #108. The task-7 run-and-look (8 steps on
  `examples/external-identity`, the auth-cms hybrid routes) was re-run by the
  lead.
- **Not exercised:** no real Firebase project and no real Auth0 tenant (owner
  ruling: the Firebase legs run on the local emulator, whose tokens are
  unsigned; the signed-JWT path is proven by go-oidc against `oidctest`). The
  firebase `-tags=live` leg exists and was not run for v0.1.0.

**Known.**
- **v0.19.0 regression — do not pin it.** Its `ClassifyBearer` refused a
  JWT-shaped bearer whose payload was not JSON, or whose `iss` was neither the
  delegated issuer nor a wired provider, before the host `TokenSigner` saw it.
  A host signer that sets its own `iss` or emits non-JSON dotted tokens got 401
  on every request. Fixed in v0.19.1 (#106). gps-360-go and Segovia use
  `golangjwt.New` (no `iss`) and were not affected.
- **The form-encoded CSRF hole (before v0.19.1).** The gate skipped the
  token for every form body, but only the account-form handlers check a body
  token, and about 35 routes sat behind it. Confirmed live: a cookie form POST
  with no token cancelled an invitation (200). Modern browsers' cross-site and
  sibling-subdomain requests were still blocked by the Origin allowlist and
  `SameSite=Lax`. Fixed in v0.19.1 (#108).
- **Cold runs of stores pgx v0.9.0 / Turso v0.8.0 need
  `-skip '^TestMigrationParity$'`.** The test reads the sibling store's
  migrations directory, which a published module does not contain. It now
  skips itself when the sibling is absent; that ships with the next store tag.
- **CI has no PR-triggered live PostgreSQL leg** (#109). `live-stores.yml` is
  dispatch-only, and its hosted Turso target is gone. The v0.19.0 regression
  slipped through that gap.
- **Unverified vendor facts:** the Firebase IAM permission names
  (`firebaseauth.users.get`, `firebaseauth.users.createSession`), Clerk and
  Okta password-hash export, and every Clerk, Cognito, Okta and Keycloak claim
  name (no preset; README recipes cite the vendor pages). What the Firebase
  Admin SDK's `Token.Claims` carries WAS verified against the emulator
  (firebase README).
- **Known limitation, host-route CSRF (owner ruling 2026-10-21, corrected the same
  day):** the pocket's double-submit gate covers only its bundled `/auth/*` routes,
  and the pocket exports no gate for a host's own routes. Host routes are the
  host's responsibility, **on every host, native (`New`) or external-only
  (`NewAuthenticator`)**. A host whose clients authenticate with the session
  cookie (for example a same-origin SPA calling its API with
  `credentials: 'include'`) **must** wrap its router in
  `http.CrossOriginProtection` (ARCHITECTURE host posture); gps-360-go's
  `cmd/server` is the reference. CORS is not a substitute, because it governs
  reading responses, not sending forged requests. Bearer- and API-key-only hosts
  need nothing, since those credentials are not ambient. The residual limitation:
  `CrossOriginProtection` admits a request that carries neither `Origin` nor
  `Sec-Fetch-Site` (non-browser clients, very old browsers), and the
  `SameSite=Lax` session cookie is the backstop there. The first wording of this
  entry reasoned that host traffic is "overwhelmingly API requests". That does
  not hold for cookie-authenticated SPAs, so it was corrected.
- **Deferred:** the native handshake exchange (F1), Firebase anonymous users
  (F3), the symmetric collision check in native registration (F4), identifier
  refresh on a later verified token (F5), revocation adapters for Keycloak and
  others (F6), a pocket-less middleware (F7), the native-session seam (F8), a
  Firebase scrypt `Hasher` (F9), Firestore `external_subjects` (F10).
- Downstream (owner):
  - **gps-360-go and Segovia v2:** repin `pockets/authentication` to
    **v0.19.1** (skip v0.19.0) and the auth stores to pgx v0.9.0 / Turso v0.8.0.
    Copy or re-export `0020_external_subjects.sql` into the host ledger and
    apply it before deploying. Make sure every cookie client sends
    `X-CSRF-Token` on bundled mutations, including invitations and form-encoded
    posts. Neither host implements `CredentialService` (owner grep), so the
    signature break is internal. Expect one extra session read on host mutation
    routes (R-LIVE); add `Stateless()` where that is wrong.
  - Unrelated and still open: #95 (jobs fenced runtime wake signal).

## Host contract reconciliation (workshop/gopernicus v0.4.0 — published 2026-10-20)

MINOR release of the `workshop/gopernicus` CLI, alone. It adds the `guard` verb,
the executable floor of the host contract in `examples/README.md` (issue #90).
No other module is tagged, and no library API changes. Design, rulings and
build notes: [the plan](plans/host-contract-reconciliation.md).

**What changed.**
- **The contract (PR #91).** `examples/README.md` adopts the reference host's
  shape. Host tech adapters live at top-level `integrations/<capability>[/<vendor>]`
  and only `cmd` names them (H11). A host may have a top-level `sdk/` (H13) and
  `ui/` (H14). H12 is the outer import fence, and H10 gains the client-binary
  carve-out. The series is now H0–H14, fifteen rules.
- **The example hosts (PR #93).** auth-cms, cms, minimal and goth-showcase
  moved their out-of-shape packages to host-pocket or `ui/` homes, with package
  names kept. auth-cms's delivery supervisor moved out of `cmd/server` into
  `deliveryhealth`. auth-cms passes the floor only; its `cmd/` provider behavior
  is recorded as named debt.
- **The verb (PR #96).** `gopernicus guard [host-root]` checks the fifteen rules
  with the standard library alone (`go/parser`), so the module still has zero
  `require` lines. `gopernicus guard --list` prints the canonical sentences, and
  `TestGuardListMatchesContract` keeps them byte-identical to the README table.
  It prints one status line per rule, then one line per finding. Exit codes:
  0 clean, 1 findings, 2 for a missing `go.mod`, an unparsable file or a bad
  flag. There is no skip list, allow file or config.
- **G30 `guard-host-contract`** runs the verb over all five example hosts in
  `make guard`, which makes it part of CI. Twenty-nine guards in total.

**Tag and verification.** The tag `workshop/gopernicus/v0.4.0` is annotated at
`fef8f47b70fe0ae6a310533e41cd756fe743fd94` (PR #96 merge). `check` passed on
that commit on `main` before tagging.

- The proxy `.info` resolved about 10 s after the push, with `Origin.Hash` =
  `fef8f47b…`.
- A cold download (`GOWORK=off`, an empty module cache, `proxy.golang.org`,
  `sum.golang.org`) passed: `go run …/workshop/gopernicus@v0.4.0 guard --list`
  printed 15 lines, `version` ran, and `guard` on a fresh copy of
  `examples/minimal` printed `guard: clean (15 rules, 5 packages)` and exited 0.
- Before merge, all five example hosts were clean. A copy of minimal with a
  stray `internal/leak` package and a logic→outbound import exited 1 with
  exactly one H0 and one H1 finding.

**Known.**
- `go run` reports a child exit of 2 as 1. Use a built or `go tool` binary when
  the distinction matters; G30's `|| exit 1` works either way.
- The `init` skeleton still emits the three grep guards. Emitting the `tool`
  pin and a `guard:` target is deferred to v0.4.1 (YOUR CALL 7).
- Downstream: gps-360-go, venona-project and Segovia v2 pin the tool with
  `go get -tool github.com/gopernicus/gopernicus/workshop/gopernicus@v0.4.0`
  and settle their findings in their own slices. The plan's "Downstream
  handoff" section lists each host's findings.

## Secrets pocket (sdk v0.13.0; pockets/secrets v0.1.0; pockets/secrets/stores/pgx v0.1.0 — published 2026-10-17)

Three tags for one feature, in dependency order. Design and build record:
[plans/secrets-pocket.md](plans/secrets-pocket.md).

**What changed.**
- **sdk v0.13.0 (MINOR).** `cryptids.KeyRing`, `ParseKeyRing`, `KeyedEncrypter`
  and `NewAESGCMRing`: ciphertext names its key (`v1.<key id>.<AESGCM payload>`),
  the active key encrypts, every ring key decrypts, and a dropped key yields
  `ErrUnknownKeyID`. `AESGCM` and `Encrypter` are unchanged; no host code changes.
- **pockets/secrets v0.1.0 (new).** Owner-scoped secrets encrypted at rest with
  a per-owner data key wrapped by the master ring; every read, write, delete
  and key operation audited in the same transaction; reads require a `Source`.
  `logic/vault`, `logic/rotation` (`RewrapOwnerKeys`, `RotateOwnerKey`),
  `stores/memory`, `stores/storetest`. No HTTP adapter.
- **pockets/secrets/stores/pgx v0.1.0 (new).** Three tables, `WithSchema`,
  `StatusCheck`, `ExportMigrations`; `ReplaceOwnerKey` is a compare-and-swap.

**Tags and verification.**
- `sdk/v0.13.0` is annotated at `96643d1a68439540d36ba34e545c3874b931a495`
  (PR #85 merge). `pockets/secrets/v0.1.0` at
  `0dc4ee686373339354b65587c5a1945a0a2eddad` (PR #86, which also pinned sdk
  v0.13.0 and dropped the pre-tag replace). `pockets/secrets/stores/pgx/v0.1.0`
  at `dddf25af3f0e37a0834e15bea283fff92ac09cdc` (PR #87, pinning the pocket and
  dropping the last replace). Every PR passed `make check` on CI.
- Each proxy `.info` resolved before the dependent pin moved (each pin's
  `go mod tidy` ran with `GOWORK=off` against `proxy.golang.org`).
- A cold scratch consumer (`GOWORK=off`, empty module and build caches,
  `proxy.golang.org`, `sum.golang.org`) requiring all three passed build, vet,
  tests (the storetest suite on the memory store plus a put/get/rewrap round
  trip) and `go mod verify`.
- Live PostgreSQL 17 conformance ran before the merge, bare and under a schema,
  with the 16-racer `ReplaceOwnerKey` proof; it is env-gated
  (`POSTGRES_TEST_DSN`) and not part of CI.
- Known: PR #85's `TestAESGCMRingRejectsBadCiphertext` tamper case was flaky
  (it flipped a base64 character, not a decoded byte) and failed once on PR
  #86's first CI run; fixed in #86 as a test-only change. `sdk/v0.13.0` runtime
  code is identical.

Checksums and provenance: [release manifest](plans/secrets-pocket-release-manifest.json).

## Direct-hit short circuit (authorization v0.26.0 — published 2026-10-14)

MINOR release of authorization core `v0.26.0`, alone. This is Phase 1 of the
per-route lookup strategy. No store tag, no migration and no host code change.
The pgx, Turso and goredis adapters were repinned to `v0.26.0` for their own
tests only (PR #82), because their conformance tests call the new
`storetest.RunLookupEquivalence` suites. Their runtime code is unchanged, so
existing adapter tags (pgx `v0.18.0`, Turso `v0.17.0`, goredis `v0.5.0`) work
with `v0.26.0`.

**What changed.** Before a `Direct` relation check expands subject-first, the
engine probes the principal's own concrete tuple on the resource. A hit grants.
A miss denies when the relation admits no userset subject. Otherwise the
existing expansion runs unchanged, and no extra budget step is charged.

- Batch traversal reads each distinct exact fact once per round.
- The optimized batch probes all IDs with one `ContainsMany`.
- Answers change only at the budget edge. A principal over `MaxGraphStates`
  now gets a grant on a direct hit, or a deny on a miss for a relation that
  admits no usersets, instead of `ErrEvaluationLimit`.
- Explain steps for a hit carry the new `Outcome: direct_hit`. `CheckResult`
  codes are unchanged.
- Costs: a miss on a relation that admits usersets pays one extra round trip.
  With the tuple cache on, a resource with very many grantees costs about 1 ms
  per check. See `stores/UPGRADE.md` and
  [BENCHMARKS.md](pockets/authorization/BENCHMARKS.md).

**Tag and verification.** The tag is annotated at
`826c519767578c7540805e20d2d229821cf73fa5` (PR #81), and that commit passed CI on
`main`.

- The proxy `.info` resolved before the adapter repin moved.
- A cold download (`GOWORK=off`, empty module cache, `proxy.golang.org`,
  `sum.golang.org`) passed build, vet, uncached tests and `go mod verify`.
- Equivalence and adapter suites ran on local PostgreSQL 17, local SQLite (Turso
  untagged and `-tags=integration`), and goredis against a local
  `redis-server`. **Hosted Turso was not run: the playground database no longer
  exists.**
- An owner rerun of `BenchmarkDirectLookupPostgres` reproduced the recorded
  numbers within about 3%.
- A proposed 7-column pgx index was measured and dropped by owner ruling: it
  grew that index from 12 MB to 120 MB to fix only freshly bulk-loaded,
  un-vacuumed tables.

Checksums and provenance: [release manifest](plans/per-route-lookup-strategy-release-manifest.json).
Design, rulings and build notes: [the plan](plans/per-route-lookup-strategy.md).
Host repins: [gps-360-go](plans/gps-360-go-authorization-v0.26-repin-handoff.md),
[Segovia v2](plans/segovia-v2-authorization-v0.26-repin-handoff.md).

## Tuple-cache mirror keyed on its binding (authorization v0.25.0; goredis v0.5.0 — published 2026-10-14)

Fixes #76. This release has two parts: the core MINOR `pockets/authorization` `v0.25.0`, and the
Redis adapter `pockets/authorization/stores/goredis` `v0.5.0`. The pgx and Turso adapters were
repinned without a tag; their runtime code did not change, so pgx `v0.18.0` and Turso `v0.17.0`
remain current.

**What changed.**
- **Keys.** `tuplecache.Backend.State` now takes the runtime binding. The goredis mirror is keyed
  `tuplecache:v3:{<binding>}`, or `{<namespace>/<binding>}`, so hosts never type a value that has
  to match the binding.
- **Breaking constructor change.** `goredis.NewTupleCache(client, namespace, opts...)` becomes
  `NewTupleCache(client, opts...)`, and the namespace moves to the optional
  `goredis.WithNamespace`. goredis v0.4.0 does not satisfy the v0.25 `Backend` interface, so
  hosts must bump both modules together.
- **Expiry.** Every publication renews a Redis `PEXPIRE` on the mirror (`WithExpiry`, default 7
  days; `0` never expires). Superseded mirrors therefore expire on their own.
- **New defaults.** `Policy.MaxStaleness` zero now selects 5s. The default `PollInterval` is now
  `min(1s, MaxStaleness/4)`, previously `min(250ms, …)`.
- **Known limitation, issue #80.** Run one steady-state binding per authorization database: every
  replica must use the same `MaxStaleness`. While two bindings overlap, each one rebuilds in full
  on every poll. Answers stay correct; only the cost rises. The owner has no plans to build
  per-binding receipts.

**Host rollout.** Redis keys move from `v2` to `v3`.
- Deploy quickly, or turn the cache off, deploy, and turn it back on.
- Old binaries `PERSIST`ed the `tuplecache:v2:` key. Delete it by hand once the whole fleet runs
  v3, and never before.
- Rollback is safe: a v2 binary ignores the v3 keys and rebuilds its own key once.

The goredis README covers all of this under "Redis key", "One binding per authorization
database" and "Mirror expiry and orphans".

**Tags and verification.**
- `pockets/authorization/v0.25.0` is annotated at `fabc62613874fe5c59de4ac2a19f21268b9af7d7`
  (PR #77). The pin commit came next (PR #79), and `stores/goredis/v0.5.0` is annotated on it at
  `63af377b950a2ae14b03798dfb16e805e40a12df`.
- PR #78 fixed a race in the auth-cms resend test, which had been taken for a known flake.
- Each proxy `.info` resolved on the first poll.
- This record was backfilled on 2026-10-14 after the v0.26.0 release. Both archives were then
  re-verified cold (`GOWORK=off`, empty module cache, `proxy.golang.org`, `sum.golang.org`):
  build, vet, uncached tests (goredis against a local `redis-server`, with no skips) and
  `go mod verify`.
- Hosted Turso was not run; its adapter has no cache-runtime change.

Checksums and provenance: [release manifest](plans/tuplecache-key-on-binding-release-manifest.json).
Design, review findings F1–F12 and rulings: [the plan](plans/tuplecache-key-on-binding.md).
Host adoption is covered by the v0.26.0 repin handoffs, which span this release.

## Invitation terminal settlement (published 2026-10-13)

Coordinated MINOR releases: authorization `v0.24.0`, its PostgreSQL adapter
`v0.18.0` and Turso adapter `v0.17.0`; authentication `v0.18.0`, its PostgreSQL
adapter `v0.8.0` and Turso adapter `v0.7.0`. No `sdk` change; no firestore
tag (firestore hosts stay on authentication ≤ v0.17.x).

Authorization gains a durable **fence** over the operation ledger (`OperationLedger`
port: `Fence`, `Recorded`; `OutcomeFenced`) so an accepting invitation claim can be
cancelled with the first grant permanently blocked, or reported as already
committed. Every SQL host using `Repositories` must apply **`0004_iam_operations_fenced.sql`**
before the new binaries start; the probe refuses the old schema and old binaries
refuse the new one. Apply 0004 in its own migrate run with a lock timeout; on Turso
the rebuild silently drops host triggers/indexes on `iam_operations` (save and
recreate them; a host view fails loudly). Phase 1 (no Fencer wired) remains
reversible; the first fenced row is the one-way door. See `stores/UPGRADE.md`.

Authentication adds the `Fencer` seam, `Cancel` on `accepting` claims
(`ErrAlreadyGranted` when the grant already committed), `ErrGrantFenced`, and
`ListActiveByResource` (active statuses only, server-side, partial-index proven).
No authentication migration. `InvitationRepository` gains two methods, which
breaks third-party implementers at compile time.

Core tags are annotated at `b7c8a73a28bc581a473fcdab783134b0ef08e8b0` (PRs #71, #72); adapter tags at `8c9b56e77813ed6fe4d6dbca2506391093beb1f6`
(PR #74), pinning the public cores. Both commits passed full `make check` and CI.
Each proxy `.info` resolved before dependent pins moved. Using `GOWORK=off`, empty
caches, `proxy.golang.org` and `sum.golang.org`, every archive passed standalone
build, uncached tests, vet and `go mod verify` (Turso integration-tag vet included).

Verification before release: isolated local PostgreSQL 17 (default + schema legs),
local SQLite, and an isolated local HTTP libSQL server (authorization integration
suite 806/806; authentication conformance 280/280), plus real-HTTP settlement drives
in `examples/auth-cms` (PR #73). **Hosted Turso was not run: the playground
database's group no longer exists.** The hosted sqld transaction bound for 0004 on
large ledgers is unverified; Turso hosts should dry-run 0004 on a branched copy.

Checksums and provenance: [release manifest](plans/invitation-terminal-settlement-release-manifest.json).
Exact verification and Segovia adoption: [the plan](plans/invitation-terminal-settlement.md).

## StaticFileServer for an SPA beside an API (sdk v0.12.0 — published 2026-10-12)

SDK `v0.12.0` adds two `StaticFileServer` options and one SPA-mode default.
`WithIndex(bytes)` serves a boot-rewritten `index.html` through
`http.ServeContent`, keeping `Content-Length`, `HEAD` and ranges; the fs need
not contain `index.html`, the bytes are copied, and an empty slice panics.
`WithReservedPrefixes` answers a `no-store` plain-text 404 at or under the given
rooted prefixes before the filesystem is consulted, in every mode; entries are
cleaned, and an empty, unrooted or `/` entry panics. Last call wins.

**Behavior change for SPA-mode hosts.** A missing path or directory under the
asset prefix (default `assets/`, applied even when `WithAssetPrefix` was never
called) is now a `no-store` 404 instead of `index.html`, so a browser holding a
stale hashed chunk gets a failure the app can reload from. The zone has
directory semantics (`static` does not catch `/statistics`); the immutable
caching match is unchanged. A client route under the asset prefix loses
deep-link refresh: rename the bundler's asset dir and pass a matching
`WithAssetPrefix`. `WithAssetPrefix("")` opts out and drops immutable caching
with it. Both new 404s are `no-store` because during a rolling deploy the path
may exist on the next instance and a cached 404 would outlive the rollout. The
SPA owns recovery (Vite `vite:preloadError` → reload). No in-repo consumer uses
SPA mode; `ui/goth`, examples, pockets, integrations and scaffold pins are
unchanged.

Annotated tag `sdk/v0.12.0` on merge commit `93c38a35a98feca94e4c640eac8fa4856a4b41a3` (PR #69). `make check`
passed before the PR; CI passed on the PR. The proxy `.info` resolved before
any tidy. Using `GOWORK=off`, an empty module cache, `proxy.golang.org` and
`sum.golang.org`, a standalone consumer built, vetted, passed `go mod verify`
and saw the index bytes, the SPA fallback, the asset-zone 404 and the reserved
404 behave as documented. No replacements were used.

Downstream: three-sixty repins sdk v0.9.0 → v0.12.0 (crossing additive v0.10.0
and v0.11.0) and retires its `ui/spa` overlay and stat branches; Segovia v2 7.2
uses `WithSPAMode` with the stdlib `GET /api/{path...}` JSON-404 recipe.

Plan: [static-spa-beside-api](plans/static-spa-beside-api.md).

## Email unsubscribe headers (sdk v0.11.0; sendgrid v0.4.0 — published 2026-10-09)

SDK `v0.11.0` adds `email.Unsubscribe` on `Message` and `SendRequest`: the SMTP
sender writes RFC 2369 `List-Unsubscribe` and, with OneClick, RFC 8058
`List-Unsubscribe-Post`; the console sender logs both. The zero value changes
nothing. Validation refuses non-https or non-ASCII URLs, URLs over 900 bytes,
header-breaking characters, and OneClick without a URL or with more than one
recipient. A host-owned `email.Sender` must deliver the headers or refuse a
non-zero Unsubscribe with `sdk.ErrInvalidInput`, never drop it. SMTP relays must
DKIM-sign both headers.

SendGrid `v0.4.0` maps the field to the v3 `headers` object and requires SDK
`v0.11.0`. Keep SendGrid Subscription Tracking off for mail that sets it.
Pockets, examples and scaffold pins are unchanged.

Both are annotated tags on merge commits: `sdk/v0.11.0` at
`7cc785eae6193aa649d1d2584e78c97abe821b56` (PR #65) and
`integrations/email/sendgrid/v0.4.0` at
`0d0b6f4d7b7265fe712b8a77d41635bec28bb8c8` (PR #66, pinning public SDK v0.11.0).
`make check` passed before PR #65; CI passed on both PRs. Both proxy `.info`
records resolved before the SendGrid pin moved. Using `GOWORK=off`, an empty
module/build cache, `proxy.golang.org` and `sum.golang.org`, a standalone consumer
built, vetted and passed `go mod verify`, and saw shared one-click refused with
`sdk.ErrInvalidInput`. The public SendGrid archive matched its tag source and passed
uncached tests and vet. The public SDK `notify/email` package matched its source
and passed uncached tests and vet. No replacements were used.

The live provider check is still open: no real SendGrid message has been sent to
confirm the client's Unsubscribe button, a single `List-Unsubscribe`, and DKIM
coverage of both headers.

Plan: [email-list-unsubscribe](plans/email-list-unsubscribe.md).

## Invitation grant operation ledger (published 2026-10-06)

Coordinated MINOR releases: authorization `v0.23.0`, PostgreSQL adapter
`v0.17.0`, Turso adapter `v0.16.0`, and authentication `v0.17.0`. The ledger is
opt-in per mutation command; every SQL host using `Repositories` must nevertheless
apply the additive `0003_iam_operations.sql` before upgrading. Preserve operation
records across teardown and any restore that preserves retriable invitation claims.
Authentication has no migration: superseded grants finalize acceptance with a
conflict response, a blocked/superseded security event and no member-added notice.

Core and authentication are annotated tags at
`ee4be7e62f5fde5428085ceaac9954a4a5c1d465` (PRs #58 and #59). Adapter tags are at
`bb46a7d0c390fd496e3f0de026ecab9610443894` (PR #61), pinning public core v0.23.0.
Both exact tag commits passed the full 42-module `make check` and main CI
([core/auth](https://github.com/gopernicus/gopernicus/actions/runs/37508561886),
[adapters](https://github.com/gopernicus/gopernicus/actions/runs/37509878732)).
All four proxy `.info` records resolved before dependent pins were updated.

Using `GOWORK=off`, an initially empty module/build cache, `proxy.golang.org` and
`sum.golang.org`, all four public archives matched their tag source byte for byte
and passed standalone build, uncached tests, vet and `go mod verify`; Turso
integration-tag vet also passed. No replacements or checksum exemptions were used.
All 501 prior remote tag refs are unchanged. A standalone public consumer also passed
race tests, build, vet and checksum verification, exercising memory and SQLite
revocation/replay plus fresh-handle restart.

The reference host adoption is PR #62. Standalone public-module build, uncached
full tests and vet passed, including its real TCP completion-fault → revoke →
409 recovery with accepted list state and absent access. Memory, isolated local
PostgreSQL, SQLite and HTTP libSQL behavior passed before release. Hosted Turso,
visual browser automation and production host migration remain unverified.

Checksums and provenance: [release manifest](plans/invitation-grant-operation-ledger-release-manifest.json).
Exact verification and Segovia adoption instructions: [the plan](plans/invitation-grant-operation-ledger.md#release-record--2026-10-06).

## Namespace origin policy (sdk v0.10.0; authentication v0.16.0 — published 2026-09-28)

SDK `v0.10.0` adds `web.OriginPolicy`: immutable exact origins and one-label
namespace rules with exhaustive port lists and optional reserved labels. Share
one policy between `web.CORSConfig.OriginPolicy` and authentication's
`BrowserConfig.OriginPolicy`. CORS policy matches echo the request Origin with
credentials and take precedence over `*`. Authentication admits the exact list
OR the policy at its mutation, credential-establishment and HTML form gates.
Existing behavior is preserved when the policy is absent; production
authentication rejects HTTP namespaces.

Every admitted subdomain has the trust of an exact origin. The host must control
the suffix, audit subdomain takeovers, keep session cookies host-only, and still
resolve tenants and authorize requests separately. Public-suffix ownership is
not checked. The parser rejects bracketed DNS/IPv4 and malformed IPv6 hosts;
valid IPv6 exact origins remain supported.

SDK published 2026-09-28 from PR #55's main merge commit
`fb2e8cd0e3b8b8dc4ecc28ae8c2acb53d603d60d` as annotated tag `sdk/v0.10.0`.
Both PR CI runs and `make check` on the merge commit passed. A scratch consumer
with `GOWORK=off` and an empty module cache downloaded through
`proxy.golang.org` and `sum.golang.org`; consumer build/runtime tests, all
published SDK tests and `go mod verify` passed.

Authentication `v0.16.0` published from PR #56's main merge commit
`d5c962d6e67ef53fbd7f471d7914a5fffb634815` as annotated tag
`pockets/authentication/v0.16.0`. It pins SDK `v0.10.0`; no store, schema,
example or downstream version is changed. Standalone build, uncached tests and
vet passed with `GOWORK=off`, as did both final PR CI runs and `make check` on
the merge commit. A second scratch consumer with an empty module cache resolved
both public modules through the normal proxy/checksum database; its build/API
tests, all published authentication tests and `go mod verify` passed.
The regression suite covers constructors, malformed origins, credentialed CORS
preflight, mounted HTML login and a TLS cookie-jar flow through login, CSRF
bootstrap, password change and missing-token rejection. Browser automation and
live datastore tests were not run for this change.

Plan: [origin-namespace-policy](plans/origin-namespace-policy.md).
Checksums and provenance: [release manifest](plans/origin-namespace-policy-release-manifest.json).

## Invitation resource rule (authentication v0.15.0)

Authentication `v0.15.0` (MINOR, additive): who may administer invitations is now
a property of the RESOURCE. `InvitationsConfig` gains `ResourcePermissions`
(resource type → permission) and `Can` (the host's authorizer adapter,
`authenticationhttp.InvitationCan`); direct HTTP adapters use
`WithInvitationResourceRule`. With the rule on, any holder of the mapped
permission on the resource may create, list, resend and cancel its invitations,
whoever issued them. Unmapped types are refused without calling `Can`; create/list
denials are 403, resend/cancel denials are 404 (indistinguishable from an unknown
id); `Can` errors fail closed through their own mapping. `InviteCheck` becomes the
optional create-only refinement, run after `Can` allows.

**Rule-only authority is broad:** without `InviteCheck`, the mapped permission
authorizes invitations for every relation the `Granter` accepts, including
`owner`, with any domain-valid metadata. Hosts whose managers may not invite
owners (or whose metadata/invitees are restricted) keep `InviteCheck`.

Construction: a half-wired or invalid rule is `ErrInvitationResourceRuleIncomplete`
(checked first); a rule without a `Granter` is
`ErrInvitationResourceRuleWithoutGranter`; `ErrInviteCheckRequired` keeps its name
with a reworded message. `authenticationhttp.New` mirrors the precedence against
service presence and wraps `sdk.ErrInvalidInput`.

Audit: cancel/resend populate the canonical `SecurityEvent.Actor` via the new
`PreparedManagement.WithActor`, keep `UserID` user-only, and add
`Details["invited_by"]`. Under rule OFF, `UserID` is unchanged and `Actor` is now
populated. Create, grant and decline attribution are unchanged.

Host adoption: none required — rule OFF (`InviteCheck` only) behaves exactly as
before. Stores untouched, no migration. Guard G28 now also rejects the rule's
names under authentication logic. Plan: [plans/invitation-resource-rule.md](plans/invitation-resource-rule.md).

Published 2026-09-27 from main merge commit
`ab7df2cc593f1289c6b2314e68ca560b852ba3e2` (PR #52) as annotated tag
`pockets/authentication/v0.15.0`. The `check` gate passed on the PR and on main
([36339785942](https://github.com/gopernicus/gopernicus/actions/runs/36339785942)).
Before merge: module build/vet/test, `examples/auth-cms`, `make guard`, mutation
checks, and a real-TCP two-manager drive (B resends/cancels A's invitation; A
after revocation → 404/404/403, row unchanged; audit `actor=user:u2`,
`invited_by=u1`). Cold-verified from a scratch consumer with `GOWORK=off` and an
empty module cache against `proxy.golang.org` and `sum.golang.org`: the proxy
`.info` resolved on the first poll, the consumer compiled against
`InvitationsConfig.ResourcePermissions`/`Can`, `WithInvitationResourceRule`,
`PreparedManagement.WithActor` and both new sentinels, and `go mod verify` passed.
`h1:srmQeJH/R0r+uerUK4A+HMchFDGSA7c2yruvie59yO0=` (module),
`h1:V4s8PuUv/BPWtuLigf9aVCYEcSA+tklqVUnMnTkxvg4=` (`go.mod`). Stores and schema
unchanged. Segovia repin (from v0.13.2, which also brings v0.14.0) is downstream.

## Browser session recovery after access expiry (published 2026-09-22)

Authentication `v0.14.0` plus Goth views `v0.6.0`: a protected HTML GET/HEAD with a missing or
expired access cookie can restore its still-live refresh session at the login
landing, then return to the requested page. Upgrade both modules and use the
Goth view's `HTMLPolicy()`; no new asset mount, schema change, or poller is needed.

Eligible `Browser()` redirects now include `recover=1`. `LoginPage` gains the
optional `SessionRecovery` model; existing custom views can ignore it. API
denials, authoritative bearers, nested/liveness denials and submitted forms
retain their behavior. The script coordinates same-origin tabs with Web Locks,
checks fresh cookies before rotating, and bounds redirect loops. Normal sign-in
remains available if recovery cannot run or fails. Other refresh clients on
the origin must use the same lock/check protocol. See the authentication
README's browser recovery section for prefix, CSP and custom-view requirements.

Custom templ login pages retain their own forms and layout and include
`@authgoth.SessionRecovery(m.SessionRecovery, m.CSPNonce)` once after sign-in
controls. The public component owns its status markup and script; no Goth DOM
hooks or new asset route are required. Pass the handler's model and nonce
unchanged and allow the nonce plus `connect-src 'self'` in custom CSP. Recovery
requires JavaScript, Web Locks in a secure context, and session storage.

Goth pins core `v0.14.0`. These coordinated additive host-contract changes use
minor versions; stores and schema are unchanged. Published from
`7d358447428dd46a0af527cff689ba82ec549955` through an atomic push of main, the
release branch and both annotated tags. All 491 prior remote tag refs are unchanged.

Verified: the 42-module repository gate; independent build, uncached race tests
and vet of both modules; all 391 public archive entries and checksums match the
private candidates. A fresh external consumer using public proxy/sumdb modules,
local SQLite storage and real authentication handlers passed custom-page recovery
in Chromium, Firefox and WebKit, including three tabs with exactly one rotation.
No public-proxy retries, replacements or checksum exemptions were needed.
All four release CI runs passed on Linux ([main check](https://github.com/gopernicus/gopernicus/actions/runs/35804533873)).
Checksums and provenance are recorded in the
[release manifest](plans/authentication-browser-session-recovery-release-manifest.json).
GPS adoption and deployment remain separate work. Live PostgreSQL, remote Turso
and Firestore were not exercised; no persistence code changed.

## Redis connector — URL, host and port, ACL username (published 2026-09-21)

**BREAKING for every host of `integrations/kvstores/goredis`** (`v0.3.0`):
`Config.Addr` / `REDIS_ADDR` is REMOVED. There is no alias.

- `Config.Host` (`REDIS_HOST`, default `localhost`) and `Config.Port`
  (`REDIS_PORT`, default `0`). Port set: the address is `Host:Port`, and a Host
  that already carries a port is an `ErrInvalidInput` naming both keys. Port
  unset: Host is read as written, so `REDIS_HOST=cache.internal:6380` works, and
  a Host with no port at all gets `6379`.
- `Config.URL` (`REDIS_URL`): a `redis://`, `rediss://` or `unix://` URL read by
  go-redis's `ParseURL`. **The URL wins**: it supplies the address, the ACL user,
  the password, the database and TLS, and `Host`, `Port`, `Username`,
  `Password`, `DB` and `TLSEnabled` are not read. The retry, timeout and pool
  fields fill whatever the URL leaves unsaid; `ContextTimeoutEnabled` is always
  set. A rejected URL is reported by its reason alone — `url.Parse` quotes its
  whole input, and the input holds a password.
- `Config.Username` (`REDIS_USERNAME`, default empty), passed to go-redis
  unchanged. `Open` previously sent a password-only AUTH, which Redis and Valkey
  read as the `default` ACL user; a managed service whose credential belongs to
  any other user answered `WRONGPASS invalid username-password pair or user is
  disabled` at the boot ping, and no `ClientOption` reaches the go-redis options
  to work around it. Found on gps-360-go's first production boot against a
  managed Valkey cluster.

Host adoption: rename `<NAMESPACE>_REDIS_ADDR` to `<NAMESPACE>_REDIS_HOST` (a
`host:port` value keeps working as written) or replace the connection keys with
one `<NAMESPACE>_REDIS_URL`; rename `Config{Addr: …}` literals to `Host`; and a
host that logged `cfg.Addr` logs `rdb.Options().Addr` instead, since with a URL
the Host field is not where the client connected. No other module in this
repository pins the connector, and every module that takes the resulting
`*redis.Client` is unaffected.

Verified: `TestHostAndPortResolveToOneAddress`, `TestURLWinsOverTheSeparateFields`
and `TestAURLFaultNeverRepeatsTheURL` pin the resolution without a server;
`TestLive_OpenAuthenticatesAsTheNamedUser` (needs `REDIS_TEST_ADDR`) creates an
ACL user on a stock `redis:7`, opens as it by field and by URL, and reads `ACL
WHOAMI` back. The whole live suite passes against the same server. NOT verified
here: a managed Valkey, `rediss://` against a real TLS endpoint, or the
WRONGPASS itself, which needs the `default` user locked and would break the
package's other live tests on a shared server.

Published from `b2ef629806d091051d0083b687d594b2dd8fbebb`; the GitHub `check` run passed on
Linux ([35649270420](https://github.com/gopernicus/gopernicus/actions/runs/35649270420)).
Cold-verified from a scratch consumer with `GOWORK=off` and an empty module
cache against `proxy.golang.org` and `sum.golang.org`: the consumer compiled
against `URL`, `Host`, `Port` and `Username`, and `go mod verify` passed.
`h1:ch+zTHsBSGDXIGvXd9s8i7VokgBtSCklQwfg64SKnCY=` (module),
`h1:FgppPk3eiVREbRaeKVApcd9VNJO/0L8NjwfQqmGdj+Q=` (`go.mod`).

## Linux filestorage fix, authorization outcome cleanup and docs pass (2026-09-16)

SDK patch `v0.9.1`: `filestorage.Disk.DownloadRange` clamps its seek offset to the
current file size. Seeking to `MaxInt64` succeeds on macOS but fails with
`EINVAL` on Linux, which made `TestDisk_Conformance/RangeEdges` fail on every
GitHub Linux run since the coordinated 2026-09-11 release. The contract already
says offsets at or beyond EOF read as empty. Verified in a `golang:1.26` Linux
container: the test fails before the change and passes after it.

Authorization `v0.22.0`, PostgreSQL store `v0.16.0` and Turso store `v0.15.0`
remove the unreachable `mutations.OutcomeInvariantBlocked` and
`Outcome.Rejection`. `Plan` reports integrity refusals as the `ErrInvariantBlocked`
error, never as an outcome; the stores drop their dead `Rejection` calls. No
schema, cursor/cache protocol, configuration or dependency change. Adapter and
auth-cms pins stay at `v0.21.0`, which remains a compatible minimum. See
[API adoption](pockets/authorization/stores/UPGRADE.md#adopting-the-outcome-cleanup-v0220).

The same commit carries a documentation pass: the Docusaurus authorization page
is rewritten around the authorization-versus-integrity boundary, the tuple
authority, audit and the TupleCache outbox, and nineteen site pages plus the
root, authorization and Firestore connector READMEs lose stale claims (module
count, release line, removed `web.Decode`, `authpgx.Repositories` context
parameter, Turso local-file profile, Firestore store coverage, the unshipped
`gopernicus guard` command).

Published from `b0249fa08573cf5a9a4a4f4180c348e707d6787a`; the GitHub `check`
run passed on Linux and the docs deploy succeeded. Cold-verified from a scratch
consumer with `GOWORK=off` and an empty module cache against `sum.golang.org`.
Checksums and run links are in [plans/docs-pass-2026-09.md](plans/docs-pass-2026-09.md).

## Authorization internal cleanup (published 2026-09-16)

Authorization core and PostgreSQL/Turso adapters consolidate raw relationship
reads over canonical tuples, reject invalid selectors/unsupported search and
honor cancellation consistently. Standalone raw listings/counts now use snapshots;
raw ambient transaction joining and decision isolation requirements are preserved.
The obsolete decision-service validation trio, CreateRelationship alias and unused
relationship error are removed. This pre-v1 source/accepted-input change is
published as authorization `v0.21.0`, PostgreSQL store `v0.15.0` and Turso store
`v0.14.0` from `47ebb259d7eff87cad782ef2d72957b54d3d52ac`.
SQL adapter and auth-cms pins are updated. There is no schema, cursor/cache
protocol or new third-party dependency change.

See [API adoption](pockets/authorization/stores/UPGRADE.md#adopting-the-internal-cleanup-v0210)
and [implementation/verification](plans/authorization-internal-cleanup.md).

Upgrade the core and selected SQL adapter together; Redis remains at `v0.4.0`.
All 312 public archive entries match verified candidates. Public checksums,
Git provenance, independent build/test/vet, consumer/auth-cms races and real
SQLite/Redis compatibility passed, as did the local 42-module make check.
See the [release record](plans/authorization-internal-cleanup-release.md) and
[checksum manifest](plans/authorization-internal-cleanup-release-manifest.json).

GitHub [main CI](https://github.com/gopernicus/gopernicus/actions/runs/35152994183)
and all three tag checks reproduced the unchanged Linux SDK RangeEdges failure;
the remote workspace gate did not complete. Remote Turso remains unverified.

## Inbound authorization ownership and integrity (published 2026-09-16)

Coordinated breaking pre-v1 release: authentication `v0.12.0`, authorization
`v0.20.0`, PostgreSQL authorization store `v0.14.0`, and Turso authorization store
`v0.13.0`. All four annotated tags identify main commit
`f989b89567504273099e07ae885a07db9d54184e`. Unchanged Redis,
authentication stores/views, SDK and connectors retain their existing versions.
Upgrade the authorization core and its PostgreSQL/Turso adapter together. Hosts
relying on integrity enforcement for every writer must drain or replace all older
writing instances: the unchanged schema does not fence old binaries, whose raw
writers can bypass the new minima. Configure the same policy on every new writer.

Authentication host-policy callbacks move to `authentication/inbound/http`.
`InviteCheck`, `UserAdminCheck` and their request/action types belong there;
root configuration forwards them to HTTP, not domain services. Custom transports
invoke their own policies. Remove service policy options and calls to
`CreateAuthorized`, `ListByResourceAuthorized` and `AuthorizeUserAdmin`.

For invitations that need normalized invitee context, call `PrepareCreate`,
authorize its inspected command in inbound, then execute the same opaque value
with `CreatePrepared`. Plain `Create` remains policy-free. Prepared values are
short-lived domain preparation, not durable authorization receipts. The bundled
HTTP adapter retains fail-closed policy wiring and preserves no-write-on-denial.
User administration still checks policy before target lookup. Identity proofs,
token/recipient proof and business invariants remain in services.

The auth-cms document example now owns permission evaluation, bypass selection
and encrypted cursors at inbound. Domain reads take explicit restrictions;
storage enforces them before paging. Complete-set, candidate and same-statement
SQL strategies remain supported.

The owner subsequently settled concurrency: all principal authorization is inbound.
`MutationGuard`, `WithGuard`, transactional `DecisionView`, `ApplyGuarded`, actor
parameters on tuple commands and `SystemMutator` are removed. Construct one
`mutations.Service` with the repository and optional compiled model/limits.
`Components.Mutations` is now principal-free; inbound supplies audit attribution.
Bundled role routes require `RoleRoutes.WritePolicy` for validated assign and
unassign commands; `Gate` alone does not enable writes.

`GuardianPolicy`/`GuardianRule`/`MinAnchors` become
`IntegrityPolicy`/`IntegrityRule`/`MinSubjects`, configured through store
`WithIntegrityPolicy`. Logic defines the data rule and stores enforce it inside
the write boundary, including raw tuple/role/relationship facades that previously
bypassed minima. Explicit reason-bearing resource teardown remains the exception.
Integrity refusal remains a conflict (409), separate from principal denial.
Atomic commands reject ambient transactions with `ErrMutationInsideTransaction`;
raw writes still join ambient transactions and roll back failures to a savepoint.

Invitation management now uses `PrepareManagement`, an inbound issuer check, and
`Cancel`/`Resend` over that opaque prepared value. Services preserve token/status
consistency and recipient proof, without checking the caller's permission.

An admitted operation may finish after permission revocation. Integrity always
uses the write's serialized state. Upgrade core and adapters together; the API
breaks are intentional. No schema or cache protocol change. SQL adapter and
example pins are updated for this release. Implementation verification is recorded
in AUDIT-047 and the integrity-policy execution record;
AUDIT-046 preserves the earlier checkpoint and its then-unsettled exception.
The [API adoption map](pockets/authorization/stores/UPGRADE.md#adopting-the-inbound-and-integrity-api)
lists removed names, current replacements and host verification cases.

All 637 public archive entries match the verified candidates, with normal Go
checksum and Git-origin verification. Independent versioned build/test/vet,
standalone consumer and auth-CMS races, SQLite/Redis integration races, and the
final local 42-module make check passed. Unchanged authentication adapters/views
and Redis passed compatibility checks. See the
[release record](plans/authorization-integrity-release.md) and
[checksum manifest](plans/authorization-integrity-release-manifest.json).

GitHub [main CI](https://github.com/gopernicus/gopernicus/actions/runs/35146615264)
and all four tag checks failed the existing Linux SDK RangeEdges test; SDK is
unchanged. The remote workspace gate did not complete. Docs deployment passed.
Remote Turso and live authentication datastore suites remain unverified.

## Host denial responses and decision logging (v0.19.0, published 2026-09-16)

Authorization `Require` accepts optional per-policy `WithDeniedHandler` to let
hosts render a denial as 404 or their normal JSON/HTML response. Its default
remains 403; authentication and evaluation errors keep their existing mapping.
Ordinary one-argument calls remain source-compatible; code assigning the method
to an exact non-variadic function type must update that type.

The existing root `WithLogger` now also supplies DEBUG decision-operation records;
standalone services accept `decisions.WithLogger`. Host slog handlers control
enablement, formatting and redaction. The decision-service interface, SQL schema,
cache protocol and module dependencies are unchanged. Core `v0.19.0` is selected
for the pre-v1 method-signature change; current SQL/Redis adapters remain compatible.
Published from main `68866b39e3d68830dd97d4e1d4232ef390d0d23c` as annotated tag
`pockets/authorization/v0.19.0`. Public checksums and all 227 archive entries match
the candidate. Independent build/test/vet/race and a real HTTP consumer passed.
GitHub main/tag CI both hit the existing Linux SDK `RangeEdges` failure; SDK is
unchanged. See [AUDIT-045](AUDIT.md#audit-045-host-denial-responses-and-decision-logging),
the [release record](plans/authorization-host-policy-release.md) and
[checksum manifest](plans/authorization-host-policy-release-manifest.json).

## Unified authorization tuples (published 2026-09-16)

Published from main `66d70c511a74b28b1019e526acfc1382044395ad` in one coordinated train:
PostgreSQL connector `v0.9.0`, authorization core `v0.18.0`, PostgreSQL store
`v0.13.0`, Turso store `v0.12.0`, and Redis store `v0.4.0`. All five annotated tags
point to this commit; public module archives match the verified candidates.
See the [release record](plans/authorization-unified-release.md) and
[checksum manifest](plans/authorization-unified-release-manifest.json).

Independent versioned build/test/vet, public checksum verification, HTTP consumer,
SQLite/Redis race tests and the local 42-module gate passed. GitHub Linux CI hit
the existing SDK RangeEdges failure (SDK unchanged); remote Turso remains unverified.

The canonical authority is `iam_tuples`; roles and relationships address the same
facts, independent labels coexist, global membership is exact and global policy
is explicit. The one `decisions.Model` replaces split declarations at compile time.
The authorization schema is a fresh base `0001_iam_tuples.sql` containing tuples
and audit, plus optional `tuple_cache_migrations/0002_iam_tuple_cache.sql`.
There is no legacy conversion/downgrade stream. Both fresh installation
and cache installation over populated canonical facts passed release verification.
See [AUDIT-043](AUDIT.md#audit-043-authorization-cleanup-and-fresh-sql-schema)
and the [schema setup guide](pockets/authorization/stores/UPGRADE.md).

Authorization HTTP uses only `Adapter.Require` with composable predicates.
Remove calls to the former permission middleware and `NewGates`; use the
two-method decision contract for custom HTTP adapters. Compound policies share
one snapshot and budget. See [AUDIT-044](AUDIT.md#audit-044-one-authorization-middleware-api).

The raw tuple continuation is now `Query.After *Tuple`, ordered by
`tuples.Compare`. `Repositories.Relationships` is removed; the root derives both
fact facades from `Repositories.Tuples`. `GetPermissionsForRelation`,
`LookupSnapshotter`/`ReadLookupSnapshot` and the unused
`roles.ErrInvalidRoleAssignment` sentinel are retired. Custom stores provide
canonical tuple snapshots, retaining optimized graph reads as optional capabilities.

The core also adds composable HTTP `Require` guards with `All`/`Any`, exact
`HasRole`, userset-aware `HasRelationship` and named `Can` predicates. Mixed
resources share one decision operation and lazy input resolution. The middleware
consolidation requires no further migration or cache protocol change; immediate
roles-service APIs retain their contracts.

The authorization Firestore module is removed from the workspace and CI matrix.
Do not publish another authorization Firestore tag. The shared Firestore connector
and authentication Firestore adapter remain. Published historical versions are
not retracted, and no application data is changed by repository removal.

Release validation includes the full 42-module make check, core/adapter race
suites, non-C PostgreSQL and named-schema checks, fresh base/cache installation
proofs, actual SQLite/PostgreSQL-to-Redis integration and measured benchmarks.
Remote Turso conformance requires an independently pinned
`AUTHORIZATION_TURSO_DISPOSABLE_URL` matching its supplied URL before any delete.
A missing fixture is an explicit skip, never evidence of live verification.

## Authorization consistency and TupleCache hardening (published 2026-09-15)

Published from main `373ef0668dfdf17a56ba002bb344197d6779e374`: authorization core
`v0.17.0`, Turso store `v0.11.0`, PostgreSQL
store `v0.12.0`, go-redis adapter `v0.3.0`. Ordinary relationship Check, Explain,
Batch and Filter operations now use one durable snapshot in supported readers.
TupleCache adds `Rebuild(ctx)`, capacity errors, and delivery diagnostics; SQL
full snapshots capture current facts plus exact event IDs without decoding
obsolete event payloads.

Upgrade core and the configured SQL store together. Cached hosts also upgrade
Redis and set `redis.Options.ContextTimeoutEnabled=true` before construction.
Hosts using `integrations/kvstores/goredis.Open` already receive that setting.
Redis now defaults to 1 MiB per raw read and 4 MiB per delta budget. Oversized
reads fall back to SQL; oversized accumulated deltas may recover with an explicit
rebuild when current facts fit. No schema migration, Redis key change or mandatory
cache rebuild is required. Existing source consumers must tolerate ID-only
Changes on Full snapshots. Relation exclusivity remains unchanged.

All four public archives match the verified candidates and passed normal Go
checksum/source verification, independent build/test/vet and tagged compilation.
A standalone public SQLite/Redis consumer and the published integration race test
passed, as did local full-workspace and implementation SQL/Redis race checks.
See the [release plan](plans/authorization-review-hardening-release.md) and
[manifest](plans/authorization-review-hardening-release-manifest.json).
See [AUDIT-039](AUDIT.md#audit-039-authorization-consistency-and-tuplecache-hardening)
and the [benchmark report](pockets/authorization/BENCHMARKS.md). Remote Turso,
Firestore, non-C PostgreSQL locale and production-scale load remain unverified.
GitHub Linux CI still fails the unchanged SDK RangeEdges test; the release
record links its failing run and evidence. No SDK changes are included.

## Authorization consistent lookup reads (published 2026-09-15)

Published from main `bd534a495010abe8720febffb8d2caf1b0497577`: authorization core
`v0.16.0`, Turso store `v0.10.0`, PostgreSQL store `v0.11.0`. Both SQL stores pin the new core. Lookup candidate discovery,
verification batches and page lookahead now share one durable read snapshot.
The pocket retries discovered-grant mismatches twice; persistent contention
returns `model.ErrEnumerationContended` / `sdk.ErrUnavailable` (HTTP 503).

Upgrade core and the configured SQL adapter together. No migration, cache rebuild
or new configuration is required. Existing ambient transactions retain their
pending writes and isolation; separate page calls do not share a snapshot.
See [release plan](plans/authorization-lookup-snapshots-release.md),
[implementation verification](plans/authorization-lookup-snapshots.md) and
[AUDIT-038](AUDIT.md#audit-038-consistent-authorization-lookup-reads).

All three public archives match the verified candidates and passed normal checksum
verification and independent versioned build/test/vet. The published consumer
passed SQLite/PostgreSQL concurrent lookup and existing SQLite/Redis cache checks.
Local full-workspace and race checks passed. GitHub Linux CI still fails the
unchanged SDK file-storage RangeEdges case disclosed in the preceding release.
See the [release manifest](plans/authorization-lookup-snapshots-release-manifest.json).

## Authorization cached filters and readable Redis keys (published 2026-09-15)

Published from main `26c7ff55fc728cac88724309028f11d99c953db9`: authorization core
`v0.15.1` and go-redis adapter `v0.2.0`.
Relationship-owned `Decisions.FilterAuthorized` now uses TupleCache through
`CheckBatch`, including whole-operation durable fallback. The Redis adapter pins
the fixed core and uses `tuplecache:{<namespace>}` with the namespace verbatim.
Accepted characters are ASCII letters, digits and `:._-/`.

Segovia can retain Turso store `v0.9.0`. No SQL migration is needed. Restart the
single dev server after updating both modules; its relay reconstructs the new
hash automatically. Old base64-named hashes are left untouched. Multiple readers
and relays sharing a source should switch together to avoid maintaining two mirrors.
See [release plan](plans/authorization-filter-tuple-cache-release.md),
[implementation verification](plans/authorization-filter-tuple-cache.md) and
[AUDIT-037](AUDIT.md#audit-037-raw-authorization-tuplecache-replaces-generation-caching).

Both published archives passed normal public checksum/source verification and
isolated versioned build/test/vet. The published SQLite/Redis consumer passed
warm filtering with zero SQL, revocation and mirror recovery using Turso v0.9.0.
Local full-workspace and relevant race checks passed. GitHub Linux CI still fails
the unchanged SDK file-storage `RangeEdges` case disclosed by the previous release.
See the [release manifest](plans/authorization-filter-tuple-cache-release-manifest.json).

## Authorization TupleCache and Through batching (published 2026-09-15)

Published from main `83d48957714e53e6c1dbcc1d2a62ef4650c448e8`: authorization core `v0.15.0`, Turso store `v0.9.0`, PostgreSQL
store `v0.10.0`, Firestore store `v0.3.0`, new go-redis store `v0.1.0`.
The raw TupleCache replaces the previous generation cache; stores pin the new core.
Turso uses already published connector `v0.6.0`. The same core release includes
set-based Through verification, preserving existing evaluation limits and results.

This is a breaking pre-v1 cache API and optional SQL migration upgrade. Hosts stop
old cache-enabled binaries before optional `authorization-cache` migration 0002,
use a fresh Redis namespace, wire the new runtime and retain a shared explicit
freshness bound. The configured Turso or PostgreSQL store remains authoritative.
Firestore retires old cache APIs and remains durable-only. There is no DecisionCache.
See [AUDIT-037](AUDIT.md#audit-037-raw-authorization-tuplecache-replaces-generation-caching),
the [release plan](plans/authorization-tuple-cache-release.md) and
[Segovia handoff prompt](plans/segovia-tuple-cache-adoption-prompt.md).

All five annotated tags passed normal public checksum/source verification and
isolated versioned build/test/vet. A published consumer passed actual SQLite/Redis
delivery and recovery checks with GOWORK=off and no replacements. Local full
repository and race checks passed. GitHub Linux CI fails an unchanged SDK
file-storage extreme-range test; that failure is disclosed in the release plan.
Remote Turso routing, live Firestore and representative application capacity
remain unverified. No host deployment is part of this release.

## Turso local file profile (published 2026-09-15)

Published `integrations/datastores/turso` `v0.6.0` from main commit
`943b8bf989aa1d63e61e8092f7ed12b669e452a5` (PR #51): additive `Config` env tags
(`DB_*`) and `BusyTimeout`, the local file profile in `Open` for `file:` URLs, and
the new `turso/localfile` driver package. No other module changed; existing tags are
unchanged. Candidate verification with the workspace off, every Turso-backed store
suite and the repository guards passed before tagging; standard public
checksum/origin verification and an isolated versioned consumer passed after
tagging, with no bypass. See the [release plan](plans/turso-local-file-profile-release.md),
[manifest](plans/turso-local-file-profile-release-manifest.json) and
[AUDIT-036](AUDIT.md#audit-036-turso-local-file-profile-and-env-tagged-config).
Adopting hosts bump the connector, delete their local copies of the boundary file
and rename their `AUTH_DATABASE_*` keys to the namespaced `AUTH_DB_*`.

## Authorization cache rerelease from main (published 2026-09-14)

The owner requested joining the full cache release with current main and cutting
fresh patch versions from the verified main commit. Main's Firestore squash tree
exactly matches an already included ancestor; the integration retains all later
cache/audit/security changes. No production algorithm change is required.

Published from main commit `5aa0b29e23f5fd411bdb40b3a7abaea4fa531a1a`:
Turso connector `v0.5.1`, PostgreSQL connector `v0.8.1`,
authorization core `v0.14.1`, and authorization stores Turso `v0.8.1`, PostgreSQL
`v0.9.1`, Firestore `v0.2.1`. Existing tags remain immutable. Prior accepted
cloud/performance gaps remain disclosed; no new verification claim is implied.
All candidate, repository and six-module public checksum/source/build/test/vet
checks passed after proxy indexing. See [main integration plan](plans/authorization-cache-main-release.md)
and [manifest](plans/authorization-cache-main-release-manifest.json).

## Authorization read caching (published 2026-09-14)

Published six dependency-ordered module tags from
`9e67a165fb3c042c60e3b9bc32eb56374d5c5c80` on `authorization-cache-20260914`:
Turso connector `v0.5.0`, PostgreSQL connector `v0.8.0`, authorization core
`v0.14.0`, and authorization stores Turso `v0.8.0`, PostgreSQL `v0.9.0`,
Firestore `v0.2.0`. No SDK, Redis adapter or authentication release was needed.

Local correctness, repository-wide checks and isolated versioned consumer
build/test/vet passed. Remote tag identities match the verified source. All six public
checksum/source and isolated consumer checks passed after Go proxy indexing; see the
[release plan](plans/authorization-cache-release.md) and
[release manifest](plans/authorization-cache-release-manifest.json).

The owner explicitly accepted this feature's unexecuted real-GCP/Turso Cloud
verification and failed/incomplete representative performance acceptance for
publication. These gaps remain open and are not recorded as passed. The earlier
Firestore exception was not reused. Caching remains opt-in; host adoption must
choose its freshness policy and validate its deployment and workload.

Migration: [AUDIT-035](AUDIT.md#audit-035-optional-bounded-staleness-authorization-read-cache).
Optional SQL installation and Firestore writer activation require distinct host
rollout plans. No host activation, migration or deployment was performed.

## Coordinated audit release (2026-09-11)

Published all 34 selected module tags from commit
`c3f8b4ad453021ba1e40c91572d8e4618c2d8382` on `audit-release-20260911`.
The [release manifest](plans/audit-release-manifest.json) records versions and
verified ZIP/go.mod checksums; [AUDIT.md](AUDIT.md) is the consumer migration guide.
All published modules resolve and build with `GOWORK=off`, normal public checksum
verification and no local replacements. Remote main and the newer Firestore branch
were preserved. All three Firestore modules are published as `v0.1.0` from
`10e5f97b32c043f8fdd71cbd733fbb09592c42f7`; the `v0.1.0-beta.1` tags remain
at `973f94322a3a99b8699df8f266c33539c0c21ed8`. Public checksum/source/build/test/vet
verification passed; [firestore-release.md](plans/firestore-release.md) records
the evidence. The owner accepted the explicitly untested real GCP suite for this release.
CMS is compatibility only.

The implementation-time entries below retain their history; the coordinated
manifest supplies their published versions. Segovia v2 adoption is complete in
its local upgrade branch; consumer publication and deployment remain separate.

## Unreleased: Turso statement completion (2026-09-11)

QueryOne now checks result closure before returning a row. SQLite can return an
UPDATE...RETURNING row before an implicit commit; ignoring a close-time busy
failure exposed uncommitted claims as successful work. Return the driver error
and zero result so store-owned busy retry can handle the failed statement.
No signature/schema change. Upgrade connector and adapters together.

Migration: [AUDIT-033](AUDIT.md#audit-033-turso-statement-completion-before-success).
This fix was found by the coordinated release's live jobs concurrency gate.

## Unreleased: Host startup cancellation and constructor errors (2026-09-11)

SQL connector Open and Firestore probing constructors take the host context
first. SendGrid.New, S3.New and events outbox.NewPoller return construction errors
instead of deferring known-invalid configuration to use. Raw SQL store wrappers
retain simple signatures and reject nil borrowed databases explicitly.

Migration: [AUDIT-032](AUDIT.md#audit-032-host-startup-cancellation-and-constructor-errors).
Implementation/release plan: [startup-release-segovia.md](plans/startup-release-segovia.md).
The coordinated SQL/core release is published; see the manifest above.
Firestore v0.1.0 tags are published and retain the disclosed,
owner-accepted gap in real GCP verification. Segovia v2 is the first PostgreSQL-backed adopter.

## Unreleased: Consistent pocket construction (2026-09-11)

Non-CMS pocket roots and configurable services, HTTP adapters and runtimes now
accept typed WithFoo options with explicit required inputs. Optional feature and
policy records replace whole groups. Former construction-only Config/Deps bags
are removed; owned callers and examples use the new signatures. Dedicated source
files use `constructor.go`. This supersedes the retained-pocket-Config choices
from the previous constructor pass. Schemas, dependencies and versions are unchanged.

Migration: [AUDIT-031](AUDIT.md#audit-031-consistent-pocket-constructor-options).
Implementation and checks: [pocket-constructor-options.md](plans/pocket-constructor-options.md).
No modules have been tagged or published.

## Unreleased: Constructor options (2026-09-11)

Coordinate SDK, changed adapters, non-CMS pockets, UI and consuming hosts.
Selected small constructors now accept typed WithFoo options; required inputs
stay explicit and coherent policy/connection records remain. Existing options
configure private construction settings instead of live objects. Nil options
receive explicit diagnostics; defaults and runtime behavior are preserved.

Migration: [AUDIT-030](AUDIT.md#audit-030-constructor-options).
Review and verification: [constructor-options.md](plans/constructor-options.md).
No module versions, dependencies, schemas or persisted formats changed.
No modules have been tagged or published.

## Unreleased: Integration contracts and startup behavior (2026-09-11)

Coordinate affected connectors, pocket stores and hosts. SQL probing constructors
now require the host context; PostgreSQL pool defaults, Turso credential handling,
Firestore terminal errors, Redis deadlines, cron validation and S3 region checks
are corrected. Migration file selection and connection ownership are explicit.
No dependency, persisted-format or SQL migration change is introduced.

Migration: [AUDIT-029](AUDIT.md#audit-029-integration-contracts-and-startup-behavior).
Review and verification: [integration-sweep.md](plans/integration-sweep.md).
No modules have been tagged or published.

## Unreleased: Public pocket services and HTTP adapters (2026-09-11)

Upgrade authentication, authorization, jobs and events together with their stores,
authentication views and consuming hosts. Services and their types now live in
public `logic/<concern>` packages; HTTP adapters live in `inbound/http`. Root
constructors assemble named components. Removed import paths and universal root
services have no compatibility shims. CMS is excluded. This change adds no SQL
migration, stored-format change or module version update.

Migration: [AUDIT-028](AUDIT.md#audit-028-public-pocket-services-and-http-adapters),
including the symbol map and changes to construction, methods and lifecycle wiring.
Implementation and verification: [pocket-structure.md](plans/pocket-structure.md).
No modules have been tagged or published.

## Unreleased: Authorization change history and mutation simplification (2026-09-11)

Upgrade core and adapters together. Commands no longer carry mutation IDs or
expected revisions; results describe current outcomes. Remove Config.Audit and
use store WithAudit() plus explicit source metadata for trusted/raw writes when
recording is wanted. Stop old writers, optionally archive old ledgers, then
apply SQL 0007 (after 0006). Firestore uses explicit ledger cleanup and optional
additional audit indexes. Audit records and changed facts commit together.

Migration: [AUDIT-026](AUDIT.md#audit-026-authorization-change-history-and-mutation-simplification).
Verification: [implementation plan](plans/authorization-audit-log-implementation.md).
This entry supersedes earlier authorization receipt/revision descriptions below.


## Unreleased: Authorization tuple identity (2026-09-11)

Upgrade authorization core and stores together. Remove relationship IDs and
relationship/role timestamps from callers; restart raw listing cursors with
`tuple_key`/`role_key` ordering. SQL hosts apply append-only migration `0006`;
Firestore hosts deploy the new indexes and explicitly upgrade tuple documents
with traffic stopped. Scope revisions and receipt history are removed by the subsequent
AUDIT-026 change above. No tags, publication or external consumer edits occurred.

Migration: [AUDIT-025](AUDIT.md#audit-025-authorization-tuple-identity-and-metadata-removal).
Implementation and verification: [authorization-tuples-implementation.md](plans/authorization-tuples-implementation.md).

## Unreleased: Authorization mutation safety and host contracts (2026-09-11)

Upgrade authorization core and adapters together. Explicitly configure guardian
protection where required; the new default is empty. Migrate guard reads to
DecisionView.Check(CheckRequest), handle semantic/invariant refusals as errors,
and rename the route-only assignment policy. Custom mutation repositories expose
a defensive GuardianPolicy snapshot. PostgreSQL serializable mutation protection,
Firestore retry classification, role budgets and fixed FilterPage pulls are
included. No stored-data migration, tags or publication.

Migration: [AUDIT-024](AUDIT.md#audit-024-authorization-mutation-safety-and-simpler-host-contracts).
Plan and verification: [authorization-followup-implementation.md](plans/authorization-followup-implementation.md).

## Unreleased: Authorization model authority and listing (2026-09-11)

Upgrade authorization core and all store adapters together. Custom stores/views
must supply model-scoped readers; current models govern every permission read,
including retained older tuples. Lookup APIs now distinguish complete ResourceSet
from ResourceIDPage, and FilterPage returns FilteredPage with scan-limit status.
New repeated-work limits, canonical target order, stricter boot validation,
guarded role permissions and constructor-owned logging are host-visible.
No new authorization schema migration or automatic tuple deletion is required.
Baseline/guarded dependency ownership remains an explicit host decision.
No tags, publication or external consumer changes occurred.

Migration: [AUDIT-023](AUDIT.md#audit-023-authorization-model-authority-evaluation-and-listing-apis).
Implementation and verification: [authorization-audit-implementation.md](plans/authorization-audit-implementation.md).

## Unreleased: Authentication proof lifecycle (2026-09-10)

Upgrade authentication core, SQL/Firestore adapters and bundled views together.
Custom repositories adopt atomic provisioning, credential/revision admission,
policy-aware grants and durable invitation claims. Apply invitation migration
`0018`; stop old writers/workers during the cutover. Existing sessions require no
new column, but in-flight sensitive codes, passwordless codes/all magic links, reset links
and OAuth linking flows restart. Browser Origin/logout
requirements, verified invitation ownership and configuration validation change.
Firestore remains incomplete; implemented capabilities have the stronger contracts.
No tags, publication or external consumer edits occurred.

Migration: [AUDIT-022](AUDIT.md#audit-022-authentication-proof-lifecycle-and-host-api).
Verification: [authentication-audit-implementation.md](plans/authentication-audit-implementation.md).

## Unreleased: Jobs SQL generation ordering (2026-09-10)

Jobs pgx/Turso adapters now keep new same-key fenced generations ordered through
repeated/backward clocks, using existing locks and stored timestamp precision.
No schema, repository interface or dependency-version change. Upgrade every SQL
writer together; old adapters can still insert out-of-order generations. Historical
rows are not backfilled. Scheduler durability findings are recorded separately;
this release note does not claim they are fixed. No tags or publication occurred.
Migration: [AUDIT-019](AUDIT.md#audit-019-jobs-sql-generation-ordering).
Evidence and remaining work: [jobs-persistence-audit.md](plans/jobs-persistence-audit.md).

## Unreleased: Jobs runtime handler snapshots (2026-09-10)

Jobs validates and snapshots staged handlers at NewRuntime; queue and scheduler
use the same final kinds, independently for each runtime. Optional Register no
longer rejects enqueue-only or fenced-only services. Public signatures and store
schemas are unchanged, but hosts must handle late invalid configuration at
runtime construction rather than rely on Register. No tags, module requirements
or external publication changed. Include this fix in the coordinated audit release.
Migration: [AUDIT-018](AUDIT.md#audit-018-jobs-runtime-handler-snapshots).
Implementation: [jobs-runtime-implementation.md](plans/jobs-runtime-implementation.md).

## Unreleased: SDK package directory rename (2026-09-10)

SDK foundation packages move to `sdk/pkg/{async,cryptids,environment,list,logging,
validation,web,workers}`. The module and package identifiers/APIs stay the same;
old import paths are removed. All repository imports, templates, guards and
canonical docs use the new paths. No dependency versions or module count changed.

Release a compatible SDK and all dependent modules still referring to the old
paths together; update consumer imports and any generation/build scripts. Current
workspace verification does not prove unpublished module versions resolve remotely.
No tags or external publication occurred. Migration: [AUDIT-017](AUDIT.md#audit-017-sdk-foundation-directory-renamed-to-pkg).
Implementation: [sdk-pkg-rename.md](plans/sdk-pkg-rename.md).

## Unreleased: Shared pockets module (2026-09-10)

The host mounting contract moves from SDK into the independently versioned
`github.com/gopernicus/gopernicus/pockets` module, package `pockets`. Concrete
pocket module paths and runtime behavior stay the same. SDK has no forwarding
package or outward dependency. The workspace now contains 42 modules.

Publish the compatible SDK release, then the new shared `pockets/v0.1.0` module,
then compatible concrete pocket/adapters and Workshop releases with coordinated
requirements. Current `v0.1.0` requirements name the intended first shared-module
release; nothing was tagged or published during this change. Existing SDK and
other dependency pins are retained except where minimum-version selection
requires alignment. Confirm those pins against the final releases before tagging.

Local workspace and explicit sibling replacements verify the unpublished tree;
they do not establish that module-proxy consumers can resolve these changes yet.
Migration: [AUDIT-016](AUDIT.md#audit-016-shared-pockets-module).
Implementation: [pockets-module-move.md](plans/pockets-module-move.md).

## Unreleased: Bound OAuth flows and truthful tracing (2026-09-10)

OAuth providers use concrete configuration and a smaller SDK port with optional
OIDC/refresh capabilities. Authentication requires initiating-client proof for
browser/native authorization-code completion and explicit host email trust for
new email-based registration/adoption. Native routes are opt-in and cookie-free.
Malformed identities, unsafe redirects, oversized responses and raw upstream
error disclosure are rejected. Old in-flight authorization flows restart; no
repository schema or pending-link format changes.

Tracing now reports partial-response failures/panics accurately. OTLP literal
zero disables root sampling, parent decisions take precedence, and explicit
OTel HTTP middleware supplies server spans, typed metadata and opt-in W3C trust.

Coordinate SDK, authentication, OAuth adapters, OTel and dependent module
requirements when publishing. No module versions/tags or external consumer files
were changed. Migration: [AUDIT-015](AUDIT.md#audit-015-bound-oauth-flows-and-truthful-tracing).
Implementation: [oauth-tracing-implementation.md](plans/oauth-tracing-implementation.md).

## Unreleased: Explicit notification deliveries and email correctness (2026-09-10)

Breaking SDK/authentication update: email moves to sdk/capabilities/notify/email.
notify.Send(ctx, deliveries...) attempts exactly the caller-selected deliveries;
email.NewDelivery preserves typed email content and DeliveryFunc adapts host
channels. Indexed partial failures preserve causes; hosts choose retries.
The old Notifier/Message API and integrations/notify/mailer bridge are removed.
Authentication uses Mailer for email and Config.BodySenders for non-email kinds.

Rendering is independent of sending, with immutable construction options,
explicit RenderRequest/SendRequest.Layout and deliberate plain-text templates.
Empty/misnamed template roots and invalid explicit configuration now fail.
SMTP honors cancellation and encodes MIME safely; mailbox/header validation is
stricter. SendGrid isolates concurrent requests, refuses redirects and returns
safe inspectable status errors. Shared production posture lives in notify.

Consumer migration: [AUDIT-014](AUDIT.md#audit-014-explicit-notification-deliveries-and-email-correctness).
Implementation and verification: [email-notify-implementation.md](plans/email-notify-implementation.md).
Coordinate SDK, authentication, SendGrid and dependent module requirements when
publishing. Remove bridge requirements; SendGrid drops unused rest/x/net
dependencies. No module versions, tags, external consumers or queued envelope
schemas were changed. Hosts retain provider configuration and delivery policy.

## Unreleased: File-storage correctness and direct adapter contracts (2026-09-10)

Breaking SDK/storage update: pass Storer or a narrow consumer interface directly;
FileStore/New/WithLogger and operation/unsupported sentinels are removed. Keys
are canonical without silent rewriting; listing is literal, reads describe stored
bytes, ranges share zero/EOF behavior, and missing objects match sdk.ErrNotFound.
Disk confines paths, stages complete replacements and requires concrete Close.
It reserves .gopernicus-tmp (including case variants) and creates replacement/new files with mode
0600; review deployed keys, symlinks and cross-user file access before upgrade.

GCS aborts failed sources, uses explicit local-key/IAM signing and initiates client
PUT sessions through its configured authenticated HTTP client with ContentType
and Origin options. S3 exposes honest InitiateMultipartUpload IDs separately;
its bounded upload manager supports non-seekable inputs up to about 48.8 GiB and
attempts cancellation-safe multipart cleanup. Signed expiry is whole seconds
from one second through seven days. Hosts own error reporting and resources.

Consumer migration: [AUDIT-013](AUDIT.md#audit-013-file-storage-correctness-and-direct-adapter-contracts).
Implementation and verification: [filestorage-implementation.md](plans/filestorage-implementation.md).
Coordinate SDK, GCS/S3 adapter and dependent requirements when publishing. S3
adds AWS transfermanager v0.1.6 without upgrading other pinned dependencies.
GCS promotes its existing auth dependency to direct for credential-context tests.
No external consumers, deployed storage, stored keys, schema, versions or tags
were changed. Actual cloud signing/session permissions and browser CORS need
host verification.

## Unreleased: Event delivery contracts and Redis recovery (2026-09-10)

Breaking SDK/events update: Emit accepts no options and admits bounded async
notification. Memory.Dispatch checks local handler completion; Redis.Publish
checks remote acceptance. Bus.Subscribe now means notification fanout on both
backends; Redis competing consumers use exact-topic SubscribeWork. Outbox pollers
take an explicit delivery function. Record/RemoteEvent preserve event IDs,
metadata and opaque payloads; DecodeRemoteMetadata is removed.

Redis work requires Redis 6.2 or newer and reclaims pending entries, acknowledging
only successful complete handler attempts. MaxLen and BatchSize are removed;
hosts own poison disposition and stream retention. The new v2 transport requires
coordinated old-stream drain, stopped old writers and disjoint physical prefixes;
it does not migrate or discard old pending work automatically.

Consumer migration: [AUDIT-012](AUDIT.md#audit-012-event-delivery-contracts-and-redis-recovery).
Implementation and verification: [events-implementation.md](plans/events-implementation.md).
Coordinate SDK, events pocket, Redis adapter and dependent module requirements
when publishing. No outbox schema, external consumer, module pin, tag or deployed
Redis state was changed.

## Unreleased: Rate-limiter contract and adapter corrections (2026-09-10)

Breaking SDK update: bounded NewMemory(MemoryConfig), small Allower/Limiter ports,
explicit MiddlewareConfig with closed default and error reporting, and removal of
no-op Close and the subject-shaped default resolver/wrapper. Memory, Redis and
Postgres now share validated millisecond windows, anchored approximate admission,
Burst, cancellation and logical expiry behavior. Authentication preserves and
reports its deliberate open outage paths. A runnable host policy example retains
dynamic limit resolution and worker Acquire without SDK account policy.

Consumer migration: [AUDIT-011](AUDIT.md#audit-011-rate-limiter-contract-and-adapter-corrections).
Implementation: [ratelimiter-implementation.md](plans/ratelimiter-implementation.md).
Coordinate SDK, goredis, pgxdb and authentication requirements at release.
Postgres requires the host-owned window_ms column migration. Both adapters use
internal v2 keys; review physical namespace overlap and fresh/dual quotas before
rolling upgrades. No deployment, module pins, tags or external consumers changed.

## Unreleased: Complete caching and correct storage/HTTP policy (2026-09-10)

Breaking SDK/cache update: explicit MemoryConfig/Config/PageConfig constructors,
optional literal PrefixDeleter replacing DeletePattern, and host-owned resource
lifecycle replacing cache Close. Memory is bounded and copies bytes; Redis TTL,
namespace escaping and cancellation now match the storage contract. Cache adds
namespaces, an error hook and typed JSON/load-on-miss helpers. Pages uses bounded,
versioned public HTML records with request isolation and conservative metadata
policy. CMS exposes PageCache configuration. The minimal host demonstrates
application-data caching at GET /catalog.json.

Consumer migration: [AUDIT-010](AUDIT.md#audit-010-cache-storage-data-helpers-and-public-page-policy).
Implementation and verification: [cacher-implementation.md](plans/cacher-implementation.md).
Coordinate SDK, Redis adapter and CMS requirements when releasing. Old page keys
become cold entries; no schema, module pins, tags or external consumers changed.

## Unreleased: Common primitives in root SDK (2026-09-10)

Breaking SDK package consolidation: foundation/{pointer,slug,id,identity} move to
root sdk. Use Deref/DerefOr, Slugify, IDGenerator/IDGenerateFunc/NewIDGenerator,
NanoID/DatabaseID and the explicit Principal/Identity vocabulary. Existing output,
fields and runtime policy stay unchanged. Validation/environment/cryptids retain
their packages. Consumer mapping: [AUDIT-009](AUDIT.md#audit-009-common-primitives-promoted-into-root-sdk).

Coordinate SDK and affected pocket/store/integration requirements and the Workshop
scaffold SDK pin before publishing; the old SDK lacks these root symbols. No
requirements, versions, tags, schema or consumer applications changed here.
Implementation: [sdk-root-promotion.md](plans/sdk-root-promotion.md).

## Unreleased: Restore the cryptids package name (2026-09-10)

The SDK crypto namespace is `foundation/cryptids` ("cryptography tidbits"). This
reverses only S4's temporary `cryptography` name; its ID separation, removed APIs
and security fixes remain. Update any interim cryptography imports/qualifiers.
No behavior, persisted formats, module pins or tags changed. AUDIT-004 now shows
the final API; [AUDIT-008](AUDIT.md#audit-008-cryptids-name-restoration) covers
checkouts already using the temporary name. Coordinate SDK/authentication/JWT
requirements at release. Root SDK consolidation is now implemented in AUDIT-009.

## Unreleased: Workers, job middleware and async lifecycle (2026-09-10)

Breaking SDK update: simplify ProcessFunc to return error, narrow Job, remove
redundant hooks/retry options and Pool.Errors, and replace async presets/shutdown
options with explicit host configuration. Generic runners and worker middleware
remain SDK-owned. Add job middleware, explicit defer/reject outcomes and optional
atomic deferral ports, implemented by jobs memory/pgx/Turso. Correct admission,
drain, persistence reporting, polling cadence and fatal sibling shutdown.
Keyed-work protocol methods now reject empty logical keys.

Consumer migration: [AUDIT-007](AUDIT.md#audit-007-workers-job-middleware-and-async-lifecycle).
Implementation: [workers-cleanup.md](plans/workers-cleanup.md). Coordinate SDK,
jobs, jobs store and dependent module requirements: old processors do not compile
with the new SDK. Custom stores need the optional deferral port only if their
processors defer. No stored status/schema changes, consumer edits, pins or tags.

## Unreleased: Web correctness and API cleanup (2026-09-09)

Breaking SDK update: remove dead router logging options, the Decode alias,
duplicate streaming/generic response conveniences and the reflection OpenAPI
builder. Correct middleware isolation, proxy attribution, response status/error
forwarding, failed-render caching, panic/abort/hijack handling, timeout shutdown,
SSE frames, strict body handling and static/SPA behavior. Authentication's older
JSON routes now share its existing 1-MiB bound and trailing-input rejection.

Consumer migration: [AUDIT-006](AUDIT.md#audit-006-web-correctness-and-api-cleanup).
Implementation: [web-cleanup.md](plans/web-cleanup.md). Coordinate SDK, CMS and
Workshop requirements because old router constructors will not compile with the
new SDK; release authentication/authorization input changes deliberately. No
module pins, tags, consumer upgrades or data migrations were made.

## Unreleased: Listing and transaction audit cleanup (2026-09-09)

Breaking SDK change: `foundation/crud` becomes `foundation/list`, with concise
request/parser names; `Transactor` moves to `capabilities/transaction`. Unused
generic repository/patch helpers and the duplicate not-found alias are removed.
CMS EntryQuery's embedded field is now Request. Listing fixes cover cursor
validation/precision, inclusive previous probes, stable folded ordering, SQL
projection/filter composition and empty offset JSON. SQL transaction helpers
clean up panic/error/canceled/failed commits before connection reuse.

Consumer migration: [AUDIT-005](AUDIT.md#audit-005-listing-vocabulary-and-transaction-correctness).
Implementation: [listing-transaction-cleanup.md](plans/listing-transaction-cleanup.md).
Coordinate SDK, affected pocket/connector and Workshop module requirements at
release. Custom SQL projections and previous probes require review alongside
import changes. No schema migration, release tag or consumer upgrade was made.


## Unreleased: Identity and cryptography cleanup (2026-09-09)

Breaking SDK update: ID generation moves from `foundation/cryptids` into
root SDK (final destination after AUDIT-009); crypto keeps the cryptids name. The SDK HS256 implementation, hasher object, and
identity batch helper are removed. JWT signing uses the existing golang-jwt
integration with an explicit expiration/time/key contract. Bcrypt consistently
enforces its byte limit and classifies it as invalid input. Authentication adds
the host's optional `Config.ValidatePassword` callback; nil preserves defaults.

Consumer migration: [AUDIT-004](AUDIT.md#audit-004-identity-ids-cryptography-and-host-password-policy).
Implementation: [identity-cryptography-cleanup.md](plans/identity-cryptography-cleanup.md).
Coordinate pre-v1 SDK, affected pocket/integration, and Workshop releases and
their SDK requirements: old SDK tags lack the new packages. Integration module
paths stay unchanged. Auth-cms adds the local JWT integration dependency; bcrypt
adds an SDK requirement. No tags were created or consumer applications upgraded.
Auth-cms's module tidy also records SDK v0.7.1 and jobs v0.5.0 already selected
by its dependency graph; these are not the future coordinated release versions.
The verification-discovered jobs memstore generation-order fix needs no schema
or API migration; durable store ordering remains a later audit item.

## Unreleased: SDK environment and logging cleanup (2026-09-09)

Breaking SDK update: dotenv parsing rejects malformed assignments and reports
write failures; tagged parsing checks destination types/ranges and returns
value-free diagnostics. Logging automatically includes available context IDs,
renames the public wrapper to ContextHandler, and removes its functional
options and redundant constructors. Two namespace lookup wrappers are removed.
Examples and Workshop templates now check dotenv loading and use their
configured logger for startup errors.

Consumer migration: [AUDIT-003](AUDIT.md#audit-003-environment-parsing-and-context-logging).
No release tag or dependency pin has changed. Select deliberate pre-v1 SDK and
Workshop versions and coordinate the scaffold's SDK requirement at release.

## Unreleased: SDK utility package cleanup (2026-09-09)

Breaking SDK update: `foundation/conversion` is removed. `Deref` and `DerefOr`
move to root SDK (final destination after AUDIT-009); use Go 1.26's `new(value)` in place of `Ptr`.
The case/acronym, date parsing, JSON default, and overlap APIs are removed.
`sdk.Slugify` preserves the old slug.Make output. Consumer instructions:
[AUDIT.md — AUDIT-002](AUDIT.md#audit-002-conversion-removal-and-pointer-package).
Implementation: [plans/utility-package-cleanup.md](plans/utility-package-cleanup.md).
No tag or dependency pin has changed; select a deliberate pre-v1 breaking SDK
version when releasing this change.

## Unreleased: SDK validation consolidation (2026-09-09)

Breaking SDK update; no tag or dependency pin has been changed. Release this as
a deliberate pre-v1 breaking SDK version. The standalone consumer migration
instructions are in [AUDIT.md — AUDIT-001](AUDIT.md#audit-001-validation-consolidation),
covering removed collectors, typed helper results, password-policy removal,
Unicode length checks, optional fields, and pointer/null JSON decoding.
Implementation record: [plans/validation-consolidation.md](plans/validation-consolidation.md).

This repo is a multi-module workspace (`go.work`, dev-only) with thirty-seven
modules today: `sdk`; `integrations/{cryptids/bcrypt, cryptids/golang-jwt, cryptids/google-uuid,
datastores/pgxdb, datastores/turso, email/sendgrid, filestorage/gcs,
filestorage/s3, kvstores/goredis, oauth/github, oauth/google,
notify/mailer, scheduling/robfig-cron, tracing/otel}`; `pockets/authentication`
(+ `views/goth`, its bundled default views module — auth-v3 AV3-8.2, 2026-07-13;
renamed from `views/templ` and re-implemented on `ui/goth` in ui-goth GOTH-7.2,
2026-07-18), `pockets/authorization` (authorization-v1, 2026-07-09), `pockets/cms`
(+ `views/goth`, its bundled default views module — feature-standard B2, 2026-07-07;
renamed from `views/templ` and re-implemented on `ui/goth` in ui-goth GOTH-7.3,
2026-07-18), `pockets/events` (events-v1, 2026-07-08), `pockets/jobs`
(each pocket + `stores/{turso,pgx}`); `examples/{cms,
minimal, auth-cms, jobs-minimal}`; `workshop/gopernicus` (the scaffolding
CLI — a `go install`-able tool, tagged like any importable module). Each importable module (everything except the four
`examples/*` hosts, which are demonstrations, not libraries) is tagged and
versioned **independently** — there is no single repo-wide version.

**First release cut 2026-08-14: every importable module tagged `<dir>/v0.1.0`**
(33 tags — everything except the four `examples/*` hosts; plan of record
`.claude/plans/release-v0.1.0.md`). The release commit dropped every nested
module's pre-tag relative `replace` and pinned sibling requires at `v0.1.0`
(precondition 2 below, satisfied); `go.work` continues to resolve siblings by
directory for local dev. All upgrade notes below keyed "next tag" describe the
v0.1.0 vintage. Tags are immutable once the module proxy serves them — a
correction is a new patch tag, never a retag.

**2026-08-14 (same day): `sdk/v0.2.0` + `features/jobs/v0.1.1`** — the fenced
dead-letter reason fix (plan of record
`.claude/plans/deadletter-failure-reason.md`; Coordination-Hub issue #5). See
the upgrade notes for both tags below. No other module was bumped: only
`features/jobs` consumes the changed kernel symbol, and its store modules'
`features/jobs v0.1.0` pins upgrade at the host via MVS.

**2026-08-14 (same day): `features/jobs/v0.2.0` +
`features/jobs/stores/{pgx,turso}/v0.2.0`** — jobs tenant metadata (plan of
record `.claude/plans/jobs-tenant-metadata.md`; Coordination-Hub issue #4).
Minor bumps all three; store pins move to `features/jobs v0.2.0` / `sdk
v0.2.0`. See the upgrade notes below — already-migrated hosts need a host-tree
ALTER (reference SQL in the store note).

**2026-08-14 (same day): `sdk/v0.3.0`,
`features/authentication/v0.2.0`, `integrations/datastores/pgxdb/v0.3.0`** — the
coordination-hub authentication upstream batch (plan of record
`.claude/plans/coordination-hub-auth-upstream/`; the owner-cut tag manifest and
post-tag verification steps are in that directory's `tag-manifest.md`). Minor
bumps all three: genuinely global middleware + configurable CORS, the browser
cookie-flow seams (`RefreshCookiePath`, `/auth/csrf`, `/auth/me`,
`origin_rejected`), and the durable pgx rate limiter. `features/authentication`
pins `sdk v0.3.0`; `integrations/oauth/google` stays at **v0.1.0** (byte-identical
to its tag, verified) and the authentication store modules do **not** retag (no
repository contract changed). Read the three upgrade notes below before adopting —
pgxdb v0.3.0 ships **host schema**, and the sdk note carries the batch's one silent
behavior change (`HandleRaw` no longer bypasses global middleware).

**2026-08-15: `integrations/email/sendgrid/v0.2.0`** — truthful capability
metadata (plan of record, in the Coordination-Hub repo,
`.claude/plans/email-and-invitations.md`, task U1). A **minor**: `Sender` now
implements `email.CapabilityReporter`, describing the configured instance —
the default host and explicit HTTPS hosts report TLS/production-capable; a
non-HTTPS custom host (tests, local emulators) reports development-only. No
breaking API change. See the upgrade note below.

**2026-08-16: `features/authentication/v0.2.2`** — the email layout-override
seam (plan of record, in the Coordination-Hub repo,
`.claude/plans/branded-auth-mail-and-pages.md`, task U1). A **patch**, additive
only: `Config.EmailLayouts []EmailLayoutOverride` (aliasing
`delivery.LayoutOverride{FS embed.FS; Dir string}`, `Dir` defaulting to
`"layouts"`) registers host layout files at `email.LayerApp`, so a host-shipped
`transactional` pair re-frames every auth mail while the zero value keeps the
sdk default byte-identically (pinned by test). Content templates
(`EmailContentTemplates`) are untouched and compose with it. No exported API
break, no schema change, no `go.mod` change (still `sdk v0.3.0`), no store
retags.

**2026-08-15: `features/authentication/v0.2.1`** — the
add-or-signup invitation lifecycle fix (plan of record, in the Coordination-Hub
repo, `.claude/plans/email-and-invitations.md`, task U2). A **patch**: a brand-new
OAuth-provisioned account now resolves its pending auto-accept invitations through
the same best-effort resolver `Register`/`Verify` use, and the invitation JSON
response gains the additive `invited_by` owner field. No exported API change, no
schema change, no `go.mod` change (still `sdk v0.3.0`), and no store retags. Read
the upgrade note below before adopting — it pins which OAuth branches do and do
not grant.

**2026-08-15: `sdk/v0.3.1`** — the coordination-hub
`coordination-api-consistency` upstream flags: `web.ErrStateConflict` completes
the 409 vocabulary (code `conflict`, distinct from `ErrConflict`'s
`already_exists`), and `crud.Page`'s absent-`has_more`-means-false end-of-list
contract is now documented (godoc + sdk README). Cut as a **patch by owner
ruling** despite the one additive symbol — see the upgrade note below. No other
module bumps: the sdk tree is otherwise byte-identical to `sdk/v0.3.0`, and no
in-repo module pins move.

**2026-08-16: `sdk/v0.4.0`, `integrations/datastores/pgxdb/v0.4.0`,
`integrations/datastores/turso/v0.3.0`, `features/authentication/v0.3.0`,
`features/authentication/stores/{pgx,turso}/v0.2.0`** — the coordination-hub
authentication upstream batch, phases 1–8 (plan of record
`.claude/plans/coordination-hub-auth-upstream/`), stacked with the crud search
plan (`.claude/plans/crud.md`, T1–T4). Six minor bumps closing every hub flag:
account lifecycle + operator directory, verification resend, canonical
`environment.Mode` with capability-owned transport checks, password-reset links,
provision-on-consumption (**default-off**), the transactional-layout logo fix,
and SQL-side list search in both dialects. Shipped as **one train** by owner
ruling — provisioning is additive and default-off, so it raised no bump.
`integrations/email/sendgrid` did **not** retag (test-only change).

**Two operator-incompatible changes — read before deploying.** Store migrations
`0014` and `0015` must be applied *before* binaries built against the new store
tags (both constructors probe the added columns and refuse to construct
otherwise), and `Config.PasswordResetURL` is now **required in production**. The
as-executed record, per-module upgrade notes, and the adopter checklist are in
the plan directory's `tag-manifest.md` §8 and `adoption-checklist.md`.

**2026-08-18 `features/authentication/v0.3.1` — clickable OAuth pending-link mail.**
Cut from `main` (which was already at `features/authentication/v0.3.0`) as an
ordinary patch on the current line, NOT the earlier-considered maintenance-line
`v0.2.4` off `v0.2.3`: coordination-hub adopts the current line, so no independent
patch line is maintained. The bump is a **patch** despite adding an exported
`Config` field — the field is additive with a safe zero value, following the
owner's `sdk/v0.3.1` precedent that an additive symbol may ship as a patch.

Scope is presentation-only: the anti-takeover OAuth pending-link email becomes a
clickable link (single-use token in the URL **fragment**, mirroring the magic
link) when the new `Config.OAuthLinkBaseURL` (`AUTH_OAUTH_LINK_URL`) is set, and
the pending-link callback redirect carries `auth=link_sent&provider=<name>` so the
host SPA can render a "check your email" state. The anti-takeover model is
unchanged — the emailed secret is still proof of inbox control.

Upgrade note (backward-compatible, no new production requirement): `OAuthLinkBaseURL`
is OPTIONAL. Empty keeps the historical raw-token email line and, when OAuth
providers are wired, emits one startup WARN naming `AUTH_OAUTH_LINK_URL`. A
non-empty value is validated at construction — absolute http(s) with a host, no
fragment (`ErrOAuthLinkURLInvalid`), HTTPS in production (`ErrOAuthLinkURLInsecure`);
existing non-secret query parameters are preserved. No schema or sibling-module
change. The host-side landing route that reads the fragment token and POSTs
`verify-link` is coordination-hub's #162.

**2026-08-25: `sdk` + `integrations/datastores/pgxdb` — next tags, patch by owner
ruling** — `crud.MapPageErr` and `pgxdb.ProbeTable` (plan of record
`plans/pgxdb-probe-table-map-page-err.md`; originating host gps-360-go).
Both additive, zero-value-preserving, no schema, no sibling pin moves. See the
two upgrade notes below.

**2026-08-25: `features/authentication/v0.5.3`** —
`Config.PasswordFlowsDisabled` (plan of record
`plans/authentication-password-flows-toggle.md`; originating host gps-360-go, a
Google-only staff host). Additive, zero value keeps every route; no schema, no
pin moves. See the upgrade note below.

**2026-08-25: `features/authentication/v0.5.4`** — `Config.MachineRoutesDisabled`
(plan of record `plans/authentication-machine-routes-toggle.md`; originating
host gps-360-go). Additive, zero value keeps every route; no schema, no pin
moves. Carries a **security caveat** on the bundled lifecycle routes — read the
upgrade note before exposing them on a multi-user host.

**2026-08-26: `integrations/datastores/pgxdb/v0.5.0` +
`features/authentication/stores/pgx/v0.4.0`,
`features/{authorization,cms,events}/stores/pgx/v0.2.0`,
`features/jobs/stores/pgx/v0.3.0` — ONE train, cut and pushed 2026-08-26 (minor
across all six; cold-resolution verified)** — host-chosen schema for every feature
pgx store (plan of record `plans/pgx-store-schema-option.md`; gopernicus issue
#4; originating host gps-360-go). `pgxdb.Schema` + `RunMigrations(...,
pgxdb.WithSchema(s))`, and `WithSchema(s)` on every store's `Repositories` AND
every exported per-repository constructor. Default (no option) SQL is
byte-for-byte unchanged. **Pin moves: every store pins `pgxdb v0.5.0`, which
drags `sdk` to v0.4.x for four of them.** Read the combined upgrade note below
before adopting — it ratifies a change to the migration runner's stream model
(one stream per schema, per-schema ledgers) and names the jobs `QueueOption`
compatibility consequence.

**2026-08-26: `features/authentication/v0.6.0` — next tag, MINOR** — the bundled
machine-identity lifecycle routes come standard **behind a host gate**
(`Config.MachineRoutesGate`), and the one-day-old `Config.MachineRoutesDisabled`
is **REMOVED** — a source break (plan of record
`plans/authentication-machine-routes-gate.md`; gopernicus issue #6; the host
that had to re-implement these routes is gps-360-go). Ungated routes no longer
mount at all:
a v0.5.x host with the machine repositories wired and no gate answers 404 on all
five and WARNs at boot until it names one. `POST /auth/service-accounts` no
longer takes `owner_user_id` (400 by name, one release) — the caller is the
owner, and `act_as_user_id` is the explicit, validated, audited delegation, and
cookie clients must now double-submit the CSRF token on the three lifecycle
POSTs (`application/json` required there too). No
schema, no store retags, no pin moves. Read the upgrade note below before
adopting.

**2026-08-27: `sdk/v0.6.0`, `integrations/datastores/pgxdb/v0.6.0`,
`pockets/authentication/v0.8.0` — next tags, ONE train, MINOR across all
three** — the list contract reaches the request (plan of record
`plans/web-crud-list-request.md`; gopernicus issues #11 and #9, bundled by
owner ruling; originating host gps-360-go). `crud.ParseListQuery` over
`url.Values` with every `ParseListRequest`/`ParseOrder` rejection wrapping
`sdk.ErrInvalidInput`; `crud.Items`/`MapItems` and nil-page normalization;
`web.NoStore()`; pgxdb registers a UTC `TimestamptzCodec` on every connection
and defaults the session `timezone` to `UTC` when the host names none; plus
the #9 helpers `Collect[T]`, `ProbeTables`, and `MapError` 22P02. **Pin move:
`pockets/authentication` → `sdk v0.6.0`**; pgxdb stays on `sdk v0.4.0`; no
store module retags (their pins upgrade at the host via MVS). Three observable
changes ride this train — read the notes below before adopting: parser errors
now classify as 400 through `ErrFromDomain` (a host that special-cased the 500
can delete the special case), SDK-constructed empty pages marshal `"items":[]`
instead of `null` (visible on turso-backed hosts), and every scanned
`timestamptz` is UTC-located while zone-dependent SQL on a zone-less DSN now
evaluates in UTC.

**2026-08-28: `features/authentication/v0.4.3` — TAGGED (maintenance line off the
immutable `v0.4.2` tag) + `pockets/authentication/v0.8.1` (next tag on main) —
host-owned mail DATA and SUBJECTS, not only bodies** (plan of record
`plans/auth-mail-host-data-and-subjects.md`; consumer ruling 2026-08-28,
originating host coordination-hub, pinned to `features/authentication v0.4.2`
+ `sdk v0.4.1`). `Config.DeliveryData` (a per-render data hook with reserved
`Secret`/`Link`/`Subject`), `Config.EmailSubjects` + `Config.SMSBodies`
(per-purpose overrides, missing-key errors on), the ten `Purpose*` constants
exported, and enriched invitation/member-added data (`ResourceName`,
`RelationLabel`, `InviterName`, `InvitedBy`, `Metadata`, `InvitationID`,
`OperationID`, …). Nil hook + empty maps render byte-for-byte today's output
(pinned by golden). **Patch on both lines by plan ruling** — additive only, no
schema, no store retags, **no pin moves**: the `v0.4.3` maintenance commit keeps
`sdk v0.4.0` in `go.mod` (the hub's `sdk v0.4.1` pin stays), and `v0.8.1`
keeps `sdk v0.6.0`. Same public contract, safety rules, and tests on both;
adapted commits, not one cherry-pick (the module/paths were renamed between
them). Read the upgrade note below.

**2026-08-28: `pockets/authentication/v0.8.2` — TAGGED @ `c016423` (#17
squash) — `identity.Resolver` is principal-exact**
(plan of record `plans/identity-resolver-principal-exact.md`; originating host
gps-360-go plans/32). `Resolve` no longer synthesizes a user's display name
from the primary email local part when the stored `DisplayName` is blank — a
blank name is projected blank, exactly as stored; the port doc's "never
fabricates an Info" now holds for every field. Service accounts unchanged.
Patch by plan ruling: no port change, no schema, no Config, no pin moves. One
observable change for a host that rendered `Info.DisplayName` unguarded: a
name-less user now shows "" where it showed `bob` — choose the fallback at the
render site. See the upgrade note below.

**2026-08-28: `integrations/datastores/pgxdb/v0.6.1` — TAGGED @ `098241f`
(#16 squash), a patch by owner ruling, as with `ProbeTable`** —
`ListQuery.FixedOrder`, a store-fixed composite `ORDER BY` for the offset
strategy (plan of record `plans/pgxdb-list-fixed-order.md`; gopernicus issue
#15; originating host gps-360-go). Additive, zero-value-preserving, no schema,
no pin moves (still `sdk v0.4.0`), no store retags. See the upgrade note below.

**2026-08-29: `pockets/authentication/v0.9.0` — TAGGED @ `ad8b8a8` (#18
squash) — BREAKING, pre-1.0** — one composable authenticator, `RequirePrincipal(opts ...PrincipalOption)`,
named helpers, and the credential on the context (plan of record
`plans/authentication-principal-posture.md`; originating host gps-360-go
`plans/33-principals-api-keys-and-route-proof.md` D2). `RequireUser`,
`RequireServiceAccount`, `RequireLiveSession`, `RequirePrincipalBrowser`,
`RequireLiveSessionBrowser`, and `AuthenticateAPIKey` are REMOVED; every call
site is rewritten against the four options (`Accept`, `Transports`, `Live`,
`Browser`), the six named helpers, and `CurrentCredential`. `Config.BundledRouteAuth`
lets a host replace one bundled route group's authentication posture without
restating the others. No schema, no store retags. See the upgrade note below
before adopting — the owner cuts the tag.

**2026-08-31: `sdk/v0.7.0` — TAGGED @ `41ddfdb` (#23), MINOR (additive)** — the crud write
vocabulary (plan of record `.claude/plans/crud-write-vocabulary.md`; gopernicus
issue #21; originating host gps-360-go slice S1). One module changes, one tag:
the kernel gains `sdk.Violation`/`ValidationError`/`StaleError` + the five
`Code*` strings (`faults.go`), `crud` gains `Field[T]`/`Some`/`Overlay`, and
`web` gains `ReadBody` + `ErrStale` and renders the two new fault types. Every
new symbol is additive and every new wire field is `omitempty`, so existing
bodies are byte-identical; no `go.mod` changes, no sibling retags, no schema.
See the upgrade note below before adopting — the owner cuts the tag.

**2026-08-31: `pockets/authorization/v0.7.0` — TAGGED @ `e95c82b` (#24, #25), MINOR (additive)** —
the authorization train (plans of record
`.claude/plans/authorization-gates-and-lookup.md` and
`.claude/plans/authorization-role-routes.md`; gopernicus issues #19, #20, #22;
originating host gps-360-go). One module changes, one tag:
`RequireAnyPermission(…GateSpec)` puts the disjunction on the route line (with
the `ErrAlternativeNotApplicable` resolver sentinel), `LookupResourcesIn` adds
a caller `Limit` + `LookupResult.Truncated` above the untouched budget, and the
role-administration routes come standard under `/authorization/*` behind
`Config.RoleRoutesGate` + `Config.AssignmentPolicy`. Pin moves `sdk v0.5.0 →
v0.7.0`. One visible upgrade-time change: a host wiring the roles kind + a
`Guard` with no gate now logs a boot WARN (routes not mounted). No schema, no
store retags. See the upgrade note below before adopting — the owner cuts the
tag.

**2026-08-31: `pockets/jobs/v0.4.0` — TAGGED @ `3107dce`, MINOR (behavioral
fix, no API change)** — graceful drain for the unfenced runtime (plan of record
`.claude/plans/jobs-graceful-drain.md`; originating host gps-360-go plans/40
Batch 0). One module changes, one tag: the unfenced queue and scheduler pool
iterations run under `context.WithoutCancel`, so cancellation stops NEW claims
but an iteration already begun finishes its handler and persists its terminal
`Complete`/`Fail` on a live context — the drain the README always promised.
The fenced runtime is deliberately unchanged (its cancellation-to-lease-recovery
contract is different and stays different). No schema, no store retags, no pin
moves. See the upgrade note below — a host must bound handler work with its own
timeout strictly below the queue lease, since a detached iteration no longer
dies with the process context.

**2026-09-01: `sdk/v0.7.1` — TAGGED @ `418414f` (PR #33), PATCH (additive, zero-value-preserving)**
— workers idle observability (plan of record
`.claude/plans/workers-idle-observability.md`; gopernicus issue #32;
originating host gps-360-go `cmd/workers/io`, its plans/40). One module
changes, one tag: `foundation/workers.Pool` logs a Debug `"iteration: no work"`
line per idle iteration and gains `WithHeartbeat(interval)`, an opt-in INFO
`"pool alive"` beat carrying the iteration / claim / error deltas since the
previous beat. An idle pool was silent between boot and the first job, so an
operator could not tell "alive and polling" from "wedged"; now a MISSING
heartbeat is the alarm. Zero or less disables the heartbeat and is the default,
so the option is a no-op for every existing host, and the Debug line is
invisible to any host whose logger does not enable Debug — INFO-and-higher
output, the exported `Stats` shape, and the shutdown stats line are all
unchanged. No `go.mod` change (`sdk` still has no require block), no sibling
retags, no schema, no new guard. See the upgrade note below before adopting.
Cold-resolution verified on the module proxy first poll.

**2026-09-01: `pockets/jobs/v0.4.1` — TAGGED, PATCH (additive)** — `Config.Heartbeat`
passes the sdk v0.7.1 pool heartbeat through to jobs-built pools (plan of record
`.claude/plans/jobs-heartbeat-passthrough.md`; originating host gps-360-go
`cmd/workers/io`). A jobs host never calls `workers.NewPool`, so `WithHeartbeat`
was unreachable behind `jobs.NewRuntime`; one additive Config field now threads
it onto both pools (queue and scheduler). Zero is the default and stays "no
heartbeat" — existing hosts are byte-identical. Pin moves sdk v0.5.0 → v0.7.1.
The Debug "iteration: no work" line needed no pocket change — it rides the pool.

**2026-09-03: `sdk/v0.8.0` — TAGGED @ `8564703` (PR #36), MINOR (additive + one parser semantic)** —
environment secrets, the dotenv comment rule, and `ParseEnvTags` adoption (plan
of record `.claude/plans/environment-secret-and-dotenv-gaps.md`; gopernicus
issue #34; originating hosts gps-360-go and segovia v2). One module changes:
`foundation/environment` gains `Secret`/`DecodeSecret` (hex-encoded secret with
a minimum-length floor; unset or empty → nil so the host applies its posture;
`ErrSecretEncoding`/`ErrSecretUnderMinimum`), `LoadPath` adopts the dotenv
inline-comment rule (an unquoted `#` opens a comment only when preceded by
whitespace, evaluated before trimming — `KEY=   # note` is now empty instead of
`# note`, `KEY=#alerts` stays `#alerts`), and `ParseEnvTags` descends into
untagged nested structs and treats an empty value (`KEY=`) as not provided
(`required` errors, `default:` applies, a pre-seeded field survives).
`foundation/web.ServerConfig` gains `TrustedProxyCount` and `PublicBaseURL`
(env-tagged) plus `Origin()`. No `go.mod` change (`sdk` still has no require
block), no schema, no new guard. See the upgrade note below — it carries a
**key-rotation advisory** for hosts that read `AUTH_JWT_SECRET` as ASCII from a
`.env` seeded off a `KEY=   # comment` template. Cold-resolution verified on the
module proxy first poll (fresh `GOMODCACHE`; `Secret` and `ServerConfig.Origin`
exercised from a throwaway module).

**2026-09-03: `pockets/jobs/v0.4.2`, `pockets/events/v0.2.1`, `pockets/cms/v0.2.1`,
`pockets/authentication/v0.9.1` — TAGGED @ `8564703` (PR #36), ONE train, PATCH (tags only)** —
`env:` struct tags on the Configs' tuning and host-read fields (same plan of
record; `JOBS_*` on `jobs.Config` and `FencedRuntimeConfig`, `EVENTS_*`,
`CMS_MAIL_FROM`/`CMS_CONTACT_TO`, and the six untagged `AUTH_*` fields plus
`AUTH_COOKIE_*` on `CookieConfig`). No `default:` tags — the pockets' existing
zero-value contracts stay the defaults — so a struct-literal host is
byte-identical; a host opts in by calling `ParseEnvTags` after its literal. No
behavior, signature, schema, store retag, or pin move; the pockets keep their
older sdk pins (the nested `AUTH_COOKIE_*` tags need the HOST's sdk ≥ v0.8.0).
Cut after `sdk/v0.8.0` resolved; all four resolved on the proxy first poll.

**2026-09-03: `workshop/gopernicus/v0.2.1` — TAGGED @ `8564703` (PR #36), PATCH** — the init
scaffold reads `web.ServerConfig`, `logging.Options`, and `pgxdb.Config`
through `ParseEnvTags` (the `serverConfig()` helper and the three
`GetEnvOrDefault` logging lines are gone), its `.env.example` moves every hint
onto its own line above the key and states the `KEY=`-keeps-the-default
convention, and its `go.mod` template pins move `sdk v0.5.0 → v0.8.0`,
`pgxdb v0.1.0 → v0.6.1`, `turso v0.1.0 → v0.3.0`. Cut LAST, after `sdk/v0.8.0`
resolved on the proxy, because the rendered `go.mod` requires it. Cold-verified:
`go install …@v0.2.1` into a fresh `GOMODCACHE`/`GOBIN`, `gopernicus init --db pgx`
into a scratch dir renders the new `.env.example`/`go.mod`, and the scratch host
builds against the proxy.

**2026-09-04: `pockets/jobs/v0.5.0` — TAGGED @ `eac19a0` (PR #38);
`pockets/jobs/stores/pgx/v0.5.0` + `pockets/jobs/stores/turso/v0.4.0` — TAGGED
@ `de453fb`; ONE train, MINOR (breaking ports)** — Claim and the scheduler pool filter by the kinds a runtime actually
handles (plan of record `.claude/plans/jobs-kind-filtered-claim.md`; gopernicus
issue #37; originating host gps-360-go plans/43 §4.5a). `job.QueueRepository.
Claim`, `job.FencedQueueRepository.Claim`, and `schedule.Repository.ListDue`
gain a trailing `kinds []string` (nil = unfiltered); `schedule.Repository.
ClaimDue` gains a trailing `expectedKind string` re-asserted in the CAS. Both
runtimes wrap the store in a kind-scoped adapter (the repository ports are no
longer kernel `JobStore`/`FencedStore` themselves), derive the kinds from
`Handlers`, and log them at start-up. No schema, no sdk change, no new guard.
Store pins move `pockets/jobs v0.3.0 → v0.5.0` and, by MVS through it,
`sdk v0.5.0 → v0.7.1`. Core tag first, proxy poll, then the pin commit and the
store tags; all three cold-resolve from a fresh `GOMODCACHE` and satisfy the new
ports from a throwaway module. #37 closed; the silent-orphan follow-up is #39.
See the upgrade note below.

**2026-09-08: `pockets/authorization/v0.8.0` — TAGGED @ `3c446c0` (PR #40);
`pockets/authorization/stores/pgx/v0.4.0` + `pockets/authorization/stores/turso/v0.3.0`
— TAGGED @ `9e5a713`; ONE train, MINOR (breaking store port; train prepared
2026-09-05 on branch `authorization-decisionview-permission`)** — `DecisionView.CheckPermission`: the hierarchy walk
inside the guarded mutation (plan of record
`.claude/plans/authorization-decisionview-permission.md`; originating host
segovia v2 tenancy D13). The guard's only relationship read was a direct-tuple
`CheckRelation` that could not follow a `Through`, so a space manager or tenant
owner — who holds no direct tuple on the dashboard they legitimately manage —
was invisible to the guard. Now the read-side walk is generic over a two-method
`PermissionReader`, `Service.EvaluateWith` runs it over any reader, and the
core composes a `permissionView` over the store's view so a host guard asks
`view.CheckPermission(scope, permission, principal)` and gets the read-side
answer inside the transaction, every navigated scope a locked,
revision-validated dependency. The store port splits: `StoreDecisionView`
(what a store implements; gains `CheckRelationBounded` and `RelationTargets`,
record-before-read) and `DecisionView` (what a guard receives; embeds it and
adds `CheckPermission`); `mutation.Guard` takes `StoreDecisionView`. The legacy
pgx/turso `CheckRelation` two-statement snapshot hazard is closed by delegation
(grant, reached scopes, and their revisions now come from ONE statement). No
schema, no sdk change, no new guard. Store pins move `pockets/authorization
v0.6.0 → v0.8.0` and, by MVS through it, `sdk v0.5.0 → v0.7.0`. Core tag first,
proxy poll, then the pin commit and the store tags; all three cold-resolve from
a fresh `GOMODCACHE` with `GOWORK=off` and the `StoreDecisionView` /
`DecisionView` split compiles from a throwaway module. See the upgrade note
below.

**2026-09-08 (same day): `pockets/authorization/v0.9.0` — TAGGED @ `495787c` (PR #41);
`pockets/authorization/stores/pgx/v0.5.0` + `pockets/authorization/stores/turso/v0.4.0`
— TAGGED @ `a5ca592`; ONE train, MINOR (host-contract expansion: the store ports
gain the ambient-transaction paragraph and `storetest` gains an exported entry
point; stores change behavior)** — baseline writes join the ambient
`crud.Transactor` transaction (plan of record
`.claude/plans/authorization-stores-ambient-transaction.md`, executed copy in
`plans/`; originating host segovia v2 tenancy D15: "application rows are
canonical; navigational tuples are an atomic projection"). Before, a host that
wrapped its own row write and `RelationshipWriter.SetRelationTargets` in one
`Transact` got a silent atomicity split: the row rolled back with the
transaction while the tuple committed on the store's own connection. Now every
`relationship.Storer` / `role.Storer` method in both SQL stores runs on
`QuerierFrom(ctx)` (reads included), `SetRelationTargets` reconciles ON the
ambient transaction instead of opening its own (pgx: the advisory xact lock is
released at the HOST's commit; turso: no busy retry on the ambient path), and
the guarded path — `Apply`/`ApplyGuarded`, so every `Service` mutation and
`SystemMutator` — REFUSES inside an ambient transaction with the new
`mutation.ErrGuardedInsideTransaction` (re-exported `authorization.ErrGuardedInsideTransaction`,
wraps `sdk.ErrInvalidInput`) rather than splitting atomicity silently. Outside
a transaction nothing changed. The proof is `storetest.RunTransactional` (eight
specs, the join proven from both sides through the same fixture) plus the pgx
row-plus-tuple live test with the widened-lock proof. The turso store's connector
pin moves `v0.1.0 → v0.3.0`. No database migration. See the upgrade note.

## Tagging scheme

Nested Go modules in a single repo are tagged with the module's directory as a
prefix, per the standard Go module convention for multi-module repos:

```
sdk/v0.1.0
pockets/v0.1.0
integrations/datastores/turso/v0.1.0
pockets/cms/v0.1.0
pockets/cms/stores/turso/v0.1.0
ui/goth/v0.1.0
```

Each module's own `go.mod` `require` versions (e.g. `pockets/cms/stores/turso`
requiring `sdk`) are bumped and tagged independently — a patch release of
`sdk` does not force a release of every module that depends on it, only the
ones whose `go.mod` is updated to require the new version.

**UI-implementation modules (ui-goth GOTH-0.2, 2026-07-17).** The seventh module
kind — a UI implementation under the top-level `ui/` family — tags the same
nested way and is versioned independently (`ui/goth/v0.1.0`). Unlike the four
`examples/*` hosts it is an **importable** module, so it IS tagged; its `go.mod`
requires its own view/runtime libraries and `sdk`, never a pocket/integration/
example/workshop (guard G17). A pocket's `views/goth` adapter module (when it
lands) tags independently and requires its pocket core + `sdk` + the pinned
`ui/goth` tag. The `ui/goth` module itself is created at GOTH-1.1; no `ui/*` tag
is cut this milestone.

**The `workshop/gopernicus` CLI as a host `tool` pin (host-layout-contract,
2026-08-27) — LIVING.** The scaffolding CLI is tagged like any other importable
module, but from **v0.4.0** (published 2026-10-20; host-contract-reconciliation) it is also
something a host depends on: it carries the `guard` verb that checks a host
against the H0–H14 rules of
`examples/README.md`, and a host pins it with a `go.mod` `tool` directive plus
the matching `require` — a developer-time dependency, never a runtime import.
(The `init` scaffold does not emit that `tool` pin yet — v0.4.0/v0.4.1 Known; a
scaffolded host carries three grep guards in its Makefile and adds the pin by hand.)
That makes the rule-set versioned with the binary: **new H-rules ship in a minor
version of `workshop/gopernicus`, and a host adopts them by bumping its pin**
(`go get -tool github.com/gopernicus/gopernicus/workshop/gopernicus@<next>`),
never by copying Makefile text. **The opt-in production-posture P rules
(`guard --posture`, from v0.5.0) ship the same way**: a new P rule or a new
entry on `examples/README.md` §3b's Recognized posture surfaces list is a
workshop minor, and an older pin exits 2 on `--posture`, which CI treats as a
broken invocation, never a pass. A host that stays on an older pin keeps the
older rule-set and is unaffected until it bumps; a rule whose wording changes
is a change to `examples/README.md` and to the CLI's `--list` output in the same
tag. This repo's own `examples/*` never carry the `tool` directive (it would put
the workshop module in an example's graph, against G11) — they are guarded with
`go run` from the Makefile instead.

**Expand-step patches.** A store patch that ships a migration ahead of the minor
that requires it (the expand step, with probes that tolerate either schema) is
tagged on `main` only after the migration text is pinned by a test on `main`.

## Preconditions before the first tag

1. **Module paths are final.** Every `go.mod` module line and internal import
   is rooted at `github.com/gopernicus/gopernicus/...`.
2. **`replace` directives are dropped or pinned.** `go.work` itself is
   dev-only and is never part of what a downstream consumer sees. The nested
   modules that reference sibling modules by relative path in their own
   `go.mod` (e.g. `pockets/cms/stores/turso`'s `replace` of `sdk` and
   `pockets/cms` to `../../../../sdk` and `../../..`) must have those
   `replace` lines removed and replaced with ordinary `require` entries
   pinned to the sibling module's tagged version, so `go build` works for a
   consumer who does not have this repo checked out as a workspace.
3. **Guards + tests green** (`make check`) on the commit being tagged.

## Cutting a tag

For each module being released, from the repo root. Tags are annotated (`-a`) and unsigned;
they remain verifiable through `sum.golang.org` and the checksum manifests in this file.
Signed tags are a planned follow-up once a dedicated release-signing key exists (FHC-9).

```sh
git tag -a pockets/cms/v0.1.0 -m "pockets/cms v0.1.0"
git push origin pockets/cms/v0.1.0
```

A consumer depends on it the normal Go way:

```sh
go get github.com/gopernicus/gopernicus/pockets/cms@v0.1.0
```

## Version bumps

Standard Go module semver rules apply per-module:

- **Patch** — bugfix, internal behavior, or a narrowly scoped additive change
  whose zero value preserves the historical behavior and which requires no
  schema migration, sibling-module bump, or mandatory production configuration.
  This explicitly includes optional host configuration and presentation changes
  when they can be adopted independently on a maintenance line. The new field
  or behavior must still have compatibility tests and an upgrade note.
- **Minor** — additive, backward-compatible exported API change that materially
  expands the host contract, requires coordinated adoption, introduces a
  mandatory production configuration, or otherwise cannot be safely consumed as
  a maintenance-line patch (for example, a new required `Config` field or a
  migration-bearing store change).
- **Major** — breaking exported API change (removed/renamed exported type or
  field, changed method signature). Pre-`v1`, breaking changes are expected
  and do not require a major bump by Go's own pre-release semantics; each
  module should still move to `v1.0.0` deliberately once its contract is
  considered stable, not accidentally on the first tag.

**`ui/goth` `Requirements`-surface convention (ui-goth gate-b, 2026-07-18).** Any
change to a `ui/goth` bundle's browser `Requirements` — a new CSP directive, a new
required source, or a change to what a profile requires — is an **adopter-facing
upgrade note even when it is only a semver patch**. Adopters map `Requirements` into
their own CSP (see `ui/goth/README.md` §11.3), so a widened requirement that ships
silently would break a host whose CSP no longer covers the kit's assets. Record it in
the module's next-tag upgrade note below and tell hosts to re-derive their CSP header.

## Upgrade notes (keyed to each module's next tag)

### GPS-360-Go follow-ups — credential ownership and limiter schema (published 2026-09-13)

Published from `5fb8d51cb2355027c6a32a9b1bb617acb42b13db`; all five modules passed
normal public checksum/source and independent build/test/vet verification.
The release is tracked in [plans/gps360-upstream-followups.md](plans/gps360-upstream-followups.md).
The patch set is pgxdb `v0.7.1`, authentication `v0.11.1`, authentication/pgx
`v0.6.1`, authentication/turso `v0.5.1`, and authentication/firestore `v0.1.1`.
Published source/checksum and verification evidence is in
[the release manifest](plans/gps360-upstream-release-manifest.json).

The reported HTTP identifier-removal request could promote another user's
identifier without changing that user's authentication revision, even with
password and delivery disabled. Core validation now rejects an invalid
replacement before consuming a recent-authentication grant. Atomic store
validation protects both retirement and identifier-use changes, including direct
repository callers. Targets must be owned and active; an explicit replacement
must be distinct, active and of the same owner and kind, replacing a primary.
Contact-only unverified identifiers remain valid replacements. Login/recovery
use changes retain their verification requirement. Invalid mutations leave no
partial credential, revision or revocation changes. This is a confirmed
cross-user credential-metadata mutation; account takeover was not demonstrated.

Repin core and the affected adapter together and stop old vulnerable writers
during rollout. These authentication patches add no migration. Custom stores
must implement the clarified atomic validation contract and shared regressions.
Firestore `v0.1.0` already rejected foreign ownership; `v0.1.1` adds the remaining
eligibility checks. Its changed source requires fresh emulator/race evidence;
the real GCP suite remains unrun, with the same index/Admin API/production
contention and backend-limit verification gap disclosed for the initial release.

`pgxdb.WithLimiterSchema(pgxdb.Schema)` qualifies admission, reset and startup
probes on the existing host pool. Its zero value preserves unqualified behavior;
the algorithm and `v2:` key namespace are unchanged. Existing compatible limiter
tables require no migration. A host adopting a new selected schema applies the
qualified table/index DDL before startup and probes that schema. No automatic
DDL, search-path changes or counter relocation occurs. Schema changes require a
coordinated cutover to avoid split quotas; host-owned pruning must be qualified.
See [qualified DDL and wiring](integrations/datastores/pgxdb/README.md#schema-selection-on-a-shared-pool)
and the [consumer repinning handoff](plans/gps360-upstream-repin-handoff.md).

### Firestore — v0.1.0 (published 2026-09-13; real GCP suite untested)

The release plan is [plans/firestore-release.md](plans/firestore-release.md).
These are three new, opt-in modules; existing SQL hosts do not need to adopt them.
The owner authorized unsuffixed `v0.1.0` after the verified beta publication,
accepting the known real GCP testing gap. All three tags are published from
`10e5f97b32c043f8fdd71cbd733fbb09592c42f7` in dependency order. Final pins,
source identity, full workspace checks and independent candidate/public module
verification passed. Public downloads use ordinary checksums and no replacements.
The full real GCP suite has not run. Index deployment/readiness, Admin API
probing, production contention/snapshot behavior and backend limits remain
unverified against GCP. This limitation belongs in each module README and tag
annotation; it must not be reported as a passing or completed live check.

| Module | Direct framework dependencies |
|---|---|
| `integrations/datastores/firestore` | `sdk v0.9.0` |
| `pockets/authorization/stores/firestore` | connector `v0.1.0`, authorization `v0.13.0`, SDK `v0.9.0` |
| `pockets/authentication/stores/firestore` | connector `v0.1.0`, authentication `v0.11.0`, SDK `v0.9.0` |

The store modules have no connector replacements, and the temporary root
`go.work` bootstrap was removed after public v0.1.0 connector verification.
Verify candidate archives and then public downloads with
`GOWORK=off` and no replacement directives; do not rewrite the already-published
SDK or pocket tags to accommodate an older Firestore implementation.

**Connector.** Google Cloud Firestore Native mode support covers connection
lifecycle, document/query access, `sdk/capabilities/transaction.Transactor`, read snapshots,
`List[T]`, error mapping, index manifest merge/export/probing and disposable
emulator/live test helpers. Datastore mode and Enterprise are outside this
module's scope. See its [README](integrations/datastores/firestore/README.md).

A transaction callback can run more than once. Read everything before writing,
reset attempt-local state, and keep external effects outside the callback.
A commit failure may have an ambiguous outcome; callers must not assume that
any error means nothing committed. The connector retains wrapped SDK error
identities and backend context.

**Authorization store.** Implements public relationship, role and atomic mutation
ports against the current host model, with optional atomic change history via
`WithAudit()`. Relationships use natural tuple identity; roles and tuples carry
no historical metadata. There are no scopes, caller mutation IDs, revision
ledgers or durable replay receipts. Apply operations validate current state even
for natural no-ops. See its [README](pockets/authorization/stores/firestore/README.md)
and [UPGRADE.md](pockets/authorization/stores/firestore/UPGRADE.md) for unpublished
schema migration and retired-storage cleanup.

The adapter does not split oversized transactions. Backend request-size limits
apply to facts, subject claims and optional audit writes together; there is no
invented 500-document transaction ceiling. Effective-role counts read the
matching population because they must group role assignments.

**Authentication store.** Implements the current authentication repository
contracts, including credential changes and revocation, session admission,
OAuth and authentication grants, machine credentials, invitations and acceptance,
challenges, contact changes, password resets and passwordless redemption. The
reconciliation must retain both the completed adapter and the audited proof
binding, ownership and lifecycle rules. See its
[README](pockets/authentication/stores/firestore/README.md) and
[SCHEMA.md](pockets/authentication/stores/firestore/SCHEMA.md). Current release
verification is recorded in the plan; earlier test counts are not evidence for
this reconciled implementation.

Firestore TTL deletes bypass claim maintenance. Do not enable TTL on records
whose deletion must release uniqueness claims or satisfy counted purge contracts.
Hosts own retention and any safe TTL configuration; neither store installs it.

**Transaction family difference.** Both pocket adapters reject ambient host
transactions with `ErrAmbientTransactionUnsupported`; authorization mutation
calls also match `mutations.ErrGuardedInsideTransaction`. Firestore cannot supply
the authorization SQL baseline writers' read-after-write ambient join contract.
Those SQL baseline writers can join a host transaction; guarded authorization
calls and authentication compositions retain their own documented transaction
boundaries. Selecting SQL alone does not make every pocket operation part of
the host's transaction. The connector still supplies native transactions for
host-owned document operations.

**Index deployment.** Hosts merge and deploy the complete manifests before boot:
connector live-test shapes, authorization baseline and optional audit indexes,
and authentication indexes. Both composite indexes and `fieldOverrides` matter;
the workflow deploys and waits for them separately. Constructors probe their
required manifests through the Admin API. `WithoutIndexProbe()` transfers index
verification to the host and is required for the emulator, which cannot prove
composite coverage. Constructors never provision a database or deploy indexes.

**v0.1.0 release gate (owner exception).** Record all three modules' build/test/vet,
emulator integration race suites, live-tag compilation/vet, workflow helper
tests, the full `make check` gate and isolated candidate/public module checks. Audit all
emulator skips; only authorization's documented `TestRunTransactional` ambient
family may skip. Publish exact module versions and checksum evidence, with
`live_workflow_run` and `live_artifact` unset and the real GCP suite marked unrun.
Today's emulator race evidence may be reused for this promotion only after
proving runtime/test/index source identity; final pins and archives are rechecked.
This owner-directed release exception does not weaken the live workflow,
runtime index probes, transaction guards or conformance assertions.

**Outstanding real GCP verification.** The owner accepted this untested scope for
`v0.1.0`; it remains open after publication. To establish live coverage, record a
passing `live-stores` dispatch with `firestore_live_required: true`, the run ID and its
`firestore-live-evidence-<run id>-<attempt>` artifact. It must use a disposable,
run-owned named database, never `(default)`, with the full index manifests ready,
and run `-tags='integration,live' -run 'Live$'`. Missing configuration, missing
roots, failed tests and unexpected skips must fail the gate, including skips
nested under a passing parent. Only authorization's `TestRunTransactionalLive`
family may skip because ambient joins are unsupported. Connector and
authentication allow no skipped live cases. The archived JSON results, derived
expected roots, audit and index state are the release evidence. Emulator results
alone cannot establish real GCP coverage.

### pockets/authorization — v0.12.0 @ `1439407` (+ stores/pgx v0.7.0, stores/turso v0.6.0 @ `340f6f2`) — tagged 2026-09-09: `FilterAuthorized` decides the candidate SET in one evaluation (minor; BREAKING store port; no schema)

Plan of record `plans/authorization-batch-decision.md` (originating host
segovia v2, `v2-tenancy.md` leg 6.3 / O13: the owner's ruling "upstream should
support batch lookups on B" after a DENIED check was measured at ~60–80 ms
against pgx, making a sparse container page of 300 candidates take ~18 s). ONE
train: core minor + both store minors together, because the point of the change
IS the store port (ruling R1). No migration — the `0005` keyset index from
`authorization-lookup-paging` already serves the new reads. `CheckBatch` is
deliberately untouched (ruling R2).

**What changed — decision surface (no API change).**

- `FilterAuthorized(principal, permission, type, ids)` — and therefore every
  `FilterPage` pull — is now ONE set evaluation of the permission instead of N
  `Check`s. The engine walks the permission tree once, level by level: each
  `Direct` branch is a single store read over the candidates still undecided (an
  admitted candidate leaves the set — the `AnyOf` short-circuit, per candidate),
  and each `Through` hop is a single read whose DISTINCT targets are decided the
  same way one level down. Reads are `O(branches + hops)` for the whole set: the
  shipped benchmark reports **9 reads at N = 50 and at N = 500** for a five-branch
  permission with one userset and two `Through` hops where every candidate is
  denied, against `6N + 3` (303 and 1803) for the same work through `CheckBatch`.
- Answers are unchanged: the result is the least fixpoint of the schema's rules
  over the tuples, which is exactly what a per-resource `Check` computes (its
  path-local cycle rule IS the least-fixpoint rule). A property test over random
  schemas, tuple sets and candidate sets pins `FilterAuthorized(ids)` ==
  `{id : Check(id).Allowed}`. Output order and multiplicity are unchanged: the
  caller's `ids` filtered in place.
- Answers are also unchanged for the shape that used to be subtle: a candidate
  set containing BOTH a resource and its ancestor. The set walk is a fixpoint,
  not a depth-first path, so the ancestor is another state whose grant
  propagates — never an "in progress" frame that denies its own descendant.
- **One behavior consequence to size for.** The set evaluation is ONE decision
  and carries ONE budget. `MaxThroughDepth`, `MaxRelationTargets` and the
  group-expansion bound are charged exactly as `Check` charges them, but
  `MaxGraphStates` is charged ONCE for the set rather than once per candidate. A
  very wide candidate set over a very deep graph can therefore exhaust it where
  each candidate alone would not; exhaustion is `ErrEvaluationLimit`
  (indeterminate, HTTP 503, fail closed) for the whole call, exactly as
  `CheckBatch` has always reported one request's overflow for a whole batch —
  never a wrong answer and never a partial list presented as complete. Size
  `MaxGraphStates` above `MaxBatchSize` × the states one candidate's walk
  reaches. The roles kind's answer, `Unrestricted` included, is untouched.
- `CheckBatch` keeps its per-request semantics (it accepts mixed principals,
  permissions and resource types) and its v0.10.0 memoized reader.

**What changed — store port (BREAKING for third-party stores).**
`relationship.Storer` gains two SET reads beside their per-resource siblings:

```go
FilterRelation(ctx, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error)
RelationTargetsFor(ctx, resourceType string, resourceIDs []string, relation string) (map[string][]RelationTarget, error)
```

`FilterRelation` is the set form of `CheckRelationWithGroupExpansion`: DISTINCT,
byte-order sorted, a SUBSET of the input, same expansion bound and same
`ErrExpansionBudgetExceeded` overflow. `RelationTargetsFor` is the set form of
`GetRelationTargets`; an id with no targets is ABSENT from the map. Empty input
is an empty result and NO store call. The candidate set is bounded by the CALLER
(`MaxBatchSize`), so the port declares no id-count ceiling: a store whose dialect
bounds statement parameters CHUNKS internally and merges — it never rejects an id
list. A host with its own `relationship.Storer` must implement both before
upgrading; the bundled `memstore`, pgx and turso stores already do, and
`storetest`'s new `Relationship/SetReads` family proves every backend answers
them identically to N per-resource reads (subset/sorted/distinct, group
expansion, empty input, duplicate ids, and a list past the statement bound).

**stores/pgx.** Both reads bind the whole id set as ONE `text[]` parameter
(`resource_id = ANY(@resource_ids)`), so no chunking is needed and the `0005`
index `(resource_type, relation, resource_id COLLATE "C")` serves the equality
columns and the ordered output; `COLLATE "C"` is pinned per query exactly as the
keyset lookups pin it. **stores/turso.** libSQL binds each id positionally, so
both reads chunk at **500 ids per statement** and merge; the ids are sorted and
de-duplicated before chunking, so the chunks cover disjoint ascending ranges and
their concatenation is already the sorted, distinct answer. Chunking is invisible
in the result.

**Adoption.** Nothing to change: same call, same answers, same order. The
listings worth revisiting are the ones moved to prefilter only because
postfiltering was expensive — a manager's listing inside a huge tenant, a search
result, any host-ordered candidate stream whose visible set is not bounded. A
container listing whose visible set IS bounded should stay on the prefilter
(`LookupResourcesIn` + `WHERE id = ANY(...)`); see the amended choosing table in
`plans/authorization-lookup-paging.md`.

### pockets/authorization — v0.11.0 @ `4bd2363` (+ stores/pgx v0.6.0, stores/turso v0.5.0 @ `a78aab2`) — tagged 2026-09-08: `LookupResourcesIn` pages — keyset `After`/`NextCursor` over both kinds (minor; BREAKING store ports; host migration `0005`)

Plan of record `.claude/plans/authorization-lookup-paging.md` (#29, the
deferred half of #22; originating host segovia v2, `v2-tenancy.md` O6/D10),
tasks 1–4 — the SECOND train under ruling R4, on top of v0.10.0. A **minor**
by the repo's pre-1.0 convention that **breaks both store ports** and ships a
**required host migration**. Owner rulings 2026-09-08: R1 `MaxLookupResults`
stays 1000 (hosts with large open tenants raise it themselves); R2 cursors are
bound to the owning model digest on BOTH kinds; R5 `Truncated` stays one
release as a deprecated alias.

**What changed — decision surface.**

- `LookupRequest.After` + `LookupResult{HasMore, NextCursor}`. `Limit` is now
  a PAGE SIZE: 0 = `MaxLookupResults`, above it → `sdk.ErrInvalidInput`; it is
  never a total-results cap. Order is resource id ascending, BYTE order — opaque
  and stable, meaningless to a human; never sort a paged prefilter client-side
  and expect cross-page consistency. No snapshot across pages (standard keyset:
  a grant that lands ahead of the cursor is seen, one that lands behind it is
  missed; refetch from page one for a consistent view).
- `NextCursor` is base64url JSON `{v, id, fp}`; the fingerprint binds the
  owning kind, that kind's model digest, the principal, the permission, and the
  resource type. A cursor presented against any other query, the other kind,
  or a model that changed since (a deploy that edits the Schema or the
  RoleModel) is `ErrInvalidCursor` (wraps `sdk.ErrInvalidInput`, 400) — the
  client restarts from page one. `CompiledRoleModel.Digest()` is new and
  order-independent.
- **What the budget bounds now.** Top-level leaf reads are bounded by the page
  (every stream reads `after` + `limit+1`). A non-self `Through` hop's TARGET
  set and a self-hierarchy's ROOT set are still complete and bounded by
  `MaxLookupResults` — a principal who can view more containers than the budget
  gets `ErrEvaluationLimit` on EVERY page (the documented A3 cliff; the
  access-index follow-up is the structural fix). The roles kind pages straight
  off an indexed store lookup: no assignment scan, no `MaxGraphStates` charge.
- `LookupResult.Truncated` is **deprecated**: it carries `HasMore`'s value and
  is removed next minor. Plain `LookupResources` is unchanged in output, order,
  and budget.

**What changed — ports (BREAKING for third-party stores).**

- `relationship.Storer`: `LookupResourceIDs(…, subjectID, after string, limit
  int)` and `LookupResourceIDsByRelationTarget(…, targetIDs, after string, limit
  int)` return distinct ids sorted in BYTE order, strictly greater than `after`,
  at most `limit`; `LookupDescendantResourceIDs(ctx, resourceType, relations
  []string, subjectType, rootIDs, after, limit)` closes over the UNION of the
  self relations in one call. Byte order is contractual: the engine merges
  streams with Go string comparison, so a locale-collated order skips or
  repeats ids across pages.
- `role.Storer.LookupResourceIDsBySubjectAndRoles(ctx, subjectType,
  subjectID, resourceType, roles, after, limit) (ids, unrestricted, err)`.
- `storetest` proves both: `Relationship/LookupKeyset/*`,
  `Roles/RolesLookupKeyset/*`, `Parity/LookupPagedParity/*`,
  `Parity/RolesPagedParity/*`, `Composed/PagedPairOwnershipDispatch`.

**What changed — stores (host migration).**

- `migrations/0005_iam_lookup_keyset.sql` in BOTH ledgers:
  `idx_iam_relationships_type_relation_resource` on `(resource_type, relation,
  resource_id COLLATE "C")` (pgx; no COLLATE on turso — BINARY is byte order)
  and `idx_iam_roles_subject_resource_lookup` on `(subject_type, subject_id,
  resource_type, resource_id, role)`. **Hosts pin the ledger verbatim, so this
  file is a REQUIRED re-export** (segovia v2's `ledger_test` fails until it is
  copied). It is an ORDINARY transactional `CREATE INDEX IF NOT EXISTS` —
  `pgxdb.RunMigrations` applies the stream in one transaction, where
  `CONCURRENTLY` is impossible — so the build holds a SHARE lock (writes block,
  reads proceed) for its duration; schedule it, or create the two indexes
  `CONCURRENTLY` by hand first and let `IF NOT EXISTS` make the file a no-op.
- pgx pins `COLLATE "C"` per lookup query (`resource_id` is deliberately
  uncollated in 0001 because it is a recursion column of the expansion CTE) and
  projects the collated expression (PostgreSQL requires a `SELECT DISTINCT`
  ORDER BY expression in the select list). Measured at 1e6 rows on an
  `en_US.utf8` cluster (`TestLookupPlansAtScale`, env-gated): the role lookup
  is an Index Only Scan on the new index; `ByRelationTarget` over 200 broad
  containers is a BitmapAnd of the subject index and the new index; the
  descendant closure's cost is the closure, not the page.
- Full details, including the third-party-store obligations:
  `pockets/authorization/stores/UPGRADE.md`, "Store-port note (2026-09-08, core
  v0.11.0 …)".

**Verification (train).** Pocket hermetic + race green on both PRs; `make
guard` green; pgx live (postgres:17, `en_US.utf8`) default +
`POSTGRES_TEST_SCHEMA` legs green incl. `TestLookupPlansAtScale` at 1e6 rows;
turso playground full `TestConformance$` green (1145 s, its own invocation) and
every other live test green; v0.10.0, v0.11.0 and both store tags served by
the proxy on the first poll; all three modules cold-built and vetted
`GOWORK=off` from a fresh `GOMODCACHE`; the v0.10.0 core cold-tested too.
PR #43 (the stacked form of this train) was auto-closed when #42's branch was
deleted at merge and reopened as #45 on the same commits.

**Pin moves.** Both store modules: `pockets/authorization v0.9.0 → v0.11.0`
(through v0.10.0). `sdk v0.7.0` unchanged.

**Adoption.**

- segovia v2 leg 6 (home, D10a): `LookupResourcesIn` per item type with
  `Limit: 50`, cursor per section; set `Limits.MaxLookupResults: 5000` (R1);
  re-export `0005` into the ledger; add the overflow case to the verify list.
- gps-360-go `/client-hubs` (the #22 origin): `After` replaces the host-side
  cap of 50; re-export `0005`.
- Coordination Hub `myCampaigns`: `After` once it moves onto v0.11+.
- Any host implementing `relationship.Storer` / `role.Storer` itself: add the
  parameters and the role lookup, honor byte order, run `storetest.Run`.

### pockets/authorization — v0.10.0 @ `162d24e` — tagged 2026-09-08: `FilterPage`, the postfilter page-filler, and the memoized batch reader (minor; core-only)

Plan of record `.claude/plans/authorization-lookup-paging.md` (#29, the
deferred half of #22; originating host segovia v2, `v2-tenancy.md` O6/D10),
tasks 5–6 — the FIRST of two trains under ruling R4. A **minor**: purely
additive, no store port change, store modules NOT retagged, `sdk v0.7.0` pin
unchanged.

**Additions.**

- `FilterPage[T](ctx, *Service, FilterPageRequest[T]) (crud.Page[T], error)` —
  the DENSE-and-bounded enumeration path: pulls candidate pages from a host
  `CandidateSource[T]` (one `Candidate.NextCursor` PER ROW, the source's own
  keyset cursor after that row), decides each pull with ONE `FilterAuthorized`
  call, and fills one authorized page in the host's order. Continuation = the
  cursor after the last candidate consumed, including a mid-batch stop. A
  source that returns more than asked, reports `HasMore` with no rows, returns
  an empty or non-advancing cursor, or repeats a cursor within one call is
  refused with `sdk.ErrInvalidInput` (400). `Limit` 0 is `crud.DefaultLimit`
  (this IS a page, unlike `LookupResourcesIn`); negative or above
  `MaxBatchSize` is `sdk.ErrInvalidInput`. Items is always non-nil.
- `EvaluationLimits.MaxFilterScan` (default `DefaultMaxFilterScan` = 20 000 =
  20 × `DefaultMaxBatchSize`): every source pull is clamped to
  `min(2×Limit, MaxBatchSize, remaining MaxFilterScan)`, so one call never
  scans more. Reaching the bound with the page unfilled returns the PARTIAL
  page plus a continuation — the one budget dimension whose exhaustion is a
  page, not `ErrEvaluationLimit`, because the cursor makes it resumable.
  Consequence hosts must render for: `Page.HasMore` means UNSCANNED CANDIDATES
  REMAIN, not that another authorized row exists — following a `HasMore` page
  may yield a final empty page.
- **B4 — the memoized batch reader (no API change).** `CheckBatch` /
  `FilterAuthorized` over a permission with any `Through` check (or a
  heterogeneous batch) still evaluates each request with its OWN fresh budget,
  but over ONE batch-local reader that memoizes successful exact
  `GetRelationTargets` / `CheckRelationWithGroupExpansion` reads. A container's
  500 items now read the shared container's targets and run its direct check
  once. Results and every `ErrEvaluationLimit` are identical to sequential
  `Check`; it never enumerates globally, so a bounded container listing cannot
  fail on a principal whose total accessible set exceeds `MaxLookupResults`.

**Adoption.** segovia v2 container listings (`/spaces/{id}/spaces`,
`/spaces/{id}/dashboards`, `/tenants/{id}/spaces`): replace the unbounded
`FilterAuthorized` over the whole container with `FilterPage` over the
tenancy/dashboards stores' keyset queries (per-row cursors). "Everything of
type X I may see" stays on the prefilter — paged on the next tag.

### pockets/authentication — v0.10.0 @ `62e69f4` — tagged 2026-09-08: `Optional()`, the pass-by-absence posture on `RequirePrincipal` (minor; additive)

Plan of record `plans/authentication-optional-principal.md` (originating host
segovia v2 tenancy D11 / leg 6b, the anonymous public read). `sdk` untouched;
store modules untouched; no schema. PR #44.

**One more `PrincipalOption`:**

```go
Optional()  // no credential ⇒ continue with NO principal; a presented credential still resolves and still denies when bad
```

At the OUTERMOST position, no credential presented within the set continues the
request with no principal and no credential stashed (`CurrentPrincipal` /
`CurrentCredential` report false), skipping the `Live()` tier and the `Browser()`
redirect. A credential presented within the set is resolved exactly as before and
**still denies** when invalid, expired, revoked, or of a kind outside the set — a
stale cookie is a 401, never an anonymous pass. "Presented" follows the resolver's
own transport rules: a credential on a transport outside the set is not read, so
an anonymous pass there is correct. Nested: an inner gate under an anonymous outer
finds no stash and resolves the same request itself — a required inner denies, an
optional inner passes; an optional inner under a required outer narrows as usual.

**Adoption.** `RequirePrincipal(Optional())` on a surface that serves both
principals (ReBAC decides) and anonymous visitors (a per-resource public
attribute decides); the handler reads `CurrentPrincipal` and branches on
absence. No existing posture changes; no host code needs to move.


### pockets/authorization — v0.9.0 @ `495787c` (+ stores/pgx v0.5.0, stores/turso v0.4.0 @ `a5ca592`) — tagged 2026-09-08: baseline writes join the ambient `crud.Transactor` transaction (minor; store behavior change; guarded path refuses inside a transaction)

Plan of record `.claude/plans/authorization-stores-ambient-transaction.md`
(originating host segovia v2, `v2-tenancy.md` D15). A **minor** by the repo's
pre-1.0 convention: no signature changes, but the store ports gain a contract
paragraph every store author must honor and `storetest` gains an exported
entry point. Owner rulings 2026-09-08: this version line; roles IN; the guarded
path refuses.

**What changed.**

- **Both SQL stores honor the connector's ambient transaction.** When the
  context handed to ANY `relationship.Storer` or `role.Storer` method carries
  the transaction a `(*pgxdb.DB).Transact` / `(*tursodb.DB).Transact` call
  stashed there, the method runs ON it — `s.db.QuerierFrom(ctx)` for every
  statement, helper argument, connector `List`, and `ExecAffecting`. Reads too:
  a host that reads, decides, and writes inside one transaction sees its own
  uncommitted state. `SetRelationTargets` dispatches on `TxFromContext`: ambient
  → the reconciliation body over the host's `*Tx` (no begin, no commit, no
  rollback); standalone → its own transaction exactly as before.
- **Host obligations.** Return write errors from the `Transact` callback — the
  store never rolls back or marks the transaction rollback-only, so a swallowed
  `sdk.ErrConflict` from `SetRelationTargets` followed by `return nil` commits
  the earlier host work without the tuple. On PostgreSQL the conflict probe is a
  successful SELECT and does not abort the transaction; a genuinely failed
  statement does (25P02 until rollback) — return it.
- **pgx: the advisory lock widens to the host's commit.** `pg_advisory_xact_lock`
  ends with the transaction it was taken in; inside an ambient one that is the
  host's commit, so a competing `SetRelationTargets` on the same key waits for
  the whole workflow (the property D15 wants). Do slow work before the write.
  Documented, not mitigated; the live test proves the wait is bounded by the
  commit, not leaked.
- **turso: no busy retry on the ambient path.** `retryBusy` re-runs the whole
  transaction, which is only sound when the store owns it; the host's `BEGIN
  IMMEDIATE` already holds the write lock, so `SQLITE_BUSY` is not expected and
  is returned as-is if it surfaces.
- **Guarded mutations refuse inside an ambient transaction (D5).**
  `Apply`/`ApplyGuarded` — therefore every `Service` mutation method and
  `SystemMutator`, protected teardown included — return
  `authorization.ErrGuardedInsideTransaction` with a nil receipt when the
  context carries the ambient transaction, BEFORE any guard, validator, lock, or
  row. Previously the guarded path silently ran on its own connection. No known
  host does this (the Hub writes no tuples; segovia's only guarded call, the
  `tenant#owner` seed, runs outside `Transact`). If yours does: move the guarded
  call outside `Transact`, or file for the joining design.
- **`storetest.RunTransactional(t, newRepos func(t) (authorization.Repositories, crud.Transactor))`**
  — the new family; a nil transactor skips it loudly (memstore). Third-party
  stores must pass it. Each bundled SQL store additionally asserts through SQL
  that a refused guarded mutation left `iam_scopes`/`iam_mutations` untouched.
- **Pins.** `stores/pgx` and `stores/turso` pin `pockets/authorization v0.9.0`;
  `stores/turso` moves `integrations/datastores/turso v0.1.0 → v0.3.0` (v0.1.0
  predates `Transact`/`QuerierFrom`). **No database migration**: no schema,
  table, or column changes.

**Adopting.** Repin the core and your store module together. A host that
wants the join wraps row write + `RelationshipWriter` call in its connector's
`Transact` and passes the callback's `ctx` to both — the same `*pgxdb.DB` must
back the host repositories and the authorization store (two pools over one DSN
never share a transaction). The memstore ignores the context (mutex-atomic per
operation) — wire a SQL store for the join.

**Verification (train).** Pocket hermetic green; pgx live (postgres:17)
default + `POSTGRES_TEST_SCHEMA` legs green incl. the new family and
`TestAmbientRowAndTupleCommitTogether`; turso live playground: `TestConformance`
green (1466s, run as its own invocation), `TestTransactional` + every other live
test green, `TestSchemaProbe` = the known playground constraint gap; `make
guard` 22/22; store modules cold-built `GOWORK=off` from a fresh `GOMODCACHE`
against the tags.

### pockets/authorization — v0.8.0 @ `3c446c0` (+ stores/pgx v0.4.0, stores/turso v0.3.0 @ `9e5a713`) — tagged 2026-09-08: `DecisionView.CheckPermission` — the hierarchy walk inside the guarded mutation (minor, breaking store port)

Plan of record `.claude/plans/authorization-decisionview-permission.md`
(originating host segovia v2, `v2-tenancy.md` D13). A **minor** by the repo's
pre-1.0 convention that **breaks the mutation store port** for anyone who
implements `mutation.DecisionView` or hands a `mutation.Guard` closure to
`ApplyGuarded` directly. A host guard's `AuthorizeMutation(ctx, attempt, view
authorization.DecisionView)` compiles unchanged and gains a method.

**What changed.** Before, the `DecisionView` a `MutationGuard` received had ONE
relationship read, `CheckRelation`: a direct-tuple test with userset expansion
on one scope. In any schema where authority is inherited from a container
(`dashboard.manage = owner | Through(space, manage)`), the actor who manages
the container holds no direct tuple on the item and the guard could not see
their authority; the host's only options were to re-walk the ancestor chain in
its own code (duplicating the schema) or to drop to a detached `Service.Check`
(reopening the check-then-write race). Now the traversal lives in the engine,
once:

- `authorizersvc.PermissionReader` — the exact two-method subset the walk reads
  (`CheckRelationWithGroupExpansion`, `GetRelationTargets`); the per-decision
  budget carries the reader; `Service.EvaluateWith(ctx, reader, req)` runs the
  ordinary evaluation over any reader. No read-side behavior change.
- `mutation.StoreDecisionView` — what a **store** implements: `CheckRelation`
  (unbounded, now `CheckRelationBounded(…, 0)`), **`CheckRelationBounded`**
  (the read-side expansion accounting; overflow is
  `relationship.ErrExpansionBudgetExceeded`, never an allow or a truncated
  deny), **`RelationTargets`** (a Through hop's edge read, revision recorded
  BEFORE the rows are read), `HasRole`, `Dependencies`.
- `mutation.DecisionView` — what a **guard** receives: embeds
  `StoreDecisionView` and adds **`CheckPermission(ctx, scope, permission,
  principalType, principalID)`**: the SAME decision the read-side `Check`
  makes, evaluated inside the mutation transaction, every navigated scope a
  dependency. Budget exhaustion is `ErrEvaluationLimit` (a command error, the
  mutation writes nothing), never a deny.
- `mutation.Guard` is now `func(ctx, view StoreDecisionView) error`. The core's
  `composeGuard` wraps the store view in the permission view before calling
  the host `MutationGuard`.
- `authorization.StoreDecisionView` alias; `ErrPermissionOwnedByRoles`
  (wrapping `sdk.ErrInvalidInput`): `CheckPermission` on a pair the
  `RoleModel` declares is refused before any store read, in mixed-model and
  roles-only deployments alike — the guard uses `HasRole` for those. A
  non-resource scope is `ErrInvalidRequest`.
- **Snapshot fix in the legacy primitive (pgx, turso).** `CheckRelation` ran
  the EXISTS query in one statement and re-ran the reachable CTE in a second
  to record expansion scopes; under READ COMMITTED a membership revoke
  committing between them paired a pre-revoke allow with post-revoke
  revisions, and validation accepted it. Both primitives now obtain the grant
  match, the reached scopes, and their revisions from ONE statement (the
  `recordExpansionScopes` helpers are gone). Deterministic pgx interleaving
  tests (`stores/pgx/mutations_permission_live_test.go`) prove a parent edge
  revoked after the target read, and an inherited grant revoked after the
  check, both abort as `ErrStaleRevision` with no row, receipt, or revision
  bump.

**Who must change.**

- **Third-party store authors** (none known): implement `CheckRelationBounded`
  and `RelationTargets` on your view and satisfy `StoreDecisionView`; the
  repository callback now receives `StoreDecisionView`. Record a scope's
  revision BEFORE reading its rows, or read both in one statement snapshot; a
  scope already recorded keeps its first revision. The storetest suite
  (`Mutations/GuardedPermission*`) is the conformance oracle.
- **Host tests with a hand-rolled `DecisionView` fake**: add the two methods
  (a stub that records the scope and returns nothing is enough), and retype a
  `mutation.Guard` closure's parameter to `StoreDecisionView`. A fake handed to
  a host guard as `authorization.DecisionView` also needs `CheckPermission`.
- **Host guards**: nothing. Adopt `view.CheckPermission` where the guard was
  re-walking the schema or calling a detached `Check`.

**Pin move.** Stores: `pockets/authorization v0.6.0 → v0.8.0` (through v0.7.0's
additive gate/lookup/role-routes tag; no store symbol depends on them). Core
`go.mod` unchanged (`sdk v0.7.0`).

**Adoption.** segovia v2 leg 3: the host guard becomes platform-admin
short-circuit, the change-shape matrix, then `view.CheckPermission(scope,
manage | manage_access, actor)`.

### pockets/jobs — v0.5.0 @ `eac19a0` (+ stores/pgx v0.5.0, stores/turso v0.4.0 @ `de453fb`) — tagged 2026-09-04: Claim and the scheduler pool filter by registered kinds (minor, breaking ports)

Plan of record `.claude/plans/jobs-kind-filtered-claim.md` (gopernicus #37;
originating host gps-360-go). A **minor** by the repo's pre-1.0 convention that
**breaks the three repository ports** for anyone who implements them.

**What changed.** Before, a worker polling a shared queue claimed the oldest
due job of ANY kind, failed it "no handler registered for kind", and — at
`MaxAttempts` — dead-lettered it; the scheduler pool fired EVERY due schedule
row, then the claim half destroyed the fired jobs when the owning worker was
down. N binaries sharing one queue all had to be up for any to be healthy.
Now `Claim` (unfenced and fenced) takes the runtime's registered `kinds` —
applied to the pending arm AND the stale-reclaim arm — `ListDue` takes them
too, and `ClaimDue` re-asserts the listed schedule's kind in its CAS (an
`Ensure` can re-kind a row without moving `next_run_at`). A job or schedule
of a kind this binary does not handle **waits** for the binary that does:
pending, zero attempts charged, never dead-lettered by a foreign worker.

**Adopter action.**

- **Hand-written repository doubles** (test fakes, host-owned stores): add
  the trailing `kinds []string` parameter to `Claim` (both ports) and
  `ListDue`, and `expectedKind string` to `ClaimDue`. `nil` kinds = no
  filter; a non-empty `expectedKind` must match the row's kind for the CAS to
  win. The compile error is the whole migration.
- **Hosts running ONE worker binary with every kind registered**: no
  behavior change beyond one INFO line per runtime (`"jobs runtime: claiming
  kinds"`) and a `kinds` attr on `Register`'s line.
- **Hosts running SEVERAL worker binaries over one queue** (gps-360-go's two
  ingest workers): they stop dead-lettering each other's jobs and firing each
  other's schedules — retire any deploy-together stopgap. Note the trade: a
  kind nobody registers now waits silently instead of failing loudly; watch
  the start-up `kinds` line and query pending-by-kind via
  `Queue.List(ListFilter{Kind, Status: pending})`. A stale-pending report is
  a filed follow-up, not in this train.
- **Repin both**: `pockets/jobs` and your `stores/{pgx,turso}` move together
  (the stores' `Claim`/`ListDue`/`ClaimDue` statements carry the filter; the
  old store tags do not satisfy the new port). No migration, no new index.

### sdk — v0.8.0 — tagged 2026-09-03 @ `8564703`: secrets, the dotenv comment rule, and ParseEnvTags that hosts can actually adopt (minor)

Plan of record `.claude/plans/environment-secret-and-dotenv-gaps.md`
(gopernicus #34; originating hosts gps-360-go and segovia v2). A **minor**:
four additive surfaces and one parser semantic in `foundation/environment` +
`foundation/web`. Read all five items before repinning.

**(a) `Secret(key, minBytes)` / `DecodeSecret(value, minBytes)`.** Hex-only.
Unset OR empty → `nil, nil` (deliberately indistinguishable) so the host keeps
its own "ephemeral in development, required in production" branch; a decode
failure wraps `ErrSecretEncoding`, a short key wraps `ErrSecretUnderMinimum`,
and `Secret` prefixes every error with the key name. `minBytes` is a floor,
not the authority — `cryptids.NewAESGCM` still requires exactly 32. Hosts may
delete their `hexKeyEnv` / `secretKeys.key` decode+length lines. A key read as
raw ASCII today (the `examples/auth-cms` encrypter keys, its and gps-360-go's
`AUTH_JWT_SECRET`) gets `ErrSecretEncoding` at boot if moved to `Secret`
without re-encoding — move the value to hex when you move the read.

**(b) KEY-ROTATION ADVISORY.** Before this tag, `LoadPath` turned
`AUTH_JWT_SECRET=   # >=32 bytes; …` (the shape in `examples/auth-cms/.env.example`
and any template copied from it) into the value `# >=32 bytes; …`. A host that
reads that key as ASCII (`cryptids.NewHS256([]byte(secret))` — `examples/auth-cms`
and gps-360-go do) accepted the 55-byte comment as its signing key with no
warning: the EPHEMERAL-key WARN never fired and the host ran on publicly
committed key material. Hex-read keys and exactly-32 AES keys failed loudly;
the JWT key is the one that succeeded silently. If any environment was ever
booted from such a `.env`, rotate `AUTH_JWT_SECRET` at repin.

**(c) `LoadPath`: the dotenv inline-comment rule.** An unquoted `#` opens a
comment only when the byte before it is a space or tab, evaluated on the raw
text after `=` (the old code trimmed first, so `KEY=   # note` had no leading
space left and kept `# note`). `KEY=   # note` and `KEY=\t# note` → `""` (the
key is SET, empty); `KEY=#alerts`, `KEY=#ff0000` are unchanged; a quoted value
is never comment-stripped. Consequence: a `KEY=   # hint` line that used to
yield a loud hex/DSN error now yields empty — an operator debugging "why is my
key unset" should look for whitespace-then-`#` on the line — and a value that
legitimately contains ` #` must be quoted (`PASS="p4ss #1"`); today it was
already truncated at the ` #`, so nothing that works today breaks.

**(d) `ParseEnvTags` descends into nested structs.** An exported, settable
struct-kind field WITHOUT an `env:` tag is recursed under the same namespace
(no prefix); a tagged struct field is still an error; pointer-to-struct and
interface fields are skipped; `time.Time` recurses to a no-op. A host whose
parent struct already embeds a tagged struct as a field (`otel.Config`,
`pgxdb.Config`, …) will see those tags applied where they were silently
ignored before — check for that shape before repinning.

**(e) `ParseEnvTags` treats an empty value as not provided — for every kind.**
`KEY=` now behaves exactly like an unset key: `required:"true"` errors, a zero
field takes its `default:`, a pre-seeded field keeps its value. Five of six
kinds already ignored empty; `string` was the exception and wrote `""`. A host
that relied on `KEY=` to blank a defaulted string must set the field in code.
`GetEnvOrDefault` is UNCHANGED (raw `LookupEnv`: `KEY=` is set-and-empty) —
the two now disagree about `KEY=`, so read your config through one of them.

**(f) `web.ServerConfig`: `TrustedProxyCount` (`TRUSTED_PROXY_COUNT`),
`PublicBaseURL` (`PUBLIC_BASE_URL`), and `Origin()`.** `HTTPServer` ignores the
two new fields; pass `cfg.TrustedProxyCount` to `TrustProxies` and derive
callback bases, allowed-origin fallbacks, and magic-link bases from
`Origin()` (`PublicBaseURL` with trailing slashes trimmed, else
`http://HOST:PORT` with `localhost` substituted for an empty or `0.0.0.0`
host). `PublicBaseURL` must be scheme-qualified and path-free; `Origin()`
documents that, it does not validate it. Every `ServerConfig` literal in this
repo is keyed; a positional literal downstream would break.

**Adopter shape — pre-seed your own defaults, then parse; env wins, `KEY=`
keeps the seed:**

```go
logOpts := logging.Options{Format: "text"}
if err := environment.ParseEnvTags("", &logOpts); err != nil { return err }
srv := web.ServerConfig{Port: "8082"}
if err := environment.ParseEnvTags("", &srv); err != nil { return err }
router.Use(web.TrustProxies(srv.TrustedProxyCount), …)
cfg := auth.Config{ /* ports… */, PublicAuthBaseURL: srv.Origin() + "/auth/magic" }
if err := environment.ParseEnvTags("", &cfg); err != nil { return err }
```

Hosts may delete `serverConfig()`, the three `GetEnvOrDefault` logging lines,
`trustedProxyCount()`, `publicBaseURL()`, and the `JOBS_*` / `AUTH_*`
hand-parsers once the pocket tags below are pinned. Two semantic shifts to
expect when `authentication.Config` starts going through `ParseEnvTags`: its
thirteen pre-existing tags become live (`AUTH_RUNTIME_MODE` from the
environment now overrides a hard-coded posture; an unknown `AUTH_DELIVERY_MODE`
is the pocket's loud construction error instead of a host fallback), and list
values keep empty entries (`a,,b` is three).

### pockets/jobs v0.4.2 · events v0.2.1 · cms v0.2.1 · authentication v0.9.1 — tagged 2026-09-03 @ `8564703`: env tags on the Configs (patch; tags only)

Same plan of record. Tags only — no field, behavior, schema, store, or pin
change; a struct-literal host is byte-identical. Keys: `jobs.Config` —
`JOBS_WORKERS`, `JOBS_POLL_INTERVAL`, `JOBS_IDLE_INTERVAL`, `JOBS_MAX_ATTEMPTS`,
`JOBS_SCHEDULE_BATCH`, `JOBS_HEARTBEAT_INTERVAL`; `jobs.FencedRuntimeConfig` —
the shared `JOBS_WORKERS`/`JOBS_POLL_INTERVAL`/`JOBS_IDLE_INTERVAL`/`JOBS_MAX_ATTEMPTS`
plus `JOBS_LEASE_FOR`, `JOBS_PROCESS_TIMEOUT` (a host running both runtimes in
one process disambiguates with the namespace argument: `ParseEnvTags("FENCED",
&cfg)` → `FENCED_JOBS_WORKERS`; `JOBS_PROCESS_TIMEOUT` without a covering
`JOBS_LEASE_FOR` trips the existing `ErrProcessTimeoutExceedsLease`, loudly);
`events.Config` — `EVENTS_HEARTBEAT`, `EVENTS_BUFFER_SIZE`, `EVENTS_MAX_CONN_AGE`,
`EVENTS_MAX_CONNS_PER_SUBJECT`; `cms.Config` — `CMS_MAIL_FROM`,
`CMS_CONTACT_TO`; `authentication.Config` — `AUTH_MAIL_FROM`,
`AUTH_ALLOWED_ORIGINS`, `AUTH_OAUTH_CALLBACK_BASE`, `AUTH_REDIRECT_ALLOWLIST`,
`AUTH_REQUIRE_VERIFIED_EMAIL`, `AUTH_PASSWORDLESS`; `CookieConfig` —
`AUTH_COOKIE_NAME`, `AUTH_COOKIE_PATH`, `AUTH_COOKIE_DOMAIN`, `AUTH_COOKIE_SECURE`,
`AUTH_COOKIE_MAX_AGE` (nested: needs the host's sdk ≥ v0.8.0; the pockets' own
sdk pins do not move — MVS takes the host's). No `default:` tags anywhere: zero
keeps every documented `0 → default` contract. Not retired by tags:
gps-360-go's `JOBS_HANDLER_TIMEOUT` (a host-side handler wrapper, no Config
field). `authorization.Config` is untagged (its scalars live in the nested
`EvaluationLimits`; gopernicus #35 tracks it with the other follow-ups —
example hosts onto `Secret` with hex keys, and the example env templates
documenting the newly readable keys).

### workshop/gopernicus — v0.2.1 — tagged 2026-09-03 @ `8564703`: the scaffold reads its config the sdk way (patch)

Same plan of record. `gopernicus init` now emits a `main.go` whose logger and
server config come from `ParseEnvTags` over pre-seeded literals (built inside
`run()` so a parse error is returned; `main()` reports it through the default
`slog`), a `.env.example` with every hint on its own line and the two-line
`KEY=`-keeps-the-default / quote-` #` header, three optional timeout keys, and
a `go.mod` pinning `sdk v0.8.0` / `pgxdb v0.6.1` / `turso v0.3.0`. A
render-only test pins the header, the no-inline-hint rule, and the sdk pin.
Re-init a host with `go install …/workshop/gopernicus@v0.2.1` only after
`sdk/v0.8.0` resolves on the proxy.

### pockets/jobs — v0.4.1 — tagged 2026-09-01: Config.Heartbeat, the pool heartbeat passthrough (patch)

Plan of record `.claude/plans/jobs-heartbeat-passthrough.md`. A **patch**: one
additive field. `jobs.Config.Heartbeat time.Duration` reaches
`workers.WithHeartbeat` on both runtime pools; 0 (the default) keeps the
heartbeat off, so no existing host changes behavior. Adopt by setting it at
NewService and alerting on the ABSENCE of the "pool alive" line; sdk v0.7.1's
Debug "iteration: no work" line comes with the repin regardless. Pin moves
sdk v0.5.0 → v0.7.1; store modules do not retag (no repository contract
change). Proof: `TestHeartbeatReachesThePools` — an idle pool with a heartbeat
set must beat.

### sdk — v0.7.1 (tagged 2026-09-01 @ `418414f`): the worker pool proves it is idle, not wedged (patch)

Plan of record `.claude/plans/workers-idle-observability.md` (gopernicus #32;
originating host gps-360-go `cmd/workers/io`). A **patch**: two additive
observability seams in `foundation/workers`, both zero-value-preserving. A
workless pool used to emit nothing after its `worker started` lines — an empty
claim (`ErrNoWork`) was swallowed in `Pool.worker` at every level, and the
`Stats` the pool already tracks printed exactly once, at shutdown.

**Addition 1 — a Debug line per idle iteration.** `Pool.worker` now logs
`"iteration: no work"` at **Debug** with `pool` and `worker_id` whenever the
WorkFunc returns `ErrNoWork`. Free in production; `LOG_LEVEL=DEBUG` makes a
worker chatty exactly when someone is staring at it. It fires whether or not
the heartbeat is enabled. This is the one change visible without opting in, and
only to a host whose logger enables Debug.

**Addition 2 — `WithHeartbeat(interval time.Duration) PoolOption`.** One INFO
`"pool alive"` line per interval with `pool`, `active_workers`, and the
`iterations` / `claims` / `errors` **deltas** since the previous beat. It beats
even when every delta is zero — an all-zero beat from an idle pool is the whole
point, so a missing heartbeat is the alarm rather than silence being the steady
state. First beat lands one full interval after start (the existing
`worker pool starting` line covers boot); the heartbeat stops at cancellation
and deliberately does not beat during the drain. `interval <= 0` disables it
and **is the default** — unlike `WithPollInterval`/`WithIdleInterval` there is
no fallback clamp, because an unset value has to keep meaning "no heartbeat"
for existing hosts. There is no minimum clamp either; a sub-second interval is
operator noise, not an error.

**Adopter action: none required.** The zero value is off, so every existing
host behaves exactly as it did. To adopt, pass the option at pool
construction and alert on the ABSENCE of the line:

```go
pool := workers.NewPool(runner.WorkFunc,
    workers.WithWorkerCount(4),
    workers.WithHeartbeat(time.Minute),
)
```

**Vocabulary.** `claims` counts WorkFunc calls that returned `nil` — for
`Runner.WorkFunc`, the package's intended adapter, that means a job was claimed
and handled. `ErrNoWork`, ordinary errors, the shutdown sentinels, and
recovered panics never count as claims; recovered panics already count into
`errors` through the returned error, so there is no separate panic attr.

Not on this tag: **no `Stats` change** — the claims counter stays private to the
pool (an exported field would break external positional composite literals and
would alter the shutdown line even with the heartbeat off), and cumulative
totals remain where they are today, on `Stats()` and the shutdown line. No
per-iteration INFO logging (four workers polling every 5–30s would drown the
lines that matter), no metrics export (expvar/Prometheus/OTel), no `Runner` or
fenced-runtime change, no parity change in `foundation/async`, and no cadence or
drain-contract change. `sdk`'s `go.mod` still has no require block.

### pockets/jobs — v0.4.0 — tagged 2026-08-31 @ `3107dce`: unfenced graceful drain honors the documented contract (minor)

Plan of record `.claude/plans/jobs-graceful-drain.md` (originating host
gps-360-go plans/40). A **minor**: a behavioral fix toward the README's own
drain contract ("in-flight handlers finish and persist"); no API change, no
schema, no store retags.

**What changed.** `Runtime.Run` used to pass the canceled pool context through
an already-claimed handler and into `Complete`/`Fail` — a context-aware handler
stopped mid-work and the terminal write failed on the dead context, leaving the
row `running` until lease recovery re-ran it. Now the unfenced queue and
scheduler iterations are wrapped in `context.WithoutCancel` (values, including
the worker id, survive); `workers.Pool` still checks cancellation before each
iteration, so shutdown still stops new claims. The **fenced runtime is
unchanged**: its cancellation intentionally abandons processing and leaves the
lease reclaimable.

**Adopter action.** A detached iteration no longer dies with the process
context, so bound handler work yourself: wrap registered handlers in a timeout
**strictly below the queue lease** (default 15m), or a hung handler can drain
past the lease and be reclaimed concurrently. Reference: gps-360-go wraps every
handler at a configurable 10m, boot-refusing a value at or above the lease. The
regression proof is `internal/logic/runtime/runtime_test.go`'s
cancellation-during-handler test.

### pockets/authorization — v0.7.0 — tagged 2026-08-31 @ `e95c82b`: the disjunction gate, the bounded lookup, and role administration comes standard (minor)

Plans of record `.claude/plans/authorization-gates-and-lookup.md` (#19, #22)
and `.claude/plans/authorization-role-routes.md` (#20). A **minor**: purely
additive symbols, one pin move, one new boot WARN.

**Additions — decision surface.**

- `RequireAnyPermission(alternatives …GateSpec)` on `*Service`: the route line
  states its own inclusive OR. Every (resourceType, permission) pair is
  validated against the compiled model at REGISTRATION; evaluation
  short-circuits on the first allow; a `Check` error or a non-sentinel resolver
  error fails the WHOLE request closed; the shared 401/403/500/503 gate body
  answers. `ErrAlternativeNotApplicable` is the one deliberate skip: a
  `ResourceResolver` returning it marks its alternative "does not apply to this
  request" and evaluation moves on as it would after a deny. The sentinel wraps
  no sdk taxonomy kind and never reaches a response body.
- `EvaluationLimits.MaxBatchSize` now ALSO caps the alternatives on one gate —
  a host that tunes it down to 1 makes any two-alternative gate panic at mount.
  The field doc names all three consumers.
- `LookupResourcesIn(ctx, LookupRequest)` — struct-input sibling of
  `LookupResources` with a caller `Limit` (0 = the `MaxLookupResults` ceiling,
  today's behavior); `LookupResult` gains `Truncated`. The budget is untouched:
  enumeration is still bounded by `MaxLookupResults` and still answers
  `ErrEvaluationLimit` on overflow, whatever the Limit. Negative Limit wraps
  `sdk.ErrInvalidInput`. The continuation cursor is deliberately NOT on this
  tag (deterministic-ordering design, follow-up issue).

**Additions — bundled role-administration routes (#6 precedent).**

- Mounted by `Register` ONLY when `Config.RoleRoutesGate` (one
  `web.Middleware`; the host pre-composes its whole stack — authenticator,
  CSRF where cookies are in play, steward check) is non-nil: `POST
  /authorization/roles`, `POST /authorization/roles/unassign`, `GET
  /authorization/roles/{by-subject,by-resource,effective}`. Nil gate = not
  mounted; a host with the roles kind + a `Guard` and no gate gets a boot WARN
  — the one visible upgrade-time change.
- `Config.AssignmentPolicy func(ctx, AssignRoleCommand) error` runs before
  every bundled assign (assign-only by ruling); an error refuses with the sdk
  error shape. Actor derives from the sdk `identity.FromContext` principal;
  missing principal → 401.
- Error bodies carry the pocket's STABLE codes (`stale_revision`,
  `mutation_payload_mismatch`, …) via the root `RespondError` — fixed before
  this tag so the wire never has to break to gain them.
- Browser hosts: the bundled writes are JSON-only (415 to any form-encoded
  request), which together with `SameSite=Lax` session cookies is the current
  CSRF posture — it is NOT an origin check and lapses under a credentialed
  CORS allowlist; compose a CSRF layer into the gate for cookie-authenticated
  browser use (see the pocket README).

**Pin move.** `sdk v0.5.0 → v0.7.0` (through v0.6.0's list contract and
v0.7.0's write vocabulary; no authorization symbol depends on the new kernel
faults yet).

**Adoption.** gps-360-go: replace the sequential-`Check` OR handlers with
`RequireAnyPermission` on the route line; delete the hand-written role wire
(`pockets/auth/inbound/roles.go`) and pass `ValidateAssignment` as
`Config.AssignmentPolicy`; re-cap dashboard rosters with
`LookupResourcesIn(…, Limit)` instead of post-hoc slicing.

### sdk — v0.7.0 — tagged 2026-08-31 @ `41ddfdb`: the write vocabulary (minor)

Plan of record `.claude/plans/crud-write-vocabulary.md` (gopernicus #21). A
**minor**: purely additive, but it materially expands the host contract. Every
JSON write slice had been re-deriving the same four pieces — a sparse-PATCH
field, a collect-every-problem refusal, a strict body reader, and a
compare-and-set token. They now ship once.

**Additions — kernel (`sdk`, new file `faults.go`).**

- **`sdk.Violation{Field, Code, Message}`** — no json tags; the kernel stays
  transport-agnostic and `web` owns the wire shape.
- **`sdk.ValidationError{Violations []Violation}`** with `Add(field, code,
  message)`, `Err()`, `Error()`, and `Unwrap() → sdk.ErrInvalidInput`. Every
  method has a POINTER receiver and `Error` is pointer-only by design: a value
  receiver makes `errors.As(err, &ve)` silently miss a value-stored error and
  turn a 400 into a nondeterministic 500. `Err()` returns an explicit `nil` on
  an empty collector (the typed-nil trap), pinned by test.
- **`sdk.Refuse(field, code, message)`** and **`sdk.UnknownReference(field,
  id)`** — the one-violation constructors. `UnknownReference` is a DOMAIN
  PRE-CHECK artifact: store connectors map an FK fault to a bare
  `sdk.ErrInvalidReference` with no field name, so the pre-check produces the
  good message, the FK constraint still guards the check-then-write race, and
  the race's fallback stays the generic 400 "invalid reference". Pass only an id
  the CALLER supplied in that field — the message echoes it back.
- **`sdk.CodeRequired`/`CodeInvalidType`/`CodeInvalidFormat`/`CodeUnknownField`/`CodeUnknownReference`**
  — the stable machine codes clients may switch on.
- **`sdk.StaleError{CurrentUpdatedAt time.Time}`**, `Unwrap() → sdk.ErrConflict`.

**Why the kernel.** `foundation/web` may never import `foundation/crud` (the
tier is flat, G12b, tests included) and `crud` may never import `net/http`
(G21). A vocabulary both tiers must name therefore goes to the root. The
admission criterion this first behavior-bearing promotion establishes is
written into `sdk/README.md`: shared by two or more foundation packages,
stdlib-only, zero transport semantics.

**Additions — `crud` (new file `field.go`, zero new imports; G21 and
store-adapter weight untouched).**

- **`crud.Field[T]{Set bool; Value T}`**, **`crud.Some[T](v T) Field[T]`**,
  **`crud.Overlay[T](current T, f Field[T]) T`** — the sparse-PATCH
  representation and its fold. Zero value = absent = leave unchanged.
- **The nullable rule is normative**, so five domains don't each pick
  differently: a nullable column rides `Field[*T]` (`Some[*T](nil)` is the
  explicit clear, `Field[*T]{}` is absent); a NOT NULL column rides `Field[T]`.
- No custom JSON methods on `Field`: it is the domain's write representation,
  not a decode target.

**Additions — `web` (`errors.go`, `openapi.go`, new file `readbody.go`).**

- **`web.ReadBody(w, r, keys ...string) (*Body, error)`** — bounded by
  `web.DefaultBodyLimit` (1 MiB), decodes exactly one JSON object, and rejects
  any key not in `keys` with one `unknown_field` violation PER unknown key, all
  collected. That is the rule that a body must not smuggle a path-owned id: an
  undeclared `organization_id` is a named 400, never silently ignored. `{}` is
  valid — whether an empty PATCH is acceptable is the domain's call. Structural
  failures return a **nil Body**: an overrun wraps `*http.MaxBytesError` (413
  through `ErrValidation`), and an empty body / `null` / non-object / trailing
  content wraps `sdk.ErrInvalidInput`, sentence first.
- **`web.Body`'s getters** — `Has`, `Str`, `OptStr`, `Bool`, `Strs`, `Date`,
  `Instant`, `ExpectedUpdatedAt` — **one getter never short-circuits the next**:
  each records its violation and returns its zero value, so one pass collects
  every field's problem and `Body.Err()` is the single terminal check. (Within a
  getter the scan may stop early: `Strs` reports one violation per field, naming
  the first non-string element.) They return PLAIN values plus presence, never `crud.Field` (web cannot
  import crud), so a handler composes:
  `if body.Has("title") { in.Title = crud.Some(body.Str("title")) }`.
- **`web.BodyKeyExpectedUpdatedAt = "expected_updated_at"`** — the CAS key,
  snake_case like every other key this framework puts on the wire, and NOT
  implicit: a CAS route declares it in `keys` explicitly.
- **`web.ErrStale(msg, current)`** — 409, code `"stale"`, with the token in the
  new `current_updated_at` response field (RFC3339Nano, UTC). Handlers never
  hand-build the literal.
- **`web.FieldError.Code`** (`json:"code,omitempty"`) and
  **`web.Error.CurrentUpdatedAt`** (`json:"current_updated_at,omitempty"`) —
  a typed field, deliberately not a generic details map.
- `errorSchema()` in `openapi.go` documents both new keys.

**Posture amendment — `ErrFromDomain` recognizes two more typed errors.**
Without it, every pocket/handler responding through `ErrFromDomain` would emit
a fieldless generic 400 — the boilerplate this change exists to delete. The
branch order is pinned by test: `SafeDomainError` → `*sdk.ValidationError`
(guarded `len(Violations) > 0`) → `*sdk.StaleError` → `*http.MaxBytesError` →
the existing `errors.Is` switch. Consequences to know:

- A `SafeDomainError` wrapping a `ValidationError` still wins, and the
  per-field detail is dropped — the host seam's chosen body is the more
  specific statement of intent.
- A typed but EMPTY `&sdk.ValidationError{}` falls through to the generic 400;
  it never renders `"fields":[]`.
- An oversized body answers **413**, the same body `ErrValidation` produces, so
  a handler that maps everything through `ErrFromDomain` no longer 500s a
  `*http.MaxBytesError` (a `SafeDomainError` around one still wins).
- `ErrValidation` and `ErrFromDomain` render a `*sdk.ValidationError` through
  one shared unexported helper, pinned by a body-parity test, so the two cannot
  drift.
- The "nothing but `SafeDomainError` is recognized" paragraph in `web/errors.go`
  is now false and was rewritten in the same change: THREE explicit wire-text
  contracts are recognized, all by concrete type (never structurally), and the
  counter-rule is that `Violation.Message` is caller-facing text only — never
  `sdk.Refuse(field, code, err.Error())` around a store or driver error, now
  enforced by guard **G23** (`guard-violation-message-not-error`).
- The pocket ruling, written into the same doc: a pocket MAY return
  `sdk.ValidationError`/`sdk.StaleError` and have those sentences reach the
  host's wire without wrapping — a field-shape refusal (required, invalid
  format, unknown reference) states what the request got wrong about its own
  body and is product-neutral in a way a policy sentence is not. Free-form
  policy text still requires `SafeDomainError`.

**Compatibility assertion (verified, not assumed).** Every new symbol is
additive; both new wire fields are `omitempty`, so every existing response body
is byte-identical (pinned by marshal tests); `sdk`'s `go.mod` still has no
require block; no module repins and `examples/*` build unchanged.

**The CAS precision contract — read before wiring `expected_updated_at`.** The
framework cannot enforce a domain's comparison, so it is pinned in
`StaleError`'s doc: compare with `time.Time.Equal`, never `==` and never a
string compare of formatted tokens, and compare AT THE STORE'S PRECISION —
Postgres `timestamptz` keeps microseconds (truncate both sides with
`Truncate(time.Microsecond)`), turso stores text and round-trips what it wrote.
A CAS that never matches on one store is worse than no CAS.

**Adopter recipe.**

```go
body, err := web.ReadBody(w, r, "title", "summary", web.BodyKeyExpectedUpdatedAt)
if err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}

var in article.Patch
if body.Has("title") {
    in.Title = crud.Some(body.Str("title"))
}
if body.Has("summary") {
    in.Summary = crud.Some(body.OptStr("summary")) // *string: null clears
}
expected := body.ExpectedUpdatedAt()
if err := body.Err(); err != nil {
    web.RespondJSONError(w, web.ErrValidation(err))
    return
}

// The domain refuses with sdk.Refuse / a sdk.ValidationError collector, or with
// &sdk.StaleError{CurrentUpdatedAt: current} when the CAS loses; the handler's
// tail is one line.
out, err := svc.Update(ctx, id, expected, in)
if err != nil {
    web.RespondJSONError(w, web.ErrFromDomain(err))
    return
}
```

Not on this tag: **no numeric getter and no raw accessor on `Body`** — a write
surface with a numeric field waits on the follow-up (the reader already decodes
with `UseNumber`, so the precision is preserved for it and nothing about the
decode changes when it lands); no generic PATCH endpoint and no
reflection-driven struct patching (the domain still writes its own patch type
and validators); no `UnmarshalJSON` on `Field`; no civil-date type — `Body.Date` returns a
midnight-UTC `time.Time` parsed with strict `time.DateOnly`, and when a civil
date lands, `Date()` may change or be deprecated (a v0.x break accepted here
deliberately); no per-call body-limit option; no changes to `validation.Errors`
or `web.FieldErrors` beyond the additive `Code` field and the three-collectors
doc rule (which collector when: `validation.Errors` for pure field validators,
`web.FieldErrors` for transport-edge request shape, `sdk.ValidationError` for
domain-authored refusals that cross layer boundaries); no pocket, store, or
example adopts the vocabulary on this train.

### sdk — v0.6.0 (next tag, 2026-08-27): the list contract reaches the request (minor)

Plan of record `plans/web-crud-list-request.md` (gopernicus #11). A **minor**
rather than the additive-as-patch precedent (`v0.3.1`, `v0.4.1`) by owner
ruling, because two things change observably alongside the additions.

**Additions.**

- **`crud.ParseListQuery(q url.Values, opts crud.ListQueryOptions) (crud.ListRequest, error)`**
  — `ParseListRequest` over the canonical query keys `limit` / `cursor` /
  `offset` / `count` / `q` (exported as `crud.QueryKeyLimit` … `QueryKeySearch`;
  `crud.QueryKeyOrder` names `order` for callers). `ListQueryOptions{Limits,
  DefaultStrategy}` is the resource-side policy; the zero value is sdk's
  defaults. It builds a `ListParams` and delegates, so there is one parser, not
  two. It lives in `crud`, not `web`: foundation packages import the root only
  (G12b), and every store adapter imports `crud`, so `crud` carries `net/url`
  and never `net/http` (guard **G21**, `guard-crud-no-nethttp`).
- **`order` is deliberately NOT folded in.** Parse it beside the call with
  `crud.ParseOrder(fields, q.Get(crud.QueryKeyOrder), defaultOrder)` — reject
  (JSON edges) or fall back to the default order (SSR, the cms admin posture)
  is a per-aggregate choice a combined parser cannot express.
- **`crud.Items[T](items []T) Page[T]`** and **`crud.MapItems[T,U](items []T,
  fn func(T) U) Page[U]`** — the bounded-page constructors for a
  parent-scoped, uncursored list: a `Page` holding only `Items`, so the wire
  shape is `{"items":[…]}`. `MapItems` is defined as `MapPage(Items(x), fn)`.
- **`web.NoStore() web.Middleware`** — a named preset of
  `DefaultHeadersMiddleware` writing exactly `Cache-Control: no-store` (no
  `Pragma`, no `Expires`) before the handler runs; a handler may still
  override on its own writer. Mount it on route groups whose answers derive
  from a per-request grant. It is a header policy the host applies, not an
  identity gate and not a guarantee — whatever gate is mounted beside it is
  what makes the group authenticated.

**Observable change 1 — parser rejections now classify.** Every rejection
from `ParseListRequest` (seven) and `ParseOrder` (two) wraps
`sdk.ErrInvalidInput`, sentence first, sentinel last
(`"rows value too small, must be larger than 0: invalid input"`); the
`strconv` cause stays in the chain for the three conversion errors. The
existing sentences survive verbatim as the prefix — a host matching the raw
strings keeps working. Consequences: `web.ErrFromDomain(err)` answers the
**generic** `400 bad_request "invalid input"` where it answered 500 (its
generic mapping is deliberate — `SafeDomainError` posture unchanged), and
`web.ErrValidation(err)` puts the sentence on the wire:
`{"message":"page limit conversion: strconv.Atoi: parsing \"zero\": invalid syntax: invalid input","code":"bad_request"}`.
That is the contract: `ErrFromDomain` for the status, `ErrValidation` for the
sentence — the path `DecodeJSON` failures already take. Cursor-decode errors
are NOT wrapped: a stale or bad cursor token is a first page by rule, never a
400. A host that special-cased the old 500 (gps-360-go `wire.ListRequest`)
can delete the special case; the helper collapses to
`crud.ParseListQuery(r.URL.Query(), crud.ListQueryOptions{Limits: limits})` +
`web.RespondJSONError(w, web.ErrValidation(err))`.

**Observable change 2 — SDK-built empty pages say `[]`, never `null`.**
`Items`, `MapItems`, `TrimPage`, `MapPage`, and `MapPageErr` all normalize a
nil item slice to an empty one. pgx pages never carried nil (`CollectRows`
starts non-nil); the turso connector's `List` did, so a **turso-backed host's
empty page changes from `"items":null` to `"items":[]`** on this tag —
bug-fix class, but a wire change. A directly constructed `Page[T]{}` is
caller-owned and still marshals `null`; there is no `Page.MarshalJSON`.

Not on this tag: `web/openapi.go` still documents only `limit`/`cursor`/`order`
(pre-existing drift, left as found); a civil-date type; folding `order` into the
parser.

### integrations/datastores/pgxdb — v0.6.0 (next tag, 2026-08-27): UTC-located `timestamptz` on every connection, session `timezone=UTC` by default, `Collect`/`ProbeTables`/22P02 (minor; connect-time behaviour change)

Plan of record `plans/web-crud-list-request.md` (gopernicus #11 D6, and #9
bundled by owner ruling). Pin stays `sdk v0.4.0` — no new sdk symbol is used.
No `Config` field is added: the scan location is always UTC, and the DSN is
the session-zone escape hatch.

**Why (the cause is NOT the session zone).** In the pinned pgx v5.8.0 the
default extended protocol decodes `timestamptz` in **binary** — microseconds
since epoch, no zone — via `time.Unix(...)`, which yields a `time.Local`
-located value; the session `TimeZone` only shapes the **text** decoder's
input. So a scanned `timestamptz` marshalled as `2026-08-27T11:58:03-07:00`
on a laptop and `Z` in a `TZ`-less container, and `SET TIME ZONE 'UTC'`
alone would not have changed that. `pgtype.TimestamptzCodec{ScanLocation}`
("does not change the instant") is the seam both decoders honour.

**D6a — the scan location (unconditional).** `Open` now sets
`pgxpool.Config.AfterConnect` to register, on every connection's type map, a
`timestamptz` type whose codec is `TimestamptzCodec{ScanLocation: time.UTC}`
and a `_timestamptz` array type built over that new element. The array
registration is the strictly-safe form: pgx's default array codec captured a
pointer to the default element type at init (`pgtype_default.go:169`), and
while a `[]time.Time` destination already re-plans through the map (the
element codec declines a `*time.Time`, so scalar-only registration suffices
there — measured), a `pgtype.Timestamptz` element destination would still
reach the stale codec. `tstzrange`/`tstzmultirange` are out of scope (same
captured-pointer shape; no in-repo store scans them). `timestamp` (without
zone) already decoded to UTC — untouched. **What changes:** every scanned
`timestamptz`, scalar or array, in `Scan`, `QueryOne[T]`, `List`, and under
`QueryExecModeSimpleProtocol` alike, is UTC-located — `Equal`/`Before`/`Sub`
are unchanged; `String()`/`Format` and `encoding/json` output switch from the
local offset to `Z`. Hosts already on `timestamps.go`'s `From*` helpers see
nothing new. Hosts carrying a `wire.Time`/`TimePtr`-style presentation helper
(gps-360-go: 55 sites) can delete it — DTO fields become `time.Time` /
`*time.Time`, byte-identical output for scanned instants. **Audit first:** an
instant minted in-process (`time.Now()` in a domain constructor) still carries
`time.Local` unless the container runs `TZ=UTC` — normalize those at the
domain edge with `.UTC()`. **`AfterConnect` is owned by the connector** for
its codecs; a future `Config.AfterConnect` seam will chain after the
connector's registration, never replace it (recorded on `Config`).

**D6b — the session zone (owner-ruled IN).** `Open` sets the startup
parameter `timezone=UTC` **unless** the host already named one: a `timezone`
key in the DSN (any case; pgconn also maps `PGTZ` onto it) or an `options=`
value containing `timezone=` (e.g. `-c TimeZone=Europe/Oslo`, also
`PGOPTIONS`). A startup parameter costs no round-trip, is idempotent under
pgx's reconnects, and — unlike an `AfterConnect: SET TIME ZONE` — survives
PgBouncer transaction-mode server reuse because `timezone` is in the pooler's
tracked startup set. The DSN string itself is never rewritten. **What it
changes, honestly:** nothing about scans (D6a covers those). It changes
server-side, zone-dependent SQL for hosts that never set a zone: `now()::text`,
`to_char(tstz, …)`, `date_trunc('day', tstz)`, `tstz::date`, `EXTRACT(hour
FROM tstz)`, `timestamptz → timestamp` casts, and — the sharp one — a bare
`'2026-01-01 00:00'` literal bound to a `timestamptz`, which is interpreted in
the session zone. Bound `time.Time` arguments are unaffected (pgx binds an
instant). Today those expressions differ silently between a laptop and a
`TZ`-less container; after this tag they evaluate identically. **Escape
hatches:** `AT TIME ZONE '…'` in the SQL that needs local bucketing, or pin
`timezone=` in the DSN. Adopter check before repinning: grep the host for
`date_trunc|::date|to_char|AT TIME ZONE|EXTRACT` (gps-360-go: zero hits;
coordination-hub: the owner's to run).

**#9 — the small helpers (additive).**

- **`Collect[T any](ctx, db Querier, sql string, args ...any) ([]T, error)`**
  — the exported twin of `List`'s row collection: strict
  `RowToStructByName`, `MapError` on both the query and the collect error,
  empty non-nil `[]T` on no rows. Parent-bounded, unpaginated reads; not a
  paging primitive — that is `List`. Pairs with `crud.MapItems` on the wire.
- **`ProbeTables(ctx, db Querier, tables ...string) error`** — `ProbeTable` in
  a loop, stopping at and returning the first failure (already naming the
  relation when it is an absence; a `MapError`'d infrastructure failure is not
  re-wrapped as "about" a table).
- **`MapError`: SQLSTATE `22P02` (`invalid_text_representation`)** →
  `fmt.Errorf("%s: %w", pgErr.Message, sdk.ErrInvalidInput)` — the one case
  that keeps the server's message (`invalid input syntax for type uuid:
  "not-a-uuid"`), because that sentence is what a host's **log** loses today.
  It never reaches a client: `web.ErrFromDomain` is generic by design. The
  existing four cases keep returning bare sentinels.

Proof: hermetic tests on the extracted `poolConfig` (codec identity, decoding
through `pgtype.NewMap()`, the `timezone`/`PGTZ`/`PGOPTIONS`/`options=`
precedence with the libpq env cleared) and `TestLive_ScanUTC` /
`TestLive_Collect` against a throwaway Postgres 17 run under
`TZ=America/Los_Angeles` — every scan `+0000 UTC`, the `+05:00` literal equal
to `2025-12-31T19:00:00Z`, `SHOW TimeZone` = `UTC` by default and
`Europe/Oslo` when the DSN says so.

### integrations/datastores/pgxdb — v0.6.1 — tagged 2026-08-28: `ListQuery.FixedOrder`, a store-fixed composite ORDER BY for the offset strategy (patch; additive)

Plan of record `plans/pgxdb-list-fixed-order.md` (gopernicus #15; originating
host gps-360-go, which carried an `OffsetPage[R, D]` helper in nine stores
because `pgxdb.List` could not express `closing_date DESC NULLS LAST, name
ASC, id ASC`). Pin stays `sdk v0.4.0`; no schema; no sibling bumps.

**What.** One additive field, `ListQuery.FixedOrder string` — a store-authored
`ORDER BY` expression (without the keyword), trusted store text like
`BaseSQL`, written verbatim by the offset flow. The store includes its own pk
tiebreak; `List` appends nothing. When set:

- `OrderFields`/`DefaultOrder` are not consulted; `OrderFields` also set is a
  programming error → `sdk.ErrInvalidInput` on first call.
- A request carrying an `Order` → `sdk.ErrInvalidInput` (the order is not the
  caller's).
- The list is **offset-only**: the cursor strategy (default or explicit, or a
  cursor token) → `sdk.ErrInvalidInput` — no keyset predicate is derivable
  from an arbitrary expression.
- The offset flow is otherwise unchanged: `LIMIT n+1` → `HasMore`, `HasPrev`
  from the offset, `WithCount` → `Total` via the `COUNT(*)` wrap, the search
  clause folded before the strategy switch, `MapError`.

**Zero value.** A `ListQuery` without `FixedOrder` builds byte-identical SQL
to `v0.6.0` (pinned by test).

**Adopting (gps-360-go).** Replace each local `offsetPage` helper with
`pgxdb.List` + `FixedOrder` carrying the same expression, then delete
`rows.go`'s `offsetPage`. Same SQL shape, same page fields. Hosts serving
these lists over `web.ParseListQuery` should note a client-supplied `order`
now answers 400 through `ErrFromDomain` rather than being ignored.

Proof: hermetic SQL-capture tests (`TestList_FixedOrder*`) and the live
`TestLive_ListBehavior/fixed_order_offset` against a throwaway Postgres 17 —
`NULLS LAST` + name tiebreak traversal over three offset pages with
`HasMore`/`HasPrev`/`Total` asserted and the cursor strategy refused.

### pockets/authentication — v0.9.0 — tagged 2026-08-29: one composable authenticator, named helpers, the credential on the context (BREAKING, pre-1.0)

Plan of record `plans/authentication-principal-posture.md` (originating host
gps-360-go `plans/33-principals-api-keys-and-route-proof.md` D2). `sdk`
untouched; store modules untouched; no schema.

**The one authenticator.** `Service.RequirePrincipal(opts ...PrincipalOption) web.Middleware`
replaces the five fixed-name resolvers. Its options are OR-sets over credential
kinds and transports plus a liveness tier and a browser denial mode:

```go
Accept(kinds ...CredentialKind)  // OR-set of credentials; default: every wired kind
Transports(ts ...Transport)      // OR-set of transports;  default: header + cookie
Live()                            // access_token ⇒ the session row must exist; api_key ⇒ pass
Browser()                         // on denial 303 to Config.BrowserLoginPath instead of a JSON 401
```

Zero options = every wired credential, both transports, header authoritative,
stateless, JSON 401. `Accept()` / `Transports()` with zero arguments PANICS at
construction. A credential arriving on a transport outside the set is IGNORED,
never denied. Nested under an outer `RequirePrincipal`, an inner instance never
re-resolves: it narrows by reading the stashed `Credential` (a `Live()` inner
runs the session lookup once; a second nested `Live()` reads the already-proven
session id). The supported wiring invariant is **one authentication `Service`
per chain**.

**The migration table:**

| removed | replacement | note |
|---|---|---|
| `RequirePrincipal` (method value, `func(http.Handler) http.Handler`) | `RequirePrincipal()` / `RequireAccessTokenOrAPIKey()` | every `web.Middleware` call site gains `()` |
| `RequireUser` | `RequireAccessToken()` | **correction (security tightening):** a non-JWT bearer no longer falls through to the cookie — a key plus a valid cookie is now 401, where it previously passed as the cookie's user |
| `RequireServiceAccount` | `RequireAPIKey()` or `RequirePrincipal(Accept(CredentialAPIKey), Transports(TransportHeader))` | identical behavior with the transport spelled |
| `RequireLiveSession` | `RequireAccessTokenOrAPIKeyLive()` | identical |
| `RequirePrincipalBrowser` | `RequirePrincipal(Browser())` | identical |
| `RequireLiveSessionBrowser` | `RequirePrincipal(Live(), Browser())` | identical |
| `AuthenticateAPIKey(ctx, rawKey)` | no direct replacement | raw credential verification is not an application-service entry point; it moves behind `RequirePrincipal` — read the result with `CurrentPrincipal` / `CurrentCredential` |

**`Config.BundledRouteAuth`** exposes one optional `RoutePrincipalStrategy` per
bundled route group (built with `PrincipalStrategy(opts...)`). A zero field
keeps that group's audited default; a set field replaces only that group,
resolved once at `NewService` into concrete middleware. It configures
AUTHENTICATION only — it cannot unmount the authenticator from a protected
bundled surface, and it never substitutes for the separate host authorization
seams (`MachineRoutesGate`, `UserAdminCheck`, `InviteCheck`). `PrincipalStrategy()`
called with no options is an explicit choice of the primitive's defaults, NOT
"leave the audited default in place" — leave the field zero for that. Override
example (every other bundled route keeps its default):

```go
Config{
	BundledRouteAuth: BundledRouteAuthentication{
		Invitations: PrincipalStrategy(
			Accept(CredentialAccessToken),
			Live(),
		),
	},
}
```

**The bundled-route audit tightens what an API key reaches.** The old
`RequireLiveSession` admitted both access tokens and API keys uniformly; the
audited per-surface defaults do not:

| tightened (API key no longer admitted) | preserved (API key, incl. act-as-user, still admitted) |
|---|---|
| `GET /auth/delivery/status`, `/auth/methods`, `/auth/csrf` (`SessionSecurityReads` → `RequireAccessTokenLive()`) | `GET /auth/me` (`SessionHydration` → `RequireAccessTokenOrAPIKeyLive()`) |
| password/step-up/identifier/OAuth-unlink credential mutations (`CredentialManagement` → `RequireAccessTokenLive()`) | `/auth/admin/users…` (`UserAdministration` → `RequireAccessTokenOrAPIKeyLive()`) |
| service-account/key create/list/mint/revoke (`MachineLifecycle` → single `RequireAccessTokenLive()`, replacing the old `RequireUser` → `RequireLiveSession` two-authenticator stack) | authenticated invitation routes (`Invitations` → `RequireAccessTokenOrAPIKeyLive()`) |

The bundled HTML surface (`BrowserAccount`) reads its cookie only, requires a
live session, and redirects on denial — including the HTML-only
`POST /auth/identifiers/{id}` form route, which now 303s (instead of a JSON
401) on an authentication denial; the HTML denial honors `Config.BrowserLoginPath`
and carries a validated `path?query` `return_to`.

**Host repin recipe:**

```
go mod edit -require github.com/gopernicus/gopernicus/pockets/authentication@v0.9.0
go mod tidy
```

**gps-360-go's one call site:**

```
router.Group("/api/v1", authenticationSvc.RequirePrincipal, …)
```
becomes
```
router.Group("/api/v1", authenticationSvc.RequireAccessTokenOrAPIKey(), …)
```

No tag exists yet — the owner cuts `v0.9.0`.

### pockets/authentication — v0.8.2 — tagged 2026-08-28: identity.Resolver is principal-exact (patch)

Plan of record `plans/identity-resolver-principal-exact.md`. `Resolve` for a
`user` principal projects the stored `DisplayName` exactly — blank stays blank;
the email-local-part fallback and its helpers are gone. Identifier matching (an
email value → a user) never enters `Resolve` (`TestResolveUserIsPrincipalExact`).
No symbol added or removed from the public surface; no schema; no pin moves.

**Upgrade note.** If your host renders `Info.DisplayName` without a guard, a
user with a blank stored name now renders "" instead of the email local part.
Decide at the render site: refuse (a host stamping the name into a durable
record should 403), show an address, or show nothing. Do not re-add a guess
in a shared Resolver decorator.

### pockets/authentication — v0.8.1 + features/authentication — v0.4.3 — tagged 2026-08-28 (maintenance line off v0.4.2): host-owned mail data, subjects, and SMS bodies (patch; additive)

Plan of record `plans/auth-mail-host-data-and-subjects.md`. A **patch on both
lines** by plan ruling: every symbol is additive, the zero value of every new
`Config` field renders today's output byte-for-byte (pinned by golden test
against the pre-release render), no schema, no store retags, and **no pin
moves** — the `v0.4.3` maintenance commit is the `v0.4.2` tree plus this change
(still `sdk v0.4.0` in its `go.mod`, so a host on `sdk v0.4.1` +
`stores/pgx v0.3.0` upgrades exactly one requirement), and `v0.8.1` stays on
`sdk v0.6.0`.

- **`Config.DeliveryData DeliveryDataHook`** — `func(ctx, purpose, data)
  (additions, error)`, run once per render for every purpose on both rails,
  before the subject/body/SMS templates. Input is a fresh, secret-free copy
  (nested `Metadata` copied); the return merges before `Secret`/`Subject` are
  inserted. **Reserved:** returning `Secret`, `Link`, or `Subject` is
  `ErrDeliveryDataReserved` (wraps `sdk.ErrInvalidInput`) and no envelope is
  built. Error surfacing is per flow (README "Mail content"): invitation
  create/resend return it (create with the already-persisted record), the
  member-added notice logs it (grant already committed), opaque starts follow
  the worker's bounded retry → dead-letter and never report it to the caller.
- **`Config.EmailSubjects` / `Config.SMSBodies map[string]string`** — purpose →
  `text/template` source, parsed at construction with `missingkey=error`.
  `ErrDeliveryOverrideInvalid` at `NewService`/`Register` for an unknown
  purpose, empty source, parse failure, or an SMS entry for an email-only
  purpose. `ErrDeliverySubjectInvalid` at render for an empty or CR/LF-carrying
  subject, before anything is queued.
- **`Purpose*` constants exported** from the public package (ten, aliasing the
  internal delivery set) — key the maps and switch in the hook without string
  literals.
- **Invitation / member-added data enriched** — `InvitationID`, `OperationID`,
  `ResourceType`, `ResourceID`, `ResourceName` (`""`), `ResourceKind` (`""`),
  `Relation`, `RelationLabel` (`""`), `InvitedBy`, `InviterName` (`""`),
  `Metadata` (non-nil copy), `Link`. Direct add: `InvitationID` empty,
  `OperationID` = the minted grant operation ID; accept/pending: both = the row
  ID. The bundled bodies and SMS bodies now render
  `{{or .ResourceName .ResourceID}}` — identical output while the name is
  empty, so a hook that sets only `ResourceName` is immediately useful.
- **One internal render-order change, no observable output change:** the
  subject template now executes on the email rail only (an SMS render never
  ran one to any effect), and a caller-supplied `Data["Secret"]` no longer
  shadows `Request.Secret` (no caller ever did).

**Adopting (coordination-hub):** bump `features/authentication v0.4.2 →
v0.4.3` only; set `cfg.DeliveryData` to look up the campaign name and return
`{"ResourceName": name}` for `auth.PurposeInvitation` / `auth.PurposeMemberAdded`;
optionally `cfg.EmailSubjects[auth.PurposeInvitation]`. No template override is
needed for the name to appear.

### pockets/authentication — v0.8.0 (next tag, 2026-08-27): list routes answer the parser's sentence; pin → `sdk v0.6.0` (minor)

Plan of record `plans/web-crud-list-request.md` (D3). A **minor**: the pin
moves to a sibling minor (`sdk v0.5.0 → v0.6.0`), and one wire body changes.
No schema, no store retags (`stores/{pgx,turso}` keep their `authentication
v0.7.0` / `sdk v0.5.0` pins and upgrade at the host via MVS).

- **Wire-body change on the five list routes** (`GET /auth/admin/users`,
  `/auth/service-accounts`, `/auth/service-accounts/{id}/keys`,
  `/auth/invitations/mine`, and the resource-scoped invitation list): a
  malformed `limit` / `cursor` / `offset` / `count` / `order` still answers
  **400 `bad_request`**, but the `message` is now the parser's own sentence —
  `"rows value too small, must be larger than 0: invalid input"`,
  `"cursor and offset are mutually exclusive: invalid input"`,
  `"unknown order field: nope: invalid input"` — instead of the fixed
  `"invalid page parameters"` / `"invalid order parameter"`. Status and
  `code` are unchanged; a client matching those two fixed strings must stop.
- Internally the pocket's `parseListRequest` is now the two framework calls
  (`crud.ParseListQuery` with `Config.ListStrategy` as `DefaultStrategy`, then
  `crud.ParseOrder`) + `web.ErrValidation` — de-duplication, and the pocket
  stops being the one caller that discarded the sentence. Only `q` is read for
  search (no legacy `s`), as before.

### 2026-08-27: the `features/` tier is `pockets/` (module-path rename; ONE train of 19 tags)

Plan of record `plans/rename-features-to-pockets.md`. The third tier is renamed
so a host app's own *feature* ("invite a teammate") stops colliding with the
framework's word for the unit it composes. This is a **module-path rename** —
an identity change for seventeen modules — plus the composer package move. No
behavior, schema, route, config, or wire change anywhere; no migration.

**The composer moved:** `github.com/gopernicus/gopernicus/sdk/feature` is now
`github.com/gopernicus/gopernicus/sdk/pocket`. Type and function names are
unchanged (`pocket.Mount`, `pocket.Group`, `pocket.PrefixRegistrar`,
`pocket.RouteRegistrar`). There is **no alias shim** — `sdk/feature` is gone.

Every module keeps its own version lineage and takes a minor bump on the new
path, so `git tag -l` reads as one continuous story per module:

| Module (new path) | Last tag on old path | First tag on new path |
|---|---|---|
| `sdk` | v0.4.2 | **v0.5.0** |
| `pockets/authentication` | v0.6.0 | **v0.7.0** |
| `pockets/authentication/stores/pgx` | v0.4.0 | **v0.5.0** |
| `pockets/authentication/stores/turso` | v0.3.0 | **v0.4.0** |
| `pockets/authentication/views/goth` | v0.2.2 | **v0.3.0** |
| `pockets/authorization` | v0.5.0 | **v0.6.0** |
| `pockets/authorization/stores/pgx` | v0.2.0 | **v0.3.0** |
| `pockets/authorization/stores/turso` | v0.1.0 | **v0.2.0** |
| `pockets/cms` | v0.1.0 | **v0.2.0** |
| `pockets/cms/stores/pgx` | v0.2.0 | **v0.3.0** |
| `pockets/cms/stores/turso` | v0.1.0 | **v0.2.0** |
| `pockets/cms/views/goth` | v0.1.0 | **v0.2.0** |
| `pockets/events` | v0.1.0 | **v0.2.0** |
| `pockets/events/stores/pgx` | v0.2.0 | **v0.3.0** |
| `pockets/events/stores/turso` | v0.1.0 | **v0.2.0** |
| `pockets/jobs` | v0.2.0 | **v0.3.0** |
| `pockets/jobs/stores/pgx` | v0.3.0 | **v0.4.0** |
| `pockets/jobs/stores/turso` | v0.2.0 | **v0.3.0** |
| `workshop/gopernicus` | v0.1.0 | **v0.2.0** |

`ui/goth` and every `integrations/*` module are **not** retagged: their module
paths and runtime behavior do not change. `workshop/gopernicus` does join the
train — its CLI removes `gopernicus new feature`, adds `gopernicus new pocket`,
and embeds renamed templates, so `go install …/workshop/gopernicus@latest` would
otherwise keep distributing the retired verb.

Old tags stay. They are immutable history and still resolve at the old path
(the proxy serves `features/cms@v0.1.0` from the commit its tag points at). No
consumer is force-moved by this train — a consumer moves when it repins.

**Adopter action (source-breaking at the moment you repin).** Consumers that
use a local `replace` to this checkout break the instant `features/` moves on
disk, regardless of tags — the path edit below is not optional for them. Run
the sequence in every `go.mod` of the consumer:

```sh
# 1. module/import paths (tracked Go + templ sources)
sed -i '' -e 's#gopernicus/gopernicus/features/#gopernicus/gopernicus/pockets/#g' \
          -e 's#gopernicus/gopernicus/sdk/feature"#gopernicus/gopernicus/sdk/pocket"#g' \
          go.mod $(git ls-files '*.go' '*.templ')

# 2. local replace RHS paths — step 1 changes the module/LHS but deliberately
# does NOT match /abs/.../gopernicus/features/... or ../gopernicus/features/...
sed -i '' -e 's#/features/#/pockets/#g' go.mod

# 3. `feature.` → `pocket.` at composer call sites; rename tier-derived aliases
# such as eventsfeature too (compile does not catch a still-valid old alias).
# 4. versions per the table above, then tidy/resolve.
go mod edit -json
go mod tidy
go list -m -json all
```

Acceptance for a local-replace consumer: neither a replacement `Path`/RHS nor
any resolved module `Dir` contains `/features/`.

### features/authorization — v0.5.0 (next tag): `Config.Model` removed (breaking, pre-1.0)

Owner ruling 2026-08-26, minutes after v0.4.0: "drop config.model now — let's just
make it correct now." The one-release pass-through is withdrawn: `Config.Model` is
GONE, `Config.RelationshipModel` is the only name, and `ErrConfigConflict` (which
existed solely to referee the two) is removed with it. Construction order is back to
zero kinds (`ErrNoKindConfigured`) → `ErrModelRequired`. Nothing else changes —
`RelationshipModel` alias, `ErrNoDecisionKind` at 500, and every v0.4.0 behaviour
stand. The `stores/pgx` upgrade-runbook test literal moved to `RelationshipModel`
(in-workspace only; the store's own pin is unchanged and adopters never compile a
store's tests).

**Adopter action (breaking):** rename `Model:` → `RelationshipModel:` in every
`authorization.Config` literal; remove any `errors.Is(err, authorization.ErrConfigConflict)`
branch. The compiler finds both. Known hosts: `examples/auth-cms` (done in-repo),
coordination-hub and gps-360-go (owner-updated downstream). No store change; stores
not retagged.

### features/authorization — v0.4.0 — tagged 2026-08-26: `Config.RelationshipModel` + `ErrNoDecisionKind` is a 500 (minor; one-release deprecation)

Plan of record `plans/authorization-roles-model-followups.md` — the two
owner calls left open at the v0.3.0 close, released BEFORE gps-360-go adoption so
the handoff targets the final names.

**1. `Config.Model` → `Config.RelationshipModel`.** The relationship kind's model
now says so, symmetrically with `Config.RoleModel`; `RelationshipModel` is also a
type alias of `Schema` so the two kinds read alike at the wiring site. `Config.Model`
STAYS as a deprecated pass-through and still wires the relationship kind exactly as
before. Setting BOTH (each with a non-empty `ResourceTypes`) is the new
`ErrConfigConflict` (wrapping `sdk.ErrInvalidInput`) — the feature refuses to guess
which policy governs rather than letting one silently win. Construction check order
is pinned: zero kinds (`ErrNoKindConfigured`) → `ErrConfigConflict` →
`ErrModelRequired`. The `RequirePermission*` mount panics keep their shape and name
`Config.RelationshipModel` in the message.

**Deprecation window — as tagged.** `Config.Model` was deprecated as of **v0.4.0**
with a v1.0 removal promised; that window was withdrawn by owner ruling the same
day — v0.5.0 removes it (see the entry above).

**2. `ErrNoDecisionKind` answers 500, not 400.** It no longer wraps
`sdk.ErrInvalidInput`: a decision surface with NO model-bearing kind is a
SERVER-SIDE WIRING FAULT, not something the caller said, so it wraps no sdk
taxonomy kind and lands on `web.ErrFromDomain`'s default — HTTP **500** with the
generic internal body — consistent with the three gates, which panic at mount for
exactly this wiring. It is deliberately UNLIKE `ErrMutationsNotConfigured`, which
stays **400**: that one is a precondition an actor can observe on a correctly
deployed host. Consequences: `ReasonFor(ErrNoDecisionKind)` now returns
`ok=false` (no decision reason — the caller treats it as infrastructure), and
`errors.Is(err, sdk.ErrInvalidInput)` no longer matches it. The error identity and
message are otherwise unchanged (it still does NOT wrap
`ErrRelationshipsNotConfigured`), and it is still never a 403.

**Compatibility (v0.3.0 → v0.4.0).** Additive `Config.RelationshipModel` +
`RelationshipModel` alias + `ErrConfigConflict`; `Config.Model` deprecated (still
honoured); `ErrNoDecisionKind` no longer satisfies `errors.Is(err,
sdk.ErrInvalidInput)` and answers 500 (only roles-only-without-model hosts see it
at all). No store change.

**No store change.** No port, DDL, migration, or store-tag movement:
`features/authorization/stores/{pgx,turso}` are **NOT retagged**. Their pinned
`features/authorization` requirement is untouched, so a store module still builds
against the field name it was tagged with.

### features/authorization — v0.3.0 (next tag): the roles kind's model + one decision surface (minor, predominantly additive)

Plan of record `plans/authorization-roles-model.md` (gopernicus issue #5;
originating host gps-360-go, which hand-wrote the entire roles decision half).
The roles kind gains the model it never had, and the decision surface stops being
relationship-kind-only. **A host that sets no `Config.RoleModel` is behaviourally
unchanged** — same decisions, reasons, traces, and zero-length values.

**The seam.**

- **`Config.RoleModel`** — one new field, `RoleModel{ResourceTypes map[string]RoleTypeDef}`
  with `RoleTypeDef{Roles []string; Permissions map[string][]string}` (permission →
  the roles that grant it). Registered data, no migration, validated at
  `NewService`: `ErrRoleModelWithoutRoles` when set without `Repositories.Roles`,
  `ErrInvalidRoleModel` when structurally invalid, `ErrModelConflict` when a
  `(resource type, permission)` pair is declared by BOTH `Config.Model` and
  `Config.RoleModel`.
- **One decision surface, dispatched by pair ownership.** `Check`, `CheckBatch`,
  `CheckExplain`, `FilterAuthorized`, `LookupResources` and
  `RequirePermission`/`RequirePermissionOn`/`RequirePermissionFixed` now route each
  pair to the model that DECLARES it. `Config.Limits` is that surface's budget: it
  is resolved (and a negative field rejected with `ErrInvalidLimits`) whenever any
  model-bearing kind is wired, `MaxBatchSize`/`MaxLookupResults` are charged on a
  roles+model host too, and `MaxGraphStates` additionally bounds the role-assignment
  walk so an adversarial assignment count is `ErrEvaluationLimit`, never an
  open-ended store walk.
- **New exported symbols:** `RoleModel`, `RoleTypeDef`, `Config.RoleModel`,
  `LookupResult.Unrestricted`, `ExplainStep.Role`/`.Scope`, `ExplainKindRole`,
  `ExplainScopeDirect`, `ExplainScopeGlobal`, `ErrInvalidRoleModel`,
  `ErrModelConflict`, `ErrRoleModelWithoutRoles`, `ErrNoDecisionKind`.
- **`LookupResult.Unrestricted`** reports that a granting role is held GLOBALLY, so
  the principal reaches every resource of the type and `IDs` is empty. Only the
  roles kind ever sets it. It is additive because it is fail-closed under
  ignorance: a caller reading only `IDs` shows an unrestricted principal an EMPTY
  page — restrictive, never permissive. `IDs` stays ALWAYS non-nil.
- **`Register`'s log line** gains `role_model=<bool>` — a bool only, never
  resource-type or role names; policy vocabulary stays out of logs.

**The one error-identity change — `ErrNoDecisionKind`.** On a host where NO kind
bears a model (a roles-only wiring with no `Config.RoleModel` — gps-360-go today),
these five calls return the new `ErrNoDecisionKind` where they previously returned
`ErrRelationshipsNotConfigured`: **`Check`, `CheckBatch`, `CheckExplain`,
`FilterAuthorized`, `LookupResources`.** Nothing else moves — every other
relationship-kind method still returns `ErrRelationshipsNotConfigured`, and the
roles-kind methods still return `ErrRolesNotConfigured`. The new sentinel is a
clean identity (it does NOT wrap `ErrRelationshipsNotConfigured`) and wraps
`sdk.ErrInvalidInput`, so like its `ErrMutationsNotConfigured` precedent it is a
stable precondition refusal that maps to **HTTP 400** through the `web.Error`
seam — never a deny, never 403, never `ErrUnavailable`. *(Changed to 500 in
v0.4.0: the sentinel dropped its `sdk.ErrInvalidInput` wrap and is now classed as
a server-side wiring fault — see that entry above.)* A host branching on
`errors.Is(err, ErrRelationshipsNotConfigured)` for those five calls must add the
new sentinel; no host in `examples/` or gps-360-go did (grep, 2026-08-26). The
three gates still panic at mount on that wiring, with a **new message**:

```
authorization: RequirePermission… requires a decision-capable kind (Config.Model
or Config.RoleModel); a roles-only host without a role model must not mount it
```

*(In v0.4.0 the message names `Config.RelationshipModel` instead of `Config.Model`.)*

**No cross-kind universal bypass — deliberately.** The feature declares no
`Superuser`/`IsSuperuser` primitive and performs no cross-kind union. A globally
assigned role grants exactly the role-owned pairs whose grantor lists EXPLICITLY
name it, and acquires neither relationship-owned permissions nor permissions added
to the model later. An application flag that must bypass every present and future
decision (gps-360-go's `ManageAuthorization`, auth-cms's `isPlatformAdmin`) stays
in host composition, run before the surface, and owns the widening. A host
transcribing an existing steward/admin role must therefore list it on every
permission it should grant.

**Assign-time model validation (new, model-gated).** With a `RoleModel`
configured, assigning a `(resource type, role)` pair the model does not declare is
refused with `ErrInvalidRoleModel` (wrapping `sdk.ErrInvalidInput`) — a scoped
assignment needs the role in that type's `Roles`, a global one needs it declared by
some type. It runs inside the mutation repository's boundary as part of the
receipt-absent semantic validator, so `Service.AssignRole`,
`SystemMutator.AssignRole`, and generic `SystemMutator.Apply(OpRoleAssign)` cannot
disagree or bypass it, while an exact replay still returns its stored receipt after
a later model change dropped the role. **`UnassignRole` and every read path stay
opaque**: existing rows need no migration and remain listable and removable. Hosts
with no model are untouched. The typo that used to be a permanently silent no-grant
is now loud at assign time.

**Source-compatibility caveat.** `Config`, `ExplainStep`, and `LookupResult` gain
exported fields. Keyed literals are unaffected and no unkeyed literal of any of the
three exists in-repo or in gps-360-go (grep 2026-08-26; `go vet` checks composites
in the verify set) — but, as with any exported Go struct field addition, an unknown
downstream **unkeyed** literal of those three types must become keyed.

**No store change.** The engine runs on the EXISTING `role.Storer`
(`HasExactRole` + the paged `ListBySubject` walk): no port, DDL, migration, or
store tag change, so a host adopts the model by bumping ONE module.
`features/authorization/stores/{pgx,turso}` are NOT retagged. **Follow-up (D6,
recorded not built):** store-side `RolesHeld(subject, type, id)` and
`ListBySubjectForResourceType` probes, a `storetest` contract for them, and a
store train. Trigger: `ErrEvaluationLimit` from the assignment walk in a real
host, or measured gate latency.

**`storetest`** gains `Roles/Decision`, a `Parity/Roles` arm, and a `Composed`
family (skipped unless both kinds are wired) — all in the core module, no store
module bump needed to run them. Both live dialect legs ran for this train
(pgx on a throwaway `postgres:17` container; turso on the authorized playground —
the turso suite is now ≈12 min end to end, so pass `-timeout 20m`). The next
`stores/{pgx,turso}` repin onto v0.3.0 runs the three new families against the
live dialects automatically.

### features/authorization — v0.2.0 — tagged 2026-08-26: RequirePermission in coordinates (minor, additive)

`Service.RequirePermissionOn(resourceType, permission, pathParam)` and
`Service.RequirePermissionFixed(resourceType, permission, resourceID)` join
`RequirePermission(permission, resolver)`, which is unchanged. A route line now
reads as its own authorization question:

```go
r.GET("/orgs/{orgID}/people", h.people, svc.RequirePermissionOn("org", "view", "orgID"))
r.POST("/campaigns", h.create, svc.RequirePermissionFixed("platform", "admin", "main"))
```

The coordinates are **load-bearing and checked at registration** against the
compiled model: a `(resourceType, permission)` pair the schema does not declare,
an empty parameter name, or an empty fixed id panics when the route is mounted —
never a gate that quietly checks something the model never grants. Request
semantics are `RequirePermission`'s (401 / 403 / 500 / 503, fail closed);
`PathResource(resourceType, param)` is exported for hosts composing their own
gates, and an empty path value is a resolver error (500 — the honest answer to a
parameter name that does not match the route pattern). Same relationship-kind
precondition as `RequirePermission` (panics at mount on a roles-only host).
Origin: gps-360-go / coordination-hub's per-route authorizer style (owner
ruling, 2026-08-26).

### integrations/datastores/pgxdb v0.5.0 + every feature `stores/pgx` — tagged 2026-08-26: WithSchema — host-chosen schema instead of a search_path pin (minor across all six; ONE train)

Plan of record `plans/pgx-store-schema-option.md` (gopernicus issue #4;
originating host gps-360-go, which shares another app's Postgres and today
pins `search_path` in its DSN). Everything is additive and the default is
byte-for-byte unchanged: a host that passes no option gets exactly the SQL,
probes, and ledger statements it always had. Target tags:
`integrations/datastores/pgxdb/v0.5.0`,
`features/authentication/stores/pgx/v0.4.0`,
`features/{authorization,cms,events}/stores/pgx/v0.2.0`,
`features/jobs/stores/pgx/v0.3.0`.

**pgxdb — the seam.**

- **`pgxdb.Schema`** — a validated value type. `pgxdb.NewSchema(name)
  (Schema, error)` accepts a single bare identifier (letter, then
  letters/digits/underscores; ≤ 63 bytes; not `pg_*`, not
  `information_schema`; `public` is valid) and wraps `sdk.ErrInvalidInput`
  otherwise. `(Schema).Table(t)` renders `"<schema>".t`; the **zero `Schema`
  renders the bare name**, which is what makes the default byte-identical.
  Quoting preserves case: `Auth` and `auth` are different schemas. Build it
  once at the host (from env/config) so a malformed name fails there — no
  store option panics. It is the same seam for a host's own app-local pgx
  repositories: render your table names through `Schema.Table` and they
  follow the same option.
- **`pgxdb.RunMigrations(ctx, db, fs, dir, opts ...MigrateOption)`** gains
  `pgxdb.WithSchema(s)`. Inside the existing migration transaction it probes
  the namespace, runs `CREATE SCHEMA IF NOT EXISTS` only when absent, checks
  the role has `USAGE` and `CREATE` on it, then `SET LOCAL search_path TO
  "<schema>"` — **strict, no `public` fallback** (with `public` on the path an
  `ALTER TABLE users` whose `"auth".users` is missing would silently alter the
  host's `public.users`) — and asserts `current_schema()`. Unqualified DDL in
  the exported stream therefore lands in the schema; `ExportMigrations` is
  untouched. The ledger (`schema_migrations`) is **explicitly qualified** and
  lives in the schema. Grant failures name the missing grant and wrap
  `sdk.ErrForbidden`: schema creation needs `CREATE ON DATABASE` (a
  DBA-precreated schema is a real workaround — creation is skipped when the
  namespace exists); applying needs `USAGE, CREATE ON SCHEMA`.
- **STREAM MODEL CHANGE (ratified, YOUR CALL 4).** The runner's godoc used to
  say "one database, one stream … calls `RunMigrations` once per database".
  It now says **one stream per schema, each with its own ledger in its own
  schema**. The #4 use case — feature tables in `auth`, host tables in
  `public` — is two calls over two directories (`migrations/auth/`,
  `migrations/`). Consequences a host must own: the call order is yours and
  must be deterministic; **one call is one transaction and two streams are
  two transactions — there is NO cross-schema atomicity** (if the first
  commits and the second fails, fix the failing stream and rerun; each
  committed stream is idempotent by its own ledger); cross-schema FKs/views/
  functions must be explicitly qualified and applied after the stream they
  depend on. Filename uniqueness is now per `(schema, source)`.
- **Ledger relocation for hosts coming off a `search_path` pin.** Under
  `search_path=auth,public` your ledger rows may live in
  `public.schema_migrations` while your tables live in `auth`. Adopting
  `WithSchema("auth")` then sees an empty `"auth".schema_migrations` and
  **re-runs the stream**. Most files are `IF NOT EXISTS`-safe but
  authentication `0014_user_status.sql` is a full-table `ALTER COLUMN … TYPE
  … COLLATE "C"` rewrite under an exclusive lock and `0015` repeats a backfill
  `UPDATE`. Run the **ledger-relocation preflight** in the pgxdb README
  (explicit column list, `ON CONFLICT (source, version) DO NOTHING`, a
  count/checksum assertion before `COMMIT`) BEFORE the first schema-scoped
  call; the runner then reports every copied file already applied. Skipping
  it is safe-but-expensive, not recommended.
- Do **not** merge the rate limiter's `ratelimit_windows` DDL into a
  schema-scoped stream — the limiter's SQL is unqualified and it would fail at
  its own probe. A limiter schema option is a separate demand.
- pgx's default statement cache is 512 entries per connection; a
  schema-per-tenant host running three-plus store sets on one pool should
  size `statement_cache_capacity` in the DSN or use one pool per schema.

**Every feature `stores/pgx` — the option.**

- Each package gains `type Option func(*config)` and `WithSchema(s
  pgxdb.Schema) Option` (authorization's existing `Option` gains it beside
  `WithGuardianPolicy`). It is accepted by `Repositories(db, opts...)` AND by
  **every exported per-repository constructor** — authentication's 18
  `NewXStore(db, opts...)`, cms's five, jobs' three, events' `New(db,
  opts...) (*Store, error)`, authorization's `RelationshipRepository(db,
  opts...)` — so a host composing its own repository set from individual
  stores gets the same seam. Every table reference renders through one
  unexported `table(name)` chokepoint per store, guarded by a per-package
  `TestNoBareTableReferences` (go/parser over string literals, table set
  derived from the embedded migrations).
- **Boot probes.** authentication/authorization/events replace their private
  `probeTable` with `pgxdb.ProbeTable` on the qualified name (the adoption the
  v0.4.1 note deferred to "each store's next tag"); wrapping messages still
  name the migration source and `errors.Is(err, sdk.ErrNotFound)` holds.
  authentication's ALTER-column probe filters `information_schema.columns` on
  `table_schema` **only when a schema is set** — the default probe stays
  byte-identical (and, as before, unfiltered by schema).
- **cms and jobs gain an additive `StatusCheck(ctx, db, opts ...Option)
  error`** that probes their tables under the configured schema. Call it at
  boot when you configure a schema: those packages have no probe otherwise,
  and a mismatched schema (migrated into one, constructed with another, or
  migrated with a schema and constructed without) would silently read and
  write your own `public.entries` / `public.terms` / `public.assets` /
  `public.menus` / `public.job_queue`. `Repositories` itself stays
  probe-less and error-less.
- **jobs compatibility consequence (YOUR CALL 2).** `QueueOption` is now a
  type ALIAS of the package `Option` (`func(*config)`, config unexported);
  `WithLease` returns `Option` and keeps its non-positive-ignored rule;
  `NewScheduleStore` / `NewFencedQueueStore` accept options and document that
  lease is ignored. A caller that only passes `WithLease(d)` compiles
  unchanged. **A caller that constructs, converts, invokes, returns, or wraps
  its own `QueueOption` as `func(*Queue)` breaks** — accepted at v0.x. The
  option type is now named three ways across the jobs adapters (memstore
  `Option`, pgx `Option` + alias, turso `QueueOption`); recorded, not fixed
  (a turso rename would cost turso retags this train avoids).
- Advisory locks (`pg_advisory_xact_lock` in authorization and the fenced
  jobs queue) are database-scoped, not schema-scoped: two hosts sharing one
  database AND the same lock-key text contend across schemas. Not a
  regression — true under the `search_path` pin too.
- Per-repository DIFFERENT schemas within one feature are out of scope: the
  constructors mechanically allow it, the one-stream-per-schema migration
  model gives it no story. `stores/turso` is untouched — SQLite has no
  schemas; the package docs that claimed "same exported surface" as the turso
  sibling now say "plus the Postgres-only `WithSchema` option".

**Pin moves and the sdk floor.** Every store pins `pgxdb v0.5.0`
(authentication from v0.4.0; authorization/cms/events/jobs from v0.1.0).
pgxdb requires `sdk v0.4.0`, so MVS drags `sdk` along — authorization/cms/
events move from `sdk v0.1.0`, jobs from `v0.2.0`. **Adopting any of these
four store tags raises your effective `sdk` floor to v0.4.x — read the sdk
v0.3.0 note (global middleware genuinely global; `HandleRaw` no longer
bypasses it) before adopting.** Compile-safety of the drag was cold-verified
(`GOWORK=off` builds of the four feature cores against sdk v0.4.2). No
feature-core, sdk, or turso module is bumped.

**Adoption (gps-360-go shape).** Delete the `search_path` DSN wrapper, then:

```go
authSchema, err := pgxdb.NewSchema(os.Getenv("AUTH_DB_SCHEMA")) // "auth"
if err != nil { log.Fatal(err) }

// Two streams, two calls, host-chosen order; no cross-schema atomicity.
if err := pgxdb.RunMigrations(ctx, db, hostMigrationsFS, "migrations"); err != nil { … }
if err := pgxdb.RunMigrations(ctx, db, featureMigrationsFS, "migrations/auth", pgxdb.WithSchema(authSchema)); err != nil { … }

authRepos, err := authenticationpgx.Repositories(db, authenticationpgx.WithSchema(authSchema))
iamRepos, err := authorizationpgx.Repositories(db, authorizationpgx.WithSchema(authSchema))
jobRepos := jobspgx.Repositories(db, jobspgx.WithSchema(authSchema))
if err := jobspgx.StatusCheck(ctx, db, jobspgx.WithSchema(authSchema)); err != nil { … } // cms likewise
```

If your ledger rows live in `public.schema_migrations`, run the README's
relocation preflight first. Verification for this train: `make check` green
(18 guards); `make test-stores` now runs every pgx leg twice (default and
`POSTGRES_TEST_SCHEMA=gopernicus_schema_test`) plus the pgxdb connector's own
live suite, and the schema legs of authorization and events were re-run
against an EMPTY `public` so any unqualified statement would have raised;
decoy tests in events and authentication prove a `public` twin of the same
table is neither read nor written when a schema is configured.

### features/authentication — v0.6.0 (next tag): the lifecycle routes come standard behind a host gate (minor; MachineRoutesDisabled REMOVED — a source break; ungated routes no longer mount)

authentication-machine-routes-gate (plan of record
`plans/authentication-machine-routes-gate.md`, gopernicus issue #6). A **minor**:
an intended behaviour change — the bundled machine-identity lifecycle routes no
longer mount without a host authorization gate — plus one removed `Config` field.
It supersedes the v0.5.4 caveat below, which stays as ledger history.

- **REMOVED: `Config.MachineRoutesDisabled bool`** (and its
  `authsvc.Deps.MachineRoutesDisabled` / `Service.MachineRoutesEnabled()`
  plumbing). A **compile break** for any keyed literal that sets it; the one
  known caller is gps-360-go `cmd/server/authentication.go:50`, a single line it
  deletes while adopting the gate — nil `MachineRoutesGate` is the same posture.
  Removed one day after it shipped rather than deprecated: "off even if a gate is
  set" is a second way to spell nil, and the pre-1.0 rule the owner applied to
  authorization's `Config.Model` applies here. **`AUTH_MACHINE_ROUTES_DISABLED`
  becomes an inert environment variable** — a deployment that still sets it is
  silently ignored (same off-state), so drop it from your manifests.
- **NEW: `Config.MachineRoutesGate web.Middleware`.** Nil → `/auth/service-accounts`,
  `/auth/service-accounts/{id}/keys` and `/auth/api-keys/{id}/revoke` are **NOT
  mounted** (404, deny-by-absence) even with `Repositories.ServiceAccounts` +
  `APIKeys` wired, and `NewService` logs one WARN naming the field. Key
  AUTHENTICATION is untouched: `AuthenticateAPIKey`, `RequireServiceAccount` and
  the bearer path of `RequirePrincipal` still follow `MachineEnabled`. Non-nil →
  each of the five routes registers as **`RequireUser` → `RequireLiveSession` →
  (on the three POSTs) the browser-safe `Origin`/CSRF gate → the gate**
  (outermost first): the human credential class only — an API-key
  bearer, act-as-user or not, is **401**, so a key can never mint another key —
  then immediate revocation (a logged-out session is 401 within one round-trip,
  the invitation precedent), then — for a cookie caller on a mutation — the CSRF
  rung described below, then the host's policy, which writes its own denial
  (`features/authorization`'s gates write FS9 `permission_denied` 403; the
  bundled handlers write no 403 of their own). Typical value:
  `authorizer.RequirePermissionFixed("platform", "steward", "global")`. Set with
  the machine subsystem unwired (BOTH repositories nil — a half-wired pair fails
  earlier with `ErrMachineReposRequired`) → the new
  **`ErrMachineRoutesGateWithoutRepos`** at construction. Deliberately a
  SINGULAR `web.Middleware`, not the
  `[]web.Middleware` of `cms.Config.AdminMiddleware`: nil must mean "no policy,
  do not mount", where an empty slice would mean "mounted, ungated" — the very
  bug this field closes.
- **Request DTO — `POST /auth/service-accounts`.** `owner_user_id` is **refused
  by name with a 400** ("owner_user_id is no longer accepted; the caller is the
  owner, or name act_as_user_id"); an EMPTY value is ignored. The field is kept
  for **one release** so a v0.5.x client learns the rename instead of a generic
  decode error, then dropped — after which strict decode answers 400 for it
  anyway. Its replacement is **`act_as_user_id`**: empty with `act_as_user` → the
  caller owns the account (act-as-self names no id); non-empty → delegation to
  another user, allowed only because the route sits behind the gate.
  `act_as_user_id` without `act_as_user` → 400; an unknown `act_as_user_id` → 400
  `invalid reference` (the service now verifies the act-as owner EXISTS —
  previously a typo minted a live principal for a subject nobody could
  deactivate; an unwired user rail fails closed with `ErrIdentityUnavailable`).
  Create and mint tighten their request hardening. The unknown-field 400 is NOT
  new (v0.5.x already decoded with `DisallowUnknownFields`); what changes is the
  **1 MiB body cap (413)**, **trailing data after the JSON value → 400**, and a
  **required `Content-Type: application/json` → 415** otherwise. **Response DTOs
  are byte-stable** — `owner_user_id` stays in the response, mint still returns
  `key` (now with `Cache-Control: no-store`, like every other secret-bearing
  response), revoke still answers 200 `{"status":"revoked"}`.
- **CSRF — cookie clients must double-submit on the three POSTs.** Create, mint
  and revoke now carry the same browser-safe `Origin`/CSRF gate as
  `/auth/admin/*`: a cookie-authenticated caller reads `GET /auth/csrf` and
  echoes the `__Host-auth_csrf` value in `X-CSRF-Token`, from an origin in
  `Config.AllowedOrigins`. A missing or mismatched token is **403
  `permission_denied`**; a non-allowlisted origin is **403 `origin_rejected`** —
  both BEFORE the host's gate runs. **Bearer-only clients are unaffected** (the
  gate short-circuits when there is no session cookie), and the two GETs are
  body-less reads that never carry it. A cookie-driven admin UI built against
  v0.5.x must add the bootstrap+echo step or it will see 403s.
- **Adopter audit — ghost owners written under v0.5.x.** The new act-as owner
  existence check covers NEW writes only; `userDeactivated` still reads an
  unknown id as ACTIVE, so any act-as account created with a bogus
  `owner_user_id` before this tag still mints live principals nobody can
  deactivate. Before deploying, list them and revoke their keys:

  ```sql
  SELECT id, owner_user_id FROM service_accounts
   WHERE act_as_user AND owner_user_id NOT IN (SELECT id FROM users);
  ```
- **Three new security-event types** (free strings at rest, no store change):
  `service_account_created` (`UserID` = the act-as owner; Details
  `service_account_id`, `act_as_user`, `delegated`), `api_key_minted` (`UserID` =
  the act-as owner the key authenticates as, else empty; Details `key_prefix`,
  `service_account_id` — never the raw key or its hash) and `api_key_revoked`
  (Details `key_id`), emitted **per successful CALL, not per state transition** —
  a replayed revoke of an already-revoked key writes a second row, because the
  revoke path holds only the key id and `APIKeyRepository` has no Get-by-id with
  which to read the prior state (a state-aware event waits on the deferred
  account-scoped revoke train). `Actor` is the principal resolved from
  the request context, never the caller-supplied `createdBy` string. Readers that
  filter by `event_type` are unaffected.
- **Scope, stated plainly:** behind the gate the list is the GLOBAL one and
  mint/revoke take any id — this train fixes OWNERSHIP, not per-object
  authorization. A creator-scoped list or an account-scoped revoke needs a port
  change (`ServiceAccountRepository.List` has no filter dimension;
  `APIKeyRepository` has no read-by-id) and a two-store train; a host that needs
  either leaves the gate nil and serves its own routes over the unchanged
  `CreateServiceAccount` / `MintAPIKey` / `ListServiceAccounts` / `ListAPIKeys` /
  `RevokeAPIKey`.
- **No store retags.** No port method, no DDL, no migration, no `go.mod` change:
  the new event types are free `TEXT`, and `service_accounts` / `api_keys` keep
  their columns. `features/authentication/stores/{pgx,turso}` stay at v0.4.0 /
  v0.3.0.
- **The standard unkeyed-literal caveat:** `Config` gains one field and loses
  one, so any UNKEYED `auth.Config{…}` literal breaks. None exists in-repo or in
  gps-360-go (grep 2026-08-26); keyed literals are unaffected except for the
  removed field.

**Adoption — gps-360-go.** It builds authentication at `cmd/server/main.go`
BEFORE authorization, so either move the authorization boot above the
authentication config or set `MachineRoutesGate` on the config after the
authorizer exists and before `NewService`. Then replace
`MachineRoutesDisabled: true` (`cmd/server/authentication.go:50`) with
`MachineRoutesGate: authorizationComponents.Service.RequirePermissionFixed(...)`
on its platform/steward coordinate, and delete `features/auth/inbound/machine.go`
+ `machine_test.go` (plus the `machine` field of `inbound.Auth` and the
`MachineService` parameter of its `New`). Deleting those routes is **four
client-visible divergences at once**: the path (`/api/v1/service-accounts*` →
`/auth/service-accounts*`), the mint body (`{"key":{…},"plaintext":…}` → the flat
`{…,"key":…}`), the revoke status (204 → 200 `{"status":"revoked"}`), and
`created_by` (`"user:<id>"` → a bare user id, with existing rows keeping the old
format — one column, two formats, no backfill). Repoint the key-holding clients
or keep a thin host adapter. gps's blanket `act_as_user ⇒ 400` refusal becomes a
gate-protected capability; a host that still wants no delegation keeps its own
create route (own-the-routes) and adopts the bundled ones for the rest.

**Adoption — coordination-hub and any other v0.5.x host.** If the machine
repositories are wired, the five routes answer 404 and boot logs the WARN until
`MachineRoutesGate` is named. Once named, run the ghost-owner audit query above
and repoint any cookie-driven admin UI at the CSRF bootstrap. Nothing else
moves: key authentication,
`RequirePrincipal`, `RequireServiceAccount` and `RequireLiveSession` are
unchanged, and no store or migration is touched.

### features/authentication — v0.5.4 (2026-08-25): MachineRoutesDisabled + a caveat on the bundled lifecycle routes (patch by owner ruling)

authentication-machine-routes-toggle (plan of record
`plans/authentication-machine-routes-toggle.md`). A **patch by owner ruling**:
one additive `Config` bool whose zero value preserves every route; no schema,
no `go.mod` change, no store retags.

- **`Config.MachineRoutesDisabled bool`** (`env:"AUTH_MACHINE_ROUTES_DISABLED"`)
  keeps API-key AUTHENTICATION on (`AuthenticateAPIKey` and the bearer path of
  `RequirePrincipal` follow `MachineEnabled` as before) but mounts NONE of the
  bundled lifecycle routes — `/auth/service-accounts`,
  `/auth/service-accounts/{id}/keys`, `/auth/api-keys/{id}/revoke` — so they
  answer 404 (deny-by-absence, the `PasswordFlowsDisabled` shape). A host then
  serves its own gated routes over `CreateServiceAccount` / `MintAPIKey` /
  `ListServiceAccounts` / `ListAPIKeys` / `RevokeAPIKey`.
  `Service.MachineRoutesEnabled()` is the accessor the inbound layer reads.
- **CAVEAT (unchanged behaviour, now documented):** the bundled lifecycle
  routes are gated on `RequireUser` — ANY authenticated user — and are
  UNSCOPED: `POST /auth/service-accounts` takes `owner_user_id` from the
  request body (any user can create an act-as-user account owned by ANY other
  user and mint its key — impersonation), `GET /auth/service-accounts` lists
  every account, minting and revocation accept any id. That is acceptable for
  a single-admin host and is not for a host with several trust levels. Such a
  host should set `MachineRoutesDisabled` today; binding `owner_user_id` to the
  caller and scoping list/mint/revoke to the creator is a separate, behaviour-
  changing fix that needs an owner ruling and is NOT in this tag.

### features/authentication — v0.5.3 (2026-08-25): PasswordFlowsDisabled — the password credential as a posture (patch by owner ruling)

authentication-password-flows-toggle (plan of record
`plans/authentication-password-flows-toggle.md`). A **patch by owner ruling**:
one additive `Config` bool whose zero value preserves every existing route, no
schema, no `go.mod` change, no store retags.

- **`Config.PasswordFlowsDisabled bool`** (`env:"AUTH_PASSWORD_FLOWS_DISABLED"`)
  turns the password credential OFF for a host whose only way in is OAuth or
  passwordless. `Register` then mounts NONE of the password routes —
  `/auth/register`, `/auth/login`, `/auth/verify`, `/auth/verification/resend`,
  `/auth/password/{forgot,reset,change,set,remove,remove/start}`,
  `/auth/step-up/password`, and the HTML twins of the public ones plus the
  password account pages — the same deny-by-absence shape machine identity and
  the token endpoint already use, so a disabled host answers **404**, not 4xx
  from a live handler. `GET /auth/login` (the HTML page hosting OAuth entry)
  stays mounted.
- **Service half:** `Register`, `Login`, `IssueToken`, `ForgotPassword`,
  `ResetPassword`, `ChangePassword`, `SetPassword`, `RemovePassword` refuse
  first with **`ErrPasswordFlowsDisabled`** (wraps `sdk.ErrNotFound`, matching
  the 404) before touching any store — a host reaching the `Service` directly
  gets the unmounted route's answer. `PasswordFlowsEnabled()` is the accessor
  the inbound layer reads.
- **Unchanged:** `Hasher` stays required (the credential rail is shared);
  OAuth, passwordless, sessions, refresh, logout, step-up-by-code, identifiers,
  machine identity, and admin routes are untouched. Hosts that previously
  answered the password routes with their own 404 middleware can delete it.

### sdk — next tag (2026-08-25): crud.MapPageErr — the fallible row→domain page bridge (patch by owner ruling)

pgxdb-probe-table-map-page-err (plan of record
`plans/pgxdb-probe-table-map-page-err.md`). A **patch by owner ruling**
(one additive generic function; the `sdk/v0.4.1` precedent). No in-repo module
pins move.

- **`crud.MapPageErr(p Page[T], fn func(T) (U, error)) (Page[U], error)`** is
  `MapPage` for a mapper that can fail. A store whose `toDomain` VALIDATES the
  stored row (a vocabulary outside the domain's contract must fail loud, never
  enter the domain) previously had to re-implement the page copy by hand around
  its error loop; now it is `crud.MapPageErr(page, row.toDomain)`. Fail-fast: the
  first error comes back unwrapped with a zero `Page` (no partial items, no
  cursors); on success every pagination field is copied unchanged exactly as
  `MapPage` does, and nil `Items`/`Total` stay nil.
- **`MapPage` is untouched.** Infallible mappers keep using it.

### integrations/datastores/pgxdb — next tag (2026-08-25): ProbeTable — the boot-time table probe (patch by owner ruling)

pgxdb-probe-table-map-page-err (plan of record
`plans/pgxdb-probe-table-map-page-err.md`). A **patch by owner ruling**
(one additive function; no `go.mod` change — still `sdk v0.4.0`).

- **`pgxdb.ProbeTable(ctx, q Querier, table string) error`** is the existence
  probe every pgx store constructor already carried privately
  (`features/{authentication,authorization,events}/stores/pgx`): `SELECT
  to_regclass($1)::text` with the name bound as a parameter (bare, or
  schema-qualified like `gps.organizations`). Absent → wraps `sdk.ErrNotFound`
  naming the relation; a query/infrastructure failure maps through `MapError`
  and is **never** misreported as a missing table. Accepts a `*DB` or a `*Tx`.
  Run it in a store constructor so a host aimed at a database without its
  tables fails at wiring time, naming the table, instead of 500ing on the
  first query.
- **The framework's own store copies do not move on this tag.** They adopt
  (`fmt.Errorf("…: %w", pgxdb.ProbeTable(…))`, keeping their
  migration-source wording) when each store module next retags and pins this
  pgxdb version — deliberately not forced here.

### features/authentication — next tag v0.5.2 (2026-08-25): completed account mutations stop landing silently (patch; REDIRECT TARGETS AND RENDERED COPY CHANGE)

Defect fix (Segovia flag #19). Every auth page model embeds `PageContext.Message` and
every bundled page body renders it, and the feature already populates that slot across
a redirect in four places (`?sent=1` on forgot / password-remove / step-up /
oauth-unlink). The account page was never wired in: all seven completed account
mutations 303'd to a bare `/auth/account`, the verify-link completion did the same, and
the OAuth callback's `ActionLinked` — the successful explicit link — 302'd with no
marker at all, while `accountPage` read zero query params. A host's user changed their
password, confirmed an identifier, or linked Google and the destination said **nothing**.

The `?auth=<code>` vocabulary `pendingLinkRedirect` established (`auth=link_sent`) is
now the one outcome vocabulary for the whole feature, extended rather than duplicated:

| Flow | Destination | Code |
| --- | --- | --- |
| password change / set / remove | `/auth/account` | `password_changed` / `password_set` / `password_removed` |
| identifier confirm / remove / set-uses | `/auth/account` | `identifier_confirmed` / `identifier_removed` / `identifier_updated` |
| OAuth unlink | `/auth/account` | `provider_unlinked` |
| pending-link completion (`POST /auth/oauth/verify-link`, form arm) | `/auth/account` | `provider_linked` |
| OAuth callback, **`ActionLinked` only** | the flow's validated destination | `provider_linked` |
| form logout | `/auth/login` | `signed_out` |
| password reset | `/auth/login` | `password_reset` |

Adopter-facing surface:

- **No exported API change.** No new or changed exported type, field, method,
  constant, or route; no schema; no `go.mod` change (still `sdk v0.4.x`); no store
  retag. A host implementing `Views` by hand compiles unchanged, and the JSON arm of
  every one of these endpoints is byte-stable — this is the HTML/browser lane only.
- **Redirect `Location` values change.** The eight account-destination flows now 303
  to `/auth/account?auth=<code>`, logout and reset to `/auth/login?auth=<code>`, and
  the callback's completed-link branch appends `auth=provider_linked` to the target it
  already redirected to. A host asserting the exact old `Location` string in its own
  tests must update those assertions; nothing else observes them.
- **A new query param can arrive at a HOST url.** Only on the callback's `ActionLinked`
  branch, and only on the destination the link flow already validated — the same shape
  `auth=link_sent` has taken since the pending-link work. It is additive and ignorable:
  existing query values and any fragment are preserved, and a host reading `?auth=`
  through a closed whitelist (the recommended posture) simply does not match the new
  code. `ActionLogin` / `ActionRegister` are deliberately **unmarked** — they land on
  the app, not an auth page.
- **Two pages render new copy.** `accountPage` and `loginPage` now read `?auth=`
  through **route-specific closed maps**: an account code is unknown on login and a
  login code is unknown on account, so no destination can be made to show copy for
  something that could not have happened there. Unknown, empty, malformed and
  wrong-destination codes render **no notice**, and the wire value is never returned by
  the mapper and never reaches markup — the same closed-whitelist posture the four
  `?sent=1` readers already hold. The copy is generic and enumeration-safe and
  deliberately **names no provider**: "That sign-in provider was linked to your
  account.", not "Google". The account page lists the linked provider in its own masked
  inventory two rows below the notice.
- **The notice slot has no tone.** `PageContext.Message` renders success and failure
  identically as a polite `<p role="status">`, and the login page now carries both form
  errors and these outcomes. A host styling `[data-slot="auth-message"]` should keep
  that treatment neutral. A structured `PageContext.Tone` is a separate, unshipped ask.

**Why a patch.** The bugfix arm, with the same reasoning as v0.5.1: a defect fix with
no exported-symbol, signature, schema, `go.mod`, or mandatory-configuration change,
adoptable on a maintenance line with no host action. It is louder than v0.5.1 only in
that the change is visible in `Location` headers as well as rendered bytes, which is
why both are stated plainly above rather than left to be discovered.

**`views/goth` does NOT retag.** It already renders the notice slot on every page body
and its source is untouched by this change; its `features/authentication v0.5.1` pin
upgrades at the host through MVS.

### features/authentication v0.5.1 + views/goth v0.2.2 (2026-08-23): the identifier edit form stops offering a removal the policy refuses (patch pair)

Defect fix one layer deeper than v0.2.1 (Segovia flag #18). The credential policy's
`Removable` hint reached the account page but never reached the identifier
**management form**, so `GET /auth/identifiers/{id}/edit` always rendered a live
"Remove this identifier" button — including for the account's only recovery-capable
address. The owner clicks, and the mutation refuses with the generic
`accountPolicyMsg`. The form now offers removal only where the policy would allow it,
and otherwise renders the same muted explanation v0.2.1 introduced:

> Removing this would leave your account without a way to sign in. Add another
> sign-in method first.

The **copy const is reused, not duplicated** — the account page's suppressed unlink
and the form's suppressed remove describe the same policy, and the sentence names no
method kind, no policy rule, and no contact value.

- **`IdentifierFormPage.Removable bool`** (features/authentication) — an additive
  exported field carrying the identifier's advisory removable hint to the form. The
  handler reads it from the seam it already calls, `populateIdentifierEdit`'s
  `Service.Methods` inventory — the same masked projection the account page renders —
  so **no policy logic is re-derived in the transport layer** and the page and the
  guard cannot disagree about what the account has. The server-side refusal in
  `account_forms.go` is untouched and remains the authoritative backstop; the hint
  stays advisory (the mutation re-runs the policy under revision serialization).
- **The account page's identifier row is unchanged.** It renders only "Manage", an
  edit link — editing an identifier's uses is always allowed — so there is no removal
  affordance there to gate. The only direct remove control for an identifier lives
  inside the edit form, which is what this change gates.

**Zero-value semantics — a deliberate flip, stated plainly.** A zero
`IdentifierFormPage` has `Removable == false`, so the zero model (and any edit model
the handler could not resolve to an identifier the caller owns) now renders the
explanation instead of a live remove form. That **changes the bytes of the zero
model's edit rendering**, and it is the choice we want: never offer a mutation the
policy might refuse. It is also the honest rendering of an unresolved form — a page
that cannot name the identifier has no business offering to delete it. Both shapes
are pinned by test (`TestIdentifierForm_ZeroModelFailsSafe`), as is the **removable**
rendering, which is byte-identical to v0.2.1 (`TestIdentifierForm_NonRemovableExplained`);
the add and confirm modes have no removal region and are untouched.

**Patch for both, cut as a pair.** Bugfix arm of the bump rules. For
`features/authentication`: no port method, no interface, no signature, and no schema
changes — one additive exported struct field with a fail-safe zero value, following
the `Config.OAuthLinkBaseURL` (v0.3.1) / `sdk v0.3.1` precedent that an additive
symbol may ship as a patch. A host that implements the `Views` port **by hand
compiles unchanged** and keeps its current rendering; only an **unkeyed composite
literal** of `IdentifierFormPage` would break, and hosts consume that model rather
than construct it (the `InviteCheckRequest` v0.4.1 precedent for naming this). No
store retags; `go.mod` unchanged (still `sdk v0.4.0`). For `views/goth`: template and
copy only, no exported Go symbol, no new kit class, no new asset, and **no change to
the bundle's browser `Requirements`** — no adopter needs to re-derive a CSP header.
They tag together only because the views module reads the new field, so its pin moves
to `features/authentication v0.5.1` at the release commit (`sdk v0.4.0` and
`ui/goth v0.1.0` unchanged). A pin move to a sibling **patch** does not floor a minor;
v0.2.0 was a minor because the `Views` port gained a method.

Adopter note: a host that byte-pins the identifier edit form's markup, or that
translates the shipped copy, should re-capture it. A host that overrides
`IdentifierForm` renders its own page and is unaffected — though it now has the field
available and should gate its own remove control on it.

### features/authentication/views/goth — v0.2.1 (2026-08-21): a suppressed unlink control now explains itself (patch)

Presentation-only defect fix (Segovia flag #17). When the credential policy reports
a linked OAuth account as non-removable — the common case being an account whose
only sign-in method is that link — the bundled account page rendered the row with
no unlink control **and no text at all**, which reads as a broken affordance. The
row now carries a muted explanation in place of the suppressed control:

> Removing this would leave your account without a way to sign in. Add another
> sign-in method first.

It mirrors the generic copy the feature already returns when a removal is refused
server-side, stated ahead of the attempt and paired with the remedy; it names no
policy rule, method, or contact value. Rendered with the existing
`ui/goth` `Typography` muted recipe — **no new kit class, no new asset, and no
change to the bundle's browser `Requirements`**, so no adopter needs to re-derive
a CSP header.

A **patch** per the bump rules' presentation clause: no exported Go symbol is
added or changed (the copy is an unexported const), the `Views` port and every
view model are untouched, `go.mod` is unchanged (`features/authentication v0.5.0`,
`sdk v0.4.0`, `ui/goth v0.1.0`), and `features/authentication` does **not** retag —
the guardrail, the `Removable` computation, and every handler are unchanged. The
LinkableProviders change in v0.2.0 was a minor only because the port gained a
method; new rendered copy on its own does not floor a minor.

Adopter notes: a **removable** method's row is byte-identical to v0.2.0 (pinned by
test), so only the previously-empty non-removable case changes. A host that byte-pins
the account page's linked-accounts markup, or that translates the shipped copy, should
re-capture it; a host that overrides `AccountSecurity` renders its own page and is
unaffected.

### features/authentication v0.5.0 + views/goth v0.2.0 (2026-08-21): OAuth linking completed in the bundled HTML surface (minor floor)

Closes the two browser-side gaps in the OAuth linking story (Segovia flags
#15/#16, plan of record `.claude/dashboards/04-account-oauth.md` in that repo),
and teaches the browser-lane redirect resolver to honor safe same-origin relative
paths. Additive only —
no schema change, no store retags, no `go.mod` change (`features/authentication`
still pins `sdk v0.4.0`) — but the **`Views` port gains a method**, so it is a
minor floor for both modules and they are tagged together (the goth module
implements the new method). `views/goth v0.2.0` is the module's first retag
since the `v0.1.0` first cut, so its pins move with it per the tagging scheme:
`features/authentication v0.5.0` (the port it implements) + `sdk v0.4.0` (the
feature's own floor); `ui/goth` stays at `v0.1.0` (zero drift since that tag,
no retag).

- **`AccountSecurityPage.LinkableProviders []string`** — the wired OAuth providers
  the caller has NOT linked, in the service's deterministic order. The bundled
  account page renders a "Link an account" list, one anchor per provider, pointing
  at the existing `GET /auth/oauth/{provider}/link/start?redirect=/auth/account`;
  previously the link-start route was unreachable from any shipped page. The field
  is empty when OAuth is off or every wired provider is already linked, and an
  empty value renders **byte-identically** to the pre-affordance section (pinned by
  test). The `/auth/account` destination needs no `Config.RedirectAllowlist` entry
  — see the relative-path resolver change below.
- **Browser-lane redirect resolution honors safe same-origin relative paths.**
  `Service.ResolveRedirect` — shared by the OAuth flow start and the HTML form
  return-to — now honors any safe root-relative path (the existing
  `redirect.SafeRelativePath` rule: single leading slash, no `//`, backslash,
  scheme, or control bytes) WITHOUT an allowlist entry; a relative path is never
  an off-site open-redirect vector. Absolute targets still require an exact
  `Config.RedirectAllowlist` match, and anything else still falls back to `/`.
  Behavior change: a relative `redirect`/`return_to` that previously fell back to
  `/` in the OAuth lane is now honored (the HTML form lane already honored it).
  The **invitation lane is unchanged** — its destination is embedded in a mailed
  link, so it stays exact-match only.
- **`Views.OAuthLinkLanding(m OAuthLinkPage) web.Renderer`** — a NEW port method
  (with the re-exported `authentication.OAuthLinkPage` model). A host that embeds
  the bundled `views/goth` default gets it for free; a host that implements the
  port **by hand must add it** (the `features/cms/views/goth` `EntriesListContent`
  precedent).
- **`GET /auth/oauth/link`** — a PUBLIC HTML landing page completing the
  anti-takeover pending-link branch, registered only when `Config.Views` is wired
  AND a provider is configured. It is public by construction: the caller holds the
  mailed single-use secret and no session (the flow is what mints one). It reuses
  the existing externalized `fragment.js` reader — the token rides the mailed
  link's `#token=` fragment, is scrubbed from history client-side, and never
  reaches the server on the landing GET. Point `Config.OAuthLinkBaseURL`
  (`AUTH_OAUTH_LINK_URL`) at this route (`https://<host>/auth/oauth/link`) and the
  clickable pending-link email from v0.3.1 now lands somewhere real; hosts with an
  SPA landing keep theirs unchanged.
- **`POST /auth/oauth/verify-link` gains the form arm** of the standard
  content-type dispatch. The **JSON contract is unchanged** (same body, same user
  JSON response, same strict decoding); a urlencoded body completes the same
  `VerifyLink` service method, sets the session cookies, and **303s to
  `/auth/account`**, where the new link appears in the masked inventory — the
  destination every completed link/unlink mutation already PRGs to. Like the
  reset/redeem landings it carries the credential-establishment origin policy and
  no double-submit CSRF gate (the caller has no session to bootstrap one from). A
  dead or unknown secret re-renders the landing at the mapped status with generic
  copy and echoes no token (it is single-use and the attempt consumed it). With a
  nil `Views` a form body is still 415 — the API-only posture is untouched.

### features/authentication — v0.4.2 (2026-08-21): authorized invitation operations promoted to the public facade (patch)

Same-day follow-up to v0.4.1 by owner ruling. Purely additive facade surface —
two delegate methods, no behavior change anywhere else, no schema change, no
store retags, `go.mod` unchanged (still `sdk v0.4.0`).

- **`Service.CreateAuthorized(ctx, principal, in)`** and
  **`Service.ListByResourceAuthorized(ctx, principal, resourceType, resourceID, req)`**
  are now public on the facade `authentication.Service` — the policy-carrying
  twins of the trusted `Create` / `ListByResource`, for hosts writing their OWN
  invitation handlers instead of mounting the shipped routes. They guard
  `ErrInvitationsDisabled` like their trusted siblings, prepare the request
  (metadata validation, identifier normalization, invitee lookup), pose
  `Config.InviteCheck` with the complete invitee context, and only then act; a
  denial leaves no pending row and attempts no grant. `principal` is the
  resolved caller (the inviter), never the invitee.
- The trusted `Create` / `ListByResource` remain check-free and unchanged; a
  host driving them directly still owns the authorization decision itself.

### features/authentication — v0.4.1 (2026-08-21): authorized invitation operations with invitee context + owner metadata projection (patch by owner ruling)

invitation-metadata-host-seams proposals 1 and 3 (plan of record
`plans/invitation-metadata-host-seams.md`, amending `plans/invitation-metadata.md`).
Cut as a **patch by owner ruling** despite additive exported symbols — the
`sdk/v0.3.1` / `features/authentication/v0.3.1` precedent: it completes the
v0.4.0 metadata contract rather than opening new surface. No schema change, no
store retags, `go.mod` unchanged (still `sdk v0.4.0` — the feature does not
consume the sdk v0.4.1 wrapper).

- **`InviteCheckRequest` gains the invitee context**: `Identifier` (the
  feature-normalized invitee identifier), `IdentifierKind`, and
  `ResolvedSubjectID` (the existing subject the identifier resolves to, or `""`
  when unknown or the kind is not resolvable). A host can now authorize the
  complete invitation — metadata against invitee state — before any row exists
  or grant is attempted. All three are empty for `InviteList`. `UserLookup` is
  email-kind-only today, so `ResolvedSubjectID == ""` is NEVER proof the invitee
  is new. **Unkeyed composite literals of `InviteCheckRequest` break**; keyed
  literals and func implementations are source-compatible.
- **New authorized service operations**: `CreateAuthorized(ctx, principal, in)`
  and `ListByResourceAuthorized(ctx, principal, resourceType, resourceID, req)`
  prepare metadata, normalize the identifier, perform the lookup, pose
  `InviteCheck`, and only then run the existing side-effect paths. The trusted
  `Create` / `ListByResource` composition methods remain check-free and
  behaviorally unchanged. The shipped HTTP create/resource-list handlers now
  reach the policy through the authorized operations instead of posing the check
  in the adapter; a nil check reached on an authorized path fails closed (500),
  never an allow. Practical consequence for hosts: a metadata-dependent refusal
  now lands on the **inviter at create time** instead of surfacing to the
  invitee at accept — or silently stalling on the best-effort
  resolve-on-registration path.
- **Response projections split.** The resource-owner projection (pending create,
  resource list, resend) now carries `metadata` (`omitempty`) so an owner can
  audit the routing they supplied; the recipient-facing `/mine` uses a separate
  projection with NO metadata field. Invitations without metadata keep
  byte-identical responses everywhere.

### sdk — v0.4.1 (2026-08-21): web.SafeDomainError — explicit host-facing safe wire errors (patch by owner ruling)

invitation-metadata-host-seams proposal 2 (plan of record
`plans/invitation-metadata-host-seams.md`). A **patch by owner ruling** (one
additive type + constructor; the `sdk/v0.3.1` precedent). The sdk tree is
otherwise byte-identical to `sdk/v0.4.0`; no in-repo module pins move.

- **`web.SafeDomainError`** (`NewSafeDomainError(public *Error, cause error)`)
  lets a HOST SEAM (an `InviteCheck` or `Granter` refusal) carry a deliberately
  public sentence to the wire through a vendored feature's handler.
  `ErrFromDomain` recognizes only this explicit wrapper — via `errors.As`,
  before its untouched kind switch — and returns the wrapper's `*Error`.
  `Unwrap` returns the **cause alone**, so `errors.Is`/`errors.As` keep matching
  the domain sentinel while the public `*Error` stays outside the unwrap chain;
  a zero-value wrapper falls through to the generic mapping instead of
  panicking.
- **Nothing else changes.** Bare sentinels, arbitrary wrapped `*web.Error`
  values, and feature-internal errors keep today's generic bodies — this is an
  explicit host-seam affordance, not permission for domain code to place user
  text on the wire. Example construction:
  `web.NewSafeDomainError(web.ErrStateConflict("already attached to another account"), sdk.ErrConflict)`.

### features/authentication v0.4.0 + both store modules v0.3.0 (2026-08-20): invitation metadata (minor; store migration REQUIRED)

invitation-metadata (`plans/invitation-metadata.md`). Adds an opt-in, host-defined
`Metadata map[string]string` that rides an invitation from create to the Granter
seam — a channel for a host to route its own facts (a firm id, a plan tier) from
invite-time to grant-time. The feature never interprets it; empty metadata
preserves today's grant semantics, so this is additive for hosts that do not opt in.

**`features/authentication` — additive public surface:**

- `GrantInput.Metadata`, `InviteCheckRequest.Metadata`, and `CreateInput.Metadata`
  (all `map[string]string`). Adding a field to the exported `InviteCheckRequest` /
  `GrantInput` is source-compatible for keyed struct literals and interface
  implementations; a host using an UNKEYED composite literal of either must switch
  to keyed literals — no such construction exists in-repo.
- `invitation.NewWithMetadata`, `invitation.ValidateMetadata`,
  `invitation.CloneMetadata`, and the `invitation.MetadataMax*` bound constants.

**Contract:**

- Metadata is opaque, host-owned routing data supplied at create, persisted, and
  echoed into `GrantInput` on accept, direct-add, and resolve-on-registration as a
  non-nil defensive copy (empty when none). The feature validates only shape/size:
  32 entries, 64-byte keys, 256-byte values, 4 KiB JSON-encoded total, UTF-8,
  non-empty keys — each violation wraps `ErrInvalidInput`. Nil/empty persists as `{}`.
- It is UNTRUSTED inviter input, never an authorization claim by itself:
  `InviteCheck` receives the same metadata (its OWN defensive clone, so a mutating
  policy cannot alter the persisted/granted value) to authorize the complete
  invitation at issuance, and a `Granter` applying any security-sensitive side
  effect from it MUST revalidate.
- The create route now bounds its body with the standard strict JSON decoder before
  decoding the unbounded metadata object.

**`features/authentication/stores/{pgx,turso}` — this tag ships HOST SCHEMA.**

New canonical migration **`0016_invitation_metadata.sql`** in both trees
(append-only; `0009_invitations.sql` stays byte-identical to its tag):

- `invitations.metadata` — `jsonb` (pgx) / `TEXT` (turso), `NOT NULL DEFAULT '{}'`;
  existing rows read back as an empty map. The store never writes JSON `null` (which
  would bypass the column default). Malformed stored JSON fails the read rather than
  surfacing a silent empty.

Both `Repositories()` constructors gained a **column** probe (`invitations.metadata`)
beside the existing probes, so a host that copied 0001–0015 and skipped 0016 fails
at wiring time naming the column rather than mid-request.

**Adopter order: apply 0016 BEFORE deploying a binary built against these tags.**

Live gates closed at authoring time: full `storetest` invitation conformance
including the new metadata round-trip against the live Turso playground database,
plus a populated-table upgrade + malformed-stored-JSON integration check. pgx unit,
migration-inventory, and cross-dialect parity tests pass; live pgx conformance was
not run this cut (no Postgres DSN available in the release environment).

### sdk — v0.3.0 (2026-08-14): global middleware is genuinely global + configurable CORS (minor)

coordination-hub-auth-upstream U1 (plan of record `.claude/plans/`
`coordination-hub-auth-upstream/`; Coordination-Hub upstream items 1b/3/5). Additive
API, so a **minor** floor — but it carries the batch's one silent behavior change,
which leads this note:

- **`WebHandler.Use` now wraps the ENTIRE mux, and `HandleRaw` no longer bypasses
  it.** Global middleware used to be baked into each registered pattern, so it never
  observed mux-generated 404s, method-mismatch 405s (the symptom that broke
  preflights to method-qualified feature routes), redirects, or `HandleRaw`
  registrations. It now runs once around dispatch, so all of those pass through
  panic recovery, request IDs, logging, and CORS. **This changes behavior for any
  existing sdk consumer that relied on `HandleRaw` as a policy escape hatch** — a raw
  OpenAPI/metrics/streaming handler that previously ran outside the global stack now
  runs inside it. `HandleRaw`'s contract is now "raw `http.Handler` + raw ServeMux
  pattern", never "no middleware". Route middleware order, global outermost-first
  order, and the automatic 405 `Allow` header are unchanged (the mux still routes).
  `Use` remains **boot-time-only**: it rebuilds the dispatch chain unsynchronized, so
  it must not be called once the server is serving.
- **`CORSWithConfig(CORSConfig{AllowedOrigins, AllowedHeaders, ExposedHeaders})`** is
  the new configuration entry point. `CORSMiddleware(origins)` is retained as a
  delegating compatibility constructor with byte-identical defaults. A nil
  `AllowedHeaders` keeps `Accept, Content-Type, Authorization`; a non-nil list
  REPLACES it (this is how a host opts in authentication's `X-CSRF-Token` — the sdk
  never names a feature's header).
- **`Access-Control-Expose-Headers` now defaults to `X-Request-ID`**, so cross-origin
  JavaScript can read the framework's own request id. An explicit empty
  `ExposedHeaders` suppresses the header entirely.
- **`Vary: Origin` is now set on every response** through the middleware, matched
  origin or not, appended without overwriting or duplicating an existing `Vary`
  dimension. A host running the ~6-line `varyOrigin` wrapper as a workaround can drop
  it; keeping it stays correct.
- **Only a genuine preflight short-circuits with 204** — an `OPTIONS` request must
  carry both `Origin` and `Access-Control-Request-Method`. Any other `OPTIONS`
  request now continues to the mux, which matters precisely because `Use` is global:
  a host's own `OPTIONS` route stays reachable.
- Wildcard behavior is unchanged: `*` may echo an origin but never emits
  `Access-Control-Allow-Credentials`.

### features/authentication — v0.2.0 (2026-08-14): browser cookie-flow seams (minor; pins sdk v0.3.0)

coordination-hub-auth-upstream U2–U4. Every change is additive — no existing request
body, response shape, or route was changed or removed — so a **minor** bump. The
module's `go.mod` now requires **`sdk v0.3.0`**: the documented cross-origin browser
flow depends on the CORS seam above, and pinning is what stops a host from adopting
`/auth/csrf` against an sdk that cannot be configured to allow the echo header.

- **`Config.RefreshCookiePath string`** (`AUTH_REFRESH_COOKIE_PATH`). Empty keeps
  today's `/auth`. A host mounting the feature under a prefix (`feature.PrefixRegistrar`)
  MUST set the full prefixed path — `/api/v1/auth` — or the browser never sends the
  refresh cookie and cookie-driven refresh dies silently. A non-empty value must be a
  valid absolute cookie path (leading `/`, no query/fragment/control/header-delimiter
  character, no trailing slash except `/`) or construction fails with the new
  `var ErrRefreshCookiePathInvalid`; `net/http` would otherwise silently drop bad
  bytes. The same resolved path is used for every issue AND the logout deletion, so a
  prefixed host clears exactly what it set. It configures ONLY the refresh cookie —
  the access cookie and the `SameSite=Lax` posture are untouched.
- **`GET /auth/csrf`** — the JSON double-submit bootstrap for a cookie-authenticated
  SPA on a different origin than the API (it cannot read the API-origin
  `__Host-auth_csrf` cookie). Gated by `RequireLiveSession` plus the existing
  origin-only browser gate; returns `{"csrf_token":"…"}` under
  `Cache-Control: no-store`, and the body value is always the value of the
  `__Host-auth_csrf` cookie on the same response. It REUSES a
  well-formed existing token rather than rotating (a second tab may hold it in
  flight) and fails closed — 500, no token, no cookie — if entropy is unavailable.
  The host must list `X-CSRF-Token` in its sdk CORS `AllowedHeaders` for the browser
  to send the echo header.
- **The double-submit CSRF cookie is renamed `auth_csrf` → `__Host-auth_csrf`.**
  The `__Host-` prefix makes a browser refuse the cookie unless it is `Secure`,
  `Path=/`, and carries no `Domain` attribute — which is what stops a sibling host
  under the same registrable domain from planting a well-formed token (the
  fixation residual documented at v0.1.0). The cookie was already
  `Secure`/`Path=/`/no-`Domain`, so **no deployment change is required**; the only
  new failure mode is a plain-HTTP host, where the browser now refuses the cookie
  outright. The JSON lane (`GET /auth/csrf`) is new in this tag, so it has no
  migration concern. **The HTML views lane DID use `auth_csrf` at v0.1.0**: at
  upgrade, a form page rendered before the deploy carries a token whose old-name
  cookie the server no longer reads, so that one in-flight submission gets the
  generic 403 CSRF page; reloading the form re-mints under the new name and
  proceeds. The stale `auth_csrf` cookie is neither read nor deleted — it is
  simply orphaned, keeps riding along until the browser session ends (it was set
  without `Max-Age`/`Expires`), and is harmless.
- **`GET /auth/me`** — session hydration behind `RequireLiveSession` (one revocation
  lookup, the `GET /auth/methods` posture), returning the existing
  `{id,email,display_name,email_verified}` shape under `Cache-Control: no-store`. A
  machine/API-key principal is not a current user and gets 401, never a fabricated
  profile.
- **Login/register/`/auth/me` now render email through one service projection.** The
  compatibility DTO is built at a single site over the account's active email
  identity, so the address reported after registering `New@Example.com` is the
  normalized stored value on all three paths instead of the raw submitted string on
  one. A client that depended on the raw echo sees the normalized address.
- **`origin_rejected` splits out of `permission_denied`.** An origin-gate denial
  (both the mutation gate and the origin-only gate) now returns 403 with
  `code: "origin_rejected"`; a failed double-submit keeps `permission_denied`.
  Statuses and human messages are unchanged, so this is a diagnosability gain, not a
  client break — a host that boot-fails on a CORS/auth allowlist mismatch can now
  read the runtime symptom instead.

### sdk — v0.3.1 (2026-08-15): 409 vocabulary completed + Page end-of-list contract documented (patch by owner ruling)

The two upstream flags from coordination-hub's `coordination-api-consistency`
work. **Owner ruling:** this tag carries one additive exported symbol, which the
bump rules below classify as a minor; the owner cut it as a **patch** because it
is a defect fix in an existing contract, not new surface. Pre-`v1` Go semantics
make this safe for consumers either way.

- **`web.ErrStateConflict(msg)`** — the missing constructor for the
  state-conflict half of the 409 vocabulary (code `conflict`, pairing with
  `sdk.ErrConflict`). `web.ErrConflict` keeps code `already_exists` (pairing
  with `sdk.ErrAlreadyExists`) and both doc comments now cross-reference the
  sibling, so the two 409 kinds are no longer conflated by the only reachable
  constructor. `ErrFromDomain`'s `sdk.ErrConflict` arm now calls the new
  constructor instead of an inline literal; its mapping is behaviorally
  unchanged. A host that worked around this with
  `ErrConflict(msg).WithCode("conflict")` can revert to the plain constructor.
- **`crud.Page` end-of-list contract documented** (godoc + sdk README): every
  field except `Items` is omitempty, so a final page serializes as
  `{"items":[…]}` and clients must read absent `has_more`/`next_cursor` as
  false/empty — the normal end-of-list signal, not an error. Doc-only; no
  serialization change.

### features/authentication — v0.2.1 (2026-08-15): add-or-signup invitation lifecycle finished (patch)

coordination-hub `email-and-invitations` task U2 (plan of record, in the
Coordination-Hub repo, `.claude/plans/email-and-invitations.md`). Both changes are
additive/behavioral with no exported API change and no schema change, so a
**patch**. `go.mod` is unchanged (still `sdk v0.3.0`); no store module retags.

- **A brand-new OAuth-provisioned account now resolves its pending auto-accept
  invitations.** The OAuth register-and-link branch (branch 3 — no account claims
  the provider-verified email, so a password-less user is created and linked) calls
  the SAME best-effort invitation resolver `Register` and `Verify` already call,
  with the account's normalized stored primary email and its new user id. It runs
  after the user + verified primary identifier + provider link exist and BEFORE the
  session is minted, so the grants are effective before the caller ever holds a
  session token. The contract is unchanged: nil resolver (invitations off) is a
  no-op, a resolver error is a WARN line that never fails provisioning or the OAuth
  login, and the invitation service audits each grant/failure itself. **This was
  previously the one lifecycle hole in the add-or-signup product shape**: a host
  that invited an unknown address and had the recipient sign up with Google left
  the invitation pending forever.
  - **No other OAuth branch resolves.** Ordinary login of an already-linked
    identity (branch 1) and pending-link start/completion for an
    already-registered address (branch 2) are not provisioning events, so they
    never re-grant — an address that already belongs to an account is
    direct-added at invitation-create time instead. A host that adopts this tag
    therefore sees at most ONE grant per invitation per account, not one per
    sign-in.
  - Resolution is idempotent: a resolved invitation moves off pending, so a
    second pass is a no-op, and a grant that failed stays pending and is retried
    on the account's next resolve site.
  - Unchanged and deliberate: the invitee lookup that decides direct-add vs
    pending invitation is an ACCOUNT-EXISTENCE test, so an account still awaiting
    registration verification is direct-added like any other registered email.
    `Config.RequireVerifiedEmail` remains the independent password-login gate —
    such a user holds the relation but cannot log in until `Verify` succeeds.
    Non-auto-accept invitations are never resolved this way.
- **`invited_by` is added to the invitation JSON response** (pending create, the
  resource and `mine` lists, and resend all share one mapper). It is the owning
  user id — the same value cancel/resend ownership is already enforced on — so an
  admin-facing list can hide actions on another admin's rows. **Purely additive**:
  no field was removed or renamed, the response still carries no token, and the
  server enforces ownership regardless of what a client renders. A client that
  ignores the field is unaffected.

### integrations/email/sendgrid — v0.2.0 (2026-08-15): truthful capability metadata (minor)

coordination-hub `email-and-invitations` task U1 (plan of record, in the
Coordination-Hub repo, `.claude/plans/email-and-invitations.md`). Additive
(**minor**): `Sender` now implements `email.CapabilityReporter`. The report is
instance-sensitive — it describes the configured `Config.Host`, not merely the
package default:

- empty host (SendGrid's default `https://api.sendgrid.com`) or an explicit
  `https://` host → `{TransportSecurity: TLS, DevelopmentOnly: false}`;
- any non-HTTPS host (an httptest server, a local emulator) →
  `{TransportSecurity: None, DevelopmentOnly: true}` — a test instance can no
  longer claim production capability.

Why it matters to adopters: `features/authentication`'s production runtime mode
fail-closes on senders that declare no metadata or declare `DevelopmentOnly`.
Before this tag a SendGrid sender was indistinguishable from a console mailer to
that gate; after it, `AUTH_RUNTIME_MODE=production` can boot on a real SendGrid
instance while test-pointed instances still fail closed. No exported API
removed or renamed; `go.mod` unchanged.

### integrations/datastores/pgxdb — v0.3.0 (2026-08-14): durable rate limiter — this tag ships HOST SCHEMA (minor)

coordination-hub-auth-upstream U5. The Go surface is additive (**minor**): `Limiter`,
`NewLimiter(db *DB, opts ...LimiterOption)`, `WithLimiterKeyPrefix(prefix)` (default
namespace `ratelimit:`), and `(*Limiter).StatusCheck(ctx)`. `NewLimiter` satisfies
`sdk/capabilities/ratelimiter.Limiter` over the pool the host already owns — a
cross-instance limiter with no Redis, the `kvstores/goredis` multi-port precedent.
Semantics mirror goredis deliberately (`Requests + Burst` ceiling, independent keys,
sliding-window tail, `Reset`, `Remaining`/`ResetAt`/`RetryAfter`), every decision is
one atomic `INSERT … ON CONFLICT DO UPDATE … RETURNING`, all time is Postgres server
time (weights clamped, so a stale `now` can never over-count or exceed the window),
and a non-positive window or a zero ceiling is `sdk.ErrInvalidInput` rather than a
silent deny-everything. `Close` is an idempotent no-op and never closes the caller's
pool. Full reference DDL, pruning statement, and operational notes live in
`integrations/datastores/pgxdb/README.md` — this tag is **not** adoptable from the
Go API alone:

- **This release ships host schema, not just an API.** The limiter requires a
  host-owned `ratelimit_windows` table plus its `expires_at` index. The connector
  creates and migrates NOTHING. **Copy the reference DDL into your own migration
  ledger and apply it BEFORE deploying the binary that constructs a `Limiter`:
  schema first, then binary.** A binary deployed against a missing table fails OPEN
  and SILENT wherever the caller fails open (see the posture note below).
- **The DDL is a versioned contract with no compiler signal.** Nothing in Go tells a
  host that its copied table drifted from the shape this connector's SQL expects. A
  future column/index/semantic change to the reference DDL is therefore a
  **breaking** change for adopters even when the Go API is untouched, and MUST be
  released with an explicit schema upgrade note here.
- **Verify the table at boot.** `limiter.StatusCheck(ctx)` probes for it and reports a
  missing table as `sdk.ErrNotFound` (SQLSTATE `42P01`); refuse to start on error.
  Note it is a method on the concrete `*pgxdb.Limiter`, not on the
  `ratelimiter.Limiter` port — the host must hold the concrete type at boot to call
  it.
- **Pruning scheduling, autovacuum, and pool sizing are launch prerequisites, not
  follow-ups.** Every checked request is one non-HOT write (indexed `expires_at` is
  rewritten each `Allow`), so this becomes one of the highest-churn small tables in
  the database. The host schedules
  `DELETE FROM ratelimit_windows WHERE expires_at < now();` (cron, `features/jobs`, or
  pg_cron — the connector never runs it), tunes the storage parameters commented into
  the DDL, and sizes the pool for a limiter check on every rate-limited request. The
  limiter sets no internal deadline: it inherits the caller's context, so give that
  context a deadline you are willing to serve. `UNLOGGED` is a supported tradeoff
  (halves WAL, truncated on crash recovery); the limiter assumes the stock
  `READ COMMITTED` default and never auto-retries `40001`.
- **The `key` column is a durable PII surface.** It persists whatever the host puts in
  its keys verbatim — with authentication wired that includes client IP addresses and
  user identifiers — and it inherits the retention of your backups, WAL archive, and
  replicas. **Pruning is the retention control**, not just capacity hygiene: keep the
  table inside whatever DSR/erasure process covers request logs, prefer opaque or
  digested key material, and do not enable `Config.LogQueries` on a connection with
  the limiter wired (the tracer logs SQL args verbatim, IPs included, into logs that
  are retained longer and read more widely than this table).
- **Swapping `ratelimiter.NewMemory()` → `pgxdb.NewLimiter(db)` is a security-relevant
  behavior change.** A Memory limiter cannot fail; this one can, and today's callers
  disagree on posture: `sdk/capabilities/ratelimiter.Middleware` fails **OPEN** (the
  error is swallowed and the request proceeds unthrottled) while authentication's
  login and passwordless call sites fail **CLOSED** (the attempt is rejected; its
  refresh path fails open). After the swap a database incident is an availability
  event on the fail-closed paths and an unmetered brute-force window on the fail-open
  ones. Decide per path before the swap, and monitor limiter error rate and latency
  as first-class signals.

### integrations/datastores/{pgxdb,turso} — v0.2.0: crud.Transactor implemented (minor)

transactor-connectors (2026-08-14) implemented the sdk transaction seam in BOTH
datastore connectors — the seam's first consumer arrived (the Coordination-Hub
timeline cascade, gpsimpact/Coordination-Hub#13). Additive only, so a **minor**
floor: `(*DB).Transact` (commit on nil; rollback + fn's error unwrapped;
rollback + re-panic on panic), the connector-typed `TxFromContext(ctx) (*Tx,
bool)`, and `(*DB).QuerierFrom(ctx) Querier` (ambient tx when inside a
Transact, else the pool). `InTx` and every existing symbol are unchanged.
**Nesting is now RULED (was explicitly unpinned):** Transact inside an active
Transact fails loud with the connector's `ErrNestedTransact` — the consumer's
one-transaction-per-workflow invariant makes a silent nested begin an
atomicity-splitting hazard; the error can graduate into defined behavior later
without breaking anyone. The sdk contract comment (`sdk/foundation/crud/tx.go`)
records the ruling; comment-only, no sdk API change.

### sdk — v0.2.0 (2026-08-14): FencedDeadLetterFunc carries the failure reason

`workers.FencedDeadLetterFunc[T]` gained a `reason string` parameter —
`func(ctx, job T, reason string) error` — carrying the terminal failure reason
exactly as `Fail` durably recorded it (the runner cannot mutate an arbitrary
`T`, so the reason rides alongside the as-claimed job value). A breaking
exported signature change, released as a **minor** bump per the pre-`v1`
convention above. Any hook registered via `FencedRunner.SetDeadLetterHook`
adds the parameter; `features/jobs`' frozen host-facing `DeadLetterFunc` is
unchanged (see its v0.1.1 note).

### features/jobs — v0.1.1 (2026-08-14): DeadLetterFunc's job carries FailureReason

Behavior fix, no exported API change (**patch**; pins `sdk v0.2.0`): the
per-kind `DeadLetterFunc`'s `j.FailureReason` — structurally always empty
before this tag — is now populated with the recorded terminal reason before
the hook runs (Coordination-Hub issue #5). The AV3D-1.4 compile seam
asserting `DeadLetterFunc` ≡ `workers.FencedDeadLetterFunc[job.Job]` is
removed; the runtime's dispatch closure is the adapter. Hosts that retained
their own handler-error fallback for the empty field can drop it.

### features/jobs — v0.2.0 (2026-08-14): optional tenant metadata

Additive (**minor**): `job.Job`/`job.Enqueue`/`schedule.Schedule`/
`schedule.Ensure` gain `TenantID string` — an OPTIONAL, host-defined boundary
slot, vocabulary only (the events posture): empty = none, stores map `"" ↔
NULL`, the feature attaches no semantics. `jobs.FencedClaim` carries the
claimed job's `TenantID`. The fenced path gains struct-input siblings
`EnqueueOnceIn(ctx, EnqueueOnceInput{Kind, LogicalKey, Payload, TenantID})` /
`ReplaceIn(ctx, ReplaceInput{...})`; the frozen `work` protocol methods
(`EnqueueOnce`/`Replace`/`LatestStatusByKey`) are unchanged and delegate with
empty tenant — the sdk `work` protocol's vocabulary does NOT gain tenant. A
schedule's tenant is copied onto each job it fires (vocabulary carry-through so
tenant-scoped ops queries see fired work). No new query APIs: the column's
consumer is operator SQL. Pins `sdk v0.2.0`.

### features/jobs stores (pgx + turso) — v0.2.0 (2026-08-14): tenant_id columns

Per the greenfield-migrations rule, nullable `tenant_id TEXT` is folded into
the canonical CREATEs of `0001_job_queue.sql`, `0002_job_schedules.sql`, and
`0003_fenced_job_queue.sql` in BOTH dialects — **no evolution file ships**, so
an already-migrated host's runner will refuse the changed canonical files
(checksum mismatch) until the host applies its own host-tree migration and, if
its ledger keys canonical filenames, reconciles it per its own migration
policy. Reference host ALTER (both dialects; the partial index is the fenced
queue's — the encrypted-payload rail where SQL is the ONLY way to scope rows,
the reason this column exists at all):

```sql
ALTER TABLE job_queue        ADD COLUMN tenant_id TEXT;
ALTER TABLE job_schedules    ADD COLUMN tenant_id TEXT;
ALTER TABLE fenced_job_queue ADD COLUMN tenant_id TEXT;
CREATE INDEX IF NOT EXISTS idx_fenced_job_queue_tenant
    ON fenced_job_queue (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
```

Storetest conformance gains tenant round-trip cases plus a
tenant-does-not-affect-keying proof (enqueue-once dedup and Replace
supersession ignore tenant — it is metadata, never part of the logical key).
Both dialects passed live conformance for this tag (pgx against postgres:17,
turso against a real libSQL server).

### features/authentication — next tag: session hashing invalidates all live sessions

auth-v2 (2026-07-07) moved session-token storage to service-side SHA-256
hashing (design §7.3): the service hashes the cookie token before every
repository call, and stores keep persisting an opaque string — no DDL. Any
host upgrading `features/authentication` across this change must know:

- **Every live session is invalidated at deploy — a forced logout for all
  users, remember-me/long-TTL sessions included** (a v1 plaintext row never
  matches a hashed lookup again). Users just log in again; no data is lost.
- The orphaned plaintext rows are unreachable and dead past their natural
  `expires_at` TTL. No purge ships; hosts may vacuum them at leisure.
- **Deploy in a single cutover or drain traffic first — do not roll.** On a
  rolling deploy, mixed plaintext/hashed pods make the SAME session cookie
  flap 401/200 depending on which pod answers, for the whole rollout window.
- **A rollback forces a SECOND mass logout**: sessions minted by the new
  binary are hashed rows the old binary cannot read.

The same note lives in `features/authentication/README.md` (the upgrade note section).

### features/authentication stores — next tag: invitation identifier kinds

identity-resolution (2026-07-10) gave invitations an `identifier_kind` column
(`TEXT NOT NULL DEFAULT 'email'`) and widened the pending-tuple unique index
to include the kind. Per the greenfield-migrations rule (2026-07-12) the
column lives in `0011_invitations.sql`'s CREATE — no evolution file ships. A
host that scaffolded the pre-kind table adds the column with its own host-tree
migration (`ADD COLUMN` + drop/recreate of the pending-tuple index); rows
default to `email`, and hosts that only ever create email invitations see zero
behavior change.

### sdk — next tag: the layering split moved every sdk subpackage import path

sdk-layering (2026-07-10) re-homed `sdk/errs` into the root package
(`sdk.ErrNotFound`) and moved every other sdk package under
`sdk/foundation/` or `sdk/capabilities/` (package names unchanged, paths
only). Pre-tag there is no version obligation, but any consumer pinned
to a git SHA must re-path its imports wholesale; the workshop CLI's
emitted scaffolds already use the new paths.

### sdk — next tag: additive middleware symbols (minor floor)

middleware-consolidation (2026-07-11) added exported symbols to two sdk
packages, all backward-compatible — they floor sdk's next tag at a **minor**
bump, never a major:

- `sdk/capabilities/ratelimiter`: `Middleware` + the `Allower` port (the generic
  IP/key rate-limit middleware, relocated out of `features/authentication`'s
  internals).
- `sdk/foundation/web`: `TrustProxies` + `ClientIP` (the rightmost-minus-N
  client-IP resolver ported from the original gopernicus `httpmid`).

No existing symbol changed; a consumer that ignores the new surface is
unaffected.

### features/authorization — next tag: additive gate symbols (minor floor)

middleware-consolidation (2026-07-11) added `RequirePermission`,
`ResourceResolver`, and `FixedResource` to the root package (the exported HTTP
middleware gate; implementation in `internal/logic/authorizersvc`). Additive —
floors the next tag at a **minor** bump. Adopter note: replacing a hand-rolled
gate closure with `RequirePermission` changes the 401/403 response *body* to the
FS9 `web.Error` shape (status codes unchanged) — a client contract detail, not a
Go-API break.

### features/authentication — next tag: patch-only (internal delegation)

middleware-consolidation (2026-07-11) rewrote `Service.RateLimitByIP`'s body to
delegate to `ratelimiter.Middleware` with its exact prior signature and
semantics; no exported surface changed. This is a **patch**, not a minor — the
proof is auth's existing rate-limit tests passing unmodified. (Superseded for the
same tag by the refresh change below AND the auth-v3 identity cut, which force a
**breaking** bump; the additive HTML resource-policy seam — ui-goth GOTH-0.4 — folds
into that same breaking cut, see its note below.)

### features/authentication — next tag: JWT sessions + refresh rotation (BREAKING)

The refresh change (auth-jwt-session-refresh, 2026-07-11, amends AV6) makes the
access credential a self-validating JWT and turns the session row into the
revocation + refresh anchor. It re-keys `session.SessionRepository` and the
`Config`/`Service` surface — a **breaking version bump** for the feature. The
runbook (mirrored in `features/authentication/README.md`):

- **All live sessions invalidate at deploy — a forced logout for every user**
  (the sessions table is re-keyed; an upgrading host's own migration drops and
  recreates it). No data lost; users log in again.
- **Do NOT roll-deploy across the sessions re-key.** Old binaries SELECT the
  dropped `token` column and **error outright** (not a 401 flap). Stop old,
  migrate, start new.
- **Rollback = restore the old schema AND force a second logout** (the new
  binary's id-keyed rows are unreadable by the old binary).
- **`AUTH_JWT_SECRET` is now required — and required-SHARED for multi-instance
  hosts.** `Config.TokenSigner` is required (`ErrTokenSignerRequired` on nil);
  per-instance ephemeral keys cannot cross-verify behind a load balancer, so a
  multi-instance host MUST share one secret across every instance. Ephemeral
  keys are a single-instance dev convenience only.
- **`Config.TokenTTL` is removed** (compile-time break): replace with
  `AccessTokenTTL` (default 15m) and `RefreshTTL` (default 7d). No host silently
  inherits the old 1h access window.
- **`POST /auth/token` response changed** from `{token, expires_at}` to
  `{access_token, expires_at, refresh_token}` — a breaking client contract;
  API clients now rotate via `POST /auth/refresh`.
- **The session repository port was re-keyed** (id-keyed; `Get`,
  `GetByRefreshHash`, `Rotate`/`ConsumeGrace` CAS, `Delete`, `DeleteByUser`;
  new `ErrRotationConflict`) — a breaking bump for the feature AND both nested
  store-module tags (see below).

### features/authentication stores — next tag: sessions re-key (BREAKING) + greenfield migration set

auth-jwt-session-refresh (2026-07-11, D6) re-keyed sessions to an id-keyed
anchor with `refresh_token_hash` (UNIQUE index), `previous_refresh_token_hash`
(nullable, partial index), `previous_used`, `rotation_count`, and a `user_id`
index. The store adapters implement the re-keyed `session.SessionRepository`
(CAS `Rotate`/`ConsumeGrace`, `GetByRefreshHash`, `MapError`-routed `Create`),
so both nested store-module tags (`features/authentication/stores/turso`,
`.../pgx`) take a **breaking version bump**.

**Greenfield-migrations rule applied (2026-07-12, jrazmi ruling):** the
canonical set defines the FINAL schema and never carries upgrade/evolution
files. The sessions re-key lives in `0003_sessions.sql`'s CREATE, and the
former evolution files (`0012_id_defaults`, `0013_invitation_identifier_kind`,
`0014_sessions_refresh`; cms's `0022_id_defaults`) were folded into their base
CREATEs — auth's set is `0001…0011`, one CREATE per table. New hosts scaffold
the final shape. A host that scaffolded an earlier shape writes its OWN
migration in its host tree (reference SQL in the feature README's upgrade
note; segovia's `0018_sessions_refresh.sql` is the exemplar). Same
no-rolling-deploy / rollback runbook as the feature entry above.

### features/cms stores — next tag: id defaults folded into base CREATEs

The greenfield-migrations rule also folded cms's `0022_id_defaults.sql` into
the six entity tables' CREATE files (terms, menus, menu_items, assets,
inquiries, entries) in BOTH dialects. Schema-identical for any DB that had
already applied 0022; a host that scaffolded before the id-defaults change
adds the defaults with its own host-tree migration.

### features/authentication + both store modules — next tag: auth v3 identity (BREAKING)

auth-v3 (the identity milestone, 2026-07-13) reshapes the feature off a single
`users.email`/`email_verified` pair onto the `user_identifiers` table (multiple
email/phone identifiers with explicit login/recovery/notification/primary uses),
adds `users.auth_revision` (the optimistic-serialization anchor) and session
authentication-metadata columns, adds the `challenges` / `contact_changes` /
`authentication_grants` flow tables, and retires the legacy
`verification_codes` / `verification_tokens` rail. This is a **breaking** bump
for `features/authentication` and BOTH nested store-module tags
(`features/authentication/stores/{pgx,turso}`): the `Repositories` bundle grows
identifier/challenge/contact-change/grant/credential ports, `user.User` loses its
email field, and routes/entities change.

The **AV3D delivery-runtime refactor** (2026-07-13, same untagged milestone) folds
into this cut: it removed authentication's private durable delivery queue, so the
canonical set is `0001…0013` with **no** delivery table (an earlier v3 cut's
`delivery_jobs` table was removed). Durable delivery is now the generic **jobs**
feature reached through `Config.DeliveryDispatcher`; the bounded ephemeral path is
`in_process`. Public removals: `Repositories.DeliveryJobs`, `domain/deliveryjob`,
`Service.RunDeliveryWorker`, and the delivery-durability errors. Rename:
`Config.DeliveryWorkerAcknowledged` → `DeliveryJobsAcknowledged`. Additions:
`Config.DeliveryMode`/`DeliveryDispatcher`/`InProcessDelivery`/`DeliveryEventsEmitter`/
`DeliveryEphemeralAcknowledged`, `Service.RunDelivery`/`DeliveryJobRuntime`, and the
generic-jobs fenced surface (`Repositories.FencedQueue`, `jobs.FencedRuntime`,
migration `0003_fenced_job_queue`). Behavior: `DeliveryStatus.Attempt` now reads 0.
The host-side upgrade is the **Auth delivery-runtime upgrade runbook** below.

Per the greenfield-migrations rule (2026-07-12) the canonical migration set
ships only the FINAL schema and carries **no** upgrade/evolution file — a live
v2 host owns its own evolution and MUST NOT blind-copy the canonical migrations
(the final `0001_users.sql` no longer carries `email`, so copying it onto a
populated v2 `users` drops email before any backfill). The host-owned,
backfill-first, validated migration procedure — exact pgx and SQLite/libSQL SQL
for both dialects — is the **Auth v3 host upgrade runbook** below. The same note
is mirrored in `features/authentication/README.md`.

### features/authentication + both store modules (+ authorization pgx) — next tag: auth adopter hardening (BREAKING, pre-tag)

auth-adopter-hardening (2026-07-15, task prefix `AAH`) is a **pre-tag breaking**
hardening packet folded into the same untagged auth-v3 cut — preflight re-confirmed
zero `features/authentication*` tags (`git tag -l` empty, 2026-07-15), so the
public-API and canonical-migration edits carry no append-only constraint. It closes
the framework gaps the first authorization-v3/authentication-v3 adopter exposed. The
breaking surface:

- **`auth.Granter` is now structured and fail-loud (D1/D2).** `Grant(ctx,
  resourceType, resourceID, relation, subjectType, subjectID string) error` became
  `Grant(ctx, GrantInput) error`, where `GrantInput{OperationID, ResourceType,
  ResourceID, Relation, SubjectType, SubjectID}` carries an operation-scoped identity
  (persisted invitation row id for accept/resolve-on-registration; a fresh
  high-entropy id from the unconditional secret generator — never `Config.IDs` — for
  direct-add). Success (nil) now means the EXACT requested relation was applied or is
  already exactly present; a different existing relation, an invariant refusal, a
  missing/deleted host resource, or any infrastructure error must fail loud (no
  implicit replace). A host adapter inspects its authorization receipt outcome. Any
  host implementing `Granter` updates to the struct signature and the outcome-aware
  contract (the `examples/auth-cms` `relationshipGranter` is the reference).
- **`Config.InviteCheck` is REQUIRED with a `Granter` (D3).** New `InviteAction`
  (`InviteCreate`/`InviteList`), `InviteCheckRequest{Principal, Action, ResourceType,
  ResourceID, Relation}`, and the `InviteCheck` func. A `Granter` with a nil
  `InviteCheck` is `ErrInviteCheckRequired` at construction; an `InviteCheck` with no
  `Granter` is `ErrInviteCheckWithoutGranter`. The feature's parsed create/list HTTP
  handlers call it after live-session validation and principal resolution;
  host-direct `Service` methods are trusted composition calls that skip HTTP policy.
  All authenticated invitation routes (create, resource-list, mine, accept, cancel,
  resend) moved from `RequireUser` to `RequireLiveSession` (immediate revocation);
  public decline is unchanged. Invitation authority is **issuance-time**: an issued
  invitation is a durable expiring capability, and acceptance does not re-check
  inviter authority.
- **Both store constructors now return `(auth.Repositories, error)` (D4).**
  `features/authentication/stores/{pgx,turso}.Repositories(db)` probe all 13 canonical
  tables before returning and error (`sdk.ErrNotFound`, naming the table + the
  `authentication` migration source) when one is missing — pgx via `to_regclass`,
  turso via `sqlite_master`. Constructors never apply schema. Every call site (the
  proof host, store harnesses) takes the new return signature.
- **Canonical pgx migrations carry per-column `COLLATE "C"` (D5, pre-tag fold).**
  The opaque text keyset/derived-key columns fold `COLLATE "C"` into their canonical
  CREATEs: authentication `service_accounts.id`, `api_keys.id`, `security_events.id`,
  `invitations.id`; authorization `iam_relationships.relationship_id` and all five
  `iam_roles` derived-key columns (`subject_type`, `subject_id`, `role`,
  `resource_type`, `resource_id`). Byte-order pagination parity now holds on any
  database's default collation. Deliberate **EXCLUSION**: the `iam_relationships`
  recursion columns (`resource_type`, `resource_id`, `relation`, `subject_type`,
  `subject_id`, `subject_relation`) are left uncollated — collating them raises
  SQLSTATE 42P21 in the recursive reachable CTE (the anchor seeds default-collation
  parameters), and those queries need only deterministic order/equality. Human
  display/content columns are untouched; Turso migrations are unchanged.

**v1→v3 collation upgrade caveat.** `CREATE ... IF NOT EXISTS` no-ops on a
pre-existing table, so a host upgrading from a pre-v3 schema does **not**
retroactively gain the per-column collation on already-created tables. Per the
standing greenfield-migrations rule the canonical set ships the final schema only and
hosts own their own schema evolution — a host that needs the per-column collation on
an existing table adds it with its own host-tree migration (`ALTER TABLE … ALTER
COLUMN … TYPE TEXT COLLATE "C"`) or runs the database in the `C` locale. No canonical
upgrade/evolution file ships. A `C`-locale database remains a supported
belt-and-suspenders posture either way.

These changes fold into the auth-v3 **major / breaking** floor already recorded for
`features/authentication` and both nested store modules; the authorization pgx
collation fold rides the `features/authorization` first-tag breaking-vintage cut (no
authorization Go-API change). The `examples/auth-cms` proof host (never tagged)
carries the reference composition: the structured `GrantInput` granter, a host
resource-existence check, receipt-outcome mapping, and the relation-aware
`InviteCheck`.

### features/authentication — next tag: optional HTML resource-policy seam (additive; folds into the auth-v3 breaking cut)

ui-goth (2026-07-17, GOTH-0.4; Gate C accepted 2026-07-17) added a
technology-neutral, feature-owned HTML resource-policy seam so a selected HTML view
can declare the external styles, scripts, fonts, and images it needs. The whole
surface is **additive** — every change is a new optional field/type, no existing
symbol changed — so on its own it would floor at a **minor**; it folds into the
already-**breaking** auth-v3 identity cut. Adopter-facing surface:

- **New optional `Config.HTMLPolicy *HTMLResourcePolicy`.** `nil` (the default)
  reproduces the historical asset-free CSP **byte-for-byte** (`default-src 'none';
  base-uri 'none'; form-action 'self'; frame-ancestors 'none'; script-src
  'nonce-…'|'none'`) — an upgrading host that leaves it unset sees no header change.
- **New public types/constructor:** `HTMLResourcePolicy` (opaque, validated,
  immutable), `HTMLResourceDirective`, `HTMLResourceKind`, the seven frozen
  widenable-class constants (`HTMLScriptSrc`/`HTMLStyleSrc`/`HTMLImgSrc`/`HTMLFontSrc`/
  `HTMLConnectSrc`/`HTMLMediaSrc`/`HTMLWorkerSrc`), `NewHTMLResourcePolicy` (validates
  loudly, wrapping `sdk.ErrInvalidInput`), and `var ErrHTMLPolicyWithoutViews`.
- **Behavior contract:** a policy only WIDENS the seven frozen resource classes and
  can NEVER name, relax, or remove a fixed protection (the fixed CSP prefix and the
  `Cache-Control`/`Referrer-Policy`/`X-Frame-Options`/`X-Content-Type-Options`
  headers). A non-nil policy REPLACES the default `script-src` tail entirely, so a
  policy that omits `HTMLScriptSrc` (or supplies it without `Nonce: true`) is
  fail-closed on scripts. Setting `HTMLPolicy` with a nil `Views` is
  `ErrHTMLPolicyWithoutViews` at construction (contradictory wiring, never a silent
  no-op). The seam validates directive STRUCTURE, not source VALUES — source-value
  hardening rests on the view adapter / host.
- **No new dependency.** The feature core imports no templ, Alpine, HTMX, or
  `ui/goth`; `HTMLResourcePolicy` is a plain value. The `ui/goth` authentication view
  adapter that maps `goth.Bundle.Requirements()` into a policy lands in GOTH-7.2.

### features/authentication — next tag: browser HTML seams (additive; folds into the auth-v3 breaking cut)

W2 (2026-07-20, flags #9 + #12) added the browser-facing HTML seams a host mounting
the bundled views needs, all **additive** — no existing symbol changed, every JSON API
byte-stable — so on their own they floor at a **minor**; they fold into the
already-**breaking** auth-v3 identity cut. Adopter-facing surface:

- **New `Service.RequirePrincipalBrowser` / `Service.RequireLiveSessionBrowser`.** The
  browser-facing siblings of `RequirePrincipal` / `RequireLiveSession`: same credential
  resolution and context stash, but on an authentication denial they **303 to
  `Config.BrowserLoginPath`** instead of writing a JSON 401 — a denied GET/HEAD carries
  a validated `return_to` of the original path+query, an unsafe method none. They never
  sniff `Accept` or Fetch Metadata; mount them on HTML routes. The JSON gates keep their
  byte-stable 401.
- **New optional `Config.BrowserLoginPath string`** (`AUTH_BROWSER_LOGIN_PATH`). Empty
  defaults to `/auth/login`; a non-empty value must be a safe root-relative path or
  construction fails with the new `var ErrBrowserLoginPathInvalid`. It configures ONLY
  the browser gates.
- **Form-aware `POST /auth/logout`.** The logout route now content-type dispatches like
  the other shared POSTs: the JSON contract is unchanged, and a form body (Views wired)
  clears both cookies and 303s to `/auth/login`; nil Views → 415. Logout stays
  origin-only (no double-submit token, D2). The bundled `features/authentication/views/goth`
  account page gains a matching sign-out form (that module tagged separately below).
- **No new dependency.** The feature core imports no templ or `ui/goth`; the shared
  safe-relative-path validator (`redirect.SafeRelativePath`) is feature-internal.

### features/authentication/views/goth — next tag: NEW module (first tag; renamed from views/templ)

auth-v3 (2026-07-13, AV3-8.2) added the feature's bundled default HTML view module
(the thirty-seventh workspace module), sibling to `features/cms/views/goth`, as
`features/authentication/views/templ`. ui-goth GOTH-7.2 (2026-07-18) **renamed the
module path to `features/authentication/views/goth`** (Gate A's tag-sensitive rule —
the untagged module is renamed in place, no compatibility shim) and re-implemented it
on `ui/goth`: the default auth pages now render through the `ui/goth` primitives/
components and the fingerprinted asset bundle. It carries a `HTMLPolicy()` that maps
`goth.Bundle.Requirements()` into `authentication.HTMLResourcePolicy` (the host wires
it into `Config.HTMLPolicy`), and it **externalizes the reset/magic-link
fragment-token readers** into a served `fragment.js` (`FragmentScriptHandler`,
`DefaultFragmentScriptPath`) so the pages run under a CSP whose `script-src` is
`'self'` + the per-render nonce, with no inline script. Construction is now
`New(bundle *goth.Bundle) (Views, error)` (was `New()`); a host that renders its own
views never imports it. **Migration for an adopter on the old path:** update the
import/require/replace from `.../views/templ` to `.../views/goth`, pass a
`*goth.Bundle` to `New`, serve the bundle assets + `FragmentScriptHandler()`, and set
`Config.HTMLPolicy = views.HTMLPolicy()`. The feature core stays presentation-free
(`Config.Views == nil` is API-only; the feature core imports no templ or `ui/goth`).
This is a **new, standalone module getting its first tag** (no prior tag existed on
the old path); it depends on `features/authentication` and `ui/goth` and is tagged
independently like every other importable module. W2 (2026-07-20) additionally added a
POST `/auth/logout` sign-out form to the account page (origin-only, no `@csrfField`).

### features/cms/views/goth — next tag: NEW module (first tag; renamed from views/templ)

feature-standard B2 (2026-07-07) added the CMS feature's bundled default HTML view
module `features/cms/views/templ`. ui-goth GOTH-7.3 (2026-07-18) **renamed the module
path to `features/cms/views/goth`** (Gate A's tag-sensitive rule — the untagged module
is renamed in place, no compatibility shim) and re-implemented it on `ui/goth`: the
default CMS pages (public site chrome + admin management) now render through the
`ui/goth` primitives/components and the fingerprinted asset bundle. The admin
entries-list is HTMX-enhanced (the status filter, created_at sort toggle, and
pagination swap the `#cms-entries-content` region via explicit `hx-*`, degrading to
full-document no-JS reloads); the feature core reads the `HX-Request` header as a
presentation hint only and gains no templ/`ui/goth` dependency. Construction is now
`New(bundle *goth.Bundle) (Views, error)` (was `New()`); a host serves the bundle
assets under the path it names. The CMS `Views` port gained one method
(`EntriesListContent`, the HTMX content fragment) and `Pager` gained a `Status` field.
**Migration for an adopter on the old path:** update the import/require/replace from
`.../views/templ` to `.../views/goth`, pass a `*goth.Bundle` to `New` (embedding hosts
build the bundle and serve its assets), and — if the host implements the `Views` port
by hand rather than embedding the default — add `EntriesListContent`. This is a **new,
standalone module getting its first tag** (no prior tag existed on the old path); it
depends on `features/cms` and `ui/goth` and is tagged independently.

### integrations/datastores/turso — next tag: BEGIN IMMEDIATE write-intent transactions (patch, behavior fix)

auth-v3 (2026-07-13, AV3-2.4) changed `integrations/datastores/turso`'s `DB.Begin`
to issue `BEGIN IMMEDIATE` over a pinned `*sql.Conn` instead of the driver's default
`BEGIN` (DEFERRED); see `integrations/datastores/turso/tx.go`. No exported surface
changed, so this floors the next tag at a **patch**, but it is a **behavior change a
host must know**: the v3 step-up credential/identifier CAS rails (`Apply`,
`ApplyVerifiedChange`) need write-intent-up-front so `sqld` serializes contending
transactions and the loser's own CAS returns `sdk.ErrConflict`. A host on the
**pre-fix connector** gets a raw `SQLITE_BUSY` ("database is locked") to the CAS
loser instead of `sdk.ErrConflict` and **fails the concurrent step-up contract**.
Data integrity is never at risk either way (no double-commit) — but a turso host
adopting auth-v3 must run the connector at or past this tag.

### sdk/capabilities/{email,notify} — next tag: additive capability metadata (minor floor)

auth-v3 (2026-07-13, AV3-4.4) added the production-safety capability seam consumed
by the delivery worker's fail-closed transport gate — additive, so it floors sdk's
next tag at a **minor**, never a major:

- `sdk/capabilities/email`: new `Capabilities`, `TransportSecurity`, and the
  `CapabilityReporter` interface (`capabilities.go`); `Console` reports
  `{TransportSecurityNone, DevelopmentOnly: true}` and `SMTP` reports
  `{TransportSecurityStartTLS, DevelopmentOnly: false}`.
- `sdk/capabilities/notify`: the same trio; `Console` reports development-only.

A consumer fail-closes in production on a `DevelopmentOnly` / metadata-less
transport. No existing symbol changed; a consumer that ignores the new surface is
unaffected. (`sdk/foundation/cryptids`'s HS256 default and `sdk/foundation/web`'s
`TrustProxies`/`ClientIP` were **not** touched by auth-v3 — HS256 belongs to the
JWT-refresh cut and `TrustProxies` to middleware-consolidation, each keyed above.)

### features/authentication v0.3.0 + both store modules v0.2.0 (2026-08-16): passwordless provision-on-consumption (minor; store migration REQUIRED)

coordination-hub auth upstream, phase 6 (`.claude/plans/coordination-hub-auth-upstream/06-passwordless-provisioning.md`,
CHAU-6.1…6.7, 2026-08-16). The plan called for a **separate release train** from
the core admin/resend/reset work, since it is the highest-risk change in the
packet. **The owner ruled one train**, so it shipped on the same tags: it is
additive and default-off, and it raised no bump of its own. What adopters take
regardless is store migration `0015` — the code stays dormant until they set the
flag, but the schema is not optional.

**Default behavior is unchanged.** `Config.PasswordlessProvisionOnRedeem` is false
by default, and with it false a magic link to an address with no account still
delivers nothing and creates nothing.

- **`Config.PasswordlessProvisionOnRedeem`** (env
  `AUTH_PASSWORDLESS_PROVISION_ON_REDEEM`) — opt-in account creation from an
  EMAIL magic link, at CONSUME time. Never phone, OTP, OAuth, or another kind.
  Enabling it requires the email passwordless kind, the challenge rail +
  `ChallengeProtector`, an `IdentifierKeyer`, a delivery runtime, a valid
  `PublicAuthBaseURL`, `Repositories.Passwordless`, and
  `Repositories.ActiveSessions`; anything missing is the new
  `ErrPasswordlessProvisionWiring` at construction.
- **`Repositories.Passwordless`** — the new one-transaction redemption port
  (`domain/passwordless`). The bundled pgx/turso stores always supply it, so a
  host that leaves the flag off is unaffected.
- New audit types `passwordless_provisioned` and `passwordless_adopted`, both
  secret-free (kind + purpose + outcome class; never the address or token).

**Challenge domain generalization (source-visible).** `challenge.Challenge` and
`challenge.Consumed` gain `SubjectKey`, and the single-active claim moves from
`(user_id, purpose)` to `(subject_key, purpose)`. `Challenge.ResolvedSubjectKey()`
defaults it to `UserID`, so **every purpose that predates this is unchanged**; the
field exists because an email magic link may be issued for an address with no
account, and overloading `user_id` with a digest would make one column mean two
things. `authsvc.WithSubjectKey` is the issuer-side option.

**This tag ships HOST SCHEMA:** new canonical migration
**`0015_challenge_subject_keys.sql`** in both trees (append-only;
`0011_challenges.sql` stays byte-identical to its tag). It adds
`challenges.subject_key`, backfills `subject_key := user_id`, drops
`idx_challenges_user_purpose`, and creates `idx_challenges_subject_purpose`.
Dropping the old index is required, not cosmetic: leaving it would forbid two
magic-link challenges that share an empty `user_id` for different addresses. Both
store constructors now probe the ALTER-added columns by name.

**Apply 0015 BEFORE deploying a binary built against these tags.**

**Security properties, each proven rather than asserted** (shared conformance
group `PasswordlessRedeem`, 12 adversarial cases, run against the reference, the
example host's memory store, live PostgreSQL 17, and the live Turso playground):
creation at CONSUME never at SEND; POST-only redemption so a link scanner cannot
provision; the provisioning intent captured at ISSUE and never re-derived from
current configuration; an unknown binding version rejected rather than guessed;
the send-then-register race logging in the CURRENT owner with no duplicate
subject; the unverified-claim adoption revoking the squatter's password, sessions,
grants, and outstanding challenges BEFORE the new session is written; a
deactivated subject refused; two concurrent redemptions committing exactly one
session; and every stable failure collapsing to one generic 401.

### features/authentication — v0.3.0 (2026-08-16): password-reset LINKS (minor; NEW REQUIRED PRODUCTION CONFIG)

coordination-hub auth upstream, phase 5 (`.claude/plans/coordination-hub-auth-upstream/05-password-reset-links.md`,
CHAU-5.1…5.4, 2026-08-16). Closes the "password-reset mail exposes only the raw
token" flag. **No schema change.**

- **`Config.PasswordResetURL`** (env tag `AUTH_PASSWORD_RESET_URL`) — the absolute
  public reset landing route before `?token=` is appended. A **separate** field
  from `PublicAuthBaseURL`, which is the full passwordless landing URL and not an
  origin; deriving a reset route from it would have been guesswork.
- **New required-production configuration.** With the challenge-backed
  forgot/reset rail wired, production construction now FAILS without it
  (`ErrPasswordResetURLRequired`). Development permits empty with one startup
  WARN and keeps the legacy raw-token mail, so a mid-migration local flow is not
  broken. **Adopters must set this before deploying a production binary built
  against this tag.**
- New errors: `ErrPasswordResetURLRequired`, `ErrPasswordResetURLInvalid`
  (non-absolute, non-http(s), a FRAGMENT, or a pre-existing `token` parameter),
  `ErrPasswordResetURLInsecure` (plain HTTP in production). New exported constant
  `PasswordResetTokenParam` (`"token"`), shared by the validator and the builder
  so the two cannot drift.
- **Rendered-output change:** the bundled `password_reset` body is now link-only
  when a URL is configured (CTA + copy/paste address + "ignore this message"), and
  keeps the historical raw-token body when it is not. The derived text alternative
  carries the full link and no second standalone token.
- **Template-override compatibility:** the renderer passes BOTH `.Link` and
  `.Secret`, so an app override reading `{{.Secret}}` keeps working for one
  window. `.Secret` is deprecated for reset PRESENTATION; removing it is a later
  breaking decision. It remains in the encrypted envelope regardless — the
  terminal-failure discard needs it.
- The link is built in the delivery **worker** from the configured value alone.
  Request `Host`/`Forwarded`/`X-Forwarded-*` never participate, proven by driving
  a forgot-password request with all of them hostile and asserting the delivered
  link is unchanged.

### features/authentication — v0.3.0 (2026-08-16): registration-verification resend (minor; additive)

coordination-hub auth upstream, phase 2 (`.claude/plans/coordination-hub-auth-upstream/02-verification-resend.md`,
CHAU-2.1…2.4, 2026-08-16). Closes the "no verification resend" flag. **No schema
change**; the primitives (`deliveryQueue.Replace`, `enqueueRenderedReplace`,
`verify_registration` challenges) already existed — what was missing was a
service method, an off-request-path worker initializer, routes, budgets, and any
documentation.

- **`POST /auth/verification/resend`** — public and unconditional. Always
  `202 {"status":"accepted"}`, byte-identical status/body/headers for unknown,
  malformed, verified, deactivated, and active-unverified targets. Origin-gated,
  **no** CSRF token (the caller has no session — that is the population it
  serves). New budgets: **3 per address per minute**, **10 per client IP per
  minute**, both keyed on PII-free digests and charged before any resolution.
  429 on a budget, 503 on a saturated delivery queue, 500 otherwise.
- **`POST /auth/admin/users/{id}/verification/resend`** — mounts with the admin
  surface (`Config.UserAdminCheck`, action `resend-verification`). May report
  real state: 202 + secret-free receipt, 409 `already_verified` /
  `user_deactivated`, 404 unknown / no verifiable email.
- New exported surface: `Service.ResendVerification`,
  `Service.ResendVerificationForUser`, `StepUpReceipt` (newly aliased at the
  feature root), `ErrVerificationResendRateLimited`, `ErrAlreadyVerified`,
  `ErrUserDeactivated`, `ErrNoVerifiableEmail`.
- Two new security-event types: `verification_resend_requested` (public; **no**
  user id) and `verification_resend_issued` (worker or admin; target user id).
- `PurposeRegistrationVerification` joins the delivery initializer registry, so a
  resend resolves the account, issues the replacement challenge, and renders
  entirely in the worker. Its `Discard` arm sits with the login-OTP arm: both are
  HMAC codes with no delete-by-secret path, so a never-delivered code expires
  under its TTL.

**Behavior note for adopters:** a resend REPLACES the pending job and ISSUES A
FRESH CODE, invalidating the previous one. Replacement cannot retract a provider
call already accepted, so a user may receive both the old and the new mail — only
the newest code verifies. Document that in your UI copy.

### features/authentication v0.3.0 + both store modules v0.2.0 (2026-08-16): account lifecycle and the operator directory (minor; store migration REQUIRED)

coordination-hub auth upstream, phase 1 (`.claude/plans/coordination-hub-auth-upstream/01-user-directory-and-lifecycle.md`,
CHAU-1.1…1.7, 2026-08-16). Closes two flags at once — "no administrative user
listing" and "no account status/deactivation" — as one capability, because a
directory you cannot act from and a deactivation you cannot see are each half a
feature.

**`features/authentication` — additive public surface:**

- `UserStatus` (alias of `user.Status`) with `UserStatusActive` /
  `UserStatusDeactivated`, `ErrInvalidUserStatus`, `UserSummary`,
  `UserStatusChange`, `ErrUserAdminUnavailable`, `ErrUserNotActive`.
- `UserAdminCheck` + `UserAdminCheckRequest` + `UserAdminAction` and its five
  constants — the InviteCheck precedent applied to the user directory.
- `Config.UserAdminCheck`; `Repositories.UserAdmin` and
  `Repositories.ActiveSessions`; `ErrUserAdminReposRequired`.
- Trusted `Service` methods `UserAdminEnabled`, `ListUsers`, `GetUserSummary`,
  `DeactivateUser`, `ReactivateUser` — they apply NO authorization by design.
- Two new security-event types: `user_deactivated`, `user_reactivated`.
- `user.User` gains `Status` / `StatusChangedAt` and an `Active()` method. This is
  a struct-field addition: a host constructing a `user.User` by hand still
  compiles (the zero Status normalizes to active), but a host with an exhaustive
  positional literal would not — no such construction exists in-repo.

**Behavior changes to be aware of:**

- With `Repositories.ActiveSessions` wired — the bundled stores always supply it —
  **every** session mint routes through the fenced insert. A store that cannot
  serialize it against a status transition must leave the slot nil.
- An **act-as-user API key now denies a deactivated owner**. A key whose service
  account has `ActAsUser` resolves the owner's status; an unknown owner is
  deliberately NOT treated as deactivated (the vocabulary has no "deleted"), so
  no existence requirement was added.
- Public credential endpoints collapse a lifecycle refusal into their own generic
  failure, so a deactivated login is byte-identical to a wrong password. Nothing
  that previously succeeded now fails differently; this only constrains a NEW
  failure mode.
- `Config.UserAdminCheck` set without both repositories is a LOUD construction
  error. Wiring the repositories without the check is not an error and mounts
  nothing.

**`features/authentication/stores/{pgx,turso}` — this tag ships HOST SCHEMA.**

New canonical migration **`0014_user_status.sql`** in both trees (append-only;
`0001_users.sql` stays byte-identical to its tag):

- `users.status` — `TEXT NOT NULL DEFAULT 'active'` with a closed
  `CHECK (status IN ('active','deactivated'))`; existing rows backfill to active
  from the DEFAULT.
- `users.status_changed_at` — nullable; NULL until the first transition.
- `idx_users_created_at_id` — the directory's contractual keyset.
- **pgx only:** `ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C"`.
  `users.id` became a keyset tiebreak with this release and joins the contractual
  collation inventory. It is a **rewriting DDL under ACCESS EXCLUSIVE** — call it
  out in the adopter's maintenance window.

Both `Repositories()` constructors gained a **column** probe (`users.status`,
`users.status_changed_at`) beside the existing table probes, so a host that
copied 0001–0013 and skipped 0014 fails at wiring time naming the column rather
than mid-request. Both bundles now always return `UserAdmin` and `ActiveSessions`.

**Adopter order: apply 0014 BEFORE deploying a binary built against these tags.**

One cross-dialect defect was found and fixed by the live gate: the pgx directory
list now normalizes its cursor order value to UTC (`OrderValueOf` → `.UTC()`).
Without it Postgres encodes the session-zone offset and libSQL encodes `Z`, so
byte-identical cursors across dialects would have been false for identical data.
The other pgx paged stores still encode the session offset; that is pre-existing
and untouched here, but worth a look when their contracts next move.

Live gates closed at authoring time: full `storetest` conformance including the
repeated concurrent deactivate-versus-mint race, against a live PostgreSQL 17 and
the live Turso playground database, plus the pgx collation catalog check
confirming `users.id` reports collation `C`.

### sdk v0.4.0 + pgxdb v0.4.0 + turso v0.3.0 + features/authentication v0.3.0 (+ its stores v0.2.0) — 2026-08-16: LIST SEARCH restored (minor)

Plan of record `.claude/plans/crud.md` (crud-search-upstream, T1–T4, 2026-08-16;
stacked onto this release train by owner direction). This is a **regression fix**,
not a feature request: v1 generated a `@search:` annotation, a `SearchTerm` filter
field, a store-side predicate, and a transport key, and the de-generation dropped
all of it. Two downstream repos had already invented their own word for it.

**`sdk/foundation/crud`** — `SearchField`, `ListParams.Search`,
`ListRequest.Search`, and `MatchesSearch`, plus the `q` entry in the package doc's
query-param vocabulary. Additive; a caller that sends no search is unaffected.

`MatchesSearch` is the SHARED oracle: a case-insensitive **LITERAL** substring
under an **ASCII-only** fold. Both halves are deliberate. Literal, because v1
built `"%" + term + "%"` unescaped — someone typing `100%` matched every row and
`a_c` matched `abc`; that defect is not restored. ASCII-only, because the fold
must be reproducible in three places that cannot share code (this function,
PostgreSQL's `ILIKE`, SQLite's `LIKE`), and all three agree on ASCII letters while
leaving non-ASCII code points alone. Go's `strings.ToLower` would have made the Go
matcher disagree with both dialects.

**`integrations/datastores/pgxdb`** — `ListQuery.SearchFields`,
`AddSearchClause`, `EscapeSearchTerm`, and the reserved `@list_search` argument.
The predicate is `(("col" COLLATE "C") ILIKE @list_search ESCAPE '\' OR …)`.
`COLLATE "C"` is required: `ILIKE` under a non-deterministic collation errors, and
under a locale collation its folding would diverge from the other two.

**`integrations/datastores/turso`** — the positional twin. **SQLite has no
`ILIKE`**; its `LIKE` is already ASCII-case-insensitive, so the predicate is
`("col" LIKE ? ESCAPE '\' OR …)` with one bound argument per field (positional
placeholders cannot be reused the way a named argument can). That dialect
difference lives in the store, where it belongs.

**The query fan-out, which is where this would have gone wrong.** The search
predicate is folded into `BaseSQL` **before** the strategy switch, so all FOUR
query paths inherit it: the cursor page, the offset page, the cursor strategy's
reverse probe, and the `COUNT(*)` wrap. Appending it to a per-page buffer — the
way the cursor predicate is appended — would make `WithCount` report the
**unfiltered** total (a page of 2 rows claiming 8 results) and let the reverse
probe derive `HasPrev`/`PreviousCursor` from rows the search excluded. Both
connectors clone their args before binding, because `ListQuery` is passed by value
but its args map/slice is not.

**`features/authentication` + both store modules** — `apikey.SearchFields`
declares `name` as the first searchable column (the key hash and prefix are
credential material and are deliberately NOT searchable — a searchable prefix
would let a caller probe for a key by fragment), both dialect stores declare it,
`GET /auth/service-accounts/{id}/keys?q=…` parses it, and the reference/host
memory stores apply `crud.MatchesSearch` so they cannot disagree with SQL.

**Fail-loud rules:** a non-blank term against a list declaring no `SearchFields`
is `sdk.ErrInvalidInput`, never a silently unfiltered page; an existing
`@list_search` argument fails rather than being overwritten; an invalid column
identifier fails before any SQL runs.

**Live cross-dialect oracle CLOSED at authoring time.** The full term table —
including `100%`, `a_c`, a lone `%`, a lone `_`, a literal backslash, ASCII case
pairs, and non-Latin text — ran against a live PostgreSQL 17 and the live Turso
playground database through the store conformance suite, plus `WithCount` and
cursor paging with the predicate. All three implementations agree.

**Migration for hosts:** none required. `q` is the canonical key; a transport with
v1 clients may fall back to `s` at its OWN edge with a documented removal
milestone — that alias is deliberately not in `crud.ListParams`.

### sdk — v0.4.0 (2026-08-16): canonical runtime posture + capability-owned transport checks (minor)

coordination-hub auth upstream, phase 3 (`.claude/plans/coordination-hub-auth-upstream/03-runtime-posture-foundation.md`,
CHAU-3.1…3.4, 2026-08-16). The upstream flag: coordination-hub's **generic**
`internal/integrations/mailer` imported `features/authentication` for nothing but
the runtime-mode vocabulary and the insecure-transport error. That is a layering
inversion — an app-wide mailer should not depend on an auth feature to name its
own deployment posture. Purely additive; no existing sdk symbol changed.

- **`sdk/foundation/environment`** (new `mode.go`): `Mode` with `ModeDevelopment`
  / `ModeProduction`, `ValidateMode`, `ParseMode`, and the
  `ErrModeRequired` / `ErrModeInvalid` sentinels. A REQUIRED enum — the empty
  value is an error, never a silent development default. It reads **no**
  environment variable: the host names the key and passes the already-read
  string, so nothing about mode selection is implicit. Only two values ship;
  staging/preview/CI map onto the posture the host wants (normally production).
  Stdlib-only, so `environment` keeps its "zero external dependencies" claim and
  the foundation-imports-root-only guard (G12b) holds.
- **`sdk/capabilities/email`**: `CheckSender(environment.Mode, Sender) (TransportPosture, error)`,
  `InspectSender`, `TransportPosture{Declared, Capabilities}` with
  `ProductionCapable()`, and `ErrInsecureTransport`. Production rejects a
  development-only or metadata-less Sender; development accepts both and returns
  the classification so the caller phrases its own warning. **No logger
  parameter** — message text and log routing are composition concerns. An empty
  or unknown mode is rejected rather than defaulted.
- **`sdk/capabilities/notify`**: the same trio —
  `CheckNotifier`/`InspectNotifier`/`TransportPosture`/`ErrInsecureTransport`.
- Detection stays **structural** (a `CapabilityReporter` type assertion), so a
  third-party transport opts in without the sdk knowing its type;
  `integrations/email/sendgrid` now carries a compatibility test proving the real
  Sender resolves through `email.CheckSender` as its `Capabilities` claim.

Capabilities importing `foundation/environment` is legal under the layering
guard (the `notify` → `foundation/identity` precedent); the guard forbids
capability↔capability and capability→feature only.

If the phase-4 transactional-logo change rides the same commit, do **not** split
it into its own patch — this minor covers both.

### features/authentication — v0.3.0 (2026-08-16): RuntimeMode is now an alias of environment.Mode (minor; SOURCE-COMPATIBLE)

The feature half of phase 3 (CHAU-3.3). **No host code change is required**, and
none is expected to break:

- `RuntimeMode` is a **type alias** of `environment.Mode`, and
  `RuntimeModeDevelopment`/`RuntimeModeProduction` are the canonical constants.
  Alias, not a new distinct type: values are assignable in both directions with
  no conversion, `Config{RuntimeMode: …}` literals compile unchanged, the wire
  values are still `"development"`/`"production"`, and the `AUTH_RUNTIME_MODE`
  env tag parses identically.
- `ErrRuntimeModeRequired` / `ErrRuntimeModeInvalid` now **wrap**
  `environment.ErrModeRequired` / `ErrModeInvalid`. `errors.Is` against the auth
  sentinels is unchanged, and sdk-only code can match the canonical sentinel for
  the same failure. **The error TEXT changed**: each message gained a
  `": environment: mode …"` suffix. Nothing in the repo asserted on that text
  (only `errors.Is`), but a host string-matching on the message should switch to
  `errors.Is`.
- `ErrInsecureDeliveryTransport` is unchanged as a sentinel, but the returned
  error now **also** wraps `email.ErrInsecureTransport` or
  `notify.ErrInsecureTransport`, and its message gained the capability error as a
  suffix. The verdict is now made by `email.CheckSender`/`notify.CheckNotifier`;
  auth keeps the per-transport label and its own development WARN wording.
- **Development WARN behavior is byte-identical to before**: only a transport
  that *declared* itself development-only warns. A metadata-less transport is
  still rejected in production and still silent in development — deliberately
  not "improved" as a side effect of this refactor.
- Requires the sdk tag above (`environment.Mode` + the capability checks).
- The two unexported helpers `emailCapabilities`/`notifyCapabilities` were
  removed as orphaned by this change; both were unexported and had no other
  caller.

Downstream action for coordination-hub: delete the `features/authentication`
import from the generic `internal/integrations/mailer` / notifymail packages and
name `environment.Mode` + `email.CheckSender` instead. The auth composition may
keep importing the feature — it needs `auth.Config` regardless.

### sdk/capabilities/email — sdk v0.4.0 (2026-08-16): bundled transactional layout renders Brand.LogoURL (RENDERED-OUTPUT change; rode the minor)

coordination-hub auth upstream, phase 4 (`.claude/plans/coordination-hub-auth-upstream/04-email-layout-branding.md`,
CHAU-4.1…4.3, 2026-08-16). The upstream flag read "sdk layouts ignore
`Brand.LogoURL`"; the audit found it true of exactly one bundled layout.
`marketing.html` already rendered the logo and `minimal.html` is deliberately
unbranded, but `transactional.html` — the layout **every** authentication
delivery purpose uses — dropped it. A host that set `EmailBranding.LogoURL` got
no image and no error.

- `templates/layouts/transactional.html` gains a conditional logo block above the
  brand name: `{{if .Brand.LogoURL}}<img src="{{.Brand.LogoURL}}" alt="…">{{end}}`,
  with conservative email-client inline dimensions (`max-height: 48px`,
  `max-width: 100%`, `height: auto`, `border: 0`). Name and tagline still render
  beside it, so a blocked or broken image never erases brand identity.
- `alt` is `Brand.Name`, falling back to `Your Company` — the same fallback the
  visible header already used, so image and text identity cannot disagree.
- `TemplateRegistry.SetBranding(nil)` now resets to empty branding instead of
  storing nil, making the registry's documented "branding is never nil" invariant
  hold for the public `email.WithBranding` path too.
- Package docs gain the branding/layout field matrix, the escaping boundary
  (`html/template`, never `template.HTML`; no fetching or URL validation), and
  the external-image-blocking rationale. `Branding`'s godoc carries the matrix.

**No exported symbol changed**, so this floors at a **patch**. It is, however, a
**rendered-output change**: HTML differs *only* when `Branding.LogoURL` is
non-empty, and only for hosts on the bundled transactional layout. A host that
overrides the layout at `email.LayerApp` (coordination-hub does) sees **no
change** — the override wins wholesale and owns its own logo markup. The
plain-text alternatives are untouched and remain image-free. Do not split this
into its own tag if the phase-3 runtime-posture work rides the same commit; that
work floors sdk at a **minor**.

### sdk/capabilities/work — next tag: NEW module (first tag)

The SWP promotion (sdk-work-protocol, 2026-07-13) added `sdk/capabilities/work` —
the **keyed-work submission protocol**: a vocabulary + narrow-port contract with
**no default implementation** (the `oauth` posture). It has **no prior tag**, so it
is a **new module, first tag**. It ships:

- `work.Status` — the frozen seven-value lifecycle vocabulary
  (`pending`/`running`/`completed`/`failed`/`dead_letter`/`canceled`/`superseded`;
  `failed` is NON-terminal/retryable), with `Terminal()`/`Known()` predicates,
  pinned byte-for-byte to the persisted job-status strings by the package's own
  literal test;
- segregated consumer ports `Enqueuer` (idempotent keyed admission), `Replacer`
  (optional atomic replace/supersede), and `StatusReader` (deterministic
  latest-by-key, lifecycle-only — never payload/attempt/secret);
- an **opaque `[]byte`** payload (deliberately NOT `json.RawMessage`: some producers
  submit ciphertext, so the protocol must not imply JSON); and
- `worktest`, the conformance suite an implementation runs.

The implementation of record is `features/jobs` (below). **Payload snapshot
ownership (SWP-3 / IX-23).** The implementation of record deep-copies the payload
with a central `bytes.Clone` at the protocol boundary, so a keyed unit's admitted
bytes are a store-independent snapshot: a later caller mutation of its slice cannot
alter admitted work, for every backing store, by construction. `worktest` pins this
under `-race`; a new backend inherits the semantic from the protocol, not from its
own storage layer.

### features/jobs + both store modules — next tag: SWP fenced delivery surface (minor floor)

auth-v3/AV3D (2026-07-13) made `features/jobs` the **implementation of record** for
`sdk/capabilities/work` and added the opt-in lease-fenced delivery surface. All
changes are **additive / source-compatible**, so the floor is a **minor** bump (no
existing exported signature was removed or changed incompatibly), with two adopter
notes:

- **New sdk dependency floor.** `features/jobs` now imports `sdk/capabilities/work`;
  a host pins sdk at or past the `work`-carrying tag.
- **`job.Status` is now a source-compatible alias** `type Status = work.Status` (was
  a distinct `type Status string`). The persisted strings are byte-identical, and an
  alias is assignable both ways, so existing `job.Status` code compiles unchanged;
  `job.StatusCanceled`/`job.StatusSuperseded` are new members produced only by the
  fenced queue.
- **Additive surface:** `Repositories.FencedQueue` (nil = the fenced surface is
  off), the keyed-work primitives (`EnqueueOnce`/`Replace`/`LatestStatusByKey` over
  opaque `[]byte`, `Checkpoint`/`PurgeTerminal`), `jobs.FencedRuntime`, and the
  opt-in migration `0003_fenced_job_queue` (both dialects). The existing
  unfenced `Queue`/cron surface, its migrations, and every current consumer are
  unaffected. A host may now wire `FencedQueue` alone (delivery-only), `Queue` alone
  (existing cron host), or both; `ErrQueueRequired`'s message widened accordingly.

Downstream upgrade example — a consuming feature depends on the sdk `work` ports,
never on `features/jobs` (constitution rule 6):

```go
// BEFORE (pre-SWP): a consumer hand-declared its own narrow enqueuer port.
type enqueuer interface {
    EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (string, error)
}

// AFTER (SWP): depend on the canonical sdk ports; jobs.Service satisfies them.
import "github.com/gopernicus/gopernicus/sdk/capabilities/work"

type Deps struct {
    Enqueuer work.Enqueuer     // jobs.Service.EnqueueOnce
    Replacer work.Replacer     // jobs.Service.Replace     (optional)
    Status   work.StatusReader // jobs.Service.LatestStatusByKey
}
```

### features/authorization + both store modules — next tag: authorization v3 correctness kernel (BREAKING; FIRST tags)

authorizationv3 (2026-07-14, task prefix `AZ3`) hardens the v1 IAM feature into an
exact-semantics correctness kernel. **Preflight re-confirmed zero
`features/authorization*` tags exist (`git tag -l` empty, 2026-07-14),** so this
cut is the module's **first tag** and — per recommended-default #7 and the packet's
pre-tag breaking policy — the canonical migration set was **rewritten greenfield**
(fold-to-final, not append-only). This note **supersedes** the pending
middleware-consolidation "additive gate symbols (minor floor)" note above: with no
tag ever cut, the v3 breaking surface simply absorbs that pending minor floor into
the first tag.

**Breaking-change taxonomy — this note distinguishes SEMANTIC access changes from
source-only renames** (the AZ3-5.3 acceptance criterion):

- **SEMANTIC (a decision or stored-state meaning changed):**
  - **Userset relations are now load-bearing at runtime** (critical finding #1). A
    stored `group#admin` is no longer silently satisfied by `group#member`; a
    concrete-group grant reaches only the group entity; a tuple missing its
    required userset relation is now rejected. Any adopter whose v1 data relied on
    the decorative-relation bug gets DIFFERENT (correct) decisions. This is the
    single change most likely to alter live access — the AZ3-5.1 upgrade audit
    classifies each v1 row RETAIN / LOSE and stops on ambiguity (see
    `features/authorization/stores/UPGRADE.md`).
  - **Decision requests are concrete-principal-only.** A non-empty relation at a
    decision boundary is now rejected, never ignored; `Check`/`CheckBatch`/
    `FilterAuthorized`/`LookupResources` fail closed on malformed refs.
  - **Evaluation is now bounded and fail-closed.** Limit/graph/fan-out/lookup
    exhaustion returns an indeterminate `ErrEvaluationLimit` (wraps
    `sdk.ErrUnavailable`; middleware maps it to 503), never a silent deny or a
    truncated-complete list. A caller that treated "no error, empty result" as
    "denied" must now fail closed on the error.
  - **Mutations are atomic, revisioned, idempotent, and outcome-explicit.** A
    conflicting create no longer silently succeeds: apply/revoke/replace return an
    explicit `Receipt.Outcome` (applied/no_change/semantic_conflict/
    invariant_blocked/not_found) plus an independent `Replayed` flag; stale
    revision and MutationID payload mismatch are command ERRORS, not outcomes.
    Last-owner protection is now a single-winner repository invariant
    (`OutcomeInvariantBlocked`), replacing the non-atomic exists→count→delete.
  - **`LookupResources` completeness + Check/Lookup parity.** Lookup now returns
    Through-derived descendants it previously omitted (the D1(b)/D1(c) gap), so an
    adopter enumerating resources sees a larger, correct set.
  - **Effective role listing with provenance.** A scoped revoke that leaves a
    global grant now reports `SameRoleGrantRemains=true`; `ListEffectiveRoleGrantsByResource`
    reports direct/global/both provenance (raw `ListByResource` retained,
    documented as raw).
  - **Actor/guard authority is now mandatory for untrusted writes.** A guarded
    write commits only if every authorization scope its guard read has the same
    revision when the repository locks; there is no default-allow.
- **SOURCE-ONLY renames / shape changes (compile break, no access-meaning change):**
  - **Decision vocabulary rename:** `CheckRequest.Subject` (type `Subject`) →
    `CheckRequest.Principal` (type `PrincipalRef{Type, ID}`); the stored-subject
    type is now `SubjectRef{Type, ID, Relation}`. These are intentionally distinct
    types (concrete principal vs. possibly-userset stored subject).
  - **Construction shape:** `NewService(repos, cfg)` now returns
    `(Components{Service, RelationshipWriter, SystemMutator}, error)` — the authorization-specific FS2
    amendment (a feature holding a separately-partitioned trusted capability; see
    `features/README.md` §5 FS2 amendment; NOT a general FS2 replacement).
  - **Relationship state writes moved off `Service`:** `CreateRelationships`,
    `DeleteRelationship`, `DeleteResourceRelationships`, `DeleteByResourceAndSubject`,
    raw `AssignRole`/`UnassignRole`. Actor-facing typed guarded commands
    (`GrantRelationship`/`RevokeRelationship`/`ReplaceRelationship`/`AssignRole`/
    `UnassignRole`) remain guarded on `Service`; normal relationship state writes
    live on the separately held `RelationshipWriter`, while `SystemMutator` is the
    opt-in high-integrity path.
  - **Reader ports gained a `limit int`:** the three `Lookup*` reader methods bound
    their result set (SQL `LIMIT` / memstore cap); a custom store implementation
    must add the parameter.
  - **`GetSchema()` returns a `SchemaSnapshot`** (deep-copy read-only projection)
    instead of the mutable `Schema`; new `SchemaDigest()`.
  - **Role effective-listing port addition:** `role.Storer.ListEffectiveByResource`
    (+ `EffectiveGrant`) — a custom store must implement it.

**Canonical migration set (greenfield rewrite).** Both dialect trees ship the final
v3 schema with byte-identical filename sets: `0001_iam_relationships`,
`0002_iam_roles`, `0003_iam_scopes`, `0004_iam_mutations` (the `iam_*` prefix per
owner ruling R4). The `iam_scopes` (revision anchors) and `iam_mutations` (receipt
ledger) tables are new in v3. A v1 adopter does **not** blind-copy these onto a
populated v1 database — the **AZ3-5.1 data-preserving adopter path** (detection →
blocked-until-repaired → conversion + anchor seeding → v3 boot → access comparison →
rollback boundary) is published in `features/authorization/stores/UPGRADE.md` and
linked from `features/authorization/README.md`. It was **executed live** on
fresh/reset PostgreSQL (C-collation) + libSQL both dialects (AZ3-5.1) and has **not**
been applied to a real host.

**Jobs/work axis: authorization adds NOTHING.** authorizationv3 imports **`sdk`
only** (verified: `features/authorization/go.mod` requires only `sdk`; zero
`sdk/capabilities/work`, `features/jobs`, or `features/events` imports in the core).
The v3 correctness kernel emits **no effects** — no authorization delivery queue, no
post-commit dispatch, no event append, no authorization-specific jobs table. This is
enforced permanently by the sixteenth layering guard,
`guard-authorization-no-delivery-repo` (AZ3-5.3), the `guard-auth-no-delivery-repo`
twin pointed at `features/authorization` migrations + repositories. So the settled
`sdk/capabilities/work` (new first-tag module) and `features/jobs` (MINOR floor)
notes above carry **no authorization rider**; a future authorization effects packet
must consume the shared jobs/events vocabulary, never revive a bespoke queue.

**Per-module tag requirements (semver floors; no tag cut this milestone):**

| Module | Floor | Why |
|---|---|---|
| `features/authorization` | **first tag — breaking-vintage** | userset relations load-bearing, concrete-principal decisions, immutable `SchemaSnapshot`, bounded evaluation, `Components{Service, RelationshipWriter, SystemMutator}` construction, separately held baseline state writes, and optional atomic revisioned receipts. Pre-`v1`, breaking is expected (Go pre-release semantics); move to `v1.0.0` deliberately, not on this first tag |
| `features/authorization/stores/pgx` | **first tag — breaking-vintage** | implements the relation-aware readers (+`limit`), the three atomic mutation repositories (`iam_scopes`/`iam_mutations`), and `ListEffectiveByResource` over the greenfield `0001…0004` set |
| `features/authorization/stores/turso` | **first tag — breaking-vintage** | same, libSQL dialect; requires the turso connector's `BEGIN IMMEDIATE` write-intent transactions (already keyed above) for the concurrent mutation CAS |

`examples/auth-cms` is the v3 proof host — a demonstration, never tagged.

**Consumer changes a host/feature must make:**

- **Auth invitation Granter** (`examples/auth-cms/cmd/server/membership.go`): the
  ordinary project-member adapter uses the separately held `RelationshipWriter`
  and intentionally ignores `OperationID`; exact creation is naturally
  idempotent and later re-grants restore current state. A second
  `guardedRelationshipGranter` demonstrates the opt-in sensitive posture:
  `SystemMutator` + operation-scoped `DeriveMutationID` + receipt inspection.
  Hosts choose by resource type/relation; authentication mandates neither path.
- **Events authorization closure:** `features/events` itself needs **no change** —
  its `AuthorizeStream` config seam is a host-supplied closure and does not import
  authorization. A host whose events `Authorize` closure delegates to
  `authorizer.Check` must update the call to the new `CheckRequest{Principal:
  PrincipalRef{...}}` shape (was `CheckRequest{Subject: Subject{...}}`). This is the
  source-only decision-vocabulary rename; the Check-only gate semantics are
  unchanged.
- **auth-cms (done — cite):** the proof host is fully migrated — `hostMutationGuard`
  (`cmd/server/guard.go`) composes the schema `manage_access` relation and the
  platform-admin recipe over the dependency-tracking `DecisionView`;
  `cmd/server/authorization.go` seeds via `SystemMutator`+`DeriveMutationID`; all
  session-only authorization-mutation HTTP routes were removed (browser
  role-assignment deferred to the AZADM packet). Migration proven by the AZ3-4.1/4.2
  host-composition tests and the checked-in
  `examples/auth-cms/cmd/server/testdata/az3-proof-transcript.md`.
- **External host recipe (README wiring page):** a generic host wires
  `Components{Service, RelationshipWriter, SystemMutator}` from `NewService`, uses
  the baseline writer for application-maintained tuple state, and composes a host
  `MutationGuard` (schema-declared relation + any platform-admin short-circuit) that
  fails closed, holding `SystemMutator` apart for the sensitive operations that
  opt into revisions/invariants/receipts/audit,
  and adapts `identity.Principal → Actor`/`PrincipalRef` at the boundary. The full
  wiring recipe and every API is documented in `features/authorization/README.md`
  (AZ3-5.2) — a host wires safely without reading internal code or the plan.

**sdk graduation decision (RECORDED for owner review — NO code moved).** This
milestone fires the ARCHITECTURE protocol-table trigger ("authorizationv3 settles
its semantics"). Re-running the three conjunctive graduation gates over the
authorization check/decision vocabulary: **RE-DEFER — stays consumer-declared.** The
semantics are now settled (the trigger's condition), but graduation still fails:
- *sdk/README.md admission (plurality):* exactly ONE honest implementation exists
  (`features/authorization`); host "closures" are arbitrary access composition, not
  second implementations of a shared decision contract. FAILS test 1.
- *ARCHITECTURE five-point sdk-vs-logic:* multiple honest adapters do NOT exist
  (point 1), and the conformance suite (`storetest`) is feature-coupled, not an
  sdk-generic suite (point 3). FAILS.
- *features/README.md §5 five criteria:* criterion 1 (real producer + real consumer
  in SEPARATE modules) is not met — the only cross-feature usage is consumer-declared
  Check-only closures per the C2 DEFAULT, not a separate module consuming a graduated
  sdk authorization port; and criterion 2 (canonical-across-gopernicus) is still not
  established. FAILS.
Recommendation to the owner: update the ARCHITECTURE protocol-table row reason from
"trigger: authorizationv3 settles its semantics" to "settled, but re-deferred:
single implementation; consumer-declared Check-only closures remain the only
cross-feature usage — re-evaluate when a second authorization decision
implementation or a feature needing the identical decision vocabulary appears." The
owner ratifies any table edit separately; this task records the decision only.

### Auth v3 tag requirements + production checklist

**Per-module tag requirements for the auth-v3 cut** (semver floors; no tag is cut
until the release workflow authorizes it):

| Module | Floor | Why |
|---|---|---|
| `features/authentication` | **major / breaking** | `Repositories` grows identifier/challenge/contact-change/grant/credential ports (NO delivery port — durable delivery is the generic jobs feature via `Config.DeliveryDispatcher`); `user.User` loses its email field; `Config` and routes/entities change; the legacy `verification_*` rail is retired; the AV3D delivery removals/renames above apply |
| `features/authentication/stores/pgx` | **major / breaking** | implements the re-keyed `Repositories` over the greenfield `0001…0013` set (no `delivery_jobs`) |
| `features/authentication/stores/turso` | **major / breaking** | same, libSQL dialect |
| `sdk/capabilities/work` | **new module — first tag** | the keyed-work submission protocol (vocabulary + ports, no default); `features/jobs` is its implementation of record |
| `features/jobs` + both store modules | **minor** | implements `sdk/capabilities/work` (new sdk dep); additive fenced delivery surface: `Repositories.FencedQueue`, keyed-work primitives over opaque `[]byte`, `jobs.FencedRuntime`, `PurgeTerminal`, migration `0003_fenced_job_queue`; `job.Status` is now a source-compatible alias of `work.Status` (existing consumers unaffected) |
| `features/authentication/views/templ` | **new module — first tag** | bundled default HTML views (additive; opt-in) |
| `integrations/datastores/turso` | **patch (behavior fix)** | `BEGIN IMMEDIATE` write-intent transactions (required by a turso host adopting v3) |
| `sdk/capabilities/{email,notify}` | **minor** | additive production-safety capability metadata |

The four `examples/*` hosts (including `examples/auth-cms`, the auth-v3 proof host)
are demonstrations, not importable modules, and are never tagged.

**Host upgrade order** is the seven-step, backfill-first, host-owned procedure in
the runbook below (single cutover — do not roll; destructive Step 6 only after the
v3 binary is confirmed stable). It has been validated on fresh/reset databases both
dialects (AV3-9.2) and **not** applied to any real host.

**Production readiness checklist** (fail-closed gates a host MUST satisfy before
`RuntimeMode` production — detail in `features/authentication/README.md`):

- **Five distinct host-supplied secrets — never reuse one value for two roles.**
  The real key material the host wires into `Config` (proof-host env names in
  parentheses) and each key's ACTUAL rotation story:
  1. **Access-JWT signer** (`Config.TokenSigner`, `AUTH_JWT_SECRET`). Signs the
     access JWT (required — `ErrTokenSignerRequired`). **Single-key, disruptive:**
     rotating it invalidates every live access JWT, forcing re-authentication; a
     multi-instance deployment MUST share one value (a per-instance key flaps auth).
     Use a rolling deploy and expect existing bearer sessions to re-auth.
  2. **Challenge HMAC pepper** (`Config.ChallengeProtector`, `AUTH_CHALLENGE_PEPPER`).
     Protects OTP short codes AND digests magic-link / password-reset tokens — there
     is NO separate magic-link/reset secret; those ride this pepper. It is the ONE
     key with **continuity-supporting rotation**: the bundled `HMACChallengeProtector`
     takes an `HMACKeyRing` (active key ID + retained older keys), so a rotation
     verified by `TestChallengeProtectorKeyRotation` keeps pending codes/links under a
     retained old key valid; removing an old key from the ring invalidates challenges
     still pending under it (the user restarts the flow).
  3. **Delivery payload AES key** (`Config.DeliveryEncrypter`, `AUTH_DELIVERY_ENCRYPTER_KEY`,
     AES-256-GCM). Seals the delivery-outbox envelope. **Single-key, disruptive:**
     rotating it only affects payloads sealed after the change, so **drain in-flight
     delivery work before retiring the old key** (an in-flight payload sealed under a
     removed key dead-letters and the user restarts the flow).
  4. **Provider-token AES key** (`Config.TokenEncrypter`, `AUTH_TOKEN_ENCRYPTER_KEY`,
     AES-256-GCM; optional — nil = provider OAuth tokens are not persisted). Encrypts
     stored OAuth access/refresh tokens at rest. **Single-key, disruptive:** stored
     tokens sealed under the old key become undecryptable on rotation (**stored-token
     loss** — affected users re-link the OAuth provider).
  5. **Identifier HMAC key** (`Config.IdentifierKeyer`, `AUTH_IDENTIFIER_KEY`; required
     in production — `ErrIdentifierKeyerRequired`). Derives PII-free rate-limit /
     idempotency keys. **Single-key**, but rotation is the least disruptive: derived
     limiter/idempotency keys change, so rate-limit buckets and enqueue-idempotency
     dedup reset once (transient; no session or credential loss). A multi-instance
     deployment MUST share one value so one identifier maps to one bucket.

  There is deliberately **no separate CSRF secret**: the double-submit CSRF token is a
  fresh per-render random value set as the `__Host-auth_csrf` cookie and compared in constant
  time against the `csrf_token` field — no host key material to manage or rotate.
- **Production rejects development transports and unacknowledged/incomplete wiring.**
  In `RuntimeMode` production, `NewService` fails construction on: a `DevelopmentOnly`
  / metadata-less email or notify transport (the `console` senders), a memory rate
  limiter, a missing `IdentifierKeyer`, a delivery runtime whose mode is unacknowledged
  (`DeliveryJobsAcknowledged` / `DeliveryEphemeralAcknowledged`), and — when the
  passwordless/link surface is wired — a missing or non-HTTPS `PublicAuthBaseURL`.
  `console` senders are development-only. **What `NewService` does NOT gate (host
  deployment checklist, not a construction guarantee):** `AllowedOrigins` may be empty
  at construction (an empty allowlist simply rejects every cross-origin browser POST at
  request time — the host must populate it for browser clients), and **trusted-proxy /
  `ClientIP` wiring is router-level** (`sdk/foundation/web` `TrustProxies`, wired by the
  host) and therefore **unobservable by `NewService`** — it cannot and does not reject a
  host that forgot it. Both are deployment-checklist items the host verifies, not
  construction-time failures.
- **The delivery runtime is host-lifecycle-owned, and its mode is explicit.**
  `Config.DeliveryMode` is required with no default (never inferred from a non-nil
  collaborator). The recommended production posture is `"jobs"`: wire
  `Config.DeliveryDispatcher` over the generic **jobs** feature, run the generic
  `jobs.FencedRuntime` (start on boot, stop on shutdown), and set
  `DeliveryJobsAcknowledged: true` (production rejects an unacknowledged jobs
  runtime and a `jobs`-mode config with no dispatcher). `"in_process"` is a bounded
  EPHEMERAL pool the host drives with `Service.RunDelivery(ctx)`; it does NOT
  survive a crash and has no cross-instance coordination, so production requires the
  explicit `DeliveryEphemeralAcknowledged: true`. In either mode the dispatcher is
  the only send path, delivery is **at-least-once** (not exactly-once — consumers
  tolerate duplicates; a resend cannot retract an in-flight provider call), payloads
  are always encrypted at rest, and terminal work is purged under bounded retention.
- **Migrations are host-owned and applied pre-boot** — the greenfield canonical set
  for a new host, or this runbook's backfill-first procedure for a live v2 host
  (never blind-copy the canonical `0001_users.sql` onto a populated v2 `users`).
- **pgx byte-order pagination parity needs a `C`-collation database** (parked
  shared-helper finding, not a v3 defect); **turso hosts need the `BEGIN IMMEDIATE`
  connector** (keyed above).

## Auth v3 host upgrade runbook (v2 → v3 identity)

A **host-owned** migration procedure for a database already running auth-v2
(`features/authentication` before the v3 identity work) that is upgrading to
auth-v3. Per the standing greenfield-migrations rule the canonical migration
trees ship the **final** v3 schema only and never carry upgrade/evolution files;
a v2 host's database does **not** match the canonical `0001_users.sql`, so the
host applies the steps below from its **own** host migration tree, pre-boot,
exactly like every other host-owned migration.

**No blind copy.** Do not apply the canonical `stores/{pgx,turso}/migrations/*`
files to a live v2 database. The canonical `0001_users.sql` describes the FINAL
users shape — `id, display_name, auth_revision, created_at, updated_at`, with no
`email`/`email_verified` — so applying it to a populated v2 `users` table would
drop the legacy email data before any backfill. A v2 host runs *this* additive,
backfill-first procedure instead; the destructive column removal happens only in
Step 6, after the backfill and its validation.

Validated on fresh/reset databases both dialects (see the AV3-9.2 execution
record at the end); it has **not** been applied to a real application host.

### Preconditions

- A confirmed, restorable **backup** taken immediately before Step 1.
- A maintenance window (single cutover; see deploy ordering below — the v3
  binary must not run against the pre-Step-5 schema, and old/new binaries must
  not both serve the same database across the cutover).
- v2 `users.email` is stored normalized (trimmed + lowercased) and is `UNIQUE`.
  If a host wrote un-normalized emails, the Step-1 collision dry-run catches the
  ambiguity **before** any write.

**Deploy ordering (single cutover — do NOT roll).** (1) Take the backup
(Step 1). (2) Stop the v2 binary (or drain traffic). (3) Apply Steps 1–5
(additive; keep the v2 binary stopped to avoid mixed-version reads/writes).
(4) Deploy and start the v3 binary; confirm it is healthy and stable.
(5) **Only after** v3 is confirmed stable, apply Step 6 (the destructive
cutover — the point of no return for a v2-binary rollback). Steps 1–5 are
reversible by restoring the backup or redeploying the v2 binary (the new
tables/columns are inert to it); Step 6 drops the legacy columns and
verification tables, after which the v2 binary can no longer read `users`.

### Step 1 — Backup and dry-run collision detection

Take a full, restorable backup first (`pg_dump` / a libSQL/SQLite file copy or
`.backup`). Then run the collision dry-run. **A non-empty result aborts the
upgrade** — do not choose a winner automatically; report the colliding rows for
a human decision (this mirrors the feature's atomic auth-claim invariant,
`UNIQUE(kind, normalized_value)` over active login/recovery identifiers).

pgx:

```sql
SELECT lower(btrim(email)) AS normalized_value,
       count(*)            AS n,
       array_agg(id)       AS user_ids
FROM users
GROUP BY lower(btrim(email))
HAVING count(*) > 1;
```

SQLite / libSQL:

```sql
SELECT lower(trim(email)) AS normalized_value, count(*) AS n
FROM users
GROUP BY lower(trim(email))
HAVING count(*) > 1;
```

If either returns rows, **stop** and resolve the collisions by hand. (Validated:
skipping this and forcing the Step-3 backfill fails atomically on the
`idx_user_identifiers_auth_claim` unique index with zero rows written — the index
is the structural backstop, but detecting collisions up front lets a human
choose, not the DB.)

### Step 2 — Create `user_identifiers` and its indexes

Required by the Step-3 backfill; purely additive.

pgx:

```sql
CREATE TABLE IF NOT EXISTS user_identifiers (
    id                   TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id              TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    normalized_value     TEXT NOT NULL,
    verified_at          TIMESTAMPTZ,
    login_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    notification_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    replaced_at          TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_auth_claim
    ON user_identifiers (kind, normalized_value)
    WHERE replaced_at IS NULL AND (login_enabled = TRUE OR recovery_enabled = TRUE);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_primary
    ON user_identifiers (user_id, kind)
    WHERE replaced_at IS NULL AND is_primary = TRUE;
CREATE INDEX IF NOT EXISTS idx_user_identifiers_user_active
    ON user_identifiers (user_id, kind, created_at)
    WHERE replaced_at IS NULL;
```

SQLite / libSQL:

```sql
CREATE TABLE IF NOT EXISTS user_identifiers (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id              TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    normalized_value     TEXT NOT NULL,
    verified_at          TEXT,
    login_enabled        INTEGER NOT NULL DEFAULT 0,
    recovery_enabled     INTEGER NOT NULL DEFAULT 0,
    notification_enabled INTEGER NOT NULL DEFAULT 1,
    is_primary           INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    replaced_at          TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_auth_claim
    ON user_identifiers (kind, normalized_value)
    WHERE replaced_at IS NULL AND (login_enabled = 1 OR recovery_enabled = 1);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_primary
    ON user_identifiers (user_id, kind)
    WHERE replaced_at IS NULL AND is_primary = 1;
CREATE INDEX IF NOT EXISTS idx_user_identifiers_user_active
    ON user_identifiers (user_id, kind, created_at)
    WHERE replaced_at IS NULL;
```

### Step 3 — Backfill one primary email identifier per user

Insert exactly one active, primary email identifier per existing user. The
`NOT EXISTS` guard makes the statement **idempotent** — a re-run inserts nothing.
`login_enabled`, `recovery_enabled`, and `notification_enabled` are all set (in
v2 the single `users.email` was the universal login, recovery, and notification
address; OAuth-only users get a login-enabled email too — it is still their
discovery/recovery address and a passwordless-login claim).

**`verified_at` proxy caveat.** v2 recorded only a boolean `email_verified`, not
a proof timestamp. A verified user's identifier is backfilled with `updated_at`
as the best-available verification time; an unverified user gets `NULL`. This
preserves verification **state** exactly; the verification **timestamp** is an
approximation (acceptable for lifecycle/risk policy). A host that kept a truer
verification time elsewhere may substitute it in the `CASE` expression.

pgx:

```sql
INSERT INTO user_identifiers
    (user_id, kind, normalized_value, verified_at,
     login_enabled, recovery_enabled, notification_enabled, is_primary,
     created_at, updated_at, replaced_at)
SELECT id, 'email', lower(btrim(email)),
       CASE WHEN email_verified THEN updated_at ELSE NULL END,
       TRUE, TRUE, TRUE, TRUE,
       created_at, updated_at, NULL
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM user_identifiers ui
    WHERE ui.user_id = users.id AND ui.kind = 'email' AND ui.replaced_at IS NULL
);
```

SQLite / libSQL:

```sql
INSERT INTO user_identifiers
    (user_id, kind, normalized_value, verified_at,
     login_enabled, recovery_enabled, notification_enabled, is_primary,
     created_at, updated_at, replaced_at)
SELECT id, 'email', lower(trim(email)),
       CASE WHEN email_verified = 1 THEN updated_at ELSE NULL END,
       1, 1, 1, 1,
       created_at, updated_at, NULL
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM user_identifiers ui
    WHERE ui.user_id = users.id AND ui.kind = 'email' AND ui.replaced_at IS NULL
);
```

### Step 4 — Validate before proceeding

Every check must pass before Step 5. The count-parity check must be equal; every
other query must return **zero** rows. (Column/table names are the v2 auth
schema; adapt if a host renamed anything. On SQLite/libSQL use `is_primary = 1`
and `(login_enabled = 1 OR recovery_enabled = 1)` in the predicates.)

```sql
-- Parity: users == primary active email identifiers (must be EQUAL).
SELECT (SELECT count(*) FROM users) AS users,
       (SELECT count(*) FROM user_identifiers
         WHERE kind='email' AND is_primary AND replaced_at IS NULL) AS primary_email_ids;

-- Every user has an active primary email identifier (expect 0 rows).
SELECT u.id FROM users u
LEFT JOIN user_identifiers ui
  ON ui.user_id = u.id AND ui.kind='email' AND ui.is_primary AND ui.replaced_at IS NULL
WHERE ui.id IS NULL;

-- No duplicate active auth-claim value (expect 0 rows).
SELECT normalized_value, count(*) FROM user_identifiers
WHERE replaced_at IS NULL AND (login_enabled OR recovery_enabled)
GROUP BY kind, normalized_value HAVING count(*) > 1;

-- No orphan passwords / OAuth accounts / sessions (expect 0 rows each).
SELECT p.user_id FROM user_passwords p LEFT JOIN users u ON u.id=p.user_id WHERE u.id IS NULL;
SELECT o.provider, o.provider_user_id FROM oauth_accounts o LEFT JOIN users u ON u.id=o.user_id WHERE u.id IS NULL;
SELECT s.id FROM sessions s LEFT JOIN users u ON u.id=s.user_id WHERE u.id IS NULL;

-- Informational: accepted invitations whose resolved subject is missing.
SELECT i.id FROM invitations i LEFT JOIN users u ON u.id=i.resolved_subject_id
WHERE i.status='accepted' AND (i.resolved_subject_id='' OR u.id IS NULL);
```

Sessions carry no identifier binding in v2, so no session is invalidated by the
backfill (identifier row IDs are newly generated and nothing is bound to them
before v3).

### Step 5 — Add auth/session metadata and the new flow tables

Additive. `users.auth_revision` is the optimistic-serialization anchor; the
session metadata columns back the recent-primary-login shortcut; the four flow
tables need no backfill (they start empty).

> **`delivery_jobs` is obsolete at/after the AV3D delivery refactor
> (2026-07-13).** The `delivery_jobs` CREATE below reflects the auth-v3 schema as
> shipped through AV3D-5.0; the retained DDL preserves the validation record. Auth
> no longer owns a delivery table — durable delivery is the generic **jobs**
> feature's schema and `in_process` delivery is ephemeral (see "Migrations are
> host-owned" in `features/authentication/README.md`). A host adopting auth at or
> past the delivery refactor **skips the `delivery_jobs` CREATE here** and wires
> delivery per the **Auth delivery-runtime upgrade runbook** below; a host that
> already created `delivery_jobs` under an earlier v3 cut drains and drops it via
> that same runbook.

pgx:

```sql
ALTER TABLE users    ADD COLUMN IF NOT EXISTS auth_revision          BIGINT      NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS authenticated_at       TIMESTAMPTZ;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS authentication_methods TEXT        NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS assurance_level        TEXT        NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS challenges (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    secret_digest    TEXT NOT NULL,
    protector_key_id TEXT,
    context          TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    version          INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_user_purpose ON challenges (user_id, purpose);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_purpose_secret_digest ON challenges (purpose, secret_digest);

CREATE TABLE IF NOT EXISTS contact_changes (
    id                     TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id                TEXT NOT NULL,
    kind                   TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    new_value              TEXT NOT NULL,
    login_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    notification_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    make_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    replaces_identifier_id TEXT NOT NULL DEFAULT '',
    expires_at             TIMESTAMPTZ NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_contact_changes_user_kind ON contact_changes (user_id, kind);

CREATE TABLE IF NOT EXISTS authentication_grants (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    session_id       TEXT NOT NULL,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    context_digest   TEXT NOT NULL,
    methods          TEXT NOT NULL DEFAULT '',
    assurance        TEXT NOT NULL DEFAULT '',
    authenticated_at TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_authentication_grants_session_purpose_context
    ON authentication_grants (session_id, purpose, context_digest);

CREATE TABLE IF NOT EXISTS delivery_jobs (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    kind            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload         BYTEA NOT NULL DEFAULT ''::bytea,
    state           TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    available_at    TIMESTAMPTZ NOT NULL,
    lease_id        TEXT NOT NULL DEFAULT '',
    leased_until    TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    terminal_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_jobs_idempotency
    ON delivery_jobs (idempotency_key) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_due
    ON delivery_jobs (available_at, created_at, id) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_terminal
    ON delivery_jobs (terminal_at) WHERE state <> 'pending';
```

SQLite / libSQL — `ALTER TABLE ADD COLUMN` has no `IF NOT EXISTS`; run each
`ADD COLUMN` once (the host migration runner already tracks applied files):

```sql
ALTER TABLE users    ADD COLUMN auth_revision          INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN authenticated_at       TEXT;
ALTER TABLE sessions ADD COLUMN authentication_methods TEXT    NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN assurance_level        TEXT    NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS challenges (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    secret_digest    TEXT NOT NULL,
    protector_key_id TEXT,
    context          TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    expires_at       TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    version          INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_user_purpose ON challenges (user_id, purpose);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_purpose_secret_digest ON challenges (purpose, secret_digest);

CREATE TABLE IF NOT EXISTS contact_changes (
    id                     TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id                TEXT NOT NULL,
    kind                   TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    new_value              TEXT NOT NULL,
    login_enabled          INTEGER NOT NULL DEFAULT 0,
    recovery_enabled       INTEGER NOT NULL DEFAULT 0,
    notification_enabled   INTEGER NOT NULL DEFAULT 1,
    make_primary           INTEGER NOT NULL DEFAULT 0,
    replaces_identifier_id TEXT NOT NULL DEFAULT '',
    expires_at             TEXT NOT NULL,
    created_at             TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_contact_changes_user_kind ON contact_changes (user_id, kind);

CREATE TABLE IF NOT EXISTS authentication_grants (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    session_id       TEXT NOT NULL,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    context_digest   TEXT NOT NULL,
    methods          TEXT NOT NULL DEFAULT '',
    assurance        TEXT NOT NULL DEFAULT '',
    authenticated_at TEXT NOT NULL,
    expires_at       TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    consumed_at      TEXT
);
CREATE INDEX IF NOT EXISTS idx_authentication_grants_session_purpose_context
    ON authentication_grants (session_id, purpose, context_digest);

CREATE TABLE IF NOT EXISTS delivery_jobs (
    id              TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    kind            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload         BLOB NOT NULL DEFAULT x'',
    state           TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    available_at    TEXT NOT NULL,
    lease_id        TEXT NOT NULL DEFAULT '',
    leased_until    TEXT,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    terminal_at     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_jobs_idempotency
    ON delivery_jobs (idempotency_key) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_due
    ON delivery_jobs (available_at, created_at, id) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_terminal
    ON delivery_jobs (terminal_at) WHERE state <> 'pending';
```

After Step 5 the schema is v3-complete except for the still-present legacy
`users.email`/`email_verified` columns and the verification tables. **Deploy and
verify the v3 binary now.** The feature reads identifiers, not `users.email`, so
the app is fully functional at this point.

### Step 6 — LATER: cutover / drop (only after v3 is stable)

Run this only after the v3 binary has been confirmed healthy **and** the
recovery flows that replaced the verification rail are verified end to end
(registration email verification and forgot/reset password both complete on the
v3 challenge rail — the `challenges` table, with delivery drained through the
runtime the host wired: `delivery_jobs` for a host still on a pre-refactor cut, or
the generic jobs / `in_process` runtime past it — and the Step-4 parity checks
still hold). The legacy `verification_codes` / `verification_tokens`
tables are inert to the v3 binary; drop them only once that cutover succeeds.
This step is the point of no return for a v2-binary rollback.

pgx (`ALTER TABLE ... DROP COLUMN` is a metadata operation on Postgres):

```sql
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users DROP COLUMN email_verified;
DROP TABLE verification_codes;
DROP TABLE verification_tokens;
```

SQLite / libSQL — dropping columns is the standard 12-step table rebuild (more
portable than relying on a specific `DROP COLUMN`-capable engine version); wrap
it in one transaction with foreign keys off:

```sql
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE users_new (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    display_name  TEXT NOT NULL DEFAULT '',
    auth_revision INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
INSERT INTO users_new (id, display_name, auth_revision, created_at, updated_at)
    SELECT id, display_name, auth_revision, created_at, updated_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
DROP TABLE verification_codes;
DROP TABLE verification_tokens;
COMMIT;
PRAGMA foreign_keys=ON;
```

After Step 6 the host's `users` table matches the final canonical v3 shape
(`id, display_name, auth_revision, created_at, updated_at`) — the same
legacy-column removal, reached additively instead of by a blind canonical copy.

### Step 7 — Forward-only recovery and the no-blind-copy warning

- **Forward-only.** There is no down-migration that deletes backfilled identifier
  rows. If Steps 1–5 must be abandoned, restore the Step-1 backup or redeploy the
  v2 binary (Steps 1–5 are inert to it). If Step 6 has run, recovery requires a
  restore from the Step-1 backup — the v2 binary cannot read the rebuilt `users`.
- **Never blind-copy a canonical migration** onto a live v2 database. The
  canonical trees are greenfield/final-shape; this additive, backfill-first,
  validated procedure is the only supported path from a v2 database.
- **Never auto-resolve a collision.** A non-empty Step-1 dry-run stops the upgrade
  for a human decision.

### Runtime caveats (carried from live conformance)

These do not affect the migration DDL; they are runtime/parity behaviors a host
operator must know.

1. **turso hosts must run the connector with the write-intent transaction fix.**
   The step-up credential/identifier CAS rails (`Apply`, `ApplyVerifiedChange`)
   require the turso connector's `BEGIN IMMEDIATE` write-intent transactions
   (`integrations/datastores/turso/tx.go`). An older connector using default
   `DEFERRED` transactions returns `SQLITE_BUSY` to the CAS loser instead of
   `sdk.ErrConflict` under concurrency. Data integrity is never at risk either
   way (no double-commit), but a host on the pre-fix connector fails the
   concurrent step-up contract.
2. **pgx byte-order pagination parity requires a `C`-collation database.** An
   `en_US.utf8` Postgres host pages same-`created_at` lists in linguistic order,
   which diverges from SQLite/libSQL `BINARY` byte order on the id/subject/resource
   tiebreak. This is a pre-existing, parked finding in the shared
   `integrations/datastores/pgxdb` pagination helper — **not** fixed by this
   runbook. A host that needs cross-dialect byte-order pagination parity should
   run Postgres with `LC_COLLATE 'C'`. It does not affect any v3 rail or the
   migration itself.

### AV3-9.2 execution record

Executed 2026-07-13 against fresh/reset databases in the authorized playground
containers, both dialects, all four fixture paths; every fixture torn down after
the run (the long-lived conformance databases were never touched). Fixtures:
verified+password, unverified+password, OAuth-only (no password row),
password-only, and an un-normalized duplicate-collision pair.

- **pgx clean path** — fresh `C`-collation database (`TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'`), v2-shape seed (4 users / 3 passwords / 1 oauth /
  1 session / 1 invitation / 1 verification-code / 1 verification-token). Steps
  1–5: collision dry-run **0 rows**; Step-3 backfill **`INSERT 0 4`**; Step-4
  **parity 4/4**, 0 users missing a primary email, 0 duplicate auth-claims,
  0 orphan passwords/oauth/sessions. Step-3 **re-run `INSERT 0 0`** (idempotent,
  identifier count still 4). Verification state preserved exactly (unverified →
  `verified_at NULL`, all others non-NULL). Step-6 `DROP COLUMN email` /
  `email_verified` + `DROP TABLE verification_codes/verification_tokens` clean;
  `users` left at `id, display_name, created_at, updated_at, auth_revision`.
  AFTER: passwords/oauth/sessions/invitations byte-identical to BEFORE, session
  metadata columns present and defaulted. **PASS.**
- **pgx collision path** — fresh `C`-collation database with an un-normalized
  duplicate (` Verified@Example.com ` vs `verified@example.com`): Step-1 dry-run
  **returned `verified@example.com` with both user ids** (abort signal); forcing
  the Step-3 backfill anyway **failed on `idx_user_identifiers_auth_claim`**
  (`duplicate key value violates unique constraint`) and left `user_identifiers`
  **empty** (0 rows — no partial migration). **PASS (expected failure observed).**
- **SQLite/libSQL clean path** — executed against the live libsql server
  (`http://127.0.0.1:8080`, `POST /v2/pipeline`) in an isolated table namespace so
  the standing conformance schema was untouched; identical runbook SQL. Steps 1–5:
  dry-run **0 rows**, backfill **4 rows**, parity **4/4**, 0 missing-primary,
  0 duplicate auth-claims, 0 orphans; re-run **0 inserted** (idempotent); Step-6
  **12-step table rebuild** left `users` at the final v3 shape with identifiers
  intact and both verification tables dropped; AFTER counts preserved
  (users/passwords/oauth/sessions/invitations/identifiers = 4/3/1/1/1/4),
  verification state exact. **PASS.**
- **SQLite/libSQL collision path** — live libsql server, un-normalized duplicate:
  dry-run detected `verified@example.com` (x2); forced backfill **aborted**
  (`UNIQUE constraint failed: user_identifiers.kind, normalized_value`) with
  **0 rows** written. **PASS (expected failure observed).**

**Do not apply this runbook to segovia or another real host in this milestone.**

## Auth delivery-runtime upgrade runbook (bespoke auth outbox → generic jobs / in_process)

A **host-owned** procedure for a database already running auth-v3 as shipped
through the AV3D-5.0 cut — i.e. one that scaffolded and populated the bespoke
`delivery_jobs` outbox table — that is upgrading to auth at or past the AV3D
delivery refactor (2026-07-13). After the refactor auth owns **no** delivery
table: durable delivery is the generic **jobs** feature's `fenced_job_queue`
(host-owned in the jobs migration tree) and `in_process` delivery is a bounded
ephemeral pool with no table. `Repositories.DeliveryJobs`, the private
claim/poll/purge worker, and `Service.RunDeliveryWorker` are gone; the send path
is now `Config.DeliveryMode` + a host-wired `Config.DeliveryDispatcher` (jobs
mode) or `Service.RunDelivery(ctx)` (`in_process`).

Per the standing greenfield-migrations rule (2026-07-12) this is **not** a
canonical migration — the canonical auth set ships the final schema only
(`0001–0013`, no `delivery_jobs`). This runbook is host-side prose; every DDL
step below runs from the host's **own** migration tree, pre-boot, exactly like
every other host-owned migration.

**A host with an EMPTY or absent `delivery_jobs` table has nothing to drain** —
skip Steps 1–2/4, apply the new host wiring (Step 3), drop the empty table
(Step 5), and start the chosen runtime (Step 6).

**Tooling never decrypts a payload.** Every `delivery_jobs.payload` (and every
`fenced_job_queue.payload`) is an opaque, AES-GCM-sealed `command.Envelope`
(`AUTH_DELIVERY_ENCRYPTER_KEY`) carrying the rendered secret and destination. No
step here — count or drop — opens that ciphertext. Only the running auth delivery
processor, holding the encrypter key, ever unseals it: during this upgrade that is
the OLD binary's worker as it drains.

The drain is the **single supported path**. A prior draft offered an opaque
export/re-enqueue copy of the bespoke rows into the generic queue; that path is
unsafe and has been removed — the legacy ciphertext encodes the removed bespoke
envelope shape (not the generic queue's versioned command), the legacy rail kinds
(`email`/`phone`) are not the registered `authentication.delivery` job kind the
generic runtime dispatches on, the copy never terminalized its source rows (so the
zero-source-count check could never pass honestly), and the libSQL variant minted
`datetime('now')` strings the turso connector's fixed-width `Time.Scan` cannot
parse. An opaque copy cannot work in either dialect. Drain-then-drop is the only
supported procedure off the bespoke outbox.

### Preconditions

- A confirmed, restorable **backup** taken before Step 1.
- The OLD binary retains its existing **`AUTH_DELIVERY_ENCRYPTER_KEY`** through the
  drain: it is the only process that unseals `delivery_jobs` payloads and must hold
  the key that sealed them. No payload crosses queues (the drain is in place), so no
  cross-queue key portability is required; the NEW binary seals its own newly
  admitted work under its own key.
- **Do not rotate the delivery key mid-drain** — the old worker cannot unseal
  in-flight rows that were sealed under the previous key.

### Deploy ordering (single cutover — do NOT roll)

Old and new binaries must not both serve the same database across the cutover:
the old binary claims `delivery_jobs`, the new binary has no code that reads it.
Quiesce the old binary's admission, drain, upgrade, then start the new runtime.

### Step 1 — Stop old delivery workers and quiesce admission

Stop the old binary's delivery worker loop (`Service.RunDeliveryWorker`) — or
stop the old binary outright — and quiesce admission so no NEW `delivery_jobs`
rows are written (drain traffic to the start endpoints, or take the maintenance
window). No new opaque command lands in the bespoke table from this point.

### Step 2 — Drain the pending encrypted commands

Keep the OLD binary's delivery worker running (admission still quiesced from
Step 1) until it processes every non-terminal row: `state = 'pending'` rows are
sent or retried to their terminal state (`succeeded`/`failed`/`canceled`), and
terminally undeliverable rows discard their bound challenge best-effort. A leased,
in-flight row is still `state = 'pending'` (the lease is `lease_id`/`leased_until`,
not a separate state), so it counts as non-terminal until the worker terminalizes
it. When the pending count reaches zero (Step 4), upgrade.

The drain preserves at-least-once semantics and never decrypts a payload in
tooling: only the old worker, holding the encrypter key, unseals a row, and it
does so on the normal send path.

- *Tradeoffs.* Requires the old binary + worker to keep running through the
  drain; the drain is bounded by the old queue's retry/backoff horizon (a row in
  long backoff delays completion; you may let it dead-letter rather than wait).
  No encryption handling, no key coupling, no logical-key bookkeeping, no payload
  movement. A large dead-letter/backoff backlog only means a longer drain window,
  not a different path — there is no supported alternative to the drain.

### Step 3 — Apply the generic jobs schema and new host wiring

- **jobs mode (recommended production posture).** Scaffold the generic jobs
  migration tree into the host (`jobsstore.ExportMigrations` from
  `features/jobs/stores/{pgx,turso}`; canonical set `0001_job_queue`,
  `0002_job_schedules`, `0003_fenced_job_queue` — identical filename set across
  both dialects) and apply it pre-boot. Wire `Config.DeliveryMode = "jobs"`, a
  `Config.DeliveryDispatcher` backed by the generic jobs feature, and set the
  `DeliveryJobsAcknowledged` wiring assertion (production requires it —
  `ErrDeliveryJobsUnacknowledged`). A composition adapter (never a feature core)
  bridges auth's `Service.DeliveryJobRuntime()` onto `jobs.Runtime`.
- **in_process mode.** No jobs schema is needed (the bounded pool owns no table).
  Set `Config.DeliveryMode = "in_process"`, keep `Config.DeliveryEncrypter`, and
  set `DeliveryEphemeralAcknowledged` (production requires the crash-loss
  acknowledgment — `ErrDeliveryEphemeralUnacknowledged`). Accepted work does NOT
  survive a restart, so the bespoke outbox must be fully drained (Step 2) before
  cutover — there is no supported path that moves a durable backlog into an
  ephemeral queue.

`Register` starts no runtime in either mode; the host runs the selected runtime
in Step 6.

### Step 4 — Verify no active auth delivery rows remain

Before dropping the table, confirm the bespoke outbox holds no live work. The
active-work count MUST be zero:

pgx / SQLite / libSQL:

```sql
-- Active (unprocessed) bespoke delivery rows — MUST be 0 before Step 5.
SELECT count(*) AS active_delivery_jobs FROM delivery_jobs WHERE state = 'pending';
```

The Step-2 drain drives this to 0: once every `state = 'pending'` row (including
leased, in-flight rows) has reached a terminal state, the count is exact and no
row is lost or duplicated (terminal rows are retained with `terminal_at` set until
Step 5 drops the table). A non-zero active count **stops the upgrade** — finish the
drain first; never drop a table with live encrypted work in it.

### Step 5 — Drop the obsolete `delivery_jobs` table (host-owned)

Once Step 4 shows zero active rows, remove the bespoke table from the host's own
migration tree. This is a host-owned destructive migration, not a canonical one.

pgx:

```sql
DROP TABLE IF EXISTS delivery_jobs;
```

SQLite / libSQL (`DROP TABLE` also drops the table's indexes):

```sql
DROP TABLE IF EXISTS delivery_jobs;
```

This is the point of no return for reading any residual bespoke row; take it only
after Step 4 is clean (zero active rows).

### Step 6 — Start the generic jobs runtime or the bounded runtime

- **jobs mode:** start the generic jobs runtime the host wired in Step 3
  (`go rt.Run(ctx)` for the composed `jobs.Runtime`); cancel the ctx to drain.
  Newly admitted commands now process on the durable fenced queue.
- **in_process mode:** start the bounded pool with `go authSvc.RunDelivery(ctx)`
  for the process lifetime; cancel the ctx for a bounded shutdown drain.

Confirm end to end that a fresh start endpoint (register verification,
forgot-password, passwordless start) delivers OFF the request path on the new
runtime before reopening admission.

### Forward-only recovery and the no-decrypt / no-blind-copy warnings

- **Forward-only.** There is no down-migration. If the upgrade must be abandoned
  before Step 5, redeploy the old binary (it still reads `delivery_jobs`); after
  Step 5 recovery requires the Step-1 backup.
- **Never decrypt a payload in tooling.** The count and drop steps treat
  `payload` as opaque bytes. Only the running processor with the encrypter key
  unseals it — during this upgrade that is the old worker draining the outbox.
- **Never blind-copy a canonical migration.** The canonical auth set is
  greenfield/final-shape and carries no `delivery_jobs`; this host-owned
  drain-then-drop procedure is the only supported path off the bespoke outbox.
- **`in_process` is ephemeral.** Accepted work does not survive a restart, so
  drain the bespoke outbox before cutover; there is no supported copy of a durable
  backlog into an ephemeral queue.

This delivery-runtime runbook has **not** been applied to a real application host
(no auth module tag has been cut — `git tag -l` is empty, so the greenfield
rewrite that removed `delivery_jobs` from the canonical set is allowed with no
append-only constraint).

### AV3-9.8 drain-path fixture verification (both dialects)

The drain-only path's Step-4 verification query and source-row accounting were
proven on disposable fixtures on both dialects (IX-01 remediation). Each fixture
seeds legacy-shape `delivery_jobs` rows in mixed states, runs the Step-4 count,
terminalizes the remaining non-terminal rows exactly as a drained worker would,
and re-runs the count — proving it reaches 0 with no row lost or duplicated.

- **pgx** — disposable database `dr_drain_fixture` (`TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'`), legacy `delivery_jobs` created from the shipped
  `0014` shape. Seed: 5 rows — 2 `pending` (one unleased, one leased/in-flight),
  1 `succeeded`, 1 `failed`, 1 `canceled`. Step-4 count → `active_delivery_jobs =
  2` (the leased in-flight row counts as pending — exact). Drained-worker
  terminalize (`UPDATE … SET state='succeeded', terminal_at=now(), lease_id='',
  leased_until=NULL WHERE state='pending'`) → `UPDATE 2`. Step-4 re-count → `0`.
  Total accounting: `SELECT count(*)` = 5 before and after (no row lost or
  duplicated); terminal breakdown afterward `succeeded=3, failed=1, canceled=1`.
  Fixture database dropped afterward.
- **turso/libSQL** — live server, isolated `dr_`-prefixed fixture table
  `dr_delivery_jobs` created from the shipped turso `0014` shape (fixed-width TEXT
  timestamps). Same 5-row mixed-state seed via `POST /v2/pipeline`. Step-4 count →
  `2`. Terminalize (`… SET state='succeeded', terminal_at=<fixed-width ISO>,
  leased_until=NULL WHERE state='pending'`) → 2 rows affected. Step-4 re-count →
  `0`. Total accounting: 5 rows before and after; terminal breakdown `succeeded=3,
  failed=1, canceled=1`. All `dr_`-prefixed fixture tables dropped afterward; the
  standing conformance schema was untouched.

Both container databases were left running; no canonical/conformance table was
modified.

## Migrating from the monolith repository

The old monolith (`github.com/gopernicus/gopernicus v0.5.4`) and this
multi-module repository share import-path prefixes: the monolith's packages and
the nested modules here both resolve under `github.com/gopernicus/gopernicus/...`.
Go module resolution cannot hold both in one active workspace — a `go.work` (or a
single module graph) that references the monolith and any module from this repo
sees two providers for one import prefix, so a migrating host can never keep the
old and new dependencies live side by side.

Migrate across a split workspace or module boundary instead: keep the
still-on-monolith code in its own module tree with its own workspace file, and
make the migrated tree's workspace reference only this repository's modules. The
first adopter's working pattern is a v2-only root `go.work` with a shadowing
`v1/go.work` for the legacy tree; the two trees converge only when the last
monolith import is gone.

## What this repo is not doing (yet)

- No CI-driven automated tagging — tags are cut by hand until a release
  workflow is built.
- No changelog convention is mandated yet; the tag message plus commit log is
  the record until one is adopted.

## Independent MCP OAuth coordinated release

The working implementation adds opt-in OAuth authorization, restricted MCP-to-API
exchange and independently revocable delegated sessions. Coordinate core
`pockets/authentication` v0.13.0 with pgx v0.7.0 or Turso v0.6.0 and optional
Goth views v0.5.0. Firestore v0.2.0 is a first-party compatibility release.
Publication status and exact archive verification belong in
[the release record](plans/authentication-mcp-oauth-release.md).
Firestore compatibility does not imply
support for the OAuth storage capability.

Host rollout: export the unchanged historical ledger plus new `0019_oauth2_sessions.sql`,
apply it before boot, drain old authentication writers, deploy compatible packages,
and then enable explicit issuer/resource/client trust/secret/view configuration.
Verify W/A/B independence, refresh replay, MCP/API audience rejection, global revoke
fencing, datastore-failure denial, and actual Claude discovery/consent before
production enablement. See [AUDIT-049](AUDIT.md#audit-049-independent-mcp-oauth-sessions).

A rollback must stop delegated issuance/MCP traffic and drain or revoke delegated
sessions before restoring an old binary. Keep the additive migration installed.
The release evidence must distinguish local SQLite from hosted Turso, PostgreSQL
from unrun Firestore, automated HTTP flows from visual browser verification, and
local candidate archives from public-proxy/checksum verification after publication.

## Authentication v0.13.1: shared-route delegated admission

The optional `DelegatedAudience(resources...)` middleware option admits delegated
tokens for exact resources while preserving existing web-session and API-key
admission. Strict `Audience(...)`, `FirstParty`, kinds/transports, live delegated
checks and no-fallback resolution remain unchanged. Combined audience options
must both match. This is an independently adoptable host-configuration patch:
only `pockets/authentication` is tagged; current SQL/view/Firestore versions and
all dependency pins remain unchanged. No migration or production rollout occurs
as part of this release. See [the release record](plans/authentication-delegated-audience-release.md).

## Authentication login page password posture

Authentication v0.13.2 adds a zero-default `LoginPage.PasswordFlowsDisabled` field
derived from the existing service setting. Goth views v0.5.1 pins that core and
hides the disabled password form plus registration/recovery links. OAuth and
passwordless choices remain available. Upgrading the core with older views is
compatible; adopting the corrected bundled renderer selects its required core.
These are presentation bugfix patches with no route-security, schema, store or
mandatory configuration changes. Custom renderers should honor the new field.
See [the fix and release record](plans/authentication-login-password-posture.md).
