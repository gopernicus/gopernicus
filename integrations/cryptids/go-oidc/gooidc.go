// Package gooidc is a bearer-JWT verification connector wrapping exactly one
// third-party library, github.com/coreos/go-oidc/v3. Its Verifier
// structurally satisfies the sdk-owned cryptids.JWTVerifier port for tokens
// minted by an external OpenID Connect issuer (Firebase Auth, Auth0, Clerk,
// Cognito, Okta, Keycloak, or the oidctest dev issuer).
//
// go-oidc owns discovery, JWS parsing and the signature check. The connector
// owns the key set and every check go-oidc leaves to the caller:
//
//   - cheap rejections before any network: size cap, then iss equals the
//     pinned issuer, then alg in the allow-list (never "none", never HMAC),
//     then the kid lookup;
//   - a per-issuer JWKS refetch floor (RefetchFloor), single-flight, timed out
//     and body-capped (MaxJWKSBytes), keeping the last good key set when a
//     fetch fails;
//   - a bounded negative kid cache and unknown-kid logging aggregated per
//     minute, never per token;
//   - exp (required), nbf and iat with ClockTolerance, and an any-of audience.
//
// An error matching sdk.ErrUnavailable means the issuer's keys could not be
// obtained and says nothing about the token; every other error rejects it.
// It owns no claim mapping, subject resolution or pocket policy: those belong
// to sdk/capabilities/authentication.NewOIDC and the consumer.
package gooidc

import (
	"context"
	"crypto"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

const (
	// MaxTokenBytes bounds a presented token; larger input is rejected before
	// any parsing or network.
	MaxTokenBytes = 16 << 10

	// MaxJWKSBytes bounds a JWKS response body.
	MaxJWKSBytes = 1 << 20

	// RefetchFloor is the minimum interval between JWKS fetch attempts for one
	// issuer, successful or not. It is the defense against kid sprays.
	RefetchFloor = 30 * time.Second

	// ClockTolerance is the skew allowed on exp, nbf and iat.
	ClockTolerance = 60 * time.Second

	// FetchTimeout bounds one JWKS fetch, independent of any caller deadline.
	FetchTimeout = 10 * time.Second

	// KeySetMaxAge is how long a fetched key set is served before a known kid
	// triggers a background refresh, so a key the issuer withdrew stops verifying.
	KeySetMaxAge = 15 * time.Minute
)

// Limit NumericDates to calendar years 0001–9999, matching golang-jwt's range.
const (
	minNumericDate = -62135596800 // 0001-01-01T00:00:00Z, inclusive.
	maxNumericDate = 253402300800 // 10000-01-01T00:00:00Z, exclusive.
)

var (
	// ErrInvalidToken rejects a token. It matches sdk.ErrUnauthorized.
	ErrInvalidToken = fmt.Errorf("gooidc: invalid token: %w", sdk.ErrUnauthorized)

	// ErrUnknownKey rejects a token whose kid the issuer does not publish. It
	// matches ErrInvalidToken.
	ErrUnknownKey = fmt.Errorf("gooidc: unknown signing key: %w", ErrInvalidToken)

	// ErrKeysUnavailable reports that the issuer's keys could not be obtained;
	// it says nothing about the token. It matches sdk.ErrUnavailable.
	ErrKeysUnavailable = fmt.Errorf("gooidc: issuer keys unavailable: %w", sdk.ErrUnavailable)
)

// defaultAlgorithms are the asymmetric algorithms every mainstream provider uses.
var defaultAlgorithms = []string{oidc.RS256, oidc.ES256}

// supportedAlgorithms is the closed set WithAlgorithms accepts. "none" and the
// HMAC family are absent: a public JWKS cannot carry a shared secret.
var supportedAlgorithms = []string{
	oidc.RS256, oidc.RS384, oidc.RS512,
	oidc.PS256, oidc.PS384, oidc.PS512,
	oidc.ES256, oidc.ES384, oidc.ES512,
	oidc.EdDSA,
}

// Compile-time proof that Verifier satisfies the sdk-owned port.
var _ cryptids.JWTVerifier = (*Verifier)(nil)

// Verifier verifies JWTs from one issuer. Construct it with New; the zero
// value is not usable. It is safe for concurrent use; share one per issuer so
// the refetch floor holds process-wide.
type Verifier struct {
	issuer     string
	audiences  []string
	algorithms []string
	oidcConfig *oidc.Config
	keys       *keySet
	now        func() time.Time
}

// New builds a Verifier pinned to issuer. Without WithJWKSURL it runs OIDC
// discovery against issuer under ctx (bound it with a timeout); with it, New
// performs no network I/O. At least one audience is required. Keys are fetched
// lazily on the first token.
func New(ctx context.Context, issuer string, opts ...Option) (*Verifier, error) {
	cfg := config{algorithms: slices.Clone(defaultAlgorithms)}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("gooidc: nil Option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}

	if err := validateIssuer(issuer); err != nil {
		return nil, err
	}
	if len(cfg.audiences) == 0 {
		return nil, fmt.Errorf("gooidc: at least one audience is required (WithAudiences): %w", sdk.ErrInvalidInput)
	}
	for _, aud := range cfg.audiences {
		if strings.TrimSpace(aud) == "" {
			return nil, fmt.Errorf("gooidc: empty audience: %w", sdk.ErrInvalidInput)
		}
	}
	if len(cfg.algorithms) == 0 {
		return nil, fmt.Errorf("gooidc: at least one algorithm is required: %w", sdk.ErrInvalidInput)
	}
	for _, alg := range cfg.algorithms {
		if !slices.Contains(supportedAlgorithms, alg) {
			return nil, fmt.Errorf("gooidc: algorithm %q is not allowed (asymmetric JWS algorithms only): %w", alg, sdk.ErrInvalidInput)
		}
	}
	client := cfg.httpClient
	if client == nil {
		client = defaultHTTPClient()
	}
	logger := cfg.logger
	if logger == nil {
		logger = slog.Default()
	}

	jwksURL := cfg.jwksURL
	if jwksURL == "" {
		discovered, err := discoverJWKSURL(ctx, client, issuer)
		if err != nil {
			return nil, err
		}
		jwksURL = discovered
	}
	if err := validateJWKSURL(jwksURL); err != nil {
		return nil, err
	}

	algorithms := slices.Clone(cfg.algorithms)
	return &Verifier{
		issuer:     issuer,
		audiences:  slices.Clone(cfg.audiences),
		algorithms: algorithms,
		oidcConfig: &oidc.Config{
			SupportedSigningAlgs: algorithms,
			// The adapter checks exp/nbf/iat with ClockTolerance and an any-of
			// audience itself; go-oidc's checks are single-audience and strict.
			SkipClientIDCheck: true,
			SkipExpiryCheck:   true,
		},
		keys: newKeySet(issuer, jwksURL, client, logger),
		now:  time.Now,
	}, nil
}

// Issuer returns the pinned issuer.
func (v *Verifier) Issuer() string { return v.issuer }

// Verify checks token and returns its claims. JSON numbers are float64.
func (v *Verifier) Verify(ctx context.Context, token string) (map[string]any, error) {
	if v == nil || v.keys == nil {
		return nil, errors.New("gooidc: verifier must be created with New")
	}
	header, err := v.precheck(token)
	if err != nil {
		return nil, err
	}

	key, err := v.keys.key(ctx, header.Kid, header.Alg)
	if err != nil {
		return nil, err
	}

	keySet := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key}}
	idToken, err := oidc.NewVerifier(v.issuer, keySet, v.oidcConfig).Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil || claims == nil {
		return nil, fmt.Errorf("%w: claims are not a JSON object", ErrInvalidToken)
	}
	if err := checkTimes(claims, v.now()); err != nil {
		return nil, err
	}
	if err := checkAudience(claims, v.audiences); err != nil {
		return nil, err
	}
	return claims, nil
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// precheck runs the rejections that need no key and no network, in order:
// size, shape, iss, alg.
func (v *Verifier) precheck(token string) (tokenHeader, error) {
	var header tokenHeader
	if token == "" {
		return header, fmt.Errorf("%w: empty", ErrInvalidToken)
	}
	if len(token) > MaxTokenBytes {
		return header, fmt.Errorf("%w: exceeds %d bytes", ErrInvalidToken, MaxTokenBytes)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return header, fmt.Errorf("%w: malformed", ErrInvalidToken)
	}
	var payload struct {
		Iss any `json:"iss"`
	}
	if err := decodeSegment(parts[1], &payload); err != nil {
		return header, fmt.Errorf("%w: malformed payload", ErrInvalidToken)
	}
	if iss, ok := payload.Iss.(string); !ok || iss != v.issuer {
		return header, fmt.Errorf("%w: issuer mismatch", ErrInvalidToken)
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return header, fmt.Errorf("%w: malformed header", ErrInvalidToken)
	}
	if !slices.Contains(v.algorithms, header.Alg) {
		return header, fmt.Errorf("%w: algorithm %q not allowed", ErrInvalidToken, truncate(header.Alg))
	}
	return header, nil
}

func decodeSegment(segment string, into any) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func validateIssuer(issuer string) error {
	if strings.TrimSpace(issuer) == "" {
		return fmt.Errorf("gooidc: issuer is required: %w", sdk.ErrInvalidInput)
	}
	u, err := url.Parse(issuer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("gooidc: issuer %q must be an absolute http(s) URL: %w", issuer, sdk.ErrInvalidInput)
	}
	return nil
}

// validateJWKSURL requires https, admitting plain http only for loopback
// (local dev issuers, tests): the key set is where trust comes from.
func validateJWKSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("gooidc: JWKS URL %q must be absolute: %w", raw, sdk.ErrInvalidInput)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("gooidc: JWKS URL %q must use https (http is allowed for loopback only): %w", raw, sdk.ErrInvalidInput)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func discoverJWKSURL(ctx context.Context, client *http.Client, issuer string) (string, error) {
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
	if err != nil {
		return "", fmt.Errorf("gooidc: discovery for %q: %w", issuer, err)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&doc); err != nil || doc.JWKSURI == "" {
		return "", fmt.Errorf("gooidc: discovery for %q: no jwks_uri: %w", issuer, sdk.ErrInvalidInput)
	}
	return doc.JWKSURI, nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: FetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// truncate bounds attacker-controlled header strings before they reach errors or logs.
func truncate(s string) string {
	const limit = 64
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
