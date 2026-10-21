package authentication_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/authenticationtest"
)

const testIssuer = "https://issuer.example.test"

var testKey = []byte("authentication-test-key")

// hmacVerifier is a stdlib stand-in for a JWKS-backed cryptids.JWTVerifier:
// signature, exp/nbf with 60 seconds of tolerance, and a pinned iss.
type hmacVerifier struct {
	issuer string
	calls  atomic.Int32
	err    error
}

func (v *hmacVerifier) Verify(_ context.Context, token string) (map[string]any, error) {
	v.calls.Add(1)
	if v.err != nil {
		return nil, v.err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed")
	}
	mac := hmac.New(sha256.New, testKey)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("bad signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	exp, ok := claims["exp"].(float64)
	if !ok || time.Now().After(time.Unix(int64(exp), 0).Add(60*time.Second)) {
		return nil, errors.New("expired")
	}
	if iss, _ := claims["iss"].(string); iss != v.issuer {
		return nil, errors.New("issuer not pinned")
	}
	return claims, nil
}

func mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, testKey)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func baseClaims(extra map[string]any) map[string]any {
	now := time.Now()
	c := map[string]any{
		"iss": testIssuer,
		"sub": "subject-1",
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func header(raw string) authentication.Credential {
	return authentication.Credential{Transport: authentication.TransportHeader, Raw: raw}
}

func TestOIDCConformance(t *testing.T) {
	authenticationtest.RunProvider(t, func(t *testing.T) authentication.Provider {
		return authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{})
	}, mint)
}

func TestOIDCRejectsBeforeVerifier(t *testing.T) {
	v := &hmacVerifier{issuer: testIssuer}
	p := authentication.NewOIDC(testIssuer, v, authentication.Profile{})
	foreign := baseClaims(map[string]any{"iss": "https://other.example.test"})
	cases := map[string]struct {
		raw  string
		want error
	}{
		"oversize":       {mint(t, baseClaims(map[string]any{"pad": strings.Repeat("a", authentication.MaxCredentialBytes)})), authentication.ErrInvalidCredential},
		"malformed":      {"one.two", authentication.ErrInvalidCredential},
		"foreign issuer": {mint(t, foreign), authentication.ErrIssuerMismatch},
		"missing issuer": {mint(t, map[string]any{"sub": "x"}), authentication.ErrIssuerMismatch},
	}
	for name, tc := range cases {
		if _, err := p.Authenticate(context.Background(), header(tc.raw)); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
	if n := v.calls.Load(); n != 0 {
		t.Fatalf("verifier called %d times, want 0", n)
	}
}

// A verifier wired for the wrong issuer must not let its tokens through.
func TestOIDCRechecksVerifiedIssuer(t *testing.T) {
	misconfigured := &claimsVerifier{claims: baseClaims(map[string]any{"iss": "https://other.example.test"})}
	p := authentication.NewOIDC(testIssuer, misconfigured, authentication.Profile{})
	id, err := p.Authenticate(context.Background(), header(mint(t, baseClaims(nil))))
	if !errors.Is(err, authentication.ErrIssuerMismatch) {
		t.Fatalf("error = %v, want ErrIssuerMismatch", err)
	}
	if id.Subject != "" {
		t.Fatalf("Identity = %+v on error, want zero", id)
	}
}

type claimsVerifier struct{ claims map[string]any }

func (v *claimsVerifier) Verify(context.Context, string) (map[string]any, error) {
	return v.claims, nil
}

func TestOIDCOutageIsNotInvalid(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind authentication.UnavailableKind
	}{
		{"keys unavailable", fmt.Errorf("jwks: %w", sdk.ErrUnavailable), authentication.UnavailableNetwork},
		{"deadline", context.DeadlineExceeded, authentication.UnavailableDeadline},
		{"typed kind passes through", &authentication.UnavailableError{Kind: authentication.UnavailableRejectedCredential, Err: errors.New("key revoked")}, authentication.UnavailableRejectedCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer, err: tc.err}, authentication.Profile{})
			id, err := p.Authenticate(context.Background(), header(mint(t, baseClaims(nil))))
			if !errors.Is(err, authentication.ErrProviderUnavailable) || !errors.Is(err, sdk.ErrUnavailable) {
				t.Fatalf("error = %v, want ErrProviderUnavailable", err)
			}
			if errors.Is(err, authentication.ErrInvalidCredential) || errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("error = %v reads as an invalid credential", err)
			}
			var ue *authentication.UnavailableError
			if !errors.As(err, &ue) || ue.Kind != tc.kind {
				t.Fatalf("error = %v, want *UnavailableError kind %q", err, tc.kind)
			}
			if id.Subject != "" {
				t.Fatalf("Identity = %+v on error, want zero", id)
			}
		})
	}
}

func TestOIDCDenySignIn(t *testing.T) {
	p := authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{
		Claims:     authentication.ClaimMapping{SignInMethod: "ctx.sign_in_method"},
		DenySignIn: []string{"guest"},
	})
	guest := baseClaims(map[string]any{"ctx": map[string]any{"sign_in_method": "guest"}})
	id, err := p.Authenticate(context.Background(), header(mint(t, guest)))
	if !errors.Is(err, authentication.ErrSignInMethodDenied) || !errors.Is(err, authentication.ErrInvalidCredential) {
		t.Fatalf("guest: error = %v, want ErrSignInMethodDenied", err)
	}
	if id.Subject != "" {
		t.Fatalf("guest: Identity = %+v, want zero", id)
	}
	password := baseClaims(map[string]any{"ctx": map[string]any{"sign_in_method": "password"}})
	if _, err := p.Authenticate(context.Background(), header(mint(t, password))); err != nil {
		t.Fatalf("password: error = %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("NewOIDC with DenySignIn and no SignInMethod path did not panic")
		}
	}()
	authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{DenySignIn: []string{"guest"}})
}

func TestOIDCAttributesBound(t *testing.T) {
	extra := map[string]any{"number": 7.0, "list": []any{"a"}}
	var paths []string
	for i := range 20 {
		k := fmt.Sprintf("attr%02d", i)
		extra[k] = "v" + k
		paths = append(paths, k)
	}
	paths = append([]string{"number", "list", "attr00"}, paths...)
	p := authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{
		Claims: authentication.ClaimMapping{Extra: paths},
	})
	id, err := p.Authenticate(context.Background(), header(mint(t, baseClaims(extra))))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(id.Attributes) != authentication.MaxAttributes {
		t.Fatalf("len(Attributes) = %d, want %d", len(id.Attributes), authentication.MaxAttributes)
	}
	for _, k := range []string{"number", "list"} {
		if _, ok := id.Attributes[k]; ok {
			t.Errorf("Attributes[%q] present; non-string values are never copied", k)
		}
	}
	for i := range authentication.MaxAttributes {
		k := fmt.Sprintf("attr%02d", i)
		if id.Attributes[k] != "v"+k {
			t.Errorf("Attributes[%q] = %q, want %q (first paths in Extra order)", k, id.Attributes[k], "v"+k)
		}
	}
}

func TestOIDCMethodsClone(t *testing.T) {
	amr := []any{"pwd", "otp"}
	v := &claimsVerifier{claims: baseClaims(map[string]any{"amr": amr})}
	p := authentication.NewOIDC(testIssuer, v, authentication.Profile{})
	raw := mint(t, baseClaims(nil))

	first, err := p.Authenticate(context.Background(), header(raw))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	first.Methods[0] = "mutated"
	if amr[0] != "pwd" {
		t.Fatal("mutating Identity.Methods changed the verifier's claims")
	}
	amr[1] = "changed-later"
	if first.Methods[1] != "otp" {
		t.Fatal("Identity.Methods aliases the verifier's claims")
	}
	if !first.MFA {
		t.Error("MFA = false for amr otp")
	}

	second, _ := p.Authenticate(context.Background(), header(raw))
	second.Attributes = map[string]string{"k": "v"}
	clone := second.Clone()
	clone.Methods[0] = "x"
	clone.Attributes["k"] = "y"
	if second.Methods[0] == "x" || second.Attributes["k"] == "y" {
		t.Fatal("Identity.Clone shares a slice or map with the original")
	}
}

func TestOIDCMapping(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	p := authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{
		Claims: authentication.ClaimMapping{
			Tenant:        "https://ns.example.test/org",
			EmailVerified: "verified.email",
			MFAFlag:       "ctx.second_factor",
			DisplayName:   "profile.display",
		},
		TrustPhone: true,
	})
	claims := baseClaims(map[string]any{
		"auth_time":                   now.Add(-5 * time.Minute).Unix(),
		"email":                       "a@example.test",
		"verified":                    map[string]any{"email": "true"},
		"phone_number":                "+15550100",
		"https://ns.example.test/org": "org-1",
		"ctx":                         map[string]any{"second_factor": "totp"},
		"profile":                     map[string]any{"display": "Ada"},
		"amr":                         []any{"pwd"},
	})
	id, err := p.Authenticate(context.Background(), header(mint(t, claims)))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	want := authentication.Identity{
		Issuer:          testIssuer,
		Subject:         "subject-1",
		Email:           "a@example.test",
		EmailVerified:   true,
		Phone:           "+15550100",
		PhoneVerified:   true,
		DisplayName:     "Ada",
		Tenant:          "org-1",
		AuthenticatedAt: now.Add(-5 * time.Minute).UTC(),
		ExpiresAt:       time.Unix(claims["exp"].(int64), 0).UTC(),
		Methods:         []string{"pwd"},
		MFA:             true,
	}
	if fmt.Sprintf("%+v", id) != fmt.Sprintf("%+v", want) {
		t.Fatalf("Identity =\n%+v\nwant\n%+v", id, want)
	}

	t.Run("defaults and fallbacks", func(t *testing.T) {
		p := authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{})
		c := baseClaims(map[string]any{"email": "b@example.test", "phone_number": "+15550101"})
		id, err := p.Authenticate(context.Background(), header(mint(t, c)))
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if id.EmailVerified || id.PhoneVerified {
			t.Errorf("verified flags = %v/%v without verified claims, want false", id.EmailVerified, id.PhoneVerified)
		}
		if got, want := id.AuthenticatedAt, time.Unix(c["iat"].(int64), 0).UTC(); !got.Equal(want) {
			t.Errorf("AuthenticatedAt = %v, want iat %v", got, want)
		}
		if id.MFA || id.Methods != nil || id.Attributes != nil {
			t.Errorf("MFA/Methods/Attributes = %v/%v/%v, want unset", id.MFA, id.Methods, id.Attributes)
		}
	})

	t.Run("missing subject or exp is invalid", func(t *testing.T) {
		p := authentication.NewOIDC(testIssuer, &claimsVerifier{claims: map[string]any{"iss": testIssuer, "exp": 4102444800.0}}, authentication.Profile{})
		if _, err := p.Authenticate(context.Background(), header(mint(t, baseClaims(nil)))); !errors.Is(err, authentication.ErrInvalidCredential) {
			t.Errorf("missing sub: error = %v, want ErrInvalidCredential", err)
		}
		p = authentication.NewOIDC(testIssuer, &claimsVerifier{claims: map[string]any{"iss": testIssuer, "sub": "s"}}, authentication.Profile{})
		if _, err := p.Authenticate(context.Background(), header(mint(t, baseClaims(nil)))); !errors.Is(err, authentication.ErrInvalidCredential) {
			t.Errorf("missing exp: error = %v, want ErrInvalidCredential", err)
		}
	})
}

func TestNewOIDCPanicsOnMissingWiring(t *testing.T) {
	for name, build := range map[string]func(){
		"empty issuer": func() { authentication.NewOIDC(" ", &hmacVerifier{}, authentication.Profile{}) },
		"nil verifier": func() { authentication.NewOIDC(testIssuer, nil, authentication.Profile{}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: NewOIDC did not panic", name)
				}
			}()
			build()
		}()
	}
}

func TestCredentialRedactsRaw(t *testing.T) {
	cred := authentication.Credential{Transport: authentication.TransportCookie, Raw: "secret-token-value"}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("presented", "cred", cred)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("presented", "cred", cred)
	out := buf.String() + fmt.Sprintf("%v %+v %s", cred, cred, cred)
	if strings.Contains(out, "secret-token-value") {
		t.Fatalf("Raw leaked into output: %s", out)
	}
	if !strings.Contains(out, "cookie") {
		t.Fatalf("Transport missing from output: %s", out)
	}
}

// No production file in the capability may name an identity vendor; presets
// live in integrations.
func TestNoVendorNames(t *testing.T) {
	vendor := regexp.MustCompile(`(?i)firebase|auth0|clerk|cognito|workos|keycloak|okta`)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := vendor.FindIndex(src); loc != nil {
			t.Errorf("%s names a vendor: %q", path, src[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
