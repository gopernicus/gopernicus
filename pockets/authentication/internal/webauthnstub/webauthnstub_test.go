package webauthnstub

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
)

// TestConformance holds the stub to the port's executable contract, so the
// pocket's ceremony tests exercise a relying party that behaves like the
// implementation of record.
func TestConformance(t *testing.T) {
	webauthntest.Run(t, func(cfg webauthn.Config) (webauthn.RelyingParty, error) { return New(cfg) })
}
