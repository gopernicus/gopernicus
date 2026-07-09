# Phase 03 — sdk/id kinds (flag #2): nanoid-shape generation + stdlib UUID; port typing stays string (wait-for-demand)

Status: **RATIFIED 2026-07-09 (jrazmi, in-conversation: D5 + D6 "that works"; D7 recorded from the same exchange) — EXECUTING**
Milestone: `segovia-lessons` (see `00-overview.md`)
Executor model: **opus** (task 1), **fable** (task 2)
Depends on: — (phases 01/02 closed)
Size: S

## The flag (input of record — Segovia's flags doc #2, verbatim)

> **`sdk/id` is string-only** (26-char lowercase base32). Segovia needs to
> support **int, string, and uuid** identifiers (v1 data ports string/uuid
> IDs). Owner reviewing ID strategy upstream. Interim: String IDs
> (`id.New()`) + TEXT columns everywhere.

**Owner direction (2026-07-09, in-conversation — the ID-strategy review
landing):** no third-party libs in sdk (standing law — the empty go.mod);
`sdk/id` should return to the original gopernicus shape — nanoid-like,
able to take a **custom length and alphabet**; UUID wanted but unsure how
without a dependency (answer: UUIDv4 is ~15 lines of stdlib —
`crypto/rand` + version/variant bits + hex formatting; the original's
`cryptids.GenerateUUID` already proved it).

## Facts (surveyed 2026-07-09)

- `sdk/id` today: 33 lines — one func, `New() string`, 128-bit random,
  26-char lowercase base32, panics on `crypto/rand` failure.
- The original monolith's `infrastructure/cryptids/id.go` (local:
  `~/code/gopernicus-ecosystem/gopernicus-original/`, verified identical
  to the GitHub HEAD): `GenerateID()` — 21 chars over a
  confusion-free alphabet (no vowels, no `O/I/o/i`), mask-based rejection
  sampling for uniform distribution, 1.6× read buffer;
  `GenerateCustomID(alphabet, size)` with validation errors;
  `GenerateUUID()` — stdlib v4. Context worth keeping: the original's
  **workshop codegen consumed `GenerateCustomID` in its repository/fixture
  templates** — per-aggregate custom ID shapes were a codegen feature, so
  `NewCustom` is the seam the future workshop-v2 scaffold will want.
  **Bug found in the original's default
  alphabet: uppercase `Z` appears twice and lowercase `z` is missing** —
  duplicate biases the distribution toward `Z`; the port fixes it.
- Consumers: 21 production `id.New()` call sites (feature domain
  constructors) + segovia v2 (`replace`-directive sdk; `id.New()` in every
  domain). All IDs land in TEXT columns; **grep confirms nothing asserts
  the 26-char/base32 shape** anywhere (code or SQL).
- `sdk/crud` `Reader`/`Writer` hardcode `id string` in Get/Update/Delete.
  **Segovia v2 uses zero `crud.` shapes** (verified) — its ports are its
  own; the connectors' `List[T]` helpers are ID-agnostic already.

## Decisions

### D5 — FOR RATIFICATION: the sdk/id API (generation)

```go
// New returns a 21-character ID over Alphabet — nanoid-shaped, ~120 bits.
func New() string
// NewCustom generates over a caller alphabet/length (validation errors).
func NewCustom(alphabet string, size int) (string, error)
// UUID returns a canonical lowercase UUIDv4 string (stdlib only).
func UUID() string
// Alphabet and DefaultLength are exported so hosts can derive variants.
const DefaultLength = 21
const Alphabet = "bcdfghjklmnpqrstvwxyzBCDFGHJKLMNPQRSTVWXYZ0123456789"
```

- Error posture: `New()`/`UUID()` keep today's panic-on-rand-failure (the
  only failure source is `crypto/rand`; 21 call sites stay clean).
  `NewCustom` returns errors — it has real validation (alphabet ≥ 2 chars,
  each byte unique, size ≥ 1).
- The default alphabet is the original's INTENT with the bug fixed:
  lowercase `z` in, duplicate `Z` out — 52 unique chars, no vowels, no
  `O/I/o/i`; 21 chars ≈ 119.8 bits ≈ today's 128-bit strength class.
- Mask-based rejection sampling ported as-is (uniformity), buffer trick
  kept.
- **Behavior change, ruled acceptable:** `New()` output changes shape
  (26-char base32 → 21-char nanoid). TEXT columns, zero shape assertions,
  dev-only data; old and new IDs coexist. Segovia inherits on its next
  build (live `replace` directive).
- UUID**v7** (time-ordered, index-friendly) deliberately NOT shipped:
  pagination keys on `(created_at, id)` so sortable IDs have no consumer.
  Trigger: the first consumer that keys on ID ordering.

### D6 — FOR RATIFICATION: the int/uuid PORT-typing half of the flag is wait-for-demand

The flag's other half — ports/`crud` typed for int/uuid keys — has **zero
consumers**: all in-repo features are string-keyed, and segovia v2 went
all-string via `id.New()`. Per the D4 lesson (same milestone, one day
old): no code ships for anticipation. Recorded dispositions:

- `crud.Reader/Writer` stay string-keyed. `uuid` keys already work
  through them (canonical string form; pgx/libsql accept it for uuid
  columns). Trigger to revisit: a real host embedding crud shapes over a
  non-string key.
- A `Getter`/`Lister` recomposition of `Reader` (so int-keyed hosts could
  reuse the List side) was considered and NOT shipped — same trigger.
- int keys are DB-assigned (serial/autoincrement); sdk/id generates
  nothing for them, correctly.

### D7 — RECORDED (owner-raised 2026-07-09): pluggable ID generation is wait-for-demand, design pre-agreed

The owner's question: a host wanting its own generator package (or
google/uuid) should have "a general id func." Ruling: for **app-local
code** this needs zero framework support — the host owns those call sites
and calls whatever it likes. It only has teeth **inside features**, whose
21 domain-constructor call sites hard-call `id.New()` and cannot be edited
by a host. The pre-agreed design, to be shipped ONLY on demand (extension
tier 2, "Replace a component" — the Views/RateLimiter pattern):

- `sdk/id` gains `type Generator func() string` — the one-line shared
  vocabulary so features don't each invent a port.
- Each ID-generating feature gains a nil-safe `Config.IDGenerator
  id.Generator` (nil → `id.New`, today's behavior), threaded from Config
  through its services to its domain constructors.
- Costs acknowledged: constructor/service threading at every `id.New()`
  site in that feature; per-feature host wiring; no cross-feature
  uniformity guarantee beyond what the host wires.
- **Trigger:** the first real host that needs feature-generated entities
  keyed by its own generator. Nothing ships in this phase — the same
  ruling FS7 (zero-consumer data form) and D4 (cosmetic sugar) got this
  week.

## Out of scope

- Any `crud`, storetest, store-adapter, or feature-port change (D6).
- UUID parsing/validation helpers (no consumer; generation only).
- Editing Segovia's flags doc (owner flips #2 after this lands).

## Module / API impact

`sdk/id` public API: `New()` keeps its signature (output shape changes);
`NewCustom`, `UUID`, `Alphabet`, `DefaultLength` added. No other module's
API moves. Zero tags exist; no release implication.

## Tasks

### task-1: rework sdk/id + tests

- **model:** opus — **files:** [sdk/id/id.go, sdk/id/id_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`;
  `make check` + `make guard`; run-and-look: boot `examples/auth-cms`,
  register a user, confirm the logged/returned IDs are 21-char
  nanoid-shape and the flow's codes are unchanged (201/200/200/200)
- Port `generateID` (mask + buffer) from the original with the fixed
  alphabet; implement `New`/`NewCustom`/`UUID` + consts per D5. Tests:
  length/alphabet membership, custom alphabet honored, validation errors
  (short alphabet, dup bytes, size<1), UUID regex + version/variant bits,
  a chi-squared-free sanity check on distribution (e.g. every alphabet
  byte appears across 10k IDs), and the doc'd entropy claim in a comment.
  Package doc rewritten (drop the base32 story; state nanoid shape,
  entropy, panic posture, D5/D6 provenance).

### task-2: docs + NOTES + ledger

- **model:** fable — **files:** [NOTES.md,
  .claude/plans/segovia-lessons/00-overview.md, ARCHITECTURE.md (only the
  one `id` mention in the sdk services list — check whether it needs a
  word), sdk/README.md if it names id's shape]
- **verify:** `make guard`; final `make check`
- NOTES.md dated entry: flag #2 closed — D5 shipped, D6's two
  wait-for-demand dispositions with triggers, the original-alphabet bug
  fix, the behavior-change ruling. Ledger row #2 → CLOSED. Segovia
  carry-back note for the owner (drop the interim-workaround wording when
  flipping the flag).

## Acceptance

```sh
make check && make guard
cd sdk && go test ./id/ -v
```

Run-and-look: the auth-cms register drive per task-1. Green tests alone
close nothing.

## Open questions

1. **D5** — the API surface (names, panic posture, fixed default
   alphabet, 21 default length).
2. **D6** — the wait-for-demand ruling on port typing.

## Execution log

(append dated entries here)
