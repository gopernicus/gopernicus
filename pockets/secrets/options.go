package secrets

import (
	"time"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
)

// Option configures construction.
type Option func(*config)

type config struct {
	wrapper vault.Wrapper
	clock   func() time.Time
}

// WithWrapper sets how owner data keys are protected. It is required: there is
// no safe default without key material. vault.NewRingWrapper over a
// cryptids.AESGCMRing is the bundled choice.
func WithWrapper(w vault.Wrapper) Option { return func(c *config) { c.wrapper = w } }

// WithClock replaces the audit clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option { return func(c *config) { c.clock = clock } }
