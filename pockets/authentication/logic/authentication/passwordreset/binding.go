package passwordreset

import (
	"encoding/json"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// BindingVersion identifies the reset proof stored in a challenge's Context.
const BindingVersion = 1

// Binding records the credential revision and verified recovery identifier at
// issuance. A later credential change invalidates the proof, even if the old
// address or a session remains available. Legacy unbound links must be restarted.
type Binding struct {
	Version      int    `json:"version"`
	AuthRevision int64  `json:"auth_revision"`
	IdentifierID string `json:"identifier_id"`
}

// ParseBinding rejects malformed, legacy and unknown-version reset proofs with
// the same generic failure as an expired or missing token.
func ParseBinding(raw json.RawMessage) (Binding, error) {
	var b Binding
	if err := json.Unmarshal(raw, &b); err != nil || b.Version != BindingVersion || b.IdentifierID == "" || b.AuthRevision < 0 {
		return Binding{}, sdk.ErrNotFound
	}
	return b, nil
}

// Matches checks the current credential state while the reset transaction holds
// the active user's credential lock. The identifier must still be a verified,
// active recovery address belonging to that user.
func (b Binding) Matches(userID string, revision int64, ident identifier.Identifier) bool {
	return b.Version == BindingVersion && b.AuthRevision == revision &&
		b.IdentifierID != "" && b.IdentifierID == ident.ID && ident.UserID == userID &&
		ident.Active() && ident.Verified() && ident.RecoveryEnabled
}
