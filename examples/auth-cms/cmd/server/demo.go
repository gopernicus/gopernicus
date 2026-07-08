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
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// registerDemoRoutes mounts the two host-local demo routes the A9 protocol
// drives (host code, NOT feature surface):
//
//   - GET /demo/whoami — RequirePrincipal-gated: 200 for ANY valid credential
//     class (session cookie, API-key bearer, or bearer JWT), echoing the resolved
//     principal. The API-key leg (2) and the JWT leg (3) hit this. A missing or
//     invalid/expired/revoked credential → 401.
//   - GET /demo/members-only — RequirePrincipal + toy-membership gated: 200 only
//     when the resolved principal holds the demo relation on the demo resource in
//     the toy Granter map. The invitation leg (4) hits this: B (granted on accept)
//     → 200; a third, ungranted user → 403.
func registerDemoRoutes(router *web.WebHandler, authSvc *auth.Service, members *membership) {
	router.Handle("GET", "/demo/whoami", demoWhoami(authSvc), authSvc.RequirePrincipal)
	router.Handle("GET", "/demo/members-only", demoMembersOnly(authSvc),
		authSvc.RequirePrincipal, requireMembership(authSvc, members))
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
