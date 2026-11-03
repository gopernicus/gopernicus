package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
)

const testGraceWindow = 10 * time.Second

// newGraceWindowHarness is an idleHarness with the refresh grace window wired
// and the idle timeout off.
func newGraceWindowHarness(t *testing.T) *idleHarness {
	t.Helper()
	h := newIdleHarness(t)
	h.svc.idleTimeout = 0
	h.svc.refreshGraceWindow = testGraceWindow
	return h
}

// graceWindowEvents counts refresh successes carrying the grace_window detail.
func (h *idleHarness) graceWindowEvents() int {
	n := 0
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeRefresh && e.Details["detail"] == "grace_window" {
			n++
		}
	}
	return n
}

// TestRefreshGraceWindowConcurrentTabs is #54's acceptance: five tabs present
// one refresh token at once. Every call succeeds, exactly one rotates, the
// session survives, no refresh_reuse is recorded, and the winner's token keeps
// rotating.
func TestRefreshGraceWindowConcurrentTabs(t *testing.T) {
	h := newGraceWindowHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "tabs@example.com", "password123456789")

	const tabs = 5
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results [tabs]TokenPair
		errs    [tabs]error
	)
	for i := range tabs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = h.svc.Refresh(ctx, pair.RefreshToken)
		}()
	}
	close(start)
	wg.Wait()

	winner := ""
	for i := range tabs {
		if errs[i] != nil || results[i].AccessToken == "" {
			t.Fatalf("tab %d = %+v, %v; want an access token", i, results[i], errs[i])
		}
		if results[i].RefreshToken != "" {
			if winner != "" {
				t.Fatalf("two tabs rotated")
			}
			winner = results[i].RefreshToken
		}
	}
	if winner == "" {
		t.Fatal("no tab rotated")
	}
	if n := h.eventCount(securityevent.TypeRefreshReuse); n != 0 || h.sess.count() != 1 {
		t.Fatalf("reuse events %d, sessions %d; want 0 and 1", n, h.sess.count())
	}
	if got := h.graceWindowEvents(); got != tabs-1 {
		t.Errorf("grace_window refreshes = %d, want %d", got, tabs-1)
	}
	if next, err := h.svc.Refresh(ctx, winner); err != nil || next.RefreshToken == "" {
		t.Fatalf("winner's token = %+v, %v; want a rotation", next, err)
	}
}

// TestRefreshGraceWindowSequentialThenExpires: inside the window the previous
// token is admitted any number of times without consuming the single-use slot;
// past it, the slot gives one more grace refresh and the next presentation is
// reuse, exactly as with no window.
func TestRefreshGraceWindowSequentialThenExpires(t *testing.T) {
	h := newGraceWindowHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "seq@example.com", "password123456789")

	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	for i := range 4 {
		got, err := h.svc.Refresh(ctx, pair.RefreshToken)
		if err != nil || got.AccessToken == "" || got.RefreshToken != "" {
			t.Fatalf("in-window presentation %d = %+v, %v; want an access token only", i, got, err)
		}
	}
	hash, err := h.svc.hashSessionToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if sess, _, err := h.sess.GetByRefreshHash(ctx, hash); err != nil || sess.PreviousUsed {
		t.Fatalf("previous slot after in-window refreshes = %+v, %v; want unconsumed", sess, err)
	}

	h.clock.advance(testGraceWindow)
	if got, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil || got.RefreshToken != "" {
		t.Fatalf("first presentation past the window = %+v, %v; want the single-use grace", got, err)
	}
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("second presentation past the window = %v, want ErrInvalidRefreshToken", err)
	}
	if h.sess.count() != 0 || h.eventCount(securityevent.TypeRefreshReuse) != 1 {
		t.Fatalf("sessions %d, reuse events %d; want the session revoked as reuse", h.sess.count(), h.eventCount(securityevent.TypeRefreshReuse))
	}
}

// TestRefreshGraceWindowAdmitsAfterConsumedSlot: a lost rotation CAS consumes
// the single-use slot through the re-resolution, and a later presentation
// inside the window is still admitted rather than read as reuse.
func TestRefreshGraceWindowAdmitsAfterConsumedSlot(t *testing.T) {
	h := newGraceWindowHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "slot@example.com", "password123456789")
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	hash, err := h.svc.hashSessionToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	id := snapshotSessions(h.harness)[0]
	if err := h.sess.ConsumeGrace(ctx, id, hash); err != nil {
		t.Fatalf("consume slot: %v", err)
	}
	if got, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil || got.AccessToken == "" {
		t.Fatalf("in-window presentation on a consumed slot = %+v, %v; want an access token", got, err)
	}
	if h.eventCount(securityevent.TypeRefreshReuse) != 0 || h.sess.count() != 1 {
		t.Fatal("in-window presentation on a consumed slot read as reuse")
	}
}

// TestRefreshGraceWindowLostRotationTakesWindow: a refresh that loses the
// rotation CAS to a concurrent tab re-resolves into the window, leaving the
// single-use slot unconsumed.
func TestRefreshGraceWindowLostRotationTakesWindow(t *testing.T) {
	h := newGraceWindowHarness(t)
	ctx := context.Background()
	race := &rotateRaceActivity{fakeActivity: h.activity}
	h.svc.activity = race
	pair := h.loginPair(t, "lost@example.com", "password123456789")
	hash, err := h.svc.hashSessionToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	race.before = func() {
		id := snapshotSessions(h.harness)[0]
		if err := h.activity.RotateAt(ctx, id, hash, "concurrent-winner-hash", h.clock.Now()); err != nil {
			t.Errorf("staged rotation: %v", err)
		}
	}
	got, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil || got.AccessToken == "" || got.RefreshToken != "" {
		t.Fatalf("Refresh after a lost RotateAt = %+v, %v; want an access token only", got, err)
	}
	if h.graceWindowEvents() != 1 {
		t.Errorf("grace_window refreshes = %d, want 1", h.graceWindowEvents())
	}
	if sess, _, err := h.sess.GetByRefreshHash(ctx, hash); err != nil || sess.PreviousUsed {
		t.Fatalf("previous slot = %+v, %v; want unconsumed", sess, err)
	}
}

// TestRefreshGraceWindowNeverJudgesIdle: with only the window wired, rotation
// stamps last_rotated_at, but an old stamp never expires the session as idle.
func TestRefreshGraceWindowNeverJudgesIdle(t *testing.T) {
	h := newGraceWindowHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "noidle@example.com", "password123456789")
	next, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if len(h.activity.stamps) != 1 {
		t.Fatalf("stamps = %v, want the rotation stamped", h.activity.stamps)
	}
	h.clock.advance(72 * time.Hour)
	if got, err := h.svc.Refresh(ctx, next.RefreshToken); err != nil || got.RefreshToken == "" {
		t.Fatalf("refresh after a long gap = %+v, %v; want a rotation", got, err)
	}
	if len(h.idleEvents()) != 0 {
		t.Fatal("window-only wiring judged idleness")
	}
}
