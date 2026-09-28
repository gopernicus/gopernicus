package web

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// OriginNamespace admits every origin with exactly ONE DNS label directly
// under Suffix: {label}.{Suffix}. Build one directly or with
// ParseOriginNamespace; NewOriginPolicy validates and normalizes it.
type OriginNamespace struct {
	// Scheme is "https" or "http".
	Scheme string
	// Suffix is the registrable parent the label sits under, such as
	// "flight.deck.example.com". It needs at least two labels and carries no
	// wildcard and no trailing dot.
	Suffix string
	// Ports lists the admitted ports and is exhaustive. Nil or empty admits
	// only the scheme's default port (443 or 80); list the default port
	// explicitly to admit it beside others. A browser never writes the
	// default port into Origin, so an Origin carrying it is refused.
	Ports []int
	// Reserved lists labels that are NOT namespace matches ("accounts",
	// "api"). A reserved host is admitted only by an exact entry. It is a
	// deny-list: every label that is not one of the host's own client
	// deployments belongs here.
	Reserved []string
}

// OriginPolicyConfig is the input to NewOriginPolicy.
type OriginPolicyConfig struct {
	// Exact lists exact origins ("https://app.example.com"). "*" is refused:
	// the policy is credentialed admission only.
	Exact []string
	// Namespaces lists one-label subdomain rules.
	Namespaces []OriginNamespace
}

// OriginPolicy is an immutable, parsed browser-origin admission policy: exact
// origins plus one-label namespace rules. Build it once at boot and hand the
// same value to CORSConfig.OriginPolicy and to any pocket that gates on
// Origin, so CORS and CSRF admission cannot drift. The zero value admits
// nothing.
//
// Admission grants nothing. A host that maps the label to a tenant still
// resolves and authorizes that tenant separately.
//
// Trust model: a namespace gives EVERY label under the suffix the power of an
// exact origin — credentialed CORS reads and passage through CSRF origin
// gates. The suffix must be a zone the host fully controls. There is no
// public-suffix check (the sdk is stdlib-only), so "*.github.io"-style shared
// suffixes are accepted and must never be configured. A dangling CNAME or
// script injection on any label compromises the whole namespace; keep
// session cookies host-only (no Domain attribute) so such a label cannot also
// read them. Prefer https namespaces: an http namespace lets a network
// attacker speak for any label.
type OriginPolicy struct {
	exact      []string
	namespaces []originNamespace
}

type originNamespace struct {
	scheme   string
	suffix   string // lowercase, with its leading "."
	ports    []int
	reserved []string
}

// origin is a parsed scheme://host[:port]. port is 0 when absent.
type origin struct {
	scheme string
	host   string
	port   int
}

// NewOriginPolicy validates cfg and returns the policy. Every error wraps
// sdk.ErrInvalidInput; a host should fail its boot on one.
func NewOriginPolicy(cfg OriginPolicyConfig) (OriginPolicy, error) {
	var p OriginPolicy
	for _, raw := range cfg.Exact {
		o, ok := parseOrigin(strings.ToLower(raw))
		if !ok {
			return OriginPolicy{}, fmt.Errorf("origin policy: exact origin %q is not scheme://host[:port]: %w", raw, sdk.ErrInvalidInput)
		}
		canonical := o.canonical()
		if !slices.Contains(p.exact, canonical) {
			p.exact = append(p.exact, canonical)
		}
	}
	for _, rule := range cfg.Namespaces {
		ns, err := newOriginNamespace(rule)
		if err != nil {
			return OriginPolicy{}, err
		}
		p.namespaces = append(p.namespaces, ns)
	}
	return p, nil
}

// ParseOriginNamespace parses "scheme://*.suffix[:port]" into a rule. An
// explicit port becomes the rule's only admitted port. Reserved labels are set
// on the returned value by the caller.
func ParseOriginNamespace(pattern string) (OriginNamespace, error) {
	scheme, rest, ok := strings.Cut(strings.ToLower(pattern), "://")
	if !ok || !strings.HasPrefix(rest, "*.") {
		return OriginNamespace{}, fmt.Errorf("origin namespace %q: want scheme://*.suffix[:port]: %w", pattern, sdk.ErrInvalidInput)
	}
	rule := OriginNamespace{Scheme: scheme, Suffix: rest[2:]}
	if host, portText, err := net.SplitHostPort(rule.Suffix); err == nil {
		port, ok := parsePort(portText)
		if !ok {
			return OriginNamespace{}, fmt.Errorf("origin namespace %q: invalid port: %w", pattern, sdk.ErrInvalidInput)
		}
		rule.Suffix = host
		rule.Ports = []int{port}
	}
	if _, err := newOriginNamespace(rule); err != nil {
		return OriginNamespace{}, err
	}
	return rule, nil
}

// AllowsRequest reports whether r carries exactly one Origin header whose
// value the policy admits. A missing, repeated, "null" or malformed Origin is
// refused. Matching is case-insensitive; no forwarded header is consulted.
func (p OriginPolicy) AllowsRequest(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	return p.allows(values[0])
}

// HasHTTPNamespace reports whether any namespace rule admits plain http. A
// pocket uses it to refuse such a policy in production.
func (p OriginPolicy) HasHTTPNamespace() bool {
	for _, ns := range p.namespaces {
		if ns.scheme == "http" {
			return true
		}
	}
	return false
}

func (p OriginPolicy) allows(value string) bool {
	if len(p.exact) == 0 && len(p.namespaces) == 0 {
		return false
	}
	o, ok := parseOrigin(strings.ToLower(value))
	if !ok {
		return false
	}
	if slices.Contains(p.exact, o.canonical()) {
		return true
	}
	for _, ns := range p.namespaces {
		if ns.admits(o) {
			return true
		}
	}
	return false
}

func newOriginNamespace(rule OriginNamespace) (originNamespace, error) {
	invalid := func(why string) error {
		return fmt.Errorf("origin namespace %s://*.%s: %s: %w", rule.Scheme, rule.Suffix, why, sdk.ErrInvalidInput)
	}
	scheme := strings.ToLower(rule.Scheme)
	if defaultPort(scheme) == 0 {
		return originNamespace{}, invalid(`scheme must be "https" or "http"`)
	}
	suffix := strings.ToLower(rule.Suffix)
	labels := strings.Split(suffix, ".")
	if len(labels) < 2 {
		return originNamespace{}, invalid("suffix needs at least two labels")
	}
	for _, label := range labels {
		if !validLabel(label) {
			return originNamespace{}, invalid("suffix labels must be letters, digits and inner hyphens")
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return originNamespace{}, invalid("suffix must end in a DNS name, not an IP address")
	}
	ns := originNamespace{scheme: scheme, suffix: "." + suffix}
	for _, port := range rule.Ports {
		if port < 1 || port > 65535 {
			return originNamespace{}, invalid("port out of range")
		}
		if !slices.Contains(ns.ports, port) {
			ns.ports = append(ns.ports, port)
		}
	}
	if len(ns.ports) == 0 {
		ns.ports = []int{defaultPort(scheme)}
	}
	for _, label := range rule.Reserved {
		label = strings.ToLower(label)
		if !validLabel(label) {
			return originNamespace{}, invalid(fmt.Sprintf("reserved label %q is not a DNS label", label))
		}
		ns.reserved = append(ns.reserved, label)
	}
	return ns, nil
}

func (ns originNamespace) admits(o origin) bool {
	if o.scheme != ns.scheme || !slices.Contains(ns.ports, o.effectivePort()) {
		return false
	}
	label, ok := strings.CutSuffix(o.host, ns.suffix)
	if !ok || !validLabel(label) {
		return false
	}
	return !slices.Contains(ns.reserved, label)
}

// parseOrigin strictly parses a lowercased serialized origin. It refuses
// userinfo, path, query, fragment, trailing dots, empty hosts and an explicit
// default port, none of which a browser writes into Origin.
func parseOrigin(s string) (origin, bool) {
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok || defaultPort(scheme) == 0 || rest == "" {
		return origin{}, false
	}
	if strings.ContainsAny(rest, "/?#@,\\ \t\r\n%") {
		return origin{}, false
	}
	o := origin{scheme: scheme, host: rest}
	if host, portText, err := net.SplitHostPort(rest); err == nil {
		port, ok := parsePort(portText)
		if !ok || port == defaultPort(scheme) {
			return origin{}, false
		}
		o.host, o.port = host, port
	} else if inner, ok := strings.CutPrefix(rest, "["); ok {
		o.host, ok = strings.CutSuffix(inner, "]")
		if !ok {
			return origin{}, false
		}
	} else if strings.Contains(rest, ":") {
		return origin{}, false
	}
	if strings.HasPrefix(rest, "[") {
		ip, err := netip.ParseAddr(o.host)
		if err != nil || !ip.Is6() {
			return origin{}, false
		}
	}
	if o.host == "" || strings.HasSuffix(o.host, ".") || strings.ContainsAny(o.host, "[]") {
		return origin{}, false
	}
	return o, true
}

func (o origin) effectivePort() int {
	if o.port != 0 {
		return o.port
	}
	return defaultPort(o.scheme)
}

func (o origin) canonical() string {
	host := o.host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if o.port != 0 {
		return o.scheme + "://" + host + ":" + strconv.Itoa(o.port)
	}
	return o.scheme + "://" + host
}

func defaultPort(scheme string) int {
	switch scheme {
	case "https":
		return 443
	case "http":
		return 80
	}
	return 0
}

// parsePort accepts 1-65535 written as plain digits without a leading zero.
func parsePort(s string) (int, bool) {
	if s == "" || len(s) > 5 || s[0] == '0' {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	port, _ := strconv.Atoi(s)
	return port, port <= 65535
}

// validLabel reports whether s is one lowercase LDH DNS label: 1-63 of
// [a-z0-9-], not starting or ending with a hyphen. Punycode ("xn--") labels
// pass; raw Unicode does not.
func validLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
