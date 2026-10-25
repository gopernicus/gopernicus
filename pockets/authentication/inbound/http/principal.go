package authenticationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// unavailableRetryAfter is the Retry-After, in seconds, of a 503 denial.
const unavailableRetryAfter = "5"

var (
	// errNoCredential is the resolver's "nothing presented within the set".
	errNoCredential = fmt.Errorf("authentication HTTP: no credential presented: %w", sdk.ErrUnauthorized)
	// errCredentialRefused denies a credential the set does not admit.
	errCredentialRefused = fmt.Errorf("authentication HTTP: credential refused: %w", sdk.ErrUnauthorized)
	// mfaRequired is the challenge every MFA denial carries: RequireMFA, a
	// provider's RequireMFA floor (authlogic.ErrMFARequired) and the admin gate.
	mfaRequired = &insufficientAuthentication{description: "multi-factor authentication is required"}
)

// RequirePrincipal is THE authenticator. Its options are OR-sets over credential
// kinds (Accept) and transports (Transports), a liveness tier (Live,
// LiveOnMutation, Stateless), assurance requirements (RequireMFA on access and
// external tokens; FreshAuth, VerifiedEmail, Tenant and Check on external
// tokens) and a denial mode (Browser, Optional). With no options it admits every wired credential kind —
// first-party access tokens, API keys when the machine subsystem is wired, and
// external tokens when providers are wired — over both transports, re-checking
// liveness on unsafe methods (LiveOnMutation), denying with a JSON 401. Delegated
// tokens require Audience or DelegatedAudience and always require a live grant,
// including on every nested admission. Audience also excludes audience-less
// first-party tokens, API keys and external tokens; DelegatedAudience leaves
// first-party and API-key admission unchanged. FirstParty admits only
// first-party access tokens.
//
// At the OUTERMOST position it resolves the request's credential within its set
// (resolveCredential) and stashes the Principal (read via CurrentPrincipal /
// CurrentUser) plus the Credential (read via CurrentCredential). NESTED under an
// outer RequirePrincipal it never re-resolves: it narrows, checking the stashed
// Credential against its own set and denying when it falls outside. A proven
// liveness check is stamped on the proof, so a nested gate under an outer Live()
// does not repeat it. Delegated proof is rechecked on each admission.
//
// A credential service that cannot decide (sdk.ErrUnavailable) answers 503 with
// Retry-After, never a 401: under Browser() too, and under Optional() too.
//
// The set is resolved at CONSTRUCTION, so an empty Accept()/Transports(), an
// external-evidence assurance option (FreshAuth, VerifiedEmail, Tenant, Check)
// on a set admitting access tokens, or Browser() on a set
// admitting external tokens without an explicit WithBrowserLoginPath panics at
// wiring time. Nested narrowing trusts the stash written earlier in the same
// chain: the supported wiring invariant is ONE credential service per chain.
func (s *Adapter) RequirePrincipal(opts ...PrincipalOption) web.Middleware {
	set := s.resolveSet(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cred, nested := s.service.CurrentCredential(ctx)
			if !nested {
				resolved, err := s.resolveCredential(r, set)
				if err != nil {
					if set.optional && !s.credentialPresented(r, set) {
						next.ServeHTTP(w, r)
						return
					}
					s.denyPrincipal(w, r, set, err, true)
					return
				}
				ctx = resolved
				cred, _ = s.service.CurrentCredential(ctx)
			}
			if !set.admits(cred) {
				s.denyPrincipal(w, r, set, errCredentialRefused, false)
				return
			}
			if err := set.assure(ctx, cred, s.mfaRefusal); err != nil {
				var refused *mfaRefused
				if errors.As(err, &refused) {
					s.recordMFADenial(ctx, authlogic.MFAGateRoute, cred, refused.reason)
					if set.browser && cred.Kind == CredentialAccessToken {
						s.denyBrowserMFA(w, r, cred)
						return
					}
				}
				s.denyPrincipal(w, r, set, err, false)
				return
			}
			live, err := s.service.RequireLive(ctx, set.liveness, unsafeMethod(r.Method))
			if err != nil {
				s.denyPrincipal(w, r, set, err, false)
				return
			}
			next.ServeHTTP(w, r.WithContext(live))
		})
	}
}

// resolveCredential resolves the request's credential WITHIN set — the one
// resolver, at the outermost position (design §4.3):
//
//   - a bearer header, when the header transport is in the set, is AUTHORITATIVE:
//     a JWT-shaped bearer (exactly two dots) is verified as the ONE kind
//     ClassifyBearer names from its unverified issuer, a dotless one as an API
//     key; an unclassified bearer, a kind outside the set, or a failure denies,
//     and no cookie is read after one;
//   - otherwise, when the cookie transport is in the set, the native session
//     cookie, then each provider cookie (WithExternalCookies) when the set admits
//     external tokens — FIRST PRESENT WINS, so a present but invalid native
//     cookie denies and never falls through to a provider cookie;
//   - otherwise no credential at all.
//
// A credential arriving on a transport OUTSIDE the set is ignored, not denied:
// the set says what the surface reads, so a never-consulted header is not a
// bypass. A credential whose KIND is outside the set denies.
func (s *Adapter) resolveCredential(r *http.Request, set principalSet) (context.Context, error) {
	ctx := r.Context()
	if set.header {
		if raw, ok := bearerToken(r); ok {
			if isJWTToken(raw) {
				kind, ok := s.service.ClassifyBearer(raw)
				if !ok || !set.resolves(kind) {
					return ctx, errCredentialRefused
				}
				return s.service.Authenticate(ctx, kind, TransportHeader, raw)
			}
			// The API-key path is active only when the machine subsystem is wired
			// (deny-by-absence) and the surface admits keys.
			if !set.apiKey || !s.service.MachineEnabled() {
				return ctx, errCredentialRefused
			}
			return s.service.Authenticate(ctx, CredentialAPIKey, TransportHeader, raw)
		}
	}
	if set.cookie {
		if c, err := r.Cookie(s.cookie.Name); err == nil {
			if !set.accessToken {
				return ctx, errCredentialRefused
			}
			return s.service.Authenticate(ctx, CredentialAccessToken, TransportCookie, c.Value)
		}
		if raw, ok := s.providerCookie(r, set); ok {
			if kind, ok := s.service.ClassifyBearer(raw); !ok || kind != CredentialExternalToken {
				return ctx, errCredentialRefused
			}
			return s.service.Authenticate(ctx, CredentialExternalToken, TransportCookie, raw)
		}
	}
	return ctx, errNoCredential
}

// providerCookie returns the first present provider cookie when set reads them:
// the cookie transport is checked by the caller, and only a set admitting wired
// external tokens consults provider cookie names at all.
func (s *Adapter) providerCookie(r *http.Request, set principalSet) (string, bool) {
	if !set.resolves(CredentialExternalToken) {
		return "", false
	}
	for _, name := range s.externalCookies {
		if c, err := r.Cookie(name); err == nil {
			return c.Value, true
		}
	}
	return "", false
}

// credentialPresented reports whether the request carries a credential on a
// transport within set, by PRESENCE only — no verification. It mirrors
// resolveCredential's reads (a bearer header when the header transport is in
// the set; the native cookie, then the provider cookies, when the cookie
// transport is in the set) so Optional()'s pass-by-absence check can never drift
// from what the resolver actually reads, and a revoked provider cookie is never
// laundered into anonymity.
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
		if _, ok := s.providerCookie(r, set); ok {
			return true
		}
	}
	return false
}

// denyPrincipal writes an authenticator's denial. A service that could not
// decide (sdk.ErrUnavailable) gets the JSON 503 with Retry-After in every mode,
// since a login redirect would sign a user out over an outage. Otherwise a
// Browser() set answers the 303 to the configured browser login path carrying a
// validated return_to on GET/HEAD (design §9.2); any other set answers the
// byte-stable JSON 401, carrying the RFC 9470 challenge when an assurance
// option or a provider's MFA floor denied. A nested authenticator denies in its
// OWN mode, so a plain helper nested under a browser gate answers JSON.
func (s *Adapter) denyPrincipal(w http.ResponseWriter, r *http.Request, set principalSet, err error, unresolved bool) {
	if errors.Is(err, sdk.ErrUnavailable) {
		writeUnavailable(w)
		return
	}
	if set.browser {
		_, bearer := bearerToken(r)
		recover := unresolved && !(set.header && bearer) && !s.providerCookieOnly(r, set) && set.admits(Credential{
			Kind: CredentialAccessToken, Transport: TransportCookie, Profile: session.ProfileFirstParty,
		})
		s.redirectToBrowserLogin(w, r, recover)
		return
	}
	var insufficient *insufficientAuthentication
	switch {
	case errors.As(err, &insufficient):
		w.Header().Set("WWW-Authenticate", insufficient.challenge())
	case errors.Is(err, authlogic.ErrMFARequired):
		w.Header().Set("WWW-Authenticate", mfaRequired.challenge())
	}
	writeUnauthorized(w)
}

// denyBrowserMFA answers a Browser() set whose RequireMFA refused an access
// token below AAL2 or from a source MFAConfig.Methods does not admit. A
// sign-in would only mint another AAL1 session, so it never redirects to
// login: a cookie goes to the step-up page — the TOTP chooser when TOTP views
// are wired, else the passkey step-up page when passkey views are (return_to on
// GET and HEAD only, validated like the login redirect's) — and anything else
// gets the JSON 401 with the RFC 9470 challenge.
func (s *Adapter) denyBrowserMFA(w http.ResponseWriter, r *http.Request, cred Credential) {
	if s.mfaStepUpPath == "" || cred.Transport != TransportCookie {
		writeMFARequired(w)
		return
	}
	loc := s.mfaStepUpPath
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

// writeMFARequired writes the JSON 401 an MFA gate outside RequirePrincipal
// answers, carrying the RFC 9470 challenge.
func writeMFARequired(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", mfaRequired.challenge())
	writeUnauthorized(w)
}

// providerCookieOnly reports whether the cookie the resolver read was a
// provider's: native refresh recovery can never renew a provider session.
func (s *Adapter) providerCookieOnly(r *http.Request, set principalSet) bool {
	if !set.cookie {
		return false
	}
	if _, err := r.Cookie(s.cookie.Name); err == nil {
		return false
	}
	_, ok := s.providerCookie(r, set)
	return ok
}

// unsafeMethod reports whether method may mutate: everything but GET, HEAD and
// OPTIONS pays the LiveOnMutation re-check.
func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
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

// writeUnavailable writes the JSON 503 a credential service that cannot decide
// earns, with a Retry-After the client can honor.
func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", unavailableRetryAfter)
	web.RespondJSONError(w, web.ErrUnavailable("authentication temporarily unavailable"))
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
func (s *Adapter) redirectToBrowserLogin(w http.ResponseWriter, r *http.Request, recover bool) {
	loc := s.browserLoginPath
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		target := r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		if safe := redirect.SafeRelativePath(target); safe != "" {
			loc += "?return_to=" + url.QueryEscape(safe)
			if recover {
				loc += "&recover=1"
			}
		}
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}
