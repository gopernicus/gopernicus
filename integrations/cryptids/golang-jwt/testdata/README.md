# Legacy SDK token

`legacy-hs256.jwt` was generated and verified on 2026-09-09 with the actual
`sdk/pkg/cryptids.NewHS256` implementation at repository commit
`82aa19f7`, before its removal in audit S4. It contains dummy data only.

- Key bytes: `test-secret-key-at-least-32-chars-long` (literal UTF-8/ASCII).
- Claims: `user_id: "u123"`, `session_id: "s456"`.
- `iat`: `1577836800` (2020-01-01 UTC), explicitly supplied through the legacy
  signer to keep the fixture valid independently of its generation date.
- `exp`: `4102444800` (2100-01-01 UTC), supplied through `expiresAt`.

Generation called the then-current SDK directly:

```go
signer, err := cryptids.NewHS256([]byte("test-secret-key-at-least-32-chars-long"))
// Check err.
token, err := signer.Sign(map[string]any{
    "user_id": "u123", "session_id": "s456", "iat": int64(1577836800),
}, time.Unix(4102444800, 0))
// Check err; signer.Verify(token) also succeeded before saving the fixture.
```

Tests verify this saved token with the integration and assert its claims and
key mismatch behavior. Keep the captured bytes; do not regenerate with the new
adapter or retain the retired SDK parser just for testing.
