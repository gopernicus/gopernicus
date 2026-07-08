# Phase A7a — `features/auth/stores/turso` extension

Status: RATIFIED (cut from design §10 + §9)
Executor model: opus
Depends on: A2–A6 (all six new port sets exist and pass storetest's
reference impls).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §10 (store
impact: six new tables, sessions UNCHANGED, source `"auth"` continuing
after v1's 0005), §9 (crud-typed listing — no `fop` anywhere), §3/§4/§5.1/§6
(the port contracts each repository implements). Template: the module's
own existing v1 files (trio-era conventions, fixed-width TEXT timestamps,
TEXT JSON, `-tags=integration` gating). This is the CANONICAL
version-set author — A7b reproduces the filenames exactly.

## Work items

1. **Migrations** (filenames pinned — cut refinement 8):
   `0006_oauth_accounts.sql`, `0007_oauth_states.sql`,
   `0008_service_accounts.sql`, `0009_api_keys.sql`,
   `0010_security_events.sql`, `0011_invitations.sql`. (The `"auth"`
   source is design vocabulary — the connectors dedupe by FULL FILENAME
   under ledger source `"default"`; do NOT look for a source parameter
   the connector API doesn't expose — plan-cut correction.) Turso
   conventions: fixed-width TEXT timestamps, TEXT JSON for
   `Details`/payloads; tables AND indexes use `IF NOT EXISTS`.
   Uniqueness per the ports — `(provider, provider_user_id)` on
   oauth_accounts, `token_hash` on invitations + the partial
   one-pending-per-`(resource_type, resource_id, identifier, relation)`
   index, `key_hash` lookup index on api_keys. **Named secondary indexes
   (plan-cut amendment):** `oauth_accounts(user_id)`;
   `api_keys(parent service_account_id)`;
   `invitations(resource_type, resource_id)` and
   `invitations(resolved_subject_id)`; `security_events(created_at, id)`
   (desc-order support) + the user/type/status filter indexes.
   `security_events` is append-only (no record_state/updated_at). No FK
   enforcement (the v1 precedent). NO rebac tables (authorization-v1's
   source), NO outbox columns (deferred rail).
2. **Repositories** for all six ports over the existing connector
   `DB`/`MapError`, implementing the PINNED contracts: **GetByHash
   selects by `key_hash` ALONE and returns any present row — revoked and
   expired included, NULL `expires_at` = never-expires; `ErrNotFound`
   only for unknown hashes; NO expiry filtering in SQL** (A3's pinned
   contract — the service branches). **oauthstate `Consume` =
   `DELETE … RETURNING`** (the jobs queue.go precedent), deleting
   regardless of expiry, `ErrExpired` computed in Go from the returned
   row. **NO `ON CONFLICT` upsert on oauth_accounts** — plain INSERT,
   duplicate → `ErrAlreadyExists` via `MapError` (upsert is outside the
   port and dialect-divergent). JSON columns: nil/empty
   `Details`/`Payload` store `'{}'` (or NULL) and read back NON-NIL
   empty; the security-event `List` dynamic WHERE stays parameterized,
   never concatenated. `TouchLastUsed` as a plain UPDATE.
   `DeleteByUser` already landed in A1. **Pagination/nullable templates
   (plan-cut correction — v1 auth has ZERO paginated ports and no
   nullable columns to copy):** keyset pagination per
   `features/cms/stores/turso/entries.go` and
   `features/jobs/stores/turso/queue.go` (`crud.ListRequest` +
   `EncodeCursor`/`DecodeCursor`, the pinned
   `ORDER BY <field> DESC, id DESC`); nullable timestamps per the CMS
   `nullableTS`/`sql.NullString` pattern
   (`features/cms/stores/turso/helpers.go:30`).
3. **Conformance**: the full `features/auth/storetest` suite (v1 cases +
   every new sub-runner) under `-tags=integration` + `TURSO_*`, cleaning
   auth tables per newRepos call. **The hardcoded `authTables` truncate
   slice in this module's conformance test file gains the six new tables
   (plan-cut amendment, child-before-parent): `api_keys` BEFORE
   `service_accounts`; `oauth_accounts`, `oauth_states`,
   `security_events`, `invitations` before `users`.**
4. No Makefile/go.work changes — the module already exists (zero new
   modules this milestone).

## The live gate

The ONLY authorized turso database is
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` —
**verify the env URL matches before ANY run** (the .env may point
elsewhere); then `go test -tags=integration -count=1 ./...` until GREEN
(expect minutes — remote round-trips; the suite now carries ~6 more
sub-runners than v1's 16 leaves). **Playground mechanics (plan-cut
amendment, SRE):** after ANY edit to an already-applied migration file,
the checksum guard will fail the re-run — reset by clearing the six new
rows from the migrations ledger AND dropping the six new tables on the
playground before re-running. **One executor against the playground at a
time** — concurrent runs truncate each other's tables mid-suite.

## Acceptance

```sh
cd features/auth/stores/turso && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
make check
```

Plus the live run above — recorded as a dated NOTES.md artifact at
milestone close (suite, store, DSN class, result).

## Real-interaction check

Standing check (a), plus the live run.

## Execution log

(append dated entries here)

### A7a — 2026-07-07 — PASS

Executor: opus. Extended `features/auth/stores/turso` with the canonical
0006–0011 migration version set + six repositories over the existing
connector `DB`/`MapError`, conformance wiring, and the live playground leg.
No Makefile/go.work changes (module already existed).

**Migration filenames as authored (CANONICAL — A7b reproduces exactly):**
`0006_oauth_accounts.sql`, `0007_oauth_states.sql`,
`0008_service_accounts.sql`, `0009_api_keys.sql`,
`0010_security_events.sql`, `0011_invitations.sql`. Turso conventions:
fixed-width TEXT timestamps, TEXT JSON `details`, `IF NOT EXISTS` on tables
AND indexes, 0/1 INTEGER bools, no FK enforcement, `security_events`
append-only (no updated_at). Named indexes per the pin:
`oauth_accounts(user_id)`; unique `api_keys(key_hash)` (lookup+uniqueness) +
`api_keys(service_account_id)`; unique `invitations(token_hash)` + partial
unique `invitations(resource_type,resource_id,identifier,relation) WHERE
status='pending'` + `invitations(resource_type,resource_id)` +
`invitations(resolved_subject_id)`; `security_events(created_at,id)` +
user/type/status filter indexes. `(provider,provider_user_id)` PK on
oauth_accounts. NO rebac tables, NO outbox columns.

**Contracts honored:** GetByHash selects by `key_hash` alone, returns any
present row (revoked/expired/NULL-expiry included), no SQL expiry filter;
oauthstate `Consume` = `DELETE … RETURNING`, `ErrExpired` computed in Go;
NO `ON CONFLICT` on oauth_accounts (plain INSERT → `ErrAlreadyExists`);
`invitations.UpdateStatus` = `UPDATE … RETURNING`; Details nil/empty stores
`'{}'` reads back non-nil empty; security-event `List` dynamic WHERE
parameterized; `TouchLastUsed` plain UPDATE. Keyset pagination
(`created_at DESC, id DESC`, id tiebreak) factored into a shared generic
`listPage` helper (pagination.go). authTables truncate slice extended
child-before-parent: `api_keys` before `service_accounts`; oauth/audit/
invitation tables before `users`.

**Files changed:** migrations 0006–0011 (new); repositories
`oauth_accounts.go`, `oauth_states.go`, `service_accounts.go`,
`api_keys.go`, `security_events.go`, `invitations.go` (new);
`pagination.go` (new, shared `listPage`); `helpers.go`
(+`orderField`/`nullableTS`/`parseNullTime`); `turso.go` (wire six new
stores); `conformance_integration_test.go` (authTables +6).

**Hermetic (acceptance):** `cd features/auth/stores/turso && gofmt -l .`
clean; `go build ./...` ok; `go vet ./...` + `go vet -tags=integration
./...` ok; `go test ./...` ok (TestReference-backed suite + ExportMigrations,
loud-skip conformance). Root `make check` → `all checks passed`.

**Playground reset (SRE mechanics):** first live run failed a checksum guard
on `default:0003_sessions.sql` — a v1 file whose header comment A1 edited
after it was applied to the shared playground (SQL semantically unchanged:
`CREATE TABLE IF NOT EXISTS sessions`), NOT one of the new files; the single
wrapping migration transaction rolled back so nothing new was applied. Cleared
the one stale ledger row (`DELETE FROM schema_migrations WHERE
source='default' AND version='0003_sessions.sql'`) via a throwaway
integration helper (removed after use); the runner re-applied 0003 idempotently
and recorded the current checksum. No new tables/ledger rows needed dropping.

**Live conformance run (milestone-close NOTES artifact):** store =
`features/auth/stores/turso`; suite = `features/auth/storetest` full
(v1 leaves + the six new sub-runners: OAuthAccounts, OAuthStates,
ServiceAccounts, APIKeys, SecurityEvents, Invitations); DSN class =
playground remote (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`,
env-verified MATCH pre-run); command `go test -tags=integration -count=1
-p 1 -v ./...`; result **PASS** — `--- PASS: TestConformance_Turso
(204.40s)`, `ok … 204.608s`, 0 FAIL, 69 leaf PASS, one test package /
single sub-runner (`-p 1`), duration ~205s wall. Auth token never appeared
in output (grep for `authToken|eyJ|TURSO_AUTH_TOKEN=` → 0).

**Standing check (a):** `make check` green; `examples/minimal`
(`HOST=localhost PORT=8081`) `GET /` → 200, `GET /products/widget-3000` →
200; killed; port 8081 free (0 listeners).

Divergence logged: the 0003 checksum reconciliation above (prior-phase edit,
not an A7a file). Nothing else diverged from the phase pins.
