package authentication

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// The helpers below are shared by Service and ExternalAuthenticator, which hold
// the same collaborators but neither owns the other.

// newSecurityEventRecorder builds the recorder a Service or ExternalAuthenticator
// owns when none was supplied: over events, attributing each row to the client
// through the request's client-info carrier (design §5.1 WI4).
func newSecurityEventRecorder(events securityevent.SecurityEventRepository, ids sdk.IDGenerator, now func() time.Time, logger *slog.Logger, observer securityevent.WriteObserver, opts ...securityevent.RecorderOption) *securityevent.Recorder {
	return securityevent.NewRecorder(events, ids, now, logger, append([]securityevent.RecorderOption{
		securityevent.WithClientInfoReader(ClientInfoFromContext),
		securityevent.WithWriteObserver(observer)}, opts...)...)
}

// projectAddresses returns a user's active VERIFIED identifiers as identity
// Addresses (design §7), primary-first then oldest-first for a stable projection,
// excluding replaced and unverified rows. A nil Identifiers repository (identity
// subsystem off) projects nothing; a repository error is propagated so the
// Resolver fails closed rather than fabricating a partial address set.
func projectAddresses(ctx context.Context, identifiers identifier.IdentifierRepository, userID string) ([]sdk.IdentityAddress, error) {
	return projectAddressesWhere(ctx, identifiers, userID, nil)
}

// claimAddresses is projectAddresses narrowed to the identifiers holding the
// authentication claim (login- or recovery-enabled): the values unique across
// users. Invitation resolution, sync and the accept-time match read only these.
func claimAddresses(ctx context.Context, identifiers identifier.IdentifierRepository, userID string) ([]sdk.IdentityAddress, error) {
	return projectAddressesWhere(ctx, identifiers, userID, holdsClaim)
}

// holdsClaim reports whether an identifier holds the authentication claim.
func holdsClaim(it identifier.Identifier) bool { return it.LoginEnabled || it.RecoveryEnabled }

// projectAddressesWhere is projectAddresses over the identifiers keep admits;
// nil keeps every active verified identifier.
func projectAddressesWhere(ctx context.Context, identifiers identifier.IdentifierRepository, userID string, keep func(identifier.Identifier) bool) ([]sdk.IdentityAddress, error) {
	if identifiers == nil {
		return nil, nil
	}
	idents, err := identifiers.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	verified := make([]identifier.Identifier, 0, len(idents))
	for _, it := range idents {
		if it.Active() && it.Verified() && (keep == nil || keep(it)) {
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
	addresses := make([]sdk.IdentityAddress, 0, len(verified))
	for _, it := range verified {
		addresses = append(addresses, sdk.IdentityAddress{Kind: string(it.Kind), Value: it.NormalizedValue})
	}
	return addresses, nil
}

// resolvePendingInvitations processes invitations after verified ownership of
// (kind, value). Resolver errors are logged without failing authentication;
// durable acceptance claims let the invitation service resume an interrupted
// grant.
func resolvePendingInvitations(ctx context.Context, invitations invitationResolver, logger *slog.Logger, kind, value, userID string) {
	if invitations == nil {
		return
	}
	if _, err := invitations.ResolveInvitations(ctx, kind, value, PrincipalUser, userID); err != nil {
		logger.Warn("resolve invitations failed", "error_kind", ErrorKind(err))
	}
}
