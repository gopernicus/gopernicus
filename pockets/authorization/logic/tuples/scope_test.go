package tuples_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func TestScopeValidate(t *testing.T) {
	tests := []struct {
		name  string
		scope tuples.Scope
		valid bool
	}{
		{"global", tuples.Global(), true},
		{"resource", tuples.On("project", "p1"), true},
		{"zero", tuples.Scope{}, false},
		{"unknown kind", tuples.Scope{Kind: 255}, false},
		{"untagged resource", tuples.Scope{Type: "project", ID: "p1"}, false},
		{"unknown tagged resource", tuples.Scope{Kind: 255, Type: "project", ID: "p1"}, false},
		{"global with type", tuples.Scope{Kind: tuples.GlobalScope, Type: "project"}, false},
		{"global with id", tuples.Scope{Kind: tuples.GlobalScope, ID: "p1"}, false},
		{"global with both", tuples.Scope{Kind: tuples.GlobalScope, Type: "project", ID: "p1"}, false},
		{"resource without coordinates", tuples.On("", ""), false},
		{"resource without type", tuples.On("", "p1"), false},
		{"resource without id", tuples.On("project", ""), false},
		{"resource invalid type", tuples.On("project\n", "p1"), false},
		{"resource invalid id", tuples.On("project", "\xff"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scope.Validate()
			if tt.valid {
				if err != nil {
					t.Fatalf("Validate(): %v", err)
				}
				return
			}
			requireInvalidRef(t, err)
		})
	}
}

func TestScopeConstructorsPreserveIntent(t *testing.T) {
	if got := tuples.Global(); got != (tuples.Scope{Kind: tuples.GlobalScope}) {
		t.Fatalf("Global() = %+v", got)
	}
	for _, coordinates := range [][2]string{{"", ""}, {"", "x"}, {"x", ""}, {" Project ", "e\u0301"}, {"\xff", "\x00"}} {
		got := tuples.On(coordinates[0], coordinates[1])
		if got.Kind != tuples.ResourceScope || got.Type != coordinates[0] || got.ID != coordinates[1] {
			t.Fatalf("On(%q, %q) changed the input: %+v", coordinates[0], coordinates[1], got)
		}
		if got == tuples.Global() {
			t.Fatalf("resource constructor produced global scope: %+v", got)
		}
	}
}
