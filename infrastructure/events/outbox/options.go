package outbox

import "time"

// Options configures outbox event behaviour.
// Parse from environment using environment.ParseEnvTags.
type Options struct {
	// MaxRetries is the default maximum number of processing attempts.
	// The worker increments the retry counter and stops after this many failures.
	MaxRetries int `env:"EVENTS_OUTBOX_MAX_RETRIES" default:"3"`

	// DefaultPriority is the default priority assigned to outbox events.
	// Higher values are processed first. Default is 0 (normal).
	DefaultPriority int `env:"EVENTS_OUTBOX_DEFAULT_PRIORITY" default:"0"`

	// RetryBaseDelay is the base delay for exponential backoff between retries.
	RetryBaseDelay time.Duration `env:"EVENTS_OUTBOX_RETRY_BASE_DELAY" default:"1s"`

	// RetryMaxDelay is the maximum delay between retries.
	RetryMaxDelay time.Duration `env:"EVENTS_OUTBOX_RETRY_MAX_DELAY" default:"5m"`
}
