package authsvc

import (
	"context"
	"errors"
	"sort"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
)

// ErrIdentityUnavailable is returned by CurrentUserView when the user rail is not
// wired (nil Users). The hydration read has no aggregate to project, so it fails
// CLOSED rather than fabricating a profile.
//
// It deliberately wraps NO sdk sentinel: a live-session caller hitting an unwired
// rail is a HOST WIRING BUG, not a missing resource, and must not be reported as a
// resource-shaped 404 (which would tell a signed-in user their own account is
// gone). sdk has no internal sentinel — an error wrapping none of them is exactly
// what web.ErrFromDomain maps to 500 (sdk.IsExpected reports false), so the
// unwired rail surfaces as the server error it is.
var ErrIdentityUnavailable = errors.New("identity subsystem not wired")

// CurrentUserView is the hydrated current-user projection the live-session-gated
// GET /auth/me returns. It carries the user aggregate plus the account's email
// identity exactly as the login/register responses report it: the UNMASKED active
// email — verified when the user has one, otherwise the active unverified primary
// of an allowed-but-unverified account — and the matching proof flag. The masked
// inventory read is MethodsView; this view is deliberately narrow and never
// projects credentials, sessions, or non-email identifiers.
type CurrentUserView struct {
	User          user.User
	Email         string
	EmailVerified bool
}

// CurrentUserView loads userID's aggregate plus its active email identity — the
// session-hydration read behind GET /auth/me. It reproduces login's email field
// exactly (ActiveVerifiedIdentifier alone cannot: an allowed-but-unverified
// account has no verified identifier, and login falls back to the submitted
// address, which a hydration request never carries):
//
//   - a verified active email wins, primary first then oldest — the same
//     ordering ActiveVerifiedIdentifier applies, so /auth/me and login agree;
//   - otherwise the active unverified email, primary first then oldest, with
//     EmailVerified false;
//   - with no active email identifier (or an unwired identifier rail) the email
//     is empty rather than invented.
//
// A missing user or a repository error propagates, so the read fails CLOSED.
func (s *Service) CurrentUserView(ctx context.Context, userID string) (CurrentUserView, error) {
	if s.users == nil {
		return CurrentUserView{}, ErrIdentityUnavailable
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return CurrentUserView{}, err
	}
	email, verified, err := s.activeEmailIdentity(ctx, userID)
	if err != nil {
		return CurrentUserView{}, err
	}
	return CurrentUserView{User: u, Email: email, EmailVerified: verified}, nil
}

// activeEmailIdentity returns the normalized value and proof state of userID's
// active email identifier, preferring a verified one, then the primary, then the
// oldest. No active email identifier (or a nil Identifiers repository) yields an
// empty value and false; a repository error propagates.
func (s *Service) activeEmailIdentity(ctx context.Context, userID string) (string, bool, error) {
	if s.identifiers == nil {
		return "", false, nil
	}
	idents, err := s.identifiers.ListByUser(ctx, userID)
	if err != nil {
		return "", false, err
	}
	emails := make([]identifier.Identifier, 0, len(idents))
	for _, it := range idents {
		if it.Active() && it.Kind == identifier.KindEmail {
			emails = append(emails, it)
		}
	}
	if len(emails) == 0 {
		return "", false, nil
	}
	sort.SliceStable(emails, func(i, j int) bool {
		a, b := emails[i], emails[j]
		if a.Verified() != b.Verified() {
			return a.Verified() // proven identity first
		}
		if a.IsPrimary != b.IsPrimary {
			return a.IsPrimary // then primary
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt) // then oldest
		}
		return a.ID < b.ID // stable tie-break
	})
	return emails[0].NormalizedValue, emails[0].Verified(), nil
}
