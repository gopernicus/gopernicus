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
