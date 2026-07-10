// Command server is the cms composition root: it loads config, builds the
// logger and database, mounts the CMS feature module (store adapter + feature),
// runs migrations, and serves with graceful shutdown. cmd is the only place that
// names concrete providers; the feature is reached only through its narrow
// Mount + Register surface.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/cms/internal/theme"
	"github.com/gopernicus/gopernicus/features/cms"
	cmsturso "github.com/gopernicus/gopernicus/features/cms/stores/turso"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/integrations/tracing/otel"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
	"github.com/gopernicus/gopernicus/sdk/capabilities/tracing"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

func main() {
	// Load .env (missing file is not an error) before reading any config.
	_ = environment.LoadEnv()

	log := logging.New(logging.Options{
		Level:  environment.GetEnvOrDefault("LOG_LEVEL", "INFO"),
		Format: environment.GetEnvOrDefault("LOG_FORMAT", "json"),
		Output: environment.GetEnvOrDefault("LOG_OUTPUT", "STDERR"),
	}, logging.WithTracing())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	// Database. The Turso datastore connector owns the driver, DSN, and dialect
	// error mapping; the feature store adapter owns the CMS schema + repositories.
	db, err := tursodb.Open(tursodb.Config{
		URL:             os.Getenv("TURSO_DATABASE_URL"),
		AuthToken:       os.Getenv("TURSO_AUTH_TOKEN"),
		MaxOpenConns:    4,
		MaxIdleConns:    4,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return err
	}
	defer db.Close()

	// Tracing. Gate the tracer CHOICE, not the middleware: TRACING_ENABLED=true
	// builds an OpenTelemetry tracer from the TRACING_* env knobs (see
	// .env.example); otherwise a no-op tracer keeps the always-wired middleware
	// inert. With tracing enabled, client IP + user-agent leave the process to
	// the configured trace backend.
	tracer, shutdownTracer, err := buildTracer(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// Fresh context: the run-scoped ctx is already cancelled by the time
		// web.Run returns, which would make the OTLP batch exporter skip its
		// final flush (mirrors web.Run's own srv.Shutdown pattern).
		shutdownCtx, cancel := context.WithTimeout(context.Background(), durEnv("SHUTDOWN_TIMEOUT", 10*time.Second))
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			log.ErrorContext(shutdownCtx, "tracer shutdown failed", "error", err)
		}
	}()

	// Host-owned HTTP router + middleware stack. The feature mounts its routes
	// onto this via the narrow RouteRegistrar; it never sees the concrete handler.
	// Tracing sits OUTER of Logger so the traced context (and its trace_id/span_id)
	// is on the request when Logger emits its access line, and so web.RecordError's
	// direct writer type-assert keeps landing on Logger's writer.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), tracing.Middleware(tracer), web.Logger(log), web.Panics(log))

	// The host owns its database lifecycle: migrations are scaffolded into
	// ./workshop/migrations and applied PRE-BOOT by that runner (go run
	// ./workshop/migrations / make migrate), never by the framework at startup.
	// So no migration registrar is wired here.
	mount := feature.Mount{Router: router, Logger: log}

	// Host infrastructure the feature can't default: blob storage for media and
	// an email sender for the contact form.
	diskStore, err := filestorage.NewDisk(environment.GetEnvOrDefault("MEDIA_DIR", "media-data"))
	if err != nil {
		return err
	}
	blobs := filestorage.New(diskStore, filestorage.WithLogger(log))

	var sender email.Sender
	if host := os.Getenv("SMTP_HOST"); host != "" {
		sender = email.NewSMTP(email.SMTPConfig{
			Host:     host,
			Port:     environment.GetEnvOrDefault("SMTP_PORT", "587"),
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
		})
	} else {
		sender = email.NewConsole(log)
	}

	// Mount the CMS feature: the store adapter supplies the repositories (the
	// schema was applied pre-boot by the host's migration runner); the feature
	// wires its services + routes.
	repos := cmsturso.Repositories(db)
	if err := cms.Register(mount, repos, cms.Config{
		Views:     theme.New(), // host-owned custom public-site theme (the §6 seam)
		Blobs:     blobs,
		Cache:     cacher.NewMemory(), // in-memory public-page cache; redis later
		Mailer:    sender,
		MailFrom:  environment.GetEnvOrDefault("MAIL_FROM", "cms@localhost"),
		ContactTo: environment.GetEnvOrDefault("CONTACT_EMAIL", "ops@localhost"),
	}); err != nil {
		return err
	}

	// Host-local liveness+readiness probe (host route, not feature surface).
	// Mounted on the root router with no middleware, outside any gated group —
	// unauthenticated by design, since a readiness probe can't log in.
	router.Handle(http.MethodGet, "/healthz", healthzHandler(db))

	return web.Run(ctx, router, serverConfig(), log)
}

// healthzHandler is this DB-backed host's readiness probe: it pings Turso via
// the connector's StatusCheck, so 200 {"status":"ok"} means the process is up
// AND the database is reachable; a probe failure returns 503
// {"status":"unavailable"}.
func healthzHandler(db *tursodb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := tursodb.StatusCheck(r.Context(), db); err != nil {
			_ = web.RespondJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		_ = web.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// buildTracer gates the TRACER CHOICE on TRACING_ENABLED (the Tracing middleware
// is always wired). Enabled → an OpenTelemetry tracer built from the TRACING_*
// env tags via environment.ParseEnvTags; disabled → tracing.Noop. The returned
// shutdown func flushes+stops an owned provider and is a no-op for Noop.
func buildTracer(ctx context.Context) (tracing.Tracer, func(context.Context) error, error) {
	enabled, _ := strconv.ParseBool(os.Getenv("TRACING_ENABLED"))
	if !enabled {
		return tracing.Noop{}, func(context.Context) error { return nil }, nil
	}
	var cfg otel.Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		return nil, nil, err
	}
	tracer, err := otel.Open(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return tracer, tracer.Shutdown, nil
}

// serverConfig reads HTTP server settings from the environment with defaults.
func serverConfig() web.ServerConfig {
	return web.ServerConfig{
		Host:            environment.GetEnvOrDefault("HOST", "localhost"),
		Port:            environment.GetEnvOrDefault("PORT", "8080"),
		ReadTimeout:     durEnv("READ_TIMEOUT", 15*time.Second),
		WriteTimeout:    durEnv("WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:     durEnv("IDLE_TIMEOUT", 120*time.Second),
		ShutdownTimeout: durEnv("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}

func durEnv(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
