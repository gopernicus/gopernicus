package tuples_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

func TestTupleFullIdentity(t *testing.T) {
	base := tuples.Tuple{Scope: tuples.On("project", "p1"), Relation: "editor", Subject: tuples.SubjectRef{Type: "group", ID: "eng"}}
	facts := []tuples.Tuple{base}
	for _, change := range []func(*tuples.Tuple){
		func(f *tuples.Tuple) { f.Scope = tuples.Global() },
		func(f *tuples.Tuple) { f.Scope.Type = "folder" },
		func(f *tuples.Tuple) { f.Scope.ID = "p2" },
		func(f *tuples.Tuple) { f.Relation = "reviewer" },
		func(f *tuples.Tuple) { f.Subject.Type = "team" },
		func(f *tuples.Tuple) { f.Subject.ID = "ops" },
		func(f *tuples.Tuple) { f.Subject.Relation = "member" },
		func(f *tuples.Tuple) { f.Subject.Relation = "admin" },
	} {
		fact := base
		change(&fact)
		facts = append(facts, fact)
	}
	set := make(map[tuples.Tuple]bool)
	for _, fact := range facts {
		if err := fact.Validate(); err != nil {
			t.Fatalf("invalid fixture %+v: %v", fact, err)
		}
		if set[fact] {
			t.Fatalf("distinct fact shares an identity: %+v", fact)
		}
		set[fact] = true
	}
	set[base] = true
	if len(set) != len(facts) {
		t.Fatalf("duplicate insertion changed fact count: %d, want %d", len(set), len(facts))
	}
}

func TestTupleValidate(t *testing.T) {
	for _, scope := range []tuples.Scope{tuples.Global(), tuples.On("project", "p1")} {
		for _, subjectRelation := range []string{"", "member"} {
			fact := tuples.Tuple{Scope: scope, Relation: "editor", Subject: tuples.SubjectRef{Type: "group", ID: "eng", Relation: subjectRelation}}
			if err := fact.Validate(); err != nil {
				t.Fatalf("valid fact %+v: %v", fact, err)
			}
		}
	}
	base := tuples.Tuple{Scope: tuples.On("project", "p1"), Relation: "editor", Subject: tuples.SubjectRef{Type: "group", ID: "eng"}}
	tests := []struct {
		name   string
		change func(*tuples.Tuple)
	}{
		{"scope", func(f *tuples.Tuple) { f.Scope = tuples.Scope{} }},
		{"resource type", func(f *tuples.Tuple) { f.Scope.Type = "" }},
		{"resource id", func(f *tuples.Tuple) { f.Scope.ID = "" }},
		{"relation", func(f *tuples.Tuple) { f.Relation = "" }},
		{"malformed relation", func(f *tuples.Tuple) { f.Relation = "read\x00" }},
		{"subject type", func(f *tuples.Tuple) { f.Subject.Type = "" }},
		{"subject id", func(f *tuples.Tuple) { f.Subject.ID = "" }},
		{"subject relation", func(f *tuples.Tuple) { f.Subject.Relation = "member\n" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fact := base
			tt.change(&fact)
			requireInvalidRef(t, fact.Validate())
		})
	}
}

func TestSubjectRef(t *testing.T) {
	concrete := tuples.SubjectRef{Type: "group", ID: "eng"}
	userset := tuples.SubjectRef{Type: "group", ID: "eng", Relation: "member"}
	if concrete.IsUserset() || !userset.IsUserset() {
		t.Fatal("subject relation must distinguish concrete subjects from usersets")
	}
	if concrete.String() != "group:eng" || userset.String() != "group:eng#member" {
		t.Fatalf("unexpected debug strings: %q, %q", concrete, userset)
	}
	// Debug strings can collide; comparable values must remain distinct.
	a := tuples.SubjectRef{Type: "group:eng", ID: "member"}
	b := tuples.SubjectRef{Type: "group", ID: "eng:member"}
	if a.String() != b.String() || a == b {
		t.Fatal("debug rendering must not define subject identity")
	}
}

func FuzzTupleOpaqueIdentity(f *testing.F) {
	for _, seed := range [][2]string{
		{"editor", "reviewer"},
		{"editor", "Editor"},
		{"editor", " editor "},
		{"é", "e\u0301"},
		{"a:b#c", "a#b:c"},
		{"東京", "東京"},
		{"", "\xff"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		base := tuples.Tuple{Scope: tuples.On("project", "p1"), Relation: "editor", Subject: tuples.SubjectRef{Type: "group", ID: "eng", Relation: "member"}}
		for _, field := range []struct {
			set        func(*tuples.Tuple, string)
			allowEmpty bool
		}{
			{func(f *tuples.Tuple, s string) { f.Scope.Type = s }, false},
			{func(f *tuples.Tuple, s string) { f.Scope.ID = s }, false},
			{func(f *tuples.Tuple, s string) { f.Relation = s }, false},
			{func(f *tuples.Tuple, s string) { f.Subject.Type = s }, false},
			{func(f *tuples.Tuple, s string) { f.Subject.ID = s }, false},
			{func(f *tuples.Tuple, s string) { f.Subject.Relation = s }, true},
		} {
			left, right := base, base
			field.set(&left, a)
			field.set(&right, b)
			for _, value := range []string{a, b} {
				fact := base
				field.set(&fact, value)
				valid := (field.allowEmpty && value == "") || tuples.ValidateRefField("input", value) == nil
				err := fact.Validate()
				if valid && err != nil {
					t.Fatalf("valid opaque field rejected: %v", err)
				}
				if !valid {
					requireInvalidRef(t, err)
				}
			}
			if (left == right) != (a == b) {
				t.Fatalf("opaque field lost its exact identity: %q, %q", a, b)
			}
		}
	})
}
