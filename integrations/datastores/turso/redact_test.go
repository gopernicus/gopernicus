package turso

import (
	"strings"
	"testing"
)

// TestRedactDSN covers the authToken-masking case, the userinfo-password case,
// both together, the plain-hostname passthrough, and the unparseable-input
// fallback — hermetically, no database required.
func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "libsql url with authToken masks it",
			dsn:  "libsql://db-org.turso.io?authToken=eyJhbGci.secret.token",
			want: "libsql://db-org.turso.io?authToken=REDACTED",
		},
		{
			name: "authToken alongside another query arg keeps the other arg",
			dsn:  "libsql://db-org.turso.io?tls=1&authToken=secrettoken",
			want: "libsql://db-org.turso.io?authToken=REDACTED&tls=1",
		},
		{
			name: "url with userinfo password masks it",
			dsn:  "libsql://user:secret@db-org.turso.io",
			want: "libsql://user:REDACTED@db-org.turso.io",
		},
		{
			name: "both password and token are masked",
			dsn:  "libsql://user:secret@db-org.turso.io?authToken=secrettoken",
			want: "libsql://user:REDACTED@db-org.turso.io?authToken=REDACTED",
		},
		{
			name: "plain hostname is unchanged",
			dsn:  "libsql://db-org.turso.io",
			want: "libsql://db-org.turso.io",
		},
		{
			name: "malformed input is fully redacted",
			dsn:  "libsql://user:pass@%zz",
			want: "REDACTED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactDSN(tt.dsn); got != tt.want {
				t.Fatalf("RedactDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

// TestConfigRedacted proves Config.Redacted masks the auth token Open would
// append, so a host can log the connection target without leaking it.
func TestConfigRedacted(t *testing.T) {
	cfg := Config{URL: "libsql://db-org.turso.io", AuthToken: "supersecret"}
	got := cfg.Redacted()
	if want := "libsql://db-org.turso.io?authToken=REDACTED"; got != want {
		t.Fatalf("Redacted() = %q, want %q", got, want)
	}
	if strings.Contains(got, "supersecret") {
		t.Fatalf("Redacted() = %q leaked the auth token", got)
	}
}
