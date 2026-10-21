# Segovia v2: repin authorization to v0.27.0

Paste everything below the line into a session working in
`/Users/jrazmi/code/segovia/segovia/v2`. This was prepared on 2026-10-21 from a read-only look
at that repo's `origin/main` at `62671aaa`.

---

Repin Segovia v2's Gopernicus authorization dependency to the authorization-protocol release:

| module | from | to |
|---|---|---|
| `sdk` | v0.14.0 | **v0.15.0** |
| `pockets/authorization` | v0.26.0 | **v0.27.0** |
| `pockets/authorization/stores/pgx` | v0.18.0 | v0.18.0 (unchanged, no new tag) |
| `pockets/authorization/stores/goredis` | v0.5.0 | v0.5.0 (unchanged, no new tag) |

This is a dependency repin with mechanical retypes. It needs **no migration**, and the
decisions and stored data do not change. Do not adopt anything new in this work: the
`Components.Authorizer` protocol, the lowered evaluator and the vendor backends are optional
and separate. Plan it briefly, implement it, and verify the real host behaviour, meaning the
running server and the flightdeck SPA.

## Before you edit

- **Separate open item, do not fold in:** `plans/segovia-v2-cross-origin-protection-handoff.md`
  (in the gopernicus repo) has **not** landed on Segovia `main`. A read-only grep at `62671aaa`
  finds no `CrossOriginProtection` under `v2/`. Tell the owner whether it is in flight.
- Do not touch another branch's uncommitted work; create a branch or worktree from `main`
  (confirm `git status`, the branch and HEAD first).
- Read the upstream release record: `RELEASING.md` in `github.com/gopernicus/gopernicus`, the
  entry "Authorization protocol, slice A (sdk v0.15.0; authorization v0.27.0)", and the v0.27.0
  section of `pockets/authorization/stores/UPGRADE.md`. Those are the source of truth.
- sdk v0.14.0 → v0.15.0 is additive (the new `sdk/capabilities/authorization` package).

## What changes for Segovia

1. **`Check` / `CheckBatch` return `sdkauthorization.Decision`** (from
   `github.com/gopernicus/gopernicus/sdk/capabilities/authorization`), not
   `model.CheckResult`. `Decision` has `Allowed` only. Retype every narrow interface and
   fake. Found by a read-only grep (re-run it; it may be incomplete):
   - `cmd/server/jobs.go:32`: the `Check(...) (authmodel.CheckResult, error)` dependency;
   - `internal/inbound/domains/clips/routes.go:71`: `Check(...)` (its `FilterAuthorized`
     signature is unchanged);
   - `cmd/server/discussion_audience_live_test.go:399`: the `countingDecisions.CheckBatch`
     wrapper;
   - `permissionCheckFunc` in `cmd/server/jobs_test.go` and every test that returns
     `authmodel.CheckResult{Allowed: …}` from it (`notice_delivery_test.go`,
     `personal_notices_test.go`, `timeline_notice_delivery_test.go`).
   Callers that read only `.Allowed` (`discussion_notices.go`, `personal_notices.go`,
   `workshop/authbench`) compile unchanged. Code that reads `Reason` or `ReasonCode` from a
   check must call `CheckExplain` instead, which still returns `model.CheckResult`. The grep
   found none. `git grep -nE 'CheckResult|ReasonCode' -- .` finds every site.
2. **`model.PrincipalRef`, `model.Resource`, `model.CheckRequest` and `model.LookupRequest`
   are deprecated aliases of the sdk types.** Existing literals compile unchanged. Two edges:
   - `PrincipalRef` has no `Validate` method; call `model.ValidatePrincipal(p)`;
   - `CheckRequest.Validate` / `LookupRequest.Validate` check structure only. If Segovia calls
     them for the length or control-character checks, switch to `model.ValidateCheckRequest` /
     `model.ValidateLookupRequest`.
   Leave the alias names in place for now; renaming to the sdk types is optional cleanup.
3. **`Require` answers 503 for any `sdk.ErrUnavailable`.** A TupleCache (goredis) outage, a
   closed snapshot or a tuple read limit used to be **500** and is now **503**. Segovia wires
   the goredis TupleCache, so a Redis outage on a guarded route is now 503. The flightdeck SPA
   must treat it as retryable and not sign out, as it already must for the authentication 503
   from v0.19.1.

## Verify

- `go build ./... && go vet ./... && go test ./...`, plus the live authorization tests
  (`*_live_test.go`) against a scratch database (never the shared or dev database without
  asking; **never port 55432**).
- Run the server and the flightdeck SPA, then check:
  - sign in, open a space and a timeline (allowed);
  - a user without access gets 403 on a guarded `/api/v1` route;
  - a discussion notice and a personal notice still reach permitted recipients only (the
    `Authorizer` paths retyped above);
  - stop Redis briefly: a guarded call is 503, not 500, and the SPA does not sign out.
- Report what passed, what you could not run, and the PR link.
