package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// --- compile-time seam assertions ---

var (
	_ oauth.Provider                      = (*fakeProvider)(nil)
	_ oauthaccount.OAuthAccountRepository = (*fakeOAuthAccounts)(nil)
	_ oauthstate.StateRepository          = (*fakeOAuthStates)(nil)
	_ cryptids.Encrypter                  = fakeEncrypter{}
	_ credential.MutationRepository       = (*fakeCredentialMutations)(nil)
)

// fakeCredentialMutations is the authentication service-test credential.MutationRepository over
// the linked fakeUsers/fakePasswords: Apply(RemovePassword) deletes the password
// under the owning user's revision-CAS, mirroring the store rail the OAuth
// adoption-revocation path (design §5.7/V5) removes a squatter password through.
type fakeCredentialMutations struct {
	users *fakeUsers
	pw    *fakePasswords
}

func (f *fakeCredentialMutations) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	u, err := f.users.Get(ctx, userID)
	if err != nil {
		return credential.MethodSet{}, err
	}
	set := credential.MethodSet{AuthRevision: u.AuthRevision}
	if _, err := f.pw.Get(ctx, userID); err == nil {
		set.HasPassword = true
	}
	return set, nil
}

func (f *fakeCredentialMutations) Apply(ctx context.Context, userID string, expectedAuthRevision int64, m credential.Mutation) error {
	if err := f.users.applyRevision(userID, expectedAuthRevision); err != nil {
		return err
	}
	if _, ok := m.(credential.RemovePassword); ok {
		f.pw.delete(userID)
	}
	return nil
}

// --- oauth fakes ---

// fakeProvider is an in-package stub oauth.Provider whose canned identity drives
// the callback branches. It records the state/nonce/verifier handed to
// GetAuthorizationURL so a test can replay the callback.
type fakeProvider struct {
	name           string
	oidc           bool
	trust          bool
	providerUserID string
	email          string
	emailVerified  bool
	profileName    string // the person's display name the provider reports, if any

	lastState         string
	lastNonce         string
	lastVerifier      string
	exchangeErr       error
	exchangedRedirect string
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) GetAuthorizationURL(req oauth.AuthorizationRequest) (string, error) {
	state, codeVerifier, nonce := req.State, req.CodeVerifier, req.Nonce
	p.lastState = state
	p.lastNonce = nonce
	p.lastVerifier = codeVerifier
	return "https://provider.example/authorize?state=" + state, nil
}

func (p *fakeProvider) ExchangeCode(_ context.Context, _, _, redirectURI string) (*oauth.TokenResponse, error) {
	p.exchangedRedirect = redirectURI
	if p.exchangeErr != nil {
		return nil, p.exchangeErr
	}
	return &oauth.TokenResponse{
		AccessToken:  "access-tok",
		RefreshToken: "refresh-tok",
		ExpiresIn:    3600,
		IDToken:      p.idToken(),
		TokenType:    "Bearer",
		Scopes:       "openid email",
	}, nil
}

func (p *fakeProvider) GetUserInfo(_ context.Context, _ string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{ProviderUserID: p.providerUserID, Email: p.email, EmailVerified: p.emailVerified, Name: p.profileName}, nil
}

type fakeOIDCProvider struct{ *fakeProvider }

func (p fakeOIDCProvider) ValidateIDToken(_ context.Context, _, nonce string) (*oauth.IDTokenClaims, error) {
	return &oauth.IDTokenClaims{Subject: p.providerUserID, Email: p.email, EmailVerified: p.emailVerified, Nonce: nonce, Name: p.profileName}, nil
}

func (p *fakeProvider) RefreshToken(_ context.Context, _ string) (*oauth.TokenResponse, error) {
	return nil, errors.New("unused")
}

type fakeOAuthAccounts struct {
	users *fakeUsers
	mu    sync.Mutex
	m     []oauthaccount.OAuthAccount
}

func newFakeOAuthAccounts() *fakeOAuthAccounts { return &fakeOAuthAccounts{} }

func (f *fakeOAuthAccounts) Create(_ context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.m {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, sdk.ErrAlreadyExists
		}
	}
	f.m = append(f.m, a)
	return a, nil
}

func (f *fakeOAuthAccounts) GetByProvider(_ context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.m {
		if a.Provider == provider && a.ProviderUserID == providerUserID {
			return a, nil
		}
	}
	return oauthaccount.OAuthAccount{}, sdk.ErrNotFound
}

func (f *fakeOAuthAccounts) ListByUser(_ context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []oauthaccount.OAuthAccount{}
	for _, a := range f.m {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeOAuthAccounts) Delete(_ context.Context, userID, provider string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.m[:0:0]
	deleted := false
	for _, a := range f.m {
		if a.UserID == userID && a.Provider == provider {
			deleted = true
			continue
		}
		kept = append(kept, a)
	}
	if !deleted {
		return sdk.ErrNotFound
	}
	f.m = kept
	return nil
}

type fakeOAuthStates struct {
	mu sync.Mutex
	m  map[string]oauthstate.State
}

func newFakeOAuthStates() *fakeOAuthStates { return &fakeOAuthStates{m: map[string]oauthstate.State{}} }

func (f *fakeOAuthStates) Create(_ context.Context, s oauthstate.State) (oauthstate.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[s.Token] = s
	return s, nil
}

func (f *fakeOAuthStates) Consume(_ context.Context, token string) (oauthstate.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[token]
	if !ok {
		return oauthstate.State{}, sdk.ErrNotFound
	}
	delete(f.m, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, sdk.ErrExpired
	}
	return s, nil
}

// pendingLinkToken returns the token of the single pending-link state, for the
// pending-link completion test.
func (f *fakeOAuthStates) pendingLinkToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for tok, s := range f.m {
		if s.Purpose == oauthstate.PurposePendingLink {
			return tok
		}
	}
	return ""
}

// fakeEncrypter is a reversible, deterministic stand-in: ciphertext is
// "enc:"+plaintext, so a test can assert both non-plaintext-at-rest and the drop
// behavior when the encrypter is nil.
type fakeEncrypter struct{}

func (fakeEncrypter) Encrypt(plaintext string) (string, error) { return "enc:" + plaintext, nil }
func (fakeEncrypter) Decrypt(ciphertext string) (string, error) {
	return ciphertext[len("enc:"):], nil
}

// --- oauth harness ---

type oauthHarness struct {
	flows    map[string]OAuthStart
	svc      *Service
	users    *fakeUsers
	idents   *fakeIdentifiers
	creds    *fakeCredentialMutations
	pw       *fakePasswords
	sess     *fakeSessions
	accounts *fakeOAuthAccounts
	states   *fakeOAuthStates
	mailer   *recordingMailer
	provider *fakeProvider
	events   *spySecurityEvents
}

// newOAuthHarness builds the OAuth fixture. opts mutate the assembled constructorConfig before
// construction, so a test can wire an otherwise-absent collaborator (the
// invitation resolver) without every call site growing a parameter.
func newOAuthHarness(t *testing.T, provider *fakeProvider, enc cryptids.Encrypter, opts ...func(*constructorConfig)) *oauthHarness {
	t.Helper()
	users := newFakeUsers()
	pw := newFakePasswords()
	h := &oauthHarness{
		users:    users,
		idents:   newFakeIdentifiers(users),
		creds:    &fakeCredentialMutations{users: users, pw: pw},
		pw:       pw,
		sess:     newFakeSessions(),
		accounts: newFakeOAuthAccounts(),
		states:   newFakeOAuthStates(),
		mailer:   &recordingMailer{},
		provider: provider,
		events:   newSpySecurityEvents(),
	}
	wireCredentialFakes(h.users, h.pw, h.sess, h.accounts, nil, nil)
	deps := constructorConfig{
		Users:               h.users,
		Identifiers:         h.idents,
		CredentialMutations: h.creds,
		Passwords:           h.pw,
		Sessions:            h.sess,
		ActiveSessions:      fakeActiveSessions{h.users, h.sess},
		Hasher:              &fakeHasher{},
		Limiter:             ratelimiter.NewMemory(),
		OAuthAccounts:       h.accounts,
		OAuthStates:         h.states,
		Providers:           []oauth.Provider{provider.port()},
		TrustOAuthEmail:     func(_ string, _ oauth.UserInfo) bool { return provider.trust },
		TokenEncrypter:      enc,
		OAuthCallbackBase:   "https://app.example.com",
		RedirectAllowlist:   []string{"https://app.example.com/welcome"},
		SecurityEvents:      h.events,
		TokenSigner:         newFakeSigner(),
	}
	for _, opt := range opts {
		opt(&deps)
	}
	h.svc = newServiceWithFakes(deps)
	wireSyncDelivery(t, h.svc, h.mailer, nil)
	return h
}

// seedSquatterCredential gives userID a password and a live session — the
// pre-existing credentials the adoption-revocation path (design §5.7/V5) must
// revoke. It returns the seeded session id so a test can assert its removal.
func (h *oauthHarness) seedSquatterCredential(t *testing.T, userID string) string {
	t.Helper()
	if err := h.pw.Set(context.Background(), userID, "hash:squatter"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	sess, _ := session.NewSession(userID, time.Hour, time.Now())
	sess.RefreshTokenHash = "squatter-refresh-" + userID
	created, err := h.sess.Create(context.Background(), sess)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return created.ID
}

// startState runs StartOAuth and returns the state token the provider recorded.
func (h *oauthHarness) startState(t *testing.T, redirectTo string) string {
	t.Helper()
	if _, err := h.startOAuth(context.Background(), h.provider.name, redirectTo); err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	if h.provider.lastState == "" {
		t.Fatal("provider did not receive a state token")
	}
	return h.provider.lastState
}

// --- tests ---

func TestOAuthEnabledGating(t *testing.T) {
	off := newServiceWithFakes(constructorConfig{Limiter: ratelimiter.NewMemory()})
	if off.OAuthEnabled() {
		t.Error("OAuthEnabled() true with no providers")
	}
	h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
	if !h.svc.OAuthEnabled() {
		t.Error("OAuthEnabled() false with a wired provider")
	}
}

func TestStartOAuthUnknownProvider(t *testing.T) {
	h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
	if _, err := h.startOAuth(context.Background(), "unknown", ""); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("StartOAuth(unknown): err=%v, want ErrNotFound", err)
	}
}

// TestResolveRedirect pins the browser-lane resolver: a safe same-origin relative
// path is honored WITHOUT an allowlist entry, an absolute target needs an exact
// allowlist match, and everything else — including the protocol-relative and
// backslash shapes a browser could normalize off-origin — falls back to "/".
func TestResolveRedirect(t *testing.T) {
	h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil) // allowlist: https://app.example.com/welcome
	tests := []struct {
		name, target, want string
	}{
		{"empty falls back", "", "/"},
		{"relative path honored unallowlisted", "/auth/account", "/auth/account"},
		{"relative path with query honored", "/settings/security?tab=links", "/settings/security?tab=links"},
		{"allowlisted absolute honored", "https://app.example.com/welcome", "https://app.example.com/welcome"},
		{"unlisted absolute falls back", "https://evil.example.com", "/"},
		{"protocol-relative falls back", "//evil.example.com", "/"},
		{"backslash trickery falls back", "/\\evil.example.com", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := h.svc.ResolveRedirect(tt.target); got != tt.want {
				t.Errorf("ResolveRedirect(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

// TestOAuthCallbackHonorsRelativeRedirect proves the relative-path rule end-to-end
// in the OAuth lane: a start carrying a safe same-origin relative redirect (the
// account page's link affordance shape) lands the callback on that path with no
// allowlist entry for it.
func TestOAuthCallbackHonorsRelativeRedirect(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-rel", email: "rel@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "/auth/account")
	res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.RedirectTo != "/auth/account" {
		t.Errorf("RedirectTo = %q, want the relative path honored without allowlisting", res.RedirectTo)
	}
}

// TestOAuthCallbackRegisterAndLink covers branch 3: no user exists → a new
// password-less user is created (verified per host email policy), linked,
// and a session minted.
func TestOAuthCallbackRegisterAndLink(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-new", email: "new@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "https://app.example.com/welcome")
	res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionRegister {
		t.Fatalf("Action = %q, want register", res.Action)
	}
	if res.Token == "" {
		t.Error("register did not mint a session")
	}
	// The registered primary email identifier is created verified (design §5.7 gate).
	rit, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "new@example.com")
	if err != nil || !rit.Verified() {
		t.Errorf("trusted verified provider email did not create a verified identifier: %+v err=%v", rit, err)
	}
	if res.RedirectTo != "https://app.example.com/welcome" {
		t.Errorf("RedirectTo = %q, want the allowlisted target", res.RedirectTo)
	}
	if id, _, ok := h.svc.verifyBearerClaims(res.Token); !ok || id != res.User.ID {
		t.Errorf("minted oauth access token = (%q, %v), want (%q, true)", id, ok, res.User.ID)
	}
	if res.RefreshToken == "" {
		t.Error("register did not mint a refresh token")
	}
	if _, err := h.accounts.GetByProvider(context.Background(), "google", "g-new"); err != nil {
		t.Errorf("link not persisted: %v", err)
	}
}

// TestOAuthCallbackRegisterCarriesProviderName proves that first-sign-in
// provisioning seeds the account's display name from the provider's reported
// name, on BOTH the OIDC id-token lane and the userinfo lane. Without it every
// OAuth-provisioned account is minted nameless (coordination-hub #152).
func TestOAuthCallbackRegisterCarriesProviderName(t *testing.T) {
	for _, tc := range []struct {
		name string
		oidc bool
	}{
		{"oidc id-token lane", true},
		{"userinfo lane", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{
				name: "google", oidc: tc.oidc, trust: true,
				providerUserID: "g-named", email: "named@example.com", emailVerified: true,
				profileName: "Ada Lovelace",
			}
			h := newOAuthHarness(t, p, nil)

			state := h.startState(t, "")
			res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
			if err != nil {
				t.Fatalf("OAuthCallback: %v", err)
			}
			if res.Action != ActionRegister {
				t.Fatalf("Action = %q, want register", res.Action)
			}
			if res.User.DisplayName != "Ada Lovelace" {
				t.Errorf("provisioned display name = %q, want %q", res.User.DisplayName, "Ada Lovelace")
			}
		})
	}
}

// TestOAuthCallbackUntrustedProviderRefused proves the §5.7 verified-provenance
// gate: an untrusted provider (the integration does not map its verified
// assertion) never registers or matches, even when it claims email_verified.
func TestOAuthCallbackUntrustedProviderRefused(t *testing.T) {
	p := &fakeProvider{name: "sketchy", trust: false, providerUserID: "s-1", email: "maybe@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "")
	_, err := h.svc.OAuthCallback(context.Background(), "sketchy", h.callbackRequest(state))
	if !errors.Is(err, ErrProviderEmailUnverified) {
		t.Fatalf("OAuthCallback(untrusted provider): err=%v, want ErrProviderEmailUnverified", err)
	}
	if len(h.users.byID) != 0 {
		t.Errorf("untrusted provider registered a user: %d", len(h.users.byID))
	}
	if len(h.accounts.m) != 0 {
		t.Errorf("untrusted provider created a link: %d", len(h.accounts.m))
	}
}

// TestOAuthCallbackUnverifiedEmailRefused proves a trusted provider that does NOT
// assert the address as verified is still refused: verified provenance requires
// both a mapping integration and an asserted-verified claim (design §5.7).
func TestOAuthCallbackUnverifiedEmailRefused(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-unv", email: "unv@example.com", emailVerified: false}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); !errors.Is(err, ErrProviderEmailUnverified) {
		t.Fatalf("OAuthCallback(unverified email): err=%v, want ErrProviderEmailUnverified", err)
	}
}

// TestOAuthCallbackUnverifiedEmailStillLoginsExistingLink proves the provenance
// gate never blocks branch 1: an already-linked identity logs in regardless of the
// provider's email-verified claim (branch 1 is keyed on the provider user id).
func TestOAuthCallbackUnverifiedEmailStillLoginsExistingLink(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-linked", email: "linked@example.com", emailVerified: false}
	h := newOAuthHarness(t, p, nil)

	pre := h.mustOAuthUser(t, "linked@example.com")
	acct, _ := oauthaccount.New(pre.ID, "google", "g-linked", time.Now())
	if _, err := h.accounts.Create(context.Background(), acct); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionLogin || res.User.ID != pre.ID {
		t.Fatalf("existing link with unverified email: action=%q user=%q, want login for %q", res.Action, res.User.ID, pre.ID)
	}
}

// TestOAuthCallbackExistingLinkLogin covers branch 1: the provider identity is
// already linked → login as the linked user, no new account row.
func TestOAuthCallbackExistingLinkLogin(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-existing", email: "e@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	// Pre-existing user + link.
	pre := h.mustOAuthUser(t, "e@example.com")
	acct, _ := oauthaccount.New(pre.ID, "google", "g-existing", time.Now())
	if _, err := h.accounts.Create(context.Background(), acct); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionLogin {
		t.Fatalf("Action = %q, want login", res.Action)
	}
	if res.User.ID != pre.ID {
		t.Errorf("logged in as %q, want the linked user %q", res.User.ID, pre.ID)
	}
	if len(h.accounts.m) != 1 {
		t.Errorf("existing-link login created a duplicate account: %d rows", len(h.accounts.m))
	}
	if id, _, ok := h.svc.verifyBearerClaims(res.Token); !ok || id != pre.ID {
		t.Errorf("login access token = (%q, %v), want (%q, true)", id, ok, pre.ID)
	}
}

// TestOAuthCallbackPendingLinkThenVerify covers branch 2 (anti-takeover): a user
// with the provider's email but no link → a mailed pending-link secret, NO link
// created until VerifyLink redeems it.
func TestOAuthCallbackPendingLinkThenVerify(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-pending", email: "owner@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	owner := h.mustOAuthUser(t, "owner@example.com")

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionPendingLink {
		t.Fatalf("Action = %q, want pending_link", res.Action)
	}
	if res.Token != "" {
		t.Error("pending link must not mint a session")
	}
	if len(h.accounts.m) != 0 {
		t.Fatalf("pending link created an account before verification: %d rows", len(h.accounts.m))
	}
	if h.mailer.count() == 0 {
		t.Fatal("pending link did not mail a secret")
	}

	token := h.states.pendingLinkToken()
	if token == "" {
		t.Fatal("no pending-link state stored")
	}
	vres, err := h.svc.VerifyLink(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if vres.Action != ActionLinked || vres.User.ID != owner.ID {
		t.Errorf("VerifyLink result = %+v, want linked for %q", vres, owner.ID)
	}
	if vres.Token == "" {
		t.Error("VerifyLink did not mint a session")
	}
	if _, err := h.accounts.GetByProvider(context.Background(), "google", "g-pending"); err != nil {
		t.Errorf("link not created after VerifyLink: %v", err)
	}
	// Single-use: the pending token cannot be redeemed twice.
	if _, err := h.svc.VerifyLink(context.Background(), token); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("second VerifyLink: err=%v, want ErrNotFound", err)
	}
}

// completePendingLink runs a callback to the pending-link branch and returns the
// mailed pending-link token. It fails the test if the callback did not yield a
// pending link.
func (h *oauthHarness) completePendingLink(t *testing.T) string {
	t.Helper()
	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), h.provider.name, h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionPendingLink {
		t.Fatalf("Action = %q, want pending_link", res.Action)
	}
	token := h.states.pendingLinkToken()
	if token == "" {
		t.Fatal("no pending-link state stored")
	}
	return token
}

// TestOAuthAdoptionRevokesSquatterCredentials covers the adoption-revocation
// invariant (design §5.7/V5): when the branch-2 match hit a login claim that was
// UNVERIFIED at flow start, completing the pending link revokes the pre-existing
// (squatter) password and sessions BEFORE the adopting link is created.
func TestOAuthAdoptionRevokesSquatterCredentials(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-adopt", email: "victim@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	squatter := h.mustOAuthUserVerified(t, "victim@example.com", false)
	squatterSession := h.seedSquatterCredential(t, squatter.ID)

	token := h.completePendingLink(t)
	if len(h.accounts.m) != 0 {
		t.Fatalf("pending link created a link before verification: %d", len(h.accounts.m))
	}

	vres, err := h.svc.VerifyLink(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if vres.Action != ActionLinked || vres.User.ID != squatter.ID {
		t.Fatalf("VerifyLink = %+v, want linked for %q", vres, squatter.ID)
	}
	if _, err := h.pw.Get(context.Background(), squatter.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("squatter password survived adoption: err=%v, want ErrNotFound", err)
	}
	if _, err := h.sess.Get(context.Background(), squatterSession); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("squatter session survived adoption: err=%v, want ErrNotFound", err)
	}
	if _, err := h.accounts.GetByProvider(context.Background(), "google", "g-adopt"); err != nil {
		t.Errorf("adopting link not created: %v", err)
	}
}

// TestOAuthAdoptionCapturedFlagTOCTOU proves the unverified-at-flow-start fact is
// read verbatim from the captured payload, never re-derived at completion (design
// §5.7): even after the matched identifier becomes verified between start and
// finish, revocation still fires because the CAPTURED flag says unverified.
func TestOAuthAdoptionCapturedFlagTOCTOU(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-toctou", email: "toctou@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	squatter := h.mustOAuthUserVerified(t, "toctou@example.com", false)
	h.seedSquatterCredential(t, squatter.ID)

	token := h.completePendingLink(t)

	// The identifier is verified AFTER the flag was captured — a re-derivation at
	// completion would now read "verified" and skip revocation.
	h.idents.markAllVerified(squatter.ID, time.Now())

	if _, err := h.svc.VerifyLink(context.Background(), token); err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if _, err := h.pw.Get(context.Background(), squatter.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("captured flag not honored — squatter password survived: err=%v, want ErrNotFound", err)
	}
}

// A retired address no longer authorizes adding a credential to its former owner.
func TestOAuthPendingLinkRejectsRetiredIdentifier(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-change", email: "change@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	owner := h.mustOAuthUserVerified(t, "change@example.com", false)
	token := h.completePendingLink(t)
	h.idents.retireAll(owner.ID, time.Now())
	if _, err := h.svc.VerifyLink(context.Background(), token); !errors.Is(err, ErrInvalidOAuthState) {
		t.Fatalf("retired identifier completion: %v", err)
	}
	if _, err := h.accounts.GetByProvider(context.Background(), "google", "g-change"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("provider linked after address retirement: %v", err)
	}
}

// TestOAuthPendingLinkVerifiedMatchNoRevocation proves the flag gates revocation:
// a branch-2 match on a VERIFIED identifier is a legitimate owner linking a new
// provider, so the pending-link completion never revokes their password/sessions.
func TestOAuthPendingLinkVerifiedMatchNoRevocation(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-owner", email: "owner2@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	owner := h.mustOAuthUserVerified(t, "owner2@example.com", true)
	ownerSession := h.seedSquatterCredential(t, owner.ID)

	token := h.completePendingLink(t)
	if _, err := h.svc.VerifyLink(context.Background(), token); err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if _, err := h.pw.Get(context.Background(), owner.ID); err != nil {
		t.Errorf("verified match wrongly revoked the owner password: %v", err)
	}
	if _, err := h.sess.Get(context.Background(), ownerSession); err != nil {
		t.Errorf("verified match wrongly revoked the owner session: %v", err)
	}
}

// TestOAuthAdoptionFailsClosedWithoutRail proves adoption fails closed when the
// credential-mutation rail is unwired: rather than completing the link and leaving
// the squatter credential alive, VerifyLink refuses (design §5.7/V5).
func TestOAuthAdoptionUsesAtomicLinkWithoutRemovalRail(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-norail", email: "norail@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	h.svc.credentialMutations = nil // simulate the rail being unwired

	squatter := h.mustOAuthUserVerified(t, "norail@example.com", false)
	h.seedSquatterCredential(t, squatter.ID)

	token := h.completePendingLink(t)
	if _, err := h.svc.VerifyLink(context.Background(), token); err != nil {
		t.Fatalf("VerifyLink without rail: err=%v, want atomic link success", err)
	}
	if len(h.accounts.m) != 1 {
		t.Fatalf("atomic adoption link count=%d", len(h.accounts.m))
	}
	if _, err := h.pw.Get(context.Background(), squatter.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("squatter password survived atomic adoption: %v", err)
	}
}

// TestOAuthLinkStartFlow covers the session-gated explicit link: StartLink →
// callback attaches the identity to the linking user (no register/login branch).
func TestOAuthLinkStartFlow(t *testing.T) {
	p := &fakeProvider{name: "google", oidc: true, trust: true, providerUserID: "g-link", email: "me@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	u := h.mustOAuthUser(t, "me@example.com")
	if _, err := h.startLink(context.Background(), u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if h.provider.lastNonce == "" {
		t.Error("OIDC link start did not generate a nonce")
	}
	state := h.provider.lastState

	res, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state))
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionLinked {
		t.Fatalf("Action = %q, want linked", res.Action)
	}
	got, err := h.accounts.GetByProvider(context.Background(), "google", "g-link")
	if err != nil || got.UserID != u.ID {
		t.Errorf("link not attached to the linking user: %+v err=%v", got, err)
	}
}

// TestOAuthTokenEncryptionAndDrop proves provider tokens are encrypted at rest
// when a TokenEncrypter is wired, and dropped (empty) when it is nil.
func TestOAuthTokenEncryptionAndDrop(t *testing.T) {
	ctx := context.Background()

	t.Run("EncrypterWired", func(t *testing.T) {
		p := &fakeProvider{name: "google", trust: true, providerUserID: "g-enc", email: "enc@example.com", emailVerified: true}
		h := newOAuthHarness(t, p, fakeEncrypter{})
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); err != nil {
			t.Fatalf("OAuthCallback: %v", err)
		}
		acct, _ := h.accounts.GetByProvider(ctx, "google", "g-enc")
		if acct.AccessToken != "enc:access-tok" || acct.RefreshToken != "enc:refresh-tok" {
			t.Errorf("tokens not encrypted at rest: %+v", acct)
		}
	})

	t.Run("EncrypterNilDropsTokens", func(t *testing.T) {
		p := &fakeProvider{name: "google", trust: true, providerUserID: "g-drop", email: "drop@example.com", emailVerified: true}
		h := newOAuthHarness(t, p, nil)
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); err != nil {
			t.Fatalf("OAuthCallback: %v", err)
		}
		acct, _ := h.accounts.GetByProvider(ctx, "google", "g-drop")
		if acct.AccessToken != "" || acct.RefreshToken != "" {
			t.Errorf("nil encrypter must drop provider tokens, got %+v", acct)
		}
	})
}

// TestOAuthCallbackStateSingleUse proves a consumed flow state cannot be replayed.
func TestOAuthCallbackStateSingleUse(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-once", email: "once@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); err != nil {
		t.Fatalf("first OAuthCallback: %v", err)
	}
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("replayed state: err=%v, want ErrNotFound", err)
	}
}

// TestOAuthCallbackStateExpired proves an expired flow state surfaces ErrExpired
// through the callback (the store deletes it regardless).
func TestOAuthCallbackStateExpired(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-exp", email: "exp@example.com"}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "")
	key := h.lookupKey(state)
	expired := h.states.m[key]
	expired.ExpiresAt = time.Now().Add(-time.Hour)
	h.states.m[key] = expired
	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(state)); !errors.Is(err, sdk.ErrExpired) {
		t.Errorf("expired state callback: err=%v, want ErrExpired", err)
	}
}

// TestOAuthCallbackWrongProviderState rejects a state minted for another provider.
func TestOAuthCallbackWrongProviderState(t *testing.T) {
	p := &fakeProvider{name: "google", providerUserID: "g-x", email: "x@example.com"}
	h := newOAuthHarness(t, p, nil)

	payload, _ := json.Marshal(flowState{CodeVerifier: "v", RedirectTo: "/"})
	mismatched := oauthstate.New("github", oauthstate.PurposeFlow, payload, time.Hour, time.Now())
	h.states.Create(context.Background(), mismatched)

	if _, err := h.svc.OAuthCallback(context.Background(), "google", h.callbackRequest(mismatched.Token)); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("provider-mismatched state: err=%v, want ErrNotFound", err)
	}
}

// mustOAuthUser creates a password-less user together with a VERIFIED primary
// email identifier (login+recovery+notification) so branch-2 identifier matching
// resolves it, mirroring an OAuth-registered or fully-verified account.
func (h *oauthHarness) mustOAuthUser(t *testing.T, email string) user.User {
	t.Helper()
	return h.mustOAuthUserVerified(t, email, true)
}

// mustOAuthUserVerified creates a password-less user with a primary login+recovery
// email identifier that is verified (verified=true) or UNVERIFIED (verified=false).
// An unverified login claim is the squatter fixture the adoption-revocation path
// (design §5.7/V5) revokes.
func (h *oauthHarness) mustOAuthUserVerified(t *testing.T, email string, verified bool) user.User {
	t.Helper()
	now := time.Now()
	u := user.NewUser(sdk.IDGenerator{}, "", now)
	var ident identifier.Identifier
	var err error
	if verified {
		ident, err = identifier.New(sdk.IDGenerator{}, identifier.DefaultNormalizer{}, "", identifier.KindEmail, email,
			identifier.Uses{Login: true, Recovery: true, Notification: true}, true, now, now)
	} else {
		ident, err = identifier.NewRegistrationEmail(sdk.IDGenerator{}, identifier.DefaultNormalizer{}, "", email, now)
	}
	if err != nil {
		t.Fatalf("new identifier: %v", err)
	}
	created, _, err := h.users.CreateWithPrimaryIdentifier(context.Background(), u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	return created
}

func (p *fakeProvider) port() oauth.Provider {
	if p.oidc {
		return fakeOIDCProvider{p}
	}
	return p
}
func (p *fakeProvider) idToken() string {
	if p.oidc {
		return "id-tok"
	}
	return ""
}

// These helpers model the initiating client retaining proof independently of the
// public state observed by the fake provider. Tests can retain multiple flows.
func (h *oauthHarness) rememberFlow(flow OAuthStart, err error) (OAuthStart, error) {
	if err == nil {
		if h.flows == nil {
			h.flows = make(map[string]OAuthStart)
		}
		h.flows[flow.State] = flow
	}
	return flow, err
}
func (h *oauthHarness) startOAuth(ctx context.Context, provider, redirect string) (OAuthStart, error) {
	return h.rememberFlow(h.svc.StartOAuth(ctx, provider, OAuthStartRequest{Mode: OAuthBrowser, RedirectTo: redirect}))
}
func (h *oauthHarness) startLink(ctx context.Context, userID, provider, redirect string) (OAuthStart, error) {
	return h.rememberFlow(h.svc.StartLink(ctx, userID, provider, OAuthStartRequest{Mode: OAuthBrowser, RedirectTo: redirect}))
}
func (h *oauthHarness) callbackRequest(state string) OAuthCallbackRequest {
	return OAuthCallbackRequest{Mode: OAuthBrowser, State: state, FlowSecret: h.flows[state].FlowSecret, Code: "code"}
}
func (h *oauthHarness) lookupKey(state string) string {
	return flowLookupKey(h.provider.name, OAuthBrowser, state, h.flows[state].FlowSecret)
}

func TestOAuthLinkCannotReusePreMutationStart(t *testing.T) {
	p := &fakeProvider{name: "google", oidc: true, trust: true, providerUserID: "pre-reset-provider", email: "owner@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	ctx := context.Background()
	u := h.mustOAuthUser(t, "owner@example.com")
	if _, err := h.startLink(ctx, u.ID, "google", ""); err != nil {
		t.Fatal(err)
	}
	state := h.provider.lastState
	if err := h.pw.Set(ctx, u.ID, "hash:replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("old link start: %v", err)
	}
	if _, err := h.accounts.GetByProvider(ctx, "google", p.providerUserID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("pre-mutation link persisted: %v", err)
	}
}

func TestOAuthPendingLinksKeepIndependentDelivery(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "first-provider", email: "pending@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	h.mustOAuthUser(t, p.email)
	dq := h.svc.queue.(*drainingQueue)
	h.svc.queue = dq.svc
	for _, providerID := range []string{"first-provider", "second-provider"} {
		p.providerUserID = providerID
		state := h.startState(t, "")
		res, err := h.svc.OAuthCallback(context.Background(), p.name, h.callbackRequest(state))
		if err != nil || res.Action != ActionPendingLink {
			t.Fatalf("callback: %+v %v", res, err)
		}
	}
	dq.disp.mu.Lock()
	count := len(dq.disp.items)
	dq.disp.mu.Unlock()
	if count != 2 {
		t.Fatalf("distinct pending flows shared %d delivery jobs, want 2", count)
	}
	dq.disp.drain(t, dq.proc)
	if h.mailer.count() != 2 {
		t.Fatalf("pending flow deliveries=%d", h.mailer.count())
	}
}

func TestOAuthPendingLinkCannotMutatePostIssuanceCredentials(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "adoption", true: "verified-link"}[verified], func(t *testing.T) {
			p := &fakeProvider{name: "google", trust: true, providerUserID: "delayed-link", email: "delayed@example.com", emailVerified: true}
			h := newOAuthHarness(t, p, nil)
			owner := h.mustOAuthUserVerified(t, p.email, verified)
			token := h.completePendingLink(t)
			ctx := context.Background()
			if err := h.pw.Set(ctx, owner.ID, "hash:post-issuance"); err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.VerifyLink(ctx, token); !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("delayed link: %v", err)
			}
			if hash, err := h.pw.Get(ctx, owner.ID); err != nil || hash != "hash:post-issuance" {
				t.Fatalf("replacement password changed: %v", err)
			}
			if _, err := h.accounts.GetByProvider(ctx, p.name, p.providerUserID); !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("delayed provider attached: %v", err)
			}
		})
	}
}

func TestOAuthPendingLinkRequiresIssuanceRevision(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "legacy-link", email: "legacy@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	h.mustOAuthUser(t, p.email)
	token := h.completePendingLink(t)
	h.states.mu.Lock()
	state := h.states.m[token]
	var payload pendingLink
	if err := json.Unmarshal(state.Payload, &payload); err != nil {
		h.states.mu.Unlock()
		t.Fatal(err)
	}
	payload.AuthRevision = nil
	state.Payload, _ = json.Marshal(payload)
	h.states.m[token] = state
	h.states.mu.Unlock()
	if _, err := h.svc.VerifyLink(context.Background(), token); !errors.Is(err, ErrInvalidOAuthState) {
		t.Fatalf("unbound pending flow: %v", err)
	}
}
