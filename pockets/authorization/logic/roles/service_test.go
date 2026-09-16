package roles

import (
	"context"
	"errors"
	"reflect"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestExactRoleScopeAndCoexistence(t *testing.T) {
	ctx := context.Background()
	f := &fakeRoleStore{}
	s := newRoleFixture(t, f)
	p := authmodel.PrincipalRef{Type: "user", ID: "u"}
	r := authmodel.Resource{Type: "doc", ID: "d"}
	global := Assignment{SubjectType: p.Type, SubjectID: p.ID, Role: "owner", Scope: tuples.Global()}
	for range 2 {
		if e := s.AssignRole(ctx, global); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.rows) != 1 {
		t.Fatal("duplicate fact")
	}
	if ok, e := s.HasRole(ctx, p, "owner"); e != nil || !ok {
		t.Fatalf("global: %v %v", ok, e)
	}
	if ok, e := s.HasRoleIn(ctx, p, "owner", r); e != nil || ok {
		t.Fatalf("implicit global fallback: %v %v", ok, e)
	}
	before := f.snapshots
	if ok, e := s.HasRoleInOrGlobal(ctx, p, "owner", r); e != nil || !ok {
		t.Fatalf("explicit union: %v %v", ok, e)
	}
	if f.snapshots != before+1 || len(f.batch) != 2 || f.batch[0].Scope != tuples.On("doc", "d") || f.batch[1].Scope != tuples.Global() {
		t.Fatalf("union did not share one view: %+v", f.batch)
	}
	scoped := global
	scoped.Scope = tuples.On("doc", "d")
	member := scoped
	member.Role = "member"
	for _, a := range []Assignment{scoped, member} {
		if e := s.AssignRole(ctx, a); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.rows) != 3 {
		t.Fatalf("owner/member coexistence: %v", f.rows)
	}
	if ok, e := s.HasRoleIn(ctx, p, "owner", r); e != nil || !ok {
		t.Fatalf("scoped: %v %v", ok, e)
	}
	if ok, e := s.HasRoleIn(ctx, p, "owner", authmodel.Resource{Type: "doc", ID: "other"}); e != nil || ok {
		t.Fatalf("wrong scope: %v %v", ok, e)
	}
	if e := s.UnassignRole(ctx, scoped); e != nil {
		t.Fatal(e)
	}
	if f.rows[scoped.Tuple()] || !f.rows[member.Tuple()] || !f.rows[global.Tuple()] {
		t.Fatal("unassign removed another fact")
	}
	userset := scoped.Tuple()
	userset.Subject.Relation = "member"
	f.rows[userset] = true
	if ok, e := s.HasRoleIn(ctx, p, "owner", r); e != nil || ok {
		t.Fatalf("exact role expanded userset: %v %v", ok, e)
	}
}
func TestRoleChecksFailClosed(t *testing.T) {
	boom := errors.New("snapshot failed")
	for _, failure := range []string{"read", "completion", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakeRoleStore{rows: map[tuples.Tuple]bool{(Assignment{SubjectType: "user", SubjectID: "u", Role: "admin", Scope: tuples.Global()}).Tuple(): true}}
			want := boom
			switch failure {
			case "read":
				f.err = boom
			case "completion":
				f.completion = boom
			case "cancel":
				f.cancel = cancel
				want = context.Canceled
			}
			s := newRoleFixture(t, f)
			if ok, e := s.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: "u"}, "admin"); ok || !errors.Is(e, want) {
				t.Fatalf("provisional allow escaped: %v %v", ok, e)
			}
		})
	}
}
func TestRoleListingsAreExactTupleViews(t *testing.T) {
	global := (Assignment{SubjectType: "user", SubjectID: "u", Role: "admin", Scope: tuples.Global()}).Tuple()
	f := &fakeRoleStore{page: list.Page[tuples.Tuple]{Items: []tuples.Tuple{global}, NextCursor: "next", PreviousCursor: "prev", HasMore: true, HasPrev: true}}
	s := newRoleFixture(t, f)
	ctx := context.Background()
	page, e := s.ListRoleAssignmentsBySubject(ctx, authmodel.PrincipalRef{Type: "user", ID: "u"}, list.Request{})
	if e != nil || len(page.Items) != 1 || page.Items[0].Tuple() != global || page.NextCursor != "next" || page.PreviousCursor != "prev" || !page.HasMore || !page.HasPrev {
		t.Fatalf("page: %+v %v", page, e)
	}
	if f.query.Subject == nil || *f.query.Subject != global.Subject {
		t.Fatalf("subject filter: %+v", f.query)
	}
	scope := tuples.On("doc", "d")
	if _, e := s.ListRoleAssignmentsByScope(ctx, scope, list.Request{}); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(f.query, tuples.Query{Scope: &scope, ConcreteOnly: true}) {
		t.Fatalf("scope filter: %+v", f.query)
	}
}
