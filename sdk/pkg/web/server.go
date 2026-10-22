package web

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// originFallbackHost stands in for a host that names no specific interface,
	// so a bind-all server never derives an origin like "http://:8080".
	originFallbackHost = "localhost"

	// bindAllHost is the IPv4 bind-all address, a listen address rather than a
	// reachable origin host.
	bindAllHost = "0.0.0.0"
)

// ValidateProduction errors. Each names PUBLIC_BASE_URL, the ServerConfig env
// key, and none echoes the configured value, which may carry credentials.
var (
	// ErrPublicBaseURLRequired is returned when PublicBaseURL is unset, so
	// Origin would fall back to the plain-HTTP listen address.
	ErrPublicBaseURLRequired = errors.New("web: PUBLIC_BASE_URL is required in production")
	// ErrPublicBaseURLInsecure is returned when PublicBaseURL is not an https URL.
	ErrPublicBaseURLInsecure = errors.New("web: PUBLIC_BASE_URL must be an https origin in production")
	// ErrPublicBaseURLMalformed is returned when PublicBaseURL is not a bare
	// origin: no host, or a userinfo, query, fragment, or path other than "/".
	ErrPublicBaseURLMalformed = errors.New("web: PUBLIC_BASE_URL must be a bare https origin (scheme and host, optional port)")
)

// ServerConfig holds host-owned HTTP configuration for Run or HTTPServer.
type ServerConfig struct {
	Host            string        `env:"HOST" default:"localhost"`
	Port            string        `env:"PORT" default:"8080"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT" default:"15s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" default:"15s"`
	IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" default:"120s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" default:"10s"`

	// TrustedProxyCount is the number of trusted reverse proxies in front of
	// the server, consumed by TrustProxies(cfg.TrustedProxyCount). Zero or less
	// means trust no proxy header and attribute RemoteAddr. HTTPServer ignores
	// it.
	TrustedProxyCount int `env:"TRUSTED_PROXY_COUNT"`

	// PublicBaseURL is the externally visible origin of this server — the value
	// browsers and identity providers see, which differs from the listen
	// address behind a proxy or tunnel. It MUST be scheme-qualified and
	// path-free ("https://app.example.com"), unlike
	// authentication.Config.PublicAuthBaseURL, which carries a path
	// ("https://app.example.com/auth/magic"). Read it through Origin, which
	// documents the contract rather than validating it: a malformed value lands
	// in an exact-match allowlist and simply never matches, so rejecting it is
	// a host concern: a production host calls ValidateProduction. HTTPServer
	// ignores it.
	PublicBaseURL string `env:"PUBLIC_BASE_URL"`
}

// Address returns the host:port listen address.
func (c ServerConfig) Address() string {
	return net.JoinHostPort(c.Host, c.Port)
}

// Origin returns the externally visible origin: PublicBaseURL with trailing
// slashes trimmed when set, otherwise "http://" plus the listen address, with
// "localhost" substituted for an empty or bind-all (0.0.0.0) host. Hosts derive
// OAuth callback bases, allowed-origin fallbacks, and magic-link bases from it.
//
// Origin documents the scheme-qualified, path-free contract of PublicBaseURL;
// it does not validate it.
func (c ServerConfig) Origin() string {
	if base := strings.TrimRight(c.PublicBaseURL, "/"); base != "" {
		return base
	}

	if c.Host == "" || c.Host == bindAllHost {
		c.Host = originFallbackHost
	}

	return "http://" + c.Address()
}

// ValidateProduction checks the configuration a production host must not
// leave to Origin's development fallback: PublicBaseURL must be set
// (ErrPublicBaseURLRequired), use https (ErrPublicBaseURLInsecure), and be a
// bare origin with a host and no userinfo, query, fragment, or path other
// than "/" (ErrPublicBaseURLMalformed).
//
// web takes no runtime mode, so the host gates the call on its own:
//
//	if mode.IsProduction() {
//		if err := srv.ValidateProduction(); err != nil {
//			return err
//		}
//	}
func (c ServerConfig) ValidateProduction() error {
	raw := c.PublicBaseURL
	if strings.TrimRight(raw, "/") == "" {
		return ErrPublicBaseURLRequired
	}
	// A parse error quotes its input, so it is never wrapped.
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: the value does not parse as a URL", ErrPublicBaseURLMalformed)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrPublicBaseURLInsecure, u.Scheme)
	}
	switch {
	case u.User != nil:
		return fmt.Errorf("%w: it carries userinfo", ErrPublicBaseURLMalformed)
	case u.Host == "" || u.Opaque != "":
		return fmt.Errorf("%w: it names no host", ErrPublicBaseURLMalformed)
	case u.RawQuery != "" || u.ForceQuery:
		return fmt.Errorf("%w: it carries a query", ErrPublicBaseURLMalformed)
	case u.Fragment != "" || strings.Contains(raw, "#"):
		return fmt.Errorf("%w: it carries a fragment", ErrPublicBaseURLMalformed)
	case u.EscapedPath() != "" && u.EscapedPath() != "/":
		return fmt.Errorf("%w: it carries a path", ErrPublicBaseURLMalformed)
	}
	return nil
}

// HTTPServer builds an *http.Server from the config and handler. It ignores
// TrustedProxyCount and PublicBaseURL, which are consumed by TrustProxies and
// Origin.
func (c ServerConfig) HTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         c.Address(),
		Handler:      handler,
		ReadTimeout:  c.ReadTimeout,
		WriteTimeout: c.WriteTimeout,
		IdleTimeout:  c.IdleTimeout,
	}
}
