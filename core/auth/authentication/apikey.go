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
