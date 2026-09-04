package web

import "testing"

// Origin is exercised from struct literals: sdk/foundation/web may not import
// sdk/foundation/environment (guard G12b, tests included), so the tag-driven
// path is proven in environment's own suite over a mirror struct.
func TestServerConfig_Origin(t *testing.T) {
	tests := []struct {
		name string
		cfg  ServerConfig
		want string
	}{
		{
			name: "public base url trailing slash trimmed",
			cfg:  ServerConfig{Host: "localhost", Port: "8080", PublicBaseURL: "https://app.example.com/"},
			want: "https://app.example.com",
		},
		{
			name: "public base url clean",
			cfg:  ServerConfig{Host: "localhost", Port: "8080", PublicBaseURL: "https://app.example.com"},
			want: "https://app.example.com",
		},
		{
			name: "public base url unset falls back to the listen address",
			cfg:  ServerConfig{Host: "localhost", Port: "8080"},
			want: "http://localhost:8080",
		},
		{
			name: "empty host reads as localhost",
			cfg:  ServerConfig{Port: "8080"},
			want: "http://localhost:8080",
		},
		{
			name: "bind-all host reads as localhost",
			cfg:  ServerConfig{Host: "0.0.0.0", Port: "9000"},
			want: "http://localhost:9000",
		},
		{
			name: "public base url wins over a bind-all host",
			cfg:  ServerConfig{Host: "0.0.0.0", Port: "9000", PublicBaseURL: "https://edge.example.com//"},
			want: "https://edge.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Origin(); got != tc.want {
				t.Errorf("Origin() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServerConfig_OriginDoesNotMutateConfig(t *testing.T) {
	cfg := ServerConfig{Host: "0.0.0.0", Port: "8080"}
	if got := cfg.Origin(); got != "http://localhost:8080" {
		t.Fatalf("Origin() = %q, want %q", got, "http://localhost:8080")
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q (Origin must not alter the listen address)", cfg.Host, "0.0.0.0")
	}
}
