package authentication

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// TestConfigEnvTags pins the AUTH_* keys on the previously untagged Config
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

	var cfg Config
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
