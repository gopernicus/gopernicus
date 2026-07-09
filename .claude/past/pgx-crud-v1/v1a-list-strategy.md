# pgx-crud-v1a — explicit pagination Strategy + connector flow split

Status: **RATIFIED 2026-07-08 (jrazmi, in-conversation — post-close
amendment to pgx-crud-v1; owner: "1) do it, 2) yeah… default strategy
could be an env var, but the struct tag could default to cursor")**
Executor model: opus
Depends on: pgx-crud-v1 (CLOSED same day; this amends its shipped API)

## Why

Two owner findings against the shipped `List[T]`:
(1) the single function interleaves cursor-only and offset-only steps
around a shared middle — offset mode is defined by negation (post-hoc
`page.NextCursor = ""`), which reads muddy; (2) mode selection by
`Offset > 0` is inference magic — `?offset=0` silently means cursor
mode, and a programmatic `ListRequest{Offset: 3}` flips modes with no
named intent. Option "split the public API per mode" was considered and
REJECTED (the Reader port hands stores one ListRequest; a public split
just copies the dispatch into every store).

## Ratified design

**sdk/crud — explicit strategy, break-once-additive-forever parse:**

```go
type Strategy string

const (
    StrategyCursor Strategy = "cursor" // the default
    StrategyOffset Strategy = "offset"
)

type ListRequest struct {
    Limit     int
    Cursor    string
    Offset    int
    Order     Order
    WithCount bool
    Strategy  Strategy // "" = default (cursor)
}

// ResolvedStrategy returns StrategyCursor when Strategy is empty.
func (r ListRequest) ResolvedStrategy() Strategy

// ParseListRequest — the five-string signature folds into ONE options
// struct (the Pager lesson: break once, additive forever).
type ListParams struct {
    Limit    string
    Cursor   string
    Offset   string
    Count    string
    MaxLimit int
    // DefaultStrategy applies when neither cursor nor offset params are
    // present; "" means StrategyCursor. Hosts populate it from an
    // env-tagged config field (`default:"cursor"` — sdk/config
    // ParseEnvTags), never from os.Getenv inside sdk/crud.
    DefaultStrategy Strategy
}

func ParseListRequest(p ListParams) (ListRequest, error)
```

Validate (per-strategy, NO silent inference — the old `Offset > 0`
mode selection is REMOVED):
- unknown Strategy value → error wrapping ErrInvalidInput.
- cursor strategy (incl. ""): `Offset != 0` → error (loud, was silent
  mode-flip).
- offset strategy: `Cursor != ""` → error; `Offset >= 0` valid —
  **`offset=0` is now expressible offset mode** (fixes the wart).

Transport-edge strategy resolution in ParseListRequest (no new query
param — the vocabulary stays limit/cursor/offset/count/order):
- offset param present (even "0") → StrategyOffset.
- cursor param present → StrategyCursor.
- both present → error (unchanged).
- neither → p.DefaultStrategy (→ cursor when "").

**Connectors — internal flow split (both dialects):** `List` keeps its
signature; body becomes validate → resolveOrder → `switch
req.ResolvedStrategy()` into private `listCursor()` / `listOffset()`,
each linear top-to-bottom, sharing the run-query/collect and `count`
helpers. `listOffset` computes HasMore from its own limit+1 over-fetch
and never encodes cursors — the post-hoc stripping goes away.

**Edge wiring:** `authentication.Config` gains
`ListStrategy string` with env tag (name per the package's existing env
prefix convention) and `default:"cursor"`; its shared parse helper
passes it as DefaultStrategy (loud validation at NewService if the value
is neither cursor nor offset — the Config posture). cms SSR stays
cursor-only (unchanged semantics; its handlers never parse offset).

**Ripple (mechanical):** every in-repo constructor of an offset-mode
ListRequest sets Strategy explicitly — 4 storetest six-case families
(OffsetMode + CursorOffsetExclusive extended: cursor-strategy+offset ⇒
invalid, offset-strategy+cursor ⇒ invalid), 4 memstores (authmem,
minimal, auth-cms, jobs) switch on ResolvedStrategy, machine.go moves to
ListParams. Docs: crud package-doc matrix, pgxdb/turso READMEs,
features/README checklist item 13.

## Out of scope

- Public per-mode List functions (rejected above).
- A `strategy` query param (revisit on demand).
- Per-route strategy restriction knobs (one-line edge rejection when a
  route needs it; generalize on the second occurrence).

## Verify

Per-module build/vet/test for sdk, both connectors, all four features +
storetests, all example hosts; `make check` + `make guard`; live legs:
pgx (docker postgres:17) all four store modules, turso (playground URL
gate) all four `-count=1`; real-interaction: auth-cms JSON leg proving
`?offset=0` is now offset mode (no cursors in response, has_prev false)
and `?offset=2` unchanged, plus the standing minimal/cms boots.

## Execution log

### 2026-07-09 — executed (implementer on opus); AMENDMENT COMPLETE

Landed exactly as pinned. sdk/crud: `Strategy` type +
`StrategyCursor`/`StrategyOffset`, `ListRequest.Strategy`,
`ResolvedStrategy()`, per-strategy `Validate` (unknown strategy /
cursor-strategy-with-offset / offset-strategy-with-cursor / negative
offset all reject wrapping ErrInvalidInput), **`UsesOffset` removed
repo-wide** (post-check grep → 0), `ParseListRequest(ListParams)` with
presence-based strategy resolution (offset param even "0" → offset).
Connectors: `List` switches once on ResolvedStrategy into linear
`listCursor`/`listOffset` (shared collect/count helpers; post-hoc
cursor-stripping deleted), same shape both dialects. authentication:
`Config.ListStrategy` `env:"AUTH_LIST_STRATEGY" default:"cursor"` +
`ErrInvalidListStrategy` loud validation in NewService, threaded through
Mount to the parse helper as DefaultStrategy. Storetest families ×4:
OffsetMode on explicit Strategy + new **OffsetZero** assertion,
CursorOffsetExclusive → the per-strategy rejection pair; memstores ×4 +
reference pagers on ResolvedStrategy. Docs synced (crud matrix, both
connector READMEs).

**Flagged executor decision, accepted:** authentication had NO existing
env-tag convention (its Config is host-built literally, never through
ParseEnvTags) — tag named `AUTH_LIST_STRATEGY` matching the auth-cms
host's `AUTH_*` prefix. Consequence recorded: the `default:"cursor"`
tag bites only for hosts using sdk/config.ParseEnvTags; a literal
Config with empty ListStrategy resolves to cursor via
resolveListStrategy (matches the ratified "empty → cursor" semantics);
any non-empty typo errors loudly.

**Verification:** per-module + `make check` (30) + `make guard` green
(executor AND main-session fresh re-run). Live pgx (2026-07-09, docker
postgres:17): pgxdb 0.56s / auth 5.65s / cms 3.40s / jobs 5.47s /
events 0.53s — ok. Live turso (2026-07-09, playground gate,
`-count=1`): auth 390.1s / cms 192.8s / jobs 78.1s / events 11.6s —
ok, no tokens. HTTP real-interaction (auth-cms :8082, authenticated):
bare list → cursor mode with next_cursor; `?offset=0&limit=2` → **200
offset mode** (no cursor fields, has_prev false — the previously
inexpressible case); `?offset=2` rows == cursor page 2, has_prev true,
no cursors; `?cursor=X&offset=1` → 400. Ports freed. File moves to
`.claude/past/pgx-crud-v1/` alongside its parent milestone.
