package eventshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// subjectStream serves the caller's stream: every event, filtered by the optional
// ?types allow-list. It fails closed with 401 when no identity was stashed.
func (g *Adapter) subjectStream(w http.ResponseWriter, r *http.Request) {
	principal, ok := sdk.PrincipalFromContext(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	opts := streams.Filter{Types: parseTypes(r.URL.Query().Get("types"))}
	g.serve(w, r, principal, opts)
}

// resourceStream serves a stream scoped to one aggregate, gated by the host's
// coarse Authorize check. It fails closed with 401 when no identity was stashed,
// 403 when the host denies, and 500 when the check itself errors.
func (g *Adapter) resourceStream(w http.ResponseWriter, r *http.Request) {
	principal, ok := sdk.PrincipalFromContext(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return
	}
	resourceType := r.PathValue("resource_type")
	resourceID := r.PathValue("resource_id")
	if resourceType == "" || resourceID == "" {
		web.RespondJSONError(w, web.ErrBadRequest("resource_type and resource_id are required"))
		return
	}

	allowed, err := g.authorize(r.Context(), principal, resourceType, resourceID)
	if err != nil {
		g.log.ErrorContext(r.Context(), "events gateway: stream authorization check failed", "error", err)
		web.RespondJSONError(w, web.ErrInternal("authorization check failed"))
		return
	}
	if !allowed {
		web.RespondJSONError(w, web.ErrForbidden("not permitted"))
		return
	}

	opts := streams.Filter{
		Types:        parseTypes(r.URL.Query().Get("types")),
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}
	g.serve(w, r, principal, opts)
}

// serve registers the connection with the hub and streams its frames as SSE until
// the client disconnects or MaxConnAge elapses. A per-subject cap breach is 429.
func (g *Adapter) serve(w http.ResponseWriter, r *http.Request, subject sdk.Principal, opts streams.Filter) {
	stream, err := g.streams.Open(subject, opts)
	if err != nil {
		if errors.Is(err, streams.ErrTooManyConnections) {
			web.RespondJSONError(w, web.ErrTooManyRequests("too many concurrent streams"))
		} else {
			web.RespondJSONError(w, web.ErrFromDomain(err))
		}
		return
	}
	defer stream.Close()

	// MaxConnAge bounds the stream (P5: never disabled). It rides the request
	// context, so a client disconnect and the age cap both stop the stream.
	ctx, cancel := context.WithTimeout(r.Context(), g.maxConnAge)
	defer cancel()

	sse := make(chan web.SSEEvent)
	go func() {
		defer close(sse)
		for {
			f, err := stream.Next(ctx)
			if err != nil {
				return
			}
			select {
			case sse <- web.SSEEvent{ID: f.ID, Event: f.Type, Data: f.Data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	sseStream := web.NewSSEStream(sse, web.WithHeartbeat(g.heartbeat))
	sseStream.ServeHTTP(w, r.WithContext(ctx))
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
