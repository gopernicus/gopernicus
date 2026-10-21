# External identity providers — an sdk authentication port, a generic OIDC verifier, vendor presets, and the local subject mirror

**Status:** RELEASED 2026-10-20 — eight tags, cold-verified: sdk/v0.14.0, go-oidc/v0.1.0,
firebase/v0.1.0, auth0/v0.1.0, pockets/authentication/v0.19.0 → **v0.19.1** (hosts skip v0.19.0),
stores pgx/v0.9.0 + turso/v0.8.0; examples #111 untagged; task-8 docs + release record on
`docs/external-identity-release` (see RELEASING.md). Ratified 2026-10-20 (owner: "ratified").
Rev 2 + owner rulings below.
**Firebase live leg (owner ruling 2026-10-20):** no real Firebase project. Instead, an
OPTIONAL local Firebase Auth emulator under `workshop/dev/` on docker drives the Firebase
legs. Consequence, accepted: the emulator issues UNSIGNED tokens, so Firebase-specific
signature and key verification against Google's real keys is not exercised. The signed-JWT
path is proven generically by the `oidctest` JWKS issuer (task-2). A real-tenant leg stays
optional and env-gated for later, and is NOT in the Definition of Done.
Emulator setup (owner ruling 2026-10-20), built in task-3:
- `workshop/dev/firebase/` holds a small Dockerfile pinning a `firebase-tools` version and a
  `docker-compose.yml` that runs the Auth emulator only, plus a `make dev-firebase` target.
- A matching `docker run` line goes in the Makefile comments, following the Firestore emulator
  precedent at Makefile:75–85.
- The emulator legs SKIP loudly when `FIREBASE_AUTH_EMULATOR_HOST` is unset.
**Authorization** (`authorization-provider-port.md`) is parked until this plan lands.
**Progress:** task-1 BUILT @ 9143b6d0 → PR #99 (worktree `../gopernicus-hc`, branch
`feat/sdk-authentication-port`; make check green; lead re-verified). Deviations accepted in the PR:
`*OIDC` return, construction panics, ErrSignInMethodDenied, UnavailableError kinds. #99 MERGED
@ d4c3bfa4; **sdk/v0.14.0 RELEASED** (proxy ~10 s, cold-verified: capability + cryptids tests,
the ES256 scratch, go mod verify). #100 MERGED @ 845d8313; **go-oidc/v0.1.0 RELEASED** (tagged by the lead on owner
instruction, proxy ~10 s, cold-verified including the real-HTTP scratch). #101 MERGED @ a7b97e4b; **firebase/v0.1.0 + auth0/v0.1.0 RELEASED** (tagged by the lead on
owner instruction; cold-verified, including all 7 emulator tests against the PUBLISHED firebase module).
Owner rulings: KEEP the auth0 client-credentials deny default; ADD the emulator/live build tags to
make check's vet step (do it in task-8). task-4 BUILT @ aaa814e9 → PR #102 (additive;
the lead's diff check found no removed or changed exports; the real 5-scenario run was re-verified by the lead).
No pocket tag until task-5 (one v0.19.0 break). #103 + #104 MERGED; **pockets/authentication/v0.19.0 TAGGED by the owner @ 6a71627c** and cold-verified
(19 packages ok). REGRESSION found while repinning: v0.19.0 ClassifyBearer refuses non-JSON or foreign-iss
dotted bearers before the host TokenSigner sees them → fix PR #106 (pgx live suite green). #105 invitation
CSRF gap → fix PR #107. Both ship as **v0.19.1**; the store repin then pins v0.19.1 (not v0.19.0). #106 + #107 MERGED @ 0827ca4d.
The form-encoded CSRF deferral (the gate skipped the token for EVERY form body, but only account-form
handlers run the body double-submit; ~35 routes affected) is fixed on `fix/csrf-form-lane-opt-in` @ 99952702 → PR #108:
strict by default, with an explicit form-lane variant. The hole was confirmed (a form cancel with no token returned 200
cancelled); the lead re-verified the 403s live. #108 MERGED @ 5ae1ada8;
live store legs green on main; **pockets/authentication/v0.19.1 TAGGED by the owner @ 5ae1ada8**, cold-verified
(19 packages). CI live-PG gap filed as #109 (live-stores.yml is dispatch-only; its hosted Turso target is gone). Store
repin to v0.19.1 → PR #110 (live PG + Turso verified with GOWORK=off). #110 MERGED @ 2c747607; **stores/pgx v0.9.0 +
stores/turso v0.8.0 RELEASED** (tagged by the lead on owner instruction; cold-verified live: pgx bare + schema, 11
ExternalSubjects cases, Turso local, go mod verify). Cold runs must `-skip ^TestMigrationParity$`, because that
pre-existing test reads the sibling module's migrations dir, which a published module doesn't contain. Task-8 should
make it t.Skip when the sibling is absent. #111 MERGED @ 74655703. **task-8 BUILT** on `docs/external-identity-release` (worktree `../gopernicus-hc`, one
local commit, not pushed): the pocket README "External identity providers" section (decision table, ownership,
recipes for Firebase header + the session-cookie exchange, Auth0, Clerk, Cognito, Okta, Keycloak, support matrix, lag
tables, roles-are-not-authorization, edge cases, audit gap, step-up loss, invitations, host-route CSRF, "Leaving a
provider"); docs-site mirrors (`pockets/authentication.md`, new `sdk/authentication.md` + sidebar + capabilities
catalog row); the RELEASING train entry with the BREAKING callout and the gps-360-go/Segovia heads-up; this copy in
tracked `plans/`. Owner-assigned follow-ups, all done: (a) `make check` vets `-tags=live` (auth0, firebase, go-oidc)
and `-tags=emulator` (firebase) compile-only via `PROVIDER_LIVE_TAG_MODULES`/`EMULATOR_TAG_MODULES`, proven by a
planted compile error; (b) `TestMigrationParity` in stores pgx + turso `t.Skip`s when the sibling migrations dir is
absent (runs in-repo; proven by renaming the sibling dir); published pgx v0.9.0 / turso v0.8.0 cold runs still need
`-skip ^TestMigrationParity$` until the next store tag (RELEASING Known); (c) auth-cms README Leg 12 for
`AUTH_EXTERNAL_DEMO=1` (curls re-run live); (d) host-route CSRF posture documented — still the **owner's pending
call** whether the pocket should export a host-route double-submit gate (recommendation: document, do not build).
Unverified and marked as such in the docs: Firebase IAM permission names, Clerk/Okta hash export, every Clerk/Cognito/
Okta/Keycloak claim name (incl. Clerk's missing default `aud`, which gooidc requires). `Token.Claims` verified (task-3). Earlier: task-7 BUILT @ a2abfee4 → PR #111 (the external-identity host:
zero drivers, G30 clean; 8-step run-and-look re-verified by the lead; auth-cms TestExternalDemo* pass). Host-route CSRF
RULED 2026-10-21: no exported gate; hosts use http.CrossOriginProtection; recorded as a Known limitation
("99% of these will be api requests").
Then task-8 (docs, RELEASING, vet the emulator/live tags, the parity-test skip). CI gap: no live-PG leg in check.yml.
#102 MERGED @ dd8b9709. task-5 BUILT @ 5dbf55c5 →
PR #104 (BREAKING: signature + R-LIVE native + 503 on a store read error; real HTTP a–g re-verified by
the lead, including the outage within/past grace). Found a pre-existing issue: invitation POST routes lack the
double-submit CSRF gate (owner's call on an issue). Owner's call: RFC 9470 on MFA/FreshAuth only.
(`../gopernicus-hc`, `feat/auth-external-http`) ∥ task-6 BUILT @ 57800646 → PR #103 (11 conformance
cases; live PG17 bare + schema re-verified by the lead; Turso local only). HOST IMPACT: migration
0020 is required for every pgx/turso host, even without external providers (R9 boot probe), and must be stated in
the release notes. The stores keep `require pocket v0.18.0` via go.work, and get repinned to v0.19.0
in a post-tag pin PR (the #82/#87 pattern).
Earlier: task-3 BUILT @ edf415dd → PR #101
(firebase admin v4.22.0, firebase-tools 15.34.0; 18 emulator-tag tests re-verified by the lead against the
running container). Open owner questions: auth0 denies client-credentials tokens by default
(unplanned); vet the emulator/live tags in make check?; IAM names unverified (task-8).
Earlier: task-2 BUILT @ 0694cf05 → PR #100 (go-oidc v3.17.0, sdk v0.14.0 pin; real-HTTP
scratch re-verified by the lead) on `feat/go-oidc-verifier` in
`../gopernicus-hc` (the worktree is reused per task, with a fresh branch from main each time).
Owner rulings are marked **YOUR CALL** (collected in "Rulings needed"). Rev 1
(2026-10-19) survives in D1 (`cryptids.JWTVerifier`), D2 (`integrations/cryptids/go-oidc`),
the subject mirror, the hybrid chain, the assurance options, the R5 collision policy,
the R6 JIT default and the test strategy. Everything else is reworked against the
owner direction of 2026-10-20 and three consultations (lead-backend-engineer,
architecture-steward, platform-sre — notes at the end). A sibling plan,
`.claude/plans/authorization-provider-port.md`, is being written in parallel and
owns every authorization change; this document designs none.

## Owner rulings (2026-10-20)

**All rulings in "Rulings needed" are RULED AS RECOMMENDED** ("I agree with your
recommendations on the authentication"). That covers R-SDK, R1, R2, R-FB, R-AUTH0,
R-POCKET, R-MIRROR, R-SEAM, R3, R4, R5, R-SYMMETRIC, R6, R-LIVE, R-FAIL, R-TENANT,
R-ANON, R7, R8, R9 and R10. Notable:
- **R-LIVE is uniform:** `LiveOnMutation` is the default for native tokens too. This is a
  deliberate behavior change for existing hosts: native mutations now check the session is
  still live. Call it out in the RELEASING entry as BREAKING (behavior) with the
  per-route `Stateless()` escape.
- **R-FAIL:** fail closed (503) when the provider is down; per-provider FailOpen applies to
  the default tier only, never to `Live()`.
- **R-POCKET:** the pocket is required in v1. **R-MIRROR:** a new `external_subjects`
  table, not `oauthaccount`.

**Gaps closed after rev 2:**
- **Downstream `CredentialService` implementers: none.** Grepped on 2026-10-20: neither
  gps-360-go nor Segovia v2 implements `CredentialService` or calls `NewAuthenticator`.
  The `Authenticate`/`RequireLive` return-type break is internal to this repo.
- **Authorization crossover:** out of scope here. When authorization later runs on an
  external provider (`authorization-provider-port.md` D7), invitations lose the grant
  operation ledger's fence and settle state-convergently. That plan owns the fix. This
  plan's invitation path (an external user accepting by verified email) assumes the
  gopernicus authorization engine, as today.

## One-screen summary

- **The port is in sdk.** `sdk/capabilities/authentication` (package `authentication`,
  aliased `sdkauth` everywhere) defines `Credential` → `Identity`: a verified
  *external* identity (issuer, subject, email/phone + verified flags, display name,
  tenant, auth time, methods, MFA) with **no `sdk.Principal` and no user id** on it.
  Mapping to the internal principal is the pocket's job. `NewOIDC(issuer, verifier,
  profile)` is the one generic, stdlib-only provider over a `cryptids.JWTVerifier`
  with per-issuer claim mapping. A conformance suite pins it (D3).
- **Vendor presets are integration modules** under the capability category:
  `integrations/authentication/firebase` (Admin SDK: ID-token + session-cookie
  providers, revocation facts, `MintSessionCookie`) and
  `integrations/authentication/auth0` (zero libraries, preset only). Presets return
  sdk values; `cmd` composes the go-oidc verifier (G13 forbids integration →
  integration). Vendor names never enter sdk or the pocket core (D4).
- **The pocket stays the identity home.** `pockets/authentication` consumes the
  port exactly as it consumes `cryptids.JWTSigner`: a `CredentialExternalToken`
  kind, `Credential.Identity`, the `external_subjects` mirror keyed by a host-chosen
  provider NAME + subject (not the token `iss` — Firebase ID tokens and session
  cookies carry different `iss` for one `sub`), JIT via `Users.Provision`, deny +
  audit on an email collision, and the rev 1 A′ engine (`logic/authentication/external.go`)
  so an external-only host constructs no signer, sessions or mail (D5–D7).
- **Revocation is a per-route axis** on `RequirePrincipal`: `Live()` (always,
  always fail-closed), `LiveOnMutation()` (default: unsafe methods re-check, safe
  reads pass on validity), `Stateless()` (never). An external re-check is the
  uncached local `users.Get`+`Active()` kill switch plus the provider's revocation
  FACTS (`disabled`, `valid_after`) cached 30 s per (provider, subject) and compared
  to each token's `auth_time`. Provider outage → 503 + `Retry-After`, never 401;
  a per-provider `RevocationFailOpen` relaxes only the default tier (D8).
- **Server-rendered pages ride the provider's own session mechanism** when one
  exists: a provider cookie name on the pocket's cookie transport; Firebase mints
  and verifies session cookies through the Admin SDK; Clerk's `__session` is a
  verifiable JWT; Auth0, Cognito, Okta and Keycloak have no server-minted
  application session and are documented **header-only** (D9, support matrix).
  The native "handshake → gopernicus session" exchange is deferred (two sessions,
  revocation lag).
- **Machines stay native.** `resolveCredential` routes a JWT by its unverified
  `iss` to exactly one provider (never a second attempt), a dotless bearer to the
  API-key path; every link lands in the same `sdk.Principal` shape (D10).
- **Breaking, pre-1.0 MINOR:** `CredentialService.Authenticate`/`RequireLive`
  return `(ctx, error)` so 503 is expressible; the default set admits wired
  external tokens; the liveness default changes for native POST routes (R-LIVE).
  Semver table in "Module / API impact".

## Direction (owner, 2026-10-20)

Recorded verbatim in substance; "agreed" items are settled and not relitigated
here, "recommended" items appear in the rulings table.

1. **(agreed) Shape.** An sdk-level authentication port turns a presented
   credential into an `sdk.Principal` plus normalized credential/assurance fields;
   identity providers plug in as adapters ("if you're using Firebase you wire the
   Firebase provider"). Downstream gopernicus consumes only `sdk.Principal` —
   **verified**: `pockets/authorization` (`logic/model/principal.go`,
   `inbound/http/{roles,composable_middleware,routes}.go`) and `pockets/events`
   (`internal/hub/hub.go`, `logic/streams/*`, `inbound/http/*`) read
   `sdk.PrincipalFromContext` only; `pockets/cms` is gated by injected
   `[]web.Middleware` and reads no principal; no pocket imports
   `pockets/authentication` (grep of `pockets/{authorization,cms,events,jobs,secrets}`
   is empty). ONE generic OIDC verifier with per-issuer config, THIN vendor presets,
   vendor code only where vendors differ (revocation, claim names, server session).
   Preset placement decided in D4.
2. **(agreed) Ownership split.** The provider owns credentials and their lifecycle
   (stored passwords, sign-in/sign-up UI, reset, email verification, MFA enrollment,
   social federation, brute-force protection). Gopernicus owns identity: the user
   record (internal id, profile, status), API keys/service accounts, invitations,
   the authorization link, audit/security events, admin disable. The provider
   manages its own sessions; gopernicus mints **no** native session for provider
   users in v1.
3. **(recommended) The pocket remains the identity home**: the mirror
   `(provider, sub) → user id`, JIT provisioning, local status. Native credential
   methods are optional. Rulings: R-POCKET (is the pocket required for an
   external-only host?), R-MIRROR (new table vs `oauth_accounts` — evidence in D6).
4. **(agreed) Server-rendered pages** use the provider's own server-session
   mechanism (Firebase session cookies via `createSessionCookie`/`verifySessionCookie`).
   Support matrix with citations in D9; providers without one are header-only. The
   native exchange is deferred with its trade-off recorded.
5. **(agreed) Revocation is a per-route policy**, default on unsafe methods, off on
   safe reads, configurable both ways in the `RequirePrincipal(opts…)` style; cache
   and failure mode designed in D8 (fail-closed by default — planner's call).
6. **(agreed) Machine credentials stay native**; a chain runs provider token(s)
   then API key in a defined order; every link produces the same `sdk.Principal`.
7. **(recommended) Provider claims are identity data only, never permissions.**
   Mapping into authorization happens in host code. Tenant pinning per provider
   is ruling R-TENANT.
8. **(recommended) Credential methods: seam vs flags** — ruling R-SEAM, sized in PRs.

## Context

Hosts increasingly arrive with an identity provider already chosen (Firebase Auth,
Auth0, Clerk, Cognito, Okta, Keycloak). All issue OIDC-shaped JWTs (RS256/ES256
over a JWKS, `iss`/`sub`/`aud`/`exp`, vendor-shaped assurance claims). Today the
pocket verifies exactly two credentials — its own session-backed access JWT and
its own API keys (`logic/authentication/authenticate.go:17`, `machine.go:216,304`)
— and `RequirePrincipal` classes bearers by dot count (`inbound/http/principal.go:84-101`).
No current host (gps-360-go, Segovia) needs an external provider today; owner
foresight counts as demand, so v1 is deliberately minimal and the rest is listed as
follow-ups.

Grounding verified in this pass (not re-derived):

- `CredentialService` (`inbound/http/authenticator.go:81-90`) is bool-only
  (`Authenticate`, `RequireLive` return `(ctx, bool)`); `denyPrincipal`
  (`principal.go:136-146`) writes only 401 or 303. No outside implementer exists
  (repo grep; hub/gps-360-go to confirm before repin).
- `credentialProof.owner` is `*Service` and the stash key is unexported
  (`context.go:30-41`); the bundled groups wrap `strategy(floor(next))` with
  `floor := svc.RequirePrincipal()` (`routes.go:163-175`), so a nested gate
  re-runs liveness unless the proof carries a "proven" marker (native tokens have
  one: `CurrentSessionID`, `machine.go:331`).
- Every bundled mutation group runs `requireBrowserSafeMutation` (`security.go:96-129`):
  a non-bearer request needs the `__Host-auth_csrf` cookie + `X-CSRF-Token`, minted
  by `GET /auth/csrf` under `SessionSecurityReads`.
- `Users.Provision` inserts the identifier unconditionally (`stores/pgx/users.go:99`,
  turso likewise; `user/repository.go:32` takes `identifier.Identifier` by value).
- The only identifier uniqueness is the partial index over active login/recovery rows
  (`0010_user_identifiers.sql:31-37`); `Register` relies on it alone (`service.go:454-464`).
- `ActiveVerifiedIdentifier` (`service.go:593`) projects active+verified identifiers
  regardless of use, so invitation accept (`invitations/service.go:567,1112`) already
  matches a verified notification-only email; direct-add `userLookup` uses
  `GetLogin`/`GetRecovery` only (`constructor.go:607-632`); pending auto-accept fires
  only from native verify/registration (`resolvePendingInvitations`, `service.go:576`).
- The auth-cms memory store is `examples/auth-cms/pockets/authentication/outbound/authmem`
  (rev 1's `internal/authmem` path was wrong). `gopernicus guard` (G30) exists at
  workshop/gopernicus v0.4.0 and hardcodes the host list (`Makefile:617`).
- `sdk.ErrUnavailable` exists (`sdk/errors.go:54`) and `web.RespondJSONError` maps it
  to 503; `cacher.WithNamespace`/`WithOnError`/`WithMaxEntries` exist.
- `integrations/oauth/google` already isolates `github.com/coreos/go-oidc/v3`
  (`google.go:22`, verifier at `:131` with `oidc.Config{ClientID}`); `integrations/oauth/github`
  is the zero-library vendor-contract precedent.

## Goal

A host wires one provider per identity vendor — `sdkauth.NewOIDC` over a go-oidc
verifier, or the Firebase provider — and `RequirePrincipal` admits that vendor's
tokens (header, or the vendor's own session cookie) as internal `sdk.Principal`s
with per-route revocation policy and provider-agnostic assurance options, while an
external-only host carries no signer, sessions, mail or password tables and the
pocket's downstream consumers see nothing but `sdk.Principal`.

## Definition of Done

- `sdk/capabilities/authentication` ships `Provider`, `Identity`, `Profile`,
  `NewOIDC`, `RevocationChecker` and `authenticationtest.RunProvider`; `make guard`
  (G12b/c) green with the new capability; the ARCHITECTURE protocols table gains a
  CONSUMES row.
- `integrations/cryptids/go-oidc` verifies tokens from the `oidctest` dev issuer
  and from ONE real tenant (env-gated, owner-minted) honoring pinned iss/aud, the
  algorithm allow-list, 60 s tolerance, per-issuer refetch floor, last-good-keys on
  fetch failure; the kid-spray test proves ≤ 1 fetch per floor window.
- `integrations/authentication/firebase` verifies an ID token and a session cookie
  from a real project (`-tags=live`), returns revocation facts, mints a session
  cookie, and refuses construction in production when the Admin SDK reports
  emulator mode; `integrations/authentication/auth0` returns a `Profile` whose live
  leg verifies one real Auth0 access token through go-oidc.
- `examples/external-identity` boots with zero datastore drivers, no `TokenSigner`,
  no sessions, no mail; a dev-issuer token reaches `RequirePrincipal()` and gets an
  internal user id; the second request hits the mirror; a dev-issuer session cookie
  reaches a cookie-transport route; `gopernicus guard` passes (G30 list extended).
- `examples/auth-cms` serves one hybrid route admitting a dev-issuer token OR a
  native API key, and one `RequireMFA()` route denying a token without MFA with
  401 + RFC 9470 `WWW-Authenticate`; a provider user hitting a native step-up route
  gets a deny, pinned in the route-policy test.
- A bundled POST by an external credential runs exactly ONE `users.Get` and ONE
  `CheckRevoked` (the proof stamp works); a 1,000-request provider outage records
  ONE `external_revocation_unavailable` event; re-login after "revoke all" passes
  immediately (facts cache, not verdict cache).
- Migration `0020_external_subjects.sql` ships in pgx and turso with the same
  filename; `storetest.RunExternalSubjects` passes on pgx, turso and the two memory
  stores and pins that `Provision` persists `InitialCredentials.External`.
- `make check` and `make guard` green; RELEASING train entry drafted; the README
  decision table, support matrix, lag table and hash-export notes exist.

## Out of scope

- Native sessions for provider users (the handshake exchange) — deferred, D9.
- Firebase anonymous users (`AllowAnonymous`) — v1 DENIES them; identifier-less
  `Provision` is a contract change across four stores (follow-up F3).
- Auto-linking an external subject to an existing native account by email; the
  symmetric collision check inside native `Register`/OAuth/passwordless provisioning
  (follow-up F4, ruling R-SYMMETRIC).
- Refreshing a mirrored user's identifier on a later token (email verified at the
  provider after JIT) — follow-up F5; v1 documents the gap.
- Keycloak introspection and Clerk/Auth0/Cognito revocation adapters (follow-up F6).
- A pocket-less host (sdk middleware + tiny identities store) — ruling R-POCKET,
  follow-up F7 if ruled.
- Per-route fail-open; metrics port; shared-cache requirement in production.
- Importing provider password hashes as code (`firebase-scrypt` `Hasher`) — notes only.
- Native assurance options on native access tokens (`amr` on first-party JWTs).
- Promoting `auth-cms/.../outbound/authmem` to `stores/memory` (ruling R10).

## Design

### D1 — sdk: `cryptids.JWTVerifier`, a plain port in `sdk/pkg/cryptids` (rev 1, unchanged)

```go
// JWTVerifier verifies a bearer JWT issued by a party that is not this process:
// signature against the issuer's published keys, exp/nbf/iat with 60 seconds of
// clock tolerance, iss and aud pinned at construction, and an algorithm allow-list
// that never admits "none" or an HMAC method unless explicitly configured. Verify
// performs I/O (key discovery and refresh), hence ctx. It returns the verified
// claims; claim mapping and subject resolution belong to the consumer. It is
// distinct from oauth.IDTokenValidator, which is nonce-bound and lives in the login flow.
type JWTVerifier interface {
	Verify(ctx context.Context, token string) (map[string]any, error)
}
```

R1 (rev 1) stands: `pkg/cryptids`, no sdk conformance package; proof lives in the
integration's tests. `sdk/README.md` packages table gains the sentence.

### D2 — integration: `integrations/cryptids/go-oidc` (package `gooidc`) (rev 1, hardened)

Unchanged from rev 1 (`New(ctx, issuer, opts…)`, `WithJWKSURL`, `WithAudiences`,
`WithAlgorithms`, `WithHTTPClient`; `SkipExpiryCheck`/`SkipClientIDCheck` with the
adapter's own exp/nbf/iat 60 s and any-of audience; 16 KiB cap; `oidctest` dev
issuer; live leg). SRE findings folded:

- Cheap rejections before any fetch: size cap → `iss` equals the configured issuer
  → `alg` in the allow-list → then `kid` lookup.
- **Refetch floor per issuer** (30 s), single-flight, timed out, body capped
  (`io.LimitReader`, 1 MiB); the **last good key set is kept** when a fetch fails.
  The per-kid negative cache is bounded (LRU 256) — the floor is the real defense.
- Unknown-kid rejections logged aggregated per issuer per minute, never per token.
- Tests add: 1,000 distinct kids → ≤ 1 fetch per floor window; fetch failure → old
  keys still verify; Firebase emulator token (`alg: none`) rejected.
- `oidctest` gains `MintSessionCookie`-shaped helpers only in the sense of minting
  a token with a chosen `iss` (the dev issuer stands in for a provider session cookie
  by issuing a second issuer string) — no Firebase code.

### D3 — sdk: `sdk/capabilities/authentication` — the port (NEW, owner item 1)

Package `authentication`, imported as `sdkauth` (precedent: `sdk/capabilities/events`
is aliased `sdkevents` beside `pockets/events`). Imports root + `pkg/cryptids` only;
never `oauth` or `cacher` (G12c — the revocation cache lives in the pocket).

```go
type Transport string
const (
	TransportHeader Transport = "header"
	TransportCookie Transport = "cookie"
)

// Credential is a presented proof. Raw is never logged.
type Credential struct {
	Transport Transport
	Raw       string
}

// Identity is a verified external identity: who the provider says presented the
// credential and how well. It carries NO sdk.Principal and NO local user id —
// mapping to the host's principal belongs to the identity owner (pockets/README
// §5 criterion 3). Email/Phone carry their own verified flags, which is why the
// capability does not reuse root IdentityAddress (a projection of an already
// trusted record).
type Identity struct {
	Issuer          string            // the token's iss, verbatim (audit; routing key)
	Subject         string            // the issuer's sub, verbatim
	Email           string
	EmailVerified   bool
	Phone           string
	PhoneVerified   bool
	DisplayName     string
	Tenant          string
	AuthenticatedAt time.Time         // auth_time else iat
	ExpiresAt       time.Time
	Methods         []string          // amr verbatim (cloned on every hand-off)
	MFA             bool
	Attributes      map[string]string // Profile.Claims.Extra paths, string-valued, ≤16 entries
}

// Provider verifies one credential kind from one issuer.
type Provider interface {
	Issuer() string
	Authenticate(ctx context.Context, cred Credential) (Identity, error)
}

// RevocationFacts is what a provider knows about a subject's standing. The
// consumer compares ValidAfter to each token's AuthenticatedAt per request, so a
// cached answer never locks out a subject who just re-authenticated.
type RevocationFacts struct {
	Disabled   bool
	ValidAfter time.Time // zero → never revoked
	CheckedAt  time.Time
}

// RevocationChecker is optional; a provider that cannot answer does not implement it.
type RevocationChecker interface {
	CheckRevocation(ctx context.Context, issuer, subject string) (RevocationFacts, error)
}

// Errors: ErrInvalidCredential (wraps sdk.ErrUnauthorized), ErrProviderUnavailable
// (wraps sdk.ErrUnavailable — network, 5xx, deadline, provider rejected OUR
// credential: carries a Kind for logs), ErrIssuerMismatch (wraps ErrInvalidCredential).

// Profile is the vendor-neutral claim mapping for NewOIDC. Dotted paths; defaults
// in parentheses. No field names a vendor; presets supply vendor values.
type ClaimMapping struct {
	Subject       string   // ("sub")
	Email         string   // ("email")
	EmailVerified string   // ("email_verified")
	Phone         string   // ("phone_number")
	PhoneVerified string   // ("phone_number_verified"); "" with TrustPhone → verified when present
	DisplayName   string   // ("name")
	AuthTime      string   // ("auth_time"; falls back to "iat")
	AMR           string   // ("amr")
	MFAMethods    []string // amr values counting as MFA ("mfa","otp","hwk","sms","swk","pop")
	MFAFlag       string   // optional path whose non-empty/true value means MFA ("")
	Tenant        string   // optional ("")
	SignInMethod  string   // optional path naming how the subject signed in ("")
	Extra         []string // paths copied into Identity.Attributes (string-valued only)
}

type Profile struct {
	Claims          ClaimMapping
	TrustPhone      bool     // phone claim present ⇒ PhoneVerified
	DenySignIn      []string // SignInMethod values the provider refuses outright (e.g. a guest method)
}

func NewOIDC(issuer string, verifier cryptids.JWTVerifier, profile Profile) Provider
```

`NewOIDC.Authenticate`: 16 KiB cap → `verifier.Verify` → re-check `iss` on the
verified claims → mapping → `DenySignIn` → `Identity`. Vendor meaning stays out:
"anonymous" is a Firebase value the Firebase preset puts into `DenySignIn`, the
sdk field is a neutral `SignInMethod` path (steward finding).

`authenticationtest.RunProvider(t, newProvider, mint)` pins: valid → Identity with
Issuer == `Issuer()`; wrong iss → `ErrIssuerMismatch`; expired → `ErrInvalidCredential`;
oversize → `ErrInvalidCredential` without calling the verifier; `Methods` cloned;
no fabricated Identity on error. `RevocationChecker` contract pinned by
`authenticationtest.RunRevocation` (unknown subject → facts with `Disabled: true`
or `sdk.ErrNotFound`, never a silent "valid").

Graduation: passes `sdk/README.md` admission (plural implementations: NewOIDC over
go-oidc, the Firebase provider, a host's custom), the five-point test, and
`pockets/README.md` §5's five criteria (producer = sdk + integrations, consumer =
the pocket; canonical; no pocket aggregate; narrow; conformance). **R-SDK (YOUR
CALL):** ratify `sdk/capabilities/authentication` as a platform protocol. The
ARCHITECTURE "Protocols and pocket relationships" table gains a row phrased
**CONSUMES** (the pocket is not the implementation of record; integrations are).

### D4 — vendor presets: `integrations/authentication/<vendor>` (owner item 1, placement)

Why integrations, not sdk config helpers and not pocket recipes: ARCHITECTURE
§"The framework" — "Vendor-specific connectors are never sdk defaults even when
stdlib-implementable … a vendor connector's surface churns on the vendor's
schedule"; both rev 1 reviewers forbade vendor tables in the pocket core; the
category follows "capability category by default" (R-KV1). G13 discovers
`integrations/*/*/go.mod` by glob — no guard assumes category names. **Constraint:
integrations never import integrations (G13)** — presets return sdk values and
strings; `cmd` composes `gooidc.New(...)` + `sdkauth.NewOIDC(...)`.

**`integrations/authentication/firebase` (package `firebase`)** — isolates
`firebase.google.com/go/v4` plus the companions its API forces. Ruling R-FB
(YOUR CALL) chooses it over a REST-only module (`accounts:lookup` +
`x/oauth2/google`, smaller graph) because session cookies are verified against
x509 certificates at `https://www.googleapis.com/identitytoolkit/v3/relyingparty/publicKeys`
(not a JWKS, so go-oidc cannot verify them) and the SDK carries verification,
revocation and minting in three calls. Surface:

```go
func Profile(projectID string) sdkauth.Profile          // for hosts verifying ID tokens via go-oidc (header-only, no revocation)
func New(ctx, client *auth.Client, cfg Config) (*Provider, error)  // sdkauth.Provider over VerifyIDToken; Issuer() = "https://securetoken.google.com/<p>"
func NewSessionCookie(ctx, client, cfg) (*Provider, error)       // sdkauth.Provider over VerifySessionCookie; Issuer() = "https://session.firebase.google.com/<p>"
func (p *Provider) CheckRevocation(ctx, issuer, subject) (sdkauth.RevocationFacts, error) // GetUser → Disabled, TokensValidAfterMillis
func (p *Provider) MintSessionCookie(ctx, idToken string, ttl time.Duration) (string, error) // SessionCookie; ttl 5 min–14 d
```

Identity is built from `*auth.Token` fields (`Issuer`, `Subject`, `AuthTime`,
`Firebase.SignInProvider`, `Firebase.Tenant`, `Firebase.SignInSecondFactor`,
`Claims["email"/"email_verified"/"phone_number"/"name"]`) — the adapter does not
depend on the SDK's claims map carrying standard claims (lead finding; verified in
task-3). `DenySignIn` default `["anonymous"]`; phone trusted (Firebase sets
`phone_number` only after SMS possession). Production refuses construction when
the client reports emulator mode (`FIREBASE_AUTH_EMULATOR_HOST`); development logs
it. Credentials: ADC first (the firestore README posture); off GCP
`GOOGLE_APPLICATION_CREDENTIALS` or an `sdk.Secret` env field; a dedicated
service account with user-read only (verify `firebaseauth.users.get`); never the
default `firebase-adminsdk` account; `.env.example` placeholders; a credential
rejected by Google is its own error kind so on-call does not chase a Google
outage that is a rotated key. The `GetUser` payload (email, phone, provider UIDs,
custom claims) is never logged or cached whole.

**`integrations/authentication/auth0` (package `auth0`)** — zero libraries;
`Profile(domain string) sdkauth.Profile` + `Issuer(domain)` (trailing slash kept),
`org_id` tenant, the "access tokens are JWTs only when the SPA requests an API
audience" rule in the README, a `-tags=live` leg verifying one real token through
go-oidc. Precedent: `integrations/oauth/github` (sdk-only `go.mod`, isolates a
vendor contract). **R-AUTH0 (YOUR CALL):** module (recommended — the owner asked
for presets as code, and the module is where the vendor's revocation/session
code lands if it ever exists) vs README recipe (the lead's reservation: zero
behavior today).

A new guard `guard-auth-no-vendor-names` (grep `(?i)firebase|auth0|clerk|cognito|workos|keycloak|okta`
over production `.go` under `pockets/authentication` excluding `stores/`) pins
the rule (steward suggestion; README and tests excluded).

### D5 — pocket vocabulary, config and the engine (`logic/authentication`)

```go
const CredentialExternalToken CredentialKind = "external_token"

type Credential struct {
	// … existing fields unchanged; Issuer/Audiences/Profile stay ZERO for external tokens …
	Identity sdkauth.Identity // NAMED field (an anonymous embed would shadow Issuer); zero for native kinds
}

// ExternalProvider is one wired identity provider.
type ExternalProvider struct {
	Name       string                   // host-chosen mirror namespace ("firebase"); may repeat across entries
	Provider   sdkauth.Provider         // required; Issuer() unique across entries and ≠ DelegatedTokens.Issuer
	Revocation sdkauth.RevocationChecker // optional; nil → local-status-only re-checks
	CookieName string                   // optional: the provider's own session cookie (D9)
	ProvisionOnFirstVerification bool   // JIT (R6: zero value closed; recipes set true)
	RequireVerifiedEmail bool           // deny a token whose email is present but unverified
	AllowedTenants []string             // R-TENANT: non-empty → Identity.Tenant must match
	RevocationFailOpen bool             // D8; production WARN at construction
}

type ExternalConfig struct {
	Providers           []ExternalProvider
	RevocationCache     cacher.Storer  // nil → cacher.NewMemory(WithMaxEntries(cfg.RevocationCacheEntries)); namespaced "authext"
	RevocationCacheTTL  time.Duration  // 30s
	RevocationOutageGrace time.Duration // 5m: a stale fact set is served this long before the outage path
	RevocationTimeout   time.Duration  // min(2s, request deadline); error if ≤0 or ≥10s
	RevocationCacheEntries int         // 10_000
}
func WithExternal(value ExternalConfig) Option // on authlogic.New and the root New (root forwards)
```

Validation at construction: at least one provider when wired; issuers unique and
absolute; `Revocation` typed-nil check; `Repositories.ExternalSubjects` required
when wired (mirrors `ErrOAuthReposRequired`); production `IdentifierKeyer` when any
provider provisions; TTL > 0, grace ≥ TTL, timeout coherent; `CookieName` ≠ the
native session, refresh and `__Host-auth_csrf` names (checked where the adapter
knows them — `NewAuthenticator` receives the names through `WithExternalCookies`).

**The engine** (`logic/authentication/external.go`) is the rev 1 A′ shape, placed
in the same package because the proof stash is unexported (`context.go:30-41`) and
a sibling package could neither read it nor avoid a second stash seam (steward,
lead). `credentialProof.owner` becomes `any`; `credentialProof` gains an
`externalLiveAt time.Time` stamp so a nested `floor` gate never re-runs the
external re-check (lead risk 2). Package-level helpers extracted from `*Service`
so both owners share them: `recordSecurityEvent`, `projectAddresses`,
`resolvePendingInvitations` (list in task-4).

```go
type ExternalAuthenticator struct { /* providers by issuer, repos, cache, clock, logger, stats */ }
func NewExternalAuthenticator(repos ExternalRepositories, cfg ExternalConfig, runtimeMode environment.Mode, opts ...ExternalOption) (*ExternalAuthenticator, error)
type ExternalRepositories struct {
	Users            user.UserRepository                  // required
	Identifiers      identifier.IdentifierRepository      // required
	ExternalSubjects externalsubject.Repository           // required
	SecurityEvents   securityevent.SecurityEventRepository // optional
	Invitations      invitationResolver                   // optional (pending auto-accept after JIT)
}
func (e *ExternalAuthenticator) Verify(ctx, raw string, transport Transport) (Principal, Credential, error)
func (e *ExternalAuthenticator) CheckLive(ctx, principal Principal, cred Credential, tier LivenessTier) error
func (e *ExternalAuthenticator) ClassifyBearer(raw string) (CredentialKind, bool)
func (e *ExternalAuthenticator) RevocationStats() RevocationStats
// External-only hosts: *ExternalAuthenticator implements authenticationhttp.CredentialService
// (its own stash under the shared key, MachineEnabled()==false, SessionLifetime()==0) and sdk.IdentityResolver.
```

`Verify` pipeline: 16 KiB cap → unverified `iss` peek selects ONE provider (never a
second attempt) → `Provider.Authenticate` → `AllowedTenants`, `RequireVerifiedEmail`
→ `ExternalSubjects.Get(name, sub)` → `Users.Get` + `Active()` (the local kill
switch; fail closed on a read error with `sdk.ErrUnavailable`) → else JIT (D6) →
`Principal{user, id}` + `Credential{Kind: external_token, Transport, Identity}`
(Methods cloned at stash time).

Hybrid hosts: `authentication.New(..., WithExternal(cfg))` embeds the engine;
`Service.Authenticate` gains the external branch; `Service.ClassifyBearer`: no
`iss` or `iss == DelegatedTokens.Issuer` → access token; `iss` ∈ wired providers →
external; else `false`.

**R-SEAM (YOUR CALL, owner item 8).** The credential METHODS are already optional
by flag/absence (`PasswordFlowsDisabled`, `Passwordless` empty, `Providers` empty);
what is NOT optional is the native session rail (`Sessions`/`ActiveSessions`/`TokenSigner`
hard-required at `logic/authentication/constructor.go:73-80` and dereferenced in
`refresh.go`, `stepup.go:177`, `service.go`). Options: (A) A′ now — no change to
`*Service`; external-only hosts use the engine directly; hybrid hosts still wire
an unused signer + sessions (tables already exist). (B) a real seam: extract a
`nativeSessions` rail struct, nil for external-only hosts, every native use case
failing closed with `ErrNativeSessionsDisabled`, bundled native routes
deny-by-absence — sized at **2 PRs** (PR-1 rail extraction + constructor relax +
route gating + nil audit of the ~71 exported methods; PR-2 tests + README + a
`guard` grep that no native method dereferences the rail unguarded). Recommend
**A′ for v1**, (B) as follow-up F8 when the first hybrid host objects to the dead
wiring; the flag style stays for methods.

### D6 — the subject mirror and JIT (rev 1, re-keyed)

**R-MIRROR (YOUR CALL) — new table, not `oauth_accounts`.** Evidence:

| aspect | `oauth_accounts` (0004) | the mirror needs |
|---|---|---|
| key | `(provider, provider_user_id)` where `provider` is the host's login-provider NAME ("google"); the OAuth flow keys reads on the configured provider (`oauth.go:337,608`) | `(provider_name, subject)` — same shape, but the rows must never be visible to the OAuth flow |
| columns | encrypted `access_token`/`refresh_token`, `token_expires_at`, `token_type`, `scope`, `account_verified`, `provider_email_verified` | no tokens, no adoption state; `issuer` (informational), `email`, `first_seen_at` |
| write semantics | `Link(ctx, a, expectedAuthRevision, adoptIdentifierID, now)` adopts a squatter (retires identifiers, revokes sessions, bumps `auth_revision`) | a plain insert inside `Provision`, and an admin `Link` to an ACTIVE user with no adoption |
| consumers | `credential.MutationRepository.Snapshot` projects `oauth_accounts` into `MethodSet.OAuth` (firestore SCHEMA.md:188, pgx `credential_mutations.go`), `Methods` lists them as removable login methods, `UnlinkOAuth` + the unlink routes delete them under step-up | mirror rows must NOT appear as login methods, must not be unlinkable by the user, and must survive a provider switch untouched |
| firestore | doc id `h(provider, provider_user_id)` (oauth_doc.go) | the same derivation would work; parity is deferred (R8) |
| multi-tenant | n/a | tenant is a claim (`Identity.Tenant`), the key stays `(name, sub)`; Firebase `sub` is unique per project, Auth0 `sub` per tenant domain |

Reusing it would make every Firebase user show up in `GET /auth/methods` as an
OAuth method with a removable hint and a working unlink route. Separate table.

Port `logic/authentication/externalsubject` (sibling of `oauthaccount`):

```go
type ExternalSubject struct {
	Provider, Subject, UserID string
	Issuer      string    // last iss that produced this row (informational)
	Email       string    // normalized at provisioning; informational
	FirstSeenAt time.Time
}
type Repository interface {
	Get(ctx, provider, subject string) (ExternalSubject, error)      // sdk.ErrNotFound
	Link(ctx, s ExternalSubject) (ExternalSubject, error)            // existing ACTIVE user; taken → sdk.ErrAlreadyExists
	ListByUser(ctx, userID string) ([]ExternalSubject, error)
	Unlink(ctx, provider, subject string) error                      // sdk.ErrNotFound when absent
}
```

Why the key is the host NAME and not the token `iss` (lead risk 5): Firebase ID
tokens (`https://securetoken.google.com/<p>`) and session cookies
(`https://session.firebase.google.com/<p>`) carry different `iss` for one `sub`;
Auth0 custom domains vs tenant domains likewise. Two `ExternalProvider` entries
share a `Name`; routing stays by `iss`.

JIT, when `(name, sub)` is unknown and `ProvisionOnFirstVerification`:

1. `user.NewUser(ids, displayName, now)`.
2. Identifier: email → kind email, **notification-only**, verified iff
   `EmailVerified`; phone-only → kind phone, notification-only, verified iff
   `PhoneVerified`; neither → **deny** in v1 (`external_subject_denied`, reason
   `no_identifier`; anonymous and identifier-less subjects are follow-up F3 because
   `Provision` inserts an identifier unconditionally in every store).
3. **Collision (R5, rev 1):** the normalized email/phone is looked up among ACTIVE
   login/recovery identifiers (`GetLogin`/`GetRecovery`) regardless of the token's
   verified flag; a hit → deny + `external_subject_collision` + an explicit admin
   `ExternalSubjects.Link` is the only way forward. Auto-link stays out.
4. `Users.Provision(ctx, u, ident, InitialCredentials{External: &subject})` — one
   transaction; a lost race → `sdk.ErrAlreadyExists` → re-`Get` once.
5. **Re-`Get` the mirror after `Provision` and fail closed if absent** — a host
   `UserRepository` that ignores the new field would otherwise JIT a new user per
   request (lead risk 8); storetest pins persistence.
6. Verified email → `resolvePendingInvitations` (pending auto-accept, the native
   verify path's behavior) when `Invitations` is wired.
7. `external_subject_provisioned` on the audit rail.

JIT off → deny + `external_subject_unknown`. No `last_seen_at`. Switching
providers later is `UPDATE external_subjects SET provider=…, subject=…`.

**R-SYMMETRIC (YOUR CALL):** native `Register`/OAuth/passwordless provisioning
checks only the login/recovery index, so a Firebase user who JITs first and then
registers natively with the same email gets a second account (lead risk 6).
Recommend **document in v1, follow-up F4** (the same gap exists today for every
notification-only identifier; closing it touches three native flows).

### D7 — assurance options (rev 1 D8, claims source corrected)

```go
func RequireMFA() PrincipalOption                 // cred.Identity.MFA
func FreshAuth(within time.Duration) PrincipalOption // now - cred.Identity.AuthenticatedAt <= within
func VerifiedEmail() PrincipalOption              // cred.Identity.EmailVerified
func Tenant(ids ...string) PrincipalOption         // cred.Identity.Tenant ∈ ids (exact)
func Check(fn func(ctx context.Context, cred Credential) error) PrincipalOption // reads Identity.Attributes; no raw claims exist
```

Rules as rev 1: they evaluate on external tokens; API keys pass; a set admitting
native access tokens together with an assurance option panics at wiring time;
denial adds RFC 9470 `WWW-Authenticate: Bearer error="insufficient_user_authentication"`
(+ `max_age` for `FreshAuth`) on JSON sets; `Browser()` keeps the 303.
**Roles/permissions claims are never read** (owner item 7): `ClaimMapping` has no
field for them; `Attributes` is identity data a host maps in host code; the README
says so in bold. **R-TENANT (YOUR CALL):** `AllowedTenants` per provider at
construction (recommended — a multi-tenant Auth0 org cannot be wired open by
omission; `Tenant(...)` remains the per-route narrowing).

### D8 — liveness as a per-route axis; revocation cache and fail mode (owner item 5)

`principalSet.live bool` becomes `liveness LivenessTier`:

| option | native access token | API key | delegated token | external token |
|---|---|---|---|---|
| `Live()` | session PK read, every request | pass (DB-checked at resolution) | live grant (unchanged, always) | local `users.Get`+`Active()` + provider facts; **always fail-closed**, ignores `RevocationFailOpen` |
| `LiveOnMutation()` (**default**) | session read on POST/PUT/PATCH/DELETE; GET/HEAD/OPTIONS stateless | pass | always | re-check on unsafe methods only; honors `RevocationFailOpen` |
| `Stateless()` | never | pass | always | never (token validity only; `users.Get` still ran at resolution) |

External re-check = (1) `users.Get`+`Active()` — never cached; (2) when the
provider implements `RevocationChecker`: facts from the cache (key
`authext:<name>:<sub>`, value `{disabled, valid_after, checked_at}` stored with
ttl = `RevocationOutageGrace`, freshness = `checked_at + RevocationCacheTTL`) or a
single-flight provider call; verdict computed PER TOKEN: `disabled` or
`Identity.AuthenticatedAt < valid_after` → deny `ErrRevoked` → 401. A cacher error
is a MISS (reported via `cacher.WithOnError`), never the outage path (Redis down
must not become an auth outage).

Outage (`ErrProviderUnavailable`): serve the stale facts while within the grace
window (WARN once per episode); past it → **503 + `Retry-After: 5`** on the
default tier and `Live()`; the per-provider `RevocationFailOpen` relaxes only
`LiveOnMutation()`: the request passes on validity + local status, WARN once per
episode, ONE `external_revocation_unavailable` security event per (provider,
episode) with a count — never one per request. **Fail-closed is the planner's
call (owner delegated it)**: per-provider, not per-route, because
availability-vs-security is a judgment about one identity population made at the
composition root; `Live()` is the route-level severity lever.

Lag bounds (README table): safe reads under the default → up to token `exp`;
mutations, healthy provider → ≤ TTL; mutations, outage, fail-closed → ≤ grace
then 503; mutations, fail-open → up to `exp`; providers without a checker → `exp`
plus the local kill switch on every mutation.

**Infrastructure errors become 503 for native credentials too** (SRE + lead):
`credentialSessionLive` today folds a store read error into the same `false` as a
missing row (`machine.go:345-347`); with the error channel a read error is
`sdk.ErrUnavailable` → 503, a missing/expired row stays 401. `Browser()` on a 503
renders the JSON 503 (not the login redirect); `Optional()` on a 503 is a 503, not
anonymity.

Observability: edge-triggered logs per provider (ERROR healthy→unavailable with
error kind; WARN stale-serving starts; WARN fail-open starts; INFO recovery with
duration and affected count); `RevocationStats()` (hits, misses, stale_served,
fail_open, fail_closed, provider_errors, cacher_errors, p50/max latency) + opt-in
`WithHeartbeat` (the workers #32 precedent). Field hygiene: provider name, local
user id, outcome enum, latency, error kind — never the token, claims, email or
provider payload. Production: one WARN per provider with `RevocationFailOpen`.
Nothing is production-required (every default fails safe). README documents the
provider QPS formula (instances × distinct mutating users per TTL) against
Firebase's Identity Toolkit quota and the cache capacity knob.

**R-LIVE (YOUR CALL):** the `LiveOnMutation()` default applies to NATIVE access
tokens too (recommended: one rule for every kind; bundled mutation routes are
already `Live()`; the lead found no auth-cms route that breaks; the 503 channel
makes it SPA-safe; cost = one session PK read on host POST routes that were
stateless — named in RELEASING) vs external-only (native stays `Stateless()` by
default; the per-kind default is the API wart).

### D9 — server-rendered pages: the provider's own session mechanism (owner item 4)

The pocket's cookie transport reads, in order, the native session cookie, then
each wired `ExternalProvider.CookieName` — **first present wins** (a present but
invalid native cookie denies with `recover=1` and never reads a provider cookie;
hybrid hosts clear the native cookie on logout, which the bundled logout does).
`credentialPresented` mirrors the same reads so `Optional()` never launders a
revoked provider cookie into anonymity. Cookie-borne external tokens are JWTs
verified like header tokens (transport-tagged).

Bundled routes: every bundled mutation group runs `requireBrowserSafeMutation`
(double-submit `__Host-auth_csrf` + `X-CSRF-Token`); the bootstrap `GET /auth/csrf`
moves to a new bundled group `CSRFBootstrap` (default `RequirePrincipal(Live())`,
every wired kind) so a provider-cookie user can obtain it; `SessionSecurityReads`
(`/auth/delivery/status`, `/auth/methods`) keeps `Accept(CredentialAccessToken)`.
Host HTML routes keep the ratified stdlib `CrossOriginProtection` posture
(ARCHITECTURE "Host HTML cross-origin posture"); provider cookies must be
`HttpOnly; Secure; SameSite=Lax`. `Browser()` on a set admitting external tokens
panics at wiring time unless `WithBrowserLoginPath` was configured explicitly
(the default `/auth/login` is not mounted on an external-only host), and `recover`
is never requested for an external-cookie failure.

**Support matrix (researched 2026-10-20):**

| vendor | server-session mechanism | gopernicus v1 | revocation signal | sources |
|---|---|---|---|---|
| Firebase Auth | `createSessionCookie(idToken, expiresIn)` (5 min–2 weeks), `verifySessionCookie(cookie, checkRevoked)`; iss `https://session.firebase.google.com/<projectId>`; keys (x509) `https://www.googleapis.com/identitytoolkit/v3/relyingparty/publicKeys`; Go `SessionCookie`, `VerifySessionCookie[AndCheckRevoked]`, `RevokeRefreshTokens` | **cookie-capable** via the firebase module; host declares a `SessionCookieMinter` port, `cmd` injects `*firebase.Provider` (H7) | `GetUser`: `Disabled`, `TokensValidAfterMillis` vs `auth_time` | https://firebase.google.com/docs/auth/admin/manage-cookies ; https://firebase.google.com/docs/auth/admin/verify-id-tokens |
| Clerk | `__session` cookie on same-origin requests (short-lived JWT, lifetime from the JWT template; `azp`, `sub`, `sid`, `iss` = Frontend API URL); JWKS `https://api.clerk.com/v1/jwks` or `<frontend-api>/.well-known/jwks.json`; no server-minted long-lived session | **cookie-capable** generically: `CookieName: "__session"` + `Check` on `azp`; `FreshAuth` meaningless | none (TTL-bounded) — follow-up | https://clerk.com/docs/backend-requests/handling/manual-jwt ; https://clerk.com/docs/backend-requests/resources/session-tokens |
| Auth0 | three layers: application session (yours — "nothing within your Auth0 tenant" is involved), Auth0 SSO session cookie at the Auth0 domain (not verifiable by a backend), IdP session; no mint/verify API for an app session | **header-only** (JWT access token with an API audience); SPA/SDK keeps its own session | none for access tokens (TTL-bounded) | https://auth0.com/docs/manage-users/sessions/session-layers |
| Cognito | no server session; verify ID/access JWTs against `https://cognito-idp.<region>.amazonaws.com/<pool>/.well-known/jwks.json`; access tokens carry `client_id` not `aud` | **header-only** (recipe pins the ID token or `WithAudiences(clientID)`) | none (TTL-bounded) | https://docs.aws.amazon.com/en_en/cognito/latest/developerguide/amazon-cognito-user-pools-using-tokens-verifying-a-jwt.html |
| Okta | Sessions API (`POST /api/v1/sessions`, `sessionToken`) creates an OKTA-domain `sid` cookie, "not encouraged"; the app session is the app's own; verify ID tokens via JWKS | **header-only** | `/introspect` for Org authorization servers — follow-up | https://developer.okta.com/docs/guides/validate-id-tokens/main/ ; https://developer.okta.com/docs/guides/session-cookie/main/ |
| Keycloak | no app-session API; local JWT validation cannot see revocation; `POST /realms/{realm}/protocol/openid-connect/token/introspect` (confidential client) can; backchannel logout exists | **header-only** | introspection — follow-up F6 (per-token; cache key = token hash, never the raw token) | https://skycloak.io/blog/keycloak-token-introspection-vs-local-validation/ |

**Deferred alternative — the native exchange** (`POST /auth/external/handshake`:
provider token → gopernicus session + cookie): gives step-up, `/auth/sessions`,
and the existing `Live()` tier to provider users, at the cost of TWO sessions
(provider + native) with independent lifetimes and a revocation lag equal to the
native refresh horizon unless the provider check is repeated at refresh. Not v1.

### D10 — the hybrid chain in `inbound/http` (owner item 6; rev 1 D7, corrected)

`resolveCredential`: a bearer header is authoritative. JWT-shaped →
`kind, ok := ClassifyBearer(raw)` (`!ok` → deny; kind outside the set → deny) →
`Authenticate(ctx, kind, TransportHeader, raw)`. Dotless → API key (machine
subsystem wired and admitted). Then cookies per D9. Never two attempts.

`principalSet` gains `externalToken bool`. **The default set admits every WIRED
kind** — the documented definition of `defaultSet` ("every WIRED credential kind")
— so external tokens join it when providers are wired (`ExternalEnabled()`). The
named helpers stay **literal**: `RequireAccessTokenOrAPIKey()` becomes
`RequirePrincipal(Accept(CredentialAccessToken, CredentialAPIKey))` and its `Live`
sibling likewise; new `RequireExternalToken()`, `RequireExternalTokenOrAPIKey()`.
`admits` gains `case CredentialExternalToken`: rejects `FirstParty`, `Audience`,
`DelegatedAudience`; checks transport against the set. Kind + transport suffice for
nested narrowing; assurance options read `cred.Identity` from the stash.

Bundled route audit (`route_policy.go`, pinned in `policy_test`): established-session
surfaces (`OAuthLinkStart`, `SessionSecurityReads`, `CredentialManagement`,
`MachineLifecycle`, `BrowserAccount`) get explicit `Accept(CredentialAccessToken)`
— a provider user hitting native step-up (`stepup.go:177` reads `s.sessions`) gets a
deny, never a panic; `SessionHydration` (`/auth/me`), `UserAdministration`,
`Invitations` and the new `CSRFBootstrap` default to `RequirePrincipal(Live())`
(every wired kind). The `floor` (`routes.go:163`) admits external because the
default set does — the coupling is pinned by a test.

`CredentialService` (one break, v0.19.0): `Authenticate(ctx, kind, transport,
raw) (context.Context, error)`, `RequireLive(ctx, tier LivenessTier, unsafe bool)
(context.Context, error)`, `+ ClassifyBearer(raw) (CredentialKind, bool)`,
`+ ExternalEnabled() bool`. Errors: `sdk.ErrUnauthorized`-class → 401/303,
`sdk.ErrUnavailable`-class → 503. Provider cookie names reach the adapter through
`WithExternalCookies(names ...string)` (an `AuthenticatorOption`, validated against
the native/refresh/CSRF names), so the interface does not grow for cookies.

### D11 — identity edge cases, audit, step-up, invitations, lock-in

| case | policy |
|---|---|
| Firebase anonymous (`firebase.sign_in_provider == "anonymous"`) | the preset's `DenySignIn` refuses it → `external_subject_denied` (reason `sign_in_method`). `AllowAnonymous` + identifier-less `Provision` is F3. An anonymous→email upgrade keeps the uid, so the first ACCEPTED token is the upgraded one and JIT runs then. |
| phone-only (Firebase `phone_number`, no email) | identifier kind phone, notification-only, verified iff `PhoneVerified` (Firebase preset: `TrustPhone`). Collision check against login/recovery phone rows. |
| unverified email | identifier notification-only, UNVERIFIED (`Resolve` omits it; invitations cannot match it). Per-provider `RequireVerifiedEmail` denies at verification; per-route `VerifiedEmail()` denies at the gate. Collision check runs on the address regardless (denying the attacker, not the victim). Verified-later refresh is F5. |
| collision with a native login/recovery email | deny + `external_subject_collision` + admin `Link` (R5). |
| audit thinning | gopernicus records: `external_subject_provisioned`, `external_subject_unknown`, `external_subject_collision`, `external_subject_denied` (reason: tenant, sign-in method, unverified email, inactive, revoked), `external_revocation_unavailable` (deduped per episode). NOT recorded: per-request successful authentication (native access tokens do not record it either), provider sign-ins, password/MFA events — those live at the provider; the README links each vendor's audit log and says the gap aloud. |
| step-up / session management | `RequireRecentAuthentication`, `/auth/sessions`, credential management need a native session — **a deliberate loss** for provider users; the bundled surfaces deny them (D10); hosts use `FreshAuth(within)` + the provider's re-authentication (`prompt=login`, `acr_values`), and the RFC 9470 header tells a SPA to do so. |
| invitations | accept works today for a verified JIT email (`ActiveVerifiedIdentifier`); JIT calls `resolvePendingInvitations` for a verified email (D6 step 6); direct-add at invite-create time does NOT see external users (`userLookup` is login/recovery-only) — they get a pending invitation instead; the bundled `Invitations` group admits every wired kind. |
| lock-in | README "Leaving a provider": Firebase `auth:export` yields modified-scrypt hashes with a project `hash_config` (signer key, salt separator, rounds, mem_cost) — needs a dedicated `Hasher` (follow-up) or a reset campaign (https://firebase.google.com/docs/cli/auth); Auth0 exports bcrypt `$2b$10$` hashes as NDJSON only via a support ticket, paid plans, PGP-encrypted — importable into the native `Passwords` rail unchanged (https://support.auth0.com/center/s/article/How-to-Use-the-Password-Hashes-Export-from-Auth0 ; https://auth0.com/docs/manage-users/user-migration/user-migration-scenarios); Cognito cannot export hashes — reset campaign or verify-at-login bridge (https://aws.amazon.com/blogs/security/approaches-for-migrating-users-to-amazon-cognito-user-pools/); Clerk/Okta: verify with the vendor (unverified here); Keycloak: self-hosted DB. Provider switch recipe: remap `external_subjects.provider/subject`, no FK moves. |

## Schema / datastore impact

`0020_external_subjects.sql`, identical filename in `stores/pgx/migrations` and
`stores/turso/migrations` (the `0017` gap stays). Conventions from `0004`/`0014`:
no FK to `users`, `TIMESTAMPTZ` vs fixed-width TEXT, named index.

```sql
-- pgx
CREATE TABLE IF NOT EXISTS external_subjects (
    provider      TEXT NOT NULL,          -- host-chosen provider name (the mirror namespace)
    subject       TEXT NOT NULL,          -- the issuer's sub, verbatim
    user_id       TEXT NOT NULL,          -- references users.id by convention
    issuer        TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider, subject)
);
CREATE INDEX IF NOT EXISTS idx_external_subjects_user_id ON external_subjects (user_id);
```

- `user.InitialCredentials` gains `External *externalsubject.ExternalSubject`;
  `Users.Provision` in pgx/turso/authmem/memstore inserts it in the same
  transaction (collision → `sdk.ErrAlreadyExists`, whole transaction absent).
- **R9 (rev 1):** add `external_subjects` to the boot-time probe (`postgres.go:102-109`,
  `turso.go:126`) — recommended (fails at boot, not at the first external request).
- `storetest.RunExternalSubjects`: Get/Link/ListByUser/Unlink sentinels;
  `Provision` with `External` atomicity AND persistence (a store that drops the
  field fails the suite); `Link` to an inactive/missing user fails.
- **R8 (rev 1):** Firestore — documented nil this release (`stores/firestore/README.md:35`
  precedent); `Repositories.ExternalSubjects` nil with providers wired is a loud
  construction error.
- Inventory text: `pockets/authentication/README.md` ("0001–0019" → 0020),
  `stores/pgx/README.md`, `stores/turso/README.md`, `stores/turso/migrations_test.go`,
  firestore README.

## Module / API impact

- New: `sdk/capabilities/authentication` (+ `authenticationtest`);
  `integrations/cryptids/go-oidc`; `integrations/authentication/firebase`;
  `integrations/authentication/auth0`; `examples/external-identity`. Each new module:
  `go.work` `use`, Makefile `MODULES`; the example joins `guard-host-contract`'s
  host list (`Makefile:617`); `make tidy` + `make check`.
- `pockets/authentication/go.mod` stays `sdk` + `pockets` (FS1); pins sdk v0.14.0
  via local `replace` pre-tag.
- Semver (pre-1.0 convention per RELEASING.md "Version bumps": breaking changes
  ship as MINOR with a BREAKING callout — precedent authentication v0.9.0):

| module | current | proposed | why |
|---|---|---|---|
| `sdk` | v0.13.0 | **v0.14.0** MINOR | new capability + `cryptids.JWTVerifier` (additive) |
| `integrations/cryptids/go-oidc` | — | v0.1.0 | new |
| `integrations/authentication/firebase` | — | v0.1.0 | new (Admin SDK graph) |
| `integrations/authentication/auth0` | — | v0.1.0 | new (sdk-only) |
| `pockets/authentication` | v0.18.0 | **v0.19.0** MINOR, **BREAKING** | `CredentialService` signatures → `(ctx, error)` + 2 methods; default set admits wired external; `RequireAccessTokenOrAPIKey*` literal; bundled defaults (`CSRFBootstrap` new group, `SessionHydration`/`UserAdministration`/`Invitations` → every wired kind); R-LIVE default for native POST routes; store read error → 503 on `Live` paths; `Credential`, `Repositories`, `InitialCredentials` grow (additive) |
| `pockets/authentication/stores/pgx` | v0.8.0 | v0.9.0 MINOR | migration 0020 + repository + probe |
| `pockets/authentication/stores/turso` | v0.7.0 | v0.8.0 MINOR | same |
| `pockets/authentication/stores/firestore` | v0.2.0 | untouched | documented nil |
| `pockets/authentication/views/goth` | v0.6.0 | repin only if a test needs it | no runtime change |
| `workshop/gopernicus` | v0.4.0 | untouched | guard host list is Makefile-side |

Hosts that wire no provider see: the error-channel break (compile-time, if they
implement `CredentialService` — none known), the R-LIVE read on stateless POST
routes, and the 503 path on store errors. gps-360-go, the hub and Segovia get a
RELEASING heads-up naming all three. Tag train: sdk → go-oidc → firebase/auth0 →
authentication → stores pgx/turso.

## Generated-artifact impact

None. No `.templ` is touched; the proof hosts are JSON-only.

## Risks

1. **A verdict cache locks out a re-authenticated user / admits a pre-revocation
   token.** Mitigation: cache FACTS, compare per token (D8); DoD test "re-login after
   revoke-all passes immediately".
2. **A provider outage becomes a database outage** through per-request security
   events. Mitigation: deduped per episode; DoD test "1,000-request outage → one event".
3. **One human, two accounts** — Firebase ID token vs session cookie `iss` (fixed by
   the name-keyed mirror), native-after-JIT registration (R-SYMMETRIC, documented),
   a host store dropping `InitialCredentials.External` (re-`Get` + storetest).
4. **Provider-cookie users locked out of bundled mutations by CSRF** (`/auth/csrf`
   unreachable). Mitigation: the `CSRFBootstrap` group; a test drives a cookie-borne
   invitation accept end to end.
5. **`Live()` silently passes external tokens** via the `cred.Kind != access_token`
   early return (`machine.go:328`). Mitigation: the external branch is placed first
   and pinned; the nested-floor single-read test proves the stamp.
6. **JWKS amplification / fetch failure emptying keys.** Mitigation: per-issuer
   floor, bounded negative cache, last-good keys, the spray test.

## Tasks

### task-1: `cryptids.JWTVerifier` port + `sdk/capabilities/authentication`

- **depends_on:** []
- **model:** opus
- **files:** `sdk/pkg/cryptids/jwt.go`, `sdk/pkg/cryptids/jwt_test.go`, `sdk/capabilities/authentication/authentication.go` (Transport, Credential, Identity, Provider, RevocationChecker, RevocationFacts, errors), `sdk/capabilities/authentication/profile.go` (ClaimMapping, Profile, dotted-path lookup, defaults), `sdk/capabilities/authentication/oidc.go` (NewOIDC), `sdk/capabilities/authentication/oidc_test.go` (fake verifier; iss re-check; DenySignIn; Attributes bound; Methods clone), `sdk/capabilities/authentication/authenticationtest/provider.go` (RunProvider, RunRevocation), `sdk/capabilities/authentication/doc.go`, `sdk/README.md` (packages table), `ARCHITECTURE.md` (JWTVerifier sentence; the CONSUMES row in "Protocols and pocket relationships"; capabilities list)
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard` (G12b/c must pass with the new capability; it imports root + pkg/cryptids only)
- **description:** Land D1 and D3. `Identity` carries no principal and no user id; no field or default names a vendor (the test greps the package for the vendor names in D4's guard). `RunProvider` is driven by a stdlib fake verifier so the suite needs no network.

### task-2: `integrations/cryptids/go-oidc` + `oidctest`

- **depends_on:** [task-1]
- **model:** opus
- **files:** `integrations/cryptids/go-oidc/{go.mod,go.sum,gooidc.go,options.go,timechecks.go,keyset.go,gooidc_test.go,spray_test.go,live_test.go,README.md}`, `integrations/cryptids/go-oidc/oidctest/{issuer.go,issuer_test.go}`, `go.work`, `Makefile` (`MODULES`), `workshop/documentation/docs/integrations/go-oidc.md`
- **verify:** `cd integrations/cryptids/go-oidc && go build ./... && go test ./... && go vet ./...` then `make tidy && make guard && make check`; live: `OIDC_LIVE_ISSUER=… OIDC_LIVE_AUDIENCE=… OIDC_LIVE_TOKEN=… go test -tags=live ./...` recorded in a dated NOTES entry
- **description:** Implement D2 with the hardening list: per-issuer refetch floor (30 s), single-flight, timed-out and size-capped JWKS fetch, last-good key set retained, bounded negative kid cache, cheap-rejection order, aggregated unknown-kid logging. `oidctest` can mint under a second issuer string so hosts can simulate a provider session cookie. Tests cover every rev 1 edge plus the spray, fetch-failure and `alg: none` cases; `authenticationtest.RunProvider` runs over `sdkauth.NewOIDC(oidctest issuer, gooidc verifier, Profile{})`.

### task-3: vendor presets — `integrations/authentication/firebase` and `auth0`

- **depends_on:** [task-1]
- **model:** opus
- **files:** `integrations/authentication/firebase/{go.mod,go.sum,firebase.go,profile.go,revocation.go,session.go,firebase_test.go,live_test.go,README.md,.env.example}`, `integrations/authentication/auth0/{go.mod,go.sum,auth0.go,auth0_test.go,live_test.go,README.md}`, `go.work`, `Makefile` (`MODULES`), `workshop/documentation/docs/integrations/{firebase,auth0}.md`
- **verify:** per module `go build ./... && go test ./... && go vet ./...`; `make tidy && make guard` (G13: sdk + self only — neither module may import go-oidc) `&& make check`; firebase live: `FIREBASE_PROJECT_ID=… GOOGLE_APPLICATION_CREDENTIALS=… FIREBASE_LIVE_ID_TOKEN=… go test -tags=live ./...` (verifies an ID token, mints + verifies a session cookie, reads revocation facts, and proves a token minted before `RevokeRefreshTokens` is reported `ValidAfter`-revoked); emulator leg: `FIREBASE_AUTH_EMULATOR_HOST=… go test -tags=emulator ./...` proves plumbing only and that production construction REFUSES emulator mode; auth0 live: `AUTH0_DOMAIN=… AUTH0_LIVE_TOKEN=… go test -tags=live ./...` through go-oidc (test-only import of go-oidc is permitted by G13? — NO: keep the auth0 live test in `examples/external-identity`'s `cmd` tests or a `workshop/` script; the module's own live leg validates only `Profile` mapping against a decoded token fixture)
- **description:** Implement D4. The Firebase provider builds `Identity` from `*auth.Token` fields, never from the claims map alone (verify what `Token.Claims` carries and record it in the README); `DenySignIn` defaults to `["anonymous"]`; `TrustPhone` true; ADC-first credential guidance, least-privilege service account, `.env.example` placeholders, the rotated-key error kind, and "never log/cache the GetUser payload". The auth0 README names the pinned contract (trailing-slash issuer, `org_id`, JWT-only-with-API-audience).

### task-4: pocket vocabulary, mirror port, config, engine, liveness

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/authentication/logic/authentication/credential.go` (kind + `Identity` field; LivenessTier), `context.go` (`owner any`; `externalLiveAt`; Methods clone), `externalsubject/{externalsubject.go,repository.go}`, `user/repository.go` (`InitialCredentials.External`), `external_config.go` (ExternalProvider, ExternalConfig, WithExternal, validation), `external.go` (engine: Verify, CheckLive with the facts cache + single-flight + grace + fail-open, ClassifyBearer, RevocationStats, WithHeartbeat, standalone CredentialService + sdk.IdentityResolver methods, JIT with collision + re-Get + pending-invitation resolution), `external_test.go` (fake Provider/RevocationChecker/cacher; every pipeline branch; facts-vs-verdict; outage dedupe; cacher error = miss; nested-floor single read), `helpers.go` (package-level `recordSecurityEvent`, `projectAddresses`, `resolvePendingInvitations` extracted from `*Service`), `authenticate.go` (external branch; `(ctx, error)` returns; `ClassifyBearer`; `ExternalEnabled`), `machine.go` (`enforceCredentialLiveness` external branch BEFORE the kind early-return; store read error → `sdk.ErrUnavailable`), `liveness_external_test.go`, `securityevent/securityevent.go` (five new types), `pockets/authentication/{authentication.go,constructor.go,config.go,repositories.go}` (root forwarding, `ExternalSubjects`, `WithExternal`), `pockets/authentication/go.mod` (sdk pin + replace)
- **verify:** `cd pockets/authentication && go build ./... && go test ./logic/... && go vet ./...` then `make guard` (including the new `guard-auth-no-vendor-names` target added to the Makefile in this task)
- **description:** Implement D5, D6, D8 and the pocket side of D7. Tests must prove: unknown `iss` denies without calling any provider; a provider failure never falls through; deactivated mirrored user denies; JIT off → `external_subject_unknown`; JIT on → one `Users.Provision` with `External` set, identifier notification-only and verified iff claimed, pending invitation resolved for a verified email; a store that drops `External` → fail closed; collision → deny + event; `AllowedTenants`/`RequireVerifiedEmail`/`DenySignIn` denials carry their reason; a cached "revoked" fact does not deny a token whose `AuthenticatedAt` is after `ValidAfter`; outage → stale within grace, then `ErrProviderUnavailable`; fail-open passes with one event per episode; `ClassifyBearer` on today's first-party and delegated tokens returns `CredentialAccessToken`.

### task-5: HTTP guard — error channel, liveness axis, new kind, cookies, assurance, route audit

- **depends_on:** [task-4]
- **model:** opus
- **files:** `pockets/authentication/inbound/http/authenticator.go` (`CredentialService` new shape; `WithExternalCookies`; cookie-name collision validation), `principal.go` (`resolveCredential` via `ClassifyBearer` + provider cookies; `credentialPresented` mirrors; 401/303 vs 503 + `Retry-After`; RFC 9470 header; `Browser()`/`Optional()` on 503), `principal_options.go` (`externalToken`; `LivenessTier` options `Live`/`LiveOnMutation`/`Stateless`; `Accept`/`admits`; assurance options; wiring-time panics incl. `Browser()`+external without an explicit login path; `RequireExternalToken*`; literal `RequireAccessTokenOrAPIKey*`), `route_policy.go` (`CSRFBootstrap` group; explicit `Accept(CredentialAccessToken)` on established-session surfaces; every-wired-kind defaults), `routes.go` (`/auth/csrf` → `CSRFBootstrap`), `security.go` (no change expected — pinned by test), `external_middleware_test.go` (new matrix), `policy_test.go`/`host_policy_test.go` (route audit pins, floor coupling), `credential_test.go`/`browser_middleware_test.go` (adjust to the new default + 503 path), `pockets/authentication/README.md` (middleware section: kind, helpers, liveness table, lag table, cookie transport, RFC 9470)
- **verify:** `cd pockets/authentication && go build ./... && go test ./... && go vet ./...` then `make guard`; run-and-look lands in task-7
- **description:** Implement D7, D9, D10. The matrix covers: external admitted only by sets that accept it (default included when wired, excluded when not); `Audience`/`FirstParty` reject it; header-only when no cookie name; a provider cookie read after the native cookie, first-present-wins, `Optional()` denies a revoked provider cookie; nested narrowing reads the stash and a bundled POST does ONE re-check; each assurance option; the three liveness tiers × four kinds table; 503 with `Retry-After` on `ErrUnavailable` for native AND external; JSON denial carries the RFC 9470 header; `Browser()` denial stays 303 on 401 and is 503 on outage; a cookie-borne external user obtains `/auth/csrf` and completes a bundled invitation accept.

### task-6: stores — migration 0020, repository, `Provision`, conformance, memory stores

- **depends_on:** [task-4]
- **model:** opus
- **files:** `pockets/authentication/stores/pgx/migrations/0020_external_subjects.sql`, `stores/pgx/{external_subjects.go,users.go,postgres.go,README.md,go.mod}`, `stores/turso/migrations/0020_external_subjects.sql`, `stores/turso/{external_subjects.go,users.go,turso.go,migrations_test.go,README.md,go.mod}`, `stores/storetest/{external_subjects.go,storetest.go,reference_test.go}`, `stores/firestore/README.md` (documented nil), `examples/auth-cms/pockets/authentication/outbound/authmem/{external_subjects.go,authmem.go}`, `pockets/authentication/README.md` (migration inventory → 0020)
- **verify:** `cd pockets/authentication/stores/pgx && go build ./... && go vet ./... && go test ./...` and the same in `stores/turso`; `cd examples/auth-cms && go test ./pockets/authentication/outbound/authmem/...`; live: `POSTGRES_TEST_DSN=… make test-stores` and the turso `-tags=integration` leg, recorded in a dated NOTES entry; `make check`
- **description:** Implement the Schema section. Conformance pins the sentinels, `Provision`-with-`External` atomicity AND persistence, `Link` to an inactive/missing user failing, and the probe (R9). Both SQL trees carry the identical filename.

### task-7: proof hosts — `examples/external-identity` and the auth-cms hybrid

- **depends_on:** [task-2, task-3, task-5, task-6]
- **model:** opus
- **files:** `examples/external-identity/{go.mod,README.md,.env.example}`, `cmd/server/main.go` (wiring only, H10: `gooidc.New` against the dev issuer, `sdkauth.NewOIDC(..., auth0.Profile-shaped mapping)`, `authlogic.NewExternalAuthenticator`, `authenticationhttp.NewAuthenticator(..., WithExternalCookies("__session"), WithBrowserLoginPath(...))`, routes, dev-issuer handlers mounted behind the development-mode refusal), `integrations/authentication/devissuer/devissuer.go` (H11: wraps `gooidc/oidctest`; returns sdk values + two `http.Handler`s: `GET /.well-known/jwks.json`, `POST /dev/token` with chosen `amr`/`email_verified`/`iss`, `POST /dev/session` setting `__session`), `pockets/authentication/outbound/memstore/*.go` (Users, Identifiers, ExternalSubjects, SecurityEvents — the smallest `ExternalRepositories`; H6 rim import = the pocket ports), `internal/inbound/domains/me/handler.go` (`GET /api/me` → principal + `Resolve`), `examples/auth-cms/cmd/server/{main.go,demo.go}` (+ `WithExternal` behind `AUTH_EXTERNAL_DEMO=1`, development only; routes `RequireExternalTokenOrAPIKey()` and `RequirePrincipal(Accept(CredentialExternalToken), RequireMFA())`), `examples/auth-cms/integrations/authentication/devissuer/` (same wrapper), `go.work`, `Makefile` (`MODULES`; `guard-host-contract` list + `external-identity`), `examples/README.md` (host list)
- **verify:** `cd examples/external-identity && go build ./... && go test ./... && go vet ./...`; `make check && make guard` (G30 runs `gopernicus guard` on the new host); **run-and-look (required to close):** `cd examples/external-identity && go run ./cmd/server` → `TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true}' | jq -r .token)`; `curl -i -H "Authorization: Bearer $TOKEN" localhost:8080/api/me` → 200 with an internal user id (not `u1`); repeat → same id, no provisioning event in the log; `"email_verified":false` → 200 with no address projected; a token from an unwired issuer → 401; no credential → 401; `curl -c jar -XPOST localhost:8080/dev/session -d '{"sub":"u1"}'` then `curl -b jar -i localhost:8080/api/me` → 200 (cookie transport); `curl -b jar -i -XPOST localhost:8080/api/echo` → 200 with the log showing exactly one local status read. In auth-cms: `AUTH_EXTERNAL_DEMO=1 make run`; the hybrid route answers 200 to both a dev-issuer token and a seeded API key; the `RequireMFA()` route answers 401 + `WWW-Authenticate: Bearer error="insufficient_user_authentication"` without `amr`, 200 with `"amr":["pwd","mfa"]`; a dev-issuer token against `POST /auth/step-up/begin` → 401 JSON; a native login cookie against `RequireExternalToken()` → 401.
- **description:** Build the two proofs. The dev issuer is a host integration (H11) because it touches no pocket type; the memory store is a host-pocket `outbound` package (examples/README §5) and deliberately the three-repository minimum, not a copy of authmem (R10 recorded). The Firebase session-cookie exchange is NOT built in auth-cms (it would pull the Admin SDK graph into a "zero infrastructure" host); its recipe — a host-declared `SessionCookieMinter` port, `cmd` injecting `*firebase.Provider`, the login-CSRF origin gate, an `auth_time` freshness check before minting — ships in task-8 and is proven by the firebase module's live leg.

### task-8: docs, recipes, hash-export notes, RELEASING entry

- **depends_on:** [task-7]
- **model:** opus
- **files:** `pockets/authentication/README.md` (new section "External identity providers": decision table native vs external; the ownership split; wiring recipes for Firebase (header + session cookie exchange), Auth0, Clerk, Cognito, Okta, Keycloak with claim tables; the support matrix; the liveness/lag tables; the roles-are-not-authorization rule; identity edge cases; audit gap; step-up loss; invitations; "Leaving a provider"), `workshop/documentation/docs/pockets/authentication.md` (mirror), `workshop/documentation/docs/sdk/authentication.md` (the capability), `examples/README.md`, `RELEASING.md` (train entry: sdk v0.14.0 → go-oidc v0.1.0 → firebase/auth0 v0.1.0 → authentication v0.19.0 → stores pgx v0.9.0 / turso v0.8.0; BREAKING callout naming the `CredentialService` break, the default-set change, the R-LIVE read, the 503 path; downstream heads-up for gps-360-go/hub/Segovia), `.claude/plans/external-identity-providers.md` (status)
- **verify:** `make docs-build` (if the target builds locally) and `make check`
- **description:** Write the host guidance per D9–D11, with every vendor claim cited to the URLs in this plan and the unverified ones (Clerk/Okta hash export, `Token.Claims` contents, the `firebaseauth.users.get` permission name) marked as verified-or-not after task-3.

## Sequencing

```
task-1 (sdk port + capability)
  ├─ task-2 (go-oidc + oidctest)            ──┐
  ├─ task-3 (firebase + auth0 presets)        │
  └─ task-4 (pocket vocabulary/engine/liveness)│
        ├─ task-5 (HTTP guard)  ──────────────┼─ task-7 (proof hosts) ─ task-8 (docs, release)
        └─ task-6 (stores + memory)  ─────────┘
```

task-2, task-3 and task-4 are independent after task-1; task-6 depends on task-4
(the port + `InitialCredentials`); task-5 on task-4; task-7 needs all four legs.
Tag order at release: sdk → go-oidc → firebase/auth0 → authentication → stores.

## Test strategy (real behavior, not just unit asserts)

1. **sdk conformance** (task-1): `authenticationtest.RunProvider` over a fake
   verifier; re-run by task-2 over the real go-oidc verifier and by task-3 over the
   Firebase provider (live).
2. **Integration tests with a real JWKS server** (task-2): every edge is an HTTP
   round trip, including rotation, the kid spray and the fetch-failure retention.
3. **Logic tests with a fake provider** (task-4): routing, JIT, collision,
   deactivation, liveness ordering, facts cache, outage dedupe — no network.
4. **HTTP matrix** (task-5): kinds × transports × liveness tiers × assurance
   options × denial modes, incl. the cookie-borne CSRF bootstrap and the 503 path.
5. **Store conformance** (task-6): pgx (live Postgres), turso (`-tags=integration`),
   authmem and the new memstore; dated NOTES entry.
6. **End-to-end hosts** (task-7): `curl` against running processes — the
   run-and-look block is part of verify.
7. **Live tenant legs** (tasks 2–3, env-gated): one owner-minted Auth0 token
   through go-oidc; one Firebase project for ID token, session cookie, revocation
   and mint. **The Firebase emulator issues unsigned tokens (`alg: none`) and
   accepts them only with `FIREBASE_AUTH_EMULATOR_HOST` set; it cannot stand in for
   a real tenant** — the emulator leg proves plumbing and the production refusal,
   the generic verifier's test proves the token is rejected.

## Rulings needed (YOUR CALL summary)

| id | decision | recommendation |
|---|---|---|
| R-SDK | ratify `sdk/capabilities/authentication` as a platform protocol (pockets/README §5) with `Identity` carrying no principal | **yes**; CONSUMES row in ARCHITECTURE |
| R1 | `JWTVerifier` tier: `pkg/cryptids` plain port, no sdk suite | **pkg/cryptids** (rev 1) |
| R2 | one generic OIDC adapter (`go-oidc`) for every discovery-publishing vendor | **yes**; Firebase session cookies are the one case needing the vendor SDK |
| R-FB | Firebase module on the Admin SDK vs REST `accounts:lookup` + `x/oauth2/google` | **Admin SDK** (session-cookie x509 keys, three calls); REST recorded as the slimmer alternative |
| R-AUTH0 | auth0 preset as a zero-library module vs README recipe | **module** (owner asked for presets as code; github precedent) |
| R-POCKET | is the pocket required for an external-only host, or port + verifier + small identities store? | **required in v1**: the guard, mirror, JIT, status, audit, Resolve, invitations all live there and A′ makes it cheap (no signer/sessions/mail, four repos); a pocket-less sdk middleware is follow-up F7 if a host asks |
| R-MIRROR | new `external_subjects` keyed `(provider_name, subject)` vs reuse `oauth_accounts` | **new table** (D6 evidence table) |
| R-SEAM | credential methods: flags (existing) + A′ engine vs a native-session rail seam | **A′ now**; the seam is follow-up F8 sized at 2 PRs |
| R3 | normalized fields = `sdkauth.Identity` (one named field on `Credential`) | **yes**; raw claims gone, `Attributes` for host mapping |
| R4 | external-only construction A′ engine + thin owners | **A′** (rev 1) |
| R5 | JIT collision with an existing login/recovery identifier | **deny + audit + admin Link** (rev 1) |
| R-SYMMETRIC | native registration against an existing JIT email | **document in v1; follow-up F4** |
| R6 | JIT default | **on in recipes, zero value off** (rev 1) |
| R-LIVE | `LiveOnMutation()` default for native access tokens too | **uniform** (one rule; named in RELEASING); alternative external-only |
| R-FAIL | fail mode on provider outage | **closed, 503 + Retry-After; per-provider `RevocationFailOpen` relaxes the default tier only; `Live()` never** (planner's call, recorded) |
| R-TENANT | `AllowedTenants` per provider at construction | **yes** |
| R-ANON | Firebase anonymous users in v1 | **deny**; `AllowAnonymous` + identifier-less `Provision` = F3 |
| R7 | semver: one MINOR-with-BREAKING train | **yes** (table above) |
| R8 | Firestore parity for `external_subjects` | **documented nil** this release |
| R9 | boot-time probe includes `external_subjects` | **yes** |
| R10 | promote `authmem` to `stores/memory` now (the new host is the second private copy — the trigger the steward named) | **follow-up**: the new host's memstore is the three-repository minimum, not a copy; promote when a third host or an external-only production host appears |

## Follow-ups (not v1)

F1 native exchange (`/auth/external/handshake`) · F2 per-route fail-open ·
F3 `AllowAnonymous` + identifier-less `Provision` across four stores ·
F4 symmetric collision check in native provisioning · F5 identifier refresh on a
later verified token · F6 revocation adapters: Keycloak introspection (token-hash
key), Clerk/Auth0 if they ever expose one · F7 pocket-less sdk middleware + tiny
identities store · F8 the native-session rail seam (2 PRs) · F9 `firebase-scrypt`
`Hasher` · F10 Firestore `external_subjects` · F11 native assurance claims on
first-party tokens · F12 metrics port (today: stats + heartbeat).

## Consultation notes

**lead-backend-engineer (2026-10-20)** — "ship-with-edits". Folded: the bool-only
`CredentialService` cannot express 503 → `(ctx, error)` in the v0.19.0 break and
store read errors → `sdk.ErrUnavailable` on native paths too; the bundled `floor`
re-runs external liveness → `externalLiveAt` proof stamp + single-read test;
cookie-borne external mutations ARE pocket-gated by `requireBrowserSafeMutation`
→ the `CSRFBootstrap` group; `credentialPresented` mirrors provider cookies;
`Browser()` + external requires an explicit login path, no `recover` for external;
cookie names reach the adapter by option; the mirror keyed by host provider NAME
(Firebase ID vs session `iss`); the symmetric collision gap made a ruling; anonymous
JIT blocked by `Provision`'s unconditional identifier → denied in v1; re-`Get`
after `Provision`; facts cache instead of verdict; `Check` loses `claims`
(`Attributes` added); `Identity` as a NAMED field; `Methods` cloned at stash;
engine kept in `logic/authentication` with helpers extracted; direct-add lookup
and pending auto-accept facts recorded; the Firebase `Token.Claims` caveat → build
from struct fields; auth0 module reservation recorded under R-AUTH0.

**architecture-steward (2026-10-20)** — "aligned-with-edits". Folded: tier and
name `sdk/capabilities/authentication` confirmed (events/sdkevents precedent;
`identity` rejected as root vocabulary); CONSUMES row; criterion 3 held by keeping
principal/user id off `Identity`; G12c (no cacher/oauth in the capability); presets
as `integrations/authentication/*` legitimate, G13 forbids importing go-oidc →
`cmd` composes; engine in `logic/authentication` (unexported stash); `cacher.Storer`
as a nil-safe port; vendor meaning audited out of `Profile` (`SignInMethod`/`DenySignIn`
neutral paths, preset supplies "anonymous"); the Firebase exchange handler cannot
import the integration from a host-pocket inbound (H7) → port + `cmd` injection,
and dropped from auth-cms; the dev issuer moved to the host's `integrations/`
(H11); `examples/external-identity` added to G30's host list; the second private
memory store recorded against R10; `guard-auth-no-vendor-names` added.

**platform-sre (2026-10-20)** — "ship-with-edits". Folded: facts not verdicts
(the re-login lockout); `Live()` always fail-closed, per-provider switch relaxes
only the default tier; `RevocationOutageGrace` naming and the stale-serving
mechanics on a TTL-only `cacher.Storer`; cacher error = miss; the four lag bounds;
deduped outage event + edge-triggered logs + `RevocationStats` + heartbeat; no
production-required knobs, coherence validation, production WARN per fail-open
provider; in-process cache acceptable in production (QPS formula, capacity knob,
namespace); per-issuer refetch floor, bounded kid cache, last-good keys, body cap,
spray and fetch-failure tests; Firebase credential posture (ADC, least privilege,
`sdk.Secret`/`GOOGLE_APPLICATION_CREDENTIALS`, `.env.example`, emulator refusal in
production, rotated-key error kind, never log the `GetUser` payload); the native
default flip named as a downstream behavior change (R-LIVE).

## Open questions

1. Does any downstream host implement `authenticationhttp.CredentialService`?
   Repo grep finds none; the owner's grep in gps-360-go, the hub and Segovia
   decides whether the R7 break is theoretical.
2. What does the Firebase Admin Go SDK's `auth.Token.Claims` actually carry
   (standard claims stripped or not)? task-3 verifies and records it; the design
   does not depend on the answer.
3. Exact least-privilege IAM permission for `GetUser` (`firebaseauth.users.get`?)
   — verified in task-3's README.
4. Clerk and Okta password-hash export policies — unverified; task-8 marks them.

## Recommended reviews

- **product-manager** — scope: whether R-POCKET (pocket required) and the deferred
  native exchange are acceptable for the first external host; the R-LIVE default
  flip as a shippable change.
- **platform-sre** — post-build pass on task-4/5 against the D8 list (facts cache,
  dedupe, 503 path, heartbeat) and task-3's credential posture.
- **lead-backend-engineer** — post-build pass on task-4/5 (error channel, floor
  stamp, CSRF bootstrap, mirror key, re-Get after Provision).
- **data-integration-reviewer** — task-6 parity (pgx/turso SQL, `Provision`
  persistence, probe, inventory text).
- **architecture-steward** — G12/G13/G30 and H6/H7/H11 of the two hosts; the
  no-vendor-names guard; the CONSUMES row wording.

## Notes

- Auth0 access tokens are JWTs only when the SPA requests an API `audience`;
  Cognito access tokens carry `client_id` rather than `aud`; Clerk v2 tokens put the
  organization under `o.id` (v1 used `org_id`) — the recipes say so.
- The three `authentication` packages (pocket root, pocket logic, sdk capability)
  force aliases: `sdkauth` for the capability everywhere, `authlogic` for the
  logic package (existing convention), the pocket root unaliased in hosts.
- The 16 KiB cap, exact-string `iss` comparison and "never a second provider"
  rule are the three guards that make unverified-`iss` classification safe
  (`parseAccessClaims` rejects `iss` on first-party tokens, `token.go:109-115`).
