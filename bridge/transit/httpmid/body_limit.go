package httpmid

import (
	"net/http"
)

// Common body size limits.
const (
	DefaultBodySize = 1 << 20   // 1 MB
	SmallBodySize   = 64 << 10  // 64 KB
	LargeBodySize   = 50 << 20  // 50 MB
	ExtraLargeSize  = 200 << 20 // 200 MB
)

// MaxBodySize returns middleware that limits the request body to the given
// number of bytes. Returns 413 Payload Too Large if exceeded.
func MaxBodySize(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
