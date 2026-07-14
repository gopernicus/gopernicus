# Deferred follow-up — optional admin surface

Status: **DEFERRED; not part of the authorization v3 completion gate.** Auth v3
identity and live-session seams are stable, but its recent-auth consume operation,
current session binding, and browser-safe mutation gate are internal rather than
public host-facing composition seams.
Depends on: completed authorization v3, the effects follow-up only if admin
effects are enabled, and a separately shipped authentication
`SensitiveMutationProtector` (or equivalent) that composes live session,
origin/CSRF, and operation/context-bound recent-auth consumption.

This packet is blocked indefinitely, not merely sequenced: it is unschedulable
until a separately ratified authentication follow-up packet ships the public
`SensitiveMutationProtector` seam, and no such packet currently exists or is
implied by any accepted plan. Authorization must never unblock itself by
importing authentication internals.

Required authentication prerequisite: expose a root host seam shaped like
`ProtectSensitiveMutation(SensitiveOperation) web.Middleware`, where the
operation supplies a stable purpose and a bounded request-derived context. The
middleware, not the authorization handler, reads the authenticated user/session,
applies browser-origin/CSRF policy, and consumes recent auth. Do not export raw
session context keys or require another feature to import authentication
internals.

## Goal

Expose a useful but optional JSON management surface without duplicating
authentication security policy, importing feature cores into one another, or
enabling self-escalation. Core guarded/system composition is already proved by
the v3 proof-host phase (phase 4 in `07-docs-and-closeout.md`); this follow-up
proves the generic HTTP adapter.

## Task AZADM-1 — optional protected JSON administration surface

Touch: new `internal/inbound/authorization`, public Config/Register, tests.

Implement:

- Add explicit `AdminConfig`; nil preserves v1 behavior and mounts no routes.
- Require identity in context for every route, a read guard for reads, a mutation
  guard for writes, and a host-supplied sensitive mutation middleware/protector.
  Construction rejects partial wiring.
- Register routes only after the already-built service exists; Register starts no
  goroutines.
- Suggested route families under `/authorization`: schema metadata, check/explain,
  relationship list/grant/revoke/replace, role list/assign/unassign, and mutation
  receipt lookup. Final paths are frozen before code and documented as contracts.
- Receipt lookup is available only within the ratified receipt retention window,
  is scoped by the same read guard as the target mutation scope, and never turns
  a globally unique MutationID into a cross-scope enumeration oracle.
- All writes require client MutationID and optional expected revision; responses
  return explicit domain outcome, replay metadata, and receipt where persisted.
- The handler is constructed with the guarded Service only and never receives
  `SystemMutator`.

Verify:

```sh
cd features/authorization && go test ./internal/inbound/authorization ./... -run 'Route|Admin|Registration|Guard'
make guard
```

Acceptance: nil admin config mounts nothing; partial protection fails at boot;
ordinary route code has no trusted-system escape hatch.

## Task AZADM-2 — strict HTTP body/origin/identity/error contracts

Depends on: AZADM-1.
Touch: inbound parsing/security helpers and tests.

Implement:

- Require `application/json` for JSON mutations, bounded bodies, one JSON value,
  unknown-field rejection, and empty/trailing-body rejection.
- The authentication `SensitiveMutationProtector` owns live-session,
  cookie-origin/CSRF, and recent-auth/step-up. The authorization handler still
  requires a principal and fails closed if the middleware failed to stash one.
- Errors: 401 absent identity, 403 guard denial, 404 only after authorized lookup,
  409 stale/conflict/invariant, 413 or 422 oversized caller input, 422 invalid
  schema/command, 503 indeterminate evaluation limit/infrastructure, and 500
  otherwise. Do not use 429 for deterministic graph complexity; reserve it for
  an actual request-rate limiter. Unavailability/
  backpressure wraps `sdk.ErrUnavailable` (503/`unavailable` via
  `web.ErrFromDomain`); any named machine codes come from a feature-local mapper
  over `web.RespondJSONDomainError`, per the auth v3 precedent — the sdk mapper
  stays untouched. Do not leak store errors.
- Never trust request Host/forwarded headers for policy or actor identity.
- Add self-escalation, CSRF/origin, stale revision, duplicate MutationID, body
  smuggling, oversized batch, and store-failure tests.

Verify:

```sh
cd features/authorization && go test -race ./internal/inbound/authorization/... -run 'Security|Origin|CSRF|Body|Identity|Error'
make guard
```

Acceptance: a session alone is insufficient to mutate authorization data; host
step-up/protection and authorization guard must both pass.

## Task AZADM-3 — schema/check/explain read surface with anti-enumeration gates

Depends on: AZADM-1 and completed AZ3-1.6.

Implement:

- Read guard runs before resolving/listing target state.
- Schema endpoint returns digest and a bounded safe projection; model details are
  available only to explicitly authorized administrators.
- Check endpoint requires the caller be allowed to inspect decisions for the
  requested subject/resource; it is not a public authorization oracle.
- Explain is separately gated and bounded; raw infrastructure errors, unrelated
  tuples, and policy data outside the requested path are excluded.
- List routes use crud pagination, stable ordering, filters, and configured max
  page/batch limits.

Verify:

```sh
cd features/authorization && go test ./internal/inbound/authorization/... -run 'Schema|Check|Explain|Enumerat|List'
make guard
```

Acceptance: an unauthorized caller cannot distinguish missing resources/subjects
or use the feature as a policy oracle.

## Task AZADM-4 — auth-cms generic-admin composition and step-up

Depends on: AZADM-1 and the public authentication sensitive-mutation protector.
Touch: auth-cms composition root/demo/tests only, plus the example's `go.mod`
where new module dependencies land — the example is self-contained (direct
requires + local replaces, the IX-13 layout), so wiring authorization store
modules follows that same pattern, not bare workspace resolution. The host's
post-remediation lifecycle (supervised delivery runtime, purge scheduler,
`outstanding` health accounting) is not this phase's to change.

Implement:

- Replace the current RequireUser-only role assign/unassign demo with the v3
  guarded mutation surface.
- Adapt auth v3 identity principal directly to authorization Actor.
- Adapt auth v3 recent-auth/step-up as the sensitive mutation middleware without
  either feature importing the other.
- Host MutationGuard checks a schema-declared `manage_access` permission and a
  separately composed platform-admin recipe. It evaluates pre-mutation state and
  denies self-grant unless existing policy already permits it.
- Invitation acceptance uses the separately held `SystemMutator` capability and
  stable MutationID; the admin adapter never receives that capability.
- Keep the no-authorization and host-authored-closure postures demonstrable.

Verify:

```sh
cd examples/auth-cms && go test -race ./...
make guard
```

Acceptance: login without manage permission/step-up cannot assign a role or
relationship; authorized stepped-up admin can.

## Task AZADM-5 — HTTP exact-userset, hierarchy, role, and revoke proof protocol

Depends on: AZADM-4.

Drive and record:

1. ordinary member cannot self-grant;
2. admin without step-up is challenged/denied;
3. stepped-up manager grants `group#member` and member gains access;
4. a `group#admin` grant does not authorize an ordinary member;
5. non-self Through-root hierarchy Check and Lookup return the same descendants;
6. global role appears in effective resource enumeration;
7. scoped revoke while global remains reports the same role grant remains;
8. two concurrent last-owner revokes produce one success/one invariant block;
9. stale revision and MutationID payload mismatch return stable command errors;
   and
10. exact retry returns the original receipt; durable mode has no second outbox
    row, while procedural mode may repeat the handler attempt and the handler
    de-duplicates MutationID.

Acceptance: transcript includes request/response, mutation receipts/revisions,
stored rows, and any enabled event/effect observations without secrets.

## Task AZADM-6 — optional procedural/events host variants and negative matrix

Depends on: AZADM-5 and completed AZFX follow-up tasks when effects are enabled.

Implement/prove two composition variants:

- procedural: post-commit notification handler receives the change; simulated
  failure reports committed receipt; retry may repeat the attempt and the
  handler proves MutationID de-duplication;
- events: authorization store appends to generic outbox, poller emits, generic
  jobs handler receives/de-dupes MutationID. Durability lives in the
  same-transaction outbox row; the host's default jobs runtime is the in-memory
  fenced mode documented as non-durable, so either wire a durable fenced jobs
  store for this variant or state honestly that handler execution is
  restart-lossy while the event record is not.

Negative boot matrix: admin partial wiring, missing guard, missing sensitive
middleware, events without appender/table/acknowledgment, orphan procedural
handler, invalid limits, and mutable/invalid schema. Opaque roles do not require
a core catalog; an admin host that configures one enforces it in its guard.

Verify:

```sh
cd examples/auth-cms && go test -race ./... -run 'Authorization|Admin|Effect|Event|Production|Negative'
make check
make guard
```

Acceptance: both architectural modes work, and no mode silently drops or
misstates its guarantee.

## Phase acceptance

- Optional admin routes are protected and nil-safe.
- Public auth sensitive-operation protection + authorization mutation policy
  compose at the host.
- Proof protocol covers exact usersets, parity, atomicity, retries, and roles.
- Procedural and events variants are demonstrated.
- `make check` and `make guard` pass.

## Stop conditions

- Authorization needs to import authentication for step-up.
- A mutation route can run with only RequireUser/session presence.
- Explain becomes an ungated policy oracle.
- The proof host requires any hidden `SystemMutator` HTTP path.

## Execution log

Append only during execution.
