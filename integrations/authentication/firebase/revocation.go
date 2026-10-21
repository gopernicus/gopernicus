package firebase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"firebase.google.com/go/v4/auth"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// ErrUnknownSubject reports a subject the project does not know. It matches
// sdk.ErrNotFound, so a consumer never reads it as "valid".
var ErrUnknownSubject = fmt.Errorf("firebase: unknown subject: %w", sdk.ErrNotFound)

// CheckRevocation reads the subject's standing with one GetUser call: Disabled,
// and ValidAfter from TokensValidAfterMillis (the second RevokeRefreshTokens
// last ran, else account creation). It accepts either issuer of the provider's
// project, since ID tokens and session cookies share one user pool. Only the
// two standing fields are read; the user record is never logged or kept.
func (p *Provider) CheckRevocation(ctx context.Context, issuer, subject string) (sdkauth.RevocationFacts, error) {
	if issuer != IDTokenIssuer(p.projectID) && issuer != SessionCookieIssuer(p.projectID) {
		return sdkauth.RevocationFacts{}, sdkauth.ErrIssuerMismatch
	}
	if strings.TrimSpace(subject) == "" {
		return sdkauth.RevocationFacts{}, fmt.Errorf("%w: empty", ErrUnknownSubject)
	}
	user, err := p.getUser(ctx, subject)
	if err != nil {
		if auth.IsUserNotFound(err) {
			return sdkauth.RevocationFacts{}, ErrUnknownSubject
		}
		if kind, ok := unavailableKind(ctx, err); ok {
			return sdkauth.RevocationFacts{}, &sdkauth.UnavailableError{Kind: kind, Err: err}
		}
		// An unexplained failure is an outage, never a silent "valid".
		return sdkauth.RevocationFacts{}, &sdkauth.UnavailableError{Kind: sdkauth.UnavailableUpstream, Err: err}
	}
	facts := sdkauth.RevocationFacts{Disabled: user.Disabled, CheckedAt: p.now().UTC()}
	if user.TokensValidAfterMillis > 0 {
		facts.ValidAfter = time.UnixMilli(user.TokensValidAfterMillis).UTC()
	}
	return facts, nil
}
