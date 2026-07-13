package authentication

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
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// var _ pins the seam: *authsvc.Service is the concrete authService the router
// mounts.
var _ authService = (*authsvc.Service)(nil)

// --- minimal fakes (transport tests wire a real authsvc.Service over these) ---

type fakeHasher struct{}

func (fakeHasher) HashPassword(pw string) (string, error) { return "hash:" + pw, nil }
func (fakeHasher) VerifyPassword(hash, pw string) error {
	if hash == "hash:"+pw {
		return nil
	}
	return errors.New("mismatch")
}

type nopMailer struct{}

func (nopMailer) Send(context.Context, email.Message) error { return nil }

// stubQueue satisfies the delivery outbox seam for the transport tests: enqueue
// always succeeds (the routes assert status codes, not delivery), and a status read
// is unknown. It keeps the real authsvc.Service's send sites off the fail-closed
// ErrDeliveryDisabled path without standing up a worker.
type stubQueue struct{}

func (stubQueue) Enqueue(_ context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	return delivery.Receipt{Key: cmd.IdempotencyKey, State: "pending"}, nil
}
func (stubQueue) Replace(_ context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	return delivery.Receipt{Key: cmd.IdempotencyKey, State: "pending"}, nil
}
func (stubQueue) Status(_ context.Context, _ string) (delivery.Status, error) {
	return delivery.Status{}, sdk.ErrNotFound
}

type memUsers struct {
	mu   sync.Mutex
	byID map[string]user.User
	// idents is the linked identifier store the atomic CreateWithPrimaryIdentifier
	// persists the primary into (design §2.2), so a register→login transport test
	// resolves identity through GetLogin. newMemIdentifiers links it.
	idents *memIdentifiers
}

// newMemUsers builds an empty user store; pair it with newMemIdentifiers to wire
// the atomic user+identifier aggregate the v3 register/login path needs.
func newMemUsers() *memUsers { return &memUsers{byID: map[string]user.User{}} }

func (m *memUsers) Get(_ context.Context, id string) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}
func (m *memUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ex, ok := m.byID[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	// Update never writes auth_revision (pgx UserStore.Update parity).
	u.AuthRevision = ex.AuthRevision
	m.byID[id] = u
	return u, nil
}

func (m *memUsers) applyRevision(userID string, expected int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[userID]
	if !ok {
		return sdk.ErrNotFound
	}
	if u.AuthRevision != expected {
		return sdk.ErrConflict
	}
	u.AuthRevision++
	m.byID[userID] = u
	return nil
}

// CreateWithPrimaryIdentifier is the atomic v3 registration path (design §2.2): it
// commits the user and its first identifier together and persists the primary into
// the linked identifier store so a later GetLogin resolves it (register→login).
func (m *memUsers) CreateWithPrimaryIdentifier(_ context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	ident.UserID = u.ID
	if m.idents != nil {
		if _, taken := m.idents.activeAuthClaim(ident); taken {
			return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}
	m.mu.Lock()
	m.byID[u.ID] = u
	m.mu.Unlock()
	if m.idents != nil {
		ident = m.idents.insert(ident)
	}
	return u, ident, nil
}

type memPasswords struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *memPasswords) Set(_ context.Context, id, h string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[id] = h
	return nil
}
func (p *memPasswords) Get(_ context.Context, id string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.m[id]
	if !ok {
		return "", sdk.ErrNotFound
	}
	return h, nil
}

// memSessions keys by the app-minted ID and mirrors the reference store's
// refresh-rotation invariants.
type memSessions struct {
	mu sync.Mutex
	m  map[string]session.Session
}

func (s *memSessions) Create(_ context.Context, sess session.Session) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.m {
		if ex.RefreshTokenHash == sess.RefreshTokenHash {
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	s.m[sess.ID] = sess
	return sess, nil
}
func (s *memSessions) Get(_ context.Context, id string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	if sess.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return sess, nil
}
func (s *memSessions) GetByRefreshHash(_ context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if hash == "" {
		return session.Session{}, 0, sdk.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.m {
		if sess.RefreshTokenHash == hash {
			return sess, session.RefreshMatchCurrent, nil
		}
	}
	for _, sess := range s.m {
		if sess.PreviousRefreshTokenHash != "" && sess.PreviousRefreshTokenHash == hash {
			return sess, session.RefreshMatchPrevious, nil
		}
	}
	return session.Session{}, 0, sdk.ErrNotFound
}
func (s *memSessions) Rotate(_ context.Context, id, expectedCurrentHash, newHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok || sess.RefreshTokenHash != expectedCurrentHash {
		return session.ErrRotationConflict
	}
	sess.PreviousRefreshTokenHash = expectedCurrentHash
	sess.PreviousUsed = false
	sess.RotationCount++
	sess.RefreshTokenHash = newHash
	s.m[id] = sess
	return nil
}
func (s *memSessions) ConsumeGrace(_ context.Context, id, previousHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok || sess.PreviousUsed || sess.PreviousRefreshTokenHash == "" || sess.PreviousRefreshTokenHash != previousHash {
		return session.ErrRotationConflict
	}
	sess.PreviousUsed = true
	s.m[id] = sess
	return nil
}
func (s *memSessions) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return sdk.ErrNotFound
	}
	delete(s.m, id)
	return nil
}
func (s *memSessions) DeleteByUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.m {
		if sess.UserID == userID {
			delete(s.m, id)
		}
	}
	return nil
}

// memProtector is a deterministic challenge protector for the transport tests: its
// "HMAC" folds key+user+purpose+code into a distinct string (a single active key).
type memProtector struct{}

func (memProtector) ActiveKeyID() string { return "k1" }
func (memProtector) DigestCode(keyID, userID, purpose, code string) (string, error) {
	if code == "" {
		return "", errors.New("empty code")
	}
	return "hmac|" + keyID + "|" + userID + "|" + purpose + "|" + code, nil
}
func (memProtector) CandidateCodeDigests(userID, purpose, code string) ([]challenge.DigestCandidate, error) {
	if code == "" {
		return nil, errors.New("empty code")
	}
	d, _ := memProtector{}.DigestCode("k1", userID, purpose, code)
	return []challenge.DigestCandidate{{KeyID: "k1", Digest: d}}, nil
}
func (memProtector) DigestToken(token string) string { return "sha|" + token }

// memChallenges mirrors the reference challenge store: one active row per
// (user, purpose), atomic consume with expiry/digest/attempt/lockout decisions.
type memChallenges struct {
	mu   sync.Mutex
	byID map[string]challenge.Challenge
	seq  int
}

func (f *memChallenges) Replace(_ context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, ex := range f.byID {
		if ex.UserID == c.UserID && ex.Purpose == c.Purpose {
			delete(f.byID, id)
		}
	}
	if c.ID == "" {
		f.seq++
		c.ID = "ch" + itoa(f.seq)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	f.byID[c.ID] = c
	return c, nil
}

func (f *memChallenges) ConsumeCode(_ context.Context, userID, purpose string, candidates []challenge.DigestCandidate, expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var (
		id  string
		row challenge.Challenge
	)
	found := false
	for cid, ex := range f.byID {
		if ex.UserID == userID && ex.Purpose == purpose {
			id, row, found = cid, ex, true
			break
		}
	}
	if !found {
		return challenge.Consumed{}, challenge.OutcomeNotFound, nil
	}
	if row.Expired(now) {
		delete(f.byID, id)
		return challenge.Consumed{}, challenge.OutcomeExpired, nil
	}
	matched := false
	for _, cand := range candidates {
		if cand.KeyID == row.ProtectorKeyID && cand.Digest != "" && cand.Digest == row.SecretDigest {
			matched = true
			break
		}
	}
	if !matched {
		row.AttemptCount++
		if row.AttemptCount >= maxAttempts {
			delete(f.byID, id)
			return challenge.Consumed{}, challenge.OutcomeLockedOut, nil
		}
		f.byID[id] = row
		return challenge.Consumed{}, challenge.OutcomeRejected, nil
	}
	delete(f.byID, id)
	c := challenge.Consumed{ID: row.ID, UserID: row.UserID, Purpose: row.Purpose, Context: row.Context, ProtectorKeyID: row.ProtectorKeyID, ConsumedAt: now.UTC()}
	if expectedContextDigest != "" && string(row.Context) != expectedContextDigest {
		return c, challenge.OutcomeContextMismatch, nil
	}
	return c, challenge.OutcomeRedeemed, nil
}

func (f *memChallenges) ConsumeToken(_ context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if presentedDigest == "" {
		return challenge.Consumed{}, sdk.ErrNotFound
	}
	for id, ex := range f.byID {
		if ex.Purpose == purpose && ex.SecretDigest == presentedDigest {
			delete(f.byID, id)
			if ex.Expired(now) {
				return challenge.Consumed{}, sdk.ErrExpired
			}
			return challenge.Consumed{ID: ex.ID, UserID: ex.UserID, Purpose: ex.Purpose, Context: ex.Context, ProtectorKeyID: ex.ProtectorKeyID, ConsumedAt: now.UTC()}, nil
		}
	}
	return challenge.Consumed{}, sdk.ErrNotFound
}

func (f *memChallenges) PurgeExpired(context.Context, time.Time, int) (int, error) { return 0, nil }

// takeLiveToken guards-and-deletes the live (purpose, digest) token — the reset
// composition's step 1. Unknown/expired/used are not live.
func (f *memChallenges) takeLiveToken(purpose, digest string, now time.Time) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if digest == "" {
		return "", false
	}
	for id, ex := range f.byID {
		if ex.Purpose == purpose && ex.SecretDigest == digest {
			if ex.Expired(now) {
				return "", false
			}
			delete(f.byID, id)
			return ex.UserID, true
		}
	}
	return "", false
}

func (f *memChallenges) purgeUserPurposes(userID string, purposes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, ex := range f.byID {
		if ex.UserID != userID {
			continue
		}
		for _, p := range purposes {
			if ex.Purpose == p {
				delete(f.byID, id)
				break
			}
		}
	}
}

// memPasswordResets composes the transport-test challenge/password/session mems
// into the atomic reset (design §5.9): the store performs it in one transaction;
// this functional stand-in performs it across the three mems.
type memPasswordResets struct {
	ch   *memChallenges
	pw   *memPasswords
	sess *memSessions
}

func (f *memPasswordResets) Redeem(ctx context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
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

// memIdentifiers is the transport-test IdentifierRepository over memUsers.
type memIdentifiers struct {
	users *memUsers
	mu    sync.Mutex
	byID  map[string]identifier.Identifier
	seq   int
}

// newMemIdentifiers builds an identifier store linked to users so the atomic
// register path persists its primary where GetLogin resolves it (design §2.2).
func newMemIdentifiers(users *memUsers) *memIdentifiers {
	f := &memIdentifiers{users: users, byID: map[string]identifier.Identifier{}}
	users.idents = f
	return f
}

// insert stores an identifier, minting an ID when the greenfield DB-generated
// convention left it empty (parity with the store assigning it inline).
func (f *memIdentifiers) insert(it identifier.Identifier) identifier.Identifier {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it.ID == "" {
		f.seq++
		it.ID = "id" + itoa(f.seq)
	}
	f.byID[it.ID] = it
	return it
}

// activeAuthClaim reports the active login/recovery identifier claiming ident's
// (kind, value), so the atomic create rejects a lost authentication claim
// (sdk.ErrAlreadyExists) the way the partial unique index does.
func (f *memIdentifiers) activeAuthClaim(ident identifier.Identifier) (identifier.Identifier, bool) {
	if !ident.LoginEnabled && !ident.RecoveryEnabled {
		return identifier.Identifier{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.byID {
		if it.Active() && (it.LoginEnabled || it.RecoveryEnabled) && it.Kind == ident.Kind && it.NormalizedValue == ident.NormalizedValue {
			return it, true
		}
	}
	return identifier.Identifier{}, false
}

func (f *memIdentifiers) Get(_ context.Context, id string) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.byID[id]
	if !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	return it, nil
}
func (f *memIdentifiers) GetLogin(_ context.Context, kind, v string) (identifier.Identifier, error) {
	return f.find(kind, v, func(it identifier.Identifier) bool { return it.LoginEnabled })
}
func (f *memIdentifiers) GetRecovery(_ context.Context, kind, v string) (identifier.Identifier, error) {
	return f.find(kind, v, func(it identifier.Identifier) bool { return it.RecoveryEnabled })
}
func (f *memIdentifiers) find(kind, v string, use func(identifier.Identifier) bool) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.byID {
		if it.Active() && use(it) && string(it.Kind) == kind && it.NormalizedValue == v {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}
func (f *memIdentifiers) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
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
func (f *memIdentifiers) ApplyVerifiedChange(_ context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.seq++
	id := "id" + itoa(f.seq)
	it := identifier.Identifier{
		ID: id, UserID: input.UserID, Kind: input.Kind, NormalizedValue: input.NormalizedValue,
		VerifiedAt: now, LoginEnabled: input.LoginEnabled, RecoveryEnabled: input.RecoveryEnabled,
		NotificationEnabled: input.NotificationEnabled, IsPrimary: input.MakePrimary, CreatedAt: now, UpdatedAt: now,
	}
	f.byID[id] = it
	return it, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// newTestHandler builds a real authsvc.Service over in-memory fakes, mounts the
// routes on a web.WebHandler, and returns the handler. limiter nil → memory.
func newTestHandler(t *testing.T, limiter ratelimiter.Limiter) http.Handler {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	identifiers := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:          users,
		Identifiers:    identifiers,
		Passwords:      passwords,
		Sessions:       sessions,
		Challenges:     challenges,
		Protector:      memProtector{},
		PasswordResets: &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:         fakeHasher{},
		Mailer:         nopMailer{},
		MailFrom:       "noreply@example.com",
		Deliver:        router,
		Queue:          stubQueue{},
		Limiter:        limiter,
		Cookie:         authsvc.CookieConfig{},
		TokenSigner:    newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, "", MutationSecurity{}, nil)
	return h
}

func do(t *testing.T, h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	return nil
}

func refreshCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == "session_refresh" {
			return c
		}
	}
	return nil
}

// denyLimiter always refuses.
type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: false}, nil
}
func (denyLimiter) Reset(context.Context, string) error { return nil }
func (denyLimiter) Close() error                        { return nil }
