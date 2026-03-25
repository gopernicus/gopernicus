package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/verificationtokens"
)

var _ authentication.VerificationTokenRepository = (*VerificationTokenSatisfier)(nil)

type verificationTokenRepo interface {
	Create(ctx context.Context, input verificationtokens.CreateVerificationToken) (verificationtokens.VerificationToken, error)
	GetByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string, now time.Time) (verificationtokens.VerificationToken, error)
	DeleteByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string) error
	DeleteByUserIDAndPurpose(ctx context.Context, userID string, purpose string) error
}

// VerificationTokenSatisfier satisfies authentication.VerificationTokenRepository
// using the generated verification_tokens repository.
type VerificationTokenSatisfier struct {
	repo verificationTokenRepo
}

func NewVerificationTokenSatisfier(repo verificationTokenRepo) *VerificationTokenSatisfier {
	return &VerificationTokenSatisfier{repo: repo}
}

func (s *VerificationTokenSatisfier) Create(ctx context.Context, token authentication.VerificationToken) error {
	_, err := s.repo.Create(ctx, verificationtokens.CreateVerificationToken{
		TokenHash:  token.TokenHash,
		Purpose:    token.Purpose,
		Identifier: token.Identifier,
		UserID:     &token.UserID,
		ExpiresAt:  token.ExpiresAt,
	})
	return err
}

func (s *VerificationTokenSatisfier) Get(ctx context.Context, identifier, purpose string) (authentication.VerificationToken, error) {
	vt, err := s.repo.GetByIdentifierAndPurpose(ctx, identifier, purpose, time.Now().UTC())
	if err != nil {
		return authentication.VerificationToken{}, err
	}
	t := authentication.VerificationToken{
		Identifier: vt.Identifier,
		TokenHash:  vt.TokenHash,
		Purpose:    vt.Purpose,
		ExpiresAt:  vt.ExpiresAt,
	}
	if vt.UserID != nil {
		t.UserID = *vt.UserID
	}
	return t, nil
}

func (s *VerificationTokenSatisfier) Delete(ctx context.Context, identifier, purpose string) error {
	return s.repo.DeleteByIdentifierAndPurpose(ctx, identifier, purpose)
}

func (s *VerificationTokenSatisfier) DeleteByUserIDAndPurpose(ctx context.Context, userID, purpose string) error {
	return s.repo.DeleteByUserIDAndPurpose(ctx, userID, purpose)
}
