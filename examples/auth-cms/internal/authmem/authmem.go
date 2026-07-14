// Package authmem is an in-memory implementation of the auth feature's full
// repository set: the v1 ports (user.UserRepository, user.PasswordRepository,
// session.SessionRepository) plus the auth-v2 ports the A9 proof protocol
// exercises — oauthaccount.OAuthAccountRepository, oauthstate.StateRepository,
// serviceaccount.ServiceAccountRepository, apikey.APIKeyRepository,
// securityevent.SecurityEventRepository, and invitation.InvitationRepository.
// It is the auth-side sibling of this host's cms memstore: a "bring your own
// store" proof that features/authentication runs with no datastore driver in its module
// graph — data lives in maps and is lost on exit.
//
// It mirrors the honesty the port doc comments promise (and the storetest
// conformance suite proves), not just their shape: UserRepository.Create on a
// colliding normalized email returns sdk.ErrAlreadyExists; session reads report
// expiry with sdk.ErrExpired; the OAuth-account (Provider,
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
	"sync"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// ids assigns entity keys when a Create arrives with an empty ID (the
// cryptids.Database strategy, amended D10) — mimicking a schema default.
var ids = cryptids.IDGenerator{}

// data holds every auth entity in maps behind one mutex. The v2 collections
// (oauthAccounts, securityEvents) are slices where the port has no single-key
// identity; the rest are keyed maps.
type data struct {
	mu          sync.RWMutex
	users       map[string]user.User
	identifiers map[string]identifier.Identifier // real identifier rows (design §2.2), by identifier ID
	passwords   map[string]string
	sessions    map[string]session.Session

	oauthAccounts   []oauthaccount.OAuthAccount
	oauthStates     map[string]oauthstate.State
	serviceAccounts map[string]serviceaccount.ServiceAccount
	apiKeys         map[string]apikey.APIKey
	securityEvents  []securityevent.SecurityEvent
	invitations     map[string]invitation.Invitation

	// v3 atomic-rail collections (AV3-1.4). challenges/authGrants/deliveryJobs are
	// keyed by ID; contactChanges is keyed by (user, kind) so Create is an atomic
	// replace-per-pair. The credential-mutation rail projects and mutates the real
	// identifier rows above (like a pgx/turso store over user_identifiers), so the
	// masked inventory and credential policy never diverge from the identifier rail.
	// auth_revision rides the user row.
	challenges     map[string]challenge.Challenge
	authGrants     map[string]authgrant.Grant
	contactChanges map[string]contactchange.PendingChange
}

// Store is an in-memory auth datastore. Its Repositories method yields the port
// set features/authentication needs.
type Store struct{ d *data }

// New returns an empty Store.
func New() *Store {
	return &Store{d: &data{
		users:           map[string]user.User{},
		identifiers:     map[string]identifier.Identifier{},
		passwords:       map[string]string{},
		sessions:        map[string]session.Session{},
		oauthStates:     map[string]oauthstate.State{},
		serviceAccounts: map[string]serviceaccount.ServiceAccount{},
		apiKeys:         map[string]apikey.APIKey{},
		invitations:     map[string]invitation.Invitation{},

		challenges:     map[string]challenge.Challenge{},
		authGrants:     map[string]authgrant.Grant{},
		contactChanges: map[string]contactchange.PendingChange{},
	}}
}

// Repositories bundles the per-port views as the feature's repository set. Every
// port is wired: the A9 proof host needs the v2 ports (OAuth, machine identity,
// security events, invitations) live, not nil, and AV3-1.4 wires the v3 atomic
// rails (challenges, contact changes, step-up grants, credential mutations) so the
// exported storetest suite proves them here too.
func (s *Store) Repositories() auth.Repositories {
	return auth.Repositories{
		Users:           userRepo{s.d},
		Identifiers:     identifierRepo{s.d},
		Passwords:       passwordRepo{s.d},
		Sessions:        sessionRepo{s.d},
		OAuthAccounts:   oauthAccountRepo{s.d},
		OAuthStates:     oauthStateRepo{s.d},
		ServiceAccounts: serviceAccountRepo{s.d},
		APIKeys:         apiKeyRepo{s.d},
		SecurityEvents:  securityEventRepo{s.d},
		Invitations:     invitationRepo{s.d},

		Challenges:           challengeRepo{s.d},
		PasswordResets:       passwordResetRepo{s.d},
		ContactChanges:       contactChangeRepo{s.d},
		AuthenticationGrants: authGrantRepo{s.d},
		CredentialMutations:  credentialMutationRepo{s.d},
	}
}

// --- user.UserRepository ---

type userRepo struct{ *data }

func (r userRepo) Get(_ context.Context, id string) (user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
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

// CreateWithPrimaryIdentifier commits the user and its first identifier under one
// mutex (design §2.2): when the identifier claims login/recovery, the
// (kind, value) authentication claim must be free — else neither row is written
// (sdk.ErrAlreadyExists). Empty IDs are assigned inline (the greenfield
// DB-generated convention) and the identifier is linked to the new user.
func (r userRepo) CreateWithPrimaryIdentifier(_ context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if identifierClaimsAuth(ident) {
		if _, taken := r.findActiveAuthClaim(ident.Kind, ident.NormalizedValue, nil); taken {
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
	r.identifiers[ident.ID] = ident
	return u, ident, nil
}

// --- identifier.IdentifierRepository ---

// identifierRepo keys real identifier rows by ID and hand-enforces the §2.1
// partial-index semantics: active-only login/recovery lookup, one active primary
// per (user, kind), the partial unique authentication claim, and revision-CAS
// ApplyVerifiedChange under the shared mutex so the user + identifier +
// auth_revision mutation is atomic. auth_revision rides the user row.
type identifierRepo struct{ *data }

// identifierClaimsAuth reports whether i occupies a login/recovery claim.
func identifierClaimsAuth(i identifier.Identifier) bool {
	return i.LoginEnabled || i.RecoveryEnabled
}

// findActiveAuthClaim returns the active login/recovery identifier claiming
// (kind, value), skipping any ID in exclude; callers hold d.mu.
func (d *data) findActiveAuthClaim(kind identifier.Kind, value string, exclude map[string]bool) (identifier.Identifier, bool) {
	for id, ex := range d.identifiers {
		if exclude[id] {
			continue
		}
		if ex.Active() && identifierClaimsAuth(ex) && ex.Kind == kind && ex.NormalizedValue == value {
			return ex, true
		}
	}
	return identifier.Identifier{}, false
}

func (r identifierRepo) Get(_ context.Context, id string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.identifiers[id]
	if !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	return it, nil
}

func (r identifierRepo) GetLogin(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, it := range r.identifiers {
		if it.Active() && it.LoginEnabled && string(it.Kind) == kind && it.NormalizedValue == normalizedValue {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r identifierRepo) GetRecovery(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, it := range r.identifiers {
		if it.Active() && it.RecoveryEnabled && string(it.Kind) == kind && it.NormalizedValue == normalizedValue {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r identifierRepo) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]identifier.Identifier, 0)
	for _, it := range r.identifiers {
		if it.Active() && it.UserID == userID {
			out = append(out, it)
		}
	}
	return out, nil
}

// ApplyVerifiedChange runs the confirm mutation inside one mutex-held critical
// section: revision CAS on the user row, authentication-claim arbitration,
// retirement of the replaced identifier and any displaced same-kind primary,
// insertion of the new active row, and a single revision increment (design §2.2).
func (r identifierRepo) ApplyVerifiedChange(_ context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[input.UserID]
	if !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	if u.AuthRevision != expectedAuthRevision {
		return identifier.Identifier{}, sdk.ErrConflict
	}

	retire := map[string]bool{}
	if input.ReplacesIdentifierID != "" {
		retire[input.ReplacesIdentifierID] = true
	}
	if input.MakePrimary {
		for id, it := range r.identifiers {
			if it.Active() && it.UserID == input.UserID && it.Kind == input.Kind && it.IsPrimary {
				retire[id] = true
			}
		}
	}
	if input.LoginEnabled || input.RecoveryEnabled {
		if _, taken := r.findActiveAuthClaim(input.Kind, input.NormalizedValue, retire); taken {
			return identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}

	now := verifiedAt.UTC()
	for id := range retire {
		it := r.identifiers[id]
		it.Retire(now)
		r.identifiers[id] = it
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
	r.identifiers[newIdent.ID] = newIdent
	u.AuthRevision = expectedAuthRevision + 1
	r.users[u.ID] = u
	return newIdent, nil
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

// sessionRepo keys sessions by their app-minted ID and hand-enforces the
// refresh-rotation invariants a SQL store gets from its indexes and CAS UPDATEs
// (the storetest reference semantics): a unique live refresh hash, the single
// previous (grace) slot with a consumed flag, compare-and-swap Rotate/ConsumeGrace,
// and the empty-previous guard.
type sessionRepo struct{ *data }

func (r sessionRepo) Create(_ context.Context, s session.Session) (session.Session, error) {
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

func (r sessionRepo) Get(_ context.Context, id string) (session.Session, error) {
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
func (r sessionRepo) GetByRefreshHash(_ context.Context, hash string) (session.Session, session.RefreshMatch, error) {
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
func (r sessionRepo) Rotate(_ context.Context, id, expectedCurrentHash, newHash string) error {
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
func (r sessionRepo) ConsumeGrace(_ context.Context, id, previousHash string) error {
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

func (r sessionRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[id]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.sessions, id)
	return nil
}

func (r sessionRepo) DeleteByUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, id)
		}
	}
	return nil
}
