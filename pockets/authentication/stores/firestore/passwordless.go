package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
)

var _ passwordless.Repository = (*passwordlessStore)(nil)

// passwordlessStore fills passwordless.Repository: the ONE-transaction magic-link
// redemption that consumes the token, decides login / adopt / provision,
// performs the identity mutation and every revocation the adoption branch owes,
// and inserts the session. Its rollback policy is the strictest in the pocket —
// a stable bad outcome is passwordless.ErrRedemption with NOTHING written, and
// an infrastructure error rolls back the token consumption itself, so a
// transient failure leaves the link redeemable. Bodies land in N4d.
type passwordlessStore struct {
	db *firestoredb.DB
}

func newPasswordlessStore(db *firestoredb.DB) *passwordlessStore {
	return &passwordlessStore{db: db}
}

// Redeem executes the atomic redemption.
func (s *passwordlessStore) Redeem(ctx context.Context, in passwordless.RedeemInput) (passwordless.RedeemResult, error) {
	if err := refuseAmbient(ctx); err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{}, errNotImplemented
}
