package mutations

import (
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// AssignRoleCommand grants exact concrete membership in an explicit scope.
type AssignRoleCommand struct {
	Subject authmodel.PrincipalRef
	Role    string
	Scope   tuples.Scope
}
type UnassignRoleCommand struct {
	Subject authmodel.PrincipalRef
	Role    string
	Scope   tuples.Scope
}
type UnassignRoleResult struct{ Outcome Outcome }
