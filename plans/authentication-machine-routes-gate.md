# Machine-identity lifecycle routes come standard — the host supplies the gate, the feature scopes ownership

**Feature:** `features/authentication` (root + `internal/inbound/authentication` + `internal/logic/authsvc` + `domain/securityevent`; `examples/auth-cms` as the proof host). No store module changes.
**Issue:** gopernicus/gopernicus#6
**Status:** RELEASED 2026-08-26 — `features/authentication/v0.6.0` @ 9793c2a (cold-resolved: `MachineRoutesGate` present, `MachineRoutesDisabled` fails to compile); issue #6 closed with the D3/D4/D5 scope amendments recorded. RATIFIED 2026-08-26 — owner accepted all six recommendations (Q1 remove `MachineRoutesDisabled` now; Q2 `RequireUser, RequireLiveSession, gate`; Q3 `owner_user_id` refused by name one release; Q4 D3/D4 amendments accepted, no store train; Q5 decorate seam deferred + README corrected; Q6 v0.6.0 minor). Executing T1–T6.
**Traced against:** `main` @ `f546a64` (= `features/authentication/v0.5.4`, tagged 2026-08-25; `git tag --list 'features/authentication/*'` confirms v0.5.4 is the latest core tag; stores are `pgx/v0.4.0`, `turso/v0.3.0`).
**Target tag:** `features/authentication/v0.6.0` (minor: an intended behaviour change — ungated routes no longer mount — plus one removed `Config` field). Store modules NOT retagged (D7).
**Reference implementation:** `/Users/jrazmi/code/gps/three-sixty/gps-360-go/features/auth/inbound/machine.go` + `machine_test.go` @ `89ba46e` — the gate-per-route shape, the 401/403 matrix, and the "no cross-account mutation" intent are lifted; its DTO shapes, `type:id` creator format, and 204 revoke are NOT (§D6).
**Consulted (2026-08-26):** lead-backend-engineer, lead-frontend-engineer — both ship-with-edits; edits folded in and attributed in §Consultation.

## Problem (as filed, confirmed against the code)

The bundled lifecycle routes are mounted for ANY authenticated user and are unscoped:

- `internal/inbound/authentication/routes.go:220-221` — `if svc.MachineEnabled() && svc.MachineRoutesEnabled() { mountMachine(r, h, svc.RequireUser) }`; `machine.go:117-123` registers all five routes behind `RequireUser` only (session/JWT, human) — no authorization.
- `machine.go:18-23,136` — `createServiceAccountRequest.OwnerUserID` is read from the body and passed straight to `CreateServiceAccount`: any user can create an act-as-user account owned by ANY other user, then mint its key (impersonation). The owner is never validated — `userDeactivated` treats an unknown id as active (`authsvc/useradmin.go:113-122`), so a typo'd owner yields a live ghost principal.
- `machine.go:150`, `authsvc/machine.go:68-70` — `GET /auth/service-accounts` is the global list; `ListAPIKeys` takes any account id.
- `authsvc/machine.go:76-78,105-107` — mint accepts any existing service-account id; revoke accepts any key id.
- v0.5.4 added `Config.MachineRoutesDisabled` (`authentication.go:854-862` → `authsvc.Deps.MachineRoutesDisabled` `service.go:329` → `Service.MachineRoutesEnabled()` `service.go:1452` → the `authService` interface `sessions.go:139`). A host that sets it must re-implement the five routes: gps-360-go did (`machine.go`, ~190 lines whose only addition is `RequirePermissionFixed("platform","steward","global")` on each line, plus DTOs the feature already has).

What exists to build on: `RequireUser` (`service.go:1110-1119`) admits ONLY the human credential class (cookie or bearer JWT; a non-JWT bearer is denied); `RequireLiveSession` adds the one-PK-lookup revocation check but also admits API keys (`authsvc/refresh.go:258-275`); `RequirePrincipal` (`authsvc/machine.go:235-244`) admits every class, and an act-as-user key resolves to `Principal{user, owner}` (`effectivePrincipal`, `machine.go:311-316`) — indistinguishable from its human owner downstream (`identity.Principal` has no credential-class field, `sdk/foundation/identity/identity.go:58-61`). The audit rail's single recording site `recordSecurityEvent` (`authsvc/securityevent.go:35-53`) takes an `Actor securityevent.Principal`, an optional target `UserID`, and a `Details` bag; `security_events.event_type` is free `TEXT` in both dialects (no CHECK — `stores/{pgx,turso}/migrations/0008_security_events.sql`) and `securityevent.New` does not validate the vocabulary.

## Proposal in one paragraph

Add **`Config.MachineRoutesGate web.Middleware`**: nil ⇒ the five lifecycle routes are not mounted (deny-by-absence — key AUTHENTICATION is unaffected; a WARN names the posture); set ⇒ every lifecycle route registers as `RequireUser, RequireLiveSession, gate` — human credential only, immediately revocable, then the host's policy. Remove `MachineRoutesDisabled` outright (no gate = no routes). Fix ownership in the feature: the acting human is the creator; `owner_user_id` is refused by name and `act_as_user_id` replaces it as the explicit, gate-protected, validated, **audited** delegation field; act-as-self needs no field. Behind the gate the list is the full one. Keep the five `Service` method signatures unchanged so "own the routes" hosts compile. The issue's third seam ("decorate the service") does not exist today; this train corrects the claim rather than building it. No port, DDL, or store change.

## Decisions

### D1 — `Config.MachineRoutesGate`; nil ⇒ not mounted; human live session + gate; `MachineRoutesDisabled` removed

```go
type Config struct {
    …
    // MachineRoutesGate is the authorization the bundled machine-identity lifecycle
    // routes (/auth/service-accounts*, /auth/api-keys/{id}/revoke) run behind. Each
    // route is RequireUser (human credential only — an API key, act-as-user or not,
    // never administers machine identities through the bundled routes), then
    // RequireLiveSession (immediate revocation, the invitation precedent), then this
    // gate. The feature never guesses a policy: nil ⇒ the routes are NOT mounted
    // (deny-by-absence, like PasswordFlowsDisabled) and NewService WARNs when the
    // machine repositories are wired without one; key AUTHENTICATION is unaffected.
    // Set with Repositories.ServiceAccounts / APIKeys nil ⇒ ErrMachineRoutesGateWithoutRepos.
    // A single middleware, not the []web.Middleware of cms.Config.AdminMiddleware /
    // events.Config.StreamMiddleware: nil is the unambiguous "no policy" — an empty
    // non-nil slice would mean "mounted, ungated", the very bug this field closes.
    // Typical: authorizer.RequirePermissionFixed("platform", "steward", "global").
    MachineRoutesGate web.Middleware
}
```

- **Gate stack.** `web.Handle` applies the list first = outermost (`sdk/foundation/web/handler.go:73-77`), so `mountMachine` registers each route as `svc.RequireUser, svc.RequireLiveSession, gate`: no human credential ⇒ the feature's 401 (an API-key bearer is 401 here — `RequireUser` denies non-JWT bearers, so it never reaches the live check that would admit it); a revoked session ⇒ 401 at once (`routes.go:236-245` rules invitations must ride `RequireLiveSession` for exactly this; credential issuance is not less sensitive); a live human the gate refuses ⇒ whatever the host's middleware writes (authorization's gates write FS9 `permission_denied` 403, `authorizersvc/middleware.go:114`). The bundled handlers write no 403 of their own. Both `RequireUser` and `RequireLiveSession` are already on the inbound `authService` interface (`sessions.go:87-88`); `MachineRoutesEnabled` leaves it.
- **Deviation from the issue's "after `RequirePrincipal`"** (both leads, independently): under `RequirePrincipal` a steward's act-as-user key passes the gate as the steward, passes `CurrentUser`, and can mint further keys and delegate to any user — key-mints-key with audit rows naming the human. `RequireUser` makes that structurally impossible; a host that wants machine-driven provisioning owns the routes (seam 1). The alternative — `RequirePrincipal` plus a feature-internal credential-class context marker set in `AuthenticateAPIKey` — is more machinery for a demand no host has. **Owner call (Q2).**
- **Plumbing.** Root `Service` captures `machineRoutesGate` at `NewService`. `inbound.Mount` is converted from seven positional parameters (`routes.go:67`) to `Mount(r feature.RouteRegistrar, d Deps)` — the `features/cms/cms.go:119` / `features/events/events.go:169` shape and the optional-params-are-struct-input rule; the gate rides `Deps.MachineGate`. Internal package, one call site (`authentication.go:2523`) plus the test call sites (T1). `routes.go:220` becomes `if svc.MachineEnabled() && d.MachineGate != nil`. `authsvc` is untouched by the gate — it never reaches the logic tier.
- **WARN on silent posture.** At `NewService`, machine repos wired + nil gate ⇒ `Config.Logger` WARN "auth: machine repositories are wired but Config.MachineRoutesGate is unset; the bundled lifecycle routes are NOT mounted (404) — set a gate or serve your own routes over the Service methods" (the `authentication.go:1735,1754` shape). An upgrading host learns at boot, not from production 404s.
- **Remove `MachineRoutesDisabled` now, not after a deprecation release.** Delete `Config.MachineRoutesDisabled` + its env tag (`AUTH_MACHINE_ROUTES_DISABLED` becomes an inert env var — same off-state, named in RELEASING), `authsvc.Deps.MachineRoutesDisabled`, `Service.machineRoutesEnabled` / `MachineRoutesEnabled()`, and the `authService` method (`sessions.go:139`). One day old, one adopter (gps-360-go `cmd/server/authentication.go:50`, a compile break of one line that it deletes while adopting the gate), and "off even if a gate is set" is a second way to spell nil. Same posture the owner took today for authorization's `Config.Model` at a pre-1.0 minor. **Owner call (Q1).**

### D2 — Ownership: creator = the acting human; `owner_user_id` refused by name; `act_as_user_id` explicit, validated, audited

Request DTO (`machine.go:18-23`):

```go
type createServiceAccountRequest struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    ActAsUser   bool   `json:"act_as_user"`
    // ActAsUserID names the human the account acts as. Empty with ActAsUser ⇒ the
    // caller. Non-empty ⇒ delegation, allowed only because the route sits behind
    // MachineRoutesGate; the service validates and audits it. Requires ActAsUser.
    ActAsUserID string `json:"act_as_user_id"`
    // OwnerUserID is REFUSED (400 by name) — kept one release so a client sending
    // the v0.5.x field learns the rename instead of a generic decode error. Dropped
    // at the next minor, after which strict decode answers 400 for it.
    OwnerUserID string `json:"owner_user_id"`
}
```

Handler rules (thin — decode, resolve caller, delegate). Decoding moves from `decode` (`sessions.go:528-536`) to `strictJSONBody(w, r, &req, maxJSONBodyBytes)` (`security.go:334`, `:50`) for both `createServiceAccountRequest` and `mintKeyRequest` — these are administrative routes now:

1. `owner_user_id` non-empty ⇒ **400 `bad_request`** "owner_user_id is no longer accepted; the caller is the owner, or name act_as_user_id". Empty is ignored this release (auth-cms's fixtures send `"owner_user_id":""` today, `apikey_search_test.go:47`; they drop it in T5 regardless). Strict decode's "invalid request body" is byte-identical to the malformed-JSON 400, so a rename must be distinguishable by name. (Q3.)
2. `act_as_user:false` + non-empty `act_as_user_id` ⇒ **400 `bad_request`** "act_as_user_id requires act_as_user".
3. Creator = `CurrentUser(ctx)` — always present under `RequireUser`; the machine-principal 401 branch at `machine.go:131-134` becomes unreachable and is deleted rather than turned into a 403 (a second answer to "machine principal on a human route" beside the invitation surface's 401 at `invitation.go:188-191` would be a defect).
4. `ownerUserID := ""`; if `act_as_user` then `ownerUserID = act_as_user_id` or, when empty, the caller's id. Call the UNCHANGED `CreateServiceAccount(ctx, createdBy, name, description, actAsUser, ownerUserID)`.

Service (`authsvc.CreateServiceAccount`, `authsvc/machine.go:58-64`), signature unchanged so seam-1 hosts compile (a struct-input successor is the v1.0 cut, not this train):

- When `actAsUser`: `s.users == nil` ⇒ `ErrIdentityUnavailable` (fail closed, the `currentuser.go:51-54` guard); `s.users.Get(ctx, ownerUserID)` `sdk.ErrNotFound` ⇒ `fmt.Errorf("act-as owner %q: %w", id, sdk.ErrInvalidReference)` (→ 400 "invalid reference" via `web.ErrFromDomain`, `errors.go:267-268`); other errors propagate. This closes the ghost-principal hole above for the bundled routes AND seam-1 hosts. A deactivated owner is still refused at key-authentication time (`machine.go:162-172`), unchanged.
- After `Create`: `recordSecurityEvent` with the vocabulary below. **Actor = `identity.FromContext(ctx)`** for all three events — never the `createdBy` argument, which is a caller-supplied string (gps passes `"user:<id>"`, `machine.go:107-108`; the bundled handler a bare id) and stays the `CreatedBy` column only. `UserID` = `ownerUserID` when `actAsUser` (the target subject, the `TypeUserDeactivated` convention `securityevent.go:132-138`); `Details{"service_account_id", "act_as_user": bool, "delegated": actAsUser && ownerUserID != createdBy}`. An absent principal (a seam-1 host calling from a job) records an empty Actor, which the rail already permits (`securityevent.go:209-211`).

```go
// domain/securityevent — additive vocabulary (free strings at rest; no store change)
TypeServiceAccountCreated = "service_account_created" // Actor = caller; UserID = act-as owner when set
TypeAPIKeyMinted          = "api_key_minted"          // Details: key_prefix, service_account_id — never the key or hash
TypeAPIKeyRevoked         = "api_key_revoked"         // Details: key_id (no Get-by-id port; D4)
```

Delegation itself — a steward naming another user as the owner of a live impersonation credential — is the issue's ruling (gate + audit is the control); no consent flow is added.

### D3 — List scope: behind the gate, the full list (amends the issue's checkbox)

`ListServiceAccounts` / `ListAPIKeys` are unchanged. The issue says "lists what the caller created unless the gate grants the wider view — simplest: behind the gate, the list is the full one"; this train takes the simplest reading and invents no creator-scoped second view. Cost of the alternative: `ServiceAccountRepository.List` has no filter dimension (`domain/serviceaccount/repository.go:22-33`) — a creator filter is a port change and a two-store train. A host that wants "each user sees their own" owns the routes (seam 1). The README `:726` caveat paragraph is REWRITTEN, not appended to, so an adopter reading "ownership scoped" does not believe listing changed. **Owner call (Q4).**

### D4 — Mint/revoke: the key ↔ account invariant is structural; the revoke route keeps its shape (amends the issue's checkbox)

- Mint (`POST /auth/service-accounts/{id}/keys`) already 404s for an unknown account (`authsvc/machine.go:77-78`); the key row is created under that account.
- Revoke (`POST /auth/api-keys/{id}/revoke`) carries **no service-account coordinate** and `APIKeyRepository` has no Get-by-id (`domain/apikey/repository.go:30-45`), so "verify the key belongs to the named account; cross-account = 404" is unimplementable as routed, and under one administrative gate there is no cross-account boundary to cross. The real ceiling: `RequirePermissionOn` resolves a path coordinate (`authorization/middleware.go:49`; `authorizersvc/middleware.go:42-50`) — create/list have none, mint/listKeys' `{id}` is an account, revoke's `{id}` is a key no model knows — so the single-gate seam supports only `RequirePermissionFixed` on revoke. The escape route, when a host needs per-object administration, is a nested `POST /auth/service-accounts/{id}/keys/{key}/revoke` plus `APIKeyRepository.Get(ctx, id)`: memstore + pgx + turso + `storetest` + two store retags. Not in this train. **Owner call (Q4).**
- Revoke stays 200 `{"status":"revoked"}` and idempotent (`machine.go:190-197`); gps's 204 is not lifted (§D6).

### D5 — The three seams, as they actually exist today

| seam | exists? | where |
|---|---|---|
| **Own the routes** | yes | leave `MachineRoutesGate` nil; `authentication.Service.{CreateServiceAccount,ListServiceAccounts,MintAPIKey,ListAPIKeys,RevokeAPIKey}` `authentication.go:2352-2374`, unchanged signatures (gps's `MachineService` interface keeps compiling) |
| **Own the store** | yes | `Repositories.ServiceAccounts` / `.APIKeys` ports (`domain/serviceaccount/repository.go:23`, `domain/apikey/repository.go:30`); both-or-neither ⇒ `ErrMachineReposRequired` |
| **Decorate the service** | **no** — the issue's "all existing" is wrong | `Register` hands the concrete `*authsvc.Service` to `inbound.Mount` (`authentication.go:2523`); nothing lets a host wrap one method and have the BUNDLED routes honour it |

**Deferred, not built** (both leads): no host has asked; the cited uses (naming policy, max TTL) are reachable through seam 1 or the repository ports; exporting a five-method port freezes the positional `CreateServiceAccount` signature that the v1.0 cut should restructure; and landing two new `Config` seams in one tag leaves their interaction untested. The README's seams paragraph states the truth (two seams today; decoration = own the routes). If the owner wants it in v0.6.0 the shape is: exported `MachineLifecycle` interface, `Config.MachineLifecycleDecorator func(inner MachineLifecycle) MachineLifecycle` (field ≠ type name), embed-the-inner as the documented compatibility rule, root `Service.machine` that BOTH the facade methods and the handlers route through (pinned by test), audit staying inside the inner service. **Owner call (Q5).**

### D6 — Error identity and DTO changes

- **Removed:** `Config.MachineRoutesDisabled` (+ env tag), `authsvc.Deps.MachineRoutesDisabled`, `authsvc.Service.MachineRoutesEnabled()` (never re-exported on the root `Service` — grep 2026-08-26). A source break under a minor for keyed literals that set the field: gps-360-go `cmd/server/authentication.go:50`, one line, named in RELEASING.
- **Added:** `Config.MachineRoutesGate`, `ErrMachineRoutesGateWithoutRepos`, three `securityevent.Type*` constants.
- **Request DTO:** `owner_user_id` refused by name (400); `act_as_user_id` added. **Response DTOs unchanged** — `owner_user_id` stays in `serviceAccountResponse` (the stored owner); mint keeps `key` (not gps's `plaintext`); `created_by` stays the bare user id; revoke stays 200. Explicit non-goal: gps's `{"key":{…},"plaintext":…}` / `type:id` / 204 shapes are NOT harmonised later.
- **No error-identity change** on any existing path. One new 401: a revoked human session (was: served for ≤ `AccessTokenTTL` under `RequireUser` alone). An API-key bearer was already 401 under `RequireUser` and stays so.
- `Config` gains one field and loses one: keyed literals unaffected; no unkeyed `Config` literal exists in-repo or in gps-360-go (grep 2026-08-26) — name the standard caveat in the release note.

### D7 — Tag, stores, guards

`features/authentication/v0.6.0` (minor): the intended behaviour change (a host with the machine repos wired and no gate loses the routes until it names one — that host was exposing impersonation, so the loud 404 + WARN is the point) plus the field removal. **Stores untouched — no retag:** no port method (D3/D4 deferred), no DDL, no migration; the new event types are free strings; `service_accounts` / `api_keys` columns unchanged; `go.mod` pins unchanged. **Guards:** no module or import boundary moves, so `make guard` stays at 19 with no addition; the enforcement for this change is the gate-absent-404 and gate-refusing-403 tests, and the FS9 `permission_denied` body can only be proven in `examples/auth-cms` (`guard-feature-no-cross-feature`, Makefile:210, forbids the feature importing `features/authorization`; the feature's own tests use a stub gate).

## Execution notes (2026-08-26, recorded at T6 from the two reviews — the code wins where it and the plan differ)

- **CSRF rung added (both reviewers, HIGH).** §D1's stack omitted the browser-safe-mutation gate every sibling administrative mutation carries (`mountUserAdmin`). As built, the three POSTs are `RequireUser, RequireLiveSession, browserSafe, gate`; the two GETs `RequireUser, RequireLiveSession, gate`. Cookie clients send the `__Host-auth_csrf` double-submit after `GET /auth/csrf`; bearer-only clients are unaffected (`isBearerOnly` short-circuit). Named in RELEASING.
- **`requireJSON` (415) before `strictJSONBody` on create and mint; `writeNoStore` on the mint response** — the feature's own pairing conventions, missed by §D2.
- **D2 rule 3 amended:** the `CurrentUser` `!ok` branch is KEPT as the feature's standard one-line fail-closed 401 (deleting it would let an empty creator reach the service if the mount condition ever changed); no machine-principal-specific branch or message remains.
- **Audit vocabulary:** `api_key_minted` carries `UserID` = the act-as owner (symmetry with `service_account_created`); `api_key_revoked` is emitted per CALL, not per state transition (a state-aware event needs `APIKeyRepository.Get`, the deferred D4 train) — pinned by test and documented.
- **Ghost-owner rows written under v0.5.x are NOT remediated by the upgrade** — the existence check covers new writes only; RELEASING carries the adopter audit query + revoke step.
- **T4 "run and look" premise was wrong:** auth-cms's `platform:main#admin` is seeded to the synthetic `user:demo-owner`, which cannot sign in, and AZ3-4.1 retired request-time promotion — so the 201-as-admin curl is not hand-runnable on the in-memory host. Driven by hand: 401 unauthenticated, 403 `permission_denied` as a plain user on all five, 404 + boot WARN with the gate removed, CSRF 403 without the token; the 201/400 matrix and the audit rows were driven through a temporary (reverted) grant scaffold and are pinned end-to-end by `TestAPIKeySearch*` / `TestMachineRoutesGateRefusesANonAdmin`. Open owner call: an admin-provisioning seam for auth-cms.
- Executed plans are copied to the tracked root `plans/` (`.claude/` is gitignored); RELEASING cites `plans/authentication-machine-routes-gate.md`.

## Compatibility

- **Hosts with machine repos wired and no gate** (examples/auth-cms today; any v0.5.x adopter on the default): the five routes answer 404 until `MachineRoutesGate` is set; a WARN at boot says so. Key authentication, `RequirePrincipal`, `RequireServiceAccount`, `RequireLiveSession` unchanged.
- **Hosts on `MachineRoutesDisabled: true`** (gps-360-go): compile error on the field; delete the line — nil gate is the same posture. `AUTH_MACHINE_ROUTES_DISABLED` in a deployment is ignored. Their hand-written routes keep working over the unchanged facade methods until they adopt the bundled ones.
- **Clients of the bundled routes:** `owner_user_id` in a POST body is 400 by name; use `act_as_user_id` (gate required). A revoked session is 401 immediately. Everything else on the wire is byte-stable.
- **Seam-1 hosts calling `CreateServiceAccount` directly:** an act-as owner that does not exist is now `sdk.ErrInvalidReference` (was: accepted); three audit rows appear when `SecurityEvents` is wired.
- **Audit rail consumers:** three new `event_type` values; readers that filter by type are unaffected.
- **Stores, `storetest`, migrations:** untouched.

## Tasks

Executor model per `[[subagent-model-policy]]`: implementation → **Opus**. Each task ends green on `go build ./... && go test ./... && go vet ./...` in `features/authentication`; T1 onward also `make guard` at the root (no count change expected); T6 `make check`.

- **T1 — Gate wiring, `Mount` struct input, `MachineRoutesDisabled` removal** (`features/authentication/authentication.go`: Config field + doc, `ErrMachineRoutesGateWithoutRepos` in the construction-error var block beside `ErrHTMLPolicyWithoutViews` `:743-749` and its check beside `:1491`, the nil-gate WARN, `Register`; `internal/logic/authsvc/service.go:319-330,455-460,605-612,1448-1452` deletions; `internal/inbound/authentication/routes.go` (`Deps` struct, `Mount(r, d Deps)`, the `:214-222` block), `machine.go:114-123` (`mountMachine(r, h, requireUser, requireLiveSession, gate)`), `sessions.go:136-139`; every `Mount(...)` call site in `internal/inbound/authentication/*_test.go` (`helpers_test.go:567`, `machine_test.go:144`, `invitation_test.go:153,203`, `stepup_test.go:77`, `html_test.go:102`, `oauth_test.go:123`, `methods_test.go:128,315`, `reset_token_retain_test.go:65`, `password_flows_test.go:56`); `password_flows_test.go:29-58` `newPostureHandler` takes a gate instead of the bool and `:99-125` becomes `TestMachineRoutesGateAbsent_RoutesAbsent`; root `auth_test.go:461-497` (the Register-surface machine tests: no-gate now 404, add a gate-set leg). Tests: gate nil ⇒ 404 on all five with repos wired (`MachineEnabled()` true; a bearer key still authenticates on `/auth/me`-class routes); gate set + no credential ⇒ 401; **an act-as-user API-key bearer ⇒ 401 on all five** (pins the human-only rule); a logged-out (revoked) session ⇒ 401; stub gate refusing ⇒ 403 on all five; stub gate allowing ⇒ existing happy paths pass; `ErrMachineRoutesGateWithoutRepos`; the WARN line (a capturing logger, the `:1735` test pattern).
- **T2 — Ownership scoping in the handler** (`internal/inbound/authentication/machine.go`, `machine_test.go:162-300`): DTO change, `strictJSONBody`, rules 1-4 of D2. Tests: `owner_user_id` non-empty ⇒ 400 with the named message; empty ⇒ ignored; `act_as_user_id` without `act_as_user` ⇒ 400; act-as-self ⇒ response `owner_user_id` equals the caller; delegated ⇒ equals the named user; unknown `act_as_user_id` ⇒ 400 `invalid reference`; oversized body ⇒ 413.
- **T3 — Service-side validation + audit** (`domain/securityevent/securityevent.go` constants; `internal/logic/authsvc/machine.go`; `authsvc/machine_test.go`): the owner existence check (nil users ⇒ `ErrIdentityUnavailable`; unknown ⇒ `sdk.ErrInvalidReference`; repo error propagates), the three events through `recordSecurityEvent` with Actor from `identity.FromContext`, Details hygiene (assert the raw key and hash never appear in any `Details` value — the `securityevent_test.go` pattern). Tests: created/minted/revoked rows with the pinned Actor/UserID/Details; `delegated` true/false; absent principal ⇒ empty Actor, row still written; nil audit repo ⇒ no-op; audit write failure never fails the op.
- **T4 — Proof host: examples/auth-cms** (`cmd/server/main.go`: set `authCfg.MachineRoutesGate = authorizer.RequirePermissionFixed("platform", "admin", "main")` after `buildAuthConfig` returns — the `DeliveryMode` post-set precedent at `:260`; `platform/admin` is already declared in `authzSchema()` `authorization.go:41-53`, so `mustDeclare` is satisfied; `cmd/server/apikey_search_test.go:42-47,137,186`: the fixture builds the memstore authorizer (`authorization.go:106` pattern) and grants `platform:main#admin` to the signed-up user through the trusted `SystemMutator` (`trustGrant`, `az3_proof_protocol_test.go:191`), passes the gate through `bootInProcess`'s `tune` hook, drops `"owner_user_id":""` from the bodies, and adds: a plain signed-up user ⇒ 403 with FS9 code `permission_denied` on `GET /auth/service-accounts` (the only place the real body can be proven); `examples/auth-cms/README.md:395-402,763` curl walkthrough updated with the tuple step). **Run and look:** `cd examples/auth-cms && AUTH_DEBUG=1 go run ./cmd/server` (README `:344`; port per README), sign in as the seeded owner, `POST /auth/service-accounts` `{"name":"ci","act_as_user":true}` ⇒ 201 with `owner_user_id` = your id; the same body plus `"owner_user_id":"x"` ⇒ 400 naming the field; a second, non-admin account ⇒ 403 on every lifecycle route; with the gate line commented out ⇒ 404 on all five and the WARN at boot; the security-events store shows `service_account_created`. Verify: `go build ./... && go test ./... && go vet ./...` in `examples/auth-cms`.
- **T5 — Docs** (`features/authentication/README.md`: route surface block `:249-255` ("all session-gated" → "mounted only with `Config.MachineRoutesGate`; each route `RequireUser` + `RequireLiveSession` + gate — human, live, authorized"; request/response fields incl. `act_as_user_id` and the refused `owner_user_id`; the 400/401/403 rules); the Config table row `:726` REWRITTEN as the `MachineRoutesGate` row (the v0.5.4 caveat text goes — listing is global by ruling, say so); middleware section `:598-612` one line on where the gate sits; audit-rail section `:1688` lists the three event types; a short "Machine identity — ownership, delegation, and the two seams" subsection that also corrects the issue's third-seam claim; `authentication.go` package/Config docs; `domain/serviceaccount/serviceaccount.go:19-22` doc unchanged (still true). `RELEASING.md`: a NEW top-list entry and a NEW "### features/authentication — v0.6.0 (next tag): the lifecycle routes come standard behind a host gate (minor; MachineRoutesDisabled REMOVED — a source break; ungated routes no longer mount)" section naming the removed field and its one known caller, the inert env var, the gate stack, the DTO change, the new events, the no-store-retag reason, and the adoption notes below; the v0.5.4 entry at `:608-631` is ledger history and is not edited. `ARCHITECTURE.md:152`'s middleware-seam list gains `authentication.Config.MachineRoutesGate` beside `cms.Config.AdminMiddleware`, with the singular-vs-slice note.
- **T6 — Verify + tag + cold-resolve** (`make check` at the root — 19 guards + every module; `features/authentication/stores/{pgx,turso}` hermetic tests run though untouched, pins unchanged; `features/authentication/views/goth` builds against the workspace; `examples/auth-cms` green). Tag `features/authentication/v0.6.0`; cold-resolution from a throwaway module (`GOFLAGS=-mod=mod go list -m github.com/gopernicus/gopernicus/features/authentication@v0.6.0`, then `go build` of a file that sets `Config.MachineRoutesGate`); close #6 with the tag and a comment amending its scope checkboxes per D3/D4/D5.

## Adoption (downstream, after the tag)

- **gps-360-go** (handoff prompt, the coordination-hub precedent): bump `features/authentication` to v0.6.0. gps builds authentication at `cmd/server/main.go:164-168` BEFORE authorization at `:181` — move the authorization boot above the authentication config (or set `authenticationCfg.MachineRoutesGate` once the authorizer exists, before `NewService`); in `cmd/server/authentication.go:50` replace `MachineRoutesDisabled: true` with `MachineRoutesGate: authorizationComponents.Service.RequirePermissionFixed(string(auth.ResourcePlatform), string(auth.PermissionSteward), auth.PlatformGlobal)`; delete `features/auth/inbound/machine.go` + `machine_test.go`, drop `machine` from `inbound.Auth` and the `MachineService` parameter of `New` (`http.go:22-36`); update `README.md:95` and `cmd/server/server_test.go:146-154`. **Client-visible divergence, all three at once:** path (`/api/v1/service-accounts*` → `/auth/service-accounts*` — the feature is mounted at the root, `main.go:172`), mint body (`{"key":{…},"plaintext":…}` → the flat `{…,"key":…}`), revoke status (204 → 200 `{"status":"revoked"}`), and `created_by` (`"user:<id>"` → bare id — existing rows keep the old format; one column, two formats, no backfill). Any Segovia-side key-holding client repoints or gps keeps a thin adapter. gps's `act_as_user ⇒ 400` refusal becomes a gate-protected capability; if stewards must not delegate, gps keeps its own create route only (seam 1) or waits for Q5.
- **coordination-hub**: not reachable from this machine (no `/Users/jrazmi/code/coordination-hub`); by its known wiring it is relationship-only and, if it wires the machine repos without a gate, sees the WARN and 404s until it names one — the RELEASING note says so.

## Non-goals

- A creator-scoped (per-user) view of accounts and keys through the bundled routes (D3) — seam 1, or a `List` filter port on demand.
- `APIKeyRepository.Get` / a nested account-scoped revoke route (D4) — a store train, when a host needs per-object gating.
- The decorate-the-service seam (D5) — deferred; the README says so.
- A credential-class marker on the principal (so `RequirePrincipal` could distinguish an act-as-user key from its owner) — the gate stack makes it unnecessary here; trigger is a host that needs machine-driven provisioning through the bundled routes.
- A `CreatedByType` column so a machine principal can be a creator — schema change; `RequireUser` makes the case unreachable.
- Harmonising gps's DTO/status shapes (`plaintext`, `type:id`, 204) into the feature.
- Delegation consent; machine-identity HTML pages; rate limiting on mint (the gate holder is an administrator).

## Consultation (2026-08-26) — what changed from the first draft

- **Both leads, independently (blocking):** the draft's `RequirePrincipal, gate` stack let a steward's act-as-user key pass the gate as the steward and dodge the "machine principal ⇒ 403" check (`effectivePrincipal` `machine.go:311-316`, `CurrentUser` `service.go:1124-1130`) — key-mints-key with delegation. Adopted: `RequireUser` (human class only; frontend) + `RequireLiveSession` (immediate revocation, the `routes.go:236-245` invitation precedent; backend) + gate; D2 rule 3 deleted as unreachable; a pinning test for the act-as-user-key 401.
- **lead-backend-engineer:** `Mount` → struct input (`cms.go:119` / `events.go:169`); audit Actor from the context, never `createdBy` (gps's `"user:<id>"` mixed-format note); `s.users == nil` ⇒ `ErrIdentityUnavailable`; `strictJSONBody` for the admin DTOs; nil-gate WARN at `NewService`; D3/D4 labelled as amendments to ratified checkboxes with the port cost and the `RequirePermissionOn` coordinate ceiling; D5 deferred (unearned; field/type name collision; freezes the positional signature); blast-radius list (`auth_test.go:461-497`, `password_flows_test.go`, `machine_test.go`, auth-cms fixtures + README); RELEASING gets a NEW entry, v0.5.4 untouched; guards line. Not adopted: a credential-class context marker (superseded by the gate stack; recorded as a non-goal with its trigger); service-layer machine-principal refusal (unreachable now).
- **lead-frontend-engineer:** keep `owner_user_id` one release with a named 400 (strict decode's message is indistinguishable from malformed JSON); the FS9 `permission_denied` proof lives in auth-cms (`guard-feature-no-cross-feature`); singular `web.Middleware` kept but the deviation from the slice precedent documented in the field doc; the auth-cms tuple-seeding seam named (`trustGrant`); run-and-look corrected (`make run` is `examples/cms`); gps DTO/status shapes recorded as an explicit non-goal; D5 split out. Not adopted: a domain sentinel for the machine-principal case (moot).

## Open questions for the owner — RULED 2026-08-26 (all recommendations accepted; see Status)

1. **D1 — remove `MachineRoutesDisabled` now (recommended)** or keep it one release as "off even if a gate is set"? One-day-old field, one adopter that deletes the line anyway; the owner removed authorization's `Config.Model` outright today under the same pre-1.0 rule.
2. **D1 — gate stack `RequireUser, RequireLiveSession, gate` (recommended)**, or `RequireUser, gate` alone (today's stale-JWT window ≤ `AccessTokenTTL`, no live check), or the issue's literal `RequirePrincipal, gate` plus a credential-class marker so API keys can administer under the host's policy? The recommendation deviates from the issue text on the leads' shared finding; it keeps key-mints-key structurally impossible and makes credential issuance immediately revocable.
3. **D2 — `owner_user_id` refused by name for one release then dropped (recommended)** or dropped now with strict decode's generic 400?
4. **D3/D4 — accept both amendments to the issue's checkboxes (recommended):** global list behind the gate; revoke route unchanged with the nested account-scoped revoke + `APIKeyRepository.Get` deferred. The alternative is a two-store train now.
5. **D5 — defer the decorate-the-service seam and correct the issue's claim (recommended)** or build `Config.MachineLifecycleDecorator` in v0.6.0?
6. **Tag — v0.6.0 minor (recommended)** or patch by owner ruling as v0.5.3/v0.5.4 were? The routes-no-longer-mount change and the field removal argue minor.
