package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// RequestIDHeader is the canonical header used to carry the request ID in and out.
const RequestIDHeader = "X-Request-ID"

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

// CORSMiddleware returns middleware that applies CORS headers using an origin
// allowlist and short-circuits OPTIONS preflight requests with 204.
//
// Semantics: a "*" entry matches any origin and echoes the request's Origin
// back. Because a wildcard-configured origin cannot carry credentials, the
// Access-Control-Allow-Credentials header is set only for explicit
// (non-wildcard) allowlist matches. When no configured origin matches the
// request, no CORS headers are written.
func CORSMiddleware(origins []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			var allowedOrigin string
			var wildcard bool

			for _, o := range origins {
				if o == "*" {
					if origin != "" {
						allowedOrigin = origin
					} else {
						allowedOrigin = "*"
					}
					wildcard = true
					break
				}
				if o == origin {
					allowedOrigin = origin
					break
				}
			}

			if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				if !wildcard {
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
// middlewares evicted from web (sdk/tracing, sdk/cacher), which need the status
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
