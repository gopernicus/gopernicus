package authsvc

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
)

// provisionRevokeChallengePurposes are the outstanding secrets an ADOPTION
// revokes for the adopted account (CHAU-6.5). They are the ones a squatter who
// registered the address without proving it could still redeem: a pending reset
// link, a pending OTP, and a pending magic link other than the one being consumed.
//
// It mirrors the password-reset purge list rather than inventing a second one.
var provisionRevokeChallengePurposes = []string{
	challenge.PurposePasswordReset,
	challenge.PurposeLoginOTP,
	challenge.PurposeLoginMagicLink,
	challenge.PurposeRemovePassword,
	challenge.PurposeStepUp,
}

// provisionOnRedeemEnabled reports whether the atomic provisioning rail is wired
// AND enabled. Both are required: the flag alone is meaningless without the
// repository, and package auth already refuses that combination at construction.
func (s *Service) provisionOnRedeemEnabled() bool {
	return s.provisionOnRedeem && s.passwordlessRedeem != nil
}

// magicLinkSubjectKey is the stable, PII-FREE key an email magic-link challenge is
// unique under (CHAU-6.1).
//
// It is derived from the identifier digest plus the purpose, so it exists whether
// or not the address currently maps to a user — which is the whole point. A resend
// to the same address therefore REPLACES the prior link even across the race where
// an unknown address becomes registered between the two sends, because both sends
// compute the same key.
//
// The raw address never becomes the key: identifierDigest is the same PII-free
// derivation the rate limiter and the delivery logical key already use.
func (s *Service) magicLinkSubjectKey(kind, normalizedValue string) string {
	return "challenge:" + s.identifierDigest(kind, normalizedValue) + ":" + challenge.PurposeLoginMagicLink
}

// newMagicLinkBinding builds the versioned binding stored with an email magic
// link. ident is the CURRENT active claim at issue, or the zero value when the
// address matches no account.
//
// ProvisionIfAbsent is captured HERE, at issue, and is never re-derived at
// consume. That is deliberate: flipping the host's configuration must not change
// what an already-mailed link does in either direction.
func (s *Service) newMagicLinkBinding(kind, normalizedValue string, ident identifier.Identifier) passwordless.Binding {
	return passwordless.Binding{
		Version:           passwordless.BindingVersion,
		Kind:              kind,
		NormalizedValue:   normalizedValue,
		IdentifierID:      ident.ID,
		UserID:            ident.UserID,
		ProvisionIfAbsent: s.provisionOnRedeemEnabled() && kind == string(identifier.KindEmail),
	}
}

// redeemPasswordlessAtomic completes a magic link through the ONE-transaction
// redemption repository (CHAU-6.5).
//
// The plaintext session credentials are generated HERE, in service memory, and
// only the stored representation crosses into the repository. If the transaction
// does not commit, the caller returns before the plaintext is ever handed out —
// so a rolled-back redemption cannot leak a usable token.
//
// Every stable failure is the caller's single generic ErrPasswordlessLogin; no
// response ever says "account created" versus "logged in", because the difference
// is exactly what an attacker probing an address would want to learn. The
// session-hydration endpoint shows the resulting user normally afterwards.
func (s *Service) redeemPasswordlessAtomic(ctx context.Context, token string) (TokenPair, passwordless.RedeemResult, error) {
	now := s.now()

	// Generate the proposed session and its plaintext refresh token up front, but
	// hand the repository only the stored form.
	proposed, rawRefresh := session.NewSession("", s.refreshTTL, now)
	refreshHash, err := s.hashSessionToken(rawRefresh)
	if err != nil {
		return TokenPair{}, passwordless.RedeemResult{}, err
	}
	proposed.RefreshTokenHash = refreshHash
	proposed.Authentication = s.primaryAuthentication(session.MethodEmailLink)

	res, err := s.passwordlessRedeem.Redeem(ctx, passwordless.RedeemInput{
		Purpose:     challenge.PurposeLoginMagicLink,
		TokenDigest: s.protector.DigestToken(token),
		Session:     proposed,
		// A provisioned account starts with an EMPTY display name: the feature has
		// no name to invent, and asking for one would defeat the point of a link
		// sign-in. The host's profile flow fills it in later.
		NewUser: user.NewUser(s.ids, "", now),
		NewIdentifier: identifier.Identifier{
			Kind:                identifier.KindEmail,
			LoginEnabled:        true,
			RecoveryEnabled:     true,
			NotificationEnabled: true,
			IsPrimary:           true,
		},
		AdoptedIdentifierUses:   identifier.Uses{Login: true, Recovery: true, Notification: true},
		RevokeChallengePurposes: provisionRevokeChallengePurposes,
		Now:                     now,
	})
	if err != nil {
		return TokenPair{}, passwordless.RedeemResult{}, err
	}

	// Only now, after the commit, is the plaintext pair assembled.
	access, expiresAt, err := s.signAccessToken(res.User.ID, res.Session.ID)
	if err != nil {
		return TokenPair{}, passwordless.RedeemResult{}, err
	}
	return TokenPair{AccessToken: access, AccessExpiresAt: expiresAt, RefreshToken: rawRefresh}, res, nil
}

// recordPasswordlessOutcome writes the secret-free audit distinction between
// provisioning, adoption, and an ordinary login (CHAU-6.6).
//
// The PUBLIC response stays generic; the audit rail is where an operator can tell
// the three apart. Details carry the outcome class and the identifier kind only —
// never the address, never the token.
func (s *Service) recordPasswordlessOutcome(ctx context.Context, res passwordless.RedeemResult, kind string) {
	eventType := securityevent.TypePasswordlessLogin
	switch res.Outcome {
	case passwordless.OutcomeProvisionNew:
		eventType = securityevent.TypePasswordlessProvisioned
	case passwordless.OutcomeVerifyAndAdoptExistingUnverified:
		eventType = securityevent.TypePasswordlessAdopted
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: res.User.ID,
		Type:   eventType,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"kind":    kind,
			"purpose": challenge.PurposeLoginMagicLink,
			"outcome": string(res.Outcome),
		},
	})
}

// genericRedemptionError collapses the repository's stable rejection into the
// caller's one public failure. An infrastructure error passes through so a store
// outage is a 500 rather than a credential rejection.
func genericRedemptionError(err error) error {
	if errors.Is(err, passwordless.ErrRedemption) {
		return ErrPasswordlessLogin
	}
	return err
}
