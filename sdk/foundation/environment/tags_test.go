package environment

import (
	"os"
	"testing"
	"time"
)

type tagsTestEnvironment struct {
	Host     string        `env:"HOST" default:"localhost"`
	Port     int           `env:"PORT" default:"3000"`
	Big      int64         `env:"BIG" default:"9000000000"`
	Debug    bool          `env:"DEBUG" default:"false"`
	Rate     float64       `env:"RATE" default:"1.5"`
	Timeout  time.Duration `env:"TIMEOUT" default:"30s"`
	Origins  []string      `env:"ORIGINS" default:"*" separator:","`
	Required string        `env:"REQUIRED" required:"true"`
}

func clearEnv(keys ...string) {
	for _, k := range keys {
		os.Unsetenv(k)
	}
}

func TestParseEnvTags_Defaults(t *testing.T) {
	os.Setenv("TEST_REQUIRED", "present")
	defer os.Unsetenv("TEST_REQUIRED")

	clearEnv("TEST_HOST", "TEST_PORT", "TEST_BIG", "TEST_DEBUG", "TEST_RATE", "TEST_TIMEOUT", "TEST_ORIGINS")

	var cfg tagsTestEnvironment
	if err := ParseEnvTags("TEST", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want %d", cfg.Port, 3000)
	}
	if cfg.Big != 9000000000 {
		t.Errorf("Big = %d, want %d", cfg.Big, int64(9000000000))
	}
	if cfg.Debug != false {
		t.Errorf("Debug = %v, want false", cfg.Debug)
	}
	if cfg.Rate != 1.5 {
		t.Errorf("Rate = %f, want 1.5", cfg.Rate)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if len(cfg.Origins) != 1 || cfg.Origins[0] != "*" {
		t.Errorf("Origins = %v, want [*]", cfg.Origins)
	}
}

func TestParseEnvTags_EnvOverridesDefaults(t *testing.T) {
	os.Setenv("APP_HOST", "0.0.0.0")
	os.Setenv("APP_PORT", "8080")
	os.Setenv("APP_BIG", "12000000000")
	os.Setenv("APP_DEBUG", "true")
	os.Setenv("APP_RATE", "2.75")
	os.Setenv("APP_TIMEOUT", "5m")
	os.Setenv("APP_ORIGINS", "http://localhost, https://example.com")
	os.Setenv("APP_REQUIRED", "yes")
	defer clearEnv("APP_HOST", "APP_PORT", "APP_BIG", "APP_DEBUG", "APP_RATE", "APP_TIMEOUT", "APP_ORIGINS", "APP_REQUIRED")

	var cfg tagsTestEnvironment
	if err := ParseEnvTags("APP", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
	if cfg.Big != 12000000000 {
		t.Errorf("Big = %d, want %d", cfg.Big, int64(12000000000))
	}
	if cfg.Debug != true {
		t.Errorf("Debug = %v, want true", cfg.Debug)
	}
	if cfg.Rate != 2.75 {
		t.Errorf("Rate = %f, want 2.75", cfg.Rate)
	}
	if cfg.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", cfg.Timeout)
	}
	if len(cfg.Origins) != 2 || cfg.Origins[0] != "http://localhost" || cfg.Origins[1] != "https://example.com" {
		t.Errorf("Origins = %v, want [http://localhost https://example.com]", cfg.Origins)
	}
}

func TestParseEnvTags_Required(t *testing.T) {
	clearEnv("MISS_REQUIRED")

	var cfg tagsTestEnvironment
	if err := ParseEnvTags("MISS", &cfg); err == nil {
		t.Fatal("expected error for missing required field")
	}
}

func TestParseEnvTags_NonZeroFieldPreserved(t *testing.T) {
	// A field that already holds a non-zero value and has no env var set keeps
	// its value; the default tag does not overwrite it.
	clearEnv("PRE_HOST", "PRE_REQUIRED")
	os.Setenv("PRE_REQUIRED", "present")
	defer os.Unsetenv("PRE_REQUIRED")

	cfg := tagsTestEnvironment{Host: "already-set"}
	if err := ParseEnvTags("PRE", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "already-set" {
		t.Errorf("Host = %q, want %q (should preserve non-zero)", cfg.Host, "already-set")
	}
}

func TestParseEnvTags_EnvBeatsNonZeroField(t *testing.T) {
	os.Setenv("BEAT_HOST", "from-env")
	os.Setenv("BEAT_REQUIRED", "present")
	defer clearEnv("BEAT_HOST", "BEAT_REQUIRED")

	cfg := tagsTestEnvironment{Host: "already-set"}
	if err := ParseEnvTags("BEAT", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "from-env" {
		t.Errorf("Host = %q, want %q (env beats non-zero field)", cfg.Host, "from-env")
	}
}

func TestParseEnvTags_EmptyNamespace(t *testing.T) {
	os.Setenv("HOST", "noprefix")
	os.Setenv("REQUIRED", "present")
	defer clearEnv("HOST", "REQUIRED")

	var cfg tagsTestEnvironment
	if err := ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "noprefix" {
		t.Errorf("Host = %q, want %q", cfg.Host, "noprefix")
	}
}

func TestParseEnvTags_NotPointer(t *testing.T) {
	var cfg tagsTestEnvironment
	if err := ParseEnvTags("X", cfg); err == nil {
		t.Fatal("expected error for non-pointer")
	}
}

func TestParseEnvTags_PointerToNonStruct(t *testing.T) {
	n := 0
	if err := ParseEnvTags("X", &n); err == nil {
		t.Fatal("expected error for pointer to non-struct")
	}
}

func TestParseEnvTags_UnsupportedKind(t *testing.T) {
	type bad struct {
		Ratio complex128 `env:"RATIO"`
	}
	os.Setenv("BAD_RATIO", "1")
	defer os.Unsetenv("BAD_RATIO")

	var cfg bad
	if err := ParseEnvTags("BAD", &cfg); err == nil {
		t.Fatal("expected error for unsupported field kind")
	}
}

func TestParseEnvTags_UnsupportedSliceElem(t *testing.T) {
	type bad struct {
		Ports []int `env:"PORTS"`
	}
	os.Setenv("BAD_PORTS", "1,2,3")
	defer os.Unsetenv("BAD_PORTS")

	var cfg bad
	if err := ParseEnvTags("BAD", &cfg); err == nil {
		t.Fatal("expected error for unsupported slice element type")
	}
}

func TestParseEnvTags_UntaggedFieldsIgnored(t *testing.T) {
	type mixed struct {
		Tagged   string `env:"TAGGED" default:"set"`
		Untagged string
	}
	clearEnv("MIX_TAGGED")

	var cfg mixed
	if err := ParseEnvTags("MIX", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Tagged != "set" {
		t.Errorf("Tagged = %q, want %q", cfg.Tagged, "set")
	}
	if cfg.Untagged != "" {
		t.Errorf("Untagged = %q, want empty", cfg.Untagged)
	}
}
