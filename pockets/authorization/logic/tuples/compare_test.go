package tuples

import "testing"

func TestCompareFullIdentityByteOrder(t *testing.T) {
	base := Tuple{Scope: On("doc", "d"), Relation: "viewer", Subject: SubjectRef{Type: "group", ID: "g", Relation: "member"}}
	for _, change := range []struct {
		name string
		set  func(*Tuple)
	}{
		{"scope", func(v *Tuple) { v.Scope = Global() }},
		{"resource type", func(v *Tuple) { v.Scope.Type = "do" }},
		{"resource id", func(v *Tuple) { v.Scope.ID = "D" }},
		{"relation", func(v *Tuple) { v.Relation = "view" }},
		{"subject type", func(v *Tuple) { v.Subject.Type = "grou" }},
		{"subject id", func(v *Tuple) { v.Subject.ID = "G" }},
		{"subject relation", func(v *Tuple) { v.Subject.Relation = "" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			less := base
			change.set(&less)
			if Compare(less, base) >= 0 || Compare(base, less) <= 0 || Compare(less, less) != 0 {
				t.Fatalf("incorrect comparison: %+v and %+v", less, base)
			}
		})
	}
	upper, lower := base, base
	upper.Scope.ID, lower.Scope.ID = "Z", "a"
	if Compare(upper, lower) >= 0 {
		t.Fatal("comparison did not use byte order")
	}
	lower.Scope.ID = "é"
	if Compare(base, lower) >= 0 {
		t.Fatal("UTF-8 bytes were not preserved")
	}
}
