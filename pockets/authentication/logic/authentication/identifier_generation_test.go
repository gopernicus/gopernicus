package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
)

// Pause after the older pending generation commits but before it issues a code.
type reorderedContactStart struct {
	contactchange.Repository
	mu               sync.Mutex
	calls            int
	entered, release chan struct{}
}

func (r *reorderedContactStart) Create(ctx context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	out, err := r.Repository.Create(ctx, p)
	if err != nil {
		return out, err
	}
	r.mu.Lock()
	r.calls++
	first := r.calls == 1
	r.mu.Unlock()
	if first {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return contactchange.PendingChange{}, ctx.Err()
		}
	}
	return out, nil
}

func TestIdentifierConcurrentStartsKeepLatestFlowUsable(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "identifier-race@example.com", "password123456789")
	pending := &reorderedContactStart{Repository: h.svc.contactChanges, entered: make(chan struct{}), release: make(chan struct{})}
	h.svc.contactChanges = pending
	dq := h.svc.queue.(*drainingQueue)
	h.svc.queue = dq.svc
	in := IdentifierChangeStart{UserID: uid, SessionID: sid, Kind: identifier.KindEmail, Value: "older@example.com", Uses: identifier.Uses{Login: true, Recovery: true}, MakePrimary: true}
	done := make(chan error, 1)
	go func() { _, err := h.svc.StartIdentifierChange(ctx, in); done <- err }()
	<-pending.entered
	latest := in
	latest.Value = "newer@example.com"
	if _, err := h.svc.StartIdentifierChange(ctx, latest); err != nil {
		close(pending.release)
		t.Fatal(err)
	}
	close(pending.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	dq.disp.drain(t, dq.proc)
	codeFor := func(destination string) string {
		h.mailer.mu.Lock()
		defer h.mailer.mu.Unlock()
		for i := len(h.mailer.sent) - 1; i >= 0; i-- {
			message := h.mailer.sent[i]
			if len(message.To) == 1 && message.To[0] == destination {
				return extractCode(t, message.Text)
			}
		}
		t.Fatalf("no confirmation delivered to %s", destination)
		return ""
	}
	oldCode := codeFor(in.Value)
	newCode := codeFor(latest.Value)
	confirmation := IdentifierChangeConfirm{UserID: uid, SessionID: sid, Kind: in.Kind, Code: oldCode}
	if oldCode != newCode {
		if err := h.svc.ConfirmIdentifierChange(ctx, confirmation); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("obsolete pending generation accepted: %v", err)
		}
	}
	current, err := pending.Get(ctx, uid, in.Kind)
	if err != nil || current.NewValue != latest.Value {
		t.Fatalf("obsolete confirmation destroyed latest pending value: %+v %v", current, err)
	}
	confirmation.Code = newCode
	if err := h.svc.ConfirmIdentifierChange(ctx, confirmation); err != nil {
		t.Fatalf("current pending value has no usable delivered proof: %v", err)
	}
}

func TestIdentifierStartRejectsMissingCompletionRail(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.credentialMutations = nil
	_, err := h.svc.StartIdentifierChange(context.Background(), IdentifierChangeStart{UserID: "u", Kind: identifier.KindEmail, Value: "target@example.com"})
	if !errors.Is(err, ErrCredentialMutationUnavailable) {
		t.Fatalf("unfinishable start accepted: %v", err)
	}
	if len(h.svc.contactChanges.(*fakeContactChanges).m) != 0 {
		t.Fatal("partial rail persisted pending change")
	}
}
