package cacher

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Pages returns middleware that caches GET HTML responses by request URI for
// ttl, using the given Storer. A cache hit serves the stored bytes with
// X-Cache: HIT; a miss renders, streams, and stores the result (X-Cache: MISS).
// Only 200 text/html responses are cached. Apply it to public, cacheable routes
// only — never to per-operator admin pages.
//
// This is a capability×foundation composition (a cacher capability producing a
// web.Middleware), so it lives with the cacher semantics rather than in web:
// web stays agnostic of caching, cacher legally depends on web (capability →
// foundation).
func Pages(store Storer, ttl time.Duration) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}
			key := "page:" + r.URL.RequestURI()

			if b, ok, _ := store.Get(r.Context(), key); ok {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("X-Cache", "HIT")
				w.WriteHeader(http.StatusOK)
				w.Write(b)
				return
			}

			cap := &captureWriter{ResponseWriter: w, buf: &bytes.Buffer{}, status: http.StatusOK}
			next.ServeHTTP(cap, r)

			if cap.status == http.StatusOK && strings.HasPrefix(cap.Header().Get("Content-Type"), "text/html") {
				_ = store.Set(r.Context(), key, cap.buf.Bytes(), ttl)
			}
		})
	}
}

// captureWriter streams the response through while buffering it for caching.
type captureWriter struct {
	http.ResponseWriter
	buf    *bytes.Buffer
	status int
	wrote  bool
}

func (c *captureWriter) WriteHeader(code int) {
	if !c.wrote {
		c.status = code
		c.Header().Set("X-Cache", "MISS")
		c.wrote = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	c.buf.Write(b)
	return c.ResponseWriter.Write(b)
}
