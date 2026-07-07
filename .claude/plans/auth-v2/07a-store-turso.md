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
