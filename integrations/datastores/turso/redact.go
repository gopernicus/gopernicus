package turso

import "net/url"

// redactedDSN replaces both a masked secret and an entirely unparseable DSN in
// RedactDSN's output.
const redactedDSN = "REDACTED"

// authTokenParam is the query parameter Open appends Config.AuthToken to; a
// libSQL URL carries its token there (libsql://host?authToken=…).
const authTokenParam = "authToken"

// credentialParams are the token aliases accepted by the pinned libSQL driver.
var credentialParams = [...]string{authTokenParam, "auth_token", "jwt"}

// RedactDSN masks the userinfo password and the libSQL driver's credential
// query parameters. Malformed URLs and queries are fully redacted.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return redactedDSN
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || u.Fragment != "" {
		return redactedDSN
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), redactedDSN)
		}
	}
	for _, name := range credentialParams {
		if q.Has(name) {
			q.Set(name, redactedDSN)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
