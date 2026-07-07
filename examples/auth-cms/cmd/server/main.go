// Command server is the auth-v1 two-feature proof host: it mounts BOTH
// features/cms and features/auth onto one host router, with in-memory stores and
// no datastore driver in its module graph (verify: `go list -m all | grep -i
// libsql` is empty). The host is the only party that imports both features —
// features/cms never imports features/auth and vice versa (constitution rule 6).
//
// The cross-feature wiring is the point: cms's admin surface (the CRUD routes)
// is gated by auth's identity middleware via cms.Config.AdminMiddleware ←
// authSvc.RequireUser. Neither feature imports the other; structural typing on
// sdk/web.Middleware and the auth Service is what lets the host connect them.
// Public cms routes (the home page, published singles) stay ungated.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/memstore"
	"github.com/gopernicus/gopernicus/features/auth"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
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
	// Both stores are in-memory: no driver, no migrations, no datastore module.
	cmsStore := memstore.New()
	cmsRepos := cmsStore.Repositories()
	if err := seed(ctx, cmsRepos); err != nil {
		return err
	}
	authRepos := authmem.New().Repositories()

	// Host-owned router + middleware. Both features mount their routes onto this.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	mount := feature.Mount{Router: router, Logger: log}

	// Auth config: a real bcrypt hasher and the console mailer (both zero-infra —
	// bcrypt is CPU-bound, the console mailer just logs). Hasher and Mailer are
	// REQUIRED by auth; a nil RateLimiter defaults to in-memory.
	authCfg := auth.Config{
		Hasher:   bcrypt.New(),
		Mailer:   email.NewConsole(log),
		MailFrom: "auth@localhost",
	}

	// authSvc is the cross-feature surface: its RequireUser method value is the
	// middleware cms gates its admin routes on. The auth feature's own HTTP routes
	// are mounted separately via auth.Register (§3's "built twice" seam — both
	// point at the same repos/config, hold no independent state).
	authSvc, err := auth.NewService(authRepos, authCfg)
	if err != nil {
		return err
	}
	if err := auth.Register(mount, authRepos, authCfg); err != nil {
		return err
	}

	if err := cms.Register(mount, cmsRepos, cms.Config{
		Types:           []content.ContentType{productType()},    // host-registered custom type (zero migration)
		Templates:       []cms.TemplateBinding{productBinding()}, // its dev-authored renderer
		Cache:           cacher.NewMemory(),
		Mailer:          email.NewConsole(log),
		MailFrom:        "cms@localhost",
		ContactTo:       "ops@localhost",
		AdminMiddleware: []web.Middleware{authSvc.RequireUser}, // auth gates cms's admin surface
	}); err != nil {
		return err
	}

	return web.Run(ctx, router, serverConfig(), log)
}

// seed populates a little content so the public site renders something. No user
// is seeded on purpose: registration is part of the proof flow.
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
		{"Two features, one host", "auth gates cms's admin surface with zero cross-import.", "features/cms never imports features/auth; only this host's main imports both — constitution rule 6, proved with two real feature modules."},
		{"Bring your own stores", "Both features run on in-memory stores; no libsql in the graph.", "Swap datastores without forking a feature — the whole point of the module split."},
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
		Port:            environment.GetEnvOrDefault("PORT", "8082"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}
