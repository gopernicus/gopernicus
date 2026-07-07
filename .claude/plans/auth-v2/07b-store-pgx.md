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
