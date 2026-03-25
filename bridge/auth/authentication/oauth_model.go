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
	Mobile      bool   `json:"mobile"` // when true, generates flow_secret for mobile session binding
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

// OAuthAccountResponse is the JSON response for GET /oauth/linked.
type OAuthAccountResponse struct {
	Provider      string    `json:"provider"`
	ProviderEmail string    `json:"provider_email"`
	LinkedAt      time.Time `json:"linked_at"`
}
