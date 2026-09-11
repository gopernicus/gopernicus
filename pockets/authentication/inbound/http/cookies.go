package authenticationhttp

import (
	"net/http"
	"time"
)

// CookieConfig is the resolved session-cookie policy. The auth package fills it
// from auth.BrowserConfig.SessionCookie (applying name/path defaults) plus
// auth.BrowserConfig.RefreshCookiePath.
type CookieConfig struct {
	Name   string `env:"AUTH_COOKIE_NAME"`
	Path   string `env:"AUTH_COOKIE_PATH"`
	Domain string `env:"AUTH_COOKIE_DOMAIN"`
	Secure bool   `env:"AUTH_COOKIE_SECURE"`
	MaxAge int    `env:"AUTH_COOKIE_MAX_AGE"` // access cookie lifetime in seconds; independent of session lifetime
	// RefreshPath scopes the refresh cookie (auth.BrowserConfig.RefreshCookiePath). Empty
	// → defaultRefreshCookiePath ("/auth"). It is the SINGLE path used to both
	// issue and delete the refresh cookie, so a prefixed host ("/api/v1/auth")
	// clears exactly what it set. Package auth validates a host override as an
	// absolute cookie path before it reaches here.
	RefreshPath string
}

// SetSessionCookies writes the browser credential cookies for a mint (§1.1, D4).
// It always sets the access cookie (the access JWT, existing policy: HttpOnly +
// SameSite=Lax, Secure/Domain/MaxAge from config). It sets the refresh cookie
// only when pair.RefreshToken is non-empty (so the grace lane, which issues no
// new refresh token, leaves the client's refresh cookie intact); the refresh
// cookie is HttpOnly, scoped to the resolved refresh-cookie path (default
// "/auth", covering /auth/refresh AND /auth/logout; a prefixed host configures
// "/api/v1/auth"), SameSite=Lax explicit (CSRF posture for the cookie-driven
// refresh endpoint), with MaxAge tracking the fixed refresh horizon.
func (s *Adapter) SetSessionCookies(w http.ResponseWriter, pair TokenPair) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie.Name,
		Value:    pair.AccessToken,
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   s.cookie.MaxAge,
	})
	if pair.RefreshToken != "" {
		http.SetCookie(w, s.refreshCookie(pair.RefreshToken, int(s.service.SessionLifetime()/time.Second)))
	}
}

// ClearSessionCookies expires BOTH the access and refresh cookies on the client
// (§1.5): logging out must not leave a live access JWT or refresh token behind.
func (s *Adapter) ClearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie.Name,
		Value:    "",
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(w, s.refreshCookie("", -1))
}

// refreshCookie builds the refresh cookie with the resolved refresh-cookie path
// scope (CookieConfig.RefreshPath; "/auth" by default) and the explicit
// SameSite=Lax policy (D4). maxAge < 0 expires it. It is the single construction
// point for every refresh-cookie issue and deletion, so the issued and cleared
// cookies can never be scoped to different paths.
func (s *Adapter) refreshCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     s.refreshCookieName(),
		Value:    value,
		Path:     s.cookie.RefreshPath,
		Domain:   s.cookie.Domain,
		Secure:   s.cookie.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// SessionCookieName returns the configured access (session) cookie name.
func (s *Adapter) SessionCookieName() string { return s.cookie.Name }

// RefreshCookieName returns the refresh cookie name (the access cookie name with
// a "_refresh" suffix). The refresh endpoint and logout read it to recover the
// refresh token from a browser client.
func (s *Adapter) RefreshCookieName() string { return s.refreshCookieName() }

func (s *Adapter) refreshCookieName() string { return s.cookie.Name + "_refresh" }
