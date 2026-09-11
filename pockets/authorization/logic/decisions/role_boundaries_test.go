package decisions

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestRolePermissionChargesRootAndVisitedGrantors(t *testing.T) {
	model := mustCompile(t, orgModel(), nil)
	req := orgRequest("u", "view", "o")
	for _, tc := range []struct {
		steps     int
		grant     string
		calls     int
		exhausted bool
		allowed   bool
	}{
		{1, "", 0, true, false}, {2, "", 1, true, false}, {3, "", 2, true, false}, {4, "", 3, false, false},
		{2, "contributor", 1, false, true}, {3, "steward", 2, false, true}, {3, "viewer", 2, true, false},
	} {
		t.Run(fmt.Sprintf("steps%d/grant%s", tc.steps, tc.grant), func(t *testing.T) {
			calls := 0
			got, err := authmodel.EvaluateRolePermission(context.Background(), model, req, resolvedLimits(t, authmodel.EvaluationLimits{MaxEvaluationSteps: tc.steps}), func(_ context.Context, name string) (bool, error) { calls++; return name == tc.grant, nil })
			if errors.Is(err, authmodel.ErrEvaluationLimit) != tc.exhausted || (err != nil && !tc.exhausted) || got.Allowed != tc.allowed || calls != tc.calls {
				t.Fatalf("result=%+v err=%v calls=%d", got, err, calls)
			}
			if tc.exhausted && got.ReasonCode != "" {
				t.Fatalf("budget became deny: %+v", got)
			}
		})
	}
	req.Permission = "unknown"
	got, err := authmodel.EvaluateRolePermission(context.Background(), model, req, resolvedLimits(t, authmodel.EvaluationLimits{MaxEvaluationSteps: 1}), func(context.Context, string) (bool, error) {
		t.Fatal("undeclared permission probed role")
		return false, nil
	})
	if err != nil || got.Allowed || got.Reason != "no rules defined" {
		t.Fatalf("one-step undeclared root: %+v %v", got, err)
	}
}

type roleBoundaryProbe struct {
	errProbe
	reads        int
	limits       []int
	cancel       context.CancelFunc
	held         string
	unrestricted bool
}

func (p *roleBoundaryProbe) HasExactRole(ctx context.Context, st, sid, name, rt, rid string) (bool, error) {
	p.reads++
	if p.cancel != nil {
		p.cancel()
	}
	return p.held == name, nil
}
func (p *roleBoundaryProbe) ListRoleAssignmentsBySubject(ctx context.Context, principal authmodel.PrincipalRef, req list.Request) (list.Page[roles.Assignment], error) {
	p.reads++
	if p.cancel != nil {
		p.cancel()
	}
	if p.unrestricted {
		return list.Page[roles.Assignment]{Items: []roles.Assignment{{Role: "contributor"}}}, nil
	}
	return list.Page[roles.Assignment]{}, nil
}
func (p *roleBoundaryProbe) LookupResourceIDsBySubjectAndRoles(ctx context.Context, st, sid, rt string, roles []string, after string, limit int) ([]string, bool, error) {
	p.reads++
	p.limits = append(p.limits, limit)
	if p.cancel != nil {
		p.cancel()
	}
	return nil, p.unrestricted, nil
}

func TestRoleEngineBudgetAppliesToCheckExplainAndBatch(t *testing.T) {
	for _, steps := range []int{1, 2, 4} {
		for _, method := range []string{"check", "explain", "batch"} {
			t.Run(fmt.Sprintf("steps%d/%s", steps, method), func(t *testing.T) {
				probe := &roleBoundaryProbe{}
				e := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{MaxEvaluationSteps: steps}))
				req := orgRequest("u", "view", "o")
				var err error
				switch method {
				case "check":
					_, err = e.Check(context.Background(), req)
				case "explain":
					var explanation authmodel.Explanation
					_, explanation, err = e.CheckExplain(context.Background(), req)
					if got, want := len(explanation.Steps), min(steps-1, 3); got != want {
						t.Fatalf("trace steps=%d want=%d", got, want)
					}
				case "batch":
					_, err = e.CheckBatch(context.Background(), []authmodel.CheckRequest{req, req})
				}
				if errors.Is(err, authmodel.ErrEvaluationLimit) != (steps < 4) {
					t.Fatalf("error=%v", err)
				}
				// The second complete decision reuses exact reads but still evaluates its
				// own root and grantors. An exhausted decision returns no later results.
				if want := 2 * min(steps-1, 3); probe.reads != want {
					t.Fatalf("reads=%d want=%d", probe.reads, want)
				}
			})
		}
	}
}

func TestRolePermissionRejectsCanceledSuccessfulProbe(t *testing.T) {
	for _, held := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		got, err := authmodel.EvaluateRolePermission(ctx, mustCompile(t, orgModel(), nil), orgRequest("u", "view", "o"), resolvedLimits(t, authmodel.EvaluationLimits{}), func(context.Context, string) (bool, error) { cancel(); return held, nil })
		cancel()
		if !errors.Is(err, context.Canceled) || got.Allowed {
			t.Fatalf("canceled role callback: %+v %v", got, err)
		}
	}
}

func TestRoleLookupBoundaries(t *testing.T) {
	principal := authmodel.PrincipalRef{Type: "user", ID: "u"}
	for _, page := range []bool{false, true} {
		for _, permission := range []string{"view", "unknown"} {
			ctx, cancel := context.WithCancel(context.Background())
			probe := &roleBoundaryProbe{cancel: cancel}
			if permission == "unknown" {
				cancel()
			}
			e := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{}))
			var err error
			var got authmodel.LookupResult
			if page {
				got, err = e.LookupResourcesPage(ctx, principal, permission, "organization", "", 1)
			} else {
				got, err = e.LookupResources(ctx, principal, permission, "organization")
			}
			cancel()
			if !errors.Is(err, context.Canceled) || got.IDs != nil || got.Unrestricted {
				t.Fatalf("page=%t permission=%s: %+v %v", page, permission, got, err)
			}
		}
	}
	maxInt := int(^uint(0) >> 1)
	probe := &roleBoundaryProbe{}
	e := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{MaxLookupResults: maxInt - 1}))
	if _, err := e.LookupResourcesPage(context.Background(), principal, "view", "organization", "", maxInt-1); err != nil {
		t.Fatal(err)
	}
	if probe.limits[0] != maxInt {
		t.Fatalf("lookahead cap=%d", probe.limits[0])
	}
	if _, err := e.LookupResourcesPage(context.Background(), principal, "view", "organization", "", maxInt); !errors.Is(err, sdk.ErrInvalidInput) || probe.reads != 1 {
		t.Fatalf("overflow request: %v reads=%d", err, probe.reads)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe.cancel = cancel
	probe.unrestricted = true
	got, err := e.LookupResourcesPage(ctx, principal, "view", "organization", "", 1)
	if !errors.Is(err, context.Canceled) || got.Unrestricted {
		t.Fatalf("canceled unrestricted read: %+v %v", got, err)
	}
}

func TestRoleBudgetStillChargesMemoHits(t *testing.T) {
	probe := &roleBoundaryProbe{}
	engine := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{MaxEvaluationSteps: 3}))
	read := memoRoleReads(probe.HasExactRole)
	for attempt := 0; attempt < 2; attempt++ {
		got, err := engine.check(context.Background(), orgRequest("u", "view", "o"), nil, read)
		if !errors.Is(err, authmodel.ErrEvaluationLimit) || got.ReasonCode != "" || probe.reads != 4 {
			t.Fatalf("attempt=%d result=%+v err=%v reads=%d; cache hits must still consume steps", attempt, got, err, probe.reads)
		}
	}
}

func TestRoleEnumerationChargesAllGrantorsBeforeReading(t *testing.T) {
	for _, page := range []bool{false, true} {
		for _, unrestricted := range []bool{false, true} {
			for _, steps := range []int{3, 4} {
				t.Run(fmt.Sprintf("page%t/global%t/steps%d", page, unrestricted, steps), func(t *testing.T) {
					probe := &roleBoundaryProbe{unrestricted: unrestricted}
					engine := newRoleEngine(probe, mustCompile(t, orgModel(), nil), resolvedLimits(t, authmodel.EvaluationLimits{MaxEvaluationSteps: steps}))
					var got authmodel.LookupResult
					var err error
					principal := authmodel.PrincipalRef{Type: "user", ID: "u"}
					if page {
						got, err = engine.LookupResourcesPage(context.Background(), principal, "view", "organization", "", 1)
					} else {
						got, err = engine.LookupResources(context.Background(), principal, "view", "organization")
					}
					if steps == 3 {
						if !errors.Is(err, authmodel.ErrEvaluationLimit) || probe.reads != 0 || got.IDs != nil || got.Unrestricted {
							t.Fatalf("insufficient budget: %+v %v reads=%d", got, err, probe.reads)
						}
					} else if err != nil || probe.reads != 1 || got.Unrestricted != unrestricted || got.IDs == nil {
						t.Fatalf("exact boundary: %+v %v reads=%d", got, err, probe.reads)
					}
				})
			}
		}
	}
}
