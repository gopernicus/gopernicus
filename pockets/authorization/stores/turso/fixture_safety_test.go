package turso

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const disposableTursoURLEnv = "AUTHORIZATION_TURSO_DISPOSABLE_URL"

// A separate exact URL is an explicit opt-in to this suite's destructive reset;
// ambient application connection settings alone never authorize it.
func validateDisposableTursoURL(target, disposable string) error {
	if target == "" || disposable == "" {
		return errors.New("destructive Turso tests require AUTHORIZATION_TURSO_DISPOSABLE_URL matching TURSO_DATABASE_URL")
	}
	u, err := url.Parse(target)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(target, "#") || u.Opaque != "" {
		return errors.New("disposable Turso URL must be a valid URL without credentials, query or fragment")
	}
	switch u.Scheme {
	case "file":
		if u.Host != "" || !filepath.IsAbs(u.Path) || filepath.Clean(u.Path) != u.Path || u.Path == string(filepath.Separator) {
			return errors.New("disposable Turso file URL must name an absolute local database path")
		}
	case "libsql", "https", "wss", "http", "ws":
		if u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
			return errors.New("disposable Turso URL must name one database host without a resource path")
		}
		if port := u.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return errors.New("disposable Turso URL has an invalid port")
			}
		} else if strings.HasSuffix(u.Host, ":") {
			return errors.New("disposable Turso URL has an empty port")
		}
		if u.Scheme == "http" || u.Scheme == "ws" {
			host := u.Hostname()
			if host != "localhost" && !net.ParseIP(host).IsLoopback() {
				return errors.New("insecure disposable Turso URLs must use a loopback host")
			}
		}
	default:
		return errors.New("unsupported disposable Turso URL scheme")
	}
	if target != disposable {
		return errors.New("TURSO_DATABASE_URL does not exactly match AUTHORIZATION_TURSO_DISPOSABLE_URL; refusing destructive tests")
	}
	return nil
}

func TestDisposableTursoURL(t *testing.T) {
	const fixture = "libsql://authorization-disposable-test.turso.io"
	tests := []struct {
		name       string
		target     string
		disposable string
		valid      bool
	}{
		{"explicit fixture", fixture, fixture, true},
		{"https fixture", "https://authorization-disposable-test.turso.io", "https://authorization-disposable-test.turso.io", true},
		{"websocket fixture", "wss://authorization-disposable-test.turso.io", "wss://authorization-disposable-test.turso.io", true},
		{"local server", "http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"local ipv6 server", "ws://[::1]:8080", "ws://[::1]:8080", true},
		{"local file", "file:/tmp/authorization-disposable.db", "file:/tmp/authorization-disposable.db", true},
		{"empty", "", "", false},
		{"ambient application URL", "libsql://application.turso.io", "", false},
		{"mismatched host", "libsql://application.turso.io", fixture, false},
		{"mismatched protocol", "https://authorization-disposable-test.turso.io", fixture, false},
		{"mismatched port", "libsql://authorization-disposable-test.turso.io:443", fixture, false},
		{"normalized alias", fixture + "/", fixture, false},
		{"malformed allowlist", fixture, "not a URL", false},
	}
	for _, malformed := range []string{
		"not a URL", "libsql:", "libsql:database", "libsql://", "libsql://:443",
		"libsql://%zz", "libsql://db:invalid", "libsql://db:0", "libsql://db:65536", "libsql://db:", "libsql://db\n",
		"libsql://user:password@db", "libsql://db?authToken=secret", "libsql://db?",
		"libsql://db#fragment", "libsql://db#", "libsql://db/other-database", "ftp://db",
		"http://application.turso.io", "file:app.db", "file://other-host/tmp/app.db",
		"file:/tmp/../app.db", "file:/",
	} {
		tests = append(tests, struct {
			name       string
			target     string
			disposable string
			valid      bool
		}{"invalid exact match " + malformed, malformed, malformed, false})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDisposableTursoURL(tt.target, tt.disposable)
			if (err == nil) != tt.valid {
				t.Fatalf("valid = %t, want %t: %v", err == nil, tt.valid, err)
			}
		})
	}
}
