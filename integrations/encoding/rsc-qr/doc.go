// Package rscqr is a QR-encoding connector wrapping exactly one third-party
// library, rsc.io/qr. Modules returns a code's module matrix for a renderer to
// draw; it implements no sdk port. Its signature matches the views/goth
// authentication QRModules hook by shape, so neither module imports the other.
//
// # Level and matrix
//
// Every code is encoded at error-correction level M (about 15% of codewords
// recoverable), the smallest version 1-40 that holds the text. The matrix is
// row-major, Size rows of Size modules (Size = 17 + 4*version), true meaning
// dark. It carries NO quiet zone: row 0 and column 0 hold the top-left finder
// pattern. The renderer draws the 4-module light border scanners require.
//
// # Errors
//
// An empty text, or one too long for a version-40 code at level M (2331 bytes
// of arbitrary text), returns an error matching sdk.ErrInvalidInput. rsc.io/qr's
// message is kept for logs; none of its errors is reachable through errors.Is
// or errors.As.
package rscqr
