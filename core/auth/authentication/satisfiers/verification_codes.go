package satisfiers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/verificationcodes"
)

var _ authentication.VerificationCodeRepository = (*VerificationCodeSatisfier)(nil)

type verificationCodeRepo interface {
	Create(ctx context.Context, input verificationcodes.CreateVerificationCode) (verificationcodes.VerificationCode, error)
	GetByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string, now time.Time) (verificationcodes.VerificationCode, error)
	DeleteByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string) error
	IncrementAttempts(ctx context.Context, identifier string, purpose string) (verificationcodes.IncrementAttemptsResult, error)
}

// VerificationCodeSatisfier satisfies authentication.VerificationCodeRepository
// using the generated verification_codes repository.
type VerificationCodeSatisfier struct {
	repo verificationCodeRepo
}

func NewVerificationCodeSatisfier(repo verificationCodeRepo) *VerificationCodeSatisfier {
	return &VerificationCodeSatisfier{repo: repo}
}

func (s *VerificationCodeSatisfier) Create(ctx context.Context, code authentication.VerificationCode) error {
	var data *json.RawMessage
	if code.Data != nil {
		raw := json.RawMessage(code.Data)
		data = &raw
	}
	_, err := s.repo.Create(ctx, verificationcodes.CreateVerificationCode{
		Identifier:   code.Identifier,
		CodeHash:     code.CodeHash,
		Purpose:      code.Purpose,
		Data:         data,
		AttemptCount: code.AttemptCount,
		ExpiresAt:    code.ExpiresAt,
	})
	return err
}

func (s *VerificationCodeSatisfier) Get(ctx context.Context, identifier, purpose string) (authentication.VerificationCode, error) {
	vc, err := s.repo.GetByIdentifierAndPurpose(ctx, identifier, purpose, time.Now().UTC())
	if err != nil {
		return authentication.VerificationCode{}, err
	}
	c := authentication.VerificationCode{
		Identifier:   vc.Identifier,
		CodeHash:     vc.CodeHash,
		Purpose:      vc.Purpose,
		ExpiresAt:    vc.ExpiresAt,
		AttemptCount: vc.AttemptCount,
	}
	if vc.Data != nil {
		c.Data = []byte(*vc.Data)
	}
	return c, nil
}

func (s *VerificationCodeSatisfier) Delete(ctx context.Context, identifier, purpose string) error {
	return s.repo.DeleteByIdentifierAndPurpose(ctx, identifier, purpose)
}

func (s *VerificationCodeSatisfier) IncrementAttempts(ctx context.Context, identifier, purpose string) (int, error) {
	result, err := s.repo.IncrementAttempts(ctx, identifier, purpose)
	if err != nil {
		return 0, err
	}
	return result.AttemptCount, nil
}
