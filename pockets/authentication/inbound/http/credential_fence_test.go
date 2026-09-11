package authenticationhttp

import (
	"context"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func wireCredentialFakes(users *memUsers, pw *memPasswords, sess *memSessions, accounts *memOAuthAccounts, grants *memAuthGrants, ch *memChallenges) {
	users.pw = pw
	users.sess = sess
	users.accounts = accounts
	users.grants = grants
	users.challenges = ch
	pw.users = users
	accounts.users = users
	if grants != nil {
		grants.users = users
		grants.sessions = sess
	}
}
func (f *memUsers) Provision(ctx context.Context, u user.User, ident identifier.Identifier, credentials user.InitialCredentials) (user.User, identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idents != nil && (ident.LoginEnabled || ident.RecoveryEnabled) && inboundAuthClaimTaken(f.idents, ident) {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	if _, ok := f.byID[u.ID]; ok {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	if credentials.OAuth != nil {
		f.accounts.mu.Lock()
		defer f.accounts.mu.Unlock()
		for _, a := range f.accounts.m {
			if a.Provider == credentials.OAuth.Provider && a.ProviderUserID == credentials.OAuth.ProviderUserID {
				return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
			}
		}
	}
	f.byID[u.ID] = u
	ident.UserID = u.ID
	if f.idents != nil {
		ident = f.idents.insert(ident)
	}
	if credentials.PasswordHash != "" {
		f.pw.mu.Lock()
		f.pw.m[u.ID] = credentials.PasswordHash
		f.pw.mu.Unlock()
	}
	if credentials.OAuth != nil {
		a := *credentials.OAuth
		a.UserID = u.ID
		f.accounts.m = append(f.accounts.m, a)
	}
	return u, ident, nil
}
func (f *memUsers) revokeCredentialStateLocked(userID string) {
	if f.sess != nil {
		_ = f.sess.DeleteByUser(context.Background(), userID)
	}
	if f.grants != nil {
		f.grants.mu.Lock()
		for id, g := range f.grants.m {
			if g.UserID == userID {
				delete(f.grants.m, id)
			}
		}
		f.grants.mu.Unlock()
	}
	if f.challenges != nil {
		f.challenges.purgeUserPurposes(userID, []string{challenge.PurposePasswordReset})
	}
}
func (f *memPasswords) Change(ctx context.Context, userID string, change user.PasswordChange) (int64, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	return f.changeLocked(userID, change, true)
}
func (f *memPasswords) changeLocked(userID string, change user.PasswordChange, conditional bool) (int64, error) {
	u, ok := f.users.byID[userID]
	if !ok {
		return 0, sdk.ErrNotFound
	}
	if !u.Active() {
		return 0, session.ErrUserNotActive
	}
	f.mu.Lock()
	if conditional && (u.AuthRevision != change.ExpectedAuthRevision || f.m[userID] != change.ExpectedHash) {
		f.mu.Unlock()
		return 0, sdk.ErrConflict
	}
	f.m[userID] = change.NewHash
	f.mu.Unlock()
	u.AuthRevision++
	u.UpdatedAt = change.Now
	f.users.byID[userID] = u
	f.users.revokeCredentialStateLocked(userID)
	return u.AuthRevision, nil
}

type memActiveSessions struct {
	users    *memUsers
	sessions *memSessions
}

func (f memActiveSessions) CreateForActiveUser(ctx context.Context, s session.Session, expectedAuthRevision int64) (session.Session, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[s.UserID]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return session.Session{}, session.ErrUserNotActive
	}
	if u.AuthRevision != expectedAuthRevision {
		return session.Session{}, sdk.ErrConflict
	}
	return f.sessions.Create(ctx, s)
}
func (f *memOAuthAccounts) Link(ctx context.Context, a oauthaccount.OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (oauthaccount.OAuthAccount, int64, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users.byID[a.UserID]
	if !ok {
		return oauthaccount.OAuthAccount{}, 0, sdk.ErrNotFound
	}
	if !u.Active() {
		return oauthaccount.OAuthAccount{}, 0, session.ErrUserNotActive
	}
	if u.AuthRevision != expectedAuthRevision {
		return oauthaccount.OAuthAccount{}, 0, sdk.ErrConflict
	}
	for _, ex := range f.m {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, 0, sdk.ErrAlreadyExists
		}
	}
	if adoptIdentifierID != "" {
		f.users.idents.mu.Lock()
		ident, ok := f.users.idents.byID[adoptIdentifierID]
		if !ok || ident.UserID != u.ID || !ident.Active() {
			f.users.idents.mu.Unlock()
			return oauthaccount.OAuthAccount{}, 0, sdk.ErrConflict
		}
		ident.VerifiedAt = now
		ident.UpdatedAt = now
		f.users.idents.byID[ident.ID] = ident
		f.users.idents.mu.Unlock()
		f.users.pw.mu.Lock()
		delete(f.users.pw.m, u.ID)
		f.users.pw.mu.Unlock()
		f.users.revokeCredentialStateLocked(u.ID)
	}
	f.m = append(f.m, a)
	u.AuthRevision++
	f.users.byID[u.ID] = u
	return a, u.AuthRevision, nil
}

func inboundAuthClaimTaken(idents *memIdentifiers, ident identifier.Identifier) bool {
	_, taken := idents.activeAuthClaim(ident)
	return taken
}

// newServiceWithFakes links the stores' shared user fence before constructing the
// real service. Transport fixtures use the same atomic admission as adapters.
func newServiceWithFakes(d authenticationFixture, configs ...authenticatorConfig) *testService {
	cfg := authenticatorConfig{Limiter: d.Limiter, Logger: d.Logger}
	if len(configs) > 0 {
		cfg.Cookie = configs[0].Cookie
		cfg.BrowserLoginPath = configs[0].BrowserLoginPath
	}
	// Registration now validates its challenge/delivery dependencies before it
	// persists a user. Fixtures that register over HTTP need those stores too.
	if d.Challenges == nil {
		d.Challenges = &memChallenges{byID: map[string]challenge.Challenge{}}
	}
	if d.Protector == nil {
		d.Protector = memProtector{}
	}
	if d.Queue == nil {
		d.Queue = stubQueue{}
	}
	if d.Deliver == nil {
		router, err := delivery.NewRouter(nopMailer{},
			delivery.WithMailFrom("noreply@example.com"))
		if err != nil {
			panic(err)
		}
		d.Deliver = router
	}

	users, ok := d.Users.(*memUsers)
	if !ok {
		components := mustAuthenticationService(d)
		service := components.Service
		return &testService{Service: service, initializer: components.DeliveryInitializer, Adapter: newAuthenticator(service, cfg)}
	}
	pw, _ := d.Passwords.(*memPasswords)
	if pw == nil {
		pw = users.pw
	}
	sess, _ := d.Sessions.(*memSessions)
	if sess == nil {
		sess = users.sess
	}
	accounts, _ := d.OAuthAccounts.(*memOAuthAccounts)
	if accounts == nil {
		accounts = users.accounts
	}
	if accounts == nil {
		accounts = &memOAuthAccounts{}
	}
	grants, _ := d.AuthenticationGrants.(*memAuthGrants)
	ch, _ := d.Challenges.(*memChallenges)
	if pw != nil && sess != nil {
		wireCredentialFakes(users, pw, sess, accounts, grants, ch)
		if d.ActiveSessions == nil {
			d.ActiveSessions = memActiveSessions{users, sess}
		}
	}
	components := mustAuthenticationService(d)
	service := components.Service
	return &testService{Service: service, initializer: components.DeliveryInitializer, Adapter: newAuthenticator(service, cfg)}
}

type testService struct {
	initializer delivery.Initializer
	*authlogic.Service
	*Adapter
}

func mustAuthenticationService(d authenticationFixture) *authlogic.Components {
	d.RuntimeMode = environment.ModeDevelopment
	if d.Users == nil {
		d.Users = newMemUsers()
	}
	users, _ := d.Users.(*memUsers)
	if d.Identifiers == nil && users != nil {
		d.Identifiers = newMemIdentifiers(users)
	}
	if d.Sessions == nil {
		d.Sessions = &memSessions{m: map[string]session.Session{}}
	}
	if d.Passwords == nil {
		d.Passwords = &memPasswords{m: map[string]string{}}
	}
	if d.Hasher == nil {
		d.Hasher = fakeHasher{}
	}
	if d.TokenSigner == nil {
		d.TokenSigner = newFakeSigner()
	}
	if d.Limiter == nil {
		d.Limiter = ratelimiter.NewMemory()
	}
	if d.ActiveSessions == nil && users != nil {
		if sess, ok := d.Sessions.(*memSessions); ok {
			d.ActiveSessions = memActiveSessions{users, sess}
		}
	}
	if d.PasswordResets == nil {
		if ch, ok := d.Challenges.(*memChallenges); ok {
			pw, _ := d.Passwords.(*memPasswords)
			sess, _ := d.Sessions.(*memSessions)
			if pw != nil && sess != nil {
				d.PasswordResets = &memPasswordResets{ch: ch, pw: pw, sess: sess}
			}
		}
	}
	service, err := authlogic.New(authlogic.Repositories{Users: d.Users, Identifiers: d.Identifiers, Passwords: d.Passwords, Sessions: d.Sessions, UserAdmin: d.UserAdmin, PasswordlessRedeem: d.PasswordlessRedeem, ActiveSessions: d.ActiveSessions, Challenges: d.Challenges, PasswordResets: d.PasswordResets, ContactChanges: d.ContactChanges, CredentialMutations: d.CredentialMutations, AuthenticationGrants: d.AuthenticationGrants, SecurityEvents: d.SecurityEvents, OAuthAccounts: d.OAuthAccounts, OAuthStates: d.OAuthStates, ServiceAccounts: d.ServiceAccounts, APIKeys: d.APIKeys},
		d.TokenSigner,
		d.RuntimeMode,
		d.Limiter,
		authlogic.WithPassword(authlogic.PasswordConfig{Hasher: d.Hasher, ValidatePassword: d.ValidatePassword, Compromised: d.Compromised, CompromisedFailOpen: d.CompromisedFailOpen, PasswordFlowsDisabled: d.PasswordFlowsDisabled, RequireVerifiedEmail: d.RequireVerifiedEmail}),
		authlogic.WithSessions(authlogic.SessionsConfig{AccessTokenTTL: d.AccessTokenTTL, RefreshTTL: d.RefreshTTL}),
		authlogic.WithIdentity(authlogic.IdentityConfig{Normalizer: d.Normalizer, IdentifierKeyer: d.IdentifierKeyer, CredentialPolicy: d.CredentialPolicy}),
		authlogic.WithDelivery(authlogic.DeliveryConfig{Deliver: d.Deliver, Queue: d.Queue}),
		authlogic.WithOAuth(authlogic.OAuthConfig{Providers: d.Providers, TokenEncrypter: d.TokenEncrypter, OAuthCallbackBase: d.OAuthCallbackBase, OAuthNativeRedirectURIs: d.OAuthNativeRedirectURIs, TrustOAuthEmail: d.TrustOAuthEmail}),
		authlogic.WithPasswordless(authlogic.PasswordlessConfig{Passwordless: d.Passwordless, ProvisionOnRedeem: d.ProvisionOnRedeem}),
		authlogic.WithLinks(authlogic.LinksConfig{PublicAuthBaseURL: d.PublicAuthBaseURL, PasswordResetURL: d.PasswordResetURL, OAuthLinkBaseURL: d.OAuthLinkBaseURL, RedirectAllowlist: d.RedirectAllowlist}),
		authlogic.WithChallengeProtector(d.Protector),
		authlogic.WithUserAdminCheck(d.UserAdminCheck),
		authlogic.WithInvitations(d.Invitations),
		authlogic.WithLimits(d.AuthenticationLimits),
		authlogic.WithClock(d.Clock),
		authlogic.WithLogger(d.Logger),
		authlogic.WithIDs(d.IDs))
	if err != nil {
		panic(err)
	}
	return service
}

func (s *testService) Register(ctx context.Context, email, password, name string) (user.User, error) {
	return s.Service.Register(ctx, email, password, name)
}
