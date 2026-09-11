package authenticationhttp

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// RequirePrincipal is THE authenticator. Its options are OR-sets over credential
// kinds (Accept) and transports (Transports) plus a liveness tier (Live) and a
// browser denial mode (Browser); with no options it admits every wired
// credential over both transports, statelessly, denying with a JSON 401.
//
// At the OUTERMOST position it resolves the request's credential within its set
// (resolveCredential) and stashes the Principal (read via CurrentPrincipal /
// CurrentUser) plus the Credential (read via CurrentCredential). NESTED under an
// outer RequirePrincipal it never re-resolves: it narrows, checking the stashed
// Credential against its own set and denying when it falls outside. A Live()
// gate runs the session lookup once — a nested Live() under an outer one reads
// the proven CurrentSessionID and passes.
//
// The set is resolved at CONSTRUCTION, so an empty Accept()/Transports() panics
// at wiring time. Nested narrowing trusts the stash written earlier in the same
// chain: the supported wiring invariant is ONE authentication Service per chain.
func (s *Adapter) RequirePrincipal(opts ...PrincipalOption) web.Middleware {
	set := s.resolveSet(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cred, nested := s.service.CurrentCredential(ctx)
			if nested {
				if !set.admits(cred.Kind, cred.Transport) {
					s.denyPrincipal(w, r, set)
					return
				}
			} else {
				resolved, ok := s.resolveCredential(r, set)
				if !ok {
					if set.optional && !s.credentialPresented(r, set) {
						next.ServeHTTP(w, r)
						return
					}
					s.denyPrincipal(w, r, set)
					return
				}
				ctx = resolved
				cred, _ = s.service.CurrentCredential(ctx)
			}
			if set.live {
				live, ok := s.service.RequireLive(ctx)
				if !ok {
					s.denyPrincipal(w, r, set)
					return
				}
				ctx = live
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveCredential resolves the request's credential WITHIN set — the one
// resolver, at the outermost position (design §4.3):
//
//   - a bearer header, when the header transport is in the set, is AUTHORITATIVE:
//     it is classed by shape (isJWTToken — exactly two dots ⇒ access token, else
//     ⇒ API key) and a failure denies; the cookie is never read after one;
//   - otherwise the access-JWT session cookie, when the cookie transport is in
//     the set;
//   - otherwise no credential at all.
//
// A credential arriving on a transport OUTSIDE the set is ignored, not denied:
// the set says what the surface reads, so a never-consulted header is not a
// bypass. A credential whose KIND is outside the set denies.
func (s *Adapter) resolveCredential(r *http.Request, set principalSet) (context.Context, bool) {
	if set.header {
		if raw, ok := bearerToken(r); ok {
			if isJWTToken(raw) {
				if !set.accessToken {
					return r.Context(), false
				}
				return s.service.Authenticate(r.Context(), CredentialAccessToken, TransportHeader, raw)
			}
			// The API-key path is active only when the machine subsystem is wired
			// (deny-by-absence) and the surface admits keys.
			if !set.apiKey || !s.service.MachineEnabled() {
				return r.Context(), false
			}
			return s.service.Authenticate(r.Context(), CredentialAPIKey, TransportHeader, raw)
		}
	}
	if set.cookie {
		if c, err := r.Cookie(s.cookie.Name); err == nil {
			if !set.accessToken {
				return r.Context(), false
			}
			return s.service.Authenticate(r.Context(), CredentialAccessToken, TransportCookie, c.Value)
		}
	}
	return r.Context(), false
}

// credentialPresented reports whether the request carries a credential on a
// transport within set, by PRESENCE only — no verification. It mirrors
// resolveCredential's two reads (a bearer header when the header transport is
// in the set, the access cookie when the cookie transport is in the set) so
// Optional()'s pass-by-absence check can never drift from what the resolver
// actually reads.
func (s *Adapter) credentialPresented(r *http.Request, set principalSet) bool {
	if set.header {
		if _, ok := bearerToken(r); ok {
			return true
		}
	}
	if set.cookie {
		if _, err := r.Cookie(s.cookie.Name); err == nil {
			return true
		}
	}
	return false
}

// denyPrincipal writes an authenticator's denial: the byte-stable JSON 401, or —
// for a Browser() set — the 303 to the configured browser login path carrying a
// validated return_to on GET/HEAD (design §9.2). A nested authenticator denies in
// its OWN mode, so a plain helper nested under a browser gate answers JSON.
func (s *Adapter) denyPrincipal(w http.ResponseWriter, r *http.Request, set principalSet) {
	if set.browser {
		s.redirectToBrowserLogin(w, r)
		return
	}
	writeUnauthorized(w)
}

// bearerToken extracts the token from an `Authorization: Bearer <token>` header.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// isJWTToken reports whether a bearer token is JWT-shaped: a JWT has exactly two
// dots (header.payload.signature), and a dotless API key never does (design
// §4.3's classing heuristic).
func isJWTToken(token string) bool {
	return strings.Count(token, ".") == 2
}

// writeUnauthorized writes a 401 JSON error via the shared sdk responder, so an
// authenticator's rejection matches the pocket's other error responses (FS9).
func writeUnauthorized(w http.ResponseWriter) {
	web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
}

// redirectToBrowserLogin issues the 303 See Other a browser identity gate
// (an authenticator carrying Browser()) sends on denial — no JSON
// body, no response-writer interception (design §9.2). For a GET or HEAD it appends
// ?return_to=<escaped original path+query> when the original target validates as a
// safe root-relative path (redirect.SafeRelativePath, the same rule the form lane
// uses); an unvalidated target is omitted so it can never seed an open redirect. For
// any other method it redirects WITHOUT return_to — a later GET must not replay a
// mutation.
func (s *Adapter) redirectToBrowserLogin(w http.ResponseWriter, r *http.Request) {
	loc := s.browserLoginPath
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		target := r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		if safe := redirect.SafeRelativePath(target); safe != "" {
			loc += "?return_to=" + url.QueryEscape(safe)
		}
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}
