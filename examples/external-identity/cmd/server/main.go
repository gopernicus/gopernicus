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
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// providerName is the mirror namespace both dev issuers share, so a header
// token and a session cookie for one subject reach the same local user.
const providerName = "dev"

func main() {
	// The runtime mode comes from the process environment only; .env is loaded
	// in development and never opened in production. A missing .env is allowed;
	// malformed configuration must stop startup.
	mode, err := environment.LoadEnvForMode("AUTH_RUNTIME_MODE")
	if err != nil {
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

	if err := run(ctx, log, mode); err != nil {
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, mode environment.Mode) error {
	srv := web.ServerConfig{}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return err
	}
	if mode.IsProduction() {
		if err := srv.ValidateProduction(); err != nil {
			return err
		}
	}
	handler, err := newHandler(ctx, srv.Origin(), srv.TrustedProxyCount, mode, log)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "external identity host ready", "origin", srv.Origin(), "issuer", srv.Origin()+devissuer.IssuerPath)
	return web.Run(ctx, handler, srv, log)
}

// newHandler composes the host for origin behind trustedProxyCount reverse
// proxies. It returns the dev issuer's refusal outside development mode.
func newHandler(ctx context.Context, origin string, trustedProxyCount int, mode environment.Mode, log *slog.Logger) (http.Handler, error) {
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

	// RequireMFA on every provider is this host's MFA posture: a token whose
	// provider asserted no second factor is refused on every route before it
	// provisions a user, and production boots without an acknowledgement.
	// MaxAuthenticationAge is left zero (unbounded) here; a real provider
	// would set it, e.g. MaxAuthenticationAge: 12 * time.Hour, to cap how long
	// ago the token's holder signed in at the provider (auth_time, else iat).
	store := memstore.New(log)
	engine, err := authlogic.NewExternalAuthenticator(store.Repositories(), authlogic.ExternalConfig{
		Providers: []authlogic.ExternalProvider{
			{Name: providerName, Provider: devissuer.NewProvider(dev.Issuer(), headerVerifier, profile), ProvisionOnFirstVerification: true, RequireMFA: true},
			{Name: providerName, Provider: devissuer.NewProvider(dev.SessionIssuer(), sessionVerifier, profile), CookieName: devissuer.CookieName, ProvisionOnFirstVerification: true, RequireMFA: true},
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

	// TrustProxies runs outermost so every later layer sees the resolved client
	// IP; SecurityHeaders sends HSTS and the browser defaults.
	router := web.NewWebHandler()
	router.Use(web.TrustProxies(trustedProxyCount), web.SecurityHeaders(), web.RequestID(), web.Logger(log), web.Panics(log))

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
