package httpmid

import (
	"log/slog"
	"net/http"
	"time"
)

// Logger returns middleware that logs each HTTP request with timing and status.
//
// Log levels: INFO for 2xx/3xx, WARN for 4xx, ERROR for 5xx.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
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
			}

			if ip := GetClientIP(r.Context()); ip != "" {
				attrs = append(attrs, slog.String("client_ip", ip))
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

// statusWriter wraps http.ResponseWriter to capture the status code.
//
// It forwards Flush() when the underlying writer is a [http.Flusher] and
// exposes Unwrap() so [http.NewResponseController] can walk to the real
// writer. Without these, streaming handlers wrapped by Logger would lose
// access to the flusher and fail (SSE, chunked responses, etc.).
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements [http.Flusher] so streaming handlers keep working when
// this middleware is in the chain. No-op if the underlying writer is not
// itself a flusher.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the wrapped writer so callers using
// [http.NewResponseController] can reach the underlying implementation for
// hijacking, deadlines, and similar escape hatches.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
