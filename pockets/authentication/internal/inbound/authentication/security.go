package authentication

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// HTTP security primitives (design §9.1). These are the reusable building
// blocks — CSRF/origin protection for cookie-authenticated mutations, JSON
// request hardening, and no-store helpers — that the credential, passwordless,
// and re-key phases (5/6/7) wire onto their routes. AV3-0.5 lands and proves
// them; it deliberately does NOT rewrite the existing route table.

const (
	// csrfCookieName is the non-HttpOnly double-submit CSRF cookie a browser
	// client echoes back in the csrfHeaderName header. It is readable by the
	// page's own script so a same-origin SPA can attach it; a cross-site page
	// cannot read it, which is what makes the double-submit comparison sound.
	// The __Host- prefix makes a browser refuse the cookie unless it is Secure,
	// Path=/, and carries NO Domain attribute (see setCSRFCookie), which is what
	// stops a sibling host under the same registrable domain from setting this
	// name at all. The prefix therefore requires HTTPS: over plain http a browser
	// rejects the Set-Cookie outright rather than downgrading it.
	csrfCookieName = "__Host-auth_csrf"

	// csrfHeaderName carries the CSRF token the client copies from the cookie.
	csrfHeaderName = "X-CSRF-Token"

	// csrfTokenBytes is the raw entropy behind a double-submit token. It also
	// pins the shape the bootstrap accepts when reusing an existing cookie.
	csrfTokenBytes = 32

	// originRejectedCode is the stable machine-readable code an ORIGIN-gate denial
	// carries. It is deliberately distinct from web.ErrForbidden's
	// permission_denied (an authorization denial) and from the double-submit
	// failure, so a host can tell a misconfigured Origin allowlist apart from a
	// stale token without branching on message copy.
	originRejectedCode = "origin_rejected"

	// maxJSONBodyBytes bounds an auth JSON request body before decoding so an
	// oversized upload is rejected with 413 rather than buffered whole.
	maxJSONBodyBytes = 1 << 20 // 1 MiB
)

// randRead is the CSRF token entropy source. It is a var solely so a test can
// prove the bootstrap fails closed — 500, no token, no cookie — when randomness
// is unavailable; production always reads crypto/rand.
var randRead = rand.Read

// csrfConfig configures the browser-safe-mutation gate. allowedOrigins is the
// exact-match Origin allowlist (a "*" entry never authorizes a credentialed
// cross-origin mutation); sessionCookieName is the access cookie whose presence
// marks a request as cookie-authenticated rather than bearer-only.
type csrfConfig struct {
	allowedOrigins    []string
	sessionCookieName string
}

// MutationSecurity is the host-facing browser-safe-mutation policy the pocket's
// Register threads into Mount (design §9.1): the exact-match Origin allowlist and
// the session cookie name marking a request browser-driven. It is exported so the
// pocket package can build it from Config.AllowedOrigins and the resolved cookie
// name without exposing the unexported csrfConfig.
type MutationSecurity struct {
	AllowedOrigins    []string
	SessionCookieName string
}

// csrf builds the internal gate config from the host policy.
func (m MutationSecurity) csrf() csrfConfig {
	return csrfConfig{allowedOrigins: m.AllowedOrigins, sessionCookieName: m.SessionCookieName}
}

// requireBrowserSafeMutation returns middleware that protects a
// cookie-authenticated mutation with an allowlisted Origin / Sec-Fetch-Site
// check and a double-submit CSRF token. Bearer-only (API) requests skip the
// gate entirely — a cross-site page cannot attach an Authorization header
// without a CORS-approved preflight, so they are not a browser CSRF vector and
// must not be forced through a browser CSRF flow. SameSite cookies remain
// defense-in-depth, not the sole control (design §9.1).
func requireBrowserSafeMutation(cfg csrfConfig) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isBearerOnly(r, cfg.sessionCookieName) {
				next.ServeHTTP(w, r)
				return
			}

			if !browserOriginAllowed(r, cfg.allowedOrigins) {
				forbidOrigin(w)
				return
			}

			// A form-encoded browser mutation cannot set the X-CSRF-Token header, so
			// its handler performs the double-submit compare against the body's
			// csrf_token field instead (design §9.1, form lane; see accountForm). The
			// Origin allowlist above still gates it here; the header double-submit
			// remains the sole additional gate for JSON/fetch callers. Deferring here
			// is safe: a form body reaches an account form handler only when Views is
			// wired, and that handler is required to run the body double-submit.
			if classifyContent(r) == contentForm {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(csrfCookieName)
			header := r.Header.Get(csrfHeaderName)
			if err != nil || cookie.Value == "" || header == "" ||
				subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
				forbidCSRF(w, "invalid or missing CSRF token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requireBrowserSafeOrigin returns middleware that enforces the allowlisted-Origin
// / Sec-Fetch-Site policy for a CREDENTIAL-ESTABLISHMENT endpoint (passwordless
// start/verify/redeem) WITHOUT the double-submit CSRF token check (design §9.1).
// These endpoints have no pre-existing session, so there is no __Host-auth_csrf
// cookie to double-submit; requiring one would break a first-time browser
// sign-in. The Origin
// allowlist alone prevents a cross-site page from forcing a credential mint (login
// CSRF) — a minted session cookie therefore cannot be established from a disallowed
// origin. A non-browser client that sends neither Origin nor Sec-Fetch-Site (a
// native/mobile/CLI caller with a bearer/body contract) is allowed through: browser
// origin enforcement must never block native clients (the phase-7 stop condition).
func requireBrowserSafeOrigin(cfg csrfConfig) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !browserOriginAllowed(r, cfg.allowedOrigins) {
				forbidOrigin(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// browserOriginAllowed reports whether a request passes the allowlisted-Origin /
// Sec-Fetch-Site policy (design §9.1). Sec-Fetch-Site is the strongest signal when
// the browser sets it, but ONLY same-origin is auto-allowed: same-site is a sibling
// origin under the same registrable domain (evil.example.com vs app.example.com), so
// an attacker-controlled sibling must still clear the exact Origin allowlist — a
// same-site request whose Origin is absent or not allowlisted is rejected. cross-site
// / cross-origin likewise require an allowlisted Origin. A request that carries
// NEITHER header is a non-browser client and passes (the bearer/body path). It is the
// shared origin gate behind both the browser-safe-mutation gate (which adds the
// double-submit CSRF token on top) and the credential-establishment origin gate
// (which does not).
func browserOriginAllowed(r *http.Request, allowedOrigins []string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "same-site", "cross-site", "cross-origin":
		return originAllowed(r.Header.Get("Origin"), allowedOrigins)
	default:
		if origin := r.Header.Get("Origin"); origin != "" {
			return originAllowed(origin, allowedOrigins)
		}
		return true
	}
}

// issueCSRFToken mints a 256-bit token, writes it as the double-submit cookie,
// and returns it so a rendered form or JSON response can hand the same value to
// the client for the csrfHeaderName header. On a randomness failure it returns
// the error having written NO cookie, so the caller can fail closed.
func issueCSRFToken(w http.ResponseWriter) (string, error) {
	var b [csrfTokenBytes]byte
	if _, err := randRead(b[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	setCSRFCookie(w, token)
	return token, nil
}

// setCSRFCookie writes token as the double-submit cookie. It is intentionally NOT
// HttpOnly (the page script must read it) but is Secure + SameSite=Lax. Secure,
// Path=/ and the absence of a Domain attribute are also the __Host- prefix
// preconditions csrfCookieName depends on; none of the three may be relaxed.
func setCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}

// currentCSRFToken returns the request's existing double-submit token when it is
// present AND carries the exact shape issueCSRFToken mints (csrfTokenBytes of
// base64url entropy). Reuse is what keeps a second tab's in-flight token valid
// across a bootstrap.
//
// The shape check rejects a MALFORMED cookie only. It does NOT prove the pocket
// minted the value: any well-formed 32-byte base64url string passes and IS reused
// and echoed. Provenance is enforced by the cookie NAME instead — the __Host-
// prefix makes a browser refuse a Set-Cookie for this name that carries a Domain
// attribute, so a sibling host under the same registrable domain can no longer
// plant it. What remains is script already running on THIS origin, which is
// game-over regardless of CSRF. The ORIGIN ALLOWLIST remains the mutation gate:
// even a fixated token cannot be spent from a non-allowlisted origin. Note that
// prefix rules are a BROWSER control — Go's net/http server and cookiejar treat
// the name as an ordinary string, so a test may still supply any value it likes.
// See TestCSRFBootstrapReusesForeignWellFormedToken, which pins the behavior.
func currentCSRFToken(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(raw) != csrfTokenBytes {
		return "", false
	}
	return cookie.Value, true
}

// isBearerOnly reports whether the request authenticates only via an
// Authorization: Bearer token — a bearer present and no session cookie. A
// request carrying the session cookie is treated as browser-driven even if it
// also presents a bearer, so it stays behind the CSRF gate.
func isBearerOnly(r *http.Request, sessionCookieName string) bool {
	if _, ok := bearerToken(r); !ok {
		return false
	}
	if sessionCookieName != "" {
		if _, err := r.Cookie(sessionCookieName); err == nil {
			return false
		}
	}
	return true
}

// originAllowed reports whether origin exactly matches an allowlist entry. A
// "*" entry is ignored: a wildcard never authorizes a credentialed
// cross-origin mutation (design §9.1 — CORS never combines wildcard origins
// with credentials).
func originAllowed(origin string, allow []string) bool {
	if origin == "" {
		return false
	}
	for _, a := range allow {
		if a == "*" {
			continue
		}
		if a == origin {
			return true
		}
	}
	return false
}

// forbidCSRF writes the generic 403 for a rejected double-submit token. The
// message never distinguishes which check failed, so a probe cannot map the
// gate; the code stays web.ErrForbidden's permission_denied.
func forbidCSRF(w http.ResponseWriter, msg string) {
	web.RespondJSONError(w, web.ErrForbidden(msg))
}

// forbidOrigin writes the 403 for a request the ORIGIN gate rejected, carrying
// the stable originRejectedCode. The human message stays the same non-sensitive
// copy — the code, not the message, is what a client branches on, and a
// misconfigured allowlist is the one thing worth diagnosing. It says nothing
// about which origin would have been accepted.
func forbidOrigin(w http.ResponseWriter) {
	web.RespondJSONError(w, web.NewError(http.StatusForbidden, "cross-site request rejected").WithCode(originRejectedCode))
}

// csrfResponse is the GET /auth/csrf body. Token is ALWAYS the value of the
// effective __Host-auth_csrf cookie on the same response, so a cross-origin SPA that
// cannot read the API-origin cookie can still echo it in csrfHeaderName.
type csrfResponse struct {
	Token string `json:"csrf_token"`
}

// csrfBootstrap serves GET /auth/csrf: the JSON double-submit bootstrap for a
// cookie-authenticated SPA on a different origin than the API, which cannot read
// the __Host-auth_csrf cookie itself. The route is live-session-gated and rides the
// origin-only browser gate (see Mount).
//
// It REUSES a well-formed existing token instead of rotating: another tab may be
// holding the current value in flight, and blindly minting on every bootstrap
// would invalidate it. A missing or malformed cookie mints a fresh token; a
// randomness failure fails closed with a 500 and no token in the body. Either
// way the response sets the cookie whose value the body reports, so the two can
// never disagree.
func (*handlers) csrfBootstrap(w http.ResponseWriter, r *http.Request) {
	writeNoStore(w)
	token, ok := currentCSRFToken(r)
	if ok {
		setCSRFCookie(w, token)
	} else {
		minted, err := issueCSRFToken(w)
		if err != nil {
			web.RespondJSONError(w, web.ErrInternal("could not issue a CSRF token"))
			return
		}
		token = minted
	}
	web.RespondJSONOK(w, csrfResponse{Token: token})
}

// requireJSON enforces a strict application/json content type, writing 415 and
// returning false otherwise. A charset or other parameter is tolerated; the
// media type must be exactly application/json.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		web.RespondJSONError(w, web.NewError(http.StatusUnsupportedMediaType,
			"content type must be application/json").WithCode("unsupported_media_type"))
		return false
	}
	return true
}

// strictJSONBody bounds the body with a MaxBytesReader, decodes exactly one
// JSON value into dst rejecting unknown fields, and rejects any trailing data
// after that value. It writes 413 for an oversized body and 400 for malformed,
// unknown-field, or trailing input, returning false on any failure.
func strictJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			web.RespondJSONError(w, web.ErrPayloadTooLarge("request body too large"))
			return false
		}
		web.RespondJSONError(w, web.ErrBadRequest("invalid request body"))
		return false
	}
	// A well-formed request carries exactly one JSON value; anything after it
	// (a second object, a stray token) is rejected.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		web.RespondJSONError(w, web.ErrBadRequest("unexpected trailing data in request body"))
		return false
	}
	return true
}

// writeNoStore marks a response non-cacheable. Auth responses carrying secrets
// or the method inventory set it so a shared cache never retains them
// (design §9.1).
func writeNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// writeNoReferrer instructs the browser to send no Referer when the user navigates
// away, keeping a fragment-borne magic-link token out of any downstream Referer
// header (design §6.4). It rides alongside writeNoStore on the passwordless
// credential-establishment responses; the full landing page (restrictive CSP,
// history scrub) is phase 8.
func writeNoReferrer(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// fixedCSPPrefix is the immutable head of every auth CSP (design §9.1/§9.2). It locks
// all default resource loading off, stops base-tag hijacking and cross-origin form
// posts, and forbids framing. No HTMLResourcePolicy can remove or weaken it: the policy
// only appends widenable resource directives after this prefix, and the fixed directive
// keys are not members of HTMLResourceKind.
const fixedCSPPrefix = "default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// writeHTMLSecurity applies the full HTML security-header policy every auth page and
// redirect carries (design §9.1/§9.2): no-store so a shared cache never retains a
// form or inventory, no-referrer so a fragment token never leaks downstream, frame
// and content-type protections, and a Content-Security-Policy built by buildCSP. These
// four headers plus the fixedCSPPrefix are POCKET-OWNED and unremovable: a policy or a
// view adapter can widen resource loading but can never turn them off.
func writeHTMLSecurity(w http.ResponseWriter, policy *HTMLResourcePolicy, nonce string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", buildCSP(policy, nonce))
}

// buildCSP composes the CSP header value. A nil policy reproduces the historical
// asset-free posture EXACTLY: the fixed prefix plus a script-src that permits only the
// per-render nonce (or 'none' when none was minted — the fail-safe no-script path for
// the bundled reset/magic fragment readers). A non-nil policy appends its validated,
// deterministically ordered resource directives after the fixed prefix; a widening
// policy that still wants the bundled inline scripts requests the nonce on its
// script-src directive (HTMLResourceDirective.Nonce).
func buildCSP(policy *HTMLResourcePolicy, nonce string) string {
	if policy == nil {
		scriptSrc := "'none'"
		if nonce != "" {
			scriptSrc = "'nonce-" + nonce + "'"
		}
		return fixedCSPPrefix + "; script-src " + scriptSrc
	}
	return fixedCSPPrefix + policy.render(nonce)
}
