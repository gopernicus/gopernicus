package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// RequestIDHeader is the canonical header used to carry the request ID in and out.
const RequestIDHeader = "X-Request-ID"

// CORS response defaults. corsDefaultAllowedHeaders is the compatibility
// request-header allowlist; corsDefaultExposedHeaders makes the framework's own
// request-id header readable by cross-origin JavaScript.
const (
	corsDefaultAllowedHeaders = "Accept, Content-Type, Authorization"
	corsDefaultExposedHeaders = RequestIDHeader
	corsAllowedMethods        = "GET, POST, PUT, DELETE, PATCH, OPTIONS"
	corsMaxAge                = "86400"
)

// Panics returns middleware that recovers from panics, logs the panic with a
// stack trace, and returns an HTML 500 page.
func Panics(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.ErrorContext(r.Context(), "panic recovered",
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					RecordError(w, errFromPanic(rec))
					RespondHTML(w, http.StatusInternalServerError, internalErrorHTML)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Logger returns middleware that logs each HTTP request with timing and status.
// Log levels: INFO for 2xx/3xx, WARN for 4xx, ERROR for 5xx.
//
// The wrapped writer implements RecordError so render/respond helpers can
// attach the underlying error string to the request log line.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := NewStatusRecorder(w)

			next.ServeHTTP(sw, r)

			elapsed := time.Since(start)
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", sw.status),
				slog.Duration("elapsed", elapsed),
				slog.String("remote_addr", r.RemoteAddr),
			}
			if sw.err != nil {
				attrs = append(attrs, slog.String("error", sw.err.Error()))
			}

			level := slog.LevelInfo
			switch {
			case sw.status >= 500:
				level = slog.LevelError
			case sw.status >= 400:
				level = slog.LevelWarn
			}

			log.LogAttrs(r.Context(), level, "request", attrs...)
		})
	}
}

// RequestID returns middleware that ensures every request carries a request ID.
// It reuses an inbound X-Request-ID when present, otherwise generates one,
// stashes it on the request context for the logger, and echoes it back in the
// response header.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if id == "" {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := sdk.WithRequestID(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORSConfig is the CORS policy a host declares. It carries only mechanism: the
// sdk knows no pocket's headers, so a host that needs a pocket-specific
// request header (a CSRF echo header, say) lists it in AllowedHeaders itself.
type CORSConfig struct {
	// AllowedOrigins is the origin allowlist. A "*" entry matches any origin.
	AllowedOrigins []string

	// AllowedHeaders is the request-header allowlist echoed in
	// Access-Control-Allow-Headers. A nil list selects the default
	// "Accept, Content-Type, Authorization"; a non-nil list replaces it.
	AllowedHeaders []string

	// ExposedHeaders is the response-header list echoed in
	// Access-Control-Expose-Headers. A nil list selects the default
	// X-Request-ID; an explicit empty list suppresses the header.
	ExposedHeaders []string
}

// CORSMiddleware returns middleware that applies CORS headers using an origin
// allowlist, with the default request-header allowlist and exposed-header list.
// It is the compatibility constructor for
// CORSWithConfig(CORSConfig{AllowedOrigins: origins}).
func CORSMiddleware(origins []string) Middleware {
	return CORSWithConfig(CORSConfig{AllowedOrigins: origins})
}

// CORSWithConfig returns middleware that applies the configured CORS policy and
// short-circuits genuine preflight requests with 204.
//
// Semantics: a "*" entry matches any origin and echoes the request's Origin
// back. Because a wildcard-configured origin cannot carry credentials, the
// Access-Control-Allow-Credentials header is set only for explicit
// (non-wildcard) allowlist matches. When no configured origin matches the
// request, no CORS headers are written.
//
// Vary: Origin is added for every request — matched or not — because the
// response depends on the request's Origin; an existing Vary value is extended,
// never overwritten or duplicated.
//
// Only an OPTIONS request carrying both Origin and Access-Control-Request-Method
// is treated as a preflight and answered with 204. Every other OPTIONS request
// continues to the next handler, so a host's own OPTIONS route stays reachable
// when this middleware is installed globally.
func CORSWithConfig(cfg CORSConfig) Middleware {
	origins := append([]string(nil), cfg.AllowedOrigins...)

	allowedHeaders := corsDefaultAllowedHeaders
	if cfg.AllowedHeaders != nil {
		allowedHeaders = strings.Join(cfg.AllowedHeaders, ", ")
	}
	exposedHeaders := corsDefaultExposedHeaders
	if cfg.ExposedHeaders != nil {
		exposedHeaders = strings.Join(cfg.ExposedHeaders, ", ")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			addVary(w.Header(), "Origin")

			allowedOrigin, wildcard := matchOrigin(origins, origin)
			if allowedOrigin != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", allowedOrigin)
				if !wildcard {
					h.Set("Access-Control-Allow-Credentials", "true")
				}
				h.Set("Access-Control-Allow-Methods", corsAllowedMethods)
				if allowedHeaders != "" {
					h.Set("Access-Control-Allow-Headers", allowedHeaders)
				}
				if exposedHeaders != "" {
					h.Set("Access-Control-Expose-Headers", exposedHeaders)
				}
				h.Set("Access-Control-Max-Age", corsMaxAge)
			}

			if r.Method == http.MethodOptions && origin != "" && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// DefaultHeadersMiddleware returns middleware that applies a set of default
// response headers before the handler runs. Because the defaults are written
// before next.ServeHTTP, a handler may override any of them on its own writer.
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

// NoStore is a preset of DefaultHeadersMiddleware that writes
// Cache-Control: no-store before the handler runs (a handler may still override
// on its own writer). Mount it on route groups whose responses are derived from
// a per-request grant — an authenticated API surface, where every answer must
// reflect a revocation on the very next request and nothing may be retained by
// a browser or a shared cache:
//
//	v1 := router.Group("/api/v1", requirePrincipal, web.NoStore())
//
// It is a header policy the host applies, not an identity gate and not a
// guarantee: whatever gate the host mounts beside it is what makes the group
// authenticated. Only Cache-Control is written — Pragma and Expires are HTTP/1.0
// relics no-store supersedes, and this is the exact header the authentication
// pocket's own no-store surfaces write. (The SPA index served by web's static
// handler says "no-cache, no-store, must-revalidate" instead: that is the
// index-document posture for caches that revalidate; API answers are simply
// never stored.)
func NoStore() Middleware {
	return DefaultHeadersMiddleware(map[string]string{"Cache-Control": "no-store"})
}

// matchOrigin resolves the Access-Control-Allow-Origin value for origin against
// the allowlist, reporting whether the match came from a "*" entry. An empty
// return means no configured origin matched.
func matchOrigin(origins []string, origin string) (allowed string, wildcard bool) {
	for _, o := range origins {
		if o == "*" {
			if origin != "" {
				return origin, true
			}
			return "*", true
		}
		if o == origin {
			return origin, false
		}
	}
	return "", false
}

// addVary appends dimension to the Vary header unless it is already listed,
// preserving any dimension another middleware or handler recorded first.
func addVary(h http.Header, dimension string) {
	for _, value := range h.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), dimension) {
				return
			}
		}
	}
	h.Add("Vary", dimension)
}

// newRequestID returns a 128-bit random hex string.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is catastrophic; fall back to a timestamp-free marker.
		return "req-unknown"
	}
	return hex.EncodeToString(b[:])
}

// StatusRecorder wraps http.ResponseWriter to capture the final status code and
// an optional underlying error recorded by render/respond helpers. It is the
// minimal status-capture writer shared by web.Logger and the capability
// middlewares evicted from web (sdk/capabilities/tracing, sdk/capabilities/cacher), which need the status
// the handler produced. The captured status is read via Status; RecordError and
// the underlying error feed web.Logger's access line.
type StatusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	err         error
}

// NewStatusRecorder wraps w, defaulting the captured status to 200 OK until the
// handler writes a header.
func NewStatusRecorder(w http.ResponseWriter) *StatusRecorder {
	return &StatusRecorder{ResponseWriter: w, status: http.StatusOK}
}

// Status returns the captured status code (200 until the handler sets one).
func (w *StatusRecorder) Status() int { return w.status }

func (w *StatusRecorder) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped writer for http.ResponseController.
func (w *StatusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *StatusRecorder) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// RecordError stores the underlying error so the request logger can include it.
func (w *StatusRecorder) RecordError(err error) {
	w.err = err
}

const internalErrorHTML = `<!doctype html><html><head><title>500 — internal error</title></head>` +
	`<body><h1>500</h1><p>internal error</p></body></html>`

// errFromPanic adapts a recovered panic value to an error for RecordError.
func errFromPanic(rec any) error {
	if err, ok := rec.(error); ok {
		return err
	}
	return &panicError{value: rec}
}

type panicError struct{ value any }

func (e *panicError) Error() string {
	return fmt.Sprintf("panic: %v", e.value)
}
