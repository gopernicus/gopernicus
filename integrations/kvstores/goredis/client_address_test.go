package goredis

import (
	"crypto/tls"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestHostAndPortResolveToOneAddress pins Config's three Host/Port cases and
// the two refusals. No server: options() is the resolution Open dials with.
func TestHostAndPortResolveToOneAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"zero config", Config{}, "localhost:6379"},
		{"port set", Config{Host: "cache.internal", Port: 25061}, "cache.internal:25061"},
		{"port read from host", Config{Host: "cache.internal:6380"}, "cache.internal:6380"},
		{"no port anywhere", Config{Host: "cache.internal"}, "cache.internal:6379"},
		{"ipv6 with a port", Config{Host: "::1", Port: 6380}, "[::1]:6380"},
		{"bracketed ipv6 with a port", Config{Host: "[::1]", Port: 6380}, "[::1]:6380"},
		{"bracketed ipv6 carrying its port", Config{Host: "[::1]:6380"}, "[::1]:6380"},
		{"surrounding space", Config{Host: " cache.internal ", Port: 6380}, "cache.internal:6380"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := tc.cfg.options()
			if err != nil {
				t.Fatalf("options() error = %v", err)
			}
			if opts.Addr != tc.want {
				t.Errorf("Addr = %q, want %q", opts.Addr, tc.want)
			}
			if !opts.ContextTimeoutEnabled {
				t.Error("ContextTimeoutEnabled = false, want true")
			}
		})
	}

	for _, tc := range []struct {
		name  string
		cfg   Config
		names []string
	}{
		{"port in both places", Config{Host: "cache.internal:6380", Port: 25061}, []string{"REDIS_HOST", "REDIS_PORT"}},
		{"port out of range", Config{Host: "cache.internal", Port: 70000}, []string{"REDIS_PORT"}},
		{"negative port", Config{Host: "cache.internal", Port: -1}, []string{"REDIS_PORT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.cfg.options()
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("options() error = %v, want sdk.ErrInvalidInput", err)
			}
			for _, name := range tc.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %s", err, name)
				}
			}
		})
	}
}

// TestURLWinsOverTheSeparateFields proves a URL supplies the address, the ACL
// user, the password, the database and TLS, that the fields saying otherwise
// are not read, and that the tuning fields still fill what the URL leaves out.
func TestURLWinsOverTheSeparateFields(t *testing.T) {
	opts, err := Config{
		URL:         "rediss://app:secret@managed.example:25061/2?dial_timeout=3s",
		Host:        "ignored.internal",
		Port:        6380,
		Username:    "ignored-user",
		Password:    "ignored-password",
		DB:          9,
		TLSEnabled:  false,
		DialTimeout: 9 * time.Second,
		PoolSize:    42,
	}.options()
	if err != nil {
		t.Fatalf("options() error = %v", err)
	}
	if opts.Addr != "managed.example:25061" {
		t.Errorf("Addr = %q, want the URL's", opts.Addr)
	}
	if opts.Username != "app" || opts.Password != "secret" {
		t.Errorf("Username, Password = %q, %q, want the URL's", opts.Username, opts.Password)
	}
	if opts.DB != 2 {
		t.Errorf("DB = %d, want the URL's 2", opts.DB)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.MinVersion < tls.VersionTLS12 {
		t.Errorf("TLSConfig = %+v, want TLS 1.2+ from the rediss scheme", opts.TLSConfig)
	}
	if opts.DialTimeout != 3*time.Second {
		t.Errorf("DialTimeout = %v, want the URL's 3s over the field's 9s", opts.DialTimeout)
	}
	if opts.PoolSize != 42 {
		t.Errorf("PoolSize = %d, want the field's 42 (the URL does not name it)", opts.PoolSize)
	}
	if opts.ReadTimeout != defaultReadTimeout {
		t.Errorf("ReadTimeout = %v, want the documented default", opts.ReadTimeout)
	}

	plain, err := Config{URL: "redis://localhost:6379"}.options()
	if err != nil {
		t.Fatalf("options() error = %v", err)
	}
	if plain.TLSConfig != nil {
		t.Error("TLSConfig set for a redis:// URL, want nil")
	}
	if plain.Username != "" || plain.Password != "" {
		t.Errorf("credentials = %q, %q, want none", plain.Username, plain.Password)
	}
}

// TestAURLFaultNeverRepeatsTheURL proves a rejected URL is reported by its
// reason alone: url.Parse quotes its whole input, and the input has a password.
func TestAURLFaultNeverRepeatsTheURL(t *testing.T) {
	for _, raw := range []string{
		"rediss://app:s3cr3t-pw@managed.example:25061/not-a-number",
		"https://app:s3cr3t-pw@managed.example:25061",
		"rediss://app:s3cr3t-pw@managed.example:25061?no_such_option=1",
		"rediss://app:s3cr3t-pw@managed.example:25061/%zz",
	} {
		_, err := Config{URL: raw}.options()
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%q: error = %v, want sdk.ErrInvalidInput", raw, err)
			continue
		}
		if !strings.Contains(err.Error(), "REDIS_URL") {
			t.Errorf("error %q does not name REDIS_URL", err)
		}
		if strings.Contains(err.Error(), "s3cr3t-pw") || strings.Contains(err.Error(), "managed.example") {
			t.Errorf("error repeats the URL: %q", err)
		}
	}
}
