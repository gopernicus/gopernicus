package authenticationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The passkey HTML pages and the outcome codes their PRG redirects carry.
const (
	passkeysPagePath      = "/auth/account/passkeys"
	passkeyStepUpPagePath = "/auth/step-up/passkey"

	outcomePasskeyAdded   = "passkey_added"
	outcomePasskeyRenamed = "passkey_renamed"
	outcomePasskeyRemoved = "passkey_removed"
)

// passkeyOutcomes is the manage page's closed outcome vocabulary.
var passkeyOutcomes = map[string]string{
	outcomePasskeyAdded:   "Your passkey was added.",
	outcomePasskeyRenamed: "That passkey was renamed.",
}

// PasskeyViews renders the passkey pages (authentication.PasskeyConfig.Views).
// It is separate from Views, as OAuthViews is, so the Views interface never
// grows: a host without passkey pages implements nothing new. Nil leaves the
// passkey routes JSON-only.
type PasskeyViews interface {
	// Passkeys is the manage page: the caller's passkeys, add, rename and
	// remove.
	Passkeys(m PasskeysPage) web.Renderer
	// PasskeyStepUp is the passkey step-up page a Browser() gate with
	// RequireMFA() redirects an AAL1 cookie session to.
	PasskeyStepUp(m PasskeyStepUpPage) web.Renderer
}

// PasskeyLinks carries every URL the passkey controls and their script use, so
// a template hard-codes none of them. Manage and StepUpPage are the HTML pages
// and are empty unless PasskeyViews is wired.
type PasskeyLinks struct {
	RegistrationOptions string
	Registration        string
	AssertionOptions    string
	Assertion           string
	StepUpOptions       string
	StepUp              string
	Manage              string
	StepUpPage          string
}

// PasskeyItem is one passkey on the manage page. Action is its form POST path:
// action=rename with a name, or action=remove. Removable is the credential
// policy's advisory hint; the service guard is authoritative.
type PasskeyItem struct {
	ID             string
	Name           string
	CreatedAt      string
	LastUsedAt     string
	UserVerified   bool
	BackupEligible bool
	BackupState    bool
	Transports     []string
	Removable      bool
	Action         string
}

// PasskeysPage models the manage page. ReturnTo (validated) is where the page
// sends the caller after adding a passkey — the step-up page's "Add a passkey"
// link sets it.
type PasskeysPage struct {
	PageContext
	Passkeys []PasskeyItem
	Links    PasskeyLinks
}

// PasskeyStepUpPage models the passkey step-up page. HasPasskeys reports any
// passkey; CanReachAAL2 an AAL2-capable one (registered with user
// verification). With none the page offers "Add a
// passkey" (Links.Manage, carrying ReturnTo); with presence-only passkeys it
// explains that they cannot confirm a PIN or biometric.
type PasskeyStepUpPage struct {
	PageContext
	Links        PasskeyLinks
	HasPasskeys  bool
	CanReachAAL2 bool
}

// passkeyLinks returns the page-model links, or nil when passkeys are off.
func (h *handlers) passkeyLinks() *PasskeyLinks {
	if h.passkeys == nil {
		return nil
	}
	links := &PasskeyLinks{
		RegistrationOptions: "/auth/passkeys/registration/options",
		Registration:        "/auth/passkeys/registration",
		AssertionOptions:    "/auth/passkeys/assertion/options",
		Assertion:           "/auth/passkeys/assertion",
		StepUpOptions:       "/auth/step-up/passkey/options",
		StepUp:              "/auth/step-up/passkey",
	}
	if h.passkeyViews != nil {
		links.Manage, links.StepUpPage = passkeysPagePath, passkeyStepUpPagePath
	}
	return links
}

// mountPasskeyHTML registers the passkey pages and the manage page's form
// lane. An HTML form cannot emit PATCH or DELETE, so the manage form POSTs
// /auth/passkeys/{id} with an action field.
func mountPasskeyHTML(r pockets.RouteRegistrar, h *handlers, browserAccount, browserSafeForm web.Middleware) {
	live := htmlSecured(h.htmlPolicy, browserAccount)
	r.Handle("GET", passkeysPagePath, h.passkeysPage, live)
	r.Handle("POST", "/auth/passkeys/{id}", h.passkeyForm, live, browserSafeForm)
	r.Handle("GET", passkeyStepUpPagePath, h.passkeyStepUpPage, live)
}

func (h *handlers) passkeysPage(w http.ResponseWriter, r *http.Request) {
	h.renderPasskeys(w, r, http.StatusOK, "")
}

// renderPasskeys renders the manage page at status with message, or the
// ?auth= outcome when message is empty.
func (h *handlers) renderPasskeys(w http.ResponseWriter, r *http.Request, status int, message string) {
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
	pc.ReturnTo = h.validatedReturnTo(r)
	pc.Message = message
	if message == "" {
		pc.Message = outcomeMessage(passkeyOutcomes, r.URL.Query().Get("auth"))
	}
	m := PasskeysPage{PageContext: pc, Links: *h.passkeyLinks(), Passkeys: make([]PasskeyItem, 0, len(view.Passkeys))}
	for _, p := range view.Passkeys {
		item := PasskeyItem{
			ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
			UserVerified: p.UserVerified, BackupEligible: p.BackupEligible, BackupState: p.BackupState,
			Transports: append([]string{}, p.Transports...), Removable: p.Removable,
			Action: "/auth/passkeys/" + url.PathEscape(p.ID),
		}
		if p.LastUsedAt != nil {
			item.LastUsedAt = p.LastUsedAt.UTC().Format(time.RFC3339)
		}
		m.Passkeys = append(m.Passkeys, item)
	}
	h.renderForm(w, r, status, pc.CSPNonce, h.passkeyViews.Passkeys(m))
}

// passkeyForm is the manage page's form lane. Rename PRGs back with its
// outcome. Remove needs a remove_passkey grant: a caller without one is sent to
// the passkey step-up page when they hold a user-verified passkey (AAL2 is the
// gate), else to the code or password step-up page. A completed removal has
// revoked every session, so it clears the cookies and lands on sign-in.
func (h *handlers) passkeyForm(w http.ResponseWriter, r *http.Request) {
	h.accountForm(w, r, func(form url.Values) {
		userID, sessionID, ok := h.formPrincipal(w, r)
		if !ok {
			return
		}
		id := web.Param(r, "id")
		switch form.Get("action") {
		case "rename":
			if err := h.passkeys.RenamePasskey(r.Context(), authlogic.PasskeyRenameInput{UserID: userID, PasskeyID: id, Name: form.Get("name")}); err != nil {
				status, msg := accountFailure(err)
				h.renderPasskeys(w, r, status, msg)
				return
			}
			h.prgTo(w, r, passkeysPagePath+"?auth="+outcomePasskeyRenamed)
		case "remove":
			err := h.passkeys.RemovePasskey(r.Context(), authlogic.PasskeyRemoveInput{SessionID: sessionID, UserID: userID, PasskeyID: id})
			switch {
			case errors.Is(err, authlogic.ErrStepUpRequired):
				if h.holdsVerifiedPasskey(r.Context(), userID) {
					h.prgTo(w, r, passkeyStepUpPagePath+"?return_to="+url.QueryEscape(passkeysPagePath))
					return
				}
				h.stepUpRedirect(w, r, authgrant.PurposeRemovePasskey, id, "remove a passkey", passkeysPagePath)
			case err != nil:
				status, msg := accountFailure(err)
				h.renderPasskeys(w, r, status, msg)
			default:
				h.svc.ClearSessionCookies(w)
				h.prgTo(w, r, loginDone(outcomePasskeyRemoved))
			}
		default:
			status, msg := accountFailure(sdk.ErrInvalidInput)
			h.renderPasskeys(w, r, status, msg)
		}
	})
}

// passkeyStepUpPage renders the passkey step-up page. Its return_to is honoured
// on GET and HEAD only, validated like the login page's.
func (h *handlers) passkeyStepUpPage(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.svc.CurrentUser(r.Context())
	if !ok {
		h.renderError(w, r, http.StatusUnauthorized, "Please sign in to continue.")
		return
	}
	pc := h.newPageContext(w)
	pc.ReturnTo = h.validatedReturnTo(r)
	m := PasskeyStepUpPage{PageContext: pc, Links: *h.passkeyLinks()}
	if passkeys, err := h.passkeys.ListPasskeys(r.Context(), userID); err == nil {
		for _, p := range passkeys {
			m.HasPasskeys = true
			m.CanReachAAL2 = m.CanReachAAL2 || p.AAL2Capable()
		}
	}
	h.renderPage(w, r, pc.CSPNonce, h.passkeyViews.PasskeyStepUp(m))
}

// holdsVerifiedPasskey reports whether userID holds an AAL2-capable passkey.
func (h *handlers) holdsVerifiedPasskey(ctx context.Context, userID string) bool {
	passkeys, err := h.passkeys.ListPasskeys(ctx, userID)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(passkeys, passkey.Passkey.AAL2Capable)
}
