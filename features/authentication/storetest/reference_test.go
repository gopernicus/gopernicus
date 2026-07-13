package storetest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestReference runs the conformance suite against the in-package reference
// implementation. This is what lets features/authentication self-verify under guard G2
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
	oauthAccounts   []oauthaccount.OAuthAccount // keyed by (Provider, ProviderUserID) invariant
	oauthStates     map[string]oauthstate.State
	serviceAccounts map[string]serviceaccount.ServiceAccount // by ID
	apiKeys         map[string]apikey.APIKey                 // by ID
	securityEvents  []securityevent.SecurityEvent            // append-only
	invitations     map[string]invitation.Invitation         // by ID
	challenges      map[string]challenge.Challenge           // by ID
	grants          map[string]authgrant.Grant               // by ID
	authRevisions   map[string]int64                         // per-user optimistic revision (design §5.6)
	identifiers     map[string][]credential.IdentifierMethod // phase-1 credential-projection stand-in (design §5.6), by user ID
	userIdentifiers map[string]identifier.Identifier         // real identifier rows (design §2.2), by identifier ID
	contactChanges  map[string]contactchange.PendingChange   // pending identifier changes (design §2.4), by (user, kind)
	deliveryJobs    map[string]deliveryjob.Job               // durable outbox, by ID (design §6.1.1)

	// resetFailAt/resetFailErr inject an all-or-nothing rollback failure into
	// refPasswordResets.Redeem (design §5.9): resetFailAt=N fails just after the
	// Nth composition statement, so a test proves that a mid-transaction failure at
	// EACH statement boundary leaves NO partial state. Zero = no injection.
	resetFailAt  int
	resetFailErr error
}

func newReference() *reference {
	return &reference{
		users:           map[string]user.User{},
		passwords:       map[string]string{},
		sessions:        map[string]session.Session{},
		oauthStates:     map[string]oauthstate.State{},
		serviceAccounts: map[string]serviceaccount.ServiceAccount{},
		apiKeys:         map[string]apikey.APIKey{},
		invitations:     map[string]invitation.Invitation{},
		challenges:      map[string]challenge.Challenge{},
		grants:          map[string]authgrant.Grant{},
		authRevisions:   map[string]int64{},
		identifiers:     map[string][]credential.IdentifierMethod{},
		userIdentifiers: map[string]identifier.Identifier{},
		contactChanges:  map[string]contactchange.PendingChange{},
		deliveryJobs:    map[string]deliveryjob.Job{},
	}
}

func (r *reference) repositories() auth.Repositories {
	return auth.Repositories{
		Users:                refUsers{r},
		Identifiers:          refIdentifiers{r},
		Passwords:            refPasswords{r},
		Sessions:             refSessions{r},
		OAuthAccounts:        refOAuthAccounts{r},
		OAuthStates:          refOAuthStates{r},
		ServiceAccounts:      refServiceAccounts{r},
		APIKeys:              refAPIKeys{r},
		SecurityEvents:       refSecurityEvents{r},
		Invitations:          refInvitations{r},
		Challenges:           refChallenges{r},
		PasswordResets:       refPasswordResets{r},
		ContactChanges:       refContactChanges{r},
		AuthenticationGrants: refAuthGrants{r},
		CredentialMutations:  refCredentialMutations{r},
		DeliveryJobs:         refDeliveryJobs{r},
	}
}

// --- user.UserRepository ---

type refUsers struct{ *reference }

func (r refUsers) Get(_ context.Context, id string) (user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}

func (r refUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[id]; !ok {
		return user.User{}, sdk.ErrNotFound
	}
	r.users[id] = u
	return u, nil
}

// CreateWithPrimaryIdentifier commits the user and its first identifier under one
// mutex (design §2.2): when the identifier claims login/recovery, that
// (kind, value) claim must be free — else neither row is written
// (sdk.ErrAlreadyExists). Empty IDs are assigned inline (the greenfield
// DB-generated convention), and the identifier is linked to the new user.
func (r refUsers) CreateWithPrimaryIdentifier(_ context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if identifierClaimsAuth(ident) {
		if _, ok := r.findActiveAuthClaimLocked(ident.Kind, ident.NormalizedValue, nil); ok {
			return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}
	if u.ID == "" {
		u.ID = ids.MustGenerate()
	}
	if ident.ID == "" {
		ident.ID = ids.MustGenerate()
	}
	ident.UserID = u.ID
	r.users[u.ID] = u
	r.authRevisions[u.ID] = u.AuthRevision
	r.userIdentifiers[ident.ID] = ident
	return u, ident, nil
}

// --- identifier.IdentifierRepository ---

// refIdentifiers keys real identifier rows by ID and hand-enforces the §2.1
// partial-index semantics a SQL store gets from its indexes and a transactional
// apply: active-only login/recovery lookup, one active primary per (user, kind),
// the partial unique authentication claim, and revision-CAS ApplyVerifiedChange —
// all under the shared reference mutex so the user + identifier + auth_revision
// mutation is atomic.
type refIdentifiers struct{ *reference }

// identifierClaimsAuth reports whether i occupies the login/recovery claim.
func identifierClaimsAuth(i identifier.Identifier) bool {
	return i.LoginEnabled || i.RecoveryEnabled
}

// findActiveAuthClaimLocked returns the active login/recovery identifier claiming
// (kind, value), skipping any ID in exclude; callers hold r.mu.
func (r *reference) findActiveAuthClaimLocked(kind identifier.Kind, value string, exclude map[string]bool) (identifier.Identifier, bool) {
	for id, ex := range r.userIdentifiers {
		if exclude[id] {
			continue
		}
		if ex.Active() && identifierClaimsAuth(ex) && ex.Kind == kind && ex.NormalizedValue == value {
			return ex, true
		}
	}
	return identifier.Identifier{}, false
}

func (r refIdentifiers) Get(_ context.Context, id string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.userIdentifiers[id]
	if !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	return it, nil
}

func (r refIdentifiers) GetLogin(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, it := range r.userIdentifiers {
		if it.Active() && it.LoginEnabled && string(it.Kind) == kind && it.NormalizedValue == normalizedValue {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r refIdentifiers) GetRecovery(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, it := range r.userIdentifiers {
		if it.Active() && it.RecoveryEnabled && string(it.Kind) == kind && it.NormalizedValue == normalizedValue {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r refIdentifiers) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []identifier.Identifier{}
	for _, it := range r.userIdentifiers {
		if it.Active() && it.UserID == userID {
			out = append(out, it)
		}
	}
	return out, nil
}

// ApplyVerifiedChange runs the whole confirm mutation inside one mutex-held
// critical section: revision CAS, authentication-claim arbitration, retirement of
// the replaced identifier and any displaced same-kind primary, insertion of the
// new active row, and a single revision increment (design §2.2).
func (r refIdentifiers) ApplyVerifiedChange(_ context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[input.UserID]; !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	if r.authRevisions[input.UserID] != expectedAuthRevision {
		return identifier.Identifier{}, sdk.ErrConflict
	}

	// Rows to retire: the explicitly replaced identifier, plus the displaced
	// active primary of the same kind when this change becomes primary.
	retire := map[string]bool{}
	if input.ReplacesIdentifierID != "" {
		retire[input.ReplacesIdentifierID] = true
	}
	if input.MakePrimary {
		for id, it := range r.userIdentifiers {
			if it.Active() && it.UserID == input.UserID && it.Kind == input.Kind && it.IsPrimary {
				retire[id] = true
			}
		}
	}

	// Arbitrate the authentication claim before mutating (the partial unique index).
	if input.LoginEnabled || input.RecoveryEnabled {
		if _, taken := r.findActiveAuthClaimLocked(input.Kind, input.NormalizedValue, retire); taken {
			return identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}

	now := verifiedAt.UTC()
	for id := range retire {
		it := r.userIdentifiers[id]
		it.Retire(now)
		r.userIdentifiers[id] = it
	}
	newIdent := identifier.Identifier{
		ID:                  ids.MustGenerate(),
		UserID:              input.UserID,
		Kind:                input.Kind,
		NormalizedValue:     input.NormalizedValue,
		VerifiedAt:          now,
		LoginEnabled:        input.LoginEnabled,
		RecoveryEnabled:     input.RecoveryEnabled,
		NotificationEnabled: input.NotificationEnabled,
		IsPrimary:           input.MakePrimary,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	r.userIdentifiers[newIdent.ID] = newIdent
	r.authRevisions[input.UserID] = expectedAuthRevision + 1
	return newIdent, nil
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
		return "", sdk.ErrNotFound
	}
	return h, nil
}

// --- session.SessionRepository ---

// refSessions keys sessions by their app-minted ID (r.sessions) and hand-enforces
// the refresh-rotation invariants a SQL store gets from its indexes and CAS
// UPDATEs: a unique current-hash, the single previous (grace) slot with a consumed
// flag, compare-and-swap Rotate/ConsumeGrace, and the empty-previous guard.
type refSessions struct{ *reference }

func (r refSessions) Create(_ context.Context, s session.Session) (session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Unique live refresh hash (the store's UNIQUE index; MapError → conflict).
	for _, ex := range r.sessions {
		if ex.RefreshTokenHash == s.RefreshTokenHash {
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	r.sessions[s.ID] = s
	return s, nil
}

func (r refSessions) Get(_ context.Context, id string) (session.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if s.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return s, nil
}

// GetByRefreshHash scans for a row whose current OR previous slot equals hash and
// reports which matched. It returns the row VERBATIM (no expiry filter — expiry is
// a service branch), and an empty hash NEVER matches (the fresh-row NULL previous
// slot must not be returned for GetByRefreshHash("")).
func (r refSessions) GetByRefreshHash(_ context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if hash == "" {
		return session.Session{}, 0, sdk.ErrNotFound
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.sessions {
		if s.RefreshTokenHash == hash {
			return s, session.RefreshMatchCurrent, nil
		}
	}
	for _, s := range r.sessions {
		if s.PreviousRefreshTokenHash != "" && s.PreviousRefreshTokenHash == hash {
			return s, session.RefreshMatchPrevious, nil
		}
	}
	return session.Session{}, 0, sdk.ErrNotFound
}

// Rotate is compare-and-swap on the current hash: it applies only when the row's
// live hash still equals expectedCurrentHash, then sets previous←expected,
// previous_used←false, rotation_count++, current←newHash, WITHOUT touching
// ExpiresAt. A stale expected hash affects zero rows → ErrRotationConflict.
func (r refSessions) Rotate(_ context.Context, id, expectedCurrentHash, newHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.RefreshTokenHash != expectedCurrentHash {
		return session.ErrRotationConflict
	}
	s.PreviousRefreshTokenHash = expectedCurrentHash
	s.PreviousUsed = false
	s.RotationCount++
	s.RefreshTokenHash = newHash
	r.sessions[id] = s
	return nil
}

// ConsumeGrace is compare-and-swap on the previous slot: it flips previous_used
// only when previous equals previousHash AND previous_used is still false. Zero
// rows → ErrRotationConflict.
func (r refSessions) ConsumeGrace(_ context.Context, id, previousHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.PreviousUsed || s.PreviousRefreshTokenHash == "" || s.PreviousRefreshTokenHash != previousHash {
		return session.ErrRotationConflict
	}
	s.PreviousUsed = true
	r.sessions[id] = s
	return nil
}

func (r refSessions) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[id]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.sessions, id)
	return nil
}

func (r refSessions) DeleteByUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, id)
		}
	}
	return nil
}

// --- oauthaccount.OAuthAccountRepository ---

type refOAuthAccounts struct{ *reference }

func (r refOAuthAccounts) Create(_ context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.oauthAccounts {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, sdk.ErrAlreadyExists
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
	return oauthaccount.OAuthAccount{}, sdk.ErrNotFound
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
		return sdk.ErrNotFound
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
		return oauthstate.State{}, sdk.ErrNotFound
	}
	delete(r.oauthStates, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, sdk.ErrExpired
	}
	return s, nil
}

// --- contactchange.Repository ---

// refContactChanges keys pending changes by (user, kind) so Create is an atomic
// replace-per-pair and Consume is single-use get-and-delete regardless of expiry —
// all under the shared reference mutex (design §2.4).
type refContactChanges struct{ *reference }

func contactChangeKey(userID string, kind identifier.Kind) string {
	return userID + "\x00" + string(kind)
}

func (r refContactChanges) Create(_ context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Empty ID → mimic a schema default (the greenfield DB-generated convention).
	if p.ID == "" {
		p.ID = ids.MustGenerate()
	}
	// Replace-per-(user, kind): the composite key overwrites any prior pending row.
	r.contactChanges[contactChangeKey(p.UserID, p.Kind)] = p
	return p, nil
}

// Consume is get-and-delete: the row is removed regardless of expiry, so an
// expired Consume deletes and reports ErrExpired, and any second Consume →
// ErrNotFound (design §2.4's pinned contract, the oauthstate.Consume precedent).
func (r refContactChanges) Consume(_ context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := contactChangeKey(userID, kind)
	p, ok := r.contactChanges[key]
	if !ok {
		return contactchange.PendingChange{}, sdk.ErrNotFound
	}
	delete(r.contactChanges, key)
	if p.Expired(time.Now()) {
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return p, nil
}

// --- serviceaccount.ServiceAccountRepository ---

type refServiceAccounts struct{ *reference }

func (r refServiceAccounts) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if sa.ID == "" {
		sa.ID = ids.MustGenerate()
	}
	if _, ok := r.serviceAccounts[sa.ID]; ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrAlreadyExists
	}
	r.serviceAccounts[sa.ID] = sa
	return sa, nil
}

func (r refServiceAccounts) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sa, ok := r.serviceAccounts[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
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
	return pageMem(all, req, func(sa serviceaccount.ServiceAccount) (time.Time, string) {
		return sa.CreatedAt, sa.ID
	})
}

func (r refServiceAccounts) Update(_ context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	r.serviceAccounts[id] = sa
	return sa, nil
}

func (r refServiceAccounts) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return sdk.ErrNotFound
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
			return apikey.APIKey{}, sdk.ErrAlreadyExists
		}
	}
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if k.ID == "" {
		k.ID = ids.MustGenerate()
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
	return apikey.APIKey{}, sdk.ErrNotFound
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
	return pageMem(all, req, func(k apikey.APIKey) (time.Time, string) {
		return k.CreatedAt, k.ID
	})
}

func (r refAPIKeys) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.apiKeys[id]
	if !ok {
		return sdk.ErrNotFound
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
		return sdk.ErrNotFound
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
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if evt.ID == "" {
		evt.ID = ids.MustGenerate()
	}
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
	return pageMem(matched, req, func(evt securityevent.SecurityEvent) (time.Time, string) {
		return evt.CreatedAt, evt.ID
	})
}

// --- invitation.InvitationRepository ---

type refInvitations struct{ *reference }

// Create enforces the PARTIAL pending-tuple uniqueness (design §6, migration
// 0013): a second PENDING invitation for the same (resource_type, resource_id,
// identifier_kind, identifier, relation) → ErrAlreadyExists; once a prior one
// moves off pending, a new one succeeds. Non-pending rows never block a Create.
// identifier_kind is part of the key, so the same value across two kinds coexists.
func (r refInvitations) Create(_ context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.invitations {
		if ex.Status == invitation.StatusPending &&
			ex.ResourceType == inv.ResourceType &&
			ex.ResourceID == inv.ResourceID &&
			ex.IdentifierKind == inv.IdentifierKind &&
			ex.Identifier == inv.Identifier &&
			ex.Relation == inv.Relation {
			return invitation.Invitation{}, sdk.ErrAlreadyExists
		}
	}
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if inv.ID == "" {
		inv.ID = ids.MustGenerate()
	}
	r.invitations[inv.ID] = inv
	return inv, nil
}

func (r refInvitations) Get(_ context.Context, id string) (invitation.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	return inv, nil
}

// GetByTokenHash returns the invitation for tokenHash: unknown → ErrNotFound, a
// present row past ExpiresAt → ErrExpired (the read-time expiry), else the
// record. Expiry is checked against time.Now, matching a SQL store filtering on
// the read clock.
func (r refInvitations) GetByTokenHash(_ context.Context, tokenHash string) (invitation.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inv := range r.invitations {
		if inv.TokenHash == tokenHash {
			if inv.Expired(time.Now()) {
				return invitation.Invitation{}, sdk.ErrExpired
			}
			return inv, nil
		}
	}
	return invitation.Invitation{}, sdk.ErrNotFound
}

func (r refInvitations) ListByResource(_ context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	r.mu.RLock()
	all := make([]invitation.Invitation, 0, len(r.invitations))
	for _, inv := range r.invitations {
		if inv.ResourceType == resourceType && inv.ResourceID == resourceID {
			all = append(all, inv)
		}
	}
	r.mu.RUnlock()
	return pageMem(all, req, func(inv invitation.Invitation) (time.Time, string) {
		return inv.CreatedAt, inv.ID
	})
}

func (r refInvitations) ListBySubject(_ context.Context, kind, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	r.mu.RLock()
	all := make([]invitation.Invitation, 0, len(r.invitations))
	for _, inv := range r.invitations {
		if inv.IdentifierKind == kind && inv.Identifier == identifier {
			all = append(all, inv)
		}
	}
	r.mu.RUnlock()
	return pageMem(all, req, func(inv invitation.Invitation) (time.Time, string) {
		return inv.CreatedAt, inv.ID
	})
}

func (r refInvitations) UpdateStatus(_ context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.invitations[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	inv.Status = upd.Status
	inv.TokenHash = upd.TokenHash
	inv.ExpiresAt = upd.ExpiresAt
	inv.AcceptedAt = upd.AcceptedAt
	inv.ResolvedSubjectID = upd.ResolvedSubjectID
	inv.UpdatedAt = upd.UpdatedAt
	r.invitations[id] = inv
	return inv, nil
}

// pageMem sorts the full population by (created_at, id) in the resolved
// direction, then applies the sdk/foundation/crud list matrix — cursor or offset mode, the
// reverse-probe prev page, and the optional count — the reference semantics the
// SQL stores and the host memstores must reproduce. keyOf returns each record's
// (created_at, id); created_at is the only sortable field.
func pageMem[T any](all []T, req crud.ListRequest, keyOf func(T) (time.Time, string)) (crud.Page[T], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[T]{}, err
	}
	if req.Order.Field != "" && req.Order.Field != "created_at" {
		return crud.Page[T]{}, fmt.Errorf("unknown order field %q: %w", req.Order.Field, sdk.ErrInvalidInput)
	}
	asc := req.Order.Direction == crud.ASC

	sort.Slice(all, func(i, j int) bool {
		ti, idi := keyOf(all[i])
		tj, idj := keyOf(all[j])
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return idi < idj
		}
		return idi > idj
	})

	total := int64(len(all))
	limit := req.NormalizedLimit(crud.Limits{})
	encode := func(item T) (string, error) {
		t, itemID := keyOf(item)
		return crud.EncodeCursor("created_at", t, itemID)
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		window := all
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		page, err := crud.TrimPage(window, limit, encode)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
		if req.WithCount {
			page.Total = &total
		}
		return page, nil
	}

	cur, err := crud.DecodeCursor(req.Cursor, "created_at")
	if err != nil {
		return crud.Page[T]{}, err
	}

	forward := all
	if cur != nil {
		curTime, _ := cur.OrderValue.(time.Time)
		forward = forward[:0:0]
		for _, item := range all {
			t, itemID := keyOf(item)
			if afterCursorMem(t, itemID, curTime, cur.PK, asc) {
				forward = append(forward, item)
			}
		}
	}
	window := forward
	if len(window) > limit+1 {
		window = window[:limit+1]
	}
	page, err := crud.TrimPage(window, limit, encode)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if cur != nil {
		curTime, _ := cur.OrderValue.(time.Time)
		var before []T
		for _, item := range all {
			t, itemID := keyOf(item)
			if beforeCursorMem(t, itemID, curTime, cur.PK, asc) {
				before = append(before, item)
			}
		}
		// The previous page is the `limit` rows immediately before the cursor.
		if len(before) > limit {
			before = before[len(before)-limit:]
		}
		if err := crud.MarkPrevPage(&page, before, limit, encode); err != nil {
			return crud.Page[T]{}, err
		}
	}

	if req.WithCount {
		page.Total = &total
	}
	return page, nil
}

// afterCursorMem reports whether (itemTime, itemID) sorts strictly after the
// cursor under the resolved direction — the next-page predicate.
func afterCursorMem(itemTime time.Time, itemID string, curTime time.Time, curID string, asc bool) bool {
	if !itemTime.Equal(curTime) {
		if asc {
			return itemTime.After(curTime)
		}
		return itemTime.Before(curTime)
	}
	if asc {
		return itemID > curID
	}
	return itemID < curID
}

// beforeCursorMem reports whether (itemTime, itemID) sorts strictly before the
// cursor under the resolved direction — the reverse-probe predicate.
func beforeCursorMem(itemTime time.Time, itemID string, curTime time.Time, curID string, asc bool) bool {
	if !itemTime.Equal(curTime) {
		if asc {
			return itemTime.Before(curTime)
		}
		return itemTime.After(curTime)
	}
	if asc {
		return itemID < curID
	}
	return itemID > curID
}

// --- challenge.Repository ---

// refChallenges keys challenges by ID and hand-enforces the atomic-secret
// invariants a SQL store gets from its indexes and transactional consume: one
// active row per (user, purpose), a unique (purpose, secret_digest) claim, and a
// consume that decides expiry, digest comparison, attempt counting, lockout, and
// deletion inside ONE mutex-held critical section — the "exactly one winner"
// contract. Digest comparison routes through auth.ConstantTimeDigestEqual, whose
// empty-hash guard makes an empty candidate never match.
type refChallenges struct{ *reference }

func (r refChallenges) Replace(_ context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Delete the prior (user, purpose) row (the single-active claim).
	for id, ex := range r.challenges {
		if ex.UserID == c.UserID && ex.Purpose == c.Purpose {
			delete(r.challenges, id)
		}
	}
	// Enforce the (purpose, secret_digest) unique index against the remainder.
	for _, ex := range r.challenges {
		if ex.Purpose == c.Purpose && ex.SecretDigest == c.SecretDigest {
			return challenge.Challenge{}, sdk.ErrAlreadyExists
		}
	}
	if c.ID == "" {
		c.ID = ids.MustGenerate()
	}
	if c.Version == 0 {
		c.Version = 1
	}
	r.challenges[c.ID] = c
	return c, nil
}

func (r refChallenges) ConsumeCode(_ context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id, row, found := r.findByUserPurpose(userID, purpose)
	if !found {
		return challenge.Consumed{}, challenge.OutcomeNotFound, nil
	}
	if row.Expired(now) {
		delete(r.challenges, id)
		return challenge.Consumed{}, challenge.OutcomeExpired, nil
	}
	// Select the candidate naming the row's key, then compare in constant time.
	matched := false
	for _, cand := range candidates {
		if cand.KeyID == row.ProtectorKeyID && auth.ConstantTimeDigestEqual(cand.Digest, row.SecretDigest) {
			matched = true
			break
		}
	}
	if !matched {
		newCount := row.AttemptCount + 1
		if newCount >= maxAttempts {
			delete(r.challenges, id)
			return challenge.Consumed{}, challenge.OutcomeLockedOut, nil
		}
		row.AttemptCount = newCount
		r.challenges[id] = row
		return challenge.Consumed{}, challenge.OutcomeRejected, nil
	}
	// Correct code — the row is consumed regardless of context (anti-probing).
	delete(r.challenges, id)
	if expectedContextDigest != "" && string(row.Context) != expectedContextDigest {
		return consumedOf(row, now), challenge.OutcomeContextMismatch, nil
	}
	return consumedOf(row, now), challenge.OutcomeRedeemed, nil
}

func (r refChallenges) ConsumeToken(_ context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if presentedDigest == "" {
		return challenge.Consumed{}, sdk.ErrNotFound
	}
	for id, ex := range r.challenges {
		if ex.Purpose == purpose && ex.SecretDigest == presentedDigest {
			delete(r.challenges, id) // delete-returning regardless of expiry
			if ex.Expired(now) {
				return challenge.Consumed{}, sdk.ErrExpired
			}
			return consumedOf(ex, now), nil
		}
	}
	return challenge.Consumed{}, sdk.ErrNotFound
}

func (r refChallenges) PurgeExpired(_ context.Context, before time.Time, limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for id, ex := range r.challenges {
		if limit > 0 && n >= limit {
			break
		}
		if !ex.ExpiresAt.After(before) { // expires_at <= before
			delete(r.challenges, id)
			n++
		}
	}
	return n, nil
}

// findByUserPurpose returns the single (user, purpose) row; callers hold r.mu.
func (r refChallenges) findByUserPurpose(userID, purpose string) (string, challenge.Challenge, bool) {
	for id, ex := range r.challenges {
		if ex.UserID == userID && ex.Purpose == purpose {
			return id, ex, true
		}
	}
	return "", challenge.Challenge{}, false
}

func consumedOf(c challenge.Challenge, now time.Time) challenge.Consumed {
	return challenge.Consumed{
		ID:             c.ID,
		UserID:         c.UserID,
		Purpose:        c.Purpose,
		Context:        c.Context,
		ProtectorKeyID: c.ProtectorKeyID,
		ConsumedAt:     now.UTC(),
	}
}

// --- passwordreset.Repository ---

// refPasswordResets performs the atomic reset composition inside ONE mutex-held
// critical section (design §5.9): a SQL store gets the all-or-nothing guarantee
// from its transaction; the reference applies the five composition statements in
// order but SNAPSHOTS the four affected collections first and restores them on any
// injected failure, so a failure at each statement boundary provably leaves no
// partial state. A guarded resolve (unknown/consumed/expired are all not-live)
// yields sdk.ErrNotFound.
type refPasswordResets struct{ *reference }

// resetSnapshot is a shallow copy of the four collections the reset mutates.
type resetSnapshot struct {
	challenges map[string]challenge.Challenge
	passwords  map[string]string
	sessions   map[string]session.Session
	grants     map[string]authgrant.Grant
}

func (r refPasswordResets) snapshot() resetSnapshot {
	snap := resetSnapshot{
		challenges: make(map[string]challenge.Challenge, len(r.challenges)),
		passwords:  make(map[string]string, len(r.passwords)),
		sessions:   make(map[string]session.Session, len(r.sessions)),
		grants:     make(map[string]authgrant.Grant, len(r.grants)),
	}
	for k, v := range r.challenges {
		snap.challenges[k] = v
	}
	for k, v := range r.passwords {
		snap.passwords[k] = v
	}
	for k, v := range r.sessions {
		snap.sessions[k] = v
	}
	for k, v := range r.grants {
		snap.grants[k] = v
	}
	return snap
}

func (r refPasswordResets) restore(snap resetSnapshot) {
	r.challenges = snap.challenges
	r.passwords = snap.passwords
	r.sessions = snap.sessions
	r.grants = snap.grants
}

func (r refPasswordResets) Redeem(_ context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if in.TokenDigest == "" {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	// Resolve the LIVE token challenge: unknown, already-consumed, and expired are
	// all not-live (the guarded delete leaves an expired row for purge).
	var (
		chID   string
		userID string
	)
	found := false
	for id, ex := range r.challenges {
		if ex.Purpose == in.Purpose && ex.SecretDigest == in.TokenDigest {
			if ex.Expired(in.Now) {
				return passwordreset.RedeemResult{}, sdk.ErrNotFound
			}
			chID, userID, found = id, ex.UserID, true
			break
		}
	}
	if !found {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}

	// Reassign the maps so restore() can swap the originals back wholesale; each
	// composition statement mutates the working copies, and an injected failure at
	// boundary N restores every collection to the pre-Redeem snapshot.
	snap := r.snapshot()
	work := r.snapshot()
	r.challenges, r.passwords, r.sessions, r.grants = work.challenges, work.passwords, work.sessions, work.grants

	step := 0
	fail := func() bool { step++; return r.resetFailAt == step }
	rollback := func() (passwordreset.RedeemResult, error) {
		r.restore(snap)
		return passwordreset.RedeemResult{}, r.resetFailErr
	}

	// 1. delete/return the live reset challenge.
	delete(r.challenges, chID)
	if fail() {
		return rollback()
	}
	// 2. set the typed password row.
	r.passwords[userID] = in.NewPasswordHash
	if fail() {
		return rollback()
	}
	// 3. delete every session.
	for id, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, id)
		}
	}
	if fail() {
		return rollback()
	}
	// 4a. delete every recent-authentication grant.
	for id, g := range r.grants {
		if g.UserID == userID {
			delete(r.grants, id)
		}
	}
	if fail() {
		return rollback()
	}
	// 4b. purge outstanding password/reset challenges.
	for id, ex := range r.challenges {
		if ex.UserID == userID && containsString(in.PurgeChallengePurposes, ex.Purpose) {
			delete(r.challenges, id)
		}
	}
	if fail() {
		return rollback()
	}
	return passwordreset.RedeemResult{UserID: userID}, nil
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestPasswordResetRollback injects a failure at EACH of the five composition
// statement boundaries and proves the whole reset rolls back — the password is
// unchanged, the sessions and grant survive, and the reset and remove-password
// challenges survive — so there is never a changed-password/live-old-session
// partial state (design §5.9). A SQL store gets this from its transaction; the
// reference proves the contract is expressible and honored.
func TestPasswordResetRollback(t *testing.T) {
	const (
		userID    = "u-reset"
		resetDig  = "reset-digest"
		removeDig = "remove-digest"
		oldHash   = "hash:old"
		newHash   = "hash:new"
	)
	for failAt := 1; failAt <= 5; failAt++ {
		r := newReference()
		now := time.Now()
		r.passwords[userID] = oldHash
		r.sessions["s1"] = newSession(userID, "rh-1", time.Hour, now)
		r.sessions["s2"] = newSession(userID, "rh-2", time.Hour, now)
		r.grants["g1"] = newGrant("s1", userID, challenge.PurposeRemovePassword, "ctx", 5*time.Minute, now)
		r.challenges["c-reset"] = newChallenge(userID, challenge.PurposePasswordReset, "", resetDig, nil, 0, time.Hour, now)
		r.challenges["c-remove"] = newChallenge(userID, challenge.PurposeRemovePassword, "k1", removeDig, nil, 0, time.Hour, now)
		r.resetFailAt = failAt
		r.resetFailErr = fmt.Errorf("injected failure at boundary %d", failAt)

		repo := refPasswordResets{r}
		_, err := repo.Redeem(context.Background(), passwordreset.RedeemInput{
			Purpose:                challenge.PurposePasswordReset,
			TokenDigest:            resetDig,
			NewPasswordHash:        newHash,
			PurgeChallengePurposes: []string{challenge.PurposePasswordReset, challenge.PurposeRemovePassword},
			Now:                    now,
		})
		if !errors.Is(err, r.resetFailErr) {
			t.Fatalf("failAt=%d: err=%v, want the injected failure", failAt, err)
		}
		if r.passwords[userID] != oldHash {
			t.Errorf("failAt=%d: password = %q, want unchanged %q (rollback)", failAt, r.passwords[userID], oldHash)
		}
		if len(r.sessions) != 2 {
			t.Errorf("failAt=%d: session count = %d, want 2 (rollback)", failAt, len(r.sessions))
		}
		if len(r.grants) != 1 {
			t.Errorf("failAt=%d: grant count = %d, want 1 (rollback)", failAt, len(r.grants))
		}
		if len(r.challenges) != 2 {
			t.Errorf("failAt=%d: challenge count = %d, want 2 (reset+remove survive rollback)", failAt, len(r.challenges))
		}
	}

	// The un-injected path commits every effect exactly once.
	r := newReference()
	now := time.Now()
	r.passwords[userID] = oldHash
	r.sessions["s1"] = newSession(userID, "rh-1", time.Hour, now)
	r.grants["g1"] = newGrant("s1", userID, challenge.PurposeRemovePassword, "ctx", 5*time.Minute, now)
	r.challenges["c-reset"] = newChallenge(userID, challenge.PurposePasswordReset, "", resetDig, nil, 0, time.Hour, now)
	r.challenges["c-remove"] = newChallenge(userID, challenge.PurposeRemovePassword, "k1", removeDig, nil, 0, time.Hour, now)
	res, err := refPasswordResets{r}.Redeem(context.Background(), passwordreset.RedeemInput{
		Purpose:                challenge.PurposePasswordReset,
		TokenDigest:            resetDig,
		NewPasswordHash:        newHash,
		PurgeChallengePurposes: []string{challenge.PurposePasswordReset, challenge.PurposeRemovePassword},
		Now:                    now,
	})
	if err != nil || res.UserID != userID {
		t.Fatalf("commit path: res=%+v err=%v, want %s", res, err, userID)
	}
	if r.passwords[userID] != newHash || len(r.sessions) != 0 || len(r.grants) != 0 || len(r.challenges) != 0 {
		t.Errorf("commit path partial: pw=%q sessions=%d grants=%d challenges=%d",
			r.passwords[userID], len(r.sessions), len(r.grants), len(r.challenges))
	}
}

// --- authgrant.Repository ---

// refAuthGrants keys grants by ID and hand-enforces the single-use, session-bound
// consume: the atomic operation matches (session, purpose, context) among
// unconsumed rows, decides expiry, and marks the row consumed — so a second
// consume, a context mismatch, and an expired grant all behave as the port
// promises.
type refAuthGrants struct{ *reference }

func (r refAuthGrants) Create(_ context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g.ID == "" {
		g.ID = ids.MustGenerate()
	}
	r.grants[g.ID] = g
	return g, nil
}

func (r refAuthGrants) Consume(_ context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, g := range r.grants {
		if g.Consumed() || g.SessionID != sessionID || g.Purpose != purpose || g.ContextDigest != contextDigest {
			continue
		}
		g.ConsumedAt = now.UTC() // single-use: mark before returning
		r.grants[id] = g
		if g.Expired(now) {
			return authgrant.Grant{}, sdk.ErrExpired
		}
		return g, nil
	}
	return authgrant.Grant{}, sdk.ErrNotFound
}

func (r refAuthGrants) DeleteBySession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, g := range r.grants {
		if g.SessionID == sessionID {
			delete(r.grants, id)
		}
	}
	return nil
}

// --- credential.MutationRepository ---

// refCredentialMutations projects a user's MethodSet from the credential source
// tables the reference already holds — the password map, the oauth links, and
// the identifiers stand-in — plus the per-user auth_revision. Apply performs one
// revision-CAS mutation inside a single mutex-held critical section: it rejects a
// stale revision, mutates exactly the targeted typed source, and increments the
// revision exactly once, so a concurrent double-apply produces exactly one winner
// and never a partial mutation (design §5.6). The policy is NOT run here — it is
// the service's job before Apply; this port only serializes.
type refCredentialMutations struct{ *reference }

// snapshotLocked builds the projection; callers hold r.mu.
func (r refCredentialMutations) snapshotLocked(userID string) credential.MethodSet {
	set := credential.MethodSet{
		AuthRevision: r.authRevisions[userID],
		HasPassword:  r.passwords[userID] != "",
	}
	for _, a := range r.oauthAccounts {
		if a.UserID == userID {
			set.OAuth = append(set.OAuth, credential.OAuthMethod{Provider: a.Provider, Assurance: session.AssuranceAAL1})
		}
	}
	set.Identifiers = append(set.Identifiers, r.identifiers[userID]...)
	return set
}

func (r refCredentialMutations) userExistsLocked(userID string) bool {
	if _, ok := r.users[userID]; ok {
		return true
	}
	// A user proven only by its credential state (password/oauth/identifiers) still
	// counts, so a seed that skips Users.Create is snapshottable.
	if r.passwords[userID] != "" || len(r.identifiers[userID]) > 0 {
		return true
	}
	for _, a := range r.oauthAccounts {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

func (r refCredentialMutations) Snapshot(_ context.Context, userID string) (credential.MethodSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.userExistsLocked(userID) {
		return credential.MethodSet{}, sdk.ErrNotFound
	}
	return r.snapshotLocked(userID), nil
}

func (r refCredentialMutations) Apply(_ context.Context, userID string, expectedAuthRevision int64, mutation credential.Mutation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.userExistsLocked(userID) {
		return sdk.ErrNotFound
	}
	if r.authRevisions[userID] != expectedAuthRevision {
		return sdk.ErrConflict
	}
	switch m := mutation.(type) {
	case credential.RemovePassword:
		delete(r.passwords, userID)
	case credential.UnlinkOAuth:
		kept := r.oauthAccounts[:0:0]
		for _, a := range r.oauthAccounts {
			if a.UserID == userID && a.Provider == m.Provider {
				continue
			}
			kept = append(kept, a)
		}
		r.oauthAccounts = kept
	case credential.RetireIdentifier:
		r.identifiers[userID] = retireRefIdentifier(r.identifiers[userID], m.IdentifierID, m.ReplacementPrimaryID)
	case credential.ChangeIdentifierUses:
		r.identifiers[userID] = changeRefIdentifierUses(r.identifiers[userID], m.IdentifierID, m.Uses, m.MakePrimary)
	}
	r.authRevisions[userID] = expectedAuthRevision + 1
	return nil
}

func retireRefIdentifier(ids []credential.IdentifierMethod, id, replacementPrimaryID string) []credential.IdentifierMethod {
	out := make([]credential.IdentifierMethod, 0, len(ids))
	for _, m := range ids {
		if m.ID == id {
			continue
		}
		if m.ID == replacementPrimaryID {
			m.Primary = true
		}
		out = append(out, m)
	}
	return out
}

func changeRefIdentifierUses(ids []credential.IdentifierMethod, id string, uses credential.IdentifierUses, makePrimary bool) []credential.IdentifierMethod {
	out := make([]credential.IdentifierMethod, 0, len(ids))
	for _, m := range ids {
		if m.ID == id {
			m.Uses = uses
			if makePrimary {
				m.Primary = true
			}
		} else if makePrimary {
			m.Primary = false
		}
		out = append(out, m)
	}
	return out
}

// TestCredentialPolicyConcurrentSelfRemoval is the crown-jewel security proof of
// §5.6: two verified email identifiers, each a login and recovery method, are
// retired concurrently through the Snapshot → policy → revision-CAS Apply loop.
// Either retirement is individually safe, but retiring BOTH would strand the
// account, so after the first commits (bumping auth_revision) the second reloads,
// re-runs the policy against the now-single-method set, and is rejected. Exactly
// one mutation commits; the stale safe-looking snapshot cannot win. It seeds the
// reference's identifier stand-in directly because the identifier store is phase
// 1; the generic Snapshot/Apply mechanics are proven through the port in Run.
func TestCredentialPolicyConcurrentSelfRemoval(t *testing.T) {
	r := newReference()
	const uid = "u-policy"
	r.users[uid] = user.User{ID: uid}
	r.identifiers[uid] = []credential.IdentifierMethod{
		{ID: "id-a", Kind: identity.KindEmail, Verified: true, Primary: true, Uses: credential.IdentifierUses{Login: true, Recovery: true}},
		{ID: "id-b", Kind: identity.KindEmail, Verified: true, Uses: credential.IdentifierUses{Login: true, Recovery: true}},
	}
	repo := refCredentialMutations{r}
	policy := credential.NewDefaultPolicy(credential.PolicyConfig{})

	commits := make([]bool, 2)
	targets := []string{"id-a", "id-b"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			commits[i] = applyWithPolicy(repo, policy, uid, credential.RetireIdentifier{IdentifierID: targets[i]})
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, ok := range commits {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent self-removal winners = %d, want exactly 1 (%v)", wins, commits)
	}
	final, err := repo.Snapshot(context.Background(), uid)
	if err != nil {
		t.Fatalf("Snapshot after race: %v", err)
	}
	if len(final.Identifiers) != 1 {
		t.Fatalf("final identifiers = %d, want 1 (policy floor held)", len(final.Identifiers))
	}
}

// applyWithPolicy is the bounded Snapshot → policy → revision-CAS Apply retry
// loop a §5.6 service runs: it reloads and re-evaluates on an optimistic
// conflict, and reports whether the mutation ultimately committed. A policy
// rejection is a clean loss (false); an unresolved conflict after the retry
// budget is treated as a loss too.
func applyWithPolicy(repo refCredentialMutations, policy credential.DefaultPolicy, userID string, m credential.Mutation) bool {
	ctx := context.Background()
	for attempt := 0; attempt < 8; attempt++ {
		current, err := repo.Snapshot(ctx, userID)
		if err != nil {
			return false
		}
		proposed := current.With(m)
		if err := policy.EvaluateMutation(ctx, current, proposed); err != nil {
			return false
		}
		err = repo.Apply(ctx, userID, current.AuthRevision, m)
		switch {
		case err == nil:
			return true
		case errors.Is(err, sdk.ErrConflict):
			continue // reload and re-evaluate
		default:
			return false
		}
	}
	return false
}

// --- deliveryjob.Repository ---

// refDeliveryJobs keys jobs by ID and hand-enforces the durable-outbox invariants
// a SQL store gets from its unique idempotency index and transactional claim:
// enqueue is idempotent by IdempotencyKey among non-terminal rows, a claim leases
// exactly the oldest due job under one mutex (so concurrent workers see one
// winner), lease-checked completion rejects a reclaimed job, and an expired lease
// makes a still-pending job claimable again (at-least-once). Each promised atomic
// operation runs inside ONE mutex-held critical section.
type refDeliveryJobs struct{ *reference }

func (r refDeliveryJobs) Enqueue(_ context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.deliveryJobs {
		if !ex.Terminal() && ex.IdempotencyKey == job.IdempotencyKey {
			return ex, nil // idempotent: the existing non-terminal job wins
		}
	}
	return r.insertLocked(job), nil
}

func (r refDeliveryJobs) Replace(_ context.Context, job deliveryjob.Job) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for id, ex := range r.deliveryJobs {
		if !ex.Terminal() && ex.IdempotencyKey == job.IdempotencyKey {
			ex.State = deliveryjob.StateCanceled
			ex.TerminalAt = now
			ex.LeaseID = ""
			ex.LeasedUntil = time.Time{}
			ex.UpdatedAt = now
			r.deliveryJobs[id] = ex
		}
	}
	return r.insertLocked(job), nil
}

// insertLocked stores job as a fresh pending row; callers hold r.mu.
func (r refDeliveryJobs) insertLocked(job deliveryjob.Job) deliveryjob.Job {
	if job.ID == "" {
		job.ID = ids.MustGenerate()
	}
	job.State = deliveryjob.StatePending
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.TerminalAt = time.Time{}
	r.deliveryJobs[job.ID] = job
	return job
}

func (r refDeliveryJobs) Claim(_ context.Context, now time.Time, leaseID string, leaseFor time.Duration) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now = now.UTC()

	var dueIDs []string
	for id, ex := range r.deliveryJobs {
		if ex.Due(now) {
			dueIDs = append(dueIDs, id)
		}
	}
	if len(dueIDs) == 0 {
		return deliveryjob.Job{}, sdk.ErrNotFound
	}
	// Deterministic oldest-first selection: available_at, then created_at, then id.
	sort.Slice(dueIDs, func(i, j int) bool {
		a, b := r.deliveryJobs[dueIDs[i]], r.deliveryJobs[dueIDs[j]]
		if !a.AvailableAt.Equal(b.AvailableAt) {
			return a.AvailableAt.Before(b.AvailableAt)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return dueIDs[i] < dueIDs[j]
	})
	job := r.deliveryJobs[dueIDs[0]]
	job.AttemptCount++
	job.LeaseID = leaseID
	job.LeasedUntil = now.Add(leaseFor)
	job.UpdatedAt = now
	r.deliveryJobs[job.ID] = job
	return job, nil
}

func (r refDeliveryJobs) Succeed(_ context.Context, id, leaseID string, now time.Time) error {
	return r.complete(id, leaseID, deliveryjob.StateSucceeded, "", now)
}

func (r refDeliveryJobs) Fail(_ context.Context, id, leaseID, lastErr string, now time.Time) error {
	return r.complete(id, leaseID, deliveryjob.StateFailed, lastErr, now)
}

// complete moves a leaseID-held job to a terminal state; an already-in-that-state
// completion is idempotent, a reclaimed lease or a different terminal state is a
// conflict.
func (r refDeliveryJobs) complete(id, leaseID, state, lastErr string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.deliveryJobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.State == state {
		return nil // idempotent at-least-once report
	}
	if job.Terminal() || job.LeaseID != leaseID {
		return sdk.ErrConflict // a different terminal state or a reclaimed lease
	}
	job.State = state
	job.LastError = lastErr
	job.TerminalAt = now.UTC()
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now.UTC()
	r.deliveryJobs[id] = job
	return nil
}

func (r refDeliveryJobs) Retry(_ context.Context, id, leaseID string, availableAt time.Time, lastErr string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.deliveryJobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.Terminal() || job.LeaseID != leaseID {
		return sdk.ErrConflict
	}
	job.AvailableAt = availableAt.UTC()
	job.LastError = lastErr
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now.UTC()
	r.deliveryJobs[id] = job
	return nil
}

func (r refDeliveryJobs) Cancel(_ context.Context, id string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.deliveryJobs[id]
	if !ok {
		return sdk.ErrNotFound
	}
	if job.State == deliveryjob.StateCanceled {
		return nil // idempotent
	}
	if job.Terminal() {
		return sdk.ErrConflict // cannot cancel a succeeded/failed job
	}
	job.State = deliveryjob.StateCanceled
	job.TerminalAt = now.UTC()
	job.LeaseID = ""
	job.LeasedUntil = time.Time{}
	job.UpdatedAt = now.UTC()
	r.deliveryJobs[id] = job
	return nil
}

func (r refDeliveryJobs) GetLatestByIdempotencyKey(_ context.Context, idempotencyKey string) (deliveryjob.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest deliveryjob.Job
	found := false
	for _, ex := range r.deliveryJobs {
		if ex.IdempotencyKey != idempotencyKey {
			continue
		}
		if !found ||
			ex.CreatedAt.After(latest.CreatedAt) ||
			(ex.CreatedAt.Equal(latest.CreatedAt) && ex.ID > latest.ID) {
			latest = ex
			found = true
		}
	}
	if !found {
		return deliveryjob.Job{}, sdk.ErrNotFound
	}
	return latest, nil
}

func (r refDeliveryJobs) PurgeTerminal(_ context.Context, before time.Time, limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var purgeable []string
	for id, ex := range r.deliveryJobs {
		if ex.Terminal() && !ex.TerminalAt.After(before) { // terminal_at <= before
			purgeable = append(purgeable, id)
		}
	}
	sort.Strings(purgeable) // deterministic bounded batch
	n := 0
	for _, id := range purgeable {
		if limit > 0 && n >= limit {
			break
		}
		delete(r.deliveryJobs, id)
		n++
	}
	return n, nil
}
