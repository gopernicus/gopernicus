package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// metadatalessNotifier does not implement CapabilityReporter — a third-party or
// hand-rolled transport that declares nothing about itself.
type metadatalessNotifier struct{}

func (metadatalessNotifier) Kind() string { return identity.KindPhone }
func (metadatalessNotifier) Notify(context.Context, identity.Address, Message) error {
	return nil
}

// structuralNotifier declares capabilities without being any bundled type,
// proving detection is structural rather than a concrete-type switch.
type structuralNotifier struct{ caps Capabilities }

func (structuralNotifier) Kind() string { return identity.KindPhone }
func (structuralNotifier) Notify(context.Context, identity.Address, Message) error {
	return nil
}
func (n structuralNotifier) Capabilities() Capabilities { return n.caps }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestInspectNotifier(t *testing.T) {
	tests := []struct {
		name         string
		notifier     Notifier
		wantDeclared bool
		wantCaps     Capabilities
	}{
		{
			name:         "bundled Console declares development-only",
			notifier:     NewConsole(identity.KindEmail, discardLogger()),
			wantDeclared: true,
			wantCaps:     Capabilities{TransportSecurity: TransportSecurityNone, DevelopmentOnly: true},
		},
		{
			name:         "third-party TLS notifier declares production-capable",
			notifier:     structuralNotifier{caps: Capabilities{TransportSecurity: TransportSecurityTLS}},
			wantDeclared: true,
			wantCaps:     Capabilities{TransportSecurity: TransportSecurityTLS},
		},
		{
			name:         "metadata-less notifier declares nothing",
			notifier:     metadatalessNotifier{},
			wantDeclared: false,
		},
		{
			name:         "nil notifier declares nothing",
			notifier:     nil,
			wantDeclared: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InspectNotifier(tt.notifier)
			if got.Declared != tt.wantDeclared {
				t.Errorf("Declared = %v, want %v", got.Declared, tt.wantDeclared)
			}
			if got.Capabilities != tt.wantCaps {
				t.Errorf("Capabilities = %+v, want %+v", got.Capabilities, tt.wantCaps)
			}
		})
	}
}

func TestCheckNotifier(t *testing.T) {
	console := NewConsole(identity.KindEmail, discardLogger())
	tls := structuralNotifier{caps: Capabilities{TransportSecurity: TransportSecurityTLS}}
	bare := metadatalessNotifier{}

	tests := []struct {
		name             string
		mode             environment.Mode
		notifier         Notifier
		wantErr          error
		wantErrSubstring string
		wantProdCapable  bool
	}{
		{
			name: "production accepts a declared production-capable notifier",
			mode: environment.ModeProduction, notifier: tls,
			wantProdCapable: true,
		},
		{
			name: "production rejects the bundled development-only Console",
			mode: environment.ModeProduction, notifier: console,
			wantErr: ErrInsecureTransport, wantErrSubstring: "development-only",
		},
		{
			name: "production rejects a metadata-less notifier",
			mode: environment.ModeProduction, notifier: bare,
			wantErr: ErrInsecureTransport, wantErrSubstring: "no capability metadata",
		},
		{
			name: "production rejects a nil notifier",
			mode: environment.ModeProduction, notifier: nil,
			wantErr: ErrInsecureTransport, wantErrSubstring: "no capability metadata",
		},
		{
			name: "development accepts the Console and reports it is not production-capable",
			mode: environment.ModeDevelopment, notifier: console,
			wantProdCapable: false,
		},
		{
			name: "development accepts a metadata-less notifier",
			mode: environment.ModeDevelopment, notifier: bare,
			wantProdCapable: false,
		},
		{
			name: "empty mode is rejected, not defaulted",
			mode: environment.Mode(""), notifier: tls,
			wantErr: environment.ErrModeRequired,
		},
		{
			name: "unknown mode is rejected, not defaulted",
			mode: environment.Mode("staging"), notifier: tls,
			wantErr: environment.ErrModeInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posture, err := CheckNotifier(tt.mode, tt.notifier)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CheckNotifier() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if tt.wantErrSubstring != "" && !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Errorf("error %q does not explain the reason %q", err.Error(), tt.wantErrSubstring)
				}
				return
			}

			if err != nil {
				t.Fatalf("CheckNotifier() error = %v, want nil", err)
			}
			if got := posture.ProductionCapable(); got != tt.wantProdCapable {
				t.Errorf("ProductionCapable() = %v, want %v", got, tt.wantProdCapable)
			}
		})
	}
}

// TestCheckNotifierErrorIsCapabilityOwned pins that the sdk validator's wording
// is the capability's, with no authentication vocabulary leaking into it.
func TestCheckNotifierErrorIsCapabilityOwned(t *testing.T) {
	_, err := CheckNotifier(environment.ModeProduction, metadatalessNotifier{})
	if err == nil {
		t.Fatal("want error")
	}
	for _, forbidden := range []string{"auth", "RuntimeMode", "Config."} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("capability error %q leaks pocket vocabulary %q", err.Error(), forbidden)
		}
	}
	if !strings.Contains(err.Error(), "notify:") {
		t.Errorf("error %q is not prefixed by its owning package", err.Error())
	}
}
