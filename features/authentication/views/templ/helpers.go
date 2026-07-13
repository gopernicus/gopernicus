package templ

import (
	"net/http"
	"strconv"
	"strings"
)

// identifierLabel returns the field label for a passwordless/identifier input,
// chosen by kind.
func identifierLabel(kind string) string {
	if kind == "phone" {
		return "Phone number"
	}
	return "Email"
}

// identifierInputType returns the HTML input type for an identifier field.
func identifierInputType(kind string) string {
	if kind == "phone" {
		return "tel"
	}
	return "email"
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
