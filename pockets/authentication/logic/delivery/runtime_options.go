package delivery

import (
	"log/slog"
	"time"
)

// InProcessRuntimeOption configures NewInProcessRuntime before construction. Nil options are invalid.
type InProcessRuntimeOption func(*inProcessRuntimeConfig)

// RetryPolicyConfig sets the delivery attempt limit and retry delays.
type RetryPolicyConfig struct {
	// MaxAttempts caps process-local delivery attempts before a transient failure becomes
	// a terminal dead-letter; 0 → defaultInProcessMaxAttempts. It mirrors the command.Engine
	// attempt budget so the bounded pool and the durable jobs runtime cap identically.
	MaxAttempts int
	// Backoff maps a just-spent attempt number (1-based) to the delay before the next
	// attempt; nil selects a capped exponential (defaultInProcessBackoffBase doubling to
	// defaultInProcessBackoffCap). The wait is context-cancellable and occupies a worker
	// slot for its (bounded) duration — the pool reschedules nothing; see backoffWait.
	Backoff func(attempt int) time.Duration
}

// WithRetryPolicy replaces the complete RetryPolicyConfig group, including zero values.
func WithRetryPolicy(value RetryPolicyConfig) InProcessRuntimeOption {
	return func(c *inProcessRuntimeConfig) {
		c.MaxAttempts = value.MaxAttempts
		c.Backoff = value.Backoff
	}
}

// WithWorkerCount sets the fixed pool size; zero selects two workers.
func WithWorkerCount(value int) InProcessRuntimeOption {
	return func(c *inProcessRuntimeConfig) {
		c.Workers = value
	}
}

// WithShutdownDeadline bounds the shutdown wait; zero selects 15 seconds.
func WithShutdownDeadline(value time.Duration) InProcessRuntimeOption {
	return func(c *inProcessRuntimeConfig) {
		c.ShutdownDeadline = value
	}
}

// WithRuntimeLogger supplies the runtime logger; nil selects slog.Default().
func WithRuntimeLogger(value *slog.Logger) InProcessRuntimeOption {
	return func(c *inProcessRuntimeConfig) {
		c.Logger = value
	}
}
