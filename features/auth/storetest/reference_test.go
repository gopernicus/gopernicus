package storetest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/auth"
	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// TestReference runs the conformance suite against the in-package reference
// implementation. This is what lets features/auth self-verify under guard G2
// (the core cannot import a driver or a host store, so without an in-package
// implementation the suite would compile but never execute). newRepos returns a
// fresh, empty store per call — the memory harness's clean-isolation contract.
func TestReference(t *testing.T) {
	Run(t, func(t *testing.T) auth.Repositories {
		return newReference().repositories()
	})
}

// reference is a stdlib-only, test-scoped in-memory auth.Repositories. It
// hand-enforces the uniqueness and expired-at-read semantics a SQL store gives a
// dialect adapter for free, because those are exactly the invariants the suite
// proves and the class of drift a naive memory store silently loses. Expiry is
// checked against time.Now, matching a store that filters on the read clock.
type reference struct {
	mu        sync.RWMutex
	users     map[string]user.User
	passwords map[string]string
	sessions  map[string]session.Session
	codes     map[string]verification.Code
	tokens    map[string]verification.Token
}

func newReference() *reference {
	return &reference{
		users:     map[string]user.User{},
		passwords: map[string]string{},
		sessions:  map[string]session.Session{},
		codes:     map[string]verification.Code{},
		tokens:    map[string]verification.Token{},
	}
}

func (r *reference) repositories() auth.Repositories {
	return auth.Repositories{
		Users:              refUsers{r},
		Passwords:          refPasswords{r},
		Sessions:           refSessions{r},
		VerificationCodes:  refCodes{r},
		VerificationTokens: refTokens{r},
	}
}

// --- user.UserRepository ---

type refUsers struct{ *reference }

func (r refUsers) Create(_ context.Context, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.users {
		if strings.EqualFold(ex.Email, u.Email) {
			return user.User{}, errs.ErrAlreadyExists
		}
	}
	r.users[u.ID] = u
	return u, nil
}

func (r refUsers) Get(_ context.Context, id string) (user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, errs.ErrNotFound
	}
	return u, nil
}

func (r refUsers) GetByEmail(_ context.Context, email string) (user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return user.User{}, errs.ErrNotFound
}

func (r refUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[id]; !ok {
		return user.User{}, errs.ErrNotFound
	}
	r.users[id] = u
	return u, nil
}

// --- user.PasswordRepository ---

type refPasswords struct{ *reference }

func (r refPasswords) Set(_ context.Context, userID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passwords[userID] = hash
	return nil
}

func (r refPasswords) Get(_ context.Context, userID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.passwords[userID]
	if !ok {
		return "", errs.ErrNotFound
	}
	return h, nil
}

// --- session.SessionRepository ---

type refSessions struct{ *reference }

func (r refSessions) Create(_ context.Context, s session.Session) (session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.Token] = s
	return s, nil
}

func (r refSessions) Get(_ context.Context, token string) (session.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[token]
	if !ok {
		return session.Session{}, errs.ErrNotFound
	}
	if s.Expired(time.Now()) {
		return session.Session{}, errs.ErrExpired
	}
	return s, nil
}

func (r refSessions) Delete(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[token]; !ok {
		return errs.ErrNotFound
	}
	delete(r.sessions, token)
	return nil
}

func (r refSessions) DeleteByUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for token, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, token)
		}
	}
	return nil
}

// --- verification.CodeRepository ---

type refCodes struct{ *reference }

func (r refCodes) Create(_ context.Context, c verification.Code) (verification.Code, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[c.Code] = c
	return c, nil
}

func (r refCodes) Get(_ context.Context, code string) (verification.Code, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.codes[code]
	if !ok {
		return verification.Code{}, errs.ErrNotFound
	}
	if c.Expired(time.Now()) {
		return verification.Code{}, errs.ErrExpired
	}
	return c, nil
}

func (r refCodes) Delete(_ context.Context, code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.codes[code]; !ok {
		return errs.ErrNotFound
	}
	delete(r.codes, code)
	return nil
}

// --- verification.TokenRepository ---

type refTokens struct{ *reference }

func (r refTokens) Create(_ context.Context, t verification.Token) (verification.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[t.Token] = t
	return t, nil
}

func (r refTokens) Get(_ context.Context, token string) (verification.Token, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tokens[token]
	if !ok {
		return verification.Token{}, errs.ErrNotFound
	}
	if t.Expired(time.Now()) {
		return verification.Token{}, errs.ErrExpired
	}
	return t, nil
}

func (r refTokens) Delete(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tokens[token]; !ok {
		return errs.ErrNotFound
	}
	delete(r.tokens, token)
	return nil
}
