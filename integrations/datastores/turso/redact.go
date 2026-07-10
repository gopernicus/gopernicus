package turso

import "net/url"

// redactedDSN replaces both a masked secret and an entirely unparseable DSN in
// RedactDSN's output.
const redactedDSN = "REDACTED"

// authTokenParam is the query parameter Open appends Config.AuthToken to; a
// libSQL URL carries its token there (libsql://host?authToken=…).
const authTokenParam = "authToken"

// RedactDSN masks the secrets in a libSQL/Turso URL-form DSN so hosts can safely
// place a connection target in logs and error messages (e.g. alongside a failed
// Open): the userinfo password, as pgxdb's twin does, and the authToken query
// parameter that Open appends from Config.AuthToken. Scheme, username, host,
// database, and every other query parameter are left intact. A DSN that does not
// parse as a URL is treated as opaque and reported as the literal "REDACTED" in
// full — never echo raw, unparseable input.
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
	if q := u.Query(); q.Has(authTokenParam) {
		q.Set(authTokenParam, redactedDSN)
		u.RawQuery = q.Encode()
	}
	return u.String()
}
