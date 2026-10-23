# Host handoff — audit completeness and integrity (SOC 2 slice S2, released 2026-10-23)

**Status:** OPEN — apply in gps-360-go, Segovia and coordination-hub on each host's own
schedule. Release record: `RELEASING.md` "Audit completeness and integrity". Plan of record:
`plans/s2-audit-completeness.md`. Manifest: `plans/s2-audit-completeness-release-manifest.json`.
This handoff builds on the S1 handoff (`plans/soc2-s1-production-posture-handoff.md`), which is
still open in all three hosts; S2 changes one line of it (the authorization store pins, below).

## The versions, in repin order

Repin in this order so every `go.sum` resolves against published tags; `go mod tidy` with
`GOWORK=off` after each step (and `go mod vendor` in a host that vendors). Steps 1–3 are each a
deploy of their own.

1. **The S1 adoption, with the expand pins.** sdk **v0.16.0** → pgxdb **v0.10.0** →
   `pockets/authorization` **v0.28.0** TOGETHER WITH `pockets/authorization/stores/pgx`
   **v0.19.1** / `stores/turso` **v0.18.1** — DIRECTLY, skipping v0.19.0 / v0.18.0 →
   `pockets/authentication` **v0.20.0**. Everything else in the S1 handoff applies unchanged.
   Deploy it to EVERY instance before step 3: v0.19.1 / v0.18.1 admit the authorization schema
   with or without 0005; v0.19.0 / v0.18.0 refuse it once 0005 is applied.
2. **Authentication audit.** Export and apply `0021_security_events_integrity.sql`, then
   `0022_security_events_immutable.sql` (migrate, then deploy), split the database roles (below),
   then `integrations/datastores/pgxdb` **v0.11.0** / `integrations/datastores/turso` **v0.8.0**
   with `pockets/authentication` **v0.21.0** + `stores/pgx` **v0.10.0** / `stores/turso`
   **v0.9.0**. The authentication stores require the new connector, so they move together; the
   authorization stores stay on v0.19.1 / v0.18.1, a pair the release cold-verified. Deploy until
   production boots clean.
3. **Authorization audit, LOCKSTEP after 0005.** With every instance on v0.19.1 / v0.18.1,
   export and apply `0005_iam_audit_integrity.sql`, then `0006_iam_audit_immutable.sql`, then
   repin `pockets/authorization` **v0.29.1** + `stores/pgx` **v0.20.0** / `stores/turso`
   **v0.19.0** (`stores/goredis` unchanged) — pin authorization v0.29.1, not v0.29.0, so gate denials name their target. Wire the bridge (below) in the same deploy.
4. `workshop/gopernicus` **v0.6.0** for `gopernicus guard --posture` (P9: every migration a
   mounted framework store ships has a byte-identical copy in the host ledger).

`pockets/secrets` **v0.2.0** + `stores/pgx` **v0.2.0** (migrations `0002_secrets_audit_integrity.sql`
then `0003_secrets_audit_immutable.sql`, migrate-then-deploy) apply only to a host that mounts
secrets. None of the three hosts mounts secrets today.

## Rolling deploys and rollback

**Rolling deploys: an old authorization instance that restarts after 0005 refuses to boot. Use a recreate deploy or a maintenance window unless every running instance is already on v0.19.1/v0.18.1.**

- migrate 0005 only after EVERY instance is on v0.19.1/v0.18.1; after 0005 roll back only to v0.19.1, never v0.19.0 (on turso: v0.18.1, never v0.18.0).
- Authentication 0021 and secrets 0002 are migrate-then-deploy: their store probes check
  that known columns are present, every new column has a default, and the previous binary keeps
  running on a migrated database.
- The reverse of 0005/0006 is reference SQL in `pockets/authorization/stores/UPGRADE.md` and
  `RELEASING.md`; it discards the evidence 0005 recorded. Author it as a new forward ledger
  entry, never by deleting or editing an applied file.

On PostgreSQL every S2 file is metadata-only adds plus triggers and begins `SET LOCAL
lock_timeout = '5s'`: a file blocked behind a long reader (a `pg_dump`) aborts the whole pending
stream with nothing applied, and you retry. The S2 PostgreSQL files require PostgreSQL ≥ 14.

On Turso, authentication `0021` rebuilds `security_events` and authorization `0005` rebuilds
`iam_audit` and `iam_operations`. **Turso rebuilds run inside the runner's two-minute budget; check rail size first.** Also, the turso rebuild holds the write lock: schedule a maintenance window, snapshot or branch the database first, and run the 0004 preflight (host views, triggers
and indexes on the rebuilt tables). A later host migration that rebuilds an audit table on Turso
ships a paired file re-creating its triggers.

## The migration files to export, in order

Copy each file byte-for-byte from the store module's `migrations/` directory (`go mod download
-json <module>@<version>` prints the module's `Dir`) as the next number of the ledger that holds
that store's tables. Never renumber or edit an applied file. Per rail, the integrity file comes
before its immutable file:

| rail | store | files, in order | when |
|---|---|---|---|
| `security_events` | `pockets/authentication/stores/pgx` or `/turso` | `0021_security_events_integrity.sql`, `0022_security_events_immutable.sql` | step 2, before the authentication repin |
| `iam_audit`, `iam_operations` | `pockets/authorization/stores/pgx` or `/turso` | `0005_iam_audit_integrity.sql`, `0006_iam_audit_immutable.sql` | step 3, after every instance is on v0.19.1 / v0.18.1, before the authorization repin |
| `secrets_audit` | `pockets/secrets/stores/pgx` | `0002_secrets_audit_integrity.sql`, `0003_secrets_audit_immutable.sql` | not mounted by any of the three hosts |

**Declining a trigger file** — the immutability trigger files are ordinary migrations: delete them from your ledger before applying if you decline. A declined file is never applied;
`guard --posture` (v0.6.0) prints a `declined:` note on P9's status line for an absent
`*_immutable.sql` and a finding for an edited copy. On PostgreSQL the probe then judges the
runtime role by its privileges alone; on Turso the store WARNs once per table at boot.

The reference host's ledger is the worked example: `examples/production-baseline/workshop/migrations/primary`
carries the authorization pair as `0025_iam_audit_integrity.sql`, `0026_iam_audit_immutable.sql`
and the authentication pair as `0027_security_events_integrity.sql`,
`0028_security_events_immutable.sql`.

## The role the server connects as, and what the probe will say

From authentication `stores/pgx` v0.10.0, authorization `stores/pgx` v0.20.0 and secrets
`stores/pgx` v0.2.0, a store with an audit table probes its runtime role when it is
constructed. The contract, in one sentence: **in production a store refuses to boot when its
runtime role is superuser, owns an audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without
the verified triggers, or cannot insert into it.** Development logs one `append_only=unenforced
tables=[…]` line; the zero mode is silent; the turso stores only WARN.

| the server connects as | production boot |
|---|---|
| the role that runs the migrations (the single-role host), or a member of it | refused: `pgxdb.ErrAppendOnlyTableWritable`, `owner=table` (or `schema` / `function`), every audit table, zero requests served |
| a superuser | refused: `ErrAppendOnlyTableWritable`, `superuser` |
| a runtime role with table grants, the trigger files applied | boots: `protected_by=trigger` (the migration's `GRANT USAGE … TO PUBLIC` covers the sequence) |
| the same role after the REVOKEs below | boots: `protected_by=privilege` |
| a runtime role that keeps UPDATE/DELETE/TRUNCATE/TRIGGER with a trigger file declined, edited, or left `tgenabled=O` by `pg_restore --disable-triggers` | refused: `ErrAppendOnlyTableWritable` naming `privileges=…` or the trigger state, with the fix |
| a role without INSERT, or without USAGE on `<rail>_seq` (PUBLIC's grant revoked) | refused: `ErrAppendOnlyTableUnwritable` naming the sequence; never waivable |

The waiver, `pgxdb.Config.AppendOnlyWritableAcknowledged: true`, admits a writable role only; it
is a literal in the composition root (P5 prints its `ack`), logs `trigger_exempt=true` for an
owner, and is the host's written exception, not a default.

**The fix is two roles** — create the runtime role with SQL, not the console (provider console
roles can carry `pg_write_all_data`, which no per-table `REVOKE` removes). Create it BEFORE
migrating so the default privileges cover every table, then narrow the audit tables. Adjust
`public` to the schema your stores are mounted in with `WithSchema`:

```sql
-- as the migrator
ALTER SCHEMA public OWNER TO migrator;
REVOKE CREATE ON SCHEMA public FROM runtime;
GRANT USAGE ON SCHEMA public TO runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO runtime;

REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON security_events FROM runtime;
GRANT USAGE ON SEQUENCE security_events_seq TO runtime;
REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON iam_audit FROM runtime;
GRANT USAGE ON SEQUENCE iam_audit_seq TO runtime;
REVOKE UPDATE, TRUNCATE, TRIGGER ON iam_operations FROM runtime;   -- DELETE stays granted
```

S0 hosts that revoked DELETE on `iam_operations` grant it back before S3
(`GRANT DELETE ON iam_operations TO runtime;`): its trigger refuses deleting a fenced row and
admits the expiry of the others. The full recipe is
`workshop/documentation/docs/guides/database-posture.md` ("Two roles").

**Preflight with `-posture-role`, and archive the output.** Before each deploy, from the
migrator's connection, run `go run ./workshop/migrations -posture-role <runtime role>` (a
scaffolded runner from workshop v0.6.0 has it; a host-written runner copies `postureTables` and
`reportPosture` from `examples/production-baseline/workshop/migrations/main.go`, calling each
store's `AuditTables(...)` with the options the server constructs it with — `WithSchema`,
`WithAudit()`). It prints one `table=<t> protected_by=<privilege|trigger|waiver|none> …` line
per table and decides exactly as the boot probe will. **Archive its output with the deploy as
audit evidence.** `AuditTables` exists from authentication `stores/pgx` v0.10.0 and authorization
`stores/pgx` v0.20.0, so step 2 preflights `security_events` and step 3 adds `iam_audit` and
`iam_operations` (run it from the step-3 build before the server rolls).

## Host code to check before the repin

- grep your code for `DELETE FROM security_events|iam_audit|secrets_audit` and run it as the migrator. The triggers refuse UPDATE, DELETE and TRUNCATE on the rails for every role but the
  owner; an `iam_operations` DELETE of a fenced row is refused too.
- `pgxdb.MapError` now maps SQLSTATE 42501 to `sdk.ErrForbidden` (turso: `is append-only`). A
  check that matched the raw driver error for a missing grant sees the sentinel now.
- **Dashboards reading `details.email` (login rows) or `details.identifier` (invitation rows)
  — AUDIT-054.** New rows carry `identifier_kind` + `identifier_digest` instead. Existing rows keep raw `email` and `identifier` until S3 lands. The control-map M3 row says 'new rows only'.
  Known users are found by `user_id`. The runbook gives a runnable digest command for unknown-address lookups, with no Go required. That runbook is AUDIT-054 in `AUDIT.md`
  ("Migration", step 3): use its shell recipe as written — the digest is an HMAC over three
  length-prefixed fields, so hand-built shortcuts digest the wrong input; its check value
  confirms your shell before you query.

## The bridge — denials onto the security-event rail

The authorization decision observer is opt-in; production logs one WARN at construction when
none is wired. Copy `examples/production-baseline/pockets/audit/outbound/securityevents`
(`bridge.go` and its test) into a host pocket at `pockets/audit/outbound/securityevents` —
never `cmd/` (H10). In `cmd/`:

- `bridge := securityevents.NewBridge(authenticationComponents.SecurityEvents)` — the
  authentication pocket's one recorder, so a bridged row it loses counts in
  `Components.AuditWriteFailures()`; never build a second recorder;
- `authorization.WithDecisionObserver(bridge.Observer())`, and
  `authorizationhttp.WithDecisionObserver(bridge.Observer())` when the host rebuilds the HTTP
  adapter;
- `authentication.WithAuditWriteObserver(...)` logging each lost row at ERROR, and
  `AuditWriteFailures()` and `bridge.Dropped()` in the health detail.

It records denials (`authorization_denied`, `failure`) and refused role writes
(`role_write_refused`, `blocked`) only, at most 8 writes in flight (extra events are dropped
and counted) and one row per (type, principal, permission, resource) per 60 s, per instance.
With authorization v0.29.1 a gate denial names its permission and resource. Bridged rows carry
no `ip_address` / `user_agent` yet (a known follow-up).

## gps-360-go

- **Today:** the local clone's `origin/main` (00b63c22, 2026-10-22) pins sdk v0.15.0, pgxdb
  v0.9.0, authentication v0.19.1 + `stores/pgx` v0.9.0, authorization v0.27.0 + `stores/pgx`
  v0.18.0 + `stores/goredis` v0.5.0. Re-read `go.mod` before starting. It vendors: `go mod
  vendor` after each tidy.
- **Repin:** step 1 (the open S1 adoption) with authorization `stores/pgx` **v0.19.1**, skipping
  v0.19.0 → step 2 (auth 0021 → 0022; pgxdb v0.11.0, authentication v0.21.0 + `stores/pgx`
  v0.10.0) → step 3 (authorization 0005 → 0006; authorization v0.29.1 + `stores/pgx` v0.20.0;
  the bridge) → step 4 (workshop v0.6.0). No turso store, no secrets.
- **Migrations:** export the two pairs into the ledger that holds the authentication and
  authorization tables, after its current last file; P9 checks the copies (gps-360-go also
  mounts `pockets/jobs/stores/pgx`, whose ledger P9 checks the same way).
- **Server role:** the role in the server's database URL for the authentication and
  authorization stores is ______ (owner fills in). If it is the role the migrate step uses, the
  first production boot after step 2 refuses with `ErrAppendOnlyTableWritable` `owner=table`:
  create the runtime role with the recipe above (in the stores' schema) BEFORE step 2 and
  archive the `-posture-role` output.
- **Postgres (S1 O-4, still open — owner fills in):** gps-360-go connects to Cloud SQL via ______.
- **Deploy method (owner fills in):** ______ (recreate, rolling, or a maintenance window — see
  "Rolling deploys" above before step 3).

## Segovia (v2)

- **Today:** the local clone's `origin/main` (ea8abb12, 2026-10-23) pins sdk v0.15.0, pgxdb
  v0.9.0, authentication v0.19.1 + `stores/pgx` v0.9.0, authorization v0.27.0 + `stores/pgx`
  v0.18.0 + `stores/goredis` v0.5.0, events and jobs `stores/pgx`. Re-read `go.mod` before
  starting; work from a branch or worktree off `main`.
- **Repin:** step 1 (the open S1 adoption, incl. the S1 handoff's P0/P1/P3/P6 findings) with
  authorization `stores/pgx` **v0.19.1**, skipping v0.19.0 → step 2 (auth 0021 → 0022; pgxdb
  v0.11.0, authentication v0.21.0 + `stores/pgx` v0.10.0) → step 3 (authorization 0005 → 0006;
  authorization v0.29.1 + `stores/pgx` v0.20.0; the bridge) → step 4 (workshop v0.6.0). No
  turso store, no secrets.
- **Migrations:** the two pairs go into the ledger that holds the authentication and
  authorization tables; P9 also checks the events and jobs stores' ledgers.
- **Server role:** ______ (owner fills in). A server on the migrating role refuses at the first
  production boot after step 2 (`owner=table`); split the roles first and archive the
  `-posture-role` output.
- **Deploy method (owner fills in):** ______.

## coordination-hub

- **Today:** unknown from here — the local clone was last fetched 2026-08-31 (it shows
  authentication and authorization on pgx stores, far behind S1). Read the hub's current
  `go.mod` and confirm its branch and HEAD before starting; another session may be working in
  that repo.
- **Repin:** the same four steps; step 1 is the whole S1 handoff for the hub, with authorization
  `stores/pgx` **v0.19.1** or `stores/turso` **v0.18.1** (whichever the hub mounts), skipping
  v0.19.0 / v0.18.0. If the hub mounts the turso stores, both `0021` and `0005` are table
  rebuilds: read the Turso paragraph under "Rolling deploys" before step 2.
- **Server role:** ______ (owner fills in); the probe table above says what each choice meets.
- **Deploy method (owner fills in):** ______.

## After the repin

Run `guard --posture` (workshop v0.6.0) in CI and require a zero exit. Archive the
`-posture-role` output and the boot log's `posture_waiver` lines with every deploy. Record any
declined trigger file and any `AppendOnlyWritableAcknowledged` waiver in the host's own posture
doc: they are the exceptions an auditor samples. Watch `audit_write_failures` and
`bridge_dropped` in the health detail.
