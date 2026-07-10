# sdk-layering — kernel/foundation/capabilities: the intra-sdk import law

Status: **RATIFIED 2026-07-10 (jrazmi, in-session) — the full design
conversation is the record. Principle (verbatim): "Under no circumstances
do I want an sdk package importing another sdk package UNLESS we break it
out into clear edges between foundational layer sdk packages … that are
truly completely agnostic and feature/service type layers." Rulings:
kernel = the ROOT `package sdk` (`sdk/errors.go` — compiler-enforced
leaf); tier name = `capabilities` (not "services"); MailerBridge → an
INTEGRATION (taxonomy amended: composes sdk capabilities); `feature` =
the ONE sanctioned composition package. Physical split ratified
("churn fine, correctness wanted"). Gate runs pre-execution. EXECUTING
after the fold.**
Executor model policy (standing): implementation `model: opus`;
design/doc `model: fable`. Modules: **+1 → 36**
(`integrations/notify/mailer`).

## The law (goes verbatim into ARCHITECTURE at P5)

```
sdk/                      ROOT package sdk — the KERNEL (errors.go).
                          May import: stdlib only. Compiler-enforced:
                          the root package cannot import its own
                          subpackages without a cycle.
  foundation/<pkg>        pure mechanism/vocabulary, zero service
                          semantics. May import: the ROOT only. FLAT —
                          no foundation→foundation edges.
  capabilities/<pkg>      capability ports + first-party defaults.
                          May import: root + foundation. NEVER another
                          capability.
  feature/                the ONE sanctioned composition package (the
                          mount contract composes by definition). May
                          import anything in sdk.
```

Cross-CAPABILITY composition never lives in sdk: capability×foundation
composition lives in the capability that owns the semantics (the cache
middleware belongs to cacher, not web); capability×capability
composition leaves sdk (integrations, features, hosts).

## Tier assignments (ratified table)

- **Kernel (root `package sdk`)**: the current `sdk/errs` contents
  (sentinels + IsExpected), as `sdk/errors.go`. Nothing else today;
  promotion to kernel = adding a file to the root package, a visible
  deliberate act.
- **foundation/**: web (post-purge), workers (post-de-import), identity,
  crud, cryptids, validation, logging, conversion, slug, async,
  environment.
- **capabilities/**: cacher (+ its web middleware, evicted from web),
  tracing (+ its web middleware, same), email, notify (port + Console;
  MailerBridge leaves sdk), oauth, filestorage, ratelimiter, events.
- **feature/**: stays at `sdk/feature` — tier 3, the sanctioned
  composer.

## Phases

| Phase | What | Size | Model |
|---|---|---|---|
| P1 | kernel: errs → root `package sdk` + repo-wide rename | M (wide, mechanical) | opus |
| P2 | the evictions + de-imports (web purge; workers structural tracer) | M | opus |
| P3 | the physical split (foundation/ + capabilities/) + repo-wide import sweep + workshop templates | L (mechanical, everything moves) | opus |
| P4 | `integrations/notify/mailer` (module 36) + taxonomy amendment | S | opus |
| P5 | guard G12 + ARCHITECTURE/docs + close | S–M | fable |

Ordering: P1 first (everything references errs); P2 before P3 (move
clean packages, not packages mid-surgery); P4 anytime after P2 (the
bridge's source is stable once notify stops importing email); P5 last.
One CI-green commit per phase; the workshop scaffold-compile tests run
inside make check EVERY phase — they emit sdk import paths, so they are
the canary for a missed template.

### P1 — the kernel

- `sdk/errors.go` (+ `sdk/errors_test.go`): the `sdk/errs` package
  contents re-homed as root `package sdk`; `sdk/errs/` DELETED in the
  same commit. Doc header: the kernel contract (stdlib-only,
  compiler-enforced leaf, promotion-is-deliberate).
- Repo-wide rename: every `sdk/errs` import → the root sdk import;
  `errs.X` → `sdk.X` (features, stores, integrations, examples,
  workshop CLI source AND its `.tmpl` templates, storetest suites).
  Watch: files importing stdlib `errors` alongside — no identifier
  conflict (`sdk.ErrNotFound` vs `errors.Is`), but any local variable
  named `sdk` shadows the import (grep first, rename locals if found).
- Verify: full `make check` (the scaffold-compile legs prove the
  emitted templates still build) + `make guard`.

### P2 — evictions + de-imports (web becomes truly agnostic)

- **cache middleware → `cacher`** (new file in cacher, e.g.
  `middleware.go`; exported name preserved or renamed to read as
  `cacher.PagesMiddleware`-style — executor picks what reads best at
  call sites and logs it). web/cache.go deleted.
- **tracing middleware → `tracing`** (same pattern). web's tracing
  import gone.
- **request-log middleware**: verify what web actually consumes from
  `sdk/logging` — if it is only `*slog.Logger` (stdlib types), the
  import may already be droppable in place; else the middleware moves
  to `logging` (same eviction pattern). Executor verifies, does the
  minimal correct thing, logs it.
- **workers**: local three-line structural tracer interface replaces
  the `sdk/tracing` import (`tracing.Tracer` satisfies it implicitly;
  zero API change for hosts).
- After P2: `web` imports the root only; `workers` imports the root
  only (or nothing). All call sites across the repo updated (features'
  inbound packages, examples' mains wiring the middlewares).
- Verify: full make check + guard; boot `examples/cms` (its host wires
  cache/tracing middleware if any host does — executor checks which
  hosts consume the moved symbols and drives one of them live).

### P3 — the physical split

- `git mv` every foundation package → `sdk/foundation/<pkg>`, every
  capability → `sdk/capabilities/<pkg>`; `sdk/feature` stays. Package
  NAMES unchanged (`package web` etc.) — call sites read identically;
  only import paths churn.
- Repo-wide import-path sweep: all 35 modules + the workshop `.tmpl`
  templates + goimports everywhere.
- Doc-adjacent strings that carry sdk paths (G1's regexes are
  prefix-based on `github.com/gopernicus/gopernicus/sdk` — unaffected;
  verify G5/FS1 wording "requires exactly sdk" — module path unchanged,
  unaffected; grep the Makefile guard comments for stale subpath
  references anyway).
- Verify: full make check (scaffold legs = the template canary) +
  guard; `GOWORK=off` builds for sdk + two spot modules; standing
  check (a).

### P4 — `integrations/notify/mailer` (module 36)

- The MailerBridge moves out of `sdk/notify` into a new integration
  module (constructor + tests as shipped at identity-resolution P2,
  import paths updated). `sdk/notify` keeps port + Console only.
- Registration: go.work, MODULES, header 35→36. Zero third-party deps —
  the taxonomy amendment (P5) legitimizes it: an integration isolates a
  third-party dependency OR composes sdk capabilities.
- Any consumer updates (none exist in-repo today — auth takes
  `[]notify.Notifier`, hosts construct the bridge; grep to confirm,
  update if the close drive wired one).
- Verify: module standalone + make check (36) + guard.

### P5 — guard G12 + docs + close

- **G12 `guard-sdk-layering`**: (a) root package: no sdk subpackage
  imports (belt over the compiler's suspenders — a one-line grep);
  (b) `sdk/foundation/*`: any import of `sdk/foundation/`,
  `sdk/capabilities/`, or `sdk/feature` → FAIL (flat + root-only);
  (c) `sdk/capabilities/*`: any import of `sdk/capabilities/` (non-self)
  or `sdk/feature` → FAIL; (d) `sdk/feature` unconstrained (tier 3).
  Prove-can-fail in all three directions. `make guard` → TWELVE.
- ARCHITECTURE: the law verbatim + the tier table; the taxonomy
  integration row amended ("isolates exactly one external dependency —
  a third-party library or a vendor API contract — **or composes sdk
  capabilities into a reusable adapter** (`notify/mailer`, 2026-07-10)");
  the sdk-facility row's examples re-pathed.
- sdk/README restructured around the three tiers; features/README §5 +
  auth README import-path touch-ups; RELEASING enumeration (36 modules,
  the layering as an upgrade note — every import path moved, pre-tag so
  no version obligation); root README counts + tree; NOTES entry;
  archive; memory.

## Acceptance

```sh
make check    # 36 modules, TWELVE guards, scaffold legs green
make guard
```

Plus: zero intra-sdk edges outside the law (G12 green + a one-off audit
grep recorded in the close log); `web` and `workers` import the root
only; `sdk/errs` gone; the emitted feature/host scaffolds build with the
new paths (the scaffold-compile legs); one host driven live per P2's
moved-middleware check; standing check (a) per phase.

## Real-interaction check

Standing check (a) per phase commit. P2 drives whichever host consumes
the moved middlewares. Close: boot `examples/auth-cms` and re-run one
email + one phone invitation leg (the identity-resolution close drive's
short form) — proving the whole re-pathed stack live.

## Execution log

(append dated entries here)
