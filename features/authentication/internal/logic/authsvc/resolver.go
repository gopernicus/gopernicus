package authsvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// resolverAssertion is the compile-time proof that the auth service satisfies the
// generic sdk/foundation/identity Resolver port. The public auth.Service promotes it.
var _ identity.Resolver = (*Service)(nil)

// Resolve turns a Principal into its display and contact Info (the sdk/foundation/identity
// Resolver port). It fails CLOSED per the port contract: an unknown principal
// type, a missing record, or an unwired backing subsystem returns an error
// satisfying sdk.ErrNotFound (checked with errors.Is), and it never fabricates
// an Info or panics.
//
//   - user → the User's DisplayName, or the email local part when the display
//     name is empty; Addresses carries the user's email as an identity.KindEmail
//     Address. A nil Users repository or a missing row → the not-found class.
//   - service_account → the ServiceAccount's Name. A nil ServiceAccounts
//     repository (the machine subsystem is off) or a missing row → the not-found
//     class.
//   - any other type → the not-found class.
func (s *Service) Resolve(ctx context.Context, p identity.Principal) (identity.Info, error) {
	switch p.Type {
	case identity.User:
		if s.users == nil {
			return identity.Info{}, resolveNotFound(p)
		}
		u, err := s.users.Get(ctx, p.ID)
		if err != nil {
			return identity.Info{}, err
		}
		display := u.DisplayName
		if display == "" {
			display = emailLocalPart(u.Email)
		}
		return identity.Info{
			Principal:   p,
			DisplayName: display,
			Addresses:   []identity.Address{{Kind: identity.KindEmail, Value: u.Email}},
		}, nil
	case identity.ServiceAccount:
		if s.serviceAccounts == nil {
			return identity.Info{}, resolveNotFound(p)
		}
		sa, err := s.serviceAccounts.Get(ctx, p.ID)
		if err != nil {
			return identity.Info{}, err
		}
		return identity.Info{Principal: p, DisplayName: sa.Name}, nil
	default:
		return identity.Info{}, resolveNotFound(p)
	}
}

// resolveNotFound is the fail-closed error for a principal the Resolver cannot
// resolve — an unknown type or an off subsystem. It wraps sdk.ErrNotFound.
func resolveNotFound(p identity.Principal) error {
	return fmt.Errorf("cannot resolve principal (type=%q): %w", p.Type, sdk.ErrNotFound)
}

// emailLocalPart returns the part of email before the first '@', or the whole
// string when there is none — the display fallback for a user with no name.
func emailLocalPart(email string) string {
	if i := strings.IndexByte(email, '@'); i >= 0 {
		return email[:i]
	}
	return email
}
