// Command server is the zero-datastore ui/goth showcase host (GOTH-1.5). It
// serves the embedded, fingerprinted ui/goth assets and one page per specimen
// (every bundle profile, theme axis, and HTMX fixture) under a strict CSP mapped
// from goth.Bundle.Requirements(). It owns no database, no migration, and no
// pocket — the Playwright + axe three-engine harness in ../../e2e drives it.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gopernicus/gopernicus/examples/goth-showcase/internal/showcase"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func main() {
	// A missing .env is allowed; malformed configuration must stop startup.
	if err := environment.LoadEnv(); err != nil {
		slog.Error("load environment", "error", err)
		os.Exit(1)
	}

	logOpts := logging.Options{Format: "text"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		slog.Error("configure logging", "error", err)
		os.Exit(1)
	}
	log := logging.New(logOpts)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.Error("showcase exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	router := web.NewWebHandler()
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	if _, err := showcase.New(router); err != nil {
		return err
	}

	srv := web.ServerConfig{Host: "127.0.0.1", Port: "8099"}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return err
	}

	return web.Run(ctx, router, srv, log)
}
