package firebase

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

const (
	// MinSessionCookieTTL and MaxSessionCookieTTL bound MintSessionCookie, as
	// Firebase does.
	MinSessionCookieTTL = 5 * time.Minute
	MaxSessionCookieTTL = 14 * 24 * time.Hour
)

// ErrInvalidSessionTTL rejects a ttl outside [MinSessionCookieTTL, MaxSessionCookieTTL].
var ErrInvalidSessionTTL = fmt.Errorf("firebase: session cookie ttl must be between %s and %s: %w", MinSessionCookieTTL, MaxSessionCookieTTL, sdk.ErrInvalidInput)

// MintSessionCookie exchanges a Firebase ID token for a session cookie valid
// for ttl. Firebase refuses an invalid or expired ID token, which is
// ErrInvalidCredential. It does not judge how recently the user signed in: the
// exchange handler checks auth_time freshness and the login request's origin
// first. Neither the ID token nor the cookie is logged.
func (p *Provider) MintSessionCookie(ctx context.Context, idToken string, ttl time.Duration) (string, error) {
	if ttl < MinSessionCookieTTL || ttl > MaxSessionCookieTTL {
		return "", ErrInvalidSessionTTL
	}
	if idToken == "" || len(idToken) > sdkauth.MaxCredentialBytes {
		return "", fmt.Errorf("%w: id token empty or oversize", sdkauth.ErrInvalidCredential)
	}
	cookie, err := p.client.SessionCookie(ctx, idToken, ttl)
	if err != nil {
		return "", classify(ctx, err, sdkauth.ErrInvalidCredential)
	}
	return cookie, nil
}
