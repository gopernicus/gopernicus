package authentication

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// The technology-neutral HTML resource-policy seam (design §9.2, GOTH-0.4). A host
// (or a view adapter such as the future ui/goth authentication adapter) may hand the
// feature a validated set of ADDITIONAL Content-Security-Policy resource directives so
// a selected HTML view can load the styles, scripts, fonts, and images it needs. The
// seam is deliberately technology-neutral: it names CSP resource classes and source
// strings only, never templ, Alpine, HTMX, or ui/goth, so the feature core
// keeps importing none of them.
//
// The policy WIDENS resource loading; it can never REMOVE the fixed protections
// writeHTMLSecurity always applies (Cache-Control: no-store, Referrer-Policy:
// no-referrer, X-Frame-Options: DENY, X-Content-Type-Options: nosniff, and the CSP
// default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
// prefix). Those directive keys are NOT members of HTMLResourceKind, so a policy
// structurally cannot set, relax, or drop them.
//
// A nil *HTMLResourcePolicy selects the historical asset-free posture EXACTLY (see
// buildCSP): script-src permits only the per-render nonce, or 'none' when no nonce was
// minted. The removal of the asset restriction is opt-in and per-host.

// HTMLResourceKind is one CSP resource directive an HTMLResourcePolicy may set. The
// value is the literal CSP directive name. The membership set is the frozen allowlist
// of widenable resource classes (GOTH-0.4); the fixed protection directives
// (default-src, base-uri, form-action, frame-ancestors) are deliberately absent, so a
// policy can never name them. Adding a new resource class is new public surface that
// reopens this freeze.
type HTMLResourceKind string

// The frozen widenable resource classes (GOTH-0.4): script, style, image, font,
// connect, media, and worker. Each value is its exact CSP directive name.
const (
	HTMLScriptSrc  HTMLResourceKind = "script-src"
	HTMLStyleSrc   HTMLResourceKind = "style-src"
	HTMLImgSrc     HTMLResourceKind = "img-src"
	HTMLFontSrc    HTMLResourceKind = "font-src"
	HTMLConnectSrc HTMLResourceKind = "connect-src"
	HTMLMediaSrc   HTMLResourceKind = "media-src"
	HTMLWorkerSrc  HTMLResourceKind = "worker-src"
)

// htmlResourceKinds is the membership set HTMLResourceKind.valid consults. It is the
// single source of truth for the frozen allowlist.
var htmlResourceKinds = map[HTMLResourceKind]struct{}{
	HTMLScriptSrc:  {},
	HTMLStyleSrc:   {},
	HTMLImgSrc:     {},
	HTMLFontSrc:    {},
	HTMLConnectSrc: {},
	HTMLMediaSrc:   {},
	HTMLWorkerSrc:  {},
}

// valid reports whether k is a member of the frozen widenable-resource allowlist.
func (k HTMLResourceKind) valid() bool {
	_, ok := htmlResourceKinds[k]
	return ok
}

// HTMLResourceDirective is one requested CSP resource directive: a Kind and its
// ordered Sources, plus an optional Nonce request. It is the caller-facing input to
// NewHTMLResourcePolicy; the constructor validates and normalizes it into the opaque
// HTMLResourcePolicy. The zero value is invalid (an empty Kind), so a directive is
// always constructed explicitly.
type HTMLResourceDirective struct {
	// Kind is the CSP resource class this directive widens. It MUST be a member of
	// the frozen HTMLResourceKind allowlist; any other value (including a fixed
	// protection directive such as "default-src") is rejected at construction.
	Kind HTMLResourceKind
	// Sources are the CSP source expressions permitted for this resource class
	// (e.g. "'self'", "https://cdn.example.com", "data:"). Each source is validated
	// at construction: it must be non-empty and must contain no control character,
	// whitespace, ';', or ',', so a source can never split a directive or inject a
	// second header. A caller never formats a nonce into Sources itself — it sets
	// Nonce instead.
	Sources []string
	// Nonce, when true, appends the per-render CSP nonce ('nonce-<value>') to this
	// directive at response time. It is the ONLY channel through which a policy
	// references the nonce, so the nonce value never passes through Sources
	// validation and can never be caller-forged. When no nonce was minted for a
	// render (a randomness failure — the fail-safe no-script path) the nonce token
	// is simply omitted; a directive left with no sources renders as 'none'.
	Nonce bool
}

// resolve returns the concrete source tokens for this directive at render time,
// substituting the per-render nonce when Nonce is set and a nonce was minted. Sources
// are already validated and deduped by the constructor.
func (d HTMLResourceDirective) resolve(nonce string) []string {
	out := make([]string, 0, len(d.Sources)+1)
	out = append(out, d.Sources...)
	if d.Nonce && nonce != "" {
		out = append(out, "'nonce-"+nonce+"'")
	}
	return out
}

// HTMLResourcePolicy is a validated, immutable set of additional CSP resource
// directives. It is constructed only through NewHTMLResourcePolicy (the zero value
// carries no directives and is a valid asset-free policy, but a host expresses "no
// policy" with a nil *HTMLResourcePolicy, not a zero value). Its directives are stored
// in a deterministic order (by directive name) so the emitted header is byte-stable
// across renders and processes.
//
// Source ORDER within a directive is preserved as supplied (dedup keeps the first
// occurrence), and that order is load-bearing: it is what keeps the emitted header
// byte-stable across processes. The GOTH-7.2 view adapter that maps
// goth.Bundle.Requirements() into a policy must therefore emit each directive's sources
// deterministically, or the header will vary run-to-run.
type HTMLResourcePolicy struct {
	directives []HTMLResourceDirective
}

// NewHTMLResourcePolicy validates the requested directives and returns an immutable
// policy. Validation is LOUD at construction (the feature's Config posture), never a
// silently emitted attacker-controlled header: an unknown/fixed directive key, a
// directive with neither a source nor a nonce, an empty source, or a source carrying a
// control character, whitespace, ';', or ',' each returns an error wrapping
// sdk.ErrInvalidInput. Duplicate Kinds merge (their sources concatenate, deduped);
// duplicate sources within a Kind collapse to the first occurrence. The result orders
// directives by directive name so the CSP header is deterministic.
func NewHTMLResourcePolicy(directives ...HTMLResourceDirective) (*HTMLResourcePolicy, error) {
	merged := map[HTMLResourceKind]*HTMLResourceDirective{}
	for _, d := range directives {
		if !d.Kind.valid() {
			return nil, fmt.Errorf("%w: unknown or fixed HTML resource directive %q", sdk.ErrInvalidInput, d.Kind)
		}
		if len(d.Sources) == 0 && !d.Nonce {
			return nil, fmt.Errorf("%w: HTML resource directive %q has no sources and no nonce", sdk.ErrInvalidInput, d.Kind)
		}
		cur := merged[d.Kind]
		if cur == nil {
			cur = &HTMLResourceDirective{Kind: d.Kind}
			merged[d.Kind] = cur
		}
		if d.Nonce {
			cur.Nonce = true
		}
		for _, s := range d.Sources {
			if err := validateSource(s); err != nil {
				return nil, fmt.Errorf("%w: HTML resource directive %q: %v", sdk.ErrInvalidInput, d.Kind, err)
			}
			if !containsString(cur.Sources, s) {
				cur.Sources = append(cur.Sources, s)
			}
		}
	}

	kinds := make([]HTMLResourceKind, 0, len(merged))
	for k := range merged {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	ordered := make([]HTMLResourceDirective, 0, len(kinds))
	for _, k := range kinds {
		ordered = append(ordered, *merged[k])
	}
	return &HTMLResourcePolicy{directives: ordered}, nil
}

// render returns the "; <kind> <sources>" tail the policy contributes to a CSP header,
// substituting the per-render nonce. A directive whose sources all resolve away renders
// as "<kind> 'none'" so it fails safe rather than silently inheriting default-src.
func (p *HTMLResourcePolicy) render(nonce string) string {
	var b strings.Builder
	for _, d := range p.directives {
		b.WriteString("; ")
		b.WriteString(string(d.Kind))
		srcs := d.resolve(nonce)
		if len(srcs) == 0 {
			b.WriteString(" 'none'")
			continue
		}
		for _, s := range srcs {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	return b.String()
}

// validateSource rejects any CSP source that could split a directive or inject a
// second header. It permits keyword and origin/scheme forms ('self', 'unsafe-inline',
// https://cdn.example.com, data:, https:) — the feature stays neutral about how wide a
// host chooses to open resource loading — but forbids control characters, whitespace,
// ';', and ',', which are the only tokens that carry structural meaning in a CSP header.
func validateSource(s string) error {
	if s == "" {
		return errors.New("empty source")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("control character in source %q", s)
		}
		switch r {
		case ' ', '\t', ';', ',':
			return fmt.Errorf("illegal character %q in source %q", r, s)
		}
	}
	return nil
}

// containsString reports whether ss already holds s (source dedup).
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
