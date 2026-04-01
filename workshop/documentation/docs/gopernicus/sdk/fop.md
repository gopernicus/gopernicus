---
sidebar_position: 6
title: FOP
---

# SDK — FOP

`sdk/fop` provides the primitives for **F**iltering, **O**rdering, and **P**agination. It wraps `encoding/base64`, `encoding/json`, `strconv`, and `strings` from the standard library.

These types flow from the bridge layer (HTTP query params) through core (repository interfaces) down to the database layer. The SDK defines the shared vocabulary; the bridge layer handles parsing from HTTP and formatting the response — see [Bridge FOP](../bridge/fop.md).

## Ordering

`Order` carries a field name and direction. `ParseOrder` parses the `field:direction` query param format and validates against an allowlist of sortable fields:

```go
// Define the fields callers are allowed to sort on
fields := map[string]fop.OrderField{
    "created_at": {Column: "created_at"},
    "name":       {Column: "name", CastLower: true},
}

defaultOrder := fop.NewOrder("created_at", fop.DESC)

order, err := fop.ParseOrder(fields, r.URL.Query().Get("order_by"), defaultOrder)
// "name:asc"  => Order{Field: "name", Direction: "ASC"}
// "created"   => error: unknown order field
// ""          => defaultOrder
```

`CastLower: true` signals to the query builder to wrap the column in `LOWER()` for case-insensitive text sorting. Direction parsing is case-insensitive; an empty or missing direction defaults to `ASC`.

## Pagination

`ParsePageStringCursor` parses limit and cursor from query string values with built-in bounds checking:

```go
page, err := fop.ParsePageStringCursor(
    r.URL.Query().Get("limit"),
    r.URL.Query().Get("cursor"),
    100, // maxLimit
)
// page.Limit  — clamped to [1, maxLimit], defaults to 25
// page.Cursor — opaque string, empty on first page
```

`Pagination` is the response metadata returned alongside list results:

```go
type Pagination struct {
    HasPrev        bool   `json:"has_prev,omitempty"`
    Limit          int    `json:"limit,omitempty"`
    PreviousCursor string `json:"previous_cursor,omitempty"`
    NextCursor     string `json:"next_cursor,omitempty"`
    PageTotal      int    `json:"page_total,omitempty"`
}
```

## Cursors

Cursors are base64url-encoded JSON tokens. They carry the order field, the order value at the page boundary, and the primary key — enough for the database to resume from exactly that position.

```go
// Encode — called when building the next/prev cursor for a response
token, err := fop.EncodeCursor("created_at", "2024-01-15T10:30:00Z", "user_abc123")

// Decode — called when processing an incoming cursor
cursor, err := fop.DecodeCursor(token, "created_at")
if cursor == nil {
    // empty token (first page) or stale cursor (order field changed) — treat as first page
}
// cursor.OrderField, cursor.OrderValue, cursor.PK
```

A cursor whose `OrderField` doesn't match the current sort order is silently treated as a first-page request rather than an error. This handles clients that hold onto a cursor across sort order changes gracefully.
