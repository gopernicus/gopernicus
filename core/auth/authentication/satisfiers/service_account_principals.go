package satisfiers

import (
	"context"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/serviceaccounts"
)

var _ authentication.ServiceAccountPrincipalRepository = (*ServiceAccountPrincipalSatisfier)(nil)

type serviceAccountPrincipalRepo interface {
	GetPrincipalInfo(ctx context.Context, serviceAccountID string) (serviceaccounts.GetPrincipalInfoResult, error)
}

// ServiceAccountPrincipalSatisfier satisfies authentication.ServiceAccountPrincipalRepository
// using the generated service_accounts repository.
type ServiceAccountPrincipalSatisfier struct {
	repo serviceAccountPrincipalRepo
}

func NewServiceAccountPrincipalSatisfier(repo serviceAccountPrincipalRepo) *ServiceAccountPrincipalSatisfier {
	return &ServiceAccountPrincipalSatisfier{repo: repo}
}

func (s *ServiceAccountPrincipalSatisfier) GetPrincipalInfo(ctx context.Context, serviceAccountID string) (authentication.ServiceAccountPrincipal, error) {
	info, err := s.repo.GetPrincipalInfo(ctx, serviceAccountID)
	if err != nil {
		return authentication.ServiceAccountPrincipal{}, err
	}

	return authentication.ServiceAccountPrincipal{
		ActAsUser:   info.ActAsUser,
		OwnerUserID: info.OwnerUserID,
	}, nil
}
