// Command server is the zero-datastore ui/goth showcase host (GOTH-1.5). It
// serves the embedded, fingerprinted ui/goth assets and one page per specimen
// (every bundle profile, theme axis, and HTMX fixture) under a strict CSP mapped
// from goth.Bundle.Requirements(). It owns no database, no migration, and no
// feature — the Playwright + axe three-engine harness in ../../e2e drives it.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/goth-showcase/internal/showcase"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

func main() {
	log := logging.New(logging.Options{
		Level:  environment.GetEnvOrDefault("LOG_LEVEL", "INFO"),
		Format: environment.GetEnvOrDefault("LOG_FORMAT", "text"),
		Output: environment.GetEnvOrDefault("LOG_OUTPUT", "STDERR"),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "showcase exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	if _, err := showcase.New(router); err != nil {
		return err
	}

	return web.Run(ctx, router, serverConfig(), log)
}

func serverConfig() web.ServerConfig {
	return web.ServerConfig{
		Host:            environment.GetEnvOrDefault("HOST", "127.0.0.1"),
		Port:            environment.GetEnvOrDefault("PORT", "8099"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}
