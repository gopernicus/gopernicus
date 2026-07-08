// Package events is the public surface of the events feature module: the SSE
// gateway (a bus consumer that fans events out to browser streams) plus the
// host-facing socket for wiring it. The transactional-outbox domain and the
// host-driven poller (poller.go, logic/outbox) ride alongside it; this file
// carries the host-facing constructors per the feature charter's "<name>.go is
// the feature's entire host-facing surface" rule.
//
// The feature is datastore-free and view-free: it depends on its outbox port and
// sdk facilities only, never on a concrete store, an integration, or another
// feature. The gateway's connect-time identity is read from sdk/identity (a host
// stashes it via authentication.RequireUser on Config.StreamMiddleware); the
// feature imports no other feature.
//
// Package-name collision (O5): this package is events and so is sdk/events; this
// file and hosts alias the sdk one as sdkevents.
//
// Host-facing surface, all in this file:
//
//   - Repositories — the outbox port (nil Outbox → direct-emit mode: no durable
//     rail; the host wires and drives a Poller separately, see NewPoller).
//   - AuthorizeStream — the host's coarse ownership check for resource-scoped
//     streams (consumer-declared; nil → those routes are not registered).
//   - Projector — the audited opt-in to a richer SSE body than metadata-only.
//   - Config — the gateway's collaborators and tuning; Bus is REQUIRED.
//   - NewService / Service.Register — build once (the hub subscribes here), mount
//     the routes once (FS2).
package events

import (
	"context"
	"errors"
	"time"

	internalhttp "github.com/gopernicus/gopernicus/features/events/internal/inbound/http"
	"github.com/gopernicus/gopernicus/features/events/internal/logic/hub"
	"github.com/gopernicus/gopernicus/features/events/logic/outbox"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/identity"
	"github.com/gopernicus/gopernicus/sdk/web"
)

const (
	// defaultHeartbeat is the SSE comment-frame cadence when Config.Heartbeat is 0.
	defaultHeartbeat = 25 * time.Second

	// defaultMaxConnAge bounds a single stream's lifetime when Config.MaxConnAge is
	// 0. It is deliberately ON (inverting the original's unlimited default): the
	// gateway authorizes at connect time only, so a bounded age caps how long a
	// revoked session keeps a live stream. It CANNOT be disabled in v1 (P5) — a
	// host wanting effectively-unlimited sets an explicitly large value.
	defaultMaxConnAge = 15 * time.Minute
)

// ErrBusRequired is returned by NewService when Config.Bus is nil. The gateway is
// a bus consumer — it must Subscribe — so a nil bus is a misconfiguration, not a
// degraded mode; it degrades loudly at construction (the ErrHasherRequired
// precedent). Absent identity, by contrast, fails closed at request time (401),
// not at construction (A-I1 E1): a host that wires no identity-stashing
// StreamMiddleware gets uniform 401s on every stream.
var ErrBusRequired = errors.New("events: Config.Bus is required")

// AuthorizeStream is the host-supplied coarse ownership check for resource-scoped
// streams. It is consumer-declared (the feature imports no authorizer): a host
// adapts it to whatever ownership rule it runs. v1's whole stream-authorization
// model is a valid identity plus this check — no ReBAC. It receives the effective
// caller as an identity.Principal (the authorizer reads the Principal unadapted).
type AuthorizeStream func(ctx context.Context, principal identity.Principal, resourceType, resourceID string) (bool, error)

// Projector maps an event to the SSE data body — the audited opt-in that forwards
// more than the metadata-only default. Nil → metadata-only projection ({type,
// occurred_at, aggregate_type, aggregate_id, tenant_id}); raw payloads are never
// forwarded unless a Projector opts in.
type Projector func(sdkevents.Event) any

// Repositories is the set of outbound ports the feature needs. Outbox is the
// durable rail's port; a nil Outbox is direct-emit mode — the gateway still fans
// best-effort emits out over SSE, but there is no durable outbox and the host
// runs no poller. When Outbox is wired, the host constructs and drives a Poller
// (NewPoller) on an sdk/workers pool; the gateway itself never owns the poller.
type Repositories struct {
	Outbox outbox.EntryRepository
}

// Config carries the gateway's host-provided collaborators and tuning. Bus is
// REQUIRED (nil → ErrBusRequired at construction); everything else is optional
// with a safe default.
type Config struct {
	// Bus is REQUIRED — the gateway subscribes to it. Nil → ErrBusRequired.
	Bus sdkevents.Bus
	// StreamMiddleware wraps every stream route. A host passes its identity-
	// stashing middleware here (authentication.RequireUser): the handlers read the
	// stashed identity.Principal and fail closed (401) when it is absent.
	StreamMiddleware []web.Middleware
	// Authorize gates resource-scoped streams. Nil → the /events/{resource_type}/
	// {resource_id} route is NOT registered (deny-by-absence).
	Authorize AuthorizeStream
	// Projector opts into a richer SSE body; nil → metadata-only.
	Projector Projector
	// Heartbeat is the SSE comment-frame cadence; 0 → 25s.
	Heartbeat time.Duration
	// BufferSize is the per-connection channel depth; 0 → 64. A slow client whose
	// buffer fills has further events dropped (SSE is a wake-up channel).
	BufferSize int
	// MaxConnAge bounds a single stream's lifetime; 0 → 15m. It cannot be disabled
	// (P5): the bounded age is the revocation-latency posture. A host wanting
	// effectively-unlimited sets an explicitly large value (e.g. 8760h).
	MaxConnAge time.Duration
	// MaxConnsPerSubject caps concurrent streams per subject; 0 → 10.
	MaxConnsPerSubject int
}

// Service is the events feature's gateway surface. NewService builds it and
// subscribes the hub to the bus (FS2 build-once: fan-out starts at construction);
// Register only mounts the HTTP routes over the already-built hub.
type Service struct {
	repos      Repositories
	hub        *hub.Hub
	authorize  AuthorizeStream
	middleware []web.Middleware
	heartbeat  time.Duration
	maxConnAge time.Duration
}

// NewService validates Config, builds the SSE hub, and subscribes it to the bus.
// It errors on a nil Bus (ErrBusRequired) and does not mount HTTP routes (see
// Register). The hub subscribes here, so events begin fanning out to future
// connections the moment the service exists.
func NewService(repos Repositories, cfg Config) (*Service, error) {
	if cfg.Bus == nil {
		return nil, ErrBusRequired
	}

	h, err := hub.New(cfg.Bus, hub.Config{
		BufferSize:         cfg.BufferSize,
		MaxConnsPerSubject: cfg.MaxConnsPerSubject,
		Projector:          hub.Projector(cfg.Projector),
	})
	if err != nil {
		return nil, err
	}

	heartbeat := cfg.Heartbeat
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}
	maxConnAge := cfg.MaxConnAge
	if maxConnAge <= 0 {
		maxConnAge = defaultMaxConnAge
	}

	return &Service{
		repos:      repos,
		hub:        h,
		authorize:  cfg.Authorize,
		middleware: cfg.StreamMiddleware,
		heartbeat:  heartbeat,
		maxConnAge: maxConnAge,
	}, nil
}

// Register mounts the events feature's SSE routes onto the host's Mount, over the
// already-built hub (FS2: build once via NewService, mount once). The
// resource-scoped route is registered only when Config.Authorize was wired
// (deny-by-absence). It starts no goroutines — the hub subscribed at NewService,
// and streams live for the duration of their request. Migrations are the store
// adapter's concern, not this feature core's.
func (s *Service) Register(m feature.Mount) error {
	internalhttp.Mount(m.Router, internalhttp.Config{
		Hub:        s.hub,
		Authorize:  s.authorize,
		Middleware: s.middleware,
		Heartbeat:  s.heartbeat,
		MaxConnAge: s.maxConnAge,
	})
	if m.Logger != nil {
		m.Logger.Info("registered events feature",
			"resource_streams", s.authorize != nil,
			"durable_outbox", s.repos.Outbox != nil,
		)
	}
	return nil
}
