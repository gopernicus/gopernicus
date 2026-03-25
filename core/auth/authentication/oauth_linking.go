package authentication

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// LinkOAuthAccount links an OAuth provider to an existing authenticated user.
//
// The code and state come from the OAuth callback — the user must have already
// completed the authorization flow via [InitiateOAuthFlow] with their UserID
// set in the state.
func (a *Authenticator) LinkOAuthAccount(ctx context.Context, userID, provider, code, state string) error {
	if err := a.requireOAuth(); err != nil {
		return err
	}

	p, ok := a.providers[provider]
	if !ok {
		return ErrUnsupportedProvider
	}

	// Retrieve and validate stored state.
	oauthState, err := a.getOAuthState(ctx, state)
	if err != nil {
		return ErrInvalidOAuthState
	}
	defer func() { _ = a.deleteOAuthState(ctx, state) }()

	if oauthState.Provider != provider {
		return ErrInvalidOAuthState
	}

	// Exchange code for tokens.
	tokenResp, err := p.ExchangeCode(ctx, code, oauthState.CodeVerifier, oauthState.RedirectURI)
	if err != nil {
		return fmt.Errorf("auth oauth link: exchange code: %w", err)
	}

	// Get user info from provider.
	userInfo, err := p.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return fmt.Errorf("auth oauth link: get user info: %w", err)
	}

	// Check if this OAuth account is already linked to another user.
	existing, err := a.oauthRepo.GetByProvider(ctx, provider, userInfo.ProviderUserID)
	if err == nil {
		if existing.UserID != userID {
			return ErrOAuthAccountExists
		}
		// Already linked to this user — nothing to do.
		return nil
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return fmt.Errorf("auth oauth link: check existing: %w", err)
	}

	// Create new link.
	account, err := a.buildOAuthAccount(ctx, userID, userInfo, tokenResp, p)
	if err != nil {
		return fmt.Errorf("auth oauth link: build account: %w", err)
	}
	if err := a.oauthRepo.Create(ctx, account); err != nil {
		return fmt.Errorf("auth oauth link: create: %w", err)
	}

	a.log.InfoContext(ctx, "OAuth account linked",
		"user_id", userID,
		"provider", provider,
		"provider_user_id", userInfo.ProviderUserID,
	)
	a.logSecurityEvent(ctx, userID, SecEventOAuthLinked, SecStatusSuccess, map[string]any{
		"provider": provider,
	})

	return nil
}

// UnlinkOAuthAccount removes an OAuth provider from a user's account.
//
// Prevents unlinking if it's the user's only authentication method
// (no password and no other OAuth providers).
func (a *Authenticator) UnlinkOAuthAccount(ctx context.Context, userID, provider string) error {
	if err := a.requireOAuth(); err != nil {
		return err
	}

	// Check if user has a password.
	_, err := a.repositories.passwords.GetByUserID(ctx, userID)
	hasPassword := err == nil

	// Get all linked accounts.
	accounts, err := a.oauthRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth oauth unlink: list accounts: %w", err)
	}

	// Verify the provider is linked.
	found := false
	otherProviders := 0
	for _, acct := range accounts {
		if acct.Provider == provider {
			found = true
		} else {
			otherProviders++
		}
	}
	if !found {
		return ErrOAuthAccountNotLinked
	}

	// Don't allow unlinking the last auth method.
	if !hasPassword && otherProviders == 0 {
		return ErrCannotUnlinkLastMethod
	}

	if err := a.oauthRepo.Delete(ctx, userID, provider); err != nil {
		return fmt.Errorf("auth oauth unlink: delete: %w", err)
	}

	a.log.InfoContext(ctx, "OAuth account unlinked",
		"user_id", userID,
		"provider", provider,
	)
	a.logSecurityEvent(ctx, userID, SecEventOAuthUnlinked, SecStatusSuccess, map[string]any{
		"provider": provider,
	})

	return nil
}

// GetLinkedAccounts returns all OAuth providers linked to a user.
func (a *Authenticator) GetLinkedAccounts(ctx context.Context, userID string) ([]OAuthAccount, error) {
	if err := a.requireOAuth(); err != nil {
		return nil, err
	}

	accounts, err := a.oauthRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth oauth: list accounts: %w", err)
	}

	return accounts, nil
}
