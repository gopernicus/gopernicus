package session

import (
	"context"
	"time"
)

// ActivityRepository is the OPTIONAL idle-timeout capability (SOC 2 S4d part
// 2), asserted on Repositories.Sessions only when SessionsConfig.IdleTimeout
// is set; SessionRepository is unchanged. It is backed by one nullable column,
// sessions.last_rotated_at (migration 0029), that only RotateAt writes:
// "activity" is a first-party refresh rotation, never a read.
//
// Every method runs on the store's pool path, never on a transaction the
// caller's context carries: a refresh reached inside a host transaction must
// not CAS-miss on a connection that cannot see the row and burn the session as
// reuse.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - GetByRefreshHashWithActivity is GetByRefreshHash's contract (one
//     current-or-previous scan, the row verbatim, an empty hash never matches,
//     no match → sdk.ErrNotFound) plus the row's stamp, read in the SAME
//     statement. A NULL stamp is Activity{} (Stamped false).
//   - RotateAt is Rotate's compare-and-swap plus last_rotated_at ← now in ONE
//     statement; zero rows → ErrRotationConflict. Rotate and ConsumeGrace never
//     write the stamp.
//   - DeleteIfIdle deletes the row ONLY while its stamp still equals observed
//     (NULL matching NULL) and its rotation_count still equals
//     observedRotationCount. It reports whether THIS call deleted it: false
//     means the row moved on or is gone, never an error.
//   - ProbeSchema → sdk.ErrNotFound when migration 0029 is absent.
type ActivityRepository interface {
	// GetByRefreshHashWithActivity returns the row whose current or previous
	// refresh-token hash equals hash, which slot matched, and its stamp.
	GetByRefreshHashWithActivity(ctx context.Context, hash string) (Session, RefreshMatch, Activity, error)
	// RotateAt compare-and-swaps the live refresh token like Rotate and stamps
	// last_rotated_at with now; zero rows → ErrRotationConflict.
	RotateAt(ctx context.Context, id, expectedCurrentHash, newHash string, now time.Time) error
	// DeleteIfIdle deletes the row while its stamp and rotation count are
	// still the observed ones, reporting whether it did.
	DeleteIfIdle(ctx context.Context, id string, observed Activity, observedRotationCount int) (bool, error)
	// ProbeSchema reports sdk.ErrNotFound when the activity column is absent.
	ProbeSchema(ctx context.Context) error
}

// Activity is a session row's last refresh-rotation stamp. Stamped is false
// when the column is NULL: a row created, or last rotated, while idle expiry
// was not wired.
type Activity struct {
	LastRotatedAt time.Time
	Stamped       bool
}
