package logging_test

import (
	"os"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/logging"
)

// These round-trip tests prove Options's env tags are live: environment.ParseEnvTags
// populates the struct that sdk/logging declares. The dependency is test-only —
// sdk/logging has no production import of sdk/environment.

func clearLoggingEnv() {
	for _, k := range []string{"LOGTEST_LOG_LEVEL", "LOGTEST_LOG_FORMAT", "LOGTEST_LOG_OUTPUT"} {
		os.Unsetenv(k)
	}
}

func TestOptions_EnvSet(t *testing.T) {
	clearLoggingEnv()
	os.Setenv("LOGTEST_LOG_LEVEL", "DEBUG")
	os.Setenv("LOGTEST_LOG_FORMAT", "text")
	os.Setenv("LOGTEST_LOG_OUTPUT", "STDOUT")
	defer clearLoggingEnv()

	var opts logging.Options
	if err := environment.ParseEnvTags("LOGTEST", &opts); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if opts.Level != "DEBUG" {
		t.Errorf("Level = %q, want %q", opts.Level, "DEBUG")
	}
	if opts.Format != "text" {
		t.Errorf("Format = %q, want %q", opts.Format, "text")
	}
	if opts.Output != "STDOUT" {
		t.Errorf("Output = %q, want %q", opts.Output, "STDOUT")
	}
}

func TestOptions_DefaultFallback(t *testing.T) {
	clearLoggingEnv()

	var opts logging.Options
	if err := environment.ParseEnvTags("LOGTEST", &opts); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if opts.Level != "INFO" {
		t.Errorf("Level = %q, want %q", opts.Level, "INFO")
	}
	if opts.Format != "json" {
		t.Errorf("Format = %q, want %q", opts.Format, "json")
	}
	if opts.Output != "STDERR" {
		t.Errorf("Output = %q, want %q", opts.Output, "STDERR")
	}
}

func TestOptions_ExistingFieldWins(t *testing.T) {
	clearLoggingEnv()

	opts := logging.Options{Level: "WARN"}
	if err := environment.ParseEnvTags("LOGTEST", &opts); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if opts.Level != "WARN" {
		t.Errorf("Level = %q, want %q (existing non-zero field wins)", opts.Level, "WARN")
	}
	// Untouched fields still take their defaults.
	if opts.Format != "json" {
		t.Errorf("Format = %q, want %q", opts.Format, "json")
	}
	if opts.Output != "STDERR" {
		t.Errorf("Output = %q, want %q", opts.Output, "STDERR")
	}
}
