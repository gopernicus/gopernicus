package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/tracing"
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
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

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

// Tracing returns middleware that wraps each request in a span on t. A nil
// tracer is treated as tracing.Noop, so the middleware can be wired
// unconditionally (the workers.WithTracer precedent).
//
// Place this middleware OUTER of web.Logger. Two consequences hang on that
// order:
//   - The span's context — and, when t returns a SpanFinisher implementing
//     tracing.SpanIdentity, the trace_id/span_id stashed on it — is already on
//     the request when Logger emits its access line, so those IDs appear on the
//     request log via logging.TracingHandler.
//   - web.RecordError type-asserts the response writer directly with no Unwrap
//     walk. Tracing records a 5xx onto the span with a synthesized error and
//     never touches the writer's recorded error, so a handler's RecordError
//     keeps landing on Logger's inner statusWriter — the access line's error
//     field does not silently regress.
//
// The span name is r.Pattern alone (it already embeds the method, e.g.
// "GET /posts/{id}", because Handle wraps middleware inside the mux match, so
// the pattern is populated when this runs). An empty pattern falls back to the
// static "http.request", never r.URL.Path, to keep span-name cardinality bounded.
//
// Cost: the middleware builds a per-request attribute slice and parses
// RemoteAddr even when wired with tracing.Noop (parity with Logger's
// per-request attribute build); there is no Noop fast path, so a host that
// cares about that cost simply omits the middleware.
func Tracing(t tracing.Tracer) Middleware {
	if t == nil {
		t = tracing.Noop{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := r.Pattern
			if name == "" {
				name = "http.request"
			}

			ctx, span := t.StartSpan(r.Context(), name)
			defer span.Finish()

			attrs := []tracing.Attribute{
				tracing.StringAttribute("http.method", r.Method),
				tracing.StringAttribute("http.host", r.Host),
				tracing.StringAttribute("user_agent", r.UserAgent()),
				tracing.StringAttribute("net.peer.ip", peerHost(r.RemoteAddr)),
			}
			if r.Pattern != "" {
				attrs = append(attrs, tracing.StringAttribute("http.route", r.Pattern))
			}
			span.SetAttributes(attrs...)

			if id, ok := span.(tracing.SpanIdentity); ok {
				if traceID := id.TraceID(); traceID != "" {
					ctx = logging.WithTraceID(ctx, traceID)
				}
				if spanID := id.SpanID(); spanID != "" {
					ctx = logging.WithSpanID(ctx, spanID)
				}
			}

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(ctx))

			span.SetAttributes(tracing.StringAttribute("http.status_code", strconv.Itoa(sw.status)))
			if sw.status >= 500 {
				span.RecordError(fmt.Errorf("server error: %d", sw.status))
			}
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
			ctx := logging.WithRequestID(r.Context(), id)
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

// peerHost returns the host portion of a RemoteAddr ("host:port"), falling back
// to the raw value when it carries no port.
func peerHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
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

// statusWriter wraps http.ResponseWriter to capture the status code and an
// optional underlying error recorded by render/respond helpers.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	err         error
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped writer for http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// RecordError stores the underlying error so the request logger can include it.
func (w *statusWriter) RecordError(err error) {
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
