package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
)

var _ passwordreset.Repository = (*passwordResetStore)(nil)

// passwordResetStore fills passwordreset.Repository: one transaction that
// consumes the live reset challenge with its digest claim, sets the typed
// password row, and revokes every session (with its refresh claim), every
// outstanding grant, and the named challenge purposes for the resolved user. A
// non-live challenge — unknown, consumed, or expired — is sdk.ErrNotFound with
// nothing applied. Bodies land in N4c.
type passwordResetStore struct {
	db *firestoredb.DB
}

func newPasswordResetStore(db *firestoredb.DB) *passwordResetStore {
	return &passwordResetStore{db: db}
}

// Redeem atomically consumes the reset challenge and applies the composition.
func (s *passwordResetStore) Redeem(ctx context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	if err := refuseAmbient(ctx); err != nil {
		return passwordreset.RedeemResult{}, err
	}
	return passwordreset.RedeemResult{}, errNotImplemented
}
