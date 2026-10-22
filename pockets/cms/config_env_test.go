package cms

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
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

// TestConfigPostureFieldsHaveNoEnvTag pins RuntimeMode and
// AdminUngatedAcknowledged as programmatic: hosts run ParseEnvTags over
// cms.Config, so a same-named variable must never reach either field. The mode
// has one source — the process environment, read by the composition root.
func TestConfigPostureFieldsHaveNoEnvTag(t *testing.T) {
	t.Setenv("CMS_RUNTIME_MODE", "production")
	t.Setenv("RUNTIME_MODE", "production")
	t.Setenv("CMS_ADMIN_UNGATED_ACKNOWLEDGED", "true")
	t.Setenv("ADMIN_UNGATED_ACKNOWLEDGED", "true")

	for _, namespace := range []string{"", "CMS"} {
		var cfg Config
		if err := environment.ParseEnvTags(namespace, &cfg); err != nil {
			t.Fatalf("ParseEnvTags(%q): %v", namespace, err)
		}
		if cfg.RuntimeMode != "" {
			t.Errorf("ParseEnvTags(%q) filled RuntimeMode = %q, want zero", namespace, cfg.RuntimeMode)
		}
		if cfg.AdminUngatedAcknowledged {
			t.Errorf("ParseEnvTags(%q) filled AdminUngatedAcknowledged, want false", namespace)
		}
	}
}
