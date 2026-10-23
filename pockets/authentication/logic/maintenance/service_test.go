package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestBoundsArePinned(t *testing.T) {
	if MinSecurityEventRetention != 366*24*time.Hour {
		t.Errorf("MinSecurityEventRetention = %v, want 366 days (the 365-day database floor plus one)", MinSecurityEventRetention)
	}
	if MaxBatchSize != 10000 {
		t.Errorf("MaxBatchSize = %d, want 10000 (the purge function's clamp)", MaxBatchSize)
	}
}

func TestConfigDefaultsAndRefusals(t *testing.T) {
	svc, err := New(Repositories{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got := svc.Config()
	want := Config{SessionRetention: 24 * time.Hour, OAuthStateRetention: time.Hour, GrantRetention: 24 * time.Hour,
		ContactChangeRetention: 24 * time.Hour, InvitationRetention: 90 * 24 * time.Hour, SecurityEventRetention: 400 * 24 * time.Hour,
		SecurityEventStepInterval: 24 * time.Hour, BatchSize: 1000, ArchiveChunkRows: 5000, PassBudget: 5 * time.Minute, ArchivePrefix: "security-events/"}
	if got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
	if _, err := New(Repositories{}, Config{SecurityEventRetention: MinSecurityEventRetention}); err != nil {
		t.Errorf("the minimum retention was refused: %v", err)
	}
	if _, err := New(Repositories{}, Config{BatchSize: MaxBatchSize}); err != nil {
		t.Errorf("MaxBatchSize was refused: %v", err)
	}
	for name, cfg := range map[string]Config{
		"session":        {SessionRetention: -1},
		"oauth state":    {OAuthStateRetention: -1},
		"grant":          {GrantRetention: -1},
		"contact change": {ContactChangeRetention: -1},
		"invitation":     {InvitationRetention: -1},
		"security event": {SecurityEventRetention: -1},
		"step interval":  {SecurityEventStepInterval: -1},
		"budget":         {PassBudget: -1},
		"batch negative": {BatchSize: -1},
		"batch above":    {BatchSize: MaxBatchSize + 1},
		"chunk":          {ArchiveChunkRows: -1},
		"colon prefix":   {ArchivePrefix: "c:/archive/"},
		"absolute":       {ArchivePrefix: "/archive/"},
		"dot segment":    {ArchivePrefix: "../archive/"},
	} {
		if _, err := New(Repositories{}, cfg); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	_, err = New(Repositories{}, Config{SecurityEventRetention: MinSecurityEventRetention - time.Second})
	if !errors.Is(err, ErrSecurityEventRetentionTooShort) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("below the minimum: err = %v, want ErrSecurityEventRetentionTooShort", err)
	}
	if _, err := New(Repositories{}, Config{}, WithInterval(-time.Second)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("negative interval: err = %v", err)
	}
	if _, err := New(Repositories{}, Config{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("nil option: err = %v", err)
	}
	var typedNil *fakeRepo
	if _, err := New(Repositories{Maintenance: typedNil}, Config{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("typed-nil repository: err = %v", err)
	}
}

func TestPassNamesEverySkippedStep(t *testing.T) {
	svc, logs := newTestService(t, Repositories{}, Config{}, newClock())
	rep, err := svc.Pass(context.Background())
	if err != nil {
		t.Fatalf("an all-skipped pass errored: %v", err)
	}
	want := []StepName{StepSessions, StepOAuthStates, StepGrants, StepContactChanges, StepInvitations, StepChallenges, StepOAuth2, StepSecurityEvents}
	if len(rep.Steps) != len(want) {
		t.Fatalf("steps = %+v", rep.Steps)
	}
	for i, st := range rep.Steps {
		if st.Name != want[i] || !st.Skipped || st.SkipReason == "" || st.Err != nil {
			t.Errorf("step %d = %+v, want %s skipped with a reason", i, st, want[i])
		}
	}
	if stepOf(t, rep, StepChallenges).SkipReason != "Repositories.Challenges is nil" || stepOf(t, rep, StepSessions).SkipReason != "Repositories.Maintenance is nil" {
		t.Errorf("skip reasons do not name the missing port: %+v", rep.Steps)
	}
	if !strings.Contains(logs.String(), `msg=maintenance_step step=sessions count=0 skipped=true error_kind=none skip_reason="Repositories.Maintenance is nil"`) {
		t.Errorf("the per-step line is not the stable shape:\n%s", logs)
	}

	events := &fakeEvents{}
	svc, _ = newTestService(t, Repositories{Maintenance: newFakeRepo()}, Config{}, newClock())
	rep, _ = svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); !st.Skipped || st.SkipReason != "Repositories.SecurityEvents is nil" {
		t.Errorf("rail step without the rail = %+v", st)
	}
	svc, _ = newTestService(t, Repositories{Maintenance: newFakeRepo(), SecurityEvents: events}, Config{}, newClock())
	rep, _ = svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); st.Skipped || st.Err != nil {
		t.Errorf("rail step with every port = %+v", st)
	}
}

func TestPassCutoffsAndDrain(t *testing.T) {
	repo := newFakeRepo()
	repo.eligible["sessions"] = 2500
	repo.eligible["invitations"] = 3
	repo.eligible["oauth2"] = 1000
	chal := &fakeChallenges{eligible: 7}
	svc, _ := newTestService(t, Repositories{Maintenance: repo, Challenges: chal, SecurityEvents: &fakeEvents{}}, Config{InvitationRetention: 48 * time.Hour}, newClock())
	rep, err := svc.Pass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for method, want := range map[string]time.Time{
		"sessions":        testNow.Add(-24 * time.Hour),
		"oauth_states":    testNow.Add(-time.Hour),
		"grants":          testNow.Add(-24 * time.Hour),
		"contact_changes": testNow.Add(-24 * time.Hour),
		"invitations":     testNow.Add(-48 * time.Hour),
		"count_stale":     testNow.Add(-48 * time.Hour),
		"oauth2":          testNow,
		"purge_rail":      testNow.Add(-400 * 24 * time.Hour),
	} {
		calls := repo.callsOf(method)
		if len(calls) == 0 || !calls[0].before.Equal(want) {
			t.Errorf("%s cutoff = %+v, want %v", method, calls, want)
		}
	}
	if len(chal.befores) != 1 || !chal.befores[0].Equal(testNow.Add(-time.Hour)) {
		t.Errorf("challenge cutoff = %v", chal.befores)
	}
	if calls := repo.callsOf("sessions"); len(calls) != 3 || calls[0].limit != 1000 {
		t.Errorf("sessions drained in %+v, want three 1000-row batches", calls)
	}
	if calls := repo.callsOf("oauth2"); len(calls) != 2 {
		t.Errorf("a full batch must be followed by another call: %+v", calls)
	}
	if got := stepOf(t, rep, StepSessions).Count; got != 2500 {
		t.Errorf("sessions count = %d", got)
	}
	if got := stepOf(t, rep, StepChallenges).Count; got != 7 {
		t.Errorf("challenges count = %d", got)
	}
	if rep.BudgetExhausted || rep.ArchiveBacklog {
		t.Errorf("a drained pass reported a backlog: %+v", rep)
	}
}

func TestFailedStepDoesNotStopThePass(t *testing.T) {
	repo := newFakeRepo()
	repo.errs["sessions"] = fmt.Errorf("permission denied for relation sessions: %w", sdk.ErrForbidden)
	repo.eligible["grants"] = 4
	svc, logs := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, newClock())
	rep, err := svc.Pass(context.Background())
	if err != nil {
		t.Fatalf("one failed step failed the pass: %v", err)
	}
	if st := stepOf(t, rep, StepSessions); !errors.Is(st.Err, sdk.ErrForbidden) {
		t.Errorf("sessions step = %+v", st)
	}
	if got := stepOf(t, rep, StepGrants).Count; got != 4 {
		t.Errorf("a later step did not run: grants count %d", got)
	}
	if !strings.Contains(logs.String(), "level=ERROR msg=maintenance_step step=sessions count=0 skipped=false error_kind=forbidden") {
		t.Errorf("the failed step line is missing:\n%s", logs)
	}
	if strings.Contains(logs.String(), "permission denied") {
		t.Errorf("the step line carries error text:\n%s", logs)
	}
}

func TestEveryStepFailingFailsThePass(t *testing.T) {
	repo := newFakeRepo()
	for _, m := range []string{"sessions", "oauth_states", "grants", "contact_changes", "invitations", "oauth2", "purge_rail"} {
		repo.errs[m] = errBoom
	}
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, newClock())
	if _, err := svc.Pass(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("every step failed but Pass returned %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, _ := newTestService(t, Repositories{Maintenance: newFakeRepo(), SecurityEvents: &fakeEvents{}}, Config{}, newClock())
	if _, err := ok.Pass(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("an ended context: Pass returned %v", err)
	}
}

func TestPanickingStepIsRecovered(t *testing.T) {
	repo := newFakeRepo()
	repo.panics["grants"] = true
	repo.eligible["oauth2"] = 2
	svc, logs := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, newClock())
	rep, err := svc.Pass(context.Background())
	if err != nil {
		t.Fatalf("a panicking step failed the pass: %v", err)
	}
	if st := stepOf(t, rep, StepGrants); !errors.Is(st.Err, errStepPanicked) {
		t.Errorf("grants step = %+v, want the recovered panic", st)
	}
	if stepOf(t, rep, StepOAuth2).Count != 2 {
		t.Error("the steps after the panic did not run")
	}
	out := logs.String()
	if !strings.Contains(out, "level=ERROR msg=maintenance_step_panicked step=grants") || !strings.Contains(out, "stack=") ||
		!strings.Contains(out, "step=grants count=0 skipped=false error_kind=panic") {
		t.Errorf("the panic is not logged with its stack and kind:\n%s", out)
	}
}

func TestRailStepRunsDailyInsideHourlyPasses(t *testing.T) {
	repo := newFakeRepo()
	clock := newClock()
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, clock)
	rep, _ := svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); st.Skipped {
		t.Fatalf("the first pass after start skipped the rail step: %+v", st)
	}
	clock.Advance(time.Hour)
	rep, _ = svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); !st.Skipped || st.SkipReason != "not_due" {
		t.Errorf("the rail step inside the interval = %+v, want not_due", st)
	}
	clock.Advance(23 * time.Hour)
	rep, _ = svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); st.Skipped {
		t.Errorf("the rail step a day later = %+v, want run", st)
	}
	if got := len(repo.callsOf("purge_rail")); got != 2 {
		t.Errorf("rail purge calls = %d, want one per due run", got)
	}
}

func TestUnarchivedRailPurgeLeavesEvidenceEveryRun(t *testing.T) {
	repo := newFakeRepo()
	repo.seedRail(1500, testNow.Add(-500*24*time.Hour))
	repo.seedRail(0, testNow)
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, newClock())
	rep, _ := svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); st.Count != 1500 || st.Err != nil {
		t.Errorf("rail step = %+v", st)
	}
	if len(repo.callsOf("export")) != 0 {
		t.Error("an unarchived purge exported rows")
	}
	if got := len(repo.purgeRows); got != 2 {
		t.Errorf("purge calls = %d, want 2 (1000 + a short 500)", got)
	}

	empty := newFakeRepo()
	sink, events := newFakeSink(), &fakeEvents{}
	svc, _ = newTestService(t, Repositories{Maintenance: empty, SecurityEvents: events}, Config{ArchiveSink: sink}, newClock())
	svc.Pass(context.Background())
	if calls := empty.callsOf("purge_rail"); len(calls) != 1 || !calls[0].before.Equal(testNow.Add(-400*24*time.Hour)) {
		t.Errorf("an empty archived run made purge calls %+v, want exactly one evidence call at the cutoff", calls)
	}
	if len(sink.uploads) != 0 || len(events.archived()) != 0 {
		t.Error("an empty run archived something")
	}
}

func TestArchiveThenPurgePerChunk(t *testing.T) {
	repo := newFakeRepo()
	old := testNow.Add(-450 * 24 * time.Hour)
	repo.seedRail(12, old)
	seq := int64(7)
	recorded := old.Add(time.Second)
	repo.rail[0].Seq, repo.rail[0].RecordedAt = &seq, &recorded
	repo.rail = append(repo.rail, ArchivedEvent{SecurityEvent: securityevent.SecurityEvent{ID: "fresh", EventType: securityevent.TypeLogin, CreatedAt: testNow.Add(-time.Hour)}})
	sink, events := newFakeSink(), &fakeEvents{}
	repo.allowPurge = func() map[string]bool { return archivedIDs(sink, events) }
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveSink: sink, ArchiveChunkRows: 5, BatchSize: 2, ArchiveLocation: "disk:/var/archive"}, newClock())
	rep, err := svc.Pass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st := stepOf(t, rep, StepSecurityEvents)
	if st.Err != nil || st.Count != 12 || rep.ArchiveBacklog {
		t.Fatalf("rail step = %+v backlog=%v", st, rep.ArchiveBacklog)
	}
	if len(repo.violations) != 0 {
		t.Fatalf("rows purged before their object verified: %v", repo.violations)
	}
	if repo.railLen() != 1 || repo.rail[0].ID != "fresh" {
		t.Errorf("rail after the pass = %+v, want only the row inside retention", repo.rail)
	}
	archived := events.archived()
	if len(sink.uploads) != 3 || len(archived) != 3 {
		t.Fatalf("objects = %v archived rows = %d, want three chunks (5, 5, 2)", sink.uploads, len(archived))
	}
	cutoff := testNow.Add(-400 * 24 * time.Hour)
	for i, evt := range archived {
		key := sink.uploads[i]
		if !strings.HasPrefix(key, "security-events/"+cutoff.Format("20060102T150405.000000Z")+"/") || !strings.HasSuffix(key, ".ndjson") || strings.Contains(key, ":") {
			t.Errorf("object key %q is not <prefix><cutoff>/<id>.ndjson without a colon", key)
		}
		if evt.EventType != securityevent.TypeSecurityEventsArchived || evt.EventStatus != securityevent.StatusSuccess || evt.Actor != (securityevent.Principal{Type: "system", ID: "maintenance"}) || evt.ID == "" {
			t.Errorf("archived row = %+v", evt)
		}
		d := evt.Details
		if d["object"] != key || d["archive_location"] != "disk:/var/archive" || d["sha256"] == "" || d["first_id"] == "" || d["last_id"] == "" || d["first_created_at"] == "" || d["last_created_at"] == "" {
			t.Errorf("archived Details = %v", d)
		}
	}
	if archived[0].Details["rows"] != 5 || archived[2].Details["rows"] != 2 {
		t.Errorf("rows per object = %v, %v", archived[0].Details["rows"], archived[2].Details["rows"])
	}
	lines := strings.Split(strings.TrimSpace(string(sink.object(sink.uploads[0]))), "\n")
	if len(lines) != 5 {
		t.Fatalf("first object has %d lines", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"v", "id", "seq", "recorded_at", "user_id", "actor_type", "actor_id", "event_type", "event_status", "details", "ip_address", "user_agent", "request_id", "trace_id", "created_at"} {
		if _, ok := first[key]; !ok {
			t.Errorf("archive line lacks %q: %s", key, lines[0])
		}
	}
	if first["v"] != float64(1) || first["seq"] != float64(7) || first["recorded_at"] != recorded.Format("2006-01-02T15:04:05.000000Z") || first["created_at"] != old.Format("2006-01-02T15:04:05.000000Z") {
		t.Errorf("archive line = %s", lines[0])
	}
	var second map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if second["seq"] != nil || second["recorded_at"] != nil {
		t.Errorf("absent evidence columns must be null: %s", lines[1])
	}
	for _, call := range repo.callsOf("purge_rail") {
		if call.limit != 2 {
			t.Errorf("purge call limit %d, want BatchSize", call.limit)
		}
	}
}

func TestChunkKeepsTiesWhole(t *testing.T) {
	repo := newFakeRepo()
	at := testNow.Add(-500 * 24 * time.Hour)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		repo.rail = append(repo.rail, ArchivedEvent{SecurityEvent: securityevent.SecurityEvent{ID: id, EventType: securityevent.TypeLogin, CreatedAt: at}})
	}
	repo.rail = append(repo.rail, ArchivedEvent{SecurityEvent: securityevent.SecurityEvent{ID: "z", EventType: securityevent.TypeLogin, CreatedAt: at.Add(time.Second)}})
	sink, events := newFakeSink(), &fakeEvents{}
	repo.allowPurge = func() map[string]bool { return archivedIDs(sink, events) }
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveSink: sink, ArchiveChunkRows: 2}, newClock())
	svc.Pass(context.Background())
	archived := events.archived()
	if len(archived) != 2 || archived[0].Details["rows"] != 5 || archived[1].Details["rows"] != 1 {
		t.Fatalf("archived = %+v, want the five tied rows in one object", archived)
	}
	if len(repo.violations) != 0 || repo.railLen() != 0 {
		t.Errorf("violations=%v rail=%d", repo.violations, repo.railLen())
	}
}

func TestArchiveFailuresNeverPurge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*fakeSink, *fakeEvents, *Service)
	}{
		{"upload fails", func(s *fakeSink, _ *fakeEvents, _ *Service) { s.uploadErr = errBoom }},
		{"read-back content differs", func(s *fakeSink, _ *fakeEvents, _ *Service) { s.corrupt = true }},
		{"read-back size differs", func(s *fakeSink, _ *fakeEvents, _ *Service) { s.wrongSize = true }},
		{"archived row write fails", func(_ *fakeSink, e *fakeEvents, _ *Service) { e.createErr = errBoom }},
		{"empty object key", func(_ *fakeSink, _ *fakeEvents, svc *Service) {
			svc.objectID = func() (string, error) { return "", nil }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.seedRail(8, testNow.Add(-500*24*time.Hour))
			sink, events := newFakeSink(), &fakeEvents{}
			svc, logs := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveChunkRows: 5}, newClock())
			tc.break_(sink, events, svc)
			svc.cfg.ArchiveSink = sink
			rep, err := svc.Pass(context.Background())
			if err != nil {
				t.Fatalf("one failed step failed the pass: %v", err)
			}
			st := stepOf(t, rep, StepSecurityEvents)
			if !errors.Is(st.Err, ErrArchiveFailed) || !rep.ArchiveBacklog {
				t.Errorf("rail step = %+v backlog=%v, want ErrArchiveFailed with a backlog", st, rep.ArchiveBacklog)
			}
			if len(repo.callsOf("purge_rail")) != 0 || repo.railLen() != 8 {
				t.Errorf("a chunk was purged after its archive failed: calls=%d rail=%d", len(repo.callsOf("purge_rail")), repo.railLen())
			}
			if !strings.Contains(logs.String(), "step=security_events count=0 skipped=false error_kind=archive_failed") {
				t.Errorf("the step line does not name archive_failed:\n%s", logs)
			}
			if tc.name == "empty object key" && len(sink.uploads) != 0 {
				t.Errorf("an empty key was uploaded: %v", sink.uploads)
			}
		})
	}
}

func TestObjectKeysNeverOverwrite(t *testing.T) {
	repo := newFakeRepo()
	repo.seedRail(3, testNow.Add(-500*24*time.Hour))
	sink, events := newFakeSink(), &fakeEvents{}
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveSink: sink}, newClock())
	ids := []string{"taken", "taken", "fresh", "row-id"}
	svc.objectID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	segment := "security-events/" + testNow.Add(-400*24*time.Hour).Format("20060102T150405.000000Z") + "/"
	sink.preexist[segment+"taken.ndjson"] = true
	rep, _ := svc.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); st.Err != nil {
		t.Fatal(st.Err)
	}
	if len(sink.uploads) != 1 || sink.uploads[0] != segment+"fresh.ndjson" {
		t.Errorf("uploads = %v, want the first unused key", sink.uploads)
	}

	stuck, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveSink: sink}, newClock())
	stuck.objectID = func() (string, error) { return "taken", nil }
	repo.seedRail(1, testNow.Add(-500*24*time.Hour))
	rep, _ = stuck.Pass(context.Background())
	if st := stepOf(t, rep, StepSecurityEvents); !errors.Is(st.Err, ErrArchiveFailed) {
		t.Errorf("an exhausted key search = %+v, want ErrArchiveFailed", st)
	}
}

func TestBacklogLargerThanOneBudget(t *testing.T) {
	repo := newFakeRepo()
	repo.seedRail(50000, testNow.Add(-800*24*time.Hour))
	clock := newClock()
	repo.onExport = func() { clock.Advance(300 * time.Millisecond) }
	sink, events := newFakeSink(), &fakeEvents{}
	repo.allowPurge = func() map[string]bool { return archivedIDs(sink, events) }
	svc, logs := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveSink: sink, PassBudget: time.Second}, clock)
	remaining := repo.railLen()
	passes := 0
	for remaining > 0 {
		passes++
		if passes > 10 {
			t.Fatalf("the backlog did not drain: %d rows left", remaining)
		}
		rep, err := svc.Pass(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		now := repo.railLen()
		if now >= remaining {
			t.Fatalf("pass %d made no progress (%d rows)", passes, now)
		}
		if (now > 0) != rep.BudgetExhausted || (now > 0) != rep.ArchiveBacklog {
			t.Errorf("pass %d: %d rows left, BudgetExhausted=%v ArchiveBacklog=%v", passes, now, rep.BudgetExhausted, rep.ArchiveBacklog)
		}
		remaining = now
		clock.Advance(time.Hour)
	}
	if passes < 2 {
		t.Fatalf("the backlog drained in %d pass, want several", passes)
	}
	if len(repo.violations) != 0 {
		t.Fatalf("%d rows purged before their object verified", len(repo.violations))
	}
	if got, want := strings.Count(logs.String(), "msg=maintenance_budget_exhausted"), passes-2; got != want {
		t.Errorf("persistent-budget WARNs = %d, want %d (one per pass the exhaustion persisted into)", got, want)
	}
}

func TestPurgeCountMismatchWarns(t *testing.T) {
	repo := newFakeRepo()
	repo.seedRail(3, testNow.Add(-500*24*time.Hour))
	repo.extraPurge = 1
	svc, logs := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{ArchiveSink: newFakeSink()}, newClock())
	svc.Pass(context.Background())
	if !strings.Contains(logs.String(), "level=WARN msg=archive_purge_count_mismatch archived=3 purged=4") {
		t.Errorf("no mismatch WARN:\n%s", logs)
	}
}

func TestStaleAcceptingClaimsWarnOncePerDayOrOnChange(t *testing.T) {
	repo := newFakeRepo()
	repo.stale = 2
	clock := newClock()
	svc, logs := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, clock)
	count := func() int { return strings.Count(logs.String(), "msg=invitations_accepting_stale") }
	rep, _ := svc.Pass(context.Background())
	if rep.StaleAcceptingClaims != 2 || count() != 1 {
		t.Fatalf("stale=%d warns=%d", rep.StaleAcceptingClaims, count())
	}
	if !strings.Contains(logs.String(), "count=2") || !strings.Contains(logs.String(), "maintenance never cancels it") {
		t.Errorf("the WARN does not name the count and the action:\n%s", logs)
	}
	clock.Advance(time.Hour)
	svc.Pass(context.Background())
	if count() != 1 {
		t.Errorf("an unchanged count warned again within a day: %d", count())
	}
	repo.stale = 3
	svc.Pass(context.Background())
	if count() != 2 {
		t.Errorf("a changed count did not warn: %d", count())
	}
	clock.Advance(24 * time.Hour)
	svc.Pass(context.Background())
	if count() != 3 {
		t.Errorf("a day later the unchanged count did not warn: %d", count())
	}
	repo.stale = 0
	svc.Pass(context.Background())
	if count() != 3 {
		t.Errorf("a zero count warned: %d", count())
	}
}

func TestConcurrentPassesAreSafe(t *testing.T) {
	repo := newFakeRepo()
	repo.eligible["sessions"] = 5000
	repo.seedRail(3000, testNow.Add(-500*24*time.Hour))
	sink, events := newFakeSink(), &fakeEvents{}
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: events}, Config{ArchiveSink: sink, ArchiveChunkRows: 100, BatchSize: 50}, newClock())
	var wg sync.WaitGroup
	reports := make([]Report, 2)
	for i := range reports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reports[i], _ = svc.Pass(context.Background())
		}()
	}
	wg.Wait()
	total := 0
	for _, rep := range reports {
		total += stepOf(t, rep, StepSessions).Count
	}
	if total != 5000 || repo.eligible["sessions"] != 0 {
		t.Errorf("concurrent sessions purged %d of 5000", total)
	}
	if repo.railLen() != 0 {
		t.Errorf("%d rail rows left after two concurrent passes", repo.railLen())
	}
	if _, ok := svc.LastReport(); !ok {
		t.Error("no LastReport after two passes")
	}
}

func TestLastReport(t *testing.T) {
	repo := newFakeRepo()
	repo.eligible["sessions"] = 1
	svc, _ := newTestService(t, Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, newClock())
	if _, ok := svc.LastReport(); ok {
		t.Fatal("LastReport before any pass")
	}
	rep, _ := svc.Pass(context.Background())
	got, ok := svc.LastReport()
	if !ok || !got.StartedAt.Equal(rep.StartedAt) || len(got.Steps) != len(rep.Steps) || stepOf(t, got, StepSessions).Count != 1 {
		t.Fatalf("LastReport = %+v, want the pass %+v", got, rep)
	}
	got.Steps[0].Count = 99
	again, _ := svc.LastReport()
	if again.Steps[0].Count == 99 {
		t.Error("LastReport shares its Steps with callers")
	}
	repo.eligible["sessions"] = 2
	svc.Pass(context.Background())
	if latest, _ := svc.LastReport(); stepOf(t, latest, StepSessions).Count != 2 {
		t.Error("LastReport is not the latest pass")
	}
}

func TestRunPassesAtStartAndEveryInterval(t *testing.T) {
	repo := newFakeRepo()
	repo.panics["oauth_states"] = true
	logger, _ := testLogger()
	svc, err := New(Repositories{Maintenance: repo, SecurityEvents: &fakeEvents{}}, Config{}, WithLogger(logger), WithInterval(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(repo.callsOf("sessions")) < 3 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("Run made %d passes in 5s", len(repo.callsOf("sessions")))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rep, ok := svc.LastReport(); !ok || !errors.Is(stepOf(t, rep, StepOAuthStates).Err, errStepPanicked) {
		t.Errorf("Run did not survive the panicking step: %+v", rep)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v on cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestStartJitterIsAtMostATenth(t *testing.T) {
	for range 1000 {
		if j := startJitter(time.Hour); j < 0 || j > 6*time.Minute {
			t.Fatalf("jitter %v outside [0, 6m]", j)
		}
	}
	if startJitter(5*time.Nanosecond) != 0 {
		t.Error("a sub-tenth interval must not jitter")
	}
}
