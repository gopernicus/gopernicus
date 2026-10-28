# Go-minted ids by entity class (NanoID identities, UUIDv7 records); PostgreSQL 18 floor

**Status:** EXECUTED 2026-10-27 — ten PRs plus the record (#239–#248 and PR-6), fourteen tags on
thirteen modules (`pockets/authentication/v0.33.1` was added when PR-2b split its storetest out),
plus `workshop/gopernicus/v0.10.0` for the scaffold. The R5 `EXPLAIN` gate changed both plans, so
R5 (a) was kept. As-built deviations, the tag table, the MVS sets and the checksums are in the
release record: `RELEASING.md` "Default ids by entity class; PostgreSQL 18 floor"; audit entry
AUDIT-057. Was RATIFIED rev 4 — 2026-10-27. Owner ruled "all recs" on rev 3 (R1 a, R3 a, R4 a, R5 a
EXPLAIN-gated, R7 a, R8 a, R11 no PG17 maintenance line, R12 a) and added one ruling: the live-host
upgrade protocol is CUT from this release (no host has data). Rev 1 (from `5514e3b3`) proposed
database-minted ids; the owner rulings superseded it. Rev 2 rewrote the body to the rulings; rev 3
folded the review gate (lead-backend, data-integration, platform-sre: ship-with-edits;
architecture-steward: aligned-with-edits) — see **Decided by gate**. Rev 4 = rulings applied,
restamp protocol cut, pgxdb renumbered to **v0.17.0** (v0.16.0 was taken by the RLS primitives
release, #237, `main` @ `8c66ea16`), and the tag protocol updated (Claude cuts and pushes tags).
**PR-1 branches from `origin/main` @ `8c66ea16` or later.** Code citations and line numbers are
from `66d2abde`; #237 touched only pgxdb (`TransactWithSettings`, `(*Tx).Setting`, the RLS guide),
so pgxdb line numbers may have shifted — re-locate by symbol.

**Owner direction (2026-10-27, verbatim intent):** "I think it likely makes sense to have
everything (except explicitly denied like sessions) to generate their own default ids from the
database UNLESS you pass in an id generator function." And: "For new versions of gopernicus we
should require 18+ postgres." — refined the same day into the rulings below (Go mints; the database
default is a backstop).

## Owner rulings 2026-10-27 (supersede the rev 1 draft; this body implements them)

- **Go mints ids by default (option B), not the database.** The pocket's default generator is chosen
  per entity class, and a host-supplied generator (`WithIDs` / `Config.IDs`) overrides every default.
  Gopernicus does not decide that hosts want UUIDv7; it only ships it as the default. The database
  column defaults stay as a backstop for raw SQL inserts.
- **NanoID by default:** users, service accounts, invitations. These are identities shown to people
  and outside systems, and the user id is the JWT `sub`; a UUIDv7 would reveal when the user signed up.
- **UUIDv7 by default:** everything else that is a generated record id. That covers security events,
  the authorization audit, jobs (`job_queue`, `fenced_job_queue`), API key ids, identifiers, passkeys,
  TOTP, authentication grants and the cms records, and the same rule applies to every migration a
  pocket ships.
- **Column type stays `TEXT`; generated id columns get `COLLATE "C"`.** This includes NanoID columns
  (the collation is a property of the column, not of the generator). There is no `uuid` column type.
  Under the greenfield rule, edit each table's original `CREATE` migration; existing hosts get an
  upgrade snippet in RELEASING/README, never an `ALTER` file in the canonical set.
- **The PostgreSQL 18 floor stands** (P1); the owner's hosts already run PG18 from a Docker image.
- **Add a pin test for the invitation secret** (`logic/invitations/service.go:38`, missing from the
  Exclusions list).
- **In-memory and reference stores** are built with their own id generators (they are used only in
  local development); there is no fallback logic for them.
- **Sequencing:** finish S4d part 2 first (PR-2 owns migration 0029 and the next store minors).
  Then revise this plan and send it through the planner and the two-reviewer check. *(Done: S4d part 2
  closed at #228; QuerierFrom closed at #235; this is the revision.)*
- **Greenfield rule, clarified 2026-10-27** (memory `feature-migrations-greenfield`): shipped
  migration sets are additive-only defaults; `ADD COLUMN` is fine for an opt-in feature; nothing
  destructive; hosts own their schema. A changed default or collation is an edit to the table's
  ORIGINAL `CREATE` file for greenfield hosts, plus a RELEASING/README snippet that live hosts copy
  into their own migration.

**Rulings on rev 3 (2026-10-27, "all recs"):**

- **R1 (a)** — identity-class columns (`users`, `service_accounts`, `invitations`) keep the
  `gen_random_uuid()::text` backstop.
- **R3 (a)** — challenges, contact changes and authentication grants are Go-minted with the record
  generator; the stores' empty-id branches stay as the backstop.
- **R4 (a)** — `0014_user_status.sql` / `0019_oauth2_sessions.sql` are left as they are.
- **R5 (a)** — the referencing id columns are collated too, in the canonical `CREATE` files,
  **EXPLAIN-gated** (PR-2b; if the directory join's plan does not change, fall back to generated-id
  columns only).
- **R7 (a)** — `secrets_audit.id` is left alone.
- **R8 (a)** — `Open` WARNs in every mode; `RunMigrations` refuses.
- **R11** — no PG17 maintenance line; "upgrade to 18" is the only path.
- **R12 (a)** — an empty generated id is rejected with `sdk.ErrInvalidInput` in the jobs stores and
  in `audit.NewRecordsWith`.
- **NEW — the live-host upgrade protocol is CUT from this release.** None of the hosts has data:
  gps-three-sixty and battleboardhq are greenfield, gps-360-go is a rough draft, segovia is being
  rebuilt from scratch. A host that already applied the old canonical files re-exports its ledger
  against a fresh database. A live-data upgrade path is deferred until a host needs it; until then
  "defer" (stay on the old store pins) is the supported state. The analysis is kept under
  **Deferred: live-host upgrade protocol**.
- **Standing protocol change** — Claude cuts and pushes tags: after the owner merges and a
  `live-stores` dispatch on `main` at the squash commit is green, Claude cuts the annotated tags,
  pushes them, confirms them on `origin`, then cold-verifies. The owner still merges.

## Context

Record ids are minted in Go today, but by one generator for everything: `sdk.IDGenerator`'s zero
value emits a 21-char NanoID (`sdk/id.go`), every pocket that takes an `IDs` option defaults to that
zero value, and several sites bypass or never see the option (`sdk.IDGenerator{}` literals in the
authorization audit and the OAuth2 recorder; store-side `newID("job")` in the jobs stores; two
identifier rows the logic leaves empty so the database mints them). The bundled pgx stores already
honor an empty id by omitting the column and reading `RETURNING id` (the `sdk.DatabaseID`
convention, amended D10), so each table carries a schema default — `gen_random_uuid()::text` on pgx
(UUIDv4) and `lower(hex(randomblob(16)))` on turso — and only some of those columns carry
`COLLATE "C"`.

The rulings split generated ids into two classes with two defaults, keep one host override, pin
every pgx id column to `COLLATE "C"` with a `uuidv7()::text` backstop for record columns (PostgreSQL
18), and keep secrets and keys out of the switch. The repo's verification evidence is PostgreSQL 17
everywhere today; the owner's hosts (`gps-three-sixty`, `battleboardhq`) already run
`postgres:18-alpine`.

Inventory (every non-test generator site and every generated-id column; the gate added the three
rows marked ★):

| Site | Today | Class under this plan |
|---|---|---|
| `users.id` — `user.NewUser(s.ids, …)` (`logic/authentication/user/user.go:52`; `service.go:575`, `oauth.go:618`, `provisioning.go:103`, `external.go:851`) | `IDs` (NanoID zero value) | **identity** (NanoID) |
| `service_accounts.id` — `serviceaccount.New(s.ids, …)` (`machine.go:110`) | `IDs` | **identity** |
| `invitations.id` — `invitations.NewWithMetadata(s.ids, …)` (`logic/invitations/service.go:559`) | `IDs` via `invitations.WithIDs` | **identity** |
| `user_identifiers.id` — `identifier.New` / `NewRegistrationEmail(s.ids, …)` (`service.go:571`, `oauth.go:613`, `external.go:836`) | `IDs` | **record** (UUIDv7) |
| ★ `user_identifiers.id` — the passwordless provisioning template `NewIdentifier` (`provisioning.go:104`) has no `ID`; the stores insert it as-is (`stores/pgx/passwordless.go:199-206`, turso `:156`) | **database-minted**, ignores `WithIDs` | **record** — logic sets the id |
| ★ `user_identifiers.id` — the confirmed contact change: `identifier.ApplyVerifiedChangeInput` (`identifier/repository.go:17`) has no `ID` field; `stores/pgx/identifiers.go:199` and the turso twin build an `Identifier{}` with no id | **database-minted**, ignores `WithIDs` | **record** — new additive input field, both stores honor it |
| `api_keys.id` — `apikey.New(s.ids, …)` (`machine.go:195`) | `IDs` | **record** |
| `security_events.id` — `securityevent.NewRecorder(repos, cfg.IDs, …)` (`constructor.go:546`); `newSecurityEventRecorder(…, d.IDs, …)` (`logic/authentication/service.go:448`, `helpers.go:20` via `external.go:202`); ★ `securityevent.NewRecorder(d.SecurityEvents, d.IDs, …)` (`logic/invitations/constructor.go:89`); the OAuth2 fallback `securityevent.NewRecorder(…, sdk.IDGenerator{}, …)` (`oauth2/service.go:99`) | five constructions; the OAuth2 one bypasses `WithIDs` | **record** — the class default moves INTO `NewRecorder`, which fixes all five at once |
| `passkey_credentials.id` (`passkey_ceremony.go:284`), `recovery_codes.id` (`totp_enrollment.go:320`); ★ the store-side SQL mint `COALESCE(NULLIF(c.id, ''), gen_random_uuid()::text)` (`stores/pgx/totp.go:203`) | `s.ids`; the SQL mint is v4 | **record**; the SQL mint becomes `uuidv7()::text` |
| `challenges.id`, `contact_changes.id`, `authentication_grants.id` — struct literals at `challenge.go:225`, `stepup.go:401`, and `contactchange.New` (`contactchange.go:65`, no ids parameter) leave `ID` empty; the stores insert a supplied id and omit the column only when it is empty (`stores/pgx/challenges.go:91-95`, `contact_changes.go:69-73`, `authentication_grants.go:125-136`; turso twins) | database-minted by convention | **record** — R3 (a): logic sets the id; the stores' empty-id branches stay as the backstop |
| cms `entries`, `terms`, `menus`, `menu_items`, `assets`, `inquiries` — `cms.Config.IDs` into five services (`cms.go:198-205`) | `IDs` | **record** |
| jobs `job_queue.job_id`, `fenced_job_queue.job_id` — `newID("job")` in `stores/{pgx,turso,memory}`; core paths `logic/queue/service.go:98`, `logic/queue/work.go:63,85` | store-minted `job_<32 hex>`, no option, no column default | **record** — the CORE mints (`jobs.WithIDs`); stores reject an empty id |
| jobs `job_schedules.schedule_id` — `newID("sched")` at `Ensure` in the three stores; core path `logic/schedules/service.go:119` | store-minted `sched_<32 hex>` | **record** — the core mints a create-only candidate (`Ensure.ID`) |
| authorization `iam_audit.event_id` — `(sdk.IDGenerator{}).Generate()` in `logic/audit/audit.go:221`, called from the three stores' `audit.NewRecords(ctx, changes, now)` (`stores/{memory,pgx,turso}/audit.go:46/49/48`); row `id` = `eventID:%020d` | NanoID literal, no option | **record** — `NewRecordsWith(…, ids)`; store `WithIDs` threads the host generator (records are built inside the store transaction) |
| **Excluded — secrets and keys, never record ids** (each gets a pin test): session ids and refresh tokens (`session/session.go:19`), OAuth state (`oauthstate/oauthstate.go:19`), API-key prefix/secret, nonces, PKCE (`machine.go:22`), the resend generation token (`resend.go:101`), invitation secrets AND `mintOperationID` (`logic/invitations/service.go:38`), maintenance archive keys (`logic/maintenance/service.go:174`), cms media storage keys (`domain/media/asset.go:31`) | `sdk.IDGenerator{}` on purpose | unchanged |
| **Excluded — deterministic, caller-owned or not a record id:** `schedule.OccurrenceID(scheduleID, slot)` (wraps the schedule id: `sched_<id>_<unix>_<ns>`); `iam_operations.operation_id` (idempotency key); `job_schedule_occurrences.job_id`; events `event_outbox.event_id` (the caller's envelope, minted by `sdk/capabilities/events`); the tuple-cache `identity` (`replace(gen_random_uuid()::text,'-','')` under a `^[0-9a-f]{32}$` CHECK); `oauth_clients.id` (host-configured); `external_subjects (provider, subject)`; the synthetic step-up grant built from a session at `stepup.go:176` (never persisted — must not mint) | — | unchanged |
| secrets `secrets_audit.id` — `vault.NewAuditRecord` (`%016x` ns + 8 random bytes) | time-ordered custom id, no option | R7 (a): unchanged |

## Goal

With no `WithIDs`, every pocket mints NanoIDs for users, service accounts and invitations and
UUIDv7s for every other generated record id; one host-supplied generator overrides all of them; every
pgx id column is `TEXT COLLATE "C"` with a database backstop (`uuidv7()::text` for record columns);
pgx modules require PostgreSQL 18 and say so before they migrate.

## Definition of Done

- `sdk.UUIDv7()` ships in `sdk` (stdlib, RFC 9562 text form, version nibble `7`, later mints from
  one generator sort later as text) and `IDGenerator.Or` resolves a per-class default.
- authentication, cms, jobs and authorization: with no `WithIDs` the class defaults apply; with
  `WithIDs(fn)` the host's generator reaches every table including the three paths the gate found —
  proven by a wiring test per pocket driven by a COUNTING generator.
- Every excluded secret/key site has a pin test; no `sdk.IDGenerator{}` literal mints a record id
  in any pocket `logic/`.
- Every pgx canonical `CREATE` for a generated-id column (and, under R5, every referencing id
  column) reads `TEXT COLLATE "C"` with the agreed backstop; hermetic DDL + live catalog collation
  tests exist for authentication, cms and jobs pgx; a live test proves the record-class default
  yields a version-7 uuid.
- `pgxdb.RunMigrations` refuses a server below 180000 with `ErrServerVersion` (operator-actionable
  text); `Open` warns once, non-fatally, on both of its paths. Makefile, `live-stores.yml` (with a
  PG17 service for the negative leg), scaffold templates, READMEs, test headers and the docs site
  say PostgreSQL 18.
- RELEASING carries the tag table, the per-host-shape upgrade notes (greenfield re-export; defer),
  the new checksums, the "Deferred: live-host upgrade protocol" note and the runtime-lockstep
  warning; the baseline ledger is regenerated; every example and the scaffold (templates AND pins)
  are repinned; `make guard && make check` green on each PR; a green `live-stores` dispatch on `main`
  before each tag; every tag confirmed on `origin` and cold-verified.

## Out of scope

- Changing id **column types** to `uuid`. Ports and domain types are strings; turso has no uuid
  type; existing rows hold NanoIDs.
- Rewriting existing ids. Old rows keep their NanoIDs (or v4 backstops); new rows get the class
  default. Mixed shapes in one `TEXT` column are harmless to the framework (host-side length checks
  are Risk 4).
- **A live-data upgrade path** (ledger restamp + in-place ALTERs). Cut by owner ruling; see
  "Deferred: live-host upgrade protocol". Hosts with applied old files re-export against a fresh
  database or stay on the old pins.
- The events outbox `event_id` and `sdk/capabilities/events`' NanoID generator; the pgxdb rate
  limiter (`ratelimit_windows` has no generated id).
- The examples' in-memory stores (`examples/*/pockets/*/outbound/*`): they keep their own
  `sdk.IDGenerator{}` per the "own generators, no fallback" ruling.
- A `uuid`-aware `sdk.NanoID` replacement, any id *parsing*, a cross-process ordering guarantee.
- `pockets/authorization/stores/goredis` (a mirror; mints nothing).
- Any turso DDL change (D3; R6 decided (b)).
- Dropping or editing the two historical ALTER files (`0014_user_status.sql`, `0019_oauth2_sessions.sql`) — R4 (a).
- A PG17 maintenance line of the pgx modules — R11: none.
- `secrets_audit.id` — R7 (a).

## Decisions (planner; owner rulings applied; gate folded)

### D1 — The UUIDv7 generator is a stdlib primitive in `sdk`: `sdk.UUIDv7() IDGenerateFunc`

Three placements were weighed:

1. **`sdk` root, stdlib-only (chosen; architecture-steward: passes root admission and the
   sdk-default rule).** A pocket core may require only `sdk` and the shared `pockets` module (FS1,
   `pockets/README.md`; machine-checked by G5 `guard-pocket-dependencies`), so the default
   generator a pocket uses MUST live in `sdk`. RFC 9562 UUIDv7 is vendor-neutral and ~40 lines of
   `crypto/rand` + `time` — it passes `sdk/README.md`'s root admission (small primitive, name clear
   without a qualifier, no lifecycle) and ARCHITECTURE's sdk-default rule (a stdlib, vendor-neutral
   implementation ships next to its port). It is the exact precedent of `sdk.NanoID`.
   ARCHITECTURE:420 keeps identifier strategies out of `pkg/cryptids`, and "ID generation is an
   independent root file" makes `sdk/id.go` the file. Go 1.26's stdlib has no uuid package.
2. **Pockets importing `integrations/cryptids/google-uuid`** — forbidden by FS1/G5; wrong in kind.
3. **The host passes it in** — contradicts the ruling.

`integrations/cryptids/google-uuid` stays (it still isolates `github.com/google/uuid`; ARCHITECTURE
only says sdk is not *obliged* to carry a second implementation). Its README gains one sentence
saying WHEN a host still picks it: it already links google/uuid, or wants that library's counter
semantics across generators.

Shape, in `sdk/id.go`:

```go
// UUIDv7 returns an IDGenerateFunc emitting RFC 9562 version-7 UUIDs in the
// canonical lowercase 8-4-4-4-12 text form: a 48-bit Unix-millisecond timestamp,
// a 12-bit sub-millisecond fraction (RFC 9562 §6.2 method 3), then 62 random
// bits. Mints from ONE returned generator never sort before earlier mints from
// the same generator; no ordering is promised across generators or processes.
func UUIDv7() IDGenerateFunc
```

Implementation notes: the 60-bit value `ms<<12 | frac` (frac = `ns%1e6 * 4096 / 1e6`) is clamped
under a mutex held IN THE CLOSURE the function returns (`if v <= last { v = last + 1 }`, so the
fraction carries into the milliseconds) — no package-level state in the kernel; 62 random bits
from `crypto/rand` (which no longer returns an error in Go ≥ 1.24 — do not build an untestable
error path; the `IDGenerateFunc` error stays `nil`); version nibble `7`, variant `10`.

### D2 — "A default per entity class, one override" is `IDGenerator.Or`, applied at every mint

```go
// Or returns g when it is configured (Func != nil); otherwise a generator over
// fn. The zero IDGenerator therefore means "the class default wherever Or is
// used" — a host that wants NanoID for a class that defaults to UUIDv7 passes
// sdk.NewIDGenerator(nano) with nano from sdk.NanoID("", 0), not the zero value.
func (g IDGenerator) Or(fn IDGenerateFunc) IDGenerator
```

(The `IDGenerator` doc at `sdk/id.go:35` states the same contract.) The rule the gate insisted on:
**`Or` is applied at the layer that mints**, so no construction path can reach a mint with the raw
zero value.

- **authentication** (`Config.IDs` / `WithIDs`): `ids := cfg.IDs` (identity: users, service
  accounts) and `recordIDs := cfg.IDs.Or(sdk.UUIDv7())` resolved once in `authlogic.New`
  (`service.go:487`) and `newExternalAuthenticator` (`constructor.go:235` / `externalOptions`);
  every record mint reads `recordIDs` — identifiers (incl. `provisioning.go:104`'s template, which
  gains `ID`), API keys, passkeys, recovery codes, and the R3 literals. `securityevent.NewRecorder`
  (`recorder.go:165`) resolves `ids.Or(sdk.UUIDv7())` itself, which covers all five recorder
  constructions including the OAuth2 fallback — no `oauth2.WithIDs` (a host constructing
  `oauth2.New` directly supplies `WithSecurityEventRecorder`). `invitations.WithIDs(cfg.IDs)` is
  unchanged (identity class). New mint sites that already return errors use `Generate()` and
  propagate; entity constructors keep `MustGenerate`.
  `identifier.ApplyVerifiedChangeInput` gains `ID string` (additive); `identifier_management.go`
  fills it from `recordIDs`; both stores insert it when non-empty and keep the empty-id branch.
- **cms** (`Config.IDs`): every service receives `cfg.IDs.Or(sdk.UUIDv7())`.
- **jobs (decided by gate: mint in the CORE, not the stores).** `jobs.WithIDs(sdk.IDGenerator)` on
  `jobs.New` (`pockets/jobs/options.go`); `recordIDs := ids.Or(sdk.UUIDv7())`; `queue.Service.EnqueueJob`
  (`logic/queue/service.go:84-98`), the fenced `EnqueueOnce`/`Replace` paths (`logic/queue/work.go:63,85`)
  fill an empty `Enqueue.ID` before the repository call; `schedules.Ensure` gains an optional
  create-only `ID` candidate (`logic/schedules/schedule.go:44`), filled by the schedules service
  (`service.go:119`). The three stores DROP `newID` and the prefixes and **reject an empty id at the
  repository boundary** with `sdk.ErrInvalidInput` ("id required; the jobs service mints it") —
  uniform across pgx/turso/memory (R12 (a)), so an empty-id generator (`WithIDs(sdk.NewIDGenerator(sdk.DatabaseID))`)
  fails loudly instead of inserting `job_id = ''` and turning the next enqueue into a false
  `ErrAlreadyExists`. A caller-supplied `Enqueue.ID` stays the idempotency key. The pgx
  `DEFAULT uuidv7()::text` is therefore ONLY the raw-SQL backstop.
- **authorization**: `audit.NewRecordsWith(ctx, changes, now, ids sdk.IDGenerator)` (explicit input,
  per the constructor-configuration convention — no options on a pure builder); `NewRecords(ctx, changes, now)`
  = `NewRecordsWith(…, sdk.IDGenerator{})`, so the released stores keep compiling and switch to the
  record default; inside, `ids.Or(sdk.UUIDv7())`; an empty mint is rejected with `sdk.ErrInvalidInput`
  (R12 (a); an empty `eventID` would make row ids `:000…1` and collide on the second write).
  `stores/pgx` and `stores/turso` gain `WithIDs(sdk.IDGenerator)` (records are built inside the
  store transaction, so the host generator must be threaded); memory uses the default. The row id
  stays `eventID:%020d` (42 → 57 chars; no CHECK, hash or parse over it — verified against
  `0005_iam_audit_integrity.sql`).

### D3 — Migrations: edit the original `CREATE` files (pgx only); backstops by class; no turso DDL change

- **Record-class pgx columns** become `TEXT COLLATE "C" … DEFAULT uuidv7()::text`. `uuidv7()` is in
  `pg_catalog`, so it resolves under `WithSchema`'s strict `search_path`.
- **Identity-class pgx columns** keep `DEFAULT gen_random_uuid()::text` (R1 (a): for a host wired
  with `WithIDs(sdk.NewIDGenerator(sdk.DatabaseID))` the backstop IS the generator, so v7 there would
  leak signup time — the exact leak the NanoID ruling prevents).
- **`0001_users.sql` is NOT edited** (gate, three reviewers): `0014_user_status.sql:37` already
  pins `users.id` to `COLLATE "C"` for greenfield and live hosts alike; `collation_test.go` already
  lists it as `pinnedByAlter`. Editing 0001 would be checksum churn with zero schema effect.
- **Referencing id columns (R5 (a), EXPLAIN-gated):** the columns that hold a pocket-generated id
  are pinned to `COLLATE "C"` in their canonical `CREATE` files too — authentication
  `0002 user_passwords.user_id`, `0003 sessions.user_id`, `0004 oauth_accounts.user_id`,
  `0006 service_accounts.owner_user_id`, `0007 api_keys.service_account_id`, `0008 security_events.user_id`,
  `0010 user_identifiers.user_id`, `0011 challenges.user_id`, `0012 contact_changes.{user_id,replaces_identifier_id}`,
  `0013 authentication_grants.{session_id,user_id}`, `0019 oauth_authorization_codes.user_id` +
  `oauth_refresh_history.session_id`, `0020 external_subjects.user_id`, `0025 passkey_credentials.user_id`,
  `0026 totp_credentials.session_id` + `recovery_codes.user_id` (NOT `key_id` — a pepper keyring id),
  `0027 service_account_status.service_account_id`; cms `menu_items.{menu_id,parent_id}`,
  `terms.parent_id`, `entries.parent_id` (files already edited), `0020 entry_fields.entry_id`,
  `0021 entry_terms.{entry_id,term_id}`; jobs `0002 job_schedules.last_job_id`,
  `0004 job_schedule_occurrences.schedule_id`. Host-defined ids (`resource_id`, `tenant_id`,
  `actor_id`, `provider_user_id`, `user_id` in `iam_*`) are excluded. **Gate:** PR-2b runs one
  `EXPLAIN` on PG18 over the directory join (`stores/pgx/user_admin.go:58-69`) before and after
  pinning `user_identifiers.user_id`; if the plan does not change (the index on the referencing
  side is used either way), R5 falls back to generated-id columns only and the referencing-column
  edits are dropped from every task below.
- **turso**: no DDL change anywhere. SQLite `TEXT` compares `BINARY` (the byte-order twin of `"C"`);
  the `lower(hex(randomblob(16)))` backstop stays; the turso runner also checks checksums
  (`integrations/datastores/turso/migrate.go:247`) and SQLite cannot `ALTER COLUMN … SET DEFAULT`
  (R6 → (b)).
- **No canonical ALTER file, and no upgrade snippet in this release** (owner ruling): a host that
  already applied the old files re-exports against a fresh database.

Per-file pgx edits (paths from the repo root; byte changes alter checksums):

| File | Edit |
|---|---|
| `pockets/authentication/stores/pgx/migrations/0007_api_keys.sql`, `0008_security_events.sql`, `0025_passkey_credentials.sql`, `0026_totp.sql` (`recovery_codes.id`) | `DEFAULT gen_random_uuid()::text` → `DEFAULT uuidv7()::text` (collation already `"C"`); R5 referencing columns |
| `…/0010_user_identifiers.sql`, `0011_challenges.sql`, `0012_contact_changes.sql`, `0013_authentication_grants.sql` | add `COLLATE "C"`; `DEFAULT uuidv7()::text`; R5 referencing columns; header comments: "Go-minted UUIDv7 by default; this default is the raw-SQL backstop" |
| `…/0001_users.sql` | **unchanged** |
| `…/0006_service_accounts.sql`, `0009_invitations.sql` | id column unchanged (R1 (a)); 0006 `owner_user_id` under R5 |
| `…/0002_user_passwords.sql`, `0003_sessions.sql`, `0004_oauth_accounts.sql`, `0019_oauth2_sessions.sql`, `0020_external_subjects.sql`, `0026_totp.sql` (`totp_credentials`), `0027_service_account_status.sql` | id tables unchanged; R5 referencing columns only |
| `…/0005_oauth_states.sql` | unchanged |
| `pockets/authentication/stores/pgx/totp.go:203` (store SQL, not a migration) | `gen_random_uuid()::text` → `uuidv7()::text` |
| `pockets/cms/stores/pgx/migrations/0009_terms.sql`, `0013_menus.sql`, `0014_menu_items.sql`, `0016_assets.sql`, `0017_inquiries.sql`, `0018_entries.sql` | `id TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text`; R5 `parent_id`/`menu_id` |
| `pockets/cms/stores/pgx/migrations/0020_entry_fields.sql`, `0021_entry_terms.sql` | R5 referencing columns only |
| `pockets/jobs/stores/pgx/migrations/0001_job_queue.sql`, `0003_fenced_job_queue.sql` | `job_id TEXT COLLATE "C" NOT NULL DEFAULT uuidv7()::text` (additive: every store insert supplies `job_id`; no `DEFAULT VALUES`/`COPY` path) |
| `pockets/jobs/stores/pgx/migrations/0002_job_schedules.sql` | `schedule_id TEXT COLLATE "C" NOT NULL DEFAULT uuidv7()::text`; R5 `last_job_id` |
| `pockets/jobs/stores/pgx/migrations/0004_job_schedule_occurrences.sql` | `job_id TEXT COLLATE "C" PRIMARY KEY` (deterministic id — collation only); R5 `schedule_id` |
| `pockets/authorization/stores/pgx/migrations/*`, `tuple_cache_migrations/*` | unchanged: `iam_audit.id`/`event_id` already `COLLATE "C"`; NO default (a default would mint outside the `eventID:n` grouping) |
| `pockets/events/stores/pgx/migrations/*`, `pockets/secrets/stores/pgx/migrations/*` | unchanged |

That is 18 canonical pgx files for the generated-id columns (8 authentication, 6 cms, 4 jobs) plus
~13 more under R5 (a). The `schema_decoy_test.go:29` fixture keeps its `gen_random_uuid()`.

**Collation facts (corrected by the gate).** PostgreSQL's combination rule: an expression mixing one
non-default implicit collation (`"C"`) with default-collation operands resolves to `"C"` without
error; the "could not determine which collation" error needs TWO different non-default collations.
The bundled stores already compare `"C"` and default columns today and live CI is green
(`stores/pgx/user_admin.go:64`, `:195`; `erasure.go:136`; `global_sessions.go:81`; `oauth2.go:197`;
`cms/stores/pgx/entries.go:224`; `jobs/stores/pgx/occurrences.go:92`). A foreign key between
deterministic collations is legal on PG18 (`entry_fields`/`entry_terms REFERENCES entries(id)`).
The reasons to collate the referencing columns (R5) are: (i) the planner matches an index's
collation by OID, so a default-collation index on the referencing side cannot serve a join key that
resolves to `"C"` — even on a C-locale database; (ii) `ORDER BY` over referencing columns
(`entries.go:313` `ORDER BY term_id`, `menus.go:146` `ORDER BY parent_id`) sorts mixed-case NanoIDs
under the cluster collation, away from the memory store's byte order.

### D4 — PostgreSQL 18 floor: refuse at migrate, warn at `Open` (R8 (a))

In `integrations/datastores/pgxdb` (branch from `8c66ea16` or later: v0.16.0's `TransactWithSettings`
/ `(*Tx).Setting` are already there and untouched):

- `const MinServerVersionNum = 180000`; `var ErrServerVersion = errors.New("pgxdb: PostgreSQL 18 or later is required")`.
- `RunMigrations` reads `current_setting('server_version_num')::int` and `server_version` ON THE
  MIGRATING CONNECTION (inside `InTx`, before `prepareSchema`) and returns, before touching the
  ledger: `pgxdb: PostgreSQL 18 or later is required: server reports "17.4 (Homebrew)" (server_version_num 170004 < 180000); the canonical pgx migrations use uuidv7() (PostgreSQL 18). Upgrade the server, or hold pgxdb at v0.16.x and the pgx stores at their previous tags (RELEASING "<section>")`,
  wrapping `ErrServerVersion`. No host, user, database or DSN in the text.
- `Open` runs the same probe after the connectivity check on BOTH paths (the retry path returns
  early — cover it) in every `RuntimeMode` including zero; the "mode-blind zero value" doc comment
  on `Config.RuntimeMode` is amended ("posture refusals are mode-gated; the version advisory logs in
  every mode"). Below the floor it logs ONE `WARN` through `cfg.Logger` (or `slog.Default()`) with
  stable attrs `server_version_num`, `required=180000`, `server_version`, msg "pgxdb: server below
  the PostgreSQL 18 floor; RunMigrations will refuse" — alert on the attrs, not the text. A probe
  failure (statement timeout, a PG-compatible backend) NEVER fails `Open`: one WARN with the error
  passed through the existing redaction (`redact.go`).
- The probe is one unexported helper over the package querier so a unit test fakes it; a live leg
  (`POSTGRES_TEST_DSN`, ≥ 18) asserts migrate succeeds; `POSTGRES_BELOW_FLOOR_TEST_DSN` (the owner's
  Homebrew PG17 locally; a `postgres:17` service in `live-stores.yml` job B so the leg does not rot)
  asserts `ErrServerVersion`.
- Runtime never needs PG18 except one path: the only store SQL that mints is `stores/pgx/totp.go:203`,
  which becomes `uuidv7()` — after PR-2b the recovery-code store path is PG18-only at runtime. PR-2b
  adds a Makefile grep guard that store SQL outside `migrations/` calls `uuidv7()` ONLY at that one
  allow-listed site, so the record can state precisely what runs on 17.
- The workshop `gopernicus db` verb does not link pgxdb (G11); nothing to change there.

Infra and docs moving 17 → 18 (task-3 carries the full list and a widened grep):
`Makefile` (lines 88/112), `.github/workflows/live-stores.yml` (images at :52/:105 — keep Debian
`postgres:18`, not `-alpine`, so the non-C-locale collation proof survives — and the comments at
:3/:75; plus the new PG17 service for the negative leg), pgxdb test headers
(`live_test.go:28`, `limiter_live_test.go:6`, `timezone_live_test.go:40`), `integrations/datastores/pgxdb/README.md`
(a floor paragraph; the pg_catalog function list gains `uuidv7()`; the PG17 mention near the end),
every pgx store README's docker line and "PostgreSQL 14 or later" sentence
(`pockets/{authentication,cms,jobs,events,secrets}/stores/pgx/README.md`,
`pockets/authentication/stores/turso/README.md:163`, `pockets/jobs/README.md:267`, `pockets/secrets/README.md:128`),
`pockets/*/stores/pgx/conformance_test.go` headers, `examples/production-baseline/README.md:141,144`,
`ARCHITECTURE.md`'s pgxdb line, `workshop/documentation/docs/guides/database-posture.md:225`, the
orphaned `.github/scripts/authorization_cache_verify.py:355` default `--postgres-bin`. Historical
records (`NOTES.md`, `AUDIT.md`, `BENCHMARKS.md`, past plans, RELEASING history) are not rewritten.
Scaffold TEMPLATES move in PR-6 together with their pins (see D6). Local dev: a disposable
`postgres:18` container on a free port (`lsof -i :<port>` first; never 55432, 8082 or 5436), with a
native fallback budgeted (`brew install postgresql@18`, a scratch data dir) because Docker Desktop
has hung on past trains; Homebrew PG17 stays for the negative leg.

### D5 — Ledger reconciliation under guard P9: greenfield re-export; live-data path deferred

P9 (`workshop/gopernicus/internal/commands/posture_rules.go:1302`) requires a byte-identical copy of
every file the pinned store module ships, matched by name-after-prefix, and `pgxdb.RunMigrations`
refuses a file whose bytes differ from the applied checksum. Editing canonical `CREATE` files
therefore means:

- **Every host in scope is greenfield** (owner ruling): a host that already applied the old files
  drops and recreates the database, re-exports, and P9 is green. **Renumbered ledgers never
  re-export** (`ExportMigrations` writes canonical names, which would duplicate a suffix beside
  `0032_passkey_credentials.sql`); they copy the new bytes over their own filenames.
  `examples/production-baseline/workshop/migrations/primary/` is regenerated that way (0007, 0008,
  0010, 0011, 0012, 0013, 0032, 0033, plus the R5 files); `make guard` (G32, P9 over every example)
  proves it.
- **Defer is the supported state for a host with applied old files that cannot recreate:** stay on
  the old store pins (and pgxdb ≤ v0.16.0, because any newer pgxdb refuses to migrate on PG17 and
  the new store tags' ledgers would mismatch). The record says so in one row. A live-data upgrade
  path ships when a host needs it (see "Deferred: live-host upgrade protocol").
- The record prints the sha256 of every edited canonical file (`shasum -a 256`) so a future
  upgrade plan — and any host comparing ledgers — has the before/after pairs.

### D6 — Scaffold, pins and the between-PR state

All `workshop/gopernicus` changes land in PR-6: `templates/pocket/stores/pgx/migration.sql.tmpl`
(`TEXT COLLATE "C" … DEFAULT uuidv7()::text`, comment rewritten), `readme.md.tmpl` /
`conformance_test.go.tmpl` docker lines, `templates/pocket/service.go.tmpl` (`ids.Or(sdk.UUIDv7())`
with the class-default comment — needs the sdk v0.21.0 pin), the pins in
`internal/commands/init.go:105` (`ConnectorVersion = "v0.17.0"`; today `"v0.15.0"` — #237 did not
move it), `templates/pocket/core.go.mod.tmpl:9`, `templates/pocket/stores/pgx/go.mod.tmpl:7-8`,
`templates/pocket/stores/turso/go.mod.tmpl:8`, `templates/init/go.mod.tmpl:5`,
`templates/init/env.example.tmpl` ("PostgreSQL 18 or later" comment), and a scaffold test asserting
the emitted pgxdb pin is ≥ v0.17.0 whenever the migration template emits `uuidv7()`. Keeping them
together means `main` never scaffolds PG18 DDL on a pre-floor connector pin. Tag
`workshop/gopernicus/v0.10.0` — a minor because the emitted DDL now requires PG18; the record says
"no rule-set change; hosts pinning the guard need not bump".

## Schema / datastore impact

- pgx: 18 canonical `CREATE` files change bytes for generated-id columns, ~13 more under R5 (a); no
  new files; no destructive statement; one store SQL expression (`totp.go:203`). turso: nothing.
- Store adapter parity: storetest already proves "empty id → non-empty id read back" for every
  DatabaseID-capable table and "supplied id stored verbatim" for service accounts, API keys,
  security events, invitations, identifiers, grants, cms entries and jobs queue/fenced (gate Q6
  list). Genuinely new verbatim cases: challenges, contact changes, passkeys, recovery codes, cms
  terms/menus/menu items/assets/inquiries. New option cases: jobs `WithIDs` reaches job and schedule
  ids; authorization `WithIDs` reaches `event_id`; empty-id rejection in jobs stores and
  `NewRecordsWith`. v7-SHAPE assertions live in pgx live tests only (turso backstops and memory
  reference ids are not v7).
- The jobs and authorization storetest factories (`RunQueue(t, func(*testing.T) …)`,
  `RunAudit(t, func(*testing.T, bool) …)`) cannot pass a store option; each gains a factory variant
  taking an `sdk.IDGenerator`.
- EAV spine: untouched. Keyset pagination: cms id tiebreaks now compare under `"C"`; the cms
  storetest ordering cases use explicit lowercase ids, so a `POSTGRES_NON_C_TEST_DSN` leg modelled on
  `authentication/stores/pgx/collation_test.go:194-251` is what proves the cms ordering fix.

## Module / API impact

Released state this plan builds on: sdk v0.20.0; pgxdb v0.16.0 (#237, RLS primitives) and v0.15.0;
datastores/turso v0.10.0; authentication v0.32.0, stores pgx v0.16.0 / turso v0.15.0; cms v0.6.0,
stores pgx v0.5.0 / turso v0.4.0; jobs v0.7.0, stores pgx v0.7.0 / turso v0.6.0; authorization
v0.31.0, stores pgx v0.22.0 / turso v0.21.0 / goredis v0.6.0; workshop v0.9.2. Every version below
was checked against `origin` tags on 2026-10-27 and is the next free minor.

| Module | Change | Bump |
|---|---|---|
| `sdk` | `UUIDv7()`, `IDGenerator.Or`, doc on the zero value | minor → **v0.21.0** |
| `integrations/datastores/pgxdb` | `ErrServerVersion`, `MinServerVersionNum`, migrate refusal, `Open` advisory, README | minor → **v0.17.0** (v0.16.0 = #237 RLS primitives) |
| `pockets/authentication` | per-class defaults, `recordIDs`, `NewRecorder` resolves the record default, `ApplyVerifiedChangeInput.ID`, provisioning template id, R3 literals, pin tests, storetest cases | minor → **v0.33.0** |
| `pockets/authentication/stores/pgx` | 8 migration edits (+R5), `totp.go:203`, `ApplyVerifiedChange` honors the id, collation inventories, live default test, EXPLAIN check; repins core v0.33.0 + pgxdb v0.17.0 | minor → **v0.17.0** |
| `pockets/authentication/stores/turso` | `ApplyVerifiedChange` honors the id; `tursodb.MapError` on the supplied-id branches of `challenges.go` and `contact_changes.go` (R3 makes those paths live; today a `(purpose, secret_digest)` collision there would escape the `ErrAlreadyExists` contract); repins core | minor → **v0.16.0** |
| `pockets/cms` | `cfg.IDs.Or(sdk.UUIDv7())`, wiring test, storetest cases, storage-key pin | minor → **v0.7.0** |
| `pockets/cms/stores/pgx` | 6 migration edits (+R5), hermetic + catalog collation tests, live default test; repins | minor → **v0.6.0** |
| `pockets/cms/stores/turso` | no change | not tagged |
| `pockets/jobs` | `jobs.WithIDs`, core minting on four paths, `schedules.Ensure.ID`, `stores/memory` rejects empty ids and drops `newID`, storetest cases + factory variant | minor → **v0.8.0** |
| `pockets/jobs/stores/pgx` | `newID` dropped, empty id rejected, 4 migration edits (+R5), collation tests, raw-SQL default test; `postgres.go:9-12` naming comment updated; repins | minor → **v0.8.0** |
| `pockets/jobs/stores/turso` | `newID` dropped, empty id rejected; repins | minor → **v0.7.0** |
| `pockets/authorization` | `audit.NewRecordsWith` (additive), empty-mint rejection, memory default UUIDv7, storetest cases + factory variant | minor → **v0.32.0** |
| `pockets/authorization/stores/pgx`, `…/turso` | `WithIDs` → `NewRecordsWith`; repins core | minor → **v0.23.0** / **v0.22.0** |
| `pockets/authorization/stores/goredis` | untouched (test-only store requires; standing note) | not tagged |
| `integrations/cryptids/google-uuid` | README sentence only | not tagged |
| `workshop/gopernicus` | templates, pins, scaffold test (D6) | minor → **v0.10.0** |
| `pockets`, `ui/goth`, views modules, events, secrets, other integrations | untouched | — |

**Lockstep.** Compile-level: none — `Or`, `NewRecordsWith`, the new store options, the additive
input fields and the jobs core option are all additive, so released stores build against the new
cores (Set A proves it). Behavioural: the old authentication turso store with the new core keeps
database-minted ids on the two identifier paths, hence its tag. **Runtime (Risk 5):** once ANY tag
a host takes selects pgxdb ≥ v0.17.0 through MVS, `RunMigrations` refuses on PG17 even with
nothing pending — for events, secrets and authorization stores too; the record's "Read first" box
says so and records a Set A NEGATIVE verbatim (a PG17 migrate under pgxdb v0.17.0). The pgx stores
repin pgxdb v0.17.0 explicitly so the error is the named one. The QuerierFrom authorization floor
(pgxdb ≥ v0.15.0 ⇒ authorization pgx ≥ v0.22.0) still applies. R11: there is no PG17 maintenance
line — the record says "upgrade to 18, or stay on pgxdb ≤ v0.16.0 and the previous store tags".
RELEASING "Version bumps": every change is a minor. There is no `sdk/CHANGELOG`; RELEASING is the
record.

## Generated-artifact impact

None (`*_templ.go` untouched). Scaffold `.tmpl` files are sources; the scaffold-compile tests in
`make check` cover them.

## Risks

1. **Entropy drop on record ids** (≈119/122 random bits → 62 random bits + a public timestamp),
   including the R3 rows. No record-class id is a bearer capability: challenges redeem by
   `secret_digest`, grants are consumed by `(session, purpose, context_digest)` and no inbound
   surface exposes a grant id (grepped), inquiries/security events/identifiers are opaque keys. PR-2a
   adds a review assertion in the pin-test file naming this invariant.
2. **Behaviour change for hosts without `WithIDs`:** identifiers, API keys, security events, cms
   records, jobs and schedules switch from NanoIDs/`job_` ids to UUIDv7s (URLs, logs, dashboards
   keyed on the `job_`/`sched_` prefixes). Hosts WITH `WithIDs(googleuuid.V7())` (battleboard) see no
   change — they already get v7 everywhere through `cfg.IDs` today; the note only says that dropping
   the option now yields NanoID users.
3. **A host with applied old files and no fresh-database option** has no in-release path (the
   live-host protocol is deferred by ruling); the record names "defer" as the supported state and
   the sha256 pairs are published so the deferred plan can start from facts.
4. **Host-side shape assumptions:** `VARCHAR(21)`/length CHECKs, route regexes or validators on
   pocket ids fail with 22001/404 on 36-char ids; mixed JWT `sub` shapes exist for any host that
   changes its users' generator (not battleboard, not gps). The repo validates no id shape (grepped).
5. **PG17 drift:** a stale long-lived test database carries old checksums (use fresh databases for
   the live legs — the pgxdb `live_test.go:68` follow-up); the MVS runtime lockstep above; the
   server-upgrade mechanics in the record (PG18 `initdb` enables data checksums by default, so
   `pg_upgrade` from a non-checksummed 17 cluster needs `--no-data-checksums` or checksums enabled
   first; the official `postgres:18` image moved `PGDATA` to a versioned subdir under
   `/var/lib/postgresql`, so a compose volume mount of `…/data` does not carry over and an image-tag
   change never upgrades a data dir; `pg_dump`/`pg_restore` clients must be ≥ 18; replicas/DR
   targets must be 18; an 18 dump will not restore onto 17 because of `uuidv7()` defaults).
6. **Jobs `Ensure` mints a candidate on every call** (create-only use); harmless for UUIDv7, burns
   ids from a host's counter-style generator — one doc line on `jobs.WithIDs`.

## Tasks

Worktrees: one per PR at `../gopernicus-default-ids-<n>`, branched from `origin/main` @ `8c66ea16`
or later. Every PR: `make guard && make check`; live legs on a disposable `postgres:18`
(`POSTGRES_TEST_DSN` bare and `POSTGRES_TEST_SCHEMA=ids`), turso legs `-tags=integration` on a local
file. Commit trailer per the session's attribution line. The owner merges; Claude cuts and pushes
the tags after a green `live-stores` dispatch on `main` at the squash commit, confirms them on
`origin`, then cold-verifies. Tests that exercise `WithIDs` use a COUNTING generator (`fixed-1`,
`fixed-2`, …) and assert the prefix — a constant generator collides on primary keys (recovery-code
sets, several security events per flow, audit row ids, two enqueues) and defeats the
contact-change stale-request guard (`identifier_management.go:161-178`).

### PR-1 — sdk + pgxdb + infra (the floor and the primitives)

#### task-1: `sdk.UUIDv7` and `IDGenerator.Or`

- **depends_on:** []
- **model:** opus
- **files:** `sdk/id.go`, `sdk/id_test.go`, `sdk/README.md` (root table row)
- **verify:** `cd sdk && go build ./... && go test ./... -race && go vet ./...`
- **description:** Add `UUIDv7()` per D1 (sub-ms fraction, clamp inside the closure over the
  combined 60-bit value, 62 random bits, no error path) and `Or` per D2 with the zero-value
  contract on both docs. Tests: 36-char lowercase canonical form, version nibble `7`, variant
  `10`, 1e5 mints unique, 1e4 consecutive mints from one generator strictly increasing as text,
  two generators each monotonic, `Or` returns the configured generator when `Func != nil` and the
  fallback otherwise, zero value still emits NanoIDs.

#### task-2: pgxdb PostgreSQL 18 floor

- **depends_on:** [task-1]
- **model:** opus
- **files:** `integrations/datastores/pgxdb/migrate.go`, `integrations/datastores/pgxdb/postgres.go` (both `Open` paths; the `RuntimeMode` doc comment), new `integrations/datastores/pgxdb/server_version.go` + `server_version_test.go`, `integrations/datastores/pgxdb/migrate_live_test.go` (floor legs), `integrations/datastores/pgxdb/live_test.go:28`, `limiter_live_test.go:6`, `timezone_live_test.go:40` (headers), `integrations/datastores/pgxdb/README.md` (floor paragraph; pg_catalog list; the PG17 mention)
- **verify:** `cd integrations/datastores/pgxdb && go build ./... && go test ./... -race && go vet ./...`; live on postgres:18: `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:<port>/postgres?sslmode=disable' go test ./... -race -count=1`; negative: `POSTGRES_BELOW_FLOOR_TEST_DSN=<Homebrew PG17 DSN> go test ./... -run 'ServerVersion|Floor' -count=1`
- **description:** Implement D4 (probe on the migrating connection; refusal text per D4;
  non-fatal redacted WARN on both `Open` paths with stable attrs). The pgxdb `go.mod` keeps its
  sdk pin. Nothing in #237's `TransactWithSettings` / `(*Tx).Setting` is touched.

#### task-3: CI, Makefile and docs move to PostgreSQL 18

- **depends_on:** [task-2]
- **model:** sonnet
- **files:** `Makefile` (:88, :112), `.github/workflows/live-stores.yml` (images :52/:105 → `postgres:18`; comments :3/:75; a `postgres17` service on another published port in job B with `POSTGRES_BELOW_FLOOR_TEST_DSN` wired to the pgxdb step), `pockets/{authentication,cms,jobs,events,secrets}/stores/pgx/README.md`, `pockets/authentication/stores/turso/README.md:163`, `pockets/jobs/README.md:267`, `pockets/secrets/README.md:128`, `pockets/secrets/stores/pgx/README.md:6`, `pockets/*/stores/pgx/conformance_test.go` headers, `examples/production-baseline/README.md:141,144`, `ARCHITECTURE.md` (pgxdb line), `workshop/documentation/docs/guides/database-posture.md:225`, `.github/scripts/authorization_cache_verify.py:355`
- **verify:** `make guard && make check`; `grep -rn -iE 'postgres(ql)?[ :@]?1[3-7]\b|PG ?1[3-7]\b|1[34] or later|postgresql@1[3-7]' --include='*.md' --include='*.yml' --include='*.yaml' --include='*.tmpl' --include='*.go' --include='*.py' --include='Makefile' . | grep -v -E '^./(plans/|NOTES.md|AUDIT.md|RELEASING.md|pockets/authorization/BENCHMARKS.md|workshop/gopernicus/internal/commands/templates/)'` returns nothing (templates move in PR-6)
- **description:** Every pin and sentence in D4's list says 18. No workshop module code or
  template changes here (D6). PR description notes that between PR-1 and PR-6 `main` still
  scaffolds `gen_random_uuid()` DDL (consistent with its pgxdb v0.15.0 scaffold pin).

### PR-2a — authentication core

#### task-4: per-class generators in the authentication core

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/authentication/logic/authentication/service.go` (:448 fallback recorder, :487 fields), `…/constructor.go:235`, `…/external_config.go`, `…/external.go` (:202, :836, :851), `…/helpers.go`, `…/securityevent/recorder.go:165` (`ids.Or(sdk.UUIDv7())`), `…/passkey_ceremony.go:284`, `…/totp_enrollment.go:320`, `…/machine.go` (`:110` stays `s.ids`; `:195` → `recordIDs`), `…/provisioning.go:104` (`NewIdentifier.ID`), `…/identifier/repository.go` (`ApplyVerifiedChangeInput.ID`), `…/identifier_management.go` (fill `ApplyVerifiedChangeInput.ID`; set `.ID` on the `contactchange.New` result), `…/challenge.go:225`, `…/stepup.go:401` (literal `.ID` via `Generate()` + error; NOT `:176`), `…/oauth2/service.go:99` (unchanged literal now resolves inside `NewRecorder`; comment says why), `pockets/authentication/logic/invitations/constructor.go:89` (no change needed; comment), `pockets/authentication/constructor.go:546`, `pockets/authentication/config.go` (`IDs` doc: the two classes; the stale-request guard now relies on generator uniqueness), `pockets/authentication/README.md`, `pockets/authentication/go.mod` (sdk v0.21.0)
- **verify:** `cd pockets/authentication && go build ./... && go test ./... -race && go vet ./...`; `grep -rn 'IDGenerator{}' pockets/authentication/logic --include='*.go' | grep -v _test.go` lists ONLY the six excluded files
- **description:** Implement D2 for authentication. Route every `IDGenerator` that reaches a mint —
  `s.ids`, `o.ids`, `e.ids` AND `d.IDs` — through the right class; add the two missing identifier
  ids; no new public option; no constructor signature changes (`contactchange.New` stays).

#### task-5: authentication wiring and exclusion pin tests

- **depends_on:** [task-4]
- **model:** opus
- **files:** `pockets/authentication/ids_wiring_test.go` (new; drive the public constructor with the existing `constructor_fixture_test.go` fakes), `pockets/authentication/logic/authentication/ids_classes_test.go` (new), `…/session/session_test.go`, `…/oauthstate/oauthstate_test.go`, `…/machine_test.go`, `…/resend_test.go`, `pockets/authentication/logic/invitations/secrets_pin_test.go` (new), `pockets/authentication/logic/maintenance/service_test.go`, `…/oauth2/service_test.go`
- **verify:** `cd pockets/authentication && go test ./... -race -count=1`
- **description:** (a) Wiring with a counting generator: with no `WithIDs`, user / service account /
  invitation ids are 21 chars of `sdk.DefaultIDAlphabet` and identifier (registration, OAuth,
  external, passwordless-provisioning, confirmed-change) / API key / security event (core, external,
  invitations, OAuth2 fallback) / passkey / recovery code / challenge / grant / contact-change ids
  are canonical uuids with version nibble `7`; with `WithIDs(counting)` every one of those carries
  the prefix. (b) Pins: each excluded site mints a non-empty value unchanged under
  `WithIDs(sdk.NewIDGenerator(sdk.DatabaseID))` — session ids and refresh tokens, OAuth state,
  API-key prefix/secret, resend generation, invitation secret AND `mintOperationID`, archive keys;
  plus the Risk 1 assertion comment. (c) The synthetic grant at `stepup.go:176` has an empty id.

### PR-2b — authentication stores (pgx + turso) — after `pockets/authentication/v0.33.0` is on the proxy

#### task-6: authentication pgx migrations, store SQL, collation inventories, storetest cases

- **depends_on:** [task-5]
- **model:** opus
- **files:** `pockets/authentication/stores/pgx/migrations/{0007_api_keys,0008_security_events,0010_user_identifiers,0011_challenges,0012_contact_changes,0013_authentication_grants,0025_passkey_credentials,0026_totp}.sql`, plus under R5 `{0002_user_passwords,0003_sessions,0004_oauth_accounts,0006_service_accounts,0019_oauth2_sessions,0020_external_subjects,0027_service_account_status}.sql`, `pockets/authentication/stores/pgx/totp.go:203`, `pockets/authentication/stores/pgx/identifiers.go` (`ApplyVerifiedChange` inserts `input.ID` when set), `pockets/authentication/stores/turso/identifiers.go` (same), `pockets/authentication/stores/turso/challenges.go` + `contact_changes.go` (`tursodb.MapError` on the supplied-id branches), `pockets/authentication/stores/pgx/collation_test.go` (a SECOND inventory `generatedIDColumns` — and a third, `referencingIDColumns`, under R5 — iterated by the hermetic and catalog tests; the contractual-keyset inventory keeps its meaning), new `pockets/authentication/stores/pgx/id_default_live_test.go` (reuse `probeDial`/`probeResetSchema`), `pockets/authentication/stores/storetest/storetest.go` (+ `challenge_grant_regressions.go`, `passkeys.go`, `totp.go`: verbatim cases for challenges, contact changes, passkeys, recovery codes; the confirmed-change identifier carries the supplied id; the DatabaseID + app-minted dual-leg pattern at `:252`), `Makefile` (the `uuidv7()`-outside-migrations grep guard allow-listing `totp.go`), `pockets/authentication/stores/pgx/README.md`, both store `go.mod`s (core v0.33.0; pgx also pgxdb v0.17.0, sdk v0.21.0 — explicit `GOWORK=off go get`, then tidy)
- **verify:** FIRST the R5 gate: on a fresh postgres:18 with the current ledger applied, `EXPLAIN` the directory join (`user_admin.go:58-69`) before and after `ALTER TABLE user_identifiers ALTER COLUMN user_id TYPE TEXT COLLATE "C"`; record both plans in the PR; if unchanged, drop the referencing-column edits (R5 → generated-id columns only) before continuing. Then `cd pockets/authentication/stores/pgx && go build ./... && go vet ./... && POSTGRES_TEST_DSN=… go test ./... -race -count=1 && POSTGRES_TEST_SCHEMA=ids POSTGRES_TEST_DSN=… go test ./... -race -count=1 && POSTGRES_NON_C_TEST_DSN=… go test ./... -run Collation -count=1`; `cd pockets/authentication/stores/turso && TURSO_DATABASE_URL=file:… TURSO_AUTH_TOKEN=local go test -tags=integration ./... -count=1`; `GOWORK=off go build ./...` in both store modules; `make guard && make check`
- **description:** Apply D3 for authentication (no `0001`). The live test inserts through raw SQL
  with the id omitted for every record-class table and asserts version nibble `7`; the store-path
  recovery-code insert with an empty id yields a v7 (catches `totp.go:203`); `collation_name = 'C'`
  for every column in every inventory.

### PR-3a / PR-3b — cms core, then cms pgx store

#### task-7: cms class default and wiring test (core)

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/cms/cms.go`, `pockets/cms/ids_wiring_test.go` (new), `pockets/cms/storetest/storetest.go` (verbatim cases for terms, menus, menu items, assets, inquiries), `pockets/cms/domain/media/asset_test.go` (storage-key pin), `pockets/cms/README.md`, `pockets/cms/go.mod`
- **verify:** `cd pockets/cms && go test ./... -race -count=1`; `cd pockets/cms/stores/turso && TURSO_DATABASE_URL=file:… go test -tags=integration ./... -count=1` (workspace; the new storetest cases pass on the unchanged turso store)
- **description:** `cfg.IDs.Or(sdk.UUIDv7())` into the five services; the wiring test with a
  counting generator through the public `New`.

#### task-8: cms pgx migrations and collation tests (store) — after `pockets/cms/v0.7.0`

- **depends_on:** [task-7, task-6]
- **model:** opus
- **files:** `pockets/cms/stores/pgx/migrations/{0009_terms,0013_menus,0014_menu_items,0016_assets,0017_inquiries,0018_entries}.sql` (+ R5, if it survived task-6's gate: `0020_entry_fields`, `0021_entry_terms`), new `pockets/cms/stores/pgx/collation_test.go` (hermetic DDL + catalog + a `POSTGRES_NON_C_TEST_DSN` ordering case with mixed-case entry ids) and `id_default_live_test.go`, `pockets/cms/stores/pgx/README.md`, `pockets/cms/stores/pgx/go.mod` (core v0.7.0, pgxdb v0.17.0)
- **verify:** `cd pockets/cms/stores/pgx && POSTGRES_TEST_DSN=… go test ./... -race -count=1` (bare + `POSTGRES_TEST_SCHEMA=ids` + non-C); `GOWORK=off go build ./...`; `make guard && make check`; run-and-look: `cd examples/cms && make run`, create an entry and a term in the admin, confirm the ids in the URLs are 36-char uuids with `7` at index 14
- **description:** D3's six (eight) files; the live default test as in task-6. Depends on task-6
  only for the R5 gate outcome.

### PR-4a / PR-4b — jobs core, then jobs stores

#### task-9: jobs core minting (`jobs.WithIDs`)

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/jobs/options.go`, `pockets/jobs/jobs.go`, `pockets/jobs/logic/queue/service.go` (:84-98), `pockets/jobs/logic/queue/work.go` (:63, :85), `pockets/jobs/logic/schedules/schedule.go` (`Ensure.ID`, create-only), `pockets/jobs/logic/schedules/service.go:119`, `pockets/jobs/stores/memory/{queue.go,fenced.go,schedules.go}` (drop `newID`; reject empty id with `sdk.ErrInvalidInput`), `pockets/jobs/stores/storetest/*.go` (factory variant taking `sdk.IDGenerator`; `WithIDs` reaches job AND schedule ids; empty id → `ErrInvalidInput`, never `ErrAlreadyExists`; `OccurrenceID` deterministic and ignores `WithIDs`), `pockets/jobs/ids_wiring_test.go` (new, through `jobs.New` + the memory store), `pockets/jobs/README.md`, `pockets/jobs/go.mod`
- **verify:** `cd pockets/jobs && go test ./... -race -count=1`; `cd examples/jobs-minimal && go build ./... && go test ./...` then run it and enqueue one job — the log shows a 36-char uuid job id
- **description:** D2 for jobs: the core mints on all four paths; stores never mint.

#### task-10: jobs pgx + turso stores — after `pockets/jobs/v0.8.0`

- **depends_on:** [task-9, task-6]
- **model:** opus
- **files:** `pockets/jobs/stores/pgx/{postgres.go (:9-12 comment),queue.go,fenced.go,schedules.go,helpers.go}`, `pockets/jobs/stores/turso/{helpers.go,queue.go,fenced.go,schedules.go}`, `pockets/jobs/stores/pgx/migrations/{0001_job_queue,0002_job_schedules,0003_fenced_job_queue,0004_job_schedule_occurrences}.sql` (+ R5 columns in 0002/0004 if the gate held), new `pockets/jobs/stores/pgx/collation_test.go` and `id_default_live_test.go` (raw-SQL insert with `job_id`/`schedule_id` omitted → v7; the store never omits them), `pockets/jobs/stores/{pgx,turso}/README.md`, both `go.mod`s (core v0.8.0; pgx also pgxdb v0.17.0)
- **verify:** `cd pockets/jobs/stores/pgx && POSTGRES_TEST_DSN=… go test ./... -race -count=1` (bare + schema); `cd pockets/jobs/stores/turso && TURSO_DATABASE_URL=file:… go test -tags=integration ./... -count=1`; `GOWORK=off go build ./...` in both; `make guard && make check`
- **description:** Drop `newID`, reject empty ids, D3's four files. No turso DDL.

### PR-5a / PR-5b — authorization core, then authorization stores

#### task-11: `audit.NewRecordsWith` (core)

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/authorization/logic/audit/audit.go`, `pockets/authorization/logic/audit/audit_test.go`, `pockets/authorization/stores/memory/audit.go`, `pockets/authorization/stores/storetest/audit.go` (factory variant with an `sdk.IDGenerator`; `WithIDs(counting)` → every `event_id` of one write carries the prefix and row ids are `<id>:000…1`, `…2`; default is a v7 uuid; empty mint → `ErrInvalidInput`; the existing "one write, one event_id" case stays), `pockets/authorization/README.md`, `pockets/authorization/go.mod`
- **verify:** `cd pockets/authorization && go test ./... -race -count=1`
- **description:** D2 for authorization: explicit generator input, record default inside, empty
  mint rejected; memory passes the zero value.

#### task-12: authorization pgx + turso `WithIDs` — after `pockets/authorization/v0.32.0`

- **depends_on:** [task-11]
- **model:** opus
- **files:** `pockets/authorization/stores/pgx/{postgres.go,audit.go}`, `pockets/authorization/stores/turso/{turso.go,audit.go}`, both READMEs, both `go.mod`s (core v0.32.0; pgx stays on pgxdb v0.15.0 unless tidy lifts it — no migration here, so the floor is not forced through this store)
- **verify:** `cd pockets/authorization/stores/pgx && POSTGRES_TEST_DSN=… go test ./... -race -count=1` (bare + schema); `cd pockets/authorization/stores/turso && go test -tags=integration ./... -count=1`; `GOWORK=off go build ./...` in both; `make guard && make check`
- **description:** Thread `WithIDs` into `NewRecordsWith`. No migration change. goredis untouched.

### PR-6 — record, examples, scaffold, docs

#### task-13: release record and host upgrade notes

- **depends_on:** [task-3, task-6, task-8, task-10, task-12]
- **model:** opus
- **files:** `RELEASING.md` (one new top section), `SECURITY.md` (confirm no rows), `workshop/documentation/docs/{integrations pgxdb page, pockets/{authentication,cms,jobs,authorization}.md, guides/database-posture.md, guides/operations-runbooks.md, sdk page}` + `pnpm build`, `integrations/cryptids/google-uuid/README.md`, `plans/database-default-ids.md` (executed copy)
- **verify:** `cd workshop/documentation && pnpm build`; `make guard && make check`
- **description:** Write the record per §Release plan: tag table, MVS sets incl. the Set A NEGATIVE
  (PG17 migrate), "Not tagged", the "Read first" box (runtime lockstep; R11: no PG17 line), the
  per-host-shape rows (greenfield re-export; defer), the sha256 table of edited canonical files,
  the "Deferred: live-host upgrade protocol" note (copied from this plan), the server-upgrade notes
  (Risk 5), the behaviour-change rows, the R rulings as decided.

#### task-14: example repins, the baseline ledger, the scaffold

- **depends_on:** [task-13]
- **model:** opus
- **files:** `examples/production-baseline/go.mod` + `go.sum` (authentication v0.33.0, stores/pgx v0.17.0, authorization v0.32.0, stores/pgx v0.23.0, pgxdb v0.17.0, sdk v0.21.0), `examples/production-baseline/workshop/migrations/primary/{0007_api_keys,0008_security_events,0010_user_identifiers,0011_challenges,0012_contact_changes,0013_authentication_grants,0032_passkey_credentials,0033_totp}.sql` (+ the R5 files under the baseline's numbering, e.g. `0002_user_passwords`, `0003_sessions`, `0034_service_account_status`) as byte copies over the baseline's own numbering, `examples/production-baseline/.env.example` (PostgreSQL 18 comment), `examples/auth-cms/go.mod` (authentication v0.33.0, cms v0.7.0, jobs v0.8.0, authorization v0.32.0, pgxdb v0.17.0, sdk v0.21.0), `examples/cms/go.mod` (cms v0.7.0), `examples/minimal/go.mod` (cms v0.7.0), `examples/jobs-minimal/go.mod` (jobs v0.8.0), `examples/external-identity/go.mod` (authentication v0.33.0), `examples/goth-showcase/go.mod` (only if it pins a moved module), `workshop/gopernicus/internal/commands/init.go:105`, `workshop/gopernicus/internal/commands/templates/pocket/{core.go.mod.tmpl,service.go.tmpl,memory.go.tmpl,entity.go.tmpl,stores/pgx/{go.mod.tmpl,migration.sql.tmpl,readme.md.tmpl,conformance_test.go.tmpl},stores/turso/go.mod.tmpl}`, `workshop/gopernicus/internal/commands/templates/init/{go.mod.tmpl,env.example.tmpl}`, the scaffold tests (`scaffold_test.go:49` pins; the new "pgxdb ≥ v0.17.0 iff `uuidv7()`" assertion)
- **verify:** per example `GOWORK=off go build ./... && go test ./...` from an empty `GOMODCACHE` via `proxy.golang.org` (Set C); `make guard` (P9 over the baseline); production-baseline booted on a fresh postgres:18 with the regenerated ledger: register through the dev issuer, `psql` shows `users.id` as a 21-char NanoID and the security-event ids as v7 uuids; `make check` (scaffold-compile tests)
- **description:** Explicit `GOWORK=off go get <module>@<tag>` + `go mod tidy` per example (never
  `go work sync`); byte-copy the canonical files into the baseline ledger keeping its numbering;
  D6 in full; after `workshop/gopernicus/v0.10.0` is cut, a cold `gopernicus init --db pgx` /
  `--db turso` from `@v0.10.0` (the QuerierFrom task-12 shape).

## Sequencing

```
PR-1 (sdk, pgxdb, infra) ──► tags sdk/v0.21.0, pgxdb/v0.17.0 ──┐
   ├─► PR-2a auth core ──► tag authentication/v0.33.0 ──► PR-2b auth stores (R5 EXPLAIN gate) ──► tags auth pgx v0.17.0, auth turso v0.16.0
   ├─► PR-3a cms core  ──► tag cms/v0.7.0            ──► PR-3b cms pgx  (after PR-2b's gate) ──► tag cms pgx v0.6.0
   ├─► PR-4a jobs core ──► tag jobs/v0.8.0           ──► PR-4b jobs stores (after PR-2b's gate) ──► tags jobs pgx v0.8.0, jobs turso v0.7.0
   └─► PR-5a authz core ──► tag authorization/v0.32.0 ──► PR-5b authz stores ──► tags authorization pgx v0.23.0, turso v0.22.0
PR-6 (record, examples, scaffold) after every tag above ──► tag workshop/gopernicus/v0.10.0
```

The four core PRs are independent and may be built in parallel worktrees once PR-1's tags are on
the proxy; each store PR waits for its core tag (its `go.mod` must name a tag that exists, or the
tagged store's `go.sum` lacks the core's `h1:` lines and a `GOWORK=off` build of the store is red
forever — the S4d2 #221→#223 and QuerierFrom #233→#234 trains). PR-3b and PR-4b additionally wait
for PR-2b's R5 EXPLAIN result so all three store trains apply the same R5 outcome. Ten PRs,
thirteen tags.

## Release plan

**Tag protocol** (owner ruling 2026-10-27): the owner merges each PR; a `live-stores` dispatch on
`main` at the squash commit goes green; **Claude cuts the annotated, unsigned tags at that commit,
pushes them one at a time, confirms each on `origin`** (`git ls-remote --tags origin <tag>` and the
proxy `@v/<ver>.info`), then cold-verifies from a scratch consumer with `GOWORK=off`, an empty
`GOMODCACHE`, `proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build ./...`, a consumer
pinning `go 1.26.10`.

**Tag order:**

1. `sdk/v0.21.0`, `integrations/datastores/pgxdb/v0.17.0` (PR-1)
2. `pockets/authentication/v0.33.0` (PR-2a) → `pockets/authentication/stores/pgx/v0.17.0`, `pockets/authentication/stores/turso/v0.16.0` (PR-2b)
3. `pockets/cms/v0.7.0` (PR-3a) → `pockets/cms/stores/pgx/v0.6.0` (PR-3b)
4. `pockets/jobs/v0.8.0` (PR-4a) → `pockets/jobs/stores/pgx/v0.8.0`, `pockets/jobs/stores/turso/v0.7.0` (PR-4b)
5. `pockets/authorization/v0.32.0` (PR-5a) → `pockets/authorization/stores/pgx/v0.23.0`, `pockets/authorization/stores/turso/v0.22.0` (PR-5b)
6. `workshop/gopernicus/v0.10.0` (PR-6)

Tag messages name: the class defaults, the override, the migrations edited ("checksums changed —
greenfield re-export; see RELEASING"), the PG18 floor, and the pins each module requires.

**Cold-verify sets:**
- **Set A** (after step 1): sdk v0.21.0 + pgxdb v0.17.0 + the RELEASED pgx stores (authentication
  v0.16.0, cms v0.5.0, events v0.4.0, jobs v0.7.0, secrets v0.2.1, authorization v0.22.0) build.
  **Set A negative, recorded verbatim:** that consumer's `RunMigrations` against PG17 returns
  `ErrServerVersion` with nothing pending.
- **Set B** (per pocket step): the pocket core + its new stores + pgxdb v0.17.0 (+ datastores/turso
  v0.10.0 where a turso store is in the set).
- **gps-three-sixty shape** (after step 5): authentication v0.33.0 + pgx v0.17.0 + authorization
  v0.32.0 + pgx v0.23.0 + goredis v0.6.0 + jobs v0.8.0 + jobs pgx v0.8.0 + pgxdb v0.17.0 + sdk v0.21.0.
- **battleboardhq shape** (after step 5): the gps shape minus goredis plus google-uuid v0.2.0,
  golang-jwt v0.3.0, sendgrid v0.3.0, oauth/google v0.3.0.
- **Set C** (PR-6): every example at `GOWORK=off` from an empty cache.

**Upgrade notes by host shape** (the record's rows):

- **Greenfield:** PostgreSQL 18; `gopernicus init` at `@v0.10.0`; nothing else.
- **A host that already applied the old canonical files (no data):** bump the pins; drop and
  recreate the database(s); re-export the ledger(s) from the new store tags (renumbered ledgers
  copy bytes over their own names); `guard --posture` P9 green. **A live-data upgrade path is
  deferred until a host needs it; until then "defer" — staying on the old store pins and pgxdb
  ≤ v0.16.0 — is the supported state.**
- **gps-three-sixty** (authentication v0.31.0 / pgx v0.15.0, authorization v0.31.0 / pgx v0.21.0,
  jobs v0.7.0 / pgx v0.7.0, pgxdb v0.14.0; ledgers `workshop/migrations/{iam,jobs,primary}` over
  `db` + `iamDB`; no `WithIDs`; no real data): the row above, for both databases; the pgxdb bump
  drags the authorization floor (met by v0.23.0). New defaults apply with no code change.
- **battleboardhq** (bootstrapping, no data; `authentication.WithIDs(sdk.NewIDGenerator(googleuuid.V7()))`
  at `pockets/auth/outbound/authentication.go:233`; own tables already `COLLATE "C" DEFAULT uuidv7()::text`;
  `postgres:18-alpine`; ledgers `iam/`, `jobs/`, `battleboard/`): bump the pins; re-export `iam/` +
  `jobs/`; `make dev-reset`. No id change for this host (it already mints v7 everywhere); dropping
  the option would switch users/service accounts/invitations to NanoIDs. `jobs.WithIDs` and
  `authorization/stores/pgx.WithIDs` exist if it wants its own generator there too — never
  `sdk.DatabaseID` on those two (rejected with `ErrInvalidInput`).
- **gps-360-go / segovia:** rough draft and rebuilt from scratch respectively — greenfield rows.
- **Hosts on turso:** no DDL change; new ids arrive from Go; the authentication turso store tag
  carries the two identifier paths and the error-mapping fix.
- **PostgreSQL server move 17 → 18** (every pgx host): Risk 5 mechanics, in the record.

## Tests (summary by PR)

- PR-1: sdk UUIDv7 shape/version/variant/uniqueness/per-generator order; `Or`; pgxdb floor unit,
  live ≥ 18, negative on 17.
- PR-2a: wiring with a counting generator over every record path incl. the three gate rows; eight
  exclusion pins + the synthetic grant; OAuth2 fallback default.
- PR-2b: the R5 EXPLAIN gate; new verbatim cases; pgx live raw-SQL default → v7 incl. the
  recovery-code store path; every collation inventory hermetic + catalog + non-C ordering; turso
  suite green with the MapError fix exercised (a `(purpose, secret_digest)` collision maps to
  `ErrAlreadyExists`).
- PR-3a/3b: cms wiring, storage-key pin, verbatim cases, pgx hermetic/catalog/non-C/default tests;
  run-and-look.
- PR-4a/4b: `WithIDs` reaches job and schedule ids; empty id → `ErrInvalidInput`; `OccurrenceID`
  pin; raw-SQL default on `job_queue`/`fenced_job_queue`/`job_schedules`; jobs collation tests.
- PR-5a/5b: `WithIDs` reaches `event_id`; row ids keep `eventID:%020d`; default v7; empty mint rejected.
- PR-6: Set C; P9 over the baseline; the baseline booted on postgres:18 with the regenerated ledger.

## Deferred: live-host upgrade protocol (not in this release — owner ruling 2026-10-27)

Kept so the analysis is not lost. A future plan, triggered by the first host with data that needs
to move to the new store tags, would need:

- **The problem.** P9 requires byte-identical copies of the pinned store module's files;
  `pgxdb.RunMigrations` refuses a file whose bytes differ from the applied checksum (checked per
  file, in sorted order, inside one transaction). Edited canonical `CREATE` files therefore fail
  both checks on a host that applied the old bytes.
- **The shape that works (reviewed by platform-sre and data-integration on rev 3):** one deploy,
  idempotent, per `RunMigrations` stream (one per schema/database — e.g. gps-three-sixty's `iam/`
  and `jobs/` are two streams): (1) `0000_gopernicus_ids_restamp.sql` containing ONLY scoped
  `UPDATE schema_migrations SET checksum = '<new>' WHERE source = 'default' AND version = '<file>' AND checksum = '<old>';`
  rows for the edited files in that stream (renumbered ledgers substitute their `version`); it sorts
  before every pocket file, so it restamps first and the re-exported copies then verify; zero rows
  on a fresh database; a restamp in the wrong stream updates nothing and the next file fails
  loudly (the unqualified `schema_migrations` resolves through the stream's `search_path`).
  (2) The re-exported canonical copies. (3) `<next>_gopernicus_ids_upgrade.sql`:
  `SET LOCAL lock_timeout = '5s';` then per table `ALTER TABLE IF EXISTS <t> ALTER COLUMN <id> SET DEFAULT uuidv7()::text;`
  and `ALTER TABLE IF EXISTS <t> ALTER COLUMN <c> TYPE TEXT COLLATE "C";` — `text → text COLLATE "C"`
  does not rewrite the table but takes `ACCESS EXCLUSIVE` and rebuilds every index on the column
  (for `entries.id`, the PK the EAV tables reference); maintenance window; measure on a restored
  copy; in-flight keyset cursors may skip or repeat once. Roll forward only (the previous artifact's
  copies no longer match the restamped ledger). `raw_sql` is left as applied.
- **Why rev 2's two-run variant was rejected:** between the runs the released artifact could not
  re-migrate (second replica, crashloop, rollback) and P9 was red.
- **Evidence a future plan must produce:** a pgxdb live test (old set, then new copies + the 0000
  file in ONE run → success; second run → no-op; `WithSchema` variant; wrong-stream negative) and a
  drill on a real ledger (three green runs, verbatim).
- **Inputs it will have:** the sha256 pairs the record publishes for every edited canonical file;
  the pgxdb README's ledger-relocation section as the precedent for host-side `schema_migrations`
  surgery; a possible `gopernicus db restamp` verb that emits the 0000 file from a store module's
  old/new file pairs.

## Open rulings — YOUR CALL

_None._ R1, R3, R4, R5, R7, R8, R11 and R12 were ruled 2026-10-27 ("all recs"; recorded under
Owner rulings → "Rulings on rev 3"); R2, R6, R9, R10 were closed by the gate; the live-host
protocol was cut by owner ruling. The one runtime gate left is R5's `EXPLAIN` in PR-2b, which the
lead running the PR decides from the recorded plans.

## Decided by gate

Findings from the four reviews on rev 2 and the planner's final call on each (rev 2 → rev 3; rev 4
notes where the owner's cut changed the disposition):

| # | Finding (reviewer) | Decision |
|---|---|---|
| G1 | Two identifier rows stay database-minted under `WithIDs`: the passwordless provisioning template (`provisioning.go:104`) and the confirmed contact change (`ApplyVerifiedChangeInput` has no id) (backend, blocker) | Added to the inventory and to task-4/task-6: the template gets an id; `ApplyVerifiedChangeInput.ID` (additive); both stores honor it → the authentication turso store changes and is tagged (cms turso still untagged) |
| G2 | `WithIDs(fixed)` tests collide on primary keys and defeat the contact-change stale-request guard (backend, blocker; data) | Every test uses a counting generator and asserts the prefix |
| G3 | Two recorder fallbacks take `d.IDs` (`service.go:448`, `invitations/constructor.go:89`) and the task-4 grep misses them (backend) | The record default moves INTO `securityevent.NewRecorder`; `oauth2.WithIDs` dropped; task-4 names `d.IDs` |
| G4 | The zero value must mean "class default" at every mint, including store options and the audit builder (backend; steward nit) | `Or` is applied at the minting layer; the `Or`/`IDGenerator` docs state the zero-value contract and how to force NanoID for a class |
| G5 | An empty-id generator silently corrupts jobs (`job_id = ''`, false `ErrAlreadyExists`) and audit (`:000…1` collisions) (backend; data, blocker) | Rejected with `sdk.ErrInvalidInput` at the jobs store boundary and in `NewRecordsWith` — R12 (a), ruled |
| G6 | Jobs: the default policy and the host knob belong in the core, not three adapters (steward, should-fix) | `jobs.WithIDs` on `jobs.New`; the core mints on four paths; `Ensure.ID` create-only candidate; stores drop `newID` and never mint |
| G7 | A functional option on the pure builder `audit.NewRecords` conflicts with the constructor-configuration convention (steward, should-fix) | `audit.NewRecordsWith(ctx, changes, now, ids)` explicit; `NewRecords` delegates with the zero value (additive, no lockstep) |
| G8 | `0001_users.sql` edit is checksum churn: 0014 already pins the collation (backend, data, SRE) | Dropped from D3, task-6 and task-14 |
| G9 | Store SQL mint `totp.go:203` keeps v4 (data, should-fix) | Changed to `uuidv7()::text`; a Makefile grep guard allow-lists it; D4 notes the one PG18 runtime path |
| G10 | The rev 2 two-run restamp left the artifact unable to re-migrate between runs and P9 red (SRE, data: blocker) | Rev 3 replaced it with the 0000-first protocol; **rev 4: the whole live-host protocol is cut by owner ruling and preserved under "Deferred"** |
| G11 | One PR per pocket cannot produce a correct store `go.mod`/`go.sum` at the tag (SRE, blocker) | Two-PR train per pocket; ten PRs, thirteen tags |
| G12 | R6(a) forces turso restamps for DDL SQLite cannot even run (backend, data) | R6 → (b): no turso DDL anywhere |
| G13 | The "could not determine which collation" claim is wrong; the stores already mix `"C"` and default columns; the real R5 rationale is index-collation matching and `ORDER BY` parity (data) | D3 rewritten; R5 (a) ruled with the corrected column list and the `EXPLAIN` gate |
| G14 | Scaffold pins live in `init.go:105` and four `go.mod.tmpl`s; template code needs the sdk v0.21.0 pin (SRE, backend) | All workshop template and pin changes move to PR-6 (D6) with a pin-vs-DDL scaffold test |
| G15 | `Open` probe: both paths, non-fatal, redacted, stable attrs, zero-mode doc; operator-actionable `ErrServerVersion` text; probe on the migrating connection (SRE) | Folded into D4 |
| G16 | 17-pins the grep misses; PG17 service in CI for the negative leg; Debian image; native PG18 fallback (SRE) | Folded into D4/task-3 |
| G17 | MVS makes the floor a runtime lockstep for every pgx store; record a Set A negative; the PG17 maintenance-line question (SRE) | Module impact "Lockstep" + Set A negative; R11 ruled: no maintenance line |
| G18 | Missing risks: entropy drop, host length checks, prefix dashboards, in-flight cursors, server-upgrade mechanics, defer state (SRE, data) | Risks 1, 2, 4, 5 and the D5 defer row; grant ids confirmed not exposed inbound |
| G19 | Existing storetest already covers most verbatim cases; factories cannot pass store options; v7-shape asserts are pgx-only; cms/jobs have no collation tests; auth needs a second inventory (data) | Schema impact + tasks 6/8/10/11 rewritten accordingly |
| G20 | Clamp inside the closure, no package state; `crypto/rand` has no error path; drop the PG-backstop interleave rationale (steward, backend, SRE) | D1 rewritten |
| G21 | Nits: `postgres.go:9-12` comment, README pg_catalog list, google-uuid README wording, no `sdk/CHANGELOG`, workshop minor needs the "no rule-set change" sentence, `Ensure` mints per call | Folded |
| G22 | Steward's optional grep guard against `sdk.IDGenerator{}` literals in pocket `logic/` outside the exclusion allow-list | Taken as the task-4 verify grep for authentication; a Makefile guard is a follow-up if a second pocket grows an allow-list |

## Consultation notes

Review gate on rev 2: lead-backend-engineer, data-integration-reviewer, platform-sre,
architecture-steward — read-only, in parallel, on the plan file against `66d2abde`. Their
blockers and should-fixes are the table above. Rev 4's edits (rulings applied, protocol cut,
renumbering, tag protocol) were made on owner instruction without a re-review. Single hop; no
implementer consulted.

## Recommended reviews

- lead-backend-engineer: code review of PR-2a before the owner merges (security-adjacent core change).
- product-manager (post-hoc): scope — ten PRs, thirteen tags, one behaviour change hosts see in URLs.
- architecture-steward (post-hoc): `sdk.UUIDv7` and `IDGenerator.Or` as root primitives; the jobs core option.
- platform-sre: when the deferred live-host protocol is picked up.

## Notes

- The `IDs` doc comments on `authentication.Config`, `cms.Config`, `jobs.WithIDs` and the scaffold's
  `service.go.tmpl` say: "the zero value selects the class default: NanoID for identities, UUIDv7 for
  records; a configured generator wins everywhere".
- Battleboard's seed tool (`workshop/dev/seeds/main.go:137`) builds its own `googleuuid.V7()`; unaffected.
- #237 (pgxdb v0.16.0) did not repin the examples or the scaffold (both still pin pgxdb v0.15.0);
  PR-6 moves them straight to v0.17.0.
- Follow-up candidates, not in this plan: the deferred live-host protocol and a `gopernicus db restamp`
  verb; a Makefile guard for `sdk.IDGenerator{}` literals in pocket `logic/`; a shared storetest
  id-shape case; the pgxdb `live_test.go:68` ledger count.
