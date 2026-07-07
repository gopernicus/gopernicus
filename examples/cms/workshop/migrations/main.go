// Command migrations is the host-owned, pre-boot migration runner for the cms
// example. It mirrors how gopernicus init scaffolds a feature's migrations into
// the app and lets the APP apply them: the CMS feature's SQL was scaffolded into
// ./primary (via features/cms/stores/turso.ExportMigrations), and from here on
// these files are the host's — applied by this runner, extended with the host's
// own migrations in the same directory, under one app-owned schema_migrations
// ledger. The framework never applies migrations; the server does NOT migrate at
// boot. Run with `go run ./workshop/migrations` (or `make migrate`) before serving.
package main

import (
	"context"
	"embed"
	"log/slog"
	"os"
	"time"

	"github.com/gopernicus/gopernicus/sdk/environment"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// migrationsFS embeds the host's own copy of the migrations (feature-scaffolded
// plus any app-authored ones), applied in filename order.
//
//go:embed primary/[0-9]*.sql
var migrationsFS embed.FS

func main() {
	_ = environment.LoadEnv()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db, err := tursodb.Open(tursodb.Config{
		URL:       os.Getenv("TURSO_DATABASE_URL"),
		AuthToken: os.Getenv("TURSO_AUTH_TOKEN"),
	})
	if err != nil {
		log.Error("connecting to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	log.Info("running migrations", "dir", "primary")
	if err := tursodb.RunMigrations(ctx, db, migrationsFS, "primary"); err != nil {
		log.Error("running migrations", "error", err)
		os.Exit(1)
	}
	log.Info("migrations complete")
}
