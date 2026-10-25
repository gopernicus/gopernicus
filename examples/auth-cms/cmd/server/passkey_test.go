package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	securityevent "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// passkeyHost is a running auth-cms host — passkeys over the go-webauthn
// integration exactly as buildAuthConfig wires them — on the in-process
// delivery pool, with the MFA demo routes.
type passkeyHost struct {
	*linkHost
	rpID string
	logs *bytes.Buffer
}

func newPasskeyHost(t *testing.T, tune func(*authenticationConfig)) *passkeyHost {
	t.Helper()
	origin := hostAllowedOrigins(t)[0]
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, func(cfg *authenticationConfig) {
		cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
		if tune != nil {
			tune(cfg)
		}
	})
	router := web.NewWebHandler()
	if err := svc.HTTP.Register(pockets.Mount{Router: router, Logger: quietLog(), Events: sdkevents.NewMemory(sdkevents.WithLogger(quietLog()))}); err != nil {
		t.Fatal(err)
	}
	registerMFADemoRoutes(router, svc.HTTP, svc.Authentication)
	t.Cleanup(runDelivery(t, svc))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &passkeyHost{linkHost: &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: origin}, rpID: u.Hostname(), logs: &logs}
}

func (h *passkeyHost) authenticator(verifying bool) *webauthntest.Authenticator {
	return webauthntest.NewAuthenticator(webauthntest.AuthenticatorConfig{RPID: h.rpID, Origin: h.origin, UserVerifying: verifying})
}

// ceremony runs an options call and returns its handle and the public_key.
func (c *linkClient) ceremony(path string) (string, json.RawMessage, int) {
	c.t.Helper()
	resp, body := c.mutate(path, "{}")
	if resp.StatusCode != http.StatusOK {
		return "", nil, resp.StatusCode
	}
	var opts struct {
		Ceremony  string          `json:"ceremony"`
		PublicKey json.RawMessage `json:"public_key"`
	}
	if err := json.Unmarshal(body, &opts); err != nil {
		c.t.Fatalf("decode options %s: %v", body, err)
	}
	return opts.Ceremony, opts.PublicKey, resp.StatusCode
}

func passkeyFinish(ceremony string, credential []byte) string {
	raw, _ := json.Marshal(map[string]any{"ceremony": ceremony, "credential": json.RawMessage(credential)})
	return string(raw)
}

func (c *linkClient) enrollPasskey(a *webauthntest.Authenticator) (int, map[string]any) {
	c.t.Helper()
	ceremony, options, status := c.ceremony("/auth/passkeys/registration/options")
	if status != http.StatusOK {
		return status, nil
	}
	response, err := a.Register(options)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, body := c.mutate("/auth/passkeys/registration", passkeyFinish(ceremony, response))
	var item map[string]any
	_ = json.Unmarshal(body, &item)
	return resp.StatusCode, item
}

// stepUpToAAL2 brings c's session to AAL2 the way an administrator does under
// the admins default: it enrolls a user-verified passkey (the AAL1 bootstrap a
// fresh login allows) and steps up with it. It returns the authenticator.
func (c *linkClient) stepUpToAAL2() *webauthntest.Authenticator {
	c.t.Helper()
	u, err := url.Parse(c.host.origin)
	if err != nil {
		c.t.Fatal(err)
	}
	key := webauthntest.NewAuthenticator(webauthntest.AuthenticatorConfig{RPID: u.Hostname(), Origin: c.host.origin, UserVerifying: true})
	if status, item := c.enrollPasskey(key); status != http.StatusCreated {
		c.t.Fatalf("enroll the administrator's passkey = %d %v", status, item)
	}
	if stepped := c.passkeyStepUp(key); stepped["assurance"] != "aal2" {
		c.t.Fatalf("administrator step-up = %v, want aal2", stepped)
	}
	return key
}

func (c *linkClient) passkeySignIn(a *webauthntest.Authenticator) int {
	c.t.Helper()
	resp, body := c.do("POST", "/auth/passkeys/assertion/options", "{}", nil)
	var opts struct {
		Ceremony  string          `json:"ceremony"`
		PublicKey json.RawMessage `json:"public_key"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &opts) != nil {
		c.t.Fatalf("sign-in options = %d %s", resp.StatusCode, body)
	}
	response, err := a.Assert(opts.PublicKey)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, _ = c.do("POST", "/auth/passkeys/assertion", passkeyFinish(opts.Ceremony, response), nil)
	return resp.StatusCode
}

func (c *linkClient) passkeyStepUp(a *webauthntest.Authenticator) map[string]any {
	c.t.Helper()
	ceremony, options, status := c.ceremony("/auth/step-up/passkey/options")
	if status != http.StatusOK {
		c.t.Fatalf("step-up options = %d", status)
	}
	response, err := a.Assert(options)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, body := c.mutate("/auth/step-up/passkey", passkeyFinish(ceremony, response))
	var out map[string]any
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &out) != nil {
		c.t.Fatalf("step-up = %d %s", resp.StatusCode, body)
	}
	return out
}

func (c *linkClient) assurance(path string) (int, string) {
	c.t.Helper()
	resp, body := c.do("GET", path, "", nil)
	var out struct{ Assurance string }
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode == http.StatusUnauthorized && !strings.Contains(resp.Header.Get("WWW-Authenticate"), "insufficient_user_authentication") {
		c.t.Fatalf("%s 401 without the RFC 9470 challenge: %q", path, resp.Header.Get("WWW-Authenticate"))
	}
	return resp.StatusCode, out.Assurance
}

func (h *passkeyHost) passkeyNotices(to string) int {
	n := 0
	for _, m := range h.sender.all() {
		if m.Subject == "Your passkeys were changed" && addressedTo(m.To, to) {
			n++
		}
	}
	return n
}

func (h *passkeyHost) awaitNotices(to string, n int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.passkeyNotices(to) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("passkey_change_notice mails to %s = %d, want %d", to, h.passkeyNotices(to), n)
}

// TestPasskeysThroughTheHost walks Verification §2 over this host's real route
// table with authmem and the in-process mailer: the B-2 default, the AAL1
// bootstrap, usernameless AAL2 sign-in, the AAL2 enrollment gate, the passkey
// step-up, a presence-only key at AAL1, the clone refusal, the admin gate under
// the default, the admin reset and re-enrollment.
func TestPasskeysThroughTheHost(t *testing.T) {
	var admins sync.Map
	host := newPasskeyHost(t, func(cfg *authenticationConfig) {
		cfg.UserAdminCheck = func(_ context.Context, req authenticationhttp.UserAdminCheckRequest) error {
			if _, ok := admins.Load(req.Principal.ID); ok {
				return nil
			}
			return sdk.ErrForbidden
		}
	})
	if !strings.Contains(host.logs.String(), "mfa_required=admins source=default") {
		t.Fatalf("boot line missing the B-2 default:\n%s", host.logs)
	}

	// (1) A password session is AAL1: the MFA route answers the challenge.
	alice := host.signUp("alice@example.com")
	if status, _ := alice.assurance("/demo/mfa"); status != http.StatusUnauthorized {
		t.Fatalf("AAL1 /demo/mfa = %d, want 401", status)
	}
	if resp, _ := alice.do("GET", "/demo/mfa/page", "", nil); resp.StatusCode != http.StatusSeeOther ||
		resp.Header.Get("Location") != "/auth/step-up/mfa?return_to=%2Fdemo%2Fmfa%2Fpage" {
		t.Fatalf("AAL1 /demo/mfa/page = %d %q, want the 303 to the chooser (TOTP views are wired)", resp.StatusCode, resp.Header.Get("Location"))
	}

	// (2) The first passkey bootstraps from the fresh login; a notice is mailed.
	key := host.authenticator(true)
	if status, item := alice.enrollPasskey(key); status != http.StatusCreated || item["name"] != "Passkey 1" {
		t.Fatalf("first passkey = %d %v", status, item)
	}
	host.awaitNotices("alice@example.com", 1)

	// (3) Usernameless sign-in at AAL2.
	device := host.newClient()
	if status := device.passkeySignIn(key); status != http.StatusOK {
		t.Fatalf("passkey sign-in = %d", status)
	}
	if status, level := device.assurance("/demo/mfa"); status != http.StatusOK || level != "aal2" {
		t.Fatalf("passkey /demo/mfa = %d %q", status, level)
	}

	// (4) A fresh password login of the holder cannot add another passkey…
	password := host.newClient()
	if resp, body := password.do("POST", "/auth/login", `{"email":"alice@example.com","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	if status, _ := password.enrollPasskey(host.authenticator(true)); status != http.StatusForbidden {
		t.Fatalf("UV holder at AAL1 = %d, want 403", status)
	}
	// (5) …until a passkey step-up re-mints the session at AAL2.
	if stepped := password.passkeyStepUp(key); stepped["assurance"] != "aal2" {
		t.Fatalf("step-up = %v", stepped)
	}
	if status, _ := password.enrollPasskey(host.authenticator(true)); status != http.StatusCreated {
		t.Fatalf("enrollment after the step-up = %d, want 201", status)
	}

	// (7) Registered with verification, asserted without: AAL1.
	key.SetUserVerified(false)
	presence := host.newClient()
	if status := presence.passkeySignIn(key); status != http.StatusOK {
		t.Fatalf("presence-only sign-in = %d", status)
	}
	if status, _ := presence.assurance("/demo/mfa"); status != http.StatusUnauthorized {
		t.Fatalf("presence-only /demo/mfa = %d, want 401", status)
	}
	key.SetUserVerified(true)

	// (8) A regressed counter is refused.
	key.SetSignCount(0)
	if status := host.newClient().passkeySignIn(key); status != http.StatusUnauthorized {
		t.Fatalf("regressed counter = %d, want 401", status)
	}

	// (10) Under the default, an AAL1 platform admin gets the challenge; a passkey
	// step-up admits them.
	admin := host.signUp("root@example.com")
	admins.Store(admin.userIDFor(), true)
	if resp, _ := admin.do("GET", "/auth/admin/users", "", nil); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "insufficient_user_authentication") {
		t.Fatalf("AAL1 admin = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	rootKey := host.authenticator(true)
	if status, _ := admin.enrollPasskey(rootKey); status != http.StatusCreated {
		t.Fatalf("admin enrollment = %d", status)
	}
	admin.passkeyStepUp(rootKey)
	if resp, body := admin.do("GET", "/auth/admin/users", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("AAL2 admin = %d %s", resp.StatusCode, body)
	}

	// (11) The admin resets Alice's passkeys; she re-enrolls from a password login.
	aliceID := device.userIDFor()
	resp, body := admin.mutate("/auth/admin/users/"+aliceID+"/passkeys/reset", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"removed":2`) {
		t.Fatalf("reset = %d %s", resp.StatusCode, body)
	}
	host.awaitNotices("alice@example.com", 3)
	again := host.newClient()
	if resp, body := again.do("POST", "/auth/login", `{"email":"alice@example.com","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after reset = %d %s", resp.StatusCode, body)
	}
	if status, _ := again.enrollPasskey(host.authenticator(true)); status != http.StatusCreated {
		t.Fatalf("re-enrollment after reset = %d, want 201", status)
	}
	if resp, _ := device.do("GET", "/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a session after the reset = %d, want revoked", resp.StatusCode)
	}
}

// TestPasskeyRowsAndNoticesCarryNoCredentialMaterial is Verification §2(12)
// over the go-webauthn relying party: after a registration, sign-in, step-up,
// clone refusal and removal, no security-event row, notice mail or log line
// carries the credential id, the COSE public key, the user handle or the
// passkey's name, in any encoding.
func TestPasskeyRowsAndNoticesCarryNoCredentialMaterial(t *testing.T) {
	host := newPasskeyHost(t, nil)
	const email, name = "hygiene@example.com", "Hygiene Laptop 7781"
	c := host.signUp(email)
	key := host.authenticator(true)
	ceremony, options, status := c.ceremony("/auth/passkeys/registration/options")
	if status != http.StatusOK {
		t.Fatalf("registration options = %d", status)
	}
	var opts struct {
		User struct{ ID string } `json:"user"`
	}
	if err := json.Unmarshal(options, &opts); err != nil {
		t.Fatal(err)
	}
	response, err := key.Register(options)
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		RawID    string `json:"rawId"`
		Response struct {
			AuthenticatorData string `json:"authenticatorData"`
		} `json:"response"`
	}
	if err := json.Unmarshal(response, &reg); err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding
	handle, err1 := b64.DecodeString(opts.User.ID)
	credentialID, err2 := b64.DecodeString(reg.RawID)
	authData, err3 := b64.DecodeString(reg.Response.AuthenticatorData)
	if err1 != nil || err2 != nil || err3 != nil || len(authData) <= 55+len(credentialID) {
		t.Fatalf("decode the registration: %v %v %v", err1, err2, err3)
	}
	publicKey := authData[55+len(credentialID):]
	body, _ := json.Marshal(map[string]any{"ceremony": ceremony, "credential": json.RawMessage(response), "name": name})
	resp, payload := c.mutate("/auth/passkeys/registration", string(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("registration = %d %s", resp.StatusCode, payload)
	}
	var item struct{ ID string }
	_ = json.Unmarshal(payload, &item)
	if status := host.newClient().passkeySignIn(key); status != http.StatusOK {
		t.Fatalf("sign-in = %d", status)
	}
	c.passkeyStepUp(key)
	key.SetSignCount(0)
	if status := host.newClient().passkeySignIn(key); status != http.StatusUnauthorized {
		t.Fatalf("regressed counter = %d", status)
	}
	if resp, payload := c.do("DELETE", "/auth/passkeys/"+item.ID, "", http.Header{"X-CSRF-Token": {c.csrfToken()}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("removal = %d %s", resp.StatusCode, payload)
	}
	host.awaitNotices(email, 2)

	var needles []string
	for _, secret := range [][]byte{credentialID, publicKey, handle} {
		needles = append(needles, b64.EncodeToString(secret), base64.StdEncoding.EncodeToString(secret), hex.EncodeToString(secret))
	}
	needles = append(needles, name)
	page, err := host.svc.SecurityEvents.Repository().List(context.Background(), securityevent.ListFilter{}, list.Request{Limit: list.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range page.Items {
		seen[row.EventType] = true
		raw, _ := json.Marshal(row)
		for _, needle := range needles {
			if strings.Contains(string(raw), needle) {
				t.Errorf("%s row carries credential material: %s", row.EventType, raw)
			}
		}
	}
	for _, want := range []string{securityevent.TypePasskeyRegistered, securityevent.TypePasskeyLogin, securityevent.TypePasskeySignCountRegressed, securityevent.TypePasskeyRemoved} {
		if !seen[want] {
			t.Errorf("no %s row was recorded", want)
		}
	}
	for _, m := range host.sender.all() {
		for _, needle := range needles {
			if strings.Contains(m.Subject+m.Text+m.HTML, needle) {
				t.Errorf("mail %q carries credential material", m.Subject)
			}
		}
	}
	for _, needle := range needles {
		if strings.Contains(host.logs.String(), needle) {
			t.Errorf("a log line carries credential material %q", needle)
		}
	}
}

// TestPasskeyHostWithoutAdminCheckKeepsNone pins the other arm of B-2: without
// a UserAdminCheck the zero value resolves to none.
func TestPasskeyHostWithoutAdminCheckKeepsNone(t *testing.T) {
	host := newPasskeyHost(t, nil)
	if !strings.Contains(host.logs.String(), "mfa_required=none source=default") {
		t.Fatalf("boot line:\n%s", host.logs)
	}
}
