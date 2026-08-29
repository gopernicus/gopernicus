# pockets/authentication — one composable authenticator (`RequirePrincipal(opts…)`), named helpers, and the credential on the context

**Module:** `pockets/authentication` (next tag `v0.9.0` — BREAKING, pre-1.0: four
middleware names removed, `RequirePrincipal` gains a signature). `sdk` untouched;
store modules untouched; no schema.
**Status:** RELEASED 2026-08-29 — PR #18 squash @ `ad8b8a8`, `pockets/authentication/v0.9.0` (BREAKING, pre-1.0), cold-resolution verified through proxy.golang.org; host repin (gps-360-go `RequirePrincipal` → `RequireAccessTokenOrAPIKey()`, coordination-hub) is the owner's. PROPOSED 2026-08-29. Origin: gps-360-go `plans/33-principals-api-keys-
and-route-proof.md` D2. Owner rulings 2026-08-28/29: "an application may want
to use only bearer tokens… another one may want cookie sessions… and a user may
validate either way"; "it needs to be composable ORs… exporting the explicit
ones is just helper functions for common use cases"; breaking changes are fine.
Influence: gopernicus v1 `bridge/transit/httpmid/authenticate.go`
(`Authenticate(…, opts ...AuthOption)` with `UserOnly` / `WithUserSession` /
`ServiceAccountOnly`, header-over-cookie, an explicit stateless-vs-DB tier, a
richer context stash). v1 never let an app choose transports and re-resolved
per middleware instance; this plan adds the transport axis and narrows by
reading the stash.

## Context

The pocket resolves a request to ONE `identity.Principal` and stashes nothing
else (`identity.WithPrincipal` in `RequirePrincipal`, `RequireUser`,
`RequireServiceAccount`, `RequireLiveSession`; `internal/logic/authsvc/
machine.go:302-380`, `service.go:1088-1115`, `refresh.go:213-262`). Downstream,
an act-as-user API key is indistinguishable from a session, and the four fixed
names conflate two axes:

| axis | values | fact |
|---|---|---|
| credential | `access_token` (the session-backed JWT: claims `user_id` + `session_id`) · `api_key` | the cookie's value IS the access JWT (`service.go` `SetSessionCookies`: `Value: pair.AccessToken`) — one credential, two transports |
| transport | `cookie` · `header` (`Authorization: Bearer`) | today: header authoritative, cookie only when no bearer (`machine.go` `resolvePrincipal`) |
| liveness | stateless verify · `+ sessions.Get(session_id)` | meaningful for `access_token` only; a key is DB-checked at resolution |

Five resolvers exist (`resolvePrincipal`, `resolveUserID`, `resolveLiveSession`,
`RequireServiceAccount`'s inline path, the two Browser siblings) with one
divergence: `resolveUserID` (`RequireUser`) ignores a non-JWT bearer and reads
the cookie, so an API key plus a valid cookie passes `RequireUser` as the
cookie's user. Most read routes in a host admit "an access token or an API
key, by header or cookie"; sensitive routes want "a person's live session";
browser apps want cookies only; API hosts want headers only. The pocket should
express any such OR-set in one call and ship the common ones by name.

## Goal

`RequirePrincipal(opts ...PrincipalOption)` is the ONE authenticator: its
options are OR-sets over credentials and transports plus a liveness tier and a
browser denial mode; under an outer `RequirePrincipal` an inner one NARROWS
by reading the stash, never re-resolving; named helpers are one-line
pre-compositions; `CurrentCredential(ctx)` returns what authenticated the
request.

## Definition of Done

- `RequirePrincipal(opts ...PrincipalOption) web.Middleware` with `Accept`,
  `Transports`, `Live`, `Browser`; zero options = every wired credential, both
  transports, header authoritative.
- `Config.BundledRouteAuth` exposes optional principal strategies for the
  bundled route groups. Unset fields use the audited defaults; a host can
  replace one group without changing the others or constructing the `Service`
  first.
- Narrowing: an inner `RequirePrincipal` under an outer one checks the stashed
  `Credential` against its set (and runs the live lookup if `Live()`), never
  re-resolves.
- `CurrentCredential(ctx) (Credential, bool)`; `CurrentPrincipal` /
  `CurrentUser` / `CurrentSessionID` unchanged.
- The six helpers below exist and are literally the calls they name.
- `RequireUser`, `RequireServiceAccount`, `RequireLiveSession`,
  `RequirePrincipalBrowser`, `RequireLiveSessionBrowser` are REMOVED; every
  pocket-internal use across `internal/inbound/authentication/*.go` is rewritten
  according to the route audit below; `AuthenticateAPIKey` is also REMOVED (raw
  credential verification is not an application-service entry point);
  `examples/*` and the bundled route tests compile and pass.
- The credential × transport × option matrix is tested hermetically; README
  "The middleware surface" rewritten; RELEASING entry + migration table;
  `make check` green.

## Out of scope

- Key-scoped roles (a key narrower than its owner) — `pockets/authorization`.
- `sdk/foundation/identity` — its scope fence keeps credentials with the
  credential owner; `Credential` is pocket-owned, read through `Service`.
- The `features/authentication` maintenance line (`v0.4.x`) — forward-port
  only if coordination-hub asks.
- Basic auth, mTLS, a third credential; per-key rate limits; step-up policy.

## Layering and composition boundary

Authentication posture stays at the inbound edge: middleware decides whether a
request reaches a handler, then the handler calls the application. This change
does not add authentication or authorization checks to application logic,
domain code, repositories, or outbound adapters.

Nested narrowing trusts the credential stash written by an earlier
`RequirePrincipal` in the same request chain. The supported application wiring
invariant is one authentication `Service` per chain; composing middleware from
independently configured authentication `Service` instances on the same chain
is a host wiring error. The pocket does not add service-instance provenance or
repeat verification to defend against that misconfiguration.

## Schema / datastore impact

None. `sessions.Get` is the same one-PK lookup `RequireLiveSession` performs
today; the `apikey_auth` security event is unchanged.

## Module / API impact

`pockets/authentication` only. All in `authentication.go`, forwarded from
`internal/logic/authsvc`:

```go
// ── Vocabulary ──────────────────────────────────────────────────────────────
type CredentialKind string
const (
	CredentialAccessToken CredentialKind = "access_token" // the session-backed JWT, by cookie or header
	CredentialAPIKey      CredentialKind = "api_key"      // a service account's key: its own principal, or act-as-user
)
type Transport string
const (
	TransportHeader Transport = "header" // Authorization: Bearer <token>
	TransportCookie Transport = "cookie" // the access-JWT session cookie
)

// What the authenticator stashes beside the Principal (pocket-private context
// key, like sessionID / clientInfo). Zero value when unauthenticated.
type Credential struct {
	Kind             CredentialKind
	Transport        Transport
	SessionID        string // access_token: the JWT's session_id claim (proven live only after Live())
	APIKeyID         string // api_key
	ServiceAccountID string // api_key: the owning account, act-as-user or not
	ActAsUser        bool   // api_key
}
func (s *Service) CurrentCredential(ctx context.Context) (Credential, bool)

// ── The primitive ───────────────────────────────────────────────────────────
type PrincipalOption func(*principalSet)
func Accept(kinds ...CredentialKind) PrincipalOption // OR-set of credentials; default: every wired kind
func Transports(ts ...Transport) PrincipalOption    // OR-set of transports;  default: header + cookie
func Live() PrincipalOption                          // access_token ⇒ the session row must exist; api_key ⇒ pass
func Browser() PrincipalOption                       // on denial 303 to Config.BrowserLoginPath (validated return_to) instead of a JSON 401

// One authenticator. At the OUTERMOST position it resolves the request's
// credential within its set and stashes Principal + Credential; NESTED under an
// outer RequirePrincipal it never re-resolves — it checks the stashed Credential
// against its set (401 when outside it; a Live() inner runs the lookup once).
func (s *Service) RequirePrincipal(opts ...PrincipalOption) web.Middleware

// ── Helpers: one-line pre-compositions, exported so the common lines read ──
func (s *Service) RequireAccessTokenOrAPIKey() web.Middleware     // RequirePrincipal()                                                   — most read routes
func (s *Service) RequireAccessTokenOrAPIKeyLive() web.Middleware // RequirePrincipal(Live())                                             — sensitive; keys still pass
func (s *Service) RequireAccessToken() web.Middleware             // RequirePrincipal(Accept(CredentialAccessToken))                      — a person, either transport
func (s *Service) RequireAccessTokenLive() web.Middleware         // RequirePrincipal(Accept(CredentialAccessToken), Live())              — "a key never mints a key"
func (s *Service) RequireAccessTokenCookie() web.Middleware       // RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie)) — browser apps
func (s *Service) RequireAPIKey() web.Middleware                  // RequirePrincipal(Accept(CredentialAPIKey))                           — machines only

// ── Bundled-route overrides ─────────────────────────────────────────────────
// Opaque so "unset" remains distinguishable from an explicit zero-option
// RequirePrincipal(). PrincipalStrategy() with no arguments deliberately means
// RequirePrincipal() (all wired credentials, both transports, stateless).
type RoutePrincipalStrategy struct { /* pocket-private configured bit + options */ }
func PrincipalStrategy(opts ...PrincipalOption) RoutePrincipalStrategy

type BundledRouteAuthentication struct {
	OAuthLinkStart       RoutePrincipalStrategy
	SessionSecurityReads RoutePrincipalStrategy
	SessionHydration     RoutePrincipalStrategy
	CredentialManagement RoutePrincipalStrategy
	MachineLifecycle     RoutePrincipalStrategy
	UserAdministration   RoutePrincipalStrategy
	Invitations          RoutePrincipalStrategy
	BrowserAccount       RoutePrincipalStrategy
}

type Config struct {
	// ...existing fields...
	BundledRouteAuth BundledRouteAuthentication
}
```

`BundledRouteAuth` configures authentication only. It cannot remove the
authenticator from a protected bundled surface, inject application
authorization into logic, or replace the separate host authorization seams
(`MachineRoutesGate`, `UserAdminCheck`, `InviteCheck`). Construction resolves
each strategy to a concrete middleware after the internal service exists, then
passes those middlewares to the inbound route registrar; the host never needs a
half-constructed `Service`.

An override also owns the inbound context contract for that surface. Some
handlers require `CurrentUser`; session-bound step-up and credential handlers
additionally require `CurrentSessionID`. For example, configuring an API-key or
stateless strategy can authenticate a caller but cannot manufacture the live
session id those handlers require; they still fail closed at the inbound
handler boundary. The README lists these requirements beside each override so
a host does not mistake “credential accepted” for “credential can satisfy the
route's application input.”

Example — make invitations require a person's live access-token session while
leaving every other bundled route on its default:

```go
Config{
	BundledRouteAuth: BundledRouteAuthentication{
		Invitations: PrincipalStrategy(
			Accept(CredentialAccessToken),
			Live(),
		),
	},
}
```

Removed (BREAKING) and their replacements — the RELEASING migration table:

| removed | replacement | note |
|---|---|---|
| `RequirePrincipal` (method value, `func(http.Handler) http.Handler`) | `RequirePrincipal()` / `RequireAccessTokenOrAPIKey()` | every `web.Middleware` call site gains `()` |
| `RequireUser` | `RequireAccessToken()` | **correction:** a non-JWT bearer no longer falls through to the cookie (key + cookie ⇒ 401) |
| `RequireServiceAccount` | `RequireAPIKey()` or `RequirePrincipal(Accept(CredentialAPIKey), Transports(TransportHeader))` | identical behavior with the transport spelled |
| `RequireLiveSession` | `RequireAccessTokenOrAPIKeyLive()` | identical |
| `RequirePrincipalBrowser` | `RequirePrincipal(Browser())` | identical |
| `RequireLiveSessionBrowser` | `RequirePrincipal(Live(), Browser())` | identical |
| `AuthenticateAPIKey(ctx, rawKey)` | no direct replacement | raw credential verification moves behind `RequirePrincipal`; read the result with `CurrentPrincipal` / `CurrentCredential` |

Unchanged: `CurrentPrincipal`, `CurrentUser`, `CurrentSessionID`,
`Config.MachineRoutesGate` (its stack becomes the resolved
`BundledRouteAuth.MachineLifecycle`, then `browserSafe`, then the gate; the
default strategy is `RequireAccessTokenLive()`).

## Bundled-route posture audit

The old `RequireLiveSession` admitted both access tokens and API keys. Its
replacement is therefore NOT mechanical: each bundled route names the
credential policy it actually needs. Public routes are unchanged and carry no
principal middleware.

| bundled surface / config slot | routes | default | ruling |
|---|---|---|---|
| `OAuthLinkStart` | `GET /auth/oauth/{provider}/link/start` | `RequireAccessToken()` | preserve today's stateless access-token posture; an API key cannot start a human OAuth link |
| `SessionSecurityReads` | `GET /auth/delivery/status`, `/auth/methods`, `/auth/csrf` | `RequireAccessTokenLive()` | these are explicitly established-session surfaces; `/auth/methods` exposes human credential inventory and `/auth/csrf` bootstraps a browser session |
| `SessionHydration` | `GET /auth/me` | `RequireAccessTokenOrAPIKeyLive()` | preserve the documented act-as-user-key behavior; a self-acting service-account principal still fails the handler's `CurrentUser` requirement |
| `CredentialManagement` | password change/set/remove, step-up begin/completion, identifier add/confirm/update/delete, and OAuth unlink start/completion | `RequireAccessTokenLive()` | these mutate or prove a human credential and several bind work to `CurrentSessionID`; no API key, including act-as-user, substitutes for a session |
| `MachineLifecycle` | create/list service accounts; mint/list/revoke keys | `RequireAccessTokenLive()` | a key never creates, reads, mints, or revokes keys by default; replaces the current two-authenticator stack |
| `UserAdministration` | all `/auth/admin/users…` reads and mutations | `RequireAccessTokenOrAPIKeyLive()` | preserve the explicit contract that a machine principal reaches `Config.UserAdminCheck`; the host decides whether it is authorized |
| `Invitations` | authenticated create/list/mine/accept/cancel/resend routes | `RequireAccessTokenOrAPIKeyLive()` | preserve current behavior: self-acting service accounts fail `CurrentUser`, while an act-as-user key acts as its effective human principal |
| `BrowserAccount` | HTML account, identifier/password/step-up, OAuth-unlink GETs; HTML-only identifier edit POST | `RequirePrincipal(Accept(CredentialAccessToken), Transports(TransportCookie), Live(), Browser())` | bundled browser UI reads only its cookie, requires a live session, and redirects on denial; the form-only POST also retains `browserSafe` |

The JSON/form-dispatched mutation routes keep `RequireAccessTokenLive()` (both
header and cookie transports): JSON clients may use a bearer access token and
browser form arms use the cookie, while `browserSafe` continues to distinguish
and protect the browser transport.

## Resolution rules (`resolveCredential(r, set)` — the one resolver)

1. **Outermost** (no stash on the context):
   a. If `header` ∈ set and a bearer is present: classify by shape (`isJWTToken`,
      exactly two dots) — JWT ⇒ `access_token`, else ⇒ `api_key`. If the kind
      ∉ set, or `api_key` with `!MachineEnabled()`, or verification fails ⇒
      **deny**. A consulted bearer is authoritative: the cookie is never read
      after one.
   b. Else if `cookie` ∈ set and the access cookie is present ⇒ `access_token`
      (deny if ∉ set or verification fails).
   c. Else deny.
   d. A credential arriving by a transport ∉ set is **ignored** (owner ruling:
      the set says what the surface reads; symmetry with a cookie at a
      header-only group; a never-consulted header is not a bypass).
   e. On success stash `Principal` (as today) + `Credential`; if `Live()`:
      `access_token` ⇒ `sessionLive(ctx, cred.SessionID)` (missing / expired /
      repository error ⇒ deny, fails CLOSED as today) and `withSessionID`;
      `api_key` ⇒ it was already fully checked by the private API-key credential
      path during resolution, so pass without another lookup.
2. **Nested** (a stash exists): `Kind ∈ Accept` and `Transport ∈ Transports`
   else deny; `Live()` ⇒ the same lookup as 1e (once — a second nested `Live()`
   reads `CurrentSessionID` and passes). Never re-resolves; widening past the
   outer set is impossible by construction (the stash only holds what the
   outer set admitted) and surfaces as a runtime 401 — a middleware cannot see
   its group, so there is no mount-time check.
3. **Denial**: JSON 401 (byte-stable with today's `writeUnauthorized`), or with
   `Browser()` the 303 with validated `return_to` on GET/HEAD (today's
   `redirectToBrowserLogin`). A nested denial under `Browser()` also 303s
   only if the inner carries `Browser()`; helpers never do.
4. `Accept()` / `Transports()` with zero arguments ⇒ panic at construction (a
   set that admits nothing is a programming error, like a gate naming an
   undeclared permission).

## Risks

1. **Breaking rename across every consumer** (this repo's `examples/*`, the
   pocket's internal mounts, coordination-hub, gps-360-go). Mitigation: the
   public migration table plus the separate bundled-route audit above;
   `make check` compiles the examples.
2. **`RequireUser` → `RequireAccessToken()` correction** (key + cookie ⇒ 401).
   Mitigation: pinned test; named in RELEASING as a security tightening.
3. **Five resolvers → one** touches every gate. Mitigation: task 2 must pass
   the PRE-CHANGE `TestRequirePrincipal*`, `TestRequireServiceAccount*`,
   `browser_middleware_test.go` and refresh/live tests (renamed mechanically)
   before the new matrix lands.
4. **Nested `Live()` double lookup** if a helper with `Live()` sits on a group
   and again on a route. Mitigation: rule 2 — the second reads
   `CurrentSessionID` and passes.
5. **Bundled routes did not all mean the same thing by `RequireLiveSession`.**
   Mitigation: the route audit above is authoritative; an inbound route-policy
   regression test pins access-token-only, API-key-capable, and browser-cookie
   groups independently. This deliberately tightens the routes classified as
   established-session or human-credential surfaces (for example an API key no
   longer reaches delivery-status), while preserving documented API-key access
   to user administration, invitations, and `/auth/me`.
6. **Removing direct `AuthenticateAPIKey` callers.** Mitigation: repo search
   shows no production caller outside the three middleware resolvers and the
   public forwarding wrapper; resolver tests absorb its success/denial/audit
   coverage. External callers migrate to middleware + `CurrentCredential`.
7. **A host needs a different bundled-route posture.** Mitigation:
   `Config.BundledRouteAuth` overrides one semantic surface at a time through
   the same option vocabulary. Zero-value fields retain audited defaults;
   override resolution is copied at construction and remains immutable while
   serving.

## Tasks

### task-1: vocabulary, the set, the stash, `CurrentCredential`

- **depends_on:** []
- **model:** opus
- **files:**
  `pockets/authentication/internal/logic/authsvc/credential.go` (new: `CredentialKind`, `Transport`, `Credential`, `principalSet`, `PrincipalOption`, `Accept`, `Transports`, `Live`, `Browser`, `defaultSet(s)`),
  `pockets/authentication/internal/logic/authsvc/context.go` (`credentialKey`, `withCredential`, `CurrentCredential`),
  `pockets/authentication/authentication.go` (type aliases, option forwarders, `CurrentCredential`, `RoutePrincipalStrategy`, `PrincipalStrategy`, `BundledRouteAuthentication`, and `Config.BundledRouteAuth`)
- **verify:** `cd pockets/authentication && go build ./... && go vet ./... && go test ./internal/logic/authsvc/`
- **description:** Vocabulary + a pocket-private stash beside `sessionIDKey`/`clientInfoKey`. `defaultSet` = `access_token` always (`TokenSigner` is required) + `api_key` when `MachineEnabled()`; both transports. Nothing writes the stash yet.

### task-2: the one resolver and `RequirePrincipal(opts…)`; the old names removed

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  `pockets/authentication/internal/logic/authsvc/machine.go` (`resolvePrincipal` → `resolveCredential(r, set) (Principal, Credential, bool)`; `RequirePrincipal(opts…)` with the outermost/nested branch; delete `RequireServiceAccount`, `RequirePrincipalBrowser`, and exported `AuthenticateAPIKey`; fold its lookup, revocation/expiry, effective-principal, audit, and best-effort touch behavior into a private detailed API-key credential resolver),
  `pockets/authentication/internal/logic/authsvc/service.go` (delete `RequireUser`, `resolveUserID`),
  `pockets/authentication/internal/logic/authsvc/refresh.go` (delete `RequireLiveSession`, `RequireLiveSessionBrowser`, `resolveLiveSession`; keep `sessionLive`),
  `pockets/authentication/internal/logic/authsvc/credential.go` (the six helpers),
  `pockets/authentication/authentication.go` (forwarders; resolve each zero-value/custom bundled-route strategy after `authsvc.Service` construction; retain the resolved immutable middleware set on `Service`; remove the old middleware wrappers and `AuthenticateAPIKey` wrapper at lines 2168–2230),
  `pockets/authentication/internal/inbound/authentication/routes.go` (`Deps` gains the resolved bundled-route middleware set),
  `pockets/authentication/internal/inbound/authentication/{routes,me,sessions,methods,useradmin,stepup,account_forms,html,identifiers,machine,password,invitation}.go` (rewrite mounts exactly according to "Bundled-route posture audit" using the resolved strategy slots; the default machine stack ⇒ `MachineLifecycle, browserSafe, gate`),
  `examples/*/cmd/**` (call sites gain `()` or a helper)
- **verify:** `cd pockets/authentication && go build ./... && go vet ./... && go test ./...` with PRE-CHANGE resolver tests renamed to the new names; route tests whose old assertions conflict with the authoritative audit (notably API-key access to delivery-status) are rewritten in task-3; then repo root `make build && make vet`
- **description:** Collapse the five resolvers and the direct API-key authenticator into `resolveCredential`; implement rules 1–4; wire the helpers; remove the six old exported entry points and migrate every internal and example call site. The private API-key branch returns `Principal` + `Credential` from its single `apikey.APIKey` / `serviceaccount.ServiceAccount` resolution while preserving exactly one auth event and one best-effort `TouchLastUsed` on success.

### task-3: the matrix

- **depends_on:** [task-2]
- **model:** opus
- **files:**
  `pockets/authentication/internal/logic/authsvc/credential_test.go` (new),
  `pockets/authentication/internal/logic/authsvc/machine_test.go` (move direct `AuthenticateAPIKey` cases onto the private resolver / middleware contract; the fall-through case now asserts 401),
  `pockets/authentication/internal/logic/authsvc/browser_middleware_test.go` (renames to `RequirePrincipal(Browser())` / `(Live(), Browser())`; one case: a nested non-Browser helper under a Browser outer answers JSON 401),
  `pockets/authentication/internal/inbound/authentication/{machine_gate,me}_test.go` (retain the act-as-user `/auth/me` proof; change the API-key delivery-status control from 400/reached to 401/not reached),
  `pockets/authentication/internal/inbound/authentication/principal_posture_test.go` (new: bundled-route default and override regression matrix)
- **verify:** `cd pockets/authentication && go test ./... && go vet ./...`; repo root `make check`
- **description:** Table test over credential {access JWT valid / bad signature / expired, api key valid / act-as-user / revoked / expired / unknown, two-dot garbage, none} × transport {cookie, header, both (same JWT), invalid header + valid cookie, key + cookie} × set {default, `Accept(access_token)`, `Accept(api_key)`, `Transports(cookie)`, `Transports(header)`, `Accept(access_token)+Transports(cookie)`, each ± `Live()`}, asserting status, the stashed `Credential` (kind, transport, `SessionID` / `APIKeyID` / `ServiceAccountID` / `ActAsUser`) and `CurrentPrincipal`. Nested: every helper under a default outer (narrowing, no re-resolution — assert the API-key repository is not hit twice); a nested `Accept(api_key)` under an `Accept(access_token)` outer ⇒ 401; nested `Live()` under an outer `Live()` ⇒ one lookup. `Live()`: live row ⇒ 200 + `CurrentSessionID`; deleted row ⇒ 401 while the stateless authenticator alone still 200s (the documented staleness window); repository error ⇒ 401; api key ⇒ 200 with `CurrentSessionID` absent. API-key success performs one repository resolution, one `apikey_auth` event, and one best-effort touch; denial preserves today's generic error and audit branches. Zero-argument `Accept()` / `Transports()` ⇒ panic. Pin the six helpers as regression rows, including `RequireAccessToken()` + (api key + valid cookie) ⇒ 401 — the correction. The bundled-route default matrix proves: user-admin accepts a service-account key and delegates to the host check; `/auth/me` and invitations preserve act-as-user keys but reject self-acting service accounts; session/security and credential-mutation routes reject every API key; machine lifecycle rejects a key even alongside a valid cookie; HTML account routes ignore headers, accept a live cookie, and redirect on denial. Override rows configure each slot in isolation, prove only that surface changes, prove `PrincipalStrategy()` means explicit primitive defaults rather than "unset", and prove all unconfigured slots retain their audited defaults.

### task-4: docs, RELEASING, plan status

- **depends_on:** [task-3]
- **model:** sonnet
- **files:**
  `pockets/authentication/README.md` ("The middleware surface": the three axes, the primitive and its options, the nesting rule and one-Service-per-chain invariant, the helper table, the migration table, the bundled-route defaults and override example, and the correction; the machine-identity section cites the default `MachineLifecycle` posture for "a key never mints a key"),
  `RELEASING.md` (chronological entry + upgrade note: `v0.9.0` BREAKING, the migration table including `AuthenticateAPIKey` removal, the `RequireUser` correction, the bundled-route defaults/override seam, and the ignore-on-un-admitted-transport rule),
  `.claude/plans/authentication-principal-posture.md` (Status → EXECUTED; tag by owner)
- **verify:** `make check`
- **description:** Document; no code. The owner cuts the tag; the originating host repins (`go mod edit -require …@v0.9.0 && go mod tidy && go mod vendor`) and migrates its one call site (`router.Group("/api/v1", authenticationSvc.RequirePrincipal, …)` ⇒ `RequireAccessTokenOrAPIKey()`).

## Sequencing

1 → 2 → 3 → 4, one PR (`authentication-principal-posture`), squash. Task 2 is
the risky one: keep the pre-change test bodies and only rename until task 3.

## Open questions

_None — resolved by owner ruling 2026-08-29:_ composable OR-sets are the
primitive; helpers are exported one-liners; breaking removal of the old names
and `AuthenticateAPIKey` is accepted; authentication posture remains inbound;
nested middleware in one chain belongs to one `Service`; bundled mounts follow
the route audit above by default and hosts may override one semantic route
group through `Config.BundledRouteAuth`; a credential on an un-admitted
transport is ignored, not denied; helper vocabulary is `AccessToken` / `APIKey`
/ `Live` / `Cookie` to match the constants.

## Recommended reviews

`lead-backend-engineer` (the one-resolver collapse; the nested-narrowing
branch; `Live()` fail-closed parity), `architecture-steward` (`Credential`
stays pocket-owned — not sdk `identity`), `platform-sre` (the `RequireUser`
correction as a security tightening; the breaking migration across hosts),
`product-manager` (helper names — the vocabulary hosts will type).

## Notes

- The originating host (gps-360-go) changes one line in `cmd/server/main.go`
  and adopts `RequireAccessTokenLive()` on three self-service key routes and
  `CurrentCredential` for `whoami` / audit attribution (its plans/33 D3–D5).
- gopernicus v1's `httpmid.Authenticate` reached the options shape first; the
  differences here are the transport axis, narrowing by stash instead of a
  second resolving instance, and the exported helpers.
