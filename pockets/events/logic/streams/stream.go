package streams

import (
	"context"
	"fmt"
	"sync"

	"github.com/gopernicus/gopernicus/pockets/events/internal/hub"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

// Filter restricts event types and optionally one complete aggregate identity.
type Filter struct {
	Types        []string
	ResourceType string
	ResourceID   string
}

// Frame is a projected, visible event suitable for delivery to its principal.
type Frame struct {
	ID   string
	Type string
	Data any
}

// Stream delivers only projected events that pass its service's visibility policy.
// Use one Next reader. Close releases the connection and wakes a blocked Next.
type Stream struct {
	service    *Service
	principal  sdk.Principal
	events     <-chan sdkevents.Event
	unregister func()
	done       chan struct{}
	once       sync.Once
}

// Open registers an authenticated principal's connection. Resource identity must
// contain both type and ID. The service owns filtering and visibility on every Next.
func (s *Service) Open(principal sdk.Principal, filter Filter) (*Stream, error) {
	if err := s.Ready(); err != nil {
		return nil, err
	}
	if principal.Type == "" || principal.ID == "" {
		return nil, fmt.Errorf("events: principal is required: %w", sdk.ErrInvalidInput)
	}
	if (filter.ResourceType == "") != (filter.ResourceID == "") {
		return nil, fmt.Errorf("events: complete resource identity is required: %w", sdk.ErrInvalidInput)
	}
	ch, unregister, err := s.hub.Connect(principal, hub.ConnectOptions{Types: filter.Types, ResourceType: filter.ResourceType, ResourceID: filter.ResourceID})
	if err != nil {
		return nil, err
	}
	return &Stream{service: s, principal: principal, events: ch, unregister: unregister, done: make(chan struct{})}, nil
}

// Next blocks until a visible event arrives or ctx is canceled. Policy errors and
// callback panics deny the event. No raw event or unfiltered projection escapes.
func (s *Stream) Next(ctx context.Context) (Frame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Frame{}, err
		}
		select {
		case <-s.done:
			return Frame{}, sdkevents.ErrClosed
		default:
		}
		select {
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		case <-s.done:
			return Frame{}, sdkevents.ErrClosed
		case event := <-s.events:
			frame, allowed := s.service.visibleFrame(ctx, s.principal, event)
			if allowed && ctx.Err() == nil {
				select {
				case <-s.done:
					return Frame{}, sdkevents.ErrClosed
				default:
					return frame, nil
				}
			}
		}
	}
}

func (s *Stream) Close() error { s.once.Do(func() { close(s.done); s.unregister() }); return nil }

// visibleFrame keeps callbacks off the shared bus and fails closed on errors.
func (s *Service) visibleFrame(ctx context.Context, principal sdk.Principal, event sdkevents.Event) (frame Frame, allowed bool) {
	defer func() {
		if recover() != nil {
			allowed = false
			s.log.ErrorContext(ctx, "events gateway: event policy or projection panicked")
		}
	}()
	if ctx.Err() != nil {
		return frame, false
	}
	ok, err := s.visible(ctx, principal, event)
	if err != nil {
		if ctx.Err() == nil {
			s.log.ErrorContext(ctx, "events gateway: event visibility check failed", "error", err)
		}
		return frame, false
	}
	if !ok || ctx.Err() != nil {
		return frame, false
	}
	f := s.hub.Frame(event)
	return Frame{ID: f.ID, Type: f.Type, Data: f.Data}, true
}
