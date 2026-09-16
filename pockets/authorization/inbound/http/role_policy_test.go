package authorizationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestRolePolicyValidatesBeforeAdmissionAndCoversBothWrites(t *testing.T) {
	for _, route := range []struct {
		path      string
		operation mutations.Operation
	}{{pathAssignRole, mutations.OpRoleAssign}, {pathUnassignRole, mutations.OpRoleUnassign}} {
		t.Run(string(route.operation), func(t *testing.T) {
			stub := &stubRoleAdmin{receipt: testReceipt()}
			var seen []RoleWriteRequest
			policy := func(_ context.Context, req RoleWriteRequest) error { seen = append(seen, req); return sdk.ErrForbidden }
			a, err := New(Services{Roles: stub, Mutations: stub}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: policy}))
			if err != nil {
				t.Fatal(err)
			}
			var router http.Handler = a.AssignRole()
			if route.operation == mutations.OpRoleUnassign {
				router = a.UnassignRole()
			}
			for _, body := range []string{`{"subject_type":"user","subject_id":"","role":"owner","scope":{"kind":"global"}}`, `{"subject_type":"user","subject_id":"one","role":"","scope":{"kind":"global"}}`} {
				if rec := doJSON(t, router, "POST", route.path, body); rec.Code != 400 {
					t.Fatalf("invalid body status=%d", rec.Code)
				}
			}
			if len(seen) != 0 {
				t.Fatal("malformed command reached policy")
			}
			rec := doJSON(t, router, "POST", route.path, `{"subject_type":"user","subject_id":"one","role":"owner","scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}`)
			if rec.Code != 403 || len(seen) != 1 {
				t.Fatalf("status=%d requests=%+v", rec.Code, seen)
			}
			want := RoleWriteRequest{Principal: sdk.Principal{Type: "user", ID: "admin-1"}, Operation: route.operation, Subject: model.PrincipalRef{Type: "user", ID: "one"}, Role: "owner", Scope: tuples.On("project", "p1")}
			if seen[0] != want {
				t.Fatalf("policy saw %+v, want %+v", seen[0], want)
			}
			if stub.assignActor.ID != "" || stub.unassignActor.ID != "" {
				t.Fatal("denial reached data writer")
			}
		})
	}
}

func TestRoleAdmissionRevocationAndIntegrityAreSeparate(t *testing.T) {
	store := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{Rules: []mutations.IntegrityRule{{ResourceType: "project", Relation: "owner", MinSubjects: 1}}}))
	reader, err := roles.NewService(store.Tuples())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := mutations.NewService(store.Mutations())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := decisions.NewService(store.Tuples())
	if err != nil {
		t.Fatal(err)
	}
	admin := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "admin-1"}}
	owner := tuples.Tuple{Scope: tuples.On("project", "p1"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "owner"}}
	if err := store.Tuples().ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{admin, owner}}); err != nil {
		t.Fatal(err)
	}
	var admissions int
	policy := func(ctx context.Context, req RoleWriteRequest) error {
		result, err := engine.EvaluateResolved(ctx, model.PrincipalFrom(req.Principal), decisions.Role("admin"), nil)
		if err != nil {
			return err
		}
		if !result.Allowed {
			return sdk.ErrForbidden
		}
		admissions++
		// Revoke after the decision, before the admitted data write begins.
		return store.Tuples().ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{admin}})
	}
	a, err := New(Services{Roles: reader, Mutations: writer}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: policy}))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"subject_type":"user","subject_id":"reader","role":"viewer","scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}`
	if rec := doJSON(t, a.AssignRole(), "POST", pathAssignRole, body); rec.Code != 200 {
		t.Fatalf("admitted operation denied retroactively: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, a.AssignRole(), "POST", pathAssignRole, strings.Replace(body, "reader", "other", 1)); rec.Code != 403 {
		t.Fatalf("new request after revocation: %d", rec.Code)
	}
	if admissions != 1 {
		t.Fatalf("admissions=%d", admissions)
	}
	if err := store.Tuples().ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{admin}}); err != nil {
		t.Fatal(err)
	}
	ownerBody := `{"subject_type":"user","subject_id":"owner","role":"owner","scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}`
	if rec := doJSON(t, a.UnassignRole(), "POST", pathUnassignRole, ownerBody); rec.Code != 409 {
		t.Fatalf("admitted orphaning was not an integrity conflict: %d %s", rec.Code, rec.Body.String())
	}
	if held, err := store.Tuples().Contains(t.Context(), owner); err != nil || !held {
		t.Fatalf("integrity refusal removed owner: %v %v", held, err)
	}
}

func TestRolePolicyErrorsCancellationAndAuditAttribution(t *testing.T) {
	for _, failure := range []error{sdk.ErrForbidden, errors.New("policy unavailable"), context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			stub := &stubRoleAdmin{receipt: testReceipt()}
			a, err := New(Services{Roles: stub, Mutations: stub}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: func(context.Context, RoleWriteRequest) error { return failure }}))
			if err != nil {
				t.Fatal(err)
			}
			rec := doJSON(t, a.AssignRole(), "POST", pathAssignRole, `{"subject_type":"user","subject_id":"one","role":"viewer","scope":{"kind":"global"}}`)
			if rec.Code == 200 || stub.assignActor.ID != "" {
				t.Fatal("failed admission reached data writer")
			}
		})
	}
	store := memory.New(memory.WithAudit())
	reader, _ := roles.NewService(store.Tuples())
	writer, _ := mutations.NewService(store.Mutations())
	a, err := New(Services{Roles: reader, Mutations: writer}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: allowWrite}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", pathAssignRole, strings.NewReader(`{"subject_type":"user","subject_id":"one","role":"viewer","scope":{"kind":"global"}}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(audit.WithSource(req.Context(), audit.Source{System: "spoofed", Reason: "ticket 42"}))
	rec := httptest.NewRecorder()
	a.AssignRole().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	records, err := store.Audit().List(t.Context(), audit.Filter{}, list.Request{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Items) != 1 || records.Items[0].Source != (audit.Source{ActorType: "user", ActorID: "admin-1", Reason: "ticket 42"}) {
		t.Fatalf("inbound attribution: %+v", records.Items)
	}
}

func TestRolePolicyCannotRewriteOrApplyAfterCancellation(t *testing.T) {
	for _, cancelAdmission := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAdmission), func(t *testing.T) {
			store := memory.New()
			reader, _ := roles.NewService(store.Tuples())
			writer, _ := mutations.NewService(store.Mutations())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			policy := func(_ context.Context, request RoleWriteRequest) error {
				request.Subject.ID = "rewritten"
				request.Role = "owner"
				if cancelAdmission {
					cancel()
				}
				return nil
			}
			a, err := New(Services{Roles: reader, Mutations: writer}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: policy}))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", pathAssignRole, strings.NewReader(`{"subject_type":"user","subject_id":"original","role":"viewer","scope":{"kind":"global"}}`))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			a.AssignRole().ServeHTTP(rec, req)
			fact := tuples.Tuple{Scope: tuples.Global(), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "original"}}
			held, err := store.Tuples().Contains(t.Context(), fact)
			if err != nil {
				t.Fatal(err)
			}
			if cancelAdmission {
				if rec.Code == 200 || held {
					t.Fatal("canceled admission applied a write")
				}
			} else if rec.Code != 200 || !held {
				t.Fatalf("policy rewrote admitted command: status=%d held=%v", rec.Code, held)
			}
			fact.Relation = "owner"
			fact.Subject.ID = "rewritten"
			held, err = store.Tuples().Contains(t.Context(), fact)
			if err != nil || held {
				t.Fatalf("policy's local changes leaked: %v %v", held, err)
			}
		})
	}
}

func TestRolePolicyCancellationNeverReachesInjectedWriter(t *testing.T) {
	for _, operation := range []mutations.Operation{mutations.OpRoleAssign, mutations.OpRoleUnassign} {
		for _, preCanceled := range []bool{false, true} {
			t.Run(string(operation)+fmt.Sprint(preCanceled), func(t *testing.T) {
				stub := &stubRoleAdmin{receipt: testReceipt()}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				calls := 0
				if preCanceled {
					cancel()
				}
				policy := func(context.Context, RoleWriteRequest) error { calls++; cancel(); return nil }
				a, err := New(Services{Roles: stub, Mutations: stub}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: policy}))
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("POST", pathAssignRole, strings.NewReader(`{"subject_type":"user","subject_id":"one","role":"viewer","scope":{"kind":"global"}}`))
				req.Header.Set("Content-Type", "application/json")
				req = req.WithContext(ctx)
				rec := httptest.NewRecorder()
				handler := a.AssignRole()
				if operation == mutations.OpRoleUnassign {
					handler = a.UnassignRole()
				}
				handler.ServeHTTP(rec, req)
				if rec.Code == 200 || stub.assignActor.ID != "" || stub.unassignActor.ID != "" {
					t.Fatalf("canceled request reached injected writer: status=%d", rec.Code)
				}
				want := 1
				if preCanceled {
					want = 0
				}
				if calls != want {
					t.Fatalf("policy calls=%d want=%d", calls, want)
				}
			})
		}
	}
}
