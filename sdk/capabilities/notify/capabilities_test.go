package notify

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestConsoleCapabilities_DevelopmentOnly proves the bundled console notifier
// declares itself development-only so a production host rejects it (auth v3
// §6.3): it logs message bodies rather than delivering them.
func TestConsoleCapabilities_DevelopmentOnly(t *testing.T) {
	var n Notifier = NewConsole(identity.KindPhone, nil)
	r, ok := n.(CapabilityReporter)
	if !ok {
		t.Fatal("Console does not implement CapabilityReporter")
	}
	caps := r.Capabilities()
	if !caps.DevelopmentOnly {
		t.Error("Console.Capabilities().DevelopmentOnly = false, want true")
	}
	if caps.TransportSecurity != TransportSecurityNone {
		t.Errorf("Console.Capabilities().TransportSecurity = %q, want %q", caps.TransportSecurity, TransportSecurityNone)
	}
}
