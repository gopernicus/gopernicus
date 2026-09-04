package cms

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// TestConfigEnvTags pins the CMS_* keys on Config: the contact-notification
// addresses are read through ParseEnvTags, never from os.Getenv inside the
// pocket. Every other Config field is a host collaborator with no tag.
func TestConfigEnvTags(t *testing.T) {
	t.Setenv("CMS_MAIL_FROM", "site@example.com")
	t.Setenv("CMS_CONTACT_TO", "inbox@example.com")

	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.MailFrom != "site@example.com" {
		t.Errorf("MailFrom = %q, want %q", cfg.MailFrom, "site@example.com")
	}
	if cfg.ContactTo != "inbox@example.com" {
		t.Errorf("ContactTo = %q, want %q", cfg.ContactTo, "inbox@example.com")
	}
}
