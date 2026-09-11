package authmem

import (
	"context"
	"time"

	challenge "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	oauthaccount "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	user "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

func (r *data) revokeCredentialStateLocked(userID string) {
	for id, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, id)
		}
	}
	for id, g := range r.authGrants {
		if g.UserID == userID {
			delete(r.authGrants, id)
		}
	}
	for id, c := range r.challenges {
		if c.UserID == userID && c.Purpose == challenge.PurposePasswordReset {
			delete(r.challenges, id)
		}
	}
}
func (r *data) advanceCredentialRevisionLocked(userID string, now time.Time) int64 {
	u := r.users[userID]
	u.AuthRevision++
	u.UpdatedAt = now.UTC()
	r.users[userID] = u

	return u.AuthRevision
}
func (r *data) changePasswordLocked(userID string, change user.PasswordChange, conditional bool) (int64, error) {
	u, ok := r.users[userID]
	if !ok {
		return 0, sdk.ErrNotFound
	}
	if !u.Active() {
		return 0, session.ErrUserNotActive
	}
	if conditional && (u.AuthRevision != change.ExpectedAuthRevision || r.passwords[userID] != change.ExpectedHash) {
		return 0, sdk.ErrConflict
	}
	r.passwords[userID] = change.NewHash
	revision := r.advanceCredentialRevisionLocked(userID, change.Now)
	r.revokeCredentialStateLocked(userID)
	return revision, nil
}
func (r passwordRepo) Change(ctx context.Context, userID string, change user.PasswordChange) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changePasswordLocked(userID, change, true)
}
func (r oauthAccountRepo) Link(ctx context.Context, a oauthaccount.OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (oauthaccount.OAuthAccount, int64, error) {
	if err := ctx.Err(); err != nil {
		return oauthaccount.OAuthAccount{}, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[a.UserID]
	if !ok {
		return oauthaccount.OAuthAccount{}, 0, sdk.ErrNotFound
	}
	if !u.Active() {
		return oauthaccount.OAuthAccount{}, 0, session.ErrUserNotActive
	}
	if u.AuthRevision != expectedAuthRevision {
		return oauthaccount.OAuthAccount{}, 0, sdk.ErrConflict
	}
	for _, ex := range r.oauthAccounts {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, 0, sdk.ErrAlreadyExists
		}
	}
	if adoptIdentifierID != "" {
		ident, ok := r.identifiers[adoptIdentifierID]
		if !ok || ident.UserID != a.UserID || !ident.Active() || (!ident.LoginEnabled && !ident.RecoveryEnabled) || string(ident.Kind) != "email" {
			return oauthaccount.OAuthAccount{}, 0, sdk.ErrConflict
		}
		ident.VerifiedAt = now
		ident.UpdatedAt = now
		r.identifiers[ident.ID] = ident
		delete(r.passwords, a.UserID)
		r.revokeCredentialStateLocked(a.UserID)
	}
	r.oauthAccounts = append(r.oauthAccounts, a)
	revision := r.advanceCredentialRevisionLocked(a.UserID, now)
	return a, revision, nil
}
