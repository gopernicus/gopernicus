package authsvc

import (
	"net/url"
)

// PasswordResetTokenParam is the query parameter the reset landing URL receives
// (CHAU-5.2). It is exported so the public auth package's configuration validator
// and a host's SPA route agree on one name rather than two string literals.
const PasswordResetTokenParam = "token"

// buildPasswordResetURL appends the reset token to the configured landing URL.
//
// It is a PURE function of (configured URL, token): the request Host, Forwarded,
// and X-Forwarded-* headers cannot participate, because it never sees a request —
// it runs in the delivery worker, off the request path, from a value validated at
// construction. That is the whole point. A reset link derived from a request
// header is an account-takeover primitive: an attacker who can set Host on a
// forgot-password request would receive a link pointing at their own origin.
//
// Existing non-secret query parameters on the configured URL are PRESERVED — a
// host may legitimately configure `.../reset?app=console` — and the token is
// appended alongside them. The configured URL is parsed fresh on every call and
// never mutated, so one send cannot leak its token into the next.
//
// An unparseable base returns the empty string rather than a malformed link; the
// caller treats that as "no link" and falls back to the legacy template. In
// practice construction already rejected such a value.
func buildPasswordResetURL(configured, token string) string {
	if configured == "" || token == "" {
		return ""
	}
	u, err := url.Parse(configured)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set(PasswordResetTokenParam, token)
	u.RawQuery = q.Encode()
	return u.String()
}
