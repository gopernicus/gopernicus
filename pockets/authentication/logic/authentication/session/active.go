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

// ActiveUserRepository admits sessions under the same user lock used by status
// and credential mutations. It is required for every service session mint.
// The caller captures expectedAuthRevision BEFORE reading the credential it
// proves. A changed revision returns sdk.ErrConflict without creating a session;
// an inactive user returns ErrUserNotActive, and an unknown user ErrNotFound.
// A concurrent mutation either follows admission and revokes the session, or
// commits first and makes admission fail. A service-level recheck is insufficient.
type ActiveUserRepository interface {
	CreateForActiveUser(ctx context.Context, s Session, expectedAuthRevision int64) (Session, error)
}
