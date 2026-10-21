# Pluggable authorization — `sdk/capabilities/authorization` + `integrations/authorization/*`

**Status:** REV 4.2, 2026-10-21.
- Rev 4.1 was ratified by the owner.
- Slice 0 (the spike) is DONE.
- The owner approved folding the spike into this plan, with rulings S1–S3 (see
  "Spike record").
- Slice A is next and proceeds per its gates.
Rev 4 was reviewed the same day by the architecture steward (aligned-with-edits),
the lead backend engineer (ship-with-edits) and the data-integration reviewer
(ship-with-edits). Every finding is folded in below; the "Review record" at the end
maps each one to where it landed. Rev 3 (the `providers/<pkg>` adapter-kind design)
is archived beside this file as `authorization-provider-port.rev3.md`. Its research
still holds and is cited as `rev3 §N`:
- §1 consumption survey
- §2.2 vendor mapping
- §3 hard decisions
- §4 authentication coupling
- sources

## Owner rulings

| id | ruling | date |
|---|---|---|
| P1 | **The contract is an sdk capability**: `sdk/capabilities/authorization`. Reverses rev 3's D1. The graduation gate (`pockets/README.md` §5 five criteria + `sdk/README.md` admission + the ARCHITECTURE five-point test) applies. The steward found it passable with the edits folded here. | 2026-10-21 |
| P2 | **Vendor adapters are ordinary root integrations**, `integrations/authorization/{authzed,openfga}`, named for the client package. They import sdk only. No new adapter kind and no `providers/` word. | 2026-10-21 |
| P3 | **Iterative slices** 0 → A → B → C → D (→ E on demand). Each ships alone and leaves both hosts with no required change. | 2026-10-21 |
| R13 | The spike (slice 0) runs **before** slice A. | 2026-10-21 |
| ~~D6~~ | ~~One identifier grammar at the port boundary, enforced by every implementation including the engine (rev3 §3e).~~ Superseded by D6′. | 2026-10-21 |
| D6′ | **Grammar belongs to the implementation.** The sdk port checks structure only (non-empty type, ID, relation). Each implementation rejects what it cannot represent as `sdk.ErrInvalidInput`: the native engine adapter stays as loose as the pocket; `authzed` enforces SpiceDB's grammar; `openfga` enforces OpenFGA's, including the combined `type:id` ≤ 256 cap (S3 moves there). Portability is opt-in through a strict sdk helper (`ValidatePortable`, the old D6 grammar plus the combined cap) that hosts can run in tests or before moving to a vendor. The conformance suite asserts only that out-of-grammar input is `ErrInvalidInput` per the adapter's declared grammar. The lowered evaluator passes refs through and lets the backing port decide. | 2026-10-21 |
| D8 | No global scope on the port. `Role(label)` / `HasRole(…, Global())` lower to a relation on a configured global resource; a declared permission is never rewritten. | 2026-10-21 |
| D10 | Through the port, an undeclared permission or relation is `sdk.ErrInvalidInput`. `Components.Decisions` keeps today's deny-with-nil. | 2026-10-21 |
| D12′ | The engine's port implementation is an in-core adapter in the pocket. Its home is settled by steward finding 6: its own public `logic/<concern>` package. | 2026-10-21 |
| Q1 | `Require` refuses to mount over a lowered evaluator unless the host opts in with `WithLoweredPolicy()`. | 2026-10-21 |
| R11 | Moot under P1. `authorizationhttp.DecisionService` and `decisions.Expression` stay pocket-owned. | 2026-10-21 |

Superseded from rev 3: D0, D1, D2, D12 (as written), R15, G31, the `providers/`
guard amendments (rev3 §2.4–§2.5), `NewWithProvider`, `providertest`, and the sdk-side
`ErrUnrestricted`.

## Goal: three levels of buy-in, the authentication shape

| level | host wires | gets | needs |
|---|---|---|---|
| 1 | `sdk/capabilities/authorization` + `integrations/authorization/authzed` | `Check`, `CheckBatch`, paged lookup (writes in slice C) against SpiceDB | no pocket, no tables |
| 2 | level 1 + the pocket's guard over the bare port | `Require(Can(…), HasRole(…))`, `FilterAuthorized`, lowered policies | a pocket module import; no stores, no tables |
| 3 | the pocket on its own engine (today) | everything: exact roles, `Unrestricted`, role routes, ledger, audit, TupleCache, integrity | stores + migrations |

Level 3 is today's behaviour and stays the default. The pocket **implements** the
port as its implementation of record, through an in-core engine adapter. It also
**consumes** the port, through `decisions.NewLoweredEvaluator`. Adding a vendor
means one integration module that passes `authorizationtest`.

**Hard requirement (level 2):** the guard and its policy lowering work over the bare
sdk port, with no engine, no stores and no `Components`. Slice 0's F1 tests this.

## The port (slice A ships the read half)

```go
// sdk/capabilities/authorization — imports the sdk root (+ pkg) only.
package authorization

var ErrInvalidCursor = fmt.Errorf("authorization: invalid lookup cursor: %w", sdk.ErrInvalidInput)

type Principal = sdk.Principal // concrete (Type, ID); "never a userset" is enforced by ValidateRef in each implementation
type Resource struct{ Type, ID string }

// Permission is a permission OR a relation (the Zanzibar meaning): the lowered
// guard asks relation questions (HasRole, HasRelationship) through the same call.
type CheckRequest struct {
	Principal  Principal
	Permission string
	Resource   Resource
}
type Decision struct{ Allowed bool } // a struct so a later freshness token is additive

type Checker interface {
	Check(ctx context.Context, req CheckRequest) (Decision, error)
	// CheckBatch is all-or-nothing: any failed item fails the call. Results come
	// back in request order with the same length; duplicate requests are allowed.
	CheckBatch(ctx context.Context, reqs []CheckRequest) ([]Decision, error)
}

type LookupRequest struct {
	Principal    Principal
	Permission   string
	ResourceType string
	Limit        int    // 0 = implementation default; < 0 or > Limits().MaxLookupPage → ErrInvalidInput
	After        string // opaque; foreign, stale or malformed → ErrInvalidCursor
}
type LookupPage struct {
	IDs        []string // len ≤ Limit; unique within a page, MAY repeat across pages; unordered; an empty last page is legal
	NextCursor string   // "" = done
}
type Lookup interface {
	// LookupResources never presents a truncated result as complete: an
	// implementation that cannot enumerate the full set returns sdk.ErrUnavailable.
	LookupResources(ctx context.Context, req LookupRequest) (LookupPage, error)
}

type Authorizer interface {
	Checker
	Lookup
}

// Helpers every implementation gets for free.
func FilterAuthorized(ctx context.Context, c Checker, p Principal, permission, resourceType string, ids []string) ([]string, error) // chunks by Limits().MaxCheckBatch
func LookupAll(ctx context.Context, l Lookup, req LookupRequest, max int) ([]string, error) // dedupes; counts unique IDs; ErrUnavailable on overflow or a repeated cursor, never a short list
func ValidateRef(kind, value string) error // D6 grammar; the pocket's tuples.ValidateRefField delegates to it

// Optional, discovered by type assertion.
type SchemaReader interface {
	DeclaresResource(resourceType string) bool
	DeclaresPermission(resourceType, permissionOrRelation string) bool
	DeclaresSubject(resourceType, relation, subjectType string) bool
}
type Limits struct{ MaxCheckBatch, MaxLookupPage int }
type Limiter interface{ Limits() Limits }
```

**Contract rules not visible in the signatures:**
- **Cursors are fingerprinted by every implementation.** Each one wraps its vendor
  cursor (or the engine's `decisioncursor`) in an envelope: version, a fingerprint
  of the principal, permission and resource type, then the inner cursor. Detecting a
  foreign cursor therefore never depends on the vendor (OpenFGA has no cursors at all).
- **A page never exceeds `Limit`.** An implementation whose vendor cannot page
  (OpenFGA `ListObjects`) builds the complete set, or returns `ErrUnavailable`. It
  sorts the set and serves keyset pages after the cursor, re-running the query each
  page. This is coherent under the no-snapshot rule.
- **Error classes:**
  - grammar, an oversize batch, `Limit`, an undeclared name (D10), and
    `ErrInvalidCursor` all wrap `sdk.ErrInvalidInput`;
  - budget, depth, deadline, truncation, and "cannot enumerate" wrap `sdk.ErrUnavailable`;
  - contention wraps `sdk.ErrConflict`.
- **The port never mentions `Unrestricted`.** Lookup on a universal grant is simply
  "cannot enumerate" (`sdk.ErrUnavailable`); a Check on one is `Allowed=true`.

Slice C adds these, all additively:
- `Writer`, a single-resource atomic `Write` with touch/delete semantics;
- `Relationship` / `SubjectRef`, with usersets on subjects only;
- the optional `IdempotentWriter`;
- `WithConsistency(ctx, Consistency{Mode, Token})`, with `Token` on write results.

See rev3 §2.1, §3a and §3b.

**Stays in the pocket:**
- `Unrestricted` (pocket `ErrUnrestricted` wraps `sdk.ErrUnavailable`; the port
  docs and the pocket README say "do not retry")
- reason codes and explain
- `decisions.Expression` and the guard
- roles exactness
- `IntegrityPolicy`
- ledger / `Fence`
- audit
- TupleCache
- `FilterPage`

Hosts that rely on `Unrestricted` (gps-360-go stewards, ×11) keep calling
`Components.Decisions`.

**Types (R16, owner-ruled 2026-10-21: the pocket USES the sdk types; no conversion layer).**

The shared request vocabulary lives in sdk:
- `model.Resource`, `model.CheckRequest` and `model.LookupRequest` become
  `// Deprecated:` aliases (`= authorization.X`) of the sdk types. They are the same
  types, so nothing is converted.
- `model.PrincipalRef = authorization.Principal = sdk.Principal`, and
  `authorization.PrincipalFrom` becomes the identity.
- Hosts (33 files in gps-360-go, 35 in Segovia) keep compiling. The aliases are
  removed in a later minor, after a host handoff renames them.
- The `Validate` methods move to the sdk types and use `ValidateRef`.
- `ErrInvalidCursor` lives in sdk only, and `model.ErrInvalidCursor` becomes an alias
  var. `stores/storetest/keyset.go:348` keeps passing.

`decisions.Service.Check` / `CheckBatch` return `authorization.Decision`:
- **This is the one breaking edge, so the pocket tag is "BREAKING, pre-1.0".**
- `ReasonCode` / `Reason` leave the public check result. No host production or test
  code reads them (grepped 2026-10-21). They stay available through `CheckExplain`,
  and inside the pocket's evaluation path for decision logs and audit.
- Host narrow interfaces typed `(model.CheckResult, error)` retype their result to
  `authorization.Decision`: gps-360-go `ask/allowed.go:18` and `access.go:38`, plus
  Segovia's `CheckBatch` ports. This is mechanical.

`model.CheckResult` (with reasons) survives as a pocket-only type:
- `EvaluateResolved` / the guard and `CheckExplain` still return it.
- Lookup results keep pocket types (`ResourceIDPage`, `ResourceSet`), because they
  carry `Unrestricted`.

What remains of the engine adapter is **behaviour only**:
- D10 error-not-deny;
- relation answers;
- the D8 global → `tuples.Global()` mapping;
- `Unrestricted` → `ErrUnrestricted`;
- `LookupPage` from `ResourceIDPage`.

The engine's existing fingerprinted cursor already meets the envelope rule.

**Conformance (`sdk/capabilities/authorization/authorizationtest`, stdlib only):**

```go
type Fixture struct {
	Name  string
	Facts []Fact // generated where large (case 6: 120 resources), not static files
	Cases []Case // expected answers
}
// The factory builds a FRESH authority for f, installs ITS OWN dialect's schema
// for f.Name, and seeds f.Facts through its native API (the port has no Writer
// until slice C). A fixture without a schema in that dialect FAILS, never skips.
func Run(t *testing.T, factory func(t *testing.T, f Fixture) authorization.Authorizer)
```

- **Fixtures:** `documents`, `gps-global` (using the rev3 §3c renames, so F1 parity
  is on the renamed model, not production's), `segovia-chain`, `usersets`.
- **Schemas** live with each implementation: a `decisions.Model` in the pocket,
  `.zed` in `authzed/testdata`, an OpenFGA model in `openfga/testdata`.
- **Lookup assertions** compare deduped unions as sets and assert `len(page) ≤ Limit`.
- **Optional families** (`SchemaReader`, `Limiter`) skip loudly when the interface is
  absent; a declared one must pass.
- **Policy-lowering parity** (F1) is a pocket test, because `Expression` is pocket-owned.

## Slices

### Slice 0: read-side spike (disposable; ~2 days)

A scratch module in this session's scratchpad, never in the repo tree. No tags.
Only two things survive: the "Spike record" appended here, and the fixture files
(they move into slice A).

**Setup**
- `authzed/spicedb serve-testing`, using one bearer token per test for an isolated
  datastore.
- `openfga/openfga run` (memory).
- A second OpenFGA container with `OPENFGA_LIST_OBJECTS_MAX_RESULTS=50` and a ~1 ms
  `OPENFGA_LIST_OBJECTS_DEADLINE`.
- `lsof` every port first. Never 55432.

**What gets built**
- Throwaway read adapters for both vendors against the draft port above. They
  include the cursor envelope and the D10 status mapping by gRPC/HTTP **code**,
  never by string match.
- The engine adapter draft over `stores/memory`.
- A draft `NewLoweredEvaluator` implementing the lowering algorithm below, run
  against both hosts' real `Require` shapes.

**Lowering algorithm (the thing F1 tests):**
- Walk the `Expression` in declaration order with the native short-circuit.
- Only branches the native walk would reach may run their resolvers or surface
  errors. `ErrAlternativeNotApplicable` maps to false.
- *(Superseded by S1 after the spike. The text was: "Leaves at the same depth are
  batched into one `CheckBatch` only when every one of them would be reached
  natively.")* The rule is now **speculative checks**. Consecutive leaves whose resource
  is known without running a non-pure resolver go out as one `CheckBatch`: `Global`,
  `Fixed`, `Path` (pure) and already-resolved inputs. Results past the deciding leaf
  are ignored. A `Path` read that fails holds its error until its leaf is reached; if
  the leaf is never reached, the error is discarded. On a batch error the run is
  re-walked one leaf at a time, so only reached leaves surface errors. `Resource`
  resolvers stay lazy.
- `HasRole(…, Global())` lowers to the D8 global resource through a label mapping
  (S-amendment 5).
- A resolver-supplied resource that the port cannot express (the combined object cap)
  **denies** and does not error. What the pocket grammar rejects still errors, to keep
  parity.

**Read cases** (rev3 §2.6 numbering):
- 1–6, 9, 9b, 10, 11, 19, 20, 22, 23, 24, 26
- 23b: OpenFGA deadline truncation
- duplicate IDs across SpiceDB pages
- negative `Limit`
- D10 on both vendors (unknown permission → `ErrInvalidInput`)
- the OpenFGA `type:id` > 256-byte check
- read-after-seed freshness on SpiceDB (decides slice B's default consistency)

**Falsifiers.** Any one sends the design back here.
- **F1.** A real `Require` policy from either host, lowered over the bare port, does
  not give answers identical to the engine's native `EvaluateResolved`. This includes
  the short-circuit cases (an empty `Path` param behind a granting role branch, and a
  resolver error on an unreached branch).
- **F2.** Paged lookup cannot be made coherent across the three implementations under
  the contract above.
- **F3.** The common `Any(HasRole global, Can on path)` shape needs more than one
  bulk round trip per **reached level**. Count at the go-sdk layer: it chunks
  BatchCheck at 50.
- **F4.** A fixture fails to compile in `.zed` or OpenFGA after the §3c renames, or a
  production label fails D6.

**OpenFGA drop-outs.** If any one fires, slice D is deferred and SpiceDB ships alone.
- **O1.** Neither the result cap nor a deadline truncation can be detected. The
  adapter's own context deadline must be strictly below the configured server
  deadline, so that a slow query surfaces as `ErrUnavailable` on our side before the
  server truncates. `len >= cap` maps to `ErrUnavailable`. If `StreamedListObjects`
  ends at a deadline without a distinguishable status, O1 fires.
- **O2.** `BatchCheck` per-item errors are indistinguishable from denials.
- **O3.** A fixture cannot be expressed with answers matching the engine.

### Spike record (slice 0, executed 2026-10-21)

**Where things are**
- The full record: `.claude/plans/authorization-provider-port.spike/spike-record.md`.
- The fixtures that survive into slice A: `.claude/plans/authorization-provider-port.spike/fixtures/`
  - case data in Go;
  - `schema/zed`, `schema/fga`;
  - `engine/models.go`.
- The throwaway reference code (`port/`, `engineadapter/`, `authzedadapter/`,
  `openfgaadapter/`, `lowered/`, `parity/`) is kept beside them as reading material for
  slices A, B and D. None of it is copied verbatim.

**The run**
- 512 PASS, 0 FAIL, re-verified by the lead.
- SpiceDB v1.57.0, OpenFGA v1.22.0, authzed-go v1.11.0 (`ReflectSchema` exists),
  go-sdk v0.8.3.

| id | verdict |
|---|---|
| F1 | **CLEARS** with the D6 combined cap: 24 real policies from both hosts, 6,156 lowered evaluations across the three implementations, 0 mismatches, short-circuit cases included |
| F2 | **CLEARS**. SpiceDB repeats IDs across pages (60/60) and its cursors are revision-pinned. |
| F3 | **FIRED as written** (2 round trips for `Any(HasRole global, Can on Path)` when the role is false); resolved by S1 → 1 round trip, 0 mismatches |
| F4 | **CLEARS**. auth-cms also needs a `platform.admin` relation/permission rename in its `.zed`. |
| O1 | **FIRED on its last clause**: OpenFGA returns 0 of 40 objects with HTTP 200 at its deadline. Mitigated by S2. |
| O2, O3 | **CLEAR** |

**Owner rulings (2026-10-21)**
- **S1:** pure `Path` inputs resolve speculatively, with their errors held back; `Resource`
  resolvers stay lazy. F3 now reads "≤ 1 bulk round trip per reached level, for
  policies whose inputs are pure". The lowering algorithm above is amended.
- **S2:** OpenFGA stays in scope. Truncation is detected by timing: the adapter's own
  deadline is 0.8 × the host-declared server deadline, plus `len ≥ cap`. The
  host-declared deadline is a **required** adapter config with no default. D11 is
  still decided at slice D.
- **S3:** D6 adds a combined object cap, `len(type)+1+len(id) ≤ 256`, as the sdk
  helper `ValidateObject(type, id)`. Every implementation checks it.
  *(Superseded by D6′: the cap is OpenFGA's rule; the sdk keeps it only in the opt-in
  `ValidatePortableObject`.)*

**Engineering amendments.** Each lands in the slice named.

1. **Error mapping (all slices).** Map by the gRPC/HTTP code **refined by the vendor's
   structured reason**, never by message text.
   - SpiceDB `ErrorInfo.reason`: `…UNKNOWN_RELATION_OR_PERMISSION` / `…UNKNOWN_DEFINITION`
     map to `ErrInvalidInput`; `…MAXIMUM_DEPTH_EXCEEDED` maps to `ErrUnavailable`.
   - OpenFGA `ResponseCode()`: `authorization_model_resolution_too_complex` maps to
     `ErrUnavailable`.
   - Slice B imports `genproto/googleapis/rpc/errdetails`, which is already transitive.
2. **Conditional answers (port, A).** Caveats and conditions are out of scope. A
   conditional answer is the sdk error `ErrConditional`, which wraps no sdk class
   (it is a server-side misconfiguration, so 500).
   - The SpiceDB adapter maps CONDITIONAL to it.
   - OpenFGA reports a generic `validation_error`; slice D classifies it best-effort,
     and the conformance suite does not pin OpenFGA's class.
3. **Lookup is over permissions only (port, A).** `LookupResources` with a relation is
   `ErrInvalidInput` for every implementation, because the engine cannot enumerate
   relations. `Check` still accepts permission or relation.
4. **Snapshot wording (port, A).** "No cross-page snapshot is promised; an
   implementation MAY serve one (SpiceDB does)." An expired cursor maps to
   `ErrInvalidCursor`.
5. **D8 label mapping (A).** `WithGlobalResource(resourceType, id string, relationFor
   func(label string) string)` exists on both the lowered evaluator and the engine
   adapter, so `HasRole("steward", Global())` checks `role_steward` and never the
   permission `steward`.
   - The engine adapter maps `relationFor(label)` on the global resource to an exact
     `tuples.Global()` read of `label`.
6. **Declared relations (docs, A).** A host moving a guard onto `NewLoweredEvaluator` must
   declare its scoped role labels as relations (D10/R19). gps-360-go declares none today.
   That only matters if it ever leaves the engine, which is never required.
7. **503 mapping (A).** `Require` maps any `sdk.ErrUnavailable` to 503, not only
   `model.ErrEvaluationLimit`.
8. **Kind-aware validation (A).** *(Built differently, 2026-10-21; resolved by owner
   ruling D6′ the same day: the strict boundary described below is gone. The sdk
   `Validate` methods check structure only, the engine adapter and the lowered evaluator
   use the pocket's lenient grammar, and `ValidatePortable*` is the opt-in strict check.
   Built as sdk `1f560db9` / `8da2d640` and pocket `010890b3`.)*
   - `tuples.ValidateRefField` is **unchanged** (lenient). Strict delegation broke
     deliberate free-form-ID tests and would have changed raw tuple and engine behaviour
     for hosts, which contradicts P3 and rev 3's "raw tuples APIs unchanged".
   - The strict D6 grammar is enforced at the protocol boundary: the sdk `Validate`
     methods, the engine adapter and the lowered evaluator.
   - The engine's own decision path uses lenient `model.Validate*` helpers.

   Original text: `tuples.ValidateRefField` delegates to `ValidateRef` only
   for identifier fields (types, relations, permissions, ids). Path-parameter names and
   slot keys keep the lenient check. The real names `organizationId`, `timelineID`,
   `spaceID`, `tenantID` and `programId` fail D6 and would panic at mount.
9. **Engine batch (A).**
   - The adapter pre-checks `MaxCheckBatch`, so an oversize batch is `ErrInvalidInput`,
     not the engine's `ErrEvaluationLimit`.
   - A mixed batch (permissions plus relations or global roles) runs as **one snapshot**
     through a new in-pocket multi-expression batch entry.
10. **Conformance shape (A).**
    - `Run(t *testing.T, implementation string, factory)`;
    - fixtures carry a `Diverge` map keyed by implementation;
    - separate `Checks` and `Lookups` slices;
    - the spike's `fixtures.go` types are the starting point.
11. **Shared cursor envelope (port, A).** The sdk exports `EncodeCursor` / `DecodeCursor`
    (version + fingerprint + inner); every implementation uses it.
12. **Decorators (docs, A).** A host decorator must forward `SchemaReader` as well as
    `LoweredPolicy()`.
13. **Wildcards (D).** Case 24 now reads: "a `type:*` is never *nameable* through the
    port; a natively written wildcard grants through Check and Lookup."
14. **SpiceDB default consistency (B).** Default to `fully_consistent`. `minimize_latency`
    was stale on 95–97 of 100 checks right after a grant; `fully_consistent` gave 0.
15. **Lowered evaluator placement (A).** It lives in package `decisions`, because it
    needs `Expression`'s unexported selector state. The engine adapter needed no new
    exports.

### Slice A: read port in sdk; the pocket implements and consumes it

**sdk**
- `sdk/capabilities/authorization`: the read half, `doc.go`, and `authorizationtest`
  with the read families and fixture data.
- `sdk/README.md`: add `authorization` to the capability list (`:34`) and add a row
  to the packages table (`:110–126`).

**Docs.** Apply the steward's text verbatim:
- **`ARCHITECTURE.md:194`** becomes: `| sdk/capabilities/authorization — the Zanzibar
  read protocol (Checker, Lookup, composite Authorizer; writes later) |
  pockets/authorization | IMPLEMENTS the protocol as the implementation of record
  (in-core engine adapter, Components.Authorizer) AND CONSUMES it
  (decisions.NewLoweredEvaluator); Unrestricted, reasons/explain,
  Expression/Require, roles exactness, ledger, audit, TupleCache, FilterPage stay
  pocket-owned |`. This is the first dual-role row in that table.
- **`pockets/README.md:417–418`** becomes: "Authorization's Zanzibar read protocol
  (Check/CheckBatch/paged lookup; single-resource writes later) graduated as
  sdk/capabilities/authorization (2026-10-21) with pockets/authorization as the
  implementation of record; the decision vocabulary beyond it stays pocket-owned. The
  criterion-2 failure recorded here applied to that full vocabulary, not the common
  denominator."

**Pocket**
- **Engine adapter.**
  - Lives in its own public package `logic/<concern>` (name settled at plan
    tasking). Never in `logic/decisions` or `internal/`: slice C's `Writer` over
    `mutations` would break G27 in decisions, and the adapter is exposed on
    `Components`.
  - `Components.Authorizer` is the **concrete** adapter type. Inside the pocket the
    sdk package is imported under an alias, because the root package is also named
    `authorization`.
  - What it does:
    - D10 pre-check for declared permissions **and relations**;
    - D6;
    - maps the D8 global resource to `tuples.Global()` exact role reads (R19);
    - converts types;
    - maps `Unrestricted` to the pocket's `ErrUnrestricted`;
    - wraps cursors in the envelope.
- **`decisions.NewLoweredEvaluator(authorization.Checker, opts…)`.**
  - Implements `DecisionService` using the lowering algorithm above.
  - `WithGlobalResource` (D8).
  - Snapshots `SchemaReader` at construction, because validation does no I/O
    (`routes.go:39`).
  - Exposes the exported marker `LoweredPolicy() bool`. Q1 checks it. A host
    decorator must forward it, or `Require` treats the evaluator as native; the
    README says so.
  - Without a `SchemaReader`, an undeclared name is caught only at evaluation. The
    evaluator re-wraps the Checker's `ErrInvalidInput` as a server-class error, so a
    host misconfiguration never surfaces as a client 400.
- **Delegations.**
  - `tuples.ValidateRefField` delegates kind-aware (amendment 8).
  - `model.ErrInvalidCursor` is an alias of the sdk sentinel.
- **Tests.**
  - `authorizationtest.Run` green on the engine adapter over memory, pgx and turso.
    Turso runs in the live leg; `make check` covers memory.
  - F1 lowering parity: `NewLoweredEvaluator(engineAdapter)` against native
    `EvaluateResolved` on every fixture. R19 makes this possible in-repo with no vendor.
- **Separate-module consumer (closes graduation criterion 1, R21).** `examples/auth-cms`
  consumes `Components.Authorizer` through the sdk type at one inbound seam: its
  documents `FilterAuthorized`.
- README and UPGRADE sections.

**Release**
1. Audit sdk v0.9.0 → v0.15.0 for breaking changes. The pocket and all three stores
   pin sdk v0.9.0.
2. Tag `sdk/v0.15.0` (MINOR, additive).
3. Tag `pockets/authorization/v0.27.0`, pinned to the new sdk. **BREAKING, pre-1.0**
   (R16: the `Check` / `CheckBatch` result type). The RELEASING entry lists the
   retypes each host needs.
4. Open a repin PR for **pgx, turso and goredis**: `go.mod` bumps to the new sdk and
   pocket. goredis also pins pgx and turso. The shipped code is unchanged, so no
   store tags.

**Gates**
- `make check` and `make guard`.
- `GOWORK=off go build ./... && go vet ./...` on pgx, turso and goredis against the
  new pins **before** the pocket tag. `go.work` hides MVS drift.
- The live PG store legs on merged main before the pocket tag.
- Cold-verify every tag.

**Host impact:**
- Retype the result of any narrow `Check` / `CheckBatch` interface to
  `authorization.Decision` (gps-360-go 2 ports, Segovia's `CheckBatch` ports).
- Everything else compiles through the deprecated aliases.
- Hosts that bump sdk cross v0.12 → v0.15, per the audit.
- Host handoffs are written at slice A release.

#### Slice A tasks

There are three PRs. A1 and A2 are built **together** in one worktree, under `go.work`,
so that `authorizationtest` is proven against the engine before the sdk is tagged. They
merge in sequence.

| task | PR | content | verify |
|---|---|---|---|
| A0 | (none, lead) | sdk v0.9.0 → v0.14.0 audit for the pocket and the pgx/turso/goredis stores (RELEASING entries). Findings recorded here before A1 opens. | written list of breaking items + their impact |
| A1 | PR-1 `sdk` | `sdk/capabilities/authorization`: <br>• types, `Checker` / `Lookup` / `Authorizer`, `SchemaReader`, `Limits` / `Limiter`<br>• `ErrInvalidCursor`, `ErrConditional`<br>• `ValidateRef`, `ValidateObject`<br>• `EncodeCursor` / `DecodeCursor`, `FilterAuthorized` (chunked), `LookupAll` (dedupe, repeated-cursor guard), `doc.go`<br>• `authorizationtest`: `Run(t, impl, factory)` + the four fixtures as data, ported from the spike<br>• `sdk/README.md` rows<br>Stdlib + sdk root only. | `make check`, `make guard` (G12 auto-covers); helper unit tests; `authorizationtest` exercised by A2 in the same worktree before PR-1 opens |
| A2 | PR-2 `pockets/authorization` (after `sdk/v0.15.0` is tagged; pins it) | <br>**(a) Types:** `model.*` deprecated aliases; `Validate` moves to the sdk; `PrincipalFrom` becomes the identity; `Service.Check` / `CheckBatch` return `authorization.Decision`; reasons stay internal and in `CheckExplain`; storetest moved off reason fields.<br>**(b) Engine adapter:** `logic/authorizer` (type `Engine`; `Components.Authorizer *authorizer.Engine`) with D10, D6 + `ValidateObject`, amendments 3/5/9, the `ErrUnrestricted` mapping and the envelope.<br>**(c) Lowered evaluator:** `decisions.NewLoweredEvaluator` with S1 (speculative checks + pure `Path`), the `LoweredPolicy()` marker, the `SchemaReader` snapshot, the server-class re-wrap and deny-on-inexpressible.<br>**(d) `inbound/http`:** `Path` purity marker, Q1 refusal without `WithLoweredPolicy()`, `sdk.ErrUnavailable` → 503.<br>**(e) Validation:** kind-aware `ValidateRefField`.<br>**(f) Tests:** `authorizationtest.Run` on the engine over memory (`make check`) and pgx/turso (live leg); the F1 parity suite (lowered-over-engine-adapter vs native on every fixture, short-circuit cases included) using the spike's engine models.<br>**(g) `examples/auth-cms`:** consumes `Components.Authorizer` via the sdk type for documents `FilterAuthorized`.<br>**(h) Docs:** ARCHITECTURE row, pockets/README, pocket README + UPGRADE, RELEASING draft entry ("BREAKING, pre-1.0"). | `make check`, `make guard`, `gopernicus guard` on examples; live PG legs on merged main; the `GOWORK=off` store build against the new pins (temporary `replace` to the merge commit) **before** the pocket tag; real behaviour: auth-cms boots, and a document list filtered through the port matches the native path |
| A3 | PR-3 stores repin | pgx, turso, goredis `go.mod` → sdk v0.15.0 + authorization v0.27.0 (goredis also its pgx/turso pins); test-only, no tags | `GOWORK=off go build ./... && go vet ./... && go test ./...` per store |
| A4 | PR-4 docs | RELEASING record finalised; host handoffs in `plans/` for gps-360-go and Segovia (the `Decision` retype, the sdk catch-up) | `make guard` |

**A0 result (lead, 2026-10-21).** `git diff sdk/v0.9.0 sdk/v0.14.0`: 30 files, +2975 / −41.
- No exported func or type was removed.
- The −41 lines are struct field re-layouts (email `Message` / `Request` gained fields),
  comments, and internal rewrites (`StaticFileServer`, origin matching).
- The RELEASING entries for sdk v0.10–v0.14 are all additive. Behaviour changes are
  confined to:
  - `web.StaticFileServer` asset caching (v0.12);
  - email unsubscribe headers (v0.11);
  - namespace origin admission (v0.10, opt-in).
- The authorization pocket and its stores use none of these, so the sdk catch-up is
  **safe for them**.
- Hosts already pin sdk ≥ v0.12.

Tags (owner): `sdk/v0.15.0` after PR-1, then `pockets/authorization/v0.27.0` after
PR-2, each cold-verified. There are no store tags.

### Slice B: `integrations/authorization/authzed` (read)

**Open from D6′ (decide in B).** The lowered evaluator re-wraps a Checker's
`ErrInvalidInput` as `ErrLoweredPolicyMisconfigured` (500). Over a strict vendor
backend, a client-supplied ID outside the vendor's grammar (e.g. `org.dot` from a
`Path`) would then be a 500 where the engine gives a 403. Proposed: an additive sdk
sentinel (e.g. `ErrUnrepresentable`, wrapping `ErrInvalidInput`) returned by vendor
adapters for an out-of-grammar *ID*; the evaluator denies on it for leaves fed by
`Path`/`Resource` input, and undeclared *names* stay a 500. No strict backend exists in
slice A, so A ships as built.

**Dependencies**
- The module requires sdk + `github.com/authzed/authzed-go`, plus the companions its
  API forces: `google.golang.org/grpc` with `codes` and `status`; `CheckBulkPermissionsPair`
  item errors are `google.rpc.Status`, converted with `status.FromProto`.
- `New(client *authzed.Client, opts…)` takes a ready client, so credentials stay
  with the host.
- `insecure` and `grpcutil.WithInsecureBearerToken` are test-only. They give
  per-test isolation on `serve-testing`.

**Behaviour**
- `SchemaReader` over **`ReflectSchema`** (structured), never `ReadSchema`. Parsing
  `.zed` would pull in the spicedb module.
- `Limits()`.
- A **default consistency option**. Slice 0 decides the default; the test harness
  uses `fully_consistent` so that seeding is visible.

**Tests and Makefile**
- `authorizationtest.Run` against `serve-testing`, behind `-tags=integration`, with a
  loud skip on a missing `SPICEDB_ENDPOINT`.
- Makefile: `MODULES` and `INTEGRATION_TAG_MODULES`, a
  `test-authorization-integrations` target, and an optional `make dev-spicedb`.
- G13 auto-covers the module, because it globs `integrations/*/*/go.mod`.

**Example**
- `examples/authzed-minimal` proves levels 1 and 2: the guard over
  `NewLoweredEvaluator(authzed)` with `WithLoweredPolicy()`, zero datastore drivers,
  zero SQL.
- **Append it to G30's hard-coded host list** (`Makefile:652–654`).
- `gopernicus guard` must be green on it.

**Graduation:** the ARCHITECTURE row gains authzed as the second implementation.

**Tag:** `integrations/authorization/authzed/v0.1.0`.

### Slice C: writes + consistency

- sdk write half + consistency (sdk MINOR).
- Engine `Writer` + `IdempotentWriter` over `mutations.Apply` in the adapter package
  (single `Target`, `WithAuditAttribution`, `ErrAmbientTransaction`,
  `ErrWriteTooLarge`; rev3 §2.2–§2.3).
- The engine's `Full` durable-read switch in `withOperation` (rev3 §3b; engine-internal).
- authzed writes and ZedTokens.
- The Write / Consistency / Idempotent families in `authorizationtest`.
- **Rulings due:** D3, D4, D7, R14, Q2 (preconditions), Q4.
- **Tags:** sdk MINOR, pocket MINOR, `authzed/v0.2.0`.

### Slice D: `integrations/authorization/openfga` (full)

- **Dependencies:** requires sdk + `github.com/openfga/go-sdk`. Its direct
  requirements are otel, otel/metric and `sourcegraph/conc`, all transitive. It
  needs go ≥ 1.26.0; the repo is on 1.26.1.
- **Config contract:** the host passes `ServerListDeadline` and `ServerMaxResults`
  (O1 mitigation).
- **Pinned divergences:** no tokens, model-id pinning, wildcard invisibility
  (rev3 cases 24–26).
- **Checks:** the `type:id` 256-byte check, and the keyset paging described above.
- **Tag:** `integrations/authorization/openfga/v0.1.0`.
- **Ruling due:** D11, informed by O1–O3.

### Slice E: on demand

Each item ships only when there is demand:
- host handoffs to retype host seams to the sdk types (opt-in);
- a `FilterPage` twin over `CheckBatch` (R12);
- write preconditions;
- the write-outbox rail;
- D7(b)/(c) with the authentication session;
- `export-zed` / `export-fga` workshop verbs;
- a third vendor (Permify) as the "adding one is simple" proof.

## Rulings still open (slice A)

| id | question | recommendation |
|---|---|---|
All slice A rulings are made (owner, 2026-10-21):
- **R16:** the pocket USES the sdk types. Deprecated `model.*` aliases are kept for
  one minor; `Check` returns `authorization.Decision`. Details are in "Types" above.
  This overrides the reviewers' (b).
- **R17:** `Decision{Allowed}` only.
- **R18:** `Authorizer` / `Components.Authorizer` (concrete type).
- **R19:** the lowered evaluator works over the engine adapter (relations + global
  mapping), so F1 is proven in-repo in slice A.
- **R20:** strict `Limit`.
- **R21:** criterion 1 closes in slice A via `examples/auth-cms`.

Due at slice C: D3, D4, D7, R14, Q2, Q4. Due at slice D: D11. Due at slice E: R12.

## Risks

- **Semantic drift between implementations.** It is mitigated by divergences being
  asserted as divergences, the strict lookup contract, and the cursor envelope.
- **OpenFGA truncation (O1)** is now the likeliest drop-out. The deadline
  configuration contract is the mitigation; if it fails, slice D is deferred.
- **gps-360-go cannot move to an external engine without a model rewrite** (rev3
  §3c). Every handoff says so; the move is never required.
- **The sdk catch-up.** Six sdk minors under the pocket and the stores. The audit and
  the `GOWORK=off` gate catch it before any tag.
- **Release order** is sdk tag → pocket tag → store repin. The pocket cannot be
  tagged until the sdk tag resolves on the proxy.

## Review record (2026-10-21, rev 4 → 4.1)

**architecture-steward** (aligned-with-edits)
1. Criterion 1 needs a separate-module consumer → R21, slice A auth-cms.
2. `ErrUnrestricted` out of the sdk → done.
3. `ErrInvalidCursor` wraps `ErrInvalidInput` → done.
4. Alias OK, and userset enforcement sits in `ValidateRef` → done.
5. R16–R18 endorsed; `Components` holds the concrete type → done.
6. The adapter lives in its own `logic/<concern>`, never decisions or internal (G27) → done.
7. Name the dual role → done.
8. G30 host list, `MODULES` and `INTEGRATION_TAG_MODULES` → slice B.
9. Exact doc text, plus `sdk/README.md` → slice A.

**lead-backend-engineer** (ship-with-edits)
1. The engine adapter can't answer lowered `HasRole` / `HasRelationship` → the
   Permission doc, relations in D10, and R19.
2. Eager batching breaks the short-circuit → the lowering algorithm and F1 cases.
3. `ValidateExpression` without a schema → the `SchemaReader` shape, the snapshot,
   and the 500-class re-wrap.
4. Q1 marker → `LoweredPolicy() bool`.
5. Sentinel wrapping and `ValidateRefField` delegation → done.
6. `MaxCheckBatch` and the batch order clause → done.
7. sdk v0.9 → v0.15 audit → slice A release.
8. `ErrUnrestricted` "do not retry" → done.

**data-integration-reviewer** (ship-with-edits)
1. OpenFGA deadline truncation → O1 reworded, 23b, the config contract.
2. `Limit` on OpenFGA → R20.
3. SpiceDB duplicates → page uniqueness and `LookupAll` dedupe.
4. Cursor envelope → done.
5. Factory-shaped fixtures; a missing schema fails; freshness after seeding → done,
   slice B default.
6. Seven missing read cases → slice 0.
7. `ReflectSchema` → slice B.
8. Dependency footprints → slices B and D.
9. Stores not test-only under MVS; goredis omitted → the `GOWORK=off` gate and
   goredis in the repin.
10. OpenFGA `type:id` cap → slice D and slice 0.
