package maintenance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// testNow is the fixed start of every fake clock.
var testNow = time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)

// Every filestorage.Storer is an ArchiveSink.
var _ ArchiveSink = filestorage.Storer(nil)

// fakeClock is a settable clock; tick advances it on every read when set.
type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	tick time.Duration
}

func newClock() *fakeClock { return &fakeClock{now: testNow} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now
	c.now = c.now.Add(c.tick)
	return t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeCall is one recorded repository call.
type fakeCall struct {
	method string
	before time.Time
	limit  int
}

// fakeRepo is an in-memory Repository. Plain steps purge from a counter of
// eligible rows; the rail is a sorted slice of rows. allowPurge, when set,
// returns the row ids a purge may remove; it is read once per purge call.
type fakeRepo struct {
	mu         sync.Mutex
	calls      []fakeCall
	eligible   map[string]int
	errs       map[string]error
	panics     map[string]bool
	stale      int
	rail       []ArchivedEvent
	purgeRows  []PurgeResult
	purged     []string
	allowPurge func() map[string]bool
	violations []string
	onExport   func()
	extraPurge int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{eligible: map[string]int{}, errs: map[string]error{}, panics: map[string]bool{}}
}

func (r *fakeRepo) plain(method string, before time.Time, limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fakeCall{method, before, limit})
	if r.panics[method] {
		panic("boom in " + method)
	}
	if err := r.errs[method]; err != nil {
		return 0, err
	}
	n := min(limit, r.eligible[method])
	r.eligible[method] -= n
	return n, nil
}

func (r *fakeRepo) PurgeExpiredSessions(_ context.Context, before time.Time, limit int) (int, error) {
	return r.plain("sessions", before, limit)
}
func (r *fakeRepo) PurgeExpiredOAuthStates(_ context.Context, before time.Time, limit int) (int, error) {
	return r.plain("oauth_states", before, limit)
}
func (r *fakeRepo) PurgeExpiredGrants(_ context.Context, before time.Time, limit int) (int, error) {
	return r.plain("grants", before, limit)
}
func (r *fakeRepo) PurgeExpiredContactChanges(_ context.Context, before time.Time, limit int) (int, error) {
	return r.plain("contact_changes", before, limit)
}
func (r *fakeRepo) PurgeInvitations(_ context.Context, before time.Time, limit int) (int, error) {
	return r.plain("invitations", before, limit)
}
func (r *fakeRepo) CountStaleAcceptingInvitations(_ context.Context, before time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fakeCall{"count_stale", before, 0})
	return r.stale, r.errs["count_stale"]
}
func (r *fakeRepo) PruneOAuth2(_ context.Context, now time.Time, limit int) (int, error) {
	return r.plain("oauth2", now, limit)
}

func (r *fakeRepo) ExportSecurityEvents(_ context.Context, before time.Time, after ExportCursor, limit int) ([]ArchivedEvent, ExportCursor, error) {
	if r.onExport != nil {
		r.onExport()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fakeCall{"export", before, limit})
	if err := r.errs["export"]; err != nil {
		return nil, after, err
	}
	var out []ArchivedEvent
	for _, row := range r.rail {
		if row.CreatedAt.After(before) || !afterCursor(row, after) {
			continue
		}
		if len(out) >= limit && !row.CreatedAt.Equal(out[len(out)-1].CreatedAt) {
			break
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, after, nil
	}
	last := out[len(out)-1]
	return out, ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func afterCursor(row ArchivedEvent, c ExportCursor) bool {
	if c.CreatedAt.IsZero() && c.ID == "" {
		return true
	}
	return row.CreatedAt.After(c.CreatedAt) || (row.CreatedAt.Equal(c.CreatedAt) && row.ID > c.ID)
}

func (r *fakeRepo) PurgeSecurityEventsBefore(_ context.Context, before time.Time, limit int) (PurgeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fakeCall{"purge_rail", before, limit})
	if err := r.errs["purge_rail"]; err != nil {
		return PurgeResult{}, err
	}
	var allowed map[string]bool
	if r.allowPurge != nil {
		allowed = r.allowPurge()
	}
	n := 0
	kept := r.rail[:0]
	for _, row := range r.rail {
		if n < limit && !row.CreatedAt.After(before) {
			if allowed != nil && !allowed[row.ID] {
				r.violations = append(r.violations, row.ID)
			}
			r.purged = append(r.purged, row.ID)
			n++
			continue
		}
		kept = append(kept, row)
	}
	r.rail = kept
	n += r.extraPurge
	res := PurgeResult{Cutoff: before, Count: n}
	r.purgeRows = append(r.purgeRows, res)
	return res, nil
}

func (r *fakeRepo) callsOf(method string) []fakeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []fakeCall
	for _, c := range r.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (r *fakeRepo) railLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rail)
}

// seedRail adds n rows created one microsecond apart starting at from.
func (r *fakeRepo) seedRail(n int, from time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range n {
		r.rail = append(r.rail, ArchivedEvent{SecurityEvent: securityevent.SecurityEvent{
			ID: fmt.Sprintf("evt-%06d", i), UserID: "u1", EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusSuccess,
			Details: map[string]any{"reason": "x"}, IPAddress: "203.0.113.1", CreatedAt: from.Add(time.Duration(i) * time.Microsecond),
		}})
	}
	sortRail(r.rail)
}

func sortRail(rows []ArchivedEvent) {
	slices.SortFunc(rows, func(a, b ArchivedEvent) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// fakeChallenges implements challenge.Repository's purge; the rest is unused.
type fakeChallenges struct {
	challenge.Repository
	mu       sync.Mutex
	eligible int
	befores  []time.Time
}

func (c *fakeChallenges) PurgeExpired(_ context.Context, before time.Time, limit int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.befores = append(c.befores, before)
	n := min(limit, c.eligible)
	c.eligible -= n
	return n, nil
}

// fakeEvents is the rail's Create, recording archived rows.
type fakeEvents struct {
	mu        sync.Mutex
	created   []securityevent.SecurityEvent
	createErr error
}

func (e *fakeEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.createErr != nil {
		return securityevent.SecurityEvent{}, e.createErr
	}
	e.created = append(e.created, evt)
	return evt, nil
}

func (e *fakeEvents) List(context.Context, securityevent.ListFilter, list.Request) (list.Page[securityevent.SecurityEvent], error) {
	return list.Page[securityevent.SecurityEvent]{}, nil
}

func (e *fakeEvents) archived() []securityevent.SecurityEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.created)
}

// fakeSink is an in-memory ArchiveSink with fault switches.
type fakeSink struct {
	mu        sync.Mutex
	objects   map[string][]byte
	preexist  map[string]bool
	uploadErr error
	corrupt   bool
	wrongSize bool
	uploads   []string
}

func newFakeSink() *fakeSink {
	return &fakeSink{objects: map[string][]byte{}, preexist: map[string]bool{}}
}

func (s *fakeSink) Upload(_ context.Context, path string, reader io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, path)
	if s.uploadErr != nil {
		return s.uploadErr
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.objects[path] = b
	return nil
}

func (s *fakeSink) Exists(_ context.Context, path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[path]
	return ok || s.preexist[path], nil
}

func (s *fakeSink) Download(_ context.Context, path string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[path]
	if !ok {
		return nil, sdk.ErrNotFound
	}
	if s.corrupt {
		b = bytes.Replace(b, []byte("evt-"), []byte("EVT-"), 1)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *fakeSink) GetObjectSize(_ context.Context, path string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[path]
	if !ok {
		return 0, sdk.ErrNotFound
	}
	if s.wrongSize {
		return int64(len(b)) + 1, nil
	}
	return int64(len(b)), nil
}

func (s *fakeSink) object(path string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[path]
}

// archivedIDs returns every row id held by an object whose archived row was
// recorded — the rows a purge may remove.
func archivedIDs(sink *fakeSink, events *fakeEvents) map[string]bool {
	ids := map[string]bool{}
	for _, evt := range events.archived() {
		key, _ := evt.Details["object"].(string)
		for _, line := range strings.Split(strings.TrimSpace(string(sink.object(key))), "\n") {
			if i := strings.Index(line, `"id":"`); i >= 0 {
				rest := line[i+len(`"id":"`):]
				ids[rest[:strings.IndexByte(rest, '"')]] = true
			}
		}
	}
	return ids
}

// testLogger captures every level.
func testLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTestService builds a Service over the fakes with a fake clock.
func newTestService(t *testing.T, repos Repositories, cfg Config, clock *fakeClock, opts ...Option) (*Service, *syncBuffer) {
	t.Helper()
	logger, buf := testLogger()
	svc, err := New(repos, cfg, append([]Option{WithLogger(logger), WithClock(clock.Now)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, buf
}

func stepOf(t *testing.T, rep Report, name StepName) Step {
	t.Helper()
	for _, st := range rep.Steps {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("report has no %s step: %+v", name, rep.Steps)
	return Step{}
}

var errBoom = errors.New("boom")
