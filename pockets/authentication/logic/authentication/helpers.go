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

// recordSecurityEvent appends one audit row synchronously (design §5.1). A nil
// repository is a no-op; a write failure is logged at WARN with coarse fields
// only and never fails the auth flow.
func recordSecurityEvent(ctx context.Context, events securityevent.SecurityEventRepository, ids sdk.IDGenerator, now time.Time, logger *slog.Logger, in securityEventInput) {
	if events == nil {
		return
	}
	info := clientInfoFromContext(ctx)
	evt := securityevent.New(ids, in.Type, in.Status, now)
	evt.UserID = in.UserID
	evt.Actor = in.Actor
	evt.Details = in.Details
	evt.IPAddress = info.ip
	evt.UserAgent = info.ua
	if _, err := events.Create(ctx, evt); err != nil {
		logger.Warn("security event write failed",
			"event_type", in.Type,
			"status", in.Status,
			"error_kind", ErrorKind(err),
		)
	}
}

// projectAddresses returns a user's active VERIFIED identifiers as identity
// Addresses (design §7), primary-first then oldest-first for a stable projection,
// excluding replaced and unverified rows. A nil Identifiers repository (identity
// subsystem off) projects nothing; a repository error is propagated so the
// Resolver fails closed rather than fabricating a partial address set.
func projectAddresses(ctx context.Context, identifiers identifier.IdentifierRepository, userID string) ([]sdk.IdentityAddress, error) {
	if identifiers == nil {
		return nil, nil
	}
	idents, err := identifiers.ListByUser(ctx, userID)
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
	addresses := make([]sdk.IdentityAddress, 0, len(verified))
	for _, it := range verified {
		addresses = append(addresses, sdk.IdentityAddress{Kind: string(it.Kind), Value: it.NormalizedValue})
	}
	return addresses, nil
}

// resolvePendingInvitations processes invitations after verified email ownership.
// Resolver errors are logged without failing authentication; durable acceptance
// claims let the invitation service resume an interrupted grant.
func resolvePendingInvitations(ctx context.Context, invitations invitationResolver, logger *slog.Logger, email, userID string) {
	if invitations == nil {
		return
	}
	if _, err := invitations.ResolveInvitations(ctx, email, PrincipalUser, userID); err != nil {
		logger.Warn("resolve invitations failed", "error_kind", ErrorKind(err))
	}
}
