package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// Run starts an HTTP server for handler and blocks until ctx is cancelled, then
// gracefully drains in-flight requests within cfg.ShutdownTimeout. This is the
// transport/process orchestration a host owns; it lives in sdk/foundation/web so any host
// can serve a router without re-implementing graceful shutdown.
func Run(ctx context.Context, handler http.Handler, cfg ServerConfig, log *slog.Logger) error {
	srv := cfg.HTTPServer(handler)

	serveErr := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "server starting", slog.String("address", cfg.Address()))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		log.InfoContext(ctx, "server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		log.InfoContext(ctx, "server stopped")
		return nil
	}
}
