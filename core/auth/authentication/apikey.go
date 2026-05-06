package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// AuthenticateAPIKey validates an API key and returns the service account ID.
//
// The key is hashed and looked up via the [APIKeyRepository] interface. The key
// must be active and not expired.
//
// Satisfies the [httpmid.APIKeyAuthenticator] interface.
func (a *Authenticator) AuthenticateAPIKey(ctx context.Context, key string) (string, error) {
	if a.apiKeys == nil {
		return "", ErrAPIKeysNotConfigured
	}

	hash, err := hashToken(key)
	if err != nil {
		return "", ErrAPIKeyNotFound
	}

	apiKey, err := a.apiKeys.GetByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			a.logSecurityEvent(ctx, "", SecEventAPIKeyAuth, SecStatusFailure, map[string]any{
				"reason": "key_not_found",
			})
			return "", ErrAPIKeyNotFound
		}
		return "", fmt.Errorf("auth api key lookup: %w", err)
	}

	if !apiKey.Active {
		a.logSecurityEvent(ctx, apiKey.ServiceAccountID, SecEventAPIKeyAuth, SecStatusBlocked, map[string]any{
			"reason": "key_inactive",
			"key_id": apiKey.ID,
		})
		return "", ErrAPIKeyInactive
	}

	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now().UTC()) {
		a.logSecurityEvent(ctx, apiKey.ServiceAccountID, SecEventAPIKeyAuth, SecStatusFailure, map[string]any{
			"reason": "key_expired",
			"key_id": apiKey.ID,
		})
		return "", ErrAPIKeyExpired
	}

	a.logSecurityEvent(ctx, apiKey.ServiceAccountID, SecEventAPIKeyAuth, SecStatusSuccess, map[string]any{
		"key_id": apiKey.ID,
	})
	return apiKey.ServiceAccountID, nil
}

// ResolveAPIKeyPrincipal returns the effective principal for an API key's
// service account. For normal service accounts, returns the service account
// as the principal. For personal service accounts (act_as_user=true), returns
// the owning user as the principal.
//
// If [WithServiceAccountPrincipals] is not configured, always returns the
// service account as the principal (existing behavior).
//
// Satisfies the [httpmid.APIKeyPrincipalResolver] interface.
func (a *Authenticator) ResolveAPIKeyPrincipal(ctx context.Context, serviceAccountID string) (Principal, error) {
	if a.serviceAccountPrincipals == nil {
		return Principal{Type: "service_account", ID: serviceAccountID}, nil
	}

	info, err := a.serviceAccountPrincipals.GetPrincipalInfo(ctx, serviceAccountID)
	if err != nil {
		return Principal{}, fmt.Errorf("resolve principal: %w", err)
	}

	if info.ActAsUser && info.OwnerUserID != "" {
		return Principal{Type: "user", ID: info.OwnerUserID}, nil
	}

	return Principal{Type: "service_account", ID: serviceAccountID}, nil
}

// Principal identifies the effective subject for authentication.
// Returned by [ResolveAPIKeyPrincipal].
type Principal struct {
	Type string // "user" or "service_account"
	ID   string
}
