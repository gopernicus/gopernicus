# pgx-crud-v1b — per-aggregate list Limits (crud constants demoted to fallbacks)

Status: **RATIFIED 2026-07-09 (jrazmi, in-conversation — second post-close
amendment to pgx-crud-v1; owner: max page size is a property of the
resource — "a list of ids could be more than 100; a list of embeddings
for moby dick should be limited")**
Executor model: opus
Depends on: pgx-crud-v1 + v1a (CLOSED; this amends their shipped API)

## Why

`NormalizedLimit()` hardcodes `DefaultLimit = 25` / `MaxLimit = 100` at
the store edge — one universal cap cannot fit both cheap rows (bare IDs)
and expensive ones (embeddings). The transport edge is already
per-callsite (`ListParams.MaxLimit`), but nothing per-RESOURCE exists,
and the store clamp is not configurable at all. The fix follows the Q1
pattern: limits are per-aggregate list vocabulary, declared in the
domain rim exactly like `OrderFields`/`DefaultOrder`. The crud constants
STAY — demoted to the zero-value fallbacks (sdk stays the opinionated
zero-config starter).

## Ratified design

```go
// sdk/crud — Limits is what the RESOURCE permits; ListRequest.Limit is
// what the caller wants. Zero fields fall back to the crud constants.
type Limits struct {
    Default int // 0 = DefaultLimit (25)
    Max     int // 0 = MaxLimit (100)
}

// NormalizedLimit gains the resource's Limits (breaking — the zero-arg
// form is removed; zero tags, third and final crud break this window).
// Resolution: effective default/max from l with const fallbacks; a
// declared Default greater than the effective Max clamps to Max
// (defensive, documented); limit <= 0 → effective default; limit >
// effective max → effective max.
func (r ListRequest) NormalizedLimit(l Limits) int

// ListParams: the v1a `MaxLimit int` field is REPLACED by
// `Limits Limits` — one vocabulary at both edges. Strict semantics
// unchanged in kind: empty limit param → effective default; a limit
// over the effective max is an ERROR at this edge, never clamped.
type ListParams struct {
    Limit, Cursor, Offset, Count string
    Limits          Limits
    DefaultStrategy Strategy
}
```

- **Connectors (both dialects):** `ListQuery[T]` gains
  `Limits crud.Limits`; the flows call
  `req.NormalizedLimit(q.Limits)`. Zero value preserves today's
  behavior exactly.
- **Domain-rim convention (documented, not yet exercised):** an
  aggregate whose resource needs non-default limits declares
  `var ListLimits = crud.Limits{…}` beside its `OrderFields`, and its
  stores/handlers pass it. NO current aggregate declares one — every
  existing call site passes the zero value, so this amendment changes
  no observable behavior anywhere.
- **Call-site ripple (mechanical):** every `NormalizedLimit()` caller
  gains the Limits arg (connectors, memstores ×4, reference pagers,
  any handler using clamp semantics — cms SSR); `ParseListRequest`
  call sites move `MaxLimit:` → `Limits: crud.Limits{Max: …}` (or drop
  it for the default). Storetest families unchanged (they construct
  explicit limits).
- **Docs:** crud package doc (two-semantics section + the constants'
  demotion to fallbacks + the rim convention), pgxdb/turso README
  ListQuery contracts, features/README checklist item 13 gains the
  ListLimits sentence.

## Out of scope

- Declaring custom Limits on any current aggregate (none needs one yet).
- Env-configurable limits (limits are resource properties, in code —
  ruled at ratification; strategy remains the only env-defaultable knob).
- Per-request max overrides or any ListRequest field growth.

## Verify

Per-module build/vet/test (sdk, both connectors, four features +
storetests, example hosts); `make check` + `make guard`; live legs both
dialects (pgx docker all modules; turso playground `-count=1` all four —
behavior is identity under zero Limits, the live runs are the
regression proof); real-interaction: auth-cms JSON leg — bare list →
25 items default, `?limit=101` → 400 (strict edge unchanged),
`?limit=100` → 200.

## Execution log

### 2026-07-09 — executed (implementer on opus); AMENDMENT COMPLETE

Landed as pinned. `crud.Limits{Default,Max}` with const fallbacks +
defensive default>max clamp; `NormalizedLimit(l Limits)` replaces the
zero-arg form; `ListParams.MaxLimit` → `Limits Limits` (strict edge
kind unchanged); both connectors' `ListQuery[T]` gain `Limits` and the
flows clamp through it; package doc carries the caller-intent
(ListRequest.Limit) vs resource-policy (Limits) split, the constants'
demotion, and the `var ListLimits` rim convention; features/README
item 13 extended. Call-site ripple all zero-value — observable behavior
identical everywhere; NO aggregate declares custom limits (as pinned).
New tests: Limits resolution table, custom-default/over-max parse
edges, pgxdb literal LIMIT-arg clamp assertion, turso behavioral clamp
(Max 2 caps Limit 3 + HasMore from the Max+1 over-fetch).

**Flagged, accepted:** machine.go dropped its `MaxLimit: crud.MaxLimit`
field rather than converting (it WAS the fallback — zero Limits is
identical and cleaner); turso's clamp test is behavioral over real
in-memory SQLite (no capture harness exists there — pgxdb carries the
literal-arg assertion); cms SSR untouched (its `Limit: crud.MaxLimit`
is a const reference, not a clamp call — nothing to migrate).

**Verification:** per-module + `make check` (30) + `make guard` green
(executor AND main-session fresh re-run). Live pgx (docker postgres:17):
auth 5.6s / cms 3.1s / events 0.5s / jobs 5.8s — ok, port freed. Live
turso (playground gate byte-verified, `-count=1`): auth 377s / cms
171s / events 10.8s / jobs 73s — ok, no tokens. HTTP real-interaction
(auth-cms :8082, authenticated): bare list → default-25 semantics;
`?limit=100` → 200; `?limit=101` → 400 bad_request. Ports freed
(minimal :8081 killed by the main session). File moves to
`.claude/past/pgx-crud-v1/` alongside v1a.
