# S7 — Authorization governance and invitations (SOC 2 readiness, slice 7)

**Status:** EXECUTED 2026-10-26 (tags: authorization v0.31.0 #209; stores/goredis v0.6.0 #210; authentication v0.30.0 #211; auth-cms `AUTH_DEMO_ADMIN_EMAIL` #212 (example, the owner-drill enabler); every PR passed a lead-backend code review before merge — PR-1 2 HIGH (scheduled-rebuild back-off; storetest unstripped State), PR-2 1 HIGH (header MAC binds the mirror key), PR-3 HIGH-1 owner-ruled (sign-in/recovery identifiers only) + a root-caused test flake (magic-link scraper vs the invitation `#token=`); owner drill PASSED 2026-10-26 — one docs finding: `mine` lists the primary sign-in identifier only, README corrected, merged listing deferred; auth-cms delivery idle interval set to 1 s; record PR-4). RATIFIED 2026-10-26 (owner: "ratified"; R-1 (a) per-fact tags + header MAC + epoch; R-4 (a) JSON only; R-5 (a) README checklist; R-6 (a) auth-cms drill; R-7 (a) `LinksConfig.InvitationURL`; R-8 (a) the current verified holder resolves — **narrowed by owner ruling 2026-10-26 after the PR-3 code review (HIGH-1):** only verified identifiers holding the sign-in/recovery claim (unique across users) resolve, sync, list under `mine` and match at accept; notification-only identifiers never do, and enabling login/recovery on a verified identifier resolves its invitations; passwordless adoption resolves too (lead, MEDIUM-3); R-2/R-3 decided by gate). Host facts: gps-360-go never calls `ResolveInvitations` directly (vendored copy only); its Redis is a dedicated container in dev and `AUTH_CACHE_REDIS_URL` (ACL user, `rediss://`) in production.
Two-reviewer gate RUN 2026-10-25 (lead-backend-engineer BE, lead-frontend-engineer FE); findings folded under
"Decided by gate". **Rev 1 (2026-10-26, owner direction via the lead):** the open-kind binding is REPLACED by identifier
resolution — an invitation is addressed to `(kind, identifier)`; every verification of that identifier on a subject resolves
the pending invitations to it (AutoAccept grants, the rest wait for the token); account merge is a future slice; a narrow
backend re-review (BE-R1–R8) folded. Rulings R-1, R-4–R-8 open (YOUR CALL); R-2, R-3 decided by the gate (BE6, BE11).

- **Size:** M — four PRs, three trains, three tags (authorization core, the goredis store, authentication core); no sdk,
  no pgx/turso store tag, no views tag, no migration. One owner drill gates the record.
- **Anchors.** `main` @ 8c76ebee (S1–S3, S5, S6, S4a, S4b RELEASED; S4c steps 1–4 tagged, PR-2d/2e/record pending):
  sdk v0.20.0; authorization v0.30.0, stores pgx v0.21.0 / turso v0.20.0, **goredis store v0.5.0 (pins core v0.27.0, sdk
  v0.18.0)**; authentication v0.28.0 (S4c PR-2d cuts v0.29.0 first), stores pgx v0.13.0 / turso v0.12.0, views/goth
  v0.8.0; pgxdb v0.14.0, turso v0.9.0, workshop v0.9.0. Sources: `roadmap.md:218-224,349-351`, `passes/05-authorization.md`
  FAZ-6/7/9/10, `passes/02-auth-federation.md` FAF-5/6/13, `control-map.md:33,47-50,72,137`, `s2-audit-completeness.rev2.md:54-58`
  (deviation (c)), `RELEASING.md:1-140`.
- **Owner rulings (binding).** Ruling 3 (2026-10-22): the invitation token moves to the URL fragment under a minor bump with
  a migration note, behind a `LinksConfig` option for one release. Everything that adds behavior is host-opt-in; production
  WARNs over refusals unless argued (S4c). Hosts adopt handoffs once, at the roadmap's end. One store-repin PR per slice;
  live legs never touch ports 55432 or 8082.
- **Already done — cut from this slice (evidence).** The access-review README section exists (S0: `pockets/authorization/
  README.md:1036-1059` "Access review", four recipes incl. the global listing; `operations-runbooks.md:94-95` points at it) —
  S7 only EXTENDS it. The Redis trust-boundary statement exists (S0: `production-posture.md:370-373`; `control-map.md:72`
  stays OPT-IN until the HMAC ships) — S7 adds the hardening recipe and the option. `user_directory_read` shipped (S2,
  `useradmin.go:109`); `audit_read` has no route to ride on in either pocket (S2 deviation (c)) — S7 adds both routes.
  Refused role writes already WARN + `OutcomeRefused` (`roles.go:338-360`), so `RefuseSelfTarget` needs no new rail.
- **What `main` already does.** `tuplecache.Policy{MaxStaleness, PollInterval, PollTimeout, ReadTimeout}` (`runtime.go:25-30`);
  `Rebuild(ctx)` exists, unscheduled (`:160`); the goredis mirror trusts any well-formed hash whose metadata matches
  (`scripts.go` `readScript`; `decodeSet` checks shape and placement only); the delta Lua script re-encodes whole sets INSIDE
  Redis. `audit.Reader.List` exists with no route (`audit.go:171-176`); `MutationRepository.IntegrityPolicy()` and
  `ModelSnapshot.Relations` exist. Invitations: `inviteLink` appends `?token=` (`service.go:1107-1113`); `Accept` matches a
  verified identifier for email/phone only (`:573-581`); `ResolveInvitations(ctx, email, …)` (`:858`) already pages ALL
  pending/accepting invitations for a VERIFIED identifier and accepts the AutoAccept ones — EMAIL ONLY, through
  `ActiveVerifiedIdentifier` (the primary of the kind, `service.go:652`), from registration verify, passwordless, OAuth and
  external (`helpers.go:66-76`), NOT from `ConfirmIdentifierChange` (`identifier_management.go:204-260`); `Mine` is email-keyed
  (`:846`); `identifier.Kind` is CLOSED to email/phone (`identifier.go:33-50`); `directAdd` records no provenance (`:493-524`).
  `UserAdminAction` has five values (`host_policy.go:97-111`); `securityevent.ListFilter{UserID, EventType, EventStatus, Since,
  Until}`. The production-baseline bridges `DecisionEvent`s to the security-event rail from a host pocket.

## Goal
An operator can list who holds what and every change to it over HTTP, each read leaving an `audit_read` row; a Redis
TupleCache compromise degrades to availability loss, not access gain: forged facts are never read, a rolled-back mirror is
ineligible, and a replayed fact lives no longer than the configured full-rebuild cadence; role writes can refuse self-grants
with one helper; an unprotected `owner` relation is loud; an invitation token never reaches a server log or a `Referer`; an
invitation follows its identifier to whichever account VERIFIES it, on every verification path and on demand; a direct add
says who granted it.

## Scope and non-goals
**In scope:** `goredis.WithIntegrityKeys` + `tuplecache.Policy.FullRebuildEvery` + the Redis hardening recipe (FAZ-6);
opt-in `GET /authorization/audit` + `GET /auth/admin/security-events` + the `audit_read` event (FAZ-7, S2 (c)); the
access-review section extended; `RefuseSelfTarget` + `AllOf` `RoleWritePolicy` helpers (FAZ-9); the integrity WARN (FAZ-10);
invitation token to the fragment behind the one-release hatch (FAF-5); identifier resolution for every kind, on every
verification path, plus a sync entry (FAF-6, owner direction); direct-add provenance (FAF-13); examples wired; the record.
**Non-goals:** account merge (two existing accounts; Notes); a merged "mine" listing across identifiers (store change,
deferred); a reverse listing ("who can `manage` R") and an
effective-access listing (FAZ-7 "later"; a follow-on plan); an audit EXPORT endpoint (paging IS the export; the archive
sink shipped S3); a two-person approval ledger (R-3); Redis Cluster; signing the `iam_audit` rows (S2 shipped triggers);
tenant-closure checks for pooled authorities (`roadmap.md:349-351`, the per-tenant plan's finding 7); an HTML arm for the
two audit routes (R-4); a views/goth invitation landing page (R-5); the auth-cms `/debug/security-events` dump (stays, it is
`AUTH_DEBUG` development-only and S4c's verification reads it); `ListSecurityEvents` on the sdk.

## Decided by gate
Reviews 2026-10-25: lead-frontend-engineer FE1–FE12 (one question → R-7), lead-backend-engineer BE (below). Folded unless marked.
- FE1 (HIGH, `Resolve` falls back to `/` → a relative `/#token=` in mail; a HashRouter `…/#/invite` gets a second `#`) → a
  requested `redirect` containing `#` is refused at Create/Resend (400 `invitation_redirect_fragment`) before anything
  persists; the shared allowlist is untouched (my construction-time refusal DROPPED); the `/` fallback → R-7.
- FE2 (HIGH, repo tests parse `[?&]token=`) → every site is a PR-3 step; ONE test stays pinned to the hatch.
- FE3 (HIGH, a new `UserAdminAction` fails OPEN on a check that ignores `Action` — auth-cms's does) → row 7 in bold;
  `user_id` parsed BEFORE `adminPrincipal` and passed as `TargetUserID`; the `host_policy.go:124` doc corrected.
- FE4 (HIGH, `RespondError` maps `sdk.ErrUnavailable` to `evaluation_limit`; `Services` has no reader) → `Services.Audit`,
  filled by `authorization.New` AND auth-cms's hand-built adapter; a gate without a reader fails `New`; errors through
  `web.RespondJSONDomainError`; `Audit()` accessor. "Fail without an observer" NOT taken: a nil observer WARNs (the
  `WithDecisionObserver` precedent; owner: WARNs over refusals).
- FE5 (MED) → CURSOR ONLY on both routes (`offset` → named 400); `count` kept (README: a full scan unfiltered); `q`
  rejected by name on both; README: drop the cursor when a filter changes. FE6 (MED) → `since`/`until` parsed in the
  handler; `parseOptionalTime` (`machine.go:301`) gains a field-name parameter; `since >= until` → 400; inclusive/exclusive;
  UTC RFC 3339 nanos on both routes.
- FE7 (MED, invitation errors come out generic) → `writeInvitationError` after `writeIdentifierError` (`identifiers.go:218`):
  `invitation_redirect_fragment` 400, `invitation_identity_mismatch` 403 (renames the email/phone mismatch code, row 10).
- FE8 (MED) → the landing checklist covers the login detour (token in `sessionStorage`, never in `return_to`/query; the
  fragment scrubbed before any redirect or third-party script). No goth invitation page exists (`assets/fragment.js` serves
  reset/magic/pending-link only) → no views tag. FE9 (MED) → the security-events route mounts only with
  `Repositories.SecurityEvents` wired; the authorization audit handler sets `Cache-Control: no-store` explicitly.
- FE10 (LOW) → `RoleRoutes.AuditGate web.Middleware` REPLACES the `AuditRead bool` (nil disables; a stricter MFA gate is
  the host's choice). FE11 (LOW, retire auth-cms `/debug/security-events`) → NOT this slice (S4c's open verification reads
  it); auth-cms mounts both new routes + a README curl leg. FE12 (LOW) → decline stays token-only; a leaked link lets anyone
  decline — an accepted residual (Risks).
- BE1 (HIGH, a Lua identity that includes the tag lets a remove with a differing tag no-op, so the revoked grant stays) →
  identity over `t[1..7]`; an add replaces the tag; `deltaScript` takes the epoch as an ARGV and conflicts on mismatch.
- BE2 (HIGH, `!epoch`/`!receipt`/`!until` come from Redis: a DUMP/RESTORE of an older mirror passes every tag, a rewritten
  `!until` keeps it eligible) → a Go-computed HEADER MAC over `binding, receipt, epoch, epoch_at, until` stored as `!mac`
  (Lua never sees the key); `readScript` returns the header with the sets and `Read` verifies it. BE question: YES, the
  header MAC enters the wire format — rollback bounded by `MaxStaleness`, replay within an epoch by `FullRebuildEvery` (the
  residual). Tags need not cover set completeness (no exclusion/intersection in the model: a deleted fact only denies).
- BE3 (MED) → `fullSets` overhead 25 + tag; Lua `#t == 8` with a 22-char `encoded(t[8], true)`; `decodeSet` exact 8/7;
  README: ~50 B per fact. BE4 (MED, a fingerprint suffix inside the braces collides and two mirrors under one receipt
  rebuild each other every poll) → constant prefix `tuplecache:v3:ik:{…}`; `WithIntegrityKeys(current, previous...)`, ≥ 32 B.
- BE5 (MED, a per-process `lastFull` rebuilds on every replica at boot) → the cadence reads the mirror's `!epoch_at` through
  a new `tuplecache.State.FullAt` (zero → the per-process clock); one rebuild per period fleet-wide; validated against the
  DEFAULTED `PollInterval`. "`MaxStaleness ≥ PollTimeout`" NOT taken (the 5 s / 30 s defaults would refuse).
- BE6 (MED) → the typed observer (= R-2, decided); the bridge method has NO dedupe, counts drops; `securityevent.TypeAuditRead`
  owns the string; `audit.Reader`'s "no audit HTTP route" doc corrected. BE7 (MED, "allow if admin" checks admit the new action
  on upgrade) → EXPLICIT opt-in `AdministrationConfig.SecurityEventsRoute` (the `UserErasureRoute` precedent) requiring
  `UserAdminCheck` + `Repositories.SecurityEvents`; `q`/`order`/`offset` → named 400s; `audit_read` recorded in the service.
- BE8 (LOW-MED, "zero rules" misses a policy protecting `admin` but not `owner`, and `Mutations == nil`) → one WARN per type
  with `owner` and `!Protects(T, "owner")`; a separate WARN when `Mutations` is nil beside `owner`.
- BE9 (HIGH, an inviter-named subject makes AutoAccept an inviter-only grant) → SUPERSEDED by rev 1 (§5): a subject is bound
  only through a VERIFIED identifier; AutoAccept unchanged. BE10 (MED) → `Render` already receives `Secret` (own-link
  templates unaffected, README); the hatch reaches the service through `invitations.WithLinkTokenInQuery`.
- BE11 (LOW) → `RefuseSelfTarget` compares `Subject.Type/ID` to `Principal.Type/ID`, `OpRoleAssign` only; README names the
  group limit; `AllOf` refuses nil. Distinct approver = recipe (R-3, decided). BE12 (LOW) → `State.FullAt` is a core change
  the goredis backend fills → goredis repins core v0.31.0 and tags after it; the baseline bridge needs `TypeAuditRead` from
  the NEW authentication core → ALL example wiring moves to PR-3. BE13 (gap) → deferred (Notes): no actor filter on
  `securityevent.ListFilter`; a merged "mine" needs `ListBySubjects` — both pgx/turso store changes.
- **Rev 1 re-review (lead-backend-engineer 2026-10-26, BE-R1–R8; one question → R-8).** R1 (HIGH, an unbudgeted sync
  re-drives stuck `accepting` grants into the host Granter) → the HTTP sync walks `pending` rows only, under a per-user
  budget (`invitation_sync:` 10/min) and an identifier cap; `accepting` rows re-drive on verification only. R2 (MED, a token
  `Accept` racing a sync for the SAME subject both `Grant` under one `OperationID`) → no code change (host dedupe + the
  `member_added:<id>` key); only the caller whose `CompleteAcceptance` moved the row records and counts; README. R3 (MED,
  the `invitationResolver` port is pinned narrow) → `SyncInvitations` on `authlogic.Service` over `projectAddresses`; an
  exported `VerifiedIdentifiers` accessor feeds the closure; no outside caller (grep), still a minor bump + note. R4 (MED,
  no passwordless PHONE provisioning path exists) → `passwordless.go:377` passes `res.Identifier.Kind`; the claim DROPPED.
  R5 (MED) → `ConfirmIdentifierChange` resolves LAST, after the notices, only on a nil apply. R6 (LOW) → a `Normalize`
  idempotence assertion. R7 (LOW) → `(Kind, Value)` matching; `Mine(kind, …)`; `identifier_kind` ∈ {email, phone} else 400.
  R8 (LOW) → `resolved_by` = "completed by"; `direct_add` wired on its own path.

## Design
### 1. Redis integrity: `goredis.WithIntegrityKeys` (goredis store v0.6.0; FAZ-6)
The only place a signature can be checked is the Go read side, and the only unit Lua moves without decoding is the fact:
the tag is **per fact**, the header is MAC'd in Go, and Lua never holds a key.
- `WithIntegrityKeys(current []byte, previous ...[]byte) Option` (BE4) — each ≥ 32 bytes (`sdk.ErrInvalidInput`); the host
  decodes hex (`AUTHZ_CACHE_INTEGRITY_KEYS`, `openssl rand -hex 32`) and rotates by prepending: sign with `current`, verify
  against any key in the ring, drop the old key one `WithExpiry` later.
- **Fact tags.** A keyed mirror's wire tuple has 8 elements; the 8th is `base64url(HMAC-SHA256(key, "gopernicus.tuplecache.
  fact.v1" ‖ binding ‖ epoch ‖ f1…f7, length-prefixed)[:16])` (22 chars), written by `toWire`/`fullSets` (overhead 25 + tag,
  BE3)/`changesWire`; Lua `valid_tuple` requires `#t == 8` with a 22-char `encoded(t[8], true)` (7 on an unkeyed mirror);
  **identity is `cjson.encode({t[1..7]})`** (BE1): an add replaces the stored tag, a remove matches regardless of tag.
- **Header MAC and epoch (BE2, BE5).** Every FULL publication mints `epoch` (16 random bytes, hex) and `epoch_at` (Redis
  `TIME`, ms); every publication writes `!mac = HMAC-SHA256(key, "gopernicus.tuplecache.header.v1" ‖ binding ‖ receipt ‖ epoch
  ‖ epoch_at ‖ until)` beside `!epoch`, `!epoch_at`, `!until`; `deltaScript` conflicts when its epoch ARGV differs from
  `!epoch`. `state()`/`readScript` return the header with the sets; `Read` verifies the header, then every tag against the
  returned epoch. A restored older mirror carries an old `until` → ineligible (rollback ≤ `MaxStaleness`); a replayed
  revoked fact with a valid current-epoch tag lives until the next full publication (§2). `State.FullAt` = `epoch_at`.
- **Failure and separation.** Any header or tag failure → `tuplecache.ErrUnavailable` → the durable fallback
  (`runtime.go:306-322`); `goredis.(*TupleCache).Stats()` counts `IntegrityFailures`. Keyed mirrors live under
  `tuplecache:v3:ik:{<ns/binding>}` (BE4: outside the braces, one mirror per binding), so keyed and unkeyed runtimes never
  read each other's mirror; turning the key on or off rebuilds in full on the first poll; `!schema` stays `2`.
- **Residuals (named).** Deletion is a denial; replay within an epoch (§2); the key sits in the host process beside the DB
  credential (a Redis-only compromise is the threat); ~50 B per fact, so mirrors near the limits fall back more.
- **Docs.** `stores/goredis/README.md` "Trust boundary and hardening": Redis is inside the authorization boundary — dedicated
  instance or logical DB, `rediss://` with a verified CA, an ACL user limited to the mirror key patterns + `TIME` + `EVAL`/
  `EVALSHA`, no lower-trust co-tenants, `maxmemory-policy`; the key recipe, rotation and the forgery drill. `production-
  posture.md:370-373` and the pocket README link to it. `control-map.md:72` → OPT-IN (hardened).

### 2. `tuplecache.Policy.FullRebuildEvery` (authorization v0.31.0)
`Policy.FullRebuildEvery time.Duration`: zero = never (today); negative or below the DEFAULTED `PollInterval` →
`sdk.ErrInvalidInput`; NOT part of the binding. `tuplecache.State` gains `FullAt time.Time` (the mirror's last full
publication; zero when the backend does not report it). After the `ErrNoWork` gate, `poll` promotes itself to `rebuild=true`
when `now - before.FullAt ≥ FullRebuildEvery` (BE5: one rebuild per period fleet-wide, from the mirror's clock), falling back
to a per-process `lastFull` on a backend without `FullAt` (documented as weaker). `Stats` gains `LastFullRebuild` and
`ScheduledRebuilds`; the memory backend fills `FullAt`. README: pair the keys with `FullRebuildEvery` (1 h suggested) on
mirrors that rebuild well under `MaxStaleness`; a rebuild longer than `MaxStaleness` still cannot publish.

### 3. The audit-read routes and `audit_read` (FAZ-7; S2 deviation (c))
**Authorization** (`inbound/http`, v0.31.0). `RoleRoutes.AuditGate web.Middleware` (FE10) mounts `GET /authorization/audit`
behind it — nil disables; a host passes its role gate or a stricter one (MFA composed in). `Services.Audit audit.Reader`
(FE4) is required beside a gate (`ErrAuditGateWithoutReader` from `New`); `authorization.New` fills it from `repos.Audit`,
auth-cms's hand-built adapter from `components.Audit`. Query: the three OPTIONAL exact pairs of `audit.Filter`
(`resource_type`+`resource_id`, `subject_type`+`subject_id`, `actor_type`+`actor_id`; one half alone → 400) plus `list`
params: CURSOR ONLY (`offset` → 400 `offset_not_supported`; FE5), `limit` default 25 max 100, `count` admitted,
`order=occurred_at` only, `q` → 400 naming the audit listing. Response = `pageResponse[auditRecordResponse]`: `{id,
event_id, occurred_at (UTC RFC 3339 nanos), source:{actor_type, actor_id, system, reason, request_id, trace_id},
change:{action, tuple:{scope:{kind, resource_type, resource_id}, relation, subject:{type, id, relation}}}}` (omitempty on
empty strings); `Cache-Control: no-store` (FE9). Reader errors through `web.RespondJSONDomainError` (`unavailable` → 503,
never `evaluation_limit`; FE4). `Audit() http.Handler` accessor beside `RolesBySubject()`.
**Recording.** The pocket has no event rail: each completed read emits `authorizationhttp.AuditReadEvent{Principal, Filter
audit.Filter, Limit, Returned int}` to `WithAuditReadObserver(func(context.Context, AuditReadEvent))`; nil beside a gate →
ONE WARN at construction in every mode, no refusal. The baseline bridge gains `AuditReads()` recording `audit_read` through
the one recorder (rail `authorization`); auth-cms bridges into `svc.SecurityEvents`. `Register` logs `audit_read_route`.
**Authentication** (`inbound/http`, next minor). `GET /auth/admin/security-events` joins `mountUserAdmin` ONLY when
`Repositories.SecurityEvents` is wired (FE9), under the `UserAdministration` gate; `user_id` is parsed first and passed as
`TargetUserID` to `adminPrincipal(UserAdminReadAudit)` (new `UserAdminAction = "read-audit"`; FE3) so a host policy can
confine or scope it; `admins` MFA applies as on every admin route. Query: `user_id`, `event_type`, `event_status`, `since`,
`until` (RFC 3339, since inclusive, until exclusive, `since >= until` → 400; parsed in the handler through a field-named
`parseOptionalTime`; FE6) → `securityevent.ListFilter`; list params CURSOR only (`offset`/`q` → named 400s), `count`
admitted. Items: `{id, user_id, actor_type, actor_id, event_type, event_status, ip_address, user_agent, request_id,
trace_id, details, created_at (UTC RFC 3339 nanos)}`. One row per call:

| Type | Status | UserID | Actor | Details |
|---|---|---|---|---|
| `audit_read` | success | — | the admin | `rail` (`security_events`/`authorization`), `filters` (the keys present, never values), `limit`, `returned`, `strategy` |

`TypeAuditRead` joins `Types()` (the registration test); a denied call records nothing new (the gate's 401/403 and
`mfa_required_denied` cover it). **Access review** (README): the HTTP recipes for both routes, paging-as-export (cursor until
`has_more=false`, 100 per page), the `audit_read` evidence line, the unchanged limits paragraph; `operations-runbooks.md`
"Export audit" names the routes.

### 4. `RoleWritePolicy` helpers (FAZ-9) and the integrity WARN (FAZ-10)
```go
func RefuseSelfTarget(_ context.Context, r RoleWriteRequest) error // OpRoleAssign with r.Subject.Type/ID == r.Principal.Type/ID → ErrSelfTargetRefused (sdk.ErrForbidden); unassign passes
func AllOf(policies ...RoleWritePolicy) RoleWritePolicy          // first error wins; zero policies or a nil entry → panic at construction (a wiring error)
```
The refusal rides `roleWriteRefused` unchanged (WARN + `OutcomeRefused`, `error_kind=forbidden`). README: the group limit
(BE11 — a grant to `group:X` while the actor is a member passes); a "distinct approver" is a host workflow (an invitation or
an approval record whose `operation_id` the Granter fences), shown as `AllOf(RefuseSelfTarget, hostPolicy)`. **Integrity
WARN (BE8):** `authorization.New` walks `CompiledModel().Snapshot()`: every type `T` with `owner` in `Relations(T)` and
`!repos.Mutations.IntegrityPolicy().Protects(T, "owner")` → ONE WARN listing the types and naming
`mutations.DefaultIntegrityPolicy()`; `Mutations` nil beside any `owner` → one WARN (the baseline `RelationshipWriter` can
strip a last owner). Production AND development; zero mode silent; no refusal, no acknowledgement (an availability incident,
not an access gain). `Register` logs `integrity_rules=<n>`.

### 5. Invitations (authentication next minor; FAF-5, FAF-6, FAF-13)
- **Token to the fragment (FAF-5; ruling 3).** `inviteLink(dest, secret)` → `dest + "#token=" + secret`. Hatch
  `LinksConfig.InvitationTokenInQuery bool` (`env:"AUTH_INVITATION_TOKEN_IN_QUERY"`; the constructor maps it to
  `invitations.WithLinkTokenInQuery`, BE10) restores `?token=` for ONE release: production logs one `posture_waiver=
  InvitationTokenInQuery` WARN; the next authentication minor removes the field (a compile break announced now). A requested
  `redirect` containing `#` is refused at Create/Resend with 400 `invitation_redirect_fragment` before anything persists
  (FE1); the shared `RedirectAllowlist` is untouched. Host templates that build their own link from `Secret` are unaffected
  (BE10, README). The default destination when no `redirect` is supplied → R-7. The README invitations section gains "The
  landing page's responsibilities" mirroring `README.md:3245`: read `location.hash`, `history.replaceState` before ANY network
  call or third-party script, hold the token in `sessionStorage` across the login detour (accept is session-gated; never in
  `return_to` or a query — FE8), POST `/auth/invitations/accept` or `/{id}/decline`, `Referrer-Policy: no-referrer`,
  `Cache-Control: no-store`; plus the one-release note.
- **Identifier resolution (FAF-6; owner direction, rev 1).** The owner's model: an invitation is addressed to `(kind,
  identifier)`; when a subject VERIFIES that identifier, every pending invitation addressed to it resolves to that subject —
  AutoAccept ones grant, the others stay pending until the token link is accepted (no new state; `ResolvedSubjectID` is set
  only where acceptance sets it today). Generalize what exists instead of adding a binding:
  - `ResolveInvitations(ctx, kind, value, subjectType, subjectID string) (int, error)` — the `kind` parameter is NEW (a
    signature change on the exported method and on `authlogic`'s `invitationResolver` port, `service.go:161`); kind-aware
    normalization (the same normalizer `createPending` stored with); the identifier must be VERIFIED on the subject, else
    `0, nil`; pages `ListBySubject(kind, normalized)` (already kind-generic, `repository.go:81`) and `acceptClaim`s the
    AutoAccept pending/accepting unexpired rows exactly as today.
  - `AccessConfig.VerifiedIdentifiers func(ctx, userID) ([]sdk.IdentityAddress, error)` REPLACES `CallerIdentifiers`
    (both internal, wired by `auth.New` from a new exported `authService.VerifiedIdentifiers`, BE-R3): `requireVerified
    Identifier` becomes a `(Kind, Value)` membership check (BE-R7) over ALL the subject's active verified identifiers, so a
    second, non-primary verified email now matches at `Accept` and at resolution (today `ActiveVerifiedIdentifier` returns
    the primary of the kind only). Nil → fail closed. `Normalize` must be idempotent (BE-R6, a conformance assertion).
  - `authlogic.Service.SyncInvitations(ctx, userID) (int, error)` (BE-R3: NOT on the narrow `invitationResolver` port):
    walks `projectAddresses` and calls `ResolveInvitations(kind, value, …)` per address with `pendingOnly` (BE-R1: the
    on-demand path never re-drives `accepting` rows into the host Granter); idempotent. Exposed as `POST /auth/invitations/
    sync` (the `invitations` middleware + browserSafe; `{resolved}`; budget `invitation_sync:` 10/min per user through
    `allowAuthenticationRequest`; ≤ 8 identifiers walked). A token `Accept` racing a sync for the same subject may both
    `Grant` under one `OperationID` — safe by the host's dedupe contract and the `member_added:<id>` key; only the caller whose
    `CompleteAcceptance` moved the row records and counts (BE-R2; README).
  - **Call sites:** registration verify, OAuth and external pass `AddressKindEmail`; the passwordless provision-on-redeem
    site (`passwordless.go:377`) passes `res.Identifier.Kind` (BE-R4: no phone-provisioning path exists; the hardcoded kind
    at `:371` goes too); `ConfirmIdentifierChange` calls it LAST — after `enqueueIdentifierChangeNotices`, only on a nil
    apply, with `(pending.Kind, pending.NewValue)` (BE-R5) — best-effort, logged, never failing the flow (the
    `resolvePendingInvitations` shape, `helpers.go:66-76`).
  - **Open kinds:** `identifier.Kind` is closed to email/phone, so no verified identifier of an open kind exists today → an
    open-kind invitation never resolves and keeps the token-accept path UNCHANGED (accept with the token; a named residual
    the fragment flip shrinks). A host that verifies identifiers of another kind resolves them the day the rail admits it.
  - **`mine`:** `GET /auth/invitations/mine?identifier_kind=email|phone` (default `email`, unchanged; anything else 400)
    lists by ONE of the caller's verified identifiers of that kind (`Mine(ctx, kind, value, req)`); merged listing deferred.
  - **Cross-identifier case (the owner's):** invited by email, signs up by phone, adds + verifies the email → the AutoAccept
    invitation grants on the confirm; the non-AutoAccept one appears under `?identifier_kind=email`. An invitee who ends
    up with TWO accounts needs account merge — out of S7 (Notes).
- **Direct-add provenance (FAF-13).** `invitation_granted` Details gain `invited_by`, `operation_id`, `direct_add: true` on
  the direct-add path; `invitation_id` + `operation_id` on the accept paths; and `resolved_by` ∈ {`accept`, `verification`,
  `sync`, `direct_add`} = the path that COMPLETED the claim (BE-R8, threaded through `acceptClaim`). Bounded ids, no PII.
- **Errors (FE7).** `writeInvitationError` after `writeIdentifierError` (`identifiers.go:218`) owns the two new codes.

## Upgrade notes
> **Read first:** `grep -rnE 'tuplecache\.Policy\{|goredis\.NewTupleCache|RoleRoutes\{|InvitationsConfig\{|LinksConfig\{'
> --include='*.go'`. **By host shape:** goredis TupleCache hosts (gps-360-go) → rows 1–3; every authorization host → rows
> 4–6; every invitation host → rows 8–10 (row 8 is the break); IdP-only hosts without invitations → row 7 only.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `WithIntegrityKeys`; the `tuplecache:v3:ik:{…}` prefix; header MAC + fact tags; `Stats()` | opt-in; turning it on rebuilds the mirror once; ~50 B per fact | leave unset | goredis store@v0.6.0 |
| 2 | goredis store repinned to core v0.31.0 / sdk v0.20.0 (`State.FullAt`) | MVS lifts a goredis host from core v0.27.0 to v0.31.0 | none | goredis store@v0.6.0 |
| 3 | `Policy.FullRebuildEvery`; `State.FullAt`; `Stats.LastFullRebuild` | additive; zero = never; a custom `Backend` may leave `FullAt` zero | leave zero | authorization@v0.31.0 |
| 4 | `RoleRoutes.AuditGate`, `Services.Audit`, `WithAuditReadObserver`, `GET /authorization/audit` | opt-in; a gate without a reader fails `New`; a gate without an observer WARNs | leave nil | authorization@v0.31.0 |
| 5 | `RefuseSelfTarget`, `AllOf` | additive | none | authorization@v0.31.0 |
| 6 | integrity WARN (`owner` unprotected, or `Mutations` nil beside `owner`) | one WARN line in production and development | `WithIntegrityPolicy(DefaultIntegrityPolicy())` | authorization@v0.31.0 |
| 7 | `AdministrationConfig.SecurityEventsRoute` → `GET /auth/admin/security-events`, `UserAdminReadAudit`, `audit_read` | opt-in; **a `UserAdminCheck` that does not switch on `Action` admits the new action** (auth-cms's does) — scope it | leave false | authentication@next |
| 8 | **BREAKING:** invitation token in the URL fragment; a `#` in a requested `redirect` → 400 | a landing page reading `?token=` stops accepting; templates building their own link from `Secret` are unaffected | `LinksConfig.InvitationTokenInQuery` (one release; production WARN) | authentication@next |
| 9 | identifier resolution for every kind on every verification path incl. `ConfirmIdentifierChange`; `SyncInvitations` + `POST /auth/invitations/sync`; `mine?identifier_kind=`; **`ResolveInvitations` gains a `kind` parameter** (compile-adjacent: a host calling it or implementing `invitationResolver`); `VerifiedIdentifiers` replaces `CallerIdentifiers` in `invitations.AccessConfig` | adding a verified identifier now grants its AutoAccept invitations; a second verified email matches at accept | none (behavior); fix the call | authentication@next |
| 10 | error codes `invitation_identity_mismatch` (replaces `forbidden` for the email/phone mismatch) and `invitation_redirect_fragment`; `invitation_granted` Details gain `invited_by`/`operation_id`/`direct_add`/`invitation_id`/`resolved_by` | a client switching on `forbidden` for the mismatch sees a new code | none | authentication@next |
| 11 | baseline + auth-cms wire both routes, `AllOf(RefuseSelfTarget, …)`, the bridge method | example behaviour | — | examples (untagged) |

Row 8 is the one behavior break; row 9 is a compile-adjacent signature change; row 10 changes one error code; row 7 is a
named opt-in. Every module ships MINOR under v0; tag messages name rows 2, 7, 8, 9 and 10. **If R-7 = (a):** row 8 also gains `LinksConfig.InvitationURL`
(production-required beside a Granter; `ErrInvitationURLRequired`), PR-3 adds it to `config.go`/`security.go`/both examples'
env and `.env.example`, and `guard --posture` is NOT extended (no new P-rule; the constructor refusal is the check).

## Risks
(1) Tags and the header MAC protect provenance and freshness, not presence: a Redis writer can still deny (delete) and replay
a revoked fact within the current epoch until the next full rebuild — stated residuals; the key lives in the host process.
(2) The fragment flip breaks every host landing page reading `?token=` the day it repins; the hatch and the maintenance rule
(hosts adopt once, at the end) make it a planned cut-over. (3) Resolution widens what grants: adding a verified identifier to
an existing account grants that address's AutoAccept invitations at once (the invitee proved the address; the inviter chose
AutoAccept) — row 9; open kinds still accept by token alone until the identifier rail admits their kind; R-8 decides the
"previous holder" case. (4) The goredis store jumps core v0.27.0 → v0.31.0 in one repin (row 2); set B proves it. (5) The
audit routes expose actor and subject ids to anyone the host gate admits, and a `UserAdminCheck` that ignores `Action`
admits the new read — the gate and the check are the controls (README, row 7). (6) Decline stays token-only (FE12, accepted).

## Tasks as PRs
Worktrees `../gopernicus-s7-<pr>`; one merge/tag/push per command; every PR runs with model **opus**. Live legs: a scratch
`redis-server` on a private Unix socket only (the goredis tests spawn their own); no PostgreSQL or Turso leg (no store or
schema change).

- **PR-1 — authorization core (`authorization/governance-s7`).** **Depends on** nothing. **BREAKING:** none (rows 3–6).
  **Tag:** `pockets/authorization/v0.31.0`. **Files:** `pockets/authorization/logic/tuplecache/{tuplecache.go (State.FullAt),
  runtime.go (FullRebuildEvery, cadence, Stats), runtime_test.go}`; `pockets/authorization/stores/memory/` (fills `FullAt`);
  `pockets/authorization/logic/audit/audit.go` (the `Reader` doc); `pockets/authorization/inbound/http/{routes.go (AuditGate,
  Services.Audit, ErrAuditGateWithoutReader, Audit()), audit.go (new: handler, DTOs, AuditReadEvent, no-store),
  policy_helpers.go (new), options.go (WithAuditReadObserver), errors.go, audit_test.go, policy_helpers_test.go}`;
  `pockets/authorization/{config.go, options.go, constructor.go (Services.Audit, the WARNs, Register fields),
  constructor_test.go}`; `pockets/authorization/README.md` (role administration + `AuditGate`; "Access review" extended;
  the WARNs; `FullRebuildEvery` + a pointer to the goredis trust-boundary section). NO example wiring here (BE12).
  **Verify:** `cd pockets/authorization && go build ./... && go test -race ./... && go vet ./...`; `make guard`; `make check`.
- **PR-2 — goredis store: keys, header MAC, fact tags, the hardening doc (`authorization/goredis-integrity`).** **Depends
  on** tag 1 (the slice's one store repin rides this PR: core → v0.31.0, sdk → v0.20.0 by explicit `GOWORK=off go get`, then
  `GOWORK=off go mod tidy`). **BREAKING:** none (rows 1–2). **Tag:** `pockets/authorization/stores/goredis/v0.6.0`.
  **Files:** `pockets/authorization/stores/goredis/{tuple_cache.go (WithIntegrityKeys, the ik prefix, tagging, header MAC,
  Read verification, State.FullAt, Stats), scripts.go (valid_tuple 8|7, identity over t[1..7], !epoch/!epoch_at/!mac, the
  epoch ARGV conflict), integrity.go (new: the two MACs, the ring), integrity_test.go (new: forged tag, 7-field fact,
  DUMP/RESTORE rollback, replay-until-rebuild, stale-tag revoke still removes, rotation, keyed↔unkeyed separation),
  limits_test.go (25-byte overhead), canonical_test.go, end_to_end_test.go (FullRebuildEvery from FullAt), README.md,
  go.mod, go.sum}`. **Verify:** in the module `go build ./... && go test -race ./... && go vet ./...` with `redis-server` on
  PATH (the run that counts has it), then `GOWORK=off go build ./... && GOWORK=off go test ./...`; `govulncheck ./...`
  recorded; `make check`; Verification §2.
- **PR-3 — authentication core + the example wiring (`authentication/governance-s7`).** **Depends on** PR-1 MERGED (the
  examples use `AuditGate` through go.work) and on S4c PR-2d having cut `pockets/authentication/v0.29.0` (then this is
  v0.30.0; else v0.29.0 — the record names the actual tag). **BREAKING:** rows 8, 10 (+ 7, 9 named refusals). **Tag:**
  `pockets/authentication/<next minor>`. **Files** (under `pockets/authentication/`): `inbound/http/{host_policy.go
  (UserAdminReadAudit; the TargetUserID doc), useradmin.go (route behind SecurityEventsRoute, handler, DTOs, since/until,
  the strict list parse), machine.go (parseOptionalTime field name), useradmin_test.go, invitation.go (mine
  identifier_kind, the sync route, writeInvitationError), invitation_test.go, invitation_management_test.go,
  csrf_form_lane_test.go}`; `logic/authentication/{useradmin.go (ListSecurityEvents + the audit_read record), service.go
  (VerifiedIdentifiers, SyncInvitations; the resolver port's kind), helpers.go, resolver.go, passwordless.go:365-378,
  oauth.go:485,635, external.go:782, identifier_management.go:244-259 (the LAST call), abuse.go (invitation_sync budget),
  securityevent/securityevent.go (TypeAuditRead + Types()), securityevent/securityevent_test.go}`; `logic/invitations/
  {service.go (ResolveInvitations kind + pendingOnly, Mine kind, the membership check, inviteLink, the redirect-fragment
  refusal, provenance + resolved_by), options.go (VerifiedIdentifiers replaces CallerIdentifiers; WithLinkTokenInQuery),
  service_test.go, deliverydata_test.go, accept_denial_test.go, audit_digest_test.go, delivery_test.go, recovery_test.go}`;
  `logic/authentication/identifier/*_test.go` (the Normalize idempotence assertion); `logic/delivery/router_data_test.go`;
  `{config.go (LinksConfig.InvitationTokenInQuery, AdministrationConfig.SecurityEventsRoute, the With*), security.go (the
  WARNs), constructor.go:478-535 (the new closures), security_test.go}`; `README.md` (invitations: identifier resolution +
  the sync route + the cross-identifier case + R-8's rule, landing-page responsibilities incl. the login detour, the
  `Secret` note, the one-release note; "Security posture" audit table: `audit_read`; the admin route table; the error
  codes). **Examples:** `examples/production-baseline/pockets/audit/
  outbound/securityevents/{bridge.go (AuditReads: no dedupe, drops counted), bridge_test.go}`; `examples/production-baseline/
  cmd/server/{main.go (AuditGate = the role gate, WithAuditReadObserver(bridge.AuditReads()), Services.Audit,
  WritePolicy: AllOf(RefuseSelfTarget, adminWritesAnyRole), SecurityEventsRoute: true), production_test.go}`;
  `examples/auth-cms/cmd/server/{authorization.go (AuditGate, Services.Audit, the observer into svc.SecurityEvents,
  AllOf(RefuseSelfTarget, policy.RoleWrite)), main.go (SecurityEventsRoute), authorization_test.go, invitation_recovery_
  test.go:86,233, passwordless_provision_test.go, authentication_host_api_test.go}`; `examples/auth-cms/README.md` (the
  curl leg); `examples/auth-cms/pockets/access/inbound/policy.go` (`UserAdmin` switches on `Action`, FE3). **Verify:**
  `cd pockets/authentication && go build ./... && go test -race ./... && go vet ./...`; `cd examples/auth-cms && go test
  ./...`; `cd examples/production-baseline && go test ./...`; `make guard`; `make check`; Verification §1 and §3.
- **PR-4 — the record (`docs/s7-release-record`).** **Depends on** the three tags cold-verified AND the owner drill (§4)
  recorded. **Files:** `RELEASING.md` (ONE section "Authorization governance and invitations (SOC 2 slice S7)": tags, the
  Upgrade notes by host shape, the gps-360-go goredis note, the drill result, the verification record); every
  `examples/*/go.mod` pinning a moved module (explicit `GOWORK=off go get`; the baseline and auth-cms move authorization →
  v0.31.0 and authentication → the new tag); `workshop/documentation/docs/pockets/{authorization.md ("Added by S7": the
  audit route, the helpers, the WARN, `FullRebuildEvery`, the key), authentication.md (the fragment flip, open kinds, the
  admin route)}`, `workshop/documentation/docs/guides/{production-posture.md:370 (link + the key), operations-runbooks.md
  ("Export audit" → the routes)}`; `SECURITY.md` (the Redis in-scope line); `control-map.md:33,47-50,72,137`; `roadmap.md`
  S7 line; `plans/s7-authorization-governance.md` (executed copy). **Verify:** `make check`; `make guard`; `GOWORK=off go
  build ./... && go test ./...` per repinned example; `pnpm build` in `workshop/documentation`.

## Sequencing
```
PR-1 ─[merge]─[tag 1: authorization v0.31.0]─┬─ PR-2 (repin + integrity) ─[redis leg on main]─[tag 2: goredis v0.6.0]
                                              └─ PR-3 (needs PR-1 merged; S4c PR-2d tagged) ─[§1+§3 legs]─[tag 3: authentication next]
                         tags 1–3 cold-verified + the owner drill ─ PR-4 (record)
```
PR-1 first (PR-3's example wiring needs `AuditGate` through go.work; PR-2's repin needs tag 1). PR-2 and PR-3 run in
parallel worktrees off tag 1. PR-3 waits for S4c's PR-2d tag only to avoid a core-version collision; its files do not
overlap S4c's. Three trains is the floor: stores pgx/turso are untouched (no lockstep — set A).

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `pockets/authorization/v0.31.0` | PR-1 merge | **set A:** core v0.31.0 beside RELEASED stores pgx v0.21.0 / turso v0.20.0 + pgxdb v0.14.0 + turso v0.9.0 selects sdk v0.20.0 and builds; the goredis store v0.5.0 (core v0.27.0 pin) still builds beside it |
| 2 | `pockets/authorization/stores/goredis/v0.6.0` | PR-2 + the redis leg on `main` | **set B:** goredis v0.6.0 + core v0.31.0 + sdk v0.20.0 + go-redis v9.18.0: `go mod verify`, build, `go test` from the cache (skips without redis-server; the leg ran on `main`); a gps-360-go-shaped `main` (core + pgx store v0.21.0 + goredis v0.6.0 + pgxdb v0.14.0) builds |
| 3 | `pockets/authentication/<next>` | PR-3 merge; S4c v0.29.0 cut | **set C:** core <next> beside RELEASED stores pgx v0.13.0 / turso v0.12.0 AND views/goth v0.8.0 builds an auth-cms-shaped `main` (no store or views lockstep) |

**Tagging.** Three annotated, unsigned tags, each cut at its squash commit, pushed, THEN polled on `.info`; cold-verify
with `GOWORK=off`, an empty `GOMODCACHE`, `proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build <module>/...`.
Never tidy before a tag resolves; never `go work sync`. **Not tagged:** sdk, stores pgx/turso (both pockets), views/goth,
ui/goth, `pockets`, workshop (no new P-rule), the examples.

## Verification
Real binaries in DEVELOPMENT on a FREE port (`lsof -i :8093` first; never 8082 or 55432). `$B` = a scratch auth-cms binary
(`PORT=8093 AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 go run ./cmd/server`, authmem, console mailer); `$P` = a scratch
production-baseline binary on a fresh PG17 container with the baseline ledger. Events are read from `GET /debug/security-
events` (auth-cms) or the new `GET /auth/admin/security-events` (both) as a platform admin.

- **§1 — the authorization surface, the real binaries over HTTP (PR-3 branch, PR-1 merged).** (1) `$B` boot: `registered
  authorization pocket … audit_read_route=true integrity_rules=<n>`; a second binary with an empty policy over the auth-cms
  model prints the FAZ-10 WARN naming `owner` and its types; a third with no `Mutations` prints the writer WARN. (2) As a
  non-admin `GET /authorization/audit` → **403** and ONE `authorization_denied` row; as a platform admin → **200**
  `{items:[…], next_cursor, has_more}` ordered `occurred_at DESC`, each item with `source.actor_*`, `change.action`,
  `change.tuple`, `Cache-Control: no-store`; page 2 by cursor shows no duplicate; `?resource_type=project` alone, `?q=x`,
  `?offset=5`, `?limit=101` → **400** each with its code; the same call on `$P` leaves ONE `audit_read` row
  `{rail:authorization, filters:[resource], limit:25, returned:n}` with the admin as Actor, IP and UA, and a second
  identical call a SECOND row (no dedupe). (3) Assign a role to another user → **200** and `iam_audit` rows appear in the
  listing with `request_id`; assign a role to YOURSELF → **403**, `role write refused … error_kind=forbidden`, an
  `OutcomeRefused` event, no audit row; unassign yourself → the integrity policy decides, not the helper. (4) In-process:
  `AuditGate` without `Services.Audit` → `New` fails; without an observer → the WARN; a reader returning
  `sdk.ErrUnavailable` → **503** code `unavailable`.
- **§2 — PR-2, the Redis leg (on the branch, then on `main` before tag 2).** With `redis-server` on a private socket: the
  goredis suite, `-race`; then the drill in `integrity_test.go` and once BY HAND with `redis-cli` on a keyed mirror
  (`tuplecache:v3:ik:{…}`): a `HSET` forward field with an appended wrong-tag 8-field fact (and separately a 7-field fact) →
  `Check` answers **false** from the DURABLE path (`Fallbacks` +1, `IntegrityFailures` +1), never true; `HSET !until <far
  future>` → header MAC fails → fallback; `DUMP`, revoke in SQL, delta publishes, `RESTORE` → ineligible (old `until`); a
  revoked fact's CURRENT-epoch encoding copied back → true (the residual) UNTIL `Rebuild`/`FullRebuildEvery` → false; a
  revoke whose stored tag differs still removes (BE1); `WithIntegrityKeys(new, old)` keeps reading, the next full publication
  re-tags; unkeyed and keyed runtimes never read each other's mirror; two replicas with `FullRebuildEvery: 10s` rebuild ONCE
  per period between them. Baseline: the v0.5.0 pass/skip counts recorded at step 2.
- **§3 — PR-3, the real binary over HTTP.** (1) Create an invitation for an unregistered email → the console mailer shows
  `…#token=<secret>`, no `?token`; `AUTH_INVITATION_TOKEN_IN_QUERY=1` (a second binary) → `?token=` and the WARN; a request
  with `redirect: "https://x/#a"` (allowlisted) → **400** `invitation_redirect_fragment`, no row. (2) Register that email,
  verify, sign in, `POST /auth/invitations/accept {token}` → **200**; the `invitation_granted` row carries `invitation_id`,
  `operation_id`, `resolved_by:accept`; decline with the token from the fragment → **200**; accept as a user whose verified
  email differs → **403** `invitation_identity_mismatch` + `invitation_granted failure reason=policy`. (3) `auto_accept: true`
  for a known user → `invitation_granted` with `direct_add:true, invited_by, operation_id, resolved_by:direct_add`, no
  `invitation_created`. (4) **The owner's cross-identifier case:** invite `a@x` twice (A `auto_accept`, B not); a NEW user
  signs up by passwordless PHONE, adds `a@x` through the identifier-change flow and confirms the code → **200**, A grants
  (`invitation_granted resolved_by:verification`, the member-added notice), B stays pending and appears under `GET
  /auth/invitations/mine?identifier_kind=email`; `?identifier_kind=chat` → **400**; `POST /auth/invitations/sync` →
  `{resolved: 0}` (idempotent), the 11th call in a minute → **429**; accepting B by its `#token` → **200**. A second,
  non-primary verified email accepts an invitation addressed to it → **200**. An `identifier_kind: "chat"` invitation
  (auth-cms wires a console notifier) still accepts by token alone and never resolves (the residual). (5) `GET
  /auth/admin/security-events?event_type=invitation_granted&since=<today>`
  as an AAL1 admin → **401** + challenge (`admins`); after a step-up → **200**, cursor paging to `has_more=false`, no
  duplicate across pages; ONE `audit_read` row per page `{rail:security_events, filters:[event_type, since], limit,
  returned}`; a non-admin → **403**; `since >= until`, `?q=`, `?order=`, `?offset=` → **400**; a binary without
  `SecurityEventsRoute` → **404**. (6) No row, log line or response carries a token or an address (digests only, AUDIT-054).
- **§4 — the owner drill (PRECONDITION of PR-4; R-6 picks the shape).** On the owner's Mac against a build from the tags:
  (a) the §3(1) invitation mail link in the console mailer carries `#token=`, and (b) the two audit routes answer as a
  platform admin in the browser and each read shows up as an `audit_read` row on the next call; (c) if the owner runs
  Redis locally: the §2 forgery drill by hand. Pass/fail per step goes into the RELEASING section.

## Open rulings — YOUR CALL
- **R-1 — the Redis integrity shape.** (a) Per-fact HMAC tags + the Go-computed header MAC + epoch as §1 (Lua never holds
  the key; deltas stay in Redis; ~350 lines in one module; a core `State.FullAt` field). (b) Whole-set signatures with
  deltas moved to Go (read-modify-write under `WATCH`; a rewrite of the adapter's one atomic primitive). (c) Docs only
  (hardening recipe, no option) and `FullRebuildEvery` alone. **Recommend (a):** the fit the roadmap named ("a compromised
  cache degrades to availability loss rather than access gain"); after BE2 it bounds rollback by `MaxStaleness` and replay
  by the rebuild cadence, with the residuals written down; (c) leaves `control-map:72` OPT-IN (documented only) and no
  control to show gps-360-go's auditor.
- **R-2 (DECIDED by gate, BE6): a typed `WithAuditReadObserver`, not the `DecisionObserver`** — the bridge's `switch
  ev.Outcome` drops unknown outcomes and its dedupe would merge separate reads; a compliance read trail is never deduped.
- **R-3 (DECIDED by gate, BE11): `RefuseSelfTarget` + `AllOf`; two-person approval is a README recipe**, not a helper (one
  request, one principal; a `RequireApproval(lookup)` port needs an approvals ledger the framework does not have).
- **R-4 — an HTML arm for the audit routes.** (a) JSON only; the React admin and `curl` consume it; views/goth untouched.
  (b) A goth "Security events" admin page. **Recommend (a)** (the owner's hosts are React; no views tag).
- **R-7 — the invitation link's default destination (FE1's question).** Today an invitation with no allowlisted `redirect`
  links to `/?token=` — a RELATIVE link, broken in mail clients (a latent bug the flip would carry to `/#token=`). (a) New
  `LinksConfig.InvitationURL` (`AUTH_INVITATION_URL`): an absolute http(s) landing URL with no fragment, the default
  destination when `redirect` is empty; REQUIRED in production when a Granter is wired (`ErrInvitationURLRequired`; the
  `PasswordResetURL` precedent), development WARNs and keeps `/`. (b) Keep the `/` fallback and only document it. **Recommend
  (a):** it is the precedent for every other credential-bearing link, and a production refusal is the only thing that stops
  a relative link from shipping; the cost is one more required env for invitation hosts (auth-cms and the baseline set it).
- **R-5 — the invitation landing page.** (a) README checklist only; the host's landing page is proven at adoption (the
  owner's React hosts). (b) A bundled `GET /auth/invitations/landing` page following `MagicLinkLanding` (views/goth tag,
  auth-cms run-and-look). **Recommend (a):** the pocket owns no invitation page today, and (b) adds a views train for a
  page the owner's hosts will not mount.
- **R-8 — whose identifier is it (BE-R8's question).** A verified value is unique per `(user, kind)` but not across time: if
  A gives up `X` and B later verifies `X`, B resolves every pending invitation still addressed to `X`. (a) The CURRENT
  verified holder resolves (recommend: it is the owner's model — "addressed to the identifier" — bounded by the 7-day TTL,
  the inviter's `AutoAccept` choice and the inviter's cancel; the README says so). (b) Resolve only invitations created after
  the subject's `VerifiedAt` — excludes the primary case (invited before signing up); rejected unless the owner wants it.
- **R-6 — the owner drill.** (a) §4 (a)+(b) on the auth-cms build (console mailer + browser JSON) with (c) optional.
  (b) Add a goredis run on the owner's gps-360-go dev stack before the record (the only real TupleCache host). **Recommend
  (a) now, (b) at adoption:** the maintenance rule defers host adoption to the roadmap's end; the §2 leg and `integrity_test`
  are the evidence until then.

## Open questions
_None from the code._ Host facts the owner answers at adoption: whether any host calls `ResolveInvitations` directly (row 9;
none in this repo) and where gps-360-go's Redis runs (dedicated or shared — the plan assumes dedicated).

## Recommended reviews
**product-manager:** R-1 (a vs c), R-5 and R-8 before ratification. **platform-sre:** key custody and rotation, the `ik:`
mirror cut-over, the fragment hatch's one-release removal, the sync budget. **data-integration-reviewer:** the Lua
`valid_tuple` 8|7 change and `!epoch`/`!mac` atomicity in `replaceScript`/`deltaScript`. **architecture-steward:** the
observer option on the HTTP adapter (no rail in the authorization pocket) and `SyncInvitations` on `authlogic.Service`.

## Notes
- **Follow-ups.** Reverse listing `ListSubjectsWith(permission, resource)` and an effective-access listing (FAZ-7 "later");
  `InvitationTokenInQuery` removal (next authentication minor — add to that plan's rows); a goth admin page for both rails
  (R-4b); retiring auth-cms `/debug/security-events` once S4c closes (FE11: `demo.go:158-235`, the README rows, `.env:21`,
  port `debug_route_test.go` to the bundled route); **deferred store changes (BE13):** an actor filter on
  `securityevent.ListFilter` ("what did admin X do") and an open-kind `Mine`; gps-360-go adoption of `WithIntegrityKeys` +
  `FullRebuildEvery` (handoff, at the roadmap's end).
- **Future slice — account merge (owner: "merge accounts at some point with double verification").** Two existing
  accounts (invited at one identifier, signed up at another); S7 only lets a user ADD the second identifier to one account.
  The hard parts a merge plan must settle: double verification (a live session on each account, or a step-up on the
  survivor plus a challenge to the absorbed address); which account survives and the absorbed one's terminal status;
  re-pointing authorization relations and `iam_audit` actor ids through the host's Granter/Mutations (the pocket owns no
  tuples); the absorbed account's sessions, grants, API keys (revoke), passkeys (user handle is per account), TOTP, recovery
  codes, OAuth links, external subjects, contact changes, pending invitations and `resolved_subject_*`; `account_merged`
  rows on both; erasure of a merged account; one transaction across every table on pgx + turso + memory + `storetest`.
- **Cut for the cap:** per-case lists beyond the suites; the Lua diff; the security-events DTO table (§3's item list is the
  contract); the handoff prose (PR-4 writes it).
