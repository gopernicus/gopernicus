package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/userpasswords"
)

var _ authentication.PasswordRepository = (*PasswordSatisfier)(nil)

type passwordRepo interface {
	Get(ctx context.Context, userID string) (userpasswords.UserPassword, error)
	Create(ctx context.Context, input userpasswords.CreateUserPassword) (userpasswords.UserPassword, error)
	Update(ctx context.Context, userID string, input userpasswords.UpdateUserPassword) (userpasswords.UserPassword, error)
	SetVerified(ctx context.Context, updatedAt time.Time, userID string) error
}

// PasswordSatisfier satisfies authentication.PasswordRepository using the generated user_passwords repository.
type PasswordSatisfier struct {
	repo passwordRepo
}

func NewPasswordSatisfier(repo passwordRepo) *PasswordSatisfier {
	return &PasswordSatisfier{repo: repo}
}

func (s *PasswordSatisfier) GetByUserID(ctx context.Context, userID string) (authentication.Password, error) {
	p, err := s.repo.Get(ctx, userID)
	if err != nil {
		return authentication.Password{}, err
	}
	return authentication.Password{
		UserID:   p.UserID,
		Hash:     p.PasswordHash,
		Verified: p.PasswordVerified,
	}, nil
}

func (s *PasswordSatisfier) Create(ctx context.Context, userID, hash string) error {
	_, err := s.repo.Create(ctx, userpasswords.CreateUserPassword{
		UserID:            userID,
		PasswordHash:      hash,
		PasswordChangedAt: time.Now().UTC(),
	})
	return err
}

func (s *PasswordSatisfier) Update(ctx context.Context, userID, hash string) error {
	now := time.Now().UTC()
	_, err := s.repo.Update(ctx, userID, userpasswords.UpdateUserPassword{
		PasswordHash:      &hash,
		PasswordChangedAt: &now,
	})
	return err
}

func (s *PasswordSatisfier) SetVerified(ctx context.Context, userID string) error {
	return s.repo.SetVerified(ctx, time.Now().UTC(), userID)
}
