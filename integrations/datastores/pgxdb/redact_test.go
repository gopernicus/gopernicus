package pgxdb

import "testing"

// TestRedactDSN covers the password-masking case, the no-password passthrough,
// and the unparseable-input fallback — hermetically, no database required.
func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "url with password masks it",
			dsn:  "postgres://user:secret@localhost:5432/mydb?sslmode=disable",
			want: "postgres://user:REDACTED@localhost:5432/mydb?sslmode=disable",
		},
		{
			name: "url without password is unchanged",
			dsn:  "postgres://user@localhost:5432/mydb?sslmode=disable",
			want: "postgres://user@localhost:5432/mydb?sslmode=disable",
		},
		{
			name: "malformed input is fully redacted",
			dsn:  "postgres://user:pass@%zz",
			want: "REDACTED",
		},
		{
			name: "query credentials and duplicates are masked",
			dsn:  "postgresql://user@localhost/db?password=one&password=two&sslpassword=three&sslmode=require",
			want: "postgresql://user@localhost/db?password=REDACTED&sslmode=require&sslpassword=REDACTED",
		},
		{
			name: "keyword DSN is opaque",
			dsn:  "host=localhost user=alice password=secret dbname=app",
			want: "REDACTED",
		},
		{
			name: "malformed query is opaque",
			dsn:  "postgres://localhost/db?password=secret%zz",
			want: "REDACTED",
		},
		{
			name: "fragment is opaque",
			dsn:  "postgres://localhost/db?password=secret#fragment",
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
