// Package redirect is the auth feature's open-redirect guard for OAuth
// post-flow destinations (design §3). It is an exact-match allowlist — a
// requested target is honored only when it appears verbatim in the host-supplied
// list — kept feature-internal per the capability map's allowlist row (promoted
// to sdk/foundation/web only on a second consumer). The same-origin root ("/") is always
// safe and needs no allowlisting.
package redirect

import "strings"

// defaultTarget is the always-safe, same-origin fallback used when no target is
// requested or the requested target is not allowlisted.
const defaultTarget = "/"

// SafeRelativePath returns p when it is a safe same-origin relative path (a single
// leading slash, no scheme, no protocol-relative "//" or backslash trickery a
// browser might normalize into one, and no control characters), else "". A relative
// path is never an off-site open-redirect vector, so it needs no allowlisting. It is
// the single validator every internal return-to (the form lane's stale-session
// redirect and the browser identity gates' return_to) routes through, so the rule
// can never drift between them.
func SafeRelativePath(p string) string {
	if p == "" || p[0] != '/' {
		return ""
	}
	if strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\") || strings.Contains(p, "://") {
		return ""
	}
	// Reject control characters (< 0x20 and DEL 0x7f): a browser or proxy may
	// normalize an embedded CR/LF/NUL in ways that split a header or escape the
	// path, so a target carrying one is never a safe same-origin path.
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return ""
		}
	}
	return p
}

// Allowlist matches redirect targets by exact string equality.
type Allowlist struct {
	allowed map[string]struct{}
}

// New builds an Allowlist from entries. Blank entries are ignored. A nil or
// empty list still permits the same-origin default ("/").
func New(entries []string) Allowlist {
	allowed := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e != "" {
			allowed[e] = struct{}{}
		}
	}
	return Allowlist{allowed: allowed}
}

// Allowed reports whether target is exactly permitted. The same-origin default
// ("/") is always allowed.
func (a Allowlist) Allowed(target string) bool {
	if target == defaultTarget {
		return true
	}
	_, ok := a.allowed[target]
	return ok
}

// Resolve returns the safe redirect target for a requested value: the request
// itself when allowlisted (or the same-origin default), otherwise the
// same-origin default. It never returns a non-allowlisted target, so a caller
// can pass it straight to an HTTP redirect.
func (a Allowlist) Resolve(requested string) string {
	if requested == "" {
		return defaultTarget
	}
	if a.Allowed(requested) {
		return requested
	}
	return defaultTarget
}
