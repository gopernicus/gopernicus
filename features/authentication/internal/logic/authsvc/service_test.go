package authsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// --- compile-time seam assertions ---

var (
	_ Hasher                          = (*fakeHasher)(nil)
	_ email.Sender                    = (*recordingMailer)(nil)
	_ user.UserRepository             = (*fakeUsers)(nil)
	_ identifier.IdentifierRepository = (*fakeIdentifiers)(nil)
	_ user.PasswordRepository         = (*fakePasswords)(nil)
	_ session.SessionRepository       = (*fakeSessions)(nil)
	_ passwordreset.Repository        = (*fakePasswordResets)(nil)
	_ ratelimiter.Limiter             = denyLimiter{}
)

// fakePasswordResets composes the challenge/password/session fakes into the atomic
// reset the store performs in one transaction (design §5.9). It is functional (the
// per-statement rollback proof lives in the storetest reference); an injectable err
// drives the infrastructure-failure path.
type fakePasswordResets struct {
	ch   *fakeChallenges
	pw   *fakePasswords
	sess *fakeSessions
	err  error
}

func (f *fakePasswordResets) Redeem(ctx context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	if f.err != nil {
		return passwordreset.RedeemResult{}, f.err
	}
	userID, ok := f.ch.takeLiveToken(in.Purpose, in.TokenDigest, in.Now)
	if !ok {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	if err := f.pw.Set(ctx, userID, in.NewPasswordHash); err != nil {
		return passwordreset.RedeemResult{}, err
	}
	if err := f.sess.DeleteByUser(ctx, userID); err != nil {
		return passwordreset.RedeemResult{}, err
	}
	f.ch.purgeUserPurposes(userID, in.PurgeChallengePurposes)
	return passwordreset.RedeemResult{UserID: userID}, nil
}

// --- fakes ---

// fakeHasher is a deterministic, reversible stand-in for a real password hasher:
// the "hash" is "hash:"+password, so VerifyPassword is an exact-match check.
type fakeHasher struct {
	hashErr error
}

func (h *fakeHasher) HashPassword(password string) (string, error) {
	if h.hashErr != nil {
		return "", h.hashErr
	}
	return "hash:" + password, nil
}

func (h *fakeHasher) VerifyPassword(hash, password string) error {
	if hash == "hash:"+password {
		return nil
	}
	return errors.New("mismatch")
}

// recordingMailer captures every message sent.
type recordingMailer struct {
	mu   sync.Mutex
	sent []email.Message
	err  error
}

func (m *recordingMailer) Send(_ context.Context, msg email.Message) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *recordingMailer) last() email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[len(m.sent)-1]
}

// codeFor returns the verification code from the most recent message addressed to
// normalizedEmail — so interleaved registrations resolve to the right secret.
func (m *recordingMailer) codeFor(t *testing.T, normalizedEmail string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		msg := m.sent[i]
		if len(msg.To) == 1 && msg.To[0] == normalizedEmail {
			return verificationCodeFromMail(t, msg)
		}
	}
	t.Fatalf("no verification mail for %s", normalizedEmail)
	return ""
}

type fakeUsers struct {
	mu   sync.Mutex
	byID map[string]user.User
	// idents is the sibling identifier store CreateWithPrimaryIdentifier persists
	// the primary into (the atomic user+identifier aggregate, design §2.2). The
	// harness links it after both fakes exist; nil when a test uses fakeUsers alone.
	idents *fakeIdentifiers
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byID: map[string]user.User{}} }

func (f *fakeUsers) Get(_ context.Context, id string) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byID[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ex, ok := f.byID[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	// Update never writes auth_revision (pgx UserStore.Update parity): the
	// identifier/credential rails own it via revision-CAS.
	u.AuthRevision = ex.AuthRevision
	f.byID[id] = u
	return u, nil
}

// applyRevision is the revision-CAS the fakeIdentifiers/ApplyVerifiedChange uses to
// serialize a mutation against the user's auth_revision (design §5.6). A stale
// expected revision is sdk.ErrConflict; a missing user is sdk.ErrNotFound.
func (f *fakeUsers) applyRevision(userID string, expected int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byID[userID]
	if !ok {
		return sdk.ErrNotFound
	}
	if u.AuthRevision != expected {
		return sdk.ErrConflict
	}
	u.AuthRevision++
	f.byID[userID] = u
	return nil
}

// CreateWithPrimaryIdentifier is the atomic v3 registration path (design §2.2): it
// commits the user and its first identifier together (or neither). It mirrors the
// store's lost-authentication-claim rule — a colliding active login/recovery value
// → sdk.ErrAlreadyExists with no orphan user — and persists the primary into the
// linked identifier store so a later GetLogin resolves it.
func (f *fakeUsers) CreateWithPrimaryIdentifier(_ context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	f.mu.Lock()
	if f.idents != nil && (ident.LoginEnabled || ident.RecoveryEnabled) && f.idents.authClaimTaken(ident) {
		f.mu.Unlock()
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	f.byID[u.ID] = u
	f.mu.Unlock()
	ident.UserID = u.ID
	if f.idents != nil {
		ident = f.idents.insert(ident)
	}
	return u, ident, nil
}

// fakeIdentifiers is the authsvc-test IdentifierRepository over fakeUsers: it
// mirrors the reference-memory ApplyVerifiedChange (revision-CAS on the owning
// user, retire-replaced, claim the verified row) so the register/verify flow can
// be exercised without the store module. applyErr injects the post-consume
// apply-conflict path.
type fakeIdentifiers struct {
	users         *fakeUsers
	mu            sync.Mutex
	byID          map[string]identifier.Identifier
	seq           int
	applyErr      error
	loginCalls    int // GetLogin calls, for rate-limit short-circuit ordering assertions
	recoveryCalls int // GetRecovery calls, for the no-lookup-before-admission proof
}

func newFakeIdentifiers(users *fakeUsers) *fakeIdentifiers {
	f := &fakeIdentifiers{users: users, byID: map[string]identifier.Identifier{}}
	// Link the atomic user+identifier aggregate (design §2.2) so registration's
	// CreateWithPrimaryIdentifier persists the primary where GetLogin resolves it.
	users.idents = f
	return f
}

// insert stores an identifier, minting an ID when the greenfield DB-generated
// convention left it empty (parity with the store assigning it inline).
func (f *fakeIdentifiers) insert(it identifier.Identifier) identifier.Identifier {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it.ID == "" {
		f.seq++
		it.ID = "id" + itoa(f.seq)
	}
	f.byID[it.ID] = it
	return it
}

// authClaimTaken reports whether an active login- or recovery-enabled row already
// claims (kind, normalized value) — the partial authentication-claim unique index
// (design §2.1) the store arbitrates at apply/insert time.
func (f *fakeIdentifiers) authClaimTaken(it identifier.Identifier) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.byID {
		if ex.Active() && (ex.LoginEnabled || ex.RecoveryEnabled) &&
			ex.Kind == it.Kind && ex.NormalizedValue == it.NormalizedValue {
			return true
		}
	}
	return false
}

// markAllVerified stamps VerifiedAt on every active identifier of userID —
// simulating a verification-state change between an OAuth pending-link start and
// its VerifyLink completion (the §5.7 TOCTOU the captured flag closes).
func (f *fakeIdentifiers) markAllVerified(userID string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, it := range f.byID {
		if it.UserID == userID && it.Active() {
			it.VerifiedAt = now.UTC()
			f.byID[id] = it
		}
	}
}

// retireAll retires every active identifier of userID — simulating the matched
// identifier row changing (replaced/removed) between a pending-link start and its
// completion; adoption must still complete against the captured user id.
func (f *fakeIdentifiers) retireAll(userID string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, it := range f.byID {
		if it.UserID == userID && it.Active() {
			it.Retire(now)
			f.byID[id] = it
		}
	}
}

func (f *fakeIdentifiers) Get(_ context.Context, id string) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.byID[id]
	if !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	return it, nil
}

func (f *fakeIdentifiers) GetLogin(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loginCalls++
	for _, it := range f.byID {
		if it.Active() && it.LoginEnabled && string(it.Kind) == kind && it.NormalizedValue == normalizedValue {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (f *fakeIdentifiers) GetRecovery(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recoveryCalls++
	for _, it := range f.byID {
		if it.Active() && it.RecoveryEnabled && string(it.Kind) == kind && it.NormalizedValue == normalizedValue {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (f *fakeIdentifiers) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []identifier.Identifier{}
	for _, it := range f.byID {
		if it.Active() && it.UserID == userID {
			out = append(out, it)
		}
	}
	return out, nil
}

func (f *fakeIdentifiers) ApplyVerifiedChange(_ context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return identifier.Identifier{}, f.applyErr
	}
	// Rows this apply retires are freed from the authentication-claim index (the
	// replaced identifier and, on a make-primary, the displaced primary).
	retiring := map[string]bool{}
	if input.ReplacesIdentifierID != "" {
		retiring[input.ReplacesIdentifierID] = true
	}
	if input.MakePrimary {
		for id, ex := range f.byID {
			if ex.Active() && ex.UserID == input.UserID && ex.Kind == input.Kind && ex.IsPrimary {
				retiring[id] = true
			}
		}
	}
	// Lost authentication-claim race (design §2.1): an active login/recovery row not
	// being retired already claims the new value — the partial unique index the store
	// arbitrates. Detected BEFORE the revision bumps, mirroring the store's rollback.
	if input.LoginEnabled || input.RecoveryEnabled {
		for id, ex := range f.byID {
			if retiring[id] {
				continue
			}
			if ex.Active() && (ex.LoginEnabled || ex.RecoveryEnabled) &&
				ex.Kind == input.Kind && ex.NormalizedValue == input.NormalizedValue {
				return identifier.Identifier{}, sdk.ErrAlreadyExists
			}
		}
	}
	if err := f.users.applyRevision(input.UserID, expectedAuthRevision); err != nil {
		return identifier.Identifier{}, err
	}
	now := verifiedAt.UTC()
	if input.ReplacesIdentifierID != "" {
		if ex, ok := f.byID[input.ReplacesIdentifierID]; ok {
			ex.Retire(now)
			f.byID[input.ReplacesIdentifierID] = ex
		}
	}
	// Retire any displaced active primary of the same (user, kind) — the store's
	// at-most-one-primary rule (design §2.1) — before claiming the new verified row.
	if input.MakePrimary {
		for id, ex := range f.byID {
			if ex.Active() && ex.UserID == input.UserID && ex.Kind == input.Kind && ex.IsPrimary {
				ex.Retire(now)
				f.byID[id] = ex
			}
		}
	}
	f.seq++
	id := "id" + itoa(f.seq)
	it := identifier.Identifier{
		ID:                  id,
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
	f.byID[id] = it
	return it, nil
}

type fakePasswords struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakePasswords() *fakePasswords { return &fakePasswords{m: map[string]string{}} }

func (f *fakePasswords) Set(_ context.Context, userID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[userID] = hash
	return nil
}

func (f *fakePasswords) Get(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.m[userID]
	if !ok {
		return "", sdk.ErrNotFound
	}
	return h, nil
}

// delete removes a user's password (the credential-mutation rail's RemovePassword
// seam; the PasswordRepository port itself exposes no delete).
func (f *fakePasswords) delete(userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, userID)
}

// fakeSessions keys by the app-minted ID and mirrors the reference store's
// refresh-rotation invariants (unique current hash, single previous slot + consumed
// flag, CAS Rotate/ConsumeGrace, empty-previous guard).
type fakeSessions struct {
	mu     sync.Mutex
	m      map[string]session.Session
	getErr error // non-nil → Get returns it (drives the fail-closed path)
}

func newFakeSessions() *fakeSessions { return &fakeSessions{m: map[string]session.Session{}} }

func (f *fakeSessions) Create(_ context.Context, s session.Session) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.m {
		if ex.RefreshTokenHash == s.RefreshTokenHash {
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	f.m[s.ID] = s
	return s, nil
}

func (f *fakeSessions) Get(_ context.Context, id string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return session.Session{}, f.getErr
	}
	s, ok := f.m[id]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if s.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return s, nil
}

func (f *fakeSessions) GetByRefreshHash(_ context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if hash == "" {
		return session.Session{}, 0, sdk.ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.m {
		if s.RefreshTokenHash == hash {
			return s, session.RefreshMatchCurrent, nil
		}
	}
	for _, s := range f.m {
		if s.PreviousRefreshTokenHash != "" && s.PreviousRefreshTokenHash == hash {
			return s, session.RefreshMatchPrevious, nil
		}
	}
	return session.Session{}, 0, sdk.ErrNotFound
}

func (f *fakeSessions) Rotate(_ context.Context, id, expectedCurrentHash, newHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[id]
	if !ok || s.RefreshTokenHash != expectedCurrentHash {
		return session.ErrRotationConflict
	}
	s.PreviousRefreshTokenHash = expectedCurrentHash
	s.PreviousUsed = false
	s.RotationCount++
	s.RefreshTokenHash = newHash
	f.m[id] = s
	return nil
}

func (f *fakeSessions) ConsumeGrace(_ context.Context, id, previousHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[id]
	if !ok || s.PreviousUsed || s.PreviousRefreshTokenHash == "" || s.PreviousRefreshTokenHash != previousHash {
		return session.ErrRotationConflict
	}
	s.PreviousUsed = true
	f.m[id] = s
	return nil
}

func (f *fakeSessions) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[id]; !ok {
		return sdk.ErrNotFound
	}
	delete(f.m, id)
	return nil
}

func (f *fakeSessions) DeleteByUser(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, s := range f.m {
		if s.UserID == userID {
			delete(f.m, id)
		}
	}
	return nil
}

// count returns the number of stored sessions (a revocation-assertion helper).
func (f *fakeSessions) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.m)
}

// denyLimiter always refuses — used to drive the rate-limit path.
type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: false}, nil
}
func (denyLimiter) Reset(context.Context, string) error { return nil }
func (denyLimiter) Close() error                        { return nil }

// --- harness ---

type harness struct {
	svc          *Service
	users        *fakeUsers
	idents       *fakeIdentifiers
	pw           *fakePasswords
	sess         *fakeSessions
	ch           *fakeChallenges
	prot         *fakeProtector
	pr           *fakePasswordResets
	hasher       *fakeHasher
	mailer       *recordingMailer
	events       *spySecurityEvents
	signer       *fakeSigner
	deliveryRepo *memDispatcher
	grants       *fakeAuthGrants
	creds        *fakeCredentials
	accounts     *fakeOAuthAccounts
	contacts     *fakeContactChanges
	phone        *recordingNotifier
}

func newHarness(t *testing.T, limiter ratelimiter.Limiter) *harness {
	return newHarnessDeps(t, limiter, false)
}

// newHarnessDeps builds a harness, optionally requiring verified email at login.
// The access-JWT signer is always wired now (it is required — D3): the mint path
// signs the access token, so every harness carries one.
func newHarnessDeps(t *testing.T, limiter ratelimiter.Limiter, requireVerifiedEmail bool) *harness {
	t.Helper()
	users := newFakeUsers()
	h := &harness{
		users:    users,
		idents:   newFakeIdentifiers(users),
		pw:       newFakePasswords(),
		sess:     newFakeSessions(),
		ch:       newFakeChallenges(),
		prot:     newFakeProtector("k1", "k1"),
		hasher:   &fakeHasher{},
		mailer:   &recordingMailer{},
		events:   newSpySecurityEvents(),
		signer:   newFakeSigner(),
		grants:   newFakeAuthGrants(),
		accounts: newFakeOAuthAccounts(),
		contacts: newFakeContactChanges(),
	}
	h.pr = &fakePasswordResets{ch: h.ch, pw: h.pw, sess: h.sess}
	h.creds = &fakeCredentials{users: users, pw: h.pw, idents: h.idents, accounts: h.accounts}
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	h.svc = NewService(Deps{
		Users:                h.users,
		Identifiers:          h.idents,
		Passwords:            h.pw,
		Sessions:             h.sess,
		Challenges:           h.ch,
		Protector:            h.prot,
		PasswordResets:       h.pr,
		ContactChanges:       h.contacts,
		AuthenticationGrants: h.grants,
		CredentialMutations:  h.creds,
		OAuthAccounts:        h.accounts,
		Hasher:               h.hasher,
		Limiter:              limiter,
		Cookie:               CookieConfig{},
		RequireVerifiedEmail: requireVerifiedEmail,
		SecurityEvents:       h.events,
		TokenSigner:          h.signer,
	})
	// Wire the durable delivery outbox (design §6.1.1) with a synchronous drain so
	// the send sites (register verification, forgot-password) enqueue and the worker
	// delivers to the recording mailer within the call, keeping the mail-assertion
	// tests direct.
	h.wireDelivery(t)
	return h
}

// mustRegister registers a user and returns it.
func (h *harness) mustRegister(t *testing.T, email, password string) user.User {
	t.Helper()
	u, err := h.svc.Register(context.Background(), email, password, "Test User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return u
}

// --- tests ---

// verificationCodeFromMail recovers the plaintext code the challenge rail minted
// from the delivered message (the digest at rest is not reversible).
func verificationCodeFromMail(t *testing.T, msg email.Message) string {
	t.Helper()
	const prefix = "Your verification code is: "
	i := strings.Index(msg.Text, prefix)
	if i < 0 {
		t.Fatalf("verification code not found in mail text %q", msg.Text)
	}
	// The router renders a multi-paragraph body; the code is the first whitespace-
	// delimited token after the prefix.
	fields := strings.Fields(msg.Text[i+len(prefix):])
	if len(fields) == 0 {
		t.Fatalf("verification code missing after prefix in %q", msg.Text)
	}
	return fields[0]
}

func TestRegister(t *testing.T) {
	h := newHarness(t, nil)
	u, err := h.svc.Register(context.Background(), "New@Example.com", "password123456789", "New User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Identity lives in user_identifiers now: the primary email is normalized and
	// login-enabled on the identifier, not on the user.
	it, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "new@example.com")
	if err != nil || it.UserID != u.ID {
		t.Errorf("primary identifier not created/normalized: %+v err=%v", it, err)
	}
	if _, err := h.pw.Get(context.Background(), u.ID); err != nil {
		t.Errorf("password not stored: %v", err)
	}
	// A verify_registration challenge is issued on the atomic rail.
	if h.ch.countRows() != 1 {
		t.Errorf("challenge count = %d, want 1", h.ch.countRows())
	}
	if h.mailer.count() != 1 {
		t.Errorf("mail count = %d, want 1", h.mailer.count())
	}
	if to := h.mailer.last().To; len(to) != 1 || to[0] != "new@example.com" {
		t.Errorf("mail To = %v", to)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "dup@example.com", "password123456789")
	_, err := h.svc.Register(context.Background(), "DUP@example.com", "password456789012", "Other")
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("duplicate register: err=%v, want ErrAlreadyExists", err)
	}
}

func TestRegisterShortPassword(t *testing.T) {
	h := newHarness(t, nil)
	_, err := h.svc.Register(context.Background(), "short@example.com", "short", "X")
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("short password: err=%v, want ErrInvalidInput", err)
	}
	if h.mailer.count() != 0 {
		t.Errorf("mail sent for invalid registration")
	}
}

func TestVerify(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "verify@example.com", "password123456789")
	code := verificationCodeFromMail(t, h.mailer.last())
	if err := h.svc.Verify(context.Background(), "Verify@example.com", code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The primary email identifier is claimed and verified via revision-CAS.
	it, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "verify@example.com")
	if err != nil {
		t.Fatalf("verified identifier not claimed: %v", err)
	}
	if !it.Verified() || !it.IsPrimary || !it.LoginEnabled || !it.RecoveryEnabled {
		t.Errorf("identifier uses/verification = %+v, want verified primary login+recovery", it)
	}
	u, err := h.users.Get(context.Background(), it.UserID)
	if err != nil {
		t.Fatalf("Get user: %v", err)
	}
	if u.AuthRevision != 1 {
		t.Errorf("auth_revision = %d, want 1 (one applied mutation)", u.AuthRevision)
	}
	if h.ch.countRows() != 0 {
		t.Error("verify_registration challenge not consumed")
	}
}

func TestVerifyUnknownAccount(t *testing.T) {
	h := newHarness(t, nil)
	// No such account is indistinguishable from a wrong code (enumeration safety).
	if err := h.svc.Verify(context.Background(), "ghost@example.com", "123456"); !errors.Is(err, ErrChallengeInvalid) {
		t.Errorf("Verify(unknown account): err=%v, want ErrChallengeInvalid", err)
	}
}

func TestVerifyWrongCodeLocksOut(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lock@example.com", "password123456789")
	// A guaranteed-wrong code (the real code with its first digit rotated) so the
	// six-digit space can never make this test flaky.
	real := verificationCodeFromMail(t, h.mailer.last())
	wrong := string('0'+(real[0]-'0'+1)%10) + real[1:]
	// Spend the attempt budget with wrong codes; the last locks out and deletes.
	var lastErr error
	for i := 0; i < challenge.MaxAttempts; i++ {
		lastErr = h.svc.Verify(context.Background(), "lock@example.com", wrong)
	}
	if !errors.Is(lastErr, ErrTooManyAttempts) {
		t.Fatalf("final wrong attempt: err=%v, want ErrTooManyAttempts", lastErr)
	}
	if h.ch.countRows() != 0 {
		t.Error("locked-out challenge row not deleted")
	}
	it, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "lock@example.com")
	if err != nil {
		t.Fatalf("registration identifier missing: %v", err)
	}
	if it.Verified() {
		t.Error("account verified despite lockout")
	}
}

// TestVerifyApplyConflictIsRestartable proves the post-consume apply-conflict path
// (design §5.6): the code is spent but the revision-CAS lost, so Verify returns the
// restartable conflict and no partial state (unverified account) is written.
func TestVerifyApplyConflictIsRestartable(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "conflict@example.com", "password123456789")
	code := verificationCodeFromMail(t, h.mailer.last())
	h.idents.applyErr = sdk.ErrConflict
	err := h.svc.Verify(context.Background(), "conflict@example.com", code)
	if !errors.Is(err, ErrRegistrationVerificationConflict) {
		t.Fatalf("apply conflict: err=%v, want ErrRegistrationVerificationConflict", err)
	}
	if !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("conflict does not wrap sdk.ErrConflict: %v", err)
	}
	if h.ch.countRows() != 0 {
		t.Error("code should be consumed even on apply conflict")
	}
	it, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "conflict@example.com")
	if err != nil {
		t.Fatalf("registration identifier missing: %v", err)
	}
	if it.Verified() {
		t.Error("account verified despite apply conflict — partial state written")
	}
}

func TestLogin(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "login@example.com", "password123456789")
	pair, gotU, err := h.svc.Login(context.Background(), "Login@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || gotU.ID != u.ID {
		t.Errorf("bad login: pair=%+v user=%+v", pair, gotU)
	}
	if !isJWTToken(pair.AccessToken) {
		t.Errorf("access token is not JWT-shaped: %q", pair.AccessToken)
	}
	// The access JWT resolves to the same user identity.
	if id, ok := h.svc.verifyBearer(pair.AccessToken); !ok || id != u.ID {
		t.Errorf("verifyBearer(access) = (%q, %v), want (%q, true)", id, ok, u.ID)
	}
	if h.sess.count() != 1 {
		t.Errorf("session count = %d, want 1", h.sess.count())
	}
}

// TestLoginResolvesAnyLoginEnabledIdentifier proves password login resolves
// identity through GetLogin (design §2.2): a user with multiple login-enabled email
// identifiers logs in with either, resolving to the same subject.
func TestLoginResolvesAnyLoginEnabledIdentifier(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "primary@example.com", "password123456789")
	// A second verified login-enabled email on the same user. The identifier-add
	// flow lands in a later phase, so the row is seeded directly.
	now := time.Now()
	h.idents.byID["alias"] = identifier.Identifier{
		ID: "alias", UserID: u.ID, Kind: identifier.KindEmail, NormalizedValue: "alias@example.com",
		VerifiedAt: now, LoginEnabled: true, CreatedAt: now,
	}
	for _, addr := range []string{"Primary@example.com", "Alias@example.com"} {
		_, gotU, err := h.svc.Login(context.Background(), addr, "password123456789")
		if err != nil {
			t.Fatalf("Login(%s): %v", addr, err)
		}
		if gotU.ID != u.ID {
			t.Errorf("Login(%s) resolved user %q, want %q", addr, gotU.ID, u.ID)
		}
	}
}

// TestNotificationOnlyPhoneSharedNotLoginClaim proves a verified notification-only
// phone may be held by multiple users (no login/recovery claim, design §2.1) and is
// never returned by the login-resolution path GetLogin uses.
func TestNotificationOnlyPhoneSharedNotLoginClaim(t *testing.T) {
	h := newHarness(t, nil)
	now := time.Now()
	for i, uid := range []string{"userA", "userB"} {
		h.users.byID[uid] = user.User{ID: uid}
		h.idents.byID["p"+itoa(i)] = identifier.Identifier{
			ID: "p" + itoa(i), UserID: uid, Kind: identifier.KindPhone, NormalizedValue: "+15551112222",
			VerifiedAt: now, NotificationEnabled: true, CreatedAt: now,
		}
	}
	// A shared notification-only value identifies no login subject.
	if _, err := h.idents.GetLogin(context.Background(), string(identifier.KindPhone), "+15551112222"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(notification-only phone) = %v, want ErrNotFound", err)
	}
	// Each holder still projects it as its own contact address.
	for _, uid := range []string{"userA", "userB"} {
		info, err := h.svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: uid})
		if err != nil {
			t.Fatalf("Resolve(%s): %v", uid, err)
		}
		if len(info.Addresses) != 1 || info.Addresses[0].Kind != identity.KindPhone {
			t.Errorf("Resolve(%s) Addresses = %+v, want the shared phone", uid, info.Addresses)
		}
	}
}

// TestRegisterEmailShapedSignatureResolvesIdentifier proves V9: the public register/
// login signatures stay email-shaped, and the authoritative identity is the
// normalized login-enabled identifier the atomic create persists — not a user column.
func TestRegisterEmailShapedSignatureResolvesIdentifier(t *testing.T) {
	h := newHarness(t, nil)
	u, err := h.svc.Register(context.Background(), "Compat@Example.com", "password123456789", "Compat User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	it, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "compat@example.com")
	if err != nil {
		t.Fatalf("registration identifier not created: %v", err)
	}
	if it.UserID != u.ID || !it.LoginEnabled || !it.IsPrimary || it.Verified() {
		t.Errorf("registration identifier = %+v, want unverified primary login for %q", it, u.ID)
	}
	_, gotU, err := h.svc.Login(context.Background(), "compat@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if gotU.ID != u.ID {
		t.Errorf("Login resolved user %q, want %q", gotU.ID, u.ID)
	}
}

// TestRefreshTokenHashedAtRest proves the service-side hashing (design §7.3): the
// raw refresh token Login returns is NOT what the store holds — the row keeps only
// its SHA-256 hash — and Refresh resolves the raw token back to a rotation.
func TestRefreshTokenHashedAtRest(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "hash@example.com", "password123456789")
	pair, _, err := h.svc.Login(context.Background(), "hash@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	hashed, err := h.svc.hashSessionToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("hashSessionToken: %v", err)
	}
	if hashed == pair.RefreshToken {
		t.Fatal("stored refresh hash equals the raw token — not hashed")
	}
	found := false
	h.sess.mu.Lock()
	for _, s := range h.sess.m {
		if s.RefreshTokenHash == pair.RefreshToken {
			t.Error("store holds the raw refresh token")
		}
		if s.RefreshTokenHash == hashed {
			found = true
		}
	}
	h.sess.mu.Unlock()
	if !found {
		t.Error("store does not hold the refresh token's hash")
	}
	// The raw refresh token rotates successfully (resolves via its hash).
	next, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil || next.AccessToken == "" || next.RefreshToken == "" {
		t.Errorf("Refresh(raw): pair=%+v err=%v", next, err)
	}
}

func TestLoginRequireVerifiedEmailBlocksUnverified(t *testing.T) {
	h := newHarnessDeps(t, nil, true)
	h.mustRegister(t, "unv@example.com", "password123456789") // unverified by default
	_, _, err := h.svc.Login(context.Background(), "unv@example.com", "password123456789")
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Errorf("unverified login: err=%v, want ErrEmailNotVerified", err)
	}
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Errorf("ErrEmailNotVerified must wrap ErrForbidden (403 mapping): %v", err)
	}
}

func TestLoginRequireVerifiedEmailAllowsVerified(t *testing.T) {
	h := newHarnessDeps(t, nil, true)
	h.mustRegister(t, "ver@example.com", "password123456789")
	code := verificationCodeFromMail(t, h.mailer.last())
	if err := h.svc.Verify(context.Background(), "ver@example.com", code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, _, err := h.svc.Login(context.Background(), "ver@example.com", "password123456789"); err != nil {
		t.Errorf("verified login: %v", err)
	}
}

func TestLoginVerifiedGateOffByDefault(t *testing.T) {
	h := newHarness(t, nil) // RequireVerifiedEmail defaults false
	h.mustRegister(t, "off@example.com", "password123456789")
	if _, _, err := h.svc.Login(context.Background(), "off@example.com", "password123456789"); err != nil {
		t.Errorf("gate-off unverified login: %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "wp@example.com", "password123456789")
	_, _, err := h.svc.Login(context.Background(), "wp@example.com", "wrongpassword")
	if !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("wrong password: err=%v, want ErrUnauthorized", err)
	}
}

func TestLoginUnknownEmail(t *testing.T) {
	h := newHarness(t, nil)
	_, _, err := h.svc.Login(context.Background(), "ghost@example.com", "password123456789")
	if !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("unknown email: err=%v, want ErrUnauthorized", err)
	}
}

func TestLoginRateLimitedFirst(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	h.mustRegister(t, "rl@example.com", "password123456789")
	before := h.idents.loginCalls
	_, _, err := h.svc.Login(context.Background(), "rl@example.com", "password123456789")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limited: err=%v, want ErrRateLimited", err)
	}
	if h.idents.loginCalls != before {
		t.Error("rate limit did not short-circuit before resolving the login identifier")
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lo@example.com", "password123456789")
	pair, _, _ := h.svc.Login(context.Background(), "lo@example.com", "password123456789")
	if h.sess.count() != 1 {
		t.Fatalf("session count before logout = %d, want 1", h.sess.count())
	}
	// Primary lane: the refresh token resolves the session id and deletes it.
	if err := h.svc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if h.sess.count() != 0 {
		t.Errorf("session not deleted: count=%d, want 0", h.sess.count())
	}
	// Idempotent: logging out an already-gone session is not an error.
	if err := h.svc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken); err != nil {
		t.Errorf("second Logout: %v", err)
	}
}

// TestLogoutFallbackByAccessToken proves the fallback lane (§1.5): with no refresh
// token, an access JWT surrenders its session_id (even ignoring expiry) so logout
// still revokes the session — never a no-op.
func TestLogoutFallbackByAccessToken(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lf@example.com", "password123456789")
	pair, _, _ := h.svc.Login(context.Background(), "lf@example.com", "password123456789")
	if err := h.svc.Logout(context.Background(), "", pair.AccessToken); err != nil {
		t.Fatalf("Logout(access-only): %v", err)
	}
	if h.sess.count() != 0 {
		t.Errorf("session not deleted via access-token fallback: count=%d, want 0", h.sess.count())
	}
}

// TestChangePassword covers the amended §7.2 policy: the new password works, ALL
// pre-existing sessions are revoked, a fresh session is minted for the caller,
// and the old password no longer authenticates.
func TestChangePassword(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "cp@example.com", "password123456789")
	pairA, _, err := h.svc.Login(context.Background(), "cp@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login A: %v", err)
	}
	pairB, _, err := h.svc.Login(context.Background(), "cp@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login B: %v", err)
	}

	newPair, err := h.svc.ChangePassword(context.Background(), u.ID, "password123456789", "newpassword456789")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Every pre-existing session is revoked: their refresh tokens no longer refresh.
	for name, p := range map[string]TokenPair{"A": pairA, "B": pairB} {
		if _, err := h.svc.Refresh(context.Background(), p.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
			t.Errorf("session %s survived password change: err=%v, want ErrInvalidRefreshToken", name, err)
		}
	}
	// A fresh session is minted for the caller and it works.
	if newPair.AccessToken == "" || newPair.RefreshToken == "" {
		t.Fatal("ChangePassword returned an empty pair")
	}
	if h.sess.count() != 1 {
		t.Errorf("session count after change = %d, want 1 (only the fresh one)", h.sess.count())
	}
	if _, err := h.svc.Refresh(context.Background(), newPair.RefreshToken); err != nil {
		t.Errorf("fresh session not refreshable: %v", err)
	}
	// New password works; old password no longer does.
	if _, _, err := h.svc.Login(context.Background(), "cp@example.com", "newpassword456789"); err != nil {
		t.Errorf("login with new password: %v", err)
	}
	if _, _, err := h.svc.Login(context.Background(), "cp@example.com", "password123456789"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("login with old password: err=%v, want ErrUnauthorized", err)
	}
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "cpw@example.com", "password123456789")
	_, err := h.svc.ChangePassword(context.Background(), u.ID, "wrongcurrent", "newpassword456789")
	if !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("wrong current: err=%v, want ErrUnauthorized", err)
	}
}

// resetTokenFromMail recovers the plaintext reset token the challenge rail minted
// from the delivered message (the digest at rest is not reversible).
func resetTokenFromMail(t *testing.T, msg email.Message) string {
	t.Helper()
	const prefix = "Use this token to reset your password: "
	i := strings.Index(msg.Text, prefix)
	if i < 0 {
		t.Fatalf("reset token not found in mail text %q", msg.Text)
	}
	fields := strings.Fields(msg.Text[i+len(prefix):])
	if len(fields) == 0 {
		t.Fatalf("reset token missing after prefix in %q", msg.Text)
	}
	return fields[0]
}

func TestForgotPasswordUnknownEmailNoReveal(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.svc.ForgotPassword(context.Background(), "ghost@example.com"); err != nil {
		t.Fatalf("ForgotPassword(unknown): %v", err)
	}
	if h.ch.countRows() != 0 {
		t.Error("reset challenge issued for unknown email")
	}
	if h.mailer.count() != 0 {
		t.Error("mail sent for unknown email (enumeration leak)")
	}
}

// TestForgotPasswordUnverifiedNoReveal proves recovery requires a PROVEN address
// (design §2.3): a registered-but-unverified account has no verified recovery
// identifier yet, so forgot-password stays silent — no challenge, no mail.
func TestForgotPasswordUnverifiedNoReveal(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "unverified@example.com", "password123456789") // not verified
	h.mailer.sent = nil
	// Register leaves one verify_registration challenge; a silent forgot must add
	// no password_reset row and send no mail.
	before := h.ch.countRows()
	if err := h.svc.ForgotPassword(context.Background(), "unverified@example.com"); err != nil {
		t.Fatalf("ForgotPassword(unverified): %v", err)
	}
	if h.ch.countRows() != before {
		t.Error("reset challenge issued for an unverified recovery identifier")
	}
	if h.mailer.count() != 0 {
		t.Error("reset mail sent for an unverified recovery identifier")
	}
}

func TestForgotAndResetPassword(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "fr@example.com", "password123456789")
	h.mustVerify(t, "fr@example.com") // claims the verified recovery identifier
	// A live session the reset must revoke (the reset never logs the caller in).
	pair, _, err := h.svc.Login(context.Background(), "fr@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if h.sess.count() != 1 {
		t.Fatalf("session count before reset = %d, want 1", h.sess.count())
	}
	h.mailer.sent = nil // drop the verification mail

	if err := h.svc.ForgotPassword(context.Background(), "fr@example.com"); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	if h.mailer.count() != 1 {
		t.Fatalf("reset mail count = %d, want 1", h.mailer.count())
	}
	token := resetTokenFromMail(t, h.mailer.last())

	if err := h.svc.ResetPassword(context.Background(), token, "brandnewpass1234"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if h.ch.countRows() != 0 {
		t.Errorf("reset challenge not consumed: %d rows", h.ch.countRows())
	}
	// The reset revoked every session and minted none.
	if h.sess.count() != 0 {
		t.Errorf("session count after reset = %d, want 0 (all revoked, none minted)", h.sess.count())
	}
	if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); err == nil {
		t.Error("pre-reset refresh token still valid after reset")
	}
	if _, _, err := h.svc.Login(context.Background(), "fr@example.com", "brandnewpass1234"); err != nil {
		t.Errorf("login after reset: %v", err)
	}
}

func TestResetPasswordUnknownToken(t *testing.T) {
	h := newHarness(t, nil)
	err := h.svc.ResetPassword(context.Background(), "no-such-token", "brandnewpass1234")
	if !errors.Is(err, ErrPasswordResetInvalid) {
		t.Errorf("unknown token: err=%v, want ErrPasswordResetInvalid", err)
	}
	// The generic failure maps to 404 (wraps sdk.ErrNotFound), preserving the
	// external reset contract and enumeration uniformity.
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("ErrPasswordResetInvalid does not wrap sdk.ErrNotFound: %v", err)
	}
}

func TestResetPasswordExpiredToken(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "rex@example.com", "password123456789")
	// Seed an already-expired password_reset token challenge directly on the rail.
	token := "expired-reset-token"
	now := time.Now()
	if _, err := h.ch.Replace(context.Background(), challenge.Challenge{
		UserID:       u.ID,
		Purpose:      challenge.PurposePasswordReset,
		SecretDigest: h.prot.DigestToken(token),
		ExpiresAt:    now.Add(-time.Hour),
		CreatedAt:    now.Add(-2 * time.Hour),
		Version:      1,
	}); err != nil {
		t.Fatalf("seed expired challenge: %v", err)
	}
	err := h.svc.ResetPassword(context.Background(), token, "brandnewpass1234")
	if !errors.Is(err, ErrPasswordResetInvalid) {
		t.Errorf("expired token: err=%v, want ErrPasswordResetInvalid", err)
	}
}

func TestRequireUserValidSession(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "ru@example.com", "password123456789")
	pair, u, _ := h.svc.Login(context.Background(), "ru@example.com", "password123456789")

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.svc.CurrentUser(r.Context())
		if !ok {
			t.Error("CurrentUser not set inside RequireUser")
		}
		gotUserID = id
		w.WriteHeader(http.StatusNoContent)
	})

	// The browser session cookie carries the access JWT (verified statelessly).
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if gotUserID != u.ID {
		t.Errorf("CurrentUser = %q, want %q", gotUserID, u.ID)
	}
}

func TestRequireUserNoCookie(t *testing.T) {
	h := newHarness(t, nil)
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("next handler called without a session")
	}
}

func TestRequireUserExpiredSession(t *testing.T) {
	h := newHarness(t, nil)
	// An expired access JWT in the session cookie is rejected statelessly (§1.2).
	expired, err := h.signer.Sign(map[string]any{tokenClaimUserID: "u1"}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign(expired): %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: expired})
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCurrentUserAbsent(t *testing.T) {
	h := newHarness(t, nil)
	if _, ok := h.svc.CurrentUser(context.Background()); ok {
		t.Error("CurrentUser reported ok on a bare context")
	}
}
