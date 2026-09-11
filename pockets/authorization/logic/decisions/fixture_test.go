package decisions

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

type testRoles struct {
	*roles.Service
	*roles.Writer
}

func newTestRoles(t *testing.T, store roles.Storer) *testRoles {
	t.Helper()
	svc, err := roles.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := roles.NewWriter(store)
	if err != nil {
		t.Fatal(err)
	}
	return &testRoles{svc, writer}
}

type testRelationships struct {
	*relationships.Service
	*relationships.RelationshipWriter
}
