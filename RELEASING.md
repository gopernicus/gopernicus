# Releasing gopernicus modules

This repo is a multi-module workspace (`go.work`, dev-only) with thirty-seven
modules today: `sdk`; `integrations/{cryptids/bcrypt, cryptids/golang-jwt, cryptids/google-uuid,
datastores/pgxdb, datastores/turso, email/sendgrid, filestorage/gcs,
filestorage/s3, kvstores/goredis, oauth/github, oauth/google,
notify/mailer, scheduling/robfig-cron, tracing/otel}`; `features/authentication`
(+ `views/templ`, its bundled default views module — auth-v3 AV3-8.2, 2026-07-13),
`features/authorization` (authorization-v1, 2026-07-09), `features/cms`
(+ `views/templ`, its bundled default views module — feature-standard B2,
2026-07-07), `features/events` (events-v1, 2026-07-08), `features/jobs`
(each feature + `stores/{turso,pgx}`); `examples/{cms,
minimal, auth-cms, jobs-minimal}`; `workshop/gopernicus` (the scaffolding
CLI — a `go install`-able tool, tagged like any importable module). Each importable module (everything except the four
`examples/*` hosts, which are demonstrations, not libraries) is tagged and
versioned **independently** — there is no single repo-wide version.

No tags have been cut yet. This document is the procedure for when they are;
cutting a tag is out of scope for the milestone that introduced this file.

## Tagging scheme

Nested Go modules in a single repo are tagged with the module's directory as a
prefix, per the standard Go module convention for multi-module repos:

```
sdk/v0.1.0
integrations/datastores/turso/v0.1.0
features/cms/v0.1.0
features/cms/stores/turso/v0.1.0
```

Each module's own `go.mod` `require` versions (e.g. `features/cms/stores/turso`
requiring `sdk`) are bumped and tagged independently — a patch release of
`sdk` does not force a release of every module that depends on it, only the
ones whose `go.mod` is updated to require the new version.

## Preconditions before the first tag

1. **Module paths are final.** Every `go.mod` module line and internal import
   is rooted at `github.com/gopernicus/gopernicus/...`.
2. **`replace` directives are dropped or pinned.** `go.work` itself is
   dev-only and is never part of what a downstream consumer sees. The nested
   modules that reference sibling modules by relative path in their own
   `go.mod` (e.g. `features/cms/stores/turso`'s `replace` of `sdk` and
   `features/cms` to `../../../../sdk` and `../../..`) must have those
   `replace` lines removed and replaced with ordinary `require` entries
   pinned to the sibling module's tagged version, so `go build` works for a
   consumer who does not have this repo checked out as a workspace.
3. **Guards + tests green** (`make check`) on the commit being tagged.

## Cutting a tag

For each module being released, from the repo root:

```sh
git tag features/cms/v0.1.0 -m "features/cms v0.1.0"
git push origin features/cms/v0.1.0
```

A consumer depends on it the normal Go way:

```sh
go get github.com/gopernicus/gopernicus/features/cms@v0.1.0
```

## Version bumps

Standard Go module semver rules apply per-module:

- **Patch** — bugfix, no exported API change.
- **Minor** — additive, backward-compatible exported API change (e.g. a new
  `Config` field with a working zero value, a new optional `Mount` field per
  C3's evolution policy in `features/README.md`).
- **Major** — breaking exported API change (removed/renamed exported type or
  field, changed method signature). Pre-`v1`, breaking changes are expected
  and do not require a major bump by Go's own pre-release semantics; each
  module should still move to `v1.0.0` deliberately once its contract is
  considered stable, not accidentally on the first tag.

## Upgrade notes (keyed to each module's next tag)

### features/authentication — next tag: session hashing invalidates all live sessions

auth-v2 (2026-07-07) moved session-token storage to service-side SHA-256
hashing (design §7.3): the service hashes the cookie token before every
repository call, and stores keep persisting an opaque string — no DDL. Any
host upgrading `features/authentication` across this change must know:

- **Every live session is invalidated at deploy — a forced logout for all
  users, remember-me/long-TTL sessions included** (a v1 plaintext row never
  matches a hashed lookup again). Users just log in again; no data is lost.
- The orphaned plaintext rows are unreachable and dead past their natural
  `expires_at` TTL. No purge ships; hosts may vacuum them at leisure.
- **Deploy in a single cutover or drain traffic first — do not roll.** On a
  rolling deploy, mixed plaintext/hashed pods make the SAME session cookie
  flap 401/200 depending on which pod answers, for the whole rollout window.
- **A rollback forces a SECOND mass logout**: sessions minted by the new
  binary are hashed rows the old binary cannot read.

The same note lives in `features/authentication/README.md` (the upgrade note section).

### features/authentication stores — next tag: invitation identifier kinds

identity-resolution (2026-07-10) gave invitations an `identifier_kind` column
(`TEXT NOT NULL DEFAULT 'email'`) and widened the pending-tuple unique index
to include the kind. Per the greenfield-migrations rule (2026-07-12) the
column lives in `0011_invitations.sql`'s CREATE — no evolution file ships. A
host that scaffolded the pre-kind table adds the column with its own host-tree
migration (`ADD COLUMN` + drop/recreate of the pending-tuple index); rows
default to `email`, and hosts that only ever create email invitations see zero
behavior change.

### sdk — next tag: the layering split moved every sdk subpackage import path

sdk-layering (2026-07-10) re-homed `sdk/errs` into the root package
(`sdk.ErrNotFound`) and moved every other sdk package under
`sdk/foundation/` or `sdk/capabilities/` (package names unchanged, paths
only). Pre-tag there is no version obligation, but any consumer pinned
to a git SHA must re-path its imports wholesale; the workshop CLI's
emitted scaffolds already use the new paths.

### sdk — next tag: additive middleware symbols (minor floor)

middleware-consolidation (2026-07-11) added exported symbols to two sdk
packages, all backward-compatible — they floor sdk's next tag at a **minor**
bump, never a major:

- `sdk/capabilities/ratelimiter`: `Middleware` + the `Allower` port (the generic
  IP/key rate-limit middleware, relocated out of `features/authentication`'s
  internals).
- `sdk/foundation/web`: `TrustProxies` + `ClientIP` (the rightmost-minus-N
  client-IP resolver ported from the original gopernicus `httpmid`).

No existing symbol changed; a consumer that ignores the new surface is
unaffected.

### features/authorization — next tag: additive gate symbols (minor floor)

middleware-consolidation (2026-07-11) added `RequirePermission`,
`ResourceResolver`, and `FixedResource` to the root package (the exported HTTP
middleware gate; implementation in `internal/logic/authorizersvc`). Additive —
floors the next tag at a **minor** bump. Adopter note: replacing a hand-rolled
gate closure with `RequirePermission` changes the 401/403 response *body* to the
FS9 `web.Error` shape (status codes unchanged) — a client contract detail, not a
Go-API break.

### features/authentication — next tag: patch-only (internal delegation)

middleware-consolidation (2026-07-11) rewrote `Service.RateLimitByIP`'s body to
delegate to `ratelimiter.Middleware` with its exact prior signature and
semantics; no exported surface changed. This is a **patch**, not a minor — the
proof is auth's existing rate-limit tests passing unmodified. (Superseded for
the same tag by the refresh change below, which forces a **breaking** bump.)

### features/authentication — next tag: JWT sessions + refresh rotation (BREAKING)

The refresh change (auth-jwt-session-refresh, 2026-07-11, amends AV6) makes the
access credential a self-validating JWT and turns the session row into the
revocation + refresh anchor. It re-keys `session.SessionRepository` and the
`Config`/`Service` surface — a **breaking version bump** for the feature. The
runbook (mirrored in `features/authentication/README.md`):

- **All live sessions invalidate at deploy — a forced logout for every user**
  (the sessions table is re-keyed; an upgrading host's own migration drops and
  recreates it). No data lost; users log in again.
- **Do NOT roll-deploy across the sessions re-key.** Old binaries SELECT the
  dropped `token` column and **error outright** (not a 401 flap). Stop old,
  migrate, start new.
- **Rollback = restore the old schema AND force a second logout** (the new
  binary's id-keyed rows are unreadable by the old binary).
- **`AUTH_JWT_SECRET` is now required — and required-SHARED for multi-instance
  hosts.** `Config.TokenSigner` is required (`ErrTokenSignerRequired` on nil);
  per-instance ephemeral keys cannot cross-verify behind a load balancer, so a
  multi-instance host MUST share one secret across every instance. Ephemeral
  keys are a single-instance dev convenience only.
- **`Config.TokenTTL` is removed** (compile-time break): replace with
  `AccessTokenTTL` (default 15m) and `RefreshTTL` (default 7d). No host silently
  inherits the old 1h access window.
- **`POST /auth/token` response changed** from `{token, expires_at}` to
  `{access_token, expires_at, refresh_token}` — a breaking client contract;
  API clients now rotate via `POST /auth/refresh`.
- **The session repository port was re-keyed** (id-keyed; `Get`,
  `GetByRefreshHash`, `Rotate`/`ConsumeGrace` CAS, `Delete`, `DeleteByUser`;
  new `ErrRotationConflict`) — a breaking bump for the feature AND both nested
  store-module tags (see below).

### features/authentication stores — next tag: sessions re-key (BREAKING) + greenfield migration set

auth-jwt-session-refresh (2026-07-11, D6) re-keyed sessions to an id-keyed
anchor with `refresh_token_hash` (UNIQUE index), `previous_refresh_token_hash`
(nullable, partial index), `previous_used`, `rotation_count`, and a `user_id`
index. The store adapters implement the re-keyed `session.SessionRepository`
(CAS `Rotate`/`ConsumeGrace`, `GetByRefreshHash`, `MapError`-routed `Create`),
so both nested store-module tags (`features/authentication/stores/turso`,
`.../pgx`) take a **breaking version bump**.

**Greenfield-migrations rule applied (2026-07-12, jrazmi ruling):** the
canonical set defines the FINAL schema and never carries upgrade/evolution
files. The sessions re-key lives in `0003_sessions.sql`'s CREATE, and the
former evolution files (`0012_id_defaults`, `0013_invitation_identifier_kind`,
`0014_sessions_refresh`; cms's `0022_id_defaults`) were folded into their base
CREATEs — auth's set is `0001…0011`, one CREATE per table. New hosts scaffold
the final shape. A host that scaffolded an earlier shape writes its OWN
migration in its host tree (reference SQL in the feature README's upgrade
note; segovia's `0018_sessions_refresh.sql` is the exemplar). Same
no-rolling-deploy / rollback runbook as the feature entry above.

### features/cms stores — next tag: id defaults folded into base CREATEs

The greenfield-migrations rule also folded cms's `0022_id_defaults.sql` into
the six entity tables' CREATE files (terms, menus, menu_items, assets,
inquiries, entries) in BOTH dialects. Schema-identical for any DB that had
already applied 0022; a host that scaffolded before the id-defaults change
adds the defaults with its own host-tree migration.

### features/authentication + both store modules — next tag: auth v3 identity (BREAKING)

auth-v3 (the identity milestone, 2026-07-13) reshapes the feature off a single
`users.email`/`email_verified` pair onto the `user_identifiers` table (multiple
email/phone identifiers with explicit login/recovery/notification/primary uses),
adds `users.auth_revision` (the optimistic-serialization anchor) and session
authentication-metadata columns, adds the `challenges` / `contact_changes` /
`authentication_grants` flow tables, and retires the legacy
`verification_codes` / `verification_tokens` rail. This is a **breaking** bump
for `features/authentication` and BOTH nested store-module tags
(`features/authentication/stores/{pgx,turso}`): the `Repositories` bundle grows
identifier/challenge/contact-change/grant/credential ports, `user.User` loses its
email field, and routes/entities change.

The **AV3D delivery-runtime refactor** (2026-07-13, same untagged milestone) folds
into this cut: it removed authentication's private durable delivery queue, so the
canonical set is `0001…0013` with **no** delivery table (an earlier v3 cut's
`delivery_jobs` table was removed). Durable delivery is now the generic **jobs**
feature reached through `Config.DeliveryDispatcher`; the bounded ephemeral path is
`in_process`. Public removals: `Repositories.DeliveryJobs`, `domain/deliveryjob`,
`Service.RunDeliveryWorker`, and the delivery-durability errors. Rename:
`Config.DeliveryWorkerAcknowledged` → `DeliveryJobsAcknowledged`. Additions:
`Config.DeliveryMode`/`DeliveryDispatcher`/`InProcessDelivery`/`DeliveryEventsEmitter`/
`DeliveryEphemeralAcknowledged`, `Service.RunDelivery`/`DeliveryJobRuntime`, and the
generic-jobs fenced surface (`Repositories.FencedQueue`, `jobs.FencedRuntime`,
migration `0003_fenced_job_queue`). Behavior: `DeliveryStatus.Attempt` now reads 0.
The host-side upgrade is the **Auth delivery-runtime upgrade runbook** below.

Per the greenfield-migrations rule (2026-07-12) the canonical migration set
ships only the FINAL schema and carries **no** upgrade/evolution file — a live
v2 host owns its own evolution and MUST NOT blind-copy the canonical migrations
(the final `0001_users.sql` no longer carries `email`, so copying it onto a
populated v2 `users` drops email before any backfill). The host-owned,
backfill-first, validated migration procedure — exact pgx and SQLite/libSQL SQL
for both dialects — is the **Auth v3 host upgrade runbook** below. The same note
is mirrored in `features/authentication/README.md`.

### features/authentication/views/templ — next tag: NEW module (first tag)

auth-v3 (2026-07-13, AV3-8.2) added `features/authentication/views/templ`, the
feature's bundled default HTML view module (the thirty-seventh workspace module),
sibling to `features/cms/views/templ`. It carries the `a-h/templ`-rendered default
auth pages so the feature core stays presentation-free (`Config.Views == nil` is
API-only; a non-nil `Views` — the bundled module or a host's own — adds HTML
without changing any JSON contract). This is a **new, standalone module getting its
first tag**; it depends on `features/authentication` (for the exported view models)
and is tagged independently like every other importable module. A host that renders
its own views never imports it.

### integrations/datastores/turso — next tag: BEGIN IMMEDIATE write-intent transactions (patch, behavior fix)

auth-v3 (2026-07-13, AV3-2.4) changed `integrations/datastores/turso`'s `DB.Begin`
to issue `BEGIN IMMEDIATE` over a pinned `*sql.Conn` instead of the driver's default
`BEGIN` (DEFERRED); see `integrations/datastores/turso/tx.go`. No exported surface
changed, so this floors the next tag at a **patch**, but it is a **behavior change a
host must know**: the v3 step-up credential/identifier CAS rails (`Apply`,
`ApplyVerifiedChange`) need write-intent-up-front so `sqld` serializes contending
transactions and the loser's own CAS returns `sdk.ErrConflict`. A host on the
**pre-fix connector** gets a raw `SQLITE_BUSY` ("database is locked") to the CAS
loser instead of `sdk.ErrConflict` and **fails the concurrent step-up contract**.
Data integrity is never at risk either way (no double-commit) — but a turso host
adopting auth-v3 must run the connector at or past this tag.

### sdk/capabilities/{email,notify} — next tag: additive capability metadata (minor floor)

auth-v3 (2026-07-13, AV3-4.4) added the production-safety capability seam consumed
by the delivery worker's fail-closed transport gate — additive, so it floors sdk's
next tag at a **minor**, never a major:

- `sdk/capabilities/email`: new `Capabilities`, `TransportSecurity`, and the
  `CapabilityReporter` interface (`capabilities.go`); `Console` reports
  `{TransportSecurityNone, DevelopmentOnly: true}` and `SMTP` reports
  `{TransportSecurityStartTLS, DevelopmentOnly: false}`.
- `sdk/capabilities/notify`: the same trio; `Console` reports development-only.

A consumer fail-closes in production on a `DevelopmentOnly` / metadata-less
transport. No existing symbol changed; a consumer that ignores the new surface is
unaffected. (`sdk/foundation/cryptids`'s HS256 default and `sdk/foundation/web`'s
`TrustProxies`/`ClientIP` were **not** touched by auth-v3 — HS256 belongs to the
JWT-refresh cut and `TrustProxies` to middleware-consolidation, each keyed above.)

### sdk/capabilities/work — next tag: NEW module (first tag)

The SWP promotion (sdk-work-protocol, 2026-07-13) added `sdk/capabilities/work` —
the **keyed-work submission protocol**: a vocabulary + narrow-port contract with
**no default implementation** (the `oauth` posture). It has **no prior tag**, so it
is a **new module, first tag**. It ships:

- `work.Status` — the frozen seven-value lifecycle vocabulary
  (`pending`/`running`/`completed`/`failed`/`dead_letter`/`canceled`/`superseded`;
  `failed` is NON-terminal/retryable), with `Terminal()`/`Known()` predicates,
  pinned byte-for-byte to the persisted job-status strings by the package's own
  literal test;
- segregated consumer ports `Enqueuer` (idempotent keyed admission), `Replacer`
  (optional atomic replace/supersede), and `StatusReader` (deterministic
  latest-by-key, lifecycle-only — never payload/attempt/secret);
- an **opaque `[]byte`** payload (deliberately NOT `json.RawMessage`: some producers
  submit ciphertext, so the protocol must not imply JSON); and
- `worktest`, the conformance suite an implementation runs.

The implementation of record is `features/jobs` (below). **Payload snapshot
ownership (SWP-3 / IX-23).** The implementation of record deep-copies the payload
with a central `bytes.Clone` at the protocol boundary, so a keyed unit's admitted
bytes are a store-independent snapshot: a later caller mutation of its slice cannot
alter admitted work, for every backing store, by construction. `worktest` pins this
under `-race`; a new backend inherits the semantic from the protocol, not from its
own storage layer.

### features/jobs + both store modules — next tag: SWP fenced delivery surface (minor floor)

auth-v3/AV3D (2026-07-13) made `features/jobs` the **implementation of record** for
`sdk/capabilities/work` and added the opt-in lease-fenced delivery surface. All
changes are **additive / source-compatible**, so the floor is a **minor** bump (no
existing exported signature was removed or changed incompatibly), with two adopter
notes:

- **New sdk dependency floor.** `features/jobs` now imports `sdk/capabilities/work`;
  a host pins sdk at or past the `work`-carrying tag.
- **`job.Status` is now a source-compatible alias** `type Status = work.Status` (was
  a distinct `type Status string`). The persisted strings are byte-identical, and an
  alias is assignable both ways, so existing `job.Status` code compiles unchanged;
  `job.StatusCanceled`/`job.StatusSuperseded` are new members produced only by the
  fenced queue.
- **Additive surface:** `Repositories.FencedQueue` (nil = the fenced surface is
  off), the keyed-work primitives (`EnqueueOnce`/`Replace`/`LatestStatusByKey` over
  opaque `[]byte`, `Checkpoint`/`PurgeTerminal`), `jobs.FencedRuntime`, and the
  opt-in migration `0003_fenced_job_queue` (both dialects). The existing
  unfenced `Queue`/cron surface, its migrations, and every current consumer are
  unaffected. A host may now wire `FencedQueue` alone (delivery-only), `Queue` alone
  (existing cron host), or both; `ErrQueueRequired`'s message widened accordingly.

Downstream upgrade example — a consuming feature depends on the sdk `work` ports,
never on `features/jobs` (constitution rule 6):

```go
// BEFORE (pre-SWP): a consumer hand-declared its own narrow enqueuer port.
type enqueuer interface {
    EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (string, error)
}

// AFTER (SWP): depend on the canonical sdk ports; jobs.Service satisfies them.
import "github.com/gopernicus/gopernicus/sdk/capabilities/work"

type Deps struct {
    Enqueuer work.Enqueuer     // jobs.Service.EnqueueOnce
    Replacer work.Replacer     // jobs.Service.Replace     (optional)
    Status   work.StatusReader // jobs.Service.LatestStatusByKey
}
```

### Auth v3 tag requirements + production checklist

**Per-module tag requirements for the auth-v3 cut** (semver floors; no tag is cut
until the release workflow authorizes it):

| Module | Floor | Why |
|---|---|---|
| `features/authentication` | **major / breaking** | `Repositories` grows identifier/challenge/contact-change/grant/credential ports (NO delivery port — durable delivery is the generic jobs feature via `Config.DeliveryDispatcher`); `user.User` loses its email field; `Config` and routes/entities change; the legacy `verification_*` rail is retired; the AV3D delivery removals/renames above apply |
| `features/authentication/stores/pgx` | **major / breaking** | implements the re-keyed `Repositories` over the greenfield `0001…0013` set (no `delivery_jobs`) |
| `features/authentication/stores/turso` | **major / breaking** | same, libSQL dialect |
| `sdk/capabilities/work` | **new module — first tag** | the keyed-work submission protocol (vocabulary + ports, no default); `features/jobs` is its implementation of record |
| `features/jobs` + both store modules | **minor** | implements `sdk/capabilities/work` (new sdk dep); additive fenced delivery surface: `Repositories.FencedQueue`, keyed-work primitives over opaque `[]byte`, `jobs.FencedRuntime`, `PurgeTerminal`, migration `0003_fenced_job_queue`; `job.Status` is now a source-compatible alias of `work.Status` (existing consumers unaffected) |
| `features/authentication/views/templ` | **new module — first tag** | bundled default HTML views (additive; opt-in) |
| `integrations/datastores/turso` | **patch (behavior fix)** | `BEGIN IMMEDIATE` write-intent transactions (required by a turso host adopting v3) |
| `sdk/capabilities/{email,notify}` | **minor** | additive production-safety capability metadata |

The four `examples/*` hosts (including `examples/auth-cms`, the auth-v3 proof host)
are demonstrations, not importable modules, and are never tagged.

**Host upgrade order** is the seven-step, backfill-first, host-owned procedure in
the runbook below (single cutover — do not roll; destructive Step 6 only after the
v3 binary is confirmed stable). It has been validated on fresh/reset databases both
dialects (AV3-9.2) and **not** applied to any real host.

**Production readiness checklist** (fail-closed gates a host MUST satisfy before
`RuntimeMode` production — detail in `features/authentication/README.md`):

- **Five distinct host-supplied secrets — never reuse one value for two roles.**
  The real key material the host wires into `Config` (proof-host env names in
  parentheses) and each key's ACTUAL rotation story:
  1. **Access-JWT signer** (`Config.TokenSigner`, `AUTH_JWT_SECRET`). Signs the
     access JWT (required — `ErrTokenSignerRequired`). **Single-key, disruptive:**
     rotating it invalidates every live access JWT, forcing re-authentication; a
     multi-instance deployment MUST share one value (a per-instance key flaps auth).
     Use a rolling deploy and expect existing bearer sessions to re-auth.
  2. **Challenge HMAC pepper** (`Config.ChallengeProtector`, `AUTH_CHALLENGE_PEPPER`).
     Protects OTP short codes AND digests magic-link / password-reset tokens — there
     is NO separate magic-link/reset secret; those ride this pepper. It is the ONE
     key with **continuity-supporting rotation**: the bundled `HMACChallengeProtector`
     takes an `HMACKeyRing` (active key ID + retained older keys), so a rotation
     verified by `TestChallengeProtectorKeyRotation` keeps pending codes/links under a
     retained old key valid; removing an old key from the ring invalidates challenges
     still pending under it (the user restarts the flow).
  3. **Delivery payload AES key** (`Config.DeliveryEncrypter`, `AUTH_DELIVERY_ENCRYPTER_KEY`,
     AES-256-GCM). Seals the delivery-outbox envelope. **Single-key, disruptive:**
     rotating it only affects payloads sealed after the change, so **drain in-flight
     delivery work before retiring the old key** (an in-flight payload sealed under a
     removed key dead-letters and the user restarts the flow).
  4. **Provider-token AES key** (`Config.TokenEncrypter`, `AUTH_TOKEN_ENCRYPTER_KEY`,
     AES-256-GCM; optional — nil = provider OAuth tokens are not persisted). Encrypts
     stored OAuth access/refresh tokens at rest. **Single-key, disruptive:** stored
     tokens sealed under the old key become undecryptable on rotation (**stored-token
     loss** — affected users re-link the OAuth provider).
  5. **Identifier HMAC key** (`Config.IdentifierKeyer`, `AUTH_IDENTIFIER_KEY`; required
     in production — `ErrIdentifierKeyerRequired`). Derives PII-free rate-limit /
     idempotency keys. **Single-key**, but rotation is the least disruptive: derived
     limiter/idempotency keys change, so rate-limit buckets and enqueue-idempotency
     dedup reset once (transient; no session or credential loss). A multi-instance
     deployment MUST share one value so one identifier maps to one bucket.

  There is deliberately **no separate CSRF secret**: the double-submit CSRF token is a
  fresh per-render random value set as the `auth_csrf` cookie and compared in constant
  time against the `csrf_token` field — no host key material to manage or rotate.
- **Production rejects development transports and unacknowledged/incomplete wiring.**
  In `RuntimeMode` production, `NewService` fails construction on: a `DevelopmentOnly`
  / metadata-less email or notify transport (the `console` senders), a memory rate
  limiter, a missing `IdentifierKeyer`, a delivery runtime whose mode is unacknowledged
  (`DeliveryJobsAcknowledged` / `DeliveryEphemeralAcknowledged`), and — when the
  passwordless/link surface is wired — a missing or non-HTTPS `PublicAuthBaseURL`.
  `console` senders are development-only. **What `NewService` does NOT gate (host
  deployment checklist, not a construction guarantee):** `AllowedOrigins` may be empty
  at construction (an empty allowlist simply rejects every cross-origin browser POST at
  request time — the host must populate it for browser clients), and **trusted-proxy /
  `ClientIP` wiring is router-level** (`sdk/foundation/web` `TrustProxies`, wired by the
  host) and therefore **unobservable by `NewService`** — it cannot and does not reject a
  host that forgot it. Both are deployment-checklist items the host verifies, not
  construction-time failures.
- **The delivery runtime is host-lifecycle-owned, and its mode is explicit.**
  `Config.DeliveryMode` is required with no default (never inferred from a non-nil
  collaborator). The recommended production posture is `"jobs"`: wire
  `Config.DeliveryDispatcher` over the generic **jobs** feature, run the generic
  `jobs.FencedRuntime` (start on boot, stop on shutdown), and set
  `DeliveryJobsAcknowledged: true` (production rejects an unacknowledged jobs
  runtime and a `jobs`-mode config with no dispatcher). `"in_process"` is a bounded
  EPHEMERAL pool the host drives with `Service.RunDelivery(ctx)`; it does NOT
  survive a crash and has no cross-instance coordination, so production requires the
  explicit `DeliveryEphemeralAcknowledged: true`. In either mode the dispatcher is
  the only send path, delivery is **at-least-once** (not exactly-once — consumers
  tolerate duplicates; a resend cannot retract an in-flight provider call), payloads
  are always encrypted at rest, and terminal work is purged under bounded retention.
- **Migrations are host-owned and applied pre-boot** — the greenfield canonical set
  for a new host, or this runbook's backfill-first procedure for a live v2 host
  (never blind-copy the canonical `0001_users.sql` onto a populated v2 `users`).
- **pgx byte-order pagination parity needs a `C`-collation database** (parked
  shared-helper finding, not a v3 defect); **turso hosts need the `BEGIN IMMEDIATE`
  connector** (keyed above).

## Auth v3 host upgrade runbook (v2 → v3 identity)

A **host-owned** migration procedure for a database already running auth-v2
(`features/authentication` before the v3 identity work) that is upgrading to
auth-v3. Per the standing greenfield-migrations rule the canonical migration
trees ship the **final** v3 schema only and never carry upgrade/evolution files;
a v2 host's database does **not** match the canonical `0001_users.sql`, so the
host applies the steps below from its **own** host migration tree, pre-boot,
exactly like every other host-owned migration.

**No blind copy.** Do not apply the canonical `stores/{pgx,turso}/migrations/*`
files to a live v2 database. The canonical `0001_users.sql` describes the FINAL
users shape — `id, display_name, auth_revision, created_at, updated_at`, with no
`email`/`email_verified` — so applying it to a populated v2 `users` table would
drop the legacy email data before any backfill. A v2 host runs *this* additive,
backfill-first procedure instead; the destructive column removal happens only in
Step 6, after the backfill and its validation.

Validated on fresh/reset databases both dialects (see the AV3-9.2 execution
record at the end); it has **not** been applied to a real application host.

### Preconditions

- A confirmed, restorable **backup** taken immediately before Step 1.
- A maintenance window (single cutover; see deploy ordering below — the v3
  binary must not run against the pre-Step-5 schema, and old/new binaries must
  not both serve the same database across the cutover).
- v2 `users.email` is stored normalized (trimmed + lowercased) and is `UNIQUE`.
  If a host wrote un-normalized emails, the Step-1 collision dry-run catches the
  ambiguity **before** any write.

**Deploy ordering (single cutover — do NOT roll).** (1) Take the backup
(Step 1). (2) Stop the v2 binary (or drain traffic). (3) Apply Steps 1–5
(additive; keep the v2 binary stopped to avoid mixed-version reads/writes).
(4) Deploy and start the v3 binary; confirm it is healthy and stable.
(5) **Only after** v3 is confirmed stable, apply Step 6 (the destructive
cutover — the point of no return for a v2-binary rollback). Steps 1–5 are
reversible by restoring the backup or redeploying the v2 binary (the new
tables/columns are inert to it); Step 6 drops the legacy columns and
verification tables, after which the v2 binary can no longer read `users`.

### Step 1 — Backup and dry-run collision detection

Take a full, restorable backup first (`pg_dump` / a libSQL/SQLite file copy or
`.backup`). Then run the collision dry-run. **A non-empty result aborts the
upgrade** — do not choose a winner automatically; report the colliding rows for
a human decision (this mirrors the feature's atomic auth-claim invariant,
`UNIQUE(kind, normalized_value)` over active login/recovery identifiers).

pgx:

```sql
SELECT lower(btrim(email)) AS normalized_value,
       count(*)            AS n,
       array_agg(id)       AS user_ids
FROM users
GROUP BY lower(btrim(email))
HAVING count(*) > 1;
```

SQLite / libSQL:

```sql
SELECT lower(trim(email)) AS normalized_value, count(*) AS n
FROM users
GROUP BY lower(trim(email))
HAVING count(*) > 1;
```

If either returns rows, **stop** and resolve the collisions by hand. (Validated:
skipping this and forcing the Step-3 backfill fails atomically on the
`idx_user_identifiers_auth_claim` unique index with zero rows written — the index
is the structural backstop, but detecting collisions up front lets a human
choose, not the DB.)

### Step 2 — Create `user_identifiers` and its indexes

Required by the Step-3 backfill; purely additive.

pgx:

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

SQLite / libSQL:

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

### Step 3 — Backfill one primary email identifier per user

Insert exactly one active, primary email identifier per existing user. The
`NOT EXISTS` guard makes the statement **idempotent** — a re-run inserts nothing.
`login_enabled`, `recovery_enabled`, and `notification_enabled` are all set (in
v2 the single `users.email` was the universal login, recovery, and notification
address; OAuth-only users get a login-enabled email too — it is still their
discovery/recovery address and a passwordless-login claim).

**`verified_at` proxy caveat.** v2 recorded only a boolean `email_verified`, not
a proof timestamp. A verified user's identifier is backfilled with `updated_at`
as the best-available verification time; an unverified user gets `NULL`. This
preserves verification **state** exactly; the verification **timestamp** is an
approximation (acceptable for lifecycle/risk policy). A host that kept a truer
verification time elsewhere may substitute it in the `CASE` expression.

pgx:

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

SQLite / libSQL:

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

### Step 4 — Validate before proceeding

Every check must pass before Step 5. The count-parity check must be equal; every
other query must return **zero** rows. (Column/table names are the v2 auth
schema; adapt if a host renamed anything. On SQLite/libSQL use `is_primary = 1`
and `(login_enabled = 1 OR recovery_enabled = 1)` in the predicates.)

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

Sessions carry no identifier binding in v2, so no session is invalidated by the
backfill (identifier row IDs are newly generated and nothing is bound to them
before v3).

### Step 5 — Add auth/session metadata and the new flow tables

Additive. `users.auth_revision` is the optimistic-serialization anchor; the
session metadata columns back the recent-primary-login shortcut; the four flow
tables need no backfill (they start empty).

> **`delivery_jobs` is obsolete at/after the AV3D delivery refactor
> (2026-07-13).** The `delivery_jobs` CREATE below reflects the auth-v3 schema as
> shipped through AV3D-5.0; the retained DDL preserves the validation record. Auth
> no longer owns a delivery table — durable delivery is the generic **jobs**
> feature's schema and `in_process` delivery is ephemeral (see "Migrations are
> host-owned" in `features/authentication/README.md`). A host adopting auth at or
> past the delivery refactor **skips the `delivery_jobs` CREATE here** and wires
> delivery per the **Auth delivery-runtime upgrade runbook** below; a host that
> already created `delivery_jobs` under an earlier v3 cut drains and drops it via
> that same runbook.

pgx:

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

SQLite / libSQL — `ALTER TABLE ADD COLUMN` has no `IF NOT EXISTS`; run each
`ADD COLUMN` once (the host migration runner already tracks applied files):

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

### Step 6 — LATER: cutover / drop (only after v3 is stable)

Run this only after the v3 binary has been confirmed healthy **and** the
recovery flows that replaced the verification rail are verified end to end
(registration email verification and forgot/reset password both complete on the
v3 challenge rail — the `challenges` table, with delivery drained through the
runtime the host wired: `delivery_jobs` for a host still on a pre-refactor cut, or
the generic jobs / `in_process` runtime past it — and the Step-4 parity checks
still hold). The legacy `verification_codes` / `verification_tokens`
tables are inert to the v3 binary; drop them only once that cutover succeeds.
This step is the point of no return for a v2-binary rollback.

pgx (`ALTER TABLE ... DROP COLUMN` is a metadata operation on Postgres):

```sql
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users DROP COLUMN email_verified;
DROP TABLE verification_codes;
DROP TABLE verification_tokens;
```

SQLite / libSQL — dropping columns is the standard 12-step table rebuild (more
portable than relying on a specific `DROP COLUMN`-capable engine version); wrap
it in one transaction with foreign keys off:

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

After Step 6 the host's `users` table matches the final canonical v3 shape
(`id, display_name, auth_revision, created_at, updated_at`) — the same
legacy-column removal, reached additively instead of by a blind canonical copy.

### Step 7 — Forward-only recovery and the no-blind-copy warning

- **Forward-only.** There is no down-migration that deletes backfilled identifier
  rows. If Steps 1–5 must be abandoned, restore the Step-1 backup or redeploy the
  v2 binary (Steps 1–5 are inert to it). If Step 6 has run, recovery requires a
  restore from the Step-1 backup — the v2 binary cannot read the rebuilt `users`.
- **Never blind-copy a canonical migration** onto a live v2 database. The
  canonical trees are greenfield/final-shape; this additive, backfill-first,
  validated procedure is the only supported path from a v2 database.
- **Never auto-resolve a collision.** A non-empty Step-1 dry-run stops the upgrade
  for a human decision.

### Runtime caveats (carried from live conformance)

These do not affect the migration DDL; they are runtime/parity behaviors a host
operator must know.

1. **turso hosts must run the connector with the write-intent transaction fix.**
   The step-up credential/identifier CAS rails (`Apply`, `ApplyVerifiedChange`)
   require the turso connector's `BEGIN IMMEDIATE` write-intent transactions
   (`integrations/datastores/turso/tx.go`). An older connector using default
   `DEFERRED` transactions returns `SQLITE_BUSY` to the CAS loser instead of
   `sdk.ErrConflict` under concurrency. Data integrity is never at risk either
   way (no double-commit), but a host on the pre-fix connector fails the
   concurrent step-up contract.
2. **pgx byte-order pagination parity requires a `C`-collation database.** An
   `en_US.utf8` Postgres host pages same-`created_at` lists in linguistic order,
   which diverges from SQLite/libSQL `BINARY` byte order on the id/subject/resource
   tiebreak. This is a pre-existing, parked finding in the shared
   `integrations/datastores/pgxdb` pagination helper — **not** fixed by this
   runbook. A host that needs cross-dialect byte-order pagination parity should
   run Postgres with `LC_COLLATE 'C'`. It does not affect any v3 rail or the
   migration itself.

### AV3-9.2 execution record

Executed 2026-07-13 against fresh/reset databases in the authorized playground
containers, both dialects, all four fixture paths; every fixture torn down after
the run (the long-lived conformance databases were never touched). Fixtures:
verified+password, unverified+password, OAuth-only (no password row),
password-only, and an un-normalized duplicate-collision pair.

- **pgx clean path** — fresh `C`-collation database (`TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'`), v2-shape seed (4 users / 3 passwords / 1 oauth /
  1 session / 1 invitation / 1 verification-code / 1 verification-token). Steps
  1–5: collision dry-run **0 rows**; Step-3 backfill **`INSERT 0 4`**; Step-4
  **parity 4/4**, 0 users missing a primary email, 0 duplicate auth-claims,
  0 orphan passwords/oauth/sessions. Step-3 **re-run `INSERT 0 0`** (idempotent,
  identifier count still 4). Verification state preserved exactly (unverified →
  `verified_at NULL`, all others non-NULL). Step-6 `DROP COLUMN email` /
  `email_verified` + `DROP TABLE verification_codes/verification_tokens` clean;
  `users` left at `id, display_name, created_at, updated_at, auth_revision`.
  AFTER: passwords/oauth/sessions/invitations byte-identical to BEFORE, session
  metadata columns present and defaulted. **PASS.**
- **pgx collision path** — fresh `C`-collation database with an un-normalized
  duplicate (` Verified@Example.com ` vs `verified@example.com`): Step-1 dry-run
  **returned `verified@example.com` with both user ids** (abort signal); forcing
  the Step-3 backfill anyway **failed on `idx_user_identifiers_auth_claim`**
  (`duplicate key value violates unique constraint`) and left `user_identifiers`
  **empty** (0 rows — no partial migration). **PASS (expected failure observed).**
- **SQLite/libSQL clean path** — executed against the live libsql server
  (`http://127.0.0.1:8080`, `POST /v2/pipeline`) in an isolated table namespace so
  the standing conformance schema was untouched; identical runbook SQL. Steps 1–5:
  dry-run **0 rows**, backfill **4 rows**, parity **4/4**, 0 missing-primary,
  0 duplicate auth-claims, 0 orphans; re-run **0 inserted** (idempotent); Step-6
  **12-step table rebuild** left `users` at the final v3 shape with identifiers
  intact and both verification tables dropped; AFTER counts preserved
  (users/passwords/oauth/sessions/invitations/identifiers = 4/3/1/1/1/4),
  verification state exact. **PASS.**
- **SQLite/libSQL collision path** — live libsql server, un-normalized duplicate:
  dry-run detected `verified@example.com` (x2); forced backfill **aborted**
  (`UNIQUE constraint failed: user_identifiers.kind, normalized_value`) with
  **0 rows** written. **PASS (expected failure observed).**

**Do not apply this runbook to segovia or another real host in this milestone.**

## Auth delivery-runtime upgrade runbook (bespoke auth outbox → generic jobs / in_process)

A **host-owned** procedure for a database already running auth-v3 as shipped
through the AV3D-5.0 cut — i.e. one that scaffolded and populated the bespoke
`delivery_jobs` outbox table — that is upgrading to auth at or past the AV3D
delivery refactor (2026-07-13). After the refactor auth owns **no** delivery
table: durable delivery is the generic **jobs** feature's `fenced_job_queue`
(host-owned in the jobs migration tree) and `in_process` delivery is a bounded
ephemeral pool with no table. `Repositories.DeliveryJobs`, the private
claim/poll/purge worker, and `Service.RunDeliveryWorker` are gone; the send path
is now `Config.DeliveryMode` + a host-wired `Config.DeliveryDispatcher` (jobs
mode) or `Service.RunDelivery(ctx)` (`in_process`).

Per the standing greenfield-migrations rule (2026-07-12) this is **not** a
canonical migration — the canonical auth set ships the final schema only
(`0001–0013`, no `delivery_jobs`). This runbook is host-side prose; every DDL
step below runs from the host's **own** migration tree, pre-boot, exactly like
every other host-owned migration.

**A host with an EMPTY or absent `delivery_jobs` table has nothing to drain** —
skip Steps 1–2/4, apply the new host wiring (Step 3), drop the empty table
(Step 5), and start the chosen runtime (Step 6).

**Tooling never decrypts a payload.** Every `delivery_jobs.payload` (and every
`fenced_job_queue.payload`) is an opaque, AES-GCM-sealed `command.Envelope`
(`AUTH_DELIVERY_ENCRYPTER_KEY`) carrying the rendered secret and destination. No
step here — count or drop — opens that ciphertext. Only the running auth delivery
processor, holding the encrypter key, ever unseals it: during this upgrade that is
the OLD binary's worker as it drains.

The drain is the **single supported path**. A prior draft offered an opaque
export/re-enqueue copy of the bespoke rows into the generic queue; that path is
unsafe and has been removed — the legacy ciphertext encodes the removed bespoke
envelope shape (not the generic queue's versioned command), the legacy rail kinds
(`email`/`phone`) are not the registered `authentication.delivery` job kind the
generic runtime dispatches on, the copy never terminalized its source rows (so the
zero-source-count check could never pass honestly), and the libSQL variant minted
`datetime('now')` strings the turso connector's fixed-width `Time.Scan` cannot
parse. An opaque copy cannot work in either dialect. Drain-then-drop is the only
supported procedure off the bespoke outbox.

### Preconditions

- A confirmed, restorable **backup** taken before Step 1.
- The OLD binary retains its existing **`AUTH_DELIVERY_ENCRYPTER_KEY`** through the
  drain: it is the only process that unseals `delivery_jobs` payloads and must hold
  the key that sealed them. No payload crosses queues (the drain is in place), so no
  cross-queue key portability is required; the NEW binary seals its own newly
  admitted work under its own key.
- **Do not rotate the delivery key mid-drain** — the old worker cannot unseal
  in-flight rows that were sealed under the previous key.

### Deploy ordering (single cutover — do NOT roll)

Old and new binaries must not both serve the same database across the cutover:
the old binary claims `delivery_jobs`, the new binary has no code that reads it.
Quiesce the old binary's admission, drain, upgrade, then start the new runtime.

### Step 1 — Stop old delivery workers and quiesce admission

Stop the old binary's delivery worker loop (`Service.RunDeliveryWorker`) — or
stop the old binary outright — and quiesce admission so no NEW `delivery_jobs`
rows are written (drain traffic to the start endpoints, or take the maintenance
window). No new opaque command lands in the bespoke table from this point.

### Step 2 — Drain the pending encrypted commands

Keep the OLD binary's delivery worker running (admission still quiesced from
Step 1) until it processes every non-terminal row: `state = 'pending'` rows are
sent or retried to their terminal state (`succeeded`/`failed`/`canceled`), and
terminally undeliverable rows discard their bound challenge best-effort. A leased,
in-flight row is still `state = 'pending'` (the lease is `lease_id`/`leased_until`,
not a separate state), so it counts as non-terminal until the worker terminalizes
it. When the pending count reaches zero (Step 4), upgrade.

The drain preserves at-least-once semantics and never decrypts a payload in
tooling: only the old worker, holding the encrypter key, unseals a row, and it
does so on the normal send path.

- *Tradeoffs.* Requires the old binary + worker to keep running through the
  drain; the drain is bounded by the old queue's retry/backoff horizon (a row in
  long backoff delays completion; you may let it dead-letter rather than wait).
  No encryption handling, no key coupling, no logical-key bookkeeping, no payload
  movement. A large dead-letter/backoff backlog only means a longer drain window,
  not a different path — there is no supported alternative to the drain.

### Step 3 — Apply the generic jobs schema and new host wiring

- **jobs mode (recommended production posture).** Scaffold the generic jobs
  migration tree into the host (`jobsstore.ExportMigrations` from
  `features/jobs/stores/{pgx,turso}`; canonical set `0001_job_queue`,
  `0002_job_schedules`, `0003_fenced_job_queue` — identical filename set across
  both dialects) and apply it pre-boot. Wire `Config.DeliveryMode = "jobs"`, a
  `Config.DeliveryDispatcher` backed by the generic jobs feature, and set the
  `DeliveryJobsAcknowledged` wiring assertion (production requires it —
  `ErrDeliveryJobsUnacknowledged`). A composition adapter (never a feature core)
  bridges auth's `Service.DeliveryJobRuntime()` onto `jobs.Runtime`.
- **in_process mode.** No jobs schema is needed (the bounded pool owns no table).
  Set `Config.DeliveryMode = "in_process"`, keep `Config.DeliveryEncrypter`, and
  set `DeliveryEphemeralAcknowledged` (production requires the crash-loss
  acknowledgment — `ErrDeliveryEphemeralUnacknowledged`). Accepted work does NOT
  survive a restart, so the bespoke outbox must be fully drained (Step 2) before
  cutover — there is no supported path that moves a durable backlog into an
  ephemeral queue.

`Register` starts no runtime in either mode; the host runs the selected runtime
in Step 6.

### Step 4 — Verify no active auth delivery rows remain

Before dropping the table, confirm the bespoke outbox holds no live work. The
active-work count MUST be zero:

pgx / SQLite / libSQL:

```sql
-- Active (unprocessed) bespoke delivery rows — MUST be 0 before Step 5.
SELECT count(*) AS active_delivery_jobs FROM delivery_jobs WHERE state = 'pending';
```

The Step-2 drain drives this to 0: once every `state = 'pending'` row (including
leased, in-flight rows) has reached a terminal state, the count is exact and no
row is lost or duplicated (terminal rows are retained with `terminal_at` set until
Step 5 drops the table). A non-zero active count **stops the upgrade** — finish the
drain first; never drop a table with live encrypted work in it.

### Step 5 — Drop the obsolete `delivery_jobs` table (host-owned)

Once Step 4 shows zero active rows, remove the bespoke table from the host's own
migration tree. This is a host-owned destructive migration, not a canonical one.

pgx:

```sql
DROP TABLE IF EXISTS delivery_jobs;
```

SQLite / libSQL (`DROP TABLE` also drops the table's indexes):

```sql
DROP TABLE IF EXISTS delivery_jobs;
```

This is the point of no return for reading any residual bespoke row; take it only
after Step 4 is clean (zero active rows).

### Step 6 — Start the generic jobs runtime or the bounded runtime

- **jobs mode:** start the generic jobs runtime the host wired in Step 3
  (`go rt.Run(ctx)` for the composed `jobs.Runtime`); cancel the ctx to drain.
  Newly admitted commands now process on the durable fenced queue.
- **in_process mode:** start the bounded pool with `go authSvc.RunDelivery(ctx)`
  for the process lifetime; cancel the ctx for a bounded shutdown drain.

Confirm end to end that a fresh start endpoint (register verification,
forgot-password, passwordless start) delivers OFF the request path on the new
runtime before reopening admission.

### Forward-only recovery and the no-decrypt / no-blind-copy warnings

- **Forward-only.** There is no down-migration. If the upgrade must be abandoned
  before Step 5, redeploy the old binary (it still reads `delivery_jobs`); after
  Step 5 recovery requires the Step-1 backup.
- **Never decrypt a payload in tooling.** The count and drop steps treat
  `payload` as opaque bytes. Only the running processor with the encrypter key
  unseals it — during this upgrade that is the old worker draining the outbox.
- **Never blind-copy a canonical migration.** The canonical auth set is
  greenfield/final-shape and carries no `delivery_jobs`; this host-owned
  drain-then-drop procedure is the only supported path off the bespoke outbox.
- **`in_process` is ephemeral.** Accepted work does not survive a restart, so
  drain the bespoke outbox before cutover; there is no supported copy of a durable
  backlog into an ephemeral queue.

This delivery-runtime runbook has **not** been applied to a real application host
(no auth module tag has been cut — `git tag -l` is empty, so the greenfield
rewrite that removed `delivery_jobs` from the canonical set is allowed with no
append-only constraint).

### AV3-9.8 drain-path fixture verification (both dialects)

The drain-only path's Step-4 verification query and source-row accounting were
proven on disposable fixtures on both dialects (IX-01 remediation). Each fixture
seeds legacy-shape `delivery_jobs` rows in mixed states, runs the Step-4 count,
terminalizes the remaining non-terminal rows exactly as a drained worker would,
and re-runs the count — proving it reaches 0 with no row lost or duplicated.

- **pgx** — disposable database `dr_drain_fixture` (`TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'`), legacy `delivery_jobs` created from the shipped
  `0014` shape. Seed: 5 rows — 2 `pending` (one unleased, one leased/in-flight),
  1 `succeeded`, 1 `failed`, 1 `canceled`. Step-4 count → `active_delivery_jobs =
  2` (the leased in-flight row counts as pending — exact). Drained-worker
  terminalize (`UPDATE … SET state='succeeded', terminal_at=now(), lease_id='',
  leased_until=NULL WHERE state='pending'`) → `UPDATE 2`. Step-4 re-count → `0`.
  Total accounting: `SELECT count(*)` = 5 before and after (no row lost or
  duplicated); terminal breakdown afterward `succeeded=3, failed=1, canceled=1`.
  Fixture database dropped afterward.
- **turso/libSQL** — live server, isolated `dr_`-prefixed fixture table
  `dr_delivery_jobs` created from the shipped turso `0014` shape (fixed-width TEXT
  timestamps). Same 5-row mixed-state seed via `POST /v2/pipeline`. Step-4 count →
  `2`. Terminalize (`… SET state='succeeded', terminal_at=<fixed-width ISO>,
  leased_until=NULL WHERE state='pending'`) → 2 rows affected. Step-4 re-count →
  `0`. Total accounting: 5 rows before and after; terminal breakdown `succeeded=3,
  failed=1, canceled=1`. All `dr_`-prefixed fixture tables dropped afterward; the
  standing conformance schema was untouched.

Both container databases were left running; no canonical/conformance table was
modified.

## What this repo is not doing (yet)

- No CI-driven automated tagging — tags are cut by hand until a release
  workflow is built.
- No changelog convention is mandated yet; the tag message plus commit log is
  the record until one is adopted.
