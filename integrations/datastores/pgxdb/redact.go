package pgxdb

import "net/url"

// redactedDSN replaces both a masked password and an entirely unparseable DSN
// in RedactDSN's output.
const redactedDSN = "REDACTED"

// RedactDSN masks userinfo passwords and password/sslpassword query values in a
// PostgreSQL URL DSN, preserving the username, host and other connection settings
// for diagnostics. Keyword DSNs, malformed input, fragments and unsupported URL
// forms are reported as the literal "REDACTED" in full.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Opaque != "" || u.Fragment != "" {
		return redactedDSN
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return redactedDSN
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), redactedDSN)
		}
	}
	masked := false
	for _, name := range []string{"password", "sslpassword"} {
		if q.Has(name) {
			q.Set(name, redactedDSN)
			masked = true
		}
	}
	if masked {
		u.RawQuery = q.Encode()
	}
	return u.String()
}
