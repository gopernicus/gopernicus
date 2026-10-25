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

// DefaultPasskeyScriptPath is the public URL the passkey controls load their script
// from when a host does not override it with WithPasskeyScriptPath. The host mounts
// PasskeyScriptHandler() under this path, same-origin like the fragment reader.
const DefaultPasskeyScriptPath = "/assets/auth/passkey.js"

// fragmentScript is the externalized fragment-token reader served to the reset and
// magic-link landings. It is checked in as source and embedded verbatim (no build
// step): a tiny stdlib-readable script, not a bundled runtime.
//
//go:embed assets/fragment.js
var fragmentScript []byte

// passkeyScript drives the passkey controls (sign-in, registration, step-up) over
// the pocket's passkey JSON routes. Like the fragment reader it is checked-in
// source embedded verbatim, with no build step.
//
//go:embed assets/passkey.js
var passkeyScript []byte

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

// PasskeyScript returns the bytes of the passkey script so a host can serve it
// however it prefers. Most hosts use PasskeyScriptHandler instead.
func PasskeyScript() []byte {
	out := make([]byte, len(passkeyScript))
	copy(out, passkeyScript)
	return out
}

// PasskeyScriptHandler returns an http.Handler that serves the passkey script as
// application/javascript with nosniff. The host mounts it under the path its pages
// load it from (DefaultPasskeyScriptPath, or WithPasskeyScriptPath); a custom-view
// host renders its own <script src> from the path it mounts. The page loads it with
// an un-nonced tag, so it runs under HTMLPolicy's script-src 'self' (connect-src
// 'self' covers its fetches); a host without HTMLPolicy keeps the pocket's
// nonce-only script-src, which blocks it, and the passkey controls stay hidden.
func PasskeyScriptHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "passkey.js", fragmentModTime, bytes.NewReader(passkeyScript))
	})
}

// fragmentModTime is a fixed modtime so ServeContent emits a stable Last-Modified; the
// embedded bytes never change at runtime.
var fragmentModTime = time.Unix(0, 0)
