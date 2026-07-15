# Authorization v1 → v3 host upgrade runbook — EXECUTED / VALIDATED

Status: **EXECUTED & VALIDATED 2026-07-14** (authorizationv3, AZ3-5.1; drafted at
AZ3-2.6). This is the operational protocol a host runs to move a live v1
authorization database to v3. It **wraps** [`CONVERSION.md`](CONVERSION.md) — the
detection-and-repair queries — with the full deployment sequence (backup, window,
binary stop, audit, repair, migration apply, revision seeding, v3 boot, rollback
boundary) and with the **access-change assessment** an adopter runs before
deploying: *would this v1 data gain, lose, or retain access under v3?*

AZ3-5.1 executed this runbook end to end against a populated v1 fixture on live
PostgreSQL (C-collation scratch database) and libSQL/SQLite, and BOOTED a
v3-composed `authorization.Service` over the converted PostgreSQL store to verify
the gain/lose/retain verdicts hold. The executed evidence — commands, detection
outputs, repair statements, post-boot access comparison, and rollback
demonstration — is recorded in [Executed evidence](#executed-evidence-az3-51-2026-07-14)
below. The post-conversion boot + access comparison is also a repeatable,
env-gated live test: `features/authorization/stores/pgx` `TestUpgradeRunbook`.

**Correction landed during execution (§7).** The canonical `0001`/`0002` files use
`CREATE TABLE IF NOT EXISTS`, which is a **no-op against a pre-existing v1 table**
and therefore does **not** add the v3 CHECK constraints to it. The data-preserving
path adds them explicitly — `ALTER TABLE … ADD CONSTRAINT` on PostgreSQL, a
table-rebuild on libSQL/SQLite (which has no `ALTER … ADD CONSTRAINT`) — and that
explicit step is the **enforced block**: it fails while any violating row remains.
See §7. The canonical greenfield migration files are unchanged; only this runbook's
data-preserving procedure was corrected.

Two disjoint paths follow, and a host takes exactly one:

- **Data-preserving adopter path** (§1–§8) — a production host with real v1
  tuples it must not lose.
- **Destructive reset path** (§9) — an example or dev host with disposable data.

---

## 1. Before you start — is this an upgrade at all?

Re-confirm the pre-tag posture. The canonical `0001`–`0004` migrations are the
v3 **greenfield** schema, folded clean because no `features/authorization` (or
store) tag exists. An adopter on live v1 tables therefore **converts data with
this runbook**, not by applying shipped `ALTER` files — none ship. If a relevant
tag now exists, stop: the migration strategy switches to append-only and this
draft no longer applies.

```sh
git -C <this repo> tag --list 'features/authorization*'   # expect: empty
```

## 2. The access-change assessment (run this first)

**Acceptance goal:** before touching the database, an adopter learns whether
each stored shape would **gain**, **lose**, or **retain** access under v3. This
is a read-only analysis over the current v1 tables; it drives every repair
decision in §6.

The single semantic change that moves access is that **the userset relation
became load-bearing** (AZ3-1.1). v1 expansion hard-coded `relation = 'member'`
and ignored the stored `subject_relation`; v3 evaluates the exact stored
relation. Enumerate the distinct stored shapes (CONVERSION.md query **1c**), then
classify each row against this table:

| stored shape | v1 evaluated as | v3 evaluates as | verdict |
|---|---|---|---|
| concrete principal subject, `subject_relation = ''` (e.g. `user:u1`) | direct grant | direct grant | **RETAIN** — unchanged |
| userset `subject_relation = 'member'` (e.g. `group:eng#member`) | the group's members (hard-coded) | the group's members (exact) | **RETAIN** — meaning preserved |
| userset `subject_relation = 'admin'` or any non-`member` value | **all** members (bug — relation ignored) | exactly that relation's holders | **LOSE** for every member who is not an `admin`; the grant narrows to its true relation |
| `group:eng` with `subject_relation = ''` where the schema expects members to reach *through* the group | the group's members (hard-coded member expansion) | a **concrete** reference to the group object; does **not** reach members | **LOSE** for the members — unless the row is an operator repair to `#member` |
| any structurally malformed row (empty required column, half-populated role scope) | served by a permissive v1 | rejected by a v3 CHECK constraint | **BLOCKS** the upgrade until repaired (§6) |

Two things follow, and both are honest limits, not omissions:

- **No v1 row automatically gains access under v3.** v3 can only narrow or
  preserve what v1's over-broad `member` collapse granted. A *gain* only ever
  results from a deliberate operator repair (widening an ambiguous row to a
  broader relation in §6) or from reconciling a grant that v1 silently dropped
  (CONVERSION.md **1e** — the one-relation-per-subject index dropped conflicting
  second writes; that lost intent is not in the table and is recovered only from
  the host's own source of truth).
- **An ambiguous row is never auto-classified.** A `group` subject with an empty
  relation where the schema requires a userset (CONVERSION.md **1c**) cannot be
  placed in *retain* or *lose* by the data alone — its verdict is whatever
  relation the operator sets in §6. **The runbook never guesses `member`**; that
  guess is exactly the v1 defect v3 removes.

Record the per-shape verdicts. They are the input to §6 and the sign-off
artifact for the change window.

## 3. Backup

Take a full, restorable backup of the authorization data **before** the
maintenance window. This backup — not a schema downgrade — is the rollback
mechanism (§8).

```sh
# PostgreSQL (adjust to your host's DSN / tooling)
pg_dump --format=custom --table=iam_relationships --table=iam_roles \
  "$AUTHZ_DSN" > authz_v1_backup.dump

# libSQL / SQLite: snapshot the database file or run a `.dump` of the two tables
sqlite3 authz.db ".dump iam_relationships iam_roles" > authz_v1_backup.sql
```

The v3 tables `iam_scopes` and `iam_mutations` do not exist yet on a v1 host, so
they are not in the backup — they are created in §7 and seeded in §7 (scopes)
and left empty (mutations).

## 4. Maintenance window and stopping the old binary

Open a maintenance window and **stop every v1 binary that reads or writes the
authorization tables** before proceeding. This is a hard single cutover, not a
rolling or blue/green migration — see §5 for why mixed serving is forbidden.
Confirm zero v1 processes remain (no lingering workers, cron, or admin tools)
before §6.

## 5. No mixed old/new serving while semantics differ

A v1 binary and a v3 binary **must never serve the same authorization tables at
the same time.** The two disagree on the *meaning* of the same rows:

- v1 grants every member of `group:eng` access through a `group:eng#admin` row;
  v3 grants only the admins. A request answered by whichever binary happened to
  handle it would be non-deterministic.
- The v3 write path advances `iam_scopes` revision anchors and mints
  `iam_mutations` receipts under lock; a v1 write bumps no anchor and mints no
  receipt. A v3 guarded mutation running concurrently with a v1 write would
  validate against a revision the v1 write never advanced and could commit a
  decision that a concurrent v1 change had already invalidated.

Therefore the cutover is: stop all v1 (§4) → repair (§6) → apply schema (§7) →
seed (§7) → boot v3 (§7). There is no window in which both versions serve. A
blue/green deployment must cut all traffic to v3 atomically, never split it.

## 6. Invalid-row audit and repair

Run the CONVERSION.md detection queries **read-only** and capture every result
([`CONVERSION.md` §1](CONVERSION.md)): **1a** (empty structural relationship
columns ⛔), **1b** (empty role columns / half-populated scope pair ⛔), **1c**
(distinct shapes — the concrete-where-userset-required audit), **1d**
(non-`member` usersets whose meaning changes), **1e** (silent-conflict export).

The ⛔ queries (**1a**, **1b**) block the v3 CHECK constraints; **1c**/**1d** are
the ambiguous-and-changed-meaning rows from the §2 assessment. Apply the per-row
operator decisions from §2 — repair (set the intended field / relation) or
delete (remove the tuple). This is deliberately manual: there is no automatic
transform, because every ambiguous case is a policy choice, and **no ambiguous
userset relation is ever defaulted to `member`.**

Re-run **1a** and **1b** until both return zero rows. Only then will the v3
constraints apply in §7. (Ordering matters: repair precedes both the constraint
apply and the §7 scope seeding — seeding an unrepaired empty-id row would create
an anchor that violates `ck_iam_scopes_nonempty`.)

## 7. Apply the v3 schema, then seed scope revisions

Scaffold the canonical `authorization` migration files into the host's own
migration tree and apply them with the host's pre-boot runner — the framework
never migrates at startup.

```go
// one-off, from the host module (pick the dialect the host actually runs)
authpgx.ExportMigrations("workshop/migrations/primary")     // pgx sibling
// or: authturso.ExportMigrations("workshop/migrations/primary")
```

```sh
# then apply with the host's existing runner, pre-boot
go run ./workshop/migrations        # examples/cms pattern; or `make migrate`
```

This applies the four `authorization`-source files in filename order:
`0001_iam_relationships` → `0002_iam_roles` → `0003_iam_scopes` →
`0004_iam_mutations`. On a **greenfield** host these create all four tables with
the CHECK constraints inline. On a **data-preserving** host `iam_relationships`
and `iam_roles` already exist, so `0001`/`0002` are `CREATE TABLE IF NOT EXISTS`
**no-ops** — they add nothing to the existing tables (they only re-assert the
byte-identical indexes, which already exist). `0003`/`0004` create the new
revision-anchor and receipt tables.

### 7a. Add the v3 CHECK constraints to the existing tables (the enforced block)

Because `0001`/`0002` cannot add constraints to a table that already exists, the
data-preserving path adds the three CHECK constraints on `iam_relationships` /
`iam_roles` **explicitly**, after §6 has driven **1a**/**1b** to zero. This step
is the real, enforced block: it validates every existing row and **fails while any
malformed row remains** — repair (§6) is therefore not merely advisory, it is a
hard precondition for this step to succeed.

**PostgreSQL** — `ALTER TABLE … ADD CONSTRAINT` (validates existing rows; errors
if any violate):

```sql
ALTER TABLE iam_relationships ADD CONSTRAINT ck_iam_relationships_nonempty
  CHECK (resource_type <> '' AND resource_id <> '' AND relation <> ''
         AND subject_type <> '' AND subject_id <> '');
ALTER TABLE iam_roles ADD CONSTRAINT ck_iam_roles_nonempty
  CHECK (subject_type <> '' AND subject_id <> '' AND role <> '');
ALTER TABLE iam_roles ADD CONSTRAINT ck_iam_roles_scope_pair
  CHECK ((resource_type = '') = (resource_id = ''));
```

**libSQL / SQLite** — SQLite has **no** `ALTER TABLE … ADD CONSTRAINT`, so the
constraint is added by the standard **table-rebuild** pattern: create the
constrained replacement, copy rows (the copy **fails** if any row violates the
CHECK — the same enforced block), drop the old table, rename, and recreate the
indexes. Run it inside a transaction:

```sql
BEGIN;
CREATE TABLE iam_relationships_v3 (
    relationship_id  TEXT NOT NULL PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    resource_type    TEXT NOT NULL, resource_id TEXT NOT NULL, relation TEXT NOT NULL,
    subject_type     TEXT NOT NULL, subject_id  TEXT NOT NULL, subject_relation TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    CONSTRAINT ck_iam_relationships_nonempty CHECK (
        resource_type <> '' AND resource_id <> '' AND relation <> ''
        AND subject_type <> '' AND subject_id <> ''));
INSERT INTO iam_relationships_v3 SELECT * FROM iam_relationships;   -- fails if any row is malformed
DROP TABLE iam_relationships;
ALTER TABLE iam_relationships_v3 RENAME TO iam_relationships;
CREATE UNIQUE INDEX idx_iam_relationships_unique_tuple   ON iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation);
CREATE UNIQUE INDEX idx_iam_relationships_unique_subject ON iam_relationships (resource_type, resource_id, subject_type, subject_id, subject_relation);
CREATE INDEX        idx_iam_relationships_resource       ON iam_relationships (resource_type, resource_id);
CREATE INDEX        idx_iam_relationships_subject        ON iam_relationships (subject_type, subject_id);
CREATE INDEX        idx_iam_relationships_type_relation  ON iam_relationships (resource_type, relation);
-- repeat the rebuild for iam_roles with ck_iam_roles_nonempty + ck_iam_roles_scope_pair,
-- recreating idx_iam_roles_unique / idx_iam_roles_subject / idx_iam_roles_resource.
COMMIT;
```

A host whose runner supports it may instead express 7a as its **own** appended
migration file in the `authorization` source's stream (a `0005_*` the host owns) so
the constraint add is recorded in the ledger — but it must never renumber or edit
the scaffolded `0001`–`0004`. Either way, 7a runs **after** repair and **before**
seeding (§7b).

### 7b. Seed the revision anchors

Then **seed the revision anchors** at revision 0 with the CONVERSION.md §3 SQL
(`INSERT … SELECT … ON CONFLICT DO NOTHING`, or `INSERT OR IGNORE` on
SQLite < 3.24). Seeding runs **after** repair and **after** `0003` exists. Every
seeded anchor is revision **0** — the existing rows are the baseline, not applied
v3 changes; never invent a nonzero revision.

`iam_mutations` is **not** seeded and is **not** backfilled: pre-v3 writes
carried no MutationID, so there are no receipts to reconstruct. It begins empty
and fills as v3 commands apply.

**Boot the v3 binary.** `Repositories(db)` probes for all four `iam_*` tables at
construction and fails wiring (naming the missing table) if the `authorization`
source was not fully applied — a roles-only host still applies all four. A clean
boot with the probe passing is the go-signal to reopen traffic.

## 8. Rollback boundary

The clean rollback boundary is **the first committed v3 mutation.**

- **Before any v3 mutation commits** — you have only applied additive schema
  (two new tables, CHECK constraints, seeded revision-0 anchors) and the §6
  repairs. Rollback to the v1 binary is possible, but requires restoring the §3
  backup: the §6 repairs/deletes changed rows v1 served, and the v3 CHECK
  constraints would reject a v1 write that reintroduces a malformed row. Restore
  from backup rather than trying to hand-reverse repairs.
- **After the first v3 mutation commits** — `iam_mutations` receipts exist and
  `iam_scopes` revisions have advanced; a v1 binary ignores both, so resuming v1
  writes would desync the anchors and re-open the idempotency/atomicity holes v3
  closed. Past this line, rollback means **restore the §3 backup and redeploy
  v1** as a fresh v1 state, accepting the loss of any v3-era writes. There is no
  in-place downgrade.

The §3 backup is the real rollback mechanism on both sides of the line; the
schema itself is not downgradable in place.

## Migration-source ordering

Authorization's four files are migration source **`authorization`**, distinct
from `cms`/`auth`/`jobs`/`events`. The shared `(source, version)` ledger
(`schema_migrations`, `version` = the full filename) expresses **no ordering
between sources**, so the host decides where the `authorization` source sits in
its own pre-boot stream. The source is **self-contained**: its four files create
and constrain only the `iam_*` tables and reference no other feature's schema, so
it can sit anywhere in the host's ordered stream today — before or after
`cms`/`auth`/`jobs`/`events`.

What is fixed is the **intra-source** order: the four files apply as a
contiguous group in filename order (`0001`→`0004`), because `0003`/`0004` are the
write path over the tables `0001`/`0002` create, and the boot probe requires all
four present before wiring. A host merging every feature into a single ledger
directory (the `examples/cms` `primary/` pattern) keeps the four together in
filename order; a host applying each feature's embedded `MigrationsFS` with its
own `RunMigrations` call (the `auth-cms` per-source pattern) applies the
`authorization` source as one call. Either way, **hosts never renumber the
scaffolded files** — the filenames are the ledger keys and the pgx/turso siblings
carry the byte-identical filename set.

**Effects/events composition is out of scope for v3.** A future effects packet
will make authorization mutations write to a same-transaction events outbox; at
that point the `events` source must be applied before authorization's
effect-emitting writes are wired, introducing a composed authorization↔events
migration-source ordering. That composed ordering is **owned by the effects
packet**, not this v3 runbook. v3 emits no effects, so no cross-source ordering
constraint exists today.

## 9. Destructive reset path — example / dev hosts only

An example or dev host with disposable data runs **none** of §1–§8. It drops the
old `iam_*` tables and their `schema_migrations` rows for source
`authorization`, then applies the canonical v3 set fresh. This is the only
supported "just start over" path and must never be pointed at production data.

```sql
-- DEV/EXAMPLE ONLY — irreversible data loss.
DROP TABLE IF EXISTS iam_mutations;
DROP TABLE IF EXISTS iam_scopes;
DROP TABLE IF EXISTS iam_roles;
DROP TABLE IF EXISTS iam_relationships;
DELETE FROM schema_migrations WHERE source = 'authorization';
-- then apply the canonical 0001–0004 fresh via the host's runner
```

Note: the framework's own dev containers use `source = 'default'` for a
single-ledger host stream; delete the ledger rows for whichever source the host
recorded the `authorization` files under. `examples/auth-cms` needs none of this
— it runs the authorization stores in-memory and has no migration ledger.

## Executed evidence (AZ3-5.1, 2026-07-14)

This runbook was executed end to end against a populated v1 fixture on both
dialects. The PostgreSQL leg additionally **booted** a v3-composed
`authorization.Service` over the converted store and compared access. The
repeatable form of the boot + access comparison is
`features/authorization/stores/pgx` `TestUpgradeRunbook` (env-gated on
`POSTGRES_TEST_DSN`, hermetic-skippable, fully re-runnable — it owns and drops the
`iam_*` tables around its run).

**v1 baseline used.** The pre-v3 schema was reconstructed verbatim from git
`d11c7a2` (`authorization-v1 Z2b`): `iam_relationships` + `iam_roles` with the same
columns v3 keeps but **no** `ck_*` constraints and **no** `iam_scopes` /
`iam_mutations` tables, and the v1 `idx_iam_relationships_unique_subject`
(silent-conflict) index. No column is added or dropped across v1→v3.

**Fixture design (row → category).**

| row | category |
|---|---|
| `group:geng#member@user:umem`, `group:geng#admin@user:uadm` | direct concrete group members / admins |
| `doc:dret viewer user:u1` | RETAIN — concrete principal grant |
| `doc:dmem viewer group:geng#member` | RETAIN — valid `#member` userset |
| `doc:dcon viewer group:geng` (empty relation) | LOSE — concrete-group grant v1 expanded to members |
| `doc:dadm viewer group:geng#admin` | LOSE — non-member userset v1 read as member |
| `doc:downed owner user:uowner` | last owner (guardian-protected under v3) |
| `org:o1 member group:gorg` (empty relation) | AMBIGUOUS — concrete where `org.member` requires a userset |
| `doc:dbad viewer(='') user:ubad` | BLOCKING (1a) — empty relation |
| `user:uglob platform_admin ('','')` | global role |
| `user:uscope editor doc:dscope` | scoped role |
| `user:ubad2 editor doc:('')` | BLOCKING (1b) — half-populated scope pair |
| `user:ubad3 role('') ('','')` | BLOCKING (1b) — empty role |

**What each detection query found.** `1a` → 1 row (`doc:dbad`, empty relation);
`1b` → 1 empty-role row (`ubad3`) and 1 half-populated scope pair (`ubad2`); `1c` →
9 distinct shapes including the ambiguous `org|member|group|''` (`org:o1`) and the
deliberate concrete-group `doc|viewer|group|''` (`dcon`); `1d` → 1 (`group|admin`,
the non-member userset that changes meaning); `1e` → the full subject→relation
export (no actual silent conflicts in this fixture).

**Ambiguous/malformed rows stop the upgrade.** With the malformed rows present,
each constraint-add (`ALTER TABLE … ADD CONSTRAINT` on PostgreSQL; the
`INSERT … SELECT` into the constrained rebuild table on SQLite) **failed** —
`ERROR: check constraint "ck_iam_relationships_nonempty" … is violated by some
row` / `CHECK constraint failed: ck_iam_relationships_nonempty`. The operator then
deleted the three structurally-malformed rows (`dbad`, `ubad2`, `ubad3`); `1a`/`1b`
re-ran to zero; and only then did the constraint-add succeed. The
meaning-changing rows (`dcon`, `dadm`, `org:o1`) were left as stored per operator
sign-off — **none was defaulted to `member`**.

**Post-boot access comparison (PostgreSQL boot, `TestUpgradeRunbook`).** After
conversion (constraints added, `iam_scopes`/`iam_mutations` created, 9 anchors
seeded at revision 0, `iam_mutations` empty) a v3 `Service` booted over the pgx
store and reported:

| check | v1 would grant | v3 result | verdict |
|---|---|---|---|
| `user:u1` view `doc:dret` | ✓ | ✓ | RETAIN |
| `user:umem` view `doc:dmem` | ✓ | ✓ | RETAIN |
| `user:umem` view `doc:dcon` | ✓ (member collapse) | ✗ | LOSE |
| `user:umem` view `doc:dadm` | ✓ (member collapse) | ✗ | LOSE |
| `user:uadm` view `doc:dadm` | ✓ | ✓ | RETAIN (exact `#admin`) |
| global `user:uglob` HasRole(any) | ✓ | ✓ | RETAIN |

A SQL emulation of v1's hard-coded `relation='member'` expansion confirmed v1
reached `{dcon, dadm, dmem}` for `user:umem`; v3 reaches only `dmem`. The last
owner is protected: revoking `doc:downed`'s only owner returned
`OutcomeInvariantBlocked` with the owner left in place.

**Rollback boundary demonstrated.** Before any v3 mutation, a v1-style write
reintroducing a malformed empty-relation row was **rejected** by the applied
constraint — proving repairs cannot be hand-reversed and restore-from-backup (§8)
is the mechanism. The first committed v3 mutation persisted a receipt and advanced
`doc:downed`'s anchor past the seeded 0, marking the documented past-the-line
boundary where a resumed v1 binary would desync.

## Validation status

Executed live on 2026-07-14 (AZ3-5.1):

- **PostgreSQL** — a scratch C-collation database (`az3_upgrade`, TEMPLATE
  template0 LC_COLLATE/LC_CTYPE 'C') on the `authv3-pg` container: the full v1
  fixture above was seeded, `CONVERSION.md` **1a**–**1e** returned the expected
  rows, the constraint-add blocked-then-succeeded across repair, the canonical
  `0003`/`0004` created the new tables, the §7b seeding produced 9 revision-0
  anchors, and `TestUpgradeRunbook` booted a v3 `Service` and confirmed every
  gain/lose/retain verdict and the rollback boundary. Scratch DB dropped after.
- **libSQL/SQLite** (`sqlite3` 3.43.2, the turso dialect) — the same detection
  queries returned parity results; the table-rebuild constraint-add
  blocked-then-succeeded across repair; the canonical `0003`/`0004` created the new
  tables; and both the `ON CONFLICT DO NOTHING` and `INSERT OR IGNORE` seeding
  variants produced 9 revision-0 anchors idempotently. Scratch file dropped after.
  (The turso live SchemaProbe stays integration-gated against the shared
  `authv3-libsql` container; the runbook SQL is validated on `sqlite3` per the
  AZ3-2.6 precedent so the container's conformance state is not disturbed.)
