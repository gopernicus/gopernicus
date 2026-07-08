package feature

import (
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// Route describes one HTTP route as data — its stable Name (e.g. "auth.login"),
// Method, Path, Handler, and any route-local Middleware. A feature builds its
// route table as a []Route value it can name, iterate, and reason about before
// anything is mounted: route tables become data (FS7). RegisterRoutes turns the
// table into registrations against whatever RouteRegistrar the host supplied.
//
// FS7 ships the data form deliberately WITHOUT the public override hook: no
// Config.Routes(func([]Route) []Route) seam exists until a real host hits the
// gap a single-route override fills. Do not add one here — the public Service
// bypass tier plus subsystem deny-by-absence cover the known cases.
type Route struct {
	Name       string
	Method     string
	Path       string
	Handler    http.HandlerFunc
	Middleware []web.Middleware
}

// RegisterRoutes mounts every route in routes onto r, in order. It is the
// counterpart to building a route table as data: a feature assembles its
// []Route once and hands it here, and each route is forwarded to the
// RouteRegistrar the host handed in — including any PrefixRegistrar or Group
// wrapper passed as Mount.Router, which the feature never has to know about.
func RegisterRoutes(r RouteRegistrar, routes []Route) {
	for _, rt := range routes {
		r.Handle(rt.Method, rt.Path, rt.Handler, rt.Middleware...)
	}
}
