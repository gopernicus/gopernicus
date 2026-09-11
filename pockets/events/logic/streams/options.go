package streams

import "log/slog"

// Limits bounds buffering and concurrent streams. Nonpositive values select
// 64 buffered events and 10 concurrent connections per subject.
type Limits struct {
	BufferSize         int `env:"EVENTS_BUFFER_SIZE"`
	MaxConnsPerSubject int `env:"EVENTS_MAX_CONNS_PER_SUBJECT"`
}

// Option configures the stream service before its bus subscription is acquired.
type Option func(*config)

type config struct {
	Limits
	Visible   Visibility
	Projector Projector
	Logger    *slog.Logger
}

// WithLimits replaces all connection and buffering limits.
func WithLimits(limits Limits) Option { return func(c *config) { c.Limits = limits } }

// WithVisibility replaces the event visibility policy. Nil leaves the service
// unready: Open and HTTP construction reject missing visibility.
func WithVisibility(visible Visibility) Option { return func(c *config) { c.Visible = visible } }

// WithProjector replaces the body projector; nil selects metadata-only events.
func WithProjector(projector Projector) Option { return func(c *config) { c.Projector = projector } }

// WithLogger replaces the diagnostic logger. Nil selects slog.Default.
func WithLogger(logger *slog.Logger) Option { return func(c *config) { c.Logger = logger } }
