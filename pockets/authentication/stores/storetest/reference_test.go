package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	protection "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// TestReferencePasskeys runs the passkey suite, separate from Run, against the
// reference implementation.
func TestReferencePasskeys(t *testing.T) {
	RunPasskeys(t, func(t *testing.T) auth.Repositories {
		return newReference().repositories()
	})
}

// TestReferenceTOTP runs the TOTP suite, separate from Run, against the
// reference implementation, and the schema-probe contract over a reference
// store marked unmigrated.
func TestReferenceTOTP(t *testing.T) {
	RunTOTP(t, func(t *testing.T) auth.Repositories {
		return newReference().repositories()
	})
	RunTOTPSchemaProbe(t, func(t *testing.T) totp.Repository {
		r := newReference()
		r.totpSchemaMissing = true
		return refTOTP{r}
	})
}

// TestReference runs the conformance suite against the in-package reference
// implementation. This is what lets pockets/authentication self-verify under guard G2
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
	oauthClients    map[string]oauth2.Client
	oauthCodes      map[string]oauth2.Code
	oauthRefresh    map[string]oauthRefreshRecord
	mu              sync.RWMutex
	users           map[string]user.User
	passwords       map[string]string
	sessions        map[string]session.Session
	oauthAccounts   []oauthaccount.OAuthAccount // keyed by (Provider, ProviderUserID) invariant
	oauthStates     map[string]oauthstate.State
	serviceAccounts map[string]serviceaccount.ServiceAccount // by ID
	apiKeys         map[string]apikey.APIKey                 // by ID
	securityEvents  []securityevent.SecurityEvent            // append-only
	invitations     map[string]invitations.Invitation        // by ID
	challenges      map[string]challenge.Challenge           // by ID
	grants          map[string]authgrant.Grant               // by ID
	authRevisions   map[string]int64                         // per-user optimistic revision (design §5.6)
	identifiers     map[string][]credential.IdentifierMethod // phase-1 credential-projection stand-in (design §5.6), by user ID
	userIdentifiers map[string]identifier.Identifier         // real identifier rows (design §2.2), by identifier ID
	contactChanges  map[string]contactchange.PendingChange   // pending identifier changes (design §2.4), by (user, kind)

	// externalSubjects is the external identity mirror, by (provider, subject).
	externalSubjects map[externalKey]externalsubject.ExternalSubject

	// erasedAt is the erasure marker (users.erased_at), by user ID. The stored
	// status stays deactivated; every read projects StatusErased from it.
	erasedAt map[string]time.Time

	// passkeys holds registered passkeys, by ID.
	passkeys map[string]passkey.Passkey

	// totpCreds and totpCodes are the second-factor aggregate, by user ID;
	// totpSchemaMissing makes ProbeSchema report migration 0026 absent.
	totpCreds         map[string]totp.Credential
	totpCodes         map[string][]totp.RecoveryCode
	totpSchemaMissing bool

	// resetFailAt/resetFailErr inject an all-or-nothing rollback failure into
	// refPasswordResets.Redeem (design §5.9): resetFailAt=N fails just after the
	// Nth composition statement, so a test proves that a mid-transaction failure at
	// EACH statement boundary leaves NO partial state. Zero = no injection.
	resetFailAt  int
	resetFailErr error
}

func newReference() *reference {
	return &reference{
		oauthClients:    map[string]oauth2.Client{},
		oauthCodes:      map[string]oauth2.Code{},
		oauthRefresh:    map[string]oauthRefreshRecord{},
		users:           map[string]user.User{},
		passwords:       map[string]string{},
		sessions:        map[string]session.Session{},
		oauthStates:     map[string]oauthstate.State{},
		serviceAccounts: map[string]serviceaccount.ServiceAccount{},
		apiKeys:         map[string]apikey.APIKey{},
		invitations:     map[string]invitations.Invitation{},
		challenges:      map[string]challenge.Challenge{},
		grants:          map[string]authgrant.Grant{},
		authRevisions:   map[string]int64{},
		identifiers:     map[string][]credential.IdentifierMethod{},
		userIdentifiers: map[string]identifier.Identifier{},
		contactChanges:  map[string]contactchange.PendingChange{},

		externalSubjects: map[externalKey]externalsubject.ExternalSubject{},
		erasedAt:         map[string]time.Time{},
		passkeys:         map[string]passkey.Passkey{},
		totpCreds:        map[string]totp.Credential{},
		totpCodes:        map[string][]totp.RecoveryCode{},
	}
}

func (r *reference) repositories() auth.Repositories {
	return auth.Repositories{
		OAuth2:               refOAuth2{r},
		SessionManagement:    refOAuth2{r},
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
		UserAdmin:            refUserAdmin{r},
		Passwordless:         refPasswordless{r},
		ActiveSessions:       refActiveSessions{r},
		ExternalSubjects:     refExternalSubjects{r},
		Maintenance:          refMaintenance{r},
		UserErasure:          refErasure{r},
		Passkeys:             refPasskeys{r},
		TOTP:                 refTOTP{r},
	}
}

// --- user.AdminRepository ---

// refUserAdmin is the reference user-administration capability (CHAU-1.1). Its
// whole point is the SetStatus transaction: the status write, the auth_revision
// increment, and the session/grant revocation all happen inside ONE critical
// section, so no observer can see a deactivated user still holding sessions and
// no concurrent mint can slip between them. A dialect adapter must give the same
// guarantee with a real transaction.
type refUserAdmin struct{ *reference }

// summaryLocked projects a user plus its ACTIVE PRIMARY email identifier.
// Callers hold r.mu. This mirrors the single JOIN a SQL store performs — the
// projection must never issue one identifier read per user.
func (r *reference) summaryLocked(u user.User) user.Summary {
	u = r.projectLocked(u)
	s := user.Summary{
		ID:              u.ID,
		DisplayName:     u.DisplayName,
		Status:          user.NormalizeStatus(u.Status),
		StatusChangedAt: u.StatusChangedAt,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}
	for _, it := range r.userIdentifiers {
		if it.UserID != u.ID || it.Kind != identifier.KindEmail || !it.IsPrimary || !it.Active() {
			continue
		}
		s.PrimaryEmail = it.NormalizedValue
		s.EmailVerified = it.Verified()
		break // the partial unique index guarantees at most one
	}
	return s
}

func (r refUserAdmin) List(_ context.Context, req list.Request) (list.Page[user.Summary], error) {
	r.mu.RLock()
	all := make([]user.Summary, 0, len(r.users))
	for _, u := range r.users {
		all = append(all, r.summaryLocked(u))
	}
	r.mu.RUnlock()
	return pageMem(all, req, func(s user.Summary) (time.Time, string) {
		return s.CreatedAt, s.ID
	})
}

func (r refUserAdmin) GetSummary(_ context.Context, id string) (user.Summary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return user.Summary{}, sdk.ErrNotFound
	}
	return r.summaryLocked(u), nil
}

func (r refUserAdmin) SetStatus(_ context.Context, id string, status user.Status, now time.Time) (user.StatusChange, error) {
	if !status.Valid() {
		return user.StatusChange{}, fmt.Errorf("reference: %q: %w", status, user.ErrInvalidStatus)
	}
	if status == user.StatusErased {
		return user.StatusChange{}, user.ErrErased
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.users[id]
	if !ok {
		return user.StatusChange{}, sdk.ErrNotFound
	}
	if _, erased := r.erasedAt[id]; erased {
		// Decided in the same locked read as the transition: an erased user
		// replays a deactivation and never reactivates.
		if status == user.StatusActive {
			return user.StatusChange{}, user.ErrErased
		}
		return user.StatusChange{Status: user.StatusErased, Changed: false, ChangedAt: u.StatusChangedAt}, nil
	}
	current := user.NormalizeStatus(u.Status)
	if current == status {
		// Idempotent replay: no revision increment, no revocation, no change.
		return user.StatusChange{Status: current, Changed: false, ChangedAt: u.StatusChangedAt}, nil
	}

	now = now.UTC()
	u.Status = status
	u.StatusChangedAt = now
	u.UpdatedAt = now
	u.AuthRevision++
	r.users[id] = u
	r.authRevisions[id] = u.AuthRevision

	revoked := 0
	for sid, s := range r.sessions {
		if s.UserID != id {
			continue
		}
		delete(r.sessions, sid)
		revoked++
		for gid, g := range r.grants {
			if g.SessionID == sid {
				delete(r.grants, gid)
			}
		}
	}
	// Grants are session-bound, but sweep any that reference the user directly so
	// a grant whose session was already gone cannot survive the transition.
	for gid, g := range r.grants {
		if g.UserID == id {
			delete(r.grants, gid)
		}
	}

	return user.StatusChange{Status: status, Changed: true, ChangedAt: now, RevokedSessions: revoked}, nil
}

// --- session.ActiveUserRepository ---

// refActiveSessions is the reference fenced session mint (CHAU-1.1): the status
// read and the session insert happen under ONE lock, so a concurrent SetStatus
// either commits first (and this refuses) or commits after (and revokes the row
// this wrote). A SQL adapter must reach the same outcome with row locking or
// conditional insertion inside its transaction.
type refActiveSessions struct{ *reference }

func (r refActiveSessions) CreateForActiveUser(_ context.Context, s session.Session, expectedAuthRevision int64) (session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.users[s.UserID]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return session.Session{}, session.ErrUserNotActive
	}
	if u.AuthRevision != expectedAuthRevision {
		return session.Session{}, sdk.ErrConflict
	}
	// Same uniqueness contract as the ordinary Create.
	for _, ex := range r.sessions {
		if ex.RefreshTokenHash == s.RefreshTokenHash {
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	r.sessions[s.ID] = s
	return s, nil
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
	return r.projectLocked(u), nil
}

// projectLocked reports an erased user as StatusErased, the projection a SQL
// store derives from erased_at IS NOT NULL. Callers hold r.mu.
func (r *reference) projectLocked(u user.User) user.User {
	if _, erased := r.erasedAt[u.ID]; erased {
		u.Status = user.StatusErased
	}
	return u
}

func (r refUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	current.DisplayName = u.DisplayName
	current.UpdatedAt = u.UpdatedAt
	r.users[id] = current
	return current, nil
}

// CreateWithPrimaryIdentifier commits the user and its first identifier under one
// mutex (design §2.2): when the identifier claims login/recovery, that
// (kind, value) claim must be free — else neither row is written
// (sdk.ErrAlreadyExists). Empty IDs are assigned inline (the greenfield
// DB-generated convention), and the identifier is linked to the new user.
func (r refUsers) CreateWithPrimaryIdentifier(ctx context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	return r.Provision(ctx, u, ident, user.InitialCredentials{})
}

func (r refUsers) Provision(ctx context.Context, u user.User, ident identifier.Identifier, credentials user.InitialCredentials) (user.User, identifier.Identifier, error) {
	if err := ctx.Err(); err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
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
	if _, exists := r.users[u.ID]; exists {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	if credentials.OAuth != nil {
		for _, existing := range r.oauthAccounts {
			if existing.Provider == credentials.OAuth.Provider && existing.ProviderUserID == credentials.OAuth.ProviderUserID {
				return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
			}
		}
	}
	if credentials.External != nil {
		if _, taken := r.externalSubjects[externalKey{credentials.External.Provider, credentials.External.Subject}]; taken {
			return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}
	ident.UserID = u.ID
	r.users[u.ID] = u
	r.authRevisions[u.ID] = u.AuthRevision
	r.userIdentifiers[ident.ID] = ident
	if credentials.PasswordHash != "" {
		r.passwords[u.ID] = credentials.PasswordHash
	}
	if credentials.OAuth != nil {
		a := *credentials.OAuth
		a.UserID = u.ID
		r.oauthAccounts = append(r.oauthAccounts, a)
	}
	if credentials.External != nil {
		s := *credentials.External
		s.UserID = u.ID
		r.externalSubjects[externalKey{s.Provider, s.Subject}] = s
	}
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
	r.advanceCredentialRevisionLocked(input.UserID, now)
	return newIdent, nil
}

// --- user.PasswordRepository ---

type refPasswords struct{ *reference }

func (r refPasswords) Set(ctx context.Context, userID, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.changePasswordLocked(userID, user.PasswordChange{NewHash: hash, Now: time.Now().UTC()}, false)
	return err
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
	u, ok := r.users[a.UserID]
	if !ok {
		return oauthaccount.OAuthAccount{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return oauthaccount.OAuthAccount{}, session.ErrUserNotActive
	}
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
	u, ok := r.users[userID]
	if !ok {
		return sdk.ErrNotFound
	}
	if !u.Active() {
		return session.ErrUserNotActive
	}

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
	r.advanceCredentialRevisionLocked(userID, time.Now())
	r.revokeCredentialStateLocked(userID)
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
func (r refContactChanges) Get(_ context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := contactChangeKey(userID, kind)
	p, ok := r.contactChanges[key]
	if !ok {
		return contactchange.PendingChange{}, sdk.ErrNotFound
	}
	return p, nil
}

func (r refContactChanges) Consume(_ context.Context, userID string, kind identifier.Kind, expectedID string) (contactchange.PendingChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := contactChangeKey(userID, kind)
	p, ok := r.contactChanges[key]
	if !ok || p.ID != expectedID {
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
func (r refServiceAccounts) List(_ context.Context, req list.Request) (list.Page[serviceaccount.ServiceAccount], error) {
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

// DeleteWithKeys is the cascade capability: the account and every key it holds
// go under one lock, so no concurrent mint can leave an orphan key.
func (r refServiceAccounts) DeleteWithKeys(_ context.Context, id string) (serviceaccount.Deletion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return serviceaccount.Deletion{}, sdk.ErrNotFound
	}
	return r.deleteAccountLocked(id), nil
}

// deleteAccountLocked removes the account and its keys. Callers hold r.mu.
func (r *reference) deleteAccountLocked(id string) serviceaccount.Deletion {
	var d serviceaccount.Deletion
	for kid, k := range r.apiKeys {
		if k.ServiceAccountID != id {
			continue
		}
		d.KeyCount++
		if !k.Revoked() {
			d.UnrevokedKeyCount++
		}
		delete(r.apiKeys, kid)
	}
	delete(r.serviceAccounts, id)
	return d
}

// Delete cascades to the account's keys, as the bundled stores' Delete does.
func (r refServiceAccounts) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.serviceAccounts[id]; !ok {
		return sdk.ErrNotFound
	}
	r.deleteAccountLocked(id)
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

func (r refAPIKeys) ListByServiceAccount(_ context.Context, serviceAccountID string, req list.Request) (list.Page[apikey.APIKey], error) {
	// The search term is applied through list.MatchesSearch, the SHARED oracle both
	// SQL dialects are pinned against (crud-search-upstream T4), so a memory store
	// cannot quietly disagree with Postgres about what a term matches. A non-blank
	// term against a list declaring no searchable fields is sdk.ErrInvalidInput —
	// the same fail-loud rule the SQL connectors apply — but api_keys DOES declare
	// one (apikey.SearchFields), so only the name is matched.
	r.mu.RLock()
	all := make([]apikey.APIKey, 0, len(r.apiKeys))
	for _, k := range r.apiKeys {
		if k.ServiceAccountID != serviceAccountID {
			continue
		}
		if !list.MatchesSearch(k.Name, req.Search) {
			continue
		}
		all = append(all, k)
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
func (r refSecurityEvents) List(_ context.Context, filter securityevent.ListFilter, req list.Request) (list.Page[securityevent.SecurityEvent], error) {
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

// --- invitations.InvitationRepository ---

type refInvitations struct{ *reference }

// Create enforces the PARTIAL pending-tuple uniqueness (design §6, migration
// 0013): a second PENDING invitation for the same (resource_type, resource_id,
// identifier_kind, identifier, relation) → ErrAlreadyExists; once a prior one
// moves off pending, a new one succeeds. Non-pending rows never block a Create.
// identifier_kind is part of the key, so the same value across two kinds coexists.
func (r refInvitations) Create(ctx context.Context, inv invitations.Invitation) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitations.Invitation{}, err
	}
	if inv.Status != invitations.StatusPending || inv.ResolvedSubjectType != "" {
		return invitations.Invitation{}, sdk.ErrInvalidInput
	}
	for _, ex := range r.invitations {
		if (inv.ID != "" && ex.ID == inv.ID) || ex.TokenHash == inv.TokenHash {
			return invitations.Invitation{}, sdk.ErrAlreadyExists
		}
		if ex.Active() &&
			ex.ResourceType == inv.ResourceType &&
			ex.ResourceID == inv.ResourceID &&
			ex.IdentifierKind == inv.IdentifierKind &&
			ex.Identifier == inv.Identifier &&
			ex.Relation == inv.Relation {
			return invitations.Invitation{}, sdk.ErrAlreadyExists
		}
	}
	// Empty ID → mimic a schema default (amended D10): assign the key at insert.
	if inv.ID == "" {
		inv.ID = ids.MustGenerate()
	}
	// Mirror the SQL stores' nil/empty → '{}' round-trip and defensive copy: a
	// stored row always carries a non-nil metadata map the caller cannot mutate.
	inv.Metadata = invitations.CloneMetadata(inv.Metadata)
	r.invitations[inv.ID] = inv.Clone()
	return inv.Clone(), nil
}

func (r refInvitations) Get(_ context.Context, id string) (invitations.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inv, ok := r.invitations[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	return inv.Clone(), nil
}

// GetByTokenHash returns the invitation for tokenHash: unknown → ErrNotFound, a
// present row past ExpiresAt → ErrExpired (the read-time expiry), else the
// record. Expiry is checked against time.Now, matching a SQL store filtering on
// the read clock.
func (r refInvitations) GetByTokenHash(_ context.Context, tokenHash string) (invitations.Invitation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inv := range r.invitations {
		if inv.TokenHash == tokenHash {
			if inv.Status != invitations.StatusAccepting && inv.Status != invitations.StatusAccepted && inv.Expired(time.Now()) {
				return invitations.Invitation{}, sdk.ErrExpired
			}
			return inv.Clone(), nil
		}
	}
	return invitations.Invitation{}, sdk.ErrNotFound
}

func (r refInvitations) ListByResource(_ context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitations.Invitation], error) {
	r.mu.RLock()
	all := make([]invitations.Invitation, 0, len(r.invitations))
	for _, inv := range r.invitations {
		if inv.ResourceType == resourceType && inv.ResourceID == resourceID {
			all = append(all, inv.Clone())
		}
	}
	r.mu.RUnlock()
	return pageMem(all, req, func(inv invitations.Invitation) (time.Time, string) {
		return inv.CreatedAt, inv.ID
	})
}

func (r refInvitations) ListBySubject(_ context.Context, kind, identifier string, req list.Request) (list.Page[invitations.Invitation], error) {
	r.mu.RLock()
	all := make([]invitations.Invitation, 0, len(r.invitations))
	for _, inv := range r.invitations {
		if inv.IdentifierKind == kind && inv.Identifier == identifier {
			all = append(all, inv.Clone())
		}
	}
	r.mu.RUnlock()
	return pageMem(all, req, func(inv invitations.Invitation) (time.Time, string) {
		return inv.CreatedAt, inv.ID
	})
}

func (r refInvitations) UpdateStatus(ctx context.Context, id string, upd invitations.StatusUpdate) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitations.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.UpdateStatus(upd)
	if err != nil {
		return invitations.Invitation{}, err
	}
	for existingID, existing := range r.invitations {
		if existingID == id {
			continue
		}
		if existing.TokenHash == updated.TokenHash {
			return invitations.Invitation{}, sdk.ErrAlreadyExists
		}
		if existing.Active() && updated.Active() && existing.ResourceType == updated.ResourceType && existing.ResourceID == updated.ResourceID && existing.IdentifierKind == updated.IdentifierKind && existing.Identifier == updated.Identifier && existing.Relation == updated.Relation {
			return invitations.Invitation{}, sdk.ErrAlreadyExists
		}
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

func (r refInvitations) ClaimAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitations.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.ClaimAcceptance(claim)
	if err != nil {
		return invitations.Invitation{}, err
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

func (r refInvitations) CompleteAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitations.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.CompleteAcceptance(claim)
	if err != nil {
		return invitations.Invitation{}, err
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

func (r refInvitations) CancelAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return invitations.Invitation{}, err
	}
	if err := claim.Validate(); err != nil {
		return invitations.Invitation{}, err
	}
	inv, ok := r.invitations[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	updated, err := inv.CancelAcceptance(claim)
	if err != nil {
		return invitations.Invitation{}, err
	}
	r.invitations[id] = updated.Clone()
	return updated.Clone(), nil
}

// ListActiveByResource filters on the STORED status (pending, accepting), never
// the wall clock, so an expired but still pending row is listed.
func (r refInvitations) ListActiveByResource(_ context.Context, in invitations.ActiveListInput, req list.Request) (list.Page[invitations.Invitation], error) {
	if err := in.Validate(); err != nil {
		return list.Page[invitations.Invitation]{}, err
	}
	if strings.TrimSpace(req.Search) != "" {
		return list.Page[invitations.Invitation]{}, fmt.Errorf("active invitation listing has no searchable fields: %w", sdk.ErrInvalidInput)
	}
	r.mu.RLock()
	all := make([]invitations.Invitation, 0, len(r.invitations))
	for _, inv := range r.invitations {
		if inv.ResourceType != in.ResourceType || inv.ResourceID != in.ResourceID {
			continue
		}
		if inv.Status != invitations.StatusPending && inv.Status != invitations.StatusAccepting {
			continue
		}
		if in.Relation != "" && inv.Relation != in.Relation {
			continue
		}
		all = append(all, inv.Clone())
	}
	r.mu.RUnlock()
	return pageMem(all, req, func(inv invitations.Invitation) (time.Time, string) {
		return inv.CreatedAt, inv.ID
	})
}

// pageMem sorts the full population by (created_at, id) in the resolved
// direction, then applies the sdk/pkg/list list matrix — cursor or offset mode, the
// reverse-probe prev page, and the optional count — the reference semantics the
// SQL stores and the host memstores must reproduce. keyOf returns each record's
// (created_at, id); created_at is the only sortable field.
func pageMem[T any](all []T, req list.Request, keyOf func(T) (time.Time, string)) (list.Page[T], error) {
	if err := req.Validate(); err != nil {
		return list.Page[T]{}, err
	}
	if req.Order.Field != "" && req.Order.Field != "created_at" {
		return list.Page[T]{}, fmt.Errorf("unknown order field %q: %w", req.Order.Field, sdk.ErrInvalidInput)
	}
	asc := req.Order.Direction == list.ASC

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
	limit := req.NormalizedLimit(list.Limits{})
	encode := func(item T) (string, error) {
		t, itemID := keyOf(item)
		return list.EncodeCursor("created_at", t, itemID)
	}

	if req.ResolvedStrategy() == list.StrategyOffset {
		window := all
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		page, err := list.TrimPage(window, limit, encode)
		if err != nil {
			return list.Page[T]{}, err
		}
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
		if req.WithCount {
			page.Total = &total
		}
		return page, nil
	}

	cur, err := list.DecodeCursor(req.Cursor, "created_at")
	if err != nil {
		return list.Page[T]{}, err
	}

	var curTime time.Time
	forward := all
	if cur != nil {
		var ok bool
		curTime, ok = cur.OrderValue.(time.Time)
		if !ok {
			return list.Page[T]{}, fmt.Errorf("cursor order value must be a timestamp: %w", sdk.ErrInvalidInput)
		}
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
	page, err := list.TrimPage(window, limit, encode)
	if err != nil {
		return list.Page[T]{}, err
	}

	if cur != nil {
		var before []T
		for _, item := range all {
			t, itemID := keyOf(item)
			if !afterCursorMem(t, itemID, curTime, cur.PK, asc) {
				before = append(before, item)
			}
		}
		// Include the boundary and one extra predecessor for the previous cursor.
		if len(before) > limit+1 {
			before = before[len(before)-limit-1:]
		}
		if err := list.MarkPrevPage(&page, before, limit, encode); err != nil {
			return list.Page[T]{}, err
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

// --- challenge.Repository ---

// refChallenges keys challenges by ID and hand-enforces the atomic-secret
// invariants a SQL store gets from its indexes and transactional consume: one
// active row per (user, purpose), a unique (purpose, secret_digest) claim, and a
// consume that decides expiry, digest comparison, attempt counting, lockout, and
// deletion inside ONE mutex-held critical section — the "exactly one winner"
// contract. Digest comparison routes through auth.ConstantTimeDigestEqual, whose
// empty-hash guard makes an empty candidate never match.
type refChallenges struct{ *reference }

func (r refChallenges) Replace(ctx context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return challenge.Challenge{}, err
	}
	// Delete the prior (SUBJECT KEY, purpose) row (the single-active claim). The
	// subject key defaults to the user id for every purpose that predates it
	// (CHAU-6.1), so this is unchanged for all of them.
	subjectKey := c.ResolvedSubjectKey()
	c.SubjectKey = subjectKey
	for id, ex := range r.challenges {
		if ex.ResolvedSubjectKey() == subjectKey && ex.Purpose == c.Purpose {
			continue
		}
		if (ex.Purpose == c.Purpose && ex.SecretDigest == c.SecretDigest) || (c.ID != "" && id == c.ID) {
			return challenge.Challenge{}, sdk.ErrAlreadyExists
		}
	}
	for id, ex := range r.challenges {
		if ex.ResolvedSubjectKey() == subjectKey && ex.Purpose == c.Purpose {
			delete(r.challenges, id)
		}
	}
	if c.ID == "" {
		c.ID = ids.MustGenerate()
	}
	if c.Version == 0 {
		c.Version = 1
	}
	c.Context = append([]byte(nil), c.Context...)
	r.challenges[c.ID] = c
	c.Context = append([]byte(nil), c.Context...)
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
		if cand.KeyID == row.ProtectorKeyID && protection.ConstantTimeDigestEqual(cand.Digest, row.SecretDigest) {
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

// findByUserPurpose returns the single (subject key, purpose) row; callers hold
// r.mu. Every CODE purpose keys on the user id, so the passed userID IS the
// subject key.
func (r refChallenges) findByUserPurpose(userID, purpose string) (string, challenge.Challenge, bool) {
	for id, ex := range r.challenges {
		if ex.ResolvedSubjectKey() == userID && ex.Purpose == purpose {
			return id, ex, true
		}
	}
	return "", challenge.Challenge{}, false
}

func consumedOf(c challenge.Challenge, now time.Time) challenge.Consumed {
	return challenge.Consumed{
		ID:             c.ID,
		SubjectKey:     c.ResolvedSubjectKey(),
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

	binding, err := passwordreset.ParseBinding(r.challenges[chID].Context)
	if err != nil {
		return passwordreset.RedeemResult{}, err
	}
	owner, ok := r.users[userID]
	if !ok || !owner.Active() || !binding.Matches(userID, owner.AuthRevision, r.userIdentifiers[binding.IdentifierID]) {
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
	r.advanceCredentialRevisionLocked(userID, in.Now)
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
		ensurePasswordOwner(t, r.repositories(), userID)
		proof := prepareResetProof(t, r.repositories(), userID)
		now := time.Now()
		r.passwords[userID] = oldHash
		r.sessions["s1"] = newSession(userID, "rh-1", time.Hour, now)
		r.sessions["s2"] = newSession(userID, "rh-2", time.Hour, now)
		r.grants["g1"] = newGrant("s1", userID, challenge.PurposeRemovePassword, "ctx", 5*time.Minute, now)
		r.challenges["c-reset"] = newChallenge(userID, challenge.PurposePasswordReset, "", resetDig, proof, 0, time.Hour, now)
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
	ensurePasswordOwner(t, r.repositories(), userID)
	proof := prepareResetProof(t, r.repositories(), userID)
	now := time.Now()
	r.passwords[userID] = oldHash
	r.sessions["s1"] = newSession(userID, "rh-1", time.Hour, now)
	r.grants["g1"] = newGrant("s1", userID, challenge.PurposeRemovePassword, "ctx", 5*time.Minute, now)
	r.challenges["c-reset"] = newChallenge(userID, challenge.PurposePasswordReset, "", resetDig, proof, 0, time.Hour, now)
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

func (r refAuthGrants) Create(ctx context.Context, g authgrant.Grant, expectedAuthRevision int64, now time.Time) (authgrant.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return authgrant.Grant{}, err
	}
	u, ok := r.users[g.UserID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	sess, ok := r.sessions[g.SessionID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	if !u.Active() || sess.UserID != g.UserID || !now.Before(sess.ExpiresAt) {
		return authgrant.Grant{}, sdk.ErrUnauthorized
	}
	if u.AuthRevision != expectedAuthRevision {
		return authgrant.Grant{}, sdk.ErrConflict
	}
	if g.ID == "" {
		g.ID = ids.MustGenerate()
	}
	if _, ok := r.grants[g.ID]; ok {
		return authgrant.Grant{}, sdk.ErrAlreadyExists
	}
	g.Methods = append([]session.AuthenticationMethod(nil), g.Methods...)
	r.grants[g.ID] = g
	g.Methods = append([]session.AuthenticationMethod(nil), g.Methods...)
	return g, nil
}

func (r refAuthGrants) Consume(ctx context.Context, req authgrant.Requirement, now time.Time) (authgrant.Grant, error) {
	if err := req.Validate(); err != nil {
		return authgrant.Grant{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return authgrant.Grant{}, err
	}
	u, ok := r.users[req.UserID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	sess, ok := r.sessions[req.SessionID]
	if !ok {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	if !u.Active() || sess.UserID != req.UserID || !now.Before(sess.ExpiresAt) {
		return authgrant.Grant{}, sdk.ErrUnauthorized
	}
	var selected authgrant.Grant
	for _, g := range r.grants {
		if g.Consumed() || g.SessionID != req.SessionID || g.UserID != req.UserID || g.Purpose != req.Purpose || g.ContextDigest != req.ContextDigest {
			continue
		}
		if !g.Expired(now) && !req.Accepts(g.AuthenticatedAt, g.Assurance, now) {
			continue
		}
		if selected.ID == "" || g.CreatedAt.Before(selected.CreatedAt) || (g.CreatedAt.Equal(selected.CreatedAt) && g.ID < selected.ID) {
			selected = g
		}
	}
	if selected.ID == "" {
		return authgrant.Grant{}, sdk.ErrNotFound
	}
	selected.ConsumedAt = now.UTC()
	r.grants[selected.ID] = selected
	if selected.Expired(now) {
		return authgrant.Grant{}, sdk.ErrExpired
	}
	selected.Methods = append([]session.AuthenticationMethod(nil), selected.Methods...)
	return selected, nil
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

// --- passwordless.Repository ---

// refPasswordless is the reference atomic magic-link redemption (CHAU-6.2). It is
// the behavioral SPEC the two SQL implementations are held to, so it is written to
// be readable rather than clever: one critical section, a snapshot taken before
// any mutation, and an explicit restore on every rejection path so a refused
// redemption leaves the store byte-identical — including the token, which stays
// redeemable after an INFRASTRUCTURE failure but is consumed by a committed
// outcome.
type refPasswordless struct{ *reference }

// passwordlessSnapshot copies every collection the redemption can mutate.
type passwordlessSnapshot struct {
	users         map[string]user.User
	identifiers   map[string]identifier.Identifier
	passwords     map[string]string
	sessions      map[string]session.Session
	grants        map[string]authgrant.Grant
	challenges    map[string]challenge.Challenge
	authRevisions map[string]int64
}

func (r refPasswordless) snapshot() passwordlessSnapshot {
	snap := passwordlessSnapshot{
		users:         make(map[string]user.User, len(r.users)),
		identifiers:   make(map[string]identifier.Identifier, len(r.userIdentifiers)),
		passwords:     make(map[string]string, len(r.passwords)),
		sessions:      make(map[string]session.Session, len(r.sessions)),
		grants:        make(map[string]authgrant.Grant, len(r.grants)),
		challenges:    make(map[string]challenge.Challenge, len(r.challenges)),
		authRevisions: make(map[string]int64, len(r.authRevisions)),
	}
	maps.Copy(snap.users, r.users)
	maps.Copy(snap.identifiers, r.userIdentifiers)
	maps.Copy(snap.passwords, r.passwords)
	maps.Copy(snap.sessions, r.sessions)
	maps.Copy(snap.grants, r.grants)
	maps.Copy(snap.challenges, r.challenges)
	maps.Copy(snap.authRevisions, r.authRevisions)
	return snap
}

func (r refPasswordless) restore(snap passwordlessSnapshot) {
	r.users = snap.users
	r.userIdentifiers = snap.identifiers
	r.passwords = snap.passwords
	r.sessions = snap.sessions
	r.grants = snap.grants
	r.challenges = snap.challenges
	r.authRevisions = snap.authRevisions
}

func (r refPasswordless) Redeem(_ context.Context, in passwordless.RedeemInput) (passwordless.RedeemResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := r.snapshot()
	reject := func() (passwordless.RedeemResult, error) {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	if in.TokenDigest == "" {
		return reject()
	}

	// 1. Select and consume the live matching challenge. An expired row is deleted
	//    (single-use even when stale) and still rejects.
	var consumed challenge.Challenge
	found := false
	for id, ex := range r.challenges {
		if ex.Purpose == in.Purpose && ex.SecretDigest == in.TokenDigest {
			consumed, found = ex, true
			delete(r.challenges, id)
			break
		}
	}
	if !found || consumed.Expired(in.Now) {
		if !found {
			r.restore(snap) // nothing was consumed; keep the store untouched
		}
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	// 2. Decode and validate the versioned binding.
	var binding passwordless.Binding
	if err := json.Unmarshal(consumed.Context, &binding); err != nil {
		return reject()
	}
	if binding.Version != passwordless.BindingVersion || binding.NormalizedValue == "" {
		return reject()
	}

	now := in.Now.UTC()

	// 3. Re-read the CURRENT active claim for the bound address. The binding's
	//    identifier/user ids are an expectation, not an authority.
	current, hasCurrent := r.findActiveAuthClaimLocked(identifier.Kind(binding.Kind), binding.NormalizedValue, nil)

	if hasCurrent && !binding.MatchesOwner(current.UserID, current.ID, r.users[current.UserID].AuthRevision) {
		return reject()
	}
	switch {
	case !hasCurrent:
		// 3a. Nobody claims the address.
		if !binding.ProvisionIfAbsent || binding.HasOwner() {
			// A login-only link whose account vanished between issue and consume.
			return reject()
		}
		return r.provisionLocked(in, binding, now, snap)

	case current.Verified():
		// 3b. A verified claim: ordinary login for its CURRENT owner, which may be a
		//     different user than the one recorded at issue (the address was
		//     registered between send and consume — the now-current owner wins).
		if !current.LoginEnabled {
			return reject()
		}
		return r.loginLocked(in, current, now, snap)

	default:
		// 3c. An UNVERIFIED claim: the click proves possession, so adopt it — after
		//     revoking everything that predates the proof.
		return r.adoptLocked(in, current, now, snap)
	}
}

// provisionLocked creates one active user and one verified primary identifier,
// then inserts the session. Callers hold r.mu.
func (r refPasswordless) provisionLocked(in passwordless.RedeemInput, binding passwordless.Binding, now time.Time, snap passwordlessSnapshot) (passwordless.RedeemResult, error) {
	u := in.NewUser
	if u.ID == "" {
		u.ID = ids.MustGenerate()
	}
	u.Status = user.NormalizeStatus(u.Status)
	if !u.Active() {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}
	u.CreatedAt, u.UpdatedAt = now, now

	ident := in.NewIdentifier
	if ident.ID == "" {
		ident.ID = ids.MustGenerate()
	}
	ident.UserID = u.ID
	ident.NormalizedValue = binding.NormalizedValue
	ident.Kind = identifier.Kind(binding.Kind)
	ident.VerifiedAt = now
	ident.CreatedAt, ident.UpdatedAt = now, now

	// The authentication claim must still be free at commit time.
	if _, taken := r.findActiveAuthClaimLocked(ident.Kind, ident.NormalizedValue, nil); taken {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	r.users[u.ID] = u
	r.authRevisions[u.ID] = u.AuthRevision
	r.userIdentifiers[ident.ID] = ident

	sess, err := r.insertSessionLocked(in.Session, u.ID, snap)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:     passwordless.OutcomeProvisionNew,
		User:        u,
		Identifier:  ident,
		Session:     sess,
		Provisioned: true,
	}, nil
}

// loginLocked mints a session for the identifier's current owner.
func (r refPasswordless) loginLocked(in passwordless.RedeemInput, ident identifier.Identifier, _ time.Time, snap passwordlessSnapshot) (passwordless.RedeemResult, error) {
	u, ok := r.users[ident.UserID]
	if !ok || !u.Active() {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}
	sess, err := r.insertSessionLocked(in.Session, u.ID, snap)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:    passwordless.OutcomeLoginExistingVerified,
		User:       u,
		Identifier: ident,
		Session:    sess,
	}, nil
}

// adoptLocked verifies the previously-unverified identifier and revokes every
// credential that predates the proof, BEFORE inserting the session. The ordering
// is the invariant: a completed adoption cannot leave a squatter credential alive,
// because the session is created only after the revocation.
func (r refPasswordless) adoptLocked(in passwordless.RedeemInput, ident identifier.Identifier, now time.Time, snap passwordlessSnapshot) (passwordless.RedeemResult, error) {
	u, ok := r.users[ident.UserID]
	if !ok || !u.Active() {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	// Revoke first: password, sessions, grants, and the listed challenge purposes.
	delete(r.passwords, u.ID)
	for sid, sv := range r.sessions {
		if sv.UserID == u.ID {
			delete(r.sessions, sid)
		}
	}
	for gid, g := range r.grants {
		if g.UserID == u.ID {
			delete(r.grants, gid)
		}
	}
	for cid, c := range r.challenges {
		if c.UserID != u.ID {
			continue
		}
		if slices.Contains(in.RevokeChallengePurposes, c.Purpose) {
			delete(r.challenges, cid)
		}
	}

	// Then verify and adopt the identifier, and bump the revision so any in-flight
	// credential mutation loses its CAS.
	ident.VerifiedAt = now
	ident.LoginEnabled = in.AdoptedIdentifierUses.Login
	ident.RecoveryEnabled = in.AdoptedIdentifierUses.Recovery
	ident.NotificationEnabled = in.AdoptedIdentifierUses.Notification
	ident.UpdatedAt = now
	r.userIdentifiers[ident.ID] = ident

	u.AuthRevision++
	u.UpdatedAt = now
	r.users[u.ID] = u
	r.authRevisions[u.ID] = u.AuthRevision

	sess, err := r.insertSessionLocked(in.Session, u.ID, snap)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:    passwordless.OutcomeVerifyAndAdoptExistingUnverified,
		User:       u,
		Identifier: ident,
		Session:    sess,
	}, nil
}

// insertSessionLocked applies the ordinary session uniqueness contract inside the
// transaction. A collision rolls the whole redemption back.
func (r refPasswordless) insertSessionLocked(proposed session.Session, userID string, snap passwordlessSnapshot) (session.Session, error) {
	proposed.UserID = userID
	if proposed.ID == "" {
		proposed.ID = ids.MustGenerate()
	}
	for _, ex := range r.sessions {
		if ex.RefreshTokenHash == proposed.RefreshTokenHash {
			r.restore(snap)
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	r.sessions[proposed.ID] = proposed
	return proposed, nil
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
	for _, it := range r.userIdentifiers {
		if it.UserID == userID && it.Active() {
			set.Identifiers = append(set.Identifiers, credential.IdentifierMethod{ID: it.ID, Kind: string(it.Kind), Uses: credential.IdentifierUses{Login: it.LoginEnabled, Recovery: it.RecoveryEnabled, Notification: it.NotificationEnabled}, Verified: it.Verified(), Primary: it.IsPrimary})
		}
	}
	for _, p := range r.passkeysOfLocked(userID) {
		set.Passkeys = append(set.Passkeys, credential.PasskeyMethod{ID: p.ID, UserVerified: p.UserVerified})
	}
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
		target := r.mutationIdentifierLocked(userID, m.IdentifierID)
		replacement := r.mutationIdentifierLocked(userID, m.ReplacementPrimaryID)
		if err := m.Validate(userID, target, replacement); err != nil {
			return err
		}
		if _, ok := r.userIdentifiers[target.ID]; ok {
			target.Retire(time.Now())
			r.userIdentifiers[target.ID] = target
		}
		if _, ok := r.userIdentifiers[replacement.ID]; ok && m.ReplacementPrimaryID != "" {
			replacement.MakePrimary(time.Now())
			r.userIdentifiers[replacement.ID] = replacement
		}
		r.identifiers[userID] = retireRefIdentifier(r.identifiers[userID], m.IdentifierID, m.ReplacementPrimaryID)
	case credential.ChangeIdentifierUses:
		target := r.mutationIdentifierLocked(userID, m.IdentifierID)
		if err := m.Validate(userID, target); err != nil {
			return err
		}
		if _, ok := r.userIdentifiers[target.ID]; ok {
			if m.MakePrimary {
				for id, it := range r.userIdentifiers {
					if it.UserID == userID && it.Kind == target.Kind && it.Active() {
						it.IsPrimary = false
						r.userIdentifiers[id] = it
					}
				}
				target.MakePrimary(time.Now())
			}
			_ = target.SetUses(identifier.Uses{Login: m.Uses.Login, Recovery: m.Uses.Recovery, Notification: m.Uses.Notification}, time.Now())
			r.userIdentifiers[target.ID] = target
		}
		r.identifiers[userID] = changeRefIdentifierUses(r.identifiers[userID], m.IdentifierID, m.Uses, m.MakePrimary)
	case credential.RemovePasskey:
		p, ok := r.passkeys[m.PasskeyID]
		if !ok || p.UserID != userID {
			return sdk.ErrNotFound
		}
		delete(r.passkeys, m.PasskeyID)
	case credential.RemovePasskeys:
		for _, p := range r.passkeysOfLocked(userID) {
			delete(r.passkeys, p.ID)
		}
	default:
		return fmt.Errorf("reference: unsupported credential mutation %T: %w", mutation, sdk.ErrInvalidInput)
	}
	if _, ok := r.users[userID]; ok {
		r.advanceCredentialRevisionLocked(userID, time.Now())
	} else {
		r.authRevisions[userID] = expectedAuthRevision + 1
	}
	r.revokeCredentialStateLocked(userID)
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
		{ID: "id-a", Kind: sdk.AddressKindEmail, Verified: true, Primary: true, Uses: credential.IdentifierUses{Login: true, Recovery: true}},
		{ID: "id-b", Kind: sdk.AddressKindEmail, Verified: true, Uses: credential.IdentifierUses{Login: true, Recovery: true}},
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

func (r refCredentialMutations) mutationIdentifierLocked(userID, id string) identifier.Identifier {
	if it, ok := r.userIdentifiers[id]; ok {
		return it
	}
	// The original policy-race fixture seeds projections without persistence rows.
	for _, m := range r.identifiers[userID] {
		if m.ID == id {
			it := identifier.Identifier{ID: id, UserID: userID, Kind: identifier.Kind(m.Kind), IsPrimary: m.Primary}
			if m.Verified {
				it.VerifiedAt = suiteBase
			}
			return it
		}
	}
	return identifier.Identifier{}
}

// --- externalsubject.Repository ---

// externalKey is the mirror's (provider, subject) primary key.
type externalKey struct{ provider, subject string }

// refExternalSubjects hand-enforces the (provider, subject) primary key and the
// active-user precondition of Link under the shared reference mutex.
type refExternalSubjects struct{ *reference }

var _ externalsubject.Repository = refExternalSubjects{}

func (r refExternalSubjects) Get(ctx context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	if err := ctx.Err(); err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.externalSubjects[externalKey{provider, subject}]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	return s, nil
}

func (r refExternalSubjects) Link(ctx context.Context, s externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	if err := ctx.Err(); err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[s.UserID]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return externalsubject.ExternalSubject{}, session.ErrUserNotActive
	}
	key := externalKey{s.Provider, s.Subject}
	if _, taken := r.externalSubjects[key]; taken {
		return externalsubject.ExternalSubject{}, sdk.ErrAlreadyExists
	}
	s.FirstSeenAt = s.FirstSeenAt.UTC()
	r.externalSubjects[key] = s
	return s, nil
}

func (r refExternalSubjects) ListByUser(ctx context.Context, userID string) ([]externalsubject.ExternalSubject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []externalsubject.ExternalSubject{}
	for _, s := range r.externalSubjects {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b externalsubject.ExternalSubject) int {
		if c := a.FirstSeenAt.Compare(b.FirstSeenAt); c != 0 {
			return c
		}
		if c := strings.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		return strings.Compare(a.Subject, b.Subject)
	})
	return out, nil
}

func (r refExternalSubjects) Unlink(ctx context.Context, provider, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := externalKey{provider, subject}
	if _, ok := r.externalSubjects[key]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.externalSubjects, key)
	return nil
}

// --- maintenance.Repository ---

// refMaintenance is the reference retention port. Each call is one critical
// section; the rail purge appends its security_events_purged row in the same
// one, as the port contract requires. The floor is measured on time.Now, the
// store's own clock.
type refMaintenance struct{ *reference }

var _ maintenance.Repository = refMaintenance{}

// refFloor is the reference's database floor: the Go minimum less the day of
// clock-skew margin.
const refFloor = maintenance.MinSecurityEventRetention - 24*time.Hour

func refLimit(limit int) error {
	if limit <= 0 {
		return fmt.Errorf("reference: limit %d: %w", limit, sdk.ErrInvalidInput)
	}
	return nil
}

// purgeLocked deletes up to limit keys of m whose expiry is at or before
// before, oldest first.
func purgeLocked[V any](m map[string]V, expires func(V) time.Time, before time.Time, limit int) int {
	type cand struct {
		key string
		at  time.Time
	}
	var cands []cand
	for k, v := range m {
		if at := expires(v); !at.After(before) {
			cands = append(cands, cand{k, at})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if !cands[i].at.Equal(cands[j].at) {
			return cands[i].at.Before(cands[j].at)
		}
		return cands[i].key < cands[j].key
	})
	n := min(limit, len(cands))
	for _, c := range cands[:n] {
		delete(m, c.key)
	}
	return n
}

func (r refMaintenance) PurgeExpiredSessions(_ context.Context, before time.Time, limit int) (int, error) {
	if err := refLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeLocked(r.sessions, func(s session.Session) time.Time { return s.ExpiresAt }, before, limit), nil
}

func (r refMaintenance) PurgeExpiredOAuthStates(_ context.Context, before time.Time, limit int) (int, error) {
	if err := refLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeLocked(r.oauthStates, func(s oauthstate.State) time.Time { return s.ExpiresAt }, before, limit), nil
}

func (r refMaintenance) PurgeExpiredGrants(_ context.Context, before time.Time, limit int) (int, error) {
	if err := refLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeLocked(r.grants, func(g authgrant.Grant) time.Time { return g.ExpiresAt }, before, limit), nil
}

func (r refMaintenance) PurgeExpiredContactChanges(_ context.Context, before time.Time, limit int) (int, error) {
	if err := refLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeLocked(r.contactChanges, func(p contactchange.PendingChange) time.Time { return p.ExpiresAt }, before, limit), nil
}

// invitationPurgeAt is when an invitation becomes purgeable: a terminal row at
// its last update, a pending row at its expiry; accepting never (the zero
// sentinel far in the future).
func invitationPurgeAt(inv invitations.Invitation) time.Time {
	switch inv.Status {
	case invitations.StatusAccepted, invitations.StatusDeclined, invitations.StatusCancelled:
		return inv.UpdatedAt
	case invitations.StatusPending:
		return inv.ExpiresAt
	}
	return time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
}

func (r refMaintenance) PurgeInvitations(_ context.Context, before time.Time, limit int) (int, error) {
	if err := refLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeLocked(r.invitations, invitationPurgeAt, before, limit), nil
}

func (r refMaintenance) CountStaleAcceptingInvitations(_ context.Context, before time.Time) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, inv := range r.invitations {
		if inv.Status == invitations.StatusAccepting && !inv.UpdatedAt.After(before) {
			n++
		}
	}
	return n, nil
}

func (r refMaintenance) PruneOAuth2(_ context.Context, now time.Time, limit int) (int, error) {
	if err := refLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := purgeLocked(r.oauthCodes, func(c oauth2.Code) time.Time { return c.ExpiresAt }, now, limit)
	n += purgeLocked(r.oauthClients, func(c oauth2.Client) time.Time { return c.ExpiresAt }, now, limit)
	n += purgeLocked(r.oauthRefresh, func(h oauthRefreshRecord) time.Time { return h.ExpiresAt }, now, limit)
	return n, nil
}

// railLocked returns the rail in ascending (created_at, id) order. Callers
// hold r.mu.
func (r *reference) railLocked() []securityevent.SecurityEvent {
	rows := slices.Clone(r.securityEvents)
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func (r refMaintenance) ExportSecurityEvents(_ context.Context, before time.Time, after maintenance.ExportCursor, limit int) ([]maintenance.ArchivedEvent, maintenance.ExportCursor, error) {
	if err := refLimit(limit); err != nil {
		return nil, after, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []maintenance.ArchivedEvent
	for _, evt := range r.railLocked() {
		if evt.CreatedAt.After(before) {
			break
		}
		if !after.CreatedAt.IsZero() && (evt.CreatedAt.Before(after.CreatedAt) || (evt.CreatedAt.Equal(after.CreatedAt) && evt.ID <= after.ID)) {
			continue
		}
		if len(out) >= limit && !evt.CreatedAt.Equal(out[len(out)-1].CreatedAt) {
			break
		}
		evt.Details = maps.Clone(evt.Details)
		out = append(out, maintenance.ArchivedEvent{SecurityEvent: evt})
	}
	if len(out) == 0 {
		return nil, after, nil
	}
	last := out[len(out)-1]
	return out, maintenance.ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (r refMaintenance) PurgeSecurityEventsBefore(_ context.Context, before time.Time, limit int) (maintenance.PurgeResult, error) {
	if err := refLimit(limit); err != nil {
		return maintenance.PurgeResult{}, err
	}
	now := time.Now().UTC()
	if before.After(now.Add(-refFloor)) {
		return maintenance.PurgeResult{}, fmt.Errorf("reference: cutoff %s: %w", before.Format(time.RFC3339Nano), maintenance.ErrBelowRetentionFloor)
	}
	limit = min(limit, maintenance.MaxBatchSize)
	r.mu.Lock()
	defer r.mu.Unlock()
	doomed := map[string]bool{}
	for _, evt := range r.railLocked() {
		if len(doomed) == limit || evt.CreatedAt.After(before) {
			break
		}
		doomed[evt.ID] = true
	}
	kept := r.securityEvents[:0:0]
	for _, evt := range r.securityEvents {
		if !doomed[evt.ID] {
			kept = append(kept, evt)
		}
	}
	r.securityEvents = append(kept, securityevent.SecurityEvent{
		ID:          ids.MustGenerate(),
		Actor:       securityevent.Principal{Type: "system", ID: "maintenance"},
		EventType:   securityevent.TypeSecurityEventsPurged,
		EventStatus: securityevent.StatusSuccess,
		Details: map[string]any{
			"cutoff":     before.UTC().Format("2006-01-02T15:04:05.000000Z"),
			"count":      len(doomed),
			"floor_days": int(refFloor / (24 * time.Hour)),
			"max_rows":   limit,
		},
		CreatedAt: now,
	})
	return maintenance.PurgeResult{Cutoff: before, Count: len(doomed)}, nil
}

// --- user.EraseRepository ---

// refErasure is the reference erasure: the whole Erase is one critical
// section, the stand-in for the store's one transaction, in the port's order.
type refErasure struct{ *reference }

var _ user.EraseRepository = refErasure{}

type identifierPair struct{ kind, value string }

func (r refErasure) Erase(ctx context.Context, in user.EraseInput) (user.Erasure, error) {
	if strings.TrimSpace(in.ID) == "" {
		return user.Erasure{}, fmt.Errorf("reference: erase: empty id: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return user.Erasure{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := in.ID
	u, ok := r.users[id]
	if !ok {
		return user.Erasure{}, sdk.ErrNotFound
	}
	if at, erased := r.erasedAt[id]; erased {
		return user.Erasure{Changed: false, ErasedAt: at}, nil
	}

	// Capture the user's pairs, active and retired, minus any pair another
	// user holds actively now.
	pairs := map[identifierPair]bool{}
	for _, it := range r.userIdentifiers {
		if it.UserID == id && it.NormalizedValue != "" {
			pairs[identifierPair{string(it.Kind), it.NormalizedValue}] = true
		}
	}
	for _, it := range r.userIdentifiers {
		if it.UserID != id && it.Active() {
			delete(pairs, identifierPair{string(it.Kind), it.NormalizedValue})
		}
	}
	var addressed []string
	for invID, inv := range r.invitations {
		matched := pairs[identifierPair{inv.IdentifierKind, inv.Identifier}] && (inv.ResolvedSubjectID == "" || inv.ResolvedSubjectID == id)
		resolved := inv.ResolvedSubjectType == "user" && inv.ResolvedSubjectID == id
		if !matched && !resolved {
			continue
		}
		if inv.Status == invitations.StatusAccepting {
			return user.Erasure{}, user.ErrEraseBlockedByClaim
		}
		addressed = append(addressed, invID)
	}

	now := in.Now.UTC()
	var c user.EraseCounts
	if user.NormalizeStatus(u.Status) == user.StatusActive {
		u.Status = user.StatusDeactivated
		u.StatusChangedAt = now
	}
	u.DisplayName = ""
	u.UpdatedAt = now
	u.AuthRevision++
	r.users[id] = u
	r.authRevisions[id] = u.AuthRevision
	r.erasedAt[id] = now

	for iid, it := range r.userIdentifiers {
		if it.UserID != id {
			continue
		}
		if it.ReplacedAt.IsZero() {
			it.ReplacedAt = now
		}
		it.NormalizedValue = ""
		it.VerifiedAt = time.Time{}
		it.LoginEnabled, it.RecoveryEnabled, it.NotificationEnabled, it.IsPrimary = false, false, false, false
		it.UpdatedAt = now
		r.userIdentifiers[iid] = it
		c.IdentifiersBlanked++
	}
	delete(r.identifiers, id)
	if _, ok := r.passwords[id]; ok {
		delete(r.passwords, id)
		c.Passwords = 1
	}
	for _, p := range r.passkeysOfLocked(id) {
		delete(r.passkeys, p.ID)
		c.Passkeys++
	}
	if in.TOTP {
		if _, ok := r.totpCreds[id]; ok {
			delete(r.totpCreds, id)
			c.TOTP = 1
		}
		c.RecoveryCodes = len(r.totpCodes[id])
		delete(r.totpCodes, id)
	}
	kept := r.oauthAccounts[:0:0]
	for _, a := range r.oauthAccounts {
		if a.UserID == id {
			c.OAuthAccounts++
			continue
		}
		kept = append(kept, a)
	}
	r.oauthAccounts = kept
	for key, s := range r.externalSubjects {
		if s.UserID == id {
			delete(r.externalSubjects, key)
			c.ExternalSubjects++
		}
	}
	for key, p := range r.contactChanges {
		if p.UserID == id {
			delete(r.contactChanges, key)
			c.ContactChanges++
		}
	}
	for hash, code := range r.oauthCodes {
		if code.UserID == id {
			delete(r.oauthCodes, hash)
			c.Codes++
		}
	}
	for cid, ch := range r.challenges {
		if ch.UserID == id || slices.Contains(in.SubjectKeys, ch.SubjectKey) {
			delete(r.challenges, cid)
			c.Challenges++
		}
	}
	sessions := map[string]bool{}
	for sid, s := range r.sessions {
		if s.UserID == id {
			sessions[sid] = true
			delete(r.sessions, sid)
			c.Sessions++
		}
	}
	for gid, g := range r.grants {
		if g.UserID == id || sessions[g.SessionID] {
			delete(r.grants, gid)
			c.Grants++
		}
	}
	for _, invID := range addressed {
		inv := r.invitations[invID]
		if inv.Status == invitations.StatusPending {
			delete(r.invitations, invID)
			c.InvitationsDeleted++
			continue
		}
		inv.Identifier = ""
		inv.Metadata = map[string]string{}
		inv.UpdatedAt = now
		r.invitations[invID] = inv
		c.InvitationsBlanked++
	}
	for invID, inv := range r.invitations {
		if inv.InvitedBy == id && inv.Status == invitations.StatusPending {
			inv.Status = invitations.StatusCancelled
			inv.UpdatedAt = now
			r.invitations[invID] = inv
			c.InvitationsCancelled++
		}
	}
	for said, sa := range r.serviceAccounts {
		if sa.OwnerUserID == id && sa.ActAsUser {
			c.APIKeys += r.deleteAccountLocked(said).KeyCount
			c.ServiceAccounts++
		}
	}
	if in.Event.EventType != "" {
		evt := in.Event
		evt.Details = c.Merge(evt.Details)
		if evt.ID == "" {
			evt.ID = ids.MustGenerate()
		}
		r.securityEvents = append(r.securityEvents, evt)
	}
	return user.Erasure{Changed: true, ErasedAt: now, Counts: c}, nil
}

// --- passkey.Repository ---

// refPasskeys is the reference passkey store. Create runs its guard under the
// one mutex that also serializes the credential-mutation rail, the reference's
// user-row lock.
type refPasskeys struct{ *reference }

var _ passkey.Repository = refPasskeys{}

// passkeysOfLocked returns userID's passkeys oldest first; callers hold r.mu.
func (r *reference) passkeysOfLocked(userID string) []passkey.Passkey {
	var out []passkey.Passkey
	for _, p := range r.passkeys {
		if p.UserID == userID {
			out = append(out, clonePasskey(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func clonePasskey(p passkey.Passkey) passkey.Passkey {
	p.CredentialID = slices.Clone(p.CredentialID)
	p.PublicKey = slices.Clone(p.PublicKey)
	p.AAGUID = slices.Clone(p.AAGUID)
	p.UserHandle = slices.Clone(p.UserHandle)
	p.Transports = slices.Clone(p.Transports)
	if p.LastUsedAt != nil {
		at := *p.LastUsedAt
		p.LastUsedAt = &at
	}
	return p
}

func (r refPasskeys) Create(_ context.Context, in passkey.CreateInput) (passkey.Passkey, error) {
	p := in.Passkey
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[p.UserID]
	if !ok {
		return passkey.Passkey{}, sdk.ErrNotFound
	}
	if !r.projectLocked(u).Active() {
		return passkey.Passkey{}, session.ErrUserNotActive
	}
	if u.AuthRevision != in.ExpectedAuthRevision {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	owned := r.passkeysOfLocked(p.UserID)
	if in.Bootstrap && slices.ContainsFunc(owned, passkey.Passkey.AAL2Capable) {
		return passkey.Passkey{}, passkey.ErrBootstrapClosed
	}
	for _, existing := range r.passkeys {
		if string(existing.CredentialID) == string(p.CredentialID) {
			return passkey.Passkey{}, sdk.ErrAlreadyExists
		}
	}
	if len(owned) >= in.MaxPerUser {
		return passkey.Passkey{}, passkey.ErrPasskeyLimit
	}
	if len(owned) > 0 && string(owned[0].UserHandle) != string(p.UserHandle) {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	if p.ID == "" {
		p.ID = ids.MustGenerate()
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.LastUsedAt = nil
	r.passkeys[p.ID] = clonePasskey(p)
	return clonePasskey(p), nil
}

func (r refPasskeys) GetByCredentialID(_ context.Context, credentialID []byte) (passkey.Passkey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.passkeys {
		if string(p.CredentialID) == string(credentialID) {
			return clonePasskey(p), nil
		}
	}
	return passkey.Passkey{}, sdk.ErrNotFound
}

func (r refPasskeys) ListByUser(_ context.Context, userID string) ([]passkey.Passkey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := r.passkeysOfLocked(userID)
	if out == nil {
		out = []passkey.Passkey{}
	}
	return out, nil
}

func (r refPasskeys) Rename(_ context.Context, id, userID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.passkeys[id]
	if !ok || p.UserID != userID {
		return sdk.ErrNotFound
	}
	p.Name = name
	r.passkeys[id] = p
	return nil
}

func (r refPasskeys) RecordUse(_ context.Context, id string, expected, count uint32, backupState bool, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.passkeys[id]
	if !ok || p.SignCount != expected {
		return sdk.ErrConflict
	}
	at = at.UTC()
	p.SignCount, p.BackupState, p.LastUsedAt = count, backupState, &at
	r.passkeys[id] = p
	return nil
}

// --- totp.Repository ---

// refTOTP is the reference second-factor aggregate. Confirm and Remove run
// under the one mutex that also serializes the credential-mutation rail, the
// reference's user-row lock.
type refTOTP struct{ *reference }

var (
	_ totp.Repository   = refTOTP{}
	_ totp.SchemaProber = refTOTP{}
)

func cloneTOTPCredential(c totp.Credential) totp.Credential {
	for _, p := range []**time.Time{&c.ExpiresAt, &c.ConfirmedAt, &c.LockedUntil, &c.LastUsedAt} {
		if *p != nil {
			at := **p
			*p = &at
		}
	}
	return c
}

func (r refTOTP) ProbeSchema(context.Context) error {
	if r.totpSchemaMissing {
		return fmt.Errorf("reference: totp_credentials and recovery_codes are missing; apply migration 0026_totp: %w", sdk.ErrNotFound)
	}
	return nil
}

func (r refTOTP) Begin(_ context.Context, c totp.Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ex, ok := r.totpCreds[c.UserID]; ok && ex.Confirmed() {
		return totp.ErrAlreadyEnrolled
	}
	c.ConfirmedAt, c.LastUsedStep, c.FailedAttempts, c.LockedUntil, c.LastUsedAt = nil, 0, 0, nil, nil
	r.totpCreds[c.UserID] = cloneTOTPCredential(c)
	return nil
}

func (r refTOTP) Get(_ context.Context, userID string) (totp.Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.totpCreds[userID]
	if !ok {
		return totp.Credential{}, sdk.ErrNotFound
	}
	return cloneTOTPCredential(c), nil
}

func (r refTOTP) Confirm(_ context.Context, in totp.Confirmation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[in.UserID]
	if !ok {
		return sdk.ErrNotFound
	}
	if !r.projectLocked(u).Active() {
		return session.ErrUserNotActive
	}
	c, ok := r.totpCreds[in.UserID]
	switch {
	case !ok:
		return sdk.ErrNotFound
	case c.Confirmed():
		return totp.ErrAlreadyEnrolled
	case c.Secret != in.ExpectedSecret || c.SessionID != in.ExpectedSessionID:
		return sdk.ErrNotFound
	case u.AuthRevision != in.ExpectedAuthRevision:
		return sdk.ErrConflict
	case in.Bootstrap && slices.ContainsFunc(r.passkeysOfLocked(in.UserID), passkey.Passkey.AAL2Capable):
		return totp.ErrBootstrapClosed
	}
	seen := map[string]bool{}
	for _, code := range in.Codes {
		if seen[code.Digest] {
			return sdk.ErrAlreadyExists
		}
		seen[code.Digest] = true
	}
	at := in.ConfirmedAt
	c.ConfirmedAt, c.LastUsedStep = &at, in.Step
	c.SessionID, c.Tier, c.AuthRevision, c.ExpiresAt, c.FailedAttempts, c.LockedUntil = "", "", 0, nil, 0, nil
	r.totpCreds[in.UserID] = c
	r.totpCodes[in.UserID] = slices.Clone(in.Codes)
	r.advanceCredentialRevisionLocked(in.UserID, time.Now())
	return nil
}

func (r refTOTP) RecordUse(_ context.Context, userID string, expectedStep, step int64, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.totpCreds[userID]
	if !ok || !c.Confirmed() || c.LastUsedStep != expectedStep || step <= expectedStep || (c.LockedUntil != nil && c.LockedUntil.After(at)) {
		return sdk.ErrConflict
	}
	c.LastUsedStep, c.LastUsedAt, c.FailedAttempts, c.LockedUntil = step, &at, 0, nil
	r.totpCreds[userID] = c
	return nil
}

func (r refTOTP) RecordFailure(_ context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (totp.Failure, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.totpCreds[userID]
	if !ok {
		return totp.Failure{}, sdk.ErrNotFound
	}
	c.FailedAttempts++
	out := totp.Failure{Attempts: c.FailedAttempts}
	if c.FailedAttempts >= lockAt {
		until := now.Add(lockFor)
		c.FailedAttempts, c.LockedUntil = 0, &until
		out.LockedUntil = &until
	}
	r.totpCreds[userID] = c
	return out, nil
}

func (r refTOTP) generatedAtLocked(userID string) *time.Time {
	codes := r.totpCodes[userID]
	if len(codes) == 0 {
		return nil
	}
	at := codes[0].GeneratedAt
	return &at
}

func (r refTOTP) ReplaceRecoveryCodes(_ context.Context, userID string, codes []totp.RecoveryCode, expected *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.totpCreds[userID]; !ok || !c.Confirmed() {
		return sdk.ErrNotFound
	}
	if expected != nil {
		if current := r.generatedAtLocked(userID); current == nil || !current.Equal(*expected) {
			return sdk.ErrConflict
		}
	}
	r.totpCodes[userID] = slices.Clone(codes)
	return nil
}

func (r refTOTP) ConsumeRecoveryCode(_ context.Context, userID string, candidates []challenge.DigestCandidate, _ time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	codes := r.totpCodes[userID]
	for i, code := range codes {
		if slices.ContainsFunc(candidates, func(c challenge.DigestCandidate) bool { return c.KeyID == code.KeyID && c.Digest == code.Digest }) {
			r.totpCodes[userID] = slices.Delete(slices.Clone(codes), i, i+1)
			if c, ok := r.totpCreds[userID]; ok {
				c.FailedAttempts = 0
				r.totpCreds[userID] = c
			}
			return len(r.totpCodes[userID]), nil
		}
	}
	return 0, sdk.ErrNotFound
}

func (r refTOTP) CountRecoveryCodes(_ context.Context, userID string) (int, *time.Time, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.totpCodes[userID]), r.generatedAtLocked(userID), nil
}

func (r refTOTP) Remove(_ context.Context, userID string, expectedRevision int64) (totp.Removed, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return totp.Removed{}, sdk.ErrNotFound
	}
	if u.AuthRevision != expectedRevision {
		return totp.Removed{}, sdk.ErrConflict
	}
	c, ok := r.totpCreds[userID]
	if !ok {
		return totp.Removed{}, nil
	}
	if !c.Confirmed() {
		delete(r.totpCreds, userID)
		return totp.Removed{Pending: true}, nil
	}
	removed := totp.Removed{Credential: true, RecoveryCodes: len(r.totpCodes[userID])}
	delete(r.totpCreds, userID)
	delete(r.totpCodes, userID)
	r.advanceCredentialRevisionLocked(userID, time.Now())
	r.revokeCredentialStateLocked(userID)
	return removed, nil
}

func (r refTOTP) RecoveryCodeKeyIDs(context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for _, codes := range r.totpCodes {
		for _, code := range codes {
			if !slices.Contains(out, code.KeyID) {
				out = append(out, code.KeyID)
			}
		}
	}
	return out, nil
}
