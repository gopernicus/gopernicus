// Package feature defines the registration contract between a host application
// and a feature module (Django-app / Rails-engine shaped). It carries only
// stdlib types plus sdk/web (itself stdlib-only): a feature depends on these
// narrow ports, never on a service-locator god-object. The host owns the
// concrete Router implementation and wires it into a Mount. Database
// migrations are host-owned and applied outside feature registration.
package feature

import (
	"log/slog"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// RouteRegistrar is the inbound mount point a feature uses to register its HTTP
// routes. web.WebHandler satisfies it implicitly, so the host passes its router
// without the feature importing the concrete handler. The signature mirrors
// web.WebHandler.Handle so existing routers plug in unchanged.
type RouteRegistrar interface {
	Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware)
}

// Mount is the narrow, typed context handed to a feature's Register. There is no
// service locator: a feature reaches only these ports, and cross-feature
// composition is explicit typed wiring at the host's main, never a global bus.
type Mount struct {
	Router RouteRegistrar
	Logger *slog.Logger
}
