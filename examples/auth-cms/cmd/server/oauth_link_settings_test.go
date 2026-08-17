package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
)

// CHAU-7.2 — the settings-page account-linking proof.
//
// The coordination-hub upstream flag claimed authentication had "no
// user-initiated OAuth linking". The audit found the opposite: Service.StartLink
// and the session-gated GET /auth/oauth/{provider}/link/start have shipped since
// features/authentication/v0.1.0. What was missing was a runnable end-to-end
// recipe, so this file IS that recipe as much as it is a test.
//
// It is a HOST test in a separate module: everything below is reachable through
// exported API and real HTTP. Nothing here imports a feature-internal package, so
// a reader can copy the request sequence into a settings page directly.
//
// The wired provider is the host's own fakeOAuthProvider (oauthfake.go): no
// network, and a STABLE identity derived from the authorization code, so a code
// value doubles as "which provider account is this".

const (
	// linkProvider is the provider key in the /auth/oauth/{provider}/* routes.
	linkProvider = "fake"
	// settingsRedirect is the post-link destination a settings page would send,
	// added to the host's RedirectAllowlist below.
	settingsRedirect = "/settings/security"
	// hostileRedirect is an off-origin destination the allowlist must refuse.
	hostileRedirect = "https://evil.example/steal"

	linkPassword = "password123456789"
)

// sixDigitCode matches the challenge code the bundled/overridden email bodies
// render; auth codes are six-digit numeric (authsvc.generateCode).
var sixDigitCode = regexp.MustCompile(`\b\d{6}\b`)

// linkHost is one running host composition plus the mailer it delivers through.
type linkHost struct {
	t      *testing.T
	srv    *httptest.Server
	svc    *auth.Service
	sender *recordingSender
	origin string
}

// newLinkHost boots the host's real config in in_process delivery mode with a
// recording mailer, widens the redirect allowlist to the settings destination a
// real console would use, mounts the feature, and starts the delivery runtime.
func newLinkHost(t *testing.T) *linkHost {
	t.Helper()

	sender := &recordingSender{}
	svc := bootInProcess(t, sender, func(cfg *auth.Config) {
		// The host ships RedirectAllowlist{"/"}; a settings page needs its own
		// destination allowlisted. Anything not listed falls back to "/".
		cfg.RedirectAllowlist = []string{"/", settingsRedirect}
	})
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	origins := allowedOrigins()
	if len(origins) == 0 {
		t.Fatal("host has no allowed origins; the browser-safe mutation gate cannot pass")
	}
	return &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: origins[0]}
}

// linkClient is a browser stand-in: a real cookie jar, the host's allowlisted
// Origin on every request, and redirects NOT followed — an OAuth start answers
// 302 to the provider, and following it would leave the host.
type linkClient struct {
	t    *testing.T
	host *linkHost
	http *http.Client
}

func (h *linkHost) newClient() *linkClient {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatalf("cookiejar.New: %v", err)
	}
	return &linkClient{
		t:    h.t,
		host: h,
		http: &http.Client{
			Jar:           jar,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *linkClient) do(method, path, body string, header http.Header) (*http.Response, []byte) {
	c.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, c.host.srv.URL+path, rdr)
	if err != nil {
		c.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Origin", c.host.origin)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read %s %s body: %v", method, path, err)
	}
	return resp, payload
}

// signUp performs the full public onboarding a settings page assumes has already
// happened: register, verify the mailed code, log in. It returns the client
// holding the session cookies.
func (h *linkHost) signUp(email string) *linkClient {
	h.t.Helper()
	c := h.newClient()

	before := h.sender.count()
	if resp, body := c.do("POST", "/auth/register",
		`{"email":"`+email+`","password":"`+linkPassword+`","display_name":"Link Tester"}`, nil); resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("register %s = %d, want 201; body=%s", email, resp.StatusCode, body)
	}

	code := h.awaitCode(before, email)
	if resp, body := c.do("POST", "/auth/verify",
		`{"email":"`+email+`","code":"`+code+`"}`, nil); resp.StatusCode != http.StatusOK {
		h.t.Fatalf("verify %s = %d, want 200; body=%s", email, resp.StatusCode, body)
	}
	if resp, body := c.do("POST", "/auth/login",
		`{"email":"`+email+`","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		h.t.Fatalf("login %s = %d, want 200; body=%s", email, resp.StatusCode, body)
	}
	return c
}

// awaitCode waits for a message addressed to recipient that arrived after the
// first `before` sends, and returns the six-digit code in its body.
func (h *linkHost) awaitCode(before int, recipient string) string {
	h.t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs := h.sender.all()
		for i := len(msgs) - 1; i >= before; i-- {
			m := msgs[i]
			if !addressedTo(m.To, recipient) {
				continue
			}
			if code := sixDigitCode.FindString(m.Text + " " + m.HTML); code != "" {
				return code
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("no six-digit code delivered to %s within 5s (%d messages total)", recipient, h.sender.count())
	return ""
}

func addressedTo(to []string, recipient string) bool {
	for _, addr := range to {
		if strings.EqualFold(addr, recipient) {
			return true
		}
	}
	return false
}

// methodsInventory reads GET /auth/methods — the masked linked-method inventory a
// settings page renders. It is the same route that reports the result of a link.
type methodsInventory struct {
	HasPassword bool `json:"has_password"`
	OAuth       []struct {
		Provider  string `json:"provider"`
		Removable bool   `json:"removable"`
	} `json:"oauth"`
	Identifiers []struct {
		Value      string `json:"value"`
		VerifiedAt string `json:"verified_at"`
	} `json:"identifiers"`
}

func (c *linkClient) methods() methodsInventory {
	c.t.Helper()
	resp, body := c.do("GET", "/auth/methods", "", nil)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET /auth/methods = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var out methodsInventory
	if err := json.Unmarshal(body, &out); err != nil {
		c.t.Fatalf("decode /auth/methods %q: %v", body, err)
	}
	return out
}

func (m methodsInventory) linked(provider string) bool {
	for _, o := range m.OAuth {
		if o.Provider == provider {
			return true
		}
	}
	return false
}

// startLink drives the settings-page button: GET the session-gated link-start
// route and read the opaque state out of the provider redirect. A real browser
// would simply follow the 302.
func (c *linkClient) startLink(redirect string) string {
	c.t.Helper()

	path := "/auth/oauth/" + linkProvider + "/link/start"
	if redirect != "" {
		path += "?redirect=" + url.QueryEscape(redirect)
	}
	resp, body := c.do("GET", path, "", nil)
	if resp.StatusCode != http.StatusFound {
		c.t.Fatalf("link/start = %d, want 302; body=%s", resp.StatusCode, body)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		c.t.Fatalf("parse provider Location %q: %v", resp.Header.Get("Location"), err)
	}
	if loc.Host == "" || loc.Host == c.host.srv.URL {
		c.t.Fatalf("link/start Location %q is not a provider authorization URL", loc)
	}
	state := loc.Query().Get("state")
	if state == "" {
		c.t.Fatalf("link/start Location %q carries no state", loc)
	}
	// The state is opaque and server-side: the redirect target and the linking
	// user are NOT in the URL the browser carries.
	if strings.Contains(loc.RawQuery, settingsRedirect) {
		c.t.Errorf("provider URL leaks the redirect target: %q", loc.RawQuery)
	}
	return state
}

// callback replays the provider redirect back to the host.
func (c *linkClient) callback(code, state string) (*http.Response, []byte) {
	c.t.Helper()
	q := url.Values{"code": {code}, "state": {state}}
	return c.do("GET", "/auth/oauth/"+linkProvider+"/callback?"+q.Encode(), "", nil)
}

// sessionCookiesSet reports which auth cookies a response re-issued.
func (h *linkHost) sessionCookiesSet(resp *http.Response) []string {
	var names []string
	for _, ck := range resp.Cookies() {
		if ck.Name == h.svc.SessionCookieName() || ck.Name == h.svc.RefreshCookieName() {
			names = append(names, ck.Name)
		}
	}
	return names
}

// TestOAuthExplicitLinkFromSettingsPage is the end-to-end settings recipe:
// signed-in user → link/start → provider → callback → redirect → refreshed
// inventory, with the session untouched throughout.
func TestOAuthExplicitLinkFromSettingsPage(t *testing.T) {
	host := newLinkHost(t)
	const addr = "settings-link@example.com"
	c := host.signUp(addr)

	// 1. The settings page renders from the inventory: a password, a verified
	//    identifier, and no linked provider yet.
	before := c.methods()
	if !before.HasPassword {
		t.Error("inventory reports no password for a password-registered user")
	}
	if before.linked(linkProvider) {
		t.Fatalf("provider %q is already linked before the flow", linkProvider)
	}

	// 2. The "Connect" button: a session-gated GET that 302s to the provider.
	state := c.startLink(settingsRedirect)

	// 3. The provider redirects back. The identity is derived from the code, so
	//    this stands in for "the operator approved at the provider".
	resp, body := c.callback(addr, state)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want 302; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Location"); got != settingsRedirect {
		t.Errorf("callback Location = %q, want the allowlisted %q", got, settingsRedirect)
	}

	// 4. An explicit link does NOT mint or replace the caller's session: the user
	//    was already signed in, and re-issuing a cookie here would be a silent
	//    session swap. Only login/register callbacks set cookies.
	if names := host.sessionCookiesSet(resp); len(names) != 0 {
		t.Errorf("explicit link callback re-issued session cookies %v; it must not", names)
	}

	// 5. The settings page refreshes the same inventory route and sees the link.
	after := c.methods()
	if !after.linked(linkProvider) {
		t.Fatalf("provider %q missing from the inventory after linking: %+v", linkProvider, after.OAuth)
	}
	if !after.HasPassword {
		t.Error("linking removed the password from the inventory")
	}

	// 6. The state is single-use: a replayed callback cannot re-run the link.
	replay, replayBody := c.callback(addr, state)
	if replay.StatusCode == http.StatusFound {
		t.Errorf("replayed callback = 302, want a rejection; body=%s", replayBody)
	}
}

// TestOAuthLinkStartRequiresSession pins the gate: the link initiator is not a
// public route.
func TestOAuthLinkStartRequiresSession(t *testing.T) {
	host := newLinkHost(t)
	anon := host.newClient()

	resp, body := anon.do("GET", "/auth/oauth/"+linkProvider+"/link/start", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous link/start = %d, want 401; body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("Location") != "" {
		t.Error("anonymous link/start redirected to a provider; it must refuse first")
	}
}

// TestOAuthLinkStartUnknownProvider pins that only a WIRED provider is linkable.
func TestOAuthLinkStartUnknownProvider(t *testing.T) {
	host := newLinkHost(t)
	c := host.signUp("unknown-provider@example.com")

	resp, body := c.do("GET", "/auth/oauth/not-wired/link/start", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("link/start for an unwired provider = %d, want 404; body=%s", resp.StatusCode, body)
	}
}

// TestOAuthLinkRedirectIsAllowlisted proves the open-redirect guard: a
// destination the host did not allowlist falls back to the same-origin default
// instead of bouncing the browser off-origin after a successful link.
func TestOAuthLinkRedirectIsAllowlisted(t *testing.T) {
	host := newLinkHost(t)
	c := host.signUp("redirect-guard@example.com")

	state := c.startLink(hostileRedirect)
	resp, body := c.callback("redirect-guard@example.com", state)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want 302; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Location"); got != "/" {
		t.Fatalf("callback Location = %q, want the same-origin default %q", got, "/")
	}
	if !c.methods().linked(linkProvider) {
		t.Error("the link itself did not complete; only the redirect should have been refused")
	}
}

// TestOAuthLinkTargetsTheStateOwnerNotTheCaller is the cross-user proof: the
// linking user comes from server-side state, so query input on the callback
// cannot steer a provider identity onto a different account.
func TestOAuthLinkTargetsTheStateOwnerNotTheCaller(t *testing.T) {
	host := newLinkHost(t)
	const (
		ownerAddr    = "state-owner@example.com"
		attackerAddr = "state-thief@example.com"
	)
	owner := host.signUp(ownerAddr)
	attacker := host.signUp(attackerAddr)

	// The owner starts a link and its state leaks (a shoulder-surfed URL, a
	// referrer, a shared machine).
	state := owner.startLink(settingsRedirect)

	// The attacker — signed in as a DIFFERENT user — redeems it.
	resp, body := attacker.callback(ownerAddr, state)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("attacker callback = %d, want 302; body=%s", resp.StatusCode, body)
	}
	if names := host.sessionCookiesSet(resp); len(names) != 0 {
		t.Errorf("attacker callback re-issued session cookies %v", names)
	}

	// The link landed on the state's owner, not the caller.
	if attacker.methods().linked(linkProvider) {
		t.Fatal("the provider identity was linked to the CALLER; the linking user must come from server-side state")
	}
	if !owner.methods().linked(linkProvider) {
		t.Fatal("the provider identity was not linked to the state's owner")
	}
}

// TestOAuthLinkProviderIdentityConflict proves a provider account already owned
// by one user does not move to another.
func TestOAuthLinkProviderIdentityConflict(t *testing.T) {
	host := newLinkHost(t)
	const (
		firstAddr    = "identity-owner@example.com"
		secondAddr   = "identity-claimer@example.com"
		providerCode = "shared-provider-account@example.com"
	)
	first := host.signUp(firstAddr)
	second := host.signUp(secondAddr)

	if resp, body := first.callback(providerCode, first.startLink(settingsRedirect)); resp.StatusCode != http.StatusFound {
		t.Fatalf("first link = %d, want 302; body=%s", resp.StatusCode, body)
	}
	if !first.methods().linked(linkProvider) {
		t.Fatal("the first link did not complete")
	}

	// The second user attempts to claim the SAME provider identity.
	resp, body := second.callback(providerCode, second.startLink(settingsRedirect))
	if resp.StatusCode == http.StatusFound {
		t.Errorf("conflicting link = 302, want a rejection; body=%s", body)
	}
	if second.methods().linked(linkProvider) {
		t.Error("a provider identity owned by another user was linked to the claimer")
	}
	if !first.methods().linked(linkProvider) {
		t.Error("the original owner LOST the link; a conflict must not move an identity")
	}
}

// TestOAuthUnlinkCodeFlow drives the code-gated unlink pair a settings page pairs
// with the link button. The code is mailed to the account's verified recovery
// identifier, so this also proves the delivered secret is the only way through.
func TestOAuthUnlinkCodeFlow(t *testing.T) {
	host := newLinkHost(t)
	const addr = "unlink-flow@example.com"
	c := host.signUp(addr)

	if resp, body := c.callback(addr, c.startLink(settingsRedirect)); resp.StatusCode != http.StatusFound {
		t.Fatalf("link = %d, want 302; body=%s", resp.StatusCode, body)
	}
	if !c.methods().linked(linkProvider) {
		t.Fatal("setup link did not complete")
	}

	csrf := c.csrfToken()
	header := http.Header{"X-CSRF-Token": {csrf}}

	before := host.sender.count()
	resp, body := c.do("POST", "/auth/oauth/"+linkProvider+"/unlink/start", `{}`, header)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink/start = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var start struct {
		Status  string `json:"status"`
		Receipt string `json:"receipt"`
	}
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatalf("decode unlink/start %q: %v", body, err)
	}
	if start.Status != "sent" {
		t.Errorf("unlink/start status = %q, want sent", start.Status)
	}
	if strings.Contains(start.Receipt, addr) {
		t.Errorf("unlink/start receipt %q carries the raw address", start.Receipt)
	}

	code := host.awaitCode(before, addr)

	// A wrong code cannot unlink.
	if resp, body := c.do("POST", "/auth/oauth/"+linkProvider+"/unlink", `{"code":"000000"}`, header); resp.StatusCode == http.StatusOK {
		t.Errorf("unlink with a wrong code = 200; body=%s", body)
	}
	if !c.methods().linked(linkProvider) {
		t.Fatal("a rejected unlink removed the link anyway")
	}

	// The delivered code completes it. A fresh CSRF token is read because the
	// rejected attempt above may have rotated it.
	header = http.Header{"X-CSRF-Token": {c.csrfToken()}}
	resp, body = c.do("POST", "/auth/oauth/"+linkProvider+"/unlink", `{"code":"`+code+`"}`, header)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if c.methods().linked(linkProvider) {
		t.Error("the provider is still in the inventory after a successful unlink")
	}
}

// csrfToken bootstraps the double-submit token from the JSON body, exactly as a
// browser client must (it cannot read the API-origin cookie).
func (c *linkClient) csrfToken() string {
	c.t.Helper()
	resp, body := c.do("GET", "/auth/csrf", "", nil)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET /auth/csrf = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		c.t.Fatalf("decode /auth/csrf %q: token=%q err=%v", body, out.Token, err)
	}
	return out.Token
}
