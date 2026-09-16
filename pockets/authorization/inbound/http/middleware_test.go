package authorizationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

// This substitute deliberately implements no Check, Limits or model-inspection API.
type narrowDecisions struct {
	validate func(decisions.Expression) error
	evaluate func(context.Context, authmodel.PrincipalRef, decisions.Expression, decisions.ResourceResolver) (authmodel.CheckResult, error)
}

func (s *narrowDecisions) ValidateExpression(e decisions.Expression) error { return s.validate(e) }
func (s *narrowDecisions) EvaluateResolved(ctx context.Context, p authmodel.PrincipalRef, e decisions.Expression, resolve decisions.ResourceResolver) (authmodel.CheckResult, error) {
	return s.evaluate(ctx, p, e, resolve)
}

var _ DecisionService = (*narrowDecisions)(nil)

func TestRequireNarrowDecisionService(t *testing.T) {
	var validated decisions.Expression
	validations, evaluations, resolutions := 0, 0, 0
	target := Resource("document", func(r *http.Request) (authmodel.Resource, error) {
		resolutions++
		if r.Header.Get("X-Input") != "one" {
			t.Fatal("resolver lost request input")
		}
		return authmodel.Resource{Type: "document", ID: r.Header.Get("X-Input")}, nil
	})
	service := &narrowDecisions{
		validate: func(e decisions.Expression) error { validations++; validated = e; return nil },
		evaluate: func(ctx context.Context, p authmodel.PrincipalRef, e decisions.Expression, resolve decisions.ResourceResolver) (authmodel.CheckResult, error) {
			evaluations++
			if p != (authmodel.PrincipalRef{Type: "user", ID: "alice"}) || !reflect.DeepEqual(e, validated) {
				t.Fatalf("operation input: %+v/%+v", p, e)
			}
			if len(e.AllOf) != 2 || e.AllOf[0].RoleName != "employee" || e.AllOf[1].NamedPermission != "edit" {
				t.Fatalf("lowered expression: %+v", e)
			}
			got, err := resolve(ctx, e.AllOf[1].ResourceSlot.Key)
			if err != nil || got != (authmodel.Resource{Type: "document", ID: "one"}) {
				t.Fatalf("resolved input: %+v/%v", got, err)
			}
			return authmodel.CheckResult{Allowed: true}, nil
		},
	}
	adapter, err := New(Services{Decisions: service})
	if err != nil {
		t.Fatal(err)
	}
	gate := adapter.Require(All(HasRole("employee", Global()), Can("edit", target)))
	if validations != 1 || evaluations != 0 || resolutions != 0 {
		t.Fatal("mount performed evaluation or input I/O")
	}
	rec, calls := serveGuard(gate, guardRequest(""))
	if rec.Code != 401 || calls != 0 || evaluations != 0 || resolutions != 0 {
		t.Fatal("anonymous request reached evaluation")
	}
	req := guardRequest("alice")
	req.Header.Set("X-Input", "one")
	rec, calls = serveGuard(gate, req)
	if rec.Code != 204 || calls != 1 || evaluations != 1 || resolutions != 1 {
		t.Fatalf("status=%d next=%d evaluated=%d resolved=%d", rec.Code, calls, evaluations, resolutions)
	}
}

func TestRequireDecisionErrorsAndResponseBodies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		result    authmodel.CheckResult
		err       error
		principal string
		status    int
		message   string
	}{
		{"anonymous", authmodel.CheckResult{Allowed: true}, nil, "", 401, "authentication required"},
		{"deny", authmodel.CheckResult{}, nil, "alice", 403, "permission denied"},
		{"allow", authmodel.CheckResult{Allowed: true}, nil, "alice", 204, ""},
		{"error discards allow", authmodel.CheckResult{Allowed: true}, errors.New("private provider details"), "alice", 500, "internal error"},
		{"wrapped budget", authmodel.CheckResult{Allowed: true}, fmt.Errorf("exhausted: %w", authmodel.ErrEvaluationLimit), "alice", 503, "authorization temporarily unavailable"},
		{"evaluation sentinel is error", authmodel.CheckResult{Allowed: true}, decisions.ErrResourceNotApplicable, "alice", 500, "internal error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &narrowDecisions{validate: func(decisions.Expression) error { return nil }, evaluate: func(context.Context, authmodel.PrincipalRef, decisions.Expression, decisions.ResourceResolver) (authmodel.CheckResult, error) {
				return tc.result, tc.err
			}}
			adapter, err := New(Services{Decisions: service})
			if err != nil {
				t.Fatal(err)
			}
			rec, next := serveGuard(adapter.Require(HasRole("admin", Global())), guardRequest(tc.principal))
			if rec.Code != tc.status || next != boolInt(tc.status == 204) {
				t.Fatalf("status=%d next=%d body=%s", rec.Code, next, rec.Body)
			}
			if tc.message != "" {
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"message": tc.message}
				if code := map[int]string{401: "unauthenticated", 403: "permission_denied", 500: "internal"}[tc.status]; code != "" {
					want["code"] = code
				}
				if !reflect.DeepEqual(body, want) {
					t.Fatalf("error body: %s", rec.Body)
				}
			}
		})
	}
}

func TestRequireCustomValidationAndCancellation(t *testing.T) {
	t.Run("validation aborts mount", func(t *testing.T) {
		adapter, err := New(Services{Decisions: &narrowDecisions{validate: func(decisions.Expression) error { return errors.New("unsupported policy") }}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if recover() == nil {
				t.Fatal("invalid policy mounted")
			}
		}()
		adapter.Require(HasRole("admin", Global()))
	})
	t.Run("typed nil custom service", func(t *testing.T) {
		adapter, err := New(Services{Decisions: (*narrowDecisions)(nil)})
		if adapter != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("adapter=%v error=%v", adapter, err)
		}
	})
	t.Run("canceled result cannot allow", func(t *testing.T) {
		req := guardRequest("alice")
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		req = req.WithContext(ctx)
		adapter, err := New(Services{Decisions: &narrowDecisions{validate: func(decisions.Expression) error { return nil }, evaluate: func(context.Context, authmodel.PrincipalRef, decisions.Expression, decisions.ResourceResolver) (authmodel.CheckResult, error) {
			cancel()
			return authmodel.CheckResult{Allowed: true}, nil
		}}})
		if err != nil {
			t.Fatal(err)
		}
		rec, next := serveGuard(adapter.Require(HasRole("admin", Global())), req)
		if rec.Code != http.StatusInternalServerError || next != 0 {
			t.Fatalf("status=%d next=%d", rec.Code, next)
		}
	})
}
