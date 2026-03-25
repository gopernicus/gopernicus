package web

import "net/http"

// CORSMiddleware returns middleware that handles CORS headers and OPTIONS requests.
func CORSMiddleware(origins []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			var allowedOrigin string
			var useWildcard bool

			for _, o := range origins {
				if o == "*" {
					if origin != "" {
						allowedOrigin = origin
					} else {
						allowedOrigin = "*"
						useWildcard = true
					}
					break
				}
				if o == origin {
					allowedOrigin = origin
					break
				}
			}

			if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)

				if !useWildcard {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}

				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// DefaultHeadersMiddleware returns middleware that sets default response headers.
func DefaultHeadersMiddleware(headers map[string]string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range headers {
				w.Header().Set(k, v)
			}
			next.ServeHTTP(w, r)
		})
	}
}
