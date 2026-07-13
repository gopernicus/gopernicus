# Phase 5 — optional admin surface and proof host

Status: DRAFT; ready after phases 3–4 and stable auth v3 composition seams.
Depends on: guarded mutation service, effects modes, auth v3 identity/step-up.

## Goal

Expose a useful but optional JSON management surface and prove that authentication
and authorization compose without importing each other or enabling self-escalation.

## Task AZ3-5.1 — optional protected JSON administration surface

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
- All writes require client MutationID and optional expected revision; responses
  return explicit receipt/disposition.
- The handler cannot construct a SystemActor.

Verify:

```sh
cd features/authorization && go test ./internal/inbound/authorization ./... -run 'Route|Admin|Registration|Guard'
make guard
```

Acceptance: nil admin config mounts nothing; partial protection fails at boot;
ordinary route code has no trusted-system escape hatch.

## Task AZ3-5.2 — strict HTTP body/origin/identity/error contracts

Depends on: AZ3-5.1.
Touch: inbound parsing/security helpers and tests.

Implement:

- Require `application/json` for JSON mutations, bounded bodies, one JSON value,
  unknown-field rejection, and empty/trailing-body rejection.
- Host sensitive middleware owns cookie-origin/CSRF and recent-auth/step-up. The
  authorization handler still requires a principal and fails closed if the
  middleware failed to stash one.
- Errors: 401 absent identity, 403 guard denial, 404 only after authorized lookup,
  409 stale/conflict/invariant, 422 invalid schema/command, 429/503 evaluation
  budget/infrastructure as ratified, and 500 otherwise. Do not leak store errors.
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

## Task AZ3-5.3 — schema/check/explain read surface with anti-enumeration gates

Depends on: AZ3-5.1, AZ3-1.6.

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

## Task AZ3-5.4 — auth-cms guarded mutation composition and step-up

Depends on: AZ3-5.1, stable auth v3.
Touch: auth-cms composition root/demo/tests only.

Implement:

- Replace the current RequireUser-only role assign/unassign demo with the v3
  guarded mutation surface.
- Adapt auth v3 identity principal directly to authorization Actor.
- Adapt auth v3 recent-auth/step-up as the sensitive mutation middleware without
  either feature importing the other.
- Host MutationGuard checks a schema-declared `manage_access` permission and a
  separately composed platform-admin recipe. It evaluates pre-mutation state and
  denies self-grant unless existing policy already permits it.
- Invitation acceptance uses the explicit trusted-system capability and stable
  MutationID.
- Keep the no-authorization and host-authored-closure postures demonstrable.

Verify:

```sh
cd examples/auth-cms && go test -race ./...
make guard
```

Acceptance: login without manage permission/step-up cannot assign a role or
relationship; authorized stepped-up admin can.

## Task AZ3-5.5 — exact-userset, hierarchy, role, and revoke proof protocol

Depends on: AZ3-5.4.

Drive and record:

1. ordinary member cannot self-grant;
2. admin without step-up is challenged/denied;
3. stepped-up manager grants `group#member` and member gains access;
4. a `group#admin` grant does not authorize an ordinary member;
5. non-self Through-root hierarchy Check and Lookup return the same descendants;
6. global role appears in effective resource enumeration;
7. scoped revoke while global remains reports effective access remains;
8. two concurrent last-owner revokes produce one success/one conflict;
9. stale revision and MutationID payload mismatch return stable conflicts; and
10. exact retry returns the original receipt with no second event/effect.

Acceptance: transcript includes request/response, mutation receipts/revisions,
stored rows, and event/effect observations without secrets.

## Task AZ3-5.6 — procedural/events host variants and negative matrix

Depends on: AZ3-5.5.

Implement/prove two composition variants:

- procedural: post-commit notification handler receives the change; simulated
  failure reports committed receipt and retry is safe;
- events: authorization store appends to generic outbox, poller emits, generic
  jobs handler receives/de-dupes MutationID.

Negative boot matrix: admin partial wiring, missing guard, missing sensitive
middleware, events without appender/table/acknowledgment, orphan procedural
handler, invalid limits, mutable/invalid schema, and roles without required role
validator under the ratified posture.

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
- Auth step-up + authorization mutation policy compose at the host.
- Proof protocol covers exact usersets, parity, atomicity, retries, and roles.
- Procedural and events variants are demonstrated.
- `make check` and `make guard` pass.

## Stop conditions

- Authorization needs to import authentication for step-up.
- A mutation route can run with only RequireUser/session presence.
- Explain becomes an ungated policy oracle.
- The proof host requires a hidden trusted-system HTTP path.

## Execution log

Append only during execution.
