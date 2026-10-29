package authorizationtest

import "github.com/gopernicus/gopernicus/sdk"

// GlobalRoles is a role-heavy model with global roles. Global roles are the
// relations role_<label> on the global resource platform:global; every
// resource whose permission has a global branch carries a link tuple
// <type>:<id>#platform@platform:global; scoped role labels are declared
// relations, so the protocol can answer relation questions about them.
//
// client_hub:org3 deliberately has no link tuple: a member still finds it, a
// global steward does not through a vendor (asserted as a divergence).
func GlobalRoles() Fixture {
	f := Fixture{Name: "global-roles", Global: &GlobalResource{Type: "platform", ID: "global", RelationPrefix: "role_"}}
	link := func(resourceType, id string) Fact { return fact(resourceType, id, "platform", "platform", "global") }
	f.Facts = []Fact{
		fact("platform", "global", "role_steward", "user", "stella"),
		fact("platform", "global", "role_firmwide_clients", "user", "fern"),
		fact("platform", "global", "role_developer", "user", "dev"),
		fact("platform", "global", "role_librarian", "user", "libby"),
		link("client_hub", "org1"), link("client_hub", "org2"),
		fact("client_hub", "org1", "viewer", "user", "vin"),
		fact("client_hub", "org1", "member", "user", "mo"),
		fact("client_hub", "org3", "member", "user", "mo"),
		fact("client_hub", "org1", "member", "service_account", "bot"),
		link("section", "operations"), link("section", "workshop"), link("section", "data-operations"),
		fact("section", "operations", "editor", "user", "ed"),
		fact("section", "workshop", "member", "user", "wes"),
		fact("section", "data-operations", "member", "user", "mo"),
		link("page", "contact-proposals"), link("page", "mailbox-scan"),
		fact("page", "contact-proposals", "viewer", "user", "pv"),
		fact("page", "mailbox-scan", "viewer", "user", "pv"),
		fact("page", "contact-proposals", "viewer", "user", "half"),
		fact("ledger", "main", "editor", "user", "led"),
		fact("ledger", "main", "viewer", "user", "lev"),
	}
	platform := res("platform", "global")
	bot := sdk.Principal{Type: "service_account", ID: "bot"}
	f.Checks = []CheckCase{
		check("steward permission", user("stella"), "steward", platform, true),
		check("steward permission denied", user("dev"), "steward", platform, false),
		check("developer permission for developer", user("dev"), "developer", platform, true),
		check("developer permission for steward", user("stella"), "developer", platform, true),
		check("role relation is exact, never the permission", user("stella"), "role_developer", platform, false),
		check("global role relation", user("stella"), "role_steward", platform, true),
		check("librarian via librarian role", user("libby"), "librarian", platform, true),
		check("hub view steward", user("stella"), "view", res("client_hub", "org1"), true),
		check("hub view firmwide", user("fern"), "view", res("client_hub", "org2"), true),
		check("hub contribute firmwide", user("fern"), "contribute", res("client_hub", "org2"), true),
		check("hub view viewer", user("vin"), "view", res("client_hub", "org1"), true),
		check("hub contribute viewer denied", user("vin"), "contribute", res("client_hub", "org1"), false),
		check("hub contribute member", user("mo"), "contribute", res("client_hub", "org1"), true),
		check("hub member service account", bot, "contribute", res("client_hub", "org1"), true),
		check("hub viewer relation", user("vin"), "viewer", res("client_hub", "org1"), true),
		check("hub member relation denied", user("vin"), "member", res("client_hub", "org1"), false),
		{
			Name: "steward on a hub without a link tuple", Req: checkReq(user("stella"), "view", res("client_hub", "org3")),
			Want: Expect{Allowed: true}, Diverge: map[string]Expect{Authzed: {Allowed: false}, OpenFGA: {Allowed: false}},
		},
		check("section edit editor", user("ed"), "edit", res("section", "operations"), true),
		check("section edit steward", user("stella"), "edit", res("section", "operations"), true),
		check("section enter member", user("wes"), "enter", res("section", "workshop"), true),
		check("section edit member denied", user("wes"), "edit", res("section", "workshop"), false),
		check("section firmwide denied", user("fern"), "enter", res("section", "operations"), false),
		check("hyphenated section id", user("mo"), "enter", res("section", "data-operations"), true),
		check("page view", user("pv"), "view", res("page", "mailbox-scan"), true),
		check("ledger edit editor", user("led"), "edit", res("ledger", "main"), true),
		check("ledger has no steward bypass", user("stella"), "view", res("ledger", "main"), false),
		check("ledger view viewer", user("lev"), "view", res("ledger", "main"), true),
		checkErr("undeclared role relation", user("stella"), "role_janitor", platform, sdk.ErrInvalidInput),
	}
	f.Lookups = []LookupCase{
		lookup("member finds a hub without a link tuple", user("mo"), "view", "client_hub", 10, "org1", "org3"),
		{
			Name: "steward hub lookup: engine universal grant vs vendor link-tuple enumeration",
			Req:  lookupReq(user("stella"), "view", "client_hub", 10), Want: Expect{Err: sdk.ErrUnavailable},
			Diverge: map[string]Expect{Authzed: {IDs: []string{"org1", "org2"}}, OpenFGA: {IDs: []string{"org1", "org2"}}},
		},
		lookup("ledger lookup", user("lev"), "view", "ledger", 10, "main"),
	}
	return f
}
