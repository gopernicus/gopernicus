// Package authentication assembles the authentication pocket's public use cases,
// optional HTTP adapter and host-owned delivery runtime.
package authentication

import (
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
)

// Components holds the independently usable components built by New. Invitations
// is nil when disabled. HTTP registration and delivery execution are host-owned.
type Components struct {
	Authentication *authlogic.Service
	Invitations    *invitations.Service
	HTTP           *authenticationhttp.Adapter
	Delivery       *delivery.Runtime
}
