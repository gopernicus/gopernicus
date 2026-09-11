package ratelimiter_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter/ratelimitertest"
)

func TestMemoryConformance(t *testing.T) {
	ratelimitertest.Run(t, func(t *testing.T) ratelimiter.Limiter { return ratelimiter.NewMemory() })
}
