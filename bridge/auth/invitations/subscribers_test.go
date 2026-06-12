package invitations

import (
	"context"
	"log/slog"
	"testing"
)

func TestIsSafeRelativePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/d/d_campaign_01", true},
		{"/", true},
		{"", false},                      // empty falls back to origin root
		{"dashboard", false},             // no leading slash
		{"//evil.example", false},        // scheme-relative URL
		{"/path?next=https://ok", false}, // conservative: any embedded "://" is rejected
		{"https://evil.example", false},  // absolute URL
		{"/redirect/https://x", false},   // embedded scheme separator
	}
	for _, c := range cases {
		if got := isSafeRelativePath(c.path); got != c.want {
			t.Errorf("isSafeRelativePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestOpenLinkForRejectsUnsafePaths(t *testing.T) {
	const origin = "https://app.example.com"

	resolve := func(path string) *Subscribers {
		return NewSubscribers(nil, nil, slog.Default(),
			WithDestinationPathResolver(func(context.Context, string, string) string { return path }),
		)
	}

	if got := resolve("/d/abc").openLinkFor(context.Background(), origin, "dashboard", "abc"); got != origin+"/d/abc" {
		t.Errorf("safe path: got %q", got)
	}
	if got := resolve("//evil.example").openLinkFor(context.Background(), origin, "dashboard", "abc"); got != origin {
		t.Errorf("unsafe path must fall back to origin, got %q", got)
	}

	// No resolver configured → origin root.
	noResolver := NewSubscribers(nil, nil, slog.Default())
	if got := noResolver.openLinkFor(context.Background(), origin, "dashboard", "abc"); got != origin {
		t.Errorf("nil resolver must fall back to origin, got %q", got)
	}
}

func TestValidateRedirectURL(t *testing.T) {
	legacy := New(slog.Default(), nil, nil, nil, nil)
	strict := New(slog.Default(), nil, nil, nil, nil,
		WithAllowedFrontends([]string{"https://app.example.com"}))

	t.Run("legacy mode passes anything through", func(t *testing.T) {
		for _, raw := range []string{"", "https://anything.example", "not-even-a-url"} {
			if err := legacy.validateRedirectURL(raw); err != nil {
				t.Errorf("legacy mode rejected %q: %v", raw, err)
			}
		}
	})

	t.Run("strict mode requires an allow-listed origin", func(t *testing.T) {
		if err := strict.validateRedirectURL("https://app.example.com"); err != nil {
			t.Errorf("allow-listed origin rejected: %v", err)
		}
		if err := strict.validateRedirectURL(""); err == nil {
			t.Error("empty redirect_url must be rejected in strict mode")
		}
		if err := strict.validateRedirectURL("https://evil.example"); err == nil {
			t.Error("non-listed origin must be rejected in strict mode")
		}
	})
}
