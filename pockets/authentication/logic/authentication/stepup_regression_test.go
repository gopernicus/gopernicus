package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// Hold the first queue admission AFTER its challenge was persisted. A later
// request can then persist and enqueue before the older request resumes.
type reorderedCodeQueue struct {
	deliveryQueue
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	count   int
}

func (q *reorderedCodeQueue) Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	q.mu.Lock()
	q.count++
	first := q.count == 1
	q.mu.Unlock()
	if first {
		close(q.entered)
		select {
		case <-q.release:
		case <-ctx.Done():
			return delivery.Receipt{}, ctx.Err()
		}
	}
	return q.deliveryQueue.Enqueue(ctx, cmd)
}

func TestStepUpConcurrentStartsKeepCurrentGenerationDeliverable(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "generation@example.com", "password123456789")
	dq := h.svc.queue.(*drainingQueue)
	q := &reorderedCodeQueue{deliveryQueue: dq.svc, entered: make(chan struct{}), release: make(chan struct{})}
	h.svc.queue = q
	in := StepUpStart{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword, Context: "operation"}
	first := make(chan StepUpReceipt, 1)
	fail := make(chan error, 1)
	go func() { r, e := h.svc.BeginStepUp(ctx, in); first <- r; fail <- e }()
	<-q.entered
	second, err := h.svc.BeginStepUp(ctx, in)
	if err != nil {
		close(q.release)
		t.Fatal(err)
	}
	close(q.release)
	older := <-first
	if err := <-fail; err != nil {
		t.Fatal(err)
	}
	if older.Receipt == second.Receipt {
		t.Fatal("distinct challenge generations shared a queue key")
	}
	h.ch.mu.Lock()
	_, current, ok := h.ch.find(stepUpSubject(uid, stepUpCodeBinding(sid, in.Purpose, in.Context, "")), challenge.PurposeStepUp)
	h.ch.mu.Unlock()
	if !ok {
		t.Fatal("current challenge missing")
	}
	before := h.mailer.count()
	dq.disp.drain(t, dq.proc)
	h.mailer.mu.Lock()
	messages := append(h.mailer.sent[:0:0], h.mailer.sent[before:]...)
	h.mailer.mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("accepted generations delivered %d messages, want 2", len(messages))
	}
	var liveCode, oldCode string
	for _, message := range messages {
		code := verificationCodeFromMail(t, message)
		candidates, err := h.svc.protector.CandidateCodeDigests(uid, challenge.PurposeStepUp, code)
		if err != nil {
			t.Fatal(err)
		}
		match := false
		for _, candidate := range candidates {
			if candidate.KeyID == current.ProtectorKeyID && candidate.Digest == current.SecretDigest {
				match = true
			}
		}
		if match {
			liveCode = code
		} else {
			oldCode = code
		}
	}
	if liveCode == "" {
		t.Fatal("the current challenge has no delivered matching code")
	}
	completion := StepUpCompletion{UserID: uid, SessionID: sid, Purpose: in.Purpose, Context: in.Context}
	if oldCode != "" {
		if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, completion, oldCode); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("superseded code: %v", err)
		}
	}
	if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, completion, liveCode); err != nil {
		t.Fatalf("current delivered code: %v", err)
	}
	if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, completion, liveCode); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("code reused: %v", err)
	}
}

func TestStepUpCodeBindingsAndSMSMetadata(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "scope@example.com", "password123456789")
	now := time.Now().UTC()
	phone := "+14155550123"
	h.idents.insert(identifier.Identifier{ID: "phone-step", UserID: uid, Kind: identifier.KindPhone, NormalizedValue: phone, VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, CreatedAt: now, UpdatedAt: now})
	notifier := &recordingNotifier{}
	wireSyncDelivery(t, h.svc, h.mailer, map[string]delivery.BodySender{sdk.AddressKindPhone: notifier})
	in := StepUpStart{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeUnlinkOAuth, Context: "google", Kind: sdk.AddressKindPhone}
	if _, err := h.svc.BeginStepUp(ctx, in); err != nil {
		t.Fatal(err)
	}
	code := notifier.codeFor(t, phone)
	wrong := StepUpCompletion{UserID: uid, SessionID: sid, Purpose: in.Purpose, Context: "github", Kind: in.Kind}
	if _, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, wrong, code); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("wrong operation: %v", err)
	}
	// A different pending operation must not replace the first operation's code.
	other := in
	other.Context = "github"
	if _, err := h.svc.BeginStepUp(ctx, other); err != nil {
		t.Fatal(err)
	}
	right := wrong
	right.Context = in.Context
	g, err := h.svc.CompleteStepUpWithIdentifierCode(ctx, right, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Methods) != 1 || g.Methods[0].Kind != session.MethodSMSCode || !g.Methods[0].PSTN {
		t.Fatalf("dishonest SMS proof metadata: %+v", g.Methods)
	}
}

func TestRecentAuthenticationPolicyAndLiveness(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "policy@example.com", "password123456789")
	h.backdateLogin(sid, time.Hour)
	now := time.Now().UTC()
	owner, err := h.users.Get(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	g := authgrant.Grant{SessionID: sid, UserID: uid, Purpose: authgrant.PurposeSetPassword, ContextDigest: grantContextDigest(""), AuthenticatedAt: now.Add(-time.Minute), Assurance: session.AssuranceAAL1, ExpiresAt: now.Add(time.Minute)}
	if _, err := h.grants.Create(ctx, g, owner.AuthRevision, now); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []RecentAuthPolicy{{MaxAge: time.Second}, {MinAssurance: session.AssuranceAAL2}} {
		if _, err := h.svc.RequireRecentAuthentication(ctx, sid, uid, g.Purpose, "", policy); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("unsuitable proof: %v", err)
		}
	}
	if h.grants.unconsumed() != 1 {
		t.Fatal("unsuitable policy spent proof")
	}
	if _, err := h.svc.RequireRecentAuthentication(ctx, sid, uid, g.Purpose, "", RecentAuthPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RequireRecentAuthentication(ctx, sid, uid, g.Purpose, "", RecentAuthPolicy{}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("second consume: %v", err)
	}
	if _, err := h.grants.Create(ctx, g, owner.AuthRevision, now); err != nil {
		t.Fatal(err)
	}
	if err := h.sess.Delete(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RequireRecentAuthentication(ctx, sid, uid, g.Purpose, "", RecentAuthPolicy{}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("deleted session accepted grant: %v", err)
	}
}

type pausedStepUpHasher struct {
	Hasher
	verified, release chan struct{}
}

func (h *pausedStepUpHasher) VerifyPassword(hash, password string) error {
	if err := h.Hasher.VerifyPassword(hash, password); err != nil {
		return err
	}
	close(h.verified)
	<-h.release
	return nil
}

func TestStepUpPasswordCannotAdmitGrantAfterCredentialRevocation(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "step-revoke@example.com", "password123456789")
	paused := &pausedStepUpHasher{Hasher: h.svc.hasher, verified: make(chan struct{}), release: make(chan struct{})}
	h.svc.hasher = paused
	done := make(chan error, 1)
	go func() {
		_, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword}, "password123456789")
		done <- err
	}()
	<-paused.verified
	if err := h.pw.Set(ctx, uid, "hash:replacement-password"); err != nil {
		close(paused.release)
		t.Fatal(err)
	}
	close(paused.release)
	if err := <-done; err == nil {
		t.Fatal("old password admitted a grant after revocation")
	}
	if h.grants.unconsumed() != 0 {
		t.Fatal("stale proof persisted a grant")
	}
}

func TestStepUpCannotEarnUnsupportedAssurance(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "aal2@example.com", "password123456789")
	_, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword, Policy: RecentAuthPolicy{MinAssurance: session.AssuranceAAL2}}, "password123456789")
	if !errors.Is(err, ErrStepUpProof) || h.grants.unconsumed() != 0 {
		t.Fatalf("single factor produced an AAL2 grant: %v", err)
	}
}

func TestRecentAuthenticationPreservesSessionReadError(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "read-error@example.com", "password123456789")
	cause := errors.New("session store unavailable")
	h.sess.getErr = cause
	if _, err := h.svc.RequireRecentAuthentication(ctx, sid, uid, authgrant.PurposeSetPassword, "", RecentAuthPolicy{}); !errors.Is(err, cause) {
		t.Fatalf("session error hidden as absent proof: %v", err)
	}
}
