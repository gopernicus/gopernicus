package gooidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/authenticationtest"

	"github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc/oidctest"
)

type fixture struct {
	issuer   *oidctest.Issuer
	verifier *Verifier
	clock    *fakeClock
	logs     *logBuffer
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type logBuffer struct {
	mu      sync.Mutex
	records []slog.Record
}

func (b *logBuffer) Enabled(context.Context, slog.Level) bool { return true }
func (b *logBuffer) Handle(_ context.Context, r slog.Record) error {
	b.mu.Lock()
	b.records = append(b.records, r.Clone())
	b.mu.Unlock()
	return nil
}
func (b *logBuffer) WithAttrs([]slog.Attr) slog.Handler { return b }
func (b *logBuffer) WithGroup(string) slog.Handler      { return b }

func (b *logBuffer) count(msgPrefix string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, r := range b.records {
		if strings.HasPrefix(r.Message, msgPrefix) {
			n++
		}
	}
	return n
}

func (b *logBuffer) attr(msgPrefix, key string) (slog.Value, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.records) - 1; i >= 0; i-- {
		r := b.records[i]
		if !strings.HasPrefix(r.Message, msgPrefix) {
			continue
		}
		var found slog.Value
		ok := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				found, ok = a.Value, true
				return false
			}
			return true
		})
		return found, ok
	}
	return slog.Value{}, false
}

func newFixture(t *testing.T, issuerOpts []oidctest.Option, opts ...Option) *fixture {
	t.Helper()
	iss, err := oidctest.Start(issuerOpts...)
	if err != nil {
		t.Fatalf("oidctest.Start: %v", err)
	}
	t.Cleanup(iss.Close)
	logs := &logBuffer{}
	base := []Option{WithJWKSURL(iss.JWKSURL()), WithAudiences(iss.Audience()), WithLogger(slog.New(logs))}
	v, err := New(context.Background(), iss.Issuer(), append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	clock := &fakeClock{now: time.Now()}
	v.keys.now = clock.Now
	return &fixture{issuer: iss, verifier: v, clock: clock, logs: logs}
}

func (f *fixture) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	if claims == nil {
		claims = map[string]any{}
	}
	if _, ok := claims["sub"]; !ok {
		claims["sub"] = "subject-1"
	}
	token, err := f.issuer.Mint(claims)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return token
}

func assertInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalidToken) || !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
	if errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("error = %v matches sdk.ErrUnavailable; a rejected token is not an outage", err)
	}
}

func assertUnavailable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrKeysUnavailable) || !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrKeysUnavailable", err)
	}
	if errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("error = %v matches sdk.ErrUnauthorized; an outage must not read as a bad token", err)
	}
}

func TestRunProviderOverNewOIDC(t *testing.T) {
	for _, alg := range []string{"RS256", "ES256"} {
		t.Run(alg, func(t *testing.T) {
			// RunProvider builds the provider and mints within one sequential
			// subtest, so the latest fixture is the one to mint with.
			var current *fixture
			authenticationtest.RunProvider(t,
				func(t *testing.T) sdkauth.Provider {
					current = newFixture(t, []oidctest.Option{oidctest.WithAlgorithm(alg)})
					return sdkauth.NewOIDC(current.issuer.Issuer(), current.verifier, sdkauth.Profile{})
				},
				func(t *testing.T, claims map[string]any) string {
					token, err := current.issuer.Mint(claims)
					if err != nil {
						t.Fatalf("Mint: %v", err)
					}
					return token
				})
		})
	}
}

func TestVerifyValid(t *testing.T) {
	f := newFixture(t, nil)
	claims, err := f.verifier.Verify(context.Background(), f.mint(t, map[string]any{"email": "a@example.test"}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["iss"] != f.issuer.Issuer() || claims["sub"] != "subject-1" || claims["email"] != "a@example.test" {
		t.Fatalf("claims = %v", claims)
	}
	if _, ok := claims["exp"].(float64); !ok {
		t.Fatalf("exp = %T, want float64", claims["exp"])
	}
}

func TestVerifyDiscovery(t *testing.T) {
	iss, err := oidctest.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer iss.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := New(ctx, iss.Issuer(), WithAudiences(iss.Audience()))
	if err != nil {
		t.Fatalf("New with discovery: %v", err)
	}
	token, _ := iss.Mint(map[string]any{"sub": "s"})
	if _, err := v.Verify(ctx, token); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if _, err := New(ctx, iss.Issuer()+"/other", WithAudiences("a")); err == nil {
		t.Fatal("New against an issuer whose discovery does not match succeeded")
	}
}

func TestNewValidation(t *testing.T) {
	ctx := context.Background()
	jwks := WithJWKSURL("https://issuer.example.test/jwks")
	cases := map[string]struct {
		issuer string
		opts   []Option
	}{
		"empty issuer":          {"", []Option{jwks, WithAudiences("a")}},
		"relative issuer":       {"issuer.example.test", []Option{jwks, WithAudiences("a")}},
		"no audience":           {"https://issuer.example.test", []Option{jwks}},
		"blank audience":        {"https://issuer.example.test", []Option{jwks, WithAudiences(" ")}},
		"alg none":              {"https://issuer.example.test", []Option{jwks, WithAudiences("a"), WithAlgorithms("none")}},
		"alg HS256":             {"https://issuer.example.test", []Option{jwks, WithAudiences("a"), WithAlgorithms("RS256", "HS256")}},
		"empty allow-list":      {"https://issuer.example.test", []Option{jwks, WithAudiences("a"), WithAlgorithms()}},
		"nil option":            {"https://issuer.example.test", []Option{jwks, WithAudiences("a"), nil}},
		"plain http jwks":       {"https://issuer.example.test", []Option{WithJWKSURL("http://issuer.example.test/jwks"), WithAudiences("a")}},
		"relative jwks":         {"https://issuer.example.test", []Option{WithJWKSURL("/jwks"), WithAudiences("a")}},
		"unknown jwks scheme":   {"https://issuer.example.test", []Option{WithJWKSURL("ftp://issuer.example.test/jwks"), WithAudiences("a")}},
		"discovery unreachable": {"https://127.0.0.1:1", []Option{WithAudiences("a")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(ctx, tc.issuer, tc.opts...); err == nil {
				t.Fatal("New succeeded")
			}
		})
	}
	if _, err := New(ctx, "https://issuer.example.test", WithJWKSURL("http://127.0.0.1:9/jwks"), WithAudiences("a")); err != nil {
		t.Fatalf("loopback http JWKS rejected: %v", err)
	}
	var zero *Verifier
	if _, err := zero.Verify(ctx, "a.b.c"); err == nil {
		t.Fatal("nil Verifier verified")
	}
}

const plaintextDiscoveryWarning = "gooidc: discovering over plain http"

// TestIssuerTransport pins when an http issuer is admitted. Discovery fetches
// the key set's location from the issuer, so it needs https or loopback unless
// WithInsecureIssuer; a pinned JWKS URL fetches nothing from the issuer, which
// is then only compared to iss.
func TestIssuerTransport(t *testing.T) {
	ctx := context.Background()

	t.Run("https discovery", func(t *testing.T) {
		iss, client, _ := routedIssuer(t, "https://issuer.example.com")
		logs := &logBuffer{}
		v, err := New(ctx, iss.Issuer(), WithAudiences(iss.Audience()), WithHTTPClient(client), WithLogger(slog.New(logs)))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		verifyMinted(t, v, iss, iss.Issuer())
		if n := logs.count(plaintextDiscoveryWarning); n != 0 {
			t.Fatalf("plaintext warnings = %d, want 0", n)
		}
	})

	t.Run("plain http discovery refused", func(t *testing.T) {
		iss, client, requests := routedIssuer(t, "http://issuer.example.com")
		_, err := New(ctx, iss.Issuer(), WithAudiences(iss.Audience()), WithHTTPClient(client))
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("New = %v, want sdk.ErrInvalidInput", err)
		}
		for _, want := range []string{"must use https for discovery", "WithJWKSURL", "WithInsecureIssuer"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err, want)
			}
		}
		if n := requests.Load(); n != 0 {
			t.Fatalf("requests = %d, want 0: the refusal precedes discovery", n)
		}
	})

	t.Run("plain http issuer with pinned JWKS", func(t *testing.T) {
		keys, err := oidctest.Start()
		if err != nil {
			t.Fatal(err)
		}
		defer keys.Close()
		logs := &logBuffer{}
		const issuer = "http://issuer.example.com"
		v, err := New(ctx, issuer, WithJWKSURL(keys.JWKSURL()), WithAudiences(keys.Audience()), WithLogger(slog.New(logs)))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		verifyMinted(t, v, keys, issuer)
		if n := logs.count(plaintextDiscoveryWarning); n != 0 {
			t.Fatalf("plaintext warnings = %d, want 0", n)
		}
	})

	for _, issuer := range []string{"http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080"} {
		t.Run("loopback discovery "+issuer, func(t *testing.T) {
			iss, client, _ := routedIssuer(t, issuer)
			logs := &logBuffer{}
			v, err := New(ctx, iss.Issuer(), WithAudiences(iss.Audience()), WithHTTPClient(client), WithLogger(slog.New(logs)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			verifyMinted(t, v, iss, iss.Issuer())
			if n := logs.count(plaintextDiscoveryWarning); n != 0 {
				t.Fatalf("plaintext warnings = %d, want 0", n)
			}
		})
	}

	for name, tc := range map[string]struct {
		issuer   string
		warnings int
	}{
		"plain http discovery": {"http://keycloak:8080", 1},
		"https discovery":      {"https://issuer.example.com", 0},
		"loopback discovery":   {"http://localhost:8080", 0},
	} {
		t.Run("WithInsecureIssuer "+name, func(t *testing.T) {
			iss, client, _ := routedIssuer(t, tc.issuer)
			logs := &logBuffer{}
			v, err := New(ctx, iss.Issuer(), WithInsecureIssuer(), WithAudiences(iss.Audience()), WithHTTPClient(client), WithLogger(slog.New(logs)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			verifyMinted(t, v, iss, iss.Issuer())
			if n := logs.count(plaintextDiscoveryWarning); n != tc.warnings {
				t.Fatalf("plaintext warnings = %d, want %d", n, tc.warnings)
			}
			if tc.warnings > 0 {
				if got, _ := logs.attr(plaintextDiscoveryWarning, "issuer"); got.String() != tc.issuer {
					t.Fatalf("warning issuer = %q, want %q", got, tc.issuer)
				}
			}
		})
	}

	t.Run("WithInsecureIssuer keeps the other refusals", func(t *testing.T) {
		cases := map[string]struct {
			issuer string
			opts   []Option
		}{
			"non-http issuer":        {"ftp://issuer.example.com", nil},
			"relative issuer":        {"issuer.example.com", nil},
			"pinned plain-http JWKS": {"http://issuer.example.com", []Option{WithJWKSURL("http://issuer.example.com/jwks")}},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				opts := append([]Option{WithInsecureIssuer(), WithAudiences("a")}, tc.opts...)
				if _, err := New(ctx, tc.issuer, opts...); !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("New = %v, want sdk.ErrInvalidInput", err)
				}
			})
		}
	})
}

// TestCheapRejectionsNeverFetch pins the rejection order: none of these
// tokens may cost a JWKS request.
func TestCheapRejectionsNeverFetch(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()

	foreign, _ := f.issuer.MintAs("https://foreign.example.test", map[string]any{"sub": "s"})
	unsigned, _ := f.issuer.MintUnsigned(map[string]any{"sub": "s"})
	hmac := forgeHeader(t, f.mint(t, nil), map[string]any{"alg": "HS256", "kid": f.issuer.KeyID()})
	es := forgeHeader(t, f.mint(t, nil), map[string]any{"alg": "ES384", "kid": f.issuer.KeyID()})
	noneUpper := forgeHeader(t, f.mint(t, nil), map[string]any{"alg": "None"})
	payloadNoIss := encodeSegment(t, map[string]any{"alg": "RS256"}) + "." + encodeSegment(t, map[string]any{"sub": "s"}) + ".sig"
	numericIss := encodeSegment(t, map[string]any{"alg": "RS256"}) + "." + encodeSegment(t, map[string]any{"iss": 1}) + ".sig"

	cases := map[string]string{
		"empty":            "",
		"oversize":         strings.Repeat("a", MaxTokenBytes+1),
		"two segments":     "a.b",
		"four segments":    "a.b.c.d",
		"bad base64":       "!!!.***.???",
		"padded base64":    "eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJ4In0=.sig",
		"payload not json": "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".sig",
		"foreign issuer":   foreign,
		"missing iss":      payloadNoIss,
		"numeric iss":      numericIss,
		"alg none":         unsigned,
		"alg None":         noneUpper,
		"alg HS256":        hmac,
		"alg not allowed":  es,
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := f.verifier.Verify(ctx, token)
			assertInvalid(t, err)
		})
	}
	if n := f.issuer.JWKSRequests(); n != 0 {
		t.Fatalf("JWKS requests = %d, want 0: cheap rejections must precede any fetch", n)
	}
}

// TestEmulatorShapedTokenRefused pins the Firebase Auth emulator case: an
// unsigned token carrying the real issuer is refused, through NewOIDC too.
func TestEmulatorShapedTokenRefused(t *testing.T) {
	f := newFixture(t, nil)
	token, err := f.issuer.MintUnsigned(map[string]any{"sub": "emulated", "firebase": map[string]any{"sign_in_provider": "password"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.Verify(context.Background(), token); err == nil {
		t.Fatal("alg none token verified")
	}
	provider := sdkauth.NewOIDC(f.issuer.Issuer(), f.verifier, sdkauth.Profile{})
	_, err = provider.Authenticate(context.Background(), sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: token})
	if !errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Fatalf("NewOIDC error = %v, want ErrInvalidCredential", err)
	}
	if n := f.issuer.JWKSRequests(); n != 0 {
		t.Fatalf("JWKS requests = %d, want 0", n)
	}
}

func TestSignatureAndKeyMismatch(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	valid := f.mint(t, nil)
	parts := strings.Split(valid, ".")

	tampered := parts[0] + "." + encodeSegment(t, map[string]any{
		"iss": f.issuer.Issuer(), "aud": f.issuer.Audience(), "sub": "attacker",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}) + "." + parts[2]
	_, err := f.verifier.Verify(ctx, tampered)
	assertInvalid(t, err)

	// The published kid with a different private key.
	impostor, _ := f.issuer.MintWithUnpublishedKey(f.issuer.KeyID(), map[string]any{"sub": "s"})
	_, err = f.verifier.Verify(ctx, impostor)
	assertInvalid(t, err)

	// A published RSA key presented as a different allowed algorithm.
	v2, err := New(ctx, f.issuer.Issuer(), WithJWKSURL(f.issuer.JWKSURL()), WithAudiences(f.issuer.Audience()), WithAlgorithms("RS256", "PS256"))
	if err != nil {
		t.Fatal(err)
	}
	ps := forgeHeader(t, valid, map[string]any{"alg": "PS256", "kid": f.issuer.KeyID()})
	_, err = v2.Verify(ctx, ps)
	assertInvalid(t, err)
	if !strings.Contains(err.Error(), "published for RS256") {
		t.Fatalf("error = %v, want the JWK alg pin to reject", err)
	}
}

func TestTimeChecks(t *testing.T) {
	f := newFixture(t, nil)
	now := time.Now()
	sec := func(d time.Duration) float64 { return float64(now.Add(d).Unix()) }
	cases := []struct {
		name   string
		claims map[string]any
		ok     bool
	}{
		{"exp within tolerance", map[string]any{"exp": sec(-30 * time.Second)}, true},
		{"expired past tolerance", map[string]any{"exp": sec(-90 * time.Second)}, false},
		{"exp missing", map[string]any{"exp": oidctest.Omit}, false},
		{"exp null", map[string]any{"exp": nil}, false},
		{"exp string", map[string]any{"exp": "tomorrow"}, false},
		{"exp past year 9999", map[string]any{"exp": 253402300800.0}, false},
		{"nbf within tolerance", map[string]any{"nbf": sec(30 * time.Second)}, true},
		{"nbf past tolerance", map[string]any{"nbf": sec(120 * time.Second)}, false},
		{"nbf string", map[string]any{"nbf": "soon"}, false},
		{"iat within tolerance", map[string]any{"iat": sec(30 * time.Second)}, true},
		{"iat in the future", map[string]any{"iat": sec(120 * time.Second)}, false},
		{"iat before year 1", map[string]any{"iat": -62135596801.0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{"sub": "s"}
			for k, v := range tc.claims {
				claims[k] = v
			}
			_, err := f.verifier.Verify(context.Background(), f.mint(t, claims))
			if tc.ok && err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !tc.ok {
				assertInvalid(t, err)
			}
		})
	}
}

func TestAudience(t *testing.T) {
	f := newFixture(t, nil, WithAudiences("api-one", "api-two"))
	ctx := context.Background()
	cases := []struct {
		name string
		aud  any
		ok   bool
	}{
		{"string match", "api-two", true},
		{"array any-of", []any{"other", "api-one"}, true},
		{"string mismatch", "oidctest", false},
		{"array mismatch", []any{"x", "y"}, false},
		{"empty array", []any{}, false},
		{"numeric", 7, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.verifier.Verify(ctx, f.mint(t, map[string]any{"aud": tc.aud}))
			if tc.ok && err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !tc.ok {
				assertInvalid(t, err)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		_, err := f.verifier.Verify(ctx, f.mint(t, map[string]any{"aud": oidctest.Omit}))
		assertInvalid(t, err)
	})
}

func TestRotationAndRetirement(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	oldKid := f.issuer.KeyID()
	oldToken := f.mint(t, nil)
	if _, err := f.verifier.Verify(ctx, oldToken); err != nil {
		t.Fatalf("Verify(old): %v", err)
	}

	if _, err := f.issuer.Rotate(); err != nil {
		t.Fatal(err)
	}
	newToken := f.mint(t, nil)

	// Inside the floor a new kid cannot force a fetch.
	_, err := f.verifier.Verify(ctx, newToken)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify(new kid inside floor) = %v, want ErrUnknownKey", err)
	}
	f.clock.Advance(RefetchFloor)
	if _, err := f.verifier.Verify(ctx, newToken); err != nil {
		t.Fatalf("Verify(new kid after floor): %v", err)
	}
	if _, err := f.verifier.Verify(ctx, oldToken); err != nil {
		t.Fatalf("Verify(old kid still published): %v", err)
	}
	if n := f.issuer.JWKSRequests(); n != 2 {
		t.Fatalf("JWKS requests = %d, want 2", n)
	}

	if err := f.issuer.Retire(oldKid); err != nil {
		t.Fatal(err)
	}
	// A stale set refreshes in the background on a known kid; the retired key
	// then stops verifying.
	f.clock.Advance(KeySetMaxAge + time.Second)
	if _, err := f.verifier.Verify(ctx, oldToken); err != nil {
		t.Fatalf("Verify(old kid, stale set triggers refresh): %v", err)
	}
	waitFor(t, func() bool { return f.issuer.JWKSRequests() == 3 && !f.verifier.keys.fetching() })
	if _, err := f.verifier.Verify(ctx, oldToken); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify(retired kid) = %v, want ErrUnknownKey", err)
	}
}

// TestFetchFailureKeepsLastGoodKeys pins last-good retention and the outage
// classification: a known kid verifies, an unknown kid is Unavailable.
func TestFetchFailureKeepsLastGoodKeys(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	token := f.mint(t, nil)
	if _, err := f.verifier.Verify(ctx, token); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	f.issuer.SetJWKSAvailable(false)
	f.clock.Advance(KeySetMaxAge + RefetchFloor)
	if _, err := f.verifier.Verify(ctx, token); err != nil {
		t.Fatalf("Verify(known kid, refresh due, issuer down): %v", err)
	}
	waitFor(t, func() bool { return !f.verifier.keys.fetching() && f.issuer.JWKSRequests() == 2 })
	if _, err := f.verifier.Verify(ctx, token); err != nil {
		t.Fatalf("Verify(known kid after failed refresh): %v", err)
	}

	unknown, _ := f.issuer.MintWithUnpublishedKey("rotated-kid", map[string]any{"sub": "s"})
	_, err := f.verifier.Verify(ctx, unknown)
	assertUnavailable(t, err)
	if n := f.issuer.JWKSRequests(); n != 2 {
		t.Fatalf("JWKS requests = %d, want 2 (the floor also holds after a failure)", n)
	}

	f.clock.Advance(RefetchFloor)
	_, err = f.verifier.Verify(ctx, unknown)
	assertUnavailable(t, err)
	if n := f.issuer.JWKSRequests(); n != 3 {
		t.Fatalf("JWKS requests = %d, want 3", n)
	}

	provider := sdkauth.NewOIDC(f.issuer.Issuer(), f.verifier, sdkauth.Profile{})
	_, err = provider.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: unknown})
	var unavailable *sdkauth.UnavailableError
	if !errors.As(err, &unavailable) || errors.Is(err, sdkauth.ErrInvalidCredential) {
		t.Fatalf("NewOIDC error = %v, want an UnavailableError", err)
	}
	if _, err := provider.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.TransportHeader, Raw: token}); err != nil {
		t.Fatalf("NewOIDC(known kid) during outage: %v", err)
	}

	f.issuer.SetJWKSAvailable(true)
	f.clock.Advance(RefetchFloor)
	_, err = f.verifier.Verify(ctx, unknown)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify(unknown kid after recovery) = %v, want ErrUnknownKey", err)
	}
	if got := f.logs.count("gooidc: JWKS fetch failed"); got != 2 {
		t.Fatalf("fetch-failure logs = %d, want 2 (one per attempt)", got)
	}
}

func TestColdOutage(t *testing.T) {
	f := newFixture(t, nil)
	f.issuer.SetJWKSAvailable(false)
	_, err := f.verifier.Verify(context.Background(), f.mint(t, nil))
	assertUnavailable(t, err)
}

func TestJWKSResponses(t *testing.T) {
	iss, err := oidctest.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer iss.Close()
	token, _ := iss.Mint(map[string]any{"sub": "s"})
	small := `{"keys":[{"kty":"RSA","kid":"` + iss.KeyID() + `","n":"` + strings.Repeat("A", 171) + `","e":"AQAB"}]}`

	cases := map[string]http.HandlerFunc{
		"oversize body": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[],"pad":"` + strings.Repeat("a", MaxJWKSBytes) + `"}`))
		},
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"no keys":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"keys":[]}`)) },
		"weak key": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(small)) },
		"server error": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, iss.JWKSURL(), http.StatusFound)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			v, err := New(context.Background(), iss.Issuer(), WithJWKSURL(srv.URL), WithAudiences(iss.Audience()), WithLogger(slog.New(&logBuffer{})))
			if err != nil {
				t.Fatal(err)
			}
			_, err = v.Verify(context.Background(), token)
			assertUnavailable(t, err)
		})
	}
}

func TestFetchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	iss, err := oidctest.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer iss.Close()
	v, err := New(context.Background(), iss.Issuer(), WithJWKSURL(srv.URL), WithAudiences(iss.Audience()), WithLogger(slog.New(&logBuffer{})))
	if err != nil {
		t.Fatal(err)
	}
	v.keys.timeout = 100 * time.Millisecond
	token, _ := iss.Mint(map[string]any{"sub": "s"})
	start := time.Now()
	_, err = v.Verify(context.Background(), token)
	assertUnavailable(t, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("fetch was not bounded by the timeout")
	}
}

func TestCallerContextWhileWaiting(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	iss, err := oidctest.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer iss.Close()
	v, err := New(context.Background(), iss.Issuer(), WithJWKSURL(srv.URL), WithAudiences(iss.Audience()), WithLogger(slog.New(&logBuffer{})))
	if err != nil {
		t.Fatal(err)
	}
	token, _ := iss.Mint(map[string]any{"sub": "s"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = v.Verify(ctx, token)
	close(release)
	assertUnavailable(t, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the caller's deadline", err)
	}
}

func TestSingleFlight(t *testing.T) {
	f := newFixture(t, nil)
	token := f.mint(t, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.verifier.Verify(context.Background(), token)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	if n := f.issuer.JWKSRequests(); n != 1 {
		t.Fatalf("JWKS requests = %d, want 1", n)
	}
}

func TestKidlessToken(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	kidless := forgeHeader(t, f.mint(t, nil), map[string]any{"alg": "RS256"})
	// Re-signing is not possible here; the header change breaks the signature,
	// so this pins only that a kid-less token resolves to the sole key and
	// reaches the signature check rather than an unknown-kid rejection.
	_, err := f.verifier.Verify(ctx, kidless)
	assertInvalid(t, err)
	if errors.Is(err, ErrUnknownKey) {
		t.Fatalf("kid-less token against a one-key set = %v, want a signature rejection", err)
	}

	if _, err := f.issuer.Rotate(); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(RefetchFloor)
	if _, err := f.verifier.Verify(ctx, f.mint(t, nil)); err != nil {
		t.Fatal(err)
	}
	_, err = f.verifier.Verify(ctx, kidless)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("kid-less token against a two-key set = %v, want ErrUnknownKey", err)
	}
}

func TestNegativeKidCacheBounded(t *testing.T) {
	c := newKidCache(negativeKidEntries)
	now := time.Now()
	for i := range 1000 {
		c.add(fmt.Sprintf("kid-%d", i), now)
	}
	if c.len() != negativeKidEntries {
		t.Fatalf("len = %d, want %d", c.len(), negativeKidEntries)
	}
	c.add("x", now)
	if !c.contains("x", now) {
		t.Fatal("fresh entry missing")
	}
	if c.contains("x", now.Add(negativeKidTTL+time.Second)) {
		t.Fatal("expired entry still present")
	}
}

func forgeHeader(t *testing.T, token string, header map[string]any) string {
	t.Helper()
	parts := strings.Split(token, ".")
	return encodeSegment(t, header) + "." + parts[1] + "." + parts[2]
}

func encodeSegment(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// routedIssuer serves an oidctest issuer for issuer and returns a client that
// dials that server whatever host a URL names, so discovery reaches a
// non-loopback name without DNS. An https issuer is served over TLS; the
// httptest certificate covers *.example.com. requests counts what the server
// answered.
func routedIssuer(t *testing.T, issuer string) (*oidctest.Issuer, *http.Client, *atomic.Int64) {
	t.Helper()
	iss, err := oidctest.New(issuer)
	if err != nil {
		t.Fatal(err)
	}
	requests := new(atomic.Int64)
	handler := iss.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler.ServeHTTP(w, r)
	}))
	if strings.HasPrefix(issuer, "https://") {
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	transport := srv.Client().Transport.(*http.Transport).Clone()
	addr := srv.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, addr)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return iss, &http.Client{Transport: transport, Timeout: FetchTimeout}, requests
}

// verifyMinted proves v fetches keys and verifies a token minted under issuer.
func verifyMinted(t *testing.T, v *Verifier, iss *oidctest.Issuer, issuer string) {
	t.Helper()
	token, err := iss.MintAs(issuer, map[string]any{"sub": "s"})
	if err != nil {
		t.Fatalf("MintAs: %v", err)
	}
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// fetching reports whether a JWKS fetch is in flight.
func (s *keySet) fetching() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight != nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
