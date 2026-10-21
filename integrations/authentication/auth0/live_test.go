//go:build live

package auth0

import (
	"context"
	"os"
	"testing"

	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// TestLiveTokenMapping maps one real Auth0 token through Profile. It checks
// the MAPPING only: the payload is decoded without verifying the signature.
// Signature verification of a real Auth0 token goes through
// integrations/cryptids/go-oidc, which this module must not import (G13); the
// host's cmd test (examples/external-identity) or go-oidc's own live leg
// (OIDC_LIVE_ISSUER=https://<domain>/) covers it. Run with:
//
//	AUTH0_DOMAIN=… AUTH0_LIVE_TOKEN=… go test -tags=live -run TestLiveTokenMapping ./...
func TestLiveTokenMapping(t *testing.T) {
	domain := os.Getenv("AUTH0_DOMAIN")
	token := os.Getenv("AUTH0_LIVE_TOKEN")
	if domain == "" || token == "" {
		t.Skip("SKIP (live Auth0 mapping NOT verified): set AUTH0_DOMAIN and AUTH0_LIVE_TOKEN")
	}
	claims, err := decode(token)
	if err != nil {
		t.Fatalf("decode: %v (an opaque access token means the SPA requested no API audience)", err)
	}
	if claims["iss"] != Issuer(domain) {
		t.Fatalf("iss = %v, want %q", claims["iss"], Issuer(domain))
	}
	id, err := sdkauth.NewOIDC(Issuer(domain), decodingVerifier{}, Profile(domain)).
		Authenticate(context.Background(), sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: token})
	if err != nil {
		t.Fatalf("Authenticate (mapping only): %v", err)
	}
	t.Logf("mapped (signature NOT verified here): sub=%s tenant=%q mfa=%v email_present=%v attributes=%v",
		id.Subject, id.Tenant, id.MFA, id.Email != "", id.Attributes)
}
