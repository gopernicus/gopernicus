package roles

import (
	"context"
	"errors"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestValidationBeforeStore(t *testing.T) {
	for _, a := range []Assignment{
		{SubjectType: "bad\x01type", SubjectID: "u", Role: "owner", Scope: tuples.Global()},
		{SubjectType: "user", SubjectID: "bad\x00id", Role: "owner", Scope: tuples.Global()},
		{SubjectType: "user", SubjectID: "u", Role: "", Scope: tuples.Global()},
		{SubjectType: "user", SubjectID: "u", Role: "owner"},
		{SubjectType: "user", SubjectID: "u", Role: "owner", Scope: tuples.On("doc", "")},
	} {
		f := &fakeRoleStore{}
		s := newRoleFixture(t, f)
		if e := s.AssignRole(context.Background(), a); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatalf("assign %+v: %v", a, e)
		}
		if e := s.UnassignRole(context.Background(), a); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatalf("unassign %+v: %v", a, e)
		}
		if f.writes != 0 {
			t.Fatal("invalid assignment reached store")
		}
	}
	f := &fakeRoleStore{}
	s := newRoleFixture(t, f)
	if _, e := s.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u"}, "bad\nrole"); !errors.Is(e, sdk.ErrInvalidInput) {
		t.Fatal(e)
	}
	if _, e := s.HasRoleIn(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u"}, "owner", authmodel.Resource{}); !errors.Is(e, sdk.ErrInvalidInput) {
		t.Fatal(e)
	}
	if f.snapshots != 0 {
		t.Fatal("invalid check opened snapshot")
	}
}
