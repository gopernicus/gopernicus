package authentication

import (
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestConfigEnvTags pins the AUTH_* keys on the previously untagged constructorConfig
// fields hosts read from the environment today. The lists use the parser's
// default comma separator. SessionCookie is deliberately NOT exercised here: it
// is a nested struct, and this module's sdk pin predates nested-struct descent —
// its tags are proven in the sdk's own tag suite.
func TestConfigEnvTags(t *testing.T) {
	t.Setenv("AUTH_MAIL_FROM", "no-reply@example.com")
	t.Setenv("AUTH_ALLOWED_ORIGINS", "https://app.example.com,https://admin.example.com")
	t.Setenv("AUTH_OAUTH_CALLBACK_BASE", "https://app.example.com")
	t.Setenv("AUTH_REDIRECT_ALLOWLIST", "https://app.example.com/welcome")
	t.Setenv("AUTH_REQUIRE_VERIFIED_EMAIL", "true")
	t.Setenv("AUTH_PASSWORDLESS", "email,phone")
	t.Setenv("AUTH_REGISTRATION", "invitation_only")
	t.Setenv("AUTH_SERVICE_ACCOUNT_STATUS", "true")
	t.Setenv("AUTH_API_KEY_MAX_TTL", "2160h")
	t.Setenv("AUTH_IDLE_TIMEOUT", "2h")

	var cfg constructorConfig
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.MailFrom != "no-reply@example.com" {
		t.Errorf("MailFrom = %q, want %q", cfg.MailFrom, "no-reply@example.com")
	}
	if got, want := cfg.AllowedOrigins, []string{"https://app.example.com", "https://admin.example.com"}; !equalStrings(got, want) {
		t.Errorf("AllowedOrigins = %v, want %v", got, want)
	}
	if cfg.OAuthCallbackBase != "https://app.example.com" {
		t.Errorf("OAuthCallbackBase = %q, want %q", cfg.OAuthCallbackBase, "https://app.example.com")
	}
	if got, want := cfg.RedirectAllowlist, []string{"https://app.example.com/welcome"}; !equalStrings(got, want) {
		t.Errorf("RedirectAllowlist = %v, want %v", got, want)
	}
	if !cfg.RequireVerifiedEmail {
		t.Error("RequireVerifiedEmail = false, want true")
	}
	if got, want := cfg.Passwordless, []string{"email", "phone"}; !equalStrings(got, want) {
		t.Errorf("Passwordless = %v, want %v", got, want)
	}
	if cfg.Registration != RegistrationInvitationOnly {
		t.Errorf("Registration = %q, want invitation_only", cfg.Registration)
	}
	if !cfg.ServiceAccountStatus {
		t.Error("ServiceAccountStatus = false, want true")
	}
	if cfg.APIKeyMaxTTL != 2160*time.Hour {
		t.Errorf("APIKeyMaxTTL = %v, want 2160h", cfg.APIKeyMaxTTL)
	}
	if cfg.IdleTimeout != 2*time.Hour {
		t.Errorf("IdleTimeout = %v, want 2h", cfg.IdleTimeout)
	}

	// The group structs carry the same keys, so a host embedding them parses
	// them alike.
	var groups struct {
		IdentityConfig
		AdministrationConfig
		SessionsConfig
	}
	if err := environment.ParseEnvTags("", &groups); err != nil {
		t.Fatalf("ParseEnvTags(groups): %v", err)
	}
	if groups.Registration != RegistrationInvitationOnly || !groups.ServiceAccountStatus || groups.APIKeyMaxTTL != 2160*time.Hour {
		t.Errorf("groups = %q %v %v", groups.Registration, groups.ServiceAccountStatus, groups.APIKeyMaxTTL)
	}
	if groups.IdleTimeout != 2*time.Hour {
		t.Errorf("groups.IdleTimeout = %v, want 2h", groups.IdleTimeout)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
