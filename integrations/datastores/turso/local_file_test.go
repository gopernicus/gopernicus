package turso

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestConfigEnvTagsFollowTheDBNames pins the tag names to the pgxdb DB_*
// convention, read with and without a host namespace.
func TestConfigEnvTagsFollowTheDBNames(t *testing.T) {
	t.Setenv("AUTH_DB_URL", "file:auth.db")
	t.Setenv("AUTH_DB_AUTH_TOKEN", "token")
	t.Setenv("AUTH_DB_MAX_CONNS", "4")
	t.Setenv("AUTH_DB_MAX_IDLE_CONNS", "2")
	t.Setenv("AUTH_DB_MAX_CONN_LIFETIME", "1h")
	t.Setenv("AUTH_DB_CONNECT_TIMEOUT", "3s")
	t.Setenv("AUTH_DB_BUSY_TIMEOUT", "7s")
	t.Setenv("AUTH_DB_LOG_QUERIES", "true")
	var cfg Config
	if err := environment.ParseEnvTags("AUTH", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	want := Config{URL: "file:auth.db", AuthToken: "token", MaxOpenConns: 4, MaxIdleConns: 2,
		ConnMaxLifetime: time.Hour, ConnectTimeout: 3 * time.Second, BusyTimeout: 7 * time.Second, LogQueries: true}
	if cfg != want {
		t.Fatalf("namespaced config = %+v, want %+v", cfg, want)
	}

	t.Setenv("DB_URL", "libsql://example.turso.io")
	var plain Config
	if err := environment.ParseEnvTags("", &plain); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if plain.URL != "libsql://example.turso.io" || plain.LogQueries {
		t.Fatalf("plain config = %+v, want only DB_URL set", plain)
	}
}

// TestLocalFileDSN pins the profile: three _pragma parameters in order,
// parentheses percent-encoded, appended only when the URL names none of its
// own; the parent directory created; relative paths kept relative; a zero
// busy timeout never applied.
func TestLocalFileDSN(t *testing.T) {
	dir := t.TempDir()
	const pragmas = "?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29&_pragma=journal_mode%28WAL%29"

	t.Run("absolute path, nested directory created", func(t *testing.T) {
		path := filepath.Join(dir, "nested", "auth.db")
		dsn, err := localFileDSN("file:"+path, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if want := "file:" + path + pragmas; dsn != want {
			t.Errorf("dsn = %q, want %q", dsn, want)
		}
		if _, err := os.Stat(filepath.Dir(path)); err != nil {
			t.Errorf("parent directory not created: %v", err)
		}
	})
	t.Run("relative path stays relative", func(t *testing.T) {
		dsn, err := localFileDSN("file:relative/auth.db", 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if want := "file:relative/auth.db" + pragmas; dsn != want {
			t.Errorf("dsn = %q, want %q", dsn, want)
		}
		_ = os.RemoveAll("relative")
	})
	t.Run("zero busy timeout falls back to the default", func(t *testing.T) {
		dsn, err := localFileDSN("file:"+filepath.Join(dir, "zero.db"), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsn, "busy_timeout%285000%29") {
			t.Errorf("dsn = %q, want the default busy timeout", dsn)
		}
	})
	t.Run("an existing _pragma is an opt-out", func(t *testing.T) {
		raw := "file:" + filepath.Join(dir, "own.db") + "?_pragma=busy_timeout%281234%29"
		dsn, err := localFileDSN(raw, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if dsn != raw {
			t.Errorf("dsn = %q, want unchanged %q", dsn, raw)
		}
	})
}

func TestIsLocalFileAndDriverHint(t *testing.T) {
	if isLocalFile("libsql://example.turso.io") || !isLocalFile("file:x.db") {
		t.Fatal("isLocalFile misclassified a URL")
	}
	base := errors.New("no sqlite driver present. Please import sqlite or sqlite3 driver")
	hinted := localDriverHint("file:x.db", base)
	if !errors.Is(hinted, base) || !strings.Contains(hinted.Error(), localDriverPackage) {
		t.Fatalf("hint = %v, want the base error wrapped with the import path", hinted)
	}
	if got := localDriverHint("libsql://example.turso.io", base); got != base {
		t.Fatalf("a hosted URL got a hint: %v", got)
	}
	other := errors.New("connection refused")
	if got := localDriverHint("file:x.db", other); got != other {
		t.Fatalf("an unrelated error got a hint: %v", got)
	}
}
