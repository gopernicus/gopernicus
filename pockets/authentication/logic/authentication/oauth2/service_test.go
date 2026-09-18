package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

type constructorRepository struct{ Repository }
type constructorUsers struct{ UserReader }
type constructorSessions struct{ SessionReader }
type constructorSigner struct{ cryptids.JWTSigner }
type constructorVerifier struct{ TokenVerifier }

type clientResolverFunc func(context.Context, string) (Client, error)

func (f clientResolverFunc) Resolve(ctx context.Context, id string) (Client, error) {
	return f(ctx, id)
}

func protocolConfig() Config {
	return Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
		ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}}
}

func constructorService(t *testing.T, cfg Config, mode environment.Mode, opts ...Option) (*Service, error) {
	t.Helper()
	return New(Repositories{OAuth2: &constructorRepository{}, Users: &constructorUsers{}, Sessions: &constructorSessions{}},
		&constructorSigner{}, clientResolverFunc(func(context.Context, string) (Client, error) { return Client{}, ErrInvalidClient }),
		&constructorVerifier{}, mode, cfg, opts...)
}

func TestConstructorRejectsUnsafeProtocolPostures(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"missing issuer":          func(c *Config) { c.Issuer = "" },
		"same audience":           func(c *Config) { c.APIResource = c.MCPResource },
		"insecure issuer":         func(c *Config) { c.Issuer = "http://issuer.example" },
		"userinfo":                func(c *Config) { c.Issuer = "https://user@issuer.example" },
		"fragment":                func(c *Config) { c.MCPResource += "#fragment" },
		"empty fragment":          func(c *Config) { c.Issuer += "#" },
		"ambiguous host":          func(c *Config) { c.Issuer = "https://ISSUER.example" },
		"missing secret":          func(c *Config) { c.ConfidentialSecretHashes = nil },
		"empty secret":            func(c *Config) { c.ConfidentialSecretHashes = [][32]byte{sha256.Sum256(nil)} },
		"long code":               func(c *Config) { c.CodeTTL = time.Hour },
		"long access":             func(c *Config) { c.AccessTTL = time.Hour },
		"negative lifetime":       func(c *Config) { c.SessionTTL = -time.Second },
		"production local option": func(c *Config) { c.AllowLocalHTTP = true },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := protocolConfig()
			change(&cfg)
			if _, err := constructorService(t, cfg, environment.ModeProduction); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("unsafe config accepted: %v", err)
			}
		})
	}
	cfg := protocolConfig()
	cfg.Issuer, cfg.MCPResource, cfg.APIResource, cfg.AllowLocalHTTP = "http://localhost:8082", "http://127.0.0.1:8083/mcp", "http://[::1]:8084/api", true
	if _, err := constructorService(t, cfg, environment.ModeDevelopment); err != nil {
		t.Fatalf("explicit loopback development: %v", err)
	}
	cfg.MCPResource = "http://private.example/mcp"
	if _, err := constructorService(t, cfg, environment.ModeDevelopment); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nonloopback insecure resource accepted: %v", err)
	}
	if _, err := constructorService(t, protocolConfig(), environment.ModeProduction, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
	if _, err := constructorService(t, protocolConfig(), environment.ModeProduction, WithClock(nil)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil clock: %v", err)
	}
	var typedNil *constructorRepository
	if _, err := New(Repositories{OAuth2: typedNil, Users: &constructorUsers{}, Sessions: &constructorSessions{}}, &constructorSigner{}, clientResolverFunc(nil), &constructorVerifier{}, environment.ModeProduction, protocolConfig()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil dependency: %v", err)
	}
}

func TestConfidentialProofCannotCrossServicesOrMutateConfiguredSecrets(t *testing.T) {
	cfg := protocolConfig()
	a, err := constructorService(t, cfg, environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	b, err := constructorService(t, cfg, environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfidentialSecretHashes[0] = sha256.Sum256([]byte("replacement"))
	proof, err := a.AuthenticateClient("mcp", "a high entropy confidential test secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ id, secret string }{{"public", "a high entropy confidential test secret"}, {"mcp", "replacement"}, {"mcp", ""}} {
		if _, err := a.AuthenticateClient(bad.id, bad.secret); !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("bad confidential credentials accepted: %v", err)
		}
	}
	if _, err := b.Introspect(t.Context(), proof, "token"); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("cross-service proof accepted: %v", err)
	}
	if _, err := a.Exchange(t.Context(), ClientProof{}, ExchangeRequest{}); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("zero proof accepted: %v", err)
	}
}

func TestAuthorizationValidationBeforeAnyIdentityLookup(t *testing.T) {
	svc, err := constructorService(t, protocolConfig(), environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(verifier))
	good := AuthorizationRequest{ResponseType: "code", ClientID: "https://client.example/metadata", RedirectURI: "https://client.example/callback", Resource: svc.config.MCPResource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), CodeChallengeMethod: "S256"}
	calls := 0
	svc.resolver = clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		calls++
		return Client{ID: id, Name: "Client", RedirectURIs: []string{good.RedirectURI}, ExpiresAt: time.Time{}}, nil
	})
	// An authoritative no-cache metadata response may have no reusable lifetime.
	if _, err := svc.validateAuthorization(t.Context(), good); err != nil {
		t.Fatalf("fresh no-cache metadata rejected: %v", err)
	}
	for name, mutate := range map[string]func(*AuthorizationRequest){
		"plain PKCE":        func(r *AuthorizationRequest) { r.CodeChallengeMethod = "plain" },
		"implicit":          func(r *AuthorizationRequest) { r.ResponseType = "token" },
		"missing challenge": func(r *AuthorizationRequest) { r.CodeChallenge = "" },
		"padded challenge":  func(r *AuthorizationRequest) { r.CodeChallenge += "=" },
		"scope":             func(r *AuthorizationRequest) { r.Scope = "admin" },
		"API audience":      func(r *AuthorizationRequest) { r.Resource = svc.config.APIResource },
		"redirect suffix":   func(r *AuthorizationRequest) { r.RedirectURI += "?other=true" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := good
			mutate(&bad)
			if _, err := svc.PrepareAuthorization(t.Context(), bad, "u", "w"); err == nil {
				t.Fatal("bad request reached identity/store boundary")
			}
		})
	}
	if calls < 2 {
		t.Fatal("client metadata was not checked")
	}
	if !matchesPKCE(verifier, good.CodeChallenge) || matchesPKCE(strings.Repeat("b", 43), good.CodeChallenge) || matchesPKCE(strings.Repeat("a", 42), good.CodeChallenge) {
		t.Fatal("PKCE verifier validation")
	}
}
