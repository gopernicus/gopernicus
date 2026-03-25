package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/apikeys"
)

var _ authentication.APIKeyRepository = (*APIKeySatisfier)(nil)

type apiKeyRepo interface {
	GetByHash(ctx context.Context, keyHash string, now time.Time) (apikeys.APIKey, error)
}

// APIKeySatisfier satisfies authentication.APIKeyRepository using the generated api_keys repository.
type APIKeySatisfier struct {
	repo apiKeyRepo
}

func NewAPIKeySatisfier(repo apiKeyRepo) *APIKeySatisfier {
	return &APIKeySatisfier{repo: repo}
}

func (s *APIKeySatisfier) GetByHash(ctx context.Context, hash string) (authentication.APIKey, error) {
	ak, err := s.repo.GetByHash(ctx, hash, time.Now().UTC())
	if err != nil {
		return authentication.APIKey{}, err
	}
	return authentication.APIKey{
		ID:               ak.APIKeyID,
		ServiceAccountID: ak.ParentServiceAccountID,
		KeyHash:          ak.KeyHash,
		ExpiresAt:        ak.ExpiresAt,
		Active:           ak.RecordState == "active",
	}, nil
}
