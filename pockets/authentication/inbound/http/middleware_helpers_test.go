package authenticationhttp

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

type middlewareHarness struct {
	svc    *testService
	users  *memUsers
	sess   *memSessions
	signer *fakeSigner
}

func newMiddlewareHarness(t *testing.T, limiters ...ratelimiter.Limiter) *middlewareHarness {
	var limiter ratelimiter.Limiter
	if len(limiters) > 0 {
		limiter = limiters[0]
	}
	return newMiddlewareTokenHarness(t, newFakeSigner(), false, limiter)
}

func newMiddlewareTokenHarness(t *testing.T, signer *fakeSigner, requireVerified bool, limiter ratelimiter.Limiter) *middlewareHarness {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	sess := &memSessions{m: map[string]session.Session{}}
	svc := newServiceWithFakes(authenticationFixture{
		Users: users, Identifiers: newMemIdentifiers(users),
		Passwords: &memPasswords{m: map[string]string{}}, Sessions: sess,
		Hasher: fakeHasher{}, Limiter: limiter, TokenSigner: signer,
		ServiceAccounts:      &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:              &memAPIKeys{m: map[string]apikey.APIKey{}},
		RequireVerifiedEmail: requireVerified,
	})
	return &middlewareHarness{svc: svc, users: users, sess: sess, signer: signer}
}

func (h *middlewareHarness) mustRegister(t *testing.T, email, password string) user.User {
	t.Helper()
	u, err := h.svc.Register(context.Background(), email, password, "Test User")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (h *middlewareHarness) loginPair(t *testing.T, email, password string) TokenPair {
	t.Helper()
	h.mustRegister(t, email, password)
	pair, _, err := h.svc.Login(context.Background(), email, password)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func middlewareSessions(h *middlewareHarness) []string {
	h.sess.mu.Lock()
	defer h.sess.mu.Unlock()
	ids := make([]string, 0, len(h.sess.m))
	for id := range h.sess.m {
		ids = append(ids, id)
	}
	return ids
}
