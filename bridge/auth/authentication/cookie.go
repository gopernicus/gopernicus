package authentication

import "net/http"

// setSessionCookies sets HTTP-only access and refresh token cookies.
func (b *Bridge) setSessionCookies(w http.ResponseWriter, accessToken, refreshToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     b.sessionCookieName,
		Value:    accessToken,
		Path:     cookiePath,
		MaxAge:   int(b.accessTokenExpiry.Seconds()),
		HttpOnly: true,
		Secure:   b.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     b.refreshCookieName,
		Value:    refreshToken,
		Path:     cookiePath,
		MaxAge:   int(b.refreshTokenExpiry.Seconds()),
		HttpOnly: true,
		Secure:   b.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookies clears both session cookies.
func (b *Bridge) clearSessionCookies(w http.ResponseWriter) {
	b.clearCookieByName(w, b.sessionCookieName)
	b.clearCookieByName(w, b.refreshCookieName)
}

// clearCookieByName clears a cookie by setting MaxAge to -1.
func (b *Bridge) clearCookieByName(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   b.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
