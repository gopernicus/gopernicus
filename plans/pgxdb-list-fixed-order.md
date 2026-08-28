# pgxdb.List: a store-fixed composite ORDER BY for the offset strategy

**Status: EXECUTED 2026-08-28 — gopernicus/gopernicus#15** (owner asked to
work through the issue the same day; owner rules patch vs minor and cuts the
tag). Executed as written: `FixedOrder` on `ListQuery`, `checkFixedOrder`
refusals, verbatim `ORDER BY` in the offset flow, hermetic + live tests, README
convention bullet, RELEASING entry. Origin (Josh: "i'd like to write a plan
for the OffsetPage in gopernicus"). Origin: gps-360-go, which carried a host helper
`OffsetPage[R, D]` in nine stores and found, when retiring it, that
`pgxdb.List` already does everything it did — offset strategy, `LIMIT n+1`
over-fetch for `HasMore`, `HasPrev` from the offset, `WithCount` → `Total`,
the search clause folded before the strategy switch — except one thing.

## The gap

`ListQuery` orders by ONE request-resolved field from `OrderFields` (or
`DefaultOrder`) plus the `PK` tiebreak. Nine gps-360-go lists are not
user-sortable at all; each has a store-fixed, multi-column order the product
defines:

| store | order |
|---|---|
| opportunities | `closing_date DESC NULLS LAST, name ASC, id ASC` |
| internal projects | `spotlight DESC, name ASC, id ASC` |
| districts | `state ASC, level ASC, district_raw ASC, id ASC` |
| feedback | `submitted_at DESC, id DESC` |
| influencers / partnerships / crm lists / races / offices | `name ASC, id ASC` and kin |

`OrderFields` cannot express `NULLS LAST` or a second sort column, so those
stores bypass `List` with a hand-rolled `LIMIT/OFFSET` query and re-implement
`HasMore` and the `COUNT(*)` wrap — the duplication `List` exists to remove.

## Decision (proposed)

One additive field on `ListQuery`, offset strategy only:

```go
type ListQuery[T any] struct {
	…
	// FixedOrder is a store-authored ORDER BY expression (without the keyword)
	// for a list whose order is not the caller's to choose: composite columns,
	// NULLS LAST, computed sort keys. When set, OrderFields/DefaultOrder are
	// not consulted, a request carrying an Order is sdk.ErrInvalidInput, and the
	// list is offset-only — a cursor request is sdk.ErrInvalidInput, because a
	// keyset predicate over an arbitrary expression is not derivable.
	FixedOrder string
}
```

Rules:

1. `FixedOrder` and `OrderFields` are mutually exclusive; both set is a
   programming error reported by `List` as `sdk.ErrInvalidInput` on first call
   (the same posture as an order field absent from the allow-list).
2. The expression is trusted store text (like `BaseSQL`), never request data.
3. Offset flow unchanged otherwise: `LIMIT n+1 OFFSET`, `HasMore`, `HasPrev`,
   `WithCount`, search, `MapError`. `PK` stays required for the tiebreak the
   store should include in `FixedOrder` itself; `List` does not append it.
4. Cursor flow: `FixedOrder` set + `ResolvedStrategy() == StrategyCursor` →
   `sdk.ErrInvalidInput` naming the reason.

## Changes

1. `integrations/datastores/pgxdb/list.go` — the field; `resolveOrder` short-
   circuits to `FixedOrder`; `listOffset` writes `ORDER BY <FixedOrder>`
   verbatim; `listCursor` refuses.
2. `integrations/datastores/pgxdb/list_test.go` — fixed order with `NULLS
   LAST` and two columns; `HasMore`/`HasPrev`/`Total` unchanged under it;
   request `Order` refused; cursor strategy refused; both-set refused.
3. `integrations/datastores/pgxdb/README.md` — one paragraph under the store
   conventions: "a list whose order is the product's, not the caller's".

Patch-scoped by the RELEASING.md rule (additive field, zero value preserves
today's behaviour, no schema, no sibling bump) — owner ruling on patch vs
minor, as with `ProbeTable`.

## Adoption in gps-360-go (after the tag)

Replace the nine local `offsetPage` helpers (`internal/outbound/domains/
{businessdevelopment,content,geography,races}/rows.go`) with `pgxdb.List` +
`FixedOrder`; delete `rows.go`'s `offsetPage`. No behaviour change: the same
SQL shape, the same page fields.
