# gps-360-go: repin authorization to v0.27.0

Paste everything below the line into a session working in
`gps-360-go`. This was prepared on 2026-10-21 from a read-only
look at that repo's `origin/main` at `77c82c15`.

---

Repin gps-360-go's Gopernicus authorization dependency to the authorization-protocol release:

| module | from | to |
|---|---|---|
| `sdk` | v0.14.0 | **v0.15.0** |
| `pockets/authorization` | v0.26.0 | **v0.27.0** |
| `pockets/authorization/stores/pgx` | v0.18.0 | v0.18.0 (unchanged, no new tag) |
| `pockets/authorization/stores/goredis` | v0.5.0 | v0.5.0 (unchanged, no new tag) |

This is a dependency repin with mechanical retypes. It needs **no migration**, and the
decisions and stored data do not change. Do not adopt anything new in this work: the
`Components.Authorizer` protocol, the lowered evaluator and the vendor backends are optional
and separate. Plan it briefly, implement it, and verify the real host behaviour.

## Before you edit

- Branch from `main` (confirm `git status`, the branch and HEAD; this repo vendors, so expect a
  `vendor/` diff).
- Read the upstream release record: `RELEASING.md` in `github.com/gopernicus/gopernicus`, the
  entry "Authorization protocol, slice A (sdk v0.15.0; authorization v0.27.0)", and the v0.27.0
  section of `pockets/authorization/stores/UPGRADE.md`. Those are the source of truth; this
  prompt summarises them for gps-360-go.
- sdk v0.14.0 → v0.15.0 is additive (the new `sdk/capabilities/authorization` package).

## What changes for gps-360-go

1. **`Check` / `CheckBatch` return `sdkauthorization.Decision`** (from
   `github.com/gopernicus/gopernicus/sdk/capabilities/authorization`), not
   `model.CheckResult`. `Decision` has `Allowed` only. Retype every narrow interface and
   fake. Found by a read-only grep (re-run it; it may be incomplete):
   - `pockets/auth/inbound/access.go:38`: `CheckBatch(...) ([]authmodel.CheckResult, error)`;
   - `pockets/auth/inbound/ask/allowed.go:18`: `Check(...) (authmodel.CheckResult, error)`;
   - test fakes: `pockets/auth/inbound/ask/allowed_test.go`,
     `pockets/auth/inbound/useradmin_test.go`,
     `internal/inbound/domains/lifecycle/intakefence_test.go`.
   Callers that read only `.Allowed` (`clienthub/bootstrap.go:104`, `workshop/authbench`)
   compile unchanged. Code that reads `Reason` or `ReasonCode` from a check must call
   `CheckExplain` instead, which still returns `model.CheckResult`. The grep found none.
   `git grep -nE 'CheckResult|ReasonCode' -- ':!vendor'` finds every site.
2. **`model.PrincipalRef`, `model.Resource`, `model.CheckRequest` and `model.LookupRequest`
   are deprecated aliases of the sdk types.** Existing literals compile unchanged. Two edges:
   - `PrincipalRef` has no `Validate` method; call `model.ValidatePrincipal(p)`;
   - `CheckRequest.Validate` / `LookupRequest.Validate` check structure only. If gps-360-go
     calls them for the length or control-character checks, switch to
     `model.ValidateCheckRequest` / `model.ValidateLookupRequest`.
   Leave the alias names in place for now; renaming to the sdk types is optional cleanup.
3. **`Require` answers 503 for any `sdk.ErrUnavailable`.** A TupleCache (goredis) outage, a
   closed snapshot or a tuple read limit used to be **500** and is now **503**. gps-360-go
   wires the goredis TupleCache, so a Redis outage on a guarded route is now 503. The SPA
   (`ui/`) and the `cmd/gps360` CLI should treat it as retryable, as they already must for the
   authentication 503 from v0.19.1.

## Verify

- `go mod tidy && go mod vendor`, then `go build ./... && go vet ./... && go test ./...`, then
  the repo's own `make` guard targets (`guard-authorization-boundary` included).
- The live authorization tests and `workshop/authbench` proofs, if the repo runs them against a
  scratch database. **Never use port 55432.**
- Run the server and the SPA, then check:
  - sign in, and a guarded page loads (allowed);
  - a user without the role gets 403 on a guarded route;
  - the client-hub capabilities (`bootstrap`) show the same View/Contribute/Manage flags as
    before;
  - stop Redis briefly: a guarded call is 503, not 500, and the SPA does not sign out.
- Report what passed, what you could not run, and the PR link.
