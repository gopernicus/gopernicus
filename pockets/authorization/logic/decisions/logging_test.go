package decisions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

type loggedDecision struct {
	level       slog.Level
	message     string
	attrs       map[string]any
	correlation any
}
type decisionLogHandler struct {
	mu      sync.Mutex
	enabled bool
	records []loggedDecision
}
type logCorrelation struct{}

func (h *decisionLogHandler) Enabled(context.Context, slog.Level) bool { return h.enabled }
func (h *decisionLogHandler) Handle(ctx context.Context, r slog.Record) error {
	entry := loggedDecision{level: r.Level, message: r.Message, attrs: map[string]any{}, correlation: ctx.Value(logCorrelation{})}
	r.Attrs(func(a slog.Attr) bool { entry.attrs[a.Key] = a.Value.Any(); return true })
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, entry)
	return nil
}
func (h *decisionLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *decisionLogHandler) WithGroup(string) slog.Handler      { return h }
func (h *decisionLogHandler) entries() []loggedDecision {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]loggedDecision(nil), h.records...)
}
func assertDecisionLog(t *testing.T, h *decisionLogHandler, operation, outcome string, bound bool) loggedDecision {
	t.Helper()
	entries := h.entries()
	if len(entries) != 1 {
		t.Fatalf("%s emitted %d records: %+v", operation, len(entries), entries)
	}
	got := entries[0]
	if got.level != slog.LevelDebug || got.message != "authorization decision" || got.attrs["operation"] != operation || got.attrs["outcome"] != outcome || got.attrs["bound"] != bound {
		t.Fatalf("record=%+v", got)
	}
	if _, ok := got.attrs["duration"]; !ok {
		t.Fatal("missing elapsed duration")
	}
	for _, key := range []string{"expression", "tuples", "ids", "results", "trace", "error", "reason", "cursor"} {
		if _, ok := got.attrs[key]; ok {
			t.Fatalf("unbounded %q metadata=%+v", key, got)
		}
	}
	return got
}
func loggerService(t *testing.T, h *decisionLogHandler) (*Service, *tupleFixture) {
	t.Helper()
	_, f := expressionService(t, exact(tuples.On("doc", "a"), "viewer"), exact(tuples.Global(), "admin"))
	m := Model{ResourceTypes: map[string]ResourceTypeDef{"doc": {Relations: map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]Expression{"read": Direct("viewer"), "all": Role("admin"), "exact": RoleIn("viewer")}}}}
	s, err := NewService(f, WithModel(m), WithLogger(slog.New(h)))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}
func TestDecisionLoggingPublicOperations(t *testing.T) {
	req := authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "read", Resource: authmodel.Resource{Type: "doc", ID: "a"}}
	missing := req
	missing.Resource.ID = "absent"
	page := authmodel.LookupRequest{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Limit: 2}
	allow := func(result authmodel.CheckResult, err error) error {
		if err == nil && !result.Allowed {
			return errors.New("expected grant")
		}
		return err
	}
	batch := func(results []authmodel.CheckResult, err error) error {
		if err == nil && (len(results) != 2 || !results[0].Allowed || results[1].Allowed) {
			return errors.New("wrong batch decisions")
		}
		return err
	}
	lookup := func(result authmodel.LookupResult, err error) error {
		if err == nil && !reflect.DeepEqual(result.IDs, []string{"a"}) {
			return errors.New("wrong lookup IDs")
		}
		return err
	}
	for _, tc := range []struct {
		name, outcome string
		bound         bool
		call          func(context.Context, *Service, *tupleFixture) error
	}{
		{"Check", "allowed", false, func(ctx context.Context, s *Service, _ *tupleFixture) error { return allow(s.Check(ctx, req)) }},
		{"EvaluateWith", "allowed", true, func(ctx context.Context, s *Service, f *tupleFixture) error {
			return allow(s.EvaluateWith(ctx, f, req))
		}},
		{"CheckWith", "allowed", true, func(ctx context.Context, s *Service, f *tupleFixture) error { return allow(s.CheckWith(ctx, f, req)) }},
		{"CheckBatch", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			return batch(s.CheckBatch(ctx, []authmodel.CheckRequest{req, missing}))
		}},
		{"CheckBatchWith", "complete", true, func(ctx context.Context, s *Service, f *tupleFixture) error {
			return batch(s.CheckBatchWith(ctx, f, []authmodel.CheckRequest{req, missing}))
		}},
		{"FilterAuthorized", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			ids, err := s.FilterAuthorized(ctx, expressionPrincipal, "read", "doc", []string{"a", "absent", "a"})
			if err == nil && !reflect.DeepEqual(ids, []string{"a", "a"}) {
				t.Fatalf("filter=%v", ids)
			}
			return err
		}},
		{"CheckExplain", "allowed", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			result, trace, err := s.CheckExplain(ctx, req)
			if len(trace.Steps) == 0 {
				t.Fatal("explain lost trace")
			}
			return allow(result, err)
		}},
		{"CheckExplainWith", "allowed", true, func(ctx context.Context, s *Service, f *tupleFixture) error {
			result, trace, err := s.CheckExplainWith(ctx, f, req)
			if len(trace.Steps) == 0 {
				t.Fatal("bound explain lost trace")
			}
			return allow(result, err)
		}},
		{"Evaluate", "allowed", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			return allow(s.Evaluate(ctx, expressionPrincipal, Role("admin")))
		}},
		{"EvaluateResolved", "allowed", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			return allow(s.EvaluateResolved(ctx, expressionPrincipal, BindResource("doc", "target", Permission("read")), func(context.Context, string) (authmodel.Resource, error) { return req.Resource, nil }))
		}},
		{"EvaluateExpressionWith", "allowed", true, func(ctx context.Context, s *Service, f *tupleFixture) error {
			return allow(s.EvaluateExpressionWith(ctx, f, expressionPrincipal, Role("admin")))
		}},
		{"LookupResources", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			return lookup(s.LookupResources(ctx, expressionPrincipal, "read", "doc"))
		}},
		{"LookupResourcesPage", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			return lookup(s.LookupResourcesPage(ctx, expressionPrincipal, "read", "doc", "", 2))
		}},
		{"LookupResourcesIn", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			return lookup(s.LookupResourcesIn(ctx, page))
		}},
		{"LookupAllResourceIDs", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			result, err := s.LookupAllResourceIDs(ctx, expressionPrincipal, "read", "doc")
			if err == nil && !reflect.DeepEqual(result.IDs, []string{"a"}) {
				t.Fatalf("set=%+v", result)
			}
			return err
		}},
		{"LookupResourceIDPage", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			result, err := s.LookupResourceIDPage(ctx, page)
			if err == nil && !reflect.DeepEqual(result.IDs, []string{"a"}) {
				t.Fatalf("page=%+v", result)
			}
			return err
		}},
		{"FilterPage", "complete", false, func(ctx context.Context, s *Service, _ *tupleFixture) error {
			pulls := 0
			result, err := FilterPage(ctx, s, FilterPageRequest[string]{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Limit: 1, ID: func(id string) string { return id }, Source: func(context.Context, string, int) (CandidatePage[string], error) {
				pulls++
				id := "absent"
				if pulls == 2 {
					id = "a"
				}
				return CandidatePage[string]{Items: []Candidate[string]{{Item: id, NextCursor: id}}, HasMore: pulls < 2}, nil
			}})
			if err == nil && (pulls != 2 || !reflect.DeepEqual(result.Items, []string{"a"})) {
				t.Fatalf("filtered page=%+v pulls=%d", result, pulls)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &decisionLogHandler{enabled: true}
			s, f := loggerService(t, h)
			ctx := context.WithValue(t.Context(), logCorrelation{}, "request")
			if err := tc.call(ctx, s, f); err != nil {
				t.Fatal(err)
			}
			entry := assertDecisionLog(t, h, tc.name, tc.outcome, tc.bound)
			if entry.correlation != "request" {
				t.Fatal("host correlation context lost")
			}
			if tc.name == "CheckBatch" || tc.name == "CheckBatchWith" {
				if entry.attrs["request_count"] != int64(2) || entry.attrs["allowed_count"] != int64(1) || entry.attrs["denied_count"] != int64(1) {
					t.Fatalf("batch counts=%+v", entry)
				}
			}
		})
	}
}

func TestDecisionLoggingReadFreeAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, operation, outcome, kind string
		call                           func(context.Context, *Service, *tupleFixture) error
		reads                          bool
	}{
		{"no model", "Check", "denied", "", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			r, e := s.Check(ctx, authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "unknown", Resource: authmodel.Resource{Type: "doc", ID: "a"}})
			if r.Allowed {
				t.Fatal("unknown allowed")
			}
			return e
		}, false},
		{"empty batch", "CheckBatch", "complete", "", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			r, e := s.CheckBatch(ctx, nil)
			if r != nil {
				t.Fatal("nonempty batch")
			}
			return e
		}, false},
		{"empty filter", "FilterAuthorized", "complete", "", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			r, e := s.FilterAuthorized(ctx, expressionPrincipal, "read", "doc", nil)
			if r != nil {
				t.Fatal("nonempty filter")
			}
			return e
		}, false},
		{"validation", "Evaluate", "error", "invalid_input", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			_, e := s.Evaluate(ctx, expressionPrincipal, All())
			return e
		}, false},
		{"invalid bound reader", "CheckWith", "error", "invalid_input", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			_, e := s.CheckWith(ctx, nil, authmodel.CheckRequest{})
			return e
		}, false},
		{"lookup validation", "LookupResourceIDPage", "error", "invalid_input", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			_, e := s.LookupResourceIDPage(ctx, authmodel.LookupRequest{})
			return e
		}, false},
		{"page source validation", "FilterPage", "error", "invalid_input", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			_, e := FilterPage(ctx, s, FilterPageRequest[string]{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc"})
			return e
		}, false},
		{"reader error", "Evaluate", "error", "other", func(ctx context.Context, s *Service, f *tupleFixture) error {
			f.fail = errors.New(strings.Repeat("private database error", 100))
			r, e := s.Evaluate(ctx, expressionPrincipal, Role("admin"))
			if r != (authmodel.CheckResult{}) {
				t.Fatal("error leaked decision")
			}
			return e
		}, true},
		{"completion error", "Evaluate", "error", "other", func(ctx context.Context, s *Service, f *tupleFixture) error {
			f.completion = errors.New("completion failed")
			r, e := s.Evaluate(ctx, expressionPrincipal, Role("admin"))
			if r != (authmodel.CheckResult{}) {
				t.Fatal("completion leaked grant")
			}
			return e
		}, true},
		{"cancellation", "Evaluate", "error", "canceled", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			_, e := s.Evaluate(ctx, expressionPrincipal, Role("admin"))
			return e
		}, false},
		{"deadline", "Evaluate", "error", "deadline_exceeded", func(ctx context.Context, s *Service, f *tupleFixture) error {
			f.completion = context.DeadlineExceeded
			_, e := s.Evaluate(ctx, expressionPrincipal, Role("admin"))
			return e
		}, true},
		{"budget", "Evaluate", "error", "evaluation_limit", func(ctx context.Context, s *Service, _ *tupleFixture) error {
			s.limits.MaxEvaluationSteps = 1
			_, e := s.Evaluate(ctx, expressionPrincipal, All(Role("admin"), Role("admin")))
			return e
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &decisionLogHandler{enabled: true}
			s, f := loggerService(t, h)
			err := tc.call(t.Context(), s, f)
			if (err != nil) != (tc.outcome == "error") {
				t.Fatalf("returned error=%v", err)
			}
			entry := assertDecisionLog(t, h, tc.operation, tc.outcome, tc.operation == "CheckWith")
			if tc.kind != "" && entry.attrs["error_kind"] != tc.kind {
				t.Fatalf("error kind=%+v", entry)
			}
			if !tc.reads && (f.snapshotCalls != 0 || f.containsCalls != 0 || f.batchCalls != 0) {
				t.Fatalf("read-free operation read tuples=%+v", f)
			}
		})
	}
}

func TestDecisionLoggingBoundsInvalidMetadataAndKeepsDefaultLogger(t *testing.T) {
	h := &decisionLogHandler{enabled: true}
	logger := slog.New(h)
	prior := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prior) })
	_, f := expressionService(t)
	s, err := NewService(f, WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	principal := expressionPrincipal
	principal.ID = strings.Repeat("\xff", 1024)
	_, err = s.Evaluate(t.Context(), principal, Role("admin"))
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("validation error=%v", err)
	}
	entry := assertDecisionLog(t, h, "Evaluate", "error", false)
	value := entry.attrs["principal_id"].(string)
	if len(value) > tuples.MaxRefFieldLen || !utf8.ValidString(value) {
		t.Fatalf("unbounded invalid log reference %d", len(value))
	}
}

func TestDecisionLoggingBoundEvaluationDoesNotClaimCommit(t *testing.T) {
	h := &decisionLogHandler{enabled: true}
	s, f := loggerService(t, h)
	f.completion = errors.New("outer transaction failed")
	result, err := s.EvaluateExpressionWith(t.Context(), f, expressionPrincipal, Role("admin"))
	if err != nil || !result.Allowed || f.snapshotCalls != 0 {
		t.Fatalf("bound evaluation=%+v/%v snapshots=%d", result, err, f.snapshotCalls)
	}
	assertDecisionLog(t, h, "EvaluateExpressionWith", "allowed", true)
}

func TestDecisionLoggingLookupRetriesEmitFinalRecord(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "recovered"}[succeeds], func(t *testing.T) {
			h := &decisionLogHandler{enabled: true}
			s := newLimitedService(t, &fakeStore{}, setHierarchySchema(), authmodel.EvaluationLimits{})
			s.logger = slog.New(h)
			calls := 0
			s.reader = retryLookupReader{Reader: s.reader, discover: func(context.Context) ([]string, error) {
				calls++
				if succeeds && calls == 3 {
					return []string{}, nil
				}
				return []string{"absent"}, nil
			}}
			s.store.(*graphFixture).reader = s.reader
			result, err := s.LookupAllResourceIDs(t.Context(), setPrincipal, "view", "space")
			if calls != 3 || len(result.IDs) != 0 || (err == nil) != succeeds {
				t.Fatalf("retry result=%+v/%v calls=%d", result, err, calls)
			}
			outcome := "complete"
			if !succeeds {
				outcome = "error"
			}
			entry := assertDecisionLog(t, h, "LookupAllResourceIDs", outcome, false)
			if !succeeds && entry.attrs["error_kind"] != "enumeration_contended" {
				t.Fatalf("retry error=%+v", entry)
			}
		})
	}
}

func TestDecisionLoggingDisabledAndConcurrentCalls(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "debug"}[enabled], func(t *testing.T) {
			h := &decisionLogHandler{enabled: enabled}
			s, f := loggerService(t, h)
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					r, err := s.Evaluate(t.Context(), expressionPrincipal, Role("admin"))
					if err != nil || !r.Allowed {
						t.Errorf("decision=%+v/%v", r, err)
					}
				})
			}
			wg.Wait()
			want := 0
			if enabled {
				want = 20
			}
			if len(h.entries()) != want || f.snapshotCalls != 20 || f.containsCalls != 20 {
				t.Fatalf("records=%d snapshots=%d reads=%d", len(h.entries()), f.snapshotCalls, f.containsCalls)
			}
		})
	}
}

func BenchmarkDecisionLogging(b *testing.B) {
	for _, workload := range []string{"read_free_check", "exact_role_evaluate"} {
		b.Run(workload, func(b *testing.B) {
			for _, mode := range []string{"unlogged", "debug_disabled", "debug_enabled"} {
				b.Run(mode, func(b *testing.B) {
					level := slog.LevelInfo
					if mode == "debug_enabled" {
						level = slog.LevelDebug
					}
					_, f := expressionService(b, exact(tuples.Global(), "admin"))
					s, err := NewService(f, WithLogger(slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level}))))
					if err != nil {
						b.Fatal(err)
					}
					req := authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "unknown", Resource: authmodel.Resource{Type: "doc", ID: "a"}}
					ctx := b.Context()
					expr := Role("admin")
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						if workload == "exact_role_evaluate" {
							if mode == "unlogged" {
								_, _ = s.evaluate(ctx, expressionPrincipal, expr)
							} else {
								_, _ = s.Evaluate(ctx, expressionPrincipal, expr)
							}
						} else {
							if mode == "unlogged" {
								_, _ = s.checkRequest(ctx, req)
							} else {
								_, _ = s.Check(ctx, req)
							}
						}
					}
				})
			}
		})
	}
}

func TestDecisionLoggingNestedPublicCallsRemainIndependent(t *testing.T) {
	h := &decisionLogHandler{enabled: true}
	s, _ := loggerService(t, h)
	target := authmodel.Resource{Type: "doc", ID: "a"}
	result, err := s.EvaluateResolved(t.Context(), expressionPrincipal, BindResource("doc", "target", RoleIn("viewer")), func(ctx context.Context, _ string) (authmodel.Resource, error) {
		nested, err := s.Check(ctx, authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "read", Resource: target})
		if err == nil && !nested.Allowed {
			t.Fatal("independent nested check denied")
		}
		return target, err
	})
	if err != nil || !result.Allowed {
		t.Fatalf("outer decision=%+v/%v", result, err)
	}
	entries := h.entries()
	if len(entries) != 2 || entries[0].attrs["operation"] != "Check" || entries[1].attrs["operation"] != "EvaluateResolved" {
		t.Fatalf("independent public call suppressed=%+v", entries)
	}
}
