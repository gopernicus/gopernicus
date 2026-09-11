package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// The public purpose constants alias the delivery package's, so a host keys
// override maps and switches in a DeliveryDataHook without string literals.
func TestPurposeConstantsAliasDelivery(t *testing.T) {
	pairs := map[string]string{
		delivery.PurposeRegistrationVerification: delivery.PurposeRegistrationVerification,
		delivery.PurposePasswordReset:            delivery.PurposePasswordReset,
		delivery.PurposeOAuthPendingLink:         delivery.PurposeOAuthPendingLink,
		delivery.PurposeMagicLink:                delivery.PurposeMagicLink,
		delivery.PurposeLoginCode:                delivery.PurposeLoginCode,
		delivery.PurposeSensitiveCode:            delivery.PurposeSensitiveCode,
		delivery.PurposeIdentifierChangeProof:    delivery.PurposeIdentifierChangeProof,
		delivery.PurposeIdentifierChangeNotice:   delivery.PurposeIdentifierChangeNotice,
		delivery.PurposeInvitation:               delivery.PurposeInvitation,
		delivery.PurposeMemberAdded:              delivery.PurposeMemberAdded,
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
	base := constructorConfig{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: environment.ModeDevelopment, DeliveryMode: delivery.ModeOff}

	rejected := []struct {
		name string
		mut  func(*constructorConfig)
	}{
		{"subject unknown purpose", func(c *constructorConfig) { c.EmailSubjects = map[string]string{"nope": "x"} }},
		{"subject empty", func(c *constructorConfig) { c.EmailSubjects = map[string]string{delivery.PurposeInvitation: "  "} }},
		{"subject parse failure", func(c *constructorConfig) {
			c.EmailSubjects = map[string]string{delivery.PurposeInvitation: "{{.Unclosed"}
		}},
		{"sms for email-only purpose", func(c *constructorConfig) { c.SMSBodies = map[string]string{delivery.PurposePasswordReset: "x"} }},
		{"sms unknown purpose", func(c *constructorConfig) { c.SMSBodies = map[string]string{"nope": "x"} }},
	}
	for _, r := range rejected {
		cfg := base
		r.mut(&cfg)
		_, err := newFixture(testRepositories(Repositories{}), cfg)
		if !errors.Is(err, delivery.ErrOverrideInvalid) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err=%v, want ErrDeliveryOverrideInvalid", r.name, err)
		}
	}

	cfg := base
	cfg.EmailSubjects = map[string]string{delivery.PurposeInvitation: "Join {{.ResourceName}}"}
	cfg.SMSBodies = map[string]string{delivery.PurposeInvitation: "Join {{.ResourceName}}: {{.Link}}"}
	cfg.DeliveryData = func(_ context.Context, purpose string, _ map[string]any) (map[string]any, error) {
		if purpose == delivery.PurposeInvitation {
			return map[string]any{"ResourceName": "Apollo"}, nil
		}
		return nil, nil
	}
	if _, err := newFixture(testRepositories(Repositories{}), cfg); err != nil {
		t.Fatalf("NewService with valid overrides + hook: %v", err)
	}
}
