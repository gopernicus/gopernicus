package events

import (
	"log/slog"

	eventshttp "github.com/gopernicus/gopernicus/pockets/events/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Option configures optional delivery and HTTP policy before subscribing to the bus.
type Option func(*config)

type config struct {
	outbox     outbox.EntryRepository
	visible    streams.Visibility
	projector  streams.Projector
	logger     *slog.Logger
	limits     streams.Limits
	httpPolicy eventshttp.Policy
	authorize  eventshttp.AuthorizeStream
	middleware []web.Middleware
}

// WithOutbox replaces the optional durable storage reference. The host constructs
// and runs an outbox.Poller separately; nil keeps direct-emit mode.
func WithOutbox(repo outbox.EntryRepository) Option { return func(c *config) { c.outbox = repo } }

// WithVisibility replaces the event visibility policy. Missing visibility is
// rejected by direct Open, HTTP construction and Register, rather than allowing events.
func WithVisibility(visible streams.Visibility) Option {
	return func(c *config) { c.visible = visible }
}

// WithProjector replaces the SSE body projector; nil selects metadata-only events.
func WithProjector(projector streams.Projector) Option {
	return func(c *config) { c.projector = projector }
}

// WithLogger replaces the stream and HTTP diagnostic logger. Nil selects slog.Default.
func WithLogger(logger *slog.Logger) Option { return func(c *config) { c.logger = logger } }

// WithStreamLimits replaces all buffering and connection limits.
func WithStreamLimits(limits streams.Limits) Option { return func(c *config) { c.limits = limits } }

// WithHTTPPolicy replaces connection heartbeat and maximum-age settings.
func WithHTTPPolicy(policy eventshttp.Policy) Option {
	return func(c *config) { c.httpPolicy = policy }
}

// WithAuthorization replaces the resource-stream gate. Nil omits resource routes.
func WithAuthorization(authorize eventshttp.AuthorizeStream) Option {
	return func(c *config) { c.authorize = authorize }
}

// WithStreamMiddleware replaces middleware for every stream route; first is outermost.
// The slice is copied. HTTP construction rejects a nil middleware element.
func WithStreamMiddleware(middleware ...web.Middleware) Option {
	snapshot := append([]web.Middleware(nil), middleware...)
	return func(c *config) { c.middleware = append([]web.Middleware(nil), snapshot...) }
}
