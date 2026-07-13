package authsvc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
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
//   - user → the User's DisplayName, or the primary email local part when the
//     display name is empty; Addresses carries every active VERIFIED identifier
//     (design §7), primary-first, so a Resolver consumer routes to any proven
//     address, not just the legacy email column. A nil Users repository or a
//     missing row → the not-found class.
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
		addresses, err := s.projectAddresses(ctx, p.ID)
		if err != nil {
			return identity.Info{}, err
		}
		display := u.DisplayName
		if display == "" {
			display = firstEmailLocalPart(addresses)
		}
		return identity.Info{
			Principal:   p,
			DisplayName: display,
			Addresses:   addresses,
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

// projectAddresses returns a user's active VERIFIED identifiers as identity
// Addresses (design §7), primary-first then oldest-first for a stable projection,
// excluding replaced and unverified rows. A nil Identifiers repository (identity
// subsystem off) projects nothing; a repository error is propagated so the
// Resolver fails closed rather than fabricating a partial address set.
func (s *Service) projectAddresses(ctx context.Context, userID string) ([]identity.Address, error) {
	if s.identifiers == nil {
		return nil, nil
	}
	idents, err := s.identifiers.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	verified := make([]identifier.Identifier, 0, len(idents))
	for _, it := range idents {
		if it.Active() && it.Verified() {
			verified = append(verified, it)
		}
	}
	sort.SliceStable(verified, func(i, j int) bool {
		a, b := verified[i], verified[j]
		if a.IsPrimary != b.IsPrimary {
			return a.IsPrimary // primary first
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt) // then oldest first
		}
		return a.ID < b.ID // stable tie-break
	})
	if len(verified) == 0 {
		return nil, nil
	}
	addresses := make([]identity.Address, 0, len(verified))
	for _, it := range verified {
		addresses = append(addresses, identity.Address{Kind: string(it.Kind), Value: it.NormalizedValue})
	}
	return addresses, nil
}

// firstEmailLocalPart returns the local part of the first email address in a
// primary-first projection — the display fallback for a user with no name.
func firstEmailLocalPart(addresses []identity.Address) string {
	for _, a := range addresses {
		if a.Kind == identity.KindEmail {
			return emailLocalPart(a.Value)
		}
	}
	return ""
}

// emailLocalPart returns the part of email before the first '@', or the whole
// string when there is none.
func emailLocalPart(email string) string {
	if i := strings.IndexByte(email, '@'); i >= 0 {
		return email[:i]
	}
	return email
}
