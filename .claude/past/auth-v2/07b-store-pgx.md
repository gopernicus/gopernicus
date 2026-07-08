# Phase A7b — `features/auth/stores/pgx` extension

Status: RATIFIED (cut from design §10 + §9)
Executor model: opus
Depends on: A7a (the canonical migration version-filename set is authored
there; this phase reproduces it EXACTLY — identical filenames, gaps
reproduced, per the charter §3 rule).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §10, §9 —
same contracts as A7a. Template: this module's own v1 files (pgx
conventions: TIMESTAMPTZ, JSONB for `Details`/payloads,
`POSTGRES_TEST_DSN` gating).

## Work items

1. **Migrations**: the six files with filenames IDENTICAL to A7a's
   (0006–0011), pgx dialect (TIMESTAMPTZ, JSONB, the same uniqueness,
   partial-index, and **named secondary-index set as A7a**; tables and
   indexes `IF NOT EXISTS`; `security_events` append-only, no FK
   enforcement, NO rebac/outbox anything).
2. **Repositories** for all six ports over the pgx connector,
   implementing the SAME pinned contracts as A7a: **GetByHash selects by
   `key_hash` alone, returns any present row (revoked/expired included,
   NULL expiry = never-expires), `ErrNotFound` only for unknown — no
   SQL-side expiry filtering**; `Consume` = `DELETE … RETURNING`
   (delete-regardless-of-expiry, `ErrExpired` computed in Go); **no
   `ON CONFLICT` upsert on oauth_accounts** (plain INSERT →
   `ErrAlreadyExists` via error mapping); JSON nil/empty round-trip to
   non-nil empty; parameterized dynamic WHERE. Pagination/nullable
   templates: `features/cms/stores/pgx/entries.go` +
   `features/jobs/stores/pgx/queue.go` (crud cursors, the pinned
   `ORDER BY <field> DESC, id DESC`); nullable timestamps as plain
   `*time.Time` (the pgx-side CMS pattern).
3. **Conformance**: the full `features/auth/storetest` suite env-gated on
   `POSTGRES_TEST_DSN`, cleaning auth tables per newRepos call. **The
   hardcoded `authTables` truncate slice in THIS module's conformance
   test file gains the same six tables, child-before-parent (`api_keys`
   before `service_accounts`; the rest before `users`).**
4. No Makefile/go.work changes.

## The live gate

`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`
(or the established :55432 mapping), export `POSTGRES_TEST_DSN`, then
`go test -count=1 ./...` until GREEN.

## Acceptance

```sh
cd features/auth/stores/pgx && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
make check
diff <(ls features/auth/stores/turso/migrations) <(ls features/auth/stores/pgx/migrations)   # empty — identical version sets
```

Plus the live run — recorded as a dated NOTES.md artifact at milestone
close.

## Real-interaction check

Standing check (a), plus the live run.

## Execution log

(append dated entries here)

### A7b — 2026-07-07 — PASS

Executor: opus. Extended `features/auth/stores/pgx` with the canonical 0006–0011
migration version set (filenames IDENTICAL to A7a's turso set, pgx dialect) +
six repositories over the existing `pgxdb` connector, conformance wiring, and the
live docker-postgres leg. No Makefile/go.work changes (module already existed).

**Work items.**

1. Migrations 0006–0011 authored with filenames reproduced EXACTLY from A7a:
   `0006_oauth_accounts.sql`, `0007_oauth_states.sql`,
   `0008_service_accounts.sql`, `0009_api_keys.sql`, `0010_security_events.sql`,
   `0011_invitations.sql`. pgx dialect: TIMESTAMPTZ timestamps, native BOOLEAN,
   `IF NOT EXISTS` on tables AND indexes, no FK, `security_events` append-only
   (no updated_at). Same index set as turso: `oauth_accounts(user_id)`; unique
   `api_keys(key_hash)` + `api_keys(service_account_id)`; unique
   `invitations(token_hash)` + PARTIAL unique
   `invitations(resource_type,resource_id,identifier,relation) WHERE
   status='pending'` + `invitations(resource_type,resource_id)` +
   `invitations(resolved_subject_id)`; `security_events(created_at,id)` +
   user/type/status filter indexes. `(provider,provider_user_id)` PK on
   oauth_accounts. NO rebac tables, NO outbox columns. `details` = JSONB;
   `oauth_states.payload` = BYTEA (see divergence).
2. Six repositories honoring the SAME pinned contracts as A7a: GetByHash selects
   by `key_hash` alone, returns any present row (revoked/expired/NULL-expiry),
   no SQL expiry filter; oauthstate `Consume` = `DELETE … RETURNING`,
   `ErrExpired` computed in Go; NO `ON CONFLICT` on oauth_accounts (plain INSERT
   → `ErrAlreadyExists`); `invitations.UpdateStatus` = `UPDATE … RETURNING`;
   Details nil/empty → `'{}'`, reads back non-nil empty; security-event `List`
   dynamic WHERE fully parameterized (`$n` via fmt.Sprintf on len(args), never
   concatenated values); `TouchLastUsed` plain UPDATE. Keyset pagination
   (`created_at DESC, id DESC`, id tiebreak) factored into a shared generic
   `listPage` helper (pagination.go, pgx `$n` twin of turso's). Nullable
   timestamps as `*time.Time` scan targets (`nullableTime`/`fromNullableTime`
   helpers), the pgx-side CMS pattern; native BOOLEAN, no bool↔int conversion.
3. conformance_test.go `authTables` extended child-before-parent: `api_keys`
   before `service_accounts`; oauth/audit/invitation tables before `users`.
   (TRUNCATE … CASCADE, no enforced FKs.)

**Files changed:** migrations 0006–0011 (new); repositories `oauth_accounts.go`,
`oauth_states.go`, `service_accounts.go`, `api_keys.go`, `security_events.go`,
`invitations.go` (new); `pagination.go` (new, shared generic `listPage`);
`helpers.go` (+`orderField`, `nullableTime`, `fromNullableTime`); `postgres.go`
(wire six new stores into `Repositories`); `conformance_test.go` (authTables +6,
child-before-parent).

**Hermetic (acceptance):** `cd features/auth/stores/pgx` → `gofmt -l .` clean;
`go build ./...` ok; `go vet ./...` ok; `go test ./...` ok (loud-skip
conformance, `ok … 0.210s`). Root `make check` → `all checks passed` (26-module
set + integration-tag vet + all four guards). Migration-set diff
`diff <(ls …/turso/migrations) <(ls …/pgx/migrations)` → EMPTY (identical
version sets).

**Live conformance run (milestone-close NOTES artifact):** store =
`features/auth/stores/pgx`; suite = `features/auth/storetest` full (v1 leaves +
the six new sub-runners: OAuthAccounts, OAuthStates, ServiceAccounts, APIKeys,
SecurityEvents, Invitations); DSN class = disposable docker postgres:17
(`postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable`);
container `a7b-pg` on the established :55432 mapping (port verified free
pre-start); command `POSTGRES_TEST_DSN=… go test -count=1 -v ./...`; result
**PASS** — `--- PASS: TestConformance_Postgres (1.93s)` + `TestExportMigrations`,
`ok … 2.123s`, 0 FAIL, 69 `--- PASS` lines (identical leaf count to A7a's turso
run — cross-dialect parity), single test package, ~3s wall. Fresh container =
fresh ledger, so the A7a stale-0003-checksum wrinkle did NOT recur (migrations
0001–0011 applied cleanly on first run). Container removed
(`docker rm -f a7b-pg`); port 55432 confirmed 0 listeners afterward.

**Standing check (a):** `make check` green; `examples/minimal`
(`HOST=localhost PORT=8081`) `GET /` → 200, `GET /products/widget-3000` → 200;
killed; port 8081 free (0 listeners).

**Divergence logged:** the phase-file work items say "JSONB for Details/payloads",
but the inherited A7a pinned contract (enforced by the shared, unmodifiable
`storetest`) asserts a BYTE-EXACT `oauth_states.payload` round-trip INCLUDING a
non-JSON value (`[]byte("payload")`). JSONB re-canonicalizes JSON and rejects
non-JSON input, so it cannot satisfy that contract — same reasoning the jobs
`pgx` queue used to pick JSON-not-JSONB. Closest-correct: `security_events.details`
= JSONB (map round-trip, order-independent — matches the phase intent), but
`oauth_states.payload` = BYTEA (opaque byte-preserving blob). Nothing else
diverged from the phase pins.
