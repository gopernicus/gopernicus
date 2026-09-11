package authentication

import (
	"errors"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// stubHTMLViews is a no-op Views implementation for the construction matrix: NewService
// asserts wiring and never renders, so every method returns a nil renderer.
type stubHTMLViews struct{}

func (stubHTMLViews) Login(inbound.LoginPage) web.Renderer                         { return nil }
func (stubHTMLViews) Register(inbound.RegisterPage) web.Renderer                   { return nil }
func (stubHTMLViews) Verify(inbound.VerifyPage) web.Renderer                       { return nil }
func (stubHTMLViews) ForgotPassword(inbound.ForgotPage) web.Renderer               { return nil }
func (stubHTMLViews) ResetPassword(inbound.ResetPage) web.Renderer                 { return nil }
func (stubHTMLViews) PasswordlessStart(inbound.PasswordlessStartPage) web.Renderer { return nil }
func (stubHTMLViews) PasswordlessCode(inbound.PasswordlessCodePage) web.Renderer   { return nil }
func (stubHTMLViews) MagicLinkLanding(inbound.MagicLinkPage) web.Renderer          { return nil }
func (stubHTMLViews) OAuthLinkLanding(inbound.OAuthLinkPage) web.Renderer          { return nil }
func (stubHTMLViews) CheckDelivery(inbound.CheckDeliveryPage) web.Renderer         { return nil }
func (stubHTMLViews) StepUp(inbound.StepUpPage) web.Renderer                       { return nil }
func (stubHTMLViews) AccountSecurity(inbound.AccountSecurityPage) web.Renderer     { return nil }
func (stubHTMLViews) IdentifierForm(inbound.IdentifierFormPage) web.Renderer       { return nil }
func (stubHTMLViews) PasswordForm(inbound.PasswordFormPage) web.Renderer           { return nil }
func (stubHTMLViews) OAuthUnlink(inbound.OAuthUnlinkPage) web.Renderer             { return nil }
func (stubHTMLViews) Status(inbound.StatusPage) web.Renderer                       { return nil }
func (stubHTMLViews) Error(inbound.ErrorPage) web.Renderer                         { return nil }

// TestHTMLPolicyConstructionMatrix proves the frozen GOTH-0.4 constructorConfig matrix: the HTML
// resource policy is only consulted by the HTML surface (gated on Views), so setting it
// with a nil Views is the loud contradictory-wiring error, while every other
// combination constructs.
func TestHTMLPolicyConstructionMatrix(t *testing.T) {
	base := constructorConfig{
		Hasher:       stubHasher{},
		Mailer:       stubMailer{},
		TokenSigner:  stubSigner{},
		RuntimeMode:  environment.ModeDevelopment,
		DeliveryMode: delivery.ModeOff,
	}
	policy, err := inbound.NewHTMLResourcePolicy(
		inbound.HTMLResourceDirective{Kind: inbound.HTMLScriptSrc, Sources: []string{"'self'"}, Nonce: true},
	)
	if err != nil {
		t.Fatalf("NewHTMLResourcePolicy: %v", err)
	}

	tests := []struct {
		name    string
		views   inbound.Views
		policy  *inbound.HTMLResourcePolicy
		wantErr error
	}{
		{"api-only, no policy (historical default)", nil, nil, nil},
		{"html surface, no policy (asset-free CSP)", stubHTMLViews{}, nil, nil},
		{"html surface, widening policy", stubHTMLViews{}, policy, nil},
		{"policy without views is loud", nil, policy, ErrHTMLPolicyWithoutViews},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.Views = tt.views
			cfg.HTMLPolicy = tt.policy
			_, err := newFixture(testRepositories(Repositories{}), cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("NewService: err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewService: err=%v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestNewHTMLResourcePolicyPublicConstructorValidates proves the re-exported public
// constructor rejects a malicious source through the same validation the internal one
// applies (no bypass at the public boundary).
func TestNewHTMLResourcePolicyPublicConstructorValidates(t *testing.T) {
	if _, err := inbound.NewHTMLResourcePolicy(inbound.HTMLResourceDirective{Kind: inbound.HTMLScriptSrc, Sources: []string{"'self'; script-src *"}}); err == nil {
		t.Fatal("public NewHTMLResourcePolicy accepted a directive-splitting source")
	}
	if _, err := inbound.NewHTMLResourcePolicy(inbound.HTMLResourceDirective{Kind: "default-src", Sources: []string{"'self'"}}); err == nil {
		t.Fatal("public NewHTMLResourcePolicy accepted the fixed default-src directive")
	}
}
