# Phase 2 — schema and SQL stores

Status: READY after phases 0–1.
Depends on: all phase-0 tasks, all phase-1 tasks.
Design: §§2.1–2.5, 3.2, 5.0, 5.6, 6.1.1, 10.

## Goal

Implement every new persistence contract in pgx and turso, prove dialect parity,
and draft the host migration. Preserve a buildable transition: legacy user
email/verification columns and verification tables remain until phases 3 and 5
move their callers, then are removed from the final canonical set.

## Task AV3-2.1 — transitional canonical migrations and parity test

Touch: both authentication migration trees and migration inventory tests.

Add matching pure-create migrations for:

- `user_identifiers` with authentication-claim and primary partial unique
  indexes plus active-user index;
- `challenges` with `(user_id,purpose)` and `(purpose,secret_digest)` uniqueness;
- `contact_changes` with one active `(user,kind)` row;
- `authentication_grants` with session/user/purpose/context binding and indexes;
- `delivery_jobs` with keyed idempotency, due/lease indexes, and encrypted
  payload envelope; and
- `users.auth_revision` plus session authentication metadata required by phase
  0.

Use dialect-correct timestamps, booleans, JSON/text, and partial-index syntax.
IDs follow the current inline DB-default convention. Add a test that asserts
identical migration filename sets and expected table/index inventory. Do not yet
delete verification tables or legacy email columns; tasks AV3-3.4 and AV3-5.5
own those cutovers.

Verify:

```sh
cd features/authentication/stores/pgx && go test ./...
cd features/authentication/stores/turso && go test ./...
make check
```

## Task AV3-2.2 — pgx store implementations

Depends on: AV3-2.1.
Touch: `features/authentication/stores/pgx`, its repository constructor/export,
integration tests.

Complete and audit pgx adapters for identifier/user aggregate operations,
contact-change, atomic challenge operations, authentication grants,
credential-mutation snapshot/revision-CAS apply, and delivery jobs.

Critical requirements:

- `CreateWithPrimaryIdentifier`, `ApplyVerifiedChange`, and credential mutation
  use real transactions and map uniqueness/conflict errors to stable sdk errors.
- `ConsumeToken` uses one atomic delete-returning operation.
- `ConsumeCode` locks/selects the `(user,purpose)` row, chooses the digest
  candidate matching `protector_key_id`, and performs compare + attempts/delete
  within one transaction. Exactly one correct concurrent request wins.
- Delivery claim uses database atomicity/lease predicates, not read-then-write.
- Expired challenge/grant/contact-change behavior deletes as promised.
- Every new store is included in the exported repositories bundle.

Run the full exported storetest suite from the pgx integration harness. Hermetic
unit tests may construct errors/queries, but live conformance is required by
AV3-2.4.

Verify:

```sh
cd features/authentication/stores/pgx && go build ./... && go test ./... && go vet ./...
make check
```

## Task AV3-2.3 — turso store implementations

Depends on: AV3-2.1.
Touch: `features/authentication/stores/turso`, bundle/export, integration tests.

Complete and audit the same contracts and error semantics as AV3-2.2 using the connector's
transaction/query facilities. Match the suite; do not simplify concurrency
promises because SQLite/libSQL has different locking behavior. For
`ConsumeCode`, keep read/compare/attempt-or-delete within one write transaction.
For delivery claims, use an atomic conditional update/delete-returning pattern
supported by the connector.

Verify:

```sh
cd features/authentication/stores/turso && go build ./... && go test ./... && go vet ./...
make check
```

## Task AV3-2.4 — dual-dialect live conformance and race evidence

Depends on: AV3-2.2, AV3-2.3.

Against fresh/reset authorized test databases:

- run the entire authentication `storetest` suite for pgx and turso;
- run targeted concurrent correct-code, correct-token, attempt-4→5,
  identifier-claim, credential-revision, and delivery-claim tests repeatedly;
- inspect stored challenge rows to confirm short code plaintext is absent;
- inspect delivery-job columns to confirm destination/rendered payload is one
  encrypted envelope;
- record database versions, DSNs with credentials redacted, commands, and pass
  counts in the execution log.

Do not reset a shared/long-lived database without confirming it is the
authorized playground. A hermetic skip does not complete this task.

## Task AV3-2.5 — host upgrade runbook draft

Depends on: AV3-2.1.
Touch only a draft under this plan directory or the docs location designated by
the current release convention; final publication is AV3-9.2.

Draft exact pgx and SQLite/libSQL host-owned migration steps:

1. backup and dry-run collision queries;
2. create new tables/indexes;
3. backfill one primary email identifier per user with original verification
   timestamp/state and login/recovery/notification uses;
4. verify user/identifier row counts, uniqueness, and no orphan passwords,
   OAuth accounts, sessions, or invitations;
5. add auth/session metadata and new flow tables;
6. later cutover/drop or SQLite table rebuild after application deployment;
7. forward-only recovery procedure and explicit no-blind-copy warning.

Test the SQL on disposable databases using representative verified/unverified,
OAuth-only, password-only, and duplicate-collision fixtures. Do not apply it to
an application host yet.

## Phase acceptance

```sh
make check
make guard
```

Plus recorded fresh pgx and turso conformance runs and a tested draft upgrade
runbook. Migration filename sets are identical.

## Stop conditions

- Either connector cannot provide the transaction/locking semantics promised by
  the port: stop and revise the port/design, never fake atomicity.
- Backfill fixtures expose normalized email collisions: stop with the collision
  report; do not choose a winner automatically.
- Live database identity is ambiguous or credentials target a non-test host:
  stop before connecting.

## Execution log

Append dated entries per completed task.

### 2026-07-12 — AV3-2.1 (transitional canonical migrations and parity test)

Dependencies: all phase-0 (AV3-0.1–0.6) and all phase-1 (AV3-1.1–1.4) tasks
complete and checked off in `TASKS.md` with execution-log entries; phase-0 and
phase-1 close gates green. Worktree changes preserved; no resets — the pre-existing
auth-v2 work (deleted `0012_id_defaults`/`0013_invitation_identifier_kind`, modified
`0001`/`0003`/`0008`–`0011` in both trees) is intact and built upon. Scope held to
migrations + the inventory/parity test; no store `.go` adapters, no truncate-list or
`Repositories` bundle changes (those are AV3-2.2/2.3), no verification-table/legacy-
email deletions (AV3-3.4/AV3-5.5 own those cutovers).

Files changed:

- `features/authentication/stores/{pgx,turso}/migrations/0001_users.sql` — added
  `auth_revision` to the existing greenfield users CREATE (pgx `BIGINT`, turso
  `INTEGER` — both back the int64 `users.auth_revision` §2.1/§5.6 optimistic anchor
  the identifier `ApplyVerifiedChange` and credential mutation `Apply` CAS on; single
  anchor, no separate revisions table).
- `features/authentication/stores/{pgx,turso}/migrations/0003_sessions.sql` — added
  the §5.0 session authentication-metadata columns backing the recent-primary-login
  shortcut: `authenticated_at` (nullable, NULL ↔ zero "not recorded"),
  `authentication_methods` (JSON array of honest descriptors, `TEXT NOT NULL DEFAULT ''`),
  `assurance_level` (`TEXT NOT NULL DEFAULT ''`). Purely additive/defaulted, so the
  existing explicit-column `SessionStore` INSERT/SELECT is unaffected; the store maps
  them in AV3-2.2/2.3.
- `features/authentication/stores/{pgx,turso}/migrations/0012_user_identifiers.sql`
  (new) — the §2.1 identity table the AV3-1.2 `IdentifierStore`/
  `CreateWithPrimaryIdentifier` already target: closed-`{email,phone}` CHECK,
  `verified_at`/`replaced_at` nullable sentinels, use/primary booleans, and the three
  §2.1 partial indexes (auth-claim `(kind,normalized_value)` WHERE active AND
  login/recovery; primary `(user_id,kind)` WHERE active AND is_primary; active
  `(user_id,kind,created_at)`).
- `features/authentication/stores/{pgx,turso}/migrations/0013_challenges.sql` (new) —
  the §3.2 atomic-secret table: `secret_digest` (no plaintext column), nullable
  `protector_key_id`/`context`, `attempt_count`/`version`, and the two uniques
  `UNIQUE(user_id,purpose)` + `UNIQUE(purpose,secret_digest)`.
- `features/authentication/stores/{pgx,turso}/migrations/0014_contact_changes.sql`
  (new) — the §2.4 pending-value flow state: `new_value` + use/primary/replacement
  fields lining up with `ApplyVerifiedChangeInput`, closed-kind CHECK, one-active
  `UNIQUE(user_id,kind)`; no secret column.
- `features/authentication/stores/{pgx,turso}/migrations/0015_authentication_grants.sql`
  (new) — the §5.0 step-up grant table: session/user/purpose/`context_digest`
  binding, `methods`/`assurance`, `authenticated_at`, nullable `consumed_at`, and the
  `(session_id,purpose,context_digest)` consume index (leading `session_id` serves
  DeleteBySession).
- `features/authentication/stores/{pgx,turso}/migrations/0016_delivery_jobs.sql`
  (new) — the §6.1.1 durable outbox: single opaque encrypted `payload` (pgx `BYTEA`,
  turso `BLOB` — AES-GCM ciphertext is binary; NO plaintext destination/message/
  identifier column), PII-free `idempotency_key`, lease fields, and the partial
  idempotency `UNIQUE(idempotency_key) WHERE state='pending'`, due
  `(available_at,created_at,id) WHERE pending`, and terminal-purge indexes.
- `features/authentication/stores/{pgx,turso}/migrations_test.go` (new) —
  `TestMigrationInventory` (embedded tree == the 16-file canonical set; every
  expected CREATE TABLE, the ten v3 indexes, and the `auth_revision`/session-metadata
  columns present) and `TestMigrationParity` (reads the sibling dialect's on-disk
  tree and asserts byte-for-byte identical filename SETS). Both modules assert the
  same canonical slice, so drift is caught from either module's `go test`.

Commands / results:

- `cd features/authentication/stores/pgx && go test ./...` — **PASS**
  (`TestMigrationInventory`/`TestMigrationParity` RUN+PASS; `TestConformance_Postgres`
  SKIPs loudly — no `POSTGRES_TEST_DSN` — live conformance is AV3-2.4, not this task).
- `cd features/authentication/stores/turso && go test ./...` — **PASS** (inventory +
  parity RUN+PASS; the integration-tagged live leg is not built without `-tags=integration`).
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module
  build/vet/test across every module; integration-tag compile-only vet incl. both auth
  stores; all guards green — `make guard` runs inside `make check`). `gofmt -l` on both
  new test files clean.

Premise adaptations:

- **No enforced foreign keys on the new tables.** Design §2.1's illustrative SQL shows
  `user_identifiers.user_id ... REFERENCES users(id) ON DELETE CASCADE`, but every
  existing auth migration follows the repo's logged "user_id references users.id by
  convention (no enforced FK)" decision, and the reference/authmem/memory conformance
  targets enforce no FK/cascade. Adding an enforced FK (or cascade) would break dialect
  parity with those memory impls and risk AV3-2.4 SQL-only failures where the storetest
  seeds a bare user_id (challenges/grants/contact_changes/delivery_jobs storetest rows
  are not tied to a created user). The aggregate atomicity the design requires (R7) lives
  in the `CreateWithPrimaryIdentifier`/`ApplyVerifiedChange` transactions — code, not a
  FK — so dropping the enforced FK weakens no atomicity or security promise. The v3 flows
  in scope never delete a user, so the cascade is unexercised. Documented in each new
  migration's header comment.
- **Dialect-correct int64 for `auth_revision`.** §2.1 writes generic `INTEGER`; applied
  dialect-correctly as pgx `BIGINT` / turso `INTEGER` to match the int64 domain field
  and the `var current int64` CAS scan (same treatment §2.1 already prescribes for
  BOOLEAN vs INTEGER-boolean).
- **`context`/`protector_key_id` nullable, `payload` binary.** Followed §3.2's pinned
  `TEXT NULL (JSON)` nullability for challenge `context`/`protector_key_id` (identical
  across dialects); delivery `payload` is `BYTEA`/`BLOB` (not the oauth_states `TEXT`
  path) because AES-GCM ciphertext is not valid UTF-8 — the AV3-2.3 turso delivery store
  binds `[]byte` to a BLOB column. Flagged for AV3-2.2/2.3 below.
- **Session metadata columns land without a store consumer yet.** The task pins them
  here ("session authentication metadata required by phase 0"); they are additive and
  defaulted so the current `SessionStore` (explicit column list) is unaffected. AV3-2.2/
  2.3 add the read/write mapping.

For AV3-2.2 (pgx) and AV3-2.3 (turso) store implementations: the migrations the
existing AV3-1.2 identifier/user store code targets now exist — `user_identifiers`
(exact column/index names match `identifiers.go`) and `users.auth_revision`. The four
new atomic-rail tables (`challenges`, `contact_changes`, `authentication_grants`,
`delivery_jobs`) and the session metadata columns are schema-ready but have NO SQL
adapters yet — those are AV3-2.2/2.3 to write against the reference/authmem contracts
frozen in phase 1. Notes for those tasks: (1) add the six new tables
(`user_identifiers`, `challenges`, `contact_changes`, `authentication_grants`,
`delivery_jobs`, and keep users last) to the `authTables` truncate lists in
`conformance_test.go`/`conformance_integration_test.go` and wire the new stores into
`Repositories()` — AV3-2.1 deliberately left both alone (scope); (2) delivery `payload`
is `BYTEA`/`BLOB` — bind `[]byte`, do not `string()`-convert it; (3) `challenge.context`
and `protector_key_id` are nullable — map ''↔NULL like the `nullHash` pattern; (4) the
partial unique indexes are the concurrency backstops the reference reproduces with a
mutex — `ConsumeCode`/delivery `Claim` must use one atomic write, not read-then-write.
The `authenticated_at`/`authentication_methods`/`assurance_level` session columns need
the same nullable-time + JSON-array mapping the domain `AuthenticationMetadata` implies.

### 2026-07-12 — AV3-2.2 (pgx store implementations)

Dependencies: AV3-2.1 complete and checked off in `TASKS.md` with its execution-log
entry; phases 0–1 closed. Worktree changes preserved (no resets); the pre-existing
auth-v2/JWT work and the AV3-2.1 migrations/parity test are intact and built upon.
Scope held to the pgx store module and its bundle/truncate-list; no turso work (AV3-2.3),
no live conformance run (AV3-2.4), no verification-table/legacy-email deletion.

Files changed:

- `features/authentication/stores/pgx/challenges.go` (new) — `ChallengeStore` filling
  `challenge.Repository`. `Replace` is a delete-prior-`(user,purpose)`-then-insert
  transaction (the `(purpose,secret_digest)` unique index → `sdk.ErrAlreadyExists`).
  `ConsumeCode` is one `InTx` that `SELECT … FOR UPDATE`-locks the `(user,purpose)` row,
  decides expiry (delete), selects the `DigestCandidate` whose `KeyID` matches the row's
  `protector_key_id`, compares via `auth.ConstantTimeDigestEqual`, and then increments
  `attempt_count`, deletes at `maxAttempts` (lockout), or consumes on success — the
  blocking `FOR UPDATE` makes exactly one concurrent correct code redeem and exactly one
  lockout delete win. `ConsumeToken` is one atomic `DELETE … RETURNING` by
  `(purpose,secret_digest)` (empty digest short-circuits `ErrNotFound`; expired returns
  `ErrExpired` with the row already deleted). `PurgeExpired` bounds with a `LIMIT`
  subquery. `protector_key_id`/`context` map `''`↔`NULL` via local `nullText`/`nullBytes`/
  `textFrom`/`bytesFrom` helpers.
- `features/authentication/stores/pgx/contact_changes.go` (new) — `ContactChangeStore`
  filling `contactchange.Repository`. `Create` is delete-before-insert per `(user,kind)`
  in a transaction (DB-generated id on empty). `Consume` is one `DELETE … RETURNING` by
  `(user,kind)` (expired → `ErrExpired`, row deleted; missing → `ErrNotFound`).
- `features/authentication/stores/pgx/authentication_grants.go` (new) — `AuthGrantStore`
  filling `authgrant.Repository`. `Consume` is one atomic
  `UPDATE … WHERE id = (SELECT … WHERE consumed_at IS NULL … FOR UPDATE SKIP LOCKED) RETURNING`,
  so exactly one concurrent consumer wins, a context mismatch never spends the grant, and
  an expired match is consumed then returns `ErrExpired`. `DeleteBySession` is bulk +
  idempotent. `methods` is JSON text (`encodeMethods`/`decodeMethods` over
  `[]session.AuthenticationMethod`); `consumed_at` maps `NULL`↔zero.
- `features/authentication/stores/pgx/credential_mutations.go` (new) —
  `CredentialMutationStore` filling `credential.MutationRepository`. `Snapshot` projects
  the typed `MethodSet` from `users.auth_revision` + `user_passwords` + `oauth_accounts` +
  active `user_identifiers` (unknown user → `ErrNotFound`). `Apply` is one `InTx` that
  `SELECT auth_revision … FOR UPDATE`, rejects a stale revision as `ErrConflict`, performs
  the closed-sum typed mutation (`RemovePassword`/`UnlinkOAuth`/`RetireIdentifier`/
  `ChangeIdentifierUses`), and bumps `auth_revision` exactly once — the blocking row lock
  yields exactly one concurrent winner.
- `features/authentication/stores/pgx/delivery_jobs.go` (new) — `DeliveryJobStore` filling
  `deliveryjob.Repository`. `Enqueue` returns the existing pending row or inserts (id +
  defaults read back via `RETURNING`); `Replace` cancels prior pending then inserts fresh.
  `Claim` leases the oldest due job (`available_at, created_at, id`) via
  `UPDATE … WHERE id = (SELECT … FOR UPDATE SKIP LOCKED) RETURNING`, so concurrent workers
  see one winner and an expired lease is reclaimable. `Succeed`/`Fail`/`Retry`/`Cancel` are
  lease-checked `FOR UPDATE` transitions (reclaimed lease → `ErrConflict`, same-terminal-
  state → idempotent nil). `PurgeTerminal` bounds with a `LIMIT` subquery. `payload` binds
  `[]byte` to `BYTEA` (never string-converted); `leased_until`/`terminal_at` map
  `NULL`↔zero.
- `features/authentication/stores/pgx/sessions.go` — mapped the AV3-2.1 §5.0 session
  metadata columns: `sessionRow` gains `authenticated_at` (nullable), `authentication_methods`
  (JSON text, reusing `encodeMethods`/`decodeMethods`), `assurance_level`; `toDomain` now
  returns `(Session, error)` and populates `Session.Authentication`; `Create` writes the
  three columns; `Get`/`GetByRefreshHash` handle the new error return. Additive and
  defaulted, so existing session behavior is unchanged.
- `features/authentication/stores/pgx/postgres.go` — wired the five new stores into the
  exported `Repositories()` bundle (`Challenges`, `ContactChanges`, `AuthenticationGrants`,
  `CredentialMutations`, `DeliveryJobs`), aligned the struct literal.
- `features/authentication/stores/pgx/conformance_test.go` — added the six new tables to
  the `authTables` truncate list (`user_identifiers`, `challenges`, `contact_changes`,
  `authentication_grants`, `delivery_jobs`, with `users` kept last) so a live
  `TestConformance_Postgres` run (AV3-2.4) starts each subtest from a clean set.

Commands / results:

- `cd features/authentication/stores/pgx && go build ./... && go test ./... && go vet ./...`
  — **PASS** (build clean; `TestMigrationInventory`/`TestMigrationParity` PASS;
  `TestConformance_Postgres` SKIPs loudly — no `POSTGRES_TEST_DSN`, live conformance is
  AV3-2.4; vet clean). `gofmt -l` on all touched files clean.
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test
  across every module incl. the reference + authmem hermetic conformance for the v3 rails;
  integration-tag compile-only vet incl. both auth stores; every guard green).

Premise adaptations:

- **Session metadata mapping included here.** AV3-2.2's critical-requirements list does not
  name it and no storetest case exercises it, but the AV3-2.1 handoff and the standing
  invariants pin the read/write mapping to phase 2; added it now for `Session.Authentication`
  round-trip fidelity ahead of the phase-5 re-key. It is additive/defaulted and hermetically
  compile-checked; its live round-trip rides the AV3-2.4 conformance run.
- **`CredentialMutations.Snapshot` projects real `user_identifiers`.** The reference/authmem
  use a §5.6 credential-projection stand-in map; the SQL store projects the actual active
  identifier rows (the storetest only asserts `HasPassword`/`AuthRevision`/`OAuth`, so the
  richer projection is unexercised here but correct for the phase-6 re-key). `RetireIdentifier`/
  `ChangeIdentifierUses` `Apply` branches likewise target `user_identifiers` directly; they
  compile and are internally consistent but are first exercised through the port in later
  phases.
- **Existence/revision anchor is the `users` row only.** The memory impls also count a user
  proven solely by password/oauth/identifiers without a `users` row; the SQL store anchors
  both existence and the CAS on `users.auth_revision` (the design's single anchor), which
  the storetest satisfies because every credential seed creates the user row.

For AV3-2.3 (turso): implement the same five stores + session-metadata mapping against the
turso connector with identical error semantics. Notes: (1) delivery `payload` is `BLOB` —
bind `[]byte`, never `string()`-convert; (2) `challenge.context`/`protector_key_id` and
`grant.consumed_at`/session `authenticated_at` are nullable — map `''`↔`NULL` /
zero↔`NULL`; (3) keep `ConsumeCode` read/compare/attempt-or-delete inside ONE write
transaction and use an atomic conditional `UPDATE`/`DELETE … RETURNING` for delivery
`Claim`, grant `Consume`, and challenge `ConsumeToken` (SQLite/libSQL has no
`FOR UPDATE SKIP LOCKED`, so rely on `BEGIN IMMEDIATE`/serialized writes — do not weaken the
single-winner promise); (4) `methods` (grant + session) is JSON text via the same
encode/decode shape; (5) add the same six tables (users last) to the turso truncate list and
wire the five stores into turso `Repositories()`; the pgx `conformance_test.go` truncate order
and bundle wiring are the reference to mirror.

### 2026-07-12 — AV3-2.3 (turso store implementations)

Dependencies: AV3-2.1 complete and checked off in `TASKS.md` (its migrations + parity test),
and per the standing serial protocol AV3-2.2 (the pgx reference) complete and checked off with
its execution-log entry; phases 0–1 closed. Worktree changes preserved (no resets); the
pre-existing auth-v2/JWT work, the AV3-2.1 migrations/parity test, and the AV3-2.2 pgx stores
are intact and mirrored. Scope held to the turso store module and its bundle/truncate-list; no
pgx changes, no live conformance run (AV3-2.4), no verification-table/legacy-email deletion.

Files changed:

- `features/authentication/stores/turso/challenges.go` (new) — `ChallengeStore` filling
  `challenge.Repository`, mirroring the pgx logic through the turso connector. `Replace` is a
  delete-prior-`(user,purpose)`-then-insert `InTx` (the `(purpose,secret_digest)` unique index →
  `sdk.ErrAlreadyExists` via `MapError`). `ConsumeCode` holds SELECT→Go-compare→attempt-or-
  delete inside ONE `InTx`: no `FOR UPDATE` (SQLite has none), so single-winner rests on the
  connector's serialized writes — the losing concurrent transaction aborts and the method returns
  `challenge.OutcomeNotFound` (the fail-closed zero value) on any infra error, exactly the pgx
  contract that keeps `countOutcome(Redeemed)==1`. Digest selection uses `auth.ConstantTimeDigestEqual`
  in-Go keyed on `protector_key_id`. `ConsumeToken` is one atomic `DELETE … RETURNING` by
  `(purpose,secret_digest)` (empty digest → `ErrNotFound`; expired → `ErrExpired`, row deleted).
  `PurgeExpired` bounds with a `LIMIT` subquery. Nullable `protector_key_id`/`context` map
  `''`↔`NULL` via `nullText`/`nullBytes` (write) and `sql.NullString`→`bytesFrom` (read).
- `features/authentication/stores/turso/contact_changes.go` (new) — `ContactChangeStore` filling
  `contactchange.Repository`. `Create` is delete-before-insert per `(user,kind)` in an `InTx`
  (DB-generated id on empty). `Consume` is one `DELETE … RETURNING` by `(user,kind)` (expired →
  `ErrExpired`, row deleted; missing → `ErrNotFound`). Booleans via `tursodb.Bool`/`BoolToInt`.
- `features/authentication/stores/turso/authentication_grants.go` (new) — `AuthGrantStore` filling
  `authgrant.Repository`, plus the shared `encodeMethods`/`decodeMethods` JSON helpers (the turso
  twins of the pgx pair, reused by `sessions.go`). `Consume` is one atomic
  `UPDATE … WHERE id = (SELECT … consumed_at IS NULL … LIMIT 1) AND consumed_at IS NULL RETURNING`
  — the `consumed_at IS NULL` predicate plus serialized writes make exactly one concurrent
  consumer win, a context mismatch never spends the grant, and an expired match is consumed then
  returns `ErrExpired`. `DeleteBySession` is bulk + idempotent. `consumed_at` maps `NULL`↔zero via
  `tursodb.NullTime`/`FormatNullTime`.
- `features/authentication/stores/turso/credential_mutations.go` (new) — `CredentialMutationStore`
  filling `credential.MutationRepository`. `Snapshot` projects the typed `MethodSet` from
  `users.auth_revision` + `user_passwords` (`EXISTS`) + `oauth_accounts` + active `user_identifiers`
  (unknown user → `ErrNotFound`). `Apply` is one `InTx` that reads `auth_revision`, rejects a stale
  revision as `ErrConflict`, performs the closed-sum typed mutation
  (`RemovePassword`/`UnlinkOAuth`/`RetireIdentifier`/`ChangeIdentifierUses`), and bumps
  `auth_revision` once — single-winner via serialized writes (the AV3-1.2 identifier
  `ApplyVerifiedChange` convention: the loser's transaction aborts). Booleans use `1`/`0` literals
  (SQLite has no `TRUE`/`FALSE`).
- `features/authentication/stores/turso/delivery_jobs.go` (new) — `DeliveryJobStore` filling
  `deliveryjob.Repository`. `Enqueue` returns the existing pending row or inserts (id + defaults
  read back via `RETURNING`); `Replace` cancels prior pending then inserts fresh. `Claim` leases the
  oldest due job (`available_at, created_at, id`) via
  `UPDATE … WHERE id = (SELECT … LIMIT 1) AND state='pending' AND (leased_until IS NULL OR leased_until <= ?) RETURNING`
  — the outer lease re-check + serialized writes give one winner and an expired lease is
  reclaimable. `Succeed`/`Fail`/`Retry`/`Cancel` are lease-checked `InTx` transitions (reclaimed
  lease → `ErrConflict`, same-terminal-state → idempotent nil). `PurgeTerminal` bounds with a `LIMIT`
  subquery. `payload` binds `[]byte` to `BLOB` (never string-converted); `leased_until`/`terminal_at`
  map `NULL`↔zero via `tursodb.NullTime`.
- `features/authentication/stores/turso/sessions.go` — mapped the AV3-2.1 §5.0 session metadata
  columns: `sessionColumns` and `sessionRow` gain `authenticated_at` (`tursodb.NullTime`),
  `authentication_methods` (JSON text via the grant helpers), `assurance_level`; `toDomain` now
  returns `(Session, error)` and populates `Session.Authentication`; `Create` writes the three
  columns (11-placeholder INSERT); `Get`/`GetByRefreshHash` handle the new error return. Additive
  and defaulted, so existing session behavior is unchanged.
- `features/authentication/stores/turso/turso.go` — wired the five new stores into the exported
  `Repositories()` bundle (`Challenges`, `ContactChanges`, `AuthenticationGrants`,
  `CredentialMutations`, `DeliveryJobs`), matching pgx.
- `features/authentication/stores/turso/conformance_integration_test.go` — added the six new tables
  to the `authTables` truncate list (`user_identifiers`, `challenges`, `contact_changes`,
  `authentication_grants`, `delivery_jobs`, with `users` kept last), mirroring pgx order, so a live
  `TestConformance_Turso` run (AV3-2.4) starts each subtest from a clean set.

Commands / results:

- `cd features/authentication/stores/turso && go build ./... && go test ./... && go vet ./...`
  — **PASS** (build clean; hermetic `TestMigrationInventory`/`TestMigrationParity` PASS; the
  integration-tagged live `TestConformance_Turso` is not built without `-tags=integration`, so it
  neither runs nor skips here — live conformance is AV3-2.4; vet clean). `gofmt -l` on all touched
  files clean.
- `cd features/authentication/stores/turso && go vet -tags=integration ./...` — **PASS**
  (the live conformance leg compiles against the new bundle/truncate list).
- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test across
  every module; integration-tag compile-only vet incl. `features/authentication/stores/turso`; every
  guard green — `make guard` runs inside `make check`).

Premise adaptations:

- **`FOR UPDATE [SKIP LOCKED]` translated to serialized-write single-winner, per the design/handoff
  and the AV3-1.2 turso convention.** SQLite/libSQL has no `FOR UPDATE`; the connector's `InTx`
  (`BeginTx(ctx,nil)`, no statement retry) relies on the remote primary serializing writes so exactly
  one concurrent writer commits and the loser's transaction aborts. `ConsumeCode` stays in one write
  `InTx` and returns `OutcomeNotFound` on any infra error (mirroring pgx), so a losing racer never
  reports `OutcomeRedeemed`/`OutcomeLockedOut`; grant `Consume`, delivery `Claim`, and challenge
  `ConsumeToken` are single-statement conditional `UPDATE`/`DELETE … RETURNING` whose predicates
  (`consumed_at IS NULL`, the lease re-check) are the atomic arbiter. The single-winner promise is
  NOT weakened; it is proven live in AV3-2.4 (this task's `go test` is hermetic — the concurrency
  storetest cases are integration-tagged).
- **Snapshot projects real `user_identifiers` (mirrors pgx AV3-2.2).** The reference/authmem use a
  §5.6 projection stand-in; the SQL store projects the actual active identifier rows and anchors both
  existence and the revision CAS on the `users` row (`auth_revision`), which the storetest satisfies
  because every credential seed creates the user row. The richer identifier projection and the
  `RetireIdentifier`/`ChangeIdentifierUses` `Apply` branches compile and are internally consistent but
  are first exercised through the port in later phases.
- **Session metadata mapping included here (mirrors pgx AV3-2.2).** Not named in AV3-2.3's task body
  and unexercised by any current storetest case, but the AV3-2.1 handoff and standing invariants pin
  the read/write mapping to phase 2; added for `Session.Authentication` round-trip fidelity ahead of
  the phase-5 re-key. Additive/defaulted and hermetically compile-checked; its live round-trip rides
  the AV3-2.4 conformance run.

For AV3-2.4 (dual-dialect live conformance and race evidence): both dialects' stores + bundle +
truncate lists are now complete and mirror each other. The turso live leg needs a fresh/reset
**authorized playground** Turso/libSQL database, run from the turso store module with
`go test -tags=integration ./...` and `TURSO_DATABASE_URL` + `TURSO_AUTH_TOKEN` set (see
`conformance_integration_test.go`; absent them it skips loudly). The pgx live leg needs
`POSTGRES_TEST_DSN` (e.g. a disposable `postgres:17` container) and `go test ./...` from the pgx
module. The concurrency storetest cases (`ConcurrentCodeSingleWinner`, `ConcurrentTokenSingleWinner`,
`ConcurrentLockoutSingleWinner`, `Grant/ContactChange ConcurrentConsumeSingleWinner`,
`ConcurrentApplySingleWinner`, `ConcurrentClaimSingleWinner`) are the ones that exercise the
serialized-write single-winner design on turso — run them repeatedly (`-count`/`-race`) and watch for
any turso `SQLITE_BUSY`/snapshot-conflict surfacing as a non-nil infra error that would let a loser
report a win; if that appears, it is a turso-connector transaction-mode finding (the connector uses a
DEFERRED `InTx` with no retry), not a store-logic bug. Also inspect stored `challenges` rows (no code
plaintext, only `secret_digest`) and `delivery_jobs.payload` (one opaque BLOB envelope, no plaintext
destination/message/identifier column).

### 2026-07-12 — AV3-2.4 (dual-dialect live conformance and race evidence) — BLOCKED

Status: **BLOCKED** on the turso live leg. pgx live conformance + race is fully green; the row-
inspection evidence is captured for both dialects; but the turso live leg surfaces a deterministic
turso-connector transaction-mode defect on the two read-then-write CAS rails, exactly the finding the
AV3-2.3 handoff predicted. Per this phase's stop condition ("Either connector cannot provide the
transaction/locking semantics promised by the port: stop and revise the port/design, never fake
atomicity") and the task's explicit instruction to report it "as a blocker/finding rather than papering
over it," AV3-2.4 is left unchecked in `TASKS.md` pending a turso-connector fix decision. No store `.go`
or connector code was changed by this task (verification-only); the two throwaway row-inspection tests
were run and deleted, leaving the worktree exactly as AV3-2.3 left it.

Dependencies: AV3-2.2 (pgx) and AV3-2.3 (turso) complete and checked off with execution-log entries;
phases 0–1 closed. Worktree changes preserved (no resets); only the two disposable Docker databases were
reset/truncated (the containers' lifecycle is orchestrator-owned).

Environment (redacted DSNs):

- **Postgres 17.10** (Debian, aarch64), container `authv3-pg`, `postgres://postgres:<redacted>@localhost:5432/<db>?sslmode=disable`.
  The container's default `postgres` database is `LC_COLLATE=en_US.utf8`. A byte-order database
  `authv3_cconf` (`TEMPLATE template0 LC_COLLATE 'C' LC_CTYPE 'C'`) was created in the same container for
  the byte-order-correct full-suite run (see premise adaptation 2).
- **libsql-server / sqld 0.24.33** (`ghcr.io/tursodatabase/libsql-server:latest`), container
  `authv3-libsql`, `TURSO_DATABASE_URL='http://127.0.0.1:8080'` + `TURSO_AUTH_TOKEN='<redacted>'`. The
  `http://` scheme worked directly (no `libsql://…?tls=0` fallback needed); the server has no auth
  configured and ignores the token, which the conformance harness only requires to be non-empty.

Commands / results:

- **pgx full suite (byte-order / C-collation DB):**
  `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='…/authv3_cconf…' go test ./...` — **PASS**
  (exit 0; 157 leaf subtests pass, 0 fail). This is the clean full-suite conformance record for pgx.
- **pgx full suite (container default `en_US.utf8` DB):**
  `… POSTGRES_TEST_DSN='…/postgres…' go test ./...` — **FAIL** on 8 pre-existing pagination-tiebreak leaves
  only: `ServiceAccounts/ListSameCreatedAtCollision`, `APIKeys/{Order,ListSameCreatedAtCollision}`,
  `SecurityEvents/ListSameCreatedAtCollision`, `Invitations/{ListByResourceCollision,ListBySubjectCollision,ByResource/PrevPage,BySubject/Order}`.
  **Every v3 rail passed** (Challenges 14/14, ContactChanges 6/6, AuthenticationGrants 7/7,
  CredentialMutations 5/5, DeliveryJobs 12/12, UserIdentifiers incl. `ConcurrentClaimArbitration`). The 8
  failures are collation-only (see finding 2) and live in the shared `integrations/datastores/pgxdb`
  pagination helper, not the auth store — pre-existing and unrelated to v3.
- **pgx race / repeated concurrency:**
  `… POSTGRES_TEST_DSN='…/authv3_cconf…' go test -race -count=10 -run 'TestConformance_Postgres/.*/Concurrent.*' ./...`
  — **PASS** (exit 0; 8 concurrency leaves × 10 = 80 executions under `-race`; 0 data races, 0 failures;
  ~17s). Matched leaves: `UserIdentifiers/ConcurrentClaimArbitration`, `Challenges/{ConcurrentCode,ConcurrentToken,ConcurrentLockout}SingleWinner`,
  `ContactChanges/ConcurrentConsumeSingleWinner`, `AuthenticationGrants/ConcurrentConsumeSingleWinner`,
  `CredentialMutations/ConcurrentApplySingleWinner`, `DeliveryJobs/ConcurrentClaimSingleWinner`.
- **turso full suite (live libsql):**
  `cd features/authentication/stores/turso && TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN='local-dev' go test -tags=integration ./...`
  — **FAIL** (170 leaf pass, **2 leaf fail**). The two failures are both genuine read-then-write CAS
  concurrency rails: `CredentialMutations/ConcurrentApplySingleWinner` (`Apply`, storetest.go:3150) and
  `UserIdentifiers/ConcurrentClaimArbitration` (`ApplyVerifiedChange`, storetest.go:3904). All other
  single-statement-atomic concurrency rails passed live on turso:
  `Challenges/{ConcurrentCode,ConcurrentToken,ConcurrentLockout}SingleWinner`,
  `ContactChanges/ConcurrentConsumeSingleWinner`, `AuthenticationGrants/ConcurrentConsumeSingleWinner`,
  `DeliveryJobs/ConcurrentClaimSingleWinner`.
- **turso CAS-case reproduction:**
  `… go test -tags=integration -v -count=10 -run 'TestConformance_Turso/.*/(ConcurrentApplySingleWinner|ConcurrentClaimArbitration)' ./...`
  — **FAIL, deterministic**: 20 RUN, 20 FAIL, 20 `database is locked` lines (10 × each case). Every failure
  is the storetest `default:` branch (`unexpected Apply error` / `unexpected ApplyVerifiedChange error`),
  i.e. the loser received a raw non-nil infra error. **No `winners=2`/`winners=0` message ever appeared** —
  single-winner data integrity is preserved; the defect is the loser's error *type* (SQLITE_BUSY instead
  of `sdk.ErrConflict`), not a false win or double-commit.
- **turso race on the passing atomic rails:**
  `… go test -tags=integration -race -count=10 -run 'TestConformance_Turso/.*/(ConcurrentCodeSingleWinner|ConcurrentTokenSingleWinner|ConcurrentLockoutSingleWinner|ConcurrentConsumeSingleWinner|ConcurrentClaimSingleWinner)' ./...`
  — **PASS** (exit 0; 6 concurrency leaves × 10 = 60 executions under `-race`; 0 data races, 0 failures,
  0 `database is locked`; ~18s).

Row-inspection evidence (throwaway store-path inspection test, run then deleted; identical for both
dialects):

- **`challenges` columns:** `id, user_id, purpose, secret_digest, protector_key_id, context,
  attempt_count, expires_at, created_at, version`. **No plaintext-code column exists** — the only secret
  is `secret_digest`, and a store-written row carried the 64-char hex digest verbatim
  (`3a1f9c2b…aabbccdd`, len 64), never the plaintext code. `context` holds the opaque JSON binding blob,
  not a secret.
- **`delivery_jobs` columns:** `id, kind, purpose, idempotency_key, payload, state, attempt_count,
  available_at, lease_id, leased_until, last_error, created_at, updated_at, terminal_at`. **No
  destination/recipient/message/rendered column exists** — the sole payload home is `payload`
  (pgx `BYTEA` / turso `BLOB`), which a store-written row held as opaque bytes (`de ad be ef 00 01 02 03
  ff fe`, len 10). `idempotency_key` is a PII-free key. The plaintext destination the store's caller
  supplies lives only inside the (encrypted) `payload` envelope; the domain `deliveryjob.Job` type has no
  plaintext destination field, so the store *structurally cannot* persist one.

**Finding 1 (BLOCKER — turso connector transaction mode):** `integrations/datastores/turso/tx.go:19`
opens transactions with `d.db.BeginTx(ctx, nil)` — a DEFERRED SQLite/libSQL transaction with no
`busy_timeout` and no statement retry. The two v3 rails that do a genuine read-then-write CAS —
`CredentialMutationStore.Apply` and `IdentifierStore.ApplyVerifiedChange` (both `SELECT auth_revision …`
then `UPDATE` inside one `InTx`) — start as readers, both read the same `auth_revision`, then both attempt
the write-lock upgrade; libSQL grants the write to one and returns `SQLite error: database is locked`
(SQLITE_BUSY) to the loser *immediately* (no busy wait) rather than serializing it into a stale-revision
read. The loser's transaction aborts and the store returns that raw infra error, whereas the port contract
(proven by reference/authmem/pgx) requires the loser to observe `sdk.ErrConflict`. This is 100%
reproducible (20/20). It is a **connector transaction-mode defect, not store logic** — the store code is
identical in shape to pgx, which passes because Postgres `SELECT … FOR UPDATE` blocks-then-serializes. The
single-statement-atomic turso rails (grant `Consume`, delivery `Claim`, challenge `ConsumeToken`
conditional `UPDATE/DELETE … RETURNING`, and fail-closed `ConsumeCode`) are unaffected because they never
hold a read lock across a separate write. Fix belongs in `integrations/datastores/turso` (e.g. a
write-intent/`BEGIN IMMEDIATE` transaction mode or a `busy_timeout`/SQLITE_BUSY statement retry for
read-then-write `InTx`) and requires a port/connector design decision — out of scope for this
verification task and for the auth store module. Important: single-winner *data integrity is not
violated* (no double-commit, no false win); the visible symptom is the wrong error kind reaching the
loser, which nonetheless fails live conformance and blocks the turso live-evidence record.

**Finding 2 (pre-existing, non-blocking for v3 rails — shared pgx pagination collation):** the
`en_US.utf8` failures above trace to `integrations/datastores/pgxdb/listquery.go` `AddOrderByClause`,
whose id/subject/resource tiebreak (`, "id" DESC`) sorts under the database's default collation. On an
`en_US.utf8` database that is linguistic (case-insensitive-ish) ordering, so the id tiebreak on a
`created_at` collision diverges from SQLite/libSQL `BINARY` byte order (e.g. `'SK54' < 's4HS'` is false
under `en_US.utf8`, true under `C`), breaking the storetest's byte-order tiebreak assertions. A
`C`-collation database conforms (full suite green). This is pre-existing (the failing tests and the
pagination helper are in `HEAD`, untouched by the v3 worktree), affects only pre-existing auth-v2
paginated tables (not any v3 rail), and lives in a shared integration used by every pgx-backed feature —
a cross-cutting fix (e.g. `ORDER BY … COLLATE "C"` on the pk tiebreak, or documenting a `C`-collation
requirement for byte-order parity) is out of AV3-2.4's scope and is flagged here for a separate decision.

Premise adaptations:

- **Local libsql-server container instead of a remote Turso playground DB** (orchestrator-approved): no
  Turso credentials are available in this environment. `ghcr.io/tursodatabase/libsql-server:latest`
  (sqld 0.24.33) at `http://127.0.0.1:8080` with an ignored token is the authorized disposable test
  target for this run. The `http://` scheme was accepted by the connector directly; no `libsql://…?tls=0`
  or `ws://` fallback was needed.
- **Byte-order `C`-collation Postgres database for the clean full-suite record.** The container's default
  database is `en_US.utf8` (the documented `docker run … postgres:17` harness collation); the storetest's
  pagination tiebreak asserts byte order (matching SQLite/libSQL `BINARY`). A `C`-collation database
  (`authv3_cconf`) in the *same authorized container* is the environment whose text ordering matches the
  cross-dialect contract, and is where the pgx full suite is recorded green (157/157). The `en_US.utf8`
  run is also recorded to document Finding 2. This is an environment adaptation only — no test or store
  code was changed.

For AV3-2.5 (host upgrade runbook draft): the pgx and turso v3 schemas are live-verified byte-for-byte in
column shape (identical `challenges`/`delivery_jobs` column sets recorded above), which the runbook's
"create new tables/indexes" step can rely on. Two live-evidence caveats the runbook and later phases must
carry: (1) **turso read-then-write CAS (`credential Apply`, identifier `ApplyVerifiedChange`) is not yet
live-conformant** pending the Finding-1 connector fix — any turso host relying on step-up credential/
identifier mutations under concurrency is blocked until then; (2) **pgx byte-order tiebreak parity
requires a `C`-collation database** (or a Finding-2 `COLLATE "C"` pagination fix) — an `en_US.utf8`
Postgres host will order same-`created_at` list pages differently from SQLite. Neither caveat affects the
migration DDL itself; both are runtime/parity behaviors the runbook should note.

### AV3-2.4 follow-up — Finding-1 turso connector fix and completed live evidence (2026-07-12)

Task: unblock AV3-2.4 by fixing the Finding-1 turso connector transaction-mode defect, then complete the
turso live-conformance record that the BLOCKED entry above was missing.

**Root-cause confirmation (live probe against `authv3-libsql`, sqld 0.24.33).** A throwaway probe
(written, run, deleted) ran an 8-way concurrent read/UPDATE/COMMIT CAS on a scratch table, comparing the
driver's default `BEGIN` (DEFERRED) against `BEGIN IMMEDIATE`:

- **DEFERRED:** losers failed at the `UPDATE` stage with raw `SQLite error: database is locked` — the exact
  Finding-1 symptom.
- **BEGIN IMMEDIATE:** all 8 transactions serialized cleanly, zero locked errors — sqld queues the
  write-intent acquisition at `BEGIN IMMEDIATE` and serves each transaction in turn, so each reads the
  prior winner's committed state. This is the libSQL analogue of Postgres `SELECT … FOR UPDATE`
  block-then-serialize, which is why the pgx store never exhibited the defect.
- `PRAGMA busy_timeout` is **rejected by sqld** (`unsupported statement`), so a busy-timeout approach is not
  viable on this server; write-intent transactions are the correct fix.

**Fix (surgical, connector-internal — no API change).** `integrations/datastores/turso/tx.go`:
`DB.Begin` now pins a dedicated `*sql.Conn` (`db.Conn(ctx)`) and issues `BEGIN IMMEDIATE` explicitly
instead of `db.BeginTx(ctx, nil)`. Rationale for the pinned-conn approach: the libsql driver hardcodes
plain `BEGIN` in its `BeginTx` and rejects any non-default `sql.TxOptions.Isolation`, so `BEGIN IMMEDIATE`
cannot be requested through `database/sql`'s `BeginTx` — it must be driven as an explicit statement over a
single pinned connection. `Tx` now holds `*sql.Conn`; `Commit`/`Rollback` run `COMMIT`/`ROLLBACK` and
release the conn; `Exec`/`Query`/`QueryRow` run on the pinned conn. Public method signatures and the
`Querier` interface are unchanged, so the four turso store modules (auth, cms, jobs, events) are untouched.
Every `InTx` is now write-intent; on SQLite/libSQL (single-writer) this is the recommended mode and, for
read-only `InTx` uses like `Snapshot`, only serializes reads against writes (no correctness effect). The
loser of a CAS now reads the winner's committed `auth_revision` and the store's own
`current != expected → sdk.ErrConflict` check fires — the port contract is satisfied by real
serialization, not faked atomicity, and no retry can mask a genuine conflict because the CAS re-evaluates
against committed state.

Files changed:

- `integrations/datastores/turso/tx.go` — `Begin` uses a pinned `*sql.Conn` + `BEGIN IMMEDIATE`; `Tx`
  wraps `*sql.Conn`; `Commit`/`Rollback` close the conn; `Exec`/`Query`/`QueryRow` route through it.

Commands / results:

- **Hermetic — connector:** `cd integrations/datastores/turso && go build ./... && go vet ./... &&
  go test ./...` — **PASS**.
- **Hermetic — four turso stores:** `go build ./... && go vet ./... && go test ./...` in each of
  `features/{authentication,cms,events,jobs}/stores/turso` — **PASS** (all four).
- **`make check`** — **PASS** (`all checks passed`; per-module vet/build/test + integration-tag compile
  vet for every turso store + all guards green).
- **turso live — previously failing CAS cases (10×):**
  `cd features/authentication/stores/turso && TURSO_DATABASE_URL='http://127.0.0.1:8080'
  TURSO_AUTH_TOKEN='local-dev' go test -tags=integration -count=10 -run
  'TestConformance_Turso/.*/(ConcurrentApplySingleWinner|ConcurrentClaimArbitration)' ./...` — **PASS**
  (both `CredentialMutations/ConcurrentApplySingleWinner` and `UserIdentifiers/ConcurrentClaimArbitration`
  now pass; losers observe `sdk.ErrConflict`, never raw locked errors).
- **turso live — race evidence over all concurrency cases (`-race -count=10`):**
  `… go test -tags=integration -race -count=10 -run 'TestConformance_Turso/.*/Concurrent.*' ./...` —
  **PASS** (0 data races, 0 failures, 0 locked errors; ~21s). Covers the two previously failing CAS rails
  plus every single-statement-atomic rail.
- **turso live — full auth suite:**
  `… go test -tags=integration ./...` — **PASS** (previously 2 leaf fail; now clean). This is the
  completed turso live-conformance record the BLOCKED entry lacked.
- **turso live — no cross-module regression:** full `-tags=integration` suites for
  `features/{cms,events,jobs}/stores/turso` against the same libsql container — **PASS** (all three).
- **pgx cross-dialect sanity (no regression):**
  `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='…/authv3_cconf…' go test ./...` — **PASS**
  (7.5s) on the pre-existing `C`-collation DB.

**Findings 2 (pgx pagination collation) and the pgx pagination-collation issue in
`integrations/datastores/pgxdb/listquery.go` are untouched** — explicitly parked for a separate decision.

Result: **Finding 1 resolved.** AV3-2.4's turso live-evidence gap is closed; single-winner integrity was
never at risk and is preserved, and the loser's error contract (`sdk.ErrConflict`) is now honored on
turso. AV3-2.4 is checked off in `TASKS.md`.

### 2026-07-12 — AV3-2.5 (host upgrade runbook draft) — phase-2 close

Dependencies: AV3-2.1 complete and checked off (the transitional canonical migrations this runbook
mirrors); AV3-2.2/2.3/2.4 (incl. the AV3-2.4 Finding-1 turso-connector follow-up) all complete and
checked off with execution-log entries; phases 0–1 closed. Worktree changes preserved (no resets); the
pre-existing auth-v2/JWT work and all AV3-2.1–2.4 code are intact. Scope held to a single DRAFT doc under
this plan directory (final publication is AV3-9.2) — no store/connector/migration code changed by this
task.

Files changed:

- `.claude/plans/authv3/host-upgrade-runbook-draft.md` (new) — the host-owned v2→v3 upgrade runbook draft.
  Per the standing greenfield-migrations rule (2026-07-12) this is a HOST-side document: the canonical
  trees ship only the final schema and never an upgrade file, so a deployed v2 host owns its own evolution
  and applies these steps from its own host migration tree. Placed under the plan directory per the task's
  "draft under this plan directory or the docs location designated by the current release convention"
  (RELEASING.md's upgrade-note home is the AV3-9.2 publication target, not the draft's). Structured to the
  task's seven steps for both pgx and SQLite/libSQL: (1) backup + collision dry-run with abort-not-choose;
  (2) create `user_identifiers` + its three partial indexes; (3) idempotent backfill of one active primary
  email identifier per user (login/recovery/notification uses, verified-state preserved); (4) count-parity
  + missing-primary + duplicate-auth-claim + orphan password/oauth/session validation; (5) additive
  `users.auth_revision`, session metadata columns, and the four flow tables; (6) LATER destructive cutover
  — pgx `DROP COLUMN` + verification-table drop, SQLite 12-step table rebuild — gated on a stable v3 deploy;
  (7) forward-only recovery + explicit no-blind-copy / no-auto-resolve-collision warnings. Includes a
  single-cutover deploy-ordering note (no rolling deploy across the metadata add) and the two AV3-2.4
  runtime caveats.

Commands / results (SQL tested on disposable databases inside the AV3-2.4 authorized playground
containers; every test database dropped after the run — nothing left behind, and the long-lived
conformance DBs were never touched):

- **pgx clean path** (`postgres:17`, throwaway DB `av3_runbook_test`) — v2-shape seed with
  verified+password, unverified+password, OAuth-only (no password row), and password-only fixtures, then
  the runbook Steps 1–5: collision dry-run **0 rows**; Step-3 backfill `INSERT 0 4`; Step-4 **parity 4/4**,
  0 users missing a primary email, 0 duplicate auth-claims, 0 orphan passwords/oauth/sessions; verified
  state preserved (unverified → `verified_at NULL`). Step-3 **re-run `INSERT 0 0`** (idempotent). Step-6
  `DROP COLUMN email/email_verified` + drop verification tables — clean; `users` left at
  `id, display_name, created_at, updated_at, auth_revision`. **PASS**.
- **pgx collision path** (throwaway DB `av3_runbook_collide`) — added an un-normalized duplicate
  (`Verified@Example.com` vs existing `verified@example.com`): Step-1 dry-run **returns the colliding
  `verified@example.com` with both user ids** (abort signal fires); forcing the Step-3 backfill anyway
  **fails on `idx_user_identifiers_auth_claim`** (`duplicate key value violates unique constraint`) —
  proving the structural backstop. **PASS (expected failure observed).**
- **SQLite/libSQL clean path** (`sqlite3 3.43.2`, throwaway file) — same four fixtures, Steps 1–5 identical
  outcomes (dry-run 0 rows, parity 4/4, 0 orphans, verified state preserved); Step-6 **12-step table
  rebuild** leaves `users` at the final v3 shape (`id, display_name, auth_revision, created_at, updated_at`)
  with identifiers intact and verification tables dropped. **PASS**.
- **SQLite/libSQL collision path** (throwaway file) — dry-run detects the duplicate; forced backfill
  **fails on the auth-claim unique index** (`UNIQUE constraint failed`). **PASS (expected failure
  observed).**

Phase-2 close gate:

- `make check` — **PASS** (`all checks passed`: templ drift clean; per-module build/vet/test across every
  module incl. the reference + authmem hermetic conformance for the v3 rails; integration-tag compile-only
  vet incl. both auth stores; every guard green).
- `make guard` — **PASS** (all 13 guards green, run standalone).
- **Live-store evidence (phase requirement):** already recorded — the fresh/reset **pgx** (157/157
  C-collation full suite + 80 race executions) and **turso** (full suite clean + `-race -count=10`
  all-`Concurrent.*` clean after the Finding-1 connector fix) conformance runs are in the AV3-2.4 and
  AV3-2.4-follow-up entries above; `make check`'s hermetic skip is not the milestone proof and is not
  claimed as such here. Migration filename sets are identical (0001–0016, both dialects; asserted by
  `TestMigrationParity` in AV3-2.1).

Premise adaptations:

- **`verified_at` backfill proxy.** §2.5/§2.1 ask for "original verification timestamp/state," but v2
  persisted only a boolean `users.email_verified`, never a proof time. The runbook preserves verification
  **state** exactly (verified → a non-NULL `verified_at`, unverified → NULL) and uses `users.updated_at` as
  the best-available verification **timestamp** proxy, documented as a caveat with a host-substitution hook.
  This is a stale-premise adaptation (the v2 schema simply lacks the field), not a weakening of any
  invariant — the load-bearing signal is verified-vs-unverified, which is exact.
- **Draft placement under the plan directory.** The task allows "this plan directory or the docs location
  designated by the current release convention." RELEASING.md's convention is that published upgrade notes
  live in RELEASING.md + the feature README, but that is the **publication** target owned by AV3-9.2; the
  in-flight **draft** belongs beside the plan, so it is filed here and AV3-9.2 will lift/validate it into
  the release docs.
- **Local libsql-server not re-exercised for this task.** The runbook's SQLite/libSQL DDL was validated
  with local `sqlite3` (the libSQL SQL dialect for CREATE/ALTER/rebuild is identical to SQLite's, and the
  connector-level live behavior is already recorded under AV3-2.4). No new turso live run was needed for a
  DDL-only, backfill-SQL draft.

For phase 3 (AV3-3.1, challenge issue/consume/redeem service in `04-challenges-and-recovery.md`) and
phase 4 (AV3-4.1, delivery renderer/router in `05-delivery.md`) — the overview allows phases 3 and 4 in
either order after phase 2, which is now **closed**: (1) both dialect stores + memory/reference/authmem
contracts for `challenges`, `contact_changes`, `authentication_grants`, `credential_mutations`, and
`delivery_jobs` are complete and live-conformant, so the phase-3 challenge service and phase-4 delivery
service build directly on frozen, race-proven ports — no store work remains in those phases. (2) The turso
step-up CAS rails (`Apply`/`ApplyVerifiedChange`) are only live-correct on the connector carrying the
AV3-2.4 Finding-1 `BEGIN IMMEDIATE` write-intent fix (`integrations/datastores/turso/tx.go`); any later
phase adding a new read-then-write CAS on turso relies on that same connector mode. (3) `delivery_jobs`
has NO plaintext destination/message/identifier column — the phase-4 renderer/router must seal the whole
envelope through the `DeliveryEncrypter` into the single `payload` BLOB/BYTEA before enqueue; the store
structurally cannot persist plaintext. (4) The runbook is a DRAFT pending AV3-9.2 publication and a host
application; do not apply it to `examples/auth-cms` or segovia yet (segovia carry is an out-of-scope
follow-on per §11). (5) pgx byte-order pagination parity still requires a `C`-collation DB (Finding 2,
parked) — unrelated to challenge/delivery rails but relevant to any new paginated list added later.
