# Phase 02 — route ergonomics (flag #4): feature.Methods sugar; FS7 route.go demotion; override patterns documented

Status: **RATIFIED 2026-07-08 (jrazmi, in-conversation, incl. D2 = delete) — EXECUTING**
Milestone: `segovia-lessons` (see `00-overview.md`)
Executor model: **opus** for code tasks (1–3), **fable** for docs (4)
Depends on: phase 01 (EXECUTED — the `internal/inbound/<feature>/` packages
this phase edits exist because of it)
Size: S

## The flag (input of record — owner-raised in-session, 2026-07-08; NOT from Segovia's flags doc)

Feature route tables are stringly (`r.Handle("POST", "/auth/register", …)`)
while Segovia's app-local domain reads `router.POST("/api/v1/dashboards",
h.create)` — the sdk's own `web.WebHandler` method helpers
(`sdk/web/methods.go`, present since the initial commit) are unreachable
through the one-method `feature.RouteRegistrar`. Owner direction after the
full seam discussion (RouteRegistrar retained deliberately — host router
freedom, per-route interposition, capability limiting): keep the interface
exactly as-is, add sugar so a feature's `routes.go` READS like an app
domain's, and while here (a) delete the unused FS7 data form and (b) write
down the override story hosts will ask for.

## Decisions

### D2 — FOR RATIFICATION: delete `sdk/feature/route.go` (FS7 demotion)

`Route` + `RegisterRoutes` (36 lines + 74 test lines) have ZERO consumers —
no feature, no host, no example. FS7 shipped the data form ahead of demand,
against the repo's own wait-until-needed discipline. **Recommendation:
DELETE**, with a supersession marker on FS7 in `features/README.md` (never a
silent drop): the data form returns when a real host needs a declarative
route table (the recorded trigger). Alternative (keep, documented) preserves
36 dead lines and a standard nothing exercises — declined-by-default.

### D3 — RATIFIED with the flag (owner Q&A 2026-07-08): helper breadth = PARITY, not coverage

`feature.Methods` mirrors `web.WebHandler`'s helper set ONE-TO-ONE
(GET/POST/PUT/DELETE/PATCH) and nothing more. Declined with rationale:
**HEAD** — Go 1.22+ `ServeMux` method patterns match HEAD against GET
registrations already; **OPTIONS** — CORS preflight is middleware, not a
route-table concern; **CONNECT/TRACE** — no app code registers these. The
standing rule: if `sdk/web/methods.go` ever grows a verb, `feature.Methods`
grows it in the same commit (parity is the invariant; task-1 pins it with a
reflection test). Consult caveat folded: the HEAD ruling holds *because*
`web.WebHandler.Handle` emits method-scoped `"GET /path"` patterns
(handler.go:64–67) and Go 1.22 ServeMux matches HEAD against GET patterns —
if Handle's pattern-building ever changes, D3's HEAD decline re-opens.

## Design (task-1 shape)

```go
// sdk/feature/methods.go — house style follows PrefixRegistrar/Group:
// exported struct + Next field, no constructor.
type Methods struct{ Next RouteRegistrar }

func (m Methods) GET(path string, h http.HandlerFunc, mw ...web.Middleware)  { m.Next.Handle("GET", path, h, mw...) }
// POST, PUT, DELETE, PATCH identically.
// Methods also implements Handle (delegate to Next) so it IS a
// RouteRegistrar — it composes under/over PrefixRegistrar and Group and can
// be passed anywhere the interface is expected.
```

Feature usage: `rt := feature.Methods{Next: r}` at the top of `Mount`, then
`rt.POST("/auth/register", h.register)` — reads identically to Segovia's
`dashboards/routes.go` while the host keeps the seam.

## Out of scope

- Any change to `RouteRegistrar`, `Mount`, `PrefixRegistrar`, `Group`
  (the seam ruling stands — composition, never widening).
- New verbs on `sdk/web/methods.go` (D3).
- Route PATH changes anywhere; behavior changes anywhere.

## Module / API impact

`sdk` public API grows one exported type (`feature.Methods`) and — under D2
— loses two exported symbols (`feature.Route`, `feature.RegisterRoutes`).
Zero tags exist, so no release implication; FS7 supersession marker records
the charter amendment. Feature modules: internal-only edits.

## Tasks

### task-1: `sdk/feature/methods.go` + test

- **model:** opus — **files:** [sdk/feature/methods.go, sdk/feature/methods_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`; `make check`
- Five verb methods + `Handle` delegate per the design block; doc comment
  states the D3 parity rule and points at the seam rationale in
  `features/README.md` §4. Test: verb→Handle mapping, middleware
  passthrough order, composition (Methods over Group over PrefixRegistrar
  registers the fully-wrapped route), and — REQUIRED, not optional — the
  **parity pin**: a reflection test asserting `feature.Methods`' verb-method
  set equals `web.WebHandler`'s helper set (both live in the sdk module, so
  the import is legal).

### task-2: CONDITIONAL on D2 — delete route.go/route_test.go

- **model:** opus — **files:** [sdk/feature/route.go (delete), sdk/feature/route_test.go (delete)]
- **verify:** `make check` (proves zero consumers); `git grep -n "RegisterRoutes\|feature.Route{"` returns only docs/plans
- Pure deletion. The FS7 marker lands in task-4, same phase.

### task-3: convert ALL feature route registrations to Methods style

- **model:** opus — **files:**
  [features/authentication/internal/inbound/authentication/{routes,oauth,machine,invitation}.go,
  features/cms/internal/inbound/cms/routes.go,
  features/events/internal/inbound/events/routes.go]
- **verify:** per-module build/test/vet ×3; `make check` + `make guard`;
  behavior-neutral diff (registration lines only); the widened acceptance
  grep below returns nothing; run-and-look BOTH legs as phase 01
  (examples/cms pages render 200; auth-cms register→verify→login→logout
  codes unchanged — this drive also proves the clientInfo wrap survived)
- `rt := feature.Methods{Next: r}` then verb calls. Authentication
  registers routes in FOUR files (consult must-fix: 26 calls total, 18
  outside routes.go) — all four convert; a partial conversion is the
  false-green shape this repo's history warns about. **Ruled, not
  executor's call: the `mountX` helpers take `feature.Methods`**
  (`mountOAuth(rt feature.Methods, …)` etc.). Safety traced by the
  consult: `Mount` wraps `r = clientInfoRegistrar{inner: r}` BEFORE
  building `rt`, so Methods → clientInfoRegistrar → host router and the
  blanket client-info middleware is preserved on every resource route;
  value-copy is semantically a no-op. Middleware-carrying routes
  (`svc.RequireUser`, cms adminMW, events cfg.Middleware) keep their
  variadic tails; cms's registry-loop closures convert mechanically.

### task-4: docs — override story + FS7 marker + NOTES

- **model:** fable — **files:** [features/README.md, NOTES.md]
- **verify:** `make guard`; cross-read §2/§4 for contradiction with phase
  01's text
- `features/README.md` §4 gains the route-override story (the owner's own
  question, answered durably) — framed per the consult fold as a
  **drill-down of the EXISTING §2 four-tier extension model + C1**, never a
  competing numbered taxonomy: skip-`Register`-and-hand-route is tier 4
  ("extend past the feature") applied to routes; `PrefixRegistrar`/`Group`
  is C1's existing relocation content; the genuinely NEW ink is the middle
  move — **wrap the registrar for per-route deny/replace/re-path**, with
  the ~8-line `inviteOnly` wrapper example (it is the FS7-alternative that
  justifies keeping `RouteRegistrar` narrow). §2 anatomy row: route tables
  written via `feature.Methods` (parity rule noted). **Rewrite — not
  annotate — the FS7 sentence at features/README.md:97** ("route tables
  are built as data (`[]feature.Route`) internally" — already false today,
  and about to reference a deleted type): the truthful claim is verb calls
  via `feature.Methods`; the `[]Route` data form was cut as
  FS7-premature with the resurrect trigger recorded (supersession marker).
  NOTES.md dated entry: flag #4, D2/D3, diffs, drive results.

## Sequencing

task-1 → task-2 (D2-gated) and task-3 (either order) → task-4. Each task a
CI-green commit; final `make check` closes the phase.

## Acceptance

```sh
make check && make guard
git grep -n '\.Handle("' features/*/internal/inbound/*/*.go  # ZERO hits — every registration in every resource file converted
```

Run-and-look both legs per task-3. Green tests alone close nothing.

## Consultation notes

`lead-backend-engineer` reviewed the cut (2026-07-08): **ship-with-edits**,
all folded — (1) task-3 scope widened to authentication's four route files
+ acceptance grep widened to `*/*.go` (the 70%-unconverted false-green
catch); (2) `mountX` takes `feature.Methods`, ruled with the traced
clientInfoRegistrar-wrap safety argument; (3) README:97's already-false FS7
claim gets rewritten, not annotated; (4) override story framed as a
drill-down of the §2 four-tier model + C1; (5) parity test promoted to
required; D3's HEAD rationale pinned to Handle's pattern-building. Verified
by the consult: route.go has zero consumers; no guard references it;
Methods' value-copy and non-embedded design are safe; HEAD-via-GET holds
for this codebase's pattern construction.

## Open questions

1. **D2** (delete route.go) — recommendation: delete. The only gate.

## Execution log

(append dated entries here)
