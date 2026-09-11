package decisions

import (
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

func mustCompile(t *testing.T, m authmodel.RoleModel, d authmodel.Declarer) *authmodel.CompiledRoleModel {
	t.Helper()
	c, err := authmodel.CompileRoleModel(m, d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
