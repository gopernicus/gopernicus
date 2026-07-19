package authentication

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// stubHTMLViews is a no-op Views implementation for the construction matrix: NewService
// asserts wiring and never renders, so every method returns a nil renderer.
type stubHTMLViews struct{}

func (stubHTMLViews) Login(LoginPage) web.Renderer                         { return nil }
func (stubHTMLViews) Register(RegisterPage) web.Renderer                   { return nil }
func (stubHTMLViews) Verify(VerifyPage) web.Renderer                       { return nil }
func (stubHTMLViews) ForgotPassword(ForgotPage) web.Renderer               { return nil }
func (stubHTMLViews) ResetPassword(ResetPage) web.Renderer                 { return nil }
func (stubHTMLViews) PasswordlessStart(PasswordlessStartPage) web.Renderer { return nil }
func (stubHTMLViews) PasswordlessCode(PasswordlessCodePage) web.Renderer   { return nil }
func (stubHTMLViews) MagicLinkLanding(MagicLinkPage) web.Renderer          { return nil }
func (stubHTMLViews) CheckDelivery(CheckDeliveryPage) web.Renderer         { return nil }
func (stubHTMLViews) StepUp(StepUpPage) web.Renderer                       { return nil }
func (stubHTMLViews) AccountSecurity(AccountSecurityPage) web.Renderer     { return nil }
func (stubHTMLViews) IdentifierForm(IdentifierFormPage) web.Renderer       { return nil }
func (stubHTMLViews) PasswordForm(PasswordFormPage) web.Renderer           { return nil }
func (stubHTMLViews) OAuthUnlink(OAuthUnlinkPage) web.Renderer             { return nil }
func (stubHTMLViews) Status(StatusPage) web.Renderer                       { return nil }
func (stubHTMLViews) Error(ErrorPage) web.Renderer                         { return nil }

// TestHTMLPolicyConstructionMatrix proves the frozen GOTH-0.4 Config matrix: the HTML
// resource policy is only consulted by the HTML surface (gated on Views), so setting it
// with a nil Views is the loud contradictory-wiring error, while every other
// combination constructs.
func TestHTMLPolicyConstructionMatrix(t *testing.T) {
	base := Config{
		Hasher:       stubHasher{},
		Mailer:       stubMailer{},
		TokenSigner:  stubSigner{},
		RuntimeMode:  RuntimeModeDevelopment,
		DeliveryMode: DeliveryModeOff,
	}
	policy, err := NewHTMLResourcePolicy(
		HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{"'self'"}, Nonce: true},
	)
	if err != nil {
		t.Fatalf("NewHTMLResourcePolicy: %v", err)
	}

	tests := []struct {
		name    string
		views   Views
		policy  *HTMLResourcePolicy
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
			_, err := NewService(Repositories{}, cfg)
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
	if _, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{"'self'; script-src *"}}); err == nil {
		t.Fatal("public NewHTMLResourcePolicy accepted a directive-splitting source")
	}
	if _, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: "default-src", Sources: []string{"'self'"}}); err == nil {
		t.Fatal("public NewHTMLResourcePolicy accepted the fixed default-src directive")
	}
}
