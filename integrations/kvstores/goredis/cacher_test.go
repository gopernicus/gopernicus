// These tests are hermetic: they exercise construction and option handling
// without any Redis connection. The live cacher.Storer contract is verified by
// conformance_test.go (cachertest.Run) under REDIS_TEST_ADDR.
package goredis

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

func TestCacherAppliesDefaultKeyPrefix(t *testing.T) {
	c := NewCacher(dummyClient())
	if c.keyPrefix != defaultCacheKeyPrefix {
		t.Errorf("keyPrefix = %q, want %q", c.keyPrefix, defaultCacheKeyPrefix)
	}
}

func TestCacherKeyPrefixOption(t *testing.T) {
	c := NewCacher(dummyClient(), WithCacheKeyPrefix("tenant:"))
	if c.keyPrefix != "tenant:" {
		t.Errorf("keyPrefix = %q, want %q", c.keyPrefix, "tenant:")
	}
}

func TestCacherCloseIsIdempotentWithoutRedis(t *testing.T) {
	c := NewCacher(dummyClient())
	if err := c.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestCacherPortSatisfaction(t *testing.T) {
	var _ cacher.Storer = (*Cacher)(nil)
}
