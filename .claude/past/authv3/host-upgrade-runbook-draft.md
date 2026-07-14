# Auth v3 host upgrade runbook — DRAFT (AV3-2.5)

Status: **PUBLISHED (AV3-9.2, 2026-07-13).** The final runbook now lives in
`RELEASING.md` ("Auth v3 host upgrade runbook (v2 → v3 identity)") with a mirrored
pointer in `features/authentication/README.md`. It was re-executed against
fresh/reset databases both dialects (the AV3-9.2 execution record in
`RELEASING.md`); this file is retained as the drafting history. It has **not**
been applied to any application host.

## What this is and is not

This is a **host-owned** migration procedure for a database already running
auth-v2 (`features/authentication` before the v3 identity work) that is upgrading
to auth-v3. Per the standing greenfield-migrations rule (2026-07-12): the
canonical migration trees ship the **final** v3 schema only and never carry
upgrade/evolution files. A v2 host's database does **not** match the canonical
`0001_users.sql` (which no longer reshapes users in-place); the host owns its own
schema evolution and applies the steps below from its **own** host migration tree,
pre-boot, exactly like every other host-owned migration.

**No blind copy.** Do not apply the canonical `stores/{pgx,turso}/migrations/*`
files to a live v2 database. As of AV3-5.5 the canonical `0001_users.sql` describes
the **final** users shape — `id, display_name, auth_revision, created_at,
updated_at`, with no `email`/`email_verified` column — so applying it to a v2
database that already holds a populated `users` table would drop the legacy email
data before any backfill. A v2 host must run *this* additive, backfill-first
procedure instead; the destructive column removal happens only in Step 6, after the
backfill and its validation.

## Preconditions

- A confirmed, restorable **backup** taken immediately before Step 1 (see Step 1).
- A maintenance window. See the deploy-ordering note below — the v3 binary must
  not run against the pre-Step-5 schema, and old/new binaries must not both serve
  the same database across the cutover.
- v2 `users.email` is stored normalized (trimmed + lowercased) and is `UNIQUE`.
  If a host wrote un-normalized emails, the Step-1 collision dry-run catches the
  ambiguity **before** any write.

### Deploy ordering (single cutover — do NOT roll)

1. Take the backup (Step 1).
2. Stop the v2 binary (or drain traffic).
3. Apply Steps 1–5 (additive; the v2 binary would still run against this schema,
   but keep it stopped to avoid mixed-version reads/writes).
4. Deploy and start the v3 binary; confirm it is healthy and stable.
5. **Only after** v3 is confirmed stable, apply Step 6 (the destructive cutover).
   Step 6 is the point of no return for a v2-binary rollback.

Steps 1–5 are additive and reversible by restoring the backup or redeploying the
v2 binary (the new tables/columns are inert to it). Step 6 drops the legacy
`users.email`/`email_verified` columns and the verification tables; after Step 6
the v2 binary can no longer read `users` and a rollback requires a restore.

---

## Step 1 — Backup and dry-run collision detection

Take a full, restorable backup first (`pg_dump` / a libSQL/SQLite file copy or
`.backup`). Then run the collision dry-run. **A non-empty result aborts the
upgrade** — do not choose a winner automatically; report the colliding rows for a
human decision (this mirrors the feature's atomic auth-claim invariant, which is
`UNIQUE(kind, normalized_value)` over active login/recovery identifiers).

### pgx

```sql
SELECT lower(btrim(email)) AS normalized_value,
       count(*)            AS n,
       array_agg(id)       AS user_ids
FROM users
GROUP BY lower(btrim(email))
HAVING count(*) > 1;
```

### SQLite / libSQL

```sql
SELECT lower(trim(email)) AS normalized_value, count(*) AS n
FROM users
GROUP BY lower(trim(email))
HAVING count(*) > 1;
```

If either returns rows, **stop** and resolve the collisions by hand. (Validated:
skipping this and running the Step-3 backfill anyway fails hard on the
`idx_user_identifiers_auth_claim` unique index — the index is the structural
backstop, but detecting collisions up front lets a human choose, not the DB.)

---

## Step 2 — Create `user_identifiers` and its indexes

This table is required by the Step-3 backfill. It is purely additive.

### pgx

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

### SQLite / libSQL

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

---

## Step 3 — Backfill one primary email identifier per user

Insert exactly one active, primary email identifier per existing user. The
`NOT EXISTS` guard makes the statement **idempotent** — a re-run inserts nothing.

Uses backfilled: `login_enabled`, `recovery_enabled`, and `notification_enabled`
are all set — in v2 the single `users.email` was the universal login, recovery,
and notification address, and passwordless/recovery flows key off these uses.
(OAuth-only users get a login-enabled email too: they have no password, but their
email is still their discovery/recovery address and a passwordless-login claim
identifier.)

**`verified_at` proxy caveat.** v2 recorded only a boolean `email_verified`, not a
proof timestamp. A verified user's identifier is backfilled with `updated_at` as
the best-available verification time; an unverified user gets `NULL` (the
unverified sentinel). This preserves verification **state** exactly; the
verification **timestamp** is an approximation, which is acceptable for
lifecycle/risk policy. A host that kept a truer verification time elsewhere may
substitute it in the `CASE` expression.

### pgx

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

### SQLite / libSQL

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

---

## Step 4 — Validate before proceeding

Every check must pass before Step 5. The count-parity check must be equal; every
other query must return **zero** rows. (Column/table names below are the v2 auth
schema; adapt if a host renamed anything.)

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

On SQLite/libSQL use `is_primary = 1`, `(login_enabled = 1 OR recovery_enabled = 1)`
in the predicates. Sessions carry no identifier binding in v2, so no session is
invalidated by the backfill (identifier row IDs are newly generated and nothing is
bound to them before v3).

---

## Step 5 — Add auth/session metadata and the new flow tables

Additive. `users.auth_revision` is the optimistic-serialization anchor; the session
metadata columns back the recent-primary-login shortcut; the four flow tables need
no backfill (they start empty).

### pgx

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

### SQLite / libSQL

SQLite/libSQL `ALTER TABLE ADD COLUMN` has no `IF NOT EXISTS`; run each `ADD COLUMN`
once (guard in the host migration runner, which already tracks applied files).

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

---

## Step 6 — LATER: cutover / drop (only after v3 is stable)

Run this only after the v3 binary has been confirmed healthy in production **and**
the recovery flows that replaced the verification rail are verified end to end:
registration email verification and forgot/reset password both complete on the v3
challenge rail (the `challenges` / `delivery_jobs` tables), and the Step-4 backfill
parity checks still hold. The legacy `verification_codes` / `verification_tokens`
tables are inert to the v3 binary (AV3-3.5 removed the rail); drop them only once
that cutover and flow verification succeed. This step is the point of no return for
a v2-binary rollback.

### pgx

`ALTER TABLE ... DROP COLUMN` is a metadata operation on Postgres.

```sql
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users DROP COLUMN email_verified;
DROP TABLE verification_codes;
DROP TABLE verification_tokens;
```

### SQLite / libSQL — table rebuild

Dropping columns on SQLite/libSQL is done with the standard 12-step table rebuild
(more portable than relying on a specific `DROP COLUMN`-capable engine version).
Wrap it in one transaction with foreign keys off.

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

After Step 6 the host's `users` table matches the final canonical v3 shape that
AV3-5.5 froze in the canonical set (`id, display_name, auth_revision, created_at,
updated_at`) — the same legacy-column removal, reached additively instead of by a
blind canonical copy.

---

## Step 7 — Forward-only recovery and the no-blind-copy warning

- **Forward-only.** There is no down-migration that deletes backfilled identifier
  rows. If Steps 1–5 must be abandoned, restore the Step-1 backup or redeploy the
  v2 binary (Steps 1–5 are inert to it). If Step 6 has run, recovery requires a
  restore from the Step-1 backup — the v2 binary cannot read the rebuilt `users`.
- **Never blind-copy a canonical migration** onto a live v2 database. The canonical
  trees are greenfield/final-shape; this additive, backfill-first, validated
  procedure is the only supported path from a v2 database.
- **Never auto-resolve a collision.** A non-empty Step-1 dry-run stops the upgrade
  for a human decision.

---

## Runtime caveats carried from live conformance (AV3-2.4)

These do not affect the migration DDL; they are runtime/parity behaviors a host
operator must know.

1. **turso hosts must run the connector with the write-intent transaction fix.**
   The step-up credential/identifier CAS rails (`Apply`, `ApplyVerifiedChange`)
   require the turso connector's `BEGIN IMMEDIATE` write-intent transactions
   (`integrations/datastores/turso/tx.go`, AV3-2.4 follow-up). An older connector
   using default `DEFERRED` transactions returns `SQLITE_BUSY` to the CAS loser
   instead of `sdk.ErrConflict` under concurrency. Data integrity is never at risk
   either way (no double-commit), but a host on the pre-fix connector fails the
   concurrent step-up contract. Verified live: 10× race-clean with the fix.
2. **pgx byte-order pagination parity requires a `C`-collation database.** An
   `en_US.utf8` Postgres host pages same-`created_at` lists in linguistic order,
   which diverges from SQLite/libSQL `BINARY` byte order on the id/subject/resource
   tiebreak. This is a pre-existing, parked finding in the shared
   `integrations/datastores/pgxdb` pagination helper — **not** fixed by this
   runbook. A host that needs cross-dialect byte-order pagination parity should run
   Postgres with `LC_COLLATE 'C'` (or await a future `COLLATE "C"` pagination fix).
   It does not affect any v3 rail or the migration itself.

The pgx and turso `challenges` and `delivery_jobs` column sets are live-verified
byte-for-byte identical (AV3-2.4), so the Step-2/Step-5 DDL above holds for both
dialects.

---

## Validation record (this draft)

Validated on disposable databases inside the AV3-2.4 authorized playground
containers (`authv3-pg` = `postgres:17`, throwaway DBs `av3_runbook_test` /
`av3_runbook_collide`; SQLite 3.43.2 on throwaway files). All databases were
dropped after the run. Fixtures exercised: **verified+password**,
**unverified+password**, **OAuth-only** (no password row), **password-only**, and
an un-normalized **duplicate-collision** pair.

- **pgx clean path** — Steps 1–5: collision dry-run 0 rows; backfill `INSERT 0 4`;
  parity 4/4; zero users missing a primary email; zero duplicate auth-claims; zero
  orphan passwords/oauth/sessions; verified state preserved (unverified →
  `verified_at NULL`). Step-3 re-run `INSERT 0 0` (idempotent). Step-6 column drop +
  verification-table drop clean.
- **pgx collision path** — dry-run returns the colliding normalized value with both
  user ids; skipping the dry-run and forcing the backfill fails on
  `idx_user_identifiers_auth_claim` (the structural backstop).
- **SQLite/libSQL clean path** — Steps 1–5 identical outcomes (parity 4/4, no
  orphans, state preserved); Step-6 table rebuild leaves `users` at the final v3
  shape (`id, display_name, auth_revision, created_at, updated_at`), identifiers
  intact.
- **SQLite/libSQL collision path** — dry-run detects the duplicate; forced backfill
  fails on the auth-claim unique index.
