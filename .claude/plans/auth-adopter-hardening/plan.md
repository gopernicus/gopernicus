# Auth adopter hardening — invitation operation integrity, host authorization, and store preflight

Status: **COMPLETE 2026-07-15 — AAH-1..6 all landed and green. Owner ratified
D1–D5 as recommended (2026-07-15, in-session; D3 = issuance-time authority).
Closeout verification (AAH-6, 2026-07-15): `make check` "all checks passed" +
`make guard` 16/16 exit 0; `make test-stores` against C-collation `aah_c`
(authv3-pg) + authv3-libsql all ten legs `ok` (auth pgx 13.826s / auth turso
1.740s / authz pgx 9.933s, no schema_migrations checksum conflict); `examples/auth-cms
go test -count=1 ./...` all seven packages `ok` (adversarial invitation proofs,
cmd/server 11.147s). Docs describe the shipped v3 shape (NOTES.md AAH closeout +
RELEASING.md keyed note). Uncommitted; no tag/PR (owner-owned release gates).**
Working name: `auth-adopter-hardening`; task prefix: `AAH`.
Triggered by: the Segovia checkout's v2 plan
`.planning/gopernicus-v2/09-auth-v3-adoption.md`, reviewing gopernicus at
`ccf38f7059d58ddea8eb95733113833a23183249` on branch `authorization-v3`.
Draft-time `git tag --list` was empty; preflight must re-check before editing.

## Outcome

Close the framework gaps exposed by the first authorization-v3/authentication-v3
adopter before Segovia carries the new APIs:

1. one authentication invitation operation maps to one authorization mutation
   identity, while a later re-invitation of the same tuple is a distinct
   operation;
2. authentication never records a grant as successful when a host adapter
   received a non-applied authorization outcome;
3. invitation creation/listing is authorized by a relation-aware host policy
   inside the feature's parsed request path, not by a RouteRegistrar wrapper;
4. every authenticated invitation route uses live-session validation, so a
   revoked session's outstanding access JWT cannot mutate or read invitation
   state;
5. the host Granter has enough context to reject acceptance against a deleted
   host resource;
6. authentication's pgx and Turso repository constructors fail loudly before
   traffic when any canonical auth table is missing;
7. PostgreSQL canonical migrations carry the byte-order collation required by
   their ordering/keyset contracts; and
8. the reference host, store READMEs, release notes, and tests describe the
   shipped v3 shape rather than the retired five-store v1 shape.

This is intended as a pre-tag breaking hardening packet. Preflight decides
whether that assumption is true; a relevant published module tag stops this
plan before any public API or canonical migration edit.

## Why this is framework work

The current auth-cms `relationshipGranter` derives its MutationID from the
resulting tuple and discards the returned authorization receipt. Authentication's
invitation table intentionally permits another pending invitation for the same
tuple after the prior row leaves `pending`. Therefore:

- invitation A grants tuple T and persists receipt M(T);
- T is later revoked;
- invitation B for T is valid authentication state;
- accepting B replays M(T), so T is not restored, while authentication records
  the grant as successful.

The same adapter returns nil for `semantic_conflict` and
`invariant_blocked` because those are receipt outcomes, not command errors.
That violates authentication's Granter contract: nil must mean the requested
relation was established or was already exactly present.

Separately, a host RouteRegistrar decorator sees the invitation path but not the
validated relation payload. It cannot prevent a dashboard editor from inviting
another subject as owner. Authentication owns parsing and knows the caller,
resource, action, and relation; it must call a host authorization seam there.

Finally, authorization stores already prove their schema at repository
construction, while authentication constructors only allocate adapters.
`auth.NewService` validates collaborators but cannot detect a missing table.
The asymmetry turns a migration omission into a first-request SQL failure.

## Current evidence

- `examples/auth-cms/cmd/server/membership.go` derives
  `auth-cms/invitation-grant` from only resource/relation/subject and drops the
  receipt.
- `features/authorization/domain/mutation/receipt.go` states that
  `semantic_conflict` and `invariant_blocked` are non-persisted outcomes.
  The auth-cms comment incorrectly calls one-relation conflict persisted.
- `features/authentication/stores/{pgx,turso}/migrations/0009_invitations.sql`
  uses a partial pending-only uniqueness constraint, explicitly allowing a new
  invitation after the prior row changes state.
- `features/authentication/internal/logic/invitationsvc.Granter` carries only
  tuple fields; it carries no logical operation ID.
- `features/authentication/stores/{pgx,turso}.Repositories` return repository
  bundles with no error or schema probe.
- The pgx store README and pgx package comment still claim five stores /
  migrations `0001…0005` and plaintext session/code/token persistence.
- Authentication and authorization pgx pagination/order contracts assume
  byte-wise text collation, but their canonical DDL does not put
  `COLLATE "C"` on contractual text keys.
- Every authenticated invitation route currently mounts with `RequireUser`.
  That is a stateless access-token gate: create/accept/cancel/resend remain
  usable until access-token expiry after session revocation, while resource and
  mine listing expose invitation identifiers under the same stale credential.

## Decisions for ratification

### D1 — structured, operation-scoped Granter input

Recommendation: replace the positional Granter method with a structured request:

```go
type GrantInput struct {
    OperationID string
    ResourceType string
    ResourceID string
    Relation string
    SubjectType string
    SubjectID string
}

type Granter interface {
    Grant(context.Context, GrantInput) error
}
```

`OperationID` is opaque, non-empty, and identifies THIS logical invitation
grant. It is not authority and need not be secret.

- Pending accept and resolve-on-registration use the persisted invitation ID.
  A retry of the same invitation reuses the same operation ID.
- Direct-add has no invitation row; mint one non-empty, high-entropy operation
  ID immediately before invoking the Granter. Do NOT use `Config.IDs` for this:
  its supported `cryptids.Database` strategy intentionally yields an empty ID
  until an entity is inserted, and direct-add inserts no entity. A retried HTTP
  create may mint another operation ID, but an exact existing tuple must still
  resolve as the host's idempotent no-change success.
- A later invitation row for the same tuple has a different invitation ID and
  therefore a different host mutation ID.

The feature does not import authorization or dictate how the host uses this ID.
The reference host derives its authorization MutationID from a fixed purpose,
`GrantInput.OperationID`, and the tuple fields.

**YOUR CALL:** ratify the structured breaking seam and operation-ID semantics.

### D2 — Granter success means the requested relation is effective

Recommendation: strengthen the contract:

- nil means the requested exact relation was applied or already exactly
  present;
- a different existing relation is not success;
- invariant refusal is not success;
- a missing/deleted host resource is not success;
- infrastructure/command failures propagate.

Authorization adapters must inspect `Receipt.Outcome`. For the reference ReBAC
adapter, only `OutcomeApplied` and `OutcomeNoChange` are success.
`OutcomeSemanticConflict` and `OutcomeInvariantBlocked` map to a loud error
wrapping `sdk.ErrConflict`. A host-resource existence failure wraps
`sdk.ErrNotFound`. The adapter must not use
`ReplaceRelationship`: authentication cannot decide that an invitation may
downgrade or upgrade an existing membership.

**YOUR CALL:** ratify fail-loud conflict semantics; no implicit replace.

### D3 — relation-aware InviteCheck is required with invitations

Recommendation: add a host policy seam used by the feature's HTTP invitation
handlers after parsing and principal resolution:

```go
type InviteAction string

const (
    InviteCreate InviteAction = "create"
    InviteList   InviteAction = "list"
)

type InviteCheckRequest struct {
    Principal identity.Principal
    Action InviteAction
    ResourceType string
    ResourceID string
    Relation string // set for create; empty for list
}

type InviteCheck func(context.Context, InviteCheckRequest) error
```

`Config.InviteCheck` is required whenever `Config.Granter` enables
invitations. Nil is `ErrInviteCheckRequired` at `NewService`, not an
allow-by-default or a silently unprotected route. The feature's create/list
handlers call it after live-session validation, principal resolution, and
request parsing. Denial maps through the normal sdk/web error path; an
infrastructure error fails closed. Host-direct Service methods remain trusted
composition calls and document that they do not apply HTTP policy.

All authenticated invitation routes—create, resource list, mine, accept,
cancel, and resend—move from `RequireUser` to the feature's existing
`RequireLiveSession` posture while retaining user-only behavior. The public,
token-authorized decline route remains public and rate-limited. This matches
the feature's other sensitive reads and durable mutations and requires no new
host seam.

Accept/decline/mine keep their subject/token ownership rules; acceptance does
not re-run inviter authority. Deleted-resource refusal belongs to the Granter,
because only the host knows whether a resource still exists.

Authority timing is an explicit part of this ruling. Recommendation: an
invitation authorized at creation is a durable, expiring capability until it is
accepted, declined, cancelled, or expires; later loss of the inviter's host
permission does not silently invalidate it. This matches the persisted
invitation model but means resource-owner cancellation of another inviter's
pending invitation remains a separate lifecycle/admin design. If the desired
posture is instead “inviter must still be authorized at acceptance,” stop here:
`GrantInput` must also carry the persisted initiator identity, accept/resolve
tests must pin that re-check, and the cross-feature grant/status-update retry
semantics need a fresh design. Create-time `InviteCheck` alone does not provide
that revocation behavior.

**YOUR CALL:** ratify required-at-construction rather than optional/allowing
nil, and ratify issuance-time authority (recommended) vs acceptance-time
revalidation (requires revising D1/tasks before execution).

### D4 — store probes match the authorization posture

Recommendation: change both authentication store constructors to:

```go
func Repositories(db *pgxdb.DB) (auth.Repositories, error)
func Repositories(db *tursodb.DB) (auth.Repositories, error)
```

Probe all 13 canonical tables before returning (13 migration files define 13
tables even though the bundle exposes 15 repository ports). pgx uses
`to_regclass`; Turso
uses `sqlite_master`, matching each authorization store sibling. A missing
table returns a stable error naming the table and `authentication` migration
source, wrapping `sdk.ErrNotFound`. Constructors never apply migrations.

Update every repo consumer atomically: auth-cms, any other examples, store
test harnesses, and docs.

**YOUR CALL:** ratify the breaking return signature for both dialects.

### D5 — PostgreSQL collation travels with the canonical schema

Recommendation: stop relying only on database-wide `C` collation. Audit every
authentication and authorization pgx query/order rim and add explicit
`COLLATE "C"` to opaque TEXT columns participating in:

- cursor/keyset primary-key tiebreaks;
- explicit deterministic `ORDER BY` projections;
- derived role/relationship ordering keys; and
- equality/unique keys whose reference-store parity is byte-based.

Do not apply `C` indiscriminately to human display/content fields. Add
migration tests naming the contractual columns and a live pgx test created on a
non-C database proving the column collation, not cluster default, controls
ordering. Turso migrations are unchanged.

If preflight confirms no relevant module tags exist, fold this into canonical
CREATE migrations and record the pre-tag change in `RELEASING.md`. If it finds
a relevant tag, stop and design append-only ALTER migrations instead.

**YOUR CALL:** ratify per-column durability over a deploy-time-only C-database
precondition.

## Scope

### In

- `features/authentication`: Granter request/contract, operation identity,
  InviteCheck configuration/construction matrix, parsed HTTP enforcement,
  live-session invitation gates, tests, README.
- `features/authentication/stores/{pgx,turso}`: boot probes, breaking
  constructor return, canonical migration parity/tests, docs.
- `features/authorization/stores/pgx`: contractual collation DDL/tests only;
  no mutation semantic change.
- `examples/auth-cms`: relation-aware host policy, operation-scoped trusted
  adapter, outcome mapping, deleted-resource validation in the host adapter,
  adversarial proof.
- root `RELEASING.md`, relevant READMEs, and `NOTES.md`.

### Out

- Segovia code or its plan/flags files.
- A generic authorization admin API.
- Actor-facing sharing endpoints or a default authorization MutationGuard.
- Automatic invitation cancellation when a host resource is deleted.
  Acceptance fails via the host Granter; bulk lifecycle cleanup is a separate
  host/domain orchestration design if later required.
- Implicit relation upgrade/downgrade policy.
- New migrations for already-tagged consumers unless preflight discovers tags.
- Changing authorization receipt/outcome semantics.

## Preflight

Before AAH-1:

1. Confirm the gopernicus worktree is clean and record HEAD/branch.
2. Run `git tag --list` again. Confirm no tags exist for
   `features/authentication`, `features/authentication/stores/{pgx,turso}`,
   `features/authorization/stores/pgx`, or auth-cms imports. If any relevant
   tag exists, stop and revise API/migration strategy.
3. Run `make check && make guard`; preserve the transcript.
4. Confirm live store DSNs/containers are available:
   `POSTGRES_TEST_DSN` and, for Turso integration,
   `TURSO_DATABASE_URL` / `TURSO_AUTH_TOKEN`. For D5, also identify a disposable
   non-C PostgreSQL database/DSN or explicitly provision one; do not silently
   run the ordering proof against the ordinary C-locale test database.
5. Read `ARCHITECTURE.md`, `features/README.md`, both feature READMEs,
   `RELEASING.md`, the authorization-v3 mutation/receipt contracts, and every
   touched store's canonical migrations.
6. Ratify D1–D5. Do not implement around an unresolved operation-ID or
   InviteCheck construction posture.

## Tasks

### AAH-1 — freeze the public invitation contracts

- **depends_on:** preflight + D1–D4 ratified
- **model:** opus
- **files:**
  - `features/authentication/authentication.go`
  - `features/authentication/internal/logic/invitationsvc/service.go`
  - invitation/auth construction tests
- **work:**
  - add `GrantInput` and change `Granter`;
  - add `InviteAction`, `InviteCheckRequest`, `InviteCheck`, Config field,
    and `ErrInviteCheckRequired`;
  - validate non-empty operation IDs before invoking a Granter;
  - use invitation ID for pending accept and resolve-on-registration;
  - mint a high-entropy direct-add operation ID from an unconditional random
    source, separate from the entity `Config.IDs` strategy;
  - pin the strengthened success contract in public doc comments.
- **tests:**
  - same invitation retry reuses one operation ID;
  - later invitation row for the same tuple gets a distinct operation ID;
  - direct-add carries a non-empty operation ID;
  - Granter failure never advances invitation state or records grant success;
  - Granter + nil InviteCheck fails construction; both nil keeps invitations
    off; both present enables them.
- **verify:** `cd features/authentication && go test ./... && go vet ./...`

### AAH-2 — enforce InviteCheck in parsed HTTP handlers

- **depends_on:** AAH-1
- **model:** opus
- **files:**
  - `features/authentication/internal/inbound/authentication/invitation.go`
  - its route/handler tests and wiring
  - `features/authentication/authentication.go`
- **work:**
  - invoke InviteCheck after principal resolution and JSON/path parsing;
  - create passes the exact requested relation;
  - list passes `InviteList` with empty relation;
  - denial/error fails closed through stable web/sdk mapping;
  - mount create/resource-list/mine/accept/cancel/resend with
    `RequireLiveSession`, preserving their user-only handler semantics;
  - keep the public, token-authorized, rate-limited decline posture unchanged.
- **tests:**
  - resource-allowed but relation-denied create never reaches service;
  - list authorization runs;
  - missing principal, denial, and policy error all fail closed;
  - each authenticated invitation route rejects a revoked session with an
    otherwise unexpired access JWT;
  - decline remains reachable without a session only with its token/rate-limit
    controls;
  - allowed create/list preserve existing response contracts.
- **verify:** `cd features/authentication && go test ./... && go vet ./...`

### AAH-3 — repair the auth-cms reference composition and proof

- **depends_on:** AAH-1, AAH-2
- **model:** opus
- **files:**
  - `examples/auth-cms/cmd/server/membership.go`
  - `examples/auth-cms/cmd/server/main.go`
  - authorization/invitation tests and README proof text
- **work:**
  - wire a relation-aware host InviteCheck;
  - update `relationshipGranter` to accept `GrantInput`;
  - validate the host resource exists before mutation;
  - derive MutationID from purpose + operation ID + tuple;
  - inspect the receipt and return errors for semantic conflict/invariant block;
  - delete the false “one-relation conflict is persisted success” claim.
- **tests:**
  - exact retry of invitation A replays without a revision bump;
  - revoke then invitation B for the same tuple applies again;
  - an existing different relation fails and invitation is not accepted;
  - a deleted resource fails without writing a tuple;
  - a member-capable actor cannot invite an owner when host policy forbids it.
- **verify:** `cd examples/auth-cms && go test ./... && go vet ./...`

### AAH-4 — add authentication store boot probes in both dialects

- **depends_on:** D4 ratified; independent of AAH-2/3
- **model:** opus
- **files:**
  - `features/authentication/stores/pgx/postgres.go`
  - `features/authentication/stores/turso/turso.go`
  - store constructor/probe tests and all call sites
- **work:**
  - change both constructor signatures to return `(auth.Repositories, error)`;
  - probe the exact 13-table canonical set;
  - name the missing table/source and wrap `sdk.ErrNotFound`;
  - update examples, test harnesses, and any workspace consumer in the same
    task so the repo never sits in a half-compiling state.
- **tests:**
  - full schema succeeds;
  - table-driven missing-table test covers every canonical table;
  - an infrastructure query failure is not misclassified as missing;
  - constructors never create/apply schema.
- **verify:**
  - `cd features/authentication/stores/pgx && go test ./... && go vet ./...`
  - `cd features/authentication/stores/turso && go test ./... && go vet ./...`

### AAH-5 — make pgx collation contractual in canonical migrations

- **depends_on:** D5 ratified; independent of AAH-1–4
- **model:** opus
- **files:**
  - `features/authentication/stores/pgx/migrations/*.sql`
  - `features/authorization/stores/pgx/migrations/*.sql`
  - both pgx migration tests and any ordering conformance fixture
- **work:**
  - inventory every text ORDER BY/keyset/derived-key participant from store Go;
  - apply explicit `COLLATE "C"` to those opaque columns in canonical CREATEs;
  - leave human content/display columns alone;
  - pin the inventory in migration tests so later schema edits cannot silently
    drop the contract;
  - assert the contractual columns' catalog collation is `C`, then prove on a
    disposable non-C PostgreSQL database that column collation—not the database
    default—controls the ordering.
- **verify:**
  - both pgx store module suites;
  - live `POSTGRES_TEST_DSN=... go test -count=1 ./...` for authentication
    and authorization store modules;
  - run the ordering-specific proof with a separately provisioned non-C DSN
    (record it as `POSTGRES_NON_C_TEST_DSN`; do not print credentials in the
    transcript).

### AAH-6 — documentation, release note, and closeout

- **depends_on:** AAH-1–5
- **model:** fable for docs; opus for final verification
- **files:**
  - `features/authentication/README.md`
  - `features/authentication/stores/pgx/README.md`
  - pgx/turso package comments
  - `features/authorization/README.md` where invitation derivation is shown
  - `examples/auth-cms/README.md`
  - `RELEASING.md`
  - `NOTES.md`
- **work:**
  - replace the stale five-store/plaintext documentation with the 15-port,
    13-table/13-migration v3 repository surface;
  - document operation-scoped grant identity and outcome handling;
  - document required InviteCheck and the host resource-existence duty;
  - document that authenticated invitation routes require a live session;
  - record breaking constructor/Granter changes and pre-tag migration folding;
  - record that per-column C collation is canonical while a C-database remains
    a supported belt-and-suspenders posture;
  - mark this plan complete only after the proof transcript is captured.
- **verify:**
  - `make check && make guard`;
  - `POSTGRES_TEST_DSN=... make test-stores` with Turso variables set for the
    integration leg;
  - rerun auth-cms adversarial invitation tests with `-count=1`.

## Sequencing

```text
preflight + D1–D5
        |
       AAH-1
        |
       AAH-2
        |
       AAH-3

AAH-4 and AAH-5 may run after ratification independently of AAH-1–3.
AAH-1…AAH-5 -> AAH-6 closeout.
```

Do not hand Segovia a partial framework commit. Its replace directives point at
HEAD, so the handoff commit must contain the full public API, both reference
composition changes, store constructor updates, canonical migrations, and docs.

## Acceptance criteria

- Retrying one invitation is idempotent; re-inviting after revoke is a new
  mutation and restores the requested tuple.
- Authentication cannot mark an invitation accepted/directly-added when the
  host grant produced conflict, invariant refusal, missing resource, or error.
- A host policy receives parsed relation data and can prevent privilege
  escalation such as editor → owner invitation.
- A revoked session cannot create, list, accept, cancel, resend, or inspect mine
  invitations using an outstanding access JWT; public decline keeps its
  token/rate-limit contract.
- Auth pgx/Turso constructors fail at boot with a named missing table before any
  request.
- Canonical pgx migrations carry their own byte-order guarantee for contractual
  text ordering keys.
- Auth-cms demonstrates the correct host composition; no docs recommend
  tuple-derived invitation identity or discarded receipts.
- All hermetic, live-dialect, repo guard, and adversarial proof gates pass.

## Risks

1. **Operation identity generated at the wrong layer.** Tuple identity recreates
   the original bug; request-attempt randomness for persisted invitations loses
   retry identity. The invitation row ID is the required persisted operation
   anchor.
2. **Implicit membership replacement.** Using Replace to make a conflicting
   invitation “work” can downgrade an owner. This packet fails loud instead.
3. **InviteCheck only wraps routes.** The check must run after parsing in the
   feature handler; a RouteRegistrar decorator cannot inspect the validated
   relation and is not accepted as completion.
4. **Authority timing is mistaken for revocation.** Create-time authorization
   does not revoke an already-issued invitation when the inviter later loses
   permission. Keep the ratified durable-capability semantics explicit, or stop
   and redesign operation input plus cross-feature retry behavior.
5. **Live-session change weakens user-only semantics.** Preserve the current
   user-only behavior when replacing middleware; do not accidentally make the
   invitation surface a service-account administration API.
6. **Probe false confidence.** Probe tests must cover every one of the 13
   canonical tables and both dialects; checking only users/sessions repeats the
   partial-schema failure mode.
7. **Collation overreach.** Applying C to display text changes user-facing sort
   semantics. The task inventories contract keys and leaves content columns
   untouched.
8. **Dirty cross-repo handoff.** Segovia builds directly against this checkout.
   Closeout requires one clean, fully green Gopernicus commit before Segovia
   Phase A starts.

## Ratification checklist

- [x] D1 structured Granter + invitation operation ID (owner, 2026-07-15)
- [x] D2 fail-loud non-applied outcomes; no implicit Replace (owner, 2026-07-15)
- [x] D3 required relation-aware InviteCheck + issuance-time authority (owner, 2026-07-15)
- [x] D4 breaking dual-dialect Repositories return (owner, 2026-07-15)
- [x] D5 per-column pgx C collation (owner, 2026-07-15)
- [x] No relevant published tags at preflight (`git tag --list` empty 2026-07-15)
