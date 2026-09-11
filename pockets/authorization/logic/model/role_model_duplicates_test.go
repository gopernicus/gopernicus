package model

import (
	"errors"
	"strings"
	"testing"
)

func TestCompileRoleModelRejectsDuplicateGrantor(t *testing.T) {
	model := gpsModel()
	model.ResourceTypes["organization"].Permissions["view"] = []string{"viewer", "contributor", "steward", "viewer"}
	_, err := CompileRoleModel(model, nil)
	if !errors.Is(err, ErrInvalidRoleModel) || !strings.Contains(err.Error(), `lists granting role "viewer" twice`) {
		t.Fatalf("CompileRoleModel = %v; want duplicate grantor refusal", err)
	}
}
