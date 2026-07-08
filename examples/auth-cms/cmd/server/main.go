// Command server is the auth-v2 A9 proof host: it mounts BOTH features/cms and
// features/authentication onto one host router, with in-memory stores and no datastore
// driver in its module graph (verify: `GOWORK=off go list -m all | grep -i
// libsql` is empty). The host is the only party that imports both features —
// features/cms never imports features/authentication and vice versa (constitution rule 6).
//
// The cross-feature wiring is the point: cms's admin surface (the CRUD routes)
// is gated by auth's identity middleware via cms.Config.AdminMiddleware ←
// authSvc.RequireUser. Neither feature imports the other; structural typing on
// sdk/web.Middleware and the auth Service is what lets the host connect them.
// Public cms routes (the home page, published singles) stay ungated.
//
// On top of v1, this host exercises the whole auth-v2 surface for the A9 proof
// protocol (see README): the verified-email login gate (RequireVerifiedEmail),
// a host-local fake OAuth provider (oauthfake.go), machine identity (API keys +
// service accounts), stateless bearer JWTs signed host-side by
// integrations/cryptids/golang-jwt, security-event audit rows surfaced through a
// DEFAULT-OFF debug route, and invitations that grant through a TOY in-memory
// membership Granter (membership.go) — the demonstration of ratified AV4:
// invitations work with NO ReBAC anywhere in this host's module graph. The two
// host-local demo routes (demo.go) are gated on a resolved principal and on toy
// membership respectively.
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
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	cmstempl "github.com/gopernicus/gopernicus/features/cms/views/templ"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/oauth"
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
	// authmem now fills all twelve auth ports (v1 + the v2 ports the A9 protocol
	// drives). The Store is kept so the debug route can read the audit rail.
	authStore := authmem.New()
	authRepos := authStore.Repositories()

	// Host-owned router + middleware. Both features and the host demo routes mount
	// onto this.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	mount := feature.Mount{Router: router, Logger: log}

	// The TOY membership Granter (design §6, ratified AV4): invitations grant
	// through this in-memory map with NO ReBAC in the module graph.
	members := newMembership()

	// Host-side collaborators built from the environment (see demo.go): the JWT
	// signer (golang-jwt, or nil when AUTH_JWT_DISABLED=1, or an ephemeral key
	// when AUTH_JWT_SECRET is unset) and the optional provider-token encrypter.
	signer, err := buildTokenSigner(log)
	if err != nil {
		return err
	}
	encrypter, err := buildTokenEncrypter()
	if err != nil {
		return err
	}

	// Auth config. Hasher + Mailer are REQUIRED; a nil RateLimiter defaults to
	// in-memory. RequireVerifiedEmail is ON (A9): login/token refuse an unverified
	// user with 403. Providers/TokenSigner/Granter are the v2 subsystems, each
	// deny-by-absence when its collaborator is nil.
	authCfg := auth.Config{
		Hasher:               bcrypt.New(),
		Mailer:               email.NewConsole(log),
		MailFrom:             "auth@localhost",
		RequireVerifiedEmail: true,
		Providers:            []oauth.Provider{fakeOAuthProvider{}},
		OAuthCallbackBase:    callbackBase(),
		RedirectAllowlist:    []string{"/"},
		TokenEncrypter:       encrypter,
		TokenSigner:          signer,
		TokenTTL:             tokenTTL(),
		Granter:              members,
		Logger:               log,
	}

	// authSvc is the auth feature's driving surface (FS2): its RequireUser method
	// value is the middleware cms gates its admin routes on, and RequirePrincipal/
	// CurrentPrincipal back the host demo routes. The feature's own HTTP routes are
	// the optional adapter over that surface — built once here, mounted once via
	// authSvc.Register(mount).
	authSvc, err := auth.NewService(authRepos, authCfg)
	if err != nil {
		return err
	}
	if err := authSvc.Register(mount); err != nil {
		return err
	}

	if err := cms.Register(mount, cmsRepos, cms.Config{
		Views:           cmstempl.New(),                          // FS3 one-line default: the bundled views module
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

	// Host-local demo + debug routes (host code, not feature surface).
	registerDemoRoutes(router, authSvc, members)
	registerDebugRoutes(router, authSvc, authRepos, log)

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
		{"Two features, one host", "auth gates cms's admin surface with zero cross-import.", "features/cms never imports features/authentication; only this host's main imports both — constitution rule 6, proved with two real feature modules."},
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
