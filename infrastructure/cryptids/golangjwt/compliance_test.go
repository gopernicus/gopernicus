package golangjwt_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids/golangjwt"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids/cryptidstest"
)

func TestSignerCompliance(t *testing.T) {
	signer, err := golangjwt.NewSigner("test-secret-key-at-least-32-chars-long")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	cryptidstest.RunSignerSuite(t, signer)
}
