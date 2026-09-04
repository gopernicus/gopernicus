package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// registerDemoRoutes mounts the host-local demo routes (host code, NOT pocket
// surface). Every route is READ-ONLY: AZ3-4.1 removed the session-only
// authorization-mutation routes (POST /demo/roles/{assign,unassign}, POST
// /demo/admin/bootstrap), because a shipped HTTP route must never mutate authorization
// with session presence alone. Trusted seeding happens at boot (seedAuthorization) and
// invitation acceptance rides the baseline RelationshipWriter (membership.go); the browser-driven
// role-assignment surface is deferred until authentication exports a public
// sensitive-operation protector (the AZADM packet), so the guarded actor-mutation path
// is proven by authorization_test.go, not a browser flow.
//
//   - GET /demo/whoami — RequireAccessTokenOrAPIKey-gated: 200 for ANY valid credential
//     class (session cookie, API-key bearer, or bearer JWT), echoing the resolved
//     principal. A missing/invalid/expired/revoked credential → 401.
//   - GET /demo/members-only — RequireAccessTokenOrAPIKey + engine-Check gated (the flagship
//     posture): 200 only when the resolved principal holds `view` on project/demo
//     through the authorization engine. A member (granted on invitation accept) → 200;
//     an ungranted user → 403.
//   - GET /demo/my-projects — RequireAccessTokenOrAPIKey-gated: the relationship kind's
//     LookupResources enumeration (demonstration (b)); {admin, ids} (admin flag
//     is the host-composed platform-admin recipe, not an engine bypass).
//   - GET /demo/audit — RequireAccessTokenOrAPIKey + ROLE-MODEL gated: the pocket's
//     coordinate gate asks `audit` on project/demo, a pair the RoleModel owns
//     (the `auditor` role grants it), so the host writes no role check of its own.
//     200 with a driven ListRoleAssignmentsByResource read-back, 403 without a
//     granting role.
func registerDemoRoutes(router *web.WebHandler, authSvc *auth.Service, authorizer *authorization.Service) {
	principal := authSvc.RequireAccessTokenOrAPIKey()
	router.Handle("GET", "/demo/whoami", demoWhoami(authSvc), principal)
	router.Handle("GET", "/demo/members-only", demoMembersOnly(authSvc),
		principal, requireMembership(authSvc, authorizer))
	router.Handle("GET", "/demo/my-projects", demoMyProjects(authSvc, authorizer), principal)
	router.Handle("GET", "/demo/audit", demoAudit(authorizer),
		principal, authorizer.RequirePermissionFixed(demoResourceType, demoAuditPermission, demoResourceID))
}

// The audit route's role-model vocabulary: `auditor` is the role the host declares
// on the `project` type in authzRoleModel, and `audit` is the permission it grants —
// the role-OWNED pair (project, audit), disjoint from the relationship-owned
// (project, view). The rim stays opaque: a stored role the model cannot express is
// simply never a grantor.
const (
	demoRole            = "auditor"
	demoAuditPermission = "audit"
)

// demoMyProjects (authorization-v1 Z4, demonstration (b)) exercises the
// relationship kind's ENUMERATION API — flagship-specific, NEVER a consumer seam.
// It maps the resolved principal onto an authorization.PrincipalRef and asks the engine
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
		res, err := authorizer.LookupResources(r.Context(), authorization.PrincipalRef{Type: p.Type, ID: p.ID}, demoPermission, demoResourceType)
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

// demoAudit (authorization-v1 Z4, the roles-kind leg) is reached only when the
// RequirePermissionFixed("project", "audit", "demo") gate mounted on the route has
// already allowed: the ROLE MODEL decides, so this handler writes no role check —
// there is no host-side HasRole gate to drift from the model. The gate keeps the
// roles kind's GLOBAL fallback (a global `auditor` grant satisfies the scoped
// check), and on success the response carries a DRIVEN
// ListRoleAssignmentsByResource read-back. That listing is DIRECT-SCOPE ONLY: a
// subject who passes the gate via a GLOBAL grant is allowed yet never appears in
// the resource's listing — the documented v1 enumeration-vs-decision divergence,
// visible right here.
func demoAudit(authorizer *authorization.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

// demoWhoami echoes the resolved principal (the authenticator ran first).
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

// demoMembersOnly is reached only when both the authenticator and the membership
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
// host is public). It is additionally session-gated (RequireAccessToken): with no
// AUTH_DEBUG the route is never registered (404), and with no session it is 401.
func registerDebugRoutes(router *web.WebHandler, authSvc *auth.Service, repos auth.Repositories, log *slog.Logger) {
	if environment.GetEnvOrDefault("AUTH_DEBUG", "") != "1" {
		log.Info("debug security-events route DISABLED (set AUTH_DEBUG=1 to enable)")
		return
	}
	log.Warn("debug security-events route ENABLED (AUTH_DEBUG=1) — dumps IP/UA/emails; do not enable in production")
	router.Handle("GET", "/debug/security-events", debugSecurityEvents(repos.SecurityEvents), authSvc.RequireAccessToken())
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
// a MULTI-INSTANCE deployment MUST share the key. Delivery requires it whenever a
// delivery runtime is wired (auth.ErrDeliveryEncrypterRequired). Its key is distinct
// from the challenge pepper, JWT, and token-encryption keys.
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

// writeHostJSON writes v as a JSON response at status (host-local handlers).
func writeHostJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
