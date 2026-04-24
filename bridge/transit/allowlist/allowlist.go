package allowlist

import (
	"fmt"
	"net/url"
	"strings"
)

// Matcher validates candidate URLs against a fixed allow-list of origins.
// An origin is scheme + host + port. Paths and queries on candidate URLs are
// ignored; only the origin must match.
type Matcher struct {
	allowed map[string]struct{}
	order   []string // preserves declaration order for Default() selection
}

// New parses the allow-list. Rejects entries that don't parse or
// are missing scheme/host. Normalizes: lowercase host, default port by scheme.
// Insertion order is preserved for deterministic Default() selection.
func New(origins []string) (*Matcher, error) {
	m := &Matcher{
		allowed: make(map[string]struct{}, len(origins)),
		order:   make([]string, 0, len(origins)),
	}
	for _, raw := range origins {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		canon, err := canonicalizeOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed frontend %q: %w", raw, err)
		}
		if _, seen := m.allowed[canon]; !seen {
			m.allowed[canon] = struct{}{}
			m.order = append(m.order, canon)
		}
	}
	return m, nil
}

// Empty reports whether no origins are configured. Callers use this to decide
// whether to run in strict mode (validate) or legacy mode (skip validation).
func (m *Matcher) Empty() bool {
	return len(m.allowed) == 0
}

// Matches reports whether rawURL's origin is in the allow-list. Returns false
// (not an error) for unparseable input.
func (m *Matcher) Matches(rawURL string) bool {
	canon, err := canonicalizeOrigin(rawURL)
	if err != nil {
		return false
	}
	_, ok := m.allowed[canon]
	return ok
}

// Origins returns a copy of the allowed origins in declaration order.
// Useful for deriving exact redirect URIs from origins + known paths.
func (m *Matcher) Origins() []string {
	out := make([]string, len(m.order))
	copy(out, m.order)
	return out
}

// Default returns the first configured origin, or empty string if none.
// Use this as a fallback for email links and error redirects when the
// request doesn't specify an origin.
func (m *Matcher) Default() string {
	if len(m.order) == 0 {
		return ""
	}
	return m.order[0]
}

func canonicalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("missing scheme or host")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return "", fmt.Errorf("unsupported scheme %q", scheme)
		}
	}
	return fmt.Sprintf("%s://%s:%s", scheme, host, port), nil
}
