# Phase A2 — OAuth: domains, flow orchestration, Config, routes

Status: RATIFIED (cut from design §3)
Executor model: opus
Depends on: A1 (minted OAuth sessions must route through A1's hash helper).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §3 (the
whole section: domains, the anti-takeover flow with the pinned
oauthstate pending-link, Config nil-semantics table, route surface,
ratified AV7 trims), §2.2 (seam discipline), §9 (no satisfier layer —
stores are hand-written later, in A7a/A7b). What already exists and is
consumed unchanged: `sdk/oauth` (Provider port + PKCE S256),
`integrations/oauth/{google,github}` (host-wired only — G2 forbids the
feature importing them; tests use a fake Provider).

## Work items

1. **`logic/oauthaccount`** (public rim): `OAuthAccount` entity per
   design §3 (token fields hold ciphertext when an encrypter is wired,
   empty otherwise) + `OAuthAccountRepository` — `Create` (duplicate
   `(provider, provider_user_id)` → `errs.ErrAlreadyExists`),
   `GetByProvider(ctx, provider, providerUserID)` (absent →
   `errs.ErrNotFound`), `ListByUser`, `Delete(ctx, userID, provider)`.
2. **`logic/oauthstate`** (public rim): `State{Token, Provider, Purpose,
   Payload []byte, ExpiresAt}` + `StateRepository` — `Create`,
   `Consume(ctx, token)` (single-use get-and-delete). **Consume contract
   pinned (plan-cut amendment):** the row is deleted REGARDLESS of expiry
   — an expired `Consume` deletes too and returns `errs.ErrExpired`; a
   second `Consume` of any token → `errs.ErrNotFound`; unknown →
   `errs.ErrNotFound`. (Stores implement it as `DELETE … RETURNING`, the
   jobs queue.go precedent — A7a/A7b.) Purposes: flow state and
   pending-link (design §3's pin: pending-link is an oauthstate row,
   NEVER `verification.Code`). v1's verification ports stay frozen.
3. **Flow orchestration in `authsvc`** (design §3, kept intact):
   start → PKCE verifier+challenge, state token, OIDC nonce when
   `SupportsOIDC()`, state persisted; callback → state consumed, code
   exchanged, identity read (ID-token claims for OIDC, `GetUserInfo`
   otherwise); three-way branch — existing link → login (session via
   A1's hash helper); existing user w/ matching email but no link →
   pending link (single-use secret mailed via `Config.Mailer`; completes
   only via verify-link); no user → register + link with
   `TrustEmailVerification()` gating the new user's verified flag.
   Linking (session-gated) + unlink with **last-authentication-method
   protection** (refuse to unlink the only credential when no password
   is set). Provider tokens encrypted via `Config.TokenEncrypter` when
   wired, dropped (empty) when nil.
4. **Config additions + validation** (design §3 table): `Providers
   []oauth.Provider` (nil/empty → OAuth off, routes NOT registered),
   `TokenEncrypter cryptids.Encrypter`, `OAuthCallbackBase string`,
   `RedirectAllowlist []string` (exact-match matcher, feature-internal
   package under `internal/`). Loud partial wiring:
   `ErrOAuthReposRequired` when Providers set but
   `Repositories.OAuthAccounts` or `Repositories.OAuthStates` nil.
5. **Routes** (design §3, inside `/auth/*`):
   `GET /auth/oauth/{provider}/start`,
   `GET /auth/oauth/{provider}/callback`, `POST /auth/oauth/verify-link`;
   session-gated: `GET /auth/oauth/linked`,
   `GET /auth/oauth/{provider}/link/start`,
   `DELETE /auth/oauth/{provider}/link`. AV7 trims are law: NO mobile
   endpoints, NO code-gated unlink.
6. **`storetest` sub-runners** for both new ports (+ reference in-memory
   impls inside storetest), incl. the `(provider, provider_user_id)`
   uniqueness case and the full pinned `Consume` semantics —
   **explicitly asserting second-`Consume` → `ErrNotFound` and
   expired-`Consume` → `ErrExpired` (with the row gone afterward)**.
7. **Tests**: fake `oauth.Provider` (in-package test stub) driving all
   three callback branches, pending-link completion, unlink last-method
   protection, encrypter-nil token-drop, state expiry/single-use, and the
   partial-wiring construction error.

Note: security-event recording for these ops lands in A5 (which depends
on this phase) — do not add audit calls here.

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
make check
```

Rule-6 grep (import-anchored, plan-cut form):
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
→ empty. The feature-never-imports-integrations edge is covered by **G2
(`make guard-feature-isolation`) — `make check` green suffices** (a bare
`grep -rn "integrations/" features/auth/` can NEVER pass: the stores
under `features/auth/` legitimately import connectors — plan-cut
correction).

## Real-interaction check

Standing check (a); check (b) unchanged (password flow untouched). Plus
the deny-by-absence proof: boot `examples/auth-cms` (which wires NO
providers yet) and `curl -s -o /dev/null -w '%{http_code}'
localhost:8082/auth/oauth/github/start` → **404** — OAuth routes
correctly absent when unwired. The full wired OAuth run-and-look is A9's.

## Execution log

(append dated entries here)

### A2 — 2026-07-07 — PASS

Executor: opus. Base tip: `528f953` (A1).

**Work items landed:**

1. `logic/oauthaccount` — `OAuthAccount` entity (token fields = ciphertext when
   an encrypter is wired, empty otherwise) + `New` constructor + the
   `OAuthAccountRepository` port with the pinned sentinels (duplicate
   `(Provider, ProviderUserID)` → `ErrAlreadyExists`; `GetByProvider` absent →
   `ErrNotFound`; `Delete(userID, provider)` absent → `ErrNotFound`) plus
   `ListByUser`.
2. `logic/oauthstate` — `State{Token, Provider, Purpose, Payload []byte,
   ExpiresAt}` + `New`/`Expired` + purposes (`PurposeFlow`,
   `PurposePendingLink`) + `StateRepository{Create, Consume}` documenting the
   PINNED Consume contract (delete-regardless-of-expiry; expired → `ErrExpired`
   with the row gone; second/unknown → `ErrNotFound`). Pending-link is an
   oauthstate row; v1 verification ports untouched.
3. Flow orchestration in `authsvc` (`internal/logic/authsvc/oauth.go`):
   `StartOAuth`/`StartLink` (PKCE verifier+challenge, OIDC nonce when
   `SupportsOIDC()`, state persisted, redirect validated) → `OAuthCallback`
   (state consumed single-use, code exchanged, identity read via ID-token
   claims for OIDC / `GetUserInfo` otherwise) → the three-way branch (existing
   link → login via A1's `mintSession`; matching email, no link → pending link
   mailed via `Config.Mailer`, completed only by `VerifyLink`; no user →
   register + link, `TrustEmailVerification()` gating verified). Session-gated
   `StartLink`; `Unlink` with last-authentication-method protection
   (`ErrLastAuthMethod`, wraps `errs.ErrConflict`); `ListLinked`. Provider
   tokens encrypted via `Config.TokenEncrypter` when wired, dropped when nil.
4. Config additions on `auth.Config`: `Providers []oauth.Provider` (empty →
   OAuth off, routes not registered), `TokenEncrypter cryptids.Encrypter`,
   `OAuthCallbackBase string`, `RedirectAllowlist []string` (exact-match matcher
   in feature-internal `internal/redirect`). `Repositories` gained
   `OAuthAccounts`/`OAuthStates`. Loud partial wiring: `ErrOAuthReposRequired`
   when Providers set but either oauth repo nil.
5. Routes (conditional on `OAuthEnabled()`): `GET /auth/oauth/{provider}/start`,
   `GET /auth/oauth/{provider}/callback`, `POST /auth/oauth/verify-link`;
   session-gated `GET /auth/oauth/linked`,
   `GET /auth/oauth/{provider}/link/start`,
   `DELETE /auth/oauth/{provider}/link`. AV7 trims honored (no mobile
   endpoints, no code-gated unlink).
6. storetest sub-runners for both new ports (+ reference in-memory impls in
   `reference_test.go`): `OAuthAccounts/{CRUDRoundTrip, ProviderUniqueness,
   AbsentNotFound, ListByUser, DeleteAbsentNotFound}` and
   `OAuthStates/{ConsumeSingleUse, ConsumeExpiredDeletes, ConsumeUnknown}` —
   explicitly asserting second-Consume → `ErrNotFound` and expired-Consume →
   `ErrExpired` with the row gone afterward.
7. Tests: in-package fake `oauth.Provider` in `authsvc/oauth_test.go` driving
   all three callback branches, pending-link completion + single-use,
   session-gated link, unlink last-method protection (4 sub-cases),
   encrypter-wired vs encrypter-nil token drop, state single-use + expiry +
   provider-mismatch; `http/oauth_test.go` route wiring (deny-by-absence 404,
   start 302, unknown provider 404, session-gated 401, verify-link strict
   decode); `auth_test.go` partial-wiring construction error + oauth-off
   allows-nil + Register deny-by-absence; `internal/redirect` matcher unit
   tests. No audit calls (A5); no store-module code (A7a/A7b).

**Acceptance (all PASS):**

- `cd features/auth && go build ./... && go vet ./... && go test ./...` → ok
  (auth, http, authsvc, redirect, storetest all ok).
- `cd features/auth/stores/{turso,pgx} && go build/vet/test ./...` → ok
  (hermetic; new OAuth sub-runners skip loudly there — they run non-skipped in
  A7a/A7b's live legs).
- `cd examples/auth-cms && go build/vet/test ./...` → ok (authmem's
  `storetest.Run` skips the OAuth sub-runners LOUDLY — authmem gains no OAuth
  ports until A9).
- `make check` → `all checks passed` (26 modules + 4 guards; templ drift clean;
  integration-tag vet clean). G2 covers the feature-never-imports-integrations
  edge.
- Rule-6 grep `-E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
  → empty.

**Real-interaction check (a) — standing:** `make check` green;
`examples/minimal` :8081 → `GET /` 200, `GET /products/widget-3000` 200;
killed; port 8081 free (0 listeners).

**Real-interaction check (b) — auth flow unchanged (`examples/auth-cms` :8082,
cookie jar):**

- 1. unauthenticated `GET /menus` → **401**
- 2. register → **201**
- 3. login → **200** (+ 1 session cookie captured)
- 4. `GET /menus` with cookie → **200**
- 5. logout → **200**
- 6. `GET /menus` after logout → **401**

**Real-interaction check (c) — deny-by-absence proof (same host, NO providers
wired):**

- `GET /auth/oauth/github/start` → **404** (OAuth routes correctly absent when
  unwired)

Server killed; port 8082 free (0 listeners).

**Divergences (operational, no design change):**

- **storetest OAuth sub-runners are CONDITIONAL** — each group skips LOUDLY when
  its port is nil (`OAuthAccounts`/`OAuthStates`). This keeps authmem's existing
  `storetest.Run` (`examples/auth-cms/internal/authmem/authmem_test.go`) green
  WITHOUT extending authmem (A9's scope), while the in-storetest reference impl
  exercises the full sub-runners under `make check`, and A7a/A7b's live legs run
  them non-skipped. A loud skip (never a silent green) matches the milestone's
  turso/pgx skip idiom.
- **Transport shape:** `start` and `callback` are 302 browser redirects (correct
  OAuth semantics); `verify-link`/`linked`/`unlink` are JSON. Callback/flow
  errors return JSON mapped via `ErrFromDomain`. The full wired run-and-look is
  A9's.
- **RedirectAllowlist** resolves a non-allowlisted requested target to the
  safe same-origin default `/` (silent safe fallback, never an open redirect),
  rather than a hard 400. The same-origin `/` is always allowed.
- **VerifyLink mints a session** (logs the user in) on completion — the emailed
  single-use secret proves email ownership, equivalent to a magic-link login.
- **Public surface:** beyond the phase-named `ErrOAuthReposRequired`,
  `auth.ErrOAuthLastMethod` is re-exported (a documented unlink refusal a host
  may detect; wraps `errs.ErrConflict` → 409). No new sdk dependency:
  `features/auth/go.mod` still requires exactly `sdk` (oauth/cryptids/id are
  under sdk).
- authmem NOT extended (A9 owns the wired proof host); auth-cms wires no
  providers, so the deny-by-absence 404 holds and the standing (b) flow is
  unchanged.
