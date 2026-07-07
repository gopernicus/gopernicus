// Command server is a second CMS host that proves the feature-module opt-out
// (plan §1.5): it mounts features/cms backed by an in-memory store, so its
// module graph contains NO libsql — only features/cms (+ theme deps) and sdk.
// Compare its go.mod to examples/cms: same feature, different datastore, and the
// driver a host doesn't use never enters its build.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/minimal/internal/memstore"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/web"
)

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

	if err := cms.Register(mount, repos, cms.Config{
		Types:     []content.ContentType{productType()},    // host-registered custom type (zero migration)
		Templates: []cms.TemplateBinding{productBinding()}, // its dev-authored renderer
		Cache:     cacher.NewMemory(),
		Mailer:    email.NewConsole(log),
		MailFrom:  "cms@localhost",
		ContactTo: "ops@localhost",
	}); err != nil {
		return err
	}

	return web.Run(ctx, router, serverConfig(), log)
}

// seed populates a little content so the public site renders something.
func seed(ctx context.Context, repos cms.Repositories) error {
	now := time.Now().UTC()

	menu, err := menus.NewMenu("Main", now)
	if err != nil {
		return err
	}
	if _, err := repos.Menus.CreateMenu(ctx, menu); err != nil {
		return err
	}
	for i, link := range []struct{ label, url string }{{"Home", "/"}, {"About", "/about"}} {
		item, err := menus.NewMenuItem(menu.ID, link.label, link.url, "", i, now)
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
		e, err := content.NewEntry("article", a.title, a.excerpt, a.body, "demo", content.StatusPublished, "", now)
		if err != nil {
			return err
		}
		if _, err := repos.Entries.Create(ctx, e); err != nil {
			return err
		}
	}

	page, err := content.NewEntry("page", "About", "", "This page is served from memory — no SQL involved.", "", content.StatusPublished, "", now)
	if err != nil {
		return err
	}
	if _, err := repos.Entries.Create(ctx, page); err != nil {
		return err
	}

	// A product: the host's custom type, with EAV custom fields, on the same rail.
	prod, err := content.NewEntry("product", "Widget 3000", "The flagship widget.", "Built to last; ships worldwide.", "", content.StatusPublished, "", now)
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
