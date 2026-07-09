# Phase P1 — sdk/crud List standards

Status: **RATIFIED 2026-07-08 (jrazmi — Q1/Q2/Q3 at recommendations; see 00-overview.md)**
Executor model: opus
Depends on: — (first leg)

## Context

`sdk/crud` already carries the vocabulary (Order/ParseOrder,
HasPrev/PreviousCursor/MarkPrevPage) but nothing downstream honors it, and
two capabilities are net-new: optional total counts and an optional
limit/offset mode. This phase pins and lands the request/page semantics
that every connector, store, memstore, and HTTP edge implements in P2–P5.
`sdk/crud` stays stdlib-only and connector-neutral — no SQL, no pgx types.

## Goal

`crud.ListRequest`/`crud.Page` express ordering, bidirectional cursor
paging, offset paging, and optional counts, with the transport-edge parser
and store-edge validation extended to match — fully unit-tested, fully
documented.

## Definition of Done

- The pinned API below compiles and is tested for every mode/edge case.
- The package doc's "two limit-validation semantics" section extends to
  the new fields; the mode-selection and count semantics are documented
  in one place (the package doc).
- `sdk` still has an empty go.mod require block; `make guard` green.
- No behavior change for existing callers beyond the `ParseListRequest`
  signature (one in-repo caller, updated in P3).

## The pinned API (ratified shape; field-level spec)

```go
// ListRequest — one request type, two modes.
type ListRequest struct {
    Limit     int    // page size; 0 = DefaultLimit at the store edge (unchanged)
    Cursor    string // cursor mode (default): opaque keyset token from a prior Page
    Offset    int    // offset mode: Offset > 0 selects offset mode
    Order     Order  // zero value = the aggregate's default order (unchanged field)
    WithCount bool   // when true the store also computes Page.Total
}

// Page gains the optional total.
type Page[T any] struct {
    Items          []T    `json:"items"`
    NextCursor     string `json:"next_cursor,omitempty"`
    HasMore        bool   `json:"has_more,omitempty"`
    HasPrev        bool   `json:"has_prev,omitempty"`
    PreviousCursor string `json:"previous_cursor,omitempty"`
    Total          *int64 `json:"total,omitempty"` // nil = not requested
}
```

Semantics (the normative matrix — reproduce in the package doc):

- **Mode selection.** `Offset > 0` ⇒ offset mode; otherwise cursor mode.
  `Cursor != "" && Offset > 0` is invalid — rejected at BOTH edges.
  `Offset < 0` is invalid at both edges. (`Offset == 0, Cursor == ""` is
  simply the first page; both modes agree on its rows.)
- **Cursor mode** (default, unchanged forward semantics): over-fetch
  limit+1 → `TrimPage` → `HasMore`/`NextCursor`. When `Cursor != ""`,
  the store ALSO runs a reverse probe (flipped comparison operators,
  flipped ORDER BY, results reversed back — the
  gopernicus-original users/generated.go:194-202 protocol) and applies
  `MarkPrevPage`: any probe row ⇒ `HasPrev`; a full window (len == limit)
  ⇒ `PreviousCursor` = probe's first record; a partial window ⇒
  `HasPrev` with empty `PreviousCursor`, meaning "the previous page is
  the first page". First page ⇒ `HasPrev = false`.
- **Offset mode:** same ORDER BY (order + pk tiebreaker), `LIMIT n+1
  OFFSET off`; `HasMore` from the over-fetch; `HasPrev = Offset > 0`;
  `NextCursor`/`PreviousCursor` stay empty — the caller does the offset
  arithmetic.
- **WithCount** (both modes): `Total` = the full matching row count under
  the list's filter WHERE — never including cursor/offset predicates,
  never capped by limit. `nil` when not requested.
- **Stale cursor** (order field changed between requests): the existing
  `DecodeCursor` rule — treated as first page — is now load-bearing and
  gets a conformance case in every feature suite (P3–P5).

New/changed functions:

```go
// Validate is the store-edge mode check (called by the shared connector
// helpers and hand-rolled Lists before touching SQL). Cursor+Offset both
// set, or a negative Offset, returns an error wrapping errs.ErrInvalidInput.
func (r ListRequest) Validate() error

// UsesOffset reports offset mode (Offset > 0).
func (r ListRequest) UsesOffset() bool

// MapPage converts a Page's item type, copying every pagination field
// (cursors, flags, Total). This is the row-struct→domain bridge:
// stores get Page[entryRow] from the connector helper and return
// crud.MapPage(p, toDomain).
func MapPage[T, U any](p Page[T], fn func(T) U) Page[U]

// ParseListRequest — strict transport-edge parser, extended in place
// (breaking; zero tags cut, one in-repo caller). Empty offsetStr = 0;
// non-numeric or negative offset = error; countStr parsed by
// strconv.ParseBool with "" = false; cursor+offset>0 = error. Limit
// semantics unchanged (strict, never clamped).
func ParseListRequest(limitStr, cursorStr, offsetStr, countStr string, maxLimit int) (ListRequest, error)
```

Standard query-param vocabulary (documented here, wired at each edge in
P3–P5): `limit`, `cursor`, `offset`, `count`, and `order=field:direction`
— `order` is parsed separately by the existing `ParseOrder` because the
allow-list is per-aggregate.

**Order vocabulary rule (pending Q1 in `00-overview.md`):** each paginated
aggregate declares its allow-list `map[string]crud.OrderField` + default
`crud.Order` in its feature-core domain package; `ParseOrder` validates at
the edge; `Order.Field` carries the resolved sort key (also the cursor's
order-field tag); backends validate again before use (QuoteIdentifier /
map membership) and never interpolate raw request input. `crud.OrderField`
and `ParseOrder` keep their current signatures.

Unchanged on purpose: `NormalizedLimit` (store-edge clamp),
`TrimPage`/`MarkPrevPage` (they already express the target semantics —
this milestone finally gives MarkPrevPage callers), the cursor codec, the
Reader/Writer/CRUD generics.

## Out of scope

- Any SQL or connector knowledge in sdk (P2 owns that).
- Sorting/filter DSLs beyond the existing `field:direction` form.
- Changing `DefaultLimit`/`MaxLimit` values.

## Module / API impact

`sdk/crud` only. Additive struct fields + two new funcs + one breaking
signature (`ParseListRequest`). Zero tags exist — no release obligation
(RELEASING.md). Stdlib-only preserved (guard-sdk-stdlib).

## Risks

1. Getting the mode/edge semantics subtly wrong here propagates to six
   backends — the matrix above is normative; the P1 unit tests must cover
   every row of it before P2 starts.

## Tasks

### task-1: ListRequest/Page extension + Validate/UsesOffset/MapPage

- **depends_on:** []
- **model:** opus
- **files:** [sdk/crud/crud.go, sdk/crud/pagination.go, sdk/crud/crud_test.go, sdk/crud/pagination_test.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Add `Offset`/`WithCount` to ListRequest and `Total *int64`
  to Page exactly as pinned; implement `Validate` (wrapping
  `errs.ErrInvalidInput`), `UsesOffset`, and `MapPage` (copies all five
  pagination fields + Total). Table-test the full semantics matrix rows
  that live in sdk: mode selection, both-set rejection, negative-offset
  rejection, MapPage field fidelity, NormalizedLimit unchanged.

### task-2: ParseListRequest extension + package doc

- **depends_on:** [task-1]
- **model:** opus
- **files:** [sdk/crud/pagination.go, sdk/crud/pagination_test.go, sdk/crud/crud.go]
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...` then `make check` — **expected to fail at features/authentication until its caller is updated; if so, update the single caller** `features/authentication/internal/inbound/http/machine.go:191` **mechanically in this task (pass "" for offset/count)** so every task boundary stays green, then `make check` and `make guard`
- **description:** Extend `ParseListRequest` to the pinned five-string
  signature with strict offset/count parsing and the cursor+offset
  rejection. Rewrite the package doc: extend the two-semantics section to
  offset (strict at the edge, `Validate` at the store), add the normative
  mode/count matrix and the query-param vocabulary, and document the
  order-vocabulary rule (with a pointer that allow-lists live per
  aggregate). Tests: every parse edge (empty/garbage/negative offset,
  count truthy forms, both-set, over-max limit unchanged).

## Acceptance

```sh
cd sdk && go build ./... && go vet ./... && go test ./...
make check     # 30 modules — the machine.go caller compiles against the new signature
make guard
```

## Real-interaction check

Standing check: `make check` green; boot `examples/minimal` (:8081),
`GET /` and `GET /products/widget-3000` → 200s, kill, port free. (No new
user-facing surface in this phase; green tests + the standing boot check
close it.)

## Execution log

### 2026-07-08 — phase 1 executed (implementer on opus); PHASE COMPLETE

Both tasks landed. `ListRequest` gains `Offset`/`WithCount`, `Page` gains
`Total *int64` (`total,omitempty`); `Validate` (wraps
`errs.ErrInvalidInput`), `UsesOffset`, `MapPage` (nil Items preserved as
nil — executor decision, JSON `null` vs `[]` fidelity, test-pinned);
`ParseListRequest` extended to the pinned five-string signature (strict
offset/count parse, cursor+offset rejection; new rejection strings follow
the existing limit-error phrasing — transport-edge errors stay plain
`fmt.Errorf` like the existing limit errors, `ErrInvalidInput` wrapping
is Validate's per the pin). Package doc rewritten: two-semantics section
extended to three postures (strict parse / NormalizedLimit clamp /
Validate mode check), normative mode+count matrix, query-param
vocabulary + Q1 order-vocabulary rule. Single mechanical caller update:
`features/authentication/internal/inbound/http/machine.go:191` passes
`""` for offset/count. Verified by the executor AND re-verified by the
main session: sdk build/vet/test green, `make check` green (30 modules),
`make guard` green (7 guards incl. guard-sdk-stdlib — sdk go.mod require
block still empty). Real-interaction check driven by the main session:
`examples/minimal` booted (`go run ./cmd/server` from the module dir),
GET / and GET /products/widget-3000 → 200, killed, :8081 free. Next: P2
(`02-connectors.md`).
