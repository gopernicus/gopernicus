// Command server is the cms composition root: it loads config, builds the
// logger and database, mounts the CMS feature module (store adapter + feature),
// runs migrations, and serves with graceful shutdown. cmd is the only place that
// names concrete providers; the feature is reached only through its narrow
// Mount + Register surface.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/cms/internal/theme"
	"github.com/gopernicus/gopernicus/features/cms"
	cmsturso "github.com/gopernicus/gopernicus/features/cms/stores/turso"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/filestorage"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/web"
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

	// Host-owned HTTP router + middleware stack. The feature mounts its routes
	// onto this via the narrow RouteRegistrar; it never sees the concrete handler.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

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

	return web.Run(ctx, router, serverConfig(), log)
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
