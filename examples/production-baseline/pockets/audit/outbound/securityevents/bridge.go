// Package securityevents bridges the authorization pocket's decision observer
// to the authentication pocket's security-event rail: a denied decision becomes
// an authorization_denied row and a refused role write a role_write_refused
// row. Neither framework pocket may import the other, so the bridge is this
// host pocket's outbound, and cmd hands it the authentication pocket's ONE
// recorder (Components.SecurityEvents), so a row the bridge loses counts in
// AuditWriteFailures.
//
// It records denials and refusals only: an allowed decision is not audit
// material. Two bounds keep a flood of 403s from draining the database pool or
// writing a row per request: at most WithMaxInFlight writes run at once and an
// event arriving while they all do is dropped and counted (Dropped), and one
// row is written per (event type, principal, permission, resource type,
// resource id, client IP) per WithDedupe window.
package securityevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync/atomic"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

const (
	defaultMaxInFlight  = 8
	defaultDedupeWindow = 60 * time.Second
)

// dedupeMark is the value stored under a dedupe key; only its presence counts.
var dedupeMark = []byte{1}

// Recorder is the slice of the authentication pocket's securityevent.Recorder
// the bridge records through. Pass Components.SecurityEvents; never build a
// second recorder.
type Recorder interface {
	Record(context.Context, securityevent.Input)
}

// Option configures NewBridge and New. A nil option is ignored.
type Option func(*config)

type config struct {
	maxInFlight int
	store       cacher.Storer
	window      time.Duration
}

// WithMaxInFlight bounds the writes running at once; an event arriving while n
// are in flight is dropped and counted. Nonpositive selects the default, 8.
func WithMaxInFlight(n int) Option {
	return func(c *config) { c.maxInFlight = n }
}

// WithDedupe sets where the dedupe window is kept and how long it lasts: one
// row per (event type, principal, permission, resource type, resource id,
// client IP) per window, so a second address records its own row. A nil store selects an in-memory cache and a nonpositive window the
// default, 60 s. The memory default dedupes per instance; a store the fleet
// shares dedupes across it.
func WithDedupe(store cacher.Storer, window time.Duration) Option {
	return func(c *config) { c.store, c.window = store, window }
}

// Bridge records denials and refused role writes into the security-event rail.
// Its Observer is a decisions.DecisionObserver; Dropped reports what the
// in-flight bound shed.
type Bridge struct {
	rec      Recorder
	inFlight chan struct{}
	store    cacher.Storer
	window   time.Duration
	dropped  atomic.Uint64
}

// New returns the decision observer of a Bridge over rec. Use NewBridge when
// the host reports Dropped.
func New(rec Recorder, opts ...Option) decisions.DecisionObserver {
	return NewBridge(rec, opts...).Observer()
}

// NewBridge returns a Bridge recording through rec.
func NewBridge(rec Recorder, opts ...Option) *Bridge {
	var cfg config
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxInFlight <= 0 {
		cfg.maxInFlight = defaultMaxInFlight
	}
	if cfg.store == nil {
		cfg.store = cacher.NewMemory()
	}
	if cfg.window <= 0 {
		cfg.window = defaultDedupeWindow
	}
	return &Bridge{rec: rec, inFlight: make(chan struct{}, cfg.maxInFlight), store: cfg.store, window: cfg.window}
}

// Observer returns the observer to pass to authorization.WithDecisionObserver
// and, where the host rebuilds the HTTP adapter, authorizationhttp.WithDecisionObserver.
func (b *Bridge) Observer() decisions.DecisionObserver {
	return func(ctx context.Context, ev decisions.DecisionEvent) {
		switch ev.Outcome {
		case decisions.OutcomeDenied:
			b.record(ctx, ev, securityevent.Input{UserID: ev.Principal.ID, Type: decisions.EventAuthorizationDenied,
				Status: securityevent.StatusFailure, Details: map[string]any{"operation": ev.Operation, "permission": ev.Permission,
					"resource_type": ev.ResourceType, "resource_id": ev.ResourceID}})
		case decisions.OutcomeRefused:
			b.record(ctx, ev, securityevent.Input{Actor: securityevent.Principal{Type: ev.Principal.Type, ID: ev.Principal.ID},
				UserID: ev.Subject.ID, Type: decisions.EventRoleWriteRefused, Status: securityevent.StatusBlocked,
				Details: map[string]any{"role": ev.Permission, "scope_type": ev.ResourceType, "scope_id": ev.ResourceID}})
		}
	}
}

// Dropped returns how many events the in-flight bound has dropped since
// construction.
func (b *Bridge) Dropped() uint64 {
	return b.dropped.Load()
}

// record writes in unless the dedupe window already holds its key or every
// in-flight slot is taken. A dedupe store error records: the bridge fails
// toward recording.
func (b *Bridge) record(ctx context.Context, ev decisions.DecisionEvent, in securityevent.Input) {
	ip, _ := authlogic.ClientInfoFromContext(ctx)
	key := dedupeKey(in.Type, ev, ip)
	if _, seen, err := b.store.Get(ctx, key); err == nil && seen {
		return
	}
	select {
	case b.inFlight <- struct{}{}:
	default:
		b.dropped.Add(1)
		return
	}
	defer func() { <-b.inFlight }()
	_ = b.store.Set(ctx, key, dedupeMark, b.window)
	b.rec.Record(ctx, in)
}

// dedupeKey names one (event type, principal, permission, resource type,
// resource id, client IP). The fields are length-prefixed before hashing, so
// no two distinct tuples share a key.
func dedupeKey(eventType string, ev decisions.DecisionEvent, ip string) string {
	h := sha256.New()
	for _, field := range []string{ev.Principal.Type, ev.Principal.ID, ev.Permission, ev.ResourceType, ev.ResourceID, ip} {
		h.Write([]byte(strconv.Itoa(len(field))))
		h.Write([]byte{':'})
		h.Write([]byte(field))
	}
	return eventType + ":" + hex.EncodeToString(h.Sum(nil))
}
