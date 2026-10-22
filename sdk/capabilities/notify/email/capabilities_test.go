package email

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestConsoleCapabilities_DevelopmentOnly proves the bundled console sender
// declares itself development-only so a production host rejects it (auth v3
// §6.3): it logs message bodies rather than delivering them.
func TestConsoleCapabilities_DevelopmentOnly(t *testing.T) {
	var s Sender = NewConsole(nil)
	r, ok := s.(notify.CapabilityReporter)
	if !ok {
		t.Fatal("Console does not implement notify.CapabilityReporter")
	}
	caps := r.Capabilities()
	if !caps.DevelopmentOnly {
		t.Error("Console.Capabilities().DevelopmentOnly = false, want true")
	}
	if caps.TransportSecurity != notify.TransportSecurityNone {
		t.Errorf("Console.Capabilities().TransportSecurity = %q, want %q", caps.TransportSecurity, notify.TransportSecurityNone)
	}
}

// TestSMTPCapabilities_ProductionCapable proves the bundled SMTP sender declares
// metadata and is not development-only, so a production host accepts it.
func TestSMTPCapabilities_ProductionCapable(t *testing.T) {
	var s Sender = NewSMTP(SMTPConfig{Host: "localhost", Port: "25"})
	r, ok := s.(notify.CapabilityReporter)
	if !ok {
		t.Fatal("SMTP does not implement notify.CapabilityReporter")
	}
	if r.Capabilities().DevelopmentOnly {
		t.Error("SMTP.Capabilities().DevelopmentOnly = true, want false")
	}
}

// TestSMTPCapabilities_RequireTLS proves RequireTLS turns the declared posture
// from opportunistic STARTTLS into TLS, which notify.RequireMandatoryTLS admits.
func TestSMTPCapabilities_RequireTLS(t *testing.T) {
	tests := []struct {
		requireTLS bool
		want       notify.TransportSecurity
		wantErr    error
	}{
		{requireTLS: false, want: notify.TransportSecurityStartTLS, wantErr: notify.ErrOpportunisticTLS},
		{requireTLS: true, want: notify.TransportSecurityTLS},
	}
	for _, tt := range tests {
		s := NewSMTP(SMTPConfig{Host: "localhost", Port: "587", RequireTLS: tt.requireTLS})
		if got := s.Capabilities().TransportSecurity; got != tt.want {
			t.Errorf("RequireTLS=%v: TransportSecurity = %q, want %q", tt.requireTLS, got, tt.want)
		}
		_, err := notify.CheckTransport(environment.ModeProduction, s, notify.RequireMandatoryTLS())
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("RequireTLS=%v: CheckTransport error = %v, want %v", tt.requireTLS, err, tt.wantErr)
		}
	}
}
