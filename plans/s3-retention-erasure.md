# S3 — Retention and erasure (SOC 2 readiness, slice 3) — rev 2

**Status:** EXECUTED 2026-10-23 — RELEASED. Twelve PRs squash-merged on `main` in order: #162 PR-1 pgxdb @ 31ac42b7 · #163 PR-5 cms @ acf7263d · #164 PR-5b @ 00cc48c2 · #165 PR-4 jobs @ 627f95b1 · #166 PR-3 authorization @ 66ecb03d · #167 PR-4b @ 3388d16a · #168 PR-3b @ 78f68807 · #169 PR-2a authentication core @ 7bc2630c · #170 PR-2b authentication stores @ f00205c5 · #171 PR-2c @ 371d47b4 · #172 PR-6 workshop @ 4beba21f · #173 PR-7 examples @ 46fd1260; PR-8 = the record (`docs/s3-release-record`). FOURTEEN tags released and cold-verified (pgxdb v0.12.0 @ 31ac42b7 incl. the MVS set of every released pgx store; authentication v0.23.0 @ f00205c5 + stores pgx v0.11.0 / turso v0.10.0 @ 371d47b4; authorization v0.30.0 @ 66ecb03d + stores pgx v0.21.0 / turso v0.20.0 @ 78f68807; jobs v0.7.0 @ 627f95b1 + stores pgx v0.7.0 / turso v0.6.0 @ 3388d16a; cms v0.5.0 @ acf7263d + stores pgx v0.5.0 / turso v0.4.0 @ 00cc48c2; workshop/gopernicus v0.7.0 @ 4beba21f). Live-stores dispatch runs: 40034837753, 40036209894, 40037265148, 40045860760 (PR-2b branch), 40047381425. Build-time deviations are recorded under "Build-time deviations" below. Former status: RATIFIED 2026-10-23 (owner: "ratified"; rev 2 as reviewed; R-7 = no in-place rail scrub in S3; R-8 = cms paging deferred to `cms-inquiries-admin-paging`; the roadmap deviations blessed). Build started from `main` @ 67a3fe2f the same day; worktrees `../gopernicus-s3-<pr>`. History: DRAFT rev 2 — review gate run (six reviews, folded). Nothing is built.
Anchors read on `main` @ 67a3fe2f (S2 follow-ups PR-4 #161 merged; S2 RELEASED 2026-10-23: authentication
v0.22.0 + stores pgx v0.10.1 / turso v0.9.0, authorization v0.29.1 + stores pgx v0.20.0 / turso v0.19.0,
secrets v0.2.0 + stores pgx v0.2.1, jobs v0.6.0 + stores pgx v0.6.0 / turso v0.5.0, cms v0.4.0 + stores pgx
v0.4.0 / turso v0.3.0 + views/goth v0.3.0, events v0.3.0, pgxdb v0.11.0, turso v0.8.0, sdk v0.16.0, workshop
v0.6.1; migrations latest: authentication 0022, authorization 0006, secrets 0003, jobs 0004, cms 0021, events
0002). Parent: `roadmap.md` (the S3 slice, verbatim scope, lines 157–180, and the O-3c constraints S2 wrote for
this slice; ruling 4 made S3 mandatory and FAS-3 HIGH). Evidence: `review.md` §4 **H6** (FAS-2, FAS-3, FAC-7,
FAZ-8, FOP-6, FOP-7, FOP-11, FAF-11, FAC-9) and **M3**'s "existing rows keep raw values until S3";
`control-map.md` CC6.2 "Removal of data on offboarding" and "Machine identity deprovisioning", CC6.5 "Retention
schedule enforceable per table", CC7.2 "Audit retention/export", C1; per-finding evidence in
`passes/04-auth-stores.md` (FAS-2, FAS-3), `passes/01-auth-core.md` (FAC-7), `passes/02-auth-federation.md`
(FAF-11), `passes/05-authorization.md` (FAZ-8), `passes/07-other-pockets.md` (FOP-6, FOP-7, FOP-11 and its
retention table). Released S2 state: `RELEASING.md` "S2 follow-ups", "Retired: Firestore stores", "Audit
completeness and integrity" (items (c), (d), (k)); `plans/s2-audit-completeness.md` (the trigger bodies, the
owner exemption, the probe contract, O-3c); `plans/soc2-s2-audit-completeness-handoff.md` (the maintenance rule:
hosts adopt once, at the roadmap's end); AUDIT-054/055. Shape and tone mirror `s2-audit-completeness.md` (rev 3)
and `s2-follow-ups.md` (rev 2). Rev 1 is `s3-retention-erasure.rev1.md` beside this file; the six reviews it
went through and every fold are attributed in Notes ("Review gate record") and summarized in "Decided by the
review gate". Every S2 rule stands: annotated tags, no signing, one push/tag/merge per command, never
`go work sync`, never tidy before the proxy serves a tag, the repo is public (no exploit shapes), hosts adopt
handoffs only at the roadmap's end (the handoff is written anyway), no `.env` mode line.

## Context

S1 made the production posture fail-closed and S2 made the audit rails complete and tamper-evident. Nothing
yet ends the life of a row. Expired sessions keep their refresh-token hashes (`stores/pgx/sessions.go:207-228`
deletes by id or user only), abandoned `oauth_states` keep a plaintext PKCE verifier (`0005_oauth_states.sql`),
expired grants and `contact_changes` are never swept, `invitations` has no DELETE anywhere and keeps the
invitee's address indefinitely (`logic/invitations/repository.go:65-106`), `Challenges.PurgeExpired`
(`challenge/repository.go:62-64`) and `OAuth2.Prune` (`oauth2/repository.go:89` — three UNBOUNDED, unindexed
DELETEs in one transaction, `stores/pgx/oauth2.go:352-361`) exist in every store and have zero framework
callers, `security_events` has no retention or archive path at all and — since S2 — no role but the migrator
may delete from it, `iam_operations` is "never deleted in v1" (`pockets/authorization/README.md:501-505`), the
ordinary `job_queue` keeps plaintext payloads and handler error text forever (`job_queue.sql:19,29`), cms
`inquiries` (third-party name, email, message) has no delete (`messaging/repository.go:8-13`,
`stores/pgx/inquiries.go:79-93`), and no user can be erased: `users.status` is CHECKed to
`{active, deactivated}` (`0014_user_status.sql:19-20`), every table references `users.id` by convention with
no FK, and `ServiceAccounts.Delete` leaves its `api_keys` (`stores/pgx/service_accounts.go:146-155`). The
runbooks page says so plainly ("Retention today", `docs/guides/operations-runbooks.md:97-109`).

S3 closes, by ID: **H6** (FAS-2, FAS-3, FAC-7's retention half, FAZ-8, FOP-6, FOP-7, FOP-11, FAF-11; FAC-9's
service-account cascade); **M3**'s residue (pre-AUDIT-054 raw `email`/`identifier` Details age out under this
slice's 400-day retention — the roadmap's own sentence; nothing scrubs them in place, R-7); the "Removal of
data on offboarding" GAP row and the "Retention schedule enforceable per table" GAP row of the control map, and
"Machine identity deprovisioning" to PARTIAL (the cascade here, disable in S4). It inherits, verbatim, the
constraints S2 wrote for it (O-3c, `roadmap.md:171-179`; `RELEASING.md` S2 item (k)): the audit-rail purge is a
migrator-owned SECURITY DEFINER function that keeps its retention floor inside the database under owner control
and appends a purge row to the rail it purged; a turso purge is DROP TRIGGER, DELETE, re-CREATE in one
transaction; no migration UPDATE-backfills a triggered table; every turso rebuild of an audit table ships a
paired trigger file (none is needed here — no audit table is rebuilt); `INSERT OR REPLACE` never on an audit
table; `iam_operations` keeps non-fenced DELETE so `Expire` runs in the server (S0 hosts grant it back — already
in the S2 handoff). O-3c's last clause ("in-place IP/UA erasure is designed against the owner-exempt UPDATE
branch") governs HOW such an erasure would be built; S3 does not build one (R-7): IP/UA on `security_events`
follow the 400-day retention clock, which is FAS-3's own recommendation (`passes/04-auth-stores.md:91`).

Left to later slices or plans: audit-read routes and `audit_read` (S7); `iam_audit` and `secrets_audit`
retention (host-owned — R-4, decided); the access log (S6); `ratelimit_windows` pruning stays the pgxdb README's
host cron (`pgxdb/README.md:688-692`), cited by the recipes; the cms admin inquiry paging and delete UI (R-8 —
its own cms plan, brief under Notes); a bounded `outbox.PurgePublished` (events untouched; named follow-up);
`filestorage.Disk.Upload` fsync (an sdk PATCH follow-up, named); tenant offboarding (drop the tenant database +
`secrets.DeleteOwner`) stays a documented procedure, with the per-tenant note of `roadmap.md` "Cross-plan
dependencies" (`roadmap.md:334-336` says the maintenance service "must iterate a host registry": the service
takes ONE `Repositories`; a multi-database host builds one per tenant and schedules each — the registry is the
per-tenant plan's, recorded as a deviation under Notes).

## Goal

Every table the review named has a bounded, scheduled, evidenced disposal path, a user can be erased in one
transaction without touching the immutability of the audit rail, and a production host is told — by WARN today
— when no retention runner is wired.

## Definition of Done

- `authentication.Components.Maintenance` runs one bounded pass (`Pass`) over every expired/terminal row class
  the roadmap names, archives-then-purges `security_events` PER CHUNK through the migrator-owned purge
  function on pgx and the capture-drop-restore transaction on turso, leaves one purge row per function call and
  one archived row per verified object on the rail; the blocking `Run` and the jobs-schedule recipe both drive
  it; production WARNs once without `MaintenanceRunnerAcknowledged`.
- `EraseUser` is one store transaction (status, revision, identifiers, credentials, links, mirrors, grants,
  sessions, codes, challenges, invitations, owned act-as-user service accounts and keys) that writes its own
  `user_erased` row inside the transaction; the rail is otherwise untouched (R-7); the bundled route mounts only
  on explicit opt-in; a repeat is a no-op.
- `ExpireOperations` (on `mutations.Service`), ordinary-queue `PurgeTerminalJobs`, and cms `inquiries`
  `Delete`/`DeleteByEmail`/`PurgeBefore` exist on optional capabilities resolved once at construction and printed
  in each pocket's registration log, with storetest cases over memory, pgx and turso; **which host runs what:**
  `examples/production-baseline` runs the authentication pass and `ExpireOperations` in its host-pocket runner;
  `examples/auth-cms` runs the authentication pass, `ExpireOperations`, `outbox.PurgePublished`, the inquiry
  `PurgeBefore` and the fenced delivery purge on its ticker; `examples/jobs-minimal` runs `PurgeTerminalJobs` on
  a jobs schedule. No example runs `DeleteByEmail`, the jobs-schedule form of the authentication pass, or turso
  authentication maintenance in a server (the harness proves turso in tests); the runbook composes erasure.
- No new compile break for a host-implemented repository (the one storetest contract change,
  `ServiceAccounts/DeleteCascadesKeys`, is gated on the cascade capability — Decided); the two new migrations
  per authentication dialect, one per authorization/jobs/cms dialect, carry byte-identical copies in every
  example ledger P9/G34 reads; `make check` and `make guard` (with G35 and G36) are green; the Linux-container
  `make test-stores` and per-step `live-stores` dispatches pass before each store tag; every full run uses a
  fresh database and a fresh libSQL server; every turso leg uses the corrected SKIP gate.
- Fourteen annotated tags cold-verified (every released pgx store cold-built and tested on pgxdb v0.12.0), the
  release record, the manifest and a host handoff written (not applied), `roadmap.md`/`control-map.md` rows
  flipped, the retention table and the "Erase a person" runbook published as artifacts.

## Out of scope

- Purge functions for `iam_audit` and `secrets_audit` (R-4), audit-read routes (S7), the access log (S6).
- In-place scrubbing of IP/UA or legacy address keys on `security_events` (R-7): rows stay intact, keyed to the
  pseudonymous `user_id`; they age out under retention. No `security_events_erase_client` function, no probe
  entry for it, no definer-UPDATE live case, no fifth event type.
- The cms admin paged inquiry list, the admin delete route/UI, the `Views.InquiriesList` change, the 0022
  collation rewrite and the `views/goth` retag (R-8): a named cms follow-up plan,
  `.claude/plans/cms-inquiries-admin-paging.md`, whose brief (the frontend review's CSRF, confirmation, pager,
  evidence and repeat-semantics findings) is recorded under Notes.
- A cross-pocket erasure COMPOSITION in a reference host: the runbook "Erase a person" is the composition
  (authentication `EraseUser` → authorization tuples for `user:<id>` through the role/mutation API →
  cms `DeleteByEmail` → delivery and job purges); `examples/production-baseline` mounts neither cms nor jobs.
- Leader election for the maintenance runner (one runner instance is documented; concurrent passes are proven
  safe, not cheap).
- Erasing rows already exported to an archive (archives are outside erasure's reach — documented, with the
  archive's own lifecycle as host policy in the handoff).
- Changing `ClaimAcceptance`'s "a claim never expires" contract; stale `accepting` claims are COUNTED and
  WARNed (rate-limited), never cancelled by maintenance; no force-cancel ships.
- A bounded `outbox.PurgePublished`; a `DELETE /auth/service-accounts/{id}` route (S4's "disable" row); the
  `Disk.Upload` fsync (sdk follow-up); the pgxdb `QuerierFrom` fix (S5); per-tenant registries (its own plan);
  signed tags (deferred).

## Design

### The one principle

**Every row has an owner-set lifetime, disposal is bounded and leaves evidence, and erasure never weakens
S2.** The runtime role gains no new privilege on an audit rail beyond EXECUTE on one function: the only path
that removes a `security_events` row is a migrator-owned SECURITY DEFINER purge function whose retention floor
is a framework invariant the migrator owns, which refuses a cutoff younger than the floor and appends its own
row to the rail; nothing rewrites a rail row (R-7). On turso the same statements run inside one short
transaction between dropping and restoring exactly the triggers that were there. Every purge is bounded
(`limit <= 0` is `sdk.ErrInvalidInput`, never "unbounded"; `limit > maintenance.MaxBatchSize` is refused at
construction), every retention value is a Go default a host raises, and every scheduled pass reports what it
did, what it skipped and whether its budget ran out. Two guarantees, stated separately (platform-sre #5): the
floor is the DATABASE's guarantee; archive-before-purge is the SERVICE's guarantee (a runtime role holding
EXECUTE can purge past-floor rows without archiving — the evidence is then a purge row with no matching archived
row, and the runbook's "Verify the archive" query finds it). Erasure keeps every pseudonymous id (`user_id`,
`invited_by`, `resolved_subject_id`) and removes everything that names a person.

### The retention model per store (floors, who may purge, where the floor lives)

| rows | eligible when (`<=` is the rule everywhere; a row exactly AT `before` is eligible) | default (Go, `maintenance.Config`) | floor (database, migrator-owned) | who runs the statement |
|---|---|---|---|---|
| `sessions` (both profiles) | `expires_at <= before` | `SessionRetention` 24h past expiry | none | runtime role (`maintenance.Repository`) |
| `oauth_states` | `expires_at <= before` | `OAuthStateRetention` 1h | none | runtime role |
| `authentication_grants` | `expires_at <= before` (the `consumed_at` OR-arm is dropped: a consumed grant expires within its TTL and the arm had no access path — data-integration #18) | `GrantRetention` 24h | none | runtime role |
| `contact_changes` | `expires_at <= before` | `ContactChangeRetention` 24h | none | runtime role |
| `invitations` | terminal (`accepted`/`declined`/`cancelled`) with `updated_at <= before`, OR `status='pending' AND expires_at <= before`; **never `accepting`**; `expired` is never stored (`invitation.go:39-42`) and does not appear in the SQL (data-integration #28) | `InvitationRetention` 90d (one value — no separate retention for `accepted`) | none | runtime role |
| `challenges` | the existing `Challenges.PurgeExpired(ctx, now-1h, limit)` | fixed 1h | none | runtime role |
| OAuth2 codes / client metadata / refresh history | **new bounded `PruneOAuth2(ctx, now, limit)`** on the maintenance port (the three tables, each `expires_at <= now`, `limit` per statement); the service never calls the unbounded `OAuth2.Prune` (kept for compatibility) | — (the stores' own horizons) | none | runtime role |
| `security_events` | `created_at <= before`, where `before = now − SecurityEventRetention`, archived then purged per chunk | `SecurityEventRetention` 400d; construction refuses < `maintenance.MinSecurityEventRetention` = **366d** (floor + 1 day — the Go mirror of the SQL floor plus the clock-skew margin, pinned by a test against the migration text) | **a framework invariant**: pgx `security_events_retention_floor()` = 365 days inside 0024; turso row 1 of `security_events_retention.floor_days` = 365 (0024 twin). No "how to lower it" procedure exists in S3 | pgx: the runtime role CALLS `security_events_purge_before(timestamptz, integer)`, a migrator-owned SECURITY DEFINER; turso: the store's capture-drop-restore transaction |
| `user_identifiers` (retired rows) | **not purged in S3**: kept for live users (`passes/04:187` "retired rows never purged"), blanked on erasure | — | — | — (product-manager #21) |
| `iam_operations` | `outcome IN ('applied','no_change','not_found') AND committed_at <= before`; fenced never (the SQL filters, the S2 trigger refuses regardless) | `authorization.WithOperationRetention` 180d, refused below 7d (`ErrOperationRetentionTooShort`) | none | runtime role (DELETE kept per S2) |
| `job_queue` (ordinary) | `status IN ('completed','dead_letter') AND updated_at <= before` | host-chosen; README: never below the scheduler's longest retry window (purging a completed job frees its id for a late refire — `schedules/service.go:162-164` treats `ErrAlreadyExists` as the ack) | none | runtime role (`PurgeTerminalJobs`) |
| `fenced_job_queue` | existing `Service.PurgeTerminal` (contract unchanged) | host-chosen | none | runtime role |
| `event_outbox` | existing `PurgePublished(before)` — UNBOUNDED; the recipes run it as its own step outside the maintenance pass and the handoff names a one-off manual drain before enabling it (platform-sre #6); a bounded variant is a named follow-up | host-chosen | none | runtime role |
| cms `inquiries` | `created_at <= before` (`PurgeBefore`); `Delete(id)`; `DeleteByEmail(email)` — ASCII case-fold equality | host-chosen | none | runtime role (the host's schedule calls the eraser it wired) |
| `oauth_refresh_history` | pruned by `PruneOAuth2` only after the store's reuse-detection horizon (`0019:28-29`); never by erasure | — | — | — |
| `iam_audit`, `secrets_audit` | **host-owned** (R-4): the definer-function template in `database-posture.md` is the recipe; no function ships | — | host | host migration + cron |
| `ratelimit_windows` | the pgxdb README's cron (`DELETE … WHERE expires_at < now()`) | host | — | host cron / the same schedule |

`seq` and `recorded_at` stay evidence columns no DOMAIN type carries; the export record of the maintenance
port (below) reads them, exactly as S2 said "the S3/S7 export tooling reads them". The floor clock is the
database's `now()` on pgx and `time.Now()` on turso and memory — never the service's `WithClock`
(data-integration, parity).

### The maintenance port and service (authentication) — exact exported names

Package `pockets/authentication/logic/maintenance` (beside `logic/invitations` and `logic/delivery`; the pocket
never imports `pockets/jobs` — the `DeliveryDispatcher` precedent, `config.go:258-266`). It imports the leaf
packages (`securityevent`, `challenge`, `oauth2`, `user`) and NEVER `logic/authentication` (the root service);
**G36** (a G26-shaped leaf grep, PR-2a) pins that direction. `maintenance.Repository` is a cross-aggregate,
use-case-shaped port like `passwordless.Repository`: its methods are NOT one transaction across methods (each
call is its own bounded statement), and a host store adopts it whole or not at all (lead-backend #26,
architecture-steward #10).

- `maintenance.Repository` — filled as **`Repositories.Maintenance`** (OPTIONAL; nil → the service runs only
  the steps whose ports exist and names the rest `Skipped` in its report; storetest skips the group LOUDLY):
  `PurgeExpiredSessions(ctx, before time.Time, limit int) (int, error)`, `PurgeExpiredOAuthStates`,
  `PurgeExpiredGrants`, `PurgeExpiredContactChanges`, `PurgeInvitations` (the one bounded statement over
  terminal-past-retention OR pending-past-expiry; NO `expired` writer — `service.go:827-828` promises an
  expired pending row keeps its tuple until cancelled; persisting `expired` would free the pending-tuple index,
  change `ListActiveByResource` and make `Resend` collide; the doc text becomes "until cancelled or purged past
  `InvitationRetention`" — lead-backend #22), `CountStaleAcceptingInvitations(ctx, before) (int, error)`,
  **`PruneOAuth2(ctx, now time.Time, limit int) (int, error)`** (bounded; the count is the sum over the three
  tables), `ExportSecurityEvents(ctx, before time.Time, after ExportCursor, limit int) ([]ArchivedEvent,
  ExportCursor, error)` (ascending `(created_at, id)`, `created_at <= before`; the page is EXTENDED to include
  every row tied at the last `created_at`, so a chunk boundary is always a whole instant;
  `ArchivedEvent{securityevent.SecurityEvent; Seq *int64; RecordedAt *time.Time}` — the ONLY type that carries
  the evidence columns; `Seq` nil on turso (no `seq` column) and memory, `RecordedAt` set on turso and pgx —
  data-integration #22), and `PurgeSecurityEventsBefore(ctx, before time.Time, limit int) (PurgeResult, error)`
  with `PurgeResult{Cutoff time.Time; Count int}` — ONE function call per port call, one purge row per call
  (`limit` is already ≤ `MaxBatchSize`, so the store never loops; lead-backend Q4). Every `limit <= 0` →
  `sdk.ErrInvalidInput` (the existing purges disagree — `challenges.go:246-251` treats 0 as unbounded,
  `fenced.go:138-141` as nothing — the new methods refuse). The reference store and authmem append the
  `security_events_purged` row in-transaction as part of the PORT contract (lead-backend Q5).
- `maintenance.ErrBelowRetentionFloor` (wraps `sdk.ErrInvalidInput`): the cutoff is younger than the database
  floor — the pgx store maps SQLSTATE 22023 from the function with `errors.As` on `*pgconn.PgError` (not a
  `pgxdb.MapError` widening); turso compares against `floor_days`.
- `maintenance.Config{SessionRetention, OAuthStateRetention, GrantRetention, ContactChangeRetention,
  InvitationRetention, SecurityEventRetention time.Duration; SecurityEventStepInterval time.Duration (24h — the
  rail step runs DAILY inside an hourly pass: skipped as `not_due` when it ran less than this ago in this
  process, always on the first pass after a start; product-manager "daily cadence"); BatchSize int (1000);
  ArchiveChunkRows int (5000 — K, the rows per object before ties); PassBudget time.Duration (5m — a deadline
  CHECKED BETWEEN batches and chunks, never a context deadline that cancels a statement mid-flight; lead-backend
  #23, platform-sre Q1); ArchiveSink ArchiveSink; ArchivePrefix string ("security-events/"); ArchiveLocation
  string (a host label for the sink — "gcs://audit-archive", "disk:/var/lib/app/archive" — carried in every
  archived row's Details)}`; every duration: zero = default, negative = `sdk.ErrInvalidInput`;
  `maintenance.MinSecurityEventRetention = 366 * 24 * time.Hour` (`ErrSecurityEventRetentionTooShort` — the
  authorization `…TooShort` idiom; lead-backend #21); `maintenance.MaxBatchSize = 10000` (the SQL `max_rows`
  clamp, pinned by a test to the 0024 literal; `BatchSize > MaxBatchSize` → `ErrInvalidInput` at
  construction, so the pgx clamp can never read as a short batch — platform-sre #18, data-integration #14).
  `maintenance.ArchiveSink interface{ Upload; Exists; Download; GetObjectSize }` — the narrow interface the
  service uses; `filestorage.Storer` satisfies it structurally (lead-backend #18).
- `maintenance.Service` — `New(repos maintenance.Repositories{Maintenance Repository; Challenges
  challenge.Repository; SecurityEvents securityevent.SecurityEventRepository}, cfg Config, opts ...Option)
  (*Service, error)`, `WithLogger`, `WithClock`, `WithInterval(d)` (the `Run` loop's period, default 1h).
  No `WithIDs`: archive object keys come from a PRIVATE zero-value `sdk.IDGenerator{}` (nanoid) — a host on
  database-assigned ids has `sdk.DatabaseID` returning `""`, which would make every key `<prefix>/.ndjson` and
  the rail never purge (lead-backend #9); an empty key is refused.
  **`(*Service).Pass(ctx) (Report, error)`** = one pass (the delivery convention keeps `Run` for the loop —
  architecture-steward #5, lead-backend #5): each step drains until a short batch or the budget is spent,
  continues past a failed step (`errors.Join`), survives a PANICKING step (recovered, logged ERROR with the
  stack, the step reported failed — platform-sre #20), and `Report{StartedAt, FinishedAt; Steps []Step{Name
  StepName; Count int; Skipped bool; SkipReason string; Err error}; StaleAcceptingClaims int; BudgetExhausted
  bool; ArchiveBacklog bool}` names skipped steps; step names are exported constants (`StepSessions`, …,
  `StepSecurityEvents`) so hosts can alert on them; the per-step log line is stable: `step=<name> count=<n>
  skipped=<bool> error_kind=<kind>`. A non-zero `StaleAcceptingClaims` (accepting rows older than
  `InvitationRetention`) logs WARN `invitations_accepting_stale` at most once per 24h or on change, naming
  the count and the action ("settle or cancel the claim through the host's grant path; maintenance never
  cancels it") — a host without `InvitationsConfig.Fencer` settles it through the operation the claim id
  names; S3 adds no force-cancel (product-manager Q3).
  **The rail step is archive-then-purge PER CHUNK** (product-manager #7, platform-sre #2/#4/#5, lead-backend
  #3): `cutoff = now − SecurityEventRetention` truncated to the microsecond; loop: (1) `ExportSecurityEvents`
  at most `ArchiveChunkRows` rows plus ties at the last `created_at`; (2) encode NDJSON — a versioned wire
  record keyed by column names (`{"v":1,"id":…,"seq":…,"recorded_at":…,"user_id":…,"event_type":…,…}`;
  `SecurityEvent` has no JSON tags, so the archive schema is defined here, not by Go field names —
  lead-backend #18) — and `Upload` ONE object `<ArchivePrefix><cutoff 20060102T150405.000000Z>/<nanoid>.ndjson`
  (no colons in keys — platform-sre #23; `Exists` → a fresh id, NEVER an overwrite — `Upload` replaces,
  `filestorer.go:21-24`); (3) read it back through `GetObjectSize` and `Download`: size, line count and SHA-256
  must match what was written (the disk sink does not fsync — `disk.go:142` — and a remote sink proves nothing
  on error; the read-back is the service's durability check; the fsync itself is the named sdk follow-up);
  (4) write ONE `security_events_archived` row through **`SecurityEvents.Create`** with the error RETURNED
  (never the fail-open `Recorder.Record`, `recorder.go:192-227`): `Actor{Type:"system", ID:"maintenance"}`,
  Details `{object, sha256, rows, first_created_at, first_id, last_created_at, last_id, archive_location}`;
  any error in (1)–(4) → no purge this pass, the step fails, the object (if written) is left for the runbook's
  reconciliation; (5) `PurgeSecurityEventsBefore(last_created_at, BatchSize)` draining until the count is short
  — purging only up to THAT chunk's last `created_at` (ties were all exported), with a WARN
  `archive_purge_count_mismatch` when the purged total differs from the chunk's row count (a row with an old
  application-supplied `created_at` arrived between export and purge — bounded, visible); (6) check the
  budget; repeat. `BudgetExhausted` is set when the deadline stops the loop with rows still eligible, logged
  WARN when it persists across passes ("first enablement drains the backlog over many passes; size
  `PassBudget`; watch WAL and replica lag" — the handoff's "first week" section). With NO sink the purge runs
  unarchived and the registration line says `archive_sink=none` — no WARN (a valid policy; product-manager
  #16, lead-backend Q3 answered: purge, not skip). Concurrent passes are SAFE (every statement is bounded and
  idempotent; the purge subselect takes `FOR UPDATE SKIP LOCKED`), not cheap: one runner instance is the
  documented posture.
  **`(*Service).Run(ctx) error`** = the zero-dependency blocking loop: a first pass at start after a jitter
  of up to 10 % of the interval (the auth-cms precedent waits a full interval, `purge.go:92-96`, so a host
  deployed hourly never purges), then every interval; a pass error is logged and the loop continues; returns
  on `ctx`. **`(*Service).LastReport() (Report, bool)`** is an atomic snapshot written by every pass — the
  baseline's `/healthz` reads it without a second core tag (lead-backend #5). `JobKind`/`JobHandler` are CUT
  (no example runs them; product-manager #3, lead-backend #15): the README shows the three-line
  `queue.HandlerFunc` wrapper around `Pass` (`func(ctx context.Context, _ queue.Job) error { _, err :=
  svc.Pass(ctx); return err }` — return an error only when `ctx` ended or EVERY step failed, so a persistent
  single-step failure does not dead-letter the schedule) and the real ensure shape:
  `schedules.Ensure{Name: "authentication-maintenance", Kind: "authentication.maintenance", Spec:
  schedules.Spec{Every: time.Hour}}` via `EnsureSchedule` (lead-backend #16); `PassBudget` must be shorter
  than the jobs lease window (README).
- Root: **`authentication.WithMaintenance(authentication.MaintenanceConfig) Option`** where
  `MaintenanceConfig{maintenance.Config; MaintenanceRunnerAcknowledged bool}` is a ROOT struct (posture lives
  at the root like `DeliveryConfig`'s acknowledgements; logic never reads the acknowledgement — lead-backend
  #17); `Components.Maintenance *maintenance.Service` (built from `repos.Maintenance`, `repos.Challenges`,
  `repos.SecurityEvents`, the clock); `MaintenanceRunnerAcknowledged: true` with `Repositories.Maintenance ==
  nil` is **`ErrMaintenanceAcknowledgedWithoutRepository`** in every mode (the `WithAuditDisabledAcknowledged`
  contradiction precedent, `config.go:886-896`) — a host that runs retention through its own SQL cron leaves
  the acknowledgement false and accepts the WARN; the error text names that case (product-manager #17);
  production logs ONE WARN when it is false (`maintenance_runner=unacknowledged: retention is not enforced;
  run Components.Maintenance.Run or the jobs schedule and set MaintenanceRunnerAcknowledged`) — WARN now,
  require at the roadmap's end (the owner's call then; product-manager Q8); the registration log gains
  `maintenance_runner=`, `archive_sink=<location|none>`, `security_event_retention=`. The acknowledgement is
  a wiring assertion (no env tag; the no-env-tag test), a P5 recognized surface by field name.

### User erasure — exact shape

- **Marker (R-1 (a), decided):** a nullable **`users.erased_at`** (pgx `TIMESTAMPTZ NULL`, turso `TEXT NULL`
  via `tursodb.FormatTime`), added by `0023_retention.sql` in both dialects with `ADD COLUMN IF NOT EXISTS`
  as the LAST statement of the file (metadata-only on pgx, but ACCESS EXCLUSIVE on `users` — so it follows the
  index builds, never precedes them; platform-sre #1, data-integration #3) and probed by `probeColumns` naming
  0023 (both stores). The COLUMN stays `deactivated` — every credential path and the fenced mint already refuse
  it (`user_admin.go:244-254`), and an old binary reads a correct denial. The DOMAIN becomes **honestly
  three-valued** (architecture-steward #4): `user.StatusErased = "erased"` joins `Status.Valid()` and the
  `status.go` doc ("CLOSED to exactly two values" is rewritten); `ParseStatus` round-trips it;
  `SetStatus(…, StatusErased)` is refused as a TRANSITION (`user.ErrErased`, wrapping `sdk.ErrConflict`), not
  as an unknown word; `NormalizeStatus` is unchanged. Replay semantics keep the `AdminRepository` sentinel
  contract (`user/status.go:209-220`, "never `sdk.ErrConflict`" on a same-status replay — lead-backend #13):
  `deactivate` on an erased user returns `{Status: erased, Changed: false}`; `reactivate` returns `ErrErased`
  from the store's locked read, so the replay branch at `user_admin.go:168` can never move an erased user back.
  **Every read path projects `erased` from `erased_at IS NOT NULL`**: pgx/turso `users.go` `Get`;
  `passwordless.go:174/364` user reads; `user_admin.go` `GetSummary`/`List`; `StatusChange.Status`; the
  storetest reference; authmem. One storetest case, `UserAdmin/SummaryReportsErased`, pins the projection
  across pgx, turso, reference and authmem. `List` with `Status: deactivated` EXCLUDES erased users;
  `Status: erased` is an accepted filter (product-manager Q4). Why not the roadmap's `status='erased'` CHECK
  alter: the turso twin must REBUILD `users`, and SQLite's `DROP TABLE` runs an implicit `DELETE` that fires
  host foreign-key actions inside the runner's one transaction where `PRAGMA foreign_keys` cannot be turned off
  (`turso/migrate.go:59`, `turso/turso.go:252`); on pgx the boot probe reads columns, never CHECK bodies.
- **Port:** **`user.EraseRepository`**, filled as **`Repositories.UserErasure`** (OPTIONAL; backed by the same
  transaction-capable adapter as Users/Sessions/Identifiers/… — documented the way `Passwordless` is,
  `repositories.go:125-136`; NOT added to `AdminRepository`, which `examples/auth-cms/pockets/authentication/
  outbound/authmem/user_admin.go:82` implements): `Erase(ctx, in user.EraseInput{ID string; Now time.Time;
  SubjectKeys []string; Event securityevent.SecurityEvent}) (user.Erasure, error)`; `Erasure{Changed bool;
  ErasedAt time.Time; Counts user.EraseCounts{Sessions, Grants, Passwords, OAuthAccounts, ExternalSubjects,
  ContactChanges, Codes, Challenges, IdentifiersBlanked, InvitationsDeleted, InvitationsBlanked,
  InvitationsCancelled, ServiceAccounts, APIKeys int}}` (no `AuditRowsScrubbed` — R-7). `Event` is the
  `user_erased` row the SERVICE prepares (a `(*Recorder).Prepare(ctx, Input) (SecurityEvent, bool)` factored
  out of `Record` — the same enrichment incl. the admin's IP/UA, no write); the STORE merges its counts into
  `Details` and appends the row INSIDE the transaction with the rail port's INSERT shape — atomic evidence
  (a crash after commit can no longer lose the only row naming the admin; platform-sre #12, product-manager
  #1). Empty `ID` → `sdk.ErrInvalidInput` BEFORE any statement (address-keyed magic-link challenges have
  `user_id=''`, 0015); unknown → `sdk.ErrNotFound`; already erased → `Erasure{Changed:false, ErasedAt:
  stored}` with no statement and no row (a retried POST is 200, not 409); an `accepting` invitation held by
  the user → `user.ErrEraseBlockedByClaim` (wraps `sdk.ErrConflict`): the claim id is the host's grant
  operation id and "never expires" (`invitations/repository.go:25-26,51-56`; auth-cms sends it through the
  authorization ledger, `membership.go:135-175`) — the operator settles the claim first.
- **The transaction (pgx; turso identical in SQLite, single writer), in this order (lead-backend #2,
  data-integration #16):** (1) `SELECT … FOR UPDATE` on `users` (serializes against the mint's `FOR SHARE`
  and the service-account insert's `FOR SHARE`); already erased → return; (2) CAPTURE the user's (kind,
  normalized value) pairs from `user_identifiers` (active AND retired), then DROP every pair that is currently
  an ACTIVE identifier of ANOTHER user (`EXISTS … user_id <> $1 AND replaced_at IS NULL`) — a retired address
  now owned by someone else must not reach that owner's invitations (data-integration #5); matching is exact
  on stored values: invitations store email lowercased and other kinds trimmed (`invitations/invitation.go:
  52-53`) while `user_identifiers.normalized_value` goes through the normalizer, so the capture also carries
  the invitation-side spelling for `phone` (storetest `EraseMatchesPhoneInvitation` pins it); (3) `SELECT …
  FROM invitations WHERE ((identifier_kind, identifier) IN (<pairs>) AND resolved_subject_id IN ('', $1)) OR
  (resolved_subject_type = 'user' AND resolved_subject_id = $1) FOR UPDATE` — the row lock serializes a
  concurrent `ClaimAcceptance` (an unlocked conditional UPDATE, `stores/pgx/invitations.go:225-226`), which
  then sees the row gone (`ErrNotFound`) instead of flipping it under the erase; (4) REFUSE if any locked row is
  `accepting` (`ErrEraseBlockedByClaim`); (5) `status='deactivated'` if active, `erased_at=now`,
  `status_changed_at=now` when it transitions, `display_name=''`, `updated_at=now`, `auth_revision+1`; (6)
  `user_identifiers`: for every row of the user (active AND retired) `replaced_at = COALESCE(replaced_at,
  now)`, `normalized_value=''`, `verified_at=NULL`, the three use flags and `is_primary` false,
  `updated_at=now` — both partial unique indexes are over `replaced_at IS NULL` (`0010:35-42`), so blanked rows
  cannot collide; (7) DELETE `user_passwords`, `oauth_accounts`, `external_subjects`, `contact_changes`,
  `oauth_authorization_codes`, `challenges WHERE user_id=$1 OR subject_key = ANY($keys)`,
  `authentication_grants` (by user and by the user's session ids), `sessions` (both profiles); (8) the LOCKED
  invitation rows: `pending` → DELETE; terminal → **`identifier=''`, `metadata='{}'`** (host metadata may
  carry invitee PII — data-integration #24), `updated_at=now`; **`invited_by`, `resolved_subject_id` and
  `resolved_subject_type` are KEPT** (the tombstone reversal, architecture-steward #3: they are pseudonymous
  ids, the `users` row survives, and a blank sentinel every reader must special-case buys nothing — the
  bundled cancel/resend admission reads `invited_by` only on live rows, and every row the user issued becomes
  terminal here; the pair `resolved_subject_id`/`resolved_subject_type` travels together, so lead-backend #2's
  "blank both" becomes "keep both"); invitations the user ISSUED (`invited_by=$1`): `pending` →
  `status='cancelled'`, `updated_at=now` (an offboarded admin's open invitations must not keep granting),
  nothing else changes; (9) service accounts with `owner_user_id=$1 AND act_as_user = true` and ALL their
  `api_keys` → DELETE (the cascade is limited to personal act-as accounts: a trusted `CreateServiceAccount`
  accepts an owner without act-as — `serviceaccount.go:38-62` — and an offboarded employee's non-act-as
  integration accounts are the host's to reassign, not S3's to destroy; lead-backend #7); (10) the
  `user_erased` row (Details = the counts; `UserID` = target, `Actor` = the admin) by plain INSERT. Untouched by
  design and documented: `oauth_refresh_history` (hashes; reuse detection), `security_events` entirely (R-7:
  the user's IP/UA, the admin's attribution on the rows leading up to the erasure, and any pre-AUDIT-054 raw
  address — including `invitation_created` rows attributed to an inviter — age out under retention),
  `iam_*` (the runbook's step), archives already written. Deleting `external_subjects` means the next provider
  login provisions a FRESH local user: Erase is not a ban — the README says so and points at deactivation.
- **Service and route:** `(*Service).EraseUser(ctx, actor Principal, id string) (user.Summary, user.Erasure,
  error)` — TRUSTED like `SetUserStatus` and shaped like it (`useradmin.go:106`; the route answers
  `{user, changed, counts}` from one port — lead-backend #13); `ErrUserErasureUnavailable` when the slot is
  nil; `ErrEraseSelf` (wraps `sdk.ErrInvalidInput`) when `actor.ID == id`; `SubjectKeys` =
  **`s.magicLinkSubjectKey(kind, value)`** over the user's identifiers (the function that WRITES them —
  `provisioning.go:47-49`, `"challenge:" + identifierDigest(kind, v) + ":login_magic_link"`;
  `IdentifierKey` never matches a stored key, data-integration #4; storetest
  `EraseDeletesAddressKeyedMagicLinkChallenge`); the store call runs under `context.WithoutCancel(ctx)` with a
  bounded timeout (`EraseTimeout`, 30s default) so a client disconnect cannot roll back a half-second from
  commit and every retry repeat the work (platform-sre #12). Construction invariants (precedents
  `ErrMachineRoutesGateWithoutRepos`, `ErrUserAdminReposRequired`, `config.go:827,846-849`; lead-backend #13):
  `AdministrationConfig.UserErasureRoute: true` with a nil `Repositories.UserErasure` or a nil `UserAdminCheck`
  → `ErrUserErasureRouteRequires`; `Repositories.UserErasure` set without `Repositories.ActiveSessions` →
  `ErrUserErasureRequiresActiveSessions` (an in-flight login must not mint through the unfenced
  `Sessions.Create` after the erase commits). Route **`POST /auth/admin/users/{id}/erase`** mounts under the
  existing `userAdministration` + `browserSafe` chain ONLY when the slot is wired AND
  **`AdministrationConfig.UserErasureRoute: true`** (an explicit opt-in: a host whose `UserAdminCheck` ignores
  `Action` — auth-cms's does, `host_policy.go:121-144` — must not receive a destructive route by upgrading);
  the check receives **`UserAdminErase UserAdminAction = "erase"`**; 200 with `{user, changed, counts}`; 409 on
  the accepting claim; 400 on self. A step-up grant is host policy (the `Action` is there to require it).
  `UserErasureRoute` is NOT an acknowledgement and does not join P5's `ack` vocabulary (lead-backend #17); it
  is listed in RELEASING's surprise list instead.
- **Service accounts (R-6 (a), decided — with lead-backend #7, data-integration #15):**
  `(*Service).DeleteServiceAccount(ctx, actor Principal, id string) (serviceaccount.Deletion, error)` is a NEW
  trusted service method (the tree has no delete service method and no delete route — `machine.go:143-146`;
  no route ships in S3: the "disable" row is S4's). Counts come from an OPTIONAL store capability
  **`serviceaccount.CascadeDeleter interface{ DeleteWithKeys(ctx, id string) (serviceaccount.Deletion, error)
  }`** with `Deletion{KeyCount, UnrevokedKeyCount int}`, implemented by the bundled stores and authmem; without
  it the service falls back to `Delete` and records zero counts with `cascade=false`. The port
  `ServiceAccounts.Delete(ctx, id) error` is unchanged (authmem `ports_v2.go:177` keeps compiling); the
  bundled `Delete` CASCADES to the keys in one transaction, taking `FOR UPDATE` on the account row;
  `APIKeys.Create` takes `FOR SHARE` on the account row (a concurrent mint cannot leave an orphan key); the
  service-account INSERT with an `owner_user_id` takes `FOR SHARE` on the owner and refuses `erased_at IS NOT
  NULL` at the STORE (the mint precedent, `pgx/user_admin.go:244-250` — a service-level check alone races a
  committing Erase, and a repeat Erase is a no-op that never converges). The service records
  `service_account_deleted` (Details `service_account_id`, `key_count`, `unrevoked_key_count`, `cascade` —
  counts, never prefixes, hashes or key ids; product-manager Q6); a revoked key leaves no `api_key_revoked` row
  through this path. The storetest case `ServiceAccounts/DeleteCascadesKeys` is GATED on `CascadeDeleter` (skip
  loudly without it), so PR-2a is green under go.work CI before the stores land (product-manager #12).

### The SECURITY DEFINER function (pgx, migration 0024) — honoring O-3c

`0024_security_events_retention.sql` (pgx), after `0023`, begins `SET LOCAL lock_timeout = '5s'`, then ONE
assertion block, then two functions with static bodies, each in its own dollar-quote tag, every function and
operator qualified (`pg_catalog.now()`, `pg_catalog.jsonb_build_object`, `OPERATOR(pg_catalog.<=)`, literals
cast `'…'::pg_catalog.text`; PostgreSQL 14 still grants CREATE on `public` to PUBLIC, so an unqualified name
could resolve to an attacker's overload), parameters prefixed (`p_before`, `p_max_rows`):

0. **The assertion `DO` block, FIRST** (data-integration #9, platform-sre #7/#8): `IF pg_catalog.current_schema()
   IS DISTINCT FROM (SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid =
   c.relnamespace WHERE c.oid = 'security_events'::pg_catalog.regclass) THEN RAISE EXCEPTION 'security_events
   lives in schema %, migration runs in %' …` and `IF NOT pg_catalog.pg_has_role(pg_catalog.current_user, (SELECT
   c.relowner … 'security_events'::regclass), 'MEMBER') THEN RAISE EXCEPTION 'the migrator must be a member of
   the owner of security_events; fix: GRANT <owner> TO <migrator>'` — `make migrate` fails BEFORE boot when a
   `"$user"` schema created after 0008 would pin the wrong schema, or when a rotated migrator is not the table
   owner's member (the probe would otherwise refuse production far from the cause).
1. **`security_events_retention_floor() RETURNS interval LANGUAGE sql STABLE`** — `SELECT '365
   days'::pg_catalog.interval`. **A framework invariant** (product-manager #6, platform-sre #9): no "lower the
   floor" procedure exists in S3 — every such sentence is deleted from the README, the posture guide and the
   scaffold; the Go minimum (366d) sits above it so the bundled service can never configure below the database
   floor, and a host that wants a shorter floor opens a design question, not a migration. `STABLE`, not
   `IMMUTABLE` (platform-sre #17, data-integration #21). It lives in its own plain function so the purge
   function's body never embeds a number the test cannot pin. It is NOT listed as a `DefinerFunction` (it is
   not a definer; listing it would make every production boot refuse — product-manager #13, data-integration
   #7); the store's 0024 existence probe checks it and `-posture-role` prints `floor=365 days` from it.
2. **`security_events_purge_before(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4) RETURNS TABLE
   (cutoff timestamptz, purged bigint) LANGUAGE plpgsql SECURITY DEFINER`** — `IF p_before IS NULL THEN RAISE …
   22023` (NULL would fall through every comparison — data-integration #21); `IF p_before OPERATOR(pg_catalog.>)
   pg_catalog.now() OPERATOR(pg_catalog.-) security_events_retention_floor() THEN RAISE … USING ERRCODE =
   '22023'` (**refuse, never clamp** — R-2 (a), decided: archive and purge always agree on one cutoff; the Go
   default 400d and minimum 366d sit above the 365d floor so clock skew cannot trip it); `p_max_rows :=
   LEAST(GREATEST(p_max_rows, 1), 10000)` (the literal `maintenance.MaxBatchSize` pins); `DELETE FROM
   security_events WHERE id IN (SELECT id FROM security_events WHERE created_at OPERATOR(pg_catalog.<=)
   p_before ORDER BY created_at, id LIMIT p_max_rows FOR UPDATE SKIP LOCKED)` (row locks only — a BEFORE
   trigger does not fire on `FOR UPDATE`; two replicas' purges no longer queue or deadlock on each other —
   platform-sre #10, data-integration #10) through the owner-exempt DELETE branch (the S2 live test proves a
   migrator-owned definer deletes for a non-owner caller, `audit_integrity_live_test.go:217-247`); `GET
   DIAGNOSTICS n = ROW_COUNT`; then **one purge row per call, count 0 included** (O-3c: the evidence that the
   control ran on an empty day): `INSERT INTO security_events (user_id, actor_type, actor_id, event_type,
   event_status, details, ip_address, user_agent, request_id, trace_id, created_at) VALUES ('', 'system',
   'maintenance', 'security_events_purged', 'success', pg_catalog.jsonb_build_object('cutoff',
   pg_catalog.to_char(p_before AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), 'count', n, 'floor_days',
   365, 'session_user', pg_catalog.session_user::pg_catalog.text, 'max_rows', p_max_rows), '', '', '', '',
   pg_catalog.now())` — `actor_id` is `maintenance` in BOTH dialects (platform-sre #23); `floor_days` is an
   integer (never `interval::text`, which follows `IntervalStyle`); `session_user` is the CONNECTING role (behind
   a pooler, the pooler's login role — documented as such, data-integration); `id`, `seq`, `recorded_at` take
   their defaults (the owner's sequence; `gen_random_uuid` bound by OID). Returns `(p_before, n)`. The store
   calls it with explicit casts, `security_events_purge_before($1::pg_catalog.timestamptz,
   $2::pg_catalog.int4)`, schema-qualified under `WithSchema`, so a planted overload cannot make the call
   ambiguous.

**`search_path`.** A definer function must NAME `security_events`, and the file cannot hard-code a schema: the
runner applies files under `SET LOCAL search_path TO "<schema>"` (`pgxdb/migrate.go:183`) or the default. So
0024 creates the function with a static body and then runs ONE `DO $$ … $$` block: `EXECUTE
pg_catalog.format('ALTER FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) SET
search_path = pg_catalog, %I, pg_temp', <the nspname of 'security_events'::regclass — the SAME expression the
assertion used, never current_schema()>)` (the tuple-cache trigger's `format('%I', TG_TABLE_SCHEMA)` precedent,
`tuple_cache_migrations/0002:28,39`). `pg_temp` is listed EXPLICITLY and LAST: omitted, PostgreSQL searches
`pg_temp` first for tables and a caller could shadow `security_events` with a temp table; `SET search_path FROM
CURRENT` is never used (it would capture the runner's path without `pg_temp`). The file ends with `REVOKE
EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) FROM PUBLIC`
(functions default to PUBLIC EXECUTE; the migration cannot know the runtime role, so the two-role recipe gains
`GRANT EXECUTE ON FUNCTION … TO runtime`); the floor function stays callable (it is read by `-posture-role`).
The same `DO` block ships standalone in the runbook as the re-pin snippet after a restore into a renamed
schema (`proconfig` names the old schema), beside `pg_restore -x`'s "revoke from PUBLIC, grant to the runtime
role" step (platform-sre #7). Rollback story (RELEASING): binary rollback is safe (an old binary never calls
the function and reads `erased` as `deactivated`); schema rollback is forward-fix only — no down files, and
dropping `erased_at` after an erase loses the marker.

**What the probe does and does not check — stated plainly (pgxdb v0.12.0, the one connector change).**
`pgxdb.InspectTablePrivileges` inspects the trigger PAIR's function only (`probe.go:135-137,153-157`); the 0024
functions are invisible to it today and their bodies are NEVER compared. S3 adds `pgxdb.DefinerFunction{Signature
string}` (e.g. `security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4)` — qualified argument
types, data-integration #8d), `AppendOnlyTable.Functions []DefinerFunction`, `TablePrivilege.Functions
map[string]FunctionPrivilege{Exists, SecurityDefiner, SearchPathPinned, SameNamespace, OwnerIsTableOwner,
OwnerSuperuser, PublicExecute, PathSchemaCreate, Execute bool}` (`TablePrivilege` gains a map field and stops
being comparable — one RELEASING line, lead-backend #25), resolved in this order and phase (platform-sre
#15d, data-integration #8): **`to_regprocedure` runs in the same pre-pin phase as `to_regclass` for the
tables — BEFORE `ProbeAppendOnly` pins `SET LOCAL search_path TO pg_catalog` (`probe.go:458-468`) — or a
bare-schema host reads "function missing" and refuses in every mode**; then by oid: `p.pronamespace =
c.relnamespace` (the `triggerNamespace` precedent, `probe.go:565` — a same-named decoy earlier on the runtime
path is what the bare call would resolve), `prosecdef`, `proconfig` compared in SQL against
`'search_path=pg_catalog, ' || pg_catalog.quote_ident(n.nspname) || ', pg_temp'` with EXACTLY one element
(the `canonicalTriggerConfig` shape, `probe.go:77,590`; GUC flattening quotes a mixed-case schema as `"S3"`),
`pg_has_role(proowner, relowner, 'MEMBER')`, `rolsuper` of the owner, `has_function_privilege('public', oid,
'EXECUTE')`, `has_schema_privilege(<runtime role>, n.nspname, 'CREATE')`, and the inspected role's EXECUTE.
**`Writable()` and `CannotAppend()` are unchanged — DELETE on the rail stays the migrator's, so the runtime-role
posture of S2 is unchanged.** Findings split in two (platform-sre #7): **`UnsafeFunctions()`** = exists AND
(PUBLIC EXECUTE, or `search_path` not pinned / not the canonical value, or resolved outside the table's
namespace) → `ProbeAppendOnly` in production REFUSES with **`pgxdb.ErrDefinerFunctionUnsafe`** (not waivable —
these are the escalation vectors; a host DROP + CREATE silently re-grants PUBLIC EXECUTE; the refusal names
the function and the fix: `REVOKE EXECUTE … FROM PUBLIC`, `ALTER FUNCTION … SET search_path = pg_catalog,
<schema>, pg_temp`), development prints it in the one `append_only=unenforced` line;
**`InoperativeFunctions()`** = not SECURITY DEFINER, or owner not a member of the table owner, or owner is a
superuser, or the runtime role holds CREATE on the pinned schema → a production WARN naming the function and
the fix (`ALTER FUNCTION … SECURITY DEFINER`; `ALTER FUNCTION … OWNER TO <owner>`; a `NOSUPERUSER` migrator;
`REVOKE CREATE ON SCHEMA <schema> FROM PUBLIC`) and a loud error at call time — a host whose rotated migrator
is not the owner's member boots on S3 as it did on S2. A MISSING function is the STORE's refusal naming
`0024_security_events_retention.sql` in every mode (the column-probe precedent); a missing EXECUTE for the
runtime role is a production WARN scoped to the capability wired — `maintenance_execute=missing
function=security_events_purge_before fix=GRANT EXECUTE …` fires only when `Repositories.Maintenance` is
set (product-manager #16, platform-sre #19) — and the call-time `sdk.ErrForbidden` is wrapped with the GRANT
text only when the message is "permission denied for function" (a 42501 from a non-canonical trigger inside
the definer says "the trigger refused the owner" instead — data-integration #23). `-posture-role` prints
`functions=[security_events_purge_before:execute=true|false unsafe=… inoperative=…] floor=365 days` and
decides identically (the runner template and the baseline runner both call the store's `AuditTables()`).
`authentication/stores/pgx.AuditTables(opts...)` gains ONE `DefinerFunction`; authorization and secrets
`AuditTables` list none (R-4). Probe hygiene in the docs: `REVOKE CREATE ON SCHEMA <schema> FROM PUBLIC` and a
`NOSUPERUSER` owner role for `make migrate` join `database-posture.md` "Two roles" and the baseline README
(the baseline's documented migrator `DB_USER=postgres` is a superuser in the docker image — platform-sre #8).

### turso — the capture-drop-restore transaction

No owner concept exists. `PurgeSecurityEventsBefore` runs inside `db.InTx` (`BEGIN IMMEDIATE`,
`turso/tx.go:36-38`; DDL is transactional — the runner already applies 0022 inside `InTx`,
`turso/migrate.go:59,288`), in THIS order (data-integration #12): (1) `SELECT name, sql FROM sqlite_schema WHERE
type='trigger' AND tbl_name='security_events' COLLATE NOCASE` — CAPTURE what is there, and CLOSE the rows
before any DDL (an open statement on a local libsql connection fails DDL with SQLITE_LOCKED); (2) `DROP
TRIGGER` exactly those names; (3) the bounded DML (`DELETE … WHERE id IN (SELECT id … WHERE created_at <= ?
ORDER BY created_at, id LIMIT ?)` with `tursodb.FormatTime` — every TEXT timestamp the maintenance and erasure
code writes or compares goes through it; never `strftime('now')` in a predicate, 0021's `recorded_at` default
renders 3 digits); (4) RE-EXECUTE the CAPTURED `sql` verbatim — the triggers are back; (5) the purge row by
plain INSERT (never `INSERT OR REPLACE`; `actor_type='system'`, `actor_id='maintenance'`, `floor_days`, no
`session_user` key) — AFTER the restore, so a host `AFTER INSERT` mirror trigger sees the evidence row (the
immutability triggers do not touch INSERT); (6) after COMMIT the store re-reads the trigger set and logs ERROR
`rail_triggers_changed` (a stable kind) if it differs from the capture (platform-sre #14). Restoring what was
captured — not the embedded 0022 — means a host that DECLINED 0022 (P9 ruling, `roadmap.md:141`) does not get
its triggers re-armed by a maintenance pass, a host-altered trigger is not silently "repaired", and the
restored text is byte-identical to what `ProbeAuditImmutable` compares by construction (SQLite stores
already-normalized text; `turso/probe.go:84,105` trims). Each batch is its own short transaction: the write
lock blocks every login for its length (the README names an inter-batch pause for turso hosts; the handoff's
"first week"). The floor is row 1 of `security_events_retention` (0024 twin: `CREATE TABLE IF NOT EXISTS
security_events_retention (id INTEGER PRIMARY KEY CHECK (id = 1), floor_days INTEGER NOT NULL CHECK
(floor_days >= 1)); INSERT OR IGNORE INTO security_events_retention (id, floor_days) VALUES (1, 365)` — re-runnable
after the shared ledger is dropped; not an audit table); a MISSING row is a refusal naming 0024 at construction,
never a default of 0; the conformance harness `truncate()` EXCLUDES it and a test proves the row survives a
truncate (data-integration #13). The store refuses a cutoff younger than `now − floor_days` with
`ErrBelowRetentionFloor`. The README says what the floor is on turso: a footgun guard, not a control — the
runtime token can `DROP TRIGGER` itself. Tests: the `LocalSQLite` file cases live in an UNTAGGED file
(`maintenance_localsqlite_test.go` — a `//go:build integration` file would never run in `make check`;
platform-sre #14) and include fault injection (a test hook that errors after `DROP TRIGGER`; a context
cancelled before COMMIT) asserting the `sqlite_schema` trigger set is byte-identical afterward, a host `AFTER
INSERT` trigger that fires for the evidence row and is restored byte-identical, a declined-0022 ledger copy →
purge leaves NO trigger, present → byte-identical and production `Repositories` with a WARN-capturing logger
logs ZERO WARNs (`audit_integrity_test.go:180-205` pattern), the missing floor row, and a purge with a
concurrent WAL reader; the libSQL-server cases stay tagged.

### Guards G35 and G36

**G35 `guard-rail-no-dml`** (lands in PR-2b, the PR that creates the allow-listed lines — product-manager #14):
no non-test Go file under `pockets/*/stores/**` contains a line matching
`(UPDATE|DELETE[[:space:]]+FROM|INSERT[[:space:]]+OR[[:space:]]+REPLACE[[:space:]]+INTO|TRUNCATE|DROP[[:space:]]+TRIGGER)[^;]*(\bsecurity_events\b|securityEventsTable|\biam_audit\b|\bsecrets_audit\b)`
— BOTH spellings: the literal table names turso writes and the concatenated forms pgx writes
(`s.table(securityEventsTable)`, `security_events.go:101,130`; `cfg.schema.Table("iam_audit")`,
`authorization/stores/pgx/audit.go:60`; `r.table("secrets_audit")`, `secrets/stores/pgx/pgx.go:497`); the
word boundaries keep `security_events_retention` and `security_events_purge_before` out
(lead-backend #4, data-integration #17, architecture-steward #7). The allow-list is PER LINE — the exact
`DELETE`, `DROP TRIGGER` statement lines of `pockets/authentication/stores/turso/maintenance.go`, named in the
Makefile with their text — never a whole file (G9/G13 discipline). The pgx stores only CALL functions. The
guard gets the `# G35 (SOC 2 S3, …)` header and the `make guard` aggregate line; the planted-violation test
plants a literal line AND a concatenated line; the count prose moves "thirty-three" → "thirty-four" in this
PR (`Makefile:516`, `check.yml:2`). `iam_operations` is not on the list (`AllowDelete`).

**G36 `guard-auth-maintenance-leaf`** (PR-2a, the PR that creates the package): the G26 shape —
`pockets/authentication/logic/maintenance` imports no `pockets/authentication/logic/authentication"` (the root
service package; leaf sub-packages `…/logic/authentication/{securityevent,challenge,oauth2,user}` are allowed).
Header, aggregate line, "thirty-three" → "thirty-four" in PR-2a; PR-2b then moves it to "thirty-five".

### Authorization, jobs, cms — optional capabilities, resolved ONCE at construction

The rule for all three (architecture-steward #1, lead-backend #10/#13): a capability is resolved ONCE at
construction, printed in the pocket's registration log (`operation_expiry=`, `terminal_purge=`,
`inquiry_maintenance=`), production WARNs when it is absent, and it is an explicit optional slot wherever a
`Repositories` struct exists. Nothing is discovered at call time or silently absent.

- **Authorization:** `mutations.OperationExpirer interface{ Expire(ctx, before time.Time, limit int) (int,
  error) }` — filled as **`authorization.Repositories.OperationExpiry`** (OPTIONAL; the bundled stores and the
  memory store implement it beside `OperationLedger`, not a widening of it); `DELETE FROM iam_operations WHERE
  operation_id IN (SELECT operation_id FROM iam_operations WHERE outcome IN ('applied','no_change','not_found')
  AND committed_at <= $1 ORDER BY committed_at, operation_id LIMIT $2 FOR UPDATE SKIP LOCKED)` — NO
  `lockAuthorization` (the lock is on `iam_tuples`, not `iam_operations`, `writes.go:142-144`; a row's outcome
  never changes); `Expire` mirrors `Fence`'s `ErrMutationInsideTransaction` inside an ambient transaction
  (`operations.go:62-64`; data-integration #27); **"Expire never deletes a fenced row"** (the SQL filter plus the
  S2 trigger) — but it DOES race `Fence`, which is `INSERT … ON CONFLICT DO NOTHING` then a separate `SELECT
  outcome` (`operations.go:78-88`): an `Expire` committing between the two returns "fence conflicted but no
  record exists" as non-retryable today; that branch becomes RETRYABLE (re-run the INSERT, which then records
  `fenced`) with a deterministic race test (a hook or fixture role commits the delete between the INSERT and
  the SELECT → `Fence` returns `fenced`, never an error; data-integration #11). Migration
  **`0007_iam_operations_expiry.sql`** in both dialects: `SET LOCAL lock_timeout = '5s'`; `CREATE INDEX IF NOT
  EXISTS idx_iam_operations_committed ON iam_operations (committed_at, operation_id)` — ADMITTED, not required
  (both schema probes admit non-unique indexes, `pgx/schema.go:158-161`, `turso/schema.go:59`); the store's
  constructor logs WARN `index_missing=idx_iam_operations_committed file=0007_iam_operations_expiry.sql` when
  absent (a `to_regclass` check), since every batch would otherwise sort the whole ledger (platform-sre Q3).
  Root: `authorization.WithOperationRetention(d time.Duration) Option` (default 180d; `< 7d` →
  `ErrOperationRetentionTooShort`; set WITHOUT `Repositories.OperationExpiry` →
  `ErrOperationRetentionWithoutExpirer` at construction, the contradiction precedent), threaded into
  **`(*mutations.Service).ExpireOperations(ctx, limit int) (int, error)`** (hosts call
  `Components.Mutations.ExpireOperations`; a use case on the bundle is what `pockets/README.md` §1 forbids, and
  `Components` is held by value — architecture-steward #2, lead-backend #10) computing `before = now −
  retention` (the floor lives in the SERVICE: the store stamps `committed_at` from `time.Now()`,
  `operations.go:45`, so storetest passes `before = now + δ`); `ErrOperationExpiryNotConfigured` beside
  `ErrOperationLedgerNotConfigured` when the slot is nil. Docs: `OperationLedger`/`Fence`/`Recorded` gain "an
  expired operation id reads as never recorded" (a claim retried after retention re-Applies — the host's
  Granter probes resource existence only when the op is recorded, `membership.go:155-170`); the retention MUST
  exceed every producer's retry window and the oldest open `accepting` claim; the stale-accepting WARN
  threshold (`InvitationRetention`, 90d) must stay at or below half of the operation retention (180d) — both
  READMEs; "V1 never deletes them" becomes "expired past the configured retention; fenced never".
- **Jobs:** `queue.TerminalPurger interface{ PurgeTerminal(ctx, before time.Time, limit int) (int, error) }`
  implemented by the ordinary memory/pgx/turso queues; resolved once at `queue.NewService` (a type assertion
  on the ordinary queue, logged `terminal_purge=`); **`(*queue.Service).PurgeTerminal` keeps its contract
  byte-identical** (fenced-only, `ErrFencedQueueRequired`, "zero removes nothing, negative = unbounded",
  `work.go:151-163` — auth-cms's delivery purge reports its count to `rt.Purged`, the DELIVERY lifecycle
  observation, `authjobs.go:136-137`; summing the ordinary queue into it would over-report delivery purges
  and purge ordinary jobs at the delivery retention; lead-backend #1, architecture-steward #9); NEW
  **`(*queue.Service).PurgeTerminalJobs(ctx, before, limit) (int, error)`** for the ordinary queue: refuses
  `limit <= 0`, returns `ErrTerminalPurgeRequired` (the jobs `…Required` idiom) when the ordinary queue lacks
  the capability; `job_queue`: `DELETE … WHERE job_id IN (SELECT job_id FROM job_queue WHERE status IN
  ('completed', 'dead_letter') AND updated_at <= $1 ORDER BY updated_at, job_id LIMIT $2 FOR UPDATE SKIP
  LOCKED) AND status IN ('completed','dead_letter')` (`FOR UPDATE` in the subquery already re-evaluates its
  WHERE against the newest row version under READ COMMITTED; the repeated outer predicate is extra insurance,
  not the only recheck — data-integration "the SQL is right, the reason is wrong"); migration
  **`0005_job_queue_terminal_purge.sql`** in both dialects: `lock_timeout`; `CREATE INDEX IF NOT EXISTS
  idx_job_queue_terminal ON job_queue (updated_at, job_id) WHERE status IN ('completed','dead_letter')` —
  admitted, not required, the same WARN when absent. README: payloads are stored as submitted and
  `failure_reason` is the handler's error text — handlers must not embed payload data in errors; retention
  never below the scheduler's longest retry window.
- **cms (R-8 — the retention half only):** `messaging.InquiryEraser interface{ Delete(ctx, id string) error;
  DeleteByEmail(ctx, email string) (int, error); PurgeBefore(ctx, before time.Time, limit int) (int, error) }`
  — filled as **`cms.Repositories.InquiryMaintenance`** (OPTIONAL; the bundled stores and the two example
  memstores — `examples/minimal/pockets/cms/outbound/memstore`, `examples/auth-cms/pockets/cms/outbound/memstore`
  — implement it; `InquiryRepository` is unchanged; `Register` logs `inquiry_maintenance=wired|absent` and
  WARNs in production when absent). `DeleteByEmail` is ASCII case-fold equality (`lower(email) = lower($1)` —
  SQLite's `lower` is ASCII-only, so the port says ASCII; `strings.EqualFold` on the ASCII range in memstores;
  storetest `Inquiries/DeleteByEmailCaseInsensitive` — lead-backend #12, data-integration #20). `Delete` on an
  absent id → `sdk.ErrNotFound`. `Register` returns only `error` (`cms.go:133`) and S3 adds no `messagingsvc`
  method, no route and no view: the HOST's schedule calls the eraser it wired (`repos.InquiryMaintenance.
  PurgeBefore`; the runbook's `DeleteByEmail` likewise) — the host owns the adapter instance it handed to
  `Register` (lead-frontend "host reach", answered). Migration **`0022_inquiries_created_at.sql`**: pgx `SET
  LOCAL lock_timeout = '5s'; CREATE INDEX IF NOT EXISTS idx_inquiries_created_at ON inquiries (created_at)`;
  turso the same index — a plain `created_at` access path for the bounded purge; NO collation rewrite, NO
  keyset index (the follow-up's, if it still wants one); admitted, not required, WARN when absent. The admin
  paged list, the delete route/UI, `Views.InquiriesList`, `InquiryPager` and the `views/goth` retag are the
  follow-up's (brief under Notes).
- **events:** nothing changes (`outbox.PurgePublished` exists, `outbox.go:58-61`, UNBOUNDED); the recipes
  schedule it as its own step with the manual-drain note.

### The security-event vocabulary (registered in `Types()`)

`TypeUserErased = "user_erased"` (UserID = target, Actor = admin, Details = the bounded counts — written inside
the erase transaction), `TypeServiceAccountDeleted = "service_account_deleted"`, `TypeSecurityEventsPurged =
"security_events_purged"` (written by the function / the turso store — `Actor{Type: "system", ID:
"maintenance"}` in both dialects, Details `cutoff`, `count`, `floor_days`, `max_rows`, pgx `session_user`),
`TypeSecurityEventsArchived = "security_events_archived"` (Details `object`, `sha256`, `rows`,
`first_created_at`, `first_id`, `last_created_at`, `last_id`, `archive_location`; through
`SecurityEvents.Create` with the error returned). Portable assertions in storetest check `event_type`,
`event_status`, `count` and the parsed `cutoff` only — never `actor_id` or JSON formatting. The README's
vocabulary list and the registration test grow by **four** (no `security_events_client_erased` — R-7).
Dashboards filter `actor_type='system'`: a daily rail step writes ~365 system rows a year, an hourly session
step none (RELEASING surprise list).

### `guard --posture`, the scaffold and the examples

- **P5 surfaces** (`examples/README.md` §3b list; `posture_rules.go:64-74`): `MaintenanceRunnerAcknowledged`
  joins `postureAcknowledgements` by field name (the recognizer is a field-name map; no option-form entry is
  needed), so `--posture` prints one `ack` line for it. `UserErasureRoute` is not an acknowledgement and is not
  listed. No new P rule: a runner cannot be proven by text.
- **The runner template** (`templates/init/migrations.main.go.tmpl`) and the baseline runner print the
  `functions=` and `floor=` evidence; the scaffold README gains "Retention" (the schedules: `Maintenance.Run` or
  the jobs recipe, `Mutations.ExpireOperations`, `PurgeTerminalJobs`/`PurgePublished`/inquiries, the
  `ratelimit_windows` cron; the floor as an invariant; `GRANT EXECUTE`; `REVOKE CREATE … FROM PUBLIC`; a
  `NOSUPERUSER` migrator; archive the `-posture-role` output; the index pre-build recipe). The scaffold mounts
  no pocket, so the recipe is text, not code.
- **Docs:** `docs/guides/production-posture.md` gains the retention table (the table above, with the retired
  identifiers row and the two host-owned rails) and the WARN; `docs/guides/database-posture.md` "Two roles"
  gains `GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO
  runtime`, `REVOKE CREATE ON SCHEMA <schema> FROM PUBLIC`, the `NOSUPERUSER` migrator, the definer-function
  rules (owner = migrator, `search_path` pinned with `pg_temp` last, PUBLIC EXECUTE revoked — the probe refuses
  otherwise; owner/definer mismatch WARNs), the `CREATE INDEX CONCURRENTLY IF NOT EXISTS <same name>` pre-build
  recipe with the `indisvalid` check ("a failed concurrent build leaves an INVALID index that `IF NOT EXISTS`
  keeps — `DROP INDEX` it first") and "apply the S3 files in their own runner invocation", and the definer
  template for host-owned rails; `docs/guides/operations-runbooks.md` "Retention today" becomes "Retention"
  (what runs, where the evidence is: the purge rows, the archived rows, the archive objects, the jobs
  `last_run_at`; the canonical alert "no `security_events_purged` row in 48 h"; "Verify the archive": fetch
  each object, check its digest and line count against the archived row, compare the sum of
  `security_events_purged.count` per cutoff; the archive's own lifecycle — retention, backup, access control,
  object lock on a real sink — is host policy; restore steps: after `pg_restore -x` revoke from PUBLIC and
  grant to the runtime role, after a schema rename re-pin with the `DO` block), and gains **"Erase a person"**
  (the composition: `POST /auth/admin/users/{id}/erase` or `EraseUser` → settle any accepting claim first →
  authorization tuples for `user:<id>` through the role routes/`Mutations` as an EXECUTED step → cms
  `DeleteByEmail` through the wired eraser → `PurgeTerminal` for sealed delivery rows → archives are out of
  reach → `iam_operations` fingerprints expire under `ExpireOperations`; then the SWEEP: a psql `DO` block over
  every text/jsonb column of every table counting rows containing B's email and last IP — expected zero
  except the documented carve-outs: `oauth_refresh_history` hashes, `security_events` rows within retention,
  archives); `docs/pockets/{authentication, authorization, jobs, cms}.md` the new surfaces.
- **`examples/production-baseline`** (no jobs module; R-3 (a), decided): `cmd/server/main.go` wires
  `WithMaintenance(authentication.MaintenanceConfig{Config: maintenance.Config{ArchiveSink:
  filestorage.NewDisk(cfg.AuditArchiveDir), ArchiveLocation: "disk:" + cfg.AuditArchiveDir},
  MaintenanceRunnerAcknowledged: true})`, `AdministrationConfig.UserErasureRoute: true` with the admin role,
  `WithOperationRetention(180d)`; the host pocket **`pockets/retention/inbound/runner`** (a ticker that
  INVOKES pocket services is a driving adapter — time is the caller; it implements no port, so `inbound`, not
  `outbound`; H6 lets a host pocket's inbound import framework rims; architecture-steward #6) runs
  `Components.Maintenance.Run(ctx)` and `Components.Mutations.ExpireOperations` on its own hourly ticker, and
  feeds `/healthz` from `LastReport()`: detail gains `maintenance_last_pass` (timestamp),
  `maintenance_steps_failed` (count) and `maintenance_last_error_kind` (a stable kind: `forbidden`,
  `below_floor`, `archive_failed`, `budget_exhausted`, `other`) — NEVER error text on an unauthenticated
  endpoint (platform-sre #11, product-manager #20); a maintenance failure never changes the status code (this
  is the readiness probe). One runner instance is the documented posture (the baseline is single-instance; a
  multi-replica host runs the runner on one replica or uses a shared object-store sink — README). `AUDIT_ARCHIVE_DIR`
  is REQUIRED in production and optional in development; a disk sink is ALLOWED with the ephemeral-disk caveat
  in RELEASING and the `.env.example` comment ("required in production; durable, backed up and shared across
  replicas; holds IP/UA (PII) — restrict access; not erased by Erase a person"); the README and the posture
  guide point at `integrations/filestorage/{gcs,s3}` for production. `workshop/migrations/main.go` prints
  `functions=`/`floor=` (PR-2b only). Ledger copies of 0023, 0024 (authentication) and 0007 (authorization)
  land IN THE SAME PRs as the store files (G34 compares the LOCAL store tree with the baseline ledger,
  `Makefile:625-650`); their baseline numbers are ASSIGNED AT MERGE (the next free numbers after whatever
  merged first — PR-2b and PR-3 are independent; no hard-coded 0029–0031; product-manager #8).
- **`examples/auth-cms`** (jobs-mode fenced queue, no Schedules service): the existing host ticker (`purge.go`,
  dated H10 debt per `examples/README.md` §8 — not made worse in kind: the new steps are a host pocket
  `pockets/retention/inbound/runner` shaped like the baseline's, which the ticker calls; lead-backend #24)
  gains the maintenance pass, `ExpireOperations`, `outbox.PurgePublished` (its own step), `PurgeBefore` on the
  wired inquiry eraser, beside the delivery purge; the erase route is opted in; `authmem` implements
  `Maintenance`, `UserErasure` and `CascadeDeleter`; its cms memstore `InquiryEraser`; `.env.example` gains
  `MAINTENANCE_INTERVAL`, `INVITATION_TTL`, `INVITATION_RETENTION` beside `DELIVERY_PURGE_*` (platform-sre #21).
  Each pocket PR lands its own example wiring in-workspace (product-manager #10); PR-7 only moves pins.
- **`examples/jobs-minimal`** shows the jobs-SCHEDULE form: a `retention` kind whose handler calls
  `Service.PurgeTerminalJobs(ctx, now−7d, 500)`, ensured with `schedules.Ensure{Name: "retention", Kind:
  "retention", Spec: schedules.Spec{Every: time.Hour}}`; `RETENTION_EVERY` documented in its README (it has no
  `.env.example`). It mounts no authentication: the authentication jobs-handler recipe is DOCUMENTED, not
  exercised (R-3, product-manager #3).

### Version table

| module | current | after S3 | why |
|---|---|---|---|
| `integrations/datastores/pgxdb` | v0.11.0 | **v0.12.0** | `DefinerFunction`, `AppendOnlyTable.Functions`, `FunctionPrivilege`, `UnsafeFunctions`/`InoperativeFunctions`, `ErrDefinerFunctionUnsafe`; `TablePrivilege` no longer comparable; posture unchanged |
| `integrations/datastores/turso` | v0.8.0 | untouched | the store uses `InTx` |
| `pockets/authentication` | v0.22.0 | **v0.23.0** | `logic/maintenance` (`Pass`/`Run`/`LastReport`); `Repositories.Maintenance`, `Repositories.UserErasure` (optional slots); three-valued `Status`, `EraseRepository`, `Erasure`; `serviceaccount.CascadeDeleter`; `WithMaintenance`/`MaintenanceConfig`; `AdministrationConfig.UserErasureRoute`; `UserAdminErase`; the erase route; `EraseUser`; `DeleteServiceAccount`; `(*Recorder).Prepare`; four event types; storetest groups `Maintenance`, `UserErasure`, `UserAdmin/SummaryReportsErased`, gated `ServiceAccounts/DeleteCascadesKeys`. No exported removal, no signature change |
| `…/stores/pgx` | v0.10.1 | **v0.11.0** | migrations 0023 + 0024 (REQUIRED: `erased_at` column probe; function probe); the maintenance and erasure stores; `AuditTables` lists the function; repins core v0.23.0 + pgxdb v0.12.0 |
| `…/stores/turso` | v0.9.0 | **v0.10.0** | migrations 0023 + 0024 (twin); capture-drop-restore; repins core v0.23.0 (turso connector pin unchanged) |
| `pockets/authorization` | v0.29.1 | **v0.30.0** | `OperationExpirer`, `Repositories.OperationExpiry`, `WithOperationRetention`, `Mutations.ExpireOperations`, the retryable Fence branch; memory store |
| `…/stores/pgx` | v0.20.0 | **v0.21.0** | 0007 index (admitted) + `Expire`; repins core v0.30.0 (pgxdb pin stays v0.11.0 — it lists no functions) |
| `…/stores/turso` | v0.19.0 | **v0.20.0** | 0007 + `Expire`; repins core |
| `pockets/jobs` | v0.6.0 | **v0.7.0** | `TerminalPurger`; `Service.PurgeTerminalJobs`; `PurgeTerminal` unchanged; memory store |
| `…/stores/pgx` | v0.6.0 | **v0.7.0** | 0005 index (admitted) + `PurgeTerminal` on the ordinary queue; repins core |
| `…/stores/turso` | v0.5.0 | **v0.6.0** | same |
| `pockets/cms` | v0.4.0 | **v0.5.0** | `InquiryEraser`, `Repositories.InquiryMaintenance`, the registration line; no Views change (R-8) |
| `…/views/goth` | v0.3.0 | untouched | R-8 |
| `…/stores/pgx` | v0.4.0 | **v0.5.0** | 0022 (`created_at` index, admitted) + the three methods; repins core |
| `…/stores/turso` | v0.3.0 | **v0.4.0** | 0022 (index) + the methods; repins core |
| `pockets/events`, `pockets/secrets` (+ store), `sdk`, `pockets` | — | untouched | recipes only; `filestorage.Storer` exists |
| `workshop/gopernicus` | v0.6.1 | **v0.7.0** | one P5 surface (a list edit is a workshop MINOR, RELEASING "Tagging scheme"), the runner template, the scaffold README, docs |

**Fourteen tags** in six steps (the rulings say thirteen; the enumerated list — pgxdb; authentication core + 2;
authorization core + 2; jobs core + 2; cms core + 2; workshop — is fourteen, and none can be dropped: see
Notes).

## Tasks — the PR train

Model `opus` for every task (`fable` only for PR-2a's port and function design pass, noted). Every PR branches
from `main` at or after 67a3fe2f; worktrees `../gopernicus-s3-<pr>`. Verify commands run from the repo root;
the live legs use a scratch PostgreSQL 17 on a free port (`lsof -i :<port>` first, **never 55432**) and a
local libsql-server. **The turso gate, used verbatim on EVERY turso leg** (the rev-1 one-liner passed on skips
AND on failures — without `-v` there is no `--- SKIP` line and the pipeline's status was grep's;
data-integration #1):
```
set -o pipefail; TURSO_DATABASE_URL=http://127.0.0.1:8081 TURSO_AUTH_TOKEN=local go test -v -count=1 -tags=integration ./... 2>&1 | tee leg.log && ! grep -q -- '--- SKIP' leg.log
```
(authorization adds `AUTHORIZATION_TURSO_DISPOSABLE_URL=http://127.0.0.1:8081` on the same line; `leg.log`
is written in the scratchpad, never the tree). Every full run on a **fresh database and a fresh libSQL
server** (S2 follow-ups Known: the turso schema-probe test drops the shared `schema_migrations` ledger; the
authentication pgx upgrade tests need a fresh database); the 0023/0024 upgrade tests run in the
disposable-schema fixture (`newAuditLive` + `migrationsThrough`) on pgx — **0024 applied by the NON-superuser
fixture migrator (`l.migrator`), never the admin connection** (CI's `postgres` is a superuser and passes every
owner check trivially; platform-sre #13) — and on a `t.TempDir()` file on turso. Retarget a stacked child to
`main` BEFORE merging it; never `--delete-branch` a stacked base; one push, tag or merge per command. PR titles
name symbols and migrations, never the abuse shapes this file describes. Store `go.mod` pins move in SEPARATE
repin PRs (the S2 precedent: #150, #146, #145), each cut at its own merge commit after CI and the live legs.

### PR-1 — pgxdb: definer-function evidence on the append-only probe (`integrations/definer-functions`)

- **files:** `integrations/datastores/pgxdb/probe.go` (`DefinerFunction`, `AppendOnlyTable.Functions`,
  `FunctionPrivilege`, `TablePrivilege.Functions`, `UnsafeFunctions()`, `InoperativeFunctions()`, the
  `to_regprocedure` resolution in the PRE-PIN phase beside `to_regclass`, the same-namespace check, the
  `proconfig` comparison computed in SQL with `quote_ident` and exactly one element, `rolsuper`,
  `has_schema_privilege(… 'CREATE')`, `ErrDefinerFunctionUnsafe`, the production refusal / development line /
  the WARNs in `ProbeAppendOnly`, `ProtectedBy` unchanged), `probe_test.go` (the policy matrix over a fake
  report: unsafe = PUBLIC EXECUTE / search_path unpinned or wrong / foreign namespace; inoperative = not
  definer / foreign owner / superuser owner / CREATE on the schema; `Writable` untouched by any function
  state), `probe_live_test.go` (a fixture migrator creates a definer function THROUGH THE SAME `DO`/`ALTER`
  RECIPE 0024 uses, under a schema whose name needs quoting (`"S3-Mixed"`): clean; `ALTER … SECURITY INVOKER`
  → inoperative; `ALTER … RESET search_path` → unsafe; `GRANT EXECUTE … TO PUBLIC` → unsafe; owned by another
  role → inoperative; a same-named decoy function in an earlier schema on the runtime path → `namespace`
  finding, unsafe; a bare-schema fixture resolves the function BEFORE the pin; absent → `Exists=false`, no
  error; EXECUTE granted/revoked for the inspected role; a `LANGUAGE sql` floor-style function NOT listed →
  no finding), `README.md` ("Definer functions on an append-only table": the rules, the refuse/WARN split, the
  `-posture-role` line, `TablePrivilege` no longer comparable).
- **behavior:** stores that list no functions see byte-identical probe output; stores that do get the checks;
  posture (`Writable`, `CannotAppend`) unchanged.
- **BREAKING:** no (`TablePrivilege` stops being comparable — recorded). **escape hatch:** none for
  `ErrDefinerFunctionUnsafe` (the host created the hole by hand; the fix text names the `REVOKE`/`ALTER`);
  inoperative states WARN.
- **tests:** above; `errors.Join` names every unsafe function; the no-env-tag test unchanged.
- **verify:**
  ```
  cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...
  make check
  make test-stores                                      # scratch Postgres 17; the pgxdb live leg incl. the quoted-schema and decoy cases
  ```
- **closes:** the preflight half of FAS-2. **tag:** step 1 — `integrations/datastores/pgxdb/v0.12.0` at the
  merge commit, pushed, THEN polled on `.info`, cold-verified — **cut ONLY after PR-2b's branch has passed its
  pgx live legs (bare + schema-qualified) against the REAL 0024** (platform-sre #15b, data-integration #26: a
  quoting or `proconfig` mismatch would otherwise cost a v0.12.1 and a workshop re-pin); the cold-verify
  builds AND tests EVERY released pgx store on v0.12.0 from a scratch consumer (authentication v0.10.1,
  authorization v0.20.0, secrets v0.2.1, jobs v0.6.0, cms v0.4.0, events) — the MVS SET a host adopting any S3
  store pulls (platform-sre #15c).

### PR-2a — authentication core: the maintenance service, erasure, the vocabulary, storetest, G36 (`authentication/retention-erasure-core`)

Branches from `main` (needs nothing from PR-1). Model `fable` for the port/function design pass over
`logic/maintenance/repository.go`, `user/erase.go` and the 0024 SQL text (which PR-2b ships but PR-2a's
`MinSecurityEventRetention`/`MaxBatchSize` tests pin), `opus` for the rest.

- **files:** new `pockets/authentication/logic/maintenance/{repository.go (Repository, ArchivedEvent,
  ExportCursor, PurgeResult, ErrBelowRetentionFloor, the non-transactional doc), config.go (Config,
  MinSecurityEventRetention, MaxBatchSize, ArchiveSink, the refusals incl. ErrSecurityEventRetentionTooShort),
  service.go (New, Pass, the drain loop, the per-chunk archive-then-purge, the panic recovery, Report, the
  exported StepName constants, the private key generator), run.go (Run with WithInterval and the start jitter;
  LastReport), archive.go (the NDJSON wire record v1; the read-back digest)}` + tests (a fake repository and a
  fake sink; object keys never overwrite — `Exists` then a fresh id; an empty key refused; a sink failure
  mid-chunk → no purge and the step fails; a read-back mismatch → no purge; the archived row write failing →
  no purge; a backlog larger than one budget (`PassBudget=1s`, 50k fake rows) → progress every pass, no row
  purged unless its chunk's object verified, `BudgetExhausted` set; a failed step does not stop the pass; a
  panicking step is recovered; skipped steps named with reasons; `not_due` for the rail step inside the
  interval; the first pass runs at start; two concurrent `Pass`es; `LastReport` after each); `logic/
  authentication/user/{status.go (StatusErased three-valued; ErrErased; the doc rewrite), erase.go
  (EraseRepository, EraseInput incl. Event, Erasure, EraseCounts, ErrEraseBlockedByClaim)}`;
  `logic/authentication/serviceaccount/{repository.go (CascadeDeleter, Deletion)}`; `logic/authentication/
  {useradmin.go (EraseUser returning Summary + Erasure, ErrEraseSelf, ErrUserErasureUnavailable, the
  WithoutCancel + EraseTimeout call, SubjectKeys via magicLinkSubjectKey, the prepared user_erased row),
  machine.go (DeleteServiceAccount with the cascade fallback and the service_account_deleted row),
  securityevent/{securityevent.go (the four types; the registry test), recorder.go (Prepare factored out of
  Record)}}`; `inbound/http/{host_policy.go (UserAdminErase), useradmin.go (the route, mounted only with the
  slot AND UserErasureRoute; 409/400 mapping)}`; root `repositories.go` (the two optional slots with the
  same-adapter sentence; `ActiveSessions` required beside `UserErasure`), `config.go` (`WithMaintenance`, the
  root `MaintenanceConfig`, `AdministrationConfig.UserErasureRoute`, `ErrMaintenanceAcknowledgedWithoutRepository`,
  `ErrUserErasureRouteRequires`, `ErrUserErasureRequiresActiveSessions`, the no-env-tag test), `constructor.go`
  (build `Components.Maintenance`; the production WARNs; `maintenance_runner=`/`archive_sink=`/
  `security_event_retention=` in the registration log), `authentication.go` (the field); `stores/storetest/
  {storetest.go (the new groups; the gated ServiceAccounts/DeleteCascadesKeys case; UserAdmin/SummaryReportsErased),
  maintenance.go, erasure.go, reference_test.go (the reference implements both ports, CascadeDeleter and the
  in-transaction purge row)}`; `Makefile` (G36 `guard-auth-maintenance-leaf`, header, aggregate line,
  "thirty-three" → "thirty-four"), `.github/workflows/check.yml` prose; `README.md` ("Retention and the
  maintenance service" — the retention table, `Pass`/`Run`/`LastReport`, the acknowledgement, the per-chunk
  archive and its wire format, the floor as an invariant, the `queue.HandlerFunc` recipe with the real
  `schedules.Ensure` shape and the error contract, "Maintenance must be backed by the database that holds the
  rail"; "Account lifecycle" gains "Erasure" — the transaction order, what is kept and why, "not a ban", the
  accepting-claim refusal, the route opt-in, archives out of reach; the vocabulary list +4);
  `examples/auth-cms/pockets/authentication/outbound/authmem/{maintenance.go, erasure.go, ports_v2.go
  (CascadeDeleter)}`, `examples/auth-cms/pockets/retention/inbound/runner/` + `cmd/server/{purge.go,
  main.go, authentication.go}` (the maintenance pass on the ticker; the erase route opt-in; `.env.example`
  keys) — the example wiring lands here, in-workspace (PR-2a's verify already requires auth-cms green).
- **behavior:** the Design sections above. `SetStatus` on an erased user: deactivate → `Changed:false`,
  reactivate → `ErrErased` from the store (the reference and authmem too).
- **BREAKING:** no exported signature change; two optional `Repositories` slots; `Status.Valid()` gains a
  value (hosts switching on `Status` exhaustively get a new case — RELEASING).
- **escape hatch:** leave the slots nil (the service names the steps skipped; `EraseUser` →
  `ErrUserErasureUnavailable`); `MaintenanceRunnerAcknowledged` is the production WARN's switch.
- **tests:** storetest cases, named: `Maintenance/{PurgeExpiredSessionsKeepsLiveAndBoundary,
  PurgeExpiredOAuthStates, PurgeExpiredGrants, PurgeExpiredContactChanges,
  PurgeInvitationsTerminalAndExpiredPending, PurgeInvitationsKeepsAcceptingAndLivePending,
  CountStaleAcceptingInvitations, PruneOAuth2Bounded, ExportSecurityEventsOrderedCursoredAndTiesWhole,
  PurgeSecurityEventsAppendsPurgeRowEachCall, PurgeSecurityEventsBoundaryAndLimit,
  PurgeSecurityEventsFloorPlusOneDayAcceptedMinusOneRefused (never exactly at the floor), LimitZeroRefused,
  ConcurrentPassesAreSafe}` (cutoffs relative to `time.Now()`, microsecond-aligned fixtures, never derived from
  `suiteBase`; rail fixtures older than 365d relative to the DB clock), `UserErasure/
  {EraseIsOneTransactionAndIdempotent, EraseBlanksIdentifiersReadBack,
  EraseDeletesCredentialsSessionsGrantsCodesChallenges, EraseDeletesAddressKeyedMagicLinkChallenge,
  EraseInvitationsPendingDeletedTerminalBlankedIdsKeptIssuedCancelled, EraseMatchesPhoneInvitation,
  RetiredAddressNowOwnedByAnotherUserUntouched, EraseRefusedWhileAcceptingClaimHeld,
  EraseDeletesOwnedActAsServiceAccountsAndKeysKeepsOthers, EraseWritesUserErasedRowInTransaction,
  EraseLeavesSecurityEventsIntact, EraseRefusesEmptyAndUnknown, SetStatusAfterErase, MintFencedAfterErase}`,
  `UserAdmin/SummaryReportsErased`, `ServiceAccounts/DeleteCascadesKeys` (gated); service tests: `EraseUser`
  self-refusal, the prepared row carries the admin's client info and counts and no address, the WARNs (one
  each, scoped), the contradiction and construction errors, `EraseUser` on a 409; HTTP: the route absent
  without the opt-in, present with it, the `Action` reaches the check. Run-and-look: `cd examples/auth-cms &&
  AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 go run ./cmd/server`; erase a seeded non-admin user as the admin →
  200 with counts; `GET /auth/admin/users/{id}` → status `erased`, blank name/email; a login as that user →
  401; `GET /debug/security-events` shows `user_erased` with counts, and that user's earlier `login` rows are
  UNCHANGED (R-7); repeat the erase → 200 `changed:false`; the ticker logs a maintenance report at
  `MAINTENANCE_INTERVAL=1m`.
- **verify:**
  ```
  cd pockets/authentication && go build ./... && go test ./... && go vet ./...
  cd examples/auth-cms && go build ./... && go test ./... && go vet ./...
  make guard                                            # G20, G33 (the service reads no env), G36 (maintenance never imports the root service)
  make check
  ```
- **closes:** FAS-3, FAC-7 (erasure + retention halves), FAC-9's cascade, FAS-2 (the service half), FAF-11
  (the driver for the bounded prune), H6's authentication half. **tag:** step 2a `pockets/authentication/v0.23.0`
  after PR-2a merges and PR-2b's live legs pass on its branch (PR-2b's core-side changes: **none**).

### PR-2b — authentication stores: 0023 + 0024, the maintenance and erasure stores, the probes, G35, the live proof (`authentication/retention-erasure-stores`)

PR-1 merges to `main` first; PR-2b branches from `main` stacked on PR-2a (one base).

- **files:** `pockets/authentication/stores/pgx/migrations/0023_retention.sql` (`SET LOCAL lock_timeout = '5s'`;
  **indexes FIRST**, each `CREATE INDEX IF NOT EXISTS <exact name>`: `idx_sessions_expires_at`,
  `idx_authentication_grants_expires_at`, `idx_contact_changes_expires_at`, `idx_oauth_states_expires_at`,
  `idx_challenges_expires_at`, `idx_oauth_authorization_codes_expires_at`, `idx_oauth_clients_expires_at`,
  `idx_oauth_refresh_history_expires_at` (the purge predicates have no access path today, `0003:38-47`, 0019;
  invitations get none — one row per invite, human-scale, stated); **`ALTER TABLE users ADD COLUMN IF NOT
  EXISTS erased_at TIMESTAMPTZ NULL` LAST**), `0024_security_events_retention.sql` (the assertion block, the
  two functions, the `ALTER … SET search_path` block, the `REVOKE`), `stores/turso/migrations/0023_retention.sql`
  (same column + indexes), `0024_security_events_retention.sql` (the floor table, `IF NOT EXISTS` + `INSERT OR
  IGNORE`); `stores/pgx/{maintenance.go (the purges with `FOR UPDATE SKIP LOCKED` subselects; `PruneOAuth2`;
  the export with whole-instant pages; the function call with casts, the 22023 mapping and the function-only
  GRANT hint on 42501), erasure.go (the transaction in the Design order incl. the in-transaction `user_erased`
  INSERT), service_accounts.go (the cascade with `FOR UPDATE`; `CascadeDeleter`; the owner `FOR SHARE` +
  erased refusal on insert), api_keys.go (`FOR SHARE` on the account), user_admin.go (`ErrErased` on
  reactivate, `Changed:false` on deactivate, in the locked read; the projection in `GetSummary`/`List`),
  users.go + passwordless.go (the projection in every `Get`), postgres.go (`probeColumns` += `{users,
  erased_at, 0023_retention.sql}`; the function and floor probe naming 0024 when absent; `AuditTables` lists
  the one `DefinerFunction`; `Repositories` fills the slots; the EXECUTE WARN scoped to `Maintenance`),
  migrations_test.go (0023/0024 counted; tests pin the 365-day literal to `MinSecurityEventRetention − 24h` and
  the 10000 literal to `MaxBatchSize`), maintenance_live_test.go (`TestLive_SecurityEventsPurgeFunction`: a
  fixture runtime role with EXECUTE deletes and appends the row; `max_rows` clamp; floor −1d → `ErrBelowRetentionFloor`,
  +1d accepted; count 0 still appends; without EXECUTE → `sdk.ErrForbidden` with the GRANT text and a
  production boot that WARNs once and reports the step `forbidden` while the other steps run; PUBLIC EXECUTE
  re-granted → production `Repositories` refuses with `ErrDefinerFunctionUnsafe`; `SECURITY INVOKER` altered →
  WARN + call-time error; function dropped → refusal naming 0024; the runtime-role NEGATIVES, each 42501:
  `CREATE OR REPLACE FUNCTION security_events_retention_floor()`, `ALTER FUNCTION … SECURITY INVOKER`, `ALTER
  … RESET search_path`, `DROP FUNCTION`, `ALTER FUNCTION … OWNER TO <runtime>`; under `POSTGRES_TEST_SCHEMA`
  (a name needing quotes) the call and the `proconfig` are schema-qualified; the bare leg resolves before the
  pin; a purge concurrent with an Erase on overlapping old rows — no 40P01; the 0024 schema-mismatch assertion
  fires in a fixture whose `current_schema()` differs; an EXPLAIN check that the new `expires_at` indexes are
  used), erasure_live_test.go (`TestLive_EraseVersusClaim`: a concurrent `ClaimAcceptance` on a locked row —
  the claim sees `ErrNotFound` or the erase refuses, never a half state;
  `TestLive_CreateServiceAccountVersusErase`: the insert waits on the owner lock and refuses), upgrade_live_test.go
  (0023 applied while a concurrent session reads `users` — the read waits only for the `ADD COLUMN`
  statement; a pre-built `CONCURRENTLY` index makes the in-stream build a no-op), README.md ("Retention: `0023`
  and `0024`" — the functions, the floor as an invariant, `GRANT EXECUTE`, the probe's refuse/WARN split and
  what it does not compare, the index pre-build recipe, `-posture-role functions= floor=`)}`; `stores/turso/
  {maintenance.go (capture-drop-restore in the Design order; `floor_days`; the post-commit check), erasure.go,
  service_accounts.go, api_keys.go, user_admin.go, users.go, passwordless.go, turso.go (`probeColumns` +=
  `erased_at`; the floor row probe naming 0024), migrations_test.go, maintenance_localsqlite_test.go
  (UNTAGGED: the file cases incl. fault injection), maintenance_integration_test.go (the libSQL-server
  cases), conformance_integration_test.go (`truncate()` excludes the floor table; the survive-truncate case),
  README.md}`; `examples/production-baseline/workshop/migrations/primary/<next>_retention.sql`,
  `<next>_security_events_retention.sql` (byte-identical copies — G34, same PR; numbers assigned at merge) and
  `workshop/migrations/main.go` (`functions=`/`floor=` in `reportPosture`); `Makefile` (G35 with the per-line
  allow-list naming the `maintenance.go` statement lines; "thirty-four" → "thirty-five"), `check.yml` prose.
  **No `go.mod` pin moves here** (PR-2c).
- **behavior:** Design "The SECURITY DEFINER function" and "turso". Stores refuse a database without 0023
  (column) or 0024 (function / floor row) in every mode, naming the file; production refuses an UNSAFE definer
  function and WARNs on an inoperative one; a missing EXECUTE WARNs when `Maintenance` is wired.
- **BREAKING:** stores repinning v0.11.0 / v0.10.0 refuse to boot until 0023 and 0024 are applied
  (migrate-then-deploy: an old binary keeps running on a migrated database — a nullable column, indexes,
  functions it never calls). Apply the S3 files in their own runner invocation; pre-build the indexes
  `CONCURRENTLY` on a large host (the recipe).
- **escape hatch:** none for the migrations; `AppendOnlyWritableAcknowledged` is unrelated (posture unchanged).
- **tests:** storetest over pgx (bare + schema) and turso (local libSQL + `LocalSQLite`); the live cases;
  the pre-migration row has `erased_at IS NULL` and reads `deactivated`/`active` unchanged; G35 catches a
  planted literal line and a planted concatenated line.
- **verify:**
  ```
  cd pockets/authentication/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/authentication/stores/turso && go build ./... && go test ./... && go vet ./...
  make guard                                            # G34 (the two baseline copies), G35 (per-line allow-list, both spellings)
  make check
  make test-stores                                      # fresh scratch Postgres 17: authentication pgx bare + schema incl. the function/erase/upgrade live cases, 0024 applied by the fixture migrator
  cd pockets/authentication/stores/turso && <the turso gate>
  # Linux: `make test-stores` end to end in a golang:1.26.10 container against a fresh database (S2 follow-ups precedent), then a `live-stores` dispatch on this branch
  ```
- **closes:** FAS-2 (store half), the M3 residue (by retention). **tag:** none here — the store tags are cut
  on PR-2c.

### PR-2c — authentication stores repin (`authentication/stores-repin-s3`)

Opens after tags 1 and 2a resolve on the proxy. `GOWORK=off go get` in both store modules → core v0.23.0
(pgx also pgxdb v0.12.0), `GOWORK=off go mod tidy`, nothing else. **verify:** `make check`; `make test-stores`
on the merge commit; a `live-stores` dispatch on `main`. **tag:** step 2b `pockets/authentication/stores/pgx/v0.11.0`,
`…/stores/turso/v0.10.0` at the repin's merge commit.

### PR-3 — authorization: `OperationExpirer`, `Repositories.OperationExpiry`, `Mutations.ExpireOperations`, 0007 (`authorization/operations-expiry`)

Branches from `main`; independent of PR-1/2.

- **files:** `pockets/authorization/logic/mutations/{repository.go (OperationExpirer; the Fence/Recorded doc
  amendment), mutation_service.go (ExpireOperations, ErrOperationExpiryNotConfigured, the retention option;
  the retryable "conflicted but no record" branch + its deterministic race test), options.go}`, root
  `repositories.go` (`OperationExpiry`), `options.go` (`WithOperationRetention`, `ErrOperationRetentionTooShort`,
  `ErrOperationRetentionWithoutExpirer`), `constructor.go` (threads retention into Mutations; `operation_expiry=`
  in the registration log; the production WARN), `stores/memory/…` (the interface), `stores/pgx/{operations.go
  (Expire: `FOR UPDATE SKIP LOCKED`, `ErrMutationInsideTransaction` inside an ambient tx, no lock),
  migrations/0007_iam_operations_expiry.sql (`lock_timeout`; `CREATE INDEX IF NOT EXISTS`), postgres.go (the
  index WARN), migrations_test.go, schema_probe_test.go (0007's index admitted)}`, `stores/turso/{operations.go,
  migrations/0007_…, schema tests}`, `stores/storetest/operation_ledger.go` (`OperationLedger/{ExpireKeepsFenced,
  ExpireBoundedAndBoundary, ExpireLimitZeroRefused, ExpireThenFenceRecordsFenced}` with `before = now + δ`),
  `stores/UPGRADE.md` ("Adopting 0007 — additive, admitted; pre-build `CONCURRENTLY` on a large ledger; apply
  alone"), `README.md` (":501-505" rewritten; the retention rule, the accepting-claim and half-retention
  sentences, "an expired operation id reads as never recorded"), `examples/production-baseline/workshop/
  migrations/primary/<next>_iam_operations_expiry.sql` (G34, same PR; number at merge) + `cmd/server/main.go`
  (`WithOperationRetention(180d)` in-workspace), `examples/auth-cms/cmd/server/…` (`ExpireOperations` on the
  runner). **No store pin moves** (PR-3b).
- **behavior:** fenced rows survive any `Expire` (SQL filter + the S2 trigger, proven live by deleting with a
  fixture role: non-fenced pass, fenced refused — the S2 case extended with the new method).
- **BREAKING:** no. **escape hatch:** leave the slot nil (no WARN in development; a production WARN).
- **tests:** above; live pgx bare + schema; turso integration with the gate.
- **verify:**
  ```
  cd pockets/authorization && go build ./... && go test ./... && go vet ./...
  cd pockets/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/authorization/stores/turso && go build ./... && go test ./... && go vet ./...
  make guard && make check
  make test-stores
  cd pockets/authorization/stores/turso && <the turso gate, with AUTHORIZATION_TURSO_DISPOSABLE_URL>   # re-proves S2's authorization libSQL leg
  ```
- **closes:** FAZ-8. **tag:** step 3a `pockets/authorization/v0.30.0` at the merge commit after the live legs.

### PR-3b — authorization stores repin (`authorization/stores-repin-s3`)

After tag 3a resolves: `GOWORK=off go get` → core v0.30.0 in both stores, tidy. **verify:** `make check`;
`make test-stores`; a `live-stores` dispatch on `main`. **tag:** step 3b `…/stores/pgx/v0.21.0`,
`…/stores/turso/v0.20.0` at the repin's merge commit.

### PR-4 — jobs: `TerminalPurger`, `Service.PurgeTerminalJobs`, 0005 (`jobs/ordinary-terminal-purge`)

Branches from `main`; independent.

- **files:** `pockets/jobs/logic/queue/{job.go (TerminalPurger), work.go (PurgeTerminalJobs;
  ErrTerminalPurgeRequired; `PurgeTerminal` byte-identical), service.go (the one-time resolution;
  `terminal_purge=` in the registration log; the production WARN), service_test.go}`, `stores/memory/queue.go`,
  `stores/pgx/{queue.go, postgres.go (the index WARN), migrations/0005_job_queue_terminal_purge.sql
  (`lock_timeout`; `CREATE INDEX IF NOT EXISTS`), schema_guard_test.go}`, `stores/turso/{queue.go,
  migrations/0005_…}`, `stores/storetest/storetest.go` (`Queue/BoundedTerminalPurge`: skips loudly without the
  interface; the boundary row; `limit<=0` refused; running/pending rows survive), `README.md` (retention
  paragraph; the refire caveat; "payloads are stored as submitted"); `examples/jobs-minimal/cmd/server/main.go`
  (the `retention` schedule with the real `schedules.Ensure` shape, in-workspace) + `README.md`
  (`RETENTION_EVERY`). **No store pin moves** (PR-4b).
- **BREAKING:** no. **escape hatch:** do not schedule it.
- **verify:**
  ```
  cd pockets/jobs && go build ./... && go test ./... && go vet ./...
  cd pockets/jobs/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/jobs/stores/turso && go build ./... && go test ./... && go vet ./...
  make guard && make check
  make test-stores
  cd pockets/jobs/stores/turso && <the turso gate>
  ```
- **closes:** FOP-6, FOP-11 (jobs half). **tag:** step 4a `pockets/jobs/v0.7.0`.

### PR-4b — jobs stores repin (`jobs/stores-repin-s3`)

After tag 4a: `GOWORK=off go get` → core v0.7.0, tidy. **verify:** `make check`; `make test-stores`; a
`live-stores` dispatch on `main`. **tag:** step 4b `…/stores/pgx/v0.7.0`, `…/stores/turso/v0.6.0`.

### PR-5 — cms: inquiry erasure, `Repositories.InquiryMaintenance`, 0022 (`cms/inquiries-retention`)

Branches from `main`; independent. No HTTP surface, no view (R-8).

- **files:** `pockets/cms/domain/messaging/repository.go` (`InquiryEraser`; the ASCII case-fold doc),
  `pockets/cms/cms.go` (`Repositories.InquiryMaintenance`; the registration line; the production WARN),
  `stores/pgx/{inquiries.go, postgres.go (the index WARN), migrations/0022_inquiries_created_at.sql}`,
  `stores/turso/{inquiries.go, migrations/0022_…}`, `pockets/cms/storetest/storetest.go` (`Inquiries/
  {DeleteAbsentNotFound, DeleteByEmailCaseInsensitive, PurgeBeforeBounded, PurgeBeforeLimitZeroRefused}` — skip
  loudly without the interface), `examples/minimal/pockets/cms/outbound/memstore/memstore.go`,
  `examples/auth-cms/pockets/cms/outbound/memstore/memstore.go` (`InquiryEraser`), `examples/auth-cms/cmd/server/…`
  (`PurgeBefore` on the runner, in-workspace), every example ledger that mounts a cms store (P9 under G32
  names it — a file copy), `README.md`. **No store pin moves** (PR-5b).
- **BREAKING:** no. **escape hatch:** a store without the interface keeps today's behavior and a production
  WARN.
- **tests:** above; the run-and-look is the auth-cms ticker log line `step=inquiries count=<n>` after
  submitting a contact form and backdating it (there is no UI in S3).
- **verify:**
  ```
  cd pockets/cms && go build ./... && go test ./... && go vet ./...
  cd pockets/cms/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/cms/stores/turso && go build ./... && go test ./... && go vet ./...
  make guard && make check
  make test-stores
  cd pockets/cms/stores/turso && <the turso gate>                       # the cms turso leg, explicit (data-integration #1)
  ```
- **closes:** FOP-7's disposal half (the paged list is the follow-up's). **tag:** step 5a `pockets/cms/v0.5.0`.

### PR-5b — cms stores repin (`cms/stores-repin-s3`)

After tag 5a: `GOWORK=off go get` → core v0.5.0 in both stores, tidy. **verify:** `make check`; `make
test-stores`; a `live-stores` dispatch on `main`. **tag:** step 5b `…/stores/pgx/v0.5.0`, `…/stores/turso/v0.4.0`.

### PR-6 — workshop and docs: the P5 surface, the runner template, the scaffold README, the guides (`workshop/retention`)

Opens after step 1 is pushed and resolves (`TestWarmScaffoldModuleCache`).

- **files:** `workshop/gopernicus/internal/commands/{posture_rules.go (MaintenanceRunnerAcknowledged),
  guard_test.go, templates/init/{migrations.main.go.tmpl (functions=, floor=), readme.md.tmpl ("Retention"),
  go.mod.tmpl (pgxdb v0.12.0)}, scaffold_test.go}`, `examples/README.md` §3b (the list), `workshop/
  documentation/docs/guides/{production-posture.md, database-posture.md, operations-runbooks.md}`,
  `docs/pockets/{authentication, authorization, jobs, cms}.md`, `docs/workshop/commands.md`.
- **BREAKING:** no. **escape hatch:** none.
- **tests:** a fixture for the surface (`ack`; a non-literal is a P5 finding); a cold `init --db pgx` builds on
  pgxdb v0.12.0 and is guard-clean.
- **verify:**
  ```
  cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...
  make guard-host-posture && make guard && make check
  ```
- **tag:** step 6 `workshop/gopernicus/v0.7.0`.

### PR-7 — examples: pin moves, the baseline runner and `/healthz` (`examples/retention`)

Opens after tags 1–5b resolve (the `go.mod` pins move by explicit `go get`).

- **files (baseline):** new host pocket `examples/production-baseline/pockets/retention/inbound/runner/
  runner.go` (the loop driving `Maintenance.Run` and `Mutations.ExpireOperations`; the `/healthz` snapshot
  from `LastReport()`; H6) + test; `cmd/server/main.go` (`WithMaintenance` with the disk sink at
  `AUDIT_ARCHIVE_DIR` and `ArchiveLocation`, `MaintenanceRunnerAcknowledged: true`, `UserErasureRoute: true`,
  the `/healthz` detail: timestamp, failed-step count, error kind); `cmd/server/production_test.go`;
  `.env.example` (`AUDIT_ARCHIVE_DIR` with the full comment); `README.md` ("Retention" and "Erase a person"
  sections; the `GRANT EXECUTE`, `REVOKE CREATE`, `NOSUPERUSER` lines in "Two roles"; one runner instance; the
  archive sink caveat and the gcs/s3 pointer); `go.mod` pins.
- **files (every touched example):** `examples/{auth-cms, production-baseline, jobs-minimal, cms, minimal}/go.mod`
  repinned (`cms` and `minimal` pin cms v0.3.0 today and PR-5 touched their memstores; platform-sre #16);
  `examples/README.md` §8 rows.
- **BREAKING:** no (examples are never tagged). **escape hatch:** none — the baseline shows zero waivers and
  one acknowledgement that `--posture` prints as `ack`.
- **tests:** every touched example's `go test ./...` AND `GOWORK=off go build ./...`; `guard --posture` on the
  baseline prints exactly the one new `ack` line and P9 clean; run-and-look = Verification 1–6.
- **verify:**
  ```
  for e in production-baseline auth-cms jobs-minimal cms minimal; do (cd examples/$e && go build ./... && go test ./... && go vet ./... && GOWORK=off go build ./...); done
  cd workshop/gopernicus && go run . guard --posture ../../examples/production-baseline
  make guard-host-contract && make guard && make check
  ```
- **tag:** none.

### PR-8 — release record, handoff, manifest, executed copy (`docs/s3-release-record`)

- **files:** `RELEASING.md` ("Retention and erasure" section: the fourteen tags, cold verification incl. the
  MVS SET of step 1, migrate-then-deploy for 0023/0024/0007/0005/0022 with "apply alone" and the `CONCURRENTLY`
  pre-build, the rollback story, the `GRANT EXECUTE`/`REVOKE CREATE`/`NOSUPERUSER` lines the two-role recipe
  gains, the definer-function refuse/WARN split, the floor as an invariant, the capture-drop-restore rule,
  `TablePrivilege` not comparable, the disk-sink caveat, G35/G36, and the **surprise list**: the DB says
  `deactivated` while the API says `erased` (`status:"erased"` breaks clients validating a closed enum; the
  query is `erased_at IS NOT NULL`); `Status.Valid()` has a third value; system rail rows (filter
  `actor_type='system'`); accepted invitations deleted 90 days after acceptance; two-role hosts that skip
  `GRANT EXECUTE` see the rail step `forbidden` every pass; the migrator must own `security_events` or
  production refuses — run `-posture-role` first; host stores implementing `CascadeDeleter` run the cascade
  case; erase is not a ban; `ExpireOperations` makes an expired id read as never recorded;
  `OperationRetention` must exceed the oldest open accepting claim; a host with its own retention cron leaves
  the acknowledgement false — product-manager #19, platform-sre #22); `plans/soc2-s3-retention-erasure-handoff.md`
  (per host: the files to export in order and alone, the index pre-builds with the `indisvalid` check, the
  roles' grants, the runner to wire and which form, `OperationRetention` versus the oldest accepting claim,
  the archive sink and its lifecycle, the "first week" backlog section, the dashboards and the 48 h alert,
  the sweep query; **"the handoff is written now and applied at the roadmap's end"**);
  `plans/s3-retention-erasure-release-manifest.json`; `plans/s3-retention-erasure.md` (executed copy); this
  file's status line; `roadmap.md` (S3 → done; deviations listed under Rulings recorded); `control-map.md`
  (CC6.2 "Removal of data on offboarding" → DEFAULT/OPT-IN (the port; the route opt-in); CC6.2 "Machine
  identity deprovisioning" → PARTIAL (cascade S3, disable S4); CC6.5 "Retention schedule enforceable per
  table" → DEFAULT (ports) / OPT-IN (runner) / HOST (`ratelimit_windows`, `iam_audit`, `secrets_audit`) — the
  other CC6.5 row (crypto-shredding, S0) is untouched; CC7.2 "Audit retention/export" → DEFAULT for
  `security_events` only, HOST for the two other rails, turso floor "a footgun guard, not a control"; C1
  "Retention and disposal" → the new text — product-manager #9); a note that S2's authorization libSQL leg is
  re-proven by PR-3's gate.
- **verify:** `make guard`. **tag:** none.

### Sequencing

```
PR-1 pgxdb ──────────────────────────────────┐
PR-2a authentication core (from main) ────────┤
PR-2b stores (from main, stacked on 2a; PR-1 merged first) ─ [branch live legs: pgx bare + schema vs the REAL 0024; turso gate; Linux container; live-stores dispatch]
   ─ [tag 1: pgxdb v0.12.0; MVS SET cold-built + tested] ─ [tag 2a: authentication core] ─ PR-2c repin ─ [live-stores dispatch on main] ─ [tag 2b ×2]
PR-3 authorization ─ [live legs] ─ [tag 3a] ─ PR-3b repin ─ [dispatch] ─ [tag 3b ×2]      (parallel worktree; independent of 1/2)
PR-4 jobs          ─ [live legs] ─ [tag 4a] ─ PR-4b repin ─ [dispatch] ─ [tag 4b ×2]      (parallel worktree)
PR-5 cms           ─ [live legs incl. the cms turso gate] ─ [tag 5a] ─ PR-5b repin ─ [dispatch] ─ [tag 5b ×2]   (parallel worktree)
PR-6 workshop      ─ [tag 6]                                                                (after tag 1 resolves)
PR-7 examples                                                                               (after tags 1–5b resolve)
PR-8 record
```

PR-3, PR-4, PR-5 are independent of PR-1/2 and of each other; their baseline ledger numbers are assigned at
merge. Every store tag follows its repin PR's live legs and a `live-stores` dispatch on merged `main` (the
`<=` boundary cases with microsecond fixtures are the bug class Linux exposed in `RotationKeepsExpiresAt` —
platform-sre #14); the authentication store tags additionally follow the Linux-container `make test-stores`
on the PR-2b branch, every full run against a fresh database and a fresh libSQL server.

## Release — the tag train

| step | tag(s) | after | repins |
|---|---|---|---|
| 1 | `integrations/datastores/pgxdb/v0.12.0` | PR-1 merged + PR-2b's branch live legs against the real 0024 (bare + quoted schema); cold-verify builds and tests EVERY released pgx store on v0.12.0 (authentication v0.10.1, authorization v0.20.0, secrets v0.2.1, jobs v0.6.0, cms v0.4.0, events) | none |
| 2a | `pockets/authentication/v0.23.0` | PR-2a merged; PR-2b's branch legs (pgx bare + schema, turso gate on libSQL + `LocalSQLite`, authmem, `TestReference`); PR-2b merged | none (sdk v0.16.0) |
| 2b | `pockets/authentication/stores/pgx/v0.11.0`, `…/stores/turso/v0.10.0` | PR-2c (repin) merged after tags 1 + 2a resolve; `make test-stores` + the Linux container run + a `live-stores` dispatch on `main` | pgx: core v0.23.0 + pgxdb v0.12.0; turso: core v0.23.0 |
| 3a / 3b | `pockets/authorization/v0.30.0`; `…/stores/pgx/v0.21.0`, `…/stores/turso/v0.20.0` | PR-3 + live legs (incl. the gated authorization libSQL leg); 3b at PR-3b's merge commit after a dispatch | stores: core v0.30.0 |
| 4a / 4b | `pockets/jobs/v0.7.0`; `…/stores/pgx/v0.7.0`, `…/stores/turso/v0.6.0` | PR-4 + live legs; 4b at PR-4b's merge commit after a dispatch | stores: core v0.7.0 |
| 5a / 5b | `pockets/cms/v0.5.0`; `…/stores/pgx/v0.5.0`, `…/stores/turso/v0.4.0` | PR-5 + live legs (incl. the cms turso gate); 5b at PR-5b's merge commit after a dispatch | stores: core v0.5.0 |
| 6 | `workshop/gopernicus/v0.7.0` | PR-6; after step 1 | scaffold pins pgxdb v0.12.0 |
| — | PR-7 examples | steps 1–5b cold-verified | explicit `go get` per example (all five); examples never tagged |

Fourteen tags. Annotated, not signed (owner ruling 2026-10-22). Each: cut at the squash commit on `main`,
pushed, THEN polled on the proxy `.info` (never before the push — a miss is negative-cached), cold-verified
from a scratch consumer (`GOWORK=off`, empty `GOMODCACHE`, `proxy.golang.org`, `sum.golang.org`, `go mod
verify`, `go build <module>/...`). Never tidy a dependent before the proxy serves its tag; never `go work
sync`; one git step per command. `stores/goredis`, `pockets/secrets` (+ store), `pockets/events` (+ stores),
`pockets/cms/views/goth`, `turso`, `sdk` and `pockets` are not retagged.

## Risks

1. **Archive-before-purge is the service's guarantee, not the database's** (highest; intended). A runtime
   role holding EXECUTE can purge past-floor rows without archiving them; the floor is the database's
   guarantee and the only evidence of an unarchived purge is a purge row with no matching archived row — the
   runbook's "Verify the archive" query and the 48 h alert are the detection. A disk sink on ephemeral storage
   is allowed and is not an archive until the host backs it; the read-back check proves the object was
   written, not that it survives the node (the fsync is the named sdk follow-up).
2. **Retention is enforced only where it is wired.** The acknowledgement is a WARN this slice ("later
   require"); a host that ignores the WARN keeps growing. The control map says OPT-IN (runner) / DEFAULT
   (ports) / HOST honestly. The first enablement on an old host is a sustained DELETE load for days (WAL,
   replica lag, autovacuum, turso's write lock) — the handoff's "first week" section.
3. **Erasure is not a ban, and the roadmap wording moves.** Deleting the external mirror lets the same
   provider subject provision anew; `erased_at` instead of `status='erased'`; `invited_by`/`resolved_subject_id`
   KEPT instead of tombstoned; no `expired` writer; an accepting claim blocks erasure (409) until settled; the
   rail is left intact (R-7). Each is a documented deviation for the owner to bless.
4. **Migrations.** Authentication 0023/0024, authorization 0007, jobs 0005 and cms 0022 are additive and
   migrate-then-deploy, but the runner applies every pending file in ONE transaction and `lock_timeout`
   bounds only the wait to ACQUIRE a lock, never how long a build holds it: 0023 orders its index builds
   before the `ADD COLUMN` so `users`' ACCESS EXCLUSIVE is held for one statement; every index is
   `IF NOT EXISTS` so the `CONCURRENTLY` pre-build makes the in-stream statement a no-op; "apply the S3 files
   alone". The stores refuse a missing REQUIRED file by name; the admitted indexes WARN.
5. **Concurrent purges versus claims and erasures.** `FOR UPDATE SKIP LOCKED` everywhere a bounded
   DELETE/UPDATE subselects on pgx (the purge function included); the Erase locks the invitation rows before
   deciding; SQLite's single writer; live race tests pin pgx because storetest cannot.
6. **Fourteen tags, five cores, four repin PRs.** More moving pins than S2; MVS keeps a host on the pair it
   has until it repins; the handoff orders them; tag 1 waits for the real 0024.

## Verification — how the owner proves it end to end

Framework: `make check` and `make guard` (G35, G36 listed) green on every PR; fourteen tags cold-verified with
the step-1 MVS set; `make test-stores` (fresh scratch Postgres 17) and the gated turso legs (fresh libSQL) green
on merged `main` before each pocket/store tag; the Linux-container `make test-stores` on the PR-2b branch and a
`live-stores` dispatch on `main` before every store tag; 0024 applied by the non-superuser fixture migrator in
every live case.

Baseline, from `examples/production-baseline` with a local Postgres 17 (docker on a free port, never 55432),
`.env` from `.env.example` with `DB_SSLMODE=disable` uncommented, `AUDIT_ARCHIVE_DIR=./archive`, no mode line,
a `NOSUPERUSER` owner role for `make migrate` (the README's recipe):

1. **Migrate, then preflight.** `make migrate` applies the three new baseline files (the log lists them by
   their assigned numbers) in their own invocation; `go run ./workshop/migrations -posture-role baseline_app`
   prints the S2 lines unchanged plus `functions=[security_events_purge_before:execute=false unsafe=none
   inoperative=none] floor=365 days`; `GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz,
   pg_catalog.int4) TO baseline_app;` → `execute=true`. `psql -U baseline_app -c "SELECT * FROM
   security_events_purge_before(now() - interval '367 days', 100)"` → `(cutoff, 0)` and ONE new
   `security_events_purged` row with `count=0`, `floor_days=365`, `actor_id='maintenance'`; the same with
   `now() - interval '364 days'` → `ERROR … below the retention floor` (22023); `… (NULL, 100)` → 22023;
   `DELETE FROM security_events WHERE false` as `baseline_app` → still `permission denied` (posture
   unchanged). **The floor negatives, each as `baseline_app`, each 42501:** `CREATE OR REPLACE FUNCTION
   security_events_retention_floor() …`, `ALTER FUNCTION security_events_purge_before(…) SECURITY INVOKER`,
   `ALTER FUNCTION … RESET search_path`, `DROP FUNCTION security_events_purge_before(…)`, `ALTER FUNCTION …
   OWNER TO baseline_app`. `GRANT EXECUTE … TO PUBLIC` as the owner, then boot in production as `baseline_app`
   → refused with `ErrDefinerFunctionUnsafe` naming the function and the `REVOKE`; revoke it → boots. `ALTER
   FUNCTION … SECURITY INVOKER` as the owner → boots with ONE WARN (inoperative) and the rail step reports
   `forbidden`-class failure; restore it. `REVOKE EXECUTE … FROM baseline_app` → the **degraded boot**: boots
   with exactly one WARN `maintenance_execute=missing`, each pass reports `step=security_events
   error_kind=forbidden` while the other steps run; grant it back.
2. **The runner.** `DB_USER=baseline_app APP_RUNTIME_MODE=production go run ./cmd/server` boots with no WARN
   (`maintenance_runner=true archive_sink=disk:./archive security_event_retention=9600h0m0s` in the
   registration log); within the jitter the first pass logs its report (`step=… count=… skipped=…
   error_kind=`); `/healthz` detail shows `maintenance_last_pass`, `maintenance_steps_failed=0` and NO error
   text; `ls archive/security-events/` is empty (nothing older than 400 days) and `psql -c "SELECT event_type,
   details->>'count' FROM security_events WHERE event_type='security_events_purged' ORDER BY seq DESC LIMIT 1"`
   shows the pass's row. Backdate ten rows as the owner (`UPDATE security_events SET created_at = now() -
   interval '500 days' WHERE id IN (…)` — the owner branch persists) and CAPTURE their ids; run a pass → one
   `security-events/<cutoff>/<id>.ndjson` object whose line ids EQUAL the captured set exactly, whose SHA-256
   and line count equal the `security_events_archived` row's `sha256`/`rows`, a `security_events_purged` row
   with `count=10`, and none of the ten ids remain (`SELECT count(*) … WHERE id = ANY(…)` → 0). **Budget
   exhaustion:** backdate 50,000 rows (a generator script in the scratchpad) with `PassBudget=1s` in a scratch
   copy → every pass makes progress, `BudgetExhausted=true` until the backlog drains, every purged id appears
   in a verified object, the WARN appears once per persisting pass. **Sink failure:** make `./archive`
   read-only mid-run → the step fails `archive_failed`, no purge row that pass, no partial object published,
   `/healthz` shows `maintenance_last_error_kind=archive_failed` and a 200. Unset `AUDIT_ARCHIVE_DIR` in
   production → the boot refuses (required); in development → `archive_sink=none` in the registration line
   and NO WARN. Set `MaintenanceRunnerAcknowledged: false` in a scratch copy → one production WARN, and `guard
   --posture` loses the `ack` line. **The monitor:** the runbook's alert query (`SELECT max(created_at) FROM
   security_events WHERE event_type='security_events_purged'` older than 48 h) is wired in the README's
   example and fires when the runner is stopped for the test.
3. **Erase.** With the dev issuer and the admin role: create a project viewer user B (a provider login) with
   an email and a phone identifier, invite B's address to a second project, grant a role, `POST
   /auth/admin/users/<B>/erase` → 200 `{user:{status:"erased"}, changed:true, counts:{…}}`; `GET
   /auth/admin/users/<B>` → `status: "erased"`, blank name and email; B's bearer token → 401; `SELECT
   ip_address, user_agent FROM security_events WHERE user_id='<B>'` → UNCHANGED (R-7), and one `user_erased`
   row with the counts and the admin's client info; `SELECT * FROM external_subjects WHERE user_id='<B>'` →
   none; the invitation addressed to B: `identifier=''`, `metadata='{}'`, `invited_by` and
   `resolved_subject_id` unchanged; a NEW provider login as the same subject → a fresh user (not a ban);
   repeat the erase → 200 `changed:false`; `POST /auth/admin/users/<B>/reactivate` → 409; `…/deactivate` → 200
   `changed:false`; erase yourself → 400. Remove B's tuples through the role routes (an EXECUTED runbook
   step); **the sweep:** the runbook's `DO` block over every text/jsonb column counting rows containing B's
   email and B's last IP → zero rows outside the documented carve-outs (`security_events` rows within
   retention; archives; `oauth_refresh_history` hashes). `ExpireOperations` after 180 days would drop the
   fingerprint (prove with `WithOperationRetention(7*24h)` in a scratch copy and a backdated `committed_at` as
   the owner — a fenced row survives; a `Fence` of an expired id returns `fenced`).
4. **Turso (harness).** `cd pockets/authentication/stores/turso` with a `t.TempDir()` file: the untagged
   cases drop 0022 from the ledger copy → a purge leaves no trigger; with 0022 → `SELECT sql FROM
   sqlite_schema WHERE tbl_name='security_events' AND type='trigger'` is byte-identical before and after,
   production `Repositories` logs zero WARNs, the fault-injection cases roll back to a byte-identical trigger
   set, a host `AFTER INSERT` trigger sees the purge row, the floor row survives `truncate()`; then the gated
   libSQL leg with zero `--- SKIP` lines.
5. **Harness, `cd examples/auth-cms && AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 go run ./cmd/server`** with
   `MAINTENANCE_INTERVAL=1m INVITATION_TTL=1m INVITATION_RETENTION=1m`: the ticker logs a maintenance report
   each minute; invite an address and let it expire → within two passes it is deleted and `invitations` no
   longer holds the address; the erase flow of PR-2a's run-and-look; submit a contact form and backdate it →
   the next pass logs `step=inquiries count=1`; `outbox.PurgePublished` and the delivery purge log their own
   lines.
6. **jobs-minimal:** `go run ./cmd/server`; after an hour (or `RETENTION_EVERY=1m`) the `retention` kind fires
   and the log shows `purged=<n>`; a completed job older than 7 days is gone, a pending one is not.
7. **Cold scaffold:** `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.7.0 init --db pgx
   --module example.com/x` → builds on pgxdb v0.12.0; the README's "Retention" names the runner, the floor as
   an invariant, `GRANT EXECUTE`, `REVOKE CREATE … FROM PUBLIC`, the index pre-build and the archive; `guard
   --posture .` clean.

## Decided by the review gate (no ruling needed)

All six reviewers agreed with rev 1's recommendations on R-1…R-6, with the edits below; those six are retired
as rulings and recorded here. Attribution: pm = product-manager, sre = platform-sre, arch =
architecture-steward, data = data-integration-reviewer, be = lead-backend-engineer, fe = lead-frontend-engineer.

1. **R-1 (a) — `erased_at` + a projection**, with `Status` honestly three-valued and `SetStatus(erased)`
   refused as a transition; one storetest case pins the projection across four readers; `List` filters by
   `erased` (arch #4, be #13, data, pm Q4).
2. **R-2 (a) — refuse below the floor, 365d database floor, 400d Go default, one purge row per call**; the
   floor is a framework invariant (no lowering procedure), Go minimum 366d, `MaxBatchSize` 10000, `STABLE`,
   NULL refused, integer `floor_days`, `FOR UPDATE SKIP LOCKED`, daily rail cadence (pm #6/#24, sre #9/#17/#18,
   data #10/#14/#21).
3. **R-3 (a) — the baseline runs retention in process** through `pockets/retention/inbound/runner`; the jobs
   form is a documented recipe (`queue.HandlerFunc` over `Pass`), not exercised; `JobKind`/`JobHandler` cut;
   `Pass`/`Run`/`LastReport` (pm #3, arch #5/#6, be #5/#15/#16/#24).
4. **R-4 (a) — no `iam_audit`/`secrets_audit` purge**; the control map says HOST; the definer template is the
   recipe (pm #9, all).
5. **R-5 (a) — no `expired` writer**; `PurgeInvitations` covers pending-past-expiry; `expired` leaves the SQL;
   the reservation doc gains "or purged past `InvitationRetention`" (be #22, data #28).
6. **R-6 (a) — `ServiceAccounts.Delete` cascades**, through a NEW trusted `DeleteServiceAccount` and an
   optional `CascadeDeleter`; Erase cascades only act-as-user accounts; the row locks on insert/create/delete
   (be #7, data #15/#25, pm Q6).
7. **The archive is per chunk, verified, evidenced through the repository, keyed by a private generator**;
   `archive_sink=none` is a registration-line fact; disk sinks allowed with the caveat; the fsync is an sdk
   follow-up (pm #7/#16/#18/#23, sre #2/#4/#5/#10, be #3/#9/#18/#19/#20).
8. **Optional capabilities resolve once at construction**, explicit slots on authorization and cms, a
   registration line and production WARN each; `WithOperationRetention` without the slot is a construction
   error; `ExpireOperations` on `mutations.Service`; the Fence race branch retryable (arch #1/#2, be #10/#13,
   data #11/#27).
9. **jobs: `PurgeTerminal` unchanged, `PurgeTerminalJobs` new** (be #1, arch #9).
10. **Migrations: indexes first with `IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS` last, `lock_timeout` in
    every S3 file, the `CONCURRENTLY` pre-build + `indisvalid` recipe, "apply alone", OAuth2 `expires_at`
    indexes + bounded `PruneOAuth2`, the grants OR-arm dropped, 0007/0005/0022 admitted with a WARN, rollback
    forward-fix only** (sre #1/#6, data #2/#3/#18, be #8/#14, pm #15).
11. **Erase correctness: lock the user, capture pairs, lock the invitation rows, refuse on `accepting`,
    then write; `SubjectKeys` via `magicLinkSubjectKey`; address-matched rows only when
    `resolved_subject_id IN ('', target)` and the pair is not another active owner's; phone normalization;
    `WithoutCancel` + timeout; the construction invariants; `user_erased` inside the transaction;
    `EraseUser` returns a Summary** (be #2/#13, data #4/#5/#16, sre #12, pm #1).
12. **Probe v0.12.0: pre-pin `to_regprocedure`, same namespace, `quote_ident` + one element, qualified types
    and casts, refuse only on PUBLIC EXECUTE / bad `search_path`, WARN on inoperative states and superuser
    owner / CREATE on the schema, migrate-time assertions in 0024, restore steps in the runbook, tag 1 after
    the real 0024, the MVS set** (sre #7/#8/#15, data #7/#8/#9/#23/#26, pm #13).
13. **G35 in PR-2b, per line, both spellings, the three extra statement shapes, planted fixtures of both
    forms; G36 pins the maintenance leaf; the count prose** (arch #7, data #17, be #4, pm #14).
14. **Turso: DML → restore → evidence; rows closed before DDL; `NOCASE`; the floor table re-runnable with a
    missing row refused; `truncate()` excludes it; `FormatTime` everywhere; untagged file cases with fault
    injection and the post-commit check; the corrected SKIP gate on every leg; the cms turso leg** (data
    #1/#12/#13/#22, sre #14).
15. **Sequencing and evidence: PR-1 first, PR-2b from `main` on 2a, no core-side changes in 2b, the gated
    cascade case, example wiring per pocket PR, PR-7 = pins + runner + healthz with every example repinned
    and `GOWORK=off` built, repin PRs per step, ledger numbers at merge, per-step dispatches, scoped WARNs,
    the control-map flips, the retention table rows, the Verification additions, the surprise list, the
    `.env.example` keys, the text nits** (pm #8/#10/#11/#12/#17/#19–#27, sre #11/#13/#16/#19–#23, be #17/#21/#23/#25/#26,
    data #19/#20).
16. **`/healthz` exposes a timestamp, a failed-step count and an error kind — never text; a maintenance
    failure never changes the status code** (sre #11, pm #20).
17. The purge lives on `maintenance.Repository`, never on `SecurityEventRepository`; the README sentence
    "Maintenance must be backed by the database that holds the rail" ships, with a boot-time check if cheap
    (the store's constructor already probes the rail's function on the SAME connection it purges through, so
    the mismatch pm Q2 describes — a Maintenance repository on a different database than the rail — is
    caught by the 0024 probe refusing on the wrong database; stated in the README) (pm Q2, arch #10).

### Roadmap deviations the owner is blessing

- "`SecurityEvents.PurgeBefore` + `Archive(before, sink)`" → both on `maintenance.Repository`/`Service`, the
  rail port untouched (keep; the README sentence above; the probe is the boot-time check).
- "`Operations.Expire(before)`" → an OPTIONAL `OperationExpirer` slot with a limit and the retention in the
  service (`Mutations.ExpireOperations`).
- "tombstone `invited_by`/`resolved_subject_id`" → **REVERSED**: both ids are KEPT (pseudonymous; the user
  row survives); only `identifier` and `metadata` are blanked on rows addressed to the erased user. Rev 1 had
  `''`; the review gate reversed it (arch #3) — recorded here for the owner's blessing as a deviation from the
  roadmap's wording.
- Also recorded (decided above): "`status='erased'` (ALTER CHECK)" → `erased_at` + a three-valued projection;
  "expire pending invitations" → purge them past expiry with no stored transition; "`ServiceAccounts.Delete`
  cascades or refuses" → cascade; "on a `pockets/jobs` schedule" → the in-process `Run` by default and the
  `HandlerFunc` recipe; "the maintenance service must iterate a host registry" (`roadmap.md:334-336`) → one
  `Repositories` per service, the registry is the per-tenant plan's; O-3c's "floor INSIDE the function" → a
  sibling plain function the purge function reads (wording, not intent).

### Build-time deviations (lead rulings during execution; PRs #162–#173)

All twelve PRs (#162 pgxdb, #163 cms, #164 cms repin, #165 jobs, #166 authorization, #167 jobs repin, #168 authorization repin, #169 authentication core, #170 authentication stores, #171 authentication repin, #172 workshop, #173 examples):

1. PR-3 authorization (#166): pgx `Expire` takes NO row lock. The plan's `FOR UPDATE SKIP LOCKED` cannot run as the runtime role — every PostgreSQL row-lock clause needs UPDATE privilege and the S2 posture revokes UPDATE on `iam_operations` (SQLSTATE 42501 live). General rule recorded: a runtime-role statement on a rail never carries a lock clause; a SECURITY DEFINER function runs as the owner and may. Also: `storetest.Repositories.SetOperationCommittedAt` fixture hook added; the pgx `Fence` retry sits in the store (turso single-writer / memory mutex need none); the slot lives in `config.go` (no root `repositories.go`); baseline ledger copy is `0029_iam_operations_expiry.sql`; auth-cms runs expiry on its own ticker in `purge.go` until PR-7 folds it onto the runner.
2. PR-4 jobs (#165): the capability WARN is MODE-BLIND via `slog.Default` (the queue Service has no runtime mode or logger option; adding `queue.WithLogger`/`WithRuntimeMode` was outside the version table); `ErrTerminalPurgeRequired` lives in `config.go` beside the other `…Required` errors, not `work.go`; the pgx index WARN is in `StatusCheck` (the boot gate), turso has no probe and no WARN; no `.env.example` for jobs-minimal (`RETENTION_EVERY` README-only).
3. PR-5 cms (#163): built per plan; the auth-cms `INQUIRY_PURGE_*` loop is cmd-level until PR-7 folds it onto the runner. R-8 cms paging deferred to `.claude/plans/cms-inquiries-admin-paging.md` (DRAFT rev 2 after a five-reviewer gate; owner questions open).
4. PR-2a authentication core (#169): the turso 0-skip gate applies at PR-2b (PR-2a's gate = 0 FAIL + only the four gated storetest groups skip); NO status filter on the user list (`AdminRepository.List` has no filter — a port change, follow-up); NO `INVITATION_TTL` root option (none exists — follow-up); a platform-admin `UserAdminCheck` was ADDED to auth-cms (the erase route opt-in requires one) which also mounts the admin directory routes there; a registration line `auth: maintenance maintenance_runner= archive_sink= security_event_retention=` added (the pocket had none); extra exports `ErrArchiveFailed`, `Service.Config()`, `EraseCounts.Merge`, `WithUserErasureRoute`; archive lines via `json.Marshal` (FS9 bans `json.NewEncoder`; HTML chars escaped, valid JSON); `ErrUserErasureRequiresActiveSessions` checked just before the general repository validation; auth-cms `outbox.PurgePublished` NOT a runner step; `TestUserErasureOverRealHTTP` replaces the plan's curl erase leg (auth-cms's platform admin is a synthetic seed with no sign-in); guard-count prose "thirty-three" in `README.md:92,121` + `examples/cms/README.md:91,108` left for the docs PR (PR-6/PR-8) — the count is THIRTY-FIVE after G35+G36.
5. Sequencing: PR-2a merged BEFORE PR-2b (stacked; 2b rebased `--onto main`); tag 2a still waits for PR-2b's branch legs + tag 1 per the plan.
6. Repins (#164 cms, #167 jobs, #168 authorization) moved ONLY the core pin (pgxdb/turso pins unchanged: authorization on pgxdb v0.11.0 / turso v0.8.0; cms and jobs on pgxdb v0.7.0 / turso v0.4.0; the authentication repin #171 moved pgx to pgxdb v0.12.0 as the plan required).

7. PR-2b authentication stores (#170): 0024 uses `CURRENT_USER`/`SESSION_USER` keywords (SQL value functions cannot be schema-qualified); `floor_days` derives from the one `365` literal; `AT TIME ZONE` as `pg_catalog.timezone(…)`; the plan's `OPERATOR(pg_catalog.>) now() - floor` needed parentheses (found live); turso 0023 builds six indexes not eight (0019 already covers oauth codes + refresh history); turso `api_keys.go` unchanged (single-writer); RESIDUAL: an api key minted while its account's cascade commits first is still inserted — inert (authentication finds no account), documented in both READMEs; store WARN/ERROR via `slog.Default`; G35 allow-list = 3 lines (turso purge DELETE + the two canonical trigger constants); G35 gained a second drop-by-name pattern (rail-named triggers, both spellings) — a DYNAMIC `DROP TRIGGER `+name stays outside both patterns (the turso purge is the only such line; residual); `-posture-role` fails only on the boot-refusal cases (missing/unsafe function), WARN-only cases pass; baseline ledger copies are 0030/0031 (0029 taken by PR-3); the baseline README ledger rows and the scaffold template's `functions=` line go to PR-6/PR-8; `make test-stores` requires `AUTHORIZATION_TURSO_DISPOSABLE_URL` for the authorization turso leg (precondition; a Makefile/README nit for a follow-up).

8. PR-6 workshop (#172): the scaffold pgxdb pin lives in `init.go` (`buildInitParams`), not `go.mod.tmpl` (plan :1102 — the template renders `{{.ConnectorVersion}}`); the scaffold has no authentication/authorization/jobs/cms pins (sdk v0.16.0 / turso v0.8.0 unchanged); the scaffold README keeps the `workshop/gopernicus@v0.6.0` guard invocation (v0.7.0 untagged at build time; its ack sentence says 'v0.7.0 and later'); the template's `railFloor` reads the authentication floor function so a host-only rail prints `floor=missing`; the host-rail definer template appends no evidence row (`iam_audit` CHECK constraints) and returns the count for the host cron; docs follow the merged code where the plan differed (the `maintenance_execute=missing` WARN fires at store construction in production — plan :512 said only when Repositories.Maintenance is set; `Components.Maintenance` has no interval setting so the guides say 'hourly'); only already-tagged versions cited. LEFT FOR PR-8: README.md:92,121 + examples/cms/README.md:91,108 'thirty-three' → thirty-five; `docs/pockets/authorization.md` version table stale (core v0.22.0 …); the three guides' header dates ('2026-10-22' / 'after S1'); the pocket-scaffold pgxdb pin v0.9.0 (pre-existing, out of scope — note only); G35/G36, the guard count, the dynamic DROP TRIGGER residual and ledger copies 0029–0031 appear in no PR-6 file.

9. PR-7 examples (#173): auth-cms's inquiry-purge and operation-expiry loops FOLDED onto the retention runner (lead ruling; plan :732 said PR-7 only moves pins) — two behaviour changes: first pass at boot instead of one interval later, failure lines carry `error_kind` not error text; the inquiry purge keeps its own runner instance so `INQUIRY_PURGE_INTERVAL` still applies. `/healthz` detail: `maintenance_last_pass`, `maintenance_steps_failed`, `maintenance_last_error_kind` (closed set; none when nothing failed; a failed step outranks budget_exhausted). Non-S3 pin moves needed for GOWORK=off: sdk v0.9.0→v0.16.0 (jobs-minimal, cms, minimal), turso v0.4.0→v0.8.0 + otel v0.2.0→v0.3.0 (cms); authentication/views/goth left at v0.5.0 in auth-cms (builds; latest v0.6.0). Root `README.md:55` ('zero acknowledgements') and the baseline README ledger rows 0030/0031 edited outside the file list (made false / missing by S3). PLAN CORRECTIONS from the run-and-look: production cannot boot against the plaintext scratch Postgres (`ErrTLSRequired`) and never reads `.env` → the production legs ran on a scratch TLS server (plan :1251-1253, :1273); 'B's bearer token → 401' (plan :1296) is wrong for the baseline — external tokens with `ProvisionOnFirstVerification` return 200 with a fresh user id (auth-cms native sessions do return 401), README says so; `INVITATION_TTL` (plan :1315) does not exist — cancel + `INVITATION_RETENTION` proves the invitation purge; role routes record no operations — expiry proven with a scratch probe; `PassBudget=1s` does not exhaust locally (one pass drains 50k rows), 20 ms does. NOT BUILT: `outbox.PurgePublished` as a runner step (in neither PR-7's file list nor the brief; S3 Notes list it as an out-of-scope follow-up) — gap recorded; jobs-minimal's 7-day boundary not observable live on the memory store (storetest covers it); `examples/production-baseline/archive/` is not gitignored (follow-up; PR-8 may add the ignore line). Baseline seeded by SQL as the runtime role (no invitation/identifier routes mounted): a pending invitation is deleted, blanking shown on an accepted one.

## Open rulings — YOUR CALL (exactly two)

- **R-7 — no in-place rail scrub in S3** (**recommend**: do not build it). Rev 1 shipped
  `security_events_erase_client`, a second definer function with EXECUTE for the runtime role, an in-function
  gate on `erased_at` (a column the runtime role can write — platform-sre #3), a sequential rail scan per
  erasure under the user lock, turso triggers dropped inside the erase, a fifth event type and `user_erased`
  recorded after commit. Neither the roadmap's Erase bullet nor FAS-3 requires it; FAS-3's own recommendation
  is "rows intact, keyed to the pseudonymous user id; IP/UA follow the audit retention clock"
  (`passes/04-auth-stores.md:91`); the `user_id = target` arm would also have blanked an attacker's IP/UA on
  failed-login rows and the admin's attribution on the rows leading up to the erasure (lead-backend #6,
  data-integration #6). This rev removes it: IP/UA and legacy raw addresses on `security_events` age out at
  400 days; `user_erased` is written inside the erase transaction. The alternative (b): keep it OPT-IN behind
  `UserErasureConfig{ScrubAuditClientInfo bool}` with a write-once trigger on `users.erased_at`, a narrowed
  predicate and its own PR after the stores — its own plan if ever wanted.
- **R-8 — cms paging and the admin delete UI deferred** (**recommend**: defer to
  `.claude/plans/cms-inquiries-admin-paging.md`). Rev 1 bundled a paged admin list, a delete route and
  form, a `Views.InquiriesList` port change with a `views/goth` retag, and an `inquiries.id` collation
  rewrite — none is a disposal control, the port change forces a lockstep host repin (platform-sre #16,
  lead-backend #11), the template could not know whether the delete route was mounted, and the CSRF and
  confirmation posture of the first irreversible third-party-PII delete was never stated (lead-frontend
  #2–#6). S3 keeps `InquiryEraser` (`Delete`, `DeleteByEmail` ASCII case-fold, `PurgeBefore`), the plain
  `created_at` index, the auth-cms schedule and the memstores; the follow-up's brief is under Notes. The
  alternative (b): keep paging without the rewrite and pass a "can delete" flag — still a Views change.

## Recommended reviews

- `product-manager` — R-7 and R-8 as framed, the deviations list (the tombstone reversal in particular), the
  fourteen-tag count against the rulings' "thirteen", the DoD's which-host-runs-what, the surprise list.
- `platform-sre` — the per-chunk archive and its read-back, the refuse/WARN split on the probe, the 0024
  assertion block, the index ordering and the pre-build recipe, the repin PRs and per-step dispatches, the
  `/healthz` shape, the turso fault-injection cases.
- `architecture-steward` — `Pass`/`Run`, the explicit slots, `Mutations.ExpireOperations`,
  `pockets/retention/inbound/runner`, G35/G36 shape and numbering, the kept ids on invitations.
- `data-integration-reviewer` (third pass) — the final 0024 text (assertion block, the `ALTER` block's
  namespace expression, the purge-row INSERT, casts), the Erase transaction order and the pair-exclusion SQL,
  `PruneOAuth2`, the turso order, the gated turso legs, the Fence race test.
- `lead-backend-engineer` (third pass) — `EraseInput.Event` and `(*Recorder).Prepare`, the service-account
  locks and `CascadeDeleter`, `PurgeTerminalJobs`, the root `MaintenanceConfig`, the error names.
- `lead-frontend-engineer` — nothing in S3 (R-8); the cms follow-up plan when it is drafted.

## Consultation notes

The two leads were consulted once each on a paragraph sketch before rev 1 (their folds are recorded in
`s3-retention-erasure.rev1.md` "Consultation notes" and stand). No lead was consulted for rev 2: the six
reviews are the input, and every fold is attributed in the review gate record below. Where reviewers
differed — the cms Views change (lead-frontend endorsed the break; platform-sre and lead-backend wanted an
optional interface) — the ruling (R-8, defer) decides; the actor-arm index question is moot under R-7; the
G35 count prose follows architecture-steward's "thirty-three" (the Makefile and `check.yml` say so today).

## Notes

- **Review gate record (2026-10-23).** product-manager — SHIP WITH EDITS (27 findings, 4 HIGH); platform-sre
  — SHIP WITH EDITS (23 findings, 3 HIGH); architecture-steward — SHIP WITH EDITS (9 findings + aligned
  notes); data-integration-reviewer — SHIP WITH EDITS (29 findings, 3 HIGH); lead-backend-engineer — SHIP
  WITH EDITS (26 findings, 3 HIGH); lead-frontend-engineer — SHIP WITH EDITS (PR-5 only, 14 findings).
  Reviews at `/private/tmp/claude-502/-Users-jrazmi-code-gopernicus-ecosystem-gopernicus/18fb770a-a1cf-406a-abf7-217562a2bd5e/scratchpad/review-s3/01-product-manager.md`,
  `…/02-platform-sre.md`, `…/03-architecture-steward.md`, `…/04-data-integration-reviewer.md`,
  `…/05-lead-backend-engineer.md`, `…/06-lead-frontend-engineer.md`. Every finding is accepted and folded
  under the lead rulings, except where a ruling overrides it: **rejected or superseded** — platform-sre #3
  (write-once trigger on `erased_at`), #9 (a database-floor reader so hosts can lower it), product-manager
  #6(b) (same), lead-backend #6(b) (opt-in scrub), data-integration #6 (the two-arm scrub predicate) and the
  definer-UPDATE live case — all moot under R-7 and the floor-as-invariant ruling; lead-frontend #1–#14,
  lead-backend #11, platform-sre #16's optional `InquiryPageViews`, architecture-steward #8 (the six-case
  paging family), data-integration #19 (the ICU collation case) — deferred with R-8 into the follow-up's brief
  below; lead-backend #20 (`PurgeWithoutArchiveAcknowledged`) — the ruling keeps "purge, report
  `archive_sink=none`, no WARN"; platform-sre #10's advisory lock — not built, one runner instance documented
  instead; lead-backend #2's "blank `resolved_subject_type` too" — reconciled with the tombstone reversal as
  "keep both ids together". Rev 1 of this file is `s3-retention-erasure.rev1.md`.
- **Count discrepancies, stated plainly.** (1) The rulings say "thirteen tags"; the enumerated list is
  fourteen (pgxdb 1 + authentication 3 + authorization 3 + jobs 3 + cms 3 + workshop 1) and none can be
  dropped (cms core carries the new interface and slot; both cms stores carry 0022 and the methods; the
  workshop list edit is a MINOR). The plan says fourteen everywhere. (2) The rulings' count prose
  "thirty-three → thirty-four" covers G35 alone; the G26-shaped leaf grep they also ask for is a second
  Makefile target, G36, so the aggregate ends at thirty-five (PR-2a → thirty-four, PR-2b → thirty-five).
- **cms follow-up brief — `.claude/plans/cms-inquiries-admin-paging.md` (not yet written; this is its
  input).** Scope: the admin paged inquiry list and delete UI. Findings to honor (lead-frontend, rev 1 review):
  the handler builds `list.Request{Cursor, Limit: atoiOrZero(limit)}` leniently, cursor strategy only, like
  `EntryHandlers.List` (`crud.ParseListQuery` does not exist; `list.ParseQuery` is the strict JSON-API parser);
  the port takes a model — `InquiriesList(m cms.InquiriesListModel{Items []messaging.Inquiry; Pager Pager;
  DeleteEnabled bool})` re-exported from `internal/inbound/cms/models.go` — so the template knows whether the
  delete route is mounted and later fields are not breaks; the route registers through `handleAdmin("POST",
  "/inquiries/{id}/delete", …)` (inherits `adminEnabled` + `AdminMiddleware`), the capability reaches `Mount`
  as a `messagingService` method (`InquiryDeletion() bool`) and the same bit sets `DeleteEnabled`; success is
  `303` to `/inquiries` (PRG; no return URL from the form); the CSRF posture is stated (cms admin forms carry
  no token; `AdminMiddleware` is the only gate and must authorize; SameSite=Lax does not cover same-site
  siblings) and a cms admin form-CSRF seam is filed (`ConfirmDialogProps` anticipates a token child); the
  delete control is `feedback.ConfirmDialog` (destructive, `role=alertdialog`, server-owned, works without
  JS) or an explicit "one-click, matches siblings" decision; the pager is plain `<a>` links from
  `newerHref`/`olderHref` under `<nav aria-label="Pagination" data-slot="cms-inquiries-pager">`, NOT
  `entriesPager` (whose `hx-target` region does not exist on this page) with a render test asserting no `hx-`;
  `data-slot="cms-inquiry-list"` stays, `cms-inquiry-delete` and `cms-inquiries-pager` are added; the delete
  action and pager links build from `Pager.BaseHref`; `web.NoStore()` on both routes or documented as
  host-owned; the files the port change forces (`views_stub_test.go:104`, `admin_middleware_test.go:170` and
  its `adminRoutes` table, `cms_test.go:124`, `helpers_test.go` `fakeContactSvc`, `views/goth/views_test.go`);
  storetest `Inquiries/{ListPageOrderingAndCursor, ListPageSameCreatedAtCollision, ListPagePrevProbe,
  ListPageStaleCursor}` plus the charter §8 item 13 six-case family with `messaging.OrderFields`/`DefaultOrder`
  (architecture-steward #8); the memstores tie-break on `id`; the ICU-collated parity live case and whether
  the `COLLATE "C"` change (a PK index rebuild under ACCESS EXCLUSIVE, not a heap rewrite; a host view on
  `inquiries.id` blocks it) is still wanted (data-integration #19); `pockets/cms` and `views/goth` move
  together (a lockstep pair) and the 5b cold-verify assigns `cmsgoth.Views` to `cms.Config.Views` in a scratch
  consumer; run-and-look from the repo root `make run` (examples have no Makefile), 27 submissions via curl so
  a second page exists, `GET /inquiries` (cms mounts at the root; `examples/cms` has no identity), JS on and
  off; open questions for that plan: an `inquiry.deleted` event (aggregate id only) and a purge-count log
  line; repeat-delete semantics (404 sibling-consistent or idempotent 303); the redirect target.
- **Answered reviewer questions.** pm Q3 — a host without a `Fencer` settles an accepting claim through the
  operation its id names; no force-cancel. pm Q5 — one `InvitationRetention`. pm Q8 — "require" at the
  roadmap's end. sre Q2 — the Erase transaction and a 1000-row purge batch are to be TIMED against a remote
  libSQL with a realistic rail in PR-2b's libSQL leg (recorded in the README; the inter-batch pause is the
  knob); no supported turso profile routes writes through an embedded replica (the store uses `InTx` on the
  primary). sre Q4 — one runner instance; a disk sink is per instance. sre Q5 — archived rows are outside
  erasure's reach; the host justifies the archive's retention on its legal basis (handoff). be Q4 — one
  function call per port call. be Q5 — yes, in-transaction, part of the port contract. be Q6 — listed under
  deviations.
- The S2 live test proved an owner-session UPDATE and a definer DELETE; S3 needs only the DELETE (R-7).
- `pgxdb.MapError` is not widened for 22023: the store maps it with `errors.As` (private to the maintenance
  path).
- The `0017` gap in the authentication numbering is historical; 0023 follows 0022.
- S2's actual live runs were counted with `go test -json` (0 skips, 0 fails per leg) even though the plan
  text's one-liner was weak — S2 evidence stands; S3 re-runs the authorization libSQL leg anyway (PR-3).
- Named follow-ups, out of scope: `filestorage.Disk.Upload` fsync of the staged file and parent directory (sdk
  PATCH); a bounded `outbox.PurgePublished`; a `sdk/capabilities/retention` protocol (three pockets now expose
  a bounded `Purge*(ctx, before, limit)` shape); a `ratelimiter.Limiter.Prune` on the pgxdb limiter; the cms
  admin form-CSRF seam; the cms follow-up plan above.
- Known CI flake unrelated to S3: `integrations/kvstores/goredis` `TestOpenHonorsDeadlineAfterConnection`.
