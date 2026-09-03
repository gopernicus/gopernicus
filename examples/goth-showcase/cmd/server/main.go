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
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("showcase exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// Config comes from the environment through the sdk's struct tags: the
	// literal pre-seeds this host's own defaults, the environment wins over
	// them, and an empty value (KEY=) keeps what is already set.
	logOpts := logging.Options{Format: "text"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		return err
	}
	log := logging.New(logOpts)

	router := web.NewWebHandler(web.WithLogging(log))
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
