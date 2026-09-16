package documents

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

type readerFunc func(context.Context, Query, Position, int, Restriction) ([]Row, bool, error)

func (f readerFunc) Read(ctx context.Context, query Query, after Position, limit int, restriction Restriction) ([]Row, bool, error) {
	return f(ctx, query, after, limit, restriction)
}

func TestReadValidatesBusinessQueryAndExplicitRestriction(t *testing.T) {
	membership := &ExactMembership{SubjectType: "machine", SubjectID: "importer", Relation: "editor"}
	restriction := Restriction{Membership: membership}
	after := Position{NameKey: "alpha", ID: "a"}
	row := Row{ID: "b", TenantID: "tenant", Name: "BETA", NameKey: "beta"}
	service, err := New(readerFunc(func(_ context.Context, query Query, gotAfter Position, limit int, got Restriction) ([]Row, bool, error) {
		if query != (Query{TenantID: "tenant", Search: "beta", Desc: true, Limit: 1}) || gotAfter != after || limit != 150 || !reflect.DeepEqual(got, restriction) {
			t.Fatalf("read input: %+v %+v %d %+v", query, gotAfter, limit, got)
		}
		return []Row{row}, true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	// The service requires no authenticated principal and chooses no policy. The
	// adapter-selected pull size can exceed the HTTP page size and its 100 cap.
	rows, more, err := service.Read(t.Context(), Query{TenantID: " tenant ", Search: " BETA ", Desc: true, Limit: 1}, after, 150, restriction)
	if err != nil || !more || !reflect.DeepEqual(rows, []Row{row}) {
		t.Fatalf("read: %+v %v %v", rows, more, err)
	}
}

func TestReadRejectsMalformedRestrictionsAndZeroMatchesNothing(t *testing.T) {
	service, err := New(readerFunc(func(context.Context, Query, Position, int, Restriction) ([]Row, bool, error) {
		t.Fatal("empty or malformed restriction reached storage")
		return nil, false, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	query := Query{TenantID: "a", Limit: 1}
	rows, more, err := service.Read(t.Context(), query, Position{}, 1, Restriction{})
	if err != nil || more || rows == nil || len(rows) != 0 {
		t.Fatalf("zero restriction: %+v %v %v", rows, more, err)
	}
	membership := &ExactMembership{SubjectType: "user", SubjectID: "alice", Relation: "viewer"}
	for _, restriction := range []Restriction{
		{Unrestricted: true, IDs: []string{"a"}},
		{Unrestricted: true, Membership: membership},
		{IDs: []string{"a"}, Membership: membership},
		{IDs: []string{""}},
		{Membership: &ExactMembership{SubjectType: "user", Relation: "viewer"}},
	} {
		if _, _, err := service.Read(t.Context(), query, Position{}, 1, restriction); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("malformed restriction %+v: %v", restriction, err)
		}
	}
}
