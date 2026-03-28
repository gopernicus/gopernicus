package httpmid

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Panics returns middleware that recovers from panics and returns a 500 error.
//
// This catches intentional panics from [MustGetSubject] and similar functions,
// converting them to proper error responses instead of crashing the server.
func Panics(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.ErrorContext(r.Context(), "panic recovered",
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					http.Error(w, `{"message":"internal error","code":"internal"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
