package ssebridge

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// Bridge mounts the SSE endpoints over a Hub.
type Bridge struct {
	log           *slog.Logger
	hub           *Hub
	authenticator *authentication.Authenticator
	authorizer    *authorization.Authorizer
	rateLimiter   *ratelimiter.RateLimiter
	jsonErrors    httpmid.ErrorRenderer
}

// New wires the SSE bridge; mount with AddHttpRoutes.
func New(log *slog.Logger, hub *Hub, authenticator *authentication.Authenticator, authorizer *authorization.Authorizer, rateLimiter *ratelimiter.RateLimiter) *Bridge {
	return &Bridge{
		log:           log,
		hub:           hub,
		authenticator: authenticator,
		authorizer:    authorizer,
		rateLimiter:   rateLimiter,
		jsonErrors:    httpmid.JSONErrors{},
	}
}

// AddHttpRoutes registers the SSE endpoints on the given group:
//
//	GET {group}/            — tenant stream: every event whose tenant_id
//	                          matches the authenticated subject's tenant
//	                          (subject context, never the query string).
//	GET {group}/{resource_type}/{resource_id}
//	                        — resource stream, authorized connect-time via
//	                          AuthorizeDynamicParam("read", ...).
//
// Both accept ?types=a,b to allow-list event types.
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
	group.GET("", b.httpTenantStream,
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
		httpmid.RateLimit(b.rateLimiter, b.log),
	)
	group.GET("/{resource_type}/{resource_id}", b.httpResourceStream,
		httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
		httpmid.RateLimit(b.rateLimiter, b.log),
		httpmid.AuthorizeDynamicParam(b.authorizer, b.log, b.jsonErrors, "read", "resource_type", "resource_id"),
	)
}

func (b *Bridge) httpTenantStream(w http.ResponseWriter, r *http.Request) {
	filter := connFilter{
		tenantID:   httpmid.GetTenantID(r.Context()),
		eventTypes: parseTypes(r.URL.Query().Get("types")),
	}
	b.stream(w, r, filter)
}

func (b *Bridge) httpResourceStream(w http.ResponseWriter, r *http.Request) {
	filter := connFilter{
		aggregateType: web.Param(r, "resource_type"),
		aggregateID:   web.Param(r, "resource_id"),
		eventTypes:    parseTypes(r.URL.Query().Get("types")),
	}
	b.stream(w, r, filter)
}

func (b *Bridge) stream(w http.ResponseWriter, r *http.Request, filter connFilter) {
	subject := httpmid.GetSubject(r.Context())
	c := b.hub.connect(subject, filter)
	if c == nil {
		web.RespondJSONError(w, web.ErrTooManyRequests("too many concurrent event streams"))
		return
	}
	defer b.hub.disconnect(c)

	if b.hub.opts.maxConnAge > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), b.hub.opts.maxConnAge)
		defer cancel()
		r = r.WithContext(ctx)
	}

	web.NewSSEStream(c.ch, web.WithHeartbeat(b.hub.opts.heartbeat)).ServeHTTP(w, r)
}

func parseTypes(raw string) map[string]bool {
	if raw == "" {
		return nil
	}
	set := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			set[t] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
