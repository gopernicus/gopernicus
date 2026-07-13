# Phase 8 — HTML/templ adapters and auth-cms proof host

Status: READY after phases 6 and 7.
Depends on: phases 0–7.
Design: §§6.2–6.4, 8, 9.1–9.2, 11, R12/V16.

## Goal

Ship both stable JSON API handlers and a complete optional HTML surface over the
same authentication services, bundle secure default templ views in a sibling
module, prove partial host overrides, and exercise every v3 security contract in
`examples/auth-cms`.

## Task AV3-8.1 — authentication Views port and exported view models

Touch: authentication feature core/public socket, inbound view interface/models,
config/registration tests.

Follow the CMS FS3 pattern exactly:

- define a technology-neutral `Views` interface returning `web.Renderer`;
- publicly alias/export the interface and all view models without exposing
  `internal/` packages;
- models cover login, registration, verification, forgot/reset, passwordless
  start/code/magic-link landing/check-delivery, step-up, account-security method
  inventory, identifier add/edit, password set/change/remove, OAuth unlink,
  status, and error pages;
- shared page fields include CSRF token, CSP nonce, validated return-to, generic
  message, masked actor/identifier, and accessible field errors;
- never put password, OTP, token, pepper, unmasked recovery address, or raw
  provider token in a reusable view model;
- `Config.Views == nil` means HTML GET routes/form decoding are absent while JSON
  API routes remain registered;
- no templ import in the authentication core.

Add stub-Views tests proving the full HTML route inventory mounts only when
Views is non-nil. Do not implement concrete templates yet.

Verify:

```sh
cd features/authentication && go test ./... -run 'Views|HTMLMount|APIMount'
make guard
```

## Task AV3-8.2 — bundled `features/authentication/views/templ` module

Depends on: AV3-8.1.
Touch: new sibling module, `go.work`, root Makefile module/generate inventories,
release module inventory, templ sources/tests.

Create the default implementation:

- module path `github.com/gopernicus/gopernicus/features/authentication/views/templ`;
- imports authentication feature + sdk + templ only; feature core never imports
  back;
- `Views` has a usable zero value, `New()`, compile-time assertion against
  `authentication.Views`, and one method per port method;
- semantic, deliberately plain templates with labels, field associations,
  validation summaries, focus target, correct autocomplete (`email`,
  `current-password`, `new-password`, `one-time-code`, `tel`), masked methods,
  and no third-party assets;
- CSRF hidden fields on every mutation form and validated return-to hidden field
  where applicable;
- magic landing uses a CSP-nonced script to read fragment, immediately scrub
  history, and POST; include a visible/manual fallback without placing the token
  in a query string;
- status/error copy preserves enumeration resistance and never distinguishes
  unknown/unverified accounts;
- forms never repopulate password/code/token fields after failure;
- generated `*_templ.go` comes only from `make generate`.

Add renderer snapshot/semantic tests for headers/labels/autocomplete/CSRF,
secret non-echo, generic copy, and each page rendering with zero/minimal models.
Prove `make generate` is drift-free and the module is included in `make check`.

Verify:

```sh
make generate
cd features/authentication/views/templ && go build ./... && go test ./... && go vet ./...
make check
make guard
```

## Task AV3-8.3 — dual JSON/form dispatch and public HTML handlers

Depends on: AV3-8.1, AV3-0.5, phases 3–7.
Touch: authentication inbound route table/handlers/tests.

Implement without duplicate POST route registration:

- one dispatcher on canonical POST endpoints selects JSON strictly from
  `Content-Type: application/json` and form behavior strictly from
  urlencoded/multipart content type when Views is wired;
- unsupported content type → 415; `Accept` never changes request decoding;
- JSON DTO/status/body/cookie contracts remain byte/semantically compatible;
- form handlers parse bounded bodies, use CSRF/origin protections, call the same
  service methods as JSON, render safe validation errors, and use 303 PRG on
  success;
- add HTML GET pages for login, register, verify, forgot/reset, passwordless
  start/code, magic landing, and check-delivery;
- authenticated success/return-to destinations go through the existing exact
  redirect allowlist; no Host-derived URL;
- auth-establishment endpoints apply the phase-7 origin policy without requiring
  a pre-existing authenticated CSRF session, while authenticated mutations use
  the full CSRF token contract;
- HTML responses set no-store, no-referrer, CSP, frame/content-type protections,
  and correct status.

Table tests run every shared POST once as JSON and once as form and assert the
same service call/normalized input/security result, with transport-appropriate
response. With nil Views, GET returns 404 and form POST 415 while JSON still
works.

Verify:

```sh
cd features/authentication && go test ./internal/inbound/authentication/... -run 'JSON|HTML|Form|ContentType|PRG'
make check
```

## Task AV3-8.4 — account-security HTML handlers

Depends on: AV3-8.3, phase 6.

Add live-session-gated HTML GET/forms for:

- masked account-security/method inventory;
- recent-auth start/completion;
- add/change/remove identifiers and uses/primary selection;
- set/change/remove password;
- provider-bound OAuth unlink confirmation; and
- delivery-job status.

All POSTs use the already-shipped JSON endpoints' service methods and policy;
the HTML layer does not recalculate method safety, assurance, rate limits, or
context binding. Stale/revoked sessions redirect to login only after validating
a safe relative return-to. Policy conflicts render generic actionable copy
without exposing unmasked contact values.

Tests cover CSRF, stale session, recent-auth requirement, wrong provider,
identifier collision, safe PRG, masked output, and error renderer status.

## Task AV3-8.5 — partial override and presentation-isolation proof

Depends on: AV3-8.2 through AV3-8.4.

Create a test/example host type that embeds bundled `templ.Views` and overrides
only `Login` and `AccountSecurity`. Prove promoted defaults satisfy every other
method and both overrides render. Also prove a small full-port test
implementation can use one reusable `html/template`/`web.Template` renderer for
all methods without importing templ.

Run guards/greps proving:

- authentication core has no `a-h/templ` import;
- handlers render exclusively through `Views`, never concrete components;
- overriding presentation cannot bypass middleware, decoder, service, redirect,
  or status policy; and
- email TemplateRegistry and page Views are distinct facilities.

## Task AV3-8.6 — auth-cms composition root and development secrets

Depends on: AV3-8.2, AV3-8.5.
Touch: auth-cms config/env, server composition, authmem/store wiring,
startup/shutdown tests.

Wire:

- `authtempl.New()` into `Config.Views`;
- explicit `RuntimeMode=development` default for the example only;
- `AUTH_CHALLENGE_PEPPER` key ring with active key ID, separate
  `AUTH_IDENTIFIER_KEY`, delivery AES-GCM key, JWT key, and existing token
  encryption key—all documented as distinct;
- safe development ephemeral generation only where permitted, never printing
  key material;
- console email/phone metadata, delivery worker lifecycle, repositories,
  redress URL, and public auth base URL.

Unit tests start/stop without goroutine leaks and prove production with console
wiring fails construction.

## Task AV3-8.7 — magic-link landing integration

Depends on: AV3-8.3, AV3-8.6.

Exercise the bundled magic-link page in the host: fragment read, immediate
history scrub, POST redemption, manual fallback, no token in GET logs/referrer/
analytics, generic consumed/expired state, and session cookie establishment.
Use browser-level or equivalent DOM-capable verification if available; static
HTML inspection alone does not close this task.

## Task AV3-8.8 — account-security demonstration surface

Depends on: AV3-8.4, AV3-8.6.

Wire the bundled default pages and minimal navigation needed to demonstrate
masked methods, recent-auth, identifier lifecycle, password lifecycle, OAuth
unlink where a test provider exists, delivery status, independent notice, and
policy-refused removal. Do not add host-specific duplicate handlers.

## Task AV3-8.9 — host override and production safeguards

Depends on: AV3-8.5, AV3-8.6.

Replace the phase-8.5 test override with one real auth-cms `LayerApp`-style page
override by embedding `authtempl.Views`; also keep one email `LayerApp` template
override to demonstrate that the two override systems are distinct. Observe one
development console warning. Production negative tests cover console, insecure
URL/cookies, memory limiter, audit, metadata, worker, and any unsafe Views/public
URL combination.

## Task AV3-8.10 — complete JSON + HTML run-and-look protocol

Depends on: AV3-8.6 through AV3-8.9.

Run every core journey twice where applicable: once through JSON/curl and once
through normal HTML pages/forms.

1. register → async verify → verify → password login;
2. reset → prior sessions rejected → ordinary login;
3. identifier add/change/remove/shared-notification phone;
4. recent-auth sensitive mutations;
5. set/remove password and provider unlink/wrong-provider consumption;
6. email magic link landing, phone OTP/link, refresh/logout;
7. known/unknown start timing under blocked provider;
8. worker retry/replacement/terminal cleanup/purge;
9. pepper key rotation overlap;
10. CSRF/origin/body/XFF/Host/production negative cases;
11. default view accessibility/security headers and secret non-echo; and
12. partial page override + separate email template override.

Assert HTML POSTs return 303 and JSON POSTs retain their JSON status/body. README
transcripts use placeholders, never live credentials.

## Phase acceptance

```sh
make generate
cd features/authentication/views/templ && go build ./... && go test ./... && go vet ./...
cd examples/auth-cms && go build ./... && go test ./... && go vet ./...
make check
make guard
```

All run-and-look legs observed in both transports. API-only construction remains
green with no templ module in the host graph; default templ and partial override
hosts both work.

## Stop conditions

- Supporting HTML would require changing an existing JSON contract: stop and
  fix the dispatcher boundary.
- Two handlers need the same method/path registration: use one content-type
  dispatcher; never depend on router ordering.
- A template needs policy/repository access: move that behavior back to service
  or handler view-model construction.
- A secret appears in an HTML value, URL, rendered error, GET log, or referrer:
  stop and fix before proof work.
- Default templ convenience would add templ to the feature core: keep it in the
  sibling module.

## Execution log

Append dated entries per completed task.

### 2026-07-13 — AV3-8.1 (authentication Views port and exported view models)

Scope reconciliation (logged premise adaptation): the task's "full HTML route
inventory mounts only when Views is non-nil" test requires the HTML GET routes to
exist, while 8.3 (public HTML handlers/dispatch) and 8.4 (account-security handlers)
own the rich handler behavior. Resolution: 8.1 defines the port + models + the
`Config.Views` gate and registers the full HTML GET route inventory once (in
`mountHTML`), with deliberately thin handlers that render through the port; 8.3/8.4
fill these SAME handlers in (POST content-type dispatch, PRG, live-session reads,
full header policy) without re-registering any route — so no duplicate POST
registration. Concrete templates deferred to 8.2 (no templ in the core).

Also logged: with a nil Views, a GET to a path whose JSON POST twin is still
registered returns 405 (method-not-allowed), not 404 — both prove the HTML GET
handler is unregistered and neither renders a page. The nil-Views test asserts
404-or-405.

Files changed:
- `features/authentication/internal/inbound/authentication/views.go` (new) — the
  technology-neutral `Views` port (17 page methods returning `web.Renderer`) and all
  exported view models (`PageContext`, `FieldError`, per-page models, masked
  `OAuthMethod`/`IdentifierMethod` projections). Secret discipline documented and
  enforced by construction: no password/OTP/token/pepper/unmasked-address/raw-provider-token
  field. No templ import.
- `features/authentication/internal/inbound/authentication/html.go` (new) —
  `mountHTML` registering the full HTML GET inventory gated on the Views port
  (passwordless/OAuth pages deny-by-absence; account-security pages RequireLiveSession),
  plus thin per-page GET handlers, `renderPage`/`newPageContext`/`newNonce` helpers.
- `features/authentication/internal/inbound/authentication/routes.go` — `Mount` gains
  a trailing `views Views` param; `handlers` gains a `views` field; `mountHTML` called
  only when `views != nil`.
- `features/authentication/views.go` (new) — public type aliases re-exporting the
  `Views` port and every view model (CMS A1 precedent), no `internal/` exposure.
- `features/authentication/authentication.go` — `Config.Views Views` field (nil →
  API-only; documented R12/V16 semantics); `Service.views` field; threaded through
  `NewService` and `Register` → `inbound.Mount`.
- `features/authentication/internal/inbound/authentication/html_test.go` (new) —
  `stubViews` full port implementation + compile assertion; HTMLMount tests (public
  pages render the port marker at 200; account pages mount behind the live-session
  gate, never 404); APIMount tests (nil Views → every HTML GET 404/405; JSON POST
  routes survive).
- 11 existing inbound `*_test.go` `Mount(...)` call sites updated to pass the new
  trailing `nil` views argument.

Verification (exact commands, observed):
- `cd features/authentication && go test ./... -run 'Views|HTMLMount|APIMount'` — PASS
  (inbound/authentication ok; rest no-tests-to-run).
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` — PASS
  (no failures).
- `make guard` — PASS (all 13 guards green, exit 0; feature core has no `a-h/templ`
  import).
- `cd examples/auth-cms && go build ./...` — PASS (host still compiles over the
  additive `Config.Views` field).

### 2026-07-13 — AV3-8.2 (bundled `features/authentication/views/templ` module)

New sibling module implementing the default HTML surface, mirroring the
`features/cms/views/templ` conventions exactly (module path, go.mod shape with the
`tool` templ directive + dev `replace`s, `Views` struct with a usable zero value,
`New()`, and the `var _ authentication.Views = Views{}` compile assertion). The
feature core still never imports templ (guard G2 green); concrete templ lives only
here.

Design/premise adaptations (logged):
- The `Views` port has 16 methods (Login, Register, Verify, ForgotPassword,
  ResetPassword, PasswordlessStart, PasswordlessCode, MagicLinkLanding,
  CheckDelivery, StepUp, AccountSecurity, IdentifierForm, PasswordForm,
  OAuthUnlink, Status, Error). The AV3-8.1 handoff said "17 methods" but listed 16;
  the interface in `internal/inbound/authentication/views.go` defines 16. Every one
  is implemented; the compile assertion is the authority.
- templ generates an implicit `ctx context.Context` per component, so the shared
  partials that take a page context were named `pc authentication.PageContext` to
  avoid the collision (a pure naming choice, no behavior change).
- Form CSRF hidden field is `csrf_token`; the validated return-to hidden field is
  `return_to`. AV3-8.3 (form decode/dispatch) owns the parsing side and must read
  these names (the existing OAuth/invitation JSON edges use a `redirect` query
  param — 8.3 reconciles the form field name there if it wants a single vocabulary).
- Form actions target the existing canonical POST paths from `routes.go`/`oauth.go`/
  `passwordless.go` (login `/auth/login`, passwordless start `/auth/passwordless/start`,
  verify `/auth/passwordless/verify`, magic redeem `/auth/passwordless/redeem`,
  step-up `/auth/step-up/{begin,password,code}`, password `/auth/password/{set,change,
  remove/start,remove}`, identifier add `/auth/identifiers/{email,phone}`, edit
  `/auth/identifiers/{id}`, OAuth unlink `/auth/oauth/{provider}/unlink{,/start}`).
  HTML forms can't emit PATCH/DELETE; the identifier edit form posts the `{id}` path
  and AV3-8.4's dispatcher routes it to the same service method.

Secret discipline (verified by tests): no model or template renders a password,
one-time code, magic-link/reset/verification token, HMAC pepper, unmasked address,
or raw provider token. The reset and magic-link tokens are read from the URL
fragment by a CSP-nonced inline script that immediately `history.replaceState`-scrubs
the fragment before any submit; a visible manual `Continue`/submit fallback posts the
stashed hidden field without ever placing the token in a query string. With no CSP
nonce the pages render no inline script at all (fail-safe). Password/code inputs
carry no `value` attribute, so a failed attempt never repopulates a secret.

Autocomplete contract honored: `email`, `current-password`, `new-password`,
`one-time-code` (with `inputmode="numeric"`), and `tel` (phone identifiers).
Every mutation form carries the `csrf_token` hidden field and, where applicable, the
`return_to` hidden field; every input has an associated `<label for>` and an
`aria-describedby` error target, with an accessible `role="alert"` validation
summary. No third-party assets are loaded.

Files changed (module layout `features/authentication/views/templ/`):
- `go.mod`/`go.sum` (new) — module `.../features/authentication/views/templ`,
  requires authentication + sdk + templ tool, dev `replace`s to `../..` and
  `../../../../sdk`.
- `views.go` (new) — concrete `Views` struct, `New()`, `var _ authentication.Views
  = Views{}`, one delegating method per port method.
- `helpers.go` (new) — non-templ helpers (identifier input type/label/autocomplete
  by kind, form-action builders, uses/status text).
- `layout.templ` (+ generated `layout_templ.go`) — shared plain chrome and the
  shared partials `message`/`errorSummary`/`fieldError`/`csrfField`/`returnToField`.
- `credential.templ`, `recovery.templ`, `passwordless.templ`, `stepup.templ`,
  `account.templ`, `identifier.templ`, `password.templ`, `oauth.templ`,
  `status.templ` (+ their generated `*_templ.go`) — the per-page templates.
- `views_test.go` (new) — renderer semantic tests: every page renders from a
  zero/minimal model; labels/autocomplete/CSRF/return-to assertions; secret
  non-echo; reset/magic fragment-token-never-rendered + nonce-scrub + manual
  fallback + no `?token=`; step-up viable-method gating; masked inventory; generic
  error/status copy; and the blessed partial-override embed proof.
- `go.work` — added `./features/authentication/views/templ` to the `use` block.
- `Makefile` — added the module to `MODULES` and a second `go tool templ generate`
  line to the `generate` target (per-module tool pinning).
- `RELEASING.md` — module inventory bumped to thirty-seven; noted the new bundled
  views module.

Verification (exact commands, observed):
- `make generate` — PASS; re-run is a no-op (updates=0). `git diff --exit-code --
  '*_templ.go'` clean; regeneration deterministic.
- `cd features/authentication/views/templ && go build ./... && go test ./... && go
  vet ./...` — PASS (all renderer tests green).
- `make check` — PASS ("all checks passed"); the new module appears in the per-module
  loop (`== features/authentication/views/templ == ok`) and the templ-drift gate is
  clean.
- `make guard` — PASS (all 13 guards green; feature core still has no `a-h/templ`
  import — G2 green).

### 2026-07-13 — AV3-8.3 (dual JSON/form dispatch and public HTML handlers)

Single content-type dispatcher fronts every shared public POST endpoint (register,
login, verify, password/forgot, password/reset, passwordless start/verify/redeem).
Each endpoint keeps its ONE route registration (routes.go / passwordless.go
unchanged in path/middleware); the handler behind it now selects the JSON or form
arm by `Content-Type`. No duplicate POST registration (the phase-8 stop condition).
The JSON DTO/status/body/cookie contracts are unchanged — the JSON arm is the
verbatim prior handler body, only renamed `*JSON`. Account-security form handlers
(password change/set/remove, identifiers, step-up, OAuth unlink) remain AV3-8.4.

Premise adaptations (logged):
- **Absent Content-Type decodes as JSON (not 415).** §9.2 reads "selects JSON
  strictly from application/json … unsupported content type → 415," which taken
  literally would 415 a pre-v3 JSON client that posted a body with no Content-Type
  header (all existing inbound tests, and any such live client). The standing
  invariant "non-nil Views changes no JSON contract" (overview) plus the phase stop
  condition "supporting HTML would require changing an existing JSON contract: stop
  and fix the dispatcher boundary" override the literal reading. Resolution
  (`classifyContent`, dispatch.go): a form media type (`x-www-form-urlencoded` /
  `multipart/form-data`) routes to the form arm (only when Views is wired, else
  415); `application/json` OR an ABSENT Content-Type routes to the JSON arm; any
  OTHER explicit media type is 415. So form dispatch is strict per §9.2 and a
  genuinely unsupported explicit type is 415, while the lenient JSON client is
  preserved. `TestJSONContractUnchangedByViews` pins this (register with explicit
  `application/json`, login with no header — both succeed identically with/without
  Views). The handoff's "JSON handlers get strict content-type" is honored on the
  form-discrimination axis; it is deliberately NOT extended to reject header-less
  JSON, which would break the JSON contract.
- **Form failures re-render at the mapped domain status (CMS FS3 precedent), 303
  reserved for success.** A form validation/credential failure re-renders the same
  page through the port with GENERIC, enumeration-resistant copy (never the raw
  error) at `web.ErrFromDomain(err).Status` — rate-limit exhaustion mapped to 429.
  Password/code/token fields are never repopulated (the models carry no such field;
  reset/magic tokens stay fragment-only). Success uses 303 PRG through the existing
  exact redirect allowlist (`Service.ResolveRedirect`, new — delegates to the same
  `redirect.Allowlist` OAuth uses); a non-allowlisted `return_to` falls back to the
  same-origin `/`.
- **Public establishment endpoints apply the origin policy, not the CSRF-cookie
  gate.** register/login/verify/forgot/reset form handlers call `formOriginOK`
  (`browserOriginAllowed`) so a cross-site browser POST is blocked (login CSRF)
  while a native/no-Origin caller passes; there is no pre-existing session, so no
  double-submit cookie is required (design §9.1). The passwordless routes already
  carry `requireBrowserSafeOrigin` as route middleware (both transports), so their
  form arms skip the per-handler origin check. JSON arms are untouched by the origin
  check (added inside the form handler only), preserving native JSON clients.
- **Full HTML security-header policy centralized** in `writeHTMLSecurity` (no-store,
  no-referrer, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and a
  restrictive CSP `default-src 'none'; base-uri 'none'; form-action 'self';
  frame-ancestors 'none'; script-src 'nonce-…'|'none'`). `renderPage` now takes the
  per-render nonce; the bundled pages load no third-party/inline-style asset and the
  only inline scripts are the nonced reset/magic fragment readers, so the CSP fits
  them exactly (empty nonce → `script-src 'none'`, the fail-safe no-script path).
  Redundant per-handler `writeNoReferrer` calls in resetPage/magicLinkPage removed
  (subsumed).

Files changed:
- `internal/logic/authsvc/oauth.go` — added exported `Service.ResolveRedirect(target)`
  delegating to the existing `redirect.Allowlist` (shared open-redirect guard for the
  HTML return-to; no new allowlist).
- `internal/inbound/authentication/dispatch.go` (new) — `contentKind`/`classifyContent`,
  the `dispatch` seam + `unsupportedMediaType` (415), and the eight canonical POST
  dispatch entrypoints (`register`, `login`, `verify`, `forgotPassword`,
  `resetPassword`, `passwordlessStart`, `passwordlessVerify`, `passwordlessRedeem`)
  that routes.go / passwordless.go already reference by name.
- `internal/inbound/authentication/forms.go` (new) — public HTML form handlers +
  helpers (`validatedReturnTo`/`safeReturnTo`, `formOriginOK`, bounded `parseForm`,
  `prgRedirect`/`prgTo` 303, `formFailure` status/copy mapping, `renderForm`).
- `internal/inbound/authentication/sessions.go` — renamed the JSON bodies to
  `registerJSON`/`loginJSON`/`verifyJSON`/`forgotPasswordJSON`/`resetPasswordJSON`;
  added `ResolveRedirect` to the `authService` interface.
- `internal/inbound/authentication/passwordless.go` — renamed the JSON bodies to
  `passwordlessStartJSON`/`passwordlessVerifyJSON`/`passwordlessRedeemJSON`;
  documented the single-registration/dual-transport `mountPasswordless`.
- `internal/inbound/authentication/security.go` — added `writeHTMLSecurity` (full
  HTML header policy incl. nonced CSP).
- `internal/inbound/authentication/html.go` — `renderPage`/`renderError` apply
  `writeHTMLSecurity` with the per-render nonce; public GET handlers enrich
  return-to (allowlist-validated) and generic PRG/sent/check messages; account-page
  GET handlers threaded the nonce (mechanical; 8.4 enriches behavior).
- `internal/inbound/authentication/dispatch_test.go` (new) — content-type routing
  (JSON/form/415, Views vs nil), JSON-contract-unchanged (explicit+absent header,
  Views vs nil), form login/register/forgot/passwordless-start PRG (303 + Location +
  session cookie), return-to allowlist fallback, failure re-render (no session, no
  redirect), HTML security headers, and cross-site origin rejection.

Verification (exact commands, observed):
- `cd features/authentication && go test ./internal/inbound/authentication/... -run
  'JSON|HTML|Form|ContentType|PRG'` — PASS (all dual-transport, PRG, header, and
  origin tests green; existing requireJSON/strictJSONBody/mount tests still green).
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` —
  PASS (whole feature module, incl. authsvc, green).
- `make check` — PASS ("all checks passed"): templ-drift gate clean (no `.templ`
  touched, no regeneration needed), every module builds/vets/tests incl.
  `examples/auth-cms` (compiles over the additive dispatcher and `ResolveRedirect`)
  and the bundled `views/templ` module, plus all 13 guards.
- `make guard` — PASS (exit 0, all 13 guards green; feature core still has no
  `a-h/templ` import — G2 green).

### 2026-07-13 — AV3-8.4 (account-security HTML handlers)

Converted every live-session-gated account POST endpoint to the content-type
dispatcher (rename the prior body `*JSON`, add a form arm) and added the account-
security HTML form handlers over the SAME service methods the JSON arm calls — the
HTML layer recalculates no method safety, assurance, rate limit, context binding, or
policy. Converted: `/auth/password/{change,set,remove/start,remove}`,
`/auth/step-up/{begin,password,code}`, `/auth/identifiers/{email,phone}{,/confirm}`,
`/auth/oauth/{provider}/unlink{,/start}`. The identifier PATCH/DELETE `{id}` edges
stay JSON-only (an HTML form cannot emit those verbs); a new form-only POST
`/auth/identifiers/{id}` routes an `action` field to the same SetIdentifierUses /
RemoveIdentifier methods. No duplicate POST registration — each shared POST keeps its
single route; the new `{id}` POST has no JSON twin.

Premise adaptations (logged):

- **Form-lane CSRF (the decision this task owned, design §9.1).** A form POST cannot
  set the `X-CSRF-Token` header the double-submit gate compares, so
  `requireBrowserSafeMutation` (security.go) now, after enforcing the allowlisted
  Origin, DEFERS the token compare for a form content type and lets the request
  through; the JSON/fetch header double-submit is unchanged. The form handler closes
  the deferred lane: every account form runs through `accountForm` →
  `formCSRFOK` (account_forms.go), which compares the body's `csrf_token` field to the
  `auth_csrf` cookie in constant time (`crypto/subtle`) and renders a generic 403 on
  mismatch. Neither lane is weakened: JSON still requires the header, form still
  requires the cookie-matching body token, and a form body reaches an account form
  handler only when Views is wired (else the dispatcher 415s before any state change).
  This is the design's recommended §9.1 boundary; existing JSON CSRF tests
  (`TestPasswordSetCookieRequiresCSRF`) stay green because JSON is not a form content
  type.
- **HTML method-override for the identifier `{id}` twins.** The bundled edit form
  POSTs `/auth/identifiers/{id}`; `identifierEditForm` reads an `action` field
  (`remove` → RemoveIdentifier/DELETE, default → SetIdentifierUses/PATCH). Both
  consume the same operation-bound recent-auth grant the JSON edges do.
- **Stale-session redirect (design §9.2).** The JSON `RequireLiveSession` writes a
  401; an HTML account page must instead redirect a denied browser to login.
  `htmlLiveSession` (html.go) wraps the service gate with a buffering `htmlGate`
  writer: a denial (a write before the page runs) is swallowed and converted to a 303
  to `/auth/login?return_to=<safe relative path>`; a success passes writes straight
  through. Revocation/liveness stays the service's decision — only the denial
  presentation changes. Applied to the account GET pages; the shared account POST
  routes keep the JSON gate (a stale form POST returns the JSON 401 — a minor UX edge,
  not a security one). `safeRelativePath` (forms.go) validates the reflected return-to
  (single leading slash, no scheme/`//`/backslash) so it can never be an open-redirect
  vector; `resolveReturnTo` now honors a safe relative path in addition to the exact
  OAuth allowlist, so the login round-trip and the PRG lanes accept same-origin
  relative destinations (absolute non-allowlisted targets still fall back to `/`, so
  `TestFormLoginReturnToAllowlist` stays green).
- **Operation-bound HTML step-up (design §5.0).** The step-up grant binds to
  (session, purpose, context); a form must carry those. Added non-secret
  `Purpose`/`Context` fields to `StepUpPage` (views.go; the public alias re-exports
  them) and a `stepUpBinding` partial to `stepup.templ` emitting them as hidden fields
  on all three step-up forms (regenerated via `make generate`; the AV3-8.2 substring
  snapshot tests stay green). A sensitive form whose caller lacks the grant redirects
  to `/auth/step-up?purpose=…&context=…&operation=…&return_to=…`; the completion PRGs
  back to the action page with the grant now available. The recent-primary-login
  shortcut means a freshly logged-in session already satisfies the grant, so the
  redirect fires only for an aged session (exercised by aging the session's
  recorded authentication time in the test fixture).

GET-page enrichment (html.go): account page renders the masked primary as the actor;
identifier-edit page pre-fills the masked existing address + current uses/primary from
the masked inventory; step-up page reports only viable completion methods
(`PasswordAvailable` = has-password, `CodeAvailable` = a verified recovery identifier
exists) with a masked destination; password-remove and OAuth-unlink pages show the
masked recovery destination and a generic "code sent" notice after their start step.
No page renders a password, code, token, pepper, unmasked address, or raw provider
token; failed forms never repopulate a secret (the models carry no such field).

Scope note (flagged for AV3-8.8, not silently fixed): the AV3-8.2 bundled templates
provide no identifier-CONFIRM code form and no identifier-REMOVE button, so those two
HTML journeys are wired at the handler/service level (form arms call the right
methods and PRG) but are not reachable from the bundled default UI; the demonstration
surface (AV3-8.8) adds the navigation. The JSON confirm/PATCH/DELETE edges are
unchanged and fully covered.

Files changed:
- `internal/inbound/authentication/security.go` — `requireBrowserSafeMutation` defers
  the double-submit token compare for a form content type (Origin still enforced),
  ceding the form lane to the body-`csrf_token` compare.
- `internal/inbound/authentication/account_forms.go` (new) — `accountForm`/`formCSRFOK`
  (form-lane CSRF), `formPrincipal`/`redirectToLogin`, `stepUpRedirect`,
  `accountFailure`/`formUses`, and every account form handler (password
  change/set/remove-start/remove; step-up begin/password/code; identifier
  add-email/phone, confirm-email/phone, `{id}` edit; OAuth unlink-start/unlink) with
  their generic re-render helpers.
- `internal/inbound/authentication/html.go` — `htmlGate`/`htmlLiveSession` (stale
  session → login redirect), `stepUpParams`/`stepUpPath`/`stepUpModel`,
  `maskedRecovery`/`maskedPrimary`/`populateIdentifierEdit`; `mountHTML` now takes
  `browserSafe`, gates account GET pages with `htmlLiveSession`, and registers the
  form-only POST `/auth/identifiers/{id}`; account GET pages enriched (actor mask,
  identifier-edit prefill, step-up viable methods, masked destinations, sent notices).
- `internal/inbound/authentication/forms.go` — `resolveReturnTo`/`safeRelativePath`;
  `safeReturnTo`/`prgRedirect` route through `resolveReturnTo` (safe relative paths +
  allowlist).
- `internal/inbound/authentication/routes.go` — `mountHTML(r, h, browserSafe)`.
- `internal/inbound/authentication/{sessions,stepup,password,identifiers,oauth}.go` —
  each account POST handler renamed `*JSON` with a new dispatcher entrypoint of the
  original name (`dispatch(jsonArm, formArm)`); JSON DTO/status/body/cookie contracts
  byte/semantically unchanged.
- `internal/inbound/authentication/views.go` — `StepUpPage` gains `Purpose`/`Context`.
- `views/templ/stepup.templ` (+ regenerated `stepup_templ.go`) — `stepUpBinding`
  partial emits the purpose/context hidden fields on all three step-up forms.
- `internal/inbound/authentication/account_forms_test.go` (new) — capturing Views;
  form-CSRF required, change-password PRG, wrong-current re-render (401 + secret
  non-echo), set-password already-set re-render (409), identifier-add step-up redirect
  (aged session, purpose-bound) and add PRG (fresh session), stale-session login
  redirect (validated return-to), masked actor, OAuth unlink bad-code generic
  re-render, `accountFailure` collision/last-method mapping, `safeRelativePath`.

Verification (exact commands, observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` —
  PASS (whole feature module, incl. the new account-form tests and the unchanged JSON
  password/identifier/stepup/oauth suites).
- `make generate` — PASS; the templ-drift gate in `make check` is clean (regeneration
  deterministic, one `.templ` touched and regenerated).
- `cd features/authentication/views/templ && go build ./... && go test ./... && go
  vet ./...` — PASS (AV3-8.2 renderer snapshot tests green over the added hidden
  fields).
- `make check` — PASS ("all checks passed"): every module builds/vets/tests incl.
  `examples/auth-cms` (compiles over the additive dispatchers, the `StepUpPage`
  fields, and the new `mountHTML` signature) and the bundled `views/templ` module,
  plus the templ-drift gate and all 13 guards.
- `make guard` — PASS (all guards green; feature core still has no `a-h/templ`
  import — G2 green).

For AV3-8.5 (partial override + presentation-isolation proof): handlers render
EXCLUSIVELY through the `Views` port (no concrete component import in the core; G2
still green); the account surface added no new port method (the AV3-8.1 interface is
unchanged except the additive `StepUpPage.Purpose/Context` model fields), so a partial
override that embeds `templ.Views` and overrides only `Login`/`AccountSecurity` still
satisfies every method. Presentation cannot bypass security: the middleware chain
(client-info → RequireLiveSession/htmlLiveSession → browserSafe Origin+deferred-CSRF),
the content-type dispatcher, the service methods, the redirect resolver
(`resolveReturnTo`), the form-lane `formCSRFOK`, and the status mapping all live in the
inbound handlers, never in a Views method — overriding a page changes rendering only.
The email `TemplateRegistry` and the page `Views` remain distinct facilities (no new
coupling introduced).

### 2026-07-13 — AV3-8.5 (partial override and presentation-isolation proof)

Pure proof task: two new test facilities, no production-code change (no `.templ`
touched, so the drift gate stayed clean without a regenerate). The task named
guards/greps but no dedicated `Verify:` block, so verification is the phase acceptance
gate plus the named greps.

Proof 1 — partial override of the bundled default (in the templ module, where
`templ.Views` lives). `views/templ/views_test.go` gains `hostViews struct{ Views }` +
`TestEmbed_OverrideLoginAndAccountSecurity`: a host type that embeds the bundled
`templ.Views` and overrides ONLY `Login` and `AccountSecurity`. The test proves both
overrides win (host markers render) and every one of the other fourteen methods is
supplied by promotion and renders the shipped default (a per-page landmark, e.g.
`action="/auth/register"`, `<html`), never leaking a host marker. The compile-time
`var authentication.Views = hostViews{New()}` assignment is itself the "promoted
defaults satisfy every other method" proof.

Proof 2 — full-port stdlib-template Views + presentation isolation (in the feature
core inbound test package, which is sdk-only and CANNOT import templ — the isolation
claim's structural half). New `internal/inbound/authentication/isolation_test.go`:
- `htmlTemplateViews` implements all sixteen port methods through ONE reusable
  `sdk/foundation/web.Template` over a single `html/template` set — no templ import —
  with a compile assertion `var _ Views = htmlTemplateViews{}`. `TestFullPortHTML`
  `TemplateViewsRenders` drives it through the real mount and asserts every public GET
  page renders its `TPL:<page>` body. This is the "a full-port implementation can use
  one reusable html/template/web.Template renderer for all methods without importing
  templ" proof.
- `overrideViews struct{ Views }` overrides only `Login`/`AccountSecurity` over an
  embedded port value (the same partial-override shape as Proof 1, expressed against
  the port so the core needs no templ).
- `TestPresentationOverrideCannotBypassSecurity` is the byte-identical-security proof
  the phase file asked for: three hosts differing ONLY in presentation (the marker
  `stubViews`, the `overrideViews` partial override, and the entirely different
  `htmlTemplateViews` view technology) run the identical security-sensitive request
  sequence (public GET header policy; cross-site Origin rejection to 403; unsupported
  content type to 415; form-login failure re-render status + no session cookie; form
  register+login 303 PRG + session cookie; gated `/auth/account` no-session redirect).
  A body-free `securityEnvelope` (status, Location, Cache-Control, Referrer-Policy,
  X-Frame-Options, X-Content-Type-Options, CSP-present, session-cookie-present) is
  captured per request and asserted byte-identical across all three Views. A sanity leg
  confirms the bodies genuinely DID differ (`OVERRIDE-login` vs `TPL:login`), so the
  envelope equality is a non-trivial isolation result. This demonstrates that swapping
  a page cannot bypass the middleware, decoder, service, redirect, or status policy —
  all of which live in the inbound handler, never a Views method.

Guards/greps run and observed:
- authentication core has NO `a-h/templ` import and NO import of the concrete
  `features/authentication/views/templ` module anywhere in the core (grep clean; the
  only `views/templ` matches in the core are prose comments, not imports). Enforced
  structurally by FS1/G5 (feature core go.mod requires sdk only) + G2.
- handlers render EXCLUSIVELY through the port: every render site is `h.views.<Method>`
  fed to `renderPage`/`renderForm`/`renderError`; no handler constructs a concrete
  component (grep over `internal/inbound/authentication/*.go`).
- email `TemplateRegistry` (`sdk/capabilities/email`, used only by
  `internal/logic/delivery/router.go`) and the page `Views` port
  (`features/authentication`, `web.Renderer`, consumed only in `internal/inbound`) are
  distinct facilities in distinct packages with no shared type — email content
  rendering vs. HTML page rendering.

Files changed:
- `features/authentication/views/templ/views_test.go` — `hostViews` +
  `TestEmbed_OverrideLoginAndAccountSecurity` (embed `templ.Views`, override only
  `Login`/`AccountSecurity`, promoted defaults proven for the other fourteen).
- `features/authentication/internal/inbound/authentication/isolation_test.go` (new) —
  `htmlTemplateViews` full-port stdlib-template Views (+ compile assertion),
  `overrideViews` partial override, `TestFullPortHTMLTemplateViewsRenders`, and
  `TestPresentationOverrideCannotBypassSecurity` (byte-identical security envelope
  across three presentations).

Verification (exact commands, observed):
- `cd features/authentication/views/templ && go test ./... -run Embed -v` — PASS
  (`TestEmbed_OverrideOneMethod`, `TestEmbed_OverrideLoginAndAccountSecurity`).
- `cd features/authentication && go test ./internal/inbound/authentication/... -run
  'FullPortHTMLTemplateViews|PresentationOverrideCannotBypassSecurity' -v` — PASS.
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` —
  PASS (whole feature module).
- `cd features/authentication/views/templ && go build ./... && go vet ./... && go test
  ./...` — PASS.
- `make guard` — PASS (all 13 guards green, exit 0; feature core still has no
  `a-h/templ` import — G2 green).
- `make check` — PASS ("all checks passed"); templ-drift gate clean (no `.templ`
  touched), every module builds/vets/tests incl. `examples/auth-cms` and the bundled
  `views/templ` module.

For AV3-8.6 (auth-cms composition root + development secrets): the bundled default is
wired as `authtempl.New()` into `Config.Views` (the `templ` module's `New()` returns a
zero-value-usable `Views`); the isolation proof confirms wiring a different Views —
including a host embedding `authtempl.Views` and overriding pages — cannot change any
security behavior, so AV3-8.9's real host override rides the same guarantee. The email
`TemplateRegistry` (delivery) and page `Views` are wired independently; AV3-8.6 sets up
both without coupling them.

### 2026-07-13 — AV3-8.6 (auth-cms composition root and development secrets)

Composition-only task: wired the bundled default HTML surface and the remaining v3
Config facilities into the proof host, factored the auth Config construction into a
testable seam, and added startup / production-negative unit tests. No feature-core or
templ change (no `.templ` touched; the drift gate stayed clean without a regenerate).

Wired into the auth `Config` (all additive; the JSON API contract is unchanged):
- `Views: authtempl.New()` — the sibling `features/authentication/views/templ` module's
  zero-value default (added as a require + dev `replace` in the host `go.mod`), so the
  full HTML GET/form surface mounts alongside the unchanged JSON API.
- `AllowedOrigins` (design §9.1) — the browser-safe mutation Origin allowlist,
  defaulting to this host's own origin (`AUTH_ALLOWED_ORIGINS` override) so same-origin
  browser forms pass and cross-site credentialed POSTs are refused.
- `Passwordless: [email, phone]` (design §4.2) — both v3 kinds enabled
  (`AUTH_PASSWORDLESS` override); email via the required Mailer, phone via the console
  Notifier. Enablement's preconditions (atomic challenge rail, durable outbox,
  link-capable base URL) are all already wired, so construction is clean.
- `PublicAuthBaseURL` (design §6.4) — the config-only magic-link / redemption base URL
  (`AUTH_PUBLIC_BASE_URL` override), defaulting to this host's http origin; request
  Host/forwarded headers never participate.
- `DeliveryWorkerAcknowledged: true` — affirms the host runs `RunDeliveryWorker` (it
  already does, with graceful shutdown); development tolerates its absence but the host
  sets it truthfully to demonstrate the §8 wiring.

Development secrets: renamed the challenge-pepper env var from the pre-v3
`AUTH_CHALLENGE_HMAC_KEY` to the design-canonical `AUTH_CHALLENGE_PEPPER` (§3.3/§11),
keeping the HMAC key ring with active key ID `dev`. The FIVE distinct auth secrets are
now all documented as separate roles in `.env.example` (`AUTH_JWT_SECRET`,
`AUTH_CHALLENGE_PEPPER`, `AUTH_IDENTIFIER_KEY`, `AUTH_DELIVERY_ENCRYPTER_KEY`,
`AUTH_TOKEN_ENCRYPTER_KEY`) — each with a safe development ephemeral fallback and a WARN
that never prints key material (the existing builder discipline, preserved).

Premise adaptations (logged):
- **The handoff's "ContactChanges is NOT yet wired in authmem's `Repositories()`" is
  stale.** `authmem.Repositories()` already returns `ContactChanges: contactChangeRepo{…}`
  (and every other v3 port), and `ports_v3.go` implements them, so no authmem change was
  needed for the identifier routes to work. Logged rather than "fixed" — nothing to fix.
- **`buildAuthConfig(log, granter)` extraction (task-driven, not drive-by).** The task
  requires startup + production-negative unit tests over the host's real wiring, which
  needs a testable seam. The inline `authCfg` literal in `run()` was moved verbatim into
  `buildAuthConfig` in `main.go` (which already imports every collaborator); `run()` now
  calls it with `relationshipGranter{authorizer}`. The Granter is a parameter so the
  tests pass `nil` (invitations off) without building an authorizer.
- **authmem declares no delivery durability metadata (kept as-is).** The AV3-4.4 note
  said authmem *could* declare `Durability{InProcessOnly:true}`. Left undeclared so the
  production-negative test isolates the console-transport rejection: the durability gate
  (validated before the transport gate in `NewService`) tolerates a metadata-less repo,
  so `ErrInsecureDeliveryTransport` is the single, unambiguous production failure. The
  broader production-negative matrix (insecure URL/cookies, memory limiter, audit,
  worker) is AV3-8.9's scope.
- **Redress URL:** there is no host `Config` knob for it in v3 — the independent
  binding notice (design §5.5/§6.4) is feature-owned content rendered by the delivery
  router's `identifier_change_notice` template, not a composition input, so nothing was
  invented in the host. Flagged for AV3-8.9/8.10 copy review, not silently added.

Files changed:
- `examples/auth-cms/cmd/server/main.go` — import `authtempl`; `run()` calls the new
  `buildAuthConfig(log, granter)` seam instead of the inline literal; `buildAuthConfig`
  wires Views/AllowedOrigins/Passwordless/PublicAuthBaseURL/DeliveryWorkerAcknowledged
  plus the existing dev secrets and returns `(auth.Config, error)`.
- `examples/auth-cms/cmd/server/demo.go` — `buildChallengeProtector` reads
  `AUTH_CHALLENGE_PEPPER` (was `AUTH_CHALLENGE_HMAC_KEY`); new `publicAuthBaseURL`,
  `allowedOrigins`, `passwordlessKinds`, and `splitEnvList` helpers; `strings` +
  `identity` imports.
- `examples/auth-cms/cmd/server/main_test.go` (new) — `TestBuildAuthConfigConstructs`
  (dev wiring constructs cleanly, all v3 facilities non-nil),
  `TestProductionConsoleWiringFailsConstruction` (production + console →
  `ErrInsecureDeliveryTransport`), `TestDeliveryWorkerStartStop` (worker runs, stops on
  cancel within 5s, no goroutine leak).
- `examples/auth-cms/.env.example` — five-distinct-keys block, `AUTH_CHALLENGE_PEPPER`
  rename, and the HTML/passwordless/magic-link knobs (`AUTH_PUBLIC_BASE_URL`,
  `AUTH_ALLOWED_ORIGINS`, `AUTH_PASSWORDLESS`), all with safe dev defaults.
- `examples/auth-cms/go.mod` / `go.sum` — require + dev `replace` for the bundled
  `features/authentication/views/templ` module; `make tidy` normalized go.sum.

Verification (exact commands, observed):
- `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` — PASS (the
  three new startup/production tests green; existing internal tests green).
- `make tidy` — clean (go.sum normalized for the new module).
- `make check` — PASS ("all checks passed"): every module builds/vets/tests incl.
  `examples/auth-cms` over the new wiring and the bundled `views/templ` module; the
  templ-drift gate is clean (no `.templ` touched); integration-tag vet green.
- `make guard` — PASS (all guards green; feature core still has no `a-h/templ` import).

For AV3-8.7 (magic-link landing integration): the host now enables passwordless for
`email` + `phone`, so `POST /auth/passwordless/{start,verify,redeem}` and the bundled
magic-link landing GET page are registered; `PublicAuthBaseURL` defaults to
`http://localhost:8082` (override `AUTH_PUBLIC_BASE_URL`), so magic links point at this
host and the fragment-token landing page POSTs back to `/auth/passwordless/redeem`.
`AllowedOrigins` defaults to the same origin, so the landing page's form POST passes the
browser-safe gate. The email OTP/link is delivered to the server log by the console
Mailer (dev transport); drive it end-to-end from there.

### 2026-07-13 — AV3-8.7 (magic-link landing integration)

Ran the proof host end-to-end and drove the bundled magic-link landing page in a REAL
headless Chromium (Playwright-core 1.61.1 over the ms-playwright chromium-1228 build,
installed into the scratchpad — nothing added to the repo). The run-and-look surfaced
two integration defects that broke the default host magic-link journey; both are fixed
in-scope (host composition + the bundled landing script), then re-proven at the DOM level.

Defects found + fixed (logged premise adaptations — the prior handoff's "drive it
end-to-end from there" did not survive first contact):

- **Bug 1 (host): the default magic link did not land on the landing page.** The
  framework builds the link as EXACTLY `Config.PublicAuthBaseURL + "#token=<token>"`
  (`magicLinkURL`, design §6.4 — the base is the whole link target). The host defaulted
  `PublicAuthBaseURL` to `http://localhost:8082` (the origin ROOT), so a clicked link
  landed on `/` — the CMS home (`<title>Home</title>`), which has no fragment reader —
  not the bundled landing GET at `/auth/magic` (`<title>Signing you in</title>`).
  Confirmed with curl before the fix (`GET /` = CMS Home; `GET /auth/magic` = the
  landing page). Fix: `publicAuthBaseURL()` (demo.go) now defaults to
  `callbackBase() + "/auth/magic"` so the zero-config proof host lands on its own bundled
  landing page; `AUTH_PUBLIC_BASE_URL` still overrides for a real deployment (which sets
  its own https landing URL). `AllowedOrigins` is unchanged (origin-only, and the browser
  sends `Origin: http://localhost:8082` on the redeem POST — it matches). The base-URL
  validator already permits a path (`validatePublicAuthBaseURL`: absolute http(s) + host,
  https in production), so the pathful default constructs cleanly.
- **Bug 2 (bundled landing script): the fragment was posted verbatim, not the parsed
  token.** `magicLinkURL` emits `#token=<url-escaped-token>` (frozen + tested in phase 7:
  `passwordless_redeem_test.go`, `passwordless_events_test.go`). The AV3-8.2 landing
  script stashed the WHOLE fragment (`window.location.hash.slice(1)` = `"token=<tok>"`)
  into the hidden field, so the redeem POST carried `token=token%3D<tok>`. Proven broken
  at the redeem endpoint before the fix: POST with `token=token=<tok>` → **401**, POST
  with the parsed `<tok>` → **303** + session cookies. Fix (`passwordless.templ`
  MagicLinkLanding, regenerated via `make generate`): the nonced script now parses
  `new URLSearchParams(frag).get("token")` (URL-decoding included) and only submits when
  a token is present; history is still scrubbed first via `replaceState`; the visible
  `Continue` manual fallback is unchanged. The magic-link URL contract (phase 7) is NOT
  touched — the fix is on the consuming side, so the AV3-8.2 renderer snapshot tests and
  the phase-7 URL tests both stay green. (Scope note, flagged not fixed: the reset landing
  script in `recovery.templ` reads the whole fragment the same way; reset is out of
  AV3-8.7 scope and has no server-built `#token=` reset-link builder, so it is left as-is
  for a future recovery-UX task.)

Browser-level / DOM evidence (real Chromium, captured to scratchpad `evidence*.json`):

- **Success path** (`drive.js`, fresh issued link
  `http://localhost:8082/auth/magic#token=<tok>`): the page auto-read the fragment,
  `history.replaceState`-scrubbed it, and POSTed to redeem →
  `finalURL http://localhost:8082/`, `location.hash ""` (scrubbed), `session` +
  `session_refresh` + `auth_csrf` cookies set (`session` HttpOnly), and a follow-up
  `fetch('/auth/methods')` returned **200** (a live-session-gated endpoint — the session
  is genuinely authenticated). The browser made exactly `GET /auth/magic`,
  `POST /auth/passwordless/redeem`, `GET /`, `GET /auth/methods`; **no request URL
  contained `token=`** (`tokenInAnyRequestURL: false`) — the token rode only the fragment
  (never sent to the server) and then the POST body.
- **Post-scrub DOM freeze** (`drive2.js`, `HTMLFormElement.prototype.submit` neutralized
  by an init script so the DOM stays on `/auth/magic`): `location.hash ""` (scrubbed
  BEFORE submit), `submitWasInvoked true`, hidden `#magic-token` field value ==
  the RAW token (`hiddenFieldEqualsRawToken true`, `hiddenFieldHasTokenEqPrefix false` —
  proving the parse fix, not the old whole-fragment value), and the visible manual
  fallback `Continue` button present.
- **Generic error state** (`drive3.js`, a never-issued token): redeem → **401**, the
  landing re-renders GENERIC enumeration-safe copy ("That sign-in link is no longer valid.
  Request a new one."), `finalHash ""` (fragment scrubbed even on failure), the token does
  NOT appear in the rendered body (`mentionsToken false`), and NO session cookie is set.
- **No token in GET logs / referrer / headers.** The server request log carries zero
  `token=` in any path/query (grep empty); `GET /auth/magic` = 200 and
  `POST /auth/passwordless/redeem` = 303/401 are logged with the bare path only. The
  landing GET sets `Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
  `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and a restrictive CSP
  (`default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none';
  script-src 'nonce-…'`) — the nonce admits exactly the fragment-reader inline script and
  nothing else; `form-action 'self'` admits the redeem POST.

Files changed:
- `features/authentication/views/templ/passwordless.templ` — MagicLinkLanding nonced
  script parses the `token` param out of the `#token=<escaped>` fragment (URLSearchParams,
  URL-decoded) instead of stashing the whole fragment; scrub-then-parse-then-submit order
  preserved; visible `Continue` fallback unchanged.
- `features/authentication/views/templ/passwordless_templ.go` — regenerated via
  `make generate` (drift-clean; re-run updates=0).
- `examples/auth-cms/cmd/server/demo.go` — `publicAuthBaseURL()` default now
  `callbackBase() + "/auth/magic"` so the zero-config link lands on the bundled landing
  page; doc comment updated.
- `examples/auth-cms/.env.example` — `AUTH_PUBLIC_BASE_URL` comment/default reflect the
  landing-path requirement.

Verification (exact commands, observed):
- Real-browser run-and-look (scratchpad Playwright-core + chromium-1228): success,
  post-scrub freeze, and generic-error legs all as above (evidence JSON captured).
- curl leg (pre/post fix): whole-fragment redeem → 401; parsed-token redeem → 303 +
  `session`/`session_refresh` Set-Cookie.
- `make generate` — PASS; re-run drift-clean (authentication views updates=0).
- `cd features/authentication/views/templ && go build ./... && go test ./... && go vet
  ./...` — PASS (AV3-8.2 renderer snapshot tests green over the script change).
- `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` — PASS
  (startup/production tests green over the base-URL default change).
- `make check` — PASS ("all checks passed"): every module + templ-drift gate + guards.
- `make guard` — PASS (all guards green; feature core still has no `a-h/templ` import).

For AV3-8.8 (account-security demonstration surface): the passwordless magic-link journey
is fully working from the bundled UI now, but the AV3-8.4 scope note still stands — the
bundled default templates provide no identifier-CONFIRM code form and no identifier-REMOVE
button, so those two HTML journeys are handler/service-wired but not reachable from the
default UI; AV3-8.8 adds the navigation/pages. The magic-link base-URL contract to
remember: `PublicAuthBaseURL` must point at the page hosting the fragment reader (here
`/auth/magic`), not the origin root — any host wiring a different landing route must set
`AUTH_PUBLIC_BASE_URL` accordingly.

### 2026-07-13 — AV3-8.8 (account-security demonstration surface)

Closed the two AV3-8.4-flagged bundled-UI gaps (identifier-CONFIRM code form,
identifier-REMOVE button) and drove the whole account-security surface end-to-end in a
real headless Chromium. The run-and-look surfaced a third, blocking integration defect in
the proof host's memory store (the masked inventory rendered no identifiers at all); it is
fixed in-scope in `authmem`, matching the AV3-8.7 precedent (run-and-look defects fixed in
the host, then re-proven at the DOM level). No feature-core service/policy change, no new
`Views` port method (the port stayed stable for the AV3-8.5/8.9 override proof), and no
host-specific duplicate handlers — the form arms and service methods already existed
(AV3-8.4); this task only made them reachable from the bundled default UI.

Bundled-UI additions (sibling `views/templ` module + the feature core's thin GET/PRG
wiring — never host handlers):
- `IdentifierForm` gained a `confirm` mode: a one-time-code entry form that POSTs the
  same kind-specific `/auth/identifiers/{kind}/confirm` edge the JSON API uses (autocomplete
  `one-time-code`, no address input, no code echo). The add form's success PRG now lands on
  `/auth/identifiers/confirm?kind=<kind>` (was `/auth/account`), so the ownership-proof step
  is reachable; a confirm failure re-renders the confirm form (was the add form) with
  generic copy. New live-session-gated GET `/auth/identifiers/confirm` renders it with a
  generic "we've sent a code" notice.
- `IdentifierForm` edit mode gained a second form: a Remove control that POSTs `action=remove`
  to `/auth/identifiers/{id}` (the HTML twin of the JSON DELETE edge, routed by the existing
  `identifierEditForm` dispatcher to `RemoveIdentifier`). A last-login-method removal is
  policy-refused by the service and re-renders generic actionable copy ("…without a way to
  sign in, so it was not applied") — never an unmasked value.
- Minimal navigation: a "Back to account security" link on the add/confirm/edit identifier
  pages so the account hub (`/auth/account`) chains to every management journey and back.

Premise adaptation (logged, host-store defect fixed in-scope — the run-and-look blocker):
- **authmem's credential-mutation `Snapshot` never reflected the identifier rail.** The
  masked inventory (`Service.Methods`) projects `set.Identifiers` from
  `CredentialMutations.Snapshot`; authmem projected that from a separate
  `credentialIdentifiers` stand-in map that was initialized empty and only ever mutated by
  retire/change-uses — it was NEVER seeded from the authoritative identifier rows the
  identifier rail (`identifierRepo`) writes at registration and add/confirm. Result: on the
  live host `GET /auth/methods` returned `identifiers:[]` for every user (curl-confirmed) and
  the account page rendered "No identifiers", so the entire identifier-lifecycle surface was
  un-demonstrable. This was invisible to `storetest`: the exported CredentialMutations
  conformance seeds via the public identifier rail (`CreateWithPrimaryIdentifier`) but never
  asserts `set.Identifiers`, and the reference double keeps the same never-bridged stand-in
  (seeded directly only in reference-local policy tests). pgx/turso have no such gap — they
  project the MethodSet from the one `user_identifiers` table. Fix: authmem's
  `credentialMutationRepo` now projects `Snapshot.Identifiers` from, and routes
  `RetireIdentifier`/`ChangeIdentifierUses` to, the SAME authoritative `identifiers` rows
  (like a SQL store over `user_identifiers`), all under the one shared mutex so the
  identifier and credential views never disagree and `auth_revision` stays serialized. The
  dead `credentialIdentifiers` map + its two helpers were removed. `storetest` conformance
  stays green (it seeds via the rail and asserts revision-CAS/HasPassword/OAuth, none of
  which changed). This is a host proof-repo fix, not a feature-core change; the feature core
  and the reference/pgx/turso stores are untouched.

Browser-level / DOM evidence (real Chromium via the scratchpad Playwright-core +
chromium-1228, reused from AV3-8.7; captured to `account-evidence.json`), fresh
register→verify→password-login session:
- **Masked inventory:** `/auth/account` renders the masked actor (`a•••@…`), the Password
  section, "Add an identifier", and "Linked accounts"; the full primary address never
  appears in the page (`accountNoRawEmail: true`). After adding a second identifier the
  inventory shows TWO masked identifiers (`manageLinkCount: 2`).
- **Identifier add → confirm (NEW):** the add form PRGs (303) to
  `/auth/identifiers/confirm?kind=email`; the confirm page renders the one-time-code field and
  posts `/auth/identifiers/email/confirm`. Entering the emailed code (read from the console
  log) PRGs back to `/auth/account` with the new masked identifier present.
- **Identifier remove (NEW):** the edit page shows the Remove control
  (`action=remove`); submitting it (fresh-session recent-auth shortcut, no step-up needed)
  PRGs to `/auth/account` and the inventory drops to one identifier
  (`manageLinkCountAfterRemove: 1`).
- **Policy-refused removal:** attempting to remove the last remaining login identifier
  re-renders generic actionable copy ("…without a way to sign in…") and does NOT remove it
  (`policyRefusedGenericCopy: true`), never exposing an unmasked value.
- Password lifecycle (set/change/remove links) and OAuth "Linked accounts" (the fake
  provider is wired, so unlink is reachable once linked) render on the account hub; their
  exhaustive twice-through drive is AV3-8.10's scope.

Files changed:
- `features/authentication/views/templ/identifier.templ` — `confirm` mode (one-time-code
  form to the kind-specific confirm edge), edit-mode Remove control (`action=remove`), and
  "Back to account security" navigation on add/confirm/edit.
- `features/authentication/views/templ/helpers.go` — `confirmIdentifierAction(kind)`.
- `features/authentication/views/templ/identifier_templ.go` — regenerated via
  `make generate` (drift-clean; re-run updates=0).
- `features/authentication/views/templ/views_test.go` — `TestIdentifierForm_Confirm`
  (kind-edge action + one-time-code + no address input) and an edit `action=remove`
  assertion added to `TestIdentifierForm_AddEdit`.
- `features/authentication/internal/inbound/authentication/html.go` — GET
  `/auth/identifiers/confirm` route + `identifierConfirmPage` handler; route-inventory
  comment updated.
- `features/authentication/internal/inbound/authentication/account_forms.go` — add-form
  success PRG now lands on the kind-bound confirm page; confirm failure re-renders the
  confirm form via new `renderIdentifierConfirm`.
- `features/authentication/internal/inbound/authentication/account_forms_test.go` —
  `TestAccountFormIdentifierAddPRG` now expects the confirm-page redirect.
- `examples/auth-cms/internal/authmem/ports_v3.go` — `credentialMutationRepo` projects and
  mutates the authoritative identifier rows (Snapshot + Retire/ChangeUses); removed the dead
  `credentialIdentifiers` helpers.
- `examples/auth-cms/internal/authmem/authmem.go` — dropped the `credentialIdentifiers`
  field/init and its now-unused `credential` import; updated the data-holder comment.

Verification (exact commands, observed):
- Real-browser run-and-look (scratchpad Playwright-core + chromium-1228): masked inventory,
  add→confirm(NEW)→account, edit remove(NEW)→account, and last-method policy-refused removal
  all as above (`account-evidence.json`). Pre-fix curl leg proved the blocker
  (`GET /auth/methods` → `identifiers:[]`) and the post-fix host renders two masked
  identifiers.
- `make generate` — PASS; re-run drift-clean (authentication views updates=0).
- `cd features/authentication/views/templ && go build ./... && go test ./... && go vet ./...`
  — PASS (renderer tests incl. the new confirm/remove assertions).
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` — PASS
  (inbound account-form suite incl. the updated PRG expectation).
- `cd examples/auth-cms && go build ./... && go vet ./... && go test ./...` — PASS (authmem
  conformance/storetest green over the projection change).
- `make check` — PASS ("all checks passed"): every module builds/vets/tests incl.
  `examples/auth-cms` and the bundled `views/templ` module; templ-drift gate clean;
  integration-tag vet green; all 13 guards green (feature core still has no `a-h/templ`
  import).

For AV3-8.9 (host override and production safeguards): the account surface is now fully
demonstrable on the default host, so AV3-8.9's real `authtempl.Views` page override rides a
working masked inventory. The bundled `Views` port is unchanged (no new method) — a host
embedding `authtempl.Views` and overriding a page still satisfies every method; the
identifier confirm/remove journeys are template + inbound wiring only, so an override that
swaps a page cannot change them. Also note for AV3-8.9/8.10: authmem now projects the
credential inventory from the real identifier rail (matching pgx/turso), so the
production-negative and twice-through run-and-look legs will see identifiers in the masked
inventory just as a SQL-backed host would; no further authmem work is expected.

### 2026-07-13 — AV3-8.9 (host override and production safeguards)

Replaced the phase-8.5 TEST override with a REAL auth-cms page override wired into the
running host, added the DISTINCT second override system (an email LayerApp content
override) end-to-end, made authmem honestly declare its outbox non-durable, and built
the fail-closed production-negative matrix on the host — all proven both hermetically
and with a live boot + curl run-and-look.

New feature-core surface (additive; the JSON API and every existing gate are unchanged):
- `Config.EmailContentTemplates []EmailContentTemplate` — the public seam for the email
  LayerApp override system (design §6.2). It threads into
  `delivery.NewRouter(delivery.Deps{AppTemplates: …})`, which already existed but had no
  public Config door. `EmailContentTemplate = delivery.TemplateOverride` and
  `const EmailContentNamespace = delivery.Namespace` are aliased per the `DeliveryStatus`
  precedent. `delivery.namespace` was exported as `delivery.Namespace` (unexported spelling
  kept as an internal alias — zero call-site churn) so a host names the override target
  without hardcoding the string. This is the SECOND override system; it swaps an email
  BODY and is a different Config field / different type / different subsystem from `Views`
  (which swaps a browser PAGE) — the "two override systems are distinct" claim, now
  demonstrable from one host package.

Host page override (the REAL one, replacing 8.5's test double):
- `examples/auth-cms/internal/authpages` (new) — `Views` embeds the bundled
  `authtempl.Views` and overrides ONLY `Login` with a Gopernicus-CMS-branded page rendered
  through `sdk/foundation/web.Template` (stdlib `html/template`, NO templ import in the
  host — the FS3 tech-neutral promise made concrete). The compile assertion
  `var _ auth.Views = Views{}` is the "promoted defaults satisfy every other method" proof;
  the branded Login posts to the SAME `/auth/login` with the SAME field names
  (`email`/`password`/`csrf_token`/`return_to`), so the dispatcher, CSRF/origin gate,
  service call, PRG, and status mapping are untouched. The password input carries no value
  attribute (no secret repopulation); the page is asset-free so it renders within the
  feature's restrictive CSP. Wired into `buildAuthConfig` as `Views: authpages.New()`
  (was `authtempl.New()`); the `authtempl` import moved from `main.go` into the override
  package.
- `authpages.EmailOverride()` + `templates/verification.html` — the host's email LayerApp
  override: a branded verification body that still renders `{{.Secret}}` (flow unbroken),
  wired as `EmailContentTemplates: []auth.EmailContentTemplate{authpages.EmailOverride()}`.

authmem durability declaration (phase-4's deferred wiring, now done here):
- `deliveryJobRepo.Durability() → {InProcessOnly: true}` + the
  `_ deliveryjob.DurabilityReporter = deliveryJobRepo{}` assertion. authmem's outbox lives
  in a map and cannot survive a restart, so it now HONESTLY fails closed in production —
  this IS the "metadata" production negative.

Production-negative matrix (the 8.6-deferred broader matrix, on the host):
- `cmd/server/production_test.go` (new) — a `productionBaseline(t)` helper returns a
  production-VALID config+repos (production transport doubles, a durable-declaring limiter,
  an HTTPS magic-link base, a durable-by-omission outbox wrapper, the identifier keyer
  `buildAuthConfig` already wires, worker acknowledged). `TestProductionBaselineConstructs`
  is the positive control: it proves the baseline constructs in production, so each negative
  isolates exactly one broken safeguard. `TestProductionNegatives` then breaks ONE knob per
  case and asserts the matching stable error, respecting `NewService`'s gate order:
  console transports → `ErrInsecureDeliveryTransport`; http public base (the unsafe
  Views-magic-link/public-URL combination) → `ErrPublicAuthBaseURLInsecure`; nil→Memory
  limiter → `ErrNonDurableRateLimiter`; nil keyer → `ErrIdentifierKeyerRequired`;
  authmem's non-durable outbox metadata → `ErrNonDurableDeliveryRepository`; unacknowledged
  worker → `ErrDeliveryWorkerUnacknowledged`. `TestDevelopmentConsoleTransportWarns`
  observes the one development console WARN via a capturing slog handler.
- `cmd/server/override_test.go` (new) — `TestOverrideSystemsAreDistinct` (both override
  Config fields wired, email override targets the feature email namespace) and
  `TestEmailLayerAppOverrideWins`, a REAL register → durable-worker → capturing-mailer cycle
  proving the host's LayerApp verification body won over the LayerCore default.
- `cmd/server/main_test.go` — the 8.6 `TestProductionConsoleWiringFailsConstruction` was
  REMOVED: with authmem now declaring InProcessOnly, the durability gate fires before the
  transport gate, so that single-error test's premise no longer holds. The isolated matrix
  above supersedes it (and still covers the console rejection).
- `authpages/authpages_test.go` (new) — branded-Login field/marker rendering, promoted
  default (`Register`) still bundled, and the email override FS/namespace.

Premise adaptations (logged):
- **authmem now declares `Durability{InProcessOnly:true}` — the 8.6 note is reversed here
  by design.** 8.6 deliberately left it undeclared so its single console test isolated the
  transport error. 8.9 owns the metadata negative, so the honest declaration is wired now;
  the console test was replaced by the baseline+matrix pattern, which isolates every gate
  regardless of ordering (durability(7b) → worker(7c) → transports → limiter → keyer →
  public-URL). The production-VALID baseline therefore uses a `durableJobs` wrapper (embeds
  the bare `deliveryjob.Repository`, dropping the concrete `Durability()` method) as the
  stand-in for a real durable outbox — a memory host is inherently non-production, so
  proving each gate individually satisfiable required a durable stand-in.
- **"cookies" and "audit" from the task's negative list have NO feature construction
  gate.** Cookie `Secure` is a `CookieConfig.Secure` host deployment choice, not a
  RuntimeMode gate; the host DOES set Secure cookies (observed live: `auth_csrf=…; Secure`).
  `SecurityEvents` (audit) is OPTIONAL by design (§5.1, ratified AV9) — the feature keeps no
  audit trail when it is nil and raises no production error, so there is no host negative to
  assert. Both are flagged rather than invented; the covered gates are the ones that
  actually fail closed (transport/URL/limiter/keyer/metadata/worker). The "insecure URL"
  case IS the magic-link-over-http negative, framed as the unsafe Views/public-URL combo the
  task names.

Verification (exact commands, observed):
- `cd features/authentication && go build ./... && go vet ./... && go test ./...` — PASS
  (feature module green over the additive Config field + exported delivery.Namespace).
- `cd examples/auth-cms && go build ./... && go vet ./... && go test ./...` — PASS: the
  production matrix (6 isolated negatives + positive baseline control), the dev console-WARN
  observation, the email LayerApp override register→worker→capture integration, and the
  branded-Login rendering tests all green.
- `make generate` — PASS; drift-clean (no `.templ` touched — the host override is stdlib
  `html/template`; the email override is a static `.html`; `updates=0`).
- `make check` — PASS ("all checks passed"): every module builds/vets/tests incl.
  `examples/auth-cms` over the override wiring, templ-drift gate clean, integration-tag vet
  green, all 13 guards green (feature core still has no `a-h/templ` import).
- `make guard` — PASS (exit 0, all 13 guards).
- **Live run-and-look (host booted, curl):** `GET /auth/login` → 200 rendering the branded
  page (`Gopernicus CMS`, `data-brand="gopernicus-cms"`, `action="/auth/login"`,
  `name="csrf_token"`, `autocomplete="current-password"`) under the full HTML security header
  set (nonced CSP, no-store, no-referrer, DENY, nosniff). Startup log shows the dev
  console-transport WARN for both the email sender and the phone notifier. A same-origin
  FAILED form login → 401 re-rendering the BRANDED page with NO session cookie (only the
  `Secure` anti-CSRF cookie); a cross-site-Origin form login → 403; a JSON login → 401 with
  `application/json` — proving the override changes presentation ONLY (the origin gate, PRG,
  status mapping, and JSON contract are all unchanged).

For AV3-8.10 (complete JSON + HTML run-and-look protocol): the host now runs the REAL page
override (branded Login) and the email LayerApp override, so leg 12 (partial page override +
separate email template override) is wired and live-verified — 8.10 drives it twice-through
alongside the rest. Leg 10 (production negatives) is hermetically covered by the isolated
matrix; the live host is development-mode by design, so 8.10's production leg is the
construction-negative matrix, not a booted production host (a memory host cannot boot
production — that is the point). Note the two override systems are now reachable from one
host package (`internal/authpages`): `Views` (branded Login page) and `EmailContentTemplates`
(branded verification email) — distinct fields, distinct subsystems. The console-transport
WARN and the branded email are both observable in the server log during a register→verify
drive.

### 2026-07-13 — AV3-8.10 (complete JSON + HTML run-and-look protocol)

The phase-8 run-and-look milestone. Booted the real proof host
(`go run ./cmd/server`, in-memory stores, `RuntimeMode=development`, console
email + phone transports, `RunDeliveryWorker` live, `AUTH_DEBUG=1`, all five
secrets unset → ephemeral) and drove every core journey twice where applicable —
once through the JSON API (curl) and once through the bundled HTML pages/forms
(curl + a real headless Chromium via the scratchpad Playwright-core + chromium-1228,
reused from 8.7–8.9). Startup log carried the expected WARN set: four ephemeral-key
WARNs, the two development console-transport WARNs (email sender + phone notifier),
the in-process rate-limiter WARN, and the debug-route WARN. **No production code was
changed** — the one flagged latent item (recovery.templ reset fragment) was
investigated and left flagged (see below), so `make generate` stayed drift-clean.

Premise adaptations (logged, both test-harness facts, no product change):
- **`POST /auth/verify` requires `{email, code}`, not `{code}`.** The README's A9
  leg-0 `{"code":…}` is the pre-v3 single-pending-verification shape; the v3
  challenge rail keys the challenge by identifier, so the DTO
  (`verifyRequest{Email, Code}`) needs the email. A code-only body is a 400
  `bad_request`. (The HTML verify form carries the email as a hidden/query field,
  so the browser leg is unaffected.)
- **curl's `-d` sets `Content-Type: application/x-www-form-urlencoded` by default,**
  which the content-type dispatcher (AV3-8.3) correctly routes to the FORM arm. JSON
  legs must pass `-H 'Content-Type: application/json'`; the lenient absent-header JSON
  path (AV3-8.3) is exercised with `-H 'Content-Type:'` (header stripped). Both were
  used deliberately; not a product issue.

Per-leg observed results (numbered to the protocol list; HTML POST = 303 PRG, JSON
POST = JSON status/body — both asserted throughout):

1. **register → async verify → verify → password login.** JSON: register 201 (JSON
   body); pre-verify login 403 `permission_denied` (verified-email gate); async worker
   delivered the code (`outcome=delivered attempt=1`); verify 200 `{"status":"verified"}`;
   login 200 + `session` + `session_refresh` Set-Cookie. HTML: register form 303 →
   `/auth/verify?email=…`; verify form 303 → `/auth/login?email=…`; login form 303 → `/`
   + `session` cookie (real-browser drive: same PRG chain, ends authenticated).
2. **reset → prior sessions rejected → ordinary login.** Reset token delivered as a
   PLAIN token ("Use this token to reset your password: …"). HTML reset form 303 →
   `/auth/login`; the previously-live HTML session then 401s on `/auth/methods` (reset
   revokes all sessions); ordinary JSON login with the NEW password 200; the OLD
   password 401. Also observed at the credential-mutation edge: a JSON password CHANGE
   revoked the prior bearer+refresh (both 401 after), the v3 "sensitive mutation rejects
   prior sessions" contract.
3. **identifier add/change/remove/shared-notification phone.** JSON (bearer): add email
   200 `{"status":"sent",receipt}`; add phone (+E.164) 200 sent, delivered via the
   console NOTIFIER (`kind=phone … body="Your confirmation code is <redacted>"`) — the
   shared-notification phone rail; confirm email 200 `{"status":"confirmed"}` (inventory
   now two masked identifiers, added one `removable:true`); PATCH change-uses 200
   `{"status":"updated"}` (uses grew to recovery+notification); DELETE remove 200
   `{"status":"removed"}` (back to one); DELETE the last login identifier → 409 `conflict`
   (policy-refused). The add also fanned an independent `identifier_change_notice` to the
   notification identifier (design §5.5). HTML add→confirm→edit-remove and last-method
   policy-refusal were driven at the DOM level in AV3-8.8 (`account-evidence.json`).
4. **recent-auth sensitive mutations.** step-up begin (`purpose=remove_password`) 200 sent
   (code to recovery); step-up password (recent-auth grant) 200 `{"status":"verified",
   expires_at}`; a subsequent sensitive mutation then succeeds. HTML step-up binding
   (purpose/context hidden fields) + aged-session redirect proven in AV3-8.4.
5. **set/remove password and provider unlink/wrong-provider.** set-password when already
   set → 409 `password_already_set`; password remove start 200 sent → remove-with-code 200
   `{"status":"password_removed"}` (`has_password` then false). OAuth: fake-provider
   start 302 → callback 302 + session (linked, `removable:true`); unlink-start for the
   UNWIRED `google` → 404 (deny-by-absence); unlink-start `fake` 200 sent (provider-bound
   code); consuming that code at the `github` path → 404 (unwired; the code is provider-
   bound); consuming at `fake` → 200 `{"status":"unlinked"}`. (Cross-provider code
   REJECTION for a second WIRED provider is proven hermetically; this host wires only
   `fake`, so a wrong wired-provider path is unreachable — noted.)
6. **email magic link landing, phone OTP/link, refresh/logout.** passwordless start email
   `code` → 202 `{"status":"accepted"}` → verify 200 mint (tokens + cookies); start email
   `link` → 202 → magic link built as `http://localhost:8082/auth/magic#token=…` (config
   base, fragment token) → redeem 200 + session → REPLAY same token 401 (single-use atomic
   redemption). Refresh: happy 200 new pair; grace replay of the old token 200 access-only
   (NO `refresh_token` field); second replay 401 + session burned (`current` token then
   also 401; "refresh token reuse detected" WARN logged). Logout 200, both cookies cleared
   (`Max-Age=0`). Real-browser magic drive (`evidence810.json`): landing scrubbed the
   fragment (`magicHashScrubbed:true`), established the session, and `tokenInAnyRequestURL:
   false` (request paths `/auth/magic`, `/auth/passwordless/redeem`, `/` — the token rode
   only the fragment then the POST body). Phone OTP passwordless parity is hermetic (no
   verified phone-login identifier on the demo users); phone DELIVERY is shown live in leg 3.
7. **known/unknown start timing under blocked provider.** passwordless start with an UNKNOWN
   identifier and a KNOWN identifier both return an identical generic 202 `{"status":
   "accepted"}` at ~0.00–0.01s wall time (indistinguishable); the unknown address produced
   NO delivery job and never appears in the log — the start never synchronously resolves an
   account or calls a provider (the async worker owns resolution/delivery, so a blocked/slow
   provider cannot change the start response or leak existence).
8. **worker retry/replacement/terminal cleanup/purge.** The live host proved the happy path
   (every `delivery job` line `outcome=delivered attempt=1`, plus `initialized`/`skipped`
   lifecycle rows). Failure-injection paths are hermetic (console transport never fails):
   `TestWorkerRetryThenSucceed`, `TestWorkerReplaceSupersedesPriorJob` /
   `TestServiceReplaceSupersedes`, `TestWorkerTerminalFailureCancelsChallenge`,
   `TestWorkerPurgeRespectRetention`, `TestWorkerCrashAfterSendReplaysSameSecret`,
   `TestWorkerContentionSingleClaimant` — all PASS.
9. **pepper key rotation overlap.** Hermetic (single-process host has one active key ID
   `dev`): `TestChallengeProtectorKeyRotation` (an unexpired code stamped under a prior key
   still verifies during an overlapping rotation) and `TestChallengeProtectorUnknownKeyID`
   PASS; the live host stamps `protector_key_id=dev` on every challenge.
10. **CSRF/origin/body/XFF/Host/production negatives.** unsupported `Content-Type:
    application/xml` on `POST /auth/login` → 415; absent Content-Type + JSON body → 401 with
    a JSON body (`application/json`; lenient JSON contract preserved); cross-site `Origin:
    https://evil.example` on a form login → 403; same-origin bad-cred form login → 401
    re-render (NOT 303), NO session cookie, generic copy; cookie form mutation missing
    `csrf_token` → 403 (double-submit gate); forged `Host: evil.attacker.example` on a magic
    start → link base stays `http://localhost:8082/auth/magic` ("evil" never appears — config-
    only base, Host never participates); forged `X-Forwarded-For: 9.9.9.9` → audit rows
    record only `127.0.0.1` (TRUSTED_PROXY_COUNT=0 ignores raw XFF, so it cannot poison audit
    or rotate limiter keys); a 2 MB form body was refused (connection closed — bounded
    reader). Production negatives are hermetic by design (a memory host cannot boot production):
    the isolated matrix `cmd/server/production_test.go` (`TestProductionNegatives` +
    `TestProductionBaselineConstructs`) PASSES for console→`ErrInsecureDeliveryTransport`,
    http-base→`ErrPublicAuthBaseURLInsecure`, memory-limiter→`ErrNonDurableRateLimiter`,
    nil-keyer→`ErrIdentifierKeyerRequired`, non-durable outbox→`ErrNonDurableDeliveryRepository`,
    unacknowledged worker→`ErrDeliveryWorkerUnacknowledged`; `TestDevelopmentConsoleTransportWarns`
    observes the dev WARN.
11. **default view accessibility/security headers and secret non-echo.** `GET /auth/login`
    (and every HTML page) sets `Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
    `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and a restrictive CSP
    (`default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none';
    script-src 'nonce-…'`). Accessibility: `<label for>`, `aria-describedby` error targets,
    `role="status"`, `autocomplete="email"`/`current-password`/`new-password`/`one-time-code`.
    Secret non-echo: the password `<input>` carries no `value` attribute; a failed form never
    repopulates a password/code/token; no code/token/pepper/unmasked address appears in any
    rendered page or GET URL.
12. **partial page override + separate email template override.** Both drove twice-through and
    are LIVE. Page override: `GET /auth/login` renders the host's branded
    `authpages.Views.Login` (`data-brand="gopernicus-cms"`, title "Sign in — Gopernicus CMS",
    `action="/auth/login"`, same field names) — confirmed both by curl and in a real browser
    (`evidence810.json: loginBranded=true`); it posts the SAME endpoint with the SAME dispatch/
    CSRF/origin/PRG/status policy (the failed-login form re-render above rendered the branded
    page at 401 with no session — presentation-only). Email override: the branded verification
    body ("Your Gopernicus CMS verification code is: …") is delivered by the LayerApp
    `EmailContentTemplates` override on every register — distinct field, distinct subsystem
    from `Views`.

Flagged latent item — DISPOSITION (recovery.templ reset fragment). The bundled reset page
populates its HIDDEN `#reset-token` field ONLY from `window.location.hash.slice(1)` (the whole
fragment) and scrubs history. This host delivers the reset token as a PLAIN token with NO
server-built `#token=` reset link, so the token is carried as a BARE fragment (`/auth/password/
reset#<token>`); `slice(1)` extracts it exactly (real-browser drive: `resetHiddenFieldEqualsBare
Token=true`, `resetHashScrubbed=true`, reset POST 303 → `/auth/login`). The param-parse
inconsistency (whole-fragment vs `URLSearchParams.get("token")`, the 8.7 magic-link bug) is
therefore NEVER HIT by the reset flow — it would only bite a future `#token=`-style reset-link
builder, which does not exist. Per the task rule ("if the reset flow doesn't use fragments in
the protocol, leave it flagged"), and because the flow works correctly as delivered, it is LEFT
FLAGGED (no in-scope fix) for a future recovery-UX task that introduces a `#token=` reset link.

Evidence (scratchpad, secrets redacted): `evidence810.json` (DOM capstone — branded Login,
magic scrub + no-token-in-URL, reset bare-fragment + 303), `proto-server.log` (full server
log incl. branded email, phone notify, per-job delivery lifecycle, reuse WARN),
`av3-8.10-transcript.txt` (redacted transcript excerpts), `drive810.js` (the drive).

Verification (exact commands, observed):
- Live run-and-look (real host boot + curl + real Chromium): all twelve legs as above, both
  transports where applicable; HTML POSTs 303, JSON POSTs JSON.
- Leg-8/9 hermetic: `go test ./internal/logic/delivery/... -run
  'Worker(Retry|Replace|Terminal|Purge|Crash|Contention)…'` and `go test . -run
  'ChallengeProtectorKeyRotation|ChallengeProtectorUnknownKeyID'` — PASS.
- `make generate` — PASS, drift-clean (`updates=0`; no `.templ` touched; git shows no
  `_templ.go`/`.templ` change).
- `cd features/authentication/views/templ && go build ./... && go test ./... && go vet ./...`
  — PASS.
- `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` — PASS (production-
  negative matrix + override tests green).
- **Phase-8 close gate: `make check` — PASS ("all checks passed", every module +
  templ-drift + all 13 guards); `make guard` — PASS (exit 0, all 13 guards; feature core
  still has no `a-h/templ` import).**

Phase-8 acceptance met: all run-and-look legs observed in both transports; API-only
construction remains green with no templ module forced into the host graph (nil Views path
proven hermetically in AV3-8.1/8.3); the default templ host and the partial-override host
(branded Login) both work. Phase 8 is complete.

For AV3-9.1 (final canonical migration audit, `10-docs-and-closeout.md`): the proof host is
memory-backed (`internal/authmem`), so 8.10 exercised NO SQL migration tree — the canonical
pgx/turso migration parity/audit is untouched by this task and remains phase-9 scope. Two
doc-facing notes for the phase-9 documentation task (AV3-9.3), not fixed here: (a) the
`examples/auth-cms/README.md` still documents the auth-v2 A9 curl protocol and its leg-0
verify uses the pre-v3 `{"code":…}` body — the v3 verify needs `{email, code}`, and the whole
v3 HTML/passwordless/account/override surface is undocumented there; (b) the reset page's
hidden-token-from-bare-fragment mechanic (no visible manual token field) is worth a one-line
note if a reset runbook is written. Neither blocks phase 8.
