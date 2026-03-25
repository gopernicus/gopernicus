# sdk/fop -- Filter/Order/Pagination Reference

Package `fop` provides cursor-based pagination, ordering, and pagination metadata types used by generated repository queries and bridge handlers.

**Import:** `github.com/gopernicus/gopernicus/sdk/fop`

## PageStringCursor

Represents a paginated request parsed from query parameters.

```go
type PageStringCursor struct {
    Limit  int
    Cursor string
}
```

### ParsePageStringCursor

Parses `limit` and `cursor` from query string values. Default limit is 25. The `maxLimit` parameter caps the page size (falls back to `DefaultMaxLimit` of 100 when <= 0).

```go
page, err := fop.ParsePageStringCursor(
    web.QueryParam(r, "limit"),   // e.g. "50"
    web.QueryParam(r, "cursor"),  // e.g. "eyJvcmR..."
    0,                            // 0 = use DefaultMaxLimit (100)
)
```

Returns an error if limit is non-numeric, <= 0, or exceeds maxLimit.

## Cursor

Stores the pagination position for keyset (cursor-based) pagination. The cursor includes the order field name so that stale cursors created under a different sort order are detected and gracefully ignored.

```go
type Cursor struct {
    OrderField string `json:"order_field"`
    OrderValue any    `json:"order_value"`
    PK         string `json:"pk"`
}
```

### EncodeCursor / DecodeCursor

```go
token, err := fop.EncodeCursor("created_at", row.CreatedAt, row.ID)

cursor, err := fop.DecodeCursor(token, "created_at")
// cursor is nil (not error) when:
//   - token is empty (first page)
//   - cursor's order field doesn't match (stale cursor, treat as first page)
```

Cursors are base64url-encoded JSON. `DecodeCursor` only returns errors for truly malformed tokens.

## Order

Represents a sort field and direction for query results.

```go
type Order struct {
    Field     string // DB column name
    Direction string // "ASC" or "DESC"
}
```

### Constants

```go
const (
    ASC  = "ASC"
    DESC = "DESC"
)
```

### NewOrder

Constructs an `Order`. Invalid directions default to ASC.

```go
order := fop.NewOrder("created_at", fop.DESC)
```

### ParseOrder

Parses a `field:direction` string from the query parameter against allowed fields. Returns `defaultOrder` when `orderBy` is empty.

```go
fields := map[string]fop.OrderField{
    "created_at": {Column: "created_at"},
    "name":       {Column: "name", CastLower: true},
}

order, err := fop.ParseOrder(
    fields,
    web.QueryParam(r, "order"),           // e.g. "name:desc"
    fop.NewOrder("created_at", fop.DESC), // default
)
```

`OrderField.CastLower` wraps the column in `LOWER()` for case-insensitive text sorting.

## Pagination (Response Metadata)

Returned alongside list results to inform the client about page position.

```go
type Pagination struct {
    HasPrev        bool   `json:"has_prev,omitempty"`
    Limit          int    `json:"limit,omitempty"`
    PreviousCursor string `json:"previous_cursor,omitempty"`
    NextCursor     string `json:"next_cursor,omitempty"`
    PageTotal      int    `json:"page_total,omitempty"`
}
```

## Integration with Generated Repositories

Generated stores accept `fop.PageStringCursor` and `fop.Order` for list queries. The generated query builder uses the cursor to add keyset WHERE clauses and the order to build ORDER BY + a tiebreaker on the primary key.

Typical bridge handler flow:

```go
func (b *Bridges) ListUsers(w http.ResponseWriter, r *http.Request) {
    page, err := fop.ParsePageStringCursor(
        web.QueryParam(r, "limit"),
        web.QueryParam(r, "cursor"),
        0,
    )
    if err != nil {
        web.RespondJSONError(w, web.ErrBadRequest(err.Error()))
        return
    }

    order, err := fop.ParseOrder(userOrderFields, web.QueryParam(r, "order"), defaultUserOrder)
    if err != nil {
        web.RespondJSONError(w, web.ErrBadRequest(err.Error()))
        return
    }

    users, pagination, err := b.userCore.List(r.Context(), page, order)
    if err != nil {
        web.RespondJSONDomainError(w, err)
        return
    }

    web.RespondJSONOK(w, map[string]any{
        "data":       users,
        "pagination": pagination,
    })
}
```

## Related

- [sdk/web](../sdk/web.md) -- query parameter extraction with `web.QueryParam`
- [infrastructure/database](../infrastructure/database.md) -- generated repository queries that consume fop types
