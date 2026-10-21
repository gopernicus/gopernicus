// Package oidctest is a local OpenID Connect issuer for tests and development:
// it serves a discovery document and a JWKS and mints signed JWTs, so a
// gooidc.Verifier can be exercised over real HTTP with no provider account.
//
// Start runs it on an httptest server whose URL is the issuer. New builds one
// for a caller-chosen issuer string; the caller serves Handler at that URL.
// MintAs signs under a second issuer string with the same keys, which lets a
// host stand in for a provider's session cookie (a distinct iss for the same
// sub) by wiring a second verifier with WithJWKSURL(JWKSURL()).
//
// It imports only the standard library. It is never a production issuer: keys
// live in memory and are regenerated on every construction.
package oidctest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAudience is the aud Mint adds when the claims carry none.
	DefaultAudience = "oidctest"

	// DefaultTTL is the lifetime Mint gives a token without exp.
	DefaultTTL = 10 * time.Minute

	// DiscoveryPath and JWKSPath are served by Handler, relative to the issuer.
	DiscoveryPath = "/.well-known/openid-configuration"
	JWKSPath      = "/.well-known/jwks.json"
)

var (
	// ErrUnsupportedAlgorithm is returned for an algorithm other than RS256 or ES256.
	ErrUnsupportedAlgorithm = errors.New("oidctest: algorithm must be RS256 or ES256")

	// Omit, as a claim value, removes that claim and suppresses its default,
	// e.g. {"exp": Omit} mints a token without exp.
	Omit = omit{}
)

type omit struct{}

// Issuer is a local OIDC issuer. It is safe for concurrent use.
type Issuer struct {
	issuer   string
	audience string
	alg      string
	server   *httptest.Server

	mu           sync.Mutex
	keys         []*signingKey // published; the last one signs
	unpublished  *signingKey   // signs MintWithUnpublishedKey tokens
	jwksDown     bool
	jwksRequests int
}

// Option configures an Issuer.
type Option func(*Issuer)

type signingKey struct {
	kid  string
	priv crypto.Signer
}

// WithAlgorithm selects RS256 (default) or ES256.
func WithAlgorithm(alg string) Option {
	return func(i *Issuer) { i.alg = alg }
}

// WithAudience sets the aud Mint adds when the claims carry none.
func WithAudience(audience string) Option {
	return func(i *Issuer) { i.audience = audience }
}

// New returns an Issuer for issuer without serving it; mount Handler so that
// issuer+JWKSPath reaches it.
func New(issuer string, opts ...Option) (*Issuer, error) {
	i := &Issuer{issuer: strings.TrimSuffix(issuer, "/"), audience: DefaultAudience, alg: "RS256"}
	for _, opt := range opts {
		if opt != nil {
			opt(i)
		}
	}
	if i.issuer == "" {
		return nil, errors.New("oidctest: issuer is required")
	}
	if i.alg != "RS256" && i.alg != "ES256" {
		return nil, ErrUnsupportedAlgorithm
	}
	key, err := i.generate()
	if err != nil {
		return nil, err
	}
	unpublished, err := i.generate()
	if err != nil {
		return nil, err
	}
	i.keys = []*signingKey{key}
	i.unpublished = unpublished
	return i, nil
}

// Start serves a new Issuer on a loopback httptest server; the server URL is
// the issuer. Close it when done.
func Start(opts ...Option) (*Issuer, error) {
	server := httptest.NewUnstartedServer(nil)
	server.Start()
	i, err := New(server.URL, opts...)
	if err != nil {
		server.Close()
		return nil, err
	}
	server.Config.Handler = i.Handler()
	i.server = server
	return i, nil
}

// Close stops the server Start created; it is a no-op for New.
func (i *Issuer) Close() {
	if i.server != nil {
		i.server.Close()
	}
}

// Issuer returns the issuer string (the server URL for Start).
func (i *Issuer) Issuer() string { return i.issuer }

// Audience returns the aud Mint adds by default.
func (i *Issuer) Audience() string { return i.audience }

// Algorithm returns the signing algorithm.
func (i *Issuer) Algorithm() string { return i.alg }

// JWKSURL returns the key set URL.
func (i *Issuer) JWKSURL() string { return i.issuer + JWKSPath }

// KeyID returns the kid of the current signing key.
func (i *Issuer) KeyID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.keys[len(i.keys)-1].kid
}

// Handler serves the discovery document and the JWKS.
func (i *Issuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+DiscoveryPath, i.serveDiscovery)
	mux.HandleFunc("GET "+JWKSPath, i.serveJWKS)
	return mux
}

// Mint signs claims with the current key. It adds iss (Issuer), aud
// (Audience), iat (now) and exp (now + DefaultTTL) only when absent; it never
// overrides a claim the caller set. A claim set to Omit is left out.
func (i *Issuer) Mint(claims map[string]any) (string, error) {
	return i.MintAs(i.issuer, claims)
}

// MintAs is Mint with issuer as the default iss: the same keys under a second
// issuer string.
func (i *Issuer) MintAs(issuer string, claims map[string]any) (string, error) {
	i.mu.Lock()
	key := i.keys[len(i.keys)-1]
	i.mu.Unlock()
	return i.sign(key, issuer, claims)
}

// MintWithUnpublishedKey signs with a key the JWKS never publishes, under kid
// (a random kid when empty). Verifiers must reject it.
func (i *Issuer) MintWithUnpublishedKey(kid string, claims map[string]any) (string, error) {
	if kid == "" {
		kid = randomKid()
	}
	return i.sign(&signingKey{kid: kid, priv: i.unpublished.priv}, i.issuer, claims)
}

// MintUnsigned returns an alg "none" token with an empty signature — the
// shape an auth emulator issues. Verifiers must reject it.
func (i *Issuer) MintUnsigned(claims map[string]any) (string, error) {
	header, err := encodeJSON(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := encodeJSON(i.withDefaults(i.issuer, claims))
	if err != nil {
		return "", err
	}
	return header + "." + payload + ".", nil
}

// Rotate publishes a new key and signs with it from now on. Earlier keys stay
// published until Retire.
func (i *Issuer) Rotate() (string, error) {
	key, err := i.generate()
	if err != nil {
		return "", err
	}
	i.mu.Lock()
	i.keys = append(i.keys, key)
	i.mu.Unlock()
	return key.kid, nil
}

// Retire unpublishes kid. Retiring the current signing key is refused.
func (i *Issuer) Retire(kid string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n, key := range i.keys {
		if key.kid != kid {
			continue
		}
		if n == len(i.keys)-1 {
			return errors.New("oidctest: cannot retire the current signing key; Rotate first")
		}
		i.keys = append(i.keys[:n], i.keys[n+1:]...)
		return nil
	}
	return fmt.Errorf("oidctest: unknown kid %q", kid)
}

// SetJWKSAvailable toggles an outage: while false the JWKS answers 503.
func (i *Issuer) SetJWKSAvailable(available bool) {
	i.mu.Lock()
	i.jwksDown = !available
	i.mu.Unlock()
}

// JWKSRequests counts JWKS requests served, including outage answers.
func (i *Issuer) JWKSRequests() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.jwksRequests
}

func (i *Issuer) serveDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                i.issuer,
		"jwks_uri":                              i.JWKSURL(),
		"authorization_endpoint":                i.issuer + "/authorize",
		"token_endpoint":                        i.issuer + "/token",
		"response_types_supported":              []string{"id_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{i.alg},
	})
}

func (i *Issuer) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	i.mu.Lock()
	i.jwksRequests++
	down := i.jwksDown
	keys := make([]map[string]string, 0, len(i.keys))
	for _, key := range i.keys {
		keys = append(keys, publicJWK(key, i.alg))
	}
	i.mu.Unlock()
	if down {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "jwks unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (i *Issuer) withDefaults(issuer string, claims map[string]any) map[string]any {
	out := maps.Clone(claims)
	if out == nil {
		out = make(map[string]any)
	}
	now := time.Now()
	setDefault(out, "iss", issuer)
	setDefault(out, "aud", i.audience)
	setDefault(out, "iat", now.Unix())
	setDefault(out, "exp", now.Add(DefaultTTL).Unix())
	maps.DeleteFunc(out, func(_ string, v any) bool { return v == Omit })
	return out
}

func (i *Issuer) sign(key *signingKey, issuer string, claims map[string]any) (string, error) {
	header, err := encodeJSON(map[string]any{"alg": i.alg, "typ": "JWT", "kid": key.kid})
	if err != nil {
		return "", err
	}
	payload, err := encodeJSON(i.withDefaults(issuer, claims))
	if err != nil {
		return "", err
	}
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))

	var sig []byte
	switch priv := key.priv.(type) {
	case *rsa.PrivateKey:
		sig, err = rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
		if err != nil {
			return "", fmt.Errorf("oidctest: sign: %w", err)
		}
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
		if err != nil {
			return "", fmt.Errorf("oidctest: sign: %w", err)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	default:
		return "", ErrUnsupportedAlgorithm
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (i *Issuer) generate() (*signingKey, error) {
	var priv crypto.Signer
	var err error
	switch i.alg {
	case "RS256":
		priv, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ES256":
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		return nil, ErrUnsupportedAlgorithm
	}
	if err != nil {
		return nil, fmt.Errorf("oidctest: generate key: %w", err)
	}
	return &signingKey{kid: randomKid(), priv: priv}, nil
}

func publicJWK(key *signingKey, alg string) map[string]string {
	enc := base64.RawURLEncoding.EncodeToString
	switch priv := key.priv.(type) {
	case *rsa.PrivateKey:
		return map[string]string{
			"kty": "RSA", "use": "sig", "alg": alg, "kid": key.kid,
			"n": enc(priv.N.Bytes()),
			"e": enc(big.NewInt(int64(priv.E)).Bytes()),
		}
	case *ecdsa.PrivateKey:
		point, _ := priv.PublicKey.Bytes() // 0x04 || X || Y
		return map[string]string{
			"kty": "EC", "use": "sig", "alg": alg, "kid": key.kid, "crv": "P-256",
			"x": enc(point[1:33]),
			"y": enc(point[33:]),
		}
	}
	return nil
}

func setDefault(claims map[string]any, name string, value any) {
	if _, ok := claims[name]; !ok {
		claims[name] = value
	}
}

func encodeJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("oidctest: encode: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomKid() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
