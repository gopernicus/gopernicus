package mutations

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestFingerprintGolden(t *testing.T) {
	target := Target{Kind: TargetResource, Type: "doc", ID: "d"}
	x := tuples.Tuple{Scope: target.Scope(), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "a"}}
	y := x
	y.Subject.ID = "b"
	for _, tc := range []struct {
		name string
		cmd  Command
		want string
	}{
		{"split", Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{x}, Remove: []tuples.Tuple{y}}}, "07a4eba06483d1117d5778ba9b0649a8a8df8ed9426ff0e5bfdd710ed24c7455"},
		{"all-add", Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{x, y}}}, "769aa7a1a02e4fbe37c61a40973c7a68f4d1bf24a74e57255c1b11442ac66c1f"},
		{"reconcile", Command{Target: target, Operation: OpReconcile, Relation: "viewer", Subjects: []tuples.SubjectRef{x.Subject, y.Subject}}, "2425f560cd1c7c8182ad44d2557e35589328ade2fc02898a9132a50514a61fa4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cmd.Validate(); err != nil {
				t.Fatal(err)
			}
			if got := Fingerprint(tc.cmd); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
			cmd := tc.cmd
			cmd.OperationID = "ignored"
			cmd.MaxAffectedRows = 1
			if got := Fingerprint(cmd); got != Fingerprint(tc.cmd) {
				t.Fatal("execution metadata bound")
			}
			cmd.Tuples.Add = append([]tuples.Tuple(nil), cmd.Tuples.Add...)
			if len(cmd.Tuples.Add) > 0 {
				cmd.Tuples.Add = append(cmd.Tuples.Add, cmd.Tuples.Add[0])
				if len(cmd.Tuples.Add) > 1 {
					cmd.Tuples.Add[0], cmd.Tuples.Add[1] = cmd.Tuples.Add[1], cmd.Tuples.Add[0]
				}
			}
			cmd.Subjects = append([]tuples.SubjectRef(nil), cmd.Subjects...)
			if len(cmd.Subjects) > 0 {
				cmd.Subjects = append(cmd.Subjects, cmd.Subjects[0])
				cmd.Subjects[0], cmd.Subjects[1] = cmd.Subjects[1], cmd.Subjects[0]
			}
			if Fingerprint(cmd) != Fingerprint(tc.cmd) {
				t.Fatal("order/duplicate dependence")
			}
		})
	}
}
func TestOperationIDValidation(t *testing.T) {
	cmd := Command{Target: Target{Kind: TargetResource, Type: "doc", ID: "d"}, Operation: OpPurge}
	for _, id := range []string{"\x00", "\n", string([]byte{255}), strings.Repeat("a", 257)} {
		cmd.OperationID = id
		if !errors.Is(cmd.Validate(), sdk.ErrInvalidInput) {
			t.Fatalf("accepted %q", id)
		}
	}
	cmd.OperationID = "invitation:one"
	if err := cmd.Validate(); err != nil {
		t.Fatal(err)
	}
	cmd.Operation = OpTeardown
	if !errors.Is(cmd.Validate(), ErrInvalidCommand) {
		t.Fatal("teardown accepted ID")
	}
	if reason, ok := ReasonFor(ErrOperationMismatch); !ok || reason != "operation_mismatch" {
		t.Fatalf("reason %q %v", reason, ok)
	}
}

func TestSupersededChecksOnlyRequestedFacts(t *testing.T) {
	target := Target{Kind: TargetResource, Type: "doc", ID: "d"}
	x := tuples.Tuple{Scope: target.Scope(), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "a"}}
	y := x
	y.Subject.ID = "b"
	unrelated := x
	unrelated.Relation = "owner"
	for _, tc := range []struct {
		name   string
		cmd    Command
		before []tuples.Tuple
		want   bool
	}{
		{"add present", Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{x}}, MaxAffectedRows: 1}, []tuples.Tuple{x, unrelated}, false},
		{"add absent", Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Add: []tuples.Tuple{x}}}, nil, true},
		{"remove absent", Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Remove: []tuples.Tuple{x}}}, []tuples.Tuple{unrelated}, false},
		{"remove present", Command{Target: target, Operation: OpBatch, Tuples: tuples.Changes{Remove: []tuples.Tuple{x}}}, []tuples.Tuple{x}, true},
		{"reconcile matches", Command{Target: target, Operation: OpReconcile, Relation: "viewer", Subjects: []tuples.SubjectRef{x.Subject, x.Subject}}, []tuples.Tuple{x, unrelated}, false},
		{"reconcile extra", Command{Target: target, Operation: OpReconcile, Relation: "viewer", Subjects: []tuples.SubjectRef{x.Subject}}, []tuples.Tuple{x, y}, true},
		{"reconcile missing", Command{Target: target, Operation: OpReconcile, Relation: "viewer", Subjects: []tuples.SubjectRef{x.Subject}}, nil, true},
		{"purge empty", Command{Target: target, Operation: OpPurge}, nil, false},
		{"purge populated", Command{Target: target, Operation: OpPurge}, []tuples.Tuple{unrelated}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Superseded(tc.cmd, tc.before); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
