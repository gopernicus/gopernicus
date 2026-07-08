// Package redirect is the auth feature's open-redirect guard for OAuth
// post-flow destinations (design §3). It is an exact-match allowlist — a
// requested target is honored only when it appears verbatim in the host-supplied
// list — kept feature-internal per the capability map's allowlist row (promoted
// to sdk/web only on a second consumer). The same-origin root ("/") is always
// safe and needs no allowlisting.
package redirect

// defaultTarget is the always-safe, same-origin fallback used when no target is
// requested or the requested target is not allowlisted.
const defaultTarget = "/"

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
