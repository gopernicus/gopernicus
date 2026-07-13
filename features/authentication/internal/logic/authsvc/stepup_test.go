package authsvc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// fakeAuthGrants mirrors the storetest reference authgrant.Repository: Create
// assigns an id, Consume atomically matches (session, purpose, context) among
// unconsumed rows and decides expiry/single-use, and DeleteBySession is the
// revocation cascade.
type fakeAuthGrants struct {
	mu sync.Mutex
	m  map[string]authgrant.Grant
	n  int
}

func newFakeAuthGrants() *fakeAuthGrants { return &fakeAuthGrants{m: map[string]authgrant.Grant{}} }

func (f *fakeAuthGrants) Create(_ context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g.ID == "" {
		f.n++
		g.ID = "grant-" + itoa(f.n)
	}
	f.m[g.ID] = g
	return g, nil
}

func (f *fakeAuthGrants) Consume(_ context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, g := range f.m {
		if g.Consumed() || g.SessionID != sessionID || g.Purpose != purpose || g.ContextDigest != contextDigest {
			continue
		}
		g.ConsumedAt = now.UTC()
		f.m[id] = g
		if g.Expired(now) {
			return authgrant.Grant{}, sdk.ErrExpired
		}
		return g, nil
	}
	return authgrant.Grant{}, sdk.ErrNotFound
}

func (f *fakeAuthGrants) DeleteBySession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, g := range f.m {
		if g.SessionID == sessionID {
			delete(f.m, id)
		}
	}
	return nil
}

// unconsumed counts the persisted grants not yet spent.
func (f *fakeAuthGrants) unconsumed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, g := range f.m {
		if !g.Consumed() {
			n++
		}
	}
	return n
}

// --- helpers ---

// mustVerifiedLogin registers, verifies, and logs in a user, returning the user id,
// email, and the live session id the login minted. The primary email is verified so
// BeginStepUp can deliver a step-up code to it.
func (h *harness) mustVerifiedLogin(t *testing.T, email, password string) (userID, normEmail, sessionID string) {
	t.Helper()
	ctx := context.Background()
	u := h.mustRegister(t, email, password)
	code := h.mailer.codeFor(t, normalizeTestEmail(email))
	if err := h.svc.Verify(ctx, email, code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, _, err := h.svc.Login(ctx, email, password); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return u.ID, normalizeTestEmail(email), h.sessionIDFor(t, u.ID)
}

// normalizeTestEmail lowercases the test email the way the default normalizer does
// (the harness uses the strict default). The test addresses are already lowercase
// local parts, so a simple lowercase suffices.
func normalizeTestEmail(email string) string {
	// The default normalizer lowercases the whole address for email.
	b := []byte(email)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// sessionIDFor returns the single live session id for userID from the fake store.
func (h *harness) sessionIDFor(t *testing.T, userID string) string {
	t.Helper()
	h.sess.mu.Lock()
	defer h.sess.mu.Unlock()
	for id, s := range h.sess.m {
		if s.UserID == userID {
			return id
		}
	}
	t.Fatalf("no session for user %s", userID)
	return ""
}

// backdateLogin pushes the session's recorded primary authentication into the past
// so the recent-primary-login shortcut cannot fire, isolating the explicit-grant
// path.
func (h *harness) backdateLogin(sessionID string, age time.Duration) {
	h.sess.mu.Lock()
	defer h.sess.mu.Unlock()
	s := h.sess.m[sessionID]
	s.Authentication.AuthenticatedAt = time.Now().Add(-age)
	h.sess.m[sessionID] = s
}

func hasEvent(spy *spySecurityEvents, eventType, status string) bool {
	for _, e := range spy.recorded() {
		if e.EventType == eventType && e.EventStatus == status {
			return true
		}
	}
	return false
}

// --- login metadata ---

func TestLoginRecordsRecentAuthentication(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "meta@example.com", "password123456789")

	sess, err := h.sess.Get(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if !sess.Authentication.Recorded() {
		t.Fatal("login did not record authentication metadata on the session")
	}
	if got := sess.Authentication.Assurance; got != session.AssuranceAAL1 {
		t.Fatalf("assurance = %q, want aal1", got)
	}
	if len(sess.Authentication.Methods) != 1 || sess.Authentication.Methods[0].Kind != session.MethodPassword {
		t.Fatalf("methods = %+v, want single password descriptor", sess.Authentication.Methods)
	}
	_ = userID
}

// --- recent-primary-login shortcut ---

func TestRecentAuthenticationShortcutSatisfies(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "recent@example.com", "password123456789")

	g, err := h.svc.RequireRecentAuthentication(context.Background(), sessionID, userID,
		authgrant.PurposeRemovePassword, "", RecentAuthPolicy{})
	if err != nil {
		t.Fatalf("RequireRecentAuthentication (shortcut): %v", err)
	}
	if g.UserID != userID {
		t.Fatalf("grant user = %q, want %q", g.UserID, userID)
	}
	// The shortcut never persists or spends a grant.
	if n := h.grants.unconsumed(); n != 0 {
		t.Fatalf("shortcut spent/created a grant: unconsumed=%d", n)
	}
}

func TestRecentAuthenticationShortcutStaleLoginRejected(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "stale@example.com", "password123456789")
	h.backdateLogin(sessionID, 10*time.Minute) // older than the 5m default window

	_, err := h.svc.RequireRecentAuthentication(context.Background(), sessionID, userID,
		authgrant.PurposeRemovePassword, "", RecentAuthPolicy{})
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("stale login: err = %v, want ErrStepUpRequired", err)
	}
}

func TestRecentAuthenticationShortcutAssuranceRejected(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "assurance@example.com", "password123456789")

	// An AAL1 password login cannot satisfy a policy demanding AAL2 — the shortcut
	// is refused and the operation requires an explicit fresh step-up.
	_, err := h.svc.RequireRecentAuthentication(context.Background(), sessionID, userID,
		authgrant.PurposeRemovePassword, "", RecentAuthPolicy{MinAssurance: session.AssuranceAAL2})
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("assurance gate: err = %v, want ErrStepUpRequired", err)
	}
}

// --- step-up with password ---

func TestStepUpWithPasswordEarnsGrant(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "pw@example.com", "password123456789")
	h.backdateLogin(sessionID, time.Hour) // force the explicit grant path

	ctx := context.Background()
	in := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeRemovePassword, Context: ""}
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, in, "password123456789"); err != nil {
		t.Fatalf("CompleteStepUpWithPassword: %v", err)
	}
	if !hasEvent(h.events, securityevent.TypeStepUp, securityevent.StatusSuccess) {
		t.Fatal("no step_up success event recorded")
	}
	if n := h.grants.unconsumed(); n != 1 {
		t.Fatalf("unconsumed grants = %d, want 1", n)
	}

	// The mutation consumes it atomically; a second consume finds nothing (single-use).
	g, err := h.svc.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeRemovePassword, "", RecentAuthPolicy{})
	if err != nil {
		t.Fatalf("consume grant: %v", err)
	}
	if g.UserID != userID {
		t.Fatalf("grant user = %q, want %q", g.UserID, userID)
	}
	if _, err := h.svc.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeRemovePassword, "", RecentAuthPolicy{}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("second consume: err = %v, want ErrStepUpRequired", err)
	}
}

func TestStepUpWithPasswordWrongPasswordRejected(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "wrongpw@example.com", "password123456789")

	in := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeSetPassword, Context: ""}
	_, err := h.svc.CompleteStepUpWithPassword(context.Background(), in, "not-the-password")
	if !errors.Is(err, ErrStepUpProof) {
		t.Fatalf("wrong password: err = %v, want ErrStepUpProof", err)
	}
	if n := h.grants.unconsumed(); n != 0 {
		t.Fatalf("a rejected step-up minted a grant: unconsumed=%d", n)
	}
	if !hasEvent(h.events, securityevent.TypeStepUp, securityevent.StatusFailure) {
		t.Fatal("no step_up failure event recorded")
	}
}

// --- step-up with identifier code ---

func TestStepUpWithIdentifierCodeEarnsGrant(t *testing.T) {
	h := newHarness(t, nil)
	userID, normEmail, sessionID := h.mustVerifiedLogin(t, "code@example.com", "password123456789")
	h.backdateLogin(sessionID, time.Hour)

	ctx := context.Background()
	const purpose = authgrant.PurposeUnlinkOAuth
	const opCtx = "google"
	receipt, err := h.svc.BeginStepUp(ctx, StepUpStart{SessionID: sessionID, UserID: userID, Purpose: purpose, Context: opCtx})
	if err != nil {
		t.Fatalf("BeginStepUp: %v", err)
	}
	if !receipt.Delivered {
		t.Fatal("BeginStepUp reported no delivery")
	}
	if !hasEvent(h.events, securityevent.TypeStepUpChallengeSent, securityevent.StatusSuccess) {
		t.Fatal("no step_up_challenge_sent event recorded")
	}

	code := h.mailer.codeFor(t, normEmail)
	in := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: purpose, Context: opCtx}
	if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, in, code); err != nil {
		t.Fatalf("CompleteStepUpWithIdentifierCode: %v", err)
	}
	if n := h.grants.unconsumed(); n != 1 {
		t.Fatalf("unconsumed grants = %d, want 1", n)
	}
	// The grant is bound to the exact purpose+context.
	if _, err := h.svc.RequireRecentAuthentication(ctx, sessionID, userID, purpose, opCtx, RecentAuthPolicy{}); err != nil {
		t.Fatalf("consume identifier-code grant: %v", err)
	}
}

func TestStepUpCodeWrongContextRejected(t *testing.T) {
	h := newHarness(t, nil)
	userID, normEmail, sessionID := h.mustVerifiedLogin(t, "wrongctx@example.com", "password123456789")
	h.backdateLogin(sessionID, time.Hour)

	ctx := context.Background()
	// Issue a code to unlink google.
	if _, err := h.svc.BeginStepUp(ctx, StepUpStart{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeUnlinkOAuth, Context: "google"}); err != nil {
		t.Fatalf("BeginStepUp: %v", err)
	}
	code := h.mailer.codeFor(t, normEmail)

	// A code earned for google cannot complete a github unlink — the stored context
	// mismatches, the code is consumed, and no grant is minted.
	in := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeUnlinkOAuth, Context: "github"}
	if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, in, code); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("wrong context: err = %v, want ErrChallengeInvalid", err)
	}
	if n := h.grants.unconsumed(); n != 0 {
		t.Fatalf("wrong-context step-up minted a grant: unconsumed=%d", n)
	}
	// The code was spent, so retrying with the right context also fails.
	right := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeUnlinkOAuth, Context: "google"}
	if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, right, code); err == nil {
		t.Fatal("a consumed wrong-context code was replayable with the right context")
	}
}

func TestBeginStepUpNoVerifiedIdentifier(t *testing.T) {
	h := newHarness(t, nil)
	// Register but do NOT verify: the primary email identifier is unverified, so no
	// active verified identifier exists to deliver a step-up code to.
	u := h.mustRegister(t, "unverified@example.com", "password123456789")
	sessionID := "sess-x"
	h.sess.m[sessionID] = session.Session{ID: sessionID, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}

	_, err := h.svc.BeginStepUp(context.Background(), StepUpStart{SessionID: sessionID, UserID: u.ID, Purpose: authgrant.PurposeRemovePassword})
	if !errors.Is(err, ErrStepUpDestination) {
		t.Fatalf("no verified identifier: err = %v, want ErrStepUpDestination", err)
	}
}

// --- grant binding ---

func TestRecentAuthenticationGrantWrongPurposeNotConsumed(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "purpose@example.com", "password123456789")
	h.backdateLogin(sessionID, time.Hour)

	ctx := context.Background()
	in := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeSetPassword, Context: ""}
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, in, "password123456789"); err != nil {
		t.Fatalf("CompleteStepUpWithPassword: %v", err)
	}
	// A grant earned for set_password cannot authorize remove_password.
	if _, err := h.svc.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeRemovePassword, "", RecentAuthPolicy{}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("wrong purpose: err = %v, want ErrStepUpRequired", err)
	}
	if n := h.grants.unconsumed(); n != 1 {
		t.Fatalf("wrong-purpose consume spent the grant: unconsumed=%d", n)
	}
}

func TestRecentAuthenticationGrantBoundToSession(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "sessbind@example.com", "password123456789")
	h.backdateLogin(sessionID, time.Hour)

	ctx := context.Background()
	in := StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeRemovePassword, Context: ""}
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, in, "password123456789"); err != nil {
		t.Fatalf("CompleteStepUpWithPassword: %v", err)
	}
	// A grant earned on one session cannot be consumed from another.
	if _, err := h.svc.RequireRecentAuthentication(ctx, "other-session", userID, authgrant.PurposeRemovePassword, "", RecentAuthPolicy{}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("other session: err = %v, want ErrStepUpRequired", err)
	}
}

func TestRecentAuthenticationUnavailableWhenGrantsOff(t *testing.T) {
	h := newHarness(t, nil)
	userID, _, sessionID := h.mustVerifiedLogin(t, "off@example.com", "password123456789")
	h.svc.authGrants = nil // subsystem off

	_, err := h.svc.RequireRecentAuthentication(context.Background(), sessionID, userID, authgrant.PurposeRemovePassword, "", RecentAuthPolicy{})
	if !errors.Is(err, ErrStepUpUnavailable) {
		t.Fatalf("grants off: err = %v, want ErrStepUpUnavailable", err)
	}
}
