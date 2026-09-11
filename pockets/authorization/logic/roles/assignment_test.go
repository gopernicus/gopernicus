package roles

import (
	"errors"
	"strings"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestAssignmentValidateReferences(t *testing.T) {
	valid := Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor", ResourceType: "doc", ResourceID: "d1"}
	fields := []struct {
		name string
		set  func(*Assignment, string)
	}{
		{"subject type", func(a *Assignment, v string) { a.SubjectType = v }},
		{"subject id", func(a *Assignment, v string) { a.SubjectID = v }},
		{"role", func(a *Assignment, v string) { a.Role = v }},
		{"resource type", func(a *Assignment, v string) { a.ResourceType = v }},
		{"resource id", func(a *Assignment, v string) { a.ResourceID = v }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			for _, bad := range []string{"", "a\x01b", "a\x00b", "a\nb", "\xff", strings.Repeat("a", authmodel.MaxRefFieldLen+1)} {
				a := valid
				field.set(&a, bad)
				if err := a.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("Validate(%q) = %v, want invalid input", bad, err)
				}
			}
			a := valid
			field.set(&a, " Équipe:one/#. ")
			before := a
			if err := a.Validate(); err != nil || a != before {
				t.Fatalf("opaque reference changed or rejected: %+v, %v", a, err)
			}
		})
	}
	global := valid
	global.ResourceType, global.ResourceID = "", ""
	if err := global.Validate(); err != nil {
		t.Fatalf("global assignment: %v", err)
	}
}
