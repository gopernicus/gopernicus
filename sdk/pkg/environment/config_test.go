package environment

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPath(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := `# Database environment
DB_HOST=localhost
DB_PORT=5432
DB_NAME="my_database"
DB_PASS='s3cret'
export APP_ENV=production
CACHE_TTL=300 # seconds
EMPTY_VAL=
`
	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	keys := []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_PASS", "APP_ENV", "CACHE_TTL", "EMPTY_VAL"}
	for _, k := range keys {
		os.Unsetenv(k)
	}

	if err := LoadPath(envFile); err != nil {
		t.Fatalf("LoadPath: %v", err)
	}

	tests := []struct {
		key  string
		want string
	}{
		{"DB_HOST", "localhost"},
		{"DB_PORT", "5432"},
		{"DB_NAME", "my_database"}, // double quotes stripped
		{"DB_PASS", "s3cret"},      // single quotes stripped
		{"APP_ENV", "production"},  // export prefix stripped
		{"CACHE_TTL", "300"},       // inline comment stripped
		{"EMPTY_VAL", ""},          // empty value preserved
	}

	for _, tt := range tests {
		got := os.Getenv(tt.key)
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}

	for _, k := range keys {
		os.Unsetenv(k)
	}
}

func TestLoadPath_DoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	if err := os.WriteFile(envFile, []byte("MY_KEY=from_file\n"), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("MY_KEY", "already_set")
	defer os.Unsetenv("MY_KEY")

	if err := LoadPath(envFile); err != nil {
		t.Fatalf("LoadPath: %v", err)
	}

	if got := os.Getenv("MY_KEY"); got != "already_set" {
		t.Errorf("MY_KEY = %q, want %q (should not overwrite)", got, "already_set")
	}
}

func TestLoadPath_MissingFile(t *testing.T) {
	if err := LoadPath("/nonexistent/.env"); err != nil {
		t.Errorf("expected nil for missing file, got: %v", err)
	}
}

func TestLoadPath_CommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := `
# full line comment
   # indented comment

KEY=value
`
	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("KEY")
	if err := LoadPath(envFile); err != nil {
		t.Fatalf("LoadPath: %v", err)
	}

	if got := os.Getenv("KEY"); got != "value" {
		t.Errorf("KEY = %q, want %q", got, "value")
	}
	os.Unsetenv("KEY")
}

func TestLoadPath_InlineCommentInQuotedValue(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := `QUOTED="value # with hash"` + "\n"
	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("QUOTED")
	if err := LoadPath(envFile); err != nil {
		t.Fatalf("LoadPath: %v", err)
	}

	if got := os.Getenv("QUOTED"); got != "value # with hash" {
		t.Errorf("QUOTED = %q, want %q", got, "value # with hash")
	}
	os.Unsetenv("QUOTED")
}

func TestLoadPath_InlineCommentRule(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := "SPACED_EMPTY=   # note\n" +
		"TABBED_EMPTY=\t# note\n" +
		"CACHE_TTL=300 # seconds\n" +
		"TABBED_TTL=300\t# seconds\n" +
		"SLACK_CHANNEL=#alerts\n" +
		"COLOR=#ff0000\n" +
		"QUOTED_HASH=\"#keep\"\n" +
		"EMPTY_VAL=\n"

	if err := os.WriteFile(envFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		key  string
		want string
	}{
		{"SPACED_EMPTY", ""},         // whitespace then '#' is a comment
		{"TABBED_EMPTY", ""},         // tab then '#' is a comment
		{"CACHE_TTL", "300"},         // inline comment stripped
		{"TABBED_TTL", "300"},        // tab-separated inline comment stripped
		{"SLACK_CHANNEL", "#alerts"}, // leading '#' is part of the value
		{"COLOR", "#ff0000"},         // leading '#' is part of the value
		{"QUOTED_HASH", "#keep"},     // quoted values never lose a '#'
		{"EMPTY_VAL", ""},            // empty value preserved
	}

	for _, tt := range tests {
		os.Unsetenv(tt.key)
	}
	defer func() {
		for _, tt := range tests {
			os.Unsetenv(tt.key)
		}
	}()

	if err := LoadPath(envFile); err != nil {
		t.Fatalf("LoadPath: %v", err)
	}

	for _, tt := range tests {
		got, ok := os.LookupEnv(tt.key)
		if !ok {
			t.Errorf("%s not set, want set to %q", tt.key, tt.want)
			continue
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	os.Setenv("EXISTS", "yes")
	defer os.Unsetenv("EXISTS")

	if got := GetEnvOrDefault("EXISTS", "no"); got != "yes" {
		t.Errorf("got %q, want %q", got, "yes")
	}
	if got := GetEnvOrDefault("DOES_NOT_EXIST", "fallback"); got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestGetNamespaceEnvKey(t *testing.T) {
	if got := GetNamespaceEnvKey("APP", "PORT"); got != "APP_PORT" {
		t.Errorf("got %q, want %q", got, "APP_PORT")
	}
	if got := GetNamespaceEnvKey("", "PORT"); got != "PORT" {
		t.Errorf("got %q, want %q", got, "PORT")
	}
}

func TestLoadPath_QuotedValuesWithComments(t *testing.T) {
	const key = "GOPERNICUS_TEST_QUOTED"
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`"value" # note`, "value"},
		{`"value # inside" # outside`, "value # inside"},
		{`'value # inside' # outside`, "value # inside"},
		{"\"value\"\t# note", "value"},
		{`" value "   `, " value "},
		{`"" # empty`, ""},
		{`'literal\n${VALUE}' # no expansion`, `literal\n${VALUE}`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(key+"="+tc.input+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := LoadPath(path); err != nil {
				t.Fatal(err)
			}
			if got, exists := os.LookupEnv(key); !exists || got != tc.want {
				t.Fatalf("loaded (%q, %t), want (%q, true)", got, exists, tc.want)
			}
		})
	}
}

func TestLoadPath_ErrorsAreLocatedAndValueFree(t *testing.T) {
	const key = "GOPERNICUS_TEST_INVALID"
	const dummy = "DUMMY_VALUE_NOT_A_SECRET"
	for _, tc := range []struct {
		name, line string
		wantKey    bool
	}{
		{"missing equals", dummy, false},
		{"empty key", "=" + dummy, false},
		{"whitespace key", "BAD KEY=" + dummy, false},
		{"NUL key", "BAD\x00KEY=" + dummy, false},
		{"double quote", key + "=\"" + dummy, true},
		{"single quote", key + "='" + dummy, true},
		{"trailing text", key + "=\"value\" " + dummy, true},
		{"comment without space", key + "=\"value\"#" + dummy, true},
		{"NUL value", key + "=" + dummy + "\x00", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte("# header\n"+tc.line+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			err := LoadPath(path)
			if err == nil {
				t.Fatal("expected malformed assignment to fail")
			}
			if !strings.Contains(err.Error(), path+":2:") {
				t.Errorf("error lacks file/line: %v", err)
			}
			if tc.wantKey && !strings.Contains(err.Error(), key) {
				t.Errorf("error lacks key: %v", err)
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(cause.Error(), dummy) {
					t.Errorf("error contains rejected value: %v", cause)
				}
			}
			if _, exists := os.LookupEnv(key); exists {
				t.Error("failed assignment set its key")
			}
		})
	}
}

func TestLoadPath_ExistingEmptyValueAndPartialFailure(t *testing.T) {
	const existing = "GOPERNICUS_TEST_EXISTING"
	const applied = "GOPERNICUS_TEST_APPLIED"
	t.Setenv(existing, "")
	t.Setenv(applied, "")
	if err := os.Unsetenv(applied); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	content := existing + "=replacement\n" + applied + "=applied\nINVALID\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadPath(path); err == nil {
		t.Fatal("expected malformed third line to fail")
	}
	if value, exists := os.LookupEnv(existing); !exists || value != "" {
		t.Errorf("existing empty variable changed: (%q, %t)", value, exists)
	}
	if got := os.Getenv(applied); got != "applied" {
		t.Errorf("earlier assignment = %q, want applied", got)
	}
}

const (
	modeTestKey    = "GOPERNICUS_TEST_RUNTIME_MODE"
	modeTestLoaded = "GOPERNICUS_TEST_FROM_DOTENV"
)

// modeTestDir runs the test in a fresh working directory, optionally holding a
// .env, with the mode key set to mode (unset when mode is "-") and the
// file-loaded variable unset.
func modeTestDir(t *testing.T, mode, dotenv string) {
	t.Helper()
	t.Chdir(t.TempDir())
	if dotenv != "" {
		if err := os.WriteFile(".env", []byte(dotenv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(modeTestKey, mode)
	if mode == "-" {
		os.Unsetenv(modeTestKey)
	}
	t.Setenv(modeTestLoaded, "")
	os.Unsetenv(modeTestLoaded)
}

// captureDefaultLog routes slog.Default to a buffer for the test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestLoadEnvForMode(t *testing.T) {
	const loads = modeTestLoaded + "=yes\n"
	tests := []struct {
		name       string
		mode       string // "-" leaves the key unset
		dotenv     string
		want       Mode
		wantErr    error
		wantLoaded bool
		wantWarn   bool
	}{
		{name: "unset", mode: "-", dotenv: loads, wantErr: ErrModeRequired},
		{name: "empty", mode: "", dotenv: loads, wantErr: ErrModeRequired},
		{name: "unset while .env names the key", mode: "-", dotenv: modeTestKey + "=development\n" + loads, wantErr: ErrModeRequired},
		{name: "invalid", mode: "prod", dotenv: loads, wantErr: ErrModeInvalid},
		{name: "production skips .env", mode: "production", dotenv: modeTestKey + "=development\n" + loads, want: ModeProduction, wantWarn: true},
		{name: "production without .env", mode: "production", want: ModeProduction},
		{name: "development loads .env", mode: "development", dotenv: loads, want: ModeDevelopment, wantLoaded: true},
		{name: "development without .env", mode: "development", want: ModeDevelopment},
		{name: "development .env names the key", mode: "development", dotenv: loads + modeTestKey + "=development\n", wantErr: ErrModeFromFile},
		{name: "development .env names the key empty", mode: "development", dotenv: loads + "export " + modeTestKey + "=\n", wantErr: ErrModeFromFile},
		{name: "development .env comments the key", mode: "development", dotenv: "# " + modeTestKey + "=\n" + loads, want: ModeDevelopment, wantLoaded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modeTestDir(t, tt.mode, tt.dotenv)
			logs := captureDefaultLog(t)

			got, err := LoadEnvForMode(modeTestKey)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("LoadEnvForMode() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				for _, want := range []string{modeTestKey, "process environment", "systemd", "docker --env-file"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %q", err, want)
					}
				}
				if got != "" {
					t.Errorf("mode = %q on error, want zero", got)
				}
			} else if err != nil {
				t.Fatalf("LoadEnvForMode() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("mode = %q, want %q", got, tt.want)
			}
			if _, loaded := os.LookupEnv(modeTestLoaded); loaded != tt.wantLoaded {
				t.Errorf(".env loaded = %v, want %v", loaded, tt.wantLoaded)
			}
			warned := strings.Contains(logs.String(), ".env present, ignored in production")
			if warned != tt.wantWarn {
				t.Errorf("production WARN logged = %v, want %v; log %q", warned, tt.wantWarn, logs.String())
			}
			if tt.wantWarn && strings.Count(logs.String(), "level=WARN") != 1 {
				t.Errorf("want exactly one WARN, log %q", logs.String())
			}
		})
	}
}

func TestLoadEnvForMode_FromFileNamesLine(t *testing.T) {
	modeTestDir(t, "development", "A=1\n"+modeTestKey+"=production\n")
	_, err := LoadEnvForMode(modeTestKey)
	if !errors.Is(err, ErrModeFromFile) {
		t.Fatalf("error = %v, want ErrModeFromFile", err)
	}
	if !strings.Contains(err.Error(), ".env:2") {
		t.Errorf("error %q does not name the offending line", err)
	}
	if strings.Contains(err.Error(), "=production") {
		t.Errorf("error %q echoes the file's assignment", err)
	}
}

func TestLoadEnvForMode_DevelopmentReportsMalformedEnv(t *testing.T) {
	modeTestDir(t, "development", "not an assignment\n")
	if _, err := LoadEnvForMode(modeTestKey); err == nil || !strings.Contains(err.Error(), ".env:1") {
		t.Fatalf("error = %v, want the LoadPath parse error", err)
	}
}

func TestLoadEnvForMode_RequiresKey(t *testing.T) {
	if _, err := LoadEnvForMode(""); err == nil {
		t.Fatal("LoadEnvForMode(\"\") = nil error, want one")
	}
}
