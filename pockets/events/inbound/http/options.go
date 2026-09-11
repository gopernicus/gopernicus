package eventshttp

import (
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Policy controls connection liveness and lifetime. Nonpositive durations select
// a 25-second heartbeat and a 15-minute maximum age; maximum age cannot be disabled.
type Policy struct {
	Heartbeat  time.Duration `env:"EVENTS_HEARTBEAT"`
	MaxConnAge time.Duration `env:"EVENTS_MAX_CONN_AGE"`
}

// Option configures the HTTP adapter before route construction.
type Option func(*config)

type config struct {
	Policy
	Authorize  AuthorizeStream
	Middleware []web.Middleware
	Logger     *slog.Logger
}

// WithPolicy replaces both connection timing settings.
func WithPolicy(policy Policy) Option { return func(c *config) { c.Policy = policy } }

// WithAuthorization replaces the resource gate. Nil omits resource routes.
func WithAuthorization(authorize AuthorizeStream) Option {
	return func(c *config) { c.Authorize = authorize }
}

// WithMiddleware replaces middleware for every stream route; first is outermost.
// The slice is copied. A nil middleware element is rejected by New.
func WithMiddleware(middleware ...web.Middleware) Option {
	snapshot := append([]web.Middleware(nil), middleware...)
	return func(c *config) { c.Middleware = append([]web.Middleware(nil), snapshot...) }
}

// WithLogger replaces the diagnostic logger. Nil selects slog.Default.
func WithLogger(logger *slog.Logger) Option { return func(c *config) { c.Logger = logger } }
