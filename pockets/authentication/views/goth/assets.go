package goth

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

// DefaultFragmentScriptPath is the public URL the reset/magic-link landings load the
// externalized fragment-reader script from when a host does not override it with
// WithFragmentScriptPath. The host mounts FragmentScriptHandler() under this path (a
// same-origin route so the mapped CSP's script-src 'self' covers it).
const DefaultFragmentScriptPath = "/assets/auth/fragment.js"

// fragmentScript is the externalized fragment-token reader served to the reset and
// magic-link landings. It is checked in as source and embedded verbatim (no build
// step): a tiny stdlib-readable script, not a bundled runtime.
//
//go:embed assets/fragment.js
var fragmentScript []byte

// FragmentScript returns the bytes of the externalized fragment-reader script so a
// host can serve it however it prefers. Most hosts use FragmentScriptHandler instead.
func FragmentScript() []byte {
	out := make([]byte, len(fragmentScript))
	copy(out, fragmentScript)
	return out
}

// FragmentScriptHandler returns an http.Handler that serves the externalized
// fragment-reader script as application/javascript. The host mounts it under the
// path the Views loads it from (DefaultFragmentScriptPath, or WithFragmentScriptPath).
// It sets a no-cookie, cacheable response; the script carries no secret.
func FragmentScriptHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "fragment.js", fragmentModTime, bytes.NewReader(fragmentScript))
	})
}

// fragmentModTime is a fixed modtime so ServeContent emits a stable Last-Modified; the
// embedded bytes never change at runtime.
var fragmentModTime = time.Unix(0, 0)
