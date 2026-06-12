// Package ssebridge streams domain events to browsers over Server-Sent
// Events: one Hub per process subscribes to the event bus (broadcast
// fan-out when the bus supports it) and fans events into per-connection
// buffered channels; the Bridge mounts the authenticated HTTP routes.
//
//	hub := ssebridge.NewHub(bus, log)
//	sse := ssebridge.New(log, hub, authenticator, authorizer, rateLimiter)
//	sse.AddHttpRoutes(api.Group("/events"))
//
// Security model: authorization is CONNECT-TIME ONLY (middleware runs
// once per connection). Use WithMaxConnAge to force periodic reconnect +
// re-auth when revocation latency matters. Event payloads are NOT
// forwarded by default — the projection carries only
// {type, occurred_at, tenant_id, aggregate_type, aggregate_id}, enough
// for clients to re-fetch state through the (authorized) API. Opt into
// richer payloads per deployment with WithPayloadProjector.
package ssebridge

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// Projection is the default event shape streamed to clients.
type Projection struct {
	Type          string  `json:"type"`
	OccurredAt    string  `json:"occurred_at"`
	TenantID      *string `json:"tenant_id,omitempty"`
	AggregateType *string `json:"aggregate_type,omitempty"`
	AggregateID   *string `json:"aggregate_id,omitempty"`
}

// Projector maps a domain event to the payload streamed to clients.
type Projector func(events.Event) any

// connFilter scopes one connection's stream.
type connFilter struct {
	tenantID      string // require event tenant match ("" = no tenant filter)
	aggregateType string // optional aggregate scoping
	aggregateID   string
	eventTypes    map[string]bool // optional allow-list (nil = all)
}

type conn struct {
	id      uint64
	filter  connFilter
	ch      chan web.SSEEvent
	subject string
}

// Hub fans bus events into connected SSE clients.
type Hub struct {
	log  *slog.Logger
	bus  events.Bus
	opts hubOptions

	mu     sync.RWMutex
	conns  map[uint64]*conn
	nextID uint64

	dropped atomic.Int64
	sub     events.Subscription
}

type hubOptions struct {
	topics             []string
	bufferSize         int
	heartbeat          time.Duration
	maxConnAge         time.Duration
	maxConnsPerSubject int
	projector          Projector
}

// HubOption configures a Hub.
type HubOption func(*hubOptions)

// WithTopics restricts the bus subscription (default "*").
func WithTopics(topics ...string) HubOption {
	return func(o *hubOptions) { o.topics = topics }
}

// WithBufferSize sets the per-connection event buffer (default 64). When a
// slow client's buffer is full, events are DROPPED for that connection —
// SSE is a wake-up channel, not a durable feed.
func WithBufferSize(n int) HubOption {
	return func(o *hubOptions) { o.bufferSize = n }
}

// WithHeartbeat sets the SSE comment-frame interval (default 25s).
func WithHeartbeat(d time.Duration) HubOption {
	return func(o *hubOptions) { o.heartbeat = d }
}

// WithMaxConnAge forces connections closed after d, making clients
// reconnect through the auth middleware again (default 0 = unlimited).
// Recommended (~15m) when session revocation must take effect mid-stream.
func WithMaxConnAge(d time.Duration) HubOption {
	return func(o *hubOptions) { o.maxConnAge = d }
}

// WithMaxConnsPerSubject caps concurrent streams per authenticated subject
// (default 10; 0 = unlimited).
func WithMaxConnsPerSubject(n int) HubOption {
	return func(o *hubOptions) { o.maxConnsPerSubject = n }
}

// WithPayloadProjector replaces the default metadata-only projection.
// Forwarding raw payloads can leak sensitive fields (auth events carry
// verification codes) — audit what your events contain before widening.
func WithPayloadProjector(p Projector) HubOption {
	return func(o *hubOptions) { o.projector = p }
}

// NewHub subscribes to the bus and starts fanning events out. When the bus
// implements events.Broadcaster the subscription is broadcast (every
// instance sees every event — required for SSE across multiple
// processes); otherwise it falls back to plain Subscribe with a logged
// warning (correct on single-instance deployments only).
func NewHub(bus events.Bus, log *slog.Logger, opts ...HubOption) (*Hub, error) {
	o := hubOptions{
		topics:             []string{"*"},
		bufferSize:         64,
		heartbeat:          25 * time.Second,
		maxConnsPerSubject: 10,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.projector == nil {
		o.projector = defaultProjection
	}

	h := &Hub{log: log, bus: bus, opts: o, conns: map[uint64]*conn{}}

	subscribe := bus.Subscribe
	if b, ok := bus.(events.Broadcaster); ok {
		subscribe = b.SubscribeBroadcast
	} else {
		log.Warn("sse: bus has no broadcast capability — events emitted on other instances will not reach this hub (fine for single-instance deployments)")
	}
	for _, topic := range o.topics {
		sub, err := subscribe(topic, h.handle)
		if err != nil {
			return nil, err
		}
		h.sub = sub // last one wins for Close; all unsubscribed via bus.Close
	}
	return h, nil
}

func defaultProjection(e events.Event) any {
	p := Projection{Type: e.Type(), OccurredAt: e.OccurredAt().UTC().Format(time.RFC3339Nano)}
	if m, ok := e.(events.EventWithMetadata); ok {
		p.TenantID = m.TenantID()
		p.AggregateType = m.AggregateType()
		p.AggregateID = m.AggregateID()
	}
	return p
}

// handle is the bus handler: project once, fan out to matching connections.
func (h *Hub) handle(_ context.Context, event events.Event) error {
	payload := h.opts.projector(event)
	sse := web.SSEEvent{Event: event.Type(), Data: payload, ID: event.CorrelationID()}

	var tenant, aggType, aggID string
	if m, ok := event.(events.EventWithMetadata); ok {
		if v := m.TenantID(); v != nil {
			tenant = *v
		}
		if v := m.AggregateType(); v != nil {
			aggType = *v
		}
		if v := m.AggregateID(); v != nil {
			aggID = *v
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if !c.filter.matches(event.Type(), tenant, aggType, aggID) {
			continue
		}
		select {
		case c.ch <- sse:
		default:
			// Slow client: drop rather than block the hub. One counter
			// tick; the client re-syncs via the API on its next fetch.
			if h.dropped.Add(1)%100 == 1 {
				h.log.Warn("sse: client lagging, events dropped", "subject", c.subject, "dropped_total", h.dropped.Load())
			}
		}
	}
	return nil
}

func (f connFilter) matches(eventType, tenant, aggType, aggID string) bool {
	if f.tenantID != "" && f.tenantID != tenant {
		return false
	}
	if f.aggregateType != "" && (f.aggregateType != aggType || f.aggregateID != aggID) {
		return false
	}
	if f.eventTypes != nil && !f.eventTypes[eventType] {
		return false
	}
	return true
}

// connect registers a connection; returns nil when the subject is over its
// concurrent-connection cap.
func (h *Hub) connect(subject string, filter connFilter) *conn {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.opts.maxConnsPerSubject > 0 {
		count := 0
		for _, c := range h.conns {
			if c.subject == subject {
				count++
			}
		}
		if count >= h.opts.maxConnsPerSubject {
			return nil
		}
	}

	h.nextID++
	c := &conn{id: h.nextID, filter: filter, ch: make(chan web.SSEEvent, h.opts.bufferSize), subject: subject}
	h.conns[c.id] = c
	return c
}

func (h *Hub) disconnect(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[c.id]; ok {
		delete(h.conns, c.id)
		close(c.ch)
	}
}

// ConnCount reports the live connection count (for metrics/tests).
func (h *Hub) ConnCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// Dropped reports the total events dropped to slow clients.
func (h *Hub) Dropped() int64 { return h.dropped.Load() }
