package environment

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestValidateMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    Mode
		wantErr error
	}{
		{"development", ModeDevelopment, nil},
		{"production", ModeProduction, nil},
		{"empty", Mode(""), ErrModeRequired},
		{"unknown", Mode("staging"), ErrModeInvalid},
		{"case variant", Mode("Production"), ErrModeInvalid},
		{"padded", Mode(" production "), ErrModeInvalid},
		{"prod abbreviation", Mode("prod"), ErrModeInvalid},
		{"dev abbreviation", Mode("dev"), ErrModeInvalid},
		{"test", Mode("test"), ErrModeInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMode(tt.mode)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateMode(%q) = %v, want nil", tt.mode, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateMode(%q) = %v, want errors.Is %v", tt.mode, err, tt.wantErr)
			}
		})
	}
}

func TestValidateModeInvalidCarriesValue(t *testing.T) {
	err := ValidateMode(Mode("staging"))
	if err == nil {
		t.Fatal("ValidateMode(staging) = nil, want error")
	}
	if !strings.Contains(err.Error(), `"staging"`) {
		t.Errorf("error %q does not carry the offending value", err.Error())
	}
}

func TestParseModeRoundTrip(t *testing.T) {
	for _, want := range []Mode{ModeDevelopment, ModeProduction} {
		got, err := ParseMode(string(want))
		if err != nil {
			t.Fatalf("ParseMode(%q) error = %v", want, err)
		}
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", want, got, want)
		}
		if got.String() != string(want) {
			t.Errorf("Mode.String() = %q, want %q", got.String(), want)
		}
	}
}

func TestParseModeRejects(t *testing.T) {
	tests := []struct {
		in      string
		wantErr error
	}{
		{"", ErrModeRequired},
		{"staging", ErrModeInvalid},
		{"PRODUCTION", ErrModeInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseMode(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseMode(%q) error = %v, want errors.Is %v", tt.in, err, tt.wantErr)
			}
			if got != "" {
				t.Errorf("ParseMode(%q) = %q, want zero Mode on failure", tt.in, got)
			}
		})
	}
}

// TestParseModeReadsNoEnvironment pins the contract that mode selection is the
// host's: no candidate variable name is consulted implicitly, so setting any of
// them cannot change what ParseMode returns for a given argument.
func TestParseModeReadsNoEnvironment(t *testing.T) {
	for _, key := range []string{"APP_MODE", "AUTH_RUNTIME_MODE", "ENV", "MODE", "GO_ENV", "ENVIRONMENT"} {
		t.Setenv(key, "production")
	}

	if _, err := ParseMode(""); !errors.Is(err, ErrModeRequired) {
		t.Fatalf("ParseMode(\"\") = %v with every candidate env var set to production; want ErrModeRequired", err)
	}

	got, err := ParseMode("development")
	if err != nil {
		t.Fatalf("ParseMode(development) error = %v", err)
	}
	if got != ModeDevelopment {
		t.Errorf("ParseMode(development) = %q; an env var overrode the argument", got)
	}
}

// TestParseModeFromHostVariable is the documented wiring: the host names the key,
// this package parses the value it already read.
func TestParseModeFromHostVariable(t *testing.T) {
	t.Setenv("SOME_HOST_CHOSEN_KEY", "production")

	mode, err := ParseMode(GetEnvOrDefault("SOME_HOST_CHOSEN_KEY", ""))
	if err != nil {
		t.Fatalf("ParseMode() error = %v", err)
	}
	if !mode.IsProduction() {
		t.Errorf("mode = %q, want production", mode)
	}

	os.Unsetenv("SOME_HOST_CHOSEN_KEY")
	if _, err := ParseMode(GetEnvOrDefault("SOME_HOST_CHOSEN_KEY", "")); !errors.Is(err, ErrModeRequired) {
		t.Errorf("unset key = %v, want ErrModeRequired (no silent development default)", err)
	}
}

func TestModeIsProduction(t *testing.T) {
	tests := []struct {
		mode Mode
		want bool
	}{
		{ModeProduction, true},
		{ModeDevelopment, false},
		{Mode(""), false},
		{Mode("staging"), false},
	}

	for _, tt := range tests {
		if got := tt.mode.IsProduction(); got != tt.want {
			t.Errorf("Mode(%q).IsProduction() = %v, want %v", tt.mode, got, tt.want)
		}
	}
}
