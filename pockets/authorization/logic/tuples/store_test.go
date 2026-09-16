package tuples

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestRawReadSelectors(t *testing.T) {
	subject := SubjectRef{Type: "group", ID: "g", Relation: "member"}
	global := Global()
	resource := On("doc", "d")
	for _, q := range []Query{{}, {Scope: &global}, {ResourceType: "doc", Scope: &resource}, {Subject: &subject}} {
		if err := q.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []Query{{Scope: &global, ResourceType: "doc"}, {Scope: &resource, ResourceType: "folder"}, {Subject: &subject, ConcreteOnly: true}, {Limit: -1}, {Relation: "bad\x01name"}} {
		if !errors.Is(q.Validate(), sdk.ErrInvalidInput) {
			t.Fatalf("accepted %+v", q)
		}
	}
	scoped := Tuple{Scope: resource, Relation: "member", Subject: subject}
	g := scoped
	g.Scope = global
	if (Query{ResourceType: "doc"}).Matches(g) || !(Query{ResourceType: "doc"}).Matches(scoped) || (Query{ConcreteOnly: true}).Matches(scoped) {
		t.Fatal("scope or concrete filter mismatch")
	}
	for _, k := range []SetKey{{Scope: global, Relation: "admin"}, {Reverse: true, Subject: subject}} {
		if err := k.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []SetKey{{}, {Reverse: true, Subject: subject, Scope: global}, {Scope: resource, Relation: "member", Subject: subject}, {Reverse: true}} {
		if !errors.Is(k.Validate(), sdk.ErrInvalidInput) {
			t.Fatalf("accepted %+v", k)
		}
	}
}

func TestQueryTypedContinuationAndIndependentSubjectFilters(t *testing.T) {
	global, scoped := Global(), On("doc", "d")
	subject := SubjectRef{Type: "group", ID: "g", Relation: "member"}
	fact := Tuple{Scope: scoped, Relation: "viewer", Subject: subject}
	for _, q := range []Query{
		{After: &fact}, {ResourceOnly: true}, {ResourceOnly: true, Scope: &scoped},
		{SubjectType: "group"}, {SubjectID: "g"},
		{Subject: &subject, SubjectType: "group", SubjectID: "g"},
	} {
		if err := q.Validate(); err != nil {
			t.Fatalf("valid query %+v: %v", q, err)
		}
	}
	for _, q := range []Query{
		{After: &Tuple{}}, {ResourceOnly: true, Scope: &global},
		{SubjectType: "bad\x01type"}, {SubjectID: "bad\x01id"},
		{Subject: &subject, SubjectType: "user"}, {Subject: &subject, SubjectID: "other"},
	} {
		if err := q.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid query accepted %+v: %v", q, err)
		}
	}
	for _, q := range []Query{{ResourceOnly: true}, {SubjectType: "group"}, {SubjectID: "g"}, {Subject: &subject, SubjectID: "g"}} {
		if !q.Matches(fact) {
			t.Fatalf("matching query rejected %+v", q)
		}
	}
	for _, q := range []Query{{SubjectType: "user"}, {SubjectID: "other"}, {ConcreteOnly: true}} {
		if q.Matches(fact) {
			t.Fatalf("unmatched query accepted %+v", q)
		}
	}
	fact.Scope = global
	if (Query{ResourceOnly: true}).Matches(fact) {
		t.Fatal("resource-only query matched global fact")
	}
}
func TestScopeJSONExplicitAndLossless(t *testing.T) {
	for _, scope := range []Scope{Global(), On("doc", "d")} {
		b, err := json.Marshal(scope)
		if err != nil {
			t.Fatal(err)
		}
		var got Scope
		if err := json.Unmarshal(b, &got); err != nil || got != scope {
			t.Fatalf("%s => %+v %v", b, got, err)
		}
	}
	for _, raw := range []string{`{"kind":1}`, `{"kind":""}`, `{"kind":"unknown"}`, `{"kind":null}`} {
		var s Scope
		if err := json.Unmarshal([]byte(raw), &s); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	var omitted Scope
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Validate() == nil {
		t.Fatal("omitted scope became global")
	}
}
