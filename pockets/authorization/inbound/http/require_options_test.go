package authorizationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type denialSnapshotReader struct {
	*memory.Tuples
	snapshots int
	open      bool
}

func (s *denialSnapshotReader) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	s.snapshots++
	s.open = true
	defer func() { s.open = false }()
	return s.Tuples.ReadTupleSnapshot(ctx, fn)
}

func TestRequireHostDenialResponse(t *testing.T) {
	store := &denialSnapshotReader{Tuples: memory.NewTuples()}
	seedGuard(t, store.Tuples, guardFact(tuples.On("document", "one"), "editor", "user", "alice", ""))
	adapter := guardAdapter(t, store, decisions.WithModel(guardModel()))
	document := Path("document", "documentID")
	policy := Any(HasRole("admin", Global()), Can("edit", document))
	var denied int
	var actual *http.Request
	conceal := WithDeniedHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		denied++
		actual = r
		if store.open {
			t.Error("response handler ran inside decision snapshot")
		}
		web.RespondJSONError(w, web.ErrNotFound("not found"))
	}))
	gate := adapter.Require(policy, conceal)
	if store.snapshots != 0 || denied != 0 {
		t.Fatal("mount performed decision or response work")
	}
	for _, id := range []string{"one", "missing"} {
		req := guardRequest("outsider")
		req.SetPathValue("documentID", id)
		before := store.snapshots
		rec, next := serveGuard(gate, req)
		if rec.Code != 404 || next != 0 || actual != req || store.snapshots != before+1 {
			t.Fatalf("status=%d next=%d snapshots=%d", rec.Code, next, store.snapshots-before)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" || len(body) != 2 || body["code"] != "not_found" || body["message"] != "not found" {
			t.Fatalf("host response changed: headers=%v body=%s", rec.Header(), rec.Body)
		}
	}
	for _, principal := range []string{"alice", ""} {
		rec, next := serveGuard(gate, guardRequest(principal))
		want := 204
		if principal == "" {
			want = 401
		}
		if rec.Code != want || next != boolInt(want == 204) || denied != 2 {
			t.Fatalf("status=%d next=%d denied=%d", rec.Code, next, denied)
		}
	}
	// Configuring one mounted policy must not change the adapter's other routes.
	rec, next := serveGuard(adapter.Require(policy), guardRequest("outsider"))
	if rec.Code != 403 || next != 0 || denied != 2 {
		t.Fatalf("default changed: status=%d next=%d denied=%d", rec.Code, next, denied)
	}
}

func TestRequireDenialHandlerCannotMaskFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		cancel bool
		status int
	}{
		{"store failure", errors.New("private connection details"), false, 500},
		{"budget exhausted", authmodel.ErrEvaluationLimit, false, 503},
		{"canceled completion", nil, true, 500},
		{"context failure", context.DeadlineExceeded, false, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := guardRequest("alice")
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			req = req.WithContext(ctx)
			service := &narrowDecisions{
				validate: func(decisions.Expression) error { return nil },
				evaluate: func(context.Context, authmodel.PrincipalRef, decisions.Expression, decisions.ResourceResolver) (authmodel.CheckResult, error) {
					if tc.cancel {
						cancel()
					}
					return authmodel.CheckResult{}, tc.err
				},
			}
			adapter, err := New(Services{Decisions: service})
			if err != nil {
				t.Fatal(err)
			}
			denied := 0
			gate := adapter.Require(HasRole("admin", Global()), WithDeniedHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				denied++
				w.WriteHeader(404)
			})))
			rec, next := serveGuard(gate, req)
			if rec.Code != tc.status || next != 0 || denied != 0 || strings.Contains(rec.Body.String(), "private") {
				t.Fatalf("status=%d next=%d denied=%d body=%s", rec.Code, next, denied, rec.Body)
			}
		})
	}
}

func TestRequireOptionsValidateAtMount(t *testing.T) {
	adapter := guardAdapter(t, memory.NewTuples())
	for _, tc := range []struct {
		name string
		opts []RequireOption
	}{
		{"nil option", []RequireOption{nil}},
		{"nil handler", []RequireOption{WithDeniedHandler(nil)}},
		{"nil handler func", []RequireOption{WithDeniedHandler(http.HandlerFunc(nil))}},
		{"nil handler pointer", []RequireOption{WithDeniedHandler((*http.ServeMux)(nil))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid policy mounted")
				}
			}()
			adapter.Require(HasRole("admin", Global()), tc.opts...)
		})
	}
}

func TestRequireOptionsAreIsolatedAcrossConcurrentRoutes(t *testing.T) {
	adapter := guardAdapter(t, memory.NewTuples())
	var denied atomic.Int64
	conceal := WithDeniedHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		denied.Add(1)
		http.NotFound(w, r)
	}))
	policy := HasRole("admin", Global())
	first := adapter.Require(policy, conceal)
	second := adapter.Require(policy, conceal, WithDeniedHandler(http.HandlerFunc(denyForbidden)))
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			for _, tc := range []struct {
				gate   web.Middleware
				status int
			}{{first, 404}, {second, 403}} {
				rec, next := serveGuard(tc.gate, guardRequest("outsider"))
				if rec.Code != tc.status || next != 0 {
					t.Errorf("status=%d next=%d", rec.Code, next)
				}
			}
		})
	}
	wg.Wait()
	if denied.Load() != 24 {
		t.Fatalf("denied handler calls=%d", denied.Load())
	}
}
