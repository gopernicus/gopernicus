package storetest

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/auth"
	"github.com/gopernicus/gopernicus/features/auth/logic/apikey"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthstate"
	"github.com/gopernicus/gopernicus/features/auth/logic/securityevent"
	"github.com/gopernicus/gopernicus/features/auth/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/crud"
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
	mu              sync.RWMutex
	users           map[string]user.User
	passwords       map[string]string
	sessions        map[string]session.Session
	codes           map[string]verification.Code
	tokens          map[string]verification.Token
	oauthAccounts   []oauthaccount.OAuthAccount // keyed by (Provider, ProviderUserID) invariant
	oauthStates     map[string]oauthstate.State
	serviceAccounts map[string]serviceaccount.ServiceAccount // by ID
	apiKeys         map[string]apikey.APIKey                 // by ID
	securityEvents  []securityevent.SecurityEvent            // append-only
}

func newReference() *reference {
	return &reference{
		users:           map[string]user.User{},
		passwords:       map[string]string{},
		sessions:        map[string]session.Session{},
		codes:           map[string]verification.Code{},
		tokens:          map[string]verification.Token{},
		oauthStates:     map[string]oauthstate.State{},
		serviceAccounts: map[string]serviceaccount.ServiceAccount{},
		apiKeys:         map[string]apikey.APIKey{},
	}
}

func (r *reference) repositories() auth.Repositories {
	return auth.Repositories{
		Users:              refUsers{r},
		Passwords:          refPasswords{r},
		Sessions:           refSessions{r},
		VerificationCodes:  refCodes{r},
		VerificationTokens: refTokens{r},
		OAuthAccounts:      refOAuthAccounts{r},
		OAuthStates:        refOAuthStates{r},
		ServiceAccounts:    refServiceAccounts{r},
		APIKeys:            refAPIKeys{r},
		SecurityEvents:     refSecurityEvents{r},
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

// --- oauthaccount.OAuthAccountRepository ---

type refOAuthAccounts struct{ *reference }

func (r refOAuthAccounts) Create(_ context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.oauthAccounts {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, errs.ErrAlreadyExists
		}
	}
	r.oauthAccounts = append(r.oauthAccounts, a)
	return a, nil
}

func (r refOAuthAccounts) GetByProvider(_ context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.oauthAccounts {
		if a.Provider == provider && a.ProviderUserID == providerUserID {
			return a, nil
		}
	}
	return oauthaccount.OAuthAccount{}, errs.ErrNotFound
}

func (r refOAuthAccounts) ListByUser(_ context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []oauthaccount.OAuthAccount{}
	for _, a := range r.oauthAccounts {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r refOAuthAccounts) Delete(_ context.Context, userID, provider string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.oauthAccounts[:0:0]
	deleted := false
	for _, a := range r.oauthAccounts {
		if a.UserID == userID && a.Provider == provider {
			deleted = true
			continue
		}
		kept = append(kept, a)
	}
	if !deleted {
		return errs.ErrNotFound
	}
	r.oauthAccounts = kept
	return nil
}

// --- oauthstate.StateRepository ---

type refOAuthStates struct{ *reference }

func (r refOAuthStates) Create(_ context.Context, s oauthstate.State) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.oauthStates[s.Token] = s
	return s, nil
}

// Consume is get-and-delete: the row is removed regardless of expiry, so an
// expired Consume deletes and reports ErrExpired, and any second Consume →
// ErrNotFound (design §3's pinned contract).
func (r refOAuthStates) Consume(_ context.Context, token string) (oauthstate.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.oauthStates[token]
	if !ok {
		return oauthstate.State{}, errs.ErrNotFound
	}
	delete(r.oauthStates, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, errs.ErrExpired
	}
	return s, nil
}

// --- serviceaccount.ServiceAccountRepository ---

type refServiceAccounts struct{ *reference }

func (r refServiceAccounts) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[sa.ID]; ok {
		return serviceaccount.ServiceAccount{}, errs.ErrAlreadyExists
	}
	r.serviceAccounts[sa.ID] = sa
	return sa, nil
}

func (r refServiceAccounts) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sa, ok := r.serviceAccounts[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	return sa, nil
}

// List sorts the full population by (created_at DESC, id DESC) then pages via the
// keyset cursor — the reference behavior the paginated stores must match.
func (r refServiceAccounts) List(_ context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	r.mu.RLock()
	all := make([]serviceaccount.ServiceAccount, 0, len(r.serviceAccounts))
	for _, sa := range r.serviceAccounts {
		all = append(all, sa)
	}
	r.mu.RUnlock()
	return pageDESC(all, req, func(sa serviceaccount.ServiceAccount) (time.Time, string) {
		return sa.CreatedAt, sa.ID
	})
}

func (r refServiceAccounts) Update(_ context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	r.serviceAccounts[id] = sa
	return sa, nil
}

func (r refServiceAccounts) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.serviceAccounts, id)
	return nil
}

// --- apikey.APIKeyRepository ---

type refAPIKeys struct{ *reference }

func (r refAPIKeys) Create(_ context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.apiKeys {
		if ex.KeyHash == k.KeyHash {
			return apikey.APIKey{}, errs.ErrAlreadyExists
		}
	}
	r.apiKeys[k.ID] = k
	return k, nil
}

// GetByHash selects by key_hash ALONE and returns the record for ANY present row
// — revoked and expired included; unknown hash → ErrNotFound (the pinned
// contract; revocation/expiry are service branches, never store filters).
func (r refAPIKeys) GetByHash(_ context.Context, keyHash string) (apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.apiKeys {
		if k.KeyHash == keyHash {
			return k, nil
		}
	}
	return apikey.APIKey{}, errs.ErrNotFound
}

func (r refAPIKeys) ListByServiceAccount(_ context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	r.mu.RLock()
	all := make([]apikey.APIKey, 0, len(r.apiKeys))
	for _, k := range r.apiKeys {
		if k.ServiceAccountID == serviceAccountID {
			all = append(all, k)
		}
	}
	r.mu.RUnlock()
	return pageDESC(all, req, func(k apikey.APIKey) (time.Time, string) {
		return k.CreatedAt, k.ID
	})
}

func (r refAPIKeys) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.apiKeys[id]
	if !ok {
		return errs.ErrNotFound
	}
	k.RevokedAt = revokedAt.UTC()
	r.apiKeys[id] = k
	return nil
}

func (r refAPIKeys) TouchLastUsed(_ context.Context, id string, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.apiKeys[id]
	if !ok {
		return errs.ErrNotFound
	}
	k.LastUsedAt = usedAt.UTC()
	r.apiKeys[id] = k
	return nil
}

// --- securityevent.SecurityEventRepository ---

type refSecurityEvents struct{ *reference }

// Create appends an audit row. It normalizes Details to a NON-NIL map (nil and
// empty both read back as a non-nil empty map — the uniform round-trip contract
// a SQL store gives via '{}'/NULL handling), and copies the map so a later
// caller mutation cannot rewrite the stored row (append-only).
func (r refSecurityEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	details := map[string]any{}
	for k, v := range evt.Details {
		details[k] = v
	}
	evt.Details = details
	r.securityEvents = append(r.securityEvents, evt)
	return evt, nil
}

// List filters by the (parameterized-in-SQL) ListFilter, sorts the full matching
// population by (created_at DESC, id DESC), then pages — the reference the SQL
// stores must match.
func (r refSecurityEvents) List(_ context.Context, filter securityevent.ListFilter, req crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
	r.mu.RLock()
	matched := make([]securityevent.SecurityEvent, 0, len(r.securityEvents))
	for _, evt := range r.securityEvents {
		if filter.Match(evt) {
			matched = append(matched, evt)
		}
	}
	r.mu.RUnlock()
	return pageDESC(matched, req, func(evt securityevent.SecurityEvent) (time.Time, string) {
		return evt.CreatedAt, evt.ID
	})
}

// pageDESC sorts records by (created_at DESC, id DESC), applies the keyset
// cursor, and trims to a page — the reference keyset semantics the SQL stores
// must reproduce. keyOf returns each record's (created_at, id).
func pageDESC[T any](all []T, req crud.ListRequest, keyOf func(T) (time.Time, string)) (crud.Page[T], error) {
	sort.Slice(all, func(i, j int) bool {
		ti, idi := keyOf(all[i])
		tj, idj := keyOf(all[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return idi > idj
	})

	cur, err := crud.DecodeCursor(req.Cursor, "created_at")
	if err != nil {
		return crud.Page[T]{}, err
	}

	windowed := all
	if cur != nil {
		curTime, _ := cur.OrderValue.(time.Time)
		windowed = windowed[:0:0]
		for _, item := range all {
			t, itemID := keyOf(item)
			if afterCursorDESC(t, itemID, curTime, cur.PK) {
				windowed = append(windowed, item)
			}
		}
	}

	return crud.TrimPage(windowed, req.NormalizedLimit(), func(item T) (string, error) {
		t, itemID := keyOf(item)
		return crud.EncodeCursor("created_at", t, itemID)
	})
}

// afterCursorDESC reports whether (itemTime, itemID) sorts strictly after
// (curTime, curID) under created_at DESC, id DESC — the next-page predicate.
func afterCursorDESC(itemTime time.Time, itemID string, curTime time.Time, curID string) bool {
	if !itemTime.Equal(curTime) {
		return itemTime.Before(curTime)
	}
	return itemID < curID
}
