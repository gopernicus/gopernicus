package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// Both admission methods pause at the same point so this test also catches a
// regression back to replacing a stable key after the challenge was issued.
type reorderedVerificationQueue struct {
	deliveryQueue
	once             sync.Once
	entered, release chan struct{}
}

func (q *reorderedVerificationQueue) pause(ctx context.Context) error {
	first := false
	q.once.Do(func() { first = true; close(q.entered) })
	if first {
		select {
		case <-q.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (q *reorderedVerificationQueue) Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	if err := q.pause(ctx); err != nil {
		return delivery.Receipt{}, err
	}
	return q.deliveryQueue.Enqueue(ctx, cmd)
}
func (q *reorderedVerificationQueue) Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	if err := q.pause(ctx); err != nil {
		return delivery.Receipt{}, err
	}
	return q.deliveryQueue.Replace(ctx, cmd)
}

func TestVerificationRenderedGenerationsRemainDeliverable(t *testing.T) {
	for _, firstOperation := range []string{"register", "admin_resend"} {
		t.Run(firstOperation, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			addr := "render-order@example.com"
			if firstOperation == "admin_resend" {
				h.mustRegister(t, addr, "password123456789")
			}
			dq := h.svc.queue.(*drainingQueue)
			q := &reorderedVerificationQueue{deliveryQueue: dq.svc, entered: make(chan struct{}), release: make(chan struct{})}
			h.svc.queue = q
			var once sync.Once
			release := func() { once.Do(func() { close(q.release) }) }
			defer release()
			before := h.mailer.count()
			done := make(chan error, 1)
			go func() {
				if firstOperation == "register" {
					_, err := h.svc.Register(ctx, addr, "password123456789", "Test")
					done <- err
					return
				}
				ident, err := h.idents.GetLogin(ctx, "email", addr)
				if err == nil {
					_, err = h.svc.ResendVerificationForUser(ctx, Principal{}, ident.UserID)
				}
				done <- err
			}()
			select {
			case <-q.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("first issuance did not reach queue admission")
			}
			ident, err := h.idents.GetLogin(ctx, "email", addr)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.ResendVerificationForUser(ctx, Principal{}, ident.UserID); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			dq.disp.drain(t, dq.proc)
			if got := h.mailer.count() - before; got != 2 {
				t.Fatalf("accepted issuances delivered %d messages, want 2", got)
			}
			// Match the current stored challenge to actual provider output; ordering
			// of the two queue admissions must not determine which proof survives.
			h.ch.mu.Lock()
			_, current, found := h.ch.find(ident.UserID, challenge.PurposeVerifyRegistration)
			h.ch.mu.Unlock()
			if !found {
				t.Fatal("current challenge missing")
			}
			h.mailer.mu.Lock()
			messages := append(h.mailer.sent[:0:0], h.mailer.sent[before:]...)
			h.mailer.mu.Unlock()
			var liveCode string
			for _, message := range messages {
				code := verificationCodeFromMail(t, message)
				candidates, err := h.prot.CandidateCodeDigests(ident.UserID, challenge.PurposeVerifyRegistration, code)
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range candidates {
					if candidate.KeyID == current.ProtectorKeyID && candidate.Digest == current.SecretDigest {
						liveCode = code
					}
				}
			}
			if liveCode == "" {
				t.Fatal("current proof has no delivered matching code")
			}
			if err := h.svc.Verify(ctx, addr, liveCode); err != nil {
				t.Fatalf("delivered current proof rejected: %v", err)
			}
		})
	}
}

type delayedVerificationChallenge struct {
	challenge.Repository
	once             sync.Once
	entered, release chan struct{}
}

func (p *delayedVerificationChallenge) Replace(ctx context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	first := false
	p.once.Do(func() { first = true; close(p.entered) })
	if first {
		select {
		case <-p.release:
		case <-ctx.Done():
			return challenge.Challenge{}, ctx.Err()
		}
	}
	return p.Repository.Replace(ctx, c)
}

func TestVerificationOpaqueLateInitializerKeepsItsDeliveryCommand(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	addr := "opaque-order@example.com"
	h.mustRegister(t, addr, "password123456789")
	dq := h.svc.queue.(*drainingQueue)
	h.svc.queue = dq.svc
	delayed := &delayedVerificationChallenge{Repository: h.svc.challenges, entered: make(chan struct{}), release: make(chan struct{})}
	h.svc.challenges = delayed
	var once sync.Once
	release := func() { once.Do(func() { close(delayed.release) }) }
	defer release()
	claim := func() *memDispatchItem {
		item, ok := dq.disp.claimPending()
		if !ok {
			t.Fatal("no pending delivery")
		}
		dq.disp.mu.Lock()
		item.state = "running"
		dq.disp.mu.Unlock()
		return item
	}
	checkpoint := func(item *memDispatchItem) func(context.Context, []byte) error {
		return func(_ context.Context, sealed []byte) error {
			dq.disp.mu.Lock()
			defer dq.disp.mu.Unlock()
			if dq.disp.byKey[item.key] != item.id || !item.active {
				return sdk.ErrConflict
			}
			item.payload = sealed
			return nil
		}
	}
	if err := h.svc.ResendVerification(ctx, addr); err != nil {
		t.Fatal(err)
	}
	first := claim()
	done := make(chan error, 1)
	go func() { done <- dq.proc.Handle(ctx, first.id, first.payload, first.attempt, checkpoint(first)) }()
	select {
	case <-delayed.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first initializer did not reach issuance")
	}
	if err := h.svc.ResendVerification(ctx, addr); err != nil {
		t.Fatal(err)
	}
	second := claim()
	if err := dq.proc.Handle(ctx, second.id, second.payload, second.attempt, checkpoint(second)); err != nil {
		t.Fatal(err)
	}
	earlierCode := verificationCodeFromMail(t, h.mailer.last())
	before := h.mailer.count()
	release()
	if err := <-done; err != nil {
		t.Fatalf("late initializer lost its accepted delivery command: %v", err)
	}
	if first.key == second.key {
		t.Fatal("independent opaque starts shared a supersedable key")
	}
	if h.mailer.count() != before+1 {
		t.Fatal("late initializer's current challenge was not delivered")
	}
	currentCode := verificationCodeFromMail(t, h.mailer.last())
	if earlierCode != currentCode {
		if err := h.svc.Verify(ctx, addr, earlierCode); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("superseded code: %v", err)
		}
	}
	if err := h.svc.Verify(ctx, addr, currentCode); err != nil {
		t.Fatalf("current code stranded: %v", err)
	}
}
