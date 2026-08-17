package session

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ErrUserNotActive is returned by ActiveUserRepository.CreateForActiveUser when
// the owning user is not in the active lifecycle posture at the moment the
// session would commit. It wraps sdk.ErrForbidden. Checked with errors.Is.
//
// It is deliberately distinct from sdk.ErrNotFound (unknown user) so a service
// can tell "no such subject" from "this subject may not authenticate" for
// operator diagnostics — while PUBLIC credential endpoints collapse both into
// one generic failure, because the difference is exactly the enumeration signal
// an attacker wants.
var ErrUserNotActive = fmt.Errorf("session: owning user is not active: %w", sdk.ErrForbidden)

// ActiveUserRepository is the OPTIONAL fenced session-minting capability
// (coordination-hub-auth-upstream CHAU-1.1). It exists because a status check and
// a session insert performed as two service-level steps is a race: a deactivation
// committing between them leaves a live session on a deactivated user, and no
// amount of ordering in the service can close it.
//
// CreateForActiveUser inserts the proposed session ONLY while the owning user is
// proven active under the SAME database serialization boundary as the insert. An
// implementation must lock or conditionally read the user row inside the insert's
// transaction — a SELECT ... FOR UPDATE, a conditional INSERT ... SELECT, or the
// dialect's write-serialization equivalent. Exactly one of these outcomes must
// win a concurrent deactivate/mint:
//
//   - the mint commits first, and the deactivation that follows deletes that
//     session along with the rest; or
//   - the deactivation commits first, and the mint observes the deactivated
//     status and refuses with ErrUserNotActive.
//
// A service-level Users.Get followed by an ordinary Sessions.Create is NOT an
// acceptable implementation of this port.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - unknown user → sdk.ErrNotFound;
//   - deactivated user → ErrUserNotActive, with NO session row written;
//   - active user → the same session shape an ordinary Sessions.Create returns,
//     readable through the ordinary Sessions.Get; and
//   - a colliding refresh_token_hash → sdk.ErrAlreadyExists, exactly as
//     Sessions.Create reports it.
type ActiveUserRepository interface {
	// CreateForActiveUser persists s only while s.UserID names an active user,
	// atomically with respect to a concurrent status transition.
	CreateForActiveUser(ctx context.Context, s Session) (Session, error)
}
