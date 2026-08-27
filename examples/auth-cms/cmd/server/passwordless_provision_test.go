package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
)

// CHAU-6.7 — provision-on-consumption, end to end over real HTTP through exported
// host seams.
//
// The load-bearing sequence is: request a link for an address with NO account →
// assert NO user exists yet → redeem by POST → assert the account now exists and
// the caller is signed in → assert the replay fails. A test that only checked
// "redeem returned 200" would not prove the account is created at CONSUME rather
// than at SEND, which is the entire security claim.

// magicTokenPattern extracts the token from the fragment of a delivered
// magic-link URL.
var magicTokenPattern = regexp.MustCompile(`#token=([A-Za-z0-9._~%-]+)`)

// recordingGranter is a trivial auth.Granter that records what it granted. The
// invitation flow needs a Granter to exist at all (deny-by-absence); what it does
// is the host's business, and this one just needs to succeed.
type recordingGranter struct {
	mu      sync.Mutex
	granted []string
}

func (g *recordingGranter) Grant(_ context.Context, in auth.GrantInput) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.granted = append(g.granted, in.ResourceType+":"+in.ResourceID+"#"+in.Relation+"@"+in.SubjectID)
	return nil
}

func (g *recordingGranter) grants() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.granted...)
}

// newProvisioningHostWithInvitations boots a provisioning host whose invitation
// surface is mounted, so the CHAU-6.6 resolve-on-provision path is reachable.
func newProvisioningHostWithInvitations(t *testing.T) (*linkHost, *recordingGranter) {
	t.Helper()
	granter := &recordingGranter{}
	sender := &recordingSender{}

	cfg, err := buildAuthConfig(quietLog(), granter)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	cfg.DeliveryMode = auth.DeliveryModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	cfg.PasswordlessProvisionOnRedeem = true
	cfg.InviteCheck = func(context.Context, auth.InviteCheckRequest) error { return nil }

	svc, err := auth.NewService(authmem.New().Repositories(), cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: allowedOrigins()[0]}, granter
}

// newProvisioningHost boots the host with provision-on-consumption enabled.
func newProvisioningHost(t *testing.T) *linkHost {
	t.Helper()
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, func(cfg *auth.Config) {
		cfg.PasswordlessProvisionOnRedeem = true
	})
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: allowedOrigins()[0]}
}

// awaitMagicToken waits for a magic-link message addressed to recipient and
// returns the token it carries.
func (h *linkHost) awaitMagicToken(t *testing.T, before int, recipient string) string {
	t.Helper()
	for range 200 {
		msgs := h.sender.all()
		for i := len(msgs) - 1; i >= before; i-- {
			m := msgs[i]
			if !addressedTo(m.To, recipient) {
				continue
			}
			if match := magicTokenPattern.FindStringSubmatch(m.Text + " " + m.HTML); len(match) == 2 {
				return match[1]
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no magic link delivered to %s", recipient)
	return ""
}

// startMagicLink drives the public passwordless start.
func (c *linkClient) startMagicLink(t *testing.T, addr string) {
	t.Helper()
	body := `{"identifier_kind":"email","identifier":"` + addr + `","method":"link"}`
	resp, respBody := c.do("POST", "/auth/passwordless/start", body, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("passwordless start = %d, want 202; body=%s", resp.StatusCode, respBody)
	}
}

// TestProvisionOnConsumptionCreatesTheAccountAtRedeem is the headline walk.
func TestProvisionOnConsumptionCreatesTheAccountAtRedeem(t *testing.T) {
	host := newProvisioningHost(t)
	const addr = "brand-new-person@example.com"

	c := host.newClient()
	before := host.sender.count()
	c.startMagicLink(t, addr)
	token := host.awaitMagicToken(t, before, addr)

	// SENDING created nothing. Proving that through an exported seam: a password
	// login for the address fails as unknown credentials, exactly as it would for
	// an address nobody ever mentioned.
	unknownProbe, _ := c.do("POST", "/auth/login", `{"email":"`+addr+`","password":"`+linkPassword+`"}`, nil)
	neverMentioned, _ := c.do("POST", "/auth/login", `{"email":"nobody-at-all@example.com","password":"`+linkPassword+`"}`, nil)
	if unknownProbe.StatusCode != neverMentioned.StatusCode {
		t.Errorf("an address with a pending link answers %d while an unmentioned one answers %d; sending must create nothing observable",
			unknownProbe.StatusCode, neverMentioned.StatusCode)
	}

	// Redeem by POST. No GET consumes a token, so a link scanner cannot provision.
	resp, body := c.do("POST", "/auth/passwordless/redeem", `{"token":"`+token+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("redeem = %d, want 200; body=%s", resp.StatusCode, body)
	}

	// The caller is signed in, and hydration shows the NEW account with the
	// verified address and an empty display name.
	me, meBody := c.do("GET", "/auth/me", "", nil)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("/auth/me after redeem = %d, want 200; body=%s", me.StatusCode, meBody)
	}
	var hydrated struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(meBody, &hydrated); err != nil {
		t.Fatalf("decode /auth/me %q: %v", meBody, err)
	}
	if hydrated.ID == "" {
		t.Fatal("hydration returned no user id")
	}
	if !strings.EqualFold(hydrated.Email, addr) {
		t.Errorf("hydrated email = %q, want %q", hydrated.Email, addr)
	}
	if hydrated.DisplayName != "" {
		t.Errorf("display name = %q, want empty — the pocket invents no name", hydrated.DisplayName)
	}

	// The link is single-use.
	replay := host.newClient()
	if resp, body := replay.do("POST", "/auth/passwordless/redeem", `{"token":"`+token+`"}`, nil); resp.StatusCode == http.StatusOK {
		t.Errorf("a replayed magic link redeemed again: %s", body)
	}
}

// TestProvisionOnConsumptionStartIsIndistinguishable pins the enumeration
// property that provisioning must not weaken: the public start answers
// identically for a known and an unknown address, because it still resolves
// nothing on the request path. What changed is whether the WORKER sends.
func TestProvisionOnConsumptionStartIsIndistinguishable(t *testing.T) {
	host := newProvisioningHost(t)
	known := host.signUp("provision-known@example.com")
	_ = known

	probe := func(t *testing.T, addr string) (int, string) {
		t.Helper()
		c := host.newClient()
		resp, body := c.do("POST", "/auth/passwordless/start",
			`{"identifier_kind":"email","identifier":"`+addr+`","method":"link"}`, nil)
		return resp.StatusCode, string(body)
	}

	knownStatus, knownBody := probe(t, "provision-known@example.com")
	unknownStatus, unknownBody := probe(t, "provision-unknown@example.com")
	if knownStatus != unknownStatus || knownBody != unknownBody {
		t.Errorf("a known start answers %d %q while an unknown one answers %d %q; the two must be identical",
			knownStatus, knownBody, unknownStatus, unknownBody)
	}
}

// TestProvisionDisabledSendsNothingForUnknownAddress pins the default posture:
// with the switch off, an unknown address still gets no mail and no account.
func TestProvisionDisabledSendsNothingForUnknownAddress(t *testing.T) {
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, nil) // provisioning left OFF
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: allowedOrigins()[0]}

	c := host.newClient()
	before := sender.count()
	c.startMagicLink(t, "still-unknown@example.com")

	// Give the worker a chance to do nothing.
	for range 40 {
		time.Sleep(25 * time.Millisecond)
	}
	for _, m := range sender.all()[before:] {
		if addressedTo(m.To, "still-unknown@example.com") {
			t.Fatalf("a magic link was delivered to an unknown address with provisioning OFF: %+v", m.To)
		}
	}
}

// TestProvisionWiringFailsLoudly pins the construction matrix: every dependency
// provisioning needs is required, not best-effort.
func TestProvisionWiringFailsLoudly(t *testing.T) {
	base, err := buildAuthConfig(quietLog(), nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	base.DeliveryMode = auth.DeliveryModeInProcess
	base.DeliveryJobsAcknowledged = false
	base.DeliveryEphemeralAcknowledged = true
	base.PasswordlessProvisionOnRedeem = true

	t.Run("no atomic redemption repository", func(t *testing.T) {
		repos := authmem.New().Repositories()
		repos.Passwordless = nil
		if _, err := auth.NewService(repos, base); !errors.Is(err, auth.ErrPasswordlessProvisionWiring) {
			t.Fatalf("NewService = %v, want ErrPasswordlessProvisionWiring", err)
		}
	})

	t.Run("no fenced session mint", func(t *testing.T) {
		repos := authmem.New().Repositories()
		repos.ActiveSessions = nil
		if _, err := auth.NewService(repos, base); !errors.Is(err, auth.ErrPasswordlessProvisionWiring) {
			t.Fatalf("NewService = %v, want ErrPasswordlessProvisionWiring", err)
		}
	})

	t.Run("no identifier keyer", func(t *testing.T) {
		cfg := base
		cfg.IdentifierKeyer = nil
		if _, err := auth.NewService(authmem.New().Repositories(), cfg); !errors.Is(err, auth.ErrPasswordlessProvisionWiring) {
			t.Fatalf("NewService = %v, want ErrPasswordlessProvisionWiring", err)
		}
	})

	t.Run("email passwordless kind not enabled", func(t *testing.T) {
		cfg := base
		cfg.Passwordless = []string{"phone"}
		if _, err := auth.NewService(authmem.New().Repositories(), cfg); !errors.Is(err, auth.ErrPasswordlessProvisionWiring) {
			t.Fatalf("NewService = %v, want ErrPasswordlessProvisionWiring", err)
		}
	})

	t.Run("the complete wiring constructs", func(t *testing.T) {
		if _, err := auth.NewService(authmem.New().Repositories(), base); err != nil {
			t.Fatalf("the complete provisioning wiring failed to construct: %v", err)
		}
	})
}

// TestProvisionedAccountResolvesPendingInvitations pins CHAU-6.6: a newly
// provisioned account resolves its pending invitations exactly as a password
// registration does — and an ordinary link LOGIN does not re-run that resolution.
func TestProvisionedAccountResolvesPendingInvitations(t *testing.T) {
	// The default in-process fixture wires no Granter, so the invitation surface is
	// deny-by-absence and its routes do not mount. This case needs it, so it boots a
	// host WITH a granter — the same relationshipGranter shape run() uses.
	host, granter := newProvisioningHostWithInvitations(t)
	const invitee = "invited-by-link@example.com"

	// An inviter creates a pending invitation for an address with no account.
	inviter := host.signUp("provision-inviter@example.com")
	// auto_accept is what makes an invitation resolve automatically on first
	// sign-in; a non-auto-accept row stays pending for an explicit accept, which is
	// the documented ResolveInvitations contract and NOT what this case is about.
	body := `{"identifier":"` + invitee + `","relation":"member","auto_accept":true}`
	resp, respBody := inviter.do("POST", "/auth/invitations/project/p-provision", body,
		http.Header{"X-CSRF-Token": {inviter.csrfToken()}})
	if resp.StatusCode == http.StatusNotFound {
		// The invitation surface is deny-by-absence: this host wires a Granter, but
		// if a future config drops it the route disappears rather than failing. Skip
		// LOUDLY rather than passing vacuously.
		t.Skipf("the invitation surface is not mounted on this host (404) — invitation resolution NOT verified: %s", respBody)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create invitation = %d; body=%s", resp.StatusCode, respBody)
	}

	// The invitee signs in for the first time through a magic link, which
	// provisions the account.
	c := host.newClient()
	before := host.sender.count()
	c.startMagicLink(t, invitee)
	token := host.awaitMagicToken(t, before, invitee)

	if resp, body := c.do("POST", "/auth/passwordless/redeem", `{"token":"`+token+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("redeem = %d; body=%s", resp.StatusCode, body)
	}

	// The provisioning commit resolved the pending invitation exactly once.
	grants := granter.grants()
	if len(grants) != 1 {
		t.Fatalf("granter recorded %d grants (%v), want exactly 1 from the provisioning commit", len(grants), grants)
	}
	if !strings.Contains(grants[0], "project:p-provision#member") {
		t.Errorf("grant = %q, want the pending invitation's resource and relation", grants[0])
	}

	// An ordinary LOGIN through a second link must NOT re-grant it.
	before2 := host.sender.count()
	c2 := host.newClient()
	c2.startMagicLink(t, invitee)
	token2 := host.awaitMagicToken(t, before2, invitee)
	if resp, body := c2.do("POST", "/auth/passwordless/redeem", `{"token":"`+token2+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("second redeem = %d; body=%s", resp.StatusCode, body)
	}
	if got := granter.grants(); len(got) != 1 {
		t.Errorf("a subsequent link LOGIN re-granted invitations: %v", got)
	}
}
