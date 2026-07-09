# Phase 04 — the cryptids facility: sdk/cryptids ratified; sdk/id retired; ID strategy decided-once

Status: **EXECUTED 2026-07-09 — ratified AS AMENDED in-session (owner): D9
threading replaces D9a package vars for entity keys; D10 pulled forward;
D12 google-uuid demanded; serial parked as future explicit-int-per-resource**
Milestone: `segovia-lessons` (see `00-overview.md`)
Executor model: **opus** (code tasks), **fable** (docs)
Depends on: phase 03 (CLOSED — this phase SUPERSEDES its D5 API one day
later; the intake exists for exactly this, and the D5 work was the
evidence that produced this design)
Size: M

## Provenance (owner-designed, 2026-07-09, in-conversation)

Owner direction across the session, each ruling recorded as it landed:
full separation — `sdk/id` moves into `sdk/cryptids` (the original
gopernicus "crypto tidbits" home, whose integrations category
`integrations/cryptids/{bcrypt, golang-jwt}` already exists); ports as
func types with stdlib defaults; **the ID strategy is decided ONCE at
wiring** (owner: "most applications will just want to define a generator
once and not ever think about it again"). Owner scaffolded the package;
the session completed it. It sits in the working tree now, built and
tested (10 tests green).

## What sdk/cryptids IS (already built — D8 ratifies it as-is)

- `GenerateFunc func() (string, error)` — THE id port, zero-arg: uuid vs
  nanoid vs custom is decided where the func is constructed, once.
- `IDGenerator{Func}` — what consumers hold; zero value = default nanoid
  (~119.8 bits, 21 chars over the 52-byte confusion-free Alphabet — the
  original's alphabet with its double-Z/missing-z bug fixed).
  `Generate() (string, error)` + `MustGenerate() string` (for entity
  constructors; panics only on real runtime failure — config was
  validated at construction).
- `NanoID(alphabet, size) (GenerateFunc, error)` — validates ONCE at
  wiring; attribution comment credits github.com/ai/nanoid and
  github.com/matoous/go-nanoid (stdlib reimplementation only because the
  sdk carries no third-party deps; owner-directed credit).
- `Database` — the explicit delegate-to-the-database strategy: yields ""
  so the store sees an empty ID. **The store-boundary convention (owner,
  2026-07-09): empty ID at create → the store omits the id column and the
  database generates the key (serial int, DEFAULT gen_random_uuid(), a
  stored nanoid function), read back with RETURNING.** Documented now;
  store implementation is D10-gated.
- **No uuid generator** (owner dropped it: DB generation covers postgres;
  app-side/typed uuids are the google-uuid integration's job — D12) and
  **no int generator** (owner: "postgres or turso can handle that").
- Owner-scaffolded siblings in the same package: `Encrypter` port +
  `AESGCM` stdlib default, `SHA256Hasher` (explicitly not-for-passwords),
  `JWTSigner` port-only (golang-jwt integration satisfies it). One merged
  package doc (was two — fixed).

## Decisions

### D8 — FOR RATIFICATION: sdk/cryptids as built (the API above)

### D9 — AMENDED BY OWNER (2026-07-09, in-session): threading, not package vars

**Owner ruling, superseding D9a below for entity keys:** package-private
`var ids` hard-codes the default and forecloses the wiring-time decision the
port exists for. The 21 sites split into two kinds, treated differently:

- **Entity keys (11 sites — take the generator):** user, serviceaccount,
  apikey, securityevent, invitation; cms entry, asset, menu, menuitem,
  inquiry, term. Each feature `Config` gains `IDs cryptids.IDGenerator`
  (zero value → default nanoid); it threads Config → service deps → domain
  constructor, which takes the generator as its first parameter and mints
  via `MustGenerate()`. `cryptids.Database` (empty ID) must be HONORED by
  the bundled stores (see amended D10).
- **Security material (10 sites — never take the generator):**
  session.Token, verification.Code/Token, oauthstate.Token, the API-key
  prefix+secret mint, PKCE/nonce, the invitation secret, and sdk/events
  EventID/Correlation. These keep a package-private unconditional random
  generator: an app's entity-ID strategy must never weaken secret entropy
  (a Database-generated session token would be an empty-string credential).

**Type ruling:** IDs stay `string`; NO generics. Serial/int keys are parked
entirely — when a resource genuinely needs one it will be modeled as an
explicit int field on that resource, with no generator func and no text
casting; the store returns the int. Bundled stores stay text-keyed end to
end; a host that wants native uuid/serial columns owns both the migrations
and the adapted store (param casts in both directions are theirs).

### D9a — ORIGINAL (superseded for entity keys; still accurate for secrets)

21 production `id.New()` sites across **18 files in 16 packages** —
domain constructors, two auth logic services minting secret material
(`authsvc` ×3, `invitationsvc` ×1), **and `sdk/events` (events.go:98,
record.go:53 — the sdk module itself; consult must-fix: the cut-time
inventory said "12 feature files" and omitted these, which would have
broken the sdk build on deletion)**. **Recommended (a):** each consuming
PACKAGE holds a private `var ids = cryptids.IDGenerator{}` and calls
`ids.MustGenerate()` — one line per package, zero threading, preserves D7
as the demand-gated seam. Collision check verified clean: none of the 16
target packages binds an `ids` identifier today. **The swap is
byte-for-byte behavior-identical** — phase 03 already gave `sdk/id` the
fixed 52-byte alphabet, same length, same algorithm — so no stored-ID or
data concern exists. Declined-for-now (b): full D7 threading — no
demanding host; the D4 lens applies.

### D10 — AMENDED BY OWNER (2026-07-09): PULLED FORWARD — implemented now for string keys

**Owner ruling:** "if someone sets the user id to be database generated we
need to honor that." The empty-ID convention ships now for the 11
entity-keyed tables: `Create` with an empty ID omits the id column and
reads the database-generated key back with `RETURNING id` (text columns —
no cast needed); a non-empty ID binds as today. Reference/mem stores
generate a default nanoid when handed an empty ID. The storetest
conformance suites gain the empty-ID Create case, which retires the
Database-var footgun warning below. Secret-keyed tables (sessions, codes,
tokens, oauth states) get NO empty-ID branch — an empty secret is a bug,
never a strategy.

### D10 — ORIGINAL (superseded)

Implementing omit-id+RETURNING across every Create in stores/pgx,
stores/turso, the memstores, and storetest is real churn with zero
current consumer (every feature generates app-side; no host wires
`Database`). Trigger: the first DB-keyed domain — most plausibly
segovia's v1-data import (int serial keys). Until then the convention
lives in the cryptids package doc + features/README, so a store author
building new surface honors it from day one.

**Latent footgun, named (consult fold):** `Database` is exported and
wireable TODAY, but every current store binds the id unconditionally
(e.g. `stores/pgx/users.go:56`) — wiring it now inserts an empty-string
PK: first row takes `''`, second collides. Guard chosen: a **loud
warning on the `Database` var doc** ("inert until your store implements
the omit-id convention — today's bundled stores do NOT") landed with
task-1, and the gated D10 implementation's storetest family becomes the
mechanical proof when it ships. Deferring the export was considered and
declined — it would hide the decided-once vocabulary the strategy
exists to provide.

### D11 — RESOLVED AT REVIEW (consult verified): the reconciliation already happened

The cut framed this as survey-then-rule; the review proved it moot —
authentication ALREADY consumes the sdk ports: `authentication.go:259`
types `TokenEncrypter cryptids.Encrypter`, `:282` types
`TokenSigner cryptids.JWTSigner`, `authsvc/machine.go:265` uses
`cryptids.SHA256Hasher`, and `integrations/cryptids/golang-jwt` declares
`var _ cryptids.JWTSigner`. There is no duplicate vocabulary. The sole
feature-owned port is `PasswordHasher`, staying by its recorded
rationale. Task-3 shrinks to recording this confirmation in NOTES.

### D12 — RECORDED: the integration modules

- `integrations/cryptids/google-uuid` — DEMANDED (owner, 2026-07-09): ships
  now; wraps github.com/google/uuid behind a `cryptids.GenerateFunc`.
- `go-nanoid` — DECLINED: it would isolate a dependency that duplicates
  ~40 stdlib lines already in-kernel; fails "every integration earns its
  existence". The attribution comment is the honest relationship.

## Tasks

### task-1: commit sdk/cryptids; delete sdk/id; migrate the 21 call sites (D9a)

- **model:** opus — **files:** [sdk/cryptids/* (commit as-is + the
  Database-var doc warning), sdk/id/ (delete), the 18 files in 16
  packages holding the 21 `id.New()` sites: 8 auth `domain/*`, `authsvc`
  (2 files), `invitationsvc`, 5 cms `domain/*`, and **sdk/events
  (events.go, record.go)** + import lines]
- **verify:** per-module build/test/vet (sdk INCLUDING ./events, + 3
  features), `make check` + `make guard`; **repo-wide** grep proves zero
  `sdk/id` imports remain (the sweep includes the sdk module itself);
  run-and-look:
  auth-cms register→verify→login→logout 201/200/200/200 with a 21-char
  new-alphabet ID observed
- Per D9a: one private `var ids = cryptids.IDGenerator{}` per consuming
  domain package (goimports handles the swap `id.New()` →
  `ids.MustGenerate()`).

### task-2: segovia heads-up (record only — other repo)

- Deleting `sdk/id` breaks segovia v2's `id.New()` on its next build
  (live replace directive). Record the one-liner migration in NOTES for
  the owner to carry over: per-domain `var ids = cryptids.IDGenerator{}`.

### task-3: record the D11 confirmation (folded into task-4's NOTES entry)

- No code moves — the review verified the adoption already exists (D11).
  The NOTES entry states it: cryptids `Encrypter`/`JWTSigner`/
  `SHA256Hasher` consumed by authentication; `PasswordHasher` the sole
  feature-owned port, by rationale; no duplicate vocabulary anywhere.

### task-4: docs + records

- **model:** fable — **files:** [sdk/README.md (id row → cryptids row:
  ids + encrypter + hasher + signer), ARCHITECTURE.md (sdk services list:
  `id` → `cryptids`; the facility-kind row already fits), NOTES.md,
  00-overview.md ledger, 03-id-kinds.md (supersession marker on D5)]
- **verify:** `make guard`; final `make check`
- NOTES entry: D8–D12, the supersession story (D5 → cryptids one day
  later — build-to-evaluate produced the better design), the empty-ID
  convention, the attribution ruling.

## Out of scope

- Store-layer RETURNING implementation (D10-gated).
- The google-uuid module (D12-gated); go-nanoid (declined).
- D7 threading (unchanged, demand-gated).
- Segovia code changes (owner's repo).

## Module / API impact

`sdk`: package `id` DELETED, package `cryptids` added (net -1 concept at
the module level: ids join the tidbits home). Feature modules: `Config.IDs`
added to authentication and cms (additive); 11 entity-constructor
signatures changed (pre-release, zero tags). `integrations/cryptids/
google-uuid` added → 31 modules. Zero tags exist.

## Acceptance

```sh
make check && make guard
git grep -n "gopernicus/sdk/id" -- ':!.claude' ':!NOTES.md'   # zero hits
```

Run-and-look per task-1. Green tests alone close nothing.

## Consultation notes

`lead-backend-engineer` reviewed cut + CODE (2026-07-09):
**ship-with-edits**, all folded — (1) the task-1 inventory corrected (21
sites / 18 files / 16 packages incl. `sdk/events`, whose omission would
have broken the sdk build and the acceptance grep); (2) D11 rewritten:
the reconciliation already exists in the tree, task-3 shrank to a NOTES
record; (3) the `Database` empty-PK footgun named in D10 with the
doc-warning guard chosen; (4) byte-identical-migration fact added to D9.
Verified by the consult: NanoID closure is concurrency-safe (fresh
buffers per call, read-only captures); AES-256-GCM correct (nonce
handling, tamper detection, key validation); the `defaultNanoID`
discarded-error pattern is test-guarded; no `ids` identifier collisions
in any target package. Its open note, recorded: `Encrypter.Encrypt`
rejects empty plaintext — deliberate; a future conformance suite must
codify it.

## Open questions

1. D8 (the API as built), D9 (migration mode incl. sdk/events), D10
   (convention documented / implementation gated + the doc-warning
   guard) — the ratification gate for the tasks. D11/D12 carry no gate.

## Execution log

- 2026-07-09: task-1 first pass executed as D9a (package vars), sdk/id
  deleted, 18 files migrated, sdk+auth+cms green. Owner REJECTED D9a for
  entity keys mid-review ("hard coding the generator into the feature"),
  ruled the D9/D10 amendments above (threading + stores honor Database;
  string IDs, no generics; serial parked as future explicit-int-per-
  resource). Secrets-site migration stands as the end state. Re-execution
  proceeding: entity-constructor threading, Config.IDs, store empty-ID
  branches, google-uuid integration.
- 2026-07-09 (same session): amended execution COMPLETE. Threading: 11
  entity constructors take `ids cryptids.IDGenerator` first; `Config.IDs`
  on authentication (→ authsvc + invitationsvc Deps) and cms (→ 5 service
  constructors); secrets renamed to package-private `secrets` generators
  with why-comments (session/verification/oauthstate/authsvc/
  invitationsvc); media StorageKey decoupled from entity ID (own random
  component). Stores: empty-ID → omit id + RETURNING in 11 entity Creates
  × pgx+turso; migrations pgx `0012`/cms `0022` (ALTER SET DEFAULT
  gen_random_uuid()::text) and turso `0012`/`0022` (SQLite rebuild with
  DEFAULT lower(hex(randomblob(16))), indexes recreated, FK-off drop
  verified against real SQLite); reference stores + example memstores +
  authmem assign-at-insert; conformance `DBGeneratedIDOnEmpty` per entity
  family (11 cases) — one case caught the example memstores' AddItem gap
  live, fixed. google-uuid module shipped (V4/V7, tests, README, go.work +
  Makefile). Docs: sdk/README cryptids row, ARCHITECTURE services list +
  module tree, features/README checklist item 14, cryptids Database doc
  (footgun warning → implemented statement), NOTES entry, ledger row #2
  supersession, 03 supersession marker. Verified: `make check` green (31
  modules + guards); run-and-look auth-cms register 201 (21-char
  new-alphabet ID observed) → gate 403 → verify 200 → login 200 → gated
  GET 200 → logout 200 → post-logout 401. Acceptance grep: zero `sdk/id`
  imports. Live-DB conformance (-tags=integration) not run — no creds in
  this environment; turso 0022 rebuild empirically verified against
  SQLite 3.43 instead.
