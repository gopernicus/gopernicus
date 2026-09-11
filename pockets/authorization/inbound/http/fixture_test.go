package authorizationhttp

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
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

func newDecisionFixture(t *testing.T, rel *testRelationships, roleReader decisions.RoleReader, roleModel authmodel.RoleModel, limits authmodel.EvaluationLimits) *decisions.Service {
	t.Helper()
	var service *relationships.Service
	if rel != nil {
		service = rel.Service
	}
	svc, err := decisions.NewService(decisions.Readers{Relationships: service, Roles: roleReader}, decisions.WithRoleModel(roleModel), decisions.WithLimits(limits))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}
