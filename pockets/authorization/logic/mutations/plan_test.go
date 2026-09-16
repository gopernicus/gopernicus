package mutations

import (
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestCanonicalPlanCoexistenceSwapAndReconcile(t *testing.T) {
	target := Target{Kind: TargetResource, Type: "doc", ID: "d"}
	owner := tuples.Tuple{Scope: target.Scope(), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	member := owner
	member.Relation = "member"
	otherOwner := owner
	otherOwner.Subject.ID = "bob"
	policy := DefaultGuardianPolicy()
	before := []tuples.Tuple{owner}
	grant := Command{Target: target, Operation: OpRoleAssign, Roles: []RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "member"}, {SubjectType: "user", SubjectID: "alice", Role: "member"}}}
	delta, out, err := Plan(grant, before, policy)
	if err != nil || out != OutcomeApplied || !reflect.DeepEqual(delta, tuples.Changes{Add: []tuples.Tuple{member}}) {
		t.Fatalf("coexistence: %+v %s %v", delta, out, err)
	}
	swap := Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{otherOwner}, Remove: []tuples.Tuple{owner}}}
	delta, out, err = Plan(swap, []tuples.Tuple{owner, member}, policy)
	if err != nil || out != OutcomeApplied || !reflect.DeepEqual(delta, swap.Tuples) {
		t.Fatalf("atomic guardian transfer: %+v %s %v", delta, out, err)
	}
	reconcile := Command{Target: target, Operation: OpReconcile, Relation: "member", Subjects: []tuples.SubjectRef{{Type: "user", ID: "bob"}, {Type: "user", ID: "bob"}}}
	newMember := member
	newMember.Subject.ID = "bob"
	delta, out, err = Plan(reconcile, []tuples.Tuple{owner, member}, policy)
	if err != nil || out != OutcomeApplied || !reflect.DeepEqual(delta, tuples.Changes{Add: []tuples.Tuple{newMember}, Remove: []tuples.Tuple{member}}) {
		t.Fatalf("reconcile changed unrelated label: %+v %s %v", delta, out, err)
	}
	if before[0] != owner {
		t.Fatal("plan mutated caller state")
	}
}

func TestCanonicalGuardianAppliesToEveryFacadeAndNoop(t *testing.T) {
	target := Target{Kind: TargetResource, Type: "doc", ID: "d"}
	owner := tuples.Tuple{Scope: target.Scope(), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	userset := owner
	userset.Subject.Relation = "member"
	cases := map[string]Command{
		"role":         {Target: target, Operation: OpRoleUnassign, Roles: []RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "owner"}}},
		"relationship": {Target: target, Operation: OpRevoke, Relationships: []RelationshipRow{{Relation: "owner", Subject: owner.Subject}}},
		"batch":        {Target: target, Operation: OpBatch, Tuples: tuples.Changes{Remove: []tuples.Tuple{owner}, Add: []tuples.Tuple{userset}}},
		"reconcile":    {Target: target, Operation: OpReconcile, Relation: "owner", Subjects: []tuples.SubjectRef{userset.Subject}},
		"purge":        {Target: target, Operation: OpPurge},
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			for _, before := range [][]tuples.Tuple{{owner}, nil} {
				d, _, err := Plan(cmd, before, DefaultGuardianPolicy())
				if !errors.Is(err, ErrInvariantBlocked) || len(d.Add)+len(d.Remove) != 0 {
					t.Fatalf("unprotected poststate: %+v %v", d, err)
				}
			}
		})
	}
	d, out, err := Plan(Command{Target: target, Operation: OpTeardown}, []tuples.Tuple{owner}, DefaultGuardianPolicy())
	if err != nil || out != OutcomeApplied || len(d.Remove) != 1 {
		t.Fatalf("explicit teardown: %+v %s %v", d, out, err)
	}
}

func TestCanonicalCommandBoundsAndIdentity(t *testing.T) {
	target := Target{Kind: TargetResource, Type: "doc", ID: "d"}
	a := tuples.Tuple{Scope: target.Scope(), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	b := a
	b.Relation = "member"
	cmd := Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{a}, Remove: []tuples.Tuple{a}}}
	if err := cmd.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("contradictory delta accepted: %v", err)
	}
	cmd.Tuples = tuples.Changes{Add: []tuples.Tuple{b}, Remove: []tuples.Tuple{a}}
	cmd.MaxAffectedRows = 1
	if d, _, err := Plan(cmd, []tuples.Tuple{a}, GuardianPolicy{}); !errors.Is(err, ErrInvariantBlocked) || len(d.Add)+len(d.Remove) != 0 {
		t.Fatalf("partial over-limit delta: %+v %v", d, err)
	}
	cmd.MaxAffectedRows = 2
	if _, _, err := Plan(cmd, []tuples.Tuple{a}, GuardianPolicy{}); err != nil {
		t.Fatal(err)
	}
	foreign := a
	foreign.Scope = tuples.Global()
	if _, _, err := Plan(cmd, []tuples.Tuple{foreign}, GuardianPolicy{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("foreign target accepted: %v", err)
	}
	cmd.Tuples.Add = make([]tuples.Tuple, MaxCommandTuples+1)
	for i := range cmd.Tuples.Add {
		cmd.Tuples.Add[i] = b
	}
	if err := cmd.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unbounded duplicate input accepted: %v", err)
	}
}

func TestGuardianModelMustPermitConcreteAnchor(t *testing.T) {
	policy := DefaultGuardianPolicy()
	m := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"doc":   {Relations: map[string]decisions.RelationDef{"owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "group", Relation: "member"}}}}},
		"group": {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}},
	}}
	engine, err := decisions.NewService(&stubDecisionView{}, decisions.WithModel(m))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGuardianModel(policy, engine); !errors.Is(err, ErrInvalidGuardianPolicy) {
		t.Fatalf("impossible guardian: %v", err)
	}
	if err := validateGuardianModel(GuardianPolicy{Rules: []GuardianRule{{Relation: "opaque"}}}, engine); err != nil {
		t.Fatalf("opaque label: %v", err)
	}
	m.ResourceTypes["doc"].Relations["owner"] = decisions.RelationDef{AllowedSubjects: []decisions.SubjectTypeRef{{Type: "group", Relation: "member"}, {Type: "user"}}}
	engine, err = decisions.NewService(&stubDecisionView{}, decisions.WithModel(m))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGuardianModel(policy, engine); err != nil {
		t.Fatalf("possible guardian: %v", err)
	}
}
