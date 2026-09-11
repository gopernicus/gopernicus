// Package streams provides authenticated, policy-filtered event streams.
// Raw bus events and connection registration remain private implementation details.
package streams

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gopernicus/gopernicus/pockets/events/internal/hub"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

var ErrBusRequired = fmt.Errorf("events: bus is required: %w", sdk.ErrInvalidInput)
var ErrTooManyConnections = hub.ErrTooManyConnections

// Visibility decides whether a principal may receive an event; errors deny it.
// It runs before projection and must honor context cancellation.
type Visibility func(context.Context, sdk.Principal, sdkevents.Event) (bool, error)

// Projector opts into a richer body than the default metadata-only projection.
// It runs only for visible events, concurrently across streams.
type Projector func(sdkevents.Event) any

// Service owns one bus subscription. Stop consumers before Close; the host owns the bus.
type Service struct {
	hub     *hub.Hub
	visible Visibility
	log     *slog.Logger
}

// New subscribes immediately and resolves buffer/projection defaults. It starts no consumer.
func New(bus sdkevents.Bus, opts ...Option) (*Service, error) {
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("events streams: nil constructor option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if bus == nil {
		return nil, ErrBusRequired
	}
	h, err := hub.New(bus, hub.Config{
		Logger:             cfg.Logger,
		BufferSize:         cfg.BufferSize,
		MaxConnsPerSubject: cfg.MaxConnsPerSubject,
		Projector:          hub.Projector(cfg.Projector),
	})
	if err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{hub: h, visible: cfg.Visible, log: log}, nil
}

// Ready checks the requirements shared by direct stream consumers and HTTP adapters.
func (s *Service) Ready() error {
	if s == nil || s.hub == nil {
		return fmt.Errorf("events: stream service is required: %w", sdk.ErrInvalidInput)
	}
	if s.visible == nil {
		return fmt.Errorf("events: visibility policy is required: %w", sdk.ErrInvalidInput)
	}
	if s.hub.Closed() {
		return sdkevents.ErrClosed
	}
	return nil
}

// Close releases the bus subscription. It is idempotent and does not close the bus.
func (s *Service) Close() error { return s.hub.Close() }
