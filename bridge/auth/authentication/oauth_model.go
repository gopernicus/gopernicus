package authentication

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/validation"
)

// ---------------------------------------------------------------------------
// OAuth Request types
// ---------------------------------------------------------------------------

// InitiateOAuthRequest is the JSON body for POST /oauth/initiate.
type InitiateOAuthRequest struct {
	Provider    string `json:"provider"`
	RedirectURI string `json:"redirect_uri"`
	Mobile      bool   `json:"mobile"`     // when true, generates flow_secret for mobile session binding
	AppOrigin   string `json:"app_origin"` // frontend origin for post-OAuth redirects
}

func (r *InitiateOAuthRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("provider", r.Provider))
	errs.Add(validation.Required("redirect_uri", r.RedirectURI))
	return errs.Err()
}

// MobileOAuthCallbackRequest is the JSON body for POST /oauth/callback/mobile/{provider}.
type MobileOAuthCallbackRequest struct {
	Code       string `json:"code"`
	State      string `json:"state"`
	FlowSecret string `json:"flow_secret"`
}

func (r *MobileOAuthCallbackRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("code", r.Code))
	errs.Add(validation.Required("state", r.State))
	errs.Add(validation.Required("flow_secret", r.FlowSecret))
	return errs.Err()
}

// VerifyOAuthLinkRequest is the JSON body for POST /oauth/verify-link.
type VerifyOAuthLinkRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (r *VerifyOAuthLinkRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("email", r.Email))
	errs.Add(validation.Required("code", r.Code))
	return errs.Err()
}

// LinkOAuthRequest is the JSON body for POST /oauth/link.
type LinkOAuthRequest struct {
	Provider string `json:"provider"`
	Code     string `json:"code"`
	State    string `json:"state"`
}

func (r *LinkOAuthRequest) Validate() error {
	var errs validation.Errors
	errs.Add(validation.Required("provider", r.Provider))
	errs.Add(validation.Required("code", r.Code))
	errs.Add(validation.Required("state", r.State))
	return errs.Err()
}

// ---------------------------------------------------------------------------
// OAuth Response types
// ---------------------------------------------------------------------------

// OAuthFlowResponse is the JSON response for POST /oauth/initiate.
type OAuthFlowResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
	FlowSecret       string `json:"flow_secret,omitempty"`
}

// OAuthCallbackResponse is the JSON response for OAuth callback and verify-link.
type OAuthCallbackResponse struct {
	UserID    string        `json:"user_id"`
	Tokens    TokenResponse `json:"tokens"`
	IsNewUser bool          `json:"is_new_user"`
}

// OAuthAccountResponse is one element of the JSON response for
// GET /auth/oauth/linked.
//
// Stability contract:
//
//   - The endpoint returns a top-level JSON array of these objects (NOT an
//     envelope like {"items": [...]}). Frontends and generated SDKs may bind
//     directly to []OAuthAccountResponse.
//   - Field names, JSON tags, and types listed below are stable. Renaming
//     or removing any of them is a breaking change to all clients.
//   - Adding new fields is non-breaking and does not require a new endpoint.
//     Clients should tolerate unknown fields when decoding.
//   - The order of accounts within the array is not specified and may change.
//   - "provider" is the canonical lowercase provider name (e.g. "google",
//     "github") — the same string used in /auth/oauth/unlink/{provider}.
//   - "provider_email" is the email reported by the OAuth provider at link
//     time, not necessarily the user's account email.
//   - "linked_at" is RFC3339 UTC.
//
// Future fields should be added below the existing ones to keep the contract
// reviewable; do not reorder existing fields. Any change to this struct
// should ship with a corresponding entry in the project changelog.
type OAuthAccountResponse struct {
	Provider      string    `json:"provider"`
	ProviderEmail string    `json:"provider_email"`
	LinkedAt      time.Time `json:"linked_at"`
}
