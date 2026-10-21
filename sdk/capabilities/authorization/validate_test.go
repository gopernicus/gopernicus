package authorization_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

func TestValidateRefAccepts(t *testing.T) {
	for _, c := range []struct{ kind, value string }{
		{authorization.KindType, "abc"},
		{authorization.KindType, "client_hub"},
		{authorization.KindType, "service_account"},
		{authorization.KindType, "a" + strings.Repeat("b", 62) + "c"},
		{authorization.KindRelation, "view_timeline_space"},
		{authorization.KindRelation, "role_firmwide_clients"},
		{authorization.KindRelation, "a" + strings.Repeat("b", 48) + "c"},
		{authorization.KindID, "data-operations"},
		{authorization.KindID, "0198c3a2-7b6e-7f00-9c1d-2a4b6c8d0e1f"},
		{authorization.KindID, "a/b_c|d-e=f+G9"},
		{authorization.KindID, "x"},
		{authorization.KindID, strings.Repeat("a", 256)},
	} {
		if err := authorization.ValidateRef(c.kind, c.value); err != nil {
			t.Errorf("ValidateRef(%s, %q) = %v", c.kind, c.value, err)
		}
	}
}

func TestValidateRefRejects(t *testing.T) {
	for _, c := range []struct{ kind, value string }{
		{authorization.KindType, ""},
		{authorization.KindType, "ab"},
		{authorization.KindType, "Client"},
		{authorization.KindType, "1abc"},
		{authorization.KindType, "abc_"},
		{authorization.KindType, "user:*"},
		{authorization.KindType, "a" + strings.Repeat("b", 63) + "c"},
		{authorization.KindRelation, "can-view"},
		{authorization.KindRelation, "a" + strings.Repeat("b", 49) + "c"},
		{authorization.KindRelation, "member#x"},
		{authorization.KindID, ""},
		{authorization.KindID, "*"},
		{authorization.KindID, "a@b.c"},
		{authorization.KindID, "org.dot"},
		{authorization.KindID, "a:b"},
		{authorization.KindID, "é"},
		{authorization.KindID, strings.Repeat("a", 257)},
		{"label", "abc"},
	} {
		if err := authorization.ValidateRef(c.kind, c.value); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ValidateRef(%s, %q) = %v, want sdk.ErrInvalidInput", c.kind, c.value, err)
		}
	}
}

// Path-parameter names are not identifiers: real host names fail the grammar,
// which is why consumers never apply it to them.
func TestPathParameterNamesAreNotIdentifiers(t *testing.T) {
	for _, name := range []string{"organizationId", "timelineID", "spaceID", "tenantID", "programId"} {
		if authorization.ValidateRef(authorization.KindRelation, name) == nil {
			t.Errorf("%q unexpectedly passes the relation grammar", name)
		}
	}
}

func TestValidateObjectCombinedCap(t *testing.T) {
	if err := authorization.ValidateObject("client_hub", strings.Repeat("a", 245)); err != nil {
		t.Fatalf("256-byte object rejected: %v", err)
	}
	err := authorization.ValidateObject("client_hub", strings.Repeat("a", 246))
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("257-byte object = %v, want sdk.ErrInvalidInput", err)
	}
	if err := authorization.ValidateObject("doc", "*"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("wildcard id = %v", err)
	}
}

func TestRequestValidate(t *testing.T) {
	alice := authorization.Principal{Type: "user", ID: "alice"}
	ok := authorization.CheckRequest{Principal: alice, Permission: "view", Resource: authorization.Resource{Type: "document", ID: "d1"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]authorization.CheckRequest{
		"wildcard principal": {Principal: authorization.Principal{Type: "user", ID: "*"}, Permission: "view", Resource: ok.Resource},
		"empty permission":   {Principal: alice, Resource: ok.Resource},
		"bad resource type":  {Principal: alice, Permission: "view", Resource: authorization.Resource{Type: "Doc", ID: "d1"}},
		"empty resource id":  {Principal: alice, Permission: "view", Resource: authorization.Resource{Type: "document"}},
	} {
		if err := req.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	lookup := authorization.LookupRequest{Principal: alice, Permission: "view", ResourceType: "document"}
	if err := lookup.Validate(); err != nil {
		t.Fatal(err)
	}
	lookup.Limit = -1
	if err := lookup.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("negative limit: %v", err)
	}
}

func TestSentinels(t *testing.T) {
	if !errors.Is(authorization.ErrInvalidCursor, sdk.ErrInvalidInput) {
		t.Fatal("ErrInvalidCursor must wrap sdk.ErrInvalidInput")
	}
	if sdk.IsExpected(authorization.ErrConditional) {
		t.Fatal("ErrConditional must wrap no sdk class")
	}
}
