package authorization

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	observedAdmin   = "admin-1"
	stewardAssign   = `{"subject_type":"user","subject_id":"u-1","role":"steward","scope":{"kind":"resource","resource_type":"organization","resource_id":"o-1"}}`
	traceTestHeader = "X-Test-Trace"
)

type decisionCapture struct {
	mu     sync.Mutex
	events []decisions.DecisionEvent
}

func (c *decisionCapture) observe(_ context.Context, event decisions.DecisionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *decisionCapture) all() []decisions.DecisionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]decisions.DecisionEvent(nil), c.events...)
}

// refuseSteward is a WritePolicy that refuses the steward role only.
func refuseSteward(_ context.Context, req authorizationhttp.RoleWriteRequest) error {
	if req.Role == "steward" {
		return fmt.Errorf("steward is not assignable through the bundled route: %w", sdk.ErrForbidden)
	}
	return nil
}

type observedHost struct {
	handler http.Handler
	comps   Components
	store   *memory.Store
	logs    *bytes.Buffer
}

// newObservedHost mounts the bundled role routes over an audited memory store
// behind web.RequestID, a trace stamp from traceTestHeader and an
// authenticating gate. The logger is at INFO, so nothing depends on DEBUG.
func newObservedHost(t *testing.T, policy authorizationhttp.RoleWritePolicy, observer decisions.DecisionObserver) observedHost {
	t.Helper()
	store := memory.New(memory.WithAudit())
	logs := &bytes.Buffer{}
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: observedAdmin})
			if trace := r.Header.Get(traceTestHeader); trace != "" {
				ctx = sdk.WithTraceID(ctx, trace)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	opts := []Option{WithModel(lifecycleModel()), WithLogger(slog.New(slog.NewTextHandler(logs, nil))), WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: gate, WritePolicy: policy})}
	if observer != nil {
		opts = append(opts, WithDecisionObserver(observer))
	}
	comps, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Audit: store.Audit()}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	if err := comps.Register(pockets.Mount{Router: router}); err != nil {
		t.Fatal(err)
	}
	return observedHost{handler: web.RequestID()(router), comps: comps, store: store, logs: logs}
}

func (h observedHost) post(t *testing.T, ctx context.Context, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, "POST", "/authorization/roles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func TestDecisionObserverReceivesUnboundDecisionsOnly(t *testing.T) {
	capture := &decisionCapture{}
	host := newObservedHost(t, allowRoleWrite, capture.observe)
	req := authmodel.CheckRequest{Principal: sdk.Principal{Type: "user", ID: "u-1"}, Permission: "edit", Resource: authmodel.Resource{Type: "doc", ID: "d"}}
	if got, err := host.comps.Decisions.Check(t.Context(), req); err != nil || got.Allowed {
		t.Fatalf("check: %+v %v", got, err)
	}
	if _, err := host.comps.Decisions.CheckWith(t.Context(), host.store.Tuples(), req); err != nil {
		t.Fatal(err)
	}
	events := capture.all()
	if len(events) != 1 || events[0].Operation != "Check" || events[0].Outcome != decisions.OutcomeDenied || events[0].Principal != req.Principal || events[0].ResourceID != "d" {
		t.Fatalf("events=%+v", events)
	}
}

func TestRefusedRoleWriteIsLoggedAndObserved(t *testing.T) {
	capture := &decisionCapture{}
	observed := newObservedHost(t, refuseSteward, capture.observe)
	plain := newObservedHost(t, refuseSteward, nil)
	got := observed.post(t, t.Context(), stewardAssign, map[string]string{"X-Request-ID": "req-1"})
	want := plain.post(t, t.Context(), stewardAssign, map[string]string{"X-Request-ID": "req-1"})
	if got.Code != http.StatusForbidden || got.Code != want.Code || got.Body.String() != want.Body.String() {
		t.Fatalf("observed response %d %s; plain %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
	}
	for _, host := range []observedHost{observed, plain} {
		logs := host.logs.String()
		if n := strings.Count(logs, "authorization: role write refused"); n != 1 {
			t.Fatalf("refusal lines=%d: %s", n, logs)
		}
		for _, field := range []string{"level=WARN", "actor_type=user", "actor_id=" + observedAdmin, "operation=role_assign", "subject_type=user", "subject_id=u-1", "role=steward", "scope_kind=resource", "scope_type=organization", "scope_id=o-1", "error_kind=forbidden"} {
			if !strings.Contains(logs, field) {
				t.Errorf("refusal line lacks %s: %s", field, logs)
			}
		}
		if strings.Contains(logs, "not assignable") {
			t.Errorf("refusal line carries the policy's error text: %s", logs)
		}
	}
	wantEvent := decisions.DecisionEvent{
		Operation: "RoleWrite", Principal: sdk.Principal{Type: "user", ID: observedAdmin}, Subject: sdk.Principal{Type: "user", ID: "u-1"},
		Permission: "steward", ResourceType: "organization", ResourceID: "o-1", Outcome: decisions.OutcomeRefused, ErrorKind: "forbidden",
	}
	if events := capture.all(); len(events) != 1 || events[0] != wantEvent {
		t.Fatalf("events=%+v", events)
	}
	if records := auditRecords(t, observed.store); len(records) != 0 {
		t.Fatalf("refused write recorded: %+v", records)
	}
}

func TestCanceledRoleWriteIsNotARefusal(t *testing.T) {
	capture := &decisionCapture{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	policy := func(ctx context.Context, _ authorizationhttp.RoleWriteRequest) error {
		cancel()
		return ctx.Err()
	}
	host := newObservedHost(t, policy, capture.observe)
	if rec := host.post(t, ctx, stewardAssign, nil); rec.Code == http.StatusOK {
		t.Fatalf("canceled write succeeded: %s", rec.Body.String())
	}
	if events := capture.all(); len(events) != 0 {
		t.Fatalf("canceled request observed as a refusal: %+v", events)
	}
	if strings.Contains(host.logs.String(), "role write refused") {
		t.Fatalf("canceled request logged as a refusal: %s", host.logs.String())
	}
}

func TestRoleWriteStampsCorrelationIDs(t *testing.T) {
	host := newObservedHost(t, allowRoleWrite, nil)
	for _, tc := range []struct {
		subject, requestID, trace string
		want                      audit.Source
		// minted marks an inbound id web.RequestID replaces at the edge; the row
		// carries the minted id it echoes.
		minted bool
	}{
		{"u-1", "req-42", "trace-1", audit.Source{RequestID: "req-42", TraceID: "trace-1"}, false},
		{"u-2", strings.Repeat("r", 200), "trace-2", audit.Source{TraceID: "trace-2"}, true},
		{"u-3", strings.Repeat("r", audit.MaxCorrelationIDLen), "trace\x7f3", audit.Source{RequestID: strings.Repeat("r", audit.MaxCorrelationIDLen)}, false},
	} {
		body := `{"subject_type":"user","subject_id":"` + tc.subject + `","role":"viewer","scope":{"kind":"global"}}`
		rec := host.post(t, t.Context(), body, map[string]string{"X-Request-ID": tc.requestID, traceTestHeader: tc.trace})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: assign = %d %s", tc.subject, rec.Code, rec.Body.String())
		}
		page, err := host.store.Audit().List(t.Context(), audit.Filter{SubjectType: "user", SubjectID: tc.subject}, list.Request{Limit: 10})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s: audit=%+v %v", tc.subject, page, err)
		}
		want := tc.want
		if tc.minted {
			want.RequestID = rec.Header().Get("X-Request-ID")
			if want.RequestID == "" || want.RequestID == tc.requestID {
				t.Fatalf("%s: echoed request id = %q, want a minted one", tc.subject, want.RequestID)
			}
		}
		want.ActorType, want.ActorID = "user", observedAdmin
		if got := page.Items[0].Source; got != want {
			t.Fatalf("%s: source=%+v want %+v", tc.subject, got, want)
		}
	}
}
