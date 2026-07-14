package authsvc

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// --- compile-time seam assertion ---

var _ challengeProtector = (*fakeProtector)(nil)

// --- fakes ---

// fakeProtector is a deterministic challenge protector: DigestCode's "HMAC" folds
// the key ID + user + purpose + code into a distinct string, so a candidate under
// the wrong key never collides. accepted lists every key CandidateCodeDigests
// returns a candidate for (sorted, mirroring the real protector), which drives the
// key-rotation test. An empty code is rejected (the real protector's empty-code
// guard).
type fakeProtector struct {
	active   string
	accepted []string
}

func newFakeProtector(active string, accepted ...string) *fakeProtector {
	keys := append([]string(nil), accepted...)
	sort.Strings(keys)
	return &fakeProtector{active: active, accepted: keys}
}

func (p *fakeProtector) ActiveKeyID() string { return p.active }

func (p *fakeProtector) DigestCode(keyID, userID, purpose, code string) (string, error) {
	if code == "" {
		return "", errors.New("empty code")
	}
	return "hmac|" + keyID + "|" + userID + "|" + purpose + "|" + code, nil
}

func (p *fakeProtector) CandidateCodeDigests(userID, purpose, code string) ([]challenge.DigestCandidate, error) {
	if code == "" {
		return nil, errors.New("empty code")
	}
	out := make([]challenge.DigestCandidate, 0, len(p.accepted))
	for _, k := range p.accepted {
		d, _ := p.DigestCode(k, userID, purpose, code)
		out = append(out, challenge.DigestCandidate{KeyID: k, Digest: d})
	}
	return out, nil
}

func (p *fakeProtector) DigestToken(token string) string { return "sha|" + token }

// fakeChallenges mirrors the storetest reference: one active row per
// (user, purpose), a unique (purpose, secret_digest) claim, and a consume that
// decides expiry, digest comparison, attempt counting, lockout, and deletion
// inside ONE mutex-held critical section — the single-winner contract. Injectable
// errors drive the infrastructure-failure paths.
type fakeChallenges struct {
	mu                       sync.Mutex
	byID                     map[string]challenge.Challenge
	seq                      int
	replErr, codeErr, tokErr error
}

func newFakeChallenges() *fakeChallenges {
	return &fakeChallenges{byID: map[string]challenge.Challenge{}}
}

func (f *fakeChallenges) Replace(_ context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replErr != nil {
		return challenge.Challenge{}, f.replErr
	}
	for id, ex := range f.byID {
		if ex.UserID == c.UserID && ex.Purpose == c.Purpose {
			delete(f.byID, id)
		}
	}
	for _, ex := range f.byID {
		if ex.Purpose == c.Purpose && ex.SecretDigest == c.SecretDigest {
			return challenge.Challenge{}, sdk.ErrAlreadyExists
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

func (f *fakeChallenges) ConsumeCode(_ context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.codeErr != nil {
		return challenge.Consumed{}, challenge.OutcomeNotFound, f.codeErr
	}
	id, row, found := f.find(userID, purpose)
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
	if expectedContextDigest != "" && string(row.Context) != expectedContextDigest {
		return consumedOfRow(row, now), challenge.OutcomeContextMismatch, nil
	}
	return consumedOfRow(row, now), challenge.OutcomeRedeemed, nil
}

func (f *fakeChallenges) ConsumeToken(_ context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokErr != nil {
		return challenge.Consumed{}, f.tokErr
	}
	if presentedDigest == "" {
		return challenge.Consumed{}, sdk.ErrNotFound
	}
	for id, ex := range f.byID {
		if ex.Purpose == purpose && ex.SecretDigest == presentedDigest {
			delete(f.byID, id)
			if ex.Expired(now) {
				return challenge.Consumed{}, sdk.ErrExpired
			}
			return consumedOfRow(ex, now), nil
		}
	}
	return challenge.Consumed{}, sdk.ErrNotFound
}

func (f *fakeChallenges) PurgeExpired(_ context.Context, before time.Time, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for id, ex := range f.byID {
		if limit > 0 && n >= limit {
			break
		}
		if !ex.ExpiresAt.After(before) {
			delete(f.byID, id)
			n++
		}
	}
	return n, nil
}

func (f *fakeChallenges) find(userID, purpose string) (string, challenge.Challenge, bool) {
	for id, ex := range f.byID {
		if ex.UserID == userID && ex.Purpose == purpose {
			return id, ex, true
		}
	}
	return "", challenge.Challenge{}, false
}

func (f *fakeChallenges) countRows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID)
}

// takeLiveToken guards-and-deletes the live (purpose, digest) token, resolving its
// user — the passwordreset composition's step 1. Unknown/expired/used → not live.
func (f *fakeChallenges) takeLiveToken(purpose, digest string, now time.Time) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if digest == "" {
		return "", false
	}
	for id, ex := range f.byID {
		if ex.Purpose == purpose && ex.SecretDigest == digest {
			if ex.Expired(now) {
				return "", false // expired is not live (guarded delete leaves it)
			}
			delete(f.byID, id)
			return ex.UserID, true
		}
	}
	return "", false
}

// purgeUserPurposes deletes the user's challenges whose purpose is in purposes —
// the passwordreset composition's challenge purge.
func (f *fakeChallenges) purgeUserPurposes(userID string, purposes []string) {
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

func consumedOfRow(c challenge.Challenge, now time.Time) challenge.Consumed {
	return challenge.Consumed{
		ID:             c.ID,
		UserID:         c.UserID,
		Purpose:        c.Purpose,
		Context:        c.Context,
		ProtectorKeyID: c.ProtectorKeyID,
		ConsumedAt:     now.UTC(),
	}
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

// --- harness ---

type challengeHarness struct {
	svc    *Service
	ch     *fakeChallenges
	prot   *fakeProtector
	events *spySecurityEvents
	clock  *time.Time
}

func newChallengeHarness(t *testing.T, prot *fakeProtector) *challengeHarness {
	t.Helper()
	ch := newFakeChallenges()
	events := newSpySecurityEvents()
	now := time.Now().UTC()
	h := &challengeHarness{ch: ch, prot: prot, events: events, clock: &now}
	h.svc = NewService(Deps{
		Users:          newFakeUsers(),
		Passwords:      newFakePasswords(),
		Sessions:       newFakeSessions(),
		Challenges:     ch,
		Protector:      prot,
		Hasher:         &fakeHasher{},
		Limiter:        ratelimiter.NewMemory(),
		SecurityEvents: events,
		TokenSigner:    newFakeSigner(),
		Clock:          func() time.Time { return *h.clock },
	})
	return h
}

func (h *challengeHarness) advance(d time.Duration) { *h.clock = h.clock.Add(d) }

// --- tests ---

// TestIssueConsumeEachCodePurpose proves every code purpose issues a fresh
// six-digit code, protects it under the active key, and redeems it exactly once.
func TestIssueConsumeEachCodePurpose(t *testing.T) {
	codePurposes := []string{
		challenge.PurposeLoginOTP,
		challenge.PurposeChangeEmail,
		challenge.PurposeChangePhone,
		challenge.PurposeRemovePassword,
		challenge.PurposeUnlinkOAuth,
	}
	for _, purpose := range codePurposes {
		t.Run(purpose, func(t *testing.T) {
			h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
			secret, err := h.svc.IssueChallenge(context.Background(), "u1", purpose)
			if err != nil {
				t.Fatalf("IssueChallenge: %v", err)
			}
			if len(secret) != 6 {
				t.Fatalf("code length = %d (%q), want 6", len(secret), secret)
			}
			for _, r := range secret {
				if r < '0' || r > '9' {
					t.Fatalf("code %q has a non-digit", secret)
				}
			}
			consumed, err := h.svc.ConsumeChallenge(context.Background(), "u1", purpose, secret)
			if err != nil {
				t.Fatalf("ConsumeChallenge: %v", err)
			}
			if consumed.UserID != "u1" || consumed.Purpose != purpose {
				t.Fatalf("consumed = %+v, want user u1 / %s", consumed, purpose)
			}
			// Single-use: a second redemption finds nothing.
			if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", purpose, secret); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("second consume err = %v, want ErrChallengeInvalid", err)
			}
		})
	}
}

// TestIssueRedeemTokenPurpose proves the magic-link purpose issues a 256-bit
// URL-safe token, resolves the user from the atomically consumed row, and is
// single-use.
func TestIssueRedeemTokenPurpose(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	token, err := h.svc.IssueChallenge(context.Background(), "u9", challenge.PurposeLoginMagicLink)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token not base64url: %v", err)
	}
	if len(raw) != tokenBytes {
		t.Fatalf("token width = %d bytes, want %d", len(raw), tokenBytes)
	}
	consumed, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token)
	if err != nil {
		t.Fatalf("RedeemToken: %v", err)
	}
	if consumed.UserID != "u9" {
		t.Fatalf("consumed user = %q, want u9", consumed.UserID)
	}
	if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("second redeem err = %v, want ErrChallengeInvalid", err)
	}
}

// TestConsumeCodeOldKeyStillVerifies proves a code issued under a now-rotated-away
// key stays verifiable while the old key is still accepted (design §3.3): the
// atomic consume selects the candidate matching the row's protector_key_id.
func TestConsumeCodeOldKeyStillVerifies(t *testing.T) {
	prot := newFakeProtector("k1", "k1")
	h := newChallengeHarness(t, prot)
	secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeChangeEmail)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	// Rotate: k2 is now active, but k1 remains accepted.
	prot.active = "k2"
	prot.accepted = []string{"k1", "k2"}

	consumed, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeChangeEmail, secret)
	if err != nil {
		t.Fatalf("ConsumeChallenge after rotation: %v", err)
	}
	if consumed.ProtectorKeyID != "k1" {
		t.Fatalf("consumed key = %q, want k1 (issued under old key)", consumed.ProtectorKeyID)
	}
}

// TestConsumeCodeContextMatchAndMismatch proves the code context binding: a
// matching expected context redeems, a mismatching one is ErrChallengeInvalid but
// STILL consumes the row (anti-probing — a valid secret never survives a wrong-
// context replay).
func TestConsumeCodeContextMatchAndMismatch(t *testing.T) {
	t.Run("match redeems", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeChangeEmail, WithStoredContext("new@example.com"))
		if err != nil {
			t.Fatalf("IssueChallenge: %v", err)
		}
		if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeChangeEmail, secret, WithExpectedContext("new@example.com")); err != nil {
			t.Fatalf("ConsumeChallenge (match): %v", err)
		}
	})
	t.Run("mismatch consumes and rejects", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeChangeEmail, WithStoredContext("new@example.com"))
		if err != nil {
			t.Fatalf("IssueChallenge: %v", err)
		}
		if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeChangeEmail, secret, WithExpectedContext("attacker@example.com")); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("mismatch err = %v, want ErrChallengeInvalid", err)
		}
		if h.ch.countRows() != 0 {
			t.Fatalf("row survived a wrong-context replay: %d rows left", h.ch.countRows())
		}
		// Even the correct secret+context now finds nothing.
		if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeChangeEmail, secret, WithExpectedContext("new@example.com")); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("post-consume err = %v, want ErrChallengeInvalid", err)
		}
	})
}

// TestRedeemTokenCurrentBindingInvalidation proves the token binding is validated
// against the caller's CURRENT binding after the atomic delete: a magic link whose
// bound identifier changed since issue fails as ErrChallengeInvalid with the
// secret already spent.
func TestRedeemTokenCurrentBindingInvalidation(t *testing.T) {
	type binding struct {
		Kind  string
		Value string
	}
	issued := binding{Kind: "email", Value: "a@example.com"}
	current := binding{Kind: "email", Value: "b@example.com"}

	t.Run("current binding matches", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		token, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginMagicLink, WithStoredContext(issued))
		if err != nil {
			t.Fatalf("IssueChallenge: %v", err)
		}
		if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token, WithExpectedContext(issued)); err != nil {
			t.Fatalf("RedeemToken (match): %v", err)
		}
	})
	t.Run("current binding changed", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		token, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginMagicLink, WithStoredContext(issued))
		if err != nil {
			t.Fatalf("IssueChallenge: %v", err)
		}
		if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token, WithExpectedContext(current)); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("changed-binding err = %v, want ErrChallengeInvalid", err)
		}
		if h.ch.countRows() != 0 {
			t.Fatalf("token survived a stale-binding redeem: %d rows left", h.ch.countRows())
		}
	})
}

// TestConsumeCodeLockout proves the wrong-attempt budget locks out on the fifth
// wrong code with ErrTooManyAttempts and records a challenge_lockout event whose
// details carry only the purpose — never the secret.
func TestConsumeCodeLockout(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	wrong := "000000"
	if wrong == secret {
		wrong = "111111"
	}
	for i := 0; i < challenge.MaxAttempts-1; i++ {
		if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, wrong); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("attempt %d err = %v, want ErrChallengeInvalid", i+1, err)
		}
	}
	if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, wrong); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("lockout err = %v, want ErrTooManyAttempts", err)
	}
	evt := requireEvent(t, h.events, securityevent.TypeChallengeLockout, securityevent.StatusBlocked)
	if evt.Details["purpose"] != challenge.PurposeLoginOTP {
		t.Fatalf("lockout event purpose = %v, want %s", evt.Details["purpose"], challenge.PurposeLoginOTP)
	}
	for k, v := range evt.Details {
		if s, ok := v.(string); ok && s == secret {
			t.Fatalf("lockout event detail %q leaked the secret code", k)
		}
	}
	// After lockout the code is gone.
	if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, secret); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("post-lockout err = %v, want ErrChallengeInvalid", err)
	}
}

// TestConsumeCodeExpired proves an expired code is ErrChallengeExpired and the row
// is deleted.
func TestConsumeCodeExpired(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	h.advance(6 * time.Minute) // login_otp TTL is 5m
	if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, secret); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("expired err = %v, want ErrChallengeExpired", err)
	}
	if h.ch.countRows() != 0 {
		t.Fatalf("expired row not deleted: %d rows left", h.ch.countRows())
	}
}

// TestRedeemTokenExpired proves an expired token is ErrChallengeExpired.
func TestRedeemTokenExpired(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	token, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginMagicLink)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	h.advance(16 * time.Minute) // magic link TTL is 15m
	if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("expired token err = %v, want ErrChallengeExpired", err)
	}
}

// TestConcurrentCodeSingleWinner proves exactly one of many simultaneous correct
// submissions of the same code redeems (the atomic single-use contract).
func TestConcurrentCodeSingleWinner(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	const n = 24
	var winners int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, secret); err == nil {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if winners != 1 {
		t.Fatalf("concurrent code winners = %d, want 1", winners)
	}
}

// TestConcurrentTokenSingleWinner proves exactly one of many simultaneous
// redemptions of the same token wins.
func TestConcurrentTokenSingleWinner(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	token, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginMagicLink)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	const n = 24
	var winners int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token); err == nil {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if winners != 1 {
		t.Fatalf("concurrent token winners = %d, want 1", winners)
	}
}

// TestChallengeRepositoryFailuresSurface proves an injected repository error is
// returned verbatim (not masked as a challenge outcome) on every path.
func TestChallengeRepositoryFailuresSurface(t *testing.T) {
	boom := errors.New("boom")

	t.Run("Replace", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		h.ch.replErr = boom
		if _, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP); !errors.Is(err, boom) {
			t.Fatalf("IssueChallenge err = %v, want boom", err)
		}
	})
	t.Run("ConsumeCode", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		secret, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP)
		if err != nil {
			t.Fatalf("IssueChallenge: %v", err)
		}
		h.ch.codeErr = boom
		if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, secret); !errors.Is(err, boom) {
			t.Fatalf("ConsumeChallenge err = %v, want boom", err)
		}
	})
	t.Run("ConsumeToken", func(t *testing.T) {
		h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
		token, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginMagicLink)
		if err != nil {
			t.Fatalf("IssueChallenge: %v", err)
		}
		h.ch.tokErr = boom
		if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, token); !errors.Is(err, boom) {
			t.Fatalf("RedeemToken err = %v, want boom", err)
		}
	})
}

// TestChallengeUnknownPurpose proves an unregistered purpose is rejected on every
// entry point, so a user-controlled purpose string can never drive the rail.
func TestChallengeUnknownPurpose(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	if _, err := h.svc.IssueChallenge(context.Background(), "u1", "bogus"); !errors.Is(err, ErrUnknownChallengePurpose) {
		t.Fatalf("IssueChallenge err = %v, want ErrUnknownChallengePurpose", err)
	}
	if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", "bogus", "123456"); !errors.Is(err, ErrUnknownChallengePurpose) {
		t.Fatalf("ConsumeChallenge err = %v, want ErrUnknownChallengePurpose", err)
	}
	if _, err := h.svc.RedeemToken(context.Background(), "bogus", "tok"); !errors.Is(err, ErrUnknownChallengePurpose) {
		t.Fatalf("RedeemToken err = %v, want ErrUnknownChallengePurpose", err)
	}
}

// TestChallengeWrongCallerPath proves the format gate: a token purpose cannot be
// redeemed through the code path and vice versa (the "allowed caller path"
// metadata).
func TestChallengeWrongCallerPath(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginMagicLink, "123456"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("code path on token purpose err = %v, want invalid", err)
	}
	if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginOTP, "tok"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("token path on code purpose err = %v, want invalid", err)
	}
}

// TestChallengeEmptyPresented proves an empty submission is a generic invalid on
// both paths and never counts an attempt or matches.
func TestChallengeEmptyPresented(t *testing.T) {
	h := newChallengeHarness(t, newFakeProtector("k1", "k1"))
	if _, err := h.svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP); err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	if _, err := h.svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, ""); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty code err = %v, want ErrChallengeInvalid", err)
	}
	if _, err := h.svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, ""); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty token err = %v, want ErrChallengeInvalid", err)
	}
}

// TestChallengeSubsystemOff proves the service methods fail closed when the
// challenge repository / protector are not wired.
func TestChallengeSubsystemOff(t *testing.T) {
	svc := NewService(Deps{
		Users:       newFakeUsers(),
		Hasher:      &fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		TokenSigner: newFakeSigner(),
	})
	if _, err := svc.IssueChallenge(context.Background(), "u1", challenge.PurposeLoginOTP); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("off IssueChallenge err = %v, want forbidden", err)
	}
	if _, err := svc.ConsumeChallenge(context.Background(), "u1", challenge.PurposeLoginOTP, "123456"); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("off ConsumeChallenge err = %v, want forbidden", err)
	}
	if _, err := svc.RedeemToken(context.Background(), challenge.PurposeLoginMagicLink, "tok"); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("off RedeemToken err = %v, want forbidden", err)
	}
}
