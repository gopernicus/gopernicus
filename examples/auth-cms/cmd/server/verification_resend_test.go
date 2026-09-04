package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// CHAU-2.3 — the registration-verification resend surface over real HTTP through
// exported host seams.
//
// The public route's whole contract is that its response cannot be used to learn
// anything about the target, so the central assertion here is a BYTE-FOR-BYTE
// comparison of status, body, and the security-relevant headers across every
// target state. A test that merely checked "all 202" would miss a body or header
// that differed.

// resendProbe is one observation of the public resend response.
type resendProbe struct {
	status  int
	body    string
	headers string
}

func (h *linkHost) probeResend(t *testing.T, email string) resendProbe {
	t.Helper()
	// A fresh client per probe so cookies from an earlier sign-up cannot alter the
	// request shape.
	c := h.newClient()
	resp, body := c.do("POST", "/auth/verification/resend", `{"email":"`+email+`"}`, nil)

	var interesting []string
	for _, key := range []string{"Content-Type", "Cache-Control", "Access-Control-Allow-Origin", "Vary"} {
		interesting = append(interesting, key+": "+resp.Header.Get(key))
	}
	return resendProbe{
		status:  resp.StatusCode,
		body:    string(body),
		headers: strings.Join(interesting, "\n"),
	}
}

// TestPublicResendIsByteIdenticalAcrossTargetStates is the enumeration proof.
func TestPublicResendIsByteIdenticalAcrossTargetStates(t *testing.T) {
	host := newLinkHost(t)

	// An active, unverified account: registered but never verified.
	const unverified = "resend-unverified@example.com"
	c := host.newClient()
	if resp, body := c.do("POST", "/auth/register",
		`{"email":"`+unverified+`","password":"`+linkPassword+`","display_name":"Unverified"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d, want 201; body=%s", resp.StatusCode, body)
	}

	// A fully verified account.
	const verified = "resend-verified@example.com"
	host.signUp(verified)

	probes := []struct {
		name  string
		email string
	}{
		{"unknown address", "resend-nobody@example.com"},
		{"malformed address", "not-an-email"},
		{"empty address", ""},
		{"active unverified", unverified},
		{"already verified", verified},
	}

	var baseline resendProbe
	for i, p := range probes {
		got := host.probeResend(t, p.email)
		if got.status != http.StatusAccepted {
			t.Errorf("%s = %d, want 202", p.name, got.status)
		}
		if !strings.Contains(got.body, `"accepted"`) {
			t.Errorf("%s body = %s, want the accepted envelope", p.name, got.body)
		}
		if strings.Contains(strings.ToLower(got.body), "verif") && !strings.Contains(got.body, `"accepted"`) {
			t.Errorf("%s body leaks target state: %s", p.name, got.body)
		}
		if i == 0 {
			baseline = got
			continue
		}
		if got.status != baseline.status || got.body != baseline.body || got.headers != baseline.headers {
			t.Errorf("%s response differs from the unknown-address baseline:\n got: %d %q\n%s\n want: %d %q\n%s",
				p.name, got.status, got.body, got.headers, baseline.status, baseline.body, baseline.headers)
		}
	}
}

// TestPublicResendThrottles proves the ONE non-uniform outcome, and that it is a
// throttle rather than anything about the target.
func TestPublicResendThrottles(t *testing.T) {
	host := newLinkHost(t)
	c := host.newClient()

	const addr = "resend-throttled@example.com"
	var last *http.Response
	var lastBody []byte
	// The per-IP budget is the smaller ceiling across distinct addresses; hammering
	// one address hits the per-identifier budget first.
	for range 12 {
		last, lastBody = c.do("POST", "/auth/verification/resend", `{"email":"`+addr+`"}`, nil)
		if last.StatusCode == http.StatusTooManyRequests {
			break
		}
	}
	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("resend never throttled; last = %d body=%s", last.StatusCode, lastBody)
	}
	if strings.Contains(strings.ToLower(string(lastBody)), "verif") {
		t.Errorf("the throttle response mentions verification state: %s", lastBody)
	}
}

// TestPublicResendDeliversAFreshUsableCode is the behavioral half: the resend is
// not just accepted, it actually produces a code that verifies.
func TestPublicResendDeliversAFreshUsableCode(t *testing.T) {
	host := newLinkHost(t)
	const addr = "resend-usable@example.com"

	c := host.newClient()
	before := host.sender.count()
	if resp, body := c.do("POST", "/auth/register",
		`{"email":"`+addr+`","password":"`+linkPassword+`","display_name":"Usable"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d; body=%s", resp.StatusCode, body)
	}
	registrationCode := host.awaitCode(before, addr)

	// Resend, and wait for the NEW message.
	beforeResend := host.sender.count()
	if resp, body := c.do("POST", "/auth/verification/resend", `{"email":"`+addr+`"}`, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("resend = %d; body=%s", resp.StatusCode, body)
	}
	resentCode := host.awaitCode(beforeResend, addr)

	if resentCode == registrationCode {
		t.Fatal("the resend delivered the SAME code; a replacement must issue a fresh one")
	}
	// The superseded code is dead …
	if resp, _ := c.do("POST", "/auth/verify", `{"email":"`+addr+`","code":"`+registrationCode+`"}`, nil); resp.StatusCode == http.StatusOK {
		t.Error("the superseded registration code still verified")
	}
	// … and the fresh one works.
	if resp, body := c.do("POST", "/auth/verify", `{"email":"`+addr+`","code":"`+resentCode+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("verify with the resent code = %d, want 200; body=%s", resp.StatusCode, body)
	}
	// A verified account can now log in, which is the point of the whole route.
	if resp, body := c.do("POST", "/auth/login", `{"email":"`+addr+`","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after resend-verify = %d, want 200; body=%s", resp.StatusCode, body)
	}
}

// TestAdminResendReportsRealTargetState is the authorized counterpart: it exists
// only when the host wired UserAdminCheck, and it may say what the public route
// never can.
func TestAdminResendReportsRealTargetState(t *testing.T) {
	host := newAdminHost(t)
	admin := host.signUp("resend-admin@example.com")
	host.authorize(admin.userIDFor())

	// An unverified target: registered, never verified, so it has no session and
	// its id must come from the directory rather than /auth/me.
	const unverified = "admin-resend-target@example.com"
	reg := host.newClient()
	if resp, body := reg.do("POST", "/auth/register",
		`{"email":"`+unverified+`","password":"`+linkPassword+`","display_name":"Target"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d; body=%s", resp.StatusCode, body)
	}
	targetID := host.findUserID(t, admin, unverified)

	header := func() http.Header { return http.Header{"X-CSRF-Token": {admin.csrfToken()}} }

	// Unverified → accepted, with a secret-free receipt.
	before := host.sender.count()
	resp, body := admin.do("POST", "/auth/admin/users/"+targetID+"/verification/resend", `{}`, header())
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("admin resend = %d, want 202; body=%s", resp.StatusCode, body)
	}
	var sent struct {
		Status  string `json:"status"`
		Receipt string `json:"receipt"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if sent.Status != "sent" || sent.Receipt == "" {
		t.Errorf("admin resend body = %+v, want a sent receipt", sent)
	}
	if strings.Contains(sent.Receipt, unverified) {
		t.Errorf("the receipt %q carries the raw address", sent.Receipt)
	}

	// The delivered code verifies, proving the admin path drives the real rail.
	code := host.awaitCode(before, unverified)
	if resp, body := reg.do("POST", "/auth/verify", `{"email":"`+unverified+`","code":"`+code+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("verify with the admin-resent code = %d; body=%s", resp.StatusCode, body)
	}

	// Now verified → a typed 409 the operator can act on.
	resp, body = admin.do("POST", "/auth/admin/users/"+targetID+"/verification/resend", `{}`, header())
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("admin resend for a verified account = %d, want 409; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "already_verified") {
		t.Errorf("409 body = %s, want the already_verified code", body)
	}

	// Unknown user → 404, not a uniform accepted.
	if resp, body := admin.do("POST", "/auth/admin/users/no-such-user/verification/resend", `{}`, header()); resp.StatusCode != http.StatusNotFound {
		t.Errorf("admin resend for an unknown user = %d, want 404; body=%s", resp.StatusCode, body)
	}

	// Deactivated → a typed 409.
	const deact = "admin-resend-deactivated@example.com"
	d := host.newClient()
	if resp, _ := d.do("POST", "/auth/register",
		`{"email":"`+deact+`","password":"`+linkPassword+`","display_name":"Deact"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatal("register(deactivated target) failed")
	}
	deactID := host.findUserID(t, admin, deact)
	if resp, body := admin.do("POST", "/auth/admin/users/"+deactID+"/deactivate", `{}`, header()); resp.StatusCode != http.StatusOK {
		t.Fatalf("deactivate = %d; body=%s", resp.StatusCode, body)
	}
	if resp, body := admin.do("POST", "/auth/admin/users/"+deactID+"/verification/resend", `{}`, header()); resp.StatusCode != http.StatusConflict {
		t.Errorf("admin resend for a deactivated account = %d, want 409; body=%s", resp.StatusCode, body)
	} else if !strings.Contains(string(body), "user_deactivated") {
		t.Errorf("409 body = %s, want the user_deactivated code", body)
	}
}

// TestAdminResendDeniedWithoutAuthorization pins that the admin resend is behind
// the same host check as the rest of the admin surface.
func TestAdminResendDeniedWithoutAuthorization(t *testing.T) {
	host := newAdminHost(t)
	stranger := host.signUp("resend-stranger@example.com")

	if resp, body := stranger.do("POST", "/auth/admin/users/anything/verification/resend", `{}`,
		http.Header{"X-CSRF-Token": {stranger.csrfToken()}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("unauthorized admin resend = %d, want 403; body=%s", resp.StatusCode, body)
	}
}

// TestAdminResendRouteAbsentWithoutCheck completes the deny-by-absence set: the
// PUBLIC route is always present, the ADMIN one only with a wired check.
func TestAdminResendRouteAbsentWithoutCheck(t *testing.T) {
	svc := bootInProcess(t, &recordingSender{}, nil)
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	host := &linkHost{t: t, srv: srv, svc: svc, sender: &recordingSender{}, origin: hostAllowedOrigins(t)[0]}
	c := host.newClient()

	if resp, _ := c.do("POST", "/auth/admin/users/x/verification/resend", `{}`, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("admin resend without a UserAdminCheck = %d, want 404", resp.StatusCode)
	}
	// The public route is unconditional.
	if resp, body := c.do("POST", "/auth/verification/resend", `{"email":"anyone@example.com"}`, nil); resp.StatusCode != http.StatusAccepted {
		t.Errorf("public resend = %d, want 202; body=%s", resp.StatusCode, body)
	}
}

// findUserID looks a user up by address through the admin directory — the only
// exported way to learn the id of an account that has no session.
func (h *linkHost) findUserID(t *testing.T, admin *linkClient, email string) string {
	t.Helper()
	resp, body := admin.do("GET", "/auth/admin/users?limit=100", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("directory = %d; body=%s", resp.StatusCode, body)
	}
	var page adminPageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode directory: %v", err)
	}
	for _, u := range page.Items {
		if strings.EqualFold(u.PrimaryEmail, email) {
			return u.ID
		}
	}
	t.Fatalf("user %q not found in the directory", email)
	return ""
}
