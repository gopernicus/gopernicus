// Package events assembles a policy-filtered event stream service and optional HTTP adapter.
// Durable handoff is available independently through logic/outbox.Poller.
package events

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"

	"github.com/gopernicus/gopernicus/pockets"
	eventshttp "github.com/gopernicus/gopernicus/pockets/events/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// Components owns the assembled stream service and HTTP configuration.
// Stop HTTP consumers before Close; the host retains bus and datastore ownership.
type Components struct {
	Streams         *streams.Service
	outbox          outbox.EntryRepository
	httpOptions     []eventshttp.Option
	resourceStreams bool
}

// New subscribes the stream service immediately. HTTP remains optional; missing
// visibility is rejected by HTTP construction, Register, and direct stream Open.
// The host owns bus. WithOutbox records optional durable storage but starts no poller.
func New(bus sdkevents.Bus, opts ...Option) (*Components, error) {
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("events: nil constructor option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	service, err := streams.New(bus, streams.WithVisibility(cfg.visible),
		streams.WithProjector(cfg.projector), streams.WithLogger(cfg.logger), streams.WithLimits(cfg.limits))
	if err != nil {
		return nil, err
	}
	return &Components{Streams: service, outbox: cfg.outbox, resourceStreams: cfg.authorize != nil,
		httpOptions: []eventshttp.Option{eventshttp.WithPolicy(cfg.httpPolicy),
			eventshttp.WithAuthorization(cfg.authorize), eventshttp.WithMiddleware(cfg.middleware...),
			eventshttp.WithLogger(cfg.logger)}}, nil
}

// HTTP constructs the optional validated adapter for bundled or custom routes.
func (c *Components) HTTP() (*eventshttp.Adapter, error) {
	return eventshttp.New(c.Streams, c.httpOptions...)
}

// Register mounts the built components' HTTP routes. No worker is started.
func (c *Components) Register(m pockets.Mount) error {
	adapter, err := c.HTTP()
	if err != nil {
		return err
	}
	if err = adapter.Register(m.Router); err != nil {
		return err
	}
	if m.Logger != nil {
		m.Logger.Info("registered events pocket", "resource_streams", c.resourceStreams, "durable_outbox", c.outbox != nil)
	}
	return nil
}

func (c *Components) Close() error { return c.Streams.Close() }
