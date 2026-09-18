package goth

import (
	"net/url"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var _ inbound.OAuthViews = Views{}

// OAuthConsent renders the independently revocable connection approval.
func (v Views) OAuthConsent(m inbound.OAuthConsentPage) web.Renderer {
	return v.page("Connect an app", nil, oauthConsentBody(m))
}

// Sessions renders web sessions and delegated connections with separate controls.
func (v Views) Sessions(m inbound.SessionsPage) web.Renderer {
	return v.page("Sessions and connected apps", nil, sessionsBody(m))
}

func consentClientName(m inbound.OAuthConsentPage) string {
	if m.ClientName != "" {
		return m.ClientName
	}
	if m.ClientHost != "" {
		return m.ClientHost
	}
	return m.ClientID
}

func sessionsByProfile(entries []inbound.SessionView, delegated bool) []inbound.SessionView {
	var out []inbound.SessionView
	for _, entry := range entries {
		if (entry.Profile == "delegated") == delegated {
			out = append(out, entry)
		}
	}
	return out
}

func sessionName(entry inbound.SessionView) string {
	if entry.Profile == "delegated" {
		if entry.ClientName != "" {
			return entry.ClientName
		}
		if u, err := url.Parse(entry.ClientID); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
		if entry.ClientID != "" {
			return entry.ClientID
		}
		return "Connected app"
	}
	if entry.Current {
		return "Current web session"
	}
	return "Web session"
}

func sessionRevokeAction(id string) string {
	return "/auth/sessions/" + url.PathEscape(id) + "/revoke"
}

func sessionDateTime(at time.Time) string  { return at.UTC().Format(time.RFC3339) }
func sessionDateLabel(at time.Time) string { return at.UTC().Format("Jan 2, 2006 at 15:04 UTC") }

func sessionRevokeLabel(entry inbound.SessionView) string {
	if entry.Profile == "delegated" {
		return "Disconnect " + sessionName(entry) + " started " + sessionDateLabel(entry.CreatedAt)
	}
	if entry.Current {
		return "Sign out this browser"
	}
	return "Sign out web session started " + sessionDateLabel(entry.CreatedAt)
}
