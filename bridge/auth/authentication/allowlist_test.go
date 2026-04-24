package authentication

import (
	"testing"
)

func TestOriginMatcher_Canonicalization(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "https with path normalized",
			input: "HTTPS://Segovia.URL.com/reset",
			want:  "https://segovia.url.com:443",
		},
		{
			name:  "http default port",
			input: "http://example.com",
			want:  "http://example.com:80",
		},
		{
			name:  "https default port",
			input: "https://example.com",
			want:  "https://example.com:443",
		},
		{
			name:  "explicit port preserved",
			input: "https://example.com:8443",
			want:  "https://example.com:8443",
		},
		{
			name:  "host case normalized",
			input: "https://EXAMPLE.COM",
			want:  "https://example.com:443",
		},
		{
			name:  "scheme case normalized",
			input: "HTTPS://example.com",
			want:  "https://example.com:443",
		},
		{
			name:    "missing scheme",
			input:   "example.com",
			wantErr: true,
		},
		{
			name:    "missing host",
			input:   "https://",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			input:   "ftp://example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalizeOrigin(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("canonicalizeOrigin(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("canonicalizeOrigin(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("canonicalizeOrigin(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewOriginMatcher_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		origins []string
	}{
		{
			name:    "missing scheme",
			origins: []string{"example.com"},
		},
		{
			name:    "unsupported scheme",
			origins: []string{"ftp://example.com"},
		},
		{
			name:    "valid then invalid",
			origins: []string{"https://ok.com", "bad"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewOriginMatcher(tt.origins)
			if err == nil {
				t.Errorf("NewOriginMatcher(%v) expected error, got nil", tt.origins)
			}
		})
	}
}

func TestNewOriginMatcher_AcceptsValid(t *testing.T) {
	origins := []string{
		"https://segovia.url.com",
		"https://dashboards.url.com",
		"http://localhost:3000",
		"  https://trimmed.com  ",
		"",
	}

	m, err := NewOriginMatcher(origins)
	if err != nil {
		t.Fatalf("NewOriginMatcher unexpected error: %v", err)
	}

	if m.Empty() {
		t.Error("Expected non-empty matcher")
	}

	if got := len(m.Origins()); got != 4 {
		t.Errorf("Expected 4 origins, got %d", got)
	}
}

func TestOriginMatcher_Empty(t *testing.T) {
	m, err := NewOriginMatcher(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Empty() {
		t.Error("Expected Empty() to be true for nil input")
	}

	m2, err := NewOriginMatcher([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m2.Empty() {
		t.Error("Expected Empty() to be true for empty slice")
	}

	m3, err := NewOriginMatcher([]string{"", "  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m3.Empty() {
		t.Error("Expected Empty() to be true for whitespace-only entries")
	}
}

func TestOriginMatcher_Matches(t *testing.T) {
	m, err := NewOriginMatcher([]string{
		"https://segovia.url.com",
		"https://dashboards.url.com",
		"http://localhost:3000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "exact match",
			input: "https://segovia.url.com",
			want:  true,
		},
		{
			name:  "match with path",
			input: "https://segovia.url.com/reset-password?token=abc",
			want:  true,
		},
		{
			name:  "match case insensitive host",
			input: "https://SEGOVIA.URL.COM/path",
			want:  true,
		},
		{
			name:  "match localhost with port",
			input: "http://localhost:3000/callback",
			want:  true,
		},
		{
			name:  "different origin",
			input: "https://attacker.com",
			want:  false,
		},
		{
			name:  "different subdomain",
			input: "https://api.segovia.url.com",
			want:  false,
		},
		{
			name:  "different port",
			input: "http://localhost:3001",
			want:  false,
		},
		{
			name:  "different scheme",
			input: "http://segovia.url.com",
			want:  false,
		},
		{
			name:  "unparseable returns false",
			input: "not-a-url",
			want:  false,
		},
		{
			name:  "empty string returns false",
			input: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Matches(tt.input)
			if got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestOriginMatcher_MatchesEmpty(t *testing.T) {
	m, _ := NewOriginMatcher(nil)

	if m.Matches("https://anything.com") {
		t.Error("Empty matcher should never match")
	}
}

func TestOriginMatcher_OrderPreserved(t *testing.T) {
	origins := []string{
		"https://first.example.com",
		"https://second.example.com",
		"https://third.example.com",
	}

	m, err := NewOriginMatcher(origins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Call Origins() multiple times — order must be stable.
	for i := 0; i < 20; i++ {
		got := m.Origins()
		if len(got) != 3 {
			t.Fatalf("Origins() returned %d elements, want 3", len(got))
		}
		// First element must always be the first declared origin (canonicalized).
		if got[0] != "https://first.example.com:443" {
			t.Errorf("iteration %d: Origins()[0] = %q, want https://first.example.com:443", i, got[0])
		}
		if got[1] != "https://second.example.com:443" {
			t.Errorf("iteration %d: Origins()[1] = %q, want https://second.example.com:443", i, got[1])
		}
		if got[2] != "https://third.example.com:443" {
			t.Errorf("iteration %d: Origins()[2] = %q, want https://third.example.com:443", i, got[2])
		}
	}
}

func TestOriginMatcher_Default(t *testing.T) {
	t.Run("returns first origin", func(t *testing.T) {
		m, _ := NewOriginMatcher([]string{
			"https://primary.example.com",
			"https://secondary.example.com",
		})
		if got := m.Default(); got != "https://primary.example.com:443" {
			t.Errorf("Default() = %q, want https://primary.example.com:443", got)
		}
	})

	t.Run("returns empty for empty matcher", func(t *testing.T) {
		m, _ := NewOriginMatcher(nil)
		if got := m.Default(); got != "" {
			t.Errorf("Default() = %q, want empty string", got)
		}
	})
}

func TestOriginMatcher_DeduplicatesPreservingOrder(t *testing.T) {
	m, err := NewOriginMatcher([]string{
		"https://a.com",
		"https://b.com",
		"https://a.com", // duplicate
		"https://c.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := m.Origins()
	want := []string{"https://a.com:443", "https://b.com:443", "https://c.com:443"}
	if len(got) != len(want) {
		t.Fatalf("Origins() returned %d elements, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Origins()[%d] = %q, want %q", i, got[i], w)
		}
	}
}
