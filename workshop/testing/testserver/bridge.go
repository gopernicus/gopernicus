package testserver

import (
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/memorylimiter"
	"github.com/gopernicus/gopernicus/workshop/testing/testhttp"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// RouteMounter mounts HTTP routes onto a route group. Generated bridges and
// bridge composites satisfy it via their AddHttpRoutes method.
type RouteMounter interface {
	AddHttpRoutes(group *web.RouteGroup)
}

// NewRateLimiter returns a rate limiter backed by the in-memory store with
// the default limit resolver — the wiring generated bridge tests use.
func NewRateLimiter() *ratelimiter.RateLimiter {
	return ratelimiter.New(memorylimiter.New(), ratelimiter.NewDefaultResolver())
}

// ServeBridge mounts the bridge's routes on a fresh web handler, starts an
// httptest server (closed via t.Cleanup), and returns a test HTTP client
// pointed at it.
func ServeBridge(t *testing.T, bridge RouteMounter) *testhttp.Client {
	t.Helper()

	handler := web.NewWebHandler()
	bridge.AddHttpRoutes(handler.Group(""))

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return testhttp.New(srv.URL)
}
