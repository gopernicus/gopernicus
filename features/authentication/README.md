# features/authentication — the identity feature

A pluggable, datastore-free identity feature. v1 shipped password + session
authentication — registration, email verification, login/logout, password
reset — and the `RequireUser` middleware other features gate on. v2 grew the
rest of the identity capability: password change, OAuth login/linking, machine
identity (service accounts + API keys), bearer JWTs, a synchronous
security-event audit rail, and ReBAC-decoupled resource invitations. The
JWT-sessions + refresh change (2026-07-11) then re-homed the access credential
onto a self-validating **access JWT** and turned the server-side session row
into the **revocation + refresh anchor**.

**auth-v3 (the identity milestone, 2026-07-13)** reshapes identity itself. A
user no longer *is* an email: identity moves off the `users.email`/
`email_verified` pair onto a **`user_identifiers`** table — multiple email/phone
identifiers with explicit **login / recovery / notification / primary** uses.
On top of that base v3 adds an atomic **challenge rail** (HMAC-protected OTP
codes + SHA-256 magic-link tokens), a host-owned **delivery runtime** in one of
two explicit modes — durable generic **jobs** or a bounded ephemeral
**in_process** pool — as the only outbound send path, a step-up-gated
**credential/identifier management suite** (revision-serialized mutations),
**passwordless** email/phone login, a
fail-closed **production runtime posture**, and an **optional HTML/templ surface**
mounted over the unchanged JSON API. `Config.Views == nil` keeps the feature
JSON-only with no view technology in the host graph; a non-nil `Views` adds HTML
GET pages + form handling without touching a single JSON contract.

Designs of record: `.claude/plans/restructure/auth-feature-design.md` (v1),
`.claude/plans/roadmap/auth-v2-feature-design.md` (v2, AV1–AV11),
`.claude/plans/roadmap/auth-jwt-session-refresh.md` (the refresh change,
D1–D8), and `.claude/plans/roadmap/auth-v3-identity-design.md` (v3, the identity
milestone — executed through `.claude/plans/authv3/`).

## Layout (the trio — see `features/README.md` §2 for the full contract)

```
authentication.go        the socket: Repositories, Config, PasswordHasher,
                         CompromisedPasswordChecker, Granter, MemberCheck,
                         Principal, TokenPair, Views (+ view-model aliases),
                         Service, NewService, Register — the entire host-facing
                         exported surface
views.go                 public aliases re-exporting the Views port and every
                         view model (no internal/ exposure)
domain/                  the hexagon's public rim — entities + repository ports.
  user/ session/         Public BY NECESSITY: hosts and store modules implement/
  identifier/            import these across module boundaries.
  challenge/ authgrant/  identifier is the v3 identity rail; challenge/authgrant/
  contactchange/         contactchange/credential are the v3 atomic security
  credential/            rails. Delivery owns NO domain here — durable delivery is
                         the generic jobs feature (see "Delivery execution modes").
  oauthaccount/ oauthstate/
  serviceaccount/ apikey/
  passwordreset/
  securityevent/ invitation/
internal/
  logic/authsvc/         the identity service — the sealed interior
  logic/delivery/        the shared delivery processor (renderer/router, encrypted
                         command envelope, initializer) + the bounded in_process
                         runtime; jobs mode drives the same processor off-module
  logic/invitationsvc/   the invitation service (built only when a Granter is wired)
  inbound/authentication/ driving adapter: JSON handlers, the content-type
                         dispatcher, the HTML GET/form handlers, the Views port,
                         and the route table
  redirect/              exact-match open-redirect allowlist matcher
storetest/               executable spec for domain/'s ports + the reference
                         in-memory implementation
stores/turso/            the outbound tier: per-dialect SQL + canonical
stores/pgx/              migrations (0001–0015; auth owns no delivery table),
                         each its own module
views/goth/              the bundled default HTML surface — a SIBLING module
                         (goth.New(bundle), the ui/goth adapter); the feature
                         core never imports templ or ui/goth
```

## The identifier model (design §2.2)

`user_identifiers` is the v3 identity-discovery rail. Each row is one
email/phone address a user owns, carrying four independent **uses**:

- **login** — the address can start a password or passwordless login;
- **recovery** — the address receives reset/step-up/removal codes;
- **notification** — the address receives independent change notices;
- **primary** — the canonical address of its kind for the user.

`Identifier.Verified()` (a non-NULL `verified_at`) gates login/recovery: an
unverified identifier can hold `notification` but never `login`/`recovery`.
**Normalization is kind-aware and service-owned:** email → trim + lowercase,
phone → strict E.164. Two partial-unique indexes encode the invariants:

- `idx_user_identifiers_auth_claim` — `UNIQUE(kind, normalized_value)` WHERE
  active AND (login OR recovery) enabled: **one active authentication claim per
  address** across the whole table (the account-takeover backstop);
- `idx_user_identifiers_primary` — `UNIQUE(user_id, kind)` WHERE active AND
  primary: one primary per (user, kind).

A **notification-only** address is not an authentication claim, so it may be
*shared* — the same phone can be a notification identifier on more than one
account (only login/recovery addresses are exclusive). Atomic writes:
`Users.CreateWithPrimaryIdentifier` commits a user + its first identifier in one
transaction; `Identifiers.ApplyVerifiedChange` is the revision-CAS that retires
the replaced/displaced rows and adds the newly verified one atomically.

## Route surface (JSON)

Claimed namespace **`/auth/*`** (prefixable via `feature.PrefixRegistrar` — a
prefixed host MUST also set `Config.RefreshCookiePath` to the full prefixed path,
e.g. `/api/v1/auth`, or the browser never sends the refresh cookie to
`/api/v1/auth/refresh`).
JSON bodies are strictly decoded (unknown fields → 400). Optional subsystems are
**deny-by-absence**: leave the enabling collaborator nil and the routes are NOT
registered (404) — never a half-registered state. Every sensitive credential/
identifier mutation is `RequireLiveSession`-gated, carries the **browser-safe
gate** (allowlisted `Origin` + double-submit CSRF for cookie callers; bearer-only
API callers skip it), and sets `Cache-Control: no-store`.

**Always registered — password + sessions + challenge rail:**

- `POST /auth/register` — `{email, password, display_name}` → 201; enqueues an
  async verification code to the new primary email identifier.
- `POST /auth/verify` — **`{email, code}`** → 200. **v3 break:** the body now
  carries `email` (the challenge rail keys the code by identifier); the pre-v3
  `{code}`-only body is a 400.
- `POST /auth/login` — `{email, password}` → 200 + BOTH cookies. Rate-limited
  (identifier+IP key) BEFORE credential work → 429. `RequireVerifiedEmail` →
  an unverified login is 403.
- `POST /auth/refresh` — rotates the presented refresh token (§1.3). Not gated;
  rotation IS the credential. Every denial is a generic 401.
- `POST /auth/logout` — NOT gated (§1.5) → 200 + both cookies cleared. Origin-only
  (no double-submit token, D2). Content-type dispatched like the other shared POSTs:
  a form body (Views wired) clears both cookies and **303s to `/auth/login`**; nil
  Views → 415.
- `POST /auth/password/forgot` — `{email}` → 200 (never reveals existence).
  **Enumeration-safe and async:** it normalizes and enqueues an opaque delivery
  command WITHOUT resolving the account or calling a provider; the off-request
  delivery runtime resolves the recovery identifier and delivers.
- `POST /auth/password/reset` — `{token, password}` → 200. Atomic: consumes the
  reset challenge, sets the password, and **revokes all sessions** in one
  transaction.
- `POST /auth/password/change` — live + browser-safe, `{current_password,
  new_password}` → 200 + a fresh cookie pair; revokes all the user's sessions.
- `GET /auth/delivery/status` — live-session-gated; poll the delivery dispatcher
  with a `receipt` to learn a send failed without holding the start request open.
- `GET /auth/methods` — live-session-gated **masked** method inventory (below);
  `Cache-Control: no-store`.
- `GET /auth/csrf` — live-session-gated JSON **CSRF bootstrap** →
  `{"csrf_token":"..."}` plus the matching `__Host-auth_csrf` cookie,
  `Cache-Control: no-store`. Origin-gated only (the double-submit gate cannot
  protect the endpoint that hands out the token). See the cross-origin SPA flow
  below.
- `GET /auth/me` — live-session-gated **session hydration** → the same
  `userResponse` body login and register return: `{id, email, display_name,
  email_verified}` with the **unmasked** active email (`/auth/methods` is the
  masked inventory; this is the signed-in identity). `Cache-Control: no-store`.
  Hydration deliberately pays one revocation lookup (`RequireLiveSession`, not
  `RequireUser`), matching `/auth/methods`: a revoked access JWT is denied within
  one round-trip. Bearer-safe (no body → no double-submit gate); both the session
  cookie and a bearer access JWT hydrate. `email_verified` is `false` — with the
  address still present — for an allowed-but-unverified account. A **machine
  principal is not a current user**: a service-account API key is a valid live
  credential elsewhere but gets 401 here rather than a fabricated profile (an
  act-as-user service account is the exception by design — it resolves to its
  human owner and hydrates that owner).

**Step-up (recent-authentication grant, design §5.0) — all live + browser-safe:**

- `POST /auth/step-up/begin` — issues a step-up code to an existing **verified
  recovery** identifier (never a proposed new address).
- `POST /auth/step-up/password` — earns a single-use grant by proving the
  current password.
- `POST /auth/step-up/code` — earns a grant by proving a delivered code.

**Credential suite (design §5.2–5.6) — all live + browser-safe:**

- `POST /auth/password/set` — set an initial password (consumes a `set_password`
  grant; 409 `password_already_set` when one exists) → fresh cookie pair.
- `POST /auth/password/remove/start` → `POST /auth/password/remove` — remove the
  password via a `remove_password` code delivered to a verified recovery
  identifier; policy-guarded, revision-CAS, revokes sessions, remints.
- `POST /auth/oauth/{provider}/unlink/start` → `POST /auth/oauth/{provider}/unlink`
  — provider-bound code-gated unlink; a Google code can never unlink GitHub
  (the code binds the exact provider; wrong-provider use consumes and rejects).
- `POST /auth/identifiers/{email,phone}` → `.../confirm` — add/change an
  identifier: start proves an existing method (step-up) and delivers a proof code
  to the NEW address; confirm consumes the code, evaluates the credential policy,
  and applies the verified change under revision-CAS. Proof of a proposed new
  address NEVER satisfies step-up — it is a separate binding proof.
- `PATCH /auth/identifiers/{id}` — change uses (login/recovery/notification/
  primary); enabling login/recovery on an unverified identifier is 409
  `verification_required`.
- `DELETE /auth/identifiers/{id}?replacement=<id>` — remove an identifier;
  removing a primary auto-selects or requires a replacement; a policy-refused
  last-method removal is 409 `cannot_remove_last_method`.

**Passwordless — registered only when `Config.Passwordless` is non-empty
(design §4):**

- `POST /auth/passwordless/start` — `{identifier_kind, identifier, method?}` →
  202 accepted (uniform for known/unknown/unverified). Method `link`|`code`
  (default email→link, phone→code). Enumeration-safe and async, exactly like
  forgot-password: normalize + limit before resolution, enqueue opaque, worker
  resolves `GetLogin` (active + verified + login-enabled) and sends.
- `POST /auth/passwordless/verify` — `{identifier_kind, identifier, code}` → 200
  mint. Atomically consumes the `login_otp` challenge bound to the current
  identifier; every invalid/expired/unknown/disabled/mismatch is one generic 401.
- `POST /auth/passwordless/redeem` — `{token}` → 200 mint. Atomic
  delete-returning of the `login_magic_link` token, then reload/validate the
  current bound identifier before minting. **POST-only — no GET consumes** (a
  link scanner cannot authenticate). All failures are one generic 401.

**OAuth — registered only when `Config.Providers` is non-empty:**

- `GET /auth/oauth/{provider}/start` → 302 (PKCE S256, server-side state, OIDC
  nonce when supported).
- `GET /auth/oauth/{provider}/callback` → 302. Existing link → login; matching
  unverified/unlinked email → **pending link** (a mailed single-use secret;
  completes only via verify-link — the takeover gate); no user → register + link.
- `POST /auth/oauth/verify-link` — `{token}` → completes a pending link.
- session-gated: `GET /auth/oauth/{provider}/link/start` — see
  [OAuth account linking](#oauth-account-linking--two-distinct-flows).
- **Removed in v3:** `GET /auth/oauth/linked` (subsumed by `/auth/methods`) and
  `DELETE /auth/oauth/{provider}/link` (replaced by the code-gated unlink pair).

**Registration verification resend — the public half is UNCONDITIONAL; the
authorized half mounts with the admin surface. See
[Verification resend](#verification-resend--one-use-case-two-surfaces):**

- `POST /auth/verification/resend` — `{email}` → **always 202
  `{"status":"accepted"}`** for every target state. Origin-gated (no CSRF token —
  the caller has no session). 429 on a budget (3/address/min, 10/IP/min), 503 on
  a saturated delivery queue.
- `POST /auth/admin/users/{id}/verification/resend` — authorized; 202 + a
  secret-free receipt, or 409 `already_verified` / `user_deactivated`, or 404.

**User administration — registered only when `Config.UserAdminCheck` is wired
(deny-by-absence; repository presence alone is NOT enough). Each route is gated
live session → (mutations only) browser-safe Origin/CSRF → `UserAdminCheck`, and
answers `Cache-Control: no-store`. See
[Account lifecycle](#account-lifecycle--the-operator-directory-and-deactivation):**

| route | check action | result |
|---|---|---|
| `GET /auth/admin/users` | `UserAdminList` (no target) | `crud.Page[user summary]`, ordered `created_at DESC, id DESC`, with the usual `limit`/`cursor`/`offset`/`count` params |
| `GET /auth/admin/users/{id}` | `UserAdminRead` | one summary |
| `POST /auth/admin/users/{id}/deactivate` | `UserAdminDeactivate` | `{user, changed}` — `changed:false` on an idempotent replay |
| `POST /auth/admin/users/{id}/reactivate` | `UserAdminReactivate` | `{user, changed}` |
| `POST /auth/admin/users/{id}/verification/resend` | `UserAdminResendVerification` | 202 + secret-free receipt, or a typed 409/404 |

**Machine identity — registered only when `ServiceAccounts` AND `APIKeys` are
both wired; all session-gated:**

- `POST /auth/service-accounts`, `GET /auth/service-accounts`
- `POST /auth/service-accounts/{id}/keys` — plaintext returned EXACTLY ONCE
  (SHA-256 at rest); `GET /auth/service-accounts/{id}/keys`
- `POST /auth/api-keys/{id}/revoke`

**Bearer JWT / token endpoint — `Config.TokenSigner` is REQUIRED, so
`/auth/token` is always registered:**

- `POST /auth/token` — `{email, password}` → 200 `{access_token, expires_at,
  refresh_token}` (the API twin of `/auth/login`). Shares login's pre-credential
  rate limit and verified-email gating; clients rotate via `/auth/refresh`.

**Invitations — registered only when `Config.Granter` is wired; every
authenticated route is `RequireLiveSession`-gated (immediate revocation), only
decline is public:**

- `POST /auth/invitations/{resource_type}/{resource_id}` — `{identifier,
  relation, identifier_kind?, auto_accept?}` → 201 pending (or immediate
  direct-add for a known email invitee). After live-session validation, principal
  resolution, and parsing, the handler calls the required host `InviteCheck` with
  the exact requested relation; a denial fails closed before the service runs.
- `GET /auth/invitations/{resource_type}/{resource_id}` — calls `InviteCheck`
  with `InviteList` (empty relation) after the same resolution; `GET
  /auth/invitations/mine`.
- `POST /auth/invitations/accept` — `{token}` → grant through the Granter.
  Acceptance does NOT re-run inviter authority (issuance-time authority, below).
- `POST /auth/invitations/{id}/{cancel,resend}` — `InvitedBy == caller` checks.
- `POST /auth/invitations/{id}/decline` — public, token-authorized, IP-limited
  (the one invitation route with no session gate).

Every response built from an invitation row (pending create, resource/mine list,
resend) is `{id, resource_type, resource_id, relation, identifier, invited_by,
status, auto_accept, resolved_subject_id?, expires_at, accepted_at?,
created_at}`. It carries **no token** — the secret is only ever in the mail.
`invited_by` is the owning user id, the same value cancel/resend ownership is
enforced on, so a resource list can hide actions on another admin's rows; it is a
rendering hint, never authority.

**Resolve-on-provisioning.** A pending AUTO-ACCEPT invitation for an address is
granted the moment an account for that address comes into existence, with no
`POST /auth/invitations/accept` call: `Register` (so a no-verify host still
resolves), `Verify`, and the OAuth register-and-link branch — a brand-new
password-less account provisioned from a **provider-verified** email. All three
call the one internal resolver, which is best-effort (a failure never fails
registration or the OAuth login, and is audited by the invitation service) and
idempotent (a resolved invitation moves off pending, so a second pass is a no-op
and a failed grant stays pending for the next attempt). Ordinary OAuth login of an
already-linked user and a pending-link completion for an already-registered
address are **not** provisioning events and never re-grant — an address that
already belongs to an account was direct-added at create time. Non-auto-accept
invitations are never resolved this way; they stay pending for an explicit accept.

**The Granter contract and invitation authority (D1–D3).** `Config.Granter` is
the host seam an accepted, auto-accepted, or directly-added invitation grants
through — a structured, operation-scoped request:

```go
type GrantInput struct {
    OperationID  string // stable per logical grant (see below)
    ResourceType string
    ResourceID   string
    Relation     string
    SubjectType  string
    SubjectID    string
}
type Granter interface{ Grant(context.Context, GrantInput) error }
```

- **Optional operation-scoped identity.** `OperationID` is an opaque, non-secret handle
  for THIS logical grant. Pending accept and resolve-on-registration use the
  persisted invitation row id — a retry of the same invitation reuses it, while a
  later invitation row for the same tuple gets a distinct id. Direct-add has no
  invitation row, so the feature mints a fresh high-entropy id from its
  unconditional secret generator (never `Config.IDs`, whose `cryptids.Database`
  strategy yields an empty id until an entity is inserted). It is available to an
  adapter that chooses durable command idempotency: such an adapter can derive an
  authorization MutationID from a fixed purpose + `OperationID` + the tuple. A
  baseline relationship-state adapter may ignore it; exact tuple creation is
  naturally idempotent and a later re-grant simply restores current state. The
  field is metadata, not authority and not a mandate to use receipts or a mutation
  repository.
- **Strengthened success contract.** `Grant` returns nil ONLY when the EXACT
  requested relation was applied or is already exactly present. A different
  existing relation, an invariant refusal, a missing/deleted host resource, and
  any infrastructure error all fail loud — there is **no implicit replace**. A host
  baseline adapter verifies the exact resulting tuple (accepting ordinary detached
  race semantics); a guarded adapter maps its authorization receipt outcome
  (applied / no_change → nil; semantic_conflict / invariant_blocked / anything
  else → a loud error).
- **Required `InviteCheck`.** Whenever a `Granter` enables invitations,
  `Config.InviteCheck` is REQUIRED at construction — nil → `ErrInviteCheckRequired`;
  an `InviteCheck` wired with no `Granter` → `ErrInviteCheckWithoutGranter`. It runs
  in the feature's own parsed create/list handlers (after live-session validation,
  principal resolution, and parsing), so the host sees the caller, resource,
  action, and validated relation a route wrapper cannot — and can refuse, e.g., an
  editor inviting a co-owner. HTTP create/list call it; host-direct `Service`
  methods are trusted composition calls that document they skip HTTP policy.
  Denial (wrap `sdk.ErrForbidden`) or an infrastructure error fails closed.
- **Issuance-time authority.** An invitation authorized at creation is a durable,
  expiring capability; acceptance does not re-check inviter authority, and a later
  loss of the inviter's host permission does not silently invalidate an already
  issued invitation. Deleted-resource refusal at acceptance is the Granter's duty —
  only the host knows whether the target still exists.

The host chooses posture per resource type/relation. Ordinary folder/document
sharing should normally use a trusted application-side relationship writer and
accept ordinary state-write races. Tenant/account owner or administrator grants
can opt into a guarded lifecycle when atomic authority checks, last-owner rules,
audit evidence, or durable idempotency justify it. `InviteCheck` remains required
in both cases: choosing simpler tuple-write semantics does not make invitation
authority optional.

## Cross-origin SPA bootstrap (`GET /auth/csrf`)

A SPA served from a **different origin than the API** (both HTTPS siblings under
one registrable domain — cross-origin but same-site) cannot read the API-origin
`__Host-auth_csrf` cookie, so it cannot satisfy the double-submit gate on its
own. `GET /auth/csrf` is the supported seam: it returns the token in a JSON body while
setting the matching cookie. Bearer-only clients need none of this — they skip the
browser gate entirely. Arbitrary **cross-site** cookie authentication remains out
of scope: both cookies stay `SameSite=Lax`.

The flow:

1. `POST /auth/login` — the browser stores the access + refresh cookies.
2. `GET /auth/csrf` — read `csrf_token` from the **body** (never from the cookie;
   the SPA origin cannot read it) and hold it in memory.
3. Send `X-CSRF-Token: <token>` on every browser-safe mutation
   (`/auth/password/*`, `/auth/step-up/*`, `/auth/identifiers/*`, …) with
   `credentials: "include"` so the session and `__Host-auth_csrf` cookies ride
   along.

Contract details:

- **The body value is always the effective cookie value.** The two can never
  disagree.
- **Bootstrapping does not rotate.** A well-formed existing `__Host-auth_csrf`
  cookie is returned as-is, so a second tab holding the current token in flight is
  not broken; only a missing or malformed cookie mints. A cookie value that does
  not have the shape the feature mints is never echoed back — it is replaced.
- **The cookie name is the provenance control; the shape check is not.** Rejecting
  a malformed cookie does not prove the feature minted the value — any well-formed
  32-byte base64url string is reused and echoed. What stops a *sibling* host under
  the same registrable domain from planting one is the **`__Host-` prefix**: a
  browser refuses a `Set-Cookie` for this name unless it is `Secure`, `Path=/`,
  and carries **no `Domain` attribute**, which no sibling origin can satisfy for
  another origin's cookie. The residual that remains is script already running on
  the API origin itself, which is game-over independently of CSRF. Prefix rules
  are enforced **by browsers**, not by Go's `net/http` or `net/http/cookiejar`, so
  a server-side test can still hand the handler any value. The **Origin
  allowlist** remains the mutation gate: even a fixated token cannot be spent
  unless the mutation comes from an allowlisted origin.
- **`__Host-` requires HTTPS.** The cookie was already `Secure`-only, so this adds
  no deployment constraint — but the failure mode on a plain-HTTP host is now
  "the browser refuses the cookie entirely" rather than "the cookie is set and
  not sent". Serve the API over HTTPS (as production already must).
- **It is live-session-gated.** No session and a revoked session are both 401;
  credential-establishment endpoints (login, passwordless, verify) need no token
  and must not be bootstrapped first.
- **Entropy failure fails closed** — 500, no token, no cookie.

**Host wiring is required (the sdk knows no feature header).** `X-CSRF-Token` is
not in the sdk's default CORS request-header allowlist, so the host opts it in —
otherwise the browser's preflight rejects the echo header and every mutation
fails while simple GETs keep working:

```go
router.Use(web.CORSWithConfig(web.CORSConfig{
    AllowedOrigins: []string{"https://spa.example.com"},
    AllowedHeaders: []string{"Accept", "Content-Type", "Authorization", "X-CSRF-Token"},
}))
```

The same origin must ALSO appear in `Config.AllowedOrigins`: CORS decides what the
browser may read, the feature's own allowlist decides what may mutate. An origin
that clears CORS but not `AllowedOrigins` gets a readable 403 with code
`origin_rejected` (below) — that is the diagnosable signature of this exact
misconfiguration.

## HTML surface (Views) — the optional presentation tier

`Config.Views == nil` (the default) → **API-only**: no HTML GET page or form
decoding is registered, the shared POST routes accept JSON only, and there is no
view technology in the host's module graph. A non-nil `Views` mounts the HTML
pages **alongside the unchanged JSON API** — the JSON DTO/status/body/cookie
contracts are byte-compatible either way. The feature core never imports templ:
`Views` is a technology-neutral `web.Renderer` port (16 page methods).

**Content-type dispatch (design §9.2).** Each shared POST keeps its ONE route
registration; a single content-type dispatcher selects the arm: a form media
type (`x-www-form-urlencoded`/`multipart/form-data`) routes to the **form arm**
(only when Views is wired, else 415); `application/json` OR an **absent**
Content-Type routes to the **JSON arm** (the lenient pre-v3 JSON client is
preserved — `Accept` never changes decoding); any other explicit media type is
415. Form success uses **303 PRG** through the exact-match redirect allowlist;
form failure re-renders the same page at the mapped status with generic,
enumeration-resistant copy (password/code/token fields are never repopulated).

**HTML GET / PRG route table (Views non-nil):**

| page | route | gate |
|---|---|---|
| login / register / verify | `GET /auth/{login,register,verify}` | public |
| forgot / reset | `GET /auth/password/{forgot,reset}` | public |
| passwordless start / code / check | `GET /auth/passwordless{,/code,/check}` | public (if enabled) |
| magic-link landing | `GET /auth/magic` | public (if enabled) |
| account-security hub | `GET /auth/account` | live session |
| add / confirm / edit identifier | `GET /auth/identifiers/{new,confirm,{id}/edit}` | live session |
| password set / change / remove | `GET /auth/password/{set,change,remove}` | live session |
| step-up | `GET /auth/step-up` | live session |
| OAuth unlink | `GET /auth/oauth/{provider}/unlink` | live session (if enabled) |
| identifier edit form POST | `POST /auth/identifiers/{id}` (`action=remove`\|uses) | live + browser-safe |

**Credential-establishment endpoints** (login/register/verify/forgot/reset,
passwordless start/verify/redeem) apply the phase-7 **origin policy** — an
allowlisted `Origin`/`Sec-Fetch-Site` check WITHOUT a pre-existing CSRF session
(a first-time browser sign-in has no cookie to compare), and native/no-Origin
clients pass. **Authenticated mutations** (account-security forms) use the full
double-submit CSRF contract: the form's `csrf_token` field is compared to the
`__Host-auth_csrf` cookie in constant time. Every HTML response carries the full
**fixed** header policy — these are feature-owned and no policy or view can turn them off:
`Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`,
`X-Content-Type-Options: nosniff`, and the fixed CSP prefix `default-src 'none';
base-uri 'none'; form-action 'self'; frame-ancestors 'none'`. With no resource
policy (`Config.HTMLPolicy == nil`, the default) that prefix is followed by
`script-src 'nonce-…'|'none'` — the historical asset-free posture.

**Bundled default, `html/template` alternative, and overrides.** The blessed
default lives in the sibling module `features/authentication/views/goth`
(`goth.New(bundle *uigoth.Bundle)`, the ui/goth adapter — GOTH-7.2) — pages
rendered through `ui/goth` primitives/components with labels, field associations,
correct `autocomplete` (`email`, `current-password`, `new-password`,
`one-time-code`, `tel`), CSRF hidden fields, masked methods, and a magic/reset
landing that reads the URL fragment through an externalized same-origin
`fragment.js` (served by `goth.FragmentScriptHandler()`), scrubs history, and POSTs
(with a manual visible fallback; the token never enters a query string, and no
inline script is emitted). The adapter also derives `Config.HTMLPolicy` from the
bundle via `authViews.HTMLPolicy()` (see §11.6 of `ui/goth/README.md` for the full
adapter recipe). A host may instead satisfy the port with stdlib `html/template` via
`sdk/foundation/web.Template` — no templ/`ui/goth` import required. The
**blessed override path is embedding the bundled `goth.Views` and overriding
individual methods**; promoted defaults satisfy every other page. Overriding
presentation **cannot bypass** middleware, decoding, service, redirect, or status
policy — all of that lives in the inbound handler, never a `Views` method (proven
byte-identical across three presentations in `isolation_test.go`).

**A `Views` override is markup-only; assets are opened separately through
`Config.HTMLPolicy`.** Overriding `Views` controls the rendered HTML markup; it does
NOT by itself widen the CSP. The asset-free CSP above is the secure **default**, not a
permanent restriction (GOTH-0.4): a host opts into loading external styles, scripts,
fonts, and images by wiring a validated `Config.HTMLPolicy` — a technology-neutral,
feature-owned resource policy built with `NewHTMLResourcePolicy`.

- `HTMLResourcePolicy` carries a deterministically ordered set of ADDITIONAL CSP
  resource directives across the frozen widenable classes `HTMLScriptSrc`,
  `HTMLStyleSrc`, `HTMLImgSrc`, `HTMLFontSrc`, `HTMLConnectSrc`, `HTMLMediaSrc`,
  `HTMLWorkerSrc`. The fixed protection directives (`default-src`, `base-uri`,
  `form-action`, `frame-ancestors`) are not members of `HTMLResourceKind`, so a policy
  **structurally cannot** name, relax, or drop them. A policy only widens.
- **A non-nil policy REPLACES the default `script-src` tail entirely — fail-closed by
  design.** With `HTMLPolicy == nil` the CSP appends the historical `script-src
  'nonce-…'|'none'` tail after the fixed prefix; with a non-nil policy that default tail
  is gone and ONLY the policy's own directives follow the fixed prefix. A policy that
  omits `HTMLScriptSrc` (or supplies it without `Nonce: true`) therefore leaves scripts
  governed by `default-src 'none'`, so the bundled magic-link/reset fragment readers do
  not run. Include `HTMLResourceDirective{Kind: HTMLScriptSrc, Nonce: true}` in the
  policy to retain them.
- **Widening is unbounded by design.** The seam validates directive STRUCTURE (header
  injection, fixed/unknown kinds) — never source VALUES. `'unsafe-inline'`,
  `'unsafe-eval'`, and `*` are deliberately accepted: the seam stays value-neutral and
  script hardening rests on the view adapter / host, not this feature.
- `HTMLResourceDirective.Nonce` is the only channel to the per-render CSP nonce
  (`'nonce-<value>'`); a caller never formats the nonce into `Sources`. A directive
  requesting the nonce with no minted nonce renders `'none'` (fail-safe).
- `NewHTMLResourcePolicy` validates LOUDLY at construction (errors wrap
  `sdk.ErrInvalidInput`): an unknown/fixed directive key, a directive with neither a
  source nor a nonce, an empty source, or a source carrying a control character,
  whitespace, `;`, or `,` (the header-injection guard) is rejected — the feature never
  emits an attacker-controlled header.
- `Config.HTMLPolicy` is consulted only by the HTML surface, which is gated on `Views`.
  Setting `HTMLPolicy` with a nil `Views` is `ErrHTMLPolicyWithoutViews` at construction
  — a policy for an absent HTML surface is contradictory wiring, never a silent no-op.
- The feature core imports no templ, Alpine, HTMX, or `ui/goth`:
  `HTMLResourcePolicy` is a plain value. The future `ui/goth` authentication view
  adapter maps `goth.Bundle.Requirements()` into one (GOTH-7.2). A nil `HTMLPolicy`
  keeps the exact historical asset-free CSP.

`Views` (HTML pages) and the email overrides — `EmailContentTemplates` (bodies)
and `EmailLayouts` (the frame around them), both below — are **distinct**
override facilities: different Config fields, different types, different
subsystems, no shared type.

## The middleware surface (what other features and host routes gate on)

- `Service.RequireUser` — the stateless tier: access-JWT cookie OR
  `Authorization: Bearer <jwt>` → user identity by **signature + expiry only,
  ZERO DB**. Revocation honored within ≤ `AccessTokenTTL`.
- `Service.RequireLiveSession` — the immediate-revocation tier (D1): verify, then
  **one PK lookup** (`sessions.Get`). Deleted/expired → deny at once; an **API key
  passes** (already DB-checked); a **repository error DENIES (fails CLOSED)**. It
  also stamps the live session id so a step-up grant binds to the proven session.
  Every sensitive credential/identifier route ships gated on it.
- `Service.RequireServiceAccount` — API-key bearer only.
- `Service.RequirePrincipal` — any configured credential class; stashes the
  resolved `auth.Principal{Type, ID}`.
- `Service.RequirePrincipalBrowser` / `Service.RequireLiveSessionBrowser` — the
  browser-facing siblings for HTML routes (design §9.2). They resolve the SAME
  credentials and stash the SAME principal/session context as the JSON gates, but on
  an authentication denial they **303 to `Config.BrowserLoginPath`** (default
  `/auth/login`) instead of writing a JSON 401. A denied GET/HEAD carries a validated
  `return_to` of the original path+query; an unsafe method carries none (a later GET
  must not replay a mutation). They never sniff `Accept` or Fetch Metadata — mount
  them deliberately on HTML routes. The JSON `RequirePrincipal` / `RequireLiveSession`
  keep their byte-stable 401 behavior.
- `Service.CurrentUser(ctx)` / `Service.CurrentPrincipal(ctx)` — read the resolved
  identity; `Service.AuthenticateAPIKey(ctx, rawKey)` for non-HTTP callers.
- `Service.RunDelivery(ctx)` — the host-owned `in_process` delivery runtime loop
  (below); in `jobs` mode the host runs the generic jobs runtime instead.

## Repositories (the ports a host or store adapter satisfies)

The bundled store adapters fill the whole bundle from one handle
(`authstore.Repositories(db) (Repositories, error)`). Both dialect constructors
now **return an error**: before returning repos they probe all 13 canonical
tables and fail loudly — naming the missing table and the `authentication`
migration source, wrapping `sdk.ErrNotFound` — when a migration was not applied
pre-boot, so an omission surfaces at wiring time rather than on the first query.
Constructors never apply migrations. The v1/v2 core ports are unchanged; v3 adds
the identity + atomic-security rails. Delivery owns **no** repository here — durable
delivery runs on the generic **jobs** feature and `in_process` delivery is
process-local (see "Delivery execution modes").

```go
type Repositories struct {
    // v1 core.
    Users     user.UserRepository       // + CreateWithPrimaryIdentifier (atomic)
    Passwords user.PasswordRepository
    Sessions  session.SessionRepository
    // v3 identity + atomic-security rails (delivery owns no repository — it runs on
    // the generic jobs feature or the bounded in_process pool).
    Identifiers          identifier.IdentifierRepository   // the discovery + revision-CAS rail
    Challenges           challenge.Repository              // atomic OTP-code / magic-link-token rail
    PasswordResets       passwordreset.Repository          // atomic reset composition
    ContactChanges       contactchange.Repository          // pending add/change flow state
    AuthenticationGrants authgrant.Repository              // single-use recent-auth / step-up grants
    CredentialMutations  credential.MutationRepository     // revision-serialized typed mutations
    // v2 optional subsystems (nil semantics below).
    OAuthAccounts   oauthaccount.OAuthAccountRepository
    OAuthStates     oauthstate.StateRepository
    ServiceAccounts serviceaccount.ServiceAccountRepository
    APIKeys         apikey.APIKeyRepository
    SecurityEvents  securityevent.SecurityEventRepository
    Invitations     invitation.InvitationRepository
}
```

Nil semantics:

| port(s) | nil means | coupling / loud error |
|---|---|---|
| `Users`, `Passwords`, `Sessions`, `Identifiers` | the v3 baseline; required for any working feature (registration re-keyed onto identifiers) | absence surfaces as a nil-deref-safe closed error at the relevant use-case |
| `Challenges` | the atomic secret rail is off (verify/reset/step-up/passwordless fail closed) | wired → `ChallengeProtector` REQUIRED (`ErrChallengeProtectorRequired`) |
| `PasswordResets`, `ContactChanges`, `AuthenticationGrants`, `CredentialMutations` | the reset composition / identifier management / step-up / credential mutations fail closed | none at construction; each use-case fails closed while its rail is nil |
| `OAuthAccounts`, `OAuthStates` | allowed only while `Providers` is empty | Providers set + either nil → `ErrOAuthReposRequired` |
| `ServiceAccounts`, `APIKeys` | both nil → machine subsystem OFF | **both-or-neither** → `ErrMachineReposRequired` |
| `SecurityEvents` | **no audit trail** — the recording site is a no-op (AV9); degrades silently by design | none — never a construction error |
| `Invitations` | allowed only while `Granter` is nil | Granter set + nil → `ErrInvitationRepoRequired` |
| `UserAdmin`, `ActiveSessions` | the account-lifecycle capability is OFF: the trusted admin methods fail closed with `ErrUserAdminUnavailable`, the bundled admin routes are not registered, and session minting uses the unfenced `Sessions.Create` | `Config.UserAdminCheck` set + either nil → `ErrUserAdminReposRequired`. The reverse is **not** an error: a bundled store adapter always supplies both, and the routes stay unmounted until the host makes an authorization decision |

Sentinel contract (the port doc comments are the spec; `storetest` is its
executable form): duplicate → `errs.ErrAlreadyExists`; absent → `errs.ErrNotFound`;
expired session/code/token/invitation → `errs.ErrExpired` on read. **Atomicity
pins:** challenge success is single-use under concurrency because the repository
consumes atomically (`ConsumeCode`/`RedeemToken` are delete-returning);
`Identifiers.ApplyVerifiedChange` and `CredentialMutations.Apply` are revision-CAS
(`sdk.ErrConflict` on a stale `auth_revision`, the service re-evaluates policy and
retries); a lost auth-claim race surfaces the generic `sdk.ErrAlreadyExists`.
Paginated ports order by `created_at DESC, id DESC` — the id tiebreak is
contractual and byte-wise. The pgx canonical migrations now carry per-column
`COLLATE "C"` on the contractual keyset id columns (`service_accounts`,
`api_keys`, `security_events`, `invitations`), so byte-order pagination holds on
any database's default collation; a `C`-locale database remains a supported
belt-and-suspenders posture, not a requirement.

## Config — required vs defaulted vs deny-by-absence

Required (nil → error at `NewService`/`Register`): **`Hasher`**
(`ErrHasherRequired`), **`Mailer`** (`ErrMailerRequired`), **`TokenSigner`**
(`ErrTokenSignerRequired`), **`RuntimeMode`** (`ErrRuntimeModeRequired` — no
default, so a host can never inherit the development posture; unknown →
`ErrRuntimeModeInvalid`). The rest carry a safe default or are deny-by-absence.

| field | nil/zero means |
|---|---|
| `RuntimeMode` | **REQUIRED, no default.** `"production"` rejects development-only delivery transports and every incomplete security wiring (below); `"development"` warns instead. A **type alias** of `environment.Mode` (`sdk/foundation/environment`) — see [Security posture](#security-posture) for the canonical-name table; app-wide code should name `environment.Mode` and needs no auth import. |
| `Hasher` (PasswordHasher) | **hard error** — a password feature with no hasher is a foot-gun. |
| `Mailer` (email.Sender) | **hard error** — silently dropping mail is unsafe degradation. |
| `MailFrom` | From address on verification/reset/change mail. |
| `CompromisedPasswordChecker` | nil → no breach/blocklist check (length policy still applies). Wired → register/set/change/reset all consult it; the core ships none and adds no network dependency. |
| `CompromisedPasswordFailOpen` | false (**FAIL CLOSED**): an unavailable breach service rejects the password rather than becoming a silent bypass. true trades coverage for availability (WARN-logged). |
| `ChallengeProtector` | REQUIRED once `Challenges` is wired (`ErrChallengeProtectorRequired`). The bundled `HMACChallengeProtector` peppers OTP codes with an in-process HMAC **key ring** (active key ID stamped on each challenge; an overlapping rotation still verifies an unexpired code) and SHA-256-digests 256-bit tokens. The pepper is **local code, not a service** (below). |
| `IdentifierNormalizer` | nil → the bundled strict default (email trim+lowercase, phone strict E.164). One policy canonicalizes registration, login, recovery, and invitations identically. |
| `IdentifierKeyer` | derives PII-free rate-limit/idempotency keys under a key **distinct** from the pepper, JWT, and encryption keys. **Production-required** (`ErrIdentifierKeyerRequired`); development falls back to a per-instance SHA-256. |
| `CredentialPolicy` | nil → the bundled safe default (`credential.NewDefaultPolicy`: one direct login method + one verified recovery method, PSTN restricted). A host may supply stronger rules; `ErrCredentialPolicyRequired` covers a strict-production posture that disables the default without a replacement. |
| `DeliveryEncrypter` (cryptids.Encrypter) | REQUIRED once delivery can send — a wired `DeliveryDispatcher` (`jobs` mode) or `DeliveryMode: "in_process"` (`ErrDeliveryEncrypterRequired`) — the command envelope briefly carries the rendered secret + destination, so it is always sealed. Bundled `cryptids.NewAESGCM` with a distinct 32-byte key. |
| `DeliveryMode` | **REQUIRED**, no default (`AUTH_DELIVERY_MODE`): `"jobs"` (durable delivery on the generic jobs runtime), `"in_process"` (bounded, EPHEMERAL in-process pool), or `"off"` (no delivery runtime). Empty → `ErrDeliveryModeRequired`, unknown → `ErrDeliveryModeInvalid`, never inferred from a non-nil collaborator. `"jobs"` requires `Config.DeliveryDispatcher` (`ErrDeliveryQueueRequired`); `"off"` rejects a wired dispatcher (`ErrDeliveryOffButDeliverable`); `"in_process"` needs no dispatcher (it owns its bounded pool). `Register` starts no runtime in any mode. |
| `DeliveryJobsAcknowledged` | a wiring assertion that the host runs the durable generic jobs delivery runtime. Meaningful for `DeliveryMode: "jobs"`; the queue is the ONLY send path, so production REQUIRES it (`ErrDeliveryJobsUnacknowledged`) rather than silently swallowing every message; development tolerates the zero value. |
| `DeliveryEphemeralAcknowledged` | a wiring assertion that the host accepts the crash-loss guarantee of `DeliveryMode: "in_process"` (in-flight work is lost on a restart). Production REQUIRES it (`ErrDeliveryEphemeralUnacknowledged`); the recommended production posture is `"jobs"`. |
| `PublicAuthBaseURL` | the absolute base magic links + landing pages build from (`AUTH_PUBLIC_BASE_URL`). REQUIRED once a link flow is enabled (`ErrPublicAuthBaseURLRequired`); production requires **HTTPS** (`ErrPublicAuthBaseURLInsecure`). Request Host/forwarded headers NEVER participate. |
| `Passwordless []string` | empty → passwordless OFF (routes not registered). Allowed v3 kinds are `"email"`/`"phone"` (`ErrPasswordlessKindInvalid`); each needs a wired delivery channel (`ErrPasswordlessKindUnsupported`), the challenge rail + durable outbox, and a valid `PublicAuthBaseURL`. Provisioning is OFF by default and opt-in per `PasswordlessProvisionOnRedeem` (email links only — see [Provision-on-consumption](#provision-on-consumption--magic-links-for-addresses-with-no-account)); NEVER enables phone+password (phone stays passwordless-only). |
| `PasswordlessProvisionOnRedeem` | **false (default) → an email magic link sent to an address with no account delivers nothing and creates nothing** — the historical login-only behavior. true → the link is delivered, and the account is created **only when the link is successfully CONSUMED**, never when it is sent. Email links ONLY; never phone, OTP, OAuth, or another identifier kind. Requires the email passwordless kind, the challenge rail + `ChallengeProtector`, an `IdentifierKeyer`, a delivery runtime, a valid `PublicAuthBaseURL`, `Repositories.Passwordless`, and `Repositories.ActiveSessions` — anything missing is `ErrPasswordlessProvisionWiring` at construction. Read the [threat model](#provision-on-consumption--magic-links-for-addresses-with-no-account) before enabling. |
| `AllowedOrigins []string` | the exact-match `Origin` allowlist for cookie-authenticated sensitive mutations and HTML form posts (design §9.1). `"*"` never authorizes a credentialed cross-origin mutation; empty rejects every cross-site cookie mutation. Bearer-only callers skip the gate. |
| `BrowserLoginPath string` | the login destination the browser identity gates (`RequirePrincipalBrowser` / `RequireLiveSessionBrowser`) 303 to on denial (`AUTH_BROWSER_LOGIN_PATH`). Empty → `/auth/login`. A non-empty value MUST be a safe root-relative path (leading `/`, no `//` prefix, scheme, backslash, or control character) or construction fails with `ErrBrowserLoginPathInvalid`. Configures ONLY the browser gates; the JSON middleware is unaffected. |
| `Views` | **nil → API-only** (no HTML routes, JSON-only POSTs, no templ in the graph). Non-nil → HTML pages mount alongside the unchanged JSON API. The blessed default is the ui/goth adapter `authgoth.New(bundle)` (`features/authentication/views/goth`); the override path is embedding its `Views`. |
| `HTMLPolicy` (*HTMLResourcePolicy) | **nil → the historical asset-free CSP** (script-src nonce-only, no external origins). Non-nil → the same fixed protections plus the policy's validated widening resource directives (script/style/image/font/connect/media/worker), so a selected HTML view can load its assets. Only WIDENS — a policy can never remove a fixed protection. Build with `NewHTMLResourcePolicy` (validates loudly). Set with a nil `Views` → `ErrHTMLPolicyWithoutViews` at construction (contradictory wiring). Technology-neutral — the core imports no templ/`ui/goth`. |
| `EmailContentTemplates` | empty → the bundled `LayerCore` email bodies render unchanged. Each entry overrides a bundled template at `email.LayerApp` (Namespace must be `EmailContentNamespace`). Changes email BODIES only — a **distinct** override system from `Views`. |
| `EmailLayouts` | empty → the sdk's bundled email layouts render unchanged. Each entry registers a host layout at `email.LayerApp` — the highest layer wins, so it resolves ahead of the sdk default. Every delivery purpose renders with `email.LayoutTransactional`, so ONE entry shipping `layouts/transactional.html` (+ optional `.txt`) re-frames ALL auth mail: `EmailLayouts: []auth.EmailLayoutOverride{{FS: layoutsFS}}`. `FS` is walked from `Dir` (empty → `"layouts"`); a file's base name is the layout type it replaces, and the file names itself with `{{define "layout:<name>"}}` (`.text` for the `.txt` half) like the sdk's bundled layouts. Changes the email FRAME only — bodies stay with `EmailContentTemplates`, brand values with `EmailBranding`. |
| `EmailBranding` (*email.Branding) | nil → the sdk's unset fallback (`Your Company`, no logo, no tagline, no address). Fills the bundled layout frame's `{{.Brand.*}}` values without touching bodies or routes. All auth mail renders with `email.LayoutTransactional`, which renders **`LogoURL`, `Name`, `Tagline`, and `Address`** — a non-empty `LogoURL` now emits an `<img>` above the brand name, so a logo works **without replacing the whole layout** (fixed in `sdk v0.3.x`; previously the transactional layout silently dropped it). `Name`/`Tagline` stay visible alongside the image because mail clients block external images by default, and the `alt` text is `Name` (falling back to `Your Company`). Use an absolute, publicly fetchable **HTTPS** URL: the sdk renderer never fetches, resolves, or validates it, and `html/template` is the escaping boundary. An `EmailLayouts` override at `LayerApp` still wins — a host replacing `transactional.html` owns its own logo markup. |
| `RequireVerifiedEmail` | false. true → login AND `/auth/token` refuse an unverified user with 403 (**requires a WORKING Mailer**, else total login lockout). |
| `RateLimiter` | `ratelimiter.NewMemory()` — an in-process limiter (not "unlimited"). **Production rejects a per-process limiter** (`ErrNonDurableRateLimiter`): a multi-instance host needs a shared/durable one. |
| `SessionCookie` (CookieConfig) | zero value usable: name `session`, path `/`, browser-session cookie backed by a 7-day server session. `Secure` is a host deployment choice (true behind TLS). |
| `RefreshCookiePath string` | the refresh cookie's `Path` scope (`AUTH_REFRESH_COOKIE_PATH`). Empty → `/auth` (covers `/auth/refresh` AND `/auth/logout`). **A host mounting the feature under a prefix MUST set the FULL prefixed path** — `feature.PrefixRegistrar{Prefix: "/api/v1"}` → `RefreshCookiePath: "/api/v1/auth"` — else the browser never sends the refresh cookie to `/api/v1/auth/refresh` and cookie-driven refresh dies SILENTLY (the registrar exposes registration, not its mount prefix, so the feature cannot derive it). A non-empty value must be a valid absolute cookie path (leading `/`, no query/fragment/control/header-delimiter character, no trailing slash except `/` itself) or construction fails with `ErrRefreshCookiePathInvalid`. The SAME resolved path issues (login, rotation) and deletes (logout) the cookie. Configures ONLY the refresh cookie: the access cookie keeps `SessionCookie.Path` and both keep `SameSite=Lax`. |
| `Providers []oauth.Provider` | OAuth OFF (deny-by-absence). Non-empty → both oauth repos required. |
| `PasswordResetURL` | **REQUIRED in production** whenever the forgot/reset rail is wired (`ErrPasswordResetURLRequired`); development permits empty with one startup WARN and keeps the legacy raw-token mail. The absolute public reset landing route BEFORE `?token=` is appended — a **separate** field from `PublicAuthBaseURL` (which is the full passwordless landing URL, not an origin). HTTPS in production; no fragment; no pre-existing `token` parameter; other query parameters preserved. Built in the worker from this value only — request `Host`/forwarded headers never participate. See [Password reset](#password-reset--the-link-rail). |
| `OAuthLinkBaseURL` (`AUTH_OAUTH_LINK_URL`) | the absolute SPA landing URL the anti-takeover OAuth **pending-link** email links to, BEFORE `#token=<token>` is appended — a **separate** field from `PublicAuthBaseURL` (that route POSTs magic-link redeem; this one POSTs `verify-link`). The token rides the URL **fragment** (mirroring the magic link), so it never reaches the server on the landing GET and the page can scrub it from history. **Never a production boot requirement** — this changes presentation, not the anti-takeover guarantee (the emailed secret stays proof of inbox control): **empty degrades** the mail to its historical bare-token line, and when OAuth providers are wired one startup WARN names `AUTH_OAUTH_LINK_URL`. A non-empty value is validated in every mode — absolute http(s), a host, **no fragment** (`ErrOAuthLinkURLInvalid`), HTTPS in production (`ErrOAuthLinkURLInsecure`); existing non-secret query parameters are preserved. Built from this value only — request `Host`/forwarded headers never participate. On the pending-link branch the callback redirect also carries `?auth=link_sent&provider=<name>` so the SPA can render a "check your email" state. See [OAuth account linking](#oauth-account-linking--two-distinct-flows). |
| `UserAdminCheck` | **nil → the bundled admin routes are NOT registered**, even when the store supplies `Repositories.UserAdmin` — turning a store capability into an HTTP surface is the host's decision, not the store's. Non-nil → `GET /auth/admin/users`, `GET /auth/admin/users/{id}`, and the deactivate/reactivate POSTs mount, and BOTH `Repositories.UserAdmin` and `Repositories.ActiveSessions` become required (`ErrUserAdminReposRequired`). Authentication never invents a role named `admin` and never imports authorization — see [Account lifecycle](#account-lifecycle--the-operator-directory-and-deactivation). |
| `TokenEncrypter` (cryptids.Encrypter) | provider tokens NOT persisted (login/linking still work). Wire `cryptids.NewAESGCM` to store them. |
| `OAuthCallbackBase`, `RedirectAllowlist` | callback origin / exact-match redirect allowlist (open-redirect guard; a non-allowlisted target falls back to `/`). |
| `TokenSigner` (cryptids.JWTSigner) | **REQUIRED.** `sdk/foundation/cryptids.NewHS256` is the stdlib default; `integrations/cryptids/golang-jwt` covers RS256/ES256. **Multi-instance hosts MUST share the signing secret** (§1.6). |
| `AccessTokenTTL` | 0 → 15m (bounds the stateless revocation window). `AUTH_ACCESS_TOKEN_TTL`. |
| `RefreshTTL` | 0 → 7d — the FIXED refresh horizon; rotation never extends it. `AUTH_REFRESH_TTL`. |
| `Granter` / `InviteCheck` / `MemberCheck` / `Notifiers` | invitation subsystem seams (see the invitation section). `Granter` now takes a structured `GrantInput` (operation-scoped, fail-loud). `InviteCheck` (the relation-aware host policy) is REQUIRED whenever `Granter` is wired — nil → `ErrInviteCheckRequired`; set without a `Granter` → `ErrInviteCheckWithoutGranter`. |
| `ListStrategy` | `"cursor"` default; `"offset"` allowed; anything else `ErrInvalidListStrategy`. |
| `IDs` (cryptids.IDGenerator) | entity-ID strategy; NEVER mints secrets (codes/tokens/keys keep their own high-entropy generator). |
| `Logger` | best-effort WARN sink for audit-write failures + the ephemeral-key/console-transport warnings; nil → `slog.Default()`. |

**Distinct secrets and rotation (design §3.3/§4.4/§6.1.1).** v3 uses **five
distinct auth keys**, each a separate role — a compromise of one must not
compromise the others: `AUTH_JWT_SECRET` (session/JWT signing),
`AUTH_CHALLENGE_PEPPER` (OTP HMAC key ring, rotatable), `AUTH_IDENTIFIER_KEY`
(PII-free rate-limit/idempotency digests), `AUTH_DELIVERY_ENCRYPTER_KEY`
(delivery-outbox envelope AES-GCM), and `AUTH_TOKEN_ENCRYPTER_KEY` (provider
tokens). Key material is never printed; an unset key falls back to an **ephemeral
per-boot** key (dev/single-instance only, WARN-logged). Multi-instance production
MUST set and SHARE every key. There is deliberately **no separate CSRF or
magic-link/reset secret**: CSRF is a per-render random double-submit token (no
managed key), and magic-link / reset tokens are digested by the challenge pepper
above. **Rotation consequence per key:** only the challenge pepper supports
continuity (the `HMACKeyRing` retains older keys, so a rotation keeps pending
codes/links valid until the old key is dropped). The other four are single-key and
disruptive — rotating the JWT signer forces re-authentication, the delivery key
must be drained-first (in-flight sealed payloads dead-letter), the provider-token
key loses stored OAuth tokens (users re-link), and the identifier key resets
rate-limit/idempotency buckets once (transient).

## Challenges and recovery (design §3.2, §5.9)

One atomic secret rail backs every code/token flow: registration verification,
password reset, step-up, credential-removal, identifier-change proof, and
passwordless login. **Short codes** use HMAC-SHA-256 under the host pepper key
ring; **256-bit tokens** use SHA-256. The repository **consumes atomically**
(delete-returning `ConsumeCode`/`RedeemToken`), so a code/token is single-use
under concurrency — a double-redemption race has exactly one winner. Challenges
carry a stored **context** (a bound identifier/provider/pending-change id) checked
at consume, so a code minted for one target can never authorize another (a
wrong-context code is spent and rejected). Wrong-code attempts count toward a
per-challenge lockout budget.

**Why the OTP HMAC pepper is local code, not a service.** The pepper protects
codes against offline brute force *if the challenge table leaks*; it is consulted
on the hot path of every code issue/verify. An external secret/HSM service would
add a network round-trip and a hard availability dependency to the login path and
buy nothing over an in-process key ring for a symmetric HMAC — so the pepper is a
`ChallengeProtector` the host wires as a **local key ring** (active key ID stamped
on each challenge; an overlapping rotation keeps verifying unexpired codes). An
external secret service as a hard dependency is an explicit global stop condition.

Password recovery is **atomic** (`PasswordResets.Reset`): one transaction consumes
the reset challenge, sets the typed password row, and revokes all sessions +
outstanding password/reset grants and challenges — so a completed reset
**rejects every prior session** and a live reset cannot restore a removed
password. The shared password policy (length + optional breach check) applies
identically at register/set/change/reset.

## Delivery execution modes (design §6.1.1)

Every auth outbound message — verification, reset, step-up/removal codes,
identifier-change proof + notices, invitations, magic links/OTP — is **submitted
to a delivery dispatcher** instead of a request-time send. **The dispatcher is the
only send path**; account resolution, rendering, and provider latency happen off
the request path, which is what makes the unauthenticated `forgot`/`passwordless
start` endpoints enumeration-safe (they submit an opaque, encrypted command
envelope and return one uniform response without resolving the account or calling
a provider). The same versioned, encrypted `command.Envelope` and the same
processor run behind both real modes; `Config.DeliveryMode` selects which:

- **`jobs` (recommended production posture).** The host wires a
  `Config.DeliveryDispatcher` backed by the generic **jobs** feature and runs the
  generic jobs runtime. Auth owns no delivery table: durability, fencing, keyed
  idempotency/replacement, retry, status, and terminal purge are the generic jobs
  store's responsibility. Auth exposes the registered job kind/handler seam via
  `Service.DeliveryJobRuntime()`; a composition adapter (never a feature core)
  bridges the two features.
- **`in_process`.** The same processor runs behind a bounded ephemeral queue and
  a fixed worker pool that the host drives with `Service.RunDelivery(ctx)` (cancel
  ctx to stop). Retry/status retention is process-local and bounded; accepted work
  does **not** survive a crash or restart — in-flight work is lost. There is **no
  cross-instance coordination**: each process keeps its own queue, its own
  de-duplication, and its own status, so running multiple instances de-duplicates
  on **neither** and a user may receive duplicate messages. `in_process` is for
  development / single-instance hosts; production requires the explicit crash-loss
  acknowledgment. Tuning knobs (workers, capacity, admission deadline, shutdown
  drain, status retention max/TTL) live on `Config.InProcessDelivery`, validated
  fail-closed at construction.
- **`off`.** Allowed only when no configured auth capability can send.

Cross-cutting guarantees (both real modes):

- **At-least-once + duplicates (never exactly-once).** A crash after provider
  acceptance but before completion replays the SAME secret (the checkpointed
  rendered envelope is stable) — so consumers must tolerate at-least-once
  duplicates. Submit idempotency deduplicates a repeated start; an explicit resend
  (`Replace`) supersedes older active work under the same PII-free logical key and
  fences a stale worker's checkpoint/completion. **The resend / in-flight
  replacement race is real:** a resend cannot retract a provider call already in
  flight, so the recipient may receive both the superseded and the fresh message.
  The freshly issued challenge invalidates/replaces the old proof where the flow
  supports replacement; single-use redemption still guarantees exactly one winner.
- **Encryption + key rotation.** The command envelope ALWAYS seals its rendered
  secret + destination with `DeliveryEncrypter` (AES-GCM, a distinct key) —
  plaintext secrets never land in a durable payload column, log, event, or status
  response. **Rotation honesty:** rotating `AUTH_DELIVERY_ENCRYPTER_KEY` only
  affects payloads sealed AFTER the change. An in-flight durable payload sealed
  under the OLD key is decrypted by the processor that opens it; if the old key is
  removed from the host before that work drains, the sealed payload becomes
  undecryptable and the job dead-letters (its bound challenge is discarded, so the
  user simply restarts the flow). There is no envelope re-encryption or dual-key
  overlap for delivery today — drain in-flight delivery work before retiring a
  delivery key.
- **Terminal / discard + status retention.** A terminally undeliverable message
  discards the bound challenge best-effort. In `jobs` mode retry, latest-by-key
  status, and terminal purge are the generic jobs store's responsibility (the host
  runs a bounded `PurgeTerminal`); in `in_process` mode retry and status are
  process-local and bounded (max entries + TTL, evicted to unknown). The
  session-gated `DeliveryStatus` projection is lifecycle-only —
  `DeliveryStatus.Attempt` reads **0** (the attempt counter is executor-internal
  retry bookkeeping, not a stable lifecycle signal; the field is retained for
  compatibility). **SRE note (intentional, IX-22):** `Attempt == 0` at the consumer
  seam is deliberate executor/consumer separation — carrying the executor's retry
  counter through the sdk keyed-work status seam would push executor mechanics into
  the protocol. Operational attempt counts come from **lifecycle/health events**, not
  this field: read the `retried` counter emitted by `Config.DeliveryEventsEmitter`
  (the delivered/skipped/**retried**/dead-lettered/superseded/purged stream). Tooling
  that read `attempt` for retry visibility must repoint to that health counter.
- **Health.** `GET /auth/delivery/status` (live-session-gated) lets a caller poll
  the dispatcher with its `receipt` to learn a send failed without holding the
  start request open.
- **Events observe, never queue.** An optional `Config.DeliveryEventsEmitter`
  publishes secret-free lifecycle events (delivered / skipped / retried /
  dead-lettered / superseded / purged). Those events are operational observation
  only: the dispatcher — not an event — is the record that accepted delivery work,
  an observer failure changes nothing about the already-recorded job state, and no
  event is ever required to make delivery happen. Wire the emitter for
  metrics/dashboards; leave it nil and delivery is unchanged.
- **Production fail-closed.** Under `DeliveryMode: "jobs"`, a missing
  `Config.DeliveryDispatcher` is rejected (`ErrDeliveryQueueRequired`), and a jobs
  delivery runtime the host never acknowledges running is rejected
  (`ErrDeliveryJobsUnacknowledged`). Under `DeliveryMode: "in_process"`, production
  requires the explicit crash-loss acknowledgment (`ErrDeliveryEphemeralUnacknowledged`).

### Composition and lifecycle ownership (host wiring)

`Register` starts **no** runtime in any mode — the host owns the delivery
lifecycle, exactly like the jobs and events pollers.

- **`jobs` mode.** The host builds a durable generic-jobs `Repositories.FencedQueue`,
  wires a **composition adapter** (the ONLY code allowed to import both features —
  neither feature core imports the other) that maps `auth.DeliveryDispatcher`
  (`Submit`/`Replace`/`LatestStatus`) onto the jobs fenced primitives
  (`EnqueueOnce`/`Replace`/`LatestStatusByKey`) and registers
  `authSvc.DeliveryJobRuntime().Handle` (+ its `Discard` terminal hook) under one
  job kind. The host then runs the generic `jobs.FencedRuntime` in its own
  goroutine (stop it after HTTP drains) and periodically calls the jobs
  `PurgeTerminal`. `examples/auth-cms/internal/authjobs` is the exemplary adapter,
  and `examples/auth-cms/cmd/server` is the executable wiring twin.

  ```go
  // composition root (main) — jobs mode
  dispatcher := authjobs.NewDispatcher(jobsSvc)     // auth.DeliveryDispatcher over the fenced primitives
  cfg.DeliveryMode = auth.DeliveryModeJobs
  cfg.DeliveryDispatcher = dispatcher
  cfg.DeliveryEncrypter = deliveryKey               // envelope always sealed
  cfg.DeliveryJobsAcknowledged = true               // asserts the host runs the jobs runtime
  authSvc, _ := auth.NewService(repos, cfg)
  rt, _ := jobs.NewFencedRuntime(jobsSvc, authjobs.FencedRuntimeConfig(authSvc.DeliveryJobRuntime()))
  go rt.Run(runtimeCtx)                              // host owns the loop; stop after HTTP drains
  ```

- **`in_process` mode.** No dispatcher, no jobs runtime; the host runs the bounded
  pool itself with `go authSvc.RunDelivery(ctx)` for the process lifetime and
  cancels the context to drain on shutdown (see the Quickstart). Set
  `DeliveryEphemeralAcknowledged: true` for production, and expect the ephemeral
  posture above.

## Provision-on-consumption — magic links for addresses with no account

By default a magic link sent to an address with no account delivers nothing: the
worker resolves the address, finds no owner, and terminates the job silently. With
`Config.PasswordlessProvisionOnRedeem` the link is delivered instead, and clicking
it **creates the account** (CHAU-6.x).

This is a real security decision, not a convenience toggle. Read the whole section
before enabling it.

### The one rule: creation happens at CONSUME, never at SEND

Sending writes **no** user row and **no** identifier row. A link that is never
clicked leaves nothing behind — there is no half-created account to clean up, no
row an attacker can create by spraying addresses at the start endpoint, and no
way to use the send path to squat an address.

Redemption is **POST-only**. No GET consumes a token, so a link scanner, a
preview fetcher, or an email-security crawler that merely *fetches* the URL
cannot provision anything.

### The intent is captured at issue

The link's stored binding records `provision_if_absent` at the moment it was
issued, and redemption reads it back **verbatim**. Flipping the configuration
later does not change what an already-mailed link does in either direction: a link
mailed while provisioning was off will not provision because the flag is on now,
and one mailed while it was on keeps its meaning if the flag is turned off.

The binding is **versioned**. A version this build does not understand is a
generic redemption failure, never a best-guess — misreading it could create an
account nobody asked for.

### The four outcomes

Redemption re-reads the **current** claim for the bound address (the ids recorded
at issue are an expectation, not an authority) and commits exactly one of:

| outcome | when | what happens |
|---|---|---|
| `login_existing_verified` | a verified, login-enabled claim | ordinary login for its **current** owner |
| `verify_and_adopt_existing_unverified` | an UNVERIFIED claim | the address is verified and adopted — **after** revoking the squatter's credentials |
| `provision_new` | no claim, and the link carried the intent | one active user + one verified primary email, created here |
| generic rejection | everything else | `ErrPasswordlessLogin` (401); nothing written |

All of it is **one store transaction**. A failure rolls back the token
consumption too, so a transient error leaves the link redeemable rather than
burning it.

### Races, and what each one does

- **Registered between send and consume.** The address gained a verified owner
  while the link was in flight → the **now-current owner** is logged in. No
  duplicate subject is created.
- **Registered-but-unverified between send and consume.** The address was claimed
  without being proven → the adoption branch. Clicking the link is the proof, so
  the claim is adopted and the **squatter's password, sessions, recent-auth
  grants, and outstanding reset/OTP/step-up challenges are revoked BEFORE the new
  session is written**. A completed adoption cannot leave one of their credentials
  alive. This is the same anti-takeover lesson the OAuth pending-link flow already
  encodes.
- **Two redemptions of one link.** The guarded consume serializes them: exactly
  one commits a session, the other gets the generic rejection.
- **Deactivated during the flow.** The redemption requires an active user inside
  the same transaction, so it refuses.
- **Resend across the unknown→registered boundary.** Every email link is keyed by
  a stable PII-free subject digest derived from the address, not by a user id, so
  a resend replaces its predecessor whether or not the address has an owner yet.

### What the caller learns: nothing

The public start is unchanged — it still resolves nothing on the request path, so
a known and an unknown address produce byte-identical responses. What provisioning
changes is whether the **worker** sends, not what the request reveals.

The redemption response never says "account created" versus "logged in": every
stable failure is one generic 401, and success is just a session. The hydration
endpoint shows the resulting user afterwards, exactly as it would for any login.

The **audit rail** is where an operator can tell them apart:
`passwordless_provisioned`, `passwordless_adopted`, and the ordinary
`passwordless_login`, each carrying the identifier kind, the challenge purpose,
and the outcome class — never the address, never the token.

### What a provisioned account looks like

One active user with an **empty display name** — the feature has no name to
invent, and asking for one would defeat the point of a link sign-in — and one
**verified**, primary, login/recovery/notification-enabled email identifier.
Fill in the profile afterwards through your own flow.

Pending invitations for that address resolve **once**, on the provisioning commit,
exactly as they do for a password registration or an OAuth provisioning. An
adoption or an ordinary login does **not** re-resolve them.

### PII and retention

The link's binding stores the **normalized address** in the challenge row's
context, because token redemption carries no identifier input and the redeemer
must know what ownership was proven. That is the same retention the pre-existing
known-user binding already had. It is bounded by the challenge TTL and the
existing purge contract. The delivery envelope holds the address encrypted. The
raw address never becomes a limiter key, a delivery logical key, a challenge
subject key, a log label, or an audit detail.

### Multi-instance requirement

Provisioning inherits every multi-instance requirement passwordless already has,
and they stop being optional: a **shared** rate limiter, a **durable** delivery
runtime (`DeliveryMode: "jobs"`), a **shared** store, and the **same**
`IdentifierKeyer` and `ChallengeProtector` key material on every instance.
Different key material across instances means different subject keys for the same
address, which means a resend from instance B will not replace instance A's link.

### Wiring

```go
cfg.Passwordless = []string{"email"}          // the only rail provisioning applies to
cfg.PasswordlessProvisionOnRedeem = true
cfg.PublicAuthBaseURL = "https://app.example.com/login/link"
cfg.IdentifierKeyer = yourKeyer               // the stable PII-free subject key
cfg.ChallengeProtector = yourProtector
// Repositories.Passwordless and Repositories.ActiveSessions come from the bundled
// pgx/turso stores automatically.
```

Anything missing is `ErrPasswordlessProvisionWiring` at construction — provisioning
never runs through a half-wired path.

### Schema

Migration **`0015_challenge_subject_keys.sql`** is append-only and must be applied
before deploying a binary built against the new store tag. It generalizes the
challenge row's single-active claim from `(user_id, purpose)` to
`(subject_key, purpose)`, backfilling `subject_key := user_id`, so every existing
purpose behaves exactly as before while an email link gains a key that exists
without a user.

The runnable proof — request a link for an unknown address, assert nothing exists
yet, redeem by POST, assert the account exists and the caller is signed in, assert
the replay fails, assert the invitation resolved once — is
`examples/auth-cms/cmd/server/passwordless_provision_test.go`.

## Password reset — the link rail

Reset mail used to print a raw token and tell the user to "use this token". That
is not a flow anyone can complete without a hand-built form. It now carries a
clickable link to a landing route the host configures (CHAU-5.x).

### Configuration

`Config.PasswordResetURL` is the **absolute public reset landing route, before the
token query parameter is appended** — for example
`https://app.example.com/reset-password`.

It is deliberately a **separate field** from `PublicAuthBaseURL`. That one is the
full passwordless landing URL (e.g. `.../login/link`), not an application origin,
so a reset route cannot be derived from it without guessing. Do not rename or
reinterpret it.

Validated at construction:

| rule | error | why |
|---|---|---|
| absolute `http`/`https` with a host | `ErrPasswordResetURLInvalid` | a relative or scheme-relative value cannot be mailed |
| HTTPS in production | `ErrPasswordResetURLInsecure` | the link carries a single-use credential |
| **no fragment** | `ErrPasswordResetURLInvalid` | the builder appends `?token=…`; after a `#` the token lands *inside* the fragment where the SPA's query parser will not find it |
| **no existing `token` parameter** | `ErrPasswordResetURLInvalid` | the builder would silently overwrite ambiguous host input |
| other non-secret query params | *allowed* | `.../reset?app=console` is preserved and the token is appended alongside |

**Compatibility posture:**

- **Production** — the field is **REQUIRED** whenever the challenge-backed
  forgot/reset rail is wired (`ErrPasswordResetURLRequired`). Raw-token-only reset
  mail is no longer an acceptable production experience.
- **Development** — empty is permitted with **one startup WARN**, and the bundled
  template falls back to the historical raw-token body so console and local flows
  keep working mid-migration.

Once a URL is present, **all** modes render link-only mail.

### Where the link comes from — and where it does not

The link is built in the **delivery worker**, off the request path, from the
configured value alone. Request `Host`, `Forwarded`, and `X-Forwarded-*` never
participate — the builder never sees a request. That is not a convention to
uphold; it is a structural property, and it matters because a Host-derived reset
link is an account-takeover primitive: an attacker who could set `Host` on a
forgot-password request would be mailed a link pointing at their own origin.
`TestPasswordResetLinkIgnoresRequestHeaders` drives a forgot-password request
with a hostile `Host`, `X-Forwarded-Host`, `Forwarded`, and `X-Original-Host` and
asserts the delivered link is unchanged.

Retry safety is unchanged: the rendered message is checkpointed before the
provider send, so a retry re-sends the **byte-identical** link and token rather
than minting another.

### The landing page's responsibilities

The token is a live credential sitting in the URL bar. A reset page should:

- read the `token` query parameter **once**, then `history.replaceState` it away
  so it does not sit in browser history or get shoulder-surfed;
- submit it by **POST** to `/auth/password/reset` — never re-navigate with it;
- send `Cache-Control: no-store` and `Referrer-Policy: no-referrer` on the landing
  response, so no shared cache retains it and no third party receives it in a
  `Referer`; and
- avoid loading third-party resources **before** the URL is scrubbed — an
  analytics beacon fired on load will carry the full URL.

### Template overrides

The bundled body is now link-only. An app content-template override that still
reads `{{.Secret}}` keeps working for **one compatibility window**: the renderer
passes both `.Link` and `.Secret`. Migrate overrides to `.Link` — `.Secret` is
deprecated *for reset presentation*, and removing it is a later breaking-release
decision, not a silent one. (`.Secret` remains in the encrypted delivery envelope
regardless; the terminal-failure discard path needs it to void a never-delivered
token.)

### Host example

```go
cfg.PasswordResetURL = "https://app.example.com/reset-password"
// or from the environment, via the AUTH_PASSWORD_RESET_URL tag
```

The public start (`POST /auth/password/forgot`) is unchanged: it still returns a
uniform 202 for every address and resolves nothing on the request path, so adding
a link added no enumeration signal.

## Verification resend — one use case, two surfaces

A user who never clicked the verification mail is locked out of a
`RequireVerifiedEmail` host with no way back. The fix is one use case exposed
twice, deliberately shaped differently (CHAU-2.x).

### `POST /auth/verification/resend` — public, enumeration-safe

Body `{"email": "..."}`. **Always answers `202 {"status":"accepted"}`**, with a
byte-identical status, body, and header set for every target state:

| target | response | what actually happens |
|---|---|---|
| unknown address | 202 accepted | opaque work submitted; the worker resolves nothing and sends nothing |
| malformed address | 202 accepted | budget charged; no work queued (the worker could never resolve it) |
| already verified | 202 accepted | opaque work submitted; the worker finds nothing to verify and sends nothing |
| deactivated + unverified | 202 accepted | opaque work submitted; the worker sends nothing |
| **active + unverified** | 202 accepted | the worker issues a **fresh** code, invalidating the previous one, and sends |

The request path does exactly three things — **normalize, rate-limit, submit** —
and then returns. It performs no account lookup, no challenge issue, no render,
and no provider call, because every one of those is a timing and behavior signal.
All of them happen later, in the delivery worker, off the request path.

**202 means ADMITTED, not delivered.** There is deliberately no pollable receipt
here: a receipt whose status eventually read "sent" versus "nothing to do" would
be exactly the oracle the uniform 202 exists to prevent. A user who does not
receive mail asks again.

Two things — and only two — produce a different response, and neither depends on
the target:

- **429** when a budget is exhausted: **3 per address per minute** and **10 per
  client IP per minute**, both keyed on PII-free digests and both charged before
  any resolution, so they bite identically for known, unknown, and malformed
  input. (They mirror the passwordless start budgets rather than inventing a
  second cooldown idiom.)
- **503** when the bounded delivery queue refuses admission, and **500** for
  other infrastructure failure. A saturated queue is answered honestly rather
  than with a 202 that quietly dropped the work.

**Gate.** Like the passwordless starts this is an unauthenticated
credential-establishment endpoint: it carries the allowlisted-`Origin` gate but
**not** the double-submit CSRF gate, because the population it serves has no
session and therefore no `__Host-auth_csrf` cookie to compare.

### Code replacement, and the in-flight duplicate

A resend uses delivery **replacement**, not idempotent enqueue: it supersedes any
still-pending verification job for the same address, including an undelivered
original registration mail, and issues a **new** challenge — which invalidates the
previous code.

Be honest about the limit: **replacement cannot retract a provider call already
accepted.** If the earlier message was already handed to the provider, the user
may receive two mails. Only the newest code verifies; the older one fails as
invalid. That is the correct trade — the alternative is re-sending a code the
user may never have received.

### `POST /auth/admin/users/{id}/verification/resend` — authorized

Mounts with the rest of the admin surface, so it exists only when the host wired
`Config.UserAdminCheck` (action `resend-verification`). Because the caller has
already been authorized, it **may** report real state:

| target | response |
|---|---|
| active + unverified | `202 {"status":"sent","receipt":"…"}` — a secret-free receipt, pollable via `GET /auth/delivery/status` |
| already verified | `409` code `already_verified` |
| deactivated | `409` code `user_deactivated` |
| no active primary email | `404` |
| unknown user | `404` |

It issues through the same challenge and delivery-replacement helper as the
public path, so the two cannot drift into parallel mail implementations.

### Audit

- `verification_resend_requested` — the PUBLIC request. Carries **no user id**
  (the request path never resolves one) and no address or code; `Details` carries
  the identifier kind only. Status distinguishes accepted from throttled, which
  is the same distinction the caller already sees.
- `verification_resend_issued` — a code actually re-issued, from the worker or
  the admin path. Carries the target user id (and, on the admin path, the actor),
  never the address or the code.

### Operator troubleshooting

A dead-lettered verification mail shows up in the generic jobs dead-letter with
its PII-free logical key, never the address. The user-visible fix is another
resend, which supersedes the dead job's key. A terminal delivery failure does
**not** restore the previous code: the replacement already invalidated it, so the
account waits for the next successful resend. Check the transport before the auth
feature — the worker's send is the only step that touches a provider.

### Downstream shape

The two surfaces serve two different screens, and a console should not confuse
them: the **public registration screen** offers "resend verification email" and
gets a uniform 202 it must not interpret; the **admin console's user row** offers
a "resend verification" control that reports real state and a receipt. Wiring the
admin control to the public route would throw away the operator feedback; wiring
the registration screen to the admin route would require authorization it does
not have.

## Account lifecycle — the operator directory and deactivation

An admin console needs two things authentication did not have: a list of users,
and a way to switch one off. Both ship here as an **opt-in, host-authorized**
capability (CHAU-1.1).

### The closed vocabulary

`UserStatus` has exactly two values in v1:

| status | meaning |
|---|---|
| `active` | the default, and the backfill for every pre-existing row |
| `deactivated` | no new session and no act-as-user authentication may succeed |

Suspension, deletion, anonymization, and lock-until are deliberately **out of
scope**: each needs its own rules on every credential path, and an open string
would let a host invent a posture the feature has no rules for. Adding a third
value is a design decision, not a constant.

Status is **not** verification. Whether an address is proven is identifier state;
whether the subject may authenticate at all is this. Either can be true without
the other.

### Deactivation is a real transition, not a flag

`DeactivateUser` (and the route) runs **one store transaction** that:

1. writes the status and its transition time;
2. increments `auth_revision` exactly once, invalidating any in-flight
   revision-CAS credential mutation; and
3. deletes **every** session row and authentication grant for the subject.

All three commit together or none does. An update-then-best-effort-delete is
explicitly not an acceptable implementation, because a crash between the two
would leave a deactivated user holding live sessions.

It is **idempotent**: deactivating an already-deactivated user returns
`changed:false` with no second revision increment and no audit event, so a
retried console request is safe.

### The mint fence (why `ActiveSessions` is required)

A status check and a session insert done as two steps is a race: a deactivation
committing between them leaves a live session on a deactivated user, and no
ordering in the service can close it. `Repositories.ActiveSessions` inserts the
session **only while the owning user is atomically proven active under the same
serialization boundary** — a locked row in Postgres, the `BEGIN IMMEDIATE` write
intent in libSQL. Exactly one of two things happens:

- the mint commits first, and the deactivation that follows deletes it; or
- the deactivation commits first, and the mint refuses.

Every mint routes through it — password login, `/auth/token`, OAuth login /
register / verify-link, passwordless code and link, and the password-mutation
remint — so a new login method inherits the fence rather than needing to
remember it. That is why wiring `UserAdminCheck` without it is
`ErrUserAdminReposRequired`: a host must not ship a deactivate button a
concurrent login can defeat.

### Denial is generic (this is a security property, not politeness)

Public credential endpoints **never** reveal that an account is deactivated. A
deactivated login returns byte-identical status and body to a wrong password; a
deactivated passwordless completion returns the same generic 401 every other
rejection does. Distinguishing them would turn the console's deactivate button
into an account-existence and account-status oracle. Operator surfaces — the
directory and the single-user read — report the real status, because the caller
there has already been authorized to see it.

### Revocation timing: live sessions vs stateless JWTs

Deactivation deletes the sessions, so anything gated on **`RequireLiveSession`**
(`/auth/me`, `/auth/methods`, `/auth/csrf`, every credential mutation, the admin
routes themselves) denies on the very next request. Refresh fails for the same
reason: the row is gone.

The **`RequireUser`** tier is stateless — it validates the access JWT's signature
and expiry without a session lookup — so an access token issued *before* the
transition stays acceptable on those routes for at most `Config.AccessTokenTTL`
(default 15 minutes). This is the same bounded asymmetry logout has always had,
and it is bounded by design rather than unnoticed. Two consequences worth
knowing:

- put anything that must revoke instantly behind `RequireLiveSession`; and
- note that `GET /auth/oauth/{provider}/link/start` currently uses `RequireUser`,
  so it carries that window (see the OAuth section below).

### Wiring it

```go
cfg.UserAdminCheck = func(ctx context.Context, req auth.UserAdminCheckRequest) error {
	// The host answers with ITS OWN policy. authentication never invents a role
	// named "admin", never interprets a role string, and never imports
	// features/authorization.
	if err := authorizer.Check(ctx, req.Principal, "platform:main", "admin"); err != nil {
		return err // a denial wraps sdk.ErrForbidden; an infra error fails closed too
	}
	// req.Action is one of list / read / deactivate / reactivate /
	// resend-verification; req.TargetUserID is empty only for list.
	return nil
}
```

A nil return authorizes. A denial and an infrastructure error **both** fail
closed — the feature never treats "the policy could not answer" as permission.
The resolved `Principal` reaches the check verbatim, machine principals included:
whether a service account may administer users is the host's call.

**Self-deactivation is not generically forbidden.** A host policy may allow an
administrator to act on their own account, and it may refuse. If your product
has a last-admin invariant, it lives in this check — the feature does not know
what an admin is and cannot enforce it for you.

### Using it without the bundled routes

The trusted methods are available whenever the repositories are wired, with or
without a check:

```go
page, err := svc.ListUsers(ctx, req)              // crud.Page[auth.UserSummary]
summary, err := svc.GetUserSummary(ctx, userID)
summary, change, err := svc.DeactivateUser(ctx, actor, userID)
summary, change, err := svc.ReactivateUser(ctx, actor, userID)
```

**They apply NO authorization.** They exist so a host can build its own transport,
console, or CLI without SQL or internal imports — and the caller owns the
decision the bundled handlers delegate to `UserAdminCheck`. `actor` is recorded
on the audit event only; it is never consulted for permission.

The runnable end-to-end proof (list → deactivate → prove generic denial on login,
refresh, and hydration → idempotent replay → reactivate → log in again) is
`examples/auth-cms/cmd/server/user_admin_test.go`.

### Schema

Migration **`0014_user_status.sql`** is append-only and must be applied before
deploying a binary built against the new store tag — see the store READMEs.

## OAuth account linking — two distinct flows

There are **two** ways a provider identity ends up attached to a user, and they
are not variations of each other. Conflating them is what makes people conclude
the explicit one is missing.

> **Version evidence.** `Service.StartLink`, the session-gated
> `GET /auth/oauth/{provider}/link/start` route, the server-side state's
> `LinkUserID` binding, the `ActionLinked` callback branch, `GET /auth/methods`,
> and the code-gated unlink pair **all shipped in `features/authentication/v0.1.0`**
> and are unchanged since. Nothing in this section requires a new tag: an adopter
> on any tagged version already has it.

### Flow 1 — explicit linking, initiated by a signed-in user

This is the settings-page "Connect your Google account" button.

```
[settings page]                  [feature]                     [provider]
   click Connect
   GET /auth/oauth/{p}/link/start?redirect=/settings/security
        ── session-gated (the caller IS the linking user) ──▶
                             persist server-side state:
                               provider, PKCE verifier, OIDC nonce,
                               resolved redirect, LinkUserID
        ◀── 302 to the provider authorization URL ──────────────▶ consent
                             GET /auth/oauth/{p}/callback?code=…&state=…
                             consume state (single use) → exchange code
                             → read identity → LinkUserID set, so:
                                 attach identity to THAT user
                                 → OAuthResult{Action: "linked"}
        ◀── 302 to the validated redirect ──
   GET /auth/methods → the provider now appears in `oauth[]`
```

Properties worth relying on:

- **The linking user comes from server-side state, never from the request.** The
  callback accepts only `code` and `state`; `LinkUserID` was bound at start. A
  leaked or replayed `state` redeemed by a *different* signed-in user still links
  to the state's owner. There is no query parameter that retargets a link.
- **No new session is minted.** The caller was already signed in, so the explicit
  callback sets **no** cookie and returns an empty token pair — re-issuing one
  would be a silent session swap. Only the `login`/`register` callback branches
  set cookies.
- **No second user is created.** The link lane never falls through to the
  register branch, even when the provider's email matches no identifier on file.
- **A provider identity owned by another user conflicts and does not move.** The
  second claimant's callback fails and the original owner keeps the link.
- **State is single-use** and expires (10 minutes); a replay is rejected.
- **The redirect is allowlisted at START.** `redirect` is resolved against
  `Config.RedirectAllowlist` before it is stored, so a non-allowlisted value is
  replaced by the same-origin default `/` and the callback can only ever land
  somewhere the host approved. The provider URL carries no redirect, verifier,
  nonce, or user id — only the opaque state token.
- **CSRF posture.** Both `link/start` and `callback` are GET redirect initiators,
  not state-changing form posts, so they are not behind the browser-safe-mutation
  Origin/CSRF gate. The anti-forgery property comes from the single-use
  server-side `state` (the OAuth login-CSRF defense) plus the session gate on
  `link/start`. A settings page navigates to `link/start` — it must not fetch it
  with XHR and follow the redirect itself.
- **Gate tier.** `link/start` uses the stateless `RequireUser` tier, so an
  already-issued access JWT stays usable until `AccessTokenTTL` even after the
  session is revoked. That bound is deliberate and documented rather than
  silently changed here; whether this route should move to `RequireLiveSession`
  is an open question for the lifecycle work (see
  `.claude/plans/coordination-hub-auth-upstream/01-user-directory-and-lifecycle.md`).
  The *sensitive* half of the pair — the unlink routes — already uses
  `RequireLiveSession`.

### Flow 2 — collision / pending link, initiated by an unauthenticated sign-in

This is the anti-takeover path, and it is **not** user-initiated linking.

```
[visitor]                        [feature]                     [provider]
   GET /auth/oauth/{p}/start ─────────────────────────────────▶ consent
   GET /auth/oauth/{p}/callback?code=…&state=…
        no LinkUserID; provider identity not linked;
        the verified provider email matches an existing account
        → mail a single-use secret to that address
        → OAuthResult{Action: "pending_link"}, NO session
   POST /auth/oauth/verify-link {token}
        → creates the link AND mints a session
```

The difference in one line: **flow 1 proves account ownership with a session and
does not mint one; flow 2 proves address ownership with a mailed secret and does
mint one.** Flow 2 additionally revokes a squatter password/sessions when the
matched identifier was unverified at flow start (design §5.7/V5).

**Presentation (config-only, security model unchanged).** Set `OAuthLinkBaseURL`
(`AUTH_OAUTH_LINK_URL`) to a host SPA route and the pending-link email becomes a
**clickable link** carrying the single-use secret in the URL **fragment**
(`…#token=<token>`), mirroring the magic link: the token never reaches the server on
the landing GET, and the SPA reads it client-side and POSTs `verify-link`. Leave it
empty and the email keeps its historical **bare-token** line (with one startup WARN
when providers are wired) — the emailed secret is still proof of inbox control
either way, so this is presentation only, never a boot requirement. On the
pending-link branch the callback redirect also carries **`?auth=link_sent&provider=<name>`**
(added through `url.Values`, preserving any existing query/fragment) so the landing
SPA can render a "check your email" state instead of a dead end. The value is
validated at construction like `PasswordResetURL` (absolute http(s), a host, **no
fragment** — the token owns it, HTTPS in production).

### Inventory and unlink

- `GET /auth/methods` (live-session-gated, `no-store`) is the linked-method
  inventory: `{has_password, oauth[], identifiers[]}`. A link shows up on the very
  next read — there is no confirmation step.
- Removing a link is the **code-gated pair**, not a DELETE:
  `POST /auth/oauth/{provider}/unlink/start` (live session + browser-safe
  mutation) mails a provider-bound code to the account's verified recovery
  identifier and returns a secret-free receipt; `POST /auth/oauth/{provider}/unlink`
  with `{code}` completes it. A code issued for one provider cannot unlink
  another.

### Settings-page client sketch

```ts
// Render: the inventory drives which providers show Connect vs Disconnect.
const inv = await fetch('/auth/methods', { credentials: 'include' }).then(r => r.json());
const linked = new Set(inv.oauth.map((o: { provider: string }) => o.provider));

// Connect: a NAVIGATION, not an XHR — the browser must follow the 302 to the
// provider and come back to the callback with its cookies.
function connect(provider: string) {
  const redirect = encodeURIComponent('/settings/security');
  window.location.assign(`/auth/oauth/${provider}/link/start?redirect=${redirect}`);
}

// Disconnect: the two-step code-gated pair, a browser-safe mutation, so it
// carries the Origin and the double-submit CSRF token from GET /auth/csrf.
async function disconnect(provider: string, csrf: string, code?: string) {
  const path = code
    ? `/auth/oauth/${provider}/unlink`
    : `/auth/oauth/${provider}/unlink/start`;
  const res = await fetch(path, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify(code ? { code } : {}),
  });
  return res.json(); // {status:"sent", receipt} then {status:"unlinked"}
}
```

After returning to `/settings/security`, re-read `GET /auth/methods`; the
callback itself carries no result body (it is a 302).

The runnable version of this recipe, driven over real HTTP through exported host
seams only, is `examples/auth-cms/cmd/server/oauth_link_settings_test.go`.

## Masked method inventory (design §5.1)

`GET /auth/methods` (live-session-gated, `Cache-Control: no-store`) returns the
caller's credential inventory projected from the same typed `MethodSet` the policy
evaluates and the mutation rail serializes (read and mutation never disagree):
`{has_password, oauth[], identifiers[]}` with each identifier's kind, **masked**
value (`a***@example.com`, `***4567`), verified time, `uses`, `primary` flag, and
an advisory `removable` hint (computed by evaluating the policy against the
proposed removal). **Identifier values are masked by default**; any full-value
read is a separate, explicitly authorized service method — never a query flag
accepted from HTTP. Replaced rows never appear. It fails CLOSED when the
credential rail is unwired.

## Security posture

- **Runtime modes.** `RuntimeMode` is required with no default. Production
  **fail-closes** on: a development-only/metadata-less delivery transport
  (`ErrInsecureDeliveryTransport`), a per-process rate limiter
  (`ErrNonDurableRateLimiter`), a missing identifier keyer
  (`ErrIdentifierKeyerRequired`), an HTTP magic-link base
  (`ErrPublicAuthBaseURLInsecure`), a `jobs`-mode config with no wired delivery
  dispatcher (`ErrDeliveryQueueRequired`), an unacknowledged `jobs` delivery runtime
  (`ErrDeliveryJobsUnacknowledged`), and an unacknowledged `in_process` crash-loss
  posture (`ErrDeliveryEphemeralUnacknowledged`). Development WARNs on each instead.

  **Deployment posture is app-wide vocabulary, not auth vocabulary.**
  `auth.RuntimeMode` is a **type alias** of `environment.Mode`
  (`sdk/foundation/environment`), and `RuntimeModeDevelopment`/`RuntimeModeProduction`
  alias `environment.ModeDevelopment`/`ModeProduction`. Nothing to migrate: the
  names are the same type, assignable in both directions, with the same
  `"development"`/`"production"` wire values and the same `AUTH_RUNTIME_MODE`
  env-tag parsing. What it buys is that a host's **general** mailer, notifier
  composition, or any other component can name the posture — and enforce the
  transport rule — without importing this feature:

  | old (still compiles) | canonical, feature-free |
  |---|---|
  | `auth.RuntimeMode` | `environment.Mode` |
  | `auth.RuntimeModeDevelopment` | `environment.ModeDevelopment` |
  | `auth.RuntimeModeProduction` | `environment.ModeProduction` |
  | `auth.ErrRuntimeModeRequired` | `environment.ErrModeRequired` |
  | `auth.ErrRuntimeModeInvalid` | `environment.ErrModeInvalid` |
  | `auth.ErrInsecureDeliveryTransport` | `email.ErrInsecureTransport` / `notify.ErrInsecureTransport` |
  | (none — the rule lived here) | `email.CheckSender(mode, sender)` / `notify.CheckNotifier(mode, n)` |

  Auth's sentinels **wrap** the canonical ones, so `errors.Is` matches either
  target and existing host checks are unaffected; only the message text gained a
  canonical suffix. Parse the mode with `environment.ParseMode` — it reads no
  environment variable itself, so the host still owns the key name:

  ```go
  mode, err := environment.ParseMode(environment.GetEnvOrDefault("AUTH_RUNTIME_MODE", ""))
  if err != nil { return err }
  cfg.RuntimeMode = mode // same type; no conversion
  ```

  A general mailer package then enforces the identical transport rule with no
  auth import at all:

  ```go
  package mailer // a host's own generic package

  func RequireProductionCapable(mode environment.Mode, sender email.Sender) error {
      posture, err := email.CheckSender(mode, sender)
      if err != nil {
          return fmt.Errorf("mail transport: %w", err) // wraps email.ErrInsecureTransport
      }
      if !posture.ProductionCapable() {
          log.Warn("development-only mail transport wired; never use in production")
      }
      return nil
  }
  ```

  **What stays feature-specific.** Only the *transport* verdict moved. The other
  production gates listed above — the durable-limiter check, the identifier
  keyer, the HTTPS public-link check, and the delivery-runtime acknowledgments —
  remain authentication's, because they are policies about auth's own
  collaborators. Auth also keeps its own startup WARN wording; the sdk validators
  take no logger deliberately, since message text and log routing are composition
  concerns.
- **Rate-limit / trusted proxy.** Login/refresh/start limits are always active and
  keyed on the PII-free identifier digest + client IP. The client IP is the
  `web.TrustProxies`-resolved value when the host wires it; **a raw
  `X-Forwarded-For` is NEVER trusted** (a client cannot forge it to rotate
  limiter keys or poison audit rows). A multi-instance host wires a shared limiter.
- **CSRF / origin / native clients.** Cookie-authenticated mutations require an
  allowlisted `Origin` + a double-submit `__Host-auth_csrf` token; credential-
  establishment endpoints enforce origin WITHOUT a pre-existing CSRF session
  (first-time sign-in has no cookie); bearer/native callers with no Origin skip
  the browser gate — origin enforcement never blocks a native client. A
  cross-origin SPA reads the token from `GET /auth/csrf` (above); an origin-gate
  refusal answers 403 `origin_rejected`, a failed double-submit 403
  `permission_denied`.
- **Security events.** When `SecurityEvents` is wired, every sensitive operation
  records an append-only row synchronously (see below). Audit content carries
  identifiers, key PREFIXES, kind, and purpose ONLY — raw codes, tokens, JWTs,
  passwords, provider tokens, and unmasked destinations never land in it. An
  audit-write failure is logged at WARN and NEVER fails the auth flow.
- **PII masking / retention / redress.** Identifier values are masked in reads and
  audit rows; an identifier change fans an independent notice to previously
  verified channels (never only the newly bound address), carrying change time +
  client context + a host redress path so a victim of a hostile change can react.

## Error codes (design §5.8)

Two families. **Explicit stable codes** (set via `WithCode`): `password_already_set`
(409), `password_not_set` (404), `cannot_remove_last_method` (409),
`kind_not_supported` (400), `rate_limited` (429), `verification_required` (409),
`identifier_exists` (409), `unsupported_media_type` (415), `origin_rejected`
(403 — the ORIGIN gate refused the request's `Origin`/`Sec-Fetch-Site`; distinct
from the `permission_denied` a failed double-submit or an authorization denial
carries, so a host can tell a misconfigured allowlist from a stale token without
branching on message copy. The message stays generic and never says which origin
would have been accepted). **Named challenge-rail
codes** (design §5.8, emitted by the shared transport mapper for the
challenge-redeeming endpoints — registration verify, step-up code, identifier
confirm, remove-password / OAuth-unlink code): `challenge_expired` (410),
`challenge_invalid` (400), `too_many_attempts` (403). The three collapse every
non-success disposition (no-such-challenge, wrong code, wrong context, malformed)
so they never distinguish the underlying cause (enumeration protection) and never
name a secret; the JSON body code and the form-arm re-render status derive from the
same mapping (transport parity — the form copy stays generic and never leaks the
code). **Generic sdk-kind codes** (via `web.RespondJSONDomainError`): `expired`
(410), `bad_request` (400), `permission_denied` (403), `not_found` (404),
`conflict` (409), `unauthenticated` (401). **Passwordless login stays a single
generic outcome** — a wrong/expired/wrong-context/unknown/disabled passwordless code
or magic link is one generic 401 that never distinguishes the reason and never names
a secret.

## The security-event audit rail (v3 events)

`register`; `login` (success/failure/blocked); `logout`; `email_verified`;
`password_change`, `password_set`, `password_remove_code_sent`,
`password_removed`, `password_reset`; the OAuth events (`oauth_login`,
`oauth_register`, `oauth_link_verified`, `oauth_linked`,
`oauth_unlink_code_sent`, `oauth_unlinked`); `apikey_auth`; `token_issued`;
`refresh` (with a `grace` detail) and `refresh_reuse` (blocked + unconditional
WARN even when `SecurityEvents` is nil, so a nil-audit host is never blind to
token theft); `step_up_challenge_sent`, `step_up` (success/failure/blocked); the
identifier events (`email_change_code_sent`/`email_changed`/`email_removed`,
`phone_change_code_sent`/`phone_changed`/`phone_removed`,
`identifier_uses_changed`); `passwordless_start` (accepted/blocked) and
`passwordless_login` (success/failure/blocked); the challenge rail's own
`challenge_lockout`; and the invitation events. The forgot-password *request*
records nothing (it must not reveal whether an address exists). There is no HTTP
read surface — query the table, or see the proof host's dev-only debug route.

## Sessions, access JWTs, and refresh rotation (2026-07-11)

**One mint path, two verification tiers.** Every credential-issuing flow (login,
`/auth/token`, OAuth callback, verify-link, password change/set/remove,
passwordless verify/redeem) routes through the one `mintSession` path, producing:

- a **session row** — the revocable anchor (id-keyed, `RefreshTokenHash` +
  rotation columns + v3 authentication-metadata columns `authenticated_at`,
  `authentication_methods`, `assurance_level`). It stores no access token.
- an **access JWT** — claims `{user_id, session_id, exp, iat}`, TTL
  `AccessTokenTTL`.
- an **opaque refresh token** — SHA-256-hashed into the session row, TTL
  `RefreshTTL` (a FIXED horizon).

Verification is a route seam: `RequireUser` is JWT signature + expiry only (zero
DB; revocation honored within ≤ `AccessTokenTTL`); `RequireLiveSession` is one PK
lookup (deleted/expired → deny immediately; API keys pass; fails CLOSED on
repository error).

**Refresh rotation** (`POST /auth/refresh`) is compare-and-swap, never blind:
`H = hash(token)` resolves once via `GetByRefreshHash`, then rotate (current) →
new pair; grace (previous, unused) → new access JWT only; reuse (previous, used)
→ revoke the session + `refresh_reuse` WARN + 401. Theft collapses correctly: a
thief on the stale token gets at most one grace access JWT; the second arrival on
the consumed slot burns the session. Two HttpOnly `SameSite=Lax` cookies: the
access-JWT cookie (`Path=/`) and the refresh cookie (`<name>_refresh`,
`Path=Config.RefreshCookiePath` — `/auth` by default, `/api/v1/auth` on a
prefixed host; the same path issues and clears it).

## Migrations are host-owned (0001–0015)

Auth ships **fifteen** canonical migrations per dialect, byte-identical filename
sets across pgx and turso:

```
0001_users              0006_service_accounts     0011_challenges
0002_user_passwords     0007_api_keys             0012_contact_changes
0003_sessions           0008_security_events      0013_authentication_grants
0004_oauth_accounts     0009_invitations          0014_user_status
0005_oauth_states       0010_user_identifiers     0015_challenge_subject_keys
```

Thirteen of those create tables; the last two **add columns** and are
**append-only**, precisely because the tables they touch are already tagged and
immutable:

- **`0014_user_status.sql`** — the account-lifecycle `status` /
  `status_changed_at` on `users`, the directory's `(created_at, id)` index, and —
  on pgx — the `COLLATE "C"` pin `users.id` needs now that it is a keyset tiebreak.
- **`0015_challenge_subject_keys.sql`** — `challenges.subject_key`, backfilled
  from `user_id`, with the single-active claim moving from `(user_id, purpose)` to
  `(subject_key, purpose)`. Every pre-existing purpose keys on the user id and is
  unchanged; an email magic link gains a stable PII-free key that exists even when
  the address has no account.

**Upgrading from a host that copied an earlier set:** re-export or copy the
missing files into your migration directory and apply them **before** deploying a
binary built against the new store tag. Both store constructors probe the added
COLUMNS by name — not just the tables — and refuse to construct otherwise, so a
missed migration fails at wiring time rather than mid-request. On pgx, `0014` also
runs `ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C"`, which **rewrites
the primary-key index under an ACCESS EXCLUSIVE lock** — schedule it accordingly
on a large users table.

Auth owns **no** delivery table: durable delivery is the generic **jobs**
feature's schema (host-owned in its own tree), and `in_process` delivery is
ephemeral. A host upgrading off an earlier v3 cut that scaffolded the bespoke
delivery-outbox table drains-then-drops it via the **Auth delivery-runtime
upgrade runbook** in `RELEASING.md` (stop old workers, drain or re-enqueue the
opaque encrypted commands WITHOUT decrypting, apply the generic jobs schema +
wiring, verify no active rows, drop the bespoke table, start the chosen runtime).

Per the greenfield-migrations rule (2026-07-12) the canonical set defines the
**FINAL** schema for a new host and carries NO upgrade/evolution file: the final
`0001_users.sql` has no `email`/`email_verified` (identity lives in
`user_identifiers`) and carries `auth_revision`; there is no legacy
`verification_codes`/`verification_tokens` table. `ExportMigrations` scaffolds the
tree into the host ONCE; from then on the files are the host's, applied pre-boot
by the host's runner. **Never renumber a scaffolded file** — the full filename is
the ledger identity. A live v2 host does NOT blind-copy these (copying the final
`0001_users.sql` drops email before any backfill); it runs the validated
host-owned **Auth v3 host upgrade runbook** in `RELEASING.md` instead (see the
UPGRADE NOTE below).

## Quickstart — the v3 minimum (dev, all defaults)

The required fields are `Hasher`, `Mailer`, `TokenSigner`, `RuntimeMode`, and
`DeliveryMode`; the challenge and delivery rails need their protector/encrypter,
and `in_process` mode needs the host to run the delivery runtime. A
single-instance dev host:

```go
cfg := auth.Config{
    Hasher:      bcrypt.New(),
    Mailer:      email.NewConsole(log),      // dev only; production uses SMTP/sendgrid
    MailFrom:    "auth@example.com",
    TokenSigner: signer,                      // cryptids.NewHS256(key) or golang-jwt
    RuntimeMode: auth.RuntimeModeDevelopment, // production has NO default

    ChallengeProtector: protector,            // HMAC pepper key ring (Challenges wired)
    DeliveryEncrypter:  deliveryKey,          // AES-GCM (command envelope always sealed)
    DeliveryMode:       auth.DeliveryModeInProcess, // bounded ephemeral pool; no dispatcher needed
    // For durable delivery set DeliveryMode "jobs" and wire Config.DeliveryDispatcher
    // over the generic jobs feature instead (recommended production posture).
    // AccessTokenTTL / RefreshTTL omitted → 15m / 7d.
    // Views: authViews                        // add HTML pages (authgoth.New(bundle)); omit for API-only
}
authSvc, err := auth.NewService(repos, cfg)   // repos, err := authstore.Repositories(db) — probes the 13 tables
// run the in-process delivery runtime for the whole process lifetime:
go authSvc.RunDelivery(ctx)
authSvc.Register(feature.Mount{Router: router, Logger: log})
```

**`examples/auth-cms/cmd/server` is this page's executable twin** — the full v3
surface (identifiers, challenge rail, delivery runtime, passwordless, HTML pages, a
branded page override) on in-memory stores with zero infra; its README carries the
JSON + HTML run-and-look protocol. Production hosts set `RuntimeMode=production`
(which fail-closes every incomplete wiring above), a stable shared secret per key,
SMTP/real notifiers, a durable/shared limiter, and an HTTPS `PublicAuthBaseURL`.

## Datastores — {turso, pgx} out of the box, or none at all

Both dialect stores ship and pass the same `storetest` suite (live runs recorded
in NOTES.md; turso against libSQL, pgx against docker postgres:17 with `C`
collation). A host may satisfy `Repositories` itself —
`examples/auth-cms/internal/authmem` is the zero-infra proof for every port.
Conformance is env-gated: turso via `-tags=integration` + `TURSO_*`; pgx via
`POSTGRES_TEST_DSN`. Child tables carry no enforced FKs (credential/identifier
atomicity lives in the `CreateWithPrimaryIdentifier`/`ApplyVerifiedChange`/
`PasswordResets.Reset` transactions, so no cascade can orphan a credential).

`integrations/cryptids/bcrypt` satisfies `PasswordHasher`;
`integrations/cryptids/golang-jwt` satisfies `cryptids.JWTSigner`;
`integrations/oauth/{google,github}` satisfy `oauth.Provider`;
`integrations/notify/mailer` bridges the email kind onto notify. None imports this
module and this module imports none — `features/authentication/go.mod` requires
exactly `sdk`.

## auth-v4 handoff (MFA — NOT shipped in v3)

v3 exposes stable **method** and **assurance** seams but implements NO
multi-factor authentication. The session carries authentication-method descriptors
and an assurance level, the credential policy evaluates a typed `MethodSet`, and
the step-up rail proves recent authentication — these are the reserved seams
auth-v4 builds MFA on: **typed authenticators** for passkeys/WebAuthn, TOTP, and
recovery codes as their own typed stores (like passwords and OAuth accounts);
**AAL2** assurance and step-up-to-AAL2 gating; and **factor replacement/reset**
flows. v3 ships none of these — no TOTP, passkey, or recovery-code MFA exists in
this milestone; the seams are reserved, not implemented.

## UPGRADE NOTE — v1 → v2 invalidates all live sessions

The session-hashing change (design §7.3) means a v1 host's existing plaintext
session rows never match a hashed lookup again. Deploying past it forces a mass
logout (users just log in again; no data lost); deploy in a single cutover or
drain first (a rolling deploy makes the same cookie flap 401/200); a rollback
forces a second mass logout. The same note lives in `RELEASING.md` keyed to this
module's tag.

## UPGRADE NOTE — the JWT-sessions + refresh change (sessions re-key)

The refresh change (2026-07-11, D6) re-keys the `sessions` table by id and gives
it refresh-rotation columns. Per the greenfield rule the canonical set ships the
FINAL shape (`0003_sessions.sql`); a host that scaffolded the earlier token-keyed
table writes its own `DROP TABLE sessions` + the canonical CREATE migration.
Deploying past it: every live session is invalidated (forced logout); do NOT
roll-deploy (old binaries SELECT the dropped `token` column and error outright);
a rollback requires reverting the schema and forces a second logout.
`AUTH_JWT_SECRET` is now required-and-SHARED for multi-instance hosts; `TokenTTL`
is removed (replaced by `AccessTokenTTL`/`RefreshTTL`); `POST /auth/token` returns
`{access_token, expires_at, refresh_token}`. Full runbook in `RELEASING.md`.

## UPGRADE NOTE — v2 → v3 identity (host-owned backfill migration)

auth-v3 (2026-07-13) reshapes `users` off `email`/`email_verified` onto
`user_identifiers`, adds `users.auth_revision` and session authentication-metadata
columns, adds the challenge / contact-change / authentication-grant flow tables
(the bespoke delivery-outbox table an earlier v3 cut shipped was removed by the
AV3D delivery refactor — see the delivery UPGRADE NOTE below), and retires the
legacy `verification_codes`/`verification_tokens` rail. Per the greenfield rule
the canonical set ships only the FINAL schema — a
live v2 host owns its own evolution and MUST NOT blind-copy the canonical
migrations (the final `0001_users.sql` no longer carries `email`, so copying it
onto a populated v2 `users` drops email before any backfill).

The full validated, step-by-step host-owned migration — exact pgx and
SQLite/libSQL SQL, collision dry-run, backfill, validation, additive metadata, and
the LATER destructive cutover — is the **Auth v3 host upgrade runbook** in
`RELEASING.md`. A host also crossing the AV3D delivery refactor (which removed the
bespoke delivery-outbox table) additionally follows the sibling **Auth
delivery-runtime upgrade runbook** in `RELEASING.md` — skip that table's CREATE
and wire generic-jobs / `in_process` delivery instead. The load-bearing
operational contract:

- **Backfill-first, single cutover — do NOT roll.** Steps 1–5 are additive (the
  v2 binary can still read the schema); deploy v3 and confirm health; only THEN
  drop the legacy columns/tables (Step 6, the point of no return for a v2
  rollback).
- **Collision-abort, never auto-resolve.** A non-empty normalized-email collision
  dry-run aborts for a human decision; `idx_user_identifiers_auth_claim` is the
  structural backstop — a forced backfill fails atomically with no partial write.
- **Verification STATE is preserved exactly** (verified → a non-NULL identifier
  `verified_at`, unverified → NULL); the timestamp is a documented `updated_at`
  proxy (v2 stored no proof time).
- **Sessions, passwords, OAuth accounts, and invitations are untouched** by the
  backfill — the identity reshape itself introduces no forced logout (the earlier
  sessions re-key note still applies to a host crossing that change).

This is a **breaking** version bump for `features/authentication` and both nested
store modules. Validated on fresh/reset databases both dialects (see the AV3-9.2
execution record in `RELEASING.md`); not yet applied to a real host.

## UPGRADE NOTE — the AV3D delivery-runtime refactor (delivery modes)

The delivery-refactor (2026-07-13) removed authentication's private durable
delivery queue. Durable delivery is now the generic **jobs** feature reached
through a host-wired `Config.DeliveryDispatcher`; the bounded ephemeral path is
`in_process`. Auth owns no delivery table (canonical set is `0001–0015`). See
"Delivery execution modes" above for the full model; this note is the compatibility
delta.

**Public removals (breaking):**

- `Repositories.DeliveryJobs` — removed. Durable delivery no longer has an auth
  repository port; wire `Config.DeliveryDispatcher` over the generic jobs feature.
- `domain/deliveryjob` — the bespoke delivery-job domain package is removed.
- `Service.RunDeliveryWorker` — removed. Run the generic `jobs.FencedRuntime` in
  `jobs` mode, or `Service.RunDelivery(ctx)` in `in_process` mode.
- Obsolete errors removed: `ErrNonDurableDeliveryRepository`,
  `ErrDeliveryWorkerUnacknowledged`, `ErrInProcessDurableDeliveryRepository`, and
  the delivery-durability construction funcs.
- The auth `0014` delivery-outbox migration is removed from both dialect trees
  (canonical set is now `0001–0015`).

**Renames:**

- `Config.DeliveryWorkerAcknowledged` → `Config.DeliveryJobsAcknowledged` (the
  wiring assertion that the host runs the generic jobs delivery runtime; production
  requires it under `jobs` mode).

**Additions:**

- `Config.DeliveryMode` (`"jobs"` | `"in_process"` | `"off"`, required, no default),
  `Config.DeliveryDispatcher` (the transport-neutral outbound seam),
  `Config.InProcessDelivery` (bounded-pool tuning), `Config.DeliveryEventsEmitter`
  (optional secret-free observer), `Config.DeliveryEphemeralAcknowledged`.
- `Service.RunDelivery(ctx)` (host-owned `in_process` runtime),
  `Service.DeliveryJobRuntime()` (the registered `jobs` kind/handler seam), and
  `Service.InProcessQueueDepth()` (secret-free depth for host health).
- Generic jobs gained the fenced surface: `Repositories.FencedQueue`, the fenced
  primitives, `jobs.FencedRuntime`, and migration `0003_fenced_job_queue` — see
  `features/jobs/README.md`.

**Behavior change:** `DeliveryStatus.Attempt` now always reads **0** — the status
seam is lifecycle-only; the attempt counter is executor-internal retry bookkeeping
and is no longer projected. The field is retained for compatibility.

**Adopter steps:**

1. Set `Config.DeliveryMode` explicitly (`jobs` recommended for production).
2. `jobs` mode: build a durable `jobs.Repositories.FencedQueue`, wire a composition
   adapter mapping `DeliveryDispatcher` onto the fenced primitives + registering
   `DeliveryJobRuntime().Handle`, run `jobs.FencedRuntime`, and set
   `DeliveryJobsAcknowledged: true` (see "Composition and lifecycle ownership").
3. `in_process` mode: run `go Service.RunDelivery(ctx)` and set
   `DeliveryEphemeralAcknowledged: true` (accept crash-loss).
4. A host that scaffolded the bespoke delivery-outbox table under an earlier v3 cut
   drains-then-drops it via the **Auth delivery-runtime upgrade runbook** in
   `RELEASING.md` (stop old workers, drain or re-enqueue the opaque encrypted
   commands WITHOUT decrypting, apply the generic jobs schema + wiring, verify no
   active rows, drop the bespoke table, start the chosen runtime).
