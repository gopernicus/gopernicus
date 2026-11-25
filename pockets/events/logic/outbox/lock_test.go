package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

// stubLock is a Lock whose outcome the test sets: held elsewhere, failing, or
// free (runs fn).
type stubLock struct {
	heldElsewhere bool
	err           error
	calls         int
}

func (l *stubLock) TryRun(ctx context.Context, fn func(context.Context) error) (bool, error) {
	l.calls++
	if l.err != nil {
		return false, l.err
	}
	if l.heldElsewhere {
		return false, nil
	}
	return true, fn(ctx)
}

func TestPollWithLockHeldElsewhereReadsNothing(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(recordFor(t, "a"), time.Now().UTC())
	bus := &stubDelivery{}
	lock := &stubLock{heldElsewhere: true}
	p := newTestPoller(t, repo, bus.deliver, WithLock(lock))

	if err := p.Poll(context.Background()); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("Poll = %v, want workers.ErrNoWork", err)
	}
	if lock.calls != 1 || repo.listCalled != 0 || bus.emitCall != 0 {
		t.Fatalf("lock calls=%d reads=%d emits=%d, want 1/0/0", lock.calls, repo.listCalled, bus.emitCall)
	}
}

func TestPollWithLockErrorPropagates(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(recordFor(t, "a"), time.Now().UTC())
	lockErr := errors.New("lock unavailable")
	p := newTestPoller(t, repo, (&stubDelivery{}).deliver, WithLock(&stubLock{err: lockErr}))

	if err := p.Poll(context.Background()); !errors.Is(err, lockErr) {
		t.Fatalf("Poll = %v, want the lock error", err)
	}
	if repo.listCalled != 0 {
		t.Fatalf("reads=%d after a lock error, want 0", repo.listCalled)
	}
}

func TestPollWithLockHeldDrains(t *testing.T) {
	repo := newFakeRepo()
	rec := recordFor(t, "a")
	repo.seed(rec, time.Now().UTC())
	bus := &stubDelivery{}
	p := newTestPoller(t, repo, bus.deliver, WithLock(NewMemoryLock()))

	if err := p.Poll(context.Background()); err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if repo.markCount(rec.EventID) != 1 || len(bus.emitted) != 1 {
		t.Fatalf("marks=%d emitted=%v, want one of each", repo.markCount(rec.EventID), bus.emitted)
	}
	if err := p.Poll(context.Background()); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("Poll on drained outbox = %v, want workers.ErrNoWork", err)
	}
}

func TestPollWithLockPassesDrainErrorThrough(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(recordFor(t, "a"), time.Now().UTC())
	deliverErr := errors.New("bus down")
	bus := &stubDelivery{emitErr: deliverErr}
	p := newTestPoller(t, repo, bus.deliver, WithLock(NewMemoryLock()))

	if err := p.Poll(context.Background()); !errors.Is(err, deliverErr) {
		t.Fatalf("Poll = %v, want the delivery error", err)
	}
}

func TestNewPollerRejectsNilLock(t *testing.T) {
	var typedNil *MemoryLock
	for name, lock := range map[string]Lock{"nil": nil, "typed nil": typedNil} {
		t.Run(name, func(t *testing.T) {
			p, err := NewPoller(newFakeRepo(), (&stubDelivery{}).deliver, WithLock(lock))
			if p != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("poller=%v error=%v, want ErrInvalidInput", p, err)
			}
		})
	}
}

func TestMemoryLockZeroValueIsUsable(t *testing.T) {
	var l MemoryLock
	ran, err := l.TryRun(context.Background(), func(context.Context) error { return nil })
	if !ran || err != nil {
		t.Fatalf("TryRun = (%v, %v), want (true, nil)", ran, err)
	}
}
