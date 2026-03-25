package satisfiers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/oauthaccounts"
)

var _ authentication.OAuthAccountRepository = (*OAuthAccountSatisfier)(nil)

type oauthAccountRepo interface {
	GetByProvider(ctx context.Context, provider string, providerUserID string) (oauthaccounts.OauthAccount, error)
	ListByUser(ctx context.Context, parentUserID string, limit int) ([]oauthaccounts.OauthAccount, error)
	Create(ctx context.Context, input oauthaccounts.CreateOauthAccount) (oauthaccounts.OauthAccount, error)
	DeleteByUserAndProvider(ctx context.Context, parentUserID string, provider string) error
}

// OAuthAccountSatisfier satisfies authentication.OAuthAccountRepository
// using the generated oauth_accounts repository.
type OAuthAccountSatisfier struct {
	repo oauthAccountRepo
}

func NewOAuthAccountSatisfier(repo oauthAccountRepo) *OAuthAccountSatisfier {
	return &OAuthAccountSatisfier{repo: repo}
}

func (s *OAuthAccountSatisfier) GetByProvider(ctx context.Context, provider, providerUserID string) (authentication.OAuthAccount, error) {
	oa, err := s.repo.GetByProvider(ctx, provider, providerUserID)
	if err != nil {
		return authentication.OAuthAccount{}, err
	}
	return toAuthOAuthAccount(oa), nil
}

func (s *OAuthAccountSatisfier) Create(ctx context.Context, account authentication.OAuthAccount) error {
	var profileData *json.RawMessage
	if account.ProfileData != nil {
		raw := json.RawMessage(account.ProfileData)
		profileData = &raw
	}
	_, err := s.repo.Create(ctx, oauthaccounts.CreateOauthAccount{
		ParentUserID:          account.UserID,
		Provider:              account.Provider,
		ProviderUserID:        account.ProviderUserID,
		ProviderEmail:         strPtr(account.ProviderEmail),
		ProviderEmailVerified: &account.ProviderEmailVerified,
		AccountVerified:       account.AccountVerified,
		AccessToken:           strPtr(account.AccessToken),
		RefreshToken:          strPtr(account.RefreshToken),
		TokenExpiresAt:        timePtr(account.TokenExpiresAt),
		TokenType:             strPtr(account.TokenType),
		Scope:                 strPtr(account.Scope),
		IDToken:               strPtr(account.IDToken),
		ProfileData:           profileData,
		LinkedAt:              account.LinkedAt,
	})
	return err
}

func (s *OAuthAccountSatisfier) GetByUserID(ctx context.Context, userID string) ([]authentication.OAuthAccount, error) {
	accounts, err := s.repo.ListByUser(ctx, userID, 20)
	if err != nil {
		return nil, err
	}
	result := make([]authentication.OAuthAccount, len(accounts))
	for i, oa := range accounts {
		result[i] = toAuthOAuthAccount(oa)
	}
	return result, nil
}

func (s *OAuthAccountSatisfier) Delete(ctx context.Context, userID, provider string) error {
	return s.repo.DeleteByUserAndProvider(ctx, userID, provider)
}

func toAuthOAuthAccount(oa oauthaccounts.OauthAccount) authentication.OAuthAccount {
	a := authentication.OAuthAccount{
		UserID:          oa.ParentUserID,
		Provider:        oa.Provider,
		ProviderUserID:  oa.ProviderUserID,
		AccountVerified: oa.AccountVerified,
		LinkedAt:        oa.LinkedAt,
	}
	if oa.ProviderEmail != nil {
		a.ProviderEmail = *oa.ProviderEmail
	}
	if oa.ProviderEmailVerified != nil {
		a.ProviderEmailVerified = *oa.ProviderEmailVerified
	}
	if oa.AccessToken != nil {
		a.AccessToken = *oa.AccessToken
	}
	if oa.RefreshToken != nil {
		a.RefreshToken = *oa.RefreshToken
	}
	if oa.TokenExpiresAt != nil {
		a.TokenExpiresAt = *oa.TokenExpiresAt
	}
	if oa.TokenType != nil {
		a.TokenType = *oa.TokenType
	}
	if oa.Scope != nil {
		a.Scope = *oa.Scope
	}
	if oa.IDToken != nil {
		a.IDToken = *oa.IDToken
	}
	if oa.ProfileData != nil {
		a.ProfileData = json.RawMessage(*oa.ProfileData)
	}
	return a
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
