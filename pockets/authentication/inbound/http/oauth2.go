package authenticationhttp

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const maxOAuth2BodyBytes = 64 << 10

func mountOAuth2(r pockets.RouteRegistrar, h *handlers) {
	metadata := h.oauth2.Service.Metadata()
	issuer, _ := url.Parse(metadata.Issuer)
	discovery := "/.well-known/oauth-authorization-server" + strings.TrimRight(issuer.Path, "/")
	r.Handle("GET", discovery, h.oauth2Metadata, oauth2Headers)
	browser := htmlSecured(h.htmlPolicy, h.svc.RequirePrincipal(FirstParty(), Transports(TransportCookie), Live(), Browser()))
	r.Handle("GET", "/auth/oauth2/authorize", h.oauth2AuthorizePage, oauth2Headers, browser)
	r.Handle("POST", "/auth/oauth2/authorize", h.oauth2Authorize, oauth2Headers, browser, requireBrowserSafeMutation(h.mutation.csrf()))
	r.Handle("POST", "/auth/oauth2/token", h.oauth2Token, oauth2Headers)
	r.Handle("POST", "/auth/oauth2/revoke", h.oauth2Revoke, oauth2Headers)
	r.Handle("POST", "/auth/oauth2/introspect", h.oauth2Introspect, oauth2Headers)
}

func oauth2Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) oauth2Metadata(w http.ResponseWriter, r *http.Request) {
	m := h.oauth2.Service.Metadata()
	issuer, _ := url.Parse(m.Issuer)
	endpoint := func(path string) string { u := *issuer; u.Path = path; u.RawPath = ""; return u.String() }
	_ = web.RespondJSONOK(w, map[string]any{
		"issuer":                                         m.Issuer,
		"authorization_endpoint":                         endpoint("/auth/oauth2/authorize"),
		"token_endpoint":                                 endpoint("/auth/oauth2/token"),
		"revocation_endpoint":                            endpoint("/auth/oauth2/revoke"),
		"introspection_endpoint":                         endpoint("/auth/oauth2/introspect"),
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{oauth2.GrantAuthorizationCode, oauth2.GrantRefreshToken, oauth2.GrantTokenExchange},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_basic"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"introspection_endpoint_auth_methods_supported":  []string{"client_secret_basic"},
		"code_challenge_methods_supported":               []string{"S256"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

func (h *handlers) oauth2AuthorizePage(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) > 16<<10 {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	form, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !singleOAuth2Values(form) {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	if (form.Get("response_mode") != "" && form.Get("response_mode") != "query") || form.Get("request") != "" || form.Get("request_uri") != "" || form.Get("authorization_details") != "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	if form.Get("audience") != "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidTarget)
		return
	}
	if !h.oauth2Limit(w, r, "authorize", form.Get("client_id"), 60) {
		return
	}
	userID, sessionID, ok := h.formPrincipal(w, r)
	if !ok {
		return
	}
	approved, err := h.oauth2.Service.PrepareAuthorization(r.Context(), oauth2.AuthorizationRequest{
		ResponseType: form.Get("response_type"), ClientID: form.Get("client_id"), RedirectURI: form.Get("redirect_uri"), Resource: form.Get("resource"), CodeChallenge: form.Get("code_challenge"), CodeChallengeMethod: form.Get("code_challenge_method"), State: form.Get("state"), Scope: form.Get("scope"),
	}, userID, sessionID)
	if err != nil {
		writeOAuth2Error(w, r, err)
		return
	}
	pc := h.newPageContext(w)
	pc.Actor = approved.DisplayName
	if pc.Actor == "" {
		pc.Actor = approved.UserID
	}
	clientHost := oauth2Hostname(approved.Client.ID)
	redirectHost := oauth2Hostname(approved.RedirectURI)
	if !writeOAuth2ConsentSecurity(w, h.htmlPolicy, pc.CSPNonce, approved.RedirectURI) {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	web.Render(r.Context(), w, http.StatusOK, h.oauth2.Views.OAuthConsent(OAuthConsentPage{
		PageContext: pc, ClientName: approved.Client.Name, ClientID: approved.Client.ID, ClientHost: clientHost, RedirectHost: redirectHost, Resource: approved.Resource, RequestToken: approved.ConsentToken, CapabilityDescription: h.oauth2.CapabilityDescription,
	}))
}

func (h *handlers) oauth2Authorize(w http.ResponseWriter, r *http.Request) {
	form, ok := readOAuth2Form(w, r)
	if !ok {
		return
	}
	csrf, err := r.Cookie(csrfCookieName)
	if err != nil || csrf.Value == "" || form.Get("csrf_token") == "" || subtle.ConstantTimeCompare([]byte(csrf.Value), []byte(form.Get("csrf_token"))) != 1 {
		writeOAuth2Failure(w, http.StatusForbidden, "access_denied")
		return
	}
	userID, sessionID, ok := h.formPrincipal(w, r)
	if !ok {
		return
	}
	if !h.oauth2Limit(w, r, "consent", "user:"+userID, 60) {
		return
	}
	var result oauth2.AuthorizationResult
	switch form.Get("decision") {
	case "allow":
		result, err = h.oauth2.Service.Approve(r.Context(), form.Get("request"), userID, sessionID)
	case "deny":
		result, err = h.oauth2.Service.Deny(r.Context(), form.Get("request"), userID, sessionID)
	default:
		err = oauth2.ErrInvalidRequest
	}
	if err != nil {
		writeOAuth2Error(w, r, err)
		return
	}
	dest, err := url.Parse(result.RedirectURI)
	if err != nil || dest.Host == "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	query := dest.Query()
	if result.Code != "" {
		query.Set("code", result.Code)
	} else {
		query.Set("error", result.Error)
	}
	if result.State != "" {
		query.Set("state", result.State)
	}
	query.Set("iss", h.oauth2.Service.Metadata().Issuer)
	dest.RawQuery = query.Encode()
	if !writeOAuth2ConsentSecurity(w, h.htmlPolicy, "", result.RedirectURI) {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	http.Redirect(w, r, dest.String(), http.StatusSeeOther)
}

func (h *handlers) oauth2Token(w http.ResponseWriter, r *http.Request) {
	form, ok := readOAuth2Form(w, r)
	if !ok {
		return
	}
	if !h.oauth2Limit(w, r, "token", form.Get("client_id"), 600) {
		return
	}
	var response oauth2.TokenResponse
	var err error
	switch form.Get("grant_type") {
	case oauth2.GrantAuthorizationCode:
		if !publicOAuth2Client(w, r, form) {
			return
		}
		if !oauth2PublicResource(w, r, form) {
			return
		}
		response, err = h.oauth2.Service.RedeemCode(r.Context(), oauth2.CodeRequest{Code: form.Get("code"), ClientID: form.Get("client_id"), RedirectURI: form.Get("redirect_uri"), Resource: form.Get("resource"), CodeVerifier: form.Get("code_verifier")})
	case oauth2.GrantRefreshToken:
		if !publicOAuth2Client(w, r, form) {
			return
		}
		if !oauth2PublicResource(w, r, form) {
			return
		}
		response, err = h.oauth2.Service.Refresh(r.Context(), oauth2.RefreshRequest{RefreshToken: form.Get("refresh_token"), ClientID: form.Get("client_id"), Resource: form.Get("resource")})
	case oauth2.GrantTokenExchange:
		proof, ok := h.oauth2ClientProof(w, r, form)
		if !ok {
			return
		}
		if form.Get("actor_token_type") != "" {
			writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
			return
		}
		response, err = h.oauth2.Service.Exchange(r.Context(), proof, oauth2.ExchangeRequest{
			SubjectToken: form.Get("subject_token"), SubjectTokenType: form.Get("subject_token_type"), RequestedTokenType: form.Get("requested_token_type"), Resource: form.Get("resource"), Scope: form.Get("scope"), Audience: form.Get("audience"), ActorToken: form.Get("actor_token"),
		})
	default:
		writeOAuth2Failure(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		writeOAuth2Error(w, r, err)
		return
	}
	_ = web.RespondJSONOK(w, response)
}

func oauth2PublicResource(w http.ResponseWriter, r *http.Request, form url.Values) bool {
	if form.Get("scope") != "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidScope)
		return false
	}
	if form.Get("audience") != "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidTarget)
		return false
	}
	return true
}

func (h *handlers) oauth2Revoke(w http.ResponseWriter, r *http.Request) {
	form, ok := readOAuth2Form(w, r)
	if !ok {
		return
	}
	if !h.oauth2Limit(w, r, "revoke", form.Get("client_id"), 60) {
		return
	}
	if !publicOAuth2Client(w, r, form) {
		return
	}
	if form.Get("token") == "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	if err := h.oauth2.Service.Revoke(r.Context(), oauth2.RevokeRequest{Token: form.Get("token"), ClientID: form.Get("client_id"), TokenTypeHint: form.Get("token_type_hint")}); err != nil {
		writeOAuth2Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *handlers) oauth2Introspect(w http.ResponseWriter, r *http.Request) {
	form, ok := readOAuth2Form(w, r)
	if !ok {
		return
	}
	if !h.oauth2Limit(w, r, "introspect", "", 6000) {
		return
	}
	proof, ok := h.oauth2ClientProof(w, r, form)
	if !ok {
		return
	}
	if form.Get("token") == "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return
	}
	response, err := h.oauth2.Service.Introspect(r.Context(), proof, form.Get("token"))
	if err != nil {
		writeOAuth2Error(w, r, err)
		return
	}
	_ = web.RespondJSONOK(w, response)
}

func readOAuth2Form(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || r.URL.RawQuery != "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuth2BodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeOAuth2Failure(w, http.StatusRequestEntityTooLarge, "invalid_request")
		} else {
			writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		}
		return nil, false
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || !singleOAuth2Values(form) {
		writeOAuth2Error(w, r, oauth2.ErrInvalidRequest)
		return nil, false
	}
	return form, true
}

func singleOAuth2Values(form url.Values) bool {
	for _, values := range form {
		if len(values) != 1 {
			return false
		}
	}
	return true
}

func publicOAuth2Client(w http.ResponseWriter, r *http.Request, form url.Values) bool {
	_, secret := form["client_secret"]
	_, assertion := form["client_assertion"]
	_, assertionType := form["client_assertion_type"]
	if len(r.Header.Values("Authorization")) != 0 || secret || assertion || assertionType || form.Get("client_id") == "" {
		writeOAuth2Error(w, r, oauth2.ErrInvalidClient)
		return false
	}
	return true
}

func (h *handlers) oauth2ClientProof(w http.ResponseWriter, r *http.Request, form url.Values) (oauth2.ClientProof, bool) {
	reject := func() (oauth2.ClientProof, bool) {
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth2"`)
		writeOAuth2Failure(w, http.StatusUnauthorized, "invalid_client")
		return oauth2.ClientProof{}, false
	}
	_, bodyID := form["client_id"]
	_, bodySecret := form["client_secret"]
	_, assertion := form["client_assertion"]
	_, assertionType := form["client_assertion_type"]
	if bodyID || bodySecret || assertion || assertionType || len(r.Header.Values("Authorization")) != 1 {
		return reject()
	}
	clientID, secret, ok := r.BasicAuth()
	if !ok {
		return reject()
	}
	clientID, err := url.QueryUnescape(clientID)
	if err != nil {
		return reject()
	}
	secret, err = url.QueryUnescape(secret)
	if err != nil {
		return reject()
	}
	proof, err := h.oauth2.Service.AuthenticateClient(clientID, secret)
	if err != nil {
		return reject()
	}
	if !h.oauth2LimitClient(w, r, "confidential", clientID, 60000) {
		return oauth2.ClientProof{}, false
	}
	return proof, true
}

func (h *handlers) oauth2Limit(w http.ResponseWriter, r *http.Request, endpoint, clientID string, perMinute int) bool {
	if !h.oauth2LimitClient(w, r, endpoint+":ip", clientIPFromContext(r.Context()), perMinute) {
		return false
	}
	return clientID == "" || h.oauth2LimitClient(w, r, endpoint+":client", clientID, perMinute*10)
}

func (h *handlers) oauth2LimitClient(w http.ResponseWriter, r *http.Request, scope, key string, perMinute int) bool {
	digest := sha256.Sum256([]byte(key))
	result, err := h.oauth2.Limiter.Allow(r.Context(), "oauth2:"+scope+":"+hex.EncodeToString(digest[:]), ratelimiter.PerMinute(perMinute))
	if err != nil {
		web.RecordError(w, err)
		writeOAuth2Failure(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return false
	}
	if !result.Allowed {
		retry := max(int64(1), int64((result.RetryAfter+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.FormatInt(retry, 10))
		writeOAuth2Failure(w, http.StatusTooManyRequests, "temporarily_unavailable")
		return false
	}
	return true
}

func writeOAuth2Error(w http.ResponseWriter, r *http.Request, err error) {
	code, status := "server_error", http.StatusInternalServerError
	switch {
	case errors.Is(err, oauth2.ErrInvalidRequest):
		code, status = "invalid_request", http.StatusBadRequest
	case errors.Is(err, oauth2.ErrInvalidGrant), errors.Is(err, oauth2.ErrRefreshReuse):
		code, status = "invalid_grant", http.StatusBadRequest
	case errors.Is(err, oauth2.ErrInvalidClient):
		code, status = "invalid_client", http.StatusBadRequest
		if r.Header.Get("Authorization") != "" {
			status = http.StatusUnauthorized
			w.Header().Set("WWW-Authenticate", `Basic realm="oauth2"`)
		}
	case errors.Is(err, oauth2.ErrInvalidScope):
		code, status = "invalid_scope", http.StatusBadRequest
	case errors.Is(err, oauth2.ErrInvalidTarget):
		code, status = "invalid_target", http.StatusBadRequest
	case errors.Is(err, oauth2.ErrAccessDenied):
		code, status = "access_denied", http.StatusForbidden
	default:
		web.RecordError(w, err)
	}
	writeOAuth2Failure(w, status, code)
}

func writeOAuth2Failure(w http.ResponseWriter, status int, code string) {
	_ = web.RespondJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: code})
}

func oauth2Hostname(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Browsers can apply form-action to the redirect following the consent POST.
// Only this validated OAuth response may widen the otherwise self-only policy.
// An origin is used because CSP ignores path restrictions after a redirect.
func writeOAuth2ConsentSecurity(w http.ResponseWriter, policy *HTMLResourcePolicy, nonce, redirectURI string) bool {
	u, err := url.Parse(redirectURI)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return false
	}
	// CSP source expressions are not quoted URLs. Require an ASCII hostname
	// (IDNs use their canonical punycode form), never wildcard or directive text.
	for _, c := range u.Host {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune(".-:[]", c) {
			continue
		}
		return false
	}
	writeHTMLSecurity(w, policy, nonce)
	csp := w.Header().Get("Content-Security-Policy")
	w.Header().Set("Content-Security-Policy", strings.Replace(csp, "form-action 'self';", "form-action 'self' "+u.Scheme+"://"+u.Host+";", 1))
	return true
}
