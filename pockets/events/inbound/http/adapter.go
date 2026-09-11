// Package eventshttp provides the events pocket's optional SSE HTTP adapter.
package eventshttp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const defaultHeartbeat = 25 * time.Second
const defaultMaxConnAge = 15 * time.Minute

// AuthorizeStream checks current access to a complete resource identity.
type AuthorizeStream func(context.Context, sdk.Principal, string, string) (bool, error)

// Adapter owns resolved transport policy. Every returned handler applies the
// same middleware, identity, lifetime, and event visibility requirements.
type Adapter struct {
	streams    *streams.Service
	log        *slog.Logger
	authorize  AuthorizeStream
	middleware []web.Middleware
	heartbeat  time.Duration
	maxConnAge time.Duration
}

// New validates the stream service and resolves HTTP defaults without mounting.
func New(service *streams.Service, opts ...Option) (*Adapter, error) {
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("events HTTP: nil constructor option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if err := service.Ready(); err != nil {
		return nil, err
	}
	for _, middleware := range cfg.Middleware {
		if middleware == nil {
			return nil, fmt.Errorf("events: nil stream middleware: %w", sdk.ErrInvalidInput)
		}
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = defaultHeartbeat
	}
	if cfg.MaxConnAge <= 0 {
		cfg.MaxConnAge = defaultMaxConnAge
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Adapter{streams: service, log: cfg.Logger, authorize: cfg.Authorize, middleware: append([]web.Middleware(nil), cfg.Middleware...), heartbeat: cfg.Heartbeat, maxConnAge: cfg.MaxConnAge}, nil
}

// SubjectStream returns the authenticated subject feed with configured middleware.
func (a *Adapter) SubjectStream() http.Handler { return a.wrap(a.subjectStream) }

// ResourceStream requires the configured resource gate before a host can mount it.
// Custom route patterns must populate resource_type and resource_id PathValues.
func (a *Adapter) ResourceStream() (http.Handler, error) {
	if a.authorize == nil {
		return nil, fmt.Errorf("events: resource authorization is required: %w", sdk.ErrInvalidInput)
	}
	return a.wrap(a.resourceStream), nil
}

func (a *Adapter) wrap(handler http.HandlerFunc) http.Handler {
	var h http.Handler = handler
	for i := len(a.middleware) - 1; i >= 0; i-- {
		h = a.middleware[i](h)
	}
	return h
}

// Register mounts the bundled routes; resource streams are absent without a gate.
func (a *Adapter) Register(r pockets.RouteRegistrar) error {
	if r == nil {
		return fmt.Errorf("events: router is required: %w", sdk.ErrInvalidInput)
	}
	if err := a.streams.Ready(); err != nil {
		return err
	}
	r.Handle("GET", "/events", a.SubjectStream().ServeHTTP)
	if a.authorize != nil {
		handler, err := a.ResourceStream()
		if err != nil {
			return err
		}
		r.Handle("GET", "/events/{resource_type}/{resource_id}", handler.ServeHTTP)
	}
	return nil
}
