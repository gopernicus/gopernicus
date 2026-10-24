package web

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIPContextKey is the unexported context key under which TrustProxies
// stashes the resolved client IP. It is read only through ClientIP.
type clientIPContextKey struct{}

// TrustProxiesConfig is the input to [TrustProxiesWithConfig].
type TrustProxiesConfig struct {
	// TrustedProxyCount is the number of trusted reverse proxies in front of the
	// server: 0 trusts only RemoteAddr; 1 for a single load balancer; 2 for a
	// CDN in front of a load balancer, and so on. It names how many rightmost
	// X-Forwarded-For hops the server wrote itself and therefore trusts.
	TrustedProxyCount int

	// TrustRealIPHeader admits X-Real-IP as the client IP for a proxy that sets
	// it instead of X-Forwarded-For. It is consulted only when
	// TrustedProxyCount >= 1 AND the request carries no X-Forwarded-For, and
	// only when its value parses as an IP. Enable it only when the trusted
	// proxy overwrites X-Real-IP on every request: a proxy that forwards a
	// client-sent X-Real-IP lets the client choose its own attributed IP.
	TrustRealIPHeader bool
}

// TrustProxies is [TrustProxiesWithConfig] with only TrustedProxyCount set:
// X-Forwarded-For is resolved rightmost-minus-N and X-Real-IP is ignored.
func TrustProxies(trustedProxyCount int) Middleware {
	return TrustProxiesWithConfig(TrustProxiesConfig{TrustedProxyCount: trustedProxyCount})
}

// TrustProxiesWithConfig returns middleware that resolves the real client IP
// from proxy headers using the rightmost-minus-N X-Forwarded-For algorithm and
// stashes it on the request context (read via ClientIP).
//
// The rightmost-minus-N rule is the correct anti-spoofing algorithm: a client
// can append anything to the left of X-Forwarded-For, so only the N rightmost
// hops (written by the trusted proxies) are believable. The client IP is the
// Nth entry counted from the right; the index is clamped at 0 so a header with
// fewer hops than trusted proxies yields the leftmost entry rather than
// underflowing. Trusting a leftmost hop (or any header at count 0) lets a
// client forge its own attributed IP to rotate rate-limit keys or poison audit
// rows — which is exactly what this replaces.
//
// The chosen hop must parse as an IP (net/netip; a bracketed or ported form is
// accepted, an IPv6 zone is dropped, an IPv4-mapped IPv6 address is unmapped).
// A hop that does not parse attributes the peer (RemoteAddr), never the
// unparsed text. X-Real-IP is read only under
// [TrustProxiesConfig.TrustRealIPHeader].
func TrustProxiesWithConfig(cfg TrustProxiesConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, cfg)
			ctx := context.WithValue(r.Context(), clientIPContextKey{}, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIP returns the TrustProxies-resolved client IP stashed on the context,
// reporting false when TrustProxies did not run for the request.
func ClientIP(ctx context.Context) (string, bool) {
	ip, ok := ctx.Value(clientIPContextKey{}).(string)
	return ip, ok
}

// resolveClientIP extracts the real client IP using the rightmost-minus-N
// strategy on the X-Forwarded-For header: with N trusted proxies the Nth entry
// from the right is the client IP, because each trusted proxy appended exactly
// one hop.
//
// Given X-Forwarded-For: forged, clientIP — a client that sent a forged header
// through one trusted proxy, which appended the real client IP:
// With TrustedProxyCount=1: returns clientIP (the rightmost entry)
// With TrustedProxyCount=2: returns forged (never set the count higher than
// the proxies actually in front)
// With TrustedProxyCount=0: returns RemoteAddr (don't trust any header)
func resolveClientIP(r *http.Request, cfg TrustProxiesConfig) string {
	if cfg.TrustedProxyCount <= 0 {
		return extractRemoteAddrIP(r)
	}

	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if xff == "" {
		if cfg.TrustRealIPHeader {
			if ip, ok := parseForwardedIP(r.Header.Get("X-Real-IP")); ok {
				return ip
			}
		}
		return extractRemoteAddrIP(r)
	}

	parts := strings.Split(xff, ",")
	// Rightmost-minus-N: the Nth entry from the right is the client IP.
	idx := max(len(parts)-cfg.TrustedProxyCount, 0)
	if ip, ok := parseForwardedIP(parts[idx]); ok {
		return ip
	}
	return extractRemoteAddrIP(r)
}

// parseForwardedIP parses one forwarded hop as an IP address, accepting the
// ip:port and [ipv6]:port forms, and returns its canonical text.
func parseForwardedIP(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	addr, err := netip.ParseAddr(value)
	if err != nil {
		addrPort, portErr := netip.ParseAddrPort(value)
		if portErr != nil {
			return "", false
		}
		addr = addrPort.Addr()
	}
	return addr.WithZone("").Unmap().String(), true
}

// extractRemoteAddrIP returns the IP portion of RemoteAddr, stripping the port.
// A RemoteAddr with no port (malformed) is returned verbatim.
func extractRemoteAddrIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
