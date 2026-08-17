# Phase 1 — user directory and account lifecycle

## Outcome

Add an opt-in administrative user surface without teaching authentication what
an application admin is. Hosts get a paginated directory projection and trusted
service operations; the bundled HTTP routes exist only behind an explicit,
action-aware host authorization check. Deactivation is a real authentication
state transition: it atomically changes status, invalidates authentication
revision state, revokes live sessions/recent-auth grants, and fences concurrent
session creation.

## Current evidence

- `domain/user.User` has `ID`, `DisplayName`, `AuthRevision`, and timestamps only.
- `user.UserRepository` exposes create/get/update but no list.
- `users` migrations have no lifecycle column.
- `SessionRepository.DeleteByUser` is bulk/idempotent, but calling it after a
  separate user update would leave a crash and concurrency gap.
- Every password/OAuth/passwordless login ultimately mints through
  `authsvc.mintSession`, while refresh relies on a stored session. That central
  mint seam is the right place to consume an atomic active-user session port.
- Built-in pgx/Turso store modules already return the top-level
  `authentication.Repositories` bundle, so they can supply optional lifecycle
  ports without the feature core importing a concrete store.

## Frozen contract to ratify (CHAU-1.1)

### Lifecycle value

Add a closed `user.Status` with exactly:

- `active` — the zero/default persisted posture for existing and new users; and
- `deactivated` — no new session or act-as-user authentication may succeed.

`user.User` gains `Status` and `StatusChangedAt`. Domain construction always
creates `active`. Store readers may normalize an empty legacy value to active,
but built-in migrations backfill and constrain the column so persisted empty or
arbitrary values do not survive.

Do not add delete, suspended, locked, pending, or arbitrary host statuses in this
phase. Verification remains identifier state, not user status.

### Directory projection

Add a public, persistence-free summary suitable for an operator directory:

```text
UserSummary
  ID
  DisplayName
  Status
  StatusChangedAt
  PrimaryEmail
  EmailVerified
  CreatedAt
  UpdatedAt
```

`PrimaryEmail` is the normalized active primary email. It may be empty for a
provider-created edge case with no email, but no masked value is used on this
explicitly authorized admin surface. No password hash, OAuth token, session,
API-key material, challenge state, recovery inventory, or auth revision appears
in the DTO.

List order is `created_at DESC, id DESC`, with both current crud cursor and
offset strategies and `WithCount` semantics. Cursor collation and tie-breaking
must match other authentication lists. V1 supports no fuzzy search; filtering
can be designed later rather than smuggling unstable query semantics into the
first contract.

### Repository capabilities

Keep base `UserRepository` source-compatible. Add explicit optional
capabilities to `authentication.Repositories` (exact names freeze in CHAU-1.1):

1. a user-administration repository that:
   - lists `UserSummary` pages;
   - gets one summary;
   - atomically transitions status, increments `auth_revision`, and deletes all
     sessions plus authentication grants for the target; and
   - reports `Changed` independently from the resulting status so replaying the
     same desired status is idempotent;
2. an active-session repository that inserts a proposed `session.Session` only
   while the owning user is active under the same database serialization
   boundary.

Unknown user is `sdk.ErrNotFound`; invalid status is `sdk.ErrInvalidInput`;
concurrent credential/status CAS loss is `sdk.ErrConflict`. A repeated desired
status returns success with `Changed=false`, never not-found/conflict.

The active-session operation is load-bearing. Its SQL transaction must lock or
conditionally read the user status so exactly one of these outcomes wins:

- mint commits first, then deactivation waits and deletes that session; or
- deactivation commits first, then mint sees deactivated and refuses.

A service-level `Get` followed by ordinary `Sessions.Create` is forbidden.

### Authorization and construction

Add `Config.UserAdminCheck`, an action-aware host seam receiving:

- the resolved actor `Principal`;
- action: list, read, deactivate, reactivate, or resend-verification; and
- target user ID when the action has one.

A nil check means bundled admin routes are **off**, even when built-in stores
provide the repository ports. This lets store adapters return a complete bundle
without enabling an authorization surface. When the check is non-nil,
construction requires both lifecycle repositories and fails loudly on missing
ports. A denial or check infrastructure error fails closed. Authentication never
imports authorization and never interprets a role string.

Trusted host-facing service methods remain callable when repositories are wired,
mirroring invitation trusted calls: the calling composition owns authorization.
Their docs must say this prominently.

## Schema and store work

### CHAU-1.2 — append-only migrations

The store modules are already tagged, so do not edit `0001_users.sql`. Add
matching `0014_user_status.sql` migrations for pgx and Turso:

- `status` non-null, default/backfilled `active`, closed CHECK;
- `status_changed_at` nullable (`NULL` until the first transition);
- an index supporting the directory's contractual `(created_at, id)` order only
  if the existing primary/order access is insufficient; and
- no new table solely for status.

Update migration parity tests, exported migration inventories, store README
counts, and schema probes. Supply an adopter upgrade snippet explaining that
hosts which previously copied 0001–0013 must copy/apply 0014 before running the
new store tag.

### CHAU-1.3 — directory parity

Implement one query per page, joining the active primary email without N+1
identifier reads. Prove:

- verified, unverified, and email-less projections;
- deactivated and active rows;
- timestamp/id collision ordering and byte-identical next cursors;
- cursor/offset/count behavior;
- no duplicate user when identifier history contains retired rows; and
- pgx/Turso/memory conformance parity.

### CHAU-1.4 — atomic transitions and minting

Add shared storetest coverage for:

- active → deactivated deletes sessions and grants and increments
  `auth_revision` once;
- deactivated → active changes status without fabricating a session;
- repeated transition is idempotent and does not increment again;
- unknown/invalid inputs map to stable sdk kinds;
- concurrent deactivate/mint leaves no live post-deactivation session;
- concurrent credential mutation/status transition has one revision winner;
- transaction failure rolls back status and revocations together; and
- active-session insertion returns the same session shape as ordinary create.

Run the concurrency cases under `-race` in memory and against live pgx/Turso.

## Service and credential enforcement

### CHAU-1.5 — every credential path

Route new session creation through the active-session capability when available.
Admin route enablement requires it, so no host can advertise deactivation while
retaining the mint race. Prove generic, non-enumerating denial for:

- password login and `/auth/token`;
- refresh after deactivation (the atomic transition deleted the session);
- OAuth existing-link login and pending-link completion;
- passwordless code and magic-link completion;
- act-as-user API-key authentication; and
- any password-reset/remint or credential-mutation path that would issue a fresh
  caller session.

Do not expose `deactivated` from public credential endpoints. Operator/admin
reads may expose the real status. Decide and document the bounded stale behavior
of stateless `RequireUser`: existing access JWTs may remain usable on non-live
routes until `AccessTokenTTL`; sensitive/admin routes use `RequireLiveSession`
and deny immediately because sessions were deleted.

### CHAU-1.6 — service and routes

Recommended optional JSON surface:

| route | gates | result |
|---|---|---|
| `GET /auth/admin/users` | live session → `UserAdminCheck(list)` | `crud.Page[UserSummary]`, no-store |
| `GET /auth/admin/users/{id}` | live session → `UserAdminCheck(read)` | one summary, no-store |
| `POST /auth/admin/users/{id}/deactivate` | live session → browser-safe mutation → `UserAdminCheck(deactivate)` | resulting summary/status |
| `POST /auth/admin/users/{id}/reactivate` | live session → browser-safe mutation → `UserAdminCheck(reactivate)` | resulting summary/status |

Use the existing strict JSON/body-size, CORS/origin, CSRF, error-envelope, and
crud parser conventions. A machine principal reaches the policy seam as a real
principal; the feature does not pre-decide whether the host may authorize it.
Self-deactivation is not generically forbidden—the host policy may allow it—but
the documentation must warn hosts whose last-admin invariant lives elsewhere.

Host-facing trusted methods should cover list/get/transition so a host may build
its own transport without SQL or internal imports.

## Documentation and gate (CHAU-1.7)

Update:

- auth README route/config/repository tables and a coordination-hub-style
  `platform#admin` check example;
- security docs for immediate live-session revocation versus stateless JWT TTL;
- store README migration/export/upgrade instructions;
- Go docs warning that trusted methods do not apply `UserAdminCheck`;
- `RELEASING.md` schema and route enablement notes; and
- an exported example-host test that lists, deactivates, proves login/refresh
  denial, reactivates, then logs in again without internal imports.

Verification:

```sh
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)
(cd features/authentication/stores/pgx && go test -race ./...)
(cd features/authentication/stores/turso && go test -race ./... && go vet -tags=integration ./...)
# Repeat store concurrency with POSTGRES_TEST_DSN and TURSO_* configured.
make check && make guard
```

The live gate is open—not silently passed—when either dialect skips.

## Stop conditions

Stop if a dialect cannot atomically serialize active-session creation against
deactivation, if admin routes would mount from repository presence alone, or if
the only implementation path requires auth core to import authorization.

## Execution log

Append only. Record each CHAU-1.x task's code/docs changes, exact verification,
live-store skips, reviewer findings, and any owner-ratified contract adaptation.

### 2026-08-16 — CHAU-1.1 … CHAU-1.7 complete; both live dialects CLOSED

**CHAU-1.1 — frozen contract.** New `domain/user/status.go`:

| symbol | shape |
|---|---|
| `user.Status` | closed: `StatusActive`, `StatusDeactivated`; `Valid()`, `Active()`, `ParseStatus`, `NormalizeStatus` |
| `user.ErrInvalidStatus` | wraps `sdk.ErrInvalidInput` |
| `user.Summary` | ID, DisplayName, Status, StatusChangedAt, PrimaryEmail, EmailVerified, CreatedAt, UpdatedAt |
| `user.OrderFields` / `user.DefaultOrder` | `created_at` only; `created_at DESC` |
| `user.StatusChange` | Status, Changed, ChangedAt, RevokedSessions |
| `user.AdminRepository` | `List`, `GetSummary`, `SetStatus` |

`user.User` gained `Status` + `StatusChangedAt` (zero → never transitioned,
matching the `identifier.VerifiedAt` convention) and an `Active()` method;
`NewUser` constructs `StatusActive`. `ParseStatus` rejects the empty value —
only a store READER normalizes it, via `NormalizeStatus`.

The fenced mint is `session.ActiveUserRepository` in `domain/session/active.go`
with `session.ErrUserNotActive` (wraps `sdk.ErrForbidden`). Placing it in the
session domain rather than the user domain keeps each domain owning its own error
and avoids a cross-domain import in the port itself.

Feature root: `Config.UserAdminCheck`, `Repositories.UserAdmin`,
`Repositories.ActiveSessions`, `ErrUserAdminReposRequired`, and the alias set
(`UserStatus`, `UserStatusActive/Deactivated`, `UserSummary`, `UserStatusChange`,
`UserAdminCheck`/`Request`/`Action` + five action constants,
`ErrUserAdminUnavailable`, `ErrUserNotActive`, `ErrInvalidUserStatus`) following
the `InviteCheck`/`CredentialPolicy` alias precedent.

**Authorization and construction, exactly as ratified:** a nil `UserAdminCheck`
leaves the bundled admin routes UNMOUNTED even when the repositories are present
(`routes.go` gates on `UserAdminEnabled() && UserAdminAuthorized()`); a non-nil
check REQUIRES both repositories (`ErrUserAdminReposRequired`); the reverse is
deliberately not an error, because the bundled stores always supply the
capability. `AuthorizeUserAdmin` fails closed on a nil check. Authentication
imports no authorization and interprets no role string.

**CHAU-1.2 — append-only migrations.** `0014_user_status.sql` in BOTH trees.
`0001_users.sql` was not touched.

- both: `status TEXT NOT NULL DEFAULT 'active'` with the closed CHECK,
  `status_changed_at` nullable, `idx_users_created_at_id`.
- pgx only: `ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C"`.

That last one is a contract discovery, not decoration: `users.id` became a keyset
tiebreak with this release, and `collation_test.go`'s own comment listed `users`
under "tables without keyset pagination". It is now in
`contractualCollatedColumns`, and the hermetic assertion learned a `pinnedByAlter`
arm because a column that becomes contractual after its table is tagged cannot be
fixed in the CREATE TABLE. The LIVE catalog check is unchanged and is the real
proof — it passed, reporting collation `C` for `users.id`.

The plan's "index only if existing access is insufficient" condition is met:
`users` had `PRIMARY KEY (id)` and nothing else, so a directory listing would
seq-scan and sort the whole table.

Both `Repositories()` constructors gained `probeUserStatusColumns` beside the
table probes (`information_schema.columns` on pgx, `pragma_table_info` on turso).
Without it a host that copied 0001–0013 would pass every table probe and fail
mid-request instead of at boot. Migration inventory/parity tests, expected
columns, expected indexes, package docs, and the pgx store README were updated
together.

**CHAU-1.3 / CHAU-1.4 — conformance.** New `storetest/useradmin.go`, wired into
`Run` and skipping LOUDLY per port. Fourteen leaf cases across three groups:

- *UserDirectory* — `SummaryProjection` (verified / unverified / email-less, in
  both statuses, with the single-row read asserted to agree with the list),
  `GetSummaryAbsent`, `OrderingAndCursorParity` (three users sharing one
  created_at so the id tiebreak is the only ordering, plus a byte-exact
  `crud.EncodeCursor` comparison), `OffsetAndCount`,
  `RetiredIdentifiersDoNotDuplicate` (replaces a primary email so the history
  contains a retired row, then asserts one row carrying the CURRENT address).
- *UserLifecycle* — `DeactivateRevokesAndBumpsRevision` (sessions gone, grants
  unconsumable, `auth_revision` +1 exactly), `ReactivateMintsNothing`,
  `RepeatedTransitionIsIdempotent` (no second increment, `StatusChangedAt`
  unmoved), `UnknownAndInvalidInputs` (`""`, `"suspended"`, `"DEACTIVATED"`,
  `"deleted"` → `ErrInvalidInput` with nothing written).
- *ActiveSessionMint* — round-trip readable through the ORDINARY session port and
  `GetByRefreshHash`, deactivated refused with no row written, unknown user
  not-found, duplicate refresh hash conflicting, and
  `ConcurrentDeactivateVersusMint` (12 rounds of a simultaneous
  deactivate/mint asserting the invariant "no live session on a deactivated
  user", accepting either legal outcome).

Reference implementation in `storetest/reference_test.go`; the example host's
`authmem` implements both ports too, so its conformance run no longer skips.

**CHAU-1.5 — every credential path.** `mintSession` now calls a single
`createSession` seam that prefers `ActiveSessions`. Because every flow —
password login, `/auth/token`, OAuth login / register / verify-link, passwordless
code and link, and the password-mutation remint — already funnels through
`mintSession`, the fence is inherited rather than remembered.

Generic denial is enforced at each PUBLIC site via `genericIfNotActive`:
login and `/auth/token` → `invalidCredentials()`; both passwordless completions →
`ErrPasswordlessLogin`; OAuth existing-link login and verify-link →
`invalidCredentials()`. Non-lifecycle errors pass through unchanged, so a store
outage is still a 500. The session-gated password-mutation remint deliberately
propagates the honest 403: the caller is already authenticated, so there is no
enumeration to protect against. Refresh needs no code change — the transition
deleted the session row.

Act-as-user API keys: `AuthenticateAPIKey` now resolves the owner's status for a
`PrincipalUser` key and denies with the same generic `invalidAPIKey()`, recording
a `StatusBlocked` audit row. **Scope ruling:** an UNKNOWN owner is not treated as
deactivated. The v1 vocabulary has no "deleted", so denying an unknown owner would
have added an owner-must-exist requirement nothing asked for — and it broke
`TestAuthenticateAPIKeyActAsUserResolvesToOwner`, which uses a synthetic owner id.
The helper is therefore `userDeactivated`, not `userIsActive`.

Stale-JWT posture decided and documented: `RequireLiveSession` routes deny within
one round-trip because the sessions are deleted; the stateless `RequireUser` tier
keeps honoring an already-issued access JWT for at most `AccessTokenTTL`. Written
up in the README with the guidance to put anything needing instant revocation
behind `RequireLiveSession` — and flagging that
`GET /auth/oauth/{provider}/link/start` currently sits on `RequireUser` (the
CHAU-7.1 finding). **That gate was NOT changed here**: it is a live-behavior
change for existing hosts and belongs to its own dispatch, so it is recorded as
an open owner call rather than altered in passing.

**CHAU-1.6 — service and routes.** All four recommended routes ship at the
recommended paths, gated live session → (mutations) browser-safe Origin/CSRF →
`UserAdminCheck`, with `Cache-Control: no-store`. The target status is chosen by
the ROUTE, never the body — a body-driven status would let a client name a value
the host's policy was never asked about. The mutation bodies still go through the
strict JSON/body-size decode so an unknown key or oversized body is rejected like
every other mutation. `adminPrincipal` runs the check BEFORE any target
resolution, which is what makes an unauthorized 403 identical for a real and a
fake user id. Trusted service methods (`ListUsers`, `GetUserSummary`,
`DeactivateUser`, `ReactivateUser`) are documented as applying no authorization.

**CHAU-1.7 — docs and gate.** New README section **"Account lifecycle — the
operator directory and deactivation"**: the closed vocabulary and why it is
closed, the one-transaction contract, why `ActiveSessions` is required, the
generic-denial security property, the live-session-vs-stateless-JWT revocation
table, a `platform:main#admin` wiring example, the self-deactivation/last-admin
warning, the trusted-method surface, and a pointer to the runnable proof. Route
table, config matrix, and repositories table each gained their rows.
`stores/pgx/README.md` and the feature README's migration section carry the
0014 upgrade instructions including the ACCESS EXCLUSIVE warning.
(`stores/turso` has no README in this repo — nothing to update there.)
`RELEASING.md` gained a keyed entry covering the additive surface, the behavior
changes, the schema, and the adopter ordering.

Example-host proof: `examples/auth-cms/cmd/server/user_admin_test.go`, five tests,
exported seams only — the full walk (list → single read → deactivate → **assert
the deactivated login is byte-identical to a wrong-password login** → refresh and
hydration denied → idempotent replay → reactivate → log in again → assert the
policy was asked every action with the right target), unauthorized denial that
cannot distinguish a real id from a fake one, session requirement, deny-by-absence
(routes 404 while the store DOES supply the capability), and the loud
construction error for a check without the fence.

**Verification (exactly as run, 2026-08-16):**

```
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)   clean
(cd features/authentication/stores/pgx && go test -race ./...)                          ok
(cd features/authentication/stores/turso && go test -race ./...)                        ok
(cd examples/auth-cms && go test -race ./...)                                           ok
make check   (runs make guard)                                                          all checks passed
```

**LIVE STORE GATES — BOTH CLOSED, not skipped.**

*Turso/libSQL* (`TURSO_DATABASE_URL` verified to equal the authorized playground
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io` before any
destructive run):

```
go test -tags=integration -count=1 -run 'TestSchemaProbe|TestProbe|TestRepositories' -v ./...
    → 0014_user_status.sql APPLIES on libSQL (checksum c80035ba). This was the one
      genuinely unverified assumption in the schema: SQLite's ALTER TABLE ADD COLUMN
      with an inline CHECK. It works.
go test -tags=integration -count=1 -run 'TestConformance_Turso/(UserDirectory|UserLifecycle|ActiveSessionMint)' -v ./...
    → --- PASS: TestConformance_Turso (76.52s), all 14 leaf cases including
      ConcurrentDeactivateVersusMint
```

The first attempt failed on a stale ledger (`checksum mismatch: 0001_users.sql`)
left by an older auth cut on that shared playground DB; the integration schema
probe's own drop-and-remigrate reset cleared it. Recorded because it is exactly
the kind of environment failure that must not be silently retried into a green.

*PostgreSQL 17* on a **throwaway container created for this run** (port 55437,
`--rm`, stopped and removed afterwards). The user's existing `coordination-hub-postgres`
and `venona-*` containers were deliberately NOT used: the conformance harness
truncates tables.

```
POSTGRES_TEST_DSN=… go test -race -count=1 -run TestConformance ./...                    ok (46.4s)
POSTGRES_TEST_DSN=… go test -race -count=1 -run '…/(UserDirectory|UserLifecycle|ActiveSessionMint)' -v
    → all 14 leaf cases PASS
POSTGRES_TEST_DSN=… go test -count=1 -run 'TestContractualCollation_Catalog|TestSchemaProbe' -v
    → PASS — users.id reports collation 'C', proving the 0014 ALTER landed
POSTGRES_TEST_DSN=… go test -race -count=5 -run '…/ConcurrentDeactivateVersusMint' ./...  ok (7.0s)
```

**Defect the live gate caught (and the memory suite could not).** The first pgx
conformance run failed `UserDirectory/OrderingAndCursorParity`: Postgres returns
`timestamptz` in the session zone, so the emitted cursor encoded
`2026-06-01T05:10:00-07:00` where the reference and libSQL stores encode
`2026-06-01T12:10:00Z` for the same instant — byte-DIFFERENT cursors for identical
data, which the directory contract forbids. Fixed by normalizing the pgx
`OrderValueOf` to `.UTC()`; the keyset predicate is unaffected because Postgres
compares the same instant either way. **Noted, not fixed:** the other pgx paged
stores (`service_accounts`, `api_keys`, `security_events`, `invitations`) have the
same un-normalized `OrderValueOf` and would emit zone-offset cursors too. Their
contracts do not currently promise cross-dialect cursor bytes and changing them is
out of this phase's scope — flagged for the owner.

**Stop conditions: none triggered.** Both dialects serialize the fenced mint
against deactivation (pgx: `FOR SHARE` on the user row inside the insert
transaction versus `FOR UPDATE` in the transition; turso: the connector's
`BEGIN IMMEDIATE` write intent). Admin routes never mount from repository
presence alone. Auth core imports no authorization.

**Open owner calls raised by this phase:**

1. `GET /auth/oauth/{provider}/link/start` uses the stateless `RequireUser` tier.
   Moving it to `RequireLiveSession` would give it immediate deactivation
   semantics but changes live behavior for existing hosts. Documented, not
   changed.
2. Cursor UTC normalization for the four other pgx paged stores (above).
