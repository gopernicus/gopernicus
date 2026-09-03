package environment

import (
	"os"
	"strings"
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

// nestedChild is recursed into as an untagged struct field: its own keys apply
// under the parent's namespace with no prefix of their own.
type nestedChild struct {
	Name    string        `env:"CHILD_NAME" default:"child"`
	Timeout time.Duration `env:"CHILD_TIMEOUT" default:"7s"`
}

type nestedParent struct {
	Top   string `env:"TOP" default:"top"`
	Child nestedChild
}

// serverConfigMirror reproduces the env/default tags of
// sdk/foundation/web.ServerConfig exactly. web must not import environment and
// environment must not import web (G12b), so the parse of those tags is proven
// here over a copy; web's own TestServerConfig_EnvironmentTags reflects over
// the real struct and fails when the two drift apart.
type serverConfigMirror struct {
	Host              string        `env:"HOST" default:"localhost"`
	Port              string        `env:"PORT" default:"8080"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT" default:"15s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT" default:"15s"`
	IdleTimeout       time.Duration `env:"IDLE_TIMEOUT" default:"120s"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT" default:"10s"`
	TrustedProxyCount int           `env:"TRUSTED_PROXY_COUNT"`
	PublicBaseURL     string        `env:"PUBLIC_BASE_URL"`
}

func TestParseEnvTags_NestedStructFromEnv(t *testing.T) {
	t.Setenv("NEST_TOP", "from-env")
	t.Setenv("NEST_CHILD_NAME", "nested-env")

	var cfg nestedParent
	if err := ParseEnvTags("NEST", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Top != "from-env" {
		t.Errorf("Top = %q, want %q", cfg.Top, "from-env")
	}
	if cfg.Child.Name != "nested-env" {
		t.Errorf("Child.Name = %q, want %q", cfg.Child.Name, "nested-env")
	}
	if cfg.Child.Timeout != 7*time.Second {
		t.Errorf("Child.Timeout = %v, want 7s (nested default)", cfg.Child.Timeout)
	}
}

func TestParseEnvTags_NestedStructKeepsPreSeeded(t *testing.T) {
	cfg := nestedParent{Child: nestedChild{Name: "seeded"}}
	if err := ParseEnvTags("NESTSEED", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Child.Name != "seeded" {
		t.Errorf("Child.Name = %q, want %q (pre-seeded nested field)", cfg.Child.Name, "seeded")
	}
	if cfg.Child.Timeout != 7*time.Second {
		t.Errorf("Child.Timeout = %v, want 7s (nested default)", cfg.Child.Timeout)
	}
}

func TestParseEnvTags_NestedRequiredNamesNamespacedKey(t *testing.T) {
	type child struct {
		Token string `env:"CHILD_TOKEN" required:"true"`
	}
	type parent struct {
		Child child
	}

	var cfg parent
	err := ParseEnvTags("NESTREQ", &cfg)
	if err == nil {
		t.Fatal("expected error for required field inside a nested struct")
	}
	if !strings.Contains(err.Error(), "NESTREQ_CHILD_TOKEN") {
		t.Errorf("error = %q, want it to name NESTREQ_CHILD_TOKEN", err)
	}
}

func TestParseEnvTags_TaggedStructFieldIsUnsupported(t *testing.T) {
	type bad struct {
		Child nestedChild `env:"CHILD"`
	}
	t.Setenv("TAGSTRUCT_CHILD", "anything")

	var cfg bad
	if err := ParseEnvTags("TAGSTRUCT", &cfg); err == nil {
		t.Fatal("expected error for a struct field carrying an env tag")
	}
}

func TestParseEnvTags_PointerToStructSkipped(t *testing.T) {
	type parent struct {
		Child *nestedChild
	}
	t.Setenv("PTR_CHILD_NAME", "ignored")

	var cfg parent
	if err := ParseEnvTags("PTR", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Child != nil {
		t.Errorf("Child = %+v, want nil (pointer-to-struct is not followed)", cfg.Child)
	}
}

func TestParseEnvTags_TimeTimeFieldIsNoOp(t *testing.T) {
	type parent struct {
		Name    string `env:"NAME" default:"named"`
		StartAt time.Time
	}

	var cfg parent
	if err := ParseEnvTags("TIME", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Name != "named" {
		t.Errorf("Name = %q, want %q", cfg.Name, "named")
	}
	if !cfg.StartAt.IsZero() {
		t.Errorf("StartAt = %v, want zero (time.Time recurses to a no-op)", cfg.StartAt)
	}
}

func TestParseEnvTags_InterfaceFieldSkipped(t *testing.T) {
	type parent struct {
		Name   string `env:"NAME" default:"named"`
		Logger any
	}

	var cfg parent
	if err := ParseEnvTags("IFACE", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Name != "named" {
		t.Errorf("Name = %q, want %q", cfg.Name, "named")
	}
	if cfg.Logger != nil {
		t.Errorf("Logger = %v, want nil", cfg.Logger)
	}
}

func TestParseEnvTags_EmptyValueTakesDefault(t *testing.T) {
	t.Setenv("EMPTY_HOST", "")
	t.Setenv("EMPTY_PORT", "")
	t.Setenv("EMPTY_DEBUG", "")
	t.Setenv("EMPTY_ORIGINS", "")
	t.Setenv("EMPTY_REQUIRED", "present")

	var cfg tagsTestEnvironment
	if err := ParseEnvTags("EMPTY", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q (empty value takes the default)", cfg.Host, "localhost")
	}
	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want %d", cfg.Port, 3000)
	}
	if cfg.Debug != false {
		t.Errorf("Debug = %v, want false", cfg.Debug)
	}
	if len(cfg.Origins) != 1 || cfg.Origins[0] != "*" {
		t.Errorf("Origins = %v, want [*]", cfg.Origins)
	}
}

func TestParseEnvTags_EmptyValueKeepsPreSeeded(t *testing.T) {
	t.Setenv("EMPTYSEED_HOST", "")
	t.Setenv("EMPTYSEED_REQUIRED", "present")

	cfg := tagsTestEnvironment{Host: "already-set"}
	if err := ParseEnvTags("EMPTYSEED", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.Host != "already-set" {
		t.Errorf("Host = %q, want %q (empty value never overwrites)", cfg.Host, "already-set")
	}
}

func TestParseEnvTags_EmptyValueFailsRequired(t *testing.T) {
	t.Setenv("EMPTYREQ_REQUIRED", "")

	var cfg tagsTestEnvironment
	if err := ParseEnvTags("EMPTYREQ", &cfg); err == nil {
		t.Fatal("expected error for a required field set to an empty value")
	}
}

func TestParseEnvTags_EmptyValueOnUndefaultedFieldLeavesZero(t *testing.T) {
	type cfgType struct {
		Base  string `env:"BASE"`
		Count int    `env:"COUNT"`
	}
	t.Setenv("BLANK_BASE", "")
	t.Setenv("BLANK_COUNT", "")

	var cfg cfgType
	if err := ParseEnvTags("BLANK", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.Base != "" {
		t.Errorf("Base = %q, want empty", cfg.Base)
	}
	if cfg.Count != 0 {
		t.Errorf("Count = %d, want 0", cfg.Count)
	}
}

func TestParseEnvTags_ServerConfigMirror(t *testing.T) {
	t.Setenv("SRV_TRUSTED_PROXY_COUNT", "2")
	t.Setenv("SRV_PUBLIC_BASE_URL", "https://app.example.com")

	cfg := serverConfigMirror{Port: "8082"}
	if err := ParseEnvTags("SRV", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}

	if cfg.TrustedProxyCount != 2 {
		t.Errorf("TrustedProxyCount = %d, want 2", cfg.TrustedProxyCount)
	}
	if cfg.PublicBaseURL != "https://app.example.com" {
		t.Errorf("PublicBaseURL = %q, want %q", cfg.PublicBaseURL, "https://app.example.com")
	}
	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != "8082" {
		t.Errorf("Port = %q, want %q (pre-seeded port survives)", cfg.Port, "8082")
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}
