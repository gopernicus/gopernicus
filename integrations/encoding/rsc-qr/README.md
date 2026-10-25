# integrations/encoding/rsc-qr

QR module matrices using `rsc.io/qr` v0.2.0. The package name is `rscqr`.
It implements no sdk port: `Modules` matches the views/goth authentication
`QRModules` hook by shape, so a host hands it over in `cmd` and neither module
imports the other:

```go
authgoth.WithQR(rscqr.Modules)
```

## Surface

| Member | Behavior |
|---|---|
| `Modules(text string) ([][]bool, error)` | Encodes `text` at error-correction level M in the smallest version (1-40) that holds it. Returns the matrix row-major, `Size` rows of `Size` modules (`Size = 17 + 4*version`), `true` meaning dark. |

An empty text, or one past a version-40 code's level-M capacity (2331 bytes of
arbitrary text), returns an error matching `sdk.ErrInvalidInput`. rsc.io/qr's
message is kept for logs; none of its errors is reachable through `errors.Is`
or `errors.As`.

## Quiet zone

The matrix carries no quiet zone: row 0 and column 0 hold the top-left finder
pattern. The renderer draws a light border at least 4 modules wide on every
side, and dark modules on a light background; scanners need both.

## Dependency

`rsc.io/qr` v0.2.0 (BSD-3-Clause, no dependencies of its own) was last
released in 2018. QR (ISO/IEC 18004) is a frozen specification, so the age is
not a defect in itself. It always uses mask pattern 0 rather than picking the
lowest-penalty mask; the codes still decode.

## Testing

Run `go test ./...`. A known-text matrix pins, per text (numeric,
alphanumeric, an `otpauth://` URI, and the 2331-byte capacity edge): the size,
a SHA-256 of the matrix, the three finder patterns and their separators, the
timing patterns, the dark module and the format information (level M, mask 0).
Further tests compare the matrix with rsc.io/qr's own code at level M (and not
L), prove that padding it with a 4-module border reproduces the code at every
offset with the border light, and refuse the empty and over-capacity texts.
There is no decode round trip: the standard library has no QR decoder.
