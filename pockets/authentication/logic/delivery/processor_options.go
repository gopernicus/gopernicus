package delivery

import (
	"time"

	deliverycmd "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery/command"
)

// ProcessorOption configures NewJobsProcessor before construction. Nil options are invalid.
type ProcessorOption func(*jobsProcessorConfig)

// WithProcessorInitializer replaces Initializer for this constructor.
func WithProcessorInitializer(value Initializer) ProcessorOption {
	return func(c *jobsProcessorConfig) {
		c.Initializer = value
	}
}

// WithProcessorClock supplies the retry clock; nil selects time.Now.
func WithProcessorClock(value func() time.Time) ProcessorOption {
	return func(c *jobsProcessorConfig) {
		c.Now = value
	}
}

// WithProcessorPolicy replaces the command retry and provider-timeout policy.
func WithProcessorPolicy(value deliverycmd.Config) ProcessorOption {
	return func(c *jobsProcessorConfig) {
		c.Config = value
	}
}

// WithProcessorObserver replaces Observer for this constructor.
func WithProcessorObserver(value deliverycmd.Observer) ProcessorOption {
	return func(c *jobsProcessorConfig) {
		c.Observer = value
	}
}
