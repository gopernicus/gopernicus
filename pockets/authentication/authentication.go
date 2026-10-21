// Package authentication assembles the authentication pocket's public use cases,
// optional HTTP adapter and host-owned delivery runtime.
package authentication

import (
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
)

// Components holds the independently usable components built by New. Invitations
// and External are nil when disabled. HTTP registration and delivery execution
// are host-owned. External exposes revocation stats and the heartbeat of the
// engine Authentication already uses.
type Components struct {
	Authentication *authlogic.Service
	External       *authlogic.ExternalAuthenticator
	OAuth2         *oauth2.Service
	Invitations    *invitations.Service
	HTTP           *authenticationhttp.Adapter
	Delivery       *delivery.Runtime
}
