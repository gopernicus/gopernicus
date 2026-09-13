package credential

import (
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// Validate checks the identifiers read for this retirement. Both must belong to
// userID and be active. A nominated replacement must be distinct and of the same
// kind, and only a primary identifier may nominate its replacement. Promotion
// changes no use or verification flags; contact-only identifiers remain eligible.
// Stores must call Validate inside the transaction protecting these rows.
func (m RetireIdentifier) Validate(userID string, target, replacement identifier.Identifier) error {
	if userID == "" || m.IdentifierID == "" || target.ID != m.IdentifierID || target.UserID != userID || !target.Active() {
		return sdk.ErrInvalidInput
	}
	if m.ReplacementPrimaryID == "" {
		return nil
	}
	if !target.IsPrimary || m.ReplacementPrimaryID == m.IdentifierID || replacement.ID != m.ReplacementPrimaryID || replacement.UserID != userID || !replacement.Active() || replacement.Kind != target.Kind {
		return sdk.ErrInvalidInput
	}
	return nil
}

// Validate checks ownership, activity and the verification invariant before an
// identifier's uses change. Stores must repeat this check within their mutation
// transaction even when the caller already validated its earlier read.
func (m ChangeIdentifierUses) Validate(userID string, target identifier.Identifier) error {
	if userID == "" || m.IdentifierID == "" || target.ID != m.IdentifierID || target.UserID != userID || !target.Active() {
		return sdk.ErrInvalidInput
	}
	if (m.Uses.Login || m.Uses.Recovery) && !target.Verified() {
		return identifier.ErrVerificationRequired
	}
	return nil
}
