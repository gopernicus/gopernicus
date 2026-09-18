// Package oauth2fixture supplies an explicitly local public OAuth client. It is
// a proof-host trust fixture, not a network metadata resolver for production.
package oauth2fixture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

type Resolver struct {
	client oauth2.Client
	config oauth2.Config
	secret string
}

func New(baseURL string) (*Resolver, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.Host != strings.ToLower(u.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(baseURL, "#") {
		return nil, fmt.Errorf("OAuth fixture requires a local HTTP origin: %w", sdk.ErrInvalidInput)
	}
	ip, _ := netip.ParseAddr(u.Hostname())
	if u.Hostname() != "localhost" && !ip.IsLoopback() {
		return nil, fmt.Errorf("OAuth fixture requires loopback: %w", sdk.ErrInvalidInput)
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, err
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	return &Resolver{
		client: oauth2.Client{ID: baseURL + "/oauth-demo/client.json", Name: "Local MCP demo", RedirectURIs: []string{baseURL + "/oauth-demo/callback"}},
		config: oauth2.Config{Issuer: baseURL, MCPResource: baseURL + "/oauth-demo/mcp", APIResource: baseURL + "/oauth-demo/api",
			ConfidentialClientID: "oauth-demo-mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte(secret))}, AllowLocalHTTP: true},
		secret: secret,
	}, nil
}

func (r *Resolver) Resolve(_ context.Context, clientID string) (oauth2.Client, error) {
	if clientID != r.client.ID {
		return oauth2.Client{}, oauth2.ErrInvalidClient
	}
	client := r.Client()
	client.ExpiresAt = time.Now().Add(time.Minute)
	return client, nil
}

func (r *Resolver) Client() oauth2.Client {
	client := r.client
	client.RedirectURIs = slices.Clone(client.RedirectURIs)
	return client
}

func (r *Resolver) Config() oauth2.Config {
	config := r.config
	config.ConfidentialSecretHashes = slices.Clone(config.ConfidentialSecretHashes)
	return config
}

func (r *Resolver) ConfidentialSecret() string { return r.secret }
