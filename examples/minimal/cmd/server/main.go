// Command server is a second CMS host that proves the feature-module opt-out
// (plan §1.5): it mounts features/cms backed by an in-memory store, so its
// module graph contains NO libsql — only features/cms, its bundled views module
// features/cms/views/goth, ui/goth, and sdk.
// Compare its go.mod to examples/cms: same feature, different datastore, and the
// driver a host doesn't use never enters its build.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/minimal/internal/memstore"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	cmsgoth "github.com/gopernicus/gopernicus/features/cms/views/goth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
	uigothassets "github.com/gopernicus/gopernicus/ui/goth/assets"
)

// gothAssetBasePath is the public URL prefix this host serves the ui/goth
// fingerprinted assets (the CMS pages' stylesheet) under. The bundle pins the same
// path so the emitted stylesheet href and the asset route agree.
const gothAssetBasePath = "/assets/goth"

func main() {
	_ = environment.LoadEnv()

	log := logging.New(logging.Options{
		Level:  environment.GetEnvOrDefault("LOG_LEVEL", "INFO"),
		Format: environment.GetEnvOrDefault("LOG_FORMAT", "text"),
		Output: environment.GetEnvOrDefault("LOG_OUTPUT", "STDERR"),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	// The store is in-memory: no driver, no migrations, no datastore module.
	store := memstore.New()
	repos := store.Repositories()
	if err := seed(ctx, repos); err != nil {
		return err
	}

	// Host-owned router + middleware. The feature mounts its routes onto this.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	mount := feature.Mount{Router: router, Logger: log}

	// The ui/goth presentation bundle backs the CMS views; the host serves the
	// kit's fingerprinted assets (the CMS pages' stylesheet) under the path the
	// bundle names. The kit owns no route, so the host mounts it.
	bundle, err := uigoth.New(uigoth.Config{AssetBasePath: gothAssetBasePath})
	if err != nil {
		return err
	}
	cmsViews, err := cmsgoth.New(bundle)
	if err != nil {
		return err
	}
	uigothStatic := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
	uigothStatic.AddRoutes(router, gothAssetBasePath)

	if err := cms.Register(mount, repos, cms.Config{
		Views:     cmsViews,                                 // the ui/goth-backed bundled default
		Types:     []content.ContentType{productType()},    // host-registered custom type (zero migration)
		Templates: []cms.TemplateBinding{productBinding()}, // its dev-authored renderer
		Cache:     cacher.NewMemory(),
		Mailer:    email.NewConsole(log),
		MailFrom:  "cms@localhost",
		ContactTo: "ops@localhost",
	}); err != nil {
		return err
	}

	// Host-local liveness probe (host route, not feature surface). Mounted on
	// the root router with no middleware, outside any gated group —
	// unauthenticated by design, since a readiness probe can't log in.
	router.Handle(http.MethodGet, "/healthz", healthzHandler())

	return web.Run(ctx, router, serverConfig(), log)
}

// healthzHandler is this host's liveness probe. This host is memory-backed, so
// there is no DB to probe — reaching the handler is itself the liveness signal.
func healthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

// seed populates a little content so the public site renders something.
// ids seeds demo content with the default entity-ID strategy, matching the
// zero-value cms.Config.IDs the host wires.
var ids = cryptids.IDGenerator{}

func seed(ctx context.Context, repos cms.Repositories) error {
	now := time.Now().UTC()

	menu, err := menus.NewMenu(ids, "Main", now)
	if err != nil {
		return err
	}
	if _, err := repos.Menus.CreateMenu(ctx, menu); err != nil {
		return err
	}
	for i, link := range []struct{ label, url string }{{"Home", "/"}, {"About", "/about"}} {
		item, err := menus.NewMenuItem(ids, menu.ID, link.label, link.url, "", i, now)
		if err != nil {
			return err
		}
		if _, err := repos.Menus.AddItem(ctx, item); err != nil {
			return err
		}
	}

	// Content is the Registry model: Articles and the About Page are content.Entry
	// rows on the shared spine, distinguished by Type — no per-type tables.
	articles := []struct{ title, excerpt, body string }{
		{"Running CMS without a database", "This host uses an in-memory store.", "The features/cms module is datastore-free; this host supplies its own store, so no libsql is in its module graph."},
		{"Bring your own store", "Implement the repository ports, mount the feature.", "Swap the datastore without forking the feature — that is the whole point of the module split."},
	}
	for _, a := range articles {
		e, err := content.NewEntry(ids, "article", a.title, a.excerpt, a.body, "demo", content.StatusPublished, "", now)
		if err != nil {
			return err
		}
		if _, err := repos.Entries.Create(ctx, e); err != nil {
			return err
		}
	}

	page, err := content.NewEntry(ids, "page", "About", "", "This page is served from memory — no SQL involved.", "", content.StatusPublished, "", now)
	if err != nil {
		return err
	}
	if _, err := repos.Entries.Create(ctx, page); err != nil {
		return err
	}

	// A product: the host's custom type, with EAV custom fields, on the same rail.
	prod, err := content.NewEntry(ids, "product", "Widget 3000", "The flagship widget.", "Built to last; ships worldwide.", "", content.StatusPublished, "", now)
	if err != nil {
		return err
	}
	prod.Fields = content.Fields{
		"subtitle": {Kind: content.KindText, Raw: "Now with more widgets"},
		"price":    {Kind: content.KindNumber, Raw: "49.99"},
	}
	if _, err := repos.Entries.Create(ctx, prod); err != nil {
		return err
	}
	return nil
}

func serverConfig() web.ServerConfig {
	return web.ServerConfig{
		Host:            environment.GetEnvOrDefault("HOST", "localhost"),
		Port:            environment.GetEnvOrDefault("PORT", "8081"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}
