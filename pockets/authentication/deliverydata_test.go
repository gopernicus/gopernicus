package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// The public purpose constants alias the delivery package's, so a host keys
// override maps and switches in a DeliveryDataHook without string literals.
func TestPurposeConstantsAliasDelivery(t *testing.T) {
	pairs := map[string]string{
		PurposeRegistrationVerification: delivery.PurposeRegistrationVerification,
		PurposePasswordReset:            delivery.PurposePasswordReset,
		PurposeOAuthPendingLink:         delivery.PurposeOAuthPendingLink,
		PurposeMagicLink:                delivery.PurposeMagicLink,
		PurposeLoginCode:                delivery.PurposeLoginCode,
		PurposeSensitiveCode:            delivery.PurposeSensitiveCode,
		PurposeIdentifierChangeProof:    delivery.PurposeIdentifierChangeProof,
		PurposeIdentifierChangeNotice:   delivery.PurposeIdentifierChangeNotice,
		PurposeInvitation:               delivery.PurposeInvitation,
		PurposeMemberAdded:              delivery.PurposeMemberAdded,
	}
	if len(pairs) != 10 {
		t.Fatalf("%d distinct purposes exported, want 10", len(pairs))
	}
	for pub, internal := range pairs {
		if pub != internal {
			t.Errorf("%q != %q", pub, internal)
		}
	}
}

// NewService validates the subject/SMS override maps at construction and accepts
// a valid DeliveryData + override wiring.
func TestNewServiceDeliveryOverrides(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff}

	rejected := []struct {
		name string
		mut  func(*Config)
	}{
		{"subject unknown purpose", func(c *Config) { c.EmailSubjects = map[string]string{"nope": "x"} }},
		{"subject empty", func(c *Config) { c.EmailSubjects = map[string]string{PurposeInvitation: "  "} }},
		{"subject parse failure", func(c *Config) { c.EmailSubjects = map[string]string{PurposeInvitation: "{{.Unclosed"} }},
		{"sms for email-only purpose", func(c *Config) { c.SMSBodies = map[string]string{PurposePasswordReset: "x"} }},
		{"sms unknown purpose", func(c *Config) { c.SMSBodies = map[string]string{"nope": "x"} }},
	}
	for _, r := range rejected {
		cfg := base
		r.mut(&cfg)
		_, err := NewService(Repositories{}, cfg)
		if !errors.Is(err, ErrDeliveryOverrideInvalid) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err=%v, want ErrDeliveryOverrideInvalid", r.name, err)
		}
	}

	cfg := base
	cfg.EmailSubjects = map[string]string{PurposeInvitation: "Join {{.ResourceName}}"}
	cfg.SMSBodies = map[string]string{PurposeInvitation: "Join {{.ResourceName}}: {{.Link}}"}
	cfg.DeliveryData = func(_ context.Context, purpose string, _ map[string]any) (map[string]any, error) {
		if purpose == PurposeInvitation {
			return map[string]any{"ResourceName": "Apollo"}, nil
		}
		return nil, nil
	}
	if _, err := NewService(Repositories{}, cfg); err != nil {
		t.Fatalf("NewService with valid overrides + hook: %v", err)
	}
}
