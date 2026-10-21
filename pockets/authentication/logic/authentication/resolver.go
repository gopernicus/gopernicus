package authentication

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// resolverAssertion is the compile-time proof that the auth service satisfies the
// generic sdk Resolver port. The public auth.Service promotes it.
var _ sdk.IdentityResolver = (*Service)(nil)

// Resolve turns a Principal into its display and contact Info (the sdk
// Resolver port). It is PRINCIPAL-EXACT: the record is looked up by the
// principal's stored ID and only that record's stored display name is
// projected — a blank display name is projected blank, never synthesized from
// an email local part, another identifier, or the principal ID. Matching an
// identifier value belongs to a pre-principal admission/linking flow
// (invitation acceptance, OAuth link), never here. It fails CLOSED per the port
// contract: an unknown principal type, a missing record, or an unwired backing
// subsystem returns an error satisfying sdk.ErrNotFound (checked with
// errors.Is), and it never fabricates an Info or panics.
//
//   - user → the User's stored DisplayName, exactly as stored (blank stays
//     blank); Addresses carries every active VERIFIED identifier (design §7),
//     primary-first, so a Resolver consumer routes to any proven address, not
//     just the legacy email column. A nil Users repository or a missing row →
//     the not-found class.
//   - service_account → the ServiceAccount's stored Name, exactly as stored. A
//     nil ServiceAccounts repository (the machine subsystem is off) or a
//     missing row → the not-found class.
//   - any other type → the not-found class.
func (s *Service) Resolve(ctx context.Context, p sdk.Principal) (sdk.IdentityInfo, error) {
	switch p.Type {
	case sdk.PrincipalTypeUser:
		if s.users == nil {
			return sdk.IdentityInfo{}, resolveNotFound(p)
		}
		u, err := s.users.Get(ctx, p.ID)
		if err != nil {
			return sdk.IdentityInfo{}, err
		}
		addresses, err := s.projectAddresses(ctx, p.ID)
		if err != nil {
			return sdk.IdentityInfo{}, err
		}
		return sdk.IdentityInfo{
			Principal:   p,
			DisplayName: u.DisplayName,
			Addresses:   addresses,
		}, nil
	case sdk.PrincipalTypeServiceAccount:
		if s.serviceAccounts == nil {
			return sdk.IdentityInfo{}, resolveNotFound(p)
		}
		sa, err := s.serviceAccounts.Get(ctx, p.ID)
		if err != nil {
			return sdk.IdentityInfo{}, err
		}
		return sdk.IdentityInfo{Principal: p, DisplayName: sa.Name}, nil
	default:
		return sdk.IdentityInfo{}, resolveNotFound(p)
	}
}

// resolveNotFound is the fail-closed error for a principal the Resolver cannot
// resolve — an unknown type or an off subsystem. It wraps sdk.ErrNotFound.
func resolveNotFound(p sdk.Principal) error {
	return fmt.Errorf("cannot resolve principal (type=%q): %w", p.Type, sdk.ErrNotFound)
}

// projectAddresses returns a user's active verified identifiers as identity
// Addresses; see the package-level projectAddresses.
func (s *Service) projectAddresses(ctx context.Context, userID string) ([]sdk.IdentityAddress, error) {
	return projectAddresses(ctx, s.identifiers, userID)
}
