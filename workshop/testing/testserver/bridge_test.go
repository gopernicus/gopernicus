package testserver

import (
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/web"
)

// pingBridge is a minimal RouteMounter standing in for a generated bridge.
type pingBridge struct{}

func (pingBridge) AddHttpRoutes(group *web.RouteGroup) {
	group.Handle(http.MethodGet, "/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestServeBridge(t *testing.T) {
	client := ServeBridge(t, pingBridge{})
	client.Get(t, "/ping").RequireStatus(t, http.StatusNoContent)
}

func TestNewRateLimiter(t *testing.T) {
	if NewRateLimiter() == nil {
		t.Fatal("NewRateLimiter returned nil")
	}
}
