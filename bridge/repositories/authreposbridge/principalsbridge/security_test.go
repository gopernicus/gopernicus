//go:build security

// This file is created once by gopernicus and will NOT be overwritten.

package principalsbridge

import (
	"testing"

	"github.com/gopernicus/gopernicus/workshop/testing/testhttp"
)

// setupSecurityServer boots the app stack WITH authentication enabled and
// returns a client pointed at it. The generated enforcement probes skip
// (loudly) while this returns nil — wire your authenticated test server
// here to activate them.
func setupSecurityServer(t *testing.T) *testhttp.Client {
	t.Helper()
	// TODO: boot your authenticated stack (e.g. via workshop/testing/testserver)
	// and return testhttp.New(server.URL).
	return nil
}
