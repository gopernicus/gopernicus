package goth

import (
	"strings"
	"testing"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
)

func TestOAuthConsentBindsDecisionAndEscapesClientMetadata(t *testing.T) {
	body := render(t, newViews(t).OAuthConsent(inbound.OAuthConsentPage{
		CapabilityDescription: "This app can use the tools exposed by this service with your current account permissions. This includes reading data and making changes where your account permits them. Changes are attributed to your account.",
		PageContext:           inbound.PageContext{CSRFToken: "csrf-proof", Actor: "a•••@example.com"},
		ClientName:            `Claude <script>alert("client")</script>`, ClientID: "https://claude.example/metadata", ClientHost: "claude.example", RedirectHost: "return.claude.example", Resource: "https://mcp.example/tools", RequestToken: `signed-request" onclick="evil`,
	}))
	mustContain(t, "OAuth consent", body,
		`action="/auth/oauth2/authorize"`, `method="post"`, `name="csrf_token" value="csrf-proof"`,
		`name="request" value="signed-request&#34; onclick=&#34;evil"`,
		`name="decision"`, `value="allow"`, `value="deny"`,
		"Allow connection", "Deny", "Signed in as a•••@example.com.", "claude.example", "return.claude.example", "https://mcp.example/tools",
		"current account permissions", "reading data and making changes", "separate app connection", "Signing out of the website keeps this connection active",
		"&lt;script&gt;", `<meta name="referrer" content="no-referrer">`,
	)
	mustNotContain(t, "OAuth consent", body, `<script>alert`, `onclick="evil"`, `href="https://claude.example`, "?request=", "?token=")
	if strings.Count(body, "<h1 ") != 1 {
		t.Fatal("consent needs one top-level heading")
	}
}

func TestSessionsSeparateWebLogoutAppDisconnectAndGlobalRevoke(t *testing.T) {
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.UTC)
	body := render(t, newViews(t).Sessions(inbound.SessionsPage{
		PageContext: inbound.PageContext{CSRFToken: "csrf-proof", Actor: "a•••@example.com"},
		Sessions: []inbound.SessionView{
			{ID: "browser-current", Profile: "first_party", Current: true, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
			{ID: "browser-other", Profile: "first_party", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
			{ID: "app/a?b#c", Profile: "delegated", ClientID: "https://claude.example/metadata", ClientName: "Claude <desktop>", Resource: "https://mcp.example", CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour)},
		}, NextURL: "/auth/sessions?limit=20&cursor=next",
	}))
	mustContain(t, "Sessions", body,
		`aria-labelledby="web-sessions-heading"`, `aria-labelledby="connected-apps-heading"`,
		`action="/auth/sessions/browser-current/revoke"`, `action="/auth/sessions/browser-other/revoke"`, `action="/auth/sessions/app%2Fa%3Fb%23c/revoke"`,
		"Current web session", "Sign out this browser", "Sign out session", "Disconnect app", "Claude &lt;desktop&gt;",
		`action="/auth/sessions/revoke-all"`, "Sign out everywhere, including connected apps", "disconnects every app",
		"Your web sessions and other app connections keep working", `datetime="2026-09-18T15:00:00Z"`, "Sep 18, 2026 at 15:00 UTC",
		`href="/auth/sessions?limit=20&amp;cursor=next"`, `href="/auth/account"`,
	)
	if strings.Count(body, `name="csrf_token" value="csrf-proof"`) != 4 {
		t.Fatal("each revoke form must have CSRF proof")
	}
	if strings.Count(body, `method="post"`) != 4 {
		t.Fatal("revocation must use POST")
	}
	start := strings.Index(body, `id="connected-apps-heading"`)
	if start < 0 || strings.Contains(body[:start], "Claude &lt;desktop&gt;") {
		t.Fatal("app connection leaked into web-session group")
	}
	mustNotContain(t, "Sessions", body, `<desktop>`, `action="/auth/logout"`)
}

func TestSessionsEmptyPageAndUnknownClientName(t *testing.T) {
	v := newViews(t)
	body := render(t, v.Sessions(inbound.SessionsPage{}))
	mustContain(t, "empty sessions", body, "No web sessions on this page.", "No connected apps on this page.")
	mustNotContain(t, "empty sessions", body, "More sessions")
	body = render(t, v.Sessions(inbound.SessionsPage{Sessions: []inbound.SessionView{{ID: "connection", Profile: "delegated", ClientID: "https://trusted.example/client.json"}}}))
	mustContain(t, "client fallback", body, "trusted.example")
}

func TestAccountSessionManagementLinkOnlyWhenEnabled(t *testing.T) {
	v := newViews(t)
	body := render(t, v.AccountSecurity(inbound.AccountSecurityPage{}))
	mustNotContain(t, "disabled session management", body, `href="/auth/sessions"`)
	body = render(t, v.AccountSecurity(inbound.AccountSecurityPage{SessionsEnabled: true}))
	mustContain(t, "enabled session management", body, `href="/auth/sessions"`, "Signing out of this browser keeps connected apps active.")
}

func TestOAuthConsentUsesTrustedHostCapabilities(t *testing.T) {
	body := render(t, newViews(t).OAuthConsent(inbound.OAuthConsentPage{CapabilityDescription: "Read project summaries only. <No edits>."}))
	mustContain(t, "host capabilities", body, "Read project summaries only. &lt;No edits&gt;.")
	mustNotContain(t, "host capabilities", body, "reading data and making changes", "<No edits>")
}
