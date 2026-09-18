// Package oauth2 supplies restricted client-metadata retrieval for the
// authentication pocket's authorization server.
package oauth2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	protocol "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

const maxDocumentBytes = 64 << 10

// CIMD accepts only exact client IDs supplied by the host. It never follows
// redirects, uses environment proxies, or connects to a nonpublic address.
type CIMD struct {
	allowed  map[string]bool
	client   *http.Client
	mu       sync.Mutex
	cache    map[string]protocol.Client
	inflight chan struct{}
	trust    func(context.Context, string) error
}

type CIMDOption func(*CIMD)

// WithClientTrust adds a host trust decision checked even on cached metadata.
// The fixed allowlist remains a mandatory outer boundary.
func WithClientTrust(check func(context.Context, string) error) CIMDOption {
	if check == nil {
		panic("oauth2: nil client trust check")
	}
	return func(c *CIMD) { c.trust = check }
}

// NewCIMD snapshots the production trust allowlist. An empty list is an error;
// there is no permissive default or development network bypass.
func NewCIMD(clientIDs []string, opts ...CIMDOption) (*CIMD, error) {
	if len(clientIDs) == 0 || len(clientIDs) > 128 {
		return nil, fmt.Errorf("OAuth client allowlist requires 1–128 IDs: %w", sdk.ErrInvalidInput)
	}
	allowed := make(map[string]bool, len(clientIDs))
	for _, id := range clientIDs {
		u, err := url.Parse(id)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("invalid client metadata URL: %w", sdk.ErrInvalidInput)
		}
		for _, segment := range strings.Split(u.Path, "/") {
			if segment == "." || segment == ".." {
				return nil, sdk.ErrInvalidInput
			}
		}
		allowed[id] = true
	}
	transport := &http.Transport{Proxy: nil, DialContext: publicDial, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxIdleConns: 16, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Minute, MaxResponseHeaderBytes: 16 << 10}
	c := &CIMD{allowed: allowed, cache: map[string]protocol.Client{}, inflight: make(chan struct{}, 8), client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, opt := range opts {
		if opt == nil {
			return nil, sdk.ErrInvalidInput
		}
		opt(c)
	}
	return c, nil
}

func (c *CIMD) Resolve(ctx context.Context, id string) (protocol.Client, error) {
	if !c.allowed[id] {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	if c.trust != nil {
		if err := c.trust(ctx, id); err != nil {
			if errors.Is(err, sdk.ErrUnauthorized) || errors.Is(err, sdk.ErrForbidden) || errors.Is(err, sdk.ErrInvalidInput) {
				return protocol.Client{}, protocol.ErrInvalidClient
			}
			return protocol.Client{}, err
		}
	}
	c.mu.Lock()
	cached, ok := c.cache[id]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.ExpiresAt) {
		cached.RedirectURIs = slices.Clone(cached.RedirectURIs)
		return cached, nil
	}
	select {
	case c.inflight <- struct{}{}:
		defer func() { <-c.inflight }()
	case <-ctx.Done():
		return protocol.Client{}, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return protocol.Client{}, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return protocol.Client{}, fmt.Errorf("client metadata unavailable: %w", sdk.ErrUnavailable)
	}
	defer res.Body.Close()
	media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode != http.StatusOK || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxDocumentBytes+1))
	if err != nil {
		return protocol.Client{}, sdk.ErrUnavailable
	}
	if len(body) > maxDocumentBytes {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	var doc struct {
		ID            string          `json:"client_id"`
		Name          string          `json:"client_name"`
		RedirectURIs  []string        `json:"redirect_uris"`
		AuthMethod    string          `json:"token_endpoint_auth_method"`
		GrantTypes    []string        `json:"grant_types"`
		ResponseTypes []string        `json:"response_types"`
		Secret        json.RawMessage `json:"client_secret"`
		SecretExpiry  json.RawMessage `json:"client_secret_expires_at"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.ID != id || strings.TrimSpace(doc.Name) == "" || len(doc.Name) > 200 || len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > 16 || doc.AuthMethod != "none" {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	if len(doc.Secret) != 0 || len(doc.SecretExpiry) != 0 {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	if len(doc.GrantTypes) > 0 && !slices.Contains(doc.GrantTypes, "authorization_code") {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	if len(doc.ResponseTypes) > 0 && !slices.Contains(doc.ResponseTypes, "code") {
		return protocol.Client{}, protocol.ErrInvalidClient
	}
	for _, redirect := range doc.RedirectURIs {
		u, err := url.Parse(redirect)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.TrimSpace(redirect) != redirect || len(redirect) > 2048 {
			return protocol.Client{}, protocol.ErrInvalidClient
		}
	}
	ttl := metadataTTL(res.Header)
	result := protocol.Client{ID: id, Name: doc.Name, RedirectURIs: slices.Clone(doc.RedirectURIs), ExpiresAt: time.Now().Add(ttl)}
	if ttl > 0 {
		c.mu.Lock()
		c.cache[id] = result
		c.mu.Unlock()
	}
	result.RedirectURIs = slices.Clone(result.RedirectURIs)
	return result, nil
}

func metadataTTL(headers http.Header) time.Duration {
	ttl := 10 * time.Minute
	for _, part := range strings.Split(headers.Get("Cache-Control"), ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "no-store" || part == "no-cache" {
			return 0
		}
		if value, ok := strings.CutPrefix(part, "max-age="); ok {
			n, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64)
			if err != nil || n <= 0 {
				return 0
			}
			if n < int64(ttl/time.Second) {
				ttl = time.Duration(n) * time.Second
			}
		}
	}
	if age, err := strconv.ParseInt(headers.Get("Age"), 10, 64); err == nil && age > 0 {
		if age >= int64(ttl/time.Second) {
			return 0
		}
		ttl -= time.Duration(age) * time.Second
	}
	return ttl
}

func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("metadata DNS lookup failed: %w", sdk.ErrUnavailable)
	}
	for _, ip := range addresses {
		if !publicAddress(ip) {
			return nil, fmt.Errorf("metadata destination denied: %w", sdk.ErrUnauthorized)
		}
	}
	var last error
	for _, ip := range addresses {
		conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

var deniedRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("64:ff9b::/96"),
}

func publicAddress(ip netip.Addr) bool {
	if ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range deniedRanges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
