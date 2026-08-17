# Coordination-hub auth upstream task board

Status only. The numbered plan files own authority, acceptance criteria,
verification, and evidence. Check a task only after its exact gate passes and
the owning file has an execution-log entry.

## Phase 1 — user directory and lifecycle

- [x] CHAU-1.1 — freeze user status, directory projection, admin check, and repository contracts
- [x] CHAU-1.2 — append-only pgx/Turso status migrations and schema probes
- [x] CHAU-1.3 — directory list parity across memory, pgx, and Turso
- [x] CHAU-1.4 — atomic deactivate/reactivate and active-session mint conformance
- [x] CHAU-1.5 — status enforcement on every credential/session path
- [x] CHAU-1.6 — optional trusted service and host-authorized HTTP admin surfaces
- [x] CHAU-1.7 — lifecycle docs, migration runbook, and live race gate (both dialects CLOSED)

## Phase 2 — verification resend

- [x] CHAU-2.1 — characterize registration verification and freeze resend outcomes/budgets
- [x] CHAU-2.2 — opaque public resend initializer and replacement semantics
- [x] CHAU-2.3 — self-service and admin resend routes with enumeration/admin proofs
- [x] CHAU-2.4 — resend docs, operational semantics, and integration gate

## Phase 3 — runtime posture foundation

- [x] CHAU-3.1 — sdk foundation mode vocabulary and validation
- [x] CHAU-3.2 — email/notify transport-safety helpers
- [x] CHAU-3.3 — authentication compatibility bridge and host migration proof
- [x] CHAU-3.4 — canonical-import and security-posture documentation

## Phase 4 — email layout branding

- [x] CHAU-4.1 — characterize bundled-layout branding behavior
- [x] CHAU-4.2 — transactional logo rendering and escaping/accessibility tests
- [x] CHAU-4.3 — branding matrix and release documentation

## Phase 5 — password reset links

- [x] CHAU-5.1 — freeze reset URL configuration and compatibility posture
- [x] CHAU-5.2 — build reset links in the off-request-path initializer
- [x] CHAU-5.3 — replace raw-token-only templates and prove host-header isolation
- [x] CHAU-5.4 — reset landing/adopter/security documentation

## Phase 6 — passwordless provision-on-consumption

- [x] CHAU-6.1 — freeze opt-in construction matrix and versioned redemption binding
- [x] CHAU-6.2 — reference-memory atomic redeem/provision/adopt operation
- [x] CHAU-6.3 — pgx and Turso atomic implementations + live concurrency proof
- [x] CHAU-6.4 — worker delivery for unknown email links with unchanged public start parity
- [x] CHAU-6.5 — provision/adopt/login service integration and status enforcement
- [x] CHAU-6.6 — invitation resolution, audit events, and terminal cleanup
- [x] CHAU-6.7 — provisioning threat model, host wiring, and migration docs

## Phase 7 — existing OAuth linking documentation

- [x] CHAU-7.1 — pin v0.1.0+ route/service evidence and remove the false missing-code premise
- [x] CHAU-7.2 — exported-surface settings-page proof
- [x] CHAU-7.3 — complete README flow, callback, failure, and CSRF/session guidance

## Phase 8 — release and adoption

- [x] CHAU-8.1 — module diff/semver and migration inventory
- [x] CHAU-8.2 — full hermetic, live-store, race, guard, and example-host gates
- [x] CHAU-8.3 — owner-cut tag manifest and cold `GOWORK=off` resolution protocol
- [x] CHAU-8.4 — coordination-hub wiring/adoption checklist and flag dispositions

