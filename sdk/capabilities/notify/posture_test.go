package notify

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// metadatalessNotifier does not implement CapabilityReporter — a third-party or
// hand-rolled transport that declares nothing about itself.
type metadatalessNotifier struct{}

// structuralNotifier declares capabilities without being any bundled type,
// proving detection is structural rather than a concrete-type switch.
type structuralNotifier struct{ caps Capabilities }

func (n structuralNotifier) Capabilities() Capabilities { return n.caps }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestInspectTransport(t *testing.T) {
	tests := []struct {
		name         string
		notifier     any
		wantDeclared bool
		wantCaps     Capabilities
	}{
		{
			name:         "bundled Console declares development-only",
			notifier:     NewConsole(discardLogger()),
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
			got := InspectTransport(tt.notifier)
			if got.Declared != tt.wantDeclared {
				t.Errorf("Declared = %v, want %v", got.Declared, tt.wantDeclared)
			}
			if got.Capabilities != tt.wantCaps {
				t.Errorf("Capabilities = %+v, want %+v", got.Capabilities, tt.wantCaps)
			}
		})
	}
}

func TestCheckTransport(t *testing.T) {
	console := NewConsole(discardLogger())
	tls := structuralNotifier{caps: Capabilities{TransportSecurity: TransportSecurityTLS}}
	bare := metadatalessNotifier{}

	tests := []struct {
		name             string
		mode             environment.Mode
		notifier         any
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
			posture, err := CheckTransport(tt.mode, tt.notifier)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CheckTransport() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if tt.wantErrSubstring != "" && !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Errorf("error %q does not explain the reason %q", err.Error(), tt.wantErrSubstring)
				}
				return
			}

			if err != nil {
				t.Fatalf("CheckTransport() error = %v, want nil", err)
			}
			if got := posture.ProductionCapable(); got != tt.wantProdCapable {
				t.Errorf("ProductionCapable() = %v, want %v", got, tt.wantProdCapable)
			}
		})
	}
}

func TestCheckTransportRequireMandatoryTLS(t *testing.T) {
	starttls := structuralNotifier{caps: Capabilities{TransportSecurity: TransportSecurityStartTLS}}
	tls := structuralNotifier{caps: Capabilities{TransportSecurity: TransportSecurityTLS}}

	t.Run("default verdict unchanged", func(t *testing.T) {
		posture, err := CheckTransport(environment.ModeProduction, starttls)
		if err != nil {
			t.Fatalf("CheckTransport(production, starttls) = %v, want nil: the default admits STARTTLS", err)
		}
		if !posture.ProductionCapable() {
			t.Error("ProductionCapable() = false, want true")
		}
	})
	t.Run("production refuses STARTTLS under the option", func(t *testing.T) {
		posture, err := CheckTransport(environment.ModeProduction, starttls, RequireMandatoryTLS())
		if !errors.Is(err, ErrOpportunisticTLS) {
			t.Fatalf("error = %v, want ErrOpportunisticTLS", err)
		}
		if !errors.Is(err, ErrInsecureTransport) {
			t.Errorf("error = %v, want it to keep matching ErrInsecureTransport", err)
		}
		if !strings.Contains(err.Error(), "notify:") || !strings.Contains(err.Error(), "STARTTLS") {
			t.Errorf("error %q does not explain itself", err)
		}
		if posture.Capabilities.TransportSecurity != TransportSecurityStartTLS {
			t.Errorf("rejection dropped the declared posture: %+v", posture)
		}
	})
	t.Run("production admits mandatory TLS under the option", func(t *testing.T) {
		if _, err := CheckTransport(environment.ModeProduction, tls, RequireMandatoryTLS()); err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	})
	t.Run("development accepts STARTTLS under the option", func(t *testing.T) {
		posture, err := CheckTransport(environment.ModeDevelopment, starttls, RequireMandatoryTLS())
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if !posture.Declared || posture.Capabilities.TransportSecurity != TransportSecurityStartTLS {
			t.Errorf("posture = %+v, want the declared STARTTLS posture", posture)
		}
	})
	t.Run("the option keeps earlier refusals", func(t *testing.T) {
		_, err := CheckTransport(environment.ModeProduction, NewConsole(discardLogger()), RequireMandatoryTLS())
		if !errors.Is(err, ErrInsecureTransport) || errors.Is(err, ErrOpportunisticTLS) {
			t.Fatalf("error = %v, want the development-only refusal", err)
		}
	})
	t.Run("empty mode still rejected", func(t *testing.T) {
		if _, err := CheckTransport("", starttls, RequireMandatoryTLS()); !errors.Is(err, environment.ErrModeRequired) {
			t.Fatalf("error = %v, want ErrModeRequired", err)
		}
	})
	t.Run("nil option rejected", func(t *testing.T) {
		if _, err := CheckTransport(environment.ModeProduction, tls, nil); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("error = %v, want sdk.ErrInvalidInput", err)
		}
	})
}

// TestCheckTransportErrorIsCapabilityOwned pins that the sdk validator's wording
// is the capability's, with no authentication vocabulary leaking into it.
func TestCheckTransportErrorIsCapabilityOwned(t *testing.T) {
	_, err := CheckTransport(environment.ModeProduction, metadatalessNotifier{})
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
