package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/sdk"
)

// signInGate is a host BeforeSession hook: it records each sign-in and refuses
// while refuse is set.
type signInGate struct {
	mu     sync.Mutex
	refuse bool
	seen   []authlogic.SignIn
}

func (g *signInGate) hook(_ context.Context, in authlogic.SignIn) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = append(g.seen, in)
	if g.refuse {
		return fmt.Errorf("address not admitted: %w", sdk.ErrForbidden)
	}
	return nil
}

func (g *signInGate) set(refuse bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refuse = refuse
}

func (g *signInGate) last(t *testing.T) authlogic.SignIn {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.seen) == 0 {
		t.Fatal("BeforeSession was not called")
	}
	return g.seen[len(g.seen)-1]
}

// TestBeforeSessionRefusesAProvisioningLink walks the one sign-in whose
// account and session commit together: a magic link that provisions the
// account. The hook runs after that commit, so a refusal must still hand out
// no session; the account stays, and the next link arrives with Created false.
func TestBeforeSessionRefusesAProvisioningLink(t *testing.T) {
	t.Parallel()
	gate := &signInGate{refuse: true}
	sender := &recordingSender{}
	svc := bootInProcess(t, sender, func(cfg *authenticationConfig) {
		cfg.PasswordlessProvisionOnRedeem = true
		cfg.SessionsConfig.BeforeSession = gate.hook
	})
	router := mountInProcess(t, svc)
	stop := runDelivery(t, svc)
	t.Cleanup(stop)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	host := &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]}
	const addr = "gated-newcomer@example.com"

	c := host.newClient()
	before := sender.count()
	c.startMagicLink(t, addr)
	token := host.awaitMagicToken(t, before, addr)

	resp, body := c.do("POST", "/auth/passwordless/redeem", `{"token":"`+token+`"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("refused redeem = %d, want 403; body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "not admitted") {
		t.Errorf("the hook's text reached the response: %s", body)
	}
	for _, ck := range resp.Cookies() {
		if ck.Value != "" && ck.MaxAge >= 0 {
			t.Errorf("a refused redeem set cookie %q", ck.Name)
		}
	}
	if me, _ := c.do("GET", "/auth/me", "", nil); me.StatusCode != http.StatusUnauthorized {
		t.Errorf("/auth/me after a refused redeem = %d, want 401", me.StatusCode)
	}
	created := gate.last(t)
	if !created.Created || created.UserID == "" {
		t.Fatalf("SignIn = %+v, want Created with the provisioned user", created)
	}
	if len(created.Identifiers) != 1 || !strings.EqualFold(created.Identifiers[0].Value, addr) {
		t.Errorf("Identifiers = %+v, want the link's verified address", created.Identifiers)
	}

	// Admitted, the next link signs the same account in with Created false.
	gate.set(false)
	before = sender.count()
	c.startMagicLink(t, addr)
	token = host.awaitMagicToken(t, before, addr)
	if resp, body := c.do("POST", "/auth/passwordless/redeem", `{"token":"`+token+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("admitted redeem = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if me, body := c.do("GET", "/auth/me", "", nil); me.StatusCode != http.StatusOK || !strings.Contains(string(body), created.UserID) {
		t.Fatalf("/auth/me = %d %s, want the account the refused link created", me.StatusCode, body)
	}
	if again := gate.last(t); again.Created || again.UserID != created.UserID {
		t.Errorf("second SignIn = %+v, want the same user with Created false", again)
	}
}
