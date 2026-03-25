package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/oauth"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// InitiateOAuthFlow starts an OAuth authorization flow for the given provider.
//
// Returns the authorization URL the user should be redirected to, the state
// parameter, and an optional flow secret for mobile session binding.
//
// Mobile flows are detected by the presence of mobileRedirectURI. When set,
// a flow secret is generated for session binding to prevent URL scheme
// interception. For browser-redirect (web) flows, pass an empty string.
func (a *Authenticator) InitiateOAuthFlow(ctx context.Context, provider, redirectURI, mobileRedirectURI string) (*OAuthFlowResult, error) {
	if err := a.requireOAuth(); err != nil {
		return nil, err
	}

	p, ok := a.providers[provider]
	if !ok {
		return nil, ErrUnsupportedProvider
	}

	if a.allowedRedirectURIs != nil {
		if _, allowed := a.allowedRedirectURIs[redirectURI]; !allowed {
			a.logSecurityEvent(ctx, "", SecEventOAuthLogin, SecStatusBlocked, map[string]any{
				"reason":       "invalid_redirect_uri",
				"redirect_uri": redirectURI,
				"provider":     provider,
			})
			return nil, ErrInvalidRedirectURI
		}
	}

	codeVerifier, err := generateBase64URLToken()
	if err != nil {
		return nil, fmt.Errorf("auth oauth: generate code verifier: %w", err)
	}

	state, err := generateBase64URLToken()
	if err != nil {
		return nil, fmt.Errorf("auth oauth: generate state: %w", err)
	}

	var nonce string
	if p.SupportsOIDC() {
		nonce, err = generateBase64URLToken()
		if err != nil {
			return nil, fmt.Errorf("auth oauth: generate nonce: %w", err)
		}
	}

	// Mobile flows are detected by the presence of MobileRedirectURI.
	var flowSecret string
	var flowSecretHash string
	isMobileFlow := mobileRedirectURI != ""
	if isMobileFlow {
		flowSecret, err = generateBase64URLToken()
		if err != nil {
			return nil, fmt.Errorf("auth oauth: generate flow secret: %w", err)
		}
		flowSecretHash = hashBase64URL(flowSecret)
	}

	// Store OAuth state for validation during callback.
	oauthState := OAuthState{
		State:             state,
		Provider:          provider,
		CodeVerifier:      codeVerifier,
		Nonce:             nonce,
		RedirectURI:       redirectURI,
		MobileRedirectURI: mobileRedirectURI,
		FlowSecretHash:    flowSecretHash,
	}

	if err := a.saveOAuthState(ctx, oauthState); err != nil {
		return nil, fmt.Errorf("auth oauth: save state: %w", err)
	}

	authURL := p.GetAuthorizationURL(state, codeVerifier, nonce, redirectURI)

	a.log.InfoContext(ctx, "OAuth flow initiated",
		"provider", provider,
		"redirect_uri", redirectURI,
		"mobile_flow", isMobileFlow,
	)

	return &OAuthFlowResult{
		AuthorizationURL: authURL,
		State:            state,
		FlowSecret:       flowSecret,
	}, nil
}

// HandleOAuthCallback processes the OAuth callback after user authorization.
//
// If the OAuth account is already linked, the user is logged in. If a user
// with the same email exists but isn't linked, a [VerificationCodeRequestedEvent]
// is emitted and [ErrOAuthVerificationRequired] is returned. If no user exists,
// a new account is created and any registered [AfterUserCreationHook] functions
// are executed. The same non-transactional semantics as [Authenticator.Register]
// apply — see its documentation for details.
func (a *Authenticator) HandleOAuthCallback(ctx context.Context, provider, code, state, flowSecret string) (*OAuthCallbackResult, error) {
	if err := a.requireOAuth(); err != nil {
		return nil, err
	}

	p, ok := a.providers[provider]
	if !ok {
		return nil, ErrUnsupportedProvider
	}

	// Retrieve and validate stored state.
	oauthState, err := a.getOAuthState(ctx, state)
	if err != nil {
		a.log.WarnContext(ctx, "invalid OAuth state", "state", state, "error", err)
		return nil, ErrInvalidOAuthState
	}
	defer func() { _ = a.deleteOAuthState(ctx, state) }()

	if oauthState.Provider != provider {
		return nil, ErrInvalidOAuthState
	}

	// Verify flow secret.
	if oauthState.FlowSecretHash != "" {
		if flowSecret == "" || hashBase64URL(flowSecret) != oauthState.FlowSecretHash {
			return nil, ErrInvalidFlowSecret
		}
	}

	// Exchange code for tokens.
	tokenResp, err := p.ExchangeCode(ctx, code, oauthState.CodeVerifier, oauthState.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("auth oauth: exchange code: %w", err)
	}

	// Get user info from provider.
	userInfo, err := p.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("auth oauth: get user info: %w", err)
	}

	a.log.InfoContext(ctx, "OAuth user info retrieved",
		"provider", provider,
		"provider_user_id", userInfo.ProviderUserID,
		"email", userInfo.Email,
		"email_verified", userInfo.EmailVerified,
	)

	// Check if this OAuth account is already linked.
	existing, err := a.oauthRepo.GetByProvider(ctx, provider, userInfo.ProviderUserID)
	if err == nil {
		// OAuth account exists — check verification before allowing login.
		// Allow if either the account itself is verified (trusted provider or
		// completed VerifyOAuthLink) OR the user's email is verified through
		// our own flows.
		if !existing.AccountVerified {
			user, lookupErr := a.repositories.users.Get(ctx, existing.UserID)
			if lookupErr != nil {
				return nil, fmt.Errorf("auth oauth: lookup user: %w", lookupErr)
			}
			if !user.EmailVerified {
				a.logSecurityEvent(ctx, existing.UserID, SecEventOAuthLogin, SecStatusBlocked, map[string]any{
					"reason":   "account_not_verified",
					"provider": provider,
				})
				a.sendVerificationCode(ctx, user, PurposeEmailVerify)
				return nil, ErrOAuthAccountUnverified
			}
		}
		// Verified — log the user in.
		return a.oauthLogin(ctx, existing.UserID)
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("auth oauth: lookup account: %w", err)
	}

	// No existing OAuth account. Check if a user with this email exists.
	existingUser, err := a.repositories.users.GetByEmail(ctx, userInfo.Email)
	if err == nil {
		// User with this email exists but no OAuth link.
		// Require email verification before linking to prevent account takeover.
		return nil, a.initiatePendingOAuthLink(ctx, existingUser, userInfo, tokenResp, provider)
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("auth oauth: lookup user: %w", err)
	}

	// New user — create account + OAuth link + session.
	return a.oauthRegister(ctx, userInfo, tokenResp, provider)
}

// VerifyOAuthLink completes an OAuth account link after email verification.
// The code is the 6-digit verification code sent to the user's email.
func (a *Authenticator) VerifyOAuthLink(ctx context.Context, email, code string) (*OAuthCallbackResult, error) {
	if err := a.requireOAuth(); err != nil {
		return nil, err
	}

	// Look up pending link.
	stored, err := a.repositories.codes.Get(ctx, email, PurposePendingLink)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, ErrCodeInvalid
		}
		return nil, fmt.Errorf("auth oauth verify: lookup code: %w", err)
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		_ = a.repositories.codes.Delete(ctx, email, PurposePendingLink)
		return nil, ErrCodeExpired
	}

	codeHash, err := hashToken(code)
	if err != nil {
		return nil, ErrCodeInvalid
	}
	if !hashEquals(codeHash, stored.CodeHash) {
		if a.config.MaxVerificationAttempts > 0 {
			count, err := a.repositories.codes.IncrementAttempts(ctx, email, PurposePendingLink)
			if err != nil {
				a.log.WarnContext(ctx, "failed to increment verification attempts", "error", err)
			} else if count >= a.config.MaxVerificationAttempts {
				_ = a.repositories.codes.Delete(ctx, email, PurposePendingLink)
				a.log.WarnContext(ctx, "OAuth link verification code locked out", "attempts", count)
				return nil, ErrTooManyAttempts
			}
		}
		return nil, ErrCodeInvalid
	}

	// Parse pending link data.
	var pending PendingOAuthLink
	if err := json.Unmarshal(stored.Data, &pending); err != nil {
		return nil, fmt.Errorf("auth oauth verify: parse pending link: %w", err)
	}

	// Check for duplicate link.
	_, err = a.oauthRepo.GetByProvider(ctx, pending.Provider, pending.ProviderUserID)
	if err == nil {
		return nil, ErrOAuthAccountExists
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("auth oauth verify: check existing: %w", err)
	}

	// Determine user ID.
	userID := pending.UserID
	if userID == "" {
		user, err := a.repositories.users.GetByEmail(ctx, email)
		if err != nil {
			return nil, fmt.Errorf("auth oauth verify: lookup user: %w", err)
		}
		userID = user.UserID
	}

	// Create OAuth account link. Encrypted tokens pass through from pending link.
	// AccountVerified is true — user proved email ownership via verification code.
	if err := a.oauthRepo.Create(ctx, OAuthAccount{
		UserID:                userID,
		Provider:              pending.Provider,
		ProviderUserID:        pending.ProviderUserID,
		ProviderEmail:         pending.ProviderEmail,
		ProviderEmailVerified: pending.ProviderEmailVerified,
		AccountVerified:       true,
		LinkedAt:              time.Now().UTC(),
		AccessToken:           pending.AccessToken,
		RefreshToken:          pending.RefreshToken,
		TokenExpiresAt:        pending.TokenExpiresAt,
	}); err != nil {
		return nil, fmt.Errorf("auth oauth verify: create link: %w", err)
	}

	// Clean up.
	_ = a.repositories.codes.Delete(ctx, email, PurposePendingLink)

	// Mark email as verified (user proved ownership via code).
	if err := a.repositories.users.SetEmailVerified(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "failed to set email verified after OAuth link", "user_id", userID, "error", err)
	}

	a.log.InfoContext(ctx, "OAuth account linked and verified",
		"user_id", userID,
		"provider", pending.Provider,
	)
	a.logSecurityEvent(ctx, userID, SecEventOAuthLinkVerified, SecStatusSuccess, map[string]any{
		"email":    email,
		"provider": pending.Provider,
	})

	// Create session.
	return a.oauthLogin(ctx, userID)
}

// ---------------------------------------------------------------------------
// Internal OAuth helpers
// ---------------------------------------------------------------------------

func (a *Authenticator) requireOAuth() error {
	if a.providers == nil || a.oauthRepo == nil {
		return ErrOAuthNotConfigured
	}
	return nil
}

// oauthLogin creates a session for an existing user (OAuth login or link verification).
func (a *Authenticator) oauthLogin(ctx context.Context, userID string) (*OAuthCallbackResult, error) {
	user, err := a.repositories.users.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth oauth: lookup user: %w", err)
	}
	if !user.Active {
		return nil, ErrUserInactive
	}

	if err := a.repositories.users.SetLastLogin(ctx, user.UserID, time.Now().UTC()); err != nil {
		a.log.WarnContext(ctx, "failed to update last login", "user_id", user.UserID, "error", err)
	}
	a.logSecurityEvent(ctx, user.UserID, SecEventOAuthLogin, SecStatusSuccess, nil)

	result, err := a.createSession(ctx, user)
	if err != nil {
		return nil, err
	}

	return &OAuthCallbackResult{
		UserID:       result.User.UserID,
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		IsNewUser:    false,
	}, nil
}

// oauthRegister creates a new user, links the OAuth account, and creates a session.
//
// If the provider does not report a verified email, the user and OAuth link
// are still created, but the session is blocked and a verification code is
// sent so the user can verify immediately.
func (a *Authenticator) oauthRegister(ctx context.Context, userInfo *oauth.UserInfo, tokenResp *oauth.TokenResponse, provider string) (*OAuthCallbackResult, error) {
	user, err := a.repositories.users.Create(ctx, CreateUserInput{
		Email:         userInfo.Email,
		DisplayName:   userInfo.Name,
		EmailVerified: userInfo.EmailVerified,
	})
	if err != nil {
		return nil, fmt.Errorf("auth oauth: create user: %w", err)
	}

	account, err := a.buildOAuthAccount(ctx, user.UserID, userInfo, tokenResp, a.providers[provider])
	if err != nil {
		return nil, fmt.Errorf("auth oauth: build account: %w", err)
	}

	if err := a.oauthRepo.Create(ctx, account); err != nil {
		return nil, fmt.Errorf("auth oauth: create link: %w", err)
	}

	// Run after user creation hooks (e.g., resolve invitations, provision defaults).
	if err := a.runAfterUserCreationHooks(ctx, user); err != nil {
		return nil, fmt.Errorf("auth oauth: %w", err)
	}

	a.log.InfoContext(ctx, "new OAuth user registered",
		"user_id", user.UserID,
		"email", userInfo.Email,
		"provider", provider,
	)

	// If the provider email isn't verified, block login and send verification.
	if !userInfo.EmailVerified {
		a.logSecurityEvent(ctx, user.UserID, SecEventOAuthRegister, SecStatusBlocked, map[string]any{
			"reason":   "email_not_verified",
			"email":    userInfo.Email,
			"provider": provider,
		})
		a.sendVerificationCode(ctx, user, PurposeEmailVerify)
		return nil, ErrOAuthAccountUnverified
	}

	a.logSecurityEvent(ctx, user.UserID, SecEventOAuthRegister, SecStatusSuccess, map[string]any{
		"email":    userInfo.Email,
		"provider": provider,
	})

	if err := a.repositories.users.SetLastLogin(ctx, user.UserID, time.Now().UTC()); err != nil {
		a.log.WarnContext(ctx, "failed to update last login", "user_id", user.UserID, "error", err)
	}

	result, err := a.createSession(ctx, user)
	if err != nil {
		return nil, err
	}

	return &OAuthCallbackResult{
		UserID:       result.User.UserID,
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		IsNewUser:    true,
	}, nil
}

// initiatePendingOAuthLink stores a pending link and emits a verification event.
// Returns ErrOAuthVerificationRequired.
func (a *Authenticator) initiatePendingOAuthLink(ctx context.Context, user User, userInfo *oauth.UserInfo, tokenResp *oauth.TokenResponse, provider string) error {
	code, err := generateVerificationCode()
	if err != nil {
		return fmt.Errorf("auth oauth: generate verification code: %w", err)
	}

	pending := PendingOAuthLink{
		Email:                 userInfo.Email,
		Provider:              provider,
		ProviderUserID:        userInfo.ProviderUserID,
		ProviderEmail:         userInfo.Email,
		ProviderEmailVerified: userInfo.EmailVerified,
		UserID:                user.UserID,
	}

	// Encrypt provider tokens if configured.
	if a.encrypter != nil && tokenResp != nil {
		if tokenResp.AccessToken != "" {
			enc, err := a.encrypter.Encrypt(tokenResp.AccessToken)
			if err != nil {
				return fmt.Errorf("auth oauth: encrypt access token: %w", err)
			}
			pending.AccessToken = enc
		}
		if tokenResp.RefreshToken != "" {
			enc, err := a.encrypter.Encrypt(tokenResp.RefreshToken)
			if err != nil {
				return fmt.Errorf("auth oauth: encrypt refresh token: %w", err)
			}
			pending.RefreshToken = enc
		}
		if tokenResp.ExpiresIn > 0 {
			pending.TokenExpiresAt = time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		}
	}

	data, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("auth oauth: marshal pending link: %w", err)
	}

	// Delete any existing pending link for this email.
	_ = a.repositories.codes.Delete(ctx, userInfo.Email, PurposePendingLink)

	if err := a.repositories.codes.Create(ctx, VerificationCode{
		Identifier: userInfo.Email,
		CodeHash:   mustHashToken(code),
		Purpose:    PurposePendingLink,
		ExpiresAt:  time.Now().UTC().Add(a.config.VerificationCodeExpiry),
		Data:       data,
	}); err != nil {
		return fmt.Errorf("auth oauth: store pending link: %w", err)
	}

	// Emit event for email delivery.
	if err := a.bus.Emit(ctx, OAuthLinkVerificationRequestedEvent{
		UserID:      user.UserID,
		Email:       userInfo.Email,
		DisplayName: user.DisplayName,
		Provider:    provider,
		Code:        code,
		ExpiresIn:   a.config.VerificationCodeExpiry.String(),
	}); err != nil {
		a.log.WarnContext(ctx, "failed to emit OAuth link verification event", "user_id", user.UserID, "error", err)
	}

	a.log.InfoContext(ctx, "OAuth link verification requested",
		"user_id", user.UserID,
		"email", userInfo.Email,
		"provider", provider,
	)

	return ErrOAuthVerificationRequired
}

// buildOAuthAccount creates an OAuthAccount, encrypting provider tokens if configured.
//
// AccountVerified is set to true only when the provider reports a verified email
// AND the provider is trusted (via [oauth.Provider.TrustEmailVerification]).
// Otherwise it starts false and is set to true through our verification flows.
func (a *Authenticator) buildOAuthAccount(ctx context.Context, userID string, userInfo *oauth.UserInfo, tokenResp *oauth.TokenResponse, p oauth.Provider) (OAuthAccount, error) {
	account := OAuthAccount{
		UserID:                userID,
		Provider:              p.Name(),
		ProviderUserID:        userInfo.ProviderUserID,
		ProviderEmail:         userInfo.Email,
		ProviderEmailVerified: userInfo.EmailVerified,
		AccountVerified:       userInfo.EmailVerified && p.TrustEmailVerification(),
		LinkedAt:              time.Now().UTC(),
	}

	if tokenResp != nil {
		account.TokenType = tokenResp.TokenType
		account.Scope = tokenResp.Scopes
		account.IDToken = tokenResp.IDToken

		if a.encrypter != nil {
			if tokenResp.AccessToken != "" {
				enc, err := a.encrypter.Encrypt(tokenResp.AccessToken)
				if err != nil {
					return OAuthAccount{}, fmt.Errorf("encrypt access token: %w", err)
				}
				account.AccessToken = enc
			}
			if tokenResp.RefreshToken != "" {
				enc, err := a.encrypter.Encrypt(tokenResp.RefreshToken)
				if err != nil {
					return OAuthAccount{}, fmt.Errorf("encrypt refresh token: %w", err)
				}
				account.RefreshToken = enc
			}
		}

		if tokenResp.ExpiresIn > 0 {
			account.TokenExpiresAt = time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		}
	}

	return account, nil
}

// ---------------------------------------------------------------------------
// OAuth state storage (uses VerificationCode with PurposeOAuthState)
// ---------------------------------------------------------------------------

func (a *Authenticator) saveOAuthState(ctx context.Context, state OAuthState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	return a.repositories.codes.Create(ctx, VerificationCode{
		Identifier: state.State,
		CodeHash:   mustHashToken(state.State),
		Purpose:    PurposeOAuthState,
		ExpiresAt:  time.Now().UTC().Add(a.config.OAuthStateExpiry),
		Data:       data,
	})
}

func (a *Authenticator) getOAuthState(ctx context.Context, state string) (OAuthState, error) {
	stored, err := a.repositories.codes.Get(ctx, state, PurposeOAuthState)
	if err != nil {
		return OAuthState{}, err
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		_ = a.repositories.codes.Delete(ctx, state, PurposeOAuthState)
		return OAuthState{}, ErrCodeExpired
	}

	var oauthState OAuthState
	if err := json.Unmarshal(stored.Data, &oauthState); err != nil {
		return OAuthState{}, fmt.Errorf("unmarshal state: %w", err)
	}

	return oauthState, nil
}

func (a *Authenticator) deleteOAuthState(ctx context.Context, state string) error {
	return a.repositories.codes.Delete(ctx, state, PurposeOAuthState)
}

// GetMobileRedirectURIForState retrieves the stored mobile redirect URI for a given OAuth state.
// Returns an error if the state is invalid, expired, or has no mobile redirect URI.
func (a *Authenticator) GetMobileRedirectURIForState(ctx context.Context, state string) (string, error) {
	stateData, err := a.getOAuthState(ctx, state)
	if err != nil {
		return "", err
	}

	if stateData.MobileRedirectURI == "" {
		return "", fmt.Errorf("auth oauth: state has no mobile redirect URI")
	}

	return stateData.MobileRedirectURI, nil
}
