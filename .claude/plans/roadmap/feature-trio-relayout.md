# Feature trio re-layout — features wear the hexagon's names

Status: **RATIFIED 2026-07-02 (jrazmi)** — L1 `logic/<domain>` (default),
L2 keep `stores/` (default), L3/L4 as proposed; plus two same-session
rulings from the extensibility discussion: **`internal/` is KEPT** (the
seam discipline — Config fields / registered data / ports are the
extension model; a legitimate need inside `internal/` means ADD A SEAM,
not open the interior) and **store posture C**: the framework maintains
the dialect store modules as reference implementations AND workshop v2
gains store scaffolding (`ExportStore`-shaped) so hosts choose import-vs-
own; the migrations + storetest suites are the durable assets under
either; DP1's "out of the box" reads as "ports + conformance suite + a
way to get a store." Executes BEFORE auth-v1 phase 6 so the docs phase
documents the new shape + all three postures exactly once.
Date: 2026-07-02

## Context

jrazmi (2026-07-02, mid-auth-v1): *"I would like to organize all of the
features into inbound, logic, outbound like for all the hexagons... at the
very least I want it laid out in a familiar way."*

The hexagon is already present in every feature but everted at the port
layer and named differently (`<domain>/` at root, `internal/<domain>svc`,
`internal/http`, `stores/`). The hard constraint that forced the eversion
is Go visibility: **hosts and store-adapter modules must import the
entities + repository ports across module boundaries, and Go forbids
importing another module's `internal/`** — so the port layer cannot live
under `internal/` no matter what it's named. Everything else can and
should wear the familiar trio names.

## Target shape (per feature)

```
features/<name>/
  <name>.go                  # the socket: Repositories/Config/Register/(Service) — unchanged
  logic/                     # THE HEXAGON's public rim (L1 default)
    <domain>/                #   entities + repository ports — public BY NECESSITY
  internal/                  # the hexagon's interior — unreachable outside
    logic/<domain>svc/       #   services (business rules)          [was internal/<domain>svc]
    inbound/http/            #   driving adapter (+ views for cms)  [was internal/http]
  storetest/                 # executable spec for logic/'s ports + reference impl (root, spans domains)
  stores/<dialect>/          # the OUTBOUND tier as separate modules (L2 default: name kept)
  theme/                     # cms only — public view seam, unchanged
```

Reading rule (one sentence, goes in the charter): **`logic/` is the public
rim outsiders implement, `internal/` is the interior wearing the same
inbound/logic names as the apps, `stores/` is outbound pushed out of the
module so drivers can't reach the core even by accident.**

## Decisions

| # | Decision | Status | Notes |
|---|---|---|---|
| L1 | Public port layer lives at `logic/<domain>` (e.g. `features/cms/logic/content`, `features/auth/logic/user`) | **Proposed default — YOUR CALL** | Alternatives offered: `logic/domains/<domain>` (gps-360-literal, one level deeper) or keep-at-root (least churn, but the port layer doesn't wear the name — half the ask) |
| L2 | `stores/<dialect>` keeps its name; docs state "stores/ IS the outbound tier, module-ized" | **Proposed default — YOUR CALL** | Renaming to `outbound/<dialect>` changes four module paths and ripples through guard G2's regex, Makefile MODULES/STORE_MODULES, RELEASING, go.work, host go.mods, and two design docs — for cosmetic gain. Cheap only because no tags exist (D8) |
| L3 | Services stay internal (`internal/logic/<domain>svc`) — the charter's "ports public, services internal" rule is unchanged | Proposed default | The app pattern co-locates service+entity+port in one domain package; a feature can't (services would become public API). The split IS the library carve-out, now visible under one `logic` name across both sides |
| L4 | Applies to BOTH shipped features (cms + auth) in one milestone-leg, plus the jobs/events design docs' anatomy trees (text edits) | Proposed default | Half-migrated tree would be worse than either shape |

## Scope of the mechanical refactor (executor: implementer on opus, one leg)

1. `features/auth`: `user/ session/ verification/` → `logic/{user,session,verification}/`; `internal/authsvc` → `internal/logic/authsvc`; `internal/http` → `internal/inbound/http`. Import-path updates in: auth core, `storetest/`, `stores/turso`, `stores/postgres`, `examples/auth-cms` (authmem + main).
2. `features/cms`: `content/ taxonomy/ menus/ media/ messaging/` → `logic/{...}/`; `internal/{entrysvc,taxonomysvc,menussvc,mediasvc,messagingsvc}` → `internal/logic/{...}`; `internal/http` → `internal/inbound/http` (templ `.templ` sources move with it — regenerate via `cd features/cms && go tool templ generate`, never edit `_templ.go`). Import-path updates in: cms core, `storetest/`, both cms stores, `examples/cms`, `examples/minimal` (memstore), `examples/auth-cms` (memstore copy), `theme/` if it references moved packages.
3. Makefile `generate` target: verify the templ walk still finds the moved `.templ` sources (it cd's into features/cms — confirm path assumptions inside).
4. Guards: G2's regex greps import strings (`features/[a-z0-9]+/stores`) and excludes `stores/` — verify unaffected; prove-can-fail once after the move.
5. Charter (`features/README.md`) §2 anatomy table rewritten to the trio shape + the app↔feature mapping table (from the 2026-07-02 discussion); ARCHITECTURE.md's feature section updated to match. (These doc edits may fold into auth-v1 phase 6, which runs immediately after — implementer does the code, phase 6 does the docs.)
6. jobs + events design docs: anatomy trees updated to the trio shape (text-only; their milestones are unstarted, so this is free).

## Acceptance

```sh
make check                                   # all modules + guards green
grep -rn "features/auth/user\|features/auth/session\|features/auth/verification" . --include='*.go' | grep -v logic   # empty (no stale import paths; same pattern for cms domains)
```
- Real-interaction: standing check (a) AND the full five-step flow (b)
  against examples/auth-cms — both must pass post-move.
- Live-store gates re-run per dialect (the moves touch store-module
  imports): auth turso (playground, authorized), auth+cms postgres (local
  docker), cms turso (playground).

## Sequencing

Ratify L1/L2 → one implementer leg (the mechanical move + verification) →
auth-v1 phase 6 (docs, documenting the NEW shape) → milestone close →
jobs-v1 continues per the roadmap. The loop stays paused until
ratification.

## Execution log

### 2026-07-02 — executed (loop interleg; implementer on opus)

Both features re-laid out per L1–L4: `logic/{user,session,verification}`
and `logic/{content,taxonomy,menus,media,messaging}` public rims;
`internal/logic/<domain>svc` + `internal/inbound/http` interiors (cms
`.templ` sources moved with http; regenerated via `go tool templ
generate`; drift gate clean). All intra-module moves — zero module-path
changes, go.work/Makefile untouched. Import updates across both cores,
both storetests, all four store modules, three example hosts, cms theme.
G2 prove-can-fail exercised post-move (forbidden import → guard exit 2 →
restored → green).

Acceptance (all FIRSTHAND after the implementer's own green run):
`make check` → "all checks passed" (13 modules, 4 guards, templ drift
clean); stale-path greps → 0 hits; tree shape verified = the ratified
target. **All four live conformance legs re-run green post-move**:
cms×postgres ok 1.03s + auth×postgres ok 0.54s (local docker
postgres:17); cms×turso ok 71.6s + auth×turso ok 28.9s (authorized
playground, URL verified). Flow (b) firsthand: 401 → 201 → 200+cookie →
200 → 200 → 401; port free. examples/minimal boot: 200/200 (implementer
run).

Unverified: nothing. Charter/ARCHITECTURE doc updates deliberately
deferred to auth-v1 phase 6 (next leg), which documents the trio shape,
the app↔feature mapping table, and the internal/store posture rulings.

## Non-goals

- No behavior changes, no port/signature changes, no new packages beyond
  the moves.
- No `outbound/` module renames unless L2 flips.
- No change to `<name>.go`'s socket role or the charter's visibility rules.
