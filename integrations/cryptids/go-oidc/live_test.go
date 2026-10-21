//go:build live

package gooidc

import (
	"context"
	"os"
	"testing"
	"time"

	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// TestLiveTenant verifies one real token from one real issuer through
// discovery, the JWKS fetch, and NewOIDC. Run with:
//
//	OIDC_LIVE_ISSUER=… OIDC_LIVE_AUDIENCE=… OIDC_LIVE_TOKEN=… go test -tags=live -run TestLiveTenant ./...
//
// The token must be unexpired (beyond ClockTolerance) when the test runs.
func TestLiveTenant(t *testing.T) {
	issuer := os.Getenv("OIDC_LIVE_ISSUER")
	audience := os.Getenv("OIDC_LIVE_AUDIENCE")
	token := os.Getenv("OIDC_LIVE_TOKEN")
	if issuer == "" || audience == "" || token == "" {
		t.Skip("SKIP (live tenant NOT verified): set OIDC_LIVE_ISSUER, OIDC_LIVE_AUDIENCE and OIDC_LIVE_TOKEN to verify a real provider token")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := New(ctx, issuer, WithAudiences(audience))
	if err != nil {
		t.Fatalf("New (discovery against %s): %v", issuer, err)
	}
	claims, err := v.Verify(ctx, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	t.Logf("verified: iss=%v sub=%v aud=%v exp=%v", claims["iss"], claims["sub"], claims["aud"], claims["exp"])

	id, err := sdkauth.NewOIDC(issuer, v, sdkauth.Profile{}).Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: token})
	if err != nil {
		t.Fatalf("NewOIDC.Authenticate: %v", err)
	}
	if id.Subject == "" || id.Issuer != issuer {
		t.Fatalf("Identity = %+v", id)
	}
}
