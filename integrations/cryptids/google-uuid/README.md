# integrations/cryptids/google-uuid

An identifier connector wrapping exactly one third-party library —
`github.com/google/uuid`. Its constructors return `cryptids.GenerateFunc`
values, so a host chooses uuid-shaped entity keys the same way it chooses any
other ID strategy: once, at wiring, on a feature's `Config.IDs`.

It owns "how to mint a uuid with google/uuid," never any feature's ID policy.
A different ID shape (the sdk's stdlib nanoid, a database-generated key via
`cryptids.Database`) is swapped at the composition root, not here.

## Surface

| member | shape |
|---|---|
| `V4() cryptids.GenerateFunc` | canonical lowercase UUIDv4 (122 random bits) |
| `V7() cryptids.GenerateFunc` | canonical lowercase UUIDv7 (time-ordered text form) |

## Wiring

```go
authentication.Config{
    IDs: cryptids.NewGenerator(googleuuid.V7()),
    // ...
}
```

## V4 vs V7

Both are 36-character canonical text, so the bundled text-keyed stores persist
them unchanged. V7 carries a millisecond timestamp prefix: its text form sorts
by creation time, which keeps uuid keys friendly to created-at/id keyset
pagination and B-tree locality — prefer it for database keys. Prefer V4 when
IDs must not reveal creation time.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — canonical-form
and version-nibble pins for both shapes, a 1000-mint uniqueness sweep, the
`cryptids.NewGenerator` wiring shape, and the V7 text-ordering property. A
compile-time assertion proves both constructors satisfy the sdk-owned port.
