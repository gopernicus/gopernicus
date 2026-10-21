package auth0

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/authenticationtest"
)

const domain = "gopernicus-test.us.auth0.com"

// decodingVerifier stands in for a signature-checking verifier: it decodes
// the payload and enforces exp, which is all the mapping under test needs.
type decodingVerifier struct{}

func (decodingVerifier) Verify(_ context.Context, raw string) (map[string]any, error) {
	claims, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if exp, _ := claims["exp"].(float64); int64(exp) < time.Now().Unix() {
		return nil, errors.New("expired")
	}
	return claims, nil
}

func decode(raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	return claims, json.Unmarshal(b, &claims)
}

func encode(claims map[string]any) string {
	p, _ := json.Marshal(claims)
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(p) + ".sig"
}

func provider() *sdkauth.OIDC {
	return sdkauth.NewOIDC(Issuer(domain), decodingVerifier{}, Profile(domain))
}

func TestIssuer(t *testing.T) {
	want := "https://" + domain + "/"
	for _, in := range []string{domain, "https://" + domain, "https://" + domain + "/", " " + domain + " "} {
		if got := Issuer(in); got != want {
			t.Errorf("Issuer(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "https://", domain + "/path", "http://" + domain} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Issuer(%q) did not panic", bad)
				}
			}()
			Issuer(bad)
		}()
	}
}

func TestConformance(t *testing.T) {
	authenticationtest.RunProvider(t,
		func(*testing.T) sdkauth.Provider { return provider() },
		func(_ *testing.T, claims map[string]any) string { return encode(claims) })
}

// TestAccessTokenMapping maps the documented shape of an Auth0 access token
// for an API audience, issued to a user who passed MFA in an organization.
func TestAccessTokenMapping(t *testing.T) {
	now := time.Now().Unix()
	raw := encode(map[string]any{
		"iss":         Issuer(domain),
		"sub":         "auth0|65f0c0ffee",
		"aud":         []any{"https://api.example.test", "https://" + domain + "/userinfo"},
		"iat":         now - 30,
		"exp":         now + 3600,
		"scope":       "openid profile email",
		"azp":         "client-abc",
		"org_id":      "org_123",
		"amr":         []any{"mfa"},
		"permissions": []any{"read:things"},
	})
	id, err := provider().Authenticate(context.Background(), sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	want := sdkauth.Identity{
		Issuer: Issuer(domain), Subject: "auth0|65f0c0ffee", Tenant: "org_123",
		AuthenticatedAt: time.Unix(now-30, 0).UTC(), ExpiresAt: time.Unix(now+3600, 0).UTC(),
		Methods: []string{"mfa"}, MFA: true,
		Attributes: map[string]string{AttributeAuthorizedParty: "client-abc"},
	}
	if !reflect.DeepEqual(id, want) {
		t.Fatalf("Identity = %+v\nwant       %+v", id, want)
	}
}

func TestIDTokenMapping(t *testing.T) {
	now := time.Now().Unix()
	raw := encode(map[string]any{
		"iss": Issuer(domain), "sub": "google-oauth2|1234", "aud": "client-abc",
		"iat": now, "exp": now + 600, "auth_time": now - 60,
		"email": "u@example.test", "email_verified": true, "name": "U Ser",
	})
	id, err := provider().Authenticate(context.Background(), sdkauth.Credential{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "u@example.test" || !id.EmailVerified || id.DisplayName != "U Ser" || id.MFA ||
		!id.AuthenticatedAt.Equal(time.Unix(now-60, 0)) {
		t.Fatalf("Identity = %+v", id)
	}
}

func TestMachineTokenDenied(t *testing.T) {
	now := time.Now().Unix()
	raw := encode(map[string]any{
		"iss": Issuer(domain), "sub": "client-abc@clients", "aud": "https://api.example.test",
		"iat": now, "exp": now + 600, "gty": GrantClientCredentials, "azp": "client-abc",
	})
	if _, err := provider().Authenticate(context.Background(), sdkauth.Credential{Raw: raw}); !errors.Is(err, sdkauth.ErrSignInMethodDenied) {
		t.Fatalf("machine token: %v, want ErrSignInMethodDenied", err)
	}
}

// TestIssuerTrailingSlash pins that a token whose iss lacks Auth0's trailing
// slash is a foreign issuer.
func TestIssuerTrailingSlash(t *testing.T) {
	now := time.Now().Unix()
	raw := encode(map[string]any{"iss": strings.TrimSuffix(Issuer(domain), "/"), "sub": "x", "iat": now, "exp": now + 600})
	if _, err := provider().Authenticate(context.Background(), sdkauth.Credential{Raw: raw}); !errors.Is(err, sdkauth.ErrIssuerMismatch) {
		t.Fatalf("slashless iss: %v", err)
	}
}

func TestProfileIsFresh(t *testing.T) {
	a := Profile(domain)
	a.DenySignIn[0] = "mutated"
	a.Claims.Extra[0] = "mutated"
	if b := Profile(domain); b.DenySignIn[0] != GrantClientCredentials || b.Claims.Extra[0] != AttributeAuthorizedParty {
		t.Fatalf("Profile shares state: %+v", b)
	}
}
