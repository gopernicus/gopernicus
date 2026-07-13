package email

import "testing"

// TestConsoleCapabilities_DevelopmentOnly proves the bundled console sender
// declares itself development-only so a production host rejects it (auth v3
// §6.3): it logs message bodies rather than delivering them.
func TestConsoleCapabilities_DevelopmentOnly(t *testing.T) {
	var s Sender = NewConsole(nil)
	r, ok := s.(CapabilityReporter)
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

// TestSMTPCapabilities_ProductionCapable proves the bundled SMTP sender declares
// metadata and is not development-only, so a production host accepts it.
func TestSMTPCapabilities_ProductionCapable(t *testing.T) {
	var s Sender = NewSMTP(SMTPConfig{Host: "localhost", Port: "25"})
	r, ok := s.(CapabilityReporter)
	if !ok {
		t.Fatal("SMTP does not implement CapabilityReporter")
	}
	if r.Capabilities().DevelopmentOnly {
		t.Error("SMTP.Capabilities().DevelopmentOnly = true, want false")
	}
}
