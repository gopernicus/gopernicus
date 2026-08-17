# Phase 7 — existing OAuth account-linking documentation and proof

## Outcome

Close the “no user-initiated account linking” flag as a discovery/documentation
issue, not an implementation task. Pin the already-shipped route/service behavior
with an exported-surface proof and explain how a settings UI drives it.

## Audited evidence

The following have shipped since `features/authentication/v0.1.0`:

- `Service.StartLink(ctx, userID, provider, redirectTo)`;
- session-gated `GET /auth/oauth/{provider}/link/start`;
- server-side OAuth state carrying `LinkUserID`;
- callback branch that attaches the provider to that exact user and returns
  `ActionLinked`; and
- `GET /auth/methods` as the masked linked-method inventory, plus the code-gated
  unlink pair.

The README names the route in one line, but does not provide the end-to-end
settings-page recipe or clearly distinguish explicit linking from the
email-collision pending-link flow. That documentation gap plausibly caused the
false upstream flag.

## CHAU-7.1 — pin the contract

Add/retain characterization proving:

- link start requires an authenticated human user and an enabled provider;
- server-side state binds provider, redirect, PKCE verifier, nonce, and linking
  user ID;
- callback cannot be redirected to a different user by query input;
- provider identity already owned by another user conflicts and does not move;
- successful explicit link returns `ActionLinked`, creates no second user, and
  does not silently mint/replace the caller's session;
- callback state is single-use and redirect is allowlisted; and
- `/auth/methods` immediately shows the linked provider and unlink follows the
  existing code-gated flow.

If the current route uses the stateless `RequireUser` tier, document its bounded
revocation behavior. Phase 1 should evaluate changing it to `RequireLiveSession`
for immediate deactivation semantics; do not quietly alter the gate in a docs-only
task.

## CHAU-7.2 — exported-surface settings proof

In `examples/auth-cms` or a focused external-package test, drive only public
seams:

1. establish a user session;
2. discover provider names/method inventory;
3. request `/auth/oauth/google/link/start?redirect=/settings/security`;
4. complete a fake provider callback using the persisted state;
5. assert redirect, existing session behavior, and `/auth/methods` inventory;
6. attempt cross-user/replayed-state failures; and
7. execute the unlink-code start/complete pair.

The test doubles as runnable adopter documentation and prevents an internal-only
method from being mistaken for a host-usable flow.

## CHAU-7.3 — README and downstream guidance

Expand the OAuth section with two separate diagrams/step lists:

1. **Explicit signed-in linking:** settings button → link/start → provider →
   callback `ActionLinked` → redirect → refresh `/auth/methods`.
2. **Collision/pending link:** unauthenticated provider sign-in matches an
   existing email → mailed confirmation → verify-link → new session.

Document:

- route methods and gates;
- redirect allowlist behavior;
- why the explicit callback does not mint a new cookie;
- provider-already-linked conflict;
- CSRF/login-CSRF posture for the GET redirect initiator and callback state;
- inventory and unlink routes;
- a TypeScript/settings-button pseudo-client; and
- version evidence (`v0.1.0+`) so coordination-hub can mark the flag resolved
  without waiting for a new code tag.

Add a `RELEASING.md` docs note only if repository docs change; do not cut a code
release solely to pretend the feature was newly added.

Verification:

```sh
(cd features/authentication && go test -race ./...)
(cd examples/auth-cms && go test -race ./...)
```

## Execution log

Append only. Record CHAU-7.x characterization/proof commands, documentation
changes, and the downstream flag-resolution evidence.

### 2026-08-16 — CHAU-7.1, CHAU-7.2, CHAU-7.3 complete

**No production code was changed in this phase.** The flag was a discovery gap;
treating it as an implementation task would have created a second route and state
model for a flow that already exists.

**CHAU-7.1 — pin the contract.** New
`features/authentication/internal/logic/authsvc/oauth_link_test.go` (nine tests,
all passing). Existing coverage already pinned the happy path
(`TestOAuthLinkStartFlow`), the wrong-provider state, and single-use state on the
login lane; these close the gaps the plan lists:

| plan requirement | test |
|---|---|
| state binds provider, redirect, PKCE verifier, nonce, linking user | `TestLinkStartStateBindsFlowFacts` — also asserts none of those facts appear in the authorization URL the browser carries |
| enabled provider only | `TestLinkStartRejectsUnknownProvider` (`sdk.ErrNotFound`) |
| redirect allowlisted | `TestLinkRedirectFallsBackToSameOrigin` — resolution happens at START, so the stored destination is already safe |
| does not mint/replace the caller's session | `TestExplicitLinkMintsNoSession` — empty token pair AND unchanged session count |
| creates no second user | `TestExplicitLinkCreatesNoSecondUser` — provider email deliberately matches no identifier, so a fall-through to the register branch would show as a user-count change |
| callback cannot be retargeted by query input | `TestLinkCallbackTargetsStateOwnerNotCaller` |
| identity owned by another user conflicts and does not move | `TestLinkConflictDoesNotMoveIdentity` — asserts the original owner still holds it after the failed claim |
| state single-use | `TestLinkStateIsSingleUse` |
| link immediately visible | `TestLinkedProviderIsImmediatelyListed` |

One deliberate substitution: the "immediately visible" test asserts
`Service.ListLinked`, not `Service.Methods`. This package's shared
`fakeCredentialMutations.Snapshot` reports only password state, so a `Methods`
assertion here would have tested the fixture rather than the feature. The
`GET /auth/methods` claim is proven against real repositories in the host test
below; the substitution and its reason are recorded in the test's own comment.
The shared fixture was **not** widened — it is used by many other tests in the
package and changing it was out of scope for a docs-and-proof phase.

**Gate-tier finding (recorded, not acted on).** `GET /auth/oauth/{provider}/link/start`
is registered with the **stateless `requireUser`** tier
(`internal/inbound/authentication/oauth.go:42`), so an already-issued access JWT
stays usable there until `AccessTokenTTL` even after session revocation. Per the
plan this was documented rather than quietly altered, and is flagged for the
phase-1 lifecycle evaluation. The sensitive half of the pair — both unlink routes
— already uses `liveSession` + the browser-safe mutation gate.

**CHAU-7.2 — exported-surface settings proof.** New
`examples/auth-cms/cmd/server/oauth_link_settings_test.go` — a HOST test in a
separate module, so nothing in it can reach a feature-internal package. It runs
the host's real `buildAuthConfig` in `in_process` delivery mode over a recording
mailer, mounts the feature, starts the delivery runtime, and drives everything
over real HTTP with a real cookie jar and redirects NOT followed.

The plan's seven steps, mapped to what runs:

1. **establish a session** — `signUp` does the whole public onboarding: register,
   wait for the mailed six-digit code, `POST /auth/verify`, `POST /auth/login`.
   (The host ships `RequireVerifiedEmail: true`, so this is not optional.)
2. **discover the method inventory** — `GET /auth/methods`, decoded into the
   published JSON shape.
3. **request `link/start` with a settings redirect** — the host's shipped
   `RedirectAllowlist` is `{"/"}`, so the test widens it to include
   `/settings/security` through the public `auth.Config` seam, which is exactly
   what an adopting console must do.
4. **complete the provider callback using the persisted state** — the state is
   parsed out of the 302 `Location`; the host's `fakeOAuthProvider` derives a
   stable identity from the authorization code, so a code value doubles as
   "which provider account".
5. **assert redirect, session behavior, inventory** —
   `TestOAuthExplicitLinkFromSettingsPage` checks the `Location` is the
   allowlisted destination, that **no** session/refresh cookie was re-issued
   (read from the real `Service.SessionCookieName()`/`RefreshCookieName()`), and
   that `/auth/methods` now lists the provider while `has_password` survives.
6. **cross-user and replayed-state failures** —
   `TestOAuthLinkTargetsTheStateOwnerNotTheCaller` has a *different signed-in
   user* redeem a leaked state and asserts the link lands on the state's owner;
   the replay assertion lives in the main flow test;
   `TestOAuthLinkProviderIdentityConflict` proves a second user cannot claim an
   identity and the first does not lose it. `TestOAuthLinkStartRequiresSession`
   (401, no provider redirect) and `TestOAuthLinkStartUnknownProvider` (404)
   cover the gate and deny-by-absence. `TestOAuthLinkRedirectIsAllowlisted`
   proves a hostile `redirect` lands on `/` **while the link still completes** —
   only the destination is refused.
7. **unlink start/complete pair** — `TestOAuthUnlinkCodeFlow` bootstraps the
   double-submit CSRF token from `GET /auth/csrf`, calls `unlink/start`, asserts
   the receipt carries no raw address, extracts the real delivered code from the
   recording mailer, proves a wrong code does **not** unlink, then completes with
   the delivered code and re-reads the inventory.

Verified as actually running (not skipped):

```
=== RUN   TestOAuthExplicitLinkFromSettingsPage        --- PASS (1.05s)
=== RUN   TestOAuthLinkStartRequiresSession            --- PASS (0.00s)
=== RUN   TestOAuthLinkStartUnknownProvider            --- PASS (1.00s)
=== RUN   TestOAuthLinkRedirectIsAllowlisted           --- PASS (1.00s)
=== RUN   TestOAuthLinkTargetsTheStateOwnerNotTheCaller --- PASS (2.02s)
=== RUN   TestOAuthLinkProviderIdentityConflict        --- PASS (2.05s)
=== RUN   TestOAuthUnlinkCodeFlow                      --- PASS (1.02s)
```

**CHAU-7.3 — README and downstream guidance.**
`features/authentication/README.md` gains a new
`## OAuth account linking — two distinct flows` section containing:

- a **version-evidence callout** stating that `StartLink`, the session-gated
  route, the `LinkUserID` state binding, the `ActionLinked` branch,
  `/auth/methods`, and the unlink pair all shipped in **`v0.1.0`** and are
  unchanged — so coordination-hub can resolve the flag on **any** tagged version
  without waiting for a code release;
- two separate ASCII step diagrams — **explicit signed-in linking** and
  **collision/pending link** — with the one-line distinction: flow 1 proves
  *account* ownership with a session and mints nothing; flow 2 proves *address*
  ownership with a mailed secret and mints a session;
- route methods and gates, redirect-allowlist behavior (resolved at START),
  why the explicit callback sets no cookie, the provider-already-linked conflict,
  the CSRF/login-CSRF posture for a GET redirect initiator (single-use
  server-side state + session gate; navigate, do not XHR), the inventory and
  code-gated unlink routes, and the `requireUser`-tier bound;
- a TypeScript settings-page sketch (`connect` as a navigation, `disconnect` as
  the two-step CSRF-carrying pair, inventory-driven rendering);
- a pointer to the runnable proof.

The route list's `link/start` bullet now cross-links the section.

**`RELEASING.md`: intentionally NOT touched by this phase.** The plan says to add
a docs note "only if repository docs change" and explicitly not to cut a code
release to pretend the feature is new. The README change rides whatever
authentication tag phase 8 freezes for the *other* phases; on its own it would
not justify a tag.

**Verification (exactly as run, 2026-08-16):**

```
(cd features/authentication && go test -race ./...)   all packages ok
(cd examples/auth-cms && go test -race ./...)         all packages ok
```

**Downstream flag disposition:** coordination-hub's "no user-initiated OAuth
linking" row is **not a code gap** and needs no upstream tag. The row should be
closed citing `features/authentication/v0.1.0`, the README section above, and the
runnable proof file. Recorded for the phase-8 adoption checklist.
