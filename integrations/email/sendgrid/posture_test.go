package sendgrid_test

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/email/sendgrid"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// TestCheckSenderCompatibility is the integration-side half of the sdk's
// capability-owned production check (coordination-hub-auth-upstream CHAU-3.2).
// The sdk validator detects a reporter structurally, so this proves the real
// third-party Sender — not a fake — resolves the way its Capabilities claim.
func TestCheckSenderCompatibility(t *testing.T) {
	tests := []struct {
		name            string
		host            string
		mode            environment.Mode
		wantErr         error
		wantProdCapable bool
	}{
		{
			name: "default host is production-capable in production",
			host: "", mode: environment.ModeProduction,
			wantProdCapable: true,
		},
		{
			name: "explicit https host is production-capable in production",
			host: "https://api.eu.sendgrid.com", mode: environment.ModeProduction,
			wantProdCapable: true,
		},
		{
			name: "plain-http host is rejected in production",
			host: "http://127.0.0.1:8080", mode: environment.ModeProduction,
			wantErr: email.ErrInsecureTransport,
		},
		{
			name: "plain-http host is accepted in development but not production-capable",
			host: "http://127.0.0.1:8080", mode: environment.ModeDevelopment,
			wantProdCapable: false,
		},
		{
			name: "default host is production-capable in development too",
			host: "", mode: environment.ModeDevelopment,
			wantProdCapable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := sendgrid.New(sendgrid.Config{APIKey: "test-key", Host: tt.host})

			posture, err := email.CheckSender(tt.mode, sender)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CheckSender() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckSender() error = %v, want nil", err)
			}
			if !posture.Declared {
				t.Fatal("the sendgrid Sender was not detected as a CapabilityReporter")
			}
			if got := posture.ProductionCapable(); got != tt.wantProdCapable {
				t.Errorf("ProductionCapable() = %v, want %v", got, tt.wantProdCapable)
			}
		})
	}
}
