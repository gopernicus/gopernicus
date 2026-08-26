# features/authentication: PasswordFlowsDisabled

**Status: EXECUTED 2026-08-25** (ratified in-session by Josh, "sounds good"
on the upstream-toggle option; owner cuts the tag). Origin: gps-360-go — a
Google-only, staff-only host that had to 404 the password routes with its own
middleware because the feature mounts them unconditionally and
`credential.PolicyConfig` cannot refuse password establishment.

## Decision

The password credential becomes a POSTURE, off by one additive Config bool,
using the feature's existing deny-by-absence shape (machine identity,
token endpoint, passwordless):

- `Config.PasswordFlowsDisabled bool` (`env:"AUTH_PASSWORD_FLOWS_DISABLED"`)
  → `authsvc.Deps.PasswordFlowsDisabled` → `Service.PasswordFlowsEnabled()`.
- Inbound `Mount` / `mountHTML` register the password routes/pages only when
  enabled (list in the RELEASING note).
- Every password use-case guards first with `requirePasswordFlows()` →
  `ErrPasswordFlowsDisabled` (wraps `sdk.ErrNotFound`, matching the 404).
- `Hasher` stays required; `GET /auth/login` (OAuth entry page) stays mounted.

Patch by owner ruling: zero value byte-preserves behaviour; no schema, no
pins.

## Changes

1. `internal/logic/authsvc/service.go` — Deps field, Service field, accessor,
   guard helper, error; guards on Register/Login/ChangePassword/Forgot/Reset.
2. `internal/logic/authsvc/password.go`, `token.go` — guards on SetPassword,
   RemovePassword, IssueToken.
3. `internal/inbound/authentication/sessions.go` — `PasswordFlowsEnabled()` on
   the `authService` interface; `routes.go` + `html.go` conditional mounts.
4. `authentication.go` — public Config field + docs, deps plumbing,
   `ErrPasswordFlowsDisabled` re-export.
5. Tests: inbound `password_flows_test.go` (routes absent when off, present
   by default, non-password routes untouched); authsvc
   `password_flows_test.go` (every use-case refuses; zero Deps keep flows on).
6. `README.md` Config row; `RELEASING.md` summary + upgrade note.
