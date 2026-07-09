// Package http is the events feature's SSE transport: the /events route surface
// over the internal hub. It reads the effective caller from sdk/identity (absent
// → 401, fails closed — A-I1 E1), enforces MaxConnAge and heartbeats via the
// sdk/web SSE primitives, and registers the resource-scoped route only when a
// host Authorize check is wired (deny-by-absence). Mounted only through
// feature.RouteRegistrar (see events.Service.Register).
package events

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/events/internal/logic/hub"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/identity"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// Config carries the already-resolved collaborators the route handlers need. The
// root events.Service builds it at Register time (defaults applied there).
type Config struct {
	// Hub is the process-wide fan-out the streams drain (subscribed at NewService).
	Hub *hub.Hub
	// Authorize is the host's coarse ownership check for resource-scoped streams.
	// Nil → the resource-scoped route is NOT registered (deny-by-absence). It
	// receives the effective caller as an identity.Principal.
	Authorize func(ctx context.Context, principal identity.Principal, resourceType, resourceID string) (bool, error)
	// Middleware wraps every stream route (the host's identity-stashing
	// middleware, e.g. authentication.RequireUser).
	Middleware []web.Middleware
	// Heartbeat is the SSE comment-frame cadence (already defaulted).
	Heartbeat time.Duration
	// MaxConnAge bounds a single stream's lifetime (already defaulted; never
	// disabled — P5).
	MaxConnAge time.Duration
}

// gateway holds the resolved dependencies the handlers close over.
type gateway struct {
	hub        *hub.Hub
	authorize  func(ctx context.Context, principal identity.Principal, resourceType, resourceID string) (bool, error)
	heartbeat  time.Duration
	maxConnAge time.Duration
}

// Mount registers the events feature's SSE routes on the registrar:
//
//   - GET /events — the authenticated subject's stream (?types=a,b exact-match
//     allow-list).
//   - GET /events/{resource_type}/{resource_id} — a resource-scoped stream,
//     registered ONLY when cfg.Authorize is non-nil (deny-by-absence).
//
// The stream middleware wraps both routes; handlers fail closed (401) when no
// middleware stashed an identity.Principal.
func Mount(r feature.RouteRegistrar, cfg Config) {
	g := &gateway{
		hub:        cfg.Hub,
		authorize:  cfg.Authorize,
		heartbeat:  cfg.Heartbeat,
		maxConnAge: cfg.MaxConnAge,
	}
	r.Handle("GET", "/events", g.subjectStream, cfg.Middleware...)
	if cfg.Authorize != nil {
		r.Handle("GET", "/events/{resource_type}/{resource_id}", g.resourceStream, cfg.Middleware...)
	}
}

// subjectStream serves the caller's stream: every event, filtered by the optional
// ?types allow-list. It fails closed with 401 when no identity was stashed.
func (g *gateway) subjectStream(w http.ResponseWriter, r *http.Request) {
	principal, ok := identity.FromContext(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	opts := hub.ConnectOptions{Types: parseTypes(r.URL.Query().Get("types"))}
	g.serve(w, r, subjectKey(principal), opts)
}

// resourceStream serves a stream scoped to one aggregate, gated by the host's
// coarse Authorize check. It fails closed with 401 when no identity was stashed,
// 403 when the host denies, and 500 when the check itself errors.
func (g *gateway) resourceStream(w http.ResponseWriter, r *http.Request) {
	principal, ok := identity.FromContext(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	resourceType := r.PathValue("resource_type")
	resourceID := r.PathValue("resource_id")

	allowed, err := g.authorize(r.Context(), principal, resourceType, resourceID)
	if err != nil {
		web.RespondJSONError(w, web.ErrInternal("authorization check failed"))
		return
	}
	if !allowed {
		web.RespondJSONError(w, web.ErrForbidden("not permitted"))
		return
	}

	opts := hub.ConnectOptions{
		Types:        parseTypes(r.URL.Query().Get("types")),
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}
	g.serve(w, r, subjectKey(principal), opts)
}

// serve registers the connection with the hub and streams its frames as SSE until
// the client disconnects or MaxConnAge elapses. A per-subject cap breach is 429.
func (g *gateway) serve(w http.ResponseWriter, r *http.Request, subject string, opts hub.ConnectOptions) {
	frames, unregister, err := g.hub.Connect(subject, opts)
	if err != nil {
		web.RespondJSONError(w, web.ErrTooManyRequests("too many concurrent streams"))
		return
	}
	defer unregister()

	// MaxConnAge bounds the stream (P5: never disabled). It rides the request
	// context, so a client disconnect and the age cap both stop the stream.
	ctx, cancel := context.WithTimeout(r.Context(), g.maxConnAge)
	defer cancel()

	sse := make(chan web.SSEEvent)
	go func() {
		defer close(sse)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-frames:
				if !ok {
					return
				}
				select {
				case sse <- web.SSEEvent{ID: f.ID, Event: f.Type, Data: f.Data}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	stream := web.NewSSEStream(sse, web.WithHeartbeat(g.heartbeat))
	stream.ServeHTTP(w, r.WithContext(ctx))
}

// subjectKey is the composite per-subject key Type + ":" + ID (A-I1 E1): it keeps
// a user and a service account with the same raw ID from colliding under the
// per-subject connection cap.
func subjectKey(p identity.Principal) string {
	return p.Type + ":" + p.ID
}

// parseTypes splits the ?types=a,b allow-list, trimming blanks. Empty → nil (no
// type filter). Matching is exact — no prefix patterns (O6).
func parseTypes(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
