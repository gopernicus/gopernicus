# Phase 3 — `integrations/scheduling/robfig-cron`

Status: RATIFIED (cut from design §4; ratified YOUR CALL #6)
Executor model: opus
Depends on: phase 2 (the `jobs.CronParser`/`CronSchedule` port shapes).
Design doc: `.claude/plans/roadmap/jobs-feature-design.md` §4. Precedent
for module conventions + structural satisfaction + the no-feature-import
rule: `integrations/cryptids/bcrypt` (read its phase file's pattern:
locally-mirrored interface literal in tests, comments phrased without the
literal "features/" string).

## Work items

1. Module `gopernicus/integrations/scheduling/robfig-cron` wrapping
   exactly `github.com/robfig/cron/v3`; go.work + Makefile MODULES.
2. `Parser` satisfying `jobs.CronParser` structurally — configured with
   the original's exact flags (`Minute|Hour|Dom|Month|Dow|Descriptor`,
   UTC), so whatever `EnsureSchedule` accepts the fire engine can
   evaluate. NO import of any features/* module.
3. Tests: 5-field expressions + `@hourly`-style descriptors round-trip
   through Next(); invalid expressions error; DOM/DOW semantics match
   robfig (spot checks); UTC evaluation; compile-time structural assertion
   against a locally-mirrored interface literal.
4. README (bcrypt-README style).

## Acceptance

```sh
cd integrations/scheduling/robfig-cron && go build ./... && go vet ./... && go test ./...
grep -rn "features/" integrations/scheduling/robfig-cron/   # empty
make check
```

## Real-interaction check

Standing check (a).

## Execution log

### 2026-07-02 — phase 3 executed (loop leg 17; implementer on opus)

Shipped `integrations/scheduling/robfig-cron` (15th module): `Parser` over
robfig/cron v3 with the §4 flags, UTC-pinned Next (normalizes input to UTC
before delegating), README with a Wiring section. Tests: 5-field +
descriptors, invalid-expression errors, DOM/DOW OR spot check
(cross-checked against robfig before pinning expectations), UTC pinning,
never-fires zero time, compile-time structural assertion.

**Load-bearing Go finding, flagged for phase 8 (not silently absorbed):**
true cross-module structural satisfaction of `CronParser` is impossible as
the port is written — `Parse` returns the DEFINED interface type
`jobs.CronSchedule`, and Go interface satisfaction requires identical
signatures, so a foreign method returning its own schedule type doesn't
match. The integration returns a type-ALIAS interface (genuinely
assertable in its own tests), and the phase-8 host needs a 3-line
composition-root adapter (documented in the README) — OR `jobs.
CronSchedule` becomes a type alias (one-line change in the feature) for
zero-adapter wiring. Decision deferred to phase 8's executor/jrazmi;
phase 3 correctly did not touch features/jobs.

Acceptance (firsthand): build/vet/test PASS; `grep "features/"` → 0 hits;
`make check` → "all checks passed" (15 modules, 4 guards). Standing boot
check deferred to the leg report (no runtime surface here; covered by the
adjacent legs' checks).

Unverified: host wiring ergonomics — phase 8 resolves the adapter-vs-alias
call live.
