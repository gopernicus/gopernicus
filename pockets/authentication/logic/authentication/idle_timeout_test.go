package authentication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const testIdleTimeout = 3 * time.Minute

// fakeActivity is the idle-timeout capability over fakeSessions: a stamp per
// row that only RotateAt writes, and a CAS DeleteIfIdle. beforeDelete runs
// just before DeleteIfIdle takes the lock, to stage a concurrent writer.
type fakeActivity struct {
	*fakeSessions
	stamps       map[string]time.Time
	reads        int
	beforeDelete func()
	probeErr     error
}

func newFakeActivity(s *fakeSessions) *fakeActivity {
	return &fakeActivity{fakeSessions: s, stamps: map[string]time.Time{}}
}

func (f *fakeActivity) GetByRefreshHashWithActivity(ctx context.Context, hash string) (session.Session, session.RefreshMatch, session.Activity, error) {
	sess, match, err := f.GetByRefreshHash(ctx, hash)
	if err != nil {
		return session.Session{}, 0, session.Activity{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	stamp, ok := f.stamps[sess.ID]
	return sess, match, session.Activity{LastRotatedAt: stamp, Stamped: ok}, nil
}

func (f *fakeActivity) RotateAt(_ context.Context, id, expectedCurrentHash, newHash string, now time.Time) error {
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
	f.stamps[id] = now
	return nil
}

func (f *fakeActivity) DeleteIfIdle(_ context.Context, id string, observed session.Activity, observedRotationCount int) (bool, error) {
	if f.beforeDelete != nil {
		hook := f.beforeDelete
		f.beforeDelete = nil
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[id]
	if !ok || s.RotationCount != observedRotationCount {
		return false, nil
	}
	stamp, stamped := f.stamps[id]
	if stamped != observed.Stamped || (stamped && !stamp.Equal(observed.LastRotatedAt)) {
		return false, nil
	}
	delete(f.m, id)
	delete(f.stamps, id)
	return true, nil
}

func (f *fakeActivity) ProbeSchema(context.Context) error { return f.probeErr }

// idleClock is a settable service clock.
type idleClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *idleClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *idleClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// idleHarness is a harness whose service runs on a settable clock with the
// idle timeout wired over fakeActivity.
type idleHarness struct {
	*harness
	activity *fakeActivity
	clock    *idleClock
}

func newIdleHarness(t *testing.T) *idleHarness {
	t.Helper()
	h := newHarness(t, nil)
	clock := &idleClock{now: time.Now().UTC()}
	activity := newFakeActivity(h.sess)
	h.svc.now = clock.Now
	h.svc.idleTimeout = testIdleTimeout
	h.svc.activity = activity
	return &idleHarness{harness: h, activity: activity, clock: clock}
}

// unwire turns the idle timeout off, as on a host that has not set it.
func (h *idleHarness) unwire() { h.svc.activity, h.svc.idleTimeout = nil, 0 }

func (h *idleHarness) rewire() { h.svc.activity, h.svc.idleTimeout = h.activity, testIdleTimeout }

func (h *idleHarness) idleEvents() []securityevent.SecurityEvent {
	var out []securityevent.SecurityEvent
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeSessionIdleExpired {
			out = append(out, e)
		}
	}
	return out
}

func (h *idleHarness) eventCount(eventType string) int {
	n := 0
	for _, e := range h.events.recorded() {
		if e.EventType == eventType {
			n++
		}
	}
	return n
}

// TestIdleTimeoutExpiresAStampedSession: a refresh inside the idle window
// rotates and stamps; one after a full idle window is the generic refusal, the
// row is deleted, and session_idle_expired is recorded once with its bounded
// details.
func TestIdleTimeoutExpiresAStampedSession(t *testing.T) {
	h := newIdleHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "idle@example.com", "password123456789")

	h.clock.advance(2 * time.Minute)
	rotated, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil || rotated.RefreshToken == "" {
		t.Fatalf("Refresh inside the idle window = %+v, %v; want a rotation", rotated, err)
	}
	stampedAt := h.clock.Now()
	ids := snapshotSessions(h.harness)
	if len(ids) != 1 || !h.activity.stamps[ids[0]].Equal(stampedAt) {
		t.Fatalf("rotation stamp = %v, want %v", h.activity.stamps, stampedAt)
	}

	h.clock.advance(testIdleTimeout)
	if _, err := h.svc.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("Refresh after the idle window = %v, want ErrInvalidRefreshToken", err)
	}
	if h.sess.count() != 0 {
		t.Fatalf("idle session not deleted: %d rows", h.sess.count())
	}
	events := h.idleEvents()
	if len(events) != 1 {
		t.Fatalf("session_idle_expired rows = %d, want 1", len(events))
	}
	e := events[0]
	if e.EventStatus != securityevent.StatusBlocked || e.UserID == "" {
		t.Errorf("event = %+v, want blocked for the owner", e)
	}
	want := map[string]any{
		"session_id":           ids[0],
		"last_activity":        stampedAt.UTC().Format(time.RFC3339),
		"idle_seconds":         int(testIdleTimeout / time.Second),
		"idle_timeout_seconds": int(testIdleTimeout / time.Second),
		"surface":              "refresh",
	}
	if len(e.Details) != len(want) {
		t.Errorf("details = %v, want exactly %v", e.Details, want)
	}
	for k, v := range want {
		if e.Details[k] != v {
			t.Errorf("details[%s] = %v, want %v", k, e.Details[k], v)
		}
	}
	if n := h.eventCount(securityevent.TypeRefreshReuse); n != 0 {
		t.Errorf("refresh_reuse rows = %d, want 0", n)
	}
	// The dead token stays dead and records nothing more.
	if _, err := h.svc.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) || len(h.idleEvents()) != 1 {
		t.Fatalf("second refresh = %v with %d idle rows, want ErrInvalidRefreshToken and still 1", err, len(h.idleEvents()))
	}
}

// TestIdleTimeoutJudgesANeverRotatedSessionOnCreatedAt: a login nobody
// refreshed is genuinely idle, judged from created_at.
func TestIdleTimeoutJudgesANeverRotatedSessionOnCreatedAt(t *testing.T) {
	h := newIdleHarness(t)
	pair := h.loginPair(t, "fresh@example.com", "password123456789")
	h.clock.advance(testIdleTimeout - time.Second)
	if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); err != nil {
		t.Fatalf("Refresh just inside the window = %v, want a rotation", err)
	}

	h2 := newIdleHarness(t)
	pair2 := h2.loginPair(t, "stale@example.com", "password123456789")
	h2.clock.advance(testIdleTimeout)
	if _, err := h2.svc.Refresh(context.Background(), pair2.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("first refresh of a stale login = %v, want ErrInvalidRefreshToken", err)
	}
	if len(h2.idleEvents()) != 1 || h2.sess.count() != 0 {
		t.Fatalf("idle rows = %d, sessions = %d; want 1 and 0", len(h2.idleEvents()), h2.sess.count())
	}
}

// TestIdleTimeoutGrandfathersLegacyRotation: a row rotated while idle expiry
// was unwired (rotation_count > 0, no stamp) is not judged until its first
// wired refresh stamps it; from then on it is.
func TestIdleTimeoutGrandfathersLegacyRotation(t *testing.T) {
	h := newIdleHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "legacy@example.com", "password123456789")

	h.unwire()
	legacy, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("unwired Refresh: %v", err)
	}
	if len(h.activity.stamps) != 0 || h.activity.reads != 0 {
		t.Fatalf("the unwired path touched the activity column: stamps %v, reads %d", h.activity.stamps, h.activity.reads)
	}

	h.rewire()
	h.clock.advance(24 * time.Hour)
	next, err := h.svc.Refresh(ctx, legacy.RefreshToken)
	if err != nil {
		t.Fatalf("grandfathered Refresh after a day = %v, want a rotation", err)
	}
	if len(h.idleEvents()) != 0 || len(h.activity.stamps) != 1 {
		t.Fatalf("idle rows %d, stamps %v; want 0 and the first stamp", len(h.idleEvents()), h.activity.stamps)
	}

	h.clock.advance(testIdleTimeout)
	if _, err := h.svc.Refresh(ctx, next.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("Refresh a full window after the stamp = %v, want ErrInvalidRefreshToken", err)
	}
	if len(h.idleEvents()) != 1 {
		t.Fatalf("idle rows = %d, want 1", len(h.idleEvents()))
	}
}

// TestIdleTimeoutReuseTakesPrecedence: a replayed consumed previous slot is
// reuse even when the session is also idle, so theft detection is never
// swallowed.
func TestIdleTimeoutReuseTakesPrecedence(t *testing.T) {
	h := newIdleHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "reuse@example.com", "password123456789")
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("grace: %v", err)
	}
	h.clock.advance(testIdleTimeout + time.Minute)
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("replay of the consumed slot = %v, want ErrInvalidRefreshToken", err)
	}
	if h.eventCount(securityevent.TypeRefreshReuse) != 1 || len(h.idleEvents()) != 0 {
		t.Fatalf("refresh_reuse %d, idle %d; want 1 and 0", h.eventCount(securityevent.TypeRefreshReuse), len(h.idleEvents()))
	}
}

// TestIdleTimeoutJudgesAnUnconsumedPreviousSlot: the grace lane of an idle
// session is idle too.
func TestIdleTimeoutJudgesAnUnconsumedPreviousSlot(t *testing.T) {
	h := newIdleHarness(t)
	ctx := context.Background()
	pair := h.loginPair(t, "prev@example.com", "password123456789")
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	h.clock.advance(testIdleTimeout)
	got, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if !errors.Is(err, ErrInvalidRefreshToken) || got.AccessToken != "" {
		t.Fatalf("idle grace = %+v, %v; want ErrInvalidRefreshToken", got, err)
	}
	if len(h.idleEvents()) != 1 || h.eventCount(securityevent.TypeRefreshReuse) != 0 {
		t.Fatalf("idle %d, reuse %d; want 1 and 0", len(h.idleEvents()), h.eventCount(securityevent.TypeRefreshReuse))
	}
}

// TestIdleTimeoutEventOnlyWhenThisCallDeletes: a DeleteIfIdle that loses its
// compare-and-swap records nothing. A row a concurrent caller already deleted
// answers the generic refusal without a refresh_reuse row; a row a concurrent
// rotation moved re-resolves to the grace lane.
func TestIdleTimeoutEventOnlyWhenThisCallDeletes(t *testing.T) {
	t.Run("row already gone", func(t *testing.T) {
		h := newIdleHarness(t)
		ctx := context.Background()
		pair := h.loginPair(t, "gone@example.com", "password123456789")
		h.clock.advance(testIdleTimeout)
		h.activity.beforeDelete = func() {
			for _, id := range snapshotSessions(h.harness) {
				_ = h.sess.Delete(ctx, id)
			}
		}
		if _, err := h.svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
			t.Fatalf("Refresh = %v, want ErrInvalidRefreshToken", err)
		}
		if len(h.idleEvents()) != 0 || h.eventCount(securityevent.TypeRefreshReuse) != 0 {
			t.Fatalf("idle %d, reuse %d; want neither", len(h.idleEvents()), h.eventCount(securityevent.TypeRefreshReuse))
		}
	})

	t.Run("row moved under a concurrent rotation", func(t *testing.T) {
		h := newIdleHarness(t)
		ctx := context.Background()
		pair := h.loginPair(t, "moved@example.com", "password123456789")
		h.clock.advance(testIdleTimeout)
		hash, err := h.svc.hashSessionToken(pair.RefreshToken)
		if err != nil {
			t.Fatal(err)
		}
		h.activity.beforeDelete = func() {
			id := snapshotSessions(h.harness)[0]
			if err := h.activity.RotateAt(ctx, id, hash, "concurrent-winner-hash", h.clock.Now()); err != nil {
				t.Errorf("staged rotation: %v", err)
			}
		}
		got, err := h.svc.Refresh(ctx, pair.RefreshToken)
		if err != nil || got.AccessToken == "" || got.RefreshToken != "" {
			t.Fatalf("Refresh = %+v, %v; want the grace lane's access token only", got, err)
		}
		if len(h.idleEvents()) != 0 || h.sess.count() != 1 {
			t.Fatalf("idle %d, sessions %d; want 0 and 1", len(h.idleEvents()), h.sess.count())
		}
	})
}

// TestIdleTimeoutFirstPartyOnly: a delegated connection's row is refused by
// the first-party refresh before any idle judgement, and never deleted or
// recorded idle.
func TestIdleTimeoutFirstPartyOnly(t *testing.T) {
	h := newIdleHarness(t)
	ctx := context.Background()
	now := h.clock.Now()
	delegated := session.Session{ID: "delegated-1", UserID: "u1", Profile: session.ProfileDelegated,
		Delegation:       session.Delegation{ClientID: "client", Issuer: "https://issuer.example.test", Resource: "https://api.example.test"},
		RefreshTokenHash: "delegated-hash", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if _, err := h.sess.Create(ctx, delegated); err != nil {
		t.Fatal(err)
	}
	h.clock.advance(testIdleTimeout + time.Minute)
	if _, err := h.svc.refreshByHash(ctx, "delegated-hash"); !errors.Is(err, ErrDelegatedCredential) {
		t.Fatalf("refreshByHash(delegated) = %v, want ErrDelegatedCredential", err)
	}
	if h.sess.count() != 1 || len(h.idleEvents()) != 0 {
		t.Fatalf("sessions %d, idle %d; want 1 and 0", h.sess.count(), len(h.idleEvents()))
	}
	if _, idle := h.svc.idleExpired(delegated, session.Activity{}); idle {
		t.Fatal("idleExpired judged a delegated row")
	}
}

// TestIdleTimeoutUnwiredIsUnchanged: with no idle timeout the refresh path
// never reads or writes the activity column and an old session refreshes.
func TestIdleTimeoutUnwiredIsUnchanged(t *testing.T) {
	h := newIdleHarness(t)
	h.unwire()
	pair := h.loginPair(t, "off@example.com", "password123456789")
	h.clock.advance(48 * time.Hour)
	if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); err != nil {
		t.Fatalf("unwired Refresh after two days = %v, want a rotation", err)
	}
	if h.activity.reads != 0 || len(h.activity.stamps) != 0 || len(h.idleEvents()) != 0 {
		t.Fatalf("reads %d, stamps %v, idle %d; want none", h.activity.reads, h.activity.stamps, len(h.idleEvents()))
	}
}

// TestValidateSessionLifetime pins the post-default refusals in every mode,
// the WARNs, and the wired-store checks.
func TestValidateSessionLifetime(t *testing.T) {
	sessions := newFakeSessions()
	activity := newFakeActivity(sessions)
	run := func(d constructorConfig) (string, error) {
		var buf bytes.Buffer
		if d.Sessions == nil {
			d.Sessions = activity
		}
		if d.RuntimeMode == "" {
			d.RuntimeMode = environment.ModeDevelopment
		}
		err := validateSessionLifetime(d, slog.New(slog.NewTextHandler(&buf, nil)))
		return buf.String(), err
	}

	for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
		if _, err := run(constructorConfig{RuntimeMode: mode, AccessTokenTTL: 200 * time.Hour}); !errors.Is(err, ErrAccessTokenTTLExceedsRefreshTTL) || !strings.Contains(err.Error(), "RefreshTTL 168h") {
			t.Errorf("%s: AccessTokenTTL 200h over the default horizon = %v, want ErrAccessTokenTTLExceedsRefreshTTL naming RefreshTTL", mode, err)
		}
		for _, idle := range []time.Duration{-time.Second, 30 * time.Second, 15 * time.Minute} {
			if _, err := run(constructorConfig{RuntimeMode: mode, IdleTimeout: idle}); !errors.Is(err, ErrIdleTimeoutBelowAccessTokenTTL) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("%s: IdleTimeout %s = %v, want ErrIdleTimeoutBelowAccessTokenTTL", mode, idle, err)
			}
		}
	}

	logs, err := run(constructorConfig{AccessTokenTTL: time.Minute, IdleTimeout: 90 * time.Second})
	if err != nil || !strings.Contains(logs, "posture_warning=idle_granularity") {
		t.Errorf("IdleTimeout under twice the access lifetime: err = %v, logs:\n%s", err, logs)
	}
	logs, err = run(constructorConfig{AccessTokenTTL: time.Minute, IdleTimeout: 3 * time.Minute})
	if err != nil || strings.Contains(logs, "level=WARN") {
		t.Errorf("a coherent idle timeout: err = %v, logs:\n%s", err, logs)
	}
	logs, err = run(constructorConfig{RefreshTTL: time.Hour, IdleTimeout: 2 * time.Hour})
	if err != nil || !strings.Contains(logs, "posture_warning=idle_never_fires") {
		t.Errorf("IdleTimeout past the horizon: err = %v, logs:\n%s", err, logs)
	}

	logs, err = run(constructorConfig{RuntimeMode: environment.ModeProduction, AccessTokenTTL: 2 * time.Hour})
	if err != nil || strings.Count(logs, "posture_warning=access_token_ttl_long access_token_ttl=2h0m0s ceiling=1h0m0s") != 1 {
		t.Errorf("production AccessTokenTTL 2h: err = %v, logs:\n%s", err, logs)
	}
	if logs, _ := run(constructorConfig{AccessTokenTTL: 2 * time.Hour}); strings.Contains(logs, "access_token_ttl_long") {
		t.Errorf("development logged the ceiling WARN:\n%s", logs)
	}

	if _, err := run(constructorConfig{Sessions: sessions, IdleTimeout: time.Hour}); !errors.Is(err, ErrIdleTimeoutRequires) {
		t.Errorf("idle on a store without the capability = %v, want ErrIdleTimeoutRequires", err)
	}
	if _, err := run(constructorConfig{Sessions: sessions}); err != nil {
		t.Errorf("unwired on a store without the capability = %v, want nil", err)
	}
	activity.probeErr = sdk.ErrNotFound
	if _, err := run(constructorConfig{IdleTimeout: time.Hour}); !errors.Is(err, ErrIdleTimeoutSchemaMissing) || !strings.Contains(err.Error(), "0029_session_activity.sql") {
		t.Errorf("unmigrated = %v, want ErrIdleTimeoutSchemaMissing naming 0029", err)
	}
	if _, err := run(constructorConfig{}); err != nil {
		t.Errorf("unwired on an unmigrated store = %v, want nil (never probed)", err)
	}
	activity.probeErr = errors.New("permission denied")
	if _, err := run(constructorConfig{IdleTimeout: time.Hour}); !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, ErrIdleTimeoutSchemaMissing) {
		t.Errorf("probe failure = %v, want sdk.ErrUnavailable, not schema-missing", err)
	}
}

// TestNewWiresIdleTimeout: New wires the activity capability only when the
// idle timeout is set.
func TestNewWiresIdleTimeout(t *testing.T) {
	users := newFakeUsers()
	sessions := newFakeSessions()
	activity := newFakeActivity(sessions)
	build := func(idle time.Duration) (*Service, error) {
		c, err := New(Repositories{Users: users, Identifiers: newFakeIdentifiers(users), Sessions: activity, ActiveSessions: fakeActiveSessions{users, sessions}},
			newFakeSigner(), environment.ModeDevelopment, ratelimiter.NewMemory(),
			WithPassword(PasswordConfig{PasswordFlowsDisabled: true}), WithSessions(SessionsConfig{IdleTimeout: idle}))
		if err != nil {
			return nil, err
		}
		return c.Service, nil
	}
	svc, err := build(0)
	if err != nil || svc.activity != nil || svc.SessionPolicy().IdleTimeout != 0 {
		t.Fatalf("unwired New = %v, activity %v", err, svc)
	}
	svc, err = build(time.Hour)
	if err != nil || svc.activity == nil {
		t.Fatalf("wired New = %v", err)
	}
	if got := svc.SessionPolicy(); got.IdleTimeout != time.Hour || got.AccessTokenTTL != defaultAccessTokenTTL || got.RefreshTTL != defaultRefreshTTL {
		t.Fatalf("SessionPolicy = %+v", got)
	}
}

// casMissActivity is a store whose DeleteIfIdle never matches, as a store
// with a broken stamp encoding would behave.
type casMissActivity struct{ *fakeActivity }

func (casMissActivity) DeleteIfIdle(context.Context, string, session.Activity, int) (bool, error) {
	return false, nil
}

// failingRereadSessions fails the re-read after a lost DeleteIfIdle.
type failingRereadSessions struct {
	*fakeSessions
	err error
}

func (f failingRereadSessions) GetByRefreshHash(context.Context, string) (session.Session, session.RefreshMatch, error) {
	return session.Session{}, 0, f.err
}

// TestIdleTimeoutCASMismatchIsNeverReuse: a DeleteIfIdle that misses a row
// which did not move (same hash current, same rotation count) is an internal
// error with a session_activity_cas_mismatch WARN — never reuse, never a
// deletion, no event, and no hash in the log.
func TestIdleTimeoutCASMismatchIsNeverReuse(t *testing.T) {
	h := newIdleHarness(t)
	var logs bytes.Buffer
	h.svc.logger = slog.New(slog.NewTextHandler(&logs, nil))
	h.svc.activity = casMissActivity{h.activity}
	ctx := context.Background()
	pair := h.loginPair(t, "casmiss@example.com", "password123456789")
	hash, err := h.svc.hashSessionToken(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	h.clock.advance(testIdleTimeout)

	_, err = h.svc.Refresh(ctx, pair.RefreshToken)
	if !errors.Is(err, errSessionActivityCASMismatch) || errors.Is(err, ErrInvalidRefreshToken) || errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("Refresh = %v, want the internal CAS-mismatch error", err)
	}
	if h.sess.count() != 1 || len(h.idleEvents()) != 0 || h.eventCount(securityevent.TypeRefreshReuse) != 0 {
		t.Fatalf("sessions %d, idle %d, reuse %d; want 1, 0, 0", h.sess.count(), len(h.idleEvents()), h.eventCount(securityevent.TypeRefreshReuse))
	}
	if strings.Count(logs.String(), "session_activity_cas_mismatch") != 1 || strings.Contains(logs.String(), "refresh token reuse") {
		t.Fatalf("logs:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), hash) || strings.Contains(logs.String(), pair.RefreshToken) {
		t.Fatalf("the WARN carries a token or hash:\n%s", logs.String())
	}
}

// TestIdleTimeoutRereadErrorIsReturned: a store error on the re-read after a
// lost DeleteIfIdle is returned as is, never swallowed into a denial or reuse.
func TestIdleTimeoutRereadErrorIsReturned(t *testing.T) {
	h := newIdleHarness(t)
	h.svc.activity = casMissActivity{h.activity}
	storeErr := fmt.Errorf("sessions read: %w", sdk.ErrUnavailable)
	h.svc.sessions = failingRereadSessions{fakeSessions: h.sess, err: storeErr}
	pair := h.loginPair(t, "reread@example.com", "password123456789")
	h.clock.advance(testIdleTimeout)

	if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, storeErr) {
		t.Fatalf("Refresh = %v, want the re-read error", err)
	}
	if h.sess.count() != 1 || len(h.idleEvents()) != 0 || h.eventCount(securityevent.TypeRefreshReuse) != 0 {
		t.Fatalf("sessions %d, idle %d, reuse %d; want 1, 0, 0", h.sess.count(), len(h.idleEvents()), h.eventCount(securityevent.TypeRefreshReuse))
	}
}
