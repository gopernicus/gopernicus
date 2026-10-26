package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// maxPasskeyBodyBytes bounds a passkey request body: a WebAuthn response is a
// few kilobytes, so the bound is far below the general JSON limit.
const maxPasskeyBodyBytes = 64 << 10

var _ passkeyService = (*authlogic.Service)(nil)

// The passkey JSON routes (SOC 2 S4b). Every route sits under /auth, so the
// mount's no-store wrap covers it. The wire contract (passkeys_test.go pins it
// with goldens; the views/goth passkey script and SPAs share it):
//
//   - each …/options call takes {} and answers 200 {ceremony, public_key}, where
//     public_key is the inner options JSON parseCreationOptionsFromJSON /
//     parseRequestOptionsFromJSON takes; registration answers 403 when the
//     enrollment gate needs a step-up and 409 passkey_limit at the cap;
//   - registration options also take an optional user_verification: "required"
//     makes the ceremony require user verification, "preferred" or absent keeps
//     the host's requirement (never lowering it), any other value is 400;
//   - each finish takes {ceremony, credential} — registration also an optional
//     name — where credential is the browser's PublicKeyCredential.toJSON(),
//     decoded raw so its extra members pass the strict decoder;
//   - registration finish answers 201 with the list item; sign-in finish answers
//     exactly as POST /auth/login; step-up finish answers the same plus
//     assurance, and a bearer caller also gets the new token pair;
//   - refusals: 410 challenge_expired, 401 passkey_rejected, 409 passkey_exists
//     or passkey_limit.
//
// No response carries a public key, credential id, user handle or ceremony
// state.

// passkeyService is the passkey capability the routes drive. It is resolved from
// the AuthenticationService at New, so the exported service interface does not
// grow a method; *authlogic.Service implements it.
type passkeyService interface {
	PasskeysEnabled() bool
	BeginPasskeyRegistration(ctx context.Context, sessionID, userID string, opts ...authlogic.PasskeyRegistrationOptions) (authlogic.PasskeyCeremony, error)
	FinishPasskeyRegistration(ctx context.Context, in authlogic.PasskeyRegistrationInput) (passkey.Passkey, error)
	BeginPasskeySignIn(ctx context.Context) (authlogic.PasskeyCeremony, error)
	FinishPasskeySignIn(ctx context.Context, in authlogic.PasskeySignInInput) (authlogic.TokenPair, user.User, error)
	BeginPasskeyStepUp(ctx context.Context, sessionID, userID string) (authlogic.PasskeyCeremony, error)
	FinishPasskeyStepUp(ctx context.Context, in authlogic.PasskeyStepUpInput) (authlogic.StepUpResult, error)
	ListPasskeys(ctx context.Context, userID string) ([]passkey.Passkey, error)
	RenamePasskey(ctx context.Context, in authlogic.PasskeyRenameInput) error
	RemovePasskey(ctx context.Context, in authlogic.PasskeyRemoveInput) error
	ResetUserPasskeys(ctx context.Context, actor authlogic.Principal, userID string) (int, error)
}

// passkeyOptionsResponse is every …/options success body.
type passkeyOptionsResponse struct {
	Ceremony  string          `json:"ceremony"`
	PublicKey json.RawMessage `json:"public_key"`
}

// passkeyRegistrationOptionsRequest is the registration options body.
type passkeyRegistrationOptionsRequest struct {
	UserVerification webauthn.Requirement `json:"user_verification"`
}

// passkeyRegistrationRequest is the registration finish body.
type passkeyRegistrationRequest struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
	Name       string          `json:"name"`
}

// passkeyAssertionRequest is the sign-in and step-up finish body.
type passkeyAssertionRequest struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
}

// passkeyRenameRequest is the PATCH /auth/passkeys/{id} body.
type passkeyRenameRequest struct {
	Name string `json:"name"`
}

// passkeyResponse is one list item.
type passkeyResponse struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	CreatedAt      string   `json:"created_at"`
	LastUsedAt     string   `json:"last_used_at,omitempty"`
	UserVerified   bool     `json:"user_verified"`
	BackupEligible bool     `json:"backup_eligible"`
	BackupState    bool     `json:"backup_state"`
	Transports     []string `json:"transports"`
}

// passkeyListResponse is the GET /auth/passkeys body.
type passkeyListResponse struct {
	Items []passkeyResponse `json:"items"`
}

// passkeyStepUpResponse is the step-up finish body: the login body, the
// assurance the assertion earned, and — for a bearer caller, whose old tokens
// the re-mint revoked — the new token pair.
type passkeyStepUpResponse struct {
	userResponse
	Assurance    string `json:"assurance"`
	AccessToken  string `json:"access_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// passkeyResetResponse is the admin reset body.
type passkeyResetResponse struct {
	Removed int `json:"removed"`
}

func newPasskeyResponse(p passkey.Passkey) passkeyResponse {
	out := passkeyResponse{
		ID:             p.ID,
		Name:           p.Name,
		CreatedAt:      p.CreatedAt.UTC().Format(time.RFC3339),
		UserVerified:   p.UserVerified,
		BackupEligible: p.BackupEligible,
		BackupState:    p.BackupState,
		Transports:     append([]string{}, p.Transports...),
	}
	if p.LastUsedAt != nil {
		out.LastUsedAt = p.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// mountPasskeys registers the passkey JSON routes. Registration, step-up and
// management run behind the CredentialManagement authenticator and the
// browser-safe gate; the sign-in pair is public, behind the origin gate and the
// shared per-IP passkey budget; the list is a session-security read.
func mountPasskeys(r pockets.RouteRegistrar, h *handlers, auth RouteAuthentication, browserSafe, origin, budget web.Middleware) {
	r.Handle("POST", "/auth/passkeys/registration/options", h.beginPasskeyRegistration, auth.CredentialManagement, browserSafe)
	r.Handle("POST", "/auth/passkeys/registration", h.finishPasskeyRegistration, auth.CredentialManagement, browserSafe)
	r.Handle("POST", "/auth/passkeys/assertion/options", h.beginPasskeySignIn, origin, budget)
	r.Handle("POST", "/auth/passkeys/assertion", h.finishPasskeySignIn, origin, budget)
	r.Handle("POST", "/auth/step-up/passkey/options", h.beginPasskeyStepUp, auth.CredentialManagement, browserSafe)
	r.Handle("POST", "/auth/step-up/passkey", h.finishPasskeyStepUp, auth.CredentialManagement, browserSafe)
	r.Handle("GET", "/auth/passkeys", h.listPasskeys, auth.SessionSecurityReads)
	r.Handle("PATCH", "/auth/passkeys/{id}", h.renamePasskey, auth.CredentialManagement, browserSafe)
	r.Handle("DELETE", "/auth/passkeys/{id}", h.removePasskey, auth.CredentialManagement, browserSafe)
}

func (h *handlers) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req passkeyRegistrationOptionsRequest
	if !strictJSONBody(w, r, &req, maxPasskeyBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	ceremony, err := h.passkeys.BeginPasskeyRegistration(r.Context(), sessionID, userID, authlogic.PasskeyRegistrationOptions{UserVerification: req.UserVerification})
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	web.RespondJSONOK(w, passkeyOptionsResponse{Ceremony: ceremony.Ceremony, PublicKey: ceremony.PublicKey})
}

func (h *handlers) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req passkeyRegistrationRequest
	if !strictJSONBody(w, r, &req, maxPasskeyBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	created, err := h.passkeys.FinishPasskeyRegistration(r.Context(), authlogic.PasskeyRegistrationInput{
		SessionID: sessionID, UserID: userID, Ceremony: req.Ceremony, Response: req.Credential, Name: req.Name,
	})
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	web.RespondJSONCreated(w, newPasskeyResponse(created))
}

func (h *handlers) beginPasskeySignIn(w http.ResponseWriter, r *http.Request) {
	if !passkeyEmptyBody(w, r) {
		return
	}
	ceremony, err := h.passkeys.BeginPasskeySignIn(r.Context())
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	web.RespondJSONOK(w, passkeyOptionsResponse{Ceremony: ceremony.Ceremony, PublicKey: ceremony.PublicKey})
}

// finishPasskeySignIn answers exactly as POST /auth/login: the session cookies
// and the login body.
func (h *handlers) finishPasskeySignIn(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req passkeyAssertionRequest
	if !strictJSONBody(w, r, &req, maxPasskeyBodyBytes) {
		return
	}
	pair, u, err := h.passkeys.FinishPasskeySignIn(r.Context(), authlogic.PasskeySignInInput{Ceremony: req.Ceremony, Response: req.Credential})
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, pair)
	web.RespondJSONOK(w, h.userResponseFor(r.Context(), u, ""))
}

func (h *handlers) beginPasskeyStepUp(w http.ResponseWriter, r *http.Request) {
	if !passkeyEmptyBody(w, r) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	ceremony, err := h.passkeys.BeginPasskeyStepUp(r.Context(), sessionID, userID)
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	web.RespondJSONOK(w, passkeyOptionsResponse{Ceremony: ceremony.Ceremony, PublicKey: ceremony.PublicKey})
}

func (h *handlers) finishPasskeyStepUp(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req passkeyAssertionRequest
	if !strictJSONBody(w, r, &req, maxPasskeyBodyBytes) {
		return
	}
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	cred, _ := h.svc.CurrentCredential(r.Context())
	result, err := h.passkeys.FinishPasskeyStepUp(r.Context(), authlogic.PasskeyStepUpInput{
		SessionID: sessionID, UserID: userID, Ceremony: req.Ceremony, Response: req.Credential,
	})
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	h.svc.SetSessionCookies(w, result.Tokens)
	out := passkeyStepUpResponse{userResponse: h.userResponseFor(r.Context(), result.User, ""), Assurance: string(result.Assurance)}
	if cred.Transport == TransportHeader {
		pair := newTokenResponse(result.Tokens)
		out.AccessToken, out.ExpiresAt, out.RefreshToken = pair.AccessToken, pair.ExpiresAt, pair.RefreshToken
	}
	web.RespondJSONOK(w, out)
}

func (h *handlers) listPasskeys(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	passkeys, err := h.passkeys.ListPasskeys(r.Context(), userID)
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	out := passkeyListResponse{Items: make([]passkeyResponse, 0, len(passkeys))}
	for _, p := range passkeys {
		out.Items = append(out.Items, newPasskeyResponse(p))
	}
	web.RespondJSONOK(w, out)
}

func (h *handlers) renamePasskey(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req passkeyRenameRequest
	if !strictJSONBody(w, r, &req, maxPasskeyBodyBytes) {
		return
	}
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	id := web.Param(r, "id")
	if err := h.passkeys.RenamePasskey(r.Context(), authlogic.PasskeyRenameInput{UserID: userID, PasskeyID: id, Name: req.Name}); err != nil {
		respondPasskeyError(w, err)
		return
	}
	passkeys, err := h.passkeys.ListPasskeys(r.Context(), userID)
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	for _, p := range passkeys {
		if p.ID == id {
			web.RespondJSONOK(w, newPasskeyResponse(p))
			return
		}
	}
	web.RespondJSONError(w, web.ErrNotFound("not found"))
}

// removePasskey removes a passkey behind its step-up grant. The removal revokes
// every session of the user, so it clears the caller's cookies too.
func (h *handlers) removePasskey(w http.ResponseWriter, r *http.Request) {
	userID, sessionID, ok := h.stepUpPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.passkeys.RemovePasskey(r.Context(), authlogic.PasskeyRemoveInput{SessionID: sessionID, UserID: userID, PasskeyID: web.Param(r, "id")}); err != nil {
		respondPasskeyError(w, err)
		return
	}
	h.svc.ClearSessionCookies(w)
	web.RespondJSONOK(w, map[string]string{"status": "removed"})
}

// adminResetPasskeys deletes every passkey of the target user behind the admin
// gate chain (UserAdminCheck, then the admins MFA gate, which refuses an API
// key unless MFAConfig.APIKeysExempt).
func (h *handlers) adminResetPasskeys(w http.ResponseWriter, r *http.Request) {
	if !passkeyEmptyBody(w, r) {
		return
	}
	id := web.Param(r, "id")
	principal, ok := h.adminPrincipal(w, r, UserAdminResetPasskeys, id)
	if !ok {
		return
	}
	removed, err := h.passkeys.ResetUserPasskeys(r.Context(), principal, id)
	if err != nil {
		respondPasskeyError(w, err)
		return
	}
	web.RespondJSONOK(w, passkeyResetResponse{Removed: removed})
}

// passkeyEmptyBody enforces the {} body every options call takes.
func passkeyEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if !requireJSON(w, r) {
		return false
	}
	var body struct{}
	return strictJSONBody(w, r, &body, maxPasskeyBodyBytes)
}
