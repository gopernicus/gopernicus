# S2 — Audit completeness and integrity (SOC 2 readiness, slice 2) — rev 3

**Status:** RELEASED 2026-10-23 — fourteen tags published annotated at their merge commits and
cold-verified on the first poll (record: RELEASING.md "Audit completeness and integrity"; handoff
`plans/soc2-s2-audit-completeness-handoff.md`; manifest `plans/s2-audit-completeness-release-manifest.json`;
this file copied by PR-7 = #152). Squash-merged to `main` in this order: PR-0 #139 @ 84b0154d
(step 0: `stores/pgx/v0.19.1`, `stores/turso/v0.18.1`, the expand patches); PR-1 #140 @ f0e1043f
(step 1: pgxdb v0.11.0, turso v0.8.0; the cold-verify also built stores pgx v0.19.1 on pgxdb
v0.11.0); PR-4 #141 @ cd80f491 (4a: secrets v0.2.0); PR-3 #142 @ 7344393c (3a: authorization
v0.29.0); PR-5 #143 @ 7619e090 (5: workshop/gopernicus v0.6.0, cold-installed, `guard --list
--posture` row 25 = P9; it also carried the baseline ledger copies 0025–0028 planned for PR-6);
PR-2a #144 @ ce894e2d; #145 @ a8a12eb4 (4b: secrets stores/pgx v0.2.0); #146 @ 1f22ad38 (3b:
authorization stores pgx v0.20.0, turso v0.19.0); PR-2b #147 @ a77dcbdd; PR-6 #148 @ 82b9b7ae
(examples, untagged); PR-2c #149 @ 9590e366 (2a: authentication v0.21.0); #150 @ ada0aeda (2b:
authentication stores pgx v0.10.0, turso v0.9.0 — firestore not tagged, retired); #151 @
159be98a (3a': authorization v0.29.1, the gate-denial patch; no store retag). Live store legs
ran on merged `main` before every pocket and store tag. Owner rulings during the build are at
the end of this file; the four deviations and the build rulings are recorded in `roadmap.md`,
the control-map rows in `control-map.md`. History: RATIFIED 2026-10-22 (owner: "ratified"; rev 3 as reviewed, patch tags at Step 0 kept) — rulings O-1..O-4 reviewer-vetted (four reviews, folded);
build STARTED from `main` @ effc79d9 the same day. Rev 1 and rev 2 are kept beside this file
(`s2-audit-completeness.rev1.md`, `.rev2.md`); the review gate (five reviews) and the rulings
review (four reviews) are in the session scratchpad (`review-s2/`, `review-s2/rulings/`, paths
under Notes). Anchors read on `main` @ effc79d9 (S1 closed: ten tags cold-verified,
#136/#137/#138 merged). Parent: `roadmap.md` (S2 slice, verbatim scope; rulings 1–4 RULED; the
pgxdb `QuerierFrom` carry-over is S5's, the per-tenant authority work is its own plan). Evidence:
`review.md` §4 (**H1**, **H2**, **M1**, **M2**, **M3**) and `control-map.md` rows tagged `[S2]`
(:20, :25, :46, :49, :99–106); per-finding evidence in `passes/01-auth-core.md` (FAC-4, FAC-7,
FAC-10, FAC-17), `passes/02-auth-federation.md` (FAF-2, FAF-3, FAF-4), `passes/04-auth-stores.md`
(FAS-4, FAS-6), `passes/05-authorization.md` (FAZ-2, FAZ-3, FAZ-4), `passes/07-other-pockets.md`
(FOP-3, FOP-8, FOP-9). Shape and tone mirror `s1-production-posture.md` (rev 2).

PR numbering keeps rev 1's labels so the reviews stay citable; PR-2 is split into 2a/2b/2c and
PR-0 (the expand step) is added. **Four roadmap deviations** are listed for the owner under
"Rulings" — the largest: immutability triggers ship DEFAULT-ON, not opt-in.

## Context

S1 made the audit rails REQUIRED in production (`authentication.ErrAuditRequired`,
`authorization.ErrAuditRequired`, each waivable by `WithAuditDisabledAcknowledged()`). S2 makes
what rides those rails complete, correlatable and tamper-evident. Today: `IssueToken` records no
failure or blocked row although its doc claims Login parity (`logic/authentication/token.go:39-50,
62-64`); `ChangePassword` (`service.go:779-781`), `ResetPassword` (`service.go:874-877`), `Verify`
(`service.go:515-546`), `SetPassword`/`RemovePassword` (`password.go:56-67,165-181`) and the
identifier-change flows (`identifier_management.go:203-360`) record success only; every OAuth
callback error path and the pending-link issuance are silent (`oauth.go:291-388,634-690`) and
`recordOAuth` only ever writes `StatusSuccess` (`oauth.go:621-628`); the OAuth2 authorization
server builds its own rows with no IP/UA/actor and a nanoid generator (`oauth2/audit.go:21-46`),
and `Deny` records nothing (`oauth2/authorization.go:79-85`). Authorization decisions exist only as
DEBUG slog (`logic/decisions/logging.go:21-24`, dropped at the INFO default) and a refused role
write returns an error and records nothing (`inbound/http/roles.go:118-121,152-155`). No rail
carries a request or trace id although the kernel already has them (`sdk/context.go:33-55`,
stamped by `web.RequestID()`, `sdk/pkg/web/middleware.go:125-136`); `"session_revoked"` and
`"all_sessions_revoked"` are string literals outside the vocabulary (`session_management.go:30,42`);
the secrets audit records a `secret_read` with version 0 before the open and nothing on a denied
read (`logic/vault/service.go:95-111`), and `DeleteOwner` lists secrets outside the transaction
that deletes them (`service.go:127-146`, `stores/pgx/pgx.go:243`). Invitation rows store the raw
invitee address (`invitations/service.go:788-792,896,911`) and login rows the attempted email,
raw when malformed (`service.go:633,663-670`). At the database layer every rail is a plain table:
the application role can `UPDATE`/`DELETE` `security_events`, `iam_audit`, `iam_operations` and
`secrets_audit` (`0008_security_events.sql`, `0001_iam_tuples.sql:26-55`, `0001_secrets.sql:35-50`;
`stores/pgx/audit_live_test.go:232` rewrites `iam_audit` in a test), timestamps come from the
application clock with no sequence, and the scaffold's runner and server share one role.

S2 closes, by ID: **H2** (FAC-4, FAF-2, FAF-3, FAZ-2, the `user_directory_read` half of FAC-17);
**M1** (FAS-4, FAZ-3, FOP-3, FAS-6); **M2** (FAC-10, FAZ-4, FOP-9 — the rail halves; the
access-log half FHS-7 and the inbound `X-Request-ID` validation FHS-8 stay in S6/S5); **M3**
(FAF-4, FAC-7's PII half); **FOP-8**; the audit-write-failure observer of **H1**; and the
`external_login` record FAF-8 names (M4's revocation-SLA work stays in S4).

**Deferred to S7 (review gate):** `audit_read` and any `ListSecurityEvents` API — the roadmap says
"on bundled admin routes" and no bundled audit-read route exists in either pocket
(`inbound/http/useradmin.go:86-96`; auth-cms reads the repository at `demo.go:189`); S7 adds the
audit-read routes (`GET /authorization/audit` and the authentication counterpart) and records
`audit_read` with them. The roadmap S2 bullet 6 shrinks to `user_directory_read`; `roadmap.md`
gets that note in PR-7.

Left to later slices: retention, purge and archive (S3 — the triggers below hand S3 written
constraints, O-3); access review (S7); the audit-grade access log and `RedactHandler` (S6);
validating inbound `X-Request-ID` at the edge (S5 — S2 blanks what it cannot persist).
Firestore: the authentication firestore store maps the two correlation ids in PR-2a (the
workspace builds it against the new core, `conformance_test.go:84` runs the workspace storetest)
and repins the core in the post-tag step; Firestore has no triggers, roles-in-band or sequences
— tamper evidence there is the host's IAM posture (documented in its README; FAS-8 already marks
the family unverified) and out of scope for code.

## Design

### The one principle

**Every denial is a row, every row is attributable to a request, and no application identity
can rewrite a row.** Recording stays synchronous and best-effort where it is today
(authentication: fails OPEN — the flow completes, the failure is counted and observed) and
in-transaction where it is today (authorization and secrets: fail CLOSED — the operation fails
with its audit). Tamper evidence lives in the database — immutability triggers that ship with the
rail's migrations and a privilege posture production refuses to boot without — and the store
constructors that already probe the schema probe the posture, so a bundled pgx store needs no new
host wiring to get the refusal (secrets gains a probing constructor to join them). The host
contract in one sentence: **in production a store refuses to boot when its runtime role is
superuser, owns an audit table, can UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified
triggers, or cannot insert into it.** Zero `RuntimeMode` stays mode-blind everywhere (S1
semantics).

### The three rails after S2 (exact exported names)

| rail | domain type gains | recorded from context | DB-side only (pgx) — never on the domain type | DB-side (turso) | migrations (all ACTIVE, numbered; O-1) |
|---|---|---|---|---|---|
| `security_events` (authentication) | `securityevent.SecurityEvent.RequestID`, `.TraceID string` | `sdk.RequestIDFromContext`, `sdk.TraceIDFromContext` (blanked when invalid, below); IP/UA via the existing client-info carrier | `request_id`, `trace_id TEXT NOT NULL DEFAULT ''`; `seq BIGINT NULL` then `ALTER COLUMN seq SET DEFAULT nextval('security_events_seq')` with `ALTER SEQUENCE security_events_seq OWNED BY security_events.seq`; `recorded_at TIMESTAMPTZ NULL` then `SET DEFAULT now()` — two metadata-only `ADD COLUMN`s with the default set AFTER the add, so no rewrite; NULL means "a row from before S2". The file ENDS with `GRANT USAGE ON SEQUENCE security_events_seq TO PUBLIC;` (`nextval` only — `setval` needs UPDATE; gaps are not evidence), so a host that already split roles keeps inserting | rebuild under `security_events_next` (SQLite refuses `ADD COLUMN` with an expression default) adding `request_id`, `trace_id`, `recorded_at TEXT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))`; the copy selects `NULL AS recorded_at` so no pre-S2 row is stamped with the migration time; no sequence (rowid is renumbered by VACUUM, so it is not evidence); rows copied in `(created_at, id)` order, indexes re-created | pgx + turso `0021_security_events_integrity.sql`, then `0022_security_events_immutable.sql` |
| `iam_audit` + `iam_operations` (authorization) | `audit.Source.RequestID`, `.TraceID string` (`Validate` bounds them to `audit.MaxCorrelationIDLen` = 128 printable ASCII; the bundled handlers BLANK before stamping, so `Validate` never fires on a header) | `roleAuditContext` (`inbound/http/roles.go:321-327`) stamps both from the kernel context; a system source (`cmd/bootstrap`) carries none | `iam_audit`: `request_id`, `trace_id TEXT COLLATE "C" NOT NULL DEFAULT ''`, `seq BIGINT NULL` + `iam_audit_seq` default + `OWNED BY`, `recorded_at TIMESTAMPTZ NULL` + `now()` default (same two-step add), `GRANT USAGE ON SEQUENCE iam_audit_seq TO PUBLIC;`; `iam_operations`: `recorded_at` only; **0005 adds NO constraint** (the probe refuses unknown constraints, `schema.go:140`). The pgx probe's column map (`stores/pgx/schema.go:55-91`) gains the four columns with a per-column nullability flag — `seq`/`recorded_at` are the ONLY nullable columns it admits — and refuses a UNIQUE index on `seq` as it refuses any extra uniqueness today (`schema.go:155-160`) | rebuild `iam_audit` as `iam_audit_next` and `iam_operations` as `iam_operations_next` (the 0004 pattern), `NULL AS recorded_at` in the copy; `probeCanonicalSchema` (`stores/turso/schema.go:19-50`) is REWRITTEN: it reads 0001's `iam_tuples` body plus 0005's two `*_next` bodies, drops 0004 from the set, and the `len != 3` check goes with it; `recorded_at` default only, no sequence | pgx + turso `0005_iam_audit_integrity.sql` — shipped by **PR-0** (v0.19.1 / v0.18.1 tolerate it, O-4), **REQUIRED and LOCKSTEP** from v0.20.0 / v0.19.0 for stores constructed with `WithAudit()` or mutations (below); then `0006_iam_audit_immutable.sql` (PR-3) |
| `secrets_audit` (secrets) | `vault.AuditRecord.Outcome vault.Outcome` (`OutcomeOK = "ok"`, `OutcomeDenied = "denied"`), `.RequestID`, `.TraceID string`; `AuditFilter.Outcome`. **No new action**: the roadmap's `secret_read_denied` is the OUTCOME value of a `secret_read` row, not an action (deviation (b)) | `vault.Correlate(ctx, records ...AuditRecord) []AuditRecord` — ONE stamping point, called by the service and by `logic/rotation` (`rotation.go:137,195`) at their repository boundary; `NewAuditRecord`'s signature is unchanged | `outcome TEXT NOT NULL DEFAULT 'ok'` with its CHECK added `NOT VALID` (a CHECK scans under ACCESS EXCLUSIVE otherwise); `request_id`, `trace_id` DEFAULT ''; `seq`/`recorded_at` nullable + defaults + `OWNED BY` + `GRANT USAGE ON SEQUENCE secrets_audit_seq TO PUBLIC;`. Every new column has a DEFAULT so an old binary's inserts keep working during a rollout | no turso store exists for secrets (`pockets/secrets/stores/{memory,pgx}` only) | pgx `0002_secrets_audit_integrity.sql` (probed by `Open` via the `outcome` column, naming the file), then `0003_secrets_audit_immutable.sql` |

**Every S2 pgx migration file begins with `SET LOCAL lock_timeout = '5s';`** — the pending stream
runs in one transaction (`pgxdb/migrate.go:283-300`), `ADD COLUMN` takes ACCESS EXCLUSIVE and
`CREATE TRIGGER`/`ENABLE ALWAYS` take SHARE ROW EXCLUSIVE, all held until the stream commits and
all queued behind a running `pg_dump`; with the timeout a blocked file aborts the stream (nothing
applied — one transaction) and the host retries, instead of stalling login inserts for the
runner's whole budget. **The S2 pgx files require PostgreSQL ≥ 14** (`CREATE OR REPLACE TRIGGER`;
stated in RELEASING and the store READMEs; the scratch suite runs 17).

**One correlation rule for all three rails:** an id longer than 128 bytes or carrying a
non-printable byte is BLANKED before stamping — by the recorder, by `roleAuditContext`, by
`vault.Correlate` — never truncated and never refused (`web.RequestID()` passes any inbound
header through, `middleware.go:128`; a client must not be able to 400 every role write). Test in
every rail: a 200-byte `X-Request-ID` → the write succeeds with `request_id=''`. S5 validates at
the edge. **`seq` and `recorded_at` are evidence columns, not API**: no domain type carries them,
no store reads or writes them (the shared column constants — `stores/pgx/security_events.go:36,99,121`,
authorization `audit.go:17,58,104` — stay as they are; the HAND-LISTED empty-id insert columns at
pgx `security_events.go:92` and turso `:101` gain the two ids), operators and the S3/S7 export
tooling read them with SQL. `seq` proves ORDER of insertion (a sequence is non-transactional, so
a rolled-back write leaves a gap) and `recorded_at` proves the database clock saw the row;
neither is a deletion detector — the trigger plus the privilege posture are. The memory stores
(authorization `stores/memory`, secrets `stores/memory`, the auth-cms host pocket
`pockets/authentication/outbound/authmem`) persist the two ids like any other field.

**Lockstep and rollback (authorization; O-4).** The pgx probe refuses a column it does not know
(`schema.go:113-118`) and the turso probe compares CREATE text, so a store at v0.19.0 / v0.18.0
refuses a database that has 0005 applied (when constructed with `WithAudit()` or mutations — the
only paths that probe those tables), and v0.20.0 / v0.19.0 refuse one that has not. **Step 0**
(PR-0, an ordinary PR to `main` under the ruleset — `git diff 10135055 HEAD` is empty for both
stores, the core since v0.28.0 and pgxdb since v0.10.0, so no maintenance branch is needed) ships
0005 itself in both dialects as the EXPAND step together with tolerant probes, tagged
`…/stores/pgx/v0.19.1` and `…/stores/turso/v0.18.1` on `main` only after 0005's text is final (a
test on `main` pins both bodies); PR-3 later only makes 0005 REQUIRED. The relaxation is not one
line: pgx `probeCanonicalTable` gains an optional-column set per table (type, collation C,
nullable — four columns on `iam_audit`, `recorded_at` on `iam_operations`, none on `iam_tuples`);
turso v0.18.1 compiles 0005's `*_next` bodies and accepts the 0001-or-0005 body for `iam_audit`
and the 0004-or-0005 body for `iam_operations`. Safe by construction: the v0.19.1 / v0.18.1 inserts
list their columns (`pgx/audit.go:58`, `turso/audit.go:57`), so the new columns take their
defaults — which is why 0005's `GRANT USAGE … TO PUBLIC` is load-bearing for the old binary too
(it has no posture probe; without the grant its `nextval` fails inside `appendAudit`'s
transaction and every authorization write fails). A post-hardening recipe (`GRANT USAGE … TO
<runtime>; REVOKE … FROM PUBLIC`) is optional and the v0.20.0 probe catches a missed grant.
Handoff order: every instance on v0.19.1 / v0.18.1 FIRST (a v0.19.0 instance that restarts after
0005 refuses to boot) → adopt the authentication and secrets stores until production boots clean
→ migrate 0005 (the turso rebuild holds the write lock: Step 0 makes it restartable, not online —
a maintenance window) → repin v0.20.0 / v0.19.0. After 0005, roll back only to v0.19.1 / v0.18.1,
never v0.19.0 / v0.18.0. The authentication and secrets probes check presence of known columns
only, so for them the order is migrate-then-deploy and an old binary keeps running on a migrated
database.

### The shared recorder (authentication) — ONE recorder

`recordSecurityEvent` (`logic/authentication/helpers.go:18-37`) is one of three recording sites
today: `oauth2.Service.audit` (`oauth2/audit.go:21-46`) and the invitations service's `recordAs`
(`invitations/service.go:940-946`) re-plumb IP/UA and mint ids differently. `oauth2` is a
sub-package that cannot import its parent, so the site moves DOWN into the domain package that
both can import, while the client-info carrier STAYS in `authentication` (it also feeds rate
limiting, `inbound/http/authenticator.go:96`):

- `securityevent.Input{UserID string; Actor Principal; Type, Status string; Details map[string]any}`
  (today's unexported `securityEventInput`, `logic/authentication/securityevent.go:19-25`).
- `securityevent.Recorder` with `NewRecorder(repo SecurityEventRepository, ids sdk.IDGenerator,
  now func() time.Time, log *slog.Logger, opts ...RecorderOption) *Recorder`;
  `(*Recorder).Record(ctx, Input)` — a nil receiver or nil repository is the documented no-op
  (ratified AV9); it fills `ID`, `CreatedAt`, the blanked-or-whole `RequestID`/`TraceID`, and
  `IPAddress`/`UserAgent` through an injected reader (`securityevent.WithClientInfoReader(func(
  context.Context) (ip, ua string)) RecorderOption`; the root passes
  `authlogic.ClientInfoFromContext`, `context.go:124-131`). **The write runs under
  `context.WithoutCancel(ctx)` with a 2-second timeout** so a client that disconnects after
  sending credentials cannot cancel its own failed-login row. On a write failure: the first
  failure logs at WARN with the stable key `audit_write_failed` and coarse fields only
  (`event_type`, `status`, `error_kind`); during an outage one summary line per minute
  (`audit_write_failed count=N`), never one per failure; the counter increments and the observer
  is called every time.
- `securityevent.ErrorKind(err) string` — today's `authentication.ErrorKind`
  (`authentication/securityevent.go:44-66`) moves here unchanged; `authentication.ErrorKind`
  stays as a forwarder.
- `securityevent.WriteFailure{EventType, EventStatus, ErrorKind string}`,
  `securityevent.WriteObserver func(context.Context, WriteFailure)`,
  `securityevent.WithWriteObserver(WriteObserver) RecorderOption`,
  `(*Recorder).WriteFailures() uint64`. A bridge or host that records through the recorder
  accepts the narrow `interface{ Record(context.Context, securityevent.Input) }` — never builds a
  second recorder.
- **The root builds ONE recorder** (`constructor.go`, before the `invitations.New` call at :461
  and the oauth2 construction) from `repos.SecurityEvents`, `cfg.IDs`, the clock and `cfg.Logger`,
  exposes it as **`Components.SecurityEvents *securityevent.Recorder`** (beside `Delivery`,
  `authentication.go:17-24`), and hands it down through ONE option name on every consumer:
  `authlogic.WithSecurityEventRecorder(*securityevent.Recorder)` (service — the external
  authenticator `authlogic.New` builds at `logic/authentication/constructor.go:180-191` inherits
  it; a standalone `NewExternalAuthenticator` takes the same option name), `oauth2.WithSecurityEventRecorder(…)`,
  `invitations.WithSecurityEventRecorder(…)`. Precedence in invitations: the recorder WINS over
  `invitations.WithSecurityEvents` (`constructor.go:472`); with only `WithSecurityEvents` the
  service builds a private recorder over that repository (today's behavior, failures uncounted —
  documented); passing both with different repositories is `ErrSecurityEventsConflict` at
  construction. A direct `oauth2.New` caller with no recorder gets a private one with no
  client-info reader (IP/UA empty — documented).
  `authentication.WithAuditWriteObserver(securityevent.WriteObserver) Option` and
  `Components.AuditWriteFailures() uint64` (delegating to the recorder) expose the counter at the
  root; the baseline reports it in `/healthz` detail (no metrics port exists — sdk carries only
  tracing).
- **The failure matrix, plainly:** authentication FAILS OPEN (the flow completes, the row is
  lost, the counter/observer/`audit_write_failed` line see it); authorization FAILS CLOSED (the
  mutation rolls back with its audit, `stores/pgx/writes.go:93-103`); secrets FAILS CLOSED (the
  read or write fails with its audit).
- The OAuth2 type constants move into `securityevent` (`TypeOAuth2ConsentApproved` …
  `TypeOAuth2RevocationRequested`, plus the two new ones) so ONE registry exists; `oauth2.Type*`
  stay as aliases. `securityevent.Types() []string` returns the registry and
  `TestEventTypesAreRegistered` (beside `securityevent_test.go:8-29`) asserts every `Type*`
  constant in the package is listed once, unique, snake_case, and that the two promoted literals
  equal `"session_revoked"` / `"all_sessions_revoked"` (free TEXT at rest; a rename orphans rows).
  The README states that host-bridged types (the authorization pocket's `decisions.Event*`
  strings, below) are free TEXT outside `Types()` — the authentication registry names only what
  the authentication pocket records.

### Denial coverage — what records what (H2)

Status vocabulary is unchanged (`StatusSuccess`/`StatusFailure`/`StatusBlocked`,
`securityevent.go:219-230`). Reasons are bounded constants under one Details key, `reason`
(`securityevent.ReasonInvalidCredentials`, `ReasonRateLimited`, `ReasonInactive`,
`ReasonEmailUnverified`, `ReasonInvalidToken`, `ReasonInvalidCode`, `ReasonLockout`,
`ReasonPolicy`, `ReasonInvalidState`, `ReasonExchangeFailed`, `ReasonIdentityInvalid`,
`ReasonLinkConflict`, `ReasonInvalidGrant`, `ReasonInvalidClient`, `ReasonRevisionConflict`);
never an error message, address, code or token. `StatusBlocked` is a protective control
(rate limit, lockout, deactivated account, fence); `StatusFailure` is a credential or proof
mismatch.

| path | today | after S2 |
|---|---|---|
| `IssueToken` (`token.go:52-77`) | success only | `token_issued` failure (`invalid_credentials`, `email_unverified`) / blocked (`rate_limited`, `inactive`) — Login parity via one shared `recordCredentialOutcome` used by both |
| `ChangePassword` (`service.go:767-812`) | success only | `password_change` failure (`invalid_credentials` on the wrong current password; `policy` on a rejected new password) |
| `SetPassword` (`password.go:47-91`) | success only | `password_set` failure (`policy`; grant refusal → `invalid_token`) |
| `ResetPassword` (`service.go:845-888`) | success only; `ErrPasswordResetInvalid` silent | `password_reset` failure (`invalid_token`, `policy`); no `UserID` (none resolved) |
| `Verify` (`service.go:514-582`) | success only; only the 5th wrong code yields `challenge_lockout` | `email_verified` failure (`invalid_code`, `inactive`, `revision_conflict`); the existing `challenge_lockout` row stays |
| `RemovePassword` (`password.go:161-194`) | success only | `password_removed` failure (`invalid_code`, `policy`, `revision_conflict`) |
| identifier change confirm / remove / uses (`identifier_management.go:203-360`) | success at :185, :250, :308, :349 | the matching `*_changed` / `*_removed` / `identifier_uses_changed` failure (`invalid_code`, `policy`, `revision_conflict`) |
| OAuth callback (`oauth.go:291-388`) | `oauth_login`/`oauth_register`/`oauth_linked` success only | `oauth_login` failure with `provider` + reason (`invalid_state`, `exchange_failed`, `identity_invalid`, `email_unverified`, `link_conflict`), blocked (`inactive`); `oauth_linked` failure on a link-start conflict |
| pending link issued (`startPendingLink`, `oauth.go:634-690`) | nothing | `oauth_pending_link_sent` success, `provider` only, `UserID` = the matched account (new `TypeOAuthPendingLinkSent`) |
| `VerifyLink` (`oauth.go:390-430`) | success only | `oauth_link_verified` failure (`invalid_token`, `invalid_state`), blocked (`inactive`) |
| OAuth2 `Deny` (`oauth2/authorization.go:79-85`) | nothing | `oauth2_consent_denied` success (`client_id`, `session_id`) |
| OAuth2 `RedeemCode`/`Refresh`/`Exchange`/`Revoke`/`Introspect` failures (`tokens.go:15-174`) | only refresh REUSE recorded | `oauth2_grant_failed` failure, `client_id` + reason (`invalid_grant`, `invalid_client`); refresh reuse stays `oauth2_refresh_reuse` blocked |
| OAuth2 every row | no IP/UA/actor, `sdk.IDGenerator{}` ids (`audit.go:37`) | through the ONE recorder: IP/UA, request/trace id, `Actor` from `sdk.PrincipalFromContext` when present, the host's `IDs` |
| external per-request verification (`external.go:236-276`) | none by design (README 1845) | `external_login` success once per token (new `TypeExternalLogin`): key `sha256(raw)` hex in its OWN bounded store — never the revocation cacher (`external.go:200-203`, whose entries it would evict): `ExternalConfig.LoginRecordCache cacher.Storer` (nil → `cacher.NewMemory(WithMaxEntries(cfg.LoginRecordCacheEntries))`, default 10 000) under its own namespace; TTL = `min(identity.ExpiresAt − now, 24h)`, floor 1m; a cache HIT skips the row; a miss, an eviction or a cacher ERROR records (fail toward recording — at most one extra row per eviction). Dedupe is per cache: per instance under the memory default, fleet-wide when the host supplies a shared store — README states it |
| invitation accept with `ErrIdentifierMismatch` / wrong token (`invitations/service.go`; `passes/02:186-187`) | unrecorded | `invitation_granted` failure (`invalid_token`, `policy`) — the only invitation denial paths left silent; decline/cancel already record |
| `session_revoked` / `all_sessions_revoked` (`session_management.go:30,42`) | literals | `TypeSessionRevoked`, `TypeAllSessionsRevoked` constants, README vocabulary list, registration test |
| `ListUsers` / `GetUserSummary` (`useradmin.go:56-74`) | nothing | `user_directory_read` success, `Actor` from `sdk.PrincipalFromContext` (as the OAuth2 rows do) — **signatures unchanged** (`ListUsers` is documented host API, README :2948); Details `{page: bool, target_user_id}` (FAC-17's directory half) |
| audit-rail reads | nothing | **deferred to S7** with the audit-read routes (Context; deviation (c)) |

Every new row keeps design §5.1 WI3: Details carry identifiers, kinds, prefixes and bounded
reasons only.

### PII out of audit content (M3 — decided: roadmap S2 bullet 4 + ruling 3)

- Invitation rows: `identifier` → `identifier_kind` (the invitation's `IdentifierKind`,
  `invitations/invitation.go:69`) + `identifier_digest` = `IdentifierKeyer.IdentifierKey(kind,
  normalized)` — the same keyed HMAC the outbox idempotency keys use (`delivery.go:59-70`), hex.
  New `invitations.WithIdentifierKeyer(protection.IdentifierKeyer)`; the root passes
  `cfg.IdentifierKeyer` (`constructor.go:461-475`); nil keyer → a per-instance SHA-256 fallback
  (production already requires the keyer, `constructor.go:268-269`).
- Login rows (`recordLogin`, `service.go:663-670`): `email` → `identifier_kind: "email"` +
  `identifier_digest` over the normalized value, or over the trimmed raw input when normalization
  failed (`service.go:633`), so no address — well-formed or not — lands in the rail.
- The audit digest is its OWN helper, `auditDigest(kind, value)`: keyer or SHA-256, and it NEVER
  returns the input — `identifierDigest`'s raw-on-error path (`delivery.go:72`) is not reused.
- **BREAKING for dashboards** reading `details.identifier` (invitation rows) or `details.email`
  (login rows): recorded as **AUDIT-054** with the correlation recipe. New rows only — existing
  rows keep their raw values until S3 lands retention (the handoff and control-map say so).
  Ships alone as PR-2c so it is revertable on its own.

### The decision observer (H2 / FAZ-2)

- `decisions.DecisionEvent{Operation string; Principal, Subject sdk.Principal; Permission,
  ResourceType, ResourceID string; Outcome DecisionOutcome; Requests, Allowed, Denied int;
  ErrorKind string; Duration time.Duration}`; `type DecisionOutcome string` with
  `OutcomeAllowed`, `OutcomeDenied`, `OutcomeError`, `OutcomeComplete` (batch/filter/lookup) and
  `OutcomeRefused` (role writes); `type DecisionObserver func(context.Context, DecisionEvent)`.
  **No `Bound` field and no events for bound views** (`CheckWith`, `CheckBatchWith`,
  `EvaluateWith`, `EvaluateExpressionWith`, `CheckExplainWith`): they run inside the caller's
  transaction, where a bridge's pool INSERT would take a second connection per denial and write
  duplicates on retry — the `DiagnosticObserver` exclusion (`diagnostic.go:23`) applies for the
  same reason.
- `decisions.EventAuthorizationDenied = "authorization_denied"` and
  `decisions.EventRoleWriteRefused = "role_write_refused"` live beside `DecisionOutcome` in
  `pockets/authorization/logic/decisions/observer.go` — the owner of the meaning names it; the
  authentication vocabulary carries nothing authorization-shaped.
- `decisions.WithDecisionObserver(DecisionObserver) Option`, mirroring `WithDiagnosticObserver`
  (`decisions/diagnostic.go:19-25`): one event per completed unbound public operation (`Check`,
  `CheckBatch`, `Evaluate`, `EvaluateResolved`, `EvaluateBatch`, `CheckExplain`,
  `FilterAuthorized`, `FilterPage`, `LookupResources*`, `LookupAllResourceIDs`,
  `LookupResourceIDPage`), emitted from the same `finish` site `decisionLog` uses
  (`logging.go:85-91`) but REGARDLESS of log level, after the operation returns, never inside a
  snapshot or retry callback; fields pass through `logRef` (`logging.go:105-110`) so a bound is
  guaranteed; the observer must return promptly and tolerate concurrency (the diagnostic contract).
- Root: `authorization.WithDecisionObserver(decisions.DecisionObserver) Option` forwards to the
  decision service AND to the HTTP adapter (`constructor.go:91`); `authorizationhttp.WithDecisionObserver(…)
  Option` for hosts that rebuild the adapter themselves (the baseline does, `main.go:288-296`).
- Refused role writes: `admitRoleWrite` (`roles.go:329-337`) failures from the `WritePolicy` log
  at WARN `authorization: role write refused` with `actor_type`, `actor_id`, `operation`,
  `subject_type`, `subject_id`, `role`, `scope_kind`, `scope_type`, `scope_id`, `error_kind`
  (identifiers only), and emit `DecisionEvent{Operation: "RoleWrite", Outcome: OutcomeRefused,
  Principal: actor, Subject: subject, Permission: role, ResourceType/ID: scope}`. A `ctx.Err()`
  exit (`:330-331,336`) emits NOTHING — a cancelled request is not a refusal.
- Posture (decided by the gate, not a ruling): the observer is OPT-IN. The registration log
  (`constructor.go:159`) gains `decision_observer=`; production logs one WARN when none is wired
  (`denials are not durably recorded; wire WithDecisionObserver`). No acknowledgement, no refusal.
- Host recipe — a HOST POCKET, never `cmd/` (H10; a bridge of two framework pockets is provider
  behavior, and a framework pocket may not import another): `pockets/audit/outbound/securityevents`
  in the host exposes `New(rec interface{ Record(context.Context, securityevent.Input) }, opts
  ...Option) decisions.DecisionObserver`, taking **`Components.SecurityEvents`** — the ONE
  recorder — so bridge failures count in `AuditWriteFailures()`. It records DENIALS and REFUSALS
  only (an allowed `Check` is not audit material), and it ships backpressure: a semaphore
  (`WithMaxInFlight(n)`, default 8 — when full the event is DROPPED and counted,
  `Dropped() uint64`) and a dedupe window per (principal, permission, resource type, resource id)
  through the cacher (`WithDedupe(store cacher.Storer, window time.Duration)`, default an
  in-memory store and 60 s), so a 403 flood neither drains the pgx pool nor writes a row per
  request. `cmd/` passes it to `authorization.WithDecisionObserver`. The authorization README
  ("Decision observer" under "Decision logging", `README.md:736`) carries the recipe verbatim:

  ```go
  func New(rec Recorder, opts ...Option) decisions.DecisionObserver {
      b := newBridge(rec, opts...) // semaphore + dedupe window
      return func(ctx context.Context, ev decisions.DecisionEvent) {
          switch ev.Outcome {
          case decisions.OutcomeDenied:
              b.record(ctx, ev, securityevent.Input{UserID: ev.Principal.ID, Type: decisions.EventAuthorizationDenied,
                  Status: securityevent.StatusFailure, Details: map[string]any{"operation": ev.Operation, "permission": ev.Permission,
                  "resource_type": ev.ResourceType, "resource_id": ev.ResourceID}})
          case decisions.OutcomeRefused:
              b.record(ctx, ev, securityevent.Input{Actor: securityevent.Principal{Type: ev.Principal.Type, ID: ev.Principal.ID},
                  UserID: ev.Subject.ID, Type: decisions.EventRoleWriteRefused, Status: securityevent.StatusBlocked,
                  Details: map[string]any{"role": ev.Permission, "scope_type": ev.ResourceType, "scope_id": ev.ResourceID}})
          }
      }
  }
  ```

### Tamper evidence (M1)

**The immutability triggers ship DEFAULT-ON (O-1 — deviation (a) from the roadmap's "opt-in").**
Each rail's trigger file is an ORDINARY numbered migration in BOTH dialects — authentication
`0022_security_events_immutable.sql`, authorization `0006_iam_audit_immutable.sql`, secrets
`0003_secrets_audit_immutable.sql` — exported, applied and checksummed like every other file; no
inert `_` file exists anywhere (the `_` prefix is already the host's scratch convention,
`templates/init/ledger.readme.md.tmpl:16`; a re-export would resurrect an inert file; hosts never
rename). **A host that declines deletes the file from its ledger before applying** (the handoff
says so; the probe then reports `protected_by=privilege` or refuses). Each trigger file sorts
after its rail's integrity file and after any rebuild of its table — on turso a later rebuild's
`DROP TABLE` silently drops the trigger, so every future turso rebuild of an audit table ships a
PAIRED re-create file (README + handoff). Why default-on is safe: on pgx the owner exemption (O-3)
admits single-role development databases, owner backfills and the superuser live suites (a
superuser is a member of every role; the pgx harnesses reset with `TRUNCATE … CASCADE` as the
owner); on turso there is no exemption, so the turso conformance harnesses — which reset with
`DELETE FROM` (`authentication/stores/turso/conformance_integration_test.go:79-83`,
`authorization/stores/turso/conformance_test.go:142-146`) — `DROP TRIGGER IF EXISTS` their rail
triggers before the reset and re-create them by re-executing the embedded trigger file
(test-only code). Idempotent re-execution is a property of the files: the turso files begin with
`DROP TRIGGER IF EXISTS`; the pgx files use `CREATE OR REPLACE FUNCTION` + `CREATE OR REPLACE
TRIGGER` (PG14+) then `ALTER TABLE … ENABLE ALWAYS TRIGGER`. control-map row 25 becomes **DEFAULT
(pgx: trigger + probe enforced in production) / DEFAULT (turso: trigger; probe WARN-only) / HOST
(firestore)**.

- pgx, one function per rail with its OWN dollar-quote tag (two functions share `0006`; the
  body extraction at `tuple_cache.go:193` splits on the tag), created `LANGUAGE plpgsql SET
  search_path = pg_catalog, pg_temp`, every catalog reference fully qualified. Canonical body
  (`security_events_refuse_write()`; `iam_audit_refuse_write()`, `secrets_audit_refuse_write()`
  identical but for the name):

  ```sql
  BEGIN
    IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
      IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;   -- RETURN OLD here would write the old row back: a silent no-op
      RETURN OLD;                                    -- DELETE; NULL for the statement-level TRUNCATE trigger
    END IF;
    RAISE EXCEPTION 'security_events is append-only' USING ERRCODE = '42501';
  END
  ```

  Triggers: `<table>_immutable BEFORE UPDATE OR DELETE … FOR EACH ROW` and `<table>_no_truncate
  BEFORE TRUNCATE … FOR EACH STATEMENT` (row triggers do not fire on TRUNCATE; `GRANT ALL` gives
  non-owners TRUNCATE and `pg_write_all_data` does not cover it, so the statement trigger is not
  made redundant by the exemption), both `ENABLE ALWAYS` so a provider admin role's `SET
  session_replication_role = replica` cannot silence them. `current_user` is the right identity:
  it refuses a session that ran `SET ROLE app` and exempts a SECURITY DEFINER purge whose owner is
  the migrator. `pgxdb.MapError` gains the 42501 case → `sdk.ErrForbidden` (`postgres.go:417-439`
  has none today) — **a PR-1 behavior change for every consumer** (any `insufficient_privilege`
  now surfaces as `sdk.ErrForbidden`, 403 through the web mappers); listed in the pgxdb README and
  RELEASING, with a test.
- **The owner exemption (O-3b).** The migrator — and a migrator-owned SECURITY DEFINER function —
  passes the trigger, so S3's purge needs no `DISABLE TRIGGER` (which takes a lock that stalls
  rail inserts and therefore logins). It is not free: on a host that WAIVES the probe and runs
  as the owner, the exemption turns a DDL-only bypass (drop the trigger) into plain DML — SQL
  injection on such a host can delete rows — which is why the waiver's WARN reads "runtime role
  owns <t>: immutability triggers do not apply" with `trigger_exempt=true`, and why the probe
  refuses an owner in production without the waiver.
- `iam_operations` (O-3a, fenced-only — deviation (d)): UPDATE always refused; DELETE refused
  only when the row is fenced — `OLD.outcome = 'fenced'` (`0004_iam_operations_fenced.sql`:
  column `outcome`, value `'fenced'`), tested IN THE FUNCTION BODY, never a WHEN clause, the
  allowed branch `RETURN OLD`s; TRUNCATE refused; owner-exempt like the rails. Nothing UPDATEs or
  DELETEs `iam_operations` today (`stores/pgx/operations.go:45,78`, `stores/turso/operations.go:45,75`:
  `INSERT … ON CONFLICT DO NOTHING` fires no UPDATE trigger), so no store path changes; S3's
  `Expire` deletes non-fenced rows under the runtime role, and the two-role recipe KEEPS DELETE on
  this table (reversing the S0 recipe's revoke, `database-posture.md:25` — S0 hosts grant it back,
  handoff). Turso gets the same two triggers unconditionally (`iam_operations_immutable_update`
  always aborts; `iam_operations_immutable_delete` aborts when `OLD.outcome = 'fenced'`, the test
  in the body).
- **S3 constraints, written now (O-3c):** the SECURITY DEFINER `<t>_purge_before(ts)` S3 ships is
  owned by the migrator and keeps its retention floor INSIDE the function under owner control —
  never from the caller's argument; every purge appends a purge row (cutoff, count,
  `session_user`) to the rail it purged; S2 ships the live test that a migrator-owned SECURITY
  DEFINER function passes the exempt branch, so S3 inherits a proven path. Turso purge = `DROP
  TRIGGER`, `DELETE`, re-`CREATE` in one transaction (no owner concept). Documented in both
  dialects' READMEs: SQLite `INSERT OR REPLACE` deletes rows WITHOUT firing DELETE triggers — no
  audit-table path may use it. In-place IP/UA erasure (an UPDATE) is S3's to design against the
  owner-exempt UPDATE branch (which now persists, see the body).
- turso: `<table>_immutable_update BEFORE UPDATE … SELECT RAISE(ABORT, '<table> is append-only')`
  and `<table>_immutable_delete BEFORE DELETE …`, each preceded by `DROP TRIGGER IF EXISTS`.
  The turso connector's `MapError` (`turso.go:281-292`) matches `strings.Contains(msg, "is
  append-only")` — modernc formats the error `constraint failed: <msg> (1811)` — and maps it to
  `sdk.ErrForbidden`.
- Live tests per store, as a fixture role (non-owner) and as the owner: non-owner `UPDATE`,
  `DELETE`, `TRUNCATE` → `sdk.ErrForbidden`; owner `UPDATE` PERSISTS (the `RETURN NEW` fix); owner
  `DELETE` passes; a migrator-owned SECURITY DEFINER function deleting as a non-owner caller
  passes; `iam_operations`: non-owner DELETE of a non-fenced row passes, of a fenced row refused,
  UPDATE refused. The existing bare-schema rewrites (`stores/pgx/fresh_schema_test.go:116-117`,
  `audit_live_test.go:232`) run as the owner and keep passing.

**The posture probe** lives in the connector and runs inside the pgx store constructors that
already probe the schema (decided by precedent: S1's in-constructor refusals), in its OWN
transaction AFTER the canonical-schema probe commits (its `search_path` is `pg_catalog`,
`schema.go:52`), with `SET LOCAL search_path TO pg_catalog` and schema-qualified names, under the
mode the DB was opened with (O-2):

- **Per-table spec, not two variadics:** `pgxdb.AppendOnlyTable{Name string; AllowDelete bool;
  Canonical []TriggerBody}`; `pgxdb.InspectTablePrivileges(ctx, q Querier, role string, tables
  ...AppendOnlyTable) (Report, error)` (`role` empty = `current_user`, so a MIGRATOR can preflight
  the runtime role — the host runner's `-posture-role <name>`, which prints
  `protected_by=privilege|trigger|waiver` per table and whose output the handoff tells hosts to
  ARCHIVE; auditors read grants, `\dp` shows `arwd`, not trigger bodies); `(*pgxdb.DB).ProbeAppendOnly(ctx,
  tables ...AppendOnlyTable) error`; each store's `AuditTables(opts ...Option) []pgxdb.AppendOnlyTable`
  (authentication: `security_events`; authorization: `iam_audit` when `WithAudit()`, and
  `iam_operations` with `AllowDelete: true`; secrets: `secrets_audit`) — so `-posture-role` and
  boot decide IDENTICALLY. `Report` holds one `TablePrivilege{Table string; Exists, Owner,
  OwnerKind (table|schema|function), Superuser, Insert, Update, Delete, Truncate, Trigger,
  SequenceUsage, ImmutableTrigger bool; TriggerState string}` per table.
- `Writable()` = `Owner || Superuser || ((Update || (Delete && !AllowDelete) || Truncate ||
  Trigger) && !ImmutableTrigger)` — the TRIGGER privilege counts because a role holding it can add
  a `BEFORE INSERT` trigger that returns NULL and drops audit rows without an error;
  `CannotAppend()` = `!Insert || !SequenceUsage`.
- Evidence per table, the oid resolved ONCE with `to_regclass` (so a missing relation is
  `Exists=false`, never the error `has_table_privilege(name, text, text)` raises); the `seq`
  sequence found through a `pg_attribute`/`pg_attrdef` join (NEVER `pg_get_serial_sequence`,
  which raises 42703 on `iam_operations`). `Owner` = `pg_has_role(role, pg_class.relowner,
  'MEMBER')` (membership of the owning role, NOINHERIT members and PG16 SET-ROLE members
  included) OR the SCHEMA owner (`pg_namespace.nspowner` — it can `DROP TABLE`; on PG15+ `public`
  belongs to `pg_database_owner`, which Cloud SQL's cloudsqlsuperuser and Neon/Supabase default
  roles reach) OR the trigger FUNCTION's `proowner` (it can `CREATE OR REPLACE` the body); the
  refusal prints `owner=table|schema|function`. `Superuser` = `pg_roles.rolsuper`. `Insert` =
  `has_table_privilege(role, oid, 'INSERT')`; `Update` = `has_any_column_privilege(role, oid,
  'UPDATE')` (a column-level grant is a grant); `Delete`/`Truncate`/`Trigger` =
  `has_table_privilege`; `SequenceUsage` = `has_sequence_privilege(role, <seq oid>, 'USAGE')`
  (true when the table has no `seq` column). **`ImmutableTrigger` = ALL of:** both triggers
  exist, are not internal, `tgenabled = 'A'` (`'O'` is what `pg_restore --disable-triggers` leaves
  behind and what `session_replication_role` silences — the refusal names `tgenabled=O` with the
  `ENABLE ALWAYS TRIGGER` fix), `tgqual IS NULL` (no WHEN clause), empty `tgattr` (no `UPDATE OF
  col`), row-level for `_immutable` / statement-level for `_no_truncate`, `pg_get_triggerdef`
  text equal to the expected CATALOG form (`BEFORE DELETE OR UPDATE`; the function qualified as
  the catalog prints it under `search_path = pg_catalog`), `prosrc` equal to the canonical body
  the store passed, `NOT prosecdef` (`ALTER FUNCTION … SECURITY DEFINER` leaves `prosrc` intact
  and would exempt every caller), language plpgsql, the function in the table's namespace, and
  `proconfig` EXACTLY `{search_path=pg_catalog, pg_temp}` (the function is created with that SET
  and its body fully qualifies every catalog reference — a `pg_temp` view named `pg_class` would
  otherwise make the runtime role "owner"). Names are bound, never concatenated (the `ProbeTable`
  discipline, `probe.go:21-31`).
- `(*pgxdb.DB).RuntimeMode() environment.Mode` — `Open` retains `cfg.RuntimeMode`, `cfg.Logger`
  and the new **`Config.AppendOnlyWritableAcknowledged bool`** (programmatic, no env tag,
  asserted by the no-env-tag test; the pair of `ErrAppendOnlyTableWritable`) on the `DB`
  (`db.go:14-16` holds only the pool today).
- `ProbeAppendOnly`: zero mode → nil, silent; **development never refuses** and prints ONE
  combined line `append_only=unenforced tables=[…]` (not N WARNs per boot and test); production →
  `errors.Join` of one error per finding wrapping **`pgxdb.ErrAppendOnlyTableWritable`**
  (`Writable()`) or **`pgxdb.ErrAppendOnlyTableUnwritable`** (`CannotAppend()` — a role that
  cannot INSERT or has no USAGE on the sequence would lose every audit row silently on the
  fail-open rail and fail every write on the fail-closed ones), naming the table, the reason
  (`owner=…`, `superuser`, `privileges=update,delete,truncate,trigger`, `tgenabled=O`,
  `trigger=absent|differs`) and the fix. The fix text begins **"activate the trigger files;
  create the runtime role with SQL, not the console"** (Neon console/API roles carry
  `pg_write_all_data` through `neon_superuser`, which no per-table REVOKE removes; rds_superuser
  can set `session_replication_role`), then `REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON <t> FROM
  <role>` (DELETE kept on `iam_operations`), `GRANT USAGE ON SEQUENCE <t>_seq TO <role>`, `ALTER
  TABLE <t> OWNER TO <migrator>`, `ALTER SCHEMA public OWNER TO <migrator>`, `ALTER TABLE <t>
  ENABLE ALWAYS TRIGGER <name>`. `ErrAppendOnlyTableWritable` is waivable (`posture_waiver=
  AppendOnlyWritableAcknowledged`, one WARN per table "runtime role owns <t>: immutability
  triggers do not apply" with `trigger_exempt=true`, nil); `ErrAppendOnlyTableUnwritable` is NOT
  (nothing to acknowledge — the rail would be dead). Owner/superuser is refused even with a
  trigger (the owner is exempt from it by design) and is the common single-role case — the
  scaffold's runner and server read the same `pgxdb.Config` env (`production-baseline/workshop/
  migrations/main.go:90-95`), so **every production host on one role refuses to boot after
  repinning a pgx store** (BREAKING at runtime, Risk 1); the two-role posture in
  `workshop/documentation/docs/guides/database-posture.md` ("Two roles", :10) is the fix — it now
  revokes UPDATE/DELETE/TRUNCATE/TRIGGER on the three rails and UPDATE/TRUNCATE/TRIGGER on
  `iam_operations`, grants `USAGE ON SEQUENCE` and `ALTER DEFAULT PRIVILEGES … GRANT USAGE ON
  SEQUENCES`, and says "create the runtime role with SQL, not the console".
- Who calls it: `authentication/stores/pgx.Repositories` (`postgres.go:94-140`, after the column
  probes) over its `AuditTables()`; `authorization/stores/pgx.Repositories` after
  `probeCanonicalSchema` commits; **secrets gains `pgx.Open(ctx, db, opts ...Option)
  (vault.Repository, error)`** — a ctx-taking constructor shaped like the authentication store's,
  probing the three tables, the `outcome` column (naming 0002) and the posture — because today
  `NewRepository` does no I/O and nothing in the repo calls `StatusCheck` (`pgx.go:77`), so "no
  host wiring" was false for secrets; `NewRepository` stays for hosts that probe themselves;
  `StatusCheck` is documented BOOT-ONLY (wiring it to readiness repeats the WARN on every probe).
  No P rule for the probe; the P5 surfaces list gains the acknowledgement (PR-5).
- turso has no privilege model: `(*tursodb.DB).RuntimeMode()` and
  `(*tursodb.DB).ProbeAuditImmutable(ctx, tables ...tursodb.AppendOnlyTable) error` read
  `sqlite_schema WHERE type='trigger' AND tbl_name=?`, compare the canonical text (precedent
  `tuple_cache.go:54`), and WARN in production when a rail lacks the pair or the text differs
  (`turso: audit table has no immutability triggers; restore the migration file`); never a
  refusal. The turso stores call it where they probe columns (`stores/turso/turso.go:74-82`,
  authorization `probeCanonicalSchema`).
- Live suites connect as a superuser today; every test that builds a store in production mode
  trips the probe, so each live suite gains a fixture role (unique name per run, table grants
  only plus sequence USAGE, `DROP OWNED BY` on cleanup) and one case that runs as a role with
  TABLE grants only and proves `ErrAppendOnlyTableUnwritable` (the sequence trap).

**Time source:** `recorded_at` is the database clock on every rail (NULL on rows that predate
S2 — a live test in both dialects inserts a row before the migration and asserts
`recorded_at IS NULL` after); `created_at`/`occurred_at` stay the application clock the ordering
contracts page by (unchanged cursors). NTP is a host requirement (`database-posture.md` "Time",
:66).

### `guard --posture` and the framework guards

No H rule requires a host ledger to be complete for the stores it mounts (H0–H14 are import and
layout rules, `examples/README.md:139-153`), so S2 adds **P9**: for every framework store module
the host's `go.mod` requires whose package ships a `migrations/` tree (the pgx/turso stores of
authentication, authorization, cms, events, jobs, secrets), every `NNNN_*.sql` in that tree has a
byte-identical copy (any number) in a host migration tree under `workshop/**` or `cmd/**`;
`--posture` resolves the module directory with `go list -m -f '{{.Dir}}' <module>` (the module
cache, as `TestWarmScaffoldModuleCache` does) and reports `no scope` when the host requires no
such module. The migration version is the filename and no store body contains its number
(`cmp`-verified on the authorization pair), so a renumbered copy stays byte-identical. §3b gains
the P9 row ("These nine lines" → ten), `TestGuardListMatchesContract` the row, and PR-5 runs P9
over every example (a drifted ledger is reconciled by re-export — a file copy, no behavior
change). The §3b "Recognized posture surfaces" acknowledgement list (`examples/README.md:312-317`)
gains `AppendOnlyWritableAcknowledged` so P5 checks it is a literal and prints its `ack` line
(ruling 2). Framework guard **G34** `guard-baseline-ledger`: for every store the baseline mounts
(`examples/production-baseline/go.mod`: `authentication/stores/pgx`, `authorization/stores/pgx`
— secrets is EXCLUDED because no example mounts it), every file in the store's `migrations/`
has a byte-identical copy in `examples/production-baseline/workshop/migrations/primary` (P9 over
the reference host, enforced in this repo's `make check`); joins `guard:` after G33
(`Makefile:207-215`); the "thirty-three" prose becomes thirty-four. No inert files, no `all:`
embed switch, no inert `-status` listing, no `TestMigrationInventory`/`TestMigrationParity`
edits; `-status` and `-posture-role` are the host runner's only S2 changes.

### Version table

| module | current | after S2 | why |
|---|---|---|---|
| `pockets/authorization/stores/pgx` | v0.19.0 | **v0.19.1** (Step 0, O-4) | ships 0005 (expand; not required), optional-column probe; tagged on `main` after 0005's text is final — a patch by the rulings review (0005 is shipped, not required; no sibling bump) |
| `pockets/authorization/stores/turso` | v0.18.0 | **v0.18.1** (Step 0, O-4) | ships 0005; accepts 0001-or-0005 / 0004-or-0005 bodies |
| `integrations/datastores/pgxdb` | v0.10.0 | **v0.11.0** | `AppendOnlyTable` probe API, `DB.RuntimeMode()`, `Config.AppendOnlyWritableAcknowledged`, `MapError` 42501 (behavior change) |
| `integrations/datastores/turso` | v0.7.0 | **v0.8.0** | `DB.RuntimeMode()`, `ProbeAuditImmutable`, `MapError` append-only text |
| `pockets/authentication` | v0.20.0 | **v0.21.0** | pre-1.0 BREAKING Details flip (AUDIT-054, PR-2c); `SecurityEvent` ids; `Components.SecurityEvents`; recorder, options, vocabulary. No signature changes |
| `…/stores/pgx` | v0.9.0 | **v0.10.0** | migrations 0021 (REQUIRED, column probe) + 0022 (default-on trigger); the posture probe (runtime BREAKING for single-role production hosts); repins core + pgxdb v0.11.0 (a multi-version jump from pgxdb v0.7.1 / core v0.19.1 — tidy `GOWORK=off` after tag 1) |
| `…/stores/turso` | v0.8.0 | **v0.9.0** | migrations 0021 (REQUIRED) + 0022; harness trigger reset; repins core + turso v0.8.0 |
| `…/stores/firestore` | v0.2.0 | **v0.3.0** | maps the two ids (PR-2a); repins core v0.21.0 in the post-tag repin step and is tagged THERE (HEAD must not be ahead of its tag under `GOWORK=off`); no triggers/sequence/roles (README) |
| `pockets/authorization` | v0.28.0 | **v0.29.0** | observer + `Event*` names, `Source` fields, WARN on refused writes |
| `…/stores/pgx` | v0.19.1 | **v0.20.0** | 0005 REQUIRED + LOCKSTEP; 0006 default-on trigger; the posture probe; repins core + pgxdb v0.11.0 |
| `…/stores/turso` | v0.18.1 | **v0.19.0** | 0005 REQUIRED + LOCKSTEP (rebuilds; rewritten probe); 0006; harness trigger reset; repins core + turso v0.8.0 |
| `…/stores/goredis` | — | untouched | tuple cache only |
| `pockets/secrets` | v0.1.1 | **v0.2.0** | pre-1.0 BREAKING port: `Repository.AppendAudit` added, `DeleteOwner` gains `expected []string` (a compile break for host-implemented repositories) |
| `…/stores/pgx` | v0.1.0 | **v0.2.0** | migrations 0002 (REQUIRED) + 0003 (default-on trigger); `Open(ctx, …)` with the posture probe; repins core + pgxdb v0.11.0 |
| `workshop/gopernicus` | v0.5.0 | **v0.6.0** | P9, surfaces list, scaffold pins, runner template (`-posture-role`), docs |
| `sdk` | v0.16.0 | untouched | `RequestIDFromContext`/`TraceIDFromContext` already exist |

Deltas versus rev 2: Step 0 moves to `main` and ships 0005; trigger files are numbered store
migrations in every store (no inert files); P9 added; the waiver renamed. Fourteen tags
(unchanged).

## Tasks — the PR train

Order: **PR-0 (expand) and PR-1 connectors first** (PR-0 needs nothing from PR-1; the stores in
PR-2a/3/4 call `DB.RuntimeMode()`/`ProbeAppendOnly`), then PR-2a, PR-3 and PR-4 in parallel
worktrees stacked on PR-1's branch (their CORE modules need nothing from PR-1; their STORE
modules do; PR-3 stacks on PR-0 too), PR-2b and PR-2c stacked on PR-2a, PR-5 workshop beside
them, PR-6 examples after tags 1–3 resolve, PR-7 record. Model `opus` unless marked. Retarget a
stacked child to `main` BEFORE merging it; never `--delete-branch` a stacked base; one push, tag
or merge per command.

### PR-0 — authorization stores: migration 0005 (expand) + tolerant probes (`authorization/stores-expand`) — O-4

Worktree `../gopernicus-s2-expand`, an ordinary PR to `main` (the ruleset requires `check` +
`vuln`; `git diff 10135055 HEAD` is empty for both stores, the core and pgxdb, so `main` IS the
v0.19.0 / v0.18.0 tree for these modules).

- **files:** `pockets/authorization/stores/pgx/migrations/0005_iam_audit_integrity.sql`
  (`SET LOCAL lock_timeout`; two-step nullable adds; `iam_audit_seq` + `OWNED BY` + `GRANT USAGE
  … TO PUBLIC`; `recorded_at` on `iam_operations`; NO constraint), `stores/pgx/schema.go:55-91,
  98-126` (an `optionalColumns` map per table — four on `iam_audit`, `recorded_at` on
  `iam_operations`, none on `iam_tuples`: type, collation C, nullable; tolerated when present,
  never required, never written) + `schema_probe_test.go`; `pockets/authorization/stores/turso/
  migrations/0005_iam_audit_integrity.sql` (both rebuilds, `NULL AS recorded_at`, indexes),
  `stores/turso/schema.go:19-50` (compiles 0005's `*_next` bodies; accepts the 0001-or-0005 body
  for `iam_audit` and the 0004-or-0005 body for `iam_operations`) + test; `stores/pgx/migrations_test.go`
  (`TestMigrationInventory`/`TestMigrationParity` count 0005; a test pins both 0005 bodies so a
  later edit fails loudly — Step 0 commits to the text); both `README.md` ("v0.19.1 / v0.18.1:
  ship and tolerate 0005; adopt BEFORE migrating it").
- **behavior:** byte-for-byte v0.19.0 / v0.18.0 otherwise; a database with or without 0005 is
  admitted; inserts list their columns so the new columns take defaults.
- **BREAKING:** no. **escape hatch:** n/a.
- **tests:** live: construct against 0001–0004 and against 0001–0005 — both admitted; a fifth
  unknown column — refused; a 0005 that added a CHECK — refused (so the file never grows one);
  writes on a 0005 database succeed as a role with table grants only (the PUBLIC sequence grant
  is load-bearing).
- **verify:**
  ```
  cd pockets/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/authorization/stores/turso && go build ./... && go test ./... && go vet ./...
  make check
  make test-stores                                      # authorization pgx legs, 0004 and 0005 schemas
  cd pockets/authorization/stores/turso && TURSO_AUTH_TOKEN=local AUTHORIZATION_TURSO_DISPOSABLE_URL=http://127.0.0.1:8081 go test -tags=integration ./... 2>&1 | tee /dev/stderr | grep -q SKIP && exit 1 || true
  ```
- **closes:** the rollback trap (platform-sre). **tag:** step 0 —
  `pockets/authorization/stores/pgx/v0.19.1`, `…/stores/turso/v0.18.1` at the merge commit on
  `main`, cut only after PR-3's review has confirmed 0005's text (the pin test guards it).

### PR-1 — connectors: append-only probe, trigger probe, 42501 mapping (`integrations/audit-integrity`)

Worktree `../gopernicus-s2-conn`.

- **files:** `integrations/datastores/pgxdb/probe.go` (`AppendOnlyTable`, `TriggerBody`,
  `TablePrivilege`, `Report`, `InspectTablePrivileges(ctx, q, role, tables...)`,
  `(*DB).ProbeAppendOnly`, `ErrAppendOnlyTableWritable`, `ErrAppendOnlyTableUnwritable`) +
  `probe_test.go` (unit: the policy matrix over a fake report incl. `AllowDelete`) +
  `probe_live_test.go` (live, with a fixture role: table grants only — `Unwritable` on the
  sequence; with sequence USAGE and REVOKEs — clean, `protected_by=privilege`; grants kept but
  verified triggers — clean, `protected_by=trigger`; a MEMBER of the owning role; a NOINHERIT
  member; the schema owner; the trigger function's owner; a TRIGGER grant; a column-level UPDATE
  grant; a superuser; the trigger pair present / only the row trigger / `tgenabled='O'` / a `WHEN`
  clause / `UPDATE OF id` / a body that differs / `SECURITY DEFINER` altered / `proconfig` missing
  — each refused and each naming its reason; preflighting a named role from the owner's
  connection; a table with no `seq` column (`iam_operations`) inspected without error);
  `postgres.go` (`Config.AppendOnlyWritableAcknowledged`, the `DB` fields and `RuntimeMode()` set
  in `Open` at :354; `MapError` 42501 → `sdk.ErrForbidden` at :417-439) + the no-env-tag test
  beside `posture_test.go` + the `MapError` test; `README.md` ("Append-only tables: the posture
  probe", the one-sentence contract, the fix text, the Neon/RDS/Cloud SQL notes, the 42501
  behavior change, the PostgreSQL ≥ 14 requirement, the managed-provider role recipe pointing at
  `database-posture.md`); `integrations/datastores/turso/turso.go` (`RuntimeMode()`; `MapError`
  `strings.Contains(msg, "is append-only")` at :281-292), new `probe.go` (`AppendOnlyTable`,
  `ProbeAuditImmutable`, `TableTrigger`, `TriggerBody`) + tests + README.
- **behavior:** the probe section of Design. Zero mode is silent; development prints the one
  combined line; production refuses or waives (`Writable` only). The migration runner is
  untouched.
- **BREAKING:** `MapError` now classifies 42501 as `sdk.ErrForbidden` on pgx and the canonical
  append-only text on turso — a behavior change for every consumer (a host that matched the raw
  driver error for `insufficient_privilege` sees a sentinel now); recorded in RELEASING. The
  REFUSAL reaches production hosts only through the store repins (PR-2a/3/4).
  **escape hatch:** `AppendOnlyWritableAcknowledged: true` (a literal; P5 prints it) — for
  `Writable` only.
- **tests:** the matrix above; `errors.Join` names every table; acknowledged → `posture_waiver`
  WARN with `trigger_exempt=true`; the development line; zero mode silent; turso: triggers
  present/absent/different × mode; both `MapError` cases incl. the modernc `constraint failed:
  … (1811)` shape.
- **verify:**
  ```
  cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...
  cd integrations/datastores/turso && go build ./... && go test ./... && go vet ./...
  make check
  make test-stores                                      # scratch Postgres 17 on 127.0.0.1:55433 (never 55432)
  cd integrations/datastores/turso && TURSO_AUTH_TOKEN=local TURSO_DATABASE_URL=http://127.0.0.1:8081 go test -tags=integration ./... 2>&1 | tee /dev/stderr | grep -q SKIP && exit 1 || true
  ```
- **closes:** FAS-4 (probe half), FAS-7 (probe), FOP-3 (probe).
- **tag:** step 1 — `integrations/datastores/pgxdb/v0.11.0`, `integrations/datastores/turso/v0.8.0`.
  Cut at the merge commit, pushed, THEN polled on `.info`; the cold-verify ALSO builds the MVS
  pair authorization stores v0.19.1 on pgxdb v0.11.0 (a `-posture-role` host sits exactly there,
  including the 42501 mapping change); dependents tidy `GOWORK=off` against the published tags
  before their own `make check` leaves the workspace.

### PR-2a — authentication: the recorder, correlation, constants, write observer, migrations 0021 + 0022 + stores (`authentication/audit-recorder`)

Worktree `../gopernicus-s2-auth`, stacked on `integrations/audit-integrity` (the stores).

- **files (core):** `pockets/authentication/logic/authentication/securityevent/securityevent.go`
  (`RequestID`/`TraceID`; `TypeSessionRevoked`, `TypeAllSessionsRevoked`, the OAuth2 family;
  `Types()`; `MaxCorrelationIDLen`), new `recorder.go` (`Input`, `Recorder`, `NewRecorder`,
  `Record` with `WithoutCancel` + 2 s, `WithClientInfoReader`, `WriteFailure`, `WriteObserver`,
  `WithWriteObserver`, `WriteFailures`, the once-then-per-minute WARN with key `audit_write_failed`,
  `ErrorKind`) + `recorder_test.go`, `securityevent_test.go` (`TestEventTypesAreRegistered`);
  `logic/authentication/securityevent.go` (`ErrorKind` forwarder; `securityEventInput` →
  `securityevent.Input` alias) and `helpers.go:18-37` (collapse onto the recorder), `options.go`
  (`WithSecurityEventRecorder`, `WithAuditWriteObserver`), `logic/authentication/constructor.go:180-191`
  (the external authenticator inherits the service recorder), `session_management.go:30,42`,
  `useradmin.go:56-74` (`user_directory_read`, actor from `sdk.PrincipalFromContext`, signatures
  unchanged), `oauth2/audit.go` (the recorder; `WithSecurityEventRecorder`),
  `logic/invitations/options.go` (`WithSecurityEventRecorder`; precedence over
  `WithSecurityEvents`; `ErrSecurityEventsConflict`), `service.go:940-946` (the `authlogic` import
  goes); root `constructor.go` (ONE recorder with the carrier reader; `Components.SecurityEvents`;
  `Components.AuditWriteFailures`), `authentication.go:17-24` (the field), `config.go`/`options.go`
  (`WithAuditWriteObserver`); `README.md` (§ "Audit" :2782 and the vocabulary list :3343 — the
  constants, the correlation rule, the recorder, the ONE-recorder contract, the failure matrix,
  "host-bridged types are free TEXT outside `Types()`").
- **files (stores):** `stores/pgx/migrations/0021_security_events_integrity.sql` (`SET LOCAL
  lock_timeout`; two-step nullable adds; `security_events_seq` + `OWNED BY` + `GRANT USAGE … TO
  PUBLIC`), `0022_security_events_immutable.sql` (`lock_timeout`; `CREATE OR REPLACE FUNCTION
  … SET search_path = pg_catalog, pg_temp` with its own dollar tag; `CREATE OR REPLACE TRIGGER`
  ×2; `ENABLE ALWAYS` ×2); `stores/turso/migrations/0021_security_events_integrity.sql`
  (rebuild, `NULL AS recorded_at`, index re-creation), `0022_security_events_immutable.sql`
  (`DROP TRIGGER IF EXISTS` ×2 then the pair); `stores/pgx/security_events.go` and
  `stores/turso/security_events.go` (`securityEventColumns` + `request_id`, `trace_id`; the
  hand-listed empty-id insert at pgx `:92` / turso `:101` too; `seq`/`recorded_at` never named),
  `stores/pgx/postgres.go:154-161` and `stores/turso/turso.go:131-139` (`probeColumns` +=
  `{securityEventsTable, "request_id", "0021_security_events_integrity.sql"}`; the posture /
  trigger probe call; `AuditTables() []pgxdb.AppendOnlyTable` with the embedded canonical
  body), `stores/turso/conformance_integration_test.go:79-83` (the harness drops the rail
  triggers before `DELETE FROM` and re-executes the embedded 0022 after),
  `stores/firestore/securityevents.go:31` + `securityevents_doc.go` (map the two ids — the
  workspace emulator leg runs the workspace storetest, `conformance_test.go:84`) + `README.md` +
  `SCHEMA.md` §7.3 (the host-IAM tamper posture; no sequence), `examples/auth-cms/pockets/
  authentication/outbound/authmem` (the two ids — PR-2a's verify already requires auth-cms
  green), `stores/storetest/storetest.go:211-234` (new case `CorrelationRoundTrip` under BOTH id
  strategies — app-minted and `sdk.DatabaseID`: both ids round-trip through `Create` and `List`;
  a 129-byte id presented to `Create` is stored as given — blanking is the recorder's job, not the
  store's), `stores/pgx/README.md` + `stores/turso/README.md` ("0021 required", "0022 is the
  immutability trigger — delete it from your ledger before applying if you decline", the paired
  re-create rule, the no-UPDATE-backfill rule, `INSERT OR REPLACE` never on this table, two-role
  pointer with the sequence grant, PostgreSQL ≥ 14); `go.mod` of pgx and turso stores → core
  v0.21.0 + connector v0.11.0/v0.8.0 (firestore repins in the post-tag step); live tests for the
  trigger file (non-owner refused, owner UPDATE persists, SECURITY DEFINER passes), for the
  `seq`/`recorded_at` defaults (`INSERT` through the store, `SELECT seq, recorded_at` raw:
  non-NULL, increasing), for `recorded_at IS NULL` on a pre-migration row (both dialects), and
  the fixture-role sequence-trap case.
- **behavior:** the recorder section; `user_directory_read` once per call (decided by the gate).
- **BREAKING:** no exported signature change. Stores repinning v0.10.0/v0.9.0 refuse to boot
  until 0021 is applied (the column probe names the file) and, in production, until the role
  posture holds (Risk 1).
- **escape hatch:** `AppendOnlyWritableAcknowledged` for a writable table; none for an
  unwritable one.
- **tests:** `Recorder` blanks a 129-byte or non-printable id and keeps a 128-byte one; a
  cancelled request context still writes the row (`WithoutCancel`); the 2 s timeout fires on a
  hung store; the write-failure WARN once, then one summary line per minute, the counter and the
  observer every time; the registration test; `user_directory_read` carries the actor; the
  ONE-recorder contract (`Components.SecurityEvents` is the pointer every consumer holds);
  storetest over pgx, turso, firestore (emulator) and `authmem`.
- **verify:**
  ```
  cd pockets/authentication && go build ./... && go test ./... && go vet ./...
  cd pockets/authentication/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/authentication/stores/turso && go build ./... && go test ./... && go vet ./...
  cd pockets/authentication/stores/firestore && go build ./... && go test ./... && go vet ./...
  cd examples/auth-cms && go build ./... && go test ./... && go vet ./...
  make guard                                            # G20 over the new text; G33 (the recorder reads no env)
  make check
  make test-stores                                      # authentication pgx bare + schema-qualified, incl. the trigger + fixture-role cases
  cd pockets/authentication/stores/turso && TURSO_AUTH_TOKEN=local AUTHENTICATION_TURSO_DISPOSABLE_URL=http://127.0.0.1:8081 go test -tags=integration ./... 2>&1 | tee /dev/stderr | grep -q SKIP && exit 1 || true
  ```
- **closes:** FAC-10 (rail half), FAS-4 / FAS-6 (authentication rail), H1 (write-failure
  observer), FAC-17 (directory half).
- **tag:** step 2a `pockets/authentication/v0.21.0` is cut only after PR-2a, PR-2b AND PR-2c merge
  (one core tag for the three); step 2b `…/stores/pgx/v0.10.0`, `…/stores/turso/v0.9.0`,
  `…/stores/firestore/v0.3.0` after 2a and step 1 resolve and a `GOWORK=off` tidy (firestore's
  repin PR lands here). `views/goth` untouched.

### PR-2b — authentication: denial coverage and `external_login` (`authentication/audit-denials`)

Worktree `../gopernicus-s2-auth-b`, stacked on `authentication/audit-recorder`.

- **files:** `securityevent/securityevent.go` (`TypeOAuthPendingLinkSent`, `TypeExternalLogin`,
  `Reason*`); `token.go`, `service.go` (`Login`/`IssueToken` share `recordCredentialOutcome`;
  `Verify`, `ChangePassword`, `ResetPassword`), `password.go`, `identifier_management.go`,
  `oauth.go` (`recordOAuth(ctx, userID, provider, type, status, reason)`), `external.go` +
  `external_config.go` (`ExternalConfig.LoginRecordCache`, `LoginRecordCacheEntries`; the
  `external_login` dedupe in its own store and namespace), `oauth2/authorization.go:79-85`,
  `oauth2/tokens.go`, `logic/invitations/service.go` (the two silent accept denials); `README.md`
  (every new type, the reason set, the `external_login` dedupe note).
- **behavior:** the coverage table. Recording never fails a flow.
- **BREAKING:** no. **escape hatch:** none (it is the control).
- **tests:** one failure/blocked case per path in the coverage table asserting type, status,
  reason, UserID presence and absence, and that no Details value equals the attempted address,
  code or token (an `assertNoSecretInDetails` helper over every recorded row in the fixture
  recorder); `external_login` recorded once across three verifications of one token, again after
  the TTL, again after an eviction, and the revocation cache's entry count unchanged by login
  records; OAuth2 rows carry IP/UA and a host id. Run-and-look: `cd examples/auth-cms &&
  AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 make run`; `POST /auth/token` with a wrong password
  → 401 and `GET /debug/security-events` (as the seeded admin) shows a `token_issued failure
  reason=invalid_credentials` row whose `request_id` equals the 401's `X-Request-ID`.
- **verify:** the PR-2a core lines (`pockets/authentication`, `examples/auth-cms`, `make guard`,
  `make check`); no store or migration change.
- **closes:** FAC-4, FAF-2, FAF-3, FAF-8, H2 (authentication half).
- **tag:** none of its own (step 2a after 2c).

### PR-2c — authentication: AUDIT-054, the PII flip (`authentication/audit-pii-digest`) — BREAKING, revertable alone

Worktree `../gopernicus-s2-auth-c`, stacked on `authentication/audit-denials`.

- **files:** `service.go:663-670` (`recordLogin` → `identifier_kind` + `identifier_digest` via
  `auditDigest`), `delivery.go` (new `auditDigest`, never raw), `logic/invitations/options.go`
  (`WithIdentifierKeyer`), `service.go:885-946` (digest + kind), root `constructor.go:461-475`
  (passes the keyer); `AUDIT.md` (**AUDIT-054: audit Details carry identifier digests, not
  addresses** after AUDIT-053 at :5574 — Implemented/Module/Impact, the before/after keys, "new
  rows only; existing rows keep raw values until S3", the runnable digest command for an
  unknown-address lookup — `printf '%s' "email:<normalized>" | openssl dgst -sha256 -mac HMAC
  -macopt hexkey:$AUTH_IDENTIFIER_KEY` (the keyer's exact construction is pinned by a test so
  the command stays true), and "known users are found by `user_id`"; verification: a failed
  login row carries `identifier_digest` and no `email`; an `invitation_created` row carries
  `identifier_kind` and no `identifier`); `README.md` (AUDIT-054 pointer).
- **behavior:** the PII section.
- **BREAKING:** yes, pre-1.0 minor — `details.email`/`details.identifier` readers (AUDIT-054).
- **escape hatch:** the keyer fallback outside production; revert this PR alone.
- **tests:** invitation and login digests equal `keyer.IdentifierKey(kind, normalized)`; a
  malformed address digests the trimmed raw input and the row carries no address; the openssl
  command in AUDIT-054 reproduces the Go digest (a test shells nothing — it pins the HMAC
  construction the command assumes).
- **verify:** the PR-2a core lines; run-and-look: invite an address in auth-cms →
  `invitation_created` carries `identifier_kind` + `identifier_digest` and no address.
- **closes:** FAF-4, FAC-7 (PII half), M3.
- **tag:** step 2a `pockets/authentication/v0.21.0` after this merges.

### PR-3 — authorization: decision observer, refused writes, correlation, 0005 REQUIRED, 0006 (`authorization/audit-completeness`)

Worktree `../gopernicus-s2-authorization`, stacked on `integrations/audit-integrity` and
`authorization/stores-expand` (the stores).

- **files (core):** `pockets/authorization/logic/decisions/observer.go` (new: `DecisionEvent`
  without `Bound`, `DecisionOutcome`, `DecisionObserver`, `EventAuthorizationDenied`,
  `EventRoleWriteRefused`, `WithDecisionObserver`) + test, `logging.go` (`finish` emits to the
  observer regardless of level for UNBOUND operations only; `decisionLog` always constructed when
  an observer is set), `constructor.go` (field; `config`); `logic/audit/audit.go`
  (`Source.RequestID`, `Source.TraceID`, `MaxCorrelationIDLen`, `Validate` bounds; `NewRecords`
  copies the ids) + test; `inbound/http/roles.go:321-337` (`roleAuditContext` BLANKS then stamps
  the ids; `admitRoleWrite` WARN + observer event, nothing on `ctx.Err()`), `inbound/http/options.go`
  (`WithDecisionObserver`); root `options.go` (`WithDecisionObserver`), `constructor.go:91,159`
  (forward; `decision_observer=` + production WARN); `stores/memory/audit.go` (the ids);
  `README.md` ("Decision observer" under :736 with the host-pocket bridge incl. its backpressure;
  "Production posture" :206 gains the observer WARN, the posture probe and the one-sentence
  contract; "Writes, integrity and audit" :366 documents `Source.RequestID`, the blanking rule,
  the `iam_operations` fenced-only trigger and "`INSERT OR REPLACE` never on an audit table");
  `stores/UPGRADE.md` ("Adopting 0005 — REQUIRED, LOCKSTEP; every instance on v0.19.1 / v0.18.1
  first; roll back only to v0.19.1", the down-SQL, the turso rebuild window, "0006 is the
  immutability trigger — delete it before applying if you decline", the `iam_operations` DELETE
  rule and the S0 revoke to reverse, the no-UPDATE-backfill rule, the paired re-create rule).
- **files (stores):** `stores/pgx/migrations/0006_iam_audit_immutable.sql` (`lock_timeout`;
  `iam_audit_refuse_write()` and `iam_operations_refuse_write()` each with its own dollar tag and
  `SET search_path = pg_catalog, pg_temp`; the fenced-only DELETE branch in the body with `RETURN
  OLD`; `CREATE OR REPLACE TRIGGER`; `ENABLE ALWAYS`); `stores/turso/migrations/0006_iam_audit_immutable.sql`
  (`DROP TRIGGER IF EXISTS`; `iam_audit` pair; `iam_operations_immutable_update` and
  `iam_operations_immutable_delete` with the fenced test in the body); `stores/pgx/schema.go`
  (the 0005 columns move from optional to REQUIRED — nullable `seq`/`recorded_at` via the flag;
  the posture probe in its own transaction after `probeCanonicalSchema` commits, with the
  embedded canonical bodies), `stores/pgx/audit.go:16-60` (`auditColumns` + the two ids; insert;
  `List`), `stores/turso/schema.go` (0005 bodies REQUIRED; `ProbeAuditImmutable` call),
  `stores/turso/audit.go` (same), `stores/turso/conformance_test.go:142-146` (the harness drops
  the rail triggers before `DELETE FROM` and re-executes the embedded 0006 after),
  `stores/pgx/postgres.go` / `stores/turso/turso.go` (`AuditTables()`: `iam_audit`;
  `iam_operations` with `AllowDelete: true`), `stores/pgx/migrations_test.go` (0006 counted; the
  0005 pin test from PR-0 stays), `stores/storetest/audit.go` (ids round-trip under both id
  strategies; a `Source` whose ids are already blank-or-whole passes; `Validate` still refuses an
  over-long id a HOST stamps by hand), live trigger (non-owner refused; owner UPDATE persists;
  SECURITY DEFINER passes; `iam_operations` fenced/non-fenced DELETE), default, pre-migration-NULL
  and fixture-role tests in both stores; `go.mod` → core v0.29.0 + connector v0.11.0/v0.8.0.
- **behavior:** the observer section; every `iam_audit` row written by the bundled role routes
  carries the request id of the HTTP request that caused it (or '' when the header was invalid);
  `cmd/bootstrap` rows carry none.
- **BREAKING:** no exported removal; `Source` gains fields (additive — custom `audit.Reader`
  implementations compile). Stores v0.20.0/v0.19.0 and the database move together (LOCKSTEP; Step
  0 first); production single-role hosts meet the probe (Risk 1).
- **escape hatch:** none needed (observer opt-in); `AppendOnlyWritableAcknowledged` for a
  writable table.
- **tests:** observer receives one event per unbound public operation with the expected outcome
  and bounded fields, is called after the operation returns, is NOT called from inside a snapshot
  callback and NOT for the `*With` family; refused role write → WARN line with the nine fields +
  `OutcomeRefused` event and the HTTP response unchanged; a cancelled context → no event; a
  200-byte `X-Request-ID` on `POST /authorization/roles` → 200 and an `iam_audit` row with
  `request_id=''`; production WARN when no observer; registration log field; storetest over
  memory, pgx, turso. Run-and-look: auth-cms with the bridge wired (`authorization.go:86-95`): a
  cms admin route under `Require(Can(...))` as a non-admin → 403 and one `authorization_denied`
  row carrying the request id; thirty repeats within a minute → one row (dedupe window); a
  `WritePolicy` refusal on `POST /authorization/roles` → 403, the WARN line, one
  `role_write_refused` row.
- **verify:**
  ```
  cd pockets/authorization && go build ./... && go test ./... && go vet ./...
  cd pockets/authorization/stores/pgx && go build ./... && go test ./... && go vet ./...
  cd pockets/authorization/stores/turso && go build ./... && go test ./... && go vet ./...
  make guard
  make check
  make test-stores                                      # authorization pgx bare + schema-qualified, incl. 0005/0006 + trigger + fixture-role cases
  cd pockets/authorization/stores/turso && TURSO_AUTH_TOKEN=local AUTHORIZATION_TURSO_DISPOSABLE_URL=http://127.0.0.1:8081 go test -tags=integration ./... 2>&1 | tee /dev/stderr | grep -q SKIP && exit 1 || true
  ```
- **closes:** FAZ-2, FAZ-4, FAZ-3 (authorization rail), H2 (authorization half).
- **tag:** step 3a `pockets/authorization/v0.29.0`; step 3b `…/stores/pgx/v0.20.0`,
  `…/stores/turso/v0.19.0` after 3a and steps 0–1 resolve. `stores/goredis` untouched.

### PR-4 — secrets: outcome, version-on-read, denied reads, in-transaction `DeleteOwner`, migrations 0002 + 0003 (`secrets/audit-completeness`)

Worktree `../gopernicus-s2-sec`, stacked on `integrations/audit-integrity` (the store).

- **files:** `pockets/secrets/logic/vault/vault.go` (`Outcome`, the constants,
  `AuditRecord.Outcome/RequestID/TraceID`, `(AuditRecord).WithCorrelation(ctx)`,
  `AuditFilter.Outcome`, `Repository.AppendAudit(ctx, []AuditRecord) error` — atomic: one invalid
  record in the batch → `sdk.ErrInvalidInput` and zero rows; `Repository.DeleteOwner(ctx, owner
  Owner, expected []string, audit []AuditRecord) error` — the store runs `DELETE … RETURNING name,
  version, key_id` inside its transaction, returns `sdk.ErrConflict` when the returned name set
  differs from `expected` in either direction (nothing deleted), and OVERWRITES each matching
  `secret_deleted` record's `Version`/`KeyID` from the returned row; `GetSecret(ctx, ref, audit)`
  keeps its signature and still fills a passed record's `Version`), `service.go:83-146` (`Get`:
  ONE row per read, action `secret_read`, appended through `AppendAudit` AFTER the open with its
  true `outcome` — `ok`, or `denied` when `Unwrap` returned `sdk.ErrInvalidInput` (`vault.go:72-75`,
  a retired wrapper key), when the sealed `KeyID` mismatches, or when `Open` fails; an append
  failure fails the read, so no plaintext leaves without its row; a missing owner key stays
  `sdk.ErrNotFound` with no row (`vault.go:100-101`, `GetMissingRecordsNothing`); a transient
  wrapper error records nothing; `GetSecret` is called with no records for reads; `DeleteOwner`
  passes `expected` from its list and retries up to three times on `ErrConflict`; `Delete`
  records `Version`/`KeyID` filled by the store), `logic/rotation/rotation.go:137,195`
  (`Correlate` at its boundary), `stores/memory/memory.go` (all of the above; `appendAudit`
  VALIDATES like pgx — `memory.go:271-276` validates nothing today), `stores/pgx/pgx.go`
  (`Open(ctx, db, opts...)` with the table, `outcome`-column and posture probes; `AppendAudit`;
  `DeleteOwner` RETURNING + conflict; CHECK failures mapped to `sdk.ErrInvalidInput` — `:368`
  wraps the raw driver error today; `AuditTables()`; `StatusCheck` doc "boot-only"),
  `stores/pgx/migrations/0002_secrets_audit_integrity.sql` (`lock_timeout`; no action change;
  `outcome` CHECK `NOT VALID`; `secrets_audit_seq` + `OWNED BY` + `GRANT USAGE … TO PUBLIC`),
  `0003_secrets_audit_immutable.sql` (`lock_timeout`; `secrets_audit_refuse_write()` with `SET
  search_path`, own dollar tag; `CREATE OR REPLACE TRIGGER`; `ENABLE ALWAYS`),
  `stores/storetest/storetest.go` (new cases `AppendAuditAtomic`, `GetRecordsOutcomeAndVersion`,
  `DeleteOwnerConflictsOnShrink`, `DeleteOwnerConflictsOnGrowth`, `DeleteOwnerRecordsReturnedVersion`,
  `CorrelationRoundTrip`); `README.md` ("Audit" rows: outcome, what "denied" means, correlation;
  "Stores" :108: 0002 required, 0003 default-on ("delete it before applying if you decline"),
  `Open`, the host-posture pointer with the sequence grant, PostgreSQL ≥ 14; "the roadmap's
  `secret_read_denied` is the outcome value"); `stores/pgx/README.md`; `Makefile` (`test-stores`
  gains the `pockets/secrets/stores/pgx` leg — it has none today); `go.mod` → core v0.2.0 +
  pgxdb v0.11.0.
- **behavior:** `outcome=denied` means "the vault could not open this secret for this caller" —
  a retired wrapper key, a key-id mismatch, a failed AEAD; it is NOT an authorization verdict (the
  vault has none; the host authorizes before calling). A successful read records the version it
  returned; owner deletion records exactly the rows it removed or conflicts.
- **BREAKING:** yes, pre-1.0 minor — `Repository` gains `AppendAudit` and `DeleteOwner` gains
  `expected` (a compile break for host-implemented repositories; in-repo: memory and pgx only).
  Stores v0.2.0 built through `Open` refuse to boot until 0002 is applied and, in production,
  until the role posture holds. 0002 is NOT metadata-only if a CHECK is validated — hence
  `NOT VALID` (a later `VALIDATE CONSTRAINT` is a host maintenance step).
- **escape hatch:** `AppendOnlyWritableAcknowledged` for a writable table.
- **tests:** the storetest cases over memory and pgx (live); service tests for each outcome
  with a retired ring key, a wrong `KeyID`, a corrupted ciphertext, a missing owner key (no row),
  a transient wrapper error (no row); an append failure fails the read and returns no plaintext;
  `AuditFilter{Outcome: "denied"}` lists only denials; an old-binary-shaped insert (no `outcome`)
  still succeeds (live); `Open` refuses a 0001-only database naming 0002; the 0003 trigger live
  cases (non-owner refused; owner UPDATE persists).
- **verify:**
  ```
  cd pockets/secrets && go build ./... && go test ./... && go vet ./...
  cd pockets/secrets/stores/pgx && go build ./... && go test ./... && go vet ./...
  make guard
  make check
  make test-stores                                      # NEW secrets pgx leg incl. 0002/0003 + trigger + fixture-role cases
  ```
- **closes:** FOP-9, FOP-8, FOP-3 (secrets rail), FAS-6 (secrets rail).
- **tag:** step 4a `pockets/secrets/v0.2.0`; step 4b `…/stores/pgx/v0.2.0` after 4a and step 1.

### PR-5 — workshop: P9, posture surfaces, runner template, scaffold pins, docs (`workshop/audit-integrity`)

Worktree `../gopernicus-s2-ws`. Opens after step 1 is PUSHED and resolves
(`TestWarmScaffoldModuleCache` runs with the proxy on). Model `fable` for the P9 evaluator's
design (module-cache resolution through `go list`), `opus` for the code.

- **files:** `workshop/gopernicus/internal/commands/posture_rules.go` (P9 sentence + evaluator;
  surfaces list + `AppendOnlyWritableAcknowledged`), `guard_test.go` (P9 fixtures: a host whose
  ledger has every store file byte-identical → clean; a missing or edited copy → finding; no
  store module required → `no scope`; the acknowledgement as a non-literal is a P5 finding; as
  `true` prints `ack`; `--list` prints the P9 row), `templates/init/migrations.main.go.tmpl`
  (`-posture-role <name>` preflights the runtime role through `pgxdb.InspectTablePrivileges`
  from the migrator's connection over every mounted pgx store's `AuditTables()`, printing
  `protected_by=` per table; `-status` unchanged), `templates/init/{go.mod,readme.md}.tmpl`
  (pins pgxdb v0.11.0, turso v0.8.0; a "Production posture" paragraph: two roles with the
  sequence grant and the TRIGGER revoke, the refusal and its one-sentence contract, "the
  immutability trigger files are migrations — delete them before applying if you decline",
  `-posture-role` before deploy, archive its output); `scaffold_test.go`; `examples/README.md`
  §3b (the P9 row, "ten lines", the list edit); `Makefile` (G34 `guard-baseline-ledger`;
  guard-count prose), `.github/workflows/check.yml:2` (prose); every example ledger reconciled
  against its store files if P9 finds drift (re-export, file copy only);
  `workshop/documentation/docs/guides/database-posture.md` ("Two roles" :10-28 gains `GRANT
  USAGE ON SEQUENCE`, `ALTER DEFAULT PRIVILEGES … GRANT USAGE ON SEQUENCES`, the TRIGGER revoke,
  keeps DELETE on `iam_operations` (reversing :25), the owner-by-membership / schema-owner /
  function-owner rules, `ALTER SCHEMA public OWNER TO <migrator>`, "create the runtime role with
  SQL, not the console", `pg_restore --disable-triggers` leaves `tgenabled=O`; "Time" :66 cites
  `recorded_at`/`seq` as order evidence; "Migrations" :81 cites `lock_timeout` and the retry),
  `docs/guides/production-posture.md` (the audit completeness rows, control-map row 25's three
  states), `docs/workshop/commands.md` (`-posture-role`, P9); `RELEASING.md` "Tagging scheme"
  (expand-step patches are tagged on `main` after the text is pinned).
- **behavior:** `guard` without `--posture` is byte-for-byte today's output; `--posture` adds P9
  and the new `ack` recognition; G34 fails when a mounted store's migration file has no
  byte-identical copy in the baseline ledger.
- **BREAKING:** no. **escape hatch:** none.
- **tests:** fixtures above; `TestGuardListMatchesContract` over §3 + §3b with the P9 row; a cold
  `init --db pgx` host tidies and builds on the new pins and `guard --posture` is clean (P9 `no
  scope` — the scaffold mounts no store); every example passes P9 under G32.
- **verify:**
  ```
  cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...
  make guard-host-posture
  make guard
  make check
  ```
- **closes:** M1's "visible in CI" half; FAS-7 (docs); the sequence-grant trap (docs half).
- **tag:** step 5 `workshop/gopernicus/v0.6.0`.

### PR-6 — examples: the baseline applies the triggers, the harness proves the recipes (`examples/audit-integrity`)

Worktree `../gopernicus-s2-ex`. Opens after tags 1–3 resolve (the examples' `go.mod` pins move;
no example mounts secrets).

- **files (baseline):** `examples/production-baseline/workshop/migrations/primary/0025_security_events_integrity.sql`,
  `0026_security_events_immutable.sql`, `0027_iam_audit_integrity.sql`,
  `0028_iam_audit_immutable.sql` (exported from the stores, renumbered into the host ledger like
  0021–0023 were, byte-identical — G34/P9; each trigger file after its rail's integrity file);
  `workshop/migrations/main.go` (`-posture-role`); new host pocket
  `pockets/audit/outbound/securityevents/bridge.go` (`New(rec, opts...)`, the semaphore, the
  dedupe window over an in-memory cacher, `Dropped()`; H10 — provider behavior outside `cmd/`;
  an H7 adapter-naming tree, so P0/P1 scan it) + test; `cmd/server/main.go` (the bridge takes
  `authenticationComponents.SecurityEvents`, is passed to `authorization.New` via
  `WithDecisionObserver` AND to the rebuilt adapter via `authorizationhttp.WithDecisionObserver`
  at :288; `authentication.WithAuditWriteObserver` logging at ERROR; `/healthz` detail carries
  `audit_write_failures` and `bridge_dropped`; no acknowledgement — the probe runs inside the
  store constructors; no second recorder anywhere); `cmd/server/production_test.go` (the wiring
  builds in production over the memory stores with the observer present); `README.md` ("Two
  roles": the runner as the owner, the server as `baseline_app` with `SELECT, INSERT` on the
  audit tables, `USAGE` on their sequences, `DELETE` kept on `iam_operations`, created BEFORE
  migrating, and the refusal text it meets otherwise; "Audit completeness": the bridge and its
  backpressure, the trigger files applied, `seq`/`recorded_at` as order evidence,
  `-posture-role` before deploy and its archived output); `.env.example` (the `DB_USER` comment
  naming both roles — P7 unaffected); `go.mod` pins.
- **files (harness):** `examples/auth-cms/cmd/server/authorization.go:86-95` (the same bridge in
  a host pocket `pockets/audit/outbound/securityevents`, over `authzmem` + `authmem`, taking
  `Components.SecurityEvents`); `demo.go` unchanged (the debug dump keeps reading the repository
  until S7's audit-read route); `README.md`; `examples/README.md` §8 (the baseline row: "triggers
  applied, two-role posture documented").
- **BREAKING:** no (examples are never tagged). **escape hatch:** none — zero acknowledgements.
- **tests:** both examples' `go test ./...`; `guard --posture` on the baseline prints no `ack`
  and P9 clean; on auth-cms the `ack`/`rand` lines as before; run-and-look (Verification 1–5).
- **verify:**
  ```
  cd examples/production-baseline && go build ./... && go test ./... && go vet ./...
  cd examples/auth-cms && go build ./... && go test ./... && go vet ./...
  cd workshop/gopernicus && go run . guard --posture ../../examples/production-baseline
  make guard-host-contract
  make guard                                            # G34
  make check
  ```
- **closes:** the reference-host halves of H2, M1, M2; `passes/08` checklist rows for audit
  evidence. **tag:** none.

### PR-7 — release record, handoff, manifest (`docs/s2-release-record`)

- **files:** `RELEASING.md` ("Audit completeness and integrity" section: the fourteen tags with
  Step 0's expand provenance, cold verification incl. the MVS pair, migration notes — 0021/0002
  migrate-then-deploy, 0005 LOCKSTEP after Step 0 with its down-SQL and the turso rebuild window,
  the sequence grant, the default-on trigger files and "delete before applying if you decline",
  AUDIT-054, the `MapError` 42501 behavior change, PostgreSQL ≥ 14, the posture refusal
  production hosts meet and the two-role recipe, O-3's S3 constraints);
  `plans/soc2-s2-audit-completeness-handoff.md` (one section per host — gps-360-go (core v0.26
  today), Segovia, coordination-hub (in both handoffs; its v0.19.0/v0.18.0 pin suggests turso,
  where 0005 rebuilds tables) — each pins **v0.19.1 / v0.18.1 DIRECTLY as part of its still-open
  S1 adoption, skipping v0.19.0**; the dashboards that read `details.email`/`details.identifier`;
  the role the host's server connects as and what the probe will say; `-posture-role` as the
  preflight, its output ARCHIVED as audit evidence; the migration files to export and their
  order; the bridge; **the deploy method per host and gps-360-go's Cloud SQL line are left BLANK
  for the owner (S1's O-4 is still open)**; and these sentences verbatim: **"Rolling deploys: an
  old authorization instance that restarts after 0005 refuses to boot. Use a recreate deploy or
  a maintenance window unless every running instance is already on v0.19.1/v0.18.1."** —
  "migrate 0005 only after EVERY instance is on v0.19.1/v0.18.1; after 0005 roll back only to
  v0.19.1, never v0.19.0" — **"Turso rebuilds run inside the runner's two-minute budget; check
  rail size first."** — "the turso rebuild holds the write lock: schedule a maintenance window"
  — **"Existing rows keep raw `email` and `identifier` until S3 lands. The control-map M3 row
  says 'new rows only'."** — **"Known users are found by `user_id`. The runbook gives a runnable
  digest command for unknown-address lookups, with no Go required."** — "grep your code for
  `DELETE FROM security_events|iam_audit|secrets_audit` and run it as the migrator" — "S0 hosts
  that revoked DELETE on `iam_operations` grant it back before S3" — "the immutability trigger
  files are ordinary migrations: delete them from your ledger before applying if you decline");
  `plans/s2-audit-completeness-release-manifest.json`; `plans/s2-audit-completeness.md` (executed
  copy); this file's status line; `roadmap.md` (S2 rows → done; bullet 6 narrowed to
  `user_directory_read` with `audit_read` moved to S7; the four deviations recorded; O-3c's
  constraints recorded under S3) and `control-map.md` (rows :20, :46, :49, :99–106 → DEFAULT /
  OPT-IN with the new text; row :25 → "DEFAULT (pgx: trigger + probe enforced in production) /
  DEFAULT (turso: trigger; probe WARN-only) / HOST (firestore)"; the M3 row "new rows only").
- **verify:** `make guard` (G20 over the new text). **tag:** none.

### Sequencing

```
PR-0 expand (0005 + tolerant probes) ─ [live legs] ─ [tag 0 on main, after 0005's text is pinned: stores pgx v0.19.1 + turso v0.18.1]
PR-1 connectors ─ [live legs] ─ [tag 1: pgxdb v0.11.0 + turso v0.8.0, pushed, .info polled, MVS pair v0.19.1-on-v0.11.0 cold-built]
   ├─ PR-2a recorder ─ PR-2b denials ─ PR-2c PII flip ─ [live legs] ─ [tag 2a core] ─ [firestore repin PR] ─ [tag 2b stores ×3]
   ├─ PR-3 authorization (on PR-0 + PR-1) ─ [live legs] ─ [tag 3a core] ─ [tag 3b stores ×2]
   ├─ PR-4 secrets        ─ [live legs] ─ [tag 4a core] ─ [tag 4b store]            (parallel worktree)
   └─ PR-5 workshop       ─ [tag 5]                                                 (after tag 1 resolves)
PR-6 examples                                                                       (after tags 1–3 resolve)
PR-7 record
```

Fourteen module tags in seven steps, every one cut at its PR's merge commit on `main`, pushed,
THEN polled on `.info` (never before the push — negative cache), and cold-verified (scratch
consumer, `GOWORK=off`, empty `GOMODCACHE`, `go mod verify`, `go build`). Live store legs run on
merged `main` before every pocket and store tag (`make test-stores` on the scratch Postgres 17 at
127.0.0.1:55433, now including the secrets leg and the fixture-role cases; turso
`-tags=integration` against the local libSQL server at `http://127.0.0.1:8081` with
`TURSO_AUTH_TOKEN=local` and the per-store `*_TURSO_DISPOSABLE_URL`, failing on any SKIP — the
hosted playground group is gone, `404 group not found`). Never `go work sync`. The repo is
public: PR titles and bodies name the symbols and migrations, never the exploit shape beyond what
`review.md` already says in a private plan.

## Release — the tag train

| step | tag(s) | after | repins |
|---|---|---|---|
| 0 | `pockets/authorization/stores/pgx/v0.19.1`, `…/stores/turso/v0.18.1` | PR-0 on `main` + live legs; 0005's text pinned | none (core v0.28.0, pgxdb v0.10.0 / turso v0.7.0 — the published pins) |
| 1 | `integrations/datastores/pgxdb/v0.11.0`, `integrations/datastores/turso/v0.8.0` | PR-1 + live legs; cold-verify incl. stores v0.19.1 on pgxdb v0.11.0 | none |
| 2a | `pockets/authentication/v0.21.0` | PR-2a + PR-2b + PR-2c + live legs | none (sdk stays v0.16.0) |
| 2b | `pockets/authentication/stores/pgx/v0.10.0`, `…/stores/turso/v0.9.0`, `…/stores/firestore/v0.3.0` | 2a + step 1 resolved + `GOWORK=off` tidy; firestore's repin PR merged first | core v0.21.0; pgxdb v0.11.0 / turso v0.8.0 |
| 3a | `pockets/authorization/v0.29.0` | PR-3 + live legs | none |
| 3b | `pockets/authorization/stores/pgx/v0.20.0`, `…/stores/turso/v0.19.0` | 3a + steps 0–1 resolved + tidy | core v0.29.0; pgxdb v0.11.0 / turso v0.8.0 |
| 4a | `pockets/secrets/v0.2.0` | PR-4 | none |
| 4b | `pockets/secrets/stores/pgx/v0.2.0` | 4a + step 1 resolved + tidy | core v0.2.0; pgxdb v0.11.0 |
| 5 | `workshop/gopernicus/v0.6.0` | PR-5 | scaffold pins pgxdb v0.11.0, turso v0.8.0 |

Annotated tags, not signed (owner ruling 2026-10-22). Steps 2–4 are independent of one another
and may land in any order once steps 0–1 resolve. `stores/goredis` is not retagged.

## Risks

1. **Production hosts on one database role refuse to boot after repinning a pgx store**
   (highest, intended, BREAKING at runtime). The scaffold's runner and server read one
   `pgxdb.Config` env, so the server connects as the owner and meets
   `pgxdb.ErrAppendOnlyTableWritable` naming every audit table, `owner=table|schema|function`
   and the fix; a host that already split roles on the S0 recipe meets
   `ErrAppendOnlyTableUnwritable` instead unless it grants sequence USAGE (the migration's
   `GRANT … TO PUBLIC` covers it; the probe refuses before a row is lost). gps-360-go, Segovia
   and coordination-hub repin on their own schedule with the PR-7 handoff and preflight with
   `-posture-role`. Development hosts see the one combined line; hosts on current tags see
   nothing.
2. **Required migrations; authorization in lockstep.** 0021/0005 are metadata-only adds on pgx
   (0002's `NOT VALID` CHECK too; `lock_timeout` aborts a blocked stream and the host retries),
   but the turso files rebuild whole audit tables inside the runner's two-minute budget holding
   the write lock (a maintenance window), and the authorization stores and schema move together
   — Step 0's v0.19.1 / v0.18.1 are the restartable binaries in that window (O-4); a v0.19.0
   instance that restarts after 0005 refuses to boot, and after 0005 the only rollback target is
   v0.19.1. Order everywhere: migrate, then deploy.
3. **Default-on triggers (deviation (a)).** A host that never hardened its roles now carries
   triggers it did not ask for: on pgx its owner-connected server is exempt and its production
   boot refuses on ownership regardless (the control, Risk 1); on turso the triggers simply hold.
   A host-side `DELETE FROM <rail>` written against the app role breaks — the handoff's grep
   line finds it. Deleting the file from the ledger opts out.
4. **The Details flip breaks dashboards** (AUDIT-054, PR-2c alone): `details.email` and
   `details.identifier` disappear from NEW rows at the bump; existing rows keep raw values until
   S3; the correlation recipe needs the host's identifier key.
5. **Triggers versus retention (written constraints for S3, O-3).** The app role cannot DELETE
   on the three rails; S3's in-server purge needs the migrator-owned SECURITY DEFINER function
   with its floor inside and a purge row per run; no later migration may UPDATE-backfill a
   triggered table; a turso rebuild drops the trigger unless its paired re-create ships.
   `iam_operations` keeps non-fenced DELETE so `Expire` stays in the server.
6. **The owner exemption on a waived single-role host** turns a DDL-only bypass into DML
   (`trigger_exempt=true` on the waiver line). The waiver is the host's written exception, not
   a default.
7. **Observer volume.** The bridge drops and counts above its in-flight cap and dedupes repeats
   within its window, so a 403 flood costs bounded inserts; the dropped count is visible in
   `/healthz`. The rail still grows at the (deduped) denial rate until S3 lands retention.
8. **`MapError` 42501.** Every pgxdb consumer now sees `sdk.ErrForbidden` for
   `insufficient_privilege`; a host that matched the raw error changes its check (RELEASING).
9. **Secrets port break.** A host-implemented `vault.Repository` stops compiling at v0.2.0 (none
   exist in this repo beyond memory and pgx).
10. **`external_login` is per cache.** Under the memory default each instance records once per
    token (and once more per eviction); fleet-wide dedupe needs a shared `LoginRecordCache`.
    Documented, not fixed.

## Verification — how the owner proves it end to end

Framework: `make check` green on every PR; `make guard` lists G34; fourteen tags cold-verified
(step 1 also builds stores v0.19.1 on pgxdb v0.11.0); `make test-stores` (now with the secrets
leg and the fixture-role cases) and the turso legs green on merged `main` before each
pocket/store tag, none skipped.

Baseline, from `examples/production-baseline` with a local Postgres 17 (docker on a free port,
never 55432), `.env` copied from `.env.example` with `DB_SSLMODE=disable` uncommented and no mode
line:

1. **Create the runtime role BEFORE migrating** (the order a real host follows): `CREATE ROLE
   baseline_app LOGIN PASSWORD '…'; GRANT USAGE ON SCHEMA public TO baseline_app; ALTER DEFAULT
   PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO baseline_app;`
   — tables only, no sequence grant, exactly the S0 recipe. `make migrate` applies 0025–0028
   (the log lists all four; the trigger files are ordinary migrations); `go run
   ./workshop/migrations -posture-role baseline_app` prints, per table, `insert=true
   sequence_usage=true update=true delete=true trigger=false immutable=true protected_by=trigger`
   (the migration's `GRANT … TO PUBLIC` covered the sequence; UPDATE/DELETE are still granted but
   the verified pair makes `writable=false`). **Before any REVOKE**, `psql -U baseline_app -c
   "UPDATE security_events SET event_status='x'"` → `ERROR: security_events is append-only`
   (42501); `DELETE FROM iam_audit` → the same; `TRUNCATE iam_operations` → refused; `UPDATE
   iam_operations SET outcome='applied'` → refused; `DELETE FROM iam_operations WHERE outcome <>
   'fenced'` → allowed; a fenced row → refused. As the OWNER: `UPDATE security_events SET
   user_agent='' WHERE false` → `UPDATE 0`, and on one real row the change PERSISTS (the `RETURN
   NEW` branch); owner `DELETE … WHERE false` → allowed. Then `REVOKE UPDATE, DELETE, TRUNCATE,
   TRIGGER ON security_events, iam_audit FROM baseline_app; REVOKE UPDATE, TRUNCATE, TRIGGER ON
   iam_operations FROM baseline_app;` — `-posture-role baseline_app` now prints
   `protected_by=privilege`, and the same UPDATE as `baseline_app` → `permission denied`.
2. `make run` (as the owner) boots; the log shows `audit_recording=true`,
   `decision_observer=true`, and ONE development line `append_only=unenforced
   tables=[security_events iam_audit iam_operations]` (`owner=table`).
3. With a dev-issuer bearer token (README recipe) and NO admin role: `POST /authorization/roles`
   → 403; `psql -c "SELECT event_type, event_status, request_id, seq, recorded_at FROM
   security_events ORDER BY seq DESC LIMIT 3"` shows an `authorization_denied failure` row whose
   `request_id` equals the response's `X-Request-ID` header, with `seq` and `recorded_at` set;
   repeat the 403 thirty times within a minute → still one row (dedupe window) and `/healthz`
   detail shows `bridge_dropped=0`. Send the same request with a 200-byte `X-Request-ID` → 403
   and a row with `request_id=''`. Run `make bootstrap` for the subject, repeat → 200 and an
   `iam_audit` row with that request id.
4. Production as the runtime role: `DB_USER=baseline_app APP_RUNTIME_MODE=production go run
   ./cmd/server` with the production keys/URL from the S1 steps → boots (`/healthz` 200, detail
   `audit_write_failures=0`), no WARN; as the owner role → `pgxdb.ErrAppendOnlyTableWritable`
   listing the tables with `owner=table`, zero requests served; as a scratch role with table
   grants and the sequence USAGE revoked → `pgxdb.ErrAppendOnlyTableUnwritable` naming
   `security_events_seq`, not waivable; with `AppendOnlyWritableAcknowledged: true` in a scratch
   copy as the owner → boots with `posture_waiver` lines carrying `trigger_exempt=true` and
   `guard --posture` prints one `ack`; `ALTER TABLE security_events DISABLE TRIGGER
   security_events_immutable` as the owner then boot as `baseline_app` → refusal naming
   `tgenabled=D` with the `ENABLE ALWAYS TRIGGER` fix (restore it after).
5. Harness, `cd examples/auth-cms && AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 make run`:
   `POST /auth/token` with a wrong password → 401; `GET /debug/security-events` as the seeded admin
   → a `token_issued failure reason=invalid_credentials` row with `identifier_digest` and no
   `email`; `GET /auth/admin/users` → a `user_directory_read` row with the admin as `actor_id`;
   invite an address → `invitation_created` carries `identifier_kind` + `identifier_digest` and no
   address, and the AUDIT-054 openssl command over the normalized address reproduces the digest;
   a cms admin route under `Require(Can(...))` as a non-admin → 403 and one `authorization_denied`
   row. Three requests with one external bearer token (the external-identity flow) → exactly one
   `external_login` row.
6. Secrets, from the scratch Postgres with `pockets/secrets/stores/pgx`'s live suite checked out:
   `make test-stores` runs the leg (0002, 0003, the trigger and fixture-role cases); then `psql -c
   "SELECT action, outcome, version, request_id, seq FROM secrets_audit ORDER BY seq DESC LIMIT
   5"` shows a `secret_read outcome=denied` row for the retired-key case beside `secret_read
   outcome=ok version=2`, and no row for the missing-owner case.
7. Cold scaffold: `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.6.0 init --db pgx
   --module example.com/x` → `go mod tidy && go build ./...` green on pgxdb v0.11.0; the README's
   "Production posture" names the two roles, the sequence grant and the trigger files;
   `guard --posture .` clean with P9 `no scope`.

## Decided by the review gate (no ruling needed)

The review gate's resolutions (rev 2) stand unchanged except where the rulings review below
amended O-1…O-4; they are recorded here in short form, attributed by reviewer.

- **Sequence grant trap** (product-manager #1, platform-sre #1, data-integration #1,
  lead-backend #1): keep `seq`; each pgx integrity migration ends with `GRANT USAGE ON SEQUENCE
  <t>_seq TO PUBLIC` (+ `OWNED BY`); the probe also checks INSERT and sequence USAGE and refuses
  in production; the recipe, handoff and Verification 1 show the grant; a fixture-role live case
  covers it. product-manager's "drop `seq`" alternative REJECTED (roadmap scope; identity columns
  stay rejected for the rewrite reason).
- **Probe evidence** (platform-sre #3, data-integration #3, lead-backend #3): ENABLE ALWAYS and
  `tgenabled='A'`, `tgqual IS NULL`, empty `tgattr`, `pg_has_role(…,'MEMBER')`, schema owner and
  function `proowner` count as Owner, one `to_regclass`, probe after the schema transaction
  commits and schema-qualified, exact comparison against the store's embedded canonical body;
  `MapError` 42501 → `sdk.ErrForbidden` in PR-1 as a listed behavior change; turso maps the
  canonical text; `ErrAuditTableWritable` → `ErrAppendOnlyTableWritable` (architecture-steward
  #5); the fix text opens with the trigger files and "create the runtime role with SQL".
- **One recorder** (product-manager #2, architecture-steward #2, platform-sre #5, lead-backend
  #6): `Components.SecurityEvents *securityevent.Recorder`; the bridge accepts the narrow
  `Record` interface; one option name; the recorder wins over `invitations.WithSecurityEvents`;
  no `WithExternalSecurityEventRecorder` on the root path; `WithoutCancel` + 2 s; WARN once then
  per-minute summary with key `audit_write_failed`; the fail-open/closed matrix stated; the
  baseline reports `AuditWriteFailures()` in `/healthz`.
- **Cross-pocket event types** (architecture-steward #1): `decisions.EventAuthorizationDenied`
  / `EventRoleWriteRefused` in `observer.go`; host-bridged types are free TEXT outside `Types()`.
- **Decision observer** (lead-backend #5, platform-sre #6; product-manager #8): no bound-view
  events, `Bound` dropped; no `OutcomeRefused` on `ctx.Err()`; the baseline bridge ships a
  semaphore and a dedupe window; opt-in with the production WARN is DECIDED, not a ruling.
- **Correlation ids** (lead-backend #2, data-integration #11): blanked, never refused, one rule
  for all three rails, 200-byte-header test; the hand-listed empty-id insert columns gain the
  ids; `CorrelationRoundTrip` under both id strategies; rotation stamps through `vault.Correlate`.
- **Privileged reads** (product-manager #4): `user_directory_read` only, actor from
  `sdk.PrincipalFromContext`, signatures unchanged; `audit_read` and `ListSecurityEvents` DEFERRED
  to S7.
- **Secrets** (product-manager #3, lead-backend #7, data-integration #12): ONE `secret_read`
  row per `Get` with its true outcome after the open; `secret_read_denied` becomes the outcome
  value, no action CHECK; missing owner key = no row; only Unwrap's `ErrInvalidInput` is denied;
  `DeleteOwner(ctx, owner, expected, audit)` with `RETURNING name, version, key_id`; `Open(ctx,
  …)` probing constructor; `StatusCheck` boot-only; memory validates; CHECK failures →
  `ErrInvalidInput`; `NOT VALID`; the secrets `test-stores` leg (platform-sre #7); a psql step in
  Verification 6; PR-6 waits on tags 1–3.
- **Turso and migration mechanics** (data-integration #5–8, lead-backend #8): `NULL AS
  recorded_at` with a pre-migration-NULL live test in both dialects; the authorization turso
  canonical probe rewritten (0001 + 0005 bodies, 0004 dropped, no `len != 3`); UNIQUE on `seq`
  refused; `ProbeAuditImmutable` filters on `type='trigger' AND tbl_name=?` and compares
  canonical text; paired re-create files for future rebuilds; fixture roles for production-mode
  live suites (platform-sre #7); turso verify lines fail on SKIP; G34 byte-for-byte
  (architecture-steward #4, platform-sre #7). The inert-file mechanics of rev 2 (`all:` embed,
  inert `-status` listing, inventory-test edits, the `inert` walker) are GONE with O-1.
- **Firestore** (architecture-steward #3, data-integration #9, lead-backend #8): PR-2a maps the
  two ids and carries `authmem`; firestore repins the core and is TAGGED v0.3.0 at step 2b.
- **PR split** (product-manager #5): PR-2a / PR-2b / PR-2c, one core tag after all three;
  `external_login` dedupe in its own bounded store (lead-backend #8); `identifierDigest`'s
  raw-on-error path is never reused by audit.
- **Handoff honesty** (product-manager #9): the four sentences verbatim in PR-7 (the first now
  carries the rulings review's Step 0 clause).
- **Naming**: worktree `../gopernicus-s2-authorization` (lead-backend #8).

### Decided by precedent (rev 1 rulings retired)

- rev 1 O-3 (login `details.email` flip): roadmap S2 bullet 4 ("login Details carry the digest or
  normalized value only") + ruling 3 (the three flips) — ships in PR-2c under AUDIT-054.
- rev 1 O-4 (observer default): the roadmap names no production requirement for the observer —
  opt-in with a WARN.
- rev 1 O-5 (read granularity): moot for `audit_read` (S7); `user_directory_read` per call is an
  engineering choice.
- rev 1 O-6 (version bumps) and O-7 (required column probes): RELEASING "Version bumps" and the
  lookup-paging 0005 precedent (`stores pgx v0.6.0/turso v0.5.0`, migration REQUIRED).
- rev 1 O-9 (the waiver): ruling 2 (acknowledgement flags) + the `TLSDisabledAcknowledged`
  pattern — `AppendOnlyWritableAcknowledged`, for `Writable` only.
- rev 1 O-10 (probe placement): S1's in-constructor refusals — the store constructors probe.

## Rulings — recommended and reviewer-vetted, awaiting the owner's word

Four reviewers attacked O-1…O-4 of rev 2 (verdicts under Notes). Each ruling below is the
RESOLVED text; the alternatives are listed as rejected with the reviewer who argued them.

- **O-1 Immutability triggers ship DEFAULT-ON as ordinary numbered migrations in both dialects**
  (`0022_security_events_immutable.sql`, `0006_iam_audit_immutable.sql`,
  `0003_secrets_audit_immutable.sql`; the same names on turso). A host that declines deletes the
  file from its ledger before applying (handoff). Safe by default: the pgx owner exemption (O-3)
  admits single-role development databases, owner backfills and the superuser live suites
  (`TRUNCATE … CASCADE` as the owner); the turso conformance harnesses drop their rail triggers
  before their `DELETE FROM` reset and re-execute the embedded trigger file after (test-only;
  the turso files begin with `DROP TRIGGER IF EXISTS`, the pgx files use `CREATE OR REPLACE
  FUNCTION`/`TRIGGER` then `ENABLE ALWAYS`, so re-execution is idempotent). Each trigger file
  sorts after its rail's integrity file and after any rebuild of its table; every future turso
  rebuild ships a paired re-create file. Every S2 pgx file starts with `SET LOCAL lock_timeout =
  '5s'` (one-transaction stream; a timeout aborts, the host retries). control-map row 25:
  DEFAULT (pgx) / DEFAULT (turso, probe WARN-only) / HOST (firestore). **This is a deviation
  from the roadmap's "opt-in" wording — deviation (a).** Rejected: the inert `_`-prefixed file
  (rev 2 — the `_` prefix is the host's scratch convention, `ledger.readme.md.tmpl:16`; a
  re-export resurrects it; hosts never rename — product-manager, lead-backend); a `hardening/`
  directory (lead-backend — less visible, the same renumbering; platform-sre, data-integration);
  turso-only inert files (product-manager — one mechanism for both dialects); creating triggers
  at construction (contradicts O-2 — platform-sre).
- **O-2 `ProbeAppendOnly` takes a per-table spec and refuses on the one-sentence contract.**
  `pgxdb.AppendOnlyTable{Name, AllowDelete, Canonical}`; `InspectTablePrivileges(ctx, q, role,
  tables...)`; `(*DB).ProbeAppendOnly(ctx, tables...)`; each store's `AuditTables()
  []pgxdb.AppendOnlyTable` so `-posture-role` and boot decide identically. `Writable()` counts
  UPDATE, DELETE (unless `AllowDelete`), TRUNCATE and TRIGGER; a VERIFIED trigger pair
  (`tgenabled='A'`, no WHEN, no column list, row/statement level as expected, catalog-form
  definition, canonical `prosrc`, `NOT prosecdef`, plpgsql, the table's namespace, `proconfig`
  exactly `{search_path=pg_catalog, pg_temp}`) admits a granted table; owner = table owner by
  `MEMBER`, schema owner, or the trigger function's owner, printed as `owner=table|schema|function`;
  superuser refused; the probe runs in its own transaction after the canonical probe, `SET LOCAL
  search_path TO pg_catalog`, `to_regclass` once, the sequence through `pg_attribute`/`pg_attrdef`;
  `tgenabled=O` (a `pg_restore --disable-triggers` leftover) is named with the `ENABLE ALWAYS`
  fix; development prints one `append_only=unenforced tables=[…]` line; the waiver is
  `pgxdb.Config.AppendOnlyWritableAcknowledged` and its WARN says "runtime role owns <t>:
  immutability triggers do not apply" with `trigger_exempt=true`; `-posture-role` prints
  `protected_by=privilege|trigger|waiver` and the handoff says to archive it. The contract: **in
  production a store refuses to boot when its runtime role is superuser, owns an audit table, can
  UPDATE/DELETE/TRUNCATE/TRIGGER it without the verified triggers, or cannot insert into it.**
  Rejected: strict — any UPDATE/DELETE grant refuses regardless of trigger (cannot be satisfied
  under Neon's `pg_write_all_data` — platform-sre); lenient — only owner/superuser refuses
  (misses grants on tables with no trigger — platform-sre); rev 2's two-variadic API with a
  table-blind canonical body (lead-backend, platform-sre, data-integration — `iam_operations`
  would be refused with or without triggers).
- **O-3 Owner-exempt rail triggers with the `RETURN NEW` fix; `iam_operations` fenced-only; S3
  constraints written now.** (1) The exempt branch is `IF TG_OP = 'UPDATE' THEN RETURN NEW; END
  IF; RETURN OLD;` — plain `RETURN OLD` made an owner's UPDATE a silent no-op (three reviewers);
  a live test proves an owner UPDATE persists. (2) The clause is `pg_catalog.pg_has_role(
  current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER')`
  in a function created `SET search_path = pg_catalog, pg_temp` (the unqualified form is
  shadowable through `pg_temp`); `current_user` refuses a `SET ROLE app` session and exempts a
  SECURITY DEFINER purge. (3) `iam_operations`: UPDATE always refused; DELETE refused only when
  `OLD.outcome = 'fenced'` (`0004_iam_operations_fenced.sql`), the test in the function body and
  the allowed branch `RETURN OLD`; TRUNCATE refused; owner-exempt; turso gets the same two
  triggers unconditionally; nothing UPDATEs or DELETEs the table today (`operations.go:45,78` /
  `:45,75`). **Deviation (d)** from the roadmap's plain "immutability" wording. (4) The
  exemption turns a DDL-only bypass into DML on a waived single-role host — said so, with
  `trigger_exempt=true` on the waiver line. (5) S3 constraints: the SECURITY DEFINER
  `<t>_purge_before(ts)` keeps its retention floor inside the function under owner control,
  never from the caller's argument; every purge appends a purge row (cutoff, count,
  `session_user`); S2 ships the live test that a migrator-owned SECURITY DEFINER function passes
  the exempt branch; turso purge = `DROP TRIGGER`, `DELETE`, re-`CREATE` in one transaction;
  `INSERT OR REPLACE` deletes rows without firing DELETE triggers (documented, never on an audit
  table); the S0 recipe's DELETE revoke on `iam_operations` is reversed in this slice's recipe.
  (6) Turso `MapError` matches `is append-only` with `strings.Contains` (modernc formats
  `constraint failed: <msg> (1811)`). Rejected: full immutability (product-manager withdrew it —
  S3 would need a definer function for a table that is not an audit table); drop `iam_operations`
  from the set (lead-backend; platform-sre's fallback — the roadmap S3 says a fenced row is never
  deleted, so the database should enforce it); unconditional triggers now (lead-backend — S3's
  definer purge only passes through this exemption; adding it later means a second canonical
  body); rev 2's plain "DELETE allowed" shape (product-manager — it let the app identity delete
  fenced rows).
- **O-4 Step 0 is an expand step on `main`: v0.19.1 / v0.18.1 SHIP 0005 and tolerate it; PR-3
  only makes it required.** (1) No maintenance branch: `git diff 10135055 HEAD` is empty for both
  stores, the core since v0.28.0 and pgxdb since v0.10.0, so PR-0 is an ordinary PR under the
  ruleset (`check` + `vuln`), tagged on `main`. (2) PR-0 ships 0005 in both dialects plus the
  tolerant probes — the freeze hazard goes; Step 0 is tagged only after 0005's text is final and
  a test on `main` pins both bodies. (3) The relaxation: pgx `probeCanonicalTable` gains an
  optional-column set per table (four on `iam_audit`, `recorded_at` on `iam_operations`, none on
  `iam_tuples`; type, collation C, nullable); 0005 adds NO constraint; turso v0.18.1 compiles
  0005's `*_next` bodies and accepts 0001-or-0005 / 0004-or-0005 (never "test fixtures only").
  (4) Safe: v0.19.1 / v0.18.1 inserts list their columns, so the new columns take defaults; 0005's
  `GRANT USAGE … TO PUBLIC` is load-bearing for the old binary too; the post-hardening `GRANT …
  TO <runtime>; REVOKE … FROM PUBLIC` is optional and the v0.20.0 probe catches a missed grant.
  (5) Handoff: migrate 0005 only after EVERY instance is on v0.19.1 / v0.18.1; after 0005 roll
  back only to v0.19.1, never v0.19.0; the turso rebuild holds the write lock (a maintenance
  window — Step 0 makes it restartable, not online); the PR-7 sentence ends "unless every running
  instance is already on v0.19.1/v0.18.1"; gps-360-go (core v0.26), Segovia and coordination-hub
  pin v0.19.1 / v0.18.1 DIRECTLY as part of their open S1 adoption, skipping v0.19.0; the deploy
  method per host and gps-360-go's Cloud SQL line stay BLANK for the owner. (6) Step 1's
  cold-verify builds the MVS pair stores v0.19.1 on pgxdb v0.11.0. Rejected: one v0.20.0
  tolerating both schemas (lead-backend — a permanent two-shape path and no rollback target
  below v0.20.0); a maintenance window with down-SQL only (platform-sre, product-manager — no
  binary to roll back to, and the down-SQL destroys audit evidence); rev 2's maintenance branch
  (lead-backend, platform-sre — unnecessary, and no ruleset covers `maint/*`).

### Roadmap deviations the owner is blessing

- (a) Immutability triggers ship DEFAULT-ON as numbered migrations, not opt-in (O-1); a host
  deletes the file to decline.
- (b) `secret_read_denied` is the OUTCOME value of a `secret_read` row, not a new action (review
  gate; keeps 0002 free of an action-CHECK rebuild).
- (c) `audit_read` is DEFERRED to S7 with the audit-read routes; S2 records `user_directory_read`
  only (review gate; no bundled audit-read route exists).
- (d) `iam_operations` is fenced-only: DELETE of non-fenced rows stays allowed so S3's `Expire`
  runs in the server (O-3).

## Recommended reviews

Gate RUN 2026-10-22 (five × RATIFY WITH AMENDMENTS, folded in rev 2); rulings review RUN the same
day (two REJECT-and-reshape on O-1, three ADOPT WITH CHANGE, folded here). A third pass is not
required unless the owner changes O-1…O-4 against the resolutions; if one is wanted, the narrow
targets are: data-integration-reviewer (the final trigger bodies and the `proconfig` equality),
platform-sre (the turso harness trigger reset and the Step 0 tag timing), lead-backend-engineer
(the `AppendOnlyTable` API and the `AuditTables()` shapes), product-manager (the four deviations'
wording in `roadmap.md`).

## Consultation notes

`lead-backend-engineer` read the paragraph sketch before rev 1 (2026-10-22, read-only). Verdict:
ship with edits; nine edits were folded into rev 1 and stand: the authorization lockstep claim;
no identity backfill (nullable `seq`/`recorded_at`, defaults set after the add, `seq` proves
order not completeness); probe hardening; `iam_operations` and S3 (now O-3); the single-role
baseline refusal named BREAKING at runtime; `Seq`/`RecordedAt` off the domain types; the carrier
staying in `authentication` with an injected reader and `ErrorKind` moving; `DeleteOwner`
keeping a RETURNING-based contract (now with `expected`); the secrets outcome model (now one row
with its true outcome). The gate's five reviews and the rulings review's four superseded the
rest; their resolutions are the "Decided by the review gate" list and the "Rulings" section.

## Notes

- **Rulings review record (2026-10-22).** product-manager — O-1 REJECT (ship pgx active, turso
  inert), O-2/O-3/O-4 ADOPT WITH CHANGE; lead-backend-engineer — O-1 REJECT (`hardening/`
  directory), O-2/O-3/O-4 ADOPT WITH CHANGE; platform-sre — all four ADOPT WITH CHANGE;
  data-integration-reviewer — all four ADOPT WITH CHANGE. Reviews at
  `/private/tmp/claude-502/-Users-jrazmi-code-gopernicus-ecosystem-gopernicus/18fb770a-a1cf-406a-abf7-217562a2bd5e/scratchpad/review-s2/rulings/01-product-manager.md`,
  `…/rulings/02-lead-backend-engineer.md`, `…/rulings/03-platform-sre.md`,
  `…/rulings/04-data-integration-reviewer.md`. Rev 2 of this file is
  `s2-audit-completeness.rev2.md`.
- **Review gate record (2026-10-22).** product-manager — RATIFY WITH AMENDMENTS (9 items);
  architecture-steward — RATIFY WITH AMENDMENTS (8 items); platform-sre — RATIFY WITH AMENDMENTS
  (7 items); data-integration-reviewer — RATIFY WITH AMENDMENTS (12 items); lead-backend-engineer
  — RATIFY WITH AMENDMENTS (8 items). Reviews at
  `/private/tmp/claude-502/-Users-jrazmi-code-gopernicus-ecosystem-gopernicus/18fb770a-a1cf-406a-abf7-217562a2bd5e/scratchpad/review-s2/01-product-manager.md`,
  `…/02-architecture-steward.md`, `…/03-platform-sre.md`, `…/04-data-integration-reviewer.md`,
  `…/05-lead-backend-engineer.md`. Rejected there: product-manager's drop-`seq` alternative. Rev
  1 of this file is `s2-audit-completeness.rev1.md`.
- **Roadmap bullets that do not hold as written** (also under Rulings / Decided):
  `audit_read` "on bundled admin routes" — no such route exists in either pocket; deferred to S7
  with the routes. "DB-side `seq`" on SQLite is not evidence (VACUUM renumbers rowids; no identity
  on a non-PK column) — turso rails get `recorded_at` only. "pgx + turso" trigger migrations:
  secrets has no turso store. "`secret_read_denied`" is an outcome value, not an action.
  "Opt-in" triggers are default-on. The authorization 0005 is REQUIRED and LOCKSTEP, not
  additive (hence O-4). Secrets' `AppendAudit` and `DeleteOwner` are port changes, so "additive"
  does not hold for custom repositories. Firestore: no triggers, sequences or in-band roles — host
  IAM is the control, documented only; the module is tagged for the id mapping and the core
  repin.
- The pgxdb `QuerierFrom` fix stays in S5 (roadmap "Cross-plan dependencies").
- `recordLogin`'s `email` key dates from design §5.1 ("the attempted email is an identifier");
  AUDIT-054 records why that reading changed (C1.1).
- Promotion-gate candidate, out of scope: `DecisionEvent.ErrorKind` and `securityevent.ErrorKind`
  make two pockets reduce sdk sentinels the same way — a root `sdk.ErrorKind` for a later pass
  (architecture-steward #6).
- `authentication/stores/pgx/go.mod` pins pgxdb v0.7.1 / core v0.19.1 today; PR-2a's repin is a
  multi-version jump — tidy `GOWORK=off` after tag 1 and expect `go.sum` churn
  (architecture-steward #8).
- The `0017` gap in the authentication migration numbering is historical; 0021 follows 0020.
- Known CI flake unrelated to S2: `integrations/kvstores/goredis` `TestOpenHonorsDeadlineAfterConnection`.

### Owner rulings during the build (2026-10-23)

- **Gate-denial target (PR-6 finding) — "patch it."** Expression evaluations (`Evaluate`,
  `EvaluateResolved`, `EvaluateBatch`) carried only the principal in their `DecisionEvent`, so
  bridged `authorization_denied` rows had empty permission/resource and the dedupe window collapsed a
  principal's gate denials. Fixed as `pockets/authorization` **v0.29.1** (branch
  `authorization/gate-denial-target`): single leaf ⇒ its permission/resource; composite ⇒ the
  first denied leaf, else the first leaf. No store retag. The tag train becomes **14 tags**: the
  firestore tag below is dropped and this patch is added.
- **Firestore store adapters — retired.** The owner retires `pockets/*/stores/firestore` and
  `integrations/datastores/firestore` ("I don't want to support that anymore"); **Firebase Auth as
  an identity provider (`integrations/authentication/firebase`) stays.** `pockets/authentication/
  stores/firestore/v0.3.0` is NOT cut; the firestore store is not repinned. Retirement is its own
  plan (`.claude/plans/retire-firestore-stores.md`, to draft). The pre-existing emulator failure
  `TestConstructorSkipsTheProbeWhenAskedTo` (fails on `main` before S2) is therefore moot.
- **Secrets `DeleteOwner` race — "follow up."** A `PutSecret` committing between the
  `DELETE … RETURNING` and the owner-key delete surfaces as `ErrInvalidReference`, not
  `ErrConflict` (nothing deleted either way). Recorded in the handoff; a later patch locks the
  owner key with `SELECT … FOR UPDATE`.
