package authsvc

import (
	"net/url"
	"strings"
	"testing"
)

// oauth-pending-link plan — the anti-takeover pending-link email becomes a
// clickable link when a landing URL is configured, and degrades to the historical
// bare-token line when it is not (D5).

// TestOAuthLinkURL is the pure builder: the token rides the FRAGMENT, so it never
// appears in the query or path, existing non-secret query values survive, and an
// empty base or token yields no link (the D5 fallback signal).
func TestOAuthLinkURL(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		token string
		want  string
	}{
		{"plain path", "https://app.example.com/auth/oauth/link", "abc123", "https://app.example.com/auth/oauth/link#token=abc123"},
		{"trailing slash trimmed", "https://app.example.com/auth/oauth/link/", "abc123", "https://app.example.com/auth/oauth/link#token=abc123"},
		{"existing non-secret query preserved", "https://app.example.com/link?app=console", "abc123", "https://app.example.com/link?app=console#token=abc123"},
		{"token with URL-significant bytes is escaped", "https://app.example.com/link", "a+b/c=d&e f", "https://app.example.com/link#token=a%2Bb%2Fc%3Dd%26e+f"},
		{"empty base yields no link", "", "abc123", ""},
		{"empty token yields no link", "https://app.example.com/link", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{oauthLinkBase: tt.base}
			if got := s.oauthLinkURL(tt.token); got != tt.want {
				t.Fatalf("oauthLinkURL(%q) with base %q = %q, want %q", tt.token, tt.base, got, tt.want)
			}
		})
	}
}

// TestOAuthLinkURLKeepsTokenOutOfQueryAndPath pins the security property the
// fragment placement exists for: the token appears ONLY after the "#", never in the
// part of the URL a landing-page GET sends to the server.
func TestOAuthLinkURLKeepsTokenOutOfQueryAndPath(t *testing.T) {
	s := &Service{oauthLinkBase: "https://app.example.com/link?app=console"}
	const token = "secret-token-value"
	link := s.oauthLinkURL(token)
	hash := strings.Index(link, "#")
	if hash < 0 {
		t.Fatalf("oauthLinkURL = %q, want a fragment", link)
	}
	if strings.Contains(link[:hash], token) {
		t.Errorf("token leaked into the URL before the fragment: %q", link[:hash])
	}
	if !strings.Contains(link[hash:], url.QueryEscape(token)) {
		t.Errorf("token not carried in the fragment: %q", link[hash:])
	}
	// The parsed URL exposes no token in its query or path either.
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse %q: %v", link, err)
	}
	if u.Query().Has("token") || strings.Contains(u.Path, token) {
		t.Errorf("token reachable via query/path of %q", link)
	}
}

// TestPendingLinkEmailRendersConfiguredLink proves branch 2's mail becomes a
// clickable link (anchor + copy/paste URL) with the token in the fragment when a
// landing URL is configured, and does NOT render the bare-token line.
func TestPendingLinkEmailRendersConfiguredLink(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-link", email: "owner@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	h.svc.oauthLinkBase = "https://app.example.com/auth/oauth/link?app=console"

	h.mustOAuthUser(t, "owner@example.com")
	token := h.completePendingLink(t)

	html := h.mailer.last().HTML
	wantLink := h.svc.oauthLinkURL(token)
	if wantLink == "" {
		t.Fatal("configured base produced no link")
	}
	if !strings.Contains(html, `<a href="`+wantLink+`">`) {
		t.Errorf("mail missing clickable anchor for %q\n%s", wantLink, html)
	}
	if !strings.Contains(html, wantLink) {
		t.Errorf("mail missing copy/paste URL %q\n%s", wantLink, html)
	}
	// The link-only branch renders no standalone bare token.
	if strings.Contains(html, "<strong>"+token+"</strong>") {
		t.Errorf("configured mail still rendered the bare token line\n%s", html)
	}
}

// TestPendingLinkEmailFallsBackToTokenWhenUnconfigured proves the D5 default: with
// no landing URL the mail keeps the historical bare-token line and renders no empty
// or blank anchor.
func TestPendingLinkEmailFallsBackToTokenWhenUnconfigured(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-fb", email: "owner@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil) // oauthLinkBase left empty

	h.mustOAuthUser(t, "owner@example.com")
	token := h.completePendingLink(t)

	html := h.mailer.last().HTML
	if !strings.Contains(html, "<strong>"+token+"</strong>") {
		t.Errorf("fallback mail missing the bare-token line\n%s", html)
	}
	if strings.Contains(html, "<a href=") {
		t.Errorf("fallback mail rendered an anchor with no configured link\n%s", html)
	}
	if strings.Contains(html, `href=""`) || strings.Contains(html, "#token=") {
		t.Errorf("fallback mail rendered an empty/blank link\n%s", html)
	}
}
