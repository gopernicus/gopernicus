package session

import (
	"context"
	"time"
)

// GlobalRevoker ends every session in the fleet (SOC 2 S4d): every ACTIVE
// user's auth_revision is bumped and every session, step-up grant and
// password_reset challenge they hold is deleted. It is OPTIONAL; the service
// refuses the global revocation while it is nil.
//
// The work is chunked so the incident button survives the incident it is for:
// RevokeAllBatch pages users by id, and each page is ONE transaction taken in
// the per-user lock order (the users UPDATE first, then the session, grant and
// challenge DELETEs). A failed batch leaves the earlier batches committed; the
// operation is re-runnable, and a re-run bumps again, which is harmless.
type GlobalRevoker interface {
	// RevokeAllBatch revokes the next page of at most limit users WHERE
	// status = 'active' AND id > afterUserID ORDER BY id, in one transaction.
	// LastUserID is the cursor for the next call, in the store's own id order
	// (byte order: COLLATE "C" on PostgreSQL); Done reports that the page was
	// the last (fewer than limit users). A limit <= 0 is sdk.ErrInvalidInput.
	RevokeAllBatch(ctx context.Context, afterUserID string, limit int, now time.Time) (GlobalRevocationBatch, error)
	// SweepOrphans deletes the sessions and step-up grants whose user is not
	// active — a deactivated or erased user's leftovers. It is the final pass,
	// after the last batch.
	SweepOrphans(ctx context.Context, now time.Time) (sessions, grants int, err error)
}

// GlobalRevocationBatch reports one RevokeAllBatch page: the users bumped,
// the sessions and grants deleted, the last user id paged, and whether the
// page was the last.
type GlobalRevocationBatch struct {
	Users      int
	Sessions   int
	Grants     int
	LastUserID string
	Done       bool
}
