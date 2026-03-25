package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
)

var _ authentication.UserRepository = (*UserSatisfier)(nil)

type userRepo interface {
	Get(ctx context.Context, userID string) (users.User, error)
	GetByEmail(ctx context.Context, email string) (users.User, error)
	Create(ctx context.Context, input users.CreateUser) (users.User, error)
	SetEmailVerified(ctx context.Context, updatedAt time.Time, userID string) error
	SetLastLogin(ctx context.Context, lastLoginAt time.Time, updatedAt time.Time, userID string) error
}

// UserSatisfier satisfies authentication.UserRepository using the generated users repository.
type UserSatisfier struct {
	repo userRepo
}

func NewUserSatisfier(repo userRepo) *UserSatisfier {
	return &UserSatisfier{repo: repo}
}

func (s *UserSatisfier) Get(ctx context.Context, id string) (authentication.User, error) {
	u, err := s.repo.Get(ctx, id)
	if err != nil {
		return authentication.User{}, err
	}
	return toAuthUser(u), nil
}

func (s *UserSatisfier) GetByEmail(ctx context.Context, email string) (authentication.User, error) {
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return authentication.User{}, err
	}
	return toAuthUser(u), nil
}

func (s *UserSatisfier) Create(ctx context.Context, input authentication.CreateUserInput) (authentication.User, error) {
	u, err := s.repo.Create(ctx, users.CreateUser{
		Email:         input.Email,
		DisplayName:   &input.DisplayName,
		EmailVerified: input.EmailVerified,
	})
	if err != nil {
		return authentication.User{}, err
	}
	return toAuthUser(u), nil
}

func (s *UserSatisfier) SetEmailVerified(ctx context.Context, id string) error {
	return s.repo.SetEmailVerified(ctx, time.Now().UTC(), id)
}

func (s *UserSatisfier) SetLastLogin(ctx context.Context, id string, at time.Time) error {
	return s.repo.SetLastLogin(ctx, at, time.Now().UTC(), id)
}

func toAuthUser(u users.User) authentication.User {
	dn := ""
	if u.DisplayName != nil {
		dn = *u.DisplayName
	}
	return authentication.User{
		UserID:        u.UserID,
		Email:         u.Email,
		DisplayName:   dn,
		EmailVerified: u.EmailVerified,
		Active:        u.RecordState == "active",
	}
}
