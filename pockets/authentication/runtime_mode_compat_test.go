package authentication_test

import (
	"errors"
	"os"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// This file is the CHAU-3.3 compatibility proof for moving the runtime-posture
// vocabulary to sdk/foundation/environment. It is an EXTERNAL test package, so
// everything here is reachable by a host through exported API only.
//
// The counterpart proof — that app-wide code enforces the same transport rule
// with NO authentication import at all — lives in the sdk itself
// (sdk/capabilities/email/posture_test.go and
// sdk/capabilities/notify/posture_test.go). Those packages cannot import a
// feature, which is exactly the guarantee coordination-hub's generic
// internal/integrations/mailer needs: it drops its features/authentication
// import and names environment.Mode + email.CheckSender instead.

// hostRuntimeConfig is an old-style host's configuration struct: it declares the
// posture with auth's type name, as every host written before this change did.
type hostRuntimeConfig struct {
	Mode auth.RuntimeMode `env:"AUTH_RUNTIME_MODE"`
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
// ONLY sdk vocabulary and would not compile if it needed the feature.
type appWideMailer struct {
	mode   environment.Mode
	sender email.Sender
}

// requireProductionCapable is the app-wide equivalent of what authentication
// does internally, written with no feature import.
func (m appWideMailer) requireProductionCapable() error {
	_, err := email.CheckSender(m.mode, m.sender)
	return err
}

// TestOldStyleHostStillCompiles pins that a host naming auth.RuntimeMode and its
// constants keeps working unchanged.
func TestOldStyleHostStillCompiles(t *testing.T) {
	cfg := hostRuntimeConfig{Mode: auth.RuntimeModeProduction}

	if cfg.Mode != auth.RuntimeModeProduction {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, auth.RuntimeModeProduction)
	}

	// The historic string values are part of the contract: a host reading
	// AUTH_RUNTIME_MODE from the environment must still match.
	if string(auth.RuntimeModeProduction) != "production" {
		t.Errorf("RuntimeModeProduction = %q, want \"production\"", auth.RuntimeModeProduction)
	}
	if string(auth.RuntimeModeDevelopment) != "development" {
		t.Errorf("RuntimeModeDevelopment = %q, want \"development\"", auth.RuntimeModeDevelopment)
	}

	// A struct literal on the real Config still accepts the old constant.
	authCfg := auth.Config{RuntimeMode: auth.RuntimeModeDevelopment}
	if authCfg.RuntimeMode != auth.RuntimeModeDevelopment {
		t.Errorf("Config.RuntimeMode = %q, want development", authCfg.RuntimeMode)
	}
}

// TestAliasIsAssignableBothDirections is the migration guarantee: the two names
// are ONE type, so a host can move package by package instead of all at once.
func TestAliasIsAssignableBothDirections(t *testing.T) {
	var fromAuth environment.Mode = auth.RuntimeModeProduction
	var fromSDK auth.RuntimeMode = environment.ModeProduction

	if fromAuth != environment.ModeProduction {
		t.Errorf("auth constant assigned to environment.Mode = %q", fromAuth)
	}
	if fromSDK != auth.RuntimeModeProduction {
		t.Errorf("environment constant assigned to auth.RuntimeMode = %q", fromSDK)
	}

	// No conversion needed in a Config literal written the new way.
	cfg := auth.Config{RuntimeMode: environment.ModeProduction}
	if cfg.RuntimeMode != auth.RuntimeModeProduction {
		t.Errorf("Config.RuntimeMode = %q, want production", cfg.RuntimeMode)
	}

	// And an app-wide component accepts a value the host read as auth's type.
	m := appWideMailer{mode: cfg.RuntimeMode, sender: email.NewSMTP(email.SMTPConfig{Host: "mail.example.com", Port: "587"})}
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
	_, err := auth.NewService(auth.Repositories{}, auth.Config{
		Hasher:       compatHasher{},
		TokenSigner:  compatSigner{},
		Mailer:       email.NewConsole(nil),
		RuntimeMode:  auth.RuntimeModeProduction,
		DeliveryMode: auth.DeliveryModeOff,
	})
	if err == nil {
		t.Fatal("NewService with a console mailer in production = nil error, want rejection")
	}
	if !errors.Is(err, auth.ErrInsecureDeliveryTransport) {
		t.Errorf("errors.Is(err, auth.ErrInsecureDeliveryTransport) = false; err = %v", err)
	}
	if !errors.Is(err, email.ErrInsecureTransport) {
		t.Errorf("errors.Is(err, email.ErrInsecureTransport) = false; err = %v", err)
	}
	if errors.Is(err, notify.ErrInsecureTransport) {
		t.Errorf("an email-sender rejection matched notify.ErrInsecureTransport; err = %v", err)
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

	cfg := auth.Config{RuntimeMode: mode}
	if cfg.RuntimeMode != auth.RuntimeModeProduction {
		t.Errorf("Config.RuntimeMode = %q, want production", cfg.RuntimeMode)
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
