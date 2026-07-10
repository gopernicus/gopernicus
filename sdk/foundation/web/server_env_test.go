package web_test

import (
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// These round-trip tests prove ServerConfig's env tags are live: environment.ParseEnvTags
// populates the struct that sdk/foundation/web declares. The dependency is test-only — sdk/foundation/web
// has no production import of sdk/foundation/environment.

func serverEnvKeys() []string {
	return []string{
		"SRVTEST_HOST", "SRVTEST_PORT", "SRVTEST_READ_TIMEOUT",
		"SRVTEST_WRITE_TIMEOUT", "SRVTEST_IDLE_TIMEOUT", "SRVTEST_SHUTDOWN_TIMEOUT",
	}
}

func clearServerEnv() {
	for _, k := range serverEnvKeys() {
		os.Unsetenv(k)
	}
}

func TestServerConfig_EnvSet(t *testing.T) {
	clearServerEnv()
	os.Setenv("SRVTEST_HOST", "0.0.0.0")
	os.Setenv("SRVTEST_PORT", "9090")
	os.Setenv("SRVTEST_READ_TIMEOUT", "5s")
	os.Setenv("SRVTEST_WRITE_TIMEOUT", "7s")
	os.Setenv("SRVTEST_IDLE_TIMEOUT", "60s")
	os.Setenv("SRVTEST_SHUTDOWN_TIMEOUT", "3s")
	defer clearServerEnv()

	var cfg web.ServerConfig
	if err := environment.ParseEnvTags("SRVTEST", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9090")
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout = %v, want 5s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 7*time.Second {
		t.Errorf("WriteTimeout = %v, want 7s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 3s", cfg.ShutdownTimeout)
	}
}

func TestServerConfig_DefaultFallback(t *testing.T) {
	clearServerEnv()

	var cfg web.ServerConfig
	if err := environment.ParseEnvTags("SRVTEST", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %v, want 15s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 15*time.Second {
		t.Errorf("WriteTimeout = %v, want 15s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestServerConfig_ExistingFieldWins(t *testing.T) {
	clearServerEnv()

	cfg := web.ServerConfig{Host: "preset.example", Port: "1234"}
	if err := environment.ParseEnvTags("SRVTEST", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "preset.example" {
		t.Errorf("Host = %q, want %q (existing non-zero field wins)", cfg.Host, "preset.example")
	}
	if cfg.Port != "1234" {
		t.Errorf("Port = %q, want %q (existing non-zero field wins)", cfg.Port, "1234")
	}
	// Untouched fields still take their defaults.
	if cfg.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %v, want 15s", cfg.ReadTimeout)
	}
}
