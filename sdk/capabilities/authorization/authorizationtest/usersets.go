package authorizationtest

import "github.com/gopernicus/gopernicus/sdk"

// Usersets exercises group#member expansion, nested groups, and a concrete
// grant beside a userset grant.
func Usersets() Fixture {
	return Fixture{
		Name: "usersets",
		Facts: []Fact{
			fact("group", "eng", "member", "user", "ann"),
			userset("group", "all", "member", "group", "eng", "member"),
			userset("document", "spec", "viewer", "group", "all", "member"),
			fact("document", "memo", "viewer", "user", "bob"),
			fact("document", "spec", "viewer", "user", "cid"),
		},
		Checks: []CheckCase{
			check("nested userset expansion", user("ann"), "view", res("document", "spec"), true),
			check("relation through a userset", user("ann"), "viewer", res("document", "spec"), true),
			check("group member relation", user("ann"), "member", res("group", "all"), true),
			check("non-member denied", user("bob"), "view", res("document", "spec"), false),
			check("direct grant", user("bob"), "view", res("document", "memo"), true),
			check("concrete grant beside a userset", user("cid"), "view", res("document", "spec"), true),
			checkErr("userset principal is not nameable", user("all#member"), "view", res("document", "spec"), sdk.ErrInvalidInput),
		},
		Lookups: []LookupCase{
			lookup("lookup through a userset", user("ann"), "view", "document", 10, "spec"),
			lookup("lookup direct", user("bob"), "view", "document", 10, "memo"),
		},
	}
}
