// Package authmem is an in-memory implementation of the auth feature's full
// repository set: the v1 ports (user.UserRepository, user.PasswordRepository,
// session.SessionRepository, verification.CodeRepository,
// verification.TokenRepository) plus the auth-v2 ports the A9 proof protocol
// exercises — oauthaccount.OAuthAccountRepository, oauthstate.StateRepository,
// serviceaccount.ServiceAccountRepository, apikey.APIKeyRepository,
// securityevent.SecurityEventRepository, and invitation.InvitationRepository.
// It is the auth-side sibling of this host's cms memstore: a "bring your own
// store" proof that features/authentication runs with no datastore driver in its module
// graph — data lives in maps and is lost on exit.
//
// It mirrors the honesty the port doc comments promise (and the storetest
// conformance suite proves), not just their shape: UserRepository.Create on a
// colliding normalized email returns sdk.ErrAlreadyExists; session/code/token
// reads report expiry with sdk.ErrExpired; the OAuth-account (Provider,
// ProviderUserID) pair and API-key KeyHash are unique; oauthstate.Consume is a
// single-use get-and-delete that deletes regardless of expiry; APIKeys.GetByHash
// returns revoked/expired rows verbatim (the pinned service-layer-branch
// contract); invitations enforce partial pending-tuple uniqueness; and the
// paginated ports page in the pinned created_at DESC, id DESC order — exactly
// the invariants a SQL store gives a dialect adapter for free and a naive memory
// store silently loses. After A9 wires these six, storetest.Run exercises the
// new sub-runners against authmem rather than skipping them.
//
// The ports reuse method names (Create/Get/Delete) across different entity
// types, so one Go type cannot satisfy all of them; each port is a thin value
// over a shared *data holder.
package authmem

import (
	"context"
	"strings"
	"sync"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

// ids assigns entity keys when a Create arrives with an empty ID (the
// cryptids.Database strategy, amended D10) — mimicking a schema default.
var ids = cryptids.IDGenerator{}

// data holds every auth entity in maps behind one mutex. The v2 collections
// (oauthAccounts, securityEvents) are slices where the port has no single-key
// identity; the rest are keyed maps.
type data struct {
	mu        sync.RWMutex
	users     map[string]user.User
	passwords map[string]string
	sessions  map[string]session.Session
	codes     map[string]verification.Code
	tokens    map[string]verification.Token

	oauthAccounts   []oauthaccount.OAuthAccount
	oauthStates     map[string]oauthstate.State
	serviceAccounts map[string]serviceaccount.ServiceAccount
	apiKeys         map[string]apikey.APIKey
	securityEvents  []securityevent.SecurityEvent
	invitations     map[string]invitation.Invitation
}

// Store is an in-memory auth datastore. Its Repositories method yields the port
// set features/authentication needs.
type Store struct{ d *data }

// New returns an empty Store.
func New() *Store {
	return &Store{d: &data{
		users:           map[string]user.User{},
		passwords:       map[string]string{},
		sessions:        map[string]session.Session{},
		codes:           map[string]verification.Code{},
		tokens:          map[string]verification.Token{},
		oauthStates:     map[string]oauthstate.State{},
		serviceAccounts: map[string]serviceaccount.ServiceAccount{},
		apiKeys:         map[string]apikey.APIKey{},
		invitations:     map[string]invitation.Invitation{},
	}}
}

// Repositories bundles the per-port views as the feature's repository set. All
// twelve ports are wired: the A9 proof host needs the v2 ports (OAuth, machine
// identity, security events, invitations) live, not nil.
func (s *Store) Repositories() auth.Repositories {
	return auth.Repositories{
		Users:              userRepo{s.d},
		Passwords:          passwordRepo{s.d},
		Sessions:           sessionRepo{s.d},
		VerificationCodes:  codeRepo{s.d},
		VerificationTokens: tokenRepo{s.d},
		OAuthAccounts:      oauthAccountRepo{s.d},
		OAuthStates:        oauthStateRepo{s.d},
		ServiceAccounts:    serviceAccountRepo{s.d},
		APIKeys:            apiKeyRepo{s.d},
		SecurityEvents:     securityEventRepo{s.d},
		Invitations:        invitationRepo{s.d},
	}
}

// --- user.UserRepository ---

type userRepo struct{ *data }

func (r userRepo) Create(_ context.Context, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.users {
		if strings.EqualFold(ex.Email, u.Email) {
			return user.User{}, sdk.ErrAlreadyExists
		}
	}
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if u.ID == "" {
		u.ID = ids.MustGenerate()
	}
	r.users[u.ID] = u
	return u, nil
}

func (r userRepo) Get(_ context.Context, id string) (user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}

func (r userRepo) GetByEmail(_ context.Context, email string) (user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return user.User{}, sdk.ErrNotFound
}

func (r userRepo) Update(_ context.Context, id string, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[id]; !ok {
		return user.User{}, sdk.ErrNotFound
	}
	r.users[id] = u
	return u, nil
}

// --- user.PasswordRepository ---

type passwordRepo struct{ *data }

func (r passwordRepo) Set(_ context.Context, userID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passwords[userID] = hash
	return nil
}

func (r passwordRepo) Get(_ context.Context, userID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.passwords[userID]
	if !ok {
		return "", sdk.ErrNotFound
	}
	return h, nil
}

// --- session.SessionRepository ---

type sessionRepo struct{ *data }

func (r sessionRepo) Create(_ context.Context, s session.Session) (session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.Token] = s
	return s, nil
}

func (r sessionRepo) Get(_ context.Context, token string) (session.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[token]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if s.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return s, nil
}

func (r sessionRepo) Delete(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[token]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.sessions, token)
	return nil
}

func (r sessionRepo) DeleteByUser(_ context.Context, userID string) error {
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

type codeRepo struct{ *data }

func (r codeRepo) Create(_ context.Context, c verification.Code) (verification.Code, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[c.Code] = c
	return c, nil
}

func (r codeRepo) Get(_ context.Context, code string) (verification.Code, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.codes[code]
	if !ok {
		return verification.Code{}, sdk.ErrNotFound
	}
	if c.Expired(time.Now()) {
		return verification.Code{}, sdk.ErrExpired
	}
	return c, nil
}

func (r codeRepo) Delete(_ context.Context, code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.codes[code]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.codes, code)
	return nil
}

// --- verification.TokenRepository ---

type tokenRepo struct{ *data }

func (r tokenRepo) Create(_ context.Context, t verification.Token) (verification.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[t.Token] = t
	return t, nil
}

func (r tokenRepo) Get(_ context.Context, token string) (verification.Token, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tokens[token]
	if !ok {
		return verification.Token{}, sdk.ErrNotFound
	}
	if t.Expired(time.Now()) {
		return verification.Token{}, sdk.ErrExpired
	}
	return t, nil
}

func (r tokenRepo) Delete(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tokens[token]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.tokens, token)
	return nil
}
