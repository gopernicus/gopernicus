// Package passkey is the WebAuthn credential domain: one registered passkey per
// row, owned by one user. The ceremonies that create and assert passkeys run in
// the authentication service over the sdk webauthn port; this package holds the
// stored record, its naming rule and the repository contract. Removal is not a
// repository method: it rides the revision-serialized credential-mutation rail
// (credential.RemovePasskey, credential.RemovePasskeys), so a removal revokes
// every session and is policed like any other loss of a login method.
package passkey

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// MaxNameRunes bounds a passkey's display name.
const MaxNameRunes = 64

var (
	// ErrPasskeyLimit refuses a registration that would take the user past the
	// configured passkey cap. It matches sdk.ErrConflict.
	ErrPasskeyLimit = fmt.Errorf("passkey: the account holds the maximum number of passkeys: %w", sdk.ErrConflict)
	// ErrBootstrapClosed refuses a bootstrap-tier registration (CreateInput.
	// Bootstrap) for an owner who, by the time it is stored, holds an
	// AAL2-capable passkey: adding one now needs AAL2 proof. It matches
	// sdk.ErrConflict.
	ErrBootstrapClosed = fmt.Errorf("passkey: the account now holds an AAL2-capable passkey; enrolling another needs AAL2: %w", sdk.ErrConflict)
)

// Passkey is one registered WebAuthn credential. CredentialID, PublicKey (a
// COSE_Key), AAGUID and UserHandle are opaque bytes: none of them is ever logged,
// audited or returned by a transport. UserHandle is the WebAuthn user.id the
// authenticator stores beside the credential; every passkey of one user carries
// the same handle, minted at random when the first is registered. UserVerified,
// BackupEligible and BackupState are the registration ceremony's flags;
// SignCount and BackupState then advance with each verified use. LastUsedAt is
// nil until the first assertion.
type Passkey struct {
	ID             string
	UserID         string
	Name           string
	CredentialID   []byte
	PublicKey      []byte
	AAGUID         []byte
	UserHandle     []byte
	SignCount      uint32
	Transports     []string
	UserVerified   bool
	BackupEligible bool
	BackupState    bool
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// AAL2Capable reports whether the passkey can produce AAL2: its registration
// ceremony reported user verification. It is the one predicate every reader
// uses — the enrollment gate, the minted assurance (an assertion is AAL2 only
// when it is user-verified AND its passkey is AAL2-capable), the credential
// policy's method set and the step-up page.
func (p Passkey) AAL2Capable() bool { return p.UserVerified }

// NormalizeName trims name and reports whether it is a valid display name: 1 to
// MaxNameRunes runes of valid UTF-8 with no control character.
func NormalizeName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameRunes {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}

// DefaultName is the name a registration falls back to when the caller supplied
// none or an invalid one: "Passkey N", N one past the user's current count.
func DefaultName(existing int) string {
	return "Passkey " + strconv.Itoa(existing+1)
}
