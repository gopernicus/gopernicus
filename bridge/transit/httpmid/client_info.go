package httpmid

import (
	"net/http"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
)

// ClientInfo returns middleware that injects [authentication.ClientInfo]
// into the request context for security event logging.
//
// IP resolution: uses [GetClientIP] if [TrustProxies] has already run,
// otherwise falls back to the IP portion of [http.Request.RemoteAddr].
// Wire TrustProxies before ClientInfo when running behind a reverse proxy.
//
// User-Agent: read directly from the request header.
//
// Example wiring (global middleware stack):
//
//	handler.Use(
//	    httpmid.TrustProxies(1),     // resolve real IP first
//	    httpmid.ClientInfo(),         // then inject into auth context
//	    httpmid.Logger(log),
//	)
func ClientInfo() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := GetClientIP(r.Context())
			if ip == "" {
				ip = extractRemoteAddrIP(r)
			}

			ctx := authentication.WithClientInfo(r.Context(), authentication.ClientInfo{
				IPAddress: ip,
				UserAgent: r.UserAgent(),
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
