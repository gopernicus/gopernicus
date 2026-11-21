package session

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var OrderFields = map[string]list.OrderField{
	"created_at": {Column: "created_at"},
}

var DefaultOrder = list.NewOrder("created_at", list.DESC)

// ManagementRepository supplies owner-constrained inventory and revocation.
// It is optional for existing hosts and shares the user/session transaction
// authority. Authorization of the acting person belongs at the inbound boundary.
type ManagementRepository interface {
	// ListByUser returns live sessions for this owner only, using the standard
	// list request/page contract and created_at descending, id descending order.
	ListByUser(ctx context.Context, userID string, req list.Request) (list.Page[Session], error)
	// DeleteForUser atomically constrains deletion to the owner, and removes
	// dependent step-up grants. Unknown or foreign IDs return sdk.ErrNotFound.
	DeleteForUser(ctx context.Context, id, userID string) error
	// RevokeAllForUser locks the user, increments auth_revision and deletes all
	// their sessions/step-up grants atomically. Code redemption uses that same
	// lock/revision fence, so outstanding approvals cannot resurrect sessions.
	// Single-session revocation must never call this operation, and the
	// fleet-wide revocation is GlobalRevoker, not a loop over this.
	RevokeAllForUser(ctx context.Context, userID string, now time.Time) error
}

// DelegatedRevoker is the OPTIONAL capability behind
// RevokeDelegatedUserSessions, implemented by the ManagementRepository a
// bundled store returns. It ends a user's app connections and keeps their
// first-party sign-ins.
type DelegatedRevoker interface {
	// RevokeDelegatedForUser locks the user, deletes every delegated session
	// they own with its step-up grants, and deletes their unredeemed OAuth
	// authorization codes, in one transaction. Code redemption takes the same
	// user lock before it reads the code, so an approval outstanding at the
	// revocation can no longer be redeemed. First-party sessions and
	// auth_revision are untouched. It returns the delegated sessions deleted;
	// an unknown user returns sdk.ErrNotFound.
	RevokeDelegatedForUser(ctx context.Context, userID string) (int, error)
}
