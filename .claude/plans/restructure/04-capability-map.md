# Phase 4 — capability map: everything the original does, classified into the new structure

Status: READY — ratified 2026-07-02
Depends on: 03-feature-contract.md (the contract capabilities map onto)

## Goal

A ratified table classifying **every** capability of
`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original` into exactly one
target home — `sdk` facility / `integrations/<cat>/<tech>` / `features/<name>` /
workshop-v2 (codegen) / drop — plus a design sketch for the **auth feature**, which
is the acid test of the feature contract (cross-feature identity, middleware,
rate limiting). Output is analysis + documents, minimal-to-zero code.

## Context an executor needs

Original repo layout (single module `github.com/gopernicus/gopernicus`):
`bridge/` (HTTP driving adapters), `core/` (domain + embedded driven adapters —
the flaw the new repo fixes), `infrastructure/` (adapters + ports-owned-by-infra),
`sdk/` (stdlib-only utils), `telemetry/`, `workshop/` (codegen + CLI). Key
reference points:

- **The good pattern to generalize**: `core/auth/authentication/authenticator.go`
  declares its own ports (`PasswordHasher`, `JWTSigner`, `UserRepository`,
  `SessionRepository`, ...) satisfied structurally by
  `infrastructure/cryptids/{bcrypt,golangjwt}` with zero imports from core.
- **The engine worth salvaging for codegen**: `infrastructure/database/crud/`
  (`Spec[T,F,C,U]`, generic `Store`, `Dialect` port with postgres/sqlite impls,
  `Querier`) + its generator entry `workshop/codegen/generators/specstore.go`.
- **The conformance suites**: `infrastructure/{cachetest,storagetest,
  ratelimitertest,cryptidstest,eventstest}` (phase 2 already ported the pattern).
- Seed inventory (from the 2026-07-02 review; executor verifies + completes by
  walking the original tree — this list is a starting point, NOT the deliverable):
  authentication, authorization, ReBAC, tenancy, invitations, sessions, api keys,
  oauth/OIDC, jobs (queue + schedules + cron), events/outbox + SSE, telemetry
  (OTEL logging/tracing/metrics), rate limiting, redis cache, GCS + S3 storage,
  sendgrid email, postgres/pgx, sqlite, validation, fop (filter/order/pagination),
  async + workers pools, conversion helpers, OpenAPI 3.1 generation, TS client
  generation, migrations tooling (`db migrate/reflect/status/create`), `doctor`,
  `init`/`new` scaffolding, allowlist middleware, bridge transit middleware stack.

## Preconditions

1. Phase 3 merged: features/README.md charter exists (the auth sketch must cite
   its checklist by item number).
2. Read-only access to `../gopernicus-original` confirmed.

## Work items

### W1 — the capability inventory (exhaustive)

Walk `gopernicus-original` top-level by top-level (bridge, core, infrastructure,
sdk, telemetry, workshop) and produce
`.claude/plans/restructure/capability-map.md` — a table:

| capability | where it lives in original (paths) | target home | rationale (1–2 lines) | depends on | size (S/M/L) |

Classification rules (apply in order):

1. Needs a third-party lib and implements an sdk facility port → **integration**
   (`integrations/caches/redis`, `integrations/filestores/{gcs,s3}`,
   `integrations/email/sendgrid`, `integrations/datastores/postgres`).
2. Domain logic with its own entities/storage that a host mounts → **feature**
   (auth — including sessions/api-keys/oauth/invitations; jobs; events/outbox;
   tenancy: decide feature vs auth-subdomain and justify).
3. Stdlib-expressible, framework-generic, passes sdk/README's admission policy →
   **sdk** (validation? async/workers? conversion subset? OpenAPI-from-routes?
   apply the policy honestly — "we might need it" fails criterion 1).
4. Generation-time only → **workshop v2** (crud Spec engine, queries.sql
   annotation parser, bridge generation, TS client, migrations CLI, doctor,
   init/new scaffolds).
5. Nothing above → **drop** (with one line of why; dropping is a valid outcome —
   e.g. telemetry may be "park until a real observability requirement", which is
   a documented drop, not a silent one).

Every seed-inventory item must appear exactly once; anything discovered beyond the
seed list gets added. Mark contested rows `YOUR CALL` with a recommended default —
expected contested rows: telemetry/OTEL scope, tenancy placement, ReBAC (full
relationship engine vs simpler roles first), OpenAPI (sdk/web vs workshop).

### W2 — the auth feature design sketch

Write `.claude/plans/restructure/auth-feature-design.md` (design doc, no code),
covering:

1. **Scope v1**: password auth + sessions + middleware identity; name what v1
   excludes (oauth, api keys, invitations, ReBAC) and where each lands later.
2. **Module shape** per the charter: `features/auth` core (entities: user,
   session; ports: user/session repositories, `PasswordHasher`, token signer —
   port the original's authenticator port list, citing
   `core/auth/authentication/authenticator.go`), `features/auth/stores/<dialect>`,
   bcrypt placement (needs `golang.org/x/crypto` → an integration module or a
   documented exception; recommend `integrations/cryptids/bcrypt` mirroring the
   original's naming).
3. **The contract stress points** (this is the real content — each gets a
   proposed answer plus what it demands of `sdk`/`Mount`):
   - middleware: how does a feature export HTTP middleware for OTHER routes
     (cms admin) to use? (Likely: feature exposes `RequireUser` middleware as a
     value the host passes into other features' Config/mount — no Mount change.)
   - identity-in-context: which package owns the context key + accessor? (This is
     the C2 worked example from the charter — likely a tiny sdk contract if BOTH
     cms and auth need the same vocabulary; apply the admission policy.)
   - rate limiting: wire `sdk/ratelimiter` (phase 2 W5) into login attempts.
   - migrations: auth's namespace in the shared ledger alongside cms's.
4. **Proof plan**: an `examples/minimal`-style zero-infra host running auth+cms
   together — the first real two-feature composition, exercising charter rule
   "no feature→feature imports" in practice.
5. **Checklist trace**: walk features/README.md's authoring checklist item by
   item, stating how the design satisfies each.

### W3 — adversarial review of the sketch

Spawn a fresh subagent to attack the auth sketch against the constitution
(00-overview.md) and the charter: hunt for hidden feature→feature imports, ports
owned by implementors, Mount bloat, sdk admission-policy violations, init()
temptations. Fix the sketch until the attack comes back clean; record the attack
findings + resolutions in the design doc's appendix.

### W4 — sequence the backlog

Append to capability-map.md a recommended build order for the next milestones
(auth feature → its integrations → jobs/events → telemetry decision → workshop
v2), with one-line justifications (auth first: it's the contract acid test AND
the original's most mature domain; workshop last: per the standing rule, codegen
follows design).

## Acceptance

- `capability-map.md`: every original top-level capability classified exactly
  once; contested rows flagged with defaults; no "TBD" cells.
- `auth-feature-design.md`: all five sections present; checklist trace complete;
  adversarial-review appendix shows zero unresolved violations.
- `make check` still green (this phase should touch no code; if it did, that's a
  flag).

## Real-interaction check

Standing check from 00-overview.md (the tree must still build/boot even though
this phase is analysis — it proves no accidental code drift).

## Out of scope

- Implementing the auth feature or any integration (next milestone).
- Workshop v2 design beyond classification (phase 5 holds the brief).

## Execution log

### 2026-07-02 — phase 4 executed

**Preconditions.** Phase 3 confirmed merged (`features/README.md` charter exists
with a 9-item authoring checklist, `04-capability-map.md`'s dependency); read-only
access to `../gopernicus-original` confirmed (730 `.go` files across
`bridge/core/infrastructure/sdk/telemetry/workshop`). Repo is not a git repository
— no commits possible, working-tree only, matching phases 1–3.

**W1 — capability inventory: DONE.** Three parallel research passes (bridge/;
core/+infrastructure/; sdk/+telemetry/+workshop/), each independently verifying
tree completeness before reporting, produced `.claude/plans/restructure/
capability-map.md` — 62 rows covering every seed-inventory item (several expand
into multiple rows per the original's own file layout: auth entities split into
required-vs-optional per the original's own `Authenticator.Repositories`
boundary; telemetry split into logging/already-done vs tracing/new vs
metrics/nonexistent; jobs split into the generic worker pool (sdk-shaped) vs the
cron-scheduling domain (feature-shaped)) plus items discovered beyond the seed:
the crud `Spec[T,F,C,U]` engine's actual maturity (a hand-written "golden
reference," not yet generator-driven even in the original — directly informs the
phase-5 brief's carry-over claim), the generic `httpc` JSON client, `sdk/
conversion`/`sdk/fop` overlap with the new repo's already-existing `sdk/slug`/
`sdk/repository`, and the finding that `modernc.org/sqlite` capability is already
folded into `integrations/datastores/turso`'s own dependency tree (no separate
sqlite integration needed). Nine YOUR CALL rows recorded, each with a
recommended default (four match the phase file's "expected contested" list —
telemetry, tenancy, ReBAC, OpenAPI; OpenAPI resolved cleanly rather than staying
open, since runtime-reflection-vs-generation-time turned out to be a clean
dividing line; five more surfaced during the walk — event-bus home, cron-parsing
dependency placement, `sdk/conversion` scope, the `sdk/fop` authorization-aware
gap, and the integration-test harness's workshop-v2-ness).

**W2 — auth feature design sketch: DONE.** `.claude/plans/restructure/
auth-feature-design.md` written, all five required sections present: (1) scope
v1 (password auth + sessions + `RequireUser`, mirroring the original's own
required-vs-optional `Repositories` boundary — not an arbitrary new cut); (2)
module shape (5 v1 ports across `user`/`session`/`verification`, `PasswordHasher`
declared feature-side per the phase file's own "good pattern to generalize" callout,
`integrations/cryptids/bcrypt` as the adapter, zero view deps — JSON-API-only,
leaner than cms); (3) the four contract stress points, each answered concretely
(middleware via a `Service`/`NewService` pair added to `auth.go`; identity-in-
context kept feature-internal in v1, not sdk — see W3; rate limiting via
`Config.RateLimiter` defaulting to the D6 `ratelimiter.Memory`; migrations
namespaced `"auth"` alongside `"cms"`); (4) a zero-infra two-feature proof-host
plan (auth+cms, in-memory stores + real `integrations/cryptids/bcrypt` — "zero
infra" means no network/datastore, not zero third-party libs), explicitly citing
and carrying forward two OPEN phase-2 findings the proof-plan author must not
re-trip: `email.NewConsole(nil)` panicking despite its nil-discard promise, and
memstore's term/menu uniqueness divergence (the new auth-local store must not
repeat that silently — assert or flag, don't paper over); (5) a full checklist
trace against `features/README.md` §8's 9 items, item by item.

**W3 — adversarial review: DONE, two real findings, both resolved; clean on
re-attack.** First pass (fresh subagent, given only the design doc +
`00-overview.md` + `features/README.md`) found: (1) the original design's
proposed `sdk/identity` package failed sdk's own plurality test on inspection —
cms only ever calls `auth.Service.CurrentUser(ctx)`, never the context key
directly, so there was exactly one real consumer, not two, and the draft applied
a looser standard to `identity` than it applied to `PasswordHasher` in the same
document; (2) `Service`/`NewService` lived in a separate `service.go`, silently
contradicting `features/README.md` §2's "`<name>.go` is the feature's entire
host-facing surface" claim. Both fixed: identity-in-context stays unexported
inside `features/auth` in v1 (a concrete graduation trigger named, not left
open-ended); `Service`/`NewService` moved into `auth.go` itself. `capability-map.md`'s
identity-in-context row updated to match (target home changed from sdk to
feature-internal). A second adversarial pass (fresh subagent) re-verified both
fixes against the actual reasoning (not just the wording) and re-swept the whole
document; found one non-blocking documentation nit (the charter's anatomy table
enumerates `<name>.go`'s contents as a closed three-item list that doesn't yet
account for a feature needing additional host-facing exports — flagged for
jrazmi below, not fixed in this phase since it's a charter edit, not this design
doc's job) and confirmed zero unresolved rule/checklist violations. Findings +
resolutions recorded in `auth-feature-design.md`'s Appendix.

**W4 — backlog sequencing: DONE.** Appended to `capability-map.md`: auth v1 →
its blocking integrations (bcrypt, postgres) → the auth+cms cross-feature proof →
jobs (worker pool + cron scheduling) → events (bus port/Mount decision + outbox +
SSE gateway, sequenced after jobs to reuse its worker infrastructure) →
telemetry execution (after domain features exist, so there's something to trace)
→ remaining integrations (as each becomes a real blocker, no forced order) →
workshop v2 (last, per the standing "codegen follows design" rule — restated
with the concrete justification that auth/jobs/events give the structure a
second, third, and fourth proof beyond cms before generation targets it).

**Acceptance: all green.**
```
grep -rn "TBD" capability-map.md auth-feature-design.md   # zero matches
```
`capability-map.md`: 62 rows, every seed-inventory item classified exactly once
(several expand into multiple rows, none skipped), 9 YOUR CALL rows each with a
recommended default, no TBD cells. `auth-feature-design.md`: all five W2 sections
present, checklist trace complete (9/9 items), adversarial appendix shows two
findings from pass 1, both resolved, zero unresolved after pass 2.
```
make check   # PASS — templ no-drift, 6 modules vet+build+test, 4 guards
```
No files outside the two deliverables (plus this phase file) were touched —
confirmed via `find . -newer 03-feature-contract.md`, empty diff against the two
expected paths.

**Real-interaction check: PASS.**
```
cd examples/minimal && go run ./cmd/server    # localhost:8081 (default, confirmed in main.go)
GET http://localhost:8081/                         -> 200 (<title>Home</title>)
GET http://localhost:8081/products/widget-3000     -> 200 (<title>Widget 3000</title>)
```
Server killed (`lsof -ti :8081 | xargs kill -9`); `lsof -i :8081` empty
afterward — port confirmed free. No code was touched this phase, so this check
exists purely to prove no accidental drift, per the phase's own framing; it
passed cleanly with zero surprises.

**Out-of-scope confirmed untouched:** no auth feature code built, no
integration modules built, no workshop v2 generator work, no `Mount` field
added, no tags cut. The three phase-2 OPEN production findings (email Console
nil-logger panic; memstore term/menu uniqueness divergence) remain untouched
and unresolved, still awaiting jrazmi's ruling — both are explicitly carried
forward into `auth-feature-design.md` §4 as gotchas the next milestone's
implementer must not re-trip, not silently dropped.

**Flags for jrazmi:**
1. Nine YOUR CALL rows in `capability-map.md` need ratification before the
   backlog they inform is built (list + recommended defaults in the map's
   Summary section).
2. `features/README.md` §2's anatomy table enumerates `<name>.go`'s contents as
   a closed three-item list; the auth design legitimately needs more
   (`PasswordHasher`, `Service`, `NewService`) under the same "entire
   host-facing surface" umbrella. Recommend generalizing that one sentence the
   day `features/auth` is actually built — not urgent, not blocking, surfaced
   by the W3 adversarial pass (finding 3, pass 2).
3. The design doc's proof plan (§4) surfaces that **`examples/cms` and
   `examples/minimal`'s admin routes are currently unauthenticated** — not a
   phase-4 regression, a pre-existing gap this design exercise made concrete.
   Exercising the auth+cms cross-feature proof for real will need a small,
   new `cms.Config` middleware hook (e.g. `AdminMiddleware`) — genuine
   next-milestone code, flagged here so it isn't a surprise then.
4. The two phase-2 OPEN findings (email Console nil-logger panic; memstore
   uniqueness divergence) still await a ruling; this phase adds no new
   urgency but the auth proof plan will collide with both directly.

**Not started:** implementing `features/auth`, any integration module, or
workshop v2 (all explicitly out of this phase's scope per the phase file). Per
the loop protocol, stopping here — no further phases run in this leg.

**2026-07-02, orchestrator addendum (post-executor):** independently re-ran
`make check` — green (6 modules, 4 guards), confirming this analysis phase touched
no code. Deliverables verified on disk: capability-map.md (62 rows, 9 YOUR CALL),
auth-feature-design.md (471 lines, adversarial appendix clean). The 9 YOUR CALL
rows + the unauthenticated-admin-routes observation are queued for jrazmi before
the post-restructure backlog is built.
