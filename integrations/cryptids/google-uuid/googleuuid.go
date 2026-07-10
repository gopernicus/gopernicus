// Package googleuuid is an identifier connector wrapping exactly one
// third-party library, github.com/google/uuid. Its constructors return
// cryptids.GenerateFunc values, so a host chooses uuid-shaped entity keys the
// same way it chooses any other ID strategy: once, at wiring, on a feature's
// Config.IDs.
//
// It owns "how to mint a uuid with google/uuid," never any feature's ID
// policy. Two shapes are offered: V4 (fully random — the interoperable
// default) and V7 (time-ordered — its text form sorts by creation time, which
// keeps uuid keys friendly to created-at/id keyset pagination and B-tree
// locality). Both emit the canonical lowercase 36-character text form, so the
// bundled text-keyed stores persist them unchanged.
package googleuuid

import (
	"github.com/google/uuid"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Compile-time proof the constructors satisfy the sdk-owned port.
var (
	_ cryptids.GenerateFunc = V4()
	_ cryptids.GenerateFunc = V7()
)

// V4 returns a GenerateFunc minting canonical lowercase UUIDv4 strings
// (122 random bits). The only error source is the OS entropy pool.
func V4() cryptids.GenerateFunc {
	return func() (string, error) {
		u, err := uuid.NewRandom()
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}
}

// V7 returns a GenerateFunc minting canonical lowercase UUIDv7 strings: a
// millisecond timestamp prefix over random bits, so the text form is
// time-ordered. Prefer it for database keys; prefer V4 when IDs must not
// reveal creation time.
func V7() cryptids.GenerateFunc {
	return func() (string, error) {
		u, err := uuid.NewV7()
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}
}
