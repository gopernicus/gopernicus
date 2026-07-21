package authentication

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// The optional HTML surface (design §9.2, task AV3-8.1). mountHTML is called by
// Mount ONLY when a Views port is wired (Config.Views != nil); a nil Views leaves
// the whole HTML GET inventory unregistered while the JSON API stays mounted. This
// task lands the route inventory and its Views gate; the per-page handler bodies are
// deliberately thin here — the dual JSON/form POST dispatch and the rich page models
// land in AV3-8.3 (public pages) and AV3-8.4 (account-security pages), which fill
// these same handlers in without re-registering any route (no duplicate POST
// registration — the phase-8 stop condition).
//
// Route inventory, all inside the claimed /auth/* namespace:
//
//   - public credential pages (unauthenticated): GET /auth/login, /auth/register,
//     /auth/verify, /auth/password/forgot, /auth/password/reset;
//   - passwordless pages (only when PasswordlessEnabled — deny-by-absence mirrors the
//     JSON routes): GET /auth/passwordless, /auth/passwordless/code,
//     /auth/passwordless/check, /auth/magic;
//   - account-security pages (RequireLiveSession — masked inventory + management
//     forms): GET /auth/account, /auth/identifiers/new, /auth/identifiers/confirm,
//     /auth/identifiers/{id}/edit, /auth/password/set, /auth/password/change,
//     /auth/password/remove, /auth/step-up;
//   - OAuth unlink confirmation (only when OAuthEnabled): GET
//     /auth/oauth/{provider}/unlink.
//
// Every HTML GET path is distinct from its JSON POST twin (method+path), so no route
// collides — the POST endpoints keep their single JSON registration.
func mountHTML(r feature.RouteRegistrar, h *handlers, browserSafe web.Middleware) {
	// Public credential pages.
	r.Handle("GET", "/auth/login", h.loginPage)
	r.Handle("GET", "/auth/register", h.registerPage)
	r.Handle("GET", "/auth/verify", h.verifyPage)
	r.Handle("GET", "/auth/password/forgot", h.forgotPage)
	r.Handle("GET", "/auth/password/reset", h.resetPage)

	// Passwordless pages, only when the subsystem is enabled (deny-by-absence).
	if h.svc.PasswordlessEnabled() {
		r.Handle("GET", "/auth/passwordless", h.passwordlessStartPage)
		r.Handle("GET", "/auth/passwordless/code", h.passwordlessCodePage)
		r.Handle("GET", "/auth/passwordless/check", h.checkDeliveryPage)
		r.Handle("GET", "/auth/magic", h.magicLinkPage)
	}

	// Account-security GET pages are live-session gated (immediate revocation), like
	// their JSON twins — but a browser denial redirects to login (htmlLiveSession)
	// rather than leaking a JSON 401 (design §9.2). The management form POSTs keep
	// their JSON route registrations (routes.go / oauth.go) and the dispatcher there
	// selects the form arm; only the HTML-form-exclusive identifier edit needs a new
	// POST route below.
	live := h.htmlLiveSession
	r.Handle("GET", "/auth/account", h.accountPage, live)
	r.Handle("GET", "/auth/identifiers/new", h.identifierNewPage, live)
	r.Handle("GET", "/auth/identifiers/confirm", h.identifierConfirmPage, live)
	r.Handle("GET", "/auth/identifiers/{id}/edit", h.identifierEditPage, live)
	r.Handle("GET", "/auth/password/set", h.passwordSetPage, live)
	r.Handle("GET", "/auth/password/change", h.passwordChangePage, live)
	r.Handle("GET", "/auth/password/remove", h.passwordRemovePage, live)
	r.Handle("GET", "/auth/step-up", h.stepUpPage, live)

	// An HTML form cannot emit PATCH/DELETE, so the identifier edit form POSTs
	// /auth/identifiers/{id} with an action field; the handler routes it to the same
	// SetIdentifierUses / RemoveIdentifier service methods the JSON PATCH/DELETE edges
	// use. This POST path is form-only (no JSON twin), so it is a single registration
	// carrying the live-session + browser-safe (Origin + deferred form-CSRF) gates.
	r.Handle("POST", "/auth/identifiers/{id}", h.identifierEditForm, h.svc.RequireLiveSession, browserSafe)

	// OAuth unlink confirmation page, only when at least one provider is wired.
	if h.svc.OAuthEnabled() {
		r.Handle("GET", "/auth/oauth/{provider}/unlink", h.oauthUnlinkPage, live)
	}
}

// renderPage writes a rendered page as a 200 HTML response under the full HTML
// security-header policy (design §9.1/§9.2): no-store, no-referrer, frame and
// content-type protections, and a restrictive CSP whose script-src permits exactly
// the page's per-render nonce. It is the single SSR seam for the auth HTML surface:
// every GET handler builds its page context and model, then renders through the
// injected Views port. nonce is the model's CSP nonce (empty → the fail-safe
// no-script CSP).
func (h *handlers) renderPage(w http.ResponseWriter, r *http.Request, nonce string, page web.Renderer) {
	writeHTMLSecurity(w, h.htmlPolicy, nonce)
	web.Render(r.Context(), w, http.StatusOK, page)
}

// newPageContext builds the shared per-render context: a fresh double-submit CSRF
// token (also set as the auth_csrf cookie so a form POST can double-submit it) and a
// per-render CSP nonce. Populating the return-to, message, actor, and field-error
// fields is left to the individual handlers (and enriched in AV3-8.3/8.4).
func (h *handlers) newPageContext(w http.ResponseWriter) PageContext {
	// issueCSRFToken sets the auth_csrf cookie and returns the token; a form on the
	// page echoes it in a hidden field for the browser-safe-mutation gate. A failure
	// to source randomness leaves the token empty rather than failing the render.
	token, _ := issueCSRFToken(w)
	return PageContext{CSRFToken: token, CSPNonce: newNonce()}
}

// newNonce returns a base64 CSP nonce for a page's inline script. On a randomness
// failure it returns an empty string; the template then renders no nonced script and
// falls back to its no-script path.
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// ---------------------------------------------------------------------------
// Public credential pages (unauthenticated). AV3-8.3 enriches these with the
// dual-transport POST dispatch, PRG, and full page models.
// ---------------------------------------------------------------------------

func (h *handlers) loginPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	m := LoginPage{
		PageContext:         pc,
		Email:               r.URL.Query().Get("email"),
		PasswordlessEnabled: h.svc.PasswordlessEnabled(),
		OAuthProviders:      h.svc.OAuthProviderNames(),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.Login(m))
}

func (h *handlers) registerPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	m := RegisterPage{
		PageContext: pc,
		Email:       r.URL.Query().Get("email"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.Register(m))
}

func (h *handlers) verifyPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	m := VerifyPage{
		PageContext: pc,
		Email:       r.URL.Query().Get("email"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.Verify(m))
}

// forgotPage renders the forgot-password start form. A ?sent=1 marker (the PRG
// landing after a form POST) shows a generic, enumeration-resistant notice — the
// same copy whether or not the address has an account.
func (h *handlers) forgotPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	if r.URL.Query().Get("sent") == "1" {
		pc.Message = "If that address has an account, we've sent a password reset link. Check your email."
	}
	m := ForgotPage{
		PageContext: pc,
		Email:       r.URL.Query().Get("email"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.ForgotPassword(m))
}

func (h *handlers) resetPage(w http.ResponseWriter, r *http.Request) {
	// The reset token rides the URL fragment, read client-side; it is never a
	// server-rendered value (design §6.4). renderPage sets no-referrer for every page.
	pc := h.newPageContext(w)
	m := ResetPage{
		PageContext: pc,
		RedeemPath:  "/auth/password/reset",
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.ResetPassword(m))
}

// ---------------------------------------------------------------------------
// Passwordless pages (unauthenticated).
// ---------------------------------------------------------------------------

func (h *handlers) passwordlessStartPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	m := PasswordlessStartPage{
		PageContext: pc,
		Kind:        r.URL.Query().Get("kind"),
		Kinds:       h.svc.PasswordlessKinds(),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.PasswordlessStart(m))
}

func (h *handlers) passwordlessCodePage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	m := PasswordlessCodePage{
		PageContext: pc,
		Kind:        r.URL.Query().Get("kind"),
		Identifier:  r.URL.Query().Get("identifier"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.PasswordlessCode(m))
}

func (h *handlers) checkDeliveryPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	pc.Message = "If that address can receive it, we've sent your sign-in link or code. Check your messages."
	m := CheckDeliveryPage{
		PageContext: pc,
		Kind:        r.URL.Query().Get("kind"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.CheckDelivery(m))
}

func (h *handlers) magicLinkPage(w http.ResponseWriter, r *http.Request) {
	// The magic-link token rides the URL fragment, read and scrubbed client-side; it
	// is never a server-rendered value (design §6.4). renderPage sets no-referrer and
	// the nonced-script CSP for the fragment reader.
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	m := MagicLinkPage{
		PageContext: pc,
		RedeemPath:  "/auth/passwordless/redeem",
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.MagicLinkLanding(m))
}

// ---------------------------------------------------------------------------
// Account-security pages (live-session gated). AV3-8.4 enriches these with the
// management form POST handlers and recent-auth gating.
// ---------------------------------------------------------------------------

// accountPage renders the masked method inventory (design §5.1). RequireLiveSession
// has already validated the session and stashed the user id.
func (h *handlers) accountPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		h.renderError(w, r, http.StatusUnauthorized, "Please sign in to continue.")
		return
	}
	view, err := h.svc.Methods(r.Context(), userID)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, "Something went wrong. Please try again.")
		return
	}
	pc := h.newPageContext(w)
	pc.Actor = maskedPrimary(view)
	m := AccountSecurityPage{
		PageContext: pc,
		HasPassword: view.HasPassword,
		OAuth:       make([]OAuthMethod, 0, len(view.OAuth)),
		Identifiers: make([]IdentifierMethod, 0, len(view.Identifiers)),
	}
	for _, o := range view.OAuth {
		entry := OAuthMethod{Provider: o.Provider, Assurance: o.Assurance, Removable: o.Removable}
		if !o.LinkedAt.IsZero() {
			entry.LinkedAt = o.LinkedAt.Format(time.RFC3339)
		}
		m.OAuth = append(m.OAuth, entry)
	}
	for _, it := range view.Identifiers {
		entry := IdentifierMethod{
			ID:          it.ID,
			Kind:        it.Kind,
			MaskedValue: it.MaskedValue,
			Uses:        usesList(it.Uses.Login, it.Uses.Recovery, it.Uses.Notification),
			Primary:     it.Primary,
			Removable:   it.Removable,
		}
		if !it.VerifiedAt.IsZero() {
			entry.VerifiedAt = it.VerifiedAt.Format(time.RFC3339)
		}
		m.Identifiers = append(m.Identifiers, entry)
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.AccountSecurity(m))
}

func (h *handlers) identifierNewPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	m := IdentifierFormPage{
		PageContext: pc,
		Mode:        "add",
		Kind:        r.URL.Query().Get("kind"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.IdentifierForm(m))
}

// identifierConfirmPage renders the ownership-proof code entry form the add flow PRGs
// to (design §5.5): after StartIdentifierChange delivers a code to the proposed new
// address, the caller lands here to enter it. The code is delivered out-of-band and
// never a server-rendered value; the generic notice never distinguishes an unknown
// address. It posts to the same kind-specific confirm edge the JSON API uses.
func (h *handlers) identifierConfirmPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	pc.Message = "If that address can receive it, we've sent a code. Enter it to confirm the identifier."
	m := IdentifierFormPage{
		PageContext: pc,
		Mode:        "confirm",
		Kind:        r.URL.Query().Get("kind"),
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.IdentifierForm(m))
}

func (h *handlers) identifierEditPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	id := r.PathValue("id")
	m := IdentifierFormPage{
		PageContext: pc,
		Mode:        "edit",
		ID:          id,
	}
	if userID, ok := h.svc.CurrentUser(r.Context()); ok {
		h.populateIdentifierEdit(r.Context(), userID, id, &m)
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.IdentifierForm(m))
}

func (h *handlers) passwordSetPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	m := PasswordFormPage{PageContext: pc, Mode: "set"}
	h.renderPage(w, r, pc.CSPNonce, h.views.PasswordForm(m))
}

func (h *handlers) passwordChangePage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	m := PasswordFormPage{PageContext: pc, Mode: "change", ShowCurrentPassword: true}
	h.renderPage(w, r, pc.CSPNonce, h.views.PasswordForm(m))
}

func (h *handlers) passwordRemovePage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	if r.URL.Query().Get("sent") == "1" {
		pc.Message = "If a recovery address is on file, we've sent a code. Enter it to finish removing your password."
	}
	m := PasswordFormPage{PageContext: pc, Mode: "remove"}
	if userID, ok := h.svc.CurrentUser(r.Context()); ok {
		m.MaskedDestination = h.maskedRecovery(r.Context(), userID)
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.PasswordForm(m))
}

func (h *handlers) stepUpPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	q := r.URL.Query()
	pc.ReturnTo = h.safeReturnTo(q.Get("return_to"))
	if q.Get("sent") == "1" {
		pc.Message = "If a code was available, we've sent it. Enter it below to continue."
	}
	p := stepUpParams{
		purpose:   q.Get("purpose"),
		context:   q.Get("context"),
		operation: q.Get("operation"),
		returnTo:  q.Get("return_to"),
	}
	userID, _ := h.svc.CurrentUser(r.Context())
	m := h.stepUpModel(r.Context(), userID, pc, p)
	h.renderPage(w, r, pc.CSPNonce, h.views.StepUp(m))
}

func (h *handlers) oauthUnlinkPage(w http.ResponseWriter, r *http.Request) {
	pc := h.newPageContext(w)
	if r.URL.Query().Get("sent") == "1" {
		pc.Message = "If a recovery address is on file, we've sent a code. Enter it to finish unlinking."
	}
	m := OAuthUnlinkPage{
		PageContext: pc,
		Provider:    r.PathValue("provider"),
	}
	if userID, ok := h.svc.CurrentUser(r.Context()); ok {
		m.MaskedDestination = h.maskedRecovery(r.Context(), userID)
	}
	h.renderPage(w, r, pc.CSPNonce, h.views.OAuthUnlink(m))
}

// ---------------------------------------------------------------------------
// Live-session HTML gate + account-page model helpers
// ---------------------------------------------------------------------------

// htmlGate buffers a would-be denial write from the JSON RequireLiveSession gate so
// an HTML account page can convert it into a login redirect instead of leaking a JSON
// 401 body. Once the page handler runs (reached), writes pass straight through to the
// underlying writer.
type htmlGate struct {
	http.ResponseWriter
	reached bool
	denied  bool
}

func (g *htmlGate) WriteHeader(status int) {
	if !g.reached {
		g.denied = true
		return
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *htmlGate) Write(b []byte) (int, error) {
	if !g.reached {
		g.denied = true
		return len(b), nil
	}
	return g.ResponseWriter.Write(b)
}

// htmlLiveSession gates an HTML account page on a live session. Unlike the JSON
// RequireLiveSession (which writes a 401), a denied browser is redirected to the login
// page after validating a safe relative return-to (design §9.2). It wraps the service
// gate so revocation/liveness stays the service's decision; only the denial
// presentation changes — a stale or revoked session lands on login, never on a JSON
// error page.
func (h *handlers) htmlLiveSession(next http.Handler) http.Handler {
	marked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g, ok := w.(*htmlGate); ok {
			g.reached = true
		}
		next.ServeHTTP(w, r)
	})
	guarded := h.svc.RequireLiveSession(marked)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := &htmlGate{ResponseWriter: w}
		guarded.ServeHTTP(g, r)
		if g.denied {
			h.redirectToLogin(w, r)
		}
	})
}

// stepUpParams is the operation binding a step-up page carries between the sensitive
// action, the step-up page, and its completion (design §5.0). All fields are non-secret
// operation identifiers.
type stepUpParams struct {
	purpose   string
	context   string
	operation string
	returnTo  string
	sent      bool
}

// stepUpPath builds the /auth/step-up destination for a step-up redirect, carrying the
// operation binding and (validated) return-to as query params.
func stepUpPath(p stepUpParams) string {
	q := url.Values{}
	if p.purpose != "" {
		q.Set("purpose", p.purpose)
	}
	if p.context != "" {
		q.Set("context", p.context)
	}
	if p.operation != "" {
		q.Set("operation", p.operation)
	}
	if rt := safeRelativePath(p.returnTo); rt != "" {
		q.Set("return_to", rt)
	}
	if p.sent {
		q.Set("sent", "1")
	}
	dest := "/auth/step-up"
	if enc := q.Encode(); enc != "" {
		dest += "?" + enc
	}
	return dest
}

// stepUpModel builds the step-up page model, reporting only the completion methods the
// caller can actually use so the template offers viable ones (the safety decision stays
// the service's, design §5.0): password when the account has one, code when a verified
// recovery identifier exists. It never recomputes grant policy.
func (h *handlers) stepUpModel(ctx context.Context, userID string, pc PageContext, p stepUpParams) StepUpPage {
	m := StepUpPage{
		PageContext: pc,
		Operation:   p.operation,
		Purpose:     p.purpose,
		Context:     p.context,
	}
	if userID == "" {
		return m
	}
	view, err := h.svc.Methods(ctx, userID)
	if err != nil {
		return m
	}
	m.PasswordAvailable = view.HasPassword
	m.MaskedIdentifier = maskedRecovery(view)
	m.CodeAvailable = m.MaskedIdentifier != ""
	return m
}

// maskedRecovery returns the masked value of the caller's first verified
// recovery-enabled identifier (design §5.1), or "" when none exists. It never returns
// an unmasked address.
func (h *handlers) maskedRecovery(ctx context.Context, userID string) string {
	view, err := h.svc.Methods(ctx, userID)
	if err != nil {
		return ""
	}
	return maskedRecovery(view)
}

// maskedRecovery selects the masked recovery destination from an already-loaded
// inventory.
func maskedRecovery(view authsvc.MethodsView) string {
	for _, it := range view.Identifiers {
		if it.Uses.Recovery && !it.VerifiedAt.IsZero() {
			return it.MaskedValue
		}
	}
	return ""
}

// maskedPrimary returns the masked value of the caller's primary identifier (the
// signed-in actor label), falling back to the first identifier's masked value. Always
// masked, never the full address.
func maskedPrimary(view authsvc.MethodsView) string {
	if len(view.Identifiers) == 0 {
		return ""
	}
	for _, it := range view.Identifiers {
		if it.Primary {
			return it.MaskedValue
		}
	}
	return view.Identifiers[0].MaskedValue
}

// populateIdentifierEdit fills the edit-identifier form from the caller's masked
// inventory: the existing address is shown masked and its current uses/primary flag
// pre-select the controls. An id the caller does not own leaves the form blank.
func (h *handlers) populateIdentifierEdit(ctx context.Context, userID, id string, m *IdentifierFormPage) {
	view, err := h.svc.Methods(ctx, userID)
	if err != nil {
		return
	}
	for _, it := range view.Identifiers {
		if it.ID != id {
			continue
		}
		m.Kind = it.Kind
		m.MaskedValue = it.MaskedValue
		m.LoginEnabled = it.Uses.Login
		m.RecoveryEnabled = it.Uses.Recovery
		m.NotificationEnabled = it.Uses.Notification
		m.MakePrimary = it.Primary
		return
	}
}

// renderError renders the generic error page at the given status. The copy stays
// generic so it never distinguishes an unknown/unverified account (design §9.2).
func (h *handlers) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	pc := h.newPageContext(w)
	writeHTMLSecurity(w, h.htmlPolicy, pc.CSPNonce)
	web.Render(r.Context(), w, status, h.views.Error(ErrorPage{
		PageContext: pc,
		Status:      status,
		Message:     message,
	}))
}
