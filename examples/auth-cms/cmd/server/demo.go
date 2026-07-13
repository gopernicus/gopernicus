package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// registerDemoRoutes mounts the host-local demo routes (host code, NOT feature
// surface):
//
//   - GET /demo/whoami — RequirePrincipal-gated: 200 for ANY valid credential
//     class (session cookie, API-key bearer, or bearer JWT), echoing the resolved
//     principal. A missing/invalid/expired/revoked credential → 401.
//   - GET /demo/members-only — RequirePrincipal + engine-Check gated (the flagship
//     posture): 200 only when the resolved principal holds `view` on project/demo
//     through the authorization engine. B (granted on invitation accept) → 200; an
//     ungranted user → 403.
//   - GET /demo/my-projects — RequirePrincipal-gated: the relationship kind's
//     LookupResources enumeration (demonstration (b)); {admin, ids} (admin flag
//     is the host-composed platform-admin recipe, not an engine bypass).
//   - GET /demo/audit — RequirePrincipal + roles-kind HasRole gated: 200 with a
//     driven ListRoleAssignmentsByResource read-back, 403 without the role.
//   - POST /demo/roles/{assign,unassign} — RequireUser-gated (the admin drives
//     them): assign/unassign a role to a subject, scoped to project/demo or GLOBAL.
//   - POST /demo/admin/bootstrap — RequireUser-gated: seeds the CALLER as
//     project:demo#owner + the platform-admin data tuple (the runtime seed step).
func registerDemoRoutes(router *web.WebHandler, authSvc *auth.Service, authorizer *authorization.Service) {
	router.Handle("GET", "/demo/whoami", demoWhoami(authSvc), authSvc.RequirePrincipal)
	router.Handle("GET", "/demo/members-only", demoMembersOnly(authSvc),
		authSvc.RequirePrincipal, requireMembership(authSvc, authorizer))
	router.Handle("GET", "/demo/my-projects", demoMyProjects(authSvc, authorizer), authSvc.RequirePrincipal)
	router.Handle("GET", "/demo/audit", demoAudit(authSvc, authorizer), authSvc.RequirePrincipal)
	router.Handle("POST", "/demo/roles/assign", demoAssignRole(authorizer), authSvc.RequireUser)
	router.Handle("POST", "/demo/roles/unassign", demoUnassignRole(authorizer), authSvc.RequireUser)
	// /demo/admin/bootstrap grants the caller platform-admin — the most privileged
	// action on this host — so it is gated on a LIVE session (auth-jwt plan §7,
	// RequireLiveSession, D1): one PK lookup denies a revoked-but-still-valid access
	// JWT immediately, rather than honoring it for up to AccessTokenTTL like the
	// stateless RequireUser tier. It returns a bare 401 on denial (this host's gated
	// surface is JSON API, matching RequireUser's 401; the browser redirect-to-login
	// UX is a host-rendered-page concern carried by segovia authpages, plan §8).
	router.Handle("POST", "/demo/admin/bootstrap", demoBootstrapAdmin(authSvc, authorizer), authSvc.RequireLiveSession)
}

// demoBootstrapAdmin (authorization-v1 Z4, seeding choice — LOGGED in the report)
// is a session-gated host route the protocol drives ONCE as the admin: it seeds
// the CALLER as project:demo#owner AND the platform-admin DATA tuple
// (platform:main#admin) via CreateRelationships. The A9 host seeds no user, so the
// admin's principal id is only known once it has registered + logged in — hence a
// runtime seed rather than a boot seed (the A9 protocol legs stay intact).
// platform-admin is DATA (a tuple over a `platform` resource type), never Config.
// Demo-only host surface. CreateRelationships is idempotent, so a repeat call is a
// no-op.
func demoBootstrapAdmin(authSvc *auth.Service, authorizer *authorization.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authSvc.CurrentPrincipal(r.Context())
		if !ok {
			writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if err := authorizer.CreateRelationships(r.Context(), []authorization.CreateRelationship{
			{ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "owner", SubjectType: p.Type, SubjectID: p.ID},
			{ResourceType: "platform", ResourceID: "main", Relation: "admin", SubjectType: p.Type, SubjectID: p.ID},
		}); err != nil {
			writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "seed failed"})
			return
		}
		writeHostJSON(w, http.StatusOK, map[string]any{
			"status":         "bootstrapped",
			"subject":        p.Type + ":" + p.ID,
			"owner":          demoResourceType + "/" + demoResourceID,
			"platform_admin": "platform/main",
		})
	}
}

// demoRole is the opaque role the audit route gates on (roles are host-interpreted
// strings — no registry).
const demoRole = "auditor"

// demoMyProjects (authorization-v1 Z4, demonstration (b)) exercises the
// relationship kind's ENUMERATION API — flagship-specific, NEVER a consumer seam.
// It maps the resolved principal onto an authorization.Subject and asks the engine
// which `project` resources the subject may `view`. LookupResources is now pure
// enumeration (the engine grants no admin bypass), so the host composes
// admin-sees-everything itself: it runs the isPlatformAdmin recipe FIRST and
// surfaces it as an explicit `admin` flag. In a real app an admin skips ID
// filtering entirely; here the JSON is {"admin": bool, "ids": [...]} — a member
// gets the ids, a stranger gets an empty list, an admin gets admin=true.
func demoMyProjects(authSvc *auth.Service, authorizer *authorization.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authSvc.CurrentPrincipal(r.Context())
		if !ok {
			writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		admin := isPlatformAdmin(r.Context(), authorizer, p.Type, p.ID)
		res, err := authorizer.LookupResources(r.Context(), authorization.Subject{Type: p.Type, ID: p.ID}, demoPermission, demoResourceType)
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

// demoAudit (authorization-v1 Z4, the roles-kind leg) gates on the roles kind:
// authorizer.HasRole (with the Q5 GLOBAL fallback — a global auditor grant
// satisfies the scoped check) decides 200 vs 403, and on success the response
// carries a DRIVEN ListRoleAssignmentsByResource read-back. That listing is
// DIRECT-SCOPE ONLY: a subject who passes the gate via a GLOBAL grant is allowed
// yet never appears in the resource's listing — the documented v1
// enumeration-vs-decision divergence, visible right here.
func demoAudit(authSvc *auth.Service, authorizer *authorization.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authSvc.CurrentPrincipal(r.Context())
		if !ok {
			writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		allowed, err := authorizer.HasRole(r.Context(), authorization.Subject{Type: p.Type, ID: p.ID}, demoRole, demoResourceType, demoResourceID)
		if err != nil {
			writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "role check failed"})
			return
		}
		if !allowed {
			writeHostJSON(w, http.StatusForbidden, map[string]string{"error": "missing role", "role": demoRole})
			return
		}
		page, err := authorizer.ListRoleAssignmentsByResource(r.Context(), demoResourceType, demoResourceID, crud.ListRequest{})
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

// roleAssignRequest is the body of the admin-driven role assign/unassign routes.
type roleAssignRequest struct {
	SubjectID string `json:"subject_id"`
	Role      string `json:"role"`
	Global    bool   `json:"global"` // true → the empty-scope GLOBAL grant (Q5 fallback); else scoped to project/demo
}

// demoAssignRole (authorization-v1 Z4 roles leg) is a session-gated host route
// (the /outbox-demo precedent) the protocol drives as the admin: it assigns a role
// to a subject, either scoped to project/demo or GLOBAL. Roles are opaque strings.
func demoAssignRole(authorizer *authorization.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeRoleRequest(w, r)
		if !ok {
			return
		}
		rt, rid := roleScope(req.Global)
		if err := authorizer.AssignRole(r.Context(), authorization.Subject{Type: "user", ID: req.SubjectID}, req.Role, rt, rid); err != nil {
			writeHostJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeHostJSON(w, http.StatusOK, map[string]any{"status": "assigned", "subject_id": req.SubjectID, "role": req.Role, "global": req.Global})
	}
}

// demoUnassignRole is the revoke leg — the exact-scope inverse of demoAssignRole.
func demoUnassignRole(authorizer *authorization.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeRoleRequest(w, r)
		if !ok {
			return
		}
		rt, rid := roleScope(req.Global)
		if err := authorizer.UnassignRole(r.Context(), authorization.Subject{Type: "user", ID: req.SubjectID}, req.Role, rt, rid); err != nil {
			writeHostJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeHostJSON(w, http.StatusOK, map[string]any{"status": "unassigned", "subject_id": req.SubjectID, "role": req.Role, "global": req.Global})
	}
}

// roleScope maps the request's global flag to a scope pair: the empty ("","")
// pair is a GLOBAL grant, otherwise the demo project scope.
func roleScope(global bool) (resourceType, resourceID string) {
	if global {
		return "", ""
	}
	return demoResourceType, demoResourceID
}

// decodeRoleRequest reads and validates the assign/unassign body.
func decodeRoleRequest(w http.ResponseWriter, r *http.Request) (roleAssignRequest, bool) {
	var req roleAssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeHostJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return roleAssignRequest{}, false
	}
	if req.SubjectID == "" || req.Role == "" {
		writeHostJSON(w, http.StatusBadRequest, map[string]string{"error": "subject_id and role are required"})
		return roleAssignRequest{}, false
	}
	return req, true
}

// demoWhoami echoes the resolved principal (RequirePrincipal ran first).
func demoWhoami(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := authSvc.CurrentPrincipal(r.Context())
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

// demoMembersOnly is reached only when both RequirePrincipal and the membership
// gate pass, so it just confirms access.
func demoMembersOnly(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := authSvc.CurrentPrincipal(r.Context())
		writeHostJSON(w, http.StatusOK, map[string]string{
			"resource":       demoResourceType + "/" + demoResourceID,
			"relation":       demoRelation,
			"principal_type": p.Type,
			"principal_id":   p.ID,
		})
	}
}

// registerDebugRoutes mounts GET /debug/security-events ONLY when AUTH_DEBUG=1
// (plan-cut amendment, SRE — DEFAULT-OFF because it dumps IP/UA/emails and this
// host is public). It is additionally session-gated (RequireUser): with no
// AUTH_DEBUG the route is never registered (404), and with no session it is 401.
func registerDebugRoutes(router *web.WebHandler, authSvc *auth.Service, repos auth.Repositories, log *slog.Logger) {
	if environment.GetEnvOrDefault("AUTH_DEBUG", "") != "1" {
		log.Info("debug security-events route DISABLED (set AUTH_DEBUG=1 to enable)")
		return
	}
	log.Warn("debug security-events route ENABLED (AUTH_DEBUG=1) — dumps IP/UA/emails; do not enable in production")
	router.Handle("GET", "/debug/security-events", debugSecurityEvents(repos.SecurityEvents), authSvc.RequireUser)
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
	CreatedAt   string         `json:"created_at"`
}

// debugSecurityEvents pages the whole append-only audit rail (newest first) and
// dumps a trimmed view. Repositories.SecurityEvents is always wired on this host.
func debugSecurityEvents(repo securityevent.SecurityEventRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := make([]debugEventResponse, 0)
		cursor := ""
		for i := 0; i < 100; i++ { // bound against a runaway cursor
			pageResult, err := repo.List(r.Context(), securityevent.ListFilter{}, crud.ListRequest{Limit: crud.MaxLimit, Cursor: cursor})
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

// buildTokenSigner builds the REQUIRED access-JWT signer from the environment
// (auth-jwt plan §1.6, D3 — the core no longer tolerates a nil signer):
//
//   - AUTH_JWT_SECRET set (≥32 bytes) → the sdk stdlib HS256 default
//     (cryptids.NewHS256) over that secret — a stable key across boots that
//     MULTIPLE INSTANCES can share.
//   - AUTH_JWT_SECRET absent → an EPHEMERAL random 32-byte key generated at boot.
//     NEVER a hardcoded constant (this host lands on public GitHub; a committed
//     signing key is a leak). Access JWTs do not survive a restart (README); API
//     clients recover via POST /auth/refresh (refresh tokens are DB-backed).
//
// The ephemeral key is a DEV / SINGLE-INSTANCE convenience ONLY: per-instance
// keys cannot cross-verify, so a MULTI-INSTANCE deployment behind a load balancer
// MUST set a shared AUTH_JWT_SECRET or every request flaps auth (§1.6).
func buildTokenSigner(log *slog.Logger) (cryptids.JWTSigner, error) {
	secret := environment.GetEnvOrDefault("AUTH_JWT_SECRET", "")
	if secret == "" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("generate ephemeral jwt key: %w", err)
		}
		secret = hex.EncodeToString(b[:]) // 64 hex chars ≥ 32 bytes
		log.Warn("AUTH_JWT_SECRET unset: using an EPHEMERAL random signing key — DEV / SINGLE-INSTANCE ONLY; access JWTs will NOT survive a restart, and a MULTI-INSTANCE deployment MUST share AUTH_JWT_SECRET across every instance")
	}
	signer, err := cryptids.NewHS256([]byte(secret))
	if err != nil {
		return nil, fmt.Errorf("build jwt signer: %w", err)
	}
	return signer, nil
}

// buildChallengeProtector wires the bundled HMAC challenge protector (design §3.3)
// from AUTH_CHALLENGE_PEPPER (hex, ≥ 32 bytes — the design-canonical env name, §11),
// or an EPHEMERAL random key — DEV / SINGLE-INSTANCE ONLY: per-instance keys cannot
// cross-verify short codes, so a multi-instance deployment MUST share the key. The
// key ring's active key ID ("dev") stamps each issued challenge's protector_key_id so
// an overlapping rotation can verify an unexpired code under the prior key. The
// register/verify challenge rail requires it (auth.ErrChallengeProtectorRequired)
// whenever Challenges is wired. This pepper is DISTINCT from the JWT signing key, the
// identifier keyer, the delivery-outbox key, and the provider-token key — never
// reused across them, and never logged (only the WARN, never the material).
func buildChallengeProtector(log *slog.Logger) (auth.ChallengeProtector, error) {
	const activeKeyID = "dev"
	key := environment.GetEnvOrDefault("AUTH_CHALLENGE_PEPPER", "")
	var raw []byte
	if key == "" {
		raw = make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate ephemeral challenge key: %w", err)
		}
		log.Warn("AUTH_CHALLENGE_PEPPER unset: using an EPHEMERAL random challenge pepper — DEV / SINGLE-INSTANCE ONLY; pending verification codes will NOT survive a restart, and a MULTI-INSTANCE deployment MUST share AUTH_CHALLENGE_PEPPER")
	} else {
		decoded, err := hex.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("decode AUTH_CHALLENGE_PEPPER (hex): %w", err)
		}
		raw = decoded
	}
	protector, err := auth.NewHMACChallengeProtector(auth.HMACKeyRing{
		Active: activeKeyID,
		Keys:   map[string][]byte{activeKeyID: raw},
	})
	if err != nil {
		return nil, fmt.Errorf("build challenge protector: %w", err)
	}
	return protector, nil
}

// buildTokenEncrypter wires an AES-256-GCM Encrypter for provider tokens at rest
// when AUTH_TOKEN_ENCRYPTER_KEY is set (exactly 32 bytes). Absent → nil: provider
// tokens are not persisted (login and linking still work) — a safe, documented
// degradation (design §3). No key ever ships in the repo.
func buildTokenEncrypter() (cryptids.Encrypter, error) {
	key := environment.GetEnvOrDefault("AUTH_TOKEN_ENCRYPTER_KEY", "")
	if key == "" {
		return nil, nil
	}
	enc, err := cryptids.NewAESGCM([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("build token encrypter (AUTH_TOKEN_ENCRYPTER_KEY must be exactly 32 bytes): %w", err)
	}
	return enc, nil
}

// buildDeliveryEncrypter wires an AES-256-GCM Encrypter for the durable delivery
// outbox payload envelope (design §6.1.1) from AUTH_DELIVERY_ENCRYPTER_KEY (exactly
// 32 bytes), or an EPHEMERAL random key — DEV / SINGLE-INSTANCE ONLY: pending
// outbox jobs seal their destination/rendered-secret under it, so a restart with an
// ephemeral key strands any in-flight job (its payload can no longer be opened), and
// a MULTI-INSTANCE deployment MUST share the key. The outbox requires it whenever
// DeliveryJobs is wired (auth.ErrDeliveryEncrypterRequired). Its key is distinct from
// the challenge pepper, JWT, and token-encryption keys.
func buildDeliveryEncrypter(log *slog.Logger) (cryptids.Encrypter, error) {
	key := environment.GetEnvOrDefault("AUTH_DELIVERY_ENCRYPTER_KEY", "")
	var raw []byte
	if key == "" {
		raw = make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate ephemeral delivery key: %w", err)
		}
		log.Warn("AUTH_DELIVERY_ENCRYPTER_KEY unset: using an EPHEMERAL random delivery-outbox key — DEV / SINGLE-INSTANCE ONLY; in-flight delivery jobs will NOT survive a restart, and a MULTI-INSTANCE deployment MUST share AUTH_DELIVERY_ENCRYPTER_KEY")
	} else {
		raw = []byte(key)
	}
	enc, err := cryptids.NewAESGCM(raw)
	if err != nil {
		return nil, fmt.Errorf("build delivery encrypter (AUTH_DELIVERY_ENCRYPTER_KEY must be exactly 32 bytes): %w", err)
	}
	return enc, nil
}

// buildIdentifierKeyer wires the bundled HMAC identifier keyer (design §4.4) from
// AUTH_IDENTIFIER_KEY (hex, ≥ 32 bytes), or an EPHEMERAL random key — DEV /
// SINGLE-INSTANCE ONLY: it derives the PII-free rate-limit/outbox idempotency keys,
// so a multi-instance deployment MUST share the key. It is deliberately separate from
// the challenge pepper, JWT, and encryption keys.
func buildIdentifierKeyer(log *slog.Logger) (auth.IdentifierKeyer, error) {
	key := environment.GetEnvOrDefault("AUTH_IDENTIFIER_KEY", "")
	var raw []byte
	if key == "" {
		raw = make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate ephemeral identifier key: %w", err)
		}
		log.Warn("AUTH_IDENTIFIER_KEY unset: using an EPHEMERAL random identifier keyer — DEV / SINGLE-INSTANCE ONLY; a MULTI-INSTANCE deployment MUST share AUTH_IDENTIFIER_KEY")
	} else {
		decoded, err := hex.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("decode AUTH_IDENTIFIER_KEY (hex): %w", err)
		}
		raw = decoded
	}
	keyer, err := auth.NewHMACIdentifierKeyer(raw)
	if err != nil {
		return nil, fmt.Errorf("build identifier keyer: %w", err)
	}
	return keyer, nil
}

// accessTokenTTL is the access-JWT lifetime from AUTH_ACCESS_TOKEN_TTL (a Go
// duration); empty/invalid → 0, which auth defaults to 15m (Config.AccessTokenTTL).
// A short value (e.g. AUTH_ACCESS_TOKEN_TTL=1s) drives the expired-access-JWT legs.
func accessTokenTTL() time.Duration {
	return durationEnv("AUTH_ACCESS_TOKEN_TTL")
}

// refreshTTL is the fixed refresh-token / session horizon from AUTH_REFRESH_TTL (a
// Go duration); empty/invalid → 0, which auth defaults to 7d (Config.RefreshTTL).
// It is set at mint and NEVER extended by rotation (fixed horizon, D2).
func refreshTTL() time.Duration {
	return durationEnv("AUTH_REFRESH_TTL")
}

// durationEnv parses env as a Go duration, returning 0 (defer to the feature's
// Config default) when unset, unparseable, or non-positive.
func durationEnv(env string) time.Duration {
	v := environment.GetEnvOrDefault(env, "")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// callbackBase is the absolute origin the OAuth callback URL is built from,
// derived from HOST/PORT (design §3's OAuthCallbackBase).
func callbackBase() string {
	host := environment.GetEnvOrDefault("HOST", "localhost")
	port := environment.GetEnvOrDefault("PORT", "8082")
	return "http://" + host + ":" + port
}

// publicAuthBaseURL is the absolute base URL magic links are built from (design §6.4):
// the framework appends "#token=<token>" to exactly this value, so it must point at
// the page that hosts the fragment-reading landing script. This host wires the bundled
// default Views, whose magic-link landing GET is mounted at /auth/magic, so the empty
// default is this host's own origin + that landing path — a clicked link then lands on
// the landing page, which scrubs the fragment and POSTs the token to redeem. From
// AUTH_PUBLIC_BASE_URL when set (a real deployment sets its own https landing URL).
// Request Host/forwarded headers NEVER participate. Production RuntimeMode requires
// HTTPS (a plain-http base is rejected at construction).
func publicAuthBaseURL() string {
	if v := environment.GetEnvOrDefault("AUTH_PUBLIC_BASE_URL", ""); v != "" {
		return v
	}
	return callbackBase() + "/auth/magic"
}

// allowedOrigins is the exact-match Origin allowlist the browser-safe mutation gate
// validates cookie-authenticated sensitive mutations (and HTML form posts) against
// (design §9.1), from a comma-separated AUTH_ALLOWED_ORIGINS; empty defaults to this
// host's own origin so same-origin browser forms pass and cross-site credentialed
// POSTs are refused. A "*" entry never authorizes a credentialed cross-origin mutation.
func allowedOrigins() []string {
	return splitEnvList("AUTH_ALLOWED_ORIGINS", []string{callbackBase()})
}

// passwordlessKinds is the set of identifier kinds passwordless login is enabled for
// (design §4.2), from a comma-separated AUTH_PASSWORDLESS; empty defaults to both v3
// kinds (email, phone) so the proof host exercises magic link + OTP on both rails.
// Each listed kind needs a wired delivery channel (email via the Mailer, phone via
// the console Notifier) or construction fails LOUDLY (auth.ErrPasswordlessKindUnsupported).
func passwordlessKinds() []string {
	return splitEnvList("AUTH_PASSWORDLESS", []string{identity.KindEmail, identity.KindPhone})
}

// splitEnvList parses env as a comma-separated list, trimming surrounding blanks and
// dropping empties; an unset/blank value (or one that is all blanks) returns fallback.
func splitEnvList(env string, fallback []string) []string {
	v := environment.GetEnvOrDefault(env, "")
	if v == "" {
		return fallback
	}
	out := make([]string, 0)
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// writeHostJSON writes v as a JSON response at status (host-local handlers).
func writeHostJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
