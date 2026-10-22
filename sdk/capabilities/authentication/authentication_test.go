package authentication_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/authenticationtest"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// undeclaredProvider implements Provider and nothing else, like every provider
// written before the capability hook.
type undeclaredProvider struct{}

func (undeclaredProvider) Issuer() string { return "https://legacy.example.test" }
func (undeclaredProvider) Authenticate(context.Context, authentication.Credential) (authentication.Identity, error) {
	return authentication.Identity{}, authentication.ErrInvalidCredential
}

// declaredProvider declares capabilities structurally; its pointer form proves
// a typed nil is treated as declaring nothing rather than called.
type declaredProvider struct {
	undeclaredProvider
	caps authentication.Capabilities
}

func (p *declaredProvider) Issuer() string { return "https://dev-issuer.example.test" }
func (p *declaredProvider) Capabilities() authentication.Capabilities {
	return p.caps
}

func TestCheckProvider(t *testing.T) {
	devOnly := &declaredProvider{caps: authentication.Capabilities{DevelopmentOnly: true}}
	safe := &declaredProvider{}

	tests := []struct {
		name         string
		mode         environment.Mode
		provider     authentication.Provider
		wantErr      error
		wantDeclared bool
		wantDevOnly  bool
	}{
		{name: "production refuses a declared development-only provider", mode: environment.ModeProduction, provider: devOnly,
			wantErr: authentication.ErrDevelopmentOnlyProvider, wantDeclared: true, wantDevOnly: true},
		{name: "production accepts a declared production provider", mode: environment.ModeProduction, provider: safe, wantDeclared: true},
		{name: "production accepts an undeclared provider", mode: environment.ModeProduction, provider: undeclaredProvider{}},
		{name: "production accepts a nil provider", mode: environment.ModeProduction, provider: nil},
		{name: "production accepts a typed-nil provider", mode: environment.ModeProduction, provider: (*declaredProvider)(nil)},
		{name: "development accepts a development-only provider and reports it", mode: environment.ModeDevelopment, provider: devOnly,
			wantDeclared: true, wantDevOnly: true},
		{name: "development accepts an undeclared provider", mode: environment.ModeDevelopment, provider: undeclaredProvider{}},
		{name: "empty mode is rejected, not defaulted", mode: "", provider: safe, wantErr: environment.ErrModeRequired, wantDeclared: true},
		{name: "unknown mode is rejected, not defaulted", mode: "staging", provider: safe, wantErr: environment.ErrModeInvalid, wantDeclared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posture, err := authentication.CheckProvider(tt.mode, tt.provider)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("CheckProvider() error = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckProvider() error = %v, want errors.Is %v", err, tt.wantErr)
			}
			if posture.Declared != tt.wantDeclared || posture.Capabilities.DevelopmentOnly != tt.wantDevOnly {
				t.Errorf("posture = %+v, want Declared=%v DevelopmentOnly=%v", posture, tt.wantDeclared, tt.wantDevOnly)
			}
		})
	}
}

func TestCheckProviderErrorNamesIssuer(t *testing.T) {
	_, err := authentication.CheckProvider(environment.ModeProduction, &declaredProvider{caps: authentication.Capabilities{DevelopmentOnly: true}})
	if err == nil || !strings.Contains(err.Error(), "https://dev-issuer.example.test") || !strings.HasPrefix(err.Error(), "authentication:") {
		t.Fatalf("error = %v, want an authentication: error naming the issuer", err)
	}
	if errors.Is(err, authentication.ErrInvalidCredential) || errors.Is(err, authentication.ErrProviderUnavailable) {
		t.Errorf("error = %v matches a credential or outage kind; a posture refusal is neither", err)
	}
}

// reportingProvider adds a declared posture to a working provider so the
// conformance suite's CapabilityReporter case runs its declared branches.
type reportingProvider struct {
	authentication.Provider
	caps authentication.Capabilities
}

func (p reportingProvider) Capabilities() authentication.Capabilities { return p.caps }

func TestOIDCConformanceWithDeclaredPosture(t *testing.T) {
	for _, caps := range []authentication.Capabilities{{DevelopmentOnly: true}, {}} {
		t.Run(fmt.Sprintf("DevelopmentOnly=%v", caps.DevelopmentOnly), func(t *testing.T) {
			authenticationtest.RunProvider(t, func(t *testing.T) authentication.Provider {
				return reportingProvider{
					Provider: authentication.NewOIDC(testIssuer, &hmacVerifier{issuer: testIssuer}, authentication.Profile{}),
					caps:     caps,
				}
			}, mint)
		})
	}
}
