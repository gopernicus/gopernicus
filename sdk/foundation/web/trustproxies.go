package web

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// clientIPContextKey is the unexported context key under which TrustProxies
// stashes the resolved client IP. It is read only through ClientIP.
type clientIPContextKey struct{}

// TrustProxies returns middleware that resolves the real client IP from proxy
// headers using the rightmost-minus-N X-Forwarded-For algorithm and stashes it
// on the request context (read via ClientIP).
//
// trustedProxyCount is the number of trusted reverse proxies in front of the
// server: 0 trusts only RemoteAddr; 1 for a single load balancer; 2 for a CDN
// in front of a load balancer, and so on. It names how many rightmost
// X-Forwarded-For hops the server wrote itself and therefore trusts.
//
// The rightmost-minus-N rule is the correct anti-spoofing algorithm: a client
// can append anything to the left of X-Forwarded-For, so only the N rightmost
// hops (written by the trusted proxies) are believable. The client IP is the
// Nth entry counted from the right; the index is clamped at 0 so a header with
// fewer hops than trusted proxies yields the leftmost entry rather than
// underflowing. Trusting a leftmost hop (or any header at count 0) lets a
// client forge its own attributed IP to rotate rate-limit keys or poison audit
// rows — which is exactly what this replaces.
func TrustProxies(trustedProxyCount int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, trustedProxyCount)
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
// With trustedProxyCount=1: returns clientIP (the rightmost entry)
// With trustedProxyCount=2: returns forged (never set the count higher than
// the proxies actually in front)
// With trustedProxyCount=0: returns RemoteAddr (don't trust any header)
func resolveClientIP(r *http.Request, trustedProxies int) string {
	if trustedProxies <= 0 {
		return extractRemoteAddrIP(r)
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		// Fall back to X-Real-IP if set by a single proxy.
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
		return extractRemoteAddrIP(r)
	}

	parts := strings.Split(xff, ",")
	// Rightmost-minus-N: the Nth entry from the right is the client IP.
	idx := max(len(parts)-trustedProxies, 0)
	return strings.TrimSpace(parts[idx])
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
