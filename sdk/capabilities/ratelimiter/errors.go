package ratelimiter

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ErrCapacity means Memory cannot track another key without forgetting an active
// budget. It matches sdk.ErrUnavailable. Existing keys continue to be enforced.
var ErrCapacity = fmt.Errorf("ratelimiter: active key capacity reached: %w", sdk.ErrUnavailable)
