package email

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// metadatalessSender is a Sender that does not implement CapabilityReporter — a
// third-party or hand-rolled transport that declares nothing about itself.
type metadatalessSender struct{}

func (metadatalessSender) Send(context.Context, Message) error { return nil }

// structuralSender declares capabilities without being any bundled type, proving
// detection is structural rather than a concrete-type switch. It stands in for a
// third-party integration Sender (integrations/email/sendgrid declares TLS +
// DevelopmentOnly:false for an https host, and none + DevelopmentOnly:true
// otherwise); that module runs the real compatibility check itself.
type structuralSender struct{ caps Capabilities }

func (structuralSender) Send(context.Context, Message) error { return nil }
func (s structuralSender) Capabilities() Capabilities        { return s.caps }

func TestInspectSender(t *testing.T) {
	tests := []struct {
		name         string
		sender       Sender
		wantDeclared bool
		wantCaps     Capabilities
	}{
		{
			name:         "bundled Console declares development-only",
			sender:       NewConsole(slog.New(slog.NewTextHandler(io.Discard, nil))),
			wantDeclared: true,
			wantCaps:     Capabilities{TransportSecurity: TransportSecurityNone, DevelopmentOnly: true},
		},
		{
			name:         "bundled SMTP declares production-capable",
			sender:       NewSMTP(SMTPConfig{Host: "mail.example.com", Port: "587"}),
			wantDeclared: true,
			wantCaps:     Capabilities{TransportSecurity: TransportSecurityStartTLS, DevelopmentOnly: false},
		},
		{
			name:         "third-party TLS sender declares production-capable",
			sender:       structuralSender{caps: Capabilities{TransportSecurity: TransportSecurityTLS}},
			wantDeclared: true,
			wantCaps:     Capabilities{TransportSecurity: TransportSecurityTLS},
		},
		{
			name:         "metadata-less sender declares nothing",
			sender:       metadatalessSender{},
			wantDeclared: false,
		},
		{
			name:         "nil sender declares nothing",
			sender:       nil,
			wantDeclared: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InspectSender(tt.sender)
			if got.Declared != tt.wantDeclared {
				t.Errorf("Declared = %v, want %v", got.Declared, tt.wantDeclared)
			}
			if got.Capabilities != tt.wantCaps {
				t.Errorf("Capabilities = %+v, want %+v", got.Capabilities, tt.wantCaps)
			}
		})
	}
}

func TestCheckSender(t *testing.T) {
	console := NewConsole(slog.New(slog.NewTextHandler(io.Discard, nil)))
	smtp := NewSMTP(SMTPConfig{Host: "mail.example.com", Port: "587"})
	tls := structuralSender{caps: Capabilities{TransportSecurity: TransportSecurityTLS}}
	devThirdParty := structuralSender{caps: Capabilities{TransportSecurity: TransportSecurityNone, DevelopmentOnly: true}}
	bare := metadatalessSender{}

	tests := []struct {
		name             string
		mode             environment.Mode
		sender           Sender
		wantErr          error
		wantProdCapable  bool
		wantErrSubstring string
		wantPostureKept  bool // posture is still returned on rejection
	}{
		{
			name: "production accepts a declared production-capable sender",
			mode: environment.ModeProduction, sender: smtp,
			wantProdCapable: true,
		},
		{
			name: "production accepts a third-party TLS sender",
			mode: environment.ModeProduction, sender: tls,
			wantProdCapable: true,
		},
		{
			name: "production rejects a development-only sender",
			mode: environment.ModeProduction, sender: console,
			wantErr: ErrInsecureTransport, wantErrSubstring: "development-only", wantPostureKept: true,
		},
		{
			name: "production rejects a development-only third-party sender",
			mode: environment.ModeProduction, sender: devThirdParty,
			wantErr: ErrInsecureTransport, wantErrSubstring: "development-only", wantPostureKept: true,
		},
		{
			name: "production rejects a metadata-less sender",
			mode: environment.ModeProduction, sender: bare,
			wantErr: ErrInsecureTransport, wantErrSubstring: "no capability metadata",
		},
		{
			name: "production rejects a nil sender",
			mode: environment.ModeProduction, sender: nil,
			wantErr: ErrInsecureTransport, wantErrSubstring: "no capability metadata",
		},
		{
			name: "development accepts a development-only sender",
			mode: environment.ModeDevelopment, sender: console,
			wantProdCapable: false, wantPostureKept: true,
		},
		{
			name: "development accepts a metadata-less sender",
			mode: environment.ModeDevelopment, sender: bare,
			wantProdCapable: false,
		},
		{
			name: "development accepts a production-capable sender",
			mode: environment.ModeDevelopment, sender: smtp,
			wantProdCapable: true,
		},
		{
			name: "empty mode is rejected, not defaulted",
			mode: environment.Mode(""), sender: smtp,
			wantErr: environment.ErrModeRequired,
		},
		{
			name: "unknown mode is rejected, not defaulted",
			mode: environment.Mode("staging"), sender: smtp,
			wantErr: environment.ErrModeInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posture, err := CheckSender(tt.mode, tt.sender)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CheckSender() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if tt.wantErrSubstring != "" && !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Errorf("error %q does not explain the reason %q", err.Error(), tt.wantErrSubstring)
				}
				if tt.wantPostureKept && !posture.Declared {
					t.Error("rejection dropped the declared posture the caller may want to report")
				}
				return
			}

			if err != nil {
				t.Fatalf("CheckSender() error = %v, want nil", err)
			}
			if got := posture.ProductionCapable(); got != tt.wantProdCapable {
				t.Errorf("ProductionCapable() = %v, want %v", got, tt.wantProdCapable)
			}
		})
	}
}

// TestCheckSenderErrorIsCapabilityOwned pins that the sdk validator's wording is
// the capability's, with no authentication vocabulary leaking into it.
func TestCheckSenderErrorIsCapabilityOwned(t *testing.T) {
	_, err := CheckSender(environment.ModeProduction, metadatalessSender{})
	if err == nil {
		t.Fatal("want error")
	}
	for _, forbidden := range []string{"auth", "RuntimeMode", "Config."} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("capability error %q leaks pocket vocabulary %q", err.Error(), forbidden)
		}
	}
	if !strings.Contains(err.Error(), "email:") {
		t.Errorf("error %q is not prefixed by its owning package", err.Error())
	}
}
