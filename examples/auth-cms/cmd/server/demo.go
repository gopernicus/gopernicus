package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
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
	router.Handle("POST", "/demo/admin/bootstrap", demoBootstrapAdmin(authSvc, authorizer), authSvc.RequireUser)
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

// buildTokenSigner builds the JWT signer from the environment (design §4.4):
//
//   - AUTH_JWT_DISABLED=1 → nil (signer-nil variant): POST /auth/token is not
//     registered (404) and bearer JWTs are NEVER parsed (401).
//   - AUTH_JWT_SECRET set (≥32 bytes) → a golang-jwt HS256 signer over that secret.
//   - AUTH_JWT_SECRET absent → an EPHEMERAL random 32-byte key generated at boot.
//     NEVER a hardcoded constant (this host lands on public GitHub; a committed
//     signing key is a leak). Tokens simply do not survive a restart (README).
func buildTokenSigner(log *slog.Logger) (cryptids.JWTSigner, error) {
	if environment.GetEnvOrDefault("AUTH_JWT_DISABLED", "") == "1" {
		log.Warn("JWT bearer mode DISABLED (AUTH_JWT_DISABLED=1): POST /auth/token 404, bearer JWTs never parsed")
		return nil, nil
	}
	secret := environment.GetEnvOrDefault("AUTH_JWT_SECRET", "")
	if secret == "" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("generate ephemeral jwt key: %w", err)
		}
		secret = hex.EncodeToString(b[:]) // 64 hex chars ≥ 32 bytes
		log.Warn("AUTH_JWT_SECRET unset: using an EPHEMERAL random signing key — tokens will NOT survive a restart")
	}
	signer, err := golangjwt.New(secret)
	if err != nil {
		return nil, fmt.Errorf("build jwt signer: %w", err)
	}
	return signer, nil
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

// tokenTTL is the bearer-JWT lifetime from AUTH_TOKEN_TTL (a Go duration), default
// 1h. A short value (e.g. AUTH_TOKEN_TTL=1s) is used by the A9 expired-token leg.
func tokenTTL() time.Duration {
	v := environment.GetEnvOrDefault("AUTH_TOKEN_TTL", "")
	if v == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return time.Hour
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

// writeHostJSON writes v as a JSON response at status (host-local handlers).
func writeHostJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
