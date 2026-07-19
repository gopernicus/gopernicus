package goth

import (
	"errors"

	"github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/ui/goth"
)

// errNilBundle is returned by New for a nil bundle.
var errNilBundle = errors.New("authentication views/goth: New requires a non-nil *goth.Bundle")

// selfSource is the CSP source that permits same-origin resources: the bundle's
// fingerprinted assets and the externalized fragment-reader script are all served
// from the host's own origin.
const selfSource = "'self'"

// directiveKind maps a ui/goth CSP Directive to the authentication feature's
// HTMLResourceKind. Both are the literal CSP directive name, but the mapping is
// explicit so a directive ui/goth requires that is NOT a member of the feature's
// frozen widenable allowlist is skipped rather than smuggled into a header.
var directiveKind = map[goth.Directive]authentication.HTMLResourceKind{
	goth.DirectiveScript:  authentication.HTMLScriptSrc,
	goth.DirectiveStyle:   authentication.HTMLStyleSrc,
	goth.DirectiveImg:     authentication.HTMLImgSrc,
	goth.DirectiveFont:    authentication.HTMLFontSrc,
	goth.DirectiveConnect: authentication.HTMLConnectSrc,
	goth.DirectiveMedia:   authentication.HTMLMediaSrc,
	goth.DirectiveWorker:  authentication.HTMLWorkerSrc,
}

// HTMLPolicy maps the bundle's deterministic browser Requirements into the feature's
// technology-neutral HTMLResourcePolicy, plus the script-src the externalized
// fragment-reader landings need. It is the value a host hands to
// authentication.Config.HTMLPolicy so the auth CSP widens exactly far enough to load
// the GOTH stylesheet (and, on Interactive/Full profiles, the runtime), the
// same-origin fragment-reader script, and any per-render nonced inline script — and
// no further. The feature's fixed protections (default-src 'none', base-uri 'none',
// form-action 'self', frame-ancestors 'none', and the no-store/no-referrer/frame/
// content-type headers) remain feature-owned and unremovable.
//
// A non-nil policy REPLACES the feature's default script-src tail entirely (see the
// feature's buildCSP), so this policy always carries an explicit script-src: the
// externalized fragment readers are same-origin ('self'), and Nonce:true keeps the
// per-render CSP nonce available for those readers and any host-added inline script
// (Gate C C5). Omitting either would leave scripts governed by default-src 'none' and
// the reset/magic-link landings would silently fail closed.
//
// The construction cannot fail for a valid bundle (every source is a fixed keyword),
// so HTMLPolicy panics only on the impossible internal contract break; callers that
// prefer an error use NewHTMLPolicy.
func (v Views) HTMLPolicy() *authentication.HTMLResourcePolicy {
	p, err := v.newHTMLPolicy()
	if err != nil {
		panic(err)
	}
	return p
}

// NewHTMLPolicy is the error-returning form of HTMLPolicy for callers that construct
// the policy in a fallible seam.
func (v Views) NewHTMLPolicy() (*authentication.HTMLResourcePolicy, error) {
	return v.newHTMLPolicy()
}

func (v Views) newHTMLPolicy() (*authentication.HTMLResourcePolicy, error) {
	return authentication.NewHTMLResourcePolicy(v.resourceDirectives()...)
}

// resourceDirectives builds the deterministic directive slice the policy is
// constructed from. It OWNS the source ordering within each directive (Gate C C3):
// the bundle's Requirements accessors already return directives and sources in a
// stable order, and the appended fragment-reader script-src is deterministic, so the
// emitted CSP header is byte-stable run-to-run and process-to-process.
//
// It is a package-private seam so the contract test can assert directly on the
// produced directives (Kind/Sources/Nonce) — the constructed HTMLResourcePolicy is
// opaque by design.
func (v Views) resourceDirectives() []authentication.HTMLResourceDirective {
	req := v.bundle.Requirements()

	var out []authentication.HTMLResourceDirective
	scriptSeen := false
	for _, d := range req.Directives() {
		kind, ok := directiveKind[d]
		if !ok {
			continue
		}
		sources, _ := req.Sources(d)
		dir := authentication.HTMLResourceDirective{Kind: kind, Sources: append([]string(nil), sources...)}
		if kind == authentication.HTMLScriptSrc {
			// The bundle's script-src (Interactive/Full) plus the same-origin
			// fragment reader plus the per-render nonce channel (C5). Dedup keeps the
			// first occurrence, so appending 'self' is safe if it is already present.
			scriptSeen = true
			dir.Sources = append(dir.Sources, selfSource)
			dir.Nonce = true
		}
		out = append(out, dir)
	}

	// A StylesOnly bundle requires no script-src, but the externalized fragment
	// readers still need one; add it so the reset/magic-link landings work under the
	// mapped policy (C5). NewHTMLResourcePolicy re-sorts directives by name, so the
	// append order here does not affect the emitted header.
	if !scriptSeen {
		out = append(out, authentication.HTMLResourceDirective{
			Kind:    authentication.HTMLScriptSrc,
			Sources: []string{selfSource},
			Nonce:   true,
		})
	}
	return out
}
