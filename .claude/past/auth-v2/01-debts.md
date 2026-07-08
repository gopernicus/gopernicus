# Phase A1 — v1 product debts (session hashing, ChangePassword, verified-email knob)

Status: RATIFIED (cut from design §7)
Executor model: opus
Depends on: — (first phase; everything else touches `authsvc` after this)
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §7 (all
three debts, incl. the pinned service-side hashing shape and the
single-hash-helper rule), §10 (store impact: sessions table UNCHANGED —
no DDL anywhere in this phase).

## Preconditions

- `make check` green on the current tree (26 modules, 4 guards).
- Read `features/auth/internal/logic/authsvc/service.go`,
  `features/auth/logic/session/{session,repository}.go`, and both stores'
  `sessions.go` before editing — the plaintext-token-as-PK comments you
  are about to obsolete are in all four places.

## Work items

1. **Service-side session hashing (design §7.3, pinned).** One private
   mint/lookup hash helper in `authsvc` is the ONLY hashing site —
   SHA-256 (`sdk/cryptids.SHA256Hasher`) of the cookie token before every
   repository `Create`/`Get`/`Delete`. The enumerated choke points, all
   routing through the one helper: Login's create, session validation's
   get, logout's delete, **and ChangePassword's fresh-session mint (WI3
   — reuse Login's internal mint helper, plan-cut amendment)**; A2/A4
   route their minted sessions through the same helper. The service
   returns the plaintext cookie value at mint; `Session.Token` holds the
   stored value (the hash). **No DDL, no store changes, no storetest
   changes.** Update the doc comments that say "no hashing":
   `logic/session/session.go`, `logic/session/repository.go`, both
   stores' `sessions.go` + migration SQL header comments (comment-only
   edits in the store modules).
2. **`SessionRepository.DeleteByUser(ctx, userID) error` port addition
   (design §7.2).** Pinned semantics (cut refinement 1): bulk +
   idempotent; zero matching rows → nil, never `ErrNotFound`. Implement
   in: `stores/turso`, `stores/pgx`, the `storetest` reference impl, and
   `examples/auth-cms/internal/authmem`. New `storetest` case: two
   sessions for user A + one for B → `DeleteByUser(A)` → both A gets →
   `ErrNotFound`, B intact; repeat `DeleteByUser(A)` → nil.
3. **`POST /auth/password/change` (design §7.2, as amended in place
   2026-07-07 at the plan-cut gate).** Session-gated, strict JSON decode
   `{current_password, new_password}`; verify current password; hash +
   `Set` the new one; then revoke ALL the user's sessions via
   `DeleteByUser` and mint a fresh session for the caller (new cookie in
   the response — the mint reuses Login's internal helper, WI1).
   **Atomicity pin (plan-cut amendment):** if `Set` succeeds but
   `DeleteByUser` fails, RETURN the error (operator-visible) — never
   best-effort-log it; the password changed but stale sessions may
   survive, and that must surface. Password-strength validation mirrors
   register's.
4. **`Config.RequireVerifiedEmail bool` (design §7.1, ratified AV8
   default false).** When true, login returns 403 wrapping
   `ErrEmailNotVerified` (new sentinel in `auth.go`) for unverified
   users. (A4 extends the same gate to `/auth/token`.)
5. **Tests.** `authsvc` unit tests: hash round-trip via a fake repo
   (mint returns plaintext ≠ stored value; validate matches; logout
   deletes by hash); change-password happy path + wrong-current-password
   401-shaped error + all-sessions-revoked assertion;
   verified-email gating on/off. HTTP tests for the new route (strict
   decode, unauthenticated 401).

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
cd features/auth/stores/turso && go build ./... && go vet ./... && go test ./...   # hermetic loud skip
cd features/auth/stores/pgx && go build ./... && go vet ./... && go test ./...     # hermetic loud skip
cd examples/auth-cms && go build ./... && go vet ./... && go test ./...
make check
```

Rule-6 grep (import-anchored — the plain-text form false-fails on a doc
comment at `features/auth/stores/turso/turso.go:5`, plan-cut finding):
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
→ empty.

## Real-interaction check

Standing check (a); plus check (b) — the auth-cms five-step flow passes
end to end with hashing live (sessions provably still work over real
HTTP; the cookie value the client holds is NOT what the memstore stores —
assert via a log line or debugger note in the execution log).

## Execution log

(append dated entries here)

### A1 — 2026-07-07 — PASS

Executor: opus. Base tip: `f2735b5`.

**Work items landed:**

1. Service-side session hashing — one private hash helper `hashSessionToken`
   (`sdk/cryptids.SHA256Hasher`) as the sole hashing site, plus a shared
   `mintSession` mint path. Choke points routed through them: Login's create,
   ValidateSession's get, Logout's delete, ChangePassword's fresh mint. Login
   now returns the plaintext cookie token; `Session.Token` holds the stored
   hash. Doc comments obsoleted in `logic/session/{session,repository}.go`, both
   stores' `sessions.go`, and both `0003_sessions.sql` headers (comment-only in
   the store modules; zero DDL).
2. `SessionRepository.DeleteByUser(ctx, userID) error` — bulk + idempotent
   (zero rows → nil). Implemented in stores/turso, stores/pgx, the storetest
   reference impl, `examples/auth-cms/internal/authmem`, and both test fakes.
   New storetest case `Sessions/DeleteByUser` exactly as pinned (two A + one B →
   DeleteByUser(A) → both A ErrNotFound, B intact → repeat → nil).
3. `POST /auth/password/change` — session-gated, strict JSON decode; verify
   current → hash+Set new → `DeleteByUser` (ALL sessions) → mint fresh session
   (new cookie in response). Atomicity pin honored: a `DeleteByUser` failure
   after `Set` is RETURNED, never logged.
4. `Config.RequireVerifiedEmail bool` (default false) → Login returns
   `ErrEmailNotVerified` (wraps `errs.ErrForbidden` → 403) for unverified users;
   the check runs after password verification. Sentinel canonical in `authsvc`,
   re-exported as `auth.ErrEmailNotVerified`.
5. Tests: authsvc hash round-trip (`TestSessionTokenHashRoundTrip`), verified-
   email gating on/off (`TestLoginRequireVerifiedEmail{BlocksUnverified,
   AllowsVerified}`, `TestLoginVerifiedGateOffByDefault`), change-password
   happy + all-sessions-revoked + wrong-current (`TestChangePassword`,
   `TestChangePasswordWrongCurrent`); HTTP route tests
   (`TestChangePasswordRoute{RequiresSession,StrictDecode,WrongCurrent,
   HappyPath}`). Existing Login/Logout/RequireUser tests updated for the new
   signatures.

**Acceptance (all PASS):**

- `cd features/auth && go build/vet/test ./...` → ok (auth, http, authsvc,
  storetest all ok).
- `cd features/auth/stores/turso && go build/vet/test ./...` → ok (hermetic).
- `cd features/auth/stores/pgx && go build/vet/test ./...` → ok (hermetic).
- `cd examples/auth-cms && go build/vet/test ./...` → ok.
- `make check` → `all checks passed` (26 modules + 4 guards; templ drift clean).
- Rule-6 grep `-E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
  → empty.

**Real-interaction check (a) — standing:** `make check` green; `examples/minimal`
:8081 → `GET /` 200, `GET /products/widget-3000` 200; killed by port; port free.

**Real-interaction check (b) — auth flow (`examples/auth-cms` :8082, RequireVerifiedEmail=false):**

- unauthenticated `GET /menus` → 401
- register → 201
- login → 200 (+ session cookie)
- `GET /menus` with cookie → 200
- logout → 200
- `GET /menus` after logout → 401

**Hashing proof (evidence method: temporary `os.Stderr` debug line in
authmem.sessionRepo.Create, removed after capture):**

- cookie the client held: `q3x2wvw2s6ml4cnpoqge35r3we` (26-char base32 sdk/id)
- memstore stored token: `578c8eea782ae923cf1b20bdd3b1b0763289aa8bc02edbdae507dcbf6bde7260`
- `shasum -a 256` of the cookie value = `578c8eea…6bde7260` — identical to the
  stored value, and ≠ the cookie. The store never holds the plaintext.

**Password-change route driven live (fresh login, cookie held `hwvrknbgwywjrjijtwxayciili`):**

- change wrong current → 401 (401-shaped)
- change correct current → 200 + NEW cookie `vjjbxwch5tz2vlpedb35r7krr4` (≠ old)
- old cookie on `GET /menus` → 401 (revoked)
- new cookie on `GET /menus` → 200
- re-login with new password → 200; re-login with old password → 401

Server killed; port 8082 free.

**Divergences (operational, no design change):**

- Login's return type changed from `session.Session` to the plaintext token
  string `(token string, u user.User, err error)`; ChangePassword now returns
  `(token string, err error)`. Forced by §7.3 (`Session.Token` = the hash, so
  the plaintext cookie must be returned separately). The inbound `authService`
  interface gained `ChangePassword`/`CurrentUser` and dropped the now-orphaned
  `session` import.
- `ErrEmailNotVerified` is declared canonically in `authsvc` (so Login can
  return it) and re-exported as `auth.ErrEmailNotVerified` (same value). It
  wraps `errs.ErrForbidden`, so the existing `ErrFromDomain` path maps it to 403
  with no special-case handler.
- The session-token hasher is constructed internally in `authsvc.NewService`
  (`cryptids.NewSHA256Hasher()`), not exposed as a host port — §7.3 pins the
  algorithm. `features/auth/go.mod` still requires exactly `sdk` (cryptids is
  under sdk).
