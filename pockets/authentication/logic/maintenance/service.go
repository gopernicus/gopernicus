package maintenance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// The steps of a pass, in the order a pass runs them. The names are stable:
// hosts alert on them and every pass logs one line per step.
const (
	StepSessions       StepName = "sessions"
	StepOAuthStates    StepName = "oauth_states"
	StepGrants         StepName = "grants"
	StepContactChanges StepName = "contact_changes"
	StepInvitations    StepName = "invitations"
	StepChallenges     StepName = "challenges"
	StepOAuth2         StepName = "oauth2"
	StepSecurityEvents StepName = "security_events"
)

const (
	// skipNoMaintenance names a step whose port is Repositories.Maintenance.
	skipNoMaintenance = "Repositories.Maintenance is nil"
	// skipNoChallenges names the challenge step without its port.
	skipNoChallenges = "Repositories.Challenges is nil"
	// skipNoSecurityEvents names the rail step without the rail.
	skipNoSecurityEvents = "Repositories.SecurityEvents is nil"
	// skipNotDue names the rail step inside SecurityEventStepInterval.
	skipNotDue = "not_due"

	// staleWarnInterval is the least time between two identical stale-claim WARNs.
	staleWarnInterval = 24 * time.Hour
	// keyAttempts bounds the search for an unused archive object key.
	keyAttempts = 5
)

var (
	// systemActor attributes the rows maintenance writes.
	systemActor = securityevent.Principal{Type: "system", ID: "maintenance"}
	// errStepPanicked marks a step that panicked and was recovered.
	errStepPanicked = errors.New("maintenance: step panicked")
)

// StepName names one step of a pass.
type StepName string

// Repositories are the ports a Service runs over. Maintenance backs every step
// but the challenge purge; Challenges backs that one; SecurityEvents receives
// the archive evidence and must be the rail Maintenance purges. A nil port
// skips the steps that need it, named in every Report.
type Repositories struct {
	Maintenance    Repository
	Challenges     challenge.Repository
	SecurityEvents securityevent.SecurityEventRepository
}

// Option configures New.
type Option func(*options)

type options struct {
	logger   *slog.Logger
	clock    func() time.Time
	interval time.Duration
	// rowIDs mints the archive marker row's security_events.id, a record-class
	// id; the zero value selects UUIDv7.
	rowIDs sdk.IDGenerator
}

// Step is one step's outcome in a Report. Count is the rows the step disposed
// of; a skipped step names why; Err is the step's failure, nil on success.
type Step struct {
	Name       StepName
	Count      int
	Skipped    bool
	SkipReason string
	Err        error
}

// Report is one pass. StaleAcceptingClaims counts accepting invitations older
// than InvitationRetention. BudgetExhausted is set when PassBudget stopped a
// step with eligible rows left; ArchiveBacklog when the rail step ended with
// rows past its cutoff still on the rail. The next pass resumes both.
type Report struct {
	StartedAt            time.Time
	FinishedAt           time.Time
	Steps                []Step
	StaleAcceptingClaims int
	BudgetExhausted      bool
	ArchiveBacklog       bool
}

// Service runs retention passes. It is safe for concurrent use: two concurrent
// passes are correct (every statement is bounded and idempotent) but not cheap,
// so one runner instance is the documented posture.
type Service struct {
	repo     Repository
	chal     challenge.Repository
	events   securityevent.SecurityEventRepository
	cfg      Config
	log      *slog.Logger
	now      func() time.Time
	interval time.Duration
	// objectID mints archive object keys (secrets); rowID mints the archive
	// marker row's record id.
	objectID func() (string, error)
	rowID    func() (string, error)

	mu             sync.Mutex
	railRanAt      time.Time
	lastExhausted  bool
	staleWarnedAt  time.Time
	staleWarnCount int

	last atomic.Pointer[Report]
}

// WithLogger sets the logger; nil selects slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithClock sets the clock every cutoff, budget and cadence is measured on;
// nil selects time.Now. A store measures its retention floor on its own clock.
func WithClock(clock func() time.Time) Option {
	return func(o *options) { o.clock = clock }
}

// WithInterval sets Run's period. Zero selects one hour; negative is
// sdk.ErrInvalidInput.
func WithInterval(d time.Duration) Option {
	return func(o *options) { o.interval = d }
}

// WithIDs sets the entity-ID strategy for the archive marker row's
// security_events.id; the zero value selects UUIDv7. It never reaches the
// archive object keys, which are secrets.
func WithIDs(ids sdk.IDGenerator) Option {
	return func(o *options) { o.rowIDs = ids }
}

// New builds a Service over repos with cfg's policy, defaults applied. It
// starts nothing: the host calls Pass or Run.
func New(repos Repositories, cfg Config, opts ...Option) (*Service, error) {
	o := options{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("maintenance: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&o)
	}
	resolved, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	for _, dep := range []struct {
		name  string
		value any
	}{{"Maintenance", repos.Maintenance}, {"Challenges", repos.Challenges}, {"SecurityEvents", repos.SecurityEvents}, {"ArchiveSink", resolved.ArchiveSink}} {
		if typedNil(dep.value) {
			return nil, fmt.Errorf("maintenance: %s contains a typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	if o.interval < 0 {
		return nil, fmt.Errorf("maintenance: negative interval: %w", sdk.ErrInvalidInput)
	}
	if o.interval == 0 {
		o.interval = defaultInterval
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	keys := sdk.IDGenerator{}
	return &Service{
		repo:     repos.Maintenance,
		chal:     repos.Challenges,
		events:   repos.SecurityEvents,
		cfg:      resolved,
		log:      o.logger,
		now:      o.clock,
		interval: o.interval,
		objectID: keys.Generate,
		rowID:    o.rowIDs.Or(sdk.UUIDv7()).Generate,
	}, nil
}

// Config returns the policy the Service runs, defaults applied.
func (s *Service) Config() Config { return s.cfg }

// LastReport returns the most recent pass's Report, and false before the
// first pass completes.
func (s *Service) LastReport() (Report, bool) {
	r := s.last.Load()
	if r == nil {
		return Report{}, false
	}
	out := *r
	out.Steps = append([]Step(nil), r.Steps...)
	return out, true
}

// Pass runs one bounded pass: every step in order, each draining batches until
// a short batch or PassBudget is spent. A failed step is reported and the pass
// continues; a panicking step is recovered, logged with its stack and reported
// failed. Each step logs one stable line (step, count, skipped, error_kind).
//
// The error is nil unless ctx ended or EVERY step that ran failed, so a
// schedule retrying on error is not dead-lettered by one failing step; the
// Report carries every step's error either way.
func (s *Service) Pass(ctx context.Context) (Report, error) {
	start := s.now().UTC()
	deadline := start.Add(s.cfg.PassBudget)
	rep := Report{StartedAt: start}

	type plain struct {
		name   StepName
		retain time.Duration
		purge  func(context.Context, time.Time, int) (int, error)
	}
	var steps []plain
	if s.repo != nil {
		steps = []plain{
			{StepSessions, s.cfg.SessionRetention, s.repo.PurgeExpiredSessions},
			{StepOAuthStates, s.cfg.OAuthStateRetention, s.repo.PurgeExpiredOAuthStates},
			{StepGrants, s.cfg.GrantRetention, s.repo.PurgeExpiredGrants},
			{StepContactChanges, s.cfg.ContactChangeRetention, s.repo.PurgeExpiredContactChanges},
		}
	}
	for _, name := range []StepName{StepSessions, StepOAuthStates, StepGrants, StepContactChanges} {
		if s.repo == nil {
			s.record(ctx, &rep, Step{Name: name, Skipped: true, SkipReason: skipNoMaintenance}, false)
		}
	}
	for _, st := range steps {
		before := start.Add(-st.retain)
		step, exhausted := s.runStep(ctx, st.name, func() (int, bool, error) {
			return s.drain(ctx, deadline, func(limit int) (int, error) { return st.purge(ctx, before, limit) })
		})
		s.record(ctx, &rep, step, exhausted)
	}

	if s.repo == nil {
		s.record(ctx, &rep, Step{Name: StepInvitations, Skipped: true, SkipReason: skipNoMaintenance}, false)
	} else {
		before := start.Add(-s.cfg.InvitationRetention)
		step, exhausted := s.runStep(ctx, StepInvitations, func() (int, bool, error) {
			n, exhausted, err := s.drain(ctx, deadline, func(limit int) (int, error) { return s.repo.PurgeInvitations(ctx, before, limit) })
			stale, countErr := s.repo.CountStaleAcceptingInvitations(ctx, before)
			if countErr == nil {
				rep.StaleAcceptingClaims = stale
			}
			return n, exhausted, errors.Join(err, countErr)
		})
		s.record(ctx, &rep, step, exhausted)
		s.warnStale(ctx, rep.StaleAcceptingClaims, start)
	}

	if s.chal == nil {
		s.record(ctx, &rep, Step{Name: StepChallenges, Skipped: true, SkipReason: skipNoChallenges}, false)
	} else {
		before := start.Add(-challengeRetention)
		step, exhausted := s.runStep(ctx, StepChallenges, func() (int, bool, error) {
			return s.drain(ctx, deadline, func(limit int) (int, error) { return s.chal.PurgeExpired(ctx, before, limit) })
		})
		s.record(ctx, &rep, step, exhausted)
	}

	if s.repo == nil {
		s.record(ctx, &rep, Step{Name: StepOAuth2, Skipped: true, SkipReason: skipNoMaintenance}, false)
	} else {
		step, exhausted := s.runStep(ctx, StepOAuth2, func() (int, bool, error) {
			return s.drain(ctx, deadline, func(limit int) (int, error) { return s.repo.PruneOAuth2(ctx, start, limit) })
		})
		s.record(ctx, &rep, step, exhausted)
	}

	switch {
	case s.repo == nil:
		s.record(ctx, &rep, Step{Name: StepSecurityEvents, Skipped: true, SkipReason: skipNoMaintenance}, false)
	case s.events == nil:
		s.record(ctx, &rep, Step{Name: StepSecurityEvents, Skipped: true, SkipReason: skipNoSecurityEvents}, false)
	case !s.railDue(start):
		s.record(ctx, &rep, Step{Name: StepSecurityEvents, Skipped: true, SkipReason: skipNotDue}, false)
	default:
		var backlog bool
		step, exhausted := s.runStep(ctx, StepSecurityEvents, func() (int, bool, error) {
			n, exhausted, more, err := s.railStep(ctx, start, deadline)
			backlog = more
			return n, exhausted, err
		})
		rep.ArchiveBacklog = backlog
		if step.Err == nil && !backlog {
			s.mu.Lock()
			s.railRanAt = start
			s.mu.Unlock()
		}
		s.record(ctx, &rep, step, exhausted)
	}

	rep.FinishedAt = s.now().UTC()
	s.finish(ctx, &rep)
	return rep, passError(ctx, rep)
}

// runStep runs one step, recovering a panic into the step's error.
func (s *Service) runStep(ctx context.Context, name StepName, fn func() (int, bool, error)) (step Step, exhausted bool) {
	step.Name = name
	defer func() {
		if r := recover(); r != nil {
			step.Err = fmt.Errorf("%w: %s: %v", errStepPanicked, name, r)
			exhausted = false
			s.log.ErrorContext(ctx, "maintenance_step_panicked", "step", string(name), "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	if err := ctx.Err(); err != nil {
		step.Err = err
		return step, false
	}
	step.Count, exhausted, step.Err = fn()
	return step, exhausted
}

// drain calls purge with BatchSize until a short batch, an error, or the
// budget deadline passes after a batch — so every step makes progress on
// every pass. exhausted reports the deadline stopping a full batch.
func (s *Service) drain(ctx context.Context, deadline time.Time, purge func(limit int) (int, error)) (total int, exhausted bool, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return total, false, err
		}
		n, err := purge(s.cfg.BatchSize)
		total += n
		if err != nil {
			return total, false, err
		}
		if n < s.cfg.BatchSize {
			return total, false, nil
		}
		if !s.now().Before(deadline) {
			return total, true, nil
		}
	}
}

// railDue reports whether the rail step runs this pass: always on the first
// pass in this process, then once SecurityEventStepInterval has passed since it
// last completed with nothing left behind.
func (s *Service) railDue(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.railRanAt.IsZero() || !now.Before(s.railRanAt.Add(s.cfg.SecurityEventStepInterval))
}

// railStep purges the rail through now − SecurityEventRetention. With a sink it
// works one chunk at a time: export, upload, read back, record the archived
// row, then purge through the chunk's last instant — any failure before the
// purge leaves the chunk on the rail. Without a sink it purges unarchived.
// Either way it makes at least one purge call, whose evidence row shows the
// control ran on a day with nothing to purge. backlog reports rows past the
// cutoff left behind.
func (s *Service) railStep(ctx context.Context, start, deadline time.Time) (count int, exhausted, backlog bool, err error) {
	cutoff := start.Add(-s.cfg.SecurityEventRetention).Truncate(time.Microsecond)
	if s.cfg.ArchiveSink == nil {
		n, exhausted, err := s.drain(ctx, deadline, func(limit int) (int, error) {
			res, err := s.repo.PurgeSecurityEventsBefore(ctx, cutoff, limit)
			return res.Count, err
		})
		return n, exhausted, exhausted, err
	}
	cursor := ExportCursor{}
	for first := true; ; first = false {
		if err := ctx.Err(); err != nil {
			return count, false, true, err
		}
		rows, next, err := s.repo.ExportSecurityEvents(ctx, cutoff, cursor, s.cfg.ArchiveChunkRows)
		if err != nil {
			return count, false, true, err
		}
		if len(rows) == 0 {
			if first {
				res, err := s.repo.PurgeSecurityEventsBefore(ctx, cutoff, s.cfg.BatchSize)
				count += res.Count
				if err != nil {
					return count, false, false, err
				}
				s.checkPurged(ctx, 0, res.Count)
			}
			return count, false, false, nil
		}
		if err := s.archive(ctx, cutoff, rows); err != nil {
			return count, false, true, fmt.Errorf("%w: %w", ErrArchiveFailed, err)
		}
		purged, err := s.purgeThrough(ctx, rows[len(rows)-1].CreatedAt)
		count += purged
		if err != nil {
			return count, false, true, err
		}
		s.checkPurged(ctx, len(rows), purged)
		cursor = next
		if len(rows) < s.cfg.ArchiveChunkRows {
			return count, false, false, nil
		}
		if !s.now().Before(deadline) {
			return count, true, true, nil
		}
	}
}

// archive writes one verified object for rows and records it on the rail.
func (s *Service) archive(ctx context.Context, cutoff time.Time, rows []ArchivedEvent) error {
	enc, err := encodeArchive(rows)
	if err != nil {
		return err
	}
	key, err := s.objectKey(ctx, cutoff)
	if err != nil {
		return err
	}
	if err := s.cfg.ArchiveSink.Upload(ctx, key, bytes.NewReader(enc.body)); err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	if err := verifyArchive(ctx, s.cfg.ArchiveSink, key, enc); err != nil {
		return err
	}
	id, err := s.rowID()
	if err != nil {
		return fmt.Errorf("archived row id: %w", err)
	}
	evt := archivedEvent(id, s.now(), archivedDetails(key, s.cfg.ArchiveLocation, enc, rows))
	if _, err := s.events.Create(ctx, evt); err != nil {
		return fmt.Errorf("record archived row for %s: %w", key, err)
	}
	return nil
}

// objectKey returns an unused object key under the cutoff's segment. A key
// that exists is never reused — Upload would replace it — and an empty id is
// refused rather than yielding "<prefix><cutoff>/.ndjson" for every chunk.
func (s *Service) objectKey(ctx context.Context, cutoff time.Time) (string, error) {
	segment := s.cfg.ArchivePrefix + cutoff.UTC().Format(archiveKeyTimeLayout) + "/"
	for range keyAttempts {
		id, err := s.objectID()
		if err != nil {
			return "", fmt.Errorf("object key: %w", err)
		}
		if id == "" {
			return "", fmt.Errorf("object key: empty id refused: %w", sdk.ErrInvalidInput)
		}
		key := segment + id + ".ndjson"
		exists, err := s.cfg.ArchiveSink.Exists(ctx, key)
		if err != nil {
			return "", fmt.Errorf("object key %s: %w", key, err)
		}
		if !exists {
			return key, nil
		}
	}
	return "", fmt.Errorf("object key: no unused key under %s after %d attempts", segment, keyAttempts)
}

// purgeThrough purges the rail through last until a short batch.
func (s *Service) purgeThrough(ctx context.Context, last time.Time) (int, error) {
	total := 0
	for {
		res, err := s.repo.PurgeSecurityEventsBefore(ctx, last, s.cfg.BatchSize)
		total += res.Count
		if err != nil || res.Count < s.cfg.BatchSize {
			return total, err
		}
	}
}

// checkPurged warns when a chunk's purge removed a different number of rows
// than it archived: a row with an old application-supplied created_at arrived
// between the export and the purge, or a concurrent pass took some.
func (s *Service) checkPurged(ctx context.Context, archived, purged int) {
	if archived != purged {
		s.log.WarnContext(ctx, "archive_purge_count_mismatch", "archived", archived, "purged", purged)
	}
}

// warnStale logs the stale-claim WARN when the count is non-zero and changed,
// or at most once per staleWarnInterval otherwise.
func (s *Service) warnStale(ctx context.Context, count int, now time.Time) {
	s.mu.Lock()
	warn := count > 0 && (count != s.staleWarnCount || !now.Before(s.staleWarnedAt.Add(staleWarnInterval)))
	if warn {
		s.staleWarnedAt = now
	}
	s.staleWarnCount = count
	s.mu.Unlock()
	if warn {
		s.log.WarnContext(ctx, "invitations_accepting_stale", "count", count,
			"action", "settle or cancel the claim through the host's grant path; maintenance never cancels it")
	}
}

// record logs one step line and appends the step to rep.
func (s *Service) record(ctx context.Context, rep *Report, step Step, exhausted bool) {
	rep.Steps = append(rep.Steps, step)
	if exhausted {
		rep.BudgetExhausted = true
	}
	attrs := []any{"step", string(step.Name), "count", step.Count, "skipped", step.Skipped, "error_kind", errorKind(step.Err)}
	if step.Skipped {
		attrs = append(attrs, "skip_reason", step.SkipReason)
	}
	if step.Err != nil {
		s.log.ErrorContext(ctx, "maintenance_step", attrs...)
		return
	}
	s.log.InfoContext(ctx, "maintenance_step", attrs...)
}

// finish publishes rep, logs the pass line and the persistent-budget WARN.
func (s *Service) finish(ctx context.Context, rep *Report) {
	snapshot := *rep
	snapshot.Steps = append([]Step(nil), rep.Steps...)
	s.last.Store(&snapshot)

	s.mu.Lock()
	persistent := rep.BudgetExhausted && s.lastExhausted
	s.lastExhausted = rep.BudgetExhausted
	s.mu.Unlock()
	if persistent {
		s.log.WarnContext(ctx, "maintenance_budget_exhausted",
			"advice", "first enablement drains the backlog over many passes; size PassBudget; watch WAL and replica lag")
	}
	failed := 0
	for _, st := range rep.Steps {
		if st.Err != nil {
			failed++
		}
	}
	s.log.InfoContext(ctx, "maintenance_pass", "duration", rep.FinishedAt.Sub(rep.StartedAt), "steps_failed", failed,
		"budget_exhausted", rep.BudgetExhausted, "archive_backlog", rep.ArchiveBacklog, "stale_accepting_claims", rep.StaleAcceptingClaims)
}

// passError is nil unless ctx ended or every step that ran failed.
func passError(ctx context.Context, rep Report) error {
	ran, failed := 0, 0
	var errs []error
	for _, st := range rep.Steps {
		if st.Skipped {
			continue
		}
		ran++
		if st.Err != nil {
			failed++
			errs = append(errs, fmt.Errorf("%s: %w", st.Name, st.Err))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(append([]error{err}, errs...)...)
	}
	if ran > 0 && failed == ran {
		return errors.Join(errs...)
	}
	return nil
}

// errorKind reduces err to a stable, text-free label for the step line.
func errorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, errStepPanicked):
		return "panic"
	case errors.Is(err, ErrArchiveFailed):
		return "archive_failed"
	case errors.Is(err, ErrBelowRetentionFloor):
		return "below_floor"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, sdk.ErrForbidden):
		return "forbidden"
	case errors.Is(err, sdk.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, sdk.ErrNotFound):
		return "not_found"
	case errors.Is(err, sdk.ErrConflict):
		return "conflict"
	case errors.Is(err, sdk.ErrUnavailable):
		return "unavailable"
	default:
		return "other"
	}
}

// typedNil reports a non-nil interface holding a nil pointer, map, slice,
// channel or func.
func typedNil(value any) bool {
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
