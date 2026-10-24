package gooidc

import (
	"log/slog"
	"net/http"
	"slices"
)

// Option configures construction of a Verifier. Options apply in order.
type Option func(*config)

type config struct {
	jwksURL        string
	audiences      []string
	algorithms     []string
	httpClient     *http.Client
	logger         *slog.Logger
	insecureIssuer bool
}

// WithJWKSURL pins the key set URL and skips discovery, so New performs no
// network I/O. It must be https, or http on loopback.
func WithJWKSURL(jwksURL string) Option {
	return func(cfg *config) { cfg.jwksURL = jwksURL }
}

// WithInsecureIssuer lets New discover a plain-http issuer on a non-loopback
// host (a container hostname in development), logging one WARN. Discovery and
// the key set it names then travel unauthenticated, so never set it in
// production. It is not needed with WithJWKSURL, which fetches nothing from
// the issuer, or for http on loopback.
func WithInsecureIssuer() Option {
	return func(cfg *config) { cfg.insecureIssuer = true }
}

// WithAudiences sets the accepted audiences; a token is accepted when its aud
// contains any of them. Required. Repeated calls replace the list.
func WithAudiences(audiences ...string) Option {
	snapshot := slices.Clone(audiences)
	return func(cfg *config) { cfg.audiences = slices.Clone(snapshot) }
}

// WithAlgorithms replaces the algorithm allow-list (default RS256, ES256).
// Only asymmetric JWS algorithms are accepted; "none" and HMAC fail New.
func WithAlgorithms(algorithms ...string) Option {
	snapshot := slices.Clone(algorithms)
	return func(cfg *config) { cfg.algorithms = slices.Clone(snapshot) }
}

// WithHTTPClient sets the client for discovery and JWKS fetches. The default
// has a FetchTimeout timeout and refuses redirects. Each JWKS fetch is also
// bounded by FetchTimeout regardless of the client.
func WithHTTPClient(client *http.Client) Option {
	return func(cfg *config) { cfg.httpClient = client }
}

// WithLogger selects the logger for fetch failures and aggregated unknown-kid
// rejections. Nil uses slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) { cfg.logger = logger }
}
