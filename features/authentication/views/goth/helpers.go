package goth

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/ui/goth/components/forms"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// identifierLabel returns the field label for a passwordless/identifier input,
// chosen by kind.
func identifierLabel(kind string) string {
	if kind == "phone" {
		return "Phone number"
	}
	return "Email"
}

// identifierInputType returns the primitives.InputType for an identifier field.
func identifierInputType(kind string) primitives.InputType {
	if kind == "phone" {
		return primitives.InputTel
	}
	return primitives.InputEmail
}

// identifierInputMode returns the inputmode hint for an identifier field.
func identifierInputMode(kind string) string {
	if kind == "phone" {
		return "tel"
	}
	return "email"
}

// identifierAutocomplete returns the autocomplete token for an identifier field:
// "tel" for phone, "email" otherwise (design AV3-8.2 autocomplete contract).
func identifierAutocomplete(kind string) string {
	if kind == "phone" {
		return "tel"
	}
	return "email"
}

// identifierFieldAttrs bundles the identifier input's inputmode/autocomplete hints
// and its aria-describedby wiring (at the FormField error region) into the single
// Base.Attributes map the primitive spreads.
func identifierFieldAttrs(kind, controlID string) templ.Attributes {
	return templ.Attributes{
		"inputmode":        identifierInputMode(kind),
		"autocomplete":     identifierAutocomplete(kind),
		"aria-describedby": forms.ErrorID(controlID),
	}
}

// codeFieldAttrs is the shared one-time-code input attribute set: numeric inputmode,
// the one-time-code autocomplete token, and the aria-describedby error wiring.
func codeFieldAttrs(controlID string) templ.Attributes {
	return templ.Attributes{
		"inputmode":        "numeric",
		"autocomplete":     "one-time-code",
		"aria-describedby": forms.ErrorID(controlID),
	}
}

// autocompleteAttrs is the input attribute set for a field that only needs an
// autocomplete token and its error wiring (the display-name/password fields).
func autocompleteAttrs(token, controlID string) templ.Attributes {
	a := templ.Attributes{"aria-describedby": forms.ErrorID(controlID)}
	if token != "" {
		a["autocomplete"] = token
	}
	return a
}

// emailFieldAttrs adds the email inputmode on top of the email autocomplete token.
func emailFieldAttrs(controlID string) templ.Attributes {
	a := autocompleteAttrs("email", controlID)
	a["inputmode"] = "email"
	return a
}

// identifierAddAction returns the POST target for an add-identifier form by kind.
func identifierAddAction(kind string) string {
	if kind == "phone" {
		return "/auth/identifiers/phone"
	}
	return "/auth/identifiers/email"
}

// identifierEditAction returns the POST target for an edit-identifier form. The
// canonical JSON edge is PATCH /auth/identifiers/{id}; the HTML form posts the
// same path and the dispatcher (AV3-8.4) routes it to the same service method.
func identifierEditAction(id string) string {
	return "/auth/identifiers/" + id
}

// confirmIdentifierAction returns the POST target for the ownership-proof code
// entry form by kind — the same kind-specific confirm edge the JSON API uses.
func confirmIdentifierAction(kind string) string {
	if kind == "phone" {
		return "/auth/identifiers/phone/confirm"
	}
	return "/auth/identifiers/email/confirm"
}

// oauthStartURL returns the provider's sign-in start URL as the primitives URL
// type the Button link form takes. Provider names are wiring constants; a parse
// failure renders an inert button rather than panicking.
func oauthStartURL(provider string) primitives.URL {
	u, err := primitives.ParseURL("/auth/oauth/" + provider + "/start")
	if err != nil {
		return primitives.URL{}
	}
	return u
}

// displayProviderName upper-cases a wired provider's first rune for the
// "Continue with X" label ("google" → "Google"). Provider names are short
// ASCII wiring constants; anything else passes through unchanged.
func displayProviderName(provider string) string {
	if provider == "" {
		return provider
	}
	r := []rune(provider)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// oauthUnlinkStartAction / oauthUnlinkAction return the provider-bound unlink
// POST targets.
func oauthUnlinkStartAction(provider string) string {
	return "/auth/oauth/" + provider + "/unlink/start"
}

func oauthUnlinkAction(provider string) string {
	return "/auth/oauth/" + provider + "/unlink"
}

// passwordFormAction returns the POST target for the set/change password form.
func passwordFormAction(mode string) string {
	if mode == "change" {
		return "/auth/password/change"
	}
	return "/auth/password/set"
}

// passwordFormTitle returns the page title for the set/change password form.
func passwordFormTitle(mode string) string {
	if mode == "change" {
		return "Change password"
	}
	return "Set a password"
}

// passwordPageTitle returns the document title for any password-form variant.
func passwordPageTitle(mode string) string {
	if mode == "remove" {
		return "Remove password"
	}
	return passwordFormTitle(mode)
}

// identifierFormTitle returns the document title for an identifier-form mode.
func identifierFormTitle(mode string) string {
	switch mode {
	case "edit":
		return "Manage identifier"
	case "confirm":
		return "Confirm your new identifier"
	default:
		return "Add an identifier"
	}
}

// usesText renders an identifier's stable role list as a human-readable string.
func usesText(uses []string) string {
	if len(uses) == 0 {
		return "—"
	}
	return strings.Join(uses, ", ")
}

// statusTitle returns the status-page title, defaulting when the model omits one.
func statusTitle(title string) string {
	if title == "" {
		return "Done"
	}
	return title
}

// statusText renders an HTTP status as "<code> <reason>" for the error page.
func statusText(status int) string {
	if t := http.StatusText(status); t != "" {
		return strconv.Itoa(status) + " " + t
	}
	return strconv.Itoa(status)
}

// fieldErrorText returns the message for the named field, or "" — used to flag the
// composed FormField invalid and render its error region.
func fieldErrorText(pc authentication.PageContext, field string) string {
	for _, fe := range pc.FieldErrors {
		if fe.Field == field {
			return fe.Message
		}
	}
	return ""
}

// fieldMessages projects the page context's field errors into the forms.ErrorSummary
// message shape (each an in-page link to its field control).
func fieldMessages(pc authentication.PageContext) []forms.FieldMessage {
	out := make([]forms.FieldMessage, 0, len(pc.FieldErrors))
	for _, fe := range pc.FieldErrors {
		out = append(out, forms.FieldMessage{Message: fe.Message, FieldID: fe.Field})
	}
	return out
}
