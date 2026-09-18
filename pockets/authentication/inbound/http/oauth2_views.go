package authenticationhttp

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// OAuthViews is separate from existing Views so an existing first-party host
// need not implement the optional authorization-server presentation.
type OAuthViews interface {
	OAuthConsent(OAuthConsentPage) web.Renderer
	Sessions(SessionsPage) web.Renderer
}

type OAuthConsentPage struct {
	PageContext
	ClientName   string
	ClientID     string
	ClientHost   string
	RedirectHost string
	Resource     string
	// CapabilityDescription comes from trusted host configuration, never client metadata.
	CapabilityDescription string
	// RequestToken is a short-lived signed consent-request binding, not an
	// access token, refresh credential or authorization code. It is tied to the
	// approving user and web session and cannot authenticate resource requests.
	RequestToken string
}

type SessionView struct {
	ID         string
	Profile    string
	ClientID   string
	ClientName string
	Resource   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Current    bool
}

type SessionsPage struct {
	PageContext
	Sessions []SessionView
	NextURL  string
}
