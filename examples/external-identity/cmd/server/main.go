// Command server is the external-only identity host: every user signs in at an
// external identity provider, and this host maps the provider's verified
// subject to a local user through the authentication pocket's external engine.
// It wires no datastore driver, no TokenSigner, no sessions and no mail — the
// engine, an in-memory store and a verifier are the whole identity stack.
//
// The provider here is a development issuer served by this same process
// (integrations/authentication/devissuer). It exists only in development:
// any other runtime mode refuses to construct it, so this host does not boot
// in production until a real provider is wired in its place.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gopernicus/gopernicus/examples/external-identity/integrations/authentication/devissuer"
	mehttp "github.com/gopernicus/gopernicus/examples/external-identity/internal/inbound/domains/me"
	"github.com/gopernicus/gopernicus/examples/external-identity/internal/logic/domains/me"
	"github.com/gopernicus/gopernicus/examples/external-identity/pockets/authentication/outbound/memstore"
	"github.com/gopernicus/gopernicus/integrations/authentication/auth0"
	gooidc "github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// providerName is the mirror namespace both dev issuers share, so a header
// token and a session cookie for one subject reach the same local user.
const providerName = "dev"

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
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	srv := web.ServerConfig{}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return err
	}
	mode := struct {
		RuntimeMode environment.Mode `env:"AUTH_RUNTIME_MODE" default:"development"`
	}{}
	if err := environment.ParseEnvTags("", &mode); err != nil {
		return err
	}
	handler, err := newHandler(ctx, srv.Origin(), mode.RuntimeMode, log)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "external identity host ready", "origin", srv.Origin(), "issuer", srv.Origin()+devissuer.IssuerPath)
	return web.Run(ctx, handler, srv, log)
}

// newHandler composes the host for origin. It returns the dev issuer's
// refusal outside development mode.
func newHandler(ctx context.Context, origin string, mode environment.Mode, log *slog.Logger) (http.Handler, error) {
	dev, err := devissuer.New(origin, mode)
	if err != nil {
		return nil, err
	}
	// The dev issuer is served by this process, so the verifiers pin its JWKS
	// URL instead of running discovery against a server not yet listening.
	headerVerifier, err := gooidc.New(ctx, dev.Issuer(),
		gooidc.WithJWKSURL(dev.JWKSURL()), gooidc.WithAudiences(devissuer.Audience), gooidc.WithLogger(log))
	if err != nil {
		return nil, err
	}
	sessionVerifier, err := gooidc.New(ctx, dev.SessionIssuer(),
		gooidc.WithJWKSURL(dev.JWKSURL()), gooidc.WithAudiences(devissuer.Audience), gooidc.WithLogger(log))
	if err != nil {
		return nil, err
	}
	// The dev issuer mints Auth0-shaped claims, so the Auth0 preset maps them.
	profile := auth0.Profile("localhost")

	store := memstore.New(log)
	engine, err := authlogic.NewExternalAuthenticator(store.Repositories(), authlogic.ExternalConfig{
		Providers: []authlogic.ExternalProvider{
			{Name: providerName, Provider: sdkauth.NewOIDC(dev.Issuer(), headerVerifier, profile), ProvisionOnFirstVerification: true},
			{Name: providerName, Provider: sdkauth.NewOIDC(dev.SessionIssuer(), sessionVerifier, profile), CookieName: devissuer.CookieName, ProvisionOnFirstVerification: true},
		},
	}, mode, authlogic.WithExternalLogger(log))
	if err != nil {
		return nil, err
	}
	authentication, err := authenticationhttp.NewAuthenticator(engine, mode,
		authenticationhttp.WithExternalCookies(devissuer.CookieName), authenticationhttp.WithAuthenticatorLogger(log))
	if err != nil {
		return nil, err
	}

	router := web.NewWebHandler()
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	router.Handle(http.MethodGet, devissuer.KeysPath, dev.KeysHandler().ServeHTTP)
	router.Handle(http.MethodPost, devissuer.TokenPath, dev.TokenHandler())
	router.Handle(http.MethodPost, devissuer.SessionPath, dev.SessionHandler())

	// The provider cookie makes /api a cookie-authenticated surface, so unsafe
	// methods ride the host cross-origin posture (ARCHITECTURE.md) before the
	// authenticator runs.
	crossOrigin := http.NewCrossOriginProtection()
	mehttp.Mount(router, me.NewService(engine), crossOrigin.Handler, authentication.RequirePrincipal())

	router.Handle(http.MethodGet, "/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return router, nil
}
