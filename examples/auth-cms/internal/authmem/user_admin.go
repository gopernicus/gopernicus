package authmem

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// The account-lifecycle ports (coordination-hub-auth-upstream CHAU-1.1): the
// operator directory plus the atomic status transition, and the fenced session
// mint that closes the deactivate-versus-login race.
//
// This host implements them for the same reason it implements every other port:
// to prove the feature runs against a "bring your own store" adapter with no
// driver in the module graph, and — because these two are the ports whose whole
// value is atomicity — to give the example host's browser proof something real to
// drive. The storetest UserDirectory / UserLifecycle / ActiveSessionMint groups
// run against this implementation rather than skipping.
//
// Both types do their work under the SHARED data mutex, which is this store's
// stand-in for a SQL transaction: the status write, the revision bump, and the
// revocation are one critical section, and a mint cannot interleave with them.

// --- user.AdminRepository ---

type userAdminRepo struct{ *data }

// summaryLocked projects a user plus its ACTIVE PRIMARY email identifier.
// Callers hold r.mu. A SQL store does this with one LEFT JOIN; the point is the
// same — never one identifier read per user.
func (r userAdminRepo) summaryLocked(u user.User) user.Summary {
	s := user.Summary{
		ID:              u.ID,
		DisplayName:     u.DisplayName,
		Status:          user.NormalizeStatus(u.Status),
		StatusChangedAt: u.StatusChangedAt,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}
	for _, it := range r.identifiers {
		if it.UserID != u.ID || it.Kind != identifier.KindEmail || !it.IsPrimary || !it.Active() {
			continue
		}
		s.PrimaryEmail = it.NormalizedValue
		s.EmailVerified = it.Verified()
		break // at most one active primary per (user, kind)
	}
	return s
}

func (r userAdminRepo) List(_ context.Context, req crud.ListRequest) (crud.Page[user.Summary], error) {
	r.mu.RLock()
	all := make([]user.Summary, 0, len(r.users))
	for _, u := range r.users {
		all = append(all, r.summaryLocked(u))
	}
	r.mu.RUnlock()
	return page(all, req, func(s user.Summary) (time.Time, string) {
		return s.CreatedAt, s.ID
	})
}

func (r userAdminRepo) GetSummary(_ context.Context, id string) (user.Summary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return user.Summary{}, sdk.ErrNotFound
	}
	return r.summaryLocked(u), nil
}

// SetStatus is the atomic transition: status, auth_revision, sessions, and grants
// all move together inside one critical section, or none of them does. Replaying
// the current status is a no-op — no revision bump, no revocation.
func (r userAdminRepo) SetStatus(_ context.Context, id string, status user.Status, now time.Time) (user.StatusChange, error) {
	if !status.Valid() {
		return user.StatusChange{}, fmt.Errorf("authmem: %q: %w", status, user.ErrInvalidStatus)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.users[id]
	if !ok {
		return user.StatusChange{}, sdk.ErrNotFound
	}
	current := user.NormalizeStatus(u.Status)
	if current == status {
		return user.StatusChange{Status: current, Changed: false, ChangedAt: u.StatusChangedAt}, nil
	}

	now = now.UTC()
	u.Status = status
	u.StatusChangedAt = now
	u.UpdatedAt = now
	u.AuthRevision++
	r.users[id] = u

	revoked := 0
	for sid, s := range r.sessions {
		if s.UserID != id {
			continue
		}
		delete(r.sessions, sid)
		revoked++
		for gid, g := range r.authGrants {
			if g.SessionID == sid {
				delete(r.authGrants, gid)
			}
		}
	}
	// A grant whose session was already gone must not outlive the transition.
	for gid, g := range r.authGrants {
		if g.UserID == id {
			delete(r.authGrants, gid)
		}
	}

	return user.StatusChange{Status: status, Changed: true, ChangedAt: now, RevokedSessions: revoked}, nil
}

// --- session.ActiveUserRepository ---

type activeSessionRepo struct{ *data }

// CreateForActiveUser reads the status and inserts the session under ONE lock, so
// a concurrent SetStatus either commits first (and this refuses) or commits after
// (and revokes the row this wrote). It reproduces the ordinary Create's
// uniqueness contract so the two paths cannot diverge.
func (r activeSessionRepo) CreateForActiveUser(_ context.Context, s session.Session) (session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.users[s.UserID]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return session.Session{}, session.ErrUserNotActive
	}
	for _, ex := range r.sessions {
		if ex.RefreshTokenHash == s.RefreshTokenHash {
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	r.sessions[s.ID] = s
	return s, nil
}
