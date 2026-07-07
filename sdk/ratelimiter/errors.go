package ratelimiter

import "errors"

// Common errors.
var (
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrLimiterClosed     = errors.New("rate limiter closed")
)
