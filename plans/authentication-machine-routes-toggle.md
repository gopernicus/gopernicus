# features/authentication: MachineRoutesDisabled (+ the lifecycle-routes caveat)

**Status: EXECUTED 2026-08-25** (in-session, Josh; owner cuts the tag). Origin:
gps-360-go batch 4 — a staff host needing API keys whose stewards alone may
create accounts and mint keys.

## Finding

`mountMachine` registers the service-account / API-key lifecycle routes behind
`RequireUser` (any authenticated user). `createServiceAccount` passes the
body's `owner_user_id` straight to `CreateServiceAccount`; `ListServiceAccounts`
is global; `MintAPIKey` / `ListAPIKeys` / `RevokeAPIKey` take any id. On a
multi-user host that is impersonation-by-key (create an act-as-user account
owned by someone else, mint it) plus global list/revoke.

## Decision

Same shape as `PasswordFlowsDisabled`: `Config.MachineRoutesDisabled` →
`Deps.MachineRoutesDisabled` → `Service.MachineRoutesEnabled()`; `Mount`
registers the lifecycle routes only when `MachineEnabled() &&
MachineRoutesEnabled()`. Key authentication is untouched. The proper scoping
fix (bind owner to caller; scope list/mint/revoke to creator) changes shipped
behaviour and is deferred to an owner ruling — recorded in the RELEASING note.

## Changes

1. `internal/logic/authsvc/service.go` — Deps field, Service field, accessor.
2. `internal/inbound/authentication/sessions.go` — interface method;
   `routes.go` — conditional `mountMachine`.
3. `authentication.go` — public Config field + docs, deps plumbing.
4. Tests — `password_flows_test.go` gains `newPostureHandler` +
   `TestMachineRoutesDisabled_RoutesAbsent` (absent when off, present by
   default, subsystem still enabled).
5. `README.md` Config row (with the caveat); `RELEASING.md` summary + note.
