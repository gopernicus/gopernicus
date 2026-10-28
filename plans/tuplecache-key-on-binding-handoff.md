# Handoff — build issue #76 (tuplecache keyed on binding)

Paste everything below the line into a fresh session started in
the `gopernicus` repo root.

---

You are implementing gopernicus issue #76: key the authorization tuple cache's Redis mirror on
its binding, so hosts never type a namespace that must match it. The plan is RATIFIED. Read it
in full before touching code:

- `.claude/plans/tuplecache-key-on-binding.md` (design D1–D7, rulings R1–R5, acceptance, verify)
- `gh api repos/gopernicus/gopernicus/issues/76 --jq .body` (the `gh issue view` command fails
  on a Projects-classic deprecation error; use the REST form)

## Decisions already made — do not reopen

- **R1:** `Backend.State(ctx, binding string)`. Per-call keying. No `Bind` method, no factory
  func. `Read` addresses `expected.Binding`; `Publish` addresses `next.Binding`.
- **R2:** key prefix `tuplecache:v3:`. `tuplecache.Protocol` and the Redis `!schema` field stay
  `2`. Wire format and Lua logic are unchanged.
- **R3:** orphaned mirrors are documented, not auto-expired. The permanent mirror is
  `PERSIST`ed on swap (`stores/goredis/scripts.go`, replaceScript), so the issue's claim that old
  keys expire on their own is wrong. Say so in the README with a scan-and-delete recipe.
- **R4:** optional `WithNamespace(string) Option`. The positional namespace argument is removed.
  Owner rule: optional parameters are never positional.
- **R5:** goredis validates the binding on every call against `[A-Za-z0-9:._/-]` and returns
  `sdk.ErrInvalidInput`. Never hash it. Custom `Source.Binding()` values can contain braces that
  would escape the Redis hash tag.
- `ErrBinding` stays. In `poll` and `attempt` it now means "the backend returned a foreign
  mirror for my key", which is corruption. The `decisions` constructor check at
  `logic/decisions/constructor.go:69` is unrelated; leave it alone.
- **Posture:** framework fix only. The owner is turning the cache off in gps-360-go and Segovia
  because their principal-heavy shape is a poor fit for this encoding. Tag the release, but do
  not open host repin work. The plan's Downstream section is advisory.

## Files you will touch

- `pockets/authorization/logic/tuplecache/tuplecache.go`: `Backend` interface and doc comment.
  Remove "The host supplies a dedicated namespace".
- `pockets/authorization/logic/tuplecache/runtime.go`: `poll` and `attempt` call
  `State(ctx, c.binding)`.
- `pockets/authorization/stores/memory/tuple_cache.go`: one mirror per binding
  (`map[string]*mirror`). This is the reference backend that the runtime tests rely on.
- `pockets/authorization/stores/goredis/tuple_cache.go`: constructor, `WithNamespace`,
  `key(binding) (string, error)` replacing the stored `key` field, and validation.
- Tests listed in plan D4. These include the rewrite of
  `TestSharedMirrorRejectsDifferentFreshnessPolicies` and of the `ErrBinding` step in
  `TestPolicyBindingStartupAndWake`, which now uses a fake whose `State` returns a foreign
  binding.
- Docs listed in plan D5, plus the D7 "Fit" section in the goredis README. `AUDIT.md` is
  historical; do not edit it.

## Working rules (from the owner's CLAUDE.md and memory)

- Surgical diffs. Every changed line traces to the plan. Do not edit generated files.
- Start Bash calls with `cd <repo> &&`, because bare commands can run with a stripped PATH.
- For implementation subagents, use the project's `implementer` agent as-is; never override its
  model. Review with `data-integration-reviewer` and `lead-backend-engineer`.
- Never claim done from green tests alone. goredis tests start their own `redis-server` and skip
  silently if it is missing. Confirm they ran (`go test -v ... | grep -c SKIP` should be 0). If
  they skipped, report that as unverified, not as passing.
- Real-behaviour check before reporting done: in one test or a scratch program, run two
  runtimes on one Redis with `MaxStaleness` of 1s and 5m. Both must reach ready, `KEYS
  'tuplecache:v3:*'` must show two keys, and no `ErrBinding` may appear.
- Commit and push only on a branch, never `main`. Do not tag or merge without the owner's word.
  The classifier blocks combined merge+tag+push, so run each step on its own. Poll the proxy
  `.info` URL before any `go mod tidy` that resolves a new tag; a premature lookup caches a
  negative result for about 30 minutes.

## Sequence

1. Branch `feat/tuplecache-key-on-binding` from `main`. Confirm `git status` is clean apart
   from the pre-existing untracked files, and check `git ls-remote` first.
2. Core module (`pockets/authorization`): D1, D3, the runtime tests, and the memory backend.
   Verify:
   `cd pockets/authorization && go build ./... && go vet ./... && go test ./...`
3. goredis module: D2, its tests, and D5/D7 docs. goredis pins the core through a published tag
   (`go.mod`: `pockets/authorization v0.18.0`, which is already stale). Use a temporary
   `replace` for local work and say so in the PR. Verify with build, vet, test, and `-race`.
4. Run `make guards` from the repo root.
5. Open the PR or PRs. Two stacked PRs (core, then goredis) are the default; the owner may
   collapse them. Release targets are authorization v0.25.0 and goredis v0.5.0. Tag only on the
   owner's word.
6. Update the plan's Status line with the PR numbers. Close with the owner's standard block:
   status board, numbered Next steps with **YOUR CALL** items, Check manually, Run next.
