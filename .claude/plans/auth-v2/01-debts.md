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
