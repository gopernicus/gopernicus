package environment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPath(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := `# Database config
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

	// Clear any existing values.
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
		{"DB_NAME", "my_database"},    // double quotes stripped
		{"DB_PASS", "s3cret"},         // single quotes stripped
		{"APP_ENV", "production"},     // export prefix stripped
		{"CACHE_TTL", "300"},          // inline comment stripped
		{"EMPTY_VAL", ""},             // empty value preserved
	}

	for _, tt := range tests {
		got := os.Getenv(tt.key)
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}

	// Clean up.
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

	// The # inside quotes should NOT be treated as a comment.
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
