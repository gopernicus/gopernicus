package authentication

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// RequestBudget independently bounds a subject and the caller's trusted IP.
// A zero limit selects the operation's default; partial/negative limits are invalid.
type RequestBudget struct {
	PerSubject ratelimiter.Limit
	PerIP      ratelimiter.Limit
}

// AuthenticationLimits is host policy for password proofs and sensitive starts.
// SensitiveCode is shared across operations so changing purpose does not reset it.
type AuthenticationLimits struct {
	Login             RequestBudget
	PasswordReset     RequestBudget
	SensitiveCode     RequestBudget
	SensitivePassword RequestBudget
}

func (l AuthenticationLimits) withDefaults() AuthenticationLimits {
	for _, item := range []struct {
		budget  *RequestBudget
		subject int
		ip      int
	}{
		{&l.Login, 5, 30},
		{&l.PasswordReset, 3, 20},
		{&l.SensitiveCode, 5, 30},
		{&l.SensitivePassword, 5, 30},
	} {
		if item.budget.PerSubject == (ratelimiter.Limit{}) {
			item.budget.PerSubject = ratelimiter.PerMinute(item.subject)
		}
		if item.budget.PerIP == (ratelimiter.Limit{}) {
			item.budget.PerIP = ratelimiter.PerMinute(item.ip)
		}
	}
	return l
}

func (l AuthenticationLimits) Validate() error {
	l = l.withDefaults()
	for _, item := range []struct {
		name   string
		budget RequestBudget
	}{
		{"Login", l.Login},
		{"PasswordReset", l.PasswordReset},
		{"SensitiveCode", l.SensitiveCode},
		{"SensitivePassword", l.SensitivePassword},
	} {
		for _, limit := range []ratelimiter.Limit{item.budget.PerSubject, item.budget.PerIP} {
			if _, err := limit.Normalize(); err != nil {
				return fmt.Errorf("authentication limits %s: %w", item.name, err)
			}
		}
	}
	return nil
}

func (s *Service) allowAuthenticationRequest(ctx context.Context, operation, subject string, budget RequestBudget) error {
	keys := []struct {
		key   string
		limit ratelimiter.Limit
	}{
		{"authentication:" + operation + ":subject:" + s.identifierDigest("subject", subject), budget.PerSubject},
		{"authentication:" + operation + ":ip:" + clientInfoFromContext(ctx).ip, budget.PerIP},
	}
	for _, item := range keys {
		result, err := s.limiter.Allow(ctx, item.key, item.limit)
		if err != nil {
			return err
		}
		if !result.Allowed {
			return ErrRateLimited
		}
	}
	return nil
}

func (s *Service) sensitiveCodeBudget(ctx context.Context, userID string) error {
	return s.allowAuthenticationRequest(ctx, "sensitive_code", userID, s.authenticationLimits.SensitiveCode)
}

func (s *Service) sensitivePasswordBudget(ctx context.Context, userID string) error {
	return s.allowAuthenticationRequest(ctx, "sensitive_password", userID, s.authenticationLimits.SensitivePassword)
}
