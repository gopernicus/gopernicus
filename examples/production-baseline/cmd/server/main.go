// Command server is the auditable reference host: one composition root that
// asks for the production posture of examples/README.md §3b on every surface
// and acknowledges nothing. Users sign in at one external OIDC provider; the
// authentication pocket maps each verified subject to a local user and keeps
// the security-event audit rail, and the authorization pocket records every
// role change. Both persist in PostgreSQL through their pgx stores, and the
// pgxdb limiter gives authentication one rate-limit budget across instances.
//
// With IDP_ISSUER unset the provider is a development issuer served by this
// process (integrations/authentication/devissuer). It refuses every mode but
// development, so production boots only with a real provider configured.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gopernicus/gopernicus/examples/production-baseline/integrations/authentication/devissuer"
	gooidc "github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc"
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	authenticationpgx "github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	authorizationpgx "github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	// providerName is the external-subject mirror namespace. Never change it
	// once rows exist.
	providerName = "idp"
	// adminRole is the global role that administers roles.
	adminRole = "admin"
)

// identityProviderConfig selects the external identity provider. An empty
// Issuer selects the development issuer; an empty JWKSURL runs OIDC discovery.
type identityProviderConfig struct {
	Issuer   string `env:"IDP_ISSUER"`
	Audience string `env:"IDP_AUDIENCE"`
	JWKSURL  string `env:"IDP_JWKS_URL"`
}

// composition is what the pockets are built from once the keys, the server
// settings, the stores and the provider are resolved: run fills it over
// PostgreSQL, the tests over in-memory stores.
type composition struct {
	mode                environment.Mode
	log                 *slog.Logger
	origin              string
	keys                authentication.Keys
	sessionCookie       authenticationhttp.CookieConfig
	limiter             ratelimiter.Limiter
	authenticationRepos authentication.Repositories
	authorizationRepos  authorization.Repositories
	provider            sdkauth.Provider
}

func main() {
	// The runtime mode comes from the process environment only: .env is loaded
	// in development and never opened in production, and a .env that names
	// APP_RUNTIME_MODE is refused.
	mode, err := environment.LoadEnvForMode("APP_RUNTIME_MODE")
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

// run composes the host in the order production checks it: the keys, the
// public origin, the database and its rate-limit table, the stores, the
// provider, then the pockets.
func run(ctx context.Context, log *slog.Logger, mode environment.Mode) error {
	keys, err := loadKeys(mode, log)
	if err != nil {
		return err
	}
	srv, err := loadServerConfig(mode)
	if err != nil {
		return err
	}

	var dbCfg pgxdb.Config
	if err := environment.ParseEnvTags("", &dbCfg); err != nil {
		return err
	}
	dbCfg.RuntimeMode = mode
	dbCfg.Logger = log
	db, err := pgxdb.Open(ctx, dbCfg)
	if err != nil {
		return err
	}
	defer db.Close()
	limiter := pgxdb.NewLimiter(db)
	if err := limiter.StatusCheck(ctx); err != nil {
		return fmt.Errorf("rate limiter is not usable: %w", err)
	}

	authenticationRepos, err := authenticationpgx.Repositories(ctx, db)
	if err != nil {
		return err
	}
	// Users sign in at the provider, so the native rails this host never
	// serves stay unwired: challenge codes and links (which would need a
	// challenge pepper), contact changes, and machine identity.
	authenticationRepos.Challenges, authenticationRepos.ContactChanges = nil, nil
	authenticationRepos.ServiceAccounts, authenticationRepos.APIKeys = nil, nil
	authorizationRepos, err := authorizationpgx.Repositories(ctx, db,
		authorizationpgx.WithAudit(), authorizationpgx.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy()))
	if err != nil {
		return err
	}

	router := newRouter(srv.TrustedProxyCount, log)
	provider, err := newProvider(ctx, router, srv.Origin(), mode, log)
	if err != nil {
		return err
	}
	if err := mount(router, composition{
		mode:                mode,
		log:                 log,
		origin:              srv.Origin(),
		keys:                keys,
		sessionCookie:       authenticationhttp.CookieConfig{Secure: true},
		limiter:             limiter,
		authenticationRepos: authenticationRepos,
		authorizationRepos:  authorizationRepos,
		provider:            provider,
	}); err != nil {
		return err
	}
	router.Handle(http.MethodGet, "/healthz", healthzHandler(db))
	return web.Run(ctx, router, srv, log)
}

// loadKeys decodes the two keys this host needs: the access-JWT signing key
// and the identifier key. Production refuses either unset, naming both in one
// error; development mints an ephemeral key for each unset one. The provider
// owns passwords and mail, so no other key is needed.
func loadKeys(mode environment.Mode, log *slog.Logger) (authentication.Keys, error) {
	var in authentication.KeysInput
	if err := environment.ParseEnvTags("", &in); err != nil {
		return authentication.Keys{}, err
	}
	return authentication.NewKeys(mode, in, log, authentication.KeyJWT, authentication.KeyIdentifier)
}

// loadServerConfig reads the HTTP server settings. Production requires an
// https PUBLIC_BASE_URL, the one origin allowed to send cookie mutations.
func loadServerConfig(mode environment.Mode) (web.ServerConfig, error) {
	var srv web.ServerConfig
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return web.ServerConfig{}, err
	}
	if mode.IsProduction() {
		if err := srv.ValidateProduction(); err != nil {
			return web.ServerConfig{}, err
		}
	}
	return srv, nil
}

// newRouter builds the host router. TrustProxies runs outermost, so every later
// layer — the access log, the rate limits and the audit rail — sees the client
// IP resolved from TRUSTED_PROXY_COUNT; SecurityHeaders sends HSTS and the
// browser defaults (TLS terminates at the edge, the app sends HSTS).
func newRouter(trustedProxyCount int, log *slog.Logger) *web.WebHandler {
	router := web.NewWebHandler()
	router.Use(web.TrustProxies(trustedProxyCount), web.SecurityHeaders(), web.RequestID(), web.Logger(log), web.Panics(log))
	return router
}

// newProvider returns the external identity provider. With IDP_ISSUER set it
// is the generic OIDC provider for that issuer, whose tokens must carry
// IDP_AUDIENCE and verify against IDP_JWKS_URL. Unset, it is the development
// issuer served by this process at origin, whose key set and token endpoints it
// mounts on router; devissuer.New refuses every mode but development.
func newProvider(ctx context.Context, router *web.WebHandler, origin string, mode environment.Mode, log *slog.Logger) (sdkauth.Provider, error) {
	var idp identityProviderConfig
	if err := environment.ParseEnvTags("", &idp); err != nil {
		return nil, err
	}
	if idp.Issuer != "" {
		options := []gooidc.Option{gooidc.WithAudiences(idp.Audience), gooidc.WithLogger(log)}
		if idp.JWKSURL != "" {
			options = append(options, gooidc.WithJWKSURL(idp.JWKSURL))
		}
		verifier, err := gooidc.New(ctx, idp.Issuer, options...)
		if err != nil {
			return nil, err
		}
		return sdkauth.NewOIDC(idp.Issuer, verifier, sdkauth.Profile{}), nil
	}

	dev, err := devissuer.New(origin, mode)
	if err != nil {
		return nil, err
	}
	// The issuer is served by this process, so the verifier pins its JWKS URL
	// instead of running discovery against a server that is not listening yet.
	verifier, err := gooidc.New(ctx, dev.Issuer(),
		gooidc.WithJWKSURL(dev.JWKSURL()), gooidc.WithAudiences(devissuer.Audience), gooidc.WithLogger(log))
	if err != nil {
		return nil, err
	}
	router.Handle(http.MethodGet, devissuer.KeysPath, dev.KeysHandler().ServeHTTP)
	router.Handle(http.MethodPost, devissuer.TokenPath, dev.TokenHandler())
	return devissuer.NewProvider(dev.Issuer(), verifier, sdkauth.Profile{}), nil
}

// mount builds both pockets from c and registers their routes on router.
// Authentication admits only the provider's tokens: password flows and
// delivery are off. Authorization records every change and serves role
// administration behind authenticate ∘ browser-safe mutation ∘ the global
// admin role.
func mount(router *web.WebHandler, c composition) error {
	signer, err := golangjwt.New(string(c.keys.JWTSecret))
	if err != nil {
		return fmt.Errorf("build jwt signer: %w", err)
	}
	keyer, err := protection.NewHMACIdentifierKeyer(c.keys.IdentifierKey)
	if err != nil {
		return fmt.Errorf("build identifier keyer: %w", err)
	}
	authenticationComponents, err := authentication.New(c.authenticationRepos, signer, c.mode, delivery.ModeOff,
		authentication.WithPassword(authentication.PasswordConfig{PasswordFlowsDisabled: true}),
		authentication.WithIdentity(authentication.IdentityConfig{IdentifierKeyer: keyer}),
		authentication.WithAbuseProtection(authentication.AbuseProtectionConfig{RateLimiter: c.limiter}),
		authentication.WithBrowser(authentication.BrowserConfig{SessionCookie: c.sessionCookie, AllowedOrigins: []string{c.origin}}),
		authentication.WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{
			Name:                         providerName,
			Provider:                     c.provider,
			ProvisionOnFirstVerification: true,
			RequireVerifiedEmail:         true,
		}}}),
		authentication.WithLogger(c.log),
	)
	if err != nil {
		return err
	}
	m := pockets.Mount{Router: router, Logger: c.log}
	if err := authenticationComponents.HTTP.Register(m); err != nil {
		return err
	}

	authorizationComponents, err := authorization.New(c.authorizationRepos,
		authorization.WithRuntimeMode(c.mode),
		authorization.WithModel(accessModel()),
		authorization.WithLogger(c.log),
	)
	if err != nil {
		return err
	}
	// The role routes' gate ends in this pocket's own guard, so the adapter
	// serving them is built once the pocket exists.
	authenticate := authenticationComponents.HTTP.RequirePrincipal(
		authenticationhttp.Accept(authenticationhttp.CredentialExternalToken), authenticationhttp.Live())
	browserSafe := authenticationComponents.HTTP.RequireBrowserSafeMutation()
	authorize := authorizationComponents.HTTP.Require(authorizationhttp.HasRole(adminRole, authorizationhttp.Global()))
	authorizationComponents.HTTP, err = authorizationhttp.New(
		authorizationhttp.Services{Decisions: authorizationComponents.Decisions, Roles: authorizationComponents.Roles, Mutations: authorizationComponents.Mutations},
		authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{
			Gate:        func(next http.Handler) http.Handler { return authenticate(browserSafe(authorize(next))) },
			WritePolicy: adminWritesAnyRole,
		}),
	)
	if err != nil {
		return err
	}
	return authorizationComponents.Register(m)
}

// accessModel is the host's permission model: a project is managed by its
// owners and viewed by its viewers and managers, and the global admin role
// manages every project.
func accessModel() decisions.Model {
	users := []decisions.SubjectTypeRef{{Type: "user"}}
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"project": {
			Relations: map[string]decisions.RelationDef{
				"owner":  {AllowedSubjects: users},
				"viewer": {AllowedSubjects: users},
			},
			Permissions: map[string]decisions.Expression{
				"manage": decisions.Any(decisions.Role(adminRole), decisions.Direct("owner")),
				"view":   decisions.Any(decisions.Permission("manage"), decisions.Direct("viewer")),
			},
		},
	}}
}

// adminWritesAnyRole admits every validated role write: the role routes' gate
// has already required the global admin role, and a global admin administers
// every role in every scope. The store's integrity policy still keeps one owner
// on every project.
func adminWritesAnyRole(context.Context, authorizationhttp.RoleWriteRequest) error { return nil }

// healthzHandler is the readiness probe: 200 while the database answers, 503
// otherwise. It is unauthenticated by design — a probe cannot sign in.
func healthzHandler(db *pgxdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pgxdb.StatusCheck(r.Context(), db); err != nil {
			_ = web.RespondJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		_ = web.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
