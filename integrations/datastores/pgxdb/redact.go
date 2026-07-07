package pgxdb

import "net/url"

// redactedDSN replaces both a masked password and an entirely unparseable DSN
// in RedactDSN's output.
const redactedDSN = "REDACTED"

// RedactDSN masks the userinfo password in a URL-form DSN, leaving the
// username, host, and other components intact, so hosts can safely place a
// connection target in logs and error messages (e.g. alongside a failed
// Open). A DSN that does not parse as a URL is treated as opaque and reported
// as the literal "REDACTED" in full — never echo raw, unparseable input.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return redactedDSN
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), redactedDSN)
		}
	}
	return u.String()
}
