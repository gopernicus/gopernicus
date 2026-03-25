package httpmid

import (
	"net"
	"net/http"
	"strings"
)

// TrustProxies returns middleware that resolves the real client IP from
// proxy headers using the rightmost-minus-N algorithm.
//
// trustedProxyCount is the number of trusted reverse proxies in front of
// the server. For example, if behind one load balancer, use 1. If behind
// a CDN and a load balancer, use 2. Use 0 to trust only RemoteAddr.
//
// The resolved IP is stored in context and retrieved via [GetClientIP].
func TrustProxies(trustedProxyCount int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, trustedProxyCount)
			ctx := SetClientIP(r.Context(), ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveClientIP extracts the real client IP using the rightmost-minus-N
// strategy on the X-Forwarded-For header.
//
// Given X-Forwarded-For: clientIP, proxy1, proxy2
// With trustedProxyCount=1: returns proxy1 (rightmost minus 1)
// With trustedProxyCount=2: returns clientIP (rightmost minus 2)
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
	idx := len(parts) - trustedProxies
	if idx < 0 {
		idx = 0
	}
	return strings.TrimSpace(parts[idx])
}

// extractRemoteAddrIP returns the IP portion of RemoteAddr, stripping the port.
func extractRemoteAddrIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
