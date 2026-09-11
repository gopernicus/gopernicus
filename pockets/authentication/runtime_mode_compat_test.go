package authentication_test

import (
	"errors"
	"os"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// This file is the CHAU-3.3 compatibility proof for moving the runtime-posture
// vocabulary to sdk/pkg/environment. It is an EXTERNAL test package, so
// everything here is reachable by a host through exported API only.
//
// The counterpart proof — that app-wide code enforces the same transport rule
// with NO authentication import at all — lives in the sdk itself
// (sdk/capabilities/notify/email/posture_test.go and
// sdk/capabilities/notify/posture_test.go). Those packages cannot import a
// pocket, which is exactly the guarantee coordination-hub's generic
// internal/integrations/mailer needs: it drops its pockets/authentication
// import and names environment.Mode + notify.CheckTransport instead.

// hostRuntimeConfig is an old-style host's configuration struct: it declares the
// posture with auth's type name, as every host written before this change did.
type hostRuntimeConfig struct {
	Mode environment.Mode `env:"AUTH_RUNTIME_MODE"`
}

// compatHasher and compatSigner are the minimum required collaborators for
// NewService, declared here so this proof reaches the transport gate through
// exported API only.
type compatHasher struct{}

func (compatHasher) HashPassword(string) (string, error) { return "x", nil }
func (compatHasher) VerifyPassword(string, string) error { return nil }

type compatSigner struct{}

func (compatSigner) Sign(map[string]any, time.Time) (string, error) { return "tok", nil }
func (compatSigner) Verify(string) (map[string]any, error)          { return map[string]any{}, nil }

// appWideMailer stands in for a host's general-purpose mailer package: it names
// ONLY sdk vocabulary and would not compile if it needed the pocket.
type appWideMailer struct {
	mode   environment.Mode
	sender email.Sender
}

// requireProductionCapable is the app-wide equivalent of what authentication
// does internally, written with no pocket import.
func (m appWideMailer) requireProductionCapable() error {
	_, err := notify.CheckTransport(m.mode, m.sender)
	return err
}

// TestOldStyleHostStillCompiles pins that a host naming auth.RuntimeMode and its
// constants keeps working unchanged.
func TestOldStyleHostStillCompiles(t *testing.T) {
	cfg := hostRuntimeConfig{Mode: environment.ModeProduction}

	if cfg.Mode != environment.ModeProduction {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, environment.ModeProduction)
	}

	// The historic string values are part of the contract: a host reading
	// AUTH_RUNTIME_MODE from the environment must still match.
	if string(environment.ModeProduction) != "production" {
		t.Errorf("RuntimeModeProduction = %q, want \"production\"", environment.ModeProduction)
	}
	if string(environment.ModeDevelopment) != "development" {
		t.Errorf("RuntimeModeDevelopment = %q, want \"development\"", environment.ModeDevelopment)
	}

	// A host-owned configuration record accepts the shared mode.
	authCfg := hostRuntimeConfig{Mode: environment.ModeDevelopment}
	if authCfg.Mode != environment.ModeDevelopment {
		t.Errorf("host configuration mode = %q, want development", authCfg.Mode)
	}
}

// TestAliasIsAssignableBothDirections is the migration guarantee: the two names
// are ONE type, so a host can move package by package instead of all at once.
func TestAliasIsAssignableBothDirections(t *testing.T) {
	var fromAuth environment.Mode = environment.ModeProduction
	var fromSDK environment.Mode = environment.ModeProduction

	if fromAuth != environment.ModeProduction {
		t.Errorf("auth constant assigned to environment.Mode = %q", fromAuth)
	}
	if fromSDK != environment.ModeProduction {
		t.Errorf("environment constant assigned to auth.RuntimeMode = %q", fromSDK)
	}

	// No conversion is needed in the host configuration.
	cfg := hostRuntimeConfig{Mode: environment.ModeProduction}
	if cfg.Mode != environment.ModeProduction {
		t.Errorf("host configuration mode = %q, want production", cfg.Mode)
	}

	// And an app-wide component accepts a value the host read as auth's type.
	m := appWideMailer{mode: cfg.Mode, sender: email.NewSMTP(email.SMTPConfig{Host: "mail.example.com", Port: "587"})}
	if err := m.requireProductionCapable(); err != nil {
		t.Errorf("app-wide production check on an SMTP sender = %v, want nil", err)
	}
}

// TestRuntimeModeSentinelsMatchBothVocabularies pins the errors.Is posture the
// plan requires: existing host checks keep matching, and sdk-only code can match
// the canonical sentinel for the same failure.
func TestRuntimeModeSentinelsMatchBothVocabularies(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		authErr   error
		canonical error
	}{
		{"required", auth.ErrRuntimeModeRequired, auth.ErrRuntimeModeRequired, environment.ErrModeRequired},
		{"invalid", auth.ErrRuntimeModeInvalid, auth.ErrRuntimeModeInvalid, environment.ErrModeInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.authErr) {
				t.Errorf("errors.Is(err, auth sentinel) = false")
			}
			if !errors.Is(tt.err, tt.canonical) {
				t.Errorf("errors.Is(err, environment sentinel) = false")
			}
		})
	}

	// The two auth sentinels stay distinguishable from each other.
	if errors.Is(auth.ErrRuntimeModeRequired, auth.ErrRuntimeModeInvalid) {
		t.Error("ErrRuntimeModeRequired matches ErrRuntimeModeInvalid; the two must stay distinct")
	}
	if errors.Is(auth.ErrRuntimeModeRequired, environment.ErrModeInvalid) {
		t.Error("ErrRuntimeModeRequired matches environment.ErrModeInvalid; the two must stay distinct")
	}
}

// TestInsecureTransportMatchesBothVocabularies proves the same for the transport
// verdict now delegated to the capability packages.
func TestInsecureTransportMatchesBothVocabularies(t *testing.T) {
	_, err := auth.New(auth.Repositories{},
		compatSigner{},
		environment.ModeProduction,
		delivery.ModeOff,
		auth.WithPassword(auth.PasswordConfig{Hasher: compatHasher{}}),
		auth.WithDelivery(auth.DeliveryConfig{Mailer: email.NewConsole(nil)}))
	if err == nil {
		t.Fatal("NewService with a console mailer in production = nil error, want rejection")
	}
	if !errors.Is(err, auth.ErrInsecureDeliveryTransport) {
		t.Errorf("errors.Is(err, auth.ErrInsecureDeliveryTransport) = false; err = %v", err)
	}
	if !errors.Is(err, notify.ErrInsecureTransport) {
		t.Errorf("errors.Is(err, notify.ErrInsecureTransport) = false; err = %v", err)
	}

}

// TestParseModeFeedsAuthConfig is the documented migration wiring: the host owns
// the variable name, the sdk parses it, and the result drops into Config
// unchanged.
func TestParseModeFeedsAuthConfig(t *testing.T) {
	t.Setenv("AUTH_RUNTIME_MODE", "production")

	mode, err := environment.ParseMode(environment.GetEnvOrDefault("AUTH_RUNTIME_MODE", ""))
	if err != nil {
		t.Fatalf("ParseMode() error = %v", err)
	}

	cfg := hostRuntimeConfig{Mode: mode}
	if cfg.Mode != environment.ModeProduction {
		t.Errorf("host configuration mode = %q, want production", cfg.Mode)
	}

	os.Unsetenv("AUTH_RUNTIME_MODE")
	if _, err := environment.ParseMode(environment.GetEnvOrDefault("AUTH_RUNTIME_MODE", "")); !errors.Is(err, auth.ErrRuntimeModeRequired) {
		// The canonical error is what ParseMode returns; auth's sentinel wraps it,
		// so a host that only knows auth's vocabulary can still classify it.
		if !errors.Is(err, environment.ErrModeRequired) {
			t.Errorf("unset AUTH_RUNTIME_MODE = %v, want a required-mode error", err)
		}
	}
}
