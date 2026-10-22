package oauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// undeclaredProvider implements Provider and nothing else, like every provider
// written before the capability hook.
type undeclaredProvider struct{}

func (undeclaredProvider) Name() string { return "legacy" }
func (undeclaredProvider) GetAuthorizationURL(AuthorizationRequest) (string, error) {
	return "", nil
}
func (undeclaredProvider) ExchangeCode(context.Context, string, string, string) (*TokenResponse, error) {
	return nil, nil
}
func (undeclaredProvider) GetUserInfo(context.Context, string) (*UserInfo, error) { return nil, nil }

// declaredProvider declares capabilities structurally; its pointer form proves
// a typed nil is treated as declaring nothing rather than called.
type declaredProvider struct {
	undeclaredProvider
	caps Capabilities
}

func (p *declaredProvider) Name() string               { return "fake" }
func (p *declaredProvider) Capabilities() Capabilities { return p.caps }

func TestCheckProvider(t *testing.T) {
	devOnly := &declaredProvider{caps: Capabilities{DevelopmentOnly: true}}
	safe := &declaredProvider{}

	tests := []struct {
		name         string
		mode         environment.Mode
		provider     Provider
		wantErr      error
		wantDeclared bool
		wantDevOnly  bool
	}{
		{name: "production refuses a declared development-only provider", mode: environment.ModeProduction, provider: devOnly,
			wantErr: ErrDevelopmentOnlyProvider, wantDeclared: true, wantDevOnly: true},
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
			posture, err := CheckProvider(tt.mode, tt.provider)
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

func TestCheckProviderErrorNamesProvider(t *testing.T) {
	_, err := CheckProvider(environment.ModeProduction, &declaredProvider{caps: Capabilities{DevelopmentOnly: true}})
	if err == nil || !strings.Contains(err.Error(), `"fake"`) || !strings.HasPrefix(err.Error(), "oauth:") {
		t.Fatalf("error = %v, want an oauth: error naming the provider", err)
	}
}
