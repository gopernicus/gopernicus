package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/examples/auth-cms/integrations/authentication/devissuer"
	gooidc "github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc"
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	protection "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	securityevent "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// externalProviderName is the mirror namespace of the AUTH_EXTERNAL_DEMO issuer.
const externalProviderName = "dev"

func registerDemoRoutes(router *web.WebHandler, authentication *authenticationhttp.Adapter, authorizer *decisions.Service, roleReader *roles.Service, authorization *authorizationhttp.Adapter) {
	principal := authentication.RequireAccessTokenOrAPIKey()
	router.Handle("GET", "/demo/whoami", demoWhoami(), principal)
	router.Handle("GET", "/demo/members-only", demoMembersOnly(),
		principal, requireMembership(authorization))
	router.Handle("GET", "/demo/my-projects", demoMyProjects(authorizer), principal)
	router.Handle("GET", "/demo/audit", demoAudit(roleReader),
		principal, authorization.Require(authorizationhttp.Can(demoAuditPermission, authorizationhttp.Fixed(demoResourceType, demoResourceID))))
}

// registerMFADemoRoutes mounts the two routes that require AAL2 of a person: GET
// /demo/mfa for API clients (an AAL1 session gets the RFC 9470 401) and GET
// /demo/mfa/page for the browser (an AAL1 cookie goes to the "Confirm it's you"
// chooser when the TOTP views are wired, else the passkey step-up page, else the
// same 401). Each answers the credential's assurance. A user-verified passkey
// sign-in or step-up, or an authenticator-app or recovery-code step-up, reaches
// them. It also mounts the two enrollment floor probes below.
func registerMFADemoRoutes(router *web.WebHandler, authentication *authenticationhttp.Adapter, credentials *authlogic.Service) {
	router.Handle("GET", "/demo/mfa", demoAssurance(credentials), authentication.RequirePrincipal(authenticationhttp.RequireMFA()))
	router.Handle("GET", "/demo/mfa/page", demoAssurance(credentials), authentication.RequirePrincipal(
		authenticationhttp.Accept(authenticationhttp.CredentialAccessToken), authenticationhttp.Transports(authenticationhttp.TransportCookie),
		authenticationhttp.Live(), authenticationhttp.Browser(), authenticationhttp.RequireMFA()))
	// The enrollment floor probes (AUTH_DEMO_MFA_REQUIRED=all): plain
	// RequirePrincipal routes with no RequireMFA(), so an AAL1 session passes
	// inside its grace and is refused once it closes — GET /demo/floor with the
	// 403 mfa_enrollment_required, GET /demo/floor/page with the redirect to the
	// enrollment page. With the floor off both answer any live session.
	router.Handle("GET", "/demo/floor", demoAssurance(credentials), authentication.RequirePrincipal())
	router.Handle("GET", "/demo/floor/page", demoAssurance(credentials), authentication.RequirePrincipal(
		authenticationhttp.Accept(authenticationhttp.CredentialAccessToken), authenticationhttp.Transports(authenticationhttp.TransportCookie),
		authenticationhttp.Live(), authenticationhttp.Browser()))
}

// registerOAuth2MFADemoRoute mounts GET /demo/oauth2/mfa, the delegated
// assurance probe: only an access token issued for the MCP demo resource, over
// the Authorization header, at AAL2. A grant approved from an AAL2 session
// carries aal2 (OAuth2Config.DelegatedAssurance) and passes; an AAL1 grant
// gets the RFC 9470 401.
func registerOAuth2MFADemoRoute(router *web.WebHandler, authentication *authenticationhttp.Adapter, credentials *authlogic.Service, mcpResource string) {
	router.Handle("GET", "/demo/oauth2/mfa", demoAssurance(credentials), authentication.RequirePrincipal(
		authenticationhttp.Accept(authenticationhttp.CredentialAccessToken), authenticationhttp.Audience(mcpResource),
		authenticationhttp.Transports(authenticationhttp.TransportHeader), authenticationhttp.RequireMFA()))
}

// demoAssurance echoes the admitted credential's kind and assurance.
func demoAssurance(credentials *authlogic.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cred, _ := credentials.CurrentCredential(r.Context())
		writeHostJSON(w, http.StatusOK, map[string]string{"credential": string(cred.Kind), "assurance": string(cred.Assurance)})
	}
}

// The audit permission explicitly checks scoped auditor membership.
const (
	demoRole            = "auditor"
	demoAuditPermission = "audit"
)

// demoMyProjects (authorization-v1 Z4, demonstration (b)) exercises the
// relationship kind's ENUMERATION API — flagship-specific, NEVER a consumer seam.
// It maps the resolved principal onto an authorization.PrincipalRef and asks the engine
// which `project` resources the subject may `view`. LookupAllResourceIDs is now pure
// enumeration (the engine grants no admin bypass), so the host composes
// admin-sees-everything itself: it runs the isPlatformAdmin recipe FIRST and
// surfaces it as an explicit `admin` flag. In a real app an admin skips ID
// filtering entirely; here the JSON is {"admin": bool, "ids": [...]} — a member
// gets the ids, a stranger gets an empty list, an admin gets admin=true.
func demoMyProjects(authorizer *decisions.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := sdk.PrincipalFromContext(r.Context())
		if !ok {
			writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		admin := isPlatformAdmin(r.Context(), authorizer, p.Type, p.ID)
		res, err := authorizer.LookupAllResourceIDs(r.Context(), model.PrincipalRef{Type: p.Type, ID: p.ID}, demoPermission, demoResourceType)
		if err != nil {
			writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}
		ids := res.IDs
		if ids == nil {
			ids = []string{}
		}
		writeHostJSON(w, http.StatusOK, map[string]any{"admin": admin, "ids": ids})
	}
}

// demoAudit lists concrete assignments after the route policy checks the
// named audit permission on the same project scope. A global auditor grant
// does not satisfy this scoped policy.
func demoAudit(roleReader *roles.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := roleReader.ListRoleAssignmentsByScope(r.Context(), tuples.On(demoResourceType, demoResourceID), list.Request{})
		if err != nil {
			writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "list assignments failed"})
			return
		}
		scoped := make([]map[string]string, 0, len(page.Items))
		for _, a := range page.Items {
			scoped = append(scoped, map[string]string{"subject_id": a.SubjectID, "role": a.Role})
		}
		writeHostJSON(w, http.StatusOK, map[string]any{
			"resource": demoResourceType + "/" + demoResourceID,
			// DIRECT-scope only: a subject allowed via a GLOBAL grant is NOT listed here.
			"scoped_auditors": scoped,
		})
	}
}

// demoWhoami echoes the resolved principal (the authenticator ran first).
func demoWhoami() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := sdk.PrincipalFromContext(r.Context())
		if !ok {
			writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		writeHostJSON(w, http.StatusOK, map[string]string{
			"principal_type": p.Type,
			"principal_id":   p.ID,
		})
	}
}

// demoMembersOnly is reached only when both the authenticator and the membership
// gate pass, so it just confirms access.
func demoMembersOnly() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := sdk.PrincipalFromContext(r.Context())
		writeHostJSON(w, http.StatusOK, map[string]string{
			"resource":       demoResourceType + "/" + demoResourceID,
			"relation":       demoRelation,
			"principal_type": p.Type,
			"principal_id":   p.ID,
		})
	}
}

// errDebugRouteOutsideDevelopment refuses AUTH_DEBUG=1 outside development: the
// route dumps the security-event audit rail (IP addresses, user agents, emails).
var errDebugRouteOutsideDevelopment = errors.New("auth-cms: AUTH_DEBUG=1 is refused outside development — GET /debug/security-events dumps the security-event audit rail (IPs, user agents, emails); unset AUTH_DEBUG")

// debugRouteEnabled reports whether AUTH_DEBUG=1 asks for the debug route.
func debugRouteEnabled() bool {
	return environment.GetEnvOrDefault("AUTH_DEBUG", "") == "1"
}

// refuseDebugRouteOutsideDevelopment is run()'s first check, so the refusal
// names AUTH_DEBUG before any key or pocket is built.
func refuseDebugRouteOutsideDevelopment(mode environment.Mode) error {
	if debugRouteEnabled() && mode != environment.ModeDevelopment {
		return errDebugRouteOutsideDevelopment
	}
	return nil
}

// registerDebugRoutes mounts GET /debug/security-events ONLY when AUTH_DEBUG=1 in
// development (plan-cut amendment, SRE — DEFAULT-OFF because it dumps IP/UA/emails
// and this host is public; run() refuses AUTH_DEBUG=1 in any other mode). It is
// gated like the role routes: a live human session, then the platform-admin
// permission. With no AUTH_DEBUG the route is never registered (404); with no
// session it is 401, and a signed-in non-admin is 403.
func registerDebugRoutes(router *web.WebHandler, authentication *authenticationhttp.Adapter, authorization *authorizationhttp.Adapter, repos auth.Repositories, log *slog.Logger) {
	if !debugRouteEnabled() {
		log.Info("debug security-events route DISABLED (set AUTH_DEBUG=1 to enable)")
		return
	}
	log.Warn("debug security-events route ENABLED (AUTH_DEBUG=1) — dumps IP/UA/emails to platform admins; development only")
	router.Handle("GET", "/debug/security-events", debugSecurityEvents(repos.SecurityEvents),
		authentication.RequireAccessTokenLive(),
		authorization.Require(authorizationhttp.Can("admin", authorizationhttp.Fixed(platformResourceType, platformResourceID))))
}

// debugEventResponse is the trimmed audit-row shape the debug dump returns.
type debugEventResponse struct {
	EventType   string         `json:"event_type"`
	EventStatus string         `json:"event_status"`
	UserID      string         `json:"user_id,omitempty"`
	ActorType   string         `json:"actor_type,omitempty"`
	ActorID     string         `json:"actor_id,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	IPAddress   string         `json:"ip_address,omitempty"`
	UserAgent   string         `json:"user_agent,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

// debugSecurityEvents pages the whole append-only audit rail (newest first) and
// dumps a trimmed view. Repositories.SecurityEvents is always wired on this host.
func debugSecurityEvents(repo securityevent.SecurityEventRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := make([]debugEventResponse, 0)
		cursor := ""
		for i := 0; i < 100; i++ { // bound against a runaway cursor
			pageResult, err := repo.List(r.Context(), securityevent.ListFilter{}, list.Request{Limit: list.MaxLimit, Cursor: cursor})
			if err != nil {
				writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "list security events"})
				return
			}
			for _, e := range pageResult.Items {
				out = append(out, debugEventResponse{
					EventType:   e.EventType,
					EventStatus: e.EventStatus,
					UserID:      e.UserID,
					ActorType:   e.Actor.Type,
					ActorID:     e.Actor.ID,
					Details:     e.Details,
					IPAddress:   e.IPAddress,
					UserAgent:   e.UserAgent,
					RequestID:   e.RequestID,
					TraceID:     e.TraceID,
					CreatedAt:   e.CreatedAt.Format(time.RFC3339),
				})
			}
			if !pageResult.HasMore || pageResult.NextCursor == "" {
				break
			}
			cursor = pageResult.NextCursor
		}
		writeHostJSON(w, http.StatusOK, map[string]any{"count": len(out), "events": out})
	}
}

// keyedCollaborators are the five key-backed collaborators buildAuthConfig wires.
type keyedCollaborators struct {
	signer             cryptids.JWTSigner
	challengeProtector protection.ChallengeProtector
	identifierKeyer    protection.IdentifierKeyer
	deliveryEncrypter  cryptids.Encrypter
	tokenEncrypter     cryptids.Encrypter
	totpEncrypter      cryptids.KeyedEncrypter
}

// buildKeyedCollaborators reads the six AUTH_* key variables (AUTH_TOTP_KEYS a ring) into
// authentication.KeysInput and decodes them with one authentication.NewKeys call
// (auth-jwt plan §1.6, design §3.3/§4.4/§6.1.1):
//
//   - production: every unset or malformed key is reported together, naming the
//     variable and never the value — a multi-instance deployment MUST set and SHARE
//     each one;
//   - development: an unset key becomes an EPHEMERAL per-boot key with a WARN —
//     NEVER a committed constant (this host lands on public GitHub). Access JWTs,
//     pending codes and in-flight delivery jobs do not survive a restart;
//   - AUTH_TOKEN_ENCRYPTER_KEY unset leaves provider tokens unpersisted in every mode
//     (login and linking still work).
//
// Each of the JWT, pepper, delivery and token keys also takes its ring form
// (AUTH_JWT_KEYS / AUTH_JWT_ACTIVE_KEY and the like); a ring wins over the single
// key, which then joins it as the legacy id (the pepper ring names its ids
// itself). The single-key challenge ring keeps the active key ID "dev" its pending
// challenges were stamped with, so a pepper ring's first entry reuses "dev". Each
// key is a distinct role and is never logged.
func buildKeyedCollaborators(mode environment.Mode, log *slog.Logger) (keyedCollaborators, error) {
	var in auth.KeysInput
	if err := environment.ParseEnvTags("", &in); err != nil {
		return keyedCollaborators{}, err
	}
	keys, err := auth.NewKeys(mode, in, log,
		auth.KeyJWT, auth.KeyChallengePepper, auth.KeyIdentifier, auth.KeyDeliveryEncrypter, auth.KeyTokenEncrypter, auth.KeyTOTPRing)
	if err != nil {
		return keyedCollaborators{}, err
	}
	signer, err := buildJWTSigner(keys)
	if err != nil {
		return keyedCollaborators{}, fmt.Errorf("build jwt signer: %w", err)
	}
	pepperRing := protection.HMACKeyRing(keys.ChallengeRing)
	if len(pepperRing.Keys) == 0 {
		const activeKeyID = "dev"
		pepperRing = protection.HMACKeyRing{
			Active: activeKeyID,
			Keys:   map[string][]byte{activeKeyID: keys.ChallengePepper},
		}
	}
	protector, err := protection.NewHMACChallengeProtector(pepperRing)
	if err != nil {
		return keyedCollaborators{}, fmt.Errorf("build challenge protector: %w", err)
	}
	keyer, err := protection.NewHMACIdentifierKeyer(keys.IdentifierKey)
	if err != nil {
		return keyedCollaborators{}, fmt.Errorf("build identifier keyer: %w", err)
	}
	deliveryEncrypter, err := buildEncrypter(keys.DeliveryEncrypterKey, keys.DeliveryEncrypterRing, keys.DeliveryLegacyKeyID)
	if err != nil {
		return keyedCollaborators{}, fmt.Errorf("build delivery encrypter: %w", err)
	}
	totpEncrypter, err := cryptids.NewAESGCMRing(keys.TOTPRing)
	if err != nil {
		return keyedCollaborators{}, fmt.Errorf("build totp encrypter: %w", err)
	}
	collaborators := keyedCollaborators{
		signer:             signer,
		challengeProtector: protector,
		identifierKeyer:    keyer,
		deliveryEncrypter:  deliveryEncrypter,
		totpEncrypter:      totpEncrypter,
	}
	if keys.TokenEncrypterKey != nil || len(keys.TokenEncrypterRing.Keys) > 0 {
		tokenEncrypter, err := buildEncrypter(keys.TokenEncrypterKey, keys.TokenEncrypterRing, keys.TokenLegacyKeyID)
		if err != nil {
			return keyedCollaborators{}, fmt.Errorf("build token encrypter: %w", err)
		}
		collaborators.tokenEncrypter = tokenEncrypter
	}
	return collaborators, nil
}

// buildJWTSigner signs with the AUTH_JWT_KEYS ring when it is set (stamping
// kid; the legacy id, when AUTH_JWT_SECRET is kept beside the ring, verifies
// kid-less tokens), else with the single AUTH_JWT_SECRET.
func buildJWTSigner(keys auth.Keys) (cryptids.JWTSigner, error) {
	if len(keys.JWTRing.Keys) > 0 {
		return golangjwt.NewRing(keys.JWTRing, golangjwt.WithLegacyKeyID(keys.JWTLegacyKeyID))
	}
	return golangjwt.New(string(keys.JWTSecret))
}

// buildEncrypter seals with the key ring when it is set (the legacy id opens
// what the single key wrote), else with the single key.
func buildEncrypter(single []byte, ring cryptids.KeyRing, legacyKeyID string) (cryptids.Encrypter, error) {
	if len(ring.Keys) > 0 {
		return cryptids.NewAESGCMRing(ring, cryptids.WithLegacyKeyID(legacyKeyID))
	}
	return cryptids.NewAESGCM(single)
}

// writeHostJSON writes v as a JSON response at status (host-local handlers).
func writeHostJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// externalDemo is the AUTH_EXTERNAL_DEMO=1 hybrid proof: a development issuer
// served by this process is wired beside the native credentials through
// auth.WithExternal, so one route admits a provider token OR an API key and
// another demands provider MFA. It is development-only — devissuer.New refuses
// any other mode — and the Firebase session-cookie exchange is not built here.
type externalDemo struct {
	issuer *devissuer.Issuer
	config authlogic.ExternalConfig
}

// newExternalDemo builds the dev issuer and the provider config auth.New takes.
func newExternalDemo(ctx context.Context, origin string, mode environment.Mode, log *slog.Logger) (*externalDemo, error) {
	if mode != environment.ModeDevelopment {
		return nil, fmt.Errorf("AUTH_EXTERNAL_DEMO requires development mode")
	}
	dev, err := devissuer.New(origin, mode)
	if err != nil {
		return nil, err
	}
	// The issuer is served by this process, so the verifier pins its JWKS URL
	// rather than running discovery against a server that is not listening yet.
	verifier, err := gooidc.New(ctx, dev.Issuer(),
		gooidc.WithJWKSURL(dev.JWKSURL()), gooidc.WithAudiences(devissuer.Audience), gooidc.WithLogger(log))
	if err != nil {
		return nil, err
	}
	return &externalDemo{issuer: dev, config: authlogic.ExternalConfig{
		Providers: []authlogic.ExternalProvider{{
			Name:                         externalProviderName,
			Provider:                     devissuer.NewProvider(dev.Issuer(), verifier, sdkauth.Profile{}),
			ProvisionOnFirstVerification: true,
		}},
	}}, nil
}

// register mounts the dev issuer and the three demo routes, and seeds one API
// key whose plaintext is logged once (development only):
//
//	GET /demo/external/hybrid  provider token OR API key
//	GET /demo/external/mfa     provider token whose amr carries a second factor
//	GET /demo/external/only    provider token only (a native session is refused)
func (d *externalDemo) register(ctx context.Context, router *web.WebHandler, components *auth.Components, log *slog.Logger) error {
	router.Handle(http.MethodGet, devissuer.KeysPath, d.issuer.KeysHandler().ServeHTTP)
	router.Handle(http.MethodPost, devissuer.TokenPath, d.issuer.TokenHandler())

	authentication := components.HTTP
	router.Handle(http.MethodGet, "/demo/external/hybrid", demoWhoami(), authentication.RequireExternalTokenOrAPIKey())
	router.Handle(http.MethodGet, "/demo/external/mfa", demoWhoami(),
		authentication.RequirePrincipal(authenticationhttp.Accept(authenticationhttp.CredentialExternalToken), authenticationhttp.RequireMFA()))
	router.Handle(http.MethodGet, "/demo/external/only", demoWhoami(), authentication.RequireExternalToken())

	account, err := components.Authentication.CreateServiceAccount(ctx, "external-demo", "external-demo-bot", "AUTH_EXTERNAL_DEMO seeded machine identity", false, "")
	if err != nil {
		return err
	}
	_, key, err := components.Authentication.MintAPIKey(ctx, account.ID, "external-demo", time.Time{})
	if err != nil {
		return err
	}
	log.WarnContext(ctx, "AUTH_EXTERNAL_DEMO enabled (development only): dev issuer and hybrid routes mounted; seeded API key printed ONCE",
		"issuer", d.issuer.Issuer(), "token_path", devissuer.TokenPath, "seeded_api_key", key)
	return nil
}
