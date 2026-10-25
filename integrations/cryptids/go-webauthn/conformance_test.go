package gowebauthn_test

import (
	"testing"

	gowebauthn "github.com/gopernicus/gopernicus/integrations/cryptids/go-webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
)

func TestConformance(t *testing.T) {
	webauthntest.Run(t, func(cfg webauthn.Config) (webauthn.RelyingParty, error) {
		rp, err := gowebauthn.New(cfg)
		if err != nil {
			return nil, err
		}
		return rp, nil
	})
}
