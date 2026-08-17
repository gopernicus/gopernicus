# Coordination-hub authentication upstream follow-ups

Status: **PROPOSED — planning and documentation only. Owner ratification is
required before implementation, releases, pushes, or downstream adoption.**

Date: 2026-08-16

Source: coordination-hub's `.claude/plans/upstream-flags.md`, audited against
gopernicus `main` at `20dfcc9` (`features/authentication/v0.2.2`,
`sdk/v0.3.1`).

## Outcome

Close the remaining authentication and email-adjacent upstream gaps with
framework-owned contracts that coordination-hub can adopt through tagged module
updates and explicit host wiring. The program adds:

- an optional, host-authorized user-administration directory;
- an account lifecycle with atomic deactivation/session revocation;
- enumeration-safe self-service verification resend plus an authorized admin
  resend;
- app-wide runtime-posture vocabulary outside the authentication feature;
- correct logo rendering in the sdk transactional email layout;
- configured password-reset links rather than raw-token-only mail;
- opt-in magic-link provision-on-consumption for unknown email addresses; and
- complete operator/adopter documentation for every public contract.

This packet does not treat documentation as closeout polish. Every phase owns
its public API comments, route contract, security semantics, host wiring,
migration guidance, and release note before its gate can pass.

## Audit disposition of the coordination-hub rows

| coordination-hub flag | audited state | disposition in this packet |
|---|---|---|
| no administrative user listing | confirmed | `01-user-directory-and-lifecycle.md` |
| no account status/deactivation | confirmed | `01-user-directory-and-lifecycle.md` |
| no verification resend | confirmed | `02-verification-resend.md` |
| sdk layouts ignore `Brand.LogoURL` | partially true: `marketing.html` already renders it; the auth-default `transactional.html` does not; `minimal.html` is intentionally unbranded | `04-email-layout-branding.md` |
| app mail imports auth's `RuntimeMode`/transport error | confirmed layering inversion | `03-runtime-posture-foundation.md` |
| password-reset mail exposes only the raw token | confirmed | `05-password-reset-links.md` |
| no user-initiated OAuth linking | **not a code gap**: `StartLink` and session-gated `GET /auth/oauth/{provider}/link/start` have shipped since `features/authentication/v0.1.0` | documentation and proof only in `07-oauth-linking-documentation.md` |
| magic link never provisions an unknown address | confirmed; current worker deliberately resolves unknown/unverified to no delivery | `06-passwordless-provisioning.md` |

The authorization resolver/role middleware flags and the filestorage flag are
outside this auth packet. They need separate audits: notably, this checkout now
contains `sdk/capabilities/filestorage`, so the coordination-hub filestore row
must not be dispatched verbatim without a fresh version audit.

## Recommended design rulings

1. **Administration is opt-in and host-authorized.** Built-in stores may expose
   the repository capability, but admin HTTP routes mount only when the host
   supplies an action-aware `UserAdminCheck`. The feature never invents a role
   named `admin` and never imports authorization.
2. **The lifecycle vocabulary is closed.** V1 has `active` and `deactivated`.
   Suspension, deletion, anonymization, lock-until, and arbitrary status strings
   remain out of scope.
3. **Deactivation and session minting are race-safe.** Deactivation changes the
   status, increments `auth_revision`, deletes sessions and recent-auth grants in
   one repository transaction. Every new session is inserted only while the
   same user row is atomically proven active. A check-then-insert race is not an
   acceptable implementation.
4. **Public resend remains enumeration-safe.** The request path normalizes and
   rate-limits, then submits opaque replacement work. Account lookup, challenge
   replacement, rendering, and delivery stay off the request path. Admin resend
   is authorized and may report real target state.
5. **Runtime posture is application vocabulary.** Canonical development/
   production mode lives in the sdk foundation. Email and notify capabilities
   own their transport-safety validators. Authentication keeps source-compatible
   aliases during migration.
6. **Password-reset landing configuration is distinct from magic-link
   configuration.** `PublicAuthBaseURL` is already the full passwordless landing
   URL; deriving a reset route from it is invalid. A separate validated reset URL
   is built only from configuration, never request headers.
7. **Unknown-address provisioning is explicit and email-link-only.** It is
   disabled by default, never applies to OTP/phone starts, and creates the account
   only in the transaction that successfully consumes the link. Sending a link
   must not create a user or identifier.
8. **Existing OAuth linking is documented, not rebuilt.** The work adds an
   end-to-end settings-page recipe and exported-surface proof; it does not create
   a second route or state model.
9. **No release claim without adopter docs.** A tag manifest must state migration
   order, configuration changes, compatibility aliases, route enablement, and
   the exact coordination-hub wiring/removal of any interim.

## Program invariants

- Unknown, malformed, deactivated, verified, and unverified addresses do not
  become distinguishable through public resend/passwordless-start timing,
  status, body, delivery receipt, or provider-call behavior.
- No raw identifier appears in a limiter key, generic jobs logical key, log
  label, metric label, or lifecycle event.
- Codes/tokens remain protected at rest; delivery envelopes remain encrypted;
  retries resend the checkpointed secret rather than minting another.
- A status transition cannot race a session mint and leave a session created
  after deactivation.
- A magic-link consume is single-use and transactional with create/adopt/login;
  a transient transaction failure cannot commit a half-provisioned user.
- Deactivated users cannot obtain a new session through password, refresh,
  OAuth, passwordless, or act-as-user API-key paths.
- Admin authorization is fail-closed on denial, infrastructure error, missing
  principal, and partial wiring.
- Feature core remains datastore-, authorization-, and integration-blind.
- Pgx and Turso canonical migration filenames and store behavior remain in
  parity; live concurrency claims are not closed by memory tests alone.

## Phase queue

| Phase | File | Principal output | Dependencies |
|---|---|---|---|
| 1 | `01-user-directory-and-lifecycle.md` | user directory, active/deactivated lifecycle, atomic revocation/minting, optional admin routes | none |
| 2 | `02-verification-resend.md` | public opaque resend and authorized admin resend | phase 1 contracts for status-aware behavior; implementation may characterize independently |
| 3 | `03-runtime-posture-foundation.md` | sdk runtime mode + capability-owned transport checks, auth compatibility bridge | none |
| 4 | `04-email-layout-branding.md` | transactional logo rendering + branding docs/tests | none |
| 5 | `05-password-reset-links.md` | configured reset landing URL and link-rendering mail | phase 3 canonical runtime mode |
| 6 | `06-passwordless-provisioning.md` | opt-in atomic provision-on-consumption | phase 1 active-session/status contract |
| 7 | `07-oauth-linking-documentation.md` | existing-flow proof and adopter recipe | none |
| 8 | `08-release-and-adoption.md` | full gates, upgrade notes, tag/cold-resolution/adoption manifest | selected implementation phases |

Phases 3, 4, and 7 are independent. Phases 1 and 2 should be implemented by one
owner sequentially because they share the user/admin route and delivery seams.
Phase 6 is deliberately last on the authentication security path.

## Documentation definition of done

Every implemented phase must update, as applicable:

- exported Go doc for types, sentinels, construction matrices, and trusted
  service calls;
- `features/authentication/README.md` route tables, configuration matrix,
  security posture, delivery guarantees, and host examples;
- `sdk/capabilities/email` or sdk foundation package docs for app-wide concepts;
- pgx/Turso store READMEs and migration export/upgrade instructions;
- `RELEASING.md` keyed next-tag entries without contradictory duplicates;
- an example-host or black-box exported-surface proof that doubles as runnable
  adopter documentation; and
- the coordination-hub adoption manifest: exact module versions, config fields,
  route paths, schema-copy steps, and upstream-flag disposition.

Documentation must state failure and race semantics, not only the happy-path
method signatures.

## Release shape

Prefer two release trains rather than coupling the high-risk provisioning work
to the admin-console unblock:

1. **Core/admin train:** phases 1–5 and 7. This can unblock user administration,
   resend, reset links, runtime layering, and sdk branding.
2. **Provisioning train:** phase 6 after its dual-dialect atomicity and adoption
   proof passes.

Exact versions are frozen during phase 8 after diffing every module from its
latest tag. Expected floors, not promises: an sdk minor for new canonical
runtime/capability APIs; an authentication minor for the lifecycle/admin public
surface; and minor store-adapter tags because new repository behavior and
append-only migrations are adopter-visible. Owner-cut tags are never created by
executing this plan unless separately dispatched.

## Global stop conditions

Stop for owner direction rather than weaken the contract if implementation would:

- mount admin routes without an explicit host authorization check;
- implement deactivate as update-then-best-effort-session-delete;
- accept a session mint based on a non-atomic status precheck;
- make public resend resolve an account or render/send on the request path;
- reuse request `Host`/forwarded headers to construct reset or magic links;
- provision an account when a link is sent rather than when it is consumed;
- keep an attacker-controlled password/session alive after an unverified-email
  adoption branch;
- widen provisioning to phone/OTP without a separately ratified identity model;
- store a raw challenge or delivery secret; or
- claim live pgx/Turso concurrency coverage when those tests skipped.

