// Command migrations is the host-owned, pre-boot migration runner for the cms
// example. It mirrors how gopernicus init scaffolds a pocket's migrations into
// the app and lets the APP apply them: the CMS pocket's SQL was scaffolded into
// ./primary (via pockets/cms/stores/turso.ExportMigrations), and from here on
// these files are the host's — applied by this runner, extended with the host's
// own migrations in the same directory, under one app-owned schema_migrations
// ledger. The framework never applies migrations; the server does NOT migrate at
// boot. Run with `go run ./workshop/migrations` (or `make migrate`) before serving.
package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// migrationsFS embeds the host's own copy of the migrations (pocket-scaffolded
// plus any app-authored ones), applied in filename order.
//
//go:embed primary/[0-9]*.sql
var migrationsFS embed.FS

func main() {
	if err := environment.LoadEnv(); err != nil {
		slog.Error("load environment", "error", err)
		os.Exit(1)
	}

	logOpts := logging.Options{Format: "text", Output: "STDOUT"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		slog.Error("configure logging", "error", err)
		os.Exit(1)
	}
	log := logging.New(logOpts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := run(ctx, log); err != nil {
		log.Error("migrations", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	db, err := tursodb.Open(ctx, tursodb.Config{
		URL:       os.Getenv("TURSO_DATABASE_URL"),
		AuthToken: os.Getenv("TURSO_AUTH_TOKEN"),
	})
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	log.Info("running migrations", "dir", "primary")
	if err := tursodb.RunMigrations(ctx, db, migrationsFS, "primary"); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	log.Info("migrations complete")
	return nil
}
