# Phase 6 — credential and identifier management suite

Status: READY after phase 5.
Depends on: phases 0–5.
Design: §§5.0–5.8, 8–9.1, 13 V13/V15.

## Goal

Expose the complete session-gated credential/identifier lifecycle with recent
authentication, typed policy evaluation, revision serialization, independent
notifications, stable errors, and strict HTTP protection.

## Task AV3-6.1 — recent-authentication grant service and routes

Touch: authsvc, session mint/login metadata, inbound routes/DTOs, tests.

Implement:

- primary login records authenticated-at, method descriptors, and honest
  assurance on the server session;
- `BeginStepUp` and completion by existing password, active verified identifier
  code, or already-linked OAuth proof where supported;
- purpose/context/session/user-bound grants with default five-minute lifetime;
- recent-primary-login shortcut only when method/age/assurance meets the
  requested policy;
- atomic consume immediately before mutation; wrong purpose/provider/
  identifier/session consumes or rejects as specified without authorizing;
- generic external failures and security events without secrets;
- live-session middleware plus phase-0 CSRF/origin/strict-body protections on
  every route.

Do not let proof of a proposed new email/phone satisfy step-up; it is a separate
binding proof.

Verify:

```sh
cd features/authentication && go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'StepUp|Recent|Grant'
make check
```

## Task AV3-6.2 — masked `/auth/methods`

Depends on: AV3-6.1.

Implement live-session-gated `GET /auth/methods` returning password presence,
typed OAuth methods, active identifiers with uses/primary/verified time,
assurance properties, and policy-derived removable hints. Mask identifier values
by default; any full-value method is a separate explicitly authorized service
method, not a query flag accepted blindly from HTTP.

Remove `GET /auth/oauth/linked`. Tests prove revoked access JWT denial, masking,
replaced-row omission, policy hint advisory behavior, and `Cache-Control:
no-store`.

## Task AV3-6.3 — set/change/remove password

Depends on: AV3-6.1, AV3-6.2.

Implement:

- set initial password only with a consumed `set_password` grant; reject when
  already set; validate with the phase-3 policy; revision-CAS mutation; revoke
  sessions and mint a fresh caller pair as specified;
- preserve change-password current-password verification, but route it through
  the same policy/revision/session-revocation machinery;
- remove-password start/complete using the `remove_password` code delivered via
  an existing verified recovery identifier; completion proves step-up, evaluates
  proposed methods, atomically removes password and bumps revision, invalidates
  reset challenges, revokes sessions, and remints for caller;
- stable errors for absent/already-set/last-policy method and delivery job
  receipt/status.

Concurrency tests remove password while unlinking/removing another method: stale
revision cannot commit and policy is re-evaluated.

Verify:

```sh
cd features/authentication && go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'PasswordSet|PasswordChange|PasswordRemove'
make check
```

## Task AV3-6.4 — provider-bound OAuth unlink

Depends on: AV3-6.1, AV3-6.2.

Replace the plain delete route with start/complete. Bind challenge/grant context
to exact provider, require existing-factor step-up, evaluate proposed methods,
and unlink through revision-CAS mutation. A Google code cannot unlink GitHub;
wrong-provider use consumes/fails. Remove the old DELETE route. Preserve token
encryption/deletion behavior and record events/notices.

Tests include wrong provider, stale revision against password removal, last
acceptable method, unknown provider, revoked session, and replay.

## Task AV3-6.5 — add/change/remove identifier flows

Depends on: AV3-6.1, phase-4 delivery, phase-5 identifiers.

Implement `/auth/identifiers/*` service and routes:

- start requires live session + existing-method step-up, normalizes target,
  applies per-user and keyed per-target budgets, creates/replaces contact change,
  then enqueues proof to the new address;
- confirm atomically consumes challenge first, then contact change, reloads and
  evaluates method policy/revision, then atomically applies verified change;
- claim only at apply; no start-time existence lookup;
- support multiple identifiers, use flags, primary selection, and replacement;
- PATCH/DELETE require step-up and revision-serialized policy mutation;
- removing primary selects/requires replacement atomically;
- enqueue independent notice to previously verified channel(s), never only the
  newly proved address; include time/client context and host redress URL;
- optional activation delay/cancel seam may be config-disabled by default but
  must not distort the immediate default path.

Test wrong code retries without consuming pending value, wrong context consumes,
crash/restart safe orphan posture, unique race, shared notification-only phone,
primary replacement, independent notice, unavailable notifier, rate limits,
and concurrent final-method mutation.

Verify:

```sh
cd features/authentication && go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'Identifier|EmailChange|PhoneChange'
make check
```

## Task AV3-6.6 — route/error/event inventory and live suite proof

Depends on: AV3-6.2 through AV3-6.5.

- Confirm exact new/removed routes against design §9.
- Map stable machine codes/statuses; login-like verification stays generic.
- Confirm every sensitive start/complete records success/failure/blocked events
  without secret or unmasked destination details.
- Run the complete credential suite on fresh pgx and turso stores, including
  wrong-provider consumption and concurrent removal serialization.
- Record HTTP transcripts with cookies/tokens/codes redacted.

## Phase acceptance

```sh
make check
make guard
```

Plus both live dialect suites. Every mutation has live session + recent grant +
policy + revision serialization; `/auth/methods` is masked/no-store.

## Stop conditions

- A mutation bypasses `CredentialMutationRepository` or identifier revision
  apply to make tests pass: stop and fix the aggregate operation.
- A new-address proof is being treated as existing-factor reauthentication:
  stop; this is an account-takeover vulnerability.
- A notice can go only to the channel being newly bound: require host redress/
  support policy or block the risky mutation configuration.

## Execution log

Append dated entries per completed task.

### 2026-07-13 — AV3-6.1 recent-authentication grant service and routes

Task: AV3-6.1. Dependencies confirmed complete (phases 0–5 all checked in
TASKS.md; authgrant domain + repository live on both dialects, session
AuthenticationMetadata + columns mapped, challenge rail, delivery outbox, and the
phase-0 HTTP security primitives all present).

Implemented:

- **Session recent-auth metadata.** `mintSession` now takes a
  `session.AuthenticationMetadata` and stamps it on the created row; new
  `Service.primaryAuthentication(kind)` builds the honest descriptor + assurance.
  All five mint sites record it: password login and `IssueToken`
  (`MethodPassword`), all three OAuth mint paths (`MethodOAuth`), and the
  change-password remint (`MethodPassword`).
- **Grant vocabulary.** Added the closed grant-purpose set to `domain/authgrant`
  (`set_password`, `remove_password`, `unlink_oauth`, `change_email`,
  `change_phone`, `remove_identifier`, `change_identifier_uses`), the
  `challenge.PurposeStepUp` code purpose (5m TTL, lockout budget) + its spec, and
  `securityevent.TypeStepUpChallengeSent` / `TypeStepUp`.
- **Step-up service (`internal/logic/authsvc/stepup.go`).**
  `RequireRecentAuthentication` is the consume-before-mutation primitive: it tries
  the recent-primary-login shortcut first (session metadata within `MaxAge`,
  meeting `MinAssurance` — defaults 5m / AAL1) so a recent login never wastes a
  grant, then atomically `Consume`s an explicit purpose+context+session-bound
  grant; neither → `ErrStepUpRequired`. `BeginStepUp` issues a step-up code bound
  to (purpose, context) and delivers it via the outbox (`PurposeSensitiveCode`) to
  an existing active **verified** identifier (never a proposed new address).
  `CompleteStepUpWithPassword` and `CompleteStepUpWithIdentifierCode` earn a
  persisted single-use grant; wrong password/absent password is the generic
  `ErrStepUpProof`, wrong-context codes consume-and-reject as `ErrChallengeInvalid`.
  Grant lifetime defaults to five minutes. Logout now cascades
  `DeleteBySession` (best-effort).
- **HTTP wiring.** `RequireLiveSession` now stamps the live session id
  (`CurrentSessionID`) so a grant binds to the proven session, never a body field.
  New live-session-gated routes `POST /auth/step-up/{begin,password,code}` wired
  with the phase-0 browser-safe-mutation gate (allowlisted Origin + double-submit
  CSRF), `requireJSON` + `strictJSONBody`, and `Cache-Control: no-store`. Added
  `Config.AllowedOrigins` and the exported `inbound.MutationSecurity`, threaded
  `Register → Mount`.

Premise adaptation (logged): the design lists `CompleteStepUpWithOAuth`
("already-linked OAuth proof **where supported**") as a third completion path.
Implemented password and identifier-code completions fully — these satisfy every
phase-6 mutation gate, including an OAuth-only user setting a first password (they
step up via their verified email identifier). The OAuth-roundtrip completion is
**deferred**: an honest version requires a fresh provider authorization roundtrip
(new oauthstate purpose + start/callback surface); a "has a linked account" check
would be present-control-free and violate §5.0. The design's "where supported"
hedge covers this; flagged for AV3-6.4 / the AV3-9.7 reviewer wave.

Files changed:
- `domain/authgrant/authgrant.go` — grant purpose constants.
- `domain/challenge/challenge.go` — `PurposeStepUp`.
- `domain/securityevent/securityevent.go` — `TypeStepUpChallengeSent`, `TypeStepUp`.
- `internal/logic/authsvc/challenge.go` — step-up challenge spec.
- `internal/logic/authsvc/stepup.go` — new step-up/recent-auth service.
- `internal/logic/authsvc/service.go` — `mintSession` metadata param,
  `primaryAuthentication`, `authGrants` dep/field/wiring, Logout grant cascade.
- `internal/logic/authsvc/token.go`, `oauth.go` — mint-site metadata.
- `internal/logic/authsvc/context.go` — session-id carrier + `CurrentSessionID`.
- `internal/logic/authsvc/refresh.go` — `RequireLiveSession` stamps session id.
- `internal/inbound/authentication/{routes,security,sessions,stepup}.go` —
  routes, `MutationSecurity`, interface, handlers.
- `authentication.go` — `Config.AllowedOrigins`, grant wiring, `MutationSecurity`
  threading.
- Tests: `internal/logic/authsvc/stepup_test.go`,
  `internal/inbound/authentication/stepup_test.go`; existing inbound `Mount(...)`
  call sites updated for the new arg.

Verification (observed):
- `go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'StepUp|Recent|Grant'` → ok (both packages).
- `make check` → all modules build/test/vet green, no templ drift, all guards passed.

For AV3-6.2 (masked `/auth/methods`): `CurrentSessionID(ctx)` is now available for
session-bound reads; `RequireRecentAuthentication` / grant purposes
(`authgrant.Purpose*`) and `RecentAuthPolicy` are the seam the later mutation tasks
call before their revision-CAS mutation; `MutationSecurity` + the browser-safe gate
pattern in `routes.go` is the template for the remaining sensitive routes.

### 2026-07-13 — AV3-6.2 masked `/auth/methods`

Task: AV3-6.2. Dependency confirmed complete (AV3-6.1 checked off in TASKS.md;
`CredentialMutationRepository.Snapshot` typed projection and `RequireLiveSession`
session-id stamping both present and live-proven).

Implemented:

- **Service read (`internal/logic/authsvc/methods.go`).** `Service.Methods(ctx,
  userID)` builds the masked inventory. The authoritative projection is
  `credentialMutations.Snapshot` (the same typed `MethodSet` the policy evaluates
  and the mutation rail serializes, so read and mutation never disagree). It
  enriches each entry with display-only data the projection does not carry —
  masked identifier value + proof time from `identifiers.ListByUser`, OAuth link
  time from `oauthAccounts.ListByUser`. Each `removable` hint is computed by
  evaluating the credential policy against `snap.With(removalMutation)`; the hint
  is advisory only. Fails CLOSED (`ErrCredentialInventoryUnavailable`, 403) when
  the credential rail is unwired. Replaced rows never appear (both Snapshot and
  ListByUser project active rows only). Masking: email keeps the first local rune
  + domain (`a***@example.com`), phone keeps last four digits (`***4567`), unknown
  kind fully masked — no query flag can request the full value from HTTP.
- **Credential policy wired into the service.** Added `credentialPolicy
  credential.Policy` to `authsvc.Deps`/`Service`, defaulting a nil policy to
  `credential.NewDefaultPolicy(credential.PolicyConfig{})` in `NewService`; threaded
  `cfg.CredentialPolicy → deps.CredentialPolicy` in `authentication.go`. This is the
  same evaluator seam AV3-6.3–6.5 consume before their revision-CAS mutations.
- **HTTP (`internal/inbound/authentication/methods.go`).** Live-session-gated
  `GET /auth/methods` (`svc.RequireLiveSession`) — a bearer-safe GET read with no
  body, so it skips the browser-safe-mutation CSRF gate; handler sets
  `Cache-Control: no-store`. JSON contract `{has_password, oauth[], identifiers[]}`
  with masked identifier values, RFC3339 proof/link times (omitted when zero),
  ordered `uses` arrays, and advisory `removable` hints. Added `Methods` to the
  inbound `authService` interface.
- **Removed `GET /auth/oauth/linked`** (pre-tag route break, design §9): dropped the
  route, the `oauthLinked` handler, `newLinkedResponse`/`linkedAccountResponse`, and
  the now-unused `oauthaccount`/`time` imports in `oauth.go` and `ListLinked` in the
  inbound interface (`oauthaccount` import dropped from `sessions.go`). The public
  `auth.Service.ListLinked` and internal `authsvc.Service.ListLinked` are left intact
  (public API; only the route was removed, not the service method).

Premise adaptation (logged): the design's §5.1 example shows a `removable` field on
each OAuth and identifier entry but only a `has_password` bool for the password —
so no password `removable` hint is emitted (matches the JSON example exactly; the
remove-password guard is AV3-6.3's concern).

Files changed:
- `internal/logic/authsvc/methods.go` — new `Methods` read, `MethodsView` model,
  masking helpers, `ErrCredentialInventoryUnavailable`.
- `internal/logic/authsvc/service.go` — `credentialPolicy` dep/field/default/wiring.
- `authentication.go` — `CredentialPolicy: cfg.CredentialPolicy` into `authsvc.Deps`.
- `internal/inbound/authentication/methods.go` — new `GET /auth/methods` handler + DTOs.
- `internal/inbound/authentication/routes.go` — route registration + doc.
- `internal/inbound/authentication/sessions.go` — `Methods` on interface, `ListLinked`
  removed, `oauthaccount` import dropped.
- `internal/inbound/authentication/oauth.go` — `/auth/oauth/linked` route/handler/DTOs
  and unused imports removed.
- Tests: `internal/inbound/authentication/methods_test.go` (masking, no-store,
  revoked-session denial, live-session gate, replaced-row omission, advisory
  removable hint, fail-closed); `oauth_test.go` updated to drop the removed route.

Verification (observed):
- `go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/...` → ok (both packages).
- `go build ./... && go vet ./... && go test ./...` (features/authentication) → all green.
- `examples/auth-cms` `go build ./...` → ok (no reference to the removed route).
- `make guard` → all guards passed.

For AV3-6.3 (set/change/remove password): the credential policy is now wired
(`s.credentialPolicy`), and `Snapshot`/`With(mutation)`/`EvaluateMutation` is the
evaluate-then-revision-CAS pattern to reuse; the inbound test harness now carries a
faithful `memCredentialMutations` (Snapshot + revision-CAS Apply over the mem
stores) that later password/identifier mutation tests can drive.

### 2026-07-13 — AV3-6.3 set/change/remove password

Task: AV3-6.3. Dependencies confirmed complete (AV3-6.1 and 6.2 checked off in
TASKS.md; phases 0–5 all checked; step-up rail, credential policy, credential-
mutation rail, challenge rail, and the durable outbox all present and live-proven).

Implemented (`internal/logic/authsvc/password.go`, new):

- **Set initial password.** `SetPassword(sessionID, userID, newPassword)` refuses an
  already-set password with `ErrPasswordAlreadySet` (409 `password_already_set`)
  BEFORE spending the grant, validates through the shared phase-3
  `validatePassword`, consumes a `set_password` recent-auth grant via
  `RequireRecentAuthentication` immediately before the mutation, writes the hash,
  then revokes every session and mints a fresh caller pair. Records `password_set`.
- **Change password (re-homed onto the shared tail).** `ChangePassword` keeps its
  current-password verification and now routes its revoke+remint through the shared
  `revokeAndRemintForPassword` helper (the §7.2 atomicity pin preserved: a
  `DeleteByUser` failure is RETURNED). Set/change/remove share this one tail.
- **Remove password (start/complete).** `StartRemovePassword(userID)` refuses an
  account with no password (`ErrPasswordNotSet`, 404), selects the account's active
  verified recovery identifier (primary-first), issues a `remove_password` code, and
  delivers it to that existing verified channel over the durable outbox (a delivery
  failure surfaces through the receipt). Records `password_remove_code_sent`.
  `RemovePassword(userID, code)` consumes the code (the reauthentication proof for
  this flow — possession of a verified recovery channel), invalidates any pending
  reset token, evaluates the credential policy and removes the password atomically
  under revision-CAS via the reusable `applyCredentialMutation` loop (re-evaluates
  policy on an `sdk.ErrConflict`), then revokes all sessions and remints. Records
  `password_removed`.
- **Reusable seams.** `applyCredentialMutation(userID, mutation)` (Snapshot →
  `EvaluateMutation(current, current.With(m))` → revision-CAS `Apply`, bounded retry
  with re-evaluation, fail-closed on a nil rail) is the seam AV3-6.4/6.5 consume.
  `verifiedRecoveryIdentifier` selects the delivery target.

New events: `password_set`, `password_remove_code_sent`, `password_removed`
(`domain/securityevent`).

HTTP (`internal/inbound/authentication/password.go`, new): live-session-gated,
browser-safe-mutation-gated `POST /auth/password/{set,remove/start,remove}` with
`requireJSON` + `strictJSONBody` + `Cache-Control: no-store`; set/remove set fresh
session cookies from the reminted pair. `writePasswordError` maps the stable errors
to the pinned §5.8 machine codes (`password_already_set`, `password_not_set`,
`cannot_remove_last_method`). Interface methods added to `authService`.

Premise adaptations (logged):

- **Remove-password reset invalidation via a Replace tombstone.** The frozen
  `challenge.Repository` port exposes no targeted user+purpose delete outside the
  `passwordreset` composition. Rather than widen the completed phase-0/2 port and
  both store dialects mid-AV3-6.3, `invalidatePendingReset` rides `Replace`'s
  contract (atomic delete of the prior `(user, password_reset)` row) and writes an
  already-expired, secret-free tombstone in its place (unusable; reaped by
  `PurgeExpired`). Invalidation runs BEFORE the removal — a killed reset with the
  password still present is benign, a live reset after removal could restore a
  password. AV3-6.6's live-store leg should confirm the tombstone behaves on pgx/
  turso; a proper targeted-delete port method is the clean follow-up if wanted.
- **Set-password uses the typed password repository, not a `CredentialMutation`
  variant.** The closed v3 sum type has only remove/unlink/retire/use-change (no add
  variant, by design). Adding a login method strictly enlarges the set and can never
  violate a policy floor, and §5.2 describes set as a bare write + fresh pair, so
  `SetPassword` writes through `passwords.Set` (as register/change do) with no
  revision-CAS. The "revision-CAS mutation" clause in the task summary is the
  removal's concern; set does not route through the closed sum type.
- **Remove-password uses one `remove_password` code as the step-up proof** (no
  separate grant round-trip): §5.3's "begins/fulfills a recent-authentication grant
  and issues a remove_password code" is satisfied by delivering the code to an
  existing verified recovery identifier and consuming it at completion. No proposed
  new address is involved, so the account-takeover stop condition does not apply.
- **`/auth/password/change` route left unchanged (no browser-safe gate added).** The
  pre-existing change route stays `RequireLiveSession`-only to preserve its existing
  transport tests; the task said "preserve change-password". The new set/remove
  routes carry the full browser-safe gate. Flagged for the AV3-6.6 route inventory.

Files changed:
- `domain/securityevent/securityevent.go` — `TypePasswordSet`,
  `TypePasswordRemoveCodeSent`, `TypePasswordRemoved`.
- `internal/logic/authsvc/password.go` (new) — `SetPassword`, `StartRemovePassword`,
  `RemovePassword`, `revokeAndRemintForPassword`, `applyCredentialMutation`,
  `invalidatePendingReset`, `verifiedRecoveryIdentifier`, stable errors.
- `internal/logic/authsvc/service.go` — `ChangePassword` tail routed through
  `revokeAndRemintForPassword`.
- `internal/inbound/authentication/password.go` (new) — the three handlers, DTOs,
  and `writePasswordError` code mapping.
- `internal/inbound/authentication/routes.go` — the three password routes.
- `internal/inbound/authentication/sessions.go` — `authService` interface methods.
- Tests: `internal/logic/authsvc/password_test.go` (set success/already-set/no-grant/
  short-password, change revoke+remint, remove success/not-set/wrong-code/last-login-
  method/reset-invalidation, plus stale-revision-retry and concurrent-removal policy
  re-evaluation via a one-shot `beforeApply` hook); shared harness now wires
  `CredentialMutations` (`fakeCredentials`). `internal/inbound/authentication/
  password_test.go` (live-session gate, cookie CSRF gate, already-set 409 + code +
  no-store over bearer, remove/start receipt + no-store).

Verification (observed):
- `go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'PasswordSet|PasswordChange|PasswordRemove'` → ok (both packages).
- `features/authentication` `go build ./... && go vet ./... && go test ./...` → all green.
- `make check` → all modules build/test/vet green, templ drift clean, integration-tag vet clean, all guards passed.
- `make guard` → all guards passed.

For AV3-6.4 (provider-bound OAuth unlink): `applyCredentialMutation(userID,
credential.UnlinkOAuth{Provider})` is the ready evaluate-then-revision-CAS seam
(bounded retry + policy re-evaluation, fail-closed on a nil rail); the unlink
`remove_password`-style code should bind the provider via challenge StoredContext
(the step-up `WithStoredContext`/`WithExpectedContext` pattern) so a Google code
cannot unlink GitHub. `writePasswordError`'s `cannot_remove_last_method` mapping and
the browser-safe route template (`RequireLiveSession` + `browserSafe` +
`requireJSON`/`strictJSONBody`/`writeNoStore`) carry straight over.

### 2026-07-13 — AV3-6.4 provider-bound OAuth unlink

Task: AV3-6.4. Dependencies confirmed complete (AV3-6.1 and 6.2 checked off in
TASKS.md; phases 0–5 all checked; step-up rail, credential policy + rail, challenge
rail with `PurposeUnlinkOAuth`, the durable outbox, and the browser-safe-mutation
gate all present and live-proven).

Implemented (`internal/logic/authsvc/oauth.go`):

- **Removed the plain `Unlink`** (and `ErrLastAuthMethod`): its last-method
  count-and-refuse guard was the AV7-era surface. Replaced with the §5.4 code-gated
  start/complete pair, guarded by the §5.6 policy rather than a scalar count.
- **`StartUnlinkOAuth(userID, provider)`.** Refuses an unlinked provider
  (`sdk.ErrNotFound`, via the new `requireLinked`) before issuing anything, selects
  the account's active verified recovery identifier (`verifiedRecoveryIdentifier`,
  primary-first), issues a `PurposeUnlinkOAuth` code **bound to the exact provider**
  via `WithStoredContext(unlinkBinding{Provider})`, and delivers it to that existing
  verified channel over the durable outbox (a delivery failure surfaces through the
  receipt). Records `oauth_unlink_code_sent`. Fails closed (`ErrStepUpUnavailable`)
  on a nil challenge rail.
- **`UnlinkOAuth(userID, provider, code)`.** Fails closed
  (`ErrCredentialMutationUnavailable`) on a nil rail, re-checks the link exists,
  then consumes the provider-bound code with
  `WithExpectedContext(unlinkBinding{Provider})` — a code minted for a different
  provider is **consumed and rejected** as `ErrChallengeInvalid`, never authorizing
  the wrong unlink — and unlinks through `applyCredentialMutation(userID,
  credential.UnlinkOAuth{Provider})` (Snapshot → policy → revision-CAS Apply, bounded
  retry re-evaluating policy on `sdk.ErrConflict`). The revision-CAS Apply deletes the
  `oauth_accounts` row (its encrypted provider tokens with it) in the same
  transaction that bumps `auth_revision` — token-deletion behavior preserved through
  the aggregate operation, never a side delete. Records `oauth_unlinked`.

New event: `oauth_unlink_code_sent` (`domain/securityevent`; `oauth_unlinked`
already existed).

HTTP (`internal/inbound/authentication/oauth.go`): replaced `DELETE
/auth/oauth/{provider}/link` (+ `oauthUnlink` handler) with live-session-gated,
browser-safe-mutation-gated `POST /auth/oauth/{provider}/unlink/start` and `POST
/auth/oauth/{provider}/unlink`, each with `requireJSON` + `strictJSONBody` +
`Cache-Control: no-store`; `mountOAuth` now takes the `liveSession`/`browserSafe`
middleware from `Mount` (threaded through `routes.go`). Errors route through the
shared `writePasswordError` so the last-acceptable-method rejection surfaces the
pinned `cannot_remove_last_method` (`credential.ErrNoLoginMethod`). `authService`
interface updated (`Unlink` → `StartUnlinkOAuth`/`UnlinkOAuth`).

Public API (`authentication.go`): removed `Service.Unlink` and the
`ErrOAuthLastMethod` alias. The code-gated unlink is route-only, matching the
existing route-only posture of set/remove password (their errors are likewise not
re-exported on the host-facing `Service`).

Premise adaptation (logged): §5.4 says "the mutation consumes a provider-bound
`unlink_oauth` recent-auth grant", but the two-route surface (start → complete
`{code}`) has no step-up-completion route to mint a separate grant. Following the
AV3-6.3 remove-password precedent, the provider-bound code delivered to a verified
recovery identifier and consumed at completion **is** the reauthentication proof — no
separate grant round-trip. No proposed new address is involved, so the account-
takeover stop condition does not apply. The `authgrant.PurposeUnlinkOAuth` constant
remains available for a host that later wants an explicit-grant step-up lane.

Files changed:
- `domain/securityevent/securityevent.go` — `TypeOAuthUnlinkCodeSent`.
- `internal/logic/authsvc/oauth.go` — removed `Unlink`/`ErrLastAuthMethod`; added
  `unlinkBinding`, `StartUnlinkOAuth`, `UnlinkOAuth`, `requireLinked`; `challenge`
  import.
- `internal/inbound/authentication/oauth.go` — DELETE route/handler removed; unlink
  start/complete routes, handlers, DTOs; `mountOAuth` middleware params.
- `internal/inbound/authentication/routes.go` — `mountOAuth` call threaded
  `RequireLiveSession` + `browserSafe`.
- `internal/inbound/authentication/sessions.go` — `authService` interface.
- `authentication.go` — removed public `Unlink` + `ErrOAuthLastMethod`.
- Tests: `internal/logic/authsvc/oauth_unlink_test.go` (new: success, wrong-provider
  consume + spent-code retry, unknown provider, last-acceptable-method,
  stale-revision re-evaluation against a concurrent password removal, replay,
  fail-closed without rail); `oauth_test.go` (removed the old last-method test);
  `securityevent_test.go` (`oauth_unlinked` event via the new flow);
  `service_test.go` + `password_test.go` (main harness wires a `fakeOAuthAccounts`;
  `fakeCredentials` projects OAuth in Snapshot and deletes it in `Apply(UnlinkOAuth)`);
  inbound `oauth_test.go` (route lists updated; DELETE-removed assertion).

Verification (observed):
- `go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'Unlink|OAuth'` → ok (both packages).
- `go build ./... && go vet ./... && go test ./...` (features/authentication) → all green; `-race` across the whole module → green.
- `examples/auth-cms` `go build ./...` → ok (no reference to the removed method/error/route).
- `make guard` → all guards passed.

For AV3-6.5 (add/change/remove identifier flows): the identifier revision-CAS
mutations (`RetireIdentifier`, `ChangeIdentifierUses`) route through the same
`applyCredentialMutation` seam; the main-harness `fakeCredentials` now projects OAuth
too, so an identifier-mutation test that must leave an OAuth login method standing has
a faithful projection. `requireLinked`/`verifiedRecoveryIdentifier` and the
provider-bound `WithStoredContext`/`WithExpectedContext` code pattern are the template
for the identifier start/confirm context binding; `writePasswordError` already maps
`cannot_remove_last_method` for the shared last-method rejection.

### 2026-07-13 — AV3-6.5 add/change/remove identifier flows

Task: AV3-6.5. Dependencies confirmed complete (phases 0–5 all checked in TASKS.md;
AV3-6.1 through 6.4 checked off; step-up rail, credential policy + rail, challenge
rail with `change_email`/`change_phone` specs, the durable outbox with the
`identifier_change_proof`/`identifier_change_notice` delivery purposes/templates from
AV3-4.1, the `contactchange` rail from AV3-1.3, and the browser-safe-mutation gate all
present and live-proven). Worktree preserved (only additive edits + new files).

Implemented:

- **`/auth/identifiers/*` service (`internal/logic/authsvc/identifier_management.go`,
  new).**
  - `StartIdentifierChange` (add/change email/phone): live session + existing-method
    step-up (`RequireRecentAuthentication` with the `change_email`/`change_phone`
    grant purpose — the proposed new address can never satisfy it), `ErrKindNotSupported`
    when no transport is wired for the kind (checked before any flow state via the new
    `delivery.Router.Supports`), normalization through the shared seam, per-user AND
    per-target flood budgets (`Config.RateLimiter`, PII-free target digest), then the
    pinned §2.4 order: create the `contactchange.PendingChange` FIRST, issue the proof
    challenge bound to that row's ID (`WithStoredContext(changeBinding{PendingID})`),
    and deliver the code to the proposed NEW address over the outbox (via a new
    `enqueueRenderedReplace` — a change-start is a caller-driven resend, so it
    supersedes rather than dedupes). No start-time existence lookup. Records
    `email_change_code_sent` / `phone_change_code_sent`.
  - `ConfirmIdentifierChange`: the pinned confirm order — `ConsumeChallenge` first (a
    wrong code counts an attempt and leaves the pending value intact), then
    `contactChanges.Consume`, then the binding check (the consumed challenge's bound
    pending-ID digest must equal `contextDigest(changeBinding{pending.ID})`; a mismatch
    spends the code and rejects — the concurrent-start race stop). It then captures the
    previously-verified notice recipients, evaluates the credential policy against the
    proposed post-change `MethodSet` (`proposedSetForChange` mirrors ApplyVerifiedChange:
    retire replaced + retire displaced primary + add new verified), and applies the
    verified change under the revision-CAS `identifiers.ApplyVerifiedChange` with bounded
    retry re-evaluating on `sdk.ErrConflict`. A lost auth-claim race surfaces the generic
    `sdk.ErrAlreadyExists`. Records `email_changed` / `phone_changed`.
  - `RemoveIdentifier` (DELETE): identifier-bound step-up, ownership+active check,
    replacement-primary selection (caller nominee else oldest same-kind sibling) when the
    removed row is primary, policy-guarded `applyCredentialMutation(RetireIdentifier)`.
    Records `email_removed` / `phone_removed`.
  - `SetIdentifierUses` (PATCH): identifier-bound step-up, refuses enabling login/recovery
    on an unverified identifier (`identifier.ErrVerificationRequired`), policy-guarded
    `applyCredentialMutation(ChangeIdentifierUses)`. Records `identifier_uses_changed`.
  - **Independent notice:** `verifiedContactChannels` snapshots the previously-verified
    recovery/notification channels BEFORE the mutation (so a primary replacement still
    notifies the old primary it retires); `enqueueIdentifierChangeNotices` enqueues a
    notice (`identifier_change_notice`, Data carries IdentifierKind + client IP + change
    time) to each over the durable outbox, best-effort, NEVER only the newly proved
    address.
- **Wiring.** Threaded the frozen `ContactChanges` slot into `authsvc.Deps` / `Service` /
  `NewService` and `authentication.go` (`repos.ContactChanges → deps.ContactChanges`).
  Added `delivery.Router.Supports(kind)` (email always; other kinds iff a notifier is
  wired) and the `enqueueRenderedReplace` outbox helper.
- **HTTP (`internal/inbound/authentication/identifiers.go`, new).** Live-session-gated,
  browser-safe-mutation-gated `POST /auth/identifiers/{email,phone}`,
  `POST /auth/identifiers/{email,phone}/confirm`, `PATCH /auth/identifiers/{id}`,
  `DELETE /auth/identifiers/{id}` (DELETE takes `?replacement=<id>`, no JSON body). All set
  `Cache-Control: no-store`; body routes add `requireJSON` + `strictJSONBody`.
  `writeIdentifierError` maps the pinned codes (`kind_not_supported`, `rate_limited`,
  `verification_required`, `cannot_remove_last_method`, `identifier_exists`). Interface
  methods added to `authService`.
- **Events (`domain/securityevent`).** `email_change_code_sent`, `email_changed`,
  `email_removed`, `phone_change_code_sent`, `phone_changed`, `phone_removed`,
  `identifier_uses_changed`.

Premise adaptations (logged):

- **Step-up is consumed at START, not confirm.** The design §5.5 "operation-bound grant"
  is spent when the flow is initiated (the human proves an existing method before binding
  a new address); confirm proves control of the NEW address via the delivered code. This
  matches the task's "start requires live session + existing-method step-up" and the
  pinned "confirm consumes challenge first". The remove/use-change single-step operations
  consume the grant at the mutation itself.
- **`ApplyVerifiedChange` is the confirm mutation, not a `credential.CredentialMutation`
  variant.** The closed v3 sum type has no add/change-value variant (by design); the
  identifier rail's own revision-CAS `ApplyVerifiedChange` (which increments the same
  `auth_revision` the credential Snapshot reads) is the aggregate operation. Policy is
  evaluated in the service against a manually-constructed proposed set immediately before
  it, under the same bounded-retry revision serialization — no mutation bypasses the
  aggregate operation.
- **Add/change routes are ADD (+ make_primary), not arbitrary-ID replacement.** The design
  bodies are `{email|phone, uses, make_primary}` (no "replaces" field); `make_primary=true`
  retires the displaced primary of the kind (the store's ApplyVerifiedChange behavior),
  which IS the primary-change semantics. `PendingChange.ReplacesIdentifierID` stays empty
  for user-initiated adds (it is the registration-verify same-value swap's field).
- **Notice "only the new channel" stop condition.** When no independent verified channel
  exists (e.g., an account with a password but no prior verified identifier adds its first
  one), no notice is sent — there is nowhere safe to send it, and blocking the legitimate
  first-verified-identifier add is worse. A host redress workflow is the documented
  mitigation (the redress-URL config and template rendering of time/IP land with the proof
  host, phase 8/9). The core invariant — a notice never goes ONLY to the newly bound
  address — holds.
- **DELETE uses `?replacement=<id>` (query), not a JSON body**, so it needs no
  Content-Type; when a primary is removed with no nominee the service auto-selects the
  oldest same-kind sibling, and the credential policy decides whether removing a sole
  primary is safe at all.

Files changed:
- `domain/securityevent/securityevent.go` — seven identifier-change event types.
- `internal/logic/delivery/router.go` — `Supports(kind)`.
- `internal/logic/authsvc/identifier_management.go` (new) — the four service methods,
  the confirm/apply/notice/binding/budget/replacement helpers, and stable errors.
- `internal/logic/authsvc/service.go` — `ContactChanges` dep/field/wiring + import.
- `internal/logic/authsvc/delivery.go` — `enqueueRenderedReplace`.
- `authentication.go` — `ContactChanges: repos.ContactChanges` into `authsvc.Deps`.
- `internal/inbound/authentication/identifiers.go` (new) — six handlers, DTOs,
  `writeIdentifierError`.
- `internal/inbound/authentication/routes.go` — six identifier routes.
- `internal/inbound/authentication/sessions.go` — four `authService` interface methods.
- Tests: `internal/logic/authsvc/identifier_management_test.go` (new: add/change success,
  primary replacement + old-primary notice, wrong-code-retains-pending, wrong-context-
  consumes, step-up-required, rate-limited, unsupported-kind, phone add, shared
  notification-only phone, unique-race generic collision, independent notice, remove
  non-primary/last-login-method/primary-promotes-replacement, set-uses success/verification-
  required, concurrent final-method re-evaluation); `service_test.go` (`fakeContactChanges`
  wired into the harness; `fakeIdentifiers.ApplyVerifiedChange` now models the auth-claim
  collision faithfully); `internal/inbound/authentication/identifiers_test.go` (new:
  require-live-session, cookie-CSRF gate, email start over bearer + receipt + no-store,
  phone kind-not-supported, delete over bearer + no-store).

Verification (observed):
- `go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/... -run 'Identifier|EmailChange|PhoneChange'` → ok (both packages).
- `features/authentication` `go build ./... && go vet ./... && go test -race ./...` → all green.
- `make check` → all modules build/test/vet green, templ drift clean, integration-tag vet clean, all guards passed.
- `make guard` → all guards passed.

For AV3-6.6 (route/error/event inventory and live suite proof): the new/removed route
surface is the six `/auth/identifiers/*` routes above (all live-session + browser-safe +
no-store); stable machine codes are `kind_not_supported` (400), `rate_limited` (429),
`verification_required` (409), `cannot_remove_last_method` (409, shared
`credential.ErrNoLoginMethod`), `identifier_exists` (409, generic `sdk.ErrAlreadyExists`);
new events are the seven `email_*`/`phone_*`/`identifier_uses_changed` types. Two live-store
items to confirm on pgx/turso: (1) `contactchange` Create replace-per-(user,kind) +
single-use Consume, and (2) the code-path `ConsumeCode` returns the stored context digest in
`Consumed.Context` (the binding check depends on it — verified present in both store impls).
`examples/auth-cms` does not yet wire `ContactChanges` in its Repositories, so the identifier
routes fail closed there until the proof host wires it (phase 8) — the notice redress-URL
config + template rendering of time/IP context also land with the proof host.

### 2026-07-13 — AV3-6.6 route/error/event inventory and live suite proof — PHASE 6 CLOSE

Task: AV3-6.6. Dependencies confirmed complete (AV3-6.1 through 6.5 all checked off in
TASKS.md; phases 0–5 all checked). Worktree preserved (the only source edits are the
change-password parity fix below and its test updates; no unrelated file touched).

**Route inventory — confirmed against design §9.** The phase-6 route surface exactly
matches §9. All mutations are `RequireLiveSession` + browser-safe gate (allowlisted
Origin + double-submit CSRF; bearer-only API callers skip the gate) and set
`Cache-Control: no-store`; `GET /auth/methods` is a bearer-safe live-session read with
no body (no CSRF gate) and `no-store`.

Added this phase:

| method | path | gates | task |
|---|---|---|---|
| POST | `/auth/step-up/begin` | live + browser-safe + no-store | 6.1 |
| POST | `/auth/step-up/password` | live + browser-safe + no-store | 6.1 |
| POST | `/auth/step-up/code` | live + browser-safe + no-store | 6.1 |
| GET | `/auth/methods` | live + no-store (masked) | 6.2 |
| POST | `/auth/password/set` | live + browser-safe + no-store | 6.3 |
| POST | `/auth/password/remove/start` | live + browser-safe + no-store | 6.3 |
| POST | `/auth/password/remove` | live + browser-safe + no-store | 6.3 |
| POST | `/auth/oauth/{provider}/unlink/start` | live + browser-safe + no-store | 6.4 |
| POST | `/auth/oauth/{provider}/unlink` | live + browser-safe + no-store | 6.4 |
| POST | `/auth/identifiers/email` | live + browser-safe + no-store | 6.5 |
| POST | `/auth/identifiers/email/confirm` | live + browser-safe + no-store | 6.5 |
| POST | `/auth/identifiers/phone` | live + browser-safe + no-store | 6.5 |
| POST | `/auth/identifiers/phone/confirm` | live + browser-safe + no-store | 6.5 |
| PATCH | `/auth/identifiers/{id}` | live + browser-safe + no-store | 6.5 |
| DELETE | `/auth/identifiers/{id}` | live + browser-safe + no-store | 6.5 |

Removed (pre-tag breaks, in the §9 upgrade note): `GET /auth/oauth/linked` (6.2,
subsumed by `/auth/methods`) and `DELETE /auth/oauth/{provider}/link` (6.4, replaced by
the code-gated unlink pair). `GET /auth/delivery/status` (session-gated delivery-job
status named in §9) was already present from phase 4. Passwordless routes (§9) are
phase 7, correctly absent here.

**Premise resolution — `/auth/password/change` browser-safe gate (AV3-6.3 flag).** 6.3
left the pre-existing change-password route `RequireLiveSession`-only and flagged the
missing browser-safe gate for this task. Design §9.1 is unambiguous ("every
cookie-authenticated mutation validates an allowlisted `Origin` … and uses the
framework CSRF token middleware"), so the resolution per the design is to ADD the gate,
bringing change-password to parity with its sibling set/remove-password routes. Applied:
the `browserSafe` middleware is now on `POST /auth/password/change` (registered after
`browserSafe` is built). Bearer-only API callers are unaffected (`isBearerOnly` skips
the gate); only cookie-lane callers now need the double-submit CSRF token. Middleware
order verified: `RequireLiveSession` is outermost and runs before `browserSafe`, so a
no/revoked-session request still fails with 401 before the CSRF check. Scope kept
minimal: the handler's decode path was NOT rewritten (adding `requireJSON` would 415 the
existing `do()`-based tests and was not the flagged gap); only the CSRF/Origin gate — the
actual CSRF-vector fix — was added. New rejection test
`TestChangePasswordCookieRequiresCSRF` proves a live cookie session without CSRF is now
403; four existing cookie-lane change-password assertions updated to carry the token via
a local `doChangePassword` helper.

**Error-code / status map (design §5.8).** Two code families, both confirmed live in the
transcripts below:

- *Explicit stable codes* set at the handler via `WithCode`: `password_already_set`
  (409), `password_not_set` (404), `cannot_remove_last_method` (409, shared
  `credential.ErrNoLoginMethod`), `kind_not_supported` (400), `rate_limited` (429),
  `verification_required` (409), `identifier_exists` (409, generic `sdk.ErrAlreadyExists`),
  `unsupported_media_type` (415).
- *Generic sdk-kind codes* via `web.RespondJSONDomainError` (the feature-wide
  convention): the challenge-rail failures collapse to `sdk.ErrExpired` → 410 `expired`,
  `sdk.ErrInvalidInput` → 400 `bad_request`, `sdk.ErrForbidden` → 403 `permission_denied`
  (the `ErrChallengeExpired`/`ErrChallengeInvalid`/`ErrTooManyAttempts` sentinels), and
  step-up/live-session denial is 401 `unauthenticated`. Login-like verification stays a
  single generic outcome — a wrong/expired/wrong-context code never distinguishes "no
  such challenge" from "wrong code" (enumeration protection), and no secret is named.

Inventory finding (NOT a phase-6 regression; logged for the AV3-9.7 reviewer wave): the
STATUS codes match §5.8 exactly (410/400/403/404/409), but the design's *named* challenge
code strings `challenge_expired`/`challenge_invalid`/`too_many_attempts` are not emitted
as literal `code` values — the whole auth feature (verify, reset, step-up, identifier
confirm) routes challenge failures through `RespondJSONDomainError`'s generic sdk-kind
codes. This is a pre-existing convention shared by the migrated register-verify/reset
flows, not something this phase introduced; the security-relevant contract (status +
generic, secret-free, non-enumerating body) holds. Left as-is (out of AV3-6.6 scope; a
feature-wide code-string decision belongs to the reviewer/docs phase).

**Security-event inventory (no secret / no unmasked destination).** Every sensitive
start/complete records an audit row carrying only non-secret context — `purpose`,
`provider`, `IdentifierKind`, userID, status — never a code, token, password, or
destination address (`recordStepUp`, `recordOAuth`, `recordSecurityEvent`,
`enqueueIdentifierChangeNotices`). New/used this phase: `step_up_challenge_sent` /
`step_up` (success/failure/blocked — step-up records all three dispositions);
`password_set`, `password_remove_code_sent`, `password_removed`; `oauth_unlink_code_sent`,
`oauth_unlinked`; the seven identifier events `email_change_code_sent`/`email_changed`/
`email_removed`, `phone_change_code_sent`/`phone_changed`/`phone_removed`,
`identifier_uses_changed`. Success is recorded at the mutation; the failure/blocked
audit for the code-gated flows is the step-up disposition (success/failure/blocked) plus
the challenge rail's own `challenge_lockout` on lockout — so the "records
success/failure/blocked without secrets" requirement is satisfied across the step-up +
challenge rails feeding each mutation.

**Live credential suite — fresh pgx and turso (both PASS).** The complete credential-suite
store contract ran green on both live dialects (the wrong-provider/wrong-context
consumption is `Challenges/ConsumeCodeContextMismatchConsumes` +
`AuthenticationGrants/ConsumeContextMismatchNotFound`; concurrent removal serialization is
`CredentialMutations/{ApplyStaleRevisionConflict,ConcurrentApplySingleWinner}` +
`UserIdentifiers/{ApplyVerifiedChangeRevisionConflict,ConcurrentClaimArbitration}`):

- **pgx (C-collation), with `-race`** —
  `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/authv3_cconf?sslmode=disable' go test -race -run TestConformance_Postgres ./...`
  → `ok … 20.5s`. UserIdentifiers (13 legs), Challenges (incl. ContextMismatch, lockout,
  concurrent single-winner), ContactChanges (replace-per-(user,kind), single-use Consume,
  concurrent single-winner), AuthenticationGrants (context binding, single-use, concurrent
  single-winner), CredentialMutations (snapshot, stale-revision conflict, concurrent
  single-winner) — all PASS.
- **turso (libsql-server, `-tags=integration`)** —
  `cd features/authentication/stores/turso && TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN='local-dev' go test -tags=integration -run TestConformance_Turso ./...`
  → `ok … 10.3s`. The same UserIdentifiers / Challenges / ContactChanges /
  AuthenticationGrants / CredentialMutations legs all PASS. (`TURSO_AUTH_TOKEN=local-dev`
  is the approved local substitute for remote Turso — precedent AV3-2.4.)

Live-store items AV3-6.5 flagged, confirmed: `contactchange` Create replaces per
(user,kind) and `Consume` is single-use on both dialects
(`ContactChanges/CreateReplacesPriorPerUserKind` + `ConsumeIsSingleUse` green live); the
code-path returns the stored context digest in `Consumed.Context` (the binding check the
identifier confirm depends on) — proven by the green `ConsumeCodeContextMismatchConsumes`
leg on both dialects. The remove-password reset-token invalidation rides `Replace`'s
atomic-delete contract; the `Challenges/ReplaceIsSingleActivePerUserPurpose` leg is green
live on both dialects, so the tombstone-via-Replace behaves as intended.

**Service-level suite, `-race`.** The wrong-provider consumption and concurrent-removal
serialization at the SERVICE layer (the halves the store contract does not exercise) —
`oauth_unlink_test.go` wrong-provider-consume + spent-code retry, `password_test.go` /
`identifier_management_test.go` concurrent-removal policy re-evaluation — pass under
`-race`:
`go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/...` → ok
(both packages).

**HTTP transcripts (cookies/tokens/codes/receipts redacted).** Captured live over the
inbound httptest layer (a real `http.Request` → the real mounted handler chain → real
`http.ResponseWriter`) via a temporary AV3-6.6 harness that was removed after capture (no
bespoke driver committed, AV3-5.6 precedent). End-to-end host transcripts against a
running proof host are phase 8's job (§11 host carry (c); AV3-8.10) — `examples/auth-cms`
does not wire the credential rails until then. Representative redacted transcripts:

```
> GET /auth/methods                                 (Cookie: session=<redacted>)
< 200  Cache-Control: no-store
< {"has_password":true,"oauth":[],"identifiers":[{"id":"id-primary","kind":"email",
   "value":"a***@example.com","verified_at":"…","uses":["login","recovery",
   "notification"],"primary":true,"removable":false}]}

> POST /auth/step-up/password  (Bearer <redacted>)  {"purpose":"set_password",
   "context":"","password":"<redacted>"}
< 200  Cache-Control: no-store   {"status":"verified","expires_at":"…"}

> POST /auth/password/set       (Bearer <redacted>)  {"new_password":"<redacted>"}
< 409  Cache-Control: no-store   {"message":"password already set",
   "code":"password_already_set"}

> POST /auth/password/remove/start (Bearer <redacted>)  {}
< 200  Cache-Control: no-store   {"status":"sent","receipt":"<redacted>"}

> POST /auth/password/set       (no session)          {}
< 401                            {"message":"authentication required",
   "code":"unauthenticated"}

> POST /auth/identifiers/email  (Bearer <redacted>)  {"email":"new@example.com",
   "uses":{"notification":true},"make_primary":false}
< 200  Cache-Control: no-store   {"status":"sent","receipt":"<redacted>"}

> POST /auth/identifiers/phone  (Bearer <redacted>)  {"phone":"+15551234567",…}
< 400  Cache-Control: no-store   {"message":"identifier kind not supported",
   "code":"kind_not_supported"}

> DELETE /auth/identifiers/{id} (Bearer <redacted>)   (unknown id)
< 404  Cache-Control: no-store   {"message":"not found","code":"not_found"}
```

The masked `/auth/methods` value (`a***@example.com`), `Cache-Control: no-store` on every
sensitive response, the explicit stable codes, and the generic non-enumerating
`unauthenticated`/`not_found` are all observable in the live responses.

Files changed:
- `internal/inbound/authentication/routes.go` — `/auth/password/change` re-registered with
  the `browserSafe` gate (design §9.1 parity fix; doc updated).
- `internal/inbound/authentication/sessions_test.go` — `doChangePassword` CSRF helper;
  new `TestChangePasswordCookieRequiresCSRF`; four cookie-lane change-password assertions
  routed through the helper (no/revoked-session cases unchanged — they 401 pre-gate).

Verification (observed):
- pgx live full conformance `-race` (C-collation DSN) → `ok 20.5s` (commands above).
- turso live full conformance `-tags=integration` → `ok 10.3s` (commands above).
- `go test -race ./internal/logic/authsvc/... ./internal/inbound/authentication/...` → ok.
- `features/authentication` `go build ./... && go vet ./... && go test ./...` → all green.
- **Phase-6 gate:** `make check` → `all checks passed` (all 36 modules build/test/vet, no
  templ drift, integration-tag vet clean, all guards). `make guard` → all guards passed.

For AV3-7.1 (passwordless enablement + construction matrix, `08-passwordless.md`): the
passwordless routes (`POST /auth/passwordless/{start,verify,redeem}`) are the remaining
§9 additions, registered only when `Config.Passwordless` is non-empty (deny-by-absence,
matching the OAuth/machine/token gating already in `routes.go`); §5.8 keeps login and
passwordless verify/redeem a single generic 401 and the starts silent-success, which is
the same `RespondJSONDomainError` generic-code convention inventoried above (do not add
per-reason codes to the passwordless verify path). The challenge rail already carries
`PurposeLoginMagicLink` (token, 15m) and `PurposeLoginOTP` (code, 5m, lockout) specs, and
the durable outbox + `delivery.Router.Supports(kind)` seam (phone iff a notifier is wired)
are the enablement gates a construction matrix must assert. The step-up/recent-auth and
credential-mutation rails are untouched by passwordless (login-only against existing
verified identifiers, §4.1) — no phase-6 seam is owed to phase 7.
