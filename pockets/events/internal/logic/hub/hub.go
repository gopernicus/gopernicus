// Package hub is the events feature's SSE fan-out core: one per process, it
// subscribes to the bus once at construction and fans every event into
// per-connection buffered channels the HTTP layer drains onto Server-Sent-Event
// streams (design §6). It is a logic package — it holds no transport type, only
// the neutral Frame the inbound HTTP adapter maps to a web.SSEEvent.
//
// Delivery is deliberately weak, matching the bus: a slow connection's buffer
// fills and further events are DROPPED (with a sampled warning), never blocking
// the emitter — SSE is a wake-up channel, not a durable feed, and clients
// re-fetch authoritative state through the normal API. The projection is
// metadata-only by default ({type, occurred_at, aggregate_type, aggregate_id,
// tenant_id}); raw payloads are never forwarded unless a Projector opts in.
package hub

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

const (
	// topicAll is the wildcard topic the hub subscribes to: every event on the
	// bus reaches the gateway, which filters per connection.
	topicAll = "*"

	// defaultBufferSize is the per-connection channel depth when Config.BufferSize
	// is not set — the number of events a slow client may lag before drops start.
	defaultBufferSize = 64

	// defaultMaxConnsPerSubject caps concurrent streams for one subject when
	// Config.MaxConnsPerSubject is not set.
	defaultMaxConnsPerSubject = 10

	// dropSampleInterval samples the slow-client drop warning: the hub logs the
	// first drop and then every dropSampleInterval-th, so a persistently slow
	// client warns without flooding the log.
	dropSampleInterval = 100
)

// ErrTooManyConnections is returned by Connect when a subject already holds the
// configured maximum concurrent streams (MaxConnsPerSubject). The HTTP layer maps
// it to 429.
var ErrTooManyConnections = errors.New("events: too many concurrent streams for subject")

// Projector maps an event to the SSE data body — the audited opt-in that lets a
// host forward more than the metadata-only default. A nil Projector keeps the
// metadata-only projection.
type Projector func(sdkevents.Event) any

// Config configures a Hub. All fields are optional; zero values take the
// documented defaults.
type Config struct {
	// Logger receives the single-instance warning and the sampled drop warning.
	// Nil → slog.Default().
	Logger *slog.Logger
	// BufferSize is the per-connection channel depth; 0 → 64.
	BufferSize int
	// MaxConnsPerSubject caps concurrent streams per subject; 0 → 10.
	MaxConnsPerSubject int
	// Projector opts into a richer SSE body; nil → metadata-only.
	Projector Projector
}

// Frame is the neutral, transport-free event the hub fans to a connection. The
// inbound HTTP adapter maps it to a web.SSEEvent (ID → id:, Type → event:, Data →
// data:). Data is already the projected body (metadata-only, or the Projector's
// output).
type Frame struct {
	ID   string
	Type string
	Data any
}

// ConnectOptions describes a connection's delivery filter.
type ConnectOptions struct {
	// Types is an exact-match allow-list of event types; empty → every type
	// (topic matching is exact + "*" only — O6, no prefix patterns).
	Types []string
	// ResourceType and ResourceID scope a connection to one aggregate. When
	// ResourceType is non-empty the connection delivers ONLY events whose Metadata
	// matches both (deny-by-default: events carrying no Metadata are suppressed —
	// post-gate P4). An empty ResourceType is a subject stream: no resource filter.
	ResourceType string
	ResourceID   string
}

// metaView is the metadata-only projection body (design §6): the default SSE
// data payload. Payloads stay opaque unless a Projector opts in.
type metaView struct {
	Type          string    `json:"type"`
	OccurredAt    time.Time `json:"occurred_at"`
	AggregateType *string   `json:"aggregate_type,omitempty"`
	AggregateID   *string   `json:"aggregate_id,omitempty"`
	TenantID      *string   `json:"tenant_id,omitempty"`
}

// connection is one live SSE stream's fan-in channel and filter.
type connection struct {
	subject      string
	types        map[string]struct{} // nil/empty → all types
	resourceType string              // "" → subject stream (no resource filter)
	resourceID   string
	ch           chan Frame
}

// accepts reports whether event e should be delivered to this connection: the
// types allow-list (when set) and, for a resource-scoped connection, the P4
// metadata filter (matching aggregate, no-Metadata suppressed).
func (c *connection) accepts(e sdkevents.Event) bool {
	if len(c.types) > 0 {
		if _, ok := c.types[e.Type()]; !ok {
			return false
		}
	}
	if c.resourceType != "" {
		md, ok := e.(sdkevents.Metadata)
		if !ok {
			return false
		}
		at, aid := md.AggregateType(), md.AggregateID()
		if at == nil || aid == nil {
			return false
		}
		if *at != c.resourceType || *aid != c.resourceID {
			return false
		}
	}
	return true
}

// Hub is the process-wide SSE fan-out. Construct it with New (which subscribes to
// the bus); it fans events to connections registered via Connect.
type Hub struct {
	log       *slog.Logger
	bufSize   int
	maxPerSub int
	projector Projector

	sub sdkevents.Subscription

	mu    sync.Mutex
	conns map[string][]*connection

	dropped atomic.Uint64
}

// New builds a Hub and subscribes it to the bus immediately (the FS2 build-once
// posture: fan-out begins at construction, not at route mounting). It uses
// SubscribeBroadcast when the bus satisfies Broadcaster (multi-instance fan-out),
// else plain Subscribe with a logged single-instance warning — the v1 memory-bus
// deployment shape, which is single-process anyway.
func New(bus sdkevents.Bus, cfg Config) (*Hub, error) {
	h := &Hub{
		log:       cfg.Logger,
		bufSize:   cfg.BufferSize,
		maxPerSub: cfg.MaxConnsPerSubject,
		projector: cfg.Projector,
		conns:     make(map[string][]*connection),
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if h.bufSize <= 0 {
		h.bufSize = defaultBufferSize
	}
	if h.maxPerSub <= 0 {
		h.maxPerSub = defaultMaxConnsPerSubject
	}

	var (
		sub sdkevents.Subscription
		err error
	)
	if b, ok := bus.(sdkevents.Broadcaster); ok {
		sub, err = b.SubscribeBroadcast(topicAll, h.handle)
	} else {
		h.log.Warn("events gateway: bus does not satisfy Broadcaster; SSE fan-out is single-instance only")
		sub, err = bus.Subscribe(topicAll, h.handle)
	}
	if err != nil {
		return nil, err
	}
	h.sub = sub
	return h, nil
}

// Close unsubscribes the hub from the bus. It is safe to call more than once.
func (h *Hub) Close() error {
	if h.sub == nil {
		return nil
	}
	return h.sub.Unsubscribe()
}

// Connect registers a connection for subject with the given filter and returns
// its Frame channel plus an unregister func the caller MUST defer. It enforces
// the per-subject cap (ErrTooManyConnections when exceeded). The channel is never
// closed by the hub: the caller stops reading on its own context and unregisters,
// so a late fan-out send can only land in the buffered channel (or be dropped),
// never on a closed channel.
func (h *Hub) Connect(subject string, opts ConnectOptions) (<-chan Frame, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.conns[subject]) >= h.maxPerSub {
		return nil, nil, ErrTooManyConnections
	}

	c := &connection{
		subject:      subject,
		resourceType: opts.ResourceType,
		resourceID:   opts.ResourceID,
		ch:           make(chan Frame, h.bufSize),
	}
	if len(opts.Types) > 0 {
		c.types = make(map[string]struct{}, len(opts.Types))
		for _, t := range opts.Types {
			if t != "" {
				c.types[t] = struct{}{}
			}
		}
	}
	h.conns[subject] = append(h.conns[subject], c)

	return c.ch, func() { h.remove(subject, c) }, nil
}

// remove drops a connection from its subject's list.
func (h *Hub) remove(subject string, target *connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.conns[subject]
	for i, c := range list {
		if c == target {
			h.conns[subject] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(h.conns[subject]) == 0 {
		delete(h.conns, subject)
	}
}

// handle is the bus subscription: it projects the event once, then non-blocking
// sends the shared Frame to every accepting connection, dropping on a full buffer.
func (h *Hub) handle(_ context.Context, e sdkevents.Event) error {
	frame := h.buildFrame(e)

	h.mu.Lock()
	var targets []*connection
	for _, list := range h.conns {
		for _, c := range list {
			if c.accepts(e) {
				targets = append(targets, c)
			}
		}
	}
	h.mu.Unlock()

	for _, c := range targets {
		select {
		case c.ch <- frame:
		default:
			h.recordDrop(c.subject)
		}
	}
	return nil
}

// buildFrame projects an event to a Frame. The SSE id: is the durable rail's
// EventID when the event exposes one (the poller's rehydrated events — gate edit
// 1), else the CorrelationID (best-effort events carry no per-event de-dupe
// guarantee: that path is a wake-up signal). The body is metadata-only unless a
// Projector opts in.
func (h *Hub) buildFrame(e sdkevents.Event) Frame {
	id := e.CorrelationID()
	if withID, ok := e.(interface{ EventID() string }); ok {
		if eid := withID.EventID(); eid != "" {
			id = eid
		}
	}

	var data any
	if h.projector != nil {
		data = h.projector(e)
	} else {
		data = projectMetadata(e)
	}

	return Frame{ID: id, Type: e.Type(), Data: data}
}

// projectMetadata builds the metadata-only body from an event.
func projectMetadata(e sdkevents.Event) metaView {
	v := metaView{Type: e.Type(), OccurredAt: e.OccurredAt()}
	if md, ok := e.(sdkevents.Metadata); ok {
		v.AggregateType = md.AggregateType()
		v.AggregateID = md.AggregateID()
		v.TenantID = md.TenantID()
	}
	return v
}

// recordDrop counts a slow-client drop and logs it on a sampled cadence.
func (h *Hub) recordDrop(subject string) {
	n := h.dropped.Add(1)
	if n == 1 || n%dropSampleInterval == 0 {
		h.log.Warn("events gateway: dropped SSE event for slow client",
			"subject", subject,
			"total_dropped", n,
		)
	}
}
