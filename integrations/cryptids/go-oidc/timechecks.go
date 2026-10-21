package gooidc

import (
	"fmt"
	"slices"
	"time"
)

// checkTimes requires a numeric exp and validates exp, nbf and iat with
// ClockTolerance. nbf and iat are optional but must be numeric when present;
// explicit null is invalid. Dates must fall in calendar years 0001–9999.
func checkTimes(claims map[string]any, now time.Time) error {
	exp, present, err := numericDate(claims, "exp")
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("%w: missing exp", ErrInvalidToken)
	}
	nbf, hasNBF, err := numericDate(claims, "nbf")
	if err != nil {
		return err
	}
	iat, hasIAT, err := numericDate(claims, "iat")
	if err != nil {
		return err
	}

	nowSecs := float64(now.UnixNano()) / float64(time.Second)
	tolerance := ClockTolerance.Seconds()
	if nowSecs > exp+tolerance {
		return fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	if hasNBF && nbf-tolerance > nowSecs {
		return fmt.Errorf("%w: not yet valid", ErrInvalidToken)
	}
	if hasIAT && iat-tolerance > nowSecs {
		return fmt.Errorf("%w: issued in the future", ErrInvalidToken)
	}
	return nil
}

func numericDate(claims map[string]any, name string) (float64, bool, error) {
	raw, present := claims[name]
	if !present {
		return 0, false, nil
	}
	value, ok := raw.(float64)
	if !ok {
		return 0, true, fmt.Errorf("%w: %s is not a NumericDate", ErrInvalidToken, name)
	}
	if value < minNumericDate || value >= maxNumericDate {
		return 0, true, fmt.Errorf("%w: %s outside calendar years 0001–9999", ErrInvalidToken, name)
	}
	return value, true, nil
}

// checkAudience accepts aud as a string or an array of strings containing any
// configured audience.
func checkAudience(claims map[string]any, audiences []string) error {
	switch aud := claims["aud"].(type) {
	case string:
		if slices.Contains(audiences, aud) {
			return nil
		}
	case []any:
		for _, entry := range aud {
			if s, ok := entry.(string); ok && slices.Contains(audiences, s) {
				return nil
			}
		}
	case nil:
		return fmt.Errorf("%w: missing aud", ErrInvalidToken)
	}
	return fmt.Errorf("%w: audience not accepted", ErrInvalidToken)
}
