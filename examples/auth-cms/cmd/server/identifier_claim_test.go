package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// recordingPhone keeps every SMS body the phone kind delivers.
type recordingPhone struct {
	mu   sync.Mutex
	sent []struct{ to, body string }
}

func (p *recordingPhone) Send(_ context.Context, to, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, struct{ to, body string }{to, body})
	return nil
}

// await returns the first submatch of pattern in an SMS to `to` after `before`.
func (p *recordingPhone) await(t *testing.T, before int, to string, pattern *regexp.Regexp) string {
	t.Helper()
	for range 200 {
		p.mu.Lock()
		msgs := append([]struct{ to, body string }(nil), p.sent...)
		p.mu.Unlock()
		for i := len(msgs) - 1; i >= before; i-- {
			if msgs[i].to == to {
				if m := pattern.FindStringSubmatch(msgs[i].body); len(m) == 2 {
					return m[1]
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no matching SMS to %s", to)
	return ""
}

func (p *recordingPhone) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

var smsCodePattern = regexp.MustCompile(`(\d{6})`)

// newClaimHost boots an in-process host with invitations and a recording phone
// channel.
func newClaimHost(t *testing.T) (*linkHost, *recordingGranter, *recordingPhone) {
	t.Helper()
	granter := &recordingGranter{}
	sender := &recordingSender{}
	phone := &recordingPhone{}
	cfg, err := buildAuthConfig(quietLog(), environment.ModeDevelopment, granter)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	cfg.BodySenders = map[string]delivery.BodySender{sdk.AddressKindPhone: phone}
	cfg.InviteCheck = func(context.Context, authenticationhttp.InviteCheckRequest) error { return nil }
	svc, err := auth.New(authmem.New().Repositories(), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	router := mountInProcess(t, svc)
	t.Cleanup(runDelivery(t, svc))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}
	return host, granter, phone
}

// TestNotificationOnlyPhoneNeverResolvesSyncsOrMatches is the HIGH-1 scenario:
// the same phone verified notification-only on two accounts holds no
// authentication claim, so an AutoAccept invitation to it never resolves for
// either, a sync answers {"resolved":0}, a token accept is refused and "mine"
// lists nothing.
func TestNotificationOnlyPhoneNeverResolvesSyncsOrMatches(t *testing.T) {
	t.Parallel()
	host, granter, phone := newClaimHost(t)

	const number = "+15550100"
	addPhone := func(c *linkClient) {
		t.Helper()
		token := http.Header{"X-CSRF-Token": {c.csrfToken()}}
		if resp, body := c.do("POST", "/auth/step-up/password", `{"purpose":"change_phone","password":"`+linkPassword+`"}`, token); resp.StatusCode != http.StatusOK {
			t.Fatalf("step-up = %d; %s", resp.StatusCode, body)
		}
		before := phone.count()
		if resp, body := c.do("POST", "/auth/identifiers/phone", `{"phone":"`+number+`","uses":{"notification":true}}`, token); resp.StatusCode != http.StatusOK {
			t.Fatalf("add phone = %d; %s", resp.StatusCode, body)
		}
		code := phone.await(t, before, number, smsCodePattern)
		if resp, body := c.do("POST", "/auth/identifiers/phone/confirm", `{"code":"`+code+`"}`, token); resp.StatusCode != http.StatusOK {
			t.Fatalf("confirm phone = %d; %s", resp.StatusCode, body)
		}
	}
	first := host.signUp("claim-first@example.com")
	second := host.signUp("claim-second@example.com")
	addPhone(first)
	addPhone(second)

	inviter := host.signUp("claim-inviter@example.com")
	before := phone.count()
	if resp, body := inviter.do("POST", "/auth/invitations/project/p-claim",
		`{"identifier":"`+number+`","identifier_kind":"phone","relation":"member","auto_accept":true}`,
		http.Header{"X-CSRF-Token": {inviter.csrfToken()}}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation = %d; %s", resp.StatusCode, body)
	}
	token := phone.await(t, before, number, regexp.MustCompile(`#token=([A-Za-z0-9._~-]+)`))

	csrf := http.Header{"X-CSRF-Token": {second.csrfToken()}, "Content-Type": {"application/json"}}
	resp, body := second.do("POST", "/auth/invitations/sync", `{}`, csrf)
	if resp.StatusCode != http.StatusOK || string(body) != "{\"resolved\":0}\n" && string(body) != `{"resolved":0}` {
		t.Fatalf("sync = %d %q, want {\"resolved\":0}", resp.StatusCode, body)
	}
	resp, body = second.do("POST", "/auth/invitations/accept", `{"token":"`+token+`"}`, csrf)
	var refused struct{ Code string }
	_ = json.Unmarshal(body, &refused)
	if resp.StatusCode != http.StatusForbidden || refused.Code != "invitation_identity_mismatch" {
		t.Fatalf("accept by a notification-only holder = %d %s, want 403 invitation_identity_mismatch", resp.StatusCode, body)
	}
	if resp, body := second.do("GET", "/auth/invitations/mine?identifier_kind=phone", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("mine?identifier_kind=phone = %d %s, want 404 (no sign-in phone)", resp.StatusCode, body)
	}
	if got := granter.grants(); len(got) != 0 {
		t.Fatalf("a notification-only phone granted: %v", got)
	}
}

// TestNotificationOnlyEmailResolvesOnceLoginIsEnabled pins the second half of
// the ruling: an email added notification-only resolves nothing on its
// confirm; enabling login on it later resolves its AutoAccept invitation.
func TestNotificationOnlyEmailResolvesOnceLoginIsEnabled(t *testing.T) {
	t.Parallel()
	host, granter, _ := newClaimHost(t)
	const added = "claim-added@example.com"
	user := host.signUp("claim-user@example.com")
	csrf := http.Header{"X-CSRF-Token": {user.csrfToken()}}
	identifierIDs := func() map[string]bool {
		_, body := user.do("GET", "/auth/methods", "", nil)
		var methods struct{ Identifiers []struct{ ID string } }
		_ = json.Unmarshal(body, &methods)
		ids := map[string]bool{}
		for _, it := range methods.Identifiers {
			ids[it.ID] = true
		}
		return ids
	}
	existing := identifierIDs()
	if resp, body := user.do("POST", "/auth/step-up/password", `{"purpose":"change_email","password":"`+linkPassword+`"}`, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("step-up = %d; %s", resp.StatusCode, body)
	}
	before := host.sender.count()
	if resp, body := user.do("POST", "/auth/identifiers/email", `{"email":"`+added+`","uses":{"notification":true}}`, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("add email = %d; %s", resp.StatusCode, body)
	}
	code := host.awaitCode(before, added)
	if resp, body := user.do("POST", "/auth/identifiers/email/confirm", `{"code":"`+code+`"}`, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm = %d; %s", resp.StatusCode, body)
	}
	inviter := host.signUp("claim-inviter2@example.com")
	if resp, body := inviter.do("POST", "/auth/invitations/project/p-claim2", `{"identifier":"`+added+`","relation":"member","auto_accept":true}`,
		http.Header{"X-CSRF-Token": {inviter.csrfToken()}}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invitation = %d; %s (a notification-only address must not direct-add)", resp.StatusCode, body)
	}
	if got := granter.grants(); len(got) != 0 {
		t.Fatalf("a notification-only email granted: %v", got)
	}
	var addedID string
	for id := range identifierIDs() {
		if !existing[id] {
			addedID = id
		}
	}
	if resp, body := user.do("POST", "/auth/step-up/password", `{"purpose":"change_identifier_uses","context":"`+addedID+`","password":"`+linkPassword+`"}`, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("step-up = %d; %s", resp.StatusCode, body)
	}
	if resp, body := user.do("PATCH", "/auth/identifiers/"+addedID, `{"uses":{"login":true,"notification":true}}`, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("enable login = %d; %s", resp.StatusCode, body)
	}
	if got := granter.grants(); len(got) != 1 {
		t.Fatalf("enabling login resolved %v, want the one AutoAccept invitation", got)
	}
}
