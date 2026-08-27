// Package pocket defines the registration contract between a host application
// and a pocket module (Django-app / Rails-engine shaped). It carries only
// stdlib types plus sdk/foundation/web and sdk/capabilities/events (both stdlib-only): a pocket depends
// on these narrow ports, never on a service-locator god-object. The host owns
// the concrete Router implementation and wires it into a Mount. Database
// migrations are host-owned and applied outside pocket registration.
package pocket

import (
	"log/slog"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// RouteRegistrar is the inbound mount point a pocket uses to register its HTTP
// routes. web.WebHandler satisfies it implicitly, so the host passes its router
// without the pocket importing the concrete handler. The signature mirrors
// web.WebHandler.Handle so existing routers plug in unchanged.
type RouteRegistrar interface {
	Handle(method, path string, handler http.HandlerFunc, middleware ...web.Middleware)
}

// Mount is the narrow, typed context handed to a pocket's Register. There is no
// service locator: a pocket reaches only these ports, and cross-pocket
// composition is explicit typed wiring at the host's main, never a global bus.
type Mount struct {
	Router RouteRegistrar
	Logger *slog.Logger

	// Events is the emit-only rail a pocket publishes ephemeral notifications
	// on. It is best-effort at-most-once — never transactional, and an event is
	// lost on a crash between a domain commit and the emit. The durable path
	// rides pocket Repositories, never this field. A nil Events means the
	// pocket emits nothing: pockets either nil-guard the field or wrap
	// events.Noop, with identical behavior.
	Events events.Emitter
}
