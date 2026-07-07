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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactDSN(tt.dsn); got != tt.want {
				t.Fatalf("RedactDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}
