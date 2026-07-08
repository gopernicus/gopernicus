package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthstate"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// --- compile-time seam assertions ---

var (
	_ oauth.Provider                      = (*fakeProvider)(nil)
	_ oauthaccount.OAuthAccountRepository = (*fakeOAuthAccounts)(nil)
	_ oauthstate.StateRepository          = (*fakeOAuthStates)(nil)
	_ cryptids.Encrypter                  = fakeEncrypter{}
)

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

	lastState    string
	lastNonce    string
	lastVerifier string
	exchangeErr  error
}

func (p *fakeProvider) Name() string                 { return p.name }
func (p *fakeProvider) SupportsOIDC() bool           { return p.oidc }
func (p *fakeProvider) TrustEmailVerification() bool { return p.trust }

func (p *fakeProvider) GetAuthorizationURL(state, codeVerifier, nonce, redirectURI string) string {
	p.lastState = state
	p.lastNonce = nonce
	p.lastVerifier = codeVerifier
	return "https://provider.example/authorize?state=" + state
}

func (p *fakeProvider) ExchangeCode(_ context.Context, _, _, _ string) (*oauth.TokenResponse, error) {
	if p.exchangeErr != nil {
		return nil, p.exchangeErr
	}
	return &oauth.TokenResponse{
		AccessToken:  "access-tok",
		RefreshToken: "refresh-tok",
		ExpiresIn:    3600,
		IDToken:      "id-tok",
		TokenType:    "Bearer",
		Scopes:       "openid email",
	}, nil
}

func (p *fakeProvider) GetUserInfo(_ context.Context, _ string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{ProviderUserID: p.providerUserID, Email: p.email, EmailVerified: p.emailVerified}, nil
}

func (p *fakeProvider) ValidateIDToken(_ context.Context, _, nonce string) (*oauth.IDTokenClaims, error) {
	return &oauth.IDTokenClaims{Subject: p.providerUserID, Email: p.email, EmailVerified: p.emailVerified, Nonce: nonce}, nil
}

func (p *fakeProvider) RefreshToken(_ context.Context, _ string) (*oauth.TokenResponse, error) {
	return nil, errors.New("unused")
}

type fakeOAuthAccounts struct {
	mu sync.Mutex
	m  []oauthaccount.OAuthAccount
}

func newFakeOAuthAccounts() *fakeOAuthAccounts { return &fakeOAuthAccounts{} }

func (f *fakeOAuthAccounts) Create(_ context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.m {
		if ex.Provider == a.Provider && ex.ProviderUserID == a.ProviderUserID {
			return oauthaccount.OAuthAccount{}, errs.ErrAlreadyExists
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
	return oauthaccount.OAuthAccount{}, errs.ErrNotFound
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
		return errs.ErrNotFound
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
		return oauthstate.State{}, errs.ErrNotFound
	}
	delete(f.m, token)
	if s.Expired(time.Now()) {
		return oauthstate.State{}, errs.ErrExpired
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
	svc      *Service
	users    *fakeUsers
	pw       *fakePasswords
	sess     *fakeSessions
	accounts *fakeOAuthAccounts
	states   *fakeOAuthStates
	mailer   *recordingMailer
	provider *fakeProvider
	events   *spySecurityEvents
}

func newOAuthHarness(t *testing.T, provider *fakeProvider, enc cryptids.Encrypter) *oauthHarness {
	t.Helper()
	h := &oauthHarness{
		users:    newFakeUsers(),
		pw:       newFakePasswords(),
		sess:     newFakeSessions(),
		accounts: newFakeOAuthAccounts(),
		states:   newFakeOAuthStates(),
		mailer:   &recordingMailer{},
		provider: provider,
		events:   newSpySecurityEvents(),
	}
	h.svc = NewService(Deps{
		Users:             h.users,
		Passwords:         h.pw,
		Sessions:          h.sess,
		Codes:             newFakeCodes(),
		Tokens:            newFakeTokens(),
		Hasher:            &fakeHasher{},
		Mailer:            h.mailer,
		MailFrom:          "noreply@example.com",
		Limiter:           ratelimiter.NewMemory(),
		Cookie:            CookieConfig{},
		OAuthAccounts:     h.accounts,
		OAuthStates:       h.states,
		Providers:         []oauth.Provider{provider},
		TokenEncrypter:    enc,
		OAuthCallbackBase: "https://app.example.com",
		RedirectAllowlist: []string{"https://app.example.com/welcome"},
		SecurityEvents:    h.events,
	})
	return h
}

// startState runs StartOAuth and returns the state token the provider recorded.
func (h *oauthHarness) startState(t *testing.T, redirectTo string) string {
	t.Helper()
	if _, err := h.svc.StartOAuth(context.Background(), h.provider.name, redirectTo); err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	if h.provider.lastState == "" {
		t.Fatal("provider did not receive a state token")
	}
	return h.provider.lastState
}

// --- tests ---

func TestOAuthEnabledGating(t *testing.T) {
	off := NewService(Deps{Limiter: ratelimiter.NewMemory()})
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
	if _, err := h.svc.StartOAuth(context.Background(), "unknown", ""); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("StartOAuth(unknown): err=%v, want ErrNotFound", err)
	}
}

// TestOAuthCallbackRegisterAndLink covers branch 3: no user exists → a new
// password-less user is created (verified per TrustEmailVerification), linked,
// and a session minted.
func TestOAuthCallbackRegisterAndLink(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-new", email: "new@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "https://app.example.com/welcome")
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.Action != ActionRegister {
		t.Fatalf("Action = %q, want register", res.Action)
	}
	if res.Token == "" {
		t.Error("register did not mint a session")
	}
	if !res.User.EmailVerified {
		t.Error("trusted verified provider email did not mark the user verified")
	}
	if res.RedirectTo != "https://app.example.com/welcome" {
		t.Errorf("RedirectTo = %q, want the allowlisted target", res.RedirectTo)
	}
	if _, err := h.svc.ValidateSession(context.Background(), res.Token); err != nil {
		t.Errorf("minted oauth session not valid: %v", err)
	}
	if _, err := h.accounts.GetByProvider(context.Background(), "google", "g-new"); err != nil {
		t.Errorf("link not persisted: %v", err)
	}
}

// TestOAuthCallbackRegisterUntrustedEmailUnverified proves TrustEmailVerification
// gates the verified flag: an untrusted provider leaves the new user unverified
// even when it claims email_verified.
func TestOAuthCallbackRegisterUntrustedEmailUnverified(t *testing.T) {
	p := &fakeProvider{name: "sketchy", trust: false, providerUserID: "s-1", email: "maybe@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	state := h.startState(t, "")
	res, err := h.svc.OAuthCallback(context.Background(), "sketchy", "code", state)
	if err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if res.User.EmailVerified {
		t.Error("untrusted provider must not mark the new user verified")
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
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
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
	if _, err := h.svc.ValidateSession(context.Background(), res.Token); err != nil {
		t.Errorf("login session not valid: %v", err)
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
	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
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
	if _, err := h.svc.VerifyLink(context.Background(), token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("second VerifyLink: err=%v, want ErrNotFound", err)
	}
}

// TestOAuthLinkStartFlow covers the session-gated explicit link: StartLink →
// callback attaches the identity to the linking user (no register/login branch).
func TestOAuthLinkStartFlow(t *testing.T) {
	p := &fakeProvider{name: "google", oidc: true, trust: true, providerUserID: "g-link", email: "me@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)

	u := h.mustOAuthUser(t, "me@example.com")
	if _, err := h.svc.StartLink(context.Background(), u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if h.provider.lastNonce == "" {
		t.Error("OIDC link start did not generate a nonce")
	}
	state := h.provider.lastState

	res, err := h.svc.OAuthCallback(context.Background(), "google", "code", state)
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

// TestOAuthUnlinkLastMethodProtection covers the refusal when the link is the
// only credential, and the allowed paths (password present, or another link).
func TestOAuthUnlinkLastMethodProtection(t *testing.T) {
	ctx := context.Background()

	// Only credential, no password → refuse.
	t.Run("OnlyCredentialNoPassword", func(t *testing.T) {
		h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
		u := h.mustOAuthUser(t, "solo@example.com")
		acct, _ := oauthaccount.New(u.ID, "google", "g-solo", time.Now())
		h.accounts.Create(ctx, acct)
		if err := h.svc.Unlink(ctx, u.ID, "google"); !errors.Is(err, errs.ErrConflict) {
			t.Errorf("Unlink last method: err=%v, want ErrConflict (ErrLastAuthMethod)", err)
		}
		if !errors.Is(h.svc.Unlink(ctx, u.ID, "google"), ErrLastAuthMethod) {
			t.Error("Unlink last method must be ErrLastAuthMethod")
		}
	})

	// Has a password → the link is not the only credential; unlink allowed.
	t.Run("WithPassword", func(t *testing.T) {
		h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
		u := h.mustOAuthUser(t, "pw@example.com")
		h.pw.Set(ctx, u.ID, "hash:secret")
		acct, _ := oauthaccount.New(u.ID, "google", "g-pw", time.Now())
		h.accounts.Create(ctx, acct)
		if err := h.svc.Unlink(ctx, u.ID, "google"); err != nil {
			t.Errorf("Unlink with password set: %v", err)
		}
	})

	// Two links, no password → unlinking one still leaves a credential; allowed.
	t.Run("SecondOfTwo", func(t *testing.T) {
		h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
		u := h.mustOAuthUser(t, "two@example.com")
		g, _ := oauthaccount.New(u.ID, "google", "g-two", time.Now())
		gh, _ := oauthaccount.New(u.ID, "github", "gh-two", time.Now())
		h.accounts.Create(ctx, g)
		h.accounts.Create(ctx, gh)
		if err := h.svc.Unlink(ctx, u.ID, "google"); err != nil {
			t.Errorf("Unlink one of two links: %v", err)
		}
	})

	// Absent link → ErrNotFound.
	t.Run("AbsentLink", func(t *testing.T) {
		h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
		u := h.mustOAuthUser(t, "none@example.com")
		if err := h.svc.Unlink(ctx, u.ID, "google"); !errors.Is(err, errs.ErrNotFound) {
			t.Errorf("Unlink absent: err=%v, want ErrNotFound", err)
		}
	})
}

// TestOAuthTokenEncryptionAndDrop proves provider tokens are encrypted at rest
// when a TokenEncrypter is wired, and dropped (empty) when it is nil.
func TestOAuthTokenEncryptionAndDrop(t *testing.T) {
	ctx := context.Background()

	t.Run("EncrypterWired", func(t *testing.T) {
		p := &fakeProvider{name: "google", trust: true, providerUserID: "g-enc", email: "enc@example.com", emailVerified: true}
		h := newOAuthHarness(t, p, fakeEncrypter{})
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", "code", state); err != nil {
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
		if _, err := h.svc.OAuthCallback(ctx, "google", "code", state); err != nil {
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
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", state); err != nil {
		t.Fatalf("first OAuthCallback: %v", err)
	}
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", state); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("replayed state: err=%v, want ErrNotFound", err)
	}
}

// TestOAuthCallbackStateExpired proves an expired flow state surfaces ErrExpired
// through the callback (the store deletes it regardless).
func TestOAuthCallbackStateExpired(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-exp", email: "exp@example.com"}
	h := newOAuthHarness(t, p, nil)

	payload, _ := json.Marshal(flowState{CodeVerifier: "v", RedirectTo: "/"})
	expired := oauthstate.New("google", oauthstate.PurposeFlow, payload, time.Minute, time.Now().Add(-time.Hour))
	h.states.Create(context.Background(), expired)

	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", expired.Token); !errors.Is(err, errs.ErrExpired) {
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

	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", mismatched.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("provider-mismatched state: err=%v, want ErrNotFound", err)
	}
}

// mustOAuthUser creates a password-less user directly in the user store and
// returns it (mirroring an OAuth-registered account).
func (h *oauthHarness) mustOAuthUser(t *testing.T, email string) user.User {
	t.Helper()
	u, err := user.NewUser(email, "", time.Now())
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	created, err := h.users.Create(context.Background(), u)
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	return created
}
