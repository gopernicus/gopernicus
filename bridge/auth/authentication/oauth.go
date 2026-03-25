package authentication

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gopernicus/gopernicus/bridge/protocol/httpmid"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// ---------------------------------------------------------------------------
// Public OAuth handlers
// ---------------------------------------------------------------------------

// httpOAuthInitiate handles POST /auth/oauth/initiate.
// Starts an OAuth flow by generating authorization URL.
// For mobile flows, the client sends mobile: true and receives a flow_secret.
func (b *Bridge) httpOAuthInitiate(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[InitiateOAuthRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	// For mobile flows, use the configured mobile redirect URI.
	var mobileRedirectURI string
	if req.Mobile {
		if b.mobileRedirectURI == "" {
			web.RespondJSONError(w, web.ErrBadRequest("mobile OAuth not configured"))
			return
		}
		mobileRedirectURI = b.mobileRedirectURI
	}

	result, err := b.authenticator.InitiateOAuthFlow(r.Context(), req.Provider, req.RedirectURI, mobileRedirectURI)
	if err != nil {
		b.handleOAuthError(w, r, err, "initiate OAuth flow")
		return
	}

	web.RespondJSONOK(w, OAuthFlowResponse{
		AuthorizationURL: result.AuthorizationURL,
		State:            result.State,
		FlowSecret:       result.FlowSecret,
	})
}

// httpOAuthStart handles GET /auth/oauth/start/{provider}.
// Initiates OAuth flow and redirects user's browser to OAuth provider.
// This is the web equivalent of httpOAuthInitiate — no JS required.
func (b *Bridge) httpOAuthStart(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider == "" {
		web.RespondJSONError(w, web.ErrBadRequest("provider is required"))
		return
	}

	// Construct callback URL (where OAuth provider will redirect back to).
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	callbackURI := fmt.Sprintf("%s://%s%s/oauth/callback/%s", scheme, r.Host, b.callbackPrefix, provider)

	// Web flow — no mobile redirect URI, no flow secret.
	result, err := b.authenticator.InitiateOAuthFlow(r.Context(), provider, callbackURI, "")
	if err != nil {
		b.handleOAuthError(w, r, err, "initiate OAuth flow")
		return
	}

	web.RespondRedirect(w, r, result.AuthorizationURL, http.StatusTemporaryRedirect)
}

// httpOAuthCallback handles GET /auth/oauth/callback/{provider}.
// Processes OAuth callback, sets session cookies, and redirects to frontend.
// This is the web callback — Google/GitHub redirects the browser here.
func (b *Bridge) httpOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		web.RespondJSONError(w, web.ErrBadRequest("code and state are required"))
		return
	}

	// Web callback — no flow secret (browser redirect can't carry it).
	result, err := b.authenticator.HandleOAuthCallback(r.Context(), provider, code, state, "")
	if err != nil {
		if errors.Is(err, authentication.ErrOAuthVerificationRequired) {
			var redirectURL string
			if b.frontendURL != "" {
				redirectURL = b.frontendURL + "/auth/oauth/verify?provider=" + provider
			} else {
				redirectURL = "/auth/oauth/verify?provider=" + provider
			}

			b.log.InfoContext(r.Context(), "OAuth verification required - redirecting",
				"provider", provider,
				"redirect_url", redirectURL,
			)
			web.RespondRedirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}

		// For web flows, redirect to login with error instead of returning JSON.
		var redirectURL string
		if b.frontendURL != "" {
			redirectURL = b.frontendURL + "/login?error=oauth_failed"
		} else {
			redirectURL = "/login?error=oauth_failed"
		}
		b.log.ErrorContext(r.Context(), "OAuth callback failed", "error", err, "provider", provider)
		web.RespondRedirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	// Set HTTP-only session cookies.
	b.setSessionCookies(w, result.AccessToken, result.RefreshToken)

	// Redirect to frontend.
	redirectPath := "/"
	var redirectURL string
	if b.frontendURL != "" {
		redirectURL = b.frontendURL + redirectPath
	} else {
		redirectURL = redirectPath
	}

	b.log.InfoContext(r.Context(), "OAuth callback successful - redirecting user",
		"user_id", result.UserID,
		"is_new_user", result.IsNewUser,
		"redirect_url", redirectURL,
	)

	web.RespondRedirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// httpOAuthMobileRedirect handles GET /auth/oauth/mobile-redirect/{provider}.
// Redirect proxy for mobile OAuth: Google redirects here with code+state,
// and we redirect the browser to the mobile app's custom scheme URI.
func (b *Bridge) httpOAuthMobileRedirect(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		b.log.WarnContext(r.Context(), "missing code or state in mobile OAuth redirect")
		web.RespondJSONError(w, web.ErrBadRequest("code and state are required"))
		return
	}

	mobileURI, err := b.authenticator.GetMobileRedirectURIForState(r.Context(), state)
	if err != nil {
		b.log.WarnContext(r.Context(), "failed to get mobile redirect URI", "error", err, "state", state)
		web.RespondJSONError(w, web.ErrBadRequest("invalid or expired OAuth state"))
		return
	}

	redirectURL := fmt.Sprintf("%s?code=%s&state=%s", mobileURI, code, state)

	b.log.InfoContext(r.Context(), "mobile OAuth redirect",
		"redirect_url", mobileURI,
		"state", state,
	)

	web.RespondRedirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// httpOAuthMobileCallback handles POST /auth/oauth/callback/mobile/{provider}.
// Mobile OAuth callback that returns JSON tokens instead of setting cookies.
// Requires flow_secret for session binding to prevent URL scheme interception.
func (b *Bridge) httpOAuthMobileCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider == "" {
		web.RespondJSONError(w, web.ErrBadRequest("provider is required"))
		return
	}

	req, err := web.DecodeJSON[MobileOAuthCallbackRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	result, err := b.authenticator.HandleOAuthCallback(r.Context(), provider, req.Code, req.State, req.FlowSecret)
	if err != nil {
		b.handleOAuthError(w, r, err, "mobile OAuth callback")
		return
	}

	// Return JSON tokens (no cookies — mobile uses Bearer auth).
	web.RespondJSONOK(w, OAuthCallbackResponse{
		UserID: result.UserID,
		Tokens: TokenResponse{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
		},
		IsNewUser: result.IsNewUser,
	})
}

func (b *Bridge) httpOAuthVerifyLink(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[VerifyOAuthLinkRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	result, err := b.authenticator.VerifyOAuthLink(r.Context(), req.Email, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, authentication.ErrCodeExpired):
			web.RespondJSONError(w, web.ErrGone("verification code expired"))
		case errors.Is(err, authentication.ErrTooManyAttempts):
			web.RespondJSONError(w, web.ErrForbidden("too many attempts"))
		case errors.Is(err, authentication.ErrCodeInvalid):
			web.RespondJSONError(w, web.ErrBadRequest("invalid verification code"))
		case errors.Is(err, authentication.ErrOAuthAccountExists):
			web.RespondJSONError(w, web.ErrConflict("oauth account already linked"))
		default:
			web.RespondJSONDomainError(w, err)
		}
		return
	}

	// Set session cookies (for web clients).
	b.setSessionCookies(w, result.AccessToken, result.RefreshToken)

	web.RespondJSONOK(w, OAuthCallbackResponse{
		UserID: result.UserID,
		Tokens: TokenResponse{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
		},
		IsNewUser: result.IsNewUser,
	})
}

// ---------------------------------------------------------------------------
// Protected OAuth handlers (require auth)
// ---------------------------------------------------------------------------

func (b *Bridge) httpOAuthLink(w http.ResponseWriter, r *http.Request) {
	req, err := web.DecodeJSON[LinkOAuthRequest](r)
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}

	userID := httpmid.GetSubjectID(r.Context())

	if err := b.authenticator.LinkOAuthAccount(r.Context(), userID, req.Provider, req.Code, req.State); err != nil {
		b.handleOAuthError(w, r, err, "link OAuth account")
		return
	}

	web.RespondNoContent(w)
}

// httpOAuthLinkStart handles GET /auth/oauth/link/start/{provider} (protected).
// Initiates OAuth account linking flow for authenticated users via browser redirect.
func (b *Bridge) httpOAuthLinkStart(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider == "" {
		web.RespondJSONError(w, web.ErrBadRequest("provider is required"))
		return
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	callbackURI := fmt.Sprintf("%s://%s%s/oauth/callback/%s", scheme, r.Host, b.callbackPrefix, provider)

	result, err := b.authenticator.InitiateOAuthFlow(r.Context(), provider, callbackURI, "")
	if err != nil {
		b.handleOAuthError(w, r, err, "link OAuth initiation")
		return
	}

	web.RespondRedirect(w, r, result.AuthorizationURL, http.StatusTemporaryRedirect)
}

func (b *Bridge) httpOAuthUnlink(w http.ResponseWriter, r *http.Request) {
	userID := httpmid.GetSubjectID(r.Context())
	provider := r.PathValue("provider")

	if err := b.authenticator.UnlinkOAuthAccount(r.Context(), userID, provider); err != nil {
		b.handleOAuthError(w, r, err, "unlink OAuth account")
		return
	}

	web.RespondNoContent(w)
}

func (b *Bridge) httpOAuthGetLinked(w http.ResponseWriter, r *http.Request) {
	userID := httpmid.GetSubjectID(r.Context())

	accounts, err := b.authenticator.GetLinkedAccounts(r.Context(), userID)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}

	resp := make([]OAuthAccountResponse, len(accounts))
	for i, acct := range accounts {
		resp[i] = OAuthAccountResponse{
			Provider:      acct.Provider,
			ProviderEmail: acct.ProviderEmail,
			LinkedAt:      acct.LinkedAt,
		}
	}

	web.RespondJSONOK(w, resp)
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

// handleOAuthError converts OAuth errors to HTTP responses.
func (b *Bridge) handleOAuthError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	b.log.ErrorContext(r.Context(), "OAuth operation failed", "operation", operation, "error", err)

	switch {
	case errors.Is(err, authentication.ErrUnsupportedProvider):
		web.RespondJSONError(w, web.ErrBadRequest("unsupported OAuth provider"))
	case errors.Is(err, authentication.ErrInvalidOAuthState):
		web.RespondJSONError(w, web.ErrUnauthorized("invalid or expired OAuth state"))
	case errors.Is(err, authentication.ErrOAuthAccountNotLinked):
		web.RespondJSONError(w, web.ErrNotFound("OAuth account not linked"))
	case errors.Is(err, authentication.ErrCannotUnlinkLastMethod):
		web.RespondJSONError(w, web.ErrConflict("cannot unlink last authentication method"))
	case errors.Is(err, authentication.ErrOAuthAccountExists):
		web.RespondJSONError(w, web.ErrConflict("OAuth account already linked to another user"))
	case errors.Is(err, authentication.ErrInvalidFlowSecret):
		web.RespondJSONError(w, web.ErrUnauthorized("invalid or missing flow secret"))
	case errors.Is(err, authentication.ErrInvalidRedirectURI):
		web.RespondJSONError(w, web.ErrBadRequest("invalid redirect URI"))
	case errors.Is(err, authentication.ErrOAuthVerificationRequired):
		web.RespondJSONAccepted(w, SuccessResponse{
			Success: true,
			Message: "check your email to verify the account link",
		})
	default:
		web.RespondJSONDomainError(w, err)
	}
}
